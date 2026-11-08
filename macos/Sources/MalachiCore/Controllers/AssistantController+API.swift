// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/window/ai_provider.go: the third provider of the assistant
// in the app (docs/plans/ai-api.md §7.4, D14), an AI provider account of
// the daemon (kind `ai`), over which the daemon runs the assistant itself
// (`DaemonAssistantProvider`). The `assistant-provider` key names it by
// its account id; the controller keeps the daemon's `ai` accounts as last
// listed (account.list, again on notify.accountsChanged and on
// connecting: `accountsChanged()`) and builds one provider over the
// chosen one per runtime generation (`rebuildAPI`, from
// `providerChanged()`). An id of an account the daemon no longer has
// reads as Claude (D14) once the list is known; while it is not, the
// account counts as the provider, unavailable, so that nothing runs
// Claude Code meanwhile.
//
// The account behaves like ChatGPT for the rest of the controller: no
// registration of the bridge in the Claude apps is needed (the daemon
// starts the bridge itself, so only the bundled bridge must exist), the
// consent is the account's (`AIConfig.consent`, given in the account's
// dialog and taken again here when the text's version moved), and "not
// found" means the account is switched off, "not signed in" that the
// provider refused its key (the account's state authRequired). The
// application wires the rest: it feeds `apiSessions` from its
// notification hub, calls `accountsChanged()` and `apiDisconnected()`
// with the connection, and returns `apiProviderOrUnavailable()` as the
// selected provider while `usesAPI`.

import Foundation
import os

extension AssistantController {
    private static let log = Logger(subsystem: "io.github.schotek.Malachi", category: "assistant")

    /// How long `waitAPISessions` blocks at most (a sessionStop answers in
    /// milliseconds; a daemon that does not answer is not waited for).
    public static let apiSessionStopWait: Duration = .seconds(2)

    // MARK: The source in effect

    /// Which provider the assistant runs on now (`Assistant.sourceOf` with
    /// the account rule: an account the daemon no longer lists reads as
    /// Claude once the list is known).
    public var source: Assistant.ProviderSource {
        let src = Assistant.sourceOf(settings.assistantProvider)
        if src == .account, aiAccountsKnown, apiAccount == nil {
            return .claude
        }
        return src
    }

    /// Whether an AI provider account is the provider in effect.
    public var usesAPI: Bool { source == .account }

    /// The account the `assistant-provider` key names, as last listed; nil
    /// while there is none (another provider, or an account the daemon no
    /// longer has).
    public var apiAccount: Account? {
        guard let id = Assistant.accountOf(settings.assistantProvider) else { return nil }
        return aiAccounts.first { $0.id == id }
    }

    /// Whether the chosen account can run now: it exists, is switched on
    /// and the provider has not refused its key. Its consent is asked when
    /// a request runs, as Claude Code's.
    public var apiReady: Bool {
        guard let acc = apiAccount else { return false }
        return acc.enabled && acc.state.status != .authRequired
    }

    /// Why the chosen account cannot run the message actions; nil when it
    /// can (Go `apiProblem` returns ""). The words are the board's too.
    public var apiProblem: String? {
        let x = Assistant.apiText()
        guard daemonBridge != nil else { return Assistant.panelTexts().toolsMissing }
        guard let acc = apiAccount else { return x.accountMissing }
        if !acc.enabled { return x.boardAccountOff }
        if acc.state.status == .authRequired { return x.boardKeyRefused }
        return nil
    }

    // MARK: The provider over the account

    /// Makes the provider over the chosen account anew (a runtime
    /// generation): one `DaemonAssistantProvider` while an account is
    /// chosen, listed and the client exists, nil otherwise.
    public func rebuildAPI() {
        apiProvider = nil
        guard source == .account, let acc = apiAccount, let client else { return }
        let p = DaemonAssistantProvider(client: client, account: acc, sessions: apiSessions)
        // The daemon starts its own bridge (the worker copy in the App
        // Store build), whatever bridge the panel or a request names.
        p.bridgePath = daemonBridge
        apiProvider = p
    }

