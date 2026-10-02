// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/jira"
)

// The board's view model: a pure function from a snapshot, the view state
// (style, filters, selection) and the date to everything the three styles
// show — the list's sections, the columns, the Today page, the navigation
// column and the detail of the selected case. Every string of a case is
// cleaned here (CleanLine, CleanBlock), so the views only lay out plain
// text. The selection rules live here too, so the controller and the tests
// share them.
//
// The macOS client leads (MalachiCore/Board/BoardView.swift); this is its
// port.

// Dates writes the board's dates in the user's locale. A pure package
// cannot reach GLib, so the window passes widget's formatters; the tests
// pass a fixed English one.
type Dates interface {
	// Date is a list's date relative to now: the time today, else the day
	// and month in now's year, else the full date (widget.FormatDate).
	Date(t, now time.Time) string
	// Time is a time of day, "15:04" (widget.FormatTime).
	Time(t time.Time) string
	// DateTime is a full date and time (widget.FormatDateTime).
	DateTime(t time.Time) string
	// Weekday is the abbreviated weekday, "Thu" (strftime "%a").
	Weekday(t time.Time) string
}

// Env is what the view model is written with besides its data: the
// translator, the dates and the time zone that says what today is (the
// Swift client's Calendar). The window passes i18n.Tr, widget's formatters
// and time.Local. Tr and Dates are required.
type Env struct {
	Tr    Translator
	Dates Dates
	// Loc decides what a day is (deadlines, reminds); nil is time.Local.
	// Dates formats in the zone the window's formatters use, which must be
	// the same.
	Loc *time.Location
}

func (e Env) loc() *time.Location {
	if e.Loc == nil {
		return time.Local
	}
	return e.Loc
}

// Same reports the same filter: the state counts for FilterState only.
func (f Filter) Same(o Filter) bool {
	if f.Kind != o.Kind {
		return false
	}
	return f.Kind != FilterState || f.State == o.State
}

// ViewState is what the user chose to look at (Board.ViewState). Not case
// data: the source keeps that. Start from NewViewState.
type ViewState struct {
	Style  Style
	Filter Filter
	// Account is the account whose cases the board shows; "" = every
	// account.
	Account api.AccountID
	// Selection is the selected case; "" = none.
	Selection CaseID
	// RevealsWhy: the detail's "Why is this here?" box is open.
	RevealsWhy bool
	// InlineDetail: the list style has room for the detail beside it;
	// without, the detail is the sliding panel.
	InlineDetail bool
}

// NewViewState is the List under Overview, every account, nothing
// selected, the detail beside the list.
func NewViewState() ViewState {
	return ViewState{Style: StyleList, InlineDetail: true}
}

// Row is a case in a list, a column or the Today page. No selected field
// on purpose: a selection change must not rebuild the rows.
type Row struct {
	ID     CaseID
	State  State
	Person string
	Time   string
	Title  string
	// TitleIsAssistant: Title is the assistant's (its annotation's
	// title), not the subject; the views mark it (docs/api.md §4.13).
	TitleIsAssistant bool
	Snippet          string
	// SnippetIsAssistant: Snippet is the assistant's summary, not the
	// message's text.
	SnippetIsAssistant bool
	Account            string
	// IssueKey is "" when the case is no issue.
	IssueKey    string
	IssueStatus string
	IssueStyle  jira.StatusStyle
	// Due is "" without a due date.
	Due string
	// Remind is when a snoozed case comes back ("Tomorrow 09:00"); ""
	// otherwise.
	Remind      string
	Attachments bool
	// CountText is the message count from two on, "" below.
	CountText string
	Unread    bool
	// Spoken is the row's accessible label ("Assistant:" before the
	// assistant's title).
	Spoken string
}

// MarksAssistant reports a row that shows text the assistant wrote (its
// title, or its summary as the snippet): the views put the assistant's
// mark in front of the title.
func (r Row) MarksAssistant() bool { return r.TitleIsAssistant || r.SnippetIsAssistant }

// CommitmentRow is a commitment of the user's, with the case it comes
// from.
type CommitmentRow struct {
	ID     api.BoardCommitmentID
	CaseID CaseID
	Text   string
	Quote  string
	Due    string
	// From is the case's title.
	From string
	// FromIsAssistant: From is the assistant's title of the case, not its
	// subject.
	FromIsAssistant bool
	// SpokenFrom is From for the screen reader ("Assistant:" before the
	// assistant's title).
	SpokenFrom string
}

