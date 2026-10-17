// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestNewEventDefaults(t *testing.T) {
	loc := prague(t)
	prefs := api.DefaultCalendarPreferences()
	noAlert := prefs
	noAlert.DefaultAlertMinutes, noAlert.DefaultDurationMinutes = -1, 30
	badDuration := prefs
	badDuration.DefaultDurationMinutes = 0
	tests := []struct {
		name       string
		at         time.Time
		origin     Origin
		prefs      api.CalendarPreferences
		start, end time.Time
		allDay     bool
		alerts     []int
	}{
		{"next half hour", at(loc, 2026, 10, 14, 10, 7), OriginNow, prefs, at(loc, 2026, 10, 14, 10, 30), at(loc, 2026, 10, 14, 11, 30), false, []int{15}},
		{"an exact half hour stays", at(loc, 2026, 10, 14, 10, 30), OriginNow, prefs, at(loc, 2026, 10, 14, 10, 30), at(loc, 2026, 10, 14, 11, 30), false, []int{15}},
		{"seconds past", time.Date(2026, 10, 14, 10, 30, 1, 0, loc), OriginNow, prefs, at(loc, 2026, 10, 14, 11, 0), at(loc, 2026, 10, 14, 12, 0), false, []int{15}},
		{"into tomorrow", at(loc, 2026, 10, 14, 23, 45), OriginNow, noAlert, at(loc, 2026, 10, 15, 0, 0), at(loc, 2026, 10, 15, 0, 30), false, []int{}},
		{"spring gap", at(loc, 2026, 3, 29, 1, 45), OriginNow, prefs, at(loc, 2026, 3, 29, 3, 0), at(loc, 2026, 3, 29, 4, 0), false, []int{15}},
		{"slot", at(loc, 2026, 10, 14, 14, 40), OriginSlot, prefs, at(loc, 2026, 10, 14, 14, 30), at(loc, 2026, 10, 14, 15, 30), false, []int{15}},
		{"month cell", at(loc, 2026, 10, 20, 0, 0), OriginMonthCell, badDuration, at(loc, 2026, 10, 20, 9, 0), at(loc, 2026, 10, 20, 10, 0), false, []int{15}},
		{"all-day row", at(loc, 2026, 10, 25, 13, 0), OriginAllDay, prefs, at(loc, 2026, 10, 25, 0, 0), at(loc, 2026, 10, 26, 0, 0), true, []int{900}},
		{"all-day row without an alert", at(loc, 2026, 10, 25, 13, 0), OriginAllDay, noAlert, at(loc, 2026, 10, 25, 0, 0), at(loc, 2026, 10, 26, 0, 0), true, []int{}},
	}
	for _, tt := range tests {
		d := NewEventDefaults(tt.at, tt.origin, tt.prefs, loc)
		if !d.Start.Equal(tt.start) || !d.End.Equal(tt.end) || d.AllDay != tt.allDay || !slices.Equal(d.Alerts, tt.alerts) || d.Alerts == nil {
			t.Errorf("%s: %+v, want %v–%v all-day %v alerts %v", tt.name, d, tt.start, tt.end, tt.allDay, tt.alerts)
		}
	}
}

