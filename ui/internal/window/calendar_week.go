// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"math"
	"reflect"
	"strconv"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/cairo"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/calendar"
)

// The Week and Day views (docs/calendar.md §5.3, §6.5 G11) in the page's
// week_bin and day_bin; the port of macos CalendarTimeGridView.swift,
// CalendarDayHeaderView.swift and EventBlockView.swift (the all-day row is
// calendar_allday.go). Top to bottom: the day header row (weekday and
// number, today's number in a filled circle, weekends grey), the all-day
// row, and a vertical ScrolledWindow over a Gtk.Overlay of the whole day,
// 24 × 60 px. The overlay's main child is a DrawingArea (weekend shading,
// today's column tint, the hour and half-hour lines, the day separators);
// over it, in this order, the hour axis's labels, the blocks (buttons:
// title, time, recurring symbol, attendee count, in the Day view also the
// place and the video call) and last the now line (can-target false,
// moved each minute). Every overlay child is placed in get-child-position
// from calendar.LayoutDay, so it is exact on every resize. Everything
// placed comes from ui/internal/calendar; the page makes two of these
// (day false for the Week's, true for the Day's), calls update after
// every change of the controller (each builds only while its view is the
// controller's) and asks anchorFor for a popover's anchor.

// The grid's metrics (macOS CalendarMetrics).
const (
	// calHourHeight is an hour of the grid; a minute is a pixel.
	calHourHeight = 60
	// calAxisWidth is the hour axis left of the day columns.
	calAxisWidth = 60
	// calDayHeaderHeight is the day header row.
	calDayHeaderHeight = 40
	// calGridHeight is the grid of the whole day.
	calGridHeight = calendar.DayMinutes * calHourHeight / 60
	// calScrollAbove is how far above the working hours' first line the
	// first scroll stops, so that its label shows whole.
	calScrollAbove = 10
	// calLabelAbove is how far above its line an hour's label starts
	// (labels are 2 × that high, centred on the line).
	calLabelAbove = 10
	// calTitleLine and calSmallLine are a block's title line and its
	// other lines, for what fits a block's height.
	calTitleLine = 16
	calSmallLine = 14
)

// calendarWeek is one time grid's widgets.
type calendarWeek struct {
	page *calendarPage
	// day: the Day view's grid (one column), else the Week's.
	day bool

	root     *gtk.Box
	header   *gtk.Grid
	allDay   *calendarAllDay
	scroller *gtk.ScrolledWindow
	overlay  *gtk.Overlay
	area     *gtk.DrawingArea
	nowLine  *gtk.DrawingArea
	hours    []*gtk.Label

	// headerDays are the header's children as last built.
	headerDays []gtk.Widgetter
	// days are the columns' days (midnights) as last built.
	days []time.Time
	// blocks are the blocks as last built, day by day.
	blocks []*weekBlock
	// place is where each overlay child goes (get-child-position), by
	// its native pointer.
	place map[uintptr]func(width int) gdk.Rectangle
	// input is what the views were last built from; selection the key
	// last marked.
	input     *weekInput
	selection string

	// The first scroll (§6.5): ScrollMinute px − calScrollAbove, applied
	// again while the viewport's height changes until the user scrolls
	// (autoScroll false), and armed again for other days.
	scrollMinute   int
	autoScroll     bool
	scrollPage     float64
	applyingScroll bool

	// now is the instant the now line and today's tint were drawn for,
	// moved by a one-minute timer.
	now time.Time
}

// weekInput is what a grid is built from: it is rebuilt only when this
// changes, so a refresh of the same data keeps the widgets (and focus).
type weekInput struct {
	days         []time.Time
	occurrences  []api.Occurrence
	showDeclined bool
	colors       map[api.CalendarID]int
	today        time.Time
}

// weekBlock is one block of the grid.
type weekBlock struct {
	button *gtk.Button
	block  calendar.Block
	// day is the block's column.
	day int
	key string
}

