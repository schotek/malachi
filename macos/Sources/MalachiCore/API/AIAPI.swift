// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// AI provider accounts (kind `ai`, docs/api.md §4.1) and the assistant the
// daemon runs over them (§4.18; backend/pkg/api/ai.go; the design is
// docs/plans/ai-api.md). An `ai` account names a model API (Anthropic,
// OpenAI, or any server with the OpenAI-compatible chat completions
// interface) and keeps its key in the keyring; the daemon builds the
// prompts (assistant.prompt gives the same ones to a client that runs
// Claude Code or Codex itself), calls the model, streams the answer to the
// connection that started the session and runs the model's tool calls
// through the MCP bridge. Nothing the model writes is trusted: the
// application shows it as plain text, as it shows the bridge's.

import Foundation

// MARK: - Accounts

/// api.AIProvider: the model API an `ai` account talks to.
public struct AIProvider: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// The Anthropic Messages API at `API.anthropicBaseURL`.
    public static let anthropic: AIProvider = "anthropic"
    /// The OpenAI Responses API at `API.openAIBaseURL`.
    public static let openai: AIProvider = "openai"
    /// A server with the OpenAI chat completions interface at
    /// `AIConfig.baseUrl` (Ollama, llama.cpp, LM Studio, vLLM, a gateway);
    /// the key is optional.
    public static let openaiCompatible: AIProvider = "openaiCompatible"

    /// api.AIProvider.Valid: one of the three values.
    public var isValid: Bool { AIProvider.all.contains(self) }

    /// The three values, in the order of ai.go.
    public static let all: [AIProvider] = [.anthropic, .openai, .openaiCompatible]
}

extension API {
    /// api.AnthropicBaseURL, api.OpenAIBaseURL: the fixed base URLs of the
    /// two named providers; `AIConfig.baseUrl` is empty for them.
    public static let anthropicBaseURL = "https://api.anthropic.com"
    public static let openAIBaseURL = "https://api.openai.com/v1"
}

/// api.AIConsent: the version of the consent text the user accepted and
/// when (set by the daemon on account.add/update with a version).
public struct AIConsent: Codable, Sendable, Equatable {
    public var version: Int
    public var acceptedAt: Date?

    public init(version: Int = API.Limits.assistantConsentVersion, acceptedAt: Date? = nil) {
        self.version = version
        self.acceptedAt = acceptedAt
    }
}

/// api.AIConfig: present only on an account of kind `ai`.
public struct AIConfig: Codable, Sendable, Equatable {
    public var provider: AIProvider
    /// The server's base URL for `openaiCompatible` ("…/v1"; normalised:
    /// no user info, query or fragment, no trailing slash; http allowed, a
    /// client warns outside loopback) and empty for the named providers.
    public var baseUrl: String
    /// The model id the assistant uses; required.
    public var model: String
    /// The model of the board's triage; empty = `model`.
    public var boardModel: String
    /// The user's consent to sending mail to this provider (§4.18); nil =
    /// not given, and no session starts.
    public var consent: AIConsent?
    /// Pins the leaf certificate of `baseUrl`'s host (https only), as
    /// `ServerConfig.certificateSha256`.
    public var certificateSha256: String?

    public init(
        provider: AIProvider, baseUrl: String = "", model: String, boardModel: String = "",
        consent: AIConsent? = nil, certificateSha256: String? = nil
    ) {
        self.provider = provider
        self.baseUrl = baseUrl
        self.model = model
        self.boardModel = boardModel
        self.consent = consent
        self.certificateSha256 = certificateSha256
    }

    private enum CodingKeys: String, CodingKey {
        case provider, baseUrl, model, boardModel, consent, certificateSha256
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        provider = try c.decode(AIProvider.self, forKey: .provider)
        baseUrl = try c.decodeIfPresent(String.self, forKey: .baseUrl) ?? ""
        model = try c.decodeIfPresent(String.self, forKey: .model) ?? ""
        boardModel = try c.decodeIfPresent(String.self, forKey: .boardModel) ?? ""
        consent = try c.decodeIfPresent(AIConsent.self, forKey: .consent)
        certificateSha256 = try c.decodeIfPresent(String.self, forKey: .certificateSha256)
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(provider, forKey: .provider)
        if !baseUrl.isEmpty { try c.encode(baseUrl, forKey: .baseUrl) }
        try c.encode(model, forKey: .model)
        if !boardModel.isEmpty { try c.encode(boardModel, forKey: .boardModel) }
        try c.encodeIfPresent(consent, forKey: .consent)
        if let certificateSha256, !certificateSha256.isEmpty { try c.encode(certificateSha256, forKey: .certificateSha256) }
    }

    /// api.AIConfig.Consented: the current consent text was accepted.
    public var consented: Bool {
        guard let consent else { return false }
        return consent.version >= API.Limits.assistantConsentVersion
    }

    /// api.AIConfig.EffectiveBaseURL: the base URL the daemon calls, the
    /// fixed one of a named provider, else `baseUrl`.
    public var effectiveBaseURL: String {
        switch provider {
        case .anthropic: return API.anthropicBaseURL
        case .openai: return API.openAIBaseURL
        default: return baseUrl
        }
    }
}

/// api.AIModel: a model the provider lists (GET /v1/models).
public struct AIModel: Codable, Sendable, Equatable {
    public var id: String
    public var displayName: String?

