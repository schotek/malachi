// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The draft lifecycle of a compose window (ui/internal/compose/draft.go)
// over a fake form and a fake daemon.

private struct Timeout: Error {}

@MainActor
private func waitUntil(_ timeout: Duration = .seconds(5), _ cond: @MainActor () async throws -> Bool) async throws {
    let deadline = ContinuousClock.now + timeout
    while try await !cond() {
        if ContinuousClock.now > deadline { throw Timeout() }
        try await Task.sleep(for: .milliseconds(10))
    }
}

/// The daemon's answers and what it was asked, off the main actor.
private actor Script {
    var saveError: RPCError?
    var saveDelay: Duration = .zero
    var saveBlocked = BlockedContent()
    var saveAttachments: [DraftAttachment]?
    var sendError: RPCError?
    private(set) var saves: [DraftSaveParams] = []
    private(set) var sends: [MessageSendParams] = []
    private(set) var deletes: [DraftDeleteParams] = []
    private(set) var removes: [AttachmentRemoveParams] = []

    func set(saveError: RPCError?) { self.saveError = saveError }
    func set(saveDelay: Duration) { self.saveDelay = saveDelay }
    func set(saveBlocked: BlockedContent) { self.saveBlocked = saveBlocked }
    func set(saveAttachments: [DraftAttachment]?) { self.saveAttachments = saveAttachments }
    func set(sendError: RPCError?) { self.sendError = sendError }

    func save(_ params: Data) async throws -> Data {
        saves.append(try decode(DraftSaveParams.self, params))
        if saveDelay > .zero {
            try await Task.sleep(for: saveDelay)
        }
        if let saveError {
            throw saveError
        }
        return try encode(DraftSaveResult(
            draftId: "d1", version: saves.count, textBody: "", blocked: saveBlocked, attachments: saveAttachments))
    }

    func send(_ params: Data) throws -> Data {
        sends.append(try decode(MessageSendParams.self, params))
        if let sendError {
            throw sendError
        }
        return try encode(MessageSendResult(outboxId: "o1"))
    }

    func delete(_ params: Data) throws -> Data {
        deletes.append(try decode(DraftDeleteParams.self, params))
        return try encode(EmptyResult())
    }

    func remove(_ params: Data) throws -> Data {
        removes.append(try decode(AttachmentRemoveParams.self, params))
        return try encode(EmptyResult())
    }

    private func encode<T: Encodable>(_ v: T) throws -> Data { try JSONCoding.encoder().encode(v) }
    private func decode<T: Decodable>(_ t: T.Type, _ d: Data) throws -> T {
        try JSONCoding.decoder().decode(t, from: d.isEmpty ? Data("{}".utf8) : d)
    }
}

/// The compose window as the controller sees it.
@MainActor
private final class FakeForm: ComposeForm {
    var account = testAccount("acc1", email: "me@example.invalid", displayName: "Me")
    var to = ""
    var cc = ""
    var bcc = ""
    var subject = ""
    var attachments: [DraftAttachment] = []
    var html = "<p>hi</p>"
    var text = "hi"
    var flushes = 0
    var statuses: [String] = []
    var toasts: [String] = []
    var sendEnabled: [Bool] = []
    var closes = 0
    var replaced: [[DraftAttachment]] = []

    func recipients() -> (to: [Address], cc: [Address], bcc: [Address], ok: Bool) {
        let t = AddressList.parse(to)
        let c = AddressList.parse(cc)
        let b = AddressList.parse(bcc)
        return (t.addresses, c.addresses, b.addresses, t.invalid.isEmpty && c.invalid.isEmpty && b.invalid.isEmpty)
    }

    func editorHTML() -> String { html }
    func editorText() -> String { text }

    func flushEditor(_ done: @escaping @MainActor () -> Void) {
        flushes += 1
        done()
    }

    func setAttachments(_ list: [DraftAttachment]) {
        attachments = list
        replaced.append(list)
    }

    func setStatus(_ text: String) { statuses.append(text) }
    func toast(_ text: String) { toasts.append(text) }
    func setSendEnabled(_ enabled: Bool) { sendEnabled.append(enabled) }
    func closeWindow() { closes += 1 }
}

private func att(_ id: String, inline: Bool = false, cid: String? = nil) -> DraftAttachment {
    DraftAttachment(id: id, filename: id + ".bin", contentType: "application/octet-stream", size: 10, inline: inline, contentId: cid)
}

/// A value a test changes between calls of a closure that captured it.
@MainActor
private final class Cell<T> {
    var value: T

    init(_ value: T) {
        self.value = value
    }
}

