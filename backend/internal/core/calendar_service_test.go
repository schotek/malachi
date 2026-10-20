// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/caldav/caldavtest"
	"github.com/schotek/malachi/backend/internal/calendar"
	"github.com/schotek/malachi/backend/internal/calsync"
	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// ---------------------------------------------------------------------------
// A fake calendar source and an engine over it
// ---------------------------------------------------------------------------

const calMe = "me@example.test"

// fakeCal is a calsync.Provider over objects held in memory. Writes go
// through calendar.ApplyChange/ApplyDelete like a CalDAV server would,
// with ETags; every call is recorded.
type fakeCal struct {
	mu        sync.Mutex
	cals      []calsync.RemoteCalendar
	objs      map[string][]calendar.Object // calendar remote id → objects
	feats     api.CalendarFeatures
	seq       int
	calls     []string
	opts      []calsync.WriteOptions
	responses []calsync.Response
	writeErr  error // the next write fails with it
	rangeErr  error
	fb        []calsync.Availability
	fbErr     error
	win       calsync.Window
}

func newFakeCal() *fakeCal {
	return &fakeCal{objs: map[string][]calendar.Object{}, feats: api.CalendarFeatures{Invitations: api.InvitationsViaServer}}
}

func (f *fakeCal) record(call string, opts calsync.WriteOptions) {
	f.calls = append(f.calls, call)
	f.opts = append(f.opts, opts)
}

func (f *fakeCal) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *fakeCal) lastOpts() calsync.WriteOptions {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.opts) == 0 {
		return calsync.WriteOptions{}
	}
	return f.opts[len(f.opts)-1]
}

func (f *fakeCal) put(cal string, obj calendar.Object) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.putLocked(cal, obj)
}

func (f *fakeCal) putLocked(cal string, obj calendar.Object) {
	list := f.objs[cal]
	for i := range list {
		if list[i].RemoteID == obj.RemoteID {
			list[i] = obj
			return
		}
	}
	f.objs[cal] = append(list, obj)
}

func (f *fakeCal) find(remoteID string) (string, int) {
	for cal, list := range f.objs {
		for i := range list {
			if list[i].RemoteID == remoteID {
				return cal, i
			}
		}
	}
	return "", -1
}

func (f *fakeCal) Calendars(context.Context) ([]calsync.RemoteCalendar, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.cals), nil
}

func (f *fakeCal) Sync(_ context.Context, cal calsync.StoredCalendar, win calsync.Window, sink calsync.Sink) (calsync.SyncState, error) {
	f.mu.Lock()
	f.win = win
	objs := slices.Clone(f.objs[cal.RemoteID])
	f.mu.Unlock()
	for _, o := range objs {
		occ, _, err := calendar.Occurrences(o, calendar.Window{From: win.From, To: win.To}, time.UTC, 0)
		if err != nil {
			return calsync.SyncState{}, err
		}
		o.Occurrences = occ
		if err := sink.Upsert(o); err != nil {
			return calsync.SyncState{}, err
		}
	}
	return calsync.SyncState{Full: true, DeltaMode: "list"}, nil
}

func (f *fakeCal) Range(_ context.Context, cal calsync.StoredCalendar, from, to time.Time) ([]calsync.Occurrence, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rangeErr != nil {
		return nil, f.rangeErr
	}
	var out []calsync.Occurrence
	for _, o := range f.objs[cal.RemoteID] {
		occ, _, _ := calendar.Occurrences(o, calendar.Window{From: from, To: to}, time.UTC, 0)
		for _, x := range occ {
			x.RemoteID = o.RemoteID
			out = append(out, x)
		}
	}
	return out, nil
}

func (f *fakeCal) failing() error {
	err := f.writeErr
	f.writeErr = nil
	return err
}

// expanded is obj with its occurrences in the last window (a Graph
// source answers with its own expansion).
func (f *fakeCal) expanded(obj calendar.Object) calendar.Object {
	win := f.win
	if !win.From.Before(win.To) {
		win = calsync.Window{From: time.Now().AddDate(-1, 0, 0), To: time.Now().AddDate(1, 1, 0)}
	}
	obj.Occurrences, _, _ = calendar.Occurrences(obj, calendar.Window{From: win.From, To: win.To}, time.UTC, 0)
	return obj
}

