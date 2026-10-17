// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendardata

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/client"
)

// The calendar from the daemon against a fake daemon
// (DaemonCalendarSourceTests.swift, the same cases, and the rules the
// Swift tests do not reach): the snapshot's calls and params, one load at
// a time, the retries, the range cache and its invalidation by
// notify.calendarChanged, the event cache and a conflict's reload, a
// consentRequired state, the optimistic visibility and its revert, an old
// daemon. The fake daemon holds a method's answers until the test
// releases them, timers wait until the test fires them, and "nothing
// happened" is checked once the source is idle.

// rpcLoop is the main loop of a test: what is posted (from any goroutine)
// runs on the test's goroutine while it waits; a timer waits until fire.
type rpcLoop struct {
	mu     sync.Mutex
	queue  []func()
	timers []rpcTimer
	wake   chan struct{}
}

type rpcTimer struct {
	d time.Duration
	f func()
}

func newRPCLoop() *rpcLoop { return &rpcLoop{wake: make(chan struct{}, 1)} }

func (l *rpcLoop) Post(f func()) {
	l.mu.Lock()
	l.queue = append(l.queue, f)
	l.mu.Unlock()
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

func (l *rpcLoop) After(d time.Duration, f func()) {
	l.mu.Lock()
	l.timers = append(l.timers, rpcTimer{d, f})
	l.mu.Unlock()
}

func (l *rpcLoop) pending() []time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := []time.Duration{}
	for _, t := range l.timers {
		out = append(out, t.d)
	}
	return out
}

func (l *rpcLoop) fire() {
	l.mu.Lock()
	timers := l.timers
	l.timers = nil
	l.mu.Unlock()
	for _, t := range timers {
		t.f()
	}
}

// runUntil runs what was posted until cond holds; fails after ten seconds.
func (l *rpcLoop) runUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		l.mu.Lock()
		var f func()
		if len(l.queue) > 0 {
			f = l.queue[0]
			l.queue = l.queue[1:]
		}
		l.mu.Unlock()
		if f != nil {
			f()
			continue
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting")
		}
		select {
		case <-l.wake:
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// fakeDaemon answers the calendar's methods from the samples.
type fakeDaemon struct {
	mu       sync.Mutex
	data     calendar.Samples
	calls    []string
	params   map[string][]json.RawMessage
	failures map[string]error
	held     map[string]bool
	gates    map[string][]chan struct{}
	// conflict is the current event event.update answers conflict with.
	conflict *api.Event
	// etag is what event.get answers as the etag ("" = the sample's).
	etag string
}

func newFakeDaemon() *fakeDaemon {
	return &fakeDaemon{data: samples(), params: map[string][]json.RawMessage{}, failures: map[string]error{},
		held: map[string]bool{}, gates: map[string][]chan struct{}{}}
}

func (f *fakeDaemon) Call(ctx context.Context, method string, params, result any) error {
	raw, _ := json.Marshal(params)
	f.mu.Lock()
	f.calls = append(f.calls, method)
	f.params[method] = append(f.params[method], raw)
	var gate chan struct{}
	if f.held[method] {
		gate = make(chan struct{})
		f.gates[method] = append(f.gates[method], gate)
	}
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failures[method]; err != nil {
		return err
	}
	answer, err := f.answer(method, raw)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(answer)
	return json.Unmarshal(b, result)
}

func (f *fakeDaemon) answer(method string, raw json.RawMessage) (any, error) {
	switch method {
	case api.MethodAccountList:
		return api.AccountListResult{Accounts: f.data.Accounts}, nil
	case api.MethodCalendarList:
		return f.data.Calendars, nil
	case api.MethodCalendarPreferences:
		return api.CalendarPreferencesResult{Preferences: f.data.Preferences}, nil
	case api.MethodInvitationList:
		return api.InvitationListResult{Invitations: f.data.Invitations}, nil
	case api.MethodEventList:
		var p api.EventListParams
		_ = json.Unmarshal(raw, &p)
		out := []api.Occurrence{}
		for _, o := range f.data.Occurrences {
			if calendar.Overlaps(o, p.From, p.To, prague) {
				out = append(out, o)
			}
		}
		return api.EventListResult{Occurrences: out, Complete: true}, nil
	case api.MethodEventGet:
		var p api.EventGetParams
		_ = json.Unmarshal(raw, &p)
		for _, e := range f.data.Events {
			if e.EventID == p.EventID {
				if f.etag != "" {
					e.ETag = f.etag
				}
				return api.EventGetResult{Event: e}, nil
			}
		}
		return nil, &api.Error{Code: api.CodeEventNotFound, Message: "no"}
	case api.MethodCalendarUpdate:
		var p api.CalendarUpdateParams
		_ = json.Unmarshal(raw, &p)
		for _, a := range f.data.Calendars.Accounts {
			for _, c := range a.Calendars {
				if c.ID == p.CalendarID {
					if p.Visible != nil {
						c.Visible = *p.Visible
					}
					if p.UserColor != nil {
						c.UserColor = *p.UserColor
					}
					return api.CalendarUpdateResult{Calendar: c}, nil
				}
			}
		}
		return nil, &api.Error{Code: api.CodeCalendarNotFound, Message: "no"}
	case api.MethodEventUpdate, api.MethodEventRespond, api.MethodEventDelete:
		if f.conflict != nil {
			// As the client hands it over: the data decoded from JSON.
			b, _ := json.Marshal(api.ConflictEventData{Event: *f.conflict})
			var data any
			_ = json.Unmarshal(b, &data)
			return nil, &api.Error{Code: api.CodeConflict, Message: "etag", Data: data}
		}
		if method == api.MethodEventDelete {
			return api.EventDeleteResult{}, nil
		}
		return nil, &api.Error{Code: api.CodeReadOnly, Message: "no"}
	case api.MethodCalendarSetPreferences:
		p := f.data.Preferences
		p.ShowDeclined = !p.ShowDeclined
		return api.CalendarSetPreferencesResult{Preferences: p}, nil
	}
	return struct{}{}, nil
}

func (f *fakeDaemon) count(m string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == m {
			n++
		}
	}
	return n
}

