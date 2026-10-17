// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendarctl

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/calendar"
)

// The alerts (CalendarAlertsControllerTests of the macOS client): due
// presents, dismissed from elsewhere withdraws, Snooze and a dismissal
// write and withdraw, the notification preference; and the invitation
// and cancellation notifications of a new message.

func g8Alert(state api.EventAlertState, video bool) api.EventAlert {
	start, end := g8Now.Add(10*time.Minute), g8Now.Add(time.Hour)
	return api.EventAlert{AlertID: "al_1", State: state, EventID: "ev_sample_planning", AccountID: calendar.SampleWork,
		CalendarID: calendar.SampleWorkCalendar, Title: "Q4 planning", Start: &start, End: &end, Location: "Room A",
		VideoCall: video, MinutesBefore: 10}
}

func g8AlertRaw(t *testing.T, a api.EventAlert) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(api.EventAlertNotification{Alert: a})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestAlertsDueDismissAndSnooze(t *testing.T) {
	s, l := g8Samples(true)
	a := NewCalendarAlertsController(s, g8Opts(l))
	var presented []AlertRequest
	var withdrawn []api.AlertID
	a.OnPresent = func(r AlertRequest) { presented = append(presented, r) }
	a.OnWithdraw = func(id api.AlertID) { withdrawn = append(withdrawn, id) }
	if !a.Handle(api.NotifyEventAlert, g8AlertRaw(t, g8Alert(api.AlertDue, true))) {
		t.Fatal("not the calendar's")
	}
	if len(presented) != 1 || !presented[0].Join || presented[0].Title != "Q4 planning" ||
		presented[0].Category() != CategoryEventAlertJoin {
		t.Fatalf("presented %+v", presented)
	}
	if want := calendar.AlertNotification(g8Alert(api.AlertDue, true), g8Now, g8Env).Body; presented[0].Body != want ||
		want != "in 10 minutes · Room A" {
		t.Fatalf("body %q", presented[0].Body)
	}
	if y, m, d := presented[0].Day.Date(); y != 2026 || m != time.October || d != 14 {
		t.Fatalf("day %v", presented[0].Day)
	}
	if _, ok := a.Shown()["al_1"]; !ok {
		t.Fatal("not shown")
	}
	// Another client dismissed it.
	a.HandleAlert(g8Alert(api.AlertDismissed, true))
	if !slices.Equal(withdrawn, []api.AlertID{"al_1"}) || len(a.Shown()) != 0 {
		t.Fatalf("withdrawn %v", withdrawn)
	}
	// A dismissal of an alert not shown withdraws nothing.
	a.HandleAlert(g8Alert(api.AlertDismissed, true))
	if len(withdrawn) != 1 {
		t.Fatal("withdrew again")
	}
	a.HandleAlert(g8Alert(api.AlertDue, true))
	a.Snooze("al_1")
	if len(withdrawn) != 2 {
		t.Fatal("snooze did not withdraw")
	}
	l.run()
	if ws := s.Writes(); len(ws) != 1 || ws[0].Params != (api.EventSnoozeAlertParams{AlertID: "al_1", Minutes: 5}) {
		t.Fatalf("writes %+v", ws)
	}
	a.HandleAlert(g8Alert(api.AlertDue, true))
	a.Dismiss("al_1")
	l.run()
	if ws := s.Writes(); len(ws) != 2 || ws[1].Params != (api.EventDismissAlertParams{AlertID: "al_1"}) {
		t.Fatalf("writes %+v", ws)
	}
	// Other calendar notifications are the calendar's but present nothing;
	// mail's are not the calendar's.
	if !a.Handle(api.NotifyInvitationsChanged, json.RawMessage(`{}`)) || a.Handle(api.NotifyNewMessage, json.RawMessage(`{}`)) {
		t.Fatal("routing")
	}
	// A malformed alert is ignored.
	a.Handle(api.NotifyEventAlert, json.RawMessage(`{"alert":7}`))
	if len(presented) != 3 {
		t.Fatalf("presented %d", len(presented))
	}
}

func TestAlertsWithoutVideoAllDayAndFailures(t *testing.T) {
	s, l := g8Samples(false)
	a := NewCalendarAlertsController(s, g8Opts(l))
	var got AlertRequest
	a.OnPresent = func(r AlertRequest) { got = r }
	var errs []string
	a.OnError = func(e string) { errs = append(errs, e) }
	al := g8Alert(api.AlertDue, false)
	al.Start, al.End, al.AllDay, al.StartDate = nil, nil, true, "2026-10-14"
	a.HandleAlert(al)
	if got.Join || got.Category() != CategoryEventAlert || got.Body != "today · Room A" {
		t.Fatalf("request %+v", got)
	}
	if y, m, d := got.Day.Date(); y != 2026 || m != time.October || d != 14 {
		t.Fatalf("day %v", got.Day)
	}
	// The preview cannot snooze: a log line.
	a.Snooze("al_1")
	l.run()
	if !slices.Equal(errs, []string{calendar.PreviewOnly(g8En{})}) {
		t.Fatalf("errors %v", errs)
	}
}

func TestAlertsNotificationsOffPresentNothing(t *testing.T) {
	s, l := g8Samples(true)
	a := NewCalendarAlertsController(s, g8Opts(l))
	a.Enabled = func() bool { return false }
	presented := 0
	a.OnPresent = func(AlertRequest) { presented++ }
	a.HandleAlert(g8Alert(api.AlertDue, false))
	if presented != 0 || len(a.Shown()) != 0 {
		t.Fatal("presented with notifications off")
	}
}

func TestAlertsInvitationNotifications(t *testing.T) {
	start := g8Now.Add(24 * time.Hour)
	end := start.Add(90 * time.Minute)
	jana := &api.Address{Name: "Jana Nováková", Address: "jana@example.org"}
	inv := api.InvitationSummary{Method: api.InvitationMethodRequest, Kind: api.InvitationKindInvitation,
		State: api.InvitationUnanswered, Title: "Q4 planning", Start: &start, End: &end, Organizer: jana}
	n, ok := InvitationNotificationOf(&inv, g8Env)
	if !ok || n.Category != CategoryInvitation || n.Title != "New invitation from Jana Nováková" ||
		n.Body != calendar.InvitationNotification(inv, g8Env).Body {
		t.Fatalf("invitation %+v", n)
	}
	inv.State = api.InvitationAnswered
	if n, _ := InvitationNotificationOf(&inv, g8Env); n.Category != CategoryNone {
		t.Fatal("an answered invitation offers Accept")
	}
	inv.Kind, inv.State = api.InvitationKindUpdate, api.InvitationUnanswered
	if n, _ := InvitationNotificationOf(&inv, g8Env); n.Title != "Jana Nováková updated the event" || n.Category != CategoryInvitation {
		t.Fatalf("update %+v", n)
	}
	inv.Kind, inv.State = api.InvitationKindCancelled, api.InvitationCancelled
	n, ok = InvitationNotificationOf(&inv, g8Env)
	if !ok || n.Category != CategoryCancellation || n.Title != "Jana Nováková cancelled the event" {
		t.Fatalf("cancellation %+v", n)
	}
	inv.State = api.InvitationProcessed
	if n, _ := InvitationNotificationOf(&inv, g8Env); n.Category != CategoryNone {
		t.Fatal("a processed cancellation offers Remove")
	}
	inv.Kind = api.InvitationKindReply
	if _, ok := InvitationNotificationOf(&inv, g8Env); ok {
		t.Fatal("a reply retitles the notification")
	}
	if _, ok := InvitationNotificationOf(nil, g8Env); ok {
		t.Fatal("no invitation")
	}
}
