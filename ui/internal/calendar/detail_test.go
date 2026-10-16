// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestTimeLine(t *testing.T) {
	loc := prague(t)
	env := envIn(loc)
	tests := []struct {
		o    api.Occurrence
		want string
	}{
		{timed("a", at(loc, 2026, 10, 13, 10, 0), at(loc, 2026, 10, 13, 11, 30)), "Tuesday 13 October 2026 · 10:00–11:30 · 1 h 30 min"},
		{timed("a", at(loc, 2026, 10, 13, 22, 0), at(loc, 2026, 10, 14, 0, 0)), "Tuesday 13 October 2026 · 22:00–00:00 · 2 h"},
		{timed("a", at(loc, 2026, 10, 13, 10, 0), at(loc, 2026, 10, 15, 12, 0)), "Tue 13 Oct 2026 10:00–Thu 15 Oct 2026 12:00 · 50 h"},
		{allDayOcc("a", at(loc, 2026, 10, 13, 0, 0), 1), "Tuesday 13 October 2026 · All Day"},
		{allDayOcc("a", at(loc, 2026, 10, 13, 0, 0), 2), "Tuesday 13 October 2026–Wednesday 14 October 2026 · 2 days"},
		// The autumn change: 3 real hours, 2 on the clock.
		{timed("a", at(loc, 2026, 10, 25, 1, 0), at(loc, 2026, 10, 25, 3, 0)), "Sunday 25 October 2026 · 01:00–03:00 · 3 h"},
		{api.Occurrence{}, ""},
	}
	for _, tt := range tests {
		if got := TimeLine(tt.o, env); got != tt.want {
			t.Errorf("TimeLine = %q, want %q", got, tt.want)
		}
	}
}

