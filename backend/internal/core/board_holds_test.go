// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The board's service fixes of 2026-10-08, end to end over the rules.

func (x *boardBox) commitment(id api.BoardCommitmentID) store.BoardCommitment {
	x.t.Helper()
	k, err := x.b.store.GetBoardCommitment(x.ctx, string(id))
	if err != nil {
		x.t.Fatal(err)
	}
	return k
}

// A note to self and a forward of the user's in the thread close none of
// the user's commitments and do not end the case; the user's next reply
// to the other party does close them (rules 6, the deciding member).
func TestBoardCommitmentsClosedByDecidingReplyOnly(t *testing.T) {
	x := newBoardBox(t)
	x.put(bmail{folder: x.inbox, thread: "t_deck", rfc: "d1", from: boardBob, to: []api.Address{boardMe}, subject: "Deck",
		text: "Can you send the deck?"})
	mine := x.put(bmail{folder: x.sent, thread: "t_deck", rfc: "d2", inReplyTo: "d1", from: boardMe, to: []api.Address{boardBob},
		subject: "Re: Deck", at: time.Hour, text: "I will send the deck tomorrow."})
	x.drain()
	x.assistantOn()
	c := x.caseOf("t_deck")
	if c.RuleReason != api.BoardReasonThemReplied {
		t.Fatalf("case: %s", c.RuleReason)
	}
	res, err := x.svc.Commit(x.ctx, api.BoardCommitParams{CaseID: c.ID, InputKey: x.inputKey(c.ID), Source: "claude",
		MessageID: api.MessageID(mine), Text: "Send the deck", Quote: "I will send the deck tomorrow"})
	if err != nil {
		t.Fatal(err)
	}
	// A note to self.
	x.put(bmail{folder: x.sent, thread: "t_deck", rfc: "d3", inReplyTo: "d2", from: boardMe, to: []api.Address{boardMe},
		subject: "Re: Deck", at: 2 * time.Hour, text: "Remember the deck."})
	x.drain()
	if k := x.commitment(res.Commitment.ID); k.State != api.CommitmentOpen {
		t.Fatalf("closed by a note to self: %+v", k)
	}
	// A forward to Alice.
	x.put(bmail{folder: x.sent, thread: "t_deck", rfc: "d4", inReplyTo: "d2", from: boardMe, to: []api.Address{boardAlice},
		subject: "Fwd: Deck", at: 3 * time.Hour,
		text: "FYI\n\n---------- Forwarded message ---------\nFrom: Bob\n\nCan you send the deck?"})
	x.drain()
	if k := x.commitment(res.Commitment.ID); k.State != api.CommitmentOpen {
		t.Fatalf("closed by a forward: %+v", k)
	}
	if got := x.caseOf("t_deck"); got.RuleReason != api.BoardReasonThemReplied {
		t.Fatalf("after the forward: %s", got.RuleReason)
	}
	// The user writes to Bob again.
	x.put(bmail{folder: x.sent, thread: "t_deck", rfc: "d5", inReplyTo: "d2", from: boardMe, to: []api.Address{boardBob},
		subject: "Re: Deck", at: 4 * time.Hour, text: "Here it is."})
	x.drain()
	if k := x.commitment(res.Commitment.ID); k.State != api.CommitmentClosed || k.ClosedReason != api.CommitmentClosedReplied {
		t.Fatalf("not closed by the reply: %+v", k)
	}
}

// The user's own flag makes a case hot also when the user wrote last.
func TestBoardFlaggedWithMyReplyLast(t *testing.T) {
	x := newBoardBox(t)
	x.put(bmail{folder: x.inbox, thread: "t_f", rfc: "f1", from: boardBob, to: []api.Address{boardMe}, flagged: true, text: "Sign it"})
	x.put(bmail{folder: x.sent, thread: "t_f", rfc: "f2", inReplyTo: "f1", from: boardMe, to: []api.Address{boardBob}, at: time.Hour,
		text: "Signed."})
	x.drain()
	if c := x.caseOf("t_f"); c.RuleState != api.BoardHot || c.RuleReason != api.BoardReasonHotFlagged {
		t.Fatalf("case: %s %s", c.RuleState, c.RuleReason)
	}
}

