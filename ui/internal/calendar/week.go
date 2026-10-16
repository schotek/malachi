// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The time grid of the Week and Day views: the timed blocks of a day with
// their columns, the all-day row's lanes, the now line, and how a block is
// drawn. Positions are wall-clock minutes from midnight (0–1440), so the
// grid always has 24 rows: on the day the clocks spring forward an hour
// is missing from the events, not from the grid; on the day they fall
// back the repeated hour folds onto one row (a block of that hour keeps
// the minimum height).

// DayMinutes is the height of the grid in minutes.
const DayMinutes = 24 * 60

// MinBlockMinutes is the shortest block drawn.
const MinBlockMinutes = 15

// BlockState is how a block, bar or pill is drawn.
type BlockState int

// The states.
const (
	// BlockNormal: filled with the calendar's colour.
	BlockNormal BlockState = iota
	// BlockTentative: answered tentatively, or tentative by status or
	// show-as: diagonal stripes.
	BlockTentative
	// BlockUnanswered: an invitation waiting for the user's answer: a
	// dashed outline with a "?" badge.
	BlockUnanswered
	// BlockDeclined: declined by the user: struck through (shown only with
	// the preference showDeclined).
	BlockDeclined
	// BlockCancelled: the organiser cancelled it: struck through and faded.
	BlockCancelled
)

// StateOf is how an occurrence is drawn: cancelled, then declined, then
// unanswered (an invitation not in a calendar, or one the user has not
// answered), then tentative.
func StateOf(o api.Occurrence) BlockState {
	switch {
	case o.Status == api.EventStatusCancelled:
		return BlockCancelled
	case o.MyResponse == api.ResponseDeclined:
		return BlockDeclined
	case o.Invitation != nil || o.MyResponse == api.ResponseNeedsAction:
		return BlockUnanswered
	case o.MyResponse == api.ResponseTentative || o.Status == api.EventStatusTentative ||
		o.ShowAs == api.ShowAsTentative:
		return BlockTentative
	}
	return BlockNormal
}

// Shown reports an occurrence the views draw: declined ones only with
// showDeclined.
func Shown(o api.Occurrence, showDeclined bool) bool {
	return showDeclined || o.MyResponse != api.ResponseDeclined
}

// Block is a timed occurrence placed in a day's column.
type Block struct {
	Occurrence api.Occurrence
	// Start and End are the occurrence clipped to the day.
	Start, End time.Time
	// Top and Bottom are wall-clock minutes from midnight, Bottom ≥ Top +
	// MinBlockMinutes and ≤ DayMinutes.
	Top, Bottom int
	// Column is the block's column (0-based) among Columns side by side.
	Column, Columns int
	// ContinuesBefore/After: the occurrence started the day before or
	// ends after this day.
	ContinuesBefore, ContinuesAfter bool
	State                           BlockState
}

// wallMinutes is t's wall-clock minutes from the midnight of day (1440
// when t is at or after the next midnight).
func wallMinutes(t, dayStart, dayEnd time.Time) int {
	if !t.Before(dayEnd) {
		return DayMinutes
	}
	if !t.After(dayStart) {
		return 0
	}
	return t.Hour()*60 + t.Minute()
}

// LayoutDay places the timed occurrences that touch day (any time of it)
// in columns: occurrences are sorted by start, then the longer first,
// then by OccurrenceKey; each joins the first column whose last block ended by its
// start (greedy interval colouring); a cluster of transitively
// overlapping blocks shares one column count. All-day occurrences and,
// without showDeclined, declined ones are left out.
func LayoutDay(occurrences []api.Occurrence, day time.Time, loc *time.Location, showDeclined bool) []Block {
	dayStart := StartOfDay(day, loc)
	dayEnd := AddDays(dayStart, 1)
	var blocks []Block
	for _, o := range occurrences {
		if o.AllDay || !Shown(o, showDeclined) || !Overlaps(o, dayStart, dayEnd, loc) {
			continue
		}
		s, e, _ := Span(o, loc)
		b := Block{Occurrence: o, Start: maxTime(s, dayStart), End: minTime(e, dayEnd),
			ContinuesBefore: s.Before(dayStart), ContinuesAfter: e.After(dayEnd), State: StateOf(o)}
		b.Top = wallMinutes(b.Start, dayStart, dayEnd)
		b.Bottom = wallMinutes(b.End, dayStart, dayEnd)
		if b.Bottom < b.Top+MinBlockMinutes {
			b.Bottom = b.Top + MinBlockMinutes
		}
		if b.Bottom > DayMinutes {
			b.Bottom = DayMinutes
			b.Top = DayMinutes - MinBlockMinutes
		}
		blocks = append(blocks, b)
	}
	slices.SortStableFunc(blocks, func(a, b Block) int {
		return cmp.Or(cmp.Compare(a.Top, b.Top), cmp.Compare(b.Bottom-b.Top, a.Bottom-a.Top),
			a.Start.Compare(b.Start), cmp.Compare(OccurrenceKey(a.Occurrence), OccurrenceKey(b.Occurrence)))
	})
	assignColumns(blocks)
	return blocks
}

// assignColumns colours the sorted blocks cluster by cluster.
func assignColumns(blocks []Block) {
	start, clusterEnd := 0, -1
	var ends []int // the bottom of each column's last block
	flush := func(to int) {
		for i := start; i < to; i++ {
			blocks[i].Columns = len(ends)
		}
	}
	for i := range blocks {
		b := &blocks[i]
		if b.Top >= clusterEnd {
			flush(i)
			start, ends = i, ends[:0]
		}
		col := -1
		for c, end := range ends {
			if end <= b.Top {
				col = c
				break
			}
		}
		if col < 0 {
			col = len(ends)
			ends = append(ends, 0)
		}
		ends[col] = b.Bottom
		b.Column = col
		clusterEnd = max(clusterEnd, b.Bottom)
	}
	flush(len(blocks))
}

