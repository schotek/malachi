// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	_ "time/tzdata" // Europe/Prague on every platform, as in cmd/malachid

	"github.com/schotek/malachi/backend/internal/caldav/caldavtest"
	"github.com/schotek/malachi/backend/internal/calsync"
	"github.com/schotek/malachi/backend/internal/ingest"
	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

const outlookUID = "040000008200E00074C5B7101A82E00800000000C1D2E3F4A5B6C701000000000000000010000000AABBCCDDEEFF00112233445566778899"

// invRecorder keeps the notifications of an invitation test.
type invRecorder struct {
	mu       sync.Mutex
	news     []api.NewMessageNotification
	invites  []api.InvitationsChangedNotification
	calendar []api.CalendarChangedNotification
}

func (r *invRecorder) NewMessage(n api.NewMessageNotification) {
	r.mu.Lock()
	r.news = append(r.news, n)
	r.mu.Unlock()
}
func (*invRecorder) SyncState(api.SyncStateNotification)             {}
func (*invRecorder) AuthRequired(api.AuthRequiredNotification)       {}
func (*invRecorder) AccountsChanged(api.AccountsChangedNotification) {}
func (*invRecorder) MessagesChanged(api.MessagesChangedNotification) {}
func (r *invRecorder) CalendarChanged(n api.CalendarChangedNotification) {
	r.mu.Lock()
	r.calendar = append(r.calendar, n)
	r.mu.Unlock()
}
func (*invRecorder) CalendarState(api.CalendarStateNotification) {}
func (*invRecorder) EventAlert(api.EventAlertNotification)       {}
func (r *invRecorder) InvitationsChanged(n api.InvitationsChangedNotification) {
	r.mu.Lock()
	r.invites = append(r.invites, n)
	r.mu.Unlock()
}

func (r *invRecorder) invitationsChangedCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.invites)
}

// invBox is the mail account bo@example.org (inbox, junk, trash,
// archive) over fake supervisors, and optionally a CalDAV account over
// caldavtest with one calendar.
type invBox struct {
	t   *testing.T
	ctx context.Context
	b   *Backend
	rec *invRecorder
	out *fakeOutbox

	mail                        string
	inbox, junk, trash, archive store.Folder
	uid                         uint32

	srv    *caldavtest.Server
	calAcc string
	cal    store.Calendar
}

func newInvBox(t *testing.T) *invBox {
	t.Helper()
	was := invitationsChangedDelay
	invitationsChangedDelay = 5 * time.Millisecond
	t.Cleanup(func() { invitationsChangedDelay = was })
	srv := caldavtest.New(t, caldavtest.Nextcloud)
	b, _, out, _ := newCalDAVBackend(t, srv)
	rec := &invRecorder{}
	b.SetNotifier(rec)
	t.Cleanup(b.Close)
	x := &invBox{t: t, ctx: context.Background(), b: b, rec: rec, out: out, srv: srv}
	x.mail = seedAccount(t, b, "bo@example.org")
	f := seedFolders(t, b, x.mail, []store.Folder{
		{Mailbox: "INBOX", Name: "Inbox", Path: "Inbox", Role: api.RoleInbox, Subscribed: true, Selectable: true},
		{Mailbox: "Junk", Name: "Junk", Path: "Junk", Role: api.RoleJunk, Subscribed: true, Selectable: true},
		{Mailbox: "Trash", Name: "Trash", Path: "Trash", Role: api.RoleTrash, Subscribed: true, Selectable: true},
		{Mailbox: "Archive", Name: "Archive", Path: "Archive", Role: api.RoleArchive, Subscribed: true, Selectable: true},
	})
	x.inbox, x.junk, x.trash, x.archive = f["INBOX"], f["Junk"], f["Trash"], f["Archive"]
	return x
}

// addCalDAV adds the CalDAV account and its default calendar.
func (x *invBox) addCalDAV(edit func(*api.AccountConfig)) {
	x.t.Helper()
	cfg := calDAVConfig(x.srv.ServerURL())
	cfg.CalDAV.HomeSetURL = x.srv.HomeSetURL()
	if edit != nil {
		edit(&cfg)
	}
	res, err := x.b.Accounts().Add(x.ctx, api.AccountAddParams{Config: cfg, Credentials: api.Credentials{Password: caldavtest.Password}})
	if err != nil {
		x.t.Fatal(err)
	}
	x.calAcc = string(res.AccountID)
	cals, err := x.b.store.UpsertCalendars(x.ctx, x.calAcc, []store.Calendar{{RemoteID: x.srv.DefaultCalendarHref(),
		Name: "Personal", Access: api.CalendarAccessOwner, Kind: api.CalendarKindEvents, ServerDefault: true}})
	if err != nil {
		x.t.Fatal(err)
	}
	x.cal = cals[0]
}

