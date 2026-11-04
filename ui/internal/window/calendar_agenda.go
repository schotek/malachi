// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"reflect"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/calendarctl"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// The Agenda, the Unanswered filter and the search results
// (docs/calendar.md §5.4, §6.5 G10) in the page's agenda_bin and
// search_bin: a heading in place of the view ("Results", "Invitations"),
// a ListBox of day header rows (not selectable) and occurrence rows (time
// or All Day, the calendar's colour dot, title, place and the response
// capsule), the controller's empty text. The Agenda asks for its next
// page (LoadMoreAgenda) as the list scrolls near its end; the results
// come without day headers, each row with its day and calendar. The page
// makes it once (newCalendarAgenda), calls update after every change of
// the controller and asks anchorFor for a popover's anchor. Port of macos
// CalendarAgendaViewController.swift and CalendarSearchController.swift.

// agendaPagingMargin is how near the list's end (px) the next page is
// asked for.
const agendaPagingMargin = 200

// agendaEntry is a line of a list: a day's heading (Heading set) or an
// occurrence with its time column's text and its second line.
type agendaEntry struct {
	Heading string
	Item    calendar.AgendaItem
	Time    string
	// Place is the row's dim text: the place, in the results also the
	// calendar's name.
	Place string
}

// calendarList is one of the two lists (the Agenda and Unanswered in
// agenda_bin, the results in search_bin).
type calendarList struct {
	page    *calendarPage
	root    *gtk.Box
	heading *gtk.Label
	scroll  *gtk.ScrolledWindow
	list    *gtk.ListBox
	empty   *gtk.Label
	entries []agendaEntry
	// rows are the list's rows by index of entries.
	rows []*gtk.ListBoxRow
	// paging: this list pages on (the Agenda view itself); requested: a
	// page was asked for and has not arrived.
	paging, requested bool
	// selecting is set while the list follows the controller's selection.
	selecting bool
}

// calendarAgenda is the lists' widgets.
type calendarAgenda struct {
	page    *calendarPage
	agenda  *calendarList
	results *calendarList
}

// newCalendarAgenda builds the lists into page.agendaBin and
// page.searchBin.
func newCalendarAgenda(page *calendarPage) *calendarAgenda {
	a := &calendarAgenda{page: page, agenda: newCalendarList(page), results: newCalendarList(page)}
	page.agendaBin.SetChild(a.agenda.root)
	page.searchBin.SetChild(a.results.root)
	return a
}

// newCalendarList makes an empty list.
func newCalendarList(page *calendarPage) *calendarList {
	l := &calendarList{page: page}
	l.root = gtk.NewBox(gtk.OrientationVertical, 0)
	l.root.AddCSSClass("cal-agenda")
	l.heading = gtk.NewLabel("")
	l.heading.SetUseMarkup(false)
	l.heading.SetXAlign(0)
	l.heading.SetEllipsize(pango.EllipsizeEnd)
	l.heading.SetMarginTop(10)
	l.heading.SetMarginStart(20)
	l.heading.SetMarginEnd(20)
	l.heading.SetMarginBottom(4)
	l.heading.AddCSSClass("title-2")
	l.heading.SetVisible(false)
	l.root.Append(l.heading)

	l.list = gtk.NewListBox()
	l.list.SetSelectionMode(gtk.SelectionSingle)
	l.list.AddCSSClass("cal-agenda-list")
	l.list.ConnectRowActivated(func(row *gtk.ListBoxRow) {
		if e, ok := l.entryOf(row); ok {
			l.page.ctl.OpenDetail(e.Item.Occurrence)
		}
	})
	l.list.ConnectRowSelected(func(row *gtk.ListBoxRow) {
		if l.selecting || row == nil {
			return
		}
		if e, ok := l.entryOf(row); ok {
			l.page.ctl.Select(calendar.OccurrenceKey(e.Item.Occurrence))
		}
	})
	l.scroll = gtk.NewScrolledWindow()
	l.scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	l.scroll.SetVExpand(true)
	l.scroll.SetChild(l.list)
	adj := l.scroll.VAdjustment()
	adj.ConnectValueChanged(func() {
		if !l.paging || l.requested {
			return
		}
		if adj.Upper() <= 0 || adj.Value()+adj.PageSize() < adj.Upper()-agendaPagingMargin {
			return
		}
		l.requested = true
		l.page.ctl.LoadMoreAgenda()
	})
	l.root.Append(l.scroll)

	l.empty = gtk.NewLabel("")
	l.empty.SetUseMarkup(false)
	l.empty.SetWrap(true)
	l.empty.SetJustify(gtk.JustifyCenter)
	l.empty.SetVExpand(true)
	l.empty.SetMarginStart(40)
	l.empty.SetMarginEnd(40)
	l.empty.AddCSSClass("dim-label")
	l.empty.SetVisible(false)
	l.root.Append(l.empty)
	return l
}

