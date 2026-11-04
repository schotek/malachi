// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendarctl

import (
	"encoding/json"
	"slices"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/calendardata"
)

// One event detail popover (docs/calendar.md §5.5), the port of
// EventDetailController.swift: loads the event (event.get through the
// source; the occurrence it was opened for shows until it arrives), its
// rows (calendar.DetailOf), and runs the flows of its buttons: the answer
// ("You: …", with the series question and Decline Without Reply), Remove
// from Calendar / Delete (calendar.RemoveQuestion, else the Mail
// preference confirm-delete and calendar.DeleteQuestion, then the series
// question), Edit (the series question, then the editor), Show Invitation
// in Mail, Join Video Call and the notes' links (hand-offs). Questions
// are continuations the popover presents (OnQuestion, OnScope); a write's
// error shows inline under the footer (Error), a conflict reloads the
// event and says so.

// Answer is the answer to a calendar.Question (Cal.Answer): Cancel is
// always there, Confirm is its Confirm button, Alternative its second one.
type Answer int

// The answers.
const (
	AnswerCancel Answer = iota
	AnswerConfirm
	AnswerAlternative
)

// EventDetailController is the controller of one event detail popover.
// Every method runs on the main loop.
type EventDetailController struct {
	source        calendardata.DataSource
	opts          Options
	eventID       api.EventID
	recurrenceID  api.DateOrTime
	confirmDelete func() bool

	// OnChange: anything shown changed.
	OnChange func()
	// OnQuestion asks a question (Cancel always there); nil answers
	// Cancel. answer runs once, on the main loop.
	OnQuestion func(q calendar.Question, answer func(Answer))
	// OnScope asks which occurrences of a series (calendar.ScopeQuestion);
	// nil, or an answer nil, is Cancel.
	OnScope func(q calendar.ScopeQuestion, answer func(*api.EditScope))
	// OnEdit: the editor for this event and scope.
	OnEdit func(e api.Event, scope api.EditScope)
	// OnShowInvitation: Mail mode, that message revealed.
	OnShowInvitation func(m api.MessageRef)
	// OnOpenLink: Join Video Call, a link of the notes; through the link
	// confirmation ("Open this link?").
	OnOpenLink func(url string)
	// OnClose: the event is gone (removed here); the popover closes.
	OnClose func()
	// OnFindAnotherTime: ✦ Find Another Time, the editor with Next Free
	// Slot.
	OnFindAnotherTime func(e api.Event)
	// OnAsk: ✦ Ask about This Event, the assistant panel.
	OnAsk func(e api.Event)

	occurrence        *api.Occurrence
	event             *api.Event
	loading, busy     bool
	err               string
	attendeesExpanded bool

	token      *board.ObserverToken
	generation int
	closed     bool
}

// NewEventDetailController is the popover of the event id (rec "" = not
// an instance); occurrence (may be nil) shows until the event arrives.
// confirmDelete reads the Mail preference "Ask before moving to the
// Trash" (settings confirm-delete); nil is always.
func NewEventDetailController(src calendardata.DataSource, opts Options, id api.EventID, rec api.DateOrTime,
	occurrence *api.Occurrence, confirmDelete func() bool) *EventDetailController {
	if confirmDelete == nil {
		confirmDelete = func() bool { return true }
	}
	d := &EventDetailController{source: src, opts: opts, eventID: id, recurrenceID: rec, confirmDelete: confirmDelete}
	if occurrence != nil {
		o := *occurrence
		d.occurrence = &o
	}
	return d
}

// NewEventDetailFor is the popover of an occurrence of a view; nil for one
// without an event (an invitation not in a calendar: its card is in Mail).
func NewEventDetailFor(src calendardata.DataSource, opts Options, o api.Occurrence, confirmDelete func() bool) *EventDetailController {
	if o.EventID == "" {
		return nil
	}
	return NewEventDetailController(src, opts, o.EventID, o.RecurrenceID, &o, confirmDelete)
}

// Start observes the source and loads the event.
func (d *EventDetailController) Start() {
	if d.token != nil || d.closed {
		return
	}
	d.token = d.source.Observe(d.sourceChanged)
	d.Load(false)
}

