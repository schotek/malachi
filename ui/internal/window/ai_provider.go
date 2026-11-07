// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/aiaccount"
	"github.com/schotek/malachi/ui/internal/apiassistant"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/assistantpanel"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The third provider of the assistant in the app (docs/plans/ai-api.md
// §7.4, D14): an AI provider account of the daemon (kind ai), over which
// the daemon runs the assistant itself (ui/internal/apiassistant). The
// assistant-provider key names it by its account id; the Assistant keeps
// the ai accounts of the daemon as last listed (account.list, again on
// notify.accountsChanged and on connecting) and builds one
// apiassistant.Provider over the chosen one per runtime generation. An
// id of an account the daemon no longer has reads as Claude (D14) once
// the list is known; while it is not, the account counts as the
// provider, unavailable, so that nothing runs Claude Code meanwhile.
//
// The account behaves like ChatGPT for the rest of the Assistant: no
// registration of the bridge in the Claude apps is needed (the daemon
// starts the bridge itself, so only the bundled bridge must exist), the
// consent is the account's (AIConfig.Consent, given in the account's
// dialog and taken again here when the text's version moved), and "not
// found" means the account is switched off, "not signed in" that the
// provider refused its key (the account's state authRequired).

// aiSource is which provider the Assistant runs on now (assistant.SourceOf
// with the account rule above).
func (a *Assistant) source() assistant.ProviderSource {
	src := assistant.SourceOf(a.settings.AssistantProvider())
	if src == assistant.SourceAccount && a.aiAccountsKnown {
		if _, ok := a.apiAccount(); !ok {
			return assistant.SourceClaude
		}
	}
	return src
}

// usesAPI says whether an AI provider account is the provider in effect.
func (a *Assistant) usesAPI() bool { return a.source() == assistant.SourceAccount }

// apiAccount is the account the assistant-provider key names, as last
// listed; false while there is none (another provider, or an account the
// daemon no longer has).
func (a *Assistant) apiAccount() (api.Account, bool) {
	id := assistant.AccountOf(a.settings.AssistantProvider())
	if id == "" {
		return api.Account{}, false
	}
	for _, acc := range a.aiAccounts {
		if acc.ID == id {
			return acc, true
		}
	}
	return api.Account{}, false
}

// apiReady says whether the chosen account can run now: it exists, is
// switched on and the provider has not refused its key. Its consent is
// asked when a request runs (EnsureAssistantConsent), as Claude Code's.
func (a *Assistant) apiReady() bool {
	acc, ok := a.apiAccount()
	return ok && acc.Enabled && acc.State.Status != api.SyncAuthRequired
}

// apiProblem says why the chosen account cannot run the message actions;
// "" when it can. The words are the board's too (providerBoardTranslator).
func (a *Assistant) apiProblem() string {
	x := assistant.APIText(tr)
	acc, ok := a.apiAccount()
	switch {
	case a.daemonBridge == "":
		return assistant.PanelTexts(tr).ToolsMissing
	case !ok:
		return x.AccountMissing
	case !acc.Enabled:
		return x.BoardAccountOff
	case acc.State.Status == api.SyncAuthRequired:
		return x.BoardKeyRefused
	}
	return ""
}

// rebuildAPI makes the provider over the chosen account anew (a runtime
// generation): one apiassistant.Provider while an account is chosen and
// the RPC client exists, nil otherwise.
func (a *Assistant) rebuildAPI() {
	a.apiProvider = nil
	if a.source() != assistant.SourceAccount {
		return
	}
	acc, ok := a.apiAccount()
	rpc := a.rpc.Load()
	if !ok || rpc == nil {
		return
	}
	if a.apiSessions == nil {
		a.apiSessions = apiassistant.NewSessions()
	}
	a.apiProvider = apiassistant.New(rpc, glibLoop{}, acc, a.apiSessions, a.log)
}

// apiProviderOrUnavailable is the provider over the chosen account, or
// one whose sessions fail with "api_unavailable" while there is none
// (no RPC client yet, the account not listed yet).
func (a *Assistant) apiProviderOrUnavailable() assistantpanel.Provider {
	if a.apiProvider != nil {
		return a.apiProvider
	}
	return unavailableAPI{name: a.apiBrand()}
}

