// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package core composes the daemon's internal packages into the api.Backend
// that malachid serves. It embeds rpc.StubBackend and overrides one service
// at a time as they become real; anything not overridden still answers
// notImplemented.
package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/schotek/malachi/backend/internal/auth"
	"github.com/schotek/malachi/backend/internal/auth/goa"
	"github.com/schotek/malachi/backend/internal/auth/oauth2flow"
	"github.com/schotek/malachi/backend/internal/board"
	"github.com/schotek/malachi/backend/internal/caldav"
	"github.com/schotek/malachi/backend/internal/calsync"
	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/contacts"
	"github.com/schotek/malachi/backend/internal/contacts/eds"
	"github.com/schotek/malachi/backend/internal/discover"
	"github.com/schotek/malachi/backend/internal/eway"
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

	// CalDAVHTTP carries every request to a CalDAV server (discovery,
	// listing, account.test, the calendar engine) when set; nil, in
	// production, is per-host transports with the TLS policy and the
	// account's pin (accounts_caldav.go). CalDAVResolver is the DNS of
	// account.discoverCalDAV (nil = the system resolver). Tests point them
	// at internal/caldav/caldavtest.
	CalDAVHTTP     *http.Client
	CalDAVResolver caldav.Resolver
	// CalendarGraphBaseURL and CalendarGraphHTTP override the Graph
	// endpoint of the calendar providers (tests; "" and nil = Microsoft
	// Graph with the client's defaults).
	CalendarGraphBaseURL string
	CalendarGraphHTTP    *http.Client

	// EwayHTTP carries every request to an eWay-CRM instance (the eway
	// supervisor's clients, account.detectEway, account.test,
	// account.add) when set; nil, in production, is the default
	// transport. Tests point it at internal/eway/ewaytest.
	EwayHTTP *http.Client
	// ewaySync is the eway kind's supervisor (clients, no syncer);
	// crm is the state of the crm.* methods (crm_service.go).
	ewaySync *eway.Supervisor
	crm      crmState

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
	// calSync is the calendar engine (internal/calsync): the caldav kind's
	// supervisor behind Supervisor and the calendar of Graph accounts
	// beside their mail (graphWithCalendar). calendarEngine returns it.
	calSync *calsync.Supervisor
	// graphLimiters are the request limiters of Graph accounts, one per
	// account shared by its mail syncer, outbox and calendar (D13), under
	// graphLimMu.
	graphLimMu    sync.Mutex
	graphLimiters map[string]*graph.Limiter
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
	// dn gathers notify.draftsChanged (drafts_notify.go).
	dn draftNotify
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
		graphLimiters:     map[string]*graph.Limiter{},
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
		Limiter:    b.graphLimiter,
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
	// The calendar engine: the caldav kind's supervisor, and the calendar
	// of a Graph account started and stopped with its mail.
	b.calSync = calsync.New(calsync.Options{
		Store:     st,
		Providers: b.calendarProvider,
		Notifier:  calendarNotifier{outboxAwareNotifier: notifier},
		Log:       log,
	})
	// eWay-CRM accounts synchronise nothing: the supervisor only holds
	// their clients (docs/eway.md D21).
	b.ewaySync = eway.NewSupervisor(eway.SupervisorDeps{
		Password:  b.PasswordFor,
		ClientID:  b.ewayClientID,
		HTTP:      b.ewayHTTP(),
		UserAgent: eway.UserAgent(version),
		Log:       log.With("component", "eway"),
	})
	b.Supervisor = newKindSupervisor(map[api.AccountKind]SyncSupervisor{
		api.AccountIMAP:   imapSync,
		api.AccountGraph:  &graphWithCalendar{mail: graphSync, cal: b.calSync},
		api.AccountJira:   b.jiraSync,
		api.AccountCalDAV: b.calSync.Mail(),
		api.AccountEway:   b.ewaySync,
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
		Limiter:    b.graphLimiter(id),
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
func (b *Backend) Messages() api.MessageService       { return mailOnlyMessages{&messageService{b}, b} }
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
	if err := b.backfillInvitations(ctx); err != nil && !isCancelled(err) {
		b.log.Warn("backfill invitations", "err", err)
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
	b.crmFollowSweep(ctx)
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
			b.crmFollowSweep(ctx)
		}
	}
}

// --- calendar engine (docs/calendar.md §3.8) --------------------------------

