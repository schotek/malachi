// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"cmp"
	"math"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/jira"
)

// The board's model: states, the view model, the selection rules, the
// cleaning of hostile strings and the invented samples (macOS
// BoardModelTests.swift, the same cases). Pure and deterministic: a fixed
// now, UTC and English dates.

func optState(s State) *State { return &s }

func TestStateOf(t *testing.T) {
	cases := []struct {
		user, annotation *State
		annotated        bool
		want             State
	}{
		{nil, nil, false, StateInfo},
		{nil, nil, true, StateInfo},
		{nil, optState(StateHot), true, StateHot},
		{nil, optState(StateInfo), true, StateInfo}, // the annotation agrees with the rules
		{nil, optState(StateHot), false, StateInfo}, // annotations switched off
		{optState(StateThem), nil, true, StateThem},
		{optState(StateThem), optState(StateHot), true, StateThem}, // the user wins over the assistant
		{optState(StateThem), optState(StateHot), false, StateThem},
		{optState(StateInfo), optState(StateInfo), true, StateInfo},
	}
	for i, c := range cases {
		k := mk("x", StateInfo)
		k.UserState = c.user
		if c.annotation != nil {
			k.Annotation = &Annotation{State: c.annotation, Title: "t"}
		}
		if got := StateOf(k, c.annotated); got != c.want {
			t.Errorf("case %d: StateOf = %d, want %d", i, got, c.want)
		}
	}
}

func TestStateSourceOf(t *testing.T) {
	cases := []struct {
		user, annotation *State
		annotated        bool
		want             Source
	}{
		{nil, nil, false, Source{Kind: SourceAssistantOff}},
		{nil, optState(StateHot), false, Source{Kind: SourceAssistantOff}},
		{nil, nil, true, Source{Kind: SourceRules}},
		{nil, optState(StateYou), true, Source{Kind: SourceAssistantKept}},
		{nil, optState(StateHot), true, Source{Kind: SourceAssistantChanged, From: StateYou}},
		{optState(StateThem), nil, true, Source{Kind: SourceUser}},
		{optState(StateThem), optState(StateHot), true, Source{Kind: SourceUser}},
		{optState(StateThem), optState(StateHot), false, Source{Kind: SourceUser}},
		{optState(StateThem), nil, false, Source{Kind: SourceUser}},
	}
	for i, c := range cases {
		k := mk("x", StateYou)
		k.UserState = c.user
		if c.annotation != nil {
			k.Annotation = &Annotation{State: c.annotation, Title: "t"}
		}
		if got := StateSourceOf(k, c.annotated); got != c.want {
			t.Errorf("case %d: StateSourceOf = %+v, want %+v", i, got, c.want)
		}
	}
}

// Ordering and sections.

func TestOrderIsStateThenNewestThenID(t *testing.T) {
	v := view(casesOf(
		mk("c1", StateYou, withHours(5)), mk("c2", StateHot, withHours(9)), mk("c3", StateYou, withHours(1)),
		mk("c4", StateInfo, withHours(3)), mk("c6", StateThem, withHours(2)), mk("c5", StateThem, withHours(2)),
	))
	eq(t, "kinds", sectionKinds(v), []string{"hot", "you", "them", "info"})
	var rows [][]string
	var titles []string
	for _, s := range v.Sections {
		rows = append(rows, idsOf(s.Rows))
		titles = append(titles, s.Title)
	}
	eq(t, "rows", rows, [][]string{{"c2"}, {"c3", "c1"}, {"c5", "c6"}, {"c4"}})
	eq(t, "titles", titles, []string{"Hot", "Waiting for You", "Waiting for Them", "For Your Information"})
}

func casesOf(cs ...Case) []Case { return cs }

func TestSectionsPerFilter(t *testing.T) {
	cases := casesOf(
		mk("c1", StateYou), mk("c2", StateHot), mk("c3", StateInfo, withHours(2), withDone()),
		mk("c4", StateThem, withHours(4), withDone()), mk("c5", StateThem, withHours(3)),
	)
	eq(t, "all", sectionKinds(view(cases)), []string{"hot", "you", "them"}) // info is done: no empty section
	you := view(cases, configured(func(v *ViewState) { v.Filter = filterState(StateYou) }))
	eq(t, "you", sectionKinds(you), []string{"you"})
	eq(t, "you rows", idsOf(you.Sections[0].Rows), []string{"c1"})
	none := view(cases, configured(func(v *ViewState) { v.Filter = filterState(StateInfo) }))
	check(t, len(none.Sections) == 0 && none.SectionsEmptyText == "Nothing here.", "info: %v", none.Sections)
	done := view(cases, configured(func(v *ViewState) { v.Filter = doneFilter }))
	eq(t, "done", sectionKinds(done), []string{"done"})
	eq(t, "done title", done.Sections[0].Title, "Done")
	eq(t, "done rows", idsOf(done.Sections[0].Rows), []string{"c3", "c4"}) // newest first
	emptyDone := view(casesOf(mk("c1", StateYou)), configured(func(v *ViewState) { v.Filter = doneFilter }))
	check(t, len(emptyDone.Sections) == 0, "an empty done list has sections")
}

func TestAnnotationDecidesTheSectionOnlyWhenAnnotated(t *testing.T) {
	cases := casesOf(mk("c1", StateInfo, withAnnotation(Annotation{State: optState(StateHot), Title: "Burning"})))
	eq(t, "annotated", sectionKinds(view(cases, annotatedOn())), []string{"hot"})
	eq(t, "not", sectionKinds(view(cases)), []string{"info"})
}

func TestColumnsAreAlwaysFourWhateverTheFilter(t *testing.T) {
	cases := casesOf(mk("c1", StateYou), mk("c2", StateHot, withDone()))
	for _, f := range []Filter{{}, filterState(StateHot), doneFilter} {
		v := view(cases, configured(func(v *ViewState) { v.Filter = f; v.Style = StyleColumns }))
		var states []State
		var rows [][]string
		var empty, titles []string
		for _, c := range v.Columns {
			states = append(states, c.State)
			rows = append(rows, idsOf(c.Rows))
			empty = append(empty, c.EmptyText)
			titles = append(titles, c.Title)
		}
		eq(t, "states", states, States)
		eq(t, "rows", rows, [][]string{{}, {"c1"}, {}, {}})
		eq(t, "empty", empty, []string{"Nothing burning.", "Empty.", "Empty.", "Empty."})
		eq(t, "titles", titles, []string{StateName(StateHot, tr), StateName(StateYou, tr), StateName(StateThem, tr), StateName(StateInfo, tr)})
	}
}

// Scope, counts.

func navCounts(v ViewModel) []int {
	var out []int
	for _, n := range v.Nav {
		out = append(out, n.Count)
	}
	return out
}

func accountCounts(v ViewModel) []int {
	var out []int
	for _, a := range v.Accounts {
		out = append(out, a.Count)
	}
	return out
}

func TestAccountScopeAndCounts(t *testing.T) {
	cases := casesOf(
		mk("c1", StateHot), mk("c2", StateYou, withAccount(accountB)), mk("c3", StateYou, withAccount(accountB), withHours(2)),
		mk("c4", StateInfo, withAccount(accountB), withDone()), mk("c5", StateThem, withDone()),
	)
	all := view(cases)
	eq(t, "nav counts", navCounts(all), []int{3, 1, 2, 0, 0, 0, 2})
	var titles, accountTitles, badges []string
	var dots []string
	var selected, accountSelected []bool
	for _, n := range all.Nav {
		titles = append(titles, n.Title)
		selected = append(selected, n.Selected)
		if n.HasDot {
			dots = append(dots, string(n.Dot.API()))
		} else {
			dots = append(dots, "")
		}
	}
	for _, a := range all.Accounts {
		accountTitles = append(accountTitles, a.Title)
		badges = append(badges, a.Badge)
		accountSelected = append(accountSelected, a.Selected)
	}
	eq(t, "nav titles", titles, []string{"Overview", "Hot", "Waiting for You", "Waiting for Them", "For Your Information", "Snoozed", "Done"})
	eq(t, "dots", dots, []string{"", "hot", "you", "them", "info", "", ""})
	eq(t, "nav selected", selected, []bool{true, false, false, false, false, false, false})
	eq(t, "account titles", accountTitles, []string{"All Accounts", "Alpha", "Beta"})
	eq(t, "badges", badges, []string{"", "IMAP", "JIRA"})
	eq(t, "account counts", accountCounts(all), []int{3, 1, 2})
	eq(t, "account selected", accountSelected, []bool{true, false, false})
	eq(t, "subtitle", all.Subtitle, "All Accounts · 3 cases")

	// The account filter narrows the cases; the account list keeps its own counts.
	b := view(cases, configured(func(v *ViewState) { v.Account = accountB; v.Filter = doneFilter }))
	eq(t, "b nav counts", navCounts(b), []int{2, 0, 2, 0, 0, 0, 1})
	selected = nil
	for _, n := range b.Nav {
		selected = append(selected, n.Selected)
	}
	eq(t, "b nav selected", selected, []bool{false, false, false, false, false, false, true})
	eq(t, "b account counts", accountCounts(b), []int{3, 1, 2})
	accountSelected = nil
	for _, a := range b.Accounts {
		accountSelected = append(accountSelected, a.Selected)
	}
	eq(t, "b account selected", accountSelected, []bool{false, false, true})
	eq(t, "b title", b.AccountTitle, "Beta")
	eq(t, "b subtitle", b.Subtitle, "Beta · 2 cases")
	eq(t, "b column you", idsOf(b.Columns[1].Rows), []string{"c2", "c3"})
	eq(t, "b done", idsOf(b.Sections[0].Rows), []string{"c4"})
	var tiles []int
	for _, tl := range b.Today.Tiles[:4] {
		tiles = append(tiles, tl.Count)
	}
	eq(t, "b tiles", tiles, []int{0, 2, 0, 0})
}

