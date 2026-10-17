// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendarctl

import (
	"slices"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/calendardata"
)

// The event detail popover's controller over the in-memory samples, the
// port of CalendarEventDetailControllerTests.swift: its rows, the answer
// with and without the series question and Decline Without Reply, the
// remove questions of an attendee and an organiser and the confirm-delete
// preference, Edit's scope, the conflict reload and the hand-offs.

// dtEn is the identity translator.
type dtEn struct{}

func (dtEn) T(msgid string) string { return msgid }
func (dtEn) N(msgid, plural string, n int) string {
	if n == 1 {
		return msgid
	}
	return plural
}
func (dtEn) C(_, msgid string) string { return msgid }

// dtLoop holds what is posted until run.
type dtLoop struct{ queue []func() }

func (l *dtLoop) Post(f func())                   { l.queue = append(l.queue, f) }
func (l *dtLoop) After(_ time.Duration, f func()) { l.queue = append(l.queue, f) }

// run runs the queue until it is empty.
func (l *dtLoop) run() {
	for i := 0; len(l.queue) > 0 && i < 1000; i++ {
		f := l.queue[0]
		l.queue = l.queue[1:]
		f()
	}
}

// dtZone is Europe/Prague; dtNow Wednesday 14 October 2026, 10:17 there.
func dtZone(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Prague")
	if err != nil {
		t.Skipf("no Europe/Prague: %v", err)
	}
	return loc
}

func dtNow(loc *time.Location) time.Time { return time.Date(2026, 10, 14, 10, 17, 0, 0, loc) }

func dtOpts(loc *time.Location, l *dtLoop) Options {
	return Options{Env: calendar.Env{Tr: dtEn{}, Format: calendar.EnglishStrftime, Loc: loc}, Loop: l,
		Now: func() time.Time { return dtNow(loc) }}
}

// dtSamples is the samples with writes emulated (or not).
func dtSamples(t *testing.T, writes bool) (*calendardata.InMemorySource, *dtLoop, *time.Location) {
	loc := dtZone(t)
	l := &dtLoop{}
	s := calendardata.NewInMemorySource(calendar.SampleData(dtNow(loc), loc), loc, l)
	s.EmulatesWrites = writes
	return s, l, loc
}

// detail opens and loads the popover of id.
func detail(t *testing.T, s *calendardata.InMemorySource, l *dtLoop, loc *time.Location, id api.EventID,
	rec api.DateOrTime, confirm bool) *EventDetailController {
	t.Helper()
	d := NewEventDetailController(s, dtOpts(loc, l), id, rec, nil, func() bool { return confirm })
	d.Start()
	l.run()
	if d.Event() == nil && d.Error() == "" {
		t.Fatalf("%s did not load", id)
	}
	return d
}

// lastWrite is the last write of method's params.
func lastWrite[T any](t *testing.T, s *calendardata.InMemorySource, method string) T {
	t.Helper()
	w := s.Writes()
	for i := len(w) - 1; i >= 0; i-- {
		if w[i].Method == method {
			if p, ok := w[i].Params.(T); ok {
				return p
			}
		}
	}
	var zero T
	t.Fatalf("no %s", method)
	return zero
}

func sampleEvent(s *calendardata.InMemorySource, id api.EventID) api.Event {
	for _, e := range s.Data().Events {
		if e.EventID == id {
			return e
		}
	}
	return api.Event{}
}

func TestDetailRowsOfAnInvitedEvent(t *testing.T) {
	s, l, loc := dtSamples(t, true)
	d := detail(t, s, l, loc, "ev_sample_planning", "", true)
	r, ok := d.Detail()
	if !ok {
		t.Fatal("no detail")
	}
	if r.CalendarLine != "Work · Calendar" || r.Title != "Q4 planning" {
		t.Errorf("lines %q %q", r.CalendarLine, r.Title)
	}
	if !r.CanRespond || r.CanEdit || !r.ShowInvitation || r.RemoveTitle != "Remove from Calendar" {
		t.Errorf("actions %+v", r)
	}
	if r.VideoHost != "teams.microsoft.com" {
		t.Errorf("video host %q", r.VideoHost)
	}
	if len(d.ShownAttendees()) != 4 || d.ShowAllAttendees() != "" {
		t.Errorf("attendees %d %q", len(d.ShownAttendees()), d.ShowAllAttendees())
	}
	var links []string
	var shown []api.MessageRef
	d.OnOpenLink = func(u string) { links = append(links, u) }
	d.OnShowInvitation = func(m api.MessageRef) { shown = append(shown, m) }
	d.JoinVideoCall()
	d.OpenLink("https://example.org/q4-slides")
	d.OpenLink("https://evil.example/")
	d.ShowInvitation()
	if !slices.Equal(links, []string{"https://teams.microsoft.com/l/meetup-join/sample", "https://example.org/q4-slides"}) {
		t.Errorf("links %v", links)
	}
	if !slices.Equal(shown, []api.MessageRef{{AccountID: calendar.SampleWork, MessageID: "msg_sample_planning"}}) {
		t.Errorf("shown %v", shown)
	}
}

