// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendardata

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/calendar"
)

// InMemoryCalendarSource.swift and CalendarSamples.swift: the cases the
// Swift controllers' tests rely on (samplesAnswerThePreview, the
// emulated writes, the notifications).

func source(writes bool) (*InMemorySource, *manualLoop) {
	l := &manualLoop{}
	m := NewSampleSource(calNow, prague, l)
	m.EmulatesWrites = writes
	return m, l
}

func week() Range {
	return RangeIn(calendar.ViewRange(calendar.ViewWeek, calNow, time.Monday, prague), prague)
}

func record(m *InMemorySource) *[]Change {
	var got []Change
	m.Observe(func(c Change) { got = append(got, c) })
	return &got
}

func TestSampleSourceIsReady(t *testing.T) {
	m, _ := source(false)
	if m.Snapshot().Phase != PhaseReady || len(m.Data().Occurrences) == 0 {
		t.Fatal("the sample source is not ready")
	}
	if NewEmptySource(nil, nil).Snapshot().Phase != PhaseReady {
		t.Error("the empty source is not ready")
	}
}

// Answers wait for the loop.
func TestAnswersComeThroughTheLoop(t *testing.T) {
	m, l := source(false)
	var got []api.Occurrence
	called := false
	m.Occurrences(week(), func(o []api.Occurrence, err *api.Error) { got, called = o, err == nil })
	if called {
		t.Fatal("answered before the loop ran")
	}
	l.run()
	if !called || len(got) != len(m.Data().Occurrences) {
		t.Fatalf("occurrences = %d of %d", len(got), len(m.Data().Occurrences))
	}
	if _, ok := m.CachedOccurrences(week()); ok {
		t.Error("the in-memory source caches")
	}
	if !slices.Equal(m.Reads(), []string{api.MethodEventList}) {
		t.Errorf("reads %v", m.Reads())
	}
}

// samplesAnswerThePreview: writes answer the preview's error, the
// checkboxes work.
func TestSamplesAnswerThePreview(t *testing.T) {
	m, l := source(false)
	var errs []*api.Error
	m.Sync(api.CalendarSyncParams{}, func(e *api.Error) { errs = append(errs, e) })
	m.CreateEvent(api.EventCreateParams{CalendarID: calendar.SamplePersonal}, func(r *api.EventCreateResult, e *api.Error) {
		if r != nil {
			t.Error("a preview create has a result")
		}
		errs = append(errs, e)
	})
	m.SetColor(calendar.SampleSport, "#000000", func(_ *api.Calendar, e *api.Error) { errs = append(errs, e) })
	m.UpdateAccount(api.AccountUpdateParams{}, nil) // a nil done is fine
	l.run()
	if len(errs) != 3 {
		t.Fatalf("answers %d", len(errs))
	}
	for _, e := range errs {
		if !IsPreview(e) {
			t.Errorf("answer %v", e)
		}
	}
	if len(m.Writes()) != 0 {
		t.Errorf("preview writes recorded: %v", m.Writes())
	}
	got := record(m)
	m.SetVisible(calendar.SampleFamily, false)
	if c, _ := m.Snapshot().Calendar(calendar.SampleFamily); c.Visible {
		t.Error("the checkbox does not work")
	}
	if len(*got) != 2 || (*got)[0].Kind != ChangeSnapshot || (*got)[1].Kind != ChangeOccurrences {
		t.Errorf("changes %+v", *got)
	}
	var occ []api.Occurrence
	m.Occurrences(week(), func(o []api.Occurrence, _ *api.Error) { occ = o })
	l.run()
	for _, o := range occ {
		if o.CalendarID == calendar.SampleFamily {
			t.Error("a hidden calendar's occurrence is listed")
		}
	}
	// The same visibility again changes nothing.
	*got = nil
	m.SetVisible(calendar.SampleFamily, false)
	m.SetVisible("unknown", true)
	if len(*got) != 0 {
		t.Errorf("no-op visibility told %v", *got)
	}
}