func TestIsEmpty(t *testing.T) {
	check(t, view(nil).IsEmpty, "no case is not empty")
	check(t, !view(casesOf(mk("c1", StateYou))).IsEmpty, "one case is empty")
	// Done cases still count: the board is not empty, the list is.
	check(t, !view(casesOf(mk("c1", StateYou, withDone()))).IsEmpty, "a done case is empty")
	// In the account scope.
	check(t, view(casesOf(mk("c1", StateYou)), configured(func(v *ViewState) { v.Account = accountB })).IsEmpty, "account B")
	check(t, !view(casesOf(mk("c1", StateYou, withDone())), configured(func(v *ViewState) { v.Account = accountA })).IsEmpty, "account A")
	// Whatever the filter.
	check(t, !view(casesOf(mk("c1", StateYou)), configured(func(v *ViewState) { v.Filter = doneFilter })).IsEmpty, "done filter")
}

func TestPluralTexts(t *testing.T) {
	eq(t, "none", view(nil).Subtitle, "All Accounts · 0 cases")
	eq(t, "one", view(casesOf(mk("c1", StateYou))).Subtitle, "All Accounts · 1 case")
	eq(t, "two", view(casesOf(mk("c1", StateYou), mk("c2", StateYou))).Subtitle, "All Accounts · 2 cases")
	d := view(casesOf(mk("c1", StateYou, withCount(1)), mk("c2", StateYou, withHours(2), withCount(7))),
		configured(func(v *ViewState) { v.Selection = "c2" })).Detail
	eq(t, "seven", d.ConversationTitle, "Conversation · 7 messages")
	eq(t, "one message", view(casesOf(mk("c1", StateYou, withCount(1)))).Detail.ConversationTitle, "Conversation · 1 message")
	// A count below one still means one message.
	eq(t, "zero", view(casesOf(mk("c1", StateYou, withCount(0)))).Detail.ConversationTitle, "Conversation · 1 message")
}

// Rows.

func TestRowFields(t *testing.T) {
	c := Case{
		ID: "c1", Account: accountB, Person: "Ada", Date: ago(2), Subject: "DEMO-1: Subject", Snippet: "snip",
		Unread: true, HasAttachments: true, MessageCount: 3,
		Issue: &IssueInfo{Key: "DEMO-1", Status: "To Do", Style: jira.StatusTodo}, RuleState: StateYou,
	}
	r := view(casesOf(c)).Sections[0].Rows[0]
	check(t, r.ID == "c1" && r.State == StateYou && r.Person == "Ada", "row %+v", r)
	check(t, r.Title == "DEMO-1: Subject" && r.Snippet == "snip" && r.Account == "Beta", "row %+v", r)
	check(t, r.IssueKey == "DEMO-1" && r.IssueStatus == "To Do" && r.IssueStyle == jira.StatusTodo, "row %+v", r)
	check(t, r.Attachments && r.Unread && r.CountText == "3" && r.Due == "", "row %+v", r)
	check(t, r.Time != "", "no time")
	eq(t, "spoken", r.Spoken, "Waiting for You. Ada. DEMO-1: Subject. DEMO-1, To Do. 3 messages. Has attachments. Unread.")
	plain := view(casesOf(mk("c2", StateYou))).Sections[0].Rows[0]
	check(t, plain.IssueKey == "" && plain.IssueStyle == jira.StatusPlain && plain.CountText == "" && !plain.Unread && !plain.Attachments,
		"plain %+v", plain)
	check(t, !strings.Contains(plain.Spoken, "message"), "spoken %q", plain.Spoken)
}

// Annotated and not.

func annotatedCase() Case {
	due := day(16, 10, 0)
	return mk("c1", StateYou, withSubject("Raw subject"), withSnippet("raw snippet"),
		withAnnotation(Annotation{
			State: optState(StateHot), Title: "Assistant title", Summary: "Assistant summary", Why: "Assistant why",
			Due: &due, DueQuote: "by tomorrow", Tasks: []string{"t1", "  ", "t2"},
		}), withDraft("Hi,\n\nreply"))
}

func TestWithAnnotations(t *testing.T) {
	v := view(casesOf(annotatedCase()), annotatedOn())
	r := v.Sections[0].Rows[0]
	check(t, r.Title == "Assistant title" && r.Snippet == "Assistant summary" && r.Due == "Tomorrow" && !r.DueOverdue, "row %+v", r)
	d := v.Detail
	check(t, d.State == StateHot && d.Source == Source{Kind: SourceAssistantChanged, From: StateYou}, "state %+v", d)
	check(t, d.Title == "Assistant title" && d.Subject == "Raw subject", "title %q subject %q", d.Title, d.Subject)
	check(t, d.Summary == "Assistant summary" && d.Why == "Assistant why", "summary %q why %q", d.Summary, d.Why)
	check(t, d.Due == "Tomorrow" && d.DueQuote == "by tomorrow", "due %q %q", d.Due, d.DueQuote)
	eq(t, "tasks", d.Tasks, []string{"t1", "t2"})
	eq(t, "draft", d.Draft, "Hi,\n\nreply")
	eq(t, "state title", d.StateTitle, "Hot")
	check(t, strings.Contains(d.SourceText, "The rules suggested: Waiting for You."), "source %q", d.SourceText)
}

func TestWithoutAnnotations(t *testing.T) {
	v := view(casesOf(annotatedCase()))
	r := v.Sections[0].Rows[0]
	check(t, r.State == StateYou && r.Title == "Raw subject" && r.Snippet == "raw snippet" && r.Due == "", "row %+v", r)
	d := v.Detail
	check(t, d.State == StateYou && d.Source == Source{Kind: SourceAssistantOff}, "state %+v", d)
	check(t, d.Title == "Raw subject" && d.Subject == "" && d.Summary == "", "detail %+v", d)
	check(t, d.Due == "" && d.DueQuote == "" && len(d.Tasks) == 0, "detail %+v", d)
	// The draft is a real draft of the account: it stays without the assistant.
	check(t, d.Draft == "Hi,\n\nreply" && d.DraftID == "d_c1", "draft %q %q", d.Draft, d.DraftID)
	eq(t, "why", d.Why, ReasonUnknown(tr)) // "rule c1" is no code this client knows
	check(t, len(v.Today.DueGroups) == 0 && len(v.Today.Tiles) == 4, "today %+v", v.Today)
	eq(t, "status", v.StatusLine, "Sorted by the daemon’s rules · assistant off")
}

func TestSubjectShownOnlyWhenTheTitleDiffers(t *testing.T) {
	detail := func(title, subject string) *Detail {
		return view(casesOf(mk("c1", StateYou, withSubject(subject),
			withAnnotation(Annotation{State: optState(StateYou), Title: title}))), annotatedOn()).Detail
	}
	eq(t, "same", detail("Same", "Same").Subject, "")
	eq(t, "cleaned first", detail("Same", "  Same \n").Subject, "")
	eq(t, "other", detail("Other", "Same").Subject, "Same")
	// An empty title falls back to the subject, and then the two agree.
	eq(t, "empty title", detail("", "Same").Title, "Same")
	eq(t, "empty title's subject", detail("", "Same").Subject, "")
	eq(t, "invisible title", detail("\u202E", "Same").Title, "Same")
}

func TestEmptySubjectAndSnippetFallbacks(t *testing.T) {
	v := view(casesOf(mk("c1", StateYou, withSubject(" \n "), withSnippet("snip"))))
	eq(t, "title", v.Sections[0].Rows[0].Title, "(No subject)")
	// An annotation with an empty summary leaves the case snippet in the row.
	a := Annotation{State: optState(StateYou), Title: "T", Summary: " "}
	got := view(casesOf(mk("c1", StateYou, withSnippet("snip"), withAnnotation(a))), annotatedOn())
	eq(t, "snippet", got.Sections[0].Rows[0].Snippet, "snip")
	eq(t, "summary", got.Detail.Summary, "")
}

