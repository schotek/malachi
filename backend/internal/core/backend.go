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
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/schotek/malachi/backend/internal/auth"
	"github.com/schotek/malachi/backend/internal/auth/goa"
	"github.com/schotek/malachi/backend/internal/auth/oauth2flow"
	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/contacts"
	"github.com/schotek/malachi/backend/internal/contacts/eds"
	"github.com/schotek/malachi/backend/internal/discover"
	"github.com/schotek/malachi/backend/internal/graph"
	"github.com/schotek/malachi/backend/internal/imap"
	"github.com/schotek/malachi/backend/internal/jira"
	"github.com/schotek/malachi/backend/internal/outbox"
	"github.com/schotek/malachi/backend/internal/remoteimg"
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

	// Sanitize is the HTML sanitiser behind message.body (view mode) and
	// draft.save (compose mode). It defaults to sanitize.Sanitize and is a
	// field so tests can substitute a fake.
	Sanitize func(sanitize.Input) (sanitize.Output, error)
	// FetchRemoteImages downloads the https: images of a message once the
	// remote-content policy allowed them, for inlining by the sanitiser. It
	// defaults to a remoteimg.Fetcher and is a field so tests never touch
	// the network.
	FetchRemoteImages func(ctx context.Context, urls []string) map[string]remoteimg.Image

	// LookupTXT resolves the DKIM key records of message.unsubscribe; nil
	// = the system resolver. OneClickPost sends the RFC 8058 request; nil
	// = internal/oneclick's strict client. Both are fields so tests never
	// touch the network.
	LookupTXT    func(ctx context.Context, domain string) ([]string, error)
	OneClickPost func(ctx context.Context, rawURL string) error

	// unsubBusy are the unsubscriptions in flight (account + key), under
	// unsubMu (unsubscribe.go).
	unsubMu   sync.Mutex
	unsubBusy map[string]bool

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

	// JiraHTTP carries every request to a Jira site (the jira supervisor,
	// account.detectSite, account.listSpaces, account.test, a rebuild for
	// message.download) when set; nil, in production, is the default
	// transport. Tests point it at a fake site (internal/jira/jiratest)
	// before StartSync.
	JiraHTTP *http.Client
	// DetectJiraSite, ListJiraSpaces and ProbeJira back account.detectSite,
	// account.listSpaces and the jira probe of account.test. They default
	// to internal/jira's over JiraHTTP and are fields so tests can
	// substitute fakes.
	DetectJiraSite func(ctx context.Context, rawURL string) (api.AccountDetectSiteResult, error)
	ListJiraSpaces func(ctx context.Context, cfg api.JiraConfig, token string, counts bool) (api.AccountListSpacesResult, error)
	ProbeJira      func(ctx context.Context, cfg api.JiraConfig, token string) (api.EndpointTestResult, error)

	// GOA is the GNOME Online Accounts client behind account.linked and the
	// tokens of Graph accounts. It connects lazily; without a session bus
	// every call reports unavailable. Tests substitute a fake.
	GOA GOAClient

	// OAuth runs the sign-in sessions of the backend's own OAuth2 flow
	// (account.oauthStart, and the re-sign-in of an account whose refresh
	// token was lost); OAuthClients resolves the client registration of a
	// source "daemon" account (the account's clientId, config.toml
	// [oauth2.*], a built-in one). New builds both from the configuration;
	// tests substitute ones that talk to a fake provider.
	OAuth        *oauth2flow.Manager
	OAuthClients *oauth2flow.Registry
	// TokenSourceDefaults is the template of every account's token source
	// (HTTP client, endpoints, clock); zero fields take oauth2flow's
	// defaults. Tests point it at a fake provider.
	TokenSourceDefaults oauth2flow.TokenSourceOptions

	// Directory is the system address-book client behind contact.search:
	// Evolution Data Server over D-Bus. It connects lazily; nil, no session
	// bus or no service leave the address-book part of a search simply
	// empty. Tests substitute a fake.
	Directory contacts.Directory

	// Supervisor runs one syncer per enabled account. New installs a
	// kind dispatcher over imap.NewSupervisor, the Graph supervisor and
	// the Jira one; tests replace it with a recording fake before
	// StartSync. The account and config services drive it.
	Supervisor SyncSupervisor
	// Delivery runs one outbox worker per enabled account, driven by the
	// same hooks as Supervisor. New installs outbox.NewSupervisor; tests
	// replace it with a recording fake.
	Delivery OutboxSupervisor
	// jiraSync is the Jira supervisor New installed behind Supervisor:
	// message.download rebuilds a Jira message through it
	// (jira.Supervisor.FetchMessage), whatever a test put in Supervisor.
	jiraSync *jira.Supervisor

	// syncNotifier is what the supervisors emit into (through
	// outboxAwareNotifier): the coalescer over forwardingNotifier, so events
	// reach whatever SetNotifier installed.
	syncNotifier *coalescingNotifier

	mu       sync.RWMutex
	notifier api.Notifier // nil until SetNotifier

	// oauthMu guards the token sources of source "daemon" accounts (made
	// on demand, dropped when the account changes or goes), what the
	// backend remembers about its sign-in sessions, and the per-account
	// secret locks. It is never held while calling into OAuth, a token
	// source or the keyring.
	oauthMu       sync.Mutex
	tokenSources  map[string]*oauth2flow.TokenSource
	oauthSessions map[string]oauthSession
	// secretLocks serialise, per account, the backend's keyring writes and
	// the configuration they belong to (account.add / update / remove and
	// a completed re-sign-in); see lockSecrets.
	secretLocks map[string]*secretLock
	closeOnce   sync.Once

	// draftTimers are the armed wake-ups of the syncers for their next
	// draft upload (scheduleDraftSync), per account, under draftMu.
	draftMu     sync.Mutex
	draftTimers map[string]*time.Timer

	// rawSteps are the background jobs of the raw maintenance loop
	// (raw_maintenance.go), in the order AddRawStep registered them.
	rawSteps []RawStep

	// dl is the state of message.download (download.go).
	dl downloadState
	// mem holds the messages message.download keeps in memory under
	// Preferences.NeverStoreAttachments (memcache.go).
	mem memCache

	// rawKick wakes the raw maintenance loop (kickRaw; capacity 1), and
	// rawNoSpace is set while the loop waits after a full disk.
	rawKick    chan struct{}
	rawNoSpace atomic.Bool
	// rawRestart names the steps the loop is to run from the start
	// although they are done for their key (restartRawStep), under
	// rawRestartMu.
	rawRestartMu sync.Mutex
	rawRestart   map[string]bool
	// runtimeDefaults are the preference defaults of this run
	// (SetRuntimeDefaults); nil until set.
	runtimeDefaults atomic.Pointer[RuntimeDefaults]
	// prefMu orders writing the preferences with applying them
	// (config.set, StartSync), so that what applies is what was written
	// last.
	prefMu sync.Mutex

	// im is the state of the notification mail of issue-tracker accounts
	// (issue_mail.go).
	im issueMail
	// board is the state of the board's worker (board_worker.go).
	board boardState
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
		StubBackend:       rpc.StubBackend{Version: version, StorePath: st.Path()},
		store:             st,
		defaults:          cfg,
		log:               log.With("component", "core"),
		Sanitize:          sanitize.Sanitize,
		Keyring:           auth.NotImplementedKeyring{},
		FetchRemoteImages: remoteimg.New(log).FetchAll,
		ProbeIMAP:         imap.Probe,
		ProbeSMTP:         smtp.Probe,
		ProbeGraph:        graph.Probe,
		GOA:               goa.New(log),
		Directory:         eds.New(log),
		OAuthClients:      &oauth2flow.Registry{Configured: oauthClientsFrom(cfg)},
		tokenSources:      map[string]*oauth2flow.TokenSource{},
		draftTimers:       map[string]*time.Timer{},
		oauthSessions:     map[string]oauthSession{},
		rawKick:           make(chan struct{}, 1),
	}
	b.im.init()
	b.board.init()
	b.DetectJiraSite = func(ctx context.Context, rawURL string) (api.AccountDetectSiteResult, error) {
		return jira.DetectSite(ctx, b.JiraHTTP, rawURL)
	}
	b.ListJiraSpaces = func(ctx context.Context, cfg api.JiraConfig, token string, counts bool) (api.AccountListSpacesResult, error) {
		return jira.ListSpaces(ctx, b.JiraHTTP, cfg, token, counts)
	}
	b.ProbeJira = func(ctx context.Context, cfg api.JiraConfig, token string) (api.EndpointTestResult, error) {
		return jira.Probe(ctx, b.JiraHTTP, cfg, token)
	}
	b.OAuth = oauth2flow.NewManager(oauth2flow.Options{
		Log:      log,
		Identity: map[oauth2flow.Provider]oauth2flow.IdentityFunc{oauth2flow.Microsoft: b.graphIdentity},
	})
	disc := discover.New(log)
	disc.GOA = b.goaAccountFor
	disc.GOAAvailable = b.goaAvailable
	b.Discover = disc.Discover
	b.syncNotifier = newCoalescingNotifier(forwardingNotifier{b}, b.log, nil)
	notifier := outboxAwareNotifier{b: b, inner: b.syncNotifier}
	// The real supervisors, one pair per account kind behind a dispatcher:
	// constructing them starts nothing (Run does), so tests may still
	// replace them with fakes before StartSync.
	imapSync := imap.NewSupervisor(imap.SupervisorDeps{
		Store:      st,
		Password:   b.credentialFor,
		AuthFailed: b.invalidateCredentialsFor,
		Notifier:   notifier,
		Prefs: func() imap.SyncPrefs {
			interval, days := b.SyncPrefs()
			pol := b.attachmentPolicy()
			return imap.SyncPrefs{IntervalSeconds: interval, OfflineDays: days,
				AttachmentOfflineDays: pol.AttachmentOfflineDays, NeverStoreAttachments: pol.NeverStore}
		},
		Log:        log,
		BuildDraft: b.buildDraft,
		DraftQuiet: draftSyncQuiet,
		Stored:     b.afterMailStored,
	})
	graphSync := graph.NewSupervisor(graph.SupervisorDeps{
		Store:      st,
		Token:      b.GraphTokenFor,
		Invalidate: b.invalidateGraphTokenFor,
		Notifier:   notifier,
		Prefs: func() graph.SyncPrefs {
			interval, days := b.SyncPrefs()
			pol := b.attachmentPolicy()
			return graph.SyncPrefs{IntervalSeconds: interval, OfflineDays: days,
				AttachmentOfflineDays: pol.AttachmentOfflineDays, NeverStoreAttachments: pol.NeverStore}
		},
		Log:        log,
		BuildDraft: b.buildDraft,
		DraftQuiet: draftSyncQuiet,
		Stored:     b.afterMailStored,
	})
	b.jiraSync = jira.NewSupervisor(jira.SupervisorDeps{
		Store:    st,
		Token:    b.PasswordFor,
		Notifier: notifier,
		Log:      log,
		Stored:   b.afterIssueStored,
		// JiraHTTP is read per request: tests set it after New.
		HTTP: &http.Client{Transport: jiraTransport{b}},
		Prefs: func() jira.SyncPrefs {
			interval, _ := b.SyncPrefs()
			pol := b.attachmentPolicy()
			return jira.SyncPrefs{IntervalSeconds: interval,
				AttachmentOfflineDays: pol.AttachmentOfflineDays, NeverStoreAttachments: pol.NeverStore}
		},
	})
	b.Supervisor = newKindSupervisor(map[api.AccountKind]SyncSupervisor{
		api.AccountIMAP: imapSync, api.AccountGraph: graphSync, api.AccountJira: b.jiraSync,
	})
	// Through b.Supervisor, not the values above: tests swap it.
	trigger := func(id string, f api.FolderID, full bool) bool { return b.Supervisor.Trigger(id, f, full) }
	imapOutbox := outbox.NewSupervisor(outbox.SupervisorDeps{
		Store:            st,
		Password:         b.credentialFor,
		AuthFailed:       b.invalidateCredentialsFor,
		Notifier:         notifier,
		FilesSentCopyFor: func(a store.Account) bool { return serverFilesSentCopy(a.Config) },
		Trigger:          trigger,
		Changed:          b.outboxChanged,
		Log:              log,
	})
	graphOutbox := outbox.NewSupervisor(outbox.SupervisorDeps{
		Store: st,
		// No password: the token is fetched inside the delivery function.
		Password:         func(context.Context, string) (string, error) { return "", nil },
		Notifier:         notifier,
		DeliverFor:       b.graphDeliverFor,
		FilesSentCopyFor: func(store.Account) bool { return true },
		Trigger:          trigger,
		Changed:          b.outboxChanged,
		Log:              log,
	})
	// An issue tracker's outbox holds comments, which the jira supervisor
	// posts to their issues; the comment then arrives with the issue's
	// refresh, so no Sent copy is kept (FilesSentCopy).
	jiraOutbox := outbox.NewSupervisor(outbox.SupervisorDeps{
		Store: st,
		// No password: the token is the jira supervisor's.
		Password:         func(context.Context, string) (string, error) { return "", nil },
		Notifier:         notifier,
		DeliverEntryFor:  func(store.Account) outbox.DeliverEntryFunc { return b.jiraSync.Deliver },
		FilesSentCopyFor: func(store.Account) bool { return true },
		Trigger:          trigger,
		Changed:          b.outboxChanged,
		Log:              log,
	})
	b.Delivery = newKindOutbox(map[api.AccountKind]OutboxSupervisor{
		api.AccountIMAP: imapOutbox, api.AccountGraph: graphOutbox, api.AccountJira: jiraOutbox,
	})
	// The raw maintenance steps in the order they run: the conversion to
	// the store's codec, then the attachments kept on the server only.
	b.AddRawStep(newCodecStep(b))
	b.AddRawStep(newAttachmentStep(b))
	return b
}

