// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// SupervisorDeps is what every syncer of the supervisor shares.
type SupervisorDeps struct {
	Store *store.Store
	// Token returns an access token for the account's mailbox.
	Token func(ctx context.Context, accountID string) (string, error)
	// Invalidate drops the account's cached token (optional).
	Invalidate func(accountID string)
	Notifier   api.Notifier
	Prefs      func() SyncPrefs
	Log        *slog.Logger
	// BaseURL and HTTP override the Graph endpoint (tests).
	BaseURL string
	HTTP    *http.Client
}

// Supervisor owns one Syncer per started account. It satisfies
// core.SyncSupervisor (asserted below without importing core).
type Supervisor struct {
	deps SupervisorDeps

	mu      sync.Mutex
	ctx     context.Context // set by Run; nil before
	stopped bool
	seq     int
	entries map[string]*entry
	queued  []store.Account // Start calls before Run
	wg      sync.WaitGroup
}

type entry struct {
	seq    int
	syncer *Syncer
	cancel context.CancelFunc
	done   chan struct{}
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
	return &Supervisor{deps: deps, entries: map[string]*entry{}}
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

// Start begins synchronising the account; a second Start for the same id
// is a no-op. Before Run the account is queued.
func (sv *Supervisor) Start(a store.Account) {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	if sv.stopped {
		return
	}
	if sv.ctx == nil {
		for _, q := range sv.queued {
			if q.ID == a.ID {
				return
			}
		}
		sv.queued = append(sv.queued, a)
		return
	}
	if _, running := sv.entries[a.ID]; running {
		return
	}
	id := a.ID
	deps := Deps{
		Store:    sv.deps.Store,
		Token:    func(ctx context.Context) (string, error) { return sv.deps.Token(ctx, id) },
		Notifier: sv.deps.Notifier,
		Prefs:    sv.deps.Prefs,
		Log:      sv.deps.Log,
		BaseURL:  sv.deps.BaseURL,
		HTTP:     sv.deps.HTTP,
	}
	if sv.deps.Invalidate != nil {
		deps.Invalidate = func() { sv.deps.Invalidate(id) }
	}
	syncer := NewSyncer(a, deps)
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
		sv.deps.Log.Debug("graph syncer stopped", "account", id, "after", time.Since(start), "err", err)
	}()
}

// Stop ends the account's syncer, waits for it and forgets its state.
func (sv *Supervisor) Stop(accountID string) {
	sv.mu.Lock()
	e, ok := sv.entries[accountID]
	if ok {
		delete(sv.entries, accountID)
	}
	sv.mu.Unlock()
	if !ok {
		return
	}
	e.cancel()
	<-e.done
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

// Trigger asks for a pass now; false when no syncer runs for the account.
func (sv *Supervisor) Trigger(accountID string, folder api.FolderID, full bool) bool {
	sv.mu.Lock()
	e, ok := sv.entries[accountID]
	sv.mu.Unlock()
	if !ok {
		return false
	}
	e.syncer.Trigger(folder, full)
	return true
}

// State returns the live state of a running syncer.
func (sv *Supervisor) State(accountID string) (api.SyncState, bool) {
	sv.mu.Lock()
	e, ok := sv.entries[accountID]
	sv.mu.Unlock()
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
