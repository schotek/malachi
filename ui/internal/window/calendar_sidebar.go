// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"reflect"
	"strconv"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/calendarctl"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The Calendar's sidebar (docs/calendar.md §5.3, §6.5 G10) in the page's
// sidebar_bin: the mini month, Invitations ▸ Unanswered with its count
// badge, a section per calendar account (its name and kind capsule, a
// check button per calendar with its colour square and note, the state
// row with Sign In…), and Add Calendar… with its menu. Everything comes
// from CalendarController.Sidebar; names only through labels without
// markup. Port of macos CalendarSidebarViewController.swift and
// MiniMonthView.swift; the static CSS is style/calendar_sidebar.go.

// calendarSidebar is the sidebar's widgets.
type calendarSidebar struct {
	page *calendarPage

	root *gtk.ScrolledWindow

	// The mini month: its title, ‹ ›, the weekday headers and the 42 day
	// buttons (each holding its number's label, which carries today's
	// circle while the button carries the band).
	miniTitle  *gtk.Label
	miniPrev   *gtk.Button
	miniNext   *gtk.Button
	miniGrid   *gtk.Grid
	miniHeads  [7]*gtk.Label
	miniDays   [42]*gtk.Button
	miniNums   [42]*gtk.Label
	mini       calendar.MiniMonth
	miniLoaded bool

	// list holds the rows after the mini month: headings, Unanswered,
	// calendars, state rows, Add Calendar….
	list       *gtk.ListBox
	unanswered *gtk.ListBoxRow
	addButton  *gtk.MenuButton
	// shown is the model the rows were last built for (without its mini
	// month), shownFilter the filter then.
	shown       *calendarctl.Sidebar
	shownFilter calendarctl.Filter
	// selecting is set while the sidebar changes the list's selection
	// itself.
	selecting bool
}

// newCalendarSidebar builds the sidebar into page.sidebarBin.
func newCalendarSidebar(page *calendarPage) *calendarSidebar {
	s := &calendarSidebar{page: page}
	tr := i18n.Tr

	box := gtk.NewBox(gtk.OrientationVertical, 0)
	box.SetMarginTop(8)
	box.SetMarginBottom(12)
	box.SetMarginStart(12)
	box.SetMarginEnd(12)
	box.Append(s.buildMiniMonth())

	s.list = gtk.NewListBox()
	s.list.SetSelectionMode(gtk.SelectionSingle)
	s.list.AddCSSClass("navigation-sidebar")
	s.list.AddCSSClass("cal-sidebar-list")
	s.list.SetMarginTop(10)
	s.list.ConnectRowActivated(func(row *gtk.ListBoxRow) {
		if s.unanswered != nil && row.Index() == s.unanswered.Index() {
			s.page.ctl.ShowUnanswered()
		}
	})
	box.Append(s.list)

	// Add Calendar…'s menu acts in the sidebar's own action group.
	group := gio.NewSimpleActionGroup()
	caldav := gio.NewSimpleAction("add-caldav", nil)
	caldav.ConnectActivate(func(*glib.Variant) {
		s.page.w.showAddCalDAV(s.page.w.ApplicationWindow, nil)
	})
	group.AddAction(caldav)
	ms := gio.NewSimpleAction("sync-microsoft", nil)
	ms.ConnectActivate(func(*glib.Variant) {
		// Preferences open on their first page, Accounts.
		if a := s.page.w.app; a != nil {
			a.ActivateAction("preferences", nil)
		}
	})
	group.AddAction(ms)

	s.addButton = gtk.NewMenuButton()
	s.addButton.AddCSSClass("flat")
	s.addButton.AddCSSClass("cal-add-calendar")
	s.addButton.SetHAlign(gtk.AlignStart)
	s.addButton.SetMarginTop(6)
	add := gtk.NewBox(gtk.OrientationHorizontal, 6)
	add.Append(gtk.NewImageFromIconName("list-add-symbolic"))
	addLabel := gtk.NewLabel(calendar.AddCalendar(tr))
	addLabel.SetUseMarkup(false)
	add.Append(addLabel)
	s.addButton.SetChild(add)
	menu := gio.NewMenu()
	menu.Append(calendar.AddCalDAV(tr), "calsidebar.add-caldav")
	menu.Append(calendar.SyncMicrosoft(tr), "calsidebar.sync-microsoft")
	s.addButton.SetMenuModel(menu)
	box.Append(s.addButton)

	s.root = gtk.NewScrolledWindow()
	s.root.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	s.root.SetVExpand(true)
	s.root.SetChild(box)
	s.root.InsertActionGroup("calsidebar", group)
	page.sidebarBin.SetChild(s.root)
	return s
}

