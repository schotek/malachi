// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The board's fixes of 2026-10-08: reminded cases, new contacts, the
// user's pin, Snoozed, the Today phrase, Undo, the remembered style and
// filter, the keys, Escape and the provider's swapped texts.

func reminded() caseOpt { return func(c *Case) { c.RemindedAt = ago(2) } }

func TestRemindedComesFirstInItsState(t *testing.T) {
	cases := casesOf(
		mk("y1", StateYou, withHours(1)), mk("y2", StateYou, withHours(30), reminded()),
		mk("h1", StateHot, withHours(1)), mk("y3", StateYou, withHours(5)),
	)
	v := view(cases)
	eq(t, "rows", rowsOf(v), []string{"h1", "y2", "y1", "y3"})
	eq(t, "column", idsOf(v.Columns[1].Rows), []string{"y2", "y1", "y3"})
	eq(t, "today", idsOf(v.Today.You), []string{"y2", "y1", "y3"})
	r := v.Columns[1].Rows[0]
	check(t, r.Reminded && slices.Equal(r.Badges, []string{"Reminded"}), "row %+v", r)
	check(t, strings.HasPrefix(r.Spoken, "Waiting for You. Reminded."), "spoken %q", r.Spoken)
	check(t, !v.Columns[1].Rows[1].Reminded && len(v.Columns[1].Rows[1].Badges) == 0, "y1 marked")

	d := view(cases, configured(func(v *ViewState) { v.Selection = "y2" })).Detail
	check(t, d.Reminded && slices.Equal(d.WhyNotes, []string{"A reminder you set has come due."}), "detail %+v", d)
	// A snoozed or done case is not reminded, whatever the field says.
	snoozed := mk("s", StateYou, reminded(), withVisibility(Visibility{Kind: VisibleSnoozed, At: testNow.Add(time.Hour)}))
	check(t, !snoozed.Reminded(), "snoozed reminded")
}

func TestNewContactBadgeAndReason(t *testing.T) {
	c := mk("n1", StateYou, withReason(api.BoardReasonYouNewContact))
	v := view(casesOf(c))
	check(t, v.Sections[0].Rows[0].NewContact && slices.Equal(v.Sections[0].Rows[0].Badges, []string{"New contact"}), "row %+v", v.Sections[0].Rows[0])
	eq(t, "why", v.Detail.Why, "The newest message is addressed to you by someone you have never written to.")
	both := view(casesOf(mk("n2", StateYou, withReason(api.BoardReasonYouNewContact), reminded())))
	eq(t, "both badges", both.Detail.Badges, []string{"Reminded", "New contact"})
	check(t, Reason("info.unknownSender", tr) != Reason("you.newContact", tr), "the same text")
}

func TestTheUsersDecisionKeepsIt(t *testing.T) {
	v := view(casesOf(mk("u1", StateInfo, withUser(StateHot))))
	eq(t, "notes", v.Detail.WhyNotes, []string{"Your decision keeps it on the board."})
	eq(t, "none", len(view(casesOf(mk("r1", StateHot))).Detail.WhyNotes), 0)
}

func TestDetailByline(t *testing.T) {
	d := view(casesOf(mk("c1", StateYou))).Detail
	eq(t, "byline", d.Byline, PersonAndTime(d.Person, d.Time, tr))
}

func TestAccountLabels(t *testing.T) {
	v := view(casesOf(mk("c1", StateYou)))
	var labels []string
	for _, a := range v.Accounts {
		labels = append(labels, a.Label)
	}
	eq(t, "labels", labels, []string{"All Accounts", "Alpha (IMAP)", "Beta (JIRA)"})
}

// TestTodayPhraseCountsOnlyWhatNeedsYouToday: new since yesterday's
// midnight, due today, or back from a reminder; hot and waiting for you
// only.
func TestTodayPhraseCountsOnlyWhatNeedsYouToday(t *testing.T) {
	due := func(at time.Time) caseOpt {
		return withAnnotation(Annotation{Due: &at})
	}
	cases := casesOf(
		mk("new", StateYou, withHours(1)),        // today
		mk("yesterday", StateHot, withHours(35)), // the 14th, 01:00
		mk("old", StateYou, withHours(40)),       // the 13th, 20:00
		mk("old due today", StateYou, withHours(100), due(day(15, 18, 0))),
		mk("old due tomorrow", StateYou, withHours(100), due(day(16, 9, 0))),
		mk("old reminded", StateHot, withHours(100), reminded()),
		mk("them new", StateThem, withHours(1)),
		mk("info new", StateInfo, withHours(1)),
	)
	eq(t, "with notes", view(cases, annotatedOn()).Today.Phrase, "4 things need you today.")
	// Without the assistant its deadlines do not count.
	eq(t, "without", view(cases).Today.Phrase, "3 things need you today.")
	eq(t, "none", view(casesOf(mk("old", StateYou, withHours(100)))).Today.Phrase, "Nothing needs you today.")
}