    public init(id: String, displayName: String? = nil) {
        self.id = id
        self.displayName = displayName
    }
}

/// api.AITestResult: account.test's outcome for an `ai` account, the probe
/// (`EndpointTestResult`'s fields, flat on the wire: Go embeds it) and the
/// models it listed.
public struct AITestResult: Codable, Sendable, Equatable {
    public var ok: Bool
    public var error: RPCError?
    public var capabilities: [String]?
    public var latencyMs: Int
    @NullAsEmpty public var models: [AIModel]

    public init(ok: Bool, error: RPCError? = nil, capabilities: [String]? = nil, latencyMs: Int, models: [AIModel] = []) {
        self.ok = ok
        self.error = error
        self.capabilities = capabilities
        self.latencyMs = latencyMs
        self.models = models
    }

    /// The probe as an `EndpointTestResult`, for code that reads every
    /// account's test the same way.
    public var endpoint: EndpointTestResult {
        EndpointTestResult(ok: ok, error: error, capabilities: capabilities, latencyMs: latencyMs)
    }

    private enum CodingKeys: String, CodingKey {
        case ok, error, capabilities, latencyMs, models
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        ok = try c.decodeIfPresent(Bool.self, forKey: .ok) ?? false
        error = try c.decodeIfPresent(RPCError.self, forKey: .error)
        capabilities = try c.decodeIfPresent([String].self, forKey: .capabilities)
        latencyMs = try c.decodeIfPresent(Int.self, forKey: .latencyMs) ?? 0
        _models = try c.decode(NullAsEmpty<AIModel>.self, forKey: .models)
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(ok, forKey: .ok)
        try c.encodeIfPresent(error, forKey: .error)
        try c.encodeIfPresent(capabilities, forKey: .capabilities)
        try c.encode(latencyMs, forKey: .latencyMs)
        if !models.isEmpty { try c.encode(models, forKey: .models) }
    }
}

// MARK: - Prompts

/// api.PromptPurpose: what an assistant session or prompt is for.
public struct PromptPurpose: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// The assistant panel: a conversation with tools.
    public static let panel: PromptPurpose = "panel"
    /// The compose window's rewrite of a passage, no tools.
    public static let rewrite: PromptPurpose = "rewrite"
    /// The compose window's writing bar, no tools.
    public static let write: PromptPurpose = "write"
    /// Search in the user's own words, no tools, a schema.
    public static let search: PromptPurpose = "search"
    /// A triage run of the board, with tools.
    public static let triage: PromptPurpose = "triage"
    /// A suggested reply of the board, with tools.
    public static let reply: PromptPurpose = "reply"

    /// api.PromptPurpose.Valid: one of the six values.
    public var isValid: Bool { PromptPurpose.all.contains(self) }

    /// The six values, in the order of ai.go.
    public static let all: [PromptPurpose] = [.panel, .rewrite, .write, .search, .triage, .reply]
}

/// api.RewritePreset: what the rewrite does with the passage.
public struct RewritePreset: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let politer: RewritePreset = "politer"
    public static let shorter: RewritePreset = "shorter"
    public static let fix: RewritePreset = "fix"
    public static let toEnglish: RewritePreset = "english"
    /// `RewriteInput.instruction` says what.
    public static let custom: RewritePreset = "custom"
}

/// api.WriteMode: the kind of compose window the writing bar writes for.
public struct WriteMode: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let new: WriteMode = "new"
    public static let reply: WriteMode = "reply"
    public static let replyAll: WriteMode = "replyAll"
    public static let forward: WriteMode = "forward"
    public static let edit: WriteMode = "edit"
}

/// api.PromptContext: what a panel conversation is about, the selected
/// messages of an account or a folder. `added` marks a context added to a
/// running conversation.
public struct PromptContext: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var messageIds: [MessageID]
    public var folderId: FolderID?
    public var added: Bool

    public init(accountId: AccountID, messageIds: [MessageID] = [], folderId: FolderID? = nil, added: Bool = false) {
        self.accountId = accountId
        self.messageIds = messageIds
        self.folderId = folderId
        self.added = added
    }

    private enum CodingKeys: String, CodingKey {
        case accountId, messageIds, folderId, added
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        accountId = try c.decode(AccountID.self, forKey: .accountId)
        messageIds = try c.decodeIfPresent([MessageID].self, forKey: .messageIds) ?? []
        let folder = try c.decodeIfPresent(FolderID.self, forKey: .folderId)
        folderId = folder?.rawValue.isEmpty == false ? folder : nil
        added = try c.decodeIfPresent(Bool.self, forKey: .added) ?? false
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(accountId, forKey: .accountId)
        if !messageIds.isEmpty { try c.encode(messageIds, forKey: .messageIds) }
        if let folderId, !folderId.rawValue.isEmpty { try c.encode(folderId, forKey: .folderId) }
        if added { try c.encode(added, forKey: .added) }
    }
}

/// api.RewriteInput: a compose rewrite, the preset (or `instruction` with
/// `custom`) and the passage.
public struct RewriteInput: Codable, Sendable, Equatable {
    public var preset: RewritePreset
    public var instruction: String
    public var passage: String

    public init(preset: RewritePreset, instruction: String = "", passage: String) {
        self.preset = preset
        self.instruction = instruction
        self.passage = passage
    }

