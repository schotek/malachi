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
