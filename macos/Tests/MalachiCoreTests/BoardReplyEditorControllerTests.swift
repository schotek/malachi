// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The board's inline reply editor state (BoardReplyEditorController) against
// a fake daemon: a new key loads the draft with draft.get, the same key
// never reloads (an autosave bumps the case's version), a stale answer is
// dropped, a draft the pane ended stays hidden until the board drops the
// link, retry, and the failures. The fake holds draft.get answers until the
// test releases them, so no outcome depends on timing; "nothing more
// happens" checks wait briefly.

private struct Timeout: Error {}

@MainActor
private func waitUntil(_ timeout: Duration = .seconds(10), _ cond: @MainActor () async -> Bool) async throws {
    let deadline = ContinuousClock.now + timeout
    while await !cond() {
        if ContinuousClock.now > deadline { throw Timeout() }
        try await Task.sleep(for: .milliseconds(5))
    }
}

/// draft.get: what it was asked, the answers held until released.
private actor Drafts {
    var failure: RPCError?
    var holding = false
    private(set) var asked: [DraftGetParams] = []
    private var waiting: [CheckedContinuation<Void, Never>] = []

    func set(failure: RPCError?) { self.failure = failure }
    func hold(_ on: Bool) {
        holding = on
        if !on {
            let w = waiting
            waiting = []
            for c in w { c.resume() }
        }
    }

    /// Releases the oldest held answer only.
    func releaseOne() {
        guard !waiting.isEmpty else { return }
        waiting.removeFirst().resume()
    }

    var held: Int { waiting.count }

    func get(_ data: Data) async throws -> Data {
        let p = try JSONCoding.decoder().decode(DraftGetParams.self, from: data)
        asked.append(p)
        let failure = failure
        if holding {
            await withCheckedContinuation { waiting.append($0) }
        }
        if let failure { throw failure }
        let d = Draft(
            id: p.draftId, accountId: p.accountId, version: 5, to: [Address(name: "Ann", address: "ann@example.org")],
            subject: "Re: Offer", textBody: "Text of \(p.draftId.rawValue)", htmlBody: "<p>Text of \(p.draftId.rawValue)</p>",
            inReplyTo: "m_2", local: true)
        return try JSONCoding.encoder().encode(DraftGetResult(draft: d))
    }
}

private func boardCase(_ n: String, draft: String? = nil, version: Int64 = 1) -> Board.Case {
    Board.Case(
        id: Board.CaseID(rawValue: "c_\(n)"), account: "acc_1", thread: ThreadID(rawValue: "t_\(n)"), person: "Ann",
        date: Date(timeIntervalSince1970: 1_790_000_000), subject: "Offer", ruleState: .you,
        reply: Board.ReplyTarget(message: "m_2", folder: "f_inbox"),
        draft: draft.map { Board.DraftLink(id: DraftID(rawValue: $0), text: "") }, version: version)
}

private func key(_ n: String, _ draft: String) -> BoardReplyEditorController.Key {
    .init(caseID: Board.CaseID(rawValue: "c_\(n)"), account: "acc_1", draft: DraftID(rawValue: draft))
}

@MainActor
private final class Harness {
    let fake: FakeDaemon
    let drafts = Drafts()
    let client: RPCClient
    let editor: BoardReplyEditorController
    var changes = 0
    var token: BoardObserverToken?

    init() async throws {
        fake = try FakeDaemon()
        let d = drafts
        await fake.on(API.DraftGet.name) { try await d.get($0) }
        try await fake.start()
        client = RPCClient(socketPath: fake.path)
        try await client.connect()
        editor = BoardReplyEditorController(client: client)
        token = editor.observe { [unowned self] in self.changes += 1 }
    }

    var asked: Int {
        get async { await drafts.asked.count }
    }

    func stop() async {
        await drafts.hold(false)
        await client.close()
        await fake.stop()
    }
}

