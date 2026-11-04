// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package calsync

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Options wire the engine to the daemon. Store and Providers are
// required; the rest has defaults.
type Options struct {
	Store *store.Store
	// Providers makes the calendar source of an account (core: graph →
	// graph.NewCalendarProvider, caldav → caldav.NewProvider). It is asked
	// when the account's syncer starts and again after it failed; an
	// *api.Error (authRequired, calendarConsentRequired, …) is the
	// account's state until the next attempt.
	Providers func(ctx context.Context, a store.Account) (Provider, error)
	// Notifier receives notify.syncState and notify.authRequired of caldav
	// accounts, and — when it implements api.CalendarNotifier — the
	// calendar notifications. nil = none.
	Notifier api.Notifier
	// Clock is the engine's time (nil = the real clock).
	Clock Clock
	// Location is the daemon's zone: the window's days, all-day and
	// floating events, the working-day start of all-day alerts (nil =
	// time.Local).
	Location *time.Location
	// Preferences reads the calendar preferences (nil = the store's meta
	// calendar.prefs).
	Preferences func(ctx context.Context) (api.CalendarPreferences, error)
	Log         *slog.Logger
	// Backoff overrides the retry delay after the attempt-th failure in a
	// row (nil = 30 s doubling to 1 h, ±10 % jitter).
	Backoff func(attempt int) time.Duration
	// ExpandKey overrides the value of meta calendar.expand (tests; "" =
	// ExpandKey()).
	ExpandKey string
}

// Versioned is an optional interface of a Provider: the version of how it
// maps the server's data onto the model. When it differs from the one the
// engine last synchronised the account with (meta
// calendar.provider.<accountID>), every calendar of the account is listed
// again in full and its stored rows replaced (a mapping fix reaches rows
// a delta token would never re-send). An unversioned provider is never
// reset this way.
type Versioned interface {
	DataVersion() int
}

// MetaProviderVersion is the meta key holding the DataVersion an
// account's calendars were last synchronised with.
func MetaProviderVersion(accountID string) string { return "calendar.provider." + accountID }

// HasCalendars reports whether the engine synchronises the account: an
// enabled caldav account, or an enabled graph account whose calendar sync
// is on.
func HasCalendars(a store.Account) bool {
	if !a.Enabled {
		return false
	}
	switch a.Config.Protocol() {
	case api.AccountCalDAV:
		return CalDAVHasCalendars(a.Config)
	case api.AccountGraph:
		return a.Config.Calendar != nil && a.Config.Calendar.Sync
	}
	return false
}

// CalDAVHasCalendars reports whether a caldav configuration synchronises
// calendars: always, unless its contacts are on and its calendar block
// says sync false (an account of a CardDAV-only server, or one whose user
// keeps only the address books; docs/api.md §4.19).
func CalDAVHasCalendars(c api.AccountConfig) bool {
	if c.Protocol() != api.AccountCalDAV {
		return false
	}
	return c.CalDAV == nil || !c.CalDAV.Contacts || c.Calendar == nil || c.Calendar.Sync
}

// Supervisor owns one syncer per account with calendars and the alert
// scheduler. Its methods are safe from any goroutine; syncers start once
// Run is called (Start before Run is queued).
type Supervisor struct {
	opts   Options
	clock  Clock
	loc    *time.Location
	log    *slog.Logger
	notify *notifier
	alerts *alertScheduler

	mu      sync.Mutex
	ctx     context.Context
	running bool
	stopped bool
	seq     int
	entries map[string]*entry
	queued  []store.Account
	wg      sync.WaitGroup
}

type entry struct {
	seq    int
	syncer *syncer
	cancel context.CancelFunc
	done   chan struct{}
}

