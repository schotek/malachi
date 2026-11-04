// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendardata

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/client"
)

// The calendar from the daemon (docs/calendar.md §4.3–4.7, docs/api.md
// §4.14–4.16): account.list, calendar.list, calendar.preferences and
// invitation.list for the snapshot; event.list per range and event.get
// per event, both cached; every write passed through with its answer
// stored. A port of MalachiCore/Calendar/DaemonCalendarSource.swift.
//
// It subscribes to nothing itself: the window's notification fan-out
// calls Handle with each calendar notification, AccountsChanged on
// notify.accountsChanged and ConnectionChanged when the connection comes
// and goes; Start loads the first time.
//
//   - notify.calendarChanged drops every cached range overlapping its
//     from/to (absent: every range) and the cached events of that time,
//     tells the observers (ChangeOccurrences), and lists the calendars
//     again (and, without calendars named, the preferences: §4.14 says
//     so for calendar.setPreferences).
//   - notify.calendarState replaces the account's state in the snapshot
//     (consentRequired, authRequired… reach the sidebar this way).
//   - notify.eventAlert goes to the observers as it is.
//   - notify.invitationsChanged lists the calendars (the count) and the
//     unanswered invitations again.
//
// One snapshot load is on its way at a time: asked for again meanwhile it
// runs once more after the answer. A load the daemon could not answer is
// PhaseFailed and is asked again after board.RetryDelay; without a
// connection it is PhaseUnavailable until the connection comes back; a
// daemon without the calendar (methodNotFound) is PhaseUnsupported. A
// range asked for twice while its event.list is on its way waits for that
// one call. A range dropped while its call is on its way is not stored
// from that answer (and a range asked for after the drop gets a call of
// its own).
//
// calendar.update visible is optimistic: the snapshot shows the change at
// once (an overlay a calendar.list arriving meanwhile does not undo), the
// call follows, a refusal takes it back and says why (ChangeError). A
// write that answers conflict stores the event of its error data and
// tells the observers (ChangeEvent) before done gets the error.
//
// Every method runs on the main loop; the calls run on their own
// goroutines and every answer comes back through the board.Loop.

// The calls' time limits beyond DaemonOptions.CallTimeout (RPCTimeouts of
// the macOS client).
const (
	eventListTimeout = 10 * time.Second
	slowTimeout      = 20 * time.Second // freeBusy, findFreeTime, discoverCalDAV
	saveTimeout      = 30 * time.Second // account.add, account.update: the keyring may prompt
	partTimeout      = 60 * time.Second // attachment.import
)

// DaemonOptions tune a DaemonSource; the zero value is the defaults.
type DaemonOptions struct {
	// RetryDelay is board.RetryDelay when nil.
	RetryDelay func(attempt int) time.Duration
	// CallTimeout bounds every call not named above; board.DefaultCallTimeout
	// when 0.
	CallTimeout time.Duration
	// Loc is the zone of event.get and of the cached events' spans; nil =
	// time.Local.
	Loc *time.Location
	// Log may be nil. Only method names and errors are logged, never an
	// event's text.
	Log *slog.Logger
}

// DaemonSource is the calendar from the daemon (DaemonCalendarSource).
// Make it with NewDaemonSource, then Start.
type DaemonSource struct {
	caller      board.Caller
	loop        board.Loop
	log         *slog.Logger
	retryDelay  func(int) time.Duration
	callTimeout time.Duration
	loc         *time.Location

	snapshot Snapshot
	obs      observers
	// base is the daemon's data as last answered, without the overlays.
	base Snapshot
	// pendingVisible are the calendar.update visible under way.
	pendingVisible map[api.CalendarID]pendingVisible
	nextToken      int

	// ranges are event.list answers by Range.Key; rangeCalls the calls on
	// their way, with their generation and who waits.
	ranges          map[string]cachedRange
	rangeCalls      map[string]*rangeCall
	rangeGeneration int
	// events are event.get answers by EventKey.
	events map[string]api.Event

	started      bool
	loadInFlight bool
	loadAgain    bool
	// What the next load asks for besides the calendars.
	wantAccounts, wantPreferences, wantInvitations bool
	connGeneration                                 int
	// The retry after a failed load: armed, the token that cancels it, the
	// failures in a row.
	retryArmed bool
	retryToken int
	failures   int
	// calls counts the calls whose answer has not been handled.
	calls int
}

