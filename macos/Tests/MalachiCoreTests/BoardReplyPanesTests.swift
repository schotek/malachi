// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The rules of the board's inline reply panes (BoardReplyPanes) with fake
// panes over the real loader and a fake daemon: a retired pane is saved and
// closed; a sending pane is never settled and its outcome always arrives; a
// pane with unsaved text is kept (never trimmed), retried with back-off and
// shown again with its note; another link is not proof the draft went; a
// pane taken back while it settles stays; the quit says whether anything is
// at stake. Settles are held by the test where an outcome depends on order;
// back-off delays are milliseconds, and every wait is for a condition.

private struct Timeout: Error {}

@MainActor
private func waitUntil(_ timeout: Duration = .seconds(10), _ cond: @MainActor () async -> Bool) async throws {
    let deadline = ContinuousClock.now + timeout
    while await !cond() {
        if ContinuousClock.now > deadline { throw Timeout() }
        try await Task.sleep(for: .milliseconds(5))
    }
}

/// A pane as the rules see it.
@MainActor
private final class FakePane: BoardReplyPane {
    let key: BoardReplyEditorController.Key
    /// The outcomes of the next settles; past the list, `fallback`.
    var results: [Bool] = []
    var fallback = true
    /// Settles wait for `release` while set, and so does every settle
    /// from the `holdFrom`th on.
    var holding = false
    var holdFrom = Int.max
    private var held: [CheckedContinuation<Void, Never>] = []
    var settles = 0
    var closes = 0
    var abandons = 0
    var hasUnsavedText = false
    var isSending = false
    var isLost = false
    var replyTitle = "Re: Offer"

    init(_ key: BoardReplyEditorController.Key) {
        self.key = key
    }

    func settle() async -> Bool {
        settles += 1
        if holding || settles >= holdFrom {
            await withCheckedContinuation { held.append($0) }
        }
        let ok = results.isEmpty ? fallback : results.removeFirst()
        hasUnsavedText = !ok
        return ok
    }

    /// Lets the held settles go on.
    func release() {
        holding = false
        holdFrom = Int.max
        let h = held
        held = []
        for c in h { c.resume() }
    }

    var waiting: Int { held.count }

    func close() { closes += 1 }

    func abandon() { abandons += 1 }

    var closed: Bool { closes > 0 }
}

/// draft.get answers at once.
private actor Drafts {
    private(set) var asked = 0

    func get(_ data: Data) throws -> Data {
        asked += 1
        let p = try JSONCoding.decoder().decode(DraftGetParams.self, from: data)
        let d = Draft(id: p.draftId, accountId: p.accountId, version: 5, subject: "Re: Offer", textBody: "", local: true)
        return try JSONCoding.encoder().encode(DraftGetResult(draft: d))
    }
}

private func boardCase(_ n: String, draft: String? = "d") -> Board.Case {
    Board.Case(
        id: Board.CaseID(rawValue: "c_\(n)"), account: "acc_1", thread: ThreadID(rawValue: "t_\(n)"), person: "Ann",
        date: Date(timeIntervalSince1970: 1_790_000_000), subject: "Offer", ruleState: .you,
        reply: Board.ReplyTarget(message: "m_2", folder: "f_inbox"),
        draft: draft.map { Board.DraftLink(id: DraftID(rawValue: "\($0)_\(n)"), text: "") }, version: 1)
}

@MainActor
private final class Harness {
    let fake: FakeDaemon
    let drafts = Drafts()
    let client: RPCClient
    let panes: BoardReplyPanes<FakePane>
    var made: [FakePane] = []
    var detached: [FakePane] = []
    var adopted: [FakePane] = []
    var toasts: [String] = []
    /// What the next made pane is set up with.
    var setUp: (FakePane) -> Void = { _ in }

