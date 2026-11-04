// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendarctl

import (
	"net/url"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/calendardata"
	"github.com/schotek/malachi/ui/internal/recipients"
)

// EventEditorController is one New Event / Edit Event window
// (docs/calendar.md §5.6; port of EventEditorController.swift): the input
// (calendar.Input in Zone) with its validation, the writable calendars,
// the Repeat and Alert choices, the attendees through the compose
// window's recipients.Tokens model, the attendees' availability
// (calendar.freeBusy AvailabilityDebounce after the last change, only
// when the calendar's account has features.freeBusy), Next Free Slot
// (calendar.findFreeTime), the attachments (attachment.import), the
// primary button and the footer, and the save paths: event.create, or
// event.update with the scope the detail asked. Nothing is stored before
// Save (D6): closing with changes asks Save / Discard / Cancel, and
// Discard just closes. A conflict on Save offers Reload Event: the
// window closes unasked and OnReload reopens the event as the daemon has
// it.
type EventEditorController struct {
	src  calendardata.DataSource
	opts Options

	// editing is the event of Edit Event (nil: New Event).
	editing      *api.Event
	scope        api.EditScope
	findFreeSlot bool

	// OnChange: the state changed; the view reads it anew.
	OnChange func()
	// OnQuestion asks the close question (and Delete's, without
	// OnDeleteQuestion); nil answers Cancel.
	OnQuestion func(q calendar.Question, answer func(Answer))
	// OnDeleteQuestion asks Delete's questions (Cancel the default, the
	// confirmation destructive); nil asks through OnQuestion.
	OnDeleteQuestion func(q calendar.Question, answer func(Answer))
	// OnSaved: stored; the window closes.
	OnSaved func(api.Event)
	// OnClose: the window may close.
	OnClose func()
	// OnError: the inline error was set (Error), for the view to announce.
	OnError func(text string)
	// OnReload: Reload Event after a conflict fetched the event anew; the
	// window closes unasked and opens fresh with scope in its place.
	OnReload func(fresh api.Event, scope api.EditScope)
	// ConfirmDelete is the Mail preference "Ask before moving to the
	// Trash" (confirm-delete); nil is true.
	ConfirmDelete func() bool

	input        calendar.Input
	zone         *time.Location
	repeatChoice calendar.Repeat
	recurrence   *api.Recurrence
	original     *api.Recurrence
	alert        int
	attendees    *recipients.Tokens
	attachments  []api.DraftAttachment
	videoCall    *api.VideoCallInput
	problems     []calendar.Problem
	availability *api.CalendarFreeBusyResult
	freeSlotText string
	dirty        bool
	saving       bool
	errText      string
	conflict     bool

	availabilityPending    bool
	availabilityGeneration int
	closed                 bool
}

// EditorCalendar is a choice of the Calendar popup.
type EditorCalendar struct {
	ID        api.CalendarID
	AccountID api.AccountID
	// Title is "Account · Calendar".
	Title string
	Color string
}

// AvailabilityDebounce is how long the availability waits after the last
// change.
const AvailabilityDebounce = 500 * time.Millisecond

// FreeSlotDays is how far Next Free Slot looks.
const FreeSlotDays = 14

// NewEventEditor is New Event: defaults nil are calendar.NewEventDefaults
// of now (OriginNow); calendarID a preset calendar ("" the first
// writable, default first).
func NewEventEditor(src calendardata.DataSource, o Options, defaults *calendar.Defaults, calendarID api.CalendarID) *EventEditorController {
	c := &EventEditorController{src: src, opts: o, zone: o.Loc(), attendees: recipients.New(), alert: -1}
	s := src.Snapshot()
	d := calendar.NewEventDefaults(o.Clock(), calendar.OriginNow, s.Preferences, c.zone)
	if defaults != nil {
		d = *defaults
	}
	if calendarID == "" {
		if w := s.WritableCalendars(); len(w) > 0 {
			calendarID = w[0].ID
		}
	}
	c.input = calendar.Input{AllDay: d.AllDay, Start: d.Start.In(c.zone), End: d.End.In(c.zone), CalendarID: calendarID}
	if len(d.Alerts) > 0 {
		c.alert = d.Alerts[0]
	}
	c.repeatChoice = calendar.RepeatNever
	c.finishInit()
	return c
}

