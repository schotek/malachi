// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The conversation view's controller half (ConversationController) and
// what the list controller does for it (onSelectedRowChanged,
// onThreadMembersChanged, the mark-as-read of a conversation row), over
// MailFixture: a conversation row shows the whole conversation through a
// folder-scoped thread.get and marks only its newest message read; a member
// row keeps the single message; arrivals, flag changes and removals reach
// the model; bodies are fetched per card, message.get only when needed.

private struct Timeout: Error {}

@MainActor
private func waitUntil(_ timeout: Duration = .seconds(5), _ cond: @MainActor () async throws -> Bool) async throws {
    let deadline = ContinuousClock.now + timeout
    while try await !cond() {
        if ContinuousClock.now > deadline { throw Timeout() }
        try await Task.sleep(for: .milliseconds(10))
    }
}

private let account: AccountID = "a"
private let inbox = FolderKey(account: "a", folder: "in")
/// 2026-09-01T10:00:00Z.
private let base = Date(timeIntervalSince1970: 1_788_256_800)

private func msg(
    _ id: String, _ hours: Int, thread: String, from: String = "alice", attachments: Bool = false, _ flags: Flag...
) -> MessageSummary {
    MessageSummary(
        id: MessageID(id), accountId: account, folderId: inbox.folder, threadId: ThreadID(thread),
        from: [Address(name: from, address: from + "@example.invalid")], subject: "s-" + id,
        date: base.addingTimeInterval(Double(hours) * 3600), snippet: "p-" + id, flags: flags,
        hasAttachments: attachments, size: 0)
}

/// t1: three members, the newest unread and an older one unread too; t2: one
/// member; t3: two members, all read; t3 is the newest conversation.
private func messages() -> [MessageSummary] {
    [
        msg("a1", 1, thread: "t1", from: "bob"),
        msg("a2", 2, thread: "t1", from: "alice", .seen),
        msg("a3", 3, thread: "t1", from: "carol", attachments: true),
        msg("b1", 5, thread: "t2", from: "dave", .seen),
        msg("c1", 4, thread: "t3", from: "erin", .seen),
        msg("c2", 6, thread: "t3", from: "frank", .seen),
    ]
}

private func shape(_ m: Conversation.Model?) -> [String] {
    (m?.items ?? []).map { it in it.kind == .truncated ? "more" : (it.message?.id.rawValue ?? "") }
}

@MainActor
private final class Harness {
    let fixture: MailFixture
    let client: RPCClient
    let scratch: ScratchSettings
    let mailbox: MailboxController
    let list: ListController
    let cache: MessageCache
    let conversation: ConversationController
    var changes: [ConversationController.Change] = []
    var marks: [MessageID] = []
    var selections: [MessageID?] = []
    var shown: [Bool] = []
    var loads: [MessageID] = []

    static let info = SystemInfo(version: "fake", protocolVersion: API.protocolVersion, pid: 7, storePath: "/tmp/s.db")

    init(messages list: [MessageSummary] = messages(), grouped: Bool = true) async throws {
        fixture = try MailFixture()
        await fixture.setAccounts([testAccount("a", email: "a@example.invalid")])
        await fixture.setFolders([
            testFolder("in", path: "INBOX", role: .inbox),
            testFolder("trash", path: "Trash", role: .trash),
        ], for: account)
        await fixture.setMessages(list, in: inbox)
        try await fixture.start()
        client = RPCClient(socketPath: fixture.path)
        scratch = ScratchSettings()
        scratch.settings.groupByConversation = grouped
        scratch.settings.markReadDelay = 1
        mailbox = MailboxController(client: client, settings: scratch.settings, sync: SyncController()) { _ in }
        self.list = ListController(mailbox: mailbox, settings: scratch.settings)
        self.list.markReadTick = .milliseconds(50)
        cache = MessageCache(client: client) { _ in }
        conversation = ConversationController(list: self.list, cache: cache)
        // As Integration wires the reading pane.
        self.list.onSelectedRowChanged = { [weak self] row in
            guard let self else { return }
            self.shown.append(self.conversation.show(row))
        }
        self.list.onSelectedMessageChanged = { [weak self] s in self?.selections.append(s?.id) }
        self.list.onMarkRead = { [weak self] id in self?.marks.append(id) }
        conversation.onChange = { [weak self] c in self?.changes.append(c) }
        conversation.onLoaded = { [weak self] id, _ in self?.loads.append(id) }
        try await client.connect()
        mailbox.handleConnection(.connected(Harness.info))
        try await waitUntil { self.mailbox.model.listFolder == inbox && !self.mailbox.model.loading && self.list.listState == .messages }
    }

