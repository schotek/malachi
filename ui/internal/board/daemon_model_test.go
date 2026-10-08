// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/client"
)

// The board's model with what the daemon adds (docs/api.md §4.13): the
// reason codes and their texts, stale annotations, the linked draft,
// visibility (done, snoozed), commitments' states, the conversation loaded
// on demand, the phases and the failure texts (macOS
// BoardDaemonModelTests.swift, the same cases).

// TestEveryKnownCodeHasItsOwnText: every code of the contract has a text of
// its own; nothing else does.
func TestEveryKnownCodeHasItsOwnText(t *testing.T) {
	seen := map[string]bool{}
	for _, code := range KnownReasons {
		text := Reason(string(code), tr)
		check(t, text != "" && text != ReasonUnknown(tr), "%s has no text", code)
		check(t, !seen[text], "%s repeats another code's text", code)
		seen[text] = true
	}
	eq(t, "known", len(KnownReasons), 17)
}

func TestUnknownCodesGetTheGenericText(t *testing.T) {
	for _, code := range []string{"", "hot.someday", "HOT.IMPORTANT", "you.addressed ", "rule c1"} {
		eq(t, code, Reason(code, tr), ReasonUnknown(tr))
	}
}

func staleCase() Case {
	due := day(16, 10, 0)
	return mk("c1", StateYou, withSubject("Raw subject"), withAnnotation(Annotation{
		State: optState(StateHot), Title: "Old title", Summary: "Old summary", Why: "Old why", Due: &due,
		DueQuote: "by tomorrow", Tasks: []string{"t1"}, Stale: true,
	}), withDraft("Hi"))
}

// TestStaleAnnotationCountsForNothing: a stale annotation counts for
// nothing; its draft stays.
func TestStaleAnnotationCountsForNothing(t *testing.T) {
	c := staleCase()
	eq(t, "state", StateOf(c, true), StateYou)
	eq(t, "source", StateSourceOf(c, true), Source{Kind: SourceRules})
	check(t, AnnotationOf(c, true) == nil, "a stale annotation counts")
	v := view(casesOf(c), annotatedOn())
	d := v.Detail
	check(t, d.Title == "Raw subject" && d.Summary == "" && len(d.Tasks) == 0 && d.Due == "" && d.DueQuote == "", "detail %+v", d)
	eq(t, "why", d.Why, ReasonUnknown(tr))
	eq(t, "stale note", d.StaleNote, StaleNotes(tr))
	check(t, d.Draft == "Hi" && d.DraftID != "", "draft %q %q", d.Draft, d.DraftID)
	check(t, len(v.Today.DueGroups) == 0 && v.Sections[0].Rows[0].Due == "", "a stale deadline shows")
	// Without the assistant no note: nothing of it was shown anyway.
	eq(t, "off", view(casesOf(c)).Detail.StaleNote, "")
}

// TestAnnotationWithoutAState: an annotation that leaves the state to the
// rules kept it.
func TestAnnotationWithoutAState(t *testing.T) {
	c := mk("c1", StateThem, withAnnotation(Annotation{Title: "T"}))
	eq(t, "state", StateOf(c, true), StateThem)
	eq(t, "source", StateSourceOf(c, true), Source{Kind: SourceAssistantKept})
	eq(t, "title", view(casesOf(c), annotatedOn()).Detail.Title, "T")
}

var back = day(16, 9, 0)

func visibilityCases() []Case {
	return casesOf(
		mk("l1", StateYou, withHours(1)), mk("d1", StateInfo, withHours(2), withDone()),
		mk("s1", StateYou, withHours(3), withVisibility(Visibility{Kind: VisibleSnoozed, At: day(20, 9, 0)})),
		mk("s2", StateHot, withHours(4), withVisibility(Visibility{Kind: VisibleSnoozed, At: back})),
	)
}

