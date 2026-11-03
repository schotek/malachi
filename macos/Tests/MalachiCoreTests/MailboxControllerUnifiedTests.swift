// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The All Inboxes item over the folder, list and actions controllers
// (docs/plans/unified-inbox.md; ui/internal/unified), exercised against
// MailFixture with two mail accounts.

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

private let aIn = FolderKey(account: "a", folder: "a-in")
private let bIn = FolderKey(account: "b", folder: "b-in")
private let aTrash = FolderKey(account: "a", folder: "a-trash")
/// 2026-09-01T10:00:00Z.
private let base = Date(timeIntervalSince1970: 1_788_256_800)

/// A message dated `hours` after `base`, unread unless `.seen` is given.
private func msg(_ id: String, _ hours: Int, thread: String? = nil, _ flags: Flag...) -> MessageSummary {
    MessageSummary(
        id: MessageID(id), accountId: "", folderId: "", threadId: thread.map { ThreadID($0) },
        from: [Address(name: "x", address: "x@example.invalid")], subject: "s-" + id,
        date: base.addingTimeInterval(Double(hours) * 3600), snippet: "p-" + id, flags: flags,
        hasAttachments: false, size: 0
    )
}

private func folders(of acc: String) -> [Folder] {
    [("in", "INBOX", FolderRole.inbox), ("trash", "Trash", .trash), ("arch", "Archive", .archive)].map { id, path, role in
        var f = testFolder("\(acc)-\(id)", path: path, role: role)
        f.accountId = AccountID(acc)
        return f
    }
}

private func ids(_ rows: [ListRow]) -> [String] {
    rows.map { $0.thread ? "T:" + ($0.key.thread?.rawValue ?? "") : $0.message.id.rawValue }
}

@MainActor
private final class Harness {
    let fixture: MailFixture
    let client: RPCClient
    let scratch: ScratchSettings
    let mailbox: MailboxController
    let list: ListController
    let cache: MessageCache
    let actions: ActionsController
    var toasts: [String] = []

    static let info = SystemInfo(version: "fake", protocolVersion: API.protocolVersion, pid: 7, storePath: "/tmp/s.db")

    init(
        a: [MessageSummary] = [msg("a1", 1), msg("a3", 3)], b: [MessageSummary] = [msg("b2", 2), msg("b4", 4)],
        grouped: Bool = false, unified: Bool = true
    ) async throws {
        fixture = try MailFixture()
        await fixture.setAccounts([testAccount("a", name: "Work"), testAccount("b", name: "Home")])
        await fixture.setFolders(["a": folders(of: "a"), "b": folders(of: "b")])
        await fixture.setMessages(a, in: aIn)
        await fixture.setMessages(b, in: bIn)
        try await fixture.start()
        client = RPCClient(socketPath: fixture.path)
        scratch = ScratchSettings()
        scratch.settings.groupByConversation = grouped
        scratch.settings.unifiedInbox = unified
        scratch.settings.confirmDelete = false
        scratch.settings.markReadDelay = 0
        mailbox = MailboxController(client: client, settings: scratch.settings) { _ in }
        list = ListController(mailbox: mailbox, settings: scratch.settings)
        cache = MessageCache(client: client) { _ in }
        actions = ActionsController(mailbox: mailbox, list: list, cache: cache, settings: scratch.settings) { _ in }
        try await client.connect()
        mailbox.handleConnection(.connected(Harness.info))
        try await waitUntil { self.mailbox.model.listFolder != nil && !self.mailbox.model.loading }
    }

    /// Selects the All Inboxes item and waits for its first page.
    func selectUnified() async throws {
        mailbox.selectFolder(.unified, fav: false)
        try await waitUntil { self.mailbox.model.listFolder == .unified && !self.mailbox.model.loading }
    }

    func stop() async {
        list.close()
        mailbox.close()
        await client.close()
        await fixture.stop()
    }
}

@MainActor
@Suite(.serialized) struct MailboxControllerUnifiedTests {
    @Test func theItemListsEveryInboxNewestFirstUnderItsOwnTitle() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        #expect(h.mailbox.model.entries.first?.unified == true)
        #expect(h.mailbox.model.entries.first?.badge == 4)
        #expect(h.mailbox.model.selected == aIn, "the initial folder is a real one")