func (f *fakeDaemon) allCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *fakeDaemon) last(m string, v any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.params[m]
	if len(p) > 0 {
		_ = json.Unmarshal(p[len(p)-1], v)
	}
}

func (f *fakeDaemon) set(change func(f *fakeDaemon)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

func (f *fakeDaemon) hold(m string) { f.set(func(f *fakeDaemon) { f.held[m] = true }) }

func (f *fakeDaemon) heldCount(m string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.gates[m])
}

// release lets every held call of m answer and stops holding m.
func (f *fakeDaemon) release(m string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.held[m] = false
	for _, g := range f.gates[m] {
		close(g)
	}
	f.gates[m] = nil
}

type harness struct {
	t       *testing.T
	fake    *fakeDaemon
	loop    *rpcLoop
	src     *DaemonSource
	changes []Change
}

func newHarness(t *testing.T) *harness {
	h := &harness{t: t, fake: newFakeDaemon(), loop: newRPCLoop()}
	h.src = NewDaemonSource(h.fake, h.loop, DaemonOptions{Loc: prague})
	h.src.Observe(func(c Change) { h.changes = append(h.changes, c) })
	t.Cleanup(func() {
		for _, m := range []string{api.MethodCalendarList, api.MethodCalendarUpdate, api.MethodEventList} {
			h.fake.release(m)
		}
	})
	return h
}

func (h *harness) started() *harness {
	h.src.Start()
	h.idle()
	return h
}

func (h *harness) idle() {
	h.t.Helper()
	h.loop.runUntil(h.t, h.src.idle)
}

func (h *harness) last() Change {
	if len(h.changes) == 0 {
		return Change{Kind: -1}
	}
	return h.changes[len(h.changes)-1]
}

func (h *harness) has(kind ChangeKind) bool {
	return slices.ContainsFunc(h.changes, func(c Change) bool { return c.Kind == kind })
}

func at(y int, m time.Month, d, hh int) *time.Time {
	v := time.Date(y, m, d, hh, 0, 0, 0, prague)
	return &v
}