// newCalendarWeek builds a grid into page.weekBin (day false) or
// page.dayBin (day true).
func newCalendarWeek(page *calendarPage, day bool) *calendarWeek {
	v := &calendarWeek{page: page, day: day, place: map[uintptr]func(int) gdk.Rectangle{},
		scrollMinute: calendar.ScrollMinute(api.WorkingHours{}), autoScroll: true, now: time.Now()}
	v.root = gtk.NewBox(gtk.OrientationVertical, 0)
	v.root.AddCSSClass(map[bool]string{false: "cal-week", true: "cal-day"}[day])

	head := gtk.NewBox(gtk.OrientationHorizontal, 0)
	head.AddCSSClass("cal-week-header")
	head.SetSizeRequest(-1, calDayHeaderHeight)
	spacer := gtk.NewBox(gtk.OrientationHorizontal, 0)
	spacer.SetSizeRequest(calAxisWidth, -1)
	head.Append(spacer)
	v.header = gtk.NewGrid()
	v.header.SetColumnHomogeneous(true)
	v.header.SetHExpand(true)
	head.Append(v.header)
	v.root.Append(head)

	v.allDay = newCalendarAllDay(v)
	v.root.Append(v.allDay.root)

	v.area = gtk.NewDrawingArea()
	v.area.SetContentHeight(calGridHeight)
	v.area.SetHExpand(true)
	v.area.SetDrawFunc(v.draw)
	click := gtk.NewGestureClick()
	click.ConnectPressed(v.gridPressed)
	v.area.AddController(click)

	v.overlay = gtk.NewOverlay()
	v.overlay.SetChild(v.area)
	v.overlay.ConnectGetChildPosition(v.childPosition)
	for h := 1; h < 24; h++ {
		l := gtk.NewLabel("")
		l.SetUseMarkup(false)
		l.AddCSSClass("cal-hour-label")
		l.SetXAlign(1)
		l.SetCanTarget(false)
		v.hours = append(v.hours, l)
		y := h*calHourHeight - calLabelAbove
		v.addOverlay(l, func(int) gdk.Rectangle { return gdk.NewRectangle(0, y, calAxisWidth-8, 2*calLabelAbove) })
	}
	v.nowLine = gtk.NewDrawingArea()
	v.nowLine.SetCanTarget(false)
	v.nowLine.SetCanFocus(false)
	v.nowLine.AddCSSClass("cal-now-line")
	v.nowLine.SetDrawFunc(drawNowLine)
	v.addOverlay(v.nowLine, v.nowPosition)

	v.scroller = gtk.NewScrolledWindow()
	v.scroller.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	v.scroller.SetVExpand(true)
	v.scroller.SetChild(v.overlay)
	adj := v.scroller.VAdjustment()
	adj.ConnectChanged(v.applyScroll)
	adj.ConnectValueChanged(v.scrolled)
	v.scroller.ConnectMap(v.applyScroll)
	v.root.Append(v.scroller)

	bin := page.weekBin
	if day {
		bin = page.dayBin
	}
	if bin != nil {
		bin.SetChild(v.root)
	}
	// The now line moves each minute (the page lives with its window).
	glib.TimeoutAdd(60*1000, func() bool {
		v.tick(time.Now())
		return true
	})
	return v
}

// addOverlay adds an overlay child placed by at (the overlay's width in).
func (v *calendarWeek) addOverlay(w gtk.Widgetter, at func(width int) gdk.Rectangle) {
	v.place[coreglib.InternObject(w).Native()] = at
	v.overlay.AddOverlay(w)
}

// removeOverlay removes an overlay child added by addOverlay.
func (v *calendarWeek) removeOverlay(w gtk.Widgetter) {
	delete(v.place, coreglib.InternObject(w).Native())
	v.overlay.RemoveOverlay(w)
}

// childPosition is the overlay's get-child-position.
func (v *calendarWeek) childPosition(w gtk.Widgetter) (*gdk.Rectangle, bool) {
	at, ok := v.place[coreglib.InternObject(w).Native()]
	if !ok {
		return nil, false
	}
	r := at(v.overlay.Width())
	return &r, true
}

// columnWidth is a day column's width for the overlay's width.
func (v *calendarWeek) columnWidth(width int) float64 {
	return float64(width-calAxisWidth) / float64(max(1, len(v.days)))
}

