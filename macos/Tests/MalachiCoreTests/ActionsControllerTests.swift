// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The per-message actions over the actions controller (ui/internal/window/
// actions.go, the RPC halves of outbox.go, remote.go and compose_open.go),
// exercised against MailFixture: the optimistic change, the call, the
// revert, the confirmation and the toasts.

private struct Timeout: Error, CustomStringConvertible {
    let line: UInt
    var description: String { "timed out waiting at line \(line)" }
}

@MainActor
private func waitUntil(_ timeout: Duration = .seconds(5), line: UInt = #line, _ cond: @MainActor () async throws -> Bool) async throws {
    let deadline = ContinuousClock.now + timeout
    while try await !cond() {
        if ContinuousClock.now > deadline { throw Timeout(line: line) }
        try await Task.sleep(for: .milliseconds(10))
    }
}

/// One confirmation the controller asked for.
private struct Confirmation: Equatable {
    var heading: String
    var body: String
    var label: String
}

/// What the controllers emitted, in order.
@MainActor
private final class ActionLog {
    var toasts: [String] = []
    var confirmations: [Confirmation] = []
    /// What the next confirmation answers.
    var answer = true
    var composed: [ComposeParams] = []
    /// The drafts `raiseDraft` was asked about, and what it answers.
    var raised: [Draft] = []
    var raise = false
    var messageWindows: [MessageID] = []
    var activatedDrafts: [MessageID] = []
    var closedWindows: [MessageID] = []
    /// "id:flagged" per star change.
    var stars: [String] = []
    var outboxStates: [MessageID] = []
    /// "id:loading" per remote-bar redraw.
    var bars: [String] = []
}

/// What the scripted daemon handlers received.
private actor Recorder {
    var senders: [String] = []
    var preferences: [Preferences] = []
    var drafts: [DraftCreateParams] = []
    var opens: [DraftOpenParams] = []
    var lists: [DraftListParams] = []
    var downloads: [MessageDownloadParams] = []
    /// What the next message.download answers with, nil for success.
    var downloadError: RPCError?

    func addDownload(_ p: MessageDownloadParams) { downloads.append(p) }
    func set(downloadError: RPCError?) { self.downloadError = downloadError }

    func addSender(_ a: String) { senders.append(a) }
    func addOpen(_ p: DraftOpenParams) { opens.append(p) }
    func addList(_ p: DraftListParams) { lists.append(p) }
    func addPreferences(_ p: Preferences) { preferences.append(p) }
    func addDraft(_ d: DraftCreateParams) { drafts.append(d) }
}

/// Whether message.download has run, for a body that changes with it.
private actor Downloads {
    var done = false

    func finish() { done = true }
    func reset() { done = false }
}

private let account: AccountID = "a"
private let inbox = FolderKey(account: "a", folder: "in")
private let trash = FolderKey(account: "a", folder: "trash")
private let archive = FolderKey(account: "a", folder: "arch")
private let junk = FolderKey(account: "a", folder: "junk")
private let outbox = FolderKey(account: "a", folder: "out")
private let drafts = FolderKey(account: "a", folder: "dr")
/// 2026-09-01T10:00:00Z.
private let base = Date(timeIntervalSince1970: 1_788_256_800)

/// A message dated `hours` after `base`; unread unless `.seen` is given.
private func msg(_ id: String, _ hours: Int, from: String = "alice", thread: String? = nil, _ flags: Flag...) -> MessageSummary {
    MessageSummary(
        id: MessageID(id), accountId: account, folderId: inbox.folder, threadId: thread.map { ThreadID($0) },
        from: [Address(name: from, address: from + "@example.invalid")], subject: "s-" + id,
        date: base.addingTimeInterval(Double(hours) * 3600), snippet: "p-" + id, flags: flags,
        hasAttachments: false, size: 0
    )
}

private func testFolders() -> [Folder] {
    [
        testFolder("in", path: "INBOX", role: .inbox),
        testFolder("trash", path: "Trash", role: .trash),
        testFolder("arch", path: "Archive", role: .archive),
        testFolder("junk", path: "Junk", role: .junk),
        testFolder("out", path: "Outbox", role: .outbox),
    ]
}

/// The conversations of the grouped tests: t1 with three members, t2 with
/// one, t3 with two; t3 is the newest.
private func threadedMessages() -> [MessageSummary] {
    [
        msg("a1", 1, from: "bob", thread: "t1", .seen),
        msg("a2", 2, from: "alice", thread: "t1", .seen),
        msg("a3", 3, from: "carol", thread: "t1"),
        msg("b1", 5, from: "dave", thread: "t2", .seen),
        msg("c1", 4, from: "erin", thread: "t3", .seen),
        msg("c2", 6, from: "frank", thread: "t3"),
    ]
}

private func ids(_ rows: [ListRow]) -> [String] {
    rows.map { r in
        if r.thread {
            return "T:" + (r.key.thread?.rawValue ?? "")
        }
        return r.message.id.rawValue
    }
}

private func body(_ id: String, html: String?, remote: RemoteContentPolicy) -> MessageBodyResult {
    MessageBodyResult(
        messageId: MessageID(id), bodyState: .fetched, hasHtml: html != nil, html: html, text: "text of \(id)",
        blocked: BlockedContent(remoteImages: remote == .block ? 2 : 0), remoteContent: remote, sanitizerVersion: "1"
    )
}

private func encode<T: Encodable>(_ value: T) throws -> Data {
    try JSONCoding.encoder().encode(value)
}

private func decode<T: Decodable>(_ type: T.Type, _ params: Data) throws -> T {
    try JSONCoding.decoder().decode(type, from: params)
}

/// A fixture, a connected client, the folder, list and cache controllers
/// and the actions controller over a throwaway settings domain, with the
/// initial folder (the inbox) listed.
@MainActor
private final class Harness {
    let fixture: MailFixture
    let client: RPCClient
    let scratch: ScratchSettings
    let mailbox: MailboxController
    let list: ListController
    let cache: MessageCache
    let actions: ActionsController
    let log = ActionLog()

    static let info = SystemInfo(version: "fake", protocolVersion: API.protocolVersion, pid: 7, storePath: "/tmp/s.db")

    init(
        folders: [Folder] = testFolders(), messages: [FolderKey: [MessageSummary]] = [:], grouped: Bool = false,
        confirmDelete: Bool = true
    ) async throws {
        fixture = try MailFixture()
        await fixture.setAccounts([testAccount("a", email: "me@example.invalid", displayName: "Me")])
        await fixture.setFolders(folders, for: account)
        for (k, list) in messages {
            await fixture.setMessages(list, in: k)
        }
        try await fixture.start()
        client = RPCClient(socketPath: fixture.path)
        scratch = ScratchSettings()
        scratch.settings.groupByConversation = grouped
        scratch.settings.confirmDelete = confirmDelete
        scratch.settings.markReadDelay = 0
        let log = log
        mailbox = MailboxController(client: client, settings: scratch.settings) { log.toasts.append($0) }
        list = ListController(mailbox: mailbox, settings: scratch.settings)
        cache = MessageCache(client: client) { log.toasts.append($0) }
        cache.onRemoteBar = { id, lm in log.bars.append("\(id.rawValue):\(lm.loadingImages)") }
        actions = ActionsController(mailbox: mailbox, list: list, cache: cache, settings: scratch.settings) {
            log.toasts.append($0)
        }
        actions.confirm = { _, heading, body, label in
            log.confirmations.append(Confirmation(heading: heading, body: body, label: label))
            return log.answer
        }
        actions.openCompose = { log.composed.append($0) }
        actions.raiseDraft = { d in
            log.raised.append(d)
            return log.raise
        }
        actions.openMessageWindow = { log.messageWindows.append($0.id) }
        actions.onWindowsClose = { log.closedWindows.append($0) }
        actions.onStarChanged = { id, on in log.stars.append("\(id.rawValue):\(on)") }
        actions.onOutboxStateChanged = { log.outboxStates.append($0) }
        try await client.connect()
        mailbox.handleConnection(.connected(Harness.info))
        try await loaded(inbox)
    }

