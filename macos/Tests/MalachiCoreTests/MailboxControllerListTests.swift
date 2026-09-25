// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The message-list half of the main window over the list controller
// (ui/internal/window/messages.go, threads.go, the list part of
// folders.go `onNewMessage` and the selection handling of window.go and
// actions.go), exercised against MailFixture.

private struct Timeout: Error {}

@MainActor
private func waitUntil(_ timeout: Duration = .seconds(5), _ cond: @MainActor () async throws -> Bool) async throws {
    let deadline = ContinuousClock.now + timeout
    while try await !cond() {
        if ContinuousClock.now > deadline { throw Timeout() }
        try await Task.sleep(for: .milliseconds(10))
    }
}

/// What the controllers emitted, in order.
@MainActor
private final class ListLog {
    var toasts: [String] = []
    var rowEvents: [SelectionHint] = []
    var states: [ListState] = []
    var loadMore: [LoadMoreState] = []
    var selections: [MessageID?] = []
    var activations: [MessageID] = []
    var flags: [ActionFlags] = []
    var marks: [MessageID] = []
    var refreshed: [[ListKey]] = []
    var cleared = 0
    /// What `selectedIDs` handed over.
    var ids: [[MessageID]] = []
}

private let account: AccountID = "a"
private let inbox = FolderKey(account: "a", folder: "in")
private let trash = FolderKey(account: "a", folder: "trash")
private let empty = FolderKey(account: "a", folder: "empty")
private let outbox = FolderKey(account: "a", folder: "out")
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
        testFolder("empty", path: "Empty"),
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

/// A fixture, a connected client, the folder controller and the list
/// controller over a throwaway settings domain.
@MainActor
private final class Harness {
    let fixture: MailFixture
    let client: RPCClient
    let scratch: ScratchSettings
    let sync: SyncController
    let mailbox: MailboxController
    let list: ListController
    let log = ListLog()

    static let info = SystemInfo(version: "fake", protocolVersion: API.protocolVersion, pid: 7, storePath: "/tmp/s.db")

    init(folders: [Folder] = testFolders(), messages: [FolderKey: [MessageSummary]] = [:], grouped: Bool = false, connect: Bool = true) async throws {
        fixture = try MailFixture()
        await fixture.setAccounts([testAccount("a", email: "a@example.invalid")])
        await fixture.setFolders(folders, for: account)
        for (k, list) in messages {
            await fixture.setMessages(list, in: k)
        }
        try await fixture.start()
        client = RPCClient(socketPath: fixture.path)
        scratch = ScratchSettings()
        scratch.settings.groupByConversation = grouped
        sync = SyncController()
        let log = log
        mailbox = MailboxController(client: client, settings: scratch.settings, sync: sync) { log.toasts.append($0) }
        list = ListController(mailbox: mailbox, settings: scratch.settings)
        list.markReadTick = .milliseconds(20)
        list.onRows = { _, hint in log.rowEvents.append(hint) }
        list.onListState = { log.states.append($0) }
        list.onLoadMore = { log.loadMore.append($0) }
        list.onSelectedMessageChanged = { log.selections.append($0?.id) }
        list.onActivateMessage = { log.activations.append($0.id) }
        list.onActionFlagsChanged = { log.flags.append($0) }
        list.onMarkRead = { log.marks.append($0) }
        list.onRowsRefreshed = { log.refreshed.append($0) }
        list.onSelectionCleared = { log.cleared += 1 }
        if connect {
            try await self.connect()
        }
    }

    func connect() async throws {
        try await client.connect()
        mailbox.handleConnection(.connected(Harness.info))
    }

    /// Waits for the list to settle on its rows.
    func settled() async throws {
        try await waitUntil { self.list.listState == .messages && !self.mailbox.model.loading }
    }

    /// Waits for the list of `k` to be loaded (rows or an empty page).
    func loaded(_ k: FolderKey) async throws {
        try await waitUntil { self.mailbox.model.listFolder == k && !self.mailbox.model.loading }
    }

    func select(_ k: FolderKey) {
        mailbox.selectFolder(k, fav: false)
    }

    func stop() async {
        list.close()
        mailbox.close()
        await client.close()
        await fixture.stop()
    }
}

