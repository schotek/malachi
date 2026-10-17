// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"

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
