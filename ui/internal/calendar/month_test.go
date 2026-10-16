// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestMonthCells(t *testing.T) {
	loc := prague(t)
	now := at(loc, 2026, 10, 16, 12, 0)
	// 2026: the 1st falls on every weekday in one of these months.
	months := map[time.Weekday]time.Month{time.Sunday: 2, time.Monday: 6, time.Tuesday: 9, time.Wednesday: 4,
		time.Thursday: 10, time.Friday: 5, time.Saturday: 8}
	for wd, m := range months {
		for _, ws := range []time.Weekday{time.Monday, time.Sunday} {
			g := MonthCells(at(loc, 2026, m, 15, 0, 0), ws, now, loc)
			first := at(loc, 2026, m, 1, 0, 0)
			if first.Weekday() != wd {
				t.Fatalf("%v starts on %v", m, first.Weekday())
			}
			lead := (int(wd) - int(ws) + 7) % 7
			if g.Cells[0].Day.Weekday() != ws || !g.Cells[lead].Day.Equal(first) {
				t.Errorf("%v from %v: the 1st at %v, first cell %v", m, ws, lead, g.Cells[0].Day)
			}
			days := AddDays(time.Date(2026, m+1, 1, 0, 0, 0, 0, loc), -1).Day()
			in := 0
			for i, c := range g.Cells {
				if i > 0 && DaysBetween(g.Cells[i-1].Day, c.Day, loc) != 1 {
					t.Errorf("%v: cells %d and %d are not consecutive", m, i-1, i)
				}
				if c.InMonth {
					in++
				}
				if c.Weekend != (c.Day.Weekday() == time.Saturday || c.Day.Weekday() == time.Sunday) {
					t.Errorf("%v: weekend of %v", m, c.Day)
				}
				if c.Day.Hour() != 0 {
					t.Errorf("%v: cell %v not at midnight", m, c.Day)
				}
			}
			if in != days {
				t.Errorf("%v from %v: %d days in the month, want %d", m, ws, in, days)
			}
			for r := range 6 {
				if g.WeekNumbers[r] != WeekNumber(g.Cells[r*7].Day, ws, loc) {
					t.Errorf("%v: week number of row %d", m, r)
				}
			}
		}
	}
	g := MonthCells(now, time.Monday, now, loc)
	var today []int
	for i, c := range g.Cells {
		if c.Today {
			today = append(today, i)
		}
	}
	if !slices.Equal(today, []int{18}) || g.WeekNumbers != [6]int{40, 41, 42, 43, 44, 45} {
		t.Errorf("October: today at %v, weeks %v", today, g.WeekNumbers)
	}
	gs := MonthCells(now, time.Sunday, now, loc)
	if gs.WeekNumbers != [6]int{40, 41, 42, 43, 44, 45} || gs.Cells[0].Day.Day() != 27 {
		t.Errorf("October from Sunday: weeks %v", gs.WeekNumbers)
	}
}

func TestWeekdayHeaders(t *testing.T) {
	env := envIn(time.UTC)
	if got := WeekdayHeaders(time.Monday, env); fmt.Sprint(got) != "[Mon Tue Wed Thu Fri Sat Sun]" {
		t.Errorf("Monday: %v", got)
	}
	if got := WeekdayHeaders(time.Sunday, env); fmt.Sprint(got) != "[Sun Mon Tue Wed Thu Fri Sat]" {
		t.Errorf("Sunday: %v", got)
	}
	if got := WeekdayHeaders(time.Saturday, env); got[0] != "Sat" || got[6] != "Fri" {
		t.Errorf("Saturday: %v", got)
	}
}

func TestCellCapacity(t *testing.T) {
	tests := []struct {
		h, line float64
		want    int
		lanes   int
		laneH   float64
	}{{100, 20, 5, 0, 0}, {99.9, 20, 4, 0, 0}, {19, 20, 0, 0, 0}, {0, 20, 0, 0, 0}, {50, 0, 0, 0, 0}, {-5, 10, 0, 0, 0},
		{80, 16, 5, 0, 16}, {100, 20, 3, 2, 18}, {100, 20, 0, 6, 18}, {100, 20, 5, -1, 18}}
	for _, tt := range tests {
		if got := CellCapacity(tt.h, tt.line, tt.lanes, tt.laneH); got != tt.want {
			t.Errorf("CellCapacity(%v, %v) = %d, want %d", tt.h, tt.line, got, tt.want)
		}
	}
}

