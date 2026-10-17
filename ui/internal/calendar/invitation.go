// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"fmt"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Invitations in Mail: the list pill of a message with a calendar part,
// and the invitation card above the message header, built from the
// parsed part (never from HTML): the date tile, the time and people
// lines, the conflict line, "Add to:", the response buttons, the reply
// note, the state strip after answering, a reply's one-line card and the
// lines of invalid, outdated and cancelled invitations.

// InvitationPill is the list pill of a message.
type InvitationPill int

// The pills.
const (
	PillNone InvitationPill = iota
	PillInvitation
	PillUpdate
	PillCancelled
	PillReply
)

// PillKind is the pill of a listed message: none without an invitation,
// for an unreadable one (state invalid) and for kind other.
func PillKind(s *api.InvitationSummary) InvitationPill {
	if s == nil || s.State == api.InvitationInvalid {
		return PillNone
	}
	switch s.Kind {
	case api.InvitationKindInvitation:
		return PillInvitation
	case api.InvitationKindUpdate:
		return PillUpdate
	case api.InvitationKindCancelled:
		return PillCancelled
	case api.InvitationKindReply:
		return PillReply
	}
	return PillNone
}

// PillTitle is a pill's text ("" for none).
func PillTitle(p InvitationPill, tr Translator) string {
	switch p {
	case PillInvitation:
		// TRANSLATORS: the message list's pill of a message with an
		// invitation to an event.
		return tr.C("pill", "Invitation")
	case PillUpdate:
		// TRANSLATORS: the message list's pill of an updated invitation.
		return tr.C("pill", "Update")
	case PillCancelled:
		// TRANSLATORS: the message list's pill of a cancelled event.
		return tr.C("pill", "Cancelled")
	case PillReply:
		// TRANSLATORS: the message list's pill of an attendee's answer.
		return tr.C("pill", "Reply")
	}
	return ""
}

// StrikesSubject reports a row whose subject is struck through: a
// cancellation.
func StrikesSubject(p InvitationPill) bool { return p == PillCancelled }

// CardMode is which card a message shows.
type CardMode int

// The modes.
const (
	// CardNone: no card (no invitation, state invalid, the account's card
	// switch off, kind other).
	CardNone CardMode = iota
	// CardFull: the invitation card with its actions or state strip.
	CardFull
	// CardReply: the one-line card of an attendee's answer.
	CardReply
	// CardCancelled: the card of a cancellation with Remove from Calendar.
	CardCancelled
)

// CardModeOf is the card of a message's invitation; cardOn is the
// account's InvitationCard switch.
func CardModeOf(c *api.InvitationCard, cardOn bool) CardMode {
	if c == nil || !cardOn || c.State == api.InvitationInvalid {
		return CardNone
	}
	switch c.Kind {
	case api.InvitationKindReply:
		return CardReply
	case api.InvitationKindCancelled:
		return CardCancelled
	case api.InvitationKindInvitation, api.InvitationKindUpdate:
		return CardFull
	}
	return CardNone
}

// ShowsResponses reports the Accept / Tentative / Decline row: an
// unanswered invitation the daemon lets the user answer.
func ShowsResponses(c api.InvitationCard) bool {
	return c.State == api.InvitationUnanswered && c.CanRespond
}

// ShowsStateStrip reports the strip after answering (with Change
// Response and Show in Calendar).
func ShowsStateStrip(c api.InvitationCard) bool {
	return c.State == api.InvitationAnswered && c.Response != ""
}

// ShowsProposeNewTime reports Propose New Time… (Graph events that allow
// it, docs/calendar.md D20).
func ShowsProposeNewTime(c api.InvitationCard) bool {
	return c.CanProposeNewTime && (ShowsResponses(c) || ShowsStateStrip(c))
}

// DateTile is the card's date tile.
type DateTile struct {
	Weekday, Day, Month string
}

// cardStart is the invitation's start in the zone.
func cardStart(s api.InvitationSummary, loc *time.Location) (time.Time, bool) {
	if s.AllDay {
		return ParseDate(s.StartDate, loc)
	}
	if s.Start == nil {
		return time.Time{}, false
	}
	return s.Start.In(loc), true
}

// DateTileOf is the tile of the invitation's first day; false without a
// start.
func DateTileOf(s api.InvitationSummary, env Env) (DateTile, bool) {
	t, ok := cardStart(s, env.loc())
	if !ok {
		return DateTile{}, false
	}
	return DateTile{Weekday: ShortWeekdayText(t, env), Day: DayText(t, env), Month: ShortMonthText(t, env)}, true
}

// occurrenceOf is the card's time as an occurrence (for TimeLine).
func occurrenceOf(c api.InvitationCard) api.Occurrence {
	return api.Occurrence{Title: c.Title, Start: c.Start, End: c.End, StartDate: c.StartDate, EndDate: c.EndDate,
		AllDay: c.AllDay, Location: c.Location}
}

