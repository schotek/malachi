// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"log/slog"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/internal/calendardata"
)

// Run with a display: MALACHI_GTK_SMOKE=1 go test ./internal/window -run
// '^TestTodayCalendarStripGTKSmoke$' -count=1 -v. The Board → Today strip
// over the invented sample calendar (no daemon, no account): it logs a
// LAYOUT: line per row, then hides once no account has a calendar.
func TestTodayCalendarStripGTKSmoke(t *testing.T) {
	if os.Getenv("MALACHI_GTK_SMOKE") != "1" {
		t.Skip("set MALACHI_GTK_SMOKE=1 with a GTK display to run the widget smoke test")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	adw.Init()
	w := &Window{log: slog.Default()}
	main := gtk.NewBox(gtk.OrientationVertical, 8)
	greeting := gtk.NewLabel("greeting")
	main.Append(greeting)
	main.Append(gtk.NewLabel("tiles"))
	src := calendardata.NewSampleSource(time.Now(), time.Local, glibLoop{})
	s := newTodayCalendarStrip(w, nil, main, greeting)
	win := gtk.NewWindow()
	win.SetChild(main)
	win.SetDefaultSize(720, 600)
	win.Present()
	defer win.Destroy()
	wait := func(d time.Duration) {
		end := time.Now().Add(d)
		ctx := glib.MainContextDefault()
		for time.Now().Before(end) {
			ctx.Iteration(false)
			time.Sleep(5 * time.Millisecond)
		}
	}
	s.attach(src)
	wait(400 * time.Millisecond)
	if !s.shown || !s.root.Visible() {
		t.Fatal("the strip is hidden over a calendar")
	}
	if gtk.BaseWidget(s.root).Parent() == nil || gtk.BaseWidget(s.root).PrevSibling() == nil {
		t.Fatal("the strip is not after the greeting")
	}
	t.Logf("LAYOUT: today-strip %dx%d rows %d heading %q", s.root.Width(), s.root.Height(), len(s.rows), s.heading.Text())
	for i, r := range s.rows {
		row := s.list.RowAtIndex(i)
		t.Logf("LAYOUT: today-row %d %dx%d time %q title %q took-place %v respond %v place %q",
			i, row.Width(), row.Height(), r.Time, r.Title, r.TookPlace, r.Respond, r.Place)
	}
	if len(s.rows) == 0 && s.list.RowAtIndex(0) == nil {
		t.Error("no rows and no Nothing Today line")
	}

	src.Update(func(sn *calendardata.Snapshot) { sn.Accounts = nil })
	wait(200 * time.Millisecond)
	if s.shown || s.root.Visible() {
		t.Error("the strip shows without a calendar account")
	}
}