// TestSnoozedHaveTheirOwnFilter: snoozed cases are off the board and
// listed under Snoozed, soonest back first; Done lists and counts only the
// done ones.
func TestSnoozedHaveTheirOwnFilter(t *testing.T) {
	all := view(visibilityCases())
	eq(t, "rows", rowsOf(all), []string{"l1"})
	var cols []string
	for _, c := range all.Columns {
		cols = append(cols, idsOf(c.Rows)...)
	}
	eq(t, "columns", cols, []string{"l1"})
	eq(t, "done count", all.Nav[len(all.Nav)-1].Count, 1)
	eq(t, "snoozed nav", all.Nav[len(all.Nav)-2].Filter.Kind, FilterSnoozed)
	eq(t, "snoozed count", all.Nav[len(all.Nav)-2].Count, 2)
	eq(t, "overview count", all.Nav[0].Count, 1)
	eq(t, "accounts count", all.Accounts[0].Count, 1) // live only
	done := view(visibilityCases(), configured(func(v *ViewState) { v.Filter = doneFilter }))
	eq(t, "kinds", sectionKinds(done), []string{"done"})
	eq(t, "done rows", idsOf(done.Sections[0].Rows), []string{"d1"})
	eq(t, "no remind", done.Sections[0].Rows[0].Remind, "")
	snoozed := view(visibilityCases(), configured(func(v *ViewState) { v.Filter = Filter{Kind: FilterSnoozed} }))
	eq(t, "snoozed kinds", sectionKinds(snoozed), []string{"snoozed"})
	eq(t, "title", snoozed.Sections[0].Title, "Snoozed")
	eq(t, "snoozed rows", idsOf(snoozed.Sections[0].Rows), []string{"s2", "s1"})
	eq(t, "remind", snoozed.Sections[0].Rows[0].Remind, "Tomorrow at 09:00")
	eq(t, "selection", snoozed.Selection, CaseID("s2"))
	d := snoozed.Detail
	check(t, d.IsSnoozed && !d.IsDone && d.RemindText == "Back on the board: Tomorrow at 09:00", "detail %+v", d)
	// Only snoozed: not empty.
	check(t, !view(casesOf(mk("s1", StateYou, withVisibility(Visibility{Kind: VisibleSnoozed, At: back})))).IsEmpty, "empty")
}

func TestSelectionAfterDoneWalksTheSnoozed(t *testing.T) {
	v := stateWith(func(v *ViewState) { v.Filter = Filter{Kind: FilterSnoozed} })
	s := testSnapshot(visibilityCases()...)
	eq(t, "s2", SelectionAfterDone("s2", s, v), CaseID("s1"))
	eq(t, "s1", SelectionAfterDone("s1", s, v), CaseID("s2"))
	eq(t, "d1 not listed", SelectionAfterDone("d1", s, v), CaseID(""))
}

func TestDoneIsTheVisibility(t *testing.T) {
	c := mk("c1", StateYou)
	check(t, !c.Done() && c.Visibility.IsLive(), "a new case is not live")
	c.SetDone(true)
	eq(t, "done", c.Visibility, Visibility{Kind: VisibleDone})
	c.Visibility = Visibility{Kind: VisibleDone, At: ago(1)}
	c.SetDone(true) // stays as it is, date and all
	eq(t, "done at", c.Visibility, Visibility{Kind: VisibleDone, At: ago(1)})
	c.Visibility = Visibility{Kind: VisibleSnoozed, At: back}
	check(t, !c.Done(), "snoozed is done")
	c.SetDone(false) // not done already: the remind stays
	at, ok := c.Visibility.RemindAt()
	check(t, ok && at.Equal(back), "the remind went")
	c.SetDone(true)
	eq(t, "done again", c.Visibility, Visibility{Kind: VisibleDone})
}

func TestDetailCarriesTheTargets(t *testing.T) {
	c := mk("c1", StateYou)
	c.Thread = "t_9"
	c.Reply = &ReplyTarget{Message: "m_5", Folder: "f_inbox"}
	c.LatestMessage = "m_7"
	c.CanArchive = true
	d := view(casesOf(c)).Detail
	check(t, d.AccountID == accountA && d.Thread == "t_9" && d.LatestMessage == "m_7", "detail %+v", d)
	eq(t, "reply", *d.Reply, ReplyTarget{Message: "m_5", Folder: "f_inbox"})
	check(t, d.CanArchive && d.Draft == "" && d.DraftID == "", "detail %+v", d)
}

