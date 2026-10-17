// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendarctl

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/calendardata"
)

// The invitation card's controller over the sample cards, the port of
// CalendarInvitationControllerTests.swift: the card mode per state and
// switch, the lines, the target calendar, the answer with the target,
// Decline Without Reply, Propose New Time, a refusal inline, the
// cancellation's Remove from Calendar and Show in Calendar; and the
// cards of D29, D30 and D32.

func sampleCard(t *testing.T, loc *time.Location, st api.InvitationState) api.InvitationCard {
	t.Helper()
	i := slices.Index(calendar.InvitationStates, st)
	if i < 0 {
		t.Fatalf("no sample card %s", st)
	}
	return calendar.SampleData(dtNow(loc), loc).Cards[i]
}

func invitation(t *testing.T, s *calendardata.InMemorySource, l *dtLoop, loc *time.Location, card api.InvitationCard,
	cardOn bool) *InvitationController {
	return NewInvitationController(s, dtOpts(loc, l), calendar.SampleGmail, "msg_sample_demo", card, cardOn)
}

func TestInvitationModesPerState(t *testing.T) {
	s, l, loc := dtSamples(t, true)
	for st, want := range map[api.InvitationState]calendar.CardMode{
		api.InvitationUnanswered: calendar.CardFull, api.InvitationAnswered: calendar.CardFull,
		api.InvitationCancelled: calendar.CardCancelled, api.InvitationProcessed: calendar.CardReply,
		api.InvitationInvalid: calendar.CardNone,
	} {
		if got := invitation(t, s, l, loc, sampleCard(t, loc, st), true).Mode(); got != want {
			t.Errorf("%s: mode %d, want %d", st, got, want)
		}
	}
	if invitation(t, s, l, loc, sampleCard(t, loc, api.InvitationUnanswered), false).Mode() != calendar.CardNone {
		t.Error("a card with the switch off")
	}
}

func TestInvitationLinesOfAnUnansweredCard(t *testing.T) {
	s, l, loc := dtSamples(t, true)
	c := invitation(t, s, l, loc, sampleCard(t, loc, api.InvitationUnanswered), true)
	if c.Title() != "Vendor demo" || c.PillTitle() != calendar.PillTitle(calendar.PillInvitation, dtEn{}) {
		t.Errorf("title %q pill %q", c.Title(), c.PillTitle())
	}
	if tile, ok := c.DateTile(); !ok || tile != (calendar.DateTile{Weekday: "Fri", Day: "16", Month: "Oct"}) {
		t.Errorf("tile %+v", tile)
	}
	if !c.ShowsResponses() || c.ShowsStateStrip() || c.ShowsProposeNewTime() {
		t.Error("the buttons of an unanswered card")
	}
	if !strings.HasPrefix(c.ConflictLine(), "Conflicts with Standup") {
		t.Errorf("conflict %q", c.ConflictLine())
	}
	if c.Target() != calendar.SamplePersonal || !c.TargetIsPopup() {
		t.Errorf("target %s popup %v", c.Target(), c.TargetIsPopup())
	}
	var titles []string
	for _, x := range c.Targets() {
		titles = append(titles, x.Title)
	}
	if !slices.Equal(titles, []string{"Gmail · Personal", "Gmail · Family"}) {
		t.Errorf("targets %q", titles)
	}
	if !slices.Contains(c.ReplyNote(), "The reply leaves from account Gmail.") {
		t.Errorf("reply note %q", c.ReplyNote())
	}
	if c.StateLine() != "" || len(c.PreviousLines()) != 0 {
		t.Errorf("state %q previous %q", c.StateLine(), c.PreviousLines())
	}
}

func TestInvitationAnswerWithTheTarget(t *testing.T) {
	s, l, loc := dtSamples(t, true)
	c := invitation(t, s, l, loc, sampleCard(t, loc, api.InvitationUnanswered), true)
	c.SetTarget(calendar.SampleFamily)
	c.SetTarget("cal_unknown")
	if c.Target() != calendar.SampleFamily {
		t.Fatalf("target %s", c.Target())
	}
	c.Respond(api.ResponseAccepted, false)
	if !c.Busy() {
		t.Error("not busy while answering")
	}
	l.run()
	p := lastWrite[api.InvitationRespondParams](t, s, api.MethodInvitationRespond)
	if p.AccountID != calendar.SampleGmail || p.MessageID != "msg_sample_demo" || p.Response != api.ResponseAccepted ||
		p.CalendarID != calendar.SampleFamily || p.SendResponse != nil {
		t.Errorf("params %+v", p)
	}
	if p.Mail == nil || p.Mail.Subject != "Accepted: Vendor demo" {
		t.Errorf("mail %+v", p.Mail)
	}
	if c.Busy() || c.Card().State != api.InvitationAnswered || !c.ShowsStateStrip() {
		t.Errorf("after answering: busy %v state %s", c.Busy(), c.Card().State)
	}
	if c.StateStrip() != "Accepted · in calendar Gmail · Family" {
		t.Errorf("strip %q", c.StateStrip())
	}
}

