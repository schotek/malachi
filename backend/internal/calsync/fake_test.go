// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package calsync

import (
	"context"
	"log/slog"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/calendar"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

var t0 = time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)

// fakeClock is a Clock whose time moves only by Advance.
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

func newFakeClock(t time.Time) *fakeClock { return &fakeClock{now: t} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) AfterFunc(d time.Duration, f func()) func() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{at: c.now.Add(d), f: f}
	c.timers = append(c.timers, t)
	return func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		was := !t.stopped
		t.stopped = true
		return was
	}
}

// Advance moves the time and runs the timers that came due, in order.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	var due []*fakeTimer
	keep := c.timers[:0]
	for _, t := range c.timers {
		switch {
		case t.stopped:
		case !t.at.After(c.now):
			t.stopped = true
			due = append(due, t)
		default:
			keep = append(keep, t)
		}
	}
	c.timers = keep
	c.mu.Unlock()
	sort.SliceStable(due, func(i, j int) bool { return due[i].at.Before(due[j].at) })
	for _, t := range due {
		t.f()
	}
}

// fakeProvider is a calendar source in memory.
type fakeProvider struct {
	mu        sync.Mutex
	cals      []RemoteCalendar
	objects   map[string]map[string]Object // calendar remote id → remote id → object
	listErr   error
	syncErr   map[string]error
	syncs     []syncCall
	listCalls int
	full      bool
	deltaMode string
	seq       int
}

type syncCall struct {
	remoteID string
	token    string
	window   Window
}

func newFakeProvider(cals ...RemoteCalendar) *fakeProvider {
	return &fakeProvider{cals: cals, objects: map[string]map[string]Object{}, syncErr: map[string]error{}, full: true}
}

func (p *fakeProvider) put(cal string, obj Object) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.objects[cal] == nil {
		p.objects[cal] = map[string]Object{}
	}
	p.objects[cal][obj.RemoteID] = obj
}

func (p *fakeProvider) drop(cal, id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.objects[cal], id)
}

func (p *fakeProvider) setListErr(err error) {
	p.mu.Lock()
	p.listErr = err
	p.mu.Unlock()
}

func (p *fakeProvider) calls() []syncCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]syncCall(nil), p.syncs...)
}

func (p *fakeProvider) callsOf(remote string) int {
	n := 0
	for _, c := range p.calls() {
		if c.remoteID == remote {
			n++
		}
	}
	return n
}

func (p *fakeProvider) Calendars(context.Context) ([]RemoteCalendar, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.listCalls++
	if p.listErr != nil {
		return nil, p.listErr
	}
	return append([]RemoteCalendar(nil), p.cals...), nil
}

func (p *fakeProvider) Sync(_ context.Context, cal StoredCalendar, w Window, sink Sink) (SyncState, error) {
	p.mu.Lock()
	p.syncs = append(p.syncs, syncCall{remoteID: cal.RemoteID, token: cal.SyncToken, window: w})
	if err := p.syncErr[cal.RemoteID]; err != nil {
		p.mu.Unlock()
		return SyncState{}, err
	}
	var objs []Object
	for _, o := range p.objects[cal.RemoteID] {
		objs = append(objs, o)
	}
	p.seq++
	st := SyncState{Token: "tok" + string(rune('0'+p.seq%10)), Full: p.full, DeltaMode: p.deltaMode}
	p.mu.Unlock()
	sort.Slice(objs, func(i, j int) bool { return objs[i].RemoteID < objs[j].RemoteID })
	for _, o := range objs {
		if err := sink.Upsert(o); err != nil {
			return SyncState{}, err
		}
	}
	return st, nil
}