    /// The provider over the chosen account, or one whose sessions fail
    /// with "api_unavailable" while there is none (no client yet, the
    /// account not listed yet), named after the account.
    public func apiProviderOrUnavailable() -> any AssistantProvider {
        if let apiProvider { return apiProvider }
        return UnavailableAPIProvider(name: apiBrand)
    }

    /// Names an AI provider account as the Assistant Uses row does
    /// (`Assistant.providerChoices`): its name, else its provider kind.
    public static func aiAccountName(_ a: Account) -> String {
        let choices = Assistant.providerChoices(claude: .init(), chatgpt: .init(), accounts: [a])
        guard choices.count >= 3 else { return Assistant.apiText().accountMissing }
        return choices[2].name
    }

    /// The consent of the chosen account: the provider's (which counts an
    /// accepted one at once and writes it to the daemon), else the
    /// account's as listed.
    public var apiConsent: Bool {
        if let apiProvider { return apiProvider.hasConsent }
        return apiAccount?.config.ai?.consented == true
    }

    /// Records the user's consent on the chosen account (account.update
    /// through the provider).
    public func acceptAPIConsent() {
        apiProvider?.acceptConsent()
    }

    /// The board's consent sentence over account `a` (the board's heading
    /// is `Board.Text.triageConsentHeading`).
    public static func apiBoardConsentBody(_ a: Account) -> String {
        L10n.format(Assistant.apiText().boardConsentBody, [Assistant.destination(a)])
    }

    /// The assistant panel's footer over account `a`.
    public static func apiFooter(_ a: Account) -> String {
        L10n.format(Assistant.apiText().footer, [Assistant.destination(a)])
    }

    /// Names the provider in the compose window's assistant bar and the
    /// panel's subtitle while no provider exists yet: the chosen account's
    /// name, or that the account was removed.
    public var apiBrand: String {
        guard let acc = apiAccount else { return Assistant.apiText().accountMissing }
        return Self.aiAccountName(acc)
    }

    // MARK: The accounts

    /// The daemon's account set changed (notify.accountsChanged, a
    /// connection made): the AI provider accounts are listed again.
    public func accountsChanged() { refreshAIAccounts() }

    /// Lists the accounts again and keeps the `ai` ones. Nothing before
    /// the client exists; a late answer of an older listing is dropped.
    public func refreshAIAccounts() {
        guard let client, !closed else { return }
        aiAccountsGen += 1
        let gen = aiAccountsGen
        Task { [weak self] in
            let res: AccountListResult
            do {
                res = try await client.call(API.AccountList.self, EmptyParams())
            } catch {
                Self.log.debug("account.list for the assistant failed: \(String(describing: error), privacy: .public)")
                return
            }
            guard let self, !self.closed, gen == self.aiAccountsGen else { return }
            self.setAIAccounts(res.accounts)
        }
    }

    /// Keeps the `ai` accounts among `accounts` and tells what changed:
    /// the chosen account's configuration or switch (a runtime generation,
    /// `providerChanged()`: its provider is built anew and a request under
    /// way is discarded, as a model change of ChatGPT is), its state alone
    /// or the other accounts (the observers only: availability, the
    /// choice's entries).
    public func setAIAccounts(_ accounts: [Account]) {
        let ai = accounts.filter { $0.config.protocolKind == .ai }
        let before = apiAccount
        let knownBefore = aiAccountsKnown
        let wasAPI = source == .account
        let same = aiAccountsKnown && ai.count == aiAccounts.count
            && zip(ai, aiAccounts).allSatisfy { Self.sameAIAccount($0, $1) }
        aiAccounts = ai
        aiAccountsKnown = true
        if same { return }
        let after = apiAccount
        let configChanged: Bool
        if let before, let after {
            configChanged = !Self.sameAIAccountConfig(before, after)
        } else {
            configChanged = false
        }
        if (!knownBefore && Assistant.isAccountProvider(settings.assistantProvider))
            || (before != nil) != (after != nil)
            || wasAPI != (source == .account)
            || configChanged
        {
            providerChanged()
        } else {
            notify()
        }
    }