    /// Waits for the list of `k` to be loaded (rows or an empty page).
    func loaded(_ k: FolderKey) async throws {
        try await waitUntil { self.mailbox.model.listFolder == k && !self.mailbox.model.loading }
    }

    func select(_ k: FolderKey) {
        mailbox.selectFolder(k, fav: false)
    }

    /// The cached unread count of a folder (the sidebar badge).
    func unread(_ k: FolderKey) -> Int {
        mailbox.model.folder(k)?.unread ?? -1
    }

    /// The summary the list holds for `id`.
    func summary(_ id: MessageID) throws -> MessageSummary {
        try #require(mailbox.model.message(id)?.summary)
    }

    /// Loads `id` into the cache (message.get and message.body).
    func load(_ id: MessageID) async throws {
        let s = try summary(id)
        cache.fetch(s) { _ in }
        try await waitUntil { self.cache.loaded(id)?.complete == true }
    }

    func stop() async {
        list.close()
        mailbox.close()
        await client.close()
        await fixture.stop()
    }
}

@MainActor
@Suite(.serialized) struct ActionsControllerTests {
    @Test func setSeenIsOptimisticAndRevertsOnFailure() async throws {
        let h = try await Harness(messages: [inbox: [msg("m1", 1), msg("m2", 2), msg("m3", 3, .seen)]])
        defer { Task { await h.stop() } }
        #expect(h.unread(inbox) == 2)

        // At once: the row, the badge; then one message.flag for what
        // actually changes (m3 is read already, "unknown" is not listed).
        h.actions.setSeen(["m1", "m3", "unknown"], true)
        #expect(h.list.row(for: ListKey(message: "m1"))?.message.flags == [.seen])
        #expect(h.unread(inbox) == 1)
        try await waitUntil { await h.fixture.flagRequests.count == 1 }
        #expect(await h.fixture.flagRequests.last == MessageFlagParams(accountId: "a", messageIds: ["m1"], set: [.seen], clear: nil))
        #expect(h.log.toasts.isEmpty)

        // Nothing to do: no call at all.
        h.actions.setSeen(["m1"], true)
        h.actions.markRead("m1")
        try await Task.sleep(for: .milliseconds(30))
        #expect(await h.fixture.flagRequests.count == 1)

        // A refused change is put back, with a toast naming the count.
        await h.fixture.fail(API.MessageFlag.name, with: RPCError(code: .serverError, message: "500"))
        h.actions.setSeen(["m1", "m3"], false)
        #expect(h.unread(inbox) == 3)
        #expect(h.list.row(for: ListKey(message: "m3"))?.message.flags == [])
        try await waitUntil { !h.log.toasts.isEmpty }
        #expect(h.log.toasts == ["Marking 2 messages as unread failed: the server returned an error"])
        #expect(h.unread(inbox) == 1)
        #expect(h.list.row(for: ListKey(message: "m1"))?.message.flags == [.seen])
        #expect(h.list.row(for: ListKey(message: "m3"))?.message.flags == [.seen])
        await h.fixture.succeed(API.MessageFlag.name)

        // The singular forms.
        await h.fixture.fail(API.MessageFlag.name, with: RPCError(code: .networkError, message: "down"))
        h.actions.markUnread("m1")
        try await waitUntil { h.log.toasts.count == 2 }
        #expect(h.log.toasts.last == "Marking the message as unread failed: the server could not be reached")
        h.actions.markRead("m2")
        try await waitUntil { h.log.toasts.count == 3 }
        #expect(h.log.toasts.last == "Marking the message as read failed: the server could not be reached")
        #expect(h.unread(inbox) == 1)
        await h.fixture.succeed(API.MessageFlag.name)

        // The mark-as-read timer's target: only a message still unread.
        h.actions.markRead("m2")
        try await waitUntil { await h.fixture.flagRequests.count == 2 }
        #expect(await h.fixture.flagRequests.last?.messageIds == ["m2"])
        #expect(h.unread(inbox) == 0)
    }

    @Test func starActsOnEveryMemberOfAConversation() async throws {
        let h = try await Harness(messages: [inbox: threadedMessages()], grouped: true)
        defer { Task { await h.stop() } }
        try await waitUntil { h.list.listState == .messages }
        h.list.select(key: ListKey(thread: "t1"))
        let log = h.log
        var members: [MessageID] = []
        h.list.selectedIDs { row, ids in
            members = ids
            #expect(h.list.flagTarget(row), "no member flagged: the star flags them all")
        }
        try await waitUntil { !members.isEmpty }
        #expect(members == ["a1", "a2", "a3"])
        let row = try #require(h.list.selectedRow)

        // Every member at once, one call, every star told.
        h.actions.setFlagged(members, h.list.flagTarget(row))
        #expect(log.stars == ["a1:true", "a2:true", "a3:true"])
        #expect(h.list.selectedRow?.summary.map { hasFlag($0.flags, .flagged) } == true)
        #expect(h.actions.actionFlags(for: h.list.selectedRow).flagged)
        try await waitUntil { await h.fixture.flagRequests.count == 1 }
        #expect(await h.fixture.flagRequests.last == MessageFlagParams(accountId: "a", messageIds: ["a1", "a2", "a3"], set: [.flagged], clear: nil))

        // Any member flagged: the target is off for all of them, and only
        // the flagged ones are sent.
        let flaggedRow = try #require(h.list.selectedRow)
        #expect(!h.list.flagTarget(flaggedRow))
        h.actions.setFlagged(members, false)
        try await waitUntil { await h.fixture.flagRequests.count == 2 }
        #expect(log.stars.count == 6)
        h.actions.setFlagged(["a2"], true)
        try await waitUntil { await h.fixture.flagRequests.count == 3 }
        let partial = try #require(h.list.selectedRow)
        #expect(!h.list.flagTarget(partial))
        h.actions.setFlagged(members, h.list.flagTarget(partial))
        try await waitUntil { await h.fixture.flagRequests.count == 4 }
        #expect(await h.fixture.flagRequests.last == MessageFlagParams(accountId: "a", messageIds: ["a2"], set: nil, clear: [.flagged]))
        #expect(log.stars.last == "a2:false")

        // A single message toggles; a refused change comes back.
        await h.fixture.fail(API.MessageFlag.name, with: RPCError(code: .storageError, message: "disk"))
        h.actions.toggleFlagged("b1")
        #expect(log.stars.last == "b1:true")
        try await waitUntil { !log.toasts.isEmpty }
        #expect(log.toasts == ["Starring the message failed"])
        #expect(log.stars.last == "b1:false")
        #expect(h.mailbox.model.message("b1")?.summary.flags == [.seen])
        #expect(h.log.toasts.count == 1)
    }

