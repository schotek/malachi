// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// boardNow is "now" for the board tests: a few days after threadBase.
var boardNow = threadBase.Add(72 * time.Hour)

var boardWindows = BoardWindowDays{Hot: 90, You: 90, Them: 30, Info: 14}

// boardMail stores a personal (classified) header-only message.
func boardMail(t *testing.T, s *Store, f Folder, uid uint32, rfc, inReplyTo string, refs ...string) *Message {
	t.Helper()
	return seedThread(t, s, f, Message{UID: uid, RFCMessageID: rfc, InReplyTo: inReplyTo, References: refs, Bulk: "none"})
}

// dirtyThreads returns the dirty set as "account/thread".
func dirtyThreads(t *testing.T, s *Store) map[string]bool {
	t.Helper()
	rows, err := s.db.Query(`SELECT account_id, thread_id FROM board_dirty`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var a, th string
		if err := rows.Scan(&a, &th); err != nil {
			t.Fatal(err)
		}
		out[a+"/"+th] = true
	}
	return out
}

func clearDirty(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.db.Exec(`DELETE FROM board_dirty`); err != nil {
		t.Fatal(err)
	}
}

// expectDirty checks that exactly the threads are dirty and clears the set.
func expectDirty(t *testing.T, s *Store, what string, want ...string) {
	t.Helper()
	got := dirtyThreads(t, s)
	w := map[string]bool{}
	for _, x := range want {
		w[x] = true
	}
	if fmt.Sprint(sortedKeys(got)) != fmt.Sprint(sortedKeys(w)) {
		t.Errorf("%s: dirty %v, want %v", what, sortedKeys(got), sortedKeys(w))
	}
	clearDirty(t, s)
}

// testDecider is a small stand-in for internal/board: the newest member
// that counts decides; mine → them, inbound → you (hot when a member is
// flagged); no member that counts → no case.
func testDecider(th *BoardThread) (BoardVerdict, error) {
	var newest, inbound *BoardMember
	n, flagged := 0, false
	for i := range th.Members {
		m := &th.Members[i]
		if !m.Counts {
			continue
		}
		n++
		newest = m
		if !m.Mine && !m.TwinOfMine {
			inbound = m
		}
		flagged = flagged || m.Flagged
	}
	if newest == nil {
		return BoardVerdict{}, nil
	}
	v := BoardVerdict{RulesVersion: "test", Subject: newest.Subject, Snippet: newest.Snippet, Date: newest.Date,
		MessageCount: n, LatestMessageID: newest.ID, ReplyMessageID: newest.ID, ReplyFolderID: newest.FolderID,
		Person: api.Address{Address: "alice@example.invalid"}}
	if inbound != nil {
		v.NewestInboundStored, v.NewestInboundDate = inbound.StoredAt, inbound.Date
	}
	switch {
	case newest.Mine:
		v.State, v.Reason = api.BoardThem, api.BoardReasonThemReplied
	case flagged:
		v.State, v.Reason = api.BoardHot, api.BoardReasonHotFlagged
	default:
		v.State, v.Reason = api.BoardYou, api.BoardReasonYouAddressed
	}
	return v, nil
}

func drainAll(t *testing.T, s *Store, now time.Time, decide BoardDecider) []string {
	t.Helper()
	changed := map[string]bool{}
	for i := 0; ; i++ {
		d, err := s.DrainBoard(context.Background(), BoardDrainOptions{Now: now}, decide)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range d.Failed {
			t.Fatalf("thread %s failed: %v", f.ThreadID, f.Err)
		}
		for _, a := range d.Accounts {
			changed[a] = true
		}
		if !d.More {
			break
		}
		if i > 10000 {
			t.Fatal("drain does not end")
		}
	}
	return sortedKeys(changed)
}

func caseOf(t *testing.T, s *Store, account, threadID string) BoardCase {
	t.Helper()
	c, err := s.BoardCaseByThread(context.Background(), account, threadID)
	if err != nil {
		t.Fatalf("case of %s: %v", threadID, err)
	}
	return c
}

