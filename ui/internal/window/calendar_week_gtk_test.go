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

// Run in a dedicated test process with a display, for example xvfb-run:
// MALACHI_GTK_SMOKE=1 go test ./internal/window -run
// '^TestCalendarWeekGTKSmoke$' -count=1 -v. Only the invented sample
// calendar (calendardata.NewSampleSource) is used. It lays out the Week
// and the Day (G11) and logs LAYOUT: lines: the grid's height, the
// overlap cluster of Tuesday, the now line's place among the overlay's
// children, the first scroll and the all-day bar of "Team offsite".
func TestCalendarWeekGTKSmoke(t *testing.T) {
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
	p.ctl.SetView(calendar.ViewWeek)
	p.ctl.Today()
	settle := func() {
		for i := 0; i < 60 && (i < 4 || p.ctl.Loading()); i++ {
			wait(50 * time.Millisecond)
		}
		wait(100 * time.Millisecond)
	}
	settle()
	v := p.week
	if p.stack.VisibleChildName() != "week" || len(v.days) != 7 {
		t.Fatalf("the Week shows %q with %d columns", p.stack.VisibleChildName(), len(v.days))
	}
	rect := func(wd gtk.Widgetter) [4]int {
		b, ok := gtk.BaseWidget(wd).ComputeBounds(v.overlay)
		if !ok {
			return [4]int{-1, -1, -1, -1}
		}
		return [4]int{int(b.X()), int(b.Y()), int(b.Width()), int(b.Height())}
	}

	// The view shrinks: the page must reach its narrow breakpoints.
	minWidth := func(name string, wd gtk.Widgetter) {
		m, _, _, _ := gtk.BaseWidget(wd).Measure(gtk.OrientationHorizontal, -1)
		t.Logf("LAYOUT: %s min-width %d", name, m)
		if m > 420 {
			t.Errorf("the %s view asks for %d px, want ≤ 420", name, m)
		}
	}
	minWidth("week", v.root)

	// The grid: 24 × 60 px.
	t.Logf("LAYOUT: grid area=%dx%d overlay=%dx%d minutes=%d header=%dx%d allday=%dx%d",
		v.area.Width(), v.area.Height(), v.overlay.Width(), v.overlay.Height(), calendar.DayMinutes,
		v.header.Width(), v.header.Height(), v.allDay.root.Width(), v.allDay.root.Height())
	if v.area.Height() != calendar.DayMinutes || v.overlay.Height() != calendar.DayMinutes {
		t.Errorf("the grid is %d px high (overlay %d), want %d", v.area.Height(), v.overlay.Height(), calendar.DayMinutes)
	}

	// Tuesday's overlap cluster: Q4 planning 10:00–11:30, the supplier
	// 10:30–11:00, the design review 11:00–12:00 in two columns.
	cluster := map[string]*weekBlock{}
	for _, b := range v.blocks {
		switch b.block.Occurrence.Title {
		case "Q4 planning", "Call with the supplier", "Design review":
			cluster[b.block.Occurrence.Title] = b
			r := rect(b.button)
			t.Logf("LAYOUT: block %q day=%d column=%d/%d rect=%v label=%q classes=%v", b.block.Occurrence.Title, b.day,
				b.block.Column, b.block.Columns, r, b.button.TooltipText(), b.button.CSSClasses())
		}
	}
	t.Logf("LAYOUT: occurrences=%d blocks=%d bars=%d days=%v loading=%v", len(p.ctl.Occurrences()), len(v.blocks), len(v.allDay.bars), v.days, p.ctl.Loading())
	if len(cluster) != 3 {
		t.Fatalf("Tuesday's cluster has %d blocks", len(cluster))
	}
	plan, call, review := rect(cluster["Q4 planning"].button), rect(cluster["Call with the supplier"].button),
		rect(cluster["Design review"].button)
	cw := v.columnWidth(v.overlay.Width())
	if plan[1] != 10*60+1 || plan[3] != 88 || call[1] != 10*60+30+1 || call[3] != 28 || review[1] != 11*60+1 || review[3] != 58 {
		t.Errorf("the cluster's tops and heights: planning %v, call %v, review %v", plan, call, review)
	}
	if call[0] <= plan[0] || call[0] != review[0] || plan[0] < calAxisWidth+int(cw) || plan[2]+call[2] > int(cw) {
		t.Errorf("the cluster's columns: planning %v, call %v, review %v (column %.1f px)", plan, call, review, cw)
	}
	for _, cls := range [][2]string{{"Design review", "cal-tentative"}} {
		if !cluster[cls[0]].button.HasCSSClass(cls[1]) {
			t.Errorf("%s has no %s", cls[0], cls[1])
		}
	}
	for _, b := range v.blocks {
		if b.block.Occurrence.Title == "Budget review" && !b.button.HasCSSClass("cal-unanswered") {
			t.Error("the unanswered Budget review is not dashed")
		}
	}

	// The now line: the overlay's last child, above every block.
	last := v.overlay.LastChild()
	isLast := last != nil && sameObject(gtk.BaseWidget(last), v.nowLine)
	_, minute, today := v.nowAt()
	t.Logf("LAYOUT: now line last=%v visible=%v rect=%v minute=%d blocks=%d", isLast, v.nowLine.Visible(), rect(v.nowLine), minute, len(v.blocks))
	if !isLast || v.nowLine.CanTarget() {
		t.Errorf("the now line is not the overlay's last, untargetable child (last %v)", isLast)
	}
	if today && rect(v.nowLine)[1] != minute-4 {
		t.Errorf("the now line is at y %d, want %d", rect(v.nowLine)[1], minute-4)
	}

	// The first scroll: 08:00 working hours → 07:00 − 10 px.
	adj := v.scroller.VAdjustment()
	want := min(float64(calendar.ScrollMinute(p.ctl.Snapshot().Preferences.WorkingHours)-calScrollAbove), adj.Upper()-adj.PageSize())
	t.Logf("LAYOUT: scroll value=%.0f want=%.0f page=%.0f upper=%.0f", adj.Value(), want, adj.PageSize(), adj.Upper())
	if adj.Value() != want {
		t.Errorf("the first scroll is %.0f, want %.0f", adj.Value(), want)
	}
	// A new viewport height puts it back; the user's scroll ends the rule.
	w.SetDefaultSize(1280, 700)
	wait(300 * time.Millisecond)
	want = min(float64(calendar.ScrollMinute(p.ctl.Snapshot().Preferences.WorkingHours)-calScrollAbove), adj.Upper()-adj.PageSize())
	t.Logf("LAYOUT: scroll after resize value=%.0f want=%.0f page=%.0f", adj.Value(), want, adj.PageSize())
	if adj.Value() != want {
		t.Errorf("after a resize the scroll is %.0f, want %.0f", adj.Value(), want)
	}
	adj.SetValue(100)
	wait(100 * time.Millisecond)
	if v.autoScroll || adj.Value() != 100 {
		t.Errorf("the user's scroll did not hold (auto %v, value %.0f)", v.autoScroll, adj.Value())
	}

	// The all-day bar of Team offsite: Thursday and Friday.
	var offsite *allDayBar
	for _, b := range v.allDay.bars {
		if b.bar.Occurrence.Title == "Team offsite" {
			offsite = b
		}
	}
	if offsite == nil {
		t.Fatal("no all-day bar for Team offsite")
	}
	col, row, span, _ := v.allDay.grid.QueryChild(offsite.button)
	b, _ := offsite.button.ComputeBounds(v.allDay.grid)
	t.Logf("LAYOUT: allday %q column=%d row=%d span=%d rect=%.0f,%.0f %.0fx%.0f lanes=%d", offsite.bar.Occurrence.Title,
		col, row, span, b.X(), b.Y(), b.Width(), b.Height(), v.allDay.lanes)
	if col != 3 || span != 2 || row != offsite.bar.Lane+1 {
		t.Errorf("Team offsite is attached at column %d row %d span %d, want 3, %d, 2", col, row, span, offsite.bar.Lane+1)
	}

	// A click on a block selects it and opens its detail anchored there.
	var opened api.Occurrence
	p.ctl.OnOpenDetail = func(o api.Occurrence) { opened = o }
	cluster["Q4 planning"].button.Emit("clicked")
	wait(50 * time.Millisecond)
	key := cluster["Q4 planning"].key
	if opened.Title != "Q4 planning" || p.ctl.Selection() != key || !cluster["Q4 planning"].button.HasCSSClass("cal-selected") {
		t.Errorf("the click opened %q, selection %q", opened.Title, p.ctl.Selection())
	}
	if a := v.anchorFor(key); a == nil || !sameObject(gtk.BaseWidget(a), cluster["Q4 planning"].button) {
		t.Error("anchorFor is not the clicked block")
	}

	// A double click on empty time: New Event at that half hour.
	var asked calendar.Defaults
	p.ctl.OnNewEvent = func(d calendar.Defaults) { asked = d }
	v.gridPressed(2, float64(calAxisWidth)+cw*2+5, 14*60+40)
	t.Logf("LAYOUT: new event at %v", asked.Start)
	if s := asked.Start.In(time.Local); s.Hour() != 14 || s.Minute() != 30 || !calendar.SameDay(s, v.days[2], time.Local) {
		t.Errorf("a double click at Wednesday 14:40 asks for %v", asked.Start)
	}

	// The Day: one column, with the place in the block.
	p.ctl.Show(v.days[1], calendar.ViewDay, "")
	settle()
	d := p.day
	if p.stack.VisibleChildName() != "day" || len(d.days) != 1 {
		t.Fatalf("the Day shows %q with %d columns", p.stack.VisibleChildName(), len(d.days))
	}
	minWidth("day", d.root)
	for _, b := range d.blocks {
		r, _ := b.button.ComputeBounds(d.overlay)
		t.Logf("LAYOUT: day block %q column=%d/%d rect=%.0f,%.0f %.0fx%.0f", b.block.Occurrence.Title, b.block.Column,
			b.block.Columns, r.X(), r.Y(), r.Width(), r.Height())
	}
	t.Logf("LAYOUT: day scroll value=%.0f", d.scroller.VAdjustment().Value())
}