func TestVisibilityRefused(t *testing.T) {
	m, _ := source(true)
	m.Failures[api.MethodCalendarUpdate] = &api.Error{Code: api.CodeReadOnly}
	got := record(m)
	m.SetVisible(calendar.SampleSport, false)
	if c, _ := m.Snapshot().Calendar(calendar.SampleSport); !c.Visible {
		t.Error("a refused visibility changed")
	}
	if len(*got) != 1 || (*got)[0].Kind != ChangeError || (*got)[0].Err.Code != api.CodeReadOnly {
		t.Errorf("changes %+v", *got)
	}
	if len(m.Writes()) != 1 || m.Writes()[0].Method != api.MethodCalendarUpdate {
		t.Errorf("writes %v", m.Writes())
	}
}

func TestReads(t *testing.T) {
	m, l := source(false)
	var ev *api.Event
	var gone *api.Error
	m.Event("ev_sample_planning", "", false, func(e *api.Event, _ *api.Error) { ev = e })
	m.Event("ev_none", "", true, func(_ *api.Event, e *api.Error) { gone = e })
	var standup *api.Event
	rec := m.Data().Events[2].RecurrenceID
	m.Event("ev_sample_standup", rec, false, func(e *api.Event, _ *api.Error) { standup = e })
	var found *api.EventSearchResult
	m.Search(api.EventSearchParams{Query: "STAND"}, func(r *api.EventSearchResult, _ *api.Error) { found = r })
	var room *api.EventSearchResult
	m.Search(api.EventSearchParams{Query: "boulder"}, func(r *api.EventSearchResult, _ *api.Error) { room = r })
	var fb *api.CalendarFreeBusyResult
	from, to := week().From, week().To
	m.FreeBusy(api.CalendarFreeBusyParams{AccountID: calendar.SampleWork, Attendees: []string{"x@example.org"}, From: from, To: to},
		func(r *api.CalendarFreeBusyResult, _ *api.Error) { fb = r })
	var slots *api.CalendarFindFreeTimeResult
	m.FindFreeTime(api.CalendarFindFreeTimeParams{}, func(r *api.CalendarFindFreeTimeResult, _ *api.Error) { slots = r })
	l.run()
	if ev == nil || ev.Title != "Q4 planning" {
		t.Errorf("event %+v", ev)
	}
	if gone == nil || gone.Code != api.CodeEventNotFound {
		t.Errorf("unknown event %v", gone)
	}
	if standup == nil || standup.RecurrenceID != rec {
		t.Errorf("instance %+v", standup)
	}
	if found == nil || found.Total != 1 || found.Occurrences[0].Title != "Standup" {
		t.Errorf("search %+v", found)
	}
	if room == nil || room.Total != 1 {
		t.Errorf("search of the place %+v", room)
	}
	if fb == nil || fb.Me.Address != "me@example.org" || !fb.Me.Known || len(fb.Attendees) != 1 || fb.Attendees[0].Known {
		t.Fatalf("free/busy %+v", fb)
	}
	for _, b := range fb.Me.Busy {
		if b.ShowAs == api.ShowAsFree {
			t.Error("a free event is busy")
		}
	}
	if len(fb.Me.Busy) == 0 {
		t.Error("nothing busy")
	}
	if slots == nil || slots.Slots == nil {
		t.Errorf("find free time %+v", slots)
	}
	m.Failures[api.MethodEventGet] = &api.Error{Code: api.CodeOffline}
	var off *api.Error
	m.Event("ev_sample_planning", "", false, func(_ *api.Event, e *api.Error) { off = e })
	l.run()
	if off == nil || off.Code != api.CodeOffline {
		t.Errorf("failure %v", off)
	}
}

