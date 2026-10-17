// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"os"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/calendarctl"
	"github.com/schotek/malachi/ui/internal/calendardata"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/style"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The window's third mode, Calendar (docs/calendar.md §5.3, §6.5 G9): the
// page shell and its glue. This file builds the page (calendar_page.blp)
// on its first show, owns the window's calendar source (shared with
// Preferences → Calendar and, later, the board's Today strip and the
// invitation cards), the page's CalendarController and its header bar,
// and the mode's hooks (enterCalendar, leaveCalendar, calendarKey). The
// views fill the page's bins in files of their own, one task each:
// calendar_sidebar.go, calendar_week.go (Week and Day), calendar_month.go,
// calendar_agenda.go (Agenda, the Unanswered filter and the search
// results); the detail popover is calendar_detail.go, the editor
// event_editor.go, the CalDAV sheet calendar_caldav.go, the assistant
// calendar_assistant.go, the notifications calendar_notify.go and
// calendar_alerts.go, the keys calendar_keys.go. The macOS client leads
// (MainWindowController+Calendar.swift, CalendarPageViewController.swift,
// CalendarToolbar.swift).

// calendarSamplesEnv is MALACHI_CALENDAR_SAMPLES; calendarSamples is
// whether it was "1" when the program started (read once, like the
// board's): the window's calendar is then the invented sample calendar,
// never the daemon's.
const calendarSamplesEnv = "MALACHI_CALENDAR_SAMPLES"

var calendarSamples = os.Getenv(calendarSamplesEnv) == "1"

// calendarStatusTick is how often the status strip's "synced … ago"
// moves while the Calendar shows.
const calendarStatusTick = 60 * time.Second

// windowCalendar is the window's calendar: its page, built on the first
// switch to the Calendar (ensureCalendar), and its source, made on the
// first ask (calendarSource); daemon is the source when it is the
// daemon's (notify.accountsChanged and the connection reach it,
// calendar_notify.go).
type windowCalendar struct {
	page   *calendarPage
	source calendardata.DataSource
	daemon *calendardata.DaemonSource
	// alerts are the events' alerts (calendar_alerts.go), made on the
	// first alert or notification action.
	alerts *calendarctl.CalendarAlertsController
	// detail is the event detail popover, built on its first show
	// (calendar_detail.go); reveal is Show Invitation in Mail's wait while
	// the board's page, whose wait leaving Mail drops, does not exist.
	detail *eventDetail
	reveal revealState
}

// calendarPage is the Calendar's page, its controller and its views,
// built once by ensureCalendar.
type calendarPage struct {
	w   *Window
	ctl *calendarctl.CalendarController

	// root is calendar_page_root (Adw.BreakpointBin): what goes into
	// window.blp's calendar_page_bin.
	root *adw.BreakpointBin

	header          *adw.HeaderBar
	modeSwitch      *adw.ToggleGroup
	sidebarToggle   *gtk.ToggleButton
	today           *gtk.Button
	previous        *gtk.Button
	next            *gtk.Button
	viewSwitch      *adw.ToggleGroup
	viewMenu        *gtk.DropDown
	title           *adw.WindowTitle
	menuButton      *gtk.MenuButton
	assistantButton *gtk.Button
	newEvent        *gtk.Button
	searchToggle    *gtk.ToggleButton
	searchBar       *gtk.SearchBar
	searchEntry     *gtk.SearchEntry

	split *adw.OverlaySplitView
	// The bins the views fill (calendar_page.blp).
	sidebarBin, weekBin, dayBin, monthBin, agendaBin, searchBin *adw.Bin

	stack       *gtk.Stack
	banner      *adw.Banner
	empty       *adw.StatusPage
	emptyButton *gtk.Button

	statusButton *gtk.MenuButton
	statusLabel  *gtk.Label
	statusNote   *gtk.Label
	statusTimer  glib.SourceHandle

	sidebar *calendarSidebar
	week    *calendarWeek
	day     *calendarWeek
	month   *calendarMonth
	agenda  *calendarAgenda

	// settingView and settingSearch guard the view switch's and the
	// search entry's handlers against the page's own writes.
	settingView   bool
	settingSearch bool
	// searchShown is the controller's search text as apply last saw it.
	searchShown string
	// started: the controller follows the source (the first entry).
	started bool
	// colors are the calendars' colours in index order as last loaded
	// (style.ApplyCalendar); colorIndex the index of each calendar.
	colors     []string
	colorIndex map[api.CalendarID]int
	// pendingDetail is the occurrence key whose detail opens once the
	// range it is in has loaded (showInCalendar).
	pendingDetail string
}