    init(retryFirst: Duration = .milliseconds(20), retryMax: Duration = .milliseconds(80)) async throws {
        fake = try FakeDaemon()
        let d = drafts
        await fake.on(API.DraftGet.name) { try await d.get($0) }
        try await fake.start()
        client = RPCClient(socketPath: fake.path)
        try await client.connect()
        panes = BoardReplyPanes(
            loader: BoardReplyEditorController(client: client),
            timing: .init(retryFirst: retryFirst, retryMax: retryMax))
        panes.make = { [unowned self] key, _ in
            let p = FakePane(key)
            self.setUp(p)
            self.made.append(p)
            return p
        }
        panes.detach = { [unowned self] in self.detached.append($0) }
        panes.onAdopt = { [unowned self] in self.adopted.append($0) }
        panes.onToast = { [unowned self] in self.toasts.append($0) }
    }

    /// Selects `c` and waits until its pane is live.
    @discardableResult
    func open(_ c: Board.Case) async throws -> FakePane {
        panes.show(c)
        try await waitUntil { self.panes.live?.key.caseID == c.id }
        return try #require(panes.live)
    }

    /// Panes made for case `c`.
    func made(for c: Board.Case) -> Int {
        made.filter { $0.key.caseID == c.id }.count
    }

    func isUnsaved(_ c: Board.Case) -> Bool? {
        if case .pane(_, let unsaved) = panes.slot(for: c.id) { return unsaved }
        return nil
    }

    func stop() async {
        await client.close()
        await fake.stop()
    }
}

@MainActor
@Suite(.serialized) struct BoardReplyPanesTests {
    @Test func aReadyDraftGetsAPaneAndTheSameKeyKeepsIt() async throws {
        let h = try await Harness()
        let c1 = boardCase("1")
        let p = try await h.open(c1)
        #expect(h.made.count == 1 && h.adopted.count == 1 && p.key.draft == "d_1")
        // The board lists the case again (every autosave): nothing changes.
        h.panes.show(c1)
        h.panes.show(c1)
        #expect(h.panes.live === p && h.made.count == 1 && p.settles == 0)
        await h.stop()
    }

    @Test func anotherSelectionSavesThenClosesThePane() async throws {
        let h = try await Harness()
        let p = try await h.open(boardCase("1"))
        p.holding = true
        h.panes.show(boardCase("2"))
        try await waitUntil { p.waiting == 1 }
        #expect(!p.closed && h.detached.isEmpty, "not before its save answered")
        p.release()
        try await waitUntil { p.closed }
        #expect(h.detached.first === p && p.settles == 1 && p.abandons == 0)
        await h.stop()
    }

    // Finding 2: Send in flight, then another case.
    @Test func aSendingPaneIsNeverSettledAndItsSuccessArrives() async throws {
        let h = try await Harness()
        let c1 = boardCase("1")
        let p = try await h.open(c1)
        p.isSending = true
        h.panes.show(boardCase("2"))
        try await waitUntil { h.panes.live?.key.caseID.rawValue == "c_2" }
        try await Task.sleep(for: .milliseconds(50))
        #expect(p.settles == 0 && !p.closed && h.detached.isEmpty)
        // The send answers while the user is on case 2.
        p.isSending = false
        h.panes.ended(p, .sent("Message queued for sending"))
        #expect(h.toasts == ["Message queued for sending"])
        #expect(p.closed && h.detached.contains { $0 === p })
        // The loader hides the sent draft while the board still links it.
        h.panes.show(c1)
        try await Task.sleep(for: .milliseconds(50))
        #expect(h.panes.live == nil && h.made.count == 2)
        await h.stop()
    }

    @Test func aFailedSendOutOfSightSaysWhichReplyAndKeepsThePane() async throws {
        let h = try await Harness()
        let c1 = boardCase("1")
        let p = try await h.open(c1)
        p.isSending = true
        p.replyTitle = "Re: Offer"
        h.panes.show(boardCase("2"))
        p.isSending = false
        h.panes.sendFailed(p)
        #expect(h.toasts == [Board.Text.replyNotSent("Re: Offer")])
        try await waitUntil { p.settles == 1 }
        try await Task.sleep(for: .milliseconds(50))
        #expect(!p.closed && h.detached.isEmpty, "kept for the user to come back")
        // Back on case 1: the same pane, Send there again.
        h.panes.show(c1)
        #expect(h.panes.live === p && h.made(for: c1) == 1)
        await h.stop()
    }

