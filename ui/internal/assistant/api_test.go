// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistant

import (
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestProviderIDs(t *testing.T) {
	for _, c := range []struct {
		id      string
		account api.AccountID
		norm    string
		source  ProviderSource
	}{
		{"", "", "claude", SourceClaude},
		{"claude", "", "claude", SourceClaude},
		{"chatgpt", "", "chatgpt", SourceChatGPT},
		{"acc_0123456789abcdef0123456789abcdef", "acc_0123456789abcdef0123456789abcdef", "acc_0123456789abcdef0123456789abcdef", SourceAccount},
		{"acc_", "", "claude", SourceClaude},
		{"account", "", "claude", SourceClaude},
		{"Claude", "", "claude", SourceClaude},
	} {
		if got := IsAccountProvider(c.id); got != (c.account != "") {
			t.Errorf("IsAccountProvider(%q) = %v", c.id, got)
		}
		if got := AccountOf(c.id); got != c.account {
			t.Errorf("AccountOf(%q) = %q", c.id, got)
		}
		if got := NormalizeProvider(c.id); got != c.norm {
			t.Errorf("NormalizeProvider(%q) = %q", c.id, got)
		}
		if got := SourceOf(c.id); got != c.source {
			t.Errorf("SourceOf(%q) = %v", c.id, got)
		}
	}
}

func aiAccount(id, name string, p api.AIProvider) api.Account {
	a := api.Account{ID: api.AccountID(id), Enabled: true,
		Config:       api.AccountConfig{Kind: api.AccountAI, Name: name, AI: &api.AIConfig{Provider: p, Model: "m"}},
		Capabilities: []api.AccountCapability{api.CapabilityAssistant}}
	a.Config.AI.Consent = &api.AIConsent{Version: api.AssistantConsentVersion}
	return a
}

func TestProviderChoices(t *testing.T) {
	tr := identity{}
	off := aiAccount("acc_2", "Off", api.AIOpenAI)
	off.Enabled = false
	noConsent := aiAccount("acc_3", "", api.AIOpenAICompatible)
	noConsent.Config.AI.Consent = nil
	old := aiAccount("acc_4", "Old", api.AIAnthropic)
	old.Config.AI.Consent.Version = api.AssistantConsentVersion - 1
	refused := aiAccount("acc_5", "Refused", api.AIAnthropic)
	refused.State.Status = api.SyncAuthRequired
	mail := api.Account{ID: "acc_9", Enabled: true, Config: api.AccountConfig{Name: "Mail", Email: "a@example.invalid"}}
	accounts := []api.Account{mail, aiAccount("acc_1", " Work ", api.AIAnthropic), off, noConsent, old, refused}

	got := ProviderChoices(ProviderChoiceState{Available: true}, ProviderChoiceState{Problem: "Codex not found"}, accounts, tr)
	want := []ProviderChoice{
		{ID: "claude", Name: "Claude Code", Available: true},
		{ID: "chatgpt", Name: "ChatGPT (Codex)", Problem: "Codex not found"},
		{ID: "acc_1", Name: "Work", Available: true},
		{ID: "acc_2", Name: "Off", Problem: "Switched off in Integrations"},
		{ID: "acc_3", Name: "OpenAI-compatible server", Problem: "Sending mail to it is not allowed yet"},
		{ID: "acc_4", Name: "Old", Problem: "Sending mail to it is not allowed yet"},
		{ID: "acc_5", Name: "Refused", Problem: "The provider refused the API key"},
	}
	if len(got) != len(want) {
		t.Fatalf("%d choices, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("choice %d: %+v, want %+v", i, got[i], want[i])
		}
	}
	if only := ProviderChoices(ProviderChoiceState{}, ProviderChoiceState{}, nil, tr); len(only) != 2 || only[0].Available || only[1].Available {
		t.Errorf("without accounts: %+v", only)
	}
}

func TestAPIText(t *testing.T) {
	tr := identity{}
	x := APIText(tr)
	for _, s := range []string{x.Anthropic, x.OpenAI, x.OpenAICompatible, x.ClaudeCode, x.ChatGPT, x.UsesRow, x.UsesSubtitle,
		x.AccountOff, x.AccountNoConsent, x.AccountKeyRefused, x.AccountMissing, x.ModelRow, x.ModelSubtitle, x.BoardModelSubtitle,
		x.StoreDialogBody, x.StoreAddAPI, x.RegisterInIntegrations, x.Footer, x.BoardConsentBody, x.BoardAccountOff,
		x.BoardKeyRefused, x.SetUpInIntegrations, x.UsageToday, x.BoardModelDefault} {
		if s == "" {
			t.Errorf("empty text in %+v", x)
		}
	}
	if x.UsesRow != "Assistant Uses" || x.Anthropic != "Anthropic API" || x.OpenAI != "OpenAI API" || x.OpenAICompatible != "OpenAI-compatible server" {
		t.Errorf("%+v", x)
	}
	if x.StoreDialogBody != "This version from the App Store cannot run Claude Code or Codex. Use Malachi Mail from malachimail.app, or add an AI provider through its API." {
		t.Errorf("store dialog %q", x.StoreDialogBody)
	}
	if x.Footer != "Mail you ask about is sent to %s" || x.UsageToday != "%s tokens today" || x.SetUpInIntegrations != "Set Up in Integrations…" {
		t.Errorf("%+v", x)
	}
	for _, c := range []struct {
		p    api.AIProvider
		name string
	}{
		{api.AIAnthropic, "Anthropic API"}, {api.AIOpenAI, "OpenAI API"},
		{api.AIOpenAICompatible, "OpenAI-compatible server"}, {"", "OpenAI-compatible server"}, {"other", "OpenAI-compatible server"},
	} {
		if got := ProviderName(tr, c.p); got != c.name {
			t.Errorf("ProviderName(%q) = %q, want %q", c.p, got, c.name)
		}
	}
}

// Destination names where an account sends the mail: the provider for
// the named ones, the server's host for a compatible one.
func TestDestination(t *testing.T) {
	tr := identity{}
	for _, c := range []struct {
		cfg  *api.AIConfig
		want string
	}{
		{&api.AIConfig{Provider: api.AIAnthropic}, "Anthropic"},
		{&api.AIConfig{Provider: api.AIOpenAI}, "OpenAI"},
		{&api.AIConfig{Provider: api.AIOpenAICompatible, BaseURL: "http://localhost:11434/v1"}, "localhost:11434"},
		{&api.AIConfig{Provider: api.AIOpenAICompatible, BaseURL: " https://openrouter.ai/api/v1 "}, "openrouter.ai"},
		{&api.AIConfig{Provider: api.AIOpenAICompatible, BaseURL: "not a url"}, "OpenAI-compatible server"},
		{nil, "OpenAI-compatible server"},
	} {
		a := api.Account{Config: api.AccountConfig{Kind: api.AccountAI, AI: c.cfg}}
		if got := Destination(tr, a); got != c.want {
			t.Errorf("Destination(%+v) = %q, want %q", c.cfg, got, c.want)
		}
	}
}
