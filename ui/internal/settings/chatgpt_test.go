// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package settings

import "testing"

func TestProviderConsentsNeverAuthorizeAnotherService(t *testing.T) {
	s := NewMemory()
	s.SetAssistantConsent(true)
	s.SetBoardTriageConsent(true)
	s.SetAssistantProvider("chatgpt")
	if s.AssistantChatGPTConsentVersion() != 0 || s.BoardChatGPTConsentVersion() != 0 {
		t.Fatal("Claude authorized OpenAI")
	}
	s.SetAssistantChatGPTConsentVersion(1)
	if s.BoardChatGPTConsentVersion() != 0 {
		t.Fatal("panel consent authorized background mail")
	}
	s.SetBoardChatGPTConsentVersion(1)
	s.SetAssistantChatGPTModel("panel-model")
	s.SetBoardChatGPTModel("board-model")
	if s.AssistantChatGPTModel() == s.BoardChatGPTModel() {
		t.Fatal("board model overwrote panel model")
	}
	s.SetAssistantChatGPTConsentVersion(-1)
	if s.AssistantChatGPTConsentVersion() != 0 || !s.AssistantConsent() || !s.BoardTriageConsent() {
		t.Fatal("withdrawing OpenAI affected Claude")
	}
	s.SetAssistantProvider("unknown")
	if s.AssistantProvider() != "chatgpt" {
		t.Fatal("invalid provider accepted")
	}
}

// assistant-provider is a string of three forms (docs/plans/ai-api.md
// D14): claude, chatgpt or the id of an AI provider account.
func TestAssistantProviderForms(t *testing.T) {
	s := NewMemory()
	if s.AssistantProvider() != "claude" {
		t.Fatalf("default %q, want claude", s.AssistantProvider())
	}
	const acc = "acc_0123456789abcdef0123456789abcdef"
	s.SetAssistantProvider(acc)
	if s.AssistantProvider() != acc {
		t.Fatalf("account id not kept: %q", s.AssistantProvider())
	}
	for _, bad := range []string{"", "acc_", "Claude", "codex"} {
		s.SetAssistantProvider(bad)
		if s.AssistantProvider() != acc {
			t.Fatalf("%q accepted: %q", bad, s.AssistantProvider())
		}
	}
	s.SetAssistantProvider("claude")
	if s.AssistantProvider() != "claude" {
		t.Fatalf("claude not kept: %q", s.AssistantProvider())
	}
	// A stored value from before the account form, or an emptied one,
	// reads as claude.
	s.set(KeyAssistantProvider, "")
	if s.AssistantProvider() != "claude" {
		t.Fatalf("empty reads as %q", s.AssistantProvider())
	}
}
