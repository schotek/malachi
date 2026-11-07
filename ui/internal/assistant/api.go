// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistant

import (
	"net/url"
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The provider of the assistant in the app is the setting
// assistant-provider, a string (docs/plans/ai-api.md D14): ProviderClaude
// for the user's Claude Code, ProviderChatGPT for Codex with the user's
// ChatGPT plan, or the id of an AI provider account of the daemon (kind
// ai, docs/api.md §4.1), over which the daemon runs the assistant itself
// (§4.18). The board's triage uses the same provider (its model apart:
// AIConfig.BoardModel). An empty, unknown or removed value reads as
// Claude (NormalizeProvider, ProviderSource).
//
// This file is the reference of the texts of that choice and of the AI
// provider accounts for the three clients: the names of the provider
// kinds, the Assistant Uses row of Settings → AI (§7.3), and the words
// of the Mac App Store build's dialog (D17), which GTK never shows but
// carries for the macOS client (Windows: windows/parity-exclusions.txt).
// macOS MalachiCore/Assistant/AssistantAPI.swift, Windows
// Malachi.Core/Assistants/Assistant.Api.cs port it.

// The two local providers; an account's id is the third form.
const (
	ProviderClaude  = "claude"
	ProviderChatGPT = "chatgpt"
	// accountPrefix starts every account id of the daemon (api.AccountID,
	// "acc_" + 32 hex digits).
	accountPrefix = "acc_"
)

// ProviderSource is which of the three forms a provider setting is.
type ProviderSource int

// The sources.
const (
	SourceClaude ProviderSource = iota
	SourceChatGPT
	SourceAccount
)

// IsAccountProvider reports a setting that names an AI provider account
// (an id of the daemon).
func IsAccountProvider(id string) bool {
	return strings.HasPrefix(id, accountPrefix) && len(id) > len(accountPrefix)
}

// AccountOf is the account a setting names; "" for the local providers.
func AccountOf(id string) api.AccountID {
	if !IsAccountProvider(id) {
		return ""
	}
	return api.AccountID(id)
}

// NormalizeProvider is the stored form of a provider setting: "chatgpt"
// and an account id pass, anything else is "claude".
func NormalizeProvider(id string) string {
	switch {
	case id == ProviderChatGPT, IsAccountProvider(id):
		return id
	}
	return ProviderClaude
}

// SourceOf is the source of a provider setting.
func SourceOf(id string) ProviderSource {
	switch NormalizeProvider(id) {
	case ProviderChatGPT:
		return SourceChatGPT
	case ProviderClaude:
		return SourceClaude
	}
	return SourceAccount
}

// ProviderChoiceState is what the client knows of a local provider for
// the Assistant Uses row: whether it can run now, and why not (a
// sentence shown with the greyed entry; "" when Available).
type ProviderChoiceState struct {
	Available bool
	Problem   string
}

// ProviderChoice is one entry of the Assistant Uses row: its setting
// value, its name, whether it can be chosen, and why not.
type ProviderChoice struct {
	ID        string
	Name      string
	Available bool
	Problem   string
}

// ProviderChoices are the entries of the Assistant Uses row of Settings →
// AI (docs/plans/ai-api.md §7.3): Claude Code, ChatGPT, then every AI
// provider account in the order of accounts by name (an account without
// a name by its provider). An account that is switched off, that has no
// current consent, or whose key the provider refused is listed with its
// Problem and not available; other accounts are available. Accounts
// that are not AI provider accounts are skipped.
func ProviderChoices(claude, chatgpt ProviderChoiceState, accounts []api.Account, tr Translator) []ProviderChoice {
	x := APIText(tr)
	out := []ProviderChoice{
		{ID: ProviderClaude, Name: x.ClaudeCode, Available: claude.Available, Problem: claude.Problem},
		{ID: ProviderChatGPT, Name: x.ChatGPT, Available: chatgpt.Available, Problem: chatgpt.Problem},
	}
	for _, a := range accounts {
		if a.Config.Protocol() != api.AccountAI {
			continue
		}
		c := ProviderChoice{ID: string(a.ID), Name: strings.TrimSpace(a.Config.Name), Available: true}
		if c.Name == "" {
			c.Name = x.providerName(a)
		}
		switch {
		case !a.Enabled:
			c.Available, c.Problem = false, x.AccountOff
		case a.Config.AI == nil || !a.Config.AI.Consented():
			c.Available, c.Problem = false, x.AccountNoConsent
		case a.State.Status == api.SyncAuthRequired:
			c.Available, c.Problem = false, x.AccountKeyRefused
		}
		out = append(out, c)
	}
	return out
}

// APIStrings are the texts of the provider choice and of the AI provider
// accounts shared by the clients.
type APIStrings struct {
	// The names of the three provider kinds (ProviderName) and of the
	// two local providers in the Assistant Uses row.
	Anthropic, OpenAI, OpenAICompatible string
	ClaudeCode, ChatGPT                 string
	// UsesRow is the Assistant Uses row of Settings → AI, UsesSubtitle
	// its line below.
	UsesRow, UsesSubtitle string
	// Why an account cannot be chosen (ProviderChoice.Problem), and the
	// Assistant Uses row's entry for an account that was removed.
	AccountOff, AccountNoConsent, AccountKeyRefused, AccountMissing string
	// ModelRow is the Model row of Settings → AI and of the board's
	// group; ModelSubtitle its line for an AI provider account (the
	// model is the account's and is saved to it).
	ModelRow, ModelSubtitle, BoardModelSubtitle string
	// StoreDialogBody is the Mac App Store build's dialog (D17), opened
	// by Add… of a local integration: this version cannot run Claude Code
	// or Codex. StoreAddAPI is its button that adds an AI provider
	// account instead (the website button is WebsiteFeatures.Open).
	StoreDialogBody, StoreAddAPI string
	// RegisterInIntegrations says, under Show the Assistant Menu of
	// Settings → AI, that the bridge is registered in Claude under
	// Integrations now (PanelTexts.RegisterFirst named the switch while
	// it was on the AI page).
	RegisterInIntegrations string
	// Footer is the assistant panel's footer over an AI provider account;
	// %s is Destination.
	Footer string
	// BoardConsentBody is the board's consent over an AI provider
	// account (the board's TriageConsentBody speaks of Claude Code); %s is
	// Destination. The heading is board.TriageConsentHeading's.
	BoardConsentBody string
	// What the board's triage and the panel say instead of Claude Code's
	// missing executable and sign-in (providerBoardTranslator in the
	// clients): the account is switched off, the key was refused, and the
	// button that opens Integrations instead of Get Claude Code… / Sign
	// In….
	BoardAccountOff, BoardKeyRefused, SetUpInIntegrations string
	// BoardModelDefault is the first entry of the board's Model row over
	// an AI provider account: the account's model.
	BoardModelDefault string
	// UsageToday is the line of an AI provider account's row in
	// Integrations: %s is the number of tokens the daemon's assistant used
	// through it today (the client groups its digits).
	UsageToday string
}

// APIText translates the texts.
func APIText(tr Translator) APIStrings {
	return APIStrings{
		// TRANSLATORS: the kind of an AI provider account: the Anthropic Messages API with the user's own key.
		Anthropic: tr.T("Anthropic API"),
		// TRANSLATORS: the kind of an AI provider account: the OpenAI API with the user's own key.
		OpenAI: tr.T("OpenAI API"),
		// TRANSLATORS: the kind of an AI provider account: any server with the OpenAI chat completions interface (Ollama, llama.cpp, LM Studio, vLLM, a gateway).
		OpenAICompatible: tr.T("OpenAI-compatible server"),
		ClaudeCode:       "Claude Code",
		ChatGPT:          "ChatGPT (Codex)",
		// TRANSLATORS: row of Settings → AI: which provider the assistant in the app runs on (Claude Code, ChatGPT, or an AI provider account).
		UsesRow:      tr.T("Assistant Uses"),
		UsesSubtitle: tr.T("Claude Code or ChatGPT on this computer, or an AI provider added in Integrations"),
		// TRANSLATORS: why an AI provider account cannot be chosen: its switch in Integrations is off.
		AccountOff: tr.T("Switched off in Integrations"),
		// TRANSLATORS: why an AI provider account cannot be chosen: the user did not allow sending mail to it yet.
		AccountNoConsent: tr.T("Sending mail to it is not allowed yet"),
		// TRANSLATORS: why an AI provider account cannot be chosen: the provider refused its API key.
		AccountKeyRefused: tr.T("The provider refused the API key"),
		// TRANSLATORS: the Assistant Uses row shows this when the chosen AI provider account no longer exists.
		AccountMissing: tr.T("Removed account"),
		ModelRow:       tr.T("Model"),
		// TRANSLATORS: under the Model row of Settings → AI when the assistant runs on an AI provider account.
		ModelSubtitle: tr.T("The model the account uses; saved to the account"),
		// TRANSLATORS: under the Model row of the board's group when the triage runs on an AI provider account.
		BoardModelSubtitle: tr.T("The model of the board's triage; empty uses the account's model"),
		// TRANSLATORS: dialog of Malachi Mail from the Mac App Store, opened by Add… of Claude or ChatGPT in Integrations; malachimail.app is the website and stays as it is.
		StoreDialogBody: tr.T("This version from the App Store cannot run Claude Code or Codex. Use Malachi Mail from malachimail.app, or add an AI provider through its API."),
		// TRANSLATORS: button of that dialog: it opens the catalogue entry that adds an Anthropic API account.
		StoreAddAPI: tr.T("Add Anthropic API…"),
		// TRANSLATORS: under Show the Assistant Menu in Settings → AI while the bridge is not registered in Claude; Integrations is the preferences page.
		RegisterInIntegrations: tr.T("Register with Claude under Integrations first"),
		// TRANSLATORS: footer of the assistant panel; %s is where the mail goes (Anthropic, OpenAI, or the server's host).
		Footer: tr.T("Mail you ask about is sent to %s"),
		// TRANSLATORS: the board's consent over an AI provider account; %s is where the mail goes (Anthropic, OpenAI, or the server's host).
		BoardConsentBody: tr.T("The assistant reads the conversations on the board that need notes, and any other mail and attachments it needs to understand them, and sends their text to %s with your API key. It adds titles, summaries, tasks, deadlines and suggested replies to the board, and a triage you start yourself may also write replies, which stay on the board in Malachi Mail, not in your Drafts folder, until you send them. It cannot send, move or delete mail, and messages may contain instructions from their senders that it is told not to follow. Which accounts it triages, and whether it runs at all, you choose in Settings."),
		// TRANSLATORS: why the assistant cannot run: the chosen AI provider account's switch in Integrations is off.
		BoardAccountOff: tr.T("The AI account is switched off. Turn it on under Integrations in the preferences."),
		// TRANSLATORS: why the assistant cannot run: the provider refused the AI provider account's API key.
		BoardKeyRefused: tr.T("The provider refused the API key. Replace it under Integrations in the preferences."),
		// TRANSLATORS: button that opens the AI provider account's settings in Integrations (instead of Get Claude Code… / Sign In…).
		SetUpInIntegrations: tr.T("Set Up in Integrations…"),
		// TRANSLATORS: entry of the board's Model row in Settings → AI over an AI provider account: no model of its own, the account's.
		BoardModelDefault: tr.T("The account's model"),
		// TRANSLATORS: line of an AI provider account's row in Integrations; %s is a number of tokens with its digits grouped.
		UsageToday: tr.T("%s tokens today"),
	}
}

// Destination names where an AI provider account sends the mail, for the
// panel's footer and the consents: Anthropic, OpenAI, or the host of an
// OpenAI-compatible server (the provider's name when the address has no
// host).
func Destination(tr Translator, a api.Account) string {
	if a.Config.AI == nil {
		return APIText(tr).OpenAICompatible
	}
	switch a.Config.AI.Provider {
	case api.AIAnthropic:
		return "Anthropic"
	case api.AIOpenAI:
		return "OpenAI"
	}
	if u, err := url.Parse(strings.TrimSpace(a.Config.AI.BaseURL)); err == nil && u.Host != "" {
		return u.Host
	}
	return APIText(tr).OpenAICompatible
}

// ProviderName is the name of a provider kind; an unknown one is named
// as the OpenAI-compatible server (the daemon accepts no other).
func ProviderName(tr Translator, p api.AIProvider) string {
	return APIText(tr).name(p)
}

func (x APIStrings) name(p api.AIProvider) string {
	switch p {
	case api.AIAnthropic:
		return x.Anthropic
	case api.AIOpenAI:
		return x.OpenAI
	}
	return x.OpenAICompatible
}

func (x APIStrings) providerName(a api.Account) string {
	if a.Config.AI == nil {
		return x.OpenAICompatible
	}
	return x.name(a.Config.AI.Provider)
}