func TestWhyFallsBackToTheRulesReason(t *testing.T) {
	a := Annotation{State: optState(StateYou), Title: "T"}
	eq(t, "unknown", view(casesOf(mk("c1", StateYou, withAnnotation(a))), annotatedOn()).Detail.Why, ReasonUnknown(tr))
	known := mk("c1", StateYou, withAnnotation(a), withReason(api.BoardReasonYouAddressed))
	eq(t, "known", view(casesOf(known), annotatedOn()).Detail.Why, Reason("you.addressed", tr))
	b := Annotation{State: optState(StateYou), Title: "T", Why: "Mine"}
	eq(t, "the assistant's", view(casesOf(mk("c1", StateYou, withAnnotation(b))), annotatedOn()).Detail.Why, "Mine")
}

func TestDueQuoteOnlyWithADueDate(t *testing.T) {
	a := Annotation{State: optState(StateYou), Title: "T", DueQuote: "quote"}
	eq(t, "quote", view(casesOf(mk("c1", StateYou, withAnnotation(a))), annotatedOn()).Detail.DueQuote, "")
}

func TestDetailMessagesAndIssue(t *testing.T) {
	c := mk("c1", StateYou, withCount(3), withMessages(
		CaseMessage{From: "Bob", Date: ago(2), Text: "second"},
		CaseMessage{From: "Ann", Date: ago(5), Text: "first"},
		CaseMessage{From: "Me", Date: ago(1), Text: "third", Mine: true},
	), withIssue(IssueInfo{Key: "DEMO-2", Status: "Done", Style: jira.StatusDone}))
	d := view(casesOf(c)).Detail
	var texts, froms []string
	var mine []bool
	for _, m := range d.Messages {
		texts = append(texts, m.Text)
		froms = append(froms, m.From)
		mine = append(mine, m.Mine)
	}
	eq(t, "texts", texts, []string{"first", "second", "third"}) // oldest first
	eq(t, "froms", froms, []string{"Ann", "Bob", "You"})        // the user's own are "You"
	eq(t, "mine", mine, []bool{false, false, true})
	eq(t, "issue", *d.Issue, IssueInfo{Key: "DEMO-2", Status: "Done", Style: jira.StatusDone})
	check(t, d.Account == "Alpha" && d.Person == "P c1" && d.Time != "" && !d.IsDone, "detail %+v", d)
	check(t, view(casesOf(mk("c1", StateYou, withDone())), configured(func(v *ViewState) { v.Filter = doneFilter })).Detail.IsDone,
		"a done case is not done")
}

// Commitments.

func commitmentIDs(ks []CommitmentRow) []string {
	out := []string{}
	for _, k := range ks {
		out = append(out, string(k.ID))
	}
	return out
}

func TestCommitments(t *testing.T) {
	cases := casesOf(
		mk("c1", StateHot, withAnnotation(Annotation{State: optState(StateHot), Title: "Titled"})),
		mk("c2", StateYou, withAccount(accountB)), mk("c3", StateInfo, withDone()),
	)
	d20 := day(20, 9, 0)
	nextYear := time.Date(2027, 1, 3, 9, 0, 0, 0, time.UTC)
	ks := []Commitment{
		{ID: "k1", CaseID: "c1", Text: "Do it", Quote: "I will", Due: &d20},
		{ID: "k2", CaseID: "c2", Text: "Other account"},
		{ID: "k3", CaseID: "c3", Text: "On a done case"},
		{ID: "k4", CaseID: "missing", Text: "Unknown case"},
		{ID: "k5", CaseID: "c1", Text: "Next year", Due: &nextYear},
	}
	on := view(cases, annotatedOn(), withCommitments(ks...))
	eq(t, "ids", commitmentIDs(on.Commitments), []string{"k1", "k2", "k5"})
	k := on.Commitments[0]
	check(t, k.From == "Titled" && k.CaseID == "c1" && k.Text == "Do it" && k.Quote == "I will", "k1 %+v", k)
	eq(t, "k1 due", k.Due, "20 Oct")
	check(t, on.Commitments[1].Due == "" && on.Commitments[1].From == "Subject c2", "k2 %+v", on.Commitments[1])
	eq(t, "k5 due", on.Commitments[2].Due, "2027-01-03")
	check(t, on.ShowsCommitmentsInList, "not in the list")
	eq(t, "today's", on.Today.Commitments, on.Commitments)
	eq(t, "tile", on.Today.Tiles[len(on.Today.Tiles)-1], Tile{Kind: TileCommitments, Count: 3, Title: "Promised", ToolTip: "Promised: 3 promises"})

	// In the account scope.
	b := view(cases, annotatedOn(), withCommitments(ks...), configured(func(v *ViewState) { v.Account = accountB }))
	eq(t, "b", commitmentIDs(b.Commitments), []string{"k2"})

	// The list shows them under Overview only.
	you := view(cases, annotatedOn(), withCommitments(ks...), configured(func(v *ViewState) { v.Filter = filterState(StateYou) }))
	check(t, !you.ShowsCommitmentsInList && len(you.Commitments) > 0, "you %+v", you.Commitments)

	// Not annotated: none, and no tile.
	off := view(cases, withCommitments(ks...))
	check(t, len(off.Commitments) == 0 && len(off.Today.Commitments) == 0 && !off.ShowsCommitmentsInList, "off")
	var kinds []TileKind
	for _, tl := range off.Today.Tiles {
		kinds = append(kinds, tl.Kind)
	}
	eq(t, "off tiles", kinds, []TileKind{TileState, TileState, TileState, TileState})

	// No commitment, nothing to show.
	none := view(cases, annotatedOn())
	check(t, len(none.Commitments) == 0 && !none.ShowsCommitmentsInList, "none")
	eq(t, "none tile", none.Today.Tiles[len(none.Today.Tiles)-1].Count, 0)
}

// Due groups.

func dueAnnotation(due *time.Time, quote string) Annotation {
	return Annotation{State: optState(StateYou), Title: "T", Due: due, DueQuote: quote}
}

func at(t time.Time) *time.Time { return &t }

func TestDueGroups(t *testing.T) {
	cases := casesOf(
		mk("c1", StateYou, withAnnotation(dueAnnotation(at(day(13, 9, 0)), "q1"))),
		mk("c2", StateYou, withAnnotation(dueAnnotation(at(day(15, 23, 59)), ""))),
		mk("c3", StateYou, withAnnotation(dueAnnotation(at(day(16, 0, 30)), ""))),
		mk("c4", StateYou, withAnnotation(dueAnnotation(at(day(22, 23, 0)), ""))), // +7 days
		mk("c5", StateYou, withAnnotation(dueAnnotation(at(day(23, 0, 0)), ""))),  // +8 days
		mk("c6", StateYou, withAnnotation(dueAnnotation(at(day(20, 9, 0)), ""))),
		mk("c7", StateYou, withAnnotation(dueAnnotation(at(day(18, 9, 0)), ""))),
		mk("c8", StateYou, withAnnotation(dueAnnotation(at(day(15, 9, 0)), "")), withDone()),            // done: hidden
		mk("c9", StateYou, withAccount(accountB), withAnnotation(dueAnnotation(at(day(15, 8, 0)), ""))), // other account
		mk("c10", StateYou, withAnnotation(dueAnnotation(nil, ""))),
	)
	v := view(cases, annotatedOn(), configured(func(v *ViewState) { v.Account = accountA }))
	// Only a previous calendar day is overdue, not an earlier time today.
	for _, section := range v.Sections {
		for _, row := range section.Rows {
			if row.DueOverdue != (row.ID == "c1") {
				t.Errorf("case %s: overdue = %t", row.ID, row.DueOverdue)
			}
		}
	}
	groups := v.Today.DueGroups
	var kinds []DueGroup
	var titles []string
	var items [][]string
	for _, g := range groups {
		kinds = append(kinds, g.Kind)
		titles = append(titles, g.Title)
		var ids []string
		for _, it := range g.Items {
			ids = append(ids, string(it.CaseID))
		}
		items = append(items, ids)
	}
	eq(t, "kinds", kinds, DueGroups)
	eq(t, "titles", titles, []string{"Overdue", "Today", "Tomorrow", "Next 7 Days", "Later"})
	eq(t, "items", items, [][]string{{"c1"}, {"c2"}, {"c3"}, {"c7", "c6", "c4"}, {"c5"}})
	first := groups[0].Items[0]
	check(t, first.Quote == "q1" && first.Label == "13 Oct", "overdue %+v", first)
	check(t, groups[1].Items[0].Label == "Today" && groups[2].Items[0].Label == "Tomorrow", "labels")
	check(t, first.Person == "P c1" && first.Title == "T", "overdue %+v", first)

	// Without the account filter the other account's case joins its group, sorted by date.
	all := view(cases, annotatedOn()).Today.DueGroups
	var today []string
	for _, it := range all[1].Items {
		today = append(today, string(it.CaseID))
	}
	eq(t, "today", today, []string{"c9", "c2"})

	// Empty groups are left out.
	one := view(casesOf(mk("c1", StateYou, withAnnotation(dueAnnotation(at(day(15, 10, 0)), "")))), annotatedOn()).Today.DueGroups
	check(t, len(one) == 1 && one[0].Kind == DueToday, "one %+v", one)
	check(t, len(view(casesOf(mk("c1", StateYou)), annotatedOn()).Today.DueGroups) == 0, "no deadline has groups")

	// Switched off: none, even with the annotation there.
	check(t, len(view(cases).Today.DueGroups) == 0, "off has groups")
	eq(t, "empty", view(cases, annotatedOn()).Today.DueEmpty, DueEmpty(tr))
}