@MainActor
@Suite(.serialized) struct BoardReplyEditorControllerTests {
    @Test func aCaseWithoutADraftShowsNothing() async throws {
        let h = try await Harness()
        h.editor.show(boardCase("1"))
        h.editor.show(nil)
        #expect(h.editor.phase == .none && h.changes == 0)
        try await Task.sleep(for: .milliseconds(50))
        #expect(await h.asked == 0)
        await h.stop()
    }

    @Test func aNewKeyLoadsTheDraft() async throws {
        let h = try await Harness()
        await h.drafts.hold(true)
        h.editor.show(boardCase("1", draft: "d_1"))
        #expect(h.editor.phase == .loading(key("1", "d_1")))
        try await waitUntil { await h.drafts.held == 1 }
        await h.drafts.hold(false)
        try await waitUntil { h.editor.phase.key == key("1", "d_1") && h.editor.phase != .loading(key("1", "d_1")) }
        guard case .ready(let k, let p) = h.editor.phase else {
            Issue.record("not ready: \(h.editor.phase)")
            await h.stop()
            return
        }
        #expect(k == key("1", "d_1"))
        #expect(p.kind == .edit && p.draftID == "d_1" && p.version == 5 && p.accountID == "acc_1")
        #expect(p.bodyHTML == "<p>Text of d_1</p>" && p.inReplyTo == "m_2" && p.subject == "Re: Offer")
        #expect(await h.drafts.asked == [DraftGetParams(accountId: "acc_1", draftId: "d_1")])
        #expect(h.changes == 2, "loading, ready")
        await h.stop()
    }

    @Test func theSameKeyNeverReloads() async throws {
        let h = try await Harness()
        h.editor.show(boardCase("1", draft: "d_1", version: 1))
        try await waitUntil { if case .ready = h.editor.phase { return true } else { return false } }
        let changes = h.changes
        // Autosaves bump the case's version; the board lists it again.
        for v in 2...6 {
            h.editor.show(boardCase("1", draft: "d_1", version: Int64(v)))
        }
        try await Task.sleep(for: .milliseconds(50))
        #expect(await h.asked == 1 && h.changes == changes)
        if case .ready = h.editor.phase {} else { Issue.record("not ready: \(h.editor.phase)") }
        // Also while loading.
        await h.drafts.hold(true)
        h.editor.show(boardCase("2", draft: "d_2"))
        h.editor.show(boardCase("2", draft: "d_2", version: 9))
        try await waitUntil { await h.drafts.held == 1 }
        #expect(await h.asked == 2)
        await h.stop()
    }

    @Test func aStaleAnswerIsDropped() async throws {
        let h = try await Harness()
        await h.drafts.hold(true)
        h.editor.show(boardCase("1", draft: "d_1"))
        try await waitUntil { await h.drafts.held == 1 }
        h.editor.show(boardCase("2", draft: "d_2"))
        try await waitUntil { await h.drafts.held == 2 }
        // The first case's answer arrives after the second was selected.
        await h.drafts.releaseOne()
        try await Task.sleep(for: .milliseconds(100))
        #expect(h.editor.phase == .loading(key("2", "d_2")))
        await h.drafts.releaseOne()
        try await waitUntil { if case .ready(key("2", "d_2"), _) = h.editor.phase { return true } else { return false } }
        // Deselected while loading: the answer is dropped too.
        h.editor.show(boardCase("3", draft: "d_3"))
        try await waitUntil { await h.drafts.held == 1 }
        h.editor.show(nil)
        await h.drafts.hold(false)
        try await Task.sleep(for: .milliseconds(100))
        #expect(h.editor.phase == .none)
        await h.stop()
    }

    @Test func anotherDraftOfTheSameCaseLoads() async throws {
        let h = try await Harness()
        h.editor.show(boardCase("1", draft: "d_1"))
        try await waitUntil { if case .ready = h.editor.phase { return true } else { return false } }
        h.editor.show(boardCase("1", draft: "d_9"))
        try await waitUntil { if case .ready(key("1", "d_9"), _) = h.editor.phase { return true } else { return false } }
        #expect(await h.asked == 2)
        // The link dropped: nothing.
        h.editor.show(boardCase("1"))
        #expect(h.editor.phase == .none)
        await h.stop()
    }

