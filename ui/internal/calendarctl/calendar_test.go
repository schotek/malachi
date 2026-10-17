// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendarctl

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/calendardata"
)

// The calendar page's controller (calendar.go) over the in-memory source
// with the invented samples: the views and their ranges per week start,
// the navigation, the stored view, the search with its debounce on a loop
// the test fires, the selection and its keys, the sidebar with the consent
// row, the Unanswered filter, the empty states. Port of
// CalendarControllerTests.swift. The helpers carry a page prefix so that
// the other controllers' tests in this package keep their own.

// pageTr is the identity translator: the msgid, the singular for n == 1.
type pageTr struct{}

func (pageTr) T(msgid string) string { return msgid }
func (pageTr) N(msgid, plural string, n int) string {
	if n == 1 {
		return msgid
	}
	return plural
}
func (pageTr) C(_, msgid string) string { return msgid }

// pageLoop holds what is posted until run and the debounces until fire.
type pageLoop struct {
	posted []func()
	timers []time.Duration
	later  []func()
}

func (l *pageLoop) Post(f func()) { l.posted = append(l.posted, f) }
func (l *pageLoop) After(d time.Duration, f func()) {
	l.timers = append(l.timers, d)
	l.later = append(l.later, f)
}

// run runs everything posted, also what that posts.
func (l *pageLoop) run() {
	for len(l.posted) > 0 {
		f := l.posted[0]
		l.posted = l.posted[1:]
		f()
	}
}

// fire runs the debounces waiting, then what they posted.
func (l *pageLoop) fire() {
	fs := l.later
	l.later, l.timers = nil, nil
	for _, f := range fs {
		f()
	}
	l.run()
}

var (
	pageZone = func() *time.Location {
		l, err := time.LoadLocation("Europe/Prague")
		if err != nil {
			panic(err)
		}
		return l
	}()
	// pageNow is Wednesday 14 October 2026, 10:17 in Prague.
	pageNow = time.Date(2026, 10, 14, 10, 17, 0, 0, pageZone)
	pageEnv = calendar.Env{Tr: pageTr{}, Format: calendar.EnglishStrftime, Loc: pageZone}
)

func pageDay(m time.Month, d, h, mi int) time.Time {
	return time.Date(2026, m, d, h, mi, 0, 0, pageZone)
}

func pageSamples(l *pageLoop) *calendardata.InMemorySource {
	s := calendardata.NewInMemorySource(calendar.SampleData(pageNow, pageZone), pageZone, l)
	s.EmulatesWrites = true
	return s
}

// pageView is a stored calendar-view.
type pageView struct{ nick string }

func pageOptions(l *pageLoop, first time.Weekday, store *pageView) Options {
	o := Options{Env: pageEnv, Now: func() time.Time { return pageNow }, Loop: l, LocaleFirstDay: first}
	if store != nil {
		o.View = func() string { return store.nick }
		o.SetView = func(n string) { store.nick = n }
	}
	return o
}

// pageStarted is a started controller with its first load answered.
func pageStarted(src calendardata.DataSource, l *pageLoop, o Options) *CalendarController {
	c := NewCalendarController(src, o)
	c.Start()
	l.run()
	return c
}

func pageController(t *testing.T) (*CalendarController, *calendardata.InMemorySource, *pageLoop) {
	t.Helper()
	l := &pageLoop{}
	src := pageSamples(l)
	return pageStarted(src, l, pageOptions(l, time.Monday, nil)), src, l
}

func pageCount(src *calendardata.InMemorySource, method string) int {
	n := 0
	for _, r := range src.Reads() {
		if r == method {
			n++
		}
	}
	return n
}

func titles(os []api.Occurrence) []string {
	out := []string{}
	for _, o := range os {
		out = append(out, o.Title)
	}
	return out
}

