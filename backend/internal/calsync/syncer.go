// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package calsync

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/calendar"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Cadence (docs/calendar.md §3.7).
const (
	graphDefaultEvery = 2 * time.Minute  // a Graph account's default calendar
	graphOtherEvery   = 10 * time.Minute // its other calendars
	caldavEvery       = 5 * time.Minute  // every CalDAV calendar (the ctag makes it cheap)
	listEvery         = 10 * time.Minute // the calendar list itself
	fullEvery         = 24 * time.Hour   // a full reconcile per calendar
	backoffMin        = 30 * time.Second
	backoffMax        = time.Hour
	backoffJitter     = 0.1
	maxNameBytes      = 256
	maxRemoteIDBytes  = 2048
)

// syncer synchronises one account's calendars: one goroutine (run) owns
// the passes; request, wakeUp and State are safe from any goroutine.
type syncer struct {
	sv      *Supervisor
	account store.Account
	daemon  bool // the daemon expands the account's objects (every source but Graph)
	log     *slog.Logger
	wake    chan struct{}

	mu    sync.Mutex
	state api.CalendarSyncState
	prov  Provider
	req   request
	// notifiedAuth is the code of the last notify.authRequired (caldav).
	notifiedAuth api.ErrorCode

	// Owned by run.
	next       map[string]time.Time // calendar id → when its next pass is due
	lastFull   map[string]time.Time
	failures   map[string]int
	listedAt   time.Time
	windowFrom string // the window start the stored data was last moved to
	// versionChecked: the provider's DataVersion was compared with the
	// recorded one since the provider was made.
	versionChecked bool
}

// request is what SyncNow, Touch and Trigger asked for.
type request struct {
	list bool            // list the calendars again
	all  bool            // every calendar now
	full bool            // a full reconcile of what is synchronised
	cals map[string]bool // these calendars now
}

func newSyncer(sv *Supervisor, a store.Account) *syncer {
	s := &syncer{
		sv: sv, account: a, daemon: a.Config.Protocol() != api.AccountGraph,
		log:      sv.log.With("account", a.ID),
		wake:     make(chan struct{}, 1),
		state:    api.CalendarSyncState{AccountID: api.AccountID(a.ID), Status: api.CalendarSyncIdle, Progress: -1},
		next:     map[string]time.Time{},
		lastFull: map[string]time.Time{},
		failures: map[string]int{},
	}
	s.req.cals = map[string]bool{}
	return s
}

func (s *syncer) caldav() bool { return s.account.Config.Protocol() == api.AccountCalDAV }

// State returns the live state.
func (s *syncer) State() api.CalendarSyncState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *syncer) provider() (Provider, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prov, s.prov != nil
}

// request queues a pass: of one calendar, or ("") of every one; list
// also lists the calendars again; full reconciles.
func (s *syncer) request(calendarID string, full, list bool) {
	s.mu.Lock()
	if calendarID == "" {
		s.req.all = true
	} else {
		s.req.cals[calendarID] = true
	}
	s.req.full = s.req.full || full
	s.req.list = s.req.list || list
	s.mu.Unlock()
	s.wakeUp()
}

func (s *syncer) wakeUp() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *syncer) takeRequest() request {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.req
	s.req = request{cals: map[string]bool{}}
	return r
}

func (s *syncer) pending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.req.all || s.req.list || s.req.full || len(s.req.cals) > 0
}

// setState applies a change and announces it (notify.calendarState; for
// a caldav account also notify.syncState with the mail statuses).
func (s *syncer) setState(mut func(st *api.CalendarSyncState)) {
	s.mu.Lock()
	prev := s.state
	mut(&s.state)
	s.state.AccountID = api.AccountID(s.account.ID)
	cur := s.state
	s.mu.Unlock()
	if sameCalendarState(prev, cur) {
		return
	}
	s.sv.notify.state(cur)
	if s.caldav() && s.sv.opts.Notifier != nil {
		s.sv.opts.Notifier.SyncState(api.SyncStateNotification{State: MailState(cur)})
	}
}

