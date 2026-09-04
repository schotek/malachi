// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// SyncSupervisor owns one syncer per enabled account. internal/imap
// provides the real implementation; core only drives its lifecycle from
// the account and config services and reads its state for sync.status.
//
// Every method is safe to call from any goroutine and must return
// promptly (Stop may wait for the syncer's current command, bounded by the
// command timeout). Stop, Restart, Trigger and State on an unknown account
// are no-ops (Trigger returns false, State returns ok == false).
type SyncSupervisor interface {
	// Run serves the supervisor until ctx is cancelled, then stops every
	// syncer and returns.
	Run(ctx context.Context)
	// Start begins synchronising the account; a second Start for the same
	// id is a no-op.
	Start(a store.Account)
	// Stop ends the account's syncer and forgets its state.
	Stop(accountID string)
	// Restart applies a changed configuration: Stop followed by Start.
	Restart(a store.Account)
	// Reload tells every syncer that the preferences (interval, retention
	// window) changed.
	Reload()
	// Trigger asks for a pass now: one folder or (empty) the whole account,
	// incremental or full. It reports whether a syncer for the account exists.
	Trigger(accountID string, folder api.FolderID, full bool) bool
	// State returns the live state of a running syncer.
	State(accountID string) (api.SyncState, bool)
	// States returns the live state of every running syncer.
	States() []api.SyncState
}

// noopSupervisor is the default until the real supervisor is wired in: no
// account is ever synchronised and every state is "idle".
type noopSupervisor struct{}

func (noopSupervisor) Run(context.Context)                     {}
func (noopSupervisor) Start(store.Account)                     {}
func (noopSupervisor) Stop(string)                             {}
func (noopSupervisor) Restart(store.Account)                   {}
func (noopSupervisor) Reload()                                 {}
func (noopSupervisor) Trigger(string, api.FolderID, bool) bool { return false }
func (noopSupervisor) State(string) (api.SyncState, bool)      { return api.SyncState{}, false }
func (noopSupervisor) States() []api.SyncState                 { return nil }

var _ SyncSupervisor = noopSupervisor{}

// OutboxSupervisor owns one outbox worker per enabled account.
// internal/outbox provides the real implementation; core drives its
// lifecycle next to the SyncSupervisor's and wakes it after message.send
// and outbox.retry. Every method is safe from any goroutine; Stop waits
// for the worker (an SMTP session in progress is cut short); Stop,
// Restart and Wake on an unknown account are no-ops (Wake returns false).
type OutboxSupervisor interface {
	// Run serves until ctx is cancelled, then stops every worker.
	Run(ctx context.Context)
	// Start begins delivering the account's outbox; a second Start is a
	// no-op.
	Start(a store.Account)
	// Stop ends the account's worker.
	Stop(accountID string)
	// Restart applies a changed configuration (Stop, then Start); the new
	// worker retries every deferred message at once.
	Restart(a store.Account)
	// Wake asks the account's worker to look at the queue now; false when
	// no worker runs for the account.
	Wake(accountID string) bool
}

// noopOutbox is the default until a real supervisor is wired in: messages
// queue up and are never delivered.
type noopOutbox struct{}

func (noopOutbox) Run(context.Context)   {}
func (noopOutbox) Start(store.Account)   {}
func (noopOutbox) Stop(string)           {}
func (noopOutbox) Restart(store.Account) {}
func (noopOutbox) Wake(string) bool      { return false }

var _ OutboxSupervisor = noopOutbox{}

type syncService struct{ b *Backend }

// Status merges the account registry with the live syncer states: every
// account in account.list order, paused ones as "disabled", running ones
// with whatever the syncer reports, the rest idle.
func (s *syncService) Status(ctx context.Context, p api.SyncStatusParams) (*api.SyncStatusResult, error) {
	if p.AccountID != "" {
		a, err := s.b.requireAccount(ctx, string(p.AccountID))
		if err != nil {
			return nil, err
		}
		return &api.SyncStatusResult{Accounts: []api.SyncState{s.b.stateFor(a)}}, nil
	}
	accounts, err := s.b.store.ListAccounts(ctx)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	out := make([]api.SyncState, 0, len(accounts))
	for _, a := range accounts {
		out = append(out, s.b.stateFor(a))
	}
	return &api.SyncStatusResult{Accounts: out}, nil
}