func TestWeekRangeFollowsTheWeekStart(t *testing.T) {
	c, _, _ := pageController(t)
	if c.View() != calendar.ViewWeek {
		t.Fatalf("view %v", c.View())
	}
	if r := c.Range(); !r.From.Equal(pageDay(10, 12, 0, 0)) || !r.To.Equal(pageDay(10, 19, 0, 0)) {
		t.Errorf("range %v", r)
	}
	if c.Title() != "October 2026" || c.Subtitle() == "" {
		t.Errorf("title %q subtitle %q", c.Title(), c.Subtitle())
	}
	// The locale's first day is Sunday: the week from the 11th.
	l := &pageLoop{}
	us := pageStarted(pageSamples(l), l, pageOptions(l, time.Sunday, nil))
	if !us.Range().From.Equal(pageDay(10, 11, 0, 0)) {
		t.Errorf("sunday week from %v", us.Range().From)
	}
	// The preference wins over the locale.
	src := pageSamples(l)
	src.Update(func(s *calendardata.Snapshot) { s.Preferences.WeekStart = api.WeekStartSaturday })
	sat := pageStarted(src, l, pageOptions(l, time.Monday, nil))
	if !sat.Range().From.Equal(pageDay(10, 10, 0, 0)) {
		t.Errorf("saturday week from %v", sat.Range().From)
	}
}

func TestViewsAndStepsAndToday(t *testing.T) {
	l := &pageLoop{}
	store := &pageView{nick: "week"}
	c := pageStarted(pageSamples(l), l, pageOptions(l, time.Monday, store))
	c.SetView(calendar.ViewDay)
	if store.nick != "day" {
		t.Errorf("stored %q", store.nick)
	}
	if r := c.Range(); !r.From.Equal(pageDay(10, 14, 0, 0)) || !r.To.Equal(pageDay(10, 15, 0, 0)) {
		t.Errorf("day range %v", r)
	}
	c.Step(1)
	if !c.Range().From.Equal(pageDay(10, 15, 0, 0)) {
		t.Errorf("stepped day %v", c.Range().From)
	}
	c.SetView(calendar.ViewMonth)
	if r := c.Range(); !r.From.Equal(pageDay(9, 28, 0, 0)) || calendar.DaysBetween(r.From, r.To, pageZone) != 42 {
		t.Errorf("month range %v", r)
	}
	c.Step(-1)
	if c.Anchor().Month() != time.September {
		t.Errorf("anchor %v", c.Anchor())
	}
	c.Today()
	if !c.Anchor().Equal(pageDay(10, 14, 0, 0)) {
		t.Errorf("today %v", c.Anchor())
	}
	c.SetView(calendar.ViewAgenda)
	if r := c.Range(); calendar.DaysBetween(r.From, r.To, pageZone) != 30 {
		t.Errorf("agenda %v", r)
	}
	c.LoadMoreAgenda()
	if r := c.Range(); calendar.DaysBetween(r.From, r.To, pageZone) != 60 || c.AgendaPages() != 2 {
		t.Errorf("agenda paged %v", r)
	}
	// A new window reads the stored view.
	if again := NewCalendarController(pageSamples(l), pageOptions(l, time.Monday, store)); again.View() != calendar.ViewAgenda {
		t.Errorf("again %v", again.View())
	}
	// No store: the default view.
	if d := NewCalendarController(pageSamples(l), pageOptions(l, time.Monday, nil)); d.View() != calendar.DefaultView {
		t.Errorf("default %v", d.View())
	}
}

func TestAgendaStartsAtTodayWhenSwitchedTo(t *testing.T) {
	c, _, _ := pageController(t)
	today := pageDay(10, 14, 0, 0)
	c.SetView(calendar.ViewDay)
	c.GoTo(pageDay(10, 13, 9, 0))
	if !c.Anchor().Equal(pageDay(10, 13, 0, 0)) {
		t.Fatalf("anchor %v", c.Anchor())
	}
	c.SetView(calendar.ViewAgenda)
	if !c.Anchor().Equal(today) || !c.Range().From.Equal(today) {
		t.Errorf("agenda at %v", c.Anchor())
	}
	// ‹ › move the Agenda; leaving it keeps the stepped anchor.
	c.Step(1)
	stepped := c.Anchor()
	if !stepped.Equal(calendar.AddDays(today, 30)) {
		t.Errorf("stepped %v", stepped)
	}
	c.SetView(calendar.ViewWeek)
	if !c.Anchor().Equal(stepped) {
		t.Errorf("week after agenda %v", c.Anchor())
	}
	// Month → Week keeps the anchor too.
	c.SetView(calendar.ViewMonth)
	c.Step(1)
	month := c.Anchor()
	c.SetView(calendar.ViewWeek)
	if !c.Anchor().Equal(month) {
		t.Errorf("week after month %v", c.Anchor())
	}
}