// EditEventEditor is Edit Event of e with the scope the detail's series
// question chose; findFreeSlot (the detail's Find Another Time) runs Next
// Free Slot from Start.
func EditEventEditor(src calendardata.DataSource, o Options, e api.Event, scope api.EditScope, findFreeSlot bool) *EventEditorController {
	c := &EventEditorController{src: src, opts: o, editing: &e, scope: scope, findFreeSlot: findFreeSlot,
		attendees: recipients.New(), alert: -1}
	c.zone = o.Loc()
	if e.TimeZone != "" {
		if l, err := time.LoadLocation(string(e.TimeZone)); err == nil {
			c.zone = l
		}
	}
	start, end, ok := calendar.Span(e.Occurrence, c.zone)
	if !ok {
		now := o.Clock().In(c.zone)
		start, end = now, now
	}
	c.input = calendar.Input{Title: e.Title, AllDay: e.AllDay, Start: start, End: end, Location: e.Location,
		Notes: e.Notes, CalendarID: e.CalendarID}
	if len(e.Alerts) > 0 {
		c.alert = e.Alerts[0]
	}
	c.recurrence, c.original = e.Recurrence, e.Recurrence
	c.repeatChoice = calendar.RepeatOf(e.Recurrence, start)
	if v := e.VideoCallLink; v != nil && v.URL != "" {
		c.videoCall = &api.VideoCallInput{Mode: api.VideoCallLink, URL: v.URL}
	}
	for _, a := range e.Attendees {
		if !a.Me && !a.Organizer {
			c.attendees.Add(api.Address{Name: a.Name, Address: a.Address})
		}
	}
	c.finishInit()
	return c
}

func (c *EventEditorController) finishInit() {
	c.input.Alerts = editorAlerts(c.alert)
	c.syncAttendees()
}

func editorAlerts(m int) []int {
	if m >= 0 {
		return []int{m}
	}
	return []int{}
}

// Start starts the availability for the first input (and Next Free Slot
// when the editor was opened for it).
func (c *EventEditorController) Start() {
	c.scheduleAvailability()
	if c.findFreeSlot && !c.input.AllDay && len(c.Calendars()) > 0 {
		c.NextFreeSlot()
	}
}

// Stop: the window closed; no answer is taken any more.
func (c *EventEditorController) Stop() {
	c.closed = true
	c.availabilityGeneration++
	c.availabilityPending = false
}

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

func (c *EventEditorController) tr() calendar.Translator { return c.opts.Env.Tr }

// Input is what the form holds (times in Zone).
func (c *EventEditorController) Input() calendar.Input { return c.input }

// Zone is the event's time zone.
func (c *EventEditorController) Zone() *time.Location { return c.zone }

// Editing reports Edit Event, with the event and the scope.
func (c *EventEditorController) Editing() (api.Event, api.EditScope, bool) {
	if c.editing == nil {
		return api.Event{}, "", false
	}
	return *c.editing, c.scope, true
}

// RepeatChoice is the Repeat popup's choice.
func (c *EventEditorController) RepeatChoice() calendar.Repeat { return c.repeatChoice }

// Recurrence is the rule sent (the choice's, a custom one, the event's).
func (c *EventEditorController) Recurrence() *api.Recurrence { return c.recurrence }

// Alert is the Alert popup's minutes before the start (-1 None).
func (c *EventEditorController) Alert() int { return c.alert }

// Attendees is the attendee field's model; EditAttendees changes it.
func (c *EventEditorController) Attendees() *recipients.Tokens { return c.attendees }

// Attachments are the imported attachments.
func (c *EventEditorController) Attachments() []api.DraftAttachment { return c.attachments }

// VideoCall is the video call (nil none).
func (c *EventEditorController) VideoCall() *api.VideoCallInput { return c.videoCall }

// Problems are the problems of the last Save (cleared by the next change).
func (c *EventEditorController) Problems() []calendar.Problem { return c.problems }

// ProblemTexts are the problem lines shown inline under the form.
func (c *EventEditorController) ProblemTexts() []string {
	out := make([]string, 0, len(c.problems))
	for _, p := range c.problems {
		out = append(out, calendar.ProblemText(p, c.tr()))
	}
	return out
}

// Availability is calendar.freeBusy's answer for the input (nil none).
func (c *EventEditorController) Availability() *api.CalendarFreeBusyResult { return c.availability }

// AvailabilityPending reports a request waiting or on its way.
func (c *EventEditorController) AvailabilityPending() bool { return c.availabilityPending }

