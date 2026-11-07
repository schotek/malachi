// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/assistantpanel"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// aiTestAccount is an Anthropic API account with the current consent.
func aiTestAccount(id api.AccountID) api.Account {
	return api.Account{
		ID: id, Enabled: true, Capabilities: []api.AccountCapability{api.CapabilityAssistant},
		Config: api.AccountConfig{Name: "Work key", Kind: api.AccountAI, AI: &api.AIConfig{
			Provider: api.AIAnthropic, Model: "claude-sonnet-4-5",
			Consent: &api.AIConsent{Version: api.AssistantConsentVersion},
		}},
	}
}

// An AI provider account chosen in assistant-provider runs the panel
// without the bridge registered in any Claude app: only the bundled
// bridge must exist. Switched off it is "not found", with its key
// refused it is "not signed in", and once the daemon no longer lists it
// the setting reads as Claude (D14).
func TestAssistantAPIAccount(t *testing.T) {
	a := testAssistant()
	a.daemonBridge = a.bridge
	a.settings.SetAssistantTarget(assistant.App)
	a.settings.SetAssistantProvider("acc_0123456789abcdef0123456789abcdef")
	if a.source() != assistant.SourceAccount || !a.usesAPI() {
		t.Fatal("an account id names the account while the list is not known")
	}
	if a.CanRunInApp() || a.problem(assistant.App) == "" {
		t.Error("nothing runs before the accounts are listed")
	}
	acc := aiTestAccount("acc_0123456789abcdef0123456789abcdef")
	a.setAIAccounts([]api.Account{acc, {ID: "m", Enabled: true, Config: api.AccountConfig{Email: "me@example.invalid"}}})
	if got := a.AIAccounts(); len(got) != 1 || got[0].ID != acc.ID {
		t.Fatalf("ai accounts %+v", got)
	}
	a.status = ptr(statusWith(false, false)) // registered nowhere
	if !a.shown() || !a.panelShown() || !a.CanRunInApp() || a.problem(assistant.App) != "" {
		t.Errorf("shown %v panel %v run %v problem %q", a.shown(), a.panelShown(), a.CanRunInApp(), a.problem(assistant.App))
	}
	if target, ok := a.pick(true); target != assistant.App || !ok {
		t.Errorf("pick = (%q, %v)", target, ok)
	}
	if a.triageSource() != "api:anthropic" {
		t.Errorf("triage source %q", a.triageSource())
	}
	if a.ProviderBrand() != "Work key" || a.Provider().Name() != "Work key" {
		t.Errorf("brand %q, provider %q", a.ProviderBrand(), a.Provider().Name())
	}
	if !(providerSettings{a}).AssistantConsent() || !(boardProviderSettings{providerSettings{a}}).AssistantConsent() {
		t.Error("the account's consent is the panel's and the board's")
	}
	words := providerBoardTranslator{a, i18n.Tr}
	x := assistant.APIText(i18n.Tr)
	if words.T("Get Claude Code…") != x.SetUpInIntegrations || words.T("Sign In…") != x.SetUpInIntegrations {
		t.Error("the triage's buttons open Integrations over an account")
	}
	if words.T("Claude Code is not signed in") != x.BoardKeyRefused {
		t.Errorf("not signed in reads %q", words.T("Claude Code is not signed in"))
	}
	if l := (providerLocator{a}); l.Locate() == "" || l.SigningIn() {
		t.Error("the locator finds a switched-on account")
	}

	off := acc
	off.Enabled = false
	a.setAIAccounts([]api.Account{off})
	if a.CanRunInApp() || a.problem(assistant.App) != x.BoardAccountOff || (providerLocator{a}).Locate() != "" {
		t.Errorf("switched off: run %v, problem %q", a.CanRunInApp(), a.problem(assistant.App))
	}
	refused := acc
	refused.State.Status = api.SyncAuthRequired
	a.setAIAccounts([]api.Account{refused})
	if a.CanRunInApp() || a.problem(assistant.App) != x.BoardKeyRefused {
		t.Errorf("key refused: run %v, problem %q", a.CanRunInApp(), a.problem(assistant.App))
	}
	var signedIn *bool
	(providerLocator{a}).SignedIn(func(in assistantpanel.SignIn) { signedIn = &in.SignedIn })
	if signedIn == nil || *signedIn {
		t.Error("a refused key is not signed in")
	}

	a.setAIAccounts(nil)
	if a.source() != assistant.SourceClaude || a.usesAPI() || a.Provider() != nil {
		t.Error("a removed account reads as Claude")
	}
	if a.triageSource() != assistant.TriageSource {
		t.Errorf("triage source %q", a.triageSource())
	}
}

// The listing changes the runtime generation only when the chosen
// account's configuration or switch changed, or the account came or went;
// its state alone, and the other accounts, only notify.
func TestSetAIAccountsGenerations(t *testing.T) {
	a := testAssistant()
	a.settings.SetAssistantProvider("acc_0123456789abcdef0123456789abcdef")
	notified := 0
	a.OnChange(func() { notified++ })
	acc := aiTestAccount("acc_0123456789abcdef0123456789abcdef")
	other := aiTestAccount("acc_ffffffffffffffffffffffffffffffff")

	a.setAIAccounts([]api.Account{acc})
	gen := a.RuntimeGeneration()
	if gen == 0 || notified == 0 {
		t.Fatalf("the first listing of a chosen account is a generation (%d) and a change (%d)", gen, notified)
	}
	a.setAIAccounts([]api.Account{acc})
	if a.RuntimeGeneration() != gen {
		t.Error("the same listing changes nothing")
	}
	n := notified
	syncing := acc
	syncing.State.Status = api.SyncError
	a.setAIAccounts([]api.Account{syncing})
	if a.RuntimeGeneration() != gen || notified == n {
		t.Error("a state change only notifies")
	}
	a.setAIAccounts([]api.Account{syncing, other})
	if a.RuntimeGeneration() != gen {
		t.Error("another account only notifies")
	}
	renamed := syncing
	renamed.Config.Name = "Other key"
	a.setAIAccounts([]api.Account{renamed, other})
	if a.RuntimeGeneration() == gen {
		t.Error("the chosen account's name is a generation")
	}
	gen = a.RuntimeGeneration()
	a.setAIAccounts([]api.Account{other})
	if a.RuntimeGeneration() == gen || a.source() != assistant.SourceClaude {
		t.Error("the chosen account going is a generation")
	}
}

// usageLine groups the digits of the tokens used today.
func TestUsageLine(t *testing.T) {
	if got := usageLine(12345); got != "12 345 tokens today" {
		t.Errorf("usageLine = %q", got)
	}
}
