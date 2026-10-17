// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The event editor (New Event / Edit Event): where a new event starts,
// what makes the input invalid, the primary button, the footer note, the
// alert and duration choices, the time-zone link, the iMIP subjects and
// texts the daemon sends (it is language-neutral, docs/calendar.md D9),
// the close question and the question of a recurring event's scope.

// Origin is where a new event was asked for.
type Origin int

// The origins.
const (
	// OriginNow: the "+" button, ⌥⌘N, the File menu: the next half hour
	// from now (an exact half hour stays).
	OriginNow Origin = iota
	// OriginSlot: a double click on empty time of the grid: the half hour
	// the click is in.
	OriginSlot
	// OriginMonthCell: a double click on a month cell: 9:00 that day.
	OriginMonthCell
	// OriginAllDay: a double click on the all-day row: an all-day event
	// that day.
	OriginAllDay
)

// MonthCellHour is the start of an event made from a month cell.
const MonthCellHour = 9

// Defaults is a new event's start, end and alerts.
type Defaults struct {
	AllDay bool
	// Start and End in the editor's zone; an all-day event's are the
	// midnights of its first day and of the day after its last.
	Start, End time.Time
	// Alerts: the default alert, or none (empty, not nil).
	Alerts []int
}

// DefaultDuration is the preference's default duration, 60 minutes when
// it is out of 5–1440.
func DefaultDuration(p api.CalendarPreferences) time.Duration {
	m := p.DefaultDurationMinutes
	if m < 5 || m > 1440 {
		m = 60
	}
	return time.Duration(m) * time.Minute
}

// NewEventDefaults is where a new event starts: at is now (OriginNow) or
// the time or day the user double-clicked.
func NewEventDefaults(at time.Time, origin Origin, p api.CalendarPreferences, loc *time.Location) Defaults {
	t := at.In(loc)
	d := Defaults{Alerts: []int{}}
	if p.DefaultAlertMinutes >= 0 && p.DefaultAlertMinutes <= api.MaxAlertMinutes {
		d.Alerts = []int{p.DefaultAlertMinutes}
	}
	switch origin {
	case OriginAllDay:
		if len(d.Alerts) > 0 {
			d.Alerts = []int{AllDayAlertDefault}
		}
		d.AllDay = true
		d.Start = StartOfDay(t, loc)
		d.End = AddDays(d.Start, 1)
		return d
	case OriginMonthCell:
		d.Start = time.Date(t.Year(), t.Month(), t.Day(), MonthCellHour, 0, 0, 0, loc)
	case OriginSlot:
		d.Start = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute()/30*30, 0, 0, loc)
	default:
		half := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute()/30*30, 0, 0, loc)
		if half.Before(t) {
			half = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute()/30*30+30, 0, 0, loc)
		}
		d.Start = half
	}
	d.End = d.Start.Add(DefaultDuration(p))
	return d
}

// Input is what the editor holds, for validation and for EventInput.
type Input struct {
	Title    string
	AllDay   bool
	Start    time.Time
	End      time.Time
	Location string
	Notes    string
	// Attendees are the recipient field's resolved addresses.
	Attendees []api.EventInputAttendee
	Alerts    []int
	// CalendarID is the chosen calendar ("" none chosen).
	CalendarID api.CalendarID
}

// Problem is what is wrong with an Input.
type Problem int

// The problems, in the order Validate reports them.
const (
	ProblemNoCalendar Problem = iota
	ProblemEndBeforeStart
	ProblemTitleTooLong
	ProblemLocationTooLong
	ProblemNotesTooLong
	ProblemTooManyAttendees
	ProblemInvalidAttendee
	ProblemTooManyAlerts
)