// SectionKind is what a section of the list holds.
type SectionKind int

// The kinds.
const (
	// SectionState is the live cases of one state (Section.State).
	SectionState SectionKind = iota
	// SectionSnoozed is, under the Done filter before the done cases, the
	// cases that come back later, the soonest first.
	SectionSnoozed
	// SectionDone is the done cases, newest first.
	SectionDone
)

// Section is a section of the list.
type Section struct {
	Kind SectionKind
	// State counts for SectionState only.
	State State
	Title string
	Rows  []Row
}

// Column is a column of the Columns style.
type Column struct {
	State     State
	Title     string
	Rows      []Row
	EmptyText string
}

// NavItem is a filter in the navigation column.
type NavItem struct {
	Filter Filter
	Title  string
	// Dot is the state's colour dot, when HasDot (not for Overview and
	// Done).
	Dot      State
	HasDot   bool
	Count    int
	Selected bool
}

// AccountItem is an account in the navigation column and the account
// menu.
type AccountItem struct {
	// Filter is the account; "" is All Accounts.
	Filter api.AccountID
	Title  string
	Badge  string
	// Count counts the live cases.
	Count    int
	Selected bool
}

// MessageCard is a message of the detail's conversation.
type MessageCard struct {
	// ID is "" for the samples.
	ID   api.MessageID
	From string
	When string
	Text string
	Mine bool
}

// Detail is the selected case in full. An empty string hides its block.
type Detail struct {
	ID        CaseID
	AccountID api.AccountID
	// Thread is "" for the samples.
	Thread api.ThreadID
	// Reply is what Reply answers and Show in Mail selects; nil for the
	// samples.
	Reply         *ReplyTarget
	LatestMessage api.MessageID
	State         State
	StateTitle    string
	Source        Source
	Why           string
	// WhyIsAssistant: Why is the assistant's reason, not the rules'; the
	// box leads it with the assistant's mark.
	WhyIsAssistant bool
	SourceText     string
	Account        string
	Issue          *IssueInfo
	Person         string
	Time           string
	Title          string
	// TitleIsAssistant: Title is the assistant's, not the subject; the
	// detail marks it.
	TitleIsAssistant bool
	// SpokenTitle is Title for the screen reader ("Assistant:" before the
	// assistant's).
	SpokenTitle string
	// Subject is "" = hidden: shown only when the assistant's title
	// differs.
	Subject  string
	Due      string
	DueQuote string
	Summary  string
	Tasks    []string
	// Draft is the suggested reply's plain text: the samples' static block
	// shows it (the daemon's board edits the draft inline instead).
	Draft string
	// DraftID is the suggested reply's draft, whatever its text (an empty
	// draft is still edited inline); "" without one. It stays while the
	// draft exists, also after the notes went stale.
	DraftID api.DraftID
	// CanUnstar: Unstar is offered, the case is on the board because of a
	// star (hot.flagged) and not done (DataSource.Unflag).
	CanUnstar bool
	// StaleNote is "" or StaleNotes: the assistant's notes no longer
	// count.
	StaleNote string
	IsDone    bool
	IsSnoozed bool
	// RemindText is "" or "Back on the board Tomorrow 09:00".
	RemindText string
	// CanArchive: Archive moves messages; without, it only marks the case
	// done.
	CanArchive        bool
	ConversationTitle string
	// Messages are oldest first; the last loaded while a newer version
	// loads.
	Messages []MessageCard
	// MessagesLoading: the conversation has not arrived yet (MessagesNote
	// says so).
	MessagesLoading bool
	// MessagesNote is "", MessagesLoading or MessagesFailed, shown in place
	// of the cards.
	MessagesNote string
	// MessagesRetry: the conversation could not be loaded; the note offers
	// TryAgain (Controller.RetryMessages).
	MessagesRetry bool
}

// DueItem is a deadline on the Today page.
type DueItem struct {
	CaseID CaseID
	Label  string
	Title  string
	// TitleIsAssistant: Title is the assistant's, not the subject.
	TitleIsAssistant bool
	// SpokenTitle is Title for the screen reader.
	SpokenTitle string
	Person      string
	Quote       string
}

// DueSection is a group of the Today page's deadlines (Swift
// Board.DueGroup).
type DueSection struct {
	Kind  DueGroup
	Title string
	Items []DueItem
}

