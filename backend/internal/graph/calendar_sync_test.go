// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/calendar"
	"github.com/schotek/malachi/backend/internal/calsync"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

var (
	calT0  = time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC) // a Monday, 10:00 in Prague
	calWin = calsync.Window{From: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}
)

func storedCal(remote string, def bool) calsync.StoredCalendar {
	return store.Calendar{ID: "cal_" + remote, AccountID: "acc_1", RemoteID: remote, ServerDefault: def, Access: api.CalendarAccessOwner}
}

func apiCodeOf(err error) api.ErrorCode {
	var ae *api.Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	return 0
}

func TestCalendarsListing(t *testing.T) {
	f := newFakeCal(t)
	f.calendars = []map[string]any{
		{"id": "CAL-A", "name": "Kalendář", "isDefaultCalendar": true, "canEdit": true, "color": "lightRed", "hexColor": "#D13438",
			"owner": map[string]string{"name": "Me", "address": "ME@contoso.invalid"}, "allowedOnlineMeetingProviders": []string{"teamsForBusiness"}},
		{"id": "CAL-B", "name": "Second‮", "canEdit": true, "color": "lightBlue", "hexColor": "",
			"owner": map[string]string{"name": "Me", "address": "me@contoso.invalid"}},
		{"id": "CAL-BDAY", "name": "Narozeniny", "canEdit": false, "color": "auto", "hexColor": ""},
		{"id": "CAL-SHARED", "name": "Boss", "canEdit": true, "color": "maxColor", "hexColor": "#12",
			"owner": map[string]string{"name": "Boss", "address": "boss@contoso.invalid"}},
	}
	p := f.provider(CalendarProviderOptions{})
	cals, err := p.Calendars(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(cals) != 4 {
		t.Fatalf("calendars %d", len(cals))
	}
	a, b, bday, shared := cals[0], cals[1], cals[2], cals[3]
	if a.Color != "#d13438" || !a.IsDefault || a.Access != api.CalendarAccessOwner || a.Owner != nil || len(a.OnlineMeetingProviders) != 1 {
		t.Errorf("default calendar %+v", a)
	}
	if b.Color != "#0078d4" || b.Name != "Second" {
		t.Errorf("palette fallback / cleaned name %+v", b)
	}
	if bday.Color != "" || bday.Access != api.CalendarAccessRead || bday.Kind != api.CalendarKindEvents {
		t.Errorf("birthdays %+v", bday)
	}
	if shared.Color != "" || shared.Access != api.CalendarAccessWrite || shared.Owner == nil || shared.Owner.Address != "boss@contoso.invalid" {
		t.Errorf("shared %+v", shared)
	}
	feat := p.Features()
	if !feat.OnlineMeetings || !feat.FreeBusy || !feat.ProposeNewTime || feat.Invitations != api.InvitationsViaServer {
		t.Errorf("features %+v", feat)
	}
	if f.provider(CalendarProviderOptions{Personal: true}).Features().FreeBusy {
		t.Error("a personal account has no free/busy")
	}
}

func TestCalendarConsentRequired(t *testing.T) {
	for _, tc := range []struct {
		code   string
		signIn bool
	}{{"ErrorAccessDenied", true}, {"Authorization_RequestDenied", false}} {
		f := newFakeCal(t)
		f.fail["GET /me/calendars"] = fakeFailure{403, tc.code}
		_, err := f.provider(CalendarProviderOptions{SignIn: tc.signIn}).Calendars(context.Background())
		var ae *api.Error
		if !errors.As(err, &ae) || ae.Code != api.CodeCalendarConsentRequired {
			t.Fatalf("%s: %v", tc.code, err)
		}
		d, ok := ae.Data.(api.CalendarConsentRequiredData)
		if !ok || d.AccountID != "acc_1" || d.SignIn != tc.signIn {
			t.Errorf("%s data %+v", tc.code, ae.Data)
		}
	}
	// A sync pass answers the same way.
	f := newFakeCal(t)
	f.fail["GET /me/calendarView/delta"] = fakeFailure{403, "ErrorAccessDenied"}
	f.calendars = []map[string]any{{"id": "CAL-A", "isDefaultCalendar": true}}
	_, err := f.provider(CalendarProviderOptions{}).Sync(context.Background(), storedCal("CAL-A", true), calWin, &recSink{})
	if apiCodeOf(err) != api.CodeCalendarConsentRequired {
		t.Fatalf("sync: %v", err)
	}
}

func seedSeries(f *fakeCal, cal string) {
	f.add(cal, master("M1", calT0, time.Hour, map[string]any{"type": "weekly", "interval": 1, "daysOfWeek": []string{"monday"},
		"firstDayOfWeek": "monday", "index": "first", "dayOfMonth": 0, "month": 0}))
	f.add(cal, occurrence("O1", "M1", calT0, time.Hour, "occurrence"))
	ex := occurrence("O2", "M1", calT0.AddDate(0, 0, 7).Add(2*time.Hour), time.Hour, "exception")
	ex["originalStart"] = calT0.AddDate(0, 0, 7).Format(time.RFC3339)
	ex["subject"] = "Moved"
	f.add(cal, ex)
	f.add(cal, occurrence("O3", "M1", calT0.AddDate(0, 0, 14), time.Hour, "occurrence"))
}

func TestCalendarSyncDeltaFirstPassAndIncrement(t *testing.T) {
	f := newFakeCal(t)
	f.calendars = []map[string]any{{"id": "CAL-A", "isDefaultCalendar": true}}
	f.add("CAL-A", single("S1", calT0.Add(24*time.Hour), 30*time.Minute, map[string]any{
		"body":          map[string]any{"contentType": "html", "content": "<p>Agenda <b>here</b></p><script>x()</script>"},
		"onlineMeeting": map[string]any{"joinUrl": "https://teams.microsoft.com/l/meetup-join/abc"},
		"attendees": []any{
			map[string]any{"type": "required", "status": map[string]any{"response": "accepted"}, "emailAddress": map[string]any{"name": "Jana", "address": "jana@example.org"}},
			map[string]any{"type": "optional", "emailAddress": map[string]any{"address": "me@contoso.invalid"}},
			map[string]any{"type": "resource", "emailAddress": map[string]any{"name": "Room", "address": "room@example.org"}},
		},
		"isReminderOn": true, "reminderMinutesBeforeStart": 15, "sensitivity": "private",
	}))
	f.add("CAL-A", single("S2", calT0.Add(48*time.Hour), time.Hour, map[string]any{
		"isOrganizer": false, "responseStatus": map[string]any{"response": "notResponded"}, "responseRequested": true,
		"allowNewTimeProposals": true, "organizer": map[string]any{"emailAddress": map[string]any{"address": "boss@example.org"}},
		"bodyPreview": "Only a preview",
	}))
	f.add("CAL-A", map[string]any{"id": "AD1", "type": "singleInstance", "iCalUId": "uid-AD1", "subject": "Holiday", "isAllDay": true,
		"start": map[string]any{"dateTime": "2026-10-20T00:00:00.0000000", "timeZone": "UTC"},
		"end":   map[string]any{"dateTime": "2026-10-22T00:00:00.0000000", "timeZone": "UTC"}})
	seedSeries(f, "CAL-A")

	p := f.provider(CalendarProviderOptions{})
	sink := &recSink{}
	st, err := p.Sync(context.Background(), storedCal("CAL-A", true), calWin, sink)
	if err != nil {
		t.Fatal(err)
	}
	if st.DeltaMode != DeltaModeDelta || !st.Full || !strings.Contains(st.Token, "/deltastate?") {
		t.Fatalf("state %+v", st)
	}
	deltas := f.reqs("GET", "/me/calendarView/delta")
	if len(deltas) != 1 || !strings.Contains(deltas[0].Prefer, `outlook.timezone="UTC"`) || !strings.Contains(deltas[0].Prefer, "odata.maxpagesize=200") ||
		!strings.Contains(deltas[0].Query, "startDateTime=2026-10-01T00%3A00%3A00Z") {
		t.Fatalf("delta request %+v", deltas)
	}
	if len(f.reqs("GET", "/next")) == 0 {
		t.Error("paging not exercised")
	}
	if len(f.reqs("GET", "/me/events/M1")) != 1 {
		t.Errorf("master fetches %d", len(f.reqs("GET", "/me/events/M1")))
	}
	if len(sink.upserts) != 4 {
		t.Fatalf("upserts %d", len(sink.upserts))
	}
	// CLDR maps "Central Europe Standard Time" to its territory-001
	// zone, Europe/Budapest, which has Prague's rules.
	s1 := sink.byID("S1")
	m := s1.Master
	if m.Zone != "Europe/Budapest" || !m.Start.Equal(calT0.Add(24*time.Hour)) || !m.End.Equal(calT0.Add(24*time.Hour+30*time.Minute)) {
		t.Errorf("S1 times %v %v %s", m.Start, m.End, m.Zone)
	}
	if strings.Contains(m.Notes, "<") || !strings.Contains(m.Notes, "Agenda") || strings.Contains(m.Notes, "x()") {
		t.Errorf("notes %q", m.Notes)
	}
	if m.VideoCall == nil || m.VideoCall.Provider != api.VideoProviderTeams {
		t.Errorf("video %+v", m.VideoCall)
	}
	if m.AttendeeCount != 3 || len(m.Attendees) != 3 || m.Attendees[0].Response != api.ResponseAccepted || !m.Attendees[1].Me ||
		m.Attendees[1].Role != api.AttendeeOptional || m.Attendees[2].Type != api.AttendeeTypeResource {
		t.Errorf("attendees %+v", m.Attendees)
	}
	if len(m.Alarms) != 1 || m.Alarms[0] != 15 || !m.Private || !m.IAmOrganizer || m.MyResponse != "" {
		t.Errorf("alarms/private/organiser %+v", m)
	}
	if s1.ETag != f.etag("S1") || len(s1.Occurrences) != 1 || s1.Occurrences[0].Override != -1 || s1.Occurrences[0].RemoteID != "S1" {
		t.Errorf("S1 object %+v", s1)
	}
	s2 := sink.byID("S2").Master
	if s2.MyResponse != api.ResponseNeedsAction || !s2.ResponseRequested || !s2.NewTimeOK || s2.Notes != "Only a preview" ||
		s2.Organizer == nil || s2.Organizer.Address != "boss@example.org" {
		t.Errorf("S2 %+v", s2)
	}
	ad := sink.byID("AD1").Master
	if !ad.AllDay || ad.StartDate != "2026-10-20" || ad.EndDate != "2026-10-22" || ad.Zone != "" {
		t.Errorf("all-day %+v", ad)
	}
	ser := sink.byID("M1")
	if ser == nil || ser.Master == nil || ser.Master.Kind != calendar.KindMaster || ser.Master.RRule != "FREQ=WEEKLY;BYDAY=MO" ||
		ser.Master.Recurrence == nil || ser.Master.Recurrence.Exceptions != 1 {
		t.Fatalf("series %+v", ser)
	}
	if len(ser.Occurrences) != 3 || len(ser.Overrides) != 1 {
		t.Fatalf("series expansion %d occurrences, %d overrides", len(ser.Occurrences), len(ser.Overrides))
	}
	ov := ser.Overrides[0]
	if ov.UID != "uid-M1" || ov.Title != "Moved" || ov.RecurrenceID != "2026-10-19T08:00:00Z" || ov.Zone != "Europe/Budapest" {
		t.Errorf("override %+v", ov)
	}
	if o := ser.Occurrences[1]; o.Override != 0 || o.RemoteID != "O2" || !o.Start.Equal(calT0.AddDate(0, 0, 7).Add(2*time.Hour)) || !o.Series {
		t.Errorf("exception occurrence %+v", o)
	}
	if o := ser.Occurrences[0]; o.Override != -1 || o.Key != "2026-10-12T08:00:00Z" || o.RemoteID != "O1" {
		t.Errorf("plain occurrence %+v", o)
	}

	// The next pass resumes: an edited single, a removed one, an edited
	// occurrence (its series is fetched with instances).
	f.edit("S2", map[string]any{"subject": "Renamed"})
	f.remove("AD1")
	f.edit("O3", map[string]any{"subject": "Changed", "type": "exception"})
	known := Known{Objects: map[string]string{"M1": ser.ETag}, Instances: map[string]string{"O1": "M1", "O2": "M1", "O3": "M1"}}
	p.opts.Known = func(context.Context, calsync.StoredCalendar) (Known, error) { return known, nil }
	cal := storedCal("CAL-A", true)
	cal.SyncToken, cal.SyncMode = st.Token, st.DeltaMode
	sink2 := &recSink{}
	st2, err := p.Sync(context.Background(), cal, calWin, sink2)
	if err != nil {
		t.Fatal(err)
	}
	if st2.Full || st2.Token == st.Token || st2.DeltaMode != DeltaModeDelta {
		t.Fatalf("incremental state %+v", st2)
	}
	if len(sink2.removes) != 1 || sink2.removes[0] != "AD1" {
		t.Errorf("removes %v", sink2.removes)
	}
	if s := sink2.byID("S2"); s == nil || s.Master.Title != "Renamed" {
		t.Errorf("edited single %+v", s)
	}
	ser2 := sink2.byID("M1")
	if ser2 == nil || len(ser2.Occurrences) != 3 || len(ser2.Overrides) != 2 || len(f.reqs("GET", "/me/events/M1/instances")) != 1 {
		t.Fatalf("series refetched %+v", ser2)
	}
	if sink2.byID("S1") != nil {
		t.Error("unchanged single upserted again")
	}
	// A removed occurrence of a known series refetches its series.
	f.remove("O1")
	cal.SyncToken = st2.Token
	sink3 := &recSink{}
	if _, err := p.Sync(context.Background(), cal, calWin, sink3); err != nil {
		t.Fatal(err)
	}
	if s := sink3.byID("M1"); s == nil || len(s.Occurrences) != 2 || len(sink3.removes) != 0 {
		t.Errorf("removed occurrence: %+v removes %v", s, sink3.removes)
	}
}

func TestCalendarSyncPerCalendarDeltaAndStaleToken(t *testing.T) {
	f := newFakeCal(t)
	f.calendars = []map[string]any{{"id": "CAL-A", "isDefaultCalendar": true}, {"id": "CAL-B"}}
	f.add("CAL-B", single("B1", calT0, time.Hour, nil))
	p := f.provider(CalendarProviderOptions{})
	st, err := p.Sync(context.Background(), storedCal("CAL-B", false), calWin, &recSink{})
	if err != nil || len(f.reqs("GET", "/me/calendars/CAL-B/calendarView/delta")) != 1 {
		t.Fatalf("per-calendar delta: %v %+v", err, st)
	}
	// The service no longer takes the token: the pass starts over.
	cal := storedCal("CAL-B", false)
	cal.SyncToken, cal.SyncMode = f.srv.URL+"/v1.0/deltastate?s=gone", DeltaModeDelta
	f.fail["GET /deltastate"] = fakeFailure{410, "SyncStateNotFound"}
	sink := &recSink{}
	st, err = p.Sync(context.Background(), cal, calWin, sink)
	if err != nil || !st.Full || sink.byID("B1") == nil {
		t.Fatalf("resync: %v %+v %d", err, st, len(sink.upserts))
	}
	// A stored token on another host is never followed.
	cal.SyncToken = "https://evil.example/delta?x=1"
	st, err = p.Sync(context.Background(), cal, calWin, &recSink{})
	if err != nil || !st.Full {
		t.Fatalf("foreign token: %v %+v", err, st)
	}
}

func TestCalendarSyncListFallback(t *testing.T) {
	f := newFakeCal(t)
	f.calendars = []map[string]any{{"id": "CAL-A", "isDefaultCalendar": true}, {"id": "CAL-H", "canEdit": false}}
	f.noDelta["CAL-H"] = true
	f.add("CAL-H", single("H1", calT0, 24*time.Hour, nil))
	f.add("CAL-H", single("H2", calT0.AddDate(0, 0, 1), time.Hour, nil))
	seedSeries(f, "CAL-H")
	p := f.provider(CalendarProviderOptions{})
	sink := &recSink{}
	st, err := p.Sync(context.Background(), storedCal("CAL-H", false), calWin, sink)
	if err != nil {
		t.Fatal(err)
	}
	if st.DeltaMode != DeltaModeList || !st.Full || st.Token != "" || len(sink.upserts) != 3 {
		t.Fatalf("list fallback %+v, %d upserts", st, len(sink.upserts))
	}
	if s := sink.byID("M1"); s == nil || len(s.Occurrences) != 3 || s.Master == nil {
		t.Fatalf("series in list mode %+v", s)
	}
	// With what is stored known: only changes, and removals made here.
	known := Known{Objects: map[string]string{}, Instances: map[string]string{"O1": "M1", "O2": "M1", "O3": "M1"}}
	for _, o := range sink.upserts {
		known.Objects[o.RemoteID] = o.ETag
	}
	known.Objects["GONE"] = `W/"x"`
	p.opts.Known = func(context.Context, calsync.StoredCalendar) (Known, error) { return known, nil }
	f.edit("H2", map[string]any{"subject": "New"})
	cal := storedCal("CAL-H", false)
	cal.SyncMode = st.DeltaMode
	deltasBefore := len(f.reqs("GET", "calendarView/delta"))
	sink2 := &recSink{}
	st2, err := p.Sync(context.Background(), cal, calWin, sink2)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.reqs("GET", "calendarView/delta")) != deltasBefore {
		t.Error("list mode asked for delta again")
	}
	if st2.Full || len(sink2.upserts) != 1 || sink2.upserts[0].RemoteID != "H2" || len(sink2.removes) != 1 || sink2.removes[0] != "GONE" {
		t.Fatalf("list increment %+v upserts %d removes %v", st2, len(sink2.upserts), sink2.removes)
	}
}

