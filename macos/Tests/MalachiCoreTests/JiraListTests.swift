// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The message list of a Jira account (MailModel+Jira.swift, the Jira parts
// of MailModel, MailModel+Threads and the list controller): its folders are
// always listed as conversations, one per issue, the rows carry the issue,
// an event never shows as unread, and a notified message brings the issue
// as it is now. Over MailFixture where the controller is involved.

private struct Timeout: Error {}

@MainActor
private func waitUntil(_ timeout: Duration = .seconds(5), _ cond: @MainActor () async throws -> Bool) async throws {
    let deadline = ContinuousClock.now + timeout
    while try await !cond() {
        if ContinuousClock.now > deadline { throw Timeout() }
        try await Task.sleep(for: .milliseconds(10))
    }
}

private let mailAccount: AccountID = "a"
private let jiraAccount: AccountID = "j"
private let inbox = FolderKey(account: "a", folder: "in")
private let web = FolderKey(account: "j", folder: "web")
private let assigned = FolderKey(account: "j", folder: "assigned")
private let jiraOutbox = FolderKey(account: "j", folder: "out")
/// 2026-09-01T10:00:00Z.
private let base = Date(timeIntervalSince1970: 1_788_256_800)

private func jiraAccountFixture(enabled: Bool = true) -> Account {
    Account(
        id: jiraAccount,
        config: AccountConfig(
            name: "Acme Jira", email: "jana@acme.example", kind: .jira,
            jira: JiraConfig(siteUrl: "https://acme.atlassian.net", deployment: .cloud)
        ),
        enabled: enabled, state: SyncState(accountId: jiraAccount, status: .idle), capabilities: []
    )
}

private func issue(_ key: String, status: String = "To Do", category: IssueStatusCategory? = .todo) -> IssueInfo {
    IssueInfo(
        key: key, url: "https://acme.atlassian.net/browse/" + key, summary: "Summary of " + key, status: status,
        statusCategory: category
    )
}

/// A message of issue `key` (its thread) dated `hours` after `base`.
private func item(
    _ id: String, _ hours: Int, key: String, kind: IssueItemKind = .comment, issue info: IssueInfo? = nil,
    changes: [IssueChange] = [], from: String = "Jana Dvořáková", _ flags: Flag...
) -> MessageSummary {
    let info = info ?? issue(key)
    return MessageSummary(
        id: MessageID(id), accountId: jiraAccount, folderId: web.folder, threadId: ThreadID("issue-" + key),
        from: [Address(name: from, address: "")], subject: key + ": " + info.summary,
        date: base.addingTimeInterval(Double(hours) * 3600), snippet: "p-" + id, flags: flags,
        hasAttachments: false, size: 0,
        issue: MessageIssue(info: info, item: kind, changes: changes)
    )
}

private func mailMessage(_ id: String, _ hours: Int, thread: String) -> MessageSummary {
    MessageSummary(
        id: MessageID(id), accountId: mailAccount, folderId: inbox.folder, threadId: ThreadID(thread),
        from: [Address(name: "Petr", address: "petr@example.invalid")], subject: "s-" + id,
        date: base.addingTimeInterval(Double(hours) * 3600), snippet: "p-" + id, flags: [.seen],
        hasAttachments: false, size: 0
    )
}

/// Two issues: WEB-1 with a description and a comment, WEB-2 with a
/// description and a status change (the latest member).
private func jiraItems() -> [MessageSummary] {
    [
        item("w1d", 1, key: "WEB-1", kind: .description, .seen),
        item("w1c", 4, key: "WEB-1"),
        item("w2d", 2, key: "WEB-2", kind: .description, .seen),
        item(
            "w2e", 3, key: "WEB-2", kind: .event,
            changes: [IssueChange(field: .status, from: "To Do", to: "In Progress")], .seen
        ),
    ]
}

private func rowIDs(_ rows: [ListRow]) -> [String] {
    rows.map { r in r.thread ? "T:" + (r.key.thread?.rawValue ?? "") : r.message.id.rawValue }
}

/// A fixture with a mail account (first, so its Inbox is the initial
/// folder) and a Jira account, a connected client and the two controllers.
@MainActor
private final class Harness {
    let fixture: MailFixture
    let client: RPCClient
    let scratch: ScratchSettings
    let mailbox: MailboxController
    let list: ListController
    var hints: [SelectionHint] = []