// DueGroups lists the deadline groups in their order.
var DueGroups = []DueGroup{DueOverdue, DueToday, DueTomorrow, DueThisWeek, DueLater}

// TileKind is what a count tile of the Today page counts.
type TileKind int

// The kinds.
const (
	// TileState counts the live cases of Tile.State.
	TileState TileKind = iota
	// TileCommitments counts the open commitments.
	TileCommitments
)

// Tile is a count tile of the Today page ("3 Hot").
type Tile struct {
	Kind TileKind
	// State counts for TileState only.
	State State
	Count int
	Title string
}

// TodayPage is the Today style (Swift Board.Today).
type TodayPage struct {
	Title  string
	Phrase string
	// Tiles are the four states, and the commitments when annotations
	// count.
	Tiles []Tile
	Hot   []Row
	// You are the first YouTopCount cases waiting for the user, YouMore
	// counts the others.
	You         []Row
	YouMore     int
	Commitments []CommitmentRow
	// DueGroups are the non-empty groups, in DueGroups order.
	DueGroups     []DueSection
	DueEmpty      string
	CalendarTitle string
	CalendarBody  string
}

// ViewModel is everything the board shows (Swift Board.View).
type ViewModel struct {
	// Phase is how far the source's data is.
	Phase Phase
	// EmptyTitle and EmptyBody are the empty board's texts for the phase
	// (IsEmpty).
	EmptyTitle string
	EmptyBody  string
	// Notice is a line above the cases while they are partial or old; ""
	// when none.
	Notice string
	// Triage and Run are carried for the triage's status.
	Triage       TriageInfo
	Run          *Run
	Nav          []NavItem
	Accounts     []AccountItem
	AccountTitle string
	// Subtitle is the window's subtitle: "All Accounts · 23 cases".
	Subtitle string
	// IsEmpty: no case at all in the account scope (live, snoozed or
	// done).
	IsEmpty bool
	// Sections are the list's non-empty sections.
	Sections               []Section
	SectionsEmptyText      string
	Commitments            []CommitmentRow
	ShowsCommitmentsInList bool
	// Columns are always the four states, whatever the filter.
	Columns    []Column
	Today      TodayPage
	Detail     *Detail
	ShowsPanel bool
	StatusLine string
	// AssistantOn is the snapshot's Annotated.
	AssistantOn bool
	// Selection is the selection, resolved (ResolveSelection); "" for
	// none.
	Selection CaseID
}

// YouTopCount is how many cases waiting for the user the Today page lists.
const YouTopCount = 5

