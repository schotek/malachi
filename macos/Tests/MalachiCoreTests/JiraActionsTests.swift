// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The message actions on a Jira account that comments and forwards
// (capabilities ["comment", "forward"]) over MailFixture: Reply writes a
// comment (draft.create reply on the Jira account, never the mail
// prefill), Forward is written in a mail account (draft.create with
// messageAccountId), the compose window's From lists only accounts that
// write mail, New Message needs one, and a queued comment in the Jira
// outbox can be cancelled and retried like a message.

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

private let mailA: AccountID = "a"
private let mailB: AccountID = "b"
private let jira: AccountID = "j"
private let inboxA = FolderKey(account: "a", folder: "in")
private let inboxB = FolderKey(account: "b", folder: "binb")
private let web = FolderKey(account: "j", folder: "web")
private let jiraOutbox = FolderKey(account: "j", folder: "out")
/// 2026-09-01T10:00:00Z.
private let base = Date(timeIntervalSince1970: 1_788_256_800)

private let webIssue = IssueInfo(
    key: "WEB-1", url: "https://acme.atlassian.net/browse/WEB-1", summary: "Logo on the front page",
    status: "To Do", statusCategory: .todo
)

private func jiraAccount(capabilities: [Capability] = [.comment, .forward]) -> Account {
    Account(
        id: jira,
        config: AccountConfig(
            name: "Acme Jira", email: "jana@acme.example", kind: .jira,
            jira: JiraConfig(siteUrl: "https://acme.atlassian.net", deployment: .cloud)
        ),
        enabled: true, state: SyncState(accountId: jira, status: .idle), capabilities: capabilities
    )
}

/// A member of WEB-1 dated `hours` after `base`.
private func item(_ id: String, _ hours: Int, kind: IssueItemKind = .comment, _ flags: Flag...) -> MessageSummary {
    MessageSummary(
        id: MessageID(id), accountId: jira, folderId: web.folder, threadId: "issue-WEB-1",
        from: [Address(name: "Jana Dvořáková", address: "")], subject: "WEB-1: " + webIssue.summary,
        date: base.addingTimeInterval(Double(hours) * 3600), snippet: "p-" + id, flags: flags,
        hasAttachments: false, size: 0,
        issue: MessageIssue(info: webIssue, item: kind)
    )
}

/// One confirmation the controller asked for.
private struct Confirmation: Equatable {
    var heading: String
    var body: String
    var label: String
}

/// What the actions controller emitted.
@MainActor
private final class ActionLog {
    var toasts: [String] = []
    var composed: [ComposeParams] = []
    var confirmations: [Confirmation] = []
}

/// What the scripted handlers received.
private actor Recorder {
    var drafts: [DraftCreateParams] = []
    var downloads: [MessageDownloadParams] = []
    var retries: [OutboxRetryParams] = []

    func addDraft(_ p: DraftCreateParams) { drafts.append(p) }
    func addDownload(_ p: MessageDownloadParams) { downloads.append(p) }
    func addRetry(_ p: OutboxRetryParams) { retries.append(p) }
}

private func encode<T: Encodable>(_ value: T) throws -> Data {
    try JSONCoding.encoder().encode(value)
}

private func decode<T: Decodable>(_ type: T.Type, _ params: Data) throws -> T {
    try JSONCoding.decoder().decode(type, from: params)
}

/// Two mail accounts (the first one's Inbox is the initial folder) and a
/// Jira account with the space WEB and an outbox holding a failed comment.
@MainActor
private final class Harness {
    let fixture: MailFixture
    let client: RPCClient
    let scratch: ScratchSettings
    let mailbox: MailboxController
    let list: ListController
    let cache: MessageCache
    let actions: ActionsController
    let recorder = Recorder()
    let log = ActionLog()
    var toasts: [String] { log.toasts }
    var composed: [ComposeParams] { log.composed }
    var confirmations: [Confirmation] { log.confirmations }

    static let info = SystemInfo(version: "fake", protocolVersion: API.protocolVersion, pid: 7, storePath: "/tmp/s.db")

