// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The board's controller over an in-memory source: what the user looks
// at, what they decide about a case, and which Changes the page is told
// about (macOS BoardControllerTests.swift, the same cases).

// sampleCases are, in the list under Overview: c1 hot, c3 c2 you (c3 is
// newer), c4 them, c5 info; d1 and d2 done.
func sampleCases() []Case {
	return casesOf(
		mk("c1", StateHot, withHours(4)), mk("c2", StateYou, withHours(3)), mk("c3", StateYou, withHours(2)),
		mk("c4", StateThem, withHours(1)), mk("c5", StateInfo, withAccount(accountB), withHours(5)),
		mk("d1", StateInfo, withHours(6), withDone()), mk("d2", StateInfo, withHours(7), withDone()),
	)
}

// probe is the controller's observer: every OnChange, in order.
type probe struct{ log []Changes }

func (p *probe) changes(c Changes) { p.log = append(p.log, c) }

func controllerOptions(now func() time.Time) ControllerOptions {
	if now == nil {
		now = func() time.Time { return testNow }
	}
	return ControllerOptions{Env: testEnv, Now: now}
}

// makeController is a controller over an in-memory source of cases.
func makeController(cases []Case, annotated bool, now func() time.Time) (*Controller, *InMemorySource, *probe) {
	src := NewInMemorySource(Snapshot{Accounts: testAccounts, Cases: cases, Annotated: annotated, Phase: PhaseReady}, tr)
	c := NewController(src, controllerOptions(now))
	p := &probe{}
	c.OnChange = p.changes
	return c, src, p
}

func defaultController() (*Controller, *InMemorySource, *probe) {
	return makeController(sampleCases(), false, nil)
}

func ctrlRows(c *Controller) []string { return rowsOf(c.View()) }

// lateSource holds the user's writes until flush, as a daemon-backed one
// reports them when the daemon answers.
type lateSource struct {
	snapshot  Snapshot
	h         Handlers
	queued    []func(*Snapshot)
	loads     []CaseID
	refreshes int
}

func (l *lateSource) Snapshot() Snapshot     { return l.snapshot }
func (l *lateSource) SetHandlers(h Handlers) { l.h = h }

// change queues f over case id, on a new slice of cases.
func (l *lateSource) change(id CaseID, f func(*Case)) {
	l.queued = append(l.queued, func(s *Snapshot) {
		cases := slices.Clone(s.Cases)
		for i := range cases {
			if cases[i].ID == id {
				f(&cases[i])
			}
		}
		s.Cases = cases
	})
}

func (l *lateSource) SetState(id CaseID, state *State) {
	l.change(id, func(c *Case) { c.UserState = state })
}
func (l *lateSource) SetDone(id CaseID, done bool) { l.change(id, func(c *Case) { c.SetDone(done) }) }
func (l *lateSource) Remind(id CaseID, until *time.Time) {
	l.change(id, func(c *Case) {
		if until != nil {
			c.Visibility = Visibility{Kind: VisibleSnoozed, At: *until}
		} else {
			c.Visibility = Visibility{}
		}
	})
}
func (l *lateSource) Archive(id CaseID)                             { l.SetDone(id, true) }
func (l *lateSource) SetCommitmentDone(api.BoardCommitmentID, bool) {}
func (l *lateSource) DiscardDraft(CaseID)                           {}
func (l *lateSource) Unflag(CaseID)                                 {}
func (l *lateSource) LoadMessages(id CaseID)                        { l.loads = append(l.loads, id) }
func (l *lateSource) Refresh()                                      { l.refreshes++ }
func (l *lateSource) DiscardStoredDraft(id CaseID, _ api.DraftID, _ api.AccountID, done func(error)) {
	done(nil)
}

func (l *lateSource) flush() {
	for _, q := range l.queued {
		q(&l.snapshot)
	}
	l.queued = nil
	if l.h.Change != nil {
		l.h.Change()
	}
}

// setCase changes case id of the source's snapshot in place of the daemon.
func (l *lateSource) setCase(id CaseID, f func(*Case)) {
	cases := slices.Clone(l.snapshot.Cases)
	for i := range cases {
		if cases[i].ID == id {
			f(&cases[i])
		}
	}
	l.snapshot.Cases = cases
}

// without is cs without case id, on a new slice.
func without(cs []Case, id CaseID) []Case {
	var out []Case
	for _, c := range cs {
		if c.ID != id {
			out = append(out, c)
		}
	}
	return out
}

func caseIn(s Snapshot, id CaseID) Case {
	c, _ := s.Case(id)
	return c
}

// Initial state.

func TestControllerInitialState(t *testing.T) {
	c, src, p := defaultController()
	st := c.State()
	check(t, st.Style == StyleList && st.Filter.Kind == FilterAll && st.Account == "", "state %+v", st)
	check(t, st.InlineDetail && !st.RevealsWhy, "state %+v", st)
	eq(t, "selection", st.Selection, CaseID("c1")) // the list selects its first row
	check(t, c.View().Selection == "c1" && c.View().Detail.ID == "c1" && !c.View().ShowsPanel, "view")
	eq(t, "rows", ctrlRows(c), []string{"c1", "c3", "c2", "c4", "c5"})
	check(t, src.Handlers().Change != nil, "no handler installed")
	check(t, len(p.log) == 0, "something reported at construction")
}

func TestControllerEmptyBoard(t *testing.T) {
	c, _, _ := makeController(nil, false, nil)
	check(t, c.View().IsEmpty && c.State().Selection == "" && c.View().Detail == nil, "not empty")
}

