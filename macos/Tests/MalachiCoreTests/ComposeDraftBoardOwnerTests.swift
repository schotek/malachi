// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// ComposeDraftController with `owner: .board` (a board case's suggested
// reply edited inline): the board keeps the draft, so nothing here deletes
// it on cleanup or after a late save, closing never asks, `finish` saves
// what was typed and says whether anything was lost, a draft deleted
// elsewhere is reported (`onLost`) and never recreated, a conflict keeps our
// text in the same draft (draft.get for the version), and Discard goes
// through `discardStored`. The window owner's behaviour is covered by the
// compose suites; a few contrasts are repeated here.

private struct Timeout: Error {}

@MainActor
private func waitUntil(_ timeout: Duration = .seconds(10), _ cond: @MainActor () async throws -> Bool) async throws {
    let deadline = ContinuousClock.now + timeout
    while try await !cond() {
        if ContinuousClock.now > deadline { throw Timeout() }
        try await Task.sleep(for: .milliseconds(10))
    }
}

/// The daemon's answers and what it was asked, off the main actor.
private actor Script {
    /// Errors for the next saves, in order; a save past the list succeeds.
    var saveErrors: [RPCError] = []
    var saveDelay: Duration = .zero
    var sendError: RPCError?
    var sendDelay: Duration = .zero
    var getError: RPCError?
    /// The version the stored draft has (draft.get answers it; a save
    /// succeeds with one more).
    var stored = 3
    private(set) var saves: [DraftSaveParams] = []
    private(set) var sends: [MessageSendParams] = []
    private(set) var deletes: [DraftDeleteParams] = []
    private(set) var gets: [DraftGetParams] = []

    func set(saveErrors: [RPCError]) { self.saveErrors = saveErrors }
    func set(saveDelay: Duration) { self.saveDelay = saveDelay }
    func set(sendError: RPCError?) { self.sendError = sendError }
    func set(sendDelay: Duration) { self.sendDelay = sendDelay }
    func set(getError: RPCError?) { self.getError = getError }
    func set(stored: Int) { self.stored = stored }

    func save(_ params: Data) async throws -> Data {
        let p = try decode(DraftSaveParams.self, params)
        saves.append(p)
        if saveDelay > .zero {
            try await Task.sleep(for: saveDelay)
        }
        if !saveErrors.isEmpty {
            throw saveErrors.removeFirst()
        }
        stored += 1
        return try encode(DraftSaveResult(draftId: p.draft.id ?? DraftID("d_new\(saves.count)"), version: stored, textBody: p.draft.textBody))
    }

    func get(_ params: Data) throws -> Data {
        let p = try decode(DraftGetParams.self, params)
        gets.append(p)
        if let getError {
            throw getError
        }
        return try encode(DraftGetResult(draft: Draft(id: p.draftId, accountId: p.accountId, version: stored, local: true)))
    }

    func send(_ params: Data) async throws -> Data {
        sends.append(try decode(MessageSendParams.self, params))
        if sendDelay > .zero {
            try await Task.sleep(for: sendDelay)
        }
        if let sendError {
            throw sendError
        }
        return try encode(MessageSendResult(outboxId: "o1"))
    }

    func delete(_ params: Data) throws -> Data {
        deletes.append(try decode(DraftDeleteParams.self, params))
        return try encode(EmptyResult())
    }

    private func encode<T: Encodable>(_ v: T) throws -> Data { try JSONCoding.encoder().encode(v) }
    private func decode<T: Decodable>(_ t: T.Type, _ d: Data) throws -> T {
        try JSONCoding.decoder().decode(t, from: d.isEmpty ? Data("{}".utf8) : d)
    }
}

/// The inline editor as the controller sees it: a reply with one
/// recipient, or a comment on an issue.
@MainActor
private final class Form: ComposeForm {
    var account = testAccount("a", email: "me@example.invalid")
    var isComment = false
    var commentVisibility: CommentVisibility = .public
    var subject = "Re: Offer"
    var attachments: [DraftAttachment] = []
    var html = "<p>Thanks, Ann.</p>"
    var text = "Thanks, Ann."
    var statuses: [String] = []
    var toasts: [String] = []
    var sendEnabled: [Bool] = []
    var closes = 0
    var flushes = 0
    /// Typed, but not reported yet (the bridge's debounce): the next flush
    /// reports it.
    var unreported: String?
    /// The page is up (`ready`); before, a flush reports nothing.
    var ready = true
    /// How the editor writes the HTML it was given (it normalises it): the
    /// first flush once ready reports this. nil: as given.
    var rendering: String?

