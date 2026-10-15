// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// board.trash moves every received message of the case to the trash, from
// whatever folder, and marks it done: the user's own messages and their
// copies, drafts and what is in the trash already stay; moved names each
// for an undo, which works as board.archive's.
func TestBoardTrash(t *testing.T) {
	x := newBoardBox(t)
	a1 := x.put(bmail{folder: x.inbox, thread: "t_a", rfc: "a1", from: boardAlice, to: []api.Address{boardMe}, text: "Hi"})
	a2 := x.put(bmail{folder: x.sent, thread: "t_a", rfc: "a2", inReplyTo: "a1", from: boardMe, to: []api.Address{boardAlice},
		at: time.Hour, text: "Yes?"})
	// A Bcc of the reply to oneself: the user's own message.
	twin := x.put(bmail{folder: x.inbox, thread: "t_a", rfc: "a2", inReplyTo: "a1", from: boardMe, to: []api.Address{boardAlice},
		bcc: []api.Address{boardMe}, at: time.Hour, text: "Yes?"})
	a3 := x.put(bmail{folder: x.archive, thread: "t_a", rfc: "a3", inReplyTo: "a2", from: boardAlice, to: []api.Address{boardMe},
		at: 2 * time.Hour, text: "Good"})
	a4 := x.put(bmail{folder: x.trash, thread: "t_a", rfc: "a4", inReplyTo: "a3", from: boardAlice, to: []api.Address{boardMe},
		at: 3 * time.Hour, text: "Never mind"})
	x.drain()
	c := x.caseOf("t_a")

	res, err := x.svc.Trash(x.ctx, api.BoardTrashParams{CaseID: c.ID})
	if err != nil || res.Trashed != 2 || res.Case.Visibility != api.BoardDone || len(res.Moved) != 2 {
		t.Fatalf("trash: %+v %v", res, err)
	}
	from := map[api.MessageID]api.FolderID{}
	for _, m := range res.Moved {
		from[m.MessageID] = m.FromFolderID
	}
	if from[api.MessageID(a1)] != api.FolderID(x.inbox.ID) || from[api.MessageID(a3)] != api.FolderID(x.archive.ID) {
		t.Fatalf("moved: %+v", res.Moved)
	}
	for id, want := range map[string]string{a1: x.trash.ID, a3: x.trash.ID, a2: x.sent.ID, twin: x.inbox.ID, a4: x.trash.ID} {
		m, err := x.b.store.GetMessage(x.ctx, x.acc, id)
		if err != nil || m.FolderID != want {
			t.Errorf("%s in %s (want %s), %v", id, m.FolderID, want, err)
		}
	}
	x.drain()
	if got := x.caseOf("t_a"); got.Visibility != api.BoardDone {
		t.Fatalf("after trashing: %+v", got)
	}

	// Undo: each back to its folder, then done cleared.
	for _, m := range res.Moved {
		if _, err := x.b.Messages().Move(x.ctx, api.MessageMoveParams{AccountID: api.AccountID(x.acc),
			MessageIDs: []api.MessageID{m.MessageID}, TargetFolderID: m.FromFolderID}); err != nil {
			t.Fatal(err)
		}
	}
	if d, err := x.svc.SetDone(x.ctx, api.BoardSetDoneParams{CaseID: c.ID}); err != nil || d.Case.Visibility != api.BoardLive {
		t.Fatalf("undo: %+v %v", d, err)
	}
	x.drain()
	if got := x.caseOf("t_a"); got.Visibility != api.BoardLive {
		t.Fatalf("after the undo: %+v", got)
	}

	// Nothing received left outside the trash: only marked done.
	if _, err := x.svc.Trash(x.ctx, api.BoardTrashParams{CaseID: c.ID}); err != nil {
		t.Fatal(err)
	}
	again, err := x.svc.Trash(x.ctx, api.BoardTrashParams{CaseID: c.ID})
	if err != nil || again.Trashed != 0 || again.Moved != nil || again.Case.Visibility != api.BoardDone {
		t.Fatalf("trash again: %+v %v", again, err)
	}

	_, err = x.svc.Trash(x.ctx, api.BoardTrashParams{CaseID: "c_nope"})
	wantCode(t, "unknown case", err, api.CodeCaseNotFound)
}

// Without a trash folder nothing moves and the case stays as it was.
func TestBoardTrashNoTrashFolder(t *testing.T) {
	x := newBoardBox(t)
	if _, err := x.b.store.DB().ExecContext(x.ctx, `UPDATE folders SET role = 'none' WHERE id = ?`, x.trash.ID); err != nil {
		t.Fatal(err)
	}
	b1 := x.put(bmail{folder: x.inbox, thread: "t_b", rfc: "b1", from: boardAlice, to: []api.Address{boardMe}, text: "Hi"})
	x.drain()
	c := x.caseOf("t_b")
	_, err := x.svc.Trash(x.ctx, api.BoardTrashParams{CaseID: c.ID})
	wantCode(t, "no trash folder", err, api.CodeFolderNotFound)
	if m, err := x.b.store.GetMessage(x.ctx, x.acc, b1); err != nil || m.FolderID != x.inbox.ID {
		t.Fatalf("moved: %s %v", m.FolderID, err)
	}
	if got := x.caseOf("t_b"); got.Visibility != api.BoardLive {
		t.Fatalf("case: %+v", got)
	}
}
