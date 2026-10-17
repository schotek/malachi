// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"encoding/json"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/calendardata"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// Preferences → Calendar over the in-memory sample source (no daemon, no
// window): the Display group, one section per sample account, the CalDAV
// rows only in the CalDAV account's section, and the empty state without
// accounts. Run with MALACHI_GTK_SMOKE=1 and a
// GTK display.
func TestPreferencesCalendarGTKSmoke(t *testing.T) {
	if os.Getenv("MALACHI_GTK_SMOKE") != "1" {
		t.Skip("set MALACHI_GTK_SMOKE=1 with a GTK display")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	adw.Init()
	src := calendardata.NewSampleSource(time.Date(2026, time.October, 14, 10, 0, 0, 0, time.Local), time.Local, nil)
	b := data.Builder("preferences.ui")
	d := &PreferencesDialog{
		PreferencesDialog: b.GetObject("preferences_dialog").Cast().(*adw.PreferencesDialog),
		cal:               func() calendardata.DataSource { return src },
	}
	stop := d.bindCalendar(b, nil)
	defer func() { d.closed = true; stop() }()
	flush := func() {
		ctx := glib.MainContextDefault()
		for i := 0; i < 200 && ctx.Pending(); i++ {
			ctx.Iteration(false)
		}
	}
	flush()
	p := d.calendarPage
	if p == nil {
		t.Fatal("no calendar page")
	}
	lines := p.calendarLayoutLines()
	for _, l := range lines {
		t.Log("LAYOUT: " + l)
	}
	tr := i18n.Tr
	// Nothing on load writes (the samples answer every write with an
	// error): no controller error, no group description.
	for _, l := range lines {
		if strings.Contains(l, " error:") {
			t.Errorf("error right after load: %s", l)
		}
	}
	if !strings.Contains(lines[0], calendar.WeekStartSetting(tr)) || !strings.Contains(lines[0], calendar.WorkingHoursSetting(tr)) {
		t.Errorf("display group: %q", lines[0])
	}
	if len(p.sections) != 3 {
		t.Fatalf("sections: %d", len(p.sections))
	}
	server := calendar.CalDAVField(calendar.CalDAVServer, tr)
	for _, s := range p.sections {
		caldav := s.ctl.IsCalDAV()
		if got := slices.Contains(s.rowTitles, server); got != caldav {
			t.Errorf("%s: server row %v, caldav %v", s.layout.AccountID, got, caldav)
		}
		if got := slices.Contains(s.rowTitles, calendar.RemoveAccount(tr)); got != caldav {
			t.Errorf("%s: remove row %v", s.layout.AccountID, got)
		}
		if caldav == s.invitations.Visible() {
			t.Errorf("%s: invitations shown %v", s.layout.AccountID, s.invitations.Visible())
		}
		if s.ctl.SyncOn() && len(s.calRows) == 0 {
			t.Errorf("%s: no calendar rows", s.layout.AccountID)
		}
	}
	if p.empty.Visible() {
		t.Error("empty state with accounts")
	}
	// The display group's rows reflect the preferences.
	if p.weekStart.selected() < 0 || p.duration.selected() < 0 || p.workStart.selected() < 0 || p.workEnd.selected() < 0 {
		t.Errorf("display selection: %d %d %d %d", p.weekStart.selected(), p.duration.selected(), p.workStart.selected(), p.workEnd.selected())
	}
	if len(p.dayButtons) != 7 {
		t.Errorf("day toggles: %d", len(p.dayButtons))
	}
	// 50 calendarState notifications (the daemon's keyring retries) and 10
	// reloads with the same accounts and calendars (accountsChanged)
	// recreate no widget, register no observer and leave nothing running.
	type ident struct {
		groups []*adw.PreferencesGroup
		rows   []*adw.ActionRow
		shown  [][]any
	}
	snap := func() ident {
		var id ident
		id.groups = append(id.groups, p.display, p.empty, p.add)
		for _, s := range p.sections {
			id.groups = append(id.groups, s.group, s.invitations)
			for _, w := range s.calWidgets {
				id.rows = append(id.rows, w.row)
			}
			var sh []any
			for _, w := range s.shown {
				sh = append(sh, w)
			}
			id.shown = append(id.shown, sh)
		}
		return id
	}
	before, observers, pageBefore := snap(), src.Observers(), p.page
	statuses := []api.CalendarSyncStatus{api.CalendarSyncSyncing, api.CalendarSyncError}
	for i := range 50 {
		st := api.CalendarSyncState{AccountID: calendar.SampleWork, Status: statuses[i%2], Progress: -1}
		if st.Status == api.CalendarSyncError {
			st.Error = &api.Error{Code: api.CodeInternalError, Message: "keyringError"}
		}
		raw, _ := json.Marshal(api.CalendarStateNotification{State: st})
		src.Handle(api.NotifyCalendarState, raw)
		flush()
	}
	for range 10 {
		src.Replace(src.Data(), calendardata.PhaseReady)
		flush()
	}
	settleIdle := func() {
		end := time.Now().Add(prefsRebuildDelay*time.Millisecond + 400*time.Millisecond)
		for time.Now().Before(end) {
			glib.MainContextDefault().Iteration(false)
			time.Sleep(10 * time.Millisecond)
		}
	}
	settleIdle()
	after := snap()
	if p.page != pageBefore || !slices.Equal(before.groups, after.groups) || !slices.Equal(before.rows, after.rows) ||
		len(before.shown) != len(after.shown) {
		t.Fatal("state notifications or reloads recreated widgets")
	}
	for i := range before.shown {
		if !slices.Equal(before.shown[i], after.shown[i]) {
			t.Errorf("section %d: rows recreated", i)
		}
	}
	if src.Observers() != observers {
		t.Errorf("observers %d → %d", observers, src.Observers())
	}
	for _, s := range p.sections {
		if s.rebuildTimer != 0 {
			t.Errorf("%s: a rebuild is still pending", s.layout.AccountID)
		}
	}
	if glib.MainContextDefault().Pending() {
		t.Error("the main loop is still busy after the notifications")
	}

	// Two snapshot changes of the same structure keep the row widgets; a
	// changed calendar set rebuilds the section (after the debounce).
	work := p.sections[0]
	if len(work.calWidgets) < 2 {
		t.Fatalf("work calendars: %d", len(work.calWidgets))
	}
	first, group := work.calWidgets[0].row, work.group
	for range 2 {
		src.Update(func(s *calendardata.Snapshot) {
			s.CalendarAccounts = slices.Clone(s.CalendarAccounts)
			s.CalendarAccounts[0].State.Status = api.CalendarSyncError
			s.CalendarAccounts[0].State.Error = &api.Error{Code: 1, Message: "the system keyring is unavailable"}
		})
		flush()
	}
	settle := func() {
		end := time.Now().Add(prefsRebuildDelay*time.Millisecond + 400*time.Millisecond)
		for time.Now().Before(end) {
			glib.MainContextDefault().Iteration(false)
			time.Sleep(10 * time.Millisecond)
		}
	}
	settle()
	if p.sections[0] != work || work.group != group || work.calWidgets[0].row != first {
		t.Error("a state change rebuilt the rows")
	}
	src.Update(func(s *calendardata.Snapshot) {
		s.CalendarAccounts = slices.Clone(s.CalendarAccounts)
		a := s.CalendarAccounts[0]
		a.Calendars = slices.Clone(a.Calendars[:len(a.Calendars)-1])
		s.CalendarAccounts[0] = a
	})
	settle()
	if len(work.calWidgets) == 0 || work.calWidgets[0].row == first {
		t.Error("a changed calendar set did not rebuild the rows")
	}
	for _, l := range p.calendarLayoutLines() {
		t.Log("LAYOUT (after removing a calendar): " + l)
	}

	// Without accounts the empty state replaces the sections.
	empty := calendardata.NewEmptySource(time.Local, nil)
	b2 := data.Builder("preferences.ui")
	d2 := &PreferencesDialog{
		PreferencesDialog: b2.GetObject("preferences_dialog").Cast().(*adw.PreferencesDialog),
		cal:               func() calendardata.DataSource { return empty },
	}
	stop2 := d2.bindCalendar(b2, nil)
	defer func() { d2.closed = true; stop2() }()
	flush()
	for _, l := range d2.calendarPage.calendarLayoutLines() {
		t.Log("LAYOUT (no accounts): " + l)
	}
	if len(d2.calendarPage.sections) != 0 || !d2.calendarPage.empty.Visible() {
		t.Errorf("empty source: %d sections, empty %v", len(d2.calendarPage.sections), d2.calendarPage.empty.Visible())
	}
}