// Bar is an all-day occurrence in the all-day row of a range of days.
type Bar struct {
	Occurrence api.Occurrence
	// Lane is the bar's row (0-based) in the all-day area.
	Lane int
	// First and Last are the indexes of the bar's first and last day in
	// the days given.
	First, Last int
	// ContinuesBefore/After: the event goes on before the first day or
	// after the last day shown.
	ContinuesBefore, ContinuesAfter bool
	State                           BlockState
}

// AllDayLanes places the all-day occurrences that touch days (consecutive
// midnights, a week or a day) in lanes: sorted by first day, then the
// longer first, then by OccurrenceKey; each takes the lowest lane free over its
// days. The second result is the number of lanes.
func AllDayLanes(occurrences []api.Occurrence, days []time.Time, loc *time.Location, showDeclined bool) ([]Bar, int) {
	if len(days) == 0 {
		return nil, 0
	}
	from, to := days[0], AddDays(days[len(days)-1], 1)
	var bars []Bar
	for _, o := range occurrences {
		if !o.AllDay || !Shown(o, showDeclined) || !Overlaps(o, from, to, loc) {
			continue
		}
		s, e, _ := Span(o, loc)
		first := max(0, DaysBetween(from, s, loc))
		last := min(len(days)-1, DaysBetween(from, e, loc)-1)
		bars = append(bars, Bar{Occurrence: o, First: first, Last: last,
			ContinuesBefore: s.Before(from), ContinuesAfter: e.After(to), State: StateOf(o)})
	}
	slices.SortStableFunc(bars, func(a, b Bar) int {
		return cmp.Or(cmp.Compare(a.First, b.First), cmp.Compare(b.Last-b.First, a.Last-a.First),
			cmp.Compare(OccurrenceKey(a.Occurrence), OccurrenceKey(b.Occurrence)))
	})
	var laneEnds []int // the last day taken in each lane
	for i := range bars {
		lane := -1
		for l, end := range laneEnds {
			if end < bars[i].First {
				lane = l
				break
			}
		}
		if lane < 0 {
			lane = len(laneEnds)
			laneEnds = append(laneEnds, 0)
		}
		laneEnds[lane] = bars[i].Last
		bars[i].Lane = lane
	}
	return bars, len(laneEnds)
}

// NowLine is where the red line of now goes: the index of today among the
// range's days and the wall-clock minute; false when now is outside r.
func NowLine(now time.Time, r Range, loc *time.Location) (day, minute int, ok bool) {
	n := now.In(loc)
	if !r.Contains(n) {
		return 0, 0, false
	}
	return DaysBetween(r.From, n, loc), n.Hour()*60 + n.Minute(), true
}

// ScrollMinute is the minute a time grid scrolls to at its first show:
// the start of the working hours ("HH:MM"), an hour before when there is
// room; 7:00 when the preference cannot be read.
func ScrollMinute(wh api.WorkingHours) int {
	m, ok := ParseClock(wh.Start)
	if !ok {
		m = 8 * 60
	}
	return max(0, m-60)
}

// ParseClock reads "HH:MM" into minutes from midnight.
func ParseClock(s string) (int, bool) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, false
	}
	return t.Hour()*60 + t.Minute(), true
}

// BlockStateText is the spoken state of a block ("" for a normal one).
func BlockStateText(s BlockState, tr Translator) string {
	switch s {
	case BlockTentative:
		// TRANSLATORS: spoken after an event of the calendar the user
		// answered tentatively.
		return tr.T("tentative")
	case BlockUnanswered:
		// TRANSLATORS: spoken after an event of the calendar the user has
		// not answered yet.
		return tr.T("awaiting your answer")
	case BlockDeclined:
		// TRANSLATORS: spoken after an event the user declined.
		return tr.T("declined")
	case BlockCancelled:
		// TRANSLATORS: spoken after an event the organiser cancelled.
		return tr.T("cancelled")
	}
	return ""
}

// BlockLabel is a block's accessibility label: "Standup, Tuesday
// 10:00–10:30, awaiting your answer".
func BlockLabel(o api.Occurrence, env Env) string {
	tr := env.Tr
	title := cleanTitle(o.Title, tr)
	var when string
	if s, e, ok := Span(o, env.loc()); ok {
		if o.AllDay {
			// TRANSLATORS: the time of an all-day event when spoken: the
			// weekday, e.g. "Tuesday, all day".
			when = fmt.Sprintf(tr.T("%s, all day"), WeekdayText(s, env))
		} else {
			// TRANSLATORS: the time of an event when spoken: the weekday and
			// the time range, e.g. "Tuesday 10:00–10:30".
			when = fmt.Sprintf(tr.T("%s %s"), WeekdayText(s, env), TimeRangeText(s, e, env))
		}
	}
	state := BlockStateText(StateOf(o), tr)
	switch {
	case when == "":
		return title
	case state == "":
		// TRANSLATORS: an event of the calendar when spoken: its title and
		// its time.
		return fmt.Sprintf(tr.T("%s, %s"), title, when)
	}
	// TRANSLATORS: an event of the calendar when spoken: its title, its
	// time and its state, e.g. "Standup, Tuesday 10:00–10:30, awaiting
	// your answer".
	return fmt.Sprintf(tr.T("%s, %s, %s"), title, when, state)
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
