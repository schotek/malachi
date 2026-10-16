// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package calsync

import (
	"context"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

func calendarRows(t *testing.T, st *store.Store, accountID string) []store.Calendar {
	t.Helper()
	cals, err := st.ListCalendars(context.Background(), accountID)
	if err != nil {
		t.Fatal(err)
	}
	return cals
}

func idleWithSync(sv *Supervisor, id string) func() bool {
	return func() bool {
		s, ok := sv.CalendarState(id)
		return ok && s.Status == api.CalendarSyncIdle && s.LastSync != nil
	}
}

func TestPassStoresObjectsAndOccurrences(t *testing.T) {
	h := newHarness(t)
	p := newFakeProvider(RemoteCalendar{RemoteID: "/cal/", Name: "Home‮", Color: "#AABBCCFF", Access: api.CalendarAccessOwner, IsDefault: true})
	p.put("/cal/", timed("one", t0.Add(24*time.Hour), time.Hour))
	p.put("/cal/", weekly("series", t0.Add(-7*24*time.Hour), 5))
	a := caldavAccount("acc_1")
	h.setProvider(a.ID, p)
	h.sv.Start(a) // queued before Run
	h.run()
	eventually(t, "first pass", idleWithSync(h.sv, a.ID))

	cals := calendarRows(t, h.st, a.ID)
	if len(cals) != 1 || cals[0].Name != "Home" || cals[0].ServerColor != "#aabbcc" || cals[0].SyncMode != syncModeDaemon {
		t.Fatalf("calendars %+v", cals)
	}
	if cals[0].SyncToken == "" || cals[0].WindowFrom != "2025-10-16" || cals[0].WindowTo != "2027-11-20" {
		t.Errorf("sync state %+v", cals[0].CalendarSyncState)
	}
	occ := occurrences(t, h.st, a.ID, t0.Add(-30*24*time.Hour), t0.Add(60*24*time.Hour))
	if len(occ) != 6 { // one single + five weekly instances
		t.Fatalf("%d occurrences: %+v", len(occ), occ)
	}
	series := 0
	for _, o := range occ {
		if o.SeriesID != "" {
			series++
			if o.OccurrenceKey == "" {
				t.Errorf("series instance without a key")
			}
		}
	}
	if series != 5 {
		t.Errorf("series instances %d", series)
	}
	res, err := h.st.ListResources(context.Background(), cals[0].ID)
	if err != nil || len(res) != 2 || res[0].State != store.ResourceStored {
		t.Errorf("resources %+v %v", res, err)
	}
	// notify.calendarChanged comes once, a second after the pass.
	if len(h.note.changedList()) != 0 {
		t.Errorf("changed before the coalescing delay")
	}
	h.clock.Advance(time.Second)
	eventually(t, "calendar changed", func() bool { return len(h.note.changedList()) == 1 })
	c := h.note.changedList()[0]
	if len(c.AccountIDs) != 1 || c.AccountIDs[0] != "acc_1" || len(c.CalendarIDs) != 1 {
		t.Errorf("notification %+v", c)
	}
}

func TestFullPassRemovesVanished(t *testing.T) {
	h := newHarness(t)
	p := newFakeProvider(RemoteCalendar{RemoteID: "/cal/", Name: "C", Access: api.CalendarAccessOwner})
	p.put("/cal/", timed("a", t0.Add(time.Hour), time.Hour))
	p.put("/cal/", timed("b", t0.Add(2*time.Hour), time.Hour))
	p.put("/cal/", timed("old", t0.Add(-500*24*time.Hour), time.Hour)) // outside the window
	a := caldavAccount("acc_1")
	h.setProvider(a.ID, p)
	h.run()
	h.sv.Start(a)
	eventually(t, "first pass", idleWithSync(h.sv, a.ID))
	if n := len(occurrences(t, h.st, a.ID, t0, t0.Add(24*time.Hour))); n != 2 {
		t.Fatalf("%d occurrences", n)
	}
	cal := calendarRows(t, h.st, a.ID)[0]
	res, _ := h.st.ListResources(context.Background(), cal.ID)
	outside := 0
	for _, r := range res {
		if r.State == store.ResourceOutside {
			outside++
		}
	}
	if outside != 1 {
		t.Errorf("resources %+v", res)
	}
	if rows, _ := h.st.EventsByRemoteID(context.Background(), cal.ID, "/cal/old.ics"); len(rows) != 0 {
		t.Errorf("an object outside the window was stored")
	}

	p.drop("/cal/", "/cal/b.ics")
	before := p.callsOf("/cal/")
	h.sv.SyncNow(a.ID, cal.ID)
	eventually(t, "second pass", func() bool {
		return p.callsOf("/cal/") > before && len(occurrences(t, h.st, a.ID, t0, t0.Add(24*time.Hour))) == 1
	})
	if rows, _ := h.st.EventsByRemoteID(context.Background(), cal.ID, "/cal/b.ics"); len(rows) != 0 {
		t.Errorf("vanished object kept")
	}
}

func TestIncrementalPassKeepsUnseen(t *testing.T) {
	h := newHarness(t)
	p := newFakeProvider(RemoteCalendar{RemoteID: "/cal/", Name: "C"})
	p.put("/cal/", timed("a", t0.Add(time.Hour), time.Hour))
	a := caldavAccount("acc_1")
	h.setProvider(a.ID, p)
	h.run()
	h.sv.Start(a)
	eventually(t, "first pass", idleWithSync(h.sv, a.ID))
	p.mu.Lock()
	p.full = false
	p.mu.Unlock()
	p.drop("/cal/", "/cal/a.ics")
	before := p.callsOf("/cal/")
	h.sv.SyncNow(a.ID, "")
	eventually(t, "pass", func() bool { return p.callsOf("/cal/") > before })
	eventually(t, "idle", idleWithSync(h.sv, a.ID))
	if n := len(occurrences(t, h.st, a.ID, t0, t0.Add(24*time.Hour))); n != 1 {
		t.Errorf("an incremental pass removed an object it did not name (%d)", n)
	}
}

func TestDisabledCalendarKeepsItsRow(t *testing.T) {
	h := newHarness(t)
	p := newFakeProvider(RemoteCalendar{RemoteID: "/a/", Name: "A"}, RemoteCalendar{RemoteID: "/b/", Name: "B"})
	p.put("/b/", timed("x", t0.Add(time.Hour), time.Hour, 15))
	a := caldavAccount("acc_1")
	h.setProvider(a.ID, p)
	h.run()
	h.sv.Start(a)
	eventually(t, "first pass", idleWithSync(h.sv, a.ID))
	if n := len(calendarRows(t, h.st, a.ID)); n != 2 {
		t.Fatalf("%d calendars", n)
	}
	eventually(t, "alert", func() bool { return len(alertRows(t, h.st)) == 1 })
	rowOf := func(remote string) store.Calendar {
		for _, c := range calendarRows(t, h.st, a.ID) {
			if c.RemoteID == remote {
				return c
			}
		}
		t.Fatalf("no row for %s", remote)
		return store.Calendar{}
	}
	ctx := context.Background()
	hidden := false
	if _, err := h.st.UpdateCalendarUser(ctx, rowOf("/b/").ID, &hidden, nil); err != nil {
		t.Fatal(err)
	}

	// /b/ disabled: the row stays (marked, user columns kept), the contents go.
	a.Config.Calendar = &api.CalendarConfig{Sync: true, DisabledCalendars: []string{"/b/"}}
	h.sv.Restart(a)
	eventually(t, "disabled", func() bool { return Disabled(rowOf("/b/")) })
	b := rowOf("/b/")
	if b.Name != "B" || b.Visible || b.SyncToken != "" {
		t.Errorf("disabled row %+v", b)
	}
	if n := len(occurrences(t, h.st, a.ID, t0, t0.Add(24*time.Hour))); n != 0 {
		t.Errorf("occurrences of a disabled calendar kept: %d", n)
	}
	if rows, _ := h.st.CalendarEvents(ctx, b.ID); len(rows) != 0 {
		t.Errorf("events kept")
	}
	if res, _ := h.st.ListResources(ctx, b.ID); len(res) != 0 {
		t.Errorf("resources kept")
	}
	eventually(t, "alerts gone", func() bool { return len(alertRows(t, h.st)) == 0 })
	// It is never synchronised while disabled.
	before := p.callsOf("/b/")
	h.sv.SyncNow(a.ID, "")
	h.sv.SyncNow(a.ID, b.ID)
	h.advanceUntil("a few passes", 5*time.Minute, time.Hour, func() bool { return p.callsOf("/a/") >= 6 })
	if p.callsOf("/b/") != before {
		t.Errorf("a disabled calendar was synchronised")
	}

	// Enabled again: a fresh state and a pass.
	a.Config.Calendar = &api.CalendarConfig{Sync: true}
	h.sv.Restart(a)
	eventually(t, "re-enabled pass", func() bool {
		return p.callsOf("/b/") > before && len(occurrences(t, h.st, a.ID, t0, t0.Add(24*time.Hour))) == 1
	})
	eventually(t, "row synced", func() bool { c := rowOf("/b/"); return !Disabled(c) && c.SyncMode == syncModeDaemon })
	for _, c := range p.calls() {
		if c.remoteID == "/b/" && c.token != "" && !Disabled(rowOf("/b/")) && p.callsOf("/b/") == before+1 {
			t.Errorf("re-enabled pass kept a token")
		}
	}

	// /a/ gone from the server: its row goes whole.
	p.mu.Lock()
	p.cals = p.cals[1:]
	p.mu.Unlock()
	h.sv.SyncNow(a.ID, "")
	eventually(t, "vanished", func() bool { return len(calendarRows(t, h.st, a.ID)) == 1 })
	if rowOf("/b/").ID == "" {
		t.Errorf("wrong calendar deleted")
	}
}

func TestStartStopRestart(t *testing.T) {
	h := newHarness(t)
	a, b := caldavAccount("acc_1"), caldavAccount("acc_2")
	h.setProvider(a.ID, newFakeProvider(RemoteCalendar{RemoteID: "/a/"}))
	h.setProvider(b.ID, newFakeProvider(RemoteCalendar{RemoteID: "/b/"}))
	off := graphAccount("acc_3", false)
	paused := caldavAccount("acc_4")
	paused.Enabled = false
	h.run()
	for _, x := range []store.Account{a, a, b, off, paused} {
		h.sv.Start(x)
	}
	eventually(t, "both idle", func() bool { return idleWithSync(h.sv, a.ID)() && idleWithSync(h.sv, b.ID)() })
	if h.madeCount(a.ID) != 1 {
		t.Errorf("second Start made another syncer (%d providers)", h.madeCount(a.ID))
	}
	if _, ok := h.sv.CalendarState(off.ID); ok {
		t.Errorf("graph account without calendar sync started")
	}
	if _, ok := h.sv.CalendarState(paused.ID); ok {
		t.Errorf("paused account started")
	}
	if got := len(h.sv.CalendarStates()); got != 2 {
		t.Errorf("%d states", got)
	}
	h.sv.Stop(a.ID)
	if _, ok := h.sv.CalendarState(a.ID); ok {
		t.Errorf("state after Stop")
	}
	if h.sv.SyncNow(a.ID, "") {
		t.Errorf("SyncNow of a stopped account")
	}
	if len(calendarRows(t, h.st, a.ID)) != 1 {
		t.Errorf("Stop deleted the cache")
	}
	h.sv.Restart(b)
	eventually(t, "restarted", func() bool { return h.madeCount(b.ID) == 2 && idleWithSync(h.sv, b.ID)() })
	h.stop()
	if len(h.sv.CalendarStates()) != 0 {
		t.Errorf("syncers left after Run ended")
	}
}

func TestStateMachineAndBackoff(t *testing.T) {
	h := newHarness(t)
	p := newFakeProvider(RemoteCalendar{RemoteID: "/cal/"})
	p.setListErr(api.NewError(api.CodeNetworkError, "down"))
	a := caldavAccount("acc_1")
	h.setProvider(a.ID, p)
	h.run()
	h.sv.Start(a)
	state := func(want api.CalendarSyncStatus) func() bool {
		return func() bool { s, _ := h.sv.CalendarState(a.ID); return s.Status == want }
	}
	eventually(t, "offline", state(api.CalendarSyncOffline))
	if m, _ := h.sv.Mail().State(a.ID); m.Status != api.SyncOffline || m.Error == nil || m.Error.Code != api.CodeNetworkError {
		t.Errorf("mail state %+v", m)
	}
	// Backoff 30 s (the harness: (attempt+1)·30 s): nothing before it.
	calls := func() int { p.mu.Lock(); defer p.mu.Unlock(); return p.listCalls }
	eventually(t, "first try", func() bool { return calls() == 1 && h.madeCount(a.ID) == 1 })
	time.Sleep(20 * time.Millisecond)
	h.clock.Advance(29 * time.Second)
	time.Sleep(20 * time.Millisecond)
	if calls() != 1 {
		t.Fatalf("retried before the backoff (%d)", calls())
	}
	h.clock.Advance(time.Second)
	eventually(t, "second try", func() bool { return calls() == 2 })
	// The second wait is 60 s.
	time.Sleep(20 * time.Millisecond)
	h.clock.Advance(59 * time.Second)
	time.Sleep(20 * time.Millisecond)
	if calls() != 2 {
		t.Fatalf("second backoff too short")
	}
	// Refused credentials: authRequired, announced once.
	p.setListErr(api.NewError(api.CodeAuthFailed, "no"))
	h.clock.Advance(time.Second)
	eventually(t, "auth required", state(api.CalendarSyncAuthRequired))
	h.advanceUntil("another auth failure", 30*time.Second, 10*time.Minute, func() bool { return calls() >= 4 })
	if n := len(h.note.authList()); n != 1 {
		t.Errorf("%d authRequired notifications", n)
	}
	if m, _ := h.sv.Mail().State(a.ID); m.Status != api.SyncAuthRequired {
		t.Errorf("mail state %+v", m)
	}
	// A wake (SyncNow) retries at once and recovers.
	p.setListErr(nil)
	h.sv.SyncNow(a.ID, "")
	eventually(t, "idle", idleWithSync(h.sv, a.ID))
	if s, _ := h.sv.CalendarState(a.ID); s.Error != nil || s.Progress != -1 {
		t.Errorf("state %+v", s)
	}
	// The states seen include syncing.
	saw := false
	for _, s := range h.note.stateList() {
		saw = saw || s.Status == api.CalendarSyncSyncing
	}
	if !saw {
		t.Errorf("no syncing state announced")
	}
}

func TestConsentRequiredFromTheFactory(t *testing.T) {
	h := newHarness(t)
	a := graphAccount("acc_g", true)
	h.mu.Lock()
	h.factErr[a.ID] = api.NewError(api.CodeCalendarConsentRequired, "scope")
	h.mu.Unlock()
	h.run()
	h.sv.Start(a)
	eventually(t, "consent", func() bool { s, _ := h.sv.CalendarState(a.ID); return s.Status == api.CalendarSyncConsentRequired })
	if _, ok := h.sv.Mail().State(a.ID); ok {
		t.Errorf("a graph account's calendar state reached the mail surface")
	}
	if len(h.sv.Mail().States()) != 0 || h.sv.Mail().Trigger(a.ID, "", false) {
		t.Errorf("graph account on the caldav surface")
	}
	if len(h.note.authList()) != 0 || len(h.note.mail) != 0 {
		t.Errorf("graph calendar touched the mail notifications")
	}
}

func TestCalendarErrorDoesNotStopOthers(t *testing.T) {
	h := newHarness(t)
	p := newFakeProvider(RemoteCalendar{RemoteID: "/a/"}, RemoteCalendar{RemoteID: "/b/"})
	p.syncErr["/a/"] = api.NewError(api.CodeMalformedCalendar, "bad")
	p.put("/b/", timed("x", t0.Add(time.Hour), time.Hour))
	a := caldavAccount("acc_1")
	h.setProvider(a.ID, p)
	h.run()
	h.sv.Start(a)
	eventually(t, "error state", func() bool { s, _ := h.sv.CalendarState(a.ID); return s.Status == api.CalendarSyncError })
	if n := len(occurrences(t, h.st, a.ID, t0, t0.Add(24*time.Hour))); n != 1 {
		t.Errorf("the other calendar was not synchronised (%d)", n)
	}
	for _, c := range calendarRows(t, h.st, a.ID) {
		if (c.RemoteID == "/a/") != (c.ErrorCode == int(api.CodeMalformedCalendar)) {
			t.Errorf("calendar %s error %d", c.RemoteID, c.ErrorCode)
		}
	}
	if m, _ := h.sv.Mail().State(a.ID); m.Status != api.SyncError {
		t.Errorf("mail state %+v", m)
	}
}

func TestCadence(t *testing.T) {
	h := newHarness(t)
	g := newFakeProvider(RemoteCalendar{RemoteID: "def", IsDefault: true}, RemoteCalendar{RemoteID: "other"})
	g.deltaMode = "delta"
	c := newFakeProvider(RemoteCalendar{RemoteID: "/dav/"})
	ga, ca := graphAccount("acc_g", true), caldavAccount("acc_c")
	h.setProvider(ga.ID, g)
	h.setProvider(ca.ID, c)
	h.run()
	h.sv.Start(ga)
	h.sv.Start(ca)
	eventually(t, "first passes", func() bool { return idleWithSync(h.sv, ga.ID)() && idleWithSync(h.sv, ca.ID)() })
	h.advanceUntil("ten minutes", 30*time.Second, 11*time.Minute, func() bool {
		return h.clock.Now().Sub(t0) >= 10*time.Minute && g.callsOf("other") >= 2
	})
	// In ten minutes: default every 2 min (1 + 5), other every 10 (1 + 1),
	// caldav every 5 (1 + 2).
	if n := g.callsOf("def"); n < 5 || n > 7 {
		t.Errorf("default calendar %d passes", n)
	}
	if n := g.callsOf("other"); n != 2 {
		t.Errorf("other calendar %d passes", n)
	}
	eventually(t, "caldav passes", func() bool { return c.callsOf("/dav/") >= 3 })
	if n := c.callsOf("/dav/"); n > 4 {
		t.Errorf("caldav %d passes", n)
	}
	if cals := calendarRows(t, h.st, ga.ID); cals[0].SyncMode != "delta" {
		t.Errorf("graph sync mode %q", cals[0].SyncMode)
	}
}

func TestFullReconcileDaily(t *testing.T) {
	h := newHarness(t)
	p := newFakeProvider(RemoteCalendar{RemoteID: "/cal/"})
	a := caldavAccount("acc_1")
	h.setProvider(a.ID, p)
	h.run()
	h.sv.Start(a)
	eventually(t, "first pass", idleWithSync(h.sv, a.ID))
	h.advanceUntil("a day", 10*time.Minute, 25*time.Hour, func() bool { return h.clock.Now().Sub(t0) > 24*time.Hour+5*time.Minute })
	time.Sleep(20 * time.Millisecond)
	empty := 0
	for i, c := range p.calls() {
		if i > 0 && c.token == "" {
			empty++
		}
	}
	if empty != 1 {
		t.Errorf("%d full reconciles in a day", empty)
	}
}

func TestMailStateMapping(t *testing.T) {
	cases := map[api.CalendarSyncStatus]api.SyncStatus{
		api.CalendarSyncIdle: api.SyncIdle, api.CalendarSyncSyncing: api.SyncSyncing, api.CalendarSyncOffline: api.SyncOffline,
		api.CalendarSyncAuthRequired: api.SyncAuthRequired, api.CalendarSyncConsentRequired: api.SyncAuthRequired,
		api.CalendarSyncError: api.SyncError, api.CalendarSyncDisabled: api.SyncDisabled,
	}
	for in, want := range cases {
		if got := MailState(api.CalendarSyncState{AccountID: "a", Status: in, Progress: 40}); got.Status != want || got.Progress != 40 || got.AccountID != "a" {
			t.Errorf("%s → %+v", in, got)
		}
	}
	h := newHarness(t)
	a := caldavAccount("acc_1")
	h.setProvider(a.ID, newFakeProvider(RemoteCalendar{RemoteID: "/cal/"}))
	h.run()
	h.sv.Mail().Start(a)
	eventually(t, "idle", idleWithSync(h.sv, a.ID))
	st, ok := h.sv.Mail().State(a.ID)
	if !ok || st.Status != api.SyncIdle || st.LastSync == nil {
		t.Errorf("state %+v %v", st, ok)
	}
	if len(h.sv.Mail().States()) != 1 || !h.sv.Mail().Trigger(a.ID, "", true) {
		t.Errorf("caldav surface")
	}
	eventually(t, "syncState notified", func() bool {
		h.note.mu.Lock()
		defer h.note.mu.Unlock()
		return len(h.note.mail) > 0
	})
}

func TestApplyWrite(t *testing.T) {
	h := newHarness(t)
	p := newFakeProvider(RemoteCalendar{RemoteID: "/cal/"})
	a := caldavAccount("acc_1")
	h.setProvider(a.ID, p)
	h.run()
	h.sv.Start(a)
	eventually(t, "idle", idleWithSync(h.sv, a.ID))
	cal := calendarRows(t, h.st, a.ID)[0]
	if err := h.sv.ApplyWrite(context.Background(), a, cal.ID, WriteResult{Objects: []Object{timed("w", t0.Add(time.Hour), time.Hour)}}); err != nil {
		t.Fatal(err)
	}
	if n := len(occurrences(t, h.st, a.ID, t0, t0.Add(24*time.Hour))); n != 1 {
		t.Errorf("%d occurrences after a write", n)
	}
	if err := h.sv.ApplyWrite(context.Background(), a, cal.ID, WriteResult{Removed: []string{"/cal/w.ics"}}); err != nil {
		t.Fatal(err)
	}
	if n := len(occurrences(t, h.st, a.ID, t0, t0.Add(24*time.Hour))); n != 0 {
		t.Errorf("%d occurrences after a removal", n)
	}
}
