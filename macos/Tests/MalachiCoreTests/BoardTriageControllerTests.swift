// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The board's triage run (`BoardTriageController`) against the fake daemon
// of BoardPreferencesControllerTests (`TriageDaemon`: preferences,
// board.runStart, board.runEnd) and the stand-in `claude` of
// AssistantPanelControllerTests (`FakeClaude`). No real Claude Code, daemon
// or bridge is ever run: the bridge's path is only passed on.

/// Switches the stand-ins read from any thread.
private final class Flags: @unchecked Sendable {
    var found = true
    var available = true
}

private let t0 = Date(timeIntervalSince1970: 1_790_848_800)

/// An annotate_case the bridge accepted (`ok`) or refused.
private func annotate(_ id: String, ok: Bool = true) -> [String] {
    [fakeToolUse(id, "annotate_case"), fakeToolResult(id, ok ? "annotated" : "conflict", error: !ok)]
}

/// An annotate_case call in an API message `msg` that reports `input`
/// input and `read` cache-read tokens (output 1, cache writes 10), with
/// the bridge's acceptance.
private func annotateUsing(_ id: String, msg: String, input: Int, read: Int) -> [String] {
    [#"{"type":"assistant","message":{"id":"\#(msg)","role":"assistant","content":[{"type":"tool_use","id":"\#(id)","name":"mcp__malachi__annotate_case","input":{}}],"usage":{"input_tokens":\#(input),"output_tokens":1,"cache_creation_input_tokens":10,"cache_read_input_tokens":\#(read)}},"parent_tool_use_id":null}"#,
     fakeToolResult(id, "annotated")]
}

/// The `--model` of a command line.
private func model(_ args: [String]) -> String? {
    args.firstIndex(of: "--model").flatMap { $0 + 1 < args.count ? args[$0 + 1] : nil }
}

/// A result line with its usage; `success` false is an error result.
private func resultUsing(input: Int, output: Int, write: Int, read: Int, success: Bool = true) -> String {
    #"{"type":"result","subtype":"\#(success ? "success" : "error_during_execution")","is_error":\#(!success),"result":"ok","usage":{"input_tokens":\#(input),"output_tokens":\#(output),"cache_creation_input_tokens":\#(write),"cache_read_input_tokens":\#(read)}}"#
}

@MainActor
private final class Harness {
    let daemon: TriageDaemon
    let scratch = ScratchSettings()
    let fake: FakeClaude
    let flags = Flags()
    let prefs: BoardPreferencesController
    let controller: BoardTriageController
    var states: [Board.TriageState] = []
    var refreshes = 0
    var consentAsked = 0
    var ends: [(Board.TriageTrigger, Board.TriageFailure?)] = []
    private var tokens: [BoardObserverToken] = []

    init(
        fake: FakeClaude, consent: Bool = true, prefs: BoardPreferences = BoardPreferences(assistant: true),
        bridge: String? = "/b/malachi-mcp", load: Bool = true
    ) async throws {
        self.fake = fake
        daemon = try await TriageDaemon()
        await daemon.script.set(prefs: prefs)
        let s = scratch.settings
        s.assistantClaudePath = fake.path
        s.assistantConsent = consent
        s.boardTriageConsent = consent
        // The panel's model and the board's apart: the run takes the board's.
        s.assistantModel = .haiku
        s.boardTriageModel = .opus
        let prefix = fake.dir.path + "/"
        let flags = flags
        let locator = ClaudeCodeLocator(
            settings: s, environment: ["HOME": fake.dir.path], timeout: .seconds(5),
            usable: { flags.found && $0.hasPrefix(prefix) && ClaudeCodeLocator.isExecutableFile($0) })
        let request = AssistantRequest(
            settings: s, locator: locator, directory: fake.dir.appendingPathComponent("work"),
            environment: ["HOME": fake.dir.path], killGrace: .milliseconds(300), timeout: .seconds(10))
        self.prefs = BoardPreferencesController(client: daemon.client)
        controller = BoardTriageController(
            client: daemon.client, settings: s, locator: locator, preferences: self.prefs, request: request,
            bridge: bridge, socket: "/s.sock", available: { flags.available }, now: { t0 }, language: { "Czech" },
            today: { "2026-10-01" })
        controller.timeout = .seconds(10)
        controller.onRefresh = { [unowned self] in self.refreshes += 1 }
        tokens.append(controller.observe { [unowned self] in
            if self.states.last != self.controller.state {
                self.states.append(self.controller.state)
            }
        })
        tokens.append(controller.observeEnded { [unowned self] in
            if let e = self.controller.lastEnded {
                self.ends.append((e.trigger, e.failure))
            }
        })
        if load {
            _ = await self.prefs.loadNow()
            try await triageWait { self.prefs.isIdle }
        }
    }

    /// Waits until a run is running.
    func running() async throws {
        try await triageWait { if case .running = self.controller.state { return true } else { return false } }
    }

    /// The board reported `queue` cases for the assistant.
    func board(queue: Int, assistantOn: Bool = true, annotatedToday: Int = 0, run: Board.Run? = nil) {
        controller.boardChanged(Board.Snapshot(
            annotated: assistantOn, run: run, phase: .ready,
            triage: Board.Triage(queue: queue, annotatedToday: annotatedToday)))
    }

    /// Waits until the run ended and its board.runEnd was answered.
    func ended() async throws {
        try await triageWait { !self.controller.state.isActive && self.controller.isIdle }
    }

    func stop() async {
        controller.cancel()
        for t in tokens {
            t.cancel()
        }
        await daemon.stop()
    }
}

@MainActor
@Suite(.serialized) struct BoardTriageControllerTests {
    // MARK: A run