func (s *syncer) now() time.Time { return s.sv.clock.Now() }

// run drives the account until ctx ends.
func (s *syncer) run(ctx context.Context) {
	attempt := 0
	for ctx.Err() == nil {
		err := s.ensureProvider(ctx)
		if err == nil {
			err = s.pass(ctx)
		}
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			s.fail(err)
			s.sleep(ctx, s.backoff(attempt))
			attempt++
			continue
		}
		attempt = 0
		s.waitNext(ctx)
	}
}

func (s *syncer) ensureProvider(ctx context.Context) error {
	if _, ok := s.provider(); ok {
		return nil
	}
	if s.sv.opts.Providers == nil {
		return api.NewError(api.CodeUnavailable, "calsync: no calendar source")
	}
	p, err := s.sv.opts.Providers(ctx, s.account)
	if err != nil {
		return err
	}
	if p == nil {
		return api.NewError(api.CodeUnavailable, "calsync: no calendar source")
	}
	s.mu.Lock()
	s.prov = p
	s.mu.Unlock()
	s.versionChecked = false
	return nil
}

// checkVersion compares a Versioned provider's DataVersion with the one
// recorded in meta calendar.provider.<account>; when it differs (or none
// is recorded), every calendar's sync state is cleared — token, ctag,
// sync mode, window — so that the next pass lists everything again and
// every object replaces its stored rows (user columns kept), and the new
// version is recorded. Returns whether a reset happened.
func (s *syncer) checkVersion(ctx context.Context, cals []store.Calendar) (bool, error) {
	if s.versionChecked {
		return false, nil
	}
	prov, _ := s.provider()
	v, ok := prov.(Versioned)
	if !ok {
		s.versionChecked = true
		return false, nil
	}
	st := s.sv.opts.Store
	key := MetaProviderVersion(s.account.ID)
	want := strconv.Itoa(v.DataVersion())
	have, found, err := st.GetMeta(ctx, key)
	if err != nil {
		return false, err
	}
	if found && have == want {
		s.versionChecked = true
		return false, nil
	}
	for i := range cals {
		cals[i].CalendarSyncState = store.CalendarSyncState{}
		if err := st.SetCalendarSyncState(ctx, cals[i].ID, cals[i].CalendarSyncState); err != nil && !errors.Is(err, store.ErrNotFound) {
			return false, err
		}
	}
	if err := st.SetMeta(ctx, key, want); err != nil {
		return false, err
	}
	s.log.Info("provider data version changed: full re-list", "from", have, "to", want)
	s.versionChecked = true
	return true, nil
}

// statusOf classifies an error: offline, authRequired, consentRequired
// (the account waits with backoff) or error.
func statusOf(err error) (api.CalendarSyncStatus, *api.Error) {
	var ae *api.Error
	if !errors.As(err, &ae) {
		if errors.Is(err, ErrUnsupported) {
			ae = api.NewError(api.CodeUnavailable, "calsync: not supported by this calendar source")
		} else {
			ae = api.NewError(api.CodeStorageError, "calsync: %s", cleanMessage(err.Error()))
		}
	}
	switch ae.Code {
	case api.CodeOffline, api.CodeNetworkError, api.CodeServerTimeout, api.CodeTLSError:
		return api.CalendarSyncOffline, ae
	case api.CodeAuthFailed, api.CodeAuthRequired:
		return api.CalendarSyncAuthRequired, ae
	case api.CodeCalendarConsentRequired:
		return api.CalendarSyncConsentRequired, ae
	}
	return api.CalendarSyncError, ae
}

// accountWide reports whether an error of one calendar stops the whole
// pass (the network, the sign-in).
func accountWide(err error) bool {
	st, _ := statusOf(err)
	return st != api.CalendarSyncError
}

func cleanMessage(s string) string { return calendar.CleanLine(s, 300) }

