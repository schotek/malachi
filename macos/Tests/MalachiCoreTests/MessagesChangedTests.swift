// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// notify.messagesChanged (docs/api.md §5) over the mailbox controller
// against MailFixture: a Jira account hid notification mails in a mail
// account, or showed them again. The folders of that account are read
// again for their counts, and the selected folder is listed again when
// the notification is about it. The reading half (`ReadingHarness`): a
// Jira account's messages rebuilt in place are dropped from the message
// cache and fetched again by the pane, a single message and a
// conversation's cards alike. Also `jiraEditor`, which "edit account"
// route opens what for a Jira account.

private struct Timeout: Error {}

@MainActor
private func waitUntil(_ timeout: Duration = .seconds(5), _ cond: @MainActor () async -> Bool) async throws {
    let deadline = ContinuousClock.now + timeout
    while await !cond() {
        if ContinuousClock.now > deadline { throw Timeout() }
        try await Task.sleep(for: .milliseconds(10))
    }
}

/// A fixture, a connected client and a mailbox controller that gets the
/// daemon's notifications, as the app routes them.
@MainActor
private final class Harness {
    let fixture: MailFixture
    let client: RPCClient
    let scratch = ScratchSettings()
    let mailbox: MailboxController
    var reloads = 0
    var rebuilds = 0
    var toasts: [String] = []
    private var pump: Task<Void, Never>?

    init() async throws {
        let (accounts, folders) = testAccounts()
        fixture = try MailFixture()
        await fixture.setAccounts(accounts)
        await fixture.setFolders(folders)
        try await fixture.start()
        client = RPCClient(socketPath: fixture.path)
        mailbox = MailboxController(client: client, settings: scratch.settings, toast: { _ in })
        mailbox.onEntriesChanged = { [unowned self] in self.rebuilds += 1 }
        mailbox.reloadMessages = { [unowned self] in
            // The list half's loadMessages, as far as the folder half sees it.
            self.mailbox.model.listFolder = self.mailbox.model.selected
            self.reloads += 1
        }
        try await client.connect()
        mailbox.handleConnection(.connected(SystemInfo(version: "fake", protocolVersion: API.protocolVersion, pid: 7, storePath: "/tmp/s.db")))
        let client = client
        let mailbox = mailbox
        pump = Task { @MainActor in
            for await raw in client.notifications {
                if let n = try? DaemonNotification(raw) {
                    mailbox.handleNotification(n)
                }
            }
        }
        // The accounts and their folders loaded, the first Inbox selected.
        try await waitUntil { self.rebuilds >= 1 && self.mailbox.model.selected != nil }
    }

    /// folder.list calls so far.
    func folderLists() async -> Int {
        await fixture.callCount(API.FolderList.name)
    }

    func stop() async {
        pump?.cancel()
        mailbox.close()
        await client.close()
        await fixture.stop()
    }
}

private let inbox1 = FolderKey(account: "acc1", folder: "inbox")

@MainActor
@Suite(.serialized) struct MessagesChangedTests {
    @Test func theNamedFolderIsListedAgainWithItsCounts() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        #expect(h.mailbox.model.selected == inbox1)
        #expect(h.reloads == 1)
        #expect(h.mailbox.model.folder(inbox1)?.unread == 2)
        let lists = await h.folderLists()
        let rebuilds = h.rebuilds

        // Two notification mails were hidden: the Inbox counts less.
        var folders = testAccounts().1["acc1"]!
        let i = try #require(folders.firstIndex { $0.id == "inbox" })
        folders[i].unread = 0
        folders[i].total = 5
        await h.fixture.setFolders(folders, for: "acc1")
        try await h.fixture.push(.messagesChanged(MessagesChangedNotification(accountId: "acc1", folderIds: ["inbox", "trash"])))

