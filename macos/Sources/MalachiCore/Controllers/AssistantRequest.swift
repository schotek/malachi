// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os

/// One question to the user's assistant: the one-shot requests of
/// ui/internal/assistant's In App target, the compose window's rewrite
/// (`ComposeRewriteController`) and writing bar, the search in the user's
/// own words (`SearchConversion`), which read no mail, and the board's
/// triage run (`BoardTriageController`) and suggested reply
/// (`BoardReplyController`), which read it through the bridge. GTK
/// ui/internal/assistantpanel `Request` is the port of this.
///
/// Every prompt is the daemon's (`AssistantPrompter`, assistant.prompt,
/// docs/api.md §4.18): a request names its purpose and input
/// (`PromptInput`) and gets the system prompt with the user's profile, the
/// tools, the bridge's arguments, the schema and the message back. It
/// runs the panel's protocol once (`ClaudeCodeProcess`): the command line
/// of `Assistant.args` over that prompt without the bridge (no MCP server,
/// no tool), or with the bridge, the prompt's arguments and tools when the
/// caller passes `Tools`, and with `--json-schema` when the prompt has a
/// schema, in the panel's private directory and with `Assistant.childEnv`;
/// one `Assistant.userMessage` on stdin, which is then closed; the answer
/// is the result event. The steps, each of which may end it:
///
/// 1. The first request ever asks for consent (`consent`, the panel's
///    sheet "Send Mail to Claude?" on the window that asks; the answer is
///    the shared `assistant-consent`, kept even when the request was
///    cancelled meanwhile). Declined: `.declined`, nothing is sent.
/// 2. The prompt is built by the daemon (`prompter`, assistant.prompt with
///    the call's purpose and input). A refused input (empty, too long, the
///    daemon away) is `.stopped` with the daemon's reason.
/// 3. Claude Code is located (none: `.notFound`) and must not say it is
///    signed out (`ClaudeCodeLocator.signedIn`, asked afresh:
///    `.notSignedIn`; not known counts as signed in, and the process then
///    says what is wrong: a turn the API refused for its sign-in,
///    `Assistant.Event.Kind.failure`, is `.notSignedIn` too).
/// 4. The process starts; text deltas stream to `onText` (a whole text
///    block replaces the deltas before it), the result ends it: a success
///    is `.answered` with the result's text and structured_output, anything
///    else `.stopped` with the result's text or subtype. The process ending
///    before its result is `.stopped` with its stderr's first line, and no
///    result within `timeout` (120 s, or the call's own) ends it with
///    `.stopped(timedOut)`. With `Tools`, the init event must report the
///    bridge connected (else `.toolsMissing`), and every tool call and
///    tool result goes to `onTool`. Every event that carries usage (an API
///    message's, the result's) goes to `onUsage` first, before the event
///    is handled (`Assistant.UsageTally` adds them up); none arrives once
///    the request ended or was cancelled.
///
/// With another provider in effect (`provider`: Codex, a session the
/// daemon runs over an `ai` account) steps 3 and 4 are its session, opened
/// with the prompt and the call's purpose, input and bound, and its events
/// normalised to the same protocol.
///
/// One request at a time: `start` cancels the one under way, and
/// `cancel()` ends it (its process terminated); a cancelled request never
/// calls its completion. Nothing is kept on disk; model text is never
/// logged.
@MainActor
public final class AssistantRequest {
    /// Why a request brought no answer (assistantpanel.Failure).
    public enum Failure: Error, Sendable, Equatable {
        /// Claude Code was not found on this computer.
        case notFound
        /// Claude Code says it is not signed in, or the API refused its
        /// sign-in; for a provider, its reason (a ChatGPT connection to
        /// make again, an `ai` account's missing consent or refused key:
        /// "api_consent_required", "api_auth"), "" for Claude Code's own.
        case notSignedIn(reason: String = "")
        /// It ended badly; the reason is technical (the result's text or
        /// subtype, stderr's first line, the timeout, a launch failure).
        case stopped(String)
        /// A request with `Tools`: Claude Code did not report the bridge
        /// connected.
        case toolsMissing
        /// The provider refused the request because the usage limit of the
        /// user's plan was reached (ChatGPT's HTTP 429, an API provider's
        /// rate limit or exhausted credit); the provider's code.
        case limit(String)

