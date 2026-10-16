// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"context"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/calendar"
	"github.com/schotek/malachi/backend/internal/calsync"
	"github.com/schotek/malachi/backend/pkg/api"
)

// allDayItem is an all-day event as the service sends it with the UTC
// Prefer header: start and end as given, zone names as given.
func allDayItem(id, start, end, zone string) map[string]any {
	return map[string]any{"id": id, "type": "singleInstance", "iCalUId": "uid-" + id, "subject": "Holiday " + id, "isAllDay": true,
		"start": map[string]any{"dateTime": start, "timeZone": "UTC"}, "end": map[string]any{"dateTime": end, "timeZone": "UTC"},
		"originalStartTimeZone": zone, "originalEndTimeZone": zone, "showAs": "free"}
}

func seedAllDay(f *fakeCal, cal string) {
	// Midnight UTC of the date.
	f.add(cal, allDayItem("AD-UTC", "2026-10-28T00:00:00.0000000", "2026-10-29T00:00:00.0000000", "Central Europe Standard Time"))
	// The zone's midnight in UTC: 22:00Z the day before (summer time) and
	// the exact live shape of late October.
	f.add(cal, allDayItem("AD-CEST", "2026-09-27T22:00:00.0000000", "2026-09-28T22:00:00.0000000", "Central Europe Standard Time"))
	f.add(cal, allDayItem("AD-SHIFT", "2026-10-27T22:00:00.0000000", "2026-10-28T22:00:00.0000000", "Central Europe Standard Time"))
	// Two days, no zone the provider knows.
	f.add(cal, allDayItem("AD-TWO", "2026-10-24T00:00:00.0000000", "2026-10-26T00:00:00.0000000", "Somewhere Odd Time"))
}

var allDayWant = map[string][2]api.Date{
	"AD-UTC":   {"2026-10-28", "2026-10-29"},
	"AD-CEST":  {"2026-09-28", "2026-09-29"},
	"AD-SHIFT": {"2026-10-28", "2026-10-29"},
	"AD-TWO":   {"2026-10-24", "2026-10-26"},
}

func checkAllDay(t *testing.T, how string, objs []calendar.Object) {
	t.Helper()
	found := 0
	for _, o := range objs {
		want, ok := allDayWant[o.RemoteID]
		if !ok {
			continue
		}
		found++
		m := o.Master
		if m == nil || !m.AllDay || m.StartDate != want[0] || m.EndDate != want[1] || m.Zone != "" {
			t.Errorf("%s %s: %+v", how, o.RemoteID, m)
			continue
		}
		s, _ := calendar.ParseDate(want[0])
		e, _ := calendar.ParseDate(want[1])
		if len(o.Occurrences) != 1 || !o.Occurrences[0].AllDay || !o.Occurrences[0].Start.Equal(s) || !o.Occurrences[0].End.Equal(e) {
			t.Errorf("%s %s occurrence %+v", how, o.RemoteID, o.Occurrences)
		}
	}
	if found != len(allDayWant) {
		t.Errorf("%s: %d all-day events", how, found)
	}
}

func TestCalendarAllDayShapes(t *testing.T) {
	win := calsync.Window{From: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC)}
	f := newFakeCal(t)
	f.calendars = []map[string]any{{"id": "CAL-A", "isDefaultCalendar": true}, {"id": "CAL-H", "canEdit": false}}
	seedAllDay(f, "CAL-A")
	p := f.provider(CalendarProviderOptions{})
	// The list mode over its own fake (item ids are per service).
	fl := newFakeCal(t)
	fl.calendars = f.calendars
	fl.noDelta["CAL-H"] = true
	seedAllDay(fl, "CAL-H")
	pl := fl.provider(CalendarProviderOptions{})

	sink := &recSink{}
	if _, err := p.Sync(context.Background(), storedCal("CAL-A", true), win, sink); err != nil {
		t.Fatal(err)
	}
	checkAllDay(t, "delta", sink.upserts)

	sink = &recSink{}
	ro := storedCal("CAL-H", false)
	ro.Access = api.CalendarAccessRead
	st, err := pl.Sync(context.Background(), ro, win, sink)
	if err != nil || st.DeltaMode != DeltaModeList {
		t.Fatalf("list: %v %+v", err, st)
	}
	checkAllDay(t, "list", sink.upserts)

	objs, err := p.RangeObjects(context.Background(), storedCal("CAL-A", true), win.From, win.To)
	if err != nil {
		t.Fatal(err)
	}
	checkAllDay(t, "range", objs)

	// A series instance of an all-day series is all-day even when its
	// item leaves isAllDay out.
	m := master("YM", time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC), 24*time.Hour, map[string]any{"type": "absoluteYearly", "interval": 1, "month": 10, "dayOfMonth": 28})
	m["isAllDay"] = true
	inst := occurrence("YO", "YM", time.Date(2026, 10, 27, 23, 0, 0, 0, time.UTC), 24*time.Hour, "occurrence")
	inst["originalStartTimeZone"] = "Central Europe Standard Time"
	f.add("CAL-A", m)
	f.add("CAL-A", inst)
	sink = &recSink{}
	if _, err := p.Sync(context.Background(), storedCal("CAL-A", true), win, sink); err != nil {
		t.Fatal(err)
	}
	ser := sink.byID("YM")
	if ser == nil || !ser.Master.AllDay || len(ser.Occurrences) != 1 || !ser.Occurrences[0].AllDay || ser.Occurrences[0].Key != "2026-10-28" ||
		!ser.Occurrences[0].Start.Equal(time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("all-day series %+v", ser)
	}
}