    private enum CodingKeys: String, CodingKey {
        case preset, instruction, passage
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        preset = try c.decode(RewritePreset.self, forKey: .preset)
        instruction = try c.decodeIfPresent(String.self, forKey: .instruction) ?? ""
        passage = try c.decodeIfPresent(String.self, forKey: .passage) ?? ""
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(preset, forKey: .preset)
        if !instruction.isEmpty { try c.encode(instruction, forKey: .instruction) }
        try c.encode(passage, forKey: .passage)
    }
}

/// api.WriteInput: the writing bar's request, the kind of window, the
/// user's optional instruction, the subject, the user's own text in the
/// editor and, for a reply or forward, the text of the prepared original.
public struct WriteInput: Codable, Sendable, Equatable {
    public var mode: WriteMode
    public var instruction: String
    public var subject: String
    public var own: String
    public var quote: String

    public init(mode: WriteMode, instruction: String = "", subject: String = "", own: String = "", quote: String = "") {
        self.mode = mode
        self.instruction = instruction
        self.subject = subject
        self.own = own
        self.quote = quote
    }

    private enum CodingKeys: String, CodingKey {
        case mode, instruction, subject, own, quote
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        mode = try c.decode(WriteMode.self, forKey: .mode)
        instruction = try c.decodeIfPresent(String.self, forKey: .instruction) ?? ""
        subject = try c.decodeIfPresent(String.self, forKey: .subject) ?? ""
        own = try c.decodeIfPresent(String.self, forKey: .own) ?? ""
        quote = try c.decodeIfPresent(String.self, forKey: .quote) ?? ""
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(mode, forKey: .mode)
        if !instruction.isEmpty { try c.encode(instruction, forKey: .instruction) }
        if !subject.isEmpty { try c.encode(subject, forKey: .subject) }
        if !own.isEmpty { try c.encode(own, forKey: .own) }
        if !quote.isEmpty { try c.encode(quote, forKey: .quote) }
    }
}

/// api.SearchInput: the search in the user's own words.
public struct SearchInput: Codable, Sendable, Equatable {
    public var words: String

    public init(words: String) {
        self.words = words
    }
}

/// api.TriageInput: a triage of at most `maxCases` cases; `drafts` lets
/// the model make suggested replies (a manual run).
public struct TriageInput: Codable, Sendable, Equatable {
    public var maxCases: Int
    public var drafts: Bool

    public init(maxCases: Int, drafts: Bool) {
        self.maxCases = maxCases
        self.drafts = drafts
    }
}

/// api.ReplyInput: a suggested reply to `messageId` of a board case of
/// `accountId`; `others` the case's other newest members (ids only),
/// `instruction` the user's optional line, `followUp` the "suggested
/// follow-up" variant (the user waits for someone).
public struct ReplyInput: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var messageId: MessageID
    public var others: [MessageID]
    public var instruction: String
    public var followUp: Bool

    public init(accountId: AccountID, messageId: MessageID, others: [MessageID] = [], instruction: String = "", followUp: Bool = false) {
        self.accountId = accountId
        self.messageId = messageId
        self.others = others
        self.instruction = instruction
        self.followUp = followUp
    }

    private enum CodingKeys: String, CodingKey {
        case accountId, messageId, others, instruction, followUp
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        accountId = try c.decode(AccountID.self, forKey: .accountId)
        messageId = try c.decode(MessageID.self, forKey: .messageId)
        others = try c.decodeIfPresent([MessageID].self, forKey: .others) ?? []
        instruction = try c.decodeIfPresent(String.self, forKey: .instruction) ?? ""
        followUp = try c.decodeIfPresent(Bool.self, forKey: .followUp) ?? false
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(accountId, forKey: .accountId)
        try c.encode(messageId, forKey: .messageId)
        if !others.isEmpty { try c.encode(others, forKey: .others) }
        if !instruction.isEmpty { try c.encode(instruction, forKey: .instruction) }
        if followUp { try c.encode(followUp, forKey: .followUp) }
    }
}

/// api.PromptInput: the input of one turn, the user's text (panel) and
/// the context it is about, or the structured input of a one-shot
/// purpose. Exactly the fields of the purpose are set.
public struct PromptInput: Codable, Sendable, Equatable {
    public var text: String
    public var context: PromptContext?
    public var rewrite: RewriteInput?
    public var write: WriteInput?
    public var search: SearchInput?
    public var triage: TriageInput?
    public var reply: ReplyInput?

    public init(
        text: String = "", context: PromptContext? = nil, rewrite: RewriteInput? = nil, write: WriteInput? = nil,
        search: SearchInput? = nil, triage: TriageInput? = nil, reply: ReplyInput? = nil
    ) {
        self.text = text
        self.context = context
        self.rewrite = rewrite
        self.write = write
        self.search = search
        self.triage = triage
        self.reply = reply
    }

    private enum CodingKeys: String, CodingKey {
        case text, context, rewrite, write, search, triage, reply
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        text = try c.decodeIfPresent(String.self, forKey: .text) ?? ""
        context = try c.decodeIfPresent(PromptContext.self, forKey: .context)
        rewrite = try c.decodeIfPresent(RewriteInput.self, forKey: .rewrite)
        write = try c.decodeIfPresent(WriteInput.self, forKey: .write)
        search = try c.decodeIfPresent(SearchInput.self, forKey: .search)
        triage = try c.decodeIfPresent(TriageInput.self, forKey: .triage)
        reply = try c.decodeIfPresent(ReplyInput.self, forKey: .reply)
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        if !text.isEmpty { try c.encode(text, forKey: .text) }
        try c.encodeIfPresent(context, forKey: .context)
        try c.encodeIfPresent(rewrite, forKey: .rewrite)
        try c.encodeIfPresent(write, forKey: .write)
        try c.encodeIfPresent(search, forKey: .search)
        try c.encodeIfPresent(triage, forKey: .triage)
        try c.encodeIfPresent(reply, forKey: .reply)
    }
}

