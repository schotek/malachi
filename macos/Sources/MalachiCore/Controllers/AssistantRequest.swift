// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os

/// One question to the user's Claude Code: the one-shot requests of
/// ui/internal/assistant's In App target, the compose window's rewrite
/// (`ComposeRewriteController`) and the search in the user's own words
/// (`SearchConversion`), which read no mail, and the board's triage run
/// (`BoardTriageController`), which reads it through the bridge. GTK
/// ui/internal/assistantpanel `Request` is the port of the first two.
///
/// It runs the panel's protocol once (`ClaudeCodeProcess`): the command
/// line of `Assistant.args` without the bridge (no MCP server, no tool),
/// or with the bridge, its extra arguments and the tools of `Tools` when
/// the caller passes them, and with `--json-schema` when the answer has a
/// shape, in the panel's
/// private directory and with `Assistant.childEnv`; one
/// `Assistant.userMessage` on stdin, which is then closed; the answer is
/// the result event. The steps, each of which may end it:
///
/// 1. The first request ever asks for consent (`consent`, the panel's
///    sheet "Send Mail to Claude?" on the window that asks; the answer is
///    the shared `assistant-consent`, kept even when the request was
///    cancelled meanwhile). Declined: `.declined`, nothing is sent.
/// 2. Claude Code is located (none: `.notFound`) and must not say it is
///    signed out (`ClaudeCodeLocator.signedIn`, asked afresh:
///    `.notSignedIn`; not known counts as signed in, and the process then
///    says what is wrong: a turn the API refused for its sign-in,
///    `Assistant.Event.Kind.failure`, is `.notSignedIn` too).
/// 3. The process starts; text deltas stream to `onText` (a whole text
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
/// One request at a time: `start` cancels the one under way, and
/// `cancel()` ends it (its process terminated); a cancelled request never
/// calls its completion. Nothing is kept on disk; model text is never
/// logged.
@MainActor
public final class AssistantRequest {
    /// Why a request brought no answer.
    public enum Failure: Error, Sendable, Equatable {
        /// Claude Code was not found on this computer.
        case notFound
        /// Claude Code says it is not signed in, or the API refused its
        /// sign-in.
        case notSignedIn
        /// It ended badly; the reason is technical (the result's text or
        /// subtype, stderr's first line, the timeout, a launch failure).
        case stopped(String)
        /// A request with `Tools`: Claude Code did not report the bridge
        /// connected.
        case toolsMissing
        /// The provider refused the request because the usage limit of the
        /// user's plan was reached (ChatGPT's HTTP 429); the provider's code.
        case limit(String)

        /// The line where the panel's errors are shown (the compose
        /// window's popover): the panel's texts, and for a missing sign-in
        /// where to sign in (a request has no Sign In… of its own).
        public var text: String {
            switch self {
            case .notFound: return Assistant.panelTexts().notFound
            case .notSignedIn: return Assistant.signInTexts().hint
            case .stopped(let reason): return Assistant.stoppedText(reason)
            case .toolsMissing: return Assistant.panelTexts().toolsMissing
            case .limit: return Assistant.stoppedText(Board.Text.triageFailure(.limit))
            }
        }