func (f *fakeCal) result(obj calendar.Object, n int, notify bool) calsync.WriteResult {
	obj = f.expanded(obj)
	res := calsync.WriteResult{Objects: []calendar.Object{obj}, Invitations: api.InvitationsSent{Via: api.InvitationsViaNone}}
	if n > 0 && notify {
		res.Invitations = api.InvitationsSent{Via: f.feats.Invitations, Count: n}
	}
	return res
}

func (f *fakeCal) Create(_ context.Context, cal calsync.StoredCalendar, ev calsync.Event, opts calsync.WriteOptions) (calsync.WriteResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("create", opts)
	if err := f.failing(); err != nil {
		return calsync.WriteResult{}, err
	}
	f.seq++
	if ev.RRule != "" {
		ev.Kind = calendar.KindMaster
	}
	obj := calendar.Object{RemoteID: fmt.Sprintf("obj-%d", f.seq), ETag: fmt.Sprintf("e%d", f.seq), Master: &ev}
	f.putLocked(cal.RemoteID, obj)
	return f.result(obj, len(ev.Attendees), opts.Notify), nil
}

func (f *fakeCal) current(ev calsync.StoredEvent, ifMatch string) (string, calendar.Object, error) {
	cal, i := f.find(ev.RemoteID)
	if i < 0 {
		return "", calendar.Object{}, api.NewError(api.CodeEventGone, "gone")
	}
	obj := f.objs[cal][i]
	if ifMatch != "" && ifMatch != obj.ETag {
		e := api.NewError(api.CodeConflict, "changed")
		c := obj
		e.Data = &c
		return "", calendar.Object{}, e
	}
	return cal, obj, nil
}

func (f *fakeCal) Update(_ context.Context, _ calsync.StoredCalendar, ev calsync.StoredEvent, ch calsync.Change, opts calsync.WriteOptions) (calsync.WriteResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("update:"+string(ch.Scope), opts)
	if err := f.failing(); err != nil {
		return calsync.WriteResult{}, err
	}
	cal, obj, err := f.current(ev, opts.IfMatch)
	if err != nil {
		return calsync.WriteResult{}, err
	}
	f.seq++
	updated, split, dropped, err := calendar.ApplyChange(obj, ch, fmt.Sprintf("split-%d", f.seq))
	if err != nil {
		return calsync.WriteResult{}, api.NewError(api.CodeInvalidArgument, "%v", err)
	}
	updated.ETag = fmt.Sprintf("e%d", f.seq)
	f.putLocked(cal, updated)
	res := f.result(updated, 0, false)
	res.DroppedExceptions = dropped
	if split != nil {
		split.RemoteID, split.ETag = fmt.Sprintf("obj-split-%d", f.seq), "s1"
		f.putLocked(cal, *split)
		res.Objects = append(res.Objects, f.expanded(*split))
	}
	return res, nil
}

func (f *fakeCal) Delete(_ context.Context, _ calsync.StoredCalendar, ev calsync.StoredEvent, scope calsync.Scope, opts calsync.WriteOptions) (calsync.WriteResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("delete:"+string(scope), opts)
	if err := f.failing(); err != nil {
		return calsync.WriteResult{}, err
	}
	cal, obj, err := f.current(ev, opts.IfMatch)
	if err != nil {
		return calsync.WriteResult{}, err
	}
	updated, err := calendar.ApplyDelete(obj, scope, opts.RecurrenceID)
	if err != nil {
		return calsync.WriteResult{}, api.NewError(api.CodeInvalidArgument, "%v", err)
	}
	n := 0
	if obj.Master != nil {
		n = len(obj.Master.Attendees)
	}
	if updated == nil {
		_, i := f.find(obj.RemoteID)
		f.objs[cal] = slices.Delete(f.objs[cal], i, i+1)
		res := calsync.WriteResult{Removed: []string{obj.RemoteID}, Invitations: api.InvitationsSent{Via: api.InvitationsViaNone}}
		if n > 0 && opts.Notify {
			res.Invitations = api.InvitationsSent{Via: f.feats.Invitations, Count: n}
		}
		return res, nil
	}
	f.seq++
	updated.ETag = fmt.Sprintf("e%d", f.seq)
	f.putLocked(cal, *updated)
	return f.result(*updated, n, opts.Notify), nil
}