// FreeSlotText is the Next Free Slot line ("" none).
func (c *EventEditorController) FreeSlotText() string { return c.freeSlotText }

// HasChanges reports a change since the window opened.
func (c *EventEditorController) HasChanges() bool { return c.dirty }

// Saving reports a write on its way (the buttons wait).
func (c *EventEditorController) Saving() bool { return c.saving }

// Error is the inline error of Save, Delete, Attach or Next Free Slot
// ("" none).
func (c *EventEditorController) Error() string { return c.errText }

// Conflict reports that the inline error is a conflict: the view offers
// ReloadTitle (Reload).
func (c *EventEditorController) Conflict() bool { return c.conflict }

// ReloadTitle is the button shown with a conflict.
func (c *EventEditorController) ReloadTitle() string { return calendar.ReloadEvent(c.tr()) }

// WindowTitle is New Event / Edit Event.
func (c *EventEditorController) WindowTitle() string {
	if c.editing != nil {
		return calendar.EditEvent(c.tr())
	}
	return calendar.NewEvent(c.tr())
}

// Calendars is the Calendar popup: the writable calendars (Edit: the
// event's own).
func (c *EventEditorController) Calendars() []EditorCalendar {
	s := c.src.Snapshot()
	var records []api.Calendar
	if c.editing != nil {
		if cal, ok := s.Calendar(c.editing.CalendarID); ok && c.editing.CalendarID != "" {
			records = []api.Calendar{cal}
		}
	} else {
		records = s.WritableCalendars()
	}
	out := make([]EditorCalendar, 0, len(records))
	for _, r := range records {
		out = append(out, EditorCalendar{ID: r.ID, AccountID: r.AccountID, Color: r.Color,
			Title: calendar.Dot(s.AccountName(r.AccountID), board.CleanLine(r.Name, api.MaxEventLocationBytes), c.tr())})
	}
	return out
}

func (c *EventEditorController) calendarAccount() (api.CalendarAccount, bool) {
	s := c.src.Snapshot()
	if c.input.CalendarID == "" {
		return api.CalendarAccount{}, false
	}
	cal, ok := s.Calendar(c.input.CalendarID)
	if !ok {
		return api.CalendarAccount{}, false
	}
	return s.CalendarAccount(cal.AccountID)
}

// RepeatChoices are the Repeat popup's choices.
func (c *EventEditorController) RepeatChoices() []calendar.Repeat { return calendar.Repeats }

// RepeatTitle is a Repeat choice's title.
func (c *EventEditorController) RepeatTitle(r calendar.Repeat) string {
	return calendar.RepeatTitle(r, c.tr())
}

// AlertChoices are the Alert popup's values (day-based at 9:00 for an
// all-day event, D31).
func (c *EventEditorController) AlertChoices() []int { return calendar.AlertChoicesFor(c.input.AllDay) }

// AlertTitle is the Alert popup's title of minutes for the current All
// Day state.
func (c *EventEditorController) AlertTitle(minutes int) string {
	return calendar.AlertTitleFor(minutes, c.input.AllDay, c.tr())
}

// PrimaryLabel is Save / Save and Send Invitations (SendsInvitations).
func (c *EventEditorController) PrimaryLabel() string {
	a, ok := c.calendarAccount()
	if !ok {
		return calendar.PrimaryLabel(0, api.CalendarSourceCalDAV, api.CalendarFeatures{}, c.tr())
	}
	return calendar.PrimaryLabel(len(c.input.Attendees), a.Source, a.Features, c.tr())
}

// SendsInvitations reports that Save sends invitations.
func (c *EventEditorController) SendsInvitations() bool {
	a, ok := c.calendarAccount()
	return ok && calendar.SendsInvitations(len(c.input.Attendees), a.Source, a.Features)
}

// SaveKeyAllowed: Ctrl+S saves only without attendees (Ctrl+Return is
// the primary).
func (c *EventEditorController) SaveKeyAllowed() bool { return len(c.input.Attendees) == 0 }

// Footer is the footer's lines: who sends the invitations, the
// attachments.
func (c *EventEditorController) Footer() []string {
	s := c.src.Snapshot()
	a, ok := c.calendarAccount()
	f := a.Features
	mail := f.MailAccountID
	if mail == "" && ok {
		mail = a.AccountID
	}
	var name, addr string
	if mail != "" {
		name, addr = s.AccountName(mail), s.MyAddress(mail)
	}
	files := make([]string, 0, len(c.attachments))
	for _, at := range c.attachments {
		files = append(files, at.Filename)
	}
	return calendar.Footer(len(c.input.Attendees), f, name, addr, files, c.tr())
}

