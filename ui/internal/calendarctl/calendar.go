// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendarctl

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/calendardata"
)

// The calendar page of one main window (docs/calendar.md §5.3–5.4): the
// view (Day, Week, Month, Agenda; the key calendar-view), the anchor date
// and its range, the occurrences of the range, the selection, the search
// (from 2 characters, 300 ms after the last keystroke), the Unanswered
// filter (the unanswered invitations as an agenda), the sidebar's model
// and the status bar's texts. The page reads the state after OnChange and
// sends what the user did; what it must present (the detail popover, the
// editor, a re-sign-in) leaves through the hand-off callbacks.
//
// Port of macos MalachiCore/Controllers/CalendarController.swift.

// Filter is what the content shows besides the view.
type Filter int

// The filters.
const (
	FilterNone Filter = iota
	// FilterUnanswered: the unanswered invitations as an agenda (the
	// sidebar's Invitations ▸ Unanswered row).
	FilterUnanswered
)

// SidebarCalendar is a calendar row of the sidebar.
type SidebarCalendar struct {
	ID   api.CalendarID
	Name string
	// Color is "#rrggbb" as the daemon sent it (the CSS checks it again).
	Color   string
	Visible bool
	// Note is "default" / "read only" / "shared · %s"
	// (calendar.CalendarRowNote).
	Note string
}

// SidebarAccount is an account of the sidebar: its header (the page
// picks the kind capsule from Account), its calendars and the state row.
type SidebarAccount struct {
	AccountID api.AccountID
	// Account is the account of account.list; nil when not (yet) known.
	Account   *api.Account
	Name      string
	Source    api.CalendarSource
	Calendars []SidebarCalendar
	// StateLine is the consent or state row ("" none).
	StateLine string
	// OffersSignIn: the state row offers Sign In… (SignIn).
	OffersSignIn bool
}

// Sidebar is the sidebar's model.
type Sidebar struct {
	MiniMonth calendar.MiniMonth
	// InvitationsTitle heads the Invitations section, UnansweredTitle is
	// its row and Unanswered that row's count badge (0: no badge).
	InvitationsTitle string
	UnansweredTitle  string
	Unanswered       int
	Accounts         []SidebarAccount
	// AddCalendar is "Add Calendar…", AddCalDAV and SyncMicrosoft its
	// menu items.
	AddCalendar   string
	AddCalDAV     string
	SyncMicrosoft string
}

// The search and the agenda's paging.
const (
	// SearchMinimum is the number of characters the search starts at.
	SearchMinimum = 2
	// SearchDebounce is how long the search waits after the last change.
	SearchDebounce = 300 * time.Millisecond
	// AgendaPage is the agenda's page in days; LoadMoreAgenda adds one.
	AgendaPage = calendar.AgendaDays
)

// CalendarController is the calendar page's state (CalendarController).
type CalendarController struct {
	// OnChange: the state changed; the page reads it again.
	OnChange func()
	// OnError: a short sentence for a toast (a refused visibility change,
	// a failed load or search).
	OnError func(string)
	// OnOpenDetail: Return on the selection, a click on a block: the
	// detail popover of this occurrence.
	OnOpenDetail func(api.Occurrence)
	// OnRemove: Delete on the selection: the detail's remove flow for
	// this occurrence.
	OnRemove func(api.Occurrence)
	// OnNewEvent: New Event ("+", the key, a double click): the editor
	// with these defaults.
	OnNewEvent func(calendar.Defaults)
	// OnClosePopover: Escape with a popover open: close it.
	OnClosePopover func()
	// OnSignIn: Sign In… of a state row: the account's re-sign-in with
	// calendar access (account.oauthStart calendar: true).
	OnSignIn func(api.AccountID)

	source calendardata.DataSource
	opts   Options

	view        calendar.View
	anchor      time.Time
	miniAnchor  time.Time
	filter      Filter
	occurrences []api.Occurrence
	loading     bool
	selection   string
	searchText  string
	results     []api.Occurrence
	hasResults  bool
	searchTotal int
	agendaPages int

	observer      *board.ObserverToken
	loadGen       int
	searchGen     int
	searchPending bool
}