func TestDurationText(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "0 min"}, {30 * time.Second, "0 min"}, {time.Minute, "1 min"}, {45 * time.Minute, "45 min"},
		{time.Hour, "1 h"}, {90 * time.Minute, "1 h 30 min"}, {24 * time.Hour, "1 day"}, {25 * time.Hour, "25 h"},
		{48 * time.Hour, "2 days"}, {-time.Hour, "0 min"},
	}
	for _, tt := range tests {
		if got := DurationText(tt.d, en{}); got != tt.want {
			t.Errorf("DurationText(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestResponseLabels(t *testing.T) {
	tests := map[api.EventResponse]string{api.ResponseAccepted: "Accepted", api.ResponseTentative: "Tentative",
		api.ResponseDeclined: "Declined", api.ResponseNeedsAction: "No Reply", api.ResponseDelegated: "Delegated", "x": "No Reply"}
	for r, want := range tests {
		if got := ResponseLabel(r, en{}); got != want {
			t.Errorf("ResponseLabel(%q) = %q, want %q", r, got, want)
		}
	}
	if got := MyResponseText(api.ResponseAccepted, en{}); got != "You: Accepted" {
		t.Errorf("MyResponseText %q", got)
	}
	if ResponseAction(api.ResponseAccepted, en{}) != "Accept" || ResponseAction(api.ResponseDeclined, en{}) != "Decline" ||
		ResponseAction(api.ResponseTentative, en{}) != "Tentative" {
		t.Error("ResponseAction")
	}
}

func TestDetailRules(t *testing.T) {
	attendee := api.Event{Occurrence: api.Occurrence{Title: "Planning", MyResponse: api.ResponseAccepted, AttendeeCount: 4},
		CanRespond: true, CanDelete: true, OrganizerAddress: &api.Address{Name: "Jana Nováková", Address: "jana@example.org"}}
	tests := []struct {
		name       string
		e          api.Event
		respond    bool
		remove     string
		ask        bool
		heading    string
		people     string
		invitation bool
	}{
		{"attendee", attendee, true, "Remove from Calendar", true, "Also tell the organiser you won’t attend?",
			"Organiser Jana Nováková · 4 attendees", false},
		{"declined attendee", func() api.Event { e := attendee; e.MyResponse = api.ResponseDeclined; return e }(), true,
			"Remove from Calendar", false, "", "Organiser Jana Nováková · 4 attendees", false},
		{"cancelled", func() api.Event { e := attendee; e.Status = api.EventStatusCancelled; return e }(), false,
			"Remove from Calendar", false, "", "Organiser Jana Nováková · 4 attendees", false},
		{"daemon says no", func() api.Event { e := attendee; e.CanRespond = false; return e }(), false,
			"Remove from Calendar", false, "", "Organiser Jana Nováková · 4 attendees", false},
		{"organiser with attendees", api.Event{Occurrence: api.Occurrence{Title: "Mine", Organizer: true, AttendeeCount: 1},
			RemoveIsDelete: true, CanRespond: true, InvitationMessages: []api.MessageRef{{}}}, false, "Delete", true, "Delete the Event?",
			"You organise · 1 attendee", true},
		{"own event", api.Event{Occurrence: api.Occurrence{Title: "Dentist"}, RemoveIsDelete: true}, false, "Delete", false, "", "", false},
		{"no organiser address", api.Event{Occurrence: api.Occurrence{AttendeeCount: 2}}, false, "Remove from Calendar", false, "",
			"2 attendees", false},
	}
	for _, tt := range tests {
		if got := CanRespond(tt.e); got != tt.respond {
			t.Errorf("%s: CanRespond = %v", tt.name, got)
		}
		if got := RemoveTitle(tt.e, en{}); got != tt.remove {
			t.Errorf("%s: RemoveTitle = %q", tt.name, got)
		}
		q, ok := RemoveQuestion(tt.e, en{})
		if ok != tt.ask || q.Heading != tt.heading {
			t.Errorf("%s: RemoveQuestion = %+v, %v", tt.name, q, ok)
		}
		if got := PeopleLine(tt.e, en{}); got != tt.people {
			t.Errorf("%s: PeopleLine = %q", tt.name, got)
		}
		if ShowsInvitation(tt.e) != tt.invitation {
			t.Errorf("%s: ShowsInvitation", tt.name)
		}
	}
	q, _ := RemoveQuestion(attendee, en{})
	if q.Confirm != "Remove and Decline" || q.Alternative != "Remove Only" || q.Body != "“Planning” will be removed from your calendar." {
		t.Errorf("attendee question %+v", q)
	}
	if q := DeleteQuestion(api.Event{}, en{}); q.Heading != "Remove “Untitled Event”?" || q.Confirm != "Remove from Calendar" {
		t.Errorf("DeleteQuestion %+v", q)
	}
}

func TestDetailOf(t *testing.T) {
	loc := prague(t)
	env := envIn(loc)
	s := SampleData(at(loc, 2026, 10, 14, 12, 0), loc)
	var planning api.Event
	for _, e := range s.Events {
		if e.EventID == "ev_sample_planning" {
			planning = e
		}
	}
	planning.Attendees = append(planning.Attendees, planning.Attendees[3], planning.Attendees[3], planning.Attendees[3])
	d := DetailOf(planning, "Work", "Calendar", env)
	if d.CalendarLine != "Work · Calendar" || d.Title != "Q4 planning" ||
		d.TimeLine != "Tuesday 13 October 2026 · 10:00–11:30 · 1 h 30 min" || d.VideoHost != "teams.microsoft.com" ||
		d.PeopleLine != "Organiser Jana Nováková · 7 attendees" || !d.Collapsed || !d.CanRespond || d.CanEdit ||
		d.RemoveTitle != "Remove from Calendar" || !d.ShowInvitation || d.Attendees[0].ResponseText != "Accepted" ||
		!d.Attendees[0].Organizer || !d.Attendees[3].Optional || d.Recurrence != "" {
		t.Errorf("detail %+v", d)
	}
	standup := s.Events[0]
	if d := DetailOf(standup, "Work", "", env); d.Recurrence != "Every weekday" || d.CalendarLine != "Work" || d.PeopleLine != "" {
		t.Errorf("standup %+v", d)
	}
}
