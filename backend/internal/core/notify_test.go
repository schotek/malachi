// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/pkg/api"
)

// fakeClock is a manual clock whose timers fire on advance.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	at      time.Time
	f       func()
	stopped bool
}

func (t *fakeTimer) Stop() bool {
	was := t.stopped
	t.stopped = true
	return !was
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) AfterFunc(d time.Duration, f func()) stopper {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{at: c.now.Add(d), f: f}
	c.timers = append(c.timers, t)
	return t
}

// advance moves the clock and fires due timers outside the clock lock.
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	var due []*fakeTimer
	var rest []*fakeTimer
	for _, t := range c.timers {
		if !t.stopped && !t.at.After(c.now) {
			due = append(due, t)
		} else if !t.stopped {
			rest = append(rest, t)
		}
	}
	c.timers = rest
	c.mu.Unlock()
	for _, t := range due {
		t.f()
	}
}

// recorder collects delivered notifications.
type recorder struct {
	mu     sync.Mutex
	states []api.SyncState
	events []string
}

func (r *recorder) NewMessage(n api.NewMessageNotification) {
	r.mu.Lock()
	r.events = append(r.events, "newMessage:"+string(n.Message.ID))
	r.mu.Unlock()
}
func (r *recorder) SyncState(n api.SyncStateNotification) {
	r.mu.Lock()
	r.states = append(r.states, n.State)
	r.events = append(r.events, "syncState:"+string(n.State.AccountID))
	r.mu.Unlock()
}
func (r *recorder) AuthRequired(n api.AuthRequiredNotification) {
	r.mu.Lock()
	r.events = append(r.events, "authRequired:"+string(n.AccountID))
	r.mu.Unlock()
}
func (r *recorder) AccountsChanged(api.AccountsChangedNotification) {
	r.mu.Lock()
	r.events = append(r.events, "accountsChanged")
	r.mu.Unlock()
}

func (r *recorder) snapshot() ([]api.SyncState, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]api.SyncState(nil), r.states...), append([]string(nil), r.events...)
}

// waitFor polls cond until it holds or two seconds pass.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

// settle gives the sender goroutine a moment to deliver anything queued.
func settle() { time.Sleep(30 * time.Millisecond) }

func newTestCoalescer(t *testing.T) (*coalescingNotifier, *recorder, *fakeClock) {
	t.Helper()
	r := &recorder{}
	clk := newFakeClock()
	c := newCoalescingNotifier(r, nil, clk)
	t.Cleanup(c.Close)
	return c, r, clk
}

func syncing(acc string, progress int) api.SyncStateNotification {
	return api.SyncStateNotification{State: api.SyncState{AccountID: api.AccountID(acc), Status: api.SyncSyncing, FolderID: "f_1", Progress: progress}}
}

func TestCoalescerStatusChangeIsImmediate(t *testing.T) {
	c, r, _ := newTestCoalescer(t)
	when := time.Now()
	// Each change is significant and goes out at once, with no clock
	// advance in between. The sender is latest-wins per account, so wait for
	// every delivery before sending the next.
	steps := []api.SyncState{
		{AccountID: "a", Status: api.SyncSyncing, FolderID: "f_1", Progress: 0},
		{AccountID: "a", Status: api.SyncIdle, Progress: -1},
		{AccountID: "a", Status: api.SyncIdle, Progress: -1, LastSync: &when},
		{AccountID: "a", Status: api.SyncError, Progress: -1, LastSync: &when, Error: api.NewError(api.CodeServerError, "x")},
		{AccountID: "a", Status: api.SyncError, Progress: -1, LastSync: &when, Error: api.NewError(api.CodeServerError, "x"), PendingOutbox: 1},
	}
	for i, st := range steps {
		c.SyncState(api.SyncStateNotification{State: st})
		waitFor(t, "state delivered", func() bool { s, _ := r.snapshot(); return len(s) >= i+1 })
	}
	// Identical state: nothing new.
	c.SyncState(api.SyncStateNotification{State: steps[4]})
	settle()
	states, _ := r.snapshot()
	if len(states) != 5 {
		t.Fatalf("delivered %d states: %+v", len(states), states)
	}
	want := []api.SyncStatus{api.SyncSyncing, api.SyncIdle, api.SyncIdle, api.SyncError, api.SyncError}
	for i, w := range want {
		if states[i].Status != w {
			t.Errorf("states[%d].Status = %s, want %s", i, states[i].Status, w)
		}
	}
	if states[2].LastSync == nil || states[4].PendingOutbox != 1 {
		t.Fatalf("lastSync/pendingOutbox changes not delivered: %+v", states)
	}
}

