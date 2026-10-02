// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"strings"
	"testing"
	"time"
)

// plain returns the msgid (the singular for n == 1), as the English UI.
type plain struct{}

func (plain) T(msgid string) string { return msgid }
func (plain) N(msgid, plural string, n int) string {
	if n == 1 {
		return msgid
	}
	return plural
}
func (plain) C(_, msgid string) string { return msgid }

var tr plain

// forms is a translator with three plural forms, as Czech (1, 2–4, 5+):
// it shows which form was picked and keeps the directive.
type forms struct{}

func (forms) T(msgid string) string { return msgid }
func (forms) N(msgid, _ string, n int) string {
	switch {
	case n == 1:
		return "one:" + msgid
	case n >= 2 && n <= 4:
		return "few:" + msgid
	}
	return "many:" + msgid
}
func (forms) C(ctx, msgid string) string { return ctx + "|" + msgid }

// reasonCodes are the rule codes of docs/api.md §4.13.
var reasonCodes = []string{
	"hot.important", "hot.flagged", "you.addressed", "you.repliedToYou", "them.replied", "them.asked",
	"info.ccOnly", "info.notAddressed", "info.yourNote", "info.unknownSender", "jira.yourComment",
	"jira.assigned", "jira.reporter", "jira.commented", "jira.watching", "kept",
}