func TestUndoArchiveCalls(t *testing.T) {
	moved := []api.BoardMoved{
		{MessageID: "m1", FromFolderID: "inbox"}, {MessageID: "m2", FromFolderID: "work"},
		{MessageID: "m3", FromFolderID: "inbox"}, {MessageID: "m1", FromFolderID: "inbox"},
		{MessageID: "", FromFolderID: "inbox"}, {MessageID: "m4", FromFolderID: ""},
	}
	got := UndoArchive(moved, "acc", "c_1")
	eq(t, "moves", got.Moves, []api.MessageMoveParams{
		{AccountID: "acc", MessageIDs: []api.MessageID{"m1", "m3"}, TargetFolderID: "inbox"},
		{AccountID: "acc", MessageIDs: []api.MessageID{"m2"}, TargetFolderID: "work"},
	})
	eq(t, "reopen", got.Reopen, api.BoardSetDoneParams{CaseID: "c_1", Done: false})
	eq(t, "nothing moved", len(UndoArchive(nil, "acc", "c_1").Moves), 0)
}

func TestControllerArchiveOffersUndo(t *testing.T) {
	c, src, _ := defaultController()
	var got []ArchiveOutcome
	c.OnArchived = func(o ArchiveOutcome) { got = append(got, o) }
	c.Archive("c1")
	if len(got) != 1 {
		t.Fatalf("outcomes %v", got)
	}
	check(t, got[0].Case == "c1" && got[0].UndoLabel == "Undo" && got[0].Text != "", "outcome %+v", got[0])
	check(t, caseIn(src.Snapshot(), "c1").Done(), "not done")
	c.UndoArchive(got[0]) // the samples move nothing: only back on the board
	check(t, !caseIn(src.Snapshot(), "c1").Done(), "still done")
	// Without OnArchived the text is a toast.
	c2, _, _ := defaultController()
	var toasts []string
	c2.OnToast = func(s string) { toasts = append(toasts, s) }
	c2.Archive("c1")
	eq(t, "toast", len(toasts), 1)
}

// TestBoardViewLastUsed: Board View's Last Used takes the style used last;
// a chosen one applies until the user picks a style in the run.
func TestBoardViewLastUsed(t *testing.T) {
	def, last := "last", "today"
	o := controllerOptions(nil)
	o.DefaultStyle = func() string { return def }
	o.LastStyle = func() string { return last }
	c := NewController(NewInMemorySource(testSnapshot(sampleCases()...), tr), o)
	c.BoardWillShow()
	eq(t, "last used", c.State().Style, StyleToday)
	// Not picked yet: a new Board View applies at the next show.
	def = "columns"
	c.BoardWillShow()
	eq(t, "changed setting", c.State().Style, StyleColumns)
	c.SetStyle(StyleList)
	def = "today"
	c.BoardWillShow()
	eq(t, "picked", c.State().Style, StyleList)
}

func TestSavedAccountFilter(t *testing.T) {
	src := NewInMemorySource(Snapshot{Cases: sampleCases(), Phase: PhaseLoading}, tr)
	o := controllerOptions(nil)
	o.SavedAccount = func() string { return string(accountB) }
	c := NewController(src, o)
	c.BoardWillShow()
	eq(t, "accounts not known yet", c.State().Account, api.AccountID(""))
	s := src.Snapshot()
	s.Accounts, s.Phase = testAccounts, PhaseReady
	src.Replace(s)
	eq(t, "applied once known", c.State().Account, accountB)
	c.SetAccount("")
	c.BoardWillShow()
	eq(t, "not again", c.State().Account, api.AccountID(""))

	// An account that went away: every account.
	o.SavedAccount = func() string { return "gone" }
	gone := NewController(NewInMemorySource(testSnapshot(sampleCases()...), tr), o)
	gone.BoardWillShow()
	eq(t, "gone", gone.State().Account, api.AccountID(""))
}

