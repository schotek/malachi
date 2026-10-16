// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/calendar"
	"github.com/schotek/malachi/backend/internal/calsync"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

func newEvent(t *testing.T, in api.EventInput) calendar.Event {
	t.Helper()
	ev, err := calendar.FromInput(in, calendar.InputOptions{UID: "uid-new", DefaultAlertMinutes: 15,
		Organizer: &api.Address{Address: "me@contoso.invalid"}})
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func storedOf(id string, ev calendar.Event, kind store.EventKind) store.Event {
	return store.Event{RemoteID: id, Kind: kind, UID: ev.UID, StartAt: ev.Start, EndAt: ev.End, AllDay: ev.AllDay,
		TimeZone: ev.Zone, RRule: ev.RRule, IAmOrganizer: true, AttendeeCount: len(ev.Attendees), Recurrence: ev.Recurrence}
}

func TestCalendarCreate(t *testing.T) {
	f := newFakeCal(t)
	p := f.provider(CalendarProviderOptions{})
	ev := newEvent(t, api.EventInput{Title: "Review", Start: "2026-10-14T10:00:00", End: "2026-10-14T11:00:00", TimeZone: "Europe/Prague",
		Notes: "Bring notes", Attendees: []api.EventInputAttendee{{Address: "jana@example.org", Role: api.AttendeeOptional}},
		VideoCall: &api.VideoCallInput{Mode: api.VideoCallCreate, Provider: "teamsForBusiness"}, Private: true, ShowAs: api.ShowAsTentative,
		Recurrence: &api.Recurrence{Frequency: api.FrequencyWeekly, ByDay: []string{"WE"}, Count: 5}})
	opts := calsync.WriteOptions{Notify: true, Attachments: []calendar.Attachment{{Name: "a.pdf", MediaType: "application/pdf", Data: []byte("%PDF"), Size: 4}}}
	res, err := p.Create(context.Background(), storedCal("CAL-A", true), ev, opts)
	if err != nil {
		t.Fatal(err)
	}
	posts := f.reqs("POST", "/me/calendars/CAL-A/events")
	if len(posts) != 1 {
		t.Fatalf("posts %d", len(posts))
	}
	b := posts[0].Body
	start := b["start"].(map[string]any)
	if start["timeZone"] != "Central Europe Standard Time" || start["dateTime"] != "2026-10-14T10:00:00" {
		t.Errorf("start %v", start)
	}
	if b["attendees"] != nil {
		t.Error("attendees must come after the attachments")
	}
	if tx, _ := b["transactionId"].(string); !strings.HasPrefix(tx, "malachi-") {
		t.Errorf("transactionId %v", b["transactionId"])
	}
	if b["isOnlineMeeting"] != true || b["onlineMeetingProvider"] != "teamsForBusiness" || b["sensitivity"] != "private" ||
		b["showAs"] != "tentative" || b["reminderMinutesBeforeStart"] != float64(15) {
		t.Errorf("fields %v", b)
	}
	rec := b["recurrence"].(map[string]any)
	pat, rng := rec["pattern"].(map[string]any), rec["range"].(map[string]any)
	if pat["type"] != "weekly" || pat["daysOfWeek"].([]any)[0] != "wednesday" || rng["type"] != "numbered" ||
		rng["numberOfOccurrences"] != float64(5) || rng["startDate"] != "2026-10-14" || rng["recurrenceTimeZone"] != "Central Europe Standard Time" {
		t.Errorf("recurrence %v", rec)
	}
	id := res.Objects[0].RemoteID
	if len(f.attachments[id]) != 1 || f.attachments[id][0]["@odata.type"] != "#microsoft.graph.fileAttachment" || f.attachments[id][0]["contentBytes"] != "JVBERg==" {
		t.Errorf("attachments %v", f.attachments[id])
	}
	patches := f.reqs("PATCH", "/me/events/"+id)
	if len(patches) != 1 || patches[0].Body["attendees"] == nil {
		t.Fatalf("attendees patch %+v", patches)
	}
	if res.Invitations.Via != api.InvitationsViaServer || res.Invitations.Count != 1 {
		t.Errorf("invitations %+v", res.Invitations)
	}
	// The same create again is the same event (transactionId).
	res2, err := p.Create(context.Background(), storedCal("CAL-A", true), ev, calsync.WriteOptions{})
	if err != nil || res2.Objects[0].RemoteID != id {
		t.Errorf("idempotent create %v %+v", err, res2)
	}
}

func TestCalendarCreateRefusals(t *testing.T) {
	f := newFakeCal(t)
	p := f.provider(CalendarProviderOptions{})
	ev := newEvent(t, api.EventInput{Title: "x", Start: "2026-10-14T10:00:00", End: "2026-10-14T11:00:00", TimeZone: "UTC"})
	big := calsync.WriteOptions{Attachments: []calendar.Attachment{{Name: "big", Data: make([]byte, api.MaxEventAttachmentBytes+1)}}}
	if _, err := p.Create(context.Background(), storedCal("CAL-A", true), ev, big); apiCodeOf(err) != api.CodeAttachmentTooBig {
		t.Errorf("big attachment: %v", err)
	}
	many := calsync.WriteOptions{Attachments: make([]calendar.Attachment, api.MaxEventAttachments+1)}
	if _, err := p.Create(context.Background(), storedCal("CAL-A", true), ev, many); apiCodeOf(err) != api.CodeInvalidArgument {
		t.Errorf("many attachments: %v", err)
	}
	ro := storedCal("CAL-R", false)
	ro.Access = api.CalendarAccessRead
	if _, err := p.Create(context.Background(), ro, ev, calsync.WriteOptions{}); apiCodeOf(err) != api.CodeReadOnly {
		t.Errorf("read-only: %v", err)
	}
	if n := len(f.reqs("POST", "")); n != 0 {
		t.Errorf("%d requests for refused creates", n)
	}
	f.fail["POST /me/calendars/CAL-A/events"] = fakeFailure{400, "ErrorInvalidRecurrencePattern"}
	if _, err := p.Create(context.Background(), storedCal("CAL-A", true), ev, calsync.WriteOptions{}); apiCodeOf(err) != api.CodeRecurrenceUnsupported {
		t.Errorf("recurrence refused: %v", err)
	}
	f.fail["POST /me/calendars/CAL-A/events"] = fakeFailure{403, "ErrorAccessDenied"}
	if _, err := p.Create(context.Background(), storedCal("CAL-A", true), ev, calsync.WriteOptions{}); apiCodeOf(err) != api.CodeCalendarConsentRequired {
		t.Errorf("consent: %v", err)
	}
	// A failed attachment deletes the new event again.
	f.fail["POST /me/events/new-1/attachments"] = fakeFailure{500, "ErrorInternalServerError"}
	f.mu.Lock()
	f.seq = 0
	f.mu.Unlock()
	small := calsync.WriteOptions{Attachments: []calendar.Attachment{{Name: "a", Data: []byte("a")}}}
	if _, err := p.Create(context.Background(), storedCal("CAL-A", true), ev, small); apiCodeOf(err) != api.CodeServerError {
		t.Errorf("attachment failure: %v", err)
	}
	if f.item("new-1") != nil || len(f.reqs("DELETE", "/me/events/new-1")) != 1 {
		t.Error("event kept after a failed attachment")
	}
}

func TestCalendarUpdateScopes(t *testing.T) {
	f := newFakeCal(t)
	seedSeries(f, "CAL-A")
	f.add("CAL-A", single("S1", calT0, time.Hour, nil))
	p := f.provider(CalendarProviderOptions{Window: func() calsync.Window { return calWin }})
	ctx := context.Background()
	cal := storedCal("CAL-A", true)
	mev := newEvent(t, api.EventInput{Title: "Series", Start: "2026-10-12T10:00:00", End: "2026-10-12T11:00:00", TimeZone: "Europe/Prague",
		Recurrence: &api.Recurrence{Frequency: api.FrequencyWeekly, ByDay: []string{"MO"}}})
	stored := storedOf("M1", mev, store.EventMaster)

	// all, with If-Match; the recurrence is kept.
	ch := calendar.Change{Scope: api.EditScopeAll, Event: mev, KeepRecurrence: true}
	ch.Event.Title = "Renamed series"
	res, err := p.Update(ctx, cal, stored, ch, calsync.WriteOptions{IfMatch: f.etag("M1")})
	if err != nil {
		t.Fatal(err)
	}
	pt := f.reqs("PATCH", "/me/events/M1")
	if len(pt) != 1 || pt[0].IfMatch == "" || pt[0].Body["subject"] != "Renamed series" || pt[0].Body["recurrence"] != nil {
		t.Fatalf("patch all %+v", pt)
	}
	if len(res.Objects) != 1 || res.Objects[0].Master.Title != "Renamed series" || len(res.Objects[0].Occurrences) != 3 {
		t.Fatalf("result %+v", res)
	}

	// A stale version is conflict with the current event.
	res, err = p.Update(ctx, cal, stored, ch, calsync.WriteOptions{IfMatch: `W/"old"`})
	if apiCodeOf(err) != api.CodeConflict || len(res.Objects) != 1 || res.Objects[0].ETag != f.etag("M1") {
		t.Fatalf("conflict %v %+v", err, res)
	}

	// this: the occurrence is patched with its own version.
	one := newEvent(t, api.EventInput{Title: "Just once", Start: "2026-10-26T12:00:00", End: "2026-10-26T13:00:00", TimeZone: "Europe/Prague"})
	occEtag := f.etag("O3")
	_, err = p.Update(ctx, cal, stored, calendar.Change{Scope: api.EditScopeThis, RecurrenceID: "2026-10-26T08:00:00Z", Event: one},
		calsync.WriteOptions{IfMatch: f.etag("M1")})
	if err != nil {
		t.Fatal(err)
	}
	if pt := f.reqs("PATCH", "/me/events/O3"); len(pt) != 1 || pt[0].IfMatch != occEtag || pt[0].Body["subject"] != "Just once" {
		t.Fatalf("patch this %+v", pt)
	}

	// thisAndFuture: the series ends the day before, the rest is new.
	fut := newEvent(t, api.EventInput{Title: "Later", Start: "2026-10-19T11:00:00", End: "2026-10-19T12:00:00", TimeZone: "Europe/Prague",
		Recurrence: &api.Recurrence{Frequency: api.FrequencyWeekly, ByDay: []string{"MO"}}})
	res, err = p.Update(ctx, cal, stored, calendar.Change{Scope: api.EditScopeThisAndFuture, RecurrenceID: "2026-10-19T08:00:00Z", Event: fut},
		calsync.WriteOptions{IfMatch: f.etag("M1")})
	if err != nil {
		t.Fatal(err)
	}
	pt = f.reqs("PATCH", "/me/events/M1")
	cut := pt[len(pt)-1].Body["recurrence"].(map[string]any)["range"].(map[string]any)
	if cut["type"] != "endDate" || cut["endDate"] != "2026-10-18" {
		t.Errorf("cut %v", cut)
	}
	if len(f.reqs("POST", "/me/calendars/CAL-A/events")) != 1 || len(res.Objects) != 2 || res.DroppedExceptions != 2 { // O2 and the O3 just made an exception
		t.Errorf("split result %+v (dropped %d)", res.Objects, res.DroppedExceptions)
	}

	// An attendee changes only their own fields.
	att := storedOf("S1", one, store.EventSingle)
	att.IAmOrganizer = false
	if _, err := p.Update(ctx, cal, att, calendar.Change{Scope: api.EditScopeAll, Event: one}, calsync.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	pt = f.reqs("PATCH", "/me/events/S1")
	if b := pt[0].Body; b["subject"] != nil || b["start"] != nil || b["isReminderOn"] == nil {
		t.Errorf("attendee patch %v", b)
	}

	// Gone on the server.
	if _, err := p.Update(ctx, cal, storedOf("NOPE", one, store.EventSingle), calendar.Change{Scope: api.EditScopeAll, Event: one}, calsync.WriteOptions{}); apiCodeOf(err) != api.CodeEventGone {
		t.Errorf("gone: %v", err)
	}
}

func TestCalendarDeleteAndRespond(t *testing.T) {
	f := newFakeCal(t)
	f.add("CAL-A", single("S1", calT0, time.Hour, nil))
	f.add("CAL-A", single("S2", calT0.Add(2*time.Hour), time.Hour, map[string]any{"isOrganizer": false, "allowNewTimeProposals": true}))
	f.add("CAL-A", single("S3", calT0.Add(4*time.Hour), time.Hour, map[string]any{"isOrganizer": false}))
	p := f.provider(CalendarProviderOptions{})
	ctx := context.Background()
	cal := storedCal("CAL-A", true)
	base := newEvent(t, api.EventInput{Title: "x", Start: "2026-10-12T10:00:00", End: "2026-10-12T11:00:00", TimeZone: "Europe/Prague"})

	org := storedOf("S1", base, store.EventSingle)
	org.AttendeeCount = 2
	res, err := p.Delete(ctx, cal, org, api.EditScopeAll, calsync.WriteOptions{Notify: true, Comment: "Sorry", IfMatch: f.etag("S1")})
	if err != nil {
		t.Fatal(err)
	}
	c := f.reqs("POST", "/me/events/S1/cancel")
	if len(c) != 1 || c[0].Body["comment"] != "Sorry" || len(res.Removed) != 1 || res.Invitations.Count != 2 {
		t.Fatalf("cancel %+v %+v", c, res)
	}
	if _, err := p.Delete(ctx, cal, org, api.EditScopeAll, calsync.WriteOptions{}); apiCodeOf(err) != api.CodeEventGone {
		t.Errorf("delete gone: %v", err)
	}

	att := storedOf("S2", base, store.EventSingle)
	att.IAmOrganizer, att.NewTimeOK = false, true
	ps, pe := calT0.Add(5*time.Hour), calT0.Add(6*time.Hour)
	res, err = p.Respond(ctx, cal, att, calendar.Response{Scope: api.EditScopeAll, Response: api.ResponseTentative, ProposedStart: &ps, ProposedEnd: &pe},
		calsync.WriteOptions{Notify: true, Comment: "Later?"})
	if err != nil {
		t.Fatal(err)
	}
	r := f.reqs("POST", "/me/events/S2/tentativelyAccept")
	if len(r) != 1 || r[0].Body["sendResponse"] != true || r[0].Body["comment"] != "Later?" {
		t.Fatalf("respond %+v", r)
	}
	pn := r[0].Body["proposedNewTime"].(map[string]any)
	if pn["start"].(map[string]any)["dateTime"] != "2026-10-12T13:00:00" || pn["end"].(map[string]any)["timeZone"] != "UTC" {
		t.Errorf("proposed %v", pn)
	}
	if res.Objects[0].Master.MyResponse != api.ResponseTentative || res.Invitations.Via != api.InvitationsViaServer {
		t.Errorf("result %+v", res)
	}
	// A new time where the organiser allows none, or with accept.
	no := storedOf("S3", base, store.EventSingle)
	no.IAmOrganizer = false
	if _, err := p.Respond(ctx, cal, no, calendar.Response{Response: api.ResponseDeclined, ProposedStart: &ps, ProposedEnd: &pe}, calsync.WriteOptions{Notify: true}); apiCodeOf(err) != api.CodeInvalidArgument {
		t.Errorf("no new time allowed: %v", err)
	}
	if _, err := p.Respond(ctx, cal, att, calendar.Response{Response: api.ResponseAccepted, ProposedStart: &ps, ProposedEnd: &pe}, calsync.WriteOptions{Notify: true}); apiCodeOf(err) != api.CodeInvalidArgument {
		t.Errorf("accept with a new time: %v", err)
	}
	// Decline without reply, then the attendee's delete with notify declines first.
	if _, err := p.Respond(ctx, cal, no, calendar.Response{Response: api.ResponseDeclined}, calsync.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	if d := f.reqs("POST", "/me/events/S3/decline"); len(d) != 1 || d[0].Body["sendResponse"] != false {
		t.Errorf("decline without reply %+v", d)
	}
	if _, err := p.Delete(ctx, cal, no, api.EditScopeAll, calsync.WriteOptions{Notify: true}); err != nil {
		t.Fatal(err)
	}
	if d := f.reqs("POST", "/me/events/S3/decline"); len(d) != 2 || len(f.reqs("DELETE", "/me/events/S3")) != 1 {
		t.Errorf("attendee delete: %d declines", len(d))
	}
}

func TestCalendarDeleteInstance(t *testing.T) {
	f := newFakeCal(t)
	seedSeries(f, "CAL-A")
	p := f.provider(CalendarProviderOptions{Window: func() calsync.Window { return calWin }})
	mev := newEvent(t, api.EventInput{Title: "Series", Start: "2026-10-12T10:00:00", End: "2026-10-12T11:00:00", TimeZone: "Europe/Prague",
		Recurrence: &api.Recurrence{Frequency: api.FrequencyWeekly, ByDay: []string{"MO"}}})
	res, err := p.Delete(context.Background(), storedCal("CAL-A", true), storedOf("M1", mev, store.EventMaster), api.EditScopeThis,
		calsync.WriteOptions{RecurrenceID: "2026-10-19T08:00:00Z", IfMatch: f.etag("M1")})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.reqs("DELETE", "/me/events/O2")) != 1 || len(res.Objects) != 1 || len(res.Objects[0].Occurrences) != 2 {
		t.Fatalf("instance delete %+v", res)
	}
}
