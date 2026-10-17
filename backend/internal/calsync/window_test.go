// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package calsync

import (
	"context"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

func TestWindowFor(t *testing.T) {
	prague, err := time.LoadLocation("Europe/Prague")
	if err != nil {
		t.Skip("no tzdata")
	}
	w := WindowFor(time.Date(2026, 10, 16, 23, 30, 0, 0, time.UTC), 30, prague) // 01:30 on the 17th in Prague
	if got := w.From.In(prague); got.Format(time.DateTime) != "2026-09-17 00:00:00" {
		t.Errorf("from %s", got)
	}
	if got := w.To.In(prague); got.Format(time.DateTime) != "2027-11-21 00:00:00" {
		t.Errorf("to %s", got)
	}
	if d := WindowFor(t0, 0, nil); !d.From.Equal(time.Date(2025, 10, 16, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("default keepDays %s", d.From)
	}
	a := caldavAccount("x")
	if KeepDays(a) != api.DefaultCalendarKeepDays {
		t.Errorf("keepDays default")
	}
	a.Config.Calendar = &api.CalendarConfig{KeepDays: 90}
	if KeepDays(a) != 90 {
		t.Errorf("keepDays 90")
	}
	if f, to := windowDates(WindowFor(t0, 30, time.UTC), time.UTC); f != "2026-09-16" || to != "2027-11-20" {
		t.Errorf("dates %s %s", f, to)
	}
}

func TestNeedsRebaseline(t *testing.T) {
	w := WindowFor(t0, 365, time.UTC) // from 2025-10-16
	for stored, want := range map[string]bool{
		"":           false,
		"2025-10-16": false,
		"2025-10-09": false, // exactly 7 days behind
		"2025-10-08": true,
		"2026-07-18": true, // keepDays 90 → 365: far ahead
		"garbage":    true,
	} {
		if got := needsRebaseline(stored, w, time.UTC); got != want {
			t.Errorf("%q: %v", stored, got)
		}
	}
}

func TestGraphRebaseline(t *testing.T) {
	h := newHarness(t)
	g := newFakeProvider(RemoteCalendar{RemoteID: "def", IsDefault: true})
	g.deltaMode = "delta"
	a := graphAccount("acc_g", true)
	h.setProvider(a.ID, g)
	h.run()
	h.sv.Start(a)
	eventually(t, "first pass", idleWithSync(h.sv, a.ID))
	cal := calendarRows(t, h.st, a.ID)[0]
	if cal.WindowFrom != "2025-10-16" || cal.SyncToken == "" {
		t.Fatalf("state %+v", cal.CalendarSyncState)
	}
	// Within 7 days the token and its window stay.
	h.advanceUntil("next pass", 30*time.Second, 3*time.Minute, func() bool { return g.callsOf("def") >= 2 })
	calls := g.calls()
	if last := calls[len(calls)-1]; last.token == "" {
		t.Errorf("token dropped within the slack")
	}
	// A token window 8 days behind: a new chain over the current window.
	st := cal.CalendarSyncState
	st.WindowFrom = "2025-10-08"
	if err := h.st.SetCalendarSyncState(context.Background(), cal.ID, st); err != nil {
		t.Fatal(err)
	}
	before := g.callsOf("def")
	h.sv.SyncNow(a.ID, cal.ID)
	eventually(t, "rebaseline pass", func() bool { return g.callsOf("def") > before })
	calls = g.calls()
	if last := calls[len(calls)-1]; last.token != "" {
		t.Errorf("no new delta chain (token %q)", last.token)
	}
	eventually(t, "window stored", func() bool { return calendarRows(t, h.st, a.ID)[0].WindowFrom == "2025-10-16" })
}

func TestWindowMoveTrims(t *testing.T) {
	h := newHarness(t)
	p := newFakeProvider(RemoteCalendar{RemoteID: "/cal/"})
	a := caldavAccount("acc_1")
	a.Config.Calendar = &api.CalendarConfig{Sync: true, KeepDays: 30}
	// An event that ends on the window's first day and a daily series
	// running across the far edge.
	p.put("/cal/", timed("edge", time.Date(2026, 9, 16, 9, 0, 0, 0, time.UTC), time.Hour))
	daily := weekly("daily", t0, 0)
	daily.Master.RRule = "FREQ=DAILY"
	p.put("/cal/", daily)
	h.setProvider(a.ID, p)
	h.run()
	h.sv.Start(a)
	eventually(t, "first pass", idleWithSync(h.sv, a.ID))
	cal := calendarRows(t, h.st, a.ID)[0]
	if rows, _ := h.st.EventsByRemoteID(context.Background(), cal.ID, "/cal/edge.ics"); len(rows) != 1 {
		t.Fatalf("edge event not stored")
	}
	far := func() int {
		return len(occurrences(t, h.st, a.ID, time.Date(2027, 11, 19, 0, 0, 0, 0, time.UTC), time.Date(2027, 11, 22, 0, 0, 0, 0, time.UTC)))
	}
	if n := far(); n != 1 { // the 19th only: the window ends at the 20th
		t.Fatalf("%d instances at the far edge", n)
	}
	// Stop provider changes from mattering: nothing new is listed.
	p.mu.Lock()
	p.full = false
	p.objects = map[string]map[string]Object{}
	p.mu.Unlock()
	// Next day: the window moves without network for the stored data.
	h.advanceUntil("window moved", 30*time.Minute, 30*time.Hour, func() bool {
		rows, _ := h.st.EventsByRemoteID(context.Background(), cal.ID, "/cal/edge.ics")
		return len(rows) == 0 && far() == 2
	})
	res, _ := h.st.ListResources(context.Background(), cal.ID)
	for _, r := range res {
		if r.Href == "/cal/edge.ics" && r.State != store.ResourceOutside {
			t.Errorf("trimmed resource %+v", r)
		}
	}
	eventually(t, "window state", func() bool { return calendarRows(t, h.st, a.ID)[0].WindowFrom == "2026-09-17" })
}

func TestExpandRebuild(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.ExpandKey = "k1" })
	p := newFakeProvider(RemoteCalendar{RemoteID: "/cal/"})
	p.put("/cal/", weekly("s", t0, 4))
	a := caldavAccount("acc_1")
	h.setProvider(a.ID, p)
	h.run()
	h.sv.Start(a)
	eventually(t, "first pass", idleWithSync(h.sv, a.ID))
	h.stop()
	ctx := context.Background()
	if v, _, _ := h.st.GetMeta(ctx, store.MetaCalendarExpand); v != "k1" {
		t.Fatalf("meta %q", v)
	}
	cal := calendarRows(t, h.st, a.ID)[0]
	rows, _ := h.st.CalendarEvents(ctx, cal.ID)
	// Lose the occurrences: only a rebuild brings them back (no network).
	if err := h.st.ReplaceOccurrences(ctx, []string{rows[0].ID}, nil); err != nil {
		t.Fatal(err)
	}
	same := newHarness(t, func(o *Options) { o.ExpandKey = "k1"; o.Store = h.st })
	same.run()
	time.Sleep(30 * time.Millisecond)
	if n := len(occurrences(t, h.st, a.ID, t0.Add(-time.Hour), t0.Add(40*24*time.Hour))); n != 0 {
		t.Errorf("rebuilt without a key change (%d)", n)
	}
	same.stop()
	next := newHarness(t, func(o *Options) { o.ExpandKey = "k2"; o.Store = h.st })
	next.run()
	eventually(t, "rebuilt", func() bool {
		return len(occurrences(t, h.st, a.ID, t0.Add(-time.Hour), t0.Add(40*24*time.Hour))) == 4
	})
	eventually(t, "meta", func() bool { v, _, _ := h.st.GetMeta(ctx, store.MetaCalendarExpand); return v == "k2" })
	if len(p.calls()) != 1 {
		t.Errorf("the rebuild used the network")
	}
	if ExpandKey() == "" {
		t.Errorf("empty expand key")
	}
}