func TestGoToDayAndMiniMonth(t *testing.T) {
	c, _, _ := pageController(t)
	c.GoTo(pageDay(11, 3, 15, 0))
	if !c.Range().From.Equal(pageDay(11, 2, 0, 0)) {
		t.Fatalf("range %v", c.Range())
	}
	if got := c.Sidebar().MiniMonth.Title; got != "November 2026" {
		t.Errorf("mini month %q", got)
	}
	c.StepMiniMonth(1)
	if got := c.Sidebar().MiniMonth.Title; got != "December 2026" {
		t.Errorf("mini month %q", got)
	}
	// The range did not move with the mini month.
	if !c.Range().From.Equal(pageDay(11, 2, 0, 0)) {
		t.Errorf("range moved %v", c.Range())
	}
	// The shown week is banded in the mini month of its month.
	c.StepMiniMonth(-1)
	var banded []calendar.MiniMonthDay
	for _, d := range c.Sidebar().MiniMonth.Days {
		if d.Shown {
			banded = append(banded, d)
		}
	}
	if len(banded) != 7 || !banded[0].BandStart || !banded[6].BandEnd {
		t.Errorf("band %+v", banded)
	}
}

func TestOccurrencesOfTheRangeAndVisibility(t *testing.T) {
	c, src, l := pageController(t)
	if !slices.Contains(titles(c.Occurrences()), "Climbing") {
		t.Fatalf("occurrences %v", titles(c.Occurrences()))
	}
	if !slices.ContainsFunc(c.Occurrences(), func(o api.Occurrence) bool { return o.Invitation != nil }) {
		t.Error("no invitation in the week")
	}
	c.SetVisible(calendar.SampleSport, false)
	// At once from what is held, again after the reload.
	if slices.Contains(titles(c.Occurrences()), "Climbing") {
		t.Error("hidden calendar still shown")
	}
	l.run()
	if !c.Idle() || slices.Contains(titles(c.Occurrences()), "Climbing") {
		t.Error("hidden calendar shown after the reload")
	}
	w := src.Writes()
	if len(w) == 0 || w[len(w)-1].Method != api.MethodCalendarUpdate {
		t.Errorf("writes %v", w)
	} else if p := w[len(w)-1].Params.(api.CalendarUpdateParams); p.CalendarID != calendar.SampleSport || p.Visible == nil || *p.Visible {
		t.Errorf("visible params %+v", p)
	}
	for _, a := range c.Sidebar().Accounts {
		for _, k := range a.Calendars {
			if k.ID == calendar.SampleSport && k.Visible {
				t.Error("sidebar shows sport visible")
			}
		}
	}
	// Next week has none of the samples' events.
	c.Step(1)
	l.run()
	if len(c.Occurrences()) != 0 {
		t.Errorf("next week %v", titles(c.Occurrences()))
	}
}

func TestRefusedVisibilityIsReported(t *testing.T) {
	c, src, _ := pageController(t)
	src.Failures[api.MethodCalendarUpdate] = &api.Error{Code: api.CodeCalendarNotFound, Message: "gone"}
	var errs []string
	c.OnError = func(s string) { errs = append(errs, s) }
	c.SetVisible(calendar.SampleSport, false)
	want, _ := calendar.ErrorText(api.CodeCalendarNotFound, pageTr{})
	if !slices.Equal(errs, []string{want}) {
		t.Errorf("errors %q", errs)
	}
	if k, _ := src.Snapshot().Calendar(calendar.SampleSport); !k.Visible {
		t.Error("refused change kept")
	}
}

