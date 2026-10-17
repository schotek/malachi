// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package caldav

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/caldav/caldavtest"
	"github.com/schotek/malachi/backend/internal/calendar"
	"github.com/schotek/malachi/backend/internal/calendar/imip"
	"github.com/schotek/malachi/backend/internal/calsync"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

const me = "jana@example.test"

var allProfiles = []caldavtest.Profile{caldavtest.Nextcloud, caldavtest.ICloud, caldavtest.Fastmail, caldavtest.Radicale, caldavtest.Hostile}

// fakeEngine is the engine's side as T8 describes it: the sink stores
// objects and writes cal_resources (stored/outside), Remove forgets the
// href; the provider writes skipped rows through PutResources.
type fakeEngine struct {
	mu      sync.Mutex
	win     calsync.Window
	ops     []string // "+href" / "-href"
	objs    map[string]calendar.Object
	res     map[string]store.Resource
	failOn  string
	failErr error // the error of failOn; nil = a plain one
	removed int
}

func newEngine(win calsync.Window) *fakeEngine {
	return &fakeEngine{win: win, objs: map[string]calendar.Object{}, res: map[string]store.Resource{}}
}

func (e *fakeEngine) Upsert(o calendar.Object) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.failOn != "" && strings.HasSuffix(o.RemoteID, e.failOn) {
		if e.failErr != nil {
			return e.failErr
		}
		return errors.New("sink failed")
	}
	e.ops = append(e.ops, "+"+o.RemoteID)
	e.objs[o.RemoteID] = o
	st := store.ResourceStored
	if !inWindow(o, e.win) {
		st = store.ResourceOutside
	}
	e.res[o.RemoteID] = store.Resource{Href: o.RemoteID, ETag: o.ETag, State: st}
	return nil
}

func (e *fakeEngine) Remove(href string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ops = append(e.ops, "-"+href)
	delete(e.objs, href)
	delete(e.res, href)
	return nil
}

func (e *fakeEngine) ListResources(_ context.Context, _ string) ([]store.Resource, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []store.Resource
	for _, r := range e.res {
		out = append(out, r)
	}
	return out, nil
}

func (e *fakeEngine) PutResources(_ context.Context, _ string, rs []store.Resource) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, r := range rs {
		e.res[r.Href] = r
	}
	return nil
}

func (e *fakeEngine) takeOps() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := e.ops
	e.ops = nil
	sort.Strings(out)
	return out
}

func (e *fakeEngine) resource(href string) store.Resource {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.res[href]
}

var testWin = calsync.Window{From: t0.AddDate(0, 0, -30), To: t0.AddDate(0, 0, 90)}

// passer runs passes like the engine: it stores the state in the row.
type passer struct {
	t   *testing.T
	p   *Provider
	cal store.Calendar
	e   *fakeEngine
}

func (ps *passer) run() calsync.SyncState {
	ps.t.Helper()
	st, err := ps.p.Sync(ps.t.Context(), ps.cal, testWin, ps.e)
	if err != nil {
		ps.t.Fatalf("sync: %v", err)
	}
	ps.cal.SyncToken, ps.cal.CTag = st.Token, st.CTag
	ps.cal.WindowFrom, ps.cal.WindowTo = dateOf(testWin.From), dateOf(testWin.To)
	return st
}

func firstCalendar(s *caldavtest.Server) string {
	if s.Profile() == caldavtest.Radicale {
		return s.CalendarHref("calendar.ics")
	}
	return s.DefaultCalendarHref()
}

type mailbox struct {
	mu   sync.Mutex
	msgs []IMIPMessage
	opts []calsync.WriteOptions
}

func (m *mailbox) send(_ context.Context, msgs []IMIPMessage, o calsync.WriteOptions) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.msgs = append(m.msgs, msgs...)
	m.opts = append(m.opts, o)
	return nil
}

func newProvider(t *testing.T, s *caldavtest.Server, edit func(*ProviderOptions)) (*Provider, *mailbox) {
	t.Helper()
	mb := &mailbox{}
	opts := ProviderOptions{
		Addresses: []string{me}, Identity: api.Address{Name: "Jana", Address: me},
		MailAccountID: "acc_mail", SendIMIP: mb.send,
		Now: func() time.Time { return t0 },
	}
	if edit != nil {
		edit(&opts)
	}
	return NewProvider(newClient(t, s), opts), mb
}

func vcal(lines ...string) []byte {
	return []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\n" + strings.Join(lines, "\r\n") + "\r\nEND:VCALENDAR\r\n")
}

