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
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/calendardata"
)

// Run with a display (xvfb-run or Broadway): MALACHI_GTK_SMOKE=1 go test
// ./internal/window -run '^TestCalendarMonthGTKSmoke$' -count=1 -v. Over the
// invented sample calendar with three occurrences added (an all-day one
// across a week boundary and a day with more pills than room), it shows
// the Month and logs LAYOUT: lines with the bars' columns and widths, the
// rows' capacities before and after a resize and the "+N more" counts.
func TestCalendarMonthGTKSmoke(t *testing.T) {
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
	loc := time.Local
	now := time.Now()
	src := calendardata.NewSampleSource(now, loc, glibLoop{})

	// The sample week's Monday; an all-day event from a Saturday over the
	// next week's start, in a row pair of this month's grid.
	monday := calendar.StartOfWeek(now, time.Monday, loc)
	g := calendar.MonthCells(now, time.Monday, now, loc)
	row := 0
	for i, c := range g.Cells {
		if c.Today {
			row = i / 7
		}
	}
	saturday := calendar.AddDays(monday, 5)
	if row == 5 {
		saturday = calendar.AddDays(monday, -2)
	}
	d := src.Data()
	across := api.Occurrence{EventID: "ev_test_across", CalendarID: d.Occurrences[0].CalendarID, AccountID: d.Occurrences[0].AccountID,
		Title: "Conference across weeks", AllDay: true, StartDate: calendar.DateOf(saturday, loc),
		EndDate: calendar.DateOf(calendar.AddDays(saturday, 4), loc), Status: api.EventStatusConfirmed, ShowAs: api.ShowAsBusy}
	d.Occurrences = append(d.Occurrences, across)
	// Eight half-hour meetings on the Wednesday: more than a cell holds.
	wednesday := calendar.AddDays(monday, 2)
	for i := range 8 {
		s := time.Date(wednesday.Year(), wednesday.Month(), wednesday.Day(), 7+i, 0, 0, 0, loc).UTC()
		e := s.Add(30 * time.Minute)
		d.Occurrences = append(d.Occurrences, api.Occurrence{EventID: api.EventID("ev_test_busy_" + string(rune('a'+i))),
			CalendarID: d.Occurrences[0].CalendarID, AccountID: d.Occurrences[0].AccountID, Title: "Busy " + string(rune('A'+i)),
			Start: &s, End: &e, Status: api.EventStatusConfirmed, ShowAs: api.ShowAsBusy})
	}
	src.Replace(d, calendardata.PhaseReady)

	w.cal.source = src
	w.ensureCalendar()
	p := w.cal.page
	w.SetContent(p.root)
	w.SetDefaultSize(1280, 900)
	w.Present()
	p.start()
	wait := func(dur time.Duration) {
		end := time.Now().Add(dur)
		ctx := glib.MainContextDefault()
		for time.Now().Before(end) {
			ctx.Iteration(false)
			time.Sleep(5 * time.Millisecond)
		}
	}
	// Until the controller has loaded (the first range, then the Month's).
	loaded := func() {
		for i := 0; i < 200 && (i < 20 || p.ctl.Loading()); i++ {
			wait(10 * time.Millisecond)
		}
		wait(200 * time.Millisecond)
	}
	loaded()
	p.ctl.Show(now, calendar.ViewMonth, "")
	loaded()
	m := p.month
	if p.stack.VisibleChildName() != "month" || m.input == nil {
		t.Fatalf("the Month is not shown (stack %q)", p.stack.VisibleChildName())
	}

	type found struct {
		row int
		bar monthBar
	}
	bars := func(title string) []found {
		var out []found
		for _, r := range m.rows {
			for _, b := range r.bars {
				if b.span.Title == title {
					out = append(out, found{r.index, b})
				}
			}
		}
		return out
	}
	logRows := func(step string) []int {
		caps := make([]int, 6)
		for i, r := range m.rows {
			caps[i] = r.capacity
			t.Logf("LAYOUT: %s row %d week %s height %d lanes %d capacity %d more %v pills %d", step, i, r.week.Label(),
				r.height, r.lanes, r.capacity, r.more, len(r.pills))
		}
		return caps
	}
	colWidth := float64(m.rows[0].overlay.Width()) / 7
	t.Logf("LAYOUT: month %dx%d row overlay %dx%d column %.1f", m.root.Width(), m.root.Height(),
		m.rows[0].overlay.Width(), m.rows[0].overlay.Height(), colWidth)

	t.Logf("LAYOUT: occurrences %d (controller %d, source %d)", len(m.occurrences), len(p.ctl.Occurrences()), len(src.Data().Occurrences))
	minW, _, _, _ := m.root.Measure(gtk.OrientationHorizontal, -1)
	t.Logf("LAYOUT: month min-width %d", minW)
	if minW > 420 {
		t.Errorf("the Month asks for at least %d px, want ≤ 420", minW)
	}
	offsite := bars("Team offsite")
	if len(offsite) != 1 {
		t.Fatalf("Team offsite has %d bars, want 1", len(offsite))
	}
	o := offsite[0]
	t.Logf("LAYOUT: bar %q row %d cols %d–%d lane %d width %d", o.bar.span.Title, o.row, o.bar.span.StartCol,
		o.bar.span.EndCol, o.bar.span.Lane, o.bar.button.Width())
	if o.bar.span.EndCol-o.bar.span.StartCol != 1 {
		t.Errorf("Team offsite spans columns %d–%d, want two", o.bar.span.StartCol, o.bar.span.EndCol)
	}
	if w := float64(o.bar.button.Width()); w < 2*colWidth-12 || w > 2*colWidth {
		t.Errorf("Team offsite's bar is %.0f px wide, two columns are %.0f", w, 2*colWidth)
	}

	acrossBars := bars("Conference across weeks")
	for _, b := range acrossBars {
		t.Logf("LAYOUT: bar %q row %d cols %d–%d lane %d before %v after %v width %d", b.bar.span.Title, b.row,
			b.bar.span.StartCol, b.bar.span.EndCol, b.bar.span.Lane, b.bar.span.ContinuesBefore, b.bar.span.ContinuesAfter,
			b.bar.button.Width())
	}
	if len(acrossBars) != 2 || acrossBars[0].row+1 != acrossBars[1].row ||
		acrossBars[0].bar.span.EndCol != 6 || !acrossBars[0].bar.span.ContinuesAfter ||
		acrossBars[1].bar.span.StartCol != 0 || !acrossBars[1].bar.span.ContinuesBefore ||
		!acrossBars[0].bar.button.HasCSSClass("cal-continues-after") ||
		!acrossBars[1].bar.button.HasCSSClass("cal-continues-before") {
		t.Errorf("the bar across the week boundary is not two continuing bars: %+v", acrossBars)
	}

	before := logRows("1280x900")
	wedRow, wedCol := -1, -1
	for i, c := range m.grid.Cells {
		if c.Day.Equal(wednesday) {
			wedRow, wedCol = i/7, i%7
		}
	}
	if wedRow < 0 {
		t.Fatal("the Wednesday is not in the grid")
	}
	r := m.rows[wedRow]
	all, _ := calendar.CellItems(m.occurrences, wednesday, 1<<30, loc, m.showDeclined)
	want := len(all) - max(0, r.capacity-1)
	t.Logf("LAYOUT: Wednesday row %d col %d pills %d capacity %d more %d", wedRow, wedCol, len(all), r.capacity, r.more[wedCol])
	if r.capacity <= 0 || r.more[wedCol] != want {
		t.Errorf("Wednesday shows +%d more with capacity %d, want +%d", r.more[wedCol], r.capacity, want)
	}
	if a := m.anchorFor(acrossBars[0].bar.span.Key); a == nil {
		t.Error("no anchor for the bar")
	}

	// Smaller: the rows hold fewer pills; the capacity follows the height.
	w.SetDefaultSize(1280, 560)
	wait(500 * time.Millisecond)
	after := logRows("1280x560")
	if after[wedRow] >= before[wedRow] {
		t.Errorf("the capacity of row %d stayed %d after the resize (was %d)", wedRow, after[wedRow], before[wedRow])
	}
	t.Logf("LAYOUT: Wednesday after resize capacity %d more %d", r.capacity, r.more[wedCol])

	// "+N more" opens a popover with the day's whole list.
	if mb := r.moreButton[wedCol]; mb != nil {
		mb.Emit("clicked")
		wait(200 * time.Millisecond)
		if m.more == nil || !m.more.Visible() {
			t.Error("+N more opened no popover")
		} else {
			n := 0
			for ch := gtk.BaseWidget(m.more.Child()).FirstChild(); ch != nil; ch = gtk.BaseWidget(ch).NextSibling() {
				n++
			}
			t.Logf("LAYOUT: popover %dx%d lines %d (heading + %d pills)", m.more.Width(), m.more.Height(), n, len(all))
			if n != len(all)+1 {
				t.Errorf("the popover lists %d lines, want the heading and %d pills", n, len(all))
			}
			m.closeMore()
		}
	} else {
		t.Error("Wednesday has no +N more after the resize")
	}

	// Double click on empty space would open the editor (OnNewEvent); the
	// defaults land at 9:00 of the day.
	def := p.ctl.NewEventDefaults(wednesday, calendar.OriginMonthCell)
	if def.Start.Hour() != 9 || !calendar.StartOfDay(def.Start, loc).Equal(wednesday) {
		t.Errorf("a month cell's New Event starts %v", def.Start)
	}
}
