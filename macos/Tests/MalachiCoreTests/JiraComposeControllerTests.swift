// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The comment mode of the compose window, the controller half
// (ComposeDraftController over a fake form and a fake daemon): a comment
// draft from draft.create opens it (`fromDraft`), it is sent without
// recipients but not empty, draft.save carries the chosen visibility, and
// there is no Save Draft: closing asks whether to discard, and the copy the
// autosave kept goes with the window.

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
    var sendError: RPCError?
    private(set) var saves: [DraftSaveParams] = []
    private(set) var sends: [MessageSendParams] = []
    private(set) var deletes: [DraftDeleteParams] = []

    func set(saveError: RPCError?) { self.saveError = saveError }
    func set(saveDelay: Duration) { self.saveDelay = saveDelay }
    func set(sendError: RPCError?) { self.sendError = sendError }

    func save(_ params: Data) async throws -> Data {
        let p = try decode(DraftSaveParams.self, params)
        saves.append(p)
        if saveDelay > .zero {
            try await Task.sleep(for: saveDelay)
        }
        if let saveError {
            throw saveError
        }
        return try encode(DraftSaveResult(draftId: DraftID("d\(saves.count)"), version: 1, textBody: p.draft.textBody))
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

    private func encode<T: Encodable>(_ v: T) throws -> Data { try JSONCoding.encoder().encode(v) }
    private func decode<T: Decodable>(_ t: T.Type, _ d: Data) throws -> T {
        try JSONCoding.decoder().decode(t, from: d.isEmpty ? Data("{}".utf8) : d)
    }
}

private let jiraAccount: AccountID = "j"

/// A service-desk request: its comments may be public or internal.
private let request = IssueInfo(
    key: "ITSD-42", url: "https://acme.atlassian.net/browse/ITSD-42", summary: "The printer on the third floor",
    status: "Waiting for Support", statusCategory: .inProgress, commentVisibilities: [.public, .internal]
)

/// An ordinary issue: public comments only.
private let issue = IssueInfo(
    key: "WEB-7", url: "https://acme.atlassian.net/browse/WEB-7", summary: "Logo on the front page",
    status: "To Do", statusCategory: .todo
)

/// The comment draft draft.create returns for a reply to `w1` of `issue`.
private func commentDraft(_ info: IssueInfo = request, visibility: CommentVisibility = "") -> Draft {
    Draft(
        accountId: jiraAccount, subject: info.key + ": " + info.summary, inReplyTo: "w1",
        comment: DraftComment(issue: info, visibility: visibility)
    )
}

/// The compose window in comment mode, as the controller sees it: no
/// recipient rows (they would parse as empty), the visibility control.
@MainActor
private final class CommentForm: ComposeForm {
    var account = Account(
        id: jiraAccount, config: AccountConfig(name: "Acme Jira", email: "jana@acme.example", kind: .jira),
        enabled: true, state: SyncState(accountId: jiraAccount, status: .idle), capabilities: [.comment, .forward]
    )
    var isComment = true
    var commentVisibility: CommentVisibility = .public
    var subject = "ITSD-42: The printer on the third floor"
    var attachments: [DraftAttachment] = []
    var html = "<p>Replaced the toner.</p>"
    var text = "Replaced the toner."
    var statuses: [String] = []
    var toasts: [String] = []
    var sendEnabled: [Bool] = []
    var closes = 0

    func recipients() -> (to: [Address], cc: [Address], bcc: [Address], ok: Bool) { ([], [], [], true) }
    func editorHTML() -> String { html }
    func editorText() -> String { text }
    func flushEditor(_ done: @escaping @MainActor () -> Void) { done() }
    func setAttachments(_ list: [DraftAttachment]) { attachments = list }
    func setStatus(_ text: String) { statuses.append(text) }
    func toast(_ text: String) { toasts.append(text) }
    func setSendEnabled(_ enabled: Bool) { sendEnabled.append(enabled) }
    func closeWindow() { closes += 1 }
}

/// An e-mail window written before comments: none of their members.
@MainActor
private final class MailForm: ComposeForm {
    var account = testAccount("a", email: "me@example.invalid")
    var subject = ""
    var attachments: [DraftAttachment] = []

    func recipients() -> (to: [Address], cc: [Address], bcc: [Address], ok: Bool) { ([], [], [], true) }
    func editorHTML() -> String { "" }
    func editorText() -> String { "" }
    func flushEditor(_ done: @escaping @MainActor () -> Void) { done() }
    func setAttachments(_ list: [DraftAttachment]) {}
    func setStatus(_ text: String) {}
    func toast(_ text: String) {}
    func setSendEnabled(_ enabled: Bool) {}
    func closeWindow() {}
}

@MainActor
private final class Harness {
    let fake: FakeDaemon
    let script = Script()
    let client: RPCClient
    let scratch = ScratchSettings()
    let form = CommentForm()
    let draft: ComposeDraftController
    var sent: [String] = []
    var discardQuestions = 0
    var saveQuestions = 0
    var discardAnswer = true