// buildMiniMonth makes the mini month's widgets: the title row with ‹ ›
// and a 7 × 7 grid of the weekday headers and the days.
func (s *calendarSidebar) buildMiniMonth() gtk.Widgetter {
	tr := i18n.Tr
	col := gtk.NewBox(gtk.OrientationVertical, 2)
	col.AddCSSClass("cal-mini")

	head := gtk.NewBox(gtk.OrientationHorizontal, 2)
	s.miniTitle = gtk.NewLabel("")
	s.miniTitle.SetUseMarkup(false)
	s.miniTitle.SetXAlign(0)
	s.miniTitle.SetHExpand(true)
	s.miniTitle.SetEllipsize(pango.EllipsizeEnd)
	s.miniTitle.SetMarginStart(4)
	s.miniTitle.AddCSSClass("heading")
	head.Append(s.miniTitle)
	s.miniPrev = gtk.NewButtonFromIconName("go-previous-symbolic")
	s.miniNext = gtk.NewButtonFromIconName("go-next-symbolic")
	for _, b := range []*gtk.Button{s.miniPrev, s.miniNext} {
		b.AddCSSClass("flat")
		b.AddCSSClass("circular")
		b.AddCSSClass("cal-mini-step")
		head.Append(b)
	}
	s.miniPrev.SetTooltipText(calendar.Previous(tr))
	s.miniNext.SetTooltipText(calendar.Next(tr))
	s.miniPrev.ConnectClicked(func() { s.page.ctl.StepMiniMonth(-1) })
	s.miniNext.ConnectClicked(func() { s.page.ctl.StepMiniMonth(1) })
	col.Append(head)

	s.miniGrid = gtk.NewGrid()
	s.miniGrid.SetColumnHomogeneous(true)
	s.miniGrid.SetRowHomogeneous(true)
	s.miniGrid.AddCSSClass("cal-mini-grid")
	for i := range s.miniHeads {
		l := gtk.NewLabel("")
		l.SetUseMarkup(false)
		l.AddCSSClass("caption")
		l.AddCSSClass("dim-label")
		l.AddCSSClass("cal-mini-head")
		s.miniHeads[i] = l
		s.miniGrid.Attach(l, i, 0, 1, 1)
	}
	for i := range s.miniDays {
		n := gtk.NewLabel("")
		n.SetUseMarkup(false)
		n.SetHAlign(gtk.AlignCenter)
		n.SetVAlign(gtk.AlignCenter)
		n.AddCSSClass("cal-mini-number")
		b := gtk.NewButton()
		b.SetChild(n)
		b.AddCSSClass("flat")
		b.AddCSSClass("cal-mini-day")
		b.SetCanFocus(true)
		idx := i
		b.ConnectClicked(func() {
			if s.miniLoaded {
				s.page.ctl.GoTo(s.mini.Days[idx].Date)
			}
		})
		s.miniDays[i], s.miniNums[i] = b, n
		s.miniGrid.Attach(b, i%7, 1+i/7, 1, 1)
	}
	col.Append(s.miniGrid)
	return col
}

// The mini month's state classes (style/calendar_sidebar.go).
var miniDayClasses = []string{
	"cal-mini-out", "cal-mini-weekend", "cal-mini-today", "cal-mini-band", "cal-mini-band-start", "cal-mini-band-end",
}

// setMiniMonth shows m in the mini month's widgets when it changed.
func (s *calendarSidebar) setMiniMonth(m calendar.MiniMonth) {
	if s.miniLoaded && reflect.DeepEqual(m, s.mini) {
		return
	}
	s.mini, s.miniLoaded = m, true
	env := s.page.w.calendarEnv()
	s.miniTitle.SetText(m.Title)
	for i, l := range s.miniHeads {
		if i < len(m.Headers) {
			l.SetText(m.Headers[i])
		}
	}
	for i, d := range m.Days {
		b, n := s.miniDays[i], s.miniNums[i]
		n.SetText(d.Number)
		for _, c := range miniDayClasses {
			b.RemoveCSSClass(c)
			n.RemoveCSSClass(c)
		}
		switch {
		case !d.InMonth:
			n.AddCSSClass("cal-mini-out")
		case d.Weekend:
			n.AddCSSClass("cal-mini-weekend")
		}
		if d.Today {
			n.AddCSSClass("cal-mini-today")
		}
		if d.Shown {
			b.AddCSSClass("cal-mini-band")
			if d.BandStart {
				b.AddCSSClass("cal-mini-band-start")
			}
			if d.BandEnd {
				b.AddCSSClass("cal-mini-band-end")
			}
		}
		label := calendar.LongDateText(d.Date, env)
		b.UpdateProperty([]gtk.AccessibleProperty{gtk.AccessiblePropertyLabel},
			[]coreglib.Value{*coreglib.NewValue(label)})
	}
}