// Stop: the popover closed; nothing is taken any more and no callback
// runs.
func (d *EventDetailController) Stop() {
	d.closed = true
	if d.token != nil {
		d.token.Cancel()
		d.token = nil
	}
	d.generation++
}

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

// EventID is the event shown.
func (d *EventDetailController) EventID() api.EventID { return d.eventID }

// RecurrenceID is the instance shown ("" none).
func (d *EventDetailController) RecurrenceID() api.DateOrTime { return d.recurrenceID }

// Occurrence is the occurrence shown (the one opened until the event
// arrives); nil none.
func (d *EventDetailController) Occurrence() *api.Occurrence { return d.occurrence }

// Event is the loaded event; nil until it arrived.
func (d *EventDetailController) Event() *api.Event { return d.event }

// Loading: event.get is on its way.
func (d *EventDetailController) Loading() bool { return d.loading }

// Busy: a write is on its way; the buttons wait.
func (d *EventDetailController) Busy() bool { return d.busy }

// Error is the inline error under the footer ("" none).
func (d *EventDetailController) Error() string { return d.err }

// AttendeesExpanded: the list beyond calendar.MaxAttendeesShown is open.
func (d *EventDetailController) AttendeesExpanded() bool { return d.attendeesExpanded }

// Detail is the popover's rows; false until the event arrived.
func (d *EventDetailController) Detail() (calendar.Detail, bool) {
	if d.event == nil {
		return calendar.Detail{}, false
	}
	e := *d.event
	s := d.source.Snapshot()
	cal := ""
	if e.CalendarID != "" {
		cal = s.CalendarName(e.CalendarID)
	}
	return calendar.DetailOf(e, s.AccountName(e.AccountID), cal, d.opts.Env), true
}

// ShownAttendees are the attendee rows to show (collapsed beyond
// calendar.MaxAttendeesShown).
func (d *EventDetailController) ShownAttendees() []calendar.AttendeeRow {
	r, ok := d.Detail()
	if !ok {
		return nil
	}
	if r.Collapsed && !d.attendeesExpanded {
		return r.Attendees[:calendar.MaxAttendeesShown]
	}
	return r.Attendees
}

// ShowAllAttendees is "Show All %d Attendees" ("" when nothing is
// hidden).
func (d *EventDetailController) ShowAllAttendees() string {
	r, ok := d.Detail()
	if !ok || !r.Collapsed || d.attendeesExpanded {
		return ""
	}
	return calendar.ShowAllAttendees(len(r.Attendees), d.opts.Env.Tr)
}

// ResponseChoices are the "You: …" popup's choices: Accept, Tentative,
// Decline (Decline's menu adds Decline Without Reply).
func (d *EventDetailController) ResponseChoices() []api.EventResponse {
	return slices.Clone(calendar.ResponseChoices)
}

// ExpandAttendees opens the collapsed list.
func (d *EventDetailController) ExpandAttendees() {
	d.attendeesExpanded = true
	d.changed()
}

// ---------------------------------------------------------------------------
// Loading
// ---------------------------------------------------------------------------

// Load asks for the event (reload: the daemon anyway, not the cache).
func (d *EventDetailController) Load(reload bool) {
	if d.closed {
		return
	}
	d.generation++
	g := d.generation
	d.loading = true
	d.changed()
	d.source.Event(d.eventID, d.recurrenceID, reload, func(e *api.Event, err *api.Error) {
		if d.closed || g != d.generation {
			return
		}
		d.loading = false
		switch {
		case err == nil && e != nil:
			ev := *e
			d.event = &ev
			o := ev.Occurrence
			d.occurrence = &o
		case err != nil:
			d.err = d.opts.ErrorMessage(err, "")
		}
		d.changed()
	})
}

func (d *EventDetailController) sourceChanged(c calendardata.Change) {
	switch c.Kind {
	case calendardata.ChangeEvent:
		if c.Event == d.eventID {
			d.Load(false)
		}
	case calendardata.ChangeOccurrences:
		o := d.occurrence
		if o == nil && d.event != nil {
			o = &d.event.Occurrence
		}
		if o != nil {
			if s, e, ok := calendar.Span(*o, d.opts.Loc()); ok && !c.Overlaps(s, e) {
				return
			}
		}
		d.Load(false)
	}
}

