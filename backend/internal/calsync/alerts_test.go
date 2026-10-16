// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package calsync

import (
	"context"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/calendar"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// alertHarness runs one caldav account whose calendar holds objs.
func alertHarness(t *testing.T, objs ...Object) (*harness, store.Account) {
	t.Helper()
	h := newHarness(t)
	p := newFakeProvider(RemoteCalendar{RemoteID: "/cal/"})
	for _, o := range objs {
		p.put("/cal/", o)
	}
	a := caldavAccount("acc_1")
	h.setProvider(a.ID, p)
	h.run()
	h.sv.Start(a)
	eventually(t, "first pass", idleWithSync(h.sv, a.ID))
	return h, a
}

func alertRows(t *testing.T, st *store.Store) []store.Alert {
	t.Helper()
	all, err := st.DueAlerts(context.Background(), time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return all
}

func dueAlerts(h *harness) int {
	n := 0
	for _, a := range h.note.alertList() {
		if a.State == api.AlertDue {
			n++
		}
	}
	return n
}

func TestAlertDue(t *testing.T) {
	h, _ := alertHarness(t, timed("m", t0.Add(time.Hour), time.Hour, 15))
	eventually(t, "alert row", func() bool { return len(alertRows(t, h.st)) == 1 })
	al := alertRows(t, h.st)[0]
	if !al.DueAt.Equal(t0.Add(45*time.Minute)) || al.MinutesBefore != 15 {
		t.Fatalf("alert %+v", al)
	}
	h.advanceUntil("due", time.Minute, time.Hour, func() bool { return dueAlerts(h) == 1 })
	if now := h.clock.Now(); now.Before(t0.Add(45 * time.Minute)) {
		t.Errorf("fired early at %s", now)
	}
	got := h.note.alertList()[0]
	if got.AlertID != api.AlertID(al.ID) || got.Title != "T m" || got.Start == nil || !got.Start.Equal(t0.Add(time.Hour)) || got.MinutesBefore != 15 {
		t.Errorf("alert %+v", got)
	}
	a, _ := h.st.GetAlert(context.Background(), al.ID)
	if a.State != store.AlertFired {
		t.Errorf("state %s", a.State)
	}
	// A recomputation keeps the fired state of the unchanged key.
	h.sv.Reload()
	time.Sleep(20 * time.Millisecond)
	h.clock.Advance(time.Minute)
	time.Sleep(20 * time.Millisecond)
	if a, _ := h.st.GetAlert(context.Background(), al.ID); a.State != store.AlertFired || dueAlerts(h) != 1 {
		t.Errorf("refired: %s, %d", a.State, dueAlerts(h))
	}
}

func TestAlertLate(t *testing.T) {
	// Found 10 minutes late (alarm 15 min before an event starting in 5):
	// announced. Found 20 minutes late (still running): fired silently.
	h, _ := alertHarness(t,
		timed("soon", t0.Add(5*time.Minute), time.Hour, 15),
		timed("running", t0.Add(-5*time.Minute), time.Hour, 15))
	eventually(t, "fired", func() bool {
		for _, a := range alertRows(t, h.st) {
			_ = a
		}
		rows, _ := h.st.DueAlerts(context.Background(), t0.Add(time.Hour))
		return len(rows) == 0 && dueAlerts(h) == 1
	})
	time.Sleep(20 * time.Millisecond)
	if n := dueAlerts(h); n != 1 {
		t.Fatalf("%d announced", n)
	}
	if got := h.note.alertList()[0]; got.Title != "T soon" {
		t.Errorf("announced %q", got.Title)
	}
}

func TestAlertEndedNotAnnounced(t *testing.T) {
	h, _ := alertHarness(t, timed("past", t0.Add(-2*time.Hour), time.Hour, 15))
	time.Sleep(30 * time.Millisecond)
	if n := len(alertRows(t, h.st)); n != 0 || dueAlerts(h) != 0 {
		t.Errorf("an ended occurrence has alerts (%d)", n)
	}
}

func TestAlertSnoozeAndDismiss(t *testing.T) {
	h, _ := alertHarness(t, timed("m", t0.Add(10*time.Minute), time.Hour, 10))
	eventually(t, "due", func() bool { return dueAlerts(h) == 1 })
	id := h.note.alertList()[0].AlertID
	ctx := context.Background()
	if err := h.sv.DismissAlert(ctx, "nope"); err == nil {
		t.Errorf("dismiss of an unknown alert")
	}
	if err := h.sv.SnoozeAlert(ctx, string(id), t0.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := h.sv.SnoozeAlert(ctx, string(id), t0.Add(5*time.Minute)); err == nil {
		t.Errorf("snooze of a pending alert")
	}
	time.Sleep(20 * time.Millisecond)
	h.clock.Advance(4 * time.Minute)
	time.Sleep(20 * time.Millisecond)
	if dueAlerts(h) != 1 {
		t.Fatalf("snooze ended early")
	}
	h.advanceUntil("snooze end", 30*time.Second, 5*time.Minute, func() bool { return dueAlerts(h) == 2 })
	if err := h.sv.DismissAlert(ctx, string(id)); err != nil {
		t.Fatal(err)
	}
	last := h.note.alertList()[len(h.note.alertList())-1]
	if last.State != api.AlertDismissed || last.AlertID != id || last.Title != "T m" {
		t.Errorf("dismissed %+v", last)
	}
	if a, _ := h.st.GetAlert(ctx, string(id)); a.State != store.AlertDismissed {
		t.Errorf("state %s", a.State)
	}
}

func TestAlertDeclinedAndPaused(t *testing.T) {
	declined := timed("d", t0.Add(time.Hour), time.Hour, 15)
	declined.Master.MyResponse = api.ResponseDeclined
	h, a := alertHarness(t, declined, timed("ok", t0.Add(2*time.Hour), time.Hour, 15))
	eventually(t, "alert", func() bool { return len(alertRows(t, h.st)) == 1 })
	rows := alertRows(t, h.st)
	ev, _ := h.st.GetEvent(context.Background(), rows[0].EventID)
	if ev.Title != "T ok" {
		t.Errorf("alert of %q", ev.Title)
	}
	// A paused account (its syncer stopped) has no alerts.
	h.sv.Stop(a.ID)
	eventually(t, "no alerts", func() bool { return len(alertRows(t, h.st)) == 0 })
}

func TestAlertAllDayAtWorkingStart(t *testing.T) {
	day := calendar.Event{UID: "ad", Kind: calendar.KindSingle, Title: "Holiday", AllDay: true,
		Start: time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC),
		StartDate: "2026-10-17", EndDate: "2026-10-18", Alarms: []int{30}}
	h, _ := alertHarness(t, Object{RemoteID: "/cal/ad.ics", Master: &day})
	eventually(t, "alert row", func() bool { return len(alertRows(t, h.st)) == 1 })
	// Working day starts 08:00 (the defaults): the alert is at 07:30.
	if got := alertRows(t, h.st)[0].DueAt; !got.Equal(time.Date(2026, 10, 17, 7, 30, 0, 0, time.UTC)) {
		t.Errorf("due %s", got)
	}
	// Preferences with another start move it on the next recomputation.
	p := api.DefaultCalendarPreferences()
	p.WorkingHours.Start = "09:15"
	if err := h.st.SetCalendarPreferences(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	h.sv.Reload()
	eventually(t, "moved", func() bool {
		r := alertRows(t, h.st)
		return len(r) == 1 && r[0].DueAt.Equal(time.Date(2026, 10, 17, 8, 45, 0, 0, time.UTC))
	})
	h.advanceUntil("all-day alert", 5*time.Minute, 24*time.Hour, func() bool { return dueAlerts(h) == 1 })
	got := h.note.alertList()[0]
	if !got.AllDay || got.StartDate != "2026-10-17" || got.Start != nil {
		t.Errorf("alert %+v", got)
	}
}

func TestWorkingStart(t *testing.T) {
	for in, want := range map[string][2]int{"09:30": {9, 30}, "": {8, 0}, "25:00": {8, 0}, "7:05": {7, 5}, "x": {8, 0}} {
		if h, m := workingStart(in); h != want[0] || m != want[1] {
			t.Errorf("%q → %d:%d", in, h, m)
		}
	}
}