// NewCalendarController is the page's controller over source, at today in
// the stored view. It does nothing until Start.
func NewCalendarController(source calendardata.DataSource, opts Options) *CalendarController {
	c := &CalendarController{source: source, opts: opts, view: calendar.DefaultView, agendaPages: 1}
	if opts.View != nil {
		c.view, _ = calendar.ParseView(opts.View())
	}
	today := calendar.StartOfDay(opts.Clock(), opts.Loc())
	c.anchor, c.miniAnchor = today, today
	return c
}

// Start follows the source and loads the range.
func (c *CalendarController) Start() {
	if c.observer != nil {
		return
	}
	c.observer = c.source.Observe(c.sourceChanged)
	c.Reload()
}

// Stop stops following the source; no answer is taken any more.
func (c *CalendarController) Stop() {
	if c.observer != nil {
		c.observer.Cancel()
		c.observer = nil
	}
	c.loadGen++
	c.searchGen++
	c.searchPending = false
	c.loading = false
}

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

// View is the view shown.
func (c *CalendarController) View() calendar.View { return c.view }

// Anchor is a day of the shown range (the range is computed from it).
func (c *CalendarController) Anchor() time.Time { return c.anchor }

// MiniMonthAnchor is a day of the month the sidebar shows.
func (c *CalendarController) MiniMonthAnchor() time.Time { return c.miniAnchor }

// Filter is the filter shown.
func (c *CalendarController) Filter() Filter { return c.filter }

// Occurrences are the range's occurrences of the visible calendars.
func (c *CalendarController) Occurrences() []api.Occurrence { return c.occurrences }

// Loading: an event.list is on its way.
func (c *CalendarController) Loading() bool { return c.loading }

// Selection is the occurrence key of the selection (calendar.OccurrenceKey;
// "" none).
func (c *CalendarController) Selection() string { return c.selection }

// SearchText is the search field as typed.
func (c *CalendarController) SearchText() string { return c.searchText }

// SearchResults are event.search's answer for SearchText; false while not
// searching.
func (c *CalendarController) SearchResults() ([]api.Occurrence, bool) { return c.results, c.hasResults }

// SearchTotal is event.search's total.
func (c *CalendarController) SearchTotal() int { return c.searchTotal }

// SearchPending: a search waits for its debounce or its answer.
func (c *CalendarController) SearchPending() bool { return c.searchPending }

// Idle: no load and no search is on its way.
func (c *CalendarController) Idle() bool { return !c.loading && !c.searchPending }

// AgendaPages is the number of the agenda's pages shown.
func (c *CalendarController) AgendaPages() int { return c.agendaPages }

// Snapshot is the source's snapshot.
func (c *CalendarController) Snapshot() calendardata.Snapshot { return c.source.Snapshot() }

// Source is the controller's source.
func (c *CalendarController) Source() calendardata.DataSource { return c.source }

// WeekStart is the first day of the week (the preference, else the
// locale's).
func (c *CalendarController) WeekStart() time.Weekday {
	return calendar.WeekStart(c.Snapshot().Preferences.WeekStart, c.opts.LocaleFirstDay)
}

// Range is the view's range; the agenda's covers its pages.
func (c *CalendarController) Range() calendar.Range {
	loc := c.opts.Loc()
	r := calendar.ViewRange(c.view, c.anchor, c.WeekStart(), loc)
	if c.view == calendar.ViewAgenda && c.agendaPages > 1 {
		r.To = calendar.AddDays(r.From, AgendaPage*c.agendaPages)
	}
	return r
}

// DataRange is Range for the source (in the page's zone).
func (c *CalendarController) DataRange() calendardata.Range {
	return calendardata.RangeIn(c.Range(), c.opts.Loc())
}