    @Test func aFailedSendInSightAddsNothing() async throws {
        let h = try await Harness()
        let p = try await h.open(boardCase("1"))
        h.panes.sendFailed(p)
        #expect(h.toasts.isEmpty && h.panes.live === p && p.settles == 0)
        await h.stop()
    }

    // Finding 3: unsaved text is kept, retried, never trimmed.
    @Test func anUnsavedPaneIsKeptAndRetriedWithBackOff() async throws {
        let h = try await Harness()
        let p = try await h.open(boardCase("1"))
        p.results = [false, false, false]
        h.panes.show(boardCase("2"))
        // Two identical failures do not end the retries.
        try await waitUntil { p.settles == 4 }
        try await waitUntil { p.closed }
        #expect(p.abandons == 0 && h.detached.first === p)
        await h.stop()
    }

    @Test func panesWithUnsavedTextAreNeverTrimmed() async throws {
        let h = try await Harness(retryFirst: .seconds(600), retryMax: .seconds(600))
        h.setUp = { $0.fallback = false }
        for n in 1...6 {
            try await h.open(boardCase("\(n)"))
        }
        h.panes.show(nil)
        try await waitUntil { h.made.allSatisfy { $0.settles == 1 } }
        try await Task.sleep(for: .milliseconds(50))
        #expect(h.made.count == 6)
        #expect(h.made.allSatisfy { !$0.closed && $0.abandons == 0 })
        #expect(h.detached.isEmpty)
        await h.stop()
    }

    @Test func comingBackShowsTheKeptPaneWithItsNote() async throws {
        let h = try await Harness(retryFirst: .seconds(600), retryMax: .seconds(600))
        let c1 = boardCase("1")
        let p = try await h.open(c1)
        p.fallback = false
        h.panes.show(boardCase("2"))
        try await waitUntil { p.settles == 1 }
        h.panes.show(c1)
        #expect(h.panes.live === p && h.made(for: c1) == 1)
        #expect(h.isUnsaved(c1) == true)
        await h.stop()
    }

    @Test func theNoteGoesOnceAKeptPaneSaves() async throws {
        let h = try await Harness()
        let c1 = boardCase("1")
        let p = try await h.open(c1)
        p.results = [false]
        // The retry waits until the user is back.
        p.holdFrom = 2
        h.panes.show(boardCase("2"))
        // The first failed; its retry has begun and waits.
        try await waitUntil { p.waiting == 1 }
        h.panes.show(c1)
        #expect(h.isUnsaved(c1) == true)
        // The retry saves while the user is back in it: the note goes, the
        // pane stays the live one.
        p.release()
        try await waitUntil { h.isUnsaved(c1) == false }
        #expect(h.panes.live === p && !p.closed)
        await h.stop()
    }

    // Finding 4: another link, or none, is not proof the draft went.
    @Test func aChangedLinkSavesThePaneInsteadOfAbandoningIt() async throws {
        let h = try await Harness()
        let p = try await h.open(boardCase("1"))
        h.panes.show(boardCase("1", draft: "other"))
        try await waitUntil { p.closed }
        #expect(p.settles == 1 && p.abandons == 0 && h.toasts.isEmpty)
        // The new link gets its own pane.
        try await waitUntil { h.panes.live?.key.draft == "other_1" }
        let q = try await h.open(boardCase("2"))
        h.panes.show(boardCase("2", draft: nil))
        try await waitUntil { q.closed }
        #expect(q.abandons == 0 && h.toasts.isEmpty)
        await h.stop()
    }

    @Test func onlyTheDraftControllersLostAbandons() async throws {
        let h = try await Harness()
        let p = try await h.open(boardCase("1"))
        p.holding = true
        h.panes.show(boardCase("1", draft: nil))
        try await waitUntil { p.waiting == 1 }
        // The save said draftNotFound: the draft controller abandoned and
        // reported it.
        p.isLost = true
        p.fallback = false
        h.panes.ended(p, .lost)
        p.release()
        try await waitUntil { h.detached.count == 1 }
        try await Task.sleep(for: .milliseconds(50))
        #expect(h.toasts == [Board.Text.replyRemoved])
        #expect(p.settles == 1, "no retry for a lost draft")
        await h.stop()
    }

