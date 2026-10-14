// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"testing"

	"github.com/schotek/malachi/ui/internal/chatgpt"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/settings"
)

func TestBoardProviderGrantDoesNotAuthorizePanel(t *testing.T) {
	s := settings.NewMemory()
	s.SetAssistantProvider("chatgpt")
	s.SetAssistantChatGPTModel("panel-model")
	a := &Assistant{settings: s, chatGPT: chatgpt.NewConnectionService(nil, nil, nil)}
	defer a.chatGPT.Close()
	a.rebuildCodex()
	b := boardProviderSettings{providerSettings{a}}
	b.SetAssistantConsent(true)
	if a.Provider().HasConsent() {
		t.Fatal("Board authorized panel")
	}
	b.SetBoardTriageConsent(true)
	if !b.AssistantConsent() || !a.boardProvider().HasConsent() {
		t.Fatal("Board grant not recognized")
	}
	if a.Provider().HasConsent() || s.AssistantChatGPTConsentVersion() != 0 {
		t.Fatal("Board authorized panel")
	}
	if a.boardProvider().Model() != "" {
		t.Fatal("Board inherited panel model instead of provider default")
	}
	s.SetBoardChatGPTModel("board-model")
	if a.boardProvider().Model() != "board-model" || a.Provider().Model() != "panel-model" {
		t.Fatal("models mixed")
	}
	b.SetBoardTriageConsent(false)
	if a.boardProvider().HasConsent() {
		t.Fatal("withdrawn Board grant remained")
	}
}

func TestBoardMissingRuntimePresentationUsesSelectedProvider(t *testing.T) {
	s := settings.NewMemory()
	a := &Assistant{settings: s}
	words := providerBoardTranslator{a, i18n.Tr}
	legacy := "Claude Code is not signed in"
	if words.T(legacy) != i18n.Tr.T(legacy) {
		t.Fatal("Claude presentation changed")
	}
	s.SetAssistantProvider("chatgpt")
	if words.T(legacy) == i18n.Tr.T(legacy) {
		t.Fatal("ChatGPT prompts Claude sign-in")
	}
}