func TestStoredKeyRoundTrip(t *testing.T) {
	for in, want := range map[api.DateOrTime]string{
		"2026-10-16":           "2026-10-16",
		"2026-10-16T08:00:00Z": "2026-10-16T08:00:00.000Z",
		"":                     "",
	} {
		if got := StoredKey(in); got != want {
			t.Errorf("%q → %q", in, got)
		}
		if back := ModelKey(want); back != in {
			t.Errorf("%q back → %q", want, back)
		}
	}
	obj := weekly("s", t0, 3)
	obj.Master.EXDates = []time.Time{t0.Add(7 * 24 * time.Hour)}
	ov := *obj.Master
	ov.Kind, ov.RecurrenceID, ov.Title, ov.RRule = "override", "2026-10-30T10:00:00Z", "moved", ""
	ov.Start, ov.End = t0.Add(14*24*time.Hour+time.Hour), t0.Add(14*24*time.Hour+2*time.Hour)
	obj.Overrides = []Event{ov}
	rows := StoreEvents(obj)
	if len(rows) != 2 || rows[1].RecurrenceID != "2026-10-30T10:00:00.000Z" || rows[0].ExDates[0] != "2026-10-23T10:00:00.000Z" {
		t.Fatalf("rows %+v", rows)
	}
	back := ObjectOf(rows)
	if back.Master == nil || len(back.Overrides) != 1 || back.Overrides[0].RecurrenceID != ov.RecurrenceID ||
		!back.Master.EXDates[0].Equal(obj.Master.EXDates[0]) || !back.Master.RecurOK {
		t.Errorf("object %+v", back)
	}
}
