// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The archive in the mail view, end to end with the rules (docs/api.md
// §4.13): message.move of a case's inbox messages into the archive folder
// marks it done as board.archive does, whatever client moved them; the
// move back makes it live again with the user's state; the user's way
// back to the board by hand sticks; a reply that arrives archived (handled
// on the phone) leaves a done case alone until it is in the inbox; a
// thread archived before the board saw it is no case; a case judged
// before rules 7 is marked done at its first judgement since.
func TestBoardArchiveInMailView(t *testing.T) {
	x := newBoardBox(t)
	a1 := x.put(bmail{folder: x.inbox, thread: "t_a", rfc: "a1", from: boardAlice, to: []api.Address{boardMe}, text: "Can you send the deck?"})
	mine := x.put(bmail{folder: x.sent, thread: "t_a", rfc: "a2", inReplyTo: "a1", from: boardMe, to: []api.Address{boardAlice}, at: time.Hour,
		text: "I will send the deck tomorrow."})
	x.drain()
	x.assistantOn()
	c := x.caseOf("t_a")
	if c.RuleReason != api.BoardReasonThemReplied || !c.CanArchive || c.Visibility != api.BoardLive {
		t.Fatalf("case: %+v", c)
	}
	move := func(id string, f api.FolderID) {
		t.Helper()
		if _, err := x.b.Messages().Move(x.ctx, api.MessageMoveParams{AccountID: api.AccountID(x.acc), MessageIDs: []api.MessageID{api.MessageID(id)},
			TargetFolderID: f}); err != nil {
			t.Fatal(err)
		}
		x.drain()
	}
	archiveF, inboxF := api.FolderID(x.archive.ID), api.FolderID(x.inbox.ID)
	// Snoozed, with a state of the user's and an open commitment.
	until := time.Now().UTC().Add(24 * time.Hour)
	if _, err := x.svc.Remind(x.ctx, api.BoardRemindParams{CaseID: c.ID, Until: &until}); err != nil {
		t.Fatal(err)
	}
	st := api.BoardHot
	if _, err := x.svc.SetState(x.ctx, api.BoardSetStateParams{CaseID: c.ID, State: &st}); err != nil {
		t.Fatal(err)
	}
	k, err := x.svc.Commit(x.ctx, api.BoardCommitParams{CaseID: c.ID, InputKey: x.inputKey(c.ID), Source: "claude",
		MessageID: api.MessageID(mine), Text: "Send the deck", Quote: "I will send the deck tomorrow"})
	if err != nil {
		t.Fatal(err)
	}
	x.rec.take()

	// Archived in the mail view: done, the remind gone, the state kept,
	// the commitment closed; the clients are told.
	move(a1, archiveF)
	got := x.caseOf("t_a")
	if got.Visibility != api.BoardDone || got.DoneAt == nil || got.RemindAt != nil || got.UserState == nil || *got.UserState != api.BoardHot || got.CanArchive {
		t.Fatalf("archived: %+v", got)
	}
	if k2 := x.commitment(k.Commitment.ID); k2.State != api.CommitmentClosed || k2.ClosedReason != api.CommitmentClosedDone {
		t.Fatalf("commitment: %+v", k2)
	}
	waitBoardEvent(t, x.rec, x.acc)
	// Back to the board by hand: judged again (read), it stays live.
	if res, err := x.svc.SetDone(x.ctx, api.BoardSetDoneParams{CaseID: c.ID}); err != nil || res.Case.Visibility != api.BoardLive {
		t.Fatalf("back by hand: %+v %v", res, err)
	}
	if err := x.b.store.MarkBoardThreadsDirty(x.ctx, x.acc, []string{"t_a"}); err != nil {
		t.Fatal(err)
	}
	x.drain()
	if got := x.caseOf("t_a"); got.Visibility != api.BoardLive || got.RuleReason != api.BoardReasonThemReplied {
		t.Fatalf("judged again after the way back: %+v", got)
	}
	// Into the inbox and archived again: done again; un-archived: live,
	// the state kept and the commitment open again.
	move(a1, inboxF)
	if got := x.caseOf("t_a"); got.Visibility != api.BoardLive || !got.CanArchive {
		t.Fatalf("in the inbox again: %+v", got)
	}
	move(a1, archiveF)
	if got := x.caseOf("t_a"); got.Visibility != api.BoardDone {
		t.Fatalf("archived again: %+v", got)
	}
	move(a1, inboxF)
	got = x.caseOf("t_a")
	if got.Visibility != api.BoardLive || got.UserState == nil || *got.UserState != api.BoardHot {
		t.Fatalf("un-archived: %+v", got)
	}
	if k2 := x.commitment(k.Commitment.ID); k2.State != api.CommitmentOpen {
		t.Fatalf("commitment after the un-archive: %+v", k2)
	}
	// Alice's reply handled on the phone arrives archived: the done case
	// stays done; in the inbox it reopens the case as new mail does.
	move(a1, archiveF)
	a3 := x.put(bmail{folder: x.archive, thread: "t_a", rfc: "a3", inReplyTo: "a2", from: boardAlice, to: []api.Address{boardMe},
		date: time.Now().UTC(), text: "Thanks."})
	x.drain()
	if got := x.caseOf("t_a"); got.Visibility != api.BoardDone {
		t.Fatalf("a reply that arrived archived: %+v", got)
	}
	move(a3, inboxF)
	if got := x.caseOf("t_a"); got.Visibility != api.BoardLive || got.RuleReason != api.BoardReasonYouRepliedToYou || got.UserState != nil {
		t.Fatalf("the reply in the inbox: %+v", got)
	}

	// A thread archived before the board saw it is no case; a message of
	// it in the inbox makes one.
	b1 := x.put(bmail{folder: x.archive, thread: "t_b", rfc: "b1", from: boardAlice, to: []api.Address{boardMe}, text: "Old"})
	x.drain()
	x.noCase("t_b")
	move(b1, inboxF)
	cb := x.caseOf("t_b")
	// Judged before rules 7, its thread archived and put back by hand:
	// marked done once by the rules that tell an archive.
	move(b1, archiveF)
	if _, err := x.svc.SetDone(x.ctx, api.BoardSetDoneParams{CaseID: cb.ID}); err != nil {
		t.Fatal(err)
	}
	if err := x.b.store.MarkBoardThreadsDirty(x.ctx, x.acc, []string{"t_b"}); err != nil {
		t.Fatal(err)
	}
	x.drain()
	if got := x.caseOf("t_b"); got.Visibility != api.BoardLive {
		t.Fatalf("put back: %+v", got)
	}
	if _, err := x.b.store.DB().ExecContext(x.ctx, `UPDATE board_cases SET rules_version = '6' WHERE id = ?`, string(cb.ID)); err != nil {
		t.Fatal(err)
	}
	if err := x.b.store.MarkBoardThreadsDirty(x.ctx, x.acc, []string{"t_b"}); err != nil {
		t.Fatal(err)
	}
	x.drain()
	if got := x.caseOf("t_b"); got.Visibility != api.BoardDone {
		t.Fatalf("first judged by rules 7: %+v", got)
	}
}
