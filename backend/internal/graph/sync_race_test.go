// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// A message the user trashes while a pass runs is still in the inbox on
// the server until the next pass pushes the move. The pass must leave its
// row alone: neither drag it back into the inbox when the inbox's delta
// names it, nor — the pass being a reconciliation that enumerates every
// folder (reconcileAfter) — delete it from the trash as a message the
// server's trash does not hold. It did the latter: the row went, and the
// pass that pushed the move created it anew under another local id (the
// IMAP twin is TestLocalTrashDuringPassIsNotFetchedBack).
func TestLocalTrashDuringPassKeepsItsRow(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, SyncPrefs{})
	a := h.fake.add("F-INBOX", "A", "alice@example.test", daysAgo(1), "a")
	h.fake.add("F-INBOX", "B", "alice@example.test", daysAgo(1), "b")
	start := time.Now()
	h.start()
	h.waitIdle(start)
	la, _ := h.byRemote(a)
	inbox, trash := h.folder("inbox"), h.folder("trash")

	var armed atomic.Bool
	trashed := make(chan error, 1)
	h.fake.mu.Lock()
	h.fake.onRequest = func(method, path string) {
		if path == "/mailFolders/F-INBOX/messages/delta" && armed.CompareAndSwap(true, false) {
			// The user trashes A as the pass reads the inbox.
			_, err := h.st.TrashMessages(ctx, h.acc.ID, []string{la.ID}, trash.ID)
			trashed <- err
		}
	}
	h.fake.mu.Unlock()
	// Every folder is due for a reconciliation: the pass enumerates them.
	h.mu.Lock()
	h.offset = reconcileAfter + time.Hour
	h.mu.Unlock()
	armed.Store(true)
	h.pass("", false)
	if err := <-trashed; err != nil {
		t.Fatal(err)
	}
	if got, ok := h.byRemote(a); !ok || got.FolderID != trash.ID || got.ID != la.ID {
		t.Fatalf("after the pass: %+v (found %v)", got, ok)
	}

	// The next pass pushes the move: one row per folder, A's local id
	// kept, nothing queued.
	h.pass("", false)
	if m, ok := h.fake.get(a); !ok || m.folder != "F-TRASH" {
		t.Fatalf("move not pushed: %+v", m)
	}
	if n, _ := h.st.CountPendingOps(ctx, h.acc.ID); n != 0 {
		t.Fatalf("pending ops = %d", n)
	}
	if got, ok := h.byRemote(a); !ok || got.FolderID != trash.ID || got.ID != la.ID {
		t.Fatalf("after the move: %+v (found %v)", got, ok)
	}
	if msgs := h.messages(trash.ID); len(msgs) != 1 || msgs[0].Subject != "A" {
		t.Fatalf("trash locally = %+v", msgs)
	}
	if msgs := h.messages(inbox.ID); len(msgs) != 1 || msgs[0].Subject != "B" {
		t.Fatalf("inbox locally = %+v", msgs)
	}
}
