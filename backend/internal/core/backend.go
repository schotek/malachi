// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package core composes the daemon's internal packages into the api.Backend
// that malachid serves. It embeds rpc.StubBackend and overrides one service
// at a time as they become real; anything not overridden still answers
// notImplemented.
package core

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/auth"
	"github.com/schotek/malachi/backend/internal/auth/goa"
	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/discover"
	"github.com/schotek/malachi/backend/internal/graph"
	"github.com/schotek/malachi/backend/internal/imap"
	"github.com/schotek/malachi/backend/internal/outbox"
	"github.com/schotek/malachi/backend/internal/rpc"
	"github.com/schotek/malachi/backend/internal/sanitize"
	"github.com/schotek/malachi/backend/internal/smtp"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// attachmentSweepAge is how long an imported attachment may stay unbound
// (never listed by a draft.save) before the sweep deletes it.
const attachmentSweepAge = 24 * time.Hour

// Backend is the production api.Backend.
type Backend struct {
	rpc.StubBackend

	store    *store.Store
	defaults config.Config // config.toml as loaded at startup (bootstrap defaults)
	log      *slog.Logger

	// Sanitize is the HTML sanitiser used for composed bodies. It defaults
	// to sanitize.Sanitize and is a field so tests can substitute a fake.
	Sanitize func(sanitize.Input) (sanitize.Output, error)

	// Keyring stores account secrets. It defaults to the not-implemented
	// placeholder and is a field so tests can substitute an in-memory one.
	Keyring auth.Keyring

	// ProbeIMAP, ProbeSMTP and ProbeGraph back account.test. They default
	// to the real probes and are fields so tests can substitute fakes.
	ProbeIMAP  func(ctx context.Context, cfg api.ServerConfig, password string) (imap.ProbeResult, error)
	ProbeSMTP  func(ctx context.Context, cfg api.ServerConfig, password string) (smtp.ProbeResult, error)
	ProbeGraph func(ctx context.Context, token string) (graph.ProbeResult, error)
	// Discover backs account.discover; a field for the same reason.
	Discover func(ctx context.Context, email string) (discover.Result, error)

	// GOA is the GNOME Online Accounts client behind account.linked and the
	// tokens of Graph accounts. It connects lazily; without a session bus
	// every call reports unavailable. Tests substitute a fake.
	GOA GOAClient

	// Supervisor runs one syncer per enabled account. New installs a
	// kind dispatcher over imap.NewSupervisor (and the Graph supervisor);
	// tests replace it with a recording fake before StartSync. The account
	// and config services drive it.
	Supervisor SyncSupervisor
	// Delivery runs one outbox worker per enabled account, driven by the
	// same hooks as Supervisor. New installs outbox.NewSupervisor; tests
	// replace it with a recording fake.
	Delivery OutboxSupervisor

	// syncNotifier is what the supervisors emit into (through
	// outboxAwareNotifier): the coalescer over forwardingNotifier, so events
	// reach whatever SetNotifier installed.
	syncNotifier *coalescingNotifier

	mu       sync.RWMutex
	notifier api.Notifier // nil until SetNotifier
}

var _ api.Backend = (*Backend)(nil)

// GOAClient is the slice of goa.Client the backend uses; tests substitute
// a fake.
type GOAClient interface {
	Accounts(ctx context.Context) ([]goa.Account, error)
	AccessToken(ctx context.Context, id string) (string, time.Time, error)
	Invalidate(id string)
}

