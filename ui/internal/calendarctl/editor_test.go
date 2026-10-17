// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendarctl

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/calendardata"
	"github.com/schotek/malachi/ui/internal/recipients"
)

// CalendarEventEditorControllerTests.swift over the in-memory samples:
// the defaults of a new event, the calendars, the validation, the
// attendees through recipients.Tokens, the primary label and the footer,
// the availability's debounce and its account rule, Next Free Slot,
// Attach, the save paths with their params, Delete, the close question,
// the conflict's reload and Find Another Time.

type edTr struct{}

func (edTr) T(s string) string { return s }
func (edTr) N(s, p string, n int) string {
	if n == 1 {
		return s
	}
	return p
}
func (edTr) C(_, s string) string { return s }

// edLoop holds what is posted until run; After records its delay.
type edLoop struct {
	queue  []func()
	delays []time.Duration
}

func (l *edLoop) Post(f func()) { l.queue = append(l.queue, f) }
func (l *edLoop) After(d time.Duration, f func()) {
	l.delays = append(l.delays, d)
	l.queue = append(l.queue, f)
}

func (l *edLoop) run() {
	for len(l.queue) > 0 {
		f := l.queue[0]
		l.queue = l.queue[1:]
		f()
	}
}

var (
	edZone = func() *time.Location {
		l, err := time.LoadLocation("Europe/Prague")
		if err != nil {
			panic(err)
		}
		return l
	}()
	edNow = time.Date(2026, 10, 14, 10, 17, 0, 0, edZone)
)

func edOptions(l *edLoop) Options {
	return Options{Env: calendar.Env{Tr: edTr{}, Format: calendar.EnglishStrftime, Loc: edZone},
		Now: func() time.Time { return edNow }, Loop: l}
}

func edSamples() (*calendardata.InMemorySource, *edLoop) {
	l := &edLoop{}
	s := calendardata.NewInMemorySource(calendar.SampleData(edNow, edZone), edZone, l)
	s.EmulatesWrites = true
	return s, l
}

func edNew(src *calendardata.InMemorySource, l *edLoop) *EventEditorController {
	d := calendar.NewEventDefaults(edNow, calendar.OriginNow, src.Snapshot().Preferences, edZone)
	e := NewEventEditor(src, edOptions(l), &d, "")
	e.Start()
	return e
}

func edEdit(src *calendardata.InMemorySource, l *edLoop, id string, scope api.EditScope) (*EventEditorController, api.Event) {
	ev := edEvent(src, id)
	e := EditEventEditor(src, edOptions(l), ev, scope, false)
	e.Start()
	return e, ev
}

func edEvent(src *calendardata.InMemorySource, id string) api.Event {
	for _, e := range src.Data().Events {
		if string(e.EventID) == id {
			return e
		}
	}
	panic("no event " + id)
}

func edAt(h, m int) time.Time { return time.Date(2026, 10, 14, h, m, 0, 0, edZone) }

func edLast(t *testing.T, src *calendardata.InMemorySource, method string) any {
	t.Helper()
	w := src.Writes()
	if len(w) == 0 || w[len(w)-1].Method != method {
		t.Fatalf("last write %v, want %s", w, method)
	}
	return w[len(w)-1].Params
}

func edPaste(s string) func(*recipients.Tokens) { return func(t *recipients.Tokens) { t.Paste(s) } }

func edSave(e *EventEditorController, l *edLoop) bool {
	got := false
	e.Save(func(ok bool) { got = ok })
	l.run()
	return got
}

func TestEditorNewEventDefaults(t *testing.T) {
	src, l := edSamples()
	e := edNew(src, l)
	if e.WindowTitle() != "New Event" {
		t.Error(e.WindowTitle())
	}
	in := e.Input()
	if !in.Start.Equal(edAt(10, 30)) || !in.End.Equal(edAt(11, 30)) {
		t.Errorf("times %v–%v", in.Start, in.End)
	}
	if e.Alert() != 15 || !slices.Equal(in.Alerts, []int{15}) {
		t.Errorf("alert %d %v", e.Alert(), in.Alerts)
	}
	if in.CalendarID != calendar.SampleWorkCalendar {
		t.Errorf("calendar %s", in.CalendarID)
	}
	var ids []api.CalendarID
	for _, c := range e.Calendars() {
		ids = append(ids, c.ID)
	}
	if !slices.Equal(ids, []api.CalendarID{calendar.SampleWorkCalendar, calendar.SamplePersonal, calendar.SampleFamily, calendar.SampleSport}) {
		t.Errorf("calendars %v", ids)
	}
	if e.Calendars()[0].Title != "Work · Calendar" {
		t.Errorf("title %q", e.Calendars()[0].Title)
	}
	if e.PrimaryLabel() != "Save" || len(e.Footer()) != 0 || e.HasChanges() || !e.SaveKeyAllowed() {
		t.Error("primary, footer or dirty")
	}
	// A preset calendar wins.
	p := NewEventEditor(src, edOptions(l), nil, calendar.SampleFamily)
	if p.Input().CalendarID != calendar.SampleFamily || !p.Input().Start.Equal(edAt(10, 30)) {
		t.Errorf("preset %v", p.Input())
	}
}