// calendarEnv is how the calendar writes its texts with dates: the
// application's translations, the locale's strftime, the local zone.
func (w *Window) calendarEnv() calendar.Env {
	return calendar.Env{Tr: i18n.Tr, Format: widget.Strftime, Loc: time.Local}
}

// calendarOptions are the options of every calendar controller of the
// window.
func (w *Window) calendarOptions() calendarctl.Options {
	o := calendarctl.Options{
		Env: w.calendarEnv(), Loop: glibLoop{}, LocaleFirstDay: w.dateWeekStart, RPCText: widget.RPCErrorText,
	}
	if st := w.settings; st != nil {
		o.View, o.SetView = st.CalendarView, st.SetCalendarView
	}
	return o
}

// calendarSource is the window's calendar source, made and started on the
// first ask: the samples with MALACHI_CALENDAR_SAMPLES=1, else the
// daemon's over the window's client. One for the window: the page, the
// Preferences and the invitation cards share it, and the daemon's
// notifications reach it once (calendar_notify.go).
func (w *Window) calendarSource() calendardata.DataSource {
	if w.cal.source != nil {
		return w.cal.source
	}
	if calendarSamples || w.client == nil {
		w.cal.source = calendardata.NewSampleSource(time.Now(), time.Local, glibLoop{})
		return w.cal.source
	}
	d := calendardata.NewDaemonSource(w.client, glibLoop{}, calendardata.DaemonOptions{Loc: time.Local, Log: w.log})
	w.cal.daemon = d
	w.cal.source = d
	d.Start()
	return d
}

// CalendarSource is calendarSource for main.go (Preferences → Calendar).
func (w *Window) CalendarSource() calendardata.DataSource { return w.calendarSource() }

// ensureCalendar builds the Calendar's page and its controller the first
// time the Calendar shows. A second call does nothing.
func (w *Window) ensureCalendar() {
	if w.cal.page != nil {
		return
	}
	p := &calendarPage{w: w, colorIndex: map[api.CalendarID]int{}}
	p.ctl = calendarctl.NewCalendarController(w.calendarSource(), w.calendarOptions())
	p.bind(data.Builder("calendar_page.ui"))
	w.cal.page = p
	p.wire()
	p.sidebar = newCalendarSidebar(p)
	p.week = newCalendarWeek(p, false)
	p.day = newCalendarWeek(p, true)
	p.month = newCalendarMonth(p)
	p.agenda = newCalendarAgenda(p)
	p.wireController()
	if w.calendarPageBin != nil {
		w.calendarPageBin.SetChild(p.root)
	}
	w.wireCalendarKeys()
}

// bind takes the page's widgets from its builder.
func (p *calendarPage) bind(b *gtk.Builder) {
	get := func(id string) any { return b.GetObject(id).Cast() }
	p.root = get("calendar_page_root").(*adw.BreakpointBin)
	p.header = get("calendar_header").(*adw.HeaderBar)
	p.modeSwitch = get("calendar_mode_switch").(*adw.ToggleGroup)
	p.sidebarToggle = get("calendar_sidebar_toggle").(*gtk.ToggleButton)
	p.today = get("calendar_today").(*gtk.Button)
	p.previous = get("calendar_previous").(*gtk.Button)
	p.next = get("calendar_next").(*gtk.Button)
	p.viewSwitch = get("calendar_view_switch").(*adw.ToggleGroup)
	p.viewMenu = get("calendar_view_menu").(*gtk.DropDown)
	p.title = get("calendar_title").(*adw.WindowTitle)
	p.menuButton = get("calendar_menu_button").(*gtk.MenuButton)
	p.assistantButton = get("calendar_assistant_button").(*gtk.Button)
	p.newEvent = get("calendar_new_event").(*gtk.Button)
	p.searchToggle = get("calendar_search_toggle").(*gtk.ToggleButton)
	p.searchBar = get("calendar_search_bar").(*gtk.SearchBar)
	p.searchEntry = get("calendar_search_entry").(*gtk.SearchEntry)
	p.split = get("calendar_split").(*adw.OverlaySplitView)
	p.sidebarBin = get("sidebar_bin").(*adw.Bin)
	p.weekBin = get("week_bin").(*adw.Bin)
	p.dayBin = get("day_bin").(*adw.Bin)
	p.monthBin = get("month_bin").(*adw.Bin)
	p.agendaBin = get("agenda_bin").(*adw.Bin)
	p.searchBin = get("search_bin").(*adw.Bin)
	p.stack = get("calendar_stack").(*gtk.Stack)
	p.banner = get("calendar_banner").(*adw.Banner)
	p.empty = get("calendar_empty").(*adw.StatusPage)
	p.emptyButton = get("calendar_empty_button").(*gtk.Button)
	p.statusButton = get("calendar_status_button").(*gtk.MenuButton)
	p.statusLabel = get("calendar_status_label").(*gtk.Label)
	p.statusNote = get("calendar_status_note").(*gtk.Label)
}