func TestCellItems(t *testing.T) {
	loc := prague(t)
	day := at(loc, 2026, 10, 14, 0, 0)
	occ := []api.Occurrence{
		timed("t2", at(loc, 2026, 10, 14, 14, 0), at(loc, 2026, 10, 14, 15, 0)),
		timed("t1", at(loc, 2026, 10, 14, 9, 0), at(loc, 2026, 10, 14, 10, 0)),
		timed("t1b", at(loc, 2026, 10, 14, 9, 0), at(loc, 2026, 10, 14, 9, 30)),
		allDayOcc("one", day, 1),
		allDayOcc("long", at(loc, 2026, 10, 13, 0, 0), 3),
		timed("night", at(loc, 2026, 10, 13, 22, 0), at(loc, 2026, 10, 14, 1, 0)),
		timed("other", at(loc, 2026, 10, 15, 9, 0), at(loc, 2026, 10, 15, 10, 0)),
	}
	ids := func(ps []Pill) string {
		var s []string
		for _, p := range ps {
			s = append(s, string(p.Occurrence.EventID))
		}
		return fmt.Sprint(s)
	}
	tests := []struct {
		capacity int
		want     string
		more     int
	}{
		{10, "[t1b t1 t2]", 0},
		{3, "[t1b t1 t2]", 0},
		{2, "[t1b]", 2},
		{1, "[]", 3},
		{0, "[]", 3},
	}
	for _, tt := range tests {
		ps, more := CellItems(occ, day, tt.capacity, loc, false)
		if ids(ps) != tt.want || more != tt.more {
			t.Errorf("capacity %d: %s +%d, want %s +%d", tt.capacity, ids(ps), more, tt.want, tt.more)
		}
	}
	if got := More(2, en{}); got != "+2 more" {
		t.Errorf("More = %q", got)
	}
}

func TestMonthLanes(t *testing.T) {
	loc := prague(t)
	g := MonthCells(at(loc, 2026, 10, 1, 0, 0), time.Monday, at(loc, 2026, 10, 16, 0, 0), loc)
	row := func(r int) (w [7]time.Time) {
		for i := range w {
			w[i] = g.Cells[r*7+i].Day
		}
		return
	}
	two := allDayOcc("two", at(loc, 2026, 10, 7, 0, 0), 2)     // Wed–Thu of row 1
	ten := allDayOcc("ten", at(loc, 2026, 10, 9, 0, 0), 10)    // Fri 9 – Sun 18: rows 1 and 2
	other := allDayOcc("other", at(loc, 2026, 10, 8, 0, 0), 3) // Thu–Sat, overlaps both
	night := timed("night", at(loc, 2026, 10, 13, 22, 0), at(loc, 2026, 10, 14, 2, 0))
	inDay := timed("day", at(loc, 2026, 10, 13, 9, 0), at(loc, 2026, 10, 13, 10, 0))
	toMidnight := timed("mid", at(loc, 2026, 10, 13, 23, 0), at(loc, 2026, 10, 14, 0, 0))
	occ := []api.Occurrence{night, ten, inDay, other, two, toMidnight, two}
	type span struct {
		key              string
		lane, start, end int
		before, after    bool
	}
	got := func(r int) []span {
		var out []span
		for _, s := range MonthLanes(occ, row(r), loc, false) {
			out = append(out, span{s.Key, s.Lane, s.StartCol, s.EndCol, s.ContinuesBefore, s.ContinuesAfter})
		}
		return out
	}
	// Row 1 is Mon 5 – Sun 11 October.
	want1 := []span{{"two|", 0, 2, 3, false, false}, {"other|", 1, 3, 5, false, false}, {"ten|", 0, 4, 6, false, true}}
	if fmt.Sprint(got(1)) != fmt.Sprint(want1) {
		t.Errorf("row 1: %v, want %v", got(1), want1)
	}
	// Row 2 is Mon 12 – Sun 18 October.
	want2 := []span{{"ten|", 0, 0, 6, true, false}, {"night|", 1, 1, 2, false, false}}
	if fmt.Sprint(got(2)) != fmt.Sprint(want2) {
		t.Errorf("row 2: %v, want %v", got(2), want2)
	}
	if n := LaneCount(MonthLanes(occ, row(1), loc, false)); n != 2 {
		t.Errorf("row 1 lanes %d", n)
	}
	if LaneCount(nil) != 0 || len(MonthLanes(occ, row(4), loc, false)) != 0 {
		t.Error("an empty row")
	}
	if IsMonthSpan(inDay, loc) || IsMonthSpan(toMidnight, loc) || !IsMonthSpan(night, loc) || !IsMonthSpan(two, loc) {
		t.Error("IsMonthSpan")
	}
	// The pills of Tuesday 13 leave out the night's bar; the capacity of
	// row 2 loses its two lanes.
	pills, _ := CellItems(occ, at(loc, 2026, 10, 13, 0, 0), 10, loc, false)
	if len(pills) != 2 || pills[0].Occurrence.EventID != "day" || pills[1].Occurrence.EventID != "mid" {
		t.Errorf("pills %+v", pills)
	}
	if c := CellCapacity(100, 20, LaneCount(MonthLanes(occ, row(2), loc, false)), 18); c != 3 {
		t.Errorf("capacity %d", c)
	}
	declined := two
	declined.MyResponse = api.ResponseDeclined
	if len(MonthLanes([]api.Occurrence{declined}, row(1), loc, false)) != 0 ||
		len(MonthLanes([]api.Occurrence{declined}, row(1), loc, true)) != 1 {
		t.Error("declined bars")
	}
}