func (f *fakeCal) Respond(_ context.Context, _ calsync.StoredCalendar, ev calsync.StoredEvent, r calsync.Response, opts calsync.WriteOptions) (calsync.WriteResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("respond:"+string(r.Response), opts)
	f.responses = append(f.responses, r)
	if err := f.failing(); err != nil {
		return calsync.WriteResult{}, err
	}
	cal, obj, err := f.current(ev, opts.IfMatch)
	if err != nil {
		return calsync.WriteResult{}, err
	}
	obj = calendar.Object{RemoteID: obj.RemoteID, Master: obj.Master, Overrides: slices.Clone(obj.Overrides)}
	m := *obj.Master
	m.Attendees = slices.Clone(m.Attendees)
	for i := range m.Attendees {
		if m.Attendees[i].Me {
			m.Attendees[i].Response = r.Response
		}
	}
	m.MyResponse = r.Response
	obj.Master = &m
	f.seq++
	obj.ETag = fmt.Sprintf("e%d", f.seq)
	f.putLocked(cal, obj)
	return f.result(obj, 1, opts.Notify), nil
}

func (f *fakeCal) FreeBusy(ctx context.Context, attendees []string, _, _ time.Time, _ time.Duration) ([]calsync.Availability, error) {
	f.mu.Lock()
	fb, err := slices.Clone(f.fb), f.fbErr
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return fb, ctx.Err()
}

func (f *fakeCal) Features() api.CalendarFeatures {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.feats
}

// calRecorder records the calendar notifications.
type calRecorder struct {
	mu          sync.Mutex
	changed     []api.CalendarChangedNotification
	invitations []api.InvitationsChangedNotification
	alerts      []api.EventAlert
}

func (*calRecorder) NewMessage(api.NewMessageNotification)           {}
func (*calRecorder) SyncState(api.SyncStateNotification)             {}
func (*calRecorder) AuthRequired(api.AuthRequiredNotification)       {}
func (*calRecorder) AccountsChanged(api.AccountsChangedNotification) {}
func (*calRecorder) MessagesChanged(api.MessagesChangedNotification) {}
func (r *calRecorder) CalendarChanged(n api.CalendarChangedNotification) {
	r.mu.Lock()
	r.changed = append(r.changed, n)
	r.mu.Unlock()
}
func (*calRecorder) CalendarState(api.CalendarStateNotification) {}
func (r *calRecorder) EventAlert(n api.EventAlertNotification) {
	r.mu.Lock()
	r.alerts = append(r.alerts, n.Alert)
	r.mu.Unlock()
}
func (r *calRecorder) InvitationsChanged(n api.InvitationsChangedNotification) {
	r.mu.Lock()
	r.invitations = append(r.invitations, n)
	r.mu.Unlock()
}

func (r *calRecorder) changes() []api.CalendarChangedNotification {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.changed)
}

// calHarness is a backend whose calendar engine runs over fake sources,
// one per account.
type calHarness struct {
	t     *testing.T
	b     *Backend
	ctx   context.Context
	rec   *calRecorder
	out   *fakeOutbox
	mu    sync.Mutex
	fakes map[string]*fakeCal
	provs map[string]calsync.Provider // instead of the fake, when set
}

