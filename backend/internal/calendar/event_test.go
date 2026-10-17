// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package calendar

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/calendar/ics"
	"github.com/schotek/malachi/backend/pkg/api"
)

var me = []string{"bo@example.org"}

func utc(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

// summary is what a round trip must keep of an event.
func summary(e *Event) string {
	if e == nil {
		return "<nil>"
	}
	at := make([]string, len(e.Attendees))
	for i, a := range e.Attendees {
		at[i] = fmt.Sprintf("%s|%s|%s|%s|%s", a.Address, a.Name, a.Role, a.Response, a.Type)
	}
	return fmt.Sprintf("uid=%s kind=%s rid=%s seq=%d title=%q loc=%q notes=%q start=%s end=%s allday=%v float=%v zone=%s/%s rrule=%s rd=%v ex=%v ok=%v status=%s show=%s priv=%v org=%v att=%v n=%d alarms=%v vc=%v",
		e.UID, e.Kind, e.RecurrenceID, e.Sequence, e.Title, e.Location, e.Notes, e.Start, e.End, e.AllDay, e.Floating,
		e.Zone, e.EndZone, e.RRule, e.RDates, e.EXDates, e.RecurOK, e.Status, e.ShowAs, e.Private, e.Organizer, at,
		e.AttendeeCount, e.Alarms, e.VideoCall)
}

func objSummary(o Object) string {
	s := []string{summary(o.Master)}
	for i := range o.Overrides {
		s = append(s, summary(&o.Overrides[i]))
	}
	return strings.Join(s, "\n")
}

// TestCorpus reads every fixture of testdata/calendar: nothing panics, an
// error is only ErrNoEvent or ErrTooBig, and writing an unchanged object
// back reads as the same object.
func TestCorpus(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join(corpusDir, "*.ics"))
	if len(files) < 50 {
		t.Fatalf("only %d fixtures", len(files))
	}
	for _, f := range files {
		name := filepath.Base(f)
		t.Run(name, func(t *testing.T) {
			obj, err := Parse(readFixture(t, name), FromOptions{Me: me})
			if err != nil {
				if !errors.Is(err, ErrNoEvent) && !errors.Is(err, ics.ErrTooBig) {
					t.Fatalf("error %v", err)
				}
				return
			}
			out, err := ToICS(obj, WriteOptions{})
			if err != nil {
				t.Fatalf("ToICS: %v", err)
			}
			back, err := Parse(out, FromOptions{Me: me})
			if err != nil {
				t.Fatalf("re-read: %v\n%s", err, out)
			}
			if a, b := objSummary(obj), objSummary(back); a != b {
				t.Errorf("round trip changed the object:\n%s\n---\n%s", a, b)
			}
			// The model alone (no raw) also round-trips what it holds.
			out2, err := ToICS(obj, WriteOptions{IgnoreRaw: true})
			if err != nil {
				t.Fatalf("ToICS without raw: %v", err)
			}
			if _, err := Parse(out2, FromOptions{Me: me}); err != nil {
				t.Fatalf("re-read of the model: %v\n%s", err, out2)
			}
		})
	}
}

func TestOutlookRequest(t *testing.T) {
	obj := parseFixture(t, "outlook-request.ics", FromOptions{Me: me})
	m := obj.Master
	if m.Kind != KindSingle || m.Title != "Quarterly review" || m.Location != "Meeting Room 2.04" {
		t.Fatalf("%s", summary(m))
	}
	if m.Zone != "Europe/Budapest" || !m.Start.Equal(utc("2026-10-22T08:00:00Z")) || !m.End.Equal(utc("2026-10-22T09:00:00Z")) {
		t.Errorf("times: %s %s %s", m.Zone, m.Start, m.End)
	}
	if m.ShowAs != api.ShowAsBusy || m.Status != api.EventStatusConfirmed {
		t.Errorf("showAs %s status %s", m.ShowAs, m.Status)
	}
	if m.Organizer == nil || m.Organizer.Address != "ada@example.org" || m.IAmOrganizer {
		t.Errorf("organizer %+v", m.Organizer)
	}
	if m.MyResponse != api.ResponseNeedsAction || !m.ResponseRequested || m.AttendeeCount != 2 || !m.Attendees[0].Me || m.Attendees[1].Role != api.AttendeeOptional {
		t.Errorf("attendees %+v resp %s", m.Attendees, m.MyResponse)
	}
	if !slices.Equal(m.Alarms, []int{15}) {
		t.Errorf("alarms %v", m.Alarms)
	}
	if !strings.HasPrefix(m.Notes, "Hi all,\n\nlet's go") {
		t.Errorf("notes %q", m.Notes)
	}
}