func (p *fakeProvider) Range(context.Context, StoredCalendar, time.Time, time.Time) ([]Occurrence, error) {
	return nil, ErrUnsupported
}
func (p *fakeProvider) Create(context.Context, StoredCalendar, Event, WriteOptions) (WriteResult, error) {
	return WriteResult{}, ErrUnsupported
}
func (p *fakeProvider) Update(context.Context, StoredCalendar, StoredEvent, Change, WriteOptions) (WriteResult, error) {
	return WriteResult{}, ErrUnsupported
}
func (p *fakeProvider) Delete(context.Context, StoredCalendar, StoredEvent, Scope, WriteOptions) (WriteResult, error) {
	return WriteResult{}, ErrUnsupported
}
func (p *fakeProvider) Respond(context.Context, StoredCalendar, StoredEvent, Response, WriteOptions) (WriteResult, error) {
	return WriteResult{}, ErrUnsupported
}
func (p *fakeProvider) FreeBusy(context.Context, []string, time.Time, time.Time, time.Duration) ([]Availability, error) {
	return nil, ErrUnsupported
}
func (p *fakeProvider) Features() api.CalendarFeatures { return api.CalendarFeatures{} }

// fakeNotifier records what it is told.
type fakeNotifier struct {
	mu      sync.Mutex
	changed []api.CalendarChangedNotification
	states  []api.CalendarSyncState
	alerts  []api.EventAlert
	mail    []api.SyncState
	auth    []api.AuthRequiredNotification
}

func (n *fakeNotifier) NewMessage(api.NewMessageNotification) {}
func (n *fakeNotifier) SyncState(s api.SyncStateNotification) {
	n.mu.Lock()
	n.mail = append(n.mail, s.State)
	n.mu.Unlock()
}
func (n *fakeNotifier) AuthRequired(a api.AuthRequiredNotification) {
	n.mu.Lock()
	n.auth = append(n.auth, a)
	n.mu.Unlock()
}
func (n *fakeNotifier) AccountsChanged(api.AccountsChangedNotification) {}
func (n *fakeNotifier) MessagesChanged(api.MessagesChangedNotification) {}
func (n *fakeNotifier) CalendarChanged(c api.CalendarChangedNotification) {
	n.mu.Lock()
	n.changed = append(n.changed, c)
	n.mu.Unlock()
}
func (n *fakeNotifier) CalendarState(s api.CalendarStateNotification) {
	n.mu.Lock()
	n.states = append(n.states, s.State)
	n.mu.Unlock()
}
func (n *fakeNotifier) EventAlert(a api.EventAlertNotification) {
	n.mu.Lock()
	n.alerts = append(n.alerts, a.Alert)
	n.mu.Unlock()
}
func (n *fakeNotifier) InvitationsChanged(api.InvitationsChangedNotification) {}

func (n *fakeNotifier) alertList() []api.EventAlert {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]api.EventAlert(nil), n.alerts...)
}

func (n *fakeNotifier) changedList() []api.CalendarChangedNotification {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]api.CalendarChangedNotification(nil), n.changed...)
}

func (n *fakeNotifier) stateList() []api.CalendarSyncState {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]api.CalendarSyncState(nil), n.states...)
}

func (n *fakeNotifier) authList() []api.AuthRequiredNotification {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]api.AuthRequiredNotification(nil), n.auth...)
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "store.db"), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// harness is a supervisor over a temp store, a fake clock and providers.
type harness struct {
	t      *testing.T
	st     *store.Store
	clock  *fakeClock
	note   *fakeNotifier
	sv     *Supervisor
	cancel context.CancelFunc
	done   chan struct{}

	mu        sync.Mutex
	providers map[string]*fakeProvider
	versioned map[string]Provider
	made      map[string]int
	factErr   map[string]error
}

