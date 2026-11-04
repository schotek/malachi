// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"fmt"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
)

// The event detail popover: its rows from an Event, the time line with
// the duration, the people and their responses, and which actions it
// offers (respond, remove or delete with its question, Show Invitation in
// Mail, Edit).

// MaxAttendeesShown is how many attendees the list shows before it
// collapses behind "Show All".
const MaxAttendeesShown = 6

// AttendeeRow is one person of the detail's list.
type AttendeeRow struct {
	Name, Address string
	Response      api.EventResponse
	// ResponseText is the response's label (ResponseLabel).
	ResponseText string
	Organizer    bool
	Me           bool
	// Optional: the attendee's role is optional.
	Optional bool
}

// Detail is everything the popover shows of an event.
type Detail struct {
	// CalendarLine is "Work · Calendar" (account · calendar).
	CalendarLine string
	Title        string
	// TimeLine is "Tuesday 14 October 2026 · 10:00–11:30 · 1 h 30 min".
	TimeLine string
	// Recurrence is the rule's text; "" for a single event.
	Recurrence string
	Location   string
	// VideoCall is the call's link ("" none); VideoHost its host.
	VideoCall, VideoHost string
	// PeopleLine is "Organiser Jana Nováková · 4 attendees"; "" without
	// attendees.
	PeopleLine string
	Attendees  []AttendeeRow
	// Collapsed: more than MaxAttendeesShown attendees.
	Collapsed bool
	// MyResponse is the user's answer when the user is an attendee.
	MyResponse api.EventResponse
	Notes      string
	Links      []string
	// The actions.
	CanRespond, CanEdit, CanRemove, ShowInvitation bool
	RemoveTitle                                    string
}

// DetailOf is the detail of e, with its account's and calendar's names.
func DetailOf(e api.Event, accountName, calendarName string, env Env) Detail {
	tr := env.Tr
	d := Detail{
		CalendarLine: dots(tr, cleanLine(accountName), cleanLine(calendarName)),
		Title:        cleanTitle(e.Title, tr),
		TimeLine:     TimeLine(e.Occurrence, env),
		Location:     cleanLine(e.Location),
		PeopleLine:   PeopleLine(e, tr),
		MyResponse:   e.MyResponse,
		Notes:        board.CleanBlock(e.Notes, api.MaxEventNotesBytes),
		Links:        e.Links,
		CanRespond:   CanRespond(e),
		CanEdit:      e.CanEdit,
		CanRemove:    e.CanDelete,
		RemoveTitle:  RemoveTitle(e, tr),

		ShowInvitation: ShowsInvitation(e),
	}
	if e.Recurrence != nil {
		if s, _, ok := Span(e.Occurrence, env.loc()); ok {
			d.Recurrence = RecurrenceText(e.Recurrence, s, env)
		}
	}
	if e.VideoCallLink != nil {
		d.VideoCall, d.VideoHost = e.VideoCallLink.URL, cleanLine(e.VideoCallLink.Host)
	}
	for _, a := range e.Attendees {
		d.Attendees = append(d.Attendees, AttendeeRow{Name: cleanLine(a.Name), Address: cleanLine(a.Address),
			Response: a.Response, ResponseText: ResponseLabel(a.Response, tr), Organizer: a.Organizer, Me: a.Me,
			Optional: a.Role == api.AttendeeOptional})
	}
	d.Collapsed = len(d.Attendees) > MaxAttendeesShown
	return d
}

// TimeLine is an occurrence's date and time with its duration: "Tuesday
// 14 October 2026 · 10:00–11:30 · 1 h 30 min"; an all-day day "Tuesday
// 14 October 2026 · All Day"; several days with both ends and the length.
func TimeLine(o api.Occurrence, env Env) string {
	s, e, ok := Span(o, env.loc())
	if !ok {
		return ""
	}
	return spanLine(s, e, o.AllDay, env)
}

// spanLine is TimeLine of a span.
func spanLine(s, e time.Time, allDay bool, env Env) string {
	tr, loc := env.Tr, env.loc()
	if allDay {
		days := max(1, DaysBetween(s, e, loc))
		if days == 1 {
			return Dot(LongDateText(s, env), AllDay(tr), tr)
		}
		return Dot(RangeText(LongDateText(s, env), LongDateText(AddDays(e, -1), env), tr), DaysText(days, tr), tr)
	}
	if occurrenceDays(s, e, loc) <= 1 {
		return dots(tr, LongDateText(s, env), TimeRangeText(s, e, env), DurationText(e.Sub(s), tr))
	}
	return Dot(RangeText(DateTimeText(s, env), DateTimeText(e, env), tr), DurationText(e.Sub(s), tr), tr)
}

