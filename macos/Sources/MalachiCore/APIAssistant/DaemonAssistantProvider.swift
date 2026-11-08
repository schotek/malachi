// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/apiassistant provider.go: the assistant the daemon runs
// itself over an `ai` account (docs/api.md §4.18, docs/plans/ai-api.md D7,
// E2) as a provider of the panel's protocol (`AssistantProvider`): `start`
// is assistant.sessionStart, a session's `submit` assistant.sessionSend,
// `terminate` assistant.sessionStop, and every notify.assistantEvent of
// the session, routed to it by `AssistantSessions`, is turned into the
// `Assistant.Event`s the panel, the one-shot requests, the board's triage
// and the suggested reply already read (what `CodexSession` emits for
// Codex). The daemon builds the prompts, calls the model and runs the
// bridge; nothing here sees a key or a model's text beyond passing the
// events on. It knows no AppKit: the application owns the provider per
// `ai` account and feeds `AssistantSessions` from its notification hub.

import Foundation
import os

/// apiassistant.Provider: the daemon's assistant over one `ai` account.
/// One per account and application; the application makes it from
/// account.list and replaces it when the account changes (its name, model
/// or consent).
@MainActor
public final class DaemonAssistantProvider: AssistantProvider {
    public let client: RPCClient
    /// The account as the provider has it (the consent as last accepted
    /// here).
    public private(set) var account: Account
    /// The registry its sessions register with, which the application
    /// feeds with the daemon's notifications; nil registers nothing.
    public let sessions: AssistantSessions?
    /// The bridge the daemon may start (`Paths.daemonBridge`: in the App
    /// Store build the helper bundle's worker copy, not the `malachi-mcp`
    /// Claude starts), given to assistant.sessionStart instead of the
    /// spec's `bridgePath`, which the panel and the requests fill with the
    /// bridge of the Claude apps; nil passes the spec's.
    public var bridgePath: String?
    /// The account.update of `acceptConsent` on its way (nil when none):
    /// `start` waits for it before it starts a session, so the daemon sees
    /// the consent.
    private var consentWrite: Task<Void, any Error>?
    let log = Logger(subsystem: "io.github.schotek.Malachi", category: "apiassistant")

    /// apiassistant.New: the provider of `account` (kind `ai`) over
    /// `client`; its sessions register with `sessions`.
    public init(client: RPCClient, account: Account, sessions: AssistantSessions?) {
        self.client = client
        self.account = account
        self.sessions = sessions
    }

    /// The account's id.
    public var accountID: AccountID { account.id }

    /// apiassistant.Provider.Name: the account's name for the panel's
    /// subtitle (its address, or its id, when it has none).
    public var name: String {
        if !account.config.name.isEmpty {
            return account.config.name
        }
        if !account.config.email.isEmpty {
            return account.config.email
        }
        return account.id.rawValue
    }

    /// apiassistant.Provider.Model: the account's model (AIConfig.model),
    /// "" without an AIConfig.
    public var model: String { account.config.ai?.model ?? "" }

    /// The account is switched on (Go has no such gate; the panel reads it
    /// as `Assistant.providerChoices` does).
    public var available: Bool { account.enabled }

    /// The provider did not refuse the account's key (its state is not
    /// authRequired).
    public var connected: Bool { account.state.status != .authRequired }

    /// apiassistant.Provider.HasConsent: the user accepted the current
    /// consent text for the account (AIConfig.consented, or
    /// `acceptConsent` here).
    public var hasConsent: Bool { account.config.ai?.consented ?? false }

    /// apiassistant.Provider.AcceptConsent: records the user's consent:
    /// account.update with the config as stored and the consent's version
    /// (the daemon sets acceptedAt; no credentials, so the key stays). The
    /// write goes in the background; the provider counts the consent as
    /// given at once, and `start` waits for the write (a refused one fails
    /// the session with its reason).
    public func acceptConsent() {
        guard var ai = account.config.ai else { return }
        ai.consent = AIConsent(version: API.Limits.assistantConsentVersion)
        var cfg = account.config
        cfg.ai = ai
        account.config = cfg
        let id = account.id
        let client = client
        let log = log
        let write = Task { @MainActor in
            do {
                _ = try await client.call(API.AccountUpdate.self, AccountUpdateParams(accountId: id, config: cfg, credentials: Credentials()))
            } catch {
                log.warning("ai account: consent refused, code \((error as? RPCError)?.code.rawValue ?? 0, privacy: .public)")
                throw error
            }
        }
        consentWrite = write
        // Forget the write once it answered, unless a later one replaced it.
        Task { @MainActor [weak self] in
            _ = try? await write.value
            if let self, self.consentWrite == write {
                self.consentWrite = nil
            }
        }
    }

