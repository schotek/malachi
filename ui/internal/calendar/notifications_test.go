// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestAlertNotification(t *testing.T) {
	loc := prague(t)
	env := envIn(loc)
	now := at(loc, 2026, 10, 14, 9, 50)
	in := func(d time.Duration) *time.Time { t := now.Add(d).UTC(); return &t }
	tests := []struct {
		a    api.EventAlert
		want Notification
	}{
		{api.EventAlert{Title: "Standup", Start: in(10 * time.Minute), Location: "Room B"}, Notification{"Standup", "in 10 minutes · Room B"}},
		{api.EventAlert{Title: "Standup", Start: in(time.Minute)}, Notification{"Standup", "in 1 minute"}},
		{api.EventAlert{Title: "Standup", Start: in(20 * time.Second)}, Notification{"Standup", "now"}},
		{api.EventAlert{Title: "Standup", Start: in(-5 * time.Minute)}, Notification{"Standup", "now"}},
		{api.EventAlert{Title: "Lunch", Start: in(2*time.Hour + 10*time.Minute)}, Notification{"Lunch", "at 12:00"}},
		{api.EventAlert{Title: "Trip", Start: in(24 * time.Hour)}, Notification{"Trip", "Thursday 15 October · 09:50"}},
		{api.EventAlert{Title: "Offsite", AllDay: true, StartDate: "2026-10-14"}, Notification{"Offsite", "today"}},
		{api.EventAlert{Title: "Offsite", AllDay: true, StartDate: "2026-10-15", Location: "Hotel"}, Notification{"Offsite", "Thursday 15 October · Hotel"}},
		{api.EventAlert{}, Notification{"Untitled Event", ""}},
	}
	for _, tt := range tests {
		if got := AlertNotification(tt.a, now, env); got != tt.want {
			t.Errorf("AlertNotification(%s) = %+v, want %+v", tt.a.Title, got, tt.want)
		}
	}
}

func TestInvitationNotifications(t *testing.T) {
	loc := prague(t)
	env := envIn(loc)
	s := at(loc, 2026, 10, 14, 10, 0).UTC()
	e := s.Add(time.Hour)
	jana := &api.Address{Name: "Jana Nováková", Address: "jana@example.org"}
	sum := api.InvitationSummary{Kind: api.InvitationKindInvitation, Title: "Q4 planning", Start: &s, End: &e, Organizer: jana}
	if got := InvitationNotification(sum, env); got != (Notification{"New invitation from Jana Nováková",
		"Q4 planning · Wednesday 14 October 2026 · 10:00–11:00 · 1 h"}) {
		t.Errorf("invitation %+v", got)
	}
	sum.Kind = api.InvitationKindUpdate
	if got := InvitationNotification(sum, env); got.Title != "Jana Nováková updated the event" {
		t.Errorf("update %+v", got)
	}
	sum.Organizer = nil
	if got := InvitationNotification(sum, env); got.Title != "An event was updated" {
		t.Errorf("update without organiser %+v", got)
	}
	sum.Kind = api.InvitationKindInvitation
	if got := InvitationNotification(sum, env); got.Title != "New invitation" {
		t.Errorf("invitation without organiser %+v", got)
	}
	ad := api.InvitationSummary{Title: "Offsite", AllDay: true, StartDate: "2026-10-15", Organizer: jana}
	if got := CancellationNotification(ad, env); got != (Notification{"Jana Nováková cancelled the event",
		"Offsite · Thursday 15 October 2026 · All Day"}) {
		t.Errorf("cancellation %+v", got)
	}
	if got := CancellationNotification(api.InvitationSummary{}, env); got != (Notification{"An event was cancelled", "Untitled Event"}) {
		t.Errorf("bare cancellation %+v", got)
	}
}