// New prepares a supervisor.
func New(opts Options) *Supervisor {
	if opts.Clock == nil {
		opts.Clock = realClock{}
	}
	if opts.Location == nil {
		opts.Location = time.Local
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	if opts.ExpandKey == "" {
		opts.ExpandKey = ExpandKey()
	}
	sv := &Supervisor{opts: opts, clock: opts.Clock, loc: opts.Location, log: opts.Log.With("component", "calsync"),
		entries: map[string]*entry{}}
	sv.notify = newNotifier(opts.Clock, opts.Notifier)
	sv.alerts = newAlertScheduler(sv)
	sv.notify.changed = sv.alerts.changed
	return sv
}

// Run serves until ctx is cancelled, then stops every syncer and returns.
// Before the first syncer starts it rebuilds the daemon-side occurrences
// when meta calendar.expand changed. A second, concurrent Run only waits
// for its ctx (core may reach the supervisor as the caldav kind's
// SyncSupervisor and directly).
func (sv *Supervisor) Run(ctx context.Context) {
	sv.mu.Lock()
	if sv.running || sv.stopped {
		sv.mu.Unlock()
		<-ctx.Done()
		return
	}
	sv.running = true
	early := slices.Clone(sv.queued)
	sv.mu.Unlock()

	sv.rebuildIfNeeded(ctx, early)

	sv.mu.Lock()
	sv.ctx = ctx
	queued := sv.queued
	sv.queued = nil
	sv.mu.Unlock()
	for _, a := range queued {
		sv.Start(a)
	}
	sv.wg.Add(1)
	go func() { defer sv.wg.Done(); sv.alerts.run(ctx) }()
	<-ctx.Done()

	sv.mu.Lock()
	sv.stopped = true
	entries := sv.entries
	sv.entries = map[string]*entry{}
	sv.mu.Unlock()
	for _, e := range entries {
		e.cancel()
	}
	for _, e := range entries {
		<-e.done
	}
	sv.wg.Wait()
	sv.notify.flushAll()
}

func (sv *Supervisor) rebuildIfNeeded(ctx context.Context, accounts []store.Account) {
	windows := map[string]Window{}
	now := sv.clock.Now()
	for _, a := range accounts {
		windows[a.ID] = WindowFor(now, KeepDays(a), sv.loc)
	}
	done, err := rebuildExpansions(ctx, sv.opts.Store, sv.opts.ExpandKey, now, sv.loc, func(id string) (Window, bool) {
		w, ok := windows[id]
		return w, ok
	})
	if err != nil {
		sv.log.Warn("rebuild of occurrences failed", "err", err)
	}
	byAccount := map[string][]string{}
	for _, c := range done {
		byAccount[c.AccountID] = append(byAccount[c.AccountID], c.ID)
	}
	for id, cals := range byAccount {
		sv.notify.calendarChanged(id, cals, span{unbounded: true})
	}
	if len(done) > 0 {
		sv.log.Info("occurrences rebuilt", "calendars", len(done), "expand", sv.opts.ExpandKey)
	}
}

// Start begins synchronising the account's calendars; a no-op for an
// account without them (HasCalendars) and for a running one.
func (sv *Supervisor) Start(a store.Account) {
	if !HasCalendars(a) {
		return
	}
	sv.mu.Lock()
	defer sv.mu.Unlock()
	if sv.stopped {
		return
	}
	if sv.ctx == nil {
		if !slices.ContainsFunc(sv.queued, func(q store.Account) bool { return q.ID == a.ID }) {
			sv.queued = append(sv.queued, a)
		}
		return
	}
	if _, ok := sv.entries[a.ID]; ok {
		return
	}
	ctx, cancel := context.WithCancel(sv.ctx)
	sv.seq++
	s := newSyncer(sv, a)
	e := &entry{seq: sv.seq, syncer: s, cancel: cancel, done: make(chan struct{})}
	sv.entries[a.ID] = e
	sv.wg.Add(1)
	go func() {
		defer sv.wg.Done()
		defer close(e.done)
		s.run(ctx)
	}()
	sv.alerts.kick()
}

// Stop ends the account's syncer, waits for it and forgets its state.
// The stored calendar rows stay (removing them is the core's, D23).
func (sv *Supervisor) Stop(accountID string) {
	sv.mu.Lock()
	e, ok := sv.entries[accountID]
	delete(sv.entries, accountID)
	sv.queued = slices.DeleteFunc(sv.queued, func(q store.Account) bool { return q.ID == accountID })
	sv.mu.Unlock()
	if ok {
		e.cancel()
		<-e.done
		sv.notify.forget(accountID)
		sv.alerts.kick()
	}
}

// Restart applies a changed configuration: Stop followed by Start.
func (sv *Supervisor) Restart(a store.Account) {
	sv.Stop(a.ID)
	sv.Start(a)
}

// Reload tells every syncer and the alert scheduler that preferences
// changed (working hours, the window of an account).
func (sv *Supervisor) Reload() {
	for _, e := range sv.snapshot() {
		e.syncer.wakeUp()
	}
	sv.alerts.kick()
}

// NetworkChanged wakes the syncers waiting while offline, so that a
// regained network does not wait for the backoff. The daemon has no
// network monitor today; core calls it when one exists (or never).
func (sv *Supervisor) NetworkChanged() {
	for _, e := range sv.snapshot() {
		if e.syncer.State().Status == api.CalendarSyncOffline {
			e.syncer.wakeUp()
		}
	}
}

// SyncNow asks for a pass now (calendar.sync): every account ("") or one,
// every calendar ("") or one, the calendar list included. false when the
// account has no running syncer.
func (sv *Supervisor) SyncNow(accountID, calendarID string) bool {
	if accountID == "" {
		for _, e := range sv.snapshot() {
			e.syncer.request("", false, true)
		}
		return true
	}
	e, ok := sv.entry(accountID)
	if !ok {
		return false
	}
	e.syncer.request(calendarID, false, calendarID == "")
	return true
}

// Touch asks for a pass of the calendar soon: a write answered and the
// server may have changed more than the write's result says.
func (sv *Supervisor) Touch(calendarID string) {
	c, err := sv.opts.Store.GetCalendar(context.Background(), calendarID)
	if err != nil {
		return
	}
	if e, ok := sv.entry(c.AccountID); ok {
		e.syncer.request(calendarID, false, false)
	}
}

// CalendarState returns the calendar state of a running syncer.
func (sv *Supervisor) CalendarState(accountID string) (api.CalendarSyncState, bool) {
	e, ok := sv.entry(accountID)
	if !ok {
		return api.CalendarSyncState{}, false
	}
	return e.syncer.State(), true
}

// CalendarStates returns the states of every running syncer, in start
// order.
func (sv *Supervisor) CalendarStates() []api.CalendarSyncState {
	var out []api.CalendarSyncState
	for _, e := range sv.snapshot() {
		out = append(out, e.syncer.State())
	}
	return out
}

// Provider returns the running syncer's provider (the writes of core use
// the same source); false before it has one.
func (sv *Supervisor) Provider(accountID string) (Provider, bool) {
	e, ok := sv.entry(accountID)
	if !ok {
		return nil, false
	}
	return e.syncer.provider()
}

// Window returns the current window of an account (running or not).
func (sv *Supervisor) Window(a store.Account) Window {
	return WindowFor(sv.clock.Now(), KeepDays(a), sv.loc)
}

// Location is the daemon's zone the engine works in.
func (sv *Supervisor) Location() *time.Location { return sv.loc }

// ApplyWrite stores what a write's server answer holds (WriteResult)
// in the calendar, as a pass would: objects replace their resources with
// their occurrences, Removed go; then notify.calendarChanged, the alerts
// and a pass of the calendar (Touch).
func (sv *Supervisor) ApplyWrite(ctx context.Context, a store.Account, calendarID string, res WriteResult) error {
	cal, err := sv.opts.Store.GetCalendar(ctx, calendarID)
	if err != nil {
		return err
	}
	if cal.AccountID != a.ID {
		return errors.New("calsync: the calendar is not the account's")
	}
	if Disabled(cal) {
		return api.NewError(api.CodeInvalidArgument, "calsync: the calendar is disabled")
	}
	sk := newSink(sv, a, cal, sv.Window(a))
	for _, obj := range res.Objects {
		if err := sk.Upsert(obj); err != nil {
			return err
		}
	}
	for _, id := range res.Removed {
		if err := sk.Remove(id); err != nil {
			return err
		}
	}
	sv.notify.calendarChanged(a.ID, []string{cal.ID}, sk.span)
	sv.alerts.changed(a.ID, sk.span)
	sv.Touch(calendarID)
	return nil
}

func (sv *Supervisor) entry(accountID string) (*entry, bool) {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	e, ok := sv.entries[accountID]
	return e, ok
}

func (sv *Supervisor) snapshot() []*entry {
	sv.mu.Lock()
	out := make([]*entry, 0, len(sv.entries))
	for _, e := range sv.entries {
		out = append(out, e)
	}
	sv.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].seq < out[j].seq })
	return out
}

