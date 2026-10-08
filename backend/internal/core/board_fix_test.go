// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// gmailQuote is the HTML of a reply as Gmail writes it: the user's words,
// then the attribution and the quoted original.
func gmailQuote(own, quoted string) string {
	return `<div dir="ltr">` + own + `</div><br><div class="gmail_quote"><div dir="ltr" class="gmail_attr">` +
		`On Mon, Sep 29, 2026 at 10:00 AM Bob &lt;bob@example.invalid&gt; wrote:<br></div>` +
		`<blockquote class="gmail_quote" style="margin:0px 0px 0px 0.8ex;border-left:1px solid rgb(204,204,204);padding-left:1ex">` +
		`<div dir="ltr">` + quoted + `</div></blockquote></div>`
}

// The own text of the user's messages with HTML comes from the HTML with
// its quoted history cut off: a question only in the quote makes no
// "them.asked", a commitment quoted from the other party's words in it is
// refused although the plain-text part (innerText, no quote marks, no
// attribution) holds them; and a plain-text innerText reply's quote is
// not the user's own text either.
func TestBoardOwnTextFromHTML(t *testing.T) {
	x := newBoardBox(t)
	// Only the quote asks: no case. The text part alone would say
	// them.asked.
	party := x.put(bmail{folder: x.sent, thread: "t_party", rfc: "p2", inReplyTo: "p1", from: boardMe, to: []api.Address{boardBob}, subject: "Re: Party",
		text: "Noted, see you there.\n\nAre you coming tomorrow?", html: gmailQuote("Noted, see you there.", "Are you coming tomorrow?")})
	// The user's own words ask: them.asked.
	x.put(bmail{folder: x.sent, thread: "t_venue", rfc: "v2", inReplyTo: "v1", from: boardMe, to: []api.Address{boardBob}, subject: "Re: Venue",
		text: "Can you confirm the venue?\n\nSure.", html: gmailQuote("Can you confirm the venue?", "Sure.")})
	// An older message of the user's before Bob's question, then a reply
	// with HTML to it, and a plain-text innerText one.
	older := x.put(bmail{folder: x.sent, thread: "t_deck", rfc: "d0", from: boardMe, to: []api.Address{boardBob}, subject: "Deck",
		at: -time.Hour, text: "The deck is coming.", html: "<p>The deck is coming.</p>"})
	x.put(bmail{folder: x.inbox, thread: "t_deck", rfc: "d1", from: boardBob, to: []api.Address{boardMe}, subject: "Deck",
		text: "Can you send the deck by Friday?"})
	html := x.put(bmail{folder: x.sent, thread: "t_deck", rfc: "d2", inReplyTo: "d1", from: boardMe, to: []api.Address{boardBob}, subject: "Re: Deck",
		at: time.Hour, text: "I will send it on Thursday.\n\nCan you send the deck by Friday?",
		html: gmailQuote("I will send it on Thursday.", "Can you send the deck by Friday?")})
	plain := x.put(bmail{folder: x.sent, thread: "t_deck", rfc: "d3", inReplyTo: "d1", from: boardMe, to: []api.Address{boardBob}, subject: "Re: Deck",
		at: 2 * time.Hour, text: "I will look at the numbers too.\n\nOn Mon, 29 Sep 2026 at 10:00, Bob wrote:\nCan you send the deck by Friday?"})
	x.drain()

	x.noCase("t_party")
	if c := x.caseOf("t_venue"); c.RuleReason != api.BoardReasonThemAsked {
		t.Fatalf("venue: %s", c.RuleReason)
	}
	deck := x.caseOf("t_deck")
	if deck.RuleReason != api.BoardReasonThemReplied {
		t.Fatalf("deck: %s", deck.RuleReason)
	}
	// Own texts are derived only for what the rules read: the user's
	// messages after the newest inbound one (rules 6: their forward
	// shape), not an older one before it.
	k := func(id string) boardOwnKey { return boardOwnKey{account: x.acc, id: id, state: store.BodyFetched} }
	if own, ok := x.b.board.own.get(k(party)); !ok || !own.html || own.text != "Noted, see you there." {
		t.Fatalf("own text of the HTML message: %+v %v", own, ok)
	}
	if own, ok := x.b.board.own.get(k(plain)); !ok || own.html {
		t.Fatalf("own text of the plain message: %+v %v", own, ok)
	}
	if own, ok := x.b.board.own.get(k(html)); !ok || !own.html {
		t.Fatalf("own text of the HTML reply after the inbound message: %+v %v", own, ok)
	}
	if _, ok := x.b.board.own.get(k(older)); ok {
		t.Fatal("the own text of a message before the inbound one was derived")
	}

	x.assistantOn()
	cm := func(msg, quote string) error {
		_, err := x.svc.Commit(x.ctx, api.BoardCommitParams{CaseID: deck.ID, InputKey: x.inputKey(deck.ID), Source: "claude",
			MessageID: api.MessageID(msg), Text: "Send the deck", Quote: quote})
		return err
	}
	wantCode(t, "the other party's words quoted in HTML", cm(html, "send the deck by Friday"), api.CodeQuoteNotFound)
	wantCode(t, "the other party's words quoted in innerText", cm(plain, "send the deck by Friday"), api.CodeQuoteNotFound)
	if err := cm(html, "I will send it on Thursday"); err != nil {
		t.Fatalf("own words of the HTML reply: %v", err)
	}
	if err := cm(plain, "I will look at the numbers too"); err != nil {
		t.Fatalf("own words of the innerText reply: %v", err)
	}
}