    @Test func endedHidesUntilTheLinkGoes() async throws {
        let h = try await Harness()
        h.editor.show(boardCase("1", draft: "d_1"))
        try await waitUntil { if case .ready = h.editor.phase { return true } else { return false } }
        h.editor.ended(key("1", "d_1"))
        #expect(h.editor.phase == .none)
        // The board still links it for a moment (the refresh is on its way).
        h.editor.show(boardCase("1", draft: "d_1", version: 2))
        h.editor.show(boardCase("2"))
        h.editor.show(boardCase("1", draft: "d_1", version: 3))
        #expect(h.editor.phase == .none)
        try await Task.sleep(for: .milliseconds(50))
        #expect(await h.asked == 1)
        // The link went; a later suggested reply, even under the same id,
        // is edited again.
        h.editor.show(boardCase("1"))
        h.editor.show(boardCase("1", draft: "d_1", version: 4))
        #expect(h.editor.phase == .loading(key("1", "d_1")))
        try await waitUntil { await h.asked == 2 }
        // Ending another key changes nothing shown.
        try await waitUntil { if case .ready = h.editor.phase { return true } else { return false } }
        h.editor.ended(key("7", "d_7"))
        if case .ready = h.editor.phase {} else { Issue.record("not ready: \(h.editor.phase)") }
        await h.stop()
    }

    @Test func failuresAndRetry() async throws {
        let h = try await Harness()
        await h.drafts.set(failure: RPCError(code: .storageError, message: "disk"))
        h.editor.show(boardCase("1", draft: "d_1"))
        try await waitUntil { h.editor.phase == .failed(key("1", "d_1"), .backend) }
        // The same key while failed: no new request (Try Again asks).
        h.editor.show(boardCase("1", draft: "d_1", version: 2))
        try await Task.sleep(for: .milliseconds(50))
        #expect(await h.asked == 1)
        await h.drafts.set(failure: nil)
        h.editor.retry()
        #expect(h.editor.phase == .loading(key("1", "d_1")))
        try await waitUntil { if case .ready = h.editor.phase { return true } else { return false } }
        #expect(await h.asked == 2)
        // retry outside a failure does nothing.
        h.editor.retry()
        try await Task.sleep(for: .milliseconds(50))
        #expect(await h.asked == 2)

        await h.drafts.set(failure: RPCError(code: .draftNotFound, message: "gone"))
        h.editor.show(boardCase("2", draft: "d_2"))
        try await waitUntil { h.editor.phase == .failed(key("2", "d_2"), .gone) }
        await h.stop()
    }

    @Test func aLostConnectionIsABackendFailure() async throws {
        let h = try await Harness()
        await h.drafts.hold(true)
        h.editor.show(boardCase("1", draft: "d_1"))
        try await waitUntil { await h.drafts.held == 1 }
        await h.fake.closeAll()
        try await waitUntil { h.editor.phase == .failed(key("1", "d_1"), .backend) }
        await h.stop()
    }

    @Test func observersCanBeRemoved() async throws {
        let h = try await Harness()
        h.token?.cancel()
        h.editor.show(boardCase("1", draft: "d_1"))
        try await waitUntil { if case .ready = h.editor.phase { return true } else { return false } }
        #expect(h.changes == 0)
        await h.stop()
    }

    // MARK: Unstar through the sources

    @Test func samplesUnflagIsAPlaceholder() {
        let source = InMemoryBoardSource(Board.Snapshot(accounts: [], cases: [boardCase("1")], commitments: []))
        var notices: [String] = []
        var changes = 0
        source.onNotice = { notices.append($0) }
        source.onChange = { changes += 1 }
        source.unflag(Board.CaseID(rawValue: "c_1"))
        source.unflag(Board.CaseID(rawValue: "c_9"))
        #expect(notices == [Board.Text.later] && changes == 0)
    }
}
