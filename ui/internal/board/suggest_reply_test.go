// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"testing"
	"time"
)

// The pure half of the board's Suggest Reply: when the detail offers it and
// what the control shows (macOS BoardSuggestReplyTests.swift; the request's
// command line and message are ui/internal/assistant's).

func suggestCase(state State) Case {
	return Case{
		ID: "c_1", Account: "acc_1", Person: "P", Date: time.Unix(1_790_848_800, 0), Subject: "S", RuleState: state,
		Reply: &ReplyTarget{Message: "m_1", Folder: "f_1"},
	}
}

func TestSuggestReplyOffered(t *testing.T) {
	mail := AccountInfo{ID: "acc_1", Name: "Work", Badge: "IMAP", CanReply: true}
	s := Snapshot{Accounts: []AccountInfo{mail}, Phase: PhaseReady}
	check(t, SuggestReplyOffered(suggestCase(StateYou), s, false), "a mail case")
	check(t, !SuggestReplyOffered(suggestCase(StateYou), s, true), "never with the samples")
	for _, st := range []State{StateHot, StateThem} {
		check(t, SuggestReplyOffered(suggestCase(st), s, false), "state %d", st)
	}
	check(t, !SuggestReplyOffered(suggestCase(StateInfo), s, false), "for reading")
	// The state in effect counts: the user's choice, the annotation's.
	moved := suggestCase(StateInfo)
	moved.UserState = optState(StateYou)
	check(t, SuggestReplyOffered(moved, s, false), "moved to you")
	toInfo := suggestCase(StateYou)
	toInfo.UserState = optState(StateInfo)
	check(t, !SuggestReplyOffered(toInfo, s, false), "moved to info")
	annotated := suggestCase(StateYou)
	annotated.Annotation = &Annotation{State: optState(StateInfo), Title: "t"}
	check(t, !SuggestReplyOffered(annotated, Snapshot{Accounts: []AccountInfo{mail}, Annotated: true}, false), "annotated info")
	check(t, SuggestReplyOffered(annotated, s, false), "annotations off")
	noReply := suggestCase(StateYou)
	noReply.Reply = nil
	check(t, !SuggestReplyOffered(noReply, s, false), "nothing to answer")
	drafted := suggestCase(StateYou)
	drafted.Draft = &DraftLink{ID: "d_1", Text: "x"}
	check(t, !SuggestReplyOffered(drafted, s, false), "a reply already")
	done := suggestCase(StateYou)
	done.SetDone(true)
	check(t, !SuggestReplyOffered(done, s, false), "done")
}

// TestSuggestReplyOfferedByAccount: on Jira the reply is a comment draft,
// offered when the account can comment; an account not listed counts as
// mail unless the case is an issue.
func TestSuggestReplyOfferedByAccount(t *testing.T) {
	issue := suggestCase(StateYou)
	issue.Issue = &IssueInfo{Key: "K-1", Status: "Open"}
	commenting := AccountInfo{ID: "acc_1", Name: "Jira", Badge: "JIRA", CanReply: true}
	mute := AccountInfo{ID: "acc_1", Name: "Jira", Badge: "JIRA"}
	check(t, SuggestReplyOffered(issue, Snapshot{Accounts: []AccountInfo{commenting}}, false), "commenting")
	check(t, !SuggestReplyOffered(issue, Snapshot{Accounts: []AccountInfo{mute}}, false), "mute")
	check(t, !SuggestReplyOffered(issue, Snapshot{}, false), "an unlisted issue tracker")
	check(t, SuggestReplyOffered(suggestCase(StateYou), Snapshot{}, false), "an unlisted mail account")
	check(t, !SuggestReplyOffered(suggestCase(StateYou), Snapshot{Accounts: []AccountInfo{mute}}, false), "a mute mail account")
}

var testWords = PanelWords{
	Stop: "Stop", NotFound: "Claude Code was not found on this computer",
	SignInHint: "Claude Code is not signed in. Sign in under AI in the preferences.",
}

func suggestInputs(f func(*SuggestReplyInputs)) SuggestReplyInputs {
	i := SuggestReplyInputs{Offered: true, Available: true, ClaudeFound: true, Case: "c_1"}
	if f != nil {
		f(&i)
	}
	return i
}

func suggestView(f func(*SuggestReplyInputs)) SuggestReplyView {
	return SuggestReplyViewOf(suggestInputs(f), testWords, tr)
}

func TestSuggestReplyView(t *testing.T) {
	eq(t, "not offered", suggestView(func(i *SuggestReplyInputs) { i.Offered = false }), SuggestReplyView{})
	eq(t, "not available", suggestView(func(i *SuggestReplyInputs) { i.Available = false }), SuggestReplyView{})
	idle := suggestView(nil)
	check(t, idle.Shown && idle.Enabled && !idle.Running && idle.Note == "" && !idle.NoteIsFailure, "idle %+v", idle)
	check(t, idle.Title == "✦ Suggest Reply" && idle.Placeholder == "What should the reply say? (optional)", "idle %+v", idle)
	check(t, idle.Progress == "Writing a suggested reply…" && idle.Stop == "Stop", "idle %+v", idle)

	running := suggestView(func(i *SuggestReplyInputs) { i.State = SuggestReplyState{Kind: SuggestRunning, Case: "c_1"} })
	check(t, running.Running && !running.Enabled && running.Note == "", "running %+v", running)
	elsewhere := suggestView(func(i *SuggestReplyInputs) { i.State = SuggestReplyState{Kind: SuggestRunning, Case: "c_2"} })
	check(t, !elsewhere.Running && !elsewhere.Enabled, "elsewhere %+v", elsewhere)
	eq(t, "elsewhere note", elsewhere.Note, "The assistant is writing a reply for another conversation")

	missing := suggestView(func(i *SuggestReplyInputs) { i.ClaudeFound = false })
	check(t, missing.Shown && !missing.Enabled && missing.Note == testWords.NotFound, "missing %+v", missing)
	out := suggestView(func(i *SuggestReplyInputs) { i.SignedOut = true })
	check(t, out.Shown && !out.Enabled && out.Note == testWords.SignInHint && !out.NoteIsFailure, "signed out %+v", out)

	failed := suggestView(func(i *SuggestReplyInputs) {
		i.State = SuggestReplyState{Kind: SuggestFailed, Case: "c_1", Failure: ReplyTimeout}
	})
	check(t, failed.Enabled && failed.NoteIsFailure && failed.Note == "The suggested reply failed: it took too long.", "failed %+v", failed)
	failedThere := suggestView(func(i *SuggestReplyInputs) {
		i.State = SuggestReplyState{Kind: SuggestFailed, Case: "c_2", Failure: ReplyTimeout}
	})
	check(t, failedThere.Enabled && failedThere.Note == "", "failed there %+v", failedThere)
	// Signed out says so rather than the failure.
	eq(t, "signed out first", suggestView(func(i *SuggestReplyInputs) {
		i.SignedOut = true
		i.State = SuggestReplyState{Kind: SuggestFailed, Case: "c_1", Failure: ReplyNotSignedIn}
	}).Note, testWords.SignInHint)
	check(t, !SuggestReplyState{}.IsRunning() && (SuggestReplyState{Kind: SuggestRunning}).IsRunning(), "IsRunning")
}