func TestDueGroupBoundaries(t *testing.T) {
	d := func(dd, h, m, s int) time.Time { return time.Date(2026, 10, dd, h, m, s, 0, time.UTC) }
	cases := []struct {
		now, due time.Time
		want     DueGroup
	}{
		{d(15, 0, 0, 10), d(14, 23, 59, 59), DueOverdue}, // just before midnight
		{d(15, 0, 0, 10), d(15, 0, 0, 0), DueToday},      // earlier today still counts as today
		{d(15, 23, 59, 30), d(15, 23, 59, 59), DueToday},
		{d(15, 23, 59, 30), d(16, 0, 0, 10), DueTomorrow}, // seconds away, yet tomorrow
		{d(15, 12, 0, 0), d(16, 23, 59, 59), DueTomorrow},
		{d(15, 12, 0, 0), d(17, 0, 0, 0), DueThisWeek},
		{d(15, 12, 0, 0), d(22, 23, 59, 59), DueThisWeek}, // +7 days
		{d(15, 12, 0, 0), d(23, 0, 0, 0), DueLater},       // +8 days
		{d(15, 12, 0, 0), d(14, 12, 0, 0), DueOverdue},
	}
	for i, c := range cases {
		if got := DueGroupOf(c.due, c.now, time.UTC); got != c.want {
			t.Errorf("case %d: %d, want %d", i, got, c.want)
		}
	}
	// The month end: 31 Oct to 1 Nov is one calendar day.
	nov1 := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	eq(t, "month end", DueGroupOf(nov1, d(31, 23, 0, 0), time.UTC), DueTomorrow)
	// A time zone moves midnight: 23:30 UTC is already tomorrow in Prague.
	prague, err := time.LoadLocation("Europe/Prague")
	if err != nil {
		t.Fatal(err)
	}
	late, due := d(15, 23, 30, 0), d(16, 10, 0, 0)
	eq(t, "UTC", DueGroupOf(due, late, time.UTC), DueTomorrow)
	eq(t, "Prague", DueGroupOf(due, late, prague), DueToday)
}

// Panel and Today.

func TestShowsPanel(t *testing.T) {
	cases := casesOf(mk("c1", StateYou), mk("c2", StateHot))
	table := []struct {
		style  Style
		inline bool
		sel    CaseID
		want   bool
	}{
		{StyleList, true, "c1", false},
		{StyleList, true, "", false},
		{StyleList, false, "c1", true},
		{StyleList, false, "", false},
		{StyleColumns, true, "c1", true},
		{StyleColumns, false, "c1", true},
		{StyleColumns, true, "", false},
		{StyleToday, true, "c1", true},
		{StyleToday, true, "", false},
	}
	for _, c := range table {
		v := view(cases, configured(func(v *ViewState) { v.Style = c.style; v.InlineDetail = c.inline; v.Selection = c.sel }))
		if v.ShowsPanel != c.want {
			t.Errorf("%d %v %q: ShowsPanel %v", c.style, c.inline, c.sel, v.ShowsPanel)
		}
	}
}

func TestTodayPage(t *testing.T) {
	cases := casesOf(mk("h1", StateHot, withHours(1)), mk("h2", StateHot, withHours(2)))
	for i := 1; i <= 7; i++ {
		cases = append(cases, mk("y"+string(rune('0'+i)), StateYou, withHours(float64(i))))
	}
	cases = append(cases, mk("t1", StateThem), mk("i1", StateInfo, withDone()))
	today := view(cases).Today
	eq(t, "title", today.Title, "Today")
	var counts []int
	var titles []string
	for _, tl := range today.Tiles {
		counts = append(counts, tl.Count)
		titles = append(titles, tl.Title)
	}
	eq(t, "counts", counts, []int{2, 7, 1, 0})
	eq(t, "titles", titles, []string{"Hot", "Waiting for You", "Waiting for Them", "For Your Information"})
	eq(t, "hot", idsOf(today.Hot), []string{"h1", "h2"})
	eq(t, "you", idsOf(today.You), []string{"y1", "y2", "y3", "y4", "y5"})
	eq(t, "more", today.YouMore, 2)
	eq(t, "phrase", today.Phrase, "9 things need you today.")

	few := view(casesOf(mk("y1", StateYou), mk("y2", StateYou))).Today
	check(t, few.YouMore == 0 && len(few.You) == 2 && few.Phrase == "2 things need you today.", "few %+v", few)
	var five []Case
	for i := 1; i <= 5; i++ {
		five = append(five, mk("y"+string(rune('0'+i)), StateYou, withHours(float64(i))))
	}
	exactly := view(five).Today
	check(t, exactly.YouMore == 0 && len(exactly.You) == 5, "exactly %+v", exactly)
	eq(t, "one", view(casesOf(mk("y1", StateYou))).Today.Phrase, "1 thing needs you today.")
	eq(t, "them", view(casesOf(mk("t1", StateThem))).Today.Phrase, "Nothing needs you today.")
	eq(t, "none", view(nil).Today.Phrase, "Nothing needs you today.")

	// The state decides, so an annotation can move a case into the top list.
	moved := view(casesOf(mk("y1", StateYou, withAnnotation(Annotation{State: optState(StateHot), Title: "t"}))), annotatedOn()).Today
	check(t, len(moved.You) == 0 && moved.Phrase == "1 thing needs you today.", "moved %+v", moved)
	eq(t, "moved hot", idsOf(moved.Hot), []string{"y1"})
}

func TestViewSaysWhetherTheAssistantIsOn(t *testing.T) {
	check(t, view(casesOf(mk("c1", StateYou)), annotatedOn()).AssistantOn, "on")
	check(t, !view(casesOf(mk("c1", StateYou))).AssistantOn, "off")
}

func TestSnoozedRowSpeaksItsRemindTime(t *testing.T) {
	c := mk("c1", StateYou, withVisibility(Visibility{Kind: VisibleSnoozed, At: testNow.Add(20 * time.Hour)}))
	v := view(casesOf(c), configured(func(v *ViewState) { v.Filter = Filter{Kind: FilterSnoozed} }))
	r := v.Sections[0].Rows[0]
	check(t, r.Remind != "", "no remind")
	check(t, strings.Contains(r.Spoken, SpokenRemind(r.Remind, tr)+"."), "spoken %q", r.Spoken)
}

func TestStatusLine(t *testing.T) {
	s := Snapshot{Cases: casesOf(mk("c1", StateYou)), Annotated: true, Run: &Run{Model: "Claude", Date: testNow, Note: "3 sorted"}, Phase: PhaseReady}
	eq(t, "with run", View(s, NewViewState(), testNow, testEnv).StatusLine, "Sorted by rules · refined by the assistant (Claude) · 3 sorted")
	s.Run = nil
	eq(t, "bare", View(s, NewViewState(), testNow, testEnv).StatusLine, "Sorted by rules · refined by the assistant")
}

// Cleaning in the view.