        try await waitUntil { h.rebuilds > rebuilds }
        #expect(h.reloads == 2, "the selected folder is one of those named")
        #expect(await h.folderLists() == lists + 1, "the folders of that account alone")
        #expect(h.mailbox.model.folder(inbox1)?.unread == 0)
        #expect(h.mailbox.model.folder(inbox1)?.total == 5)
        #expect(h.mailbox.model.selected == inbox1, "the selection stays")
    }

    @Test func anotherFolderOnlyReadsTheCounts() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        let lists = await h.folderLists()
        let rebuilds = h.rebuilds
        try await h.fixture.push(.messagesChanged(MessagesChangedNotification(accountId: "acc1", folderIds: ["trash"])))
        try await waitUntil { h.rebuilds > rebuilds }
        #expect(h.reloads == 1, "the selected folder is not named")
        #expect(await h.folderLists() == lists + 1)
    }

    @Test func noFolderNamedMeansEveryFolderOfTheAccount() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        var rebuilds = h.rebuilds
        try await h.fixture.push(.messagesChanged(MessagesChangedNotification(accountId: "acc1")))
        try await waitUntil { h.rebuilds > rebuilds }
        #expect(h.reloads == 2, "the selected folder belongs to the account")

        // Another account's: its counts are read, the list stays.
        rebuilds = h.rebuilds
        let lists = await h.folderLists()
        try await h.fixture.push(.messagesChanged(MessagesChangedNotification(accountId: "acc2")))
        try await waitUntil { h.rebuilds > rebuilds }
        #expect(h.reloads == 2)
        #expect(await h.folderLists() == lists + 1)

        // A folder of that name in another account is not the selected one.
        rebuilds = h.rebuilds
        try await h.fixture.push(.messagesChanged(MessagesChangedNotification(accountId: "acc2", folderIds: ["inbox"])))
        try await waitUntil { h.rebuilds > rebuilds }
        #expect(h.reloads == 2)
    }

    @Test func anAccountThatIsNotShownIsLeftAlone() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        let lists = await h.folderLists()
        let rebuilds = h.rebuilds
        // A paused account, and one this client does not know.
        h.mailbox.handleMessagesChanged(MessagesChangedNotification(accountId: "acc3"))
        h.mailbox.handleMessagesChanged(MessagesChangedNotification(accountId: "nobody", folderIds: ["inbox"]))
        try await Task.sleep(for: .milliseconds(100))
        #expect(await h.folderLists() == lists)
        #expect(h.rebuilds == rebuilds && h.reloads == 1)
    }

    @Test func nothingSelectedListsNothing() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        h.mailbox.model.selected = nil
        let rebuilds = h.rebuilds
        h.mailbox.handleMessagesChanged(MessagesChangedNotification(accountId: "acc2"))
        try await waitUntil { h.rebuilds > rebuilds }
        // The rebuild selects the initial folder again, which lists it
        // once; the notification itself asked for no list.
        #expect(h.mailbox.model.selected == inbox1)
        #expect(h.reloads == 2)
    }

    /// Which "edit account" route opens what for a Jira account.
    @Test func jiraEditorOfARoute() {
        #expect(jiraEditor(requestToken: nil) == .settings, "Settings ▸ Accounts, Edit Account… in the status popover")
        #expect(jiraEditor(requestToken: .authFailed) == .token, "the sign-in banner")
        #expect(jiraEditor(requestToken: .authRequired) == .token)
        #expect(jiraEditor(requestToken: 0) == .token, "Sign In without a known reason")
    }
}


// MARK: - The reading half

private let jiraAccount: AccountID = "j"
private let web = FolderKey(account: "j", folder: "web")
private let assigned = FolderKey(account: "j", folder: "assigned")
/// 2026-09-01T10:00:00Z.
private let base = Date(timeIntervalSince1970: 1_788_256_800)

private func issueInfo(_ key: String) -> IssueInfo {
    IssueInfo(
        key: key, url: "https://acme.atlassian.net/browse/" + key, summary: "Summary of " + key, status: "To Do",
        statusCategory: .todo)
}

/// A message of issue `key` (its thread) dated `hours` after `base`.
private func item(_ id: String, _ hours: Int, key: String, kind: IssueItemKind = .comment, from: String = "Petr Svoboda") -> MessageSummary {
    MessageSummary(
        id: MessageID(id), accountId: jiraAccount, folderId: web.folder, threadId: ThreadID("issue-" + key),
        from: [Address(name: from, address: "")], subject: key + ": Summary of " + key,
        date: base.addingTimeInterval(Double(hours) * 3600), snippet: "p-" + id, flags: [.seen],
        hasAttachments: false, size: 0, issue: MessageIssue(info: issueInfo(key), item: kind))
}