// inputKey reads a case's current input key from the queue.
func (x *boardBox) inputKey(id api.BoardCaseID) string {
	x.t.Helper()
	q, err := x.svc.Queue(x.ctx, api.BoardQueueParams{CaseIDs: []api.BoardCaseID{id}})
	if err != nil || len(q.Items) == 0 {
		c, gerr := x.b.store.GetBoardCase(x.ctx, string(id))
		if gerr != nil {
			x.t.Fatalf("input key of %s: %v %v", id, err, gerr)
		}
		return c.InputKey
	}
	return q.Items[0].InputKey
}

// A remind that came due keeps its case listed (and unpruned) although
// the case is older than its window, until the user marks it done.
func TestBoardFiredRemindOutsideWindow(t *testing.T) {
	x := newBoardBox(t)
	p := api.DefaultBoardPreferences()
	p.Windows = api.BoardWindows{Hot: 14, You: 14, Them: 14, Info: 14}
	if _, err := x.svc.SetPreferences(x.ctx, api.BoardSetPreferencesParams{Preferences: p}); err != nil {
		t.Fatal(err)
	}
	// Carol is unknown and writes to Alice, the user in Cc: info, dated ten days ago.
	x.put(bmail{folder: x.inbox, thread: "t_i", rfc: "i1", from: boardCarol, to: []api.Address{boardAlice}, cc: []api.Address{boardMe},
		at: -8 * 24 * time.Hour, text: "FYI"})
	x.drain()
	c := x.caseOf("t_i")
	if c.RuleReason != api.BoardReasonInfoUnknownSender {
		t.Fatalf("case: %s", c.RuleReason)
	}
	now := time.Now().UTC()
	until := now.Add(7 * 24 * time.Hour)
	if _, err := x.svc.Remind(x.ctx, api.BoardRemindParams{CaseID: c.ID, Until: &until}); err != nil {
		t.Fatal(err)
	}
	// Eight days on: the case is 18 days old, its window 14.
	x.b.board.now = func() time.Time { return now.Add(8 * 24 * time.Hour) }
	t.Cleanup(func() { x.b.board.now = time.Now })
	x.b.clearDueReminds(x.ctx)
	if got := x.caseOf("t_i"); got.Visibility != api.BoardLive || got.RemindAt != nil {
		t.Fatalf("after the remind: %+v", got)
	}
	// Evaluated again and pruned: still there.
	if err := x.b.store.MarkBoardThreadsDirty(x.ctx, x.acc, []string{"t_i"}); err != nil {
		t.Fatal(err)
	}
	x.drain()
	x.b.boardUpkeep(x.ctx)
	if got := x.caseOf("t_i"); got.Visibility != api.BoardLive || got.ID != c.ID {
		t.Fatalf("after evaluation and prune: %+v", got)
	}
	// Marked done: listed as done, the remind gone.
	if d, err := x.svc.SetDone(x.ctx, api.BoardSetDoneParams{CaseID: c.ID, Done: true}); err != nil || d.Case.Visibility != api.BoardDone {
		t.Fatalf("done: %+v %v", d, err)
	}
}

