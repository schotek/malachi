// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/calendarctl"
	"github.com/schotek/malachi/ui/internal/calendardata"
)

// Run with a display: MALACHI_GTK_SMOKE=1 go test ./internal/window -run
// '^TestCalendarSidebarGTKSmoke$' -count=1 -v. Over the invented sample
// calendar only: the sidebar (mini month, Invitations ▸ Unanswered, the
// accounts and their calendars), the Agenda with its paging, the
// Unanswered list and the search results, with LAYOUT: lines.
func TestCalendarSidebarGTKSmoke(t *testing.T) {
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
	w.SetContent(p.root)
	w.SetDefaultSize(1280, 900)
	w.Present()
	p.start()
	wait := func(d time.Duration) {
		end := time.Now().Add(d)
		ctx := glib.MainContextDefault()
		for time.Now().Before(end) {
			ctx.Iteration(false)
			time.Sleep(5 * time.Millisecond)
		}
	}
	wait(400 * time.Millisecond)
	s := p.sidebar
	alloc := func(wd gtk.Widgetter) string {
		r, ok := gtk.BaseWidget(wd).ComputeBounds(p.root)
		if !ok {
			return "-"
		}
		return fmtRect(r.X(), r.Y(), r.Width(), r.Height())
	}

	// The mini month.
	t.Logf("LAYOUT: mini month %s title=%q grid=%s", alloc(s.miniGrid), s.miniTitle.Text(), alloc(s.miniGrid))
	today, banded := -1, 0
	for i, b := range s.miniDays {
		if s.miniNums[i].HasCSSClass("cal-mini-today") {
			today = i
		}
		if b.HasCSSClass("cal-mini-band") {
			banded++
		}
	}
	if today < 0 {
		t.Error("the mini month shows no today")
	}
	if banded != 7 && p.ctl.View() == calendar.ViewWeek {
		t.Errorf("the week view bands %d days of the mini month", banded)
	}
	t.Logf("LAYOUT: mini day[0] %s day[41] %s today=%d banded=%d heads=%q..%q", alloc(s.miniDays[0]), alloc(s.miniDays[41]),
		today, banded, s.miniHeads[0].Text(), s.miniHeads[6].Text())
	title := s.miniTitle.Text()
	s.miniNext.Emit("clicked")
	wait(50 * time.Millisecond)
	if s.miniTitle.Text() == title {
		t.Error("› did not step the mini month")
	}
	s.miniPrev.Emit("clicked")
	wait(50 * time.Millisecond)

	// The rows after it.
	n := 0
	for c := s.list.FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
		row := c.(*gtk.ListBoxRow)
		kind := "row"
		switch {
		case row.HasCSSClass("cal-sidebar-heading"):
			kind = "heading"
		case row.HasCSSClass("cal-unanswered-row"):
			kind = "unanswered"
		case row.HasCSSClass("cal-calendar-row"):
			kind = "calendar"
		case row.HasCSSClass("cal-state-row"):
			kind = "state"
		}
		t.Logf("LAYOUT: sidebar %s %s", kind, alloc(row))
		n++
	}
	sb := p.ctl.Sidebar()
	want := 2
	for _, a := range sb.Accounts {
		want += 1 + len(a.Calendars)
		if a.StateLine != "" {
			want++
		}
	}
	if n != want {
		t.Errorf("sidebar has %d rows, want %d", n, want)
	}
	t.Logf("LAYOUT: sidebar accounts=%d unanswered=%d add=%s", len(sb.Accounts), sb.Unanswered, alloc(s.addButton))

	// A calendar's check button hides it.
	if len(sb.Accounts) > 0 && len(sb.Accounts[0].Calendars) > 0 {
		id := sb.Accounts[0].Calendars[0].ID
		for c := s.list.FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
			if ch, ok := c.(*gtk.ListBoxRow).Child().(*gtk.CheckButton); ok {
				ch.SetActive(false)
				break
			}
		}
		wait(100 * time.Millisecond)
		if k, ok := p.ctl.Snapshot().Calendar(id); !ok || k.Visible {
			t.Error("unchecking the first calendar left it visible")
		}
	}

	// The Agenda and its paging.
	p.ctl.SetView(calendar.ViewAgenda)
	wait(300 * time.Millisecond)
	a := p.agenda.agenda
	heads, items := 0, 0
	for _, e := range a.entries {
		if e.Heading != "" {
			heads++
		} else {
			items++
		}
	}
	t.Logf("LAYOUT: agenda %s days=%d rows=%d empty=%q", alloc(a.root), heads, items, a.empty.Text())
	for i, r := range a.rows {
		if i >= 6 {
			break
		}
		t.Logf("LAYOUT: agenda row %d %s heading=%q time=%q", i, alloc(r), a.entries[i].Heading, a.entries[i].Time)
	}
	if items > 0 {
		var key string
		for _, e := range a.entries {
			if e.Heading == "" {
				key = calendar.OccurrenceKey(e.Item.Occurrence)
				break
			}
		}
		p.ctl.Select(key)
		wait(50 * time.Millisecond)
		if r := a.list.SelectedRow(); r == nil || p.agenda.anchorFor(key) == nil {
			t.Error("the selection is not the agenda's row")
		}
	}
	pages := p.ctl.AgendaPages()
	adj := a.scroll.VAdjustment()
	adj.SetValue(adj.Upper())
	wait(300 * time.Millisecond)
	t.Logf("LAYOUT: agenda pages %d → %d upper=%.0f", pages, p.ctl.AgendaPages(), adj.Upper())

	// The Unanswered list.
	s.unanswered.Activate()
	wait(100 * time.Millisecond)
	if p.ctl.Filter() != calendarctl.FilterUnanswered || s.list.SelectedRow() == nil {
		t.Error("the Unanswered row did not show the filter")
	}
	t.Logf("LAYOUT: unanswered heading=%q rows=%d stack=%q", a.heading.Text(), len(a.rows), p.stack.VisibleChildName())
	p.ctl.ClearFilter()
	wait(50 * time.Millisecond)

	// The search results.
	p.searchBar.SetSearchMode(true)
	p.searchEntry.SetText("Standup")
	wait(600 * time.Millisecond)
	r := p.agenda.results
	t.Logf("LAYOUT: results %s heading=%q rows=%d empty=%q", alloc(r.root), r.heading.Text(), len(r.rows), r.empty.Text())
	for i, e := range r.entries {
		if i >= 3 {
			break
		}
		t.Logf("LAYOUT: result %d time=%q place=%q", i, e.Time, e.Place)
	}
	p.searchEntry.SetText("zzzzqqq")
	wait(600 * time.Millisecond)
	if r.empty.Text() == "" || !r.empty.Visible() {
		t.Error("a search without results shows no empty text")
	}
	t.Logf("LAYOUT: no results empty=%q", r.empty.Text())
}

// fmtRect writes a rectangle as "x,y wxh".
func fmtRect(x, y, w, h float32) string {
	return fmt.Sprintf("%.0f,%.0f %.0fx%.0f", x, y, w, h)
}