// calendarEngine is the calendar engine New installed: the providers and
// stored state behind calendar.*, event.* and invitation.* (T9b, T9c),
// whatever a test put in Supervisor. Reload it after the calendar
// preferences changed.
func (b *Backend) calendarEngine() *calsync.Supervisor { return b.calSync }

// graphLimiter is the request limiter of a Graph account, made on first
// use: its mail syncer, outbox and calendar share it (D13, 4 slots, the
// per-mailbox limit of Microsoft).
func (b *Backend) graphLimiter(accountID string) *graph.Limiter {
	b.graphLimMu.Lock()
	defer b.graphLimMu.Unlock()
	l, ok := b.graphLimiters[accountID]
	if !ok {
		l = graph.NewLimiter(4)
		b.graphLimiters[accountID] = l
	}
	return l
}

// dropGraphLimiter forgets a removed account's limiter.
func (b *Backend) dropGraphLimiter(accountID string) {
	b.graphLimMu.Lock()
	delete(b.graphLimiters, accountID)
	b.graphLimMu.Unlock()
}

// calendarNotifier is what the calendar engine emits into: notify.syncState
// and notify.authRequired of caldav accounts through the sync path
// (outboxAwareNotifier, the coalescer), and the calendar notifications
// (api.CalendarNotifier) to the notifier SetNotifier installed, when it
// has them.
type calendarNotifier struct {
	outboxAwareNotifier
}

var _ api.CalendarNotifier = calendarNotifier{}

func (n calendarNotifier) calendar() api.CalendarNotifier {
	cn, _ := n.b.getNotifier().(api.CalendarNotifier)
	return cn
}

func (n calendarNotifier) CalendarChanged(ev api.CalendarChangedNotification) {
	if cn := n.calendar(); cn != nil {
		cn.CalendarChanged(ev)
	}
}

func (n calendarNotifier) CalendarState(ev api.CalendarStateNotification) {
	if cn := n.calendar(); cn != nil {
		cn.CalendarState(ev)
	}
}

func (n calendarNotifier) EventAlert(ev api.EventAlertNotification) {
	if cn := n.calendar(); cn != nil {
		cn.EventAlert(ev)
	}
}

func (n calendarNotifier) InvitationsChanged(ev api.InvitationsChangedNotification) {
	if cn := n.calendar(); cn != nil {
		cn.InvitationsChanged(ev)
	}
}

// calendarProvider makes the calendar source of an account for the
// engine (calsync.Options.Providers): caldav.NewProvider over the
// account's server, or graph.NewCalendarProvider over the account's token
// and limiter once its sign-in allows the calendar. Errors are *api.Error
// and become the account's calendar state (calendarConsentRequired:
// consentRequired).
func (b *Backend) calendarProvider(ctx context.Context, a store.Account) (calsync.Provider, error) {
	switch a.Config.Protocol() {
	case api.AccountCalDAV:
		return b.calDAVProvider(ctx, a)
	case api.AccountGraph:
		return b.graphCalendarProvider(ctx, a)
	}
	return nil, api.NewError(api.CodeInvalidArgument, "account %q has no calendars", a.ID)
}

// calDAVProvider: the client trusts the server and the origins of the
// principal and home set the configuration names; the password is read
// from the keyring once per provider (the engine makes a new one after an
// authentication failure). iMIP of client scheduling leaves through the
// mail account of the configuration (sendCalDAVIMIP).
func (b *Backend) calDAVProvider(ctx context.Context, a store.Account) (calsync.Provider, error) {
	cd := a.Config.CalDAV
	if cd == nil {
		return nil, api.NewError(api.CodeInvalidArgument, "caldav account %q without caldav settings", a.ID)
	}
	var mu sync.Mutex
	var cached string
	password := func(ctx context.Context) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if cached != "" {
			return cached, nil
		}
		pw, err := b.PasswordFor(ctx, a.ID)
		if err != nil {
			return "", err
		}
		cached = pw
		return pw, nil
	}
	c, err := b.newCalDAVClient(*cd, calDAVConfiguredOrigins(*cd), password)
	if err != nil {
		return nil, err
	}
	addrs := []string{a.Config.Email}
	addrs = append(addrs, b.userAddresses(ctx)...)
	return caldav.NewProvider(c, caldav.ProviderOptions{
		Addresses:     addrs,
		Identity:      api.Address{Name: a.Config.DisplayName, Address: a.Config.Email},
		Scheduling:    cd.Scheduling,
		MailAccountID: cd.MailAccountID,
		Resources:     b.store,
		SendIMIP:      b.sendCalDAVIMIP,
		Log:           b.log,
	}), nil
}