func readMIME(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "mime", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// store puts raw in folder f as a syncer does (envelope, body with the
// attachments list, raw file), without the Stored hook.
func (x *invBox) store(f store.Folder, raw []byte) store.Message {
	x.t.Helper()
	parsed, err := mime.Parse(bytes.NewReader(raw), mime.DefaultLimits())
	if err != nil {
		x.t.Fatal(err)
	}
	x.uid++
	row := &store.Message{AccountID: f.AccountID, FolderID: f.ID, UID: x.uid, Subject: parsed.Subject, From: parsed.From, To: parsed.To,
		Date: parsed.Date, InternalDate: time.Now(), RFCMessageID: fmt.Sprintf("<inv-%d@example.org>", x.uid), Size: int64(len(raw))}
	if err := x.b.store.UpsertMessages(x.ctx, []*store.Message{row}); err != nil {
		x.t.Fatal(err)
	}
	if err := x.b.store.SetMessageBody(x.ctx, row.ID, store.BodyUpdate{Text: parsed.Text, HasHTML: parsed.HasHTML, Snippet: parsed.Snippet,
		Attachments: parsed.Attachments, HasAttachments: parsed.HasAttachments, Headers: parsed.Headers, State: store.BodyFetched}); err != nil {
		x.t.Fatal(err)
	}
	if _, err := x.b.store.WriteMessageRaw(x.ctx, f.AccountID, row.ID, bytes.NewReader(raw), 25<<20); err != nil {
		x.t.Fatal(err)
	}
	return x.message(row.ID)
}

// receive is store + the Stored hook + the announcement, as a syncer.
func (x *invBox) receive(f store.Folder, raw []byte) store.Message {
	x.t.Helper()
	m := x.store(f, raw)
	x.b.afterMailStored(x.ctx, m.AccountID, m.ID, ingest.Policy{})
	m = x.message(m.ID)
	x.rec.NewMessage(api.NewMessageNotification{AccountID: api.AccountID(m.AccountID), FolderID: api.FolderID(m.FolderID),
		Message: toAPISummary(m)})
	return m
}

func (x *invBox) message(id string) store.Message {
	x.t.Helper()
	m, err := x.b.store.GetMessage(x.ctx, x.mail, id)
	if err != nil {
		x.t.Fatal(err)
	}
	return m
}

func (x *invBox) card(m store.Message) *api.InvitationCard {
	x.t.Helper()
	res, err := x.b.Messages().Get(x.ctx, api.MessageGetParams{AccountID: api.AccountID(x.mail), MessageID: api.MessageID(m.ID)})
	if err != nil {
		x.t.Fatal(err)
	}
	return res.Message.Invitation
}

// putEvent stores one event of the CalDAV calendar with its occurrence.
func (x *invBox) putEvent(remote string, e store.Event) store.Event {
	x.t.Helper()
	e.Kind = store.EventSingle
	rows, err := x.b.store.ReplaceResource(x.ctx, x.cal.ID, remote, []store.Event{e}, func(rows []store.Event) []store.Occurrence {
		return []store.Occurrence{{EventID: rows[0].ID, CalendarID: x.cal.ID, AccountID: x.calAcc, StartAt: e.StartAt, EndAt: e.EndAt}}
	})
	if err != nil {
		x.t.Fatal(err)
	}
	return rows[0]
}

func (x *invBox) queued() []store.OutboxEntry {
	x.t.Helper()
	got, err := x.b.store.ListOutbox(x.ctx, x.mail, store.OutboxQueued, time.Now().Add(time.Hour))
	if err != nil {
		x.t.Fatal(err)
	}
	return got
}

// calendarText is the decoded calendar part of a message.
func calendarText(t *testing.T, raw string) string {
	t.Helper()
	p, err := mime.Parse(strings.NewReader(raw), mime.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range p.Attachments {
		if a.ContentType == "text/calendar" {
			part, err := mime.ExtractPart(strings.NewReader(raw), a.PartID, mime.DefaultLimits(), api.MaxICSBytes)
			if err != nil {
				t.Fatal(err)
			}
			return strings.ReplaceAll(string(part.Body), "\r\n ", "")
		}
	}
	t.Fatal("no calendar part")
	return ""
}

func (x *invBox) raw(accountID, id string) string {
	x.t.Helper()
	f, err := x.b.store.OpenMessageRaw(x.ctx, accountID, id)
	if err != nil {
		x.t.Fatal(err)
	}
	defer f.Close()
	data, _ := io.ReadAll(f)
	return string(data)
}

// Every invite-*.eml: the pill of message.list and of notify.newMessage,
// the forwarded invitation none, two parts the first.
func TestInvitationDetectionOfTheCorpus(t *testing.T) {
	x := newInvBox(t)
	cases := []struct {
		file  string
		kind  api.InvitationKind // "" = no invitation
		state api.InvitationState
		title string
	}{
		{"invite-outlook.eml", api.InvitationKindInvitation, api.InvitationUnanswered, "Quarterly review"},
		{"invite-update.eml", api.InvitationKindUpdate, api.InvitationUnanswered, "Quarterly review"},
		{"invite-google.eml", api.InvitationKindInvitation, api.InvitationUnanswered, "Weekly sync"},
		{"invite-calendar-as-attachment.eml", api.InvitationKindInvitation, api.InvitationUnanswered, ""},
		{"invite-two-calendar-parts.eml", api.InvitationKindInvitation, api.InvitationUnanswered, ""},
		{"invite-reply.eml", api.InvitationKindReply, api.InvitationProcessed, ""},
		{"invite-reply-from-mismatch.eml", api.InvitationKindReply, api.InvitationProcessed, ""},
		{"invite-cancel.eml", api.InvitationKindCancelled, api.InvitationCancelled, ""},
		{"invite-in-forwarded-message.eml", "", "", ""},
	}
	ids := map[string]string{}
	for _, c := range cases {
		m := x.receive(x.inbox, readMIME(t, c.file))
		ids[c.file] = m.ID
		got := m.Invitation
		if c.kind == "" {
			if got != nil {
				t.Errorf("%s: invitation %+v", c.file, got)
			}
			continue
		}
		if got == nil {
			t.Fatalf("%s: no invitation", c.file)
		}
		if got.Kind != c.kind {
			t.Errorf("%s: kind %q, want %q", c.file, got.Kind, c.kind)
		}
		// The update and the cancel make the first invitation outdated
		// and cancelled; the state is checked when it arrives.
		if got.State != c.state {
			t.Errorf("%s: state %q, want %q", c.file, got.State, c.state)
		}
		if c.title != "" && !strings.Contains(got.Title, c.title) {
			t.Errorf("%s: title %q", c.file, got.Title)
		}
		x.rec.mu.Lock()
		last := x.rec.news[len(x.rec.news)-1]
		x.rec.mu.Unlock()
		if last.Message.Invitation == nil || last.Message.Invitation.Kind != c.kind {
			t.Errorf("%s: notify.newMessage carries %+v", c.file, last.Message.Invitation)
		}
	}
	two, err := x.b.store.GetInvitation(x.ctx, ids["invite-two-calendar-parts.eml"])
	if err != nil || two.UID != "first@example.org" {
		t.Errorf("two parts: %+v %v", two, err)
	}
	// After the cancel the first invitation and its update are cancelled.
	first := x.message(ids["invite-outlook.eml"])
	if first.Invitation.State != api.InvitationCancelled {
		t.Errorf("outlook after cancel: %q", first.Invitation.State)
	}
	// message.list carries the summaries.
	list, err := x.b.Messages().List(x.ctx, api.MessageListParams{AccountID: api.AccountID(x.mail), FolderID: api.FolderID(x.inbox.ID)})
	if err != nil {
		t.Fatal(err)
	}
	with := 0
	for _, s := range list.Messages {
		if s.Invitation != nil {
			with++
		}
	}
	if with != len(cases)-1 {
		t.Errorf("message.list: %d summaries with an invitation, want %d", with, len(cases)-1)
	}
	waitFor(t, "notify.invitationsChanged", func() bool { return x.rec.invitationsChangedCount() > 0 })
	// The pill and the board: the rows marked the threads dirty.
	if n, err := x.b.store.CountBoardDirty(x.ctx); err != nil || n == 0 {
		t.Errorf("board_dirty %d %v", n, err)
	}
}

// The board is told about every row written (no trigger joins
// cal_invitations to it).
func TestInvitationMarksTheBoardDirty(t *testing.T) {
	x := newInvBox(t)
	m := x.store(x.inbox, readMIME(t, "invite-outlook.eml"))
	if _, err := x.b.store.DB().ExecContext(x.ctx, `DELETE FROM board_dirty`); err != nil {
		t.Fatal(err)
	}
	x.b.afterMailStored(x.ctx, x.mail, m.ID, ingest.Policy{})
	if n, err := x.b.store.CountBoardDirty(x.ctx); err != nil || n != 1 {
		t.Errorf("board_dirty %d %v", n, err)
	}
}

// The card of message.get: static fields, conflicts and the day strip
// from a stored occurrence, targets, the reply path.
//
// The day strip is the working hours of the daemon's zone, here Prague
// (the invitation starts at 10:00 there, two hours into the
// strip); on a machine in UTC, the hosted Windows runner's, the strip would
// start at 08:00 UTC, the invitation's own start.
func TestInvitationCard(t *testing.T) {
	prague, zerr := time.LoadLocation("Europe/Prague")
	if zerr != nil {
		t.Fatal(zerr)
	}
	was := time.Local
	time.Local = prague
	t.Cleanup(func() { time.Local = was })
	x := newInvBox(t)
	x.addCalDAV(nil)
	start := time.Date(2026, 10, 22, 8, 30, 0, 0, time.UTC)
	x.putEvent("/standup.ics", store.Event{UID: "standup@example.test", Title: "Standup", StartAt: start, EndAt: start.Add(time.Hour),
		Status: api.EventStatusConfirmed, ShowAs: api.ShowAsBusy})
	m := x.receive(x.inbox, readMIME(t, "invite-outlook.eml"))
	c := x.card(m)
	if c == nil {
		t.Fatal("no card")
	}
	if c.UID != outlookUID || c.PartID == "" || c.Location != "Meeting Room 2.04" || c.AttendeeCount != 2 {
		t.Errorf("static: %+v", c)
	}
	if c.Me == nil || c.Me.Address != "bo@example.org" || c.Me.Response != api.ResponseNeedsAction {
		t.Errorf("me: %+v", c.Me)
	}
	if c.VideoCall == nil || c.VideoCall.Provider != api.VideoProviderTeams {
		t.Errorf("video call: %+v", c.VideoCall)
	}
	if len(c.Conflicts) != 1 || c.Conflicts[0].Title != "Standup" || c.Conflicts[0].CalendarID != api.CalendarID(x.cal.ID) {
		t.Errorf("conflicts: %+v", c.Conflicts)
	}
	if len(c.Day.Busy) != 1 || !c.Day.From.Before(*c.Start) || !c.Day.To.After(*c.End) {
		t.Errorf("day: %+v", c.Day)
	}
	if len(c.Targets) != 1 || c.Targets[0].CalendarID != api.CalendarID(x.cal.ID) || !c.Targets[0].IsDefault || c.FixedTarget {
		t.Errorf("targets: %+v fixed %v", c.Targets, c.FixedTarget)
	}
	if !c.CanRespond || c.CanProposeNewTime || c.ReplyVia != api.InvitationsViaMail || c.ReplyAccountID != api.AccountID(x.mail) ||
		c.ReplyTo == nil || c.ReplyTo.Address != "ada@example.org" || c.OrganizerMismatch {
		t.Errorf("reply path: %+v", c)
	}
	// The same message in junk: a card, no answer.
	j := x.receive(x.junk, readMIME(t, "invite-google.eml"))
	if jc := x.card(j); jc == nil || jc.CanRespond {
		t.Errorf("junk card: %+v", jc)
	}
	_, err := x.b.Invitations().Respond(x.ctx, api.InvitationRespondParams{AccountID: api.AccountID(x.mail),
		MessageID: api.MessageID(j.ID), Response: api.ResponseAccepted})
	if errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("respond in junk: %v", err)
	}
	// invitation.list: one per UID, junk left out.
	res, err := x.b.Invitations().List(x.ctx, api.InvitationListParams{State: api.InvitationUnanswered})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Invitations) != 1 || res.Invitations[0].MessageID != api.MessageID(m.ID) {
		t.Errorf("list: %+v", res.Invitations)
	}
}