// D31.
func TestEditorAllDayRemapsTheAlert(t *testing.T) {
	src, l := edSamples()
	e := edNew(src, l)
	if !slices.Equal(e.AlertChoices(), calendar.AlertChoices) {
		t.Error("timed choices")
	}
	e.SetAllDay(true)
	if e.Alert() != calendar.AllDayAlertDefault || !slices.Equal(e.Input().Alerts, []int{calendar.AllDayAlertDefault}) {
		t.Errorf("on: %d", e.Alert())
	}
	if !slices.Equal(e.AlertChoices(), calendar.AllDayAlertChoices) ||
		e.AlertTitle(e.Alert()) != calendar.AlertTitleFor(calendar.AllDayAlertDefault, true, edTr{}) {
		t.Error("all-day choices or title")
	}
	e.SetAllDay(false)
	if e.Alert() != 15 || !slices.Equal(e.Input().Alerts, []int{15}) || !slices.Equal(e.AlertChoices(), calendar.AlertChoices) {
		t.Errorf("off: %d", e.Alert())
	}
	e.SetAlert(-1)
	e.SetAllDay(true)
	if e.Alert() != -1 || len(e.Input().Alerts) != 0 {
		t.Error("None on")
	}
	e.SetAllDay(false)
	if e.Alert() != -1 || len(e.Input().Alerts) != 0 {
		t.Error("None off")
	}
}

func TestEditorValidationBlocksSave(t *testing.T) {
	src, l := edSamples()
	e := edNew(src, l)
	e.SetEnd(e.Input().Start.Add(-time.Minute))
	if !e.HasChanges() {
		t.Error("not dirty")
	}
	if edSave(e, l) {
		t.Error("saved")
	}
	if !slices.Equal(e.Problems(), []calendar.Problem{calendar.ProblemEndBeforeStart}) || len(e.ProblemTexts()) != 1 {
		t.Errorf("problems %v", e.Problems())
	}
	if len(src.Writes()) != 0 {
		t.Error("wrote")
	}
	e.SetTitle("Lunch")
	if len(e.Problems()) != 0 {
		t.Error("a change kept the problems")
	}
}

func TestEditorStartMovesTheEnd(t *testing.T) {
	src, l := edSamples()
	e := edNew(src, l)
	e.SetStart(time.Date(2026, 10, 15, 14, 0, 0, 0, edZone))
	if !e.Input().End.Equal(time.Date(2026, 10, 15, 15, 0, 0, 0, edZone)) {
		t.Errorf("end %v", e.Input().End)
	}
	e.SetAllDay(true)
	if !e.Input().Start.Equal(time.Date(2026, 10, 15, 0, 0, 0, 0, edZone)) || !e.Input().End.Equal(time.Date(2026, 10, 16, 0, 0, 0, 0, edZone)) {
		t.Errorf("all day %v–%v", e.Input().Start, e.Input().End)
	}
	ny, _ := time.LoadLocation("America/New_York")
	e.SetZone(ny)
	if e.TimeZoneLink() != "Time zone: America/New_York" {
		t.Error(e.TimeZoneLink())
	}
	if h := e.Input().Start.In(ny); h.Day() != 15 || h.Hour() != 0 {
		t.Errorf("wall clock %v", h)
	}
}