func TestChangesBits(t *testing.T) {
	eq(t, "bits", []Changes{ChangeContent, ChangeSelection, ChangeStyle, ChangeFilters}, []Changes{1, 2, 4, 8})
	check(t, (ChangeStyle|ChangeSelection).Has(ChangeStyle) && !ChangeStyle.Has(ChangeStyle|ChangeSelection), "Has")
}

// Style.

func TestControllerSetStyle(t *testing.T) {
	c, _, p := defaultController()
	c.SetStyle(StyleColumns)
	check(t, c.State().Style == StyleColumns && c.State().Selection == "" && c.View().Detail == nil, "columns")
	eq(t, "log", p.log, []Changes{ChangeStyle | ChangeSelection})
	c.SetStyle(StyleColumns)
	eq(t, "the same style: silent", len(p.log), 1)
	c.SetStyle(StyleToday)
	eq(t, "today", p.log, []Changes{ChangeStyle | ChangeSelection, ChangeStyle})
	c.SetStyle(StyleList) // entering the list with the detail beside it selects the first row
	check(t, c.State().Selection == "c1" && !c.View().ShowsPanel, "list")
	eq(t, "last", p.log[len(p.log)-1], ChangeStyle|ChangeSelection)
}

func TestLeavingTheListClearsTheSelection(t *testing.T) {
	for _, style := range []Style{StyleColumns, StyleToday} {
		c, _, p := defaultController()
		c.Select("c3")
		p.log = nil
		c.SetStyle(style)
		eq(t, "selection", c.State().Selection, CaseID(""))
		eq(t, "log", p.log, []Changes{ChangeStyle | ChangeSelection})
	}
}

func TestEnteringTheListWithoutInlineDetailSelectsNothing(t *testing.T) {
	c, _, p := defaultController()
	c.SetInlineDetail(false)
	c.SetStyle(StyleColumns)
	p.log = nil
	c.SetStyle(StyleList)
	eq(t, "selection", c.State().Selection, CaseID(""))
	eq(t, "log", p.log, []Changes{ChangeStyle})
}

func TestStyleChangeResetsWhy(t *testing.T) {
	c, _, _ := defaultController()
	c.ToggleWhy()
	check(t, c.State().RevealsWhy, "not open")
	c.SetStyle(StyleColumns)
	check(t, !c.State().RevealsWhy, "still open")
}

// Filters.

func TestControllerSetFilter(t *testing.T) {
	c, _, p := defaultController()
	c.SetFilter(filterState(StateYou))
	check(t, c.State().Filter.Same(filterState(StateYou)), "filter %+v", c.State().Filter)
	eq(t, "selection", c.State().Selection, CaseID("c3")) // the first row of the filter
	eq(t, "log", p.log, []Changes{ChangeFilters | ChangeSelection | ChangeContent})
	c.SetFilter(filterState(StateYou))
	eq(t, "silent", len(p.log), 1)
	c.SetFilter(doneFilter)
	eq(t, "done selection", c.State().Selection, CaseID("d1"))
	eq(t, "done rows", ctrlRows(c), []string{"d1", "d2"})
	c.SetFilter(filterState(StateInfo)) // c5 is the live info case
	eq(t, "info selection", c.State().Selection, CaseID("c5"))
}

func TestFilterWithNothingSelectsNothing(t *testing.T) {
	c, _, _ := makeController(casesOf(mk("c1", StateYou)), false, nil)
	c.SetFilter(filterState(StateHot))
	check(t, c.State().Selection == "" && c.View().Detail == nil && len(c.View().Sections) == 0, "hot")
}

func TestFilterInColumnsLeavesNoSelection(t *testing.T) {
	c, _, p := defaultController()
	c.SetStyle(StyleColumns)
	p.log = nil
	c.SetFilter(filterState(StateHot))
	eq(t, "selection", c.State().Selection, CaseID(""))
	eq(t, "log", p.log, []Changes{ChangeFilters | ChangeContent}) // the list behind it changed, the columns did not
	eq(t, "columns", len(c.View().Columns), 4)
}

func TestControllerSetAccount(t *testing.T) {
	c, _, p := defaultController()
	c.SetAccount(accountB)
	eq(t, "account", c.State().Account, accountB)
	check(t, slices.Equal(ctrlRows(c), []string{"c5"}) && c.State().Selection == "c5", "rows %v", ctrlRows(c))
	eq(t, "subtitle", c.View().Subtitle, "Beta · 1 case")
	eq(t, "log", p.log, []Changes{ChangeFilters | ChangeSelection | ChangeContent})
	c.SetAccount(accountB)
	eq(t, "silent", len(p.log), 1)
	c.SetAccount("")
	eq(t, "selection", c.State().Selection, CaseID("c1"))
	eq(t, "count", len(p.log), 2)
}

func TestFilterChangeResetsWhy(t *testing.T) {
	c, _, _ := defaultController()
	c.ToggleWhy()
	c.SetFilter(filterState(StateYou))
	check(t, !c.State().RevealsWhy, "filter")
	c.ToggleWhy()
	c.SetAccount(accountB)
	check(t, !c.State().RevealsWhy, "account")
}

// Selection.

func TestControllerSelect(t *testing.T) {
	c, _, p := defaultController()
	c.Select("c3")
	check(t, c.State().Selection == "c3" && c.View().Detail.ID == "c3", "c3")
	eq(t, "log", p.log, []Changes{ChangeSelection}) // another case's detail is no content change
	c.Select("c3")
	eq(t, "silent", len(p.log), 1)
	// A case that is not shown resolves like nothing: the first row.
	c.Select("nope")
	eq(t, "nope", c.State().Selection, CaseID("c1"))
	c.Select("d1") // done, and the list shows live cases
	eq(t, "d1", c.State().Selection, CaseID("c1"))
	c.Select("")
	eq(t, "none", c.State().Selection, CaseID("c1"))
	eq(t, "log after", p.log, []Changes{ChangeSelection, ChangeSelection})
}