// fail degrades the state; a caldav account's refused credentials are
// announced once per transition (notify.authRequired).
func (s *syncer) fail(err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	status, ae := statusOf(err)
	if ae.Code == api.CodeCancelled {
		return
	}
	s.log.Warn("calendar sync failed", "status", status, "code", ae.Code, "err", ae.Message)
	if status != api.CalendarSyncError {
		// The source must be made again (a new token, a new sign-in).
		s.mu.Lock()
		s.prov = nil
		s.mu.Unlock()
	}
	s.setState(func(st *api.CalendarSyncState) { st.Status, st.Progress, st.Error = status, -1, ae })
	if status != api.CalendarSyncAuthRequired || !s.caldav() || s.sv.opts.Notifier == nil {
		return
	}
	s.mu.Lock()
	already := s.notifiedAuth == ae.Code
	s.notifiedAuth = ae.Code
	s.mu.Unlock()
	if !already {
		s.sv.opts.Notifier.AuthRequired(api.AuthRequiredNotification{AccountID: api.AccountID(s.account.ID), Reason: ae.Code, Message: ae.Message})
	}
}

func (s *syncer) backoff(attempt int) time.Duration {
	return backoffDelay(s.sv.opts.Backoff, attempt)
}

func backoffDelay(override func(int) time.Duration, attempt int) time.Duration {
	if override != nil {
		return override(attempt)
	}
	d := backoffMin
	for i := 0; i < attempt && d < backoffMax; i++ {
		d *= 2
	}
	d = min(d, backoffMax)
	return time.Duration(float64(d) * (1 + (rand.Float64()*2-1)*backoffJitter))
}

// sleep waits d, a wake or the end of ctx.
func (s *syncer) sleep(ctx context.Context, d time.Duration) {
	timer := make(chan struct{}, 1)
	stop := s.sv.clock.AfterFunc(d, func() { timer <- struct{}{} })
	defer stop()
	select {
	case <-ctx.Done():
	case <-s.wake:
	case <-timer:
	}
}

// waitNext waits until a calendar is due, a request or the end of ctx.
func (s *syncer) waitNext(ctx context.Context) {
	if s.pending() {
		return
	}
	now := s.now()
	next := s.listedAt.Add(listEvery)
	for _, t := range s.next {
		if t.Before(next) {
			next = t
		}
	}
	// The window moves at local midnight.
	l := now.In(s.sv.loc)
	midnight := time.Date(l.Year(), l.Month(), l.Day()+1, 0, 0, 0, 0, s.sv.loc)
	if midnight.Before(next) {
		next = midnight
	}
	d := next.Sub(now)
	if d <= 0 {
		return
	}
	s.sleep(ctx, d)
}

// interval is the cadence of a calendar.
func (s *syncer) interval(c store.Calendar) time.Duration {
	if s.daemon {
		return caldavEvery
	}
	if c.ServerDefault {
		return graphDefaultEvery
	}
	return graphOtherEvery
}

