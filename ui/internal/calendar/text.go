// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"fmt"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
)

// The calendar's general texts: the mode, the views, the toolbar, the
// status bar, the empty states, the errors by code, and the date formats
// every other file writes with (strftime msgids, so that a locale orders
// them its own way). The macOS client looks them up with key = msgid.

// ModeName is the mode switch's segment, the View menu item and the
// window's title in Calendar mode (board.CalendarName).
func ModeName(tr Translator) string { return board.CalendarName(tr) }

// ViewTitle is a view's segment in the toolbar and its View menu item.
func ViewTitle(v View, tr Translator) string {
	switch v {
	case ViewDay:
		// TRANSLATORS: a view of the calendar: one day.
		return tr.T("Day")
	case ViewMonth:
		// TRANSLATORS: a view of the calendar: the month's grid.
		return tr.T("Month")
	case ViewAgenda:
		// TRANSLATORS: a view of the calendar: a list of the coming events
		// by day.
		return tr.T("Agenda")
	}
	// TRANSLATORS: a view of the calendar: seven days side by side.
	return tr.T("Week")
}

// Today is the toolbar button that shows today.
func Today(tr Translator) string {
	// TRANSLATORS: the calendar's toolbar button that goes to today.
	return tr.T("Today")
}

// Previous is the tooltip of ‹ (the previous day, week or month).
func Previous(tr Translator) string {
	// TRANSLATORS: the calendar's toolbar button ‹ and its key.
	return tr.T("Previous")
}

// Next is the tooltip of › (the next day, week or month).
func Next(tr Translator) string {
	// TRANSLATORS: the calendar's toolbar button › and its key.
	return tr.T("Next")
}

// NewEvent is the "+" button, the File menu item and the editor window's
// title for a new event.
func NewEvent(tr Translator) string { return tr.T("New Event") }

// EditEvent is the editor window's title for an existing event.
func EditEvent(tr Translator) string { return tr.T("Edit Event") }

// SearchEvents is the placeholder of the toolbar's search field.
func SearchEvents(tr Translator) string { return tr.T("Search Events") }

// Untitled stands for an event without a title (a private one of a
// shared calendar, or one nobody named).
func Untitled(tr Translator) string {
	// TRANSLATORS: an event without a title.
	return tr.T("Untitled Event")
}

// AllDay is the all-day row's label, the editor's switch and the time of
// an all-day event in lists.
func AllDay(tr Translator) string {
	// TRANSLATORS: an event that lasts whole days, without a time.
	return tr.T("All Day")
}

// SyncedLine is the status bar's text when every account is synced: last
// is the oldest lastSync of the accounts.
func SyncedLine(last, now time.Time, tr Translator) string {
	// TRANSLATORS: the status bar in Calendar mode; %s is a relative time
	// such as "2 minutes ago".
	return fmt.Sprintf(tr.T("Calendars synced %s"), board.RelativeTime(last, now, tr))
}

// SyncingLine is the status bar's text while a pass runs.
func SyncingLine(tr Translator) string { return tr.T("Syncing calendars…") }

// OfflineLine is the status bar's text with every account offline.
func OfflineLine(tr Translator) string { return tr.T("Calendars are offline") }

// UnansweredCount is the status bar's and the sidebar's count of the
// invitations waiting for an answer.
func UnansweredCount(n int, tr Translator) string {
	return fmt.Sprintf(tr.N("%d unanswered invitation", "%d unanswered invitations", n), n)
}

// UnansweredRow is the sidebar's row under the Invitations heading
// (UnansweredHeading), with the count badge.
func UnansweredRow(tr Translator) string {
	// TRANSLATORS: the sidebar's row under the Invitations heading that
	// lists the invitations without an answer.
	return tr.T("Unanswered")
}

// ConsentLine says that a Microsoft account's sign-in lacks calendar
// access (state consentRequired).
func ConsentLine(tr Translator) string { return tr.T("Calendar access needs a new sign-in") }

// SignIn is the button beside ConsentLine.
func SignIn(tr Translator) string { return tr.T("Sign In…") }

// ConsentOnline is ConsentLine's advice for a GOA account (signIn false).
func ConsentOnline(tr Translator) string {
	return tr.T("Allow calendar access in the desktop’s Online Accounts settings.")
}

// StatusLine is the status bar's left text from the accounts' states:
// consent wins, then syncing, then all offline, then the synced line
// with the oldest last sync; "" when there is nothing to say.
func StatusLine(states []api.CalendarSyncState, now time.Time, tr Translator) string {
	var oldest *time.Time
	syncing, offline := false, 0
	for _, s := range states {
		switch s.Status {
		case api.CalendarSyncConsentRequired:
			return ConsentLine(tr)
		case api.CalendarSyncSyncing:
			syncing = true
		case api.CalendarSyncOffline:
			offline++
		}
		if s.LastSync != nil && (oldest == nil || s.LastSync.Before(*oldest)) {
			t := *s.LastSync
			oldest = &t
		}
	}
	switch {
	case syncing:
		return SyncingLine(tr)
	case len(states) > 0 && offline == len(states):
		return OfflineLine(tr)
	case oldest != nil:
		return SyncedLine(*oldest, now, tr)
	}
	return ""
}