func TestSelectInColumnsOpensThePanel(t *testing.T) {
	c, _, p := defaultController()
	c.SetStyle(StyleColumns)
	p.log = nil
	c.Select("c4")
	check(t, c.State().Selection == "c4" && c.View().ShowsPanel, "c4")
	eq(t, "log", p.log, []Changes{ChangeSelection})
	c.Select("")
	check(t, c.State().Selection == "" && !c.View().ShowsPanel, "none")
	eq(t, "log 2", p.log, []Changes{ChangeSelection, ChangeSelection})
	c.Select("")
	eq(t, "silent", len(p.log), 2)
	c.Select("d1") // done: not on the board
	eq(t, "d1", c.State().Selection, CaseID(""))
}

func TestToggleWhyAndItsReset(t *testing.T) {
	c, _, p := defaultController()
	c.ToggleWhy()
	check(t, c.State().RevealsWhy && c.View().Detail != nil, "open")
	eq(t, "log", p.log, []Changes{ChangeSelection})
	c.Select("c1") // the same case: stays open
	check(t, c.State().RevealsWhy && len(p.log) == 1, "the same case")
	c.Select("c2")
	check(t, !c.State().RevealsWhy, "another case")
	eq(t, "log 2", p.log, []Changes{ChangeSelection, ChangeSelection})
	c.ToggleWhy()
	c.ToggleWhy()
	check(t, !c.State().RevealsWhy && len(p.log) == 4, "toggled twice: %v", p.log)
}

func TestToggleWhyNeedsASelection(t *testing.T) {
	c, _, p := defaultController()
	c.SetStyle(StyleColumns)
	p.log = nil
	c.ToggleWhy()
	check(t, !c.State().RevealsWhy && len(p.log) == 0, "toggled without a selection")
}

func TestControllerSetInlineDetail(t *testing.T) {
	c, _, p := defaultController()
	c.SetInlineDetail(true)
	check(t, len(p.log) == 0, "the same: reported")
	c.SetInlineDetail(false) // narrow: the panel never opens by itself
	check(t, !c.State().InlineDetail && c.State().Selection == "" && !c.View().ShowsPanel, "narrow")
	eq(t, "log", p.log, []Changes{ChangeSelection})
	c.Select("c2") // a deliberate selection opens the panel
	check(t, c.View().ShowsPanel, "no panel")
	c.SetInlineDetail(true) // wide again: beside the list, the selection stays
	check(t, c.State().Selection == "c2" && !c.View().ShowsPanel, "wide")
	eq(t, "log 3", p.log, []Changes{ChangeSelection, ChangeSelection, ChangeSelection})
	c.SetInlineDetail(false)
	c.SetInlineDetail(true) // nothing selected: the first row
	eq(t, "first row", c.State().Selection, CaseID("c1"))
}

func TestShowWaitingForYou(t *testing.T) {
	c, _, p := defaultController()
	c.SetAccount(accountA)
	c.SetStyle(StyleColumns)
	p.log = nil
	c.ShowWaitingForYou()
	check(t, c.State().Style == StyleList && c.State().Filter.Same(filterState(StateYou)), "state %+v", c.State())
	eq(t, "account kept", c.State().Account, accountA)
	eq(t, "selection", c.State().Selection, CaseID("c3"))
	eq(t, "log", p.log, []Changes{ChangeStyle | ChangeFilters | ChangeSelection | ChangeContent})
	// Already there: the selection stays and nothing is reported.
	c.Select("c2")
	p.log = nil
	c.ShowWaitingForYou()
	check(t, c.State().Selection == "c2" && len(p.log) == 0, "again: %v", p.log)
}

// What the user decides.

func TestSetStateMovesTheCase(t *testing.T) {
	c, src, p := defaultController()
	c.SetState("c3", StateThem)
	check(t, caseIn(src.Snapshot(), "c3").UserState != nil && *caseIn(src.Snapshot(), "c3").UserState == StateThem, "not moved")
	eq(t, "kinds", sectionKinds(c.View()), []string{"hot", "you", "them", "info"})
	eq(t, "rows", ctrlRows(c), []string{"c1", "c2", "c4", "c3", "c5"})
	eq(t, "nav", navCounts(c.View()), []int{5, 1, 1, 2, 1, 2})
	eq(t, "selection", c.State().Selection, CaseID("c1"))
	eq(t, "log", p.log, []Changes{ChangeContent})
	c.SetState("c3", StateThem) // nothing changes
	c.SetState("missing", StateHot)
	eq(t, "silent", len(p.log), 1)
}

func TestMovingTheSelectedCaseKeepsItSelectedWhileItIsShown(t *testing.T) {
	c, _, p := defaultController()
	c.Select("c3")
	p.log = nil
	c.SetState("c3", StateInfo)
	eq(t, "selection", c.State().Selection, CaseID("c3"))
	check(t, c.View().Detail.State == StateInfo && c.View().Detail.Source == Source{Kind: SourceUser}, "detail")
	eq(t, "log", p.log, []Changes{ChangeContent}) // the same case's changed detail is content
}