// blockRect is where b goes for the overlay's width (macOS
// CalendarTimeGridView.layout): its share of the column with a pixel of
// air around.
func (v *calendarWeek) blockRect(b *weekBlock, width int) gdk.Rectangle {
	cw := v.columnWidth(width)
	inner := cw - 4
	w := inner / float64(max(1, b.block.Columns))
	x := float64(calAxisWidth) + float64(b.day)*cw + 1 + float64(b.block.Column)*w
	top, bottom := b.block.Top*calHourHeight/60, b.block.Bottom*calHourHeight/60
	return gdk.NewRectangle(int(math.Round(x)), top+1, max(4, int(math.Round(w))-2), max(4, bottom-top-2))
}

// nowPosition is where the now line goes: over today's column at the
// minute, its dot left of the column.
func (v *calendarWeek) nowPosition(width int) gdk.Rectangle {
	day, minute, ok := v.nowAt()
	if !ok {
		return gdk.NewRectangle(0, 0, 1, 1)
	}
	cw := v.columnWidth(width)
	x := float64(calAxisWidth) + float64(day)*cw - 4
	return gdk.NewRectangle(int(math.Round(x)), minute*calHourHeight/60-4, int(math.Round(cw))+4, 8)
}

// nowAt is calendar.NowLine over the columns' days.
func (v *calendarWeek) nowAt() (day, minute int, ok bool) {
	if len(v.days) == 0 {
		return 0, 0, false
	}
	loc := v.page.w.calendarEnv().Loc
	r := calendar.Range{From: v.days[0], To: calendar.AddDays(v.days[len(v.days)-1], 1)}
	return calendar.NowLine(v.now, r, loc)
}

// drawNowLine draws the red line with its dot at the left.
func drawNowLine(_ *gtk.DrawingArea, cr *cairo.Context, width, height int) {
	cr.SetSourceRGBA(0.878, 0.106, 0.141, 1) // GNOME red 3
	mid := float64(height) / 2
	cr.Rectangle(4, mid-0.75, float64(width-4), 1.5)
	cr.Fill()
	cr.Arc(4, mid, 4, 0, 2*math.Pi)
	cr.Fill()
}

// draw is the grid's DrawingArea: weekend shading, today's tint, the hour
// and half-hour lines and the day separators.
func (v *calendarWeek) draw(area *gtk.DrawingArea, cr *cairo.Context, width, height int) {
	fg := area.Color()
	set := func(a float64) {
		cr.SetSourceRGBA(float64(fg.Red()), float64(fg.Green()), float64(fg.Blue()), a)
	}
	cw := v.columnWidth(width)
	loc := v.page.w.calendarEnv().Loc
	today := calendar.StartOfDay(v.now, loc)
	for i, d := range v.days {
		x := float64(calAxisWidth) + float64(i)*cw
		if weekViewWeekend(d) {
			set(0.03)
			cr.Rectangle(x, 0, cw, float64(height))
			cr.Fill()
		}
		if d.Equal(today) {
			accent := adw.StyleManagerGetDefault().AccentColorRGBA()
			cr.SetSourceRGBA(float64(accent.Red()), float64(accent.Green()), float64(accent.Blue()), 0.06)
			cr.Rectangle(x, 0, cw, float64(height))
			cr.Fill()
		}
	}
	set(0.15)
	for h := 1; h < 24; h++ {
		cr.Rectangle(calAxisWidth-4, float64(h*calHourHeight), float64(width-calAxisWidth+4), 1)
	}
	cr.Fill()
	set(0.06)
	for h := 0; h < 24; h++ {
		cr.Rectangle(calAxisWidth, float64(h*calHourHeight+calHourHeight/2), float64(width-calAxisWidth), 1)
	}
	cr.Fill()
	set(0.15)
	for i := 0; i < max(1, len(v.days)); i++ {
		cr.Rectangle(float64(calAxisWidth)+math.Round(float64(i)*cw), 0, 1, float64(height))
	}
	cr.Fill()
}