// weekly is a series on Wednesdays 10:00 UTC from 2026-08-12 (t0 is an
// instance) with an unknown property, an organiser and attendees.
func weekly(uid, organizer string, attendees ...string) []byte {
	l := []string{"BEGIN:VEVENT", "UID:" + uid, "DTSTAMP:20261001T000000Z", "SEQUENCE:2",
		"DTSTART:20260812T100000Z", "DTEND:20260812T110000Z", "RRULE:FREQ=WEEKLY;COUNT=30",
		"SUMMARY:Weekly", "X-MALACHI-TEST;X-P=1:keep me"}
	if organizer != "" {
		l = append(l, "ORGANIZER;CN=Org:mailto:"+organizer)
	}
	for _, a := range attendees {
		l = append(l, "ATTENDEE;PARTSTAT=NEEDS-ACTION;RSVP=TRUE:mailto:"+a)
	}
	return vcal(append(l, "END:VEVENT")...)
}

func storedOf(o calendar.Object) store.Event {
	k := store.EventSingle
	if o.Master != nil && o.Master.Kind == calendar.KindMaster {
		k = store.EventMaster
	}
	return store.Event{RemoteID: o.RemoteID, ETag: o.ETag, UID: o.UID(), Kind: k, Raw: o.Raw}
}

func TestProviderSync(t *testing.T) {
	for _, prof := range allProfiles {
		t.Run(string(prof), func(t *testing.T) {
			s := caldavtest.New(t, prof)
			coll := firstCalendar(s)
			a, _ := s.PutObject(coll, "a.ics", event("a", t0))
			out, _ := s.PutObject(coll, "out.ics", event("out", t0.AddDate(-2, 0, 0)))
			rec, _ := s.PutObject(coll, "rec.ics", weekly("rec", ""))
			todo, _ := s.PutObject(coll, "todo.ics", vcal("BEGIN:VTODO", "UID:t", "DUE:20261020T100000Z", "SUMMARY:x", "END:VTODO"))
			bad, _ := s.PutObject(coll, "bad.ics", []byte("\x00\x01 not a calendar"))
			big, _ := s.PutObject(coll, "big.ics", vcal("BEGIN:VEVENT", "UID:big", "DTSTART:20261014T100000Z",
				"DESCRIPTION:"+strings.Repeat("x", api.MaxICSBytes), "END:VEVENT"))
			p, _ := newProvider(t, s, nil)
			e := newEngine(testWin)
			p.opts.Resources = e
			ps := &passer{t: t, p: p, cal: store.Calendar{ID: "cal_1", RemoteID: coll}, e: e}

			st := ps.run()
			if got := e.takeOps(); !slices.Equal(got, []string{"+" + a, "+" + rec}) {
				t.Fatalf("first pass %v", got)
			}
			if _, ok := e.objs[out]; ok {
				t.Fatal("object outside the window stored")
			}
			for href, reason := range map[string]string{todo: SkipNoEvent, bad: SkipMalformed, big: SkipTooBig} {
				if r := e.resource(href); r.State != store.ResourceSkipped || r.Reason != reason {
					t.Errorf("%s: %+v, want skipped %s", href, r, reason)
				}
			}
			ctag, tok := s.CalendarState(coll)
			if st.CTag != ctag || (st.Token != tok && prof != caldavtest.Radicale) || !st.Full {
				t.Fatalf("state %+v, server %q %q", st, ctag, tok)
			}
			if !bytes.Contains(e.objs[rec].Raw, []byte("X-MALACHI-TEST")) || e.objs[a].ETag == "" {
				t.Fatal("object without Raw or ETag")
			}

			// Nothing changed: one PROPFIND, no sink call, the same state.
			s.ResetRequests()
			if st2 := ps.run(); st2.Token != st.Token || st2.CTag != st.CTag {
				t.Fatalf("unchanged %+v", st2)
			}
			if reqs := s.Requests(); len(reqs) != 1 || reqs[0].Method != "PROPFIND" || len(e.takeOps()) != 0 {
				t.Fatalf("unchanged pass made %d requests", len(reqs))
			}

			// A change and a delete: only the changed object is fetched.
			s.PutObject(coll, "a.ics", event("a", t0.Add(time.Hour)))
			s.RemoveObject(rec)
			s.ResetRequests()
			ps.run()
			if got := e.takeOps(); !slices.Equal(got, []string{"+" + a, "-" + rec}) {
				t.Fatalf("second pass %v", got)
			}
			fetched := 0
			for _, r := range s.Requests() {
				fetched += r.MultigetHrefs
				if r.Method == http.MethodGet {
					fetched++
				}
			}
			if fetched != 1 {
				t.Fatalf("fetched %d objects, want 1", fetched)
			}
			if !e.objs[a].Master.Start.Equal(t0.Add(time.Hour)) {
				t.Fatal("change not applied")
			}
		})
	}
}

