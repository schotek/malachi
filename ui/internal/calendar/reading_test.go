// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"math"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// What Mail shows of a message with a calendar part (Swift
// InvitationReadingTests).

func sampleCard(t *testing.T, st api.InvitationState) (api.InvitationCard, Env) {
	t.Helper()
	p := prague(t)
	s := SampleData(at(p, 2026, 10, 14, 10, 0), p)
	for i, x := range InvitationStates {
		if x == st {
			return s.Cards[i], envIn(p)
		}
	}
	t.Fatalf("no sample for %s", st)
	return api.InvitationCard{}, Env{}
}

func summaryOf(inv *api.InvitationSummary) api.MessageSummary {
	return api.MessageSummary{ID: "m1", Subject: "Vendor demo", Invitation: inv}
}

func TestInvitationPillOfARow(t *testing.T) {
	if InvitationPillOf(summaryOf(nil)) != PillNone {
		t.Error("no invitation")
	}
	kinds := map[api.InvitationKind]InvitationPill{api.InvitationKindInvitation: PillInvitation,
		api.InvitationKindUpdate: PillUpdate, api.InvitationKindCancelled: PillCancelled,
		api.InvitationKindReply: PillReply, api.InvitationKindOther: PillNone}
	for k, want := range kinds {
		if got := InvitationPillOf(summaryOf(&api.InvitationSummary{Kind: k, State: api.InvitationUnanswered})); got != want {
			t.Errorf("%s: %v want %v", k, got, want)
		}
		if got := InvitationPillOf(summaryOf(&api.InvitationSummary{Kind: k, State: api.InvitationInvalid})); got != PillNone {
			t.Errorf("%s invalid: %v", k, got)
		}
	}
	// The state shows in Unanswered, not in the pill.
	if InvitationPillOf(summaryOf(&api.InvitationSummary{Kind: api.InvitationKindInvitation, State: api.InvitationAnswered})) != PillInvitation {
		t.Error("answered elsewhere keeps its pill")
	}
}

func TestInvitationReadingOnlyFromMessageGet(t *testing.T) {
	c, _ := sampleCard(t, api.InvitationUnanswered)
	msg := func(c *api.InvitationCard) *api.Message {
		return &api.Message{MessageSummary: summaryOf(nil), Invitation: c}
	}
	if InvitationReading(nil, true) != nil || InvitationReading(msg(nil), true) != nil {
		t.Error("nothing before message.get")
	}
	if InvitationReading(msg(&c), true) == nil {
		t.Error("the card")
	}
	if InvitationReading(msg(&c), false) != nil {
		t.Error("the account's switch")
	}
	inv, _ := sampleCard(t, api.InvitationInvalid)
	if InvitationReading(msg(&inv), true) != nil {
		t.Error("an unreadable part")
	}
	for _, st := range []api.InvitationState{api.InvitationProcessed, api.InvitationCancelled} {
		x, _ := sampleCard(t, st)
		if InvitationReading(msg(&x), true) == nil {
			t.Errorf("%s shows a card", st)
		}
	}
}

func TestShowsInvitationCardOfAnAccount(t *testing.T) {
	if !ShowsInvitationCard(nil) || !ShowsInvitationCard(&api.Account{}) {
		t.Error("default on")
	}
	a := api.Account{}
	a.Config.Calendar = &api.CalendarConfig{InvitationCard: api.Ptr(false)}
	if ShowsInvitationCard(&a) {
		t.Error("switched off")
	}
}

func TestCardClockLineWithTheRepetition(t *testing.T) {
	c, env := sampleCard(t, api.InvitationUnanswered)
	plain := CardTimeLine(c, env)
	if plain == "" || CardClockLine(c, env) != plain {
		t.Fatalf("plain %q", plain)
	}
	c.Recurrence = &api.Recurrence{Frequency: api.FrequencyWeekly, Interval: 2}
	line := CardClockLine(c, env)
	if len(line) <= len(plain)+3 || line[:len(plain)+len(" · ")] != plain+" · " {
		t.Errorf("line %q", line)
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestDayStripOfTheSample(t *testing.T) {
	c, env := sampleCard(t, api.InvitationUnanswered)
	s := DayStripOf(c, env)
	if s == nil {
		t.Fatal("no strip")
	}
	// 8:00–17:00: ten whole hours, both ends.
	if len(s.Ticks) != 10 || s.Ticks[0] != (StripTick{0, "08:00"}) || s.Ticks[9] != (StripTick{1, "17:00"}) {
		t.Fatalf("ticks %v", s.Ticks)
	}
	if len(s.Busy) != 2 || !near(s.Busy[0].From, 1.0/9) || !near(s.Busy[0].To, 1.25/9) {
		t.Fatalf("busy %v", s.Busy)
	}
	if s.Proposed == nil || !near(s.Proposed.From, 7.0/9) || !near(s.Proposed.To, 8.0/9) || s.ProposedLabel != "15:00–16:00" {
		t.Fatalf("proposed %v %q", s.Proposed, s.ProposedLabel)
	}
}

func TestDayStripOfHostileSpans(t *testing.T) {
	c, env := sampleCard(t, api.InvitationUnanswered)
	inv, _ := sampleCard(t, api.InvitationInvalid)
	if DayStripOf(inv, env) != nil {
		t.Error("the zero span of an unreadable part")
	}
	back := c
	back.Day = api.InvitationDay{From: c.Day.To, To: c.Day.From}
	if DayStripOf(back, env) != nil {
		t.Error("backwards")
	}
	now := at(env.loc(), 2026, 10, 14, 10, 0)
	long := c
	long.Day = api.InvitationDay{From: now, To: now.Add(30 * 24 * time.Hour)}
	if DayStripOf(long, env) != nil {
		t.Error("not a day")
	}

	// Busy spans outside, backwards or many: clipped, dropped, capped.
	from, to := now, now.Add(10*time.Hour)
	busy := []api.DayBusy{
		{Start: from.Add(-2 * time.Hour), End: from.Add(30 * time.Minute)},
		{Start: to.Add(time.Hour), End: to.Add(2 * time.Hour)},
		{Start: from.Add(time.Hour), End: from.Add(30 * time.Minute)},
	}
	for i := 0; i < 500; i++ {
		s := from.Add(time.Duration(i) * time.Minute)
		busy = append(busy, api.DayBusy{Start: s, End: s.Add(30 * time.Second)})
	}
	c.Day = api.InvitationDay{From: from, To: to, Busy: busy}
	st, en := from.Add(-time.Hour), from.Add(-time.Minute)
	c.Start, c.End = &st, &en
	s := DayStripOf(c, env)
	if s == nil {
		t.Fatal("no strip")
	}
	if len(s.Busy) > MaxStripBusy {
		t.Errorf("busy %d", len(s.Busy))
	}
	for _, b := range s.Busy {
		if b.From < 0 || b.To > 1 || b.From >= b.To {
			t.Errorf("span %v", b)
		}
	}
	if s.Busy[0] != (StripSpan{0, 0.05}) {
		t.Errorf("clipped at the start: %v", s.Busy[0])
	}
	if s.Proposed != nil || s.ProposedLabel != "" {
		t.Error("a slot off the axis")
	}
	if len(s.Ticks) > MaxStripTicks {
		t.Errorf("ticks %d", len(s.Ticks))
	}

	// A long axis labels fewer hours.
	c.Day = api.InvitationDay{From: from, To: from.Add(40 * time.Hour)}
	if l := DayStripOf(c, env); l == nil || len(l.Ticks) > MaxStripTicks {
		t.Error("long axis")
	}
}