// A thread that loses its only member (another client moved it: the row
// goes, a copy comes back elsewhere in a thread of its own) keeps its case
// with the user's decisions; the copy does not reopen it.
func TestBoardOrphanedCaseMovedByAnotherClient(t *testing.T) {
	x := newBoardBox(t)
	id := x.put(bmail{folder: x.inbox, thread: "t_m", rfc: "mv1@x", from: boardAlice, to: []api.Address{boardMe}, text: "Hi"})
	x.drain()
	c := x.caseOf("t_m")
	st := api.BoardThem
	if _, err := x.svc.SetState(x.ctx, api.BoardSetStateParams{CaseID: c.ID, State: &st}); err != nil {
		t.Fatal(err)
	}
	tick()
	if _, err := x.svc.SetDone(x.ctx, api.BoardSetDoneParams{CaseID: c.ID, Done: true}); err != nil {
		t.Fatal(err)
	}
	tick()
	// The inbox copy is expunged before the archive copy is fetched.
	if _, err := x.b.store.DeleteMessages(x.ctx, x.acc, []string{id}); err != nil {
		t.Fatal(err)
	}
	x.drain()
	x.noCase("t_m")
	if kept, err := x.b.store.GetBoardCase(x.ctx, string(c.ID)); err != nil || kept.OrphanedAt.IsZero() {
		t.Fatalf("orphaned case: %+v %v", kept, err)
	}
	x.put(bmail{folder: x.archive, thread: "t_m2", rfc: "mv1@x", from: boardAlice, to: []api.Address{boardMe}, text: "Hi"})
	x.drain()
	_, cases := x.list()
	var got api.BoardCase
	for _, k := range cases {
		if k.ID == c.ID {
			got = k
		}
	}
	if got.ID != c.ID || got.UserState == nil || *got.UserState != api.BoardThem || got.Visibility != api.BoardDone {
		t.Fatalf("after the move: %+v (cases %d)", got, len(cases))
	}
}

// A thread the store cannot evaluate (a row that does not decode) is
// reported and dropped from the dirty set; the others are evaluated.
func TestBoardPoisonThread(t *testing.T) {
	x := newBoardBox(t)
	bad := x.put(bmail{folder: x.inbox, thread: "t_bad", rfc: "b1", from: boardAlice, to: []api.Address{boardMe}, text: "Hi"})
	x.put(bmail{folder: x.inbox, thread: "t_good", rfc: "g1", from: boardAlice, to: []api.Address{boardMe}, text: "Hi"})
	if _, err := x.b.store.DB().ExecContext(x.ctx, `UPDATE messages SET attachments_json = '{"not":"a list"}' WHERE id = ?`, bad); err != nil {
		t.Fatal(err)
	}
	if err := x.b.store.MarkBoardThreadsDirty(x.ctx, x.acc, []string{"t_bad", "t_good"}); err != nil {
		t.Fatal(err)
	}
	x.drain() // no error
	x.caseOf("t_good")
	x.noCase("t_bad")
	if n, err := x.b.store.CountBoardDirty(x.ctx); err != nil || n != 0 {
		t.Fatalf("dirty after the drain: %d %v", n, err)
	}
}