func TestProviderInvalidSyncTokenListsAgain(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Nextcloud)
	coll := firstCalendar(s)
	a, _ := s.PutObject(coll, "a.ics", event("a", t0))
	p, _ := newProvider(t, s, nil)
	e := newEngine(testWin)
	p.opts.Resources = e
	ps := &passer{t: t, p: p, cal: store.Calendar{ID: "cal_1", RemoteID: coll}, e: e}
	ps.run()
	e.takeOps()
	b, _ := s.PutObject(coll, "b.ics", event("b", t0))
	s.RemoveObject(a)
	s.ExpireSyncTokens(coll)
	st := ps.run()
	if got := e.takeOps(); !slices.Equal(got, []string{"+" + b, "-" + a}) {
		t.Fatalf("full pass %v", got)
	}
	if _, tok := s.CalendarState(coll); st.Token != tok {
		t.Fatalf("token %q, server %q", st.Token, tok)
	}
	var report bool
	for _, r := range s.Requests() {
		report = report || bytes.Contains(r.Body, []byte("calendar-query"))
	}
	if !report {
		t.Fatal("no calendar-query after the refused token")
	}
}

func TestProviderFallbacksAndMemoryIndex(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Radicale)
	s.Set(func(s *caldavtest.Server) { s.TimeRange, s.Multiget = false, false })
	coll := firstCalendar(s)
	a, _ := s.PutObject(coll, "a.ics", event("a", t0))
	out, _ := s.PutObject(coll, "out.ics", event("out", t0.AddDate(-2, 0, 0)))
	p, _ := newProvider(t, s, nil) // Resources nil: the in-memory index
	e := newEngine(testWin)
	ps := &passer{t: t, p: p, cal: store.Calendar{ID: "cal_1", RemoteID: coll}, e: e}
	st := ps.run()
	// PROPFIND lists every member; the engine decides stored/outside.
	if got := e.takeOps(); !slices.Equal(got, []string{"+" + a, "+" + out}) || !st.Full || st.Token != "" {
		t.Fatalf("first pass %v %+v", got, st)
	}
	if e.resource(out).State != store.ResourceOutside {
		t.Fatal("outside object not outside")
	}
	s.PutObject(coll, "a.ics", event("a", t0.Add(time.Hour)))
	if st := ps.run(); st.Full {
		t.Fatal("pass with known resources reported Full")
	}
	if got := e.takeOps(); !slices.Equal(got, []string{"+" + a}) {
		t.Fatalf("second pass %v", got)
	}
	// A moved window lists again and re-reads the outside object.
	ps.cal.WindowFrom = "2020-01-01"
	ps.cal.CTag = ""
	st, err := p.Sync(t.Context(), ps.cal, testWin, e)
	if err != nil {
		t.Fatal(err)
	}
	if got := e.takeOps(); !slices.Equal(got, []string{"+" + out}) || st.Full {
		t.Fatalf("moved window %v", got)
	}
}

func TestProviderSinkErrorEndsPass(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Fastmail)
	coll := firstCalendar(s)
	s.PutObject(coll, "a.ics", event("a", t0))
	p, _ := newProvider(t, s, nil)
	e := newEngine(testWin)
	e.failOn = "a.ics"
	if _, err := p.Sync(t.Context(), store.Calendar{ID: "c", RemoteID: coll}, testWin, e); err == nil {
		t.Fatal("sink error not returned")
	}
	// The engine's *api.Error (storageError for a failure of the store)
	// keeps its code: the provider's mapping is for its own errors.
	e.failErr = api.NewError(api.CodeStorageError, "calsync: database is locked")
	_, err := p.Sync(t.Context(), store.Calendar{ID: "c", RemoteID: coll}, testWin, e)
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.CodeStorageError {
		t.Fatalf("sink's storageError came back as %v", err)
	}
}