    init(accounts: [Account]? = nil) async throws {
        fixture = try MailFixture()
        let accounts = accounts ?? [
            testAccount("a", email: "petr@example.invalid", displayName: "Petr"),
            testAccount("b", email: "petr@work.example", displayName: "Petr"),
            jiraAccount(),
        ]
        await fixture.setAccounts(accounts)
        await fixture.setFolders([testFolder("in", path: "INBOX", role: .inbox)], for: mailA)
        await fixture.setFolders([testFolder("binb", path: "INBOX", role: .inbox)], for: mailB)
        await fixture.setFolders([
            testFolder("web", path: "WEB", name: "Web"),
            testFolder("out", path: "Outbox", role: .outbox),
        ], for: jira)
        await fixture.setMessages([item("w1d", 1, kind: .description, .seen), item("w1c", 2, .seen)], in: web)
        var queued = item("o1", 3, .seen)
        queued.outbox = OutboxInfo(state: .failed, attempts: 2, error: RPCError(code: .serverError, message: "500"))
        await fixture.setMessages([queued], in: jiraOutbox)
        let rec = recorder
        let downloaded = try encode(MessageDownloadResult(message: Message(summary: item("w1c", 2, .seen))))
        await fixture.on(API.MessageDownload.name) { params in
            await rec.addDownload(try decode(MessageDownloadParams.self, params))
            return downloaded
        }
        await fixture.on(API.OutboxRetry.name) { params in
            await rec.addRetry(try decode(OutboxRetryParams.self, params))
            return try encode(EmptyResult())
        }
        try await fixture.start()
        client = RPCClient(socketPath: fixture.path)
        scratch = ScratchSettings()
        scratch.settings.markReadDelay = 0
        mailbox = MailboxController(client: client, settings: scratch.settings) { _ in }
        list = ListController(mailbox: mailbox, settings: scratch.settings)
        cache = MessageCache(client: client) { _ in }
        let log = log
        actions = ActionsController(mailbox: mailbox, list: list, cache: cache, settings: scratch.settings) {
            log.toasts.append($0)
        }
        actions.confirm = { _, heading, body, label in
            log.confirmations.append(Confirmation(heading: heading, body: body, label: label))
            return true
        }
        actions.openCompose = { log.composed.append($0) }
        try await client.connect()
        mailbox.handleConnection(.connected(Harness.info))
        try await waitUntil { !self.mailbox.model.accounts.isEmpty && !self.mailbox.model.loading }
    }

    func select(_ k: FolderKey) async throws {
        mailbox.selectFolder(k, fav: false)
        try await waitUntil {
            self.mailbox.model.listFolder == k && !self.mailbox.model.loading && self.list.listState == .messages
        }
    }

    /// Lists the space and makes the members of WEB-1 known.
    func showIssue() async throws {
        try await select(web)
        var done = false
        list.ensureMembers("issue-WEB-1", { done = true })
        try await waitUntil { done && self.mailbox.model.message("w1c") != nil }
    }