// TestMessagesOnDemand: the conversation is loading until it arrives, a
// note when it failed, the cards once there.
func TestMessagesOnDemand(t *testing.T) {
	loading := view(casesOf(mk("c1", StateYou, notLoaded()))).Detail
	check(t, loading.MessagesLoading && loading.MessagesNote == MessagesLoading(tr) && len(loading.Messages) == 0, "loading %+v", loading)
	failed := mk("c1", StateYou, notLoaded())
	failed.MessagesFailed = true
	f := view(casesOf(failed)).Detail
	check(t, !f.MessagesLoading && f.MessagesNote == MessagesFailed(tr) && f.MessagesRetry, "failed %+v", f)
	loaded := view(casesOf(mk("c1", StateYou, withMessages(CaseMessage{ID: "m_1", From: "Ann", Date: ago(2), Text: "hello"})))).Detail
	check(t, !loaded.MessagesLoading && loaded.MessagesNote == "" && len(loaded.Messages) == 1 && loaded.Messages[0].ID == "m_1", "loaded %+v", loaded)
	none := view(casesOf(mk("c1", StateYou, withMessages()))).Detail
	check(t, !none.MessagesLoading && none.MessagesNote == "" && len(none.Messages) == 0, "none %+v", none)
}

// TestOnlyOpenCommitments: only open promises are shown.
func TestOnlyOpenCommitments(t *testing.T) {
	ks := []Commitment{
		{ID: "k1", CaseID: "c1", Text: "Open"},
		{ID: "k2", CaseID: "c1", Text: "Ticked", State: CommitmentDone},
		{ID: "k3", CaseID: "c1", Text: "Closed", State: CommitmentClosed},
	}
	v := view(casesOf(mk("c1", StateYou)), annotatedOn(), withCommitments(ks...))
	eq(t, "ids", commitmentIDs(v.Commitments), []string{"k1"})
	eq(t, "tile", v.Today.Tiles[len(v.Today.Tiles)-1], Tile{Kind: TileCommitments, Count: 1, Title: Commitments(tr), ToolTip: "Promised: 1 promise"})
}

func phaseView(p Phase, truncated bool, cases ...Case) ViewModel {
	s := Snapshot{Accounts: testAccounts, Cases: cases, Phase: p, Truncated: truncated}
	return View(s, NewViewState(), testNow, testEnv)
}

func TestEmptyTextsFollowThePhase(t *testing.T) {
	ready := phaseView(PhaseReady, false)
	check(t, ready.Phase == PhaseReady && ready.IsEmpty, "ready %+v", ready.Phase)
	check(t, ready.EmptyTitle == EmptyTitle(tr) && ready.EmptyBody == EmptyBody(tr) && ready.Notice == "", "ready texts")
	titles := map[string]bool{}
	for _, p := range []Phase{PhaseLoading, PhasePreparing, PhaseReady, PhaseUnavailable, PhaseOff} {
		v := phaseView(p, false)
		check(t, v.Phase == p && v.EmptyTitle != "", "phase %d", p)
		titles[v.EmptyTitle] = true
	}
	eq(t, "titles", len(titles), 5)
	eq(t, "loading body", phaseView(PhaseLoading, false).EmptyBody, "")
}

// TestPhaseNotice: the notice says when the cases shown are partial or old.
func TestPhaseNotice(t *testing.T) {
	c := mk("c1", StateYou)
	check(t, strings.Contains(phaseView(PhasePreparing, false, c).Notice, "first time"), "preparing")
	check(t, strings.Contains(phaseView(PhaseUnavailable, false, c).Notice, "not running"), "unavailable")
	eq(t, "ready", phaseView(PhaseReady, false, c).Notice, "")
	check(t, strings.Contains(phaseView(PhaseReady, true, c).Notice, "1,000"), "truncated")
	eq(t, "off", phaseView(PhaseOff, false).Notice, "")
}

