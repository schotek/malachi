// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"reflect"
	"strconv"
	"time"

	"github.com/diamondburned/gotk4/pkg/cairo"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/calendar"
)

// The Month view (docs/calendar.md §5.4, §6.5 G12) in the page's
// month_bin, a port of CalendarMonthView.swift and MonthCellView.swift:
// the weekday header, then six week rows, each the week number at the
// left and an Overlay over a DrawingArea. The DrawingArea paints the
// cells (weekend and other months shaded, the grid lines) and is the
// row's resize probe; over it a Grid of seven homogeneous columns holds
// the day numbers (row 0), the spanning bars of calendar.MonthLanes at
// (StartCol, lane+1, span, 1) and per day a box of the pills
// calendar.CellItems chose for the row's capacity. The capacity comes from
// calendar.CellCapacity with the probe's height, never from measuring the
// pills; a height that changes the capacity refills the pills in an idle
// (never during the allocation). A double click on the row's empty space
// is New Event that day at 9:00 (OriginMonthCell).

// The month's metrics, in px (CalendarMetrics of the macOS client): the
// day number's line, one pill or bar lane (17 px and 1 px between), the
// week number column.
const (
	monthNumberLine = 22
	monthPillHeight = 17
	monthPillLine   = monthPillHeight + 1
	monthWeekWidth  = 28
	// monthPopoverWidth is the "+N more" popover's list.
	monthPopoverWidth = 240
)

// calendarMonth is the month's widgets.
type calendarMonth struct {
	page *calendarPage
	root *gtk.Box
	// headers are the seven weekday labels.
	headers [7]*gtk.Label
	rows    [6]*monthRow

	// What the widgets show (rebuild when it changes).
	input        *monthInput
	grid         calendar.MonthGrid
	occurrences  []api.Occurrence
	showDeclined bool
	selection    string
	env          calendar.Env

	// refillQueued: an idle refills the rows whose capacity changed.
	refillQueued bool
	// more is the open "+N more" popover; moreKey the occurrence opened
	// from it and moreButton its button (anchorFor's answer for that key).
	more       *gtk.Popover
	moreKey    string
	moreButton *gtk.Button
}

// monthInput is what the widgets were built from.
type monthInput struct {
	month        time.Time
	today        time.Time
	weekStart    time.Weekday
	occurrences  []api.Occurrence
	showDeclined bool
	colors       map[api.CalendarID]int
}

// monthRow is one week row.
type monthRow struct {
	v       *calendarMonth
	index   int
	week    *gtk.Label
	overlay *gtk.Overlay
	base    *gtk.DrawingArea
	grid    *gtk.Grid

	// height is the probe's height, 0 before the first allocation.
	height int
	// lanes are the bars' lanes (calendar.LaneCount); capacity the
	// pills a cell holds (-1: not filled yet).
	lanes    int
	capacity int
	bars     []monthBar
	pillBox  [7]*gtk.Box
	pills    []monthItem
	// more is each day's "+N more" count (0: everything fits), moreButton
	// its button.
	more       [7]int
	moreButton [7]*gtk.Button
}

// monthBar is a spanning bar.
type monthBar struct {
	span   calendar.MonthSpan
	button *gtk.Button
}

// monthItem is a pill.
type monthItem struct {
	key    string
	button *gtk.Button
}

