// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"slices"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestPillKind(t *testing.T) {
	kinds := map[api.InvitationKind]InvitationPill{api.InvitationKindInvitation: PillInvitation,
		api.InvitationKindUpdate: PillUpdate, api.InvitationKindCancelled: PillCancelled,
		api.InvitationKindReply: PillReply, api.InvitationKindOther: PillNone, "future": PillNone}
	methods := []api.InvitationMethod{api.InvitationMethodRequest, api.InvitationMethodReply, api.InvitationMethodCancel,
		api.InvitationMethodCounter, api.InvitationMethodDeclineCounter, api.InvitationMethodRefresh,
		api.InvitationMethodAdd, api.InvitationMethodPublish, api.InvitationMethodOther}
	for kind, want := range kinds {
		for _, st := range InvitationStates {
			for _, m := range methods {
				got := PillKind(&api.InvitationSummary{Method: m, Kind: kind, State: st})
				w := want
				if st == api.InvitationInvalid {
					w = PillNone
				}
				if got != w {
					t.Errorf("PillKind(%s, %s, %s) = %v, want %v", m, kind, st, got, w)
				}
			}
		}
	}
	if PillKind(nil) != PillNone {
		t.Error("nil")
	}
	titles := map[InvitationPill]string{PillNone: "", PillInvitation: "Invitation", PillUpdate: "Update",
		PillCancelled: "Cancelled", PillReply: "Reply"}
	for p, want := range titles {
		if got := PillTitle(p, en{}); got != want {
			t.Errorf("PillTitle(%v) = %q", p, got)
		}
		if StrikesSubject(p) != (p == PillCancelled) {
			t.Errorf("StrikesSubject(%v)", p)
		}
	}
}

func TestSampleCards(t *testing.T) {
	loc := prague(t)
	env := envIn(loc)
	s := SampleData(at(loc, 2026, 10, 14, 12, 0), loc)
	names := func(id api.CalendarID) string { return s.CalendarName(id) }
	type row struct {
		mode               CardMode
		responses, strip   bool
		propose            bool
		state, reply, conf string
	}
	want := map[api.InvitationState]row{
		api.InvitationUnanswered: {CardFull, true, false, false, "", "Petr Svoboda replied",
			"Conflicts with Standup (15:30–16:00) in calendar Calendar"},
		api.InvitationAnswered: {CardFull, false, true, false, "", "Petr Svoboda accepted",
			"Conflicts with Standup (15:30–16:00) in calendar Calendar"},
		api.InvitationOutdated: {CardFull, false, false, false, "This invitation is out of date: a newer one arrived, or the event is over.",
			"Petr Svoboda replied", "Conflicts with Standup (15:30–16:00) in calendar Calendar"},
		api.InvitationCancelled: {CardCancelled, false, false, false, "This event was cancelled.", "Petr Svoboda replied",
			"Conflicts with Standup (15:30–16:00) in calendar Calendar"},
		api.InvitationProcessed: {CardReply, false, false, false, "", "Jana Nováková accepted (applied to your event)", ""},
		api.InvitationInvalid:   {CardNone, false, false, false, "The invitation in this message could not be read.", " replied", ""},
	}
	for _, c := range s.Cards {
		w := want[c.State]
		got := row{CardModeOf(&c, true), ShowsResponses(c), ShowsStateStrip(c), ShowsProposeNewTime(c), StateLine(c, en{}),
			ReplyCard(c, en{}), ConflictLine(c.Conflicts, names, env)}
		if got != w {
			t.Errorf("%s: %+v, want %+v", c.State, got, w)
		}
		if CardModeOf(&c, false) != CardNone {
			t.Errorf("%s: a card with the switch off", c.State)
		}
	}
	c := s.Cards[0]
	if tile, ok := DateTileOf(c.InvitationSummary, env); !ok || tile != (DateTile{"Fri", "16", "Oct"}) {
		t.Errorf("tile %+v", tile)
	}
	if got := CardTimeLine(c, env); got != "Friday 16 October 2026 · 15:00–16:00 · 1 h" {
		t.Errorf("time %q", got)
	}
	if got := CardPeopleLine(c, en{}); got != "Organiser Petr Svoboda · 3 attendees" {
		t.Errorf("people %q", got)
	}
	if got := ReplyNote(c, "Gmail", en{}); !slices.Equal(got, []string{"The reply leaves from account Gmail.",
		"The reply goes to Petr Svoboda <petr@example.org>."}) {
		t.Errorf("reply note %q", got)
	}
	if got := StateStrip(s.Cards[1], "Personal", en{}); got != "Accepted · in calendar Personal" {
		t.Errorf("strip %q", got)
	}
	if got := PreviousLines(s.Cards[2], env); !slices.Equal(got, []string{"Previously Friday 16 October 2026 · 14:00–15:00 · 1 h"}) {
		t.Errorf("previous %q", got)
	}
	if CardModeOf(nil, true) != CardNone {
		t.Error("nil card")
	}
	other := c
	other.Kind = api.InvitationKindOther
	if CardModeOf(&other, true) != CardNone {
		t.Error("kind other has a card")
	}
	proposing := c
	proposing.CanProposeNewTime = true
	if !ShowsProposeNewTime(proposing) {
		t.Error("Propose New Time hidden")
	}
}

