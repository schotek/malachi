// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestReplaceResourceKeepsIDs(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	_, cal := calAccount(t, s, "me@example.invalid")
	start := threadBase.Add(9 * time.Hour)
	ov1, ov2 := FormatCalendarTime(start.AddDate(0, 0, 1)), FormatCalendarTime(start.AddDate(0, 0, 2))
	first, err := s.ReplaceResource(ctx, cal.ID, "/e/s.ics", []Event{
		{Kind: EventMaster, UID: "series", Title: "Standup", StartAt: start, EndAt: start.Add(15 * time.Minute), RRule: "FREQ=DAILY",
			Recurrence: &api.Recurrence{Frequency: api.FrequencyDaily, Interval: 1}, Organizer: &api.Address{Name: "Boss", Address: "boss@example.invalid"},
			MyResponse: api.ResponseAccepted, Raw: []byte("raw"), ExDates: []string{"x"}},
		{Kind: EventOverride, UID: "series", RecurrenceID: ov1, Title: "Standup (late)"},
		{Kind: EventOverride, UID: "series", RecurrenceID: ov2, Title: "Standup (later)"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	master := first[0]
	if master.AccountID != cal.AccountID || master.Status != api.EventStatusConfirmed || master.ShowAs != api.ShowAsBusy ||
		master.RecurRefused || master.Recurrence == nil || master.Organizer == nil || string(master.Raw) != "raw" ||
		first[1].MasterID != master.ID || first[2].MasterID != master.ID {
		t.Fatalf("stored: %+v", first)
	}
	// The second pass drops one override, changes the master and adds one.
	ov3 := FormatCalendarTime(start.AddDate(0, 0, 3))
	second, err := s.ReplaceResource(ctx, cal.ID, "/e/s.ics", []Event{
		{Kind: EventMaster, UID: "series", Title: "Daily", StartAt: start, EndAt: start.Add(15 * time.Minute), RRule: "FREQ=DAILY", RecurRefused: true},
		{Kind: EventOverride, UID: "series", RecurrenceID: ov2, Title: "Standup (later)"},
		{Kind: EventOverride, UID: "series", RecurrenceID: ov3, Title: "New"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second[0].ID != master.ID || second[1].ID != first[2].ID || second[2].ID == first[1].ID || !second[0].RecurRefused || second[0].Raw != nil {
		t.Fatalf("second pass: %+v", second)
	}
	if _, err := s.GetEvent(ctx, first[1].ID); err != ErrNotFound {
		t.Fatalf("dropped override: %v", err)
	}
	byUID, err := s.EventsByUID(ctx, cal.AccountID, "series")
	if err != nil || len(byUID) != 3 || byUID[0].Kind != EventMaster {
		t.Fatalf("by uid: %+v %v", byUID, err)
	}
	byRemote, err := s.EventsByRemoteID(ctx, cal.ID, "/e/s.ics")
	if err != nil || len(byRemote) != 3 {
		t.Fatalf("by remote: %v %v", len(byRemote), err)
	}
	if ovs, err := s.SeriesOverrides(ctx, master.ID); err != nil || len(ovs) != 2 {
		t.Fatalf("overrides: %v %v", len(ovs), err)
	}
	if _, err := s.ReplaceResource(ctx, cal.ID, "/e/x.ics", []Event{{Kind: EventSingle}, {Kind: EventSingle}}, nil); err == nil {
		t.Fatal("two rows with one recurrence id")
	}
	if _, err := s.ReplaceResource(ctx, "cal_none", "/e/x.ics", []Event{{Kind: EventSingle}}, nil); err != ErrNotFound {
		t.Fatalf("unknown calendar: %v", err)
	}
	if n, err := s.RemoveResource(ctx, cal.ID, "/e/s.ics"); err != nil || n != 3 {
		t.Fatalf("remove: %d %v", n, err)
	}
}

func TestTrimEvents(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	a, cal := calAccount(t, s, "me@example.invalid")
	windowStart := threadBase.AddDate(0, 1, 0)
	put := func(remote string, e Event, occ ...Occurrence) Event {
		t.Helper()
		st, err := s.ReplaceResource(ctx, cal.ID, remote, []Event{e}, func(st []Event) []Occurrence {
			for i := range occ {
				occ[i].EventID, occ[i].CalendarID, occ[i].AccountID = st[0].ID, cal.ID, a.ID
				if occ[i].SeriesID == "x" {
					occ[i].SeriesID = st[0].ID
				}
			}
			return occ
		})
		if err != nil {
			t.Fatal(err)
		}
		return st[0]
	}
	old := put("/old", Event{Kind: EventSingle, StartAt: threadBase, EndAt: threadBase.Add(time.Hour)})
	oldDay := put("/oldday", Event{Kind: EventSingle, AllDay: true, StartDate: "2026-09-01", EndDate: "2026-09-02"})
	kept := put("/new", Event{Kind: EventSingle, StartAt: windowStart.Add(time.Hour), EndAt: windowStart.Add(2 * time.Hour)})
	series := put("/series", Event{Kind: EventMaster, StartAt: threadBase, EndAt: threadBase.Add(time.Hour), RRule: "FREQ=WEEKLY"},
		Occurrence{OccurrenceKey: "k", SeriesID: "x", StartAt: windowStart.AddDate(0, 0, 1), EndAt: windowStart.AddDate(0, 0, 1).Add(time.Hour)})
	ended := put("/ended", Event{Kind: EventMaster, StartAt: threadBase, EndAt: threadBase.Add(time.Hour), RRule: "FREQ=WEEKLY;COUNT=2"},
		Occurrence{OccurrenceKey: "k", SeriesID: "x", StartAt: threadBase, EndAt: threadBase.Add(time.Hour)})
	gone, err := s.TrimEvents(ctx, a.ID, windowStart)
	if err != nil {
		t.Fatal(err)
	}
	if len(gone) != 3 {
		t.Fatalf("gone: %+v", gone)
	}
	for id, want := range map[string]bool{old.ID: false, oldDay.ID: false, kept.ID: true, series.ID: true, ended.ID: false} {
		_, err := s.GetEvent(ctx, id)
		if (err == nil) != want {
			t.Errorf("%s: %v, want kept %v", id, err, want)
		}
	}
}
