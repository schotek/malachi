// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// boardRecorder records notify.boardChanged (and is an api.Notifier that
// ignores the rest).
type boardRecorder struct {
	mu     sync.Mutex
	events []api.BoardChangedNotification
	at     []time.Time
}

func (*boardRecorder) NewMessage(api.NewMessageNotification)           {}
func (*boardRecorder) SyncState(api.SyncStateNotification)             {}
func (*boardRecorder) AuthRequired(api.AuthRequiredNotification)       {}
func (*boardRecorder) AccountsChanged(api.AccountsChangedNotification) {}
func (*boardRecorder) MessagesChanged(api.MessagesChangedNotification) {}
func (r *boardRecorder) BoardChanged(n api.BoardChangedNotification) {
	r.mu.Lock()
	r.events = append(r.events, n)
	r.at = append(r.at, time.Now())
	r.mu.Unlock()
}

func (r *boardRecorder) take() []api.BoardChangedNotification {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.events
	r.events, r.at = nil, nil
	return out
}

var _ api.BoardNotifier = (*boardRecorder)(nil)

// boardBox is a mail account with an inbox, Sent, Archive, Trash and
// Drafts, the board's service and a recorder.
type boardBox struct {
	t                                   *testing.T
	ctx                                 context.Context
	b                                   *Backend
	svc                                 api.BoardService
	rec                                 *boardRecorder
	acc                                 string
	inbox, sent, archive, trash, drafts store.Folder
	base                                time.Time
	uid                                 uint32
}

var (
	boardMe    = api.Address{Name: "Me", Address: "me@example.invalid"}
	boardAlice = api.Address{Name: "Alice", Address: "alice@example.invalid"}
	boardBob   = api.Address{Name: "Bob", Address: "bob@example.invalid"}
	boardCarol = api.Address{Name: "Carol", Address: "carol@example.invalid"} // the user never wrote to her
)

func newBoardBox(t *testing.T) *boardBox {
	t.Helper()
	b, _ := newSyncBackend(t)
	x := &boardBox{t: t, ctx: context.Background(), b: b, svc: b.Board(), rec: &boardRecorder{},
		base: time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)}
	b.SetNotifier(x.rec)
	x.acc = seedAccount(t, b, boardMe.Address)
	f := seedFolders(t, b, x.acc, []store.Folder{
		{Mailbox: "INBOX", Name: "Inbox", Path: "Inbox", Role: api.RoleInbox, Subscribed: true, Selectable: true},
		{Mailbox: "Sent", Name: "Sent", Path: "Sent", Role: api.RoleSent, Subscribed: true, Selectable: true},
		{Mailbox: "Archive", Name: "Archive", Path: "Archive", Role: api.RoleArchive, Subscribed: true, Selectable: true},
		{Mailbox: "Trash", Name: "Trash", Path: "Trash", Role: api.RoleTrash, Subscribed: true, Selectable: true},
		{Mailbox: "Drafts", Name: "Drafts", Path: "Drafts", Role: api.RoleDrafts, Subscribed: true, Selectable: true},
	})
	x.inbox, x.sent, x.archive, x.trash, x.drafts = f["INBOX"], f["Sent"], f["Archive"], f["Trash"], f["Drafts"]
	return x
}

// mail is a message to store with put().
type bmail struct {
	folder    store.Folder
	thread    string
	rfc       string
	inReplyTo string
	refs      []string // References
	from      api.Address
	to, cc    []api.Address
	replyTo   []api.Address
	subject   string
	at        time.Duration // after base
	date      time.Time     // overrides at for the Date header (no internal date then)
	text      string
	html      string // an HTML alternative of text: stored as the raw message, hasHTML set
	headers   map[string]string
	flagged   bool
	pending   bool // leave the bulk classification undone
}

// put stores the message with its body (classified as the store does on a
// body) and returns its id.
func (x *boardBox) put(m bmail) string {
	x.t.Helper()
	x.uid++
	date, internal := x.base.Add(m.at), x.base.Add(m.at)
	if !m.date.IsZero() {
		date, internal = m.date, time.Time{}
	}
	var flags []api.Flag
	if m.flagged {
		flags = append(flags, api.FlagFlagged)
	}
	if m.folder.Role == api.RoleSent {
		flags = append(flags, api.FlagSeen)
	}
	subject := m.subject
	if subject == "" {
		subject = "Lunch"
	}
	row := &store.Message{AccountID: x.acc, FolderID: m.folder.ID, UID: x.uid, ThreadID: m.thread, Subject: subject,
		Date: date, InternalDate: internal, From: []api.Address{m.from}, To: m.to, CC: m.cc, ReplyTo: m.replyTo, RFCMessageID: m.rfc,
		InReplyTo: m.inReplyTo, References: m.refs, Size: 100, Flags: flags, Snippet: firstLine(m.text)}
	if err := x.b.store.UpsertMessages(x.ctx, []*store.Message{row}); err != nil {
		x.t.Fatal(err)
	}
	if m.html != "" {
		raw := "From: " + m.from.Address + "\r\nSubject: " + subject + "\r\nMIME-Version: 1.0\r\n" +
			"Content-Type: multipart/alternative; boundary=\"b1\"\r\n\r\n" +
			"--b1\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + m.text + "\r\n" +
			"--b1\r\nContent-Type: text/html; charset=utf-8\r\n\r\n" + m.html + "\r\n--b1--\r\n"
		if _, err := x.b.store.PutMessageRaw(x.ctx, x.acc, row.ID, store.RawWrite{}, func(w io.Writer) error {
			_, err := io.WriteString(w, raw)
			return err
		}); err != nil {
			x.t.Fatal(err)
		}
	}
	if !m.pending {
		if err := x.b.store.SetMessageBody(x.ctx, row.ID, store.BodyUpdate{Text: m.text, HasHTML: m.html != "", Headers: m.headers,
			Snippet: firstLine(m.text), References: m.refs, State: store.BodyFetched}); err != nil {
			x.t.Fatal(err)
		}
	}
	return row.ID
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// drain evaluates every dirty thread.
func (x *boardBox) drain() {
	x.t.Helper()
	if err := x.b.drainBoard(x.ctx); err != nil {
		x.t.Fatal(err)
	}
}

// list returns board.list's cases by thread id.
func (x *boardBox) list() (*api.BoardListResult, map[api.ThreadID]api.BoardCase) {
	x.t.Helper()
	res, err := x.svc.List(x.ctx, api.BoardListParams{})
	if err != nil {
		x.t.Fatal(err)
	}
	out := map[api.ThreadID]api.BoardCase{}
	for _, c := range res.Cases {
		out[c.ThreadID] = c
	}
	return res, out
}

func (x *boardBox) caseOf(thread string) api.BoardCase {
	x.t.Helper()
	_, cases := x.list()
	c, ok := cases[api.ThreadID(thread)]
	if !ok {
		x.t.Fatalf("thread %s is not on the board", thread)
	}
	return c
}

func (x *boardBox) noCase(thread string) {
	x.t.Helper()
	_, cases := x.list()
	if c, ok := cases[api.ThreadID(thread)]; ok {
		x.t.Fatalf("thread %s is on the board: %s %s", thread, c.RuleState, c.RuleReason)
	}
}

// assistantOn turns the assistant preference on.
func (x *boardBox) assistantOn() {
	x.t.Helper()
	p := api.DefaultBoardPreferences()
	p.Assistant = true
	if _, err := x.svc.SetPreferences(x.ctx, api.BoardSetPreferencesParams{Preferences: p}); err != nil {
		x.t.Fatal(err)
	}
}

func wantCode(t *testing.T, what string, err error, code api.ErrorCode) *api.Error {
	t.Helper()
	var e *api.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("%s: err %v, want code %d", what, err, code)
	}
	return e
}