func TestSearchWaitsForTheDebounceAndTwoCharacters(t *testing.T) {
	c, src, l := pageController(t)
	c.SetSearch("b")
	if _, ok := c.SearchResults(); c.SearchPending() || ok || len(l.timers) != 0 {
		t.Fatal("one character searched")
	}
	c.SetSearch("bu")
	c.SetSearch("bud")
	if !slices.Equal(l.timers, []time.Duration{SearchDebounce, SearchDebounce}) || SearchDebounce != 300*time.Millisecond {
		t.Fatalf("timers %v", l.timers)
	}
	if pageCount(src, api.MethodEventSearch) != 0 || !c.SearchPending() {
		t.Fatal("searched before the debounce")
	}
	l.fire()
	// Only the last query ran.
	if pageCount(src, api.MethodEventSearch) != 1 || c.SearchPending() {
		t.Fatalf("searches %d", pageCount(src, api.MethodEventSearch))
	}
	if r, ok := c.SearchResults(); !ok || !slices.Equal(titles(r), []string{"Budget review"}) {
		t.Errorf("results %v", titles(r))
	}
	if c.ListHeading() != calendar.ResultsHeading(pageTr{}) || !slices.Equal(titles(c.ShownOccurrences()), []string{"Budget review"}) {
		t.Errorf("heading %q shown %v", c.ListHeading(), titles(c.ShownOccurrences()))
	}
	// Escape without a popover clears the search.
	if !c.HandleKey(calendar.Press{Special: calendar.SpecialEscape}, false, false) {
		t.Error("escape not taken")
	}
	if _, ok := c.SearchResults(); ok || c.SearchText() != "" {
		t.Error("search not cleared")
	}
	c.SetSearch("zz")
	l.fire()
	if c.EmptyListText() != calendar.NoResults(pageTr{}) {
		t.Errorf("empty %q", c.EmptyListText())
	}
	c.SetSearch("z")
	if _, ok := c.SearchResults(); ok {
		t.Error("one character keeps the results")
	}
	// A failed search shows no results and a toast.
	var errs []string
	c.OnError = func(s string) { errs = append(errs, s) }
	src.Failures[api.MethodEventSearch] = &api.Error{Code: api.CodeOffline}
	c.SetSearch("bud")
	l.fire()
	if r, ok := c.SearchResults(); !ok || len(r) != 0 || len(errs) != 1 {
		t.Errorf("failed search %v %q", r, errs)
	}
	// A stopped controller takes no answer.
	c.SetSearch("budget")
	c.Stop()
	l.fire()
	if pageCount(src, api.MethodEventSearch) != 3 {
		t.Errorf("searched after Stop")
	}
}

func TestSelectionAndKeys(t *testing.T) {
	c, _, _ := pageController(t)
	var opened, removed []string
	closed := 0
	var defaults []calendar.Defaults
	c.OnOpenDetail = func(o api.Occurrence) { opened = append(opened, calendar.OccurrenceKey(o)) }
	c.OnRemove = func(o api.Occurrence) { removed = append(removed, calendar.OccurrenceKey(o)) }
	c.OnClosePopover = func() { closed++ }
	c.OnNewEvent = func(d calendar.Defaults) { defaults = append(defaults, d) }
	key := func(p calendar.Press, inText, popover bool) bool { return c.HandleKey(p, inText, popover) }
	// Return and Delete need a selection.
	if key(calendar.Press{Special: calendar.SpecialReturn}, false, false) {
		t.Error("return without a selection")
	}
	if !key(calendar.Press{Special: calendar.SpecialDown}, false, false) {
		t.Fatal("down not taken")
	}
	order := calendar.SelectionOrder(c.Occurrences(), pageZone)
	if len(order) < 2 || c.Selection() != order[0] {
		t.Fatalf("selection %q of %v", c.Selection(), order)
	}
	key(calendar.Press{Special: calendar.SpecialRight}, false, false)
	if c.Selection() != order[1] {
		t.Errorf("right %q", c.Selection())
	}
	key(calendar.Press{Special: calendar.SpecialLeft}, false, false)
	if c.Selection() != order[0] {
		t.Errorf("left %q", c.Selection())
	}
	// Not in a text field.
	if key(calendar.Press{Special: calendar.SpecialDown}, true, false) {
		t.Error("down in a text field")
	}
	if !key(calendar.Press{Special: calendar.SpecialReturn}, false, false) || !slices.Equal(opened, []string{order[0]}) {
		t.Errorf("opened %v", opened)
	}
	if !key(calendar.Press{Special: calendar.SpecialDelete}, false, false) || !slices.Equal(removed, []string{order[0]}) {
		t.Errorf("removed %v", removed)
	}
	if !key(calendar.Press{Special: calendar.SpecialEscape}, false, true) || closed != 1 {
		t.Errorf("closed %d", closed)
	}
	// Escape with nothing open is not the calendar's.
	if key(calendar.Press{Special: calendar.SpecialEscape}, false, false) {
		t.Error("escape with nothing to do taken")
	}
	// Ctrl+Shift+3 (Linux), Ctrl+→, Ctrl+T, Ctrl+Shift+N.
	gtk := func(r rune, s calendar.Special, shift bool) calendar.Press {
		return calendar.Press{Rune: r, Special: s, Primary: true, Shift: shift, ShiftModifier: true}
	}
	if !key(gtk('3', calendar.SpecialNone, true), false, false) || c.View() != calendar.ViewMonth {
		t.Errorf("month view %v", c.View())
	}
	if !key(gtk(0, calendar.SpecialRight, false), false, false) || c.Anchor().Month() != time.November {
		t.Errorf("next %v", c.Anchor())
	}
	if !key(gtk('t', calendar.SpecialNone, false), false, false) || c.Anchor().Month() != time.October {
		t.Errorf("today %v", c.Anchor())
	}
	if !key(gtk('n', calendar.SpecialNone, true), false, false) || len(defaults) != 1 ||
		!defaults[0].Start.Equal(pageDay(10, 14, 10, 30)) {
		t.Errorf("new event %+v", defaults)
	}
	// The macOS modifiers too: ⌃⌘1.
	if !key(calendar.Press{Rune: '1', Primary: true, Control: true}, false, false) || c.View() != calendar.ViewDay {
		t.Errorf("day view %v", c.View())
	}
	// Ctrl+3 is the window's.
	if key(gtk('3', calendar.SpecialNone, false), false, false) {
		t.Error("show calendar taken")
	}
}

