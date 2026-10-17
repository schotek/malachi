// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package imip

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/calendar"
	"github.com/schotek/malachi/backend/internal/calendar/ics"
	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/pkg/api"
)

const (
	mimeDir     = "../../../testdata/mime"
	calendarDir = "../../../testdata/calendar"
)

var (
	bo  = []string{"bo@example.org"}
	ada = []string{"ada@example.org"}
	now = time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
)

func read(t *testing.T, dir, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type calPart struct {
	id   string
	data []byte
}

// calendarParts returns the calendar parts of a message as the core finds
// them: the leaves of type text/calendar or application/ics that
// internal/mime lists (a message/rfc822 is one leaf: its parts are not
// the message's).
func calendarParts(t *testing.T, msg []byte) []calPart {
	t.Helper()
	p, err := mime.Parse(bytes.NewReader(msg), mime.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	var out []calPart
	for _, a := range p.Attachments {
		if a.ContentType != "text/calendar" && a.ContentType != "application/ics" {
			continue
		}
		part, err := mime.ExtractPart(bytes.NewReader(msg), a.PartID, mime.DefaultLimits(), api.MaxICSBytes)
		if err != nil {
			out = append(out, calPart{id: a.PartID})
			continue
		}
		out = append(out, calPart{id: a.PartID, data: part.Body})
	}
	return out
}

func partIDs(parts []calPart) string {
	ids := make([]string, len(parts))
	for i, p := range parts {
		ids[i] = p.id
	}
	return strings.Join(ids, ",")
}

func TestInviteMessages(t *testing.T) {
	cases := []struct {
		file, parts string
		method      api.InvitationMethod
		me          []string
		check       func(t *testing.T, inv Invitation)
	}{
		{"invite-outlook.eml", "3", api.InvitationMethodRequest, bo, func(t *testing.T, inv Invitation) {
			if inv.Sequence != 0 || inv.Me == nil || inv.Me.Response != api.ResponseNeedsAction || !inv.RSVP ||
				inv.Event.Zone != "Europe/Budapest" || inv.Event.VideoCall == nil || inv.Event.VideoCall.Provider != api.VideoProviderTeams ||
				inv.Organizer.Address != "ada@example.org" || inv.Event.AttendeeCount != 2 {
				t.Errorf("%+v", inv)
			}
			c := inv.Card("3")
			if c.Kind != api.InvitationKindInvitation || c.Start == nil || !c.Start.Equal(time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)) ||
				c.TimeZone != "Europe/Budapest" || c.Conflicts == nil || c.Targets == nil || c.Day.Busy == nil || len(c.Attendees) != 2 {
				t.Errorf("card %+v", c)
			}
			if inv.OrganizerMismatch("ada@example.org") || !inv.OrganizerMismatch("ada@example.net") || inv.OrganizerMismatch("x@mail.example.org") {
				t.Error("organizer mismatch")
			}
		}},
		{"invite-update.eml", "3", api.InvitationMethodRequest, bo, func(t *testing.T, inv Invitation) {
			if inv.Sequence != 1 || !inv.Event.Start.Equal(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)) {
				t.Errorf("update %d %s", inv.Sequence, inv.Event.Start)
			}
		}},
		{"invite-cancel.eml", "3", api.InvitationMethodCancel, bo, func(t *testing.T, inv Invitation) {
			if inv.Sequence != 2 || inv.Event.Status != api.EventStatusCancelled || KindOf(inv.Method) != api.InvitationKindCancelled {
				t.Errorf("cancel %+v", inv.Event)
			}
		}},
		{"invite-reply.eml", "2", api.InvitationMethodReply, ada, func(t *testing.T, inv Invitation) {
			if inv.ReplyFrom == nil || inv.ReplyFrom.Address != "bo@example.org" || inv.ReplyResponse != api.ResponseAccepted ||
				inv.ReplyAttendees != 1 || inv.ReplyComment != "See you there, Bo" || !inv.ReplyMatchesSender("Bo@example.org") {
				t.Errorf("reply %+v", inv)
			}
			if inv.Organizer != nil {
				t.Errorf("Exchange's REPLY has no ORGANIZER here: %+v", inv.Organizer)
			}
		}},
		{"invite-reply-from-mismatch.eml", "2", api.InvitationMethodReply, ada, func(t *testing.T, inv Invitation) {
			// The From is mallory@example.net: the data says so, the core decides (D10).
			if inv.ReplyFrom == nil || inv.ReplyFrom.Address != "bo@example.org" || inv.ReplyResponse != api.ResponseDeclined ||
				inv.ReplyMatchesSender("mallory@example.net") {
				t.Errorf("spoofed reply %+v", inv)
			}
		}},
		{"invite-google.eml", "1.3,2", api.InvitationMethodRequest, bo, func(t *testing.T, inv Invitation) {
			if inv.Event.Kind != calendar.KindMaster || inv.Event.Recurrence == nil || inv.Event.Recurrence.Frequency != api.FrequencyWeekly ||
				inv.Event.VideoCall == nil || inv.Event.VideoCall.Provider != api.VideoProviderMeet || inv.Me == nil || inv.Me.Address != "bo@example.org" {
				t.Errorf("google %+v", inv.Event)
			}
		}},
		{"invite-two-calendar-parts.eml", "2,3", api.InvitationMethodRequest, bo, nil},
		{"invite-calendar-as-attachment.eml", "2", api.InvitationMethodRequest, bo, func(t *testing.T, inv Invitation) {
			if !inv.Event.AllDay || inv.Event.StartDate != "2026-10-30" || inv.Event.ShowAs != api.ShowAsFree {
				t.Errorf("all-day %+v", inv.Event)
			}
			if s := inv.Summary(); s.StartDate != "2026-10-30" || s.Start != nil || !s.AllDay {
				t.Errorf("summary %+v", s)
			}
		}},
		// The calendar part inside the forwarded message/rfc822 is not a
		// part of this message: no calendar part at all.
		{"invite-in-forwarded-message.eml", "", "", bo, nil},
	}
	files, _ := filepath.Glob(filepath.Join(mimeDir, "invite-*.eml"))
	if len(files) != len(cases) {
		t.Errorf("%d invite fixtures, %d cases", len(files), len(cases))
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			parts := calendarParts(t, read(t, mimeDir, c.file))
			if got := partIDs(parts); got != c.parts {
				t.Fatalf("calendar parts %q, want %q", got, c.parts)
			}
			for _, p := range parts {
				inv, err := ParseMessage(p.data, c.me)
				if err != nil {
					t.Fatalf("part %s: %v", p.id, err)
				}
				if inv.Method != c.method {
					t.Errorf("part %s: method %s", p.id, inv.Method)
				}
				if c.check != nil {
					c.check(t, inv)
				}
			}
		})
	}
}