// The occurrence the popover was opened for shows until the event
// arrives.
func TestDetailShowsTheOccurrenceWhileLoading(t *testing.T) {
	s, l, loc := dtSamples(t, true)
	var o api.Occurrence
	for _, x := range s.Data().Occurrences {
		if x.EventID == "ev_sample_offsite" {
			o = x
		}
	}
	d := NewEventDetailFor(s, dtOpts(loc, l), o, nil)
	changes := 0
	d.OnChange = func() { changes++ }
	d.Start()
	if !d.Loading() || d.Occurrence() == nil || d.Occurrence().EventID != "ev_sample_offsite" {
		t.Fatal("the occurrence is not shown while loading")
	}
	if _, ok := d.Detail(); ok {
		t.Error("a detail before the event")
	}
	l.run()
	if d.Loading() || d.Event() == nil || changes < 2 {
		t.Errorf("loading %v, event %v, changes %d", d.Loading(), d.Event() != nil, changes)
	}
	d.Stop()
	if s.Observers() != 0 {
		t.Error("the observer stays after Stop")
	}
	if NewEventDetailFor(s, dtOpts(loc, l), api.Occurrence{Title: "invitation"}, nil) != nil {
		t.Error("a detail for an occurrence without an event")
	}
}

func TestDetailAnswerOfASingleEvent(t *testing.T) {
	s, l, loc := dtSamples(t, true)
	d := detail(t, s, l, loc, "ev_sample_planning", "", true)
	asked := 0
	d.OnScope = func(calendar.ScopeQuestion, func(*api.EditScope)) { asked++ }
	d.Respond(api.ResponseTentative, false)
	if !d.Busy() {
		t.Error("not busy while answering")
	}
	l.run()
	p := lastWrite[api.EventRespondParams](t, s, api.MethodEventRespond)
	if asked != 0 || p.Scope != api.EditScopeAll || p.Response != api.ResponseTentative || p.ETag != `"1"` || p.SendResponse != nil {
		t.Errorf("asked %d, params %+v", asked, p)
	}
	if p.Mail == nil || p.Mail.Subject != "Tentative: Q4 planning" {
		t.Errorf("mail %+v", p.Mail)
	}
	if d.Event().MyResponse != api.ResponseTentative || d.Busy() {
		t.Errorf("response %s busy %v", d.Event().MyResponse, d.Busy())
	}
	// Decline Without Reply.
	d.Respond(api.ResponseDeclined, true)
	l.run()
	q := lastWrite[api.EventRespondParams](t, s, api.MethodEventRespond)
	if q.SendResponse == nil || *q.SendResponse || q.Mail != nil || q.Response != api.ResponseDeclined {
		t.Errorf("without reply %+v", q)
	}
}

func TestDetailAnswerOfASeriesAsksTheScope(t *testing.T) {
	loc := dtZone(t)
	l := &dtLoop{}
	data := calendar.SampleData(dtNow(loc), loc)
	for i := range data.Events {
		if data.Events[i].EventID == "ev_sample_review" {
			data.Events[i].Recurring = true
			data.Events[i].Recurrence = &api.Recurrence{Frequency: api.FrequencyWeekly, Interval: 1, ByDay: []string{"TU"}}
			data.Events[i].RecurrenceID = api.DateOrTime(data.Events[i].Start.Format(time.RFC3339))
		}
	}
	s := calendardata.NewInMemorySource(data, loc, l)
	s.EmulatesWrites = true
	d := detail(t, s, l, loc, "ev_sample_review", "", true)
	var questions []calendar.ScopeQuestion
	d.OnScope = func(q calendar.ScopeQuestion, answer func(*api.EditScope)) {
		questions = append(questions, q)
		answer(nil)
	}
	d.Respond(api.ResponseAccepted, false)
	l.run()
	if len(questions) != 1 || len(questions[0].Choices) != 2 || questions[0].Choices[0].Scope != api.EditScopeThis ||
		questions[0].Choices[1].Scope != api.EditScopeAll {
		t.Fatalf("questions %+v", questions)
	}
	if len(s.Writes()) != 0 || d.Busy() {
		t.Error("a cancelled question wrote")
	}
	all := api.EditScopeAll
	d.OnScope = func(_ calendar.ScopeQuestion, answer func(*api.EditScope)) { answer(&all) }
	d.Respond(api.ResponseAccepted, false)
	l.run()
	if p := lastWrite[api.EventRespondParams](t, s, api.MethodEventRespond); p.Scope != api.EditScopeAll {
		t.Errorf("scope %s", p.Scope)
	}
}

