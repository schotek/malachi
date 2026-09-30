// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// IssueSupervisor is what the supervisor of issue-tracker accounts adds
// to SyncSupervisor (internal/jira): a refresh of one issue by its key,
// soon (TriggerIssue, false when no syncer runs for the account or the key
// is none) or now and waited for (RefreshIssueWait, *api.Error). The kind
// dispatcher forwards both to the account's supervisor when it has them.
type IssueSupervisor interface {
	TriggerIssue(accountID, key string) bool
	RefreshIssueWait(ctx context.Context, accountID, key string, d time.Duration) error
}

// kindSupervisor routes every SyncSupervisor call to the supervisor of the
// account's kind (by, a table; a kind it has no entry for goes to a no-op
// supervisor). It remembers the kind of each started account, because
// Stop, Trigger and State only carry the id; an id it has not seen is a
// no-op, as the interface promises. Run, Reload and States reach every
// distinct supervisor of the table once.
type kindSupervisor struct {
	by map[api.AccountKind]SyncSupervisor

	mu    sync.Mutex
	kinds map[string]api.AccountKind
}

var (
	_ SyncSupervisor  = (*kindSupervisor)(nil)
	_ IssueSupervisor = (*kindSupervisor)(nil)
)

func newKindSupervisor(by map[api.AccountKind]SyncSupervisor) *kindSupervisor {
	table := make(map[api.AccountKind]SyncSupervisor, len(by))
	for k, s := range by {
		if s != nil {
			table[k] = s
		}
	}
	return &kindSupervisor{by: table, kinds: map[string]api.AccountKind{}}
}

func (k *kindSupervisor) for_(kind api.AccountKind) SyncSupervisor {
	if s, ok := k.by[kind]; ok {
		return s
	}
	return noopSupervisor{}
}

// distinct is every supervisor of the table once, in a stable order
// (kindOrder), so that one serving two kinds runs once. The table's values are
// pointers (or other comparable values).
func (k *kindSupervisor) distinct() []SyncSupervisor {
	var out []SyncSupervisor
	for _, kind := range sortedKinds(k.by) {
		s := k.by[kind]
		dup := false
		for _, have := range out {
			if have == s {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, s)
		}
	}
	return out
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
	for _, s := range k.distinct() {
		wg.Add(1)
		go func() { defer wg.Done(); s.Run(ctx) }()
	}
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
	for _, s := range k.distinct() {
		s.Reload()
	}
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
	var out []api.SyncState
	for _, s := range k.distinct() {
		out = append(out, s.States()...)
	}
	return out
}

// TriggerIssue forwards to the account's supervisor when it refreshes
// issues; false otherwise (an unknown account, a mail account).
func (k *kindSupervisor) TriggerIssue(accountID, key string) bool {
	s, ok := k.lookup(accountID)
	if !ok {
		return false
	}
	is, ok := s.(IssueSupervisor)
	return ok && is.TriggerIssue(accountID, key)
}

// RefreshIssueWait forwards to the account's supervisor when it refreshes
// issues; unavailable otherwise.
func (k *kindSupervisor) RefreshIssueWait(ctx context.Context, accountID, key string, d time.Duration) error {
	s, ok := k.lookup(accountID)
	if !ok {
		return api.NewError(api.CodeUnavailable, "account %s is not synchronising", accountID)
	}
	is, ok := s.(IssueSupervisor)
	if !ok {
		return api.NewError(api.CodeUnavailable, "account %s has no issues to refresh", accountID)
	}
	return is.RefreshIssueWait(ctx, accountID, key, d)
}

// kindOutbox is kindSupervisor for the outbox workers: mail goes out over
// SMTP or Graph, an issue tracker's comments to their issues; a kind
// without an entry goes to a no-op worker.
type kindOutbox struct {
	by map[api.AccountKind]OutboxSupervisor

	mu    sync.Mutex
	kinds map[string]api.AccountKind
}

var _ OutboxSupervisor = (*kindOutbox)(nil)

func newKindOutbox(by map[api.AccountKind]OutboxSupervisor) *kindOutbox {
	table := make(map[api.AccountKind]OutboxSupervisor, len(by))
	for k, s := range by {
		if s != nil {
			table[k] = s
		}
	}
	return &kindOutbox{by: table, kinds: map[string]api.AccountKind{}}
}

func (k *kindOutbox) for_(kind api.AccountKind) OutboxSupervisor {
	if s, ok := k.by[kind]; ok {
		return s
	}
	return noopOutbox{}
}

func (k *kindOutbox) distinct() []OutboxSupervisor {
	var out []OutboxSupervisor
	for _, kind := range sortedKinds(k.by) {
		s := k.by[kind]
		dup := false
		for _, have := range out {
			if have == s {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, s)
		}
	}
	return out
}

func (k *kindOutbox) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, s := range k.distinct() {
		wg.Add(1)
		go func() { defer wg.Done(); s.Run(ctx) }()
	}
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

// kindOrder is the order the dispatchers reach their supervisors in (Run,
// Reload, States); a kind not listed comes after these, by name.
var kindOrder = []api.AccountKind{api.AccountIMAP, api.AccountGraph, api.AccountJira}

// sortedKinds is the keys of a dispatch table in kindOrder.
func sortedKinds[V any](by map[api.AccountKind]V) []api.AccountKind {
	rank := func(k api.AccountKind) int {
		if i := slices.Index(kindOrder, k); i >= 0 {
			return i
		}
		return len(kindOrder)
	}
	kinds := make([]api.AccountKind, 0, len(by))
	for k := range by {
		kinds = append(kinds, k)
	}
	slices.SortFunc(kinds, func(a, b api.AccountKind) int {
		if ra, rb := rank(a), rank(b); ra != rb {
			return ra - rb
		}
		return strings.Compare(string(a), string(b))
	})
	return kinds
}
