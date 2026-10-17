// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistant

import (
	"slices"
	"strings"
	"testing"
)

func TestPanelActionsWithCalendar(t *testing.T) {
	if !slices.Equal(PanelActionsWith(false), PanelActions) {
		t.Errorf("without the calendar: %q", PanelActionsWith(false))
	}
	want := []Action{Summarize, DraftReply, Tasks, Unread, WhatsOnToday, FindFreeSlot}
	if got := PanelActionsWith(true); !slices.Equal(got, want) {
		t.Errorf("with the calendar: %q, want %q", got, want)
	}
	got := PanelActionsWith(false)
	got[0] = "x"
	if PanelActions[0] != Summarize {
		t.Error("PanelActionsWith hands out PanelActions itself")
	}
	for _, a := range CalendarActions {
		if !IsCalendarAction(a) || CalendarLabel(identity{}, a) == "" {
			t.Errorf("%q: not a calendar action or no label", a)
		}
	}
	if CalendarLabel(identity{}, Summarize) != "" || IsCalendarAction(Summarize) {
		t.Error("a mail action is a calendar action")
	}
}

func TestCalendarLabels(t *testing.T) {
	eq := func(a Action, want string) {
		t.Helper()
		if got := CalendarLabel(identity{}, a); got != want {
			t.Errorf("CalendarLabel(%q) = %q", a, got)
		}
	}
	eq(WhatsOnToday, "What's on Today")
	eq(FindFreeSlot, "Find a Free Slot")
}

func TestCalendarPrompts(t *testing.T) {
	today, err := CalendarPrompt(identity{}, WhatsOnToday, "2026-10-16")
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"list_events", "list_invitations", "2026-10-16", "Change nothing"} {
		if !strings.Contains(today, w) {
			t.Errorf("What's on Today lacks %q: %s", w, today)
		}
	}
	slot, err := CalendarPrompt(identity{}, FindFreeSlot, "2026-10-16")
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"find_free_time", "2026-10-16"} {
		if !strings.Contains(slot, w) {
			t.Errorf("Find a Free Slot lacks %q: %s", w, slot)
		}
	}
	if !strings.HasSuffix(slot, ": ") || strings.HasSuffix(today, " ") {
		t.Errorf("endings: %q, %q", slot, today)
	}
	// No mail text and no ids: the date is all they carry.
	for _, p := range []string{today, slot} {
		if strings.Contains(p, "%") || strings.Contains(p, "message") {
			t.Errorf("a prompt with mail or a stray verb: %s", p)
		}
	}
	for _, a := range CalendarActions {
		if _, err := CalendarPrompt(identity{}, a, " "); err == nil {
			t.Errorf("%q: no error without a date", a)
		}
	}
	if _, err := CalendarPrompt(identity{}, Summarize, "2026-10-16"); err == nil {
		t.Error("a mail action has a calendar prompt")
	}
}

func TestCalendarTools(t *testing.T) {
	want := []string{"list_calendars", "list_events", "get_event", "find_free_time", "list_invitations"}
	var got []string
	for _, tool := range CalendarTools {
		got = append(got, strings.TrimPrefix(tool, toolPrefix))
		if !slices.Contains(AllowedTools, tool) {
			t.Errorf("the panel lacks %s", tool)
		}
		if slices.Contains(TriageTools, tool) || slices.Contains(TriageToolsWith(true), tool) || slices.Contains(TriageToolsWith(false), tool) {
			t.Errorf("triage has %s", tool)
		}
		if slices.Contains(SuggestReplyTools, tool) {
			t.Errorf("a suggested reply has %s", tool)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("CalendarTools = %q", got)
	}
}

func TestAskAboutEventPrompt(t *testing.T) {
	one := AskAboutEventPrompt(identity{}, "ev1", "")
	if !strings.Contains(one, "eventId ev1 (get_event)") || strings.Contains(one, "recurrenceId") || !strings.HasSuffix(one, "My question: ") {
		t.Errorf("single event: %q", one)
	}
	rec := AskAboutEventPrompt(identity{}, "ev1", "2026-10-20")
	if !strings.Contains(rec, "eventId ev1 and recurrenceId 2026-10-20 (get_event)") || !strings.HasSuffix(rec, "My question: ") {
		t.Errorf("occurrence: %q", rec)
	}
	for _, p := range []string{one, rec} {
		if strings.Contains(p, "%") || strings.Contains(p, "message") {
			t.Errorf("mail text or stray verb: %q", p)
		}
	}
}