// Validate lists what keeps in from being saved, in the order of
// Problem: no calendar, a timed end not after its start (an all-day
// event's last day before its first: End is the day after the last), a
// title, location or notes over the daemon's limit, too many attendees,
// an attendee address that is not one, too many alerts.
func Validate(in Input) []Problem {
	var out []Problem
	if in.CalendarID == "" {
		out = append(out, ProblemNoCalendar)
	}
	if in.AllDay && !StartOfDay(in.End, in.End.Location()).After(StartOfDay(in.Start, in.Start.Location())) ||
		!in.AllDay && !in.End.After(in.Start) {
		out = append(out, ProblemEndBeforeStart)
	}
	if len(strings.TrimSpace(in.Title)) > api.MaxEventTitleBytes {
		out = append(out, ProblemTitleTooLong)
	}
	if len(strings.TrimSpace(in.Location)) > api.MaxEventLocationBytes {
		out = append(out, ProblemLocationTooLong)
	}
	if len(in.Notes) > api.MaxEventNotesBytes {
		out = append(out, ProblemNotesTooLong)
	}
	if len(in.Attendees) > api.MaxEventAttendeesIn {
		out = append(out, ProblemTooManyAttendees)
	}
	for _, a := range in.Attendees {
		if addr, err := mail.ParseAddress(a.Address); err != nil || addr.Address != a.Address {
			out = append(out, ProblemInvalidAttendee)
			break
		}
	}
	if len(in.Alerts) > api.MaxEventAlerts {
		out = append(out, ProblemTooManyAlerts)
	}
	return out
}

// ProblemText is a problem's sentence under the form.
func ProblemText(p Problem, tr Translator) string {
	switch p {
	case ProblemNoCalendar:
		return tr.T("Choose a calendar for the event.")
	case ProblemEndBeforeStart:
		return tr.T("The event must end after it starts.")
	case ProblemTitleTooLong:
		return tr.T("The title is too long.")
	case ProblemLocationTooLong:
		return tr.T("The location is too long.")
	case ProblemNotesTooLong:
		return tr.T("The notes are too long.")
	case ProblemTooManyAttendees:
		return fmt.Sprintf(tr.N("An event can have at most %d attendee.", "An event can have at most %d attendees.",
			api.MaxEventAttendeesIn), api.MaxEventAttendeesIn)
	case ProblemInvalidAttendee:
		return tr.T("An attendee’s address is not valid.")
	}
	return fmt.Sprintf(tr.N("An event can have at most %d alert.", "An event can have at most %d alerts.",
		api.MaxEventAlerts), api.MaxEventAlerts)
}

// EventInput is in as event.create and event.update take it, timed in
// zone (its IANA name), all-day as dates with the exclusive end.
func EventInput(in Input, zone *time.Location) api.EventInput {
	out := api.EventInput{
		Title:     strings.TrimSpace(in.Title),
		AllDay:    in.AllDay,
		Location:  strings.TrimSpace(in.Location),
		Notes:     in.Notes,
		Attendees: in.Attendees,
		Alerts:    in.Alerts,
	}
	if out.Alerts == nil {
		out.Alerts = []int{}
	}
	if in.AllDay {
		out.StartDate = DateOf(in.Start, zone)
		end := StartOfDay(in.End, zone)
		if !end.After(StartOfDay(in.Start, zone)) {
			end = AddDays(StartOfDay(in.Start, zone), 1)
		}
		out.EndDate = DateOf(end, zone)
		return out
	}
	const layout = "2006-01-02T15:04:05"
	out.Start = api.LocalDateTime(in.Start.In(zone).Format(layout))
	out.End = api.LocalDateTime(in.End.In(zone).Format(layout))
	out.TimeZone = api.TimeZone(zone.String())
	return out
}

// SendsInvitations reports whether saving sends invitations: there are
// attendees and someone delivers them (a Graph account always does).
func SendsInvitations(attendees int, source api.CalendarSource, f api.CalendarFeatures) bool {
	return attendees > 0 && (source == api.CalendarSourceGraph || f.Invitations != api.InvitationsViaNone)
}

// PrimaryLabel is the editor's primary button: Save, or Save and Send
// Invitations when saving sends them.
func PrimaryLabel(attendees int, source api.CalendarSource, f api.CalendarFeatures, tr Translator) string {
	if SendsInvitations(attendees, source, f) {
		return tr.T("Save and Send Invitations")
	}
	return tr.T("Save")
}

// EditorDelete is the destructive button at the left of the footer of an
// Edit Event window (editing; never for a new event), offered when the
// event can be deleted, titled as the detail's (RemoveTitle). Pressing it
// asks what the detail's Remove asks (RemoveQuestion, else DeleteQuestion
// under the Mail preference confirm-delete), then runs event.delete with
// the scope the editor was opened with (no second series question) and
// closes the window without the unsaved-changes question.
func EditorDelete(editing bool, e api.Event, tr Translator) (string, bool) {
	if !editing || !e.CanDelete {
		return "", false
	}
	return RemoveTitle(e, tr), true
}

// Cancel is the editor's and the questions' Cancel.
func Cancel(tr Translator) string { return tr.T("Cancel") }