// gridPressed is a click on the grid's empty time: a double click asks
// for New Event at the half hour of the click (calendar.OriginSlot rounds
// down).
func (v *calendarWeek) gridPressed(n int, x, y float64) {
	if n != 2 || len(v.days) == 0 || x < calAxisWidth {
		return
	}
	i := min(len(v.days)-1, int((x-calAxisWidth)/v.columnWidth(v.area.Width())))
	minute := max(0, min(calendar.DayMinutes-1, int(y*60/calHourHeight)))
	d := v.days[i]
	at := time.Date(d.Year(), d.Month(), d.Day(), minute/60, minute%60, 0, 0, d.Location())
	v.page.ctl.NewEventAt(at, calendar.OriginSlot)
}

// shown: the controller shows this grid's view.
func (v *calendarWeek) shown() bool {
	want := calendar.ViewWeek
	if v.day {
		want = calendar.ViewDay
	}
	return v.page.ctl.View() == want
}

// update shows the controller's state.
func (v *calendarWeek) update() {
	if !v.shown() {
		return
	}
	c := v.page.ctl
	env := v.page.w.calendarEnv()
	v.now = time.Now()
	in := &weekInput{days: c.Range().Days(), occurrences: c.Occurrences(), showDeclined: c.ShowDeclined(),
		colors: v.page.colorIndex, today: calendar.StartOfDay(v.now, env.Loc)}
	v.scrollMinute = calendar.ScrollMinute(c.Snapshot().Preferences.WorkingHours)
	if v.input == nil || !reflect.DeepEqual(*v.input, *in) {
		if v.input == nil || !sameDays(v.input.days, in.days) {
			v.autoScroll = true
		}
		v.input = in
		v.build(env, c.Selection())
		v.applyScroll()
	}
	v.markSelection(c.Selection())
}

// sameDays reports the same columns.
func sameDays(a, b []time.Time) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].Equal(b[i]) {
			return false
		}
	}
	return true
}

// build rebuilds the header, the all-day row and the blocks.
func (v *calendarWeek) build(env calendar.Env, selection string) {
	in := v.input
	v.days = in.days
	for _, w := range v.headerDays {
		v.header.Remove(w)
	}
	v.headerDays = nil
	for i, d := range v.days {
		col := v.dayHeader(d, d.Equal(in.today), env)
		v.header.Attach(col, i, 0, 1, 1)
		v.headerDays = append(v.headerDays, col)
	}
	for i, l := range v.hours {
		var d time.Time
		if len(v.days) > 0 {
			d = v.days[0]
		} else {
			d = in.today
		}
		l.SetText(calendar.TimeText(time.Date(d.Year(), d.Month(), d.Day(), i+1, 0, 0, 0, env.Loc), env))
	}
	v.allDay.set(v.days, in.occurrences, in.showDeclined, selection)

	for _, b := range v.blocks {
		v.removeOverlay(b.button)
	}
	v.blocks = nil
	for i, d := range v.days {
		for _, blk := range calendar.LayoutDay(in.occurrences, d, env.Loc, in.showDeclined) {
			b := &weekBlock{block: blk, day: i, key: calendar.OccurrenceKey(blk.Occurrence)}
			b.button = v.makeBlock(blk, env)
			v.blocks = append(v.blocks, b)
			v.addOverlay(b.button, func(width int) gdk.Rectangle { return v.blockRect(b, width) })
		}
	}
	// The now line stays the last child: above every block.
	v.removeOverlay(v.nowLine)
	v.addOverlay(v.nowLine, v.nowPosition)
	v.selection = ""
	v.tick(v.now)
}