func TestOpenDetailSelects(t *testing.T) {
	c, _, _ := pageController(t)
	var opened []string
	c.OnOpenDetail = func(o api.Occurrence) { opened = append(opened, o.Title) }
	o := c.Occurrences()[0]
	c.OpenDetail(o)
	if c.Selection() != calendar.OccurrenceKey(o) || !slices.Equal(opened, []string{o.Title}) {
		t.Errorf("selection %q opened %v", c.Selection(), opened)
	}
	if s, ok := c.SelectedOccurrence(); !ok || s.Title != o.Title {
		t.Error("selected occurrence")
	}
	// Show in Calendar: the day view of the day with the key selected.
	start, _, _ := calendar.Span(o, pageZone)
	c.Select("")
	c.Show(start, calendar.ViewDay, calendar.OccurrenceKey(o))
	if c.View() != calendar.ViewDay || !c.Anchor().Equal(calendar.StartOfDay(start, pageZone)) ||
		c.Selection() != calendar.OccurrenceKey(o) {
		t.Errorf("show %v %v %q", c.View(), c.Anchor(), c.Selection())
	}
}

func TestNewEventDefaultsByOrigin(t *testing.T) {
	c, _, _ := pageController(t)
	cell := c.NewEventDefaults(pageDay(10, 20, 0, 0), calendar.OriginMonthCell)
	if !cell.Start.Equal(pageDay(10, 20, 9, 0)) || !cell.End.Equal(pageDay(10, 20, 10, 0)) {
		t.Errorf("cell %v–%v", cell.Start, cell.End)
	}
	if d := c.NewEventDefaults(pageDay(10, 20, 13, 0), calendar.OriginAllDay); !d.AllDay {
		t.Error("all day")
	}
	if d := c.NewEventDefaults(pageDay(10, 20, 13, 40), calendar.OriginSlot); !d.Start.Equal(pageDay(10, 20, 13, 30)) {
		t.Errorf("slot %v", d.Start)
	}
}