func TestCalendarRange(t *testing.T) {
	f := newFakeCal(t)
	f.calendars = []map[string]any{{"id": "CAL-A", "isDefaultCalendar": true}}
	f.add("CAL-A", single("S1", calT0, time.Hour, nil))
	seedSeries(f, "CAL-A")
	p := f.provider(CalendarProviderOptions{})
	occs, err := p.Range(context.Background(), storedCal("CAL-A", true), calWin.From, calWin.To)
	if err != nil {
		t.Fatal(err)
	}
	if len(occs) != 4 || !occs[0].Start.Equal(calT0) {
		t.Fatalf("range %+v", occs)
	}
	objs, _ := p.RangeObjects(context.Background(), storedCal("CAL-A", true), calWin.From, calWin.To)
	for _, o := range objs {
		for _, occ := range o.Occurrences {
			if o.EventOf(occ) == nil {
				t.Errorf("occurrence without its event in %s", o.RemoteID)
			}
		}
	}
	if len(f.reqs("GET", "/me/events/")) != 0 {
		t.Error("range fetched masters")
	}
}

func TestParseGraphTimeShapes(t *testing.T) {
	for _, tc := range []struct {
		in   dateTimeTimeZone
		want time.Time
	}{
		{dateTimeTimeZone{"2022-04-12T08:00:00.0000000", "UTC"}, time.Date(2022, 4, 12, 8, 0, 0, 0, time.UTC)},
		{dateTimeTimeZone{"2022-04-12T08:00:00.1234567", "UTC"}, time.Date(2022, 4, 12, 8, 0, 0, 0, time.UTC)},
		{dateTimeTimeZone{"2022-07-12T10:00:00.0000000", "Central Europe Standard Time"}, time.Date(2022, 7, 12, 8, 0, 0, 0, time.UTC)},
		{dateTimeTimeZone{"2022-01-12T10:00:00", "Europe/Prague"}, time.Date(2022, 1, 12, 9, 0, 0, 0, time.UTC)},
	} {
		_, got, ok := parseGraphTime(&tc.in)
		if !ok || !got.Equal(tc.want) {
			t.Errorf("%+v: %v %v", tc.in, got, ok)
		}
	}
	for _, bad := range []string{"", "yesterday", "0001-01-01T00:00:00", strings.Repeat("9", 100)} {
		if _, _, ok := parseGraphTime(&dateTimeTimeZone{bad, "UTC"}); ok {
			t.Errorf("%q accepted", bad)
		}
	}
	if zoneName("Central Europe Standard Time") != "Europe/Budapest" || zoneName("tzone://Microsoft/Utc") != "UTC" || zoneName("Nowhere/Land") != "" {
		t.Error("zone names")
	}
}
