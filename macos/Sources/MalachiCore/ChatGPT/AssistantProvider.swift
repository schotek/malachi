// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/assistantpanel/provider.go `Provider`, `SessionSpec` and
// `Session`: the seam a provider other than the user's Claude Code fits
// into — Codex with the user's ChatGPT plan (`CodexProvider`), and a
// session the daemon runs over an `ai` account (docs/api.md §4.18). Each
// session owns an immutable spec; credentials are never exposed to views.
// The setting that chooses the provider is a string
// (`Settings.assistantProvider`, `Assistant.normalizeProvider`).

import Foundation

/// assistantpanel.SessionSpec: what a session is for, and the
/// instructions and tool authority resolved for it before any input is
/// sent. A provider that runs the model itself (Codex) takes
/// `systemPrompt`, `tools`, `jsonSchema` and the message of each turn from
/// the daemon's prompt (`AssistantPrompter`); the daemon's own session
/// takes `purpose`, `language`, `noteAccountID`, `trigger`, `runID` and
/// `bridgePath` and builds the rest itself.
public struct AssistantSessionSpec: Sendable {
    /// What the session is for; the UI language (BCP 47, "" = English)
    /// the model answers in; the account whose profile note a one-shot
    /// purpose of a compose window appends; the triage's trigger and board
    /// run.
    public var purpose: PromptPurpose = .panel
    public var language: String = ""
    public var noteAccountID: AccountID?
    public var trigger: BoardTrigger?
    public var runID: String = ""
    /// The bundled bridge's absolute path ("" without one).
    public var bridgePath: String = ""
    /// The resolved prompt of the purpose (`PromptResult` without the
    /// message): the system prompt with the profile, the answer's schema
    /// ("" for none), the tools and the bridge's arguments.
    public var systemPrompt: String
    public var jsonSchema: String = ""
    public var tools: AssistantRequest.Tools?
    /// Overrides the provider's model; `timeout` bounds one turn.
    public var modelID: String = ""
    public var timeout: Duration = .seconds(120)
    /// The board's consent applies instead of the panel's.
    public var boardConsent = false
    /// The one turn of a one-shot purpose, given with the start: the
    /// daemon's assistant.sessionStart takes it there and sends it at once
    /// (a one-shot purpose cannot start without it), and its session then
    /// takes the request's `submit` of the same input as already sent. A
    /// provider that takes its turns by `submit` alone (Codex) ignores it.
    /// nil for the panel.
    public var input: PromptInput?

    public init(
        purpose: PromptPurpose = .panel, language: String = "", noteAccountID: AccountID? = nil, trigger: BoardTrigger? = nil,
        runID: String = "", bridgePath: String = "", systemPrompt: String, jsonSchema: String = "",
        tools: AssistantRequest.Tools? = nil, modelID: String = "", timeout: Duration = .seconds(120), boardConsent: Bool = false,
        input: PromptInput? = nil
    ) {
        self.purpose = purpose
        self.language = language
        self.noteAccountID = noteAccountID
        self.trigger = trigger
        self.runID = runID
        self.bridgePath = bridgePath
        self.systemPrompt = systemPrompt
        self.jsonSchema = jsonSchema
        self.tools = tools
        self.modelID = modelID
        self.timeout = timeout
        self.boardConsent = boardConsent
        self.input = input
    }
}

/// assistantpanel.Session: a provider's session, its events normalised to
/// the panel's protocol (`Assistant.Event`). Handlers are called on the
/// main actor.
@MainActor public protocol AssistantSession: AnyObject {
    var running: Bool { get }
    var onEvents: (([Assistant.Event]) -> Void)? { get set }
    var onExit: ((String) -> Void)? { get set }
    /// Sends one turn: the panel's text with its context, or the
    /// structured input of a one-shot purpose.
    func submit(_ input: PromptInput) async throws
    func terminate()
}

/// assistantpanel.Provider: opens isolated sessions. `name` is the
/// provider's name for the panel's subtitle (ChatGPT's `ChatGPTText.name`,
/// an `ai` account's name), `model` the model it runs.
@MainActor public protocol AssistantProvider: AnyObject {
    var name: String { get }
    var model: String { get }
    var available: Bool { get }
    var connected: Bool { get }
    var hasConsent: Bool { get }
    func acceptConsent()
    func start(_ spec: AssistantSessionSpec) async throws -> any AssistantSession
}

public struct ChatGPTFailure: Error, Sendable, CustomStringConvertible, Equatable {
    public let code: String
    public init(_ code: String) { self.code = code }
    public var description: String { code }
}
public struct CodexModel: Sendable, Equatable {
    public var id: String
    public var name: String
    public init(id: String, name: String) { self.id = id; self.name = name }
}

/// Only shared GTK msgids; never expose provider diagnostics or tokens to views.
public enum ChatGPTText {
    public static var name: String { L10n.T("ChatGPT (Codex, experimental)") }
    public static var footer: String { L10n.T("Mail you ask about is sent to OpenAI using your ChatGPT plan") }
    public static var consentHeading: String { L10n.T("Send Mail to OpenAI?") }
    public static var consentBody: String { L10n.T("Malachi Mail will send the selected mail and text you provide to OpenAI through Codex, using your ChatGPT plan. The assistant can read mail and prepare drafts. It cannot send, delete or move messages. This experimental integration does not import your ChatGPT conversations or memory.") }
    public static var boardHeading: String { L10n.T("Let OpenAI Refine the Board?") }
    public static var boardBody: String { L10n.T("Malachi Mail will send board mail to OpenAI through Codex, using your ChatGPT plan. It reads the conversations on the board and any other mail and attachments it needs, and annotates cases. A triage you start yourself may also write replies, which stay on the board, never in your Drafts folder, until you send them. Automatic triage sends newly received mail while enabled. It cannot send, delete or move messages. Which accounts it triages you choose in Settings.") }
}
