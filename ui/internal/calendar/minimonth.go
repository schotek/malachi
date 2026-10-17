// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import "time"

// The sidebar's month (docs/calendar.md §5.2): the 6×7 days of a month,
// today, and the band of the days the page shows, built from MonthCells.
// Port of macos MalachiCore/Calendar/MiniMonth.swift (Cal.MiniMonth).

// MiniMonthDay is one day of the mini month's grid.
type MiniMonthDay struct {
	// Date is the midnight of the day.
	Date time.Time
	// Number is the day of the month, "14" (DayText).
	Number  string
	InMonth bool
	Today   bool
	Weekend bool
	// Shown: the day is in the range the page shows (drawn banded).
	Shown bool
	// BandStart and BandEnd: the first and last shown day of its row, for
	// the band's rounded ends.
	BandStart, BandEnd bool
}

// MiniMonth is the sidebar's month.
type MiniMonth struct {
	// Month is the midnight of the month's 1st.
	Month time.Time
	// Title is "October 2026".
	Title string
	// Headers are the seven weekday headers from the week's first day.
	Headers []string
	// Days are the 42 days, row by row.
	Days [42]MiniMonthDay
	// WeekNumbers are the six rows' week numbers.
	WeekNumbers [6]int
}

// NewMiniMonth is the month of anchor with shown banded (nil bands
// nothing); the range only colours days and may lie partly or wholly
// outside the grid. Swift: Cal.MiniMonth.init.
func NewMiniMonth(anchor time.Time, weekStart time.Weekday, now time.Time, shown *Range, env Env) MiniMonth {
	g := MonthCells(anchor, weekStart, now, env.loc())
	m := MiniMonth{Month: g.Month, Title: MonthYearText(g.Month, env), Headers: WeekdayHeaders(weekStart, env),
		WeekNumbers: g.WeekNumbers}
	var flags [42]bool
	for i, c := range g.Cells {
		flags[i] = shown != nil && shown.Contains(c.Day)
	}
	for i, c := range g.Cells {
		col := i % 7
		on := flags[i]
		m.Days[i] = MiniMonthDay{Date: c.Day, Number: DayText(c.Day, env), InMonth: c.InMonth, Today: c.Today,
			Weekend: c.Weekend, Shown: on,
			BandStart: on && (col == 0 || !flags[i-1]),
			BandEnd:   on && (col == 6 || !flags[i+1])}
	}
	return m
}

// StepMonth is the anchor of the month before (dir -1) or after (+1): the
// 1st. Swift: Cal.MiniMonth.step.
func StepMonth(month time.Time, dir int, loc *time.Location) time.Time {
	m := month.In(loc)
	return time.Date(m.Year(), m.Month()+time.Month(dir), 1, 0, 0, 0, 0, loc)
}

// Index is the index of the day t falls on; false outside the grid.
// Swift: Cal.MiniMonth.index(of:zone:).
func (m MiniMonth) Index(t time.Time, loc *time.Location) (int, bool) {
	d := StartOfDay(t, loc)
	for i, c := range m.Days {
		if c.Date.Equal(d) {
			return i, true
		}
	}
	return 0, false
}