// newCalendarMonth builds the month into page.monthBin.
func newCalendarMonth(page *calendarPage) *calendarMonth {
	v := &calendarMonth{page: page}
	v.root = gtk.NewBox(gtk.OrientationVertical, 0)
	v.root.SetHExpand(true)
	v.root.SetVExpand(true)

	header := gtk.NewBox(gtk.OrientationHorizontal, 0)
	corner := gtk.NewBox(gtk.OrientationHorizontal, 0)
	corner.SetSizeRequest(monthWeekWidth, -1)
	header.Append(corner)
	days := gtk.NewBox(gtk.OrientationHorizontal, 0)
	days.SetHomogeneous(true)
	days.SetHExpand(true)
	for i := range v.headers {
		l := gtk.NewLabel("")
		l.SetUseMarkup(false)
		l.AddCSSClass("cal-month-weekday")
		monthShrink(l)
		v.headers[i] = l
		days.Append(l)
	}
	header.Append(days)
	v.root.Append(header)
	v.root.Append(gtk.NewSeparator(gtk.OrientationHorizontal))

	rows := gtk.NewBox(gtk.OrientationVertical, 0)
	rows.SetHomogeneous(true)
	rows.SetVExpand(true)
	for i := range v.rows {
		r := v.newRow(i)
		v.rows[i] = r
		box := gtk.NewBox(gtk.OrientationHorizontal, 0)
		box.Append(r.week)
		box.Append(r.overlay)
		rows.Append(box)
	}
	v.root.Append(rows)
	if page.monthBin != nil {
		page.monthBin.SetChild(v.root)
	}
	return v
}

// newRow builds week row i: its week number and its overlay over the
// probe.
func (v *calendarMonth) newRow(i int) *monthRow {
	r := &monthRow{v: v, index: i, capacity: -1}
	r.week = gtk.NewLabel("")
	r.week.SetUseMarkup(false)
	r.week.SetSizeRequest(monthWeekWidth, -1)
	r.week.SetVAlign(gtk.AlignStart)
	r.week.AddCSSClass("cal-month-week")

	r.base = gtk.NewDrawingArea()
	r.base.SetHExpand(true)
	r.base.SetVExpand(true)
	r.base.SetDrawFunc(r.draw)
	r.base.ConnectResize(func(_, height int) {
		if height != r.height {
			r.height = height
			v.queueRefill()
		}
	})
	r.overlay = gtk.NewOverlay()
	r.overlay.SetHExpand(true)
	r.overlay.SetVExpand(true)
	r.overlay.SetOverflow(gtk.OverflowHidden)
	r.overlay.SetChild(r.base)

	// A double click on the row's empty space (the pills and bars are
	// buttons and keep their clicks): New Event that day.
	click := gtk.NewGestureClick()
	click.ConnectPressed(func(n int, x, _ float64) {
		if n != 2 {
			return
		}
		w := r.overlay.Width()
		if w <= 0 {
			return
		}
		col := min(6, max(0, int(x*7/float64(w))))
		v.page.ctl.NewEventAt(v.grid.Cells[r.index*7+col].Day, calendar.OriginMonthCell)
	})
	r.overlay.AddController(click)
	return r
}

// update shows the controller's state; only while the Month shows.
func (v *calendarMonth) update() {
	c := v.page.ctl
	if c.View() != calendar.ViewMonth {
		return
	}
	env := v.page.w.calendarEnv()
	loc := env.Loc
	now := time.Now()
	in := &monthInput{
		month: calendar.MonthCells(c.Anchor(), c.WeekStart(), now, loc).Month, today: calendar.StartOfDay(now, loc),
		weekStart: c.WeekStart(), occurrences: c.Occurrences(), showDeclined: c.ShowDeclined(), colors: v.page.colorIndex,
	}
	v.selection = c.Selection()
	if v.input != nil && reflect.DeepEqual(in, v.input) {
		v.applySelection()
		return
	}
	v.input = in
	v.env = env
	v.grid = calendar.MonthCells(c.Anchor(), in.weekStart, now, loc)
	v.occurrences = in.occurrences
	v.showDeclined = in.showDeclined
	v.rebuild()
}