// A part over api.MaxICSBytes is an invalid row: the pill says invalid,
// the card has nothing to answer.
func TestInvitationOversizedPartIsInvalid(t *testing.T) {
	x := newInvBox(t)
	body := "BEGIN:VCALENDAR\r\nMETHOD:REQUEST\r\nX-PAD:" + strings.Repeat("a", api.MaxICSBytes) + "\r\nEND:VCALENDAR\r\n"
	raw := "From: Ada <ada@example.org>\r\nTo: bo@example.org\r\nSubject: Huge\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=b\r\n\r\n--b\r\nContent-Type: text/plain\r\n\r\nhi\r\n" +
		"--b\r\nContent-Type: text/calendar; method=REQUEST\r\n\r\n" + body + "\r\n--b--\r\n"
	m := x.receive(x.inbox, []byte(raw))
	if m.Invitation == nil || m.Invitation.State != api.InvitationInvalid {
		t.Fatalf("summary %+v", m.Invitation)
	}
	if c := x.card(m); c == nil || c.State != api.InvitationInvalid || c.CanRespond {
		t.Errorf("card %+v", c)
	}
	_, err := x.b.Invitations().Respond(x.ctx, api.InvitationRespondParams{AccountID: api.AccountID(x.mail),
		MessageID: api.MessageID(m.ID), Response: api.ResponseAccepted})
	if errCode(t, err) != api.CodeMalformedCalendar {
		t.Errorf("respond: %v", err)
	}
}

