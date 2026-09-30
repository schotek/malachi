// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The one-shot requests of the In App target (ui/internal/assistant
// rewrite.go and search.go): `AssistantRequest`, the compose window's
// `ComposeRewriteController` and the search field's `SearchConversion`
// against the stand-in `claude` of AssistantPanelControllerTests
// (`FakeClaude`). No real Claude Code is ever run here. The Go
// counterpart is ui/internal/assistantpanel oneshot_test.go.

/// A result line with a structured_output (raw JSON) and a text.
private func structuredResult(_ structured: String, text: String = "") -> String {
    #"{"type":"result","subtype":"success","is_error":false,"result":"\#(text)","structured_output":\#(structured),"total_cost_usd":0.001}"#
}

private let errorResult = #"{"type":"result","subtype":"error_during_execution","is_error":true,"result":"","total_cost_usd":0}"#

@MainActor
private final class RequestHarness {
    let scratch = ScratchSettings()
    let fake: FakeClaude
    let request: AssistantRequest
    let work: URL
    var consentAsked = 0
    var consentAnswer = true

    init(fake: FakeClaude, consent: Bool = true, found: Bool = true) throws {
        self.fake = fake
        scratch.settings.assistantClaudePath = fake.path
        scratch.settings.assistantConsent = consent
        scratch.settings.assistantModel = .haiku
        work = fake.dir.appendingPathComponent("work", isDirectory: true)
        let prefix = fake.dir.path + "/"
        let locator = ClaudeCodeLocator(
            settings: scratch.settings, environment: ["HOME": fake.dir.path], timeout: .seconds(5),
            usable: { found && $0.hasPrefix(prefix) && ClaudeCodeLocator.isExecutableFile($0) })
        request = AssistantRequest(
            settings: scratch.settings, locator: locator, directory: work,
            environment: ["HOME": fake.dir.path, "LANG": "cs_CZ.UTF-8", "ANTHROPIC_API_KEY": "sk-never", "CLAUDECODE": "1"],
            killGrace: .milliseconds(300), timeout: .seconds(10))
        request.consent = { [unowned self] in
            self.consentAsked += 1
            return self.consentAnswer
        }
    }

    func stop() {
        request.cancel()
    }
}

/// A value the callbacks change.
@MainActor
private final class Box<T> {
    var value: T

    init(_ value: T) {
        self.value = value
    }
}

/// What a request reported.
@MainActor
private final class Outcomes {
    var outcomes: [AssistantRequest.Outcome] = []
    var texts: [String] = []
}

@MainActor
@Suite(.serialized) struct AssistantRequestTests {
    // MARK: AssistantRequest

    /// One turn without the bridge: the one-shot command line, the message
    /// on stdin, stdin closed, the streamed text, then the result.
    @Test func answersOnce() async throws {
        let fake = try FakeClaude(turns: [FakeTurn(lines: [
            fakeInit, fakeDelta("Dear "), fakeDelta("Jana"), fakeText("Dear Jana,"), fakeResult("Dear Jana,"),
        ])])
        let h = try RequestHarness(fake: fake)
        defer { h.stop() }
        let got = Outcomes()
        h.request.start(
            systemPrompt: "SYS", message: "line one\nline two", onText: { got.texts.append($0) },
            completion: { got.outcomes.append($0) })
        #expect(h.request.running)
        try await waitFor { !got.outcomes.isEmpty }
        #expect(got.outcomes == [.answered(text: "Dear Jana,", structured: nil)])
        #expect(got.texts == ["Dear ", "Dear Jana", "Dear Jana,"])
        #expect(!h.request.running)
        #expect(h.consentAsked == 0)
        #expect(fake.starts == 1)
        #expect(fake.prompts == ["line one\nline two"])
        #expect(fake.args == Assistant.args(Assistant.Options(bridge: "", model: .haiku, systemPrompt: "SYS")))
        #expect(!fake.args.contains("--mcp-config") && !fake.args.contains("--allowedTools"))
        #expect(!fake.env.contains("ANTHROPIC") && !fake.env.contains("CLAUDECODE"))
        #expect(fake.env.contains("LANG=cs_CZ.UTF-8"))
        #expect(fake.cwd == h.work.path)
        let mode = try FileManager.default.attributesOfItem(atPath: h.work.path)[.posixPermissions] as? Int
        #expect(mode == 0o700)
    }