// The emulated writes change the data, tell the observers before done and
// record themselves.
func TestEmulatedWrites(t *testing.T) {
	m, l := source(true)
	got := record(m)
	var order []string
	m.Observe(func(Change) { order = append(order, "change") })

	var created *api.EventCreateResult
	m.CreateEvent(api.EventCreateParams{CalendarID: calendar.SamplePersonal, Event: api.EventInput{Title: "Lunch",
		Start: "2026-10-15T12:00:00", End: "2026-10-15T13:00", TimeZone: "Europe/Prague",
		Attendees: []api.EventInputAttendee{{Address: "a@example.org"}}}},
		func(r *api.EventCreateResult, _ *api.Error) { created, order = r, append(order, "done") })
	l.run()
	if created == nil || created.Event.EventID != "ev_created_1" || created.Event.AccountID != calendar.SampleGmail ||
		created.Invitations.Via != api.InvitationsViaServer || created.Invitations.Count != 1 {
		t.Fatalf("created %+v", created)
	}
	if s := created.Event.Start; s == nil || !s.Equal(time.Date(2026, 10, 15, 12, 0, 0, 0, prague)) {
		t.Errorf("start %v", s)
	}
	if !slices.Equal(order, []string{"change", "done"}) {
		t.Errorf("order %v", order)
	}

	// Update: a stale etag is conflict, the right one a new etag.
	var conflict *api.Error
	m.UpdateEvent(api.EventUpdateParams{EventID: "ev_sample_supplier", ETag: "old"}, func(_ *api.EventUpdateResult, e *api.Error) { conflict = e })
	var updated *api.EventUpdateResult
	m.UpdateEvent(api.EventUpdateParams{EventID: "ev_sample_supplier", ETag: "\"1\"", Event: api.EventInput{Title: "Call", Location: "Room"}},
		func(r *api.EventUpdateResult, _ *api.Error) { updated = r })
	l.run()
	if conflict == nil || conflict.Code != api.CodeConflict {
		t.Errorf("stale etag %v", conflict)
	}
	if updated == nil || updated.Event.Title != "Call" || updated.Event.ETag != "\"1\"+" {
		t.Errorf("updated %+v", updated)
	}

	// Respond sets the answer on the event and its occurrences.
	var resp *api.EventRespondResult
	no := false
	m.Respond(api.EventRespondParams{EventID: "ev_sample_budget", Response: api.ResponseAccepted, SendResponse: &no},
		func(r *api.EventRespondResult, _ *api.Error) { resp = r })
	l.run()
	if resp == nil || resp.Event.MyResponse != api.ResponseAccepted || resp.Via != api.InvitationsViaNone {
		t.Errorf("respond %+v", resp)
	}
	for _, o := range m.Data().Occurrences {
		if o.EventID == "ev_sample_budget" && o.MyResponse != api.ResponseAccepted {
			t.Error("the occurrence keeps the old answer")
		}
	}

	// Delete of one instance leaves the others.
	rec := m.Data().Events[1].RecurrenceID
	m.DeleteEvent(api.EventDeleteParams{EventID: "ev_sample_standup", RecurrenceID: rec, Scope: api.EditScopeThis}, nil)
	l.run()
	n := 0
	for _, o := range m.Data().Occurrences {
		if o.EventID == "ev_sample_standup" {
			n++
			if o.RecurrenceID == rec {
				t.Error("the deleted instance stays")
			}
		}
	}
	if n != 4 {
		t.Errorf("standups left %d", n)
	}
	m.DeleteEvent(api.EventDeleteParams{EventID: "ev_sample_standup", Scope: api.EditScopeAll}, nil)
	l.run()
	for _, o := range m.Data().Occurrences {
		if o.EventID == "ev_sample_standup" {
			t.Error("the series stays")
		}
	}

	// Preferences, color, account, attachment.
	seven := 7
	var prefs *api.CalendarPreferences
	m.SetPreferences(api.CalendarPreferencesUpdate{DefaultAlertMinutes: &seven}, func(p *api.CalendarPreferences, _ *api.Error) { prefs = p })
	var cal *api.Calendar
	m.SetColor(calendar.SampleSport, "#123456", func(c *api.Calendar, _ *api.Error) { cal = c })
	cfg := m.Snapshot().Accounts[2].Config
	cfg.Name = "Cloud"
	m.UpdateAccount(api.AccountUpdateParams{AccountID: calendar.SampleNextcloud, Config: cfg}, nil)
	var att *api.AttachmentImportResult
	m.ImportAttachment(api.AttachmentImportParams{Path: "/tmp/agenda.pdf"}, func(r *api.AttachmentImportResult, _ *api.Error) { att = r })
	l.run()
	if prefs == nil || prefs.DefaultAlertMinutes != 7 || m.Snapshot().Preferences.DefaultAlertMinutes != 7 {
		t.Errorf("preferences %+v", prefs)
	}
	if c, _ := m.Snapshot().Calendar(calendar.SampleSport); cal == nil || c.Color != "#123456" || c.UserColor != "#123456" {
		t.Errorf("color %+v", c)
	}
	if m.Snapshot().AccountName(calendar.SampleNextcloud) != "Cloud" {
		t.Error("account.update did not reach the snapshot")
	}
	if att == nil || att.Attachment.Filename != "agenda.pdf" {
		t.Errorf("attachment %+v", att)
	}

	// Invitations and CalDAV.
	var card *api.InvitationRespondResult
	m.RespondToInvitation(api.InvitationRespondParams{Response: api.ResponseTentative, CalendarID: calendar.SampleFamily},
		func(r *api.InvitationRespondResult, _ *api.Error) { card = r })
	var cancel *api.InvitationApplyCancelResult
	m.ApplyCancel(api.InvitationApplyCancelParams{}, func(r *api.InvitationApplyCancelResult, _ *api.Error) { cancel = r })
	m.DiscoverResult.ServerURL = "https://dav.example.com/"
	var disc *api.AccountDiscoverCalDAVResult
	m.DiscoverCalDAV(api.AccountDiscoverCalDAVParams{URL: "dav.example.com"}, func(r *api.AccountDiscoverCalDAVResult, _ *api.Error) { disc = r })
	var list *api.AccountListCalDAVCalendarsResult
	m.ListCalDAVCalendars(api.AccountListCalDAVCalendarsParams{}, func(r *api.AccountListCalDAVCalendarsResult, _ *api.Error) { list = r })
	var added *api.AccountAddResult
	m.AddAccount(api.AccountAddParams{}, func(r *api.AccountAddResult, _ *api.Error) { added = r })
	var snoozed, dismissed bool
	m.SnoozeAlert(api.EventSnoozeAlertParams{AlertID: "a", Minutes: 5}, func(e *api.Error) { snoozed = e == nil })
	m.DismissAlert(api.EventDismissAlertParams{AlertID: "a"}, func(e *api.Error) { dismissed = e == nil })
	l.run()
	if card == nil || card.Invitation.State != api.InvitationAnswered || card.Invitation.Response != api.ResponseTentative ||
		card.Invitation.CalendarID != calendar.SampleFamily {
		t.Errorf("invitation %+v", card)
	}
	if cancel == nil || !cancel.Removed || disc == nil || disc.ServerURL != "https://dav.example.com/" || list == nil ||
		added == nil || added.AccountID != "acc_added" || !snoozed || !dismissed {
		t.Error("an emulated answer is missing")
	}

	var methods []string
	for _, w := range m.Writes() {
		methods = append(methods, w.Method)
	}
	want := []string{api.MethodEventCreate, api.MethodEventUpdate, api.MethodEventUpdate, api.MethodEventRespond,
		api.MethodEventDelete, api.MethodEventDelete, api.MethodCalendarSetPreferences, api.MethodCalendarUpdate,
		api.MethodAccountUpdate, api.MethodAttachmentImport, api.MethodInvitationRespond, api.MethodInvitationApplyCancel,
		api.MethodAccountDiscoverCalDAV, api.MethodAccountListCalDAVCalendars, api.MethodAccountAdd,
		api.MethodEventSnoozeAlert, api.MethodEventDismissAlert}
	if !slices.Equal(methods, want) {
		t.Errorf("writes %v", methods)
	}
	if len(*got) == 0 {
		t.Error("no changes")
	}
}

