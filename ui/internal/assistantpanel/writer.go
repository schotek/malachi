// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistantpanel

import "github.com/schotek/malachi/ui/internal/assistant"

// Writer is the compose window's assistant bar without its widgets (macOS
// ComposeWindowController writeWithAssistant): the optional instruction,
// the subject, the user's own text and the prepared original go to the
// user's assistant (assistant.WriteMessage under
// assistant.WriteSystemPrompt, one Request), and the answer, cleaned
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

// Start asks for the body of a window of mode. A request that cannot be
// made (nothing to work from, too long; assistant.WriteMessage) fails at
// once with its reason; nothing is sent then.
func (w *Writer) Start(mode assistant.WriteMode, instruction, subject, own, quote string) {
	message, err := assistant.WriteMessage(mode, instruction, subject, own, quote)
	if err != nil {
		w.request.Cancel()
		w.set(RewriteState{Kind: RewriteFailed, Text: assistant.StoppedText(w.tr, err.Error())})
		return
	}
	w.set(RewriteState{Kind: RewriteRunning})
	w.request.Start(assistant.WriteSystemPrompt(), message, "", func(text string) {
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