func TestOversizedPart(t *testing.T) {
	// Generated: a committed fixture over 1 MiB would bloat the repository.
	var b bytes.Buffer
	b.WriteString("From: ada@example.org\r\nTo: bo@example.org\r\nSubject: Big\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=b\r\n\r\n--b\r\nContent-Type: text/plain\r\n\r\nhi\r\n--b\r\n" +
		"Content-Type: text/calendar; method=REQUEST\r\n\r\nBEGIN:VCALENDAR\r\nMETHOD:REQUEST\r\nBEGIN:VEVENT\r\nUID:big@example.org\r\n")
	for b.Len() < api.MaxICSBytes+4096 {
		b.WriteString("X-PAD:" + strings.Repeat("x", 70) + "\r\n")
	}
	b.WriteString("DTSTART:20261020T090000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n--b--\r\n")
	msg := b.Bytes()
	p, err := mime.Parse(bytes.NewReader(msg), mime.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	var id string
	for _, a := range p.Attachments {
		if a.ContentType == "text/calendar" {
			id = a.PartID
		}
	}
	if id != "2" {
		t.Fatalf("part %q", id)
	}
	if _, err := mime.ExtractPart(bytes.NewReader(msg), id, mime.DefaultLimits(), api.MaxICSBytes); !errors.Is(err, mime.ErrPartTooBig) {
		t.Errorf("an oversized part: %v", err)
	}
	if _, err := ParseMessage(msg[strings.Index(string(msg), "BEGIN:VCALENDAR"):], bo); !errors.Is(err, ics.ErrTooBig) {
		t.Errorf("ParseMessage over the cap: %v", err)
	}
}

func TestMethodFixtures(t *testing.T) {
	inv, err := ParseMessage(read(t, calendarDir, "method-reply-spoofed.ics"), ada)
	if err != nil {
		t.Fatal(err)
	}
	if inv.Method != api.InvitationMethodReply || inv.ReplyAttendees != 2 || inv.ReplyFrom.Address != "someone-else@example.org" ||
		!slices.ContainsFunc(inv.Problems, func(p calendar.Problem) bool { return p.Code == ProblemReplyAttendees }) {
		t.Errorf("spoofed reply %+v", inv)
	}
	unk, err := ParseMessage(read(t, calendarDir, "method-unknown.ics"), bo)
	if err != nil || unk.Method != api.InvitationMethodOther || unk.RawMethod != "X-FROBNICATE" || KindOf(unk.Method) != api.InvitationKindOther {
		t.Errorf("unknown method: %v %+v", err, unk.Method)
	}
	cu, err := ParseMessage(read(t, calendarDir, "method-cancel-unknown-uid.ics"), bo)
	if err != nil || cu.Method != api.InvitationMethodCancel || cu.UID != "never-seen@example.org" || cu.Sequence != 3 {
		t.Errorf("cancel: %v %+v", err, cu)
	}
	mixed, err := ParseMessage(read(t, calendarDir, "mixed-uids.ics"), bo)
	if err != nil || mixed.UID != "mixed-1@example.org" ||
		!slices.ContainsFunc(mixed.Problems, func(p calendar.Problem) bool { return p.Code == calendar.ProblemMixedUIDs }) {
		t.Errorf("mixed uids: %v %+v", err, mixed.Problems)
	}
	if _, err := ParseMessage(read(t, calendarDir, "two-vcalendars.ics"), bo); !errors.Is(err, ErrMultipleCalendars) {
		t.Errorf("two VCALENDARs: %v", err)
	}
	orphan, err := ParseMessage(read(t, calendarDir, "override-without-master.ics"), bo)
	if err != nil || orphan.RecurrenceID != "2026-10-21T09:00:00Z" {
		t.Errorf("instance invitation: %v %q", err, orphan.RecurrenceID)
	}
	nm, err := ParseMessage(read(t, calendarDir, "organizer-missing.ics"), bo)
	if err == nil {
		if _, err := BuildReply(nm, "", api.ResponseAccepted, "", now); !errors.Is(err, ErrNoOrganizer) {
			t.Errorf("reply without organizer: %v", err)
		}
	}
	// Every fixture of the corpus reads or fails cleanly.
	files, _ := filepath.Glob(filepath.Join(calendarDir, "*.ics"))
	for _, f := range files {
		data, _ := os.ReadFile(f)
		inv, err := ParseMessage(data, bo)
		if err != nil {
			if !errors.Is(err, calendar.ErrNoEvent) && !errors.Is(err, ics.ErrTooBig) && !errors.Is(err, ErrMultipleCalendars) {
				t.Errorf("%s: %v", filepath.Base(f), err)
			}
			continue
		}
		_ = inv.Card("1")
	}
}

// requireProps checks the properties RFC 5546 requires of a method's
// VEVENTs (and VCALENDAR).
func requireProps(t *testing.T, data []byte, method string, props ...string) *ics.Component {
	t.Helper()
	obj, err := ics.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if obj.HasProblems() {
		t.Errorf("problems %+v", obj.Problems)
	}
	cal := obj.Calendar()
	for _, p := range []string{"PRODID", "VERSION", "METHOD"} {
		if cal.Property(p) == nil {
			t.Errorf("VCALENDAR without %s", p)
		}
	}
	if propValue(cal, "METHOD") != method {
		t.Errorf("method %q", propValue(cal, "METHOD"))
	}
	evs := cal.Children("VEVENT")
	if len(evs) == 0 {
		t.Fatal("no VEVENT")
	}
	for _, ev := range evs {
		for _, p := range props {
			if ev.Property(p) == nil {
				t.Errorf("VEVENT without %s:\n%s", p, data)
			}
		}
	}
	return cal
}

func TestBuildReply(t *testing.T) {
	parts := calendarParts(t, read(t, mimeDir, "invite-outlook.eml"))
	inv, err := ParseMessage(parts[0].data, bo)
	if err != nil {
		t.Fatal(err)
	}
	out, err := BuildReply(inv, "", api.ResponseTentative, "Running\nlate \u202E", now)
	if err != nil {
		t.Fatal(err)
	}
	cal := requireProps(t, out, "REPLY", "ATTENDEE", "DTSTAMP", "ORGANIZER", "UID", "SEQUENCE", "DTSTART", "DTEND", "SUMMARY")
	ev := cal.Children("VEVENT")[0]
	if at := ev.Properties("ATTENDEE"); len(at) != 1 || at[0].CalAddress().Email != "bo@example.org" || at[0].Param("PARTSTAT") != "TENTATIVE" {
		t.Errorf("attendee %+v", at)
	}
	if ev.Property("COMMENT").Text() != "Running\nlate" || propValue(ev, "DTSTAMP") != "20261017T090000Z" {
		t.Errorf("comment %q", ev.Property("COMMENT").Text())
	}
	// The organiser's DTSTART is echoed with the VTIMEZONE it needs.
	if ev.Property("DTSTART").Param("TZID") != "Central Europe Standard Time" || len(cal.Children("VTIMEZONE")) != 1 {
		t.Errorf("times not echoed:\n%s", out)
	}
	back, err := ParseMessage(out, ada)
	if err != nil || back.Method != api.InvitationMethodReply || back.ReplyFrom.Address != "bo@example.org" || back.ReplyResponse != api.ResponseTentative || back.UID != inv.UID {
		t.Errorf("reply re-read: %v %+v", err, back)
	}
	if got := Recipients(MethodReply, nil, &inv.Event, bo); !slices.Equal(got, []string{"ada@example.org"}) {
		t.Errorf("reply recipients %v", got)
	}
	if _, err := BuildReply(inv, "", api.ResponseNeedsAction, "", now); !errors.Is(err, ErrResponse) {
		t.Errorf("needsAction: %v", err)
	}
	if _, err := BuildReply(inv, "javascript:x", api.ResponseAccepted, "", now); !errors.Is(err, ErrNoAttendee) {
		t.Errorf("bad me: %v", err)
	}
	// An instance invitation echoes its RECURRENCE-ID.
	orphan, _ := ParseMessage(read(t, calendarDir, "override-without-master.ics"), bo)
	out, err = BuildReply(orphan, "", api.ResponseDeclined, "", now)
	if err != nil {
		t.Fatal(err)
	}
	requireProps(t, out, "REPLY", "RECURRENCE-ID", "ATTENDEE", "ORGANIZER", "UID", "DTSTAMP")
}

func organised(t *testing.T) calendar.Object {
	t.Helper()
	org := &api.Address{Name: "Ada", Address: "ada@example.org"}
	ev, err := calendar.FromInput(api.EventInput{
		Title: "Planning", Start: "2026-10-22T10:00:00", End: "2026-10-22T11:00:00", TimeZone: "Europe/Prague",
		Recurrence: &api.Recurrence{Frequency: api.FrequencyWeekly, Interval: 1},
		Attendees:  []api.EventInputAttendee{{Name: "Bo", Address: "bo@example.org"}, {Address: "cy@example.org"}},
	}, calendar.InputOptions{UID: "plan@example.org", Organizer: org, DefaultAlertMinutes: 15})
	if err != nil {
		t.Fatal(err)
	}
	return calendar.Object{Master: &ev}
}

func TestBuildRequest(t *testing.T) {
	obj := organised(t)
	out, err := BuildRequest(obj, api.Address{Name: "Ada", Address: "ada@example.org"}, now)
	if err != nil {
		t.Fatal(err)
	}
	cal := requireProps(t, out, "REQUEST", "ATTENDEE", "DTSTAMP", "DTSTART", "ORGANIZER", "SEQUENCE", "SUMMARY", "UID")
	ev := cal.Children("VEVENT")[0]
	for _, a := range ev.Properties("ATTENDEE") {
		if a.Param("RSVP") != "TRUE" || a.Param("PARTSTAT") != "NEEDS-ACTION" || a.Param("SCHEDULE-AGENT") != "CLIENT" {
			t.Errorf("attendee %+v", a)
		}
	}
	if ev.Property("ORGANIZER").CalAddress().Email != "ada@example.org" || ev.Property("DTSTART").Param("TZID") != "Europe/Prague" ||
		len(cal.Children("VTIMEZONE")) != 1 || propValue(ev, "RRULE") != "FREQ=WEEKLY" || propValue(ev, "SEQUENCE") != "0" {
		t.Errorf("request:\n%s", out)
	}
	inv, err := ParseMessage(out, bo)
	if err != nil || inv.Me == nil || !inv.RSVP || inv.Event.Kind != calendar.KindMaster {
		t.Errorf("request re-read: %v %+v", err, inv)
	}
	after := *obj.Master
	after.Attendees = append([]api.Attendee{{Address: "dee@example.org"}}, after.Attendees[1:]...) // bo removed, dee added
	if got := Recipients(MethodRequest, obj.Master, &after, ada); !slices.Equal(got, []string{"dee@example.org", "cy@example.org"}) {
		t.Errorf("request recipients %v", got)
	}
	if got := Recipients(MethodCancel, obj.Master, &after, ada); !slices.Equal(got, []string{"bo@example.org"}) {
		t.Errorf("cancel recipients %v", got)
	}
	if got := Recipients(MethodCancel, obj.Master, nil, []string{"ada@example.org", "cy@example.org"}); !slices.Equal(got, []string{"bo@example.org"}) {
		t.Errorf("cancel of a deleted event, never to me: %v", got)
	}
	empty := organised(t)
	empty.Master.Attendees, empty.Master.AttendeeCount = nil, 0
	if _, err := BuildRequest(empty, api.Address{Address: "ada@example.org"}, now); !errors.Is(err, ErrNoAttendee) {
		t.Errorf("no attendees: %v", err)
	}
}

func TestBuildCancel(t *testing.T) {
	obj := organised(t)
	obj.Master.Sequence = 2
	out, err := BuildCancel(obj, api.Address{Address: "ada@example.org"}, api.EditScopeAll, "", now)
	if err != nil {
		t.Fatal(err)
	}
	cal := requireProps(t, out, "CANCEL", "ATTENDEE", "DTSTAMP", "ORGANIZER", "SEQUENCE", "UID", "STATUS")
	ev := cal.Children("VEVENT")[0]
	if propValue(ev, "SEQUENCE") != "3" || propValue(ev, "STATUS") != "CANCELLED" || ev.Property("RECURRENCE-ID") != nil {
		t.Errorf("cancel all:\n%s", out)
	}
	if obj.Master.Sequence != 2 || obj.Master.Status != api.EventStatusConfirmed {
		t.Error("BuildCancel changed its input")
	}
	out, err = BuildCancel(obj, api.Address{Address: "ada@example.org"}, api.EditScopeThis, "2026-10-29T09:00:00Z", now)
	if err != nil {
		t.Fatal(err)
	}
	cal = requireProps(t, out, "CANCEL", "RECURRENCE-ID", "DTSTAMP", "ORGANIZER", "SEQUENCE", "UID", "STATUS")
	evs := cal.Children("VEVENT")
	if len(evs) != 1 || evs[0].Property("RECURRENCE-ID").Param("TZID") != "Europe/Prague" || propValue(evs[0], "RECURRENCE-ID") != "20261029T100000" ||
		propValue(evs[0], "SEQUENCE") != "3" || evs[0].Property("RRULE") != nil {
		t.Errorf("cancel this:\n%s", out)
	}
	inv, err := ParseMessage(out, bo)
	if err != nil || inv.Method != api.InvitationMethodCancel || inv.RecurrenceID != "2026-10-29T09:00:00Z" {
		t.Errorf("cancel re-read: %v %+v", err, inv.RecurrenceID)
	}
	if _, err := BuildCancel(obj, api.Address{Address: "ada@example.org"}, api.EditScopeThis, "2026-10-30T09:00:00Z", now); !errors.Is(err, calendar.ErrNoEvent) {
		t.Errorf("no such instance: %v", err)
	}
	if _, err := BuildCancel(obj, api.Address{Address: "ada@example.org"}, api.EditScopeThisAndFuture, "2026-10-29T09:00:00Z", now); !errors.Is(err, ErrScope) {
		t.Errorf("thisAndFuture: %v", err)
	}
	// Cancelling for removed attendees only.
	removed := RemovedAttendees(obj.Master, &calendar.Event{Attendees: obj.Master.Attendees[:1]})
	only := organised(t)
	only.Master.Attendees = removed
	out, err = BuildCancel(only, api.Address{Address: "ada@example.org"}, api.EditScopeAll, "", now)
	if err != nil {
		t.Fatal(err)
	}
	if at := mustCal(t, out).Children("VEVENT")[0].Properties("ATTENDEE"); len(at) != 1 || at[0].CalAddress().Email != "cy@example.org" {
		t.Errorf("removed attendees %+v", at)
	}
}

func mustCal(t *testing.T, data []byte) *ics.Component {
	t.Helper()
	obj, err := ics.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return obj.Calendar()
}