    /// Whether two listings of an account agree in what the controller
    /// looks at: the configuration, the switch and the state.
    public static func sameAIAccount(_ x: Account, _ y: Account) -> Bool {
        sameAIAccountConfig(x, y) && x.state.status == y.state.status
    }

    /// Whether two listings of an account agree in its id, name, switch
    /// and AI configuration (the consent's version included, when it was
    /// accepted not).
    public static func sameAIAccountConfig(_ x: Account, _ y: Account) -> Bool {
        if x.id != y.id || x.enabled != y.enabled || x.config.name != y.config.name {
            return false
        }
        switch (x.config.ai, y.config.ai) {
        case (nil, nil):
            return true
        case (nil, _), (_, nil):
            return false
        case (let p?, let q?):
            if (p.consent == nil) != (q.consent == nil) || p.consent?.version != q.consent?.version {
                return false
            }
            var p = p, q = q
            p.consent = nil
            q.consent = nil
            return p == q
        }
    }

    // MARK: The sessions

    /// Lets the sessionStop calls of the daemon's assistant reach the
    /// daemon as the application quits: called after the panel, the
    /// requests and the board ended their sessions and before the
    /// connection closes; waits at most `apiSessionStopWait`.
    public func waitAPISessions() async {
        _ = await apiSessions.wait(Self.apiSessionStopWait)
    }

    /// The connection to the daemon went: every session of the daemon's
    /// assistant with it.
    public func apiDisconnected() {
        apiSessions.disconnected()
    }

    // MARK: The choice and the board

    /// Whether Codex is found and the ChatGPT connection stands (GTK
    /// `chatGPTReady`).
    var chatGPTReady: Bool {
        guard let chatGPT else { return false }
        return chatGPT.available && chatGPT.connected
    }

    /// Why ChatGPT (Codex) cannot run; "" when it can (GTK
    /// `chatGPTProblem`, without the Flatpak case).
    var chatGPTProblem: String {
        guard let chatGPT, chatGPT.available else { return L10n.T("Codex was not found. Choose a native Codex executable.") }
        if !chatGPT.connected { return L10n.T("Reconnect to ChatGPT") }
        if bridge == nil { return Assistant.panelTexts().toolsMissing }
        return ""
    }

    /// The entries of the Assistant Uses row: Claude Code, ChatGPT and
    /// every AI provider account, each with why it cannot be chosen
    /// (`Assistant.providerChoices`).
    public func providerChoices() -> [Assistant.ProviderChoice] {
        var claude = Assistant.ProviderChoiceState(available: true)
        if bridge == nil || locator?.locate() == nil {
            claude = Assistant.ProviderChoiceState(problem: Assistant.problem(.app, Assistant.Availability()))
            if bridge == nil {
                claude.problem = Assistant.panelTexts().toolsMissing
            }
        }
        var chatgpt = Assistant.ProviderChoiceState(available: chatGPTReady)
        if !chatgpt.available {
            chatgpt.problem = chatGPTProblem
        }
        return Assistant.providerChoices(claude: claude, chatgpt: chatgpt, accounts: aiAccounts)
    }

    /// The board run's source as the daemon records it
    /// (`BoardTriageController.source`): Claude Code's, ChatGPT's, or
    /// "api:<provider>" of the account.
    public var triageSource: String {
        switch source {
        case .chatgpt:
            return "malachi-chatgpt"
        case .account:
            return "api:" + (apiAccount?.config.ai?.provider ?? .openaiCompatible).rawValue
        case .claude:
            return Assistant.triageSource
        }
    }
}

/// Stands in for the provider over an account that cannot be used yet (no
/// client, the account not listed yet, or removed): its name is the
/// account's, and every session fails with "api_unavailable" (GTK
/// `unavailableAPI`).
@MainActor
public final class UnavailableAPIProvider: AssistantProvider {
    public let name: String
    public var model: String { "" }
    public var available: Bool { false }
    public var connected: Bool { false }
    public var hasConsent: Bool { false }

    public init(name: String) {
        self.name = name
    }

    public func acceptConsent() {}

    public func start(_ spec: AssistantSessionSpec) async throws -> any AssistantSession {
        throw ChatGPTFailure("api_unavailable")
    }
}
