// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// An AI provider account (the daemon's own assistant, docs/api.md §4.18)
// is not mail: list_accounts leaves it out.
func TestListAccountsSkipsAIAccounts(t *testing.T) {
	fb := newFixture()
	fb.accounts = append(fb.accounts, api.Account{
		ID:           "ai_1",
		Config:       api.AccountConfig{Name: "Ollama at home", Kind: api.AccountAI, AI: &api.AIConfig{Provider: api.AIOpenAICompatible, BaseURL: "http://192.168.1.20:11434/v1", Model: "llama3.3"}},
		Enabled:      true,
		State:        api.SyncState{AccountID: "ai_1", Status: api.SyncIdle, Progress: -1},
		Capabilities: []api.AccountCapability{api.CapabilityAssistant},
	})
	h := newHarness(t, fb, false, false)
	out := h.ok(t, "list_accounts", nil)
	mustContain(t, out, `"id": "`+string(fxAccount)+`"`)
	mustNotContain(t, out, "ai_1", "Ollama at home", "llama3.3", "11434", "assistant")
}