// ShowsAvailability: the account answers for others and there are
// attendees.
func (c *EventEditorController) ShowsAvailability() bool {
	a, ok := c.calendarAccount()
	return ok && a.Features.FreeBusy && len(c.input.Attendees) > 0
}

// AvailabilityDay is the day the availability rows span (in the
// options' zone).
func (c *EventEditorController) AvailabilityDay() (from, to time.Time) {
	d := calendar.StartOfDay(c.input.Start, c.opts.Loc())
	return d, calendar.AddDays(d, 1)
}

// AvailabilityRows are the rows: the user, then the attendees.
func (c *EventEditorController) AvailabilityRows() []calendar.AvailabilityRow {
	if !c.ShowsAvailability() || c.availability == nil {
		return nil
	}
	names := map[string]string{}
	for _, t := range c.attendees.Items() {
		if t.Address != nil {
			names[t.Address.Address] = t.Label()
		}
	}
	from, to := c.AvailabilityDay()
	return calendar.AvailabilityRows(*c.availability, names, from, to, c.tr())
}

// AvailabilitySummary is the summary line(s) of the proposed time.
func (c *EventEditorController) AvailabilitySummary() []string {
	if !c.ShowsAvailability() || c.availability == nil {
		return nil
	}
	return calendar.Summary(*c.availability, c.input.Start, c.input.End, c.opts.Env)
}

// VideoProviders are what Add Video Call offers: the chosen calendar's
// onlineMeetingProviders when its account can create meetings.
func (c *EventEditorController) VideoProviders() []string {
	a, ok := c.calendarAccount()
	if !ok || !a.Features.OnlineMeetings {
		return nil
	}
	cal, _ := c.src.Snapshot().Calendar(c.input.CalendarID)
	return cal.OnlineMeetingProviders
}

// TimeZoneLink is the time-zone link's text.
func (c *EventEditorController) TimeZoneLink() string {
	return calendar.TimeZoneLink(string(calendardata.ZoneName(c.zone)), c.tr())
}

// DeleteTitle is Edit Event's footer button at the left ("Delete" /
// "Remove from Calendar"); false without one.
func (c *EventEditorController) DeleteTitle() (string, bool) {
	if c.editing == nil {
		return "", false
	}
	return calendar.EditorDelete(true, *c.editing, c.tr())
}

// ---------------------------------------------------------------------------
// Editing
// ---------------------------------------------------------------------------

func (c *EventEditorController) edited(again bool) {
	c.dirty = true
	c.problems = nil
	c.errText = ""
	c.conflict = false
	if again {
		c.scheduleAvailability()
	}
	c.changed()
}

// SetTitle sets the title.
func (c *EventEditorController) SetTitle(s string) {
	if s == c.input.Title {
		return
	}
	c.input.Title = s
	c.edited(false)
}

// SetAllDay switches All Day; the alert follows (AlertOnAllDayToggle).
func (c *EventEditorController) SetAllDay(on bool) {
	if on == c.input.AllDay {
		return
	}
	c.input.AllDay = on
	prefs := c.src.Snapshot().Preferences
	if on {
		c.input.Start = calendar.StartOfDay(c.input.Start, c.zone)
		end := calendar.StartOfDay(c.input.End, c.zone)
		if !end.After(c.input.Start) {
			end = calendar.AddDays(c.input.Start, 1)
		}
		c.input.End = end
	} else {
		s := c.input.Start.In(c.zone)
		c.input.Start = time.Date(s.Year(), s.Month(), s.Day(), 9, 0, 0, 0, c.zone)
		c.input.End = c.input.Start.Add(calendar.DefaultDuration(prefs))
	}
	c.alert = calendar.AlertOnAllDayToggle(c.alert, on, prefs)
	c.input.Alerts = editorAlerts(c.alert)
	c.edited(true)
}

// SetStart sets the start; the end moves with it (the duration stays).
func (c *EventEditorController) SetStart(t time.Time) {
	if t.Equal(c.input.Start) {
		return
	}
	d := max(c.input.End.Sub(c.input.Start), 0)
	c.input.Start = t.In(c.zone)
	c.input.End = c.input.Start.Add(d)
	c.edited(true)
}