    // Finding 6.
    @Test func aPaneTakenBackWhileItSavesIsNotReleasedUnderTheUser() async throws {
        let h = try await Harness()
        let c1 = boardCase("1")
        let p = try await h.open(c1)
        p.holding = true
        h.panes.show(boardCase("2"))
        try await waitUntil { p.waiting == 1 }
        // Back at once: the same pane, no loading row while it saves.
        h.panes.show(c1)
        #expect(h.panes.live === p && h.made(for: c1) == 1)
        p.release()
        try await Task.sleep(for: .milliseconds(50))
        #expect(h.panes.live === p && !p.closed && !h.detached.contains { $0 === p })
        // Left again later: saved and closed as usual.
        h.panes.show(nil)
        try await waitUntil { p.closed }
        await h.stop()
    }

    @Test func panesKeptWithNothingAtStakeAreBounded() async throws {
        let h = try await Harness()
        var kept: [FakePane] = []
        for n in 1...(BoardReplyPanes<FakePane>.keptLimit + 1) {
            let p = try await h.open(boardCase("\(n)"))
            p.isSending = true
            h.panes.show(nil)
            p.isSending = false
            h.panes.sendFailed(p)
            try await waitUntil { p.settles == 1 }
            kept.append(p)
        }
        // The oldest is settled once more and closed; the others stay.
        try await waitUntil { kept[0].closed }
        #expect(kept[0].settles == 2)
        #expect(kept.dropFirst().allSatisfy { !$0.closed })
        await h.stop()
    }

    @Test func aDiscardUnderWayIsNotSavedOver() async throws {
        let h = try await Harness()
        let c1 = boardCase("1")
        let p = try await h.open(c1)
        h.panes.discarding(p, true)
        // The board drops the link at once (optimistic).
        h.panes.show(boardCase("1", draft: nil))
        try await Task.sleep(for: .milliseconds(50))
        #expect(h.panes.live === p && p.settles == 0)
        h.panes.ended(p, .discarded)
        #expect(p.closed && h.toasts.isEmpty)
        await h.stop()
    }

    @Test func aFailedDiscardOfARetiredPaneSavesIt() async throws {
        let h = try await Harness()
        let p = try await h.open(boardCase("1"))
        h.panes.discarding(p, true)
        h.panes.show(boardCase("2"))
        try await Task.sleep(for: .milliseconds(50))
        #expect(p.settles == 0)
        h.panes.discarding(p, false)
        try await waitUntil { p.closed }
        #expect(p.settles == 1)
        await h.stop()
    }

    @Test func aDiscardedLinkThatCameBackLoadsAgain() async throws {
        let h = try await Harness()
        let c1 = boardCase("1")
        let p = try await h.open(c1)
        let asked = await h.drafts.asked
        h.panes.ended(p, .discarded)
        #expect(p.closed)
        try await waitUntil { h.panes.live != nil && h.panes.live !== p }
        #expect(await h.drafts.asked == asked + 1)
        await h.stop()
    }

    // The quit.
    @Test func quittingSavesEverything() async throws {
        let h = try await Harness()
        let p = try await h.open(boardCase("1"))
        #expect(await h.panes.finishAll(wait: .seconds(10)))
        #expect(p.settles == 1 && p.closed)
        await h.stop()
    }

    @Test func quittingWithAReplyThatCannotBeSavedSaysSo() async throws {
        let h = try await Harness(retryFirst: .seconds(600), retryMax: .seconds(600))
        let c1 = boardCase("1")
        let p = try await h.open(c1)
        p.fallback = false
        #expect(await h.panes.finishAll(wait: .seconds(10)) == false)
        #expect(!p.closed && p.abandons == 0)
        // The user stays: the pane is there again.
        h.panes.resume()
        h.panes.show(c1)
        #expect(h.panes.live === p && h.isUnsaved(c1) == true)
        await h.stop()
    }