// wire gives the header bar its texts and connects its controls.
func (p *calendarPage) wire() {
	w, tr := p.w, i18n.Tr
	wireModeSwitch(w, p.modeSwitch)
	w.settingModeSwitch = true
	p.modeSwitch.SetActiveName(w.mode.Nick())
	w.settingModeSwitch = false
	if t := p.modeSwitch.ToggleByName(board.ModeBoard.Nick()); t != nil {
		t.SetEnabled(w.modeSwitchBoardOn)
	}

	p.today.SetLabel(calendar.Today(tr))
	p.today.ConnectClicked(p.ctl.Today)
	p.previous.SetTooltipText(calendar.Previous(tr))
	p.previous.ConnectClicked(func() { p.ctl.Step(-1) })
	p.next.SetTooltipText(calendar.Next(tr))
	p.next.ConnectClicked(func() { p.ctl.Step(1) })
	for _, v := range calendar.Views {
		if t := p.viewSwitch.ToggleByName(v.Nick()); t != nil {
			t.SetLabel(calendar.ViewTitle(v, tr))
		}
	}
	p.viewSwitch.NotifyProperty("active-name", func() {
		if p.settingView {
			return
		}
		if v, ok := calendar.ParseView(p.viewSwitch.ActiveName()); ok {
			p.ctl.SetView(v)
		}
	})
	titles := make([]string, len(calendar.Views))
	for i, v := range calendar.Views {
		titles[i] = calendar.ViewTitle(v, tr)
	}
	p.viewMenu.SetModel(gtk.NewStringList(titles))
	p.viewMenu.NotifyProperty("selected", func() {
		i := int(p.viewMenu.Selected())
		if p.settingView || i < 0 || i >= len(calendar.Views) {
			return
		}
		p.ctl.SetView(calendar.Views[i])
	})

	p.newEvent.SetTooltipText(calendar.NewEvent(tr))
	p.newEvent.ConnectClicked(p.ctl.NewEvent)
	p.assistantButton.SetTooltipText(assistant.PanelTexts(tr).Show)
	p.assistantButton.ConnectClicked(w.revealCalendarAssistant)
	if w.assist != nil {
		// The main window lives as long as the application: no unbinding.
		w.assist.OnChange(p.syncAssistant)
	}
	p.syncAssistant()

	p.searchToggle.SetTooltipText(calendar.SearchEvents(tr))
	p.searchEntry.SetPlaceholderText(calendar.SearchEvents(tr))
	p.searchEntry.ConnectSearchChanged(func() {
		if !p.settingSearch {
			p.ctl.SetSearch(p.searchEntry.Text())
		}
	})
	p.searchEntry.ConnectStopSearch(func() {
		p.ctl.ClearSearch()
		p.searchBar.SetSearchMode(false)
	})
	p.searchBar.NotifyProperty("search-mode-enabled", func() {
		if !p.searchBar.SearchMode() && !p.settingSearch {
			p.ctl.ClearSearch()
		}
	})

	p.menuButton.SetTooltipText(mainMenuTooltip())
	if m := w.mainMenu; m != nil {
		p.menuButton.SetMenuModel(m)
	}
	p.emptyButton.SetLabel(calendar.OpenPreferences(tr))
}

// syncAssistant shows ✦ while the assistant panel may be shown.
func (p *calendarPage) syncAssistant() {
	p.assistantButton.SetVisible(p.w.assist != nil && p.w.assist.panelShown())
}

// wireController connects the controller's callbacks to the window.
func (p *calendarPage) wireController() {
	w := p.w
	p.ctl.OnChange = p.apply
	p.ctl.OnError = w.Toast
	p.ctl.OnOpenDetail = func(o api.Occurrence) { w.showEventDetail(o, p.anchorFor(calendar.OccurrenceKey(o))) }
	p.ctl.OnRemove = w.removeCalendarOccurrence
	p.ctl.OnNewEvent = func(d calendar.Defaults) { w.openEventEditor(&d, "") }
	p.ctl.OnClosePopover = w.closeEventDetail
	p.ctl.OnSignIn = w.calendarSignIn
}