// rebuild makes every row's day numbers and bars again from the input;
// the pills follow in the next refill.
func (v *calendarMonth) rebuild() {
	v.closeMore()
	tr := v.env.Tr
	for i, h := range calendar.WeekdayHeaders(v.input.weekStart, v.env) {
		v.headers[i].SetLabel(h)
	}
	for _, r := range v.rows {
		n := v.grid.WeekNumbers[r.index]
		r.week.SetLabel(strconv.Itoa(n))
		r.week.SetTooltipText(calendar.WeekLabel(n, tr))
		r.week.UpdateProperty([]gtk.AccessibleProperty{gtk.AccessiblePropertyLabel},
			[]coreglib.Value{*coreglib.NewValue(calendar.WeekLabel(n, tr))})
		r.build()
	}
	v.queueRefill()
}

// build replaces the row's grid: the day numbers, the bars and the empty
// pill boxes.
func (r *monthRow) build() {
	v := r.v
	if r.grid != nil {
		r.overlay.RemoveOverlay(r.grid)
	}
	g := gtk.NewGrid()
	g.SetColumnHomogeneous(true)
	g.SetHExpand(true)
	g.SetVExpand(true)
	r.grid = g

	var days [7]time.Time
	for col := range 7 {
		cell := v.grid.Cells[r.index*7+col]
		days[col] = cell.Day
		num := gtk.NewLabel(calendar.DayText(cell.Day, v.env))
		num.SetUseMarkup(false)
		num.AddCSSClass("cal-month-day")
		if cell.Today {
			num.AddCSSClass("cal-month-today")
		} else if !cell.InMonth {
			num.AddCSSClass("cal-month-out")
		}
		num.SetHAlign(gtk.AlignEnd)
		num.SetVAlign(gtk.AlignCenter)
		num.SetMarginEnd(4)
		num.SetSizeRequest(-1, monthNumberLine)
		num.UpdateProperty([]gtk.AccessibleProperty{gtk.AccessiblePropertyLabel},
			[]coreglib.Value{*coreglib.NewValue(calendar.LongDateText(cell.Day, v.env))})
		g.Attach(num, col, 0, 1, 1)
	}

	spans := calendar.MonthLanes(v.occurrences, days, v.env.Loc, v.showDeclined)
	r.lanes = calendar.LaneCount(spans)
	r.bars = r.bars[:0]
	for _, s := range spans {
		b := v.barButton(s)
		g.Attach(b, s.StartCol, s.Lane+1, s.EndCol-s.StartCol+1, 1)
		r.bars = append(r.bars, monthBar{span: s, button: b})
	}
	for col := range 7 {
		box := gtk.NewBox(gtk.OrientationVertical, 1)
		box.SetVExpand(true)
		box.SetVAlign(gtk.AlignFill)
		r.pillBox[col] = box
		g.Attach(box, col, r.lanes+1, 1, 1)
	}
	r.pills = nil
	r.more = [7]int{}
	r.moreButton = [7]*gtk.Button{}
	r.capacity = -1
	r.overlay.AddOverlay(g)
	r.overlay.SetClipOverlay(g, true)
	r.base.QueueDraw()
}

// queueRefill refills, in an idle, the rows whose capacity changed.
func (v *calendarMonth) queueRefill() {
	if v.refillQueued {
		return
	}
	v.refillQueued = true
	glib.IdleAdd(func() {
		v.refillQueued = false
		v.refill()
	})
}

// refill gives each row the bars that fit its height and, when its
// capacity (calendar.CellCapacity) changed, its pills again.
func (v *calendarMonth) refill() {
	if v.input == nil {
		return
	}
	for _, r := range v.rows {
		for _, b := range r.bars {
			// A lane the row has no room for is not drawn.
			b.button.SetVisible(r.height <= 0 || monthNumberLine+(b.span.Lane+1)*monthPillLine <= r.height)
		}
		capacity := calendar.CellCapacity(float64(r.height-monthNumberLine), monthPillLine, r.lanes, monthPillLine)
		if capacity != r.capacity {
			r.fill(capacity)
		}
	}
}