// An answer by mail goes to the stored ORGANIZER, never to the From; with
// a CalDAV calendar the organiser's object is stored with the user's
// PARTSTAT and SCHEDULE-AGENT=CLIENT; afterAnswer archive moves the
// message; a later CANCEL marks the copy and applyCancel removes it.
func TestInvitationRespondByMailWithCalDAVCopy(t *testing.T) {
	x := newInvBox(t)
	x.addCalDAV(func(c *api.AccountConfig) {
		c.Calendar = &api.CalendarConfig{Sync: true, AfterAnswer: api.AfterAnswerArchive}
	})
	raw := strings.Replace(string(readMIME(t, "invite-outlook.eml")), "From: Ada Example <ada@example.org>", "From: Mallory <mallory@example.net>", 1)
	m := x.receive(x.inbox, []byte(raw))
	if c := x.card(m); c == nil || !c.OrganizerMismatch || c.ReplyTo.Address != "ada@example.org" {
		t.Fatalf("card %+v", c)
	}
	res, err := x.b.Invitations().Respond(x.ctx, api.InvitationRespondParams{AccountID: api.AccountID(x.mail),
		MessageID: api.MessageID(m.ID), Response: api.ResponseAccepted, CalendarID: api.CalendarID(x.cal.ID),
		Mail: &api.MailText{Subject: "Přijato: Quarterly review", Text: "Bo accepted."}})
	if err != nil {
		t.Fatal(err)
	}
	q := x.queued()
	if len(q) != 1 || len(q[0].Recipients) != 1 || q[0].Recipients[0] != "ada@example.org" {
		t.Fatalf("outbox %+v", q)
	}
	reply := x.raw(x.mail, q[0].MessageID)
	reply += calendarText(t, reply)
	for _, want := range []string{"METHOD:REPLY", "PARTSTAT=ACCEPTED", "mailto:bo@example.org", "Bo accepted.", "In-Reply-To: <inv-1@example.org>"} {
		if !strings.Contains(reply, want) {
			t.Errorf("reply lacks %q:\n%s", want, reply)
		}
	}
	if strings.Contains(reply, "mallory@example.net") {
		t.Error("the reply names the From")
	}
	if res.MovedTo != api.FolderID(x.archive.ID) || x.message(m.ID).FolderID != x.archive.ID {
		t.Errorf("moved to %q", res.MovedTo)
	}
	if res.EventID == "" || res.Invitation.Response != api.ResponseAccepted || res.Invitation.State != api.InvitationAnswered ||
		res.Invitation.EventID != res.EventID {
		t.Errorf("result %+v", res)
	}
	hrefs := x.srv.Objects(x.srv.DefaultCalendarHref())
	if len(hrefs) != 1 {
		t.Fatalf("server objects %v", hrefs)
	}
	data, _, _ := x.srv.Object(hrefs[0])
	obj := strings.ReplaceAll(string(data), "\r\n ", "")
	if !strings.Contains(obj, "SCHEDULE-AGENT=CLIENT") || !strings.Contains(obj, "PARTSTAT=ACCEPTED") || !strings.Contains(obj, outlookUID) {
		t.Errorf("stored object:\n%s", obj)
	}
	ev, err := x.b.store.GetEvent(x.ctx, string(res.EventID))
	if err != nil || ev.MyResponse != api.ResponseAccepted {
		t.Fatalf("stored copy %+v %v", ev, err)
	}
	// The cancel marks the copy (the daemon stored it: no server cancels
	// it), and applyCancel removes it.
	cm := x.receive(x.inbox, readMIME(t, "invite-cancel.eml"))
	if ev, err := x.b.store.GetEvent(x.ctx, string(res.EventID)); err != nil || ev.Status != api.EventStatusCancelled {
		t.Errorf("copy after cancel %+v %v", ev.Status, err)
	}
	ac, err := x.b.Invitations().ApplyCancel(x.ctx, api.InvitationApplyCancelParams{AccountID: api.AccountID(x.mail), MessageID: api.MessageID(cm.ID)})
	if err != nil || !ac.Removed {
		t.Fatalf("applyCancel %+v %v", ac, err)
	}
	if left := x.srv.Objects(x.srv.DefaultCalendarHref()); len(left) != 0 {
		t.Errorf("server objects after applyCancel %v", left)
	}
	if len(x.queued()) != 1 {
		t.Error("applyCancel sent something")
	}
	ac, err = x.b.Invitations().ApplyCancel(x.ctx, api.InvitationApplyCancelParams{AccountID: api.AccountID(x.mail), MessageID: api.MessageID(cm.ID)})
	if err != nil || ac.Removed {
		t.Errorf("second applyCancel %+v %v", ac, err)
	}
	_, err = x.b.Invitations().ApplyCancel(x.ctx, api.InvitationApplyCancelParams{AccountID: api.AccountID(x.mail), MessageID: api.MessageID(m.ID)})
	if errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("applyCancel of a request: %v", err)
	}
}

// Decline Without Reply sends nothing; a decline removes the copy.
func TestInvitationDeclineWithoutReply(t *testing.T) {
	x := newInvBox(t)
	x.addCalDAV(nil)
	m := x.receive(x.inbox, readMIME(t, "invite-outlook.eml"))
	if _, err := x.b.Invitations().Respond(x.ctx, api.InvitationRespondParams{AccountID: api.AccountID(x.mail),
		MessageID: api.MessageID(m.ID), Response: api.ResponseTentative, CalendarID: api.CalendarID(x.cal.ID),
		SendResponse: api.Ptr(false)}); err != nil {
		t.Fatal(err)
	}
	if len(x.queued()) != 0 || len(x.srv.Objects(x.srv.DefaultCalendarHref())) != 1 {
		t.Fatalf("outbox %d objects %v", len(x.queued()), x.srv.Objects(x.srv.DefaultCalendarHref()))
	}
	res, err := x.b.Invitations().Respond(x.ctx, api.InvitationRespondParams{AccountID: api.AccountID(x.mail),
		MessageID: api.MessageID(m.ID), Response: api.ResponseDeclined, SendResponse: api.Ptr(false)})
	if err != nil {
		t.Fatal(err)
	}
	if len(x.queued()) != 0 || len(x.srv.Objects(x.srv.DefaultCalendarHref())) != 0 || res.Invitation.Response != api.ResponseDeclined {
		t.Errorf("after decline: outbox %d objects %v %+v", len(x.queued()), x.srv.Objects(x.srv.DefaultCalendarHref()), res.Invitation)
	}
	_, err = x.b.Invitations().Respond(x.ctx, api.InvitationRespondParams{AccountID: api.AccountID(x.mail),
		MessageID: api.MessageID(m.ID), Response: api.ResponseAccepted, CalendarID: "cal_nope"})
	if errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("a calendar not in targets: %v", err)
	}
}

// invProvider is a calendar source that answers Respond with the stored
// object and the new response.
type invProvider struct {
	calsync.Provider
	st       *store.Store
	mu       sync.Mutex
	responds []calsync.Response
	opts     []calsync.WriteOptions
}

func (p *invProvider) Features() api.CalendarFeatures {
	return api.CalendarFeatures{Invitations: api.InvitationsViaServer}
}

