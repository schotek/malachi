// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

func putInvite(t *testing.T, s *Store, m *Message, method api.InvitationMethod, uid string, seq int, start time.Time) {
	t.Helper()
	if err := s.PutInvitation(context.Background(), Invitation{MessageID: m.ID, AccountID: m.AccountID, PartID: "2", Method: method,
		UID: uid, Sequence: seq, Organizer: "boss@example.invalid", StartAt: start, EndAt: start.Add(time.Hour), Title: "Q4 planning",
		Card: []byte(`{"title":"Q4 planning","organizer":{"name":"Boss","address":"boss@example.invalid"}}`), ParserVersion: 1}); err != nil {
		t.Fatal(err)
	}
}

// Every listing that builds a MessageSummary carries the invitation:
// message.list, message.get, search, thread.get members and thread.list
// heads.
func TestInvitationJoinInListings(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	a, _ := calAccount(t, s, "me@example.invalid")
	inbox := seedFolder(t, s, a.ID, "INBOX", api.RoleInbox)
	future := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Millisecond)
	inv := seedThread(t, s, inbox, Message{UID: 1, RFCMessageID: "<inv@x>", Subject: "Invitation: Q4 planning"})
	plain := seedThread(t, s, inbox, Message{UID: 2, RFCMessageID: "<re@x>", InReplyTo: "<inv@x>", Subject: "Re: Q4"})
	putInvite(t, s, inv, api.InvitationMethodRequest, "u1", 0, future)

	check := func(what string, msgs []Message) {
		t.Helper()
		found := false
		for _, m := range msgs {
			switch m.ID {
			case inv.ID:
				found = true
				if m.Invitation == nil || m.Invitation.Kind != api.InvitationKindInvitation || m.Invitation.State != api.InvitationUnanswered ||
					m.Invitation.Title != "Q4 planning" || m.Invitation.Organizer == nil || m.Invitation.Organizer.Name != "Boss" ||
					m.Invitation.Start == nil || !m.Invitation.Start.Equal(future) {
					t.Errorf("%s: invitation %+v", what, m.Invitation)
				}
			case plain.ID:
				if m.Invitation != nil {
					t.Errorf("%s: plain message has %+v", what, m.Invitation)
				}
			}
		}
		if !found {
			t.Errorf("%s: invitation message not listed", what)
		}
	}
	list, _, _, err := s.ListMessages(ctx, a.ID, inbox.ID, "", 50, "", "")
	if err != nil {
		t.Fatal(err)
	}
	check("list", list)
	got, err := s.GetMessage(ctx, a.ID, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	check("get", []Message{got})
	rows, _, _, err := s.SearchMessages(ctx, SearchFilter{AccountID: a.ID}, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	var found []Message
	for _, r := range rows {
		found = append(found, r.Message)
	}
	check("search", found)
	members, err := s.ThreadMessages(ctx, a.ID, inv.ThreadID, inbox.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	check("thread members", members)
	// The thread's head is the reply (newer); make the invitation the head
	// of its own thread to see it on thread.list.
	threads, _, _, err := s.ListThreads(ctx, a.ID, inbox.ID, "", 50, "", "")
	if err != nil || len(threads) != 1 {
		t.Fatalf("threads: %+v %v", threads, err)
	}
	putInvite(t, s, plain, api.InvitationMethodReply, "u1", 0, future)
	threads, _, _, _ = s.ListThreads(ctx, a.ID, inbox.ID, "", 50, "", "")
	if h := threads[0].Latest.Invitation; h == nil || h.Kind != api.InvitationKindReply || h.State != api.InvitationProcessed {
		t.Errorf("thread head: %+v", h)
	}
}

func TestInvitationStates(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	a, cal := calAccount(t, s, "me@example.invalid")
	inbox := seedFolder(t, s, a.ID, "INBOX", api.RoleInbox)
	future := time.Now().Add(48 * time.Hour).UTC()
	msg := func(uid uint32) *Message {
		return seedThread(t, s, inbox, Message{UID: uid, RFCMessageID: "<" + string(rune('a'+uid)) + "@x>"})
	}
	state := func(m *Message) api.InvitationSummary {
		t.Helper()
		got, err := s.GetMessage(ctx, a.ID, m.ID)
		if err != nil || got.Invitation == nil {
			t.Fatalf("get: %+v %v", got.Invitation, err)
		}
		return *got.Invitation
	}
	first := msg(1)
	putInvite(t, s, first, api.InvitationMethodRequest, "u", 0, future)
	if st := state(first); st.State != api.InvitationUnanswered || st.Kind != api.InvitationKindInvitation {
		t.Fatalf("first: %+v", st)
	}
	if err := s.PutResponse(ctx, Response{AccountID: a.ID, UID: "u", Sequence: 0, Response: api.ResponseAccepted, Via: "imip"}); err != nil {
		t.Fatal(err)
	}
	if st := state(first); st.State != api.InvitationAnswered {
		t.Fatalf("answered: %+v", st)
	}
	// An update (SEQUENCE 1 of a known UID) outdates the first and waits for an answer.
	update := msg(2)
	putInvite(t, s, update, api.InvitationMethodRequest, "u", 1, future)
	if st := state(first); st.State != api.InvitationOutdated {
		t.Fatalf("first after update: %+v", st)
	}
	if st := state(update); st.State != api.InvitationUnanswered || st.Kind != api.InvitationKindUpdate {
		t.Fatalf("update: %+v", st)
	}
	// SEQUENCE > 0 of an unknown UID is a plain invitation; a stored event
	// makes the UID known.
	other := msg(3)
	putInvite(t, s, other, api.InvitationMethodRequest, "v", 2, future)
	if st := state(other); st.Kind != api.InvitationKindInvitation {
		t.Fatalf("unknown uid: %+v", st)
	}
	if _, err := s.ReplaceResource(ctx, cal.ID, "/v", []Event{{Kind: EventSingle, UID: "v"}}, nil); err != nil {
		t.Fatal(err)
	}
	if st := state(other); st.Kind != api.InvitationKindUpdate {
		t.Fatalf("known by event: %+v", st)
	}
	// A cancel cancels the requests of its UID up to its SEQUENCE.
	cancel := msg(4)
	putInvite(t, s, cancel, api.InvitationMethodCancel, "u", 1, future)
	if st := state(update); st.State != api.InvitationCancelled {
		t.Fatalf("update after cancel: %+v", st)
	}
	if st := state(cancel); st.State != api.InvitationCancelled || st.Kind != api.InvitationKindCancelled {
		t.Fatalf("cancel: %+v", st)
	}
	// An event in the past is outdated; an unreadable part is invalid.
	past := msg(5)
	putInvite(t, s, past, api.InvitationMethodRequest, "w", 0, time.Now().Add(-48*time.Hour))
	if st := state(past); st.State != api.InvitationOutdated {
		t.Fatalf("past: %+v", st)
	}
	bad := msg(6)
	if err := s.PutInvitation(ctx, Invitation{MessageID: bad.ID, AccountID: a.ID, PartID: "2", Method: api.InvitationMethodOther, ParserVersion: 1}); err != nil {
		t.Fatal(err)
	}
	if st := state(bad); st.State != api.InvitationInvalid {
		t.Fatalf("invalid: %+v", st)
	}
	if inv, err := s.GetInvitation(ctx, bad.ID); err != nil || inv.Card != nil {
		t.Fatalf("invalid card: %+v %v", inv, err)
	}

	// invitation.list: one per UID, the newest message of it.
	listed, err := s.ListInvitations(ctx, InvitationQuery{AccountIDs: []string{a.ID}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	byUID := map[string]string{}
	for _, l := range listed {
		if _, dup := byUID[l.UID]; dup {
			t.Fatalf("uid %q twice", l.UID)
		}
		byUID[l.UID] = l.MessageID
		if l.FolderRole != api.RoleInbox {
			t.Errorf("role %q", l.FolderRole)
		}
	}
	if byUID["u"] != cancel.ID || byUID["v"] != other.ID || len(byUID) != 4 {
		t.Fatalf("listed: %v", byUID)
	}
	// Only v waits for an answer (u was cancelled, w is past, the last one invalid).
	if un, err := s.ListInvitations(ctx, InvitationQuery{State: api.InvitationUnanswered}, time.Now()); err != nil || len(un) != 1 || un[0].UID != "v" {
		t.Fatalf("unanswered: %+v %v", un, err)
	}
	if byU, err := s.InvitationsByUID(ctx, a.ID, "u"); err != nil || len(byU) != 3 {
		t.Fatalf("by uid: %d %v", len(byU), err)
	}
	if ids, err := s.InvitationsBelowVersion(ctx, 2, 100); err != nil || len(ids) != 6 {
		t.Fatalf("below version: %v %v", ids, err)
	}
}

// Deleting a message row takes its invitation along; the answer, shared
// by the UID's copies, stays.
func TestInvitationCascadesWithMessage(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	a, _ := calAccount(t, s, "me@example.invalid")
	inbox := seedFolder(t, s, a.ID, "INBOX", api.RoleInbox)
	m := seedMessage(t, s, inbox, 7, "Invite", threadBase)
	putInvite(t, s, m, api.InvitationMethodRequest, "u", 0, threadBase)
	if err := s.PutResponse(ctx, Response{AccountID: a.ID, UID: "u", Response: api.ResponseTentative, Via: "none"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteMessagesByUID(ctx, inbox.ID, []uint32{7}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetInvitation(ctx, m.ID); err != ErrNotFound {
		t.Fatalf("invitation after delete: %v", err)
	}
	if r, err := s.GetResponse(ctx, a.ID, "u", ""); err != nil || r.Response != api.ResponseTentative {
		t.Fatalf("response: %+v %v", r, err)
	}
	if err := s.DeleteResponse(ctx, a.ID, "u", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetResponse(ctx, a.ID, "u", ""); err != ErrNotFound {
		t.Fatalf("after delete: %v", err)
	}
	if err := s.PutInvitation(ctx, Invitation{MessageID: "m_none", AccountID: a.ID, Method: api.InvitationMethodRequest, ParserVersion: 1}); err == nil {
		t.Fatal("invitation of no message stored")
	}
}

func TestInvitationScanBatch(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	a, _ := calAccount(t, s, "me@example.invalid")
	inbox := seedFolder(t, s, a.ID, "INBOX", api.RoleInbox)
	ics := seedThread(t, s, inbox, Message{UID: 1, RFCMessageID: "<1@x>"})
	seedThread(t, s, inbox, Message{UID: 2, RFCMessageID: "<2@x>"})
	named := seedThread(t, s, inbox, Message{UID: 3, RFCMessageID: "<3@x>"})
	seedThread(t, s, inbox, Message{UID: 4, RFCMessageID: "<4@x>"})
	for id, att := range map[string]api.Attachment{
		ics.ID:   {PartID: "2", ContentType: "text/calendar; method=REQUEST", Filename: "invite"},
		named.ID: {PartID: "2", ContentType: "application/octet-stream", Filename: "Meeting.ICS"},
	} {
		if err := s.SetMessageBody(ctx, id, BodyUpdate{Attachments: []api.Attachment{att}, HasAttachments: true}); err != nil {
			t.Fatal(err)
		}
	}
	rows, next, err := s.InvitationScanBatch(ctx, 0, 3)
	if err != nil || len(rows) != 2 || rows[0].MessageID != ics.ID || rows[1].MessageID != named.ID || next == 0 {
		t.Fatalf("batch 1: %+v next %d %v", rows, next, err)
	}
	rows, next, err = s.InvitationScanBatch(ctx, next, 3)
	if err != nil || len(rows) != 0 || next != 0 {
		t.Fatalf("batch 2: %+v next %d %v", rows, next, err)
	}
	if !IsCalendarPart("Application/ICS", "") || IsCalendarPart("text/plain", "ics") {
		t.Fatal("IsCalendarPart")
	}
}