// View builds the view model of snapshot s for the view state v at now.
func View(s Snapshot, v ViewState, now time.Time, env Env) ViewModel {
	tr := env.Tr
	sc := newScope(s, v)
	ctx := newViewContext(s, now, env)

	// The columns, and their counts for the navigation and the tiles.
	columns := make([]Column, 0, len(States))
	for _, st := range States {
		var rows []Row
		for _, c := range sc.live {
			if sc.state(c) == st {
				rows = append(rows, ctx.row(c))
			}
		}
		columns = append(columns, Column{State: st, Title: StateName(st, tr), Rows: rows, EmptyText: ColumnEmpty(st, tr)})
	}
	count := func(st State) int {
		for _, col := range columns {
			if col.State == st {
				return len(col.Rows)
			}
		}
		return 0
	}

	// The list: the columns' rows under the filter.
	var sections []Section
	switch v.Filter.Kind {
	case FilterDone:
		if len(sc.snoozed) > 0 {
			sections = append(sections, Section{Kind: SectionSnoozed, Title: Snoozed(tr), Rows: ctx.rows(sc.snoozed)})
		}
		if len(sc.finished) > 0 {
			sections = append(sections, Section{Kind: SectionDone, Title: Done(tr), Rows: ctx.rows(sc.finished)})
		}
	default:
		for _, col := range columns {
			if len(col.Rows) > 0 && (v.Filter.Kind == FilterAll || v.Filter.State == col.State) {
				sections = append(sections, Section{Kind: SectionState, State: col.State, Title: col.Title, Rows: col.Rows})
			}
		}
	}

	// The commitments: of live cases in the scope, when annotations count;
	// the first capCommitments shown, all of them counted.
	var commitments []CommitmentRow
	commitmentCount := 0
	if s.Annotated {
		live := make(map[CaseID]Case, len(sc.live))
		for _, c := range sc.live {
			if _, ok := live[c.ID]; !ok {
				live[c.ID] = c
			}
		}
		for _, k := range s.Commitments {
			if k.State != CommitmentOpen {
				continue
			}
			c, ok := live[k.CaseID]
			if !ok {
				continue
			}
			commitmentCount++
			if len(commitments) >= capCommitments {
				continue
			}
			from := ctx.titled(c)
			due := ""
			if k.Due != nil {
				due = ctx.dueLabel(*k.Due)
			}
			commitments = append(commitments, CommitmentRow{
				ID: k.ID, CaseID: c.ID, Text: CleanLine(k.Text, capCommitment), Quote: CleanLine(k.Quote, capQuote),
				Due: due, From: from.text, FromIsAssistant: from.assistant, SpokenFrom: from.spoken,
			})
		}
	}

	// The navigation column.
	nav := []NavItem{{
		Filter: Filter{Kind: FilterAll}, Title: FilterTitle(Filter{Kind: FilterAll}, tr), Count: len(sc.live),
		Selected: v.Filter.Kind == FilterAll,
	}}
	for _, st := range States {
		f := Filter{Kind: FilterState, State: st}
		nav = append(nav, NavItem{
			Filter: f, Title: FilterTitle(f, tr), Dot: st, HasDot: true, Count: count(st), Selected: v.Filter.Same(f),
		})
	}
	nav = append(nav, NavItem{
		Filter: Filter{Kind: FilterDone}, Title: FilterTitle(Filter{Kind: FilterDone}, tr),
		Count: len(sc.finished) + len(sc.snoozed), Selected: v.Filter.Kind == FilterDone,
	})

	liveIn := func(account api.AccountID) int {
		n := 0
		for _, c := range sc.unique {
			if c.Visibility.IsLive() && (account == "" || c.Account == account) {
				n++
			}
		}
		return n
	}
	accounts := []AccountItem{{Title: AllAccounts(tr), Count: liveIn(""), Selected: v.Account == ""}}
	for _, a := range s.Accounts {
		accounts = append(accounts, AccountItem{
			Filter: a.ID, Title: CleanLine(a.Name, capAccount), Badge: CleanLine(a.Badge, capBadge),
			Count: liveIn(a.ID), Selected: v.Account == a.ID,
		})
	}
	accountTitle := AllAccounts(tr)
	if v.Account != "" {
		accountTitle = ctx.accountName(v.Account)
	}

	// The Today page.
	hot := columns[0].Rows
	you := columns[1].Rows
	tiles := make([]Tile, 0, len(States)+1)
	for _, st := range States {
		tiles = append(tiles, Tile{Kind: TileState, State: st, Count: count(st), Title: StateName(st, tr)})
	}
	if s.Annotated {
		tiles = append(tiles, Tile{Kind: TileCommitments, Count: commitmentCount, Title: Commitments(tr)})
	}
	today := TodayPage{
		Title: StyleTitle(StyleToday, tr), Phrase: TodoPhrase(len(hot)+len(you), tr), Tiles: tiles, Hot: hot,
		You: you[:min(len(you), YouTopCount)], YouMore: max(0, len(you)-YouTopCount), Commitments: commitments,
		DueGroups: dueSections(sc, ctx), DueEmpty: DueEmpty(tr), CalendarTitle: CalendarTitle(tr),
		CalendarBody: CalendarBody(tr),
	}

	// The selection and its detail.
	selection := sc.resolve(v.Selection)
	var detail *Detail
	if selection != "" {
		for _, c := range sc.unique {
			if c.ID == selection {
				d := ctx.detail(c)
				detail = &d
				break
			}
		}
	}

	model, note := "", ""
	if s.Run != nil {
		model = CleanLine(s.Run.Model, capModel)
		note = CleanLine(s.Run.Note, capNote)
	}
	return ViewModel{
		Phase: s.Phase, EmptyTitle: EmptyTitleOf(s.Phase, tr), EmptyBody: EmptyBodyOf(s.Phase, tr),
		Notice: Notice(s.Phase, s.Truncated, tr), Triage: s.Triage, Run: s.Run, Nav: nav, Accounts: accounts,
		AccountTitle: accountTitle, Subtitle: accountTitle + separator + CaseCount(len(sc.live), tr),
		IsEmpty: len(sc.live) == 0 && len(sc.finished) == 0 && len(sc.snoozed) == 0, Sections: sections,
		SectionsEmptyText: SectionEmpty(tr), Commitments: commitments,
		ShowsCommitmentsInList: len(commitments) > 0 && v.Filter.Kind == FilterAll, Columns: columns, Today: today,
		Detail: detail, ShowsPanel: selection != "" && (v.Style != StyleList || !v.InlineDetail),
		StatusLine: StatusLine(s.Annotated, model, note, tr), AssistantOn: s.Annotated, Selection: selection,
	}
}

