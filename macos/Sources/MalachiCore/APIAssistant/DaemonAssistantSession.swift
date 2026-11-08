// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/apiassistant session.go: one session of the daemon's
// assistant as the panel's protocol sees it.

import Foundation

/// apiassistant.Session: one session of the daemon's assistant
/// (assistant.sessionStart … sessionStop) as `AssistantSession`. The
/// daemon's events of the session (`handle`, from `AssistantSessions`)
/// become `Assistant.Event`s:
///
///   - init: `.systemInit` with the bridge connected and the tools;
///   - textDelta, text: `.textDelta`, `.text` (the text blocks of the turn
///     are kept for the result);
///   - toolUse, toolResult: `.toolUse` (the tool, the call's id),
///     `.toolResult` (the id, whether it failed, the bridge's text);
///   - usage: `.other` with the usage so far as the whole, final usage of
///     the turn's message (its id is the session's and the turn's number,
///     so that `Assistant.UsageTally` keeps the last value);
///   - result: `.result` with the success, the turn's text blocks joined
///     by a newline and the structured answer;
///   - failure: `.failure` with the reason "api_<class>", and, as the
///     daemon ends the turn with it, a `.result` that failed with that
///     reason (what the panel and the requests read); a class that ends
///     the session (auth, permission, toolsUnsupported, contextFull,
///     bridge, stopped) ends it here too: `onExit` with that reason.
///
/// `terminate` is assistant.sessionStop (in the background;
/// `AssistantSessions.wait` waits for it) and an exit of "api_stopped";
/// `disconnected` (the connection went) the same without the call.
/// Events and the exit that arrive before `onEvents` / `onExit` are set
/// (a turn sent with the start answers before the caller attached) are
/// kept and handed over, in order, as soon as they are. Everything runs on
/// the main actor.
@MainActor
public final class DaemonAssistantSession: AssistantSession {
    /// The daemon's session id.
    public let id: String
    /// The bare tools the daemon gave the model.
    public let tools: [String]
    /// The provider it belongs to (its client, registry and log).
    public let provider: DaemonAssistantProvider

    public var onEvents: (([Assistant.Event]) -> Void)? {
        didSet { flushQueued() }
    }
    public var onExit: ((String) -> Void)? {
        didSet { flushQueued() }
    }

    private var ended = false
    /// The first turn went with assistant.sessionStart (a one-shot
    /// purpose's input), so the first `submit` sends nothing.
    private var sent: Bool
    /// Counts the turns for the usage's message id; `blocks` are the text
    /// blocks of the turn under way.
    private var turn = 0
    private var blocks: [String] = []
    /// What arrived before the handlers were set, delivered then.
    private var queued: [Assistant.Event] = []
    private var queuedExit: String?

    init(provider: DaemonAssistantProvider, id: String, tools: [String], sent: Bool) {
        self.provider = provider
        self.id = id
        self.tools = tools
        self.sent = sent
        if sent {
            turn = 1
        }
    }

    /// apiassistant.Session.Running: the session is open.
    public var running: Bool { !ended }

    /// apiassistant.Session.Submit: sends one turn (assistant.sessionSend).
    /// The daemon's refusal is thrown as the reason
    /// `AssistantRequest.providerFailure` reads: a session it no longer has
    /// ends this one.
    public func submit(_ input: PromptInput) async throws {
        guard running else { throw ChatGPTFailure(DaemonAssistantProvider.reasonStopped) }
        if sent {
            // The start sent this turn; its events are on their way.
            sent = false
            return
        }
        turn += 1
        blocks = []
        do {
            _ = try await provider.client.call(API.AssistantSessionSend.self, AssistantSessionSendParams(sessionId: id, input: input))
        } catch {
            if let e = error as? RPCError, e.code == .assistantSessionNotFound {
                end(DaemonAssistantProvider.reasonStopped, stop: false)
            }
            throw DaemonAssistantProvider.failure(error)
        }
    }

    /// apiassistant.Session.Terminate: ends the session:
    /// assistant.sessionStop in the background (its answer is not waited
    /// for), the exit at once.
    public func terminate() {
        end(DaemonAssistantProvider.reasonStopped, stop: true)
    }

    /// apiassistant.Session.Disconnected: the connection to the daemon
    /// went, and the session with it (the daemon ends it itself).
    public func disconnected() {
        end(DaemonAssistantProvider.reasonStopped, stop: false)
    }