// A store at 0016 with mail: 0017 creates the board's objects and scans
// nothing; afterwards every writer marks threads dirty.
func TestMigration0017Board(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')))`); err != nil {
		t.Fatal(err)
	}
	migs, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migs {
		if m.version > 16 {
			continue
		}
		if _, err := db.Exec(m.sql); err != nil {
			t.Fatalf("migration %d: %v", m.version, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations (version, name) VALUES (?, ?)`, m.version, m.name); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`INSERT INTO folders (id, account_id, mailbox, name, path, role) VALUES ('f1', 'acc', 'INBOX', 'Inbox', 'Inbox', 'inbox')`,
		`INSERT INTO messages (id, account_id, folder_id, uid, subject, thread_id, bulk) VALUES ('m1', 'acc', 'f1', 1, 'hello', 't_1', 'none')`,
		`INSERT INTO messages (id, account_id, folder_id, uid, subject, thread_id, bulk) VALUES ('m2', 'acc', 'f1', 2, 'again', 't_1', '')`,
		`INSERT INTO issues (account_id, issue_id, key, space_id, status) VALUES ('jira', '42', 'ITSD-42', '10001', 'Open')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(ctx, path, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, name := range []string{"board_cases", "board_annotations", "board_commitments", "board_runs", "board_dirty"} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + name).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s after the migration: %d rows, %v", name, n, err)
		}
	}
	for _, name := range []string{"messages_board_ai", "messages_board_au", "messages_board_ad", "issues_board_ai", "issues_board_au",
		"issues_board_ad", "issue_items_board_ai", "issue_items_board_au", "issue_items_board_ad", "meta_board_me_ai",
		"meta_board_me_au", "drafts_board_au", "drafts_board_ad"} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name = ?`, name).Scan(&n); err != nil || n != 1 {
			t.Errorf("trigger %s: %d %v", name, n, err)
		}
	}
	// The old rows are untouched and the triggers work on them.
	if _, err := s.db.Exec(`UPDATE messages SET bulk = 'none' WHERE id = 'm2'`); err != nil {
		t.Fatal(err)
	}
	expectDirty(t, s, "classified", "acc/t_1")
	if _, err := s.db.Exec(`UPDATE issues SET status = 'Done' WHERE issue_id = '42'`); err != nil {
		t.Fatal(err)
	}
	expectDirty(t, s, "issue status", "jira/jira:42")
	// The upgrade pass marks the window's threads, in batches.
	if _, err := s.db.Exec(`UPDATE messages SET date = ? WHERE id = 'm1'`, stamp(boardNow)); err != nil {
		t.Fatal(err)
	}
	clearDirty(t, s)
	last, visited, err := s.MarkBoardDirtyBatch(ctx, boardNow.Add(-time.Hour), "", 1)
	if err != nil || last != "m1" || visited != 1 {
		t.Fatalf("first batch: %q %d %v", last, visited, err)
	}
	if last, visited, err = s.MarkBoardDirtyBatch(ctx, boardNow.Add(-time.Hour), last, 1); err != nil || last != "m2" || visited != 1 {
		t.Fatalf("second batch: %q %d %v", last, visited, err)
	}
	if last, visited, err = s.MarkBoardDirtyBatch(ctx, boardNow.Add(-time.Hour), last, 1); err != nil || last != "" || visited != 0 {
		t.Fatalf("end: %q %d %v", last, visited, err)
	}
	expectDirty(t, s, "upgrade pass", "acc/t_1")
}

// Every writer a case depends on marks its thread dirty; a write that
// changes nothing the board reads does not.
func TestBoardTriggers(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	archive := seedFolder(t, s, "acc", "Archive", api.RoleArchive)

	a := boardMail(t, s, inbox, 1, "a", "")
	ta := "acc/" + a.ThreadID
	expectDirty(t, s, "insert", ta)

	if err := s.FlagMessages(ctx, "acc", []string{a.ID}, []api.Flag{api.FlagFlagged}, nil); err != nil {
		t.Fatal(err)
	}
	expectDirty(t, s, "flag", ta)
	if err := s.FlagMessages(ctx, "acc", []string{a.ID}, []api.Flag{api.FlagFlagged}, nil); err != nil {
		t.Fatal(err)
	}
	expectDirty(t, s, "flag again")
	z := boardMail(t, s, inbox, 5, "z", "")
	clearDirty(t, s)
	if _, err := s.ApplyServerFlags(ctx, inbox.ID, 5, []api.Flag{api.FlagSeen}, 9); err != nil {
		t.Fatal(err)
	}
	expectDirty(t, s, "server flags", "acc/"+z.ThreadID)
	if _, err := s.ApplyServerFlags(ctx, inbox.ID, 5, []api.Flag{api.FlagSeen}, 10); err != nil {
		t.Fatal(err)
	}
	expectDirty(t, s, "server flags unchanged")
	if _, err := s.db.Exec(`UPDATE messages SET modseq = 10, uid = uid, size = 99 WHERE id = ?`, a.ID); err != nil {
		t.Fatal(err)
	}
	expectDirty(t, s, "columns the board does not read")

	if err := s.SetMessageBody(ctx, a.ID, BodyUpdate{Text: "hi", Headers: map[string]string{"List-Id": "<l.example>", "List-Post": "<mailto:l@example>"}}); err != nil {
		t.Fatal(err)
	}
	expectDirty(t, s, "body and bulk classification", ta)

	if _, err := s.MoveMessages(ctx, "acc", []string{a.ID}, archive.ID); err != nil {
		t.Fatal(err)
	}
	expectDirty(t, s, "move", ta)

	if _, _, err := s.SetMessageHidden(ctx, a.ID, true); err != nil {
		t.Fatal(err)
	}
	expectDirty(t, s, "hidden", ta)

	// A merge marks both threads.
	b := boardMail(t, s, inbox, 2, "b", "x")
	tb := "acc/" + b.ThreadID
	clearDirty(t, s)
	x := boardMail(t, s, inbox, 3, "x", "a", "a")
	tid := sameThread(t, s, a, b, x)
	got := dirtyThreads(t, s)
	if !got["acc/"+tid] || !(got[ta] || got[tb]) || len(got) < 2 {
		t.Errorf("merge: dirty %v (a %s, b %s, merged %s)", sortedKeys(got), ta, tb, tid)
	}
	clearDirty(t, s)

	if _, err := s.DeleteMessages(ctx, "acc", []string{b.ID}); err != nil {
		t.Fatal(err)
	}
	expectDirty(t, s, "delete", "acc/"+tid)

	// Issues: stored, status, assignee and reporter changes, items, the
	// user's record; a refresh that changes nothing does not mark.
	is := Issue{AccountID: "jira", IssueID: "7", Key: "ITSD-7", SpaceID: "10001", Status: "Open", StatusCategory: "new"}
	if err := s.PutIssue(ctx, is); err != nil {
		t.Fatal(err)
	}
	expectDirty(t, s, "issue stored", "jira/jira:7")
	if err := s.PutIssue(ctx, is); err != nil {
		t.Fatal(err)
	}
	expectDirty(t, s, "issue unchanged")
	for _, change := range []func(*Issue){
		func(i *Issue) { i.Status, i.StatusCategory = "Done", "done" },
		func(i *Issue) { i.AssigneeID = "me" },
		func(i *Issue) { i.ReporterID = "me" },
		func(i *Issue) { i.Watching = true },
	} {
		change(&is)
		if err := s.PutIssue(ctx, is); err != nil {
			t.Fatal(err)
		}
		expectDirty(t, s, "issue change", "jira/jira:7")
	}
	if err := s.PutIssueItems(ctx, "jira", []IssueItem{{RemoteID: "c:1", IssueID: "7", Kind: api.IssueItemComment, AuthorID: "u1"}}); err != nil {
		t.Fatal(err)
	}
	expectDirty(t, s, "item", "jira/jira:7")
	if err := s.SetMeta(ctx, MetaIssueMePrefix+"jira", `{"id":"me"}`); err != nil {
		t.Fatal(err)
	}
	expectDirty(t, s, "jira user", "jira/jira:7")
	if err := s.SetMeta(ctx, MetaIssueMePrefix+"jira", `{"id":"me"}`); err != nil {
		t.Fatal(err)
	}
	expectDirty(t, s, "jira user unchanged")
	if err := s.SetMeta(ctx, "issues.mail.settled.jira", `x`); err != nil {
		t.Fatal(err)
	}
	expectDirty(t, s, "other meta")
	if _, err := s.DeleteIssues(ctx, "jira", []string{"7"}); err != nil {
		t.Fatal(err)
	}
	expectDirty(t, s, "issue deleted", "jira/jira:7")
}

func TestBoardDrainLifecycle(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	sent := seedFolder(t, s, "acc", "Sent", api.RoleSent)
	trash := seedFolder(t, s, "acc", "Trash", api.RoleTrash)

	a := boardMail(t, s, inbox, 1, "a", "")
	if got := drainAll(t, s, boardNow, testDecider); fmt.Sprint(got) != "[acc]" {
		t.Fatalf("changed accounts: %v", got)
	}
	c := caseOf(t, s, "acc", a.ThreadID)
	if !strings.HasPrefix(c.ID, "c_") || len(c.ID) != 34 || c.RuleState != api.BoardYou || c.RuleReason != api.BoardReasonYouAddressed ||
		c.MessageCount != 1 || c.LatestMessageID != a.ID || c.Version != 1 || len(c.InputKey) != 32 || c.Annotation != nil {
		t.Fatalf("new case: %+v", c)
	}
	// Evaluating again without a change changes nothing.
	if err := s.MarkBoardThreadsDirty(ctx, "acc", []string{a.ThreadID}); err != nil {
		t.Fatal(err)
	}
	if got := drainAll(t, s, boardNow, testDecider); len(got) != 0 {
		t.Fatalf("unchanged thread reported: %v", got)
	}
	if again := caseOf(t, s, "acc", a.ThreadID); again.Version != 1 {
		t.Fatalf("version without a change: %d", again.Version)
	}

	// The user replies: them; the key changes.
	r := boardMail(t, s, sent, 2, "r", "a", "a")
	drainAll(t, s, boardNow, testDecider)
	c2 := caseOf(t, s, "acc", a.ThreadID)
	if c2.ID != c.ID || c2.RuleState != api.BoardThem || c2.Version != 2 || c2.InputKey == c.InputKey || c2.MessageCount != 2 || c2.LatestMessageID != r.ID {
		t.Fatalf("after the reply: %+v", c2)
	}

	// Skip leaves the row.
	if err := s.MarkBoardThreadsDirty(ctx, "acc", []string{a.ThreadID}); err != nil {
		t.Fatal(err)
	}
	drainAll(t, s, boardNow, func(*BoardThread) (BoardVerdict, error) { return BoardVerdict{Skip: true, State: api.BoardInfo}, nil })
	if got := caseOf(t, s, "acc", a.ThreadID); got.RuleState != api.BoardThem || got.Version != 2 {
		t.Fatalf("skip changed the case: %+v", got)
	}
	if n, _ := s.CountBoardDirty(ctx); n != 0 {
		t.Fatalf("skip left the thread dirty: %d", n)
	}

	// Both in the trash: nothing counts, no user decision → deleted.
	if _, err := s.MoveMessages(ctx, "acc", []string{a.ID, r.ID}, trash.ID); err != nil {
		t.Fatal(err)
	}
	drainAll(t, s, boardNow, testDecider)
	if _, err := s.BoardCaseByThread(ctx, "acc", a.ThreadID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("case of a thread in the trash: %v", err)
	}

	// A user state keeps a case the rules drop ("kept", last rule state).
	b := boardMail(t, s, inbox, 3, "b", "")
	drainAll(t, s, boardNow, testDecider)
	cb := caseOf(t, s, "acc", b.ThreadID)
	if _, err := s.SetBoardUserState(ctx, cb.ID, api.BoardInfo, boardNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MoveMessages(ctx, "acc", []string{b.ID}, trash.ID); err != nil {
		t.Fatal(err)
	}
	drainAll(t, s, boardNow, testDecider)
	kept := caseOf(t, s, "acc", b.ThreadID)
	if kept.ID != cb.ID || kept.RuleReason != api.BoardReasonKept || kept.RuleState != api.BoardYou || kept.UserState != api.BoardInfo {
		t.Fatalf("kept case: %+v", kept)
	}
	// Every member gone: the case goes off the board but stays (with the
	// user's decisions) until the prune, in case the members come back.
	if _, err := s.DeleteMessages(ctx, "acc", []string{b.ID}); err != nil {
		t.Fatal(err)
	}
	if got := drainAll(t, s, boardNow, testDecider); fmt.Sprint(got) != "[acc]" {
		t.Fatalf("orphaning not reported: %v", got)
	}
	orphan, err := s.GetBoardCase(ctx, cb.ID)
	if err != nil || !orphan.OrphanedAt.Equal(boardNow) || orphan.UserState != api.BoardInfo {
		t.Fatalf("case without members: %+v %v", orphan, err)
	}

	// A decider error drops that thread (its writes undone) and the batch
	// goes on; an invalid state likewise.
	d := boardMail(t, s, inbox, 4, "d", "")
	poison := boardMail(t, s, inbox, 5, "poison", "")
	bad := boardMail(t, s, inbox, 6, "bad", "")
	got, err := s.DrainBoard(ctx, BoardDrainOptions{Now: boardNow}, func(th *BoardThread) (BoardVerdict, error) {
		switch th.ThreadID {
		case poison.ThreadID:
			return BoardVerdict{}, errors.New("boom")
		case bad.ThreadID:
			return BoardVerdict{State: "later"}, nil
		}
		return testDecider(th)
	})
	if err != nil || got.Threads != 3 || len(got.Failed) != 2 || got.More {
		t.Fatalf("drain with a poison thread: %+v %v", got, err)
	}
	for _, f := range got.Failed {
		if f.ThreadID != poison.ThreadID && f.ThreadID != bad.ThreadID || f.Err == nil {
			t.Errorf("failure: %+v", f)
		}
	}
	if n, _ := s.CountBoardDirty(ctx); n != 0 {
		t.Fatalf("dirty after the poison thread: %d", n)
	}
	if _, err := s.BoardCaseByThread(ctx, "acc", poison.ThreadID); !errors.Is(err, ErrNotFound) {
		t.Errorf("poison thread got a case: %v", err)
	}
	caseOf(t, s, "acc", d.ThreadID)
	// A cancelled context fails the call and writes nothing.
	if err := s.MarkBoardThreadsDirty(ctx, "acc", []string{d.ThreadID}); err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithCancel(ctx)
	if _, err := s.DrainBoard(cctx, BoardDrainOptions{Now: boardNow}, func(*BoardThread) (BoardVerdict, error) {
		cancel()
		return BoardVerdict{}, cctx.Err()
	}); err == nil {
		t.Fatal("cancelled drain succeeded")
	}
	if n, _ := s.CountBoardDirty(ctx); n != 1 {
		t.Fatalf("dirty after a cancelled drain: %d", n)
	}
	// The decider reads text inside the drain, and only of the thread.
	if err := s.SetMessageBody(ctx, d.ID, BodyUpdate{Text: "Can you send it?"}); err != nil {
		t.Fatal(err)
	}
	var text string
	var foreign error
	drainAll(t, s, boardNow, func(th *BoardThread) (BoardVerdict, error) {
		text, _ = th.Text(d.ID)
		_, foreign = th.Text(a.ID)
		return testDecider(th)
	})
	if text != "Can you send it?" || !errors.Is(foreign, ErrNotFound) {
		t.Errorf("text in the drain: %q, foreign %v", text, foreign)
	}
}

func TestBoardMembersCountAndTwins(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	sent := seedFolder(t, s, "acc", "Sent", api.RoleSent)
	junk := seedFolder(t, s, "acc", "Junk", api.RoleJunk)

	a := boardMail(t, s, inbox, 1, "a", "")
	news := seedThread(t, s, inbox, Message{UID: 2, RFCMessageID: "n", InReplyTo: "a", Bulk: "newsletter"})
	unclassified := seedThread(t, s, inbox, Message{UID: 3, RFCMessageID: "u", InReplyTo: "a"})
	spam := boardMail(t, s, junk, 4, "s", "a")
	mine := seedThread(t, s, sent, Message{UID: 5, RFCMessageID: "r", InReplyTo: "a"}) // unclassified, yet the user's own
	twin := boardMail(t, s, inbox, 6, "r", "a")                                        // a Bcc to oneself
	sameThread(t, s, a, news, unclassified, spam, mine, twin)

	var seen []BoardMember
	drainAll(t, s, boardNow, func(th *BoardThread) (BoardVerdict, error) {
		seen = th.Members
		return testDecider(th)
	})
	got := map[string]BoardMember{}
	for _, m := range seen {
		got[m.ID] = m
	}
	for _, x := range []struct {
		m             *Message
		counts, mineM bool
	}{{a, true, false}, {news, false, false}, {unclassified, false, false}, {spam, false, false}, {mine, true, true}, {twin, true, false}} {
		m := got[x.m.ID]
		if m.Counts != x.counts || m.Mine != x.mineM {
			t.Errorf("%s: counts %v mine %v", x.m.RFCMessageID, m.Counts, m.Mine)
		}
	}
	if !got[twin.ID].TwinOfMine || got[a.ID].TwinOfMine {
		t.Error("twin of the user's message not recognised")
	}
	// board.get's members: each Message-ID once (the user's row), oldest first.
	c := caseOf(t, s, "acc", a.ThreadID)
	msgs, err := s.BoardMessages(ctx, c.ID, 0)
	if err != nil || len(msgs) != 2 || msgs[0].ID != a.ID || msgs[1].ID != mine.ID || !msgs[1].Mine {
		t.Fatalf("members: %+v %v", msgs, err)
	}
	counts, isMine, err := s.BoardMemberOf(ctx, c.ID, mine.ID)
	if err != nil || !counts || !isMine {
		t.Errorf("member of: %v %v %v", counts, isMine, err)
	}
	if counts, _, _ := s.BoardMemberOf(ctx, c.ID, news.ID); counts {
		t.Error("a newsletter counts")
	}
}

func TestBoardDoneRemindAndReopen(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	a := boardMail(t, s, inbox, 1, "a", "")
	drainAll(t, s, boardNow, testDecider)
	c := caseOf(t, s, "acc", a.ThreadID)

	until := boardNow.Add(24 * time.Hour)
	r, err := s.SetBoardRemind(ctx, c.ID, until)
	if err != nil || !r.RemindAt.Equal(until) || r.Version != 2 || r.Visibility(boardNow) != api.BoardSnoozed {
		t.Fatalf("remind: %+v %v", r, err)
	}
	d, err := s.SetBoardDone(ctx, c.ID, true, boardNow)
	if err != nil || !d.RemindAt.IsZero() || !d.DoneAt.Equal(boardNow) || d.Version != 3 || d.Visibility(boardNow) != api.BoardDone {
		t.Fatalf("done clears remind: %+v %v", d, err)
	}
	if d2, _ := s.SetBoardDone(ctx, c.ID, true, boardNow.Add(time.Hour)); d2.Version != 3 || !d2.DoneAt.Equal(boardNow) {
		t.Errorf("done twice: %+v", d2)
	}
	r, err = s.SetBoardRemind(ctx, c.ID, until)
	if err != nil || !r.DoneAt.IsZero() || !r.RemindAt.Equal(until) {
		t.Fatalf("remind clears done: %+v %v", r, err)
	}
	if r, _ = s.SetBoardRemind(ctx, c.ID, time.Time{}); !r.RemindAt.IsZero() || r.Visibility(boardNow) != api.BoardLive {
		t.Fatalf("remind cleared: %+v", r)
	}
	if _, err := s.SetBoardRemind(ctx, "c_nope", until); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown case: %v", err)
	}
	if _, err := s.SetBoardUserState(ctx, c.ID, "later", boardNow); err == nil {
		t.Error("unknown user state accepted")
	}

	// Due reminds end and name their accounts.
	if _, err := s.SetBoardRemind(ctx, c.ID, until); err != nil {
		t.Fatal(err)
	}
	if next, ok, err := s.NextBoardRemind(ctx); err != nil || !ok || !next.Equal(until) {
		t.Fatalf("next remind: %v %v %v", next, ok, err)
	}
	if accts, err := s.ClearDueBoardReminds(ctx, boardNow); err != nil || len(accts) != 0 {
		t.Fatalf("early: %v %v", accts, err)
	}
	if accts, err := s.ClearDueBoardReminds(ctx, until); err != nil || fmt.Sprint(accts) != "[acc]" {
		t.Fatalf("due: %v %v", accts, err)
	}
	// The remind that came due stays as a marker: told once, live, and no
	// longer the next remind.
	fired := caseOf(t, s, "acc", a.ThreadID)
	if !fired.RemindAt.Equal(until) || !fired.Reminded || fired.Visibility(until) != api.BoardLive {
		t.Fatalf("fired remind: %+v", fired)
	}
	if accts, err := s.ClearDueBoardReminds(ctx, until.Add(time.Hour)); err != nil || len(accts) != 0 {
		t.Fatalf("due twice: %v %v", accts, err)
	}
	if _, ok, err := s.NextBoardRemind(ctx); err != nil || ok {
		t.Fatalf("a fired remind is the next one: %v %v", ok, err)
	}
	// A new remind is a remind again.
	if r, err := s.SetBoardRemind(ctx, c.ID, until.Add(time.Hour)); err != nil || r.Reminded {
		t.Fatalf("remind after a fired one: %+v %v", r, err)
	}
	if next, ok, _ := s.NextBoardRemind(ctx); !ok || !next.Equal(until.Add(time.Hour)) {
		t.Fatalf("next remind after a new one: %v %v", next, ok)
	}

	// Done; a reply stored before done does not reopen, a later inbound
	// one does, an old message backfilled later does not.
	doneAt := time.Now().UTC().Add(time.Second)
	if _, err := s.SetBoardDone(ctx, c.ID, true, doneAt); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkBoardThreadsDirty(ctx, "acc", []string{a.ThreadID}); err != nil {
		t.Fatal(err)
	}
	drainAll(t, s, boardNow, testDecider)
	if got := caseOf(t, s, "acc", a.ThreadID); got.DoneAt.IsZero() {
		t.Fatal("reopened without new mail")
	}
	// A backfilled old message stored after done: dated long before.
	old := seedThread(t, s, inbox, Message{UID: 2, RFCMessageID: "old", InReplyTo: "a", Bulk: "none", Date: doneAt.Add(-30 * 24 * time.Hour)})
	sameThread(t, s, a, old)
	drainAll(t, s, boardNow, func(th *BoardThread) (BoardVerdict, error) {
		v, err := testDecider(th)
		// time stamps of the store are millisecond; make the old one "stored after done"
		v.NewestInboundStored = doneAt.Add(time.Minute)
		return v, err
	})
	if got := caseOf(t, s, "acc", a.ThreadID); got.DoneAt.IsZero() {
		t.Fatal("an old backfilled message reopened the case")
	}
	drainAllNew := func(th *BoardThread) (BoardVerdict, error) {
		v, err := testDecider(th)
		v.NewestInboundStored, v.NewestInboundDate = doneAt.Add(time.Minute), doneAt.Add(time.Minute)
		return v, err
	}
	if err := s.MarkBoardThreadsDirty(ctx, "acc", []string{a.ThreadID}); err != nil {
		t.Fatal(err)
	}
	drainAll(t, s, boardNow, drainAllNew)
	if got := caseOf(t, s, "acc", a.ThreadID); !got.DoneAt.IsZero() || got.Visibility(boardNow) != api.BoardLive {
		t.Fatalf("new inbound mail did not reopen: %+v", got)
	}
}

// boardThreadWithMine stores an inbound message and the user's reply and
// returns them with the case.
func boardThreadWithMine(t *testing.T, s *Store, inbox, sent Folder, uid uint32, rfc string) (*Message, *Message, BoardCase) {
	t.Helper()
	in := boardMail(t, s, inbox, uid, rfc, "")
	mine := boardMail(t, s, sent, uid+1, rfc+"-re", rfc, rfc)
	sameThread(t, s, in, mine)
	drainAll(t, s, boardNow, testDecider)
	return in, mine, caseOf(t, s, "acc", in.ThreadID)
}

func TestBoardAnnotateAndCommit(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	sent := seedFolder(t, s, "acc", "Sent", api.RoleSent)
	in, mine, c := boardThreadWithMine(t, s, inbox, sent, 1, "a")

	ref := BoardRunRef{Source: "claude", Day: "2026-09-04"}
	ann := BoardAnnotationInput{CaseID: c.ID, InputKey: c.InputKey, State: api.BoardThem, Title: "Lunch", Summary: "a\nb",
		Tasks: []string{"call"}, Due: &BoardDue{At: boardNow.Add(48 * time.Hour), Quote: "by Friday noon", MessageID: in.ID},
		Source: "claude", Run: ref, Now: boardNow}
	got, runID, err := s.AnnotateBoardCase(ctx, ann)
	if err != nil {
		t.Fatal(err)
	}
	if got.Annotation == nil || got.Annotation.Stale || got.Annotation.Title != "Lunch" || got.Annotation.Due == nil ||
		got.Annotation.Due.Quote != "by Friday noon" || fmt.Sprint(got.Annotation.Tasks) != "[call]" || got.Version != c.Version+1 ||
		got.Annotation.RunID != runID {
		t.Fatalf("annotated: %+v %+v", got, got.Annotation)
	}
	// A stale key is refused and nothing changes.
	ann.InputKey = "0123456789abcdef0123456789abcdef"
	if _, _, err := s.AnnotateBoardCase(ctx, ann); !errors.Is(err, ErrBoardConflict) {
		t.Fatalf("stale key: %v", err)
	}
	if _, _, err := s.AnnotateBoardCase(ctx, BoardAnnotationInput{CaseID: "c_nope", InputKey: c.InputKey}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown case: %v", err)
	}

	// A commitment from the user's message; not from someone else's.
	k, _, err := s.AddBoardCommitment(ctx, BoardCommitmentInput{CaseID: c.ID, InputKey: c.InputKey, MessageID: mine.ID,
		Text: "send the slides", Quote: "I will send the slides", Source: "claude", Run: ref, Now: boardNow})
	if err != nil || k.State != api.CommitmentOpen || k.AccountID != "acc" || !k.MessageDate.Equal(mine.Date) {
		t.Fatalf("commit: %+v %v", k, err)
	}
	if _, _, err := s.AddBoardCommitment(ctx, BoardCommitmentInput{CaseID: c.ID, InputKey: c.InputKey, MessageID: in.ID,
		Text: "x", Quote: "y", Run: ref, Now: boardNow}); !errors.Is(err, ErrBoardNotMine) {
		t.Fatalf("someone else's message: %v", err)
	}
	if _, err := s.CountBoardRejected(ctx, ref, boardNow); err != nil {
		t.Fatal(err)
	}

	// A new member makes the annotation stale; a commit with the old key
	// is refused.
	later := boardMail(t, s, inbox, 9, "later", "a-re", "a", "a-re")
	sameThread(t, s, in, later)
	drainAll(t, s, boardNow, testDecider)
	stale := caseOf(t, s, "acc", in.ThreadID)
	if stale.Annotation == nil || !stale.Annotation.Stale || stale.InputKey == c.InputKey {
		t.Fatalf("annotation after a new member: %+v %+v", stale, stale.Annotation)
	}
	if _, _, err := s.AddBoardCommitment(ctx, BoardCommitmentInput{CaseID: c.ID, InputKey: c.InputKey, MessageID: mine.ID,
		Text: "x", Quote: "y", Run: ref, Now: boardNow}); !errors.Is(err, ErrBoardConflict) {
		t.Fatalf("commit with a stale key: %v", err)
	}
	// Annotate checks the key derived inside its transaction, not the
	// stored one: a member added and not yet evaluated is a conflict too.
	extra := boardMail(t, s, inbox, 10, "extra", "later", "a", "later")
	sameThread(t, s, in, extra)
	if _, _, err := s.AnnotateBoardCase(ctx, BoardAnnotationInput{CaseID: c.ID, InputKey: stale.InputKey, Run: ref, Now: boardNow}); !errors.Is(err, ErrBoardConflict) {
		t.Fatalf("annotate before the drain: %v", err)
	}
	drainAll(t, s, boardNow, testDecider)

	// The user writes again: the open commitment closes (replied).
	reply2 := boardMail(t, s, sent, 11, "a-re2", "extra", "a", "extra")
	sameThread(t, s, in, reply2)
	drainAll(t, s, boardNow, testDecider)
	if got, _ := s.GetBoardCommitment(ctx, k.ID); got.State != api.CommitmentClosed || got.ClosedReason != api.CommitmentClosedReplied {
		t.Fatalf("commitment after a newer reply: %+v", got)
	}
	if got, err := s.SetBoardCommitment(ctx, k.ID, false, boardNow); err != nil || got.State != api.CommitmentOpen || got.ClosedReason != "" {
		t.Fatalf("reopen: %+v %v", got, err)
	}
	if got, err := s.SetBoardCommitment(ctx, k.ID, true, boardNow); err != nil || got.State != api.CommitmentDone {
		t.Fatalf("tick off: %+v %v", got, err)
	}
	if _, err := s.SetBoardCommitment(ctx, "k_nope", true, boardNow); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown commitment: %v", err)
	}
	if _, err := s.SetBoardCommitment(ctx, k.ID, false, boardNow); err != nil {
		t.Fatal(err)
	}
	// Done closes open commitments (done).
	if _, err := s.SetBoardDone(ctx, c.ID, true, boardNow); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetBoardCommitment(ctx, k.ID); got.State != api.CommitmentClosed || got.ClosedReason != api.CommitmentClosedDone {
		t.Fatalf("commitment after done: %+v", got)
	}

	// The implicit external run counted the calls; one per source and day.
	run, err := s.GetBoardRun(ctx, runID)
	if err != nil || run.Trigger != api.TriggerExternal || run.Source != "claude" || run.Day != "2026-09-04" ||
		run.Annotated != 1 || run.Commitments != 1 || run.Rejected != 1 {
		t.Fatalf("external run: %+v %v", run, err)
	}
	other, err := s.CountBoardRejected(ctx, BoardRunRef{Source: "claude", Day: "2026-09-05"}, boardNow)
	if err != nil || other == runID {
		t.Fatalf("next day's run: %q %v", other, err)
	}
}

func TestBoardDraftLink(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	a := boardMail(t, s, inbox, 1, "a", "")
	drainAll(t, s, boardNow, testDecider)
	c := caseOf(t, s, "acc", a.ThreadID)
	if _, err := s.SetBoardDraft(ctx, "c_nope", "d_x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown case: %v", err)
	}
	d := &Draft{AccountID: "acc", Subject: "Re: a", TextBody: strings.Repeat("ž", 3000)}
	if err := s.SaveDraft(ctx, d, nil); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.AnnotateBoardCase(ctx, BoardAnnotationInput{CaseID: c.ID, InputKey: c.InputKey, DraftID: d.ID, Source: "x", Now: boardNow})
	if err != nil || got.Draft == nil || got.Draft.DraftID != d.ID || len(got.Draft.Text) > api.MaxBoardDraftTextBytes ||
		!strings.HasPrefix(d.TextBody, got.Draft.Text) || len(got.Draft.Text) < api.MaxBoardDraftTextBytes-1 {
		t.Fatalf("draft: %+v %v", got.Draft, err)
	}
	// Editing the draft changes the case's version; deleting it removes it.
	v := got.Version
	d.TextBody = "short"
	if err := s.SaveDraft(ctx, d, nil); err != nil {
		t.Fatal(err)
	}
	after, _ := s.GetBoardCase(ctx, c.ID)
	if after.Version != v+1 || after.Draft == nil || after.Draft.Text != "short" {
		t.Fatalf("after the edit: %+v %+v", after, after.Draft)
	}
	if err := s.DeleteDraft(ctx, "acc", d.ID); err != nil {
		t.Fatal(err)
	}
	after, _ = s.GetBoardCase(ctx, c.ID)
	if after.Version != v+2 || after.Draft != nil || after.DraftID != d.ID {
		t.Fatalf("after the deletion: %+v", after)
	}
	if got, err := s.SetBoardDraft(ctx, c.ID, ""); err != nil || got.DraftID != "" || got.Version != v+3 {
		t.Fatalf("unlink: %+v %v", got, err)
	}
	// A draft of another account is no link.
	o := &Draft{AccountID: "other", TextBody: "x"}
	if err := s.SaveDraft(ctx, o, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := s.SetBoardDraft(ctx, c.ID, o.ID); err != nil || got.Draft != nil {
		t.Fatalf("foreign draft: %+v %v", got.Draft, err)
	}
}

// The draft link is the case's (board.setDraft): it needs no annotation,
// a later annotation without a draft keeps it, one with another draft does
// not replace a link to a draft that exists, and a link to a draft that is
// gone may be replaced. A thread merge carries it over.
func TestBoardDraftLinkIsTheCases(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	a := boardMail(t, s, inbox, 1, "a", "")
	drainAll(t, s, boardNow, testDecider)
	c := caseOf(t, s, "acc", a.ThreadID)
	draft := func(text string) *Draft {
		d := &Draft{AccountID: "acc", Subject: "Re: a", TextBody: text}
		if err := s.SaveDraft(ctx, d, nil); err != nil {
			t.Fatal(err)
		}
		return d
	}
	d1, d2 := draft("one"), draft("two")

	got, err := s.SetBoardDraft(ctx, c.ID, d1.ID)
	if err != nil || got.Annotation != nil || got.DraftID != d1.ID || got.Draft == nil || got.Draft.Text != "one" || got.Version != c.Version+1 {
		t.Fatalf("link without an annotation: %+v %v", got, err)
	}
	if again, err := s.SetBoardDraft(ctx, c.ID, d1.ID); err != nil || again.Version != got.Version {
		t.Fatalf("the same link again: %+v %v", again, err)
	}
	if _, err := s.SetBoardDraft(ctx, c.ID, d2.ID); !errors.Is(err, ErrBoardDraftLinked) {
		t.Fatalf("another draft over a live link: %v", err)
	}

	// An annotation without a draft keeps the link; one with another draft
	// is stored and leaves the link as it is.
	got, _, err = s.AnnotateBoardCase(ctx, BoardAnnotationInput{CaseID: c.ID, InputKey: c.InputKey, Title: "T", Source: "x", Now: boardNow})
	if err != nil || got.DraftID != d1.ID || got.Draft == nil || got.Annotation == nil || got.Annotation.Title != "T" {
		t.Fatalf("annotation without a draft: %+v %v", got, err)
	}
	got, _, err = s.AnnotateBoardCase(ctx, BoardAnnotationInput{CaseID: c.ID, InputKey: c.InputKey, Title: "U", DraftID: d2.ID, Source: "x", Now: boardNow})
	if err != nil || got.DraftID != d1.ID || got.Draft.DraftID != d1.ID || got.Annotation.Title != "U" {
		t.Fatalf("annotation with another draft: %+v %v", got, err)
	}

	// The draft is gone (sent, deleted): no draft shows, and another may
	// be linked, by the user or by an annotation.
	if err := s.DeleteDraft(ctx, "acc", d1.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetBoardCase(ctx, c.ID); got.Draft != nil {
		t.Fatalf("draft after its deletion: %+v", got.Draft)
	}
	got, _, err = s.AnnotateBoardCase(ctx, BoardAnnotationInput{CaseID: c.ID, InputKey: c.InputKey, DraftID: d2.ID, Source: "x", Now: boardNow})
	if err != nil || got.DraftID != d2.ID || got.Draft == nil || got.Draft.Text != "two" {
		t.Fatalf("annotation over a dead link: %+v %v", got, err)
	}
	if err := s.DeleteDraft(ctx, "acc", d2.ID); err != nil {
		t.Fatal(err)
	}
	d3 := draft("three")
	if got, err := s.SetBoardDraft(ctx, c.ID, d3.ID); err != nil || got.Draft == nil || got.Draft.DraftID != d3.ID {
		t.Fatalf("link over a dead link: %+v %v", got, err)
	}
	if linked, err := s.BoardDraftLinked(ctx, "acc", d3.ID); err != nil || !linked {
		t.Fatalf("linked: %v %v", linked, err)
	}
	if linked, _ := s.BoardDraftLinked(ctx, "other", d3.ID); linked {
		t.Fatal("linked in another account")
	}

	// A merge: the absorbed case's link moves to the case that has none.
	x := boardMail(t, s, inbox, 10, "x", "")
	y := boardMail(t, s, inbox, 11, "y", "")
	drainAll(t, s, boardNow, testDecider)
	// Whichever of the two cases survives, the one link must reach it.
	cx := caseOf(t, s, "acc", x.ThreadID)
	caseOf(t, s, "acc", y.ThreadID)
	dx := draft("x")
	if _, err := s.SetBoardDraft(ctx, cx.ID, dx.ID); err != nil {
		t.Fatal(err)
	}
	both := boardMail(t, s, inbox, 12, "both", "x", "y", "x")
	merged := sameThread(t, s, x, y, both)
	survivor := caseOf(t, s, "acc", merged)
	if survivor.DraftID != dx.ID || survivor.Draft == nil || survivor.Draft.Text != "x" {
		t.Fatalf("link after the merge: %+v", survivor)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM board_cases WHERE draft_id = ?`, dx.ID); n != 1 {
		t.Fatalf("cases linking the draft: %d", n)
	}
}