// start makes the controller follow the source, once.
func (p *calendarPage) start() {
	if p.started {
		return
	}
	p.started = true
	p.ctl.Start()
}

// enterCalendar runs as setMode switches to the Calendar, before the mode
// stack shows its page: the page is built on first use, the controller
// started, the status strip and its timer taken over.
func (w *Window) enterCalendar() {
	w.ensureCalendar()
	p := w.cal.page
	p.start()
	p.apply()
	if p.statusTimer == 0 {
		p.statusTimer = glib.TimeoutAdd(uint(calendarStatusTick/time.Millisecond), func() bool {
			p.applyStatus()
			return true
		})
	}
}

// leaveCalendar runs as setMode leaves the Calendar for another mode: the
// detail closes and the status timer stops; the controller keeps its
// state for the next entry.
func (w *Window) leaveCalendar() {
	p := w.cal.page
	if p == nil {
		return
	}
	w.closeEventDetail()
	w.returnCalendarAssistant()
	p.pendingDetail = ""
	if p.statusTimer != 0 {
		glib.SourceRemove(p.statusTimer)
		p.statusTimer = 0
	}
}

// calendarKey is a view key of the Calendar's that mode_keys.go read by
// its physical key (calendar.KeyFor in Calendar mode: Ctrl+Shift+1–4).
func (w *Window) calendarKey(k calendar.KeyAction) {
	p := w.cal.page
	if p == nil || w.mode != board.ModeCalendar {
		return
	}
	switch k {
	case calendar.KeyDayView:
		p.ctl.SetView(calendar.ViewDay)
	case calendar.KeyWeekView:
		p.ctl.SetView(calendar.ViewWeek)
	case calendar.KeyMonthView:
		p.ctl.SetView(calendar.ViewMonth)
	case calendar.KeyAgendaView:
		p.ctl.SetView(calendar.ViewAgenda)
	}
}

// showInCalendar is Show in Calendar (an invitation card, an alert's
// click, the Today strip): the window in Calendar mode at day (zero: today)
// in the Day view, the occurrence of eventID (rec its instance) selected
// and its detail opened once the day has loaded; eventID "" shows the day
// alone.
func (w *Window) showInCalendar(eventID api.EventID, rec api.DateOrTime, day time.Time) {
	w.Present()
	w.setMode(board.ModeCalendar)
	p := w.cal.page
	if w.mode != board.ModeCalendar || p == nil {
		return
	}
	if day.IsZero() {
		day = time.Now()
	}
	key := ""
	if eventID != "" {
		key = calendar.OccurrenceKey(api.Occurrence{EventID: eventID, RecurrenceID: rec})
	}
	p.pendingDetail = key
	p.ctl.Show(day, calendar.ViewDay, key)
	p.openPendingDetail()
}

// openPendingDetail opens showInCalendar's detail once the controller has
// the range: the selected occurrence, anchored to its view; nothing when
// the range has loaded without it.
func (p *calendarPage) openPendingDetail() {
	if p.pendingDetail == "" {
		return
	}
	if o, ok := p.ctl.SelectedOccurrence(); ok && calendar.OccurrenceKey(o) == p.pendingDetail {
		key := p.pendingDetail
		p.pendingDetail = ""
		// After the view laid the day out.
		glib.IdleAdd(func() {
			if p.w.mode == board.ModeCalendar {
				p.w.showEventDetail(o, p.anchorFor(key))
			}
		})
		return
	}
	if !p.ctl.Loading() {
		p.pendingDetail = ""
	}
}

// anchorFor is the widget showing the occurrence key in the visible view,
// for a popover's anchor; the content stack when no view shows it.
func (p *calendarPage) anchorFor(key string) gtk.Widgetter {
	var a gtk.Widgetter
	switch p.stack.VisibleChildName() {
	case "week":
		a = p.week.anchorFor(key)
	case "day":
		a = p.day.anchorFor(key)
	case "month":
		a = p.month.anchorFor(key)
	case "agenda", "search":
		a = p.agenda.anchorFor(key)
	}
	if a == nil {
		return p.stack
	}
	return a
}