    /// stdin is closed after the one message: a Claude Code that answers
    /// nothing ends at once, and that is a failure with its status.
    @Test func stdinIsClosed() async throws {
        let fake = try FakeClaude(turns: [FakeTurn(lines: [fakeInit])])
        let h = try RequestHarness(fake: fake)
        defer { h.stop() }
        let got = Outcomes()
        h.request.start(systemPrompt: "S", message: "m", completion: { got.outcomes.append($0) })
        try await waitFor(.seconds(5)) { !got.outcomes.isEmpty }
        #expect(got.outcomes == [.failed(.stopped("claude exited with status 0"))])
    }

    /// A schema goes on the command line; the structured_output comes back
    /// as it was written.
    @Test func structuredAnswer() async throws {
        let fake = try FakeClaude(turns: [FakeTurn(lines: [fakeInit, structuredResult(#"{"query":"x"}"#)])])
        let h = try RequestHarness(fake: fake)
        defer { h.stop() }
        let got = Outcomes()
        h.request.start(systemPrompt: "S", message: "m", jsonSchema: Assistant.searchSchema, completion: { got.outcomes.append($0) })
        try await waitFor { !got.outcomes.isEmpty }
        #expect(got.outcomes == [.answered(text: "", structured: Data(#"{"query":"x"}"#.utf8))])
        #expect(Array(fake.args.suffix(2)) == ["--json-schema", Assistant.searchSchema])
    }

    /// Consent: declined, nothing starts; allowed, it is kept, and the
    /// answer counts even when the request was cancelled meanwhile.
    @Test func consent() async throws {
        let fake = try FakeClaude(turns: [answerTurn("ok")])
        let h = try RequestHarness(fake: fake, consent: false)
        defer { h.stop() }
        let got = Outcomes()
        h.consentAnswer = false
        h.request.start(systemPrompt: "S", message: "m", completion: { got.outcomes.append($0) })
        try await waitFor { !got.outcomes.isEmpty }
        #expect(got.outcomes == [.declined])
        #expect(h.consentAsked == 1)
        #expect(!h.scratch.settings.assistantConsent)
        #expect(fake.starts == 0)

        // Allowed while the request was cancelled: kept, nothing sent.
        let answer = Box<CheckedContinuation<Bool, Never>?>(nil)
        h.request.consent = {
            await withCheckedContinuation { answer.value = $0 }
        }
        h.request.start(systemPrompt: "S", message: "m", completion: { got.outcomes.append($0) })
        try await waitFor { answer.value != nil }
        h.request.cancel()
        answer.value?.resume(returning: true)
        try await Task.sleep(for: .milliseconds(100))
        #expect(h.scratch.settings.assistantConsent)
        #expect(got.outcomes.count == 1)
        #expect(fake.starts == 0)

        // From now on nobody is asked.
        h.request.consent = nil
        h.request.start(systemPrompt: "S", message: "m", completion: { got.outcomes.append($0) })
        try await waitFor { got.outcomes.count == 2 }
        #expect(got.outcomes.last == .answered(text: "done", structured: nil))
    }

    /// Without a consent hook nothing is ever sent.
    @Test func noConsentHookSendsNothing() async throws {
        let fake = try FakeClaude(turns: [answerTurn("ok")])
        let h = try RequestHarness(fake: fake, consent: false)
        defer { h.stop() }
        h.request.consent = nil
        let got = Outcomes()
        h.request.start(systemPrompt: "S", message: "m", completion: { got.outcomes.append($0) })
        try await waitFor { !got.outcomes.isEmpty }
        #expect(got.outcomes == [.declined])
        #expect(fake.starts == 0)
    }

    /// Claude Code missing or signed out: the panel's texts.
    @Test func notFoundAndNotSignedIn() async throws {
        let missing = try RequestHarness(fake: try FakeClaude(turns: [answerTurn("x")]), found: false)
        defer { missing.stop() }
        let got = Outcomes()
        missing.request.start(systemPrompt: "S", message: "m", completion: { got.outcomes.append($0) })
        try await waitFor { !got.outcomes.isEmpty }
        #expect(got.outcomes == [.failed(.notFound)])
        #expect(AssistantRequest.Failure.notFound.text == "Claude Code was not found on this computer")
        #expect(missing.fake.starts == 0)

        let signedOut = try RequestHarness(fake: try FakeClaude(loggedIn: "false", turns: [answerTurn("x")]))
        defer { signedOut.stop() }
        signedOut.request.start(systemPrompt: "S", message: "m", completion: { got.outcomes.append($0) })
        try await waitFor { got.outcomes.count == 2 }
        #expect(got.outcomes[1] == .failed(.notSignedIn))
        #expect(AssistantRequest.Failure.notSignedIn.text == "Claude Code is not signed in. Sign in under AI in the preferences.")
        #expect(AssistantRequest.Failure.notSignedIn.reason == "Claude Code is not signed in. Sign in under AI in the preferences.")
        #expect(AssistantRequest.Failure.stopped("x").text == "The assistant stopped: x")
        #expect(AssistantRequest.Failure.stopped("x").reason == "x")
        #expect(signedOut.fake.starts == 0)
    }

    /// A result that is not a success, and an exit before the result.
    @Test func failures() async throws {
        let fake = try FakeClaude(
            onStart: #"if [ "$n" = 2 ]; then read -r line; echo 'Error: boom' >&2; exit 1; fi"#,
            turns: [FakeTurn(lines: [fakeInit, errorResult])])
        let h = try RequestHarness(fake: fake)
        defer { h.stop() }
        let got = Outcomes()
        h.request.start(systemPrompt: "S", message: "m", completion: { got.outcomes.append($0) })
        try await waitFor { got.outcomes.count == 1 }
        #expect(got.outcomes[0] == .failed(.stopped("error_during_execution")))
        h.request.start(systemPrompt: "S", message: "m", completion: { got.outcomes.append($0) })
        try await waitFor { got.outcomes.count == 2 }
        #expect(got.outcomes[1] == .failed(.stopped("Error: boom")))
    }

    /// The API refuses the sign-in although auth status says loggedIn: the
    /// request ends as not signed in, Claude Code's own message is no
    /// answer.
    @Test func refusedSignIn() async throws {
        let refused = "Failed to authenticate. API Error: 401"
        let fake = try FakeClaude(turns: [
            FakeTurn(lines: [fakeInit, fakeFailure("authentication_failed", refused), fakeResult(refused, success: false)]),
        ])
        let h = try RequestHarness(fake: fake)
        defer { h.stop() }
        let got = Outcomes()
        h.request.start(systemPrompt: "S", message: "m", onText: { got.texts.append($0) }, completion: { got.outcomes.append($0) })
        try await waitFor { !got.outcomes.isEmpty }
        #expect(got.outcomes == [.failed(.notSignedIn)])
        #expect(got.texts.isEmpty)
        // Another refusal is the result's.
        let limit = "API Error: Rate limit reached"
        let other = try RequestHarness(fake: try FakeClaude(turns: [
            FakeTurn(lines: [fakeInit, fakeFailure("rate_limit", limit), fakeResult(limit, success: false)]),
        ]))
        defer { other.stop() }
        other.request.start(systemPrompt: "S", message: "m", onText: { got.texts.append($0) }, completion: { got.outcomes.append($0) })
        try await waitFor { got.outcomes.count == 2 }
        #expect(got.outcomes[1] == .failed(.stopped(limit)))
        #expect(got.texts.isEmpty)
    }

    /// No answer in time: the process is ended and the request fails.
    @Test func timeout() async throws {
        let fake = try FakeClaude(turns: [FakeTurn(lines: [fakeInit, fakeDelta("thinking")], shell: "sleep 30")])
        let h = try RequestHarness(fake: fake)
        defer { h.stop() }
        h.request.timeout = .milliseconds(500)
        let got = Outcomes()
        h.request.start(systemPrompt: "S", message: "m", onText: { got.texts.append($0) }, completion: { got.outcomes.append($0) })
        try await waitFor { !got.outcomes.isEmpty }
        #expect(got.outcomes == [.failed(.stopped(AssistantRequest.timedOut))])
        #expect(got.texts == ["thinking"])
        #expect(!h.request.running)
    }

    /// Cancelled: no completion, the process ended; a new request cancels
    /// the one under way.
    @Test func cancelAndReplace() async throws {
        let fake = try FakeClaude(turns: [
            FakeTurn(lines: [fakeInit, fakeDelta("slow")], shell: "sleep 30"),
            answerTurn("second"),
        ])
        let h = try RequestHarness(fake: fake)
        defer { h.stop() }
        let got = Outcomes()
        h.request.start(systemPrompt: "S", message: "one", onText: { got.texts.append($0) }, completion: { got.outcomes.append($0) })
        try await waitFor { got.texts == ["slow"] }
        h.request.cancel()
        #expect(!h.request.running)
        try await Task.sleep(for: .milliseconds(200))
        #expect(got.outcomes.isEmpty)

        // The fake numbers its turns over every start: the second start
        // gets "second".
        h.request.start(systemPrompt: "S", message: "two", completion: { got.outcomes.append($0) })
        h.request.start(systemPrompt: "S", message: "two again", completion: { got.outcomes.append($0) })
        try await waitFor { !got.outcomes.isEmpty }
        try await Task.sleep(for: .milliseconds(200))
        #expect(got.outcomes == [.answered(text: "done", structured: nil)])
        #expect(fake.prompts.first == "one")
        #expect(fake.prompts.last == "two again")
        #expect(!fake.prompts.contains("two"))
    }

    // MARK: ComposeRewriteController

    @Test func rewriteRunsAndCleans() async throws {
        let fake = try FakeClaude(turns: [FakeTurn(lines: [
            fakeInit, fakeDelta("```\\n„Dobrý"), fakeDelta(" den.“\\n```"), fakeText("```\\n„Dobrý den.“\\n```"),
            fakeResult("```\\n„Dobrý den.“\\n```"),
        ])])
        let h = try RequestHarness(fake: fake)
        defer { h.stop() }
        let rewrite = ComposeRewriteController(request: h.request)
        let states = Box<[ComposeRewriteController.State]>([])
        rewrite.onState = { states.value.append($0) }
        #expect(rewrite.start(rewrite: .politer, custom: "", passage: "  Ahoj.\n"))
        #expect(rewrite.running)
        try await waitFor { !rewrite.running }
        #expect(rewrite.state == .done("Dobrý den."))
        #expect(states.value.first == .running(preview: ""))
        #expect(states.value.contains(.running(preview: "```\n„Dobrý")))
        #expect(states.value.last == .done("Dobrý den."))
        #expect(fake.prompts == [try Assistant.rewriteMessage(.politer, custom: "", passage: "Ahoj.")])
        #expect(fake.args == Assistant.args(Assistant.Options(bridge: "", model: .haiku, systemPrompt: Assistant.rewriteSystemPrompt())))
    }

    @Test func rewriteRefusesAndFails() async throws {
        let fake = try FakeClaude(turns: [FakeTurn(lines: [fakeInit, fakeResult(#"  \"\"  "#)])])
        let h = try RequestHarness(fake: fake)
        defer { h.stop() }
        let rewrite = ComposeRewriteController(request: h.request)
        // Nothing to ask: nothing happens.
        #expect(!rewrite.start(rewrite: .fix, custom: "", passage: " \n "))
        #expect(!rewrite.start(rewrite: .custom, custom: "  ", passage: "text"))
        #expect(rewrite.state == .idle)
        // Too long: fails at once.
        #expect(rewrite.start(rewrite: .fix, custom: "", passage: String(repeating: "a", count: Assistant.maxPassage + 1)))
        #expect(rewrite.state == .failed("The assistant stopped: assistant: the passage is too long: 20001 characters, at most 20000"))
        #expect(fake.starts == 0)
        // An empty answer is a failure, never an empty replacement.
        #expect(rewrite.start(rewrite: .custom, custom: "Make it shorter", passage: "text"))
        try await waitFor { !rewrite.running }
        #expect(rewrite.state == .failed("The assistant stopped: the answer is empty"))
        #expect(fake.prompts == ["Follow this instruction: Make it shorter\n\nPassage:\n<<<\ntext\n>>>"])
        // Cancelled: idle.
        rewrite.cancel()
        #expect(rewrite.state == .idle)
    }

    @Test func rewriteErrorsAndDeclinedConsent() async throws {
        let missing = try RequestHarness(fake: try FakeClaude(turns: [answerTurn("x")]), found: false)
        defer { missing.stop() }
        let rewrite = ComposeRewriteController(request: missing.request)
        rewrite.start(rewrite: .shorter, custom: "", passage: "text")
        try await waitFor { !rewrite.running }
        #expect(rewrite.state == .failed("Claude Code was not found on this computer"))

        let asked = try RequestHarness(fake: try FakeClaude(turns: [answerTurn("x")]), consent: false)
        defer { asked.stop() }
        asked.consentAnswer = false
        let declined = ComposeRewriteController(request: asked.request)
        declined.start(rewrite: .shorter, custom: "", passage: "text")
        try await waitFor { !declined.running }
        #expect(declined.state == .idle)
        #expect(asked.consentAsked == 1)
    }

    /// Cancelled while running: idle, and the late answer is dropped.
    @Test func rewriteCancelled() async throws {
        let fake = try FakeClaude(turns: [FakeTurn(lines: [fakeInit, fakeDelta("x")], shell: "sleep 1; echo '\(fakeResult("late"))'")])
        let h = try RequestHarness(fake: fake)
        defer { h.stop() }
        let rewrite = ComposeRewriteController(request: h.request)
        rewrite.start(rewrite: .fix, custom: "", passage: "text")
        try await waitFor { rewrite.state == .running(preview: "x") }
        rewrite.cancel()
        #expect(rewrite.state == .idle)
        try await Task.sleep(for: .milliseconds(1500))
        #expect(rewrite.state == .idle)
    }

    // MARK: SearchConversion

    @Test func searchConverts() async throws {
        let fake = try FakeClaude(turns: [
            FakeTurn(lines: [fakeInit, structuredResult(#"{"query":"from:jan  faktur\nafter:2026-03-01"}"#)]),
            // No structured_output: the text is read the same way.
            FakeTurn(lines: [fakeInit, fakeResult(#"{\"query\":\"is:unread\"}"#)]),
            FakeTurn(lines: [fakeInit, structuredResult(#"{"q":"x"}"#, text: "no JSON")]),
        ])
        let h = try RequestHarness(fake: fake)
        defer { h.stop() }
        let search = SearchConversion(request: h.request)
        search.today = { "2026-09-29" }
        let got = Box<[SearchConversion.Outcome]>([])
        #expect(search.convert("  faktury od Jany z března  ") { got.value.append($0) })
        #expect(search.running)
        try await waitFor { got.value.count == 1 }
        #expect(got.value == [.query("from:jan faktur after:2026-03-01")])
        #expect(fake.prompts == ["faktury od Jany z března"])
        // The fake writes one argument per line, and the prompt has lines.
        #expect(fake.args.joined(separator: "\n") == Assistant.args(Assistant.Options(
            bridge: "", model: .haiku, systemPrompt: Assistant.searchSystemPrompt(today: "2026-09-29"),
            jsonSchema: Assistant.searchSchema)).joined(separator: "\n"))

        search.convert("unread") { got.value.append($0) }
        try await waitFor { got.value.count == 2 }
        #expect(got.value[1] == .query("is:unread"))

        search.convert("x") { got.value.append($0) }
        try await waitFor { got.value.count == 3 }
        #expect(got.value[2] == .failed("The search could not be converted: the answer holds no query"))
    }

    @Test func searchRefusesAndFails() async throws {
        let missing = try RequestHarness(fake: try FakeClaude(turns: [answerTurn("x")]), found: false)
        defer { missing.stop() }
        let search = SearchConversion(request: missing.request)
        let got = Box<[SearchConversion.Outcome]>([])
        #expect(!search.convert("   ") { got.value.append($0) })
        #expect(search.convert(String(repeating: "a", count: Assistant.maxSearchWords + 1)) { got.value.append($0) })
        #expect(got.value.isEmpty, "later, never inside convert")
        try await waitFor { got.value.count == 1 }
        #expect(got.value[0] == .failed("The search could not be converted: assistant: the words are too long: 501 characters, at most 500"))
        search.convert("faktury") { got.value.append($0) }
        try await waitFor { got.value.count == 2 }
        #expect(got.value[1] == .failed("The search could not be converted: Claude Code was not found on this computer"))

        let signedOut = try RequestHarness(fake: try FakeClaude(loggedIn: "false", turns: [answerTurn("x")]))
        defer { signedOut.stop() }
        let other = SearchConversion(request: signedOut.request)
        other.convert("faktury") { got.value.append($0) }
        try await waitFor { got.value.count == 3 }
        #expect(got.value[2] == .failed("The search could not be converted: Claude Code is not signed in. Sign in under AI in the preferences."))
    }

    @Test func searchOutcomes() {
        typealias O = SearchConversion.Outcome
        #expect(SearchConversion.outcome(.declined) == O.declined)
        #expect(SearchConversion.outcome(.failed(.stopped("API Error: 500\nmore"))) == O.failed("The search could not be converted: API Error: 500"))
        #expect(SearchConversion.outcome(.answered(text: "", structured: Data(#"{"query":" x "}"#.utf8))) == O.query("x"))
        // The structured answer wins over the text.
        #expect(SearchConversion.outcome(.answered(text: #"{"query":"text"}"#, structured: Data(#"{"query":"structured"}"#.utf8))) == O.query("structured"))
        #expect(SearchConversion.outcome(.answered(text: #"{"query":"text"}"#, structured: Data(#"{"query":""}"#.utf8))) == O.query("text"))
        #expect(SearchConversion.outcome(.answered(text: "", structured: nil)) == O.failed("The search could not be converted: the answer holds no query"))
    }
}
