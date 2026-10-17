// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"slices"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestAvailability(t *testing.T) {
	loc := prague(t)
	env := envIn(loc)
	h := func(hh, mm int) time.Time { return at(loc, 2026, 10, 14, hh, mm) }
	busy := func(s, e time.Time, as api.ShowAs) api.BusyInterval {
		return api.BusyInterval{Start: s, End: e, ShowAs: as}
	}
	res := api.CalendarFreeBusyResult{
		Me: api.Availability{Address: "me@example.org", Known: true, Busy: []api.BusyInterval{busy(h(9, 0), h(10, 0), api.ShowAsBusy)}},
		Attendees: []api.Availability{
			{Address: "jana@example.org", Known: true, Busy: []api.BusyInterval{
				busy(h(13, 0), h(14, 0), api.ShowAsOOF), busy(h(7, 0), h(9, 0), api.ShowAsBusy),
				busy(h(10, 0), h(11, 0), api.ShowAsFree), busy(h(11, 0), h(12, 0), api.ShowAsWorkingElsewhere)}},
			{Address: "petr@example.org", Known: true, Busy: []api.BusyInterval{busy(h(10, 0), h(10, 30), api.ShowAsTentative)}},
			{Address: "eva@example.org", Known: false},
		},
	}
	rows := AvailabilityRows(res, map[string]string{"jana@example.org": "Jana"}, h(8, 0), h(16, 0), en{})
	if len(rows) != 4 || rows[0].Label != "You" || rows[1].Label != "Jana" || rows[2].Label != "petr@example.org" || rows[3].Known {
		t.Fatalf("rows %+v", rows)
	}
	if !slices.Equal(rows[1].Busy, []AxisSpan{{0, 0.125, api.ShowAsBusy}, {0.625, 0.75, api.ShowAsOOF}}) {
		t.Errorf("Jana's spans %+v", rows[1].Busy)
	}
	if !slices.Equal(rows[0].Busy, []AxisSpan{{0.125, 0.25, api.ShowAsBusy}}) || rows[3].Busy != nil {
		t.Errorf("spans %+v %+v", rows[0].Busy, rows[3].Busy)
	}
	tests := []struct {
		s, e time.Time
		want []string
	}{
		{h(15, 0), h(16, 0), []string{"15:00–16:00: all known attendees are free", "Availability of 1 attendee is unknown"}},
		{h(10, 0), h(11, 0), []string{"1 of 2 attendees is busy", "Availability of 1 attendee is unknown"}},
		{h(8, 30), h(10, 15), []string{"2 of 2 attendees are busy", "Availability of 1 attendee is unknown"}},
		{h(11, 0), h(12, 0), []string{"11:00–12:00: all known attendees are free", "Availability of 1 attendee is unknown"}},
	}
	for _, tt := range tests {
		if got := Summary(res, tt.s, tt.e, env); !slices.Equal(got, tt.want) {
			t.Errorf("Summary %v: %q", tt.s, got)
		}
	}
	if got := Summary(api.CalendarFreeBusyResult{Me: res.Me}, h(9, 0), h(10, 0), env); got != nil {
		t.Errorf("no attendees: %q", got)
	}
	if got := Summary(api.CalendarFreeBusyResult{Attendees: res.Attendees[2:]}, h(9, 0), h(10, 0), env); !slices.Equal(got,
		[]string{"Availability of 1 attendee is unknown"}) {
		t.Errorf("only unknown: %q", got)
	}
	if AxisSpanOK := axisSpans(res.Me.Busy, h(10, 0), h(10, 0)); AxisSpanOK != nil {
		t.Error("empty axis")
	}
}

func TestNextFreeSlot(t *testing.T) {
	loc := prague(t)
	env := envIn(loc)
	h := func(hh int) time.Time { return at(loc, 2026, 10, 14, hh, 0) }
	res := api.CalendarFindFreeTimeResult{Slots: []api.TimeSlot{{Start: h(15), End: h(16)}, {Start: h(9), End: h(10)},
		{Start: h(12), End: h(12)}, {Start: h(11), End: h(12)}}}
	s, ok := NextFreeSlot(res, h(10))
	if !ok || !s.Start.Equal(h(11)) {
		t.Errorf("NextFreeSlot = %v %v", s, ok)
	}
	if _, ok := NextFreeSlot(res, h(17)); ok {
		t.Error("a slot after the last")
	}
	if got := NextFreeSlotText(s, env); got != "Next free slot: Wednesday 14 October 2026 · 11:00–12:00" {
		t.Errorf("text %q", got)
	}
	if !Busy(api.ShowAsUnknown) || Busy(api.ShowAsFree) || Busy(api.ShowAsWorkingElsewhere) {
		t.Error("Busy")
	}
}