    @Test func quittingTriesAKeptPaneOnceMore() async throws {
        let h = try await Harness(retryFirst: .seconds(600), retryMax: .seconds(600))
        let p = try await h.open(boardCase("1"))
        p.results = [false]
        h.panes.show(nil)
        try await waitUntil { p.settles == 1 }
        #expect(await h.panes.finishAll(wait: .seconds(10)))
        #expect(p.settles == 2 && p.closed)
        await h.stop()
    }

    @Test func quittingWaitsForASendAtMostTheBound() async throws {
        let h = try await Harness()
        let p = try await h.open(boardCase("1"))
        p.isSending = true
        let start = ContinuousClock.now
        #expect(await h.panes.finishAll(wait: .milliseconds(100)) == false)
        #expect(ContinuousClock.now - start >= .milliseconds(100))
        #expect(p.settles == 0 && !p.closed)
        await h.stop()
    }

    @Test func quittingEndsWhenTheSendAnswers() async throws {
        let h = try await Harness()
        let p = try await h.open(boardCase("1"))
        p.isSending = true
        Task { @MainActor in
            try? await Task.sleep(for: .milliseconds(30))
            p.isSending = false
            h.panes.ended(p, .sent("Message queued for sending"))
        }
        #expect(await h.panes.finishAll(wait: .seconds(10)))
        #expect(h.toasts == ["Message queued for sending"])
        await h.stop()
    }
}

// MARK: - The real draft controller behind the rules

// The daemon takes every draft.save of a linked suggested reply as the
// user's edit, so a pane that is loaded, shown, refreshed, moved and left
// without an edit must send none. These run the rules over a real
// ComposeDraftController (owner `.board`) and an editor that writes the
// draft differently from how it was given, against a fake daemon that
// records every call.

/// The inline editor as the draft controller sees it: until `ready` a
/// flush reports nothing; the first flush after it reports the editor's own
/// writing of the loaded draft; typed text is reported by the next flush.
@MainActor
private final class EditorForm: ComposeForm {
    var account = testAccount("acc_1", email: "me@example.invalid")
    var subject = "Re: Offer"
    var attachments: [DraftAttachment] = []
    var html = "<p>Text</p>"
    var text = "Text"
    var ready = false
    var rendering: String? = "<p>Text</p>\n"
    var unreported: String?

    func recipients() -> (to: [Address], cc: [Address], bcc: [Address], ok: Bool) {
        ([Address(name: "Ann", address: "ann@example.org")], [], [], true)
    }
    func editorHTML() -> String { html }
    func editorText() -> String { text }
    func flushEditor(_ done: @escaping @MainActor () -> Void) {
        if ready {
            if let r = rendering {
                rendering = nil
                html = r
            }
            if let u = unreported {
                unreported = nil
                html = "<p>\(u)</p>"
                text = u
            }
        }
        done()
    }
    func setAttachments(_ attachments: [DraftAttachment]) {}
    func setStatus(_ text: String) {}
    func toast(_ text: String) {}
    func setSendEnabled(_ enabled: Bool) {}
    func closeWindow() {}
}

/// A pane over the real draft controller, as `ComposePane` is one.
@MainActor
private final class DraftPane: BoardReplyPane {
    let form = EditorForm()
    let draft: ComposeDraftController

    init(client: RPCClient, settings: Settings, params: ComposeParams) {
        draft = ComposeDraftController(
            client: client, settings: settings, placeholder: { false }, registry: CIDRegistry(),
            autosaveDelay: .seconds(600), owner: .board)
        draft.form = form
        draft.setOriginal(inReplyTo: params.inReplyTo, forwarding: nil)
        draft.setOpened(draftID: params.draftID, version: params.version, replaces: nil, fromDrafts: true)
    }

    /// The page came up (the host's `onReady`).
    func becomeReady() {
        form.ready = true
        draft.editorReady()
    }