// fill puts each day's pills for capacity into the row, with "+N more"
// for the rest.
func (r *monthRow) fill(capacity int) {
	v := r.v
	if v.more != nil && v.moreButton != nil && v.moreButton.IsAncestor(r.overlay) {
		v.closeMore()
	}
	r.capacity = capacity
	r.pills = nil
	r.moreButton = [7]*gtk.Button{}
	for col, box := range r.pillBox {
		for ch := box.FirstChild(); ch != nil; ch = box.FirstChild() {
			box.Remove(ch)
		}
		day := v.grid.Cells[r.index*7+col].Day
		shown, more := calendar.CellItems(v.occurrences, day, capacity, v.env.Loc, v.showDeclined)
		r.more[col] = more
		for _, p := range shown {
			b := v.pillButton(p, nil)
			box.Append(b)
			r.pills = append(r.pills, monthItem{key: calendar.OccurrenceKey(p.Occurrence), button: b})
		}
		if more > 0 {
			mb := gtk.NewButtonWithLabel(calendar.More(more, v.env.Tr))
			mb.SetHasFrame(false)
			mb.AddCSSClass("cal-month-more")
			mb.SetSizeRequest(-1, monthPillHeight)
			mb.SetMarginStart(3)
			if l, ok := mb.Child().(*gtk.Label); ok {
				l.SetUseMarkup(false)
				monthShrink(l)
				l.SetXAlign(0)
			}
			mb.SetHAlign(gtk.AlignFill)
			mb.ConnectClicked(func() { v.showMore(mb, day) })
			box.Append(mb)
			r.moreButton[col] = mb
		}
	}
	v.applySelection()
}

// titleOf is an occurrence's title for a pill or a bar: cleaned, Untitled
// when empty.
func (v *calendarMonth) titleOf(o api.Occurrence) string {
	if t := board.CleanLine(o.Title, api.MaxEventTitleBytes); t != "" {
		return t
	}
	return calendar.Untitled(v.env.Tr)
}

// monthShrink lets a label go below its text's width (ellipsized), so
// that the seven columns never ask for more than a few px each.
func monthShrink(l *gtk.Label) {
	l.SetEllipsize(pango.EllipsizeEnd)
	l.SetWidthChars(1)
	l.SetMaxWidthChars(1)
	l.SetHExpand(true)
}

// monthTitleLabel is a pill's or a bar's title: one line, clipped,
// struck through when declined or cancelled.
func monthTitleLabel(text string, state calendar.BlockState) *gtk.Label {
	l := gtk.NewLabel("")
	l.SetUseMarkup(false)
	l.SetLabel(text)
	l.SetXAlign(0)
	l.SetHExpand(true)
	l.SetEllipsize(pango.EllipsizeEnd)
	l.SetSingleLineMode(true)
	l.SetWidthChars(1)
	l.SetMaxWidthChars(1)
	if state == calendar.BlockDeclined || state == calendar.BlockCancelled {
		attrs := pango.NewAttrList()
		attrs.Insert(pango.NewAttrStrikethrough(true))
		l.SetAttributes(attrs)
	}
	return l
}

// monthStateClasses are the CSS classes of an occurrence's state on its
// pill or bar.
func monthStateClasses(w *gtk.Button, state calendar.BlockState) {
	switch state {
	case calendar.BlockUnanswered:
		w.AddCSSClass("cal-unanswered")
	case calendar.BlockTentative:
		w.AddCSSClass("cal-tentative")
	case calendar.BlockCancelled:
		w.AddCSSClass("cal-month-cancelled")
	}
}