func TestHostileStringsAreCleanedInTheView(t *testing.T) {
	evil := "\u202Eev\x00il\r\n\u2028text\u200B"
	due := day(15, 12, 0)
	ann := Annotation{State: optState(StateYou), Title: evil, Summary: evil, Why: evil, Due: &due, DueQuote: evil, Tasks: []string{evil}}
	c := Case{
		ID: "c1", Account: accountA, Person: evil, Date: ago(1), Subject: evil, Snippet: evil,
		Issue: &IssueInfo{Key: evil, Status: evil}, RuleState: StateYou, RuleReason: api.BoardReason(evil),
		Annotation: &ann, Draft: &DraftLink{ID: "d_1", Text: evil},
		Messages: []CaseMessage{{From: evil, Date: ago(2), Text: evil}}, MessagesLoaded: true,
	}
	k := Commitment{ID: "k", CaseID: "c1", Text: evil, Quote: evil}
	v := view(casesOf(c), annotatedOn(), withCommitments(k))
	r := v.Sections[0].Rows[0]
	d := v.Detail
	strs := []string{r.Person, r.Title, r.Snippet, r.IssueKey, r.IssueStatus, r.Spoken,
		d.Why, d.Person, d.Title, d.DueQuote, d.Summary, d.Draft, d.Issue.Key, d.Issue.Status}
	strs = append(strs, d.Tasks...)
	for _, m := range d.Messages {
		strs = append(strs, m.From, m.Text)
	}
	for _, k := range v.Commitments {
		strs = append(strs, k.Text, k.Quote, k.From)
	}
	for _, g := range v.Today.DueGroups {
		for _, it := range g.Items {
			strs = append(strs, it.Title, it.Person, it.Quote)
		}
	}
	for _, s := range strs {
		check(t, s != "", "an empty string")
		check(t, !strings.ContainsAny(s, "\u202E\x00\u200B\u2028"), "%q is not clean", s)
	}
	// NUL and the override vanish; CR LF and U+2028 are two line breaks in a block, spaces in a line.
	eq(t, "title", r.Title, "evil text")
	eq(t, "summary", d.Summary, "evil\n\ntext")
	eq(t, "subject", d.Subject, "") // cleaned alike, so the title does not differ
}

func TestCapsAreHonouredInTheView(t *testing.T) {
	long := strings.Repeat("é", 5000) // 10 000 bytes
	var tasks []string
	for i := range 30 {
		tasks = append(tasks, "task "+itoa(i))
	}
	tasks = append(tasks, "   ", "\x00")
	var messages []CaseMessage
	for i := range 60 {
		messages = append(messages, CaseMessage{From: "F", Date: ago(float64(100 - i)), Text: "m" + itoa(i)})
	}
	slices.Reverse(messages)
	ann := Annotation{State: optState(StateYou), Title: long, Summary: long, Tasks: tasks}
	c := mk("c1", StateYou, withSubject(long), withSnippet(long), withCount(60), withAnnotation(ann),
		withMessages(messages...), withDraft(long))
	v := view(casesOf(c), annotatedOn())
	r := v.Sections[0].Rows[0]
	check(t, len(r.Title) <= 300 && len(r.Title) > 250, "title %d bytes", len(r.Title))
	check(t, len(r.Snippet) <= 400, "snippet %d bytes", len(r.Snippet))
	d := v.Detail
	check(t, len(d.Title) <= 300 && d.Subject == "", "title %d bytes, subject %q", len(d.Title), d.Subject)
	check(t, len(d.Summary) <= 2000 && len(d.Draft) <= 4000, "summary %d, draft %d", len(d.Summary), len(d.Draft))
	check(t, len(d.Tasks) == 20 && d.Tasks[0] == "task 0" && d.Tasks[19] == "task 19", "tasks %v", d.Tasks)
	check(t, len(d.Messages) == 50 && d.Messages[0].Text == "m10" && d.Messages[49].Text == "m59", "messages %d", len(d.Messages))
	eq(t, "conversation", d.ConversationTitle, "Conversation · 60 messages")

	big := mk("c2", StateYou, withMessages(CaseMessage{From: strings.Repeat("x", 1000), Date: ago(1), Text: strings.Repeat("é", 10000)}))
	m := view(casesOf(big)).Detail.Messages[0]
	// board.get's cap (8000 bytes): a whole ordinary mail, no more.
	check(t, len(m.Text) <= 8000 && len(m.Text) > 7990 && len(m.From) <= 200, "text %d, from %d", len(m.Text), len(m.From))
}

func itoa(i int) string { return strconv.Itoa(i) }

func TestBigAccountAndBadgeAreCapped(t *testing.T) {
	s := Snapshot{
		Accounts: []AccountInfo{{ID: accountA, Name: strings.Repeat("n", 1000), Badge: strings.Repeat("b", 1000)}},
		Cases:    casesOf(mk("c1", StateYou)), Phase: PhaseReady,
	}
	v := View(s, NewViewState(), testNow, testEnv)
	check(t, len(v.Accounts[1].Title) == 120 && len(v.Accounts[1].Badge) == 64, "%d %d", len(v.Accounts[1].Title), len(v.Accounts[1].Badge))
}

func TestCommitmentsShownAreCapped(t *testing.T) {
	var ks []Commitment
	for i := range 250 {
		ks = append(ks, Commitment{ID: api.BoardCommitmentID("k" + itoa(i)), CaseID: "c1", Text: "promise " + itoa(i)})
	}
	v := view(casesOf(mk("c1", StateYou)), annotatedOn(), withCommitments(ks...))
	check(t, len(v.Commitments) == 100 && len(v.Today.Commitments) == 100, "%d", len(v.Commitments))
	check(t, v.Commitments[0].ID == "k0" && v.Commitments[99].ID == "k99", "first %q last %q", v.Commitments[0].ID, v.Commitments[99].ID)
	// The tile counts them all.
	last := v.Today.Tiles[len(v.Today.Tiles)-1]
	check(t, last.Kind == TileCommitments && last.Count == 250, "tile %+v", last)
}

func TestNewestMessagesWithoutSortingThemAll(t *testing.T) {
	// Shuffled dates with ties: the same as a stable sort's last 50.
	rng := rand.New(rand.NewPCG(1, 2))
	for _, n := range []int{0, 1, 49, 50, 51, 120} {
		var ms []CaseMessage
		for i := range n {
			ms = append(ms, CaseMessage{From: "F", Date: ago(float64(rng.IntN(20))), Text: "m" + itoa(i)})
		}
		type indexed struct {
			i int
			m CaseMessage
		}
		var sorted []indexed
		for i, m := range ms {
			sorted = append(sorted, indexed{i, m})
		}
		slices.SortStableFunc(sorted, func(a, b indexed) int {
			if c := a.m.Date.Compare(b.m.Date); c != 0 {
				return c
			}
			return cmp.Compare(a.i, b.i)
		})
		want := []string{}
		for _, x := range sorted[max(0, len(sorted)-50):] {
			want = append(want, x.m.Text)
		}
		got := []string{}
		for _, m := range view(casesOf(mk("c1", StateYou, withMessages(ms...)))).Detail.Messages {
			got = append(got, m.Text)
		}
		eq(t, "n "+itoa(n), got, want)
	}
	// A huge conversation, newest first as some sources send it.
	huge := make([]CaseMessage, 200_000)
	for i := range huge {
		huge[i] = CaseMessage{From: "F", Date: ago(float64(i) / 60), Text: "m" + itoa(i)}
	}
	d := view(casesOf(mk("c1", StateYou, withMessages(huge...)))).Detail
	check(t, len(d.Messages) == 50 && d.Messages[0].Text == "m49" && d.Messages[49].Text == "m0", "huge %d", len(d.Messages))
}

func TestTasksScanIsBounded(t *testing.T) {
	blanks := func(n int) []string { return slices.Repeat([]string{" \u200B "}, n) }
	tasks := func(ts []string) []string {
		got := view(casesOf(mk("c1", StateYou, withAnnotation(Annotation{State: optState(StateYou), Title: "T", Tasks: ts}))), annotatedOn()).Detail.Tasks
		if got == nil {
			return []string{}
		}
		return got
	}
	eq(t, "the 200th", tasks(append(blanks(199), "x")), []string{"x"}) // the 200th is still looked at
	eq(t, "the 201st", tasks(append(blanks(200), "x")), []string{})    // the 201st is not
	var thirty, twenty []string
	for i := range 30 {
		thirty = append(thirty, "t"+itoa(i))
		if i < 20 {
			twenty = append(twenty, "t"+itoa(i))
		}
	}
	eq(t, "twenty", tasks(append(blanks(150), thirty...)), twenty)
	eq(t, "nothing", tasks(blanks(100_000)), []string{})
}

func TestARepeatedCaseIDShowsTheFirstCaseOnly(t *testing.T) {
	first := mk("c1", StateHot, withSubject("first"))
	again := mk("c1", StateThem, withHours(0.5), withSubject("again"))
	v := view(casesOf(mk("c2", StateYou, withHours(2)), first, again, mk("c1", StateYou, withDone())))
	eq(t, "rows", rowsOf(v), []string{"c1", "c2"})
	eq(t, "title", v.Sections[0].Rows[0].Title, "first")
	var cols []string
	for _, c := range v.Columns {
		cols = append(cols, idsOf(c.Rows)...)
	}
	eq(t, "columns", cols, []string{"c1", "c2"})
	eq(t, "nav", navCounts(v), []int{2, 1, 1, 0, 0, 0, 0})
	eq(t, "accounts", accountCounts(v), []int{2, 2, 0})
	check(t, v.Detail.ID == "c1" && v.Detail.Title == "first", "detail %+v", v.Detail)
	selected := view(casesOf(first, again), configured(func(v *ViewState) { v.Style = StyleColumns; v.Selection = "c1" }))
	check(t, selected.Detail.Title == "first" && len(selected.Columns[2].Rows) == 0, "selected %+v", selected.Detail)
}