        try await h.selectUnified()
        #expect(ids(h.list.rows) == ["b4", "a3", "b2", "a1"])
        let req = try #require(await h.fixture.listRequests.last)
        #expect(req.unified == .inbox && req.accountId.rawValue.isEmpty && req.folderId.rawValue.isEmpty)
        #expect(req.sort == .dateDesc && req.page.limit == 50)
        #expect(h.mailbox.selectedFolderTitle == "All Inboxes")
        #expect(h.mailbox.selectedFolderSubtitle == "4 unread of 4")
        // A row names its account.
        let b4 = try #require(h.list.rows.first)
        #expect(h.mailbox.model.rowMessage(b4.message).origin == "Home")
    }

    @Test func pagingGoesOnWithTheCursorOfTheUnifiedListing() async throws {
        let a = (1...60).map { msg("a\($0)", $0 * 2) }
        let b = (1...60).map { msg("b\($0)", $0 * 2 + 1) }
        let h = try await Harness(a: a, b: b)
        defer { Task { await h.stop() } }
        try await h.selectUnified()
        #expect(h.list.rows.count == 50)
        #expect(h.list.loadMoreState.button)
        h.list.loadMore()
        try await waitUntil { h.list.rows.count == 100 }
        let req = try #require(await h.fixture.listRequests.last)
        #expect(req.unified == .inbox && req.page.cursor == "50")
        // The rows of both accounts, interleaved by date.
        #expect(h.list.rows.prefix(4).map(\.message.accountId.rawValue) == ["b", "a", "b", "a"])
    }

    @Test func anArrivalInAnyCoveredInboxJoinsTheListAndTheBadge() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        try await h.selectUnified()
        let fresh = msg("b9", 9)
        h.mailbox.handleNewMessage(NewMessageNotification(accountId: "b", folderId: "b-in", message: fresh))
        #expect(ids(h.list.rows).first == "b9")
        #expect(h.mailbox.model.entries.first?.badge == 5, "the item's badge counts it")
        // Not an inbox: not in the list.
        h.mailbox.handleNewMessage(NewMessageNotification(accountId: "a", folderId: "a-trash", message: msg("t1", 10)))
        #expect(!ids(h.list.rows).contains("t1"))
        // Delivered twice: once.
        h.mailbox.handleNewMessage(NewMessageNotification(accountId: "b", folderId: "b-in", message: fresh))
        #expect(ids(h.list.rows).filter { $0 == "b9" }.count == 1)
    }

    @Test func aChangeOrASyncOfACoveredInboxListsTheItemAgain() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        try await h.selectUnified()
        var count = await h.fixture.listRequests.count

        h.mailbox.handleMessagesChanged(MessagesChangedNotification(accountId: "b", folderIds: ["b-in"]))
        try await waitUntil { await h.fixture.listRequests.count > count }
        count = await h.fixture.listRequests.count

        // A folder of no interest to the list.
        h.mailbox.handleMessagesChanged(MessagesChangedNotification(accountId: "b", folderIds: ["b-trash"]))
        try await Task.sleep(for: .milliseconds(60))
        #expect(await h.fixture.listRequests.count == count)

        // Named by no folder: any of the account's.
        h.mailbox.handleMessagesChanged(MessagesChangedNotification(accountId: "a"))
        try await waitUntil { await h.fixture.listRequests.count > count }
        count = await h.fixture.listRequests.count

        // A pass of an account that ends: its inbox, or the whole account.
        h.mailbox.handleSyncState(SyncState(accountId: "b", status: .syncing))
        h.mailbox.handleSyncState(SyncState(accountId: "b", status: .idle))
        try await waitUntil { await h.fixture.listRequests.count > count }
        count = await h.fixture.listRequests.count
        h.mailbox.handleSyncState(SyncState(accountId: "a", status: .syncing, folderId: "a-trash"))
        h.mailbox.handleSyncState(SyncState(accountId: "a", status: .idle, folderId: "a-trash"))
        try await Task.sleep(for: .milliseconds(60))
        #expect(await h.fixture.listRequests.count == count, "the pass of a folder the list does not cover")
    }

    @Test func checkingMailFromTheItemSyncsEveryAccount() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        try await h.selectUnified()
        h.mailbox.triggerSync()
        try await waitUntil { await !h.fixture.triggers.isEmpty }
        let t = try #require(await h.fixture.triggers.last)
        #expect(t.accountId == nil && t.folderId == nil)
    }

    @Test func aConversationRowAsksItsOwnAccountAndFolder() async throws {
        let a = [msg("a1", 1, thread: "ta"), msg("a3", 3, thread: "ta")]
        let b = [msg("b2", 2, thread: "tb"), msg("b4", 4, thread: "tb")]
        let h = try await Harness(a: a, b: b, grouped: true)
        defer { Task { await h.stop() } }
        try await h.selectUnified()
        let req = try #require(await h.fixture.threadListRequests.last)
        #expect(req.unified == .inbox && req.accountId.rawValue.isEmpty)
        let ta = Unified.scopedThread("ta", account: "a")
        let tb = Unified.scopedThread("tb", account: "b")
        #expect(h.list.rows.compactMap { $0.key.thread } == [tb, ta], "conversations are keyed by account and thread")
        h.list.toggleThread(ta)
        try await waitUntil { await !h.fixture.threadGetRequests.isEmpty }
        let get = try #require(await h.fixture.threadGetRequests.last)
        #expect(get.accountId == "a" && get.folderId == "a-in" && get.threadId == "ta", "the daemon gets its own thread id")
        h.list.toggleThread(tb)
        try await waitUntil { await h.fixture.threadGetRequests.count >= 2 }
        let get2 = try #require(await h.fixture.threadGetRequests.last)
        #expect(get2.accountId == "b" && get2.folderId == "b-in" && get2.threadId == "tb")
        try await waitUntil { h.mailbox.model.members[ta]?.complete == true && h.mailbox.model.members[tb]?.complete == true }
        #expect(h.mailbox.model.memberOf["a1"] == ta && h.mailbox.model.memberOf["b2"] == tb)
    }

    @Test func twoAccountsMayShareAThreadId() async throws {
        let a = [msg("a1", 1, thread: "same"), msg("a3", 3, thread: "same")]
        let b = [msg("b2", 2, thread: "same"), msg("b4", 4, thread: "same")]
        let h = try await Harness(a: a, b: b, grouped: true)
        defer { Task { await h.stop() } }
        try await h.selectUnified()
        let keys = h.list.rows.compactMap { $0.key.thread }
        #expect(keys.count == 2 && Set(keys).count == 2, "one row per account's conversation")
        #expect(h.mailbox.model.threads.map(\.accountId.rawValue).sorted() == ["a", "b"])
        // A notified arrival joins its own account's conversation.
        h.mailbox.handleNewMessage(NewMessageNotification(accountId: "a", folderId: "a-in", message: msg("a9", 9, thread: "same")))
        let ta = Unified.scopedThread("same", account: "a")
        try await waitUntil { h.mailbox.model.tindex[ta].map { h.mailbox.model.threads[$0].messageCount } == 3 }
        let tb = Unified.scopedThread("same", account: "b")
        #expect(h.mailbox.model.tindex[tb].map { h.mailbox.model.threads[$0].messageCount } == 2)
    }

    @Test func aSelectionMayHoldRowsOfSeveralAccounts() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        try await h.selectUnified()
        let keys = [ListKey(message: "a3"), ListKey(message: "b2")]
        #expect(h.list.restrict(keys, latest: keys[1]) == keys)
        h.list.select(keys: keys)
        #expect(h.list.selectedKeys == keys)
        #expect(h.list.actionFlags.mixedAccounts)
        #expect(!h.list.actionFlags.forward && !h.list.actionFlags.move)
        #expect(h.list.actionFlags.trash && h.list.actionFlags.archive)

    }

    @Test func trashAndArchiveAndReadRunOneCallPerAccount() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        try await h.selectUnified()

        h.actions.setSeen(["a3", "b2", "b4"], true)
        try await waitUntil { await h.fixture.flagRequests.count == 2 }
        let flags = await h.fixture.flagRequests
        #expect(flags.map(\.accountId.rawValue) == ["a", "b"])
        #expect(flags[0].messageIds == ["a3"] && flags[1].messageIds == ["b2", "b4"])

        h.actions.archive(["a1", "b4"])
        try await waitUntil { await h.fixture.moveRequests.count == 2 }
        let moves = await h.fixture.moveRequests
        #expect(moves.map(\.accountId.rawValue) == ["a", "b"])
        #expect(moves.map(\.targetFolderId.rawValue) == ["a-arch", "b-arch"], "each account's own Archive")

        h.actions.trash(["a3", "b2"], subject: "")
        try await waitUntil { await h.fixture.deleteRequests.count == 2 }
        let deletes = await h.fixture.deleteRequests
        #expect(deletes.map(\.accountId.rawValue) == ["a", "b"])
        #expect(deletes[0].messageIds == ["a3"] && deletes[1].messageIds == ["b2"])
        try await waitUntil { h.actions.canUndo }
    }

    @Test func undoTakesBackTheMovesOfEveryAccountTogether() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        try await h.selectUnified()
        h.actions.archive(["a1", "b4"])
        try await waitUntil { await h.fixture.moveRequests.count == 2 }
        try await waitUntil { h.actions.undoStack.count == 2 }
        let lists = await h.fixture.listRequests.count
        h.actions.undoLast()
        try await waitUntil { await h.fixture.moveRequests.count == 4 }
        let back = await h.fixture.moveRequests.suffix(2)
        #expect(Set(back.map(\.accountId.rawValue)) == ["a", "b"], "one action, one undo")
        #expect(back.allSatisfy { $0.targetFolderId.rawValue == "\($0.accountId.rawValue)-in" })
        #expect(!h.actions.canUndo)
        try await waitUntil { await h.fixture.listRequests.count > lists }
        #expect(h.mailbox.model.listFolder == .unified)
    }

    @Test func hidingTheItemWhileSelectedFallsBackToARealFolder() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        try await h.selectUnified()
        h.scratch.settings.unifiedInbox = false
        try await waitUntil { h.mailbox.model.selected == aIn }
        #expect(h.mailbox.model.entries.allSatisfy { !$0.unified })
        try await waitUntil { h.mailbox.model.listFolder == aIn && !h.mailbox.model.loading }
        #expect(h.mailbox.selectedFolderTitle == "Inbox")

        h.scratch.settings.unifiedInbox = true
        try await waitUntil { h.mailbox.model.entries.first?.unified == true }
        #expect(h.mailbox.model.selected == aIn, "showing it again leaves the selection")
    }

    @Test func withOneMailAccountThereIsNoItem() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        await h.fixture.setAccounts([testAccount("a", name: "Work")])
        h.mailbox.handleAccountsChanged()
        try await waitUntil { h.mailbox.model.accounts.count == 1 && h.mailbox.model.entries.allSatisfy { !$0.unified } }
    }
}