// A preference change that widens a window while the first evaluation
// runs is not lost: the running pass starts over with the new window
// before it records anything, so mail only the new window reaches is
// evaluated too.
func TestBoardBackfillRestartRace(t *testing.T) {
	oldRows := boardBackfillRows
	boardBackfillRows = 1
	t.Cleanup(func() { boardBackfillRows = oldRows; boardBackfillBatched = nil })
	x := newBoardBox(t)
	x.put(bmail{folder: x.inbox, thread: "t_new", rfc: "n1", from: boardAlice, to: []api.Address{boardMe}, text: "New"})
	x.put(bmail{folder: x.inbox, thread: "t_new2", rfc: "n2", from: boardBob, to: []api.Address{boardMe}, text: "New"})
	// 200 days old: only a 365-day window reaches it.
	x.put(bmail{folder: x.inbox, thread: "t_old", rfc: "o1", from: boardAlice, to: []api.Address{boardMe}, at: -200 * 24 * time.Hour, text: "Old"})
	if _, err := x.b.store.DB().ExecContext(x.ctx, `DELETE FROM board_dirty`); err != nil {
		t.Fatal(err)
	}
	wide := api.DefaultBoardPreferences()
	wide.Windows = api.BoardWindows{Hot: 365, You: 365, Them: 365, Info: 365}
	once := false
	boardBackfillBatched = func() {
		if once {
			return
		}
		once = true
		// What board.setPreferences does meanwhile (no worker runs here,
		// so it only resets the record).
		if _, err := x.svc.SetPreferences(x.ctx, api.BoardSetPreferencesParams{Preferences: wide}); err != nil {
			t.Error(err)
		}
	}
	if err := x.b.backfillBoard(x.ctx); err != nil {
		t.Fatal(err)
	}
	if !once {
		t.Fatal("the hook did not run")
	}
	if v, _, _ := x.b.store.GetMeta(x.ctx, metaBoardRules); v != boardRulesDone() {
		t.Fatalf("meta = %q", v)
	}
	x.drain()
	x.caseOf("t_old")
}

// A comment a bot relayed is ordered by the site's time of the item, not
// by the date its text claims: someone else's relayed comment after the
// user's makes the issue the user's turn again.
func TestBoardJiraItemTime(t *testing.T) {
	x := newBoardBox(t)
	res, err := x.b.Accounts().Add(x.ctx, api.AccountAddParams{Config: jiraConfig(), Credentials: api.Credentials{Password: "tok"}})
	if err != nil {
		t.Fatal(err)
	}
	jacc := string(res.AccountID)
	space := seedFolders(t, x.b, jacc, []store.Folder{
		{Mailbox: "space:10000", Name: "IT", Path: "IT", Selectable: true, Subscribed: true},
	})["space:10000"]
	if err := x.b.store.PutIssue(x.ctx, store.Issue{AccountID: jacc, IssueID: "6", Key: "ITSD-6", SpaceID: "10000", Summary: "VPN",
		Status: "Open", StatusID: "1", StatusCategory: api.StatusCategoryTodo, AssigneeID: "jana", ReporterID: "boss"}); err != nil {
		t.Fatal(err)
	}
	put := func(remote, author string, date time.Time, kind api.IssueItemKind, via string, updated time.Time) {
		m := &store.Message{AccountID: jacc, FolderID: space.ID, RemoteID: remote, Subject: "ITSD-6: VPN",
			From: []api.Address{{Name: author, Address: author + "@users.jira.invalid"}}, Date: date, InternalDate: date,
			RFCMessageID: "<" + strings.ReplaceAll(remote, ":", ".") + ".issue.6@acme.malachi.invalid>", Size: 10, ThreadID: store.IssueThreadID("6")}
		if err := x.b.store.UpsertMessages(x.ctx, []*store.Message{m}); err != nil {
			t.Fatal(err)
		}
		if err := x.b.store.PutIssueItems(x.ctx, jacc, []store.IssueItem{{RemoteID: remote, IssueID: "6", Kind: kind, AuthorID: author,
			Via: via, Updated: updated}}); err != nil {
			t.Fatal(err)
		}
	}
	put("i:6", "boss", x.base, api.IssueItemDescription, "", x.base)
	put("c:61", "me", x.base.Add(time.Hour), api.IssueItemComment, "", x.base.Add(time.Hour))
	// Relayed two hours in; its text claims a date 100 days ago.
	put("c:62", "bot", x.base.Add(-100*24*time.Hour), api.IssueItemComment, "Issue Sync", x.base.Add(2*time.Hour))
	if err := x.b.store.SetMeta(x.ctx, store.MetaIssueMePrefix+jacc, `{"id":"me"}`); err != nil {
		t.Fatal(err)
	}
	x.drain()
	if c := x.caseOf("jira:6"); c.RuleState != api.BoardYou || c.RuleReason != api.BoardReasonJiraCommented {
		t.Fatalf("jira:6: %s %s", c.RuleState, c.RuleReason)
	}
}

