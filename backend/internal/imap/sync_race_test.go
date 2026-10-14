// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package imap

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
)

// searchHookSession runs hook on the server's goroutine when the client
// searches the mailbox it selected last: inside syncFolder, after the
// SELECT and before the local UIDs are listed.
type searchHookSession struct {
	imapserver.Session
	mu       sync.Mutex
	selected string
	hook     func(mailbox string)
}

func (s *searchHookSession) Select(mailbox string, options *imap.SelectOptions) (*imap.SelectData, error) {
	s.mu.Lock()
	s.selected = mailbox
	s.mu.Unlock()
	return s.Session.Select(mailbox, options)
}

func (s *searchHookSession) Search(kind imapserver.NumKind, criteria *imap.SearchCriteria, options *imap.SearchOptions) (*imap.SearchData, error) {
	s.mu.Lock()
	mailbox := s.selected
	s.mu.Unlock()
	s.hook(mailbox)
	return s.Session.Search(kind, criteria, options)
}

// Move keeps the memserver's MOVE reachable (see refusingSession).
func (s *searchHookSession) Move(w *imapserver.MoveWriter, numSet imap.NumSet, dest string) error {
	return s.Session.(imapserver.SessionMove).Move(w, numSet, dest)
}

// A message the user trashes while a pass is between pushing the
// operations and listing the folder is still on the server: the pass must
// not fetch it back as new. It did: the message reappeared in the inbox
// (a second row, the first one in the trash with UID 0) until the next
// pass pushed the move and saw it gone — ten quick deletions in a row
// came back for ten, twenty seconds (owner, 2026-10-13).
func TestLocalTrashDuringPassIsNotFetchedBack(t *testing.T) {
	ctx := context.Background()
	var (
		hp      atomic.Pointer[harness]
		armed   atomic.Bool
		trashed = make(chan error, 1)
	)
	h := newHarness(t, harnessOptions{session: func(s imapserver.Session) imapserver.Session {
		return &searchHookSession{Session: s, hook: func(mailbox string) {
			if mailbox != "INBOX" || !armed.CompareAndSwap(true, false) {
				return
			}
			h := hp.Load()
			var id string
			for _, m := range h.messages(h.folder("inbox").ID) {
				if m.Subject == "Trash me" {
					id = m.ID
				}
			}
			_, err := h.st.TrashMessages(ctx, h.acc.ID, []string{id}, h.folder("trash").ID)
			trashed <- err
		}}
	}})
	hp.Store(h)
	if err := h.user.Create("Trash", nil); err != nil {
		t.Fatal(err)
	}
	h.append("INBOX", rawMessage("t1", "Trash me", "body"), daysAgo(1))
	h.append("INBOX", rawMessage("t2", "Stay", "body"), daysAgo(1))
	start := time.Now()
	h.start()
	h.waitIdle(start)
	inbox, trash := h.folder("inbox"), h.folder("trash")
	if msgs := h.messages(inbox.ID); len(msgs) != 2 {
		t.Fatalf("inbox before = %+v", msgs)
	}

	// The next pass: its operations pushed (none), the user trashes the
	// message as it lists the inbox.
	armed.Store(true)
	before := time.Now()
	h.syncer.Trigger("", false)
	if err := <-trashed; err != nil {
		t.Fatal(err)
	}
	h.waitIdle(before)
	for _, m := range h.messages(inbox.ID) {
		if m.Subject == "Trash me" {
			t.Fatalf("fetched back into the inbox: %+v", m)
		}
	}
	if msgs := h.messages(trash.ID); len(msgs) != 1 || msgs[0].Subject != "Trash me" {
		t.Fatalf("trash locally = %+v", msgs)
	}
	if n, _ := h.st.CountPendingOps(ctx, h.acc.ID); n != 1 {
		t.Fatalf("pending ops = %d", n)
	}

	// The pass after pushes the move: one message in each folder, on the
	// server and locally, no duplicate, nothing pending.
	again := time.Now()
	h.syncer.Trigger("", false)
	h.waitIdle(again)
	waitFor(t, "move pushed", func() bool {
		n, _ := h.st.CountPendingOps(ctx, h.acc.ID)
		return n == 0 && len(h.serverUIDs("Trash")) == 1
	})
	if got := h.serverUIDs("INBOX"); len(got) != 1 {
		t.Fatalf("inbox on server = %v", got)
	}
	if msgs := h.messages(inbox.ID); len(msgs) != 1 || msgs[0].Subject != "Stay" {
		t.Fatalf("inbox locally = %+v", msgs)
	}
	waitFor(t, "trash reconciled", func() bool {
		msgs := h.messages(trash.ID)
		return len(msgs) == 1 && msgs[0].Subject == "Trash me" && msgs[0].UID != 0
	})
}
