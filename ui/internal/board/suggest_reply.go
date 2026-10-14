// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The rules and the view model of the case detail's Suggest Reply control
// (the reply controller runs the request): when it is offered, when it can
// run, and what it shows. Pure; the detail only shows the view model. The
// macOS client leads (MalachiCore/Board/BoardSuggestReply.swift); this is
// its port. The texts are in reply.go.

// SuggestReplyStateKind is what the application's one suggested reply is
// doing.
type SuggestReplyStateKind int

// The kinds.
const (
	SuggestIdle SuggestReplyStateKind = iota
	// SuggestRunning: a request for SuggestReplyState.Case runs (asking,
	// writing, linking).
	SuggestRunning
	// SuggestFailed: the last request, for SuggestReplyState.Case, failed
	// with SuggestReplyState.Failure; shown on that case until another
	// request starts.
	SuggestFailed
)

// SuggestReplyState is what the application's one suggested reply is doing
// (Board.SuggestReplyState). The zero value is idle.
type SuggestReplyState struct {
	Kind SuggestReplyStateKind
	// Case is the case of a running or failed request.
	Case CaseID
	// Failure counts for SuggestFailed only.
	Failure SuggestReplyFailure
}

// IsRunning reports a request under way.
func (s SuggestReplyState) IsRunning() bool { return s.Kind == SuggestRunning }

// SuggestReplyOffered reports whether the detail of c offers Suggest
// Reply: a real case (not the samples) with a message a reply answers, no
// suggested reply yet, not for reading only (its state in effect is not
// info), not done, in an account that can reply (a mail account's reply,
// an issue tracker's comment draft). An account not listed (yet) counts as
// a mail account unless the case is an issue.
func SuggestReplyOffered(c Case, s Snapshot, samples bool) bool {
	if samples || c.Reply == nil || c.Draft != nil || c.Visibility.IsDone() || StateOf(c, s.Annotated) == StateInfo {
		return false
	}
	for _, a := range s.Accounts {
		if a.ID == c.Account {
			return a.CanReply
		}
	}
	return c.Issue == nil
}

// IsFollowUpReason reports a rule code under which the case's reply target
// is the user's own last message (them.*): a suggested reply there is a
// follow-up, a nudge on that message, not an answer.
func IsFollowUpReason(r api.BoardReason) bool { return strings.HasPrefix(string(r), "them.") }

// IsFollowUp reports a case whose suggested reply is a follow-up: the
// state the client shows for it (StateOf: the user's choice, else the
// assistant's annotation when annotated is true and the annotation is not
// stale, else the rules') is them. The control reads SuggestFollowUp and
// the request tells the assistant it nudges the user's own message.
//
// Ports (Swift, C#) must mirror this: decide by the effective state, not
// by the rule code; a them.replied case the user moved to You is not a
// follow-up, a case kept in Them by the user is.
func IsFollowUp(c Case, annotated bool) bool { return StateOf(c, annotated) == StateThem }

// IsFollowUpWire is IsFollowUp for a case as the daemon sent it.
func IsFollowUpWire(w api.BoardCase, annotated bool) bool {
	return IsFollowUp(convertCase(w), annotated)
}

// SuggestReplyInputs is what SuggestReplyViewOf looks at.
type SuggestReplyInputs struct {
	// Offered is SuggestReplyOffered for the case shown.
	Offered bool
	// Available: the feature can exist, the assistant shown with the In App
	// target (as the compose window's rewrite) and the bridge beside the
	// application.
	Available   bool
	ClaudeFound bool
	// SignedOut: Claude Code said, when last asked, that it is not signed
	// in; not knowing counts as signed in.
	SignedOut bool
	State     SuggestReplyState
	// Case is the case shown.
	Case CaseID
	// FollowUp is IsFollowUp (effective state) for the case shown: the button reads
	// SuggestFollowUp.
	FollowUp bool
}

// PanelWords are the texts the control shares with the assistant panel:
// Stop and NotFound of assistant.PanelTexts and Hint of
// assistant.SignInTexts. The caller passes them, so that this package does
// not import ui/internal/assistant (which may come to import it).
type PanelWords struct {
	Stop, NotFound, SignInHint string
}

// SuggestReplyView is the control in the place of the Suggested Reply
// block. Its strings are fixed texts; nothing from mail or from the model
// is in it.
type SuggestReplyView struct {
	// Shown: the control is there at all.
	Shown bool
	// Enabled: the field and the button take input.
	Enabled bool
	// Running: the request runs for this case; the spinner, Progress and
	// Stop instead of the button.
	Running bool
	// Note is the line under the field: why it is disabled, or how the last
	// request for this case failed; "" for none.
	Note string
	// NoteIsFailure: Note is a failure (shown as an error line).
	NoteIsFailure bool
	Title         string
	Placeholder   string
	Progress      string
	Stop          string
}

// SuggestReplyViewOf is the control for i: hidden unless offered and
// available; while the request runs for this case its progress and Stop;
// disabled, with the reason, while it runs for another case, without
// Claude Code, or signed out (the rewrite's hint pointing to the AI
// preferences); the last failure for this case under the usable control.
func SuggestReplyViewOf(i SuggestReplyInputs, words PanelWords, tr Translator) SuggestReplyView {
	if !i.Offered || !i.Available {
		return SuggestReplyView{}
	}
	v := SuggestReplyView{
		Shown: true, Enabled: true, Title: SuggestReplyTitle(i.FollowUp, tr), Placeholder: SuggestReplyPlaceholder(tr),
		Progress: SuggestReplyRunning(tr), Stop: words.Stop,
	}
	if i.State.Kind == SuggestRunning {
		v.Enabled = false
		if i.State.Case == i.Case {
			v.Running = true
		} else {
			v.Note = SuggestReplyElsewhere(tr)
		}
		return v
	}
	switch {
	case !i.ClaudeFound:
		v.Enabled = false
		v.Note = words.NotFound
	case i.SignedOut:
		v.Enabled = false
		v.Note = words.SignInHint
	case i.State.Kind == SuggestFailed && i.State.Case == i.Case:
		v.Note = SuggestReplyFailed(i.State.Failure, tr)
		v.NoteIsFailure = true
	}
	return v
}