        /// The line where the panel's errors are shown (the compose
        /// window's popover): the panel's texts, and for a missing sign-in
        /// where to sign in (a request has no Sign In… of its own); a
        /// provider's reason in that provider's words (`providerText`).
        public var text: String {
            switch self {
            case .notFound: return Assistant.panelTexts().notFound
            case .notSignedIn(let reason): return Self.providerText(reason, signIn: true) ?? Assistant.signInTexts().hint
            case .stopped(let reason): return Self.providerText(reason) ?? Assistant.stoppedText(reason)
            case .toolsMissing: return Assistant.panelTexts().toolsMissing
            case .limit(let reason): return Self.providerText(reason) ?? Assistant.stoppedText(Board.Text.triageFailure(.limit))
            }
        }

        /// The reason inside another sentence ("The search could not be
        /// converted: %s").
        public var reason: String {
            switch self {
            case .notFound: return Assistant.panelTexts().notFound
            case .notSignedIn(let reason): return Self.providerText(reason, signIn: true) ?? Assistant.signInTexts().hint
            case .stopped(let reason): return Self.providerText(reason) ?? reason
            case .toolsMissing: return Assistant.panelTexts().toolsMissing
            case .limit(let reason): return Self.providerText(reason) ?? Board.Text.triageFailure(.limit)
            }
        }

        /// assistantpanel.Failure.providerText: what a provider's reason
        /// says in that provider's words: a ChatGPT connection to make
        /// again (a missing sign-in with a "chatgpt_" reason), or the
        /// sentence of a session's failure class of the daemon's assistant
        /// (`Assistant.apiFailureText`). nil for any other reason (and for
        /// Claude Code's, whose reasons are never these codes).
        static func providerText(_ reason: String, signIn: Bool = false) -> String? {
            if signIn, reason.hasPrefix("chatgpt_") {
                return L10n.T("Reconnect to ChatGPT")
            }
            return Assistant.apiFailureText(reason)
        }

        /// The failure is the reason of a session the daemon runs over an
        /// `ai` account ("api_<class>").
        public var isAPIFailure: Bool {
            switch self {
            case .notSignedIn(let r), .stopped(let r), .limit(let r): return r.hasPrefix(Assistant.apiFailurePrefix)
            default: return false
            }
        }
    }

    /// assistantpanel.ProviderFailure: the failure a provider's reason
    /// stands for (an error of `AssistantProvider.start` or
    /// `AssistantSession.submit`, a failed result's text, an exit's
    /// reason): Codex missing is `notFound`; a ChatGPT connection that is
    /// missing, lapsed, refused or without consent is `notSignedIn` (the
    /// board offers to connect again, as it offers Claude Code's sign-in);
    /// the plan's usage limit is `limit`; anything else `stopped` with the
    /// reason as it is. The reasons of a session the daemon runs over an
    /// `ai` account are "api_<class>" (`Assistant.apiFailurePrefix`): a
    /// missing consent or a refused key is `notSignedIn` with the reason
    /// (the panel offers Set Up in Integrations…), a rate limit, an
    /// overloaded provider or an account without credit `limit`, a model
    /// without tools, a server without the model or a bridge that could
    /// not start `toolsMissing`, the rest `stopped`.
    public nonisolated static func providerFailure(_ reason: String) -> Failure {
        switch reason {
        case "codex_not_found":
            return .notFound
        case "chatgpt_not_connected", "chatgpt_reconnect_required", "chatgpt_consent_required",
             "chatgpt_permission_denied", "chatgpt_identity_mismatch",
             "api_consent_required", "api_auth":
            return .notSignedIn(reason: reason)
        case "chatgpt_usage_limit", "api_rateLimit", "api_overloaded", "api_quota":
            return .limit(reason)
        case "api_toolsUnsupported", "api_bridge", "api_model":
            return .toolsMissing
        default:
            return .stopped(reason)
        }
    }