// SetEnd sets the end.
func (c *EventEditorController) SetEnd(t time.Time) {
	if t.Equal(c.input.End) {
		return
	}
	c.input.End = t.In(c.zone)
	c.edited(true)
}

// SetZone is the time-zone popover: the wall-clock times stay, the zone
// changes.
func (c *EventEditorController) SetZone(z *time.Location) {
	if z == nil || z.String() == c.zone.String() {
		return
	}
	s, e := c.input.Start.In(c.zone), c.input.End.In(c.zone)
	c.zone = z
	c.input.Start = time.Date(s.Year(), s.Month(), s.Day(), s.Hour(), s.Minute(), 0, 0, z)
	c.input.End = time.Date(e.Year(), e.Month(), e.Day(), e.Hour(), e.Minute(), 0, 0, z)
	c.edited(true)
}

// SetVideoCall is Add Video Call ▾ (mode create with a provider of
// VideoProviders), Paste Link… (mode link with an https URL that has a
// host) or remove (nil or mode none); false (nothing changed) otherwise.
func (c *EventEditorController) SetVideoCall(v *api.VideoCallInput) bool {
	var next *api.VideoCallInput
	switch {
	case v == nil || v.Mode == api.VideoCallNone:
	case v.Mode == api.VideoCallLink:
		raw := strings.TrimSpace(v.URL)
		u, err := url.Parse(raw)
		if err != nil || strings.ToLower(u.Scheme) != "https" || u.Host == "" {
			return false
		}
		next = &api.VideoCallInput{Mode: api.VideoCallLink, URL: raw}
	case v.Mode == api.VideoCallCreate:
		if v.Provider == "" || !slices.Contains(c.VideoProviders(), v.Provider) {
			return false
		}
		next = &api.VideoCallInput{Mode: api.VideoCallCreate, Provider: v.Provider}
	default:
		return false
	}
	if reflect.DeepEqual(next, c.videoCall) {
		return true
	}
	c.videoCall = next
	c.edited(false)
	return true
}

// SetLocation sets the place.
func (c *EventEditorController) SetLocation(s string) {
	if s == c.input.Location {
		return
	}
	c.input.Location = s
	c.edited(false)
}

// SetNotes sets the notes.
func (c *EventEditorController) SetNotes(s string) {
	if s == c.input.Notes {
		return
	}
	c.input.Notes = s
	c.edited(false)
}

// SetCalendar chooses a calendar of Calendars; a meeting of a provider
// the new calendar does not offer goes.
func (c *EventEditorController) SetCalendar(id api.CalendarID) {
	if id == c.input.CalendarID || !slices.ContainsFunc(c.Calendars(), func(e EditorCalendar) bool { return e.ID == id }) {
		return
	}
	c.input.CalendarID = id
	if v := c.videoCall; v != nil && v.Mode == api.VideoCallCreate && !slices.Contains(c.VideoProviders(), v.Provider) {
		c.videoCall = nil
	}
	c.edited(true)
}

// SetRepeat is the Repeat popup (Custom… opens the popover:
// SetRecurrence).
func (c *EventEditorController) SetRepeat(r calendar.Repeat) {
	if r == calendar.RepeatCustom {
		return
	}
	c.repeatChoice = r
	c.recurrence = calendar.RepeatRecurrence(r, c.input.Start.In(c.zone))
	c.edited(false)
}

// SetRecurrence is the Custom… popover's rule (calendar.CustomRecurrence).
func (c *EventEditorController) SetRecurrence(r api.Recurrence) {
	c.recurrence = &r
	c.repeatChoice = calendar.RepeatOf(&r, c.input.Start.In(c.zone))
	c.edited(false)
}

// SetAlert sets the alert (-1 None).
func (c *EventEditorController) SetAlert(minutes int) {
	if minutes == c.alert {
		return
	}
	c.alert = minutes
	c.input.Alerts = editorAlerts(minutes)
	c.edited(false)
}

// EditAttendees applies the attendee field's keystroke, paste or token
// change to the model.
func (c *EventEditorController) EditAttendees(change func(t *recipients.Tokens)) {
	items, pending := slices.Clone(c.attendees.Items()), c.attendees.Pending()
	change(c.attendees)
	if reflect.DeepEqual(items, c.attendees.Items()) && pending == c.attendees.Pending() {
		return
	}
	before := c.input.Attendees
	c.syncAttendees()
	c.edited(!reflect.DeepEqual(before, c.input.Attendees))
}