// aiAccountName names an AI provider account as the Assistant Uses row
// does (assistant.ProviderChoices): its name, else its provider kind.
func aiAccountName(acc api.Account) string {
	choices := assistant.ProviderChoices(assistant.ProviderChoiceState{}, assistant.ProviderChoiceState{}, []api.Account{acc}, tr)
	if len(choices) < 3 {
		return assistant.APIText(tr).AccountMissing
	}
	return choices[2].Name
}

// unavailableAPI stands in for the provider over an account that cannot be
// used yet; its name is the account's.
type unavailableAPI struct{ name string }

func (u unavailableAPI) Name() string   { return u.name }
func (unavailableAPI) Model() string    { return "" }
func (unavailableAPI) HasConsent() bool { return false }
func (unavailableAPI) AcceptConsent()   {}
func (unavailableAPI) Open(context.Context, assistantpanel.SessionSpec) (assistantpanel.Session, error) {
	return nil, errors.New("api_unavailable")
}

// apiConsent is the consent of the chosen account: the provider's (which
// counts an accepted one at once and writes it to the daemon), else the
// account's as listed.
func (a *Assistant) apiConsent() bool {
	if a.apiProvider != nil {
		return a.apiProvider.HasConsent()
	}
	acc, ok := a.apiAccount()
	return ok && acc.Config.AI != nil && acc.Config.AI.Consented()
}

// acceptAPIConsent records the user's consent on the chosen account
// (account.update through the provider).
func (a *Assistant) acceptAPIConsent() {
	if a.apiProvider != nil {
		a.apiProvider.AcceptConsent()
	}
}

// askAPIConsent asks the account's consent (the account dialog's text by
// the provider's kind, ui/internal/aiaccount ConsentTexts) over parent:
// the panel's first request over an account whose consent is not the
// current text's.
func (a *Assistant) askAPIConsent(parent gtk.Widgetter, finish func(bool)) {
	acc, _ := a.apiAccount()
	provider := api.AIOpenAICompatible
	if acc.Config.AI != nil {
		provider = acc.Config.AI.Provider
	}
	c := aiaccount.ConsentTexts(tr, provider)
	widget.AskProviderConsent(parent, c.Heading, c.Body(), finish)
}

// askAPIBoardConsent asks the board's consent over an AI provider
// account: the board's heading with the sentence naming where the mail
// goes.
func (a *Assistant) askAPIBoardConsent(parent gtk.Widgetter, finish func(bool)) {
	acc, _ := a.apiAccount()
	widget.AskProviderConsent(parent, board.TriageConsentHeading(i18n.Tr), apiBoardConsentBody(acc), finish)
}

// apiBoardConsentBody is the board's consent sentence over account acc.
func apiBoardConsentBody(acc api.Account) string {
	return fmt.Sprintf(assistant.APIText(tr).BoardConsentBody, assistant.Destination(tr, acc))
}

// apiFooter is the assistant panel's footer over account acc.
func apiFooter(acc api.Account) string {
	return fmt.Sprintf(assistant.APIText(tr).Footer, assistant.Destination(tr, acc))
}

// apiBrand names the provider in the compose window's assistant bar and
// the panel's subtitle while no provider exists yet: the chosen account's
// name, or that the account was removed.
func (a *Assistant) apiBrand() string {
	acc, ok := a.apiAccount()
	if !ok {
		return assistant.APIText(tr).AccountMissing
	}
	return aiAccountName(acc)
}

// AIAccounts are the daemon's AI provider accounts as last listed, in the
// daemon's order (the Assistant Uses row, the Integrations page).
func (a *Assistant) AIAccounts() []api.Account { return slices.Clone(a.aiAccounts) }

// AccountsChanged: the daemon's account set changed (notify.accountsChanged,
// a connection made): the AI provider accounts are listed again.
func (a *Assistant) AccountsChanged() { a.refreshAIAccounts() }

// refreshAIAccounts lists the accounts again and keeps the ai ones.
// Nothing before the RPC client exists.
func (a *Assistant) refreshAIAccounts() {
	rpc := a.rpc.Load()
	if rpc == nil {
		return
	}
	a.aiAccountsGen++
	gen := a.aiAccountsGen
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		var res api.AccountListResult
		err := rpc.Call(ctx, api.MethodAccountList, api.AccountListParams{}, &res)
		glib.IdleAdd(func() {
			if gen != a.aiAccountsGen {
				return
			}
			if err != nil {
				a.log.Debug("account.list for the assistant", "err", err)
				return
			}
			a.setAIAccounts(res.Accounts)
		})
	}()
}