// pass moves the window when the day changed, lists the calendars when
// due and synchronises every calendar that is due. An error returned is
// account-wide; an error of one calendar is its state and the account's
// error status until a pass succeeds.
func (s *syncer) pass(ctx context.Context) error {
	st := s.sv.opts.Store
	req := s.takeRequest()
	now := s.now()
	win := WindowFor(now, KeepDays(s.account), s.sv.loc)
	if from, _ := windowDates(win, s.sv.loc); from != s.windowFrom {
		touched, err := moveWindow(ctx, st, s.account.ID, win, s.sv.loc)
		if err != nil {
			s.putBack(req)
			return err
		}
		if s.windowFrom != "" {
			s.sv.notify.calendarChanged(s.account.ID, touched, span{unbounded: true})
		}
		s.windowFrom = from
	}
	listing := req.list || s.listedAt.IsZero() || !now.Before(s.listedAt.Add(listEvery))
	var cals []store.Calendar
	var err error
	if listing {
		s.setState(func(st *api.CalendarSyncState) { st.Status, st.Progress = api.CalendarSyncSyncing, 0 })
		cals, err = s.listCalendars(ctx, now)
	} else {
		cals, err = st.ListCalendars(ctx, s.account.ID)
		cals = slices.DeleteFunc(cals, Disabled)
	}
	if err != nil {
		s.putBack(req)
		return err
	}
	if reset, err := s.checkVersion(ctx, cals); err != nil {
		s.putBack(req)
		return err
	} else if reset {
		req.full = true
	}
	var due []store.Calendar
	for _, c := range cals {
		if req.all || req.full || req.cals[c.ID] || !now.Before(s.next[c.ID]) {
			due = append(due, c)
		}
	}
	if len(due) == 0 {
		if listing {
			s.finish(now, nil)
		}
		return nil
	}
	prov, _ := s.provider()
	s.setState(func(st *api.CalendarSyncState) { st.Status, st.Progress = api.CalendarSyncSyncing, 0 })
	var calErr *api.Error
	for i, c := range due {
		if _, ok := s.lastFull[c.ID]; !ok {
			s.lastFull[c.ID] = now
		}
		full := req.full || !now.Before(s.lastFull[c.ID].Add(fullEvery))
		err := s.syncCalendar(ctx, prov, c, win, full, now)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if accountWide(err) {
				for _, rest := range due[i:] {
					req.cals[rest.ID] = true
				}
				s.putBack(req)
				return err
			}
			_, ae := statusOf(err)
			s.log.Warn("calendar failed", "calendar", c.ID, "code", ae.Code, "err", ae.Message)
			c.ErrorCode = int(ae.Code)
			if serr := st.SetCalendarSyncState(ctx, c.ID, c.CalendarSyncState); serr != nil && !errors.Is(serr, store.ErrNotFound) {
				return serr
			}
			s.next[c.ID] = now.Add(s.backoff(s.failures[c.ID]))
			s.failures[c.ID]++
			if calErr == nil {
				calErr = ae
			}
		} else {
			s.failures[c.ID] = 0
			s.next[c.ID] = now.Add(s.interval(c))
			if full {
				s.lastFull[c.ID] = now
			}
		}
		pct := (i + 1) * 100 / len(due)
		s.setState(func(st *api.CalendarSyncState) { st.Progress = pct })
	}
	s.finish(now, calErr)
	return nil
}

// putBack re-queues what a failed pass did not do.
func (s *syncer) putBack(r request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.req.all = s.req.all || r.all
	s.req.full = s.req.full || r.full
	s.req.list = s.req.list || r.list
	for id := range r.cals {
		s.req.cals[id] = true
	}
}

func (s *syncer) finish(now time.Time, calErr *api.Error) {
	s.mu.Lock()
	s.notifiedAuth = 0
	s.mu.Unlock()
	if calErr != nil {
		s.setState(func(st *api.CalendarSyncState) { st.Status, st.Progress, st.Error = api.CalendarSyncError, -1, calErr })
		return
	}
	t := now
	s.setState(func(st *api.CalendarSyncState) {
		st.Status, st.Progress, st.Error, st.LastSync = api.CalendarSyncIdle, -1, nil, &t
	})
}

