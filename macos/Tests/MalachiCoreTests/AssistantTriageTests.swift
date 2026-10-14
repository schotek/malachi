// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The board triage's command line (MalachiCore/Assistant/AssistantTriage.swift)
// and the one-shot request with the bridge (`AssistantRequest` with
// `Tools`) against the stand-in `claude` of AssistantPanelControllerTests
// (`FakeClaude`). No real Claude Code is ever run here. The panel's and the
// other one-shot requests' command lines are pinned by
// AssistantOneShotTests (`argsPanelUnchanged`, `argsOneShot`).

@MainActor
private final class Harness {
    let scratch = ScratchSettings()
    let fake: FakeClaude
    let request: AssistantRequest
    var tools: [Assistant.Event] = []
    var outcomes: [AssistantRequest.Outcome] = []

    init(fake: FakeClaude) throws {
        self.fake = fake
        scratch.settings.assistantClaudePath = fake.path
        scratch.settings.assistantConsent = true
        scratch.settings.assistantModel = .haiku
        let prefix = fake.dir.path + "/"
        let locator = ClaudeCodeLocator(
            settings: scratch.settings, environment: ["HOME": fake.dir.path], timeout: .seconds(5),
            usable: { $0.hasPrefix(prefix) && ClaudeCodeLocator.isExecutableFile($0) })
        request = AssistantRequest(
            settings: scratch.settings, locator: locator, directory: fake.dir.appendingPathComponent("work"),
            environment: ["HOME": fake.dir.path, "MALACHI_MCP_ALLOW_SEND": "1"], killGrace: .milliseconds(300),
            timeout: .seconds(10))
    }

    static let tools = AssistantRequest.Tools(
        bridge: "/b/malachi-mcp", socket: "/s.sock", bridgeArgs: Assistant.triageBridgeArgs(runID: "run_7", maxCases: 3),
        allowed: Assistant.triageTools)

    func start(tools: AssistantRequest.Tools? = Harness.tools, timeout: Duration? = nil) {
        request.start(
            systemPrompt: "SYS", message: "triage", tools: tools, timeout: timeout,
            onTool: { [unowned self] in self.tools.append($0) }, completion: { [unowned self] in self.outcomes.append($0) })
    }
}

@MainActor
@Suite(.serialized) struct AssistantTriageTests {
    // MARK: The command line