func TestValidate(t *testing.T) {
	loc := prague(t)
	s := at(loc, 2026, 10, 14, 10, 0)
	ok := Input{Title: "T", Start: s, End: s.Add(time.Hour), CalendarID: "cal", Alerts: []int{15},
		Attendees: []api.EventInputAttendee{{Address: "jana@example.org"}}}
	tests := []struct {
		name string
		in   func(Input) Input
		want []Problem
	}{
		{"valid", func(in Input) Input { return in }, nil},
		{"no calendar", func(in Input) Input { in.CalendarID = ""; return in }, []Problem{ProblemNoCalendar}},
		{"end at start", func(in Input) Input { in.End = in.Start; return in }, []Problem{ProblemEndBeforeStart}},
		{"end before start", func(in Input) Input { in.End = in.Start.Add(-time.Minute); return in }, []Problem{ProblemEndBeforeStart}},
		{"all-day one day", func(in Input) Input { in.AllDay, in.End = true, AddDays(StartOfDay(in.Start, loc), 1); return in }, nil},
		{"all-day same day", func(in Input) Input { in.AllDay, in.End = true, in.Start; return in }, []Problem{ProblemEndBeforeStart}},
		{"long title", func(in Input) Input { in.Title = strings.Repeat("x", api.MaxEventTitleBytes+1); return in }, []Problem{ProblemTitleTooLong}},
		{"title of spaces", func(in Input) Input { in.Title = strings.Repeat(" ", api.MaxEventTitleBytes+10); return in }, nil},
		{"long location", func(in Input) Input { in.Location = strings.Repeat("x", api.MaxEventLocationBytes+1); return in }, []Problem{ProblemLocationTooLong}},
		{"long notes", func(in Input) Input { in.Notes = strings.Repeat("x", api.MaxEventNotesBytes+1); return in }, []Problem{ProblemNotesTooLong}},
		{"bad attendee", func(in Input) Input {
			in.Attendees = []api.EventInputAttendee{{Address: "jana"}, {Address: "x y@z"}}
			return in
		}, []Problem{ProblemInvalidAttendee}},
		{"too many attendees", func(in Input) Input {
			in.Attendees = nil
			for i := range api.MaxEventAttendeesIn + 1 {
				in.Attendees = append(in.Attendees, api.EventInputAttendee{Address: fmt.Sprintf("a%d@example.org", i)})
			}
			return in
		}, []Problem{ProblemTooManyAttendees}},
		{"too many alerts", func(in Input) Input { in.Alerts = []int{1, 2, 3, 4, 5}; return in }, []Problem{ProblemTooManyAlerts}},
		{"several", func(in Input) Input { in.CalendarID, in.End = "", in.Start; return in }, []Problem{ProblemNoCalendar, ProblemEndBeforeStart}},
	}
	for _, tt := range tests {
		if got := Validate(tt.in(ok)); !slices.Equal(got, tt.want) {
			t.Errorf("%s: %v, want %v", tt.name, got, tt.want)
		}
	}
	if ProblemText(ProblemTooManyAttendees, en{}) != "An event can have at most 100 attendees." ||
		ProblemText(ProblemTooManyAlerts, en{}) != "An event can have at most 4 alerts." {
		t.Error("ProblemText")
	}
}

func TestEventInput(t *testing.T) {
	loc := prague(t)
	in := Input{Title: "  Planning ", Start: at(loc, 2026, 10, 14, 10, 0), End: at(loc, 2026, 10, 14, 11, 30), Location: " B "}
	got := EventInput(in, loc)
	if got.Title != "Planning" || got.Start != "2026-10-14T10:00:00" || got.End != "2026-10-14T11:30:00" ||
		got.TimeZone != "Europe/Prague" || got.Location != "B" || got.Alerts == nil || got.StartDate != "" {
		t.Errorf("timed %+v", got)
	}
	in.AllDay, in.End = true, at(loc, 2026, 10, 16, 0, 0)
	got = EventInput(in, loc)
	if got.StartDate != "2026-10-14" || got.EndDate != "2026-10-16" || got.Start != "" || got.TimeZone != "" {
		t.Errorf("all-day %+v", got)
	}
	in.End = in.Start
	if got = EventInput(in, loc); got.EndDate != "2026-10-15" {
		t.Errorf("all-day without an end %+v", got)
	}
}

