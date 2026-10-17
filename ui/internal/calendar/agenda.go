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

// The Agenda view and the search results: the occurrences grouped by day
// under "Today", "Tomorrow" or the weekday and date; an occurrence over
// several days is listed on each of its days with "Day 2 of 3".

// AgendaItem is one row of the agenda.
type AgendaItem struct {
	Occurrence api.Occurrence
	// Start and End are the occurrence's span in the zone.
	Start, End time.Time
	// DayOf and Days: the row is day DayOf (1-based) of an occurrence of
	// Days days; Days is 1 for one that fits in a day.
	DayOf, Days int
	State       BlockState
}

// AgendaDay is a day of the agenda with its rows.
type AgendaDay struct {
	Day     time.Time
	Heading string
	Items   []AgendaItem
}

// occurrenceDays is how many calendar days an occurrence touches: a
// timed one ending exactly at a midnight does not touch that day.
func occurrenceDays(start, end time.Time, loc *time.Location) int {
	if !end.After(start) {
		return 1
	}
	last := end
	if StartOfDay(end, loc).Equal(end.In(loc)) {
		last = end.Add(-time.Nanosecond)
	}
	return DaysBetween(start, last, loc) + 1
}

// Agenda groups the occurrences that touch [from, from + days) by day,
// only days that have some, each day's rows all-day first, then by
// start, end and Key.
func Agenda(occurrences []api.Occurrence, from time.Time, days int, now time.Time, env Env, showDeclined bool) []AgendaDay {
	loc := env.loc()
	first := StartOfDay(from, loc)
	byDay := map[int][]AgendaItem{}
	for _, o := range occurrences {
		if !Shown(o, showDeclined) {
			continue
		}
		s, e, ok := Span(o, loc)
		if !ok {
			continue
		}
		n := occurrenceDays(s, e, loc)
		for i := range n {
			idx := DaysBetween(first, s, loc) + i
			if idx < 0 || idx >= days {
				continue
			}
			byDay[idx] = append(byDay[idx], AgendaItem{Occurrence: o, Start: s, End: e, DayOf: i + 1, Days: n,
				State: StateOf(o)})
		}
	}
	idxs := make([]int, 0, len(byDay))
	for i := range byDay {
		idxs = append(idxs, i)
	}
	slices.Sort(idxs)
	out := make([]AgendaDay, 0, len(idxs))
	for _, i := range idxs {
		items := byDay[i]
		slices.SortStableFunc(items, compareItems)
		d := AddDays(first, i)
		out = append(out, AgendaDay{Day: d, Heading: DayHeading(d, now, env), Items: items})
	}
	return out
}

func compareItems(a, b AgendaItem) int {
	if a.Occurrence.AllDay != b.Occurrence.AllDay {
		if a.Occurrence.AllDay {
			return -1
		}
		return 1
	}
	return cmp.Or(a.Start.Compare(b.Start), a.End.Compare(b.End), cmp.Compare(OccurrenceKey(a.Occurrence), OccurrenceKey(b.Occurrence)))
}

// DayHeading is an agenda day's heading: Today, Tomorrow, Yesterday, else
// the weekday and date (with the year when it is not now's year).
func DayHeading(day, now time.Time, env Env) string {
	loc := env.loc()
	switch DaysBetween(now, day, loc) {
	case 0:
		// TRANSLATORS: the heading of today's events in the agenda.
		return env.Tr.T("Today")
	case 1:
		// TRANSLATORS: the heading of tomorrow's events in the agenda.
		return env.Tr.T("Tomorrow")
	case -1:
		// TRANSLATORS: the heading of yesterday's events in the agenda.
		return env.Tr.T("Yesterday")
	}
	if day.In(loc).Year() != now.In(loc).Year() {
		return LongDateText(day, env)
	}
	return WeekdayDateText(day, env)
}

// DayOfText is the note of an occurrence over several days, "Day 2 of 3".
func DayOfText(n, of int, tr Translator) string {
	// TRANSLATORS: an event that lasts several days, listed on each of its
	// days in the agenda: which day this is of how many.
	return fmt.Sprintf(tr.T("Day %d of %d"), n, of)
}

// ItemTime is a row's time column: All Day, the time range, or for a
// day of an occurrence over several days its part ("from 22:00",
// "until 02:00", All Day between).
func ItemTime(it AgendaItem, env Env) string {
	if it.Occurrence.AllDay {
		return AllDay(env.Tr)
	}
	if it.Days <= 1 {
		return TimeRangeText(it.Start, it.End, env)
	}
	switch it.DayOf {
	case 1:
		// TRANSLATORS: the first day of an event over several days in the
		// agenda: its start time.
		return fmt.Sprintf(env.Tr.T("from %s"), TimeText(it.Start, env))
	case it.Days:
		// TRANSLATORS: the last day of an event over several days in the
		// agenda: its end time.
		return fmt.Sprintf(env.Tr.T("until %s"), TimeText(it.End, env))
	}
	return AllDay(env.Tr)
}

// AgendaEmpty is the agenda's text for a range without events.
func AgendaEmpty(tr Translator) string { return tr.T("No events in this period.") }

// ResultsHeading heads the search results in place of the view.
func ResultsHeading(tr Translator) string {
	// TRANSLATORS: the heading of the event search's results.
	return tr.T("Results")
}

// NoResults is the search's text when nothing matched.
func NoResults(tr Translator) string { return tr.T("No events match the search.") }

// UnansweredHeading heads the agenda filtered to the unanswered
// invitations (the sidebar's Invitations row).
func UnansweredHeading(tr Translator) string {
	// TRANSLATORS: the sidebar's row and the agenda's heading of the
	// invitations waiting for an answer.
	return tr.T("Invitations")
}