func TestTriageIsCarried(t *testing.T) {
	run := &Run{Model: "Claude", Date: ago(1), Annotated: 4, Running: true}
	s := Snapshot{Run: run, Triage: TriageInfo{Queue: 7, AnnotatedToday: 12}, Phase: PhaseReady}
	v := View(s, NewViewState(), testNow, testEnv)
	check(t, v.Triage == TriageInfo{Queue: 7, AnnotatedToday: 12} && v.Run == run, "triage %+v run %+v", v.Triage, v.Run)
}

// TestFailureTexts: what failed, and why when the error says; never the
// daemon's message.
func TestFailureTexts(t *testing.T) {
	secret := "SELECT * FROM cases -- internal detail"
	cases := []struct {
		err  error
		want string
	}{
		{client.ErrDisconnected, "Moving the case failed: the mail backend is not running."},
		{fmt.Errorf("board.setState: %w", client.ErrDisconnected), "Moving the case failed: the mail backend is not running."},
		{context.DeadlineExceeded, "Moving the case failed: the mail backend did not answer in time."},
		{context.Canceled, "Moving the case failed: the mail backend did not answer in time."},
		{&api.Error{Code: api.CodeCaseNotFound, Message: secret}, "Moving the case failed: the case is no longer on the board."},
		{&api.Error{Code: api.CodeInvalidArgument, Message: secret}, "Moving the case failed: the board did not accept it."},
		{&api.Error{Code: api.CodeMethodNotFound, Message: secret}, "Moving the case failed: this mail backend has no board."},
		{&api.Error{Code: api.CodeNotImplemented, Message: secret}, "Moving the case failed: this mail backend has no board."},
		{&api.Error{Code: api.CodeStorageError, Message: secret}, "Moving the case failed: the mail backend could not save it."},
		{&api.Error{Code: api.CodeDraftNotFound, Message: secret}, "Moving the case failed: the draft no longer exists."},
		{&api.Error{Code: api.CodeInternalError, Message: secret}, "Moving the case failed."},
		{errors.New(secret), "Moving the case failed."},
	}
	for _, c := range cases {
		got := FailedText(ActionMove, c.err, tr)
		eq(t, c.err.Error(), got, c.want)
		check(t, !strings.Contains(got, "SELECT"), "%q leaks", got)
	}
	seen := map[string]bool{}
	for _, a := range Actions {
		s := FailedText(a, &api.Error{Code: api.CodeInternalError}, tr)
		check(t, !seen[s], "%q repeats", s)
		seen[s] = true
	}
}

// TestFailurePhasesHaveTheirOwnTexts: a board that could not be listed,
// and a backend without the board, say so in texts of their own, apart from
// a backend not running.
func TestFailurePhasesHaveTheirOwnTexts(t *testing.T) {
	phases := []Phase{PhaseUnavailable, PhaseFailed, PhaseUnsupported}
	bodies, notices := map[string]bool{}, map[string]bool{}
	for _, p := range phases {
		bodies[phaseView(p, false).EmptyBody] = true
		notices[phaseView(p, false, mk("c1", StateYou)).Notice] = true
	}
	check(t, len(bodies) == 3 && len(notices) == 3, "bodies %v notices %v", bodies, notices)
	check(t, strings.Contains(phaseView(PhaseFailed, false, mk("c1", StateYou)).Notice, "could not be loaded"), "failed")
	check(t, strings.Contains(phaseView(PhaseUnsupported, false).EmptyBody, "no board"), "unsupported")
	var failures []Phase
	for _, p := range []Phase{PhaseLoading, PhasePreparing, PhaseReady, PhaseUnavailable, PhaseFailed, PhaseUnsupported, PhaseOff} {
		if p.IsFailure() {
			failures = append(failures, p)
		}
	}
	eq(t, "failures", failures, phases)
}