// graphCalendarProvider: the account's sign-in must allow the calendar —
// the scope its grant recorded (source daemon, CalendarConsent) or the
// Calendar switch of its GNOME Online Accounts entry — else
// calendarConsentRequired. The client shares the account's token and
// limiter with its mail.
func (b *Backend) graphCalendarProvider(ctx context.Context, a store.Account) (calsync.Provider, error) {
	g := a.Config.Graph
	if g == nil {
		return nil, api.NewError(api.CodeInvalidArgument, "graph account %q without graph settings", a.ID)
	}
	signIn := g.Source == api.GraphSourceDaemon
	allowed := true
	switch g.Source {
	case api.GraphSourceDaemon:
		ok, err := b.CalendarConsent(ctx, a.ID)
		if err != nil {
			return nil, err
		}
		allowed = ok
	case api.GraphSourceGOA:
		disabled, err := b.goaCalendarDisabled(ctx, g.GOAAccountID)
		if err != nil {
			return nil, err
		}
		allowed = !disabled
	}
	if !allowed {
		e := api.NewError(api.CodeCalendarConsentRequired, "the account's sign-in does not allow the calendar")
		e.Data = api.CalendarConsentRequiredData{AccountID: api.AccountID(a.ID), SignIn: signIn}
		return nil, e
	}
	id := a.ID
	client := graph.NewClient(graph.Options{
		BaseURL:    b.CalendarGraphBaseURL,
		HTTP:       b.CalendarGraphHTTP,
		Token:      func(ctx context.Context) (string, error) { return b.GraphTokenFor(ctx, id) },
		Invalidate: func() { b.invalidateGraphTokenFor(id) },
		Log:        b.log,
		Limiter:    b.graphLimiter(id),
	})
	personal := false
	if tok, err := b.GraphTokenFor(ctx, id); err == nil {
		personal = graph.IsPersonalTenant(tokenTenant(tok))
	} else {
		return nil, err
	}
	me, err := b.boardOwn(ctx, a)
	if err != nil {
		me = []string{store.NormalizeAddress(a.Config.Email)}
	}
	return graph.NewCalendarProvider(client, graph.CalendarProviderOptions{
		AccountID: api.AccountID(id),
		SignIn:    signIn,
		Personal:  personal,
		Me:        me,
		Known:     b.graphKnown,
		Window:    func() calsync.Window { return b.calSync.Window(a) },
	}), nil
}

// graphKnown is what the store holds of a Graph calendar for the
// provider (graph.Known): the ETag of every object by remote id and the
// object of every stored occurrence.
func (b *Backend) graphKnown(ctx context.Context, cal store.Calendar) (graph.Known, error) {
	events, err := b.store.CalendarEvents(ctx, cal.ID)
	if err != nil {
		return graph.Known{}, err
	}
	k := graph.Known{Objects: map[string]string{}, Instances: map[string]string{}}
	byID := make(map[string]store.Event, len(events))
	for _, e := range events {
		byID[e.ID] = e
		if e.Kind != store.EventOverride && e.RemoteID != "" {
			k.Objects[e.RemoteID] = e.ETag
		}
	}
	list, err := b.store.EventRange(ctx, store.OccurrenceQuery{
		From: time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2200, 1, 1, 0, 0, 0, 0, time.UTC),
		CalendarIDs: []string{cal.ID}, IncludeHidden: true, IncludeDeclined: true,
		Limit: api.MaxEventsPerCalendar * 4,
	})
	if err != nil {
		return graph.Known{}, err
	}
	for _, o := range list.Occurrences {
		if o.RemoteID == "" {
			continue
		}
		e, ok := byID[o.EventID]
		if !ok {
			continue
		}
		if e.MasterID != "" {
			if m, ok := byID[e.MasterID]; ok {
				e = m
			}
		}
		if e.RemoteID != "" {
			k.Instances[o.RemoteID] = e.RemoteID
		}
	}
	return k, nil
}