func newCalHarness(t *testing.T) *calHarness {
	t.Helper()
	b := newTestBackend(t, config.Default())
	sup, out := newFakeSupervisor(), newFakeOutbox()
	b.Supervisor, b.Delivery = sup, out
	b.Keyring = newMemKeyring()
	h := &calHarness{t: t, b: b, rec: &calRecorder{}, out: out, fakes: map[string]*fakeCal{}, provs: map[string]calsync.Provider{}}
	b.SetNotifier(h.rec)
	ctx, cancel := context.WithCancel(context.Background())
	h.ctx = ctx
	b.calSync = calsync.New(calsync.Options{
		Store: b.store,
		Providers: func(_ context.Context, a store.Account) (calsync.Provider, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			if p, ok := h.provs[a.ID]; ok {
				return p, nil
			}
			f, ok := h.fakes[a.ID]
			if !ok {
				return nil, api.NewError(api.CodeOffline, "no fake")
			}
			return f, nil
		},
		Notifier: calendarNotifier{outboxAwareNotifier: outboxAwareNotifier{b: b, inner: h.rec}},
		Location: time.UTC,
		Log:      slog.New(slog.DiscardHandler),
	})
	done := make(chan struct{})
	go func() { b.calSync.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return h
}

// account stores a calendar account (graph or caldav) over a new fake
// with the given calendars and starts its syncer.
func (h *calHarness) account(kind api.AccountKind, email string, cfg func(*api.AccountConfig), cals ...calsync.RemoteCalendar) (store.Account, *fakeCal) {
	h.t.Helper()
	c := api.AccountConfig{Name: email, Email: email, DisplayName: "Me", Kind: kind, Calendar: &api.CalendarConfig{Sync: true}}
	if kind == api.AccountCalDAV {
		c.CalDAV = &api.CalDAVConfig{ServerURL: "https://dav.example.test/", Username: "me"}
	}
	if kind == api.AccountGraph {
		c.Graph = &api.GraphConfig{}
	}
	if cfg != nil {
		cfg(&c)
	}
	a := store.Account{Name: email, Enabled: true, Config: c}
	if err := h.b.store.AddAccount(h.ctx, &a); err != nil {
		h.t.Fatal(err)
	}
	f := newFakeCal()
	f.cals = cals
	h.mu.Lock()
	h.fakes[a.ID] = f
	h.mu.Unlock()
	return a, f
}

// start runs the account's syncer and waits for its first pass.
func (h *calHarness) start(a store.Account) {
	h.t.Helper()
	h.b.calSync.Start(a)
	h.waitPass(a)
}

func (h *calHarness) waitPass(a store.Account) {
	h.t.Helper()
	waitLong(h.t, "the first calendar pass of "+a.ID, func() bool {
		st, ok := h.b.calSync.CalendarState(a.ID)
		_, prov := h.b.calSync.Provider(a.ID)
		return ok && prov && st.LastSync != nil && st.Status == api.CalendarSyncIdle
	})
}

// resync asks for a pass and waits until the stored events of the
// account number n.
func (h *calHarness) resync(a store.Account, n int) {
	h.t.Helper()
	h.b.calSync.SyncNow(a.ID, "")
	waitLong(h.t, "the events stored", func() bool { return len(h.events(a)) == n })
}

func (h *calHarness) events(a store.Account) []store.Event {
	cals, _ := h.b.store.ListCalendars(h.ctx, a.ID)
	var out []store.Event
	for _, c := range cals {
		evs, _ := h.b.store.CalendarEvents(h.ctx, c.ID)
		out = append(out, evs...)
	}
	return out
}

// calendarID is the stored id of a remote calendar.
func (h *calHarness) calendarID(a store.Account, remote string) string {
	h.t.Helper()
	cals, err := h.b.store.ListCalendars(h.ctx, a.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	for _, c := range cals {
		if c.RemoteID == remote {
			return c.ID
		}
	}
	h.t.Fatalf("no calendar %q", remote)
	return ""
}

// eventID is the stored id of the master or single row of a UID.
func (h *calHarness) eventID(a store.Account, uid string) string {
	h.t.Helper()
	rows, err := h.b.store.EventsByUID(h.ctx, a.ID, uid)
	if err != nil || len(rows) == 0 {
		h.t.Fatalf("no event %q: %v", uid, err)
	}
	return rows[0].ID
}

// day0 is today's midnight (UTC), the base of the test events.
func day0() time.Time {
	n := time.Now().UTC()
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
}

// timed is a single timed event.
func timed(uid, title string, start time.Time, d time.Duration) calendar.Event {
	return calendar.Event{UID: uid, Kind: calendar.KindSingle, Title: title, Start: start, End: start.Add(d), Zone: "UTC",
		Status: api.EventStatusConfirmed, ShowAs: api.ShowAsBusy, RecurOK: true}
}

func obj(remote, etag string, ev calendar.Event, overrides ...calendar.Event) calendar.Object {
	return calendar.Object{RemoteID: remote, ETag: etag, Master: &ev, Overrides: overrides}
}

// invitedTo makes the user an attendee of ev organised by someone else.
func invitedTo(ev calendar.Event, r api.EventResponse) calendar.Event {
	ev.Organizer = &api.Address{Name: "Boss", Address: "boss@example.test"}
	ev.MyResponse, ev.ResponseRequested = r, true
	ev.Attendees = []api.Attendee{
		{Address: "boss@example.test", Role: api.AttendeeChair, Response: api.ResponseAccepted, Type: api.AttendeeTypePerson, Organizer: true},
		{Address: calMe, Role: api.AttendeeRequired, Response: r, Type: api.AttendeeTypePerson, Me: true},
	}
	ev.AttendeeCount = 2
	return ev
}

// ---------------------------------------------------------------------------
// calendar.*
// ---------------------------------------------------------------------------

func TestCalendarListAndUpdate(t *testing.T) {
	h := newCalHarness(t)
	ctx, b := h.ctx, h.b
	g, gf := h.account(api.AccountGraph, "g@example.test", func(c *api.AccountConfig) { c.Calendar.DefaultCalendar = "work" },
		calsync.RemoteCalendar{RemoteID: "home", Name: "Home‮", Color: "#1BADF8", Access: api.CalendarAccessOwner, Kind: api.CalendarKindEvents, IsDefault: true},
		calsync.RemoteCalendar{RemoteID: "work", Name: "Work", Access: api.CalendarAccessOwner, Kind: api.CalendarKindEvents},
		calsync.RemoteCalendar{RemoteID: "holidays", Name: "Holidays", Access: api.CalendarAccessRead, Kind: api.CalendarKindHolidays})
	gf.feats = api.CalendarFeatures{FreeBusy: true, ProposeNewTime: true, Invitations: api.InvitationsViaServer, OnlineMeetings: true}
	h.start(g)
	off, _ := h.account(api.AccountGraph, "off@example.test", func(c *api.AccountConfig) { c.Calendar.Sync = false })
	paused, _ := h.account(api.AccountCalDAV, "p@example.test", func(c *api.AccountConfig) {
		c.CalDAV.Scheduling, c.CalDAV.MailAccountID = api.CalDAVSchedulingClient, "acc_mail"
	})
	if err := b.store.SetAccountEnabled(ctx, paused.ID, false); err != nil {
		t.Fatal(err)
	}

	res, err := b.Calendars().List(ctx, api.CalendarListParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Accounts) != 3 || res.Unanswered != 0 {
		t.Fatalf("accounts %+v", res)
	}
	byID := map[api.AccountID]api.CalendarAccount{}
	for _, a := range res.Accounts {
		byID[a.AccountID] = a
	}
	ga := byID[api.AccountID(g.ID)]
	if ga.Source != api.CalendarSourceGraph || !ga.Sync || ga.State.Status != api.CalendarSyncIdle || !ga.Features.FreeBusy ||
		!ga.Features.OnlineMeetings || ga.Window.From == "" || ga.Window.To == "" || len(ga.Calendars) != 3 {
		t.Fatalf("graph account %+v", ga)
	}
	home, work, hol := ga.Calendars[0], ga.Calendars[1], ga.Calendars[2]
	if home.Name != "Home" || home.Color != "#1badf8" || home.IsDefault || !work.IsDefault {
		t.Errorf("home %+v work %+v", home, work)
	}
	if !hexColor.MatchString(work.Color) || work.Color != paletteColor(string(work.ID)) || work.ServerColor != "" {
		t.Errorf("palette colour %+v", work)
	}
	if hol.Access != api.CalendarAccessRead || hol.Kind != api.CalendarKindHolidays {
		t.Errorf("holidays %+v", hol)
	}
	if oa := byID[api.AccountID(off.ID)]; oa.Sync || len(oa.Calendars) != 0 || oa.Calendars == nil || oa.State.Status != api.CalendarSyncDisabled {
		t.Errorf("sync off %+v", oa)
	}
	pa := byID[api.AccountID(paused.ID)]
	if pa.State.Status != api.CalendarSyncDisabled || pa.Source != api.CalendarSourceCalDAV ||
		pa.Features.Invitations != api.InvitationsViaMail || pa.Features.MailAccountID != "acc_mail" {
		t.Errorf("paused %+v", pa)
	}

	// One account; an account without calendars.
	one, err := b.Calendars().List(ctx, api.CalendarListParams{AccountID: api.AccountID(g.ID)})
	if err != nil || len(one.Accounts) != 1 {
		t.Fatalf("one: %+v %v", one, err)
	}
	mail := seedAccount(t, b, "mail@example.test")
	if _, err := b.Calendars().List(ctx, api.CalendarListParams{AccountID: api.AccountID(mail)}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("mail account: %v", err)
	}
	if _, err := b.Calendars().List(ctx, api.CalendarListParams{AccountID: "acc_none"}); errCode(t, err) != api.CodeAccountNotFound {
		t.Errorf("unknown account: %v", err)
	}

	// calendar.update: the user's columns and a notification naming it.
	vis, col := false, "#E5534B"
	up, err := b.Calendars().Update(ctx, api.CalendarUpdateParams{CalendarID: work.ID, Visible: &vis, UserColor: &col})
	if err != nil {
		t.Fatal(err)
	}
	if up.Calendar.Visible || up.Calendar.Color != "#e5534b" || up.Calendar.UserColor != "#e5534b" || !up.Calendar.IsDefault {
		t.Errorf("updated %+v", up.Calendar)
	}
	found := false
	for _, n := range h.rec.changes() {
		found = found || slices.Contains(n.CalendarIDs, work.ID) && slices.Contains(n.AccountIDs, api.AccountID(g.ID))
	}
	if !found {
		t.Errorf("no calendarChanged for the calendar: %+v", h.rec.changes())
	}
	clear := ""
	if up, err = b.Calendars().Update(ctx, api.CalendarUpdateParams{CalendarID: work.ID, UserColor: &clear}); err != nil || up.Calendar.UserColor != "" ||
		up.Calendar.Color != paletteColor(string(work.ID)) {
		t.Errorf("cleared %+v %v", up, err)
	}
	bad := "red"
	if _, err := b.Calendars().Update(ctx, api.CalendarUpdateParams{CalendarID: work.ID, UserColor: &bad}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("bad colour: %v", err)
	}
	if _, err := b.Calendars().Update(ctx, api.CalendarUpdateParams{CalendarID: "cal_none", Visible: &vis}); errCode(t, err) != api.CodeCalendarNotFound {
		t.Errorf("unknown calendar: %v", err)
	}

	// calendar.sync.
	if _, err := b.Calendars().Sync(ctx, api.CalendarSyncParams{}); err != nil {
		t.Error(err)
	}
	if _, err := b.Calendars().Sync(ctx, api.CalendarSyncParams{AccountID: api.AccountID(g.ID), CalendarID: home.ID}); err != nil {
		t.Error(err)
	}
	if _, err := b.Calendars().Sync(ctx, api.CalendarSyncParams{AccountID: "acc_none"}); errCode(t, err) != api.CodeAccountNotFound {
		t.Errorf("sync unknown account: %v", err)
	}
	if _, err := b.Calendars().Sync(ctx, api.CalendarSyncParams{CalendarID: "cal_none"}); errCode(t, err) != api.CodeCalendarNotFound {
		t.Errorf("sync unknown calendar: %v", err)
	}
}

func TestCalendarPreferences(t *testing.T) {
	h := newCalHarness(t)
	ctx, b := h.ctx, h.b
	got, err := b.Calendars().Preferences(ctx, api.CalendarPreferencesParams{})
	if err != nil || got.Preferences.DefaultDurationMinutes != 60 || got.Preferences.DefaultAlertMinutes != 15 {
		t.Fatalf("defaults %+v %v", got, err)
	}
	dur, decl := 30, true
	set, err := b.Calendars().SetPreferences(ctx, api.CalendarSetPreferencesParams{Preferences: api.CalendarPreferencesUpdate{
		DefaultDurationMinutes: &dur, ShowDeclined: &decl,
		WorkingHours: &api.WorkingHours{Days: []string{"MO", "WE"}, Start: "09:30", End: "18:00"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	p := set.Preferences
	if p.DefaultDurationMinutes != 30 || !p.ShowDeclined || p.DefaultAlertMinutes != 15 || p.WeekStart != api.WeekStartLocale ||
		p.WorkingHours.Start != "09:30" || len(p.WorkingHours.Days) != 2 {
		t.Errorf("stored %+v", p)
	}
	if again, _ := b.Calendars().Preferences(ctx, api.CalendarPreferencesParams{}); again.Preferences.DefaultDurationMinutes != 30 {
		t.Errorf("not persisted: %+v", again)
	}
	n := h.rec.changes()
	if len(n) == 0 || n[len(n)-1].AccountIDs != nil || n[len(n)-1].CalendarIDs != nil {
		t.Errorf("notification %+v", n)
	}
	ws := api.WeekStart("tuesday")
	for name, u := range map[string]api.CalendarPreferencesUpdate{
		"duration":  {DefaultDurationMinutes: ptr(4)},
		"duration2": {DefaultDurationMinutes: ptr(1441)},
		"alert":     {DefaultAlertMinutes: ptr(-2)},
		"alert2":    {DefaultAlertMinutes: ptr(api.MaxAlertMinutes + 1)},
		"weekStart": {WeekStart: &ws},
		"noDays":    {WorkingHours: &api.WorkingHours{Start: "08:00", End: "09:00"}},
		"badDay":    {WorkingHours: &api.WorkingHours{Days: []string{"XX"}, Start: "08:00", End: "09:00"}},
		"repeated":  {WorkingHours: &api.WorkingHours{Days: []string{"MO", "MO"}, Start: "08:00", End: "09:00"}},
		"endFirst":  {WorkingHours: &api.WorkingHours{Days: []string{"MO"}, Start: "10:00", End: "09:00"}},
		"badClock":  {WorkingHours: &api.WorkingHours{Days: []string{"MO"}, Start: "8:00", End: "25:00"}},
	} {
		if _, err := b.Calendars().SetPreferences(ctx, api.CalendarSetPreferencesParams{Preferences: u}); errCode(t, err) != api.CodeInvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
	if none := -1; true {
		if res, err := b.Calendars().SetPreferences(ctx, api.CalendarSetPreferencesParams{Preferences: api.CalendarPreferencesUpdate{DefaultAlertMinutes: &none}}); err != nil ||
			res.Preferences.DefaultAlertMinutes != -1 || res.Preferences.DefaultDurationMinutes != 30 {
			t.Errorf("none alert: %+v %v", res, err)
		}
	}
}

func ptr[T any](v T) *T { return &v }

// A calendar in disabledCalendars stays in calendar.list (disabled, with
// its remoteId) without contents; enabling it again brings its events.
func TestCalendarDisabledListed(t *testing.T) {
	ctx := context.Background()
	srv := caldavtest.New(t, caldavtest.Nextcloud)
	day := day0().AddDate(0, 0, 2)
	stamp := day.Add(9 * time.Hour).Format("20060102T150405Z")
	team := srv.CalendarHref("team-shared_by_petr")
	srv.PutObject(team, "t1.ics", []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:team-1\r\nDTSTAMP:"+stamp+
		"\r\nDTSTART:"+stamp+"\r\nDURATION:PT1H\r\nSUMMARY:Team event\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"))
	b, _, a := calDAVEndToEnd(t, srv)
	list := func() ([]string, bool) {
		res := listAll(t, b, api.EventListParams{From: day, To: day.AddDate(0, 0, 1), TimeZone: "UTC", IncludeHidden: true})
		return titles(res.Occurrences), res.Complete
	}
	waitLong(t, "the team event", func() bool { got, _ := list(); return slices.Contains(got, "Team event") })

	setDisabled := func(ids []string) {
		t.Helper()
		cur, err := b.store.GetAccount(ctx, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		cfg := cur.Config
		cal := api.CalendarConfig{Sync: true}
		if cfg.Calendar != nil {
			cal = *cfg.Calendar
		}
		cal.DisabledCalendars = ids
		cfg.Calendar = &cal
		if _, err := b.Accounts().Update(ctx, api.AccountUpdateParams{AccountID: api.AccountID(a.ID), Config: cfg}); err != nil {
			t.Fatal(err)
		}
	}
	setDisabled([]string{team})
	var teamCal api.Calendar
	waitLong(t, "the disabled calendar listed", func() bool {
		res, err := b.Calendars().List(ctx, api.CalendarListParams{AccountID: api.AccountID(a.ID)})
		if err != nil || len(res.Accounts) != 1 {
			return false
		}
		for _, c := range res.Accounts[0].Calendars {
			if c.RemoteID == team {
				teamCal = c
				return c.Disabled
			}
		}
		return false
	})
	for _, c := range func() []api.Calendar {
		res, _ := b.Calendars().List(ctx, api.CalendarListParams{AccountID: api.AccountID(a.ID)})
		return res.Accounts[0].Calendars
	}() {
		if c.RemoteID == "" || c.Disabled != (c.RemoteID == team) {
			t.Errorf("calendar %+v", c)
		}
	}
	if got, _ := list(); slices.Contains(got, "Team event") {
		t.Errorf("disabled calendar listed events: %v", got)
	}
	if sr, err := b.Events().Search(ctx, api.EventSearchParams{Query: "team"}); err != nil || len(sr.Occurrences) != 0 {
		t.Errorf("search of a disabled calendar %+v %v", sr, err)
	}
	// calendar.update of a disabled calendar is harmless; writes and
	// event.list by its id refuse it.
	if up, err := b.Calendars().Update(ctx, api.CalendarUpdateParams{CalendarID: teamCal.ID, Visible: ptr(false)}); err != nil || !up.Calendar.Disabled {
		t.Errorf("update of a disabled calendar %+v %v", up, err)
	}
	if _, err := b.Events().List(ctx, api.EventListParams{From: day, To: day.AddDate(0, 0, 1), TimeZone: "UTC",
		CalendarIDs: []api.CalendarID{teamCal.ID}}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("event.list of a disabled calendar: %v", err)
	}

	setDisabled(nil)
	waitLong(t, "the team event back", func() bool { got, _ := list(); return slices.Contains(got, "Team event") })
}

// A calendar disabled and enabled again before its syncer has passed in
// between (the syncer marks the row disabled only when it does) must not
// keep the sync token of its dropped contents: a delta since then would be
// empty, and the events would never come back (the hosted Windows runner
// lost this race every time).
func TestCalendarDroppedByTheAccountStartsAfresh(t *testing.T) {
	ctx := context.Background()
	srv := caldavtest.New(t, caldavtest.Nextcloud)
	day := day0().AddDate(0, 0, 2)
	stamp := day.Add(9 * time.Hour).Format("20060102T150405Z")
	team := srv.CalendarHref("team-shared_by_petr")
	srv.PutObject(team, "t1.ics", []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:team-1\r\nDTSTAMP:"+stamp+
		"\r\nDTSTART:"+stamp+"\r\nDURATION:PT1H\r\nSUMMARY:Team event\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"))
	b, _, a := calDAVEndToEnd(t, srv)
	row := func() store.Calendar {
		t.Helper()
		cals, err := b.store.ListCalendars(ctx, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cals {
			if c.RemoteID == team {
				return c
			}
		}
		t.Fatalf("no calendar %s in %+v", team, cals)
		return store.Calendar{}
	}
	waitLong(t, "the team calendar synced", func() bool { return row().SyncToken != "" })

	next := a
	next.Config.Calendar = &api.CalendarConfig{Sync: true, DisabledCalendars: []string{team}}
	b.dropCalendarCache(ctx, a, next)
	if c := row(); c.SyncMode != calsync.SyncModeDisabled || c.SyncToken != "" || c.CTag != "" {
		t.Errorf("the dropped calendar kept its sync state: %+v", c.CalendarSyncState)
	}
}