func TestCoalescerThrottlesProgress(t *testing.T) {
	c, r, clk := newTestCoalescer(t)
	for i := 0; i < 10; i++ {
		c.SyncState(syncing("a", i*10))
		clk.advance(10 * time.Millisecond)
	}
	waitFor(t, "first state", func() bool { s, _ := r.snapshot(); return len(s) >= 1 })
	settle()
	if states, _ := r.snapshot(); len(states) != 1 || states[0].Progress != 0 {
		t.Fatalf("before the interval: %+v", states)
	}

	clk.advance(400 * time.Millisecond) // t = 500 ms: the trailing timer fires
	waitFor(t, "trailing state", func() bool { s, _ := r.snapshot(); return len(s) >= 2 })
	settle()
	states, _ := r.snapshot()
	if len(states) != 2 || states[1].Progress != 90 {
		t.Fatalf("after the interval: %+v", states)
	}

	// Well after the window a progress update goes out at once.
	clk.advance(time.Second)
	c.SyncState(syncing("a", 95))
	waitFor(t, "immediate progress", func() bool { s, _ := r.snapshot(); return len(s) >= 3 })

	// A status change during a throttle window is immediate and supersedes
	// the held progress value.
	c.SyncState(syncing("a", 96))
	c.SyncState(api.SyncStateNotification{State: api.SyncState{AccountID: "a", Status: api.SyncIdle, Progress: -1}})
	waitFor(t, "status change", func() bool { s, _ := r.snapshot(); return len(s) >= 4 })
	clk.advance(time.Second)
	settle()
	states, _ = r.snapshot()
	if len(states) != 4 || states[3].Status != api.SyncIdle {
		t.Fatalf("held progress leaked after status change: %+v", states)
	}
}

func TestCoalescerAccountsAreIndependent(t *testing.T) {
	c, r, clk := newTestCoalescer(t)
	c.SyncState(syncing("a", 0))
	c.SyncState(syncing("a", 10))
	clk.advance(100 * time.Millisecond)
	c.SyncState(syncing("b", 0)) // first state of b: immediate despite a's window
	c.SyncState(syncing("b", 50))
	waitFor(t, "two accounts", func() bool { s, _ := r.snapshot(); return len(s) >= 2 })
	settle()
	states, _ := r.snapshot()
	if len(states) != 2 || states[0].AccountID != "a" || states[1].AccountID != "b" {
		t.Fatalf("first states: %+v", states)
	}
	clk.advance(400 * time.Millisecond) // a's timer
	waitFor(t, "a trailing", func() bool { s, _ := r.snapshot(); return len(s) >= 3 })
	clk.advance(100 * time.Millisecond) // b's timer
	waitFor(t, "b trailing", func() bool { s, _ := r.snapshot(); return len(s) >= 4 })
	settle()
	states, _ = r.snapshot()
	if len(states) != 4 || states[2].AccountID != "a" || states[2].Progress != 10 || states[3].AccountID != "b" || states[3].Progress != 50 {
		t.Fatalf("trailing states: %+v", states)
	}
}

func TestCoalescerPassesOtherEventsInOrder(t *testing.T) {
	c, r, _ := newTestCoalescer(t)
	c.NewMessage(api.NewMessageNotification{AccountID: "a", Message: api.MessageSummary{ID: "m_1"}})
	c.AuthRequired(api.AuthRequiredNotification{AccountID: "a", Reason: api.CodeAuthFailed})
	c.AccountsChanged(api.AccountsChangedNotification{})
	c.NewMessage(api.NewMessageNotification{AccountID: "a", Message: api.MessageSummary{ID: "m_2"}})
	waitFor(t, "4 events", func() bool { _, e := r.snapshot(); return len(e) >= 4 })
	_, events := r.snapshot()
	want := []string{"newMessage:m_1", "authRequired:a", "accountsChanged", "newMessage:m_2"}
	for i, w := range want {
		if events[i] != w {
			t.Fatalf("events = %v, want %v", events, want)
		}
	}
}

func TestSyncNotifierIsNilSafeAndForwards(t *testing.T) {
	b := newTestBackend(t, config.Default())
	n := b.SyncNotifier()
	// No notifier installed yet: events are dropped, nothing panics.
	n.NewMessage(api.NewMessageNotification{AccountID: "a"})
	n.SyncState(api.SyncStateNotification{State: api.SyncState{AccountID: "a", Status: api.SyncSyncing}})
	n.AuthRequired(api.AuthRequiredNotification{AccountID: "a"})
	n.AccountsChanged(api.AccountsChangedNotification{})
	settle()

	r := &recorder{}
	b.SetNotifier(r)
	n.NewMessage(api.NewMessageNotification{AccountID: "a", Message: api.MessageSummary{ID: "m_1"}})
	n.SyncState(api.SyncStateNotification{State: api.SyncState{AccountID: "a", Status: api.SyncIdle}})
	waitFor(t, "forwarded events", func() bool { _, e := r.snapshot(); return len(e) >= 2 })
	_, events := r.snapshot()
	if events[0] != "newMessage:m_1" || events[1] != "syncState:a" {
		t.Fatalf("events = %v", events)
	}
}