// EmptyKind is why the calendar page has nothing to show.
type EmptyKind int

// The empty states.
const (
	// EmptyNoAccounts: no account has the calendar capability.
	EmptyNoAccounts EmptyKind = iota
	// EmptySyncOff: accounts could have calendars, none syncs them.
	EmptySyncOff
	// EmptyLoading: the first calendar.list has not answered.
	EmptyLoading
	// EmptyNoBackend: the daemon is not reachable.
	EmptyNoBackend
)

// EmptyTitle is an empty state's heading.
func EmptyTitle(k EmptyKind, tr Translator) string {
	switch k {
	case EmptySyncOff:
		return tr.T("Calendar Sync Is Off")
	case EmptyLoading:
		return tr.T("Loading Calendars…")
	case EmptyNoBackend:
		return tr.T("Calendar Unavailable")
	}
	return tr.T("No Calendars")
}

// EmptyBody is an empty state's explanation.
func EmptyBody(k EmptyKind, tr Translator) string {
	switch k {
	case EmptySyncOff:
		return tr.T("Turn on Sync This Account’s Calendars for a Microsoft 365 account in Preferences → Calendar.")
	case EmptyLoading:
		return ""
	case EmptyNoBackend:
		return tr.T("The Malachi Mail backend is not running.")
	}
	return tr.T("Add a CalDAV calendar, or sync the calendars of a Microsoft 365 account in Preferences → Calendar.")
}

// OpenPreferences is the empty state's button.
func OpenPreferences(tr Translator) string { return tr.T("Open Preferences") }

// PreviewOnly is what a write says over the sample data
// (MALACHI_CALENDAR_SAMPLES).
func PreviewOnly(tr Translator) string {
	// TRANSLATORS: the calendar shows invented sample data for a layout
	// check and an action that would change something was chosen.
	return tr.T("The preview cannot do that.")
}

// ReloadEvent is the button shown after a conflict error in the event
// editor: it reloads the event as the server has it, dropping the edits.
func ReloadEvent(tr Translator) string {
	// TRANSLATORS: the event editor's button after the event was changed
	// elsewhere: reload the event as the server has it, dropping the edits.
	return tr.T("Reload Event")
}

// ErrorText is the sentence of a calendar error code; false for a code
// the calendar has no sentence of (the caller's usual RPC text then).
func ErrorText(code api.ErrorCode, tr Translator) (string, bool) {
	switch code {
	case api.CodeReadOnly:
		return tr.T("This calendar or event cannot be changed."), true
	case api.CodeEventGone, api.CodeEventNotFound:
		return tr.T("The event no longer exists."), true
	case api.CodeCalendarNotFound:
		return tr.T("The calendar no longer exists."), true
	case api.CodeConflict:
		return tr.T("The event was changed elsewhere. It has been reloaded."), true
	case api.CodeInvitationsUnavailable:
		return tr.T("No account can send the invitations of this calendar. Choose a mail account for invitations in Preferences → Calendar."), true
	case api.CodeCalendarConsentRequired:
		return ConsentLine(tr), true
	case api.CodeRecurrenceUnsupported:
		return tr.T("The event repeats in a way Malachi Mail cannot show. Only its first occurrence is shown, and it can be changed only as a whole series."), true
	case api.CodeMalformedCalendar:
		return tr.T("The calendar data could not be read."), true
	case api.CodeOffline:
		return tr.T("The calendar cannot be changed while offline."), true
	}
	return "", false
}

// ---------------------------------------------------------------------------
// Date formats
// ---------------------------------------------------------------------------

// TimeText is a time of day, "10:00".
func TimeText(t time.Time, env Env) string {
	// TRANSLATORS: strftime format for a time of day, e.g. "15:04".
	// xgettext:no-c-format
	return env.date(t, env.Tr.T("%H:%M"))
}

// LongDateText is a full date with the weekday, "Tuesday 14 October 2026".
func LongDateText(t time.Time, env Env) string {
	// TRANSLATORS: strftime format for a full date with the weekday, e.g.
	// "Tuesday 14 October 2026".
	// xgettext:no-c-format
	return env.date(t, env.Tr.T("%A %-d %B %Y"))
}

// WeekdayDateText is a date with the weekday and without the year,
// "Tuesday 14 October" (the agenda's headings).
func WeekdayDateText(t time.Time, env Env) string {
	// TRANSLATORS: strftime format for a date with the weekday, without the
	// year, e.g. "Tuesday 14 October".
	// xgettext:no-c-format
	return env.date(t, env.Tr.T("%A %-d %B"))
}