// CardTimeLine is the card's clock line (TimeLine).
func CardTimeLine(c api.InvitationCard, env Env) string { return TimeLine(occurrenceOf(c), env) }

// CardTitle is the card's bold title.
func CardTitle(s api.InvitationSummary, tr Translator) string { return cleanTitle(s.Title, tr) }

// CardPeopleLine is "Organiser Jana Nováková · 4 attendees".
func CardPeopleLine(c api.InvitationCard, tr Translator) string {
	n := max(c.AttendeeCount, len(c.Attendees))
	org := nameOf(c.Organizer)
	switch {
	case org != "" && n > 0:
		return fmt.Sprintf(tr.N("Organiser %s · %d attendee", "Organiser %s · %d attendees", n), org, n)
	case org != "":
		// TRANSLATORS: the invitation card's people line without attendees.
		return fmt.Sprintf(tr.T("Organiser %s"), org)
	case n > 0:
		return fmt.Sprintf(tr.N("%d attendee", "%d attendees", n), n)
	}
	return ""
}

// PreviousLines are an update's former values, each only when the
// daemon sent it: the time ("Previously Tuesday 14 October 2026 ·
// 10:00–11:00 · 1 h"), the location ("Previously at Room B"), the
// recurrence ("Previously repeated: Every week on Tuesday"); nil without
// one.
func PreviousLines(c api.InvitationCard, env Env) []string {
	p := c.Previous
	if p == nil {
		return nil
	}
	tr := env.Tr
	var lines []string
	start, haveStart := cardStart(c.InvitationSummary, env.loc())
	if p.Start != "" {
		o := api.Occurrence{}
		if s, err := time.Parse(time.RFC3339, string(p.Start)); err == nil {
			o.Start = &s
			if e, err := time.Parse(time.RFC3339, string(p.End)); err == nil {
				o.End = &e
			}
		} else {
			o.AllDay, o.StartDate, o.EndDate = true, api.Date(p.Start), api.Date(p.End)
		}
		if line := TimeLine(o, env); line != "" {
			// TRANSLATORS: the invitation card of an update: the event's time
			// before the change.
			lines = append(lines, fmt.Sprintf(tr.T("Previously %s"), line))
			start, _, haveStart = Span(o, env.loc())
		}
	}
	if where := cleanLine(p.Location); where != "" {
		// TRANSLATORS: the invitation card of an update: the event's location
		// before the change.
		lines = append(lines, fmt.Sprintf(tr.T("Previously at %s"), where))
	}
	if p.Recurrence != nil && haveStart {
		if rule := RecurrenceText(p.Recurrence, start, env); rule != "" {
			// TRANSLATORS: the invitation card of an update: the event's
			// recurrence before the change; %s is a recurrence description
			// such as "Every week on Tuesday, until 27 October 2026".
			lines = append(lines, fmt.Sprintf(tr.T("Previously repeated: %s"), rule))
		}
	}
	return lines
}

// ConflictLine is the card's conflict line: one conflict "Conflicts with
// Standup (10:00–10:30) in calendar Work · Calendar" (a private one
// without its title), several "Conflicts with 2 events"; "" without
// conflicts. calendarName names a calendar by id.
func ConflictLine(conflicts []api.InvitationConflict, calendarName func(api.CalendarID) string, env Env) string {
	tr := env.Tr
	switch len(conflicts) {
	case 0:
		return ""
	case 1:
		c := conflicts[0]
		when := TimeRangeText(c.Start, c.End, env)
		cal := cleanLine(calendarName(c.CalendarID))
		if title := cleanLine(c.Title); title != "" {
			// TRANSLATORS: the invitation card: the event overlaps one in the
			// user's calendar; its title, time and calendar.
			return fmt.Sprintf(tr.T("Conflicts with %s (%s) in calendar %s"), title, when, cal)
		}
		// TRANSLATORS: the invitation card: the event overlaps a private
		// event of a shared calendar; its time and calendar.
		return fmt.Sprintf(tr.T("Conflicts with a private event (%s) in calendar %s"), when, cal)
	}
	n := len(conflicts)
	return fmt.Sprintf(tr.N("Conflicts with %d event", "Conflicts with %d events", n), n)
}

// NoConflict is the card's line when the time is free.
func NoConflict(tr Translator) string { return tr.T("No conflicts in your calendars") }

// AddTo is the label of the target-calendar popup.
func AddTo(tr Translator) string {
	// TRANSLATORS: the invitation card's popup of the calendar the event
	// goes to when accepted.
	return tr.T("Add to:")
}