func tick() { time.Sleep(3 * time.Millisecond) } // store stamps have milliseconds

// The rules on mail end to end: store → worker → board.list.
func TestBoardMailStates(t *testing.T) {
	x := newBoardBox(t)
	// you.addressed: Alice asks me.
	x.put(bmail{folder: x.inbox, thread: "t_you", rfc: "you1", from: boardAlice, to: []api.Address{boardMe}, text: "Lunch tomorrow?"})
	// hot.flagged.
	x.put(bmail{folder: x.inbox, thread: "t_hot", rfc: "hot1", from: boardBob, to: []api.Address{boardMe}, flagged: true, subject: "Contract", text: "Sign it"})
	// hot.important.
	x.put(bmail{folder: x.inbox, thread: "t_imp", rfc: "imp1", from: boardBob, to: []api.Address{boardMe}, subject: "Server down",
		headers: map[string]string{"Importance": "high"}, text: "Down"})
	// info.ccOnly.
	x.put(bmail{folder: x.inbox, thread: "t_cc", rfc: "cc1", from: boardBob, to: []api.Address{boardAlice}, cc: []api.Address{boardMe}, subject: "FYI", text: "fyi"})
	// them.replied: Alice wrote, I answered her.
	x.put(bmail{folder: x.inbox, thread: "t_them", rfc: "th1", from: boardAlice, to: []api.Address{boardMe}, subject: "Offer", text: "Our offer"})
	x.put(bmail{folder: x.sent, thread: "t_them", rfc: "th2", inReplyTo: "th1", from: boardMe, to: []api.Address{boardAlice}, subject: "Re: Offer", at: time.Hour, text: "Thanks, I will look."})
	// them.asked: I started with a question.
	x.put(bmail{folder: x.sent, thread: "t_ask", rfc: "ask1", from: boardMe, to: []api.Address{boardBob}, subject: "Invoice", text: "Did you send the invoice?\n-- \nMe"})
	// No case: a statement to someone, and a forward of mine.
	x.put(bmail{folder: x.sent, thread: "t_stmt", rfc: "st1", from: boardMe, to: []api.Address{boardBob}, subject: "Notes", text: "Here are the notes."})
	x.put(bmail{folder: x.inbox, thread: "t_fwd", rfc: "fw1", from: boardAlice, to: []api.Address{boardMe}, subject: "Plan", text: "The plan"})
	x.put(bmail{folder: x.sent, thread: "t_fwd", rfc: "fw2", from: boardMe, to: []api.Address{boardBob}, subject: "Fwd: Plan", at: time.Hour,
		text: "What do you think?\n\n---------- Forwarded message ---------\nFrom: Alice\n\nThe plan"})
	// Bulk mail is left out.
	x.put(bmail{folder: x.inbox, thread: "t_news", rfc: "nw1", from: boardBob, to: []api.Address{boardMe}, subject: "Newsletter",
		headers: map[string]string{"List-Unsubscribe": "<mailto:unsub@example.invalid>", "List-Id": "<news.example.invalid>"}, text: "News"})
	// Waiting for its classification: no case yet.
	pending := x.put(bmail{folder: x.inbox, thread: "t_pending", rfc: "pe1", from: boardBob, to: []api.Address{boardMe}, subject: "Soon", pending: true})
	// A spoofed "from me" inbound mail never makes a case "them".
	x.put(bmail{folder: x.inbox, thread: "t_spoof", rfc: "sp1", from: boardMe, to: []api.Address{boardBob}, subject: "Urgent", text: "Pay this?"})
	// Known senders: Alice and Bob are (the user wrote to them above),
	// Carol is not: her mail to the user is info, also when she marks it
	// important; her flagged mail stays hot (the user's own flag).
	x.put(bmail{folder: x.inbox, thread: "t_carol", rfc: "ca1", from: boardCarol, to: []api.Address{boardMe}, subject: "Offer", text: "Buy?"})
	x.put(bmail{folder: x.inbox, thread: "t_carolimp", rfc: "ca2", from: boardCarol, to: []api.Address{boardMe}, subject: "Now",
		headers: map[string]string{"Importance": "high"}, text: "Now!"})
	x.put(bmail{folder: x.inbox, thread: "t_carolflag", rfc: "ca3", from: boardCarol, to: []api.Address{boardMe}, subject: "Flagged", flagged: true, text: "x"})
	// A sender known through Reply-To: the shop's no-reply address, whose
	// Reply-To is the help desk the user wrote to.
	help := api.Address{Name: "Help", Address: "help@shop.invalid"}
	x.put(bmail{folder: x.sent, thread: "t_helpsent", rfc: "hs1", from: boardMe, to: []api.Address{help}, subject: "Order 42", text: "Where is it."})
	x.put(bmail{folder: x.inbox, thread: "t_shop", rfc: "sh1", from: api.Address{Address: "noreply@shop.invalid"}, replyTo: []api.Address{help},
		to: []api.Address{boardMe}, subject: "Order 42 shipped", text: "Shipped"})
	// "you" is listed for 30 days by default: older is off the board.
	x.put(bmail{folder: x.inbox, thread: "t_oldyou", rfc: "oy1", from: boardAlice, to: []api.Address{boardMe}, subject: "Old",
		at: -38 * 24 * time.Hour, text: "Old"})
	x.drain()

	res, cases := x.list()
	if !res.Enabled || res.Assistant || res.Cases == nil || res.Commitments == nil {
		t.Fatalf("list: %+v", res)
	}
	want := map[string]api.BoardReason{
		"t_you": api.BoardReasonYouAddressed, "t_hot": api.BoardReasonHotFlagged, "t_imp": api.BoardReasonHotImportant,
		"t_cc": api.BoardReasonInfoCcOnly, "t_them": api.BoardReasonThemReplied, "t_ask": api.BoardReasonThemAsked,
		"t_spoof": api.BoardReasonInfoNotAddressed,
		"t_carol": api.BoardReasonInfoUnknownSender, "t_carolimp": api.BoardReasonInfoUnknownSender,
		"t_carolflag": api.BoardReasonHotFlagged, "t_shop": api.BoardReasonYouAddressed,
	}
	for th, reason := range want {
		c, ok := cases[api.ThreadID(th)]
		if !ok {
			t.Errorf("%s: no case", th)
			continue
		}
		if c.RuleReason != reason || c.Visibility != api.BoardLive || c.Version == 0 || !strings.HasPrefix(string(c.ID), "c_") {
			t.Errorf("%s: %s %s %s v%d", th, c.RuleState, c.RuleReason, c.Visibility, c.Version)
		}
	}
	for _, th := range []string{"t_stmt", "t_fwd", "t_news", "t_pending", "t_helpsent", "t_oldyou"} {
		if c, ok := cases[api.ThreadID(th)]; ok {
			t.Errorf("%s is a case: %s %s", th, c.RuleState, c.RuleReason)
		}
	}
	if len(cases) != len(want) {
		t.Errorf("cases: %d, want %d", len(cases), len(want))
	}
	them := cases["t_them"]
	if them.Person.Address != boardAlice.Address || them.MessageCount != 2 || them.Subject != "Offer" ||
		them.ReplyFolderID != api.FolderID(x.inbox.ID) || !them.CanArchive {
		t.Errorf("them case: %+v", them)
	}
	ask := cases["t_ask"]
	if ask.Person.Address != boardBob.Address || ask.CanArchive {
		t.Errorf("ask case: %+v", ask)
	}
	// Classifying the pending message makes it count.
	if err := x.b.store.SetMessageBody(x.ctx, pending, store.BodyUpdate{Text: "Soon?", State: store.BodyFetched}); err != nil {
		t.Fatal(err)
	}
	x.drain()
	if c := x.caseOf("t_pending"); c.RuleReason != api.BoardReasonYouAddressed {
		t.Errorf("classified: %s", c.RuleReason)
	}

	// board.get: the members that count, plain text, the signature cut.
	got, err := x.svc.Get(x.ctx, api.BoardGetParams{CaseID: ask.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 1 || got.Messages[0].Text != "Did you send the invoice?" || !got.Messages[0].Trimmed || !got.Messages[0].Mine {
		t.Errorf("get: %+v", got.Messages)
	}
	_, err = x.svc.Get(x.ctx, api.BoardGetParams{CaseID: "c_00000000000000000000000000000000"})
	wantCode(t, "unknown case", err, api.CodeCaseNotFound)
	_, err = x.svc.Get(x.ctx, api.BoardGetParams{})
	wantCode(t, "empty case id", err, api.CodeInvalidArgument)
}

// Done, reopening by later inbound mail (by when it was stored, not by its
// Date), remind with the clock moved on, the user's state.
func TestBoardDoneRemindAndState(t *testing.T) {
	x := newBoardBox(t)
	x.put(bmail{folder: x.inbox, thread: "t_a", rfc: "a1", from: boardAlice, to: []api.Address{boardMe}, text: "Hi"})
	x.drain()
	c := x.caseOf("t_a")
	tick()
	done, err := x.svc.SetDone(x.ctx, api.BoardSetDoneParams{CaseID: c.ID, Done: true})
	if err != nil || done.Case.Visibility != api.BoardDone || done.Case.DoneAt == nil || done.Case.Version <= c.Version {
		t.Fatalf("done: %+v %v", done, err)
	}
	tick()
	// A message with a forged old Date (no server arrival) stored now: not
	// newer, no reopening.
	x.put(bmail{folder: x.inbox, thread: "t_a", rfc: "a2", inReplyTo: "a1", from: boardAlice, to: []api.Address{boardMe},
		date: x.base.Add(-30 * 24 * time.Hour), text: "old"})
	x.drain()
	if got := x.caseOf("t_a"); got.Visibility != api.BoardDone {
		t.Fatalf("forged date reopened: %s", got.Visibility)
	}
	// My own reply never reopens.
	x.put(bmail{folder: x.sent, thread: "t_a", rfc: "a3", inReplyTo: "a1", from: boardMe, to: []api.Address{boardAlice}, at: 2 * time.Hour, text: "ok"})
	x.drain()
	if got := x.caseOf("t_a"); got.Visibility != api.BoardDone {
		t.Fatalf("own reply reopened: %s", got.Visibility)
	}
	// A new inbound message does.
	x.put(bmail{folder: x.inbox, thread: "t_a", rfc: "a4", inReplyTo: "a3", from: boardAlice, to: []api.Address{boardMe}, at: 47 * time.Hour, text: "and?"})
	x.drain()
	got := x.caseOf("t_a")
	if got.Visibility != api.BoardLive || got.RuleReason != api.BoardReasonYouRepliedToYou {
		t.Fatalf("after a new message: %s %s", got.Visibility, got.RuleReason)
	}

	// Remind: the past and more than a year ahead are refused.
	now := time.Now().UTC()
	for _, until := range []time.Time{now.Add(-time.Minute), now.Add(api.MaxBoardRemind + time.Hour)} {
		_, err := x.svc.Remind(x.ctx, api.BoardRemindParams{CaseID: c.ID, Until: &until})
		wantCode(t, "remind "+until.String(), err, api.CodeInvalidArgument)
	}
	until := now.Add(time.Hour)
	r, err := x.svc.Remind(x.ctx, api.BoardRemindParams{CaseID: c.ID, Until: &until})
	if err != nil || r.Case.Visibility != api.BoardSnoozed || r.Case.RemindAt == nil {
		t.Fatalf("remind: %+v %v", r, err)
	}
	// The clock moves past it: the due remind ends, the case is live.
	x.b.board.now = func() time.Time { return now.Add(2 * time.Hour) }
	x.rec.take()
	x.b.clearDueReminds(x.ctx)
	if got := x.caseOf("t_a"); got.Visibility != api.BoardLive || got.RemindAt != nil {
		t.Fatalf("after the remind: %+v", got)
	}
	x.b.board.now = time.Now

	// Done clears a remind, a remind clears done.
	if _, err := x.svc.Remind(x.ctx, api.BoardRemindParams{CaseID: c.ID, Until: &until}); err != nil {
		t.Fatal(err)
	}
	d, _ := x.svc.SetDone(x.ctx, api.BoardSetDoneParams{CaseID: c.ID, Done: true})
	if d.Case.RemindAt != nil || d.Case.Visibility != api.BoardDone {
		t.Fatalf("done after remind: %+v", d.Case)
	}
	r, _ = x.svc.Remind(x.ctx, api.BoardRemindParams{CaseID: c.ID, Until: &until})
	if r.Case.DoneAt != nil || r.Case.Visibility != api.BoardSnoozed {
		t.Fatalf("remind after done: %+v", r.Case)
	}
	if r, _ = x.svc.Remind(x.ctx, api.BoardRemindParams{CaseID: c.ID}); r.Case.Visibility != api.BoardLive {
		t.Fatalf("remind cleared: %+v", r.Case)
	}

	// The user's state wins and keeps a case the rules drop.
	st := api.BoardThem
	s, err := x.svc.SetState(x.ctx, api.BoardSetStateParams{CaseID: c.ID, State: &st})
	if err != nil || s.Case.UserState == nil || *s.Case.UserState != api.BoardThem {
		t.Fatalf("set state: %+v %v", s, err)
	}
	bad := api.BoardState("later")
	_, err = x.svc.SetState(x.ctx, api.BoardSetStateParams{CaseID: c.ID, State: &bad})
	wantCode(t, "unknown state", err, api.CodeInvalidArgument)
	// Every member to the trash: the rules make no case, the state keeps it.
	ids, _ := x.b.store.ThreadMessages(x.ctx, x.acc, "t_a", "", 0)
	var inboxIDs []string
	for _, m := range ids {
		if m.FolderID == x.inbox.ID {
			inboxIDs = append(inboxIDs, m.ID)
		}
	}
	if _, err := x.b.store.TrashMessages(x.ctx, x.acc, inboxIDs, x.trash.ID); err != nil {
		t.Fatal(err)
	}
	x.drain()
	if got := x.caseOf("t_a"); got.RuleReason != api.BoardReasonKept {
		t.Fatalf("kept: %s %s", got.RuleState, got.RuleReason)
	}
	if _, err := x.svc.SetState(x.ctx, api.BoardSetStateParams{CaseID: c.ID}); err != nil {
		t.Fatal(err)
	}
	x.drain()
	x.noCase("t_a")
}

// board.archive moves the inbox members and marks the case done; without
// an archive folder it only marks it done.
func TestBoardArchive(t *testing.T) {
	x := newBoardBox(t)
	a1 := x.put(bmail{folder: x.inbox, thread: "t_a", rfc: "a1", from: boardAlice, to: []api.Address{boardMe}, text: "Hi"})
	x.put(bmail{folder: x.sent, thread: "t_a", rfc: "a2", inReplyTo: "a1", from: boardMe, to: []api.Address{boardAlice}, at: time.Hour, text: "Yes"})
	a3 := x.put(bmail{folder: x.inbox, thread: "t_a", rfc: "a3", inReplyTo: "a2", from: boardAlice, to: []api.Address{boardMe}, at: 2 * time.Hour, text: "Good"})
	x.drain()
	c := x.caseOf("t_a")
	if !c.CanArchive {
		t.Fatalf("canArchive: %+v", c)
	}
	res, err := x.svc.Archive(x.ctx, api.BoardArchiveParams{CaseID: c.ID})
	if err != nil || res.Archived != 2 || res.NoArchive || res.Case.Visibility != api.BoardDone {
		t.Fatalf("archive: %+v %v", res, err)
	}
	for _, id := range []string{a1, a3} {
		m, err := x.b.store.GetMessage(x.ctx, x.acc, id)
		if err != nil || m.FolderID != x.archive.ID {
			t.Errorf("%s in %s, %v", id, m.FolderID, err)
		}
	}
	x.drain()
	if got := x.caseOf("t_a"); got.Visibility != api.BoardDone || got.CanArchive {
		t.Errorf("after archiving: %+v", got)
	}

	// An account without an archive folder.
	y := newBoardBox(t)
	if _, err := y.b.store.DB().ExecContext(y.ctx, `UPDATE folders SET role = 'none' WHERE id = ?`, y.archive.ID); err != nil {
		t.Fatal(err)
	}
	y.b.board.idMu.Lock()
	y.b.board.ids = map[string]boardIdentityEntry{}
	y.b.board.idMu.Unlock()
	y.put(bmail{folder: y.inbox, thread: "t_b", rfc: "b1", from: boardAlice, to: []api.Address{boardMe}, text: "Hi"})
	y.drain()
	c = y.caseOf("t_b")
	if c.CanArchive {
		t.Fatalf("canArchive without a folder: %+v", c)
	}
	res, err = y.svc.Archive(y.ctx, api.BoardArchiveParams{CaseID: c.ID})
	if err != nil || res.Archived != 0 || !res.NoArchive || res.Case.Visibility != api.BoardDone {
		t.Fatalf("archive without a folder: %+v %v", res, err)
	}
}

// Annotate and commit: the happy paths, a stale key, quotes, limits, the
// other party's words as a commitment, the draft link and discarding it,
// and the runs that count them.
func TestBoardTriage(t *testing.T) {
	x := newBoardBox(t)
	in := x.put(bmail{folder: x.inbox, thread: "t_a", rfc: "a1", from: boardAlice, to: []api.Address{boardMe}, subject: "Report",
		text: "Please send the report by Friday 3 October.\nThanks"})
	mine := x.put(bmail{folder: x.sent, thread: "t_a", rfc: "a2", inReplyTo: "a1", from: boardMe, to: []api.Address{boardAlice},
		subject: "Re: Report", at: time.Hour, text: "I will send it on Thursday morning. I will also book the room.\n\nOn Mon, Alice wrote:\n> Please send the report by Friday 3 October."})
	x.drain()

	// The assistant off: no queue, no annotations.
	_, err := x.svc.Queue(x.ctx, api.BoardQueueParams{})
	wantCode(t, "queue with the assistant off", err, api.CodeInvalidArgument)
	x.assistantOn()
	res, _ := x.list()
	if res.Triage.Queue != 1 || !res.Assistant {
		t.Fatalf("triage before: %+v", res.Triage)
	}
	_, err = x.svc.Queue(x.ctx, api.BoardQueueParams{Limit: api.MaxBoardQueueLimit + 1})
	wantCode(t, "queue limit", err, api.CodeInvalidArgument)
	q, err := x.svc.Queue(x.ctx, api.BoardQueueParams{})
	if err != nil || len(q.Items) != 1 || q.Remaining != 0 {
		t.Fatalf("queue: %+v %v", q, err)
	}
	item := q.Items[0]
	if len(item.InputKey) != 32 || len(item.Messages) != 2 || !slices.Contains(item.Own, boardMe.Address) ||
		item.Messages[1].Text != "I will send it on Thursday morning. I will also book the room." || !item.Messages[1].Truncated || !item.Messages[1].Mine ||
		item.ReplyMessageID != api.MessageID(in) {
		t.Fatalf("queue item: %+v", item)
	}

	run, err := x.svc.RunStart(x.ctx, api.BoardRunStartParams{Trigger: api.TriggerManual, Source: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	ann := func(p api.BoardAnnotateParams) (*api.BoardAnnotateResult, error) {
		p.CaseID, p.RunID = item.CaseID, run.RunID
		if p.InputKey == "" {
			p.InputKey = item.InputKey
		}
		if p.Source == "" {
			p.Source = "claude"
		}
		return x.svc.Annotate(x.ctx, p)
	}
	friday := x.base.Add(72 * time.Hour)
	// Refusals, each counted: a stale key, a quote not in the message, an
	// overlong title, a due out of range, an empty source (in the run the
	// call names).
	_, err = ann(api.BoardAnnotateParams{InputKey: strings.Repeat("0", 32), Title: "x"})
	wantCode(t, "stale key", err, api.CodeConflict)
	_, err = ann(api.BoardAnnotateParams{Due: &api.BoardDue{At: friday, Quote: "by Monday 6 October", MessageID: api.MessageID(in)}})
	e := wantCode(t, "quote not found", err, api.CodeQuoteNotFound)
	if d, ok := e.Data.(api.QuoteNotFoundData); !ok || d.Field != api.QuoteFieldDue {
		t.Fatalf("quoteNotFound data: %#v", e.Data)
	}
	_, err = ann(api.BoardAnnotateParams{Title: strings.Repeat("t", api.MaxBoardTitleBytes+1)})
	wantCode(t, "long title", err, api.CodeInvalidArgument)
	_, err = ann(api.BoardAnnotateParams{Due: &api.BoardDue{At: x.base.Add(500 * 24 * time.Hour), Quote: "by Friday 3 October", MessageID: api.MessageID(in)}})
	wantCode(t, "due out of range", err, api.CodeInvalidArgument)
	_, err = ann(api.BoardAnnotateParams{Source: " "})
	wantCode(t, "empty source", err, api.CodeInvalidArgument)

	// The happy path; URLs and bidi characters are cleaned away.
	st := api.BoardYou
	a, err := ann(api.BoardAnnotateParams{State: &st, Title: "Send the report ‮https://evil.invalid/x", Summary: "Alice wants\n\nthe report.",
		Why: "She asked", Tasks: []string{"Write it", " ", "Send it"},
		Due: &api.BoardDue{At: friday, Quote: "the report by Friday 3 October", MessageID: api.MessageID(in)}})
	if err != nil {
		t.Fatal(err)
	}
	an := a.Case.Annotation
	if an == nil || an.Title != "Send the report" || an.Summary != "Alice wants\n\nthe report." || len(an.Tasks) != 2 ||
		an.Due == nil || an.Due.Quote != "the report by Friday 3 October" || an.Stale || an.State == nil || an.Source != "claude" {
		t.Fatalf("annotation: %+v", an)
	}

	// Commitments: the quote must be in my own text, not in what I quoted.
	runID := run.RunID
	cm := func(p api.BoardCommitParams) (*api.BoardCommitResult, error) {
		p.CaseID, p.InputKey, p.Source, p.RunID = item.CaseID, item.InputKey, "claude", runID
		return x.svc.Commit(x.ctx, p)
	}
	_, err = cm(api.BoardCommitParams{MessageID: api.MessageID(mine), Text: "Send the report", Quote: "send the report by Friday"})
	e = wantCode(t, "quote from the quoted history", err, api.CodeQuoteNotFound)
	if d, _ := e.Data.(api.QuoteNotFoundData); d.Field != api.QuoteFieldCommitment {
		t.Fatalf("commitment data: %#v", e.Data)
	}
	_, err = cm(api.BoardCommitParams{MessageID: api.MessageID(in), Text: "Send the report", Quote: "send the report by Friday"})
	wantCode(t, "the other party's message", err, api.CodeInvalidArgument)
	k, err := cm(api.BoardCommitParams{MessageID: api.MessageID(mine), Text: "Send the report on Thursday", Quote: "I will send it on Thursday morning", Due: &friday})
	if err != nil || k.Commitment.State != api.CommitmentOpen || k.Commitment.Quote != "I will send it on Thursday morning" {
		t.Fatalf("commit: %+v %v", k, err)
	}
	// The same promise again, in other words or with a shorter span of
	// the sentence: the one recorded comes back, nothing is counted.
	for _, quote := range []string{"I will send it on Thursday morning", "I will send it\u200b on Thursday morning", "send it on Thursday"} {
		again, err := cm(api.BoardCommitParams{MessageID: api.MessageID(mine), Text: "Report on Thursday", Quote: quote})
		if err != nil || !again.Existing || again.Commitment.ID != k.Commitment.ID || again.Commitment.Text != "Send the report on Thursday" {
			t.Fatalf("again %q: %+v %v", quote, again, err)
		}
	}
	res, _ = x.list()
	if len(res.Commitments) != 1 || res.Triage.Queue != 0 {
		t.Fatalf("after triage: %d commitments, queue %d", len(res.Commitments), res.Triage.Queue)
	}
	if _, err := x.svc.RunEnd(x.ctx, api.BoardRunEndParams{RunID: run.RunID}); err != nil {
		t.Fatal(err)
	}
	if _, err := x.svc.RunEnd(x.ctx, api.BoardRunEndParams{RunID: run.RunID, Error: "exploded"}); err != nil {
		t.Fatalf("ending twice: %v", err)
	}
	r, err := x.b.store.GetBoardRun(x.ctx, string(run.RunID))
	if err != nil || r.Annotated != 1 || r.Commitments != 1 || r.Rejected != 7 || r.Error != "" || r.EndedAt.IsZero() {
		t.Fatalf("manual run: %+v %v", r, err)
	}
	_, err = x.svc.RunEnd(x.ctx, api.BoardRunEndParams{RunID: "r_nope"})
	wantCode(t, "unknown run", err, api.CodeInvalidArgument)
	_, err = x.svc.RunStart(x.ctx, api.BoardRunStartParams{Trigger: api.TriggerExternal, Source: "x"})
	wantCode(t, "external run start", err, api.CodeInvalidArgument)

	// After the run ended, a call naming it counts in the implicit
	// external run of its source and day.
	k2, err := cm(api.BoardCommitParams{MessageID: api.MessageID(mine), Text: "Book the room", Quote: "I will also book the room"})
	if err != nil {
		t.Fatal(err)
	}
	ext := externalRun(t, x, "claude")
	if ext.Commitments != 1 || ext.Rejected != 0 || ext.Trigger != api.TriggerExternal {
		t.Fatalf("external run: %+v", ext)
	}
	// An auto run counts toward annotatedTodayAuto.
	auto, _ := x.svc.RunStart(x.ctx, api.BoardRunStartParams{Trigger: api.TriggerAuto, Source: "claude"})
	if _, err := x.svc.Annotate(x.ctx, api.BoardAnnotateParams{CaseID: item.CaseID, InputKey: item.InputKey, RunID: auto.RunID,
		Source: "claude", Title: "Again"}); err != nil {
		t.Fatal(err)
	}
	res, _ = x.list()
	if res.Triage.AnnotatedTodayAuto != 1 || res.Triage.LastRun == nil || res.Triage.LastRun.Trigger != api.TriggerAuto || res.Triage.LastRun.EndedAt != nil {
		t.Fatalf("triage: %+v %+v", res.Triage, res.Triage.LastRun)
	}
	if _, err := x.svc.SetCommitment(x.ctx, api.BoardSetCommitmentParams{CommitmentID: k2.Commitment.ID, Done: true}); err != nil {
		t.Fatal(err)
	}
	_, err = x.svc.SetCommitment(x.ctx, api.BoardSetCommitmentParams{CommitmentID: "k_nope", Done: true})
	wantCode(t, "unknown commitment", err, api.CodeCaseNotFound)

	// A draft link: a draft of another thread is refused, a reply to the
	// case is linked, discarding deletes it.
	other := x.put(bmail{folder: x.inbox, thread: "t_other", rfc: "o1", from: boardBob, to: []api.Address{boardMe}, subject: "Other", text: "x"})
	stray, err := x.b.Drafts().Save(x.ctx, api.DraftSaveParams{Draft: api.Draft{AccountID: api.AccountID(x.acc), To: []api.Address{boardBob},
		Subject: "Re: Other", TextBody: "no", InReplyTo: api.MessageID(other)}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = x.svc.Annotate(x.ctx, api.BoardAnnotateParams{CaseID: item.CaseID, InputKey: item.InputKey, Source: "claude", DraftID: stray.DraftID})
	wantCode(t, "a draft of another thread", err, api.CodeInvalidArgument)
	reply, err := x.b.Drafts().Save(x.ctx, api.DraftSaveParams{Draft: api.Draft{AccountID: api.AccountID(x.acc), To: []api.Address{boardAlice},
		Subject: "Re: Report", TextBody: "Here it is.", InReplyTo: api.MessageID(in)}})
	if err != nil {
		t.Fatal(err)
	}
	a, err = x.svc.Annotate(x.ctx, api.BoardAnnotateParams{CaseID: item.CaseID, InputKey: item.InputKey, Source: "claude", DraftID: reply.DraftID})
	if err != nil || a.Case.Draft == nil || a.Case.Draft.Text != "Here it is." {
		t.Fatalf("draft link: %+v %v", a, err)
	}
	// Saving the draft tells the clients.
	x.rec.take()
	if _, err := x.b.Drafts().Save(x.ctx, api.DraftSaveParams{Draft: api.Draft{ID: reply.DraftID, Version: reply.Version,
		AccountID: api.AccountID(x.acc), To: []api.Address{boardAlice}, Subject: "Re: Report", TextBody: "Here it is, v2.", InReplyTo: api.MessageID(in)}}); err != nil {
		t.Fatal(err)
	}
	waitBoardEvent(t, x.rec, x.acc)
	d, err := x.svc.DiscardDraft(x.ctx, api.BoardDiscardDraftParams{CaseID: item.CaseID})
	if err != nil || d.Case.Draft != nil {
		t.Fatalf("discard: %+v %v", d, err)
	}
	if _, err := x.b.store.GetDraft(x.ctx, x.acc, string(reply.DraftID)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("draft still stored: %v", err)
	}
	if _, err := x.svc.DiscardDraft(x.ctx, api.BoardDiscardDraftParams{CaseID: item.CaseID}); err != nil {
		t.Fatalf("discard again: %v", err)
	}

	// A new member makes the annotation stale and the case queued again.
	x.put(bmail{folder: x.inbox, thread: "t_a", rfc: "a3", inReplyTo: "a2", from: boardAlice, to: []api.Address{boardMe}, at: 3 * time.Hour, text: "Thanks"})
	x.drain()
	c := x.caseOf("t_a")
	if c.Annotation == nil || !c.Annotation.Stale {
		t.Fatalf("not stale: %+v", c.Annotation)
	}
	_, err = x.svc.Annotate(x.ctx, api.BoardAnnotateParams{CaseID: item.CaseID, InputKey: item.InputKey, Source: "claude"})
	wantCode(t, "the old key", err, api.CodeConflict)
	q, _ = x.svc.Queue(x.ctx, api.BoardQueueParams{})
	if len(q.Items) != 2 { // t_a again, and t_other
		t.Fatalf("queue after the new member: %d", len(q.Items))
	}
}

// externalRun reads the implicit run of a source today.
func externalRun(t *testing.T, x *boardBox, source string) store.BoardRun {
	t.Helper()
	day, _ := localDay(time.Now())
	var id string
	if err := x.b.store.DB().QueryRowContext(x.ctx, `SELECT id FROM board_runs WHERE trigger = 'external' AND source = ? AND day = ?`,
		source, day).Scan(&id); err != nil {
		t.Fatal(err)
	}
	r, err := x.b.store.GetBoardRun(x.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func waitBoardEvent(t *testing.T, r *boardRecorder, account string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		for _, ev := range r.events {
			if ev.AccountIDs == nil || slices.Contains(ev.AccountIDs, api.AccountID(account)) {
				r.mu.Unlock()
				return
			}
		}
		r.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no notify.boardChanged for %s", account)
}

// Preferences: defaults, the round trip, validation, the disabled board
// keeping the user's decisions.
// board.runEnd's usage reaches board.list's usage24h: only from the call
// that ends the run, only runs ended within 24 hours, never negative.
func TestBoardRunUsage(t *testing.T) {
	old := boardNotifyEvery
	boardNotifyEvery = 20 * time.Millisecond
	t.Cleanup(func() { boardNotifyEvery = old })
	x := newBoardBox(t)
	if res, _ := x.list(); res.Triage.Usage24h != nil {
		t.Fatalf("usage before any run: %+v", res.Triage.Usage24h)
	}
	start := func() api.BoardRunID {
		t.Helper()
		r, err := x.svc.RunStart(x.ctx, api.BoardRunStartParams{Trigger: api.TriggerManual, Source: "claude"})
		if err != nil {
			t.Fatal(err)
		}
		return r.RunID
	}
	a := start()
	_, err := x.svc.RunEnd(x.ctx, api.BoardRunEndParams{RunID: a, Usage: &api.BoardUsage{InputTokens: -1}})
	wantCode(t, "negative usage", err, api.CodeInvalidArgument)
	if r, _ := x.b.store.GetBoardRun(x.ctx, string(a)); !r.EndedAt.IsZero() {
		t.Fatalf("a refused end ended the run: %+v", r)
	}

	time.Sleep(4 * boardNotifyEvery) // whatever the setup sent is out
	x.rec.take()
	u := api.BoardUsage{InputTokens: 1000, OutputTokens: 200, CacheCreationInputTokens: 30, CacheReadInputTokens: 4000}
	if _, err := x.svc.RunEnd(x.ctx, api.BoardRunEndParams{RunID: a, Usage: &u}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		x.rec.mu.Lock()
		n := len(x.rec.events)
		x.rec.mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no notify.boardChanged after board.runEnd")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Ending again is a no-op, its usage included.
	if _, err := x.svc.RunEnd(x.ctx, api.BoardRunEndParams{RunID: a, Usage: &api.BoardUsage{InputTokens: 5}}); err != nil {
		t.Fatalf("ending twice: %v", err)
	}
	// A run ended without usage, and one closed by the daemon, add nothing.
	b := start()
	if _, err := x.svc.RunEnd(x.ctx, api.BoardRunEndParams{RunID: b, Error: api.RunCancelled}); err != nil {
		t.Fatal(err)
	}
	c := start()
	if _, err := x.b.store.CloseOpenBoardRuns(x.ctx, time.Now().Add(time.Minute), time.Now()); err != nil {
		t.Fatal(err)
	}
	// The implicit external run cannot take usage.
	if _, err := x.b.store.CountBoardRejected(x.ctx, store.BoardRunRef{Source: "desktop"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	ext := externalRun(t, x, "desktop")
	if _, err := x.svc.RunEnd(x.ctx, api.BoardRunEndParams{RunID: api.BoardRunID(ext.ID), Usage: &u}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{string(b), string(c), ext.ID} {
		if r, _ := x.b.store.GetBoardRun(x.ctx, id); r.Usage != nil {
			t.Fatalf("run %s has usage %+v", id, r.Usage)
		}
	}
	res, _ := x.list()
	if got := res.Triage.Usage24h; got == nil || *got != (api.BoardUsageTotal{BoardUsage: u, Runs: 1}) {
		t.Fatalf("usage24h: %+v", got)
	}
	// 24 hours after the run ended it is out of the window.
	later := time.Now().Add(24*time.Hour + time.Minute)
	x.b.board.now = func() time.Time { return later }
	t.Cleanup(func() { x.b.board.now = time.Now })
	if res, _ := x.list(); res.Triage.Usage24h != nil {
		t.Fatalf("usage24h a day later: %+v", res.Triage.Usage24h)
	}
}

func TestBoardPreferences(t *testing.T) {
	x := newBoardBox(t)
	p, err := x.svc.Preferences(x.ctx, api.BoardPreferencesParams{})
	if err != nil || fmt.Sprint(p.Preferences) != fmt.Sprint(api.DefaultBoardPreferences()) {
		t.Fatalf("defaults: %+v %v", p, err)
	}
	if w := p.Preferences.Windows; w.Hot != 90 || w.You != 30 || w.Them != 30 || w.Info != 14 {
		t.Fatalf("default windows: %+v", w)
	}
	next := api.DefaultBoardPreferences()
	next.Assistant, next.AutoTriage, next.AutoTriageMinutes, next.AutoTriageDailyCases = true, true, 60, 0
	next.Windows.Info = 7
	next.TriageAccounts = []api.AccountID{api.AccountID(x.acc), api.AccountID(x.acc)}
	set, err := x.svc.SetPreferences(x.ctx, api.BoardSetPreferencesParams{Preferences: next})
	if err != nil || len(set.Preferences.TriageAccounts) != 1 {
		t.Fatalf("set: %+v %v", set, err)
	}
	got, _ := x.svc.Preferences(x.ctx, api.BoardPreferencesParams{})
	if fmt.Sprint(got.Preferences) != fmt.Sprint(set.Preferences) {
		t.Fatalf("round trip: %+v, want %+v", got.Preferences, set.Preferences)
	}
	for name, edit := range map[string]func(*api.BoardPreferences){
		"window 0":         func(p *api.BoardPreferences) { p.Windows.Hot = 0 },
		"window 366":       func(p *api.BoardPreferences) { p.Windows.Them = 366 },
		"minutes":          func(p *api.BoardPreferences) { p.AutoTriageMinutes = 4 },
		"daily":            func(p *api.BoardPreferences) { p.AutoTriageDailyCases = 1001 },
		"unknown account":  func(p *api.BoardPreferences) { p.TriageAccounts = []api.AccountID{"acc_nope"} },
		"negative minutes": func(p *api.BoardPreferences) { p.AutoTriageMinutes = -1 },
	} {
		bad := api.DefaultBoardPreferences()
		edit(&bad)
		_, err := x.svc.SetPreferences(x.ctx, api.BoardSetPreferencesParams{Preferences: bad})
		wantCode(t, name, err, api.CodeInvalidArgument)
	}

	// The disabled board: no cases, every other method refused, the user's
	// state kept for later.
	x.put(bmail{folder: x.inbox, thread: "t_a", rfc: "a1", from: boardAlice, to: []api.Address{boardMe}, text: "Hi"})
	x.drain()
	c := x.caseOf("t_a")
	st := api.BoardInfo
	if _, err := x.svc.SetState(x.ctx, api.BoardSetStateParams{CaseID: c.ID, State: &st}); err != nil {
		t.Fatal(err)
	}
	off := set.Preferences
	off.Enabled = false
	if _, err := x.svc.SetPreferences(x.ctx, api.BoardSetPreferencesParams{Preferences: off}); err != nil {
		t.Fatal(err)
	}
	res, _ := x.list()
	if res.Enabled || len(res.Cases) != 0 {
		t.Fatalf("disabled list: %+v", res)
	}
	_, err = x.svc.Get(x.ctx, api.BoardGetParams{CaseID: c.ID})
	wantCode(t, "get while disabled", err, api.CodeInvalidArgument)
	_, err = x.svc.SetDone(x.ctx, api.BoardSetDoneParams{CaseID: c.ID, Done: true})
	wantCode(t, "done while disabled", err, api.CodeInvalidArgument)
	_, err = x.svc.RunStart(x.ctx, api.BoardRunStartParams{Trigger: api.TriggerManual, Source: "x"})
	wantCode(t, "run while disabled", err, api.CodeInvalidArgument)
	x.drain() // computes nothing
	on := off
	on.Enabled = true
	if _, err := x.svc.SetPreferences(x.ctx, api.BoardSetPreferencesParams{Preferences: on}); err != nil {
		t.Fatal(err)
	}
	if got := x.caseOf("t_a"); got.UserState == nil || *got.UserState != api.BoardInfo {
		t.Fatalf("state after the board came back: %+v", got)
	}
}

// Jira: the store fed as the jira syncer does.
func TestBoardJira(t *testing.T) {
	x := newBoardBox(t)
	res, err := x.b.Accounts().Add(x.ctx, api.AccountAddParams{Config: jiraConfig(), Credentials: api.Credentials{Password: "tok"}})
	if err != nil {
		t.Fatal(err)
	}
	jacc := string(res.AccountID)
	folders := seedFolders(t, x.b, jacc, []store.Folder{
		{Mailbox: "space:10000", Name: "IT Service Desk", Path: "IT Service Desk", Selectable: true, Subscribed: true},
		{Mailbox: "view:assignedToMe", Name: "Assigned", Path: "Assigned", Virtual: api.VirtualAssignedToMe, Selectable: true, Subscribed: true},
	})
	space, view := folders["space:10000"], folders["view:assignedToMe"]
	item := func(f store.Folder, issue, remote, author string, kind api.IssueItemKind, at time.Duration) {
		m := &store.Message{AccountID: jacc, FolderID: f.ID, RemoteID: remote, Subject: "ITSD-" + issue + ": Printer",
			From: []api.Address{{Name: author, Address: author + "@users.jira.invalid"}}, Date: x.base.Add(at), InternalDate: x.base.Add(at),
			RFCMessageID: "<" + strings.ReplaceAll(remote, ":", ".") + ".issue." + issue + "@acme.malachi.invalid>", Size: 10,
			ThreadID: store.IssueThreadID(issue)}
		if err := x.b.store.UpsertMessages(x.ctx, []*store.Message{m}); err != nil {
			t.Fatal(err)
		}
		if f.Virtual == "" {
			if err := x.b.store.PutIssueItems(x.ctx, jacc, []store.IssueItem{{RemoteID: remote, IssueID: issue, Kind: kind, AuthorID: author}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	issue := func(id, assignee, statusID string, cat api.IssueStatusCategory, watching bool) {
		if err := x.b.store.PutIssue(x.ctx, store.Issue{AccountID: jacc, IssueID: id, Key: "ITSD-" + id, SpaceID: "10000", Summary: "Printer",
			Status: "Open", StatusID: statusID, StatusCategory: cat, AssigneeID: assignee, ReporterID: "boss", Watching: watching}); err != nil {
			t.Fatal(err)
		}
	}
	// 1: assigned to me, someone else's comment last → you.
	issue("1", "me", "1", api.StatusCategoryTodo, false)
	item(space, "1", "i:1", "boss", api.IssueItemDescription, 0)
	item(view, "1", "i:1", "boss", api.IssueItemDescription, 0)
	item(space, "1", "c:11", "jana", api.IssueItemComment, time.Hour)
	item(space, "1", "h:12", "me", api.IssueItemEvent, 2*time.Hour) // events never decide
	// 2: my comment last → them.
	issue("2", "jana", "1", api.StatusCategoryTodo, false)
	item(space, "2", "i:2", "boss", api.IssueItemDescription, 0)
	item(space, "2", "c:21", "me", api.IssueItemComment, time.Hour)
	// 3: only watched → info.
	issue("3", "jana", "1", api.StatusCategoryTodo, true)
	item(space, "3", "i:3", "boss", api.IssueItemDescription, 0)
	// 4: done → no case; 5: a closed status of the account → no case.
	issue("4", "me", "9", api.StatusCategoryDone, false)
	item(space, "4", "i:4", "boss", api.IssueItemDescription, 0)
	issue("5", "me", "7", api.StatusCategoryInProgress, false)
	item(space, "5", "i:5", "boss", api.IssueItemDescription, 0)

	// Without the user's id there are no jira cases.
	x.drain()
	_, cases := x.list()
	for th := range cases {
		if strings.HasPrefix(string(th), "jira:") {
			t.Fatalf("a jira case without the user's id: %s", th)
		}
	}
	if err := x.b.store.SetMeta(x.ctx, store.MetaIssueMePrefix+jacc, `{"id":"me"}`); err != nil {
		t.Fatal(err)
	}
	// closedStatuses of the account (account.update marks it dirty).
	cfg := jiraConfig()
	cfg.Jira.ClosedStatuses = []api.StatusRef{{ID: "7", Name: "Won't do"}}
	if _, err := x.b.Accounts().Update(x.ctx, api.AccountUpdateParams{AccountID: api.AccountID(jacc), Config: cfg}); err != nil {
		t.Fatal(err)
	}
	x.drain()
	_, cases = x.list()
	want := map[string]api.BoardReason{"jira:1": api.BoardReasonJiraAssigned, "jira:2": api.BoardReasonJiraYourComment, "jira:3": api.BoardReasonJiraWatching}
	for th, reason := range want {
		c, ok := cases[api.ThreadID(th)]
		if !ok || c.RuleReason != reason || c.Issue == nil || c.Issue.Key != "ITSD-"+strings.TrimPrefix(th, "jira:") {
			t.Errorf("%s: %+v (want %s)", th, c, reason)
		}
	}
	for _, th := range []string{"jira:4", "jira:5"} {
		if _, ok := cases[api.ThreadID(th)]; ok {
			t.Errorf("%s is a case", th)
		}
	}
	if c := cases["jira:1"]; c.MessageCount != 2 || c.CanArchive || c.Subject != "ITSD-1: Printer" {
		t.Errorf("jira:1: %+v", c)
	}
	// Archive on jira only marks done.
	a, err := x.svc.Archive(x.ctx, api.BoardArchiveParams{CaseID: cases["jira:1"].ID})
	if err != nil || !a.NoArchive || a.Archived != 0 || a.Case.Visibility != api.BoardDone {
		t.Fatalf("jira archive: %+v %v", a, err)
	}
	// A jira account is triaged only when named.
	x.assistantOn()
	q, err := x.svc.Queue(x.ctx, api.BoardQueueParams{AccountIDs: []api.AccountID{api.AccountID(jacc)}})
	if err != nil || len(q.Items) != 0 {
		t.Fatalf("jira queue unnamed: %+v %v", q, err)
	}
}

// A merge of two threads that are both cases keeps one case (the
// canonical thread's) with the user's state of the other.
func TestBoardThreadMerge(t *testing.T) {
	x := newBoardBox(t)
	// Two threads that a later message joins: b1 answers a1 by References.
	x.put(bmail{folder: x.inbox, thread: "", rfc: "m-a1@x", from: boardAlice, to: []api.Address{boardMe}, subject: "Plan", text: "Plan?"})
	x.drain()
	var first api.BoardCase
	_, cases := x.list()
	for _, c := range cases {
		first = c
	}
	if first.ID == "" {
		t.Fatal("no case")
	}
	st := api.BoardThem
	if _, err := x.svc.SetState(x.ctx, api.BoardSetStateParams{CaseID: first.ID, State: &st}); err != nil {
		t.Fatal(err)
	}
	x.put(bmail{folder: x.inbox, thread: "", rfc: "m-c1@x", from: boardBob, to: []api.Address{boardMe}, subject: "Plan B", at: time.Hour, text: "B"})
	x.drain()
	// The joining message names both.
	x.put(bmail{folder: x.inbox, thread: "", rfc: "m-d1@x", inReplyTo: "m-c1@x", from: boardBob, to: []api.Address{boardMe}, subject: "Re: Plan",
		at: 2 * time.Hour, text: "both"})
	if err := x.b.store.SetMessageBody(x.ctx, mustID(t, x, "m-d1@x"), store.BodyUpdate{Text: "both", State: store.BodyFetched,
		References: []string{"m-a1@x", "m-c1@x"}}); err != nil {
		t.Fatal(err)
	}
	x.drain()
	_, cases = x.list()
	if len(cases) != 1 {
		t.Fatalf("after the merge: %d cases", len(cases))
	}
	for _, c := range cases {
		if c.UserState == nil || *c.UserState != api.BoardThem || c.MessageCount != 3 {
			t.Errorf("merged case: %+v", c)
		}
	}
}

func mustID(t *testing.T, x *boardBox, rfc string) string {
	t.Helper()
	var id string
	if err := x.b.store.DB().QueryRowContext(x.ctx, `SELECT id FROM messages WHERE rfc_message_id = ?`, rfc).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// notify.boardChanged is coalesced: at most one per boardNotifyEvery,
// naming the accounts; any account when one asked for all. The test does
// not race the timer: it starts as if an event had just been sent, so
// that the calls are gathered for the whole interval, and it waits for
// each event rather than for a fixed time.
func TestBoardNotificationsCoalesce(t *testing.T) {
	old := boardNotifyEvery
	boardNotifyEvery = 150 * time.Millisecond
	t.Cleanup(func() { boardNotifyEvery = old })
	x := newBoardBox(t)
	waitEvents := func(n int) []api.BoardChangedNotification {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			x.rec.mu.Lock()
			got := len(x.rec.events)
			x.rec.mu.Unlock()
			if got >= n {
				return x.rec.take()
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("fewer than %d events", n)
		return nil
	}
	time.Sleep(2 * boardNotifyEvery) // whatever the setup sent is out
	x.rec.take()
	x.b.board.mu.Lock()
	x.b.board.sent = time.Now()
	x.b.board.mu.Unlock()
	start := time.Now()
	x.b.notifyBoard(false, "acc_b")
	x.b.notifyBoard(false, "acc_a", "acc_b")
	evs := waitEvents(1)
	first := time.Now()
	if len(evs) != 1 || fmt.Sprint(evs[0].AccountIDs) != "[acc_a acc_b]" || first.Sub(start) < boardNotifyEvery-20*time.Millisecond {
		t.Fatalf("first event after %v: %+v", first.Sub(start), evs)
	}
	// Right after one was sent, the next waits out the interval.
	x.b.notifyBoard(false, "acc_c")
	evs = waitEvents(1)
	if len(evs) != 1 || fmt.Sprint(evs[0].AccountIDs) != "[acc_c]" || time.Since(first) < boardNotifyEvery-20*time.Millisecond {
		t.Fatalf("second event after %v: %+v", time.Since(first), evs)
	}
	x.b.notifyBoard(false, "acc_a")
	x.b.notifyBoard(true)
	evs = waitEvents(1)
	if len(evs) != 1 || evs[0].AccountIDs != nil {
		t.Fatalf("all: %+v", evs)
	}
}

// The worker started by StartSync evaluates new mail by itself, and the
// first evaluation (Maintain's backfill) makes the board ready.
func TestBoardWorkerAndBackfill(t *testing.T) {
	oldTick := boardTick
	boardTick = 20 * time.Millisecond
	t.Cleanup(func() { boardTick = oldTick })
	x := newBoardBox(t)
	x.put(bmail{folder: x.inbox, thread: "t_old", rfc: "o1", from: boardAlice, to: []api.Address{boardMe}, text: "Old"})
	// As a store upgraded to 0017: the rows are there, nothing is dirty.
	if _, err := x.b.store.DB().ExecContext(x.ctx, `DELETE FROM board_dirty`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(x.ctx)
	done := x.b.StartSync(ctx)
	t.Cleanup(func() {
		cancel()
		<-done
	})
	res, _ := x.list()
	if res.Ready || len(res.Cases) != 0 {
		t.Fatalf("before the backfill: %+v", res)
	}
	if err := x.b.backfillBoard(ctx); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := x.b.store.GetMeta(x.ctx, metaBoardRules); v != boardRulesDone() || v != "4:done" {
		t.Fatalf("meta = %q", v)
	}
	waitBoard(t, func() bool { r, c := x.list(); _, ok := c["t_old"]; return ok && r.Ready })
	// New mail: the worker picks it up without being asked.
	x.put(bmail{folder: x.inbox, thread: "t_new", rfc: "n1", from: boardBob, to: []api.Address{boardMe}, text: "New"})
	waitBoard(t, func() bool { _, c := x.list(); _, ok := c["t_new"]; return ok })
	waitBoardEvent(t, x.rec, x.acc)
	// A rules version of another time starts the pass over.
	if err := x.b.store.SetMeta(x.ctx, metaBoardRules, "0:done"); err != nil {
		t.Fatal(err)
	}
	if err := x.b.backfillBoard(ctx); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := x.b.store.GetMeta(x.ctx, metaBoardRules); v != boardRulesDone() {
		t.Fatalf("meta after a new rules version = %q", v)
	}
	waitBoard(t, func() bool { r, _ := x.list(); return r.Ready })
}

func waitBoard(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}