func TestMovingTheSelectedCaseOutOfTheFilterSelectsTheNextRow(t *testing.T) {
	c, _, p := defaultController()
	c.SetFilter(filterState(StateYou))
	eq(t, "selection", c.State().Selection, CaseID("c3"))
	p.log = nil
	c.SetState("c3", StateHot)
	check(t, slices.Equal(ctrlRows(c), []string{"c2"}) && c.State().Selection == "c2", "rows %v", ctrlRows(c))
	eq(t, "log", p.log, []Changes{ChangeSelection | ChangeContent})
	c.SetState("c2", StateThem) // the last row: nothing is left
	check(t, c.State().Selection == "" && c.View().Detail == nil, "left")
}

func TestSetStateBackToAutomatic(t *testing.T) {
	ann := Annotation{State: optState(StateHot), Title: "T"}
	c, src, _ := makeController(casesOf(mk("c1", StateYou, withAnnotation(ann))), true, nil)
	user := func() *State { return src.Snapshot().Cases[0].UserState }
	// The automatic state is the assistant's (hot), not the rules' (you).
	c.SetState("c1", StateThem)
	check(t, user() != nil && *user() == StateThem, "them")
	c.SetState("c1", StateHot) // the same as automatic: back to automatic
	check(t, user() == nil && c.View().Detail.Source == Source{Kind: SourceAssistantChanged, From: StateYou}, "hot")
	c.SetState("c1", StateYou) // the rules' state is not automatic here: the user's choice
	check(t, user() != nil && *user() == StateYou && c.View().Detail.Source == Source{Kind: SourceUser}, "you")
	c.SetState("c1", StateHot)
	check(t, user() == nil, "hot again")

	// Without annotations the rules decide.
	d, src2, _ := makeController(casesOf(mk("c1", StateYou, withAnnotation(ann))), false, nil)
	d.SetState("c1", StateThem)
	d.SetState("c1", StateYou)
	check(t, src2.Snapshot().Cases[0].UserState == nil, "you is automatic")
	d.SetState("c1", StateHot)
	check(t, src2.Snapshot().Cases[0].UserState != nil && *src2.Snapshot().Cases[0].UserState == StateHot, "hot")
}

func TestMarkDoneInTheListSelectsTheNextRow(t *testing.T) {
	c, src, p := defaultController()
	c.Select("c3")
	p.log = nil
	c.MarkDone("c3")
	check(t, caseIn(src.Snapshot(), "c3").Done(), "not done")
	eq(t, "rows", ctrlRows(c), []string{"c1", "c2", "c4", "c5"})
	eq(t, "selection", c.State().Selection, CaseID("c2")) // the row that followed
	eq(t, "log", p.log, []Changes{ChangeSelection | ChangeContent})
	c.Select("c5")
	c.MarkDone("c5") // the last row: the previous one
	eq(t, "last", c.State().Selection, CaseID("c4"))
	c.Select("c1")
	c.MarkDone("c1") // the first row: the next
	eq(t, "first", c.State().Selection, CaseID("c2"))
}

func TestMarkDoneTheOnlyRow(t *testing.T) {
	c, _, _ := makeController(casesOf(mk("c1", StateYou)), false, nil)
	c.MarkDone("c1")
	check(t, c.State().Selection == "" && c.View().Detail == nil && len(c.View().Sections) == 0, "left")
	check(t, !c.View().IsEmpty, "a done case does not count") // a done case still counts
}

func TestMarkDoneUnderAFilter(t *testing.T) {
	c, _, _ := defaultController()
	c.SetFilter(filterState(StateYou))
	c.MarkDone("c3")
	eq(t, "c2", c.State().Selection, CaseID("c2"))
	c.MarkDone("c2")
	eq(t, "none", c.State().Selection, CaseID(""))
}

func TestMarkDoneInColumnsAndTodayClearsTheSelection(t *testing.T) {
	for _, style := range []Style{StyleColumns, StyleToday} {
		c, _, p := defaultController()
		c.SetStyle(style)
		c.Select("c2")
		check(t, c.View().ShowsPanel, "no panel")
		p.log = nil
		c.MarkDone("c2")
		check(t, c.State().Selection == "" && !c.View().ShowsPanel && c.View().Detail == nil, "style %d", style)
		eq(t, "log", p.log, []Changes{ChangeSelection | ChangeContent})
	}
}

func TestMarkDoneAnotherCaseKeepsTheSelection(t *testing.T) {
	c, _, p := defaultController()
	c.MarkDone("c4")
	eq(t, "selection", c.State().Selection, CaseID("c1"))
	eq(t, "log", p.log, []Changes{ChangeContent})
	c.MarkDone("c4") // already done: silent
	c.MarkDone("missing")
	eq(t, "silent", len(p.log), 1)
}

func TestControllerReopen(t *testing.T) {
	c, src, p := defaultController()
	c.SetFilter(doneFilter)
	eq(t, "selection", c.State().Selection, CaseID("d1"))
	p.log = nil
	c.Reopen("d1")
	check(t, !caseIn(src.Snapshot(), "d1").Done(), "still done")
	check(t, slices.Equal(ctrlRows(c), []string{"d2"}) && c.State().Selection == "d2", "rows %v", ctrlRows(c))
	eq(t, "log", p.log, []Changes{ChangeSelection | ChangeContent})
	c.Reopen("d2")
	check(t, c.State().Selection == "" && len(c.View().Sections) == 0, "left")
	// The reopened cases are back on the board.
	c.SetFilter(Filter{})
	check(t, slices.Contains(ctrlRows(c), "d1") && slices.Contains(ctrlRows(c), "d2"), "rows %v", ctrlRows(c))
}

func TestReopenFromAnotherViewKeepsTheSelection(t *testing.T) {
	c, _, p := defaultController()
	c.Reopen("d1") // from the list under Overview: a new row appears
	eq(t, "selection", c.State().Selection, CaseID("c1"))
	eq(t, "log", p.log, []Changes{ChangeContent})
}

