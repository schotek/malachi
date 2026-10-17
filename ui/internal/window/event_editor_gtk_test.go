// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/calendardata"
)

// Run with a display (xvfb-run or Broadway): MALACHI_GTK_SMOKE=1 go test
// ./internal/window -run '^TestEventEditorGTKSmoke$' -count=1 -v. Over the
// invented sample calendar only (no daemon, no settings): opens New Event
// and an Edit of two sample events (timed, all-day), checks the registry
// raises an open edit, and logs LAYOUT: lines.
func TestEventEditorGTKSmoke(t *testing.T) {
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
		actions:       make(map[string]*gio.SimpleAction),
		toasts:        adw.NewToastOverlay(),
		dateWeekStart: time.Monday,
	}
	defer w.Destroy()
	src := calendardata.NewSampleSource(time.Now(), time.Local, glibLoop{})
	w.cal.source = src
	wait := func(d time.Duration) {
		end := time.Now().Add(d)
		ctx := glib.MainContextDefault()
		for time.Now().Before(end) {
			ctx.Iteration(false)
			time.Sleep(5 * time.Millisecond)
		}
	}
	alloc := func(name string, wd gtk.Widgetter, root gtk.Widgetter) {
		b, ok := gtk.BaseWidget(wd).ComputeBounds(root)
		if !ok {
			t.Logf("LAYOUT: %s not placed", name)
			return
		}
		t.Logf("LAYOUT: %s x %.0f y %.0f w %.0f h %.0f visible %v", name, b.X(), b.Y(), b.Width(), b.Height(),
			gtk.BaseWidget(wd).IsVisible())
	}
	report := func(label string, ed *eventEditor) {
		wait(400 * time.Millisecond)
		root := ed.win
		t.Logf("LAYOUT: %s window %dx%d title %q primary %q", label, root.Width(), root.Height(),
			ed.titleWidget.Title(), ed.primary.Label())
		alloc(label+" title", ed.title, root)
		for _, r := range []struct {
			n string
			w gtk.Widgetter
		}{{"calendar", ed.calendar}, {"all-day", ed.allDay}, {"start-date", ed.startDate}, {"start-time", ed.startTime},
			{"zone", ed.zone}, {"end-date", ed.endDate}, {"repeat", ed.repeat}, {"location", ed.location},
			{"video", ed.video}, {"attendees", ed.attendees.Entry()}, {"availability", ed.availability},
			{"alert", ed.alert}, {"notes", ed.notes}} {
			alloc(label+" "+r.n, r.w, root)
		}
		lb, _ := gtk.BaseWidget(ed.grid.ChildAt(0, 1)).ComputeBounds(root)
		if lb != nil {
			t.Logf("LAYOUT: %s label width %.0f", label, lb.Width())
		}
		var buttons []string
		for _, b := range []*gtk.Button{ed.deleteBtn, ed.cancel, ed.primary} {
			if !b.IsVisible() {
				continue
			}
			bb, ok := b.ComputeBounds(root)
			if ok {
				buttons = append(buttons, fmt.Sprintf("'%s' x %.0f–%.0f", b.Label(), bb.X(), bb.X()+bb.Width()))
			}
		}
		t.Logf("LAYOUT: %s footer %s note %q", label, strings.Join(buttons, ", "), ed.footer.Label())
		var alerts []string
		for i, m := range ed.alertValues {
			mark := ""
			if uint(i) == ed.alert.Selected() {
				mark = "*"
			}
			alerts = append(alerts, fmt.Sprintf("%d=%s%s", m, ed.ctl.AlertTitle(m), mark))
		}
		t.Logf("LAYOUT: %s allDay %v alerts [%s]", label, ed.ctl.Input().AllDay, strings.Join(alerts, ", "))
	}

	w.openEventEditor(nil, "")
	if len(eventEditors[w]) != 1 {
		t.Fatalf("New Event: %d editors", len(eventEditors[w]))
	}
	report("new", eventEditors[w][0])
	eventEditors[w][0].closeUnasked()
	wait(100 * time.Millisecond)

	var timed, allDay api.Event
	for _, e := range src.Data().Events {
		switch e.EventID {
		case "ev_sample_supplier":
			timed = e
		case "ev_sample_offsite":
			allDay = e
		}
	}
	w.editEvent(timed, api.EditScopeAll, false)
	w.editEvent(timed, api.EditScopeAll, false)
	if n := len(eventEditors[w]); n != 1 {
		t.Fatalf("an open edit was not raised: %d editors", n)
	}
	ed := eventEditors[w][0]
	report("edit-timed", ed)
	ed.closeUnasked()
	w.editEvent(allDay, api.EditScopeAll, false)
	ed = eventEditors[w][0]
	report("edit-allday", ed)
	ed.allDay.SetActive(false)
	report("edit-allday-off", ed)
	ed.closeUnasked()
	wait(100 * time.Millisecond)
	if len(eventEditors[w]) != 0 {
		t.Fatalf("closed editors stay registered: %d", len(eventEditors[w]))
	}
}