    /// Whether a change of settings `key` concerns the assistant provider
    /// in effect, so that a request of it under way must end: the provider
    /// itself always; the Codex executable and a ChatGPT consent withdrawn
    /// only while the provider is ChatGPT. A model (the panel's or the
    /// triage's ChatGPT model) never ends a request: it applies from the
    /// next one, as a change that concerns the other provider.
    public static func providerChangeConcernsActive(_ key: Settings.Key, settings: Settings) -> Bool {
        switch key {
        case .assistantProvider:
            return true
        case .assistantChatGPTModel, .boardTriageChatGPTModel:
            return false
        case .assistantCodexPath:
            return settings.assistantProvider == Assistant.providerChatGPT
        case .assistantChatGPTConsentVersion:
            return settings.assistantProvider == Assistant.providerChatGPT && settings.assistantChatGPTConsentVersion != 1
        case .boardTriageChatGPTConsentVersion:
            return settings.assistantProvider == Assistant.providerChatGPT && settings.boardTriageChatGPTConsentVersion != 1
        default:
            return false
        }
    }

    /// The settings keys `providerChangeConcernsActive` looks at.
    public static let providerKeys: [Settings.Key] = [
        .assistantProvider, .assistantCodexPath, .assistantChatGPTModel, .assistantChatGPTConsentVersion,
        .boardTriageChatGPTModel, .boardTriageChatGPTConsentVersion,
    ]

    /// assistantpanel.Tools: the bridge a request gives the assistant, and
    /// what of it may run. A caller sets `bridge` and `socket`; the request
    /// fills `bridgeArgs` and `allowed` from the daemon's prompt of the
    /// purpose.
    public struct Tools: Sendable, Equatable {
        /// The path of `malachi-mcp`.
        public var bridge: String
        /// The daemon's socket (--socket); "" for the bridge's default.
        public var socket: String
        /// The bridge's further arguments after --socket
        /// (`PromptResult.bridgeArgs`: --in-app first, then the triage's or
        /// the suggested reply's).
        public var bridgeArgs: [String]
        /// --allowedTools, prefixed (`Assistant.prefixTools` of
        /// `PromptResult.tools`).
        public var allowed: [String]

        public init(bridge: String, socket: String, bridgeArgs: [String] = [], allowed: [String] = []) {
            self.bridge = bridge
            self.socket = socket
            self.bridgeArgs = bridgeArgs
            self.allowed = allowed
        }
    }

    /// assistantpanel.Call: what one `startCall` asks for.
    public struct Call: Sendable {
        /// What the daemon builds the prompt for, and the turn's input
        /// (exactly the field of the purpose set, `PromptInput`).
        public var purpose: PromptPurpose
        public var input: PromptInput
        /// The account whose profile note is appended (the compose
        /// window's account for a rewrite or the writing bar); nil for
        /// none.
        public var noteAccountID: AccountID?
        /// A triage's trigger and board run.
        public var trigger: BoardTrigger?
        public var runID: String
        /// Overrides the selected provider's model for this call.
        public var modelID: String
        /// Gives the assistant the bridge (its `bridge` and `socket`); nil:
        /// no tool at all.
        public var tools: Tools?
        /// Replaces the request's own timeout for this call.
        public var timeout: Duration?
        /// Replaces the `assistant-model` setting when set (read at the
        /// start).
        public var model: Assistant.Model?
        /// Gets the answer's text as it streams, all of it so far.
        public var onText: (@MainActor (String) -> Void)?
        /// Gets every tool call and tool result (`Event.Kind.toolUse`,
        /// `.toolResult`); it may cancel the request.
        public var onTool: (@MainActor (Assistant.Event) -> Void)?
        /// Gets every event that carries usage, before the event is
        /// handled.
        public var onUsage: (@MainActor (Assistant.Event) -> Void)?

        public init(
            purpose: PromptPurpose, input: PromptInput, noteAccountID: AccountID? = nil, trigger: BoardTrigger? = nil,
            runID: String = "", modelID: String = "", tools: Tools? = nil, timeout: Duration? = nil,
            model: Assistant.Model? = nil, onText: (@MainActor (String) -> Void)? = nil,
            onTool: (@MainActor (Assistant.Event) -> Void)? = nil, onUsage: (@MainActor (Assistant.Event) -> Void)? = nil
        ) {
            self.purpose = purpose
            self.input = input
            self.noteAccountID = noteAccountID
            self.trigger = trigger
            self.runID = runID
            self.modelID = modelID
            self.tools = tools
            self.timeout = timeout
            self.model = model
            self.onText = onText
            self.onTool = onTool
            self.onUsage = onUsage
        }
    }