func TestSidebarStatusAndConsent(t *testing.T) {
	c, src, _ := pageController(t)
	s := c.Sidebar()
	var names []string
	var sources []api.CalendarSource
	for _, a := range s.Accounts {
		names = append(names, a.Name)
		sources = append(sources, a.Source)
		if a.StateLine != "" || a.OffersSignIn || a.Account == nil {
			t.Errorf("account %+v", a)
		}
	}
	if !slices.Equal(names, []string{"Work", "Gmail", "Nextcloud"}) ||
		!slices.Equal(sources, []api.CalendarSource{api.CalendarSourceGraph, api.CalendarSourceGoogle, api.CalendarSourceCalDAV}) {
		t.Errorf("accounts %v %v", names, sources)
	}
	if s.Unanswered != 3 || s.InvitationsTitle != "Invitations" || s.UnansweredTitle != "Unanswered" {
		t.Errorf("invitations %+v", s)
	}
	if s.AddCalendar != "Add Calendar…" || s.AddCalDAV != "Add CalDAV Calendar…" || s.SyncMicrosoft == "" {
		t.Errorf("add calendar %q %q %q", s.AddCalendar, s.AddCalDAV, s.SyncMicrosoft)
	}
	if c.StatusLine() != "Calendars synced 2 minutes ago" || c.UnansweredLine() != "3 unanswered invitations" {
		t.Errorf("status %q unanswered %q", c.StatusLine(), c.UnansweredLine())
	}
	var signIn []api.AccountID
	c.OnSignIn = func(id api.AccountID) { signIn = append(signIn, id) }
	raw, _ := json.Marshal(api.CalendarStateNotification{State: api.CalendarSyncState{AccountID: calendar.SampleWork,
		Status: api.CalendarSyncConsentRequired, Progress: -1}})
	src.Handle(api.NotifyCalendarState, raw)
	work := c.Sidebar().Accounts[0]
	if work.StateLine != calendar.ConsentLine(pageTr{}) || !work.OffersSignIn || c.StatusLine() != work.StateLine {
		t.Errorf("consent %+v status %q", work, c.StatusLine())
	}
	c.SignIn(work.AccountID)
	if !slices.Equal(signIn, []api.AccountID{calendar.SampleWork}) {
		t.Errorf("sign in %v", signIn)
	}
	// Offline and authRequired rows.
	src.Update(func(s *calendardata.Snapshot) {
		s.CalendarAccounts[1].State.Status = api.CalendarSyncOffline
		s.CalendarAccounts[2].State.Status = api.CalendarSyncAuthRequired
	})
	a := c.Sidebar().Accounts
	if a[1].StateLine != calendar.OfflineLine(pageTr{}) || a[1].OffersSignIn || a[2].StateLine == "" || !a[2].OffersSignIn {
		t.Errorf("rows %+v %+v", a[1], a[2])
	}
}

func TestDisabledCalendarsAreOnlyInPreferences(t *testing.T) {
	l := &pageLoop{}
	src := pageSamples(l)
	src.Update(func(s *calendardata.Snapshot) { s.CalendarAccounts[1].Calendars[1].Disabled = true })
	c := pageStarted(src, l, pageOptions(l, time.Monday, nil))
	var ids []api.CalendarID
	for _, k := range c.Sidebar().Accounts[1].Calendars {
		ids = append(ids, k.ID)
	}
	if !slices.Equal(ids, []api.CalendarID{calendar.SamplePersonal}) {
		t.Errorf("gmail calendars %v", ids)
	}
	if slices.ContainsFunc(c.Occurrences(), func(o api.Occurrence) bool { return o.CalendarID == calendar.SampleFamily }) {
		t.Error("a disabled calendar's occurrence shown")
	}
	if a, _ := src.Snapshot().CalendarAccount(calendar.SampleGmail); len(a.Calendars) != 2 {
		t.Error("preferences lost the disabled calendar")
	}
}

func TestUnansweredFilterListsTheInvitations(t *testing.T) {
	c, _, _ := pageController(t)
	c.ShowUnanswered()
	if c.Filter() != FilterUnanswered || c.ListHeading() != calendar.UnansweredHeading(pageTr{}) {
		t.Fatalf("filter %v heading %q", c.Filter(), c.ListHeading())
	}
	days, ok := c.Agenda()
	got := map[string]bool{}
	for _, d := range days {
		for _, it := range d.Items {
			got[it.Occurrence.Title] = true
			if it.State != calendar.BlockUnanswered {
				t.Errorf("%q state %v", it.Occurrence.Title, it.State)
			}
		}
	}
	if !ok || len(got) != 3 || !got["Budget review"] || !got["Vendor demo"] || !got["Club meeting"] {
		t.Errorf("unanswered %v", got)
	}
	// Escape ends the filter; so does a view change.
	if !c.HandleKey(calendar.Press{Special: calendar.SpecialEscape}, false, false) || c.Filter() != FilterNone {
		t.Error("escape kept the filter")
	}
	c.ShowUnanswered()
	c.SetView(calendar.ViewWeek)
	if c.Filter() != FilterNone {
		t.Error("view change kept the filter")
	}
	if _, ok := c.Agenda(); ok {
		t.Error("week has an agenda")
	}
}