// update shows sb (CalendarController.Sidebar).
func (s *calendarSidebar) update(sb calendarctl.Sidebar) {
	s.setMiniMonth(sb.MiniMonth)
	filter := s.page.ctl.Filter()
	rows := sb
	rows.MiniMonth = calendar.MiniMonth{}
	if s.shown == nil || !reflect.DeepEqual(*s.shown, rows) {
		s.shown = &rows
		s.rebuild(rows)
	}
	s.shownFilter = filter
	s.selecting = true
	if filter == calendarctl.FilterUnanswered && s.unanswered != nil {
		s.list.SelectRow(s.unanswered)
	} else {
		s.list.UnselectAll()
	}
	s.selecting = false
}

// rebuild makes the rows after the mini month for sb.
func (s *calendarSidebar) rebuild(sb calendarctl.Sidebar) {
	tr := i18n.Tr
	s.selecting = true
	s.list.RemoveAll()
	s.selecting = false

	s.list.Append(calendarSidebarHeading(sb.InvitationsTitle, "", false))
	s.unanswered = calendarUnansweredRow(sb.UnansweredTitle, sb.Unanswered)
	s.list.Append(s.unanswered)

	for _, a := range sb.Accounts {
		s.list.Append(calendarSidebarHeading(a.Name, calendarAccountBadge(a), true))
		for _, c := range a.Calendars {
			s.list.Append(s.calendarRow(c))
		}
		if a.StateLine != "" {
			id := a.AccountID
			s.list.Append(calendarStateRow(a.StateLine, a.OffersSignIn, tr, func() { s.page.ctl.SignIn(id) }))
		}
	}
}

// calendarSidebarHeading is a heading row: a section's or an account's
// name, with the kind capsule after an account's (folders.go's headings).
func calendarSidebarHeading(text, badge string, account bool) *gtk.ListBoxRow {
	row := gtk.NewListBoxRow()
	row.SetSelectable(false)
	row.SetActivatable(false)
	row.SetCanFocus(false)
	row.AddCSSClass("cal-sidebar-heading")
	label := gtk.NewLabel(text)
	label.SetUseMarkup(false)
	label.SetXAlign(0)
	label.SetEllipsize(pango.EllipsizeEnd)
	label.AddCSSClass("caption-heading")
	label.AddCSSClass("dim-label")
	box := gtk.NewBox(gtk.OrientationHorizontal, 0)
	if account {
		box.SetMarginTop(folderHeadingGap)
	}
	box.Append(label)
	if badge != "" {
		b := widget.NewKindBadge(badge)
		b.SetMarginStart(6)
		box.Append(b)
		rest := gtk.NewBox(gtk.OrientationHorizontal, 0)
		rest.SetHExpand(true)
		box.Append(rest)
	} else {
		label.SetHExpand(true)
	}
	row.SetChild(box)
	return row
}

// calendarUnansweredRow is Invitations ▸ Unanswered with its count badge
// (none for 0).
func calendarUnansweredRow(text string, count int) *gtk.ListBoxRow {
	row := gtk.NewListBoxRow()
	row.AddCSSClass("cal-unanswered-row")
	box := gtk.NewBox(gtk.OrientationHorizontal, 8)
	box.Append(gtk.NewImageFromIconName("mail-unread-symbolic"))
	label := gtk.NewLabel(text)
	label.SetUseMarkup(false)
	label.SetXAlign(0)
	label.SetHExpand(true)
	label.SetEllipsize(pango.EllipsizeEnd)
	box.Append(label)
	name := text
	if count > 0 {
		n := strconv.Itoa(count)
		badge := gtk.NewLabel(n)
		badge.SetUseMarkup(false)
		badge.SetVAlign(gtk.AlignCenter)
		badge.AddCSSClass("cal-count-badge")
		box.Append(badge)
		name = calendar.Dot(text, n, i18n.Tr)
	}
	row.SetChild(box)
	row.UpdateProperty([]gtk.AccessibleProperty{gtk.AccessiblePropertyLabel},
		[]coreglib.Value{*coreglib.NewValue(name)})
	return row
}