    static let info = SystemInfo(version: "fake", protocolVersion: API.protocolVersion, pid: 7, storePath: "/tmp/s.db")

    init(grouped: Bool = false) async throws {
        fixture = try MailFixture()
        await fixture.setAccounts([testAccount("a", email: "a@example.invalid"), jiraAccountFixture()])
        await fixture.setFolders([testFolder("in", path: "INBOX", role: .inbox)], for: mailAccount)
        await fixture.setFolders([
            Folder(
                id: "assigned", accountId: jiraAccount, name: "Assigned to Me", path: "Assigned to Me", role: .none,
                subscribed: true, selectable: true, synced: true, unread: 0, total: 0, virtual: .assignedToMe
            ),
            testFolder("web", path: "WEB", name: "Web"),
            testFolder("out", path: "Outbox", role: .outbox),
        ], for: jiraAccount)
        await fixture.setMessages([mailMessage("m1", 1, thread: "t1"), mailMessage("m2", 2, thread: "t1")], in: inbox)
        await fixture.setMessages(jiraItems(), in: web)
        await fixture.setMessages(jiraItems().filter { $0.issue?.info.key == "WEB-1" }, in: assigned)
        try await fixture.start()
        client = RPCClient(socketPath: fixture.path)
        scratch = ScratchSettings()
        scratch.settings.groupByConversation = grouped
        mailbox = MailboxController(client: client, settings: scratch.settings, sync: SyncController()) { _ in }
        list = ListController(mailbox: mailbox, settings: scratch.settings)
        list.onRows = { [weak self] _, hint in self?.hints.append(hint) }
        try await client.connect()
        mailbox.handleConnection(.connected(Harness.info))
        try await loaded(inbox)
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

    func stop() async {
        list.close()
        mailbox.close()
        await client.close()
        await fixture.stop()
    }
}

@MainActor
@Suite(.serialized) struct JiraListTests {
    @Test func jiraFolderIsListedAsConversationsWithTheSettingOff() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        // The mail Inbox, first: flat, as the setting says.
        #expect(!h.mailbox.model.grouped)
        #expect(await h.fixture.listRequests.map(\.folderId) == ["in"])
        #expect(await h.fixture.threadListRequests.isEmpty)

        try await h.select(web)
        #expect(h.mailbox.model.grouped)
        #expect(h.mailbox.model.alwaysGrouped(web))
        #expect(await h.fixture.threadListRequests.map(\.folderId) == ["web"])
        #expect(await h.fixture.listRequests.count == 1, "no message.list for a Jira folder")
        #expect(rowIDs(h.list.rows) == ["T:issue-WEB-1", "T:issue-WEB-2"])
        #expect(h.list.rows[0].summary?.issue?.key == "WEB-1")
        // Nothing selected: the actions are those of the listed account.
        #expect(h.list.selectedKey == nil)
        #expect(h.list.actionFlags.unsupported == [.reply, .replyAll, .forward, .trash, .move, .archive, .junk])

        // A virtual folder of the account too.
        try await h.select(assigned)
        #expect(h.mailbox.model.grouped)
        #expect(await h.fixture.threadListRequests.map(\.folderId) == ["web", "assigned"])

        // Back in the mail account: flat again.
        try await h.select(inbox)
        #expect(!h.mailbox.model.grouped)
        #expect(!h.mailbox.model.alwaysGrouped(inbox))
        #expect(await h.fixture.listRequests.map(\.folderId) == ["in", "in"])
        #expect(rowIDs(h.list.rows) == ["m2", "m1"])
        #expect(h.list.actionFlags == .none)
    }

