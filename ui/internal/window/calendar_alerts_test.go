// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"strings"
	"testing"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/calendarctl"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// An alert's notification carries its ids through every action's target
// and back (display-free: variants and the note model only).
func TestCalendarAlertNotificationTargetsRoundTrip(t *testing.T) {
	day := time.Date(2026, 10, 14, 10, 0, 0, 0, time.Local)
	r := calendarctl.AlertRequest{AlertID: "al_1", Title: "Q4 planning", Body: "in 5 minutes · Room B",
		EventID: "ev_1", RecurrenceID: "2026-10-14T08:00:00Z", AccountID: "acc_1", Day: day}
	want := CalendarAlertTarget{AlertID: "al_1", AccountID: "acc_1", EventID: "ev_1",
		RecurrenceID: "2026-10-14T08:00:00Z", Day: day}

	n := alertNote(r, i18n.Tr)
	if n.Title != "Q4 planning" || n.Body != "in 5 minutes · Room B" {
		t.Fatalf("text: %q / %q", n.Title, n.Body)
	}
	if n.Default.Action != "app."+CalendarOpenEventAction {
		t.Fatalf("default action %q", n.Default.Action)
	}
	if len(n.Buttons) != 1 || n.Buttons[0].Action != "app."+CalendarSnoozeAction || n.Buttons[0].Label != "Snooze" {
		t.Fatalf("buttons without a video call: %+v", n.Buttons)
	}
	for _, b := range append([]notificationButton{n.Default}, n.Buttons...) {
		got, ok := ParseCalendarAlertTarget(b.Target)
		if !ok || !got.Day.Equal(want.Day) {
			t.Fatalf("%s: %+v %v", b.Action, got, ok)
		}
		got.Day = want.Day
		if got != want {
			t.Fatalf("%s: %+v, want %+v", b.Action, got, want)
		}
	}

	r.Join = true
	n = alertNote(r, i18n.Tr)
	if len(n.Buttons) != 2 || n.Buttons[0].Action != "app."+CalendarJoinAction || n.Buttons[0].Label != "Join" ||
		n.Buttons[1].Action != "app."+CalendarSnoozeAction {
		t.Fatalf("buttons with a video call: %+v", n.Buttons)
	}

	// No instance and no day: both come back empty.
	r.RecurrenceID, r.Day = "", time.Time{}
	got, ok := ParseCalendarAlertTarget(alertNote(r, i18n.Tr).Default.Target)
	if !ok || got.RecurrenceID != "" || !got.Day.IsZero() {
		t.Fatalf("bare target: %+v %v", got, ok)
	}
}

// A target of another type or without the alert or the event is refused.
func TestCalendarAlertTargetRefusesForeignParameters(t *testing.T) {
	if _, ok := ParseCalendarAlertTarget(nil); ok {
		t.Fatal("nil accepted")
	}
	if _, ok := ParseCalendarAlertTarget(NotificationTarget("acc_1", "m_1")); ok {
		t.Fatal("a message target accepted")
	}
	if _, ok := ParseCalendarAlertTarget(glib.NewVariantString("al_1")); ok {
		t.Fatal("a string accepted")
	}
	for _, c := range []CalendarAlertTarget{{EventID: "ev_1"}, {AlertID: "al_1"}} {
		if _, ok := ParseCalendarAlertTarget(c.Variant()); ok {
			t.Fatalf("%+v accepted", c)
		}
	}
}

// A new invitation's notification offers Accept and Show, a
// cancellation's Remove from Calendar, each with the message's ids.
func TestInvitationNotificationButtonsRoundTrip(t *testing.T) {
	cases := []struct {
		c       calendarctl.NotificationCategory
		actions []string
	}{
		{calendarctl.CategoryInvitation, []string{"app." + CalendarAcceptInvitationAction, "app." + OpenMessageAction}},
		{calendarctl.CategoryCancellation, []string{"app." + CalendarRemoveCancelledAction}},
		{calendarctl.CategoryNone, nil},
	}
	for _, c := range cases {
		bs := invitationButtons(c.c, "acc_1", "m_1", i18n.Tr)
		if len(bs) != len(c.actions) {
			t.Fatalf("%v: %+v", c.c, bs)
		}
		for i, b := range bs {
			if b.Action != c.actions[i] || b.Label == "" {
				t.Fatalf("%v: %+v", c.c, b)
			}
			acc, id, ok := ParseNotificationTarget(b.Target)
			if !ok || acc != api.AccountID("acc_1") || id != api.MessageID("m_1") {
				t.Fatalf("%v: %q %q %v", c.c, acc, id, ok)
			}
		}
	}
	if bs := invitationButtons(calendarctl.CategoryInvitation, "a", "m", i18n.Tr); bs[0].Label != "Accept" ||
		bs[1].Label != "Show" {
		t.Fatalf("labels %q %q", bs[0].Label, bs[1].Label)
	}
}

// Every action a notification names is one CalendarNotificationActions
// registers, with the parameter type the notification's target has.
func TestCalendarNotificationActionsMatchTheTargets(t *testing.T) {
	types := map[string]string{}
	for _, a := range CalendarNotificationActions() {
		types["app."+a.Name] = a.Type.DupString()
	}
	r := calendarctl.AlertRequest{AlertID: "al_1", EventID: "ev_1", Join: true}
	n := alertNote(r, i18n.Tr)
	bs := append([]notificationButton{n.Default}, n.Buttons...)
	bs = append(bs, invitationButtons(calendarctl.CategoryInvitation, "a", "m", i18n.Tr)[0],
		invitationButtons(calendarctl.CategoryCancellation, "a", "m", i18n.Tr)[0])
	for _, b := range bs {
		typ, ok := types[b.Action]
		if !ok {
			t.Fatalf("%s is not registered", b.Action)
		}
		if got := b.Target.TypeString(); got != typ {
			t.Fatalf("%s: target %s, action %s", b.Action, got, typ)
		}
	}
	if len(types) != 5 {
		t.Fatalf("actions: %v", types)
	}
	if !strings.HasPrefix(alertNotificationID("al_1"), "alert-") {
		t.Fatal("alert id")
	}
}