func (p *invProvider) Respond(ctx context.Context, cal calsync.StoredCalendar, ev calsync.StoredEvent, r calsync.Response, opts calsync.WriteOptions) (calsync.WriteResult, error) {
	p.mu.Lock()
	p.responds, p.opts = append(p.responds, r), append(p.opts, opts)
	p.mu.Unlock()
	rows, err := p.st.EventsByRemoteID(ctx, cal.ID, ev.RemoteID)
	if err != nil {
		return calsync.WriteResult{}, err
	}
	obj := calsync.ObjectOf(rows)
	obj.Master.MyResponse = r.Response
	obj.ETag = "e2"
	return calsync.WriteResult{Objects: []calsync.Object{obj}, Invitations: api.InvitationsSent{Via: api.InvitationsViaServer, Count: 1}}, nil
}

// An invitation whose UID is a stored event of its organiser is answered
// through that event (D18): no mail of the daemon, the target fixed.
func TestInvitationRespondThroughLinkedEvent(t *testing.T) {
	x := newInvBox(t)
	x.addCalDAV(nil)
	prov := &invProvider{st: x.b.store}
	invitationProviders = func(context.Context, store.Account) (calsync.Provider, error) { return prov, nil }
	t.Cleanup(func() { invitationProviders = nil })
	start := time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
	ev := x.putEvent("/q4.ics", store.Event{UID: outlookUID, Title: "Quarterly review", StartAt: start, EndAt: start.Add(time.Hour),
		ETag: "e1", Status: api.EventStatusConfirmed, ShowAs: api.ShowAsBusy, Organizer: &api.Address{Address: "ada@example.org"},
		MyResponse: api.ResponseNeedsAction, AttendeeCount: 1,
		Attendees: []api.Attendee{{Address: "bo@example.org", Response: api.ResponseNeedsAction, Me: true}}})
	m := x.receive(x.inbox, readMIME(t, "invite-outlook.eml"))
	c := x.card(m)
	if c.EventID != api.EventID(ev.ID) || !c.FixedTarget || len(c.Targets) != 1 || c.ReplyVia != api.InvitationsViaServer || len(c.Conflicts) != 0 {
		t.Fatalf("card %+v", c)
	}
	res, err := x.b.Invitations().Respond(x.ctx, api.InvitationRespondParams{AccountID: api.AccountID(x.mail),
		MessageID: api.MessageID(m.ID), Response: api.ResponseAccepted, Comment: "ok"})
	if err != nil {
		t.Fatal(err)
	}
	if len(prov.responds) != 1 || prov.responds[0].Scope != api.EditScopeAll || !prov.opts[0].Notify || prov.opts[0].IfMatch != "e1" ||
		prov.opts[0].MailAccountID != api.AccountID(x.mail) {
		t.Errorf("provider got %+v %+v", prov.responds, prov.opts)
	}
	if len(x.queued()) != 0 {
		t.Error("the daemon queued a reply")
	}
	got, _ := x.b.store.GetEvent(x.ctx, ev.ID)
	if got.MyResponse != api.ResponseAccepted || got.ETag != "e2" || res.EventID != api.EventID(ev.ID) {
		t.Errorf("stored %+v / %+v", got, res)
	}
	r, err := x.b.store.GetResponse(x.ctx, x.mail, outlookUID, "")
	if err != nil || r.Via != "caldav" || r.Response != api.ResponseAccepted {
		t.Errorf("response %+v %v", r, err)
	}
}

// D10: a REPLY whose From is not its attendee changes nothing; the
// attendee's own reply updates the stored attendee (never adds one).
func TestInvitationReplyApplied(t *testing.T) {
	x := newInvBox(t)
	x.addCalDAV(nil)
	start := time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
	ev := x.putEvent("/q4.ics", store.Event{UID: outlookUID, Title: "Quarterly review", StartAt: start, EndAt: start.Add(time.Hour),
		Status: api.EventStatusConfirmed, ShowAs: api.ShowAsBusy, Organizer: &api.Address{Address: "jana@example.test"}, IAmOrganizer: true,
		AttendeeCount: 2, Attendees: []api.Attendee{{Address: "bo@example.org", Response: api.ResponseNeedsAction},
			{Address: "cy@example.org", Response: api.ResponseNeedsAction}}})
	spoof := x.receive(x.inbox, readMIME(t, "invite-reply-from-mismatch.eml"))
	got, _ := x.b.store.GetEvent(x.ctx, ev.ID)
	if got.Attendees[0].Response != api.ResponseNeedsAction {
		t.Errorf("spoofed reply applied: %+v", got.Attendees)
	}
	if c := x.card(spoof); c == nil || c.ReplyApplied || c.CanRespond {
		t.Errorf("spoofed card %+v", c)
	}
	real := x.receive(x.inbox, readMIME(t, "invite-reply.eml"))
	got, _ = x.b.store.GetEvent(x.ctx, ev.ID)
	if got.Attendees[0].Response != api.ResponseAccepted || len(got.Attendees) != 2 || got.Attendees[1].Response != api.ResponseNeedsAction {
		t.Errorf("reply not applied: %+v", got.Attendees)
	}
	if c := x.card(real); c == nil || !c.ReplyApplied || c.ReplyFrom == nil || c.ReplyFrom.Address != "bo@example.org" {
		t.Errorf("reply card %+v", c)
	}
	x.rec.mu.Lock()
	n := len(x.rec.calendar)
	x.rec.mu.Unlock()
	if n == 0 {
		t.Error("no notify.calendarChanged")
	}
	// A reply from an attendee the event does not have adds nobody.
	other := strings.Replace(string(readMIME(t, "invite-reply.eml")), "From: Bo Tester <bo@example.org>", "From: Dee <dee@example.org>", 1)
	other = strings.Replace(other, "Subject:", "X-Note: x\r\nSubject:", 1)
	x.receive(x.inbox, []byte(other))
	got, _ = x.b.store.GetEvent(x.ctx, ev.ID)
	if len(got.Attendees) != 2 {
		t.Errorf("attendees %+v", got.Attendees)
	}
}