func TestInvitationLines(t *testing.T) {
	loc := prague(t)
	env := envIn(loc)
	s, e := at(loc, 2026, 10, 14, 10, 0), at(loc, 2026, 10, 14, 10, 30)
	name := func(api.CalendarID) string { return "Work · Calendar" }
	tests := []struct {
		c    []api.InvitationConflict
		want string
	}{
		{nil, ""},
		{[]api.InvitationConflict{{Title: "Standup", Start: s, End: e}}, "Conflicts with Standup (10:00–10:30) in calendar Work · Calendar"},
		{[]api.InvitationConflict{{Start: s, End: e}}, "Conflicts with a private event (10:00–10:30) in calendar Work · Calendar"},
		{[]api.InvitationConflict{{}, {}, {}}, "Conflicts with 3 events"},
	}
	for _, tt := range tests {
		if got := ConflictLine(tt.c, name, env); got != tt.want {
			t.Errorf("ConflictLine = %q, want %q", got, tt.want)
		}
	}
	org := &api.Address{Address: "jana@example.org"}
	notes := []struct {
		c    api.InvitationCard
		acct string
		want []string
	}{
		{api.InvitationCard{ReplyVia: api.InvitationsViaServer, ReplyTo: org}, "", []string{"The reply goes to jana@example.org."}},
		{api.InvitationCard{ReplyVia: api.InvitationsViaNone}, "", []string{"No reply will be sent."}},
		{api.InvitationCard{ReplyVia: api.InvitationsViaMail, OrganizerMismatch: true}, "Work", []string{"The reply leaves from account Work.",
			"The organiser’s address is not from the sender’s domain. Check the invitation before you answer."}},
	}
	for _, tt := range notes {
		if got := ReplyNote(tt.c, tt.acct, en{}); !slices.Equal(got, tt.want) {
			t.Errorf("ReplyNote = %q, want %q", got, tt.want)
		}
	}
	strips := []struct {
		r    api.EventResponse
		cal  string
		want string
	}{
		{api.ResponseAccepted, "Work", "Accepted · in calendar Work"},
		{api.ResponseTentative, "", "Tentative"},
		{api.ResponseDeclined, "Work", "Declined"},
	}
	for _, tt := range strips {
		if got := StateStrip(api.InvitationCard{Response: tt.r}, tt.cal, en{}); got != tt.want {
			t.Errorf("StateStrip = %q, want %q", got, tt.want)
		}
	}
	jana := &api.Address{Name: "Jana", Address: "jana@example.org"}
	replies := map[api.EventResponse]string{api.ResponseAccepted: "Jana accepted", api.ResponseTentative: "Jana tentatively accepted",
		api.ResponseDeclined: "Jana declined", api.ResponseDelegated: "Jana replied"}
	for r, want := range replies {
		if got := ReplyCard(api.InvitationCard{InvitationSummary: api.InvitationSummary{ReplyFrom: jana}, Response: r}, en{}); got != want {
			t.Errorf("ReplyCard(%s) = %q", r, got)
		}
	}
	allDay := api.InvitationCard{InvitationSummary: api.InvitationSummary{AllDay: true, StartDate: "2026-10-15"}, EndDate: "2026-10-17",
		Previous: &api.InvitationPrevious{Start: "2026-10-14", End: "2026-10-15"}}
	if got := CardTimeLine(allDay, env); got != "Thursday 15 October 2026–Friday 16 October 2026 · 2 days" {
		t.Errorf("all-day card %q", got)
	}
	if got := PreviousLines(allDay, env); !slices.Equal(got, []string{"Previously Wednesday 14 October 2026 · All Day"}) {
		t.Errorf("all-day previous %q", got)
	}
	if got := PreviousLines(api.InvitationCard{Previous: &api.InvitationPrevious{Start: "garbage"}}, env); got != nil {
		t.Errorf("bad previous %q", got)
	}
	if _, ok := DateTileOf(api.InvitationSummary{}, env); ok {
		t.Error("tile without a start")
	}
	unanswerable := api.InvitationCard{InvitationSummary: api.InvitationSummary{State: api.InvitationUnanswered}}
	if StateLine(unanswerable, en{}) != "This invitation cannot be answered from here." || ShowsResponses(unanswerable) {
		t.Error("an unanswerable invitation")
	}
	_ = time.Second
}