/// WEB-1: a description and a comment (a conversation row); WEB-2: a
/// description alone (a plain row, the single-message view).
private func jiraItems(comment from: String = "Petr Svoboda") -> [MessageSummary] {
    [
        item("w1d", 1, key: "WEB-1", kind: .description),
        item("w1c", 4, key: "WEB-1", from: from),
        item("w2d", 2, key: "WEB-2", kind: .description),
    ]
}

private func body(_ id: String, _ text: String) -> MessageBodyResult {
    MessageBodyResult(
        messageId: MessageID(id), bodyState: .fetched, hasHtml: false, text: text, remoteContent: .block,
        sanitizerVersion: "1")
}

/// A fixture with a mail account (first, so its Inbox is the initial
/// folder) and a Jira account with a space folder and a virtual one, the
/// list controller, the message cache and the conversation controller,
/// wired as Integration wires the reading pane: the selected row shows
/// its conversation, or fetches its message as the single-message view
/// does; notify.messagesChanged lets the cache go of the account's
/// messages.
@MainActor
private final class ReadingHarness {
    let fixture: MailFixture
    let client: RPCClient
    let scratch = ScratchSettings()
    let mailbox: MailboxController
    let list: ListController
    let cache: MessageCache
    let conversation: ConversationController
    var changes: [ConversationController.Change] = []
    /// The messages the single-message view was asked to show, in order.
    var shown: [MessageID] = []
    private var pump: Task<Void, Never>?

    static let info = SystemInfo(version: "fake", protocolVersion: API.protocolVersion, pid: 7, storePath: "/tmp/s.db")

    init() async throws {
        fixture = try MailFixture()
        let jira = Account(
            id: jiraAccount,
            config: AccountConfig(
                name: "Acme Jira", email: "jana@acme.example", kind: .jira,
                jira: JiraConfig(siteUrl: "https://acme.atlassian.net", deployment: .cloud)),
            enabled: true, state: SyncState(accountId: jiraAccount, status: .idle), capabilities: [])
        await fixture.setAccounts([testAccount("a", email: "a@example.invalid"), jira])
        await fixture.setFolders([testFolder("in", path: "INBOX", role: .inbox)], for: "a")
        await fixture.setFolders([
            Folder(
                id: "assigned", accountId: jiraAccount, name: "Assigned to Me", path: "Assigned to Me", role: .none,
                subscribed: true, selectable: true, synced: true, unread: 0, total: 0, virtual: .assignedToMe),
            testFolder("web", path: "WEB", name: "Web"),
        ], for: jiraAccount)
        await fixture.setMessages([
            MessageSummary(
                id: "m1", accountId: "a", folderId: "in", threadId: "t1",
                from: [Address(name: "Petr", address: "petr@example.invalid")], subject: "s-m1", date: base,
                snippet: "", flags: [.seen], hasAttachments: false, size: 0),
        ], in: FolderKey(account: "a", folder: "in"))
        await fixture.setMessages(jiraItems(), in: web)
        await fixture.setMessages(jiraItems().filter { $0.threadId == "issue-WEB-1" }, in: assigned)
        try await fixture.start()
        client = RPCClient(socketPath: fixture.path)
        mailbox = MailboxController(client: client, settings: scratch.settings, sync: SyncController()) { _ in }
        list = ListController(mailbox: mailbox, settings: scratch.settings)
        cache = MessageCache(client: client) { _ in }
        conversation = ConversationController(list: list, cache: cache)
        list.onSelectedRowChanged = { [weak self] row in
            guard let self, !self.conversation.show(row), let row else { return }
            // The single-message view: the pane fetches the row's message.
            self.shown.append(row.message.id)
            self.cache.fetch(row.message) { _ in }
        }
        conversation.onChange = { [weak self] c in self?.changes.append(c) }
        mailbox.onMessagesChanged = { [weak self] n in self?.cache.evict(account: n.accountId) }
        try await client.connect()
        mailbox.handleConnection(.connected(ReadingHarness.info))
        let client = client
        let mailbox = mailbox
        pump = Task { @MainActor in
            for await raw in client.notifications {
                if let n = try? DaemonNotification(raw) {
                    mailbox.handleNotification(n)
                }
            }
        }
        try await loaded(FolderKey(account: "a", folder: "in"))
    }