func TestCalendarLocationDeduplicated(t *testing.T) {
	ge := &graphEvent{
		Location: &graphLocation{DisplayName: "Schůzka Microsoft Teams; Zasedací místnost EUROMILIONY (2.336) - TV, VK; zasedací MÍSTNOST euromiliony (2.336) - tv, vk"},
		Locations: []graphLocation{{DisplayName: "Schůzka Microsoft Teams"}, {DisplayName: "Zasedací místnost EUROMILIONY (2.336) - TV, VK"},
			{DisplayName: "Room 2"}, {DisplayName: " "}},
	}
	if got := locationOf(ge); got != "Schůzka Microsoft Teams; Zasedací místnost EUROMILIONY (2.336) - TV, VK; Room 2" {
		t.Errorf("location %q", got)
	}
	if got := locationOf(&graphEvent{Locations: []graphLocation{{DisplayName: "A"}, {DisplayName: "a"}}}); got != "A" {
		t.Errorf("locations only %q", got)
	}
}

func TestCalendarSharedResponses(t *testing.T) {
	f := newFakeCal(t)
	f.calendars = []map[string]any{{"id": "CAL-A", "isDefaultCalendar": true}, {"id": "CAL-BOSS", "canEdit": true}}
	boss := map[string]any{"emailAddress": map[string]any{"address": "boss@contoso.invalid"}}
	// The boss's own meeting: isOrganizer and responseStatus are the boss's.
	f.add("CAL-BOSS", single("B1", calT0, time.Hour, map[string]any{"isOrganizer": true, "organizer": boss,
		"responseStatus": map[string]any{"response": "organizer"},
		"attendees":      []any{map[string]any{"type": "required", "emailAddress": map[string]any{"address": "jana@example.org"}}}}))
	// The boss is invited; the user is not.
	f.add("CAL-BOSS", single("B2", calT0.Add(2*time.Hour), time.Hour, map[string]any{"isOrganizer": false,
		"organizer": map[string]any{"emailAddress": map[string]any{"address": "jana@example.org"}}, "responseRequested": true, "allowNewTimeProposals": true,
		"responseStatus": map[string]any{"response": "accepted"},
		"attendees":      []any{map[string]any{"type": "required", "status": map[string]any{"response": "accepted"}, "emailAddress": map[string]any{"address": "boss@contoso.invalid"}}}}))
	// Both are invited: the user's own answer counts.
	f.add("CAL-BOSS", single("B3", calT0.Add(4*time.Hour), time.Hour, map[string]any{"isOrganizer": false,
		"organizer": map[string]any{"emailAddress": map[string]any{"address": "jana@example.org"}}, "responseRequested": true,
		"responseStatus": map[string]any{"response": "accepted"},
		"attendees": []any{
			map[string]any{"type": "required", "status": map[string]any{"response": "accepted"}, "emailAddress": map[string]any{"address": "boss@contoso.invalid"}},
			map[string]any{"type": "optional", "status": map[string]any{"response": "tentativelyAccepted"}, "emailAddress": map[string]any{"address": "ME@contoso.invalid"}},
		}}))
	p := f.provider(CalendarProviderOptions{})
	cal := storedCal("CAL-BOSS", false)
	cal.Access, cal.OwnerAddress = api.CalendarAccessWrite, "boss@contoso.invalid"
	sink := &recSink{}
	if _, err := p.Sync(context.Background(), cal, calWin, sink); err != nil {
		t.Fatal(err)
	}
	if e := sink.byID("B1").Master; e.IAmOrganizer || e.MyResponse != "" || e.ResponseRequested {
		t.Errorf("boss's meeting %+v", e)
	}
	if e := sink.byID("B2").Master; e.MyResponse != "" || e.ResponseRequested || e.NewTimeOK || e.IAmOrganizer {
		t.Errorf("boss invited %+v", e)
	}
	if e := sink.byID("B3").Master; e.MyResponse != api.ResponseTentative || !e.ResponseRequested {
		t.Errorf("both invited %+v", e)
	}
	// The same item in the user's own calendar keeps its responseStatus.
	f.add("CAL-A", single("A2", calT0, time.Hour, map[string]any{"isOrganizer": false, "responseStatus": map[string]any{"response": "accepted"},
		"attendees": []any{map[string]any{"type": "required", "emailAddress": map[string]any{"address": "x@example.org"}}}}))
	sink = &recSink{}
	if _, err := p.Sync(context.Background(), storedCal("CAL-A", true), calWin, sink); err != nil {
		t.Fatal(err)
	}
	if e := sink.byID("A2").Master; e.MyResponse != api.ResponseAccepted {
		t.Errorf("own calendar %+v", e)
	}
}

func TestCalendarProviderIsVersioned(t *testing.T) {
	var prov calsync.Provider = NewCalendarProvider(nil, CalendarProviderOptions{})
	v, ok := prov.(calsync.Versioned)
	if !ok || v.DataVersion() != graphDataVersion || graphDataVersion < 2 {
		t.Fatalf("versioned %v", ok)
	}
}