func TestCounts(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{CaseCount(1, tr), "1 case"},
		{CaseCount(23, tr), "23 cases"},
		{CaseCount(0, tr), "0 cases"},
		{MessageCount(1, tr), "1 message"},
		{MessageCount(3, tr), "3 messages"},
		{Conversation(3, tr), "Conversation · 3 messages"},
		{TodoPhrase(0, tr), "Nothing needs you today."},
		{TodoPhrase(-1, tr), "Nothing needs you today."},
		{TodoPhrase(1, tr), "1 thing needs you today."},
		{TodoPhrase(4, tr), "4 things need you today."},
		{AndMore(3, tr), "and 3 more"},
		{PromiseCount(1, tr), "1 promise"},
		{PromiseCount(3, tr), "3 promises"},
		{Archived(1, false, tr), "Archived 1 message."},
		{Archived(3, false, tr), "Archived 3 messages."},
		{Archived(0, false, tr), "Marked as done. No message was in the inbox."},
		{Archived(5, true, tr), "Marked as done. This account has no archive."},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

// TestPluralForms checks that every counted text asks for the form of
// its own number, so a three-form language reads naturally.
func TestPluralForms(t *testing.T) {
	var f forms
	for _, c := range []struct{ got, want string }{
		{CaseCount(1, f), "one:1 case"},
		{CaseCount(3, f), "few:3 case"},
		{CaseCount(12, f), "many:12 case"},
		{TriageInterval(120, f), "few:2 hour"},
		{TriageInterval(45, f), "many:45 minute"},
		{RelativeTime(time.Unix(0, 0), time.Unix(3*3600, 0), f), "few:3 hour ago"},
		{RelativeFuture(time.Unix(5*86400, 0), time.Unix(0, 0), f), "many:in 5 day"},
		{Archived(2, false, f), "few:Archived 2 message."},
		{TriageFinished(1, 0, f), "one:Triage finished: 1 conversation refined."},
		{TriageWaiting(1, f), "one:1 conversation waits for the assistant"},
		{TriageWaiting(3, f), "few:3 conversation waits for the assistant"},
		{TriageWaiting(12, f), "many:12 conversation waits for the assistant"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
	if got := AllAccounts(f); got != "account filter|All Accounts" {
		t.Errorf("AllAccounts = %q", got)
	}
	if got := TriageDailyCap(0, f); got != "daily cap|None" {
		t.Errorf("TriageDailyCap(0) = %q", got)
	}
	if got := TriageUsageNone(f); got != "token usage|None" {
		t.Errorf("TriageUsageNone = %q", got)
	}
	if got := TriageUsageRuns(3, f); got != "few:From 3 triage run" {
		t.Errorf("TriageUsageRuns(3) = %q", got)
	}
}

func TestStates(t *testing.T) {
	want := []string{"Hot", "Waiting for You", "Waiting for Them", "For Your Information"}
	for i, s := range States {
		if got := StateName(s, tr); got != want[i] {
			t.Errorf("StateName(%d) = %q, want %q", s, got, want[i])
		}
		if got := FilterTitle(Filter{Kind: FilterState, State: s}, tr); got != want[i] {
			t.Errorf("FilterTitle(%d) = %q", s, got)
		}
	}
	if FilterTitle(Filter{}, tr) != "Overview" || FilterTitle(Filter{Kind: FilterDone}, tr) != "Done" {
		t.Error("FilterTitle of all or done")
	}
	if ColumnEmpty(StateHot, tr) != "Nothing burning." || ColumnEmpty(StateInfo, tr) != "Empty." {
		t.Error("ColumnEmpty")
	}
	if StateName(State(9), tr) != "" {
		t.Error("an unknown state has a name")
	}
}

func TestStylesAndGroups(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{StyleTitle(StyleList, tr), "List"},
		{StyleTitle(StyleColumns, tr), "Columns"},
		{StyleTitle(StyleToday, tr), "Today"},
		{StyleMenuTitle(StyleList, tr), "As List"},
		{StyleMenuTitle(StyleColumns, tr), "As Columns"},
		{StyleMenuTitle(StyleToday, tr), "Today"},
		{DueGroupTitle(DueOverdue, tr), "Overdue"},
		{DueGroupTitle(DueToday, tr), "Today"},
		{DueGroupTitle(DueTomorrow, tr), "Tomorrow"},
		{DueGroupTitle(DueThisWeek, tr), "Next 7 Days"},
		{DueGroupTitle(DueLater, tr), "Later"},
		{RemindPreset(RemindLaterToday, tr), "Later Today"},
		{RemindPreset(RemindTomorrow, tr), "Tomorrow"},
		{RemindPreset(RemindNextWeek, tr), "Next Week"},
		{Close(tr), "_Close"},
		{Discard(tr), "_Discard"},
		{Quoted("Friday at noon", tr), "“Friday at noon”"},
		{SnoozedUntil("Tomorrow 09:00", tr), "Back on the board Tomorrow 09:00"},
		{SpokenRemind("Tomorrow 09:00", tr), "Back on the board Tomorrow 09:00"},
		{SpokenDue("Tomorrow", tr), "Due Tomorrow"},
		{SpokenAssistant("Lunch on Friday", tr), "Assistant: Lunch on Friday"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

func TestSourceAndStatusLine(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{SourceText(Source{}, "", tr), "The daemon’s rules set the state. The assistant has not looked at this case yet."},
		{SourceText(Source{Kind: SourceAssistantKept}, "", tr), "The daemon’s rules set the state and the assistant kept it."},
		{SourceText(Source{Kind: SourceAssistantKept}, "Sonnet", tr), "The daemon’s rules set the state and the assistant (Sonnet) kept it."},
		{SourceText(Source{Kind: SourceAssistantChanged, From: StateYou}, "", tr), "The assistant refined the state. The rules suggested: Waiting for You."},
		{SourceText(Source{Kind: SourceAssistantChanged, From: StateHot}, "Sonnet", tr), "The assistant (Sonnet) refined the state. The rules suggested: Hot."},
		{SourceText(Source{Kind: SourceUser}, "", tr), "You moved this case yourself."},
		{SourceText(Source{Kind: SourceAssistantOff}, "x", tr), "The daemon’s rules set the state; the assistant is off."},
		{StatusLine(false, "m", "n", tr), "Sorted by the daemon’s rules · assistant off"},
		{StatusLine(true, "", "", tr), "Sorted by rules · refined by the assistant"},
		{StatusLine(true, "Sonnet", "", tr), "Sorted by rules · refined by the assistant (Sonnet)"},
		{StatusLine(true, "", "2 errors", tr), "Sorted by rules · refined by the assistant · 2 errors"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

func TestReasons(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range reasonCodes {
		r := Reason(c, tr)
		if r == ReasonUnknown(tr) || seen[r] {
			t.Errorf("Reason(%q) = %q is not its own", c, r)
		}
		seen[r] = true
	}
	for _, c := range []string{"", "hot", "kept.", "HOT.IMPORTANT"} {
		if Reason(c, tr) != "The daemon’s rules put the case here." {
			t.Errorf("Reason(%q) is not the unknown reason", c)
		}
	}
}

func TestPhases(t *testing.T) {
	if EmptyTitleOf(PhaseReady, tr) != EmptyTitle(tr) || EmptyBodyOf(PhaseReady, tr) != EmptyBody(tr) {
		t.Error("the ready board is not the empty board")
	}
	if EmptyBodyOf(PhaseLoading, tr) != "" {
		t.Error("loading has a body")
	}
	for _, p := range []Phase{PhaseLoading, PhaseReady, PhaseOff} {
		if Notice(p, false, tr) != "" {
			t.Errorf("Notice(%d, false) is not empty", p)
		}
		if Notice(p, true, tr) != "Only the newest 1,000 cases are on the board." {
			t.Errorf("Notice(%d, true)", p)
		}
	}
	if Notice(PhasePreparing, true, tr) != EmptyBodyOf(PhasePreparing, tr) {
		t.Error("preparing's notice is not its body")
	}
	if Notice(PhaseUnsupported, false, tr) != EmptyBodyOf(PhaseUnsupported, tr) {
		t.Error("unsupported's notice is not its body")
	}
	for _, p := range []Phase{PhaseUnavailable, PhaseFailed, PhaseUnsupported} {
		if EmptyTitleOf(p, tr) != "Board Unavailable" {
			t.Errorf("EmptyTitleOf(%d)", p)
		}
	}
}

func TestFailed(t *testing.T) {
	for _, c := range []struct {
		a    Action
		w    Why
		want string
	}{
		{ActionMove, WhyNone, "Moving the case failed."},
		{ActionMove, WhyBackendDown, "Moving the case failed: the mail backend is not running."},
		{ActionCommitment, WhyPromiseGone, "Changing the promise failed: the promise no longer exists."},
		{ActionDone, WhyCaseGone, "Marking the case done failed: the case is no longer on the board."},
		{ActionPreferences, WhyNoBoard, "Changing the board’s settings failed: this mail backend has no board."},
		{ActionDiscardDraft, WhyDraftGone, "Discarding the draft failed: the draft no longer exists."},
		{ActionUnflag, WhyCaseGone, "Removing the star failed: the case is no longer on the board."},
	} {
		if got := Failed(c.a, c.w, tr); got != c.want {
			t.Errorf("Failed(%d, %d) = %q, want %q", c.a, c.w, got, c.want)
		}
	}
	seen := map[string]bool{}
	for _, a := range Actions {
		s := Failed(a, WhyNone, tr)
		if seen[s] || s == " failed." {
			t.Errorf("Failed(%d) = %q is not its own", a, s)
		}
		seen[s] = true
	}
}

func TestTriage(t *testing.T) {
	t0 := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct{ got, want string }{
		{TriageProgress(3, 12, tr), "Triaging… 3 of 12"},
		{TriageProgress(15, 12, tr), "Triaging… 12 of 12"},
		{TriageProgress(3, 0, tr), "Triaging…"},
		{TriageRunningLine(3, 12, tr), "The assistant is triaging the board… 3 of 12"},
		{TriageRunningLine(0, 0, tr), "The assistant is triaging the board…"},
		{TriageFinished(0, 0, tr), "Triage finished. No conversation needed new notes."},
		{TriageFinished(1, 0, tr), "Triage finished: 1 conversation refined."},
		{TriageFinished(4, 1, tr), "Triage finished: 4 conversations refined. The board refused 1 of the assistant’s notes."},
		{TriagedToday(1, tr), "1 conversation triaged automatically today"},
		{TriagedToday(7, tr), "7 conversations triaged automatically today"},
		{TriageWaiting(1, tr), "1 conversation waits for the assistant"},
		{TriageWaiting(40, tr), "40 conversations wait for the assistant"},
		{TriageFailed(FailNotSignedIn, tr), "Triage failed: Claude Code is not signed in."},
		{TriageFailed(FailTimeout, tr), "Triage failed: it took too long."},
		{TriageStatusLine(false, &t0, t0, tr), "Sorted by the daemon’s rules · assistant off"},
		{TriageStatusLine(true, nil, t0, tr), "Triaged by rules · not refined by the assistant yet"},
		{TriageStatusLine(true, &t0, t0.Add(5*time.Minute), tr), "Triaged by rules · refined by the assistant 5 minutes ago"},
		{AutoTriagePaused(Pause{Kind: PauseFailed, Failure: FailNoProgress, Until: t0.Add(time.Hour)}, t0, tr),
			"Automatic triage paused: the assistant added no notes · next try in 1 hour"},
		{AutoTriagePaused(Pause{Kind: PauseSignedOut}, t0, tr), "Automatic triage paused: Claude Code is not signed in"},
		{AutoTriagePaused(Pause{Kind: PauseUnavailable}, t0, tr), "Automatic triage paused: the assistant cannot run"},
		{AutoTriagePaused(Pause{Kind: PauseNoConsent}, t0, tr), "Automatic triage paused: sending mail to the assistant is not allowed"},
		{TriageInterval(15, tr), "15 minutes"},
		{TriageInterval(1, tr), "1 minute"},
		{TriageInterval(60, tr), "1 hour"},
		{TriageInterval(90, tr), "90 minutes"},
		{TriageInterval(180, tr), "3 hours"},
		{TriageDailyCap(60, tr), "Up to 60"},
		{TriageDailyCap(0, tr), "None"},
		{TriageDailyCap(-3, tr), "None"},
		{TriageSettingsUsage(tr), "Tokens in the Last 24 Hours"},
		{TriageUsageNone(tr), "None"},
		{TriageUsageSplit("1,200", "340", "5", "90,000", tr), "Input 1,200 · output 340 · written to cache 5 · read from cache 90,000"},
		{TriageUsageRuns(1, tr), "From 1 triage run"},
		{TriageUsageRuns(3, tr), "From 3 triage runs"},
		{TriageUsageToolTip(tr), "Counts only the triage runs Malachi Mail started, not those of other assistants"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
	seen := map[string]bool{}
	for _, f := range TriageFailures {
		s := TriageFailureText(f, tr)
		if s == "" || seen[s] {
			t.Errorf("TriageFailureText(%d) = %q is not its own", f, s)
		}
		seen[s] = true
	}
}

// TestRelative pins the same bounds as the Swift tests (seconds ago or
// ahead → text).
func TestRelative(t *testing.T) {
	t0 := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	past := map[int]string{
		-30: "just now", 0: "just now", 59: "just now", 60: "1 minute ago", 119: "1 minute ago",
		120: "2 minutes ago", 3599: "59 minutes ago", 3600: "1 hour ago", 7199: "1 hour ago",
		7200: "2 hours ago", 86399: "23 hours ago", 86400: "yesterday", 172_799: "yesterday",
		172_800: "2 days ago", 10 * 86400: "10 days ago",
	}
	for s, want := range past {
		if got := RelativeTime(t0.Add(-time.Duration(s)*time.Second), t0, tr); got != want {
			t.Errorf("RelativeTime(%d s) = %q, want %q", s, got, want)
		}
	}
	future := map[int]string{
		-30: "now", 0: "now", 59: "now", 60: "in 1 minute", 119: "in 1 minute", 120: "in 2 minutes",
		3600: "in 1 hour", 7200: "in 2 hours", 86400: "tomorrow", 172_799: "tomorrow",
		172_800: "in 2 days",
	}
	for s, want := range future {
		if got := RelativeFuture(t0.Add(time.Duration(s)*time.Second), t0, tr); got != want {
			t.Errorf("RelativeFuture(%d s) = %q, want %q", s, got, want)
		}
	}
}

func TestInlineReply(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{DraftNote(tr), "Only here on the board until you send it"},
		{ReplyLoading(tr), "Loading the suggested reply…"},
		{ReplyLoadFailed(tr), "The suggested reply could not be opened."},
		{ReplyRemoved(tr), "The suggested reply was removed elsewhere."},
		{ReplyNotSaved(tr), "This reply could not be saved yet; Malachi Mail keeps trying."},
		{ReplyNotSent("Re: Offer", tr), "Your reply “Re: Offer” was not sent; it is still on the board."},
		{QuitUnsavedHeading(tr), "Quit without saving a reply?"},
		{QuitUnsavedBody(tr), "A reply on the board could not be saved or sent yet. If you quit now, what you typed in it may be lost."},
		{QuitAnyway(tr), "_Quit Anyway"},
		{Unstar(tr), "Unstar"},
		{Failed(ActionUnflag, WhyNone, tr), "Removing the star failed."},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
	// The consent texts no longer send replies to the Drafts folder.
	for _, s := range []string{TriageConsentBody(tr), TriageSettingsConsentSubtitle(tr)} {
		if !strings.Contains(s, "stay on the board") || strings.Contains(s, "reply drafts") {
			t.Errorf("consent text %q", s)
		}
	}
}

func TestSuggestReply(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{SuggestReply(tr), "✦ Suggest Reply"},
		{SuggestReplyPlaceholder(tr), "What should the reply say? (optional)"},
		{SuggestReplyRunning(tr), "Writing a suggested reply…"},
		{SuggestReplyElsewhere(tr), "The assistant is writing a reply for another conversation"},
		{SuggestReplyFailed(ReplyNotFound, tr), "The suggested reply failed: Claude Code was not found."},
		{SuggestReplyFailed(ReplyNotSignedIn, tr), "The suggested reply failed: Claude Code is not signed in."},
		{SuggestReplyFailed(ReplyToolsMissing, tr), "The suggested reply failed: the Malachi Mail tools are not available to the assistant."},
		{SuggestReplyFailed(ReplyTimeout, tr), "The suggested reply failed: it took too long."},
		{SuggestReplyFailed(ReplyCancelled, tr), "The suggested reply failed: it was stopped."},
		{SuggestReplyFailed(ReplyStopped, tr), "The suggested reply failed: the assistant stopped."},
		{SuggestReplyFailed(ReplyBackend, tr), "The suggested reply failed: the mail backend did not answer."},
		{SuggestReplyFailed(ReplyNoDraft, tr), "The suggested reply failed: the assistant wrote no reply."},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
	if len(SuggestReplyFailures) != int(ReplyNoDraft)+1 {
		t.Errorf("SuggestReplyFailures has %d entries", len(SuggestReplyFailures))
	}
}
