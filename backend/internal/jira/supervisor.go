// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/ingest"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// SupervisorDeps is what every syncer of the supervisor shares.
type SupervisorDeps struct {
	Store *store.Store
	// Token returns the account's API token (cloud) or personal access
	// token (datacenter) from the keyring. It is asked once and cached per
	// account until the site refuses it or the account restarts; an
	// *api.Error (keyringError, authRequired) drives the state machine.
	Token    func(ctx context.Context, accountID string) (string, error)
	Notifier api.Notifier
	Log      *slog.Logger
	// Stored is told of every body stored, with the attachment policy it
	// was stored under (the role of graph.SupervisorDeps.Stored). nil =
	// nothing.
	Stored func(ctx context.Context, accountID, messageID string, pol ingest.Policy)
	// Now is the clock of the windows and of the unread rules (nil =
	// time.Now).
	Now func() time.Time
	// HTTP carries the requests (nil = the default transport; tests
	// inject the fake site's client).
	HTTP *http.Client
	// Prefs are the preferences read on every pass (nil = poll, keep
	// every attachment).
	Prefs func() SyncPrefs
	// Backoff overrides every retry delay and Sleep the client's wait on
	// a short Retry-After (tests; nil = the real ones).
	Backoff func(attempt int) time.Duration
	Sleep   func(ctx context.Context, d time.Duration) error
}

// Supervisor owns one Syncer per started account and one client per
// account (the Atlassian gateway route it found kept across restarts).
// It satisfies core.SyncSupervisor (asserted below without importing
// core) and adds TriggerIssue, RefreshIssueWait and FetchMessage.
type Supervisor struct {
	deps SupervisorDeps

	mu      sync.Mutex
	ctx     context.Context // set by Run; nil before
	stopped bool
	seq     int
	entries map[string]*entry
	queued  []store.Account // Start calls before Run
	clients map[string]*accountClient
	routes  map[string]bool // account id → the gateway route worked last
	wg      sync.WaitGroup
}

type entry struct {
	seq    int
	syncer *Syncer
	cancel context.CancelFunc
	done   chan struct{}
}

// accountClient is the client of one account and the token it caches.
type accountClient struct {
	key    string // the configuration it was built for
	client *Client
	remote Remote
	token  *tokenCache
}

// tokenCache asks the keyring once and keeps the token until invalidated.
type tokenCache struct {
	mu    sync.Mutex
	value string
	fetch func(ctx context.Context) (string, error)
}

func (t *tokenCache) get(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.value != "" {
		return t.value, nil
	}
	v, err := t.fetch(ctx)
	if err != nil {
		return "", err
	}
	t.value = v
	return v, nil
}

func (t *tokenCache) invalidate() {
	t.mu.Lock()
	t.value = ""
	t.mu.Unlock()
}

var _ interface {
	Run(ctx context.Context)
	Start(a store.Account)
	Stop(accountID string)
	Restart(a store.Account)
	Reload()
	Trigger(accountID string, folder api.FolderID, full bool) bool
	State(accountID string) (api.SyncState, bool)
	States() []api.SyncState
} = (*Supervisor)(nil)

