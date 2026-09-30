// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os

/// One question to the user's Claude Code that reads no mail: the one-shot
/// requests of ui/internal/assistant's In App target, the compose window's
/// rewrite (`ComposeRewriteController`) and the search in the user's own
/// words (`SearchConversion`). GTK ui/internal/assistantpanel `Request`
/// is its port.
///
/// It runs the panel's protocol once (`ClaudeCodeProcess`): the command
/// line of `Assistant.args` without the bridge (no MCP server, no tool),
/// with `--json-schema` when the answer has a shape, in the panel's
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
///    result within `timeout` (120 s) ends it with `.stopped` too.
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

        /// The line where the panel's errors are shown (the compose
        /// window's popover): the panel's texts, and for a missing sign-in
        /// where to sign in (a request has no Sign In… of its own).
        public var text: String {
            switch self {
            case .notFound: return Assistant.panelTexts().notFound
            case .notSignedIn: return Assistant.signInTexts().hint
            case .stopped(let reason): return Assistant.stoppedText(reason)
            }
        }

        /// The reason inside another sentence ("The search could not be
        /// converted: %s").
        public var reason: String {
            switch self {
            case .notFound: return Assistant.panelTexts().notFound
            case .notSignedIn: return Assistant.signInTexts().hint
            case .stopped(let reason): return reason
            }
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
    }

    /// Asks Claude Code once: `message` as the one turn under
    /// `systemPrompt`, with the model of the `assistant-model` setting and,
    /// when `jsonSchema` is set, that shape of answer. A request under way
    /// is cancelled first. `onText` gets the answer's text as it streams
    /// (all of it so far); `completion` is called once with the outcome,
    /// unless the request is cancelled.
    public func start(
        systemPrompt: String, message: String, jsonSchema: String = "",
        onText: (@MainActor (String) -> Void)? = nil, completion: @escaping @MainActor (Outcome) -> Void
    ) {
        cancel()
        gen += 1
        let my = gen
        running = true
        Task { @MainActor [weak self] in
            await self?.run(my, systemPrompt, message, jsonSchema, onText, completion)
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

    private func run(
        _ my: Int, _ systemPrompt: String, _ message: String, _ jsonSchema: String,
        _ onText: (@MainActor (String) -> Void)?, _ completion: @escaping @MainActor (Outcome) -> Void
    ) async {
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
            p = try launch(my, path, systemPrompt, jsonSchema, onText, completion)
        } catch {
            log.warning("assistant request: \(String(describing: error), privacy: .public)")
            finish(my, .failed(.stopped(String(describing: error))), completion)
            return
        }
        process = p
        guard p.send(Assistant.userMessage(message)) else {
            finish(my, .failed(.stopped("claude is not running")), completion)
            return
        }
        p.closeInput()
        let timeout = timeout
        timer = Task { @MainActor [weak self] in
            try? await Task.sleep(for: timeout)
            guard !Task.isCancelled, let self, my == self.gen, self.running else { return }
            self.log.warning("assistant request: no answer in time")
            self.finish(my, .failed(.stopped(Self.timedOut)), completion)
        }
    }

    private func launch(
        _ my: Int, _ path: String, _ systemPrompt: String, _ jsonSchema: String,
        _ onText: (@MainActor (String) -> Void)?, _ completion: @escaping @MainActor (Outcome) -> Void
    ) throws -> ClaudeCodeProcess {
        try FileManager.default.createDirectory(
            at: directory, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        let options = Assistant.Options(
            bridge: "", model: settings.assistantModel, systemPrompt: systemPrompt, jsonSchema: jsonSchema)
        let p = ClaudeCodeProcess(
            executable: path, arguments: Assistant.args(options),
            environment: Assistant.childEnvironment(environment, claudePath: path), directory: directory,
            killGrace: killGrace)
        blocks = ""
        streamed = ""
        p.onEvents = { [weak self, weak p] events in
            guard let self, let p, p === self.process, my == self.gen else { return }
            for e in events {
                switch e.kind {
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
        guard let p = process else { return }
        process = nil
        p.onEvents = nil
        if p.running {
            retired.append(p)
            p.terminate()
        }
    }
}
