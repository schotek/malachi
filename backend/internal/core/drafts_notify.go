// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// draftsNotifyCoalesce is how long notify.draftsChanged gathers: an
// editor's autosaves and a send's delete go out as one.
const draftsNotifyCoalesce = 250 * time.Millisecond

// draftNotify gathers notify.draftsChanged: the threads of each account
// whose drafts changed, "" for any thread of the account.
type draftNotify struct {
	mu      sync.Mutex
	changed map[string]map[string]bool
	timer   *time.Timer
	stopped bool
}

// draftChanged is told that a draft of the account that replies to
// inReplyTo was saved, deleted, sent or linked to a board case or
// unlinked: its thread is announced (any thread of the account when the
// message is no longer stored or the lookup fails).
func (b *Backend) draftChanged(ctx context.Context, accountID, inReplyTo string) {
	if inReplyTo == "" {
		return // a forward or a new message: no conversation shows it
	}
	tid, err := b.store.DraftThread(ctx, accountID, inReplyTo)
	if err != nil {
		b.log.Warn("drafts: thread of a draft", "err", err)
	}
	b.draftsChanged(accountID, tid)
}

// draftsChanged queues notify.draftsChanged for the account's threads
// ("" or none: any thread of the account).
func (b *Backend) draftsChanged(accountID string, threadIDs ...string) {
	if accountID == "" {
		return
	}
	dn := &b.dn
	dn.mu.Lock()
	defer dn.mu.Unlock()
	if dn.stopped {
		return
	}
	if dn.changed == nil {
		dn.changed = map[string]map[string]bool{}
	}
	set := dn.changed[accountID]
	if set == nil {
		set = map[string]bool{}
		dn.changed[accountID] = set
	}
	if len(threadIDs) == 0 {
		set[""] = true
	}
	for _, t := range threadIDs {
		set[t] = true
	}
	if dn.timer == nil {
		dn.timer = time.AfterFunc(draftsNotifyCoalesce, b.flushDraftsChanged)
	}
}

// flushDraftsChanged sends what draftsChanged gathered, one notification
// an account, the accounts and their threads in order.
func (b *Backend) flushDraftsChanged() {
	dn := &b.dn
	dn.mu.Lock()
	changed, stopped := dn.changed, dn.stopped
	dn.changed, dn.timer = nil, nil
	dn.mu.Unlock()
	n, ok := b.getNotifier().(api.DraftsNotifier)
	if stopped || !ok {
		return
	}
	accounts := make([]string, 0, len(changed))
	for acc := range changed {
		accounts = append(accounts, acc)
	}
	sort.Strings(accounts)
	for _, acc := range accounts {
		ev := api.DraftsChangedNotification{AccountID: api.AccountID(acc)}
		if !changed[acc][""] {
			threads := make([]string, 0, len(changed[acc]))
			for t := range changed[acc] {
				threads = append(threads, t)
			}
			sort.Strings(threads)
			for _, t := range threads {
				ev.ThreadIDs = append(ev.ThreadIDs, api.ThreadID(t))
			}
		}
		n.DraftsChanged(ev)
	}
}

// stopDraftNotify ends the timer; nothing is announced after it.
func (b *Backend) stopDraftNotify() {
	dn := &b.dn
	dn.mu.Lock()
	defer dn.mu.Unlock()
	dn.stopped = true
	if dn.timer != nil {
		dn.timer.Stop()
		dn.timer = nil
	}
	dn.changed = nil
}
