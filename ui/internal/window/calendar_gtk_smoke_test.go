// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/calendardata"
)

// Run in a dedicated test process with a display, for example Broadway or
// xvfb-run: MALACHI_GTK_SMOKE=1 go test ./internal/window -run
// '^TestCalendarGTKSmoke$' -count=1 -v. Only the invented sample calendar
// (calendardata.NewSampleSource) is used: no settings, daemon or account.
// It builds the Calendar's page shell (G9), steps through the four views,
// ‹ ›, Today and the search, and logs LAYOUT: lines with the allocations
// and the header's texts.
func TestCalendarGTKSmoke(t *testing.T) {
	if os.Getenv("MALACHI_GTK_SMOKE") != "1" {
		t.Skip("set MALACHI_GTK_SMOKE=1 with a GTK display to run the widget smoke test")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	adw.Init()
	wb := data.Builder("window.ui")
	w := &Window{
		ApplicationWindow: wb.GetObject("main_window").Cast().(*adw.ApplicationWindow),
		modeSwitchMail:    wb.GetObject("mode_switch").Cast().(*adw.ToggleGroup),
		modeStack:         wb.GetObject("mode_stack").Cast().(*gtk.Stack),
		calendarPageBin:   wb.GetObject("calendar_page_bin").Cast().(*adw.Bin),
		log:               slog.Default(), mode: board.ModeCalendar,
		actions:           make(map[string]*gio.SimpleAction),
		modeSwitchBoardOn: true,
		toasts:            adw.NewToastOverlay(),
		dateWeekStart:     time.Monday,
	}
	defer w.Destroy()
	src := calendardata.NewSampleSource(time.Now(), time.Local, glibLoop{})
	w.cal.source = src
	w.ensureCalendar()
	p := w.cal.page
	if p == nil || w.calendarSource() != calendardata.DataSource(src) {
		t.Fatal("the page is not built over the window's source")
	}
	// In window.blp's own mode stack, as the application shows it.
	w.modeStack.SetVisibleChildName(board.ModeCalendar.Nick())
	w.SetDefaultSize(1280, 900)
	w.Present()
	p.start()
	flush := func() {
		ctx := glib.MainContextDefault()
		for i := 0; i < 200 && ctx.Pending(); i++ {
			ctx.Iteration(false)
		}
	}
	// wait runs the main loop for d (the search's debounce, the first
	// allocation).
	wait := func(d time.Duration) {
		end := time.Now().Add(d)
		ctx := glib.MainContextDefault()
		for time.Now().Before(end) {
			ctx.Iteration(false)
			time.Sleep(5 * time.Millisecond)
		}
	}
	wait(300 * time.Millisecond)
	flush()
	headerMin := func() int {
		m, _, _, _ := p.header.Measure(gtk.OrientationHorizontal, -1)
		return m
	}
	layout := func(step string) {
		t.Logf("LAYOUT: %s page=%dx%d sidebar=%dx%d content=%dx%d header=%d(min %d) stack=%q view=%q title=%q subtitle=%q status=%q note=%q",
			step, p.root.Width(), p.root.Height(), p.sidebarBin.Width(), p.sidebarBin.Height(),
			p.stack.Width(), p.stack.Height(), p.header.Width(), headerMin(), p.stack.VisibleChildName(), p.viewSwitch.ActiveName(),
			p.title.Title(), p.title.Subtitle(), p.statusLabel.Label(), p.statusNote.Label())
	}
	if _, empty := p.ctl.EmptyKind(); empty {
		t.Fatal("the sample calendar shows the empty state")
	}
	if got := p.sidebarBin.Width(); got != 240 {
		t.Errorf("sidebar is %d px wide, want 240", got)
	}
	if !p.split.ShowSidebar() || p.split.Collapsed() {
		t.Errorf("sidebar not shown beside the content at 1280 px (shown %v, collapsed %v)", p.split.ShowSidebar(), p.split.Collapsed())
	}
	if p.statusLabel.Label() == "" {
		t.Error("the status strip has no calendar line")
	}
	if p.today.Label() == "" {
		t.Error("Today has no label")
	}
	for _, v := range calendar.Views {
		if tg := p.viewSwitch.ToggleByName(v.Nick()); tg == nil || tg.Label() == "" {
			t.Fatalf("view switch has no labelled %q toggle", v.Nick())
		}
	}
	layout("start")
	t.Log(p.minLine())
	t.Log(w.modeMinLine())
	for _, width := range []int{1024, 700} {
		w.SetDefaultSize(width, 768)
		wait(300 * time.Millisecond)
		flush()
		t.Logf("at %d: %s", width, p.minLine())
		t.Logf("at %d: %s", width, w.modeMinLine())
		if got := devMinWidth(w.modeStack); got > 700 || devMinWidth(p.root) > 700 {
			t.Errorf("at %d px the mode stack asks for %d px", width, got)
		}
		if got := p.root.Width(); got > width {
			t.Errorf("at %d px the page is %d px wide", width, got)
		}
	}
	w.SetDefaultSize(1280, 900)
	wait(300 * time.Millisecond)
	flush()

	for _, v := range calendar.Views {
		p.viewSwitch.SetActiveName(v.Nick())
		flush()
		if p.ctl.View() != v {
			t.Fatalf("the %s toggle shows %s", v.Nick(), p.ctl.View().Nick())
		}
		if got := p.stack.VisibleChildName(); got != v.Nick() {
			t.Fatalf("view %s shows the stack's %q", v.Nick(), got)
		}
		title := p.title.Title() + " / " + p.title.Subtitle()
		if p.title.Title() == "" {
			t.Fatalf("view %s has no title", v.Nick())
		}
		layout(v.Nick())
		p.next.Emit("clicked")
		flush()
		if p.title.Title()+" / "+p.title.Subtitle() == title {
			t.Errorf("› left the %s title at %q", v.Nick(), title)
		}
		layout(v.Nick() + " next")
		p.previous.Emit("clicked")
		p.previous.Emit("clicked")
		flush()
		layout(v.Nick() + " previous")
		p.today.Emit("clicked")
		flush()
		if got := p.title.Title() + " / " + p.title.Subtitle(); got != title {
			t.Errorf("Today in %s shows %q, want %q", v.Nick(), got, title)
		}
	}

	// The view keys from mode_keys.go.
	w.calendarKey(calendar.KeyWeekView)
	flush()
	if p.ctl.View() != calendar.ViewWeek || p.viewSwitch.ActiveName() != "week" {
		t.Fatal("Ctrl+Shift+2 did not show the Week")
	}

	// Show in Calendar's selection and detail: the Day of an occurrence.
	if occ := p.ctl.Occurrences(); len(occ) > 0 {
		o := occ[0]
		key := calendar.OccurrenceKey(o)
		day, _, _ := calendar.Span(o, time.Local)
		p.pendingDetail = key
		p.ctl.Show(day, calendar.ViewDay, key)
		p.openPendingDetail()
		wait(100 * time.Millisecond)
		flush()
		if p.ctl.View() != calendar.ViewDay || p.ctl.Selection() != key || p.pendingDetail != "" {
			t.Errorf("Show in Calendar: view %s, selection %q, pending %q", p.ctl.View().Nick(), p.ctl.Selection(), p.pendingDetail)
		}
		layout("show in calendar")
	}

	// The search: the results page, then Escape's clear.
	p.searchBar.SetSearchMode(true)
	p.searchEntry.SetText("Standup")
	wait(600 * time.Millisecond)
	flush()
	if got := p.stack.VisibleChildName(); got != "search" {
		t.Errorf("a search shows the stack's %q", got)
	}
	layout("search")
	p.ctl.ClearSearch()
	flush()
	if p.searchEntry.Text() != "" || p.stack.VisibleChildName() == "search" {
		t.Error("clearing the search left it shown")
	}

	// The Unanswered filter is listed in the Agenda's page.
	p.ctl.ShowUnanswered()
	flush()
	if got := p.stack.VisibleChildName(); got != "agenda" {
		t.Errorf("the Unanswered filter shows the stack's %q", got)
	}
	p.ctl.ClearFilter()
	flush()

	// Narrower: the header gives way, the sidebar folds over the content,
	// the view menu stands for the view switch; nothing asks for more than
	// the page has.
	for _, width := range []int{1000, 920, 880, 800, 700, 600, 500, 400, 360} {
		w.SetDefaultSize(width, 700)
		wait(300 * time.Millisecond)
		flush()
		layout("width " + strconv.Itoa(width))
		if got, min := p.root.Width(), headerMin(); got > width && width > 360 || min > got {
			t.Errorf("at %d px the page is %d px wide, its header at least %d", width, got, min)
		}
		if width <= 700 && !p.split.Collapsed() {
			t.Errorf("the sidebar did not fold at %d px", width)
		}
		if width <= 700 && (p.viewSwitch.Visible() || p.viewMenu.Visible() != (p.root.Width() > 460)) {
			t.Errorf("at %d px the view switch is visible %v, the menu %v", width, p.viewSwitch.Visible(), p.viewMenu.Visible())
		}
	}
	p.viewMenu.SetSelected(uint(calendar.ViewMonth))
	flush()
	if p.ctl.View() != calendar.ViewMonth || p.viewSwitch.ActiveName() != "month" {
		t.Error("the view menu did not show the Month")
	}

	// An empty calendar shows the empty state with its title.
	src.Replace(calendar.Samples{}, calendardata.PhaseReady)
	flush()
	if p.stack.VisibleChildName() != "empty" || p.empty.Title() == "" {
		t.Errorf("an empty calendar shows %q titled %q", p.stack.VisibleChildName(), p.empty.Title())
	}
	layout("empty")

	// A dialog in the window (not a toplevel) is what devPrintDialog finds.
	w.showAddCalDAV(w, nil)
	flush()
	if d := w.VisibleDialog(); d == nil {
		t.Error("the CalDAV sheet is not the window's visible dialog")
	} else {
		w.devPrintDialog()
		d.ForceClose()
		flush()
	}

	// The mode switch mirror follows the window's mode.
	w.syncModeSwitches(board.ModeMail)
	if p.modeSwitch.ActiveName() != "mail" {
		t.Error("the calendar's mode switch does not follow the window")
	}
}
