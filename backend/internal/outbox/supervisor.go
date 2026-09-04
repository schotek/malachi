// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package outbox

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// SupervisorDeps is what every worker of the supervisor shares. See Deps
// for the meaning of the fields; Password takes the account id here.
type SupervisorDeps struct {
	Store    *store.Store
	Password func(ctx context.Context, accountID string) (string, error)
	Notifier api.Notifier
	Deliver  DeliverFunc
	Trigger  func(accountID string, folder api.FolderID, full bool) bool
	Changed  func(accountID string)
	Log      *slog.Logger
}

// Supervisor owns one Worker per started account. It satisfies
// core.OutboxSupervisor (asserted below without importing core).
type Supervisor struct {
	deps SupervisorDeps

	mu      sync.Mutex
	ctx     context.Context // set by Run; nil before
	stopped bool
	entries map[string]*entry
	queued  []store.Account // Start calls before Run
	wg      sync.WaitGroup
}

type entry struct {
	worker *Worker
	cancel context.CancelFunc
	done   chan struct{}
}

var _ interface {
	Run(ctx context.Context)
	Start(a store.Account)
	Stop(accountID string)
	Restart(a store.Account)
	Wake(accountID string) bool
} = (*Supervisor)(nil)

// NewSupervisor prepares a supervisor; workers start once Run is called.
func NewSupervisor(deps SupervisorDeps) *Supervisor {
	if deps.Log == nil {
		deps.Log = slog.New(slog.DiscardHandler)
	}
	return &Supervisor{deps: deps, entries: map[string]*entry{}}
}

// Run serves until ctx is cancelled, then stops every worker and returns.
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

// Start begins delivering for the account; a second Start for the same id
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
	worker := NewWorker(a, Deps{
		Store:    sv.deps.Store,
		Password: func(ctx context.Context) (string, error) { return sv.deps.Password(ctx, id) },
		Notifier: sv.deps.Notifier,
		Deliver:  sv.deps.Deliver,
		Trigger:  sv.deps.Trigger,
		Changed:  sv.deps.Changed,
		Log:      sv.deps.Log,
	})
	ctx, cancel := context.WithCancel(sv.ctx)
	e := &entry{worker: worker, cancel: cancel, done: make(chan struct{})}
	sv.entries[id] = e
	sv.wg.Add(1)
	go func() {
		defer sv.wg.Done()
		defer close(e.done)
		start := time.Now()
		err := worker.Run(ctx)
		sv.deps.Log.Debug("outbox worker stopped", "account", id, "after", time.Since(start), "err", err)
	}()
}

// Stop ends the account's worker and waits for it. Cancelling the context
// closes an SMTP session in progress, so the wait is short.
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

// Restart applies a changed configuration: Stop followed by Start. The new
// worker's ResetOutbox makes every deferred message due at once, which is
// how account.update retries after an authentication failure.
func (sv *Supervisor) Restart(a store.Account) {
	sv.Stop(a.ID)
	sv.Start(a)
}

// Wake asks the account's worker to look at the queue now; false when no
// worker runs for the account (the message waits until it is started).
func (sv *Supervisor) Wake(accountID string) bool {
	sv.mu.Lock()
	e, ok := sv.entries[accountID]
	sv.mu.Unlock()
	if !ok {
		return false
	}
	e.worker.Wake()
	return true
}
