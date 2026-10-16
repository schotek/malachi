// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"testing"
	"time"
)

func TestAlerts(t *testing.T) {
	ctx := context.Background()
	s, _, _, ids := rangeFixture(t)
	ev := ids["timed"]
	due := time.Date(2026, 10, 14, 9, 45, 0, 0, time.UTC)
	got, err := s.SyncAlerts(ctx, []string{ev}, []Alert{{EventID: ev, MinutesBefore: 15, DueAt: due}, {EventID: ev, MinutesBefore: 60, DueAt: due.Add(-45 * time.Minute)}})
	if err != nil || len(got) != 2 || got[0].State != AlertPending {
		t.Fatalf("first: %+v %v", got, err)
	}
	if next, ok, err := s.NextAlert(ctx); err != nil || !ok || !next.Equal(due.Add(-45*time.Minute)) {
		t.Fatalf("next: %v %v %v", next, ok, err)
	}
	if d, _ := s.DueAlerts(ctx, due.Add(-time.Minute)); len(d) != 1 || d[0].MinutesBefore != 60 {
		t.Fatalf("due: %+v", d)
	}
	if err := s.FireAlert(ctx, got[1].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.FireAlert(ctx, got[1].ID); err != ErrConflict {
		t.Fatalf("fire twice: %v", err)
	}
	if err := s.SnoozeAlert(ctx, got[0].ID, due); err != ErrConflict {
		t.Fatalf("snooze pending: %v", err)
	}
	snooze := due.Add(5 * time.Minute)
	if err := s.SnoozeAlert(ctx, got[1].ID, snooze); err != nil {
		t.Fatal(err)
	}
	// A recompute that keeps the 60-minute alert keeps its state and snooze;
	// the 15-minute one moved (the occurrence did) and starts over.
	again, err := s.SyncAlerts(ctx, []string{ev}, []Alert{{EventID: ev, MinutesBefore: 15, DueAt: due.Add(time.Hour)}, {EventID: ev, MinutesBefore: 60, DueAt: due.Add(-45 * time.Minute)}})
	if err != nil {
		t.Fatal(err)
	}
	if again[1].ID != got[1].ID || !again[1].SnoozedUntil.Equal(snooze) || again[0].ID == got[0].ID || again[0].State != AlertPending {
		t.Fatalf("recompute: %+v", again)
	}
	if a, _ := s.GetAlert(ctx, got[1].ID); !a.When().Equal(snooze) {
		t.Fatalf("when: %+v", a)
	}
	if err := s.DismissAlert(ctx, again[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DismissAlert(ctx, "al_none"); err != ErrNotFound {
		t.Fatalf("unknown: %v", err)
	}
	if _, err := s.SyncAlerts(ctx, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.NextAlert(ctx); ok {
		t.Fatal("alerts left")
	}
}