    func recipients() -> (to: [Address], cc: [Address], bcc: [Address], ok: Bool) {
        ([Address(name: "Ann", address: "ann@example.org")], [], [], true)
    }
    func editorHTML() -> String { html }
    func editorText() -> String { text }
    func flushEditor(_ done: @escaping @MainActor () -> Void) {
        flushes += 1
        guard ready else {
            done()
            return
        }
        if let r = rendering {
            rendering = nil
            html = r
        }
        if let s = unreported {
            unreported = nil
            type(s)
        }
        done()
    }
    func setAttachments(_ list: [DraftAttachment]) { attachments = list }
    func setStatus(_ text: String) { statuses.append(text) }
    func toast(_ text: String) { toasts.append(text) }
    func setSendEnabled(_ enabled: Bool) { sendEnabled.append(enabled) }
    func closeWindow() { closes += 1 }

    func type(_ s: String) {
        html = "<p>\(s)</p>"
        text = s
    }
}

private let boardDraft: DraftID = "d_board"

@MainActor
private final class Harness {
    let fake: FakeDaemon
    let script = Script()
    let client: RPCClient
    let scratch = ScratchSettings()
    let form = Form()
    let draft: ComposeDraftController
    var lost = 0
    var sent: [String] = []
    var sendFailures = 0
    var questions = 0
    var stored: [(AccountID, DraftID)] = []
    var storedError: (any Error)?

    /// - autosaveDelay: long by default, so no autosave interferes; the
    ///   retry tests pass a short one.
    init(owner: DraftOwner = .board, comment: Bool = false, autosaveDelay: Duration = .seconds(600)) async throws {
        fake = try FakeDaemon()
        let script = script
        await fake.on(API.DraftSave.name) { try await script.save($0) }
        await fake.on(API.DraftGet.name) { try await script.get($0) }
        await fake.on(API.MessageSend.name) { try await script.send($0) }
        await fake.on(API.DraftDelete.name) { try await script.delete($0) }
        try await fake.start()
        client = RPCClient(socketPath: fake.path)
        try await client.connect()
        draft = ComposeDraftController(
            client: client, settings: scratch.settings, placeholder: { false },
            registry: CIDRegistry(), autosaveDelay: autosaveDelay, owner: owner)
        form.isComment = comment
        draft.form = form
        // As the inline pane does with the draft draft.get returned.
        if comment {
            let issue = IssueInfo(
                key: "WEB-7", url: "https://acme.atlassian.net/browse/WEB-7", summary: "Logo",
                status: "To Do", statusCategory: .todo)
            draft.setOriginal(inReplyTo: "w1", forwarding: nil, comment: DraftComment(issue: issue, visibility: .public))
        } else {
            draft.setOriginal(inReplyTo: "m_2", forwarding: nil)
        }
        draft.setOpened(draftID: boardDraft, version: 3, replaces: nil, fromDrafts: true)
        draft.onLost = { [unowned self] in self.lost += 1 }
        draft.onSent = { [unowned self] in self.sent.append($0) }
        draft.onSendFailed = { [unowned self] in self.sendFailures += 1 }
        draft.confirmDiscard = { _, _, _ in true }
        draft.saveDraftQuestion = { [unowned self] in
            self.questions += 1
            return .discard
        }
    }

    /// Saves now and waits for the outcome.
    func save(_ reason: SaveReason = .autosave) async -> (any Error)? {
        await withCheckedContinuation { cont in
            draft.save(reason: reason) { cont.resume(returning: $0) }
        }
    }

    func stop() async {
        await client.close()
        await fake.stop()
    }
}

