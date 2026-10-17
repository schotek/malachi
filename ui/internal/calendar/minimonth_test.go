// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"slices"
	"testing"
	"time"
)

// The sidebar month: the grid of MonthCells, today, and the band of the
// shown week with its ends (Swift CalendarMonthLayoutTests.miniMonth).
func TestMiniMonth(t *testing.T) {
	p := prague(t)
	env := envIn(p)
	now := at(p, 2026, 10, 16, 12, 0)
	week := ViewRange(ViewWeek, now, time.Monday, p)
	m := NewMiniMonth(now, time.Monday, now, &week, env)
	if m.Title != "October 2026" || !m.Month.Equal(at(p, 2026, 10, 1, 0, 0)) {
		t.Fatalf("title %q month %v", m.Title, m.Month)
	}
	if m.Headers[0] != "Mon" || len(m.Days) != 42 || m.WeekNumbers != [6]int{40, 41, 42, 43, 44, 45} {
		t.Fatalf("headers %v weeks %v", m.Headers, m.WeekNumbers)
	}
	var today, shown []int
	for i, d := range m.Days {
		if d.Today {
			today = append(today, i)
		}
		if d.Shown {
			shown = append(shown, i)
		}
	}
	if !slices.Equal(today, []int{18}) || !slices.Equal(shown, []int{14, 15, 16, 17, 18, 19, 20}) {
		t.Fatalf("today %v shown %v", today, shown)
	}
	if !(m.Days[14].BandStart && !m.Days[14].BandEnd && m.Days[20].BandEnd && !m.Days[15].BandStart) {
		t.Error("band ends")
	}
	if m.Days[0].Number != "28" || m.Days[0].InMonth || m.Days[3].Number != "1" || !m.Days[3].InMonth {
		t.Error("numbers")
	}
	if i, ok := m.Index(at(p, 2026, 10, 16, 23, 59), p); !ok || i != 18 {
		t.Errorf("index %d %v", i, ok)
	}
	if _, ok := m.Index(at(p, 2027, 1, 1, 0, 0), p); ok {
		t.Error("outside the grid")
	}
	// A range across rows is banded per row; no range bands nothing.
	agenda := ViewRange(ViewAgenda, at(p, 2026, 10, 30, 0, 0), time.Monday, p)
	a := NewMiniMonth(now, time.Monday, now, &agenda, env)
	if !(a.Days[32].Shown && a.Days[32].BandStart && a.Days[34].BandEnd && a.Days[35].BandStart) {
		t.Error("agenda band")
	}
	for _, d := range NewMiniMonth(now, time.Monday, now, nil, env).Days {
		if d.Shown {
			t.Fatal("no range, no band")
		}
	}
	if got := StepMonth(at(p, 2026, 12, 31, 0, 0), 1, p); !got.Equal(at(p, 2027, 1, 1, 0, 0)) {
		t.Errorf("step +1 %v", got)
	}
	if got := StepMonth(at(p, 2026, 3, 31, 0, 0), -1, p); !got.Equal(at(p, 2026, 2, 1, 0, 0)) {
		t.Errorf("step -1 %v", got)
	}
}
