// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistantpanel

import (
	"slices"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/assistantpanel/prompttest"
)

// systemPromptOf is the --system-prompt argument the fake was started
// with (it writes one argument per line, and the prompt has lines).
func systemPromptOf(t *testing.T, args []string) string {
	t.Helper()
	joined := strings.Join(args, "\n")
	_, rest, ok := strings.Cut(joined, "\n--system-prompt\n")
	if !ok {
		t.Fatalf("no --system-prompt in %q", args)
	}
	return rest
}

// A new conversation runs with the daemon's prompt of the panel (which
// carries the profile there): asked in the UI's language, without an
// account; the bridge is the app's own.
func TestPanelSystemPromptIsTheDaemons(t *testing.T) {
	fake := newFakeClaude(t, "true", "", answerTurn("ok"))
	h := newHarness(t, fake, true, testBridge)
	if !h.panel.Submit("Ahoj") {
		t.Fatal("question refused")
	}
	h.turn()
	want := prompttest.SystemPrompt(panelParams(nil))
	if got := systemPromptOf(t, fake.args()); !strings.HasPrefix(got, want) {
		t.Errorf("system prompt %q", got)
	}
	if !strings.Contains(strings.Join(fake.args(), "\n"), `"--in-app"`) {
		t.Errorf("the bridge is not the app's: %q", fake.args())
	}
	calls := h.prompter.Calls()
	if len(calls) != 1 || calls[0].Purpose != api.PurposePanel || calls[0].Language != testLanguage || calls[0].AccountID != "" ||
		calls[0].Input == nil || calls[0].Input.Text != "Ahoj" || calls[0].Input.Context != nil {
		t.Errorf("prompter calls %+v", calls)
	}
}

// A proposal the bridge stored is a card, by its id; a failed call, a line
// that is not the bridge's or another tool's result is none.
func TestPanelProposalCard(t *testing.T) {
	fake := newFakeClaude(t, "true", "", fakeTurn{lines: []string{
		fakeInit,
		fakeToolUse("t1", "propose_profile_note"),
		fakeToolResult("t1", "profile note proposal p_1 is pending; the user accepts or dismisses it in Malachi Mail.", false),
		fakeToolUse("t2", "propose_profile_note"),
		fakeToolResult("t2", "profile note proposal p_2 is pending; …", true),
		fakeToolUse("t3", "read_message"),
		fakeToolResult("t3", "profile note proposal p_3 is pending; …", false),
		fakeToolUse("t4", "propose_profile_note"),
		fakeToolResult("t4", "I proposed p_4 for you", false),
		fakeText("Noted."), fakeResult("done", true),
	}})
	h := newHarness(t, fake, true, testBridge)
	if !h.panel.Submit("Pamatuj si, že schůzky chci v pondělí.") {
		t.Fatal("question refused")
	}
	h.turn()
	var ids []string
	for _, c := range h.contents() {
		if c.Kind == ContentProposal {
			ids = append(ids, c.Proposal)
		}
	}
	if !slices.Equal(ids, []string{"p_1"}) {
		t.Fatalf("proposal cards %q", ids)
	}
	if !slices.Contains(h.contents(), activity("Suggesting a note for your profile…", true)) {
		t.Errorf("transcript %+v", h.contents())
	}
}

// A rewrite names the window's account for its profile note; a search
// names none.
func TestOneShotsNameTheNoteAccount(t *testing.T) {
	fake := newFakeClaude(t, "true", "", fakeTurn{lines: []string{fakeInit, fakeResult("Dobrý den.", true)}},
		fakeTurn{lines: []string{fakeInit, fakeResult(`{\"query\":\"is:unread\"}`, true)}})
	h := newRequestHarness(t, fake, true, true)
	w := NewRewriter(tr, h.request)
	w.Account = func() api.AccountID { return "acc_1" }
	w.Start(assistant.Politer, "", "Ahoj.")
	h.loop.runUntil(t, func() bool { return !w.Running() })
	if got := systemPromptOf(t, fake.args()); got != "SYS rewrite cs note:acc_1" {
		t.Errorf("rewrite system prompt %q", got)
	}
	if last, ok := h.prompter.Last(); !ok || last.AccountID != "acc_1" || last.Purpose != api.PurposeRewrite {
		t.Errorf("prompter %+v", last)
	}

	s := NewSearcher(tr, h.request)
	done := false
	s.Convert("nepřečtené", func(SearchOutcome) { done = true })
	h.loop.runUntil(t, func() bool { return done })
	if got := systemPromptOf(t, fake.args()); !strings.HasPrefix(got, "SYS search cs\n") || strings.Contains(got, "note:") {
		t.Errorf("search system prompt %q", got)
	}
	if last, ok := h.prompter.Last(); !ok || last.AccountID != "" || last.Purpose != api.PurposeSearch {
		t.Errorf("prompter %+v", last)
	}
}

// A Prompter that fails (the daemon away) ends the request as stopped
// with its reason, the daemon's message for its own errors; nothing
// starts.
func TestOneShotWithoutThePrompt(t *testing.T) {
	fake := newFakeClaude(t, "true", "", answerTurn("x"))
	h := newRequestHarness(t, fake, true, true)
	h.prompter.Err = api.NewError(api.CodeInvalidArgument, "rewrite.passage is longer than 65536 bytes")
	w := NewRewriter(tr, h.request)
	w.Start(assistant.Fix, "", "text")
	h.loop.runUntil(t, func() bool { return !w.Running() })
	if w.State() != (RewriteState{Kind: RewriteFailed, Text: "The assistant stopped: rewrite.passage is longer than 65536 bytes"}) || fake.starts() != 0 {
		t.Errorf("state %+v, starts %d", w.State(), fake.starts())
	}
	// A request without any Prompter: the same, with its own reason.
	h.request.prompter = nil
	w.Start(assistant.Fix, "", "text")
	h.loop.runUntil(t, func() bool { return !w.Running() })
	if w.State() != (RewriteState{Kind: RewriteFailed, Text: "The assistant stopped: " + errNoPrompter.Error()}) || fake.starts() != 0 {
		t.Errorf("state %+v", w.State())
	}
}