@MainActor
@Suite(.serialized) struct ComposeDraftBoardOwnerTests {
    @Test func theOwnerDefaultsToTheWindow() async throws {
        let h = try await Harness(owner: .window)
        defer { Task { await h.stop() } }
        #expect(h.draft.owner == .window)
        let b = try await Harness()
        defer { Task { await b.stop() } }
        #expect(b.draft.owner == .board)
    }

    @Test func closingNeverAsksAndNeverDeletes() async throws {
        for comment in [false, true] {
            let h = try await Harness(comment: comment)
            h.draft.markDirty()
            #expect(h.draft.canCloseWithoutAsking, "dirty, yet the board keeps it")
            #expect(await h.draft.closeRequest())
            #expect(h.questions == 0)
            #expect(h.draft.draft.closed)
            try await Task.sleep(for: .milliseconds(100))
            #expect(await h.script.deletes.isEmpty, "comment: \(comment)")
            await h.stop()
        }
    }

    @Test func theWindowStillDeletesAClosedComment() async throws {
        // The contrast: a comment window's copy goes with the window.
        let h = try await Harness(owner: .window, comment: true)
        defer { Task { await h.stop() } }
        h.draft.cleanup()
        try await waitUntil { await h.script.deletes.count == 1 }
        #expect(await h.script.deletes.first?.draftId == boardDraft)
    }

    @Test func aLateSaveNeverDeletes() async throws {
        for comment in [false, true] {
            let h = try await Harness(comment: comment)
            await h.script.set(saveDelay: .milliseconds(200))
            h.draft.markDirty()
            h.draft.save(reason: .autosave)
            try await waitUntil { await h.script.saves.count == 1 }
            h.draft.cleanup()
            // The save answers after the cleanup; give it time to.
            try await Task.sleep(for: .milliseconds(500))
            #expect(await h.script.deletes.isEmpty, "comment: \(comment)")
            await h.stop()
        }
    }