func TestControllerDiscardDraft(t *testing.T) {
	ann := Annotation{State: optState(StateYou), Title: "T"}
	c, src, p := makeController(casesOf(mk("c1", StateYou, withAnnotation(ann), withDraft("Hi,\n\nbye")), mk("c2", StateYou, withHours(2))), true, nil)
	eq(t, "draft", c.View().Detail.Draft, "Hi,\n\nbye")
	c.DiscardDraft("c1")
	check(t, src.Snapshot().Cases[0].Draft == nil && reflect.DeepEqual(*src.Snapshot().Cases[0].Annotation, ann), "discarded")
	check(t, c.View().Detail.Draft == "" && c.View().Detail.Title == "T", "detail")
	eq(t, "log", p.log, []Changes{ChangeContent})
	c.DiscardDraft("c1") // nothing left to discard
	c.DiscardDraft("c2") // no draft
	c.DiscardDraft("missing")
	eq(t, "silent", len(p.log), 1)
	// The inline editor's Discard on the samples' source: the link goes.
	c2, src2, _ := makeController(casesOf(mk("c1", StateYou, withDraft("x"))), false, nil)
	called := false
	c2.DiscardStoredDraft("c1", "d_c1", accountA, func(err error) {
		called = true
		check(t, err == nil, "stored draft: %v", err)
	})
	check(t, called && src2.Snapshot().Cases[0].Draft == nil, "the stored draft stays")
}

// Refresh and the source changing from outside.

func TestRefreshWithoutChangeIsSilent(t *testing.T) {
	c, _, p := defaultController()
	before := c.View()
	c.Refresh()
	check(t, reflect.DeepEqual(c.View(), before) && len(p.log) == 0, "refresh changed something")
}

func TestRefreshFollowsTheClock(t *testing.T) {
	now := testNow
	due := day(16, 10, 0)
	c, _, p := makeController(casesOf(mk("c1", StateYou, withAnnotation(Annotation{State: optState(StateYou), Title: "T", Due: &due}))),
		true, func() time.Time { return now })
	eq(t, "due", c.View().Sections[0].Rows[0].Due, "Tomorrow")
	eq(t, "group", c.View().Today.DueGroups[0].Kind, DueTomorrow)
	now = day(16, 9, 0) // a new day moves the deadline
	c.Refresh()
	eq(t, "due today", c.View().Sections[0].Rows[0].Due, "Today")
	eq(t, "group today", c.View().Today.DueGroups[0].Kind, DueToday)
	eq(t, "log", p.log, []Changes{ChangeContent})
}

func TestASourceChangeFromOutside(t *testing.T) {
	c, src, p := defaultController()
	// A new, newer hot case: content only, the selection stays.
	s := src.Snapshot()
	s.Cases = append(slices.Clone(s.Cases), mk("c6", StateHot, withHours(0.5)))
	src.Replace(s)
	check(t, ctrlRows(c)[0] == "c6" && c.State().Selection == "c1", "rows %v", ctrlRows(c))
	eq(t, "log", p.log, []Changes{ChangeContent})
	// The same snapshot again: silent.
	src.Replace(s)
	eq(t, "silent", len(p.log), 1)
	// The selected case disappears: the first row.
	s.Cases = without(s.Cases, "c1")
	src.Replace(s)
	eq(t, "selection", c.State().Selection, CaseID("c6"))
	eq(t, "last", p.log[len(p.log)-1], ChangeSelection|ChangeContent)
}

func TestAWriteStraightToTheSourceIsFollowed(t *testing.T) {
	c, src, p := defaultController()
	src.SetDone("c1", true) // not through the controller
	check(t, c.State().Selection == "c3", "selection %q", c.State().Selection)
	eq(t, "log", p.log, []Changes{ChangeSelection | ChangeContent})
	src.SetState("c2", optState(StateThem))
	eq(t, "nav", navCounts(c.View()), []int{4, 0, 1, 2, 1, 3})
}

func TestReplaceCanEmptyTheBoard(t *testing.T) {
	c, src, p := defaultController()
	c.Select("c3")
	p.log = nil
	src.Replace(EmptySnapshot())
	check(t, c.View().IsEmpty && c.State().Selection == "" && c.View().Detail == nil, "not empty")
	eq(t, "log", p.log, []Changes{ChangeSelection | ChangeContent})
	// Data arriving later fills it again.
	src.Replace(Snapshot{Accounts: testAccounts, Cases: sampleCases(), Phase: PhaseReady})
	check(t, !c.View().IsEmpty && c.State().Selection == "c1", "refilled")
}

func TestAWriteToAnUnknownCaseCallsNoOne(t *testing.T) {
	c, _, p := defaultController()
	c.SetState("nope", StateHot)
	c.MarkDone("nope")
	c.Reopen("nope")
	c.DiscardDraft("nope")
	check(t, len(p.log) == 0 && c.State().Selection == "c1", "log %v", p.log)
}

// The departure after a write applies once.

// TestADepartureDoesNotOutliveTheReportOfItsWrite: the selected case moved
// where it stays shown, then dropped by the source, follows the rule for
// any outside change, not the neighbour noted before the move.
func TestADepartureDoesNotOutliveTheReportOfItsWrite(t *testing.T) {
	table := []struct {
		style  Style
		inline bool
		want   CaseID
	}{
		{StyleList, true, "c1"}, {StyleList, false, ""}, {StyleColumns, true, ""}, {StyleToday, true, ""},
	}
	for _, row := range table {
		c, src, _ := defaultController()
		c.SetStyle(row.style)
		c.SetInlineDetail(row.inline)
		c.Select("c3")
		c.SetState("c3", StateInfo) // still shown
		eq(t, "shown", c.State().Selection, CaseID("c3"))
		s := src.Snapshot()
		s.Cases = without(s.Cases, "c3")
		src.Replace(s)
		if c.State().Selection != row.want {
			t.Errorf("%d %v: selection %q, want %q", row.style, row.inline, c.State().Selection, row.want)
		}
	}
}