// A calendar in disabledCalendars (sync_mode disabled) is no target, and
// an event in it is no linked event.
func TestInvitationSkipsDisabledCalendars(t *testing.T) {
	x := newInvBox(t)
	x.addCalDAV(nil)
	start := time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
	x.putEvent("/q4.ics", store.Event{UID: outlookUID, Title: "Quarterly review", StartAt: start, EndAt: start.Add(time.Hour),
		Status: api.EventStatusConfirmed, ShowAs: api.ShowAsBusy, Organizer: &api.Address{Address: "ada@example.org"},
		Attendees: []api.Attendee{{Address: "bo@example.org", Response: api.ResponseNeedsAction, Me: true}}})
	m := x.receive(x.inbox, readMIME(t, "invite-outlook.eml"))
	if c := x.card(m); c.EventID == "" || len(c.Targets) != 1 {
		t.Fatalf("before: %+v", c)
	}
	if err := x.b.store.SetCalendarSyncState(x.ctx, x.cal.ID, store.CalendarSyncState{SyncMode: calsync.SyncModeDisabled}); err != nil {
		t.Fatal(err)
	}
	c := x.card(m)
	if c.EventID != "" || c.FixedTarget || len(c.Targets) != 0 {
		t.Errorf("disabled calendar still used: %+v", c)
	}
	_, err := x.b.Invitations().Respond(x.ctx, api.InvitationRespondParams{AccountID: api.AccountID(x.mail),
		MessageID: api.MessageID(m.ID), Response: api.ResponseAccepted, CalendarID: api.CalendarID(x.cal.ID)})
	if errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("respond into a disabled calendar: %v", err)
	}
}

// linkedQ4 stores the Exchange-made event of invite-outlook.eml in the
// CalDAV calendar: organiser ada, the user an attendee with response r.
func (x *invBox) linkedQ4(r api.EventResponse, edit func(*store.Event)) store.Event {
	start := time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
	e := store.Event{UID: outlookUID, Title: "Quarterly review", StartAt: start, EndAt: start.Add(time.Hour),
		Location: "Meeting Room 2.04", Status: api.EventStatusConfirmed, ShowAs: api.ShowAsBusy,
		Organizer: &api.Address{Address: "ada@example.org"}, MyResponse: r, AttendeeCount: 1,
		Attendees: []api.Attendee{{Address: "bo@example.org", Response: r, Me: true}}}
	if edit != nil {
		edit(&e)
	}
	return x.putEvent("/q4.ics", e)
}

// An answer given in another client (the linked event's response) counts:
// answered in the summary, out of the unanswered list, the card's
// response without respondedAt; needsAction stays unanswered; a local
// answer wins.
func TestInvitationAnsweredElsewhere(t *testing.T) {
	for _, r := range []api.EventResponse{api.ResponseAccepted, api.ResponseTentative} {
		x := newInvBox(t)
		x.addCalDAV(nil)
		ev := x.linkedQ4(r, nil)
		m := x.receive(x.inbox, readMIME(t, "invite-outlook.eml"))
		if m.Invitation == nil || m.Invitation.State != api.InvitationAnswered {
			t.Fatalf("%s: summary %+v", r, m.Invitation)
		}
		c := x.card(m)
		if c.State != api.InvitationAnswered || c.Response != r || c.RespondedAt != nil || c.EventID != api.EventID(ev.ID) ||
			c.CalendarID != api.CalendarID(x.cal.ID) || !c.CanRespond {
			t.Errorf("%s: card %+v", r, c)
		}
		list, err := x.b.Invitations().List(x.ctx, api.InvitationListParams{State: api.InvitationUnanswered})
		if err != nil || len(list.Invitations) != 0 {
			t.Errorf("%s: unanswered list %+v %v", r, list, err)
		}
		all, _ := x.b.Invitations().List(x.ctx, api.InvitationListParams{State: api.InvitationAnswered})
		if len(all.Invitations) != 1 {
			t.Errorf("%s: answered list %+v", r, all)
		}
	}
	x := newInvBox(t)
	x.addCalDAV(nil)
	x.linkedQ4(api.ResponseNeedsAction, nil)
	m := x.receive(x.inbox, readMIME(t, "invite-outlook.eml"))
	if m.Invitation.State != api.InvitationUnanswered {
		t.Errorf("needsAction: %q", m.Invitation.State)
	}
	if c := x.card(m); c.Response != "" {
		t.Errorf("needsAction card response %q", c.Response)
	}
	// A local answer wins over the event's.
	if err := x.b.store.PutResponse(x.ctx, store.Response{AccountID: x.mail, UID: outlookUID, Response: api.ResponseDeclined, Via: "imip"}); err != nil {
		t.Fatal(err)
	}
	if _, err := x.b.store.DB().ExecContext(x.ctx, `UPDATE cal_events SET my_response = 'accepted' WHERE uid = ?`, outlookUID); err != nil {
		t.Fatal(err)
	}
	c := x.card(x.message(m.ID))
	if c.State != api.InvitationAnswered || c.Response != api.ResponseDeclined || c.RespondedAt == nil {
		t.Errorf("local wins: %+v", c)
	}
}

// previous: a room change only, a series extended, time and room together.
func TestInvitationPrevious(t *testing.T) {
	x := newInvBox(t)
	x.addCalDAV(nil)
	x.linkedQ4(api.ResponseAccepted, func(e *store.Event) { e.Location = "Room 1" })
	m := x.receive(x.inbox, readMIME(t, "invite-outlook.eml"))
	if p := x.card(m).Previous; p == nil || p.Location != "Room 1" || p.Start != "" || p.End != "" || p.Recurrence != nil {
		t.Errorf("room change: %+v", p)
	}

	x = newInvBox(t)
	x.addCalDAV(nil)
	x.linkedQ4(api.ResponseAccepted, func(e *store.Event) { e.Location, e.StartAt = "Room 1", e.StartAt.Add(-time.Hour) })
	m = x.receive(x.inbox, readMIME(t, "invite-outlook.eml"))
	if p := x.card(m).Previous; p == nil || p.Location != "Room 1" || p.Start != "2026-10-22T07:00:00Z" || p.End != "2026-10-22T09:00:00Z" {
		t.Errorf("time and room: %+v", p)
	}

	x = newInvBox(t)
	x.addCalDAV(nil)
	g := x.receive(x.junk, readMIME(t, "invite-google.eml")) // only to read the invitation's rule
	cur := x.card(g)
	if cur.Recurrence == nil || cur.Start == nil {
		t.Fatalf("google card %+v", cur)
	}
	if _, err := x.b.store.DB().ExecContext(x.ctx, `DELETE FROM messages WHERE id = ?`, g.ID); err != nil {
		t.Fatal(err)
	}
	old := *cur.Recurrence
	old.Until = "2026-11-27"
	x.putEvent("/sync.ics", store.Event{UID: cur.UID, Title: "Weekly sync", StartAt: *cur.Start, EndAt: *cur.End,
		Location: cur.Location, Status: api.EventStatusConfirmed, ShowAs: api.ShowAsBusy,
		Organizer: &api.Address{Address: "ada@example.org"}, Recurrence: &old, MyResponse: api.ResponseAccepted})
	m = x.receive(x.inbox, readMIME(t, "invite-google.eml"))
	p := x.card(m).Previous
	if p == nil || p.Recurrence == nil || p.Recurrence.Until != "2026-11-27" || p.Start != "" || p.Location != "" {
		t.Errorf("series extended: %+v", p)
	}
	// The same rule (another pointer) reports nothing.
	same := *cur.Recurrence
	x.putEvent("/sync.ics", store.Event{UID: cur.UID, Title: "Weekly sync", StartAt: *cur.Start, EndAt: *cur.End,
		Location: cur.Location, Status: api.EventStatusConfirmed, ShowAs: api.ShowAsBusy,
		Organizer: &api.Address{Address: "ada@example.org"}, Recurrence: &same, MyResponse: api.ResponseAccepted})
	if p := x.card(m).Previous; p != nil {
		t.Errorf("same rule: %+v", p)
	}
}