func TestProviderHostile(t *testing.T) {
	for _, f := range []caldavtest.Failure{
		caldavtest.Garbage207(), caldavtest.DeepMultistatus(100), caldavtest.HugeMultistatus(40 << 20),
		caldavtest.WrongContentType(), {Method: "REPORT", Status: http.StatusInsufficientStorage},
		{Method: "REPORT", Status: http.StatusUnauthorized},
	} {
		s := caldavtest.New(t, caldavtest.Hostile)
		coll := firstCalendar(s)
		s.PutObject(coll, "a.ics", event("a", t0))
		p, _ := newProvider(t, s, nil)
		f.Method = "REPORT"
		s.FailNext(f)
		_, err := p.Sync(t.Context(), store.Calendar{ID: "c", RemoteID: coll}, testWin, newEngine(testWin))
		var ae *api.Error
		if !errors.As(err, &ae) {
			t.Fatalf("%d: %v not an api error", f.Status, err)
		}
		switch ae.Code {
		case api.CodeMalformedCalendar, api.CodeServerError, api.CodeAuthFailed:
		default:
			t.Fatalf("%d: code %v", f.Status, ae.Code)
		}
	}
	// A hostile object never panics: every line of it is broken.
	s := caldavtest.New(t, caldavtest.Hostile)
	coll := firstCalendar(s)
	s.PutObject(coll, "x.ics", vcal("BEGIN:VEVENT", "UID:"+strings.Repeat("u", 5000), "DTSTART:99999999T999999Z",
		"RRULE:FREQ=SECONDLY;BYSECOND=61", "ATTENDEE:"+strings.Repeat("mailto:x@y,", 2000), "END:VEVENT"))
	p, _ := newProvider(t, s, nil)
	if _, err := p.Sync(t.Context(), store.Calendar{ID: "c", RemoteID: coll}, testWin, newEngine(testWin)); err != nil {
		t.Fatal(err)
	}
}

func TestProviderTooManyEvents(t *testing.T) {
	err := syncErr(ErrTooManyResources)
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.CodeServerError || ae.Data.(map[string]string)["reason"] != "tooManyEvents" {
		t.Fatalf("%v", err)
	}
}

func TestProviderCalendarsAndFeatures(t *testing.T) {
	for _, prof := range allProfiles {
		s := caldavtest.New(t, prof)
		p, _ := newProvider(t, s, func(o *ProviderOptions) { o.Addresses = nil })
		cals, err := p.Calendars(t.Context())
		if err != nil {
			t.Fatalf("%s: %v", prof, err)
		}
		if len(cals) == 0 || cals[0].Kind != api.CalendarKindEvents {
			t.Fatalf("%s: %+v", prof, cals)
		}
		for _, c := range cals {
			if c.Color != "" && (len(c.Color) != 7 || c.Color != strings.ToLower(c.Color)) {
				t.Errorf("%s: colour %q", prof, c.Color)
			}
		}
		f := p.Features()
		want := api.InvitationsViaServer
		if prof == caldavtest.Radicale {
			want = api.InvitationsViaMail
		}
		if f.Invitations != want || f.FreeBusy || f.ProposeNewTime || f.OnlineMeetings {
			t.Fatalf("%s: features %+v", prof, f)
		}
		if _, err := p.FreeBusy(t.Context(), nil, t0, t0, 0); !errors.Is(err, calsync.ErrUnsupported) {
			t.Fatal("free/busy")
		}
	}
	s := caldavtest.New(t, caldavtest.Nextcloud)
	p, _ := newProvider(t, s, nil)
	cals, _ := p.Calendars(t.Context())
	var ro, def bool
	for _, c := range cals {
		ro = ro || c.Access == api.CalendarAccessRead
		def = def || c.IsDefault
	}
	if !ro || !def || len(cals) != 2 {
		t.Fatalf("nextcloud %+v", cals)
	}
	if p := NewProvider(newClient(t, s), ProviderOptions{Scheduling: api.CalDAVSchedulingClient}); p.Features().Invitations != api.InvitationsViaNone {
		t.Fatal("client scheduling without a mail account")
	}
}

func TestProviderRange(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Fastmail)
	coll := firstCalendar(s)
	rec, _ := s.PutObject(coll, "rec.ics", weekly("rec", ""))
	p, _ := newProvider(t, s, nil)
	occs, err := p.Range(t.Context(), store.Calendar{RemoteID: coll}, t0.AddDate(0, 0, -1), t0.AddDate(0, 0, 20))
	if err != nil || len(occs) != 3 || occs[0].RemoteID != rec || !occs[0].Start.Equal(t0) {
		t.Fatalf("%+v %v", occs, err)
	}
}

