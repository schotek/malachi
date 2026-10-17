// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
)

// The Month view: a 6×7 grid of days from the week that holds the 1st,
// the week numbers at the left, how many pills a cell has room for and
// which pills a day shows, with "+2 more" for the rest.

// Cell is one day of the month grid.
type Cell struct {
	// Day is the midnight of the day.
	Day time.Time
	// InMonth: the day belongs to the grid's month (others are grey).
	InMonth bool
	// Today: the day is today (ringed).
	Today bool
	// Weekend: Saturday or Sunday (shaded).
	Weekend bool
}

// MonthGrid is the month view of one month.
type MonthGrid struct {
	// Month is the midnight of the month's 1st.
	Month time.Time
	// Cells are the 42 days, row by row.
	Cells [42]Cell
	// WeekNumbers are the numbers of the six rows' weeks (WeekNumber).
	WeekNumbers [6]int
}

// gridStart is the first day of the grid of anchor's month: the start of
// the week that holds the 1st.
func gridStart(anchor time.Time, weekStart time.Weekday, loc *time.Location) time.Time {
	a := anchor.In(loc)
	first := time.Date(a.Year(), a.Month(), 1, 0, 0, 0, 0, loc)
	return StartOfWeek(first, weekStart, loc)
}

// MonthCells is the grid of anchor's month: six weeks from the week that
// holds the 1st (a month that starts on the week's first day starts in
// the first row), with today marked.
func MonthCells(anchor time.Time, weekStart time.Weekday, now time.Time, loc *time.Location) MonthGrid {
	a := anchor.In(loc)
	g := MonthGrid{Month: time.Date(a.Year(), a.Month(), 1, 0, 0, 0, 0, loc)}
	d := gridStart(anchor, weekStart, loc)
	today := StartOfDay(now, loc)
	for i := range g.Cells {
		g.Cells[i] = Cell{Day: d, InMonth: d.Month() == g.Month.Month(), Today: d.Equal(today),
			Weekend: d.Weekday() == time.Saturday || d.Weekday() == time.Sunday}
		if i%7 == 0 {
			g.WeekNumbers[i/7] = WeekNumber(d, weekStart, loc)
		}
		d = AddDays(d, 1)
	}
	return g
}

// WeekdayHeaders are the seven days of the grid's header from weekStart,
// abbreviated ("Mon").
func WeekdayHeaders(weekStart time.Weekday, env Env) []string {
	// Any week will do: 2026-10-04 is a Sunday.
	sunday := time.Date(2026, time.October, 4, 12, 0, 0, 0, env.loc())
	out := make([]string, 7)
	for i := range out {
		out[i] = ShortWeekdayText(AddDays(sunday, (int(weekStart)+i)%7), env)
	}
	return out
}

// CellCapacity is how many lines of pills a cell's content area holds:
// cellHeight is the room below the day number, less the lanes of
// spanning bars of its week row (MonthLanes, LaneCount) of laneHeight
// each; lineHeight is one pill's height; at least 0.
func CellCapacity(cellHeight, lineHeight float64, lanes int, laneHeight float64) int {
	room := cellHeight - float64(max(0, lanes))*max(0, laneHeight)
	if lineHeight <= 0 || room <= 0 {
		return 0
	}
	return int(room / lineHeight)
}

// MonthSpan is an occurrence drawn as one bar across the days it covers
// in a week row of the month grid (like Apple Calendar): an all-day event,
// or a timed one that crosses midnight.
type MonthSpan struct {
	Occurrence api.Occurrence
	// Key is OccurrenceKey, Title the cleaned title ("" shows Untitled),
	// CalendarID the source of its colour.
	Key        string
	Title      string
	CalendarID api.CalendarID
	// Lane is the bar's row (0-based) under the day numbers.
	Lane int
	// StartCol and EndCol are its first and last column (0–6, inclusive),
	// clipped to the week.
	StartCol, EndCol int
	// ContinuesBefore/After: the occurrence goes on before the row's first
	// day or after its last.
	ContinuesBefore, ContinuesAfter bool
	State                           BlockState
}

// spanDays is an occurrence's first day and its last day touched (a
// timed one ending exactly at a midnight does not touch that day).
func spanDays(o api.Occurrence, loc *time.Location) (first, last time.Time, ok bool) {
	s, e, ok := Span(o, loc)
	if !ok {
		return time.Time{}, time.Time{}, false
	}
	first = StartOfDay(s, loc)
	if o.AllDay {
		return first, AddDays(StartOfDay(e, loc), -1), true
	}
	return first, AddDays(first, occurrenceDays(s, e, loc)-1), true
}

// IsMonthSpan reports an occurrence the month grid draws as a bar: all
// day, or timed over more than one calendar day. A timed one within a
// day stays a pill (CellItems).
func IsMonthSpan(o api.Occurrence, loc *time.Location) bool {
	first, last, ok := spanDays(o, loc)
	return ok && (o.AllDay || last.After(first))
}