// New builds the backend over an open store. cfg supplies the defaults for
// preferences that have not been set through config.set.
func New(version string, st *store.Store, cfg config.Config, log *slog.Logger) *Backend {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	b := &Backend{
		StubBackend: rpc.StubBackend{Version: version, StorePath: st.Path()},
		store:       st,
		defaults:    cfg,
		log:         log.With("component", "core"),
		Sanitize:    sanitize.Sanitize,
		Keyring:     auth.NotImplementedKeyring{},
		ProbeIMAP:   imap.Probe,
		ProbeSMTP:   smtp.Probe,
		ProbeGraph:  graph.Probe,
		GOA:         goa.New(log),
	}
	disc := discover.New(log)
	disc.GOA = b.goaAccountFor
	b.Discover = disc.Discover
	b.syncNotifier = newCoalescingNotifier(forwardingNotifier{b}, b.log, nil)
	notifier := outboxAwareNotifier{b: b, inner: b.syncNotifier}
	// The real supervisors, one pair per account kind behind a dispatcher:
	// constructing them starts nothing (Run does), so tests may still
	// replace them with fakes before StartSync.
	imapSync := imap.NewSupervisor(imap.SupervisorDeps{
		Store:    st,
		Password: b.PasswordFor,
		Notifier: notifier,
		Prefs: func() imap.SyncPrefs {
			interval, days := b.SyncPrefs()
			return imap.SyncPrefs{IntervalSeconds: interval, OfflineDays: days}
		},
		Log: log,
	})
	graphSync := graph.NewSupervisor(graph.SupervisorDeps{
		Store:      st,
		Token:      b.GraphTokenFor,
		Invalidate: b.invalidateGraphTokenFor,
		Notifier:   notifier,
		Prefs: func() graph.SyncPrefs {
			interval, days := b.SyncPrefs()
			return graph.SyncPrefs{IntervalSeconds: interval, OfflineDays: days}
		},
		Log: log,
	})
	b.Supervisor = newKindSupervisor(imapSync, graphSync)
	// Through b.Supervisor, not the values above: tests swap it.
	trigger := func(id string, f api.FolderID, full bool) bool { return b.Supervisor.Trigger(id, f, full) }
	imapOutbox := outbox.NewSupervisor(outbox.SupervisorDeps{
		Store:    st,
		Password: b.PasswordFor,
		Notifier: notifier,
		Trigger:  trigger,
		Changed:  b.outboxChanged,
		Log:      log,
	})
	graphOutbox := outbox.NewSupervisor(outbox.SupervisorDeps{
		Store: st,
		// No password: the token is fetched inside the delivery function.
		Password:      func(context.Context, string) (string, error) { return "", nil },
		Notifier:      notifier,
		DeliverFor:    b.graphDeliverFor,
		FilesSentCopy: true,
		Trigger:       trigger,
		Changed:       b.outboxChanged,
		Log:           log,
	})
	b.Delivery = newKindOutbox(imapOutbox, graphOutbox)
	return b
}

// graphDeliverFor is the outbox delivery function of a Graph account: it
// submits the message through sendMail with the account's token; the
// SMTP settings and password of the DeliverFunc signature are unused.
func (b *Backend) graphDeliverFor(a store.Account) outbox.DeliverFunc {
	id := a.ID
	client := graph.NewClient(graph.Options{
		Token:      func(ctx context.Context) (string, error) { return b.GraphTokenFor(ctx, id) },
		Invalidate: func() { b.invalidateGraphTokenFor(id) },
		Log:        b.log,
	})
	return func(ctx context.Context, _ api.ServerConfig, _ string, from string, rcpts []string, r io.Reader, size int64) error {
		return graph.Deliver(ctx, client, from, rcpts, r, size)
	}
}

// invalidateGraphTokenFor drops the cached token of an account after the
// service rejected it.
func (b *Backend) invalidateGraphTokenFor(accountID string) {
	a, err := b.store.GetAccount(context.Background(), accountID)
	if err != nil {
		return
	}
	b.InvalidateGraphToken(a.Config)
}

// GraphTokenFor returns an access token for a Graph account's mailbox from
// the account's token source. Errors are *api.Error: authRequired when the
// user must sign in again in the desktop's account settings, unavailable
// without a session bus, accountNotFound / invalidArgument for a bad
// account. The token is never logged.
func (b *Backend) GraphTokenFor(ctx context.Context, accountID string) (string, error) {
	a, err := b.requireAccount(ctx, accountID)
	if err != nil {
		return "", err
	}
	return b.graphToken(ctx, a.Config)
}

func (b *Backend) graphToken(ctx context.Context, cfg api.AccountConfig) (string, error) {
	if cfg.Protocol() != api.AccountGraph || cfg.Graph == nil {
		return "", api.NewError(api.CodeInvalidArgument, "not a Microsoft Graph account")
	}
	switch cfg.Graph.Source {
	case api.GraphSourceGOA:
		if b.GOA == nil {
			return "", api.NewError(api.CodeUnavailable, "GNOME Online Accounts is not available")
		}
		token, _, err := b.GOA.AccessToken(ctx, cfg.Graph.GOAAccountID)
		if err != nil {
			var apiErr *api.Error
			if errors.As(err, &apiErr) {
				return "", apiErr
			}
			return "", api.NewError(api.CodeServerError, "%v", err)
		}
		return token, nil
	default:
		return "", api.NewError(api.CodeInvalidArgument, "unsupported graph token source %q", cfg.Graph.Source)
	}
}

// goaAccountFor finds the address among the Microsoft 365 accounts of
// GNOME Online Accounts (mail enabled, OAuth2), for account.discover. A
// missing or failing GOA is simply no match.
func (b *Backend) goaAccountFor(ctx context.Context, email string) (string, bool) {
	if b.GOA == nil {
		return "", false
	}
	accounts, err := b.GOA.Accounts(ctx)
	if err != nil {
		b.log.Debug("gnome online accounts lookup", "err", err)
		return "", false
	}
	want := store.NormalizeAddress(email)
	for _, a := range accounts {
		if a.ProviderType != goa.ProviderMicrosoft365 || !a.OAuth2 || a.MailDisabled {
			continue
		}
		if store.NormalizeAddress(a.Email) == want || (a.Email == "" && store.NormalizeAddress(a.Identity) == want) {
			return a.ID, true
		}
	}
	return "", false
}