// Footer is the editor's footer note, line by line: who sends the
// invitations (only with attendees), then the attachments. mailAccount
// and mailAddress name the account invitations leave from (features
// invitations "mail").
func Footer(attendees int, f api.CalendarFeatures, mailAccount, mailAddress string, attachments []string, tr Translator) []string {
	var lines []string
	if attendees > 0 {
		switch f.Invitations {
		case api.InvitationsViaMail:
			lines = append(lines, fmt.Sprintf(tr.T("Invitations leave by e-mail from account %s (%s) through the Outbox."),
				cleanLine(mailAccount), cleanLine(mailAddress)))
		case api.InvitationsViaServer:
			lines = append(lines, tr.T("The server sends the invitations."))
		default:
			lines = append(lines, tr.T("No account can send invitations from this calendar."))
		}
	}
	for _, a := range attachments {
		// TRANSLATORS: the event editor's footer: a file attached to the
		// event; %s is its name.
		lines = append(lines, fmt.Sprintf(tr.T("Attachment: %s"), cleanLine(a)))
	}
	return lines
}

// AlertChoices are the Alert popup's values in minutes before the start;
// -1 is None.
var AlertChoices = []int{-1, 0, 5, 10, 15, 30, 60, 1440}

// AlertTitle is an alert's title: None, At time of event, "15 minutes
// before", "1 hour before", "1 day before" (any value, not only the
// choices: a server's 120 is "2 hours before").
func AlertTitle(minutes int, tr Translator) string {
	switch {
	case minutes < 0:
		// TRANSLATORS: the event editor's Alert popup: no alert.
		return tr.C("alert", "None")
	case minutes == 0:
		return tr.T("At time of event")
	case minutes%1440 == 0:
		return fmt.Sprintf(tr.N("%d day before", "%d days before", minutes/1440), minutes/1440)
	case minutes%60 == 0:
		return fmt.Sprintf(tr.N("%d hour before", "%d hours before", minutes/60), minutes/60)
	}
	return fmt.Sprintf(tr.N("%d minute before", "%d minutes before", minutes), minutes)
}

// AllDayAlertChoices are the Alert popup's values of an all-day event:
// None, then days before at 9:00, stored as minutes before its midnight
// (1 day before at 9:00 is 15 hours before; the daemon takes nothing
// negative, so "on the day at 9:00" cannot be stored).
var AllDayAlertChoices = []int{-1, 900, 2340, 9540}

// AllDayAlertDefault is an all-day event's alert when it has one: 1 day
// before at 9:00.
const AllDayAlertDefault = 900

// allDayAlertHour is the hour of the all-day alerts, in minutes after
// midnight.
const allDayAlertHour = 9 * 60

// AlertChoicesFor is the Alert popup's values of a timed or an all-day
// event.
func AlertChoicesFor(allDay bool) []int {
	if allDay {
		return AllDayAlertChoices
	}
	return AlertChoices
}

// AlertTitleFor is an alert's title. An all-day event's value that is a
// whole number of days before 9:00 reads "1 day before (9:00)", "2 days
// before (9:00)", "1 week before (9:00)"; any other value (Outlook's 18
// hours) and every timed one is AlertTitle's.
func AlertTitleFor(minutes int, allDay bool, tr Translator) string {
	if !allDay || minutes <= 0 || (minutes+allDayAlertHour)%1440 != 0 {
		return AlertTitle(minutes, tr)
	}
	d := (minutes + allDayAlertHour) / 1440
	if d == 7 {
		// TRANSLATORS: the Alert popup of an all-day event: a week before
		// the event's day, at 9:00.
		return tr.T("1 week before (9:00)")
	}
	// TRANSLATORS: the Alert popup of an all-day event: a number of days
	// before the event's day, at 9:00.
	return fmt.Sprintf(tr.N("%d day before (9:00)", "%d days before (9:00)", d), d)
}

// AlertOnAllDayToggle is the editor's alert after the All Day switch
// changed (on: now all-day): turning all-day on keeps None and an
// all-day choice, anything else becomes AllDayAlertDefault; turning it
// off keeps None and otherwise takes the preference's default alert.
func AlertOnAllDayToggle(current int, on bool, p api.CalendarPreferences) int {
	if current < 0 {
		return -1
	}
	if on {
		for _, c := range AllDayAlertChoices {
			if c == current {
				return current
			}
		}
		return AllDayAlertDefault
	}
	if p.DefaultAlertMinutes < 0 || p.DefaultAlertMinutes > api.MaxAlertMinutes {
		return -1
	}
	return p.DefaultAlertMinutes
}