func notification(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDaemonStartLoadsTheSnapshot(t *testing.T) {
	h := newHarness(t)
	if h.src.Snapshot().Phase != PhaseLoading {
		t.Fatal("not loading before Start")
	}
	h.started()
	s := h.src.Snapshot()
	d := samples()
	if s.Phase != PhaseReady || len(s.Accounts) != len(d.Accounts) || len(s.CalendarAccounts) != len(d.Calendars.Accounts) ||
		s.Unanswered != d.Calendars.Unanswered || len(s.Invitations) != len(d.Invitations) {
		t.Fatalf("snapshot = %+v", s)
	}
	want := []string{api.MethodAccountList, api.MethodCalendarList, api.MethodCalendarPreferences, api.MethodInvitationList}
	if got := h.fake.allCalls(); !slices.Equal(got, want) {
		t.Errorf("calls = %v", got)
	}
	var p api.InvitationListParams
	h.fake.last(api.MethodInvitationList, &p)
	if p.State != api.InvitationUnanswered {
		t.Errorf("invitation.list state = %q", p.State)
	}
	if !h.has(ChangeSnapshot) {
		t.Error("no ChangeSnapshot")
	}
}

func TestDaemonOneLoadAtATime(t *testing.T) {
	h := newHarness(t)
	h.fake.hold(api.MethodCalendarList)
	h.src.Start()
	h.loop.runUntil(t, func() bool { return h.fake.heldCount(api.MethodCalendarList) == 1 })
	h.src.Refresh()
	h.src.Refresh()
	h.src.AccountsChanged()
	h.fake.release(api.MethodCalendarList)
	h.idle()
	if n := h.fake.count(api.MethodCalendarList); n != 2 {
		t.Errorf("calendar.list %d times, want 2 (once more after the answer)", n)
	}
	if n := h.fake.count(api.MethodAccountList); n != 2 {
		t.Errorf("account.list %d times", n)
	}
}

func TestDaemonRangesAreCachedAndDroppedByOverlap(t *testing.T) {
	h := newHarness(t).started()
	r := week()
	h.fake.hold(api.MethodEventList)
	var a, b []api.Occurrence
	answered := 0
	h.src.Occurrences(r, func(o []api.Occurrence, e *api.Error) { a = o; answered++ })
	h.src.Occurrences(r, func(o []api.Occurrence, e *api.Error) { b = o; answered++ })
	h.loop.runUntil(t, func() bool { return h.fake.heldCount(api.MethodEventList) == 1 })
	h.fake.release(api.MethodEventList)
	h.loop.runUntil(t, func() bool { return answered == 2 })
	if len(a) == 0 || len(a) != len(b) {
		t.Fatalf("answers %d and %d occurrences", len(a), len(b))
	}
	if n := h.fake.count(api.MethodEventList); n != 1 {
		t.Errorf("event.list %d times while in flight", n)
	}
	var p api.EventListParams
	h.fake.last(api.MethodEventList, &p)
	if !p.From.Equal(r.From) || !p.To.Equal(r.To) || p.TimeZone != "Europe/Prague" || p.IncludeInvitations == nil || !*p.IncludeInvitations {
		t.Errorf("event.list params = %+v", p)
	}
	if got, ok := h.src.CachedOccurrences(r); !ok || len(got) != len(a) {
		t.Error("not cached")
	}
	answered = 0
	h.src.Occurrences(r, func([]api.Occurrence, *api.Error) { answered++ })
	h.loop.runUntil(t, func() bool { return answered == 1 })
	if n := h.fake.count(api.MethodEventList); n != 1 {
		t.Errorf("a cached range asked the daemon (%d)", n)
	}

	// A change next year leaves this week's cache.
	lists := h.fake.count(api.MethodCalendarList)
	from, to := at(2027, 3, 1, 0), at(2027, 3, 2, 0)
	h.src.Handle(api.NotifyCalendarChanged, notification(t, api.CalendarChangedNotification{
		CalendarIDs: []api.CalendarID{calendar.SampleHolidays}, From: from, To: to}))
	h.idle()
	if _, ok := h.src.CachedOccurrences(r); !ok {
		t.Error("a change next year dropped this week")
	}
	if c := h.last(); c.Kind != ChangeOccurrences || c.From == nil || !c.From.Equal(*from) || !c.To.Equal(*to) {
		t.Errorf("last change = %+v", c)
	}
	// The calendars are listed again, the preferences not (calendars named).
	if n := h.fake.count(api.MethodCalendarList); n != lists+1 {
		t.Errorf("calendar.list %d, want %d", n, lists+1)
	}
	if n := h.fake.count(api.MethodCalendarPreferences); n != 1 {
		t.Errorf("calendar.preferences %d", n)
	}
	// A change on Thursday drops it, and the preferences are asked.
	h.src.Handle(api.NotifyCalendarChanged, notification(t, api.CalendarChangedNotification{
		From: at(2026, 10, 15, 0), To: at(2026, 10, 15, 12)}))
	h.idle()
	if _, ok := h.src.CachedOccurrences(r); ok {
		t.Error("a change on Thursday left the week")
	}
	if n := h.fake.count(api.MethodCalendarPreferences); n != 2 {
		t.Errorf("calendar.preferences %d, want 2", n)
	}
	h.src.Occurrences(r, nil)
	h.idle()
	if n := h.fake.count(api.MethodEventList); n != 2 {
		t.Errorf("event.list %d, want 2", n)
	}
	// Without a range: everything.
	h.src.Handle(api.NotifyCalendarChanged, json.RawMessage(`{}`))
	h.idle()
	if _, ok := h.src.CachedOccurrences(r); ok {
		t.Error("a change without a range left the week")
	}
	if c := h.last(); c.Kind != ChangeOccurrences || c.From != nil || c.To != nil {
		t.Errorf("last change = %+v", c)
	}
}

func TestDaemonRangeDroppedInFlightIsNotStored(t *testing.T) {
	h := newHarness(t).started()
	r := week()
	h.fake.hold(api.MethodEventList)
	var first []api.Occurrence
	firstDone := false
	h.src.Occurrences(r, func(o []api.Occurrence, _ *api.Error) { first, firstDone = o, true })
	h.loop.runUntil(t, func() bool { return h.fake.heldCount(api.MethodEventList) == 1 })
	h.src.Handle(api.NotifyCalendarChanged, json.RawMessage(`{}`))
	// Asked again after the drop: a call of its own.
	secondDone := false
	h.src.Occurrences(r, func([]api.Occurrence, *api.Error) { secondDone = true })
	h.loop.runUntil(t, func() bool { return h.fake.heldCount(api.MethodEventList) == 2 })
	h.fake.set(func(f *fakeDaemon) { f.held[api.MethodEventList] = false })
	// Only the first answers.
	h.fake.mu.Lock()
	close(h.fake.gates[api.MethodEventList][0])
	h.fake.gates[api.MethodEventList] = h.fake.gates[api.MethodEventList][1:]
	h.fake.mu.Unlock()
	h.loop.runUntil(t, func() bool { return firstDone })
	if len(first) == 0 {
		t.Error("the waiter got no answer")
	}
	if _, ok := h.src.CachedOccurrences(r); ok {
		t.Error("an answer of a dropped range was stored")
	}
	h.fake.release(api.MethodEventList)
	h.loop.runUntil(t, func() bool { return secondDone })
	h.idle()
	if _, ok := h.src.CachedOccurrences(r); !ok {
		t.Error("the call after the drop was not stored")
	}
}

func TestDaemonEventsAreCachedAndAConflictReplacesThem(t *testing.T) {
	h := newHarness(t).started()
	var e *api.Event
	h.src.Event("ev_sample_offsite", "", false, func(v *api.Event, _ *api.Error) { e = v })
	h.idle()
	h.src.Event("ev_sample_offsite", "", false, nil)
	h.idle()
	if e == nil || h.fake.count(api.MethodEventGet) != 1 {
		t.Fatalf("event %v, event.get %d", e, h.fake.count(api.MethodEventGet))
	}
	var p api.EventGetParams
	h.fake.last(api.MethodEventGet, &p)
	if p.TimeZone != "Europe/Prague" {
		t.Errorf("event.get zone %q", p.TimeZone)
	}
	newer := *e
	newer.ETag = `"7"`
	newer.Title = "Offsite (changed elsewhere)"
	h.fake.set(func(f *fakeDaemon) { f.conflict = &newer })
	var gotErr *api.Error
	var seenBefore bool
	h.src.UpdateEvent(api.EventUpdateParams{EventID: "ev_sample_offsite", Scope: api.EditScopeAll, ETag: e.ETag,
		Event: api.EventInput{Title: "x", AllDay: true}}, func(_ *api.EventUpdateResult, err *api.Error) {
		gotErr = err
		seenBefore = h.last().Kind == ChangeEvent && h.last().Event == "ev_sample_offsite"
	})
	h.idle()
	if gotErr == nil || gotErr.Code != api.CodeConflict {
		t.Fatalf("error = %v", gotErr)
	}
	if !seenBefore {
		t.Error("observers did not hear of the conflict's event before done")
	}
	var cached *api.Event
	h.src.Event("ev_sample_offsite", "", false, func(v *api.Event, _ *api.Error) { cached = v })
	h.idle()
	if cached == nil || cached.ETag != `"7"` || cached.Title != "Offsite (changed elsewhere)" {
		t.Errorf("cached = %+v", cached)
	}
	if n := h.fake.count(api.MethodEventGet); n != 1 {
		t.Errorf("event.get %d", n)
	}

	// A reload with a new etag tells the observers; the same etag does not.
	h.changes = nil
	h.fake.set(func(f *fakeDaemon) { f.etag = `"8"` })
	h.src.Event("ev_sample_offsite", "", true, nil)
	h.idle()
	if len(h.changes) != 1 || h.changes[0].Kind != ChangeEvent {
		t.Errorf("reload changes = %+v", h.changes)
	}
	h.src.Event("ev_sample_offsite", "", true, nil)
	h.idle()
	if len(h.changes) != 1 {
		t.Errorf("a reload without a change told the observers: %+v", h.changes)
	}

	// A refusal that is not a conflict stores nothing.
	h.fake.set(func(f *fakeDaemon) { f.conflict = nil })
	h.changes = nil
	h.src.UpdateEvent(api.EventUpdateParams{EventID: "ev_sample_offsite", Scope: api.EditScopeAll}, func(_ *api.EventUpdateResult, err *api.Error) {
		gotErr = err
	})
	h.idle()
	if gotErr == nil || gotErr.Code != api.CodeReadOnly || len(h.changes) != 0 {
		t.Errorf("readOnly: %v, changes %+v", gotErr, h.changes)
	}

	// A delete drops the event.
	h.src.DeleteEvent(api.EventDeleteParams{EventID: "ev_sample_offsite", Scope: api.EditScopeAll}, nil)
	h.idle()
	if c := h.last(); c.Kind != ChangeEvent || c.Event != "ev_sample_offsite" {
		t.Errorf("delete change = %+v", c)
	}
	h.src.Event("ev_sample_offsite", "", false, nil)
	h.idle()
	if n := h.fake.count(api.MethodEventGet); n != 4 {
		t.Errorf("event.get after the delete: %d calls", n)
	}
}

func TestConflictEvent(t *testing.T) {
	ev := api.Event{ETag: "1"}
	ev.EventID = "ev_1"
	if got, ok := ConflictEvent(&api.Error{Code: api.CodeConflict, Data: api.ConflictEventData{Event: ev}}); !ok || got.EventID != "ev_1" {
		t.Error("typed data")
	}
	for i, e := range []*api.Error{nil, {Code: api.CodeConflict}, {Code: api.CodeReadOnly, Data: api.ConflictEventData{Event: ev}},
		{Code: api.CodeConflict, Data: map[string]any{"event": "nonsense"}}, {Code: api.CodeConflict, Data: map[string]any{}}} {
		if _, ok := ConflictEvent(e); ok {
			t.Errorf("%d: an event", i)
		}
	}
}

func TestDaemonStatesAndAlerts(t *testing.T) {
	h := newHarness(t).started()
	h.changes = nil
	state := api.CalendarSyncState{AccountID: calendar.SampleWork, Status: api.CalendarSyncConsentRequired, Progress: -1}
	h.src.Handle(api.NotifyCalendarState, notification(t, api.CalendarStateNotification{State: state}))
	a, _ := h.src.Snapshot().CalendarAccount(calendar.SampleWork)
	if a.State.Status != api.CalendarSyncConsentRequired {
		t.Errorf("state = %+v", a.State)
	}
	if len(h.changes) != 1 || h.changes[0].Kind != ChangeSnapshot {
		t.Errorf("changes = %+v", h.changes)
	}
	// The same state again: nothing.
	h.src.Handle(api.NotifyCalendarState, notification(t, api.CalendarStateNotification{State: state}))
	if len(h.changes) != 1 {
		t.Error("an unchanged state told the observers")
	}
	// An unknown account: nothing.
	h.src.Handle(api.NotifyCalendarState, notification(t, api.CalendarStateNotification{
		State: api.CalendarSyncState{AccountID: "acc_x", Status: api.CalendarSyncAuthRequired}}))
	if len(h.changes) != 1 {
		t.Error("an unknown account changed the snapshot")
	}
	alert := api.EventAlert{AlertID: "al_9", EventID: "ev_sample_planning", AccountID: calendar.SampleWork, Title: "Q4 planning", MinutesBefore: 10}
	h.src.Handle(api.NotifyEventAlert, notification(t, api.EventAlertNotification{Alert: alert}))
	if c := h.last(); c.Kind != ChangeAlert || c.Alert.AlertID != "al_9" {
		t.Errorf("alert change = %+v", c)
	}
	// A malformed payload is the calendar's and is ignored; another method
	// is not the calendar's.
	n := len(h.changes)
	if !h.src.Handle(api.NotifyEventAlert, json.RawMessage(`[`)) || len(h.changes) != n {
		t.Error("malformed alert")
	}
	if h.src.Handle(api.NotifyBoardChanged, json.RawMessage(`{}`)) {
		t.Error("notify.boardChanged taken")
	}
	h.idle()
	if c := h.fake.allCalls(); len(c) != 4 {
		t.Errorf("a state or alert made calls: %v", c)
	}
}

func TestDaemonInvitationsChanged(t *testing.T) {
	h := newHarness(t).started()
	h.src.Handle(api.NotifyInvitationsChanged, json.RawMessage(`{"accountIds":["acc_1"]}`))
	h.idle()
	for m, want := range map[string]int{api.MethodCalendarList: 2, api.MethodInvitationList: 2,
		api.MethodAccountList: 1, api.MethodCalendarPreferences: 1} {
		if n := h.fake.count(m); n != want {
			t.Errorf("%s %d times, want %d", m, n, want)
		}
	}
}

func TestDaemonVisibilityIsOptimisticAndRevertedOnRefusal(t *testing.T) {
	h := newHarness(t).started()
	h.fake.hold(api.MethodCalendarUpdate)
	h.fake.set(func(f *fakeDaemon) {
		f.failures[api.MethodCalendarUpdate] = &api.Error{Code: api.CodeCalendarNotFound, Message: "gone"}
	})
	h.src.SetVisible(calendar.SampleSport, false)
	if c, _ := h.src.Snapshot().Calendar(calendar.SampleSport); c.Visible {
		t.Fatal("not shown at once")
	}
	if !h.has(ChangeOccurrences) {
		t.Error("no ChangeOccurrences for the optimistic change")
	}
	h.loop.runUntil(t, func() bool { return h.fake.heldCount(api.MethodCalendarUpdate) == 1 })
	// A calendar.list meanwhile does not undo it.
	h.src.Refresh()
	h.loop.runUntil(t, func() bool { return !h.src.loadInFlight && h.fake.count(api.MethodCalendarList) == 2 })
	if c, _ := h.src.Snapshot().Calendar(calendar.SampleSport); c.Visible {
		t.Error("calendar.list undid the overlay")
	}
	h.fake.release(api.MethodCalendarUpdate)
	h.idle()
	if c, _ := h.src.Snapshot().Calendar(calendar.SampleSport); !c.Visible {
		t.Error("the refusal was not taken back")
	}
	if c := h.last(); c.Kind != ChangeError || c.Err == nil || c.Err.Code != api.CodeCalendarNotFound {
		t.Errorf("last change = %+v", c)
	}
	var p api.CalendarUpdateParams
	h.fake.last(api.MethodCalendarUpdate, &p)
	if p.CalendarID != calendar.SampleSport || p.Visible == nil || *p.Visible {
		t.Errorf("calendar.update params = %+v", p)
	}
	// Accepted: it stays.
	h.fake.set(func(f *fakeDaemon) { delete(f.failures, api.MethodCalendarUpdate) })
	h.src.SetVisible(calendar.SampleFamily, false)
	h.idle()
	if c, _ := h.src.Snapshot().Calendar(calendar.SampleFamily); c.Visible {
		t.Error("an accepted change went back")
	}
	// No change: no call.
	n := h.fake.count(api.MethodCalendarUpdate)
	h.src.SetVisible(calendar.SampleFamily, false)
	h.src.SetVisible("cal_unknown", true)
	h.idle()
	if h.fake.count(api.MethodCalendarUpdate) != n {
		t.Error("a no-op asked the daemon")
	}
}

func TestDaemonWritesUpdateTheSnapshot(t *testing.T) {
	h := newHarness(t).started()
	var got *api.Calendar
	h.src.SetColor(calendar.SampleSport, "#123456", func(c *api.Calendar, _ *api.Error) { got = c })
	h.idle()
	if c, _ := h.src.Snapshot().Calendar(calendar.SampleSport); got == nil || c.UserColor != "#123456" {
		t.Errorf("colour: %v, snapshot %q", got, c.UserColor)
	}
	before := h.src.Snapshot().Preferences.ShowDeclined
	h.src.SetPreferences(api.CalendarPreferencesUpdate{}, nil)
	h.idle()
	if h.src.Snapshot().Preferences.ShowDeclined == before {
		t.Error("preferences not stored")
	}
	lists := h.fake.count(api.MethodCalendarList)
	h.src.UpdateAccount(api.AccountUpdateParams{}, nil)
	h.idle()
	if h.fake.count(api.MethodCalendarList) != lists+1 {
		t.Error("account.update did not load again")
	}
}

func TestDaemonAccountsChangedListsTheCalendarsAgain(t *testing.T) {
	h := newHarness(t).started()
	lists, accounts := h.fake.count(api.MethodCalendarList), h.fake.count(api.MethodAccountList)
	h.src.AccountsChanged()
	h.idle()
	if h.fake.count(api.MethodCalendarList) != lists+1 || h.fake.count(api.MethodAccountList) != accounts+1 {
		t.Error("not loaded again")
	}
}

func TestDaemonNamedCalendarsDropTheirRanges(t *testing.T) {
	h := newHarness(t).started()
	r := week()
	h.src.Occurrences(r, nil)
	h.idle()
	if _, ok := h.src.CachedOccurrences(r); !ok {
		t.Fatal("not cached")
	}
	// A range far away, but naming a calendar this week shows: dropped.
	h.src.Handle(api.NotifyCalendarChanged, notification(t, api.CalendarChangedNotification{
		CalendarIDs: []api.CalendarID{calendar.SampleSport}, From: at(2027, 3, 1, 0), To: at(2027, 3, 2, 0)}))
	h.idle()
	if _, ok := h.src.CachedOccurrences(r); ok {
		t.Error("a named calendar's range stayed")
	}
	if c := h.last(); c.Kind != ChangeOccurrences || c.From != nil || c.To != nil {
		t.Errorf("last change = %+v", c)
	}
}

func TestDaemonOldDaemonIsUnsupported(t *testing.T) {
	h := newHarness(t)
	h.fake.set(func(f *fakeDaemon) {
		f.failures[api.MethodCalendarList] = &api.Error{Code: api.CodeMethodNotFound, Message: "unknown method"}
	})
	h.started()
	if p := h.src.Snapshot().Phase; p != PhaseUnsupported {
		t.Errorf("phase = %v", p)
	}
	if len(h.loop.pending()) != 0 {
		t.Error("an old daemon is asked again")
	}
	h.src.ConnectionChanged(false)
	if p := h.src.Snapshot().Phase; p != PhaseUnavailable {
		t.Errorf("phase = %v", p)
	}
}

func TestDaemonRetriesAFailedLoad(t *testing.T) {
	h := newHarness(t)
	h.fake.set(func(f *fakeDaemon) {
		f.failures[api.MethodCalendarPreferences] = &api.Error{Code: api.CodeInternalError, Message: "x"}
	})
	h.started()
	if p := h.src.Snapshot().Phase; p != PhaseFailed {
		t.Fatalf("phase = %v", p)
	}
	if got := h.loop.pending(); !slices.Equal(got, []time.Duration{board.RetryDelay(1)}) {
		t.Fatalf("timers = %v", got)
	}
	// A second failure waits longer.
	h.loop.fire()
	h.idle()
	if got := h.loop.pending(); !slices.Equal(got, []time.Duration{board.RetryDelay(2)}) {
		t.Fatalf("timers = %v", got)
	}
	// The retry asks again what failed (the preferences) and lands.
	h.fake.set(func(f *fakeDaemon) { delete(f.failures, api.MethodCalendarPreferences) })
	h.loop.fire()
	h.idle()
	if p := h.src.Snapshot().Phase; p != PhaseReady {
		t.Errorf("phase = %v", p)
	}
	if n := h.fake.count(api.MethodCalendarPreferences); n != 3 {
		t.Errorf("calendar.preferences %d", n)
	}
	if n := h.fake.count(api.MethodAccountList); n != 1 {
		t.Errorf("account.list %d (it had answered)", n)
	}

	// No connection: unavailable, no retry; the connection coming back
	// loads everything and drops the caches.
	h.src.Occurrences(week(), nil)
	h.idle()
	h.fake.set(func(f *fakeDaemon) { f.failures[api.MethodCalendarList] = client.ErrDisconnected })
	h.src.Refresh()
	h.idle()
	if p := h.src.Snapshot().Phase; p != PhaseUnavailable || len(h.loop.pending()) != 0 {
		t.Errorf("phase %v, timers %v", p, h.loop.pending())
	}
	h.fake.set(func(f *fakeDaemon) { delete(f.failures, api.MethodCalendarList) })
	h.src.ConnectionChanged(true)
	h.idle()
	if p := h.src.Snapshot().Phase; p != PhaseReady {
		t.Errorf("phase after reconnect = %v", p)
	}
	if _, ok := h.src.CachedOccurrences(week()); ok {
		t.Error("a reconnect kept the ranges")
	}
}

func TestDaemonDisconnectDropsTheLoadOnItsWay(t *testing.T) {
	h := newHarness(t)
	h.fake.hold(api.MethodCalendarList)
	h.src.Start()
	h.loop.runUntil(t, func() bool { return h.fake.heldCount(api.MethodCalendarList) == 1 })
	h.src.ConnectionChanged(false)
	h.fake.release(api.MethodCalendarList)
	// Give the stale answer time to arrive.
	deadline := time.Now().Add(50 * time.Millisecond)
	h.loop.runUntil(t, func() bool { return time.Now().After(deadline) })
	if p := h.src.Snapshot().Phase; p != PhaseUnavailable {
		t.Errorf("a stale load landed: %v", p)
	}
}

func TestDaemonNothingBeforeStart(t *testing.T) {
	h := newHarness(t)
	h.src.Refresh()
	h.src.AccountsChanged()
	h.src.ConnectionChanged(true)
	if !h.src.Handle(api.NotifyInvitationsChanged, json.RawMessage(`{}`)) {
		t.Error("not the calendar's")
	}
	h.idle()
	if c := h.fake.allCalls(); len(c) != 0 {
		t.Errorf("calls before Start: %v", c)
	}
}