    func loaded(_ k: FolderKey) async throws {
        try await waitUntil {
            self.mailbox.model.listFolder == k && !self.mailbox.model.loading && self.list.listState == .messages
        }
    }

    func select(_ k: FolderKey) async throws {
        mailbox.selectFolder(k, fav: false)
        try await loaded(k)
    }

    func bodies() async -> Int {
        await fixture.callCount(API.MessageBody.name)
    }

    func stop() async {
        pump?.cancel()
        list.close()
        mailbox.close()
        await client.close()
        await fixture.stop()
    }
}

@MainActor
@Suite(.serialized) struct MessagesChangedReadingTests {
    /// The single-message view: the cache drops the account's messages and
    /// the shown one is fetched again, rebuilt.
    @Test func theShownMessageIsFetchedAgain() async throws {
        let h = try await ReadingHarness()
        defer { Task { await h.stop() } }
        await h.fixture.setBody(body("w2d", "with the bot's header line"))
        try await h.select(web)
        h.list.select(key: ListKey(thread: "issue-WEB-2", message: "w2d"))
        try await waitUntil { h.cache.loaded("w2d")?.complete == true }
        #expect(h.shown == ["w2d"])
        #expect(await h.bodies() == 1)
        #expect(h.cache.loaded("w2d")?.body?.text == "with the bot's header line")
        #expect(h.cache.loaded("w2d")?.accountId == jiraAccount)
        let lists = await h.fixture.threadListRequests.count

        // The pass rebuilt the message in place: the pane shows it again
        // and the cache fetches it afresh; the folder is listed again.
        await h.fixture.setBody(body("w2d", "cleaned"))
        try await h.fixture.push(.messagesChanged(MessagesChangedNotification(accountId: jiraAccount, folderIds: ["web", "assigned"])))
        try await waitUntil { h.cache.loaded("w2d")?.body?.text == "cleaned" }
        #expect(h.shown == ["w2d", "w2d"])
        #expect(await h.bodies() == 2)
        #expect(await h.fixture.callCount(API.MessageGet.name) == 2, "the headers too: a re-attributed comment changes them")
        try await waitUntil { await h.fixture.threadListRequests.count == lists + 1 }
        #expect(h.list.selectedKey == ListKey(thread: "issue-WEB-2", message: "w2d"), "the selection stays")

        // Another account's notification leaves the Jira entry alone, and
        // one for another folder of the account drops the cache but shows
        // nothing again (nothing there changed).
        try await h.fixture.push(.messagesChanged(MessagesChangedNotification(accountId: "a", folderIds: ["in"])))
        try await Task.sleep(for: .milliseconds(100))
        #expect(h.cache.loaded("w2d")?.body?.text == "cleaned")
        #expect(h.shown.count == 2)
        try await h.fixture.push(.messagesChanged(MessagesChangedNotification(accountId: jiraAccount, folderIds: ["assigned"])))
        try await Task.sleep(for: .milliseconds(100))
        #expect(h.cache.loaded("w2d") == nil, "the account's entries go")
        #expect(h.shown.count == 2, "the shown folder is not named")
        #expect(await h.bodies() == 2)
    }