// ---------------------------------------------------------------------------
// Respond
// ---------------------------------------------------------------------------

// Respond is the "You: …" popup: answers with r; with a series asks This
// Event / All Events first. withoutReply is Decline Without Reply
// (sendResponse false, no mail).
func (d *EventDetailController) Respond(r api.EventResponse, withoutReply bool) {
	if d.event == nil || !calendar.CanRespond(*d.event) || d.busy {
		return
	}
	e := *d.event
	d.scope(calendar.ScopeRespond, e, func(scope api.EditScope) {
		p := api.EventRespondParams{EventID: d.eventID, RecurrenceID: d.recOf(e), Scope: scope, ETag: e.ETag, Response: r}
		if withoutReply {
			f := false
			p.SendResponse = &f
		} else {
			m := calendar.MailTexts(calendar.MailKindOf(r), e.Occurrence, d.source.Snapshot().MyName(e.AccountID), d.opts.Env)
			p.Mail = &m
		}
		d.write(func(done func(*api.Error)) {
			d.source.Respond(p, func(res *api.EventRespondResult, err *api.Error) {
				if err == nil && res != nil && !d.closed {
					ev := res.Event
					d.event = &ev
				}
				done(err)
			})
		}, nil)
	})
}

// ---------------------------------------------------------------------------
// Remove
// ---------------------------------------------------------------------------

// Remove is Remove from Calendar / Delete, ⌫: the questions, then
// event.delete; the popover closes when it went through.
func (d *EventDetailController) Remove() {
	if d.event == nil || !d.event.CanDelete || d.busy {
		return
	}
	e := *d.event
	tr := d.opts.Env.Tr
	if q, ok := calendar.RemoveQuestion(e, tr); ok {
		d.ask(q, func(a Answer) {
			switch a {
			case AnswerConfirm:
				t := true
				d.runRemove(e, &t)
			case AnswerAlternative:
				f := false
				d.runRemove(e, &f)
			}
		})
		return
	}
	if d.confirmDelete() {
		d.ask(calendar.DeleteQuestion(e, tr), func(a Answer) {
			if a == AnswerConfirm {
				d.runRemove(e, nil)
			}
		})
		return
	}
	d.runRemove(e, nil)
}

// runRemove is the rest of Remove after its question: the scope, the
// texts of the mail when the organiser or the attendees are told, then
// event.delete.
func (d *EventDetailController) runRemove(e api.Event, notify *bool) {
	d.scope(calendar.ScopeDelete, e, func(scope api.EditScope) {
		p := api.EventDeleteParams{EventID: d.eventID, RecurrenceID: d.recOf(e), Scope: scope, ETag: e.ETag, Notify: notify}
		if notify != nil && *notify {
			kind := calendar.MailDeclined
			if e.RemoveIsDelete {
				kind = calendar.MailCancel
			}
			m := calendar.MailTexts(kind, e.Occurrence, d.source.Snapshot().MyName(e.AccountID), d.opts.Env)
			p.Mail = &m
		}
		d.write(func(done func(*api.Error)) {
			d.source.DeleteEvent(p, func(_ *api.EventDeleteResult, err *api.Error) { done(err) })
		}, func() {
			if d.OnClose != nil {
				d.OnClose()
			}
		})
	})
}

// ---------------------------------------------------------------------------
// Hand-offs
// ---------------------------------------------------------------------------

// Edit: with a series the scope first, then the editor (OnEdit).
func (d *EventDetailController) Edit() {
	if d.event == nil || !d.event.CanEdit || d.busy {
		return
	}
	e := *d.event
	d.scope(calendar.ScopeEdit, e, func(scope api.EditScope) {
		if d.OnEdit != nil {
			d.OnEdit(e, scope)
		}
	})
}

// ShowInvitation is Show Invitation in Mail: the first invitation message.
func (d *EventDetailController) ShowInvitation() {
	if d.event == nil || len(d.event.InvitationMessages) == 0 || d.OnShowInvitation == nil {
		return
	}
	d.OnShowInvitation(d.event.InvitationMessages[0])
}

// JoinVideoCall opens the call's link (through OnOpenLink).
func (d *EventDetailController) JoinVideoCall() {
	if d.event == nil || d.event.VideoCallLink == nil || d.event.VideoCallLink.URL == "" || d.OnOpenLink == nil {
		return
	}
	d.OnOpenLink(d.event.VideoCallLink.URL)
}