// A reply card's response is the attendee's answer (with replyFrom), not
// the user's; a request's card keeps the user's.
func TestInvitationReplyCardResponse(t *testing.T) {
	for _, ps := range []struct {
		partstat string
		want     api.EventResponse
	}{{"DECLINED", api.ResponseDeclined}, {"ACCEPTED", api.ResponseAccepted}, {"TENTATIVE", api.ResponseTentative}} {
		x := newInvBox(t)
		x.addCalDAV(nil)
		start := time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
		x.putEvent("/q4.ics", store.Event{UID: outlookUID, Title: "Quarterly review", StartAt: start, EndAt: start.Add(time.Hour),
			Status: api.EventStatusConfirmed, ShowAs: api.ShowAsBusy, Organizer: &api.Address{Address: "jana@example.test"}, IAmOrganizer: true,
			AttendeeCount: 1, Attendees: []api.Attendee{{Address: "bo@example.org", Response: api.ResponseNeedsAction}}})
		raw := "From: Bo Tester <bo@example.org>\r\nTo: jana@example.test\r\nSubject: Reply\r\nMIME-Version: 1.0\r\n" +
			"Content-Type: text/calendar; charset=utf-8; method=REPLY\r\n\r\n" +
			"BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nMETHOD:REPLY\r\nBEGIN:VEVENT\r\nUID:" + outlookUID + "\r\n" +
			"DTSTAMP:20261016T090000Z\r\nDTSTART:20261022T080000Z\r\nDTEND:20261022T090000Z\r\nSEQUENCE:0\r\n" +
			"ORGANIZER:mailto:jana@example.test\r\nATTENDEE;CN=Bo Tester;PARTSTAT=" + ps.partstat + ":mailto:bo@example.org\r\n" +
			"END:VEVENT\r\nEND:VCALENDAR\r\n"
		m := x.receive(x.inbox, []byte(raw))
		c := x.card(m)
		if c == nil || c.Response != ps.want || c.RespondedAt != nil || c.ReplyFrom == nil || c.ReplyFrom.Address != "bo@example.org" || !c.ReplyApplied {
			t.Errorf("%s: card %+v", ps.partstat, c)
		}
	}
	x := newInvBox(t)
	m := x.receive(x.inbox, readMIME(t, "invite-outlook.eml"))
	if c := x.card(m); c.Response != "" || c.ReplyFrom != nil {
		t.Errorf("request card %+v", c)
	}
}