func (c *EventEditorController) syncAttendees() {
	addrs, _ := c.attendees.Resolved()
	out := make([]api.EventInputAttendee, 0, len(addrs))
	for _, a := range addrs {
		name := a.Name
		if board.CleanLine(name, api.MaxEventLocationBytes) == "" {
			name = ""
		}
		out = append(out, api.EventInputAttendee{Name: name, Address: a.Address})
	}
	c.input.Attendees = out
}

// ---------------------------------------------------------------------------
// Availability
// ---------------------------------------------------------------------------

func (c *EventEditorController) scheduleAvailability() {
	c.availabilityGeneration++
	c.availabilityPending = false
	a, ok := c.calendarAccount()
	if !c.ShowsAvailability() || !ok {
		c.availability = nil
		return
	}
	g := c.availabilityGeneration
	from, to := c.AvailabilityDay()
	addrs := make([]string, 0, len(c.input.Attendees))
	for _, at := range c.input.Attendees[:min(len(c.input.Attendees), api.MaxFreeBusyAttendees)] {
		addrs = append(addrs, at.Address)
	}
	p := api.CalendarFreeBusyParams{AccountID: a.AccountID, Attendees: addrs, From: from, To: to}
	c.availabilityPending = true
	c.opts.After(AvailabilityDebounce, func() {
		if c.closed || g != c.availabilityGeneration {
			return
		}
		c.src.FreeBusy(p, func(r *api.CalendarFreeBusyResult, _ *api.Error) {
			if c.closed || g != c.availabilityGeneration {
				return
			}
			c.availabilityPending = false
			c.availability = r
			c.changed()
		})
	})
}

// NextFreeSlot is the first free slot of the event's length after its
// start in the next FreeSlotDays; moves the event there.
func (c *EventEditorController) NextFreeSlot() {
	a, ok := c.calendarAccount()
	if !ok || c.input.AllDay {
		return
	}
	minutes := max(15, int(c.input.End.Sub(c.input.Start)/time.Minute))
	from := c.input.Start
	if now := c.opts.Clock(); now.After(from) {
		from = now
	}
	addrs := make([]string, 0, len(c.input.Attendees))
	for _, at := range c.input.Attendees {
		addrs = append(addrs, at.Address)
	}
	p := api.CalendarFindFreeTimeParams{AccountID: a.AccountID, Attendees: addrs, DurationMinutes: minutes, From: from,
		To: from.In(c.opts.Loc()).AddDate(0, 0, FreeSlotDays), Limit: 5}
	c.src.FindFreeTime(p, func(r *api.CalendarFindFreeTimeResult, err *api.Error) {
		if c.closed {
			return
		}
		if err != nil || r == nil {
			c.setError(err, "")
			c.changed()
			return
		}
		if slot, ok := calendar.NextFreeSlot(*r, from); ok {
			c.input.Start, c.input.End = slot.Start.In(c.zone), slot.End.In(c.zone)
			c.freeSlotText = calendar.NextFreeSlotText(slot, c.opts.Env)
			c.edited(true)
			return
		}
		c.freeSlotText = calendar.NoFreeSlot(c.tr())
		c.changed()
	})
}

// ---------------------------------------------------------------------------
// Attachments
// ---------------------------------------------------------------------------

// Attach is attachment.import of a file the user picked, under the
// calendar's account.
func (c *EventEditorController) Attach(path string) {
	a, ok := c.calendarAccount()
	account := a.AccountID
	if !ok {
		if cs := c.Calendars(); len(cs) > 0 {
			account = cs[0].AccountID
		}
	}
	if account == "" || len(c.attachments) >= api.MaxEventAttachments {
		return
	}
	c.src.ImportAttachment(api.AttachmentImportParams{AccountID: account, Path: path},
		func(r *api.AttachmentImportResult, err *api.Error) {
			if c.closed {
				return
			}
			if err != nil || r == nil {
				c.setError(err, c.tr().T("Saving the attachment"))
				c.changed()
				return
			}
			c.attachments = append(c.attachments, r.Attachment)
			c.edited(false)
		})
}

// RemoveAttachment removes an attachment's chip.
func (c *EventEditorController) RemoveAttachment(id string) {
	n := len(c.attachments)
	c.attachments = slices.DeleteFunc(c.attachments, func(a api.DraftAttachment) bool { return a.ID == id })
	if len(c.attachments) != n {
		c.edited(false)
	}
}