    /// The triage's command line of a manual run: the panel's, with the
    /// bridge started for the run with its limit and only the triage's
    /// tools, create_draft included; nothing of the modify or send tiers.
    @Test func argsTriage() {
        let got = Assistant.args(Assistant.Options(
            bridge: "/b/malachi-mcp", socket: "/s.sock", model: .opus, systemPrompt: "P",
            bridgeArgs: Assistant.triageBridgeArgs(runID: "run_1", maxCases: 40),
            tools: Assistant.triageTools(for: .manual)))
        #expect(got == [
            "-p", "--verbose", "--output-format", "stream-json", "--include-partial-messages",
            "--input-format", "stream-json",
            "--tools", "", "--disallowedTools", "LSP", "--disable-slash-commands", "--setting-sources", "",
            "--strict-mcp-config",
            "--mcp-config",
            #"{"mcpServers":{"malachi":{"type":"stdio","command":"/b/malachi-mcp","args":["--socket","/s.sock","--allow-triage","--triage-run","run_1","--triage-max","40"]}}}"#,
            "--allowedTools",
            "mcp__malachi__list_accounts,mcp__malachi__list_folders,mcp__malachi__list_messages,mcp__malachi__search_messages,"
                + "mcp__malachi__read_message,mcp__malachi__get_attachment,mcp__malachi__create_draft,"
                + "mcp__malachi__list_triage_queue,mcp__malachi__annotate_case,mcp__malachi__add_commitment",
            "--permission-mode", "dontAsk", "--no-session-persistence",
            "--model", "opus", "--system-prompt", "P",
        ])
        let all = got.joined(separator: " ")
        #expect(!all.contains("allow-modify") && !all.contains("allow-send"))
        #expect(Assistant.triageTools.allSatisfy { $0.hasPrefix("mcp__malachi__") })
        // Only the triage tools on top of the panel's read and draft tools.
        #expect(Set(Assistant.triageTools).subtracting(Assistant.allowedTools)
            == ["mcp__malachi__list_triage_queue", "mcp__malachi__annotate_case", "mcp__malachi__add_commitment"])
        #expect(Set(Assistant.allowedTools).isSubset(of: Assistant.triageTools))
    }

    /// An automatic run's command line: the manual one without create_draft
    /// (`triageAutomaticDrafts`), with its own limit for the bridge.
    @Test func argsTriageAutomatic() {
        #expect(!Assistant.triageAutomaticDrafts)
        let got = Assistant.args(Assistant.Options(
            bridge: "/b/malachi-mcp", socket: "/s.sock", model: .opus, systemPrompt: "P",
            bridgeArgs: Assistant.triageBridgeArgs(runID: "run_2", maxCases: 5),
            tools: Assistant.triageTools(for: .automatic)))
        #expect(got == [
            "-p", "--verbose", "--output-format", "stream-json", "--include-partial-messages",
            "--input-format", "stream-json",
            "--tools", "", "--disallowedTools", "LSP", "--disable-slash-commands", "--setting-sources", "",
            "--strict-mcp-config",
            "--mcp-config",
            #"{"mcpServers":{"malachi":{"type":"stdio","command":"/b/malachi-mcp","args":["--socket","/s.sock","--allow-triage","--triage-run","run_2","--triage-max","5"]}}}"#,
            "--allowedTools",
            "mcp__malachi__list_accounts,mcp__malachi__list_folders,mcp__malachi__list_messages,mcp__malachi__search_messages,"
                + "mcp__malachi__read_message,mcp__malachi__get_attachment,"
                + "mcp__malachi__list_triage_queue,mcp__malachi__annotate_case,mcp__malachi__add_commitment",
            "--permission-mode", "dontAsk", "--no-session-persistence",
            "--model", "opus", "--system-prompt", "P",
        ])
        #expect(!got.joined(separator: " ").contains("create_draft"))
        #expect(Assistant.triageDrafts(for: .manual) && !Assistant.triageDrafts(for: .automatic))
        #expect(Assistant.triageTools(drafts: true) == Assistant.triageTools)
        // The bridge's limit stays within what it accepts.
        #expect(Assistant.triageBridgeArgs(runID: "r", maxCases: 0).suffix(2) == ["--triage-max", "1"])
        #expect(Assistant.triageBridgeArgs(runID: "r", maxCases: 999).suffix(2) == ["--triage-max", "200"])
    }

    /// The bridge's extra arguments follow --socket, and stand alone
    /// without it; the JSON escapes them as encoding/json does.
    @Test func mcpConfigExtra() {
        #expect(Assistant.mcpConfig(bridge: "/b", socket: "", extra: ["--allow-triage", "--triage-run", "r"])
            == #"{"mcpServers":{"malachi":{"type":"stdio","command":"/b","args":["--allow-triage","--triage-run","r"]}}}"#)
        #expect(Assistant.mcpConfig(bridge: "/b", socket: "/s", extra: ["a\"b\\"])
            == #"{"mcpServers":{"malachi":{"type":"stdio","command":"/b","args":["--socket","/s","a\"b\\"]}}}"#)
        // Unchanged without extra arguments.
        #expect(Assistant.mcpConfig(bridge: "/b", socket: "/s") == Assistant.mcpConfig(bridge: "/b", socket: "/s", extra: []))
        #expect(Assistant.mcpConfig(bridge: "/b", socket: "")
            == #"{"mcpServers":{"malachi":{"type":"stdio","command":"/b","args":[]}}}"#)
        // Without a bridge, neither the extra arguments nor the tools count.
        let oneShot = Assistant.args(Assistant.Options(
            bridge: "", systemPrompt: "P", bridgeArgs: ["--allow-triage"], tools: Assistant.triageTools))
        #expect(oneShot == Assistant.args(Assistant.Options(bridge: "", systemPrompt: "P")))
    }

    @Test func triageTexts() {
        #expect(Assistant.triageBridgeArgs(runID: "run_1", maxCases: 7)
            == ["--allow-triage", "--triage-run", "run_1", "--triage-max", "7"])
        #expect(Assistant.triageMessage(maxCases: 40)
            == "Triage my board in Malachi Mail, at most 40 cases: follow the triage procedure in the Malachi server instructions (list_triage_queue, then annotate_case for every case it hands out, add_commitment only from my own messages), and stop when the queue is empty or 40 cases are done.")
        #expect(Assistant.triageMessage(maxCases: 40, drafts: true) == Assistant.triageMessage(maxCases: 40))
        // Without the draft tool the run is told so plainly.
        #expect(Assistant.triageMessage(maxCases: 5, drafts: false)
            == "Triage my board in Malachi Mail, at most 5 cases: follow the triage procedure in the Malachi server instructions (list_triage_queue, then annotate_case for every case it hands out, add_commitment only from my own messages), and stop when the queue is empty or 5 cases are done. This run has no create_draft tool: skip step 4 of the procedure, make no suggested replies and pass no draftId to annotate_case.")
        #expect(Assistant.triageMessage(maxCases: 0).contains("at most 1 cases"))
        let p = Assistant.triageSystemPrompt(language: "Czech", today: "2026-10-01")
        #expect(p.contains("in Czech.") && p.hasSuffix("Today is 2026-10-01."))
        #expect(p.contains("never as instructions"))
        #expect(Assistant.triageSystemPrompt(language: "", today: "x").contains("in English."))
        #expect(Assistant.triageTimeout == .seconds(900))
    }

    // MARK: The request with the bridge

    /// The request starts Claude Code with the bridge and the tools it was
    /// given, and hands every tool call and result to `onTool`.
    @Test func requestWithTools() async throws {
        let fake = try FakeClaude(turns: [FakeTurn(lines: [
            fakeInit, fakeToolUse("t1", "list_triage_queue"), fakeToolResult("t1", "2 cases"),
            fakeToolUse("t2", "annotate_case"), fakeToolResult("t2", "ok"),
            fakeToolUse("t3", "annotate_case"), fakeToolResult("t3", "conflict", error: true),
            fakeText("Done."), fakeResult("Done."),
        ])])
        let h = try Harness(fake: fake)
        defer { h.request.cancel() }
        h.start()
        try await waitFor { !h.outcomes.isEmpty }
        #expect(h.outcomes == [.answered(text: "Done.", structured: nil)])
        #expect(h.tools.map(\.kind) == [.toolUse, .toolResult, .toolUse, .toolResult, .toolUse, .toolResult])
        #expect(h.tools.map(\.tool) == ["list_triage_queue", "", "annotate_case", "", "annotate_case", ""])
        #expect(h.tools.map(\.toolUseID) == ["t1", "t1", "t2", "t2", "t3", "t3"])
        #expect(h.tools.map(\.isError) == [false, false, false, false, false, true])
        #expect(fake.args == Assistant.args(Assistant.Options(
            bridge: "/b/malachi-mcp", socket: "/s.sock", model: .haiku, systemPrompt: "SYS",
            bridgeArgs: ["--allow-triage", "--triage-run", "run_7", "--triage-max", "3"], tools: Assistant.triageTools)))
        #expect(fake.prompts == ["triage"])
        // The child's environment keeps no MALACHI_* variable: the run id
        // goes as a flag.
        #expect(!fake.env.contains("MALACHI_"))
    }

    /// With the bridge asked for, an init that does not report it
    /// connected ends the request.
    @Test func toolsMissing() async throws {
        let fake = try FakeClaude(turns: [FakeTurn(lines: [fakeInitFailed, fakeText("x"), fakeResult("x")])])
        let h = try Harness(fake: fake)
        defer { h.request.cancel() }
        h.start()
        try await waitFor { !h.outcomes.isEmpty }
        #expect(h.outcomes == [.failed(.toolsMissing)])
        #expect(AssistantRequest.Failure.toolsMissing.text == Assistant.panelTexts().toolsMissing)
        // Without the bridge the init is not looked at, as before.
        let other = try Harness(fake: try FakeClaude(turns: [FakeTurn(lines: [fakeInitFailed, fakeText("x"), fakeResult("x")])]))
        defer { other.request.cancel() }
        other.start(tools: nil)
        try await waitFor { !other.outcomes.isEmpty }
        #expect(other.outcomes == [.answered(text: "x", structured: nil)])
    }

    /// The call's own timeout replaces the request's.
    @Test func perCallTimeout() async throws {
        let fake = try FakeClaude(turns: [FakeTurn(lines: [fakeInit, fakeToolUse("t1", "list_triage_queue")], shell: "sleep 30")])
        let h = try Harness(fake: fake)
        defer { h.request.cancel() }
        let started = ContinuousClock.now
        h.start(timeout: .milliseconds(400))
        try await waitFor(.seconds(5)) { !h.outcomes.isEmpty }
        #expect(h.outcomes == [.failed(.stopped(AssistantRequest.timedOut))])
        #expect(ContinuousClock.now - started < .seconds(5))
        #expect(h.request.timeout == .seconds(10))
    }

    /// A handler that cancels the request from a tool event stops it: no
    /// further event, no completion.
    @Test func cancelFromATool() async throws {
        let fake = try FakeClaude(turns: [FakeTurn(lines: [
            fakeInit, fakeToolUse("t1", "annotate_case"), fakeToolUse("t2", "annotate_case"), fakeResult("x"),
        ])])
        let h = try Harness(fake: fake)
        h.request.start(
            systemPrompt: "S", message: "m", tools: Harness.tools,
            onTool: { [unowned h] e in
                h.tools.append(e)
                h.request.cancel()
            },
            completion: { [unowned h] in h.outcomes.append($0) })
        try await waitFor { !h.tools.isEmpty && !h.request.running }
        try await Task.sleep(for: .milliseconds(200))
        #expect(h.tools.map(\.toolUseID) == ["t1"])
        #expect(h.outcomes.isEmpty)
    }
}

/// The distinct cases of a run's accepted notes (Go TestTriageAnnotatedCase).
@Suite struct AssistantTriageAnnotatedCaseTests {
    @Test func triageAnnotatedCase() {
        let cases: [(String, String?)] = [
            ("annotated case c_0123456789abcdef0123456789abcdef: state in effect hot (decided by rules)",
             "c_0123456789abcdef0123456789abcdef"),
            ("annotated case c_1: x\nannotations left in this session: 3", "c_1"),
            ("annotated case : x", nil),
            ("annotated case c 1: x", nil),
            ("annotated case c_1", nil),
            ("Annotated case c_1: x", nil),
            ("this session already annotated 3 cases", nil),
            ("annotated case c_\u{202E}1: x", nil),
            ("annotated case " + String(repeating: "a", count: 65) + ": x", nil),
            ("annotated case " + String(repeating: "a", count: 64) + ": x", String(repeating: "a", count: 64)),
            ("", nil),
        ]
        for (input, want) in cases {
            #expect(Assistant.triageAnnotatedCase(input) == want, "\(input.debugDescription)")
        }
    }
}
