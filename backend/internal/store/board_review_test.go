// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The fixes after the review of the board's store layer.

// seenDecider is testDecider that also names the Message-ID of the
// inbound member it reports, as core does.
func seenDecider(th *BoardThread) (BoardVerdict, error) {
	v, err := testDecider(th)
	for i := len(th.Members) - 1; i >= 0; i-- {
		if m := th.Members[i]; m.Counts && !m.Mine && !m.TwinOfMine {
			v.NewestInboundStored, v.NewestInboundDate, v.NewestInboundMessageID = m.StoredAt, m.Date, m.RFCMessageID
			break
		}
	}
	return v, err
}

func listedIDs(t *testing.T, s *Store, q BoardListQuery) map[string]bool {
	t.Helper()
	l, err := s.ListBoard(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, c := range l.Cases {
		out[c.ID] = true
	}
	return out
}

// A move by another client deletes the row before the copy arrives: the
// case is kept off the board with the user's decisions, the copy does not
// reopen it (its Message-ID was there at done time), a new message does.
// An orphan the members never come back to is pruned after the grace.
func TestBoardOrphanAndMoveByAnotherClient(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	archive := seedFolder(t, s, "acc", "Archive", api.RoleArchive)
	recent := boardNow.Add(-time.Hour)
	a := seedThread(t, s, inbox, Message{UID: 1, RFCMessageID: "a", Bulk: "none", Date: recent, InternalDate: recent})
	drainAll(t, s, boardNow, seenDecider)
	c := caseOf(t, s, "acc", a.ThreadID)
	if _, err := s.SetBoardDone(ctx, c.ID, true, boardNow); err != nil {
		t.Fatal(err)
	}
	done := caseOf(t, s, "acc", a.ThreadID)
	if !done.SeenAtDone("<a>") || !done.SeenAtDone("a") || done.SeenAtDone("b") || done.SeenAtDone("") {
		t.Fatalf("done_seen: %q", done.doneSeen)
	}

	// The phone archives it: the inbox row goes first.
	if _, err := s.DeleteMessages(ctx, "acc", []string{a.ID}); err != nil {
		t.Fatal(err)
	}
	drainAll(t, s, boardNow, seenDecider)
	orphan, err := s.GetBoardCase(ctx, c.ID)
	if err != nil || orphan.OrphanedAt.IsZero() || orphan.DoneAt.IsZero() {
		t.Fatalf("orphan: %+v %v", orphan, err)
	}
	q := BoardListQuery{AccountIDs: []string{"acc"}, Now: boardNow, Windows: boardWindows}
	if listedIDs(t, s, q)[c.ID] {
		t.Error("an orphan is listed")
	}
	// The copy arrives in the archive: stored now (after done), dated an
	// hour before done, in a thread of its own, which takes the case over.
	cp := seedThread(t, s, archive, Message{UID: 1, RFCMessageID: "a", Bulk: "none", Date: recent, InternalDate: recent})
	if cp.ThreadID == a.ThreadID {
		t.Fatal("the copy kept the old thread; the test needs a new one")
	}
	drainAll(t, s, boardNow, seenDecider)
	back, err := s.GetBoardCase(ctx, c.ID)
	if err != nil || !back.OrphanedAt.IsZero() || back.DoneAt.IsZero() || back.ThreadID != cp.ThreadID || back.LatestMessageID != cp.ID {
		t.Fatalf("after the copy arrived: %+v %v", back, err)
	}
	if !listedIDs(t, s, q)[c.ID] {
		t.Error("the case is not listed again")
	}
	// A new inbound message reopens it.
	n := seedThread(t, s, archive, Message{UID: 2, RFCMessageID: "n", InReplyTo: "a", Bulk: "none", Date: boardNow.Add(time.Hour),
		InternalDate: boardNow.Add(time.Hour)})
	sameThread(t, s, cp, n)
	drainAll(t, s, boardNow, seenDecider)
	if got := caseOf(t, s, "acc", n.ThreadID); got.ID != c.ID || !got.DoneAt.IsZero() || got.SeenAtDone("a") {
		t.Fatalf("new mail did not reopen: %+v", got)
	}

	// An orphan kept by a user state is pruned after the grace, not before.
	b := boardMail(t, s, inbox, 5, "b", "")
	drainAll(t, s, boardNow, seenDecider)
	cb := caseOf(t, s, "acc", b.ThreadID)
	if _, err := s.SetBoardUserState(ctx, cb.ID, api.BoardHot, boardNow); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SetMessageHidden(ctx, b.ID, true); err != nil {
		t.Fatal(err)
	}
	drainAll(t, s, boardNow, seenDecider)
	prune := BoardPrune{Before: boardNow.Add(-365 * 24 * time.Hour), DoneBefore: boardNow.Add(-30 * 24 * time.Hour)}
	prune.Now = boardNow.Add(BoardOrphanGrace - time.Minute)
	if accts, err := s.PruneBoardCases(ctx, prune); err != nil || len(accts) != 0 {
		t.Fatalf("pruned within the grace: %v %v", accts, err)
	}
	prune.Now = boardNow.Add(BoardOrphanGrace + time.Minute)
	if accts, err := s.PruneBoardCases(ctx, prune); err != nil || fmt.Sprint(accts) != "[acc]" {
		t.Fatalf("prune after the grace: %v %v", accts, err)
	}
	if _, err := s.GetBoardCase(ctx, cb.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("orphan after the grace: %v", err)
	}
	if _, err := s.GetBoardCase(ctx, c.ID); err != nil {
		t.Fatalf("a case with members pruned: %v", err)
	}
}

// A remind that came due keeps its case listed and kept, beyond its window,
// until the user marks it done or ends the remind.
func TestBoardFiredRemindKeepsCase(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	trash := seedFolder(t, s, "acc", "Trash", api.RoleTrash)
	a := boardMail(t, s, inbox, 1, "a", "") // dated threadBase, three days before boardNow
	drainAll(t, s, boardNow, testDecider)
	c := caseOf(t, s, "acc", a.ThreadID)
	until := boardNow.Add(time.Hour)
	if _, err := s.SetBoardRemind(ctx, c.ID, until); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClearDueBoardReminds(ctx, until); err != nil {
		t.Fatal(err)
	}
	later := until.Add(time.Minute)
	short := BoardWindowDays{Hot: 1, You: 1, Them: 1, Info: 1}
	q := BoardListQuery{AccountIDs: []string{"acc"}, Now: later, Windows: short}
	if !listedIDs(t, s, q)[c.ID] {
		t.Fatal("a reminded case out of its window is not listed")
	}
	if n, _ := s.CountBoardQueue(ctx, BoardQueueQuery{AccountIDs: []string{"acc"}, Now: later, Windows: short}); n != 1 {
		t.Fatalf("a reminded case is not in the queue: %d", n)
	}
	// The rules drop it: kept.
	if _, err := s.MoveMessages(ctx, "acc", []string{a.ID}, trash.ID); err != nil {
		t.Fatal(err)
	}
	drainAll(t, s, later, testDecider)
	if got := caseOf(t, s, "acc", a.ThreadID); got.RuleReason != api.BoardReasonKept {
		t.Fatalf("reminded case not kept: %+v", got)
	}
	prune := BoardPrune{Before: later, DoneBefore: later, Now: later}
	if accts, _ := s.PruneBoardCases(ctx, prune); len(accts) != 0 {
		t.Fatal("a reminded case pruned")
	}
	// The user ends the remind: nothing keeps it any more.
	if _, err := s.SetBoardRemind(ctx, c.ID, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if listedIDs(t, s, q)[c.ID] {
		t.Error("listed after the remind ended")
	}
	if accts, _ := s.PruneBoardCases(ctx, prune); len(accts) != 1 {
		t.Fatal("not pruned after the remind ended")
	}
}

// No case row for a thread older than the longest window; an existing
// case is still updated, and a new message makes the thread a case.
func TestBoardCreationWindow(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	since := boardNow.Add(-90 * 24 * time.Hour)
	drain := func() {
		t.Helper()
		for {
			d, err := s.DrainBoard(ctx, BoardDrainOptions{Now: boardNow, Since: since}, testDecider)
			if err != nil || len(d.Failed) > 0 {
				t.Fatal(err, d.Failed)
			}
			if !d.More {
				return
			}
		}
	}
	old := seedThread(t, s, inbox, Message{UID: 1, RFCMessageID: "old", Bulk: "none", Date: boardNow.Add(-200 * 24 * time.Hour)})
	kept := seedThread(t, s, inbox, Message{UID: 2, RFCMessageID: "kept", Bulk: "none", Date: boardNow.Add(-200 * 24 * time.Hour)})
	drainAll(t, s, boardNow, testDecider) // no window: kept gets a case
	ck := caseOf(t, s, "acc", kept.ThreadID)
	if _, err := s.DB().Exec(`DELETE FROM board_cases WHERE thread_id = ?`, old.ThreadID); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkBoardAccountDirty(ctx, "acc", time.Time{}); err != nil {
		t.Fatal(err)
	}
	drain()
	if _, err := s.BoardCaseByThread(ctx, "acc", old.ThreadID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old thread got a case: %v", err)
	}
	if err := s.FlagMessages(ctx, "acc", []string{kept.ID}, []api.Flag{api.FlagFlagged}, nil); err != nil {
		t.Fatal(err)
	}
	drain()
	if got := caseOf(t, s, "acc", kept.ThreadID); got.ID != ck.ID || got.RuleState != api.BoardHot {
		t.Fatalf("existing old case not updated: %+v", got)
	}
	reply := boardMail(t, s, inbox, 3, "new", "old", "old")
	if _, err := s.DB().Exec(`UPDATE messages SET date = ? WHERE id = ?`, stamp(boardNow), reply.ID); err != nil {
		t.Fatal(err)
	}
	sameThread(t, s, old, reply)
	drain()
	caseOf(t, s, "acc", reply.ThreadID)
}

// A batch ends at its member budget (at least one thread each); nothing
// dirty means nothing to do.
func TestBoardDrainBudgets(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	if d, err := s.DrainBoard(ctx, BoardDrainOptions{Now: boardNow}, testDecider); err != nil || d.Threads != 0 || d.More {
		t.Fatalf("empty drain: %+v %v", d, err)
	}
	for i := 0; i < 3; i++ {
		boardMail(t, s, inbox, uint32(i+1), fmt.Sprintf("m%d", i), "")
	}
	// The budget option runs on a clock that advances a millisecond per
	// reading: the real one can stand still across a thread on Windows.
	tick := boardNow
	ticking := func() time.Time { tick = tick.Add(time.Millisecond); return tick }
	for i, opt := range []BoardDrainOptions{{Now: boardNow, MaxMembers: 1}, {Now: boardNow, Budget: time.Millisecond, Clock: ticking}} {
		if err := s.MarkBoardAccountDirty(ctx, "acc", time.Time{}); err != nil {
			t.Fatal(err)
		}
		for j := 0; j < 3; j++ {
			d, err := s.DrainBoard(ctx, opt, testDecider)
			if err != nil || d.Threads != 1 || d.More != (j < 2) {
				t.Fatalf("option %d batch %d: %+v %v", i, j, d, err)
			}
		}
	}
}

// The merge hook reads plain columns only: a case row whose JSON has
// another shape does not fail the mail write. Two links to drafts that do
// not exist: one link stays, nothing is reported. Two done cases keep both
// Message-ID sets.
func TestBoardMergeHookIsPlain(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	x := boardMail(t, s, inbox, 1, "x", "")
	y := boardMail(t, s, inbox, 2, "y", "")
	drainAll(t, s, boardNow, testDecider)
	cx, cy := caseOf(t, s, "acc", x.ThreadID), caseOf(t, s, "acc", y.ThreadID)
	for _, c := range []BoardCase{cx, cy} {
		if _, _, err := s.AnnotateBoardCase(ctx, BoardAnnotationInput{CaseID: c.ID, InputKey: c.InputKey, DraftID: "d_" + c.ID, Source: "t", Now: boardNow}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SetBoardDone(ctx, c.ID, true, boardNow); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DB().Exec(`UPDATE board_cases SET person_json = '[1]'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`UPDATE board_annotations SET tasks_json = '{"x":1}'`); err != nil {
		t.Fatal(err)
	}
	both := boardMail(t, s, inbox, 3, "both", "x", "y", "x")
	merged := sameThread(t, s, x, y, both)
	if n := countRows(t, s, `SELECT COUNT(*) FROM board_cases WHERE thread_id = ?`, merged); n != 1 {
		t.Fatalf("cases on the merged thread: %d", n)
	}
	if _, err := s.DB().Exec(`UPDATE board_cases SET person_json = '{}'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`UPDATE board_annotations SET tasks_json = '[]'`); err != nil {
		t.Fatal(err)
	}
	survivor := caseOf(t, s, "acc", merged)
	if survivor.DoneAt.IsZero() || !survivor.SeenAtDone("x") || !survivor.SeenAtDone("y") {
		t.Fatalf("merged done case: %+v %q", survivor, survivor.doneSeen)
	}
	if survivor.DraftID != "d_"+survivor.ID {
		t.Fatalf("survivor's link: %q", survivor.DraftID)
	}
	// Neither linked draft exists: nothing lost its case, nothing is
	// reported (a line would announce a draft that is not there).
	if raw, found, err := s.GetMeta(ctx, MetaBoardUnlinkedDrafts); err != nil || found {
		t.Fatalf("recorded: %q %v", raw, err)
	}
	if got, err := s.TakeBoardUnlinkedDrafts(ctx); err != nil || len(got) != 0 {
		t.Fatalf("unlinked drafts: %+v %v", got, err)
	}
}

// board.queue takes any number of case ids.
func TestBoardQueueManyCaseIDs(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	a := boardMail(t, s, inbox, 1, "a", "")
	drainAll(t, s, boardNow, testDecider)
	c := caseOf(t, s, "acc", a.ThreadID)
	ids := make([]string, 0, 40001)
	for i := 0; i < 40000; i++ {
		ids = append(ids, fmt.Sprintf("c_%032d", i))
	}
	ids = append(ids, c.ID, c.ID)
	q := BoardQueueQuery{AccountIDs: []string{"acc"}, CaseIDs: ids, Now: boardNow, Windows: boardWindows}
	items, remaining, err := s.BoardQueue(ctx, q)
	if err != nil || len(items) != 1 || items[0].Case.ID != c.ID || remaining != 0 {
		t.Fatalf("queue of 40000 ids: %d %d %v", len(items), remaining, err)
	}
	if n, err := s.CountBoardQueue(ctx, q); err != nil || n != 1 {
		t.Fatalf("count: %d %v", n, err)
	}
}

// A commitment recorded on an older message of the user's stays open until
// the user writes again, whatever re-evaluates the thread meanwhile.
func TestBoardCommitmentOnOlderMessage(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	sent := seedFolder(t, s, "acc", "Sent", api.RoleSent)
	in := boardMail(t, s, inbox, 1, "a", "")
	mine1 := boardMail(t, s, sent, 2, "r1", "a", "a")
	in2 := boardMail(t, s, inbox, 3, "a2", "r1", "a", "r1")
	mine2 := boardMail(t, s, sent, 4, "r2", "a2", "a", "a2")
	tid := sameThread(t, s, in, mine1, in2, mine2)
	drainAll(t, s, boardNow, testDecider)
	c := caseOf(t, s, "acc", tid)
	k, _, err := s.AddBoardCommitment(ctx, BoardCommitmentInput{CaseID: c.ID, InputKey: c.InputKey, MessageID: mine1.ID,
		Text: "send it", Quote: "I will send it", Source: "t", Now: boardNow})
	if err != nil || !k.RepliedAfter.Equal(mine2.Date) || !k.MessageDate.Equal(mine1.Date) {
		t.Fatalf("commit: %+v %v", k, err)
	}
	if err := s.FlagMessages(ctx, "acc", []string{in.ID}, []api.Flag{api.FlagFlagged}, nil); err != nil {
		t.Fatal(err)
	}
	drainAll(t, s, boardNow, testDecider)
	if got, _ := s.GetBoardCommitment(ctx, k.ID); got.State != api.CommitmentOpen || !got.RepliedAfter.Equal(mine2.Date) {
		t.Fatalf("closed by an unrelated change: %+v", got)
	}
	mine3 := boardMail(t, s, sent, 5, "r3", "r2", "a", "r2")
	sameThread(t, s, in, mine3)
	drainAll(t, s, boardNow, testDecider)
	if got, _ := s.GetBoardCommitment(ctx, k.ID); got.State != api.CommitmentClosed || got.ClosedReason != api.CommitmentClosedReplied {
		t.Fatalf("not closed by a newer message: %+v", got)
	}
}

// lastRun is the run of the latest activity: an open run, else the latest
// end (an external run's latest call).
func TestBoardLastRunByActivity(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	day := boardNow.Truncate(24 * time.Hour)
	ext, err := s.CountBoardRejected(ctx, BoardRunRef{Source: "desktop", Day: "d"}, day)
	if err != nil {
		t.Fatal(err)
	}
	manual, err := s.StartBoardRun(ctx, api.TriggerManual, "app", day.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EndBoardRun(ctx, manual, "", nil, day.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.BoardRunStats(ctx, day); st.LastRun == nil || st.LastRun.ID != manual {
		t.Fatalf("last run: %+v", st.LastRun)
	}
	// The external run is called again later in the day.
	if _, err := s.CountBoardRejected(ctx, BoardRunRef{Source: "desktop", Day: "d"}, day.Add(5*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.BoardRunStats(ctx, day); st.LastRun == nil || st.LastRun.ID != ext {
		t.Fatalf("last run after the later call: %+v", st.LastRun)
	}
	open, err := s.StartBoardRun(ctx, api.TriggerAuto, "app", day.Add(3*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := s.BoardRunStats(ctx, day); st.LastRun == nil || st.LastRun.ID != open {
		t.Fatalf("last run with one open: %+v", st.LastRun)
	}
}

// Members carry Reply-To; a Jira comment a bot relayed is ordered and
// timed by the site, never by the date in its text.
func TestBoardMemberReplyToAndItemTime(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	seedThread(t, s, inbox, Message{UID: 1, RFCMessageID: "a", Bulk: "none",
		ReplyTo: []api.Address{{Name: "List", Address: "list@example.invalid"}}})
	var mail []BoardMember
	drainAll(t, s, boardNow, func(th *BoardThread) (BoardVerdict, error) {
		mail = th.Members
		return testDecider(th)
	})
	if len(mail) != 1 || len(mail[0].ReplyTo) != 1 || mail[0].ReplyTo[0].Address != "list@example.invalid" || !mail[0].ItemAt.IsZero() {
		t.Fatalf("mail member: %+v", mail)
	}

	space, _, _ := jiraFolders(t, s, "jira")
	at := boardNow.Add(-time.Hour)
	desc := seedItem(t, s, space, "7", "i:7", "ITSD-7: Printer", at)
	plain := seedItem(t, s, space, "7", "c:1", "ITSD-7: Printer", at.Add(30*time.Minute))
	relayed := seedItem(t, s, space, "7", "c:2", "ITSD-7: Printer", at.Add(-100*24*time.Hour)) // the date the bot's text claims
	if err := s.PutIssue(ctx, Issue{AccountID: "jira", IssueID: "7", Key: "ITSD-7", SpaceID: "10001", Status: "Open"}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutIssueItems(ctx, "jira", []IssueItem{
		{RemoteID: "i:7", IssueID: "7", Kind: api.IssueItemDescription, AuthorID: "u1", Updated: at},
		{RemoteID: "c:1", IssueID: "7", Kind: api.IssueItemComment, AuthorID: "u1", Updated: at.Add(30 * time.Minute)},
		{RemoteID: "c:2", IssueID: "7", Kind: api.IssueItemComment, AuthorID: "bot", Via: "Issue Sync", Updated: at.Add(time.Hour)},
	}); err != nil {
		t.Fatal(err)
	}
	var items []BoardMember
	drainAll(t, s, boardNow, func(th *BoardThread) (BoardVerdict, error) {
		if th.Issue != nil {
			items = th.Members
		}
		return testDecider(th)
	})
	var order []string
	for _, m := range items {
		order = append(order, m.ID)
	}
	if fmt.Sprint(order) != fmt.Sprint([]string{desc.ID, plain.ID, relayed.ID}) {
		t.Fatalf("issue members %v, want description, comment, relayed comment", order)
	}
	if !items[0].ItemAt.Equal(at) || !items[1].ItemAt.Equal(at.Add(30*time.Minute)) || !items[2].ItemAt.Equal(at.Add(time.Hour)) ||
		items[2].ItemVia != "Issue Sync" || items[0].ItemVia != "" {
		t.Fatalf("item times: %v %v %v %q", items[0].ItemAt, items[1].ItemAt, items[2].ItemAt, items[2].ItemVia)
	}
}

func TestBoardDraftLinked(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	a := boardMail(t, s, inbox, 1, "a", "")
	drainAll(t, s, boardNow, testDecider)
	c := caseOf(t, s, "acc", a.ThreadID)
	if _, _, err := s.AnnotateBoardCase(ctx, BoardAnnotationInput{CaseID: c.ID, InputKey: c.InputKey, DraftID: "d_1", Source: "t", Now: boardNow}); err != nil {
		t.Fatal(err)
	}
	for _, x := range []struct {
		account, draft string
		want           bool
	}{{"acc", "d_1", true}, {"other", "d_1", false}, {"acc", "d_2", false}, {"acc", "", false}} {
		if got, err := s.BoardDraftLinked(ctx, x.account, x.draft); err != nil || got != x.want {
			t.Errorf("%s/%s: %v %v", x.account, x.draft, got, err)
		}
	}
}

func TestBoardKnownCorrespondents(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	sent := seedFolder(t, s, "acc", "Sent", api.RoleSent)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	other := seedFolder(t, s, "other", "Sent", api.RoleSent)
	outbox, err := s.OutboxFolder(ctx, "acc")
	if err != nil {
		t.Fatal(err)
	}
	addr := func(a ...string) []api.Address {
		var out []api.Address
		for _, x := range a {
			out = append(out, api.Address{Address: x})
		}
		return out
	}
	seedThread(t, s, sent, Message{UID: 1, RFCMessageID: "s1", To: addr("Old@Example.invalid"), Date: threadBase})
	seedThread(t, s, sent, Message{UID: 2, RFCMessageID: "s2", To: addr("bob@example.invalid"), CC: addr(" CAROL@example.invalid ", ""),
		Date: threadBase.Add(time.Hour)})
	seedThread(t, s, outbox, Message{UID: 3, RFCMessageID: "o1", To: addr("dave@example.invalid", "old@example.invalid"), Date: threadBase.Add(2 * time.Hour)})
	seedThread(t, s, inbox, Message{UID: 4, RFCMessageID: "i1", To: addr("me@example.invalid"), CC: addr("eve@example.invalid"), Date: boardNow})
	seedThread(t, s, other, Message{UID: 1, RFCMessageID: "x1", To: addr("frank@example.invalid"), Date: boardNow})
	junk := seedThread(t, s, sent, Message{UID: 5, RFCMessageID: "s3", Date: threadBase.Add(-time.Hour)})
	if _, err := s.DB().Exec(`UPDATE messages SET to_json = '["text", 7, {"name": "no address"}, {"address": "grace@example.invalid"}]' WHERE id = ?`, junk.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.BoardKnownCorrespondents(ctx, []string{"acc"}, 0)
	want := []string{"dave@example.invalid", "old@example.invalid", "bob@example.invalid", "carol@example.invalid", "grace@example.invalid"}
	if err != nil || fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("known: %v %v, want %v", got, err, want)
	}
	if got, _ := s.BoardKnownCorrespondents(ctx, []string{"acc"}, 2); len(got) != 2 || got[0] != "dave@example.invalid" {
		t.Fatalf("limited: %v", got)
	}
	if got, _ := s.BoardKnownCorrespondents(ctx, []string{"acc", "other"}, 0); len(got) != 6 || got[0] != "frank@example.invalid" {
		t.Fatalf("two accounts: %v", got)
	}
	if got, err := s.BoardKnownCorrespondents(ctx, nil, 0); err != nil || len(got) != 0 {
		t.Fatalf("no accounts: %v %v", got, err)
	}
}

func TestCutUTF8(t *testing.T) {
	for _, x := range []struct {
		in   string
		n    int
		want string
	}{{"abc", 5, "abc"}, {"abc", 2, "ab"}, {"žluť", 3, "žl"}, {"žluť", 2, "ž"}, {"žluť", 1, ""}, {"abc", 0, ""}, {"abc", -1, ""}, {"", 0, ""}} {
		if got := CutUTF8(x.in, x.n); got != x.want {
			t.Errorf("CutUTF8(%q, %d) = %q, want %q", x.in, x.n, got, x.want)
		}
	}
	if s := strings.Repeat("€", 10); CutUTF8(s, 10) != strings.Repeat("€", 3) {
		t.Error("cut inside a character")
	}
}

// Members carry the first identifiers of their References; whatever the
// stored list holds, the store reads a bounded prefix of it and a
// malformed list is none.
func TestBoardMemberReferences(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	sent := seedFolder(t, s, "acc", "Sent", api.RoleSent)

	a := boardMail(t, s, inbox, 1, "a", "")
	r := boardMail(t, s, sent, 2, "r", "", "a")
	sameThread(t, s, a, r)
	huge := make([]string, 50_000)
	for i := range huge {
		huge[i] = fmt.Sprintf("<h%d@example.invalid>", i)
	}
	boardMail(t, s, sent, 3, "h", "", huge...) // a thread of its own
	got := map[string][]string{}
	capture := func(th *BoardThread) (BoardVerdict, error) {
		for _, m := range th.Members {
			got[m.RFCMessageID] = m.References
		}
		return testDecider(th)
	}
	drainAll(t, s, boardNow, capture)
	if fmt.Sprint(got["a"]) != "[]" || fmt.Sprint(got["r"]) != "[a]" {
		t.Fatalf("references: a %q, r %q", got["a"], got["r"])
	}
	if len(got["h"]) != boardReferencesMax || got["h"][0] != huge[0] || got["h"][boardReferencesMax-1] != huge[boardReferencesMax-1] {
		t.Fatalf("huge list: %d %q", len(got["h"]), got["h"][:min(2, len(got["h"]))])
	}
	// A valid JSON value of the wrong shape (the column only checks
	// json_valid) is no list.
	for _, bad := range []string{`{"a":1}`, `[1]`, `["x",[1]]`, `"x"`, `null`, `["x",null]`, `[]`} {
		if _, err := s.db.ExecContext(ctx, `UPDATE messages SET references_json = ? WHERE id = ?`, bad, r.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.MarkBoardThreadsDirty(ctx, "acc", []string{a.ThreadID}); err != nil {
			t.Fatal(err)
		}
		drainAll(t, s, boardNow, capture)
		if got["r"] != nil {
			t.Errorf("%s: %q", bad, got["r"])
		}
	}
}

func TestDecodeBoardReferences(t *testing.T) {
	long := `["` + strings.Repeat("x", boardReferencesChars) + `"]`
	for _, c := range []struct {
		raw  string
		cut  bool
		want string
	}{
		{`["a","b"]`, false, "[a b]"},
		{`[]`, false, "[]"},
		{``, false, "[]"},
		{`[`, false, "[]"},
		{`["a",`, false, "[]"},                // malformed, not cut: none
		{`["a","b`, true, "[a]"},              // cut inside the second: the first stays
		{`["a\"`, true, "[]"},                 // cut after an escaped quote
		{`["a",{`, true, "[]"},                // an object in the list: malformed
		{`["a",1]`, false, "[]"},              // a number: malformed
		{`["\u0000<x>"]`, false, "[\x00<x>]"}, // decoded as is; the rules normalise
		{long[:boardReferencesChars], true, "[]"},
		{`  ["a"]  trailing`, false, "[a]"},
	} {
		if got := fmt.Sprint(decodeBoardReferences(c.raw, c.cut)); got != c.want {
			t.Errorf("%q cut %v: %s, want %s", c.raw, c.cut, got, c.want)
		}
	}
	// Many blank entries: never more than boardReferencesMax kept.
	many := "[" + strings.Repeat(`"",`, 10_000) + `""]`
	if got := decodeBoardReferences(many, false); len(got) != boardReferencesMax {
		t.Fatalf("blanks: %d", len(got))
	}
}

// BoardMemberOf looks one row up by its primary key, and answers as the
// members the rules read would.
func TestBoardMemberOfLookup(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	sent := seedFolder(t, s, "acc", "Sent", api.RoleSent)
	trash := seedFolder(t, s, "acc", "Trash", api.RoleTrash)

	a := boardMail(t, s, inbox, 1, "a", "")
	r := boardMail(t, s, sent, 2, "r", "a", "a")
	gone := boardMail(t, s, trash, 3, "g", "a", "a")
	hidden := boardMail(t, s, inbox, 4, "h", "a", "a")
	sameThread(t, s, a, r, gone, hidden)
	other := boardMail(t, s, inbox, 5, "o", "")
	if _, _, err := s.SetMessageHidden(ctx, hidden.ID, true); err != nil {
		t.Fatal(err)
	}
	drainAll(t, s, boardNow, testDecider)
	c := caseOf(t, s, "acc", a.ThreadID)
	for _, x := range []struct {
		id           string
		counts, mine bool
	}{{a.ID, true, false}, {r.ID, true, true}, {gone.ID, false, false}, {hidden.ID, false, false}, {other.ID, false, false}, {"", false, false}, {"m_nope", false, false}} {
		counts, mine, err := s.BoardMemberOf(ctx, c.ID, x.id)
		if err != nil || counts != x.counts || mine != x.mine {
			t.Errorf("%s: %v %v %v, want %v %v", x.id, counts, mine, err, x.counts, x.mine)
		}
	}
	if _, _, err := s.BoardMemberOf(ctx, "c_nope", a.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown case: %v", err)
	}
	if _, _, err := s.BoardMemberOf(ctx, "", a.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("no case id: %v", err)
	}
	// The message is found by its primary key, never by scanning the
	// thread or the table.
	rows, err := s.db.QueryContext(ctx, `EXPLAIN QUERY PLAN `+boardMemberOfQuery, "acc", a.ThreadID, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if len(plan) == 0 || !strings.Contains(plan[0], "SEARCH m USING INDEX") || !strings.Contains(plan[0], "(id=?)") {
		t.Fatalf("plan: %q", plan)
	}
	for _, p := range plan {
		if strings.HasPrefix(p, "SCAN") {
			t.Fatalf("plan scans: %q", plan)
		}
	}
}
