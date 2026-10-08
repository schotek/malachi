// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// What keeps a case, what ends a remind and what reopens commitments
// (the board's store fixes of 2026-10-08).

// storeTick lets the store's millisecond stamps move on.
func storeTick() { time.Sleep(3 * time.Millisecond) }

// noCaseDecider is testDecider whose rules make no case of the thread,
// naming the newest member as the deciding one.
func noCaseDecider(th *BoardThread) (BoardVerdict, error) {
	v, err := testDecider(th)
	v.State, v.Reason = "", ""
	return v, err
}

func markDirty(t *testing.T, s *Store, threadID string) {
	t.Helper()
	if err := s.MarkBoardThreadsDirty(context.Background(), "acc", []string{threadID}); err != nil {
		t.Fatal(err)
	}
}

// A case kept only by a commitment that the deciding member of the user's
// answers is deleted in the same pass (row 1), and only a deciding member
// of the user's closes commitments (row 28).
func TestBoardKeptAfterRepliedCommitmentsClose(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	mine1, mine2, c, sent := commitmentsThreadIn(t, s)
	k, _, err := s.AddBoardCommitment(ctx, commitIn(c, mine1, "send it", "I will send it"))
	if err != nil {
		t.Fatal(err)
	}
	mine3 := boardMail(t, s, sent, 5, "r3", "r2", "a", "r2")
	sameThread(t, s, mine1, mine2, mine3)

	// The rules drop the thread, and the newest member is not deciding
	// (a note to self, a forward): nothing closes, the commitment keeps it.
	drainAll(t, s, boardNow, func(th *BoardThread) (BoardVerdict, error) {
		v, err := noCaseDecider(th)
		v.DecidingMessageID, v.DecidingMine = "", false
		return v, err
	})
	if got, err := s.GetBoardCommitment(ctx, k.ID); err != nil || got.State != api.CommitmentOpen {
		t.Fatalf("closed by a member that does not decide: %+v %v", got, err)
	}
	if kept := caseOf(t, s, "acc", c.ThreadID); kept.RuleReason != api.BoardReasonKept {
		t.Fatalf("kept: %+v", kept)
	}
	// An inbound deciding member closes nothing either.
	markDirty(t, s, c.ThreadID)
	drainAll(t, s, boardNow, func(th *BoardThread) (BoardVerdict, error) {
		v, err := noCaseDecider(th)
		v.DecidingMine = false
		return v, err
	})
	if got, _ := s.GetBoardCommitment(ctx, k.ID); got.State != api.CommitmentOpen {
		t.Fatalf("closed by an inbound member: %+v", got)
	}
	// The user's deciding member closes it, and the case, held by nothing
	// else, goes in the same pass.
	markDirty(t, s, c.ThreadID)
	drainAll(t, s, boardNow, noCaseDecider)
	if _, err := s.GetBoardCase(ctx, c.ID); err == nil {
		t.Fatal("the case outlived its only hold")
	}
}