// board.archive names what it moved, so that a client can undo it: each
// message back to its folder, then done cleared.
func TestBoardArchiveMoved(t *testing.T) {
	x := newBoardBox(t)
	a1 := x.put(bmail{folder: x.inbox, thread: "t_a", rfc: "a1", from: boardAlice, to: []api.Address{boardMe}, text: "Hi"})
	a2 := x.put(bmail{folder: x.inbox, thread: "t_a", rfc: "a2", inReplyTo: "a1", from: boardAlice, to: []api.Address{boardMe}, at: time.Minute, text: "Again"})
	x.drain()
	c := x.caseOf("t_a")
	res, err := x.svc.Archive(x.ctx, api.BoardArchiveParams{CaseID: c.ID})
	if err != nil || res.Archived != 2 || len(res.Moved) != 2 {
		t.Fatalf("archive: %+v %v", res, err)
	}
	ids := []api.MessageID{}
	for _, m := range res.Moved {
		if m.FromFolderID != api.FolderID(x.inbox.ID) || (m.MessageID != api.MessageID(a1) && m.MessageID != api.MessageID(a2)) {
			t.Fatalf("moved: %+v", m)
		}
		ids = append(ids, m.MessageID)
	}
	// Undo.
	if _, err := x.b.Messages().Move(x.ctx, api.MessageMoveParams{AccountID: api.AccountID(x.acc), MessageIDs: ids,
		TargetFolderID: res.Moved[0].FromFolderID}); err != nil {
		t.Fatal(err)
	}
	if d, err := x.svc.SetDone(x.ctx, api.BoardSetDoneParams{CaseID: c.ID}); err != nil || d.Case.Visibility != api.BoardLive {
		t.Fatalf("undo: %+v %v", d, err)
	}
	for _, id := range []string{a1, a2} {
		if m, err := x.b.store.GetMessage(x.ctx, x.acc, id); err != nil || m.FolderID != x.inbox.ID {
			t.Fatalf("not back: %v %v", m.FolderID, err)
		}
	}
	x.drain()
	if got := x.caseOf("t_a"); got.Visibility != api.BoardLive {
		t.Fatalf("after the undo: %+v", got)
	}
	// Nothing to move: no list.
	res, err = x.svc.Archive(x.ctx, api.BoardArchiveParams{CaseID: c.ID})
	if err != nil {
		t.Fatal(err)
	}
	res2, err := x.svc.Archive(x.ctx, api.BoardArchiveParams{CaseID: c.ID})
	if err != nil || res2.Moved != nil || res2.Archived != 0 {
		t.Fatalf("archive again: %+v %v", res2, err)
	}
}

// remindedAt: board.list shows when a remind came due until the user acts
// on the case; inbound mail ends a remind early (live, not reminded); a
// remind that came due stays live when the clock goes back.
func TestBoardRemindedAtAndWake(t *testing.T) {
	x := newBoardBox(t)
	x.put(bmail{folder: x.inbox, thread: "t_r", rfc: "r1", from: boardAlice, to: []api.Address{boardMe}, at: 40 * time.Hour, text: "Hi"})
	x.drain()
	c := x.caseOf("t_r")
	now := time.Now().UTC()
	until := now.Add(24 * time.Hour).Truncate(time.Second)
	remind := func() {
		t.Helper()
		if _, err := x.svc.Remind(x.ctx, api.BoardRemindParams{CaseID: c.ID, Until: &until}); err != nil {
			t.Fatal(err)
		}
	}
	setNow := func(at time.Time) { x.b.board.now = func() time.Time { return at } }
	t.Cleanup(func() { x.b.board.now = time.Now })

	remind()
	setNow(until.Add(time.Minute))
	x.b.clearDueReminds(x.ctx)
	got := x.caseOf("t_r")
	if got.Visibility != api.BoardLive || got.RemindedAt == nil || !got.RemindedAt.Equal(until) || got.RemindAt != nil {
		t.Fatalf("reminded: %+v", got)
	}
	// The clock goes back: still live and reminded, not snoozed.
	setNow(now)
	if got := x.caseOf("t_r"); got.Visibility != api.BoardLive || got.RemindedAt == nil {
		t.Fatalf("clock back: %+v", got)
	}
	// The user acts: the mark goes.
	st := api.BoardYou
	if res, err := x.svc.SetState(x.ctx, api.BoardSetStateParams{CaseID: c.ID, State: &st}); err != nil || res.Case.RemindedAt != nil {
		t.Fatalf("setState: %+v %v", res, err)
	}

	// Snoozed again; new mail from Alice wakes it before the remind.
	x.b.board.now = time.Now
	remind()
	if got := x.caseOf("t_r"); got.Visibility != api.BoardSnoozed {
		t.Fatalf("snoozed: %+v", got)
	}
	tick()
	x.put(bmail{folder: x.inbox, thread: "t_r", rfc: "r2", inReplyTo: "r1", from: boardAlice, to: []api.Address{boardMe},
		at: 47 * time.Hour, text: "Any news?"})
	x.drain()
	got = x.caseOf("t_r")
	if got.Visibility != api.BoardLive || got.RemindAt != nil || got.RemindedAt != nil {
		t.Fatalf("not woken: %+v", got)
	}
	// The user's own reply does not wake a remind.
	remind()
	tick()
	x.put(bmail{folder: x.sent, thread: "t_r", rfc: "r3", inReplyTo: "r2", from: boardMe, to: []api.Address{boardAlice},
		at: 47*time.Hour + time.Minute, text: "Soon."})
	x.drain()
	if got := x.caseOf("t_r"); got.Visibility != api.BoardSnoozed {
		t.Fatalf("woken by the user's own reply: %+v", got)
	}

	// board.unflag ends a remind that came due.
	setNow(until.Add(time.Minute))
	x.b.clearDueReminds(x.ctx)
	if got := x.caseOf("t_r"); got.RemindedAt == nil {
		t.Fatalf("not reminded: %+v", got)
	}
	if res, err := x.svc.Unflag(x.ctx, api.BoardUnflagParams{CaseID: c.ID}); err != nil || res.Case.RemindedAt != nil {
		t.Fatalf("unflag: %+v %v", res, err)
	}
}