// DurationChoices are the Default Duration popup's values in minutes.
var DurationChoices = []int{15, 30, 45, 60, 90, 120}

// DurationTitle is a duration choice's title ("1 h 30 min").
func DurationTitle(minutes int, tr Translator) string {
	return DurationText(time.Duration(minutes)*time.Minute, tr)
}

// TimeZoneLink is the editor's link that opens the zone popover; zone is
// an IANA name.
func TimeZoneLink(zone string, tr Translator) string {
	// TRANSLATORS: the event editor's link to the time-zone popover; %s is
	// a zone's name such as "Europe/Prague".
	return fmt.Sprintf(tr.T("Time zone: %s"), zone)
}

// ZoneSearch is the zone popover's search field's placeholder.
func ZoneSearch(tr Translator) string { return tr.T("Search Time Zones") }

// The editor's field labels.

// TitlePlaceholder is the title field's placeholder.
func TitlePlaceholder(tr Translator) string { return tr.T("Title") }

// FieldLabel is the label of an editor field.
func FieldLabel(f Field, tr Translator) string {
	switch f {
	case FieldCalendar:
		return tr.T("Calendar:")
	case FieldStart:
		return tr.T("Starts:")
	case FieldEnd:
		return tr.T("Ends:")
	case FieldRepeat:
		return tr.T("Repeat:")
	case FieldLocation:
		return tr.T("Location:")
	case FieldAttendees:
		return tr.T("Attendees:")
	case FieldAvailability:
		return tr.T("Availability:")
	case FieldAlert:
		return tr.T("Alert:")
	case FieldNotes:
		return tr.T("Notes:")
	}
	return tr.T("All Day:")
}

// Field is a row of the editor's form.
type Field int

// The fields, in the form's order.
const (
	FieldCalendar Field = iota
	FieldAllDay
	FieldStart
	FieldEnd
	FieldRepeat
	FieldLocation
	FieldAttendees
	FieldAvailability
	FieldAlert
	FieldNotes
)

// Fields lists the form's rows in order.
var Fields = []Field{FieldCalendar, FieldAllDay, FieldStart, FieldEnd, FieldRepeat, FieldLocation,
	FieldAttendees, FieldAvailability, FieldAlert, FieldNotes}

// AddVideoCall is the location row's menu button; PasteLink its item for
// a link the user has.
func AddVideoCall(tr Translator) string { return tr.T("Add Video Call") }

// PasteLink is the Add Video Call menu's item for a typed link.
func PasteLink(tr Translator) string { return tr.T("Paste Link…") }

// VideoProviderName is an online-meeting provider's name in the Add
// Video Call menu (Graph's open enum; brand names are not translated).
func VideoProviderName(p string, tr Translator) string {
	switch p {
	case "teamsForBusiness", "teamsForConsumer":
		return "Microsoft Teams"
	case "skypeForBusiness":
		return "Skype for Business"
	case "skypeForConsumer":
		return "Skype"
	}
	// TRANSLATORS: an online-meeting provider the calendar does not know.
	return tr.T("Online Meeting")
}

// Attach is the editor's toolbar button for files.
func Attach(tr Translator) string { return tr.T("Attach") }

// CloseQuestion is asked when an editor with changes closes (Save /
// Discard / Cancel); nothing is stored before Save.
func CloseQuestion(tr Translator) Question {
	return Question{
		Heading:     tr.T("Save the Changes to This Event?"),
		Body:        tr.T("If you don’t save, your changes are lost."),
		Confirm:     tr.T("Save"),
		Alternative: tr.T("Discard"),
	}
}

// MailKind is an iMIP message the daemon sends with the client's texts.
type MailKind int

// The kinds.
const (
	MailInvitation MailKind = iota
	MailUpdate
	MailCancel
	MailAccepted
	MailTentative
	MailDeclined
)

// MailKindOf is the reply kind of an answer.
func MailKindOf(r api.EventResponse) MailKind {
	switch r {
	case api.ResponseTentative:
		return MailTentative
	case api.ResponseDeclined:
		return MailDeclined
	}
	return MailAccepted
}