/// A `json.RawMessage` field: the document's bytes as `Data`, kept as the
/// daemon sent it in spirit (the decoder sees a tree, so the bytes are the
/// tree written again with sorted keys, the same for the same document).
/// nil for an absent or `null` member.
enum RawJSON {
    static func decode<K: CodingKey>(_ c: KeyedDecodingContainer<K>, _ key: K) throws -> Data? {
        guard let value = try c.decodeIfPresent(JSONValue.self, forKey: key), value != .null else { return nil }
        let e = JSONEncoder()
        e.outputFormatting = [.sortedKeys, .withoutEscapingSlashes]
        return try e.encode(value)
    }

    static func encode<K: CodingKey>(_ data: Data?, _ c: inout KeyedEncodingContainer<K>, _ key: K) throws {
        guard let data, !data.isEmpty else { return }
        let value = try JSONDecoder().decode(JSONValue.self, from: data)
        try c.encode(value, forKey: key)
    }
}

/// api.PromptResult: what the daemon built, the system prompt (with the
/// user's profile appended), the message of the turn when an input was
/// given, the bridge tools the model may call (bare names), the arguments
/// the bridge is started with, and the JSON schema of the answer (search)
/// as raw JSON.
public struct PromptResult: Codable, Sendable, Equatable {
    public var systemPrompt: String
    public var message: String
    public var tools: [String]
    public var bridgeArgs: [String]
    public var jsonSchema: Data?

    public init(systemPrompt: String, message: String = "", tools: [String] = [], bridgeArgs: [String] = [], jsonSchema: Data? = nil) {
        self.systemPrompt = systemPrompt
        self.message = message
        self.tools = tools
        self.bridgeArgs = bridgeArgs
        self.jsonSchema = jsonSchema
    }

    /// The schema as a string, "" without one (what a provider that runs
    /// the model itself hands on, `AssistantSessionSpec.jsonSchema`).
    public var jsonSchemaString: String {
        guard let jsonSchema else { return "" }
        return String(decoding: jsonSchema, as: UTF8.self)
    }

    private enum CodingKeys: String, CodingKey {
        case systemPrompt, message, tools, bridgeArgs, jsonSchema
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        systemPrompt = try c.decodeIfPresent(String.self, forKey: .systemPrompt) ?? ""
        message = try c.decodeIfPresent(String.self, forKey: .message) ?? ""
        tools = try c.decodeIfPresent([String].self, forKey: .tools) ?? []
        bridgeArgs = try c.decodeIfPresent([String].self, forKey: .bridgeArgs) ?? []
        jsonSchema = try RawJSON.decode(c, .jsonSchema)
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(systemPrompt, forKey: .systemPrompt)
        if !message.isEmpty { try c.encode(message, forKey: .message) }
        if !tools.isEmpty { try c.encode(tools, forKey: .tools) }
        if !bridgeArgs.isEmpty { try c.encode(bridgeArgs, forKey: .bridgeArgs) }
        try RawJSON.encode(jsonSchema, &c, .jsonSchema)
    }
}

/// api.AssistantPromptParams: asks for the prompt of a purpose. `language`
/// is the client's UI language (BCP 47) the model answers in; `accountId`,
/// for a one-shot purpose, the account whose profile note is appended;
/// `trigger` the triage run's trigger and `runId` its board run, which the
/// bridge arguments carry; `input` the turn's input (the message is built
/// only with it).
public struct AssistantPromptParams: Codable, Sendable, Equatable {
    public var purpose: PromptPurpose
    public var language: String
    public var accountId: AccountID?
    public var trigger: BoardTrigger?
    public var runId: String
    public var input: PromptInput?

    public init(
        purpose: PromptPurpose, language: String, accountId: AccountID? = nil, trigger: BoardTrigger? = nil,
        runId: String = "", input: PromptInput? = nil
    ) {
        self.purpose = purpose
        self.language = language
        self.accountId = accountId
        self.trigger = trigger
        self.runId = runId
        self.input = input
    }

    private enum CodingKeys: String, CodingKey {
        case purpose, language, accountId, trigger, runId, input
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        purpose = try c.decode(PromptPurpose.self, forKey: .purpose)
        language = try c.decodeIfPresent(String.self, forKey: .language) ?? ""
        accountId = try c.decodeIfPresent(AccountID.self, forKey: .accountId)
        trigger = try c.decodeIfPresent(BoardTrigger.self, forKey: .trigger)
        runId = try c.decodeIfPresent(String.self, forKey: .runId) ?? ""
        input = try c.decodeIfPresent(PromptInput.self, forKey: .input)
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(purpose, forKey: .purpose)
        try c.encode(language, forKey: .language)
        if let accountId, !accountId.rawValue.isEmpty { try c.encode(accountId, forKey: .accountId) }
        if let trigger, !trigger.rawValue.isEmpty { try c.encode(trigger, forKey: .trigger) }
        if !runId.isEmpty { try c.encode(runId, forKey: .runId) }
        try c.encodeIfPresent(input, forKey: .input)
    }
}