private let conflict = RPCError(code: .conflict, message: "version")
private let serverError = RPCError(code: .serverError, message: "500")

@MainActor
private final class Harness {
    let fake: FakeDaemon
    let script = Script()
    let client: RPCClient
    let scratch: ScratchSettings
    let form = FakeForm()
    let registry = CIDRegistry()
    let draft: ComposeDraftController

    init(autosaveDelay: Duration = .milliseconds(50), connect: Bool = true) async throws {
        fake = try FakeDaemon()
        let script = script
        await fake.on(API.DraftSave.name) { try await script.save($0) }
        await fake.on(API.MessageSend.name) { try await script.send($0) }
        await fake.on(API.DraftDelete.name) { try await script.delete($0) }
        await fake.on(API.AttachmentRemove.name) { try await script.remove($0) }
        try await fake.start()
        client = RPCClient(socketPath: fake.path)
        if connect {
            try await client.connect()
        }
        scratch = ScratchSettings()
        draft = ComposeDraftController(
            client: client, settings: scratch.settings, placeholder: { false },
            registry: registry, autosaveDelay: autosaveDelay)
        draft.form = form
    }

    var calls: [String] {
        get async { await fake.calls }
    }

    /// One explicit save, awaited.
    func saveNow() async -> (any Error)? {
        await withCheckedContinuation { cont in
            draft.save(reason: .explicit) { cont.resume(returning: $0) }
        }
    }

    func stop() async {
        await client.close()
        await fake.stop()
    }
}