func TestEmptyStates(t *testing.T) {
	l := &pageLoop{}
	empty := calendardata.NewEmptySource(pageZone, l)
	c := pageStarted(empty, l, pageOptions(l, time.Monday, nil))
	if k, ok := c.EmptyKind(); !ok || k != calendar.EmptyNoAccounts {
		t.Errorf("empty %v %v", k, ok)
	}
	empty.Update(func(s *calendardata.Snapshot) { s.Phase = calendardata.PhaseUnavailable })
	if k, ok := c.EmptyKind(); !ok || k != calendar.EmptyNoBackend {
		t.Errorf("unavailable %v %v", k, ok)
	}
	empty.Update(func(s *calendardata.Snapshot) { s.Phase = calendardata.PhaseLoading })
	if k, ok := c.EmptyKind(); !ok || k != calendar.EmptyLoading {
		t.Errorf("loading %v %v", k, ok)
	}
	src := pageSamples(l)
	src.Update(func(s *calendardata.Snapshot) {
		for i := range s.CalendarAccounts {
			s.CalendarAccounts[i].Sync = false
		}
	})
	if k, ok := pageStarted(src, l, pageOptions(l, time.Monday, nil)).EmptyKind(); !ok || k != calendar.EmptySyncOff {
		t.Errorf("sync off %v %v", k, ok)
	}
	if k, ok := pageStarted(pageSamples(l), l, pageOptions(l, time.Monday, nil)).EmptyKind(); ok {
		t.Errorf("samples empty %v", k)
	}
	// An empty agenda says so.
	c.SetView(calendar.ViewAgenda)
	l.run()
	if c.EmptyListText() != calendar.AgendaEmpty(pageTr{}) {
		t.Errorf("agenda empty %q", c.EmptyListText())
	}
}

func TestNotificationsReloadAnOverlappingRange(t *testing.T) {
	c, src, l := pageController(t)
	before := pageCount(src, api.MethodEventList)
	changed := func(from, to *time.Time) {
		raw, _ := json.Marshal(api.CalendarChangedNotification{From: from, To: to})
		src.Handle(api.NotifyCalendarChanged, raw)
		l.run()
	}
	ptr := func(t time.Time) *time.Time { return &t }
	// Next year: not this week.
	changed(ptr(time.Date(2027, 1, 1, 0, 0, 0, 0, pageZone)), ptr(time.Date(2027, 1, 2, 0, 0, 0, 0, pageZone)))
	if pageCount(src, api.MethodEventList) != before {
		t.Error("reloaded for another range")
	}
	changed(ptr(pageDay(10, 15, 0, 0)), ptr(pageDay(10, 16, 0, 0)))
	if pageCount(src, api.MethodEventList) != before+1 {
		t.Error("not reloaded for this week")
	}
	changed(nil, nil)
	if pageCount(src, api.MethodEventList) != before+2 || !c.Idle() {
		t.Error("not reloaded for every range")
	}
}

func TestLoadErrorsAndStaleAnswers(t *testing.T) {
	l := &pageLoop{}
	src := pageSamples(l)
	c := NewCalendarController(src, pageOptions(l, time.Monday, nil))
	changes := 0
	c.OnChange = func() { changes++ }
	var errs []string
	c.OnError = func(s string) { errs = append(errs, s) }
	c.Start()
	if !c.Loading() || changes == 0 {
		t.Fatal("no load on Start")
	}
	// Stepping before the answer: only the second answer counts.
	c.Step(1)
	l.run()
	if c.Loading() || len(c.Occurrences()) != 0 {
		t.Errorf("stale answer taken: %v", titles(c.Occurrences()))
	}
	src.Failures[api.MethodEventList] = &api.Error{Code: api.CodeOffline}
	c.Reload()
	l.run()
	want, _ := calendar.ErrorText(api.CodeOffline, pageTr{})
	if !slices.Equal(errs, []string{want}) || c.Loading() {
		t.Errorf("errors %q", errs)
	}
	// An error without a calendar sentence goes to RPCText.
	o := pageOptions(l, time.Monday, nil)
	o.RPCText = func(what string, _ error) string { return "rpc: " + what }
	if got := o.ErrorMessage(&api.Error{Code: api.CodeAuthRequired}, ""); got != "rpc: Calendar" {
		t.Errorf("rpc text %q", got)
	}
	if got := o.ErrorMessage(calendardata.ErrPreview, ""); got != calendar.PreviewOnly(pageTr{}) {
		t.Errorf("preview %q", got)
	}
}