// ResolveSelection is the selection the board shows: v.Selection while
// that case is shown (in the list: under its filter; in Columns and Today:
// live in the account scope), else none — except that the list with its
// detail beside it selects its first row.
func ResolveSelection(s Snapshot, v ViewState) CaseID {
	return newScope(s, v).resolve(v.Selection)
}

// SelectionAfterDone is the selection after the case id leaves the list
// (done, reopened or moved out of the filter), computed on the snapshot
// from before: the next row, else the previous one. Columns and Today
// select nothing; "" also when id is not in the list.
func SelectionAfterDone(id CaseID, s Snapshot, v ViewState) CaseID {
	if v.Style != StyleList {
		return ""
	}
	shown := newScope(s, v).shown()
	i := slices.Index(shown, id)
	switch {
	case i < 0:
		return ""
	case i+1 < len(shown):
		return shown[i+1]
	case i > 0:
		return shown[i-1]
	}
	return ""
}

// DueGroupOf is the deadline group of due, by calendar days in loc from
// now: before today, today, tomorrow, the rest of the next seven days,
// later.
func DueGroupOf(due, now time.Time, loc *time.Location) DueGroup {
	switch days := dayDifference(now, due, loc); {
	case days < 0:
		return DueOverdue
	case days == 0:
		return DueToday
	case days == 1:
		return DueTomorrow
	case days <= 7:
		return DueThisWeek
	}
	return DueLater
}

// dayDifference is how many calendar days in loc lie from a's day to b's.
func dayDifference(a, b time.Time, loc *time.Location) int {
	ay, am, ad := a.In(loc).Date()
	by, bm, bd := b.In(loc).Date()
	d := time.Date(by, bm, bd, 0, 0, 0, 0, time.UTC).Sub(time.Date(ay, am, ad, 0, 0, 0, 0, time.UTC))
	return int(d / (24 * time.Hour))
}

// dueSections are the Today page's deadlines: of live cases in the scope
// whose annotation counts, soonest first.
func dueSections(sc scope, ctx viewContext) []DueSection {
	if !sc.s.Annotated {
		return nil
	}
	type dated struct {
		c   Case
		due time.Time
		a   *Annotation
	}
	var list []dated
	for _, c := range sc.live {
		if a := AnnotationOf(c, true); a != nil && a.Due != nil {
			list = append(list, dated{c, *a.Due, a})
		}
	}
	slices.SortFunc(list, func(x, y dated) int {
		if c := x.due.Compare(y.due); c != 0 {
			return c
		}
		return cmp.Compare(x.c.ID, y.c.ID)
	})
	groups := map[DueGroup][]DueItem{}
	for _, d := range list {
		kind := DueGroupOf(d.due, ctx.now, ctx.loc)
		title := ctx.titled(d.c)
		groups[kind] = append(groups[kind], DueItem{
			CaseID: d.c.ID, Label: ctx.dueLabel(d.due), Title: title.text, TitleIsAssistant: title.assistant,
			SpokenTitle: title.spoken, Person: CleanLine(d.c.Person, capPerson), Quote: CleanLine(d.a.DueQuote, capQuote),
		})
	}
	var out []DueSection
	for _, k := range DueGroups {
		if items := groups[k]; len(items) > 0 {
			out = append(out, DueSection{Kind: k, Title: DueGroupTitle(k, ctx.tr), Items: items})
		}
	}
	return out
}

// scope is the cases in the account scope, ordered, and what the list
// shows.
type scope struct {
	s Snapshot
	v ViewState
	// unique is the snapshot's cases, the first of each id only: a source
	// that repeats an id must not give two rows one identity.
	unique []Case
	// live is on the board, in the account scope: by state, then newest
	// first.
	live []Case
	// finished is done, in the account scope: newest first.
	finished []Case
	// snoozed is snoozed, in the account scope: the soonest back first.
	snoozed []Case
}