// DaysText is a number of days, "2 days".
func DaysText(n int, tr Translator) string {
	return fmt.Sprintf(tr.N("%d day", "%d days", n), n)
}

// DurationText is a length of time in hours and minutes, "1 h 30 min",
// "45 min", "2 h"; a whole number of days of 24 hours is "2 days".
// Rounded down to the minute; under a minute is "0 min".
func DurationText(d time.Duration, tr Translator) string {
	m := max(0, int(d/time.Minute))
	if m >= 1440 && m%1440 == 0 {
		return DaysText(m/1440, tr)
	}
	h, m := m/60, m%60
	// TRANSLATORS: a duration's hours, abbreviated: "1 h", "2 h".
	hours := fmt.Sprintf(tr.N("%d h", "%d h", h), h)
	// TRANSLATORS: a duration's minutes, abbreviated: "30 min".
	minutes := fmt.Sprintf(tr.N("%d min", "%d min", m), m)
	switch {
	case h == 0:
		return minutes
	case m == 0:
		return hours
	}
	// TRANSLATORS: a duration of hours and minutes, "1 h 30 min".
	return fmt.Sprintf(tr.T("%s %s"), hours, minutes)
}

// ResponseLabel is an attendee's answer: Accepted, Tentative, Declined,
// No Reply (needsAction and unknown), Delegated.
func ResponseLabel(r api.EventResponse, tr Translator) string {
	switch r {
	case api.ResponseAccepted:
		// TRANSLATORS: an attendee's answer to an invitation.
		return tr.T("Accepted")
	case api.ResponseTentative:
		// TRANSLATORS: an attendee's answer to an invitation: maybe.
		return tr.T("Tentative")
	case api.ResponseDeclined:
		// TRANSLATORS: an attendee's answer to an invitation.
		return tr.T("Declined")
	case api.ResponseDelegated:
		// TRANSLATORS: an attendee passed the invitation to someone else.
		return tr.T("Delegated")
	}
	// TRANSLATORS: an attendee has not answered the invitation yet.
	return tr.T("No Reply")
}

// MyResponseText is the popup's title of the user's answer, "You:
// Accepted".
func MyResponseText(r api.EventResponse, tr Translator) string {
	// TRANSLATORS: the event detail's popup of the user's own answer; %s is
	// Accepted, Tentative, Declined or No Reply.
	return fmt.Sprintf(tr.T("You: %s"), ResponseLabel(r, tr))
}

// ResponseChoices are the answers the popup and the card offer.
var ResponseChoices = []api.EventResponse{api.ResponseAccepted, api.ResponseTentative, api.ResponseDeclined}

// ResponseAction is a response button's title: Accept, Tentative,
// Decline.
func ResponseAction(r api.EventResponse, tr Translator) string {
	switch r {
	case api.ResponseTentative:
		// TRANSLATORS: the button that answers an invitation with maybe.
		return tr.C("respond", "Tentative")
	case api.ResponseDeclined:
		// TRANSLATORS: the button that declines an invitation.
		return tr.T("Decline")
	}
	// TRANSLATORS: the button that accepts an invitation.
	return tr.T("Accept")
}

// DeclineWithoutReply is the Decline menu's item that declines without
// telling the organiser (sendResponse false).
func DeclineWithoutReply(tr Translator) string { return tr.T("Decline Without Reply") }

// PeopleLine is "Organiser Jana Nováková · 4 attendees", "You organise ·
// 4 attendees", or the attendee count alone; "" without attendees.
func PeopleLine(e api.Event, tr Translator) string {
	n := max(e.AttendeeCount, len(e.Attendees))
	if n == 0 {
		return ""
	}
	if e.Organizer {
		// TRANSLATORS: the event detail's people line of an event the user
		// organises; %d is the number of attendees.
		return fmt.Sprintf(tr.N("You organise · %d attendee", "You organise · %d attendees", n), n)
	}
	if org := nameOf(e.OrganizerAddress); org != "" {
		// TRANSLATORS: the event detail's people line: the organiser's name
		// and the number of attendees.
		return fmt.Sprintf(tr.N("Organiser %s · %d attendee", "Organiser %s · %d attendees", n), org, n)
	}
	return fmt.Sprintf(tr.N("%d attendee", "%d attendees", n), n)
}

