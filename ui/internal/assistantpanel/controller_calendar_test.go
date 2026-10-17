// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistantpanel

import (
	"slices"
	"testing"
	"time"

	"github.com/schotek/malachi/ui/internal/assistant"
)

// The bridge's lines of a list_events and a get_event result (escaped for
// fakeToolResult's JSON string).
const (
	listEventsResult = `2 events from 2026-09-28T22:00:00Z to 2026-09-29T22:00:00Z\n` +
		`1. 2026-09-29T08:00:00Z..2026-09-29T09:00:00Z response=accepted eventId=e_1 recurrenceId=2026-09-29T08:00:00Z calendarId=c1\n` +
		`2. all-day 2026-09-29..2026-09-30 (end exclusive) eventId=e_2 calendarId=c1`
	getEventResult = `event e_9\ncalendar: c1 (account a1)\ntime: all-day 2026-10-20..2026-10-21 (end exclusive)`
)

// What's on Today is sent at once, without a context; the first event a
// list_events result of the turn names becomes a calendar card (one per
// turn, never from an error); its Show in Calendar goes to the
// application. A later turn's get_event has a card of its own.
func TestRunCalendarWhatsOnTodayAndCard(t *testing.T) {
	fake := newFakeClaude(t, "true", "",
		fakeTurn{lines: []string{
			fakeInit,
			fakeToolUse("t0", "list_events"), fakeToolResult("t0", listEventsResult, true),
			fakeToolUse("t1", "list_events"), fakeToolResult("t1", listEventsResult, false),
			fakeToolUse("t2", "get_event"), fakeToolResult("t2", getEventResult, false),
			fakeText("Two events today."), fakeResult("done", true),
		}},
		fakeTurn{lines: []string{
			fakeInit, fakeToolUse("t3", "get_event"), fakeToolResult("t3", getEventResult, false),
			fakeText("ok"), fakeResult("done", true),
		}})
	h := newHarness(t, fake, true, testBridge)
	var opened []assistant.CalendarRef
	h.panel.OpenCalendar = func(r assistant.CalendarRef) { opened = append(opened, r) }
	if !h.panel.CanRunCalendarActions() || h.panel.CanRunActions() {
		t.Fatal("the calendar actions need no context, the message actions do")
	}
	h.panel.RunCalendar(assistant.Summarize) // not a calendar action
	if h.panel.Phase() != PhaseIdle {
		t.Fatal("a mail action ran as a calendar one")
	}
	h.panel.RunCalendar(assistant.WhatsOnToday)
	h.turn()
	want, err := assistant.WhatsOnTodayPrompt(tr, "2026-09-29")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(fake.prompts(), []string{want}) {
		t.Errorf("prompts %q", fake.prompts())
	}
	var cards []Item
	for _, it := range h.panel.Items() {
		if it.Content.Kind == ContentCalendar {
			cards = append(cards, it)
		}
	}
	if len(cards) != 1 {
		t.Fatalf("%d calendar cards: %+v", len(cards), h.contents())
	}
	c := h.contents()
	if c[0] != user("What's on Today", "") {
		t.Errorf("question %+v", c[0])
	}
	ref := cards[0].Content.Calendar
	if ref.EventID != "e_1" || ref.RecurrenceID != "2026-09-29T08:00:00Z" || ref.Start == nil ||
		!ref.Start.Equal(time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("card %+v", ref)
	}
	h.panel.OpenCalendarItem(cards[0].ID)
	h.panel.OpenDraftItem(cards[0].ID) // not a draft card
	if len(opened) != 1 || opened[0].EventID != "e_1" || len(h.opened) != 0 {
		t.Errorf("opened %+v, drafts %+v", opened, h.opened)
	}

	// The next turn: its own card.
	if !h.panel.Submit("And next week?") {
		t.Fatal("follow-up refused")
	}
	h.turn()
	if l := h.last(); l != answer("ok", false) {
		t.Errorf("last %+v", l)
	}
	n := 0
	for _, it := range h.panel.Items() {
		if it.Content.Kind == ContentCalendar {
			n++
			if it.ID != cards[0].ID && (it.Content.Calendar.EventID != "e_9" || it.Content.Calendar.Date != "2026-10-20") {
				t.Errorf("second card %+v", it.Content.Calendar)
			}
		}
	}
	if n != 2 {
		t.Errorf("%d cards after two turns", n)
	}
}

// Find a Free Slot waits for the user's words and sends them after its
// prompt; empty words are refused.
func TestRunCalendarFindFreeSlotWaits(t *testing.T) {
	fake := newFakeClaude(t, "true", "", answerTurn("Tuesday at 10."))
	h := newHarness(t, fake, true, testBridge)
	h.panel.RunCalendar(assistant.FindFreeSlot)
	if h.panel.Pending() != (Pending{Kind: PendingAction, Action: assistant.FindFreeSlot}) ||
		h.panel.PendingLabel() != "Find a Free Slot" || h.focused != 1 || h.panel.Phase() != PhaseIdle {
		t.Fatalf("pending %+v, label %q, focused %d", h.panel.Pending(), h.panel.PendingLabel(), h.focused)
	}
	if h.panel.Submit("   ") {
		t.Fatal("empty words taken")
	}
	if !h.panel.Submit("30 minutes with Jana") {
		t.Fatal("words refused")
	}
	h.turn()
	p, err := assistant.FindFreeSlotPrompt(tr, "2026-09-29")
	if err != nil {
		t.Fatal(err)
	}
	if want := p + "30 minutes with Jana"; !slices.Equal(fake.prompts(), []string{want}) {
		t.Errorf("prompts %q", fake.prompts())
	}
	if h.panel.Pending().Kind != PendingNone {
		t.Error("the action still waits")
	}
	if c := h.contents(); len(c) == 0 || c[0] != user("Find a Free Slot", "30 minutes with Jana") {
		t.Errorf("transcript %+v", c)
	}
}