func TestEditorAttendeesLabelFooterAndInvalid(t *testing.T) {
	src, l := edSamples()
	e := edNew(src, l)
	e.EditAttendees(edPaste("Jana <jana@example.org>, nonsense"))
	var addrs []string
	for _, a := range e.Input().Attendees {
		addrs = append(addrs, a.Address)
	}
	if !slices.Equal(addrs, []string{"jana@example.org"}) {
		t.Errorf("attendees %v", addrs)
	}
	if e.PrimaryLabel() != "Save and Send Invitations" || !e.SendsInvitations() {
		t.Error(e.PrimaryLabel())
	}
	if !slices.Equal(e.Footer(), []string{"The server sends the invitations."}) || e.SaveKeyAllowed() {
		t.Errorf("footer %v", e.Footer())
	}
	if edSave(e, l) || !slices.Contains(e.Problems(), calendar.ProblemInvalidAttendee) {
		t.Errorf("problems %v", e.Problems())
	}
	e.SetCalendar(calendar.SampleSport)
	if !slices.Equal(e.Footer(), []string{"Invitations leave by e-mail from account Gmail (me@example.com) through the Outbox."}) {
		t.Errorf("footer %v", e.Footer())
	}
}

func TestEditorAvailabilityIsDebouncedAndOnlyWithFreeBusy(t *testing.T) {
	src, l := edSamples()
	e := edNew(src, l)
	if e.ShowsAvailability() || e.AvailabilityPending() || len(l.queue) != 0 {
		t.Fatal("availability without attendees")
	}
	e.EditAttendees(edPaste("jana@example.org, petr@example.org,"))
	e.EditAttendees(edPaste("eva@example.org,"))
	if !e.ShowsAvailability() || !e.AvailabilityPending() {
		t.Fatal("no availability")
	}
	if len(l.delays) != 2 || l.delays[0] != 500*time.Millisecond || l.delays[1] != 500*time.Millisecond {
		t.Errorf("delays %v", l.delays)
	}
	l.run()
	if e.AvailabilityPending() {
		t.Error("still pending")
	}
	n := 0
	for _, r := range src.Reads() {
		if r == api.MethodCalendarFreeBusy {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d freeBusy reads, the first was not dropped", n)
	}
	var labels []string
	for _, r := range e.AvailabilityRows() {
		labels = append(labels, r.Label)
	}
	if !slices.Equal(labels, []string{"You", "jana@example.org", "petr@example.org", "eva@example.org"}) {
		t.Errorf("rows %v", labels)
	}
	for _, r := range e.AvailabilityRows()[1:] {
		if r.Known {
			t.Error("a known attendee")
		}
	}
	if len(e.AvailabilitySummary()) == 0 {
		t.Error("no summary")
	}
	// Gmail cannot answer for others: no request.
	e.SetCalendar(calendar.SamplePersonal)
	if e.ShowsAvailability() || e.AvailabilityPending() || len(e.AvailabilityRows()) != 0 || len(l.queue) != 0 {
		t.Error("availability on Gmail")
	}
}

func TestEditorNextFreeSlotMovesTheEvent(t *testing.T) {
	src, l := edSamples()
	slot := api.TimeSlot{Start: time.Date(2026, 10, 15, 13, 0, 0, 0, edZone), End: time.Date(2026, 10, 15, 14, 0, 0, 0, edZone)}
	src.FindFreeTimeResult = api.CalendarFindFreeTimeResult{Slots: []api.TimeSlot{slot}}
	e := edNew(src, l)
	e.NextFreeSlot()
	l.run()
	if !e.Input().Start.Equal(slot.Start) || !e.Input().End.Equal(slot.End) || !strings.HasPrefix(e.FreeSlotText(), "Next free slot: ") {
		t.Errorf("slot %v %q", e.Input().Start, e.FreeSlotText())
	}
	src.FindFreeTimeResult = api.CalendarFindFreeTimeResult{}
	none := edNew(src, l)
	none.NextFreeSlot()
	l.run()
	if none.FreeSlotText() != calendar.NoFreeSlot(edTr{}) {
		t.Error(none.FreeSlotText())
	}
}

// Find Another Time: Edit with findFreeSlot runs Next Free Slot at Start.
func TestEditorFindFreeSlotOnOpen(t *testing.T) {
	src, l := edSamples()
	slot := api.TimeSlot{Start: time.Date(2026, 10, 16, 13, 0, 0, 0, edZone), End: time.Date(2026, 10, 16, 14, 30, 0, 0, edZone)}
	src.FindFreeTimeResult = api.CalendarFindFreeTimeResult{Slots: []api.TimeSlot{slot}}
	e := EditEventEditor(src, edOptions(l), edEvent(src, "ev_sample_planning"), api.EditScopeAll, true)
	e.Start()
	l.run()
	if !e.Input().Start.Equal(slot.Start) || !e.HasChanges() {
		t.Errorf("start %v", e.Input().Start)
	}
}

func TestEditorSaveCreatesWithInvitations(t *testing.T) {
	src, l := edSamples()
	e := edNew(src, l)
	var saved []api.Event
	e.OnSaved = func(ev api.Event) { saved = append(saved, ev) }
	e.SetTitle("  Roadmap  ")
	e.SetLocation("Room D")
	e.SetRepeat(calendar.RepeatWeekly)
	e.SetAlert(-1)
	e.EditAttendees(edPaste("jana@example.org,"))
	e.Attach("/tmp/agenda.pdf")
	l.run()
	if len(e.Attachments()) != 1 || !slices.Contains(e.Footer(), "Attachment: agenda.pdf") {
		t.Fatalf("footer %v", e.Footer())
	}
	if !edSave(e, l) {
		t.Fatalf("not saved: %q %v", e.Error(), e.Problems())
	}
	p := edLast(t, src, api.MethodEventCreate).(api.EventCreateParams)
	if p.CalendarID != calendar.SampleWorkCalendar || p.SendInvitations == nil || !*p.SendInvitations {
		t.Error("calendar or invitations")
	}
	if p.Event.Title != "Roadmap" || p.Event.Location != "Room D" || len(p.Event.Alerts) != 0 || p.Event.Alerts == nil {
		t.Errorf("event %+v", p.Event)
	}
	if p.Event.Start != "2026-10-14T10:30:00" || p.Event.TimeZone != "Europe/Prague" {
		t.Errorf("start %s %s", p.Event.Start, p.Event.TimeZone)
	}
	if !reflect.DeepEqual(p.Event.Recurrence, &api.Recurrence{Frequency: api.FrequencyWeekly, Interval: 1, ByDay: []string{"WE"}}) {
		t.Errorf("rule %+v", p.Event.Recurrence)
	}
	if !slices.Equal(p.Event.Attachments, []string{e.Attachments()[0].ID}) {
		t.Error("attachments")
	}
	if p.Mail == nil || p.Mail.Subject != "Invitation: Roadmap" {
		t.Errorf("mail %+v", p.Mail)
	}
	if len(saved) != 1 || e.HasChanges() {
		t.Error("saved or dirty")
	}
	a := src.Writes()[0].Params.(api.AttachmentImportParams)
	if a.AccountID != calendar.SampleWork || a.Path != "/tmp/agenda.pdf" {
		t.Errorf("import %+v", a)
	}
}

func TestEditorEditUpdatesWithTheScope(t *testing.T) {
	src, l := edSamples()
	e, offsite := edEdit(src, l, "ev_sample_offsite", api.EditScopeAll)
	if e.WindowTitle() != "Edit Event" || !e.Input().AllDay || e.Input().Title != "Team offsite" {
		t.Error("edit state")
	}
	if cs := e.Calendars(); len(cs) != 1 || cs[0].ID != calendar.SampleWorkCalendar {
		t.Errorf("calendars %v", cs)
	}
	e.SetTitle("Team offsite 2026")
	if !edSave(e, l) {
		t.Fatal(e.Error())
	}
	p := edLast(t, src, api.MethodEventUpdate).(api.EventUpdateParams)
	if p.Scope != api.EditScopeAll || p.ETag != offsite.ETag || p.RecurrenceID != "" || p.Mail != nil {
		t.Errorf("update %+v", p)
	}
	if !p.Event.AllDay || p.Event.StartDate != offsite.StartDate || p.Event.EndDate != offsite.EndDate {
		t.Errorf("dates %s %s", p.Event.StartDate, p.Event.EndDate)
	}
	// One occurrence of the standup: no rule in the exception.
	one, standup := edEdit(src, l, "ev_sample_standup", api.EditScopeThis)
	if one.RepeatChoice() != calendar.RepeatCustom && one.RepeatChoice() != calendar.RepeatWeekly {
		t.Error("repeat")
	}
	one.SetTitle("Standup (moved)")
	if !edSave(one, l) {
		t.Fatal(one.Error())
	}
	q := edLast(t, src, api.MethodEventUpdate).(api.EventUpdateParams)
	if q.Scope != api.EditScopeThis || q.RecurrenceID != standup.RecurrenceID || q.Event.Recurrence != nil {
		t.Errorf("this %+v", q)
	}
}

// A conflict shows the Core text with Reload Event, which reopens the
// event as the daemon has it.
func TestEditorConflictReloads(t *testing.T) {
	src, l := edSamples()
	ev := edEvent(src, "ev_sample_offsite")
	ev.ETag = "stale"
	e := EditEventEditor(src, edOptions(l), ev, api.EditScopeAll, false)
	var errs []string
	e.OnError = func(s string) { errs = append(errs, s) }
	e.SetTitle("x")
	if edSave(e, l) {
		t.Fatal("saved over a stale etag")
	}
	want, _ := calendar.ErrorText(api.CodeConflict, edTr{})
	if !e.Conflict() || e.Error() != want || !slices.Equal(errs, []string{want}) || e.ReloadTitle() != "Reload Event" {
		t.Errorf("conflict %v %q", e.Conflict(), e.Error())
	}
	var fresh api.Event
	var scope api.EditScope
	e.OnReload = func(f api.Event, s api.EditScope) { fresh, scope = f, s }
	e.Reload()
	l.run()
	if fresh.ETag != `"1"` || scope != api.EditScopeAll || e.HasChanges() {
		t.Errorf("reload %q %s", fresh.ETag, scope)
	}
}

func TestEditorDeleteRunsTheDetailsFlow(t *testing.T) {
	src, l := edSamples()
	if _, ok := edNew(src, l).DeleteTitle(); ok {
		t.Error("New Event has Delete")
	}
	e, planning := edEdit(src, l, "ev_sample_planning", api.EditScopeAll)
	if title, ok := e.DeleteTitle(); !ok || title != calendar.RemoveTitle(planning, edTr{}) {
		t.Errorf("title %q", title)
	}
	answer := AnswerCancel
	var asked []calendar.Question
	saveQuestions, closed := 0, 0
	e.OnQuestion = func(calendar.Question, func(Answer)) { saveQuestions++ }
	e.OnDeleteQuestion = func(q calendar.Question, a func(Answer)) { asked = append(asked, q); a(answer) }
	e.OnClose = func() { closed++ }
	e.SetTitle("changed")
	got := true
	e.Delete(func(ok bool) { got = ok })
	l.run()
	if got || len(asked) == 0 || asked[0].Heading != "Also tell the organiser you won’t attend?" || len(src.Writes()) != 0 || closed != 0 {
		t.Fatalf("cancel: %v %v", got, asked)
	}
	answer = AnswerAlternative
	e.Delete(func(ok bool) { got = ok })
	l.run()
	p := edLast(t, src, api.MethodEventDelete).(api.EventDeleteParams)
	if !got || p.EventID != planning.EventID || p.Scope != api.EditScopeAll || p.Notify == nil || *p.Notify || p.Mail != nil {
		t.Errorf("remove only %+v", p)
	}
	if closed != 1 || saveQuestions != 0 || e.HasChanges() {
		t.Error("closed with the question")
	}

	src2, l2 := edSamples()
	d, _ := edEdit(src2, l2, "ev_sample_planning", api.EditScopeAll)
	d.OnDeleteQuestion = func(_ calendar.Question, a func(Answer)) { a(AnswerConfirm) }
	d.Delete(nil)
	l2.run()
	q := edLast(t, src2, api.MethodEventDelete).(api.EventDeleteParams)
	if q.Notify == nil || !*q.Notify || q.Mail == nil || q.Mail.Subject != "Declined: Q4 planning" {
		t.Errorf("decline %+v", q)
	}

	// The organiser's event: the delete question follows the preference.
	src3, l3 := edSamples()
	o, offsite := edEdit(src3, l3, "ev_sample_offsite", api.EditScopeAll)
	var headings []string
	o.OnDeleteQuestion = func(q calendar.Question, a func(Answer)) { headings = append(headings, q.Heading); a(AnswerCancel) }
	o.Delete(nil)
	l3.run()
	if !slices.Equal(headings, []string{"Remove “Team offsite”?"}) || len(src3.Writes()) != 0 {
		t.Errorf("headings %v", headings)
	}
	o.ConfirmDelete = func() bool { return false }
	o.Delete(func(ok bool) { got = ok })
	l3.run()
	r := edLast(t, src3, api.MethodEventDelete).(api.EventDeleteParams)
	if !got || r.Notify != nil || r.ETag != offsite.ETag || len(headings) != 1 {
		t.Errorf("unasked %+v", r)
	}
}

func TestEditorCloseAsksWithChanges(t *testing.T) {
	src, l := edSamples()
	e := edNew(src, l)
	closed := 0
	e.OnClose = func() { closed++ }
	may := false
	e.Close(func(ok bool) { may = ok })
	if !may || closed != 1 {
		t.Fatal("clean close")
	}
	e.SetTitle("Draft")
	answer := AnswerCancel
	var questions []calendar.Question
	e.OnQuestion = func(q calendar.Question, a func(Answer)) { questions = append(questions, q); a(answer) }
	e.Close(func(ok bool) { may = ok })
	if may || len(questions) != 1 || questions[0].Heading != "Save the Changes to This Event?" {
		t.Errorf("cancel %v", questions)
	}
	answer = AnswerAlternative
	e.Close(func(ok bool) { may = ok })
	if !may || closed != 2 || len(src.Writes()) != 0 {
		t.Error("discard")
	}
	s := edNew(src, l)
	s.SetTitle("Kept")
	s.OnQuestion = func(_ calendar.Question, a func(Answer)) { a(AnswerConfirm) }
	s.Close(func(ok bool) { may = ok })
	l.run()
	edLast(t, src, api.MethodEventCreate)
	if !may {
		t.Error("save and close")
	}
}

func TestEditorRefusedSaveStaysOpenWithTheError(t *testing.T) {
	src, l := edSamples()
	src.Failures[api.MethodEventCreate] = &api.Error{Code: api.CodeReadOnly, Message: "no"}
	e := edNew(src, l)
	e.SetTitle("X")
	if edSave(e, l) {
		t.Fatal("saved")
	}
	want, _ := calendar.ErrorText(api.CodeReadOnly, edTr{})
	if e.Error() != want || !e.HasChanges() || e.Conflict() || e.Saving() {
		t.Errorf("error %q", e.Error())
	}
}

func TestEditorVideoProvidersFollowTheCalendar(t *testing.T) {
	src, l := edSamples()
	e := edNew(src, l)
	if !slices.Equal(e.VideoProviders(), []string{"teamsForBusiness"}) {
		t.Errorf("providers %v", e.VideoProviders())
	}
	teams := &api.VideoCallInput{Mode: api.VideoCallCreate, Provider: "teamsForBusiness"}
	if !e.SetVideoCall(teams) || !e.HasChanges() || !reflect.DeepEqual(e.EventInput().VideoCall, teams) {
		t.Error("create")
	}
	if e.SetVideoCall(&api.VideoCallInput{Mode: api.VideoCallCreate, Provider: "zoom"}) {
		t.Error("zoom")
	}
	e.SetCalendar(calendar.SamplePersonal)
	if len(e.VideoProviders()) != 0 || e.VideoCall() != nil {
		t.Error("the meeting stayed on Gmail")
	}
}

func TestEditorVideoLinkMustBeHTTPS(t *testing.T) {
	src, l := edSamples()
	e := edNew(src, l)
	for _, u := range []string{"http://meet.example.org/x", "javascript:alert(1)", "https://"} {
		if e.SetVideoCall(&api.VideoCallInput{Mode: api.VideoCallLink, URL: u}) {
			t.Errorf("took %q", u)
		}
	}
	if e.VideoCall() != nil || e.HasChanges() {
		t.Error("changed")
	}
	if !e.SetVideoCall(&api.VideoCallInput{Mode: api.VideoCallLink, URL: " https://meet.example.org/abc "}) ||
		!reflect.DeepEqual(e.VideoCall(), &api.VideoCallInput{Mode: api.VideoCallLink, URL: "https://meet.example.org/abc"}) {
		t.Errorf("link %+v", e.VideoCall())
	}
	if !e.SetVideoCall(&api.VideoCallInput{Mode: api.VideoCallNone}) || e.VideoCall() != nil || e.EventInput().VideoCall != nil {
		t.Error("remove")
	}
}

func TestEditorEditStartsWithTheEventsLink(t *testing.T) {
	src, l := edSamples()
	e, _ := edEdit(src, l, "ev_sample_planning", api.EditScopeAll)
	want := &api.VideoCallInput{Mode: api.VideoCallLink, URL: "https://teams.microsoft.com/l/meetup-join/sample"}
	if !reflect.DeepEqual(e.VideoCall(), want) || !reflect.DeepEqual(e.EventInput().VideoCall, want) || e.HasChanges() {
		t.Errorf("link %+v", e.VideoCall())
	}
}

// A window closed: late answers are dropped.
func TestEditorStopDropsLateAnswers(t *testing.T) {
	src, l := edSamples()
	e := edNew(src, l)
	changes := 0
	e.OnChange = func() { changes++ }
	e.EditAttendees(edPaste("jana@example.org,"))
	e.Attach("/tmp/x.pdf")
	n := changes
	e.Stop()
	l.run()
	if changes != n || e.Availability() != nil || len(e.Attachments()) != 0 {
		t.Error("an answer after Stop")
	}
}
