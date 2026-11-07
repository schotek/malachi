// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistantpanel

import (
	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistant"
)

// Writer is the compose window's assistant bar without its widgets (macOS
// ComposeWindowController writeWithAssistant): the optional instruction,
// the subject, the user's own text and the prepared original go to the
// user's assistant (the daemon's prompt of purpose write, api.WriteInput,
// one Request), and the answer, cleaned
// (assistant.CleanRewrite), is previewed in the bar for Replace, which the
// window does as plain text through the editor bridge. It goes through the
// same states as the rewrite (RewriteState): the answer so far while
// running, failures as lines to show (Claude Code not found or not signed
// in, "The assistant stopped: …" otherwise, an empty answer included). A
// new request cancels the one under way; Cancel (the bar's Cancel or
// Discard, the window closed) ends it and goes back to idle.
type Writer struct {
	// OnState: the state changed.
	OnState func(RewriteState)
	// Account is the account the window writes from, whose profile note
	// the daemon appends to the system prompt; nil: none.
	Account func() api.AccountID

	tr      assistant.Translator
	request *Request
	state   RewriteState
}

// NewWriter writes through request.
func NewWriter(tr assistant.Translator, request *Request) *Writer {
	return &Writer{tr: tr, request: request}
}

// Request is the writing's request (its consent hook is the window's).
func (w *Writer) Request() *Request { return w.request }

// State is where the writing is.
func (w *Writer) State() RewriteState { return w.state }

// Running says whether an answer is awaited.
func (w *Writer) Running() bool { return w.state.Kind == RewriteRunning }

func (w *Writer) set(s RewriteState) {
	if s == w.state {
		return
	}
	w.state = s
	if w.OnState != nil {
		w.OnState(s)
	}
}

// Start asks for the body of a window of mode. A request the daemon
// refuses (nothing to work from, too long) fails with its reason.
func (w *Writer) Start(mode assistant.WriteMode, instruction, subject, own, quote string) {
	w.set(RewriteState{Kind: RewriteRunning})
	input := api.PromptInput{Write: &api.WriteInput{Mode: mode.API(), Instruction: instruction, Subject: subject, Own: own, Quote: quote}}
	w.request.Start(api.PurposeWrite, input, accountOf(w.Account), func(text string) {
		if w.Running() {
			w.set(RewriteState{Kind: RewriteRunning, Text: assistant.CleanRewrite(text)})
		}
	}, func(o Outcome) {
		w.set(answerState(w.tr, o))
	})
}

// Cancel ends the writing under way and forgets the answer.
func (w *Writer) Cancel() {
	w.request.Cancel()
	w.set(RewriteState{})
}
