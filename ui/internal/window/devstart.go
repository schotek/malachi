// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/calendar"
)

// A development aid, not a feature (the macOS client's MALACHI_START,
// MainWindowController+DevStart.swift): MALACHI_GTK_START is a script of
// steps separated by ";" that the main window runs once it shows, with
// MALACHI_GTK_START_INTERVAL seconds between them (default 3), so that a
// layout can be checked under Broadway without clicking. After each step
// it prints "MALACHI_GTK_START step <n> <name>" and LAYOUT: lines to
// stderr. Without the variable nothing runs. Only for an isolated
// instance (MALACHI_DATA_DIR, MALACHI_SOCKET, the samples), never over a
// real store.
//
// Steps: mail, board, calendar, calendar:day|week|month|agenda,
// event-popover (the detail of the shown range's first occurrence),
// event-editor (New Event with today's defaults), event-editor:edit (Edit
// of the first occurrence), caldav-dialog, prefs-calendar (Preferences on
// the Calendar page), prefs-integrations (Preferences on the Integrations
// page), size=WxH, quit.

const (
	devStartEnv         = "MALACHI_GTK_START"
	devStartIntervalEnv = "MALACHI_GTK_START_INTERVAL"
	devStartInterval    = 3 * time.Second
)

// devStepKind is what a step does.
type devStepKind int

const (
	devMail devStepKind = iota
	devBoard
	devCalendar
	devCalendarView
	devEventPopover
	devEventEditor
	devEventEdit
	devCalDAV
	devPrefsCalendar
	devPrefsIntegrations
	devSize
	devQuit
)

// devStep is one parsed step; Name is how it was written.
type devStep struct {
	Kind          devStepKind
	Name          string
	View          calendar.View
	Width, Height int
}

// parseDevStart parses a MALACHI_GTK_START script; empty steps are
// skipped, an unknown one is an error naming it.
func parseDevStart(script string) ([]devStep, error) {
	var out []devStep
	for _, raw := range strings.Split(script, ";") {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		st := devStep{Name: name}
		switch {
		case name == "mail":
			st.Kind = devMail
		case name == "board":
			st.Kind = devBoard
		case name == "calendar":
			st.Kind = devCalendar
		case strings.HasPrefix(name, "calendar:"):
			v, ok := calendar.ParseView(strings.TrimPrefix(name, "calendar:"))
			if !ok {
				return nil, fmt.Errorf("unknown calendar view in step %q", name)
			}
			st.Kind, st.View = devCalendarView, v
		case name == "event-popover":
			st.Kind = devEventPopover
		case name == "event-editor":
			st.Kind = devEventEditor
		case name == "event-editor:edit":
			st.Kind = devEventEdit
		case name == "caldav-dialog":
			st.Kind = devCalDAV
		case name == "prefs-calendar":
			st.Kind = devPrefsCalendar
		case name == "prefs-integrations":
			st.Kind = devPrefsIntegrations
		case strings.HasPrefix(name, "size="):
			wh := strings.SplitN(strings.TrimPrefix(name, "size="), "x", 2)
			if len(wh) != 2 {
				return nil, fmt.Errorf("step %q: want size=WxH", name)
			}
			w, err1 := strconv.Atoi(wh[0])
			h, err2 := strconv.Atoi(wh[1])
			if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
				return nil, fmt.Errorf("step %q: want size=WxH", name)
			}
			st.Kind, st.Width, st.Height = devSize, w, h
		case name == "quit":
			st.Kind = devQuit
		default:
			return nil, fmt.Errorf("unknown step %q", name)
		}
		out = append(out, st)
	}
	return out, nil
}

// parseDevInterval is MALACHI_GTK_START_INTERVAL in seconds (fractions
// allowed); the default when empty or not a positive number.
func parseDevInterval(s string) time.Duration {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || f <= 0 {
		return devStartInterval
	}
	return time.Duration(f * float64(time.Second))
}