// dayHeader is a column of the day header: the weekday's abbreviation and
// the day's number, today's in a filled circle, weekends grey.
func (v *calendarWeek) dayHeader(d time.Time, today bool, env calendar.Env) *gtk.Box {
	box := gtk.NewBox(gtk.OrientationVertical, 0)
	box.SetVAlign(gtk.AlignCenter)
	wd := gtk.NewLabel("")
	wd.SetUseMarkup(false)
	wd.SetText(calendar.ShortWeekdayText(d, env))
	wd.AddCSSClass("cal-day-weekday")
	wd.SetEllipsize(pango.EllipsizeEnd)
	wd.SetMaxWidthChars(1)
	wd.SetHExpand(true)
	num := gtk.NewLabel("")
	num.SetUseMarkup(false)
	num.SetText(calendar.DayText(d, env))
	num.AddCSSClass("cal-day-number")
	num.SetHAlign(gtk.AlignCenter)
	if weekViewWeekend(d) {
		wd.AddCSSClass("cal-weekend")
		num.AddCSSClass("cal-weekend")
	}
	if today {
		num.AddCSSClass("cal-today-number")
	}
	box.Append(wd)
	box.Append(num)
	return box
}

// makeBlock is a block's button: the calendar's fill with the 3 px bar
// (none for an unanswered invitation, whose outline is dashed and which
// has a "?" badge), the title, the time, in the Day view the place, then
// the recurring symbol, in the Day view the video call, and the attendee
// count; what does not fit the block's height is left out.
func (v *calendarWeek) makeBlock(b calendar.Block, env calendar.Env) *gtk.Button {
	o := b.Occurrence
	btn := gtk.NewButton()
	btn.AddCSSClass("cal-block")
	btn.AddCSSClass("cal-fill")
	btn.AddCSSClass(v.page.colorClass(o.CalendarID))
	for _, c := range weekStateClasses(b.State) {
		btn.AddCSSClass(c)
	}
	btn.SetOverflow(gtk.OverflowHidden)
	row := gtk.NewBox(gtk.OrientationHorizontal, 4)
	if b.State != calendar.BlockUnanswered {
		bar := gtk.NewBox(gtk.OrientationVertical, 0)
		bar.AddCSSClass("cal-bar")
		row.Append(bar)
	}
	content := gtk.NewBox(gtk.OrientationVertical, 0)
	content.SetHExpand(true)
	content.SetMarginTop(1)
	content.SetMarginEnd(3)
	row.Append(content)
	btn.SetChild(row)

	room := (b.Bottom-b.Top)*calHourHeight/60 - 2 - 2
	line := func(s, class string) *gtk.Label {
		l := gtk.NewLabel("")
		l.SetUseMarkup(false)
		l.SetText(s)
		l.SetXAlign(0)
		l.SetEllipsize(pango.EllipsizeEnd)
		l.SetMaxWidthChars(1)
		l.SetHExpand(true)
		l.AddCSSClass(class)
		return l
	}
	titleRow := gtk.NewBox(gtk.OrientationHorizontal, 2)
	title := line(calendar.BlockTitle(o, env.Tr), "cal-block-title")
	title.SetHExpand(true)
	titleRow.Append(title)
	if b.State == calendar.BlockUnanswered {
		// A symbol, not a word: no msgid.
		q := line("?", "cal-badge")
		q.SetEllipsize(pango.EllipsizeNone)
		q.SetHExpand(false)
		q.SetXAlign(0.5)
		q.SetVAlign(gtk.AlignStart)
		titleRow.Append(q)
	}
	content.Append(titleRow)
	used := calTitleLine
	fits := func() bool {
		if used+calSmallLine > room {
			return false
		}
		used += calSmallLine
		return true
	}
	if fits() {
		content.Append(line(calendar.TimeRangeText(b.Start, b.End, env), "cal-block-time"))
	}
	if place := calendar.BlockPlace(o); v.day && place != "" && fits() {
		content.Append(line(place, "cal-block-meta"))
	}
	symbols := gtk.NewBox(gtk.OrientationHorizontal, 3)
	symbols.SetOverflow(gtk.OverflowHidden)
	symbol := func(name string) {
		img := gtk.NewImageFromIconName(name)
		img.SetPixelSize(10)
		img.AddCSSClass("cal-block-meta")
		symbols.Append(img)
	}
	if o.Recurring {
		symbol("media-playlist-repeat-symbolic")
	}
	if v.day && o.VideoCall {
		symbol("camera-video-symbolic")
	}
	if o.AttendeeCount > 0 {
		symbol("system-users-symbolic")
		symbols.Append(line(strconv.Itoa(o.AttendeeCount), "cal-block-meta"))
	}
	if symbols.FirstChild() != nil && fits() {
		content.Append(symbols)
	}
	label := calendar.BlockLabel(o, env)
	btn.SetTooltipText(label)
	weekAccessibleLabel(btn, label)
	btn.ConnectClicked(func() { v.open(o) })
	return btn
}