    /// How a request ended.
    public enum Outcome: Sendable, Equatable {
        /// The result: its text (the streamed text when the result has
        /// none) and its structured_output (nil without one).
        case answered(text: String, structured: Data?)
        case failed(Failure)
        /// The user declined the consent question; nothing was sent.
        case declined
    }

    /// How long the answer is waited for.
    public nonisolated static let defaultTimeout: Duration = .seconds(120)
    /// The reason when it was not there in time.
    public nonisolated static let timedOut = "no answer in time"
    /// assistantpanel.errNoPrompter: the reason of a request without a
    /// prompter.
    public nonisolated static let noPrompter = "the assistant has no prompter"

    /// The provider in effect; nil (or a hook that answers nil) is the
    /// user's Claude Code.
    public var provider: (() -> (any AssistantProvider)?)?
    public var usesBoardConsent = false
    public var providerModelID: (() -> String)?
    /// Builds the prompts (the daemon's assistant.prompt); without one
    /// every request fails with `noPrompter`.
    public var prompter: (any AssistantPrompter)?
    /// The UI language as a BCP 47 tag ("cs"), which the model answers
    /// in; "" or "en" is English.
    public var language: @MainActor () -> String = { Assistant.uiLanguage }
    private var providerSession: (any AssistantSession)?
    private var providerTokens: [Settings.ChangeToken] = []
    public let settings: Settings
    public let locator: ClaudeCodeLocator
    private let directory: URL
    private let environment: [String: String]
    private let killGrace: Duration
    /// How long the answer is waited for (tests shorten it).
    public var timeout: Duration

    /// Asks the user before the first request ever (the panel's consent,
    /// `assistant-consent`); true allows. Without a hook nothing is ever
    /// sent.
    public var consent: (@MainActor () async -> Bool)?

    /// A request is under way.
    public private(set) var running = false