    @Test func jiraOutboxStaysFlat() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        // The outbox is listed once something is in it.
        await h.fixture.setMessages([item("o1", 1, key: "WEB-1")], in: jiraOutbox)
        h.mailbox.loadAccounts()
        try await waitUntil { h.mailbox.model.folder(jiraOutbox)?.total == 1 }
        #expect(h.mailbox.model.alwaysGrouped(jiraOutbox), "the account is always grouped…")
        try await h.select(jiraOutbox)
        #expect(!h.mailbox.model.grouped, "…but its outbox is not")
        #expect(await h.fixture.listRequests.last?.folderId == "out")
        #expect(await h.fixture.threadListRequests.isEmpty)
    }

    @Test func togglingTheSettingKeepsTheJiraList() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        try await h.select(web)
        let rows = h.list.rows
        let lists = await h.fixture.threadListRequests.count
        h.list.select(key: rows[1].key)
        h.hints = []

        h.scratch.settings.groupByConversation = true
        h.scratch.settings.groupByConversation = false
        #expect(h.mailbox.model.grouped)
        #expect(h.list.rows == rows, "the rows stay")
        #expect(!h.hints.contains(.clear), "the list is not emptied")
        #expect(h.list.selectedKey == rows[1].key)
        try await Task.sleep(for: .milliseconds(50))
        #expect(await h.fixture.threadListRequests.count == lists, "nothing to ask again")

        // In the mail account the setting still switches the mode.
        try await h.select(inbox)
        h.scratch.settings.groupByConversation = true
        #expect(h.mailbox.model.grouped)
        try await h.loaded(inbox)
        #expect(await h.fixture.threadListRequests.last?.folderId == "in")
    }

    @Test func aSyncOfTheAccountReloadsItsVirtualFolder() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        try await h.select(assigned)
        let lists = await h.fixture.threadListRequests.count

        // A pass over the space WEB: the virtual folder shows its issues.
        h.mailbox.handleSyncState(SyncState(accountId: jiraAccount, status: .syncing, folderId: "web"))
        h.mailbox.handleSyncState(SyncState(accountId: jiraAccount, status: .idle, folderId: "web"))
        try await waitUntil { await h.fixture.threadListRequests.count == lists + 1 }
        #expect(await h.fixture.threadListRequests.last?.folderId == "assigned")

        // A space is not reloaded for a pass over another folder.
        try await h.select(web)
        let now = await h.fixture.threadListRequests.count
        h.mailbox.handleSyncState(SyncState(accountId: jiraAccount, status: .syncing, folderId: "assigned"))
        h.mailbox.handleSyncState(SyncState(accountId: jiraAccount, status: .idle, folderId: "assigned"))
        try await Task.sleep(for: .milliseconds(100))
        #expect(await h.fixture.threadListRequests.count == now)
    }

    @Test func rowsCarryTheIssue() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        try await h.select(web)
        // WEB-2's latest member is its status change.
        let t2 = try #require(h.list.rows.first { $0.key.thread == "issue-WEB-2" }?.summary)
        let row = summaryThread(t2, expanded: false, loading: false)
        let issue = try #require(row.issue)
        #expect(issue.key == "WEB-2")
        #expect(issue.summary == "Summary of WEB-2")
        #expect(issue.status == "To Do")
        #expect(issue.statusStyle == .todo)
        #expect(issue.event)
        #expect(issue.eventText == "Status: To Do → In Progress")
        #expect(!issue.unread)

        // The mail rows have none.
        try await h.select(inbox)
        #expect(h.list.rowMessage(h.list.rows[0].message).issue == nil)
    }
}

/// The row projections and the model's in-place changes, without a daemon.
@MainActor
@Suite struct JiraRowTests {
    @Test func messageRowProjection() throws {
        let comment = item("c1", 1, key: "ITSD-7", issue: issue("ITSD-7", status: "Done", category: .done))
        let row = summaryMessage(comment)
        let r = try #require(row.issue)
        #expect(r.key == "ITSD-7")
        #expect(r.summary == "Summary of ITSD-7")
        #expect(r.status == "Done")
        #expect(r.statusStyle == .done)
        #expect(!r.event)
        #expect(row.unread, "an unseen comment is unread")
        #expect(r.unread)

        // An event is never unread, whatever its flags.
        let event = item(
            "e1", 2, key: "ITSD-7", kind: .event,
            changes: [IssueChange(field: .assignee, from: "", to: "Jana Dvořáková")]
        )
        let er = summaryMessage(event)
        #expect(!er.unread)
        #expect(er.issue?.event == true)
        #expect(er.issue?.eventText == "Assignee: Unassigned → Jana Dvořáková")

        // An internal service-desk comment has the badge.
        var internalNote = item("i1", 3, key: "ITSD-7", .seen)
        internalNote.issue?.visibility = .internal
        #expect(summaryMessage(internalNote).issue?.internal == true)
        #expect(summaryMessage(internalNote).issue?.internalLabel == "Internal")

        // A mail message has no issue and keeps its unread state.
        let mail = mailMessage("m1", 1, thread: "t1")
        #expect(summaryMessage(mail).issue == nil)
        #expect(!summaryMessage(mail).unread)
    }