// barButton is a spanning bar: the calendar's opaque fill with the colour
// bar at the left where it starts, the title once, clipped; the ends
// where it continues square.
func (v *calendarMonth) barButton(s calendar.MonthSpan) *gtk.Button {
	b := gtk.NewButton()
	b.SetHasFrame(false)
	b.AddCSSClass("cal-month-bar")
	b.AddCSSClass("cal-fill")
	b.AddCSSClass(v.page.colorClass(s.CalendarID))
	monthStateClasses(b, s.State)
	content := gtk.NewBox(gtk.OrientationHorizontal, 4)
	if s.ContinuesBefore {
		b.AddCSSClass("cal-continues-before")
	} else {
		b.SetMarginStart(3)
		if s.State != calendar.BlockUnanswered {
			bar := gtk.NewBox(gtk.OrientationHorizontal, 0)
			bar.AddCSSClass("cal-bar")
			content.Append(bar)
		}
	}
	if s.ContinuesAfter {
		b.AddCSSClass("cal-continues-after")
	} else {
		b.SetMarginEnd(3)
	}
	title := s.Title
	if title == "" {
		title = calendar.Untitled(v.env.Tr)
	}
	content.Append(monthTitleLabel(title, s.State))
	b.SetChild(content)
	b.SetSizeRequest(-1, monthPillHeight)
	b.SetMarginBottom(monthPillLine - monthPillHeight)
	b.SetVAlign(gtk.AlignStart)
	b.UpdateProperty([]gtk.AccessibleProperty{gtk.AccessiblePropertyLabel},
		[]coreglib.Value{*coreglib.NewValue(calendar.BlockLabel(s.Occurrence, v.env))})
	o := s.Occurrence
	b.ConnectClicked(func() { v.open(o) })
	return b
}

// pillButton is a pill: the colour dot, the time and the title (an
// all-day pill filled with the colour instead); onClick replaces opening
// the detail (the "+N more" popover closes first).
func (v *calendarMonth) pillButton(p calendar.Pill, onClick func()) *gtk.Button {
	o := p.Occurrence
	b := gtk.NewButton()
	b.SetHasFrame(false)
	b.AddCSSClass("cal-month-pill")
	b.AddCSSClass(v.page.colorClass(o.CalendarID))
	if o.AllDay || p.State == calendar.BlockUnanswered {
		b.AddCSSClass("cal-fill")
	}
	monthStateClasses(b, p.State)
	content := gtk.NewBox(gtk.OrientationHorizontal, 3)
	if !o.AllDay {
		dot := gtk.NewBox(gtk.OrientationHorizontal, 0)
		dot.AddCSSClass("cal-bar")
		dot.AddCSSClass("cal-month-dot")
		dot.SetVAlign(gtk.AlignCenter)
		content.Append(dot)
		if !p.ContinuesBefore {
			t := gtk.NewLabel("")
			t.SetUseMarkup(false)
			t.SetLabel(calendar.TimeText(p.Start, v.env))
			t.AddCSSClass("cal-month-time")
			t.SetEllipsize(pango.EllipsizeEnd)
			t.SetWidthChars(1)
			content.Append(t)
		}
	}
	content.Append(monthTitleLabel(v.titleOf(o), p.State))
	b.SetChild(content)
	b.SetSizeRequest(-1, monthPillHeight)
	b.SetMarginStart(3)
	b.SetMarginEnd(3)
	b.UpdateProperty([]gtk.AccessibleProperty{gtk.AccessiblePropertyLabel},
		[]coreglib.Value{*coreglib.NewValue(calendar.BlockLabel(o, v.env))})
	if onClick == nil {
		onClick = func() { v.open(o) }
	}
	b.ConnectClicked(onClick)
	return b
}

// open is a click on a pill or a bar: the occurrence selected and its
// detail opened (an invitation not in a calendar has no event and is
// only selected).
func (v *calendarMonth) open(o api.Occurrence) {
	c := v.page.ctl
	if o.EventID == "" {
		c.Select(calendar.OccurrenceKey(o))
		return
	}
	c.OpenDetail(o)
}