func newScope(s Snapshot, v ViewState) scope {
	sc := scope{s: s, v: v}
	seen := make(map[CaseID]bool, len(s.Cases))
	for _, c := range s.Cases {
		if seen[c.ID] {
			continue
		}
		seen[c.ID] = true
		sc.unique = append(sc.unique, c)
		if v.Account != "" && c.Account != v.Account {
			continue
		}
		switch c.Visibility.Kind {
		case VisibleLive:
			sc.live = append(sc.live, c)
		case VisibleDone:
			sc.finished = append(sc.finished, c)
		case VisibleSnoozed:
			sc.snoozed = append(sc.snoozed, c)
		}
	}
	slices.SortFunc(sc.live, func(a, b Case) int {
		if c := cmp.Compare(sc.state(a), sc.state(b)); c != 0 {
			return c
		}
		return newer(a, b)
	})
	slices.SortFunc(sc.finished, newer)
	slices.SortFunc(sc.snoozed, func(a, b Case) int {
		if c := a.Visibility.At.Compare(b.Visibility.At); c != 0 {
			return c
		}
		return newer(a, b)
	})
	return sc
}

// newer orders the newest first, then by id.
func newer(a, b Case) int {
	if c := b.Date.Compare(a.Date); c != 0 {
		return c
	}
	return cmp.Compare(a.ID, b.ID)
}

func (sc scope) state(c Case) State { return StateOf(c, sc.s.Annotated) }

// shown is the ids the current style shows, in order: the list's rows
// under its filter, or every live case for Columns and Today.
func (sc scope) shown() []CaseID {
	var out []CaseID
	add := func(cs []Case, keep func(Case) bool) {
		for _, c := range cs {
			if keep == nil || keep(c) {
				out = append(out, c.ID)
			}
		}
	}
	switch {
	case sc.v.Style != StyleList || sc.v.Filter.Kind == FilterAll:
		add(sc.live, nil)
	case sc.v.Filter.Kind == FilterState:
		add(sc.live, func(c Case) bool { return sc.state(c) == sc.v.Filter.State })
	default:
		add(sc.snoozed, nil)
		add(sc.finished, nil)
	}
	return out
}

func (sc scope) resolve(selection CaseID) CaseID {
	shown := sc.shown()
	if selection != "" && slices.Contains(shown, selection) {
		return selection
	}
	if sc.v.Style == StyleList && sc.v.InlineDetail && len(shown) > 0 {
		return shown[0]
	}
	return ""
}

// viewContext is what every row and detail is built with: the accounts,
// the date and how to write it.
type viewContext struct {
	s        Snapshot
	now      time.Time
	tr       Translator
	dates    Dates
	loc      *time.Location
	accounts map[api.AccountID]AccountInfo
}

func newViewContext(s Snapshot, now time.Time, env Env) viewContext {
	ctx := viewContext{s: s, now: now, tr: env.Tr, dates: env.Dates, loc: env.loc(),
		accounts: make(map[api.AccountID]AccountInfo, len(s.Accounts))}
	for _, a := range s.Accounts {
		if _, ok := ctx.accounts[a.ID]; !ok {
			ctx.accounts[a.ID] = a
		}
	}
	return ctx
}

func (ctx viewContext) annotation(c Case) *Annotation { return AnnotationOf(c, ctx.s.Annotated) }

func (ctx viewContext) accountName(id api.AccountID) string {
	return CleanLine(ctx.accounts[id].Name, capAccount)
}

// subject is the case's subject, cleaned; "(No subject)" for none.
func (ctx viewContext) subject(c Case) string {
	if s := CleanLine(c.Subject, capTitle); s != "" {
		return s
	}
	return ctx.tr.T("(No subject)")
}

// title is a case's title: its text, whether it is the assistant's, and
// how the screen reader says it.
type title struct {
	text      string
	assistant bool
	spoken    string
}

// titled is the assistant's title when its annotation counts, else the
// subject.
func (ctx viewContext) titled(c Case) title {
	if a := ctx.annotation(c); a != nil {
		if t := CleanLine(a.Title, capTitle); t != "" {
			return title{t, true, SpokenAssistant(t, ctx.tr)}
		}
	}
	s := ctx.subject(c)
	return title{s, false, s}
}

// remindLabel is when a snoozed case comes back: "Tomorrow 09:00",
// "20 Oct 09:00".
func (ctx viewContext) remindLabel(at time.Time) string {
	return ctx.dueLabel(at) + " " + ctx.dates.Time(at)
}

