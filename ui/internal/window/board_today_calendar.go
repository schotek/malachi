// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/calendardata"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/style"
)

// The Board → Today strip "Today in Calendar" (docs/calendar.md §5.2,
// §5.9, §6.5 G19): under the Today style's greeting, today's occurrences
// of the window's calendar source in the agenda's order
// (calendar.TodayRows), each a row with its time range (or All Day), its
// calendar's colour dot and its title, then "took place" for one that is
// over, Respond for an unanswered invitation or the place; "Open Calendar
// ›" at the right of the heading opens the Calendar on today, and a row
// (activated) opens it on its event. Hidden while no account has the
// calendar capability (calendar.ShowsTodayStrip); without events today it
// says so (calendar.NothingToday), as macOS does.
//
// Respond selects the invitation's message in Mail, where its card
// answers it (boardPage.showInMail's reveal), as macOS does. Every text
// is a cleaned plain string of ui/internal/calendar, shown without
// markup. Port of macos BoardTodayCalendarStrip.swift.

// todayCalendarTick is how often the strip looks at the clock: midnight
// brings another day, and "took place" moves on.
const todayCalendarTick = 60 * time.Second

// todayCalendarStrip is the strip and what it shows.
type todayCalendarStrip struct {
	w      *Window
	p      *boardPage
	source calendardata.DataSource

	root    *gtk.Box
	heading *gtk.Label
	open    *gtk.Button
	list    *gtk.ListBox

	rows      []calendar.TodayRow
	shown     bool
	loadedDay time.Time
	loading   bool
	last      []api.Occurrence
	hasLast   bool
	traced    string
}

// newTodayCalendarStrip builds the strip and puts it after the Today
// style's greeting (the parent box of after) in board_today_main. Nothing
// is attached yet: attach follows a source.
func newTodayCalendarStrip(w *Window, p *boardPage, main *gtk.Box, after gtk.Widgetter) *todayCalendarStrip {
	s := &todayCalendarStrip{w: w, p: p}
	s.root = gtk.NewBox(gtk.OrientationVertical, 4)
	s.root.AddCSSClass("board-today-card")
	s.root.SetVisible(false)

	head := gtk.NewBox(gtk.OrientationHorizontal, 8)
	s.heading = gtk.NewLabel(strings.ToUpper(calendar.TodayHeading(i18n.Tr)))
	s.heading.SetUseMarkup(false)
	s.heading.SetXAlign(0)
	s.heading.SetHExpand(true)
	s.heading.AddCSSClass("caption-heading")
	s.heading.AddCSSClass("dim-label")
	s.open = gtk.NewButtonWithLabel(calendar.OpenCalendar(i18n.Tr) + " ›")
	s.open.AddCSSClass("flat")
	s.open.AddCSSClass("caption")
	s.open.SetVAlign(gtk.AlignCenter)
	s.open.ConnectClicked(func() { s.w.showInCalendar("", "", time.Time{}) })
	head.Append(s.heading)
	head.Append(s.open)
	s.root.Append(head)

	s.list = gtk.NewListBox()
	s.list.SetSelectionMode(gtk.SelectionNone)
	s.list.AddCSSClass("boxed-list-separate")
	s.list.ConnectRowActivated(func(row *gtk.ListBoxRow) {
		i := row.Index()
		if i < 0 || i >= len(s.rows) {
			return
		}
		o := s.rows[i].Occurrence
		s.w.showInCalendar(o.EventID, o.RecurrenceID, time.Now())
	})
	s.root.Append(s.list)

	if main != nil {
		main.InsertChildAfter(s.root, after)
	}
	return s
}

// attach follows source from now on (once), with the clock's tick.
func (s *todayCalendarStrip) attach(source calendardata.DataSource) {
	if s.source != nil || source == nil {
		return
	}
	s.source = source
	source.Observe(s.sourceChanged)
	glib.TimeoutAdd(uint(todayCalendarTick/time.Millisecond), func() bool {
		s.tick()
		return true
	})
	s.update()
}

