// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"slices"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The Board → Today strip "Today in Calendar": today's occurrences with
// their time range (or All Day), title and place, "took place" for the
// ones over, Respond for unanswered invitations, and Open Calendar. The
// strip is hidden when no account has the calendar capability.

// TodayRow is one row of the strip.
type TodayRow struct {
	Occurrence api.Occurrence
	// Time is the time range, All Day, or a part of an occurrence over
	// several days (ItemTime).
	Time  string
	Title string
	// Place is the location ("" none).
	Place string
	// TookPlace: the occurrence ended before now.
	TookPlace bool
	// Respond: an unanswered invitation (the row offers Respond).
	Respond bool
	State   BlockState
}

// TodayRows are the strip's rows of now's day: all-day first, then by
// start (Agenda's order).
func TodayRows(occurrences []api.Occurrence, now time.Time, env Env, showDeclined bool) []TodayRow {
	days := Agenda(occurrences, now, 1, now, env, showDeclined)
	if len(days) == 0 {
		return nil
	}
	rows := make([]TodayRow, 0, len(days[0].Items))
	for _, it := range days[0].Items {
		rows = append(rows, TodayRow{
			Occurrence: it.Occurrence,
			Time:       ItemTime(it, env),
			Title:      cleanTitle(it.Occurrence.Title, env.Tr),
			Place:      cleanLine(it.Occurrence.Location),
			TookPlace:  !it.End.After(now),
			Respond:    it.State == BlockUnanswered,
			State:      it.State,
		})
	}
	return rows
}

// ShowsTodayStrip reports the strip: some account has the calendar
// capability.
func ShowsTodayStrip(accounts []api.Account) bool {
	return slices.ContainsFunc(accounts, func(a api.Account) bool { return a.Can(api.CapabilityCalendar) })
}

// TodayHeading is the strip's heading.
func TodayHeading(tr Translator) string {
	// TRANSLATORS: the heading of the Board's Today page strip with today's
	// events.
	return tr.T("Today in Calendar")
}

// OpenCalendar is the strip's link to Calendar mode.
func OpenCalendar(tr Translator) string { return tr.T("Open Calendar") }

// TookPlace is the note of a row that is over.
func TookPlace(tr Translator) string {
	// TRANSLATORS: beside an event of today that is already over.
	return tr.T("took place")
}

// Respond is the button of an unanswered invitation's row.
func Respond(tr Translator) string {
	// TRANSLATORS: the button that answers an invitation from the Board's
	// Today page.
	return tr.T("Respond")
}

// NothingToday is the strip's text without events today.
func NothingToday(tr Translator) string { return tr.T("Nothing in the calendar today.") }