// A deadline's range is measured from when its message arrived, not from
// its Date header: a forged future Date does not admit a deadline years
// ahead.
func TestBoardDueFromArrival(t *testing.T) {
	x := newBoardBox(t)
	future := time.Now().UTC().AddDate(2, 0, 0)
	in := x.put(bmail{folder: x.inbox, thread: "t_f", rfc: "f1", from: boardAlice, to: []api.Address{boardMe}, date: future,
		text: "Please pay by the end of the term."})
	x.drain()
	x.assistantOn()
	c := x.caseOf("t_f")
	due := future.Add(30 * 24 * time.Hour)
	_, err := x.svc.Annotate(x.ctx, api.BoardAnnotateParams{CaseID: c.ID, InputKey: x.inputKey(c.ID), Source: "claude",
		Due: &api.BoardDue{At: due, Quote: "pay by the end of the term", MessageID: api.MessageID(in)}})
	wantCode(t, "a deadline beyond the arrival's range", err, api.CodeInvalidArgument)
	soon := time.Now().UTC().Add(7 * 24 * time.Hour)
	if _, err := x.svc.Annotate(x.ctx, api.BoardAnnotateParams{CaseID: c.ID, InputKey: x.inputKey(c.ID), Source: "claude",
		Due: &api.BoardDue{At: soon, Quote: "pay by the end of the term", MessageID: api.MessageID(in)}}); err != nil {
		t.Fatalf("a deadline within range: %v", err)
	}
}

// An archive folder that is not selectable is no archive folder: the
// case cannot archive, and board.archive only marks it done.
func TestBoardArchiveUnselectable(t *testing.T) {
	x := newBoardBox(t)
	if _, err := x.b.store.DB().ExecContext(x.ctx, `UPDATE folders SET selectable = 0 WHERE id = ?`, x.archive.ID); err != nil {
		t.Fatal(err)
	}
	a1 := x.put(bmail{folder: x.inbox, thread: "t_a", rfc: "a1", from: boardAlice, to: []api.Address{boardMe}, text: "Hi"})
	x.drain()
	c := x.caseOf("t_a")
	if c.CanArchive {
		t.Fatalf("canArchive: %+v", c)
	}
	res, err := x.svc.Archive(x.ctx, api.BoardArchiveParams{CaseID: c.ID})
	if err != nil || !res.NoArchive || res.Archived != 0 || res.Case.Visibility != api.BoardDone {
		t.Fatalf("archive: %+v %v", res, err)
	}
	if m, err := x.b.store.GetMessage(x.ctx, x.acc, a1); err != nil || m.FolderID != x.inbox.ID {
		t.Fatalf("moved: %+v %v", m.FolderID, err)
	}
}