    /// Answers draft.create with `result`, or fails with `error`.
    func onDraftCreate(_ result: DraftCreateResult? = nil, error: RPCError? = nil) async throws {
        let rec = recorder
        let answer = try result.map { try encode($0) }
        await fixture.on(API.DraftCreate.name) { params in
            await rec.addDraft(try decode(DraftCreateParams.self, params))
            if let error {
                throw error
            }
            return answer ?? Data("{}".utf8)
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
@Suite(.serialized) struct JiraActionsTests {
    @Test func commentAsksDraftCreateReplyOnTheJiraAccount() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        try await h.showIssue()
        let flags = h.actions.flags(for: try #require(h.mailbox.model.message("w1c")?.summary))
        #expect(flags.reply && flags.comment && flags.forward && !flags.replyAll && !flags.trash)

        let comment = DraftComment(issue: webIssue)
        let template = Draft(accountId: jira, subject: "WEB-1: Logo on the front page", inReplyTo: "w1c", comment: comment)
        try await h.onDraftCreate(DraftCreateResult(draft: template, quoted: .none))
        h.actions.openCompose(.reply, "w1c")
        try await waitUntil { h.composed.count == 1 }
        #expect(await h.recorder.drafts == [DraftCreateParams(accountId: jira, mode: .reply, messageId: "w1c")])
        let p = h.composed[0]
        #expect(p.kind == .reply)
        #expect(p.accountID == jira, "pinned to the Jira account")
        #expect(p.comment == comment)
        #expect(p.inReplyTo == "w1c")
        #expect(p.to.isEmpty)
        #expect(h.toasts.isEmpty)
        #expect(await h.fixture.callCount(API.MessageDownload.name) == 0, "nothing is quoted, nothing downloaded")
    }

    /// compose_open.go `openComposeFrom`: the comment goes before the
    /// download a reply makes for pictures kept on the server.
    @Test func aCommentDownloadsNoPictures() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        try await h.showIssue()
        var shown = MessageBodyResult(
            messageId: "w1c", bodyState: .fetched, hasHtml: true, html: "<p><img src=\"malachi-cid:j/w1c/2\"></p>",
            text: "", blocked: BlockedContent(remoteImages: 0), remoteContent: .block, sanitizerVersion: "1")
        shown.remotePictures = 1
        await h.fixture.setBody(shown)
        let s = try #require(h.mailbox.model.message("w1c")?.summary)
        h.cache.fetchBody(s) { _ in }
        try await waitUntil { replyNeedsDownload(h.cache.loaded("w1c")) }

        try await h.onDraftCreate(DraftCreateResult(
            draft: Draft(accountId: jira, inReplyTo: "w1c", comment: DraftComment(issue: webIssue)), quoted: .none))
        h.actions.openCompose(.reply, "w1c")
        try await waitUntil { h.composed.count == 1 }
        #expect(await h.fixture.callCount(API.MessageDownload.name) == 0)
        #expect(await h.recorder.downloads.isEmpty)
    }

    @Test func aFailedCommentOnlyToasts() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        try await h.showIssue()

        try await h.onDraftCreate(error: RPCError(code: .serverError, message: "500"))
        h.actions.openCompose(.reply, "w1c")
        try await waitUntil { h.toasts.count == 1 }
        #expect(h.toasts == ["Preparing the reply failed: the server returned an error"])
        // Where a mail reply falls back to the pane's own quote silently, a
        // comment has nothing to fall back on: said, and nothing opens.
        try await h.onDraftCreate(error: RPCError(code: .notImplemented, message: "no"))
        h.actions.openCompose(.reply, "w1c")
        try await waitUntil { h.toasts.count == 2 }
        #expect(h.toasts.last == "Preparing the reply is not available yet")
        try await Task.sleep(for: .milliseconds(30))
        #expect(h.composed.isEmpty, "never the mail prefill")
        #expect(await h.recorder.drafts.count == 2)
    }

    @Test func forwardIsWrittenInAMailAccount() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        try await h.showIssue()
        let forward = Draft(
            accountId: mailA, subject: "Fwd: WEB-1: Logo on the front page", textBody: "q", htmlBody: "<p>q</p>",
            forwarding: "w1c"
        )
        try await h.onDraftCreate(DraftCreateResult(draft: forward, quoted: .html))

        // Browsing the Jira account: the first account that writes mail.
        h.actions.openCompose(.forward, "w1c")
        try await waitUntil { h.composed.count == 1 }
        let req = try #require(await h.recorder.drafts.first)
        #expect(req.accountId == mailA)
        #expect(req.mode == .forward)
        #expect(req.messageId == "w1c")
        #expect(req.messageAccountId == jira)
        #expect(req.attribution?.hasPrefix("---------- Forwarded message ----------") == true)
        #expect(await h.recorder.downloads == [MessageDownloadParams(accountId: jira, messageId: "w1c")],
                "the Jira message is downloaded from its own account")
        let p = h.composed[0]
        #expect(p.kind == .forward)
        #expect(p.accountID == mailA)
        #expect(p.forwarding == "w1c")
        #expect(p.comment == nil)

        // A mail folder selected (an issue a search found from there): that
        // account.
        h.mailbox.model.selected = inboxB
        h.actions.openCompose(.forward, "w1c")
        try await waitUntil { h.composed.count == 2 }
        #expect(await h.recorder.drafts.last?.accountId == mailB)
        #expect(await h.recorder.drafts.last?.messageAccountId == jira)
        #expect(h.composed[1].accountID == mailB)