// sourceChanged: a new snapshot (accounts, visible calendars,
// invitations) or today's occurrences dropped load the rows again.
func (s *todayCalendarStrip) sourceChanged(c calendardata.Change) {
	switch c.Kind {
	case calendardata.ChangeSnapshot:
		s.loadedDay = time.Time{}
		s.update()
	case calendardata.ChangeOccurrences:
		day := calendar.StartOfDay(time.Now(), time.Local)
		if c.Overlaps(day, calendar.AddDays(day, 1)) {
			s.loadedDay = time.Time{}
			s.update()
		}
	}
}

func (s *todayCalendarStrip) tick() {
	if !s.shown {
		return
	}
	if !s.loadedDay.Equal(calendar.StartOfDay(time.Now(), time.Local)) {
		s.loadedDay = time.Time{}
	}
	s.update()
}

// update shows or hides the strip, and loads today's occurrences when they
// are not loaded for today.
func (s *todayCalendarStrip) update() {
	if s.source == nil {
		return
	}
	shown := calendar.ShowsTodayStrip(s.source.Snapshot().Accounts)
	if shown != s.shown {
		s.shown = shown
		s.root.SetVisible(shown)
	}
	defer s.trace()
	if !shown {
		return
	}
	now := time.Now()
	day := calendar.StartOfDay(now, time.Local)
	r := calendardata.Range{From: day, To: calendar.AddDays(day, 1), Loc: time.Local}
	if hit, ok := s.source.CachedOccurrences(r); ok {
		s.loadedDay = day
		s.show(hit, true, now)
		return
	}
	if s.loadedDay.Equal(day) {
		s.show(nil, false, now)
		return
	}
	if s.loading {
		return
	}
	s.loading = true
	s.source.Occurrences(r, func(o []api.Occurrence, err *api.Error) {
		s.loading = false
		if err != nil {
			return
		}
		s.loadedDay = day
		s.show(o, true, time.Now())
		s.trace()
	})
}

// show renders the rows of occurrences (fresh) or of the last ones.
func (s *todayCalendarStrip) show(occurrences []api.Occurrence, fresh bool, now time.Time) {
	if fresh {
		s.last, s.hasLast = occurrences, true
	}
	if !s.hasLast {
		return
	}
	snap := s.source.Snapshot()
	visible := snap.VisibleCalendars()
	var kept []api.Occurrence
	for _, o := range s.last {
		if o.CalendarID == "" || visible[o.CalendarID] {
			kept = append(kept, o)
		}
	}
	s.rows = calendar.TodayRows(kept, now, s.w.calendarEnv(), snap.Preferences.ShowDeclined)
	s.rebuild(snap)
}

// rebuild replaces the list's rows.
func (s *todayCalendarStrip) rebuild(snap calendardata.Snapshot) {
	s.list.RemoveAll()
	if len(s.rows) == 0 {
		l := gtk.NewLabel(calendar.NothingToday(i18n.Tr))
		l.SetUseMarkup(false)
		l.SetXAlign(0)
		l.AddCSSClass("dim-label")
		l.SetMarginTop(6)
		l.SetMarginBottom(6)
		l.SetMarginStart(10)
		row := gtk.NewListBoxRow()
		row.SetActivatable(false)
		row.SetChild(l)
		s.list.Append(row)
		return
	}
	colors := todayCalendarColors(snap)
	for _, r := range s.rows {
		s.list.Append(s.rowFor(r, colors))
	}
}

// todayCalendarColors loads the calendars' colours into the calendar's
// stylesheet, as the Calendar's page does (calendarPage.applyColors: every
// calendar of calendar.list in its order, so the indexes agree), and
// returns each calendar's index.
func todayCalendarColors(snap calendardata.Snapshot) map[api.CalendarID]int {
	var colors []string
	index := map[api.CalendarID]int{}
	for _, a := range snap.CalendarAccounts {
		for _, k := range a.Calendars {
			if _, ok := index[k.ID]; ok {
				continue
			}
			index[k.ID] = len(colors)
			colors = append(colors, k.Color)
		}
	}
	style.ApplyCalendar(colors)
	return index
}