// MonthLanes are the bars of one week row (weekDays: its seven midnights
// in order): sorted by first column, then the longer first, then by
// OccurrenceKey; each takes the lowest lane free over its columns.
// Without showDeclined declined occurrences are left out.
func MonthLanes(occurrences []api.Occurrence, weekDays [7]time.Time, loc *time.Location, showDeclined bool) []MonthSpan {
	from, to := weekDays[0], AddDays(weekDays[6], 1)
	var spans []MonthSpan
	seen := map[string]bool{}
	for _, o := range occurrences {
		key := OccurrenceKey(o)
		if seen[key] || !Shown(o, showDeclined) || !IsMonthSpan(o, loc) || !Overlaps(o, from, to, loc) {
			continue
		}
		seen[key] = true
		first, last, _ := spanDays(o, loc)
		spans = append(spans, MonthSpan{Occurrence: o, Key: key, Title: board.CleanLine(o.Title, api.MaxEventTitleBytes), CalendarID: o.CalendarID,
			StartCol: max(0, DaysBetween(from, first, loc)), EndCol: min(6, DaysBetween(from, last, loc)),
			ContinuesBefore: first.Before(from), ContinuesAfter: !last.Before(to), State: StateOf(o)})
	}
	slices.SortStableFunc(spans, func(a, b MonthSpan) int {
		return cmp.Or(cmp.Compare(a.StartCol, b.StartCol), cmp.Compare(b.EndCol-b.StartCol, a.EndCol-a.StartCol),
			cmp.Compare(a.Key, b.Key))
	})
	var laneEnds []int
	for i := range spans {
		lane := -1
		for l, end := range laneEnds {
			if end < spans[i].StartCol {
				lane = l
				break
			}
		}
		if lane < 0 {
			lane = len(laneEnds)
			laneEnds = append(laneEnds, 0)
		}
		laneEnds[lane] = spans[i].EndCol
		spans[i].Lane = lane
	}
	return spans
}

// LaneCount is the number of lanes the spans of a row use.
func LaneCount(spans []MonthSpan) int {
	n := 0
	for _, s := range spans {
		n = max(n, s.Lane+1)
	}
	return n
}

// Pill is an occurrence in a month cell.
type Pill struct {
	Occurrence api.Occurrence
	// Start is the occurrence's start in the zone (for a timed pill's
	// time, TimeText).
	Start time.Time
	// ContinuesBefore: a timed occurrence that started an earlier day;
	// its pill shows no time.
	ContinuesBefore bool
	State           BlockState
}

// CellItems are the pills of day in a cell of capacity lines: the timed
// occurrences within one day (the others are MonthLanes' bars) by start,
// then by end, then by OccurrenceKey. When they do not all fit, the last line is "+n more" (More) and
// the second result is n; 0 when everything fits.
func CellItems(occurrences []api.Occurrence, day time.Time, capacity int, loc *time.Location, showDeclined bool) ([]Pill, int) {
	dayStart := StartOfDay(day, loc)
	dayEnd := AddDays(dayStart, 1)
	type item struct {
		p          Pill
		start, end time.Time
	}
	var items []item
	for _, o := range occurrences {
		if !Shown(o, showDeclined) || IsMonthSpan(o, loc) || !Overlaps(o, dayStart, dayEnd, loc) {
			continue
		}
		s, e, _ := Span(o, loc)
		items = append(items, item{Pill{Occurrence: o, Start: s, ContinuesBefore: !o.AllDay && s.Before(dayStart),
			State: StateOf(o)}, s, e})
	}
	slices.SortStableFunc(items, func(a, b item) int {
		if a.p.Occurrence.AllDay != b.p.Occurrence.AllDay {
			if a.p.Occurrence.AllDay {
				return -1
			}
			return 1
		}
		if a.p.Occurrence.AllDay {
			return cmp.Or(a.start.Compare(b.start), b.end.Compare(a.end),
				cmp.Compare(OccurrenceKey(a.p.Occurrence), OccurrenceKey(b.p.Occurrence)))
		}
		return cmp.Or(a.start.Compare(b.start), a.end.Compare(b.end),
			cmp.Compare(OccurrenceKey(a.p.Occurrence), OccurrenceKey(b.p.Occurrence)))
	})
	pills := make([]Pill, 0, len(items))
	for _, it := range items {
		pills = append(pills, it.p)
	}
	if len(pills) <= capacity {
		return pills, 0
	}
	shown := max(0, capacity-1)
	return pills[:shown], len(pills) - shown
}

// More is the last line of a full cell, "+2 more" (a button that lists
// the day).
func More(n int, tr Translator) string {
	// TRANSLATORS: the last line of a day of the month view that has more
	// events than room; %d is how many are not shown.
	return fmt.Sprintf(tr.N("+%d more", "+%d more", n), n)
}