// InvalidateGraphToken drops the cached token of a Graph account after the
// service rejected it, so the next request asks the token source again.
func (b *Backend) InvalidateGraphToken(cfg api.AccountConfig) {
	if cfg.Graph != nil && cfg.Graph.Source == api.GraphSourceGOA && b.GOA != nil {
		b.GOA.Invalidate(cfg.Graph.GOAAccountID)
	}
}

// SetNotifier wires the RPC server so that services can push notifications.
// malachid calls it right after rpc.NewServer, before Listen. Without a
// notifier, notifications are dropped.
func (b *Backend) SetNotifier(n api.Notifier) {
	b.mu.Lock()
	b.notifier = n
	b.mu.Unlock()
}

func (b *Backend) getNotifier() api.Notifier {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.notifier
}

// SyncNotifier is the api.Notifier the sync supervisor should emit into:
// it completes notify.syncState with pendingOutbox, coalesces it
// (docs/api.md §5), delivers from its own goroutine so a slow RPC client
// never stalls a syncer, and forwards to the notifier installed by
// SetNotifier (dropping events before that).
func (b *Backend) SyncNotifier() api.Notifier {
	return outboxAwareNotifier{b: b, inner: b.syncNotifier}
}

// PasswordFor reads an account's stored password from the keyring for the
// sync engine and account.test. A missing entry is authRequired, an unknown
// account accountNotFound; the keyring's own *api.Error passes through and
// anything else is keyringError. The password is never logged.
func (b *Backend) PasswordFor(ctx context.Context, accountID string) (string, error) {
	if _, err := b.requireAccount(ctx, accountID); err != nil {
		return "", err
	}
	pw, err := b.Keyring.Get(ctx, api.AccountID(accountID), auth.KeyPassword)
	switch {
	case errors.Is(err, auth.ErrNoSecret):
		return "", api.NewError(api.CodeAuthRequired, "no stored password for account %q", accountID)
	case err != nil:
		var apiErr *api.Error
		if errors.As(err, &apiErr) {
			return "", apiErr
		}
		return "", api.NewError(api.CodeKeyringError, "%v", err)
	}
	return pw, nil
}

// SyncPrefs returns the effective sync interval and retention window for
// the supervisor; when the store cannot be read the config.toml defaults
// apply (logged).
func (b *Backend) SyncPrefs() (intervalSeconds, offlineDays int) {
	p, err := b.preferences(context.Background())
	if err != nil {
		b.log.Warn("read sync preferences", "err", err)
		return b.defaults.Sync.IntervalSeconds, b.defaults.Sync.OfflineDays
	}
	return p.SyncIntervalSeconds, p.OfflineDays
}

// StartSync runs both supervisors and starts a syncer and an outbox worker
// for every enabled account in the store. The returned channel is closed
// when both Run methods have returned, i.e. after ctx is cancelled and
// every syncer and worker has stopped.
func (b *Backend) StartSync(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		b.Supervisor.Run(ctx)
	}()
	go func() {
		defer wg.Done()
		b.Delivery.Run(ctx)
	}()
	go func() {
		wg.Wait()
		close(done)
	}()
	accounts, err := b.store.ListAccounts(ctx)
	if err != nil {
		b.log.Error("list accounts for sync", "err", err)
		return done
	}
	started := 0
	for _, a := range accounts {
		if a.Enabled {
			b.Supervisor.Start(a)
			b.Delivery.Start(a)
			started++
		}
	}
	b.log.Info("sync started", "accounts", started)
	return done
}

func (b *Backend) Accounts() api.AccountService       { return &accountService{b} }
func (b *Backend) Outbox() api.OutboxService          { return &outboxService{b} }
func (b *Backend) Config() api.ConfigService          { return &configService{b} }
func (b *Backend) Senders() api.SenderService         { return &senderService{b} }
func (b *Backend) Drafts() api.DraftService           { return &draftService{b} }
func (b *Backend) Attachments() api.AttachmentService { return &attachmentService{b} }
func (b *Backend) Folders() api.FolderService         { return &folderService{b} }
func (b *Backend) Messages() api.MessageService       { return &messageService{b} }
func (b *Backend) Sync() api.SyncService              { return &syncService{b} }

// Maintain runs periodic housekeeping until ctx is cancelled: the orphan
// attachment sweep at start and then hourly.
func (b *Backend) Maintain(ctx context.Context) {
	sweep := func() {
		n, err := b.store.SweepAttachments(ctx, attachmentSweepAge)
		if err != nil {
			b.log.Warn("attachment sweep", "err", err)
		} else if n > 0 {
			b.log.Info("attachment sweep", "removed", n)
		}
	}
	sweep()
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sweep()
		}
	}
}
