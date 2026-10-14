// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistantpanel

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/ui/internal/assistant"
)

// The bar's request: the JSON message under the writing's system prompt,
// the answer streamed and cleaned, then done.
func TestWriterRunsAndCleans(t *testing.T) {
	fake := newFakeClaude(t, "true", "", fakeTurn{lines: []string{
		fakeInit, fakeDelta(`„Dobrý`), fakeDelta(` den.“`), fakeText(`„Dobrý den.“`), fakeResult(`„Dobrý den.“\nS pozdravem`, true),
	}})
	h := newRequestHarness(t, fake, true, true)
	w := NewWriter(tr, h.request)
	var states []RewriteState
	w.OnState = func(s RewriteState) { states = append(states, s) }
	w.Start(assistant.WriteReply, "Accept", "Meeting", "", "Can you come?")
	if !w.Running() {
		t.Fatal("writing did not start")
	}
	h.loop.runUntil(t, func() bool { return !w.Running() })
	done := RewriteState{Kind: RewriteDone, Text: "„Dobrý den.“\nS pozdravem"}
	if w.State() != done || states[0] != (RewriteState{Kind: RewriteRunning}) || states[len(states)-1] != done ||
		!slices.Contains(states, RewriteState{Kind: RewriteRunning, Text: "„Dobrý"}) {
		t.Errorf("states %+v", states)
	}
	msg, _ := assistant.WriteMessage(assistant.WriteReply, "Accept", "Meeting", "", "Can you come?")
	if !slices.Equal(fake.prompts(), []string{msg}) {
		t.Errorf("prompts %q", fake.prompts())
	}
	want := assistant.Args(assistant.Options{Model: assistant.Haiku, SystemPrompt: assistant.WriteSystemPrompt()})
	if strings.Join(fake.args(), "\n") != strings.Join(want, "\n") {
		t.Errorf("args %q", fake.args())
	}
}

// Nothing to work from fails at once and sends nothing; an empty answer is
// a failure, never an empty replacement; Cancel goes back to idle.
func TestWriterRefusesAndFails(t *testing.T) {
	fake := newFakeClaude(t, "true", "", fakeTurn{lines: []string{fakeInit, fakeResult(`  \"\"  `, true)}})
	h := newRequestHarness(t, fake, true, true)
	w := NewWriter(tr, h.request)
	w.Start(assistant.WriteNew, "  ", "Subject", "", "")
	if want := "The assistant stopped: assistant: an empty passage"; w.State() != (RewriteState{Kind: RewriteFailed, Text: want}) || fake.starts() != 0 {
		t.Errorf("state %+v", w.State())
	}
	w.Start(assistant.WriteForward, "", "", "", strings.Repeat("a", assistant.MaxPassage))
	if !strings.Contains(w.State().Text, "the passage is too long") || fake.starts() != 0 {
		t.Errorf("state %+v", w.State())
	}
	w.Start(assistant.WriteNew, "Say hello", "", "", "")
	h.loop.runUntil(t, func() bool { return !w.Running() })
	if w.State() != (RewriteState{Kind: RewriteFailed, Text: "The assistant stopped: the answer is empty"}) {
		t.Errorf("state %+v", w.State())
	}
	w.Cancel()
	if w.State() != (RewriteState{}) {
		t.Errorf("state after Cancel %+v", w.State())
	}
}

// Declined consent: idle, nothing sent; cancelled while running: idle, and
// the late answer is dropped.
func TestWriterDeclinedAndCancelled(t *testing.T) {
	asked := newRequestHarness(t, newFakeClaude(t, "true", "", answerTurn("x")), false, true)
	asked.consentAnswer = false
	declined := NewWriter(tr, asked.request)
	declined.Start(assistant.WriteReply, "Hi", "", "", "")
	asked.loop.runUntil(t, func() bool { return !declined.Running() })
	if declined.State() != (RewriteState{}) || asked.consentAsked != 1 || asked.fake.starts() != 0 {
		t.Errorf("state %+v, asked %d", declined.State(), asked.consentAsked)
	}

	fake := newFakeClaude(t, "true", "", fakeTurn{lines: []string{fakeInit, fakeDelta("x")}, shell: "sleep 1; echo '" + fakeResult("late", true) + "'"})
	h := newRequestHarness(t, fake, true, true)
	w := NewWriter(tr, h.request)
	w.Start(assistant.WriteReply, "Hi", "", "", "")
	h.loop.runUntil(t, func() bool { return w.State() == RewriteState{Kind: RewriteRunning, Text: "x"} })
	w.Cancel()
	if w.State() != (RewriteState{}) {
		t.Errorf("state %+v", w.State())
	}
	h.loop.settle(t, 1500*time.Millisecond)
	if w.State() != (RewriteState{}) {
		t.Errorf("late state %+v", w.State())
	}
}