@MainActor
@Suite(.serialized) struct DraftStateTests {
    @Test func editArmsTheAutosaveWhichSavesOnce() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        h.form.subject = "Hello"
        h.draft.markDirty()
        #expect(h.draft.draft.dirty)
        #expect(h.draft.autosaveArmed)
        #expect(h.form.statuses.last == "Unsaved changes")
        // A second edit does not arm a second timer.
        h.draft.markDirty()
        try await waitUntil { await h.script.saves.count == 1 && !h.draft.draft.saving }
        #expect(h.draft.draft.draftID == "d1")
        #expect(h.draft.draft.version == 1)
        #expect(!h.draft.draft.dirty)
        #expect(!h.draft.autosaveArmed)
        #expect(h.form.statuses.contains("Saving draft…"))
        #expect(h.form.statuses.last?.hasPrefix("Draft saved ") == true)
        #expect(h.form.flushes == 1)
        let wire = try #require(await h.script.saves.first).draft
        #expect(wire.accountId == "acc1")
        #expect(wire.id == nil)
        #expect(wire.version == 0)
        #expect(wire.subject == "Hello")
        #expect(wire.htmlBody == "<p>hi</p>")
        #expect(wire.textBody == "hi")
        #expect(wire.to == [])
        #expect(wire.attachments == nil)
        // Fires once.
        try await Task.sleep(for: .milliseconds(150))
        #expect(await h.script.saves.count == 1)
        #expect(h.form.toasts.isEmpty)
    }

    @Test func buildCarriesTheRowsAndTheAttachmentIDs() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        h.form.to = "Alice <alice@example.org>, bob@example.org"
        h.form.cc = "carol@example.org"
        h.form.attachments = [att("a1"), att("a2", inline: true, cid: "c2")]
        h.draft.setOriginal(inReplyTo: "m9", forwarding: nil)
        let wire = try #require(h.draft.build())
        #expect(wire.to == [Address(name: "Alice", address: "alice@example.org"), Address(address: "bob@example.org")])
        #expect(wire.cc == [Address(address: "carol@example.org")])
        #expect(wire.bcc == nil)
        #expect(wire.inReplyTo == "m9")
        #expect(wire.forwarding == nil)
        #expect(wire.attachments?.map(\.id) == ["a1", "a2"])
    }

    @Test func saveQueuesDoneBehindTheSaveInFlight() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        await h.script.set(saveDelay: .milliseconds(150))
        var done: [Int] = []
        h.draft.save(reason: .explicit) { _ in done.append(1) }
        #expect(h.draft.draft.saving)
        #expect(h.form.statuses.last == "Saving draft…")
        h.draft.save(reason: .explicit) { _ in done.append(2) }
        try await waitUntil { done == [1, 2] }
        #expect(await h.script.saves.count == 1)
        #expect(h.form.flushes == 1)
        #expect(!h.draft.draft.saving)
        // Edits made during the save arm the autosave again afterwards.
        h.draft.save(reason: .explicit)
        h.draft.markDirty()
        try await waitUntil { !h.draft.draft.saving }
        #expect(h.draft.draft.dirty)
        #expect(h.draft.autosaveArmed)
    }

    @Test func attachmentsAreReplacedWhenTheCountDiffers() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        h.form.attachments = [att("a1")]
        await h.script.set(saveAttachments: [att("a1"), att("a2")])
        #expect(await h.saveNow() == nil)
        #expect(h.form.replaced.map { $0.map(\.id) } == [["a1", "a2"]])
        // The same count: the window's list stands.
        #expect(await h.saveNow() == nil)
        #expect(h.form.replaced.count == 1)
        // A daemon that lists none replaces one.
        await h.script.set(saveAttachments: nil)
        #expect(await h.saveNow() == nil)
        #expect(h.form.replaced.count == 2)
        #expect(h.form.attachments.isEmpty)
    }

    @Test func blockedContentIsSaidOnce() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        await h.script.set(saveBlocked: BlockedContent(remoteImages: 2, scripts: 1))
        #expect(await h.saveNow() == nil)
        #expect(h.form.toasts == ["3 unsafe elements were removed from the message"])
        await h.script.set(saveBlocked: BlockedContent())
        #expect(await h.saveNow() == nil)
        #expect(h.form.toasts.count == 1)
    }

    @Test func conflictStartsOverWithAFreshDraft() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        #expect(await h.saveNow() == nil)
        #expect(h.draft.draft.draftID == "d1")
        await h.script.set(saveError: conflict)
        let err = await h.saveNow()
        #expect((err as? RPCError)?.code == .conflict)
        #expect(h.draft.draft.draftID == nil)
        #expect(h.draft.draft.version == 0)
        #expect(h.draft.draft.dirty)
        #expect(h.form.toasts == ["This draft was changed elsewhere; your text will be saved as a new draft"])
        #expect(!h.draft.autosaveArmed, "a conflict waits for the next edit")
        #expect(h.form.statuses.last == "Unsaved changes")
        // The next save creates a new draft with our text.
        await h.script.set(saveError: nil)
        #expect(await h.saveNow() == nil)
        #expect(await h.script.saves.last?.draft.id == nil)
        #expect(h.draft.draft.draftID == "d1")
    }

    @Test func repeatedAutosaveFailureToastsOnceExplicitSaveAlways() async throws {
        let h = try await Harness(autosaveDelay: .milliseconds(30))
        defer { Task { await h.stop() } }
        await h.script.set(saveError: serverError)
        h.draft.markDirty()
        try await waitUntil { await h.script.saves.count == 1 && !h.draft.draft.saving }
        #expect(h.form.toasts == ["Saving the draft failed: the server returned an error"])
        #expect(h.draft.draft.dirty)
        #expect(h.draft.autosaveArmed, "retried later")
        try await waitUntil { await h.script.saves.count == 2 && !h.draft.draft.saving }
        #expect(h.form.toasts.count == 1, "the same failure is not repeated")
        #expect(!h.draft.autosaveArmed, "the repeat waits for the next edit")
        // An explicit save always says so.
        _ = await h.saveNow()
        #expect(h.form.toasts.count == 2)
        // A different failure is said again by the autosave.
        await h.script.set(saveError: RPCError(code: .networkError, message: "down"))
        h.draft.markDirty()
        try await waitUntil { await h.script.saves.count == 4 && !h.draft.draft.saving }
        #expect(h.form.toasts.last == "Saving the draft failed: the server could not be reached")
        #expect(h.form.toasts.count == 3)
    }

    @Test func sendRefusesBadOrMissingRecipients() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        h.form.to = "not an address"
        h.draft.send()
        #expect(h.form.toasts == ["Fix the highlighted recipients"])
        #expect(!h.draft.draft.sending)
        h.form.to = ""
        h.draft.send()
        #expect(h.form.toasts.last == "Add at least one recipient")
        try await Task.sleep(for: .milliseconds(30))
        #expect(await h.calls.isEmpty)
        #expect(h.form.sendEnabled.isEmpty)
    }

    @Test func sendSavesThenQueuesAndCloses() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        h.form.to = "bob@example.org"
        var sent: [String] = []
        h.draft.onSent = { sent.append($0) }
        h.draft.send()
        #expect(h.draft.draft.sending)
        #expect(h.form.sendEnabled == [false])
        #expect(h.form.statuses.last == "Sending…")
        h.draft.send() // a second click while sending does nothing
        try await waitUntil { h.form.closes == 1 }
        #expect(await h.calls == [API.DraftSave.name, API.MessageSend.name])
        #expect(await h.script.sends == [MessageSendParams(accountId: "acc1", draftId: "d1", version: 1)])
        #expect(sent == ["Message queued for sending"])
        #expect(h.draft.draft.discard)
        #expect(h.form.sendEnabled == [false])
    }

    @Test func sendConflictResetsTheDraftAndReenables() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        h.form.to = "bob@example.org"
        await h.script.set(sendError: conflict)
        h.draft.send()
        try await waitUntil { h.form.sendEnabled == [false, true] }
        #expect(h.draft.draft.draftID == nil)
        #expect(h.draft.draft.version == 0)
        #expect(h.draft.draft.dirty)
        #expect(!h.draft.draft.sending)
        #expect(h.form.toasts == ["Sending conflicted with another change"])
        #expect(h.form.closes == 0)
        #expect(!h.draft.draft.discard)
        #expect(h.form.statuses.last == "Unsaved changes")

        // Any other failure keeps the draft.
        await h.script.set(sendError: serverError)
        h.draft.send()
        try await waitUntil { h.form.sendEnabled == [false, true, false, true] }
        #expect(h.draft.draft.draftID == "d1")
        #expect(h.form.toasts.last == "Sending failed: the server returned an error")

        // A failed save never reaches message.send.
        await h.script.set(saveError: serverError)
        h.draft.send()
        try await waitUntil { h.form.sendEnabled.count == 6 }
        #expect(h.form.toasts.last == "Saving the draft failed: the server returned an error")
        #expect(await h.script.sends.count == 2)
        #expect(!h.draft.draft.sending)
    }

    @Test func discardWithoutConfirmationDeletesTheDraft() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        h.scratch.settings.confirmDelete = false
        #expect(await h.saveNow() == nil)
        h.draft.markDirty()
        h.draft.discard()
        #expect(h.form.closes == 1)
        #expect(h.draft.draft.discard)
        try await waitUntil { await h.script.deletes.count == 1 }
        #expect(await h.script.deletes == [DraftDeleteParams(accountId: "acc1", draftId: "d1")])
        #expect(await h.script.removes.isEmpty)
    }

    @Test func discardOfAnUnsavedDraftReleasesItsAttachments() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        // Confirmation is on, but nothing was typed and nothing saved: no
        // question (the template's files are released all the same).
        var asked = 0
        h.draft.confirmDiscard = { _, _, _ in
            asked += 1
            return true
        }
        h.form.attachments = [att("a1"), att("a2", inline: true, cid: "c2")]
        h.draft.discard()
        #expect(asked == 0)
        #expect(h.form.closes == 1)
        try await waitUntil { await h.script.removes.count == 2 }
        #expect(Set(await h.script.removes.map(\.attachmentId)) == ["a1", "a2"])
        #expect(await h.script.removes.allSatisfy { $0.accountId == "acc1" })
        #expect(await h.script.deletes.isEmpty)
    }

    @Test func discardAsksWhileConfirmationIsOnAndThereIsSomethingToLose() async throws {
        // A long autosave: the edit below must not be saved meanwhile.
        let h = try await Harness(autosaveDelay: .seconds(30))
        defer { Task { await h.stop() } }
        var asked: [(String, String, String)] = []
        let answer = Cell(false)
        h.draft.confirmDiscard = { heading, body, label in
            asked.append((heading, body, label))
            return answer.value
        }
        h.draft.markDirty()
        h.draft.discard()
        try await waitUntil { asked.count == 1 }
        #expect(asked[0].0 == "Discard this message?")
        #expect(asked[0].1 == "")
        #expect(asked[0].2 == "_Discard")
        try await Task.sleep(for: .milliseconds(20))
        #expect(h.form.closes == 0)
        #expect(!h.draft.draft.discard)

        answer.value = true
        h.draft.discard()
        try await waitUntil { h.form.closes == 1 }
        #expect(h.draft.draft.discard)
        // Never saved: nothing to delete, no attachments to release.
        try await Task.sleep(for: .milliseconds(30))
        #expect(await h.calls.isEmpty)
    }

    @Test func closeRequestAnswers() async throws {
        // Clean: closes at once, cleaned up.
        let clean = try await Harness()
        defer { Task { await clean.stop() } }
        var questions = 0
        clean.draft.saveDraftQuestion = {
            questions += 1
            return .cancel
        }
        #expect(clean.draft.canCloseWithoutAsking)
        #expect(await clean.draft.closeRequest())
        #expect(clean.draft.draft.closed)
        #expect(questions == 0)

        // Dirty, Cancel: stays.
        let h = try await Harness()
        defer { Task { await h.stop() } }
        let answer = Cell(DraftCloseAnswer.cancel)
        h.draft.saveDraftQuestion = { answer.value }
        h.draft.markDirty()
        #expect(!h.draft.canCloseWithoutAsking)
        #expect(await h.draft.closeRequest() == false)
        #expect(!h.draft.draft.closed)
        #expect(h.draft.draft.dirty)

        // Dirty, Save Draft, the save fails: stays, the toast says why.
        answer.value = .save
        await h.script.set(saveError: serverError)
        #expect(await h.draft.closeRequest() == false)
        #expect(!h.draft.draft.closed)
        #expect(h.form.toasts == ["Saving the draft failed: the server returned an error"])
        #expect(h.draft.draft.dirty)

        // Dirty, Save Draft, the save succeeds: closes.
        await h.script.set(saveError: nil)
        #expect(await h.draft.closeRequest())
        #expect(await h.script.saves.count == 2)
        #expect(h.draft.draft.discard)
        #expect(h.draft.draft.closed)
        #expect(!h.draft.autosaveArmed)

        // Dirty, Discard: closes without saving.
        let d = try await Harness()
        defer { Task { await d.stop() } }
        d.draft.saveDraftQuestion = { .discard }
        d.draft.markDirty()
        #expect(await d.draft.closeRequest())
        #expect(d.draft.draft.discard)
        #expect(d.draft.draft.closed)
        try await Task.sleep(for: .milliseconds(30))
        #expect(await d.calls.isEmpty)

        // Saving in flight: the question is asked as well.
        let s = try await Harness()
        defer { Task { await s.stop() } }
        await s.script.set(saveDelay: .milliseconds(100))
        s.draft.saveDraftQuestion = { .cancel }
        s.draft.save(reason: .explicit)
        #expect(!s.draft.canCloseWithoutAsking)
        #expect(await s.draft.closeRequest() == false)
    }

    @Test func cleanupDisarmsTheAutosaveAndForgetsInlinePictures() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        h.registry.register("c1", path: "/tmp/x.png", contentType: "image/png")
        h.registry.register("other", path: "/tmp/y.png", contentType: "image/png")
        h.form.attachments = [att("a1", inline: true, cid: "c1"), att("a2")]
        h.draft.markDirty()
        h.draft.cleanup()
        #expect(h.draft.draft.closed)
        #expect(!h.draft.autosaveArmed)
        #expect(!h.registry.isRegistered("c1"))
        #expect(h.registry.isRegistered("other"), "only the window's own pictures")
        h.draft.cleanup() // idempotent
        try await Task.sleep(for: .milliseconds(120))
        #expect(await h.calls.isEmpty, "the autosave never fired")
    }

    @Test func repliesAfterTheWindowClosedAreDropped() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        await h.script.set(saveDelay: .milliseconds(100))
        var outcomes: [(any Error)?] = []
        h.draft.save(reason: .explicit) { outcomes.append($0) }
        let statuses = h.form.statuses.count
        h.draft.cleanup()
        // The awaited outcome is answered with a cancellation at once…
        #expect(outcomes.count == 1)
        #expect(outcomes[0] is CancellationError)
        // …and the daemon's late reply changes nothing.
        try await waitUntil { await h.script.saves.count == 1 }
        try await Task.sleep(for: .milliseconds(200))
        #expect(h.draft.draft.draftID == nil)
        #expect(h.draft.draft.saving, "left as it was")
        #expect(h.form.statuses.count == statuses)
        #expect(h.form.toasts.isEmpty)
    }

    @Test func statusLadder() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        h.draft.refreshStatus()
        #expect(h.form.statuses.last == "")
        // The placeholder identity is announced while nothing else is.
        let p = try await Harness()
        defer { Task { await p.stop() } }
        let placeholderDraft = ComposeDraftController(
            client: p.client, settings: p.scratch.settings, placeholder: { true }, registry: p.registry)
        placeholderDraft.form = p.form
        placeholderDraft.refreshStatus()
        #expect(p.form.statuses.last == "Using placeholder account")
        placeholderDraft.markDirty()
        #expect(p.form.statuses.last == "Unsaved changes")
        placeholderDraft.cleanup()
    }

    @Test func withoutADaemonTheSaveFailsAndTheAutosaveRetries() async throws {
        let h = try await Harness(autosaveDelay: .milliseconds(30), connect: false)
        defer { Task { await h.stop() } }
        h.draft.markDirty()
        try await waitUntil { !h.form.toasts.isEmpty }
        #expect(h.form.toasts == ["Saving the draft needs a running mail backend"])
        #expect(h.draft.draft.dirty)
        #expect(h.draft.autosaveArmed)
    }
}