func TestInvitationDeclineWithoutReplySendsNothing(t *testing.T) {
	s, l, loc := dtSamples(t, true)
	c := invitation(t, s, l, loc, sampleCard(t, loc, api.InvitationUnanswered), true)
	c.Respond(api.ResponseDeclined, true)
	l.run()
	p := lastWrite[api.InvitationRespondParams](t, s, api.MethodInvitationRespond)
	if p.CalendarID != "" || p.SendResponse == nil || *p.SendResponse || p.Mail != nil {
		t.Errorf("params %+v", p)
	}
}

func TestInvitationProposeNewTimeNeedsTheFlag(t *testing.T) {
	s, l, loc := dtSamples(t, true)
	start := time.Date(2026, 10, 16, 13, 0, 0, 0, loc)
	c := invitation(t, s, l, loc, sampleCard(t, loc, api.InvitationUnanswered), true)
	c.ProposeNewTime(start, start.Add(time.Hour))
	l.run()
	if len(s.Writes()) != 0 {
		t.Fatal("proposed without the flag")
	}
	card := sampleCard(t, loc, api.InvitationUnanswered)
	card.CanProposeNewTime = true
	p := NewInvitationController(s, dtOpts(loc, l), calendar.SampleGmail, "m", card, true)
	if !p.ShowsProposeNewTime() {
		t.Error("Propose New Time… not shown")
	}
	p.ProposeNewTime(start, start)
	l.run()
	if len(s.Writes()) != 0 {
		t.Fatal("proposed an empty span")
	}
	p.ProposeNewTime(start, start.Add(time.Hour))
	l.run()
	q := lastWrite[api.InvitationRespondParams](t, s, api.MethodInvitationRespond)
	if q.Response != api.ResponseTentative || q.ProposedStart == nil || !q.ProposedStart.Equal(start) ||
		q.ProposedEnd == nil || !q.ProposedEnd.Equal(start.Add(time.Hour)) {
		t.Errorf("proposal %+v", q)
	}
}

func TestInvitationRefusalShowsInline(t *testing.T) {
	s, l, loc := dtSamples(t, true)
	s.Failures[api.MethodInvitationRespond] = &api.Error{Code: api.CodeCalendarConsentRequired, Message: "consent"}
	c := invitation(t, s, l, loc, sampleCard(t, loc, api.InvitationUnanswered), true)
	c.Respond(api.ResponseAccepted, false)
	l.run()
	if c.Busy() || c.Error() != calendar.ConsentLine(dtEn{}) || c.Card().State != api.InvitationUnanswered {
		t.Errorf("error %q state %s", c.Error(), c.Card().State)
	}
	p, pl, _ := dtSamples(t, false)
	preview := invitation(t, p, pl, loc, sampleCard(t, loc, api.InvitationUnanswered), true)
	preview.Respond(api.ResponseAccepted, false)
	pl.run()
	if preview.Error() != calendar.PreviewOnly(dtEn{}) {
		t.Errorf("preview %q", preview.Error())
	}
}