    /// A manual run: runStart, the request with the bridge for the run,
    /// progress from the accepted annotate_case calls, runEnd, a refresh.
    @Test func manualRun() async throws {
        let fake = try FakeClaude(turns: [FakeTurn(lines: [fakeInit, fakeToolUse("q", "list_triage_queue"), fakeToolResult("q", "3 cases")]
            + annotate("a1") + annotate("a2", ok: false) + annotate("a3") + [fakeText("Done."), fakeResult("Done.")])])
        let h = try await Harness(fake: fake)
        h.board(queue: 3)
        #expect(h.controller.start(.manual))
        #expect(h.controller.state == .starting(.manual))
        try await h.ended()
        #expect(h.controller.state == .finished(.manual, annotated: 2, refused: 1, at: t0))
        #expect(h.states.filter { $0 != .idle } == [
            .starting(.manual), .running(.manual, done: 0, total: 3), .running(.manual, done: 1, total: 3),
            .running(.manual, done: 2, total: 3), .finished(.manual, annotated: 2, refused: 1, at: t0),
        ])
        #expect(h.controller.view.result
            == "Triage finished: 2 conversations refined. The board refused 1 of the assistant’s notes.")
        let starts = await h.daemon.script.runStarts
        #expect(starts == [BoardRunStartParams(trigger: .manual, source: "claude-code")])
        #expect(await h.daemon.script.runEnds == [BoardRunEndParams(runId: "run_1", error: nil)])
        #expect(h.refreshes == 1)
        #expect(h.ends.count == 1 && h.ends[0].0 == .manual && h.ends[0].1 == nil)
        // The command line: the bridge for this run and its limit, the
        // triage's tools with create_draft.
        #expect(fake.args == Assistant.args(Assistant.Options(
            bridge: "/b/malachi-mcp", socket: "/s.sock", model: .opus,
            systemPrompt: Assistant.triageSystemPrompt(language: "Czech", today: "2026-10-01"),
            bridgeArgs: ["--allow-triage", "--triage-run", "run_1", "--triage-max", "40"],
            tools: Assistant.triageTools)))
        #expect(fake.args.joined(separator: " ").contains("mcp__malachi__create_draft"))
        #expect(fake.prompts == [Assistant.triageMessage(maxCases: 40)])
        #expect(h.controller.signedIn == true)
        // Nothing of consent was asked or written.
        #expect(await h.daemon.script.sets.isEmpty)
        await h.stop()
    }

    /// An automatic run asks for its limit; the progress counts against the
    /// smaller of the queue and the limit. It gets no create_draft and is
    /// told so.
    @Test func automaticRun() async throws {
        let fake = try FakeClaude(turns: [FakeTurn(lines: [fakeInit] + annotate("a1") + [fakeResult("ok")])])
        let h = try await Harness(fake: fake)
        h.board(queue: 12)
        #expect(h.controller.start(.automatic, limit: 5))
        try await h.ended()
        #expect(h.states.contains(.running(.automatic, done: 0, total: 5)))
        #expect(h.controller.state == .finished(.automatic, annotated: 1, at: t0))
        #expect(await h.daemon.script.runStarts == [BoardRunStartParams(trigger: .auto, source: "claude-code")])
        #expect(fake.prompts == [Assistant.triageMessage(maxCases: 5, drafts: false)])
        #expect(fake.args == Assistant.args(Assistant.Options(
            bridge: "/b/malachi-mcp", socket: "/s.sock", model: .opus,
            systemPrompt: Assistant.triageSystemPrompt(language: "Czech", today: "2026-10-01"),
            bridgeArgs: ["--allow-triage", "--triage-run", "run_1", "--triage-max", "5"],
            tools: Assistant.triageTools(drafts: false))))
        #expect(!fake.args.joined(separator: " ").contains("create_draft"))
        await h.stop()
    }

    /// The run's limit is a hard one: once the accepted notes reach it the
    /// run has succeeded, whatever the model does next (later tool calls
    /// are not counted); without a result in the grace period it ends so.
    @Test func limitEndsTheRun() async throws {
        let fake = try FakeClaude(turns: [FakeTurn(
            lines: [fakeInit] + annotate("a1") + annotate("a2", ok: false) + annotate("a3") + annotate("a4")
                + annotate("a5", ok: false),
            shell: "sleep 30")])
        let h = try await Harness(fake: fake)
        h.controller.grace = .milliseconds(300)
        h.board(queue: 12)
        #expect(h.controller.start(.automatic, limit: 2))
        try await h.ended()
        #expect(h.controller.state == .finished(.automatic, annotated: 2, refused: 1, at: t0))
        #expect(!h.states.contains { if case .running(_, 3, _) = $0 { return true } else { return false } })
        #expect(await h.daemon.script.runEnds == [BoardRunEndParams(runId: "run_1", error: nil)])
        #expect(h.ends.count == 1 && h.ends[0].1 == nil)
        #expect(h.controller.signedIn == true)
        await h.stop()
    }

    /// The run takes the board's own model, not the panel's, read when it
    /// starts: a change applies to the next run, not to the one under way.
    @Test func boardsOwnModel() async throws {
        let fake = try FakeClaude(turns: [FakeTurn(lines: [fakeInit], shell: "sleep 0.3; echo '\(fakeResult("ok"))'")])
        let h = try await Harness(fake: fake)
        h.board(queue: 3)
        #expect(h.scratch.settings.assistantModel == .haiku)
        h.controller.start(.manual)
        try await triageWait { fake.starts == 1 && !fake.args.isEmpty }
        h.scratch.settings.boardTriageModel = .sonnet
        try await h.ended()
        #expect(model(fake.args) == "opus")
        #expect(h.controller.state == .finished(.manual, annotated: 0, at: t0))
        h.controller.start(.automatic, limit: 3)
        try await h.ended()
        #expect(fake.starts == 2)
        #expect(model(fake.args) == "sonnet")
        #expect(h.scratch.settings.assistantModel == .haiku)
        await h.stop()
    }

    /// A run that ends with no note accepted: refused notes fail it (both
    /// triggers); an automatic run whose queue had cases and that tried
    /// nothing fails as no progress (the schedule backs off); a manual run
    /// that tried nothing has simply finished.
    @Test func emptyRuns() async throws {
        let cases: [(String, Board.TriageTrigger, [String], Board.TriageState)] = [
            ("refused, automatic", .automatic, annotate("a1", ok: false) + annotate("a2", ok: false),
             .failed(.automatic, .notesRefused, at: t0)),
            ("refused, manual", .manual, annotate("a1", ok: false), .failed(.manual, .notesRefused, at: t0)),
            ("nothing, automatic", .automatic, [], .failed(.automatic, .noProgress, at: t0)),
            ("nothing, manual", .manual, [], .finished(.manual, annotated: 0, at: t0)),
        ]
        for (name, trigger, lines, end) in cases {
            let h = try await Harness(fake: try FakeClaude(turns: [FakeTurn(lines: [fakeInit] + lines + [fakeResult("ok")])]))
            h.board(queue: 3)
            h.controller.start(trigger, limit: 3)
            try await h.ended()
            #expect(h.controller.state == end, "\(name)")
            let error: BoardRunError? = if case .failed = end { .failed } else { nil }
            #expect(await h.daemon.script.runEnds == [BoardRunEndParams(runId: "run_1", error: error)], "\(name)")
            if case .failed(_, let f, _) = end {
                #expect(BoardAutoTriageScheduler.countsAsFailure(f), "\(name)")
            }
            await h.stop()
        }
        #expect(Board.Text.triageFailed(.notesRefused) == "Triage failed: the board refused the assistant’s notes.")
        #expect(Board.Text.triageFailed(.noProgress) == "Triage failed: the assistant added no notes.")
    }

    /// A queue the board has not reported with the assistant on is not
    /// known: the run asks for its limit and counts against it.
    @Test func unknownQueue() async throws {
        let fake = try FakeClaude(turns: [FakeTurn(lines: [fakeInit, fakeResult("ok")])])
        let h = try await Harness(fake: fake)
        h.board(queue: 0, assistantOn: false)
        #expect(h.controller.start(.manual))
        try await h.ended()
        #expect(h.states.contains(.running(.manual, done: 0, total: 40)))
        #expect(h.controller.state == .finished(.manual, annotated: 0, at: t0))
        await h.stop()
    }

    /// One run at a time.
    @Test func oneRunAtATime() async throws {
        let fake = try FakeClaude(turns: [FakeTurn(lines: [fakeInit] + annotate("a1"), shell: "sleep 30")])
        let h = try await Harness(fake: fake)
        h.board(queue: 2)
        #expect(h.controller.start(.manual))
        #expect(!h.controller.start(.manual))
        #expect(!h.controller.start(.automatic, limit: 3))
        try await triageWait { h.controller.state == .running(.manual, done: 1, total: 2) }
        #expect(!h.controller.start(.manual))
        #expect(await h.daemon.script.runStarts.count == 1)
        #expect(fake.starts == 1)
        await h.stop()
    }

    /// Stop: the request ends, the run is recorded as cancelled, the board
    /// asked again; nothing of the request comes later.
    @Test func cancel() async throws {
        let fake = try FakeClaude(turns: [FakeTurn(lines: [fakeInit] + annotate("a1"), shell: "sleep 30; echo '\(fakeResult("late"))'")])
        let h = try await Harness(fake: fake)
        h.board(queue: 4)
        h.controller.start(.manual)
        try await triageWait { h.controller.state == .running(.manual, done: 1, total: 4) }
        h.controller.cancel()
        #expect(h.controller.state == .failed(.manual, .cancelled, at: t0))
        try await h.ended()
        #expect(await h.daemon.script.runEnds == [BoardRunEndParams(runId: "run_1", error: .cancelled)])
        #expect(h.refreshes == 1)
        try await Task.sleep(for: .milliseconds(200))
        #expect(h.controller.state == .failed(.manual, .cancelled, at: t0))
        #expect(h.ends.count == 1)
        await h.stop()
    }

    /// Cancelled while board.runStart is on its way: the run it started is
    /// ended as cancelled, and no request starts.
    @Test func cancelWhileStarting() async throws {
        let fake = try FakeClaude(turns: [answerTurn("x")])
        let h = try await Harness(fake: fake)
        h.board(queue: 1)
        await h.daemon.script.hold(runStarts: true)
        h.controller.start(.manual)
        try await triageWait { await h.daemon.script.waitingRunStarts == 1 }
        h.controller.cancel()
        await h.daemon.script.hold(runStarts: false)
        try await triageWait { await h.daemon.script.runEnds.count == 1 }
        #expect(await h.daemon.script.runEnds == [BoardRunEndParams(runId: "run_1", error: .cancelled)])
        #expect(h.controller.state == .failed(.manual, .cancelled, at: t0))
        try await Task.sleep(for: .milliseconds(100))
        #expect(fake.starts == 0)
        await h.stop()
    }

    // MARK: Failures

    /// What fails before the daemon's run starts records no run.
    @Test func failuresBeforeTheRun() async throws {
        // Claude Code not found.
        let h1 = try await Harness(fake: try FakeClaude(turns: [answerTurn("x")]))
        h1.flags.found = false
        h1.board(queue: 1)
        h1.controller.start(.manual)
        try await h1.ended()
        #expect(h1.controller.state == .failed(.manual, .notFound, at: t0))
        #expect(await h1.daemon.script.runStarts.isEmpty)
        await h1.stop()
        // Signed out.
        let h2 = try await Harness(fake: try FakeClaude(loggedIn: "false", turns: [answerTurn("x")]))
        h2.board(queue: 1)
        h2.controller.start(.automatic, limit: 3)
        try await h2.ended()
        #expect(h2.controller.state == .failed(.automatic, .notSignedIn, at: t0))
        #expect(h2.controller.signedIn == false)
        #expect(await h2.daemon.script.runStarts.isEmpty)
        #expect(h2.fake.starts == 0)
        await h2.stop()
        // No bridge.
        let h3 = try await Harness(fake: try FakeClaude(turns: [answerTurn("x")]), bridge: nil)
        h3.controller.start(.manual)
        try await h3.ended()
        #expect(h3.controller.state == .failed(.manual, .toolsMissing, at: t0))
        await h3.stop()
        // The assistant off.
        let h4 = try await Harness(fake: try FakeClaude(turns: [answerTurn("x")]))
        h4.flags.available = false
        h4.controller.start(.manual)
        try await h4.ended()
        #expect(h4.controller.state == .failed(.manual, .assistantOff, at: t0))
        await h4.stop()
        // Nothing waits.
        let h5 = try await Harness(fake: try FakeClaude(turns: [answerTurn("x")]))
        h5.board(queue: 0)
        h5.controller.start(.manual)
        try await h5.ended()
        #expect(h5.controller.state == .failed(.manual, .nothingToDo, at: t0))
        #expect(await h5.daemon.script.runStarts.isEmpty)
        await h5.stop()
        // The daemon refuses the run.
        let h6 = try await Harness(fake: try FakeClaude(turns: [answerTurn("x")]))
        await h6.daemon.script.set(runStartFailure: RPCError(code: .storageError, message: "disk"))
        h6.board(queue: 1)
        h6.controller.start(.manual)
        try await h6.ended()
        #expect(h6.controller.state == .failed(.manual, .backend, at: t0))
        #expect(await h6.daemon.script.runEnds.isEmpty)
        #expect(h6.fake.starts == 0)
        await h6.stop()
    }

    /// What fails during the run is recorded with its class.
    @Test func failuresDuringTheRun() async throws {
        let cases: [(String, FakeTurn, Board.TriageFailure, BoardRunError)] = [
            ("the bridge not connected", FakeTurn(lines: [fakeInitFailed, fakeResult("x")]), .toolsMissing, .failed),
            ("an error result", FakeTurn(lines: [fakeInit, fakeResult("error_max_turns", success: false)]), .stopped, .failed),
            ("Claude Code exits", FakeTurn(lines: [fakeInit], shell: "exit 3"), .stopped, .failed),
            ("the API refused the sign-in",
             FakeTurn(lines: [fakeInit, fakeFailure("authentication_failed", "Please run /login"), fakeResult("x", success: false)]),
             .notSignedIn, .signedOut),
            ("too long", FakeTurn(lines: [fakeInit], shell: "sleep 30"), .timeout, .timeout),
        ]
        for (name, turn, failure, runError) in cases {
            let h = try await Harness(fake: try FakeClaude(turns: [turn]))
            // Only the run meant to time out gets a short timeout: a slow
            // start of the fake must not decide the others.
            if failure == .timeout {
                h.controller.timeout = .milliseconds(500)
            }
            h.board(queue: 2)
            h.controller.start(.automatic, limit: 2)
            try await h.ended()
            #expect(h.controller.state == .failed(.automatic, failure, at: t0), "\(name)")
            #expect(await h.daemon.script.runEnds == [BoardRunEndParams(runId: "run_1", error: runError)], "\(name)")
            #expect(h.refreshes == 1, "\(name)")
            await h.stop()
        }
    }

    // MARK: Consent

    /// Without consent a manual run asks; declined, nothing is written or
    /// started; allowed, both consents are kept and the assistant
    /// preference goes on before the run.
    @Test func consent() async throws {
        let fake = try FakeClaude(turns: [FakeTurn(lines: [fakeInit, fakeResult("ok")])])
        let h = try await Harness(fake: fake, consent: false, prefs: BoardPreferences())
        #expect(h.controller.needsConsent && !h.controller.consentGiven)
        #expect(h.controller.view.needsConsent)
        // No sheet: declined.
        h.controller.start(.manual)
        try await h.ended()
        #expect(h.controller.state == .failed(.manual, .declined, at: t0))
        // Declined in the sheet.
        h.controller.consent = { [unowned h] in
            h.consentAsked += 1
            return false
        }
        h.controller.start(.manual)
        try await h.ended()
        #expect(h.consentAsked == 1)
        #expect(h.controller.state == .failed(.manual, .declined, at: t0))
        #expect(!h.scratch.settings.boardTriageConsent && !h.scratch.settings.assistantConsent)
        #expect(await h.daemon.script.sets.isEmpty)
        #expect(await h.daemon.script.runStarts.isEmpty)
        // An automatic run never asks.
        h.controller.start(.automatic, limit: 3)
        try await h.ended()
        #expect(h.consentAsked == 1)
        #expect(h.controller.state == .failed(.automatic, .declined, at: t0))
        // Allowed.
        h.controller.consent = { [unowned h] in
            h.consentAsked += 1
            return true
        }
        h.controller.start(.manual)
        try await h.ended()
        #expect(h.consentAsked == 2)
        #expect(h.scratch.settings.boardTriageConsent && h.scratch.settings.assistantConsent)
        #expect(await h.daemon.script.sets == [BoardPreferences(assistant: true)])
        #expect(h.controller.consentGiven && !h.controller.needsConsent)
        #expect(h.controller.state == .finished(.manual, annotated: 0, at: t0))
        #expect(await h.daemon.script.runStarts.count == 1)
        await h.stop()
    }

    /// The panel's consent alone is not enough, nor the keys without the
    /// board's assistant preference.
    @Test func whatConsentNeeds() async throws {
        let h = try await Harness(fake: try FakeClaude(turns: [answerTurn("x")]), consent: true, prefs: BoardPreferences())
        #expect(h.controller.needsConsent)
        await h.prefs.update { $0.assistant = true }
        #expect(h.controller.consentGiven)
        h.scratch.settings.boardTriageConsent = false
        #expect(h.controller.needsConsent)
        await h.stop()
    }

    /// Withdrawing stops a run, drops the board's consent and turns the
    /// assistant preference off; the panel's consent stays.
    @Test func withdraw() async throws {
        let fake = try FakeClaude(turns: [FakeTurn(lines: [fakeInit], shell: "sleep 30")])
        let h = try await Harness(fake: fake)
        h.board(queue: 2)
        h.controller.start(.manual)
        try await triageWait { if case .running = h.controller.state { return true } else { return false } }
        h.controller.withdrawConsent()
        #expect(h.controller.state == .failed(.manual, .cancelled, at: t0))
        #expect(!h.scratch.settings.boardTriageConsent && h.scratch.settings.assistantConsent)
        try await triageWait { await h.daemon.script.sets.count == 1 && h.prefs.isIdle }
        #expect(await h.daemon.script.sets == [BoardPreferences(assistant: false)])
        #expect(h.controller.needsConsent)
        try await h.ended()
        await h.stop()
    }

    /// Withdrawing also turns automatic triage off: a consent given again
    /// later does not bring back runs the user did not turn on again.
    @Test func withdrawTurnsAutomaticTriageOff() async throws {
        let h = try await Harness(
            fake: try FakeClaude(turns: [answerTurn("x")]), prefs: BoardPreferences(assistant: true, autoTriage: true))
        h.controller.withdrawConsent()
        try await triageWait { await h.daemon.script.sets.count == 1 && h.prefs.isIdle }
        #expect(await h.daemon.script.sets == [BoardPreferences(assistant: false, autoTriage: false)])
        #expect(h.prefs.preferences?.autoTriage == false && !h.controller.wantsBoardData)
        await h.stop()
    }

    /// A board the daemon does not have, or has turned off, hides the
    /// triage; a board that lists again brings it back.
    @Test func boardPhaseHidesTheTriage() async throws {
        let h = try await Harness(fake: try FakeClaude(turns: [answerTurn("x")]))
        #expect(h.controller.view.offered)
        h.controller.boardChanged(Board.Snapshot(phase: .unsupported))
        #expect(h.controller.boardPhase == .unsupported && !h.controller.view.offered)
        // Transient phases change nothing.
        h.controller.boardChanged(Board.Snapshot(phase: .unavailable))
        #expect(!h.controller.view.offered)
        h.board(queue: 1)
        #expect(h.controller.boardPhase == .ready && h.controller.view.offered)
        // Turned off in the daemon's preferences: hidden at once.
        await h.prefs.update { $0.enabled = false }
        #expect(!h.controller.view.offered)
        await h.prefs.update { $0.enabled = true }
        #expect(h.controller.view.offered)
        // A snapshot of the board off, with preferences that say it is on
        // again (newer), does not hide it.
        h.controller.boardChanged(Board.Snapshot(phase: .off))
        #expect(h.controller.view.offered)
        await h.stop()
    }

    // MARK: The schedule's view of it

    /// The inputs the schedule reads.
    @Test func autoTriageInputs() async throws {
        let h = try await Harness(
            fake: try FakeClaude(turns: [answerTurn("x")]),
            prefs: BoardPreferences(assistant: true, autoTriage: true, autoTriageMinutes: 45, autoTriageDailyCases: 7))
        let run = Board.Run(model: "claude-code", date: t0, trigger: "auto", started: t0.addingTimeInterval(-60))
        h.board(queue: 3, annotatedToday: 2, run: run)
        let rev = h.controller.boardRevision
        h.controller.checkSignIn()
        try await triageWait { h.controller.signedIn == true }
        let i = h.controller.autoTriageInputs
        #expect(i.enabled && i.available && i.signedIn == true && i.consent && !i.running)
        #expect(i.queue == 3 && i.annotatedToday == 2 && i.countedAt == t0)
        #expect(i.minutes == 45 && i.dailyCap == 7)
        #expect(i.lastAttempt == t0.addingTimeInterval(-60))
        // The same board again is no new revision; another queue is.
        h.board(queue: 3, annotatedToday: 2, run: run)
        #expect(h.controller.boardRevision == rev)
        h.board(queue: 4, annotatedToday: 2, run: run)
        #expect(h.controller.boardRevision == rev + 1)
        // A manual last run is no automatic attempt; a board that could not
        // be listed changes nothing.
        h.board(queue: 4, run: Board.Run(model: "m", date: t0, trigger: "manual", started: t0))
        #expect(h.controller.autoTriageInputs.lastAttempt == nil)
        h.controller.boardChanged(Board.Snapshot(phase: .unavailable))
        #expect(h.controller.board.queue == 4)
        // With the assistant off the queue counts as empty.
        h.board(queue: 4, assistantOn: false)
        #expect(h.controller.autoTriageInputs.queue == 0)
        // Only this application's own run counts (the Go and C# rule): one
        // the daemon reports open (another client's) does not.
        h.board(queue: 4, run: Board.Run(model: "m", date: t0, running: true, trigger: "external", started: t0))
        #expect(!h.controller.autoTriageInputs.running)
        h.board(queue: 4, run: Board.Run(model: "m", date: t0, running: false, trigger: "external", started: t0))
        #expect(!h.controller.autoTriageInputs.running)
        await h.stop()
    }

    /// The schedule with the real controller: one automatic run after the
    /// board's debounce, recorded as auto.
    @Test func scheduledRun() async throws {
        let fake = try FakeClaude(turns: [FakeTurn(lines: [fakeInit] + annotate("a1") + [fakeResult("ok")])])
        let h = try await Harness(fake: fake, prefs: BoardPreferences(assistant: true, autoTriage: true))
        h.controller.checkSignIn()
        try await triageWait { h.controller.signedIn == true }
        let clock = TriageClock()
        let scheduler = BoardAutoTriageScheduler(target: h.controller, sleep: { await clock.sleep($0) }, now: { t0 })
        scheduler.start()
        #expect(scheduler.decision == .off(.emptyQueue))
        h.board(queue: 2)
        #expect(scheduler.debouncing)
        try await triageWait { await clock.count == 2 } // the debounce and the heartbeat
        await clock.fire()
        try await triageWait { h.controller.state == .finished(.automatic, annotated: 1, at: t0) && h.controller.isIdle }
        #expect(await h.daemon.script.runStarts == [BoardRunStartParams(trigger: .auto, source: "claude-code")])
        #expect(fake.prompts == [Assistant.triageMessage(maxCases: 40, drafts: false)])
        #expect(scheduler.lastAttempt == t0 && scheduler.failures == 0)
        scheduler.stop()
        await clock.fire()
        await h.stop()
    }

    // MARK: Losing what a run needs

    /// A run under way stops when triage stops being available, a consent
    /// goes, the board or its assistant preference goes off, and (an
    /// automatic one) when automatic triage is switched off; a manual run
    /// does not care about the switch.
    @Test func losingWhatItNeeds() async throws {
        let auto = BoardPreferences(assistant: true, autoTriage: true)
        let cases: [(String, Board.TriageTrigger, Bool, @MainActor (Harness) async -> Void)] = [
            ("not available", .automatic, true, { h in
                h.flags.available = false
                h.controller.availabilityChanged()
            }),
            ("the panel's consent withdrawn", .manual, true, { h in h.scratch.settings.assistantConsent = false }),
            ("the board's consent withdrawn", .automatic, true, { h in h.scratch.settings.boardTriageConsent = false }),
            ("the assistant preference off", .manual, true, { h in h.prefs.update({ $0.assistant = false }, completion: nil) }),
            ("the board off", .manual, true, { h in h.prefs.update({ $0.enabled = false }, completion: nil) }),
            ("automatic triage off", .automatic, true, { h in h.prefs.update({ $0.autoTriage = false }, completion: nil) }),
            ("automatic triage off, a manual run", .manual, false, { h in
                h.prefs.update({ $0.autoTriage = false }, completion: nil)
            }),
        ]
        for (name, trigger, stops, change) in cases {
            let h = try await Harness(fake: try FakeClaude(turns: [FakeTurn(lines: [fakeInit], shell: "sleep 30")]), prefs: auto)
            h.board(queue: 2)
            h.controller.start(trigger, limit: 2)
            try await h.running()
            await change(h)
            if stops {
                #expect(h.controller.state == .failed(trigger, .cancelled, at: t0), "\(name)")
                try await h.ended()
                #expect(await h.daemon.script.runEnds == [BoardRunEndParams(runId: "run_1", error: .cancelled)], "\(name)")
            } else {
                try await triageWait { h.prefs.isIdle }
                #expect(h.controller.state.isActive, "\(name)")
            }
            await h.stop()
        }
    }

    // MARK: The In App target

    /// Triage needs the In App target (`triageNeedsInAppTarget`): with
    /// another target the control is hidden and nothing can run; a change
    /// of the target is reported.
    @Test func needsTheInAppTarget() async throws {
        #expect(BoardTriageController.triageNeedsInAppTarget)
        #expect(BoardTriageController.triageAvailable(shown: true, target: .app))
        #expect(!BoardTriageController.triageAvailable(shown: true, target: .desktop))
        #expect(!BoardTriageController.triageAvailable(shown: true, target: .code))
        #expect(!BoardTriageController.triageAvailable(shown: false, target: .app))

        let fake = try FakeClaude(turns: [answerTurn("x")])
        let d = try await TriageDaemon()
        let scratch = ScratchSettings()
        let s = scratch.settings
        s.assistantClaudePath = fake.path
        s.assistantMenu = true
        let prefix = fake.dir.path + "/"
        let locator = ClaudeCodeLocator(
            settings: s, environment: ["HOME": fake.dir.path], timeout: .seconds(5),
            usable: { $0.hasPrefix(prefix) && ClaudeCodeLocator.isExecutableFile($0) })
        let assistant = AssistantController(bridge: nil, settings: s, locator: locator) { _ in false }
        assistant.apply(MCPStatus(command: "/b/malachi-mcp", clients: [
            MCPClient(id: "claude-code", name: "Claude Code", present: true, registered: true),
        ]))
        #expect(assistant.shown)
        let c = BoardTriageController(
            client: d.client, settings: s, locator: locator, preferences: BoardPreferencesController(client: d.client),
            assistant: assistant, bridge: "/b/malachi-mcp", socket: "/s.sock")
        var reports = 0
        let token = c.observe { reports += 1 }
        s.assistantTarget = .desktop
        #expect(c.view.control == .hidden && !c.canRun)
        s.assistantTarget = .app
        #expect(reports > 0)
        #expect(c.view.control != .hidden && c.canRun)
        s.assistantTarget = .code
        #expect(c.view.control == .hidden && !c.canRun)
        #expect(c.autoTriageInputs.available == false)
        token.cancel()
        assistant.close()
        await d.stop()
    }

    // MARK: Consent and the daemon

    /// Consent is kept only once the daemon stored the assistant
    /// preference: a refusal sets no key and is reported once (a run's own
    /// failure, or `onError` from Settings).
    @Test func consentOnlyOnceStored() async throws {
        let h = try await Harness(fake: try FakeClaude(turns: [answerTurn("x")]), consent: false, prefs: BoardPreferences())
        var errors: [String] = []
        h.prefs.onError = { errors.append($0) }
        await h.daemon.script.set(setFailure: RPCError(code: .storageError, message: "disk"))
        h.controller.consent = { true }
        h.controller.start(.manual)
        try await h.ended()
        #expect(h.controller.state == .failed(.manual, .backend, at: t0))
        #expect(!h.scratch.settings.boardTriageConsent && !h.scratch.settings.assistantConsent)
        #expect(errors.isEmpty, "the run reports it")
        // From Settings: reported through onError, no key either.
        #expect(await h.controller.giveConsent() == false)
        #expect(!h.scratch.settings.boardTriageConsent && !h.scratch.settings.assistantConsent)
        #expect(errors.count == 1)
        // Stored: both keys.
        await h.daemon.script.set(setFailure: nil)
        #expect(await h.controller.giveConsent())
        #expect(h.scratch.settings.boardTriageConsent && h.scratch.settings.assistantConsent)
        await h.stop()
    }

    @Test func providerSwitchDuringConsentWriteCannotApproveNewProvider() async throws {
        let h = try await Harness(fake: try FakeClaude(turns: [answerTurn("synthetic")]), consent: false, prefs: BoardPreferences(autoTriage: true))
        await h.daemon.script.hold(sets: true)
        let grant = Task { @MainActor in await h.controller.giveConsent() }
        try await triageWait { await h.daemon.script.waitingSets > 0 }
        h.scratch.settings.assistantProvider = .chatgpt
        await h.daemon.script.hold(sets: false)
        #expect(await grant.value == false)
        #expect(!h.scratch.settings.boardTriageConsent)
        #expect(h.scratch.settings.boardTriageChatGPTConsentVersion == 0)
        #expect(h.scratch.settings.assistantChatGPTConsentVersion == 0)
        try await triageWait { !h.prefs.writing }
        #expect(h.prefs.preferences?.autoTriage == false)
        #expect(h.prefs.preferences?.assistant == false)
        await h.stop()
    }

    /// A daemon that does not answer the board's preferences: a manual run
    /// fails without the sheet, and the control is unavailable after it,
    /// so the failure is not repeated on every click.
    @Test func noPreferencesNoSheet() async throws {
        let h = try await Harness(fake: try FakeClaude(turns: [answerTurn("x")]), consent: false, load: false)
        await h.daemon.script.set(getFailure: RPCError(code: .methodNotFound, message: "no board"))
        h.controller.consent = { [unowned h] in
            h.consentAsked += 1
            return true
        }
        h.controller.start(.manual)
        try await h.ended()
        #expect(h.controller.state == .failed(.manual, .backend, at: t0))
        #expect(h.consentAsked == 0)
        #expect(!h.scratch.settings.boardTriageConsent)
        #expect(h.controller.view.control == .unavailable && !h.controller.view.enabled)
        #expect(h.controller.view.toolTip == "the mail backend did not answer")
        await h.stop()
    }

    /// The daemon's assistant preference on while the board's consent is
    /// not given here (a withdrawal whose write failed) is turned off at
    /// the next load.
    @Test func strayAssistantPreferenceIsRepaired() async throws {
        let h = try await Harness(fake: try FakeClaude(turns: [answerTurn("x")]), consent: false, load: false)
        await h.daemon.script.set(prefs: BoardPreferences(assistant: true, autoTriage: true))
        h.prefs.connectionChanged(connected: true)
        try await triageWait { await h.daemon.script.sets.count == 1 && h.prefs.isIdle }
        #expect(await h.daemon.script.sets == [BoardPreferences(assistant: false, autoTriage: true)])
        // With the consent given nothing is touched.
        h.scratch.settings.boardTriageConsent = true
        await h.daemon.script.set(prefs: BoardPreferences(assistant: true))
        #expect(await h.prefs.loadNow())
        try await triageWait { h.prefs.isIdle }
        #expect(await h.daemon.script.sets.count == 1)
        await h.stop()
    }

    // MARK: Sign-in

    /// A sign-in check answered after a run learnt otherwise is dropped.
    @Test func lateSignInCheckIsDropped() async throws {
        let fake = try FakeClaude(turns: [answerTurn("x")])
        let d = fake.dir.path
        _ = try writeScript(fake.dir.appendingPathComponent("claude"), """
            D='\(d)'
            case "$1" in
            --version) echo '2.1.178 (Claude Code)'; exit 0;;
            auth) while [ ! -f "$D/release" ]; do sleep 0.02; done; echo '{"loggedIn": true}'; exit 0;;
            esac
            exit 1
            """)
        let h = try await Harness(fake: fake)
        h.controller.checkSignIn()
        h.controller.learnSignedIn(false)
        FileManager.default.createFile(atPath: d + "/release", contents: Data())
        try await triageWait { h.controller.signInAnswers == 1 }
        #expect(h.controller.signedIn == false)
        // A check of its own still counts.
        h.controller.checkSignIn()
        try await triageWait { h.controller.signInAnswers == 2 }
        #expect(h.controller.signedIn == true)
        await h.stop()
    }

    // MARK: Usage

    /// The result's usage is the run's, sent with board.runEnd.
    @Test func usageFromTheResult() async throws {
        let fake = try FakeClaude(turns: [FakeTurn(lines: [fakeInit] + annotateUsing("a1", msg: "m1", input: 5, read: 100)
            + [resultUsing(input: 40, output: 900, write: 1200, read: 50000)])])
        let h = try await Harness(fake: fake)
        h.board(queue: 3)
        h.controller.start(.manual)
        try await h.ended()
        #expect(await h.daemon.script.runEnds == [BoardRunEndParams(
            runId: "run_1", usage: BoardUsage(
                inputTokens: 40, outputTokens: 900, cacheCreationInputTokens: 1200, cacheReadInputTokens: 50000))])
        await h.stop()
    }

    /// Without a result: the distinct API messages seen, each once. A
    /// cancelled run, a run stopped at its limit, one that timed out and
    /// one that quit (cancelAndEnd).
    @Test func usageWithoutAResult() async throws {
        // Two lines of message m1 (counted once), one of m2.
        let lines = [fakeInit] + annotateUsing("a1", msg: "m1", input: 5, read: 100)
            + annotateUsing("a2", msg: "m1", input: 5, read: 100) + annotateUsing("a3", msg: "m2", input: 7, read: 300)
        // Without the result's usage the sum is a lower bound.
        let sum = BoardUsage(
            inputTokens: 12, outputTokens: 2, cacheCreationInputTokens: 20, cacheReadInputTokens: 400, lowerBound: true)
        // Cancelled.
        var h = try await Harness(fake: try FakeClaude(turns: [FakeTurn(
            lines: lines, shell: "sleep 30; echo '\(resultUsing(input: 1, output: 1, write: 1, read: 1))'")]))
        h.board(queue: 4)
        h.controller.start(.manual)
        try await triageWait { h.controller.state == .running(.manual, done: 3, total: 4) }
        h.controller.cancel()
        try await h.ended()
        #expect(await h.daemon.script.runEnds == [BoardRunEndParams(runId: "run_1", error: .cancelled, usage: sum)])
        await h.stop()
        // At its limit, no result within the grace period: what the API
        // messages reported, the one after the limit too.
        h = try await Harness(fake: try FakeClaude(turns: [FakeTurn(
            lines: lines + annotateUsing("a4", msg: "m3", input: 1000, read: 1000), shell: "sleep 30")]))
        h.controller.grace = .milliseconds(300)
        h.board(queue: 12)
        h.controller.start(.automatic, limit: 3)
        try await h.ended()
        #expect(await h.daemon.script.runEnds == [BoardRunEndParams(runId: "run_1", usage: BoardUsage(
            inputTokens: 1012, outputTokens: 3, cacheCreationInputTokens: 30, cacheReadInputTokens: 1400,
            lowerBound: true))])
        await h.stop()
        // Timed out.
        h = try await Harness(fake: try FakeClaude(turns: [FakeTurn(lines: lines, shell: "sleep 30")]))
        h.controller.timeout = .milliseconds(800)
        h.board(queue: 12)
        h.controller.start(.manual)
        try await h.ended()
        #expect(await h.daemon.script.runEnds == [BoardRunEndParams(runId: "run_1", error: .timeout, usage: sum)])
        await h.stop()
        // Quit.
        h = try await Harness(fake: try FakeClaude(turns: [FakeTurn(lines: lines, shell: "sleep 30")]))
        h.board(queue: 12)
        h.controller.start(.manual)
        try await triageWait { h.controller.state == .running(.manual, done: 3, total: 12) }
        await h.controller.cancelAndEnd()
        #expect(await h.daemon.script.runEnds == [BoardRunEndParams(runId: "run_1", error: .cancelled, usage: sum)])
        await h.stop()
    }

    /// At its limit the run waits for Claude Code's result and sends its
    /// usage, the whole run's; the run is a success, ended once, and a
    /// note the bridge refused after the limit is not counted.
    @Test func limitWaitsForTheResult() async throws {
        let fake = try FakeClaude(turns: [FakeTurn(
            lines: [fakeInit] + annotateUsing("a1", msg: "m1", input: 5, read: 100)
                + annotateUsing("a2", msg: "m2", input: 7, read: 300),
            shell: "sleep 0.3; echo '\(fakeToolUse("a3", "annotate_case"))'; echo '\(fakeToolResult("a3", "limit", error: true))'; "
                + "echo '\(resultUsing(input: 40, output: 2500, write: 1200, read: 50000))'")])
        let h = try await Harness(fake: fake)
        h.board(queue: 12)
        h.controller.start(.manual, limit: 2)
        try await h.ended()
        #expect(h.states.contains(.running(.manual, done: 2, total: 2)))
        #expect(h.controller.state == .finished(.manual, annotated: 2, refused: 0, at: t0))
        #expect(await h.daemon.script.runEnds == [BoardRunEndParams(
            runId: "run_1", usage: BoardUsage(
                inputTokens: 40, outputTokens: 2500, cacheCreationInputTokens: 1200, cacheReadInputTokens: 50000))])
        try await Task.sleep(for: .milliseconds(200))
        #expect(h.ends.count == 1 && h.ends[0].1 == nil)
        #expect(await h.daemon.script.runEnds.count == 1)
        await h.stop()
    }

    /// An error result after the limit does not turn the run into a
    /// failure; its usage still counts.
    @Test func errorResultAfterTheLimit() async throws {
        let fake = try FakeClaude(turns: [FakeTurn(
            lines: [fakeInit] + annotate("a1") + annotate("a2"),
            shell: "sleep 0.3; echo '\(resultUsing(input: 3, output: 700, write: 80, read: 9000, success: false))'")])
        let h = try await Harness(fake: fake)
        h.board(queue: 12)
        h.controller.start(.automatic, limit: 2)
        try await h.ended()
        #expect(h.controller.state == .finished(.automatic, annotated: 2, at: t0))
        #expect(await h.daemon.script.runEnds == [BoardRunEndParams(
            runId: "run_1", usage: BoardUsage(
                inputTokens: 3, outputTokens: 700, cacheCreationInputTokens: 80, cacheReadInputTokens: 9000))])
        try await Task.sleep(for: .milliseconds(200))
        #expect(h.ends.count == 1 && h.ends[0].1 == nil)
        await h.stop()
    }

    /// Stop while a run at its limit waits for its result ends it at once,
    /// as the success it is, with the usage seen so far.
    @Test func stopWhileWaitingForTheResult() async throws {
        let fake = try FakeClaude(turns: [FakeTurn(
            lines: [fakeInit] + annotateUsing("a1", msg: "m1", input: 5, read: 100)
                + annotateUsing("a2", msg: "m2", input: 7, read: 300),
            shell: "sleep 30; echo '\(resultUsing(input: 1, output: 1, write: 1, read: 1))'")])
        let h = try await Harness(fake: fake)
        h.controller.grace = .seconds(60)
        h.board(queue: 12)
        h.controller.start(.manual, limit: 2)
        try await triageWait { h.controller.state == .running(.manual, done: 2, total: 2) }
        h.controller.cancel()
        #expect(h.controller.state == .finished(.manual, annotated: 2, at: t0))
        try await h.ended()
        #expect(await h.daemon.script.runEnds == [BoardRunEndParams(runId: "run_1", usage: BoardUsage(
            inputTokens: 12, outputTokens: 2, cacheCreationInputTokens: 20, cacheReadInputTokens: 400,
            lowerBound: true))])
        try await Task.sleep(for: .milliseconds(200))
        #expect(h.ends.count == 1 && h.ends[0].1 == nil)
        #expect(await h.daemon.script.runEnds.count == 1)
        await h.stop()
    }

    /// A second note on the same case counts once (the bridge charges no
    /// second slot for it); a result naming no case counts as its own.
    @Test func reannotationCountsOnce() async throws {
        let fake = try FakeClaude(turns: [FakeTurn(lines: [
            fakeInit, fakeToolUse("a1", "annotate_case"), fakeToolResult("a1", "annotated case c_1: x"),
            fakeToolUse("a2", "annotate_case"), fakeToolResult("a2", "annotated case c_1: y"),
            fakeToolUse("a3", "annotate_case"), fakeToolResult("a3", "annotated"), fakeResult("ok"),
        ])])
        let h = try await Harness(fake: fake)
        h.board(queue: 5)
        h.controller.start(.manual, limit: 3)
        try await h.ended()
        #expect(h.controller.state == .finished(.manual, annotated: 2, refused: 0, at: t0))
        await h.stop()
    }

    /// A run that reported nothing sends no usage, and a new run does not
    /// carry the last one's.
    @Test func noUsageIsLeftOut() async throws {
        let fake = try FakeClaude(turns: [
            FakeTurn(lines: [fakeInit] + annotateUsing("a1", msg: "m1", input: 5, read: 100) + [fakeResult("ok")]),
            FakeTurn(lines: [fakeInit] + annotate("a2") + [fakeResult("ok")]),
        ])
        let h = try await Harness(fake: fake)
        h.board(queue: 3)
        h.controller.start(.manual)
        try await h.ended()
        h.controller.start(.manual)
        try await h.ended()
        let ends = await h.daemon.script.runEnds
        #expect(ends.count == 2)
        // A result without usage: the messages' placeholders, a lower bound.
        #expect(ends.first?.usage == BoardUsage(
            inputTokens: 5, outputTokens: 1, cacheCreationInputTokens: 10, cacheReadInputTokens: 100, lowerBound: true))
        #expect(ends.last?.usage == nil)
        await h.stop()
    }

    /// The board's usage of the last 24 hours reaches the view; unknown
    /// before a board.list, "None" without usage.
    @Test func usageInTheView() async throws {
        let h = try await Harness(fake: try FakeClaude(turns: [answerTurn("x")]))
        #expect(!h.controller.view.usageShown)
        h.board(queue: 1)
        #expect(h.controller.view.usageShown && h.controller.view.usageValue == "None" && h.controller.view.usageDetail.isEmpty)
        var reports = 0
        let token = h.controller.observe { reports += 1 }
        h.controller.boardChanged(Board.Snapshot(
            annotated: true, phase: .ready,
            triage: Board.Triage(queue: 1, usage24h: BoardUsageTotal(inputTokens: 10, cacheReadInputTokens: 5000, runs: 2))))
        #expect(reports == 1)
        #expect(h.controller.view.usageShown && h.controller.view.usageDetail.hasSuffix("From 2 triage runs"))
        token.cancel()
        await h.stop()
    }

    // MARK: For the views

    /// Quitting waits for board.runEnd, but never longer than its bound.
    @Test func cancelAndEnd() async throws {
        let h = try await Harness(fake: try FakeClaude(turns: [FakeTurn(lines: [fakeInit], shell: "sleep 30")]))
        h.board(queue: 2)
        h.controller.start(.manual)
        try await h.running()
        await h.controller.cancelAndEnd()
        #expect(await h.daemon.script.runEnds == [BoardRunEndParams(runId: "run_1", error: .cancelled)])
        #expect(h.controller.isIdle, "runEnd answered before it returned")
        // A daemon that does not answer: it returns anyway.
        await h.daemon.script.hold(runEnds: true)
        h.controller.start(.manual)
        try await h.running()
        let started = ContinuousClock.now
        await h.controller.cancelAndEnd(wait: .milliseconds(200))
        #expect(ContinuousClock.now - started < .seconds(5))
        #expect(await h.daemon.script.waitingRunEnds == 1)
        await h.daemon.script.hold(runEnds: false)
        try await h.ended()
        // Nothing under way: it returns at once.
        await h.controller.cancelAndEnd()
        await h.stop()
    }

    /// The view is published again once a minute only while it names a
    /// relative time.
    @Test func clockOnlyForRelativeTimes() async throws {
        let h = try await Harness(fake: try FakeClaude(turns: [answerTurn("x")]))
        let clock = TriageClock()
        h.controller.clockSleep = { await clock.sleep($0) }
        h.board(queue: 1)
        #expect(!h.controller.view.relativeTime && !h.controller.clockRunning)
        h.board(queue: 1, run: Board.Run(model: "claude-code", date: t0.addingTimeInterval(-300), trigger: "auto", started: t0))
        #expect(h.controller.view.relativeTime && h.controller.clockRunning)
        var reports = 0
        let token = h.controller.observe { reports += 1 }
        try await triageWait { await clock.count == 1 }
        await clock.fire()
        try await triageWait { reports == 1 }
        #expect(h.controller.clockRunning, "still relative: the next tick")
        try await triageWait { await clock.count == 1 }
        // No relative time any more: the clock stops.
        h.board(queue: 1, assistantOn: false)
        #expect(!h.controller.view.relativeTime && !h.controller.clockRunning)
        token.cancel()
        await clock.fire()
        await h.stop()
    }

    /// The board source should run while automatic triage is on, consent
    /// is given and triage can run.
    @Test func wantsBoardData() async throws {
        let h = try await Harness(
            fake: try FakeClaude(turns: [answerTurn("x")]), prefs: BoardPreferences(assistant: true, autoTriage: true))
        #expect(h.controller.wantsBoardData)
        h.flags.available = false
        #expect(!h.controller.wantsBoardData)
        h.flags.available = true
        h.scratch.settings.boardTriageConsent = false
        #expect(!h.controller.wantsBoardData)
        h.scratch.settings.boardTriageConsent = true
        await h.prefs.update { $0.autoTriage = false }
        #expect(!h.controller.wantsBoardData)
        await h.stop()
    }

    /// Today's automatic count is in the view while automatic triage is on.
    @Test func todayInTheView() async throws {
        let h = try await Harness(
            fake: try FakeClaude(turns: [answerTurn("x")]), prefs: BoardPreferences(assistant: true, autoTriage: true))
        #expect(h.controller.view.annotatedToday == nil && h.controller.view.todayLine == "")
        h.board(queue: 1, annotatedToday: 4)
        #expect(h.controller.view.annotatedToday == 4)
        #expect(h.controller.view.todayLine == "4 conversations triaged automatically today")
        await h.prefs.update { $0.autoTriage = false }
        #expect(h.controller.view.annotatedToday == nil && h.controller.view.todayLine == "")
        await h.stop()
    }
}