// setAIAccounts keeps the ai accounts among accounts and tells what
// changed: the chosen account's configuration or switch (a runtime
// generation: its provider is built anew and a request under way is
// discarded, as a model change of ChatGPT is), its state alone or the
// other accounts (the observers only: availability, the choice's
// entries).
func (a *Assistant) setAIAccounts(accounts []api.Account) {
	var ai []api.Account
	for _, acc := range accounts {
		if acc.Config.Protocol() == api.AccountAI {
			ai = append(ai, acc)
		}
	}
	before, hadBefore := a.apiAccount()
	knownBefore := a.aiAccountsKnown
	wasAPI := a.source() == assistant.SourceAccount
	same := a.aiAccountsKnown && slices.EqualFunc(ai, a.aiAccounts, sameAIAccount)
	a.aiAccounts = ai
	a.aiAccountsKnown = true
	if same {
		return
	}
	after, hasAfter := a.apiAccount()
	switch {
	case !knownBefore && assistant.IsAccountProvider(a.settings.AssistantProvider()),
		hadBefore != hasAfter, wasAPI != (a.source() == assistant.SourceAccount),
		hasAfter && !sameAIAccountConfig(before, after):
		a.runtimeChanged()
	default:
		a.notify()
	}
}

// sameAIAccount says whether two listings of an account agree in what the
// Assistant looks at: the configuration, the switch and the state.
func sameAIAccount(x, y api.Account) bool {
	return sameAIAccountConfig(x, y) && x.State.Status == y.State.Status
}

// sameAIAccountConfig says whether two listings of an account agree in
// its id, name, switch and AI configuration (the consent included).
func sameAIAccountConfig(x, y api.Account) bool {
	if x.ID != y.ID || x.Enabled != y.Enabled || x.Config.Name != y.Config.Name {
		return false
	}
	if (x.Config.AI == nil) != (y.Config.AI == nil) {
		return false
	}
	if x.Config.AI == nil {
		return true
	}
	p, q := *x.Config.AI, *y.Config.AI
	if (p.Consent == nil) != (q.Consent == nil) || (p.Consent != nil && p.Consent.Version != q.Consent.Version) {
		return false
	}
	p.Consent, q.Consent = nil, nil
	return p == q
}

// disconnected: the connection to the daemon went: every session of the
// daemon's assistant with it.
func (a *Assistant) disconnected() {
	if a.apiSessions != nil {
		a.apiSessions.Disconnected()
	}
}

// providerChoices are the entries of the Assistant Uses row: Claude Code,
// ChatGPT and every AI provider account, each with why it cannot be chosen
// (assistant.ProviderChoices).
func (a *Assistant) providerChoices() []assistant.ProviderChoice {
	claude := assistant.ProviderChoiceState{Available: true}
	if a.bridge == "" || a.locator == nil || a.locator.Locate() == "" {
		claude = assistant.ProviderChoiceState{Problem: assistant.Problem(tr, assistant.App, assistant.Availability{})}
		if a.bridge == "" {
			claude.Problem = assistant.PanelTexts(tr).ToolsMissing
		}
	}
	chatgpt := assistant.ProviderChoiceState{Available: a.chatGPTReady()}
	if !chatgpt.Available {
		chatgpt.Problem = a.chatGPTProblem()
	}
	return assistant.ProviderChoices(claude, chatgpt, a.aiAccounts, i18n.Tr)
}

// triageSource is the board run's source as the daemon records it: the
// Claude Code one, ChatGPT's, or "api:<provider>" of the account.
func (a *Assistant) triageSource() string {
	switch a.source() {
	case assistant.SourceChatGPT:
		return "malachi-chatgpt"
	case assistant.SourceAccount:
		acc, _ := a.apiAccount()
		if acc.Config.AI != nil {
			return "api:" + string(acc.Config.AI.Provider)
		}
		return "api:" + string(api.AIOpenAICompatible)
	}
	return assistant.TriageSource
}