// Title is the header's title.
func (c *CalendarController) Title() string { return calendar.Title(c.view, c.Range(), c.opts.Env) }

// Subtitle is the header's subtitle.
func (c *CalendarController) Subtitle() string {
	return calendar.Subtitle(c.view, c.Range(), c.WeekStart(), c.opts.Env)
}

// ShowDeclined is the preference Show Declined Events.
func (c *CalendarController) ShowDeclined() bool { return c.Snapshot().Preferences.ShowDeclined }

// Searching: the search field has a query the search runs for.
func (c *CalendarController) Searching() bool {
	return utf8.RuneCountInString(strings.TrimSpace(c.searchText)) >= SearchMinimum
}

// SelectedOccurrence is the selected occurrence among the shown ones.
func (c *CalendarController) SelectedOccurrence() (api.Occurrence, bool) {
	if c.selection == "" {
		return api.Occurrence{}, false
	}
	for _, o := range c.ShownOccurrences() {
		if calendar.OccurrenceKey(o) == c.selection {
			return o, true
		}
	}
	return api.Occurrence{}, false
}

// OccurrenceOf is the occurrence of an unanswered invitation not in a
// calendar, for the Unanswered agenda (Cal.occurrenceOf).
func OccurrenceOf(i api.InvitationListItem) api.Occurrence {
	return api.Occurrence{AccountID: i.AccountID, Title: i.Summary.Title, Start: i.Summary.Start, End: i.Summary.End,
		StartDate: i.Summary.StartDate, AllDay: i.Summary.AllDay, Status: api.EventStatusConfirmed,
		ShowAs: api.ShowAsTentative, MyResponse: api.ResponseNeedsAction,
		Invitation: &api.MessageRef{AccountID: i.AccountID, MessageID: i.MessageID}}
}

// ShownOccurrences are what the content lists: the search results, the
// unanswered invitations, else the range's occurrences.
func (c *CalendarController) ShownOccurrences() []api.Occurrence {
	if c.hasResults {
		return c.results
	}
	if c.filter == FilterUnanswered {
		inv := c.Snapshot().Invitations
		out := make([]api.Occurrence, 0, len(inv))
		for _, i := range inv {
			out = append(out, OccurrenceOf(i))
		}
		return out
	}
	return c.occurrences
}

// Agenda is the agenda of the Agenda view and of the Unanswered filter;
// false otherwise (the search results are listed in their own order).
func (c *CalendarController) Agenda() ([]calendar.AgendaDay, bool) {
	now := c.opts.Clock()
	loc := c.opts.Loc()
	if c.filter == FilterUnanswered {
		from := calendar.StartOfDay(now, loc)
		return calendar.Agenda(c.ShownOccurrences(), from, api.CalendarFutureDays, now, c.opts.Env, true), true
	}
	if c.view != calendar.ViewAgenda {
		return nil, false
	}
	r := c.Range()
	return calendar.Agenda(c.occurrences, r.From, calendar.DaysBetween(r.From, r.To, loc), now, c.opts.Env,
		c.ShowDeclined()), true
}

// ListHeading is the heading above the list in place of the view
// ("Results", "Invitations"; "" none).
func (c *CalendarController) ListHeading() string {
	switch {
	case c.hasResults:
		return calendar.ResultsHeading(c.opts.Env.Tr)
	case c.filter == FilterUnanswered:
		return calendar.UnansweredHeading(c.opts.Env.Tr)
	}
	return ""
}

// EmptyListText is the text of an empty list ("" when there is something
// to show).
func (c *CalendarController) EmptyListText() string {
	tr := c.opts.Env.Tr
	if c.hasResults {
		if len(c.results) == 0 {
			return calendar.NoResults(tr)
		}
		return ""
	}
	if c.view == calendar.ViewAgenda || c.filter == FilterUnanswered {
		if days, _ := c.Agenda(); len(days) == 0 {
			return calendar.AgendaEmpty(tr)
		}
	}
	return ""
}