    /// Ends the session once with `reason`, telling the daemon when `stop`.
    private func end(_ reason: String, stop: Bool) {
        guard !ended else { return }
        ended = true
        provider.sessions?.remove(self)
        if stop {
            let sessions = provider.sessions
            let client = provider.client
            let log = provider.log
            let id = id
            sessions?.stopBegan()
            Task { @MainActor in
                defer { sessions?.stopEnded() }
                do {
                    _ = try await client.call(API.AssistantSessionStop.self, AssistantSessionStopParams(sessionId: id))
                } catch {
                    log.info("ai assistant: sessionStop failed, code \((error as? RPCError)?.code.rawValue ?? 0, privacy: .public)")
                }
            }
        }
        if let onExit {
            onExit(reason)
        } else {
            queuedExit = reason
        }
    }

    /// Hands over what arrived while nobody listened, in order: the events
    /// first, then the exit.
    private func flushQueued() {
        if !queued.isEmpty, let onEvents {
            let events = queued
            queued = []
            onEvents(events)
        }
        if queued.isEmpty, let reason = queuedExit, let onExit {
            queuedExit = nil
            onExit(reason)
        }
    }

    /// apiassistant.endsSession: whether a failure of `class` ends the
    /// session (docs/api.md §4.18 assistant.sessionSend).
    static func endsSession(_ c: AssistantFailureClass) -> Bool {
        switch c {
        case .auth, .permission, .toolsUnsupported, .contextFull, .bridge, .stopped:
            return true
        default:
            return false
        }
    }

    /// apiassistant.Session.Handle: takes one event of the session
    /// (notify.assistantEvent) and delivers what it stands for; false for
    /// another session's.
    @discardableResult
    public func handle(_ e: AssistantEvent) -> Bool {
        guard e.sessionId == id else { return false }
        let (events, exitReason) = translate(e)
        deliver(events)
        if let exitReason {
            end(exitReason, stop: false)
        }
        return true
    }

    /// apiassistant.Session.translate: the `Assistant.Event`s of `e`, and
    /// the reason the session ends with (nil when it goes on).
    func translate(_ e: AssistantEvent) -> ([Assistant.Event], String?) {
        switch e.kind {
        case .`init`:
            var out = Assistant.Event(kind: .systemInit)
            out.bridgeConnected = true
            out.tools = e.tools.isEmpty ? tools : e.tools
            return ([out], nil)
        case .textDelta:
            var out = Assistant.Event(kind: .textDelta)
            out.text = e.text
            return ([out], nil)
        case .text:
            blocks.append(e.text)
            var out = Assistant.Event(kind: .text)
            out.text = e.text
            return ([out], nil)
        case .toolUse:
            var out = Assistant.Event(kind: .toolUse)
            out.tool = e.tool
            out.toolUseID = e.id
            return ([out], nil)
        case .toolResult:
            var out = Assistant.Event(kind: .toolResult)
            out.toolUseID = e.id
            out.isError = e.isError
            out.resultText = e.text
            return ([out], nil)
        case .usage:
            guard let u = e.usage else { return ([], nil) }
            var out = Assistant.Event(kind: .other)
            out.usage = Assistant.Usage(
                inputTokens: u.inputTokens, outputTokens: u.outputTokens,
                cacheCreationInputTokens: u.cacheCreationInputTokens, cacheReadInputTokens: u.cacheReadInputTokens)
            out.usageFinal = true
            out.messageID = id + ":" + String(turn)
            return ([out], nil)
        case .result:
            var out = Assistant.Event(kind: .result)
            out.resultText = blocks.joined(separator: "\n")
            blocks = []
            if let r = e.result {
                out.success = r.success
                out.isError = !r.success
                out.structured = r.structured
            }
            return ([out], nil)
        case .failure:
            let c = e.failure?.class ?? .stopped
            let reason = DaemonAssistantProvider.reason(of: c)
            blocks = []
            var failure = Assistant.Event(kind: .failure)
            failure.failure = reason
            var result = Assistant.Event(kind: .result)
            result.success = false
            result.isError = true
            result.resultText = reason
            return ([failure, result], Self.endsSession(c) ? reason : nil)
        default:
            return ([], nil)
        }
    }

    /// Hands `events` to the handler, if any; keeps them otherwise.
    private func deliver(_ events: [Assistant.Event]) {
        guard !events.isEmpty else { return }
        if let onEvents, queued.isEmpty {
            onEvents(events)
        } else {
            queued.append(contentsOf: events)
        }
    }
}