// MARK: - Sessions

/// api.AssistantSessionStartParams: opens a session over an `ai` account.
/// `bridge` is the absolute path of the MCP bridge the daemon starts for
/// the tools (purposes with tools); `noteAccountId` the mail or Jira
/// account whose profile note is appended (absent = none); `runId` the
/// board run of a triage session; `input`, when given, is sent as the
/// first turn at once (a one-shot purpose always gives it);
/// `timeoutSeconds` bounds one turn (0 = the purpose's default).
public struct AssistantSessionStartParams: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var purpose: PromptPurpose
    public var language: String
    public var bridge: String
    public var noteAccountId: AccountID?
    public var trigger: BoardTrigger?
    public var runId: String
    public var input: PromptInput?
    public var timeoutSeconds: Int

    public init(
        accountId: AccountID, purpose: PromptPurpose, language: String, bridge: String = "", noteAccountId: AccountID? = nil,
        trigger: BoardTrigger? = nil, runId: String = "", input: PromptInput? = nil, timeoutSeconds: Int = 0
    ) {
        self.accountId = accountId
        self.purpose = purpose
        self.language = language
        self.bridge = bridge
        self.noteAccountId = noteAccountId
        self.trigger = trigger
        self.runId = runId
        self.input = input
        self.timeoutSeconds = timeoutSeconds
    }

    private enum CodingKeys: String, CodingKey {
        case accountId, purpose, language, bridge, noteAccountId, trigger, runId, input, timeoutSeconds
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        accountId = try c.decode(AccountID.self, forKey: .accountId)
        purpose = try c.decode(PromptPurpose.self, forKey: .purpose)
        language = try c.decodeIfPresent(String.self, forKey: .language) ?? ""
        bridge = try c.decodeIfPresent(String.self, forKey: .bridge) ?? ""
        noteAccountId = try c.decodeIfPresent(AccountID.self, forKey: .noteAccountId)
        trigger = try c.decodeIfPresent(BoardTrigger.self, forKey: .trigger)
        runId = try c.decodeIfPresent(String.self, forKey: .runId) ?? ""
        input = try c.decodeIfPresent(PromptInput.self, forKey: .input)
        timeoutSeconds = try c.decodeIfPresent(Int.self, forKey: .timeoutSeconds) ?? 0
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(accountId, forKey: .accountId)
        try c.encode(purpose, forKey: .purpose)
        try c.encode(language, forKey: .language)
        if !bridge.isEmpty { try c.encode(bridge, forKey: .bridge) }
        if let noteAccountId, !noteAccountId.rawValue.isEmpty { try c.encode(noteAccountId, forKey: .noteAccountId) }
        if let trigger, !trigger.rawValue.isEmpty { try c.encode(trigger, forKey: .trigger) }
        if !runId.isEmpty { try c.encode(runId, forKey: .runId) }
        try c.encodeIfPresent(input, forKey: .input)
        if timeoutSeconds != 0 { try c.encode(timeoutSeconds, forKey: .timeoutSeconds) }
    }
}

/// api.AssistantSessionStartResult: the session and the tools the model
/// has (bare names).
public struct AssistantSessionStartResult: Codable, Sendable, Equatable {
    public var sessionId: String
    @NullAsEmpty public var tools: [String]

    public init(sessionId: String, tools: [String] = []) {
        self.sessionId = sessionId
        self.tools = tools
    }
}

/// api.AssistantSessionSendParams: sends a turn.
public struct AssistantSessionSendParams: Codable, Sendable, Equatable {
    public var sessionId: String
    public var input: PromptInput

    public init(sessionId: String, input: PromptInput) {
        self.sessionId = sessionId
        self.input = input
    }
}

/// api.AssistantSessionStopParams.
public struct AssistantSessionStopParams: Codable, Sendable, Equatable {
    public var sessionId: String

    public init(sessionId: String) {
        self.sessionId = sessionId
    }
}

/// api.AssistantProviderModelsParams: lists the models of an `ai`
/// account's provider; `refresh` ignores the daemon's cache.
public struct AssistantProviderModelsParams: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var refresh: Bool

    public init(accountId: AccountID, refresh: Bool = false) {
        self.accountId = accountId
        self.refresh = refresh
    }

    private enum CodingKeys: String, CodingKey {
        case accountId, refresh
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        accountId = try c.decode(AccountID.self, forKey: .accountId)
        refresh = try c.decodeIfPresent(Bool.self, forKey: .refresh) ?? false
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(accountId, forKey: .accountId)
        if refresh { try c.encode(refresh, forKey: .refresh) }
    }
}

/// api.AssistantProviderModelsResult.
public struct AssistantProviderModelsResult: Codable, Sendable, Equatable {
    @NullAsEmpty public var models: [AIModel]

    public init(models: [AIModel]) {
        self.models = models
    }
}

/// api.AssistantUsage: the token usage of a turn, a session or a day.
public struct AssistantUsage: Codable, Sendable, Equatable {
    public var inputTokens: Int64
    public var outputTokens: Int64
    public var cacheReadInputTokens: Int64
    public var cacheCreationInputTokens: Int64