// EmptyKind is why the page has nothing to show; false when it has.
func (c *CalendarController) EmptyKind() (calendar.EmptyKind, bool) {
	s := c.Snapshot()
	switch s.Phase {
	case calendardata.PhaseUnavailable, calendardata.PhaseUnsupported:
		if len(s.CalendarAccounts) == 0 {
			return calendar.EmptyNoBackend, true
		}
	case calendardata.PhaseLoading:
		return calendar.EmptyLoading, true
	}
	if len(s.CalendarAccounts) == 0 {
		if s.Phase == calendardata.PhaseFailed {
			return calendar.EmptyNoBackend, true
		}
		return calendar.EmptyNoAccounts, true
	}
	if !slices.ContainsFunc(s.CalendarAccounts, func(a api.CalendarAccount) bool { return a.Sync }) {
		return calendar.EmptySyncOff, true
	}
	return 0, false
}

// Sidebar is the sidebar's model. Disabled calendars are only in
// Preferences → Calendar.
func (c *CalendarController) Sidebar() Sidebar {
	s := c.Snapshot()
	tr := c.opts.Env.Tr
	accounts := []SidebarAccount{}
	for _, a := range s.ActiveCalendarAccounts() {
		cals := make([]SidebarCalendar, 0, len(a.Calendars))
		for _, k := range a.Calendars {
			cals = append(cals, SidebarCalendar{ID: k.ID, Name: board.CleanLine(k.Name, api.MaxEventLocationBytes),
				Color: k.Color, Visible: k.Visible, Note: calendar.CalendarRowNote(k, tr)})
		}
		var line string
		signIn := false
		switch a.State.Status {
		case api.CalendarSyncConsentRequired:
			line, signIn = calendar.ConsentLine(tr), true
		case api.CalendarSyncAuthRequired:
			line, signIn = c.opts.ErrorMessage(&api.Error{Code: api.CodeAuthRequired}, ""), true
		case api.CalendarSyncOffline:
			line = calendar.OfflineLine(tr)
		case api.CalendarSyncError:
			if a.State.Error != nil {
				line = c.opts.ErrorMessage(a.State.Error, "")
			}
		}
		var acc *api.Account
		if x, ok := s.Account(a.AccountID); ok {
			acc = &x
		}
		accounts = append(accounts, SidebarAccount{AccountID: a.AccountID, Account: acc, Name: s.AccountName(a.AccountID),
			Source: a.Source, Calendars: cals, StateLine: line, OffersSignIn: signIn})
	}
	r := c.Range()
	return Sidebar{
		MiniMonth:        calendar.NewMiniMonth(c.miniAnchor, c.WeekStart(), c.opts.Clock(), &r, c.opts.Env),
		InvitationsTitle: calendar.UnansweredHeading(tr), UnansweredTitle: calendar.UnansweredRow(tr),
		Unanswered: s.Unanswered, Accounts: accounts,
		AddCalendar: calendar.AddCalendar(tr), AddCalDAV: calendar.AddCalDAV(tr), SyncMicrosoft: calendar.SyncMicrosoft(tr),
	}
}

// StatusLine is the status bar's left text: the calendars' state.
func (c *CalendarController) StatusLine() string {
	return calendar.StatusLine(c.Snapshot().States(), c.opts.Clock(), c.opts.Env.Tr)
}

// UnansweredLine is the status bar's right text: the unanswered count (""
// for none).
func (c *CalendarController) UnansweredLine() string {
	if n := c.Snapshot().Unanswered; n > 0 {
		return calendar.UnansweredCount(n, c.opts.Env.Tr)
	}
	return ""
}

// ---------------------------------------------------------------------------
// Navigation
// ---------------------------------------------------------------------------