// developmentPrint writes one line of the script's output to stderr.
func developmentPrint(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

// startDevelopmentScript runs MALACHI_GTK_START, when set, once the window
// first shows. Called once from New.
func (w *Window) startDevelopmentScript() {
	script, ok := os.LookupEnv(devStartEnv)
	if !ok {
		return
	}
	steps, err := parseDevStart(script)
	if err != nil {
		developmentPrint("MALACHI_GTK_START: %v", err)
		return
	}
	interval := parseDevInterval(os.Getenv(devStartIntervalEnv))
	started := false
	w.ConnectMap(func() {
		if started {
			return
		}
		started = true
		var next func(i int)
		next = func(i int) {
			if i >= len(steps) {
				return
			}
			glib.TimeoutAdd(uint(interval/time.Millisecond), func() bool {
				w.runDevStep(i+1, steps[i])
				next(i + 1)
				return false
			})
		}
		next(0)
	})
}

// runDevStep runs step n and prints what it shows.
func (w *Window) runDevStep(n int, st devStep) {
	developmentPrint("MALACHI_GTK_START step %d %s", n, st.Name)
	switch st.Kind {
	case devMail:
		w.setMode(board.ModeMail)
	case devBoard:
		w.setMode(board.ModeBoard)
	case devCalendar:
		w.setMode(board.ModeCalendar)
	case devCalendarView:
		w.setMode(board.ModeCalendar)
		if p := w.cal.page; p != nil {
			p.ctl.SetView(st.View)
		}
	case devEventPopover:
		if o, p, ok := w.devFirstOccurrence(); ok {
			anchor := p.anchorFor(calendar.OccurrenceKey(o))
			p.ctl.Select(calendar.OccurrenceKey(o))
			w.showEventDetail(o, anchor)
			devPrintWidget("anchor", anchor)
		}
	case devEventEditor:
		w.setMode(board.ModeCalendar)
		if p := w.cal.page; p != nil {
			d := p.ctl.NewEventDefaults(time.Now(), calendar.OriginNow)
			w.openEventEditor(&d, "")
		}
	case devEventEdit:
		if o, _, ok := w.devFirstOccurrence(); ok {
			scope := api.EditScopeAll
			if o.RecurrenceID != "" {
				scope = api.EditScopeThis
			}
			w.calendarSource().Event(o.EventID, o.RecurrenceID, false, func(e *api.Event, err *api.Error) {
				if e == nil {
					developmentPrint("LAYOUT: event-editor:edit no event (%v)", err)
					return
				}
				w.editEvent(*e, scope, false)
				glib.IdleAdd(w.devPrintToplevels)
			})
		}
	case devCalDAV:
		w.showAddCalDAV(w, nil)
	case devPrefsCalendar:
		d := NewPreferences(w.settings, w.client, w.assist, w.log, w.calendarSource)
		d.SetVisiblePageName("calendar")
		d.Present(w)
	case devPrefsIntegrations:
		d := NewPreferences(w.settings, w.client, w.assist, w.log, w.calendarSource)
		d.SetVisiblePageName("integrations")
		d.Present(w)
	case devSize:
		w.SetDefaultSize(st.Width, st.Height)
	case devQuit:
		if app := w.Application(); app != nil {
			app.Quit()
		}
		return
	}
	// After the step has been laid out.
	glib.IdleAdd(func() {
		if p := w.cal.page; p != nil && w.mode == board.ModeCalendar {
			developmentPrint("%s", p.layoutLine(st.Name))
			developmentPrint("%s", p.minLine())
			developmentPrint("%s", w.modeMinLine())
		}
		w.devPrintToplevels()
	})
}

// devFirstOccurrence is the first occurrence of the shown range, the
// Calendar shown.
func (w *Window) devFirstOccurrence() (api.Occurrence, *calendarPage, bool) {
	w.setMode(board.ModeCalendar)
	p := w.cal.page
	if p == nil {
		return api.Occurrence{}, nil, false
	}
	occ := p.ctl.ShownOccurrences()
	if len(occ) == 0 {
		developmentPrint("LAYOUT: no occurrence in the shown range")
		return api.Occurrence{}, p, false
	}
	return occ[0], p, true
}

// layoutLine is the page's LAYOUT: line (as calendar_gtk_smoke_test.go
// logs it): the allocations, the header's minimum and texts.
func (p *calendarPage) layoutLine(step string) string {
	m, _, _, _ := p.header.Measure(gtk.OrientationHorizontal, -1)
	return fmt.Sprintf("LAYOUT: %s page=%dx%d sidebar=%dx%d content=%dx%d header=%d(min %d) stack=%q view=%q title=%q subtitle=%q status=%q note=%q",
		step, p.root.Width(), p.root.Height(), p.sidebarBin.Width(), p.sidebarBin.Height(),
		p.stack.Width(), p.stack.Height(), p.header.Width(), m, p.stack.VisibleChildName(), p.viewSwitch.ActiveName(),
		p.title.Title(), p.title.Subtitle(), p.statusLabel.Label(), p.statusNote.Label())
}

// devPrintWidget prints a widget's bounds in its window.
func devPrintWidget(name string, wd gtk.Widgetter) {
	if wd == nil {
		developmentPrint("LAYOUT: %s none", name)
		return
	}
	b := gtk.BaseWidget(wd)
	developmentPrint("LAYOUT: %s %s %dx%d", name, b.Name(), b.Width(), b.Height())
}

// devPrintToplevels prints every window and popover with its size (the
// main window, the editor, a dialog's window, Preferences) and the
// popovers open in the main window (the detail).
func (w *Window) devPrintToplevels() {
	list := gtk.WindowGetToplevels()
	for i := uint(0); i < list.NItems(); i++ {
		obj := list.Item(i)
		if obj == nil {
			continue
		}
		if tw, ok := obj.Cast().(gtk.Widgetter); ok {
			b := gtk.BaseWidget(tw)
			developmentPrint("LAYOUT: toplevel %s visible=%v %dx%d", b.Name(), b.IsVisible(), b.Width(), b.Height())
		}
	}
	w.devPrintDialog()
}

// devPrintDialog prints the dialog presented in the main window, if any:
// libadwaita 1.7's Adw.Dialog and Adw.PreferencesDialog live inside the
// window, not as toplevels. Its size, content size, title, and for
// Preferences the visible page.
func (w *Window) devPrintDialog() {
	d := w.VisibleDialog()
	if d == nil {
		return
	}
	page := ""
	if pd, ok := d.Object.Cast().(*adw.PreferencesDialog); ok {
		page = pd.VisiblePageName()
	}
	developmentPrint("LAYOUT: dialog %s %dx%d content=%dx%d title=%q page=%q", d.Name(), d.Width(), d.Height(),
		d.ContentWidth(), d.ContentHeight(), d.Title(), page)
}

// devMinWidth is wd's minimum width.
func devMinWidth(wd gtk.Widgetter) int {
	if wd == nil {
		return -1
	}
	m, _, _, _ := gtk.BaseWidget(wd).Measure(gtk.OrientationHorizontal, -1)
	return m
}

// minLine is the page's LAYOUT: min line: the minimum widths of the page,
// its split, sidebar, header bar, content stack and each stack child.
func (p *calendarPage) minLine() string {
	return fmt.Sprintf("LAYOUT: min page=%d split=%d sidebar=%d header=%d stack=%d week=%d day=%d month=%d agenda=%d search=%d empty=%d status=%d banner=%d",
		devMinWidth(p.root), devMinWidth(p.split), devMinWidth(p.sidebarBin), devMinWidth(p.header), devMinWidth(p.stack),
		devMinWidth(p.weekBin), devMinWidth(p.dayBin), devMinWidth(p.monthBin), devMinWidth(p.agendaBin), devMinWidth(p.searchBin),
		devMinWidth(p.empty), devMinWidth(p.statusButton), devMinWidth(p.banner))
}

// modeMinLine is the minimum widths of the window's mode stack and each
// of its pages (mail, board, calendar).
func (w *Window) modeMinLine() string {
	if w.modeStack == nil {
		return "LAYOUT: min modes none"
	}
	out := fmt.Sprintf("LAYOUT: min modes stack=%d", devMinWidth(w.modeStack))
	for _, m := range board.ModesWithCalendar {
		if c := w.modeStack.ChildByName(m.Nick()); c != nil {
			out += fmt.Sprintf(" %s=%d", m.Nick(), devMinWidth(c))
		}
	}
	return out
}