    func settle() async -> Bool { await draft.settle() }
    func close() { draft.cleanup() }
    func abandon() { draft.abandon() }
    var hasUnsavedText: Bool { draft.draft.dirty || draft.draft.saving }
    var isSending: Bool { draft.draft.sending }
    var isLost: Bool { draft.lost }
    var replyTitle: String { form.subject }
}

/// draft.get and draft.save, recorded.
private actor Store {
    private(set) var saves: [DraftSaveParams] = []
    private var version = 5

    func get(_ data: Data) throws -> Data {
        let p = try JSONCoding.decoder().decode(DraftGetParams.self, from: data)
        let d = Draft(
            id: p.draftId, accountId: p.accountId, version: version, to: [Address(name: "Ann", address: "ann@example.org")],
            subject: "Re: Offer", textBody: "Text", htmlBody: "<p>Text</p>", inReplyTo: "m_2", local: true)
        return try JSONCoding.encoder().encode(DraftGetResult(draft: d))
    }

    func save(_ data: Data) throws -> Data {
        let p = try JSONCoding.decoder().decode(DraftSaveParams.self, from: data)
        saves.append(p)
        version += 1
        return try JSONCoding.encoder().encode(
            DraftSaveResult(draftId: p.draft.id ?? "d_new", version: version, textBody: p.draft.textBody))
    }
}

@MainActor
@Suite(.serialized) struct BoardReplyPanesDraftTests {
    @MainActor
    private final class Rig {
        let fake: FakeDaemon
        let store = Store()
        let client: RPCClient
        let scratch = ScratchSettings()
        let panes: BoardReplyPanes<DraftPane>
        var made: [DraftPane] = []

        init() async throws {
            fake = try FakeDaemon()
            let s = store
            await fake.on(API.DraftGet.name) { try await s.get($0) }
            await fake.on(API.DraftSave.name) { try await s.save($0) }
            try await fake.start()
            client = RPCClient(socketPath: fake.path)
            try await client.connect()
            panes = BoardReplyPanes(loader: BoardReplyEditorController(client: client))
            panes.make = { [unowned self] _, params in
                let p = DraftPane(client: self.client, settings: self.scratch.settings, params: params)
                self.made.append(p)
                return p
            }
        }

        func open(_ c: Board.Case) async throws -> DraftPane {
            panes.show(c)
            try await waitUntil { self.panes.live?.draft.draft.draftID == c.draft?.id }
            let p = try #require(panes.live)
            p.becomeReady()
            return p
        }

        func stop() async {
            await client.close()
            await fake.stop()
        }
    }

    @Test func aPaneLeftWithoutAnEditSavesNothing() async throws {
        let r = try await Rig()
        let c1 = boardCase("1")
        let p = try await r.open(c1)
        // Board refreshes (every autosave elsewhere, a triage note), and the
        // detail asking from the List's pane and from the panel.
        for _ in 0..<3 {
            r.panes.show(c1)
            _ = r.panes.slot(for: c1.id)
        }
        // Another case, then back and away again, then the quit.
        r.panes.show(boardCase("2"))
        try await waitUntil { p.draft.draft.closed }
        let q = try await r.open(c1)
        r.panes.show(nil)
        try await waitUntil { q.draft.draft.closed }
        #expect(await r.panes.finishAll(wait: .seconds(10)))
        #expect(await r.store.saves.isEmpty)
        await r.stop()
    }

    @Test func anEditIsSavedOnceWhenThePaneIsLeft() async throws {
        let r = try await Rig()
        let c1 = boardCase("1")
        let p = try await r.open(c1)
        // Typed and left within the bridge's debounce.
        p.form.unreported = "Tuesday works"
        r.panes.show(boardCase("2"))
        try await waitUntil { p.draft.draft.closed }
        let saves = await r.store.saves
        #expect(saves.count == 1 && saves.first?.draft.textBody == "Tuesday works")
        #expect(saves.first?.draft.id == "d_1" && saves.first?.draft.version == 5)
        await r.stop()
    }
}