    public init(inputTokens: Int64 = 0, outputTokens: Int64 = 0, cacheReadInputTokens: Int64 = 0, cacheCreationInputTokens: Int64 = 0) {
        self.inputTokens = inputTokens
        self.outputTokens = outputTokens
        self.cacheReadInputTokens = cacheReadInputTokens
        self.cacheCreationInputTokens = cacheCreationInputTokens
    }

    private enum CodingKeys: String, CodingKey {
        case inputTokens, outputTokens, cacheReadInputTokens, cacheCreationInputTokens
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        inputTokens = try c.decodeIfPresent(Int64.self, forKey: .inputTokens) ?? 0
        outputTokens = try c.decodeIfPresent(Int64.self, forKey: .outputTokens) ?? 0
        cacheReadInputTokens = try c.decodeIfPresent(Int64.self, forKey: .cacheReadInputTokens) ?? 0
        cacheCreationInputTokens = try c.decodeIfPresent(Int64.self, forKey: .cacheCreationInputTokens) ?? 0
    }

    /// api.AssistantUsage.Add: the sum of both.
    public func adding(_ o: AssistantUsage) -> AssistantUsage {
        AssistantUsage(
            inputTokens: inputTokens + o.inputTokens, outputTokens: outputTokens + o.outputTokens,
            cacheReadInputTokens: cacheReadInputTokens + o.cacheReadInputTokens,
            cacheCreationInputTokens: cacheCreationInputTokens + o.cacheCreationInputTokens)
    }
}

/// api.AssistantUsageParams: the usage of the last `days` local days (0 =
/// 1: today), of one `ai` account or of all.
public struct AssistantUsageParams: Codable, Sendable, Equatable {
    public var accountId: AccountID?
    public var days: Int

    public init(accountId: AccountID? = nil, days: Int = 0) {
        self.accountId = accountId
        self.days = days
    }

    private enum CodingKeys: String, CodingKey {
        case accountId, days
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        accountId = try c.decodeIfPresent(AccountID.self, forKey: .accountId)
        days = try c.decodeIfPresent(Int.self, forKey: .days) ?? 0
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        if let accountId, !accountId.rawValue.isEmpty { try c.encode(accountId, forKey: .accountId) }
        if days != 0 { try c.encode(days, forKey: .days) }
    }
}

/// api.AssistantUsageDay: one account's usage on one local day
/// ("2026-11-07"), summed over its models. Go embeds `AssistantUsage`, so
/// its fields lie flat beside `day`, `accountId`, `sessions` and `turns`.
public struct AssistantUsageDay: Codable, Sendable, Equatable {
    public var day: String
    public var accountId: AccountID
    public var usage: AssistantUsage
    public var sessions: Int
    public var turns: Int

    public init(day: String, accountId: AccountID, usage: AssistantUsage = AssistantUsage(), sessions: Int = 0, turns: Int = 0) {
        self.day = day
        self.accountId = accountId
        self.usage = usage
        self.sessions = sessions
        self.turns = turns
    }

    private enum CodingKeys: String, CodingKey {
        case day, accountId, sessions, turns
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        day = try c.decodeIfPresent(String.self, forKey: .day) ?? ""
        accountId = try c.decode(AccountID.self, forKey: .accountId)
        usage = try AssistantUsage(from: decoder)
        sessions = try c.decodeIfPresent(Int.self, forKey: .sessions) ?? 0
        turns = try c.decodeIfPresent(Int.self, forKey: .turns) ?? 0
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(day, forKey: .day)
        try c.encode(accountId, forKey: .accountId)
        try usage.encode(to: encoder)
        try c.encode(sessions, forKey: .sessions)
        try c.encode(turns, forKey: .turns)
    }
}

/// api.AssistantUsageResult.
public struct AssistantUsageResult: Codable, Sendable, Equatable {
    @NullAsEmpty public var days: [AssistantUsageDay]

    public init(days: [AssistantUsageDay]) {
        self.days = days
    }
}

// MARK: - Events

/// api.AssistantEventKind: what a `notify.assistantEvent` carries.
public struct AssistantEventKind: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// Starts a turn: `tools` lists the tools the model has.
    public static let `init`: AssistantEventKind = "init"
    /// A piece of the answer as it streams (`text`).
    public static let textDelta: AssistantEventKind = "textDelta"
    /// A whole block of the answer (`text`), after its deltas.
    public static let text: AssistantEventKind = "text"
    /// The model called `tool` (`id` is the call's id).
    public static let toolUse: AssistantEventKind = "toolUse"
    /// The bridge answered the call `id` with `text`, the bridge's result
    /// verbatim (an error when `isError`).
    public static let toolResult: AssistantEventKind = "toolResult"
    /// Ends a turn (`result`).
    public static let result: AssistantEventKind = "result"
    /// The turn or the session failed (`failure`); a session failure ends
    /// the session.
    public static let failure: AssistantEventKind = "failure"
    /// The tokens of the turn so far (`usage`); the last one, right before
    /// `result`, is the turn's whole usage.
    public static let usage: AssistantEventKind = "usage"

    /// The eight kinds, in the order of ai.go.
    public static let all: [AssistantEventKind] = [.`init`, .textDelta, .text, .toolUse, .toolResult, .result, .failure, .usage]
}