// jiraTransport carries the requests of the Jira supervisor through the
// transport of Backend.JiraHTTP when a test set one, else the default
// transport. It reads the field per request, since New builds the
// supervisor before a test can set it.
type jiraTransport struct{ b *Backend }

func (t jiraTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if c := t.b.JiraHTTP; c != nil && c.Transport != nil {
		return c.Transport.RoundTrip(r)
	}
	return http.DefaultTransport.RoundTrip(r)
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
	b.InvalidateGraphToken(accountID, a.Config)
}

// GraphTokenFor returns an access token for a Graph account's mailbox from
// the account's token source. Errors are *api.Error: authRequired when the
// user must sign in again (in the desktop's account settings, or through
// the backend's own flow, which then has a session waiting), unavailable
// without a session bus, oauthClientMissing without a client for the own
// flow, accountNotFound / invalidArgument for a bad account. The token is
// never logged.
func (b *Backend) GraphTokenFor(ctx context.Context, accountID string) (string, error) {
	a, err := b.requireAccount(ctx, accountID)
	if err != nil {
		return "", err
	}
	return b.graphToken(ctx, accountID, a.Config)
}

// graphToken fetches the token of a Graph account: from GNOME Online
// Accounts, or from the token source of the stored account accountID
// with the backend's own sign-in ("" = not stored: authRequired).
func (b *Backend) graphToken(ctx context.Context, accountID string, cfg api.AccountConfig) (string, error) {
	if cfg.Protocol() != api.AccountGraph || cfg.Graph == nil {
		return "", api.NewError(api.CodeInvalidArgument, "not a Microsoft Graph account")
	}
	switch cfg.Graph.Source {
	case api.GraphSourceDaemon:
		return b.daemonToken(ctx, accountID, cfg)
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

// InvalidateGraphToken drops the cached token of a Graph account after the
// service rejected it, so the next request asks the token source again.
func (b *Backend) InvalidateGraphToken(accountID string, cfg api.AccountConfig) {
	if cfg.Graph == nil {
		return
	}
	switch cfg.Graph.Source {
	case api.GraphSourceGOA:
		if b.GOA != nil {
			b.GOA.Invalidate(cfg.Graph.GOAAccountID)
		}
	case api.GraphSourceDaemon:
		b.invalidateTokenSource(accountID)
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
// it completes notify.syncState with pendingOutbox and failedOutbox,
// coalesces it (docs/api.md §5), delivers from its own goroutine so a slow
// RPC client never stalls a syncer, and forwards to the notifier installed
// by SetNotifier (dropping events before that).
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
// for every enabled account in the store, the worker that keeps the
// notification mail of issue-tracker accounts in step with them
// (issue_mail.go), and the board's worker (board_worker.go). Before that
// it stores the runtime defaults that have no preference yet
// (SetRuntimeDefaults) and sets the codec of new raw files. The returned
// channel is closed when both Run methods have returned and those workers
// have stopped, i.e. after ctx is cancelled and every syncer and worker
// has stopped.
func (b *Backend) StartSync(ctx context.Context) <-chan struct{} {
	b.applyStoredPreferences(ctx)
	done := make(chan struct{})
	issueMailDone := b.startIssueMail(ctx)
	boardDone := b.startBoard(ctx)
	var wg sync.WaitGroup
	wg.Add(4)
	go func() {
		defer wg.Done()
		<-issueMailDone
	}()
	go func() {
		defer wg.Done()
		<-boardDone
	}()
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
			b.scheduleDraftSync(a.ID)
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
func (b *Backend) Contacts() api.ContactService       { return &contactService{b} }
func (b *Backend) Drafts() api.DraftService           { return &draftService{b} }
func (b *Backend) Attachments() api.AttachmentService { return &attachmentService{b} }
func (b *Backend) Folders() api.FolderService         { return &folderService{b} }
func (b *Backend) Messages() api.MessageService       { return &messageService{b} }
func (b *Backend) Threads() api.ThreadService         { return &threadService{b} }
func (b *Backend) Search() api.SearchService          { return &searchService{b} }
func (b *Backend) Sync() api.SyncService              { return &syncService{b} }

// Maintain runs periodic housekeeping until ctx is cancelled: the one-off
// seeding of recipient completion, the upgrade passes that link, index and
// classify (bulk mail) the messages stored before threading, search and
// bulk mail existed, and the board's first evaluation (backfillBoard),
// then the raw
// maintenance loop (maintainRaw) beside the orphan attachment sweep and
// the evaluation of the notification mail of issue-tracker accounts
// (reevaluateIssueMail) and the board's upkeep (boardUpkeep) at start
// and hourly. It returns once all of it has stopped.
func (b *Backend) Maintain(ctx context.Context) {
	if err := b.backfillCollectedAddresses(ctx); err != nil {
		b.log.Warn("backfill collected addresses", "err", err)
	}
	if err := b.backfillThreads(ctx); err != nil && !isCancelled(err) {
		b.log.Warn("backfill conversations", "err", err)
	}
	if err := b.backfillSearch(ctx); err != nil && !isCancelled(err) {
		b.log.Warn("backfill search index", "err", err)
	}
	if err := b.backfillBulk(ctx); err != nil && !isCancelled(err) {
		b.log.Warn("backfill bulk classification", "err", err)
	}
	if err := b.backfillBoard(ctx); err != nil && !isCancelled(err) {
		b.log.Warn("backfill board", "err", err)
	}
	// The raw maintenance loop runs beside the attachment sweep; Maintain
	// returns once it has stopped too.
	rawDone := make(chan struct{})
	go func() {
		defer close(rawDone)
		b.maintainRaw(ctx)
	}()
	defer func() { <-rawDone }()
	sweep := func() {
		n, err := b.store.SweepAttachments(ctx, attachmentSweepAge)
		if err != nil {
			b.log.Warn("attachment sweep", "err", err)
		} else if n > 0 {
			b.log.Info("attachment sweep", "removed", n)
		}
	}
	sweep()
	b.reevaluateIssueMail(ctx)
	b.boardUpkeep(ctx)
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sweep()
			b.reevaluateIssueMail(ctx)
			b.boardUpkeep(ctx)
		}
	}
}