// A linked draft's text is cleaned as every other string of a case.
func TestBoardDraftTextCleaned(t *testing.T) {
	x := newBoardBox(t)
	in := x.put(bmail{folder: x.inbox, thread: "t_a", rfc: "a1", from: boardAlice, to: []api.Address{boardMe}, text: "Hi"})
	x.drain()
	c := x.caseOf("t_a")
	d, err := x.b.Drafts().Save(x.ctx, api.DraftSaveParams{Draft: api.Draft{AccountID: api.AccountID(x.acc), To: []api.Address{boardAlice},
		Subject: "Re: Lunch", TextBody: "Yes\u202e, see   https://example.invalid/x\u200b\r\n\r\n\r\n\u0007Bye  ", InReplyTo: api.MessageID(in)}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := x.svc.SetDraft(x.ctx, api.BoardSetDraftParams{CaseID: c.ID, DraftID: d.DraftID})
	if err != nil || res.Case.Draft == nil {
		t.Fatalf("setDraft: %+v %v", res, err)
	}
	if got := res.Case.Draft.Text; got != "Yes, see https://example.invalid/x\n\nBye" {
		t.Fatalf("draft text: %q", got)
	}
}

// board.get derives at most boardGetDerive own texts while it waits; the
// others come from the stored text and are derived in the background.
func TestBoardGetDerivesFew(t *testing.T) {
	x := newBoardBox(t)
	const n = boardGetDerive + 4
	var ids []string
	for i := range n {
		quoted := `<div>Reply ` + string(rune('a'+i)) + `</div>` + `<blockquote type="cite">older words</blockquote>`
		ids = append(ids, x.put(bmail{folder: x.inbox, thread: "t_long", rfc: "l" + string(rune('a'+i)), from: boardAlice,
			to: []api.Address{boardMe}, at: time.Duration(i) * time.Minute, text: "Reply " + string(rune('a'+i)) + "\n\nolder words",
			html: quoted}))
	}
	x.drain()
	c := x.caseOf("t_long")
	cached := func() int {
		k := 0
		for _, id := range ids {
			if _, ok := x.b.board.own.get(boardOwnKey{account: x.acc, id: id, state: store.BodyFetched}); ok {
				k++
			}
		}
		return k
	}
	if k := cached(); k != 0 {
		t.Fatalf("cached before board.get: %d", k)
	}
	// No worker: nothing is derived later.
	if _, err := x.svc.Get(x.ctx, api.BoardGetParams{CaseID: c.ID}); err != nil {
		t.Fatal(err)
	}
	if k := cached(); k != boardGetDerive {
		t.Fatalf("derived while waiting: %d, want %d", k, boardGetDerive)
	}
	for _, id := range ids[len(ids)-boardGetDerive:] {
		if _, ok := x.b.board.own.get(boardOwnKey{account: x.acc, id: id, state: store.BodyFetched}); !ok {
			t.Fatalf("the newest were not derived first: %s", id)
		}
	}
	// With the worker's context, the rest is derived in the background.
	ctx, cancel := context.WithCancel(x.ctx)
	defer cancel()
	x.b.board.mu.Lock()
	x.b.board.ctx = ctx
	x.b.board.mu.Unlock()
	got, err := x.svc.Get(x.ctx, api.BoardGetParams{CaseID: c.ID})
	if err != nil || len(got.Messages) != n {
		t.Fatalf("get: %+v %v", got, err)
	}
	x.b.board.bg.Wait()
	if k := cached(); k != n {
		t.Fatalf("derived in the background: %d, want %d", k, n)
	}
	if len(x.b.board.own.deriving) != 0 {
		t.Fatalf("keys still held: %v", x.b.board.own.deriving)
	}
	got, err = x.svc.Get(x.ctx, api.BoardGetParams{CaseID: c.ID})
	if err != nil || strings.Contains(got.Messages[0].Text, "older words") || !got.Messages[0].Trimmed {
		t.Fatalf("an excerpt of the oldest after the background: %+v %v", got.Messages[0], err)
	}
}

// board.runEnd keeps that the usage is a lower bound; the 24-hour sum
// says so.
func TestBoardRunEndLowerBound(t *testing.T) {
	x := newBoardBox(t)
	run, err := x.svc.RunStart(x.ctx, api.BoardRunStartParams{Trigger: api.TriggerManual, Source: "claude-code"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x.svc.RunEnd(x.ctx, api.BoardRunEndParams{RunID: run.RunID, Error: api.RunCancelled,
		Usage: &api.BoardUsage{InputTokens: 7, LowerBound: true}}); err != nil {
		t.Fatal(err)
	}
	res, _ := x.list()
	if u := res.Triage.Usage24h; u == nil || !u.LowerBound || u.InputTokens != 7 || u.Runs != 1 {
		t.Fatalf("usage24h: %+v", u)
	}
}
