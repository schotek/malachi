// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The board's Suggest Reply (`BoardReplyController`) against a fake daemon
// (board.get, board.setDraft, draft.delete) and the stand-in `claude` of
// AssistantPanelControllerTests (`FakeClaude`). No real Claude Code, daemon
// or bridge is ever run: the bridge's path is only passed on. The timeout
// is a gate the test opens, never a real sleep.

private let t0 = Date(timeIntervalSince1970: 1_790_848_800)

private func wire(_ n: String, draft: BoardDraft? = nil) -> BoardCase {
    BoardCase(
        id: BoardCaseID(rawValue: "c_\(n)"), accountId: "acc_1", threadId: ThreadID(rawValue: "t_\(n)"),
        ruleState: .you, ruleReason: .youAddressed, subject: "Subject \(n)",
        person: Address(name: "P", address: "p@example.invalid"), date: t0, messageCount: 3,
        replyMessageId: MessageID(rawValue: "m_\(n)"), replyFolderId: "f_inbox",
        latestMessageId: MessageID(rawValue: "m_\(n)"), draft: draft)
}

private func boardCase(_ n: String) -> Board.Case {
    Board.Case(
        id: Board.CaseID(rawValue: "c_\(n)"), account: "acc_1", person: "P", date: t0, subject: "Subject \(n)",
        ruleState: .you, reply: Board.ReplyTarget(message: MessageID(rawValue: "m_\(n)"), folder: "f_inbox"))
}

/// The bridge's create_draft result for draft `id`.
private func created(_ id: String, account: String = "acc_1") -> String {
    "draft \(id) (version 1) stored in account \(account); it is NOT sent."
}

private func draftTurn(_ id: String = "d_9", then shell: String = "") -> FakeTurn {
    FakeTurn(
        lines: [fakeInit, fakeToolUse("r", "read_message"), fakeToolResult("r", "the message"),
                fakeToolUse("c", "create_draft"), fakeToolResult("c", created(id))]
            + (shell.isEmpty ? [fakeResult("")] : []),
        shell: shell)
}

/// The daemon's side.
private actor ReplyScript {
    var getFailure: RPCError?
    var setFailure: RPCError?
    var caseDraft: BoardDraft?
    private(set) var gets: [String] = []
    private(set) var sets: [BoardSetDraftParams] = []
    private(set) var deletes: [DraftDeleteParams] = []
    private var holdingDeletes = false
    private var heldDeletes: [CheckedContinuation<Void, Never>] = []

    func set(getFailure: RPCError?) { self.getFailure = getFailure }
    func set(setFailure: RPCError?) { self.setFailure = setFailure }
    func set(caseDraft: BoardDraft?) { self.caseDraft = caseDraft }

    func hold(deletes: Bool) {
        holdingDeletes = deletes
        if !deletes {
            let w = heldDeletes
            heldDeletes = []
            for c in w {
                c.resume()
            }
        }
    }

    func get(_ p: Data) throws -> Data {
        let q = try JSONCoding.decoder().decode(BoardGetParams.self, from: p)
        gets.append(q.caseId.rawValue)
        if let getFailure { throw getFailure }
        let n = String(q.caseId.rawValue.dropFirst(2))
        let msgs = (1...8).map { i in
            BoardMessage(
                id: MessageID(rawValue: "m_\(n)_\(i)"), folderId: "f_inbox", from: Address(address: "x@y"),
                date: t0, text: "t")
        }
        return try JSONCoding.encoder().encode(BoardGetResult(case: wire(n, draft: caseDraft), messages: msgs))
    }

    func setDraft(_ p: Data) throws -> Data {
        let q = try JSONCoding.decoder().decode(BoardSetDraftParams.self, from: p)
        sets.append(q)
        if let setFailure { throw setFailure }
        let n = String(q.caseId.rawValue.dropFirst(2))
        return try JSONCoding.encoder().encode(BoardSetDraftResult(case: wire(n, draft: BoardDraft(draftId: q.draftId, text: "x", updated: t0))))
    }

    func delete(_ p: Data) async throws -> Data {
        deletes.append(try JSONCoding.decoder().decode(DraftDeleteParams.self, from: p))
        if holdingDeletes {
            await withCheckedContinuation { heldDeletes.append($0) }
        }
        return Data("{}".utf8)
    }
}

