// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package calsync

import (
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestChangedCoalescing(t *testing.T) {
	clock := newFakeClock(t0)
	note := &fakeNotifier{}
	n := newNotifier(clock, note)
	var told []span
	n.changed = func(_ string, s span) { told = append(told, s) }
	s1, s2 := emptySpan(), emptySpan()
	s1.add(t0.Add(time.Hour), t0.Add(2*time.Hour))
	s2.add(t0.Add(-time.Hour), t0)
	n.calendarChanged("acc_1", []string{"cal_b"}, s1)
	clock.Advance(500 * time.Millisecond)
	n.calendarChanged("acc_1", []string{"cal_a", "cal_b"}, s2)
	n.calendarChanged("acc_2", []string{"cal_c"}, span{unbounded: true})
	n.calendarChanged("acc_3", nil, emptySpan()) // nothing changed: nothing sent
	if len(note.changedList()) != 0 {
		t.Fatalf("sent before a second")
	}
	clock.Advance(500 * time.Millisecond)
	got := note.changedList()
	if len(got) != 1 {
		t.Fatalf("%d notifications after the first second: %+v", len(got), got)
	}
	c := got[0]
	if len(c.CalendarIDs) != 2 || c.CalendarIDs[0] != "cal_a" || c.From == nil || !c.From.Equal(t0.Add(-time.Hour)) || !c.To.Equal(t0.Add(2*time.Hour)) {
		t.Errorf("merged %+v", c)
	}
	clock.Advance(500 * time.Millisecond)
	got = note.changedList()
	if len(got) != 2 || got[1].From != nil || got[1].AccountIDs[0] != "acc_2" {
		t.Errorf("unbounded %+v", got)
	}
	if len(told) != 2 {
		t.Errorf("alert scheduler told %d times", len(told))
	}
	// A notifier without the calendar extension sends nothing and does not fail.
	plain := newNotifier(clock, nil)
	plain.calendarChanged("acc_1", []string{"x"}, s1)
	clock.Advance(time.Second)
	plain.state(api.CalendarSyncState{AccountID: "acc_1"})
}

func TestStateThrottle(t *testing.T) {
	clock := newFakeClock(t0)
	note := &fakeNotifier{}
	n := newNotifier(clock, note)
	st := api.CalendarSyncState{AccountID: "acc_1", Status: api.CalendarSyncSyncing, Progress: 0}
	n.state(st)
	n.state(st) // unchanged: nothing
	for _, p := range []int{10, 20, 30} {
		st.Progress = p
		n.state(st)
	}
	if got := note.stateList(); len(got) != 1 {
		t.Fatalf("%d states before 500 ms", len(got))
	}
	clock.Advance(500 * time.Millisecond)
	got := note.stateList()
	if len(got) != 2 || got[1].Progress != 30 {
		t.Fatalf("throttled %+v", got)
	}
	// A status change goes at once.
	st.Status, st.Progress = api.CalendarSyncIdle, -1
	n.state(st)
	if got := note.stateList(); len(got) != 3 || got[2].Status != api.CalendarSyncIdle {
		t.Errorf("status change %+v", got)
	}
}