// TestRoundTripPreservesUnknown edits fields and checks that what the
// model does not hold survives the rewrite.
func TestRoundTripPreservesUnknown(t *testing.T) {
	obj := parseFixture(t, "outlook-request.ics", FromOptions{Me: me})
	obj.Master.Title = "Quarterly review (moved)"
	out, err := ToICS(obj, WriteOptions{Now: utc("2026-10-17T10:00:00Z")})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"X-MICROSOFT-CDO-OWNERAPPTID:2117123456", "PRIORITY:5", "X-MICROSOFT-ISRESPONSEREQUESTED:TRUE",
		"DTSTART;TZID=Central Europe Standard Time:20261022T100000", "TZID:Central Europe Standard Time",
		"SUMMARY:Quarterly review (moved)", "DESCRIPTION;LANGUAGE=en-US:", "DTSTAMP:20261017T100000Z", "TRIGGER;RELATED=START:-PT15M"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
	if strings.Contains(s, "METHOD:") {
		t.Error("a stored object keeps no METHOD")
	}
	// Moving the event rewrites the times in an IANA zone with a generated
	// VTIMEZONE; the Windows definition, no longer used, goes.
	obj.Master.Start, obj.Master.End = utc("2026-10-22T12:00:00Z"), utc("2026-10-22T13:00:00Z")
	obj.Master.Attendees = append(obj.Master.Attendees, api.Attendee{Address: "dee@example.org", Role: api.AttendeeRequired, Response: api.ResponseNeedsAction, Type: api.AttendeeTypePerson})
	obj.Master.Attendees[0].Response = api.ResponseAccepted
	obj.Master.AttendeeCount = 3
	obj.Master.Alarms = []int{30}
	out, err = ToICS(obj, WriteOptions{Now: utc("2026-10-17T10:00:00Z")})
	if err != nil {
		t.Fatal(err)
	}
	s = string(out)
	for _, want := range []string{"DTSTART;TZID=Europe/Budapest:20261022T140000", "TZID:Europe/Budapest", "BEGIN:STANDARD",
		"X-MICROSOFT-CDO-OWNERAPPTID", "mailto:dee@example.org", "TRIGGER:-PT30M"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
	if strings.Contains(s, "Central Europe Standard Time") || strings.Contains(s, "-PT15M") {
		t.Errorf("stale zone or alarm kept:\n%s", s)
	}
	back, err := Parse(out, FromOptions{Me: me})
	if err != nil {
		t.Fatal(err)
	}
	if b := back.Master; !b.Start.Equal(obj.Master.Start) || b.MyResponse != api.ResponseAccepted || b.AttendeeCount != 3 {
		t.Errorf("re-read %s", summary(b))
	}
	// The merged attendee keeps its own parameters (RSVP, CN).
	if !strings.Contains(s, "ATTENDEE;ROLE=REQ-PARTICIPANT;PARTSTAT=ACCEPTED;RSVP=TRUE;CN=Bo Tester:") {
		t.Errorf("merged attendee lost its parameters:\n%s", s)
	}
}

func TestOtherComponentsPreserved(t *testing.T) {
	raw := strings.Join([]string{"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//Test//EN", "X-WR-CALNAME:Work",
		"BEGIN:VEVENT", "UID:a@example.org", "DTSTAMP:20261016T080000Z", "DTSTART:20261020T090000Z", "DTEND:20261020T100000Z", "SUMMARY:A",
		"CATEGORIES:Work,Q4", "X-CUSTOM;X-P=1:keep me", "BEGIN:X-THING", "X-IN:1", "END:X-THING", "END:VEVENT",
		"BEGIN:VTODO", "UID:todo@example.org", "SUMMARY:Task", "END:VTODO",
		"BEGIN:VEVENT", "UID:other@example.org", "DTSTART:20261021T090000Z", "SUMMARY:Other", "END:VEVENT",
		"END:VCALENDAR", ""}, "\r\n")
	obj, err := Parse([]byte(raw), FromOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(obj.Problems, func(p Problem) bool { return p.Code == ProblemMixedUIDs }) {
		t.Errorf("problems %+v", obj.Problems)
	}
	obj.Master.Location = "Room 1"
	out, err := ToICS(obj, WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"X-WR-CALNAME:Work", "CATEGORIES:Work,Q4", "X-CUSTOM;X-P=1:keep me", "BEGIN:X-THING", "BEGIN:VTODO", "UID:other@example.org", "PRODID:-//Test//EN", "LOCATION:Room 1"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

func TestZoneResolution(t *testing.T) {
	cases := []struct {
		file, zone string
		problem    string
	}{
		{"tzid-windows.ics", "Europe/Budapest", ""},
		{"tzid-mozilla-prefix.ics", "Europe/Prague", ""},
		{"google-request.ics", "Europe/Prague", ""},
		{"vtimezone-hostile.ics", "Europe/Prague", ""},
	}
	for _, c := range cases {
		obj := parseFixture(t, c.file, FromOptions{})
		if obj.Master.Zone != c.zone || obj.Master.Floating {
			t.Errorf("%s: zone %q floating %v", c.file, obj.Master.Zone, obj.Master.Floating)
		}
	}
	unk := parseFixture(t, "tzid-unknown.ics", FromOptions{})
	if !unk.Master.Floating || !unk.Master.Start.Equal(utc("2026-10-20T09:00:00Z")) || !hasProblem(unk, ProblemZoneUnknown) {
		t.Errorf("unknown zone: %s %+v", summary(unk.Master), unk.Problems)
	}
	missing := parseFixture(t, "vtimezone-missing.ics", FromOptions{})
	if !missing.Master.Floating || !hasProblem(missing, ProblemZoneUnknown) {
		t.Errorf("missing VTIMEZONE: %s", summary(missing.Master))
	}

	vtz := func(id, std, dl string) string {
		lines := []string{"BEGIN:VTIMEZONE", "TZID:" + id, "BEGIN:STANDARD", "DTSTART:19701025T030000", "TZOFFSETFROM:" + dl, "TZOFFSETTO:" + std, "END:STANDARD"}
		if dl != "" {
			lines = append(lines, "BEGIN:DAYLIGHT", "DTSTART:19700329T020000", "TZOFFSETFROM:"+std, "TZOFFSETTO:"+dl, "END:DAYLIGHT")
		}
		return strings.Join(append(lines, "END:VTIMEZONE"), "\r\n")
	}
	ev := func(id, zone string) []byte {
		return []byte(strings.Join([]string{"BEGIN:VCALENDAR", "VERSION:2.0", zone, "BEGIN:VEVENT", "UID:z@example.org",
			"DTSTART;TZID=" + id + ":20261020T090000", "DTEND;TZID=" + id + ":20261020T100000", "END:VEVENT", "END:VCALENDAR", ""}, "\r\n"))
	}
	// Offsets matching a real zone pick it.
	m, err := Parse(ev("My Office", vtz("My Office", "+0100", "+0200")), FromOptions{})
	if err != nil || m.Master.Zone == "" || m.Master.Floating || !m.Master.Start.Equal(utc("2026-10-20T07:00:00Z")) || hasProblem(m, ProblemZoneFixed) {
		t.Errorf("matched: %v %s %+v", err, summary(m.Master), m.Problems)
	}
	// Offsets no zone has: a fixed whole-hour offset becomes Etc/GMT-7 …
	f, err := Parse(ev("Mars", vtz("Mars", "+0700", "+0900")), FromOptions{})
	if err != nil || f.Master.Zone != "Etc/GMT-7" || !f.Master.Start.Equal(utc("2026-10-20T02:00:00Z")) || !hasProblem(f, ProblemZoneFixed) {
		t.Fatalf("fixed: %v %s %+v", err, summary(f.Master), f.Problems)
	}
	out, err := ToICS(f, WriteOptions{IgnoreRaw: true})
	if err != nil || !strings.Contains(string(out), "DTSTART;TZID=Etc/GMT-7:20261020T090000") || !strings.Contains(string(out), "TZID:Etc/GMT-7") {
		t.Errorf("fixed written: %v\n%s", err, out)
	}
	// … and an odd offset is UTC.
	o, err := Parse(ev("Odd", vtz("Odd", "+0123", "")), FromOptions{})
	if err != nil || o.Master.Zone != "UTC" || !o.Master.Start.Equal(utc("2026-10-20T07:37:00Z")) {
		t.Errorf("odd fixed: %v %s", err, summary(o.Master))
	}
}

func hasProblem(o Object, code string) bool {
	return slices.ContainsFunc(o.Problems, func(p Problem) bool { return p.Code == code })
}

func TestTimeShapes(t *testing.T) {
	adm, err := Parse(readFixture(t, "all-day-datetime-mixed.ics"), FromOptions{UID: "adm@example.org"})
	if err != nil || !adm.Master.AllDay || adm.Master.StartDate != "2026-10-20" || adm.Master.EndDate != "2026-10-21" {
		t.Errorf("all-day with a date-time end: %v %s", err, summary(adm.Master))
	}
	adm2, err := Parse(readFixture(t, "all-day-datetime-mixed.ics"), FromOptions{UID: "adm-2@example.org"})
	if err != nil || adm2.Master.AllDay || !adm2.Master.Floating || !adm2.Master.Start.Equal(utc("2026-10-22T09:00:00Z")) {
		t.Errorf("lying VALUE: %v %s", err, summary(adm2.Master))
	}
	fum := parseFixture(t, "floating-and-utc-mixed.ics", FromOptions{})
	if !fum.Master.Floating || !fum.Master.End.Equal(utc("2026-10-20T10:00:00Z")) || !hasProblem(fum, ProblemMixedTimeKinds) {
		t.Errorf("floating with UTC end: %s", summary(fum.Master))
	}
	ebs := parseFixture(t, "end-before-start.ics", FromOptions{})
	if ebs.Master.End.Before(ebs.Master.Start) || !hasProblem(ebs, ProblemEndBeforeStart) {
		t.Errorf("end before start: %s", summary(ebs.Master))
	}
	dn := parseFixture(t, "duration-negative.ics", FromOptions{})
	if dn.Master.End.Before(dn.Master.Start) {
		t.Errorf("negative duration: %s", summary(dn.Master))
	}
}

func TestPeopleAndAlarms(t *testing.T) {
	anm := parseFixture(t, "attendee-non-mailto.ics", FromOptions{})
	if anm.Master.AttendeeCount != 6 || len(anm.Master.Attendees) != 1 || anm.Master.Attendees[0].Address != "Upper.Case@example.org" {
		t.Errorf("non-mailto: %d %+v", anm.Master.AttendeeCount, anm.Master.Attendees)
	}
	flood := parseFixture(t, "attendee-flood.ics", FromOptions{})
	if len(flood.Master.Attendees) > api.MaxEventAttendeesStored || flood.Master.AttendeeCount <= len(flood.Master.Attendees) {
		t.Errorf("flood: %d listed of %d", len(flood.Master.Attendees), flood.Master.AttendeeCount)
	}
	vep := parseFixture(t, "valarm-email-procedure.ics", FromOptions{})
	if !slices.Equal(vep.Master.Alarms, []int{1}) {
		t.Errorf("only the AUDIO alarm counts: %v", vep.Master.Alarms)
	}
	vap := parseFixture(t, "valarm-absolute-past.ics", FromOptions{})
	if len(vap.Master.Alarms) != 0 {
		t.Errorf("absolute and after-start alarms: %v", vap.Master.Alarms)
	}
	org := parseFixture(t, "organizer-spoofed.ics", FromOptions{})
	if org.Master.Organizer == nil || org.Master.Organizer.Address != "invoices@mail.example.org" {
		t.Errorf("organizer is the address, never the CN: %+v", org.Master.Organizer)
	}
	bidi := parseFixture(t, "bidi-summary.ics", FromOptions{})
	if strings.ContainsAny(bidi.Master.Title, "\u202A\u202B\u202C\u202D\u202E\u2066\u2067\u2068\u2069\u200E\u200F") {
		t.Errorf("bidi kept: %q", bidi.Master.Title)
	}
	au := parseFixture(t, "attach-uri.ics", FromOptions{})
	if !slices.Equal(au.Master.Attachments, []string{"agenda.pdf", "passwd", "internal"}) {
		t.Errorf("attachment names %q", au.Master.Attachments)
	}
}

func TestOverridesAndRecurrenceIDs(t *testing.T) {
	obj := parseFixture(t, "recurrence-id-tz-mismatch.ics", FromOptions{})
	if obj.Master.Kind != KindMaster || obj.Master.RRule != "FREQ=WEEKLY" || len(obj.Overrides) != 1 {
		t.Fatalf("%s", objSummary(obj))
	}
	if obj.Overrides[0].RecurrenceID != "2026-10-27T08:00:00Z" || obj.Master.Recurrence == nil || obj.Master.Recurrence.Exceptions != 1 {
		t.Errorf("override key %q rec %+v", obj.Overrides[0].RecurrenceID, obj.Master.Recurrence)
	}
	orphan := parseFixture(t, "override-without-master.ics", FromOptions{})
	if orphan.Master != nil || len(orphan.Overrides) != 1 || orphan.Overrides[0].RecurrenceID != "2026-10-21T09:00:00Z" {
		t.Errorf("orphan: %s", objSummary(orphan))
	}
	out, err := ToICS(orphan, WriteOptions{IgnoreRaw: true})
	if err != nil || !strings.Contains(string(out), "RECURRENCE-ID:20261021T090000Z") {
		t.Errorf("orphan written: %v\n%s", err, out)
	}
	of := parseFixture(t, "override-flood.ics", FromOptions{})
	if len(of.Overrides) == 0 || len(of.Overrides) > 500 {
		t.Errorf("override flood: %d", len(of.Overrides))
	}
	for _, name := range []string{"rrule-secondly.ics", "rrule-minutely.ics", "rrule-impossible-feb30.ics"} {
		o := parseFixture(t, name, FromOptions{})
		if o.Master == nil || o.Master.RecurOK || o.Master.Recurrence == nil || !o.Master.Recurrence.Complex {
			t.Errorf("%s: a refused rule shows the first instance only: %s", name, summary(o.Master))
		}
	}
}

func TestFromInput(t *testing.T) {
	org := &api.Address{Name: "Ada", Address: "ada@example.org"}
	in := api.EventInput{
		Title: "  Plan\u202E ", Start: "2026-10-22T10:00:00", End: "2026-10-22T11:00", TimeZone: "Europe/Prague",
		Location: "Room 1", Notes: "Agenda\nhttps://meet.google.com/abc", Alerts: []int{15, 5, 15},
		Recurrence: &api.Recurrence{Frequency: api.FrequencyWeekly, Interval: 1, ByDay: []string{"TH"}, Count: 5},
		Attendees:  []api.EventInputAttendee{{Name: "Bo", Address: "bo@example.org"}, {Address: "BO@example.org"}, {Address: "ada@example.org"}, {Address: "cy@example.org", Role: api.AttendeeOptional}},
	}
	ev, err := FromInput(in, InputOptions{UID: "new@example.org", Organizer: org, DefaultAlertMinutes: 10})
	if err != nil {
		t.Fatal(err)
	}
	if ev.Title != "Plan" || !ev.Start.Equal(utc("2026-10-22T08:00:00Z")) || ev.Zone != "Europe/Prague" || ev.Kind != KindMaster ||
		ev.RRule != "FREQ=WEEKLY;COUNT=5;BYDAY=TH" || !slices.Equal(ev.Alarms, []int{5, 15}) || len(ev.Attendees) != 2 ||
		!ev.IAmOrganizer || ev.VideoCall == nil || ev.VideoCall.Provider != api.VideoProviderMeet {
		t.Errorf("%s", summary(&ev))
	}
	out, err := ToICS(Object{Master: &ev}, WriteOptions{Now: utc("2026-10-16T08:00:00Z")})
	if err != nil {
		t.Fatal(err)
	}
	back, err := Parse(out, FromOptions{Me: []string{"ada@example.org"}})
	if err != nil || summary(back.Master) != summary(&ev) {
		t.Errorf("round trip:\n%s\n%s\n%s", summary(&ev), summary(back.Master), out)
	}
	if d, err := FromInput(api.EventInput{Title: "x", AllDay: true, StartDate: "2026-10-22", EndDate: "2026-10-23"}, InputOptions{UID: "d", DefaultAlertMinutes: -1}); err != nil || !d.AllDay || len(d.Alarms) != 0 {
		t.Errorf("all-day: %v %s", err, summary(&d))
	}
	bad := []api.EventInput{
		{Start: "2026-10-22T10:00:00", End: "2026-10-22T11:00:00"},
		{Start: "2026-10-22T10:00:00", End: "2026-10-22T09:00:00", TimeZone: "UTC"},
		{Start: "2026-10-22T10:00:00", End: "2026-10-22T11:00:00", TimeZone: "Central Europe Standard Time"},
		{AllDay: true, StartDate: "2026-10-22", EndDate: "2026-10-22"},
		{Title: strings.Repeat("a", MaxTitleBytes+1), Start: "2026-10-22T10:00:00", End: "2026-10-22T11:00:00", TimeZone: "UTC"},
		{Start: "2026-10-22T10:00:00", End: "2026-10-22T11:00:00", TimeZone: "UTC", Alerts: []int{1, 2, 3, 4, 5}},
		{Start: "2026-10-22T10:00:00", End: "2026-10-22T11:00:00", TimeZone: "UTC", Attendees: []api.EventInputAttendee{{Address: "not an address"}}},
		{Start: "2026-10-22T10:00:00", End: "2026-10-22T11:00:00", TimeZone: "UTC", VideoCall: &api.VideoCallInput{Mode: api.VideoCallLink, URL: "http://example.org"}},
		{Start: "2026-10-22T10:00:00", End: "2026-10-22T11:00:00", TimeZone: "UTC", Recurrence: &api.Recurrence{Frequency: api.FrequencyDaily, Interval: 100}},
		{Start: "2026-10-22T10:00:00", End: "2026-10-22T11:00:00", TimeZone: "UTC", Recurrence: &api.Recurrence{Frequency: api.FrequencyDaily, Interval: 1, Complex: true}},
	}
	for i, b := range bad {
		var apiErr *api.Error
		if _, err := FromInput(b, InputOptions{UID: "x"}); !errors.As(err, &apiErr) || apiErr.Code != api.CodeInvalidArgument {
			t.Errorf("bad input %d accepted: %v", i, err)
		}
	}
}

func weekly(t *testing.T) Object {
	t.Helper()
	raw := strings.Join([]string{"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//Test//EN",
		"BEGIN:VEVENT", "UID:w@example.org", "DTSTAMP:20261016T080000Z",
		"DTSTART;TZID=Europe/Prague:20261005T090000", "DTEND;TZID=Europe/Prague:20261005T093000", "RRULE:FREQ=WEEKLY;BYDAY=MO", "SUMMARY:Standup", "END:VEVENT",
		"BEGIN:VEVENT", "UID:w@example.org", "RECURRENCE-ID;TZID=Europe/Prague:20261012T090000",
		"DTSTART;TZID=Europe/Prague:20261012T100000", "DTEND;TZID=Europe/Prague:20261012T103000", "SUMMARY:Standup late", "END:VEVENT",
		"BEGIN:VEVENT", "UID:w@example.org", "RECURRENCE-ID;TZID=Europe/Prague:20261102T090000",
		"DTSTART;TZID=Europe/Prague:20261102T090000", "DTEND;TZID=Europe/Prague:20261102T093000", "SUMMARY:Standup Nov", "END:VEVENT",
		"END:VCALENDAR", ""}, "\r\n")
	obj, err := Parse([]byte(raw), FromOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return obj
}

func TestApplyChange(t *testing.T) {
	obj := weekly(t)
	// this: a new override.
	inst := cloneEvent(*obj.Master)
	inst.Title = "Moved"
	inst.Start, inst.End = utc("2026-10-19T09:00:00Z"), utc("2026-10-19T09:30:00Z")
	up, split, dropped, err := ApplyChange(obj, Change{Scope: api.EditScopeThis, RecurrenceID: "2026-10-19T07:00:00Z", Event: inst}, "")
	if err != nil || split != nil || dropped != 0 || len(up.Overrides) != 3 || up.Override("2026-10-19T07:00:00Z").Title != "Moved" {
		t.Fatalf("this: %v %d\n%s", err, dropped, objSummary(up))
	}
	if len(obj.Overrides) != 2 {
		t.Error("ApplyChange changed its input")
	}
	if _, _, _, err := ApplyChange(obj, Change{Scope: api.EditScopeThis, RecurrenceID: "2026-10-20T07:00:00Z", Event: inst}, ""); !errors.Is(err, ErrScope) {
		t.Errorf("an instance the rule has not: %v", err)
	}
	// thisAndFuture: cut before 26 Oct; the November override goes.
	ns := cloneEvent(*obj.Master)
	ns.Start, ns.End = utc("2026-10-26T09:00:00Z"), utc("2026-10-26T09:30:00Z")
	up, split, dropped, err = ApplyChange(obj, Change{Scope: api.EditScopeThisAndFuture, RecurrenceID: "2026-10-26T08:00:00Z", Event: ns}, "w2@example.org")
	if err != nil || split == nil || dropped != 1 || len(up.Overrides) != 1 || split.Master.UID != "w2@example.org" || split.Master.Sequence != 0 {
		t.Fatalf("thisAndFuture: %v %d %v", err, dropped, split)
	}
	if up.Master.RRule != "FREQ=WEEKLY;UNTIL=20261026T075959Z;BYDAY=MO" {
		t.Errorf("cut rule %q", up.Master.RRule)
	}
	occ, _, _ := Occurrences(up, Window{From: utc("2026-10-01T00:00:00Z"), To: utc("2026-12-01T00:00:00Z")}, nil, 0)
	if len(occ) != 3 {
		t.Errorf("cut series: %d instances", len(occ))
	}
	// all: a new time drops the overrides that are no longer instances.
	all := cloneEvent(*obj.Master)
	all.Start, all.End = utc("2026-10-06T07:00:00Z"), utc("2026-10-06T07:30:00Z")
	all.RRule = "FREQ=WEEKLY;BYDAY=TU"
	up, _, dropped, err = ApplyChange(obj, Change{Scope: api.EditScopeAll, Event: all}, "")
	if err != nil || dropped != 2 || len(up.Overrides) != 0 {
		t.Errorf("all: %v %d", err, dropped)
	}
	up, _, dropped, err = ApplyChange(obj, Change{Scope: api.EditScopeAll, Event: Event{Title: "Renamed", Start: obj.Master.Start, End: obj.Master.End, Zone: obj.Master.Zone}, KeepRecurrence: true}, "")
	if err != nil || dropped != 0 || len(up.Overrides) != 2 || up.Master.RRule != "FREQ=WEEKLY;BYDAY=MO" {
		t.Errorf("all keeping the rule: %v %d %s", err, dropped, objSummary(up))
	}
}

func TestApplyDelete(t *testing.T) {
	obj := weekly(t)
	up, err := ApplyDelete(obj, api.EditScopeThis, "2026-10-12T07:00:00Z")
	if err != nil || up == nil || len(up.Overrides) != 1 || len(up.Master.EXDates) != 1 {
		t.Fatalf("this: %v %v", err, up)
	}
	out, err := ToICS(*up, WriteOptions{})
	if err != nil || !strings.Contains(string(out), "EXDATE;TZID=Europe/Prague:20261012T090000") {
		t.Errorf("EXDATE written: %v\n%s", err, out)
	}
	if up, err := ApplyDelete(obj, api.EditScopeThisAndFuture, "2026-10-05T07:00:00Z"); up != nil || err != nil {
		t.Errorf("thisAndFuture at the first instance deletes all: %v %v", up, err)
	}
	if up, err := ApplyDelete(obj, api.EditScopeAll, ""); up != nil || err != nil {
		t.Errorf("all: %v %v", up, err)
	}
}

func TestSignificant(t *testing.T) {
	obj := weekly(t)
	a := cloneEvent(*obj.Master)
	b := cloneEvent(a)
	b.Title = "Renamed"
	if Significant(&a, &b) {
		t.Error("a title is not significant")
	}
	b.Location = "Room"
	if !Significant(&a, &b) {
		t.Error("a location is significant")
	}
}

func TestFromICSNeverPanics(t *testing.T) {
	inputs := []string{"", "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nEND:VEVENT\r\nEND:VCALENDAR", "BEGIN:VEVENT\r\nUID:x\r\nDTSTART:2026\r\n",
		"BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:x\r\nDTSTART:20261020T090000Z\r\nRECURRENCE-ID:bad\r\nEND:VEVENT\r\nEND:VCALENDAR"}
	for _, in := range inputs {
		obj, err := Parse([]byte(in), FromOptions{})
		if err == nil {
			_, _, _ = Occurrences(obj, Window{From: utc("2026-01-01T00:00:00Z"), To: utc("2027-01-01T00:00:00Z")}, nil, 0)
			if _, err := ToICS(obj, WriteOptions{}); err != nil {
				t.Errorf("%q: %v", in, err)
			}
		}
	}
}

// TestLongSeriesTimezone: a series that began long ago gets a VTIMEZONE
// covering its first year and the years around now (tz caps one at ten).
func TestLongSeriesTimezone(t *testing.T) {
	obj := parseLines(t, "BEGIN:VEVENT", "UID:old@example.org", "DTSTART;TZID=Europe/Prague:20120105T090000",
		"DTEND;TZID=Europe/Prague:20120105T100000", "RRULE:FREQ=WEEKLY", "RDATE;TZID=Europe/Prague:20120107T090000", "END:VEVENT")
	if !obj.Master.Recurrence.Complex || len(obj.Master.RDates) != 1 {
		t.Errorf("RDATE makes the rule complex: %+v", obj.Master.Recurrence)
	}
	obj.Master.Title = "Changed"
	obj.Master.Start = obj.Master.Start.Add(time.Hour)
	obj.Master.End = obj.Master.End.Add(time.Hour)
	out, err := ToICS(obj, WriteOptions{Now: utc("2026-10-16T08:00:00Z")})
	if err != nil {
		t.Fatal(err)
	}
	cal, err := ics.Parse(out)
	if err != nil || cal.HasProblems() {
		t.Fatalf("%v %+v", err, cal.Problems)
	}
	vtz := cal.Calendar().Children("VTIMEZONE")
	if len(vtz) != 1 {
		t.Fatalf("%d VTIMEZONEs", len(vtz))
	}
	var years []string
	for _, c := range vtz[0].Components {
		years = append(years, propValue(c, "DTSTART")[:4])
	}
	if !slices.Contains(years, "2012") || !slices.Contains(years, "2027") {
		t.Errorf("observance years %v", years)
	}
	// RDATEs are instants of their own: moving DTSTART does not move them.
	if !strings.Contains(string(out), "RDATE;TZID=Europe/Prague:20120107T090000") {
		t.Errorf("RDATE:\n%s", out)
	}
}