// ownInvite is a REQUEST organised by the account's own address.
func ownInvite(method, uid string, seq int) []byte {
	return []byte("From: Bo Tester <bo@example.org>\r\nTo: cy@example.org\r\nSubject: Planning\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: text/calendar; charset=utf-8; method=" + method + "\r\n\r\n" +
		"BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nMETHOD:" + method + "\r\nBEGIN:VEVENT\r\nUID:" + uid + "\r\n" +
		"DTSTAMP:20261016T090000Z\r\nDTSTART:20261023T080000Z\r\nDTEND:20261023T090000Z\r\nSEQUENCE:" + fmt.Sprint(seq) + "\r\n" +
		"SUMMARY:Planning\r\nORGANIZER;CN=Bo Tester:mailto:bo@example.org\r\n" +
		"ATTENDEE;PARTSTAT=NEEDS-ACTION;RSVP=TRUE:mailto:cy@example.org\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
}

// The user's own invitation (organiser one of the user's addresses) is
// processed: out of the list and the count, nothing to answer, linked to
// the user's event for Show in Calendar; its CANCEL is still cancelled.
func TestInvitationOwnEvent(t *testing.T) {
	x := newInvBox(t)
	x.addCalDAV(nil)
	start := time.Date(2026, 10, 23, 8, 0, 0, 0, time.UTC)
	ev := x.putEvent("/plan.ics", store.Event{UID: "plan@example.org", Title: "Planning", StartAt: start, EndAt: start.Add(time.Hour),
		Status: api.EventStatusConfirmed, ShowAs: api.ShowAsBusy, Organizer: &api.Address{Address: "bo@example.org"}, IAmOrganizer: true,
		AttendeeCount: 1, Attendees: []api.Attendee{{Address: "cy@example.org", Response: api.ResponseNeedsAction}}})
	m := x.receive(x.inbox, ownInvite("REQUEST", "plan@example.org", 0))
	sent := x.receive(x.trash, ownInvite("REQUEST", "plan@example.org", 0)) // any folder: the rule is the organiser
	for _, got := range []store.Message{m, sent} {
		if got.Invitation == nil || !got.Invitation.OwnEvent || got.Invitation.State != api.InvitationProcessed ||
			got.Invitation.Kind != api.InvitationKindInvitation {
			t.Errorf("summary %+v", got.Invitation)
		}
	}
	c := x.card(m)
	if c.CanRespond || c.CanProposeNewTime || len(c.Targets) != 0 || c.ReplyVia != api.InvitationsViaNone ||
		c.EventID != api.EventID(ev.ID) || c.CalendarID != api.CalendarID(x.cal.ID) || !c.OwnEvent {
		t.Errorf("card %+v", c)
	}
	// Someone else's invitation next to it stays unanswered.
	other := x.receive(x.inbox, readMIME(t, "invite-outlook.eml"))
	if other.Invitation.OwnEvent || other.Invitation.State != api.InvitationUnanswered {
		t.Errorf("other %+v", other.Invitation)
	}
	list, err := x.b.Invitations().List(x.ctx, api.InvitationListParams{State: api.InvitationUnanswered})
	if err != nil || len(list.Invitations) != 1 || list.Invitations[0].MessageID != api.MessageID(other.ID) {
		t.Errorf("list %+v %v", list, err)
	}
	if res, err := x.b.Calendars().List(x.ctx, api.CalendarListParams{}); err != nil || res.Unanswered != 1 {
		t.Errorf("calendar.list unanswered %+v %v", res, err)
	}
	cancel := x.receive(x.inbox, ownInvite("CANCEL", "plan@example.org", 1))
	if cancel.Invitation.State != api.InvitationCancelled || cancel.Invitation.Kind != api.InvitationKindCancelled {
		t.Errorf("own cancel %+v", cancel.Invitation)
	}
}

// instanceInvite is a REQUEST for one instance (RECURRENCE-ID) of the
// weekly series weekly@example.org organised by ada.
func instanceInvite(rid string) []byte {
	return []byte("From: Ada Example <ada@example.org>\r\nTo: bo@example.org\r\nSubject: Weekly status\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: text/calendar; charset=utf-8; method=REQUEST\r\n\r\n" +
		"BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nMETHOD:REQUEST\r\nBEGIN:VEVENT\r\nUID:weekly@example.org\r\n" +
		"RECURRENCE-ID:" + rid + "\r\nDTSTAMP:20261016T090000Z\r\nDTSTART:" + rid + "\r\nDTEND:20261103T110000Z\r\nSEQUENCE:15\r\n" +
		"SUMMARY:Weekly status\r\nORGANIZER;CN=Ada Example:mailto:ada@example.org\r\n" +
		"ATTENDEE;PARTSTAT=NEEDS-ACTION;RSVP=TRUE:mailto:bo@example.org\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
}

// putRows stores the rows of one resource as given (kinds kept).
func (x *invBox) putRows(remote string, rows ...store.Event) []store.Event {
	x.t.Helper()
	out, err := x.b.store.ReplaceResource(x.ctx, x.cal.ID, remote, rows, func(rows []store.Event) []store.Occurrence {
		var occ []store.Occurrence
		for _, r := range rows {
			occ = append(occ, store.Occurrence{EventID: r.ID, CalendarID: x.cal.ID, AccountID: x.calAcc, StartAt: r.StartAt, EndAt: r.EndAt,
				OccurrenceKey: r.RecurrenceID})
		}
		return occ
	})
	if err != nil {
		x.t.Fatal(err)
	}
	return out
}

// An instance invitation links to the Graph-shaped instance (a single row
// whose start is the RECURRENCE-ID), not to the series: the series'
// answer does not answer it, the instance's answer (event.respond, keyed
// by the row) does; the override shape of CalDAV still links.
func TestInvitationInstanceLink(t *testing.T) {
	x := newInvBox(t)
	x.addCalDAV(nil)
	org := &api.Address{Address: "ada@example.org"}
	at := func(d int) time.Time { return time.Date(2026, 10, 6+d, 10, 0, 0, 0, time.UTC) }
	base := store.Event{UID: "weekly@example.org", Title: "Weekly status", Status: api.EventStatusConfirmed, ShowAs: api.ShowAsBusy, Organizer: org}
	master := base
	master.Kind, master.StartAt, master.EndAt, master.MyResponse = store.EventMaster, at(0), at(0).Add(time.Hour), api.ResponseAccepted
	master.RRule = "FREQ=WEEKLY"
	x.putRows("/series", master)
	var inst store.Event
	for _, d := range []int{21, 28} { // 27 Oct and 3 Nov, each a single row of its own (Graph)
		e := base
		e.Kind, e.StartAt, e.EndAt = store.EventSingle, at(d), at(d).Add(time.Hour)
		inst = x.putRows(fmt.Sprintf("/inst-%d", d), e)[0]
	}
	m := x.receive(x.inbox, instanceInvite("20261103T100000Z"))
	if m.Invitation.State != api.InvitationUnanswered {
		t.Errorf("the series' answer answered the instance: %+v", m.Invitation)
	}
	if c := x.card(m); c.EventID != api.EventID(inst.ID) || c.Response != "" {
		t.Errorf("link %q (want the 3 Nov row %q), response %q", c.EventID, inst.ID, c.Response)
	}
	// As event.respond on the instance row: its response, keyed by the row.
	if _, err := x.b.store.DB().ExecContext(x.ctx, `UPDATE cal_events SET my_response = 'accepted' WHERE id = ?`, inst.ID); err != nil {
		t.Fatal(err)
	}
	if err := x.b.store.PutResponse(x.ctx, store.Response{AccountID: x.mail, UID: "weekly@example.org", Response: api.ResponseAccepted,
		Via: "graph", CalendarID: x.cal.ID, EventID: inst.ID}); err != nil {
		t.Fatal(err)
	}
	m = x.message(m.ID)
	c := x.card(m)
	if m.Invitation.State != api.InvitationAnswered || c.State != api.InvitationAnswered || c.Response != api.ResponseAccepted ||
		c.EventID != api.EventID(inst.ID) || !c.CanRespond {
		t.Errorf("after the instance's answer: %+v / %+v", m.Invitation, c)
	}
	if list, _ := x.b.Invitations().List(x.ctx, api.InvitationListParams{State: api.InvitationUnanswered}); len(list.Invitations) != 0 {
		t.Errorf("still listed %+v", list.Invitations)
	}

	// The override shape (CalDAV): master + override of the instance.
	y := newInvBox(t)
	y.addCalDAV(nil)
	ym := base
	ym.Kind, ym.StartAt, ym.EndAt, ym.MyResponse = store.EventMaster, at(0), at(0).Add(time.Hour), api.ResponseAccepted
	ym.RRule = "FREQ=WEEKLY"
	ov := base
	ov.Kind, ov.RecurrenceID, ov.StartAt, ov.EndAt, ov.MyResponse = store.EventOverride, "2026-11-03T10:00:00Z", at(28), at(28).Add(time.Hour), api.ResponseTentative
	stored := y.putRows("/series", ym, ov)
	ym2 := y.receive(y.inbox, instanceInvite("20261103T100000Z"))
	if c := y.card(ym2); c.EventID != api.EventID(stored[1].ID) || c.Response != api.ResponseTentative || c.State != api.InvitationAnswered {
		t.Errorf("override shape: %+v", c)
	}
}
