// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"fmt"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// placed is a block's id, column, columns, top and bottom.
type placed struct {
	id                    string
	col, cols, top, bottm int
}

func layout(occ []api.Occurrence, day time.Time, loc *time.Location, showDeclined bool) []placed {
	var out []placed
	for _, b := range LayoutDay(occ, day, loc, showDeclined) {
		out = append(out, placed{string(b.Occurrence.EventID), b.Column, b.Columns, b.Top, b.Bottom})
	}
	return out
}

func samePlaced(a, b []placed) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestLayoutDayOverlaps(t *testing.T) {
	loc := prague(t)
	day := at(loc, 2026, 10, 14, 0, 0)
	h := func(hh, mm int) time.Time { return at(loc, 2026, 10, 14, hh, mm) }
	// 1 to 8 events at the same time: one column each.
	for n := 1; n <= 8; n++ {
		var occ []api.Occurrence
		var want []placed
		for i := range n {
			id := fmt.Sprintf("e%d", i)
			occ = append(occ, timed(id, h(10, 0), h(11, 0)))
			want = append(want, placed{id, i, n, 600, 660})
		}
		// Given in reverse: the order is by key, not by input.
		for i, j := 0, len(occ)-1; i < j; i, j = i+1, j-1 {
			occ[i], occ[j] = occ[j], occ[i]
		}
		if got := layout(occ, day, loc, false); !samePlaced(got, want) {
			t.Errorf("%d overlapping: %v, want %v", n, got, want)
		}
	}
	tests := []struct {
		name string
		occ  []api.Occurrence
		want []placed
	}{
		{"staircase reuses the freed column",
			[]api.Occurrence{timed("a", h(9, 0), h(10, 0)), timed("b", h(9, 30), h(10, 30)), timed("c", h(10, 0), h(11, 0))},
			[]placed{{"a", 0, 2, 540, 600}, {"b", 1, 2, 570, 630}, {"c", 0, 2, 600, 660}}},
		{"two clusters",
			[]api.Occurrence{timed("a", h(9, 0), h(10, 0)), timed("b", h(9, 0), h(10, 0)), timed("c", h(11, 0), h(12, 0))},
			[]placed{{"a", 0, 2, 540, 600}, {"b", 1, 2, 540, 600}, {"c", 0, 1, 660, 720}}},
		{"longer first, greedy",
			[]api.Occurrence{timed("d", h(10, 30), h(11, 30)), timed("b", h(9, 0), h(10, 0)), timed("c", h(10, 0), h(11, 0)),
				timed("a", h(9, 0), h(12, 0))},
			[]placed{{"a", 0, 3, 540, 720}, {"b", 1, 3, 540, 600}, {"c", 1, 3, 600, 660}, {"d", 2, 3, 630, 690}}},
		{"back to back do not overlap",
			[]api.Occurrence{timed("a", h(9, 0), h(10, 0)), timed("b", h(10, 0), h(11, 0))},
			[]placed{{"a", 0, 1, 540, 600}, {"b", 0, 1, 600, 660}}},
		{"short blocks get the minimum height and overlap by it",
			[]api.Occurrence{timed("a", h(9, 0), h(9, 5)), timed("b", h(9, 10), h(9, 20))},
			[]placed{{"a", 0, 2, 540, 555}, {"b", 1, 2, 550, 565}}},
		{"no length at the end of the day",
			[]api.Occurrence{timed("a", h(23, 55), h(23, 55))},
			[]placed{{"a", 0, 1, 1425, 1440}}},
		{"all-day left out",
			[]api.Occurrence{allDayOcc("x", day, 1), timed("a", h(8, 0), h(9, 0))},
			[]placed{{"a", 0, 1, 480, 540}}},
	}
	for _, tt := range tests {
		if got := layout(tt.occ, day, loc, false); !samePlaced(got, tt.want) {
			t.Errorf("%s: %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestLayoutDayClipsAndDeclined(t *testing.T) {
	loc := prague(t)
	night := timed("n", at(loc, 2026, 10, 14, 22, 0), at(loc, 2026, 10, 15, 2, 0))
	b := LayoutDay([]api.Occurrence{night}, at(loc, 2026, 10, 14, 0, 0), loc, false)
	if len(b) != 1 || b[0].Top != 1320 || b[0].Bottom != 1440 || !b[0].ContinuesAfter || b[0].ContinuesBefore {
		t.Errorf("first night: %+v", b)
	}
	b = LayoutDay([]api.Occurrence{night}, at(loc, 2026, 10, 15, 0, 0), loc, false)
	if len(b) != 1 || b[0].Top != 0 || b[0].Bottom != 120 || !b[0].ContinuesBefore || b[0].ContinuesAfter {
		t.Errorf("second night: %+v", b)
	}
	if b := LayoutDay([]api.Occurrence{night}, at(loc, 2026, 10, 16, 0, 0), loc, false); len(b) != 0 {
		t.Errorf("third day: %+v", b)
	}
	declined := timed("d", at(loc, 2026, 10, 14, 9, 0), at(loc, 2026, 10, 14, 10, 0))
	declined.MyResponse = api.ResponseDeclined
	if b := LayoutDay([]api.Occurrence{declined}, at(loc, 2026, 10, 14, 0, 0), loc, false); len(b) != 0 {
		t.Errorf("declined shown: %+v", b)
	}
	if b := LayoutDay([]api.Occurrence{declined}, at(loc, 2026, 10, 14, 0, 0), loc, true); len(b) != 1 || b[0].State != BlockDeclined {
		t.Errorf("declined with showDeclined: %+v", b)
	}
}

func TestLayoutDayDaylightSaving(t *testing.T) {
	loc := prague(t)
	// 29 March 2026: 02:00 CET becomes 03:00 CEST, the day has 23 hours.
	spring := at(loc, 2026, 3, 29, 0, 0)
	if h := AddDays(spring, 1).Sub(spring).Hours(); h != 23 {
		t.Fatalf("spring day has %v hours", h)
	}
	s1 := time.Date(2026, 3, 29, 0, 30, 0, 0, time.UTC) // 01:30 CET
	e1 := time.Date(2026, 3, 29, 1, 30, 0, 0, time.UTC) // 03:30 CEST, one hour later
	s2 := time.Date(2026, 3, 29, 1, 0, 0, 0, time.UTC)  // 03:00 CEST
	e2 := time.Date(2026, 3, 29, 2, 0, 0, 0, time.UTC)  // 04:00 CEST
	got := layout([]api.Occurrence{timed("a", s1, e1), timed("b", s2, e2)}, spring, loc, false)
	want := []placed{{"a", 0, 2, 90, 210}, {"b", 1, 2, 180, 240}}
	if !samePlaced(got, want) {
		t.Errorf("spring: %v, want %v", got, want)
	}
	// 25 October 2026: 03:00 CEST becomes 02:00 CET, the day has 25 hours.
	autumn := at(loc, 2026, 10, 25, 0, 0)
	if h := AddDays(autumn, 1).Sub(autumn).Hours(); h != 25 {
		t.Fatalf("autumn day has %v hours", h)
	}
	s3 := time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC) // 02:30 CEST
	e3 := time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC) // 02:30 CET
	s4 := time.Date(2026, 10, 24, 23, 0, 0, 0, time.UTC) // 01:00 CEST
	e4 := time.Date(2026, 10, 25, 3, 0, 0, 0, time.UTC)  // 04:00 CET
	s5 := time.Date(2026, 10, 25, 22, 0, 0, 0, time.UTC) // 23:00 CET
	e5 := time.Date(2026, 10, 25, 23, 0, 0, 0, time.UTC) // 00:00 next day
	got = layout([]api.Occurrence{timed("c", s3, e3), timed("d", s4, e4), timed("e", s5, e5)}, autumn, loc, false)
	want = []placed{{"d", 0, 2, 60, 240}, {"c", 1, 2, 150, 165}, {"e", 0, 1, 1380, 1440}}
	if !samePlaced(got, want) {
		t.Errorf("autumn: %v, want %v", got, want)
	}
}

func TestAllDayLanes(t *testing.T) {
	loc := prague(t)
	week1 := ViewRange(ViewWeek, at(loc, 2026, 10, 14, 0, 0), time.Monday, loc).Days()
	week2 := ViewRange(ViewWeek, at(loc, 2026, 10, 21, 0, 0), time.Monday, loc).Days()
	span := allDayOcc("span", at(loc, 2026, 10, 17, 0, 0), 4) // Saturday to Tuesday
	a := allDayOcc("a", at(loc, 2026, 10, 12, 0, 0), 3)       // Monday to Wednesday
	b := allDayOcc("b", at(loc, 2026, 10, 13, 0, 0), 1)       // Tuesday
	c := allDayOcc("c", at(loc, 2026, 10, 15, 0, 0), 1)       // Thursday
	timedOne := timed("t", at(loc, 2026, 10, 13, 9, 0), at(loc, 2026, 10, 13, 10, 0))
	bars, lanes := AllDayLanes([]api.Occurrence{c, span, timedOne, b, a}, week1, loc, false)
	type bar struct {
		id                string
		lane, first, last int
		before, after     bool
	}
	var got []bar
	for _, x := range bars {
		got = append(got, bar{string(x.Occurrence.EventID), x.Lane, x.First, x.Last, x.ContinuesBefore, x.ContinuesAfter})
	}
	want := []bar{{"a", 0, 0, 2, false, false}, {"b", 1, 1, 1, false, false}, {"c", 0, 3, 3, false, false},
		{"span", 0, 5, 6, false, true}}
	if fmt.Sprint(got) != fmt.Sprint(want) || lanes != 2 {
		t.Errorf("week 1: %v (%d lanes), want %v (2)", got, lanes, want)
	}
	bars, lanes = AllDayLanes([]api.Occurrence{span}, week2, loc, false)
	if len(bars) != 1 || bars[0].First != 0 || bars[0].Last != 1 || !bars[0].ContinuesBefore || bars[0].ContinuesAfter || lanes != 1 {
		t.Errorf("week 2: %+v (%d lanes)", bars, lanes)
	}
	// One day of the Day view.
	bars, _ = AllDayLanes([]api.Occurrence{span}, week2[0:1], loc, false)
	if len(bars) != 1 || bars[0].First != 0 || bars[0].Last != 0 || !bars[0].ContinuesBefore || !bars[0].ContinuesAfter {
		t.Errorf("one day: %+v", bars)
	}
	if bars, n := AllDayLanes(nil, nil, loc, false); bars != nil || n != 0 {
		t.Error("no days")
	}
}

func TestNowLine(t *testing.T) {
	loc := prague(t)
	r := ViewRange(ViewWeek, at(loc, 2026, 10, 14, 0, 0), time.Monday, loc)
	if d, m, ok := NowLine(at(loc, 2026, 10, 14, 13, 37), r, loc); !ok || d != 2 || m != 817 {
		t.Errorf("NowLine = %d %d %v", d, m, ok)
	}
	if _, _, ok := NowLine(at(loc, 2026, 10, 19, 0, 0), r, loc); ok {
		t.Error("NowLine outside the range")
	}
	if m := ScrollMinute(api.WorkingHours{Start: "08:00"}); m != 420 {
		t.Errorf("ScrollMinute = %d", m)
	}
	if m := ScrollMinute(api.WorkingHours{Start: "x"}); m != 420 {
		t.Errorf("ScrollMinute(bad) = %d", m)
	}
	if m := ScrollMinute(api.WorkingHours{Start: "00:30"}); m != 0 {
		t.Errorf("ScrollMinute(early) = %d", m)
	}
}

func TestStateOf(t *testing.T) {
	tests := []struct {
		o    api.Occurrence
		want BlockState
	}{
		{api.Occurrence{Status: api.EventStatusConfirmed, ShowAs: api.ShowAsBusy}, BlockNormal},
		{api.Occurrence{MyResponse: api.ResponseAccepted}, BlockNormal},
		{api.Occurrence{MyResponse: api.ResponseTentative}, BlockTentative},
		{api.Occurrence{Status: api.EventStatusTentative}, BlockTentative},
		{api.Occurrence{ShowAs: api.ShowAsTentative}, BlockTentative},
		{api.Occurrence{MyResponse: api.ResponseNeedsAction}, BlockUnanswered},
		{api.Occurrence{Invitation: &api.MessageRef{}, ShowAs: api.ShowAsTentative}, BlockUnanswered},
		{api.Occurrence{MyResponse: api.ResponseDeclined, ShowAs: api.ShowAsTentative}, BlockDeclined},
		{api.Occurrence{Status: api.EventStatusCancelled, MyResponse: api.ResponseDeclined}, BlockCancelled},
	}
	for i, tt := range tests {
		if got := StateOf(tt.o); got != tt.want {
			t.Errorf("%d: StateOf = %v, want %v", i, got, tt.want)
		}
	}
}

func TestBlockLabel(t *testing.T) {
	loc := prague(t)
	env := envIn(loc)
	o := timed("Standup", at(loc, 2026, 10, 13, 10, 0), at(loc, 2026, 10, 13, 10, 30))
	o.MyResponse = api.ResponseNeedsAction
	if got := BlockLabel(o, env); got != "Standup, Tuesday 10:00–10:30, awaiting your answer" {
		t.Errorf("label %q", got)
	}
	o.MyResponse = ""
	if got := BlockLabel(o, env); got != "Standup, Tuesday 10:00–10:30" {
		t.Errorf("label %q", got)
	}
	ad := allDayOcc("Offsite", at(loc, 2026, 10, 15, 0, 0), 2)
	if got := BlockLabel(ad, env); got != "Offsite, Thursday, all day" {
		t.Errorf("label %q", got)
	}
	if got := BlockLabel(api.Occurrence{Title: "‮X"}, env); got != "X" {
		t.Errorf("label of a bare occurrence %q", got)
	}
}