// calendarRow is a calendar's row: a check button (calendar.update
// visible) holding its colour square, its name and its note.
func (s *calendarSidebar) calendarRow(c calendarctl.SidebarCalendar) *gtk.ListBoxRow {
	row := gtk.NewListBoxRow()
	row.SetSelectable(false)
	row.SetActivatable(false)
	row.AddCSSClass("cal-calendar-row")
	check := gtk.NewCheckButton()
	check.SetActive(c.Visible)
	box := gtk.NewBox(gtk.OrientationHorizontal, 6)
	square := gtk.NewBox(gtk.OrientationHorizontal, 0)
	square.SetVAlign(gtk.AlignCenter)
	square.AddCSSClass("cal-swatch")
	square.AddCSSClass("cal-square")
	square.AddCSSClass(s.page.colorClass(c.ID))
	box.Append(square)
	name := gtk.NewLabel(c.Name)
	name.SetUseMarkup(false)
	name.SetXAlign(0)
	name.SetEllipsize(pango.EllipsizeEnd)
	box.Append(name)
	tip := c.Name
	if c.Note != "" {
		note := gtk.NewLabel(c.Note)
		note.SetUseMarkup(false)
		note.SetXAlign(0)
		note.SetEllipsize(pango.EllipsizeEnd)
		note.AddCSSClass("caption")
		note.AddCSSClass("dim-label")
		// The note gives way before the name.
		name.SetWidthChars(6)
		box.Append(note)
		tip = calendar.Dot(c.Name, c.Note, i18n.Tr)
	}
	check.SetChild(box)
	check.SetTooltipText(tip)
	id := c.ID
	check.ConnectToggled(func() { s.page.ctl.SetVisible(id, check.Active()) })
	row.SetChild(check)
	return row
}

// calendarStateRow is an account's state row ("Calendar access needs a
// new sign-in") with Sign In… when offered.
func calendarStateRow(text string, signIn bool, tr calendar.Translator, onSignIn func()) *gtk.ListBoxRow {
	row := gtk.NewListBoxRow()
	row.SetSelectable(false)
	row.SetActivatable(false)
	row.AddCSSClass("cal-state-row")
	col := gtk.NewBox(gtk.OrientationVertical, 4)
	top := gtk.NewBox(gtk.OrientationHorizontal, 6)
	icon := gtk.NewImageFromIconName("dialog-warning-symbolic")
	icon.SetVAlign(gtk.AlignStart)
	icon.AddCSSClass("warning")
	top.Append(icon)
	label := gtk.NewLabel(text)
	label.SetUseMarkup(false)
	label.SetXAlign(0)
	label.SetWrap(true)
	label.SetWrapMode(pango.WrapWordChar)
	label.SetHExpand(true)
	label.AddCSSClass("caption")
	label.AddCSSClass("dim-label")
	top.Append(label)
	col.Append(top)
	if signIn {
		b := gtk.NewButtonWithLabel(calendar.SignIn(tr))
		b.SetHAlign(gtk.AlignStart)
		b.AddCSSClass("pill")
		b.AddCSSClass("cal-sign-in")
		b.ConnectClicked(onSignIn)
		col.Append(b)
	}
	row.SetChild(col)
	return row
}

// calendarAccountBadge is the capsule after an account's heading: CALDAV
// for a CalDAV account, else the mail account's (accountHeaderBadge),
// else the calendar source's provider.
func calendarAccountBadge(a calendarctl.SidebarAccount) string {
	switch {
	case a.Source == api.CalendarSourceCalDAV:
		return caldavBadge
	case a.Account != nil:
		return accountHeaderBadge(*a.Account)
	case a.Source == api.CalendarSourceGraph:
		return microsoftBadge
	case a.Source == api.CalendarSourceGoogle:
		return googleBadge
	}
	return ""
}

// caldavBadge is a calendar-only CalDAV account's capsule (a protocol
// name, never translated; FolderTree.swift caldavBadge).
const caldavBadge = "CALDAV"