// update shows the controller's state.
func (a *calendarAgenda) update() {
	c := a.page.ctl
	env := a.page.w.calendarEnv()
	// The Agenda view's pages or the Unanswered filter.
	var agenda []agendaEntry
	if days, ok := c.Agenda(); ok {
		for _, d := range days {
			agenda = append(agenda, agendaEntry{Heading: d.Heading})
			for _, it := range d.Items {
				agenda = append(agenda, agendaEntry{Item: it, Time: calendar.ItemTime(it, env),
					Place: board.CleanLine(it.Occurrence.Location, api.MaxEventLocationBytes)})
			}
		}
	}
	agendaShown := !c.Searching()
	a.agenda.paging = c.View() == calendar.ViewAgenda && c.Filter() == calendarctl.FilterNone && agendaShown
	a.agenda.set(agenda, headingIf(agendaShown, c.ListHeading()), emptyIf(agendaShown, c.EmptyListText()), c)

	// The results, in the daemon's order: each row's day before its time,
	// its calendar after its place.
	results, has := c.SearchResults()
	var found []agendaEntry
	if has {
		found = resultEntries(results, c.Snapshot().CalendarName, time.Now(), env)
	}
	a.results.paging = false
	a.results.set(found, headingIf(has, c.ListHeading()), emptyIf(has, c.EmptyListText()), c)
}

// headingIf is h when the list shows it.
func headingIf(shown bool, h string) string {
	if shown {
		return h
	}
	return ""
}

// emptyIf is e when the list shows it.
func emptyIf(shown bool, e string) string { return headingIf(shown, e) }

// resultEntries are the search results' rows: no day headers, the day
// before each row's time ("Tomorrow · 10:00–10:30"), the calendar after
// its place.
func resultEntries(results []api.Occurrence, calendarName func(api.CalendarID) string, now time.Time, env calendar.Env) []agendaEntry {
	loc := env.Loc
	if loc == nil {
		loc = time.Local
	}
	out := make([]agendaEntry, 0, len(results))
	for _, o := range results {
		s, e, ok := calendar.Span(o, loc)
		if !ok {
			continue
		}
		it := calendar.AgendaItem{Occurrence: o, Start: s, End: e, DayOf: 1, Days: 1, State: calendar.StateOf(o)}
		day := calendar.DayHeading(calendar.StartOfDay(s, loc), now, env)
		place := board.CleanLine(o.Location, api.MaxEventLocationBytes)
		if o.CalendarID != "" {
			if name := board.CleanLine(calendarName(o.CalendarID), api.MaxEventLocationBytes); name != "" {
				if place == "" {
					place = name
				} else {
					place = calendar.Dot(place, name, env.Tr)
				}
			}
		}
		out = append(out, agendaEntry{Item: it, Time: calendar.Dot(day, calendar.ItemTime(it, env), env.Tr), Place: place})
	}
	return out
}

// set shows entries under heading, or empty when there are none.
func (l *calendarList) set(entries []agendaEntry, heading, empty string, c *calendarctl.CalendarController) {
	l.heading.SetText(heading)
	l.heading.SetVisible(heading != "")
	l.empty.SetText(empty)
	showEmpty := empty != "" && !c.Loading()
	l.empty.SetVisible(showEmpty)
	l.scroll.SetVisible(!showEmpty)
	if !reflect.DeepEqual(entries, l.entries) || (l.rows == nil && entries != nil) {
		l.entries = entries
		l.requested = false
		l.rebuild()
	} else if !c.Loading() {
		l.requested = false
	}
	l.selectKey(c.Selection())
}

// rebuild makes the rows for l.entries.
func (l *calendarList) rebuild() {
	l.selecting = true
	l.list.RemoveAll()
	l.selecting = false
	l.rows = make([]*gtk.ListBoxRow, len(l.entries))
	for i, e := range l.entries {
		var row *gtk.ListBoxRow
		if e.Heading != "" {
			row = agendaHeadingRow(e.Heading)
		} else {
			row = l.itemRow(e)
		}
		l.rows[i] = row
		l.list.Append(row)
	}
}

// entryOf is the occurrence entry row shows.
func (l *calendarList) entryOf(row *gtk.ListBoxRow) (agendaEntry, bool) {
	i := row.Index()
	if i < 0 || i >= len(l.entries) || l.entries[i].Heading != "" {
		return agendaEntry{}, false
	}
	return l.entries[i], true
}

// indexOf is the first row showing the occurrence key; -1 when none does.
func (l *calendarList) indexOf(key string) int {
	if key == "" {
		return -1
	}
	for i, e := range l.entries {
		if e.Heading == "" && calendar.OccurrenceKey(e.Item.Occurrence) == key {
			return i
		}
	}
	return -1
}

// selectKey selects the row of key, none when no row shows it.
func (l *calendarList) selectKey(key string) {
	l.selecting = true
	defer func() { l.selecting = false }()
	i := l.indexOf(key)
	if i < 0 || i >= len(l.rows) {
		if l.list.SelectedRow() != nil {
			l.list.UnselectAll()
		}
		return
	}
	if r := l.list.SelectedRow(); r == nil || r.Index() != i {
		l.list.SelectRow(l.rows[i])
	}
}

