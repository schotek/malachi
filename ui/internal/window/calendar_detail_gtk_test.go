// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
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
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/calendardata"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// Run with a display (xvfb-run, Broadway): MALACHI_GTK_SMOKE=1 go test
// ./internal/window -run '^TestCalendarDetailGTKSmoke$' -count=1 -v. Over
// the invented sample calendar only: it opens the event detail popover
// (G13) for an invitation (Q4 planning: attendees, the "You:" answer,
// video call, notes with a link, Show Invitation in Mail) and for an own
// event (Call with the supplier), checks the rows and the footer and logs
// LAYOUT: lines.
func TestCalendarDetailGTKSmoke(t *testing.T) {
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

	show := func(id api.EventID) *eventDetail {
		t.Helper()
		w.showEventDetail(api.Occurrence{EventID: id}, p.stack)
		wait(400 * time.Millisecond)
		d := w.cal.detail
		if d == nil || d.ctl == nil {
			t.Fatalf("no detail for %s", id)
		}
		if !w.eventDetailShown() || !d.popover.IsVisible() {
			t.Fatalf("the detail of %s is not shown", id)
		}
		if _, ok := d.ctl.Detail(); !ok {
			t.Fatalf("the event %s did not load (error %q)", id, d.ctl.Error())
		}
		return d
	}
	rows := func(d *eventDetail) string {
		var on []string
		for _, r := range []struct {
			name string
			w    *gtk.Box
		}{{"time", d.timeRow}, {"recurrence", d.recurrenceRow}, {"location", d.locationRow}, {"video", d.videoRow},
			{"people", d.peopleRow}, {"alert", d.alertRow}, {"notes", d.notesRow}, {"invitation", d.invitationRow},
			{"footer", d.footer}} {
			if r.w.IsVisible() {
				on = append(on, r.name)
			}
		}
		return strings.Join(on, ",")
	}
	footer := func(d *eventDetail) string {
		var b []string
		for _, x := range []*gtk.Button{d.edit, d.remove} {
			if x.IsVisible() {
				b = append(b, x.Label())
			}
		}
		if d.assistantMenu.IsVisible() {
			b = append(b, "✦")
		}
		return strings.Join(b, " | ")
	}

	// An invitation the user accepted.
	d := show("ev_sample_planning")
	// The content box and its 16 px inset are the artboard's 380 px.
	width := func(d *eventDetail) int { return d.box.Width() + d.box.MarginStart() + d.box.MarginEnd() }
	if got := width(d); got != 380 {
		t.Errorf("the detail is %d px wide, want 380", got)
	}
	for _, r := range []string{"time", "location", "video", "people", "notes", "invitation", "footer"} {
		if !strings.Contains(rows(d), r) {
			t.Errorf("invitation: no %s row (%s)", r, rows(d))
		}
	}
	if !d.response.IsVisible() || d.response.Label() != calendar.MyResponseText(api.ResponseAccepted, i18n.Tr) {
		t.Errorf("invitation: the answer shows %q (visible %v)", d.response.Label(), d.response.IsVisible())
	}
	if d.edit.IsVisible() || !d.remove.IsVisible() || d.remove.Label() != calendar.RemoveFromCalendar(i18n.Tr) {
		t.Errorf("invitation footer: %s", footer(d))
	}
	if n := len(d.noteLinks); n != 1 {
		t.Errorf("notes have %d link tags, want 1", n)
	}
	n := 0
	for c := d.attendees.FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
		n++
	}
	if n != 4 {
		t.Errorf("%d attendee rows, want 4", n)
	}
	t.Logf("LAYOUT: invitation width=%d box=%dx%d popover=%v title=%q time=%q/%q people=%q attendees=%d you=%q rows=%s footer=%s",
		width(d), d.box.Width(), d.box.Height(), d.popover.IsVisible(), d.title.Label(), d.time1.Label(), d.time2.Label(),
		d.people.Label(), n, d.response.Label(), rows(d), footer(d))

	// An own event: showing it closes the invitation's.
	first := d.ctl
	d = show("ev_sample_supplier")
	if d.ctl == first {
		t.Error("the second show kept the first controller")
	}
	if !d.edit.IsVisible() || !d.remove.IsVisible() || d.remove.Label() != calendar.RemoveTitle(api.Event{RemoveIsDelete: true}, i18n.Tr) {
		t.Errorf("own event footer: %s", footer(d))
	}
	if d.peopleRow.IsVisible() || d.invitationRow.IsVisible() {
		t.Errorf("own event shows people or the invitation: %s", rows(d))
	}
	t.Logf("LAYOUT: own width=%d box=%dx%d title=%q time=%q/%q rows=%s footer=%s",
		width(d), d.box.Width(), d.box.Height(), d.title.Label(), d.time1.Label(), d.time2.Label(), rows(d), footer(d))

	// A recurring one, with its alert.
	d = show("ev_sample_standup")
	if !d.recurrenceRow.IsVisible() || !d.alertRow.IsVisible() {
		t.Errorf("standup: no recurrence or alert row (%s)", rows(d))
	}
	t.Logf("LAYOUT: series recurrence=%q alert=%q rows=%s footer=%s", d.recurrence.Label(), d.alert.Label(), rows(d), footer(d))

	w.closeEventDetail()
	wait(200 * time.Millisecond)
	if w.eventDetailShown() || d.ctl != nil {
		t.Error("the detail stays after closeEventDetail")
	}
	t.Logf("LAYOUT: closed shown=%v", w.eventDetailShown())
}