    /// The conversation view: the held bodies go, the pane asks again and
    /// gets the rebuilt ones, the members come back through one thread.get
    /// with their new senders.
    @Test func theShownConversationIsFetchedAgain() async throws {
        let h = try await ReadingHarness()
        defer { Task { await h.stop() } }
        await h.fixture.setBody(body("w1c", "ITSD-9 Eva Horáková added comment"))
        try await h.select(web)
        h.list.select(key: ListKey(thread: "issue-WEB-1"))
        try await waitUntil { h.conversation.model != nil }
        #expect(h.changes == [.loading, .opened])
        #expect(h.conversation.model?.items.map(\.sender) == ["Petr Svoboda", "Petr Svoboda"])
        // The pane asks for the card near the viewport.
        h.conversation.needsBody("w1c")
        try await waitUntil { h.conversation.loaded["w1c"]?.body != nil }
        #expect(await h.bodies() == 1)
        #expect(h.conversation.loaded["w1c"]?.body?.text == "ITSD-9 Eva Horáková added comment")
        let gets = await h.fixture.threadGetRequests.count
        let lists = await h.fixture.threadListRequests.count

        // The pass re-attributed the comment and cleaned its body.
        await h.fixture.setMessages(jiraItems(comment: "Eva Horáková"), in: web)
        await h.fixture.setBody(body("w1c", "cleaned"))
        try await h.fixture.push(.messagesChanged(MessagesChangedNotification(accountId: jiraAccount, folderIds: ["web", "assigned"])))
        try await waitUntil { h.changes.count > 2 }
        #expect(h.changes[2] == .updated, "the pane asks for the bodies again")
        #expect(h.conversation.loaded.isEmpty, "the held entries went")
        #expect(h.cache.loaded("w1c") == nil, "the cache's too")
        #expect(h.conversation.thread == "issue-WEB-1" && h.conversation.model != nil, "the cards stay up meanwhile")
        // The members follow the reload: one thread.get, the new sender.
        try await waitUntil { h.conversation.model?.items.map(\.sender) == ["Petr Svoboda", "Eva Horáková"] }
        #expect(await h.fixture.threadGetRequests.count == gets + 1)
        #expect(await h.fixture.threadListRequests.count == lists + 1)
        #expect(h.changes.last == .updated)
        // The pane asks again, as it does after `.updated`: the body is fetched afresh.
        h.conversation.needsBody("w1c")
        try await waitUntil { h.conversation.loaded["w1c"]?.body?.text == "cleaned" }
        #expect(await h.bodies() == 2)
        #expect(h.list.selectedKey == ListKey(thread: "issue-WEB-1"))
    }

    /// A virtual folder holds copies of every space's issues: shown, it is
    /// refreshed for a notification that names only the space folder.
    @Test func aVirtualFolderFollowsItsSpace() async throws {
        let h = try await ReadingHarness()
        defer { Task { await h.stop() } }
        try await h.select(assigned)
        h.list.select(key: ListKey(thread: "issue-WEB-1"))
        try await waitUntil { h.conversation.model != nil }
        h.conversation.needsBody("w1c")
        try await waitUntil { h.conversation.loaded["w1c"]?.body != nil }
        let lists = await h.fixture.threadListRequests.count
        let gets = await h.fixture.threadGetRequests.count

        await h.fixture.setMessages(jiraItems(comment: "Eva Horáková").filter { $0.threadId == "issue-WEB-1" }, in: assigned)
        try await h.fixture.push(.messagesChanged(MessagesChangedNotification(accountId: jiraAccount, folderIds: ["web"])))
        try await waitUntil { h.conversation.model?.items.map(\.sender) == ["Petr Svoboda", "Eva Horáková"] }
        #expect(h.conversation.loaded.isEmpty)
        #expect(await h.fixture.threadListRequests.count == lists + 1)
        #expect(await h.fixture.threadListRequests.last?.folderId == "assigned")
        #expect(await h.fixture.threadGetRequests.count == gets + 1)
    }

    /// The cache's account-scoped eviction (`LoadedCache.removeAll(of:)`).
    @Test func evictionIsPerAccount() {
        var c = LoadedCache()
        c.store("x", LoadedMessage(accountId: "a"))
        c.store("y", LoadedMessage(accountId: "j"))
        c.store("z", LoadedMessage())
        let w = LoadedMessage(msg: Message(summary: MessageSummary(
            id: "w", accountId: "j", folderId: "web", threadId: "t", from: [], subject: "", date: base, snippet: "",
            flags: [], hasAttachments: false, size: 0)))
        #expect(w.accountId == "j", "the account of a cached message.get")
        c.store("w", w)
        #expect(c.removeAll(of: "j") == ["w", "y", "z"], "the account's, and those of no known account")
        #expect(c["x"] != nil && c["y"] == nil && c["z"] == nil && c["w"] == nil)
        #expect(c.removeAll(of: "j").isEmpty)
    }
}
