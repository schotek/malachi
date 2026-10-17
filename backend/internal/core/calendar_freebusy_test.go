// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"errors"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/calsync"
	"github.com/schotek/malachi/backend/pkg/api"
)

// nextMonday is the Monday at least two days after today (UTC midnight).
func nextMonday() time.Time {
	d := day0().AddDate(0, 0, 2)
	for d.Weekday() != time.Monday {
		d = d.AddDate(0, 0, 1)
	}
	return d
}

func TestCalendarFreeBusy(t *testing.T) {
	h := newCalHarness(t)
	ctx, b := h.ctx, h.b
	mon := nextMonday()
	g, gf := h.account(api.AccountGraph, "g@example.test", nil,
		calsync.RemoteCalendar{RemoteID: "main", Name: "Main", Access: api.CalendarAccessOwner, Kind: api.CalendarKindEvents},
		calsync.RemoteCalendar{RemoteID: "hidden", Name: "Hidden", Access: api.CalendarAccessOwner, Kind: api.CalendarKindEvents})
	gf.feats.FreeBusy = true
	free := timed("free", "Free", mon.Add(13*time.Hour), time.Hour)
	free.ShowAs = api.ShowAsFree
	gf.put("main", obj("o1", "e", timed("busy", "Busy", mon.Add(9*time.Hour), time.Hour)))
	gf.put("main", obj("o2", "e", free))
	gf.put("main", obj("o3", "e", invitedTo(timed("decl", "Declined", mon.Add(11*time.Hour), time.Hour), api.ResponseDeclined)))
	gf.put("hidden", obj("o4", "e", timed("hid", "Hidden", mon.Add(15*time.Hour), time.Hour)))
	h.start(g)
	vis := false
	if _, err := b.Calendars().Update(ctx, api.CalendarUpdateParams{CalendarID: api.CalendarID(h.calendarID(g, "hidden")), Visible: &vis}); err != nil {
		t.Fatal(err)
	}
	busyFrom := mon.Add(9 * time.Hour)
	gf.fb = []calsync.Availability{
		{Address: "jana@example.test", Known: true, Busy: []api.BusyInterval{{Start: busyFrom, End: busyFrom.Add(2 * time.Hour), ShowAs: api.ShowAsBusy}}},
		{Address: "petr@example.test", Known: false},
	}
	p := api.CalendarFreeBusyParams{AccountID: api.AccountID(g.ID), Attendees: []string{"Jana@example.test", "petr@example.test", "eva@example.test"},
		From: mon, To: mon.Add(24 * time.Hour), IntervalMinutes: 30}
	res, err := b.Calendars().FreeBusy(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Me.Known || len(res.Me.Busy) != 1 || !res.Me.Busy[0].Start.Equal(busyFrom) || res.Me.Address != "g@example.test" {
		t.Errorf("me %+v", res.Me)
	}
	if len(res.Attendees) != 3 || res.Attendees[0].Address != "jana@example.test" || !res.Attendees[0].Known ||
		len(res.Attendees[0].Busy) != 1 || res.Attendees[1].Known || res.Attendees[2].Known || res.Attendees[2].Busy == nil {
		t.Errorf("attendees %+v", res.Attendees)
	}

	// A failing source is not an error; neither is one without free/busy.
	gf.fbErr = errors.New("boom")
	if res, err = b.Calendars().FreeBusy(ctx, p); err != nil || res.Attendees[0].Known || len(res.Me.Busy) != 1 {
		t.Errorf("failed source: %+v %v", res, err)
	}
	gf.fbErr = nil
	gf.feats.FreeBusy = false
	if res, err = b.Calendars().FreeBusy(ctx, p); err != nil || res.Attendees[0].Known {
		t.Errorf("no free/busy: %+v %v", res, err)
	}

	many := make([]string, api.MaxFreeBusyAttendees+1)
	for i := range many {
		many[i] = "x@example.test"
	}
	for name, q := range map[string]api.CalendarFreeBusyParams{
		"noAccount": {Attendees: p.Attendees, From: p.From, To: p.To},
		"reversed":  {AccountID: p.AccountID, From: p.To, To: p.From},
		"tooLong":   {AccountID: p.AccountID, From: p.From, To: p.From.AddDate(0, 0, api.MaxFreeBusyDays+1)},
		"many":      {AccountID: p.AccountID, Attendees: many, From: p.From, To: p.To},
		"interval":  {AccountID: p.AccountID, From: p.From, To: p.To, IntervalMinutes: 45},
		"address":   {AccountID: p.AccountID, Attendees: []string{"not an address"}, From: p.From, To: p.To},
	} {
		if _, err := b.Calendars().FreeBusy(ctx, q); errCode(t, err) != api.CodeInvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := b.Calendars().FreeBusy(ctx, api.CalendarFreeBusyParams{AccountID: "acc_none", From: p.From, To: p.To}); errCode(t, err) != api.CodeAccountNotFound {
		t.Errorf("unknown account: %v", err)
	}
}

func TestCalendarFindFreeTime(t *testing.T) {
	h := newCalHarness(t)
	ctx, b := h.ctx, h.b
	mon := nextMonday()
	g, gf := h.account(api.AccountGraph, "g@example.test", nil,
		calsync.RemoteCalendar{RemoteID: "main", Name: "Main", Access: api.CalendarAccessOwner, Kind: api.CalendarKindEvents})
	gf.feats.FreeBusy = true
	gf.put("main", obj("o1", "e", timed("busy", "Busy", mon.Add(8*time.Hour+30*time.Minute), time.Hour)))
	h.start(g)
	gf.fb = []calsync.Availability{{Address: "jana@example.test", Known: true,
		Busy: []api.BusyInterval{{Start: mon.Add(10 * time.Hour), End: mon.Add(11 * time.Hour), ShowAs: api.ShowAsBusy}}}}
	res, err := b.Calendars().FindFreeTime(ctx, api.CalendarFindFreeTimeParams{AccountID: api.AccountID(g.ID),
		Attendees: []string{"jana@example.test", "petr@example.test"}, DurationMinutes: 30, From: mon, To: mon.Add(24 * time.Hour), Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	// Working hours 08:00–17:00 (UTC, the engine's zone): 08:00 fits, 08:30–09:30 is mine,
	// 10:00–11:00 Jana's.
	want := []time.Time{mon.Add(8 * time.Hour), mon.Add(9*time.Hour + 30*time.Minute), mon.Add(11 * time.Hour)}
	if len(res.Slots) != 3 {
		t.Fatalf("slots %+v", res.Slots)
	}
	for i, w := range want {
		if !res.Slots[i].Start.Equal(w) || !res.Slots[i].End.Equal(w.Add(30*time.Minute)) {
			t.Errorf("slot %d = %+v, want %v", i, res.Slots[i], w)
		}
	}
	if len(res.Unknown) != 1 || res.Unknown[0] != "petr@example.test" {
		t.Errorf("unknown %v", res.Unknown)
	}
	// Outside working hours and without an account: the user only.
	off := false
	res, err = b.Calendars().FindFreeTime(ctx, api.CalendarFindFreeTimeParams{DurationMinutes: 60, From: mon, To: mon.Add(24 * time.Hour), WorkingHoursOnly: &off})
	if err != nil || len(res.Slots) != 5 || !res.Slots[0].Start.Equal(mon) || res.Unknown == nil {
		t.Errorf("any time: %+v %v", res, err)
	}
	for name, q := range map[string]api.CalendarFindFreeTimeParams{
		"duration": {From: mon, To: mon.Add(time.Hour)},
		"tooLong":  {DurationMinutes: 120, From: mon, To: mon.Add(time.Hour)},
		"limit":    {DurationMinutes: 30, From: mon, To: mon.Add(24 * time.Hour), Limit: api.MaxFreeSlots + 1},
	} {
		if _, err := b.Calendars().FindFreeTime(ctx, q); errCode(t, err) != api.CodeInvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
}