// ---------------------------------------------------------------------------
// Save, delete and close
// ---------------------------------------------------------------------------

// EventInput is what Save would send.
func (c *EventEditorController) EventInput() api.EventInput {
	e := calendar.EventInput(c.input, c.zone)
	switch {
	case c.editing != nil && c.scope == api.EditScopeThis:
		e.Recurrence = nil
	case c.editing != nil && c.original != nil && c.original.Complex && reflect.DeepEqual(c.recurrence, c.original):
		e.KeepRecurrence = true
	default:
		e.Recurrence = c.recurrence
	}
	e.VideoCall = c.videoCall
	for _, a := range c.attachments {
		e.Attachments = append(e.Attachments, a.ID)
	}
	return e
}

// Save is the primary button: validates (the problems shown inline),
// then event.create or event.update with the scope; done (may be nil)
// hears whether it was stored (the window then closes through OnSaved).
func (c *EventEditorController) Save(done func(stored bool)) {
	finish := func(ok bool) {
		if done != nil {
			done(ok)
		}
	}
	if c.saving {
		finish(false)
		return
	}
	p := calendar.Validate(c.input)
	if _, invalid := c.attendees.Resolved(); len(invalid) > 0 && !slices.Contains(p, calendar.ProblemInvalidAttendee) {
		p = append(p, calendar.ProblemInvalidAttendee)
	}
	c.problems = p
	if len(p) > 0 || c.input.CalendarID == "" {
		c.changed()
		finish(false)
		return
	}
	if c.editing != nil && c.editing.EventID == "" {
		finish(false)
		return
	}
	s := c.src.Snapshot()
	cal, _ := s.Calendar(c.input.CalendarID)
	hasAttendees := len(c.input.Attendees) > 0
	o := api.Occurrence{Title: c.input.Title, AllDay: c.input.AllDay, Location: strings.TrimSpace(c.input.Location)}
	if c.input.AllDay {
		o.StartDate, o.EndDate = calendar.DateOf(c.input.Start, c.zone), calendar.DateOf(c.input.End, c.zone)
	} else {
		st, en := c.input.Start, c.input.End
		o.Start, o.End = &st, &en
	}
	var mail *api.MailText
	var send *bool
	c.saving, c.errText, c.conflict = true, "", false
	c.changed()
	answer := func(ev *api.Event, err *api.Error) {
		c.saving = false
		if c.closed {
			finish(err == nil)
			return
		}
		if err != nil || ev == nil {
			c.setError(err, "")
			c.changed()
			finish(false)
			return
		}
		c.dirty = false
		if c.OnSaved != nil {
			c.OnSaved(*ev)
		}
		c.changed()
		finish(true)
	}
	if c.editing == nil {
		if hasAttendees {
			m := calendar.MailTexts(calendar.MailInvitation, o, s.MyName(cal.AccountID), c.opts.Env)
			t := true
			mail, send = &m, &t
		}
		c.src.CreateEvent(api.EventCreateParams{CalendarID: c.input.CalendarID, Event: c.EventInput(),
			SendInvitations: send, Mail: mail}, func(r *api.EventCreateResult, err *api.Error) {
			var ev *api.Event
			if r != nil {
				ev = &r.Event
			}
			answer(ev, err)
		})
		return
	}
	e := c.editing
	if hasAttendees {
		m := calendar.MailTexts(calendar.MailUpdate, o, s.MyName(cal.AccountID), c.opts.Env)
		t := true
		mail, send = &m, &t
	}
	rec := e.RecurrenceID
	if c.scope == api.EditScopeAll {
		rec = ""
	}
	c.src.UpdateEvent(api.EventUpdateParams{EventID: e.EventID, RecurrenceID: rec, Scope: c.scope, ETag: e.ETag,
		Event: c.EventInput(), SendUpdates: send, Mail: mail}, func(r *api.EventUpdateResult, err *api.Error) {
		var ev *api.Event
		if r != nil {
			ev = &r.Event
		}
		answer(ev, err)
	})
}

// Reload is Reload Event after a conflict: the event as the daemon has
// it now; OnReload reopens it (the edits are dropped, the window closes
// unasked).
func (c *EventEditorController) Reload() {
	if c.editing == nil || c.editing.EventID == "" {
		return
	}
	scope := c.scope
	c.src.Event(c.editing.EventID, c.editing.RecurrenceID, true, func(fresh *api.Event, err *api.Error) {
		if c.closed {
			return
		}
		if err != nil || fresh == nil {
			c.setError(err, "")
			c.changed()
			return
		}
		c.dirty = false
		if c.OnReload != nil {
			c.OnReload(*fresh, scope)
		}
	})
}