    @Test func trashAsksThenMovesAndCancelsASendInTheOutbox() async throws {
        var queued = msg("o1", 5)
        queued.outbox = OutboxInfo(state: .queued, attempts: 0)
        let h = try await Harness(messages: [inbox: [msg("m1", 1), msg("m2", 2, .seen)], outbox: [queued]])
        defer { Task { await h.stop() } }
        let log = h.log

        // Declined: nothing happens.
        log.answer = false
        h.actions.trash(["m1"], subject: "s-m1")
        try await waitUntil { log.confirmations.count == 1 }
        #expect(log.confirmations.last == Confirmation(heading: "Move to Trash?", body: "s-m1", label: "Move to _Trash"))
        try await Task.sleep(for: .milliseconds(30))
        #expect(h.list.rows.count == 2)
        #expect(await h.fixture.deleteRequests.isEmpty)

        // Confirmed: the rows go at once, the windows close, the unread
        // badge moves to Trash, message.delete follows.
        log.answer = true
        h.actions.trash(["m1", "m2"], subject: "two")
        try await waitUntil { log.confirmations.count == 2 }
        #expect(log.confirmations.last?.heading == "Move 2 messages to Trash?")
        try await waitUntil { h.list.rows.isEmpty }
        #expect(log.closedWindows == ["m1", "m2"])
        #expect(h.unread(inbox) == 0)
        #expect(h.unread(trash) == 1)
        try await waitUntil { await h.fixture.deleteRequests.count == 1 }
        #expect(await h.fixture.deleteRequests.last == MessageDeleteParams(accountId: "a", messageIds: ["m1", "m2"]))
        #expect(log.toasts.isEmpty)

        // Without the setting the question is skipped; a message already in
        // Trash is expunged, so no badge is credited.
        h.scratch.settings.confirmDelete = false
        h.select(trash)
        try await h.loaded(trash)
        #expect(ids(h.list.rows) == ["m2", "m1"])
        h.actions.trash("m1")
        #expect(log.confirmations.count == 2)
        #expect(ids(h.list.rows) == ["m2"])
        #expect(h.unread(trash) == 0)
        #expect(h.unread(inbox) == 0)
        try await waitUntil { await h.fixture.deleteRequests.count == 2 }
        #expect(await h.fixture.messages(in: trash).map(\.id) == ["m2"])

        // A single outbox message: cancelling the send, always asked, the
        // sidebar refreshed afterwards without a "sent" toast.
        h.select(outbox)
        try await h.loaded(outbox)
        #expect(h.list.inOutbox)
        let folderLists = await h.fixture.callCount(API.FolderList.name)
        h.actions.trash(["o1"], subject: "ignored")
        try await waitUntil { log.confirmations.count == 3 }
        #expect(log.confirmations.last == Confirmation(
            heading: "Cancel sending this message?",
            body: "“s-o1” will be removed from the outbox and not sent.",
            label: "Do Not _Send"
        ))
        try await waitUntil { h.list.rows.isEmpty }
        #expect(log.closedWindows.last == "o1")
        #expect(h.unread(outbox) == 0)
        try await waitUntil { await h.fixture.deleteRequests.count == 3 }
        #expect(await h.fixture.deleteRequests.last == MessageDeleteParams(accountId: "a", messageIds: ["o1"]))
        try await waitUntil { await h.fixture.callCount(API.FolderList.name) == folderLists + 1 }
        try await waitUntil { h.mailbox.model.folder(outbox)?.total == 0 }
        #expect(log.toasts.isEmpty, "the drop was ours, not a delivery")
        #expect(await h.fixture.messages(in: outbox).isEmpty)
    }

    @Test func aRefusedRemovalRestoresTheRowsAndTheBadges() async throws {
        let h = try await Harness(messages: [inbox: [msg("m1", 1), msg("m2", 2, .seen)]], confirmDelete: false)
        defer { Task { await h.stop() } }
        await h.fixture.fail(API.MessageDelete.name, with: RPCError(code: .storageError, message: "disk"))
        h.actions.trash(["m1", "m2"], subject: "x")
        #expect(h.list.rows.isEmpty)
        #expect(h.unread(inbox) == 0)
        #expect(h.unread(trash) == 1)
        #expect(h.log.closedWindows == ["m1", "m2"])
        try await waitUntil { !h.log.toasts.isEmpty }
        #expect(h.log.toasts == ["Moving 2 messages to Trash failed"])
        #expect(ids(h.list.rows) == ["m2", "m1"])
        #expect(h.unread(inbox) == 1)
        #expect(h.unread(trash) == 0)

        // A refused cancel puts the outbox row back too.
        await h.fixture.fail(API.MessageDelete.name, with: RPCError(code: .invalidArgument, message: "sending"))
        var sending = msg("o1", 5)
        sending.outbox = OutboxInfo(state: .sending, attempts: 1)
        await h.fixture.setMessages([sending], in: outbox)
        h.select(outbox)
        try await h.loaded(outbox)
        h.actions.cancelSend("o1")
        try await waitUntil { h.log.confirmations.count == 1 }
        try await waitUntil { h.log.toasts.count == 2 }
        #expect(h.log.toasts.last == "Cancelling the send was rejected: sending")
        #expect(ids(h.list.rows) == ["o1"])
        #expect(h.unread(outbox) == 1)

        // The bookkeeping alone: read messages move no badge, an unread one
        // leaves its folder for the target and comes back on undo.
        h.select(inbox)
        try await h.loaded(inbox)
        let read = try h.summary("m2")
        let unread = try h.summary("m1")
        _ = h.actions.trackMoves([read], target: trash)
        #expect(h.unread(inbox) == 1 && h.unread(trash) == 0)
        let undo = h.actions.trackMoves([unread, read], target: trash)
        #expect(h.unread(inbox) == 0 && h.unread(trash) == 1)
        undo()
        #expect(h.unread(inbox) == 1 && h.unread(trash) == 0)
        let gone = h.actions.trackMoves([unread], target: nil)
        #expect(h.unread(inbox) == 0 && h.unread(trash) == 0)
        gone()
        #expect(h.unread(inbox) == 1)
    }

    @Test func archiveAndJunkNeedTheRoleFolders() async throws {
        // Without an Archive or Junk folder: a toast, nothing else.
        let bare = try await Harness(
            folders: [testFolder("in", path: "INBOX", role: .inbox), testFolder("trash", path: "Trash", role: .trash)],
            messages: [inbox: [msg("m1", 1)]]
        )
        bare.actions.archive(["m1"])
        #expect(bare.log.toasts == ["This account has no archive folder"])
        bare.actions.junk(["m1"], subject: "s")
        #expect(bare.log.toasts.last == "This account has no junk folder")
        #expect(bare.log.confirmations.isEmpty)
        #expect(bare.list.rows.count == 1)
        #expect(!bare.actions.actionFlags(for: bare.list.rows[0]).archive)
        #expect(!bare.actions.actionFlags(for: bare.list.rows[0]).junk)
        await bare.stop()

        let h = try await Harness(
            messages: [inbox: [msg("m1", 1), msg("m2", 2, .seen)], archive: [msg("x1", 3)]], confirmDelete: false
        )
        defer { Task { await h.stop() } }
        #expect(h.actions.actionFlags(for: h.list.rows[0]).archive)

        // Archive: no question, the row and the badge go, message.move.
        h.actions.archive(["m1", "unknown"])
        #expect(ids(h.list.rows) == ["m2"])
        #expect(h.log.closedWindows == ["m1"])
        #expect(h.unread(inbox) == 0)
        #expect(h.unread(archive) == 2)
        try await waitUntil { await h.fixture.moveRequests.count == 1 }
        #expect(await h.fixture.moveRequests.last == MessageMoveParams(accountId: "a", messageIds: ["m1"], targetFolderId: "arch"))
        #expect(h.log.confirmations.isEmpty)

        // Junk always asks, whatever the Trash setting says; declined is a
        // no-op, confirmed moves.
        h.log.answer = false
        h.actions.junk("m2")
        try await waitUntil { h.log.confirmations.count == 1 }
        #expect(h.log.confirmations.last == Confirmation(heading: "Mark as junk?", body: "s-m2", label: "Mark as _Junk"))
        try await Task.sleep(for: .milliseconds(30))
        #expect(ids(h.list.rows) == ["m2"])
        h.log.answer = true
        h.actions.junk(["m2"], subject: "s-m2")
        try await waitUntil { h.log.confirmations.count == 2 }
        try await waitUntil { await h.fixture.moveRequests.count == 2 }
        #expect(await h.fixture.moveRequests.last == MessageMoveParams(accountId: "a", messageIds: ["m2"], targetFolderId: "junk"))
        #expect(h.list.rows.isEmpty)
        #expect(h.log.closedWindows == ["m1", "m2"])

        // Already in the target folder: nothing happens.
        h.select(archive)
        try await h.loaded(archive)
        #expect(ids(h.list.rows) == ["x1", "m1"])
        #expect(!h.actions.actionFlags(for: h.list.rows[0]).archive)
        h.actions.archive(["x1"])
        try await Task.sleep(for: .milliseconds(30))
        #expect(ids(h.list.rows) == ["x1", "m1"])
        #expect(await h.fixture.moveRequests.count == 2)

        // A refused move comes back with a toast, the badges with it. The
        // daemon is slowed down so the rows can be seen gone meanwhile.
        await h.fixture.fail(API.MessageMove.name, with: RPCError(code: .networkError, message: "down"))
        await h.fixture.delay(API.MessageMove.name, .milliseconds(150))
        h.actions.junk(["x1", "m1"], subject: "both")
        try await waitUntil { h.log.confirmations.count == 3 }
        #expect(h.log.confirmations.last?.heading == "Mark 2 messages as junk?")
        try await waitUntil { h.list.rows.isEmpty }
        #expect(h.unread(archive) == 0)
        #expect(h.unread(junk) == 2)
        #expect(h.log.closedWindows == ["m1", "m2", "x1", "m1"])
        #expect(h.log.toasts.isEmpty)
        try await waitUntil { h.log.toasts.count == 1 }
        #expect(h.log.toasts == ["Marking 2 messages as junk failed: the server could not be reached"])
        #expect(ids(h.list.rows) == ["x1", "m1"])
        #expect(h.unread(archive) == 2)
        #expect(h.unread(junk) == 0)
    }

    @Test func retryOutboxShowsQueuedAtOnceAndFetchesTheTruthBack() async throws {
        var failed = msg("o1", 5)
        failed.folderId = outbox.folder
        failed.outbox = OutboxInfo(state: .failed, attempts: 2, error: RPCError(code: .networkError, message: "no route"))
        let h = try await Harness(messages: [outbox: [failed]])
        defer { Task { await h.stop() } }
        // message.get keeps answering with the failed state; outbox.retry
        // is scripted.
        let detail = try encode(MessageGetResult(message: Message(summary: failed)))
        await h.fixture.on(API.MessageGet.name) { _ in detail }
        await h.fixture.on(API.OutboxRetry.name) { _ in try encode(EmptyResult()) }
        h.select(outbox)
        try await h.loaded(outbox)
        try await h.load("o1")
        #expect(h.cache.loaded("o1")?.msg?.summary.outbox?.state == .failed)
        #expect(h.actions.flags(for: try h.summary("o1")).outbox)

        // Queued at once, in the list and in the cache, the banners told.
        h.actions.retryOutbox("o1")
        #expect(h.mailbox.model.message("o1")?.summary.outbox?.state == .queued)
        #expect(h.mailbox.model.message("o1")?.summary.outbox?.error == nil)
        #expect(h.cache.loaded("o1")?.msg?.summary.outbox?.state == .queued)
        #expect(h.cache.loaded("o1")?.msg?.summary.outbox?.error == nil)
        #expect(h.log.outboxStates == ["o1"])
        try await waitUntil { await h.fixture.callCount(API.OutboxRetry.name) == 1 }
        try await Task.sleep(for: .milliseconds(30))
        #expect(h.log.toasts.isEmpty)
        #expect(h.cache.loaded("o1")?.msg?.summary.outbox?.state == .queued, "an accepted retry fetches nothing")
        #expect(await h.fixture.callCount(API.MessageGet.name) == 1)

        // Refused: the toast, then message.get brings the real state back.
        await h.fixture.on(API.OutboxRetry.name) { _ in throw RPCError(code: .invalidArgument, message: "not failed") }
        h.actions.retryOutbox("o1")
        #expect(h.log.outboxStates == ["o1", "o1"])
        try await waitUntil { h.log.outboxStates.count == 3 }
        #expect(h.log.toasts == ["Retrying the send was rejected: not failed"])
        #expect(h.cache.loaded("o1")?.msg?.summary.outbox?.state == .failed)
        #expect(await h.fixture.callCount(API.MessageGet.name) == 2)

        // Unknown: nothing.
        h.actions.retryOutbox("nope")
        try await Task.sleep(for: .milliseconds(30))
        #expect(await h.fixture.callCount(API.OutboxRetry.name) == 2)
    }

    @Test func loadImagesGoesThroughTheCache() async throws {
        let h = try await Harness(messages: [inbox: [msg("m1", 1, .seen)]])
        defer { Task { await h.stop() } }
        let blocked = try encode(body("m1", html: "<p>blocked</p>", remote: .block))
        let allowed = try encode(body("m1", html: "<p>with pictures</p>", remote: .allow))
        await h.fixture.on(API.MessageBody.name) { params in
            let p = try decode(MessageBodyParams.self, params)
            return p.remoteContent == .allow ? allowed : blocked
        }
        try await h.load("m1")
        #expect(loadableImages(h.cache.loaded("m1")?.body) == 2)

        // Unknown: nothing. Known: the bar shows the wait, the body is
        // replaced.
        h.actions.loadImages("nope")
        h.actions.loadImages("m1")
        #expect(h.cache.loaded("m1")?.loadingImages == true)
        #expect(h.log.bars == ["m1:true"])
        try await waitUntil { h.cache.loaded("m1")?.body?.html == "<p>with pictures</p>" }
        #expect(h.cache.loaded("m1")?.loadingImages == false)
        #expect(h.cache.loaded("m1")?.body?.remoteContent == .allow)
        #expect(h.log.toasts.isEmpty)
        #expect(await h.fixture.callCount(API.MessageBody.name) == 2)

        // A failure: the toast, the bar back, the body on display kept.
        await h.fixture.on(API.MessageBody.name) { params in
            let p = try decode(MessageBodyParams.self, params)
            if p.remoteContent == .allow {
                throw RPCError(code: .networkError, message: "no network")
            }
            return blocked
        }
        h.actions.loadImages("m1")
        try await waitUntil { !h.log.toasts.isEmpty }
        #expect(h.log.toasts == ["Loading the images failed: the server could not be reached"])
        #expect(h.log.bars == ["m1:true", "m1:true", "m1:false"])
        #expect(h.cache.loaded("m1")?.body?.html == "<p>with pictures</p>")
    }

    /// remote.go `downloadPictures` through the controller: unknown ids do
    /// nothing, the download and the body again go through the cache, and a
    /// failure is said through the toast of the window the click came
    /// from, or the controller's own.
    @Test func downloadPicturesGoesThroughTheCache() async throws {
        let h = try await Harness(messages: [inbox: [msg("m1", 1, .seen)]])
        defer { Task { await h.stop() } }
        let rec = Recorder()
        let script = Downloads()
        var shown = body("m1", html: "<p>pictures</p>", remote: .block)
        shown.remotePictures = 2
        let before = try encode(shown)
        shown.remotePictures = nil
        let after = try encode(shown)
        let downloaded = try encode(MessageDownloadResult(message: Message(summary: try h.summary("m1"))))
        await h.fixture.on(API.MessageBody.name) { _ in await script.done ? after : before }
        await h.fixture.on(API.MessageDownload.name) { params in
            await rec.addDownload(try decode(MessageDownloadParams.self, params))
            if let err = await rec.downloadError {
                throw err
            }
            await script.finish()
            return downloaded
        }
        try await h.load("m1")
        #expect(remotePictures(h.cache.loaded("m1")?.body) == 2)

        h.actions.downloadPictures("nope")
        h.actions.downloadPictures("m1")
        #expect(h.cache.loaded("m1")?.loadingPictures == true)
        try await waitUntil { remotePictures(h.cache.loaded("m1")?.body) == 0 }
        #expect(h.cache.loaded("m1")?.loadingPictures == false)
        #expect(await rec.downloads == [MessageDownloadParams(accountId: "a", messageId: "m1")])
        #expect(await h.fixture.callCount(API.MessageBody.name) == 2)
        #expect(h.log.toasts.isEmpty)

        // A failure, said where the click came from.
        await script.reset()
        h.cache.loaded("m1")?.body?.remotePictures = 2
        await rec.set(downloadError: RPCError(code: .offline, message: "no network"))
        var said: [String] = []
        h.actions.downloadPictures("m1") { said.append($0) }
        try await waitUntil { !said.isEmpty }
        #expect(said == ["Downloading the pictures failed: no network connection"])
        #expect(h.log.toasts.isEmpty)
        #expect(h.cache.loaded("m1")?.loadingPictures == false)
        #expect(remotePictures(h.cache.loaded("m1")?.body) == 2, "the body on display stays")

        // Without a toast of its own the controller's is used.
        h.actions.downloadPictures("m1")
        try await waitUntil { !h.log.toasts.isEmpty }
        #expect(h.log.toasts == ["Downloading the pictures failed: no network connection"])
    }

    /// compose_open.go `replyNeedsDownload`: a reply or reply all to a
    /// message whose body counts pictures on the mail server only downloads
    /// it first, once however often asked, and goes on without a word when
    /// that fails; the download asks for the body again (`endDownload`). A
    /// body that counts none, an attachment with a Content-ID on the server
    /// notwithstanding, or an unknown message does not wait for anything. A
    /// forward downloads them too (`forwardNeedsDownload`).
    @Test func replyDownloadsThePicturesOnTheServerFirst() async throws {
        let h = try await Harness(messages: [inbox: [msg("m1", 1, .seen), msg("m2", 2, .seen)]])
        defer { Task { await h.stop() } }
        let picture = MalachiCore.Attachment(
            partId: "2", filename: "photo.jpg", contentType: "image/jpeg", size: 300_000, inline: true,
            contentId: "photo@x", remote: true)
        // Outlook and Apple Mail give ordinary attachments a Content-ID too.
        let report = MalachiCore.Attachment(
            partId: "3", filename: "report.pdf", contentType: "application/pdf", size: 300_000, inline: false,
            contentId: "report@x", remote: true)
        let original = Message(summary: try h.summary("m1"), attachments: [picture, report])
        await h.fixture.setDetail(original)
        var counted = body("m1", html: "<p><img src=\"malachi-cid:a/m1/2\"></p>", remote: .block)
        counted.inlineParts = ["photo@x": "2"]
        counted.remotePictures = 1
        await h.fixture.setBody(counted)
        let rec = Recorder()
        let downloaded = try encode(MessageDownloadResult(message: original))
        await h.fixture.on(API.MessageDownload.name) { params in
            await rec.addDownload(try decode(MessageDownloadParams.self, params))
            try await Task.sleep(for: .milliseconds(30))
            if let err = await rec.downloadError {
                throw err
            }
            return downloaded
        }
        let draft = Draft(accountId: "a", subject: "Re: s-m1", textBody: "q", htmlBody: "<p>q</p>", inReplyTo: "m1")
        let created = try encode(DraftCreateResult(draft: draft, quoted: .html))
        await h.fixture.on(API.DraftCreate.name) { params in
            await rec.addDraft(try decode(DraftCreateParams.self, params))
            return created
        }
        try await h.load("m1")
        let base = await h.fixture.calls().count
        #expect(await h.fixture.callCount(API.MessageBody.name) == 1)

        // The body asked for again after the download runs beside
        // draft.create, in either order.
        func calls(after n: Int) async -> [String] {
            Array(await h.fixture.calls().dropFirst(n)).filter { $0 != API.MessageBody.name }
        }
        h.actions.openCompose(.reply, "m1")
        h.actions.openCompose(.reply, "m1")
        try await waitUntil { h.log.composed.count == 1 }
        #expect(await calls(after: base) == [API.MessageDownload.name, API.DraftCreate.name])
        #expect(await rec.downloads == [MessageDownloadParams(accountId: "a", messageId: "m1")])
        #expect(h.log.composed[0].kind == .reply)
        try await waitUntil { await h.fixture.callCount(API.MessageBody.name) == 2 }

        // A failed download: the reply all the same, nothing asked or said,
        // and no body asked for again.
        await rec.set(downloadError: RPCError(code: .offline, message: "no network"))
        h.actions.openCompose(.replyAll, "m1")
        try await waitUntil { h.log.composed.count == 2 }
        #expect(await rec.downloads.count == 2)
        #expect(h.log.composed[1].kind == .replyAll)
        #expect(h.log.confirmations.isEmpty && h.log.toasts.isEmpty)
        #expect(await rec.drafts.count == 2)
        #expect(await h.fixture.callCount(API.MessageBody.name) == 2)

        // A forward downloads the pictures the HTML shows as well.
        await rec.set(downloadError: nil)
        let before = await h.fixture.calls().count
        h.actions.openCompose(.forward, "m1")
        try await waitUntil { h.log.composed.count == 3 }
        #expect(await calls(after: before) == [API.MessageDownload.name, API.DraftCreate.name])
        #expect(await rec.downloads.count == 3)
        try await waitUntil { await h.fixture.callCount(API.MessageBody.name) == 3 }

        // A body that counts no picture on the server (the daemon holds
        // them): straight to draft.create, the attachments on the server
        // with their Content-IDs notwithstanding.
        h.cache.loaded("m1")?.body?.remotePictures = nil
        h.actions.openCompose(.reply, "m1")
        try await waitUntil { h.log.composed.count == 4 }
        #expect(await rec.downloads.count == 3)

        // Nothing known about the message: straight to draft.create.
        #expect(h.cache.loaded("m2") == nil)
        h.actions.openCompose(.reply, "m2")
        try await waitUntil { h.log.composed.count == 5 }
        #expect(await rec.downloads.count == 3)
        #expect(await rec.drafts.last?.messageId == "m2")
    }

    @Test func trustSenderAddsThenRaisesThePolicyThenLoads() async throws {
        var anonymous = msg("m2", 2, .seen)
        anonymous.from = []
        var blank = msg("m3", 3, .seen)
        blank.from = [Address(name: "x", address: "  ")]
        let h = try await Harness(messages: [inbox: [msg("m1", 1, .seen), anonymous, blank]])
        defer { Task { await h.stop() } }
        let rec = Recorder()
        await h.fixture.on(API.SenderAdd.name) { params in
            let p = try decode(SenderAddParams.self, params)
            await rec.addSender(p.address)
            return try encode(EmptyResult())
        }
        await h.fixture.on(API.ConfigSet.name) { params in
            let p = try decode(ConfigSetParams.self, params)
            await rec.addPreferences(p.preferences)
            return try encode(ConfigSetResult(preferences: p.preferences))
        }
        try await h.load("m1")
        let before = await h.fixture.calls().count

        // No address: nothing at all.
        h.actions.trustSender("m2")
        h.actions.trustSender("m3")
        try await Task.sleep(for: .milliseconds(30))
        #expect(await h.fixture.calls().count == before)

        // sender.add, config.get, config.set (the whole set echoed, the
        // policy raised from block), message.body under allow.
        h.actions.trustSender("m1")
        #expect(h.cache.loaded("m1")?.loadingImages == true)
        #expect(h.log.bars == ["m1:true"])
        h.actions.trustSender("m1") // already on its way
        try await waitUntil { h.cache.loaded("m1")?.loadingImages == false }
        let calls = await h.fixture.calls()
        #expect(Array(calls.dropFirst(before)) == [API.SenderAdd.name, API.ConfigGet.name, API.ConfigSet.name, API.MessageBody.name])
        #expect(await rec.senders == ["alice@example.invalid"])
        #expect(await rec.preferences == [Preferences(syncIntervalSeconds: 300, remoteContent: .knownSenders, offlineDays: 30)])
        #expect(h.cache.loaded("m1")?.body?.remoteContent == .allow)
        #expect(h.log.toasts.isEmpty)

        // Already from known senders: config.set is skipped.
        let known = try encode(ConfigGetResult(preferences: Preferences(syncIntervalSeconds: 60, remoteContent: .knownSenders, offlineDays: 7)))
        await h.fixture.on(API.ConfigGet.name) { _ in known }
        let second = await h.fixture.calls().count
        h.actions.trustSender("m1")
        try await waitUntil { h.cache.loaded("m1")?.loadingImages == false }
        #expect(Array(await h.fixture.calls().dropFirst(second)) == [API.SenderAdd.name, API.ConfigGet.name, API.MessageBody.name])
        #expect(await rec.preferences.count == 1)

        // A refused sender.add: the bar goes back, nothing else is asked.
        await h.fixture.on(API.SenderAdd.name) { _ in throw RPCError(code: .storageError, message: "disk") }
        let third = await h.fixture.calls().count
        h.actions.trustSender("m1")
        try await waitUntil { !h.log.toasts.isEmpty }
        #expect(h.log.toasts == ["Trusting the sender failed"])
        #expect(h.cache.loaded("m1")?.loadingImages == false)
        #expect(Array(await h.fixture.calls().dropFirst(third)) == [API.SenderAdd.name])
        #expect(h.log.bars.last == "m1:false")

        // A failed preference change is said, and the images load anyway.
        await h.fixture.on(API.SenderAdd.name) { _ in try encode(EmptyResult()) }
        let block = try encode(ConfigGetResult(preferences: Preferences(syncIntervalSeconds: 300, remoteContent: .block, offlineDays: 30)))
        await h.fixture.on(API.ConfigGet.name) { _ in block }
        await h.fixture.on(API.ConfigSet.name) { _ in throw RPCError(code: .invalidArgument, message: "bad") }
        let fourth = await h.fixture.calls().count
        h.actions.trustSender("m1")
        try await waitUntil { h.log.toasts.count == 2 }
        #expect(h.log.toasts.last == "Changing the remote content preference was rejected: bad")
        try await waitUntil { h.cache.loaded("m1")?.loadingImages == false }
        #expect(Array(await h.fixture.calls().dropFirst(fourth)) == [API.SenderAdd.name, API.ConfigGet.name, API.ConfigSet.name, API.MessageBody.name])
    }

    @Test func replyComesFromDraftCreateOrThePrefill() async throws {
        var original = msg("m1", 1, .seen)
        original.to = [Address(address: "me@example.invalid"), Address(name: "bob", address: "bob@example.invalid")]
        let h = try await Harness(messages: [inbox: [original]])
        defer { Task { await h.stop() } }
        let rec = Recorder()
        let alice = Address(name: "alice", address: "alice@example.invalid")
        let draft = Draft(
            accountId: "a", to: [alice], subject: "Re: s-m1", textBody: "quoted", htmlBody: "<p>quoted</p>", inReplyTo: "m1"
        )
        let created = try encode(DraftCreateResult(draft: draft, quoted: .html, blocked: BlockedContent(remoteImages: 1)))
        await h.fixture.on(API.DraftCreate.name) { params in
            let p = try decode(DraftCreateParams.self, params)
            await rec.addDraft(p)
            try await Task.sleep(for: .milliseconds(50))
            return created
        }

        // The backend's template; a second click while it is prepared does
        // nothing.
        h.actions.openCompose(.reply, "m1")
        h.actions.openCompose(.reply, "m1")
        h.actions.openCompose(.reply, "nope")
        try await waitUntil { h.log.composed.count == 1 }
        let p = h.log.composed[0]
        #expect(p.kind == .reply)
        #expect(p.accountID == "a")
        #expect(p.to == [alice])
        #expect(p.subject == "Re: s-m1")
        #expect(p.bodyHTML == "<p>quoted</p>")
        #expect(p.inReplyTo == "m1")
        #expect(p.blocked == BlockedContent(remoteImages: 1))
        let requests = await rec.drafts
        #expect(requests.count == 1)
        #expect(requests.first?.accountId == "a")
        #expect(requests.first?.mode == .reply)
        #expect(requests.first?.messageId == "m1")
        #expect(requests.first?.attribution?.hasSuffix("alice wrote:") == true)
        try await Task.sleep(for: .milliseconds(30))
        #expect(await rec.drafts.count == 1)
        #expect(h.log.toasts.isEmpty)

        // notImplemented: no toast, the UI's own quote from what the pane
        // knows (the body once it is loaded).
        await h.fixture.on(API.DraftCreate.name) { _ in throw RPCError(code: .notImplemented, message: "no") }
        try await h.load("m1")
        h.actions.openCompose(.forward, "m1")
        try await waitUntil { h.log.composed.count == 2 }
        let f = h.log.composed[1]
        #expect(f.kind == .forward)
        #expect(f.accountID == "a")
        #expect(f.subject == "Fwd: s-m1")
        #expect(f.forwarding == "m1")
        #expect(f.inReplyTo == nil)
        #expect(f.bodyHTML.contains("---------- Forwarded message ----------"))
        #expect(f.bodyHTML.hasSuffix("body of m1"))
        #expect(h.log.toasts.isEmpty)

        // Another failure is said; the fallback is the same. Reply all
        // leaves the account's own address out of the Cc.
        await h.fixture.on(API.DraftCreate.name) { _ in throw RPCError(code: .serverError, message: "500") }
        h.actions.openCompose(.replyAll, "m1")
        try await waitUntil { h.log.composed.count == 3 }
        #expect(h.log.toasts == ["Preparing the reply failed: the server returned an error"])
        let r = h.log.composed[2]
        #expect(r.kind == .replyAll)
        #expect(r.subject == "Re: s-m1")
        #expect(r.to == [alice])
        #expect(r.cc == [Address(name: "bob", address: "bob@example.invalid")])
        #expect(r.inReplyTo == "m1")
        #expect(r.bodyHTML.contains("<blockquote type=\"cite\">body of m1</blockquote>"))
        #expect(r.bodyHTML.contains("alice wrote:"))
    }

    @Test func flagsFollowTheRowAndTheMessage() async throws {
        var queued = msg("o1", 5)
        queued.outbox = OutboxInfo(state: .queued, attempts: 0)
        let h = try await Harness(messages: [inbox: [msg("m1", 1), msg("m2", 2, .seen, .flagged)], outbox: [queued]])
        defer { Task { await h.stop() } }
        #expect(h.actions.actionFlags(for: nil) == .none)
        let unread = h.actions.actionFlags(for: h.list.rows[1])
        #expect(unread.on && unread.markRead && !unread.markUnread && !unread.flagged && unread.archive && unread.junk && unread.trash)
        let read = h.actions.flags(for: try h.summary("m2"))
        #expect(read.on && !read.markRead && read.markUnread && read.flagged && read.star && read.trustSender)
        h.select(outbox)
        try await h.loaded(outbox)
        let sending = h.actions.flags(for: try h.summary("o1"))
        #expect(sending.on && sending.outbox && sending.trash && sending.reply && sending.loadImages)
        #expect(!sending.star && !sending.archive && !sending.junk && !sending.markRead && !sending.markUnread && !sending.trustSender)
        #expect(h.actions.actionFlags(for: h.list.rows[0]) == sending)

        // An outbox message takes no flags and no moves; trash cancels.
        h.actions.setSeen(["o1"], true)
        h.actions.setFlagged(["o1"], true)
        h.actions.archive(["o1"])
        try await Task.sleep(for: .milliseconds(30))
        #expect(await h.fixture.flagRequests.isEmpty)
        #expect(await h.fixture.moveRequests.isEmpty)
        #expect(h.log.toasts.isEmpty)
        #expect(ids(h.list.rows) == ["o1"])
    }

    @Test func destructiveActionsAreRefusedWithoutAConfirmationHook() async throws {
        let h = try await Harness(messages: [inbox: [msg("m1", 1)]])
        defer { Task { await h.stop() } }
        h.actions.confirm = nil
        h.actions.trash(["m1"], subject: "s-m1")
        h.actions.junk("m1")
        try await Task.sleep(for: .milliseconds(30))
        #expect(h.list.rows.count == 1)
        #expect(await h.fixture.deleteRequests.isEmpty)
        #expect(await h.fixture.moveRequests.isEmpty)
        #expect(h.log.toasts.isEmpty)
    }

    /// drafts.go `openDraft`: draft.open's draft opens in an edit window
    /// (once, however often asked while it runs), a window already editing
    /// it comes to the front instead, an old daemon shows the message, and
    /// a failure is said.
    @Test func draftOpensForEditing() async throws {
        var d1 = msg("d1", 1, .seen)
        d1.folderId = drafts.folder
        let h = try await Harness(folders: testFolders() + [testFolder("dr", path: "Drafts", role: .drafts)], messages: [drafts: [d1]])
        defer { Task { await h.stop() } }
        h.select(drafts)
        try await h.loaded(drafts)
        let saved = Draft(id: "d_1", accountId: "a", version: 3, subject: "s-d1", textBody: "x",
                          attachments: [DraftAttachment(id: "att_1", filename: "a.pdf", contentType: "application/pdf", size: 1, inline: false)])
        let opened = try encode(DraftOpenResult(draft: saved, skipped: [Attachment(partId: "3", filename: "big.iso", contentType: "application/octet-stream", size: 1, inline: false)]))
        let rec = Recorder()
        await h.fixture.on(API.DraftOpen.name) { params in
            await rec.addOpen(try decode(DraftOpenParams.self, params))
            try await Task.sleep(for: .milliseconds(50))
            return opened
        }

        // Activation reaches the hook as a draft, not as a message window.
        let log = h.log
        h.list.onActivateDraft = { log.activatedDrafts.append($0.id) }
        h.list.activate(key: ListKey(message: "d1"))
        #expect(h.log.activatedDrafts == ["d1"])

        h.actions.openDraft("d1")
        h.actions.openDraft("d1")
        try await waitUntil { h.log.composed.count == 1 }
        let p = h.log.composed[0]
        #expect(p.kind == .edit && p.draftID == "d_1" && p.version == 3 && p.subject == "s-d1" && p.attachments.count == 1)
        #expect(h.log.raised.count == 1)
        #expect(h.log.toasts == ["1 attachment of the draft could not be opened"])
        let requests = await rec.opens
        #expect(requests == [DraftOpenParams(accountId: "a", messageId: "d1")])
        try await Task.sleep(for: .milliseconds(80))
        #expect(h.log.composed.count == 1)

        // A window already editing it is raised instead.
        h.log.raise = true
        h.actions.openDraft("d1")
        try await waitUntil { h.log.raised.count == 2 }
        #expect(h.log.composed.count == 1)

        // A daemon without draft.open shows the message.
        await h.fixture.on(API.DraftOpen.name) { _ in throw RPCError(code: .methodNotFound, message: "no") }
        h.actions.openDraft("d1")
        try await waitUntil { h.log.messageWindows == ["d1"] }

        // Not downloaded yet: said, nothing opens.
        await h.fixture.on(API.DraftOpen.name) { _ in throw RPCError(code: .unavailable, message: "later") }
        h.actions.openDraft("d1")
        try await waitUntil { h.log.toasts.count == 2 }
        #expect(h.log.toasts.last == "The draft has not been downloaded yet; try again in a moment")
        #expect(h.log.composed.count == 1)
    }

    /// The assistant panel's Open Draft (ui/internal/assistant, the In App
    /// target): the draft is looked up with draft.list, page after page,
    /// and opens for editing, or its window comes to the front; one that
    /// is not listed says so; a failed list is the usual sentence.
    @Test func savedDraftOpensAfterDraftList() async throws {
        let h = try await Harness(messages: [inbox: [msg("m1", 1, .seen)]])
        defer { Task { await h.stop() } }
        let first = Draft(id: "d_1", accountId: "a", version: 1, subject: "older", textBody: "x")
        let wanted = Draft(id: "d_9", accountId: "a", version: 2, subject: "Re: s-m1", textBody: "Yes",
                           htmlBody: "<p>Yes</p>", inReplyTo: "m1")
        let rec = Recorder()
        await h.fixture.on(API.DraftList.name) { params in
            let p = try decode(DraftListParams.self, params)
            await rec.addList(p)
            if p.page.cursor == nil {
                return try encode(DraftListResult(drafts: [first], page: PageInfo(nextCursor: "c2", total: 2)))
            }
            return try encode(DraftListResult(drafts: [wanted], page: PageInfo(nextCursor: nil, total: 2)))
        }
        h.actions.openSavedDraft(account: "a", id: "d_9")
        h.actions.openSavedDraft(account: "a", id: "d_9") // once while it runs
        try await waitUntil { h.log.composed.count == 1 }
        let p = h.log.composed[0]
        #expect(p.kind == .edit && p.draftID == "d_9" && p.version == 2 && p.subject == "Re: s-m1")
        #expect(h.log.raised.map(\.id) == ["d_9"])
        let lists = await rec.lists
        #expect(lists == [
            DraftListParams(accountId: "a", page: Page(cursor: nil, limit: 500)),
            DraftListParams(accountId: "a", page: Page(cursor: "c2", limit: 500)),
        ])
        try await Task.sleep(for: .milliseconds(80))
        #expect(h.log.composed.count == 1)

        // A window already editing it comes to the front.
        h.log.raise = true
        h.actions.openSavedDraft(account: "a", id: "d_9")
        try await waitUntil { h.log.raised.count == 2 }
        #expect(h.log.composed.count == 1)

        // Not listed (sent, deleted, never there).
        h.actions.openSavedDraft(account: "a", id: "d_404")
        try await waitUntil { h.log.toasts.count == 1 }
        #expect(h.log.toasts == ["The draft is no longer there"])

        // The list fails.
        await h.fixture.on(API.DraftList.name) { _ in throw RPCError(code: .storageError, message: "disk") }
        h.actions.openSavedDraft(account: "a", id: "d_9")
        try await waitUntil { h.log.toasts.count == 2 }
        #expect(h.log.toasts[1].hasPrefix("Opening the draft"))
        #expect(h.log.composed.count == 1)
    }

    /// compose_open.go: a forward of a message with attachments on the
    /// mail server (or one the cache does not hold) downloads it before
    /// draft.create; a failed download asks "Forward Without
    /// Attachments?" over the window it came from, except without a
    /// daemon, on a daemon without message.download and for a message
    /// over its cap; the parts draft.create could not import reach the
    /// window as `skipped`.
    @Test func forwardDownloadsTheAttachmentsFirst() async throws {
        let h = try await Harness(messages: [inbox: [msg("m1", 1, .seen), msg("m2", 2, .seen)]])
        defer { Task { await h.stop() } }
        let onServer = MalachiCore.Attachment(partId: "2", filename: "big.pdf", contentType: "application/pdf", size: 300_000, inline: false, remote: true)
        let remote = Message(summary: try h.summary("m1"), attachments: [onServer])
        var local = remote
        local.attachments[0].remote = nil
        await h.fixture.setDetail(remote)
        let rec = Recorder()
        let downloaded = try encode(MessageDownloadResult(message: local))
        await h.fixture.on(API.MessageDownload.name) { params in
            await rec.addDownload(try decode(MessageDownloadParams.self, params))
            try await Task.sleep(for: .milliseconds(30))
            if let err = await rec.downloadError {
                throw err
            }
            return downloaded
        }
        let draft = Draft(accountId: "a", subject: "Fwd: s-m1", textBody: "fwd", htmlBody: "<p>fwd</p>", forwarding: "m1")
        let created = try encode(DraftCreateResult(draft: draft, quoted: .html, skipped: [onServer]))
        await h.fixture.on(API.DraftCreate.name) { params in
            await rec.addDraft(try decode(DraftCreateParams.self, params))
            return created
        }
        let parents = ParentLog()
        let log = h.log
        h.actions.confirm = { parent, heading, body, label in
            parents.seen.append(parent === parents.window)
            log.confirmations.append(Confirmation(heading: heading, body: body, label: label))
            return log.answer
        }
        try await h.load("m1")
        let base = await h.fixture.calls().count

        // Downloaded first, once however often asked; then the template.
        h.actions.openCompose(.forward, "m1", from: parents.window)
        h.actions.openCompose(.forward, "m1", from: parents.window)
        try await waitUntil { h.log.composed.count == 1 }
        #expect(Array(await h.fixture.calls().dropFirst(base)) == [API.MessageDownload.name, API.DraftCreate.name])
        #expect(await rec.downloads == [MessageDownloadParams(accountId: "a", messageId: "m1")])
        let f = h.log.composed[0]
        #expect(f.kind == .forward && f.forwarding == "m1" && f.subject == "Fwd: s-m1")
        #expect(f.skipped == 1)
        #expect(h.log.confirmations.isEmpty && h.log.toasts.isEmpty)
        #expect(h.cache.loaded("m1")?.msg == local, "the downloaded message replaced the cached one")

        // Nothing on the server any more: straight to draft.create.
        h.actions.openCompose(.forward, "m1")
        try await waitUntil { h.log.composed.count == 2 }
        #expect(await rec.downloads.count == 1)

        // A failed download asks over the window it came from; Cancel
        // opens nothing.
        h.cache.loaded("m1")?.msg = remote
        await rec.set(downloadError: RPCError(code: .offline, message: "no network"))
        h.log.answer = false
        h.actions.openCompose(.forward, "m1", from: parents.window)
        try await waitUntil { h.log.confirmations.count == 1 }
        #expect(h.log.confirmations[0] == Confirmation(
            heading: "Forward Without Attachments?", body: "Downloading the attachments failed: no network connection",
            label: "_Forward Without Attachments"))
        #expect(parents.seen == [true])
        try await Task.sleep(for: .milliseconds(50))
        #expect(h.log.composed.count == 2)
        #expect(await rec.drafts.count == 2)
        #expect(h.log.toasts.isEmpty)

        // Confirmed: the template without them; the next click works again.
        h.log.answer = true
        h.actions.openCompose(.forward, "m1")
        try await waitUntil { h.log.composed.count == 3 }
        #expect(h.log.confirmations.count == 2)
        #expect(h.log.composed[2].skipped == 1)
        #expect(await rec.drafts.count == 3)

        // A daemon without message.download: not asked about.
        await rec.set(downloadError: RPCError(code: .methodNotFound, message: "unknown method"))
        h.actions.openCompose(.forward, "m1")
        try await waitUntil { h.log.composed.count == 4 }
        #expect(h.log.confirmations.count == 2)

        // A reply never downloads.
        let downloads = await rec.downloads.count
        h.actions.openCompose(.reply, "m1")
        try await waitUntil { h.log.composed.count == 5 }
        #expect(await rec.downloads.count == downloads)
        #expect(h.log.composed[4].skipped == 1, "whatever draft.create reports")

        // Without a confirmation hook a failed download forwards nothing.
        h.actions.confirm = nil
        await rec.set(downloadError: RPCError(code: .offline, message: "no network"))
        h.actions.openCompose(.forward, "m1")
        try await waitUntil { await rec.downloads.count == downloads + 1 }
        try await Task.sleep(for: .milliseconds(80))
        #expect(h.log.composed.count == 5)

        // A message over the daemon's cap can never be downloaded: asking
        // would change nothing, the template comes at once.
        h.actions.confirm = { _, heading, body, label in
            log.confirmations.append(Confirmation(heading: heading, body: body, label: label))
            return log.answer
        }
        await rec.set(downloadError: RPCError(code: .attachmentTooBig, message: "over the cap"))
        h.actions.openCompose(.forward, "m1")
        try await waitUntil { h.log.composed.count == 6 }
        #expect(h.log.confirmations.count == 2)

        // A message the cache does not hold is downloaded first all the
        // same: message.download answers at once when nothing is missing.
        await rec.set(downloadError: nil)
        #expect(h.cache.loaded("m2") == nil)
        h.actions.openCompose(.forward, "m2")
        try await waitUntil { h.log.composed.count == 7 }
        #expect(await rec.downloads.last == MessageDownloadParams(accountId: "a", messageId: "m2"))
        #expect(await rec.drafts.last?.messageId == "m2")

        // Without a daemon nothing is asked either: the window opens from
        // what the pane knows, without a toast.
        h.cache.loaded("m1")?.msg = remote
        await h.client.close()
        h.actions.openCompose(.forward, "m1")
        try await waitUntil { h.log.composed.count == 8 }
        #expect(h.log.composed[7].kind == .forward && h.log.composed[7].forwarding == "m1")
        #expect(h.log.confirmations.count == 2)
        #expect(h.log.toasts.isEmpty)
    }
}

/// The parent handed to the confirmation hook, by identity.
@MainActor
private final class ParentLog {
    let window = NSObject()
    var seen: [Bool] = []
}