        /// The reason inside another sentence ("The search could not be
        /// converted: %s").
        public var reason: String {
            switch self {
            case .notFound: return Assistant.panelTexts().notFound
            case .notSignedIn: return Assistant.signInTexts().hint
            case .stopped(let reason): return reason
            case .toolsMissing: return Assistant.panelTexts().toolsMissing
            case .limit: return Board.Text.triageFailure(.limit)
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
    /// reason as it is.
    public nonisolated static func providerFailure(_ reason: String) -> Failure {
        switch reason {
        case "codex_not_found":
            return .notFound
        case "chatgpt_not_connected", "chatgpt_reconnect_required", "chatgpt_consent_required",
             "chatgpt_permission_denied", "chatgpt_identity_mismatch":
            return .notSignedIn
        case "chatgpt_usage_limit":
            return .limit(reason)
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
            return settings.assistantProvider == .chatgpt
        case .assistantChatGPTConsentVersion:
            return settings.assistantProvider == .chatgpt && settings.assistantChatGPTConsentVersion != 1
        case .boardTriageChatGPTConsentVersion:
            return settings.assistantProvider == .chatgpt && settings.boardTriageChatGPTConsentVersion != 1
        default:
            return false
        }
    }

    /// The settings keys `providerChangeConcernsActive` looks at.
    public static let providerKeys: [Settings.Key] = [
        .assistantProvider, .assistantCodexPath, .assistantChatGPTModel, .assistantChatGPTConsentVersion,
        .boardTriageChatGPTModel, .boardTriageChatGPTConsentVersion,
    ]

    /// The bridge a request gives Claude Code, and what of it may run.
    public struct Tools: Sendable, Equatable {
        /// The path of `malachi-mcp`.
        public var bridge: String
        /// The daemon's socket (--socket); "" for the bridge's default.
        public var socket: String
        /// The bridge's further arguments (`Assistant.triageBridgeArgs`).
        public var bridgeArgs: [String]
        /// --allowedTools (`Assistant.triageTools`).
        public var allowed: [String]

        public init(bridge: String, socket: String, bridgeArgs: [String], allowed: [String]) {
            self.bridge = bridge
            self.socket = socket
            self.bridgeArgs = bridgeArgs
            self.allowed = allowed
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

    public var provider: (() -> (any AssistantProvider)?)?
    public var usesBoardConsent = false
    public var providerModelID: (() -> String)?
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
    ///   - directory: Claude Code's working directory (empty, private).
    ///   - environment: the application's environment, filtered by
    ///     `Assistant.childEnv`.
    ///   - killGrace: SIGTERM to SIGKILL (tests shorten it).
    ///   - timeout: how long the answer is waited for.
    public init(
        settings: Settings, locator: ClaudeCodeLocator, directory: URL = ClaudeCodeLocator.defaultDirectory,
        environment: [String: String] = ProcessInfo.processInfo.environment,
        killGrace: Duration = ClaudeCodeProcess.defaultKillGrace, timeout: Duration = AssistantRequest.defaultTimeout
    ) {
        self.settings = settings
        self.locator = locator
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

    /// Asks Claude Code once: `message` as the one turn under
    /// `systemPrompt`, with `model` (nil: the `assistant-model` setting,
    /// read at the start) and, when `jsonSchema` is set, that shape of
    /// answer. A request under way
    /// is cancelled first. `tools` gives Claude Code the bridge (nil: no
    /// tool at all); `timeout` replaces the request's own for this call.
    /// `onText` gets the answer's text as it streams (all of it so far),
    /// `onTool` every tool call and tool result (`Event.Kind.toolUse`,
    /// `.toolResult`), `onUsage` every event that carries usage;
    /// `completion` is called once with the outcome, unless the request is
    /// cancelled.
    public func start(
        systemPrompt: String, message: String, jsonSchema: String = "", tools: Tools? = nil, timeout: Duration? = nil,
        model: Assistant.Model? = nil, onText: (@MainActor (String) -> Void)? = nil,
        onTool: (@MainActor (Assistant.Event) -> Void)? = nil, onUsage: (@MainActor (Assistant.Event) -> Void)? = nil,
        completion: @escaping @MainActor (Outcome) -> Void
    ) {
        cancel()
        gen += 1
        let my = gen
        running = true
        let call = Call(
            systemPrompt: systemPrompt, message: message, jsonSchema: jsonSchema, tools: tools,
            timeout: timeout ?? self.timeout, model: model ?? settings.assistantModel, onText: onText, onTool: onTool,
            onUsage: onUsage)
        Task { @MainActor [weak self] in
            await self?.run(my, call, completion)
        }
    }

    /// What one `start` asked for.
    private struct Call {
        var systemPrompt: String
        var message: String
        var jsonSchema: String
        var tools: Tools?
        var timeout: Duration
        var model: Assistant.Model
        var onText: (@MainActor (String) -> Void)?
        var onTool: (@MainActor (Assistant.Event) -> Void)?
        var onUsage: (@MainActor (Assistant.Event) -> Void)?
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

    private func run(_ my: Int, _ call: Call, _ completion: @escaping @MainActor (Outcome) -> Void) async {
        if settings.assistantProvider == .chatgpt {
            guard let selected = provider?() else { finish(my, .failed(Self.providerFailure("chatgpt_unavailable")), completion); return }
            await runProvider(my, call, selected, completion)
            return
        }
        // 1. Consent, once ever; an answer counts even when the request
        // was cancelled while the question was up.
        if !settings.assistantConsent {
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
        // 2. Claude Code, signed in.
        guard let path = locator.locate() else {
            finish(my, .failed(.notFound), completion)
            return
        }
        locator.refresh()
        let signedIn = await locator.signedIn()
        guard my == gen else { return }
        if signedIn == false {
            finish(my, .failed(.notSignedIn), completion)
            return
        }
        // 3. The process, the turn, the answer.
        let p: ClaudeCodeProcess
        do {
            p = try launch(my, path, call, completion)
        } catch {
            log.warning("assistant request: \(String(describing: error), privacy: .public)")
            finish(my, .failed(.stopped(String(describing: error))), completion)
            return
        }
        process = p
        guard p.send(Assistant.userMessage(call.message)) else {
            finish(my, .failed(.stopped("claude is not running")), completion)
            return
        }
        p.closeInput()
        let timeout = call.timeout
        timer = Task { @MainActor [weak self] in
            try? await Task.sleep(for: timeout)
            guard !Task.isCancelled, let self, my == self.gen, self.running else { return }
            self.log.warning("assistant request: no answer in time")
            self.finish(my, .failed(.stopped(Self.timedOut)), completion)
        }
    }

    private func launch(
        _ my: Int, _ path: String, _ call: Call, _ completion: @escaping @MainActor (Outcome) -> Void
    ) throws -> ClaudeCodeProcess {
        try FileManager.default.createDirectory(
            at: directory, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        let options = Assistant.Options(
            bridge: call.tools?.bridge ?? "", socket: call.tools?.socket ?? "", model: call.model,
            systemPrompt: call.systemPrompt, jsonSchema: call.jsonSchema, bridgeArgs: call.tools?.bridgeArgs ?? [],
            tools: call.tools?.allowed)
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
                        self.finish(my, .failed(.notSignedIn), completion)
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

    private func runProvider(_ my: Int, _ call: Call, _ provider: any AssistantProvider,
                             _ completion: @escaping @MainActor (Outcome) -> Void) async {
        if usesBoardConsent && !settings.selectedBoardConsent { finish(my, .declined, completion); return }
        if !usesBoardConsent && !provider.hasConsent {
            let allowed = await consent?() ?? false
            guard my == gen else { return }
            guard allowed else { finish(my, .declined, completion); return }
            provider.acceptConsent()
            // Consent changes cancel old requests through the settings observer;
            // this request continues only if its generation still owns the call.
            guard my == gen else { return }
        }
        do {
            let session = try await provider.start(AssistantSessionSpec(systemPrompt: call.systemPrompt,
                jsonSchema: call.jsonSchema, tools: call.tools, modelID: providerModelID?() ?? settings.assistantChatGPTModel,
                timeout: call.timeout, boardConsent: usesBoardConsent))
            guard my == gen else { session.terminate(); return }
            providerSession = session; blocks = ""; streamed = ""
            session.onEvents = { [weak self] events in
                guard let self, my == self.gen, self.running else { return }
                for event in events {
                    if event.usage != nil { call.onUsage?(event) }
                    switch event.kind {
                    case .textDelta: self.streamed += event.text; call.onText?(self.blocks + self.streamed)
                    case .text: self.blocks = event.text; self.streamed = ""; call.onText?(self.blocks)
                    case .toolUse, .toolResult: call.onTool?(event)
                    case .result:
                        self.finish(my, event.success ? .answered(text: event.resultText, structured: event.structured)
                            : .failed(Self.providerFailure(event.resultText)), completion)
                    default: break
                    }
                    guard my == self.gen, self.running else { return }
                }
            }
            session.onExit = { [weak self] reason in self?.finish(my, .failed(Self.providerFailure(reason)), completion) }
            try await session.submit(call.message)
        } catch {
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
        let session = providerSession; providerSession = nil
        session?.onEvents = nil; session?.onExit = nil; session?.terminate()
        guard let p = process else { return }
        process = nil
        p.onEvents = nil
        if p.running {
            retired.append(p)
            p.terminate()
        }
    }
}