func TestDetailAttendeeRemoveAsksAboutDeclining(t *testing.T) {
	s, l, loc := dtSamples(t, true)
	d := detail(t, s, l, loc, "ev_sample_planning", "", true)
	var questions []calendar.Question
	answer := AnswerCancel
	d.OnQuestion = func(q calendar.Question, a func(Answer)) {
		questions = append(questions, q)
		a(answer)
	}
	closed := 0
	d.OnClose = func() { closed++ }
	d.Remove()
	l.run()
	if len(questions) == 0 || questions[0].Heading != "Also tell the organiser you won’t attend?" {
		t.Fatalf("questions %+v", questions)
	}
	if len(s.Writes()) != 0 || closed != 0 {
		t.Error("a cancelled remove wrote")
	}
	answer = AnswerAlternative
	d.Remove()
	l.run()
	p := lastWrite[api.EventDeleteParams](t, s, api.MethodEventDelete)
	if p.Notify == nil || *p.Notify || p.Mail != nil || p.Scope != api.EditScopeAll {
		t.Errorf("remove only %+v", p)
	}
	if closed != 1 {
		t.Errorf("closed %d", closed)
	}
}

func TestDetailAttendeeRemoveAndDecline(t *testing.T) {
	s, l, loc := dtSamples(t, true)
	d := detail(t, s, l, loc, "ev_sample_planning", "", true)
	d.OnQuestion = func(_ calendar.Question, a func(Answer)) { a(AnswerConfirm) }
	d.Remove()
	l.run()
	p := lastWrite[api.EventDeleteParams](t, s, api.MethodEventDelete)
	if p.Notify == nil || !*p.Notify || p.Mail == nil || p.Mail.Subject != "Declined: Q4 planning" {
		t.Errorf("remove and decline %+v", p)
	}
}

func TestDetailOrganiserDeleteFollowsConfirmDelete(t *testing.T) {
	s, l, loc := dtSamples(t, true)
	d := detail(t, s, l, loc, "ev_sample_offsite", "", true)
	var headings []string
	d.OnQuestion = func(q calendar.Question, a func(Answer)) {
		headings = append(headings, q.Heading)
		a(AnswerConfirm)
	}
	d.Remove()
	l.run()
	if !slices.Equal(headings, []string{"Remove “Team offsite”?"}) {
		t.Errorf("headings %q", headings)
	}
	if p := lastWrite[api.EventDeleteParams](t, s, api.MethodEventDelete); p.Notify != nil {
		t.Errorf("notify %v", *p.Notify)
	}
	// Without the preference nothing is asked.
	s2, l2, _ := dtSamples(t, true)
	quiet := detail(t, s2, l2, loc, "ev_sample_offsite", "", false)
	asked := 0
	quiet.OnQuestion = func(_ calendar.Question, a func(Answer)) { asked++; a(AnswerCancel) }
	quiet.Remove()
	l2.run()
	if asked != 0 || len(s2.Writes()) != 1 {
		t.Errorf("asked %d, writes %d", asked, len(s2.Writes()))
	}
}

func TestDetailSeriesDeleteAsksTheScope(t *testing.T) {
	s, l, loc := dtSamples(t, true)
	rid := sampleEvent(s, "ev_sample_standup").RecurrenceID
	d := detail(t, s, l, loc, "ev_sample_standup", rid, false)
	var scopes []calendar.ScopeQuestion
	this := api.EditScopeThis
	d.OnScope = func(q calendar.ScopeQuestion, a func(*api.EditScope)) { scopes = append(scopes, q); a(&this) }
	d.Remove()
	l.run()
	if len(scopes) != 1 || len(scopes[0].Choices) != 3 || scopes[0].Choices[1].Scope != api.EditScopeThisAndFuture {
		t.Fatalf("scopes %+v", scopes)
	}
	p := lastWrite[api.EventDeleteParams](t, s, api.MethodEventDelete)
	if p.Scope != api.EditScopeThis || p.RecurrenceID != rid || rid == "" {
		t.Errorf("delete %+v (rid %q)", p, rid)
	}
}