// listCalendars stores the server's calendars of the account (user
// columns kept), deletes the rows of disabled and vanished ones (D23) and
// returns the synchronised ones.
func (s *syncer) listCalendars(ctx context.Context, now time.Time) ([]store.Calendar, error) {
	prov, _ := s.provider()
	remote, err := prov.Calendars(ctx)
	if err != nil {
		return nil, err
	}
	disabled := map[string]bool{}
	if c := s.account.Config.Calendar; c != nil {
		for _, id := range c.DisabledCalendars {
			disabled[id] = true
		}
	}
	st := s.sv.opts.Store
	before, err := st.ListCalendars(ctx, s.account.ID)
	if err != nil {
		return nil, err
	}
	var want []store.Calendar
	seen := map[string]bool{}
	for _, r := range remote {
		if len(want) >= api.MaxCalendarsPerAccount {
			break
		}
		if r.RemoteID == "" || len(r.RemoteID) > maxRemoteIDBytes || seen[r.RemoteID] {
			continue
		}
		seen[r.RemoteID] = true
		c := store.Calendar{
			RemoteID: r.RemoteID, Name: calendar.CleanLine(r.Name, maxNameBytes), ServerColor: normColor(r.Color),
			Access: r.Access, Kind: r.Kind, ServerDefault: r.IsDefault,
			TimeZone: calendar.CleanLine(string(r.TimeZone), 64), Position: len(want),
		}
		switch c.Access {
		case api.CalendarAccessOwner, api.CalendarAccessWrite, api.CalendarAccessRead:
		default:
			c.Access = api.CalendarAccessRead
		}
		if r.Owner != nil {
			c.OwnerName = calendar.CleanLine(r.Owner.Name, maxNameBytes)
			c.OwnerAddress = calendar.CleanLine(r.Owner.Address, maxNameBytes)
		}
		want = append(want, c)
	}
	stored, err := st.UpsertCalendars(ctx, s.account.ID, want)
	if err != nil {
		return nil, err
	}
	var changed []string
	keep := map[string]bool{}
	for _, c := range stored {
		keep[c.ID] = true
	}
	old := map[string]store.Calendar{}
	for _, c := range before {
		old[c.ID] = c
		if !keep[c.ID] {
			if err := st.DeleteCalendar(ctx, c.ID); err != nil {
				return nil, err
			}
			delete(s.next, c.ID)
			changed = append(changed, c.ID)
		}
	}
	var active []store.Calendar
	for i, c := range stored {
		off := disabled[c.RemoteID]
		switch {
		case off && !Disabled(c):
			// Disabled by the account: the contents go, the row stays.
			if err := st.DeleteCalendarContents(ctx, c.ID); err != nil {
				return nil, err
			}
			c.CalendarSyncState = store.CalendarSyncState{SyncMode: SyncModeDisabled}
			if err := st.SetCalendarSyncState(ctx, c.ID, c.CalendarSyncState); err != nil {
				return nil, err
			}
			delete(s.next, c.ID)
			delete(s.lastFull, c.ID)
			stored[i] = c
			changed = append(changed, c.ID)
			continue
		case !off && Disabled(c):
			// Enabled again: a fresh start, due at once.
			c.CalendarSyncState = store.CalendarSyncState{}
			if err := st.SetCalendarSyncState(ctx, c.ID, c.CalendarSyncState); err != nil {
				return nil, err
			}
			delete(s.next, c.ID)
			delete(s.lastFull, c.ID)
			stored[i] = c
			changed = append(changed, c.ID)
		}
		if !off {
			active = append(active, c)
		}
	}
	for _, c := range stored {
		o, ok := old[c.ID]
		if !ok || o.Name != c.Name || o.ServerColor != c.ServerColor || o.Access != c.Access || o.Kind != c.Kind ||
			o.ServerDefault != c.ServerDefault || o.Position != c.Position || o.OwnerAddress != c.OwnerAddress || o.TimeZone != c.TimeZone {
			changed = append(changed, c.ID)
		}
	}
	if len(changed) > 0 {
		s.sv.notify.calendarChanged(s.account.ID, changed, span{unbounded: true})
	}
	s.listedAt = now
	return active, nil
}

// normColor makes "#rrggbb" of "#RRGGBB" or "#RRGGBBAA"; "" otherwise.
func normColor(c string) string {
	c = strings.ToLower(strings.TrimSpace(c))
	if len(c) == 9 {
		c = c[:7]
	}
	if len(c) != 7 || c[0] != '#' {
		return ""
	}
	for _, r := range c[1:] {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return ""
		}
	}
	return c
}