@MainActor
@Suite(.serialized) struct MailboxControllerListTests {
    @Test func flatListingPagesFiftyAtATime() async throws {
        let all = (1...120).map { msg("m\($0)", $0) }
        let h = try await Harness(messages: [inbox: all], connect: false)
        defer { Task { await h.stop() } }
        // Nothing selected at first, then the page loads.
        #expect(h.list.listState == .status(
            icon: "folder-symbolic", title: "Select a folder",
            description: "Choose a folder in the sidebar to see its messages.", retry: false
        ))
        try await h.connect()
        try await h.settled()
        #expect(h.log.states.contains(.status(icon: "", title: "Loading…", description: "", retry: false)))
        #expect(h.list.rows.count == 50)
        #expect(h.list.rows.first?.message.id == "m120")
        #expect(h.list.rows.last?.message.id == "m71")
        #expect(h.mailbox.model.total == 120)
        #expect(h.mailbox.model.nextCursor == "50")
        #expect(h.list.loadMoreState == LoadMoreState(spinner: false, button: true))
        let first = try #require(await h.fixture.listRequests.first)
        #expect(first == MessageListParams(accountId: "a", folderId: "in", page: Page(limit: 50), sort: .dateDesc, filter: .all))
        #expect(await h.fixture.listRequests.count == 1)
        #expect(h.list.selectedKey == nil)
        #expect(h.log.selections.isEmpty)

        // The second page appends; the spinner shows while it is fetched.
        h.list.loadMore()
        #expect(h.list.loadMoreState == LoadMoreState(spinner: true, button: false))
        try await waitUntil { h.list.rows.count == 100 }
        #expect(await h.fixture.listRequests.last?.page.cursor == "50")
        #expect(h.list.loadMoreState == LoadMoreState(spinner: false, button: true))
        #expect(h.list.rows[50].message.id == "m70")

        // A message notified meanwhile sits at the top of the list and in
        // the third page too: the page skips it.
        let late = msg("m15", 15)
        h.mailbox.handleNewMessage(NewMessageNotification(accountId: "a", folderId: "in", message: late))
        #expect(h.list.rows.count == 101)
        #expect(h.list.rows.first?.message.id == "m15")
        #expect(h.mailbox.model.total == 121)
        h.list.loadMore()
        try await waitUntil { h.list.loadMoreState.button == false && h.list.rows.count > 101 }
        #expect(h.list.rows.count == 120)
        #expect(Set(h.list.rows.map(\.message.id)).count == 120)
        #expect(h.mailbox.model.total == 120)
        #expect(h.mailbox.model.nextCursor == nil)
        #expect(h.list.loadMoreState == LoadMoreState())

        // The last page is shown: nothing more to ask for.
        h.list.loadMore()
        try await Task.sleep(for: .milliseconds(30))
        #expect(await h.fixture.listRequests.count == 3)
        #expect(h.log.toasts.isEmpty)

        // A failed page keeps the cursor and the button offers a retry.
        h.mailbox.model.nextCursor = "100"
        h.list.showLoadMore()
        await h.fixture.fail(API.MessageList.name, with: RPCError(code: .serverError, message: "500"))
        h.list.loadMore()
        try await waitUntil { !h.log.toasts.isEmpty }
        #expect(h.log.toasts == ["Loading more messages failed: the server returned an error"])
        #expect(h.list.loadMoreState == LoadMoreState(spinner: false, button: true))
        #expect(h.list.rows.count == 120)
    }

    @Test func filterChangeRepagesFromTheStartAndPersistsAcrossFolders() async throws {
        var all = (1...10).map { msg("m\($0)", $0, .seen) }
        all[1].flags = []
        all[4].flags = []
        all[7].flags = []
        let h = try await Harness(messages: [inbox: all, trash: [msg("t1", 1, .seen)]])
        defer { Task { await h.stop() } }
        try await h.settled()
        #expect(h.list.rows.count == 10)
        h.list.select(key: ListKey(message: "m9"))
        #expect(h.log.selections == ["m9"])

        // The rows and the selection go at once; the folder is paged again
        // with the new filter.
        h.list.setListFilter(.unread)
        #expect(h.list.rows.isEmpty)
        #expect(h.log.rowEvents.last == .clear)
        #expect(h.list.selectedKey == nil)
        #expect(h.log.selections.last == .some(nil))
        #expect(h.list.listState == .status(icon: "", title: "Loading…", description: "", retry: false))
        try await h.settled()
        #expect(ids(h.list.rows) == ["m8", "m5", "m2"])
        let req = try #require(await h.fixture.listRequests.last)
        #expect(req.filter == .unread)
        #expect(req.page.cursor == nil)
        #expect(await h.fixture.listRequests.count == 2)

        // An unchanged filter (the toggle group's own write-back) is a no-op;
        // an empty one is "all".
        h.list.setListFilter(.unread)
        #expect(await h.fixture.listRequests.count == 2)
        h.list.setListFilter(MessageFilter(rawValue: ""))
        try await h.settled()
        #expect(h.list.rows.count == 10)
        #expect(await h.fixture.listRequests.last?.filter == .all)

        // No flagged messages: the page says so.
        h.list.setListFilter(.flagged)
        try await waitUntil { !h.mailbox.model.loading }
        #expect(h.list.listState == .status(
            icon: "starred-symbolic", title: "No Flagged Messages",
            description: "No message in this folder carries a flag.", retry: false
        ))

        // The filter is global: another folder is listed under it.
        h.select(trash)
        try await h.loaded(trash)
        #expect(await h.fixture.listRequests.last?.folderId == "trash")
        #expect(await h.fixture.listRequests.last?.filter == .flagged)
        #expect(h.list.listState.isStatus)
    }

    @Test func statusPagesFollowThePrecedenceTable() async throws {
        // Only a container: nothing to select.
        let bare = try await Harness(folders: [testFolder("c", path: "c", selectable: false)])
        defer { Task { await bare.stop() } }
        try await waitUntil { bare.mailbox.model.entries.count == 1 }
        #expect(bare.mailbox.model.selected == nil)
        #expect(bare.list.listState == .status(
            icon: "folder-symbolic", title: "Select a folder",
            description: "Choose a folder in the sidebar to see its messages.", retry: false
        ))
        #expect(await bare.fixture.listRequests.isEmpty)

        // A folder the daemon never downloads is not asked for.
        var allMail = testFolder("all", path: "All Mail", role: .all)
        allMail.synced = false
        let h = try await Harness(
            folders: testFolders() + [allMail], messages: [inbox: [msg("m1", 1)]], connect: false
        )
        defer { Task { await h.stop() } }
        await h.fixture.fail(API.MessageList.name, with: RPCError(code: .networkError, message: "down"))
        try await h.connect()
        // The first load fails: an error page with Try Again.
        try await waitUntil { h.mailbox.model.listErr != nil }
        #expect(h.list.listState == .status(
            icon: "dialog-warning-symbolic", title: "Messages Unavailable",
            description: "Loading messages failed: the server could not be reached", retry: true
        ))
        #expect(h.list.loadMoreState == LoadMoreState())
        #expect(h.log.toasts.isEmpty, "the first failure is a page, not a toast")

        // Try Again.
        await h.fixture.succeed(API.MessageList.name)
        h.list.retry()
        try await h.settled()
        #expect(h.list.rows.count == 1)

        // A failed reload of the same folder keeps the rows and toasts once.
        await h.fixture.fail(API.MessageList.name, with: RPCError(code: .networkError, message: "down"))
        h.mailbox.reloadMessages?()
        #expect(h.list.rows.count == 1, "a same-folder reload keeps the rows until the reply")
        #expect(h.list.listState == .messages)
        try await waitUntil { !h.log.toasts.isEmpty }
        #expect(h.log.toasts == ["Loading messages failed: the server could not be reached"])
        #expect(h.list.rows.count == 1)
        #expect(h.list.listState == .messages)
        await h.fixture.succeed(API.MessageList.name)

        // Not synchronised.
        h.select(FolderKey(account: "a", folder: "all"))
        #expect(h.list.rows.isEmpty)
        #expect(h.list.listState == .status(
            icon: "folder-download-symbolic", title: "Not Synchronised",
            description: "Messages moved here are archived on the server; the folder itself is not downloaded.",
            retry: false
        ))
        let requests = await h.fixture.listRequests.count

        // Loading, then empty for the filter.
        await h.fixture.delay(API.MessageList.name, .milliseconds(150))
        h.select(empty)
        #expect(h.list.listState == .status(icon: "", title: "Loading…", description: "", retry: false))
        try await h.loaded(empty)
        #expect(await h.fixture.listRequests.count == requests + 1)
        #expect(h.list.listState == .status(
            icon: "mail-unread-symbolic", title: "No Messages", description: "This folder is empty.", retry: false
        ))
        await h.fixture.delay(API.MessageList.name, .zero)
        h.list.setListFilter(.unread)
        try await waitUntil { !h.mailbox.model.loading }
        #expect(h.list.listState == .status(
            icon: "mail-read-symbolic", title: "No Unread Messages",
            description: "Everything in this folder has been read.", retry: false
        ))
        h.list.setListFilter(.flagged)
        try await waitUntil { !h.mailbox.model.loading }
        #expect(h.list.listState == .status(
            icon: "starred-symbolic", title: "No Flagged Messages",
            description: "No message in this folder carries a flag.", retry: false
        ))
    }

    @Test func groupedListingFoldsAndFetchesMembers() async throws {
        let h = try await Harness(messages: [inbox: threadedMessages()], grouped: true)
        defer { Task { await h.stop() } }
        try await h.settled()
        #expect(h.mailbox.model.grouped)
        #expect(await h.fixture.listRequests.isEmpty)
        let req = try #require(await h.fixture.threadListRequests.first)
        #expect(req == ThreadListParams(accountId: "a", folderId: "in", page: Page(limit: 50), sort: .dateDesc, filter: .all))
        #expect(ids(h.list.rows) == ["T:t3", "b1", "T:t1"])
        let t3 = h.list.rows[0]
        #expect(t3.message.id == "c2")
        #expect(t3.summary?.messageCount == 2)
        #expect(t3.summary?.unreadCount == 1)
        #expect(!t3.expanded && !t3.loading)
        #expect(h.list.rows[1].key == ListKey(thread: "t2", message: "b1"))
        #expect(!h.list.rows[1].thread && !h.list.rows[1].member)

        // Unfolding asks for the members; the row spins meanwhile.
        h.list.toggleThread("t1")
        #expect(h.list.rows[2].expanded && h.list.rows[2].loading)
        try await waitUntil { h.list.rows.count == 6 }
        #expect(await h.fixture.threadGetRequests == [ThreadGetParams(accountId: "a", threadId: "t1", folderId: "in")])
        #expect(ids(h.list.rows) == ["T:t3", "b1", "T:t1", "a1", "a2", "a3"])
        #expect(!h.list.rows[2].loading)
        #expect(h.list.rows[3].member && h.list.rows[4].member && h.list.rows[5].member)
        #expect(h.list.rows[3].key == ListKey(thread: "t1", message: "a1"))

        // Folding and unfolding again uses what is known.
        h.list.toggleThread("t1")
        #expect(ids(h.list.rows) == ["T:t3", "b1", "T:t1"])
        #expect(!h.list.setThreadExpanded("t1", false), "already folded: the key passes")
        #expect(h.list.setThreadExpanded("t1", true))
        #expect(h.list.rows.count == 6)
        #expect(await h.fixture.threadGetRequests.count == 1)

        // Activation folds a conversation row and opens a message row.
        h.list.activate(key: ListKey(thread: "t1"))
        #expect(h.list.rows.count == 3)
        h.list.activate(key: ListKey(thread: "t2", message: "b1"))
        #expect(h.log.activations == ["b1"])

        // A failed fetch folds the row back and says why.
        await h.fixture.fail(API.ThreadGet.name, with: RPCError(code: .storageError, message: "disk"))
        h.list.toggleThread("t3")
        #expect(h.list.rows[0].loading)
        try await waitUntil { !h.log.toasts.isEmpty }
        #expect(h.log.toasts == ["Loading the conversation failed"])
        #expect(!h.list.rows[0].expanded && !h.list.rows[0].loading)
        #expect(h.list.rows.count == 3)
    }

    @Test func reloadKeepsFetchedMembersOfTheSameShape() async throws {
        let h = try await Harness(messages: [inbox: threadedMessages()], grouped: true)
        defer { Task { await h.stop() } }
        try await h.settled()
        h.list.toggleThread("t1")
        try await waitUntil { h.list.rows.count == 6 }
        h.list.select(key: ListKey(thread: "t1", message: "a2"))
        #expect(h.log.selections.last == "a2")

        // A reload of the same folder (a sync finished) keeps the members
        // and the selection; no second thread.get.
        h.mailbox.reloadMessages?()
        #expect(h.list.rows.count == 6, "the rows stay until the reply")
        try await waitUntil { await h.fixture.threadListRequests.count == 2 && !h.mailbox.model.loading }
        #expect(h.list.rows.count == 6)
        #expect(h.list.rows[2].expanded && !h.list.rows[2].loading)
        #expect(await h.fixture.threadGetRequests.count == 1)
        #expect(h.list.selectedKey == ListKey(thread: "t1", message: "a2"))
        #expect(h.log.selections.count == 1, "a kept selection is not announced again")

        // The conversation changed shape: the members are fetched again.
        await h.fixture.addMessage(msg("a4", 7, from: "gina", thread: "t1"))
        h.mailbox.reloadMessages?()
        try await waitUntil { await h.fixture.threadGetRequests.count == 2 && h.list.rows.count == 7 }
        #expect(ids(h.list.rows) == ["T:t1", "a1", "a2", "a3", "a4", "T:t3", "b1"])
        // While the members were on their way the member row was gone: its
        // conversation row took the selection over (and the pane showed
        // the newest member); it keeps it once the members are back.
        #expect(h.list.selectedKey == ListKey(thread: "t1"))
        #expect(h.log.selections == ["a2", "a4"])
    }

    @Test func newMessagesJoinTheList() async throws {
        let h = try await Harness(messages: [inbox: [msg("m1", 1, .seen), msg("m2", 2, .seen)]])
        defer { Task { await h.stop() } }
        try await h.settled()
        h.list.select(key: ListKey(message: "m2"))

        // Flat: prepended, the selection untouched, the badge adjusted by
        // the folder half. The fixture gets it too, for the reloads below.
        await h.fixture.addMessage(msg("m3", 3))
        h.mailbox.handleNewMessage(NewMessageNotification(accountId: "a", folderId: "in", message: msg("m3", 3)))
        #expect(ids(h.list.rows) == ["m3", "m2", "m1"])
        #expect(h.list.selectedKey == ListKey(message: "m2"))
        #expect(h.log.selections == ["m2"])
        #expect(h.mailbox.model.total == 3)
        #expect(h.mailbox.model.folder(inbox)?.unread == 1)

        // Not for another folder; not twice.
        h.mailbox.handleNewMessage(NewMessageNotification(accountId: "a", folderId: "trash", message: msg("t1", 3)))
        h.mailbox.handleNewMessage(NewMessageNotification(accountId: "a", folderId: "in", message: msg("m3", 3)))
        #expect(h.list.rows.count == 3)

        // Not when the filter would not list it.
        h.list.setListFilter(.unread)
        try await h.settled()
        #expect(ids(h.list.rows) == ["m3"])
        h.mailbox.handleNewMessage(NewMessageNotification(accountId: "a", folderId: "in", message: msg("m4", 4, .seen)))
        #expect(ids(h.list.rows) == ["m3"])
        await h.fixture.addMessage(msg("m5", 5))
        h.mailbox.handleNewMessage(NewMessageNotification(accountId: "a", folderId: "in", message: msg("m5", 5)))
        #expect(ids(h.list.rows) == ["m5", "m3"])

        // Not while the page is loading: the reply will include it.
        await h.fixture.delay(API.MessageList.name, .milliseconds(150))
        h.list.setListFilter(.all)
        #expect(h.mailbox.model.loading)
        h.mailbox.handleNewMessage(NewMessageNotification(accountId: "a", folderId: "in", message: msg("m6", 6)))
        #expect(h.list.rows.isEmpty)
        try await h.settled()
        #expect(ids(h.list.rows) == ["m5", "m3", "m2", "m1"], "the fixture never got m6; the list did not invent it")
        await h.fixture.delay(API.MessageList.name, .zero)

        // Grouped: into the conversation row (moved to the top), a new row
        // for an unlisted conversation, a reload without a thread id.
        h.scratch.settings.groupByConversation = true
        try await h.settled()
        await h.fixture.setMessages(threadedMessages(), in: inbox)
        h.mailbox.reloadMessages?()
        try await waitUntil { ids(h.list.rows) == ["T:t3", "b1", "T:t1"] }
        h.mailbox.handleNewMessage(NewMessageNotification(accountId: "a", folderId: "in", message: msg("a4", 7, thread: "t1")))
        #expect(ids(h.list.rows) == ["T:t1", "T:t3", "b1"])
        #expect(h.list.rows[0].summary?.messageCount == 4)
        #expect(h.list.rows[0].message.id == "a4")
        h.mailbox.handleNewMessage(NewMessageNotification(accountId: "a", folderId: "in", message: msg("d1", 8, thread: "t4")))
        #expect(ids(h.list.rows) == ["d1", "T:t1", "T:t3", "b1"])
        #expect(h.list.rows[0].key == ListKey(thread: "t4", message: "d1"))
        let lists = await h.fixture.threadListRequests.count
        h.mailbox.handleNewMessage(NewMessageNotification(accountId: "a", folderId: "in", message: msg("z1", 9)))
        #expect(h.mailbox.model.loading)
        try await waitUntil { await h.fixture.threadListRequests.count == lists + 1 && !h.mailbox.model.loading }
        #expect(ids(h.list.rows) == ["T:t3", "b1", "T:t1"], "reloaded from the fixture, which has no z1")
    }

    @Test func removeRowsSelectsTheNeighbourAndRestores() async throws {
        let h = try await Harness(messages: [inbox: (1...5).map { msg("m\($0)", $0, .seen) }])
        defer { Task { await h.stop() } }
        try await h.settled()
        #expect(ids(h.list.rows) == ["m5", "m4", "m3", "m2", "m1"])
        h.list.select(key: ListKey(message: "m4"))
        #expect(h.log.selections == ["m4"])

        // The selected row goes: the row now at its place takes over.
        let restore = h.list.removeRows(["m4"])
        #expect(ids(h.list.rows) == ["m5", "m3", "m2", "m1"])
        #expect(h.log.rowEvents.last == .neighbour)
        #expect(h.list.selectedKey == ListKey(message: "m3"))
        #expect(h.log.selections == ["m4", "m3"])
        #expect(h.mailbox.model.total == 4)

        // Put back where it was; the selection stays where it went.
        restore()
        #expect(ids(h.list.rows) == ["m5", "m4", "m3", "m2", "m1"])
        #expect(h.list.selectedKey == ListKey(message: "m3"))
        #expect(h.log.selections.count == 2)
        #expect(h.mailbox.model.total == 5)

        // A row that is not selected goes quietly; the last row hands the
        // selection to the new last one.
        _ = h.list.removeRows(["m5"])
        #expect(h.list.selectedKey == ListKey(message: "m3"))
        #expect(h.log.selections.count == 2)
        h.list.select(key: ListKey(message: "m1"))
        _ = h.list.removeRows(["m1"])
        #expect(h.list.selectedKey == ListKey(message: "m2"))
        #expect(h.log.selections.last == "m2")

        // The list ran empty: the pane is cleared.
        _ = h.list.removeRows(["m4", "m3", "m2"])
        #expect(h.list.rows.isEmpty)
        #expect(h.list.selectedKey == nil)
        #expect(h.log.selections.last == .some(nil))
        #expect(h.log.cleared == 1)
        #expect(h.list.listState == .status(
            icon: "mail-unread-symbolic", title: "No Messages", description: "This folder is empty.", retry: false
        ))

        // A restore after the list moved on does nothing.
        let stale = h.list.removeRows([])
        h.mailbox.reloadMessages?()
        try await h.settled()
        #expect(h.list.rows.count == 5)
        stale()
        let restoreLate = h.list.removeRows(["m5"])
        #expect(h.list.rows.count == 4)
        h.list.loadMessages()
        try await h.settled()
        restoreLate()
        #expect(h.list.rows.count == 5, "the reload brought m5 back; the stale restore did not double it")
    }

    @Test func removeRowsInGroupedModeEditsTheConversation() async throws {
        let h = try await Harness(messages: [inbox: threadedMessages()], grouped: true)
        defer { Task { await h.stop() } }
        try await h.settled()
        h.list.toggleThread("t1")
        try await waitUntil { h.list.rows.count == 6 }
        h.list.select(key: ListKey(thread: "t1"))
        #expect(h.log.selections == ["a3"], "a conversation row shows its newest member")

        // A member goes: the conversation is recomputed in place.
        let restore = h.list.removeRows(["a3"])
        #expect(ids(h.list.rows) == ["T:t3", "b1", "T:t1", "a1", "a2"])
        #expect(h.list.rows[2].summary?.messageCount == 2)
        #expect(h.list.rows[2].summary?.unreadCount == 0)
        #expect(h.list.rows[2].message.id == "a2")
        #expect(h.list.selectedKey == ListKey(thread: "t1"))
        #expect(h.log.selections.count == 1)
        restore()
        #expect(ids(h.list.rows) == ["T:t3", "b1", "T:t1", "a1", "a2", "a3"])
        #expect(h.list.rows[2].summary?.messageCount == 3)

        // Every member goes: the row goes, the neighbour takes over.
        let restoreAll = h.list.removeRows(["a1", "a2", "a3"])
        #expect(ids(h.list.rows) == ["T:t3", "b1"])
        #expect(h.list.selectedKey == ListKey(thread: "t2", message: "b1"))
        #expect(h.log.selections.last == "b1")
        restoreAll()
        #expect(ids(h.list.rows) == ["T:t3", "b1", "T:t1", "a1", "a2", "a3"])

        // A conversation whose members are not known is loaded again.
        let lists = await h.fixture.threadListRequests.count
        let noop = h.list.removeRows(["c2"])
        #expect(h.mailbox.model.loading)
        try await waitUntil { await h.fixture.threadListRequests.count == lists + 1 && !h.mailbox.model.loading }
        noop()
        #expect(h.list.rows.count == 6)
    }

    @Test func selectedIDsFetchesMembersFirst() async throws {
        let h = try await Harness(messages: [inbox: threadedMessages()], grouped: true)
        defer { Task { await h.stop() } }
        try await h.settled()
        h.list.select(key: ListKey(thread: "t1"))
        #expect(h.list.actionFlags.on && h.list.actionFlags.markRead && h.list.actionFlags.markUnread)

        let log = h.log
        h.list.selectedIDs { row, ids in
            log.ids.append(ids)
            #expect(row.thread)
        }
        #expect(log.ids.isEmpty, "the members are asked for first")
        try await waitUntil { !log.ids.isEmpty }
        #expect(log.ids == [["a1", "a2", "a3"]])
        #expect(await h.fixture.threadGetRequests.count == 1)
        #expect(h.list.rows.count == 3, "fetching members for an action does not unfold the row")

        // Known members: at once, no round trip.
        h.list.selectedIDs { _, ids in log.ids.append(ids) }
        #expect(log.ids.count == 2)
        #expect(await h.fixture.threadGetRequests.count == 1)

        // A plain row: itself.
        h.list.select(key: ListKey(thread: "t2", message: "b1"))
        h.list.selectedIDs { row, ids in
            log.ids.append(ids)
            #expect(!row.thread)
        }
        #expect(log.ids.last == ["b1"])
        #expect(h.list.rowSubject(h.list.rows[2]) == "s-a3")
        #expect(h.list.flagTarget(h.list.rows[2]))
        #expect(h.list.rowSubject(h.list.rows[1]) == "s-b1")

        // The selection moved on before the members arrived: nothing runs.
        await h.fixture.delay(API.ThreadGet.name, .milliseconds(100))
        h.list.select(key: ListKey(thread: "t3"))
        h.list.selectedIDs { _, ids in log.ids.append(ids) }
        h.list.select(key: ListKey(thread: "t2", message: "b1"))
        try await waitUntil { await h.fixture.threadGetRequests.count == 2 }
        try await Task.sleep(for: .milliseconds(150))
        #expect(log.ids.count == 3)
    }

    @Test func markReadTimerFollowsTheDelay() async throws {
        let h = try await Harness(messages: [
            inbox: [msg("m1", 1), msg("m2", 2), msg("m3", 3), msg("m4", 4, .seen)],
            outbox: [msg("o1", 5)],
        ])
        defer { Task { await h.stop() } }
        try await h.settled()

        // Delay 0: at once; a read message never.
        h.scratch.settings.markReadDelay = 0
        h.list.select(key: ListKey(message: "m4"))
        #expect(h.log.marks.isEmpty)
        #expect(h.list.actionFlags == messageActionState(h.list.rows[0], model: h.mailbox.model))
        #expect(h.list.actionFlags.markUnread && !h.list.actionFlags.markRead)
        h.list.select(key: ListKey(message: "m3"))
        #expect(h.log.marks == ["m3"])
        #expect(h.list.actionFlags.markRead && !h.list.actionFlags.markUnread)

        // Delay 1 (20 ms here): after the delay, unless the selection moved.
        h.scratch.settings.markReadDelay = 1
        h.list.select(key: ListKey(message: "m2"))
        #expect(h.log.marks == ["m3"])
        try await waitUntil { h.log.marks.count == 2 }
        #expect(h.log.marks == ["m3", "m2"])
        h.list.select(key: ListKey(message: "m1"))
        h.list.select(key: ListKey(message: "m2"))
        try await Task.sleep(for: .milliseconds(80))
        #expect(h.log.marks == ["m3", "m2", "m2"], "m1 was left before its timer fired")

        // Clearing the selection cancels the timer.
        h.list.select(key: ListKey(message: "m1"))
        h.list.select(key: nil)
        #expect(h.log.selections.last == .some(nil))
        #expect(h.list.actionFlags == .none)
        try await Task.sleep(for: .milliseconds(80))
        #expect(h.log.marks.count == 3)

        // The flags applied by the actions phase refresh the rows and the
        // actions.
        h.list.select(key: ListKey(message: "m1"))
        #expect(h.list.applyFlags(["m1", "m4"], set: [.seen]) == ["m1"])
        #expect(h.log.refreshed.last == [ListKey(message: "m1")])
        #expect(h.list.row(for: ListKey(message: "m1"))?.message.flags == [.seen])
        #expect(h.list.actionFlags.markUnread && !h.list.actionFlags.markRead)
        h.list.select(key: nil)

        // An outbox message is never marked read.
        h.scratch.settings.markReadDelay = 0
        h.select(outbox)
        try await h.loaded(outbox)
        #expect(h.list.inOutbox)
        h.list.select(key: ListKey(message: "o1"))
        #expect(h.log.marks.count == 3)
        #expect(h.list.actionFlags.outbox && !h.list.actionFlags.star)
    }

    @Test func outboxIsNeverGrouped() async throws {
        var queued = msg("o1", 5)
        queued.outbox = OutboxInfo(state: .queued, attempts: 0)
        let h = try await Harness(messages: [inbox: threadedMessages(), outbox: [queued]], grouped: true)
        defer { Task { await h.stop() } }
        try await h.settled()
        #expect(h.mailbox.model.grouped)
        #expect(h.list.folderRole == .inbox)

        h.select(outbox)
        try await h.loaded(outbox)
        #expect(!h.mailbox.model.grouped)
        #expect(h.list.inOutbox)
        #expect(await h.fixture.listRequests.count == 1)
        #expect(await h.fixture.threadListRequests.count == 1)
        #expect(ids(h.list.rows) == ["o1"])
        #expect(h.list.rows[0].key == ListKey(message: "o1"))

        h.select(inbox)
        try await h.loaded(inbox)
        #expect(h.mailbox.model.grouped)
        #expect(await h.fixture.threadListRequests.count == 2)

        // Turning grouping off reloads the folder flat.
        h.scratch.settings.groupByConversation = false
        #expect(!h.mailbox.model.grouped)
        #expect(h.list.rows.isEmpty, "a mode switch empties the list at once")
        try await h.settled()
        #expect(ids(h.list.rows) == ["c2", "b1", "c1", "a3", "a2", "a1"])
        #expect(await h.fixture.listRequests.count == 2)
    }

    @Test func disconnectFoldsAConversationWaitingForMembers() async throws {
        let h = try await Harness(messages: [inbox: threadedMessages()], grouped: true)
        defer { Task { await h.stop() } }
        try await h.settled()
        h.mailbox.model.nextCursor = "50"
        h.list.showLoadMore()
        h.list.loadMore()
        #expect(h.list.loadMoreState.spinner)
        await h.fixture.delay(API.ThreadGet.name, .milliseconds(200))
        h.list.toggleThread("t1")
        #expect(h.list.rows[2].loading)

        // The backend went away: the row folds back, the late reply is
        // dropped, no toast. The folder half's `bumpAll` clears the loading
        // flags (model.go); the list half only redraws the footer from them.
        h.mailbox.handleConnection(.unavailable("gone"))
        #expect(!h.list.rows[2].expanded && !h.list.rows[2].loading)
        #expect(h.list.rows.count == 3)
        #expect(!h.mailbox.model.loadingMore)
        h.list.handleConnection(.unavailable("gone"))
        #expect(!h.list.loadMoreState.spinner)
        try await Task.sleep(for: .milliseconds(300))
        #expect(h.list.rows.count == 3)
        #expect(h.log.toasts.isEmpty)
        #expect(!h.mailbox.model.expanded.contains("t1"))
        #expect(h.mailbox.model.members["t1"]?.fetching == false)

        // Unfolding again asks anew once the backend is back.
        await h.fixture.delay(API.ThreadGet.name, .zero)
        h.mailbox.handleConnection(.connected(Harness.info))
        try await h.settled()
        h.list.toggleThread("t1")
        try await waitUntil { h.list.rows.count == 6 }
        #expect(await h.fixture.threadGetRequests.count == 2)
    }
}

extension ListState {
    fileprivate var isStatus: Bool {
        if case .status = self {
            return true
        }
        return false
    }
}