// MailTexts is the subject and text of an iMIP message about o (its
// title, time and place); me is the user's name or address (a reply's
// text names who answered).
func MailTexts(kind MailKind, o api.Occurrence, me string, env Env) api.MailText {
	tr := env.Tr
	title := cleanTitle(o.Title, tr)
	me = cleanLine(me)
	var subject, sentence string
	switch kind {
	case MailUpdate:
		subject = fmt.Sprintf(tr.T("Updated invitation: %s"), title)
		sentence = fmt.Sprintf(tr.T("The event “%s” has changed."), title)
	case MailCancel:
		subject = fmt.Sprintf(tr.T("Cancelled: %s"), title)
		sentence = fmt.Sprintf(tr.T("The event “%s” has been cancelled."), title)
	case MailAccepted:
		subject = fmt.Sprintf(tr.T("Accepted: %s"), title)
		sentence = fmt.Sprintf(tr.T("%s has accepted the invitation to “%s”."), me, title)
	case MailTentative:
		subject = fmt.Sprintf(tr.T("Tentative: %s"), title)
		sentence = fmt.Sprintf(tr.T("%s has tentatively accepted the invitation to “%s”."), me, title)
	case MailDeclined:
		subject = fmt.Sprintf(tr.T("Declined: %s"), title)
		sentence = fmt.Sprintf(tr.T("%s has declined the invitation to “%s”."), me, title)
	default:
		subject = fmt.Sprintf(tr.T("Invitation: %s"), title)
		sentence = fmt.Sprintf(tr.T("You are invited to “%s”."), title)
	}
	lines := []string{sentence, ""}
	if when := TimeLine(o, env); when != "" {
		// TRANSLATORS: a line of an invitation e-mail: the event's time.
		lines = append(lines, fmt.Sprintf(tr.T("When: %s"), when))
	}
	if where := cleanLine(o.Location); where != "" {
		// TRANSLATORS: a line of an invitation e-mail: the event's place.
		lines = append(lines, fmt.Sprintf(tr.T("Where: %s"), where))
	}
	return api.MailText{Subject: capBytes(subject, api.MaxMailTextBytes),
		Text: capBytes(strings.TrimRight(strings.Join(lines, "\n"), "\n"), api.MaxMailTextBytes)}
}

// capBytes cuts s to n bytes on a character boundary.
func capBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}

// ScopeAction is what a scope question is about.
type ScopeAction int

// The actions.
const (
	ScopeEdit ScopeAction = iota
	ScopeDelete
	ScopeRespond
)

// ScopeChoice is a button of the scope question.
type ScopeChoice struct {
	Scope api.EditScope
	Title string
}

// ScopeQuestion is the question asked before a change of an occurrence
// of a series.
type ScopeQuestion struct {
	Heading, Body string
	// Choices in the buttons' order; Cancel is always there.
	Choices []ScopeChoice
}

// ScopeTitle is a scope's button: This Event Only, This and Future
// Events, All Events.
func ScopeTitle(s api.EditScope, tr Translator) string {
	switch s {
	case api.EditScopeThisAndFuture:
		return tr.T("This and Future Events")
	case api.EditScopeAll:
		return tr.T("All Events")
	}
	return tr.T("This Event Only")
}

// ScopeQuestionFor is the question of an action on an occurrence of a
// series. An answer offers this and all only; a rule outside the
// editor's shape (complex) is changed only as a whole series
// (docs/calendar.md D19), so the edit and delete offer All Events alone.
func ScopeQuestionFor(a ScopeAction, complex bool, tr Translator) ScopeQuestion {
	scopes := []api.EditScope{api.EditScopeThis, api.EditScopeThisAndFuture, api.EditScopeAll}
	var q ScopeQuestion
	switch a {
	case ScopeDelete:
		q.Heading = tr.T("Delete a Repeating Event?")
		q.Body = tr.T("Do you want to delete only this occurrence, or also the ones after it?")
	case ScopeRespond:
		q.Heading = tr.T("Answer for the Whole Series?")
		q.Body = tr.T("Do you want to answer for this occurrence only, or for every occurrence?")
		scopes = []api.EditScope{api.EditScopeThis, api.EditScopeAll}
	default:
		q.Heading = tr.T("Change a Repeating Event?")
		q.Body = tr.T("Do you want to change only this occurrence, or also the ones after it?")
	}
	if complex && a != ScopeRespond {
		scopes = []api.EditScope{api.EditScopeAll}
		q.Body = tr.T("This event repeats in a way that can be changed only for the whole series.")
	}
	for _, s := range scopes {
		q.Choices = append(q.Choices, ScopeChoice{Scope: s, Title: ScopeTitle(s, tr)})
	}
	return q
}