// rowFor is one row: time, dot, title, then took place, Respond or the
// place.
func (s *todayCalendarStrip) rowFor(r calendar.TodayRow, colors map[api.CalendarID]int) *gtk.ListBoxRow {
	box := gtk.NewBox(gtk.OrientationHorizontal, 8)
	box.SetMarginTop(4)
	box.SetMarginBottom(4)
	box.SetMarginStart(10)
	box.SetMarginEnd(10)

	t := gtk.NewLabel(r.Time)
	t.SetUseMarkup(false)
	t.SetXAlign(0)
	t.SetWidthChars(13)
	t.SetEllipsize(pango.EllipsizeEnd)
	t.AddCSSClass("numeric")
	if r.TookPlace {
		t.AddCSSClass("dim-label")
	}
	box.Append(t)

	dot := gtk.NewBox(gtk.OrientationHorizontal, 0)
	dot.SetVAlign(gtk.AlignCenter)
	dot.AddCSSClass("cal-dot")
	if id := r.Occurrence.CalendarID; id != "" {
		dot.AddCSSClass(style.CalendarClass(colors[id]))
	} else {
		dot.AddCSSClass("cal-dot-accent")
	}
	if r.State == calendar.BlockUnanswered {
		dot.AddCSSClass("cal-unanswered")
	} else {
		dot.AddCSSClass("cal-swatch")
	}
	box.Append(dot)

	title := gtk.NewLabel(r.Title)
	title.SetUseMarkup(false)
	title.SetXAlign(0)
	title.SetHExpand(true)
	title.SetEllipsize(pango.EllipsizeEnd)
	if r.TookPlace {
		title.AddCSSClass("dim-label")
	}
	box.Append(title)

	switch {
	case r.TookPlace:
		box.Append(todayTrailing(calendar.TookPlace(i18n.Tr)))
	case r.Respond && r.Occurrence.Invitation != nil:
		ref := *r.Occurrence.Invitation
		b := gtk.NewButtonWithLabel(calendar.Respond(i18n.Tr))
		b.AddCSSClass("caption")
		b.SetVAlign(gtk.AlignCenter)
		b.ConnectClicked(func() { s.respond(ref) })
		box.Append(b)
	case r.Place != "":
		l := todayTrailing(r.Place)
		l.SetMaxWidthChars(24)
		box.Append(l)
	}
	row := gtk.NewListBoxRow()
	row.SetChild(box)
	row.SetActivatable(true)
	return row
}

// todayTrailing is a row's caption at its end.
func todayTrailing(text string) *gtk.Label {
	l := gtk.NewLabel(text)
	l.SetUseMarkup(false)
	l.SetXAlign(1)
	l.SetEllipsize(pango.EllipsizeEnd)
	l.AddCSSClass("caption")
	l.AddCSSClass("dim-label")
	return l
}

// respond is Respond: the invitation's message selected in Mail, where its
// card answers it; the sample calendar's ids are invented.
func (s *todayCalendarStrip) respond(ref api.MessageRef) {
	if calendarSamples || s.p == nil {
		s.w.Toast(calendar.PreviewOnly(i18n.Tr))
		return
	}
	s.p.showInMail(board.Detail{AccountID: ref.AccountID, LatestMessage: ref.MessageID})
}

// trace is MALACHI_START's LAYOUT: line of the strip (development only,
// like the board's smoke tests): its allocation, whether it shows and its
// rows.
func (s *todayCalendarStrip) trace() {
	if os.Getenv("MALACHI_START") == "" && os.Getenv("MALACHI_GTK_SMOKE") == "" {
		return
	}
	line := fmt.Sprintf("LAYOUT: today-strip %dx%d shown %v rows %d", s.root.Width(), s.root.Height(), s.shown, len(s.rows))
	if line == s.traced {
		return
	}
	s.traced = line
	fmt.Fprintln(os.Stderr, line)
}

// bindTodayCalendar is board_today.go's hook: the strip after the
// greeting (title's box) in board_today_main, following the window's
// calendar source.
func (p *boardPage) bindTodayCalendar(b *gtk.Builder, title *gtk.Label) *todayCalendarStrip {
	if p.w == nil {
		return nil
	}
	main, _ := b.GetObject("board_today_main").Cast().(*gtk.Box)
	s := newTodayCalendarStrip(p.w, p, main, title.Parent())
	s.attach(p.w.calendarSource())
	return s
}