func TestProviderRangeObjects(t *testing.T) {
	for _, prof := range allProfiles {
		s := caldavtest.New(t, prof)
		coll := firstCalendar(s)
		rec, _ := s.PutObject(coll, "rec.ics", weekly("rec", ""))
		one, _ := s.PutObject(coll, "one.ics", event("one", t0.Add(2*time.Hour)))
		s.PutObject(coll, "far.ics", event("far", t0.AddDate(2, 0, 0)))
		s.PutObject(coll, "todo.ics", vcal("BEGIN:VTODO", "UID:t", "SUMMARY:x", "END:VTODO"))
		p, _ := newProvider(t, s, nil)
		e := newEngine(testWin)
		p.opts.Resources = e
		from, to := t0.AddDate(0, 0, -1), t0.AddDate(0, 0, 20)
		objs, err := p.RangeObjects(t.Context(), store.Calendar{RemoteID: coll}, from, to)
		if err != nil {
			t.Fatalf("%s: %v", prof, err)
		}
		got := map[string]calendar.Object{}
		for _, o := range objs {
			got[o.RemoteID] = o
		}
		r, o1 := got[rec], got[one]
		if len(objs) != 2 || r.Master == nil || r.Master.Title != "Weekly" || len(r.Occurrences) != 3 ||
			r.Occurrences[0].RemoteID != rec || !r.Occurrences[0].Start.Equal(t0) || r.ETag == "" {
			t.Fatalf("%s: series %+v", prof, objs)
		}
		if o1.Master == nil || o1.Master.Title != "Test one" || len(o1.Occurrences) != 1 || o1.Occurrences[0].Override != -1 {
			t.Fatalf("%s: single %+v", prof, o1)
		}
		if len(e.takeOps()) != 0 || len(e.res) != 0 {
			t.Fatalf("%s: RangeObjects stored something", prof)
		}
		if objs, err := p.RangeObjects(t.Context(), store.Calendar{RemoteID: coll}, to, from); err != nil || objs != nil {
			t.Fatalf("%s: empty range %v %v", prof, objs, err)
		}
	}
}

func written(t *testing.T, s *caldavtest.Server, href string) string {
	t.Helper()
	data, _, ok := s.Object(href)
	if !ok {
		t.Fatalf("%s not on the server", href)
	}
	return strings.ReplaceAll(string(data), "\r\n ", "")
}