func TestPreviousLines(t *testing.T) {
	loc := prague(t)
	env := envIn(loc)
	s, e := at(loc, 2026, 10, 20, 10, 0).UTC(), at(loc, 2026, 10, 20, 11, 0).UTC()
	weekly := &api.Recurrence{Frequency: api.FrequencyWeekly, Interval: 1, Until: "2026-10-27"}
	card := func(p api.InvitationPrevious) api.InvitationCard {
		return api.InvitationCard{InvitationSummary: api.InvitationSummary{Start: &s, End: &e}, Previous: &p}
	}
	when := api.InvitationPrevious{Start: api.DateOrTime(at(loc, 2026, 10, 13, 9, 0).Format(time.RFC3339)),
		End: api.DateOrTime(at(loc, 2026, 10, 13, 10, 0).Format(time.RFC3339))}
	timeLine := "Previously Tuesday 13 October 2026 · 09:00–10:00 · 1 h"
	tests := []struct {
		name string
		p    api.InvitationPrevious
		want []string
	}{
		{"time", when, []string{timeLine}},
		{"location", api.InvitationPrevious{Location: "Room B"}, []string{"Previously at Room B"}},
		{"recurrence", api.InvitationPrevious{Recurrence: weekly}, []string{"Previously repeated: Every week on Tuesday, until 27 October 2026"}},
		{"all three", api.InvitationPrevious{Start: when.Start, End: when.End, Location: " Room\nB ", Recurrence: weekly},
			[]string{timeLine, "Previously at Room B", "Previously repeated: Every week on Tuesday, until 27 October 2026"}},
		{"empty location", api.InvitationPrevious{Location: "\u200b "}, nil},
		{"nothing", api.InvitationPrevious{}, nil},
	}
	for _, tt := range tests {
		if got := PreviousLines(card(tt.p), env); !slices.Equal(got, tt.want) {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
	if PreviousLines(api.InvitationCard{}, env) != nil {
		t.Error("no previous")
	}
	// Without a start the rule has no day to describe.
	if got := PreviousLines(api.InvitationCard{Previous: &api.InvitationPrevious{Recurrence: weekly}}, env); got != nil {
		t.Errorf("recurrence without a start %q", got)
	}
}

func TestOwnEvent(t *testing.T) {
	own := api.InvitationCard{InvitationSummary: api.InvitationSummary{Method: api.InvitationMethodRequest,
		Kind: api.InvitationKindInvitation, State: api.InvitationProcessed, OwnEvent: true}, EventID: "ev_1"}
	if got := StateLine(own, en{}); got != "You organise this event." {
		t.Errorf("own line %q", got)
	}
	if CardModeOf(&own, true) != CardFull || ShowsResponses(own) || !ShowsInCalendar(own) {
		t.Error("own card")
	}
	update := own
	update.Kind, update.State = api.InvitationKindUpdate, api.InvitationOutdated
	if StateLine(update, en{}) != "You organise this event." {
		t.Error("own update")
	}
	reply := own
	reply.Kind = api.InvitationKindReply
	if StateLine(reply, en{}) == "You organise this event." {
		t.Error("a reply to the user's event says the user organises it")
	}
	tests := []struct {
		name  string
		c     api.InvitationCard
		shows bool
	}{
		{"own, linked", own, true},
		{"own, not stored", func() api.InvitationCard { c := own; c.EventID = ""; return c }(), false},
		{"answered, linked", api.InvitationCard{InvitationSummary: api.InvitationSummary{State: api.InvitationAnswered}, EventID: "e"}, true},
		{"answered, not linked", api.InvitationCard{InvitationSummary: api.InvitationSummary{State: api.InvitationAnswered}}, false},
		{"unanswered, linked", api.InvitationCard{InvitationSummary: api.InvitationSummary{State: api.InvitationUnanswered}, EventID: "e"}, false},
	}
	for _, tt := range tests {
		if ShowsInCalendar(tt.c) != tt.shows {
			t.Errorf("%s: ShowsInCalendar %v", tt.name, !tt.shows)
		}
	}
}
