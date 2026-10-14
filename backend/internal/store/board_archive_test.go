// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// archiveDecider is seenDecider telling also where the inbound members
// lie, as internal/board does: from every row of the thread by its role,
// the newest inbound member that counts as the reply target.
func archiveDecider(th *BoardThread) (BoardVerdict, error) {
	v, err := seenDecider(th)
	if err != nil {
		return v, err
	}
	var newest *BoardMember
	inbox := false
	for i := range th.Members {
		m := &th.Members[i]
		if !m.Counts || m.Mine || m.TwinOfMine {
			continue
		}
		newest = m
		inbox = inbox || m.Role == api.RoleInbox
	}
	v.CanArchive = inbox
	if newest == nil {
		return v, nil
	}
	v.ReplyMessageID, v.ReplyFolderID = newest.ID, newest.FolderID
	for i := range th.Members {
		m := &th.Members[i]
		if m.ID != newest.ID && (m.RFCMessageID == "" || m.RFCMessageID != newest.RFCMessageID) {
			continue
		}
		switch m.Role {
		case api.RoleInbox:
			v.NewestInboundInInbox = true
		case api.RoleArchive:
			v.Archived = true
		}
	}
	v.Archived = v.Archived && !inbox
	return v, nil
}

// The archive in the mail view (docs/api.md §4.13): a thread the user
// archives marks its live case done as SetBoardDone does (the remind
// cleared, the open commitments closed, the user's state kept); the way
// back to the board by hand sticks however often the archived thread is
// judged again; a message the case had, moved back into the inbox, makes
// a done case live again with the user's state; a message that arrives
// archived leaves a done case alone until it is moved into the inbox; a
// thread archived before the board saw it is no case; a case judged by
// rules that could not tell an archive is marked done at its first
// judgement by rules that can.
func TestBoardArchiveInMailView(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	sentF := seedFolder(t, s, "acc", "Sent", api.RoleSent)
	archive := seedFolder(t, s, "acc", "Archive", api.RoleArchive)
	now := time.Now().UTC()
	a := seedThread(t, s, inbox, Message{UID: 1, RFCMessageID: "a", Bulk: "none", Date: now.Add(-2 * time.Hour)})
	r := seedThread(t, s, sentF, Message{UID: 1, RFCMessageID: "r", InReplyTo: "a", Bulk: "none", Date: now.Add(-time.Hour)})
	sameThread(t, s, a, r)
	drainAll(t, s, now, archiveDecider)
	c := caseOf(t, s, "acc", a.ThreadID)
	if !c.CanArchive || c.Visibility(now) != api.BoardLive || c.ReplyFolderID != inbox.ID {
		t.Fatalf("case: %+v", c)
	}
	judge := func(what string) BoardCase {
		t.Helper()
		if err := s.MarkBoardThreadsDirty(ctx, "acc", []string{a.ThreadID}); err != nil {
			t.Fatal(err)
		}
		drainAll(t, s, now, archiveDecider)
		return caseOf(t, s, "acc", a.ThreadID)
	}
	move := func(m *Message, f Folder) {
		t.Helper()
		if _, err := s.MoveMessages(ctx, "acc", []string{m.ID}, f.ID); err != nil {
			t.Fatal(err)
		}
	}
	// A remind, the user's state and an open commitment, to see what the
	// archive does with them.
	if _, err := s.SetBoardRemind(ctx, c.ID, now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetBoardUserState(ctx, c.ID, api.BoardHot, now); err != nil {
		t.Fatal(err)
	}
	c = caseOf(t, s, "acc", a.ThreadID)
	k, _, err := s.AddBoardCommitment(ctx, BoardCommitmentInput{CaseID: c.ID, InputKey: c.InputKey, MessageID: r.ID,
		Text: "Send it", Quote: "I will send it", Source: "claude", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	commitment := func() BoardCommitment {
		t.Helper()
		list, err := s.BoardCommitments(ctx, c.ID)
		if err != nil || len(list) != 1 {
			t.Fatalf("commitments: %v %v", list, err)
		}
		return list[0]
	}

	// The user archives it in the mail view: done, as board.archive.
	move(a, archive)
	drainAll(t, s, now, archiveDecider)
	got := caseOf(t, s, "acc", a.ThreadID)
	if got.Visibility(now) != api.BoardDone || !got.DoneAt.Equal(now.Truncate(time.Millisecond)) || !got.RemindAt.IsZero() || got.Reminded ||
		got.UserState != api.BoardHot || got.CanArchive || got.ReplyFolderID != archive.ID || !got.SeenAtDone("a") {
		t.Fatalf("archived: %+v", got)
	}
	if k2 := commitment(); k2.ID != k.ID || k2.State != api.CommitmentClosed || k2.ClosedReason != api.CommitmentClosedDone {
		t.Fatalf("commitment after the archive: %+v", k2)
	}
	// Judged again (read, flagged): still done.
	if got := judge("read"); got.Visibility(now) != api.BoardDone {
		t.Fatalf("judged again: %+v", got)
	}
	// Back to the board by hand: live, and judged again it stays so.
	if _, err := s.SetBoardDone(ctx, c.ID, false, now); err != nil {
		t.Fatal(err)
	}
	if got := judge("back"); got.Visibility(now) != api.BoardLive {
		t.Fatalf("back on the board: %+v", got)
	}
	if k2 := commitment(); k2.State != api.CommitmentOpen {
		t.Fatalf("commitment after the way back: %+v", k2)
	}
	// Moved back into the inbox and archived again: done again.
	move(a, inbox)
	drainAll(t, s, now, archiveDecider)
	if got := caseOf(t, s, "acc", a.ThreadID); got.Visibility(now) != api.BoardLive || !got.CanArchive {
		t.Fatalf("in the inbox again: %+v", got)
	}
	move(a, archive)
	drainAll(t, s, now, archiveDecider)
	if got := caseOf(t, s, "acc", a.ThreadID); got.Visibility(now) != api.BoardDone {
		t.Fatalf("archived again: %+v", got)
	}
	if k2 := commitment(); k2.State != api.CommitmentClosed {
		t.Fatalf("commitment after the second archive: %+v", k2)
	}
	// Un-archived in the mail view: live, the user's state kept, the
	// commitment open again.
	move(a, inbox)
	drainAll(t, s, now, archiveDecider)
	if got := caseOf(t, s, "acc", a.ThreadID); got.Visibility(now) != api.BoardLive || got.UserState != api.BoardHot || !got.DoneAt.IsZero() {
		t.Fatalf("un-archived: %+v", got)
	}
	if k2 := commitment(); k2.State != api.CommitmentOpen {
		t.Fatalf("commitment after the un-archive: %+v", k2)
	}
	// Done by hand with the message in the inbox: judged again, it stays
	// done (nothing came back).
	if _, err := s.SetBoardDone(ctx, c.ID, true, now); err != nil {
		t.Fatal(err)
	}
	if got := judge("done by hand"); got.Visibility(now) != api.BoardDone {
		t.Fatalf("done by hand, judged again: %+v", got)
	}
	// A reply handled on the phone arrives archived: the done case stays
	// done; moved into the inbox, it reopens the case (new mail).
	move(a, archive)
	drainAll(t, s, now, archiveDecider)
	b := seedThread(t, s, archive, Message{UID: 2, RFCMessageID: "b", InReplyTo: "a", Bulk: "none", Date: now.Add(time.Hour)})
	sameThread(t, s, a, b)
	drainAll(t, s, now, archiveDecider)
	if got := caseOf(t, s, "acc", a.ThreadID); got.Visibility(now) != api.BoardDone || got.SeenAtDone("b") {
		t.Fatalf("a reply that arrived archived: %+v", got)
	}
	move(b, inbox)
	drainAll(t, s, now, archiveDecider)
	if got := caseOf(t, s, "acc", a.ThreadID); got.Visibility(now) != api.BoardLive || got.UserState != "" {
		t.Fatalf("the reply moved into the inbox: %+v", got)
	}

	// A thread archived before the board saw it is no case until a
	// message of it is in the inbox.
	o := seedThread(t, s, archive, Message{UID: 3, RFCMessageID: "o", Bulk: "none", Date: now})
	drainAll(t, s, now, archiveDecider)
	if _, err := s.BoardCaseByThread(ctx, "acc", o.ThreadID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a thread archived already: %v", err)
	}
	move(o, inbox)
	drainAll(t, s, now, archiveDecider)
	oc := caseOf(t, s, "acc", o.ThreadID)
	if oc.Visibility(now) != api.BoardLive {
		t.Fatalf("moved into the inbox: %+v", oc)
	}
	// Judged by rules that could not tell an archive (before 7), its
	// thread archived and the case put back by hand: the first judgement
	// by rules that can marks it done, whatever the columns say.
	move(o, archive)
	drainAll(t, s, now, archiveDecider)
	if _, err := s.SetBoardDone(ctx, oc.ID, false, now); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkBoardThreadsDirty(ctx, "acc", []string{o.ThreadID}); err != nil {
		t.Fatal(err)
	}
	drainAll(t, s, now, archiveDecider)
	if got := caseOf(t, s, "acc", o.ThreadID); got.Visibility(now) != api.BoardLive {
		t.Fatalf("put back, judged again by current rules: %+v", got)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE board_cases SET rules_version = '6' WHERE id = ?`, oc.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkBoardThreadsDirty(ctx, "acc", []string{o.ThreadID}); err != nil {
		t.Fatal(err)
	}
	drainAll(t, s, now, archiveDecider)
	if got := caseOf(t, s, "acc", o.ThreadID); got.Visibility(now) != api.BoardDone || got.RulesVersion != "test" {
		t.Fatalf("first judged by rules that tell an archive: %+v", got)
	}
}

// A version that is no number (a test's, an empty one) counts as current:
// nothing is marked done on a guess.
func TestBoardRulesSawArchive(t *testing.T) {
	for v, want := range map[string]bool{"": true, "6": false, "7": true, "12": true, "test": true} {
		if got := boardRulesSawArchive(v); got != want {
			t.Errorf("%q: %v, want %v", v, got, want)
		}
	}
}
