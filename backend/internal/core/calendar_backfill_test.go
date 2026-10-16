// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"bytes"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/calendar"
	"github.com/schotek/malachi/backend/internal/ingest"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Mail stored before the calendar is scanned once: the rows appear, the
// cursor ends done, a second run reads nothing, and a row an older parser
// wrote is read again.
func TestInvitationBackfill(t *testing.T) {
	x := newInvBox(t)
	was := invitationBackfillRows
	invitationBackfillRows = 2 // several batches
	t.Cleanup(func() { invitationBackfillRows = was })
	var ids []string
	for _, f := range []string{"invite-outlook.eml", "invite-google.eml", "invite-in-forwarded-message.eml", "invite-reply.eml", "invite-cancel.eml"} {
		ids = append(ids, x.store(x.inbox, readMIME(t, f)).ID) // no Stored hook: mail from before 0020
	}
	if _, err := x.b.store.DB().ExecContext(x.ctx, `DELETE FROM board_dirty`); err != nil {
		t.Fatal(err)
	}
	if err := x.b.backfillInvitations(x.ctx); err != nil {
		t.Fatal(err)
	}
	for i, id := range ids {
		_, err := x.b.store.GetInvitation(x.ctx, id)
		if want := i != 2; (err == nil) != want {
			t.Errorf("message %d: row %v, want %v", i, err, want)
		}
	}
	cursor, _, _ := x.b.store.GetMeta(x.ctx, store.MetaCalendarInvitesScan)
	if cursor != strconv.Itoa(calendar.ParserVersion)+":done" {
		t.Errorf("cursor %q", cursor)
	}
	if n, err := x.b.store.CountBoardDirty(x.ctx); err != nil || n == 0 {
		t.Errorf("board_dirty %d %v", n, err)
	}
	if m := x.message(ids[0]); m.Invitation == nil || m.Invitation.Kind != api.InvitationKindInvitation {
		t.Errorf("summary %+v", m.Invitation)
	}
	// A row of an older parser is read again.
	row, _ := x.b.store.GetInvitation(x.ctx, ids[1])
	row.ParserVersion, row.Title = calendar.ParserVersion-1, "stale"
	if err := x.b.store.PutInvitation(x.ctx, row); err != nil {
		t.Fatal(err)
	}
	if err := x.b.backfillInvitations(x.ctx); err != nil {
		t.Fatal(err)
	}
	if again, _ := x.b.store.GetInvitation(x.ctx, ids[1]); again.ParserVersion != calendar.ParserVersion || again.Title == "stale" {
		t.Errorf("re-read %+v", again)
	}
	// A cursor of another parser version starts the scan over.
	if err := x.b.store.DeleteInvitation(x.ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	if err := x.b.store.SetMeta(x.ctx, store.MetaCalendarInvitesScan, "0:99999"); err != nil {
		t.Fatal(err)
	}
	if err := x.b.backfillInvitations(x.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := x.b.store.GetInvitation(x.ctx, ids[0]); err != nil {
		t.Errorf("rescan: %v", err)
	}
	// A cursor of this version resumes after its rowid.
	if err := x.b.store.DeleteInvitation(x.ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	if err := x.b.store.SetMeta(x.ctx, store.MetaCalendarInvitesScan, strconv.Itoa(calendar.ParserVersion)+":99999"); err != nil {
		t.Fatal(err)
	}
	if err := x.b.backfillInvitations(x.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := x.b.store.GetInvitation(x.ctx, ids[0]); err == nil {
		t.Error("the resumed scan read before its cursor")
	}
}

// Under neverStoreAttachments the calendar part stays in the stored
// skeleton, so the hook reads the invitation.
func TestInvitationNeverStoreKeepsThePart(t *testing.T) {
	x := newInvBox(t)
	raw := readMIME(t, "invite-google.eml")
	x.uid++
	row := &store.Message{AccountID: x.mail, FolderID: x.inbox.ID, UID: x.uid, Subject: "Weekly sync", Date: time.Now(),
		InternalDate: time.Now(), RFCMessageID: "<g@example.org>", Size: int64(len(raw))}
	if err := x.b.store.UpsertMessages(x.ctx, []*store.Message{row}); err != nil {
		t.Fatal(err)
	}
	pol := ingest.Policy{NeverStore: true}
	if _, err := ingest.Store(x.ctx, x.b.store, ingest.Request{
		Target: ingest.Target{AccountID: x.mail, MessageID: row.ID, Role: api.RoleInbox, HasServerCopy: true,
			InternalDate: row.InternalDate, Date: row.Date},
		Body: bytes.NewReader(raw), Size: int64(len(raw)), Policy: pol, Now: time.Now(),
		Expect: store.RawExpect{BodyState: store.BodyNone},
	}, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	m := x.message(row.ID)
	for _, a := range m.Attachments {
		if store.IsCalendarPart(a.ContentType, a.Filename) && a.Remote {
			t.Errorf("calendar part %s left on the server", a.PartID)
		}
	}
	x.b.afterMailStored(x.ctx, x.mail, row.ID, pol)
	if m := x.message(row.ID); m.Invitation == nil || m.Invitation.State != api.InvitationUnanswered {
		t.Fatalf("summary %+v", m.Invitation)
	}
}

// A reply stored by parser 1 (its card without the attendee's answer) is
// rebuilt from the stored part at the next scan; the reply is not
// applied again and replyApplied stays as it was.
func TestInvitationRescanRebuildsOldReply(t *testing.T) {
	x := newInvBox(t)
	x.addCalDAV(nil)
	start := time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
	ev := x.putEvent("/q4.ics", store.Event{UID: outlookUID, Title: "Quarterly review", StartAt: start, EndAt: start.Add(time.Hour),
		Status: api.EventStatusConfirmed, ShowAs: api.ShowAsBusy, Organizer: &api.Address{Address: "jana@example.test"}, IAmOrganizer: true,
		AttendeeCount: 1, Attendees: []api.Attendee{{Address: "bo@example.org", Response: api.ResponseNeedsAction}}})
	m := x.receive(x.inbox, readMIME(t, "invite-reply.eml"))
	row, err := x.b.store.GetInvitation(x.ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	// As parser 1 stored it: no response in the card.
	row.ParserVersion, row.Card = 1, []byte(`{"title":"Accepted: Quarterly review","replyApplied":true}`)
	if err := x.b.store.PutInvitation(x.ctx, row); err != nil {
		t.Fatal(err)
	}
	// The event moved on (a later answer); the old reply must not undo it.
	if _, err := x.b.store.DB().ExecContext(x.ctx, `UPDATE cal_events SET attendees_json = ? WHERE id = ?`,
		`[{"address":"bo@example.org","role":"required","response":"declined","type":"person"}]`, ev.ID); err != nil {
		t.Fatal(err)
	}
	if err := x.b.store.SetMeta(x.ctx, store.MetaCalendarInvitesScan, "1:done"); err != nil {
		t.Fatal(err)
	}
	if err := x.b.backfillInvitations(x.ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := x.b.store.GetInvitation(x.ctx, m.ID)
	if got.ParserVersion != calendar.ParserVersion {
		t.Errorf("version %d", got.ParserVersion)
	}
	c := x.card(m)
	if c.Response != api.ResponseAccepted || !c.ReplyApplied || c.ReplyFrom == nil {
		t.Errorf("rebuilt card %+v", c)
	}
	if e, _ := x.b.store.GetEvent(x.ctx, ev.ID); e.Attendees[0].Response != api.ResponseDeclined {
		t.Errorf("the reply was applied again: %+v", e.Attendees)
	}
	if cursor, _, _ := x.b.store.GetMeta(x.ctx, store.MetaCalendarInvitesScan); cursor != strconv.Itoa(calendar.ParserVersion)+":done" {
		t.Errorf("cursor %q", cursor)
	}
}