func TestEmulatedFailure(t *testing.T) {
	m, l := source(true)
	m.Failures[api.MethodEventRespond] = &api.Error{Code: api.CodeOffline}
	var e *api.Error
	var r *api.EventRespondResult
	m.Respond(api.EventRespondParams{EventID: "ev_sample_budget", Response: api.ResponseDeclined}, func(res *api.EventRespondResult, err *api.Error) { r, e = res, err })
	l.run()
	if r != nil || e == nil || e.Code != api.CodeOffline {
		t.Errorf("failure %v %v", r, e)
	}
	for _, o := range m.Data().Occurrences {
		if o.EventID == "ev_sample_budget" && o.MyResponse != api.ResponseNeedsAction {
			t.Error("a failed write changed the data")
		}
	}
}

func TestHandle(t *testing.T) {
	m, _ := source(false)
	got := record(m)
	raw := func(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
	from := calNow
	if !m.Handle(api.NotifyCalendarChanged, raw(api.CalendarChangedNotification{From: &from})) {
		t.Fatal("calendarChanged not handled")
	}
	state := api.CalendarSyncState{AccountID: calendar.SampleWork, Status: api.CalendarSyncConsentRequired, Progress: -1}
	m.Handle(api.NotifyCalendarState, raw(api.CalendarStateNotification{State: state}))
	m.Handle(api.NotifyEventAlert, raw(api.EventAlertNotification{Alert: api.EventAlert{AlertID: "al", Title: "Standup"}}))
	if !m.Handle(api.NotifyInvitationsChanged, raw(api.InvitationsChangedNotification{})) {
		t.Error("invitationsChanged not handled")
	}
	if !m.Handle(api.NotifyEventAlert, json.RawMessage(`{"alert":`)) {
		t.Error("a malformed calendar notification is not the calendar's")
	}
	if m.Handle(api.NotifyAccountsChanged, nil) {
		t.Error("accountsChanged handled")
	}
	kinds := []ChangeKind{}
	for _, c := range *got {
		kinds = append(kinds, c.Kind)
	}
	if !slices.Equal(kinds, []ChangeKind{ChangeOccurrences, ChangeSnapshot, ChangeAlert}) {
		t.Fatalf("changes %v", kinds)
	}
	if c := (*got)[0]; c.From == nil || !c.From.Equal(from) || c.To != nil {
		t.Errorf("range %+v", c)
	}
	if (*got)[2].Alert.AlertID != "al" {
		t.Error("alert")
	}
	if a, _ := m.Snapshot().CalendarAccount(calendar.SampleWork); a.State.Status != api.CalendarSyncConsentRequired {
		t.Error("state not in the snapshot")
	}
}

// Replace keeps the visibility the user chose; Update without a change
// tells no one; a cancelled token hears nothing.
func TestReplaceAndUpdate(t *testing.T) {
	m, _ := source(false)
	m.SetVisible(calendar.SampleSport, false)
	got := record(m)
	m.Replace(samples(), PhaseUnavailable)
	if c, _ := m.Snapshot().Calendar(calendar.SampleSport); c.Visible {
		t.Error("Replace lost the visibility")
	}
	if m.Snapshot().Phase != PhaseUnavailable || len(*got) != 2 {
		t.Errorf("replace %v %v", m.Snapshot().Phase, *got)
	}
	*got = nil
	m.Update(func(s *Snapshot) { s.Phase = PhaseUnavailable })
	if len(*got) != 0 {
		t.Error("a no-op Update told the observers")
	}
	tok := m.Observe(func(Change) { t.Error("a cancelled observer was called") })
	tok.Cancel()
	m.Update(func(s *Snapshot) { s.Unanswered = 9 })
	if m.Snapshot().Unanswered != 9 || len(*got) != 1 {
		t.Error("Update")
	}
	// The source's data is its own: a change of the samples given does
	// not reach it.
	d := samples()
	m2 := NewInMemorySource(d, prague, nil)
	d.Occurrences[0].Title = "changed"
	d.Calendars.Accounts[0].Calendars[0].Name = "changed"
	if m2.Data().Occurrences[0].Title == "changed" || m2.Snapshot().CalendarName(calendar.SampleWorkCalendar) == "changed" {
		t.Error("the source shares the caller's slices")
	}
}

func TestPhaseString(t *testing.T) {
	for p, want := range map[Phase]string{PhaseLoading: "loading", PhaseReady: "ready", PhaseUnavailable: "unavailable",
		PhaseFailed: "failed", PhaseUnsupported: "unsupported", Phase(99): "unknown"} {
		if p.String() != want {
			t.Errorf("%d = %q", p, p.String())
		}
	}
}