func TestPrimaryLabelAndFooter(t *testing.T) {
	mail := api.CalendarFeatures{Invitations: api.InvitationsViaMail}
	none := api.CalendarFeatures{Invitations: api.InvitationsViaNone}
	server := api.CalendarFeatures{Invitations: api.InvitationsViaServer}
	tests := []struct {
		n      int
		source api.CalendarSource
		f      api.CalendarFeatures
		want   string
	}{
		{0, api.CalendarSourceGraph, server, "Save"},
		{2, api.CalendarSourceGraph, none, "Save and Send Invitations"},
		{2, api.CalendarSourceCalDAV, mail, "Save and Send Invitations"},
		{2, api.CalendarSourceCalDAV, server, "Save and Send Invitations"},
		{2, api.CalendarSourceCalDAV, none, "Save"},
	}
	for _, tt := range tests {
		if got := PrimaryLabel(tt.n, tt.source, tt.f, en{}); got != tt.want {
			t.Errorf("PrimaryLabel(%d, %s, %s) = %q", tt.n, tt.source, tt.f.Invitations, got)
		}
	}
	got := Footer(1, mail, "Gmail", "me@example.com", []string{"agenda.pdf"}, en{})
	want := []string{"Invitations leave by e-mail from account Gmail (me@example.com) through the Outbox.", "Attachment: agenda.pdf"}
	if !slices.Equal(got, want) {
		t.Errorf("Footer %q", got)
	}
	if got := Footer(1, server, "", "", nil, en{}); !slices.Equal(got, []string{"The server sends the invitations."}) {
		t.Errorf("Footer server %q", got)
	}
	if got := Footer(0, mail, "Gmail", "x", nil, en{}); got != nil {
		t.Errorf("Footer without attendees %q", got)
	}
}

func TestAlertAndDurationTitles(t *testing.T) {
	alerts := map[int]string{-1: "None", 0: "At time of event", 1: "1 minute before", 5: "5 minutes before",
		60: "1 hour before", 120: "2 hours before", 90: "90 minutes before", 1440: "1 day before", 2880: "2 days before"}
	for m, want := range alerts {
		if got := AlertTitle(m, en{}); got != want {
			t.Errorf("AlertTitle(%d) = %q, want %q", m, got, want)
		}
	}
	var got []string
	for _, m := range DurationChoices {
		got = append(got, DurationTitle(m, en{}))
	}
	if fmt.Sprint(got) != "[15 min 30 min 45 min 1 h 1 h 30 min 2 h]" {
		t.Errorf("durations %v", got)
	}
}

func TestMailTexts(t *testing.T) {
	loc := prague(t)
	env := envIn(loc)
	o := timed("x", at(loc, 2026, 10, 13, 10, 0), at(loc, 2026, 10, 13, 11, 0))
	o.Title, o.Location = "Q4 planning", "Room B"
	subjects := map[MailKind]string{MailInvitation: "Invitation: Q4 planning", MailUpdate: "Updated invitation: Q4 planning",
		MailCancel: "Cancelled: Q4 planning", MailAccepted: "Accepted: Q4 planning", MailTentative: "Tentative: Q4 planning",
		MailDeclined: "Declined: Q4 planning"}
	for k, want := range subjects {
		if got := MailTexts(k, o, "Me", env); got.Subject != want {
			t.Errorf("subject %v = %q", k, got.Subject)
		}
	}
	got := MailTexts(MailAccepted, o, "Jana", env)
	if got.Text != "Jana has accepted the invitation to “Q4 planning”.\n\nWhen: Tuesday 13 October 2026 · 10:00–11:00 · 1 h\nWhere: Room B" {
		t.Errorf("text %q", got.Text)
	}
	o.Title, o.Location = "", ""
	if got := MailTexts(MailInvitation, api.Occurrence{}, "", env); got.Subject != "Invitation: Untitled Event" ||
		got.Text != "You are invited to “Untitled Event”." {
		t.Errorf("bare %+v", got)
	}
	long := o
	long.Title = strings.Repeat("é", api.MaxMailTextBytes)
	if got := MailTexts(MailInvitation, long, "", env); len(got.Subject) > api.MaxMailTextBytes || !strings.HasPrefix(got.Subject, "Invitation: ") {
		t.Errorf("long subject %d bytes", len(got.Subject))
	}
	if MailKindOf(api.ResponseDeclined) != MailDeclined || MailKindOf(api.ResponseTentative) != MailTentative ||
		MailKindOf(api.ResponseAccepted) != MailAccepted {
		t.Error("MailKindOf")
	}
}