// showMore is "+N more": a popover with every pill of day, under the
// day's heading.
func (v *calendarMonth) showMore(from *gtk.Button, day time.Time) {
	v.closeMore()
	all, _ := calendar.CellItems(v.occurrences, day, int(^uint(0)>>1), v.env.Loc, v.showDeclined)
	list := gtk.NewBox(gtk.OrientationVertical, 1)
	list.SetSizeRequest(monthPopoverWidth, -1)
	heading := gtk.NewLabel("")
	heading.SetUseMarkup(false)
	heading.SetLabel(calendar.WeekdayDateText(day, v.env))
	heading.SetXAlign(0)
	heading.AddCSSClass("cal-month-popover-heading")
	list.Append(heading)
	pop := gtk.NewPopover()
	for _, p := range all {
		o := p.Occurrence
		key := calendar.OccurrenceKey(o)
		list.Append(v.pillButton(p, func() {
			pop.Popdown()
			v.moreKey, v.moreButton = key, from
			v.open(o)
		}))
	}
	pop.SetChild(list)
	pop.SetParent(from)
	pop.ConnectClosed(func() {
		// After the popover's own handling.
		glib.IdleAdd(func() {
			if v.more == pop {
				v.more = nil
			}
			pop.Unparent()
		})
	})
	v.more = pop
	pop.Popup()
}

// closeMore closes the "+N more" popover before its button goes.
func (v *calendarMonth) closeMore() {
	if v.more == nil {
		return
	}
	pop := v.more
	v.more = nil
	pop.Popdown()
	pop.Unparent()
	v.moreKey, v.moreButton = "", nil
}

// applySelection marks the selected pill and bar.
func (v *calendarMonth) applySelection() {
	mark := func(b *gtk.Button, on bool) {
		if on {
			b.AddCSSClass("cal-month-selected")
		} else {
			b.RemoveCSSClass("cal-month-selected")
		}
	}
	for _, r := range v.rows {
		for _, b := range r.bars {
			mark(b.button, v.selection != "" && b.span.Key == v.selection)
		}
		for _, p := range r.pills {
			mark(p.button, v.selection != "" && p.key == v.selection)
		}
	}
}

// draw paints a row's cells: weekends and other months' days shaded, the
// lines between the days and under the row.
func (r *monthRow) draw(da *gtk.DrawingArea, cr *cairo.Context, width, height int) {
	v := r.v
	fg := da.Color()
	red, green, blue := float64(fg.Red()), float64(fg.Green()), float64(fg.Blue())
	colw := float64(width) / 7
	for col := range 7 {
		cell := v.grid.Cells[r.index*7+col]
		alpha := 0.0
		switch {
		case !cell.InMonth:
			alpha = 0.05
		case cell.Weekend:
			alpha = 0.03
		}
		if alpha > 0 {
			cr.SetSourceRGBA(red, green, blue, alpha)
			cr.Rectangle(float64(col)*colw, 0, colw, float64(height))
			cr.Fill()
		}
	}
	cr.SetSourceRGBA(red, green, blue, 0.12)
	for col := 1; col < 7; col++ {
		cr.Rectangle(float64(int(float64(col)*colw)), 0, 1, float64(height))
	}
	cr.Rectangle(0, 0, 1, float64(height))
	cr.Rectangle(0, float64(height-1), float64(width), 1)
	cr.Fill()
}

// anchorFor is the bar or pill showing the occurrence key
// (calendar.OccurrenceKey), nil when none does; an occurrence opened from
// a "+N more" popover is anchored to its button.
func (v *calendarMonth) anchorFor(key string) gtk.Widgetter {
	if key == "" {
		return nil
	}
	for _, r := range v.rows {
		for _, b := range r.bars {
			if b.span.Key == key && b.button.Visible() {
				return b.button
			}
		}
	}
	for _, r := range v.rows {
		for _, p := range r.pills {
			if p.key == key {
				return p.button
			}
		}
	}
	if key == v.moreKey && v.moreButton != nil {
		return v.moreButton
	}
	return nil
}