// dueLabel is a deadline's day: Today, Tomorrow, else the list's date (in
// this branch never today, so never a time).
func (ctx viewContext) dueLabel(due time.Time) string {
	switch DueGroupOf(due, ctx.now, ctx.loc) {
	case DueToday:
		return DueGroupTitle(DueToday, ctx.tr)
	case DueTomorrow:
		return DueGroupTitle(DueTomorrow, ctx.tr)
	}
	return ctx.dates.Date(due, ctx.now)
}

func (ctx viewContext) rows(cs []Case) []Row {
	out := make([]Row, 0, len(cs))
	for _, c := range cs {
		out = append(out, ctx.row(c))
	}
	return out
}

func (ctx viewContext) row(c Case) Row {
	tr := ctx.tr
	st := StateOf(c, ctx.s.Annotated)
	person := CleanLine(c.Person, capPerson)
	t := ctx.titled(c)
	a := ctx.annotation(c)
	snippet := ""
	if a != nil {
		snippet = CleanLine(a.Summary, capSnippet)
	}
	snippetIsAssistant := snippet != ""
	if snippet == "" {
		snippet = CleanLine(c.Snippet, capSnippet)
	}
	var issueKey, issueStatus string
	issueStyle := jira.StatusPlain
	if c.Issue != nil {
		issueKey = CleanLine(c.Issue.Key, capIssueKey)
		issueStatus = CleanLine(c.Issue.Status, capStatus)
		issueStyle = c.Issue.Style
	}
	due := ""
	if a != nil && a.Due != nil {
		due = ctx.dueLabel(*a.Due)
	}
	n := max(1, c.MessageCount)
	remind := ""
	if at, ok := c.Visibility.RemindAt(); ok {
		remind = ctx.remindLabel(at)
	}

	spoken := []string{StateName(st, tr), person, t.spoken}
	if due != "" {
		spoken = append(spoken, SpokenDue(due, tr))
	}
	if issueKey != "" {
		if issueStatus == "" {
			spoken = append(spoken, issueKey)
		} else {
			spoken = append(spoken, issueKey+", "+issueStatus)
		}
	}
	if n > 1 {
		spoken = append(spoken, MessageCount(n, tr))
	}
	if remind != "" {
		spoken = append(spoken, SpokenRemind(remind, tr))
	}
	if c.HasAttachments {
		spoken = append(spoken, SpokenAttachments(tr))
	}
	if c.Unread {
		spoken = append(spoken, SpokenUnread(tr))
	}
	return Row{
		ID: c.ID, State: st, Person: person, Time: ctx.dates.Date(c.Date, ctx.now), Title: t.text,
		TitleIsAssistant: t.assistant, Snippet: snippet, SnippetIsAssistant: snippetIsAssistant,
		Account: ctx.accountName(c.Account), IssueKey: issueKey, IssueStatus: issueStatus, IssueStyle: issueStyle,
		Due: due, Remind: remind, Attachments: c.HasAttachments, CountText: countText(n), Unread: c.Unread,
		Spoken: sentences(spoken),
	}
}