func TestAWriteThatChangesNothingNotesNoDeparture(t *testing.T) {
	c, src, p := defaultController()
	c.Select("c3")
	p.log = nil
	c.SetState("c3", StateYou) // its automatic state already: nothing to write
	c.MarkDone("d1")           // done already
	c.Reopen("c3")             // not done
	check(t, len(p.log) == 0, "log %v", p.log)
	s := src.Snapshot()
	s.Cases = without(s.Cases, "c3")
	src.Replace(s)
	eq(t, "selection", c.State().Selection, CaseID("c1")) // the first row, not c2
}

func TestASourceThatReportsLater(t *testing.T) {
	// Nothing in between: the departure waits for the report.
	{
		src := &lateSource{snapshot: testSnapshot(sampleCases()...)}
		c := NewController(src, controllerOptions(nil))
		c.Select("c3")
		c.MarkDone("c3")
		eq(t, "not reported yet", c.State().Selection, CaseID("c3"))
		src.flush()
		check(t, c.State().Selection == "c2" && !slices.Contains(ctrlRows(c), "c3"), "after %q", c.State().Selection)
	}
	// The user selects another case meanwhile: their choice wins.
	{
		src := &lateSource{snapshot: testSnapshot(sampleCases()...)}
		c := NewController(src, controllerOptions(nil))
		c.Select("c3")
		c.MarkDone("c3")
		c.Select("c4")
		src.flush()
		eq(t, "the user's choice", c.State().Selection, CaseID("c4"))
		// And a later report removing c4 follows the rule for outside changes.
		src.snapshot.Cases = without(src.snapshot.Cases, "c4")
		src.flush()
		eq(t, "outside", c.State().Selection, CaseID("c1"))
	}
}

func TestMarkDoneInTheNarrowList(t *testing.T) {
	c, src, p := defaultController()
	c.SetInlineDetail(false)
	c.Select("c3")
	check(t, c.View().ShowsPanel, "no panel")
	p.log = nil
	c.MarkDone("c3")
	check(t, c.State().Selection == "c2" && c.View().ShowsPanel, "the panel moves to the next row")
	eq(t, "log", p.log, []Changes{ChangeSelection | ChangeContent})
	// An unrelated change later keeps it; dropping it selects nothing (no first row here).
	s := src.Snapshot()
	s.Cases = append(slices.Clone(s.Cases), mk("c6", StateHot, withHours(0.5)))
	src.Replace(s)
	eq(t, "kept", c.State().Selection, CaseID("c2"))
	s.Cases = without(s.Cases, "c2")
	src.Replace(s)
	check(t, c.State().Selection == "" && !c.View().ShowsPanel, "dropped")
}

// An account filter whose account goes away.

func TestAVanishedAccountFallsBackToAll(t *testing.T) {
	c, src, p := defaultController()
	c.SetAccount(accountB)
	check(t, c.State().Account == accountB && slices.Equal(ctrlRows(c), []string{"c5"}), "account B")
	p.log = nil
	s := src.Snapshot()
	s.Accounts = []AccountInfo{testAccounts[0]}
	var cases []Case
	for _, k := range s.Cases {
		if k.Account != accountB {
			cases = append(cases, k)
		}
	}
	s.Cases = cases
	src.Replace(s)
	eq(t, "account", c.State().Account, api.AccountID(""))
	check(t, slices.Equal(ctrlRows(c), []string{"c1", "c3", "c2", "c4"}) && c.State().Selection == "c1", "rows %v", ctrlRows(c))
	for _, a := range c.View().Accounts {
		check(t, a.Filter != accountB, "B is still listed")
	}
	check(t, c.View().Accounts[0].Selected && c.View().AccountTitle == "All Accounts", "all accounts")
	eq(t, "log", p.log, []Changes{ChangeFilters | ChangeSelection | ChangeContent})
	// Choosing an account that is not there is no change.
	c.SetAccount(accountB)
	check(t, c.State().Account == "" && len(p.log) == 1, "account %q, log %v", c.State().Account, p.log)
}

// Calls from inside OnChange.

func TestAListenerThatSelectsGetsItsChangeAfterwards(t *testing.T) {
	c, _, _ := defaultController()
	type entry struct {
		changes   Changes
		selection CaseID
	}
	var log []entry
	nested := false
	c.OnChange = func(changes Changes) {
		log = append(log, entry{changes, c.View().Selection})
		check(t, c.View().Selection == c.State().Selection, "view and state differ")
		if !nested {
			nested = true
			c.Select("c4")
			// Not yet delivered: this call is still the outer one.
			eq(t, "inside", len(log), 1)
		}
	}
	c.SetStyle(StyleColumns)
	eq(t, "log", log, []entry{{ChangeStyle | ChangeSelection, ""}, {ChangeSelection, "c4"}})
	eq(t, "selection", c.State().Selection, CaseID("c4"))
}