/// api.AssistantFailureClass: why a turn or session failed; the
/// application words it (`Assistant.apiFailureText`), the daemon's
/// `message` is not for display.
public struct AssistantFailureClass: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// The key was refused (401).
    public static let auth: AssistantFailureClass = "auth"
    /// The key may not use this (403).
    public static let permission: AssistantFailureClass = "permission"
    /// Too many requests (429); `retryAfter` when known.
    public static let rateLimit: AssistantFailureClass = "rateLimit"
    /// The provider is busy (529, 503).
    public static let overloaded: AssistantFailureClass = "overloaded"
    /// The account has no credit or used up its quota; never retried.
    public static let quota: AssistantFailureClass = "quota"
    /// The server has no such model or could not load it.
    public static let model: AssistantFailureClass = "model"
    /// The provider refused the request (400).
    public static let invalidRequest: AssistantFailureClass = "invalidRequest"
    /// The model cannot call tools.
    public static let toolsUnsupported: AssistantFailureClass = "toolsUnsupported"
    /// No connection, timeout, TLS.
    public static let network: AssistantFailureClass = "network"
    /// The provider's answer could not be read.
    public static let `protocol`: AssistantFailureClass = "protocol"
    /// The history grew past `maxAssistantHistoryBytes`.
    public static let contextFull: AssistantFailureClass = "contextFull"
    /// The MCP bridge could not be started or died.
    public static let bridge: AssistantFailureClass = "bridge"
    /// The turn exceeded its timeout.
    public static let timeout: AssistantFailureClass = "timeout"
    /// The turn exceeded `maxAssistantTurnTools` tool rounds.
    public static let limit: AssistantFailureClass = "limit"
    /// assistant.sessionStop, the connection closed, the daemon stops.
    public static let stopped: AssistantFailureClass = "stopped"

    /// The fifteen classes, in the order of ai.go.
    public static let all: [AssistantFailureClass] = [
        .auth, .permission, .rateLimit, .overloaded, .quota, .model, .invalidRequest, .toolsUnsupported,
        .network, .`protocol`, .contextFull, .bridge, .timeout, .limit, .stopped,
    ]
}

/// api.AssistantResult: the end of a turn, whether the model finished
/// (`success`; false after a failure or a refusal), `structured` the
/// answer as raw JSON when a schema was asked for and the answer is valid
/// JSON, `reason` the model's stop reason as a word ("endTurn",
/// "maxTokens", "refusal").
public struct AssistantResult: Codable, Sendable, Equatable {
    public var success: Bool
    public var structured: Data?
    public var reason: String

    public init(success: Bool, structured: Data? = nil, reason: String = "") {
        self.success = success
        self.structured = structured
        self.reason = reason
    }

    private enum CodingKeys: String, CodingKey {
        case success, structured, reason
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        success = try c.decodeIfPresent(Bool.self, forKey: .success) ?? false
        structured = try RawJSON.decode(c, .structured)
        reason = try c.decodeIfPresent(String.self, forKey: .reason) ?? ""
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(success, forKey: .success)
        try RawJSON.encode(structured, &c, .structured)
        if !reason.isEmpty { try c.encode(reason, forKey: .reason) }
    }
}

/// api.AssistantFailure: the class of a failure, `retryAfter` seconds when
/// the provider said so, and the daemon's technical message (never shown).
public struct AssistantFailure: Codable, Sendable, Equatable {
    public var `class`: AssistantFailureClass
    public var retryAfter: Int
    public var message: String

    public init(class: AssistantFailureClass, retryAfter: Int = 0, message: String = "") {
        self.class = `class`
        self.retryAfter = retryAfter
        self.message = message
    }

    private enum CodingKeys: String, CodingKey {
        case `class`, retryAfter, message
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        `class` = try c.decode(AssistantFailureClass.self, forKey: .class)
        retryAfter = try c.decodeIfPresent(Int.self, forKey: .retryAfter) ?? 0
        message = try c.decodeIfPresent(String.self, forKey: .message) ?? ""
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(`class`, forKey: .class)
        if retryAfter != 0 { try c.encode(retryAfter, forKey: .retryAfter) }
        if !message.isEmpty { try c.encode(message, forKey: .message) }
    }
}

/// api.AssistantEvent: the params of `notify.assistantEvent`, sent to the
/// session's connection only.
public struct AssistantEvent: Codable, Sendable, Equatable {
    public var sessionId: String
    public var kind: AssistantEventKind
    public var text: String
    public var tools: [String]
    public var tool: String
    public var id: String
    public var isError: Bool
    public var result: AssistantResult?
    public var failure: AssistantFailure?
    public var usage: AssistantUsage?

    public init(
        sessionId: String, kind: AssistantEventKind, text: String = "", tools: [String] = [], tool: String = "", id: String = "",
        isError: Bool = false, result: AssistantResult? = nil, failure: AssistantFailure? = nil, usage: AssistantUsage? = nil
    ) {
        self.sessionId = sessionId
        self.kind = kind
        self.text = text
        self.tools = tools
        self.tool = tool
        self.id = id
        self.isError = isError
        self.result = result
        self.failure = failure
        self.usage = usage
    }

