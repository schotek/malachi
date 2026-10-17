// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package calsync

import (
	"sort"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Notifications of the engine (docs/calendar.md §4.7): notify.calendarChanged
// gathered per account within changeCoalesce, notify.calendarState on every
// state change with progress at most every progressEvery, notify.eventAlert
// once per state change. They go through api.CalendarNotifier, the optional
// extension of the core's Notifier; a Notifier without it gets none of them.

const (
	changeCoalesce = time.Second
	progressEvery  = 500 * time.Millisecond
)

// Clock is the engine's time: Now and one-shot timers. Tests use a fake;
// nil options get the real one.
type Clock interface {
	Now() time.Time
	// AfterFunc calls f in its own goroutine (or the fake's Advance) after
	// d; stop cancels it and reports whether it did.
	AfterFunc(d time.Duration, f func()) (stop func() bool)
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
func (realClock) AfterFunc(d time.Duration, f func()) func() bool {
	return time.AfterFunc(d, f).Stop
}

// notifier sends the calendar notifications.
type notifier struct {
	clock Clock
	base  api.Notifier
	cal   api.CalendarNotifier // nil = none
	// changed is told of every change flushed (the alert scheduler).
	changed func(accountID string, s span)

	mu      sync.Mutex
	pending map[string]*changeBatch
	states  map[string]*stateThrottle
}

type changeBatch struct {
	calendars map[string]bool
	span      span
	stop      func() bool
}

type stateThrottle struct {
	last    api.CalendarSyncState
	sentAt  time.Time
	pending *api.CalendarSyncState
	stop    func() bool
}

func newNotifier(clock Clock, base api.Notifier) *notifier {
	n := &notifier{clock: clock, base: base, pending: map[string]*changeBatch{}, states: map[string]*stateThrottle{}}
	if cn, ok := base.(api.CalendarNotifier); ok {
		n.cal = cn
	}
	return n
}

// calendarChanged gathers a change of an account's calendars (none = the
// account's calendars as a whole) within s; it is sent changeCoalesce
// after the first change of the batch.
func (n *notifier) calendarChanged(accountID string, calendarIDs []string, s span) {
	if s.empty && len(calendarIDs) == 0 {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	b := n.pending[accountID]
	if b == nil {
		b = &changeBatch{calendars: map[string]bool{}, span: emptySpan()}
		n.pending[accountID] = b
		b.stop = n.clock.AfterFunc(changeCoalesce, func() { n.flushChange(accountID) })
	}
	for _, id := range calendarIDs {
		b.calendars[id] = true
	}
	if s.empty {
		s = span{unbounded: true}
	}
	b.span.merge(s)
}

func (n *notifier) flushChange(accountID string) {
	n.mu.Lock()
	b := n.pending[accountID]
	delete(n.pending, accountID)
	n.mu.Unlock()
	if b == nil {
		return
	}
	if n.changed != nil {
		n.changed(accountID, b.span)
	}
	if n.cal == nil {
		return
	}
	note := api.CalendarChangedNotification{AccountIDs: []api.AccountID{api.AccountID(accountID)}}
	ids := make([]string, 0, len(b.calendars))
	for id := range b.calendars {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		note.CalendarIDs = append(note.CalendarIDs, api.CalendarID(id))
	}
	if !b.span.unbounded && !b.span.empty {
		f, t := b.span.from.UTC(), b.span.to.UTC()
		note.From, note.To = &f, &t
	}
	n.cal.CalendarChanged(note)
}

// flushAll sends every gathered change now (Stop of the supervisor).
func (n *notifier) flushAll() {
	n.mu.Lock()
	var ids []string
	for id, b := range n.pending {
		if b.stop != nil {
			b.stop()
		}
		ids = append(ids, id)
	}
	n.mu.Unlock()
	sort.Strings(ids)
	for _, id := range ids {
		n.flushChange(id)
	}
}

// state sends a changed calendar state: at once when anything but the
// progress differs, else at most every progressEvery (the latest wins).
func (n *notifier) state(st api.CalendarSyncState) {
	n.mu.Lock()
	t := n.states[string(st.AccountID)]
	if t == nil {
		t = &stateThrottle{}
		n.states[string(st.AccountID)] = t
	} else if sameCalendarState(t.last, st) && t.pending == nil {
		n.mu.Unlock()
		return
	}
	now := n.clock.Now()
	progressOnly := !t.sentAt.IsZero() && sameCalendarState(withProgress(t.last, st.Progress), st)
	if progressOnly && now.Sub(t.sentAt) < progressEvery {
		cp := st
		t.pending = &cp
		if t.stop == nil {
			t.stop = n.clock.AfterFunc(progressEvery-now.Sub(t.sentAt), func() { n.flushState(st.AccountID) })
		}
		n.mu.Unlock()
		return
	}
	if t.stop != nil {
		t.stop()
		t.stop = nil
	}
	t.pending = nil
	t.last, t.sentAt = st, now
	n.mu.Unlock()
	n.sendState(st)
}

func (n *notifier) flushState(accountID api.AccountID) {
	n.mu.Lock()
	t := n.states[string(accountID)]
	if t == nil || t.pending == nil {
		if t != nil {
			t.stop = nil
		}
		n.mu.Unlock()
		return
	}
	st := *t.pending
	t.pending, t.stop = nil, nil
	t.last, t.sentAt = st, n.clock.Now()
	n.mu.Unlock()
	n.sendState(st)
}

// forget drops the account's throttle (its syncer stopped).
func (n *notifier) forget(accountID string) {
	n.mu.Lock()
	if t := n.states[accountID]; t != nil && t.stop != nil {
		t.stop()
	}
	delete(n.states, accountID)
	n.mu.Unlock()
}

func (n *notifier) sendState(st api.CalendarSyncState) {
	if n.cal != nil {
		n.cal.CalendarState(api.CalendarStateNotification{State: st})
	}
}

func (n *notifier) alert(a api.EventAlert) {
	if n.cal != nil {
		n.cal.EventAlert(api.EventAlertNotification{Alert: a})
	}
}

func withProgress(st api.CalendarSyncState, p int) api.CalendarSyncState {
	st.Progress = p
	return st
}

func sameCalendarState(a, b api.CalendarSyncState) bool {
	if a.AccountID != b.AccountID || a.Status != b.Status || a.Progress != b.Progress {
		return false
	}
	if (a.LastSync == nil) != (b.LastSync == nil) || (a.LastSync != nil && !a.LastSync.Equal(*b.LastSync)) {
		return false
	}
	if (a.Error == nil) != (b.Error == nil) {
		return false
	}
	return a.Error == nil || (a.Error.Code == b.Error.Code && a.Error.Message == b.Error.Message)
}

// MailState maps a calendar state onto the mail statuses (a caldav
// account's SyncState, docs/calendar.md §4.3).
func MailState(st api.CalendarSyncState) api.SyncState {
	out := api.SyncState{AccountID: st.AccountID, Progress: st.Progress, LastSync: st.LastSync, Error: st.Error}
	switch st.Status {
	case api.CalendarSyncSyncing:
		out.Status = api.SyncSyncing
	case api.CalendarSyncOffline:
		out.Status = api.SyncOffline
	case api.CalendarSyncAuthRequired, api.CalendarSyncConsentRequired:
		out.Status = api.SyncAuthRequired
	case api.CalendarSyncError:
		out.Status = api.SyncError
	case api.CalendarSyncDisabled:
		out.Status = api.SyncDisabled
	default:
		out.Status = api.SyncIdle
	}
	return out
}