// SetView shows Day, Week, Month or Agenda (stored in calendar-view) and
// ends the Unanswered filter. The Agenda starts at today when switched to;
// every other switch keeps the anchor (calendar.SwitchAnchor).
func (c *CalendarController) SetView(v calendar.View) {
	if v == c.view && c.filter == FilterNone {
		return
	}
	c.anchor = calendar.SwitchAnchor(c.view, v, c.anchor, c.opts.Clock(), c.opts.Loc())
	c.miniAnchor = c.anchor
	c.view = v
	c.filter = FilterNone
	c.agendaPages = 1
	c.storeView()
	c.Reload()
}

func (c *CalendarController) storeView() {
	if c.opts.SetView != nil {
		c.opts.SetView(c.view.Nick())
	}
}

// Today shows the range of today.
func (c *CalendarController) Today() { c.GoTo(c.opts.Clock()) }

// Step is ‹ (dir < 0) or › : the previous or next range.
func (c *CalendarController) Step(dir int) {
	d := 1
	if dir < 0 {
		d = -1
	}
	c.anchor = calendar.Step(c.view, c.anchor, d, c.opts.Loc())
	c.miniAnchor = c.anchor
	c.agendaPages = 1
	c.filter = FilterNone
	c.Reload()
}

// GoTo shows the range of day in the current view (a day of the mini
// month, the Today strip's Open Calendar, a notification).
func (c *CalendarController) GoTo(day time.Time) {
	c.anchor = calendar.StartOfDay(day, c.opts.Loc())
	c.miniAnchor = c.anchor
	c.agendaPages = 1
	c.filter = FilterNone
	c.Reload()
}

// Show shows day in view v and selects key there ("" none): Show in
// Calendar, an alert's click.
func (c *CalendarController) Show(day time.Time, v calendar.View, key string) {
	target := day
	if v == calendar.ViewAgenda {
		target = calendar.SwitchAnchor(c.view, v, day, c.opts.Clock(), c.opts.Loc())
	}
	c.view = v
	c.storeView()
	c.selection = key
	c.GoTo(target)
}

// StepMiniMonth is the mini month's arrows: its month alone.
func (c *CalendarController) StepMiniMonth(dir int) {
	d := 1
	if dir < 0 {
		d = -1
	}
	c.miniAnchor = calendar.StepMonth(c.miniAnchor, d, c.opts.Loc())
	c.changed()
}

// LoadMoreAgenda is the Agenda's next page (scrolling on).
func (c *CalendarController) LoadMoreAgenda() {
	if c.view != calendar.ViewAgenda || c.filter != FilterNone || c.hasResults {
		return
	}
	c.agendaPages++
	c.Reload()
}

// ShowUnanswered is the Unanswered row: the unanswered invitations as an
// agenda.
func (c *CalendarController) ShowUnanswered() {
	c.filter = FilterUnanswered
	c.clearSearchQuietly()
	c.selection = ""
	c.changed()
}

// ClearFilter is back from the Unanswered filter.
func (c *CalendarController) ClearFilter() {
	if c.filter == FilterNone {
		return
	}
	c.filter = FilterNone
	c.changed()
}

// ---------------------------------------------------------------------------
// Calendars
// ---------------------------------------------------------------------------

// SetVisible is a calendar's checkbox (calendar.update visible).
func (c *CalendarController) SetVisible(id api.CalendarID, visible bool) {
	c.source.SetVisible(id, visible)
}

// SignIn is Sign In… of a state row.
func (c *CalendarController) SignIn(id api.AccountID) {
	if c.OnSignIn != nil {
		c.OnSignIn(id)
	}
}

// ---------------------------------------------------------------------------
// Selection
// ---------------------------------------------------------------------------

// Select selects an occurrence by key ("" clears).
func (c *CalendarController) Select(key string) {
	if key == c.selection {
		return
	}
	c.selection = key
	c.changed()
}

// MoveSelection is the arrows: the previous or next occurrence in time
// order.
func (c *CalendarController) MoveSelection(delta int) {
	order := calendar.SelectionOrder(c.ShownOccurrences(), c.opts.Loc())
	c.Select(calendar.MoveSelection(order, c.selection, delta))
}