// TestAMissingPromiseIsNamed: board.setCommitment's caseNotFound is a
// promise that is gone.
func TestAMissingPromiseIsNamed(t *testing.T) {
	e := &api.Error{Code: api.CodeCaseNotFound, Message: "x"}
	eq(t, "commitment", FailedText(ActionCommitment, e, tr), "Changing the promise failed: the promise no longer exists.")
	eq(t, "done", FailedText(ActionDone, e, tr), "Marking the case done failed: the case is no longer on the board.")
	eq(t, "unflag", FailedText(ActionUnflag, e, tr), "Removing the star failed: the case is no longer on the board.")
}

// Text the assistant wrote is marked as the assistant's wherever it shows
// (docs/api.md §4.13): flags in the view model, "Assistant:" for the
// screen reader.

func markedCase(title, summary, why string) Case {
	due := day(16, 10, 0)
	return mk("c1", StateYou, withSubject("Raw subject"), withSnippet("Raw snippet"), withAnnotation(Annotation{
		Title: title, Summary: summary, Why: why, Due: &due, DueQuote: "by tomorrow",
	}))
}

func TestTheAssistantsTitleIsMarked(t *testing.T) {
	c := markedCase("Approve the budget", "Anna asks", "She waits")
	v := view(casesOf(c), annotatedOn(), withCommitments(Commitment{ID: "k1", CaseID: c.ID, Text: "Send it"}))
	row := v.Sections[0].Rows[0]
	check(t, row.Title == "Approve the budget" && row.TitleIsAssistant, "row %+v", row)
	check(t, row.Snippet == "Anna asks" && row.SnippetIsAssistant && row.MarksAssistant(), "row %+v", row)
	check(t, strings.Contains(row.Spoken, "Assistant: Approve the budget"), "spoken %q", row.Spoken)
	d := v.Detail
	check(t, d.TitleIsAssistant && d.SpokenTitle == "Assistant: Approve the budget", "detail %+v", d)
	check(t, d.Why == "She waits" && d.WhyIsAssistant, "why %q", d.Why)
	due := v.Today.DueGroups[0].Items[0]
	check(t, due.TitleIsAssistant && due.SpokenTitle == "Assistant: Approve the budget", "due %+v", due)
	k := v.Commitments[0]
	check(t, k.From == "Approve the budget" && k.FromIsAssistant && k.SpokenFrom == "Assistant: Approve the budget", "commitment %+v", k)
}

// TestTheDaemonsTextIsNot: the subject, the message's snippet and the
// rules' reason are not the assistant's.
func TestTheDaemonsTextIsNot(t *testing.T) {
	for i, c := range []struct {
		k  Case
		on bool
	}{
		{markedCase("", "", ""), true},
		{markedCase("Title", "S", "W"), false},
	} {
		var opts []viewOpt
		if c.on {
			opts = append(opts, annotatedOn())
		}
		v := view(casesOf(c.k), opts...)
		row := v.Sections[0].Rows[0]
		check(t, row.Title == "Raw subject" && !row.TitleIsAssistant, "%d: row %+v", i, row)
		check(t, strings.Contains(row.Spoken, "Raw subject") && !strings.Contains(row.Spoken, "Assistant:"), "%d: spoken %q", i, row.Spoken)
		d := v.Detail
		check(t, !d.TitleIsAssistant && d.SpokenTitle == "Raw subject" && !d.WhyIsAssistant, "%d: detail %+v", i, d)
		check(t, !row.SnippetIsAssistant && !row.MarksAssistant(), "%d: row %+v", i, row)
	}
	// A summary without a title marks the row, not the title.
	row := view(casesOf(markedCase("", "Anna asks", "")), annotatedOn()).Sections[0].Rows[0]
	check(t, !row.TitleIsAssistant && row.SnippetIsAssistant && row.MarksAssistant(), "row %+v", row)
}

// TestStateFromAPI: the four states map both ways; an unknown one reads
// as for reading.
func TestStateFromAPI(t *testing.T) {
	for _, s := range States {
		got, ok := StateFromAPI(s.API())
		check(t, ok && got == s, "%d round trip", s)
	}
	got, ok := StateFromAPI("later")
	check(t, !ok && got == StateInfo, "unknown state %d %v", got, ok)
}