    init(comment: Draft = commentDraft(), autosaveDelay: Duration = .milliseconds(50)) async throws {
        fake = try FakeDaemon()
        let script = script
        await fake.on(API.DraftSave.name) { try await script.save($0) }
        await fake.on(API.MessageSend.name) { try await script.send($0) }
        await fake.on(API.DraftDelete.name) { try await script.delete($0) }
        try await fake.start()
        client = RPCClient(socketPath: fake.path)
        try await client.connect()
        draft = ComposeDraftController(
            client: client, settings: scratch.settings, placeholder: { false },
            registry: CIDRegistry(), autosaveDelay: autosaveDelay)
        draft.form = form
        // As the window does with its parameters.
        let p = fromDraft(kind: .reply, draft: comment, blocked: BlockedContent())
        draft.setOriginal(inReplyTo: p.inReplyTo, forwarding: p.forwarding, comment: p.comment)
        draft.onSent = { [unowned self] in self.sent.append($0) }
        draft.confirmDiscard = { [unowned self] heading, _, label in
            #expect(heading == "Discard this message?")
            #expect(label == "_Discard")
            self.discardQuestions += 1
            return self.discardAnswer
        }
        draft.saveDraftQuestion = { [unowned self] in
            self.saveQuestions += 1
            return .save
        }
    }

    func stop() async {
        await client.close()
        await fake.stop()
    }
}

@MainActor
@Suite(.serialized) struct JiraComposeControllerTests {
    @Test func fromDraftCarriesTheComment() throws {
        let d = commentDraft(visibility: .internal)
        let p = fromDraft(kind: .reply, draft: d, blocked: BlockedContent())
        #expect(p.comment == DraftComment(issue: request, visibility: .internal))
        #expect(p.accountID == jiraAccount)
        #expect(p.inReplyTo == "w1")
        #expect(p.to.isEmpty && p.cc.isEmpty && p.bcc.isEmpty)
        #expect(p.subject == "ITSD-42: The printer on the third floor")
        #expect(p.bodyHTML.isEmpty, "a comment starts empty")
        let w = try #require(Jira.commentCompose(d))
        #expect(w.title == "Comment on ITSD-42")

        // A mail draft opens no comment mode.
        let mail = fromDraft(kind: .reply, draft: Draft(accountId: "a", subject: "Re: x", inReplyTo: "m1"), blocked: BlockedContent())
        #expect(mail.comment == nil)
        #expect(ComposeParams(kind: .reply).comment == nil)
    }

    @Test func theVisibilityChoiceNeedsBothOptions() {
        // A service-desk request: public and internal, the draft's choice.
        let both = Jira.commentCompose(commentDraft(visibility: .internal))
        #expect(both?.visibilities.map(\.visibility) == [.public, .internal])
        #expect(both?.visibilities.map(\.label) == ["Reply to Customer", "Internal Note"])
        #expect(both?.visibility == .internal)
        // An ordinary issue: no choice, public.
        let plain = Jira.commentCompose(commentDraft(issue, visibility: .internal))
        #expect(plain?.visibilities.isEmpty == true)
        #expect(plain?.visibility == .public)
        // One option alone is no choice either.
        var publicOnly = issue
        publicOnly.commentVisibilities = [.public]
        #expect(Jira.commentCompose(commentDraft(publicOnly))?.visibilities.isEmpty == true)
    }

    @Test func buildCarriesTheVisibilityAndNoRecipients() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        h.form.commentVisibility = .internal
        let d = try #require(h.draft.build())
        #expect(d.accountId == jiraAccount)
        #expect(d.comment == DraftComment(issue: request, visibility: .internal))
        #expect(d.inReplyTo == "w1")
        #expect(d.to.isEmpty && d.cc == nil && d.bcc == nil)
        #expect(d.attachments == nil)
        #expect(d.forwarding == nil)
        #expect(d.htmlBody == "<p>Replaced the toner.</p>")

        h.form.commentVisibility = .public
        #expect(h.draft.build()?.comment?.visibility == .public)