// Selection.

func selectionSample() []Case {
	return casesOf(
		mk("c1", StateHot, withHours(4)), mk("c2", StateYou, withHours(3)), mk("c3", StateYou, withHours(2)),
		mk("c4", StateThem, withHours(1)), mk("d1", StateInfo, withHours(6), withDone()),
		mk("d2", StateInfo, withHours(5), withDone()), mk("b1", StateYou, withAccount(accountB), withHours(7)),
	)
}

func TestResolveSelectionInTheList(t *testing.T) {
	s := testSnapshot(selectionSample()...)
	resolve := func(f func(*ViewState)) CaseID { return ResolveSelection(s, stateWith(f)) }
	none := func(*ViewState) {}
	// Inline detail, nothing selected: the first row.
	eq(t, "first row", resolve(none), CaseID("c1"))
	// A shown case stays.
	eq(t, "shown", resolve(func(v *ViewState) { v.Selection = "c3" }), CaseID("c3"))
	// Outside the filter: the first row of the filter.
	eq(t, "outside", resolve(func(v *ViewState) { v.Filter = filterState(StateYou); v.Selection = "c1" }), CaseID("c3"))
	eq(t, "inside", resolve(func(v *ViewState) { v.Filter = filterState(StateYou); v.Selection = "c3" }), CaseID("c3"))
	// Done is its own list.
	eq(t, "done", resolve(func(v *ViewState) { v.Filter = doneFilter }), CaseID("d2"))
	eq(t, "done d1", resolve(func(v *ViewState) { v.Filter = doneFilter; v.Selection = "d1" }), CaseID("d1"))
	eq(t, "d1 outside", resolve(func(v *ViewState) { v.Selection = "d1" }), CaseID("c1"))
	// Outside the account scope.
	eq(t, "scope", resolve(func(v *ViewState) { v.Account = accountB; v.Selection = "c1" }), CaseID("b1"))
	eq(t, "scope hot", resolve(func(v *ViewState) { v.Account = accountB; v.Filter = filterState(StateHot) }), CaseID(""))
	// Unknown.
	eq(t, "unknown", resolve(func(v *ViewState) { v.Selection = "zz" }), CaseID("c1"))
	// No inline detail: nothing selects itself, a valid selection stays.
	eq(t, "narrow", resolve(func(v *ViewState) { v.InlineDetail = false }), CaseID(""))
	eq(t, "narrow c2", resolve(func(v *ViewState) { v.InlineDetail = false; v.Selection = "c2" }), CaseID("c2"))
	eq(t, "narrow zz", resolve(func(v *ViewState) { v.InlineDetail = false; v.Selection = "zz" }), CaseID(""))
	// Empty list.
	eq(t, "empty", ResolveSelection(EmptySnapshot(), NewViewState()), CaseID(""))
}

func TestResolveSelectionInColumnsAndToday(t *testing.T) {
	s := testSnapshot(selectionSample()...)
	for _, style := range []Style{StyleColumns, StyleToday} {
		resolve := func(f func(*ViewState)) CaseID {
			return ResolveSelection(s, stateWith(func(v *ViewState) { v.Style = style; f(v) }))
		}
		// Never selects by itself, with or without inline detail.
		eq(t, "nothing", resolve(func(*ViewState) {}), CaseID(""))
		eq(t, "narrow", resolve(func(v *ViewState) { v.InlineDetail = false }), CaseID(""))
		// Any live case, whatever the filter.
		eq(t, "c4", resolve(func(v *ViewState) { v.Selection = "c4" }), CaseID("c4"))
		eq(t, "c4 hot", resolve(func(v *ViewState) { v.Filter = filterState(StateHot); v.Selection = "c4" }), CaseID("c4"))
		eq(t, "c4 done", resolve(func(v *ViewState) { v.Filter = doneFilter; v.Selection = "c4" }), CaseID("c4"))
		// A done case is not on the board here.
		eq(t, "d1", resolve(func(v *ViewState) { v.Selection = "d1" }), CaseID(""))
		eq(t, "d1 done", resolve(func(v *ViewState) { v.Filter = doneFilter; v.Selection = "d1" }), CaseID(""))
		// The account scope.
		eq(t, "scope c4", resolve(func(v *ViewState) { v.Account = accountB; v.Selection = "c4" }), CaseID(""))
		eq(t, "scope b1", resolve(func(v *ViewState) { v.Account = accountB; v.Selection = "b1" }), CaseID("b1"))
	}
}

func TestViewSelectionIsTheResolvedOne(t *testing.T) {
	v := view(selectionSample(), configured(func(v *ViewState) { v.Selection = "zz" }))
	check(t, v.Selection == "c1" && v.Detail.ID == "c1", "list %q", v.Selection)
	col := view(selectionSample(), configured(func(v *ViewState) { v.Style = StyleColumns; v.Selection = "c4" }))
	check(t, col.Selection == "c4" && col.Detail.ID == "c4" && col.ShowsPanel, "columns %q", col.Selection)
}

func TestSelectionAfterDone(t *testing.T) {
	s := testSnapshot(selectionSample()...)
	after := func(id CaseID, f func(*ViewState)) CaseID {
		if f == nil {
			f = func(*ViewState) {}
		}
		return SelectionAfterDone(id, s, stateWith(f))
	}
	// List rows: c1 hot, c3 c2 you (c3 is newer), b1 you, c4 them.
	eq(t, "shown", rowsOf(View(s, NewViewState(), testNow, testEnv)), []string{"c1", "c3", "c2", "b1", "c4"})
	eq(t, "first", after("c1", nil), CaseID("c3"))  // the first: the next
	eq(t, "middle", after("c3", nil), CaseID("c2")) // the middle: the next
	eq(t, "last", after("c4", nil), CaseID("b1"))   // the last: the previous
	eq(t, "unknown", after("zz", nil), CaseID(""))
	eq(t, "not listed", after("d1", nil), CaseID("")) // not in the list
	// Under a filter the filter's rows count.
	you := func(v *ViewState) { v.Filter = filterState(StateYou) }
	eq(t, "you c2", after("c2", you), CaseID("b1"))
	eq(t, "you b1", after("b1", you), CaseID("c2"))
	eq(t, "you c1", after("c1", you), CaseID(""))
	// Done list: reopening.
	done := func(v *ViewState) { v.Filter = doneFilter }
	eq(t, "done d2", after("d2", done), CaseID("d1"))
	eq(t, "done d1", after("d1", done), CaseID("d2"))
	// Account scope.
	b := func(v *ViewState) { v.Account = accountB }
	eq(t, "b b1", after("b1", b), CaseID("")) // the only row
	eq(t, "b c1", after("c1", b), CaseID(""))
	// Columns and Today select nothing.
	for _, style := range []Style{StyleColumns, StyleToday} {
		st := func(v *ViewState) { v.Style = style }
		eq(t, "style c1", after("c1", st), CaseID(""))
		eq(t, "style c3", after("c3", st), CaseID(""))
	}
}

// Cleaning.

