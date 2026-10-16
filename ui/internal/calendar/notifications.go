// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"fmt"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The calendar's notifications: an event's alert (notify.eventAlert) with
// Join and Snooze, a new invitation with Accept and Show, a cancellation
// with Remove from Calendar.

// SnoozeMinutes is how long Snooze puts an alert off.
const SnoozeMinutes = 5

// Notification is a notification's title and body.
type Notification struct {
	Title, Body string
}

// AlertWhen is when an alerted event starts, seen from now: "now" when it
// has begun or starts within the minute, "in 5 minutes" under an hour,
// else the start time; an all-day event "today" or its date.
func AlertWhen(a api.EventAlert, now time.Time, env Env) string {
	tr := env.Tr
	if a.AllDay {
		d, ok := ParseDate(a.StartDate, env.loc())
		if !ok {
			return ""
		}
		if SameDay(d, now, env.loc()) {
			// TRANSLATORS: an alert of an all-day event of today.
			return tr.T("today")
		}
		return WeekdayDateText(d, env)
	}
	if a.Start == nil {
		return ""
	}
	m := int(a.Start.Sub(now).Round(time.Minute) / time.Minute)
	switch {
	case m <= 0:
		// TRANSLATORS: an alert of an event that starts now.
		return tr.C("alert", "now")
	case m < 60:
		// TRANSLATORS: an alert of an event that starts in a few minutes.
		return fmt.Sprintf(tr.N("in %d minute", "in %d minutes", m), m)
	case SameDay(*a.Start, now, env.loc()):
		// TRANSLATORS: an alert of an event later today; %s is its time.
		return fmt.Sprintf(tr.T("at %s"), TimeText(*a.Start, env))
	}
	return dots(tr, WeekdayDateText(*a.Start, env), TimeText(*a.Start, env))
}

// AlertNotification is an alert's notification: the event's title, and
// when it starts with the place, "in 5 minutes · Room B".
func AlertNotification(a api.EventAlert, now time.Time, env Env) Notification {
	return Notification{Title: cleanTitle(a.Title, env.Tr), Body: dots(env.Tr, AlertWhen(a, now, env), cleanLine(a.Location))}
}

// Join is an alert's action for an event with a video call.
func Join(tr Translator) string {
	// TRANSLATORS: the action of an event's notification that joins its
	// video call.
	return tr.T("Join")
}

// Snooze is an alert's action that puts it off SnoozeMinutes.
func Snooze(tr Translator) string {
	// TRANSLATORS: the action of an event's notification that shows it
	// again in a few minutes.
	return tr.T("Snooze")
}

// InvitationNotification is a new invitation's notification: "New
// invitation from Jana Nováková", the event's title and time.
func InvitationNotification(s api.InvitationSummary, env Env) Notification {
	tr := env.Tr
	from := nameOf(s.Organizer)
	var title string
	switch {
	case s.Kind == api.InvitationKindUpdate && from != "":
		title = fmt.Sprintf(tr.T("%s updated the event"), from)
	case s.Kind == api.InvitationKindUpdate:
		title = tr.T("An event was updated")
	case from != "":
		title = fmt.Sprintf(tr.T("New invitation from %s"), from)
	default:
		title = tr.T("New invitation")
	}
	return Notification{Title: title, Body: summaryBody(s, env)}
}

// summaryBody is "Q4 planning · Tuesday 14 October 2026 · 10:00–11:30 · 1 h
// 30 min".
func summaryBody(s api.InvitationSummary, env Env) string {
	o := api.Occurrence{Start: s.Start, End: s.End, StartDate: s.StartDate, AllDay: s.AllDay}
	if s.AllDay {
		if d, ok := ParseDate(s.StartDate, env.loc()); ok {
			o.EndDate = DateOf(AddDays(d, 1), env.loc())
		}
	}
	return dots(env.Tr, cleanTitle(s.Title, env.Tr), TimeLine(o, env))
}

// Show is an invitation's action that opens the message.
func Show(tr Translator) string {
	// TRANSLATORS: the action of an invitation's notification that opens
	// the message.
	return tr.T("Show")
}

// CancellationNotification is a cancellation's notification: "Jana
// Nováková cancelled the event", the event's title and time.
func CancellationNotification(s api.InvitationSummary, env Env) Notification {
	tr := env.Tr
	title := tr.T("An event was cancelled")
	if from := nameOf(s.Organizer); from != "" {
		title = fmt.Sprintf(tr.T("%s cancelled the event"), from)
	}
	return Notification{Title: title, Body: summaryBody(s, env)}
}