// NewSupervisor prepares a supervisor; syncers start once Run is called.
func NewSupervisor(deps SupervisorDeps) *Supervisor {
	if deps.Log == nil {
		deps.Log = slog.New(slog.DiscardHandler)
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Supervisor{deps: deps, entries: map[string]*entry{}, clients: map[string]*accountClient{}, routes: map[string]bool{}}
}

// Run serves until ctx is cancelled, then stops every syncer and returns.
func (sv *Supervisor) Run(ctx context.Context) {
	sv.mu.Lock()
	sv.ctx = ctx
	queued := sv.queued
	sv.queued = nil
	sv.mu.Unlock()
	for _, a := range queued {
		sv.Start(a)
	}
	<-ctx.Done()
	sv.mu.Lock()
	sv.stopped = true
	entries := sv.entries
	sv.entries = map[string]*entry{}
	sv.mu.Unlock()
	for _, e := range entries {
		e.cancel()
	}
	for _, e := range entries {
		<-e.done
	}
	sv.wg.Wait()
}

// clientFor returns the account's client, building it when there is none
// or the site, deployment, cloud id or login changed. Callers hold no
// lock.
func (sv *Supervisor) clientFor(a store.Account) (*accountClient, error) {
	cfg := a.Config.Jira
	if cfg == nil {
		return nil, api.NewError(api.CodeInvalidArgument, "jira: the account has no jira configuration")
	}
	key := cfg.SiteURL + "\x00" + string(cfg.Deployment) + "\x00" + cfg.CloudID + "\x00" + cfg.Login
	sv.mu.Lock()
	defer sv.mu.Unlock()
	if ac := sv.clients[a.ID]; ac != nil && ac.key == key {
		return ac, nil
	}
	id := a.ID
	tc := &tokenCache{fetch: func(ctx context.Context) (string, error) {
		if sv.deps.Token == nil {
			return "", api.NewError(api.CodeAuthRequired, "jira: no token")
		}
		return sv.deps.Token(ctx, id)
	}}
	c, err := NewClient(Options{
		SiteURL: cfg.SiteURL, Deployment: cfg.Deployment, CloudID: cfg.CloudID, Login: cfg.Login,
		Token: tc.get, HTTP: sv.deps.HTTP, Log: sv.deps.Log.With("component", "jira", "account", id),
		Now: sv.deps.Now, Sleep: sv.deps.Sleep, Gateway: sv.routes[id+"\x00"+key],
	})
	if err != nil {
		return nil, err
	}
	ac := &accountClient{key: key, client: c, remote: NewRemote(c), token: tc}
	sv.clients[id] = ac
	return ac, nil
}

// forgetClient drops the account's client (and its cached token),
// remembering the route it used.
func (sv *Supervisor) forgetClient(accountID string) {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	if ac := sv.clients[accountID]; ac != nil {
		sv.routes[accountID+"\x00"+ac.key] = ac.client.Gateway()
		delete(sv.clients, accountID)
	}
}

// Start begins synchronising the account; a second Start for the same id
// is a no-op. Before Run the account is queued. An account whose
// configuration cannot make a client gets a syncer that only reports the
// error.
func (sv *Supervisor) Start(a store.Account) {
	sv.mu.Lock()
	if sv.stopped {
		sv.mu.Unlock()
		return
	}
	if sv.ctx == nil {
		for _, q := range sv.queued {
			if q.ID == a.ID {
				sv.mu.Unlock()
				return
			}
		}
		sv.queued = append(sv.queued, a)
		sv.mu.Unlock()
		return
	}
	if _, running := sv.entries[a.ID]; running {
		sv.mu.Unlock()
		return
	}
	sv.mu.Unlock()

	id := a.ID
	deps := Deps{
		Store: sv.deps.Store, Notifier: sv.deps.Notifier, Prefs: sv.deps.Prefs, Log: sv.deps.Log,
		Now: sv.deps.Now, Backoff: sv.deps.Backoff,
		Stored: func(ctx context.Context, messageID string, pol ingest.Policy) {
			if sv.deps.Stored != nil {
				sv.deps.Stored(ctx, id, messageID, pol)
			}
		},
	}
	ac, err := sv.clientFor(a)
	if err == nil {
		ac.token.invalidate() // the account may carry a new token
		deps.Remote, deps.Client, deps.InvalidateToken = ac.remote, ac.client, ac.token.invalidate
	}
	syncer := NewSyncer(a, deps)
	if err != nil {
		sv.deps.Log.Warn("jira account cannot synchronise", "account", id, "err", err)
		syncer.startErr = err // Run reports it
	}

	sv.mu.Lock()
	defer sv.mu.Unlock()
	if sv.stopped {
		return
	}
	if _, running := sv.entries[id]; running {
		return
	}
	ctx, cancel := context.WithCancel(sv.ctx)
	sv.seq++
	e := &entry{seq: sv.seq, syncer: syncer, cancel: cancel, done: make(chan struct{})}
	sv.entries[id] = e
	sv.wg.Add(1)
	go func() {
		defer sv.wg.Done()
		defer close(e.done)
		start := time.Now()
		err := syncer.Run(ctx)
		sv.deps.Log.Debug("jira syncer stopped", "account", id, "after", time.Since(start), "err", err)
	}()
}

// Stop ends the account's syncer, waits for it and forgets its state and
// its client.
func (sv *Supervisor) Stop(accountID string) {
	sv.mu.Lock()
	e, ok := sv.entries[accountID]
	if ok {
		delete(sv.entries, accountID)
	}
	sv.mu.Unlock()
	if ok {
		e.cancel()
		<-e.done
	}
	sv.forgetClient(accountID)
}

// Restart applies a changed configuration: Stop followed by Start.
func (sv *Supervisor) Restart(a store.Account) {
	sv.Stop(a.ID)
	sv.Start(a)
}

// Reload wakes every syncer so changed preferences apply at once.
func (sv *Supervisor) Reload() {
	for _, e := range sv.snapshot() {
		e.syncer.Wake()
	}
}

// Trigger asks for a pass now: every pass covers the whole account, so
// the folder does not matter; full enumerates the window again. false
// when no syncer runs for the account.
func (sv *Supervisor) Trigger(accountID string, folder api.FolderID, full bool) bool {
	e, ok := sv.entry(accountID)
	if !ok {
		return false
	}
	e.syncer.Trigger(full)
	return true
}

// TriggerIssue asks for a refresh of the issue with the key soon (triggers
// within triggerDebounce make one pass, at most triggerBudget such passes
// a minute). false when no syncer runs for the account or key is no issue
// key.
func (sv *Supervisor) TriggerIssue(accountID, key string) bool {
	e, ok := sv.entry(accountID)
	if !ok {
		return false
	}
	return e.syncer.TriggerIssue(key)
}

// RefreshIssueWait refreshes the issue with the key now (within the
// trigger budget) and waits for the pass that did, at most d (d <= 0: as
// long as ctx allows). Errors are *api.Error: unavailable when no syncer
// runs for the account, invalidArgument for a key that is no issue key,
// serverTimeout when d ran out, cancelled, or the failure of the pass.
func (sv *Supervisor) RefreshIssueWait(ctx context.Context, accountID, key string, d time.Duration) error {
	e, ok := sv.entry(accountID)
	if !ok {
		return api.NewError(api.CodeUnavailable, "jira: the account is not synchronising")
	}
	w := &refreshWaiter{done: make(chan error, 1)}
	if !e.syncer.queueIssue(key, w, 0) {
		return api.NewError(api.CodeInvalidArgument, "jira: not an issue key")
	}
	var timeout <-chan time.Time
	if d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		timeout = t.C
	}
	select {
	case err := <-w.done:
		if err != nil {
			return ToAPIError(err)
		}
		return nil
	case <-ctx.Done():
		return api.NewError(api.CodeCancelled, "cancelled")
	case <-timeout:
		return api.NewError(api.CodeServerTimeout, "jira: the issue was not refreshed in time")
	}
}

// State returns the live state of a running syncer.
func (sv *Supervisor) State(accountID string) (api.SyncState, bool) {
	e, ok := sv.entry(accountID)
	if !ok {
		return api.SyncState{}, false
	}
	return e.syncer.State(), true
}

// States returns every running syncer's state in start order.
func (sv *Supervisor) States() []api.SyncState {
	entries := sv.snapshot()
	out := make([]api.SyncState, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.syncer.State())
	}
	return out
}

func (sv *Supervisor) entry(accountID string) (*entry, bool) {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	e, ok := sv.entries[accountID]
	return e, ok
}

func (sv *Supervisor) snapshot() []*entry {
	sv.mu.Lock()
	entries := make([]*entry, 0, len(sv.entries))
	for _, e := range sv.entries {
		entries = append(entries, e)
	}
	sv.mu.Unlock()
	sort.Slice(entries, func(i, j int) bool { return entries[i].seq < entries[j].seq })
	return entries
}