// agendaHeadingRow is a day's heading row, neither selectable nor
// activatable.
func agendaHeadingRow(text string) *gtk.ListBoxRow {
	row := gtk.NewListBoxRow()
	row.SetSelectable(false)
	row.SetActivatable(false)
	row.SetCanFocus(false)
	row.AddCSSClass("cal-agenda-day")
	label := gtk.NewLabel(text)
	label.SetUseMarkup(false)
	label.SetXAlign(0)
	label.SetEllipsize(pango.EllipsizeEnd)
	label.AddCSSClass("heading")
	row.SetChild(label)
	return row
}

// itemRow is an occurrence's row: time, colour dot, title, place, the
// response capsule (unanswered, tentative, declined, cancelled; "Day 2 of
// 3" for a part of a longer one).
func (l *calendarList) itemRow(e agendaEntry) *gtk.ListBoxRow {
	tr := i18n.Tr
	env := l.page.w.calendarEnv()
	o := e.Item.Occurrence
	row := gtk.NewListBoxRow()
	row.AddCSSClass("cal-agenda-item")
	box := gtk.NewBox(gtk.OrientationHorizontal, 8)

	t := gtk.NewLabel(e.Time)
	t.SetUseMarkup(false)
	t.SetXAlign(0)
	t.SetEllipsize(pango.EllipsizeEnd)
	t.SetSizeRequest(170, -1)
	t.AddCSSClass("dim-label")
	t.AddCSSClass("numeric")
	t.AddCSSClass("cal-agenda-time")
	box.Append(t)

	dot := gtk.NewBox(gtk.OrientationHorizontal, 0)
	dot.SetVAlign(gtk.AlignCenter)
	dot.AddCSSClass("cal-dot")
	if o.CalendarID != "" {
		dot.AddCSSClass(l.page.colorClass(o.CalendarID))
	} else {
		dot.AddCSSClass("cal-dot-accent")
	}
	if e.Item.State == calendar.BlockUnanswered {
		dot.AddCSSClass("cal-unanswered")
	} else {
		dot.AddCSSClass("cal-swatch")
	}
	box.Append(dot)

	title := gtk.NewLabel(calendarTitle(o.Title))
	title.SetUseMarkup(false)
	title.SetXAlign(0)
	title.SetEllipsize(pango.EllipsizeEnd)
	title.AddCSSClass("cal-agenda-title")
	if s := e.Item.State; s == calendar.BlockDeclined || s == calendar.BlockCancelled {
		attrs := pango.NewAttrList()
		attrs.Insert(pango.NewAttrStrikethrough(true))
		title.SetAttributes(attrs)
		if s == calendar.BlockCancelled {
			title.AddCSSClass("dim-label")
		}
	}
	box.Append(title)

	place := gtk.NewLabel(e.Place)
	place.SetUseMarkup(false)
	place.SetXAlign(0)
	place.SetHExpand(true)
	place.SetEllipsize(pango.EllipsizeEnd)
	place.AddCSSClass("dim-label")
	box.Append(place)

	var badge string
	switch e.Item.State {
	case calendar.BlockUnanswered, calendar.BlockTentative, calendar.BlockDeclined:
		badge = calendar.ResponseLabel(o.MyResponse, tr)
	case calendar.BlockCancelled:
		badge = calendar.BlockStateText(calendar.BlockCancelled, tr)
	}
	if badge == "" && e.Item.Days > 1 {
		badge = calendar.DayOfText(e.Item.DayOf, e.Item.Days, tr)
	}
	if badge != "" {
		b := gtk.NewLabel(badge)
		b.SetUseMarkup(false)
		b.SetVAlign(gtk.AlignCenter)
		b.AddCSSClass("cal-agenda-badge")
		if e.Item.State == calendar.BlockUnanswered {
			b.AddCSSClass("cal-fill")
			if o.CalendarID != "" {
				b.AddCSSClass(l.page.colorClass(o.CalendarID))
			}
		}
		box.Append(b)
	}
	row.SetChild(box)
	row.UpdateProperty([]gtk.AccessibleProperty{gtk.AccessiblePropertyLabel},
		[]coreglib.Value{*coreglib.NewValue(calendar.BlockLabel(o, env))})
	return row
}

// calendarTitle is an event's title for display: cleaned, Untitled when
// empty.
func calendarTitle(s string) string {
	if s = board.CleanLine(s, api.MaxEventTitleBytes); s == "" {
		return calendar.Untitled(i18n.Tr)
	}
	return s
}

// anchorFor is the row showing the occurrence key (calendar.OccurrenceKey),
// nil when none does.
func (a *calendarAgenda) anchorFor(key string) gtk.Widgetter {
	l := a.agenda
	if a.page.stack.VisibleChildName() == "search" {
		l = a.results
	}
	if i := l.indexOf(key); i >= 0 && i < len(l.rows) {
		return l.rows[i]
	}
	return nil
}