func TestInvitationCancellationAndShowInCalendar(t *testing.T) {
	s, l, loc := dtSamples(t, true)
	c := invitation(t, s, l, loc, sampleCard(t, loc, api.InvitationCancelled), true)
	c.ApplyCancel()
	l.run()
	if !c.CancelApplied() || c.Busy() {
		t.Error("the cancellation was not applied")
	}
	want := []calendardata.Write{{Method: api.MethodInvitationApplyCancel,
		Params: api.InvitationApplyCancelParams{AccountID: calendar.SampleGmail, MessageID: "msg_sample_demo"}}}
	if !slices.Equal(s.Writes(), want) {
		t.Errorf("writes %+v", s.Writes())
	}
	c.ApplyCancel()
	l.run()
	if len(s.Writes()) != 1 {
		t.Error("applied twice")
	}
	var ids []api.EventID
	var days []*time.Time
	c.OnShowInCalendar = func(id api.EventID, _ api.DateOrTime, day *time.Time) {
		ids, days = append(ids, id), append(days, day)
	}
	c.ShowInCalendar()
	if len(ids) != 1 || ids[0] != "ev_sample_demo" || days[0] == nil || !days[0].Equal(*c.Card().Start) {
		t.Errorf("shown %v %v", ids, days)
	}
	// A reply's one line.
	r := invitation(t, s, l, loc, sampleCard(t, loc, api.InvitationProcessed), true)
	if r.ReplyCard() != "Jana Nováková accepted (applied to your event)" {
		t.Errorf("reply %q", r.ReplyCard())
	}
}

// D29: an answer given elsewhere shows the strip without respondedAt; D32:
// the user's own invitation has no response path but Show in Calendar;
// D30: one line per former value.
func TestInvitationStatesOfTheLiveTest(t *testing.T) {
	s, l, loc := dtSamples(t, true)
	elsewhere := sampleCard(t, loc, api.InvitationUnanswered)
	elsewhere.State, elsewhere.Response, elsewhere.RespondedAt = api.InvitationAnswered, api.ResponseTentative, nil
	elsewhere.CalendarID, elsewhere.EventID = calendar.SamplePersonal, "ev_sample_demo"
	c := invitation(t, s, l, loc, elsewhere, true)
	if c.ShowsResponses() || !c.ShowsStateStrip() || !c.ShowsInCalendar() || c.StateStrip() != "Tentative · in calendar Gmail · Personal" {
		t.Errorf("answered elsewhere: strip %q", c.StateStrip())
	}

	own := sampleCard(t, loc, api.InvitationUnanswered)
	own.OwnEvent, own.State, own.CanRespond, own.Targets = true, api.InvitationProcessed, false, []api.InvitationTarget{}
	own.EventID, own.CalendarID = "ev_sample_demo", calendar.SamplePersonal
	o := invitation(t, s, l, loc, own, true)
	if o.Mode() != calendar.CardFull || o.ShowsResponses() || o.StateLine() != "You organise this event." || !o.ShowsInCalendar() ||
		o.Target() != "" || o.TargetIsPopup() {
		t.Errorf("own event: mode %d line %q target %q", o.Mode(), o.StateLine(), o.Target())
	}
	o.Respond(api.ResponseAccepted, false)
	l.run()
	if len(s.Writes()) != 0 {
		t.Error("the own event answered")
	}

	update := sampleCard(t, loc, api.InvitationUnanswered)
	update.Kind = api.InvitationKindUpdate
	update.Previous = &api.InvitationPrevious{Location: "Room B"}
	if lines := invitation(t, s, l, loc, update, true).PreviousLines(); !slices.Equal(lines, []string{"Previously at Room B"}) {
		t.Errorf("previous %q", lines)
	}
}

// A newer card replaces the shown one and its target; the same card
// changes nothing; nothing is reported after Stop.
func TestInvitationReplaceAndStop(t *testing.T) {
	s, l, loc := dtSamples(t, true)
	card := sampleCard(t, loc, api.InvitationUnanswered)
	c := invitation(t, s, l, loc, card, true)
	changes := 0
	c.OnChange = func() { changes++ }
	c.SetTarget(calendar.SampleFamily)
	c.Replace(card)
	if changes != 1 || c.Target() != calendar.SampleFamily {
		t.Errorf("same card: changes %d target %s", changes, c.Target())
	}
	newer := card
	newer.Title = "Vendor demo (moved)"
	c.Replace(newer)
	if changes != 2 || c.Title() != "Vendor demo (moved)" || c.Target() != calendar.SamplePersonal {
		t.Errorf("newer card: changes %d title %q target %s", changes, c.Title(), c.Target())
	}
	var links []string
	c.OnOpenLink = func(u string) { links = append(links, u) }
	c.JoinVideoCall()
	if card.VideoCall != nil && card.VideoCall.URL != "" && len(links) != 1 {
		t.Errorf("links %v", links)
	}
	c.Stop()
	c.Respond(api.ResponseAccepted, false)
	l.run()
	if changes != 2 {
		t.Errorf("%d changes after Stop", changes)
	}
}