    /// Selects a row and waits until the conversation (if it is one) is
    /// built.
    func select(_ key: ListKey) async throws {
        list.select(key: key)
        if list.selectedRow?.showsConversation == true {
            try await waitUntil { self.conversation.model != nil }
        }
    }

    func stop() async {
        list.close()
        mailbox.close()
        await client.close()
        await fixture.stop()
    }
}

@MainActor
@Suite(.serialized) struct ConversationControllerTests {
    @Test func conversationRowShowsTheWholeConversation() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        #expect(h.list.rows.map { $0.key } == [
            ListKey(thread: "t3"), ListKey(thread: "t2", message: "b1"), ListKey(thread: "t1"),
        ])
        #expect(h.list.rows[0].showsConversation)
        #expect(!h.list.rows[1].showsConversation, "a single-message conversation is a plain row")

        try await h.select(ListKey(thread: "t1"))
        #expect(h.shown == [true])
        #expect(h.conversation.thread == "t1")
        #expect(h.changes == [.loading, .opened])
        #expect(await h.fixture.threadGetRequests == [ThreadGetParams(accountId: "a", threadId: "t1", folderId: "in")])
        #expect(shape(h.conversation.model) == ["a1", "a2", "a3"])
        #expect(h.conversation.model?.markRead == "a3")
        #expect(h.conversation.model?.scrollTo == 2)
        #expect(h.conversation.model?.items.map(\.sender) == ["bob", "alice", "carol"])
        // The toolbar still acts on the row (its newest member).
        #expect(h.selections == ["a3"])
        #expect(h.list.rows.count == 3, "showing the conversation does not unfold the row")

        // The same row announced again keeps the model.
        #expect(h.conversation.show(h.list.selectedRow))
        #expect(h.changes == [.loading, .opened])

        // Known members: no second thread.get for another visit.
        try await h.select(ListKey(thread: "t2", message: "b1"))
        #expect(h.shown == [true, false])
        #expect(h.conversation.thread == nil && h.conversation.model == nil)
        #expect(h.changes.last == .cleared)
        try await h.select(ListKey(thread: "t1"))
        #expect(await h.fixture.threadGetRequests.count == 1)
        #expect(shape(h.conversation.model) == ["a1", "a2", "a3"])
    }

    @Test func onlyTheNewestUnreadMemberIsMarkedAfterTheDelay() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        try await h.select(ListKey(thread: "t1"))
        #expect(h.marks.isEmpty, "not before the delay")
        try await waitUntil { !h.marks.isEmpty }
        try await Task.sleep(for: .milliseconds(150))
        #expect(h.marks == ["a3"], "a1 is unread too and stays so")

        // A conversation whose newest member is read marks nothing.
        try await h.select(ListKey(thread: "t3"))
        try await Task.sleep(for: .milliseconds(150))
        #expect(h.marks == ["a3"])

        // Leaving before the timer fires cancels it.
        await h.fixture.setMessages(messages().map { var s = $0; s.flags = []; return s }, in: inbox)
        h.list.loadMessages()
        try await waitUntil { !h.mailbox.model.loading && h.list.row(for: ListKey(thread: "t3"))?.summary?.unreadCount == 2 }
        h.list.select(key: ListKey(thread: "t3"))
        h.list.select(key: ListKey(thread: "t2", message: "b1"))
        try await Task.sleep(for: .milliseconds(200))
        #expect(h.marks == ["a3", "b1"], "c2's timer went with the selection; the plain row marks itself")
    }

    @Test func memberRowKeepsTheSingleMessage() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        h.list.toggleThread("t1")
        try await waitUntil { h.list.rows.count == 6 }
        try await h.select(ListKey(thread: "t1", message: "a1"))
        #expect(h.shown == [false])
        #expect(h.conversation.thread == nil && h.changes.isEmpty)
        #expect(h.selections == ["a1"])
        try await waitUntil { !h.marks.isEmpty }
        #expect(h.marks == ["a1"], "a member row marks its own message, as before")

        // The unfolded conversation row itself shows the conversation.
        try await h.select(ListKey(thread: "t1"))
        #expect(h.shown == [false, true])
        #expect(shape(h.conversation.model) == ["a1", "a2", "a3"])
    }

    @Test func flatListShowsSingleMessages() async throws {
        let h = try await Harness(grouped: false)
        defer { Task { await h.stop() } }
        try await h.select(ListKey(message: "a3"))
        #expect(h.shown == [false])
        #expect(await h.fixture.threadGetRequests.isEmpty)
    }

    @Test func membersFollowTheList() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        try await h.select(ListKey(thread: "t1"))
        let opened = h.changes.count

        // A new arrival in the conversation: the row keeps its key, the
        // model takes the member.
        let late = msg("a4", 7, thread: "t1", from: "gina")
        await h.fixture.addMessage(late)
        h.mailbox.handleNewMessage(NewMessageNotification(accountId: account, folderId: inbox.folder, message: late))
        #expect(h.list.selectedKey == ListKey(thread: "t1"))
        #expect(shape(h.conversation.model) == ["a1", "a2", "a3", "a4"])
        #expect(h.changes.count == opened + 1 && h.changes.last == .updated)
        #expect(h.conversation.model?.markRead == "a4")

        // A flag change: the member's card follows.
        #expect(h.list.applyFlags(["a4"], set: [.seen]) == ["a4"])
        #expect(h.conversation.model?.items[3].unread == false)
        #expect(h.conversation.model?.markRead == nil)
        #expect(h.changes.last == .updated)

        // A removal drops the card; its undo brings it back.
        let restore = h.list.removeRows(["a2"])
        #expect(shape(h.conversation.model) == ["a1", "a3", "a4"])
        restore()
        #expect(shape(h.conversation.model) == ["a1", "a2", "a3", "a4"])

        // A message of another conversation changes nothing here.
        let count = h.changes.count
        let other = msg("c3", 8, thread: "t3")
        h.mailbox.handleNewMessage(NewMessageNotification(accountId: account, folderId: inbox.folder, message: other))
        #expect(h.changes.count == count)
        #expect(shape(h.conversation.model) == ["a1", "a2", "a3", "a4"])
    }

    /// The account of the conversation tells the user's own messages
    /// (`Conversation.Item.mine`): mail by the address of its first sender,
    /// an item of an issue by what the site says.
    @Test func ownMessagesAreTold() async throws {
        var own = msg("a2", 2, thread: "t1", from: "a", .seen)
        own.from = [Address(name: "Alena", address: " A@Example.INVALID ")]
        let h = try await Harness(messages: [
            msg("a1", 1, thread: "t1", from: "bob", .seen),
            own,
            msg("a3", 3, thread: "t1", from: "carol", .seen),
        ])
        defer { Task { await h.stop() } }
        try await h.select(ListKey(thread: "t1"))
        #expect(h.conversation.model?.items.map(\.mine) == [false, true, false])
        #expect(h.conversation.model?.items.map(\.sender) == ["bob", "Alena", "carol"], "the name stays the sender's")

        // An arrival while the conversation is shown.
        let late = msg("a4", 7, thread: "t1", from: "a")
        await h.fixture.addMessage(late)
        h.mailbox.handleNewMessage(NewMessageNotification(accountId: account, folderId: inbox.folder, message: late))
        #expect(shape(h.conversation.model) == ["a1", "a2", "a3", "a4"])
        #expect(h.conversation.model?.items.map(\.mine) == [false, true, false, true])
    }

    @Test func ownItemsOfAnIssueAreTold() async throws {
        let info = IssueInfo(
            key: "MOB-3", url: "https://acme.atlassian.net/browse/MOB-3", summary: "Login screen flickers", status: "To Do",
            statusCategory: .todo)
        func comment(_ id: String, _ hours: Int, from: String, mine: Bool? = nil, via: String? = nil) -> MessageSummary {
            var s = msg(id, hours, thread: "issue-MOB-3", from: from, .seen)
            s.issue = MessageIssue(info: info, item: .comment, via: via, mine: mine)
            return s
        }
        let h = try await Harness(messages: [
            comment("c1", 1, from: "Jana Dvořáková"),
            comment("c2", 2, from: "a", mine: true),
            // The sender's address is the account's, the site says nothing: not the user's.
            comment("c3", 3, from: "a"),
            comment("c4", 4, from: "Petr Svoboda", mine: true, via: "Issue Sync"),
        ])
        defer { Task { await h.stop() } }
        try await h.select(ListKey(thread: "issue-MOB-3"))
        #expect(shape(h.conversation.model) == ["c1", "c2", "c3", "c4"])
        #expect(h.conversation.model?.items.map(\.mine) == [false, true, false, false])
    }

    @Test func reloadThatChangesTheConversationFetchesItAgain() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        try await h.select(ListKey(thread: "t1"))
        #expect(await h.fixture.threadGetRequests.count == 1)

        // A reload with the same shape keeps the members: no thread.get.
        h.mailbox.reloadMessages?()
        try await waitUntil { await h.fixture.threadListRequests.count == 2 && !h.mailbox.model.loading }
        #expect(await h.fixture.threadGetRequests.count == 1)
        #expect(shape(h.conversation.model) == ["a1", "a2", "a3"])

        // A member arrived meanwhile (a sync): asked for again and merged.
        await h.fixture.addMessage(msg("a5", 9, thread: "t1", from: "hana"))
        h.mailbox.reloadMessages?()
        try await waitUntil { shape(h.conversation.model) == ["a1", "a2", "a3", "a5"] }
        #expect(await h.fixture.threadGetRequests.count == 2)
        #expect(h.conversation.thread == "t1")
        #expect(h.changes.last == .updated)
    }

    @Test func failedThreadGetShowsWhatTheListingKnows() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        await h.fixture.fail(API.ThreadGet.name, with: RPCError(code: .storageError, message: "disk"))
        h.list.select(key: ListKey(thread: "t1"))
        try await waitUntil { h.conversation.model != nil }
        #expect(shape(h.conversation.model) == ["more", "a3"])
        #expect(h.conversation.model?.earlier == 2)
        #expect(h.changes == [.loading, .opened])
        try await Task.sleep(for: .milliseconds(150))
        #expect(h.marks.isEmpty, "nothing is marked without the members")
    }

    @Test func issueConversationMarksItsNewestCommentNeverAnEvent() async throws {
        let info = IssueInfo(
            key: "WEB-7", url: "https://acme.atlassian.net/browse/WEB-7", summary: "Checkout fails", status: "In Progress",
            statusCategory: .inProgress)
        func issueItem(_ id: String, _ hours: Int, _ kind: IssueItemKind, changes: [IssueChange] = [], _ flags: Flag...) -> MessageSummary {
            var s = msg(id, hours, thread: "issue-WEB-7", from: "Jana Dvořáková")
            s.flags = flags
            s.issue = MessageIssue(info: info, item: kind, changes: changes)
            return s
        }
        let h = try await Harness(messages: [
            issueItem("d", 1, .description, .seen),
            issueItem("c1", 2, .comment),
            issueItem("c2", 3, .comment),
            issueItem("e1", 4, .event, changes: [IssueChange(field: .status, from: "To Do", to: "In Progress")], .seen),
        ])
        defer { Task { await h.stop() } }
        try await h.select(ListKey(thread: "issue-WEB-7"))
        let m = try #require(h.conversation.model)
        #expect(shape(m) == ["d", "c1", "c2", "e1"])
        #expect(m.items.map(\.kind) == [.message, .message, .message, .event])
        #expect(m.items[3].eventLines == ["Status: To Do → In Progress"])
        #expect(m.issue?.key == "WEB-7" && m.issue?.status == "In Progress")
        #expect(m.markRead == "c2")
        try await waitUntil { !h.marks.isEmpty }
        try await Task.sleep(for: .milliseconds(150))
        #expect(h.marks == ["c2"], "c1 stays unread; the event is never marked")

        // An event has no body to fetch.
        h.conversation.needsBody("e1")
        try await Task.sleep(for: .milliseconds(30))
        #expect(await h.fixture.callCount(API.MessageBody.name) == 0)
        #expect(h.conversation.loaded["e1"] == nil)
    }

    @Test func bodiesAreFetchedPerCard() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        try await h.select(ListKey(thread: "t1"))
        #expect(await h.fixture.callCount(API.MessageBody.name) == 0, "nothing before the pane asks")

        // Without attachments: message.body alone.
        h.conversation.needsBody("a1")
        h.conversation.needsBody("a1")
        try await waitUntil { h.conversation.loaded["a1"]?.body != nil }
        #expect(await h.fixture.callCount(API.MessageBody.name) == 1)
        #expect(await h.fixture.callCount(API.MessageGet.name) == 0)
        #expect(h.conversation.loaded["a1"]?.body?.text == "body of a1")
        #expect(h.loads == ["a1"])

        // Held: no second request.
        h.conversation.needsBody("a1")
        try await Task.sleep(for: .milliseconds(30))
        #expect(await h.fixture.callCount(API.MessageBody.name) == 1)

        // With attachments (the chips): message.get too.
        h.conversation.needsBody("a3")
        try await waitUntil { h.conversation.loaded["a3"]?.complete == true }
        #expect(await h.fixture.callCount(API.MessageGet.name) == 1)

        // The recipients' disclosure (Cc): message.get for a card without
        // attachments too.
        h.conversation.needsBody("a1", details: true)
        try await waitUntil { h.conversation.loaded["a1"]?.msg != nil }
        #expect(await h.fixture.callCount(API.MessageGet.name) == 2)
        #expect(await h.fixture.callCount(API.MessageBody.name) == 2, "a1's body came from the cache")

        // An id that is not shown, and an entry adopted from the cache's
        // fan-out.
        h.conversation.needsBody("b1")
        try await Task.sleep(for: .milliseconds(30))
        #expect(h.conversation.loaded["b1"] == nil)
        let lm = LoadedMessage()
        h.conversation.adopt("b1", lm)
        #expect(h.conversation.loaded["b1"] == nil)
        h.conversation.adopt("a2", lm)
        #expect(h.conversation.loaded["a2"] === lm)

        // Over the budget, far entries go, the largest first; near ones stay.
        h.conversation.trim(keeping: ["a1"], budget: 0)
        #expect(Set(h.conversation.loaded.keys) == ["a1"])

        // Another conversation forgets the entries.
        try await h.select(ListKey(thread: "t3"))
        #expect(h.conversation.loaded.isEmpty)
    }
}