func TestScopeQuestion(t *testing.T) {
	scopes := func(q ScopeQuestion) []api.EditScope {
		var out []api.EditScope
		for _, c := range q.Choices {
			out = append(out, c.Scope)
		}
		return out
	}
	all3 := []api.EditScope{api.EditScopeThis, api.EditScopeThisAndFuture, api.EditScopeAll}
	tests := []struct {
		a       ScopeAction
		complex bool
		want    []api.EditScope
		heading string
	}{
		{ScopeEdit, false, all3, "Change a Repeating Event?"},
		{ScopeDelete, false, all3, "Delete a Repeating Event?"},
		{ScopeRespond, false, []api.EditScope{api.EditScopeThis, api.EditScopeAll}, "Answer for the Whole Series?"},
		{ScopeEdit, true, []api.EditScope{api.EditScopeAll}, "Change a Repeating Event?"},
		{ScopeDelete, true, []api.EditScope{api.EditScopeAll}, "Delete a Repeating Event?"},
		{ScopeRespond, true, []api.EditScope{api.EditScopeThis, api.EditScopeAll}, "Answer for the Whole Series?"},
	}
	for _, tt := range tests {
		q := ScopeQuestionFor(tt.a, tt.complex, en{})
		if !slices.Equal(scopes(q), tt.want) || q.Heading != tt.heading {
			t.Errorf("%v %v: %+v", tt.a, tt.complex, q)
		}
	}
	q := ScopeQuestionFor(ScopeEdit, false, en{})
	if q.Choices[0].Title != "This Event Only" || q.Choices[1].Title != "This and Future Events" || q.Choices[2].Title != "All Events" {
		t.Errorf("titles %+v", q.Choices)
	}
	if c := CloseQuestion(en{}); c.Confirm != "Save" || c.Alternative != "Discard" {
		t.Errorf("close %+v", c)
	}
}

func TestAllDayAlerts(t *testing.T) {
	titles := []struct {
		m      int
		allDay bool
		want   string
	}{
		{900, true, "1 day before (9:00)"}, {2340, true, "2 days before (9:00)"}, {9540, true, "1 week before (9:00)"},
		{1080, true, "18 hours before"}, {-1, true, "None"}, {0, true, "At time of event"},
		{900, false, "15 hours before"}, {3780, true, "3 days before (9:00)"},
	}
	for _, tt := range titles {
		if got := AlertTitleFor(tt.m, tt.allDay, en{}); got != tt.want {
			t.Errorf("AlertTitleFor(%d, %v) = %q, want %q", tt.m, tt.allDay, got, tt.want)
		}
	}
	if !slices.Equal(AlertChoicesFor(true), AllDayAlertChoices) || !slices.Equal(AlertChoicesFor(false), AlertChoices) {
		t.Error("AlertChoicesFor")
	}
	prefs := api.DefaultCalendarPreferences()
	none := prefs
	none.DefaultAlertMinutes = -1
	toggles := []struct {
		cur  int
		on   bool
		p    api.CalendarPreferences
		want int
	}{
		{15, true, prefs, 900}, {-1, true, prefs, -1}, {2340, true, prefs, 2340}, {0, true, prefs, 900},
		{900, false, prefs, 15}, {-1, false, prefs, -1}, {9540, false, none, -1},
	}
	for _, tt := range toggles {
		if got := AlertOnAllDayToggle(tt.cur, tt.on, tt.p); got != tt.want {
			t.Errorf("AlertOnAllDayToggle(%d, %v) = %d, want %d", tt.cur, tt.on, got, tt.want)
		}
	}
}

func TestEditorDelete(t *testing.T) {
	own := api.Event{CanDelete: true, RemoveIsDelete: true}
	invitation := api.Event{Occurrence: api.Occurrence{MyResponse: api.ResponseAccepted}, CanDelete: true}
	tests := []struct {
		name    string
		editing bool
		e       api.Event
		title   string
		ok      bool
	}{
		{"new event", false, own, "", false},
		{"no canDelete", true, api.Event{RemoveIsDelete: true}, "", false},
		{"own event", true, own, "Delete", true},
		{"invitation", true, invitation, "Remove from Calendar", true},
	}
	for _, tt := range tests {
		if title, ok := EditorDelete(tt.editing, tt.e, en{}); title != tt.title || ok != tt.ok {
			t.Errorf("%s: %q %v", tt.name, title, ok)
		}
	}
}
