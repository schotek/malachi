// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
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
	// shape is the structural key the rows were built for
	// (sidebarShape); latest the last model update was given, which a
	// coalesced rebuild builds from.
	shape  string
	latest calendarctl.Sidebar
	// rebuildTimer is the pending coalesced rebuild (0 none).
	rebuildTimer glib.SourceHandle
	// The widgets updated in place: the Unanswered row's label and badge,
	// each calendar's check button and note, each account's state row.
	unansweredLabel *gtk.Label
	unansweredBadge *gtk.Label
	calRows         []sidebarCalendarRow
	stateRows       []sidebarStateRow
	// syncing is set while the sidebar sets the check buttons itself.
	syncing bool
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

// sidebarRebuildDelay coalesces the sidebar's structural rebuilds: the
// model changes with every sync retry (notify.calendarState).
const sidebarRebuildDelay = 300 // milliseconds

// sidebarCalendarRow is a calendar's row and its widgets updated in place.
type sidebarCalendarRow struct {
	row   *gtk.ListBoxRow
	check *gtk.CheckButton
	name  *gtk.Label
	note  *gtk.Label
}

// sidebarStateRow is an account's state row (hidden without a state) and
// its widgets updated in place.
type sidebarStateRow struct {
	row    *gtk.ListBoxRow
	label  *gtk.Label
	signIn *gtk.Button
}

// sidebarShape is the structural key of sb: the accounts in order with
// their names and capsules, their calendars in order with ids, names,
// colours and notes. The Unanswered row, the state rows and Add
// Calendar… are always there; what they say is updated in place.
func sidebarShape(sb calendarctl.Sidebar) string {
	var b strings.Builder
	b.WriteString(sb.InvitationsTitle + "\x1f" + sb.UnansweredTitle + "\x1e")
	for _, a := range sb.Accounts {
		fmt.Fprintf(&b, "a%s\x1f%s\x1f%s\x1e", a.AccountID, a.Name, calendarAccountBadge(a))
		for _, c := range a.Calendars {
			fmt.Fprintf(&b, "c%s\x1f%s\x1f%s\x1f%s\x1e", c.ID, c.Name, c.Color, c.Note)
		}
	}
	return b.String()
}

// update shows sb (CalendarController.Sidebar): the rows are rebuilt only
// when sb's structure changed (coalesced, never under a held button),
// everything else is updated in place.
func (s *calendarSidebar) update(sb calendarctl.Sidebar) {
	s.setMiniMonth(sb.MiniMonth)
	s.latest = sb
	key := sidebarShape(sb)
	switch {
	case s.shape == "":
		s.rebuild(sb, key)
	case key != s.shape:
		s.scheduleRebuild()
		// Until then the old rows keep what still fits them.
	}
	s.refresh(s.latest)
}

// scheduleRebuild rebuilds the rows from the latest model after
// sidebarRebuildDelay, later while a pointer button is held.
func (s *calendarSidebar) scheduleRebuild() {
	if s.rebuildTimer != 0 {
		return
	}
	s.rebuildTimer = glib.TimeoutAdd(sidebarRebuildDelay, func() bool {
		if calendarPointerDown() {
			return true
		}
		s.rebuildTimer = 0
		if key := sidebarShape(s.latest); key != s.shape {
			s.rebuild(s.latest, key)
			s.refresh(s.latest)
		}
		return false
	})
}

// calendarPointerDown reports a pointer button held down: a rebuild then
// would destroy the pressed widget.
func calendarPointerDown() bool {
	d := gdk.DisplayGetDefault()
	if d == nil || d.DefaultSeat() == nil {
		return false
	}
	ptr := gdk.BaseSeat(d.DefaultSeat()).Pointer()
	if ptr == nil {
		return false
	}
	const buttons = gdk.Button1Mask | gdk.Button2Mask | gdk.Button3Mask
	return gdk.BaseDevice(ptr).ModifierState()&buttons != 0
}

// refresh updates the rows built for the current shape from sb in place:
// the unanswered count, the check buttons, the state rows and the
// Unanswered row's selection. Rows of a shape not yet rebuilt are matched
// by calendar and account id; what has no row waits for the rebuild.
func (s *calendarSidebar) refresh(sb calendarctl.Sidebar) {
	tr := i18n.Tr
	n := strconv.Itoa(sb.Unanswered)
	s.unansweredBadge.SetText(n)
	s.unansweredBadge.SetVisible(sb.Unanswered > 0)
	name := sb.UnansweredTitle
	if sb.Unanswered > 0 {
		name = calendar.Dot(name, n, tr)
	}
	s.unanswered.UpdateProperty([]gtk.AccessibleProperty{gtk.AccessiblePropertyLabel},
		[]coreglib.Value{*coreglib.NewValue(name)})

	cals := map[api.CalendarID]calendarctl.SidebarCalendar{}
	states := map[api.AccountID]calendarctl.SidebarAccount{}
	for _, a := range sb.Accounts {
		states[a.AccountID] = a
		for _, c := range a.Calendars {
			cals[c.ID] = c
		}
	}
	s.syncing = true
	for _, r := range s.calRows {
		c, ok := cals[api.CalendarID(r.row.Name())]
		if ok && r.check.Active() != c.Visible {
			r.check.SetActive(c.Visible)
		}
	}
	s.syncing = false
	for _, r := range s.stateRows {
		a := states[api.AccountID(r.row.Name())]
		if r.label.Text() != a.StateLine {
			r.label.SetText(a.StateLine)
		}
		r.row.SetVisible(a.StateLine != "")
		r.signIn.SetVisible(a.OffersSignIn)
	}

	s.selecting = true
	if s.page.ctl.Filter() == calendarctl.FilterUnanswered {
		if s.list.SelectedRow() == nil {
			s.list.SelectRow(s.unanswered)
		}
	} else if s.list.SelectedRow() != nil {
		s.list.UnselectAll()
	}
	s.selecting = false
}