func TestProviderWrites(t *testing.T) {
	for _, prof := range allProfiles {
		t.Run(string(prof), func(t *testing.T) {
			s := caldavtest.New(t, prof)
			coll := firstCalendar(s)
			p, mb := newProvider(t, s, nil)
			cal := store.Calendar{ID: "cal_1", RemoteID: coll, Access: api.CalendarAccessWrite}

			// Create: a new UUID href, If-None-Match, the ETag known.
			ev, err := calendar.FromInput(api.EventInput{Title: "New", Start: "2026-10-14T12:00:00", End: "2026-10-14T13:00:00", TimeZone: "Europe/Prague"},
				calendar.InputOptions{UID: "will-be-kept"})
			if err != nil {
				t.Fatal(err)
			}
			s.ResetRequests()
			res, err := p.Create(t.Context(), cal, ev, calsync.WriteOptions{})
			if err != nil {
				t.Fatal(err)
			}
			obj := res.Objects[0]
			if !strings.HasPrefix(obj.RemoteID, coll) || !strings.HasSuffix(obj.RemoteID, ".ics") || len(obj.RemoteID)-len(coll) != 36+4 {
				t.Fatalf("href %q", obj.RemoteID)
			}
			if _, etag, _ := s.Object(obj.RemoteID); obj.ETag == "" || obj.ETag != etag {
				t.Fatalf("etag %q, server %q", obj.ETag, etag)
			}
			reqs := s.Requests()
			if reqs[0].Header.Get("If-None-Match") != "*" {
				t.Fatal("create without If-None-Match")
			}
			if prof == caldavtest.ICloud && reqs[len(reqs)-1].Method != http.MethodGet {
				t.Fatal("iCloud: no GET after a PUT without ETag")
			}
			if res.Invitations.Via != api.InvitationsViaNone || len(mb.msgs) != 0 {
				t.Fatalf("invitations %+v", res.Invitations)
			}

			// Update of a series (scope all) keeps the unknown property.
			href, _ := s.PutObject(coll, "rec.ics", weekly("rec", ""))
			o, err := p.load(t.Context(), store.Event{RemoteID: href, Kind: store.EventMaster}, "")
			if err != nil {
				t.Fatal(err)
			}
			upd := *o.Master
			upd.Title, upd.Location = "Renamed", "Room 2"
			res, err = p.Update(t.Context(), cal, storedOf(o), calsync.Change{Scope: api.EditScopeAll, Event: upd},
				calsync.WriteOptions{IfMatch: o.ETag})
			if err != nil {
				t.Fatal(err)
			}
			body := written(t, s, href)
			if !strings.Contains(body, "X-MALACHI-TEST;X-P=1:keep me") || !strings.Contains(body, "SUMMARY:Renamed") ||
				!strings.Contains(body, "SEQUENCE:3") {
				t.Fatalf("update all:\n%s", body)
			}
			o = res.Objects[0]

			// A stale ETag is conflict with the current object.
			_, err = p.Update(t.Context(), cal, storedOf(o), calsync.Change{Scope: api.EditScopeAll, Event: upd},
				calsync.WriteOptions{IfMatch: `"stale"`})
			var ae *api.Error
			if !errors.As(err, &ae) || ae.Code != api.CodeConflict {
				t.Fatalf("stale update: %v", err)
			}
			cur, ok := ae.Data.(*calendar.Object)
			if !ok || cur.Master.Title != "Renamed" || cur.ETag != o.ETag {
				t.Fatalf("conflict data %+v", ae.Data)
			}

			// Scope this: an override, the series untouched.
			inst := upd
			inst.Title = "Just once"
			res, err = p.Update(t.Context(), cal, storedOf(o), calsync.Change{Scope: api.EditScopeThis, RecurrenceID: "2026-10-14T10:00:00Z", Event: inst},
				calsync.WriteOptions{IfMatch: o.ETag})
			if err != nil {
				t.Fatal(err)
			}
			o = res.Objects[0]
			if ov := o.Override("2026-10-14T10:00:00Z"); ov == nil || ov.Title != "Just once" || o.Master.Title != "Renamed" {
				t.Fatalf("override %+v", o.Overrides)
			}

			// Scope thisAndFuture: the series cut and a new one at a new href.
			fut := upd
			fut.Title = "From now on"
			fut.Start, fut.End = t0.AddDate(0, 0, 14), t0.AddDate(0, 0, 14).Add(time.Hour)
			res, err = p.Update(t.Context(), cal, storedOf(o), calsync.Change{Scope: api.EditScopeThisAndFuture, RecurrenceID: "2026-10-28T10:00:00Z", Event: fut},
				calsync.WriteOptions{IfMatch: o.ETag})
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Objects) != 2 || res.Objects[1].UID() == "rec" || res.Objects[1].Master.Title != "From now on" {
				t.Fatalf("split %+v", res.Objects)
			}
			if body := written(t, s, href); !strings.Contains(body, "UNTIL=20261028T095959Z") || !strings.Contains(body, "X-MALACHI-TEST") {
				t.Fatalf("cut series:\n%s", body)
			}
			if _, _, ok := s.Object(res.Objects[1].RemoteID); !ok {
				t.Fatal("new series not on the server")
			}
			o = res.Objects[0]

			// Delete: this = EXDATE, all = DELETE with If-Match.
			res, err = p.Delete(t.Context(), cal, storedOf(o), api.EditScopeThis, calsync.WriteOptions{IfMatch: o.ETag, RecurrenceID: "2026-10-21T10:00:00Z"})
			if err != nil {
				t.Fatal(err)
			}
			if body := written(t, s, href); !strings.Contains(body, "EXDATE") {
				t.Fatalf("exdate:\n%s", body)
			}
			o = res.Objects[0]
			if _, err := p.Delete(t.Context(), cal, storedOf(o), api.EditScopeAll, calsync.WriteOptions{IfMatch: `"stale"`}); !isCode(err, api.CodeConflict) {
				t.Fatalf("stale delete %v", err)
			}
			s.ResetRequests()
			res, err = p.Delete(t.Context(), cal, storedOf(o), api.EditScopeAll, calsync.WriteOptions{IfMatch: o.ETag})
			if err != nil || !slices.Equal(res.Removed, []string{href}) {
				t.Fatalf("delete %+v %v", res, err)
			}
			if r := s.Requests(); r[0].Method != http.MethodDelete || r[0].Header.Get("If-Match") != o.ETag {
				t.Fatalf("delete request %+v", r[0])
			}
			if _, err := p.Delete(t.Context(), cal, storedOf(o), api.EditScopeAll, calsync.WriteOptions{IfMatch: o.ETag}); !isCode(err, api.CodeEventGone) {
				t.Fatalf("delete gone %v", err)
			}
		})
	}
}

func isCode(err error, c api.ErrorCode) bool {
	var ae *api.Error
	return errors.As(err, &ae) && ae.Code == c
}