// Trigger asks for a pass now. A paused account is skipped silently so
// "sync everything" never fails because of one paused account.
func (s *syncService) Trigger(ctx context.Context, p api.SyncTriggerParams) (*api.SyncTriggerResult, error) {
	if p.FolderID != "" && p.AccountID == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "folderId requires accountId")
	}
	if p.AccountID != "" {
		a, err := s.b.requireAccount(ctx, string(p.AccountID))
		if err != nil {
			return nil, err
		}
		if p.FolderID != "" {
			if _, err := s.b.store.GetFolder(ctx, a.ID, string(p.FolderID)); err != nil {
				if errors.Is(err, store.ErrNotFound) {
					return nil, api.NewError(api.CodeFolderNotFound, "unknown folder %q", p.FolderID)
				}
				return nil, api.NewError(api.CodeStorageError, "%v", err)
			}
		}
		if !a.Enabled {
			return &api.SyncTriggerResult{}, nil
		}
		if !s.b.Supervisor.Trigger(a.ID, p.FolderID, p.Full) {
			s.b.log.Debug("sync trigger without a running syncer", "id", a.ID)
		}
		return &api.SyncTriggerResult{}, nil
	}
	accounts, err := s.b.store.ListAccounts(ctx)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	for _, a := range accounts {
		if a.Enabled {
			s.b.Supervisor.Trigger(a.ID, "", p.Full)
		}
	}
	return &api.SyncTriggerResult{}, nil
}

// stateFor is the SyncState of an account as account.list and sync.status
// report it: "disabled" for a paused account, the syncer's live state when
// one runs, otherwise idle with unknown progress.
func (b *Backend) stateFor(a store.Account) api.SyncState {
	id := api.AccountID(a.ID)
	st := api.SyncState{AccountID: id, Status: api.SyncIdle, Progress: -1}
	switch live, ok := b.Supervisor.State(a.ID); {
	case !a.Enabled:
		st.Status = api.SyncDisabled
	case ok:
		st = live
		st.AccountID = id
	}
	st.PendingOutbox = b.pendingOutbox(a.ID)
	return st
}

// pendingOutbox is SyncState.pendingOutbox: the account's outbox messages
// still to be delivered. A store failure is logged and reads as 0 rather
// than failing the caller (a status report or a notification).
func (b *Backend) pendingOutbox(accountID string) int {
	n, err := b.store.CountOutbox(context.Background(), accountID)
	if err != nil {
		b.log.Warn("count outbox", "account", accountID, "err", err)
		return 0
	}
	return n
}

// outboxChanged re-emits the account's SyncState after an outbox change
// (a message queued, delivered, failed or deleted). The coalescer drops
// it unless pendingOutbox (or anything else) actually changed.
func (b *Backend) outboxChanged(accountID string) {
	a, err := b.store.GetAccount(context.Background(), accountID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			b.log.Warn("outbox change for unreadable account", "account", accountID, "err", err)
		}
		return
	}
	b.syncNotifier.SyncState(api.SyncStateNotification{State: b.stateFor(a)})
}

// requireAccount resolves an account id from a request: invalidArgument
// when empty, accountNotFound when unknown, storageError otherwise.
func (b *Backend) requireAccount(ctx context.Context, id string) (store.Account, error) {
	if id == "" {
		return store.Account{}, api.NewError(api.CodeInvalidArgument, "accountId is required")
	}
	a, err := b.store.GetAccount(ctx, id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return store.Account{}, api.NewError(api.CodeAccountNotFound, "unknown account %q", id)
	case err != nil:
		return store.Account{}, api.NewError(api.CodeStorageError, "%v", err)
	}
	return a, nil
}