func TestAListenerThatMarksDoneGetsItsChangeAfterwards(t *testing.T) {
	c, _, _ := defaultController()
	type entry struct {
		changes   Changes
		selection CaseID
		hasC3     bool
	}
	var log []entry
	nested := false
	c.OnChange = func(changes Changes) {
		log = append(log, entry{changes, c.View().Selection, slices.Contains(ctrlRows(c), "c3")})
		if !nested {
			nested = true
			c.MarkDone("c3")
			eq(t, "inside", len(log), 1)
		}
	}
	c.Select("c3")
	eq(t, "log", log, []entry{{ChangeSelection, "c3", true}, {ChangeSelection | ChangeContent, "c2", false}})
}

func TestDummySources(t *testing.T) {
	on := NewDummySource(true, testNow, time.UTC, tr)
	check(t, on.Snapshot().Equal(SampleSnapshot(testNow, time.UTC)), "not the samples")
	off := NewDummySource(false, testNow, time.UTC, tr)
	check(t, off.Snapshot().Equal(EmptySnapshot()), "not empty")
	c := NewController(on, controllerOptions(nil))
	check(t, c.State().Selection != "" && c.View().Detail != nil, "nothing selected")
}

// Remind, archive, promises, the conversation, toasts.

func TestRemindTakesTheCaseOffLikeDone(t *testing.T) {
	c, src, p := defaultController()
	c.Select("c3")
	p.log = nil
	until := day(16, 9, 0)
	c.Remind("c3", &until)
	check(t, caseIn(src.Snapshot(), "c3").Visibility.Equal(Visibility{Kind: VisibleSnoozed, At: until}), "not snoozed")
	eq(t, "rows", ctrlRows(c), []string{"c1", "c2", "c4", "c5"})
	eq(t, "selection", c.State().Selection, CaseID("c2"))
	eq(t, "log", p.log, []Changes{ChangeSelection | ChangeContent})
	// The same remind again changes nothing.
	again := until
	c.Remind("c3", &again)
	eq(t, "silent", len(p.log), 1)
	// Under Done, ending the remind puts it back and moves on.
	c.SetFilter(doneFilter)
	c.Select("c3")
	c.Remind("c3", nil)
	check(t, caseIn(src.Snapshot(), "c3").Visibility.IsLive(), "not live")
	eq(t, "moved on", c.State().Selection, CaseID("d1"))
}

func TestRemindAPresetFromTheController(t *testing.T) {
	c, _, _ := defaultController()
	eq(t, "presets", c.RemindPresets(), RemindPresets(testNow, testEnv))
}

func TestArchiveMarksDoneAndToasts(t *testing.T) {
	cases := sampleCases()
	cases[2].CanArchive = true // c3
	c, src, _ := makeController(cases, false, nil)
	var toasts []string
	c.OnToast = func(s string) { toasts = append(toasts, s) }
	c.Select("c3")
	c.Archive("c3")
	check(t, caseIn(src.Snapshot(), "c3").Done(), "not done")
	eq(t, "selection", c.State().Selection, CaseID("c2"))
	eq(t, "toasts", toasts, []string{"Archived 1 message."})
	c.Archive("c2")
	eq(t, "no archive", toasts[len(toasts)-1], "Marked as done. This account has no archive.")
}

func TestControllerSetCommitmentDone(t *testing.T) {
	k := Commitment{ID: "k1", CaseID: "c1", Text: "Promise"}
	src := NewInMemorySource(Snapshot{Accounts: testAccounts, Cases: sampleCases(), Commitments: []Commitment{k}, Annotated: true, Phase: PhaseReady}, tr)
	c := NewController(src, controllerOptions(nil))
	eq(t, "open", commitmentIDs(c.View().Commitments), []string{"k1"})
	c.SetCommitmentDone("k1", true)
	check(t, src.Snapshot().Commitments[0].State == CommitmentDone && len(c.View().Commitments) == 0, "not done")
	c.SetCommitmentDone("k1", false)
	eq(t, "reopened", commitmentIDs(c.View().Commitments), []string{"k1"})
}

// TestSelectionLoadsTheConversation: selecting a case asks for its
// conversation once per case and version.
func TestSelectionLoadsTheConversation(t *testing.T) {
	cases := sampleCases()
	for i := range cases {
		cases[i].Messages, cases[i].MessagesLoaded = nil, false
	}
	src := &lateSource{snapshot: testSnapshot(cases...)}
	c := NewController(src, controllerOptions(nil))
	eq(t, "first row at once", src.loads, []CaseID{"c1"})
	check(t, c.View().Detail.MessagesLoading, "not loading")
	c.Select("c3")
	c.Refresh()
	eq(t, "c3", src.loads, []CaseID{"c1", "c3"})
	// A new version of the selected case asks again.
	src.setCase("c3", func(k *Case) { k.Version = 2 })
	src.flush()
	eq(t, "version", src.loads, []CaseID{"c1", "c3", "c3"})
	// Back to c1: asked again (the source knows whether it has it).
	c.Select("c1")
	check(t, len(src.loads) == 4 && src.loads[3] == "c1", "loads %v", src.loads)
	// Nothing selected asks for nothing.
	c.SetStyle(StyleColumns)
	eq(t, "nothing", len(src.loads), 4)
}

func TestSourceToastsReachThePage(t *testing.T) {
	c, src, _ := defaultController()
	var toasts []string
	c.OnToast = func(s string) { toasts = append(toasts, s) }
	src.Handlers().Error("bad")
	src.Handlers().Notice("good")
	eq(t, "toasts", toasts, []string{"bad", "good"})
}