// open is a click on a block or a bar: it is selected and its detail
// opens anchored to it; an invitation not in a calendar (no event) is
// only selected.
func (v *calendarWeek) open(o api.Occurrence) {
	if o.EventID == "" {
		v.page.ctl.Select(calendar.OccurrenceKey(o))
		return
	}
	v.page.ctl.OpenDetail(o)
}

// markSelection marks the selection's block and bar and gives the keyboard to
// it when the selection moved while the keyboard was in the view (or
// nowhere).
func (v *calendarWeek) markSelection(key string) {
	if key == v.selection {
		return
	}
	v.selection = key
	var target gtk.Widgetter
	for _, b := range v.blocks {
		if b.key == key {
			b.button.AddCSSClass("cal-selected")
			target = b.button
		} else {
			b.button.RemoveCSSClass("cal-selected")
		}
	}
	v.allDay.setSelection(key)
	if target == nil {
		if bar := v.allDay.barFor(key); bar != nil {
			target = bar.button
		}
	}
	if target == nil || key == "" || !v.root.Mapped() {
		return
	}
	focus := v.page.w.Focus()
	if focus == nil || gtk.BaseWidget(focus).IsAncestor(v.root) {
		gtk.BaseWidget(target).GrabFocus()
	}
}

// anchorFor is the block showing the occurrence key
// (calendar.OccurrenceKey), nil when none does.
func (v *calendarWeek) anchorFor(key string) gtk.Widgetter {
	for _, b := range v.blocks {
		if b.key == key {
			return b.button
		}
	}
	if bar := v.allDay.barFor(key); bar != nil {
		return bar.button
	}
	return nil
}

// tick moves the now line to now; a new day redraws today's tint and
// header.
func (v *calendarWeek) tick(now time.Time) {
	loc := v.page.w.calendarEnv().Loc
	newDay := !calendar.StartOfDay(now, loc).Equal(calendar.StartOfDay(v.now, loc))
	v.now = now
	_, _, ok := v.nowAt()
	v.nowLine.SetVisible(ok)
	v.overlay.QueueAllocate()
	v.area.QueueDraw()
	if newDay && v.input != nil && v.shown() {
		v.update()
	}
}

// applyScroll puts the working hours' start (ScrollMinute px −
// calScrollAbove) at the top of the visible grid while autoScroll holds;
// the adjustment's changes (a new viewport height, the first allocation)
// call it again.
func (v *calendarWeek) applyScroll() {
	adj := v.scroller.VAdjustment()
	page := adj.PageSize()
	if !v.autoScroll || page <= 0 || adj.Upper() < calGridHeight {
		return
	}
	target := float64(max(0, v.scrollMinute*calHourHeight/60-calScrollAbove))
	target = min(target, max(0, adj.Upper()-page))
	v.applyingScroll = true
	adj.SetValue(target)
	v.applyingScroll = false
	v.scrollPage = page
}

// scrolled is the adjustment's value-changed: a move of the user's (not
// ours, not a clamp after a new viewport height) ends the first-scroll
// rule until other days show.
func (v *calendarWeek) scrolled() {
	if v.applyingScroll || !v.autoScroll {
		return
	}
	adj := v.scroller.VAdjustment()
	if adj.PageSize() != v.scrollPage {
		// A resize's clamp: put it back once the size settled.
		glib.IdleAdd(v.applyScroll)
		return
	}
	v.autoScroll = false
}

// weekAccessibleLabel sets a widget's accessible label.
func weekAccessibleLabel(w *gtk.Button, label string) {
	w.UpdateProperty([]gtk.AccessibleProperty{gtk.AccessiblePropertyLabel},
		[]coreglib.Value{*coreglib.NewValue(label)})
}
