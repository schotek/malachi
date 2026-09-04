// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"sync"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// kindSupervisor routes every SyncSupervisor call to the supervisor of the
// account's kind (imap or graph). It remembers the kind of each started
// account, because Stop, Trigger and State only carry the id; an id it has
// not seen is a no-op, as the interface promises.
type kindSupervisor struct {
	IMAP  SyncSupervisor
	Graph SyncSupervisor

	mu    sync.Mutex
	kinds map[string]api.AccountKind
}

var _ SyncSupervisor = (*kindSupervisor)(nil)

func newKindSupervisor(imap, graph SyncSupervisor) *kindSupervisor {
	return &kindSupervisor{IMAP: imap, Graph: graph, kinds: map[string]api.AccountKind{}}
}

func (k *kindSupervisor) for_(kind api.AccountKind) SyncSupervisor {
	if kind == api.AccountGraph {
		return k.Graph
	}
	return k.IMAP
}

func (k *kindSupervisor) remember(a store.Account) SyncSupervisor {
	kind := a.Config.Protocol()
	k.mu.Lock()
	k.kinds[a.ID] = kind
	k.mu.Unlock()
	return k.for_(kind)
}

func (k *kindSupervisor) lookup(accountID string) (SyncSupervisor, bool) {
	k.mu.Lock()
	kind, ok := k.kinds[accountID]
	k.mu.Unlock()
	if !ok {
		return nil, false
	}
	return k.for_(kind), true
}

func (k *kindSupervisor) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); k.IMAP.Run(ctx) }()
	go func() { defer wg.Done(); k.Graph.Run(ctx) }()
	wg.Wait()
}

func (k *kindSupervisor) Start(a store.Account) { k.remember(a).Start(a) }

func (k *kindSupervisor) Stop(accountID string) {
	k.mu.Lock()
	kind, ok := k.kinds[accountID]
	delete(k.kinds, accountID)
	k.mu.Unlock()
	if ok {
		k.for_(kind).Stop(accountID)
	}
}

// Restart also covers a change of kind: the old supervisor stops the
// account, the new one starts it.
func (k *kindSupervisor) Restart(a store.Account) {
	k.mu.Lock()
	old, had := k.kinds[a.ID]
	k.kinds[a.ID] = a.Config.Protocol()
	k.mu.Unlock()
	if had && old != a.Config.Protocol() {
		k.for_(old).Stop(a.ID)
		k.for_(a.Config.Protocol()).Start(a)
		return
	}
	k.for_(a.Config.Protocol()).Restart(a)
}

func (k *kindSupervisor) Reload() {
	k.IMAP.Reload()
	k.Graph.Reload()
}

func (k *kindSupervisor) Trigger(accountID string, folder api.FolderID, full bool) bool {
	s, ok := k.lookup(accountID)
	if !ok {
		return false
	}
	return s.Trigger(accountID, folder, full)
}

func (k *kindSupervisor) State(accountID string) (api.SyncState, bool) {
	s, ok := k.lookup(accountID)
	if !ok {
		return api.SyncState{}, false
	}
	return s.State(accountID)
}

func (k *kindSupervisor) States() []api.SyncState {
	return append(k.IMAP.States(), k.Graph.States()...)
}

// kindOutbox is kindSupervisor for the outbox workers.
type kindOutbox struct {
	IMAP  OutboxSupervisor
	Graph OutboxSupervisor

	mu    sync.Mutex
	kinds map[string]api.AccountKind
}

var _ OutboxSupervisor = (*kindOutbox)(nil)

func newKindOutbox(imap, graph OutboxSupervisor) *kindOutbox {
	return &kindOutbox{IMAP: imap, Graph: graph, kinds: map[string]api.AccountKind{}}
}

func (k *kindOutbox) for_(kind api.AccountKind) OutboxSupervisor {
	if kind == api.AccountGraph {
		return k.Graph
	}
	return k.IMAP
}

func (k *kindOutbox) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); k.IMAP.Run(ctx) }()
	go func() { defer wg.Done(); k.Graph.Run(ctx) }()
	wg.Wait()
}

func (k *kindOutbox) Start(a store.Account) {
	kind := a.Config.Protocol()
	k.mu.Lock()
	k.kinds[a.ID] = kind
	k.mu.Unlock()
	k.for_(kind).Start(a)
}

func (k *kindOutbox) Stop(accountID string) {
	k.mu.Lock()
	kind, ok := k.kinds[accountID]
	delete(k.kinds, accountID)
	k.mu.Unlock()
	if ok {
		k.for_(kind).Stop(accountID)
	}
}

func (k *kindOutbox) Restart(a store.Account) {
	k.mu.Lock()
	old, had := k.kinds[a.ID]
	k.kinds[a.ID] = a.Config.Protocol()
	k.mu.Unlock()
	if had && old != a.Config.Protocol() {
		k.for_(old).Stop(a.ID)
		k.for_(a.Config.Protocol()).Start(a)
		return
	}
	k.for_(a.Config.Protocol()).Restart(a)
}

func (k *kindOutbox) Wake(accountID string) bool {
	k.mu.Lock()
	kind, ok := k.kinds[accountID]
	k.mu.Unlock()
	if !ok {
		return false
	}
	return k.for_(kind).Wake(accountID)
}