// DayMonthText is "14 October".
func DayMonthText(t time.Time, env Env) string {
	// TRANSLATORS: strftime format for a day and month, e.g. "14 October".
	// xgettext:no-c-format
	return env.date(t, env.Tr.T("%-d %B"))
}

// DayMonthYearText is "14 October 2026".
func DayMonthYearText(t time.Time, env Env) string {
	// TRANSLATORS: strftime format for a date, e.g. "14 October 2026".
	// xgettext:no-c-format
	return env.date(t, env.Tr.T("%-d %B %Y"))
}

// DayText is the day of the month alone, "14" (the start of "12–18
// October", the month grid's cells, the date tile).
func DayText(t time.Time, env Env) string {
	// TRANSLATORS: strftime format for the day of the month alone, e.g.
	// "14" (the start of "12–18 October", a day of the month grid).
	// xgettext:no-c-format
	return env.date(t, env.Tr.T("%-d"))
}

// MonthYearText is "October 2026".
func MonthYearText(t time.Time, env Env) string {
	// TRANSLATORS: strftime format for a month and year, e.g. "October
	// 2026" (the calendar's title). %OB is the standalone (nominative)
	// month name, "říjen 2026", not the genitive of %B.
	// xgettext:no-c-format
	return env.date(t, env.Tr.T("%OB %Y"))
}

// MonthText is the month's name, "October".
func MonthText(t time.Time, env Env) string {
	// TRANSLATORS: strftime format for a month's name alone, e.g. "October"
	// (the start of "October–November 2026"). %OB is the standalone
	// (nominative) month name, "říjen", not the genitive of %B.
	// xgettext:no-c-format
	return env.date(t, env.Tr.T("%OB"))
}

// WeekdayText is the weekday's name, "Tuesday".
func WeekdayText(t time.Time, env Env) string {
	// TRANSLATORS: strftime format for a weekday's name, e.g. "Tuesday".
	// xgettext:no-c-format
	return env.date(t, env.Tr.T("%A"))
}

// ShortWeekdayText is the abbreviated weekday, "Tue" (the week's day
// headers, the month's header, the date tile).
func ShortWeekdayText(t time.Time, env Env) string {
	// TRANSLATORS: strftime format for an abbreviated weekday, e.g. "Tue".
	// xgettext:no-c-format
	return env.date(t, env.Tr.T("%a"))
}

// ShortMonthText is the abbreviated month, "Oct" (the date tile).
func ShortMonthText(t time.Time, env Env) string {
	// TRANSLATORS: strftime format for an abbreviated month, e.g. "Oct"
	// (the invitation card's date tile).
	// xgettext:no-c-format
	return env.date(t, env.Tr.T("%b"))
}

// DateTimeText is a short date with a time, "Tue 14 Oct 2026 10:00" (the
// two ends of an event over several days).
func DateTimeText(t time.Time, env Env) string {
	// TRANSLATORS: strftime format for a short date with the time, e.g.
	// "Tue 14 Oct 2026 10:00".
	// xgettext:no-c-format
	return env.date(t, env.Tr.T("%a %-d %b %Y %H:%M"))
}

// RangeText joins the two ends of a range of dates or times, "12–18
// October", "10:00–11:30".
func RangeText(from, to string, tr Translator) string {
	// TRANSLATORS: a range of two dates or times: "12–18 October",
	// "10:00–11:30", "28 September–4 October".
	return fmt.Sprintf(tr.T("%s–%s"), from, to)
}

// Dot joins two parts of one line, "Work · Calendar", "10:00–11:30 ·
// Room B".
func Dot(a, b string, tr Translator) string {
	// TRANSLATORS: two parts of one line of the calendar, e.g. "Work ·
	// Calendar" (account and calendar) or "10:00–11:30 · Room B".
	return fmt.Sprintf(tr.T("%s · %s"), a, b)
}

// dots joins the non-empty parts with Dot.
func dots(tr Translator, parts ...string) string {
	out := ""
	for _, p := range parts {
		switch {
		case p == "":
		case out == "":
			out = p
		default:
			out = Dot(out, p, tr)
		}
	}
	return out
}

// TimeRangeText is a timed span's times in env's zone, "10:00–11:30".
func TimeRangeText(start, end time.Time, env Env) string {
	return RangeText(TimeText(start, env), TimeText(end, env), env.Tr)
}

// cleanTitle is an event's title for display: cleaned, Untitled when
// empty.
func cleanTitle(s string, tr Translator) string {
	if s = board.CleanLine(s, api.MaxEventTitleBytes); s == "" {
		return Untitled(tr)
	}
	return s
}

// cleanLine is a server's one-line string for display.
func cleanLine(s string) string { return board.CleanLine(s, api.MaxEventLocationBytes) }

// nameOf is a person's name, else the address.
func nameOf(a *api.Address) string {
	if a == nil {
		return ""
	}
	if n := cleanLine(a.Name); n != "" {
		return n
	}
	return cleanLine(a.Address)
}