func TestProviderReadOnly(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Nextcloud)
	ro := s.CalendarHref("team-shared_by_petr")
	href, _ := s.PutObject(ro, "a.ics", event("a", t0))
	p, _ := newProvider(t, s, nil)
	o, err := p.load(t.Context(), store.Event{RemoteID: href}, "")
	if err != nil {
		t.Fatal(err)
	}
	upd := *o.Master
	upd.Title = "x"
	// The row says write (stale): the server's 403 is readOnly.
	_, err = p.Update(t.Context(), store.Calendar{RemoteID: ro, Access: api.CalendarAccessWrite}, storedOf(o),
		calsync.Change{Scope: api.EditScopeAll, Event: upd}, calsync.WriteOptions{})
	if !isCode(err, api.CodeReadOnly) {
		t.Fatalf("%v", err)
	}
	if _, err := p.Create(t.Context(), store.Calendar{RemoteID: ro, Access: api.CalendarAccessRead}, upd, calsync.WriteOptions{}); !isCode(err, api.CodeReadOnly) {
		t.Fatalf("%v", err)
	}
}

func TestProviderInvitations(t *testing.T) {
	for _, sched := range []api.CalDAVScheduling{api.CalDAVSchedulingServer, api.CalDAVSchedulingClient} {
		t.Run(string(sched), func(t *testing.T) {
			s := caldavtest.New(t, caldavtest.Fastmail)
			coll := firstCalendar(s)
			p, mb := newProvider(t, s, func(o *ProviderOptions) { o.Scheduling = sched })
			cal := store.Calendar{RemoteID: coll, Access: api.CalendarAccessWrite}
			ev, err := calendar.FromInput(api.EventInput{Title: "Meet", Start: "2026-10-14T10:00:00", End: "2026-10-14T11:00:00", TimeZone: "UTC",
				Attendees: []api.EventInputAttendee{{Address: "petr@example.test"}, {Address: "eva@example.test"}}},
				calendar.InputOptions{UID: NewUID(), Organizer: &api.Address{Address: me}})
			if err != nil {
				t.Fatal(err)
			}
			res, err := p.Create(t.Context(), cal, ev, calsync.WriteOptions{Notify: true, MailAccountID: "acc_mail"})
			if err != nil {
				t.Fatal(err)
			}
			body := written(t, s, res.Objects[0].RemoteID)
			if sched == api.CalDAVSchedulingServer {
				if res.Invitations != (api.InvitationsSent{Via: api.InvitationsViaServer, Count: 2}) || len(mb.msgs) != 0 ||
					strings.Contains(body, "SCHEDULE-AGENT") {
					t.Fatalf("server: %+v %d\n%s", res.Invitations, len(mb.msgs), body)
				}
			} else {
				if res.Invitations != (api.InvitationsSent{Via: api.InvitationsViaMail, Count: 2}) || len(mb.msgs) != 1 ||
					mb.msgs[0].Method != imip.MethodRequest || len(mb.msgs[0].Recipients) != 2 ||
					!bytes.Contains(mb.msgs[0].Data, []byte("METHOD:REQUEST")) || mb.opts[0].MailAccountID != "acc_mail" {
					t.Fatalf("client: %+v %+v", res.Invitations, mb.msgs)
				}
				if !strings.Contains(body, "ATTENDEE;") || !strings.Contains(body, "SCHEDULE-AGENT=CLIENT") {
					t.Fatalf("attendees not marked:\n%s", body)
				}
				// No mail path: invitationsUnavailable before anything is written.
				p2, _ := newProvider(t, s, func(o *ProviderOptions) { o.Scheduling = sched; o.SendIMIP = nil })
				n := len(s.Objects(coll))
				if _, err := p2.Create(t.Context(), cal, ev, calsync.WriteOptions{Notify: true}); !isCode(err, api.CodeInvitationsUnavailable) || len(s.Objects(coll)) != n {
					t.Fatalf("no mail path: %v", err)
				}
			}
			mb.msgs = nil
			// The organiser's delete cancels.
			o := res.Objects[0]
			res, err = p.Delete(t.Context(), cal, storedOf(o), api.EditScopeAll, calsync.WriteOptions{IfMatch: o.ETag, Notify: true})
			if err != nil || res.Invitations.Count != 2 {
				t.Fatalf("delete %+v %v", res, err)
			}
			if sched == api.CalDAVSchedulingClient && (len(mb.msgs) != 1 || mb.msgs[0].Method != imip.MethodCancel) {
				t.Fatalf("cancel %+v", mb.msgs)
			}
		})
	}
}