/// Opens the controller's timeout when the test says so.
private final class TimeoutGate: @unchecked Sendable {
    private let lock = NSLock()
    private var waiters: [CheckedContinuation<Void, Never>] = []
    private var open = false
    private(set) var asked: [Duration] = []

    func sleep(_ d: Duration) async {
        await withCheckedContinuation { (c: CheckedContinuation<Void, Never>) in
            lock.lock()
            asked.append(d)
            if open {
                lock.unlock()
                c.resume()
                return
            }
            waiters.append(c)
            lock.unlock()
        }
    }

    func fire() {
        lock.lock()
        open = true
        let w = waiters
        waiters = []
        lock.unlock()
        for c in w {
            c.resume()
        }
    }
}

private final class Found: @unchecked Sendable {
    var found = true
}

@MainActor
private final class Harness {
    let fake: FakeClaude
    let daemon: FakeDaemon
    let script = ReplyScript()
    let client: RPCClient
    let scratch = ScratchSettings()
    let controller: BoardReplyController
    let gate = TimeoutGate()
    let found = Found()
    var available = true
    var consentAnswer = true
    var consentAsked = 0
    var refreshes = 0
    var states: [Board.SuggestReplyState] = []
    private var token: BoardObserverToken?

    init(fake: FakeClaude, consent: Bool = true, bridge: String? = "/b/malachi-mcp") async throws {
        self.fake = fake
        daemon = try FakeDaemon()
        let s = script
        await daemon.on(API.BoardGet.name) { p in try await s.get(p) }
        await daemon.on(API.BoardSetDraft.name) { p in try await s.setDraft(p) }
        await daemon.on(API.DraftDelete.name) { p in try await s.delete(p) }
        try await daemon.start()
        client = RPCClient(socketPath: daemon.path)
        try await client.connect()
        let settings = scratch.settings
        settings.assistantClaudePath = fake.path
        settings.assistantConsent = consent
        settings.boardTriageConsent = false
        // The panel's model and the board's apart: the reply takes the panel's.
        settings.assistantModel = .haiku
        settings.boardTriageModel = .opus
        let prefix = fake.dir.path + "/"
        let found = found
        let locator = ClaudeCodeLocator(
            settings: settings, environment: ["HOME": fake.dir.path], timeout: .seconds(5),
            usable: { found.found && $0.hasPrefix(prefix) && ClaudeCodeLocator.isExecutableFile($0) })
        let request = AssistantRequest(
            settings: settings, locator: locator, directory: fake.dir.appendingPathComponent("work"),
            environment: ["HOME": fake.dir.path], killGrace: .milliseconds(300), timeout: .seconds(30))
        weak var me: Harness?
        controller = BoardReplyController(
            client: client, settings: settings, locator: locator, request: request, bridge: bridge,
            socket: "/s.sock", available: { me?.available ?? false })
        me = self
        let gate = gate
        controller.sleep = { d in await gate.sleep(d) }
        controller.consent = { [unowned self] in
            self.consentAsked += 1
            return self.consentAnswer
        }
        controller.onRefresh = { [unowned self] in self.refreshes += 1 }
        token = controller.observe { [unowned self] in
            if self.states.last != self.controller.state {
                self.states.append(self.controller.state)
            }
        }
    }

    func ended() async throws {
        try await triageWait { self.controller.isIdle }
    }

    func stop() async {
        controller.cancel()
        token?.cancel()
        await client.close()
        await daemon.stop()
    }
}