// OrganizerLabel marks the organiser in the attendee list.
func OrganizerLabel(tr Translator) string {
	// TRANSLATORS: beside the organiser's name in an event's attendee list.
	return tr.T("organiser")
}

// OptionalLabel marks an optional attendee.
func OptionalLabel(tr Translator) string {
	// TRANSLATORS: beside an optional attendee's name.
	return tr.T("optional")
}

// ShowAllAttendees expands a collapsed attendee list.
func ShowAllAttendees(n int, tr Translator) string {
	return fmt.Sprintf(tr.N("Show All %d Attendee", "Show All %d Attendees", n), n)
}

// CanRespond reports whether the popover offers the user's answer: the
// daemon allows it, the user is an attendee and not the organiser, and
// the event is not cancelled.
func CanRespond(e api.Event) bool {
	return e.CanRespond && !e.Organizer && e.MyResponse != "" && e.Status != api.EventStatusCancelled
}

// ShowsInvitation reports Show Invitation in Mail.
func ShowsInvitation(e api.Event) bool { return len(e.InvitationMessages) > 0 }

// ShowInvitation is the detail's button that reveals the invitation
// message in Mail.
func ShowInvitation(tr Translator) string { return tr.T("Show Invitation in Mail") }

// Edit is the detail's Edit button.
func Edit(tr Translator) string { return tr.T("Edit") }

// JoinVideoCall is the video-call row's link.
func JoinVideoCall(tr Translator) string { return tr.T("Join Video Call") }

// AskAboutEvent and FindAnotherTime are the detail's ✦ menu.
func AskAboutEvent(tr Translator) string { return tr.T("Ask about This Event") }

// FindAnotherTime opens the editor with Next Free Slot.
func FindAnotherTime(tr Translator) string { return tr.T("Find Another Time") }

// RemoveTitle is the footer's destructive button: Delete for an event
// the user organises (it is deleted for everyone), else Remove from
// Calendar.
func RemoveTitle(e api.Event, tr Translator) string {
	if e.RemoveIsDelete {
		return tr.T("Delete")
	}
	return RemoveFromCalendar(tr)
}

// RemoveFromCalendar removes an event from the user's calendar (an
// invitation's event, a cancelled one).
func RemoveFromCalendar(tr Translator) string { return tr.T("Remove from Calendar") }

// Question is a confirmation: its heading, its text, the button that
// goes ahead and, when there is one, the alternative button (Cancel is
// always there).
type Question struct {
	Heading, Body string
	Confirm       string
	// Alternative is a second way to go ahead ("" none).
	Alternative string
}

// RemoveQuestion is what removing e asks: an attendee who has not
// declined is asked whether to tell the organiser (Confirm declines and
// removes, Alternative removes without a reply); the organiser of an
// event with attendees is told they will hear of it; otherwise false and
// only the Mail preference confirm-delete decides whether to ask
// (DeleteQuestion).
func RemoveQuestion(e api.Event, tr Translator) (Question, bool) {
	title := cleanTitle(e.Title, tr)
	if !e.RemoveIsDelete && CanRespond(e) && e.MyResponse != api.ResponseDeclined {
		return Question{
			Heading:     tr.T("Also tell the organiser you won’t attend?"),
			Body:        fmt.Sprintf(tr.T("“%s” will be removed from your calendar."), title),
			Confirm:     tr.T("Remove and Decline"),
			Alternative: tr.T("Remove Only"),
		}, true
	}
	if e.RemoveIsDelete && max(e.AttendeeCount, len(e.Attendees)) > 0 {
		n := max(e.AttendeeCount, len(e.Attendees))
		return Question{
			Heading: tr.T("Delete the Event?"),
			Body: fmt.Sprintf(tr.N("“%s” will be cancelled for its %d attendee.",
				"“%s” will be cancelled for its %d attendees.", n), title, n),
			Confirm: tr.T("Delete"),
		}, true
	}
	return Question{}, false
}

// DeleteQuestion is the plain confirmation of removing e, asked only with
// the Mail preference confirm-delete.
func DeleteQuestion(e api.Event, tr Translator) Question {
	return Question{
		Heading: fmt.Sprintf(tr.T("Remove “%s”?"), cleanTitle(e.Title, tr)),
		Confirm: RemoveTitle(e, tr),
	}
}