func TestPhaseAndTriageInTheView(t *testing.T) {
	src := NewInMemorySource(Snapshot{Accounts: testAccounts, Phase: PhasePreparing, Triage: TriageInfo{Queue: 3, AnnotatedToday: 1}}, tr)
	c := NewController(src, controllerOptions(nil))
	check(t, c.Phase() == PhasePreparing && c.View().Phase == PhasePreparing, "phase")
	eq(t, "title", c.View().EmptyTitle, EmptyTitleOf(PhasePreparing, tr))
	eq(t, "queue", c.View().Triage.Queue, 3)
	s := src.Snapshot()
	s.Phase = PhaseReady
	src.Replace(s)
	check(t, c.View().Phase == PhaseReady && c.View().EmptyTitle == EmptyTitle(tr), "ready")
}

// TestAFailedConversationIsAskedForAgain: a conversation that could not be
// loaded is asked for again when the case is selected again, when the
// board comes back from a failure, and from the detail's Try Again; not on
// every report.
func TestAFailedConversationIsAskedForAgain(t *testing.T) {
	cases := sampleCases()
	for i := range cases {
		cases[i].Messages, cases[i].MessagesLoaded = nil, false
	}
	src := &lateSource{snapshot: testSnapshot(cases...)}
	c := NewController(src, controllerOptions(nil))
	eq(t, "loads", src.loads, []CaseID{"c1"})
	// The load fails.
	src.setCase("c1", func(k *Case) { k.MessagesFailed = true })
	src.flush()
	check(t, c.View().Detail.MessagesRetry && c.View().Detail.MessagesNote == MessagesFailed(tr), "no retry")
	eq(t, "not by itself", len(src.loads), 1)
	// Selected again: asked again.
	c.Select("c1")
	eq(t, "selected again", src.loads, []CaseID{"c1", "c1"})
	src.flush()
	eq(t, "a report", len(src.loads), 2)
	// Try Again.
	c.RetryMessages()
	eq(t, "try again", len(src.loads), 3)
	// The connection goes and comes back with the same version: asked
	// again once the board is back.
	src.snapshot.Phase = PhaseUnavailable
	src.flush()
	eq(t, "gone", len(src.loads), 3)
	src.snapshot.Phase = PhaseReady
	src.flush()
	eq(t, "back", len(src.loads), 4)
	src.flush()
	eq(t, "once", len(src.loads), 4)
	// Loaded: Try Again and selecting it again ask for nothing more.
	src.setCase("c1", func(k *Case) { k.MessagesFailed, k.Messages, k.MessagesLoaded = false, nil, true })
	src.flush()
	check(t, !c.View().Detail.MessagesRetry, "retry offered")
	c.RetryMessages()
	c.Select("c1")
	eq(t, "loaded", len(src.loads), 4)
}

// TestBoardShownRetriesAFailedBoard: entering the board while it could not
// be listed asks for it again.
func TestBoardShownRetriesAFailedBoard(t *testing.T) {
	src := &lateSource{snapshot: testSnapshot(sampleCases()...)}
	c := NewController(src, controllerOptions(nil))
	c.BoardShown()
	eq(t, "ready", src.refreshes, 0)
	for _, phase := range []Phase{PhaseUnavailable, PhaseFailed, PhaseUnsupported} {
		src.snapshot.Phase = phase
		src.flush()
		before := src.refreshes
		c.BoardShown()
		eq(t, "phase", src.refreshes, before+1)
	}
}

// The default style (Settings → General → Board).

// TestDefaultStyleAtFirstShow: the style of the first show is the
// setting's, read at that moment.
func TestDefaultStyleAtFirstShow(t *testing.T) {
	setting := "columns"
	src := NewInMemorySource(testSnapshot(sampleCases()...), tr)
	o := controllerOptions(nil)
	o.DefaultStyle = func() string { return setting }
	c := NewController(src, o)
	p := &probe{}
	c.OnChange = p.changes
	// Until the board shows it holds the List.
	check(t, c.State().Style == StyleList && !c.HasShown(), "before")
	// Changed before the first show: that one counts.
	setting = "today"
	c.BoardWillShow()
	check(t, c.HasShown() && c.State().Style == StyleToday, "first show %d", c.State().Style)
	check(t, slices.ContainsFunc(p.log, func(x Changes) bool { return x.Has(ChangeStyle) }), "no style change: %v", p.log)
	// The default List changes nothing.
	list, _, lp := defaultController()
	list.BoardWillShow()
	check(t, list.State().Style == StyleList && len(lp.log) == 0, "list %v", lp.log)
	// An unknown setting is the List.
	setting = "grid"
	odd := NewController(NewInMemorySource(testSnapshot(sampleCases()...), tr), o)
	odd.BoardWillShow()
	eq(t, "unknown", odd.State().Style, StyleList)
}

// TestLaterShowsKeepTheUsersStyle: after the first show the style is the
// user's: leaving and entering again keeps it, and so does a setting
// changed meanwhile.
func TestLaterShowsKeepTheUsersStyle(t *testing.T) {
	setting := "columns"
	o := controllerOptions(nil)
	o.DefaultStyle = func() string { return setting }
	c := NewController(NewInMemorySource(testSnapshot(sampleCases()...), tr), o)
	c.BoardWillShow()
	c.BoardShown()
	eq(t, "first", c.State().Style, StyleColumns)
	c.SetStyle(StyleToday)
	// Back from Mail.
	c.BoardWillShow()
	c.BoardShown()
	eq(t, "back", c.State().Style, StyleToday)
	// The setting changes while the board has shown: the style stays.
	setting = "list"
	c.BoardWillShow()
	eq(t, "setting list", c.State().Style, StyleToday)
	setting = "columns"
	c.BoardWillShow()
	eq(t, "setting columns", c.State().Style, StyleToday)
	// The user's own choice still works.
	c.SetStyle(StyleList)
	c.BoardWillShow()
	eq(t, "the user's", c.State().Style, StyleList)
}