// Deleting the copy of a linked draft in the Drafts folder (message.delete)
// deletes the draft; the clients are told.
func TestBoardDraftCopyDeleted(t *testing.T) {
	for _, op := range []string{"delete", "trash", "move"} {
		t.Run(op, func(t *testing.T) {
			x := newBoardBox(t)
			in := x.put(bmail{folder: x.inbox, thread: "t_a", rfc: "a1", from: boardAlice, to: []api.Address{boardMe}, text: "Hi"})
			x.drain()
			x.assistantOn()
			c := x.caseOf("t_a")
			d, err := x.b.Drafts().Save(x.ctx, api.DraftSaveParams{Draft: api.Draft{AccountID: api.AccountID(x.acc), To: []api.Address{boardAlice},
				Subject: "Re: Lunch", TextBody: "Yes.", InReplyTo: api.MessageID(in)}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := x.svc.Annotate(x.ctx, api.BoardAnnotateParams{CaseID: c.ID, InputKey: x.inputKey(c.ID), Source: "claude", DraftID: d.DraftID}); err != nil {
				t.Fatal(err)
			}
			// A linked draft is local and gets no copy any more (an upload
			// in flight drops its own); a draft linked before migration 0018
			// can still have one until the upkeep deletes it.
			copyID := x.put(bmail{folder: x.drafts, thread: "t_a", rfc: "dcopy@x", from: boardMe, to: []api.Address{boardAlice}, subject: "Re: Lunch", text: "Yes."})
			if _, err := x.b.store.DB().Exec(`UPDATE drafts SET rfc_message_id = 'dcopy@x', server_folder_id = ?, server_uid = ?,
				server_uidvalidity = (SELECT uidvalidity FROM folders WHERE id = ?), synced_version = version WHERE id = ?`,
				x.drafts.ID, x.uid, x.drafts.ID, string(d.DraftID)); err != nil {
				t.Fatal(err)
			}
			time.Sleep(2 * boardNotifyEvery)
			x.rec.take()
			ids := []api.MessageID{api.MessageID(copyID)}
			switch op {
			case "move":
				_, err = x.b.Messages().Move(x.ctx, api.MessageMoveParams{AccountID: api.AccountID(x.acc), MessageIDs: ids, TargetFolderID: api.FolderID(x.archive.ID)})
			default:
				_, err = x.b.Messages().Delete(x.ctx, api.MessageDeleteParams{AccountID: api.AccountID(x.acc), MessageIDs: ids, Permanent: op == "delete"})
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := x.b.store.GetDraft(x.ctx, x.acc, string(d.DraftID)); err == nil {
				t.Fatal("the draft survived its copy")
			}
			waitBoardEvent(t, x.rec, x.acc)
			if got := x.caseOf("t_a"); got.Draft != nil {
				t.Fatalf("draft still shown: %+v", got.Draft)
			}
		})
	}
}

// Every invalidArgument refusal of annotate and commit is counted where a
// run can be attributed: an empty case id under a valid source counts in
// the source's implicit run.
func TestBoardRefusalsCounted(t *testing.T) {
	x := newBoardBox(t)
	x.put(bmail{folder: x.inbox, thread: "t_a", rfc: "a1", from: boardAlice, to: []api.Address{boardMe}, text: "Hi"})
	x.drain()
	x.assistantOn()
	_, err := x.svc.Annotate(x.ctx, api.BoardAnnotateParams{Source: "probe", Title: "x"})
	wantCode(t, "empty case id", err, api.CodeInvalidArgument)
	if r := externalRun(t, x, "probe"); r.Rejected != 1 {
		t.Fatalf("external run: %+v", r)
	}
	// No source and no open run: nothing to count in.
	_, err = x.svc.Annotate(x.ctx, api.BoardAnnotateParams{CaseID: x.caseOf("t_a").ID, Title: "x"})
	wantCode(t, "no source", err, api.CodeInvalidArgument)
	var n int
	if err := x.b.store.DB().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM board_runs`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("runs: %d %v", n, err)
	}
}

// The known correspondents are read again after an hour; when someone
// became known, the existing cases are judged again.
func TestBoardKnownCorrespondentsRefresh(t *testing.T) {
	x := newBoardBox(t)
	x.put(bmail{folder: x.inbox, thread: "t_c", rfc: "c1", from: boardCarol, to: []api.Address{boardMe}, text: "Hello"})
	x.drain()
	if c := x.caseOf("t_c"); c.RuleReason != api.BoardReasonYouNewContact {
		t.Fatalf("before: %s", c.RuleReason)
	}
	// The user writes to Carol in another thread: known only after the
	// next read of the set.
	x.put(bmail{folder: x.sent, thread: "t_other", rfc: "o1", from: boardMe, to: []api.Address{boardCarol}, subject: "Other", text: "Hi Carol."})
	x.drain()
	if c := x.caseOf("t_c"); c.RuleReason != api.BoardReasonYouNewContact {
		t.Fatalf("within the hour: %s", c.RuleReason)
	}
	later := time.Now().Add(boardIdentityTTL + time.Minute)
	x.b.board.now = func() time.Time { return later }
	t.Cleanup(func() { x.b.board.now = time.Now })
	x.drain()
	if c := x.caseOf("t_c"); c.RuleReason != api.BoardReasonYouAddressed {
		t.Fatalf("after the refresh: %s", c.RuleReason)
	}
}

// A change of folder roles is noticed also when it happened while the
// daemon was not running: the roles last seen are kept in meta.
func TestBoardRolesNoticedAcrossRestart(t *testing.T) {
	x := newBoardBox(t)
	x.put(bmail{folder: x.inbox, thread: "t_a", rfc: "a1", from: boardAlice, to: []api.Address{boardMe}, text: "Hi"})
	x.drain()
	x.b.boardUpkeep(x.ctx) // records the roles
	if _, err := x.b.store.DB().ExecContext(x.ctx, `UPDATE folders SET role = 'none' WHERE id = ?`, x.archive.ID); err != nil {
		t.Fatal(err)
	}
	// Another daemon over the same store, as after a restart.
	b2 := New("restarted", x.b.store, config.Default(), nil)
	b2.boardUpkeep(x.ctx)
	if n, err := x.b.store.CountBoardDirty(x.ctx); err != nil || n == 0 {
		t.Fatalf("dirty after the restart: %d %v", n, err)
	}
	if err := b2.drainBoard(x.ctx); err != nil {
		t.Fatal(err)
	}
	if c := x.caseOf("t_a"); c.CanArchive {
		t.Fatalf("canArchive without an archive folder: %+v", c)
	}
}

// A message of the user's known as a reply by its References alone (no
// In-Reply-To) does not start its thread: a short reply above a quote is
// no forward, and its question makes "them.asked". The same text in a
// message that answers nothing reads as a forward.
func TestBoardReferencesAlone(t *testing.T) {
	x := newBoardBox(t)
	text := "Is it still on?\n\nOn Tue, 30 Sep 2026 at 09:12, Bob <bob@example.invalid> wrote:\n> Lunch on Friday.\n"
	x.put(bmail{folder: x.sent, thread: "t_refs", rfc: "r1", refs: []string{"<gone@example.invalid>"},
		from: boardMe, to: []api.Address{boardBob}, at: time.Hour, text: text})
	x.put(bmail{folder: x.sent, thread: "t_start", rfc: "s1",
		from: boardMe, to: []api.Address{boardBob}, at: time.Hour, text: text})
	x.drain()
	if c := x.caseOf("t_refs"); c.RuleState != api.BoardThem || c.RuleReason != api.BoardReasonThemAsked {
		t.Fatalf("references alone: %s %s", c.RuleState, c.RuleReason)
	}
	x.noCase("t_start")
}

// A message's date in board.get and board.queue is its arrival as the
// case's date has it (board.Arrival): a forged future Date header is
// capped at when the daemon stored the message, not only at now.
func TestBoardMessageDateIsArrival(t *testing.T) {
	x := newBoardBox(t)
	future := time.Now().UTC().AddDate(1, 0, 0)
	x.put(bmail{folder: x.inbox, thread: "t_d", rfc: "d1", from: boardAlice, to: []api.Address{boardMe}, date: future,
		text: "Can you check the figures?"})
	x.drain()
	x.assistantOn()
	for range 5 {
		tick() // now moves past the stored time
	}
	c := x.caseOf("t_d")
	if !c.Date.Before(time.Now()) {
		t.Fatalf("case date %v not capped at the stored time", c.Date)
	}
	got, err := x.svc.Get(x.ctx, api.BoardGetParams{CaseID: c.ID})
	if err != nil || len(got.Messages) != 1 {
		t.Fatalf("board.get: %+v %v", got, err)
	}
	if !got.Messages[0].Date.Equal(c.Date) {
		t.Fatalf("board.get message date %v, case date %v", got.Messages[0].Date, c.Date)
	}
	q, err := x.svc.Queue(x.ctx, api.BoardQueueParams{})
	if err != nil || len(q.Items) != 1 || len(q.Items[0].Messages) != 1 {
		t.Fatalf("board.queue: %+v %v", q, err)
	}
	if !q.Items[0].Messages[0].Date.Equal(c.Date) {
		t.Fatalf("board.queue message date %v, case date %v", q.Items[0].Messages[0].Date, c.Date)
	}
}