// runningAccounts are the ids of the accounts with a running syncer.
func (sv *Supervisor) runningAccounts() []string {
	var out []string
	for _, e := range sv.snapshot() {
		out = append(out, e.syncer.account.ID)
	}
	return out
}

func (sv *Supervisor) preferences(ctx context.Context) api.CalendarPreferences {
	var p api.CalendarPreferences
	var err error
	if sv.opts.Preferences != nil {
		p, err = sv.opts.Preferences(ctx)
	} else {
		p, err = sv.opts.Store.CalendarPreferences(ctx)
	}
	if err != nil {
		return api.DefaultCalendarPreferences()
	}
	return p
}

// MailSupervisor is the engine as the SyncSupervisor of the caldav kind
// (core/dispatch.go): Start, Stop, Restart, Reload and Run go to the
// engine; Trigger, State and States see caldav accounts only, their
// calendar state mapped onto the mail statuses (MailState), so that a
// Graph account's mail state is never touched.
type MailSupervisor struct{ sv *Supervisor }

// Mail returns the caldav kind's SyncSupervisor surface.
func (sv *Supervisor) Mail() MailSupervisor { return MailSupervisor{sv: sv} }

func (m MailSupervisor) Run(ctx context.Context) { m.sv.Run(ctx) }
func (m MailSupervisor) Start(a store.Account)   { m.sv.Start(a) }
func (m MailSupervisor) Stop(accountID string)   { m.sv.Stop(accountID) }
func (m MailSupervisor) Restart(a store.Account) { m.sv.Restart(a) }
func (m MailSupervisor) Reload()                 { m.sv.Reload() }

// Trigger asks for a pass of a caldav account (the folder does not
// matter; full asks for a full reconcile).
func (m MailSupervisor) Trigger(accountID string, _ api.FolderID, full bool) bool {
	e, ok := m.sv.entry(accountID)
	if !ok || !e.syncer.caldav() {
		return false
	}
	e.syncer.request("", full, true)
	return true
}

func (m MailSupervisor) State(accountID string) (api.SyncState, bool) {
	e, ok := m.sv.entry(accountID)
	if !ok || !e.syncer.caldav() {
		return api.SyncState{}, false
	}
	return MailState(e.syncer.State()), true
}

func (m MailSupervisor) States() []api.SyncState {
	var out []api.SyncState
	for _, e := range m.sv.snapshot() {
		if e.syncer.caldav() {
			out = append(out, MailState(e.syncer.State()))
		}
	}
	return out
}

var _ interface {
	Run(ctx context.Context)
	Start(a store.Account)
	Stop(accountID string)
	Restart(a store.Account)
	Reload()
	Trigger(accountID string, folder api.FolderID, full bool) bool
	State(accountID string) (api.SyncState, bool)
	States() []api.SyncState
} = MailSupervisor{}