    @Test func finishSavesWhatWasTyped() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        h.form.type("Thanks, Ann. Tuesday works.")
        h.draft.markDirty()
        #expect(await h.draft.finish())
        let saves = await h.script.saves
        #expect(saves.count == 1)
        #expect(saves.first?.draft.id == boardDraft && saves.first?.draft.version == 3)
        #expect(saves.first?.draft.textBody == "Thanks, Ann. Tuesday works.")
        #expect(h.draft.draft.closed && !h.draft.autosaveArmed)
        #expect(h.form.flushes >= 1)
        #expect(await h.script.deletes.isEmpty)
        // Finished: a second call changes nothing.
        #expect(await h.draft.finish())
        #expect(await h.script.saves.count == 1)
    }

    @Test func finishWithNothingTypedSavesNothing() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        #expect(await h.draft.finish())
        #expect(await h.script.saves.isEmpty)
        #expect(h.draft.draft.closed)
    }

    @Test func finishWaitsForASaveUnderWayAndSavesLaterEdits() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        await h.script.set(saveDelay: .milliseconds(150))
        h.draft.markDirty()
        h.draft.save(reason: .autosave)
        try await waitUntil { await h.script.saves.count == 1 }
        // Typed while the save runs.
        h.form.type("Second thought")
        h.draft.markDirty()
        #expect(await h.draft.finish())
        let saves = await h.script.saves
        #expect(saves.count == 2)
        #expect(saves.last?.draft.textBody == "Second thought")
        #expect(saves.last?.draft.version == 4, "the version the first save returned")
    }

    @Test func finishSeesAnEditReportedOnlyByTheFlush() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        h.draft.markDirty()
        #expect(await h.save() == nil)
        // The editor reports this content with the flush, too late to mark
        // the draft dirty.
        h.form.type("Changed just before leaving")
        #expect(!h.draft.draft.dirty)
        #expect(await h.draft.finish())
        let saves = await h.script.saves
        #expect(saves.count == 2 && saves.last?.draft.textBody == "Changed just before leaving")
    }

    @Test func finishSeesAnEditReportedOnlyByTheFlushBeforeAnySave() async throws {
        // The user typed and left within the debounce: nothing was
        // reported, nothing saved yet in this pane's life.
        let h = try await Harness()
        defer { Task { await h.stop() } }
        h.form.rendering = "<p>Thanks, Ann.</p>\n"
        h.draft.editorReady()
        h.form.unreported = "Thanks, Tuesday works"
        #expect(!h.draft.draft.dirty)
        #expect(await h.draft.finish())
        let saves = await h.script.saves
        #expect(saves.count == 1 && saves.first?.draft.textBody == "Thanks, Tuesday works")
        #expect(saves.first?.draft.id == boardDraft)
    }

    @Test func settleSavesAndLeavesTheControllerOpen() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        h.draft.editorReady()
        h.form.unreported = "Kept open"
        #expect(await h.draft.settle())
        #expect(await h.script.saves.count == 1)
        #expect(!h.draft.draft.closed && !h.draft.draft.dirty)
        // Still usable: a later edit is saved by the next settle.
        h.form.type("And more")
        h.draft.markDirty()
        #expect(await h.draft.settle())
        #expect(await h.script.saves.last?.draft.textBody == "And more")
        #expect(!h.draft.draft.closed)
    }

    // The daemon takes any draft.save of a linked suggestion as the user's
    // edit: nothing is saved unless the user changed something.
    @Test func anUntouchedPaneSavesNothing() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        // The editor writes the draft differently from how it was given.
        h.form.rendering = "<p>Thanks, Ann.</p>\n"
        h.draft.editorReady()
        #expect(await h.draft.settle())
        #expect(await h.draft.settle())
        #expect(await h.draft.finish())
        #expect(await h.script.saves.isEmpty)
        #expect(h.form.flushes >= 3)
    }

    @Test func theEditorsNormalisationBeforeItIsKnownIsNoEdit() async throws {
        // Left before the page was up, or the host never said `ready`: the
        // flush reports the editor's own writing, which is not compared.
        for ready in [false, true] {
            let h = try await Harness()
            h.form.ready = ready
            h.form.rendering = "<p>Thanks, Ann.</p>\n"
            #expect(await h.draft.finish())
            #expect(await h.script.saves.isEmpty, "ready: \(ready)")
            await h.stop()
        }
    }

    @Test func anEditSavesExactlyOnce() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        h.form.rendering = "<p>Thanks, Ann.</p>\n"
        h.draft.editorReady()
        // Reported by the bridge as the user typed.
        h.form.type("Tuesday works")
        h.draft.markDirty()
        // And a little more, reported only by the flush.
        h.form.unreported = "Tuesday works for me"
        #expect(await h.draft.settle())
        #expect(await h.draft.finish())
        let saves = await h.script.saves
        #expect(saves.count == 1 && saves.first?.draft.textBody == "Tuesday works for me")
    }

    @Test func settleWaitsForASendUnderWay() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        await h.script.set(sendDelay: .milliseconds(150))
        h.draft.send()
        try await waitUntil { await h.script.sends.count == 1 }
        #expect(h.draft.draft.sending)
        #expect(await h.draft.settle())
        // The outcome arrived before settle went on.
        #expect(h.sent == ["Message queued for sending"] && h.form.closes == 1)
        #expect(await h.script.saves.count == 1)
    }

    @Test func finishWhileSendingDeliversAFailure() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        await h.script.set(sendDelay: .milliseconds(150))
        await h.script.set(sendError: RPCError(code: .storageError, message: "disk"))
        h.draft.send()
        try await waitUntil { await h.script.sends.count == 1 }
        // The pane is retired while the send is on its way.
        #expect(await h.draft.finish())
        #expect(h.sendFailures == 1 && h.sent.isEmpty)
        #expect(h.form.toasts.contains { $0.hasPrefix("Sending") })
        #expect(h.form.sendEnabled.last == true)
    }

    @Test func aFailedSendIsReported() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        await h.script.set(sendError: RPCError(code: .storageError, message: "disk"))
        h.draft.send()
        try await waitUntil { h.sendFailures == 1 }
        #expect(!h.draft.draft.sending && !h.draft.draft.closed)
        // A failed save before the send counts too.
        await h.script.set(sendError: nil)
        await h.script.set(saveErrors: [RPCError(code: .storageError, message: "disk")])
        h.draft.markDirty()
        h.draft.send()
        try await waitUntil { h.sendFailures == 2 }
        // Lost is not a failed send.
        await h.script.set(sendError: RPCError(code: .draftNotFound, message: "gone"))
        h.draft.send()
        try await waitUntil { h.lost == 1 }
        #expect(h.sendFailures == 2)
    }

    @Test func aFailedFinishKeepsTheTextAndTheController() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        await h.script.set(saveErrors: [RPCError(code: .storageError, message: "disk")])
        h.draft.markDirty()
        #expect(await h.draft.finish() == false)
        #expect(!h.draft.draft.closed && h.draft.draft.dirty && h.draft.autosaveArmed)
        #expect(h.form.toasts.count == 1)
        #expect(await h.script.deletes.isEmpty)
        // Called again (the host kept the form), it saves.
        #expect(await h.draft.finish())
        #expect(h.draft.draft.closed)
        let saves = await h.script.saves
        #expect(saves.count == 2 && saves.allSatisfy { $0.draft.id == boardDraft })
    }

    @Test func aDraftDeletedElsewhereIsLostNotRecreated() async throws {
        let h = try await Harness(autosaveDelay: .milliseconds(50))
        defer { Task { await h.stop() } }
        await h.script.set(saveErrors: [RPCError(code: .draftNotFound, message: "gone")])
        h.draft.markDirty()
        let err = await h.save()
        #expect(err != nil)
        #expect(h.lost == 1 && h.draft.lost)
        #expect(h.draft.draft.closed && !h.draft.autosaveArmed)
        #expect(h.form.toasts.isEmpty, "the host says it, once")
        // Nothing more is saved: no new draft without a case.
        h.draft.markDirty()
        h.draft.save(reason: .explicit)
        try await Task.sleep(for: .milliseconds(300))
        let saves = await h.script.saves
        #expect(saves.count == 1)
        #expect(saves.allSatisfy { $0.draft.id == boardDraft })
        #expect(await h.draft.finish() == false)
        #expect(h.lost == 1)
    }

    @Test func theWindowStillStartsANewDraftWhenItsDraftWent() async throws {
        let h = try await Harness(owner: .window)
        defer { Task { await h.stop() } }
        await h.script.set(saveErrors: [RPCError(code: .draftNotFound, message: "gone")])
        h.draft.markDirty()
        _ = await h.save()
        #expect(h.draft.draft.draftID == nil && h.lost == 0)
        #expect(h.form.toasts == ["This draft was removed elsewhere; your text will be saved as a new draft"])
    }

    @Test func aConflictKeepsOurTextInTheSameDraft() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        // Saved elsewhere meanwhile: the stored draft is at version 7.
        await h.script.set(stored: 7)
        await h.script.set(saveErrors: [RPCError(code: .conflict, message: "version")])
        h.form.type("Ours")
        h.draft.markDirty()
        #expect(await h.save() != nil)
        try await waitUntil { await h.script.gets.count == 1 }
        #expect(await h.script.gets.first == DraftGetParams(accountId: "a", draftId: boardDraft))
        try await waitUntil { h.draft.draft.version == 7 }
        #expect(h.draft.draft.draftID == boardDraft && h.draft.draft.dirty)
        #expect(h.form.toasts.isEmpty)
        // The next save goes over it with our text.
        #expect(await h.draft.finish())
        let saves = await h.script.saves
        #expect(saves.count == 2)
        #expect(saves.last?.draft.id == boardDraft && saves.last?.draft.version == 7)
        #expect(saves.last?.draft.textBody == "Ours")
    }

    @Test func finishRidesOutAConflict() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        await h.script.set(stored: 9)
        await h.script.set(saveErrors: [RPCError(code: .conflict, message: "version")])
        h.draft.markDirty()
        #expect(await h.draft.finish())
        let saves = await h.script.saves
        #expect(saves.count == 2 && saves.last?.draft.version == 9)
        #expect(await h.script.gets.count == 1)
    }

    @Test func aConflictOnADraftThatWentIsLost() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        await h.script.set(saveErrors: [RPCError(code: .conflict, message: "version")])
        await h.script.set(getError: RPCError(code: .draftNotFound, message: "gone"))
        h.draft.markDirty()
        _ = await h.save()
        try await waitUntil { h.lost == 1 }
        #expect(h.draft.draft.closed)
        #expect(await h.script.saves.count == 1)
    }

    @Test func discardGoesThroughTheHost() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        h.draft.discardStored = { [unowned h] account, id in
            h.stored.append((account, id))
        }
        h.draft.markDirty()
        h.draft.discard()
        try await waitUntil { h.form.closes == 1 }
        #expect(h.stored.count == 1 && h.stored.first?.0 == "a" && h.stored.first?.1 == boardDraft)
        #expect(await h.script.deletes.isEmpty)
        #expect(h.draft.draft.discard)
    }

    @Test func discardWithoutAHostDeletesTheDraft() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        h.draft.discard()
        try await waitUntil { h.form.closes == 1 }
        #expect(await h.script.deletes == [DraftDeleteParams(accountId: "a", draftId: boardDraft)])
    }

    @Test func aFailedDiscardKeepsTheForm() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        h.draft.discardStored = { _, _ in throw RPCError(code: .storageError, message: "disk") }
        h.draft.discard()
        try await waitUntil { h.form.toasts.count == 1 }
        #expect(h.form.closes == 0 && !h.draft.draft.closed)
        #expect(h.form.toasts.first?.hasPrefix("Discarding the draft") == true)
    }

    @Test func abandonForgetsWithoutSavingOrDeleting() async throws {
        let h = try await Harness(autosaveDelay: .milliseconds(30))
        defer { Task { await h.stop() } }
        h.form.type("Not kept")
        h.draft.markDirty()
        h.draft.abandon()
        h.draft.abandon()
        #expect(h.draft.draft.closed && !h.draft.autosaveArmed)
        try await Task.sleep(for: .milliseconds(200))
        #expect(await h.script.saves.isEmpty)
        #expect(await h.script.deletes.isEmpty)
        #expect(h.lost == 0)
    }

    @Test func sendingQueuesAndEnds() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        h.draft.send()
        try await waitUntil { h.form.closes == 1 }
        #expect(await h.script.sends == [MessageSendParams(accountId: "a", draftId: boardDraft, version: 4)])
        #expect(h.sent == ["Message queued for sending"])
        #expect(await h.draft.finish(), "sent: nothing to save")
        #expect(await h.script.deletes.isEmpty)
    }

    @Test func sendingADraftThatWentIsLost() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        await h.script.set(sendError: RPCError(code: .draftNotFound, message: "gone"))
        h.draft.send()
        try await waitUntil { h.lost == 1 }
        #expect(h.form.closes == 0 && h.sent.isEmpty)
        #expect(await h.script.saves.count == 1)
    }

    @Test func aSendConflictKeepsTheDraft() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        await h.script.set(sendError: RPCError(code: .conflict, message: "version"))
        h.draft.send()
        try await waitUntil { await h.script.gets.count == 1 }
        #expect(h.draft.draft.draftID == boardDraft)
        try await waitUntil { h.form.sendEnabled.last == true }
    }

    @Test func registerFeedsAnInlineFormTheAccounts() async throws {
        let fake = try FakeDaemon()
        let accounts = [testAccount("a", email: "me@example.org")]
        await fake.on(API.AccountList.name) { _ in
            try JSONCoding.encoder().encode(AccountListResult(accounts: accounts))
        }
        try await fake.start()
        let client = RPCClient(socketPath: fake.path)
        try await client.connect()
        defer {
            Task {
                await client.close()
                await fake.stop()
            }
        }
        let scratch = ScratchSettings()
        let c = ComposeController(client: client, settings: scratch.settings)
        let pane = Handle()
        c.register(pane)
        c.register(pane)
        #expect(c.openWindows.count == 1)
        try await waitUntil { pane.lists.count == 1 }
        #expect(pane.lists.first?.map(\.id) == ["a"])
        // Registered once the list is known: it gets it at once.
        let second = Handle()
        c.register(second)
        #expect(second.lists.count == 1)
        c.remove(pane)
        c.remove(second)
        #expect(c.openWindows.isEmpty)
    }
}

@MainActor
private final class Handle: ComposeWindowHandle {
    var lists: [[Account]] = []
    func setAccounts(_ accounts: [Account], placeholder: Bool) { lists.append(accounts) }
    func toast(_ text: String) {}
}