func TestBoardKeys(t *testing.T) {
	var runes []rune
	for _, k := range BoardKeys(tr) {
		runes = append(runes, k.Rune)
		check(t, k.Title != "", "%c has no title", k.Rune)
	}
	eq(t, "keys", string(runes), "12edr")
	cases := []struct {
		r                     rune
		primary, other, input bool
		mode                  Mode
		want                  KeyAction
		ok                    bool
	}{
		{'1', true, false, false, ModeBoard, KeyShowMail, true},
		{'2', true, false, true, ModeMail, KeyShowBoard, true},
		{'2', false, false, false, ModeBoard, 0, false},
		// Clients pass the digit of the physical number-row key: on Czech
		// QWERTZ the typed character is + or ě, which never matches.
		{'+', true, false, false, ModeBoard, 0, false},
		{'ě', true, false, false, ModeBoard, 0, false},
		{'e', false, false, false, ModeBoard, KeyArchive, true},
		{'E', false, false, false, ModeBoard, KeyArchive, true},
		{'d', false, false, false, ModeBoard, KeyDone, true},
		{'r', false, false, false, ModeBoard, KeyRemind, true},
		{'e', false, false, true, ModeBoard, 0, false}, // typing
		{'e', false, false, false, ModeMail, 0, false}, // the mail's own key
		{'e', true, false, false, ModeBoard, 0, false}, // Ctrl+E is not Archive
		{'e', false, true, false, ModeBoard, 0, false}, // Shift/Alt+E neither
		{'x', false, false, false, ModeBoard, 0, false},
	}
	for _, c := range cases {
		got, ok := KeyFor(c.r, c.primary, c.other, c.input, c.mode, tr)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("KeyFor(%q, %v, %v, %v, %d) = %d, %v", c.r, c.primary, c.other, c.input, c.mode, got, ok)
		}
	}
}

func TestEscapeInTwoSteps(t *testing.T) {
	cases := []struct {
		editor, popup, panel bool
		want                 EscapeTarget
	}{
		{true, true, true, EscapeClosePopup},
		{false, true, false, EscapeClosePopup},
		{true, false, true, EscapeFocusStatePill},
		{true, false, false, EscapeFocusStatePill},
		{false, false, true, EscapeClosePanel},
		{false, false, false, EscapeNothing},
	}
	for _, c := range cases {
		if got := EscapeFor(c.editor, c.popup, c.panel); got != c.want {
			t.Errorf("EscapeFor(%v, %v, %v) = %d, want %d", c.editor, c.popup, c.panel, got, c.want)
		}
	}
}

func TestFollowUp(t *testing.T) {
	check(t, IsFollowUp(mk("t", StateThem, withReason(api.BoardReasonThemReplied)), false), "them.replied")
	check(t, IsFollowUp(mk("t", StateThem, withReason(api.BoardReasonThemAsked)), false), "them.asked")
	check(t, !IsFollowUp(mk("y", StateYou, withReason(api.BoardReasonYouAddressed)), false), "you.addressed")
	// The state shown decides: a them.replied case the user moved to You is
	// no follow-up; a kept case the user shows as Them is one.
	you, them := StateYou, StateThem
	moved := mk("m", StateThem, withReason(api.BoardReasonThemReplied))
	moved.UserState = &you
	check(t, !IsFollowUp(moved, true), "them.replied moved to you")
	kept := mk("k", StateYou, withReason(api.BoardReasonYouAddressed))
	kept.UserState = &them
	check(t, IsFollowUp(kept, true), "kept as them")
	// An annotation counts only when annotated and not stale.
	ann := mk("a", StateYou, withReason(api.BoardReasonYouAddressed))
	ann.Annotation = &Annotation{State: &them}
	check(t, IsFollowUp(ann, true) && !IsFollowUp(ann, false), "annotation, assistant on/off")
	ann.Annotation.Stale = true
	check(t, !IsFollowUp(ann, true), "stale annotation")
	in := SuggestReplyInputs{Offered: true, Available: true, ClaudeFound: true}
	eq(t, "reply", SuggestReplyViewOf(in, PanelWords{}, tr).Title, "✦ Suggest Reply")
	in.FollowUp = true
	eq(t, "follow-up", SuggestReplyViewOf(in, PanelWords{}, tr).Title, "✦ Suggest Follow-up")
}

// TestProviderSwappedTexts: the msgids a client swaps for another
// provider are msgids of the template this package asks for, by kind.
func TestProviderSwappedTexts(t *testing.T) {
	swapped := ProviderSwappedTexts()
	kinds := map[ProviderSwap]int{}
	for _, k := range swapped {
		kinds[k]++
	}
	eq(t, "kinds", kinds, map[ProviderSwap]int{SwapNotFound: 3, SwapNotSignedIn: 2, SwapConsent: 1})
	used := recorder{}
	exercise(used)
	pot := template(t)
	for msgid := range swapped {
		check(t, used[msgKey{msgid: msgid}], "%q is not asked for", msgid)
		_, ok := pot[msgKey{msgid: msgid}]
		check(t, ok, "%q is not in the template", msgid)
	}
	check(t, swapped[TriageNeedsClaudeCode(tr)] == SwapNotFound, "not found")
	check(t, swapped[TriageSettingsConsentSubtitle(tr)] == SwapConsent, "consent")
}