    private enum CodingKeys: String, CodingKey {
        case sessionId, kind, text, tools, tool, id, isError, result, failure, usage
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        sessionId = try c.decodeIfPresent(String.self, forKey: .sessionId) ?? ""
        kind = try c.decode(AssistantEventKind.self, forKey: .kind)
        text = try c.decodeIfPresent(String.self, forKey: .text) ?? ""
        tools = try c.decodeIfPresent([String].self, forKey: .tools) ?? []
        tool = try c.decodeIfPresent(String.self, forKey: .tool) ?? ""
        id = try c.decodeIfPresent(String.self, forKey: .id) ?? ""
        isError = try c.decodeIfPresent(Bool.self, forKey: .isError) ?? false
        result = try c.decodeIfPresent(AssistantResult.self, forKey: .result)
        failure = try c.decodeIfPresent(AssistantFailure.self, forKey: .failure)
        usage = try c.decodeIfPresent(AssistantUsage.self, forKey: .usage)
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(sessionId, forKey: .sessionId)
        try c.encode(kind, forKey: .kind)
        if !text.isEmpty { try c.encode(text, forKey: .text) }
        if !tools.isEmpty { try c.encode(tools, forKey: .tools) }
        if !tool.isEmpty { try c.encode(tool, forKey: .tool) }
        if !id.isEmpty { try c.encode(id, forKey: .id) }
        if isError { try c.encode(isError, forKey: .isError) }
        try c.encodeIfPresent(result, forKey: .result)
        try c.encodeIfPresent(failure, forKey: .failure)
        try c.encodeIfPresent(usage, forKey: .usage)
    }
}

/// api.AssistantUsageChangedNotification: an `ai` account's usage of today
/// changed; the application runs assistant.usage again.
public struct AssistantUsageChangedNotification: Codable, Sendable, Equatable {
    public var accountId: AccountID

    public init(accountId: AccountID) {
        self.accountId = accountId
    }
}

// MARK: - Methods and limits

extension API {
    /// Builds the prompt of a purpose for a client that runs the model
    /// itself (Claude Code, Codex).
    public enum AssistantPrompt: RPCMethod {
        public typealias Params = AssistantPromptParams
        public typealias Result = PromptResult
        public static let name = "assistant.prompt"
        public static let timeout = RPCTimeouts.assistantPrompt
    }

    /// Opens a session over an `ai` account on this connection; the daemon
    /// starts the bridge and may send the first turn at once.
    public enum AssistantSessionStart: RPCMethod {
        public typealias Params = AssistantSessionStartParams
        public typealias Result = AssistantSessionStartResult
        public static let name = "assistant.sessionStart"
        public static let timeout = RPCTimeouts.assistantSessionStart
    }

    /// Sends a turn; the answer comes as `notify.assistantEvent`s.
    public enum AssistantSessionSend: RPCMethod {
        public typealias Params = AssistantSessionSendParams
        public typealias Result = EmptyResult
        public static let name = "assistant.sessionSend"
    }

    public enum AssistantSessionStop: RPCMethod {
        public typealias Params = AssistantSessionStopParams
        public typealias Result = EmptyResult
        public static let name = "assistant.sessionStop"
    }

    /// Lists the models of an `ai` account's provider (GET /v1/models with
    /// the stored key), from the daemon's cache of an hour unless `refresh`.
    public enum AssistantProviderModels: RPCMethod {
        public typealias Params = AssistantProviderModelsParams
        public typealias Result = AssistantProviderModelsResult
        public static let name = "assistant.providerModels"
        public static let timeout = RPCTimeouts.assistantModels
    }

    /// The tokens the daemon's assistant used, per `ai` account and local
    /// day.
    public enum AssistantUsage: RPCMethod {
        public typealias Params = AssistantUsageParams
        public typealias Result = AssistantUsageResult
        public static let name = "assistant.usage"
    }

    /// The daemon's assistant's methods, which follow the profile's in
    /// methods.go's table.
    static let assistantMethods: [any RPCMethod.Type] = [
        AssistantPrompt.self, AssistantSessionStart.self, AssistantSessionSend.self, AssistantSessionStop.self,
        AssistantProviderModels.self, AssistantUsage.self,
    ]
}

extension API.Limits {
    /// api.AssistantConsentVersion: the version of the consent text an `ai`
    /// account's user accepted (`AIConsent.version`); a session starts only
    /// with this version or a later one.
    public static let assistantConsentVersion = 1
    /// Open sessions of one connection.
    public static let maxAssistantSessionsPerConn = 4
    /// Open sessions of the daemon.
    public static let maxAssistantSessions = 16
    /// Tool rounds of one turn.
    public static let maxAssistantTurnTools = 50
    /// A tool result kept in the history.
    public static let maxAssistantToolResultBytes = 256 << 10
    /// The session's history; beyond it the session ends.
    public static let maxAssistantHistoryBytes = 1 << 20
    /// `PromptInput.text` and every free text of an input.
    public static let maxPromptTextBytes = 64 << 10
    /// `PromptContext.messageIds`, `ReplyInput.others`.
    public static let maxPromptMessageIDs = 20
    /// `AssistantSessionStartParams.runId`.
    public static let maxAssistantRunIDBytes = 128
    /// `AssistantUsageParams.days`.
    public static let maxAssistantUsageDays = 400
    /// api.AssistantSessionIdle: a session nobody wrote to ends.
    public static let assistantSessionIdle: Duration = .seconds(30 * 60)
    /// api.DefaultAssistantTimeout: one turn of a panel session; the
    /// longest a triage session may ask for is `maxAssistantTimeout`.
    public static let defaultAssistantTimeout: Duration = .seconds(5 * 60)
    public static let maxAssistantTimeout: Duration = .seconds(15 * 60)
}
