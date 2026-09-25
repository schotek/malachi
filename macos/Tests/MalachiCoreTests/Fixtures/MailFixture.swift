// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
@testable import MalachiCore

/// A scripted malachid for the controller tests: a `FakeDaemon` that answers
/// the methods the main window asks from canned accounts, folders and sync
/// states, records what was asked, fails or delays a method on request and
/// pushes notifications.
///
/// Phase 1 serves `system.info`, `account.list`, `folder.list`,
/// `sync.status`, `sync.trigger` (recorded in `triggers`) and `config.get`.
/// The list phase adds `message.list/get/body/flag/move/delete` and
/// `thread.list/get` over `messages` (per folder), `bodies` and `details`,
/// with the folder counters following every change; the requests are
/// recorded in `listRequests`, `threadListRequests`, `threadGetRequests`,
/// `flagRequests`, `moveRequests` and `deleteRequests`. A handler that
/// needs no fixture state can go straight to `on(_:_:)`.
actor MailFixture {
    /// The `system.info` answer.
    var info = SystemInfoResult(version: "fake", protocolVersion: API.protocolVersion, pid: 7, storePath: "/tmp/s.db")
    /// What `account.list` returns.
    var accounts: [Account] = []
    /// What `folder.list` returns per account; an unknown account is
    /// `accountNotFound`.
    var folders: [AccountID: [Folder]] = [:]
    /// What `sync.status` returns.
    var syncStates: [SyncState] = []
    /// What `config.get` returns.
    var preferences = Preferences(syncIntervalSeconds: 300, remoteContent: .block, offlineDays: 30)
    /// Every `sync.trigger`, in order.
    private(set) var triggers: [SyncTriggerParams] = []
    /// Every method this fixture served, in order (the daemon's own log
    /// also has what went to handlers registered with `on`).
    private(set) var served: [String] = []

    // MARK: Messages (the list phase)

    /// The messages of every folder, in no particular order; the listings
    /// sort by date. `setMessages` and `addMessage` keep the folder's
    /// `unread` and `total` in step, as the daemon's counters are.
    private(set) var messages: [FolderKey: [MessageSummary]] = [:]
    /// What `message.body` returns per message; a message without an
    /// entry gets a plain-text body saying which message it is.
    var bodies: [MessageID: MessageBodyResult] = [:]
    /// What `message.get` returns per message; a message without an entry
    /// gets `Message(summary:)` of its listing.
    var details: [MessageID: Message] = [:]
    private(set) var listRequests: [MessageListParams] = []
    private(set) var threadListRequests: [ThreadListParams] = []
    private(set) var threadGetRequests: [ThreadGetParams] = []
    private(set) var flagRequests: [MessageFlagParams] = []
    private(set) var moveRequests: [MessageMoveParams] = []
    private(set) var deleteRequests: [MessageDeleteParams] = []

    let daemon: FakeDaemon
    private var failures: [String: RPCError] = [:]
    private var delays: [String: Duration] = [:]

    /// The methods answered from the fixture's state.
    static let servedMethods = [
        API.SystemInfo.name, API.AccountList.name, API.FolderList.name,
        API.SyncStatus.name, API.SyncTrigger.name, API.ConfigGet.name,
        API.MessageList.name, API.MessageGet.name, API.MessageBody.name,
        API.MessageFlag.name, API.MessageMove.name, API.MessageDelete.name,
        API.ThreadList.name, API.ThreadGet.name,
    ]

    init() throws {
        daemon = try FakeDaemon()
    }

    /// The socket path to dial.
    nonisolated var path: String { daemon.path }

    /// Registers the handlers and starts listening.
    func start() async throws {
        for method in MailFixture.servedMethods {
            await daemon.on(method) { [self] params in
                try await self.serve(method, params)
            }
        }
        try await daemon.start()
    }

    func stop() async {
        await daemon.stop()
    }

    /// Closes every client connection (the daemon went away).
    func closeAll() async {
        await daemon.closeAll()
    }

    // MARK: Scripting

    func setAccounts(_ list: [Account]) {
        accounts = list
    }

    func setFolders(_ list: [Folder], for acc: AccountID) {
        folders[acc] = list
    }

    func setFolders(_ all: [AccountID: [Folder]]) {
        folders = all
    }

    func setSyncStates(_ list: [SyncState]) {
        syncStates = list
    }

    /// Replaces the messages of a folder; each summary gets the folder's
    /// account and folder ids, and the folder's counters follow.
    func setMessages(_ list: [MessageSummary], in key: FolderKey) {
        messages[key] = list.map { s in
            var s = s
            s.accountId = key.account
            s.folderId = key.folder
            return s
        }
        refreshCounts(key.account)
    }

    /// Adds one message to the folder its summary names.
    func addMessage(_ s: MessageSummary) {
        let key = FolderKey(account: s.accountId, folder: s.folderId)
        messages[key, default: []].append(s)
        refreshCounts(key.account)
    }

    /// The stored summary of a message, wherever it is.
    func message(_ id: MessageID) -> MessageSummary? {
        locate(id)?.summary
    }

    /// The messages of a folder, in store order.
    func messages(in key: FolderKey) -> [MessageSummary] {
        messages[key] ?? []
    }

    /// Makes `method` fail with `error` until `succeed(method)`.
    func fail(_ method: String, with error: RPCError) {
        failures[method] = error
    }

    func succeed(_ method: String) {
        failures[method] = nil
    }

    /// Makes `method` answer after `delay`.
    func delay(_ method: String, _ delay: Duration) {
        delays[method] = delay
    }

    /// Every method the daemon served, in order (fixture and custom
    /// handlers alike).
    func calls() async -> [String] {
        await daemon.calls
    }

    func callCount(_ method: String) async -> Int {
        await daemon.calls.filter { $0 == method }.count
    }

    /// Registers a handler for a method the fixture does not script; a
    /// second registration replaces the first (`FakeDaemon.on`).
    func on(_ method: String, _ handler: @escaping FakeDaemon.MethodHandler) async {
        await daemon.on(method, handler)
    }

    // MARK: Notifications

    /// Pushes a notification to every connected client.
    func push(_ n: DaemonNotification) async throws {
        let method: String
        let params: Data
        switch n {
        case .newMessage(let m):
            method = API.Notify.newMessage
            params = try encode(m)
        case .syncState(let s):
            method = API.Notify.syncState
            params = try encode(SyncStateNotification(state: s))
        case .authRequired(let a):
            method = API.Notify.authRequired
            params = try encode(a)
        case .accountsChanged:
            method = API.Notify.accountsChanged
            params = Data("{}".utf8)
        case .unknown(let m):
            method = m
            params = Data("{}".utf8)
        }
        await daemon.pushNotification(method: method, paramsJSON: String(decoding: params, as: UTF8.self))
    }

    // MARK: Serving

    private func serve(_ method: String, _ params: Data) async throws -> Data {
        served.append(method)
        if let delay = delays[method] {
            try await Task.sleep(for: delay)
        }
        if let error = failures[method] {
            throw error
        }
        return try respond(method, params)
    }

    private func respond(_ method: String, _ params: Data) throws -> Data {
        switch method {
        case API.SystemInfo.name:
            return try encode(info)
        case API.AccountList.name:
            return try encode(AccountListResult(accounts: accounts))
        case API.FolderList.name:
            let p = try decode(FolderListParams.self, params)
            guard accounts.contains(where: { $0.id == p.accountId }) else {
                throw RPCError(code: .accountNotFound, message: "unknown account \(p.accountId.rawValue)")
            }
            return try encode(FolderListResult(folders: folders[p.accountId] ?? []))
        case API.SyncStatus.name:
            let p = try decode(SyncStatusParams.self, params)
            let list = p.accountId.map { id in syncStates.filter { $0.accountId == id } } ?? syncStates
            return try encode(SyncStatusResult(accounts: list))
        case API.SyncTrigger.name:
            triggers.append(try decode(SyncTriggerParams.self, params))
            return try encode(EmptyResult())
        case API.ConfigGet.name:
            return try encode(ConfigGetResult(preferences: preferences))
        case API.MessageList.name:
            let p = try decode(MessageListParams.self, params)
            listRequests.append(p)
            return try encode(listMessages(p))
        case API.MessageGet.name:
            let p = try decode(MessageGetParams.self, params)
            try requireAccount(p.accountId)
            guard let found = locate(p.messageId), found.key.account == p.accountId else {
                throw RPCError(code: .messageNotFound, message: "unknown message \(p.messageId.rawValue)")
            }
            return try encode(MessageGetResult(message: details[p.messageId] ?? Message(summary: found.summary)))
        case API.MessageBody.name:
            let p = try decode(MessageBodyParams.self, params)
            try requireAccount(p.accountId)
            guard let found = locate(p.messageId), found.key.account == p.accountId else {
                throw RPCError(code: .messageNotFound, message: "unknown message \(p.messageId.rawValue)")
            }
            let body = bodies[p.messageId] ?? MessageBodyResult(
                messageId: p.messageId, bodyState: .fetched, hasHtml: false, text: "body of \(p.messageId.rawValue)",
                remoteContent: p.remoteContent == .allow ? .allow : .block, sanitizerVersion: "1"
            )
            return try encode(body)
        case API.MessageFlag.name:
            let p = try decode(MessageFlagParams.self, params)
            flagRequests.append(p)
            try flagMessages(p)
            return try encode(EmptyResult())
        case API.MessageMove.name:
            let p = try decode(MessageMoveParams.self, params)
            moveRequests.append(p)
            try moveMessages(p)
            return try encode(EmptyResult())
        case API.MessageDelete.name:
            let p = try decode(MessageDeleteParams.self, params)
            deleteRequests.append(p)
            try deleteMessages(p)
            return try encode(EmptyResult())
        case API.ThreadList.name:
            let p = try decode(ThreadListParams.self, params)
            threadListRequests.append(p)
            return try encode(listThreads(p))
        case API.ThreadGet.name:
            let p = try decode(ThreadGetParams.self, params)
            threadGetRequests.append(p)
            return try encode(getThread(p))
        default:
            throw RPCError(code: .methodNotFound, message: "unknown method \(method)")
        }
    }

    // MARK: Messages

    private struct Located {
        var key: FolderKey
        var index: Int
        var summary: MessageSummary
    }

    private func requireAccount(_ id: AccountID) throws {
        guard accounts.contains(where: { $0.id == id }) else {
            throw RPCError(code: .accountNotFound, message: "unknown account \(id.rawValue)")
        }
    }

    private func folder(_ key: FolderKey) -> Folder? {
        folders[key.account]?.first { $0.id == key.folder }
    }

    private func requireFolder(_ key: FolderKey) throws -> Folder {
        guard let f = folder(key) else {
            throw RPCError(code: .folderNotFound, message: "unknown folder \(key.folder.rawValue)")
        }
        return f
    }

    private func locate(_ id: MessageID) -> Located? {
        for (key, list) in messages {
            if let i = list.firstIndex(where: { $0.id == id }) {
                return Located(key: key, index: i, summary: list[i])
            }
        }
        return nil
    }

    /// Every message of an account, wherever it is.
    private func allMessages(of acc: AccountID) -> [MessageSummary] {
        messages.filter { $0.key.account == acc }.values.flatMap { $0 }
    }

    /// Sets the folder counters of `acc` from its messages (the daemon's
    /// `folder.list` counters reflect flag changes at once).
    private func refreshCounts(_ acc: AccountID) {
        guard var list = folders[acc] else { return }
        for i in list.indices {
            let key = FolderKey(account: acc, folder: list[i].id)
            let msgs = messages[key] ?? []
            list[i].total = msgs.count
            list[i].unread = msgs.filter { !hasFlag($0.flags, .seen) }.count
        }
        folders[acc] = list
    }

    /// The daemon's list order: `dateDesc` unless asked otherwise, ties by
    /// id so the order is stable across calls.
    private func sorted(_ list: [MessageSummary], _ sort: MalachiCore.SortOrder?) -> [MessageSummary] {
        list.sorted { a, b in
            if a.date != b.date {
                return sort == .dateAsc ? a.date < b.date : a.date > b.date
            }
            return a.id.rawValue < b.id.rawValue
        }
    }

    /// Cursor = offset as a decimal string, limit clamped to the daemon's
    /// bounds (docs/api.md §4.3).
    private func page<T>(_ list: [T], _ p: Page) throws -> (slice: [T], page: PageInfo) {
        var offset = 0
        if let cursor = p.cursor, !cursor.isEmpty {
            guard let n = Int(cursor), n >= 0 else {
                throw RPCError(code: .invalidArgument, message: "bad cursor")
            }
            offset = n
        }
        let limit = min(max(p.limit ?? API.Limits.defaultPageLimit, 1), API.Limits.maxPageLimit)
        let end = min(offset + limit, list.count)
        let slice = offset < list.count ? Array(list[offset..<end]) : []
        let next = end < list.count ? String(end) : nil
        return (slice, PageInfo(nextCursor: next, total: list.count))
    }

    private func listMessages(_ p: MessageListParams) throws -> MessageListResult {
        try requireAccount(p.accountId)
        let key = FolderKey(account: p.accountId, folder: p.folderId)
        _ = try requireFolder(key)
        var filter = p.filter ?? .all
        if p.filter == nil, p.unreadOnly == true {
            filter = .unread
        }
        let list = sorted((messages[key] ?? []).filter { matchesFilter($0, filter) }, p.sort)
        let (slice, info) = try page(list, p.page)
        return MessageListResult(messages: slice, page: info)
    }

    private func flagMessages(_ p: MessageFlagParams) throws {
        try requireAccount(p.accountId)
        let set = p.set ?? []
        let clear = p.clear ?? []
        guard !p.messageIds.isEmpty, p.messageIds.count <= API.Limits.maxMessageIDsPerCall, !(set.isEmpty && clear.isEmpty),
              !hasFlag(set, .deleted), !hasFlag(clear, .deleted), !set.contains(where: { hasFlag(clear, $0) }) else {
            throw RPCError(code: .invalidArgument, message: "bad flag change")
        }
        let found = try locateAll(p.messageIds, in: p.accountId)
        for l in found where folder(l.key)?.role == .outbox {
            throw RPCError(code: .invalidArgument, message: "outbox message \(l.summary.id.rawValue)")
        }
        for l in found {
            let (flags, _) = applyFlagChange(l.summary.flags, set: set, clear: clear)
            messages[l.key]?[l.index].flags = flags
        }
        refreshCounts(p.accountId)
    }

    private func locateAll(_ ids: [MessageID], in acc: AccountID) throws -> [Located] {
        var out: [Located] = []
        for id in ids {
            guard let l = locate(id), l.key.account == acc else {
                throw RPCError(code: .messageNotFound, message: "unknown message \(id.rawValue)")
            }
            out.append(l)
        }
        return out
    }

    private func moveMessages(_ p: MessageMoveParams) throws {
        try requireAccount(p.accountId)
        guard !p.messageIds.isEmpty, p.messageIds.count <= API.Limits.maxMessageIDsPerCall else {
            throw RPCError(code: .invalidArgument, message: "bad ids")
        }
        let target = try requireFolder(FolderKey(account: p.accountId, folder: p.targetFolderId))
        guard target.selectable else {
            throw RPCError(code: .invalidArgument, message: "target not selectable")
        }
        let found = try locateAll(p.messageIds, in: p.accountId)
        for l in found where folder(l.key)?.role == .outbox {
            throw RPCError(code: .invalidArgument, message: "outbox message \(l.summary.id.rawValue)")
        }
        relocate(found, to: target, account: p.accountId)
    }

    /// Moves messages into `target`, or out of the store when the target
    /// is not synchronised; nil removes them.
    private func relocate(_ found: [Located], to target: Folder?, account acc: AccountID) {
        let targetKey = target.map { FolderKey(account: acc, folder: $0.id) }
        for l in found {
            if l.key == targetKey {
                continue
            }
            messages[l.key]?.removeAll { $0.id == l.summary.id }
            if let target, let targetKey, target.synced {
                var s = l.summary
                s.folderId = target.id
                messages[targetKey, default: []].append(s)
            }
        }
        refreshCounts(acc)
    }

    private func deleteMessages(_ p: MessageDeleteParams) throws {
        try requireAccount(p.accountId)
        guard !p.messageIds.isEmpty, p.messageIds.count <= API.Limits.maxMessageIDsPerCall else {
            throw RPCError(code: .invalidArgument, message: "bad ids")
        }
        let found = try locateAll(p.messageIds, in: p.accountId)
        let trash = folders[p.accountId]?.first { $0.role == .trash }
        if p.permanent != true, trash == nil, found.contains(where: { folder($0.key)?.role != .outbox }) {
            throw RPCError(code: .folderNotFound, message: "no trash folder")
        }
        // Outbox messages are cancelled (removed), messages in Trash and
        // permanent deletes go too; the rest move to Trash.
        var remove: [Located] = []
        var move: [Located] = []
        for l in found {
            let role = folder(l.key)?.role
            if p.permanent == true || role == .outbox || role == .trash {
                remove.append(l)
            } else {
                move.append(l)
            }
        }
        relocate(remove, to: nil, account: p.accountId)
        relocate(move, to: trash, account: p.accountId)
    }

    // MARK: Threads

    /// The conversation key of a message: its thread id, or its own id for
    /// one an older daemon never linked.
    private func threadKey(_ s: MessageSummary) -> ThreadID {
        s.threadId ?? ThreadID("unlinked:" + s.id.rawValue)
    }

    /// Aggregates a conversation over `members` (oldest first), as
    /// docs/api.md §4.4 describes thread.list's summary.
    private func aggregate(_ tid: ThreadID, _ members: [MessageSummary], account acc: AccountID) -> ThreadSummary {
        let latest = members[members.count - 1]
        var flags: [Flag] = []
        var senders: [Address] = []
        var attachments = false
        var unread = 0
        for s in members.reversed() {
            flags = unionFlags(flags, s.flags)
            senders.append(contentsOf: s.from)
            attachments = attachments || s.hasAttachments
            if !hasFlag(s.flags, .seen) {
                unread += 1
            }
        }
        let folderIds = messages
            .filter { $0.key.account == acc && $0.value.contains { threadKey($0) == tid } }
            .map(\.key.folder)
            .sorted { $0.rawValue < $1.rawValue }
        return ThreadSummary(
            id: tid, accountId: acc, subject: stripMarkers(latest.subject), participants: frontParticipants([], senders),
            messageCount: members.count, unreadCount: unread, latestDate: latest.date, latest: latest,
            snippet: latest.snippet, flags: flags, hasAttachments: attachments, folderIds: folderIds
        )
    }

    /// Drops leading Re:/Fwd: markers, the way the daemon names a thread.
    private func stripMarkers(_ subject: String) -> String {
        var s = subject.trimmingCharacters(in: .whitespaces)
        while true {
            let lower = s.lowercased()
            guard let marker = ["re:", "fwd:", "fw:"].first(where: { lower.hasPrefix($0) }) else { break }
            let rest = s.dropFirst(marker.count).trimmingCharacters(in: .whitespaces)
            if rest.isEmpty {
                break
            }
            s = rest
        }
        return s
    }

    /// The conversations of a folder with their members, oldest first.
    private func threads(in key: FolderKey) -> [(summary: ThreadSummary, members: [MessageSummary])] {
        var groups: [ThreadID: [MessageSummary]] = [:]
        for s in messages[key] ?? [] {
            groups[threadKey(s), default: []].append(s)
        }
        return groups.map { tid, list in
            let members = sorted(list, .dateAsc)
            return (aggregate(tid, members, account: key.account), members)
        }
    }

    private func listThreads(_ p: ThreadListParams) throws -> ThreadListResult {
        try requireAccount(p.accountId)
        let key = FolderKey(account: p.accountId, folder: p.folderId)
        _ = try requireFolder(key)
        let filter = p.filter ?? .all
        var list = threads(in: key).map(\.summary).filter { t in
            switch filter {
            case .unread: return t.unreadCount > 0
            case .flagged: return hasFlag(t.flags, .flagged)
            default: return true
            }
        }
        list.sort { a, b in
            if a.latestDate != b.latestDate {
                return p.sort == .dateAsc ? a.latestDate < b.latestDate : a.latestDate > b.latestDate
            }
            return a.id.rawValue < b.id.rawValue
        }
        let (slice, info) = try page(list, p.page)
        return ThreadListResult(threads: slice, page: info)
    }

    private func getThread(_ p: ThreadGetParams) throws -> ThreadGetResult {
        try requireAccount(p.accountId)
        var pool: [MessageSummary]
        if let fid = p.folderId {
            let key = FolderKey(account: p.accountId, folder: fid)
            _ = try requireFolder(key)
            pool = messages[key] ?? []
        } else {
            pool = allMessages(of: p.accountId)
        }
        pool = pool.filter { threadKey($0) == p.threadId }
        guard !pool.isEmpty else {
            throw RPCError(code: .threadNotFound, message: "unknown thread \(p.threadId.rawValue)")
        }
        let members = sorted(pool, .dateAsc)
        let summary = aggregate(p.threadId, members, account: p.accountId)
        return ThreadGetResult(thread: summary, messages: Array(members.suffix(API.Limits.maxThreadMessages)))
    }

    private func encode<T: Encodable>(_ value: T) throws -> Data {
        try JSONCoding.encoder().encode(value)
    }

    private func decode<T: Decodable>(_ type: T.Type, _ params: Data) throws -> T {
        try JSONCoding.decoder().decode(type, from: params.isEmpty ? Data("{}".utf8) : params)
    }
}