// apply is the controller's OnChange: the header, the content, the
// sidebar, the views, the colours and the status strip follow.
func (p *calendarPage) apply() {
	c := p.ctl
	p.applyColors()
	p.settingView = true
	p.viewSwitch.SetActiveName(c.View().Nick())
	p.viewMenu.SetSelected(uint(c.View()))
	p.settingView = false
	p.title.SetTitle(c.Title())
	p.title.SetSubtitle(c.Subtitle())
	// The entry is the user's while they type (its search-changed comes
	// later than the keys); only a search the controller ended (Escape,
	// a view's own clear) empties it.
	if q := c.SearchText(); q != p.searchShown {
		p.searchShown = q
		if q == "" && p.searchEntry.Text() != "" {
			p.settingSearch = true
			p.searchEntry.SetText("")
			p.settingSearch = false
		}
	}
	p.stack.SetVisibleChildName(p.page())
	if kind, ok := c.EmptyKind(); ok {
		p.empty.SetTitle(calendar.EmptyTitle(kind, i18n.Tr))
		p.empty.SetDescription(calendar.EmptyBody(kind, i18n.Tr))
		p.emptyButton.SetVisible(kind == calendar.EmptySyncOff || kind == calendar.EmptyNoAccounts)
	}
	p.applyBanner()
	p.sidebar.update(c.Sidebar())
	p.week.update()
	p.day.update()
	p.month.update()
	p.agenda.update()
	p.applyStatus()
	p.openPendingDetail()
}

// page is the content stack's page for the controller's state: the empty
// state, the search results, the Agenda (also the Unanswered filter), else
// the view.
func (p *calendarPage) page() string {
	c := p.ctl
	switch {
	case p.emptyShown():
		return "empty"
	case c.Searching():
		return "search"
	case c.Filter() != calendarctl.FilterNone:
		return calendar.ViewAgenda.Nick()
	}
	return c.View().Nick()
}

// emptyShown: the page has nothing to show (calendarctl EmptyKind).
func (p *calendarPage) emptyShown() bool {
	_, ok := p.ctl.EmptyKind()
	return ok
}

// applyBanner reveals the banner while calendars the page has cannot be
// reached: the source has accounts but its last load failed.
func (p *calendarPage) applyBanner() {
	s := p.ctl.Snapshot()
	offline := len(s.CalendarAccounts) > 0 &&
		(s.Phase == calendardata.PhaseFailed || s.Phase == calendardata.PhaseUnavailable)
	if offline {
		p.banner.SetTitle(calendar.OfflineLine(i18n.Tr))
	}
	p.banner.SetRevealed(offline)
}

// applyColors loads the calendars' colours into the calendar's stylesheet
// when they changed: every calendar of calendar.list, disabled ones
// included, in its order, keeps its index while the list keeps its order.
func (p *calendarPage) applyColors() {
	var colors []string
	index := make(map[api.CalendarID]int, len(p.colorIndex))
	for _, a := range p.ctl.Snapshot().CalendarAccounts {
		for _, k := range a.Calendars {
			if _, ok := index[k.ID]; ok {
				continue
			}
			index[k.ID] = len(colors)
			colors = append(colors, k.Color)
		}
	}
	p.colorIndex = index
	if sameStrings(colors, p.colors) && p.colors != nil {
		return
	}
	p.colors = colors
	style.ApplyCalendar(colors)
}

// sameStrings reports equal slices.
func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// colorClass is the CSS class carrying calendar id's colour
// (style.CalendarClass): the views add it next to cal-fill, cal-bar,
// cal-swatch, cal-unanswered or cal-tentative. An unknown calendar gets the
// palette's first colour.
func (p *calendarPage) colorClass(id api.CalendarID) string {
	return style.CalendarClass(p.colorIndex[id])
}

// calendarSignIn is a calendar consent row's Sign In…: the account's
// sign-in in the browser with the calendar scopes (account.oauthStart
// calendar: true). The daemon completes it by itself; its
// notify.accountsChanged and notify.calendarState reach the source.
func (w *Window) calendarSignIn(id api.AccountID) {
	if calendarSamples || w.client == nil {
		w.Toast(calendar.PreviewOnly(i18n.Tr))
		return
	}
	w.startBrowserSignIn(id, "", true)
}

// signInInBrowser is the mail account's sign-in in the browser
// (startBrowserSignIn without the calendar scopes; sync.go).
func (w *Window) signInInBrowser(id api.AccountID, fallback string) {
	w.startBrowserSignIn(id, fallback, false)
}

// applyStatus is the Calendar's status strip: the calendars' state on the
// left (calendar.StatusLine), the unanswered count as its note.
func (p *calendarPage) applyStatus() {
	p.statusLabel.SetUseMarkup(false)
	p.statusLabel.SetLabel(p.ctl.StatusLine())
	note := p.ctl.UnansweredLine()
	p.statusNote.SetUseMarkup(false)
	p.statusNote.SetLabel(note)
	p.statusNote.SetVisible(note != "")
}