// OpenDetail opens the detail of an occurrence (a click, Return).
func (c *CalendarController) OpenDetail(o api.Occurrence) {
	c.Select(calendar.OccurrenceKey(o))
	if c.OnOpenDetail != nil {
		c.OnOpenDetail(o)
	}
}

// ---------------------------------------------------------------------------
// New events
// ---------------------------------------------------------------------------

// NewEvent is New Event from the "+" or the key: the next half hour from
// now.
func (c *CalendarController) NewEvent() { c.NewEventAt(c.opts.Clock(), calendar.OriginNow) }

// NewEventAt is New Event at a time or day the user double-clicked.
func (c *CalendarController) NewEventAt(at time.Time, origin calendar.Origin) {
	if c.OnNewEvent != nil {
		c.OnNewEvent(c.NewEventDefaults(at, origin))
	}
}

// NewEventDefaults are a new event's defaults for a start at at from
// origin.
func (c *CalendarController) NewEventDefaults(at time.Time, origin calendar.Origin) calendar.Defaults {
	return calendar.NewEventDefaults(at, origin, c.Snapshot().Preferences, c.opts.Loc())
}

// ---------------------------------------------------------------------------
// Search
// ---------------------------------------------------------------------------

// SetSearch is the search field's change: event.search SearchDebounce
// after the last change from SearchMinimum characters; fewer end the
// search.
func (c *CalendarController) SetSearch(text string) {
	if text == c.searchText {
		return
	}
	c.searchText = text
	c.searchGen++
	c.searchPending = false
	q := strings.TrimSpace(text)
	if utf8.RuneCountInString(q) < SearchMinimum {
		if c.hasResults {
			c.results, c.hasResults, c.searchTotal = nil, false, 0
		}
		c.changed()
		return
	}
	gen := c.searchGen
	c.searchPending = true
	c.opts.After(SearchDebounce, func() {
		if gen != c.searchGen {
			return
		}
		c.source.Search(api.EventSearchParams{Query: q, Limit: api.MaxEventSearchResults},
			func(r *api.EventSearchResult, err *api.Error) {
				if gen != c.searchGen {
					return
				}
				c.searchPending = false
				c.hasResults = true
				if err != nil || r == nil {
					c.results, c.searchTotal = []api.Occurrence{}, 0
					c.reportError(err)
				} else {
					c.results, c.searchTotal = r.Occurrences, r.Total
					if c.results == nil {
						c.results = []api.Occurrence{}
					}
					c.selection = ""
				}
				c.changed()
			})
	})
	c.changed()
}

// ClearSearch is Escape after the popover, or the field's clear button.
func (c *CalendarController) ClearSearch() {
	if c.searchText == "" && !c.hasResults {
		return
	}
	c.clearSearchQuietly()
	c.changed()
}

func (c *CalendarController) clearSearchQuietly() {
	c.searchGen++
	c.searchPending = false
	c.searchText = ""
	c.results, c.hasResults, c.searchTotal = nil, false, 0
}

// ---------------------------------------------------------------------------
// Keys
// ---------------------------------------------------------------------------

