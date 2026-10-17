// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"time"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// The all-day row of the Week and Day views (docs/calendar.md §5.3, §6.5
// G11; port of macos CalendarAllDayView.swift): the "All Day" caption in
// the hour axis's width and a Gtk.Grid with a homogeneous column per day.
// Each day has a cell spanning every row (the day separator and the
// weekend shading, drawn under the bars), row 0 is the top gap and the
// bars are attached at (First, Lane+1, span, 1) in the lanes
// calendar.AllDayLanes gives, up to calAllDayMaxLanes (bars in further
// lanes are not drawn; the Agenda and the Month list them). A bar shows
// its title once and squares the ends where the event goes on
// (cal-continues-before/after). A double click on the row's empty part
// asks for an all-day event that day (calendar.OriginAllDay).

// calAllDayMaxLanes is the most lanes the all-day row grows to.
const calAllDayMaxLanes = 5

// calendarAllDay is the all-day row's widgets.
type calendarAllDay struct {
	week *calendarWeek
	root *gtk.Box
	grid *gtk.Grid
	// days are the row's days (midnights) as last built.
	days []time.Time
	// bars are the bars as last built, in calendar.AllDayLanes' order.
	bars []*allDayBar
	// children are what the grid holds (cells, the gap, the bars), so a
	// rebuild removes exactly them.
	children []gtk.Widgetter
	// lanes is AllDayLanes' count as last built.
	lanes int
}

// allDayBar is one bar of the all-day row.
type allDayBar struct {
	button *gtk.Button
	bar    calendar.Bar
	key    string
}

// newCalendarAllDay builds the row; week places its root.
func newCalendarAllDay(week *calendarWeek) *calendarAllDay {
	a := &calendarAllDay{week: week}
	a.root = gtk.NewBox(gtk.OrientationHorizontal, 0)
	a.root.AddCSSClass("cal-allday-row")
	caption := gtk.NewLabel("")
	caption.SetUseMarkup(false)
	caption.SetText(calendar.AllDay(i18n.Tr))
	caption.AddCSSClass("cal-allday-caption")
	caption.SetWrap(true)
	caption.SetWrapMode(pango.WrapWordChar)
	caption.SetLines(2)
	caption.SetEllipsize(pango.EllipsizeEnd)
	caption.SetJustify(gtk.JustifyRight)
	caption.SetXAlign(1)
	caption.SetYAlign(0)
	caption.SetMarginTop(4)
	caption.SetMarginEnd(8)
	caption.SetSizeRequest(calAxisWidth-8, -1)
	caption.SetMaxWidthChars(1)
	a.root.Append(caption)

	a.grid = gtk.NewGrid()
	a.grid.SetColumnHomogeneous(true)
	a.grid.SetHExpand(true)
	a.root.Append(a.grid)

	click := gtk.NewGestureClick()
	click.ConnectPressed(func(n int, x, _ float64) {
		if n != 2 || len(a.days) == 0 {
			return
		}
		w := a.grid.Width()
		if w <= 0 {
			return
		}
		i := int(x * float64(len(a.days)) / float64(w))
		if i >= 0 && i < len(a.days) {
			a.week.page.ctl.NewEventAt(a.days[i], calendar.OriginAllDay)
		}
	})
	a.grid.AddController(click)
	return a
}

// set rebuilds the row for days and the occurrences.
func (a *calendarAllDay) set(days []time.Time, occurrences []api.Occurrence, showDeclined bool, selection string) {
	for _, c := range a.children {
		a.grid.Remove(c)
	}
	a.children, a.bars = nil, nil
	a.days = days
	env := a.week.page.w.calendarEnv()
	bars, lanes := calendar.AllDayLanes(occurrences, days, env.Loc, showDeclined)
	a.lanes = lanes
	rows := min(lanes, calAllDayMaxLanes) + 1
	for i, d := range days {
		cell := gtk.NewBox(gtk.OrientationVertical, 0)
		cell.AddCSSClass("cal-allday-cell")
		if weekViewWeekend(d) {
			cell.AddCSSClass("cal-weekend")
		}
		cell.SetCanTarget(false)
		a.grid.Attach(cell, i, 0, 1, rows)
		a.children = append(a.children, cell)
	}
	gap := gtk.NewBox(gtk.OrientationVertical, 0)
	gap.SetSizeRequest(-1, 2)
	gap.SetCanTarget(false)
	a.grid.Attach(gap, 0, 0, max(1, len(days)), 1)
	a.children = append(a.children, gap)
	for _, b := range bars {
		if b.Lane >= calAllDayMaxLanes {
			continue
		}
		bar := a.makeBar(b, env, selection)
		a.grid.Attach(bar.button, b.First, b.Lane+1, b.Last-b.First+1, 1)
		a.children = append(a.children, bar.button)
		a.bars = append(a.bars, bar)
	}
}

// makeBar is one bar's button.
func (a *calendarAllDay) makeBar(b calendar.Bar, env calendar.Env, selection string) *allDayBar {
	o := b.Occurrence
	bar := &allDayBar{bar: b, key: calendar.OccurrenceKey(o), button: gtk.NewButton()}
	btn := bar.button
	btn.AddCSSClass("cal-allday-bar")
	btn.AddCSSClass("cal-fill")
	btn.AddCSSClass(a.week.page.colorClass(o.CalendarID))
	for _, c := range weekStateClasses(b.State) {
		btn.AddCSSClass(c)
	}
	if b.ContinuesBefore {
		btn.AddCSSClass("cal-continues-before")
	}
	if b.ContinuesAfter {
		btn.AddCSSClass("cal-continues-after")
	}
	if bar.key == selection {
		btn.AddCSSClass("cal-selected")
	}
	btn.SetOverflow(gtk.OverflowHidden)
	title := gtk.NewLabel("")
	title.SetUseMarkup(false)
	title.SetText(calendar.BlockTitle(o, env.Tr))
	title.SetEllipsize(pango.EllipsizeEnd)
	title.SetMaxWidthChars(1)
	title.SetHExpand(true)
	title.SetXAlign(0)
	title.AddCSSClass("cal-block-title")
	btn.SetChild(title)
	label := calendar.BlockLabel(o, env)
	btn.SetTooltipText(label)
	weekAccessibleLabel(btn, label)
	btn.ConnectClicked(func() { a.week.open(o) })
	return bar
}

// setSelection marks the bar of key selected.
func (a *calendarAllDay) setSelection(key string) {
	for _, b := range a.bars {
		if b.key == key {
			b.button.AddCSSClass("cal-selected")
		} else {
			b.button.RemoveCSSClass("cal-selected")
		}
	}
}

// barFor is the bar of key, nil when none shows it.
func (a *calendarAllDay) barFor(key string) *allDayBar {
	for _, b := range a.bars {
		if b.key == key {
			return b
		}
	}
	return nil
}

// weekViewWeekend: d is a Saturday or a Sunday (greyed and shaded).
func weekViewWeekend(d time.Time) bool {
	return d.Weekday() == time.Saturday || d.Weekday() == time.Sunday
}

// weekStateClasses are the CSS classes of a block's or bar's state.
func weekStateClasses(s calendar.BlockState) []string {
	switch s {
	case calendar.BlockTentative:
		return []string{"cal-tentative"}
	case calendar.BlockUnanswered:
		return []string{"cal-unanswered"}
	case calendar.BlockDeclined:
		return []string{"cal-declined"}
	case calendar.BlockCancelled:
		return []string{"cal-cancelled"}
	}
	return nil
}