    @Test func searchRowsCarryTheIssue() {
        var model = MailModel()
        let s = item("c1", 1, key: "WEB-3")
        model.search.active = true
        model.setSearchResults(SearchQueryResult(
            results: [SearchResult(message: s, snippet: "a hit", ranges: [], score: 1)], page: PageInfo(total: 1)
        ))
        let row = model.rowMessage(s)
        #expect(row.issue?.key == "WEB-3")
        #expect(row.snippet == "a hit")
    }

    @Test func applyNewMessageCarriesTheIssue() throws {
        var model = MailModel(grouped: true)
        model.listFolder = web
        let items = jiraItems()
        let t1 = ThreadSummary(
            id: "issue-WEB-1", accountId: jiraAccount, subject: items[1].subject, participants: items[1].from,
            messageCount: 2, unreadCount: 1, latestDate: items[1].date, latest: items[1], snippet: items[1].snippet,
            flags: [], hasAttachments: false, folderIds: ["web"], issue: issue("WEB-1")
        )
        model.setThreads([t1], page: PageInfo(total: 1))

        // A new comment comes with the issue as it is now: the row follows.
        let moved = issue("WEB-1", status: "In Progress", category: .inProgress)
        model.applyNewMessage(item("w1n", 6, key: "WEB-1", issue: moved), filter: .all, selected: ListKey())
        var t = try #require(model.threads.first)
        #expect(t.issue == moved)
        #expect(t.messageCount == 3)
        #expect(t.unreadCount == 2)
        #expect(summaryThread(t, expanded: false, loading: false).issue?.status == "In Progress")

        // An event, even one delivered unseen, is never unread.
        let done = issue("WEB-1", status: "Done", category: .done)
        let event = item(
            "w1e", 7, key: "WEB-1", kind: .event, issue: done,
            changes: [IssueChange(field: .status, from: "In Progress", to: "Done")]
        )
        model.applyNewMessage(event, filter: .all, selected: ListKey())
        t = try #require(model.threads.first)
        #expect(t.unreadCount == 2)
        #expect(t.issue?.statusCategory == .done)
        #expect(summaryThread(t, expanded: false, loading: false).issue?.event == true)

        // A new conversation starts with the issue of its first message.
        model.applyNewMessage(item("w9", 8, key: "WEB-9"), filter: .all, selected: ListKey())
        let t9 = try #require(model.threads.first)
        #expect(t9.id == "issue-WEB-9")
        #expect(t9.issue?.key == "WEB-9")
        #expect(t9.unreadCount == 1)
        let unseenEvent = item(
            "w8e", 9, key: "WEB-8", kind: .event, changes: [IssueChange(field: .status, from: "A", to: "B")]
        )
        model.applyNewMessage(unseenEvent, filter: .all, selected: ListKey())
        #expect(model.threads.first?.unreadCount == 0)

        // A message without an issue leaves the conversation's alone.
        let plain = MessageSummary(
            id: "p1", accountId: jiraAccount, folderId: "web", threadId: "issue-WEB-9", from: [], subject: "x",
            date: base.addingTimeInterval(36000), snippet: "", flags: [.seen], hasAttachments: false, size: 0
        )
        model.applyNewMessage(plain, filter: .all, selected: ListKey())
        #expect(model.threads.first { $0.id == "issue-WEB-9" }?.issue?.key == "WEB-9")
    }

    @Test func alwaysGroupedFollowsTheAccountKind() {
        var model = MailModel(accounts: [testAccount("a"), jiraAccountFixture()])
        #expect(model.alwaysGrouped(FolderKey(account: "j", folder: "anything")))
        #expect(!model.alwaysGrouped(FolderKey(account: "a", folder: "in")))
        #expect(!model.alwaysGrouped(FolderKey(account: "gone", folder: "in")))
        #expect(!model.alwaysGrouped(nil))
        model.accounts = []
        #expect(!model.alwaysGrouped(web))
    }
}
