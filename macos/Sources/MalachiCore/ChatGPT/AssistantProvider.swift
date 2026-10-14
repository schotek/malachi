// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// Matches ui/internal/assistantpanel/provider.go: each session owns an immutable tool policy.
public enum AssistantProviderID: String, Sendable { case claude, chatgpt }
public struct AssistantSessionSpec: Sendable {
    public var systemPrompt: String
    public var jsonSchema: String = ""
    public var tools: AssistantRequest.Tools?
    public var modelID: String = ""
    public var timeout: Duration = .seconds(120)
    public var boardConsent = false
    public init(systemPrompt: String, jsonSchema: String = "", tools: AssistantRequest.Tools? = nil,
                modelID: String = "", timeout: Duration = .seconds(120), boardConsent: Bool = false) {
        self.systemPrompt = systemPrompt; self.jsonSchema = jsonSchema; self.tools = tools
        self.modelID = modelID; self.timeout = timeout; self.boardConsent = boardConsent
    }
}
@MainActor public protocol AssistantSession: AnyObject {
    var running: Bool { get }
    var onEvents: (([Assistant.Event]) -> Void)? { get set }
    var onExit: ((String) -> Void)? { get set }
    func submit(_ input: String) async throws
    func terminate()
}
@MainActor public protocol AssistantProvider: AnyObject {
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