        // The fallback without draft.create is written there too.
        try await h.onDraftCreate(error: RPCError(code: .notImplemented, message: "no"))
        h.actions.openCompose(.forward, "w1c")
        try await waitUntil { h.composed.count == 3 }
        #expect(h.composed[2].accountID == mailB)
        #expect(h.composed[2].forwarding == "w1c")
    }

    @Test func aMailForwardNamesNoMessageAccount() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        await h.fixture.setMessages([
            MessageSummary(
                id: "m1", accountId: mailA, folderId: inboxA.folder, from: [Address(address: "x@example.invalid")],
                subject: "s", date: base, snippet: "", flags: [.seen], hasAttachments: false, size: 0),
        ], in: inboxA)
        // Listed again with the message.
        try await h.select(web)
        try await h.select(inboxA)
        try await h.onDraftCreate(DraftCreateResult(draft: Draft(accountId: mailA, subject: "Fwd: s", forwarding: "m1"), quoted: .html))
        h.mailbox.model.selected = inboxB
        h.actions.openCompose(.forward, "m1")
        try await waitUntil { h.composed.count == 1 }
        let req = try #require(await h.recorder.drafts.first)
        #expect(req.accountId == mailA, "a mail message goes out from its own account")
        #expect(req.messageAccountId == nil)
    }

    @Test func forwardNeedsAnAccountThatWritesMail() async throws {
        let h = try await Harness(accounts: [jiraAccount()])
        defer { Task { await h.stop() } }
        try await h.showIssue()
        let flags = h.actions.flags(for: try #require(h.mailbox.model.message("w1c")?.summary))
        #expect(flags.reply && flags.comment)
        #expect(!flags.forward)
        #expect(flags.unsupported.contains(.forward))

        // An accelerator bypassing the disabled action creates nothing.
        try await h.onDraftCreate(error: RPCError(code: .serverError, message: "not to be asked"))
        h.actions.openCompose(.forward, "w1c")
        try await Task.sleep(for: .milliseconds(80))
        #expect(await h.recorder.drafts.isEmpty)
        #expect(h.composed.isEmpty && h.toasts.isEmpty)
    }

    @Test func newMessageNeedsAnAccountThatWritesMail() async throws {
        let only = try await Harness(accounts: [jiraAccount()])
        defer { Task { await only.stop() } }
        #expect(!Capabilities.canComposeNew(only.mailbox.model.accounts))

        let both = try await Harness()
        defer { Task { await both.stop() } }
        #expect(Capabilities.canComposeNew(both.mailbox.model.accounts))
    }

    @Test func theConversationCardsOfferCommentAndForward() {
        let s = item("w1c", 2)
        let a = Conversation.cardActions(jiraAccount(), s, composeAccount: true)
        #expect(a.reply && a.comment && a.forward && !a.replyAll)
        #expect(!Conversation.cardActions(jiraAccount(), s, composeAccount: false).forward)
    }

    @Test func aQueuedCommentIsRetriedAndCancelledInTheJiraOutbox() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        try await h.select(jiraOutbox)
        #expect(!h.mailbox.model.grouped, "the outbox is flat")
        #expect(!h.list.actionFlags.unsupported.contains(.trash), "Delete is there to cancel")
        let queued = try #require(h.mailbox.model.message("o1")?.summary)
        let flags = h.actions.flags(for: queued)
        #expect(flags.outbox && flags.trash)

        h.actions.retryOutbox("o1")
        #expect(h.mailbox.model.message("o1")?.summary.outbox?.state == .queued)
        try await waitUntil { await h.recorder.retries.count == 1 }
        #expect(await h.recorder.retries == [OutboxRetryParams(accountId: jira, messageId: "o1")])

        h.actions.trash(["o1"], subject: "WEB-1: Logo on the front page")
        try await waitUntil { await h.fixture.deleteRequests.count == 1 }
        #expect(h.confirmations.map(\.heading) == ["Cancel sending this message?"])
        #expect(await h.fixture.deleteRequests == [MessageDeleteParams(accountId: jira, messageIds: ["o1"])])
        #expect(h.list.rows.isEmpty)
        #expect(h.toasts.isEmpty)
    }

    @Test func theFromListHasOnlyAccountsThatWriteMail() async throws {
        let fake = try FakeDaemon()
        let accounts = [jiraAccount(), testAccount("a", email: "petr@example.invalid")]
        let list = try encode(AccountListResult(accounts: accounts))
        await fake.on(API.AccountList.name) { _ in list }
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
        let compose = ComposeController(client: client, settings: scratch.settings)
        compose.refreshAccounts()
        try await waitUntil { !compose.knownAccounts.isEmpty }
        #expect(compose.accounts.map(\.id) == [mailA])
        #expect(!compose.placeholder)
        #expect(compose.knownAccounts.map(\.id) == [jira, mailA], "a comment window finds its account")
        #expect(compose.selfAddress.address == "petr@example.invalid")
    }

    @Test func issueTrackersAloneWriteFromThePlaceholder() async throws {
        let fake = try FakeDaemon()
        let list = try encode(AccountListResult(accounts: [jiraAccount()]))
        await fake.on(API.AccountList.name) { _ in list }
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
        let compose = ComposeController(client: client, settings: scratch.settings)
        compose.refreshAccounts()
        try await waitUntil { !compose.knownAccounts.isEmpty }
        #expect(compose.placeholder)
        #expect(compose.accounts == ComposeController.placeholderAccounts)
    }
}