        // An e-mail window sends no comment, whatever the controller holds.
        h.form.isComment = false
        #expect(h.draft.build()?.comment == nil)
    }

    @Test func aCommentIsSentWithoutRecipients() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        h.form.commentVisibility = .internal
        h.draft.send()
        try await waitUntil { h.form.closes == 1 }
        let saves = await h.script.saves
        #expect(saves.count == 1)
        #expect(saves.first?.draft.comment?.visibility == .internal)
        #expect(saves.first?.draft.to.isEmpty == true)
        #expect(await h.script.sends == [MessageSendParams(accountId: jiraAccount, draftId: "d1", version: 1)])
        #expect(h.sent == ["Comment queued"])
        #expect(h.form.toasts.isEmpty)
        #expect(!h.form.statuses.contains("Add at least one recipient"))
        // Sent: closing leaves nothing behind to delete.
        #expect(h.draft.canCloseWithoutAsking)
        h.draft.cleanup()
        try await Task.sleep(for: .milliseconds(30))
        #expect(await h.script.deletes.isEmpty)
    }

    @Test func anEmptyCommentIsRefused() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        // Spaces and invisible characters only.
        h.form.text = " \n\t" + jiraZWSP + jiraBOM + " "
        h.form.html = "<p> </p>"
        h.draft.send()
        #expect(h.form.toasts == ["Write a comment first"])
        #expect(h.form.sendEnabled == [false, true], "Send comes back")
        #expect(!h.draft.draft.sending)
        try await Task.sleep(for: .milliseconds(30))
        #expect(await h.script.saves.isEmpty)
        #expect(await h.script.sends.isEmpty)
        #expect(h.sent.isEmpty && h.form.closes == 0)
    }

    @Test func aRefusedSendIsShownAndTheWindowStays() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        await h.script.set(sendError: RPCError(code: .invalidArgument, message: "comment longer than 32767 characters"))
        h.draft.send()
        try await waitUntil { !h.form.toasts.isEmpty && !h.draft.draft.sending }
        #expect(h.form.toasts == ["Sending was rejected: comment longer than 32767 characters"])
        #expect(h.form.sendEnabled.last == true)
        #expect(h.form.closes == 0 && h.sent.isEmpty)

        // A draft.save refused on the way says Sending, not the draft.
        await h.script.set(sendError: nil)
        await h.script.set(saveError: RPCError(code: .invalidArgument, message: "issue WEB-7 takes no internal comment"))
        h.draft.send()
        try await waitUntil { h.form.toasts.count == 2 && !h.draft.draft.sending }
        #expect(h.form.toasts.last == "Sending was rejected: issue WEB-7 takes no internal comment")
    }

    @Test func thereIsNoSaveDraftForAComment() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        // The autosave keeps a copy against a crash, silently.
        h.draft.markDirty()
        try await waitUntil { await h.script.saves.count == 1 && !h.draft.draft.saving }
        #expect(h.draft.draft.draftID == "d1")
        #expect(!h.form.statuses.contains("Unsaved changes"))
        #expect(!h.form.statuses.contains { $0.hasPrefix("Draft saved") })
        #expect(h.form.statuses.allSatisfy { $0.isEmpty })

        // Closing with text asks whether to discard, never Save Draft;
        // Cancel keeps the window.
        #expect(!h.draft.canCloseWithoutAsking)
        h.discardAnswer = false
        #expect(await h.draft.closeRequest() == false)
        #expect(h.discardQuestions == 1)
        #expect(h.saveQuestions == 0)
        #expect(await h.script.deletes.isEmpty)

        // Discard: the saved copy goes with the window.
        h.discardAnswer = true
        #expect(await h.draft.closeRequest())
        #expect(h.discardQuestions == 2)
        #expect(h.saveQuestions == 0)
        try await waitUntil { await h.script.deletes.count == 1 }
        #expect(await h.script.deletes == [DraftDeleteParams(accountId: jiraAccount, draftId: "d1")])
        #expect(h.draft.draft.closed)
    }

    @Test func anEmptyCommentClosesUnaskedAndLeavesNothing() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        h.draft.markDirty()
        try await waitUntil { await h.script.saves.count == 1 && !h.draft.draft.saving }
        // The text was deleted again: unsaved, but nothing to lose.
        h.form.text = "  "
        h.draft.markDirty()
        #expect(h.draft.canCloseWithoutAsking)
        #expect(await h.draft.closeRequest())
        #expect(h.discardQuestions == 0 && h.saveQuestions == 0)
        try await waitUntil { await h.script.deletes.count == 1 }
        #expect(await h.script.deletes.first?.draftId == "d1")
    }

    @Test func aSaveUnderWayWhenTheWindowGoesIsDeletedToo() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        await h.script.set(saveDelay: .milliseconds(100))
        h.draft.save(reason: .autosave)
        try await waitUntil { await h.script.saves.count == 1 }
        h.draft.cleanup()
        // The first save had no id to delete; its answer brings one.
        try await waitUntil { await h.script.deletes.count == 1 }
        #expect(await h.script.deletes.first == DraftDeleteParams(accountId: jiraAccount, draftId: "d1"))
    }

    @Test func aFailedAutosaveOfACommentSaysNothing() async throws {
        let h = try await Harness(autosaveDelay: .seconds(60))
        defer { Task { await h.stop() } }
        await h.script.set(saveError: RPCError(code: .storageError, message: "disk"))
        h.draft.save(reason: .autosave)
        try await waitUntil { await h.script.saves.count == 1 && !h.draft.draft.saving }
        #expect(h.form.toasts.isEmpty)
        #expect(h.draft.autosaveArmed, "tried again later")
    }

    @Test func aFormThatKnowsNothingOfCommentsIsAMailWindow() {
        // The defaults of ComposeForm, which the older fake forms rely on.
        let form = MailForm()
        #expect(!form.isComment)
        #expect(form.commentVisibility == .public)
    }

    @Test func aMailWindowIsUnchanged() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        h.form.isComment = false
        h.draft.send()
        #expect(h.form.toasts == ["Add at least one recipient"])
        h.draft.markDirty()
        #expect(h.form.statuses.last == "Unsaved changes")
        #expect(!h.draft.canCloseWithoutAsking)
    }
}
