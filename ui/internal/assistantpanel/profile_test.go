// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistantpanel

import (
	"slices"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistant"
)

func fxProfile() assistant.Profile {
	return assistant.Profile{
		Text: "Píšu stručně.", Gender: api.GenderFeminine,
		Accounts: []assistant.AccountNote{
			{AccountID: "acc_1", Name: "Studio", Email: "s@example.org", Note: "Podepisuj jako studio."},
			{AccountID: "acc_2", Name: "Osobní", Email: "me@example.org", Note: "Tykej."},
		},
	}
}

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

// A new conversation carries the profile with every account's note; the
// bridge is the app's own.
func TestPanelSystemPromptCarriesTheProfile(t *testing.T) {
	fake := newFakeClaude(t, "true", "", answerTurn("ok"))
	h := newHarness(t, fake, true, testBridge)
	h.panel.Profile = fxProfile
	if !h.panel.Submit("Ahoj") {
		t.Fatal("question refused")
	}
	h.turn()
	want := assistant.WithProfile(assistant.SystemPrompt("Czech", "2026-09-29"), fxProfile())
	if got := systemPromptOf(t, fake.args()); !strings.HasPrefix(got, want) {
		t.Errorf("system prompt %q", got)
	}
	if !strings.Contains(strings.Join(fake.args(), "\n"), `"--in-app"`) {
		t.Errorf("the bridge is not the app's: %q", fake.args())
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

// A rewrite gets the profile as the window scoped it; a search gets it
// without account notes.
func TestOneShotsCarryTheProfile(t *testing.T) {
	fake := newFakeClaude(t, "true", "", fakeTurn{lines: []string{fakeInit, fakeResult("Dobrý den.", true)}},
		fakeTurn{lines: []string{fakeInit, fakeResult(`{\"query\":\"is:unread\"}`, true)}})
	h := newRequestHarness(t, fake, true, true)
	w := NewRewriter(tr, h.request)
	w.Profile = func() assistant.Profile { return fxProfile().For("acc_1") }
	w.Start(assistant.Politer, "", "Ahoj.")
	h.loop.runUntil(t, func() bool { return !w.Running() })
	got := systemPromptOf(t, fake.args())
	if !strings.HasPrefix(got, assistant.WithProfile(assistant.RewriteSystemPrompt(), fxProfile().For("acc_1"))) ||
		!strings.Contains(got, "Podepisuj jako studio.") || strings.Contains(got, "Tykej.") {
		t.Errorf("rewrite system prompt %q", got)
	}

	s := NewSearcher(tr, h.request)
	s.Today = func() string { return "2026-09-29" }
	s.Profile = fxProfile
	done := false
	s.Convert("nepřečtené", func(SearchOutcome) { done = true })
	h.loop.runUntil(t, func() bool { return done })
	got = systemPromptOf(t, fake.args())
	if !strings.Contains(got, "Píšu stručně.") || strings.Contains(got, "For the account") {
		t.Errorf("search system prompt %q", got)
	}
}