// tokenTenant is the "tid" claim of a JWT access token, "" when the token
// is not a JWT (a personal account's token may be opaque: the provider
// then learns from getSchedule). Nothing is verified: the claim only
// chooses whether free/busy of others is offered.
func tokenTenant(tok string) string {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 || len(parts[1]) > 16<<10 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return ""
	}
	var claims struct {
		TID string `json:"tid"`
	}
	if json.Unmarshal(raw, &claims) != nil {
		return ""
	}
	return claims.TID
}

// userAddresses are the user's addresses on every enabled account (each
// account's own and the senders of its sent folders, boardOwn), lower
// case, at most board.MaxSelfAddresses: "me" of a calendar.
func (b *Backend) userAddresses(ctx context.Context) []string {
	accounts, err := b.store.ListAccounts(ctx)
	if err != nil {
		b.log.Warn("list accounts for the user's addresses", "err", err)
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, a := range accounts {
		if !a.Enabled {
			continue
		}
		own, err := b.boardOwn(ctx, a)
		if err != nil {
			b.log.Warn("the user's addresses of an account", "account", a.ID, "err", err)
			continue
		}
		for _, s := range own {
			if !seen[s] && len(out) < board.MaxSelfAddresses {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	return out
}

// dropCalendarCache deletes the cached calendar rows an account update no
// longer covers (D23): all of them when a Graph account's calendar sync
// went off, when a caldav account's server or user name changed, or when
// the account changed kind; the calendars newly in disabledCalendars
// otherwise. The account's calendar syncer is stopped first; the caller
// restarts it.
func (b *Backend) dropCalendarCache(ctx context.Context, old, next store.Account) {
	if b.calSync == nil {
		return
	}
	all, cals := calendarCacheDropped(old.Config, next.Config)
	if !all && len(cals) == 0 {
		return
	}
	b.calSync.Stop(old.ID)
	var changed []api.CalendarID
	if all {
		if err := b.store.DeleteAccountCalendars(ctx, old.ID); err != nil {
			b.log.Warn("delete account calendars", "account", old.ID, "err", err)
			return
		}
	} else {
		stored, err := b.store.ListCalendars(ctx, old.ID)
		if err != nil {
			b.log.Warn("list calendars", "account", old.ID, "err", err)
			return
		}
		for _, c := range stored {
			if slices.Contains(cals, c.RemoteID) {
				// The row stays (calendar.list shows it disabled); its contents go.
				if err := b.store.DeleteCalendarContents(ctx, c.ID); err != nil {
					b.log.Warn("delete calendar", "account", old.ID, "err", err)
					continue
				}
				// As the syncer marks it: enabling it again starts afresh.
				// The syncer may not have passed since the update, and a
				// row that kept its sync token would ask the server for
				// the changes since then, none: the events would not come back.
				if err := b.store.SetCalendarSyncState(ctx, c.ID, store.CalendarSyncState{SyncMode: calsync.SyncModeDisabled}); err != nil {
					b.log.Warn("reset calendar sync state", "account", old.ID, "err", err)
				}
				changed = append(changed, api.CalendarID(c.ID))
			}
		}
		if len(changed) == 0 {
			return
		}
	}
	b.log.Info("calendar cache dropped", "account", old.ID, "all", all, "calendars", len(changed))
	if cn, ok := b.getNotifier().(api.CalendarNotifier); ok {
		cn.CalendarChanged(api.CalendarChangedNotification{AccountIDs: []api.AccountID{api.AccountID(old.ID)}, CalendarIDs: changed})
	}
}

// calendarCacheDropped compares two configurations of an account: all
// its calendar rows go, or those of the remote ids newly disabled.
func calendarCacheDropped(old, next api.AccountConfig) (all bool, calendars []string) {
	had := old.Protocol() == api.AccountCalDAV || (old.Protocol() == api.AccountGraph && old.Calendar != nil && old.Calendar.Sync)
	if !had {
		return false, nil
	}
	switch {
	case old.Protocol() != next.Protocol():
		return true, nil
	case next.Protocol() == api.AccountGraph && (next.Calendar == nil || !next.Calendar.Sync):
		return true, nil
	case next.Protocol() == api.AccountCalDAV && !sameCalDAVLogin(old, next):
		return true, nil
	}
	var before []string
	if old.Calendar != nil {
		before = old.Calendar.DisabledCalendars
	}
	if next.Calendar != nil {
		for _, id := range next.Calendar.DisabledCalendars {
			if !slices.Contains(before, id) && !slices.Contains(calendars, id) {
				calendars = append(calendars, id)
			}
		}
	}
	return false, calendars
}