func newHarness(t *testing.T, opts ...func(*Options)) *harness {
	h := &harness{t: t, st: openStore(t), clock: newFakeClock(t0), note: &fakeNotifier{},
		providers: map[string]*fakeProvider{}, versioned: map[string]Provider{}, made: map[string]int{}, factErr: map[string]error{}}
	o := Options{
		Store: h.st, Notifier: h.note, Clock: h.clock, Location: time.UTC,
		Backoff: func(attempt int) time.Duration { return time.Duration(attempt+1) * 30 * time.Second },
		Providers: func(_ context.Context, a store.Account) (Provider, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.made[a.ID]++
			if err := h.factErr[a.ID]; err != nil {
				return nil, err
			}
			if p := h.versioned[a.ID]; p != nil {
				return p, nil
			}
			return h.providers[a.ID], nil
		},
	}
	for _, f := range opts {
		f(&o)
	}
	h.sv = New(o)
	return h
}

func (h *harness) run() {
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel, h.done = cancel, make(chan struct{})
	go func() { defer close(h.done); h.sv.Run(ctx) }()
	h.t.Cleanup(h.stop)
}

func (h *harness) stop() {
	if h.cancel != nil {
		h.cancel()
		<-h.done
		h.cancel = nil
	}
}

func (h *harness) setProvider(id string, p *fakeProvider) {
	h.mu.Lock()
	h.providers[id] = p
	h.mu.Unlock()
}

func (h *harness) setVersioned(id string, p Provider) {
	h.mu.Lock()
	h.versioned[id] = p
	h.mu.Unlock()
}

func (h *harness) madeCount(id string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.made[id]
}

func caldavAccount(id string) store.Account {
	return store.Account{ID: id, Email: id + "@example.org", Enabled: true, Config: api.AccountConfig{
		Name: id, Email: id + "@example.org", Kind: api.AccountCalDAV,
		CalDAV: &api.CalDAVConfig{ServerURL: "https://dav.example.org/" + id, Username: id},
	}}
}

func graphAccount(id string, sync bool) store.Account {
	return store.Account{ID: id, Email: id + "@example.org", Enabled: true, Config: api.AccountConfig{
		Name: id, Email: id + "@example.org", Kind: api.AccountGraph, Graph: &api.GraphConfig{},
		Calendar: &api.CalendarConfig{Sync: sync},
	}}
}

// eventually waits (real time) for cond, moving the fake clock by step
// between tries when step > 0.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// advanceUntil moves the clock in steps of d (up to max) until cond holds.
func (h *harness) advanceUntil(what string, d, max time.Duration, cond func() bool) {
	h.t.Helper()
	moved := time.Duration(0)
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) || moved > max {
			h.t.Fatalf("%s did not happen within %s", what, moved)
		}
		time.Sleep(3 * time.Millisecond)
		if cond() {
			return
		}
		h.clock.Advance(d)
		moved += d
	}
}

func timed(uid string, start time.Time, d time.Duration, alarms ...int) Object {
	ev := calendar.Event{UID: uid, Kind: calendar.KindSingle, Title: "T " + uid, Start: start, End: start.Add(d), Zone: "UTC",
		Status: api.EventStatusConfirmed, ShowAs: api.ShowAsBusy, Alarms: alarms}
	return Object{RemoteID: "/cal/" + uid + ".ics", ETag: "e1", Master: &ev}
}

func weekly(uid string, start time.Time, count int) Object {
	ev := calendar.Event{UID: uid, Kind: calendar.KindMaster, Title: "W " + uid, Start: start, End: start.Add(time.Hour), Zone: "UTC",
		RRule: "FREQ=WEEKLY;COUNT=" + itoa(count), RecurOK: true, Status: api.EventStatusConfirmed, ShowAs: api.ShowAsBusy}
	return Object{RemoteID: "/cal/" + uid + ".ics", ETag: "e1", Master: &ev}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func occurrences(t *testing.T, st *store.Store, accountID string, from, to time.Time) []store.Occurrence {
	t.Helper()
	l, err := st.EventRange(context.Background(), store.OccurrenceQuery{From: from, To: to, AccountIDs: []string{accountID}, Limit: 5000, IncludeHidden: true, IncludeDeclined: true})
	if err != nil {
		t.Fatal(err)
	}
	return l.Occurrences
}