func TestCleanLine(t *testing.T) {
	table := []struct{ in, want string }{
		{"plain", "plain"},
		{"  a   b  ", "a b"},
		{"a\tb", "a b"},
		{"a\r\nb", "a b"},
		{"a\nb\rc", "a b c"},
		{"a\u2028b\u2029c", "a b c"},
		{"a\u0085b", "a b"},
		{"a\x00b", "ab"},
		{"a\u202Eb", "ab"}, // right-to-left override
		{"\u202Ea\u202C", "a"},
		{"a\u200Bb\u200Dc\uFEFFd", "ab\u200Dcd"}, // zero width, joiner kept between two letters, BOM
		{"a\u2066b\u2069", "ab"},                 // isolates
		{"a\u0007b\u001Bc", "abc"},               // bell, escape
		{"\u202E\u200B\x00\uFEFF", ""},           // only format characters
		{"", ""},
		{"   \n\t ", ""},
		{"čeština ✓ 🙂", "čeština ✓ 🙂"},
		{"a\xffb\xc3", "ab"}, // invalid UTF-8
		{"a\uFFFDb", "ab"},   // the replacement character
	}
	for _, c := range table {
		if got := CleanLine(c.in, 100); got != c.want {
			t.Errorf("CleanLine(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	eq(t, "max 0", CleanLine("abc", 0), "")
}

func TestCleanLineCaps(t *testing.T) {
	eq(t, "abc", CleanLine("abcdef", 3), "abc")
	eq(t, "space at the cut", CleanLine("ab cd", 3), "ab") // the space at the cut goes
	eq(t, "é", CleanLine("éé", 3), "é")                    // a scalar is never cut
	eq(t, "€€", CleanLine("€€€", 8), "€€")
	eq(t, "🙂", CleanLine("🙂🙂", 7), "🙂")
	eq(t, "nothing", CleanLine("🙂", 3), "")
}

func TestCleanLineBigInput(t *testing.T) {
	mb := strings.Repeat("é", 500_000) // 1 MB
	for _, n := range []int{1, 2, 99, 100, 300} {
		out := CleanLine(mb, n)
		check(t, len(out) == n-n%2, "max %d: %d bytes", n, len(out))
		check(t, strings.Trim(out, "é") == "", "max %d: %q", n, out)
	}
	three := strings.Repeat("€", 400_000)
	eq(t, "three-byte", len(CleanLine(three, 100)), 99) // 33 characters of 3 bytes, cut on the boundary
	// A megabyte of nothing but format characters and whitespace.
	nothing := strings.Repeat("\u202E \u200B\n", 250_000)
	eq(t, "nothing line", CleanLine(nothing, 300), "")
	eq(t, "nothing block", CleanBlock(nothing, 300), "")
	// A megabyte of spaces between two words: the scan gives up long before
	// the second word, so the text ends with the first.
	gap := "a" + strings.Repeat(" ", 1_000_000) + "b"
	eq(t, "gap line", CleanLine(gap, 300), "a")
	eq(t, "gap block", CleanBlock(gap, 300), "a")
	// A shorter gap is read through.
	eq(t, "short gap", CleanLine("a"+strings.Repeat(" ", 1000)+"b", 300), "a b")
}

// TestCleaningIsBoundedForDroppedInput: a megabyte of what the cleaners
// drop costs no more than a short input; they read a bounded number of
// characters (8 × the cap).
func TestCleaningIsBoundedForDroppedInput(t *testing.T) {
	zw := strings.Repeat("\u200B", 1_000_000)
	spaces := strings.Repeat(" ", 1_000_000)
	start := time.Now()
	for range 20 {
		check(t, CleanLine(zw, 300) == "" && CleanBlock(zw, 4000) == "", "zero width")
		check(t, CleanLine(spaces, 300) == "" && CleanBlock(spaces, 4000) == "", "spaces")
	}
	// Eighty full scans of a megabyte would take far longer.
	check(t, time.Since(start) < 2*time.Second, "took %v", time.Since(start))
	// Past the budget nothing more is read, even what would be kept.
	eq(t, "24", CleanLine(strings.Repeat("\u200B", 24)+"x", 3), "")
	eq(t, "23", CleanLine(strings.Repeat("\u200B", 23)+"x", 3), "x")
	eq(t, "24 block", CleanBlock(strings.Repeat("\u200B", 24)+"x", 3), "")

	// A whole view over such a case.
	c := mk("c1", StateYou, withSubject(zw), withSnippet(spaces), withMessages(CaseMessage{From: zw, Date: ago(1), Text: spaces}))
	v := view(casesOf(c, mk("c2", StateYou, withHours(2))))
	r := v.Sections[0].Rows[0]
	check(t, r.ID == "c1" && r.Title == "(No subject)" && r.Snippet == "" && r.Person == "P c1", "row %+v", r)
	d := v.Detail
	check(t, d.ID == "c1" && d.Title == "(No subject)" && d.Subject == "", "detail %+v", d)
	check(t, len(d.Messages) == 1 && d.Messages[0].From == "" && d.Messages[0].Text == "", "messages %+v", d.Messages)
	eq(t, "rows", idsOf(v.Sections[0].Rows), []string{"c1", "c2"})
}

func TestTheCutNeverBreaksACharacter(t *testing.T) {
	e := "e\u0301"                         // é, decomposed: 3 bytes
	eq(t, "3", CleanLine("ab"+e, 3), "ab") // the e would lose its accent
	eq(t, "4", CleanLine("ab"+e, 4), "ab")
	eq(t, "5", CleanLine("ab"+e, 5), "ab"+e)         // it fits
	eq(t, "space", CleanLine("ab "+e+"cd", 4), "ab") // the space before goes too
	eq(t, "block line", CleanBlock("ab\n"+e, 4), "ab")
	eq(t, "block", CleanBlock("ab"+e, 3), "ab")
	// Several marks on one letter: the cluster goes whole.
	eq(t, "marks", CleanLine("a"+"e\u0301\u0302\u0303", 5), "a")
	cz := "\U0001F1E8\U0001F1FF"          // a flag: two regional indicators, 8 bytes
	eq(t, "12", CleanLine(cz+cz, 12), cz) // not a lone indicator
	eq(t, "16", CleanLine(cz+cz, 16), cz+cz)
	eq(t, "4", CleanLine(cz, 4), "")
	eq(t, "block 15", CleanBlock(cz+cz, 15), cz)
	// Not cut at all: a trailing cluster stays as it is.
	eq(t, "trailing", CleanLine("x\U0001F1E8", 100), "x\U0001F1E8")
	// A joiner between two kept characters stays (the joiner rule).
	eq(t, "joiners", CleanLine("a\u200Db\u200Cc", 100), "a\u200Db\u200Cc")
	// A cut right after a joiner does not leave it last.
	eq(t, "cut after a joiner", CleanLine("ab\u200Dc", 5), "ab")
	// A Hangul syllable written in jamo is one character.
	jamo := "\u1100\u1161\u11A8" // 각, 9 bytes
	eq(t, "jamo", CleanLine("a"+jamo, 7), "a")
	eq(t, "jamo whole", CleanLine("a"+jamo, 10), "a"+jamo)
	// An emoji with its skin tone.
	tone := "👍\U0001F3FD"
	eq(t, "tone", CleanLine("a"+tone, 5), "a")
}

func TestCleanBlock(t *testing.T) {
	table := []struct{ in, want string }{
		{"plain", "plain"},
		{"a\nb", "a\nb"},
		{"a\r\nb", "a\nb"},
		{"a\rb", "a\nb"},
		{"a\r\rb", "a\n\nb"},
		{"a\n\rb", "a\n\nb"},
		{"a\u2028b", "a\nb"},
		{"a\u2029b", "a\nb"},
		{"a\u0085b\u000Bc\u000Cd", "a\nb\nc\nd"},
		{"a\n\n\n\n\nb", "a\n\nb"}, // at most one empty line
		{"a  \nb", "a\nb"},         // no spaces at the end of a line
		{"a\n  b", "a\n  b"},       // indentation stays
		{"a\tb", "a b"},
		{"\n\n a\n\n", "a"},
		{"a\x00b", "ab"},
		{"a\u202Eb\u200Bc", "abc"},
		{"\u202E\u200B\x00", ""},
		{"", ""},
	}
	for _, c := range table {
		if got := CleanBlock(c.in, 100); got != c.want {
			t.Errorf("CleanBlock(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	eq(t, "max 0", CleanBlock("abc", 0), "")
	eq(t, "cut at the break", CleanBlock("ab\n\ncd", 3), "ab")
	eq(t, "é", CleanBlock("éé", 3), "é")
}

func TestCleanBlockBigInput(t *testing.T) {
	eq(t, "é", len(CleanBlock(strings.Repeat("é", 500_000), 4000)), 4000)
	blk := CleanBlock(strings.Repeat("line\r\n", 200_000), 4000)
	check(t, len(blk) <= 4000 && !strings.Contains(blk, "\r") && strings.HasPrefix(blk, "line\nline"), "block %q…", blk[:20])
	check(t, !strings.HasSuffix(blk, "\n"), "a trailing line break")
}

// The samples.

func sampleStrings(s Snapshot) []string {
	var out []string
	for _, a := range s.Accounts {
		out = append(out, a.Name, a.Badge)
	}
	for _, c := range s.Cases {
		out = append(out, c.Person, c.Subject, c.Snippet, Reason(string(c.RuleReason), tr))
		if c.Draft != nil {
			out = append(out, c.Draft.Text)
		}
		if c.Issue != nil {
			out = append(out, c.Issue.Key, c.Issue.Status)
		}
		if a := c.Annotation; a != nil {
			out = append(out, a.Title, a.Summary, a.Why, a.DueQuote)
			out = append(out, a.Tasks...)
		}
		for _, m := range c.Messages {
			out = append(out, m.From, m.Text)
		}
	}
	for _, k := range s.Commitments {
		out = append(out, k.Text, k.Quote)
	}
	if s.Run != nil {
		out = append(out, s.Run.Model, s.Run.Note)
	}
	return out
}

func TestSamplesEveryAddressEndsInInvalid(t *testing.T) {
	strs := sampleStrings(SampleSnapshot(testNow, time.UTC))
	found := 0
	for _, s := range strs {
		for _, token := range strings.Fields(s) {
			if !strings.Contains(token, "@") {
				continue
			}
			found++
			tk := strings.TrimFunc(token, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
			check(t, strings.HasSuffix(tk, ".invalid"), "%q", token)
		}
		// No web address either.
		check(t, !strings.Contains(s, "http") && !strings.Contains(s, "www."), "%q", s)
	}
	check(t, found >= 1, "the samples use no address, so the check has no teeth")
}

func TestSamplesCoverTheStatesAndShapes(t *testing.T) {
	snap := SampleSnapshot(testNow, time.UTC)
	states := map[State]bool{}
	for _, c := range snap.Cases {
		states[StateOf(c, true)] = true
	}
	check(t, len(states) == len(States), "states %v", states)
	check(t, snap.Annotated && len(snap.Accounts) == 4, "annotated %v, %d accounts", snap.Annotated, len(snap.Accounts))
	accounts := map[api.AccountID]bool{}
	for _, a := range snap.Accounts {
		accounts[a.ID] = true
	}
	check(t, len(accounts) == 4, "account ids repeat")
	ids := map[CaseID]bool{}
	done, jiraCases := 0, 0
	var unannotated, drafted, tasks, user, kept, changed, unread, attachments bool
	for _, c := range snap.Cases {
		ids[c.ID] = true
		check(t, accounts[c.Account], "%s: unknown account", c.ID)
		if c.Done() {
			done++
		}
		if c.Issue != nil {
			jiraCases++
			check(t, strings.HasPrefix(c.Issue.Key, "DEMO-"), "%s: key %q", c.ID, c.Issue.Key)
		}
		unannotated = unannotated || c.Annotation == nil
		drafted = drafted || c.Draft != nil
		check(t, slices.Contains(KnownReasons, c.RuleReason), "%s: reason %q", c.ID, c.RuleReason)
		check(t, c.MessagesLoaded, "%s: no messages", c.ID) // loaded: the samples need no daemon
		tasks = tasks || (c.Annotation != nil && len(c.Annotation.Tasks) > 0)
		user = user || c.UserState != nil
		src := StateSourceOf(c, true)
		kept = kept || src.Kind == SourceAssistantKept
		changed = changed || src.Kind == SourceAssistantChanged
		unread = unread || c.Unread
		attachments = attachments || c.HasAttachments
		check(t, !c.Date.After(testNow), "%s: in the future", c.ID)
	}
	check(t, len(ids) == len(snap.Cases), "case ids repeat")
	check(t, done == 2 && jiraCases > 0, "done %d, jira %d", done, jiraCases)
	check(t, unannotated && drafted && tasks && user && kept && changed && unread && attachments,
		"shapes: %v %v %v %v %v %v %v %v", unannotated, drafted, tasks, user, kept, changed, unread, attachments)
}

func TestSamplesViewShowsEverything(t *testing.T) {
	snap := SampleSnapshot(testNow, time.UTC)
	v := View(snap, NewViewState(), testNow, testEnv)
	check(t, !v.IsEmpty, "empty")
	var kinds []DueGroup
	for _, g := range v.Today.DueGroups {
		kinds = append(kinds, g.Kind)
	}
	eq(t, "due groups", kinds, DueGroups)
	check(t, len(v.Commitments) >= 2, "%d commitments", len(v.Commitments))
	for _, k := range v.Commitments {
		check(t, k.CaseID != "sample-21", "a commitment of a done case") // on a done case
	}
	check(t, v.Today.YouMore >= 1 && len(v.Today.You) == YouTopCount, "today %+v", v.Today)
	for _, c := range v.Columns {
		check(t, len(c.Rows) > 0, "column %d is empty", c.State)
	}
	check(t, len(v.Sections) == 4 && v.Detail != nil, "sections %d", len(v.Sections))
	done := View(snap, stateWith(func(v *ViewState) { v.Filter = doneFilter }), testNow, testEnv)
	check(t, len(done.Sections[0].Rows) == 2, "done %d", len(done.Sections[0].Rows))
	check(t, strings.Contains(v.StatusLine, "Claude"), "status %q", v.StatusLine)
}

// TestSamplesDueQuotesCiteTheMessages: a due quote cites the conversation
// word for word, a part of one of the case's messages.
func TestSamplesDueQuotesCiteTheMessages(t *testing.T) {
	snap := SampleSnapshot(testNow, time.UTC)
	checked := 0
	for _, c := range snap.Cases {
		a := c.Annotation
		if a == nil || a.DueQuote == "" || len(c.Messages) == 0 {
			continue
		}
		check(t, slices.ContainsFunc(c.Messages, func(m CaseMessage) bool { return strings.Contains(m.Text, a.DueQuote) }),
			"%s: %q", c.ID, a.DueQuote)
		checked++
	}
	check(t, checked >= 4, "%d checked", checked)
	// No weekday names in what has a deadline: the dates are relative.
	weekdays := []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}
	for _, c := range snap.Cases {
		a := c.Annotation
		if a == nil || a.Due == nil {
			continue
		}
		texts := []string{c.Snippet, a.Summary, a.Why, a.DueQuote}
		if c.Draft != nil {
			texts = append(texts, c.Draft.Text)
		}
		for _, m := range c.Messages {
			texts = append(texts, m.Text)
		}
		for _, tx := range texts {
			for _, w := range weekdays {
				check(t, !strings.Contains(tx, w), "%s: %q names %s", c.ID, tx, w)
			}
		}
	}
}

func TestSamplesStableForAFixedNow(t *testing.T) {
	snap := SampleSnapshot(testNow, time.UTC)
	check(t, SampleSnapshot(testNow, time.UTC).Equal(snap), "two sample boards differ")
	// Relative to now: a day later, the same shape with later dates.
	later := SampleSnapshot(testNow.Add(24*time.Hour), time.UTC)
	check(t, !later.Equal(snap), "a day later is the same")
	var a, b []CaseID
	for i := range snap.Cases {
		a = append(a, snap.Cases[i].ID)
		b = append(b, later.Cases[i].ID)
	}
	eq(t, "ids", b, a)
}

// The inline suggested reply and Unstar in the detail.

func TestDraftIDWithoutText(t *testing.T) {
	// An empty draft is still the case's suggested reply: edited inline.
	d := view(casesOf(mk("c1", StateYou, withDraft("")))).Detail
	check(t, d.DraftID == "d_c1" && d.Draft == "", "draft %q %q", d.DraftID, d.Draft)
	eq(t, "none", view(casesOf(mk("c1", StateYou))).Detail.DraftID, api.DraftID(""))
}

func TestUnstarOnlyForTheStar(t *testing.T) {
	detail := func(reason api.BoardReason, done bool, user *State) *Detail {
		c := mk("c1", StateHot, withReason(reason))
		c.UserState = user
		if done {
			c.Visibility = Visibility{Kind: VisibleDone}
		}
		return view(casesOf(c), configured(func(v *ViewState) {
			if done {
				v.Filter = doneFilter
			}
		})).Detail
	}
	check(t, detail(api.BoardReasonHotFlagged, false, nil).CanUnstar, "flagged")
	check(t, detail(api.BoardReasonHotFlagged, false, optState(StateInfo)).CanUnstar, "whatever the user's state")
	check(t, !detail(api.BoardReasonHotFlagged, true, nil).CanUnstar, "done")
	check(t, !detail(api.BoardReasonHotImportant, false, nil).CanUnstar, "important")
	check(t, !detail(api.BoardReasonYouAddressed, false, nil).CanUnstar, "addressed")
}

// The inline reply editor's height: the content's, clamped to [160,
// min(480, 0.6 × visible)].

func TestEditorHeightClamps(t *testing.T) {
	const tall = 1000
	// Short content: the minimum, no scrolling.
	eq(t, "short", NewEditorHeight(40, tall), NewEditorHeight(0, tall))
	eq(t, "short value", NewEditorHeight(40, tall), EditorHeight{Height: 160})
	// In between: the content's.
	eq(t, "between", NewEditorHeight(300, tall), EditorHeight{Height: 300})
	// 0.6 × 1000 = 600 > 480: the maximum caps it.
	eq(t, "tall", NewEditorHeight(700, tall), EditorHeight{Height: 480, Scrolls: true})
	// A small detail: 0.6 × 500 = 300.
	eq(t, "small", NewEditorHeight(700, 500), EditorHeight{Height: 300, Scrolls: true})
	eq(t, "small fits", NewEditorHeight(300, 500), EditorHeight{Height: 300})
	// Tiny: never below the minimum.
	eq(t, "tiny", NewEditorHeight(700, 100), EditorHeight{Height: 160, Scrolls: true})
	eq(t, "cap 100", EditorHeightCap(100), 160.0)
	eq(t, "cap 700", EditorHeightCap(700), 420.0)
}

func TestEditorHeightOddInput(t *testing.T) {
	eq(t, "nan", NewEditorHeight(math.NaN(), 1000).Height, 160.0)
	eq(t, "inf", NewEditorHeight(math.Inf(1), 1000).Height, 160.0)
	eq(t, "negative", NewEditorHeight(-5, 1000).Height, 160.0)
	eq(t, "cap 0", EditorHeightCap(0), 480.0)
	eq(t, "cap nan", EditorHeightCap(math.NaN()), 480.0)
}