@MainActor
@Suite(.serialized) struct BoardReplyControllerTests {
    /// A reply: board.get, the request with the bridge for this one
    /// reply, the draft from create_draft's result linked, the board asked
    /// again.
    @Test func success() async throws {
        let fake = try FakeClaude(turns: [draftTurn()])
        let h = try await Harness(fake: fake)
        #expect(h.controller.start(boardCase("1"), instruction: "  Say yes,\n thanks  "))
        #expect(h.controller.state == .running(Board.CaseID(rawValue: "c_1")))
        try await h.ended()
        #expect(h.controller.state == .idle)
        #expect(h.states == [.running(Board.CaseID(rawValue: "c_1")), .idle])
        #expect(await h.script.gets == ["c_1"])
        #expect(await h.script.sets == [BoardSetDraftParams(caseId: "c_1", draftId: "d_9")])
        #expect(await h.script.deletes.isEmpty)
        #expect(h.refreshes == 1)
        #expect(h.consentAsked == 0)
        // The command line: the bridge for one reply to m_1, three tools,
        // the panel's model.
        #expect(fake.args == Assistant.args(Assistant.Options(
            bridge: "/b/malachi-mcp", socket: "/s.sock", model: .haiku,
            systemPrompt: Assistant.suggestReplySystemPrompt(), bridgeArgs: ["--reply-only", "m_1"],
            tools: Assistant.suggestReplyTools)))
        let joined = fake.args.joined(separator: " ")
        #expect(!joined.contains("--allow-triage") && !joined.contains("--allow-modify") && !joined.contains("--allow-send"))
        #expect(fake.prompts == [Assistant.suggestReplyMessage(
            accountID: "acc_1", messageID: "m_1", others: (1...8).map { "m_1_\($0)" }, instruction: "Say yes, thanks")])
        #expect(fake.prompts.first?.contains("m_1_4") == true && fake.prompts.first?.contains("m_1_3") == false)
        // The time ran for the reply's timeout.
        #expect(h.gate.asked == [Assistant.suggestReplyTimeout])
        await h.stop()
    }

    @Test func noDraft() async throws {
        let fake = try FakeClaude(turns: [FakeTurn(lines: [fakeInit, fakeText("Here you go"), fakeResult("Here you go")])])
        let h = try await Harness(fake: fake)
        #expect(h.controller.start(boardCase("1"), instruction: ""))
        try await h.ended()
        #expect(h.controller.state == .failed(Board.CaseID(rawValue: "c_1"), .noDraft))
        #expect(await h.script.sets.isEmpty)
        #expect(await h.script.deletes.isEmpty)
        #expect(fake.prompts.first?.hasSuffix("The user gave no instruction.") == true)
        #expect(h.controller.view(for: boardCase("1"), in: Board.Snapshot(), samples: false).note
            == "The suggested reply failed: the assistant wrote no reply.")
        await h.stop()
    }

    /// A refused create_draft (an error result) is no draft.
    @Test func refusedCreateIsNoDraft() async throws {
        let fake = try FakeClaude(turns: [FakeTurn(lines: [
            fakeInit, fakeToolUse("c", "create_draft"), fakeToolResult("c", created("d_9"), error: true), fakeResult("no"),
        ])])
        let h = try await Harness(fake: fake)
        #expect(h.controller.start(boardCase("1"), instruction: ""))
        try await h.ended()
        #expect(h.controller.state == .failed(Board.CaseID(rawValue: "c_1"), .noDraft))
        #expect(await h.script.sets.isEmpty)
        await h.stop()
    }

    @Test func linkRefusedDeletesTheDraft() async throws {
        let fake = try FakeClaude(turns: [draftTurn()])
        let h = try await Harness(fake: fake)
        await h.script.set(setFailure: RPCError(code: .storageError, message: "disk"))
        #expect(h.controller.start(boardCase("1"), instruction: ""))
        try await h.ended()
        #expect(h.controller.state == .failed(Board.CaseID(rawValue: "c_1"), .backend))
        #expect(await h.script.deletes == [DraftDeleteParams(accountId: "acc_1", draftId: "d_9")])
        #expect(h.controller.created == nil)
        await h.stop()
    }

    /// The case got a suggested reply meanwhile: ours goes, quietly.
    @Test func linkConflictEndsQuietly() async throws {
        let fake = try FakeClaude(turns: [draftTurn()])
        let h = try await Harness(fake: fake)
        await h.script.set(setFailure: RPCError(code: .conflict, message: "linked"))
        #expect(h.controller.start(boardCase("1"), instruction: ""))
        try await h.ended()
        #expect(h.controller.state == .idle)
        #expect(await h.script.deletes == [DraftDeleteParams(accountId: "acc_1", draftId: "d_9")])
        await h.stop()
    }

    /// Stop after the draft was created and before it was linked deletes it.
    @Test func stopDeletesTheCreatedDraft() async throws {
        let fake = try FakeClaude(turns: [draftTurn(then: "sleep 30")])
        let h = try await Harness(fake: fake)
        #expect(h.controller.start(boardCase("1"), instruction: ""))
        try await triageWait { h.controller.created != nil }
        h.controller.cancel()
        #expect(h.controller.state == .failed(Board.CaseID(rawValue: "c_1"), .cancelled))
        try await h.ended()
        #expect(await h.script.deletes == [DraftDeleteParams(accountId: "acc_1", draftId: "d_9")])
        #expect(await h.script.sets.isEmpty)
        #expect(h.controller.view(for: boardCase("1"), in: Board.Snapshot(), samples: false).note
            == "The suggested reply failed: it was stopped.")
        await h.stop()
    }

    @Test func timeoutDeletesTheCreatedDraft() async throws {
        let fake = try FakeClaude(turns: [draftTurn(then: "sleep 30")])
        let h = try await Harness(fake: fake)
        #expect(h.controller.start(boardCase("1"), instruction: ""))
        try await triageWait { h.controller.created != nil }
        h.gate.fire()
        try await triageWait { !h.controller.state.isRunning }
        try await h.ended()
        #expect(h.controller.state == .failed(Board.CaseID(rawValue: "c_1"), .timeout))
        #expect(await h.script.deletes == [DraftDeleteParams(accountId: "acc_1", draftId: "d_9")])
        #expect(await h.script.sets.isEmpty)
        await h.stop()
    }

    @Test func timeoutWithoutDraft() async throws {
        let fake = try FakeClaude(turns: [FakeTurn(lines: [fakeInit], shell: "sleep 30")])
        let h = try await Harness(fake: fake)
        #expect(h.controller.start(boardCase("1"), instruction: ""))
        try await triageWait { fake.starts == 1 }
        h.gate.fire()
        try await h.ended()
        #expect(h.controller.state == .failed(Board.CaseID(rawValue: "c_1"), .timeout))
        #expect(await h.script.deletes.isEmpty)
        await h.stop()
    }

    /// Quitting stops the request and waits for the delete of its draft,
    /// at most the bound.
    @Test func quitCleansUp() async throws {
        let fake = try FakeClaude(turns: [draftTurn(then: "sleep 30")])
        let h = try await Harness(fake: fake)
        #expect(h.controller.start(boardCase("1"), instruction: ""))
        try await triageWait { h.controller.created != nil }
        await h.controller.cancelAndCleanUp()
        #expect(await h.script.deletes == [DraftDeleteParams(accountId: "acc_1", draftId: "d_9")])
        #expect(h.controller.isIdle)
        await h.stop()
    }

    /// A daemon that does not answer the delete does not hold the quit
    /// beyond the bound.
    @Test func quitIsBounded() async throws {
        let fake = try FakeClaude(turns: [draftTurn(then: "sleep 30")])
        let h = try await Harness(fake: fake)
        await h.script.hold(deletes: true)
        #expect(h.controller.start(boardCase("1"), instruction: ""))
        try await triageWait { h.controller.created != nil }
        await h.controller.cancelAndCleanUp(wait: .milliseconds(50))
        #expect(!h.controller.isIdle, "the delete still waits")
        await h.script.hold(deletes: false)
        try await h.ended()
        await h.stop()
    }

    /// One request at a time; another case shows that one runs elsewhere.
    @Test func oneAtATime() async throws {
        let fake = try FakeClaude(turns: [FakeTurn(lines: [fakeInit], shell: "sleep 30")])
        let h = try await Harness(fake: fake)
        let snapshot = Board.Snapshot(cases: [boardCase("1"), boardCase("2")])
        #expect(h.controller.start(boardCase("1"), instruction: ""))
        #expect(!h.controller.start(boardCase("2"), instruction: ""))
        let here = h.controller.view(for: boardCase("1"), in: snapshot, samples: false)
        #expect(here.running && !here.enabled && here.note.isEmpty)
        let there = h.controller.view(for: boardCase("2"), in: snapshot, samples: false)
        #expect(!there.running && !there.enabled && there.note == Board.Text.suggestReplyElsewhere)
        try await triageWait { fake.starts == 1 }
        #expect(!h.controller.start(boardCase("2"), instruction: ""))
        h.controller.cancel()
        try await h.ended()
        #expect(await h.script.gets == ["c_1"])
        await h.stop()
    }

    /// The assistant's consent is asked first; declined, nothing runs.
    @Test func consentDeclined() async throws {
        let fake = try FakeClaude(turns: [draftTurn()])
        let h = try await Harness(fake: fake, consent: false)
        h.consentAnswer = false
        #expect(h.controller.start(boardCase("1"), instruction: ""))
        try await h.ended()
        #expect(h.consentAsked == 1 && h.controller.state == .idle)
        #expect(fake.starts == 0)
        #expect(await h.script.gets.isEmpty)
        #expect(!h.scratch.settings.assistantConsent && !h.scratch.settings.boardTriageConsent)
        await h.stop()
    }

    /// Allowed: the assistant's consent is kept, the board's is not touched.
    @Test func consentGiven() async throws {
        let fake = try FakeClaude(turns: [draftTurn()])
        let h = try await Harness(fake: fake, consent: false)
        #expect(h.controller.start(boardCase("1"), instruction: ""))
        try await h.ended()
        #expect(h.consentAsked == 1 && h.controller.state == .idle)
        #expect(h.scratch.settings.assistantConsent && !h.scratch.settings.boardTriageConsent)
        #expect(await h.script.sets.count == 1)
        await h.stop()
    }

    @Test func boardGetFails() async throws {
        let fake = try FakeClaude(turns: [draftTurn()])
        let h = try await Harness(fake: fake)
        await h.script.set(getFailure: RPCError(code: .storageError, message: "x"))
        #expect(h.controller.start(boardCase("1"), instruction: ""))
        try await h.ended()
        #expect(h.controller.state == .failed(Board.CaseID(rawValue: "c_1"), .backend))
        #expect(fake.starts == 0)
        await h.stop()
    }

    /// The case has a suggested reply by now: nothing is asked.
    @Test func caseHasDraftAlready() async throws {
        let fake = try FakeClaude(turns: [draftTurn()])
        let h = try await Harness(fake: fake)
        await h.script.set(caseDraft: BoardDraft(draftId: "d_1", text: "x", updated: t0))
        #expect(h.controller.start(boardCase("1"), instruction: ""))
        try await h.ended()
        #expect(h.controller.state == .idle && fake.starts == 0 && h.refreshes == 1)
        await h.stop()
    }

    @Test func notSignedIn() async throws {
        let fake = try FakeClaude(loggedIn: "false", turns: [draftTurn()])
        let h = try await Harness(fake: fake)
        #expect(h.controller.start(boardCase("1"), instruction: ""))
        try await h.ended()
        #expect(h.controller.state == .failed(Board.CaseID(rawValue: "c_1"), .notSignedIn))
        #expect(h.controller.signedIn == false)
        let v = h.controller.view(for: boardCase("1"), in: Board.Snapshot(), samples: false)
        #expect(!v.enabled && v.note == Assistant.signInTexts().hint)
        await h.stop()
    }

    @Test func toolsMissing() async throws {
        let fake = try FakeClaude(turns: [FakeTurn(lines: [fakeInitFailed, fakeResult("x")])])
        let h = try await Harness(fake: fake)
        #expect(h.controller.start(boardCase("1"), instruction: ""))
        try await h.ended()
        #expect(h.controller.state == .failed(Board.CaseID(rawValue: "c_1"), .toolsMissing))
        await h.stop()
    }

    /// Nothing starts without the feature, the bridge or Claude Code; a
    /// request that loses the feature stops.
    @Test func availability() async throws {
        let fake = try FakeClaude(turns: [FakeTurn(lines: [fakeInit], shell: "sleep 30")])
        let h = try await Harness(fake: fake)
        h.available = false
        #expect(!h.controller.start(boardCase("1"), instruction: ""))
        #expect(!h.controller.view(for: boardCase("1"), in: Board.Snapshot(), samples: false).shown)
        h.available = true
        h.found.found = false
        #expect(!h.controller.start(boardCase("1"), instruction: ""))
        let v = h.controller.view(for: boardCase("1"), in: Board.Snapshot(), samples: false)
        #expect(v.shown && !v.enabled && v.note == Assistant.panelTexts().notFound)
        h.found.found = true
        var withDraft = boardCase("1")
        withDraft.draft = Board.DraftLink(id: "d_1", text: "x")
        #expect(!h.controller.start(withDraft, instruction: ""))
        #expect(h.controller.start(boardCase("1"), instruction: ""))
        try await triageWait { fake.starts == 1 }
        h.available = false
        h.controller.availabilityChanged()
        #expect(h.controller.state == .failed(Board.CaseID(rawValue: "c_1"), .cancelled))
        try await h.ended()
        await h.stop()

        let none = try await Harness(fake: fake, bridge: nil)
        #expect(!none.controller.start(boardCase("1"), instruction: ""))
        #expect(!none.controller.view(for: boardCase("1"), in: Board.Snapshot(), samples: false).shown)
        await none.stop()
    }
}