func TestProviderRespond(t *testing.T) {
	for _, tc := range []struct {
		name    string
		profile caldavtest.Profile
		notify  bool
		mail    bool
		agent   bool
	}{
		{"server", caldavtest.Nextcloud, true, false, false},
		{"server without reply", caldavtest.Nextcloud, false, false, true},
		{"client", caldavtest.Radicale, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := caldavtest.New(t, tc.profile)
			coll := firstCalendar(s)
			href, _ := s.PutObject(coll, "inv.ics", weekly("inv", "petr@example.test", me, "eva@example.test"))
			p, mb := newProvider(t, s, nil)
			cal := store.Calendar{RemoteID: coll, Access: api.CalendarAccessWrite}
			o, err := p.load(t.Context(), store.Event{RemoteID: href}, "")
			if err != nil {
				t.Fatal(err)
			}
			res, err := p.Respond(t.Context(), cal, storedOf(o), calsync.Response{Scope: api.EditScopeAll, Response: api.ResponseAccepted},
				calsync.WriteOptions{IfMatch: o.ETag, Notify: tc.notify, Comment: "ok"})
			if err != nil {
				t.Fatal(err)
			}
			body := written(t, s, href)
			if !strings.Contains(body, "PARTSTAT=ACCEPTED") || !strings.Contains(body, "X-MALACHI-TEST") || !strings.Contains(body, "SEQUENCE:2") {
				t.Fatalf("respond:\n%s", body)
			}
			if got := strings.Contains(body, "ORGANIZER;CN=Org;SCHEDULE-AGENT=CLIENT"); got != tc.agent {
				t.Fatalf("organizer agent %v:\n%s", got, body)
			}
			if tc.mail {
				if len(mb.msgs) != 1 || mb.msgs[0].Method != imip.MethodReply || !slices.Equal(mb.msgs[0].Recipients, []string{"petr@example.test"}) ||
					!bytes.Contains(mb.msgs[0].Data, []byte("PARTSTAT=ACCEPTED")) || res.Invitations.Via != api.InvitationsViaMail {
					t.Fatalf("reply %+v %+v", mb.msgs, res.Invitations)
				}
			} else if len(mb.msgs) != 0 {
				t.Fatalf("mail sent %+v", mb.msgs)
			}
			if res.Objects[0].Master.MyResponse != api.ResponseAccepted {
				t.Fatal("my response not in the result")
			}

			// An instance: an override with the answer.
			o = res.Objects[0]
			mb.msgs = nil
			res, err = p.Respond(t.Context(), cal, storedOf(o), calsync.Response{Scope: api.EditScopeThis, Response: api.ResponseDeclined},
				calsync.WriteOptions{IfMatch: o.ETag, Notify: tc.notify, RecurrenceID: "2026-10-14T10:00:00Z"})
			if err != nil {
				t.Fatal(err)
			}
			ov := res.Objects[0].Override("2026-10-14T10:00:00Z")
			if ov == nil || ov.MyResponse != api.ResponseDeclined || res.Objects[0].Master.MyResponse != api.ResponseAccepted {
				t.Fatalf("instance %+v", res.Objects[0].Overrides)
			}
			if tc.mail && (len(mb.msgs) != 1 || !bytes.Contains(mb.msgs[0].Data, []byte("RECURRENCE-ID"))) {
				t.Fatalf("instance reply %+v", mb.msgs)
			}
		})
	}
	// Not an attendee.
	s := caldavtest.New(t, caldavtest.Fastmail)
	coll := firstCalendar(s)
	href, _ := s.PutObject(coll, "x.ics", weekly("x", "petr@example.test", "eva@example.test"))
	p, _ := newProvider(t, s, nil)
	o, _ := p.load(t.Context(), store.Event{RemoteID: href}, "")
	if _, err := p.Respond(t.Context(), store.Calendar{RemoteID: coll}, storedOf(o), calsync.Response{Scope: api.EditScopeAll, Response: api.ResponseAccepted},
		calsync.WriteOptions{}); !isCode(err, api.CodeInvalidArgument) {
		t.Fatalf("%v", err)
	}
}

func TestProviderStoresForeignInvitation(t *testing.T) {
	s := caldavtest.New(t, caldavtest.ICloud)
	coll := firstCalendar(s)
	p, mb := newProvider(t, s, nil)
	inv, err := calendar.Parse(weekly("ext-uid@example.org", "petr@example.test", me), calendar.FromOptions{Me: []string{me}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Create(t.Context(), store.Calendar{RemoteID: coll}, *inv.Master, calsync.WriteOptions{Notify: true})
	if err != nil {
		t.Fatal(err)
	}
	body := written(t, s, res.Objects[0].RemoteID)
	if !strings.Contains(body, "UID:ext-uid@example.org") || !strings.Contains(body, "SCHEDULE-AGENT=CLIENT") ||
		strings.Contains(res.Objects[0].RemoteID, "ext-uid") || len(mb.msgs) != 0 || res.Objects[0].ETag == "" {
		t.Fatalf("%s %+v\n%s", res.Objects[0].RemoteID, mb.msgs, body)
	}
}