type pendingVisible struct {
	token   int
	visible bool
}

type cachedRange struct {
	r   Range
	occ []api.Occurrence
}

type rangeCall struct {
	generation int
	waiters    []func([]api.Occurrence, *api.Error)
}

// NewDaemonSource makes the daemon's source. It is in PhaseLoading and
// asks for nothing before Start.
func NewDaemonSource(c board.Caller, loop board.Loop, o DaemonOptions) *DaemonSource {
	d := &DaemonSource{
		caller: c, loop: loop, log: o.Log, retryDelay: o.RetryDelay, callTimeout: o.CallTimeout, loc: o.Loc,
		snapshot: EmptySnapshot(), base: EmptySnapshot(), pendingVisible: map[api.CalendarID]pendingVisible{},
		ranges: map[string]cachedRange{}, rangeCalls: map[string]*rangeCall{}, events: map[string]api.Event{},
		wantAccounts: true, wantPreferences: true, wantInvitations: true,
	}
	if d.log == nil {
		d.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if d.retryDelay == nil {
		d.retryDelay = board.RetryDelay
	}
	if d.callTimeout <= 0 {
		d.callTimeout = board.DefaultCallTimeout
	}
	if d.loc == nil {
		d.loc = time.Local
	}
	return d
}

// Snapshot implements DataSource.
func (d *DaemonSource) Snapshot() Snapshot { return d.snapshot }

// Observe implements DataSource.
func (d *DaemonSource) Observe(f func(Change)) *board.ObserverToken { return d.obs.add(f) }

// idle reports that nothing is on its way (a load, a call, a cached
// answer posted): what the tests wait for. A retry waiting is not counted.
func (d *DaemonSource) idle() bool { return d.calls == 0 && !d.loadInFlight }

// later hands f to the loop, counted as on its way until it runs.
func (d *DaemonSource) later(f func()) {
	d.calls++
	post(d.loop, func() {
		d.calls--
		f()
	})
}

// Start loads the snapshot.
func (d *DaemonSource) Start() {
	d.started = true
	d.Refresh()
}

// Stop stops: no load is made and no load's or range's answer is taken
// any more (a write's answer still is: it ends what the user did).
func (d *DaemonSource) Stop() {
	d.started = false
	d.connGeneration++
	d.loadInFlight = false
	d.loadAgain = false
	d.cancelRetry()
	d.rangeGeneration++
	d.rangeCalls = map[string]*rangeCall{}
}

// AccountsChanged is notify.accountsChanged (an account.update, e.g.
// calendars switched off in Preferences): the accounts and calendars
// again at once.
func (d *DaemonSource) AccountsChanged() {
	if !d.started {
		return
	}
	d.Refresh()
}

// ConnectionChanged is the connection coming (everything again, the
// caches dropped) or going (the snapshot stays, in PhaseUnavailable).
func (d *DaemonSource) ConnectionChanged(connected bool) {
	if !d.started {
		return
	}
	d.connGeneration++
	d.loadInFlight = false
	d.loadAgain = false
	d.cancelRetry()
	d.failures = 0
	if connected {
		d.dropRanges(nil, nil, nil)
		d.Refresh()
		return
	}
	d.base.Phase = PhaseUnavailable
	d.publish()
}

// Refresh implements DataSource: everything of the snapshot, now (a retry
// waiting is brought forward).
func (d *DaemonSource) Refresh() {
	if !d.started {
		return
	}
	d.wantAccounts = true
	d.wantPreferences = true
	d.wantInvitations = true
	d.load()
}

// Handle implements DataSource.
func (d *DaemonSource) Handle(method string, raw json.RawMessage) bool {
	if !IsCalendarNotification(method) {
		return false
	}
	if !d.started {
		return true
	}
	switch method {
	case api.NotifyCalendarChanged:
		var n api.CalendarChangedNotification
		if json.Unmarshal(raw, &n) != nil {
			return true
		}
		d.dropRanges(n.From, n.To, n.CalendarIDs)
		if len(n.CalendarIDs) == 0 {
			d.wantPreferences = true
		}
		d.load()
	case api.NotifyCalendarState:
		var n api.CalendarStateNotification
		if json.Unmarshal(raw, &n) != nil {
			return true
		}
		for i := range d.base.CalendarAccounts {
			a := &d.base.CalendarAccounts[i]
			if a.AccountID != n.State.AccountID {
				continue
			}
			if !reflect.DeepEqual(a.State, n.State) {
				a.State = n.State
				d.publish()
			}
			break
		}
	case api.NotifyEventAlert:
		var n api.EventAlertNotification
		if json.Unmarshal(raw, &n) != nil {
			return true
		}
		d.obs.notify(Change{Kind: ChangeAlert, Alert: n.Alert})
	case api.NotifyInvitationsChanged:
		var n api.InvitationsChangedNotification
		if len(raw) > 0 && json.Unmarshal(raw, &n) != nil {
			return true
		}
		d.wantInvitations = true
		d.load()
	}
	return true
}

// ---------------------------------------------------------------------------
// The snapshot
// ---------------------------------------------------------------------------

// loaded is what one load brought.
type loaded struct {
	accounts    *[]api.Account
	calendars   *api.CalendarListResult
	preferences *api.CalendarPreferences
	invitations *[]api.InvitationListItem
	err         error
}

func (d *DaemonSource) load() {
	if !d.started {
		return
	}
	if d.loadInFlight {
		d.loadAgain = true
		return
	}
	d.cancelRetry()
	d.loadInFlight = true
	accounts := d.wantAccounts || len(d.base.Accounts) == 0
	prefs := d.wantPreferences
	invitations := d.wantInvitations
	d.wantAccounts, d.wantPreferences, d.wantInvitations = false, false, false
	generation := d.connGeneration
	timeout := d.callTimeout
	caller := d.caller
	go func() {
		var o loaded
		ask := func(method string, params, result any) bool {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			o.err = caller.Call(ctx, method, params, result)
			return o.err == nil
		}
		func() {
			if accounts {
				var r api.AccountListResult
				if !ask(api.MethodAccountList, api.AccountListParams{}, &r) {
					return
				}
				o.accounts = &r.Accounts
			}
			var c api.CalendarListResult
			if !ask(api.MethodCalendarList, api.CalendarListParams{}, &c) {
				return
			}
			o.calendars = &c
			if prefs {
				var r api.CalendarPreferencesResult
				if !ask(api.MethodCalendarPreferences, api.CalendarPreferencesParams{}, &r) {
					return
				}
				o.preferences = &r.Preferences
			}
			if invitations {
				var r api.InvitationListResult
				if !ask(api.MethodInvitationList, api.InvitationListParams{State: api.InvitationUnanswered}, &r) {
					return
				}
				o.invitations = &r.Invitations
			}
		}()
		post(d.loop, func() {
			if generation != d.connGeneration || !d.started {
				return
			}
			d.loadInFlight = false
			d.apply(o, accounts, prefs, invitations)
			if d.loadAgain {
				d.loadAgain = false
				d.load()
			}
		})
	}()
}

func (d *DaemonSource) apply(o loaded, askedAccounts, askedPreferences, askedInvitations bool) {
	if o.accounts != nil {
		d.base.Accounts = *o.accounts
	}
	if o.calendars != nil {
		d.base.CalendarAccounts = o.calendars.Accounts
		d.base.Unanswered = o.calendars.Unanswered
	}
	if o.preferences != nil {
		d.base.Preferences = *o.preferences
	}
	if o.invitations != nil {
		d.base.Invitations = *o.invitations
	}
	if o.err == nil {
		d.failures = 0
		d.base.Phase = PhaseReady
		d.publish()
		return
	}
	d.log.Info("calendar load failed", "err", o.err)
	// What was not asked successfully is asked again next time.
	d.wantAccounts = d.wantAccounts || (askedAccounts && o.accounts == nil)
	d.wantPreferences = d.wantPreferences || (askedPreferences && o.preferences == nil)
	d.wantInvitations = d.wantInvitations || (askedInvitations && o.invitations == nil)
	d.base.Phase = phaseOf(o.err)
	d.publish()
	if d.base.Phase == PhaseFailed {
		d.armRetry()
	}
}

// phaseOf is the phase a load's failure leaves: no connection
// (the reconnect loads again), a daemon without the calendar, or a
// failure that is asked again after the back-off.
func phaseOf(err error) Phase {
	if errors.Is(err, client.ErrDisconnected) {
		return PhaseUnavailable
	}
	var e *api.Error
	if errors.As(err, &e) && (e.Code == api.CodeMethodNotFound || e.Code == api.CodeNotImplemented) {
		return PhaseUnsupported
	}
	return PhaseFailed
}

func (d *DaemonSource) armRetry() {
	d.failures++
	delay := d.retryDelay(d.failures)
	d.cancelRetry()
	d.retryArmed = true
	token := d.retryToken
	d.loop.After(delay, func() {
		if !d.retryArmed || token != d.retryToken {
			return
		}
		d.retryArmed = false
		d.load()
	})
}

func (d *DaemonSource) cancelRetry() {
	d.retryArmed = false
	d.retryToken++
}

// publish lays the overlays over base; tells the observers when the
// snapshot changed.
func (d *DaemonSource) publish() {
	s := d.base.clone()
	for id, p := range d.pendingVisible {
		for ai := range s.CalendarAccounts {
			for ci := range s.CalendarAccounts[ai].Calendars {
				if s.CalendarAccounts[ai].Calendars[ci].ID == id {
					s.CalendarAccounts[ai].Calendars[ci].Visible = p.visible
				}
			}
		}
	}
	if s.Equal(d.snapshot) {
		return
	}
	d.snapshot = s
	d.obs.notify(Change{Kind: ChangeSnapshot})
}

// replaceCalendar puts a calendar.update's answer into base.
func (d *DaemonSource) replaceCalendar(c api.Calendar) {
	for ai := range d.base.CalendarAccounts {
		for ci := range d.base.CalendarAccounts[ai].Calendars {
			if d.base.CalendarAccounts[ai].Calendars[ci].ID == c.ID {
				d.base.CalendarAccounts[ai].Calendars[ci] = c
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Ranges and events
// ---------------------------------------------------------------------------

// dropRanges drops the cached ranges overlapping [from, to) and, when
// calendars are named, every cached range holding an occurrence of one of
// them (a calendar switched off, emptied or synchronised again); the
// observers then hear of every range when one outside the bounds went.
// The calls on their way are not stored from their answer.
func (d *DaemonSource) dropRanges(from, to *time.Time, calendars []api.CalendarID) {
	d.rangeGeneration++
	d.rangeCalls = map[string]*rangeCall{}
	named := make(map[api.CalendarID]bool, len(calendars))
	for _, c := range calendars {
		named[c] = true
	}
	outside := false
	for k, v := range d.ranges {
		if RangesOverlap(v.r.From, v.r.To, from, to) {
			delete(d.ranges, k)
			continue
		}
		if len(named) > 0 {
			for _, o := range v.occ {
				if o.CalendarID != "" && named[o.CalendarID] {
					delete(d.ranges, k)
					outside = true
					break
				}
			}
		}
	}
	for k, e := range d.events {
		if e.CalendarID != "" && named[e.CalendarID] {
			delete(d.events, k)
			continue
		}
		if from == nil && to == nil {
			delete(d.events, k)
			continue
		}
		s, end, ok := calendar.Span(e.Occurrence, d.loc)
		if !ok || RangesOverlap(s, end, from, to) {
			delete(d.events, k)
		}
	}
	if outside {
		d.obs.notify(Change{Kind: ChangeOccurrences})
		return
	}
	d.obs.notify(Change{Kind: ChangeOccurrences, From: from, To: to})
}

// CachedOccurrences implements DataSource.
func (d *DaemonSource) CachedOccurrences(r Range) ([]api.Occurrence, bool) {
	c, ok := d.ranges[r.Key()]
	if !ok {
		return nil, false
	}
	return c.occ, true
}

// Occurrences implements DataSource.
func (d *DaemonSource) Occurrences(r Range, done func([]api.Occurrence, *api.Error)) {
	key := r.Key()
	if c, ok := d.ranges[key]; ok {
		d.later(func() { call(done, c.occ, nil) })
		return
	}
	if running, ok := d.rangeCalls[key]; ok {
		running.waiters = append(running.waiters, done)
		return
	}
	rc := &rangeCall{generation: d.rangeGeneration, waiters: []func([]api.Occurrence, *api.Error){done}}
	d.rangeCalls[key] = rc
	yes := true
	params := api.EventListParams{From: r.From, To: r.To, TimeZone: r.Zone(), IncludeInvitations: &yes}
	res := new(api.EventListResult)
	d.call(api.MethodEventList, params, res, eventListTimeout, func(err error) {
		if d.rangeCalls[key] == rc {
			delete(d.rangeCalls, key)
		}
		var occ []api.Occurrence
		var e *api.Error
		if err != nil {
			d.log.Info("event.list failed", "err", err)
			e = AsError(err)
		} else {
			occ = res.Occurrences
			if occ == nil {
				occ = []api.Occurrence{}
			}
			if rc.generation == d.rangeGeneration && d.started {
				d.ranges[key] = cachedRange{r: r, occ: occ}
			}
		}
		for _, w := range rc.waiters {
			call(w, occ, e)
		}
	})
}

// Event implements DataSource.
func (d *DaemonSource) Event(id api.EventID, rec api.DateOrTime, reload bool, done func(*api.Event, *api.Error)) {
	key := EventKey(id, rec)
	if e, ok := d.events[key]; ok && !reload {
		d.later(func() { call(done, &e, nil) })
		return
	}
	res := new(api.EventGetResult)
	d.call(api.MethodEventGet, api.EventGetParams{EventID: id, RecurrenceID: rec, TimeZone: ZoneName(d.loc)}, res, 0,
		func(err error) {
			if err != nil {
				call(done, nil, AsError(err))
				return
			}
			e := res.Event
			old, had := d.events[key]
			changed := !had || old.ETag != e.ETag
			d.events[key] = e
			if changed && reload {
				d.obs.notify(Change{Kind: ChangeEvent, Event: id})
			}
			call(done, &e, nil)
		})
}

// store keeps a write's (or a conflict's) event and tells the observers.
func (d *DaemonSource) store(e api.Event, rec api.DateOrTime) {
	if e.EventID == "" {
		return
	}
	if e.RecurrenceID != "" {
		rec = e.RecurrenceID
	}
	d.events[EventKey(e.EventID, rec)] = e
	d.obs.notify(Change{Kind: ChangeEvent, Event: e.EventID})
}

// conflict stores the current event a conflict carries.
func (d *DaemonSource) conflict(e *api.Error, rec api.DateOrTime) {
	if ev, ok := ConflictEvent(e); ok {
		d.store(ev, rec)
	}
}

// ConflictEvent is the current event in a conflict's data
// (api.ConflictEventData); false for another error or a conflict without
// one.
func ConflictEvent(e *api.Error) (api.Event, bool) {
	if e == nil || e.Code != api.CodeConflict || e.Data == nil {
		return api.Event{}, false
	}
	var c api.ConflictEventData
	switch v := e.Data.(type) {
	case api.ConflictEventData:
		c = v
	case *api.ConflictEventData:
		c = *v
	default:
		raw, err := json.Marshal(v)
		if err != nil || json.Unmarshal(raw, &c) != nil {
			return api.Event{}, false
		}
	}
	if c.Event.EventID == "" {
		return api.Event{}, false
	}
	return c.Event, true
}

// Search implements DataSource.
func (d *DaemonSource) Search(p api.EventSearchParams, done func(*api.EventSearchResult, *api.Error)) {
	ask(d, api.MethodEventSearch, p, 0, nil, done)
}

// FreeBusy implements DataSource.
func (d *DaemonSource) FreeBusy(p api.CalendarFreeBusyParams, done func(*api.CalendarFreeBusyResult, *api.Error)) {
	ask(d, api.MethodCalendarFreeBusy, p, slowTimeout, nil, done)
}

// FindFreeTime implements DataSource.
func (d *DaemonSource) FindFreeTime(p api.CalendarFindFreeTimeParams, done func(*api.CalendarFindFreeTimeResult, *api.Error)) {
	ask(d, api.MethodCalendarFindFreeTime, p, slowTimeout, nil, done)
}

// ---------------------------------------------------------------------------
// Writes
// ---------------------------------------------------------------------------

// call runs one call on its own goroutine (timeout 0 = the default) and
// hands its error to done on the main loop; result is filled by then.
func (d *DaemonSource) call(method string, params, result any, timeout time.Duration, done func(error)) {
	if timeout <= 0 {
		timeout = d.callTimeout
	}
	d.calls++
	caller := d.caller
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		err := caller.Call(ctx, method, params, result)
		post(d.loop, func() {
			d.calls--
			done(err)
		})
	}()
}

// ask is one call whose answer then (may be nil) takes into the caches
// before done gets it; on failure then gets nil and the error.
func ask[R any](d *DaemonSource, method string, params any, timeout time.Duration, then func(*R, *api.Error), done func(*R, *api.Error)) {
	r := new(R)
	d.call(method, params, r, timeout, func(err error) {
		e := AsError(err)
		if e != nil {
			d.log.Info("calendar call failed", "method", method, "err", err)
			r = nil
		}
		if then != nil {
			then(r, e)
		}
		call(done, r, e)
	})
}

// askErr is ask for a done without a result.
func askErr[R any](d *DaemonSource, method string, params any, timeout time.Duration, then func(*R, *api.Error), done func(*api.Error)) {
	var wrapped func(*R, *api.Error)
	if done != nil {
		wrapped = func(_ *R, e *api.Error) { done(e) }
	}
	ask(d, method, params, timeout, then, wrapped)
}

// SetVisible implements DataSource.
func (d *DaemonSource) SetVisible(id api.CalendarID, visible bool) {
	c, ok := d.snapshot.Calendar(id)
	if !ok || c.Visible == visible {
		return
	}
	d.nextToken++
	token := d.nextToken
	d.pendingVisible[id] = pendingVisible{token: token, visible: visible}
	d.publish()
	d.obs.notify(Change{Kind: ChangeOccurrences})
	v := visible
	res := new(api.CalendarUpdateResult)
	d.call(api.MethodCalendarUpdate, api.CalendarUpdateParams{CalendarID: id, Visible: &v}, res, 0, func(err error) {
		if p, ok := d.pendingVisible[id]; !ok || p.token != token {
			return
		}
		delete(d.pendingVisible, id)
		if err == nil {
			d.replaceCalendar(res.Calendar)
		}
		d.publish()
		if err != nil {
			d.log.Info("calendar.update failed", "err", err)
			d.obs.notify(Change{Kind: ChangeOccurrences})
			d.obs.notify(Change{Kind: ChangeError, Err: AsError(err)})
		}
	})
}

// SetColor implements DataSource.
func (d *DaemonSource) SetColor(id api.CalendarID, color string, done func(*api.Calendar, *api.Error)) {
	c := color
	ask(d, api.MethodCalendarUpdate, api.CalendarUpdateParams{CalendarID: id, UserColor: &c}, 0,
		func(r *api.CalendarUpdateResult, e *api.Error) {
			if e == nil {
				d.replaceCalendar(r.Calendar)
				d.publish()
			}
		}, func(r *api.CalendarUpdateResult, e *api.Error) {
			if done == nil {
				return
			}
			if e != nil {
				done(nil, e)
				return
			}
			done(&r.Calendar, nil)
		})
}

// Sync implements DataSource.
func (d *DaemonSource) Sync(p api.CalendarSyncParams, done func(*api.Error)) {
	askErr[api.CalendarSyncResult](d, api.MethodCalendarSync, p, 0, nil, done)
}

// SetPreferences implements DataSource.
func (d *DaemonSource) SetPreferences(u api.CalendarPreferencesUpdate, done func(*api.CalendarPreferences, *api.Error)) {
	ask(d, api.MethodCalendarSetPreferences, api.CalendarSetPreferencesParams{Preferences: u}, 0,
		func(r *api.CalendarSetPreferencesResult, e *api.Error) {
			if e == nil {
				d.base.Preferences = r.Preferences
				d.publish()
			}
		}, func(r *api.CalendarSetPreferencesResult, e *api.Error) {
			if done == nil {
				return
			}
			if e != nil {
				done(nil, e)
				return
			}
			done(&r.Preferences, nil)
		})
}

// CreateEvent implements DataSource.
func (d *DaemonSource) CreateEvent(p api.EventCreateParams, done func(*api.EventCreateResult, *api.Error)) {
	ask(d, api.MethodEventCreate, p, 0, func(r *api.EventCreateResult, e *api.Error) {
		if e == nil {
			d.store(r.Event, "")
		}
	}, done)
}

// UpdateEvent implements DataSource.
func (d *DaemonSource) UpdateEvent(p api.EventUpdateParams, done func(*api.EventUpdateResult, *api.Error)) {
	ask(d, api.MethodEventUpdate, p, 0, func(r *api.EventUpdateResult, e *api.Error) {
		if e != nil {
			d.conflict(e, p.RecurrenceID)
			return
		}
		d.store(r.Event, p.RecurrenceID)
	}, done)
}

// DeleteEvent implements DataSource.
func (d *DaemonSource) DeleteEvent(p api.EventDeleteParams, done func(*api.EventDeleteResult, *api.Error)) {
	ask(d, api.MethodEventDelete, p, 0, func(_ *api.EventDeleteResult, e *api.Error) {
		if e != nil {
			d.conflict(e, p.RecurrenceID)
			return
		}
		delete(d.events, EventKey(p.EventID, p.RecurrenceID))
		d.obs.notify(Change{Kind: ChangeEvent, Event: p.EventID})
	}, done)
}

// Respond implements DataSource.
func (d *DaemonSource) Respond(p api.EventRespondParams, done func(*api.EventRespondResult, *api.Error)) {
	ask(d, api.MethodEventRespond, p, 0, func(r *api.EventRespondResult, e *api.Error) {
		if e != nil {
			d.conflict(e, p.RecurrenceID)
			return
		}
		d.store(r.Event, p.RecurrenceID)
	}, done)
}

// SnoozeAlert implements DataSource.
func (d *DaemonSource) SnoozeAlert(p api.EventSnoozeAlertParams, done func(*api.Error)) {
	askErr[api.EventSnoozeAlertResult](d, api.MethodEventSnoozeAlert, p, 0, nil, done)
}

// DismissAlert implements DataSource.
func (d *DaemonSource) DismissAlert(p api.EventDismissAlertParams, done func(*api.Error)) {
	askErr[api.EventDismissAlertResult](d, api.MethodEventDismissAlert, p, 0, nil, done)
}

// RespondToInvitation implements DataSource.
func (d *DaemonSource) RespondToInvitation(p api.InvitationRespondParams, done func(*api.InvitationRespondResult, *api.Error)) {
	ask(d, api.MethodInvitationRespond, p, 0, nil, done)
}

// ApplyCancel implements DataSource.
func (d *DaemonSource) ApplyCancel(p api.InvitationApplyCancelParams, done func(*api.InvitationApplyCancelResult, *api.Error)) {
	ask(d, api.MethodInvitationApplyCancel, p, 0, nil, done)
}

// DiscoverCalDAV implements DataSource.
func (d *DaemonSource) DiscoverCalDAV(p api.AccountDiscoverCalDAVParams, done func(*api.AccountDiscoverCalDAVResult, *api.Error)) {
	ask(d, api.MethodAccountDiscoverCalDAV, p, slowTimeout, nil, done)
}

// ListCalDAVCalendars implements DataSource.
func (d *DaemonSource) ListCalDAVCalendars(p api.AccountListCalDAVCalendarsParams, done func(*api.AccountListCalDAVCalendarsResult, *api.Error)) {
	ask(d, api.MethodAccountListCalDAVCalendars, p, 0, nil, done)
}

// AddAccount implements DataSource: the snapshot is loaded again after.
func (d *DaemonSource) AddAccount(p api.AccountAddParams, done func(*api.AccountAddResult, *api.Error)) {
	ask(d, api.MethodAccountAdd, p, saveTimeout, func(_ *api.AccountAddResult, e *api.Error) {
		if e == nil {
			d.Refresh()
		}
	}, done)
}

// UpdateAccount implements DataSource: the snapshot is loaded again after.
func (d *DaemonSource) UpdateAccount(p api.AccountUpdateParams, done func(*api.Error)) {
	askErr(d, api.MethodAccountUpdate, p, saveTimeout, func(_ *api.AccountUpdateResult, e *api.Error) {
		if e == nil {
			d.Refresh()
		}
	}, done)
}

// ImportAttachment implements DataSource.
func (d *DaemonSource) ImportAttachment(p api.AttachmentImportParams, done func(*api.AttachmentImportResult, *api.Error)) {
	ask(d, api.MethodAttachmentImport, p, partTimeout, nil, done)
}

var _ DataSource = (*DaemonSource)(nil)