func TestDetailEditHandsTheScopeOver(t *testing.T) {
	s, l, loc := dtSamples(t, true)
	rid := sampleEvent(s, "ev_sample_standup").RecurrenceID
	d := detail(t, s, l, loc, "ev_sample_standup", rid, true)
	var edits []api.EditScope
	future := api.EditScopeThisAndFuture
	d.OnScope = func(_ calendar.ScopeQuestion, a func(*api.EditScope)) { a(&future) }
	d.OnEdit = func(_ api.Event, sc api.EditScope) { edits = append(edits, sc) }
	d.Edit()
	if !slices.Equal(edits, []api.EditScope{api.EditScopeThisAndFuture}) {
		t.Fatalf("edits %v", edits)
	}
	// A single event goes to the editor without a question.
	single := detail(t, s, l, loc, "ev_sample_offsite", "", true)
	asked := 0
	single.OnScope = func(calendar.ScopeQuestion, func(*api.EditScope)) { asked++ }
	single.OnEdit = func(_ api.Event, sc api.EditScope) { edits = append(edits, sc) }
	single.Edit()
	if len(edits) != 2 || edits[1] != api.EditScopeAll || asked != 0 {
		t.Errorf("edits %v asked %d", edits, asked)
	}
	// The hand-offs of ✦.
	var found, askedAbout []api.EventID
	single.OnFindAnotherTime = func(e api.Event) { found = append(found, e.EventID) }
	single.OnAsk = func(e api.Event) { askedAbout = append(askedAbout, e.EventID) }
	single.FindAnotherTime()
	single.AskAssistant()
	if len(found) != 1 || len(askedAbout) != 1 || found[0] != "ev_sample_offsite" {
		t.Errorf("found %v asked %v", found, askedAbout)
	}
}

func TestDetailConflictReloadsTheEvent(t *testing.T) {
	s, l, loc := dtSamples(t, true)
	newer := sampleEvent(s, "ev_sample_planning")
	newer.ETag = `"9"`
	newer.Title = "Q4 planning (moved)"
	s.Failures[api.MethodEventRespond] = &api.Error{Code: api.CodeConflict, Message: "etag",
		Data: api.ConflictEventData{Event: newer}}
	d := detail(t, s, l, loc, "ev_sample_planning", "", true)
	d.Respond(api.ResponseAccepted, false)
	l.run()
	want, _ := calendar.ErrorText(api.CodeConflict, dtEn{})
	if d.Error() != want {
		t.Errorf("error %q", d.Error())
	}
	r, _ := d.Detail()
	if d.Event().ETag != `"9"` || r.Title != "Q4 planning (moved)" {
		t.Errorf("event %q %q", d.Event().ETag, r.Title)
	}
	// Without the event in the error the event is asked for anew.
	s.Failures[api.MethodEventRespond] = &api.Error{Code: api.CodeConflict, Message: "etag"}
	reads := len(s.Reads())
	d.Respond(api.ResponseAccepted, false)
	l.run()
	if len(s.Reads()) != reads+1 || d.Error() != want || d.Loading() {
		t.Errorf("reads %d → %d, error %q", reads, len(s.Reads()), d.Error())
	}
	// A preview write says so inline.
	p, pl, _ := dtSamples(t, false)
	pd := detail(t, p, pl, loc, "ev_sample_planning", "", true)
	pd.Respond(api.ResponseAccepted, false)
	pl.run()
	if pd.Error() != calendar.PreviewOnly(dtEn{}) || pd.Busy() {
		t.Errorf("preview %q", pd.Error())
	}
}

func TestDetailUnknownEventSaysSo(t *testing.T) {
	s, l, loc := dtSamples(t, true)
	d := detail(t, s, l, loc, "ev_nothing", "", true)
	want, _ := calendar.ErrorText(api.CodeEventNotFound, dtEn{})
	if d.Event() != nil || d.Error() != want {
		t.Errorf("event %v, error %q", d.Event(), d.Error())
	}
}

// A change of the event reloads it; a change of occurrences elsewhere
// does not; nothing runs after Stop.
func TestDetailFollowsTheSource(t *testing.T) {
	s, l, loc := dtSamples(t, true)
	d := detail(t, s, l, loc, "ev_sample_planning", "", true)
	reads := len(s.Reads())
	far := time.Date(2030, 1, 1, 0, 0, 0, 0, loc)
	farEnd := far.AddDate(0, 0, 1)
	s.Handle(api.NotifyCalendarChanged, []byte(`{"from":"`+far.UTC().Format(time.RFC3339)+`","to":"`+farEnd.UTC().Format(time.RFC3339)+`"}`))
	l.run()
	if len(s.Reads()) != reads {
		t.Error("a change elsewhere reloaded the event")
	}
	s.Handle(api.NotifyCalendarChanged, []byte(`{}`))
	l.run()
	if len(s.Reads()) != reads+1 {
		t.Error("an open change did not reload the event")
	}
	changes := 0
	d.OnChange = func() { changes++ }
	d.Stop()
	s.Handle(api.NotifyCalendarChanged, []byte(`{}`))
	l.run()
	d.ExpandAttendees()
	if changes != 0 {
		t.Errorf("%d changes after Stop", changes)
	}
}