// syncCalendar runs one Sync of a calendar and stores its result.
func (s *syncer) syncCalendar(ctx context.Context, prov Provider, c store.Calendar, win Window, full bool, now time.Time) error {
	st := s.sv.opts.Store
	cal := c
	reset := full
	if !s.daemon && needsRebaseline(c.WindowFrom, win, s.sv.loc) {
		reset = true
	}
	if reset {
		cal.SyncToken, cal.CTag = "", ""
	}
	sk := newSink(s.sv, s.account, c, win)
	sk.ctx = ctx
	res, err := prov.Sync(ctx, cal, win, sk)
	defer func() {
		s.sv.notify.calendarChanged(s.account.ID, sk.changedCalendars(), sk.span)
		if sk.changed {
			s.sv.alerts.changed(s.account.ID, sk.span) // at once, not after the coalescing
		}
	}()
	if err != nil {
		return err
	}
	if res.Full {
		if err := sk.removeUnseen(ctx); err != nil {
			return err
		}
	}
	next := store.CalendarSyncState{SyncToken: res.Token, CTag: res.CTag, LastSync: now}
	from, to := windowDates(win, s.sv.loc)
	if s.daemon {
		next.SyncMode, next.WindowFrom, next.WindowTo = syncModeDaemon, from, to
	} else {
		next.SyncMode = res.DeltaMode
		if next.SyncMode == "" {
			next.SyncMode = "delta"
		}
		next.WindowFrom, next.WindowTo = c.WindowFrom, c.WindowTo
		if reset || c.SyncToken == "" || c.WindowFrom == "" {
			next.WindowFrom, next.WindowTo = from, to
		}
	}
	if err := st.SetCalendarSyncState(ctx, c.ID, next); err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	return nil
}

// sink writes one Sync's changes (Sink).
type sink struct {
	sv      *Supervisor
	account store.Account
	cal     store.Calendar
	win     Window
	daemon  bool
	ctx     context.Context
	seen    map[string]bool
	span    span
	changed bool
}

func newSink(sv *Supervisor, a store.Account, cal store.Calendar, win Window) *sink {
	return &sink{sv: sv, account: a, cal: cal, win: win, daemon: a.Config.Protocol() != api.AccountGraph,
		ctx: context.Background(), seen: map[string]bool{}, span: emptySpan()}
}

func (k *sink) changedCalendars() []string {
	if !k.changed {
		return nil
	}
	return []string{k.cal.ID}
}

// Upsert stores one object with its occurrences.
func (k *sink) Upsert(obj Object) error {
	if obj.RemoteID == "" {
		return errors.New("calsync: object without a remote id")
	}
	k.seen[obj.RemoteID] = true
	res, err := storeObject(k.ctx, k.sv.opts.Store, k.cal, obj, k.win, k.sv.loc, k.daemon)
	if err != nil {
		return err
	}
	k.span.merge(res.span)
	if !res.span.empty {
		k.changed = true
	}
	if k.daemon {
		state := store.ResourceStored
		if res.outside {
			state = store.ResourceOutside
		}
		if err := k.sv.opts.Store.PutResources(k.ctx, k.cal.ID, []store.Resource{{Href: obj.RemoteID, ETag: obj.ETag, State: state}}); err != nil {
			return err
		}
	}
	return nil
}

// Remove deletes one object's rows.
func (k *sink) Remove(remoteID string) error {
	k.seen[remoteID] = true
	st := k.sv.opts.Store
	old, err := st.EventsByRemoteID(k.ctx, k.cal.ID, remoteID)
	if err != nil {
		return err
	}
	if len(old) > 0 {
		rowsSpan(&k.span, old)
		k.changed = true
		if _, err := st.RemoveResource(k.ctx, k.cal.ID, remoteID); err != nil {
			return err
		}
	}
	if k.daemon {
		return st.DeleteResources(k.ctx, k.cal.ID, []string{remoteID})
	}
	return nil
}

// removeUnseen removes what a Full pass did not name: stored events and
// known resources (skipped ones stay the provider's).
func (k *sink) removeUnseen(ctx context.Context) error {
	st := k.sv.opts.Store
	rows, err := st.CalendarEvents(ctx, k.cal.ID)
	if err != nil {
		return err
	}
	gone := map[string]bool{}
	for _, r := range rows {
		if !k.seen[r.RemoteID] {
			gone[r.RemoteID] = true
		}
	}
	if k.daemon {
		res, err := st.ListResources(ctx, k.cal.ID)
		if err != nil {
			return err
		}
		for _, r := range res {
			if !k.seen[r.Href] && r.State != store.ResourceSkipped {
				gone[r.Href] = true
			}
		}
	}
	for id := range gone {
		if err := k.Remove(id); err != nil {
			return err
		}
	}
	return nil
}