    /// Bumped by every `start` and `cancel`: the steps of an older request
    /// stop at their next `await`, its events and its end are dropped.
    private var gen = 0
    private var process: ClaudeCodeProcess?
    /// Processes that were ended and have not reported their exit yet,
    /// kept until they do.
    private var retired: [ClaudeCodeProcess] = []
    private var timer: Task<Void, Never>?
    /// The answer so far: the text blocks that are whole, and the deltas
    /// since the last of them.
    private var blocks = ""
    private var streamed = ""
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "assistant")

    /// - Parameters:
    ///   - settings: the model and the consent.
    ///   - locator: finds and asks Claude Code (the application's, shared
    ///     with the panel).
    ///   - prompter: builds the prompts (the application's
    ///     `DaemonPrompter`).
    ///   - directory: Claude Code's working directory (empty, private).
    ///   - environment: the application's environment, filtered by
    ///     `Assistant.childEnv`.
    ///   - killGrace: SIGTERM to SIGKILL (tests shorten it).
    ///   - timeout: how long the answer is waited for.
    public init(
        settings: Settings, locator: ClaudeCodeLocator, prompter: (any AssistantPrompter)? = nil,
        directory: URL = ClaudeCodeLocator.defaultDirectory,
        environment: [String: String] = ProcessInfo.processInfo.environment,
        killGrace: Duration = ClaudeCodeProcess.defaultKillGrace, timeout: Duration = AssistantRequest.defaultTimeout
    ) {
        self.settings = settings
        self.locator = locator
        self.prompter = prompter
        self.directory = directory
        self.environment = environment
        self.killGrace = killGrace
        self.timeout = timeout
        for key in Self.providerKeys {
            providerTokens.append(settings.onChange(key) { [weak self] in
                guard let self, Self.providerChangeConcernsActive(key, settings: self.settings) else { return }
                self.cancel()
            })
        }
    }

    /// Asks the assistant once without the bridge: `input` as the one turn
    /// of `purpose` (a rewrite, the writing bar, the search), with the note
    /// of `noteAccountID` (nil for none) in the system prompt and the
    /// model of `assistant-model`. A request under way is cancelled first.
    /// `onText` gets the answer's text as it streams (all of it so far);
    /// `completion` is called once with the outcome, unless the request is
    /// cancelled.
    public func start(
        purpose: PromptPurpose, input: PromptInput, noteAccountID: AccountID? = nil,
        onText: (@MainActor (String) -> Void)? = nil, completion: @escaping @MainActor (Outcome) -> Void
    ) {
        startCall(Call(purpose: purpose, input: input, noteAccountID: noteAccountID, onText: onText), completion: completion)
    }

    /// Asks the assistant once as `call` says (see `Call`); a request under
    /// way is cancelled first. `completion` is called once with the
    /// outcome, unless the request is cancelled.
    public func startCall(_ call: Call, completion: @escaping @MainActor (Outcome) -> Void) {
        cancel()
        gen += 1
        let my = gen
        running = true
        var call = call
        if let tools = call.tools {
            // The daemon's prompt fills the rest.
            call.tools = Tools(bridge: tools.bridge, socket: tools.socket)
        }
        let selected = provider?()
        Task { @MainActor [weak self] in
            await self?.run(my, call, selected, completion)
        }
    }

    /// Ends the request under way: its process is terminated and its
    /// completion never called.
    public func cancel() {
        gen += 1
        guard running else { return }
        running = false
        timer?.cancel()
        timer = nil
        retire()
    }

    // MARK: Running

    /// The prompter's parameters of `call`.
    func promptParams(_ call: Call) -> AssistantPromptParams {
        AssistantPromptParams(
            purpose: call.purpose, language: language(), accountId: call.noteAccountID, trigger: call.trigger,
            runId: call.runID, input: call.input)
    }

    /// assistantpanel.promptReason: a prompter's error as a reason to
    /// show: the daemon's message of an `RPCError` (its code is nothing to
    /// the user), else the error as it is.
    nonisolated static func promptReason(_ error: any Error) -> String {
        if let e = error as? RPCError {
            return e.message
        }
        return String(describing: error)
    }

    private func run(
        _ my: Int, _ call: Call, _ selected: (any AssistantProvider)?, _ completion: @escaping @MainActor (Outcome) -> Void
    ) async {
        let source = Assistant.sourceOf(settings.assistantProvider)
        if source != .claude, selected == nil {
            // The chosen provider is not wired (the application gives it).
            finish(my, .failed(Self.providerFailure(source == .chatgpt ? "chatgpt_unavailable" : "api_unavailable")), completion)
            return
        }
        // 1. Consent, once ever; an answer counts even when the request
        // was cancelled while the question was up.
        if let selected {
            // The board's consent: ChatGPT's own key; an `ai` account's
            // consent is one for the panel and the board, the provider's.
            let boardConsent = source == .account ? selected.hasConsent : settings.selectedBoardConsent
            if usesBoardConsent, !boardConsent {
                finish(my, .declined, completion)
                return
            }
            if !usesBoardConsent, !selected.hasConsent {
                let allowed = await consent?() ?? false
                guard my == gen else { return }
                guard allowed else {
                    finish(my, .declined, completion)
                    return
                }
                selected.acceptConsent()
                // A consent written cancels old requests through the
                // settings observer; this one goes on only while it still
                // owns the call.
                guard my == gen else { return }
            }
        } else if !settings.assistantConsent {
            let allowed = await consent?() ?? false
            if allowed {
                settings.assistantConsent = true
            }
            guard my == gen else { return }
            guard allowed else {
                finish(my, .declined, completion)
                return
            }
        }
        // 2. The daemon's prompt of the call.
        guard let prompter else {
            finish(my, .failed(.stopped(Self.noPrompter)), completion)
            return
        }
        let res: PromptResult
        do {
            res = try await prompter.prompt(promptParams(call))
        } catch {
            guard my == gen, running else { return }
            log.warning("assistant request: prompt: \(String(describing: error), privacy: .public)")
            finish(my, .failed(.stopped(Self.promptReason(error))), completion)
            return
        }
        guard my == gen, running else { return }
        if let selected {
            await launchProvider(my, call, res, selected, completion)
            return
        }
        // 3. Claude Code, signed in.
        guard let path = locator.locate() else {
            finish(my, .failed(.notFound), completion)
            return
        }
        locator.refresh()
        let signedIn = await locator.signedIn()
        guard my == gen else { return }
        if signedIn == false {
            finish(my, .failed(.notSignedIn()), completion)
            return
        }
        // 4. The process, the turn, the answer.
        let p: ClaudeCodeProcess
        do {
            p = try launch(my, path, call, res, completion)
        } catch {
            log.warning("assistant request: \(String(describing: error), privacy: .public)")
            finish(my, .failed(.stopped(String(describing: error))), completion)
            return
        }
        process = p
        guard p.send(Assistant.userMessage(res.message)) else {
            finish(my, .failed(.stopped("claude is not running")), completion)
            return
        }
        p.closeInput()
        startTimer(my, call, completion)
    }

    /// Ends request `my` as timed out when no answer came within the
    /// call's bound, or the request's own.
    private func startTimer(_ my: Int, _ call: Call, _ completion: @escaping @MainActor (Outcome) -> Void) {
        let timeout = call.timeout ?? self.timeout
        timer = Task { @MainActor [weak self] in
            try? await Task.sleep(for: timeout)
            guard !Task.isCancelled, let self, my == self.gen, self.running else { return }
            self.log.warning("assistant request: no answer in time")
            self.finish(my, .failed(.stopped(Self.timedOut)), completion)
        }
    }

    /// Step 4 with Claude Code: the process, with the daemon's prompt
    /// `res` on its command line.
    private func launch(
        _ my: Int, _ path: String, _ call: Call, _ res: PromptResult, _ completion: @escaping @MainActor (Outcome) -> Void
    ) throws -> ClaudeCodeProcess {
        try FileManager.default.createDirectory(
            at: directory, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        let options = Assistant.Options(
            bridge: call.tools?.bridge ?? "", socket: call.tools?.socket ?? "", model: call.model ?? settings.assistantModel,
            prompt: res)
        let onText = call.onText
        let onTool = call.onTool
        let onUsage = call.onUsage
        let withTools = call.tools != nil
        let p = ClaudeCodeProcess(
            executable: path, arguments: Assistant.args(options),
            environment: Assistant.childEnvironment(environment, claudePath: path), directory: directory,
            killGrace: killGrace)
        blocks = ""
        streamed = ""
        p.onEvents = { [weak self, weak p] events in
            guard let self, let p, p === self.process, my == self.gen else { return }
            for e in events {
                if e.usage != nil {
                    onUsage?(e)
                    // The handler may have cancelled the request.
                    guard p === self.process, my == self.gen else { return }
                }
                switch e.kind {
                case .systemInit:
                    if withTools, !e.bridgeConnected {
                        self.log.warning("assistant request: the malachi MCP server is not connected")
                        self.finish(my, .failed(.toolsMissing), completion)
                        return
                    }
                case .toolUse, .toolResult:
                    onTool?(e)
                    // The handler may have cancelled the request.
                    guard p === self.process, my == self.gen else { return }
                case .textDelta:
                    self.streamed += e.text
                    onText?(self.blocks + self.streamed)
                case .text:
                    self.blocks += e.text
                    self.streamed = ""
                    onText?(self.blocks)
                case .failure:
                    // Claude Code's own words for a turn the API refused,
                    // which the result repeats; a refused sign-in ends it.
                    if e.notSignedIn {
                        self.finish(my, .failed(.notSignedIn()), completion)
                        return
                    }
                case .result:
                    self.log.info("assistant request: success \(e.success, privacy: .public), cost \(e.costUSD, privacy: .public) USD")
                    let text = e.resultText.isEmpty ? self.blocks + self.streamed : e.resultText
                    let outcome: Outcome = e.success
                        ? .answered(text: text, structured: e.structured)
                        : .failed(.stopped(e.resultText))
                    self.finish(my, outcome, completion)
                    return
                default:
                    continue
                }
            }
        }
        p.onExit = { [weak self, weak p] exit in
            guard let self else { return }
            self.retired.removeAll { $0 === p }
            guard let p, p === self.process, my == self.gen else { return }
            self.log.info("assistant request: claude ended with status \(exit.status, privacy: .public)")
            self.finish(my, .failed(.stopped(exit.description)), completion)
        }
        try p.start()
        return p
    }

    /// assistantpanel.launchProvider: a one-shot request through the
    /// provider: a session opened with the call's resolved prompt `res`
    /// (the purpose's input given with the start; a provider that takes
    /// its turns by `submit` alone ignores it), its input submitted.
    private func launchProvider(
        _ my: Int, _ call: Call, _ res: PromptResult, _ provider: any AssistantProvider,
        _ completion: @escaping @MainActor (Outcome) -> Void
    ) async {
        var spec = AssistantSessionSpec(
            purpose: call.purpose, language: language(), noteAccountID: call.noteAccountID, trigger: call.trigger,
            runID: call.runID, systemPrompt: res.systemPrompt, jsonSchema: res.jsonSchemaString,
            modelID: call.modelID.isEmpty ? providerModelID?() ?? "" : call.modelID,
            timeout: call.timeout ?? timeout, boardConsent: usesBoardConsent)
        if call.purpose != .panel {
            spec.input = call.input
        }
        if let tools = call.tools {
            spec.bridgePath = tools.bridge
            spec.tools = Tools(
                bridge: tools.bridge, socket: tools.socket, bridgeArgs: res.bridgeArgs,
                allowed: Assistant.prefixTools(res.tools))
        }
        startTimer(my, call, completion)
        let withTools = call.tools != nil
        do {
            let session = try await provider.start(spec)
            guard my == gen, running else {
                session.terminate()
                return
            }
            providerSession = session
            blocks = ""
            streamed = ""
            session.onEvents = { [weak self, weak session] events in
                guard let self, let session, my == self.gen, self.running, self.providerSession === session else { return }
                for event in events {
                    if event.usage != nil {
                        call.onUsage?(event)
                        guard my == self.gen, self.running else { return }
                    }
                    switch event.kind {
                    case .systemInit:
                        if withTools, !event.bridgeConnected {
                            self.finish(my, .failed(.toolsMissing), completion)
                            return
                        }
                    case .textDelta:
                        self.streamed += event.text
                        call.onText?(self.blocks + self.streamed)
                    case .text:
                        self.blocks = event.text
                        self.streamed = ""
                        call.onText?(self.blocks)
                    case .toolUse, .toolResult:
                        call.onTool?(event)
                    case .result:
                        guard event.success else {
                            self.finish(my, .failed(Self.providerFailure(event.resultText)), completion)
                            return
                        }
                        let text = event.resultText.isEmpty ? self.blocks + self.streamed : event.resultText
                        self.finish(my, .answered(text: text, structured: event.structured), completion)
                        return
                    default:
                        break
                    }
                    guard my == self.gen, self.running, self.providerSession === session else { return }
                }
            }
            session.onExit = { [weak self, weak session] reason in
                guard let self, let session, my == self.gen, self.running, self.providerSession === session else { return }
                self.finish(my, .failed(Self.providerFailure(reason)), completion)
            }
            try await session.submit(call.input)
        } catch {
            guard my == gen, running else { return }
            finish(my, .failed(Self.providerFailure((error as? ChatGPTFailure)?.code ?? "chatgpt_request_failed")), completion)
        }
    }

    /// Ends request `my` with `outcome`, once.
    private func finish(_ my: Int, _ outcome: Outcome, _ completion: @MainActor (Outcome) -> Void) {
        guard my == gen, running else { return }
        running = false
        timer?.cancel()
        timer = nil
        retire()
        completion(outcome)
    }

    /// Terminates the request's process (after its answer it is about to
    /// end anyway) and keeps it until it reports its exit.
    private func retire() {
        let session = providerSession
        providerSession = nil
        session?.onEvents = nil
        session?.onExit = nil
        session?.terminate()
        guard let p = process else { return }
        process = nil
        p.onEvents = nil
        if p.running {
            retired.append(p)
            p.terminate()
        }
    }
}