func (ctx viewContext) detail(c Case) Detail {
	tr := ctx.tr
	st := StateOf(c, ctx.s.Annotated)
	source := StateSourceOf(c, ctx.s.Annotated)
	a := ctx.annotation(c)
	t := ctx.titled(c)
	subject := ctx.subject(c)

	why := ""
	if a != nil {
		why = CleanLine(a.Why, capReason)
	}
	whyIsAssistant := why != ""
	if why == "" {
		why = Reason(string(c.RuleReason), tr)
	}
	// The summary box is the assistant's: no snippet stands in for it (the
	// row's snippet does fall back, and the conversation shows the text
	// anyway).
	var summary, due, dueQuote string
	var tasks []string
	if a != nil {
		summary = CleanBlock(a.Summary, capSummary)
		if a.Due != nil {
			due = ctx.dueLabel(*a.Due)
			dueQuote = CleanLine(a.DueQuote, capQuote)
		}
		for _, task := range a.Tasks[:min(len(a.Tasks), capTaskScan)] {
			if len(tasks) == capTasks {
				break
			}
			if s := CleanLine(task, capTask); s != "" {
				tasks = append(tasks, s)
			}
		}
	}
	var issue *IssueInfo
	if c.Issue != nil {
		issue = &IssueInfo{
			Key: CleanLine(c.Issue.Key, capIssueKey), Status: CleanLine(c.Issue.Status, capStatus), Style: c.Issue.Style,
		}
	}
	var messages []MessageCard
	for _, m := range newestMessages(c.Messages, capMessages) {
		from := You(tr)
		if !m.Mine {
			from = CleanLine(m.From, capPerson)
		}
		messages = append(messages, MessageCard{
			ID: m.ID, From: from, When: ctx.dates.Date(m.Date, ctx.now), Text: CleanBlock(m.Text, capMessage),
			Mine: m.Mine,
		})
	}
	messagesNote := ""
	if !c.MessagesLoaded {
		messagesNote = MessagesLoading(tr)
		if c.MessagesFailed {
			messagesNote = MessagesFailed(tr)
		}
	}
	draft, draftID := "", api.DraftID("")
	if c.Draft != nil {
		draft = CleanBlock(c.Draft.Text, capDraft)
		draftID = c.Draft.ID
	}
	staleNote := ""
	if ctx.s.Annotated && c.Annotation != nil && c.Annotation.Stale {
		staleNote = StaleNotes(tr)
	}
	shownSubject := subject
	if t.text == subject {
		shownSubject = ""
	}
	remindText := ""
	at, snoozed := c.Visibility.RemindAt()
	if snoozed {
		remindText = SnoozedUntil(ctx.remindLabel(at), tr)
	}
	model := ""
	if ctx.s.Run != nil {
		model = CleanLine(ctx.s.Run.Model, capModel)
	}
	return Detail{
		ID: c.ID, AccountID: c.Account, Thread: c.Thread, Reply: c.Reply, LatestMessage: c.LatestMessage,
		State: st, StateTitle: StateName(st, tr), Source: source, Why: why, WhyIsAssistant: whyIsAssistant,
		SourceText: SourceText(source, model, tr), Account: ctx.accountName(c.Account), Issue: issue,
		Person: CleanLine(c.Person, capPerson), Time: ctx.dates.DateTime(c.Date), Title: t.text,
		TitleIsAssistant: t.assistant, SpokenTitle: t.spoken, Subject: shownSubject, Due: due, DueQuote: dueQuote,
		Summary: summary, Tasks: tasks, Draft: draft, DraftID: draftID, CanUnstar: CanUnstar(c),
		StaleNote: staleNote, IsDone: c.Visibility.IsDone(), IsSnoozed: snoozed, RemindText: remindText,
		CanArchive: c.CanArchive, ConversationTitle: Conversation(max(1, c.MessageCount), tr), Messages: messages,
		MessagesLoading: !c.MessagesLoaded && !c.MessagesFailed, MessagesNote: messagesNote,
		MessagesRetry: !c.MessagesLoaded && c.MessagesFailed,
	}
}

// newestMessages is the newest n of ms, oldest first, as a stable sort by
// date would leave them (of equal dates, the later in ms is newer),
// without sorting all of ms.
func newestMessages(ms []CaseMessage, n int) []CaseMessage {
	if n <= 0 || len(ms) == 0 {
		return nil
	}
	// Indices into ms, ordered by date and index.
	kept := make([]int, 0, min(n, len(ms)))
	for i := range ms {
		d := ms[i].Date
		if len(kept) == n {
			if d.Before(ms[kept[0]].Date) {
				continue
			}
			kept = slices.Delete(kept, 0, 1)
		}
		j := len(kept)
		for j > 0 && ms[kept[j-1]].Date.After(d) {
			j--
		}
		kept = slices.Insert(kept, j, i)
	}
	out := make([]CaseMessage, len(kept))
	for k, i := range kept {
		out[k] = ms[i]
	}
	return out
}

// countText is the badge of a case's row: the message count from two on,
// nothing below (widget.ThreadCountText).
func countText(n int) string {
	if n < 2 {
		return ""
	}
	return strconv.Itoa(n)
}

// sentences joins the parts as sentences: each ends with a full stop
// unless it ends with punctuation already; empty parts go.
func sentences(parts []string) string {
	var out []string
	for _, p := range parts {
		if p == "" {
			continue
		}
		if !strings.HasSuffix(p, ".") && !strings.HasSuffix(p, "?") && !strings.HasSuffix(p, "!") &&
			!strings.HasSuffix(p, "…") {
			p += "."
		}
		out = append(out, p)
	}
	return strings.Join(out, " ")
}