func (c *EventEditorController) askDelete(q calendar.Question, answer func(Answer)) {
	switch {
	case c.OnDeleteQuestion != nil:
		c.OnDeleteQuestion(q, answer)
	case c.OnQuestion != nil:
		c.OnQuestion(q, answer)
	default:
		answer(AnswerCancel)
	}
}

// Delete is the footer's Delete: the detail's remove flow with the scope
// the editor was opened with — the remove question (Confirm tells the
// organiser or the attendees, Alternative removes only), else the delete
// question when ConfirmDelete asks, then event.delete. Success closes the
// window without the unsaved question; a refusal shows inline. done (may
// be nil) hears whether it was deleted.
func (c *EventEditorController) Delete(done func(deleted bool)) {
	finish := func(ok bool) {
		if done != nil {
			done(ok)
		}
	}
	if c.editing == nil || !c.editing.CanDelete || c.editing.EventID == "" || c.saving {
		finish(false)
		return
	}
	e := *c.editing
	tr := c.tr()
	run := func(notify *bool) {
		if c.closed {
			finish(false)
			return
		}
		var mail *api.MailText
		if notify != nil && *notify {
			kind := calendar.MailDeclined
			if e.RemoveIsDelete {
				kind = calendar.MailCancel
			}
			m := calendar.MailTexts(kind, e.Occurrence, c.src.Snapshot().MyName(e.AccountID), c.opts.Env)
			mail = &m
		}
		c.saving, c.errText, c.conflict = true, "", false
		c.changed()
		c.src.DeleteEvent(api.EventDeleteParams{EventID: e.EventID, RecurrenceID: e.RecurrenceID, Scope: c.scope,
			ETag: e.ETag, Notify: notify, Mail: mail}, func(_ *api.EventDeleteResult, err *api.Error) {
			c.saving = false
			if c.closed {
				finish(err == nil)
				return
			}
			if err != nil {
				c.setError(err, "")
				c.changed()
				finish(false)
				return
			}
			c.dirty = false
			c.changed()
			if c.OnClose != nil {
				c.OnClose()
			}
			finish(true)
		})
	}
	if q, ok := calendar.RemoveQuestion(e, tr); ok {
		c.askDelete(q, func(a Answer) {
			switch a {
			case AnswerConfirm:
				t := true
				run(&t)
			case AnswerAlternative:
				f := false
				run(&f)
			default:
				finish(false)
			}
		})
		return
	}
	if c.ConfirmDelete == nil || c.ConfirmDelete() {
		c.askDelete(calendar.DeleteQuestion(e, tr), func(a Answer) {
			if a != AnswerConfirm {
				finish(false)
				return
			}
			run(nil)
		})
		return
	}
	run(nil)
}

// Close is the window's close button: with changes asks Save / Discard /
// Cancel; done (may be nil) hears whether the window may close.
func (c *EventEditorController) Close(done func(mayClose bool)) {
	finish := func(ok bool) {
		if ok && c.OnClose != nil {
			c.OnClose()
		}
		if done != nil {
			done(ok)
		}
	}
	if !c.dirty {
		finish(true)
		return
	}
	if c.OnQuestion == nil {
		if done != nil {
			done(false)
		}
		return
	}
	c.OnQuestion(calendar.CloseQuestion(c.tr()), func(a Answer) {
		switch a {
		case AnswerAlternative:
			c.dirty = false
			finish(true)
		case AnswerConfirm:
			c.Save(func(ok bool) {
				if ok {
					finish(true)
				} else if done != nil {
					done(false)
				}
			})
		default:
			if done != nil {
				done(false)
			}
		}
	})
}

func (c *EventEditorController) setError(err *api.Error, what string) {
	if err == nil {
		err = &api.Error{Code: api.CodeInternalError, Message: "no answer"}
	}
	c.errText = c.opts.ErrorMessage(err, what)
	c.conflict = err.Code == api.CodeConflict
	if c.OnError != nil && c.errText != "" {
		c.OnError(c.errText)
	}
}

func (c *EventEditorController) changed() {
	if !c.closed && c.OnChange != nil {
		c.OnChange()
	}
}