    /// apiassistant.Provider.awaitConsent: waits for the consent write on
    /// its way, if any.
    private func awaitConsent() async throws {
        guard let write = consentWrite else { return }
        try await write.value
    }

    /// apiassistant.Provider.Open: starts a session of `spec` over the
    /// account (assistant.sessionStart with the purpose, the language, the
    /// bridge's path, the note account, the triage's trigger and run and
    /// the turn's timeout; the input of a one-shot purpose, which the
    /// daemon sends as the first turn at once, so that the session takes
    /// the `submit` of it as done; the panel has none and its first turn
    /// is `submit`'s) and registers it for the daemon's events (the ones of
    /// a turn sent with the start may arrive before the start answers:
    /// `AssistantSessions` keeps them for the id the answer names). An
    /// error is the daemon's (assistantConsentRequired, authRequired,
    /// assistantUnavailable, assistantToolsUnsupported, …) as the reason
    /// `AssistantRequest.providerFailure` reads (`ChatGPTFailure`).
    public func start(_ spec: AssistantSessionSpec) async throws -> any AssistantSession {
        guard hasConsent else { throw ChatGPTFailure(Self.reasonConsentRequired) }
        do {
            try await awaitConsent()
        } catch {
            throw Self.failure(error)
        }
        var params = AssistantSessionStartParams(
            accountId: accountID, purpose: spec.purpose, language: spec.language, bridge: bridgePath ?? spec.bridgePath,
            noteAccountId: spec.noteAccountID, trigger: spec.trigger, runId: spec.runID, input: spec.input)
        if spec.timeout > .zero {
            params.timeoutSeconds = Self.seconds(roundingUp: spec.timeout)
        }
        let res: AssistantSessionStartResult
        do {
            res = try await client.call(API.AssistantSessionStart.self, params)
        } catch {
            throw Self.failure(error)
        }
        guard !res.sessionId.isEmpty else { throw ChatGPTFailure(Self.reasonProtocol) }
        let s = DaemonAssistantSession(provider: self, id: res.sessionId, tools: res.tools, sent: spec.input != nil)
        sessions?.add(s)
        return s
    }

    /// Go's `int((d + time.Second - 1) / time.Second)`: whole seconds,
    /// rounded up.
    static func seconds(roundingUp d: Duration) -> Int {
        let (secs, attos) = d.components
        return Int(secs) + (attos > 0 ? 1 : 0)
    }

    /// The reasons of the provider's own failures, read by
    /// `AssistantRequest.providerFailure` as the daemon's classes are.
    static let reasonConsentRequired = "api_consent_required"
    static let reasonProtocol = "api_protocol"
    static let reasonStopped = "api_stopped"

    /// apiassistant.failure: the reason of a daemon's error: the class the
    /// error code stands for ("api_consent_required", "api_auth", …), the
    /// message of any other `RPCError`, else the error as it is.
    static func failure(_ error: any Error) -> any Error {
        guard let e = error as? RPCError else { return error }
        switch e.code {
        case .assistantConsentRequired:
            return ChatGPTFailure(reasonConsentRequired)
        case .authRequired:
            return ChatGPTFailure(reason(of: .auth))
        case .assistantToolsUnsupported:
            return ChatGPTFailure(reason(of: .toolsUnsupported))
        case .assistantUnavailable:
            return ChatGPTFailure(reason(of: .bridge))
        case .assistantSessionNotFound:
            return ChatGPTFailure(reasonStopped)
        default:
            return ChatGPTFailure(e.message)
        }
    }

    /// apiassistant.reasonOf: the reason of a failure class: "api_" and
    /// the class.
    static func reason(of c: AssistantFailureClass) -> String {
        Assistant.apiFailurePrefix + c.rawValue
    }
}