// ReplyNote is the note under the response buttons: where the reply
// leaves from and goes to, and the warning when the organiser's domain is
// not the sender's. accountName names ReplyAccountID.
func ReplyNote(c api.InvitationCard, accountName string, tr Translator) []string {
	var lines []string
	to := nameOf(c.ReplyTo)
	if to != "" && c.ReplyTo != nil && cleanLine(c.ReplyTo.Name) != "" {
		// TRANSLATORS: an organiser in a note: the name and the address.
		to = fmt.Sprintf(tr.T("%s <%s>"), cleanLine(c.ReplyTo.Name), cleanLine(c.ReplyTo.Address))
	}
	switch c.ReplyVia {
	case api.InvitationsViaMail:
		if a := cleanLine(accountName); a != "" {
			lines = append(lines, fmt.Sprintf(tr.T("The reply leaves from account %s."), a))
		}
		if to != "" {
			lines = append(lines, fmt.Sprintf(tr.T("The reply goes to %s."), to))
		}
	case api.InvitationsViaServer:
		if to != "" {
			lines = append(lines, fmt.Sprintf(tr.T("The reply goes to %s."), to))
		}
	default:
		lines = append(lines, tr.T("No reply will be sent."))
	}
	if c.OrganizerMismatch {
		lines = append(lines, tr.T("The organiser’s address is not from the sender’s domain. Check the invitation before you answer."))
	}
	return lines
}

// StateStrip is the strip after answering: "Accepted · in calendar Work ·
// Calendar"; without a calendar the answer alone.
func StateStrip(c api.InvitationCard, calendarName string, tr Translator) string {
	answer := ResponseLabel(c.Response, tr)
	if cal := cleanLine(calendarName); cal != "" && c.Response != api.ResponseDeclined {
		// TRANSLATORS: the invitation card after answering: the answer and
		// the calendar the event is in, "Accepted · in calendar Work".
		return fmt.Sprintf(tr.T("%s · in calendar %s"), answer, cal)
	}
	return answer
}

// ChangeResponse is the state strip's menu button.
func ChangeResponse(tr Translator) string { return tr.T("Change Response") }

// ShowsInCalendar reports Show in Calendar on the card's state strip or
// state line: the invitation is linked to a stored event, and it is the
// user's own event or one the user answered.
func ShowsInCalendar(c api.InvitationCard) bool {
	return c.EventID != "" && (c.OwnEvent || c.State == api.InvitationAnswered)
}

// ShowInCalendar is the state strip's button that shows the event in
// Calendar mode.
func ShowInCalendar(tr Translator) string { return tr.T("Show in Calendar") }

// ProposeNewTime opens the popover with a start and an end.
func ProposeNewTime(tr Translator) string { return tr.T("Propose New Time…") }

// Propose is the popover's button that sends the proposal.
func Propose(tr Translator) string { return tr.T("Propose") }

// ReplyCard is a reply's one-line card: "Jana Nováková accepted", with
// "(applied to your event)" when the daemon updated the user's event.
func ReplyCard(c api.InvitationCard, tr Translator) string {
	who := nameOf(c.ReplyFrom)
	if who == "" {
		who = nameOf(c.Organizer)
	}
	var line string
	switch c.Response {
	case api.ResponseAccepted:
		// TRANSLATORS: the card of an attendee's answer: their name.
		line = fmt.Sprintf(tr.T("%s accepted"), who)
	case api.ResponseTentative:
		// TRANSLATORS: the card of an attendee's answer: their name.
		line = fmt.Sprintf(tr.T("%s tentatively accepted"), who)
	case api.ResponseDeclined:
		// TRANSLATORS: the card of an attendee's answer: their name.
		line = fmt.Sprintf(tr.T("%s declined"), who)
	default:
		// TRANSLATORS: the card of an attendee's answer that is none of
		// the usual three.
		line = fmt.Sprintf(tr.T("%s replied"), who)
	}
	if c.ReplyApplied {
		// TRANSLATORS: after the card of an attendee's answer: the user's
		// event shows it now.
		return fmt.Sprintf(tr.T("%s (applied to your event)"), line)
	}
	return line
}

// StateLine is the card's line of an invitation that cannot be answered:
// the user's own event (first), outdated, cancelled, an event in a junk
// or trash folder; "" otherwise.
func StateLine(c api.InvitationCard, tr Translator) string {
	if c.OwnEvent && (c.Kind == api.InvitationKindInvitation || c.Kind == api.InvitationKindUpdate) {
		// TRANSLATORS: the invitation card of an invitation the user sent
		// themself (the user organises the event).
		return tr.T("You organise this event.")
	}
	switch c.State {
	case api.InvitationInvalid:
		return InvalidLine(tr)
	case api.InvitationOutdated:
		return tr.T("This invitation is out of date: a newer one arrived, or the event is over.")
	case api.InvitationCancelled:
		return CancelledLine(tr)
	case api.InvitationUnanswered:
		if !c.CanRespond {
			return tr.T("This invitation cannot be answered from here.")
		}
	}
	return ""
}

// InvalidLine says the calendar part could not be read (shown where a
// client says why there is no card).
func InvalidLine(tr Translator) string {
	return tr.T("The invitation in this message could not be read.")
}

// CancelledLine is the cancellation card's line.
func CancelledLine(tr Translator) string { return tr.T("This event was cancelled.") }