// rebuild makes the rows after the mini month for sb, whose shape is key.
func (s *calendarSidebar) rebuild(sb calendarctl.Sidebar, key string) {
	tr := i18n.Tr
	if s.rebuildTimer != 0 {
		glib.SourceRemove(s.rebuildTimer)
		s.rebuildTimer = 0
	}
	s.shape = key
	s.selecting = true
	s.list.RemoveAll()
	s.selecting = false
	s.calRows, s.stateRows = nil, nil

	s.list.Append(calendarSidebarHeading(sb.InvitationsTitle, "", false))
	s.unanswered, s.unansweredLabel, s.unansweredBadge = calendarUnansweredRow(sb.UnansweredTitle)
	s.list.Append(s.unanswered)

	for _, a := range sb.Accounts {
		s.list.Append(calendarSidebarHeading(a.Name, calendarAccountBadge(a), true))
		for _, c := range a.Calendars {
			r := s.calendarRow(c)
			s.calRows = append(s.calRows, r)
			s.list.Append(r.row)
		}
		id := a.AccountID
		st := calendarStateRow(tr, func() { s.page.ctl.SignIn(id) })
		st.row.SetName(string(id))
		s.stateRows = append(s.stateRows, st)
		s.list.Append(st.row)
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

// calendarUnansweredRow is Invitations ▸ Unanswered with its label and
// its count badge (refresh sets the count).
func calendarUnansweredRow(text string) (*gtk.ListBoxRow, *gtk.Label, *gtk.Label) {
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
	badge := gtk.NewLabel("")
	badge.SetUseMarkup(false)
	badge.SetVAlign(gtk.AlignCenter)
	badge.AddCSSClass("cal-count-badge")
	badge.SetVisible(false)
	box.Append(badge)
	row.SetChild(box)
	return row, label, badge
}

// calendarRow is a calendar's row: a check button (calendar.update
// visible) holding its colour square, its name and its note. The row's
// widget name is the calendar's id (refresh matches by it).
func (s *calendarSidebar) calendarRow(c calendarctl.SidebarCalendar) sidebarCalendarRow {
	row := gtk.NewListBoxRow()
	row.SetName(string(c.ID))
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
	note := gtk.NewLabel(c.Note)
	note.SetUseMarkup(false)
	note.SetXAlign(0)
	note.SetEllipsize(pango.EllipsizeEnd)
	note.AddCSSClass("caption")
	note.AddCSSClass("dim-label")
	note.SetVisible(c.Note != "")
	tip := c.Name
	if c.Note != "" {
		// The note gives way before the name.
		name.SetWidthChars(6)
		tip = calendar.Dot(c.Name, c.Note, i18n.Tr)
	}
	box.Append(note)
	check.SetChild(box)
	check.SetTooltipText(tip)
	id := c.ID
	check.ConnectToggled(func() {
		if !s.syncing {
			s.page.ctl.SetVisible(id, check.Active())
		}
	})
	row.SetChild(check)
	return sidebarCalendarRow{row: row, check: check, name: name, note: note}
}

// calendarStateRow is an account's state row ("Calendar access needs a
// new sign-in") with Sign In…, both shown by refresh.
func calendarStateRow(tr calendar.Translator, onSignIn func()) sidebarStateRow {
	row := gtk.NewListBoxRow()
	row.SetSelectable(false)
	row.SetActivatable(false)
	row.SetVisible(false)
	row.AddCSSClass("cal-state-row")
	col := gtk.NewBox(gtk.OrientationVertical, 4)
	top := gtk.NewBox(gtk.OrientationHorizontal, 6)
	icon := gtk.NewImageFromIconName("dialog-warning-symbolic")
	icon.SetVAlign(gtk.AlignStart)
	icon.AddCSSClass("warning")
	top.Append(icon)
	label := gtk.NewLabel("")
	label.SetUseMarkup(false)
	label.SetXAlign(0)
	label.SetWrap(true)
	label.SetWrapMode(pango.WrapWordChar)
	label.SetHExpand(true)
	label.AddCSSClass("caption")
	label.AddCSSClass("dim-label")
	top.Append(label)
	col.Append(top)
	b := gtk.NewButtonWithLabel(calendar.SignIn(tr))
	b.SetHAlign(gtk.AlignStart)
	b.AddCSSClass("pill")
	b.AddCSSClass("cal-sign-in")
	b.SetVisible(false)
	b.ConnectClicked(onSignIn)
	col.Append(b)
	row.SetChild(col)
	return sidebarStateRow{row: row, label: label, signIn: b}
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