// HandleKey is a key in Calendar mode (calendar.KeyFor); true when it was
// the calendar's. Show Calendar is the window's and returns false. inText:
// the keyboard is in a text field; popoverOpen: the detail (or another
// popup) is open.
func (c *CalendarController) HandleKey(p calendar.Press, inText, popoverOpen bool) bool {
	_, selected := c.SelectedOccurrence()
	ctx := calendar.KeyContext{Mode: board.ModeCalendar, InText: inText, Selected: selected}
	action, ok := calendar.KeyFor(p, ctx, c.opts.Env.Tr)
	if !ok {
		return false
	}
	switch action {
	case calendar.KeyShowCalendar:
		return false
	case calendar.KeyToday:
		c.Today()
	case calendar.KeyPrevious:
		c.Step(-1)
	case calendar.KeyNext:
		c.Step(1)
	case calendar.KeyDayView:
		c.SetView(calendar.ViewDay)
	case calendar.KeyWeekView:
		c.SetView(calendar.ViewWeek)
	case calendar.KeyMonthView:
		c.SetView(calendar.ViewMonth)
	case calendar.KeyAgendaView:
		c.SetView(calendar.ViewAgenda)
	case calendar.KeyNewEvent:
		c.NewEvent()
	case calendar.KeyOpenDetail:
		o, ok := c.SelectedOccurrence()
		if !ok {
			return false
		}
		if c.OnOpenDetail != nil {
			c.OnOpenDetail(o)
		}
	case calendar.KeyRemove:
		o, ok := c.SelectedOccurrence()
		if !ok {
			return false
		}
		if c.OnRemove != nil {
			c.OnRemove(o)
		}
	case calendar.KeyEscape:
		switch calendar.EscapeFor(popoverOpen, c.searchText != "" || c.hasResults) {
		case calendar.EscapeClosePopover:
			if c.OnClosePopover != nil {
				c.OnClosePopover()
			}
		case calendar.EscapeClearSearch:
			c.ClearSearch()
		default:
			if c.filter == FilterNone {
				return false
			}
			c.ClearFilter()
		}
	case calendar.KeySelectPrevious:
		c.MoveSelection(-1)
	case calendar.KeySelectNext:
		c.MoveSelection(1)
	}
	return true
}

// ---------------------------------------------------------------------------
// Loading
// ---------------------------------------------------------------------------

// Reload loads the range's occurrences (the cached ones show at once).
func (c *CalendarController) Reload() {
	c.loadGen++
	gen := c.loadGen
	r := c.DataRange()
	if hit, ok := c.source.CachedOccurrences(r); ok {
		c.take(hit)
	}
	c.loading = true
	c.changed()
	c.source.Occurrences(r, func(occ []api.Occurrence, err *api.Error) {
		if gen != c.loadGen {
			return
		}
		c.loading = false
		if err != nil {
			c.reportError(err)
		} else {
			c.take(occ)
		}
		c.changed()
	})
}

// take shows the occurrences of the visible calendars; a selection no
// longer among them is dropped.
func (c *CalendarController) take(occ []api.Occurrence) {
	c.occurrences = c.visibleOnly(occ)
	if c.selection != "" && c.filter == FilterNone && !c.hasResults &&
		!slices.ContainsFunc(c.occurrences, func(o api.Occurrence) bool { return calendar.OccurrenceKey(o) == c.selection }) {
		c.selection = ""
	}
}

func (c *CalendarController) visibleOnly(occ []api.Occurrence) []api.Occurrence {
	visible := c.Snapshot().VisibleCalendars()
	out := make([]api.Occurrence, 0, len(occ))
	for _, o := range occ {
		if o.CalendarID == "" || visible[o.CalendarID] {
			out = append(out, o)
		}
	}
	return out
}

func (c *CalendarController) sourceChanged(ch calendardata.Change) {
	switch ch.Kind {
	case calendardata.ChangeSnapshot:
		// A calendar shown or hidden: the cached list without it at once,
		// the daemon's answer after its notification.
		if hit, ok := c.source.CachedOccurrences(c.DataRange()); ok {
			c.take(hit)
		} else {
			c.occurrences = c.visibleOnly(c.occurrences)
		}
		c.changed()
	case calendardata.ChangeOccurrences:
		r := c.Range()
		if ch.Overlaps(r.From, r.To) {
			c.Reload()
		}
	case calendardata.ChangeError:
		c.reportError(ch.Err)
	}
}

func (c *CalendarController) reportError(e *api.Error) {
	if c.OnError == nil {
		return
	}
	if e == nil {
		e = &api.Error{Code: api.CodeInternalError}
	}
	c.OnError(c.opts.ErrorMessage(e, ""))
}

func (c *CalendarController) changed() {
	if c.OnChange != nil {
		c.OnChange()
	}
}