// OpenLink opens a link of the notes (Detail.Links); any other URL is
// ignored.
func (d *EventDetailController) OpenLink(url string) {
	if d.event == nil || !slices.Contains(d.event.Links, url) || d.OnOpenLink == nil {
		return
	}
	d.OnOpenLink(url)
}

// FindAnotherTime is ✦ Find Another Time.
func (d *EventDetailController) FindAnotherTime() {
	if d.event != nil && d.OnFindAnotherTime != nil {
		d.OnFindAnotherTime(*d.event)
	}
}

// AskAssistant is ✦ Ask about This Event.
func (d *EventDetailController) AskAssistant() {
	if d.event != nil && d.OnAsk != nil {
		d.OnAsk(*d.event)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// recOf is the instance a write names: the event's, else the one opened.
func (d *EventDetailController) recOf(e api.Event) api.DateOrTime {
	if e.RecurrenceID != "" {
		return e.RecurrenceID
	}
	return d.recurrenceID
}

// ask asks q; without OnQuestion the answer is Cancel. Nothing runs once
// the popover closed.
func (d *EventDetailController) ask(q calendar.Question, then func(Answer)) {
	if d.OnQuestion == nil {
		then(AnswerCancel)
		return
	}
	answered := false
	d.OnQuestion(q, func(a Answer) {
		if answered || d.closed {
			return
		}
		answered = true
		then(a)
	})
}

// scope is the scope of a write: all for a single event, else the series
// question's answer (a cancel runs nothing).
func (d *EventDetailController) scope(a calendar.ScopeAction, e api.Event, then func(api.EditScope)) {
	if !e.Recurring && e.Recurrence == nil {
		then(api.EditScopeAll)
		return
	}
	complex := e.Recurrence != nil && e.Recurrence.Complex
	q := calendar.ScopeQuestionFor(a, complex, d.opts.Env.Tr)
	if len(q.Choices) == 1 {
		then(q.Choices[0].Scope)
		return
	}
	if d.OnScope == nil {
		return
	}
	answered := false
	d.OnScope(q, func(s *api.EditScope) {
		if answered || d.closed || s == nil {
			return
		}
		answered = true
		then(*s)
	})
}

// write runs a write with the buttons waiting; its error goes inline (a
// conflict shows the event the error carries, else reloads it). ok runs
// after the buttons came back when it went through.
func (d *EventDetailController) write(body func(done func(*api.Error)), ok func()) {
	d.busy = true
	d.err = ""
	d.changed()
	body(func(err *api.Error) {
		d.busy = false
		if err != nil && !d.closed {
			d.err = d.opts.ErrorMessage(err, "")
			if err.Code == api.CodeConflict {
				if e, found := detailConflict(err); found {
					d.event = &e
					o := e.Occurrence
					d.occurrence = &o
				} else {
					d.Load(true)
				}
			}
		}
		d.changed()
		if err == nil && ok != nil && !d.closed {
			ok()
		}
	})
}

// detailConflict is the current event a conflict error carries
// (api.ConflictEventData, as decoded data or raw JSON); false without one.
func detailConflict(err *api.Error) (api.Event, bool) {
	if err == nil || err.Code != api.CodeConflict || err.Data == nil {
		return api.Event{}, false
	}
	switch v := err.Data.(type) {
	case api.ConflictEventData:
		return v.Event, v.Event.EventID != ""
	case *api.ConflictEventData:
		if v == nil {
			return api.Event{}, false
		}
		return v.Event, v.Event.EventID != ""
	}
	var raw []byte
	switch v := err.Data.(type) {
	case json.RawMessage:
		raw = v
	case []byte:
		raw = v
	default:
		b, e := json.Marshal(v)
		if e != nil {
			return api.Event{}, false
		}
		raw = b
	}
	var c api.ConflictEventData
	if json.Unmarshal(raw, &c) != nil || c.Event.EventID == "" {
		return api.Event{}, false
	}
	return c.Event, true
}

func (d *EventDetailController) changed() {
	if !d.closed && d.OnChange != nil {
		d.OnChange()
	}
}
