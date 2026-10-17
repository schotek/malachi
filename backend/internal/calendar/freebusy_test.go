// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package calendar

import (
	"errors"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

func occ(title, from, to string, show api.ShowAs, resp api.EventResponse) api.Occurrence {
	s, e := utc(from), utc(to)
	return api.Occurrence{Title: title, Start: &s, End: &e, ShowAs: show, MyResponse: resp, Status: api.EventStatusConfirmed}
}

func TestBusyIntervals(t *testing.T) {
	occs := []api.Occurrence{
		occ("a", "2026-10-20T09:00:00Z", "2026-10-20T10:00:00Z", api.ShowAsBusy, ""),
		occ("b", "2026-10-20T09:30:00Z", "2026-10-20T11:00:00Z", api.ShowAsOOF, ""),
		occ("touching", "2026-10-20T11:00:00Z", "2026-10-20T11:30:00Z", api.ShowAsTentative, ""),
		occ("free", "2026-10-20T12:00:00Z", "2026-10-20T13:00:00Z", api.ShowAsFree, ""),
		occ("declined", "2026-10-20T14:00:00Z", "2026-10-20T15:00:00Z", api.ShowAsBusy, api.ResponseDeclined),
		{Title: "all day", AllDay: true, StartDate: "2026-10-21", EndDate: "2026-10-22", ShowAs: api.ShowAsBusy, Status: api.EventStatusConfirmed},
		{Title: "cancelled", Start: ptr(utc("2026-10-20T16:00:00Z")), End: ptr(utc("2026-10-20T17:00:00Z")), ShowAs: api.ShowAsBusy, Status: api.EventStatusCancelled},
	}
	prague, _ := time.LoadLocation("Europe/Prague")
	got := BusyIntervals(occs, prague)
	if len(got) != 2 {
		t.Fatalf("busy %+v", got)
	}
	if !got[0].Start.Equal(utc("2026-10-20T09:00:00Z")) || !got[0].End.Equal(utc("2026-10-20T11:30:00Z")) || got[0].ShowAs != api.ShowAsOOF {
		t.Errorf("merged %+v", got[0])
	}
	if !got[1].Start.Equal(utc("2026-10-20T22:00:00Z")) || !got[1].End.Equal(utc("2026-10-21T22:00:00Z")) {
		t.Errorf("all-day in Prague %+v", got[1])
	}
	if MergeBusy(nil) == nil {
		t.Error("MergeBusy never returns nil")
	}
}

func ptr(t time.Time) *time.Time { return &t }

func TestFreeSlots(t *testing.T) {
	prague, _ := time.LoadLocation("Europe/Prague")
	wh := &api.WorkingHours{Days: []string{"MO", "TU", "WE", "TH", "FR"}, Start: "09:00", End: "12:00"}
	busy := []api.BusyInterval{
		{Start: utc("2026-10-20T07:10:00Z"), End: utc("2026-10-20T08:00:00Z"), ShowAs: api.ShowAsBusy}, // 09:10–10:00 Prague
		{Start: utc("2026-10-20T08:30:00Z"), End: utc("2026-10-20T09:40:00Z"), ShowAs: api.ShowAsTentative},
	}
	// Tuesday 20 Oct from 08:05 Prague to Wednesday evening.
	slots, err := FreeSlots(busy, utc("2026-10-20T06:05:00Z"), utc("2026-10-21T18:00:00Z"), 30*time.Minute, wh, prague, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Tuesday: 09:00–09:10 is too short, 10:00–10:30 is free, the
	// tentative block ends at 11:40 and 11:45–12:15 runs past 12:00.
	got := []string{}
	for _, s := range slots {
		got = append(got, s.Start.In(prague).Format("Mon 15:04"))
	}
	exp := []string{"Tue 10:00", "Wed 09:00", "Wed 09:30", "Wed 10:00", "Wed 10:30", "Wed 11:00", "Wed 11:30"}
	if len(got) != len(exp) {
		t.Fatalf("slots %v want %v", got, exp)
	}
	for i := range exp {
		if got[i] != exp[i] {
			t.Errorf("slot %d = %s want %s", i, got[i], exp[i])
		}
	}
	slots, _ = FreeSlots(nil, utc("2026-10-24T00:00:00Z"), utc("2026-10-26T00:00:00Z"), time.Hour, wh, prague, 0, 0)
	if len(slots) != 0 {
		t.Errorf("weekend: %+v", slots)
	}
	slots, _ = FreeSlots(nil, utc("2026-10-20T00:00:00Z"), utc("2026-10-30T00:00:00Z"), 15*time.Minute, nil, nil, 0, 3)
	if len(slots) != 3 || !slots[1].Start.Equal(utc("2026-10-20T00:15:00Z")) {
		t.Errorf("limit and no working hours: %+v", slots)
	}
	if _, err := FreeSlots(nil, utc("2026-10-20T00:00:00Z"), utc("2026-10-21T00:00:00Z"), time.Hour, &api.WorkingHours{Days: []string{"XX"}, Start: "09:00", End: "10:00"}, nil, 0, 0); !errors.Is(err, ErrWorkingHours) {
		t.Errorf("bad working hours: %v", err)
	}
	if _, err := FreeSlots(nil, utc("2026-10-20T00:00:00Z"), utc("2026-10-21T00:00:00Z"), time.Hour, &api.WorkingHours{Days: []string{"MO"}, Start: "10:00", End: "09:00"}, nil, 0, 0); !errors.Is(err, ErrWorkingHours) {
		t.Errorf("end before start: %v", err)
	}
}

func TestConflicts(t *testing.T) {
	occs := []api.Occurrence{
		occ("late", "2026-10-20T10:30:00Z", "2026-10-20T11:30:00Z", api.ShowAsBusy, ""),
		occ("early", "2026-10-20T08:00:00Z", "2026-10-20T09:30:00Z", api.ShowAsBusy, ""),
		occ("adjacent", "2026-10-20T11:00:00Z", "2026-10-20T12:00:00Z", api.ShowAsBusy, ""),
		occ("free", "2026-10-20T09:00:00Z", "2026-10-20T10:00:00Z", api.ShowAsFree, ""),
		occ("declined", "2026-10-20T09:00:00Z", "2026-10-20T10:00:00Z", api.ShowAsBusy, api.ResponseDeclined),
		occ("inside", "2026-10-20T09:45:00Z", "2026-10-20T10:00:00Z", api.ShowAsTentative, ""),
		occ("also", "2026-10-20T10:00:00Z", "2026-10-20T10:15:00Z", api.ShowAsBusy, ""),
	}
	got := Conflicts(utc("2026-10-20T09:00:00Z"), utc("2026-10-20T11:00:00Z"), occs, nil, 0)
	names := []string{}
	for _, o := range got {
		names = append(names, o.Title)
	}
	if len(names) != 3 || names[0] != "early" || names[1] != "inside" || names[2] != "also" {
		t.Errorf("conflicts %v", names)
	}
}