// Merging two threads with cases keeps one case with both users'
// decisions; one case is renamed when only the absorbed thread has one.
func TestBoardMergeThreads(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	sent := seedFolder(t, s, "acc", "Sent", api.RoleSent)

	// One case: it moves with its thread.
	a := boardMail(t, s, inbox, 1, "a", "")
	drainAll(t, s, boardNow, testDecider)
	ca := caseOf(t, s, "acc", a.ThreadID)
	if _, err := s.SetBoardUserState(ctx, ca.ID, api.BoardInfo, boardNow); err != nil {
		t.Fatal(err)
	}
	// b names a's child c, which is not stored yet; b's thread has no case
	// (bulk).
	b := seedThread(t, s, inbox, Message{UID: 2, RFCMessageID: "b", InReplyTo: "c", Bulk: "list"})
	drainAll(t, s, boardNow, testDecider)
	if _, err := s.BoardCaseByThread(ctx, "acc", b.ThreadID); !errors.Is(err, ErrNotFound) {
		t.Fatal("bulk thread got a case")
	}
	cmsg := boardMail(t, s, inbox, 3, "c", "a", "a")
	tid := sameThread(t, s, a, b, cmsg)
	moved, err := s.GetBoardCase(ctx, ca.ID)
	if err != nil || moved.ThreadID != tid || moved.UserState != api.BoardInfo {
		t.Fatalf("moved case: %+v %v", moved, err)
	}
	drainAll(t, s, boardNow, testDecider)
	if n := countRows(t, s, `SELECT COUNT(*) FROM board_cases`); n != 1 {
		t.Fatalf("cases after the merge: %d", n)
	}

	// Two cases, each with decisions, an annotation and a commitment.
	x, xm, cx := boardThreadWithMine(t, s, inbox, sent, 10, "x")
	y, ym, cy := boardThreadWithMine(t, s, inbox, sent, 20, "y")
	ref := BoardRunRef{Source: "t", Day: "2026-09-04"}
	if _, err := s.SetBoardUserState(ctx, cx.ID, api.BoardHot, boardNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetBoardUserState(ctx, cy.ID, api.BoardYou, boardNow.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetBoardRemind(ctx, cx.ID, boardNow.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetBoardRemind(ctx, cy.ID, boardNow.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AnnotateBoardCase(ctx, BoardAnnotationInput{CaseID: cy.ID, InputKey: cy.InputKey, Title: "Y", DraftID: "d_y", Run: ref, Now: boardNow}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AnnotateBoardCase(ctx, BoardAnnotationInput{CaseID: cx.ID, InputKey: cx.InputKey, Title: "X", Run: ref, Now: boardNow}); err != nil {
		t.Fatal(err)
	}
	kx, _, err := s.AddBoardCommitment(ctx, BoardCommitmentInput{CaseID: cx.ID, InputKey: cx.InputKey, MessageID: xm.ID, Text: "x", Quote: "qx", Run: ref, Now: boardNow})
	if err != nil {
		t.Fatal(err)
	}
	ky, _, err := s.AddBoardCommitment(ctx, BoardCommitmentInput{CaseID: cy.ID, InputKey: cy.InputKey, MessageID: ym.ID, Text: "y", Quote: "qy", Run: ref, Now: boardNow})
	if err != nil {
		t.Fatal(err)
	}
	// A message naming both merges them.
	both := boardMail(t, s, inbox, 30, "both", "x-re", "y", "y-re", "x", "x-re")
	merged := sameThread(t, s, x, y, both)
	var survivor BoardCase
	switch {
	case func() bool { c, err := s.GetBoardCase(ctx, cx.ID); survivor = c; return err == nil }():
		if _, err := s.GetBoardCase(ctx, cy.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("both cases survived")
		}
	default:
		if survivor, err = s.GetBoardCase(ctx, cy.ID); err != nil {
			t.Fatalf("no case survived: %v", err)
		}
	}
	if survivor.ThreadID != merged || survivor.UserState != api.BoardYou || !survivor.RemindAt.Equal(boardNow.Add(24*time.Hour)) ||
		survivor.Annotation == nil || survivor.DraftID != "d_y" {
		t.Fatalf("merged case: %+v %+v", survivor, survivor.Annotation)
	}
	for _, k := range []BoardCommitment{kx, ky} {
		if got, err := s.GetBoardCommitment(ctx, k.ID); err != nil || got.CaseID != survivor.ID {
			t.Errorf("commitment %s after the merge: %+v %v", k.Text, got, err)
		}
	}
	drainAll(t, s, boardNow, testDecider)
	after, err := s.GetBoardCase(ctx, survivor.ID)
	if err != nil || after.MessageCount != 5 || !after.Annotation.Stale {
		t.Fatalf("merged case evaluated: %+v %v", after, err)
	}
}

func countRows(t *testing.T, s *Store, q string, args ...any) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestBoardListAndQueue(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	other := seedFolder(t, s, "other", "INBOX", api.RoleInbox)
	var cases []BoardCase
	for i := 0; i < 6; i++ {
		m := seedThread(t, s, inbox, Message{UID: uint32(i + 1), RFCMessageID: fmt.Sprintf("m%d", i), Bulk: "none",
			Date: boardNow.Add(-time.Duration(i) * 24 * time.Hour)})
		drainAll(t, s, boardNow, testDecider)
		cases = append(cases, caseOf(t, s, "acc", m.ThreadID))
	}
	// An old one (out of its window) and one of another account.
	oldMsg := seedThread(t, s, inbox, Message{UID: 50, RFCMessageID: "old", Bulk: "none", Date: boardNow.Add(-200 * 24 * time.Hour)})
	seedThread(t, s, other, Message{UID: 1, RFCMessageID: "o", Bulk: "none", Date: boardNow})
	drainAll(t, s, boardNow, testDecider)
	oldCase := caseOf(t, s, "acc", oldMsg.ThreadID)

	q := BoardListQuery{AccountIDs: []string{"acc"}, Now: boardNow, Windows: boardWindows}
	l, err := s.ListBoard(ctx, q)
	if err != nil || len(l.Cases) != 6 || l.Truncated || l.Cases[0].ID != cases[0].ID || l.Cases[5].ID != cases[5].ID {
		t.Fatalf("list: %d %v %v", len(l.Cases), l.Truncated, err)
	}
	q.Limit = 4
	if l, _ = s.ListBoard(ctx, q); len(l.Cases) != 4 || !l.Truncated {
		t.Fatalf("limited list: %d %v", len(l.Cases), l.Truncated)
	}
	q.Limit = 0
	// A user state keeps the old case; done and snoozed ones are listed.
	if _, err := s.SetBoardUserState(ctx, oldCase.ID, api.BoardThem, boardNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetBoardDone(ctx, cases[1].ID, true, boardNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetBoardRemind(ctx, cases[2].ID, boardNow.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if l, _ = s.ListBoard(ctx, q); len(l.Cases) != 7 {
		t.Fatalf("list with kept, done, snoozed: %d", len(l.Cases))
	}
	// A done case drops off after DoneDays.
	q.Now, q.DoneDays = boardNow.Add(2*24*time.Hour), 1
	if l, _ = s.ListBoard(ctx, q); len(l.Cases) != 6 {
		t.Fatalf("list after the done window: %d", len(l.Cases))
	}
	q.Now, q.DoneDays = boardNow, 0
	if l, _ = s.ListBoard(ctx, BoardListQuery{Now: boardNow, Windows: boardWindows}); len(l.Cases) != 0 {
		t.Fatal("no accounts listed cases")
	}

	// The queue: live, not annotated, newest first; caseIds restrict.
	qq := BoardQueueQuery{AccountIDs: []string{"acc"}, Now: boardNow, Windows: boardWindows, Limit: 2}
	items, remaining, err := s.BoardQueue(ctx, qq)
	if err != nil || len(items) != 2 || remaining != 3 || items[0].Case.ID != cases[0].ID || items[1].Case.ID != cases[3].ID ||
		len(items[0].Messages) != 1 {
		t.Fatalf("queue: %d %d %v", len(items), remaining, err)
	}
	if n, _ := s.CountBoardQueue(ctx, qq); n != 5 {
		t.Fatalf("queue size: %d", n)
	}
	if _, _, err := s.AnnotateBoardCase(ctx, BoardAnnotationInput{CaseID: cases[0].ID, InputKey: cases[0].InputKey, Source: "t", Now: boardNow}); err != nil {
		t.Fatal(err)
	}
	// A dirty thread waits.
	if err := s.MarkBoardThreadsDirty(ctx, "acc", []string{cases[3].ThreadID}); err != nil {
		t.Fatal(err)
	}
	items, remaining, _ = s.BoardQueue(ctx, qq)
	if len(items) != 2 || items[0].Case.ID != cases[4].ID || remaining != 1 {
		t.Fatalf("queue after an annotation: %+v %d", items, remaining)
	}
	qq.CaseIDs = []string{cases[5].ID, cases[1].ID}
	if items, remaining, _ = s.BoardQueue(ctx, qq); len(items) != 1 || items[0].Case.ID != cases[5].ID || remaining != 0 {
		t.Fatalf("queue of chosen cases: %+v %d", items, remaining)
	}

	// The assistant's state counts for the window only with the assistant
	// on: an annotation saying info keeps a 20-day-old case off the board
	// (info window 14 days)…
	m := seedThread(t, s, inbox, Message{UID: 60, RFCMessageID: "twenty", Bulk: "none", Date: boardNow.Add(-20 * 24 * time.Hour)})
	drainAll(t, s, boardNow, testDecider)
	c20 := caseOf(t, s, "acc", m.ThreadID)
	if _, _, err := s.AnnotateBoardCase(ctx, BoardAnnotationInput{CaseID: c20.ID, InputKey: c20.InputKey, State: api.BoardInfo, Source: "t", Now: boardNow}); err != nil {
		t.Fatal(err)
	}
	listed := func(assistant bool) bool {
		l, err := s.ListBoard(ctx, BoardListQuery{AccountIDs: []string{"acc"}, Now: boardNow, Windows: boardWindows, Assistant: assistant})
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range l.Cases {
			if c.ID == c20.ID {
				return true
			}
		}
		return false
	}
	if !listed(false) || listed(true) {
		t.Errorf("assistant's state and the window: off %v on %v", listed(false), listed(true))
	}
}

func TestBoardRuns(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	if _, err := s.StartBoardRun(ctx, api.TriggerExternal, "x", boardNow); err == nil {
		t.Fatal("external run started by a client")
	}
	day := boardNow.Truncate(24 * time.Hour)
	manual, err := s.StartBoardRun(ctx, api.TriggerManual, "claude", day.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	auto, err := s.StartBoardRun(ctx, api.TriggerAuto, "claude", day.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	yesterday, err := s.StartBoardRun(ctx, api.TriggerAuto, "claude", day.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range []struct {
		run, counter string
		n            int
	}{{auto, "annotated", 3}, {yesterday, "annotated", 5}, {manual, "annotated", 2}} {
		for i := 0; i < x.n; i++ {
			if _, err := countBoardRunTx(ctx, tx, BoardRunRef{RunID: x.run}, boardNow, x.counter); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	st, err := s.BoardRunStats(ctx, day)
	if err != nil || st.AnnotatedAuto != 3 || st.LastRun == nil || st.LastRun.ID != auto || !st.LastRun.EndedAt.IsZero() {
		t.Fatalf("stats: %+v %v", st, err)
	}
	// Ending: unknown class → failed; twice = no-op; unknown id.
	if err := s.EndBoardRun(ctx, auto, "exploded", nil, boardNow); err != nil {
		t.Fatal(err)
	}
	if err := s.EndBoardRun(ctx, auto, "", nil, boardNow.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.GetBoardRun(ctx, auto); r.Error != api.RunFailed || !r.EndedAt.Equal(boardNow) {
		t.Fatalf("ended run: %+v", r)
	}
	if err := s.EndBoardRun(ctx, "r_nope", "", nil, boardNow); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown run: %v", err)
	}
	// A call naming an ended run counts in the external run.
	tx, _ = s.db.Begin()
	id, err := countBoardRunTx(ctx, tx, BoardRunRef{RunID: auto, Source: "claude", Day: "2026-09-04"}, boardNow, "annotated")
	if err != nil || id == auto {
		t.Fatalf("ended run counted: %q %v", id, err)
	}
	_ = tx.Commit()
	if r, _ := s.GetBoardRun(ctx, auto); r.Annotated != 3 {
		t.Fatalf("ended run's count changed: %+v", r)
	}
	// Runs left open end with failed; old runs go.
	if n, err := s.CloseOpenBoardRuns(ctx, day.Add(90*time.Minute), boardNow); err != nil || n != 2 {
		t.Fatalf("closed %d %v", n, err)
	}
	if r, _ := s.GetBoardRun(ctx, manual); r.Error != api.RunFailed || r.EndedAt.IsZero() {
		t.Fatalf("closed run: %+v", r)
	}
	if n, err := s.PruneBoardRuns(ctx, day); err != nil || n != 1 {
		t.Fatalf("pruned %d %v", n, err)
	}
	// The newest run by start may be an external one.
	if _, err := s.CountBoardRejected(ctx, BoardRunRef{Source: "desktop", Day: "2026-09-05"}, boardNow.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.BoardRunStats(ctx, day); st.LastRun == nil || st.LastRun.Trigger != api.TriggerExternal || st.LastRun.Rejected != 1 {
		t.Fatalf("last run: %+v", st.LastRun)
	}
}

func TestBoardRunUsage(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	start := func(at time.Time) string {
		t.Helper()
		id, err := s.StartBoardRun(ctx, api.TriggerManual, "claude", at)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	end := func(id string, u *api.BoardUsage, at time.Time) {
		t.Helper()
		if err := s.EndBoardRun(ctx, id, "", u, at); err != nil {
			t.Fatal(err)
		}
	}
	if total, err := s.BoardRunUsage(ctx, boardNow.Add(-24*time.Hour)); err != nil || total != (api.BoardUsageTotal{}) {
		t.Fatalf("empty: %+v %v", total, err)
	}
	// In the window, with usage; a second end changes nothing.
	a := start(boardNow.Add(-2 * time.Hour))
	end(a, &api.BoardUsage{InputTokens: 100, OutputTokens: 20, CacheCreationInputTokens: 3, CacheReadInputTokens: 400}, boardNow.Add(-time.Hour))
	end(a, &api.BoardUsage{InputTokens: 999_999}, boardNow)
	if r, _ := s.GetBoardRun(ctx, a); r.Usage == nil || r.Usage.InputTokens != 100 || r.Usage.CacheReadInputTokens != 400 ||
		!r.EndedAt.Equal(boardNow.Add(-time.Hour)) {
		t.Fatalf("run a: %+v %+v", r, r.Usage)
	}
	// Clamped; started before the window but ended in it: counts.
	b := start(boardNow.Add(-30 * time.Hour))
	end(b, &api.BoardUsage{InputTokens: 5 * api.MaxBoardUsageTokens, OutputTokens: -7}, boardNow.Add(-23*time.Hour))
	if r, _ := s.GetBoardRun(ctx, b); r.Usage == nil || r.Usage.InputTokens != api.MaxBoardUsageTokens || r.Usage.OutputTokens != 0 {
		t.Fatalf("run b: %+v", r.Usage)
	}
	// Without usage: NULL, not counted.
	c := start(boardNow.Add(-time.Hour))
	end(c, nil, boardNow.Add(-30*time.Minute))
	if r, _ := s.GetBoardRun(ctx, c); r.Usage != nil {
		t.Fatalf("run c: %+v", r.Usage)
	}
	// Ended before the window: not counted.
	d := start(boardNow.Add(-50 * time.Hour))
	end(d, &api.BoardUsage{InputTokens: 7}, boardNow.Add(-25*time.Hour))
	// Closed by the daemon, and an external run: no usage.
	e := start(boardNow.Add(-3 * time.Hour))
	if _, err := s.CloseOpenBoardRuns(ctx, boardNow, boardNow); err != nil {
		t.Fatal(err)
	}
	ext, err := s.CountBoardRejected(ctx, BoardRunRef{Source: "desktop"}, boardNow)
	if err != nil {
		t.Fatal(err)
	}
	end(ext, &api.BoardUsage{InputTokens: 1}, boardNow)
	for _, id := range []string{e, ext} {
		if r, _ := s.GetBoardRun(ctx, id); r.Usage != nil {
			t.Fatalf("run %s has usage: %+v", id, r.Usage)
		}
	}
	total, err := s.BoardRunUsage(ctx, boardNow.Add(-24*time.Hour))
	want := api.BoardUsageTotal{BoardUsage: api.BoardUsage{InputTokens: 100 + api.MaxBoardUsageTokens, OutputTokens: 20,
		CacheCreationInputTokens: 3, CacheReadInputTokens: 400}, Runs: 2}
	if err != nil || total != want {
		t.Fatalf("total: %+v %v", total, err)
	}
}

func TestBoardPruneAndAccountDeletion(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	acc := seedAccounts(t, s, 2)
	inbox := seedFolder(t, s, acc[0], "INBOX", api.RoleInbox)
	sent := seedFolder(t, s, acc[0], "Sent", api.RoleSent)
	inbox2 := seedFolder(t, s, acc[1], "INBOX", api.RoleInbox)
	oldDate := boardNow.Add(-200 * 24 * time.Hour)
	plain := seedThread(t, s, inbox, Message{UID: 1, RFCMessageID: "p", Bulk: "none", Date: oldDate})
	held := seedThread(t, s, inbox, Message{UID: 2, RFCMessageID: "h", Bulk: "none", Date: oldDate})
	fresh := seedThread(t, s, inbox, Message{UID: 3, RFCMessageID: "f", Bulk: "none", Date: boardNow})
	mine := seedThread(t, s, sent, Message{UID: 4, RFCMessageID: "m", InReplyTo: "f", Date: boardNow})
	sameThread(t, s, fresh, mine)
	seedThread(t, s, inbox2, Message{UID: 1, RFCMessageID: "o", Bulk: "none", Date: boardNow})
	drainAll(t, s, boardNow, testDecider)
	ch := caseOf(t, s, acc[0], held.ThreadID)
	if _, err := s.SetBoardUserState(ctx, ch.ID, api.BoardYou, boardNow); err != nil {
		t.Fatal(err)
	}
	accts, err := s.PruneBoardCases(ctx, BoardPrune{Before: boardNow.Add(-120 * 24 * time.Hour), DoneBefore: boardNow.Add(-30 * 24 * time.Hour), Now: boardNow})
	if err != nil || fmt.Sprint(accts) != "["+acc[0]+"]" {
		t.Fatalf("prune: %v %v", accts, err)
	}
	if _, err := s.BoardCaseByThread(ctx, acc[0], plain.ThreadID); !errors.Is(err, ErrNotFound) {
		t.Error("old case kept")
	}
	if _, err := s.GetBoardCase(ctx, ch.ID); err != nil {
		t.Error("case with a user state pruned")
	}

	// Account deletion removes every board row of the account, the
	// dirty threads its cascade marked included.
	cf := caseOf(t, s, acc[0], fresh.ThreadID)
	if _, _, err := s.AnnotateBoardCase(ctx, BoardAnnotationInput{CaseID: cf.ID, InputKey: cf.InputKey, Source: "t", Now: boardNow}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AddBoardCommitment(ctx, BoardCommitmentInput{CaseID: cf.ID, InputKey: cf.InputKey, MessageID: mine.ID, Text: "t", Quote: "q", Now: boardNow}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAccount(ctx, acc[0], true); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"board_cases", "board_commitments", "board_dirty"} {
		if n := countRows(t, s, `SELECT COUNT(*) FROM `+table+` WHERE account_id = ?`, acc[0]); n != 0 {
			t.Errorf("%s rows of the deleted account: %d", table, n)
		}
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM board_annotations`); n != 0 {
		t.Errorf("annotations left: %d", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM board_cases WHERE account_id = ?`, acc[1]); n != 1 {
		t.Errorf("the other account's cases: %d", n)
	}
}

func TestBoardJiraThread(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	space, assigned, _ := jiraFolders(t, s, "jira")
	at := boardNow.Add(-time.Hour)
	desc := seedItem(t, s, space, "7", "i:7", "ITSD-7: Printer", at)
	copyRow := seedItem(t, s, assigned, "7", "i:7", "ITSD-7: Printer", at)
	ev := seedItem(t, s, space, "7", "h:1", "ITSD-7: Printer", at.Add(time.Minute))
	if err := s.PutIssue(ctx, Issue{AccountID: "jira", IssueID: "7", Key: "ITSD-7", SpaceID: "10001", Status: "Open", StatusCategory: "new"}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutIssueItems(ctx, "jira", []IssueItem{
		{RemoteID: "i:7", IssueID: "7", Kind: api.IssueItemDescription, AuthorID: "u1"},
		{RemoteID: "h:1", IssueID: "7", Kind: api.IssueItemEvent, AuthorID: "u1"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMeta(ctx, MetaIssueMePrefix+"jira", `{"id":"me","name":"Me"}`); err != nil {
		t.Fatal(err)
	}
	var th BoardThread
	drainAll(t, s, boardNow, func(x *BoardThread) (BoardVerdict, error) {
		th = *x
		return testDecider(x)
	})
	if th.Issue == nil || th.Issue.Key != "ITSD-7" || th.Me != "me" || len(th.Items) != 2 || len(th.Members) != 2 {
		t.Fatalf("issue thread: issue %+v me %q items %d members %d", th.Issue, th.Me, len(th.Items), len(th.Members))
	}
	for _, m := range th.Members {
		if m.ID == copyRow.ID {
			t.Error("a virtual folder's copy is a member")
		}
		if m.ID == ev.ID && (m.Counts || m.ItemKind != api.IssueItemEvent) {
			t.Errorf("event: %+v", m)
		}
		if m.ID == desc.ID && (!m.Counts || m.ItemAuthorID != "u1") {
			t.Errorf("description: %+v", m)
		}
	}
	c := caseOf(t, s, "jira", IssueThreadID("7"))
	if c.Issue == nil || c.Issue.Key != "ITSD-7" || c.Issue.Status != "Open" {
		t.Fatalf("issue of the case: %+v", c.Issue)
	}
	// A status change changes the case.
	if err := s.PutIssue(ctx, Issue{AccountID: "jira", IssueID: "7", Key: "ITSD-7", SpaceID: "10001", Status: "In Progress", StatusCategory: "indeterminate"}); err != nil {
		t.Fatal(err)
	}
	drainAll(t, s, boardNow, testDecider)
	if got := caseOf(t, s, "jira", IssueThreadID("7")); got.Issue.Status != "In Progress" || got.Version != c.Version+1 {
		t.Fatalf("after the status change: %+v %+v", got, got.Issue)
	}
}

func TestBoardOwnAddresses(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	sent := seedFolder(t, s, "acc", "Sent", api.RoleSent)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	for i, from := range []string{"Me@Example.invalid", "me@example.invalid", "alias@example.invalid"} {
		seedThread(t, s, sent, Message{UID: uint32(i + 1), RFCMessageID: fmt.Sprintf("s%d", i), From: []api.Address{{Address: from}}})
	}
	seedThread(t, s, inbox, Message{UID: 9, RFCMessageID: "spoof", From: []api.Address{{Address: "boss@example.invalid"}}})
	got, err := s.BoardOwnAddresses(ctx, "acc", 0)
	if err != nil || fmt.Sprint(got) != "[me@example.invalid alias@example.invalid]" {
		t.Fatalf("own addresses: %v %v", got, err)
	}
}

// A 500-member thread and a thousand dirty threads drain in sensible time.
func TestBoardDrainScale(t *testing.T) {
	if testing.Short() {
		t.Skip("scale")
	}
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	var batch []*Message
	for i := 0; i < 500; i++ {
		ref := ""
		if i > 0 {
			ref = "big0"
		}
		batch = append(batch, &Message{AccountID: "acc", FolderID: inbox.ID, UID: uint32(i + 1), RFCMessageID: fmt.Sprintf("big%d", i),
			InReplyTo: ref, Bulk: "none", Subject: "big", Date: threadBase.Add(time.Duration(i) * time.Minute),
			From: []api.Address{{Address: "a@example.invalid"}}})
	}
	for i := 0; i < 1000; i++ {
		batch = append(batch, &Message{AccountID: "acc", FolderID: inbox.ID, UID: uint32(1000 + i), RFCMessageID: fmt.Sprintf("solo%d", i),
			Bulk: "none", Subject: "solo", Date: threadBase.Add(time.Duration(i) * time.Minute),
			From: []api.Address{{Address: "b@example.invalid"}}})
	}
	if err := s.UpsertMessages(ctx, batch); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	drainAll(t, s, boardNow, testDecider)
	took := time.Since(start)
	if n := countRows(t, s, `SELECT COUNT(*) FROM board_cases`); n != 1001 {
		t.Fatalf("cases: %d", n)
	}
	big := caseOf(t, s, "acc", batch[0].ThreadID)
	if big.MessageCount != 500 {
		t.Fatalf("big thread: %d members", big.MessageCount)
	}
	t.Logf("drained 1001 threads (one of 500 members) in %v", took)
	if took > 30*time.Second {
		t.Errorf("drain took %v", took)
	}
	// One thread of 500 members, evaluated again.
	if err := s.MarkBoardThreadsDirty(ctx, "acc", []string{big.ThreadID}); err != nil {
		t.Fatal(err)
	}
	start = time.Now()
	drainAll(t, s, boardNow, testDecider)
	if one := time.Since(start); one > 2*time.Second {
		t.Errorf("500-member thread took %v", one)
	} else {
		t.Logf("500-member thread in %v", one)
	}
	start = time.Now()
	items, _, err := s.BoardQueue(ctx, BoardQueueQuery{AccountIDs: []string{"acc"}, Now: boardNow, Windows: boardWindows, Limit: 5})
	if err != nil || len(items) != 5 {
		t.Fatalf("queue: %d %v", len(items), err)
	}
	if _, err := s.ListBoard(ctx, BoardListQuery{AccountIDs: []string{"acc"}, Now: boardNow, Windows: boardWindows}); err != nil {
		t.Fatal(err)
	}
	t.Logf("queue and list in %v", time.Since(start))
}