// Marking a case live again, a remind that clears done and a merge that
// leaves it live open again the commitments done closed (row 2); one the
// user's reply closed stays closed.
func TestBoardUndoneReopensCommitments(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	mine1, mine2, c, _ := commitmentsThreadIn(t, s)
	replied, _, err := s.AddBoardCommitment(ctx, commitIn(c, mine1, "older", "an older promise"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`UPDATE board_commitments SET state = 'closed', closed_reason = 'replied', closed_at = ? WHERE id = ?`,
		stamp(boardNow.Add(-time.Hour)), replied.ID); err != nil {
		t.Fatal(err)
	}
	k, _, err := s.AddBoardCommitment(ctx, commitIn(caseOf(t, s, "acc", c.ThreadID), mine2, "send it", "I will send it"))
	if err != nil {
		t.Fatal(err)
	}
	state := func(id string) api.BoardCommitmentState {
		t.Helper()
		got, err := s.GetBoardCommitment(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return got.State
	}
	for _, undo := range []struct {
		name string
		do   func() error
	}{
		{"setDone false", func() error { _, err := s.SetBoardDone(ctx, c.ID, false, boardNow); return err }},
		{"remind", func() error { _, err := s.SetBoardRemind(ctx, c.ID, boardNow.Add(24*time.Hour)); return err }},
	} {
		if _, err := s.SetBoardDone(ctx, c.ID, true, boardNow); err != nil {
			t.Fatal(err)
		}
		if state(k.ID) != api.CommitmentClosed {
			t.Fatalf("%s: done did not close the commitment", undo.name)
		}
		if err := undo.do(); err != nil {
			t.Fatal(err)
		}
		if state(k.ID) != api.CommitmentOpen || state(replied.ID) != api.CommitmentClosed {
			t.Fatalf("%s: %s %s", undo.name, state(k.ID), state(replied.ID))
		}
		if _, err := s.SetBoardRemind(ctx, c.ID, time.Time{}); err != nil {
			t.Fatal(err)
		}
	}
}

// A merge of a done case into a live one: the merged case is live, its
// decisions kept, and the commitments done closed open again.
func TestBoardMergeOneDone(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	sent := seedFolder(t, s, "acc", "Sent", api.RoleSent)
	x, xm, cx := boardThreadWithMine(t, s, inbox, sent, 10, "x")
	y, _, cy := boardThreadWithMine(t, s, inbox, sent, 20, "y")
	k, _, err := s.AddBoardCommitment(ctx, BoardCommitmentInput{CaseID: cx.ID, InputKey: cx.InputKey, MessageID: xm.ID, Text: "x",
		Quote: "qx", Run: BoardRunRef{Source: "t", Day: "2026-09-04"}, Now: boardNow})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetBoardDone(ctx, cx.ID, true, boardNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetBoardUserState(ctx, cy.ID, api.BoardYou, boardNow); err != nil {
		t.Fatal(err)
	}
	both := boardMail(t, s, inbox, 30, "both", "x-re", "y", "y-re", "x", "x-re")
	sameThread(t, s, x, y, both)
	var survivor BoardCase
	for _, id := range []string{cx.ID, cy.ID} {
		if c, err := s.GetBoardCase(ctx, id); err == nil {
			survivor = c
		}
	}
	if survivor.ID == "" || !survivor.DoneAt.IsZero() || survivor.UserState != api.BoardYou || survivor.Visibility(boardNow) != api.BoardLive {
		t.Fatalf("merged: %+v", survivor)
	}
	if got, err := s.GetBoardCommitment(ctx, k.ID); err != nil || got.State != api.CommitmentOpen || got.CaseID != survivor.ID {
		t.Fatalf("commitment after the merge: %+v %v", got, err)
	}
}

// A done case that reopens sheds the user's state set before (row 27); a
// case done long ago is pruned whatever state the user had given it.
func TestBoardReopenClearsUserStateAndPrune(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	a := boardMail(t, s, inbox, 1, "a", "")
	drainAll(t, s, boardNow, seenDecider)
	c := caseOf(t, s, "acc", a.ThreadID)
	if _, err := s.SetBoardUserState(ctx, c.ID, api.BoardInfo, boardNow); err != nil {
		t.Fatal(err)
	}
	doneAt := time.Now().UTC()
	if _, err := s.SetBoardDone(ctx, c.ID, true, doneAt); err != nil {
		t.Fatal(err)
	}
	storeTick()
	reply := seedThread(t, s, inbox, Message{UID: 2, RFCMessageID: "b", InReplyTo: "a", Bulk: "none", Date: time.Now().UTC()})
	sameThread(t, s, a, reply)
	drainAll(t, s, boardNow, seenDecider)
	got := caseOf(t, s, "acc", a.ThreadID)
	if !got.DoneAt.IsZero() || got.UserState != "" || !got.UserStateAt.IsZero() {
		t.Fatalf("reopened: done %v state %q at %v", got.DoneAt, got.UserState, got.UserStateAt)
	}

	// Done long ago with a state: pruned.
	if _, err := s.SetBoardUserState(ctx, c.ID, api.BoardHot, boardNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetBoardDone(ctx, c.ID, true, boardNow); err != nil {
		t.Fatal(err)
	}
	later := time.Now().UTC().Add(400 * 24 * time.Hour)
	if accts, err := s.PruneBoardCases(ctx, BoardPrune{Before: later, DoneBefore: later, Now: later}); err != nil || len(accts) != 1 {
		t.Fatalf("prune: %v %v", accts, err)
	}
	if _, err := s.GetBoardCase(ctx, c.ID); err == nil {
		t.Fatal("a long-done case with a user state survived the prune")
	}
}

// An annotation's deadline keeps, lists and spares a case only while the
// assistant preference is on (row 29), and only until it passes.
func TestBoardDeadlineHoldNeedsAssistant(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	a := boardMail(t, s, inbox, 1, "a", "")
	drainAll(t, s, boardNow, testDecider)
	c := caseOf(t, s, "acc", a.ThreadID)
	due := boardNow.Add(48 * time.Hour)
	if _, _, err := s.AnnotateBoardCase(ctx, BoardAnnotationInput{CaseID: c.ID, InputKey: c.InputKey, Title: "T",
		Due: &BoardDue{At: due, Quote: "by Friday", MessageID: a.ID}, Run: BoardRunRef{Source: "t"}, Now: boardNow}); err != nil {
		t.Fatal(err)
	}
	// Listed past its window only with the assistant on.
	far := boardNow // the case's date is 2026-09-01; a one-day window
	q := BoardListQuery{AccountIDs: []string{"acc"}, Now: far, Windows: BoardWindowDays{Hot: 1, You: 1, Them: 1, Info: 1}}
	if listedIDs(t, s, q)[c.ID] {
		t.Fatal("listed for a deadline with the assistant off")
	}
	q.Assistant = true
	if !listedIDs(t, s, q)[c.ID] {
		t.Fatal("not listed for a deadline with the assistant on")
	}
	// Spared by the prune only with the assistant on.
	prune := BoardPrune{Before: boardNow, DoneBefore: boardNow.Add(-30 * 24 * time.Hour), Now: boardNow, Assistant: true}
	if accts, err := s.PruneBoardCases(ctx, prune); err != nil || len(accts) != 0 {
		t.Fatalf("pruned with the assistant on: %v %v", accts, err)
	}
	// Kept by the drain only with the assistant on, and only until it passes.
	drop := func(now time.Time, assistant bool) {
		t.Helper()
		markDirty(t, s, a.ThreadID)
		if _, err := s.DrainBoard(ctx, BoardDrainOptions{Now: now, Assistant: assistant}, noCaseDecider); err != nil {
			t.Fatal(err)
		}
	}
	drop(boardNow, true)
	if kept := caseOf(t, s, "acc", a.ThreadID); kept.RuleReason != api.BoardReasonKept {
		t.Fatalf("not kept for its deadline: %+v", kept)
	}
	drop(due.Add(time.Hour), true)
	if _, err := s.GetBoardCase(ctx, c.ID); err == nil {
		t.Fatal("kept after its deadline passed")
	}

	// Again, with the assistant off: dropped at once.
	b := boardMail(t, s, inbox, 5, "b", "")
	drainAll(t, s, boardNow, testDecider)
	cb := caseOf(t, s, "acc", b.ThreadID)
	if _, _, err := s.AnnotateBoardCase(ctx, BoardAnnotationInput{CaseID: cb.ID, InputKey: cb.InputKey, Title: "T",
		Due: &BoardDue{At: due, Quote: "by Friday", MessageID: b.ID}, Run: BoardRunRef{Source: "t"}, Now: boardNow}); err != nil {
		t.Fatal(err)
	}
	prune.Assistant = false
	if accts, err := s.PruneBoardCases(ctx, prune); err != nil || len(accts) != 1 {
		t.Fatalf("not pruned with the assistant off: %v %v", accts, err)
	}
}

// A thread the decider must fetch something for stays dirty in the
// drain's own transaction (row 53); a plain Skip leaves the set.
func TestBoardSkipKeepDirty(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	a := boardMail(t, s, inbox, 1, "a", "")
	d, err := s.DrainBoard(ctx, BoardDrainOptions{Now: boardNow}, func(*BoardThread) (BoardVerdict, error) {
		return BoardVerdict{Skip: true, KeepDirty: true}, nil
	})
	if err != nil || d.Threads != 1 || !d.More {
		t.Fatalf("drain: %+v %v", d, err)
	}
	if !dirtyThreads(t, s)["acc/"+a.ThreadID] {
		t.Fatal("the thread left the dirty set")
	}
	if _, err := s.DrainBoard(ctx, BoardDrainOptions{Now: boardNow}, func(*BoardThread) (BoardVerdict, error) {
		return BoardVerdict{Skip: true}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(dirtyThreads(t, s)) != 0 {
		t.Fatal("a plain skip stayed dirty")
	}
}

// Ticking a commitment off or recording one raises its case's version.
func TestBoardCommitmentBumpsVersion(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	mine1, _, c := commitmentsThread(t, s)
	k, _, err := s.AddBoardCommitment(ctx, commitIn(c, mine1, "send it", "I will send it"))
	if err != nil {
		t.Fatal(err)
	}
	v1 := caseOf(t, s, "acc", c.ThreadID).Version
	if v1 != c.Version+1 {
		t.Fatalf("version after recording: %d, was %d", v1, c.Version)
	}
	if _, err := s.SetBoardCommitment(ctx, k.ID, true, boardNow); err != nil {
		t.Fatal(err)
	}
	v2 := caseOf(t, s, "acc", c.ThreadID).Version
	if _, err := s.SetBoardCommitment(ctx, k.ID, true, boardNow); err != nil {
		t.Fatal(err)
	}
	v3 := caseOf(t, s, "acc", c.ThreadID).Version
	if v2 != v1+1 || v3 != v2 {
		t.Fatalf("versions: %d %d %d", v1, v2, v3)
	}
	if _, err := s.SetBoardCommitment(ctx, "k_nope", true, boardNow); err == nil {
		t.Fatal("unknown commitment")
	}
}

// remindedAt: set when the remind comes due, ended by the user's acts; a
// remind that came due stays live when the clock goes back.
func TestBoardRemindedAt(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	a := boardMail(t, s, inbox, 1, "a", "")
	drainAll(t, s, boardNow, testDecider)
	c := caseOf(t, s, "acc", a.ThreadID)
	until := boardNow.Add(time.Hour)
	fire := func() BoardCase {
		t.Helper()
		if _, err := s.SetBoardRemind(ctx, c.ID, until); err != nil {
			t.Fatal(err)
		}
		if got := caseOf(t, s, "acc", a.ThreadID); !got.RemindedAt(boardNow).IsZero() || got.Visibility(boardNow) != api.BoardSnoozed {
			t.Fatalf("snoozed: %+v", got)
		}
		if _, err := s.ClearDueBoardReminds(ctx, until); err != nil {
			t.Fatal(err)
		}
		got := caseOf(t, s, "acc", a.ThreadID)
		if !got.RemindedAt(until).Equal(until) {
			t.Fatalf("reminded: %+v", got)
		}
		return got
	}
	// The clock goes back before remind_at: live, still reminded, offered
	// to triage.
	back := fire()
	if back.Visibility(boardNow) != api.BoardLive || !back.RemindedAt(boardNow).Equal(until) {
		t.Fatalf("clock back: %s %v", back.Visibility(boardNow), back.RemindedAt(boardNow))
	}
	if n, err := s.CountBoardQueue(ctx, BoardQueueQuery{AccountIDs: []string{"acc"}, Now: boardNow,
		Windows: BoardWindowDays{Hot: 400, You: 400, Them: 400, Info: 400}}); err != nil || n != 1 {
		t.Fatalf("queue with the clock back: %d %v", n, err)
	}
	for _, act := range []struct {
		name string
		do   func() (BoardCase, error)
	}{
		{"setState", func() (BoardCase, error) { return s.SetBoardUserState(ctx, c.ID, api.BoardYou, boardNow) }},
		{"same state", func() (BoardCase, error) { return s.SetBoardUserState(ctx, c.ID, api.BoardYou, boardNow) }},
		{"setDone false", func() (BoardCase, error) { return s.SetBoardDone(ctx, c.ID, false, boardNow) }},
		{"remind null", func() (BoardCase, error) { return s.SetBoardRemind(ctx, c.ID, time.Time{}) }},
		{"unflag", func() (BoardCase, error) { return s.ClearBoardReminded(ctx, c.ID) }},
	} {
		before := fire()
		got, err := act.do()
		if err != nil {
			t.Fatal(err)
		}
		if got.Reminded || !got.RemindAt.IsZero() || !got.RemindedAt(until).IsZero() || got.Version <= before.Version {
			t.Fatalf("%s: %+v", act.name, got)
		}
	}
	before := fire()
	got, err := s.SetBoardDone(ctx, c.ID, true, boardNow)
	if err != nil || got.Reminded || !got.RemindedAt(until).IsZero() || got.Version <= before.Version {
		t.Fatalf("done: %+v %v", got, err)
	}
	// ClearBoardReminded leaves a case without a fired remind as it is.
	if r, err := s.ClearBoardReminded(ctx, c.ID); err != nil || r.Version != got.Version {
		t.Fatalf("nothing to clear: %+v %v", r, err)
	}
}

// Inbound mail stored after a remind was set ends it: early (live, not
// reminded) or, after it came due, the "reminded" mark. A copy of a
// message the case had, or old mail backfilled, does not. The members'
// stored-at stamps are written explicitly (storedAfter), a second after
// the remind's own, so that nothing hangs on the wall clock moving on
// between two calls.
func TestBoardRemindWake(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	archive := seedFolder(t, s, "acc", "Archive", api.RoleArchive)
	now := time.Now().UTC()
	a := seedThread(t, s, inbox, Message{UID: 1, RFCMessageID: "a", Bulk: "none", Date: now.Add(-time.Hour)})
	drainAll(t, s, now, seenDecider)
	c := caseOf(t, s, "acc", a.ThreadID)
	until := now.Add(24 * time.Hour)
	r, err := s.SetBoardRemind(ctx, c.ID, until)
	set := r.RemindSetAt()
	if err != nil || set.IsZero() || !r.SeenAtDone("a") {
		t.Fatalf("remind: set %v %v", set, err)
	}
	// A copy of a (another client's move) and old mail backfilled, both
	// stored after the remind was set.
	cp := seedThread(t, s, archive, Message{UID: 1, RFCMessageID: "a", Bulk: "none", Date: now.Add(-time.Hour)})
	old := seedThread(t, s, inbox, Message{UID: 2, RFCMessageID: "old", InReplyTo: "a", Bulk: "none", Date: now.Add(-30 * 24 * time.Hour)})
	sameThread(t, s, a, cp, old)
	storedAfter(t, s, set, cp, old)
	drainAll(t, s, now, seenDecider)
	if got := caseOf(t, s, "acc", a.ThreadID); got.Visibility(now) != api.BoardSnoozed {
		t.Fatalf("woken by a copy or old mail: %+v", got)
	}
	// New mail: live again, not reminded.
	b := seedThread(t, s, inbox, Message{UID: 3, RFCMessageID: "b", InReplyTo: "a", Bulk: "none", Date: now})
	sameThread(t, s, a, b)
	storedAfter(t, s, set, b)
	drainAll(t, s, now, seenDecider)
	got := caseOf(t, s, "acc", a.ThreadID)
	if got.Visibility(now) != api.BoardLive || !got.RemindAt.IsZero() || got.Reminded || !got.RemindedAt(now).IsZero() {
		t.Fatalf("not woken: %+v", got)
	}
	// After the remind came due: new mail ends the reminded mark.
	if r, err = s.SetBoardRemind(ctx, c.ID, until); err != nil {
		t.Fatal(err)
	}
	set = r.RemindSetAt()
	if _, err := s.ClearDueBoardReminds(ctx, until); err != nil {
		t.Fatal(err)
	}
	markDirty(t, s, a.ThreadID)
	drainAll(t, s, now, seenDecider)
	if got := caseOf(t, s, "acc", a.ThreadID); got.RemindedAt(until).IsZero() {
		t.Fatalf("the reminded mark ended without new mail: %+v", got)
	}
	d := seedThread(t, s, inbox, Message{UID: 4, RFCMessageID: "d", InReplyTo: "b", Bulk: "none", Date: now.Add(time.Minute)})
	sameThread(t, s, a, d)
	storedAfter(t, s, set, d)
	drainAll(t, s, now, seenDecider)
	if got := caseOf(t, s, "acc", a.ThreadID); got.Reminded || !got.RemindAt.IsZero() {
		t.Fatalf("still reminded after new mail: %+v", got)
	}
}

// storedAt writes the stored-at stamp (created_at) of the messages.
func storedAt(t *testing.T, s *Store, at time.Time, msgs ...*Message) {
	t.Helper()
	for _, m := range msgs {
		if _, err := s.db.ExecContext(context.Background(), `UPDATE messages SET created_at = ? WHERE id = ?`, stamp(at), m.ID); err != nil {
			t.Fatal(err)
		}
	}
}

// storedAfter stores the messages a second after at.
func storedAfter(t *testing.T, s *Store, at time.Time, msgs ...*Message) {
	t.Helper()
	storedAt(t, s, at.Add(time.Second), msgs...)
}

// The usage a client reported as a lower bound stays one, in the run and
// in the 24-hour sum.
func TestBoardRunUsageLowerBound(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	full, err := s.StartBoardRun(ctx, api.TriggerManual, "claude-code", boardNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EndBoardRun(ctx, full, "", &api.BoardUsage{InputTokens: 10}, boardNow); err != nil {
		t.Fatal(err)
	}
	if u, err := s.BoardRunUsage(ctx, boardNow.Add(-time.Hour)); err != nil || u.LowerBound || u.Runs != 1 {
		t.Fatalf("usage: %+v %v", u, err)
	}
	cut, err := s.StartBoardRun(ctx, api.TriggerAuto, "claude-code", boardNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EndBoardRun(ctx, cut, api.RunTimeout, &api.BoardUsage{InputTokens: 5, LowerBound: true}, boardNow); err != nil {
		t.Fatal(err)
	}
	r, err := s.GetBoardRun(ctx, cut)
	if err != nil || r.Usage == nil || !r.Usage.LowerBound || r.Day != "" {
		t.Fatalf("run: %+v %v", r, err)
	}
	if r, _ := s.GetBoardRun(ctx, full); r.Usage == nil || r.Usage.LowerBound {
		t.Fatalf("full run: %+v", r.Usage)
	}
	if u, err := s.BoardRunUsage(ctx, boardNow.Add(-time.Hour)); err != nil || !u.LowerBound || u.Runs != 2 || u.InputTokens != 15 {
		t.Fatalf("usage: %+v %v", u, err)
	}
}

// A change of the known correspondents marks only the cases with mail
// from the addresses concerned (From or Reply-To).
func TestBoardMarkCasesFrom(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	a := seedThread(t, s, inbox, Message{UID: 1, RFCMessageID: "a", Bulk: "none", From: []api.Address{{Address: "Carol@Example.invalid "}}})
	b := seedThread(t, s, inbox, Message{UID: 2, RFCMessageID: "b", Bulk: "none", From: []api.Address{{Address: "noreply@shop.invalid"}},
		ReplyTo: []api.Address{{Address: "help@shop.invalid"}}})
	cc := seedThread(t, s, inbox, Message{UID: 3, RFCMessageID: "c", Bulk: "none", From: []api.Address{{Address: "dave@example.invalid"}}})
	drainAll(t, s, boardNow, testDecider)
	clearDirty(t, s)
	if err := s.MarkBoardCasesFrom(ctx, []string{"acc"}, []string{"carol@example.invalid", "help@shop.invalid"}); err != nil {
		t.Fatal(err)
	}
	expectDirty(t, s, "changed senders", "acc/"+a.ThreadID, "acc/"+b.ThreadID)
	if err := s.MarkBoardCasesFrom(ctx, []string{"other"}, []string{"dave@example.invalid"}); err != nil {
		t.Fatal(err)
	}
	expectDirty(t, s, "another account")
	_ = cc
}
