// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// What a message row displays (ui/internal/widget/message_row.go
/// `Message`), a projection of a list summary.
public struct RowMessage: Sendable, Equatable {
    public var from: [Address]
    public var subject: String
    public var snippet: String
    public var date: Date
    public var unread: Bool
    public var flagged: Bool
    public var hasAttachments: Bool

    public init(
        from: [Address], subject: String, snippet: String, date: Date, unread: Bool, flagged: Bool,
        hasAttachments: Bool
    ) {
        self.from = from
        self.subject = subject
        self.snippet = snippet
        self.date = date
        self.unread = unread
        self.flagged = flagged
        self.hasAttachments = hasAttachments
    }
}

/// Projects a list summary onto what a row displays (model.go
/// `summaryMessage`).
public func summaryMessage(_ s: MessageSummary) -> RowMessage {
    RowMessage(
        from: s.from, subject: s.subject, snippet: s.snippet, date: s.date,
        unread: !hasFlag(s.flags, .seen), flagged: hasFlag(s.flags, .flagged), hasAttachments: s.hasAttachments
    )
}

/// The main window's view model (ui/internal/window/model.go `mailModel`):
/// plain Swift, no AppKit. It mirrors what the backend returned (accounts,
/// folders, the current message page) so the views can be rebuilt from it
/// and optimistic updates have one place to live. Nothing here decides
/// anything about mail; it only caches API data.
///
/// The generation counters guard asynchronous replies: a caller bumps the
/// counter before starting a request and ignores the reply when the counter
/// moved on (folder changed, connection dropped, …).
public struct MailModel: Sendable {
    public var accounts: [Account]
    public var folders: [AccountID: [Folder]]
    public var folderErr: [AccountID: any Error]
    public var entries: [FolderEntry]
    public var selected: FolderKey?
    /// Which of the selected folder's rows carries the highlight: the one in
    /// the Favourites section or the one in the tree, whichever the user
    /// clicked last. Display only; `selected` is the folder.
    public var selectedFav: Bool
    /// Which sidebar nodes are folded away (`CollapseState`); which folders
    /// are pinned to the top (`FavouriteState`).
    public var collapsed: CollapseState
    public var favourites: FavouriteState

    /// The folder messages belong to (or are being loaded for); it lags
    /// `selected` between selectFolder and loadMessages.
    public var listFolder: FolderKey?
    /// Narrows the listing (message.list "filter"). One setting for the
    /// whole window, kept across folder switches but not across restarts.
    public var listFilter: MessageFilter
    public var messages: [MessageSummary]
    public var index: [MessageID: Int]
    public var nextCursor: String?
    /// -1 when the backend could not compute it.
    public var total: Int
    public var listErr: (any Error)?

    /// Grouped mode (MailModel+Threads.swift): the conversations of
    /// `listFolder` and what is known of their members; `rows` mirrors the
    /// list view. `messages` stays empty then, and the other way round.
    public var grouped: Bool
    public var threads: [ThreadSummary]
    public var tindex: [ThreadID: Int]
    public var members: [ThreadID: ThreadMembers]
    public var memberOf: [MessageID: ThreadID]
    public var expanded: Set<ThreadID>
    public var rows: [ListRow]
    public var rowIdx: [ListKey: Int]

    public var listGen: UInt64
    public var bodyGen: UInt64
    public var foldersGen: UInt64
    public var loading: Bool
    public var loadingMore: Bool

    public init(
        accounts: [Account] = [], folders: [AccountID: [Folder]] = [:], grouped: Bool = false,
        listFilter: MessageFilter = .all, collapsed: CollapseState = CollapseState(),
        favourites: FavouriteState = FavouriteState()
    ) {
        self.accounts = accounts
        self.folders = folders
        self.folderErr = [:]
        self.entries = []
        self.selected = nil
        self.selectedFav = false
        self.collapsed = collapsed
        self.favourites = favourites
        self.listFolder = nil
        self.listFilter = listFilter
        self.messages = []
        self.index = [:]
        self.nextCursor = nil
        self.total = 0
        self.listErr = nil
        self.grouped = grouped
        self.threads = []
        self.tindex = [:]
        self.members = [:]
        self.memberOf = [:]
        self.expanded = []
        self.rows = []
        self.rowIdx = [:]
        self.listGen = 0
        self.bodyGen = 0
        self.foldersGen = 0
        self.loading = false
        self.loadingMore = false
    }

    // MARK: Lists

    /// Empties the list (folder switch, disconnect) without touching the
    /// generation counters.
    public mutating func clearMessages() {
        setMessages([], page: PageInfo(total: -1))
        clearThreads()
    }

    /// Invalidates every in-flight reply and clears the loading flags
    /// (connection lost).
    public mutating func bumpAll() {
        bumpList()
        bumpBody()
        bumpFolders()
        loading = false
        loadingMore = false
    }

    /// Replaces the list with a first page. Duplicate ids (which a
    /// well-behaved backend never sends) keep their first occurrence.
    public mutating func setMessages(_ list: [MessageSummary], page: PageInfo) {
        messages = []
        index = [:]
        index.reserveCapacity(list.count)
        for s in list {
            if index[s.id] != nil {
                continue
            }
            index[s.id] = messages.count
            messages.append(s)
        }
        nextCursor = page.nextCursor
        total = page.total
        listErr = nil
    }

    /// Adds a further page, skipping messages already listed (a message
    /// inserted from notify.newMessage may show up again in a page). Returns
    /// how many rows were added.
    @discardableResult
    public mutating func appendMessages(_ list: [MessageSummary], page: PageInfo) -> Int {
        var added = 0
        for s in list {
            if index[s.id] != nil {
                continue
            }
            index[s.id] = messages.count
            messages.append(s)
            added += 1
        }
        nextCursor = page.nextCursor
        total = page.total
        return added
    }

    /// Places `s` at `idx` (clamped to the list bounds) and reports whether
    /// it was inserted; an id already in the list is left alone.
    @discardableResult
    public mutating func insertMessage(at idx: Int, _ s: MessageSummary) -> Bool {
        if index[s.id] != nil {
            return false
        }
        let at = min(max(idx, 0), messages.count)
        messages.insert(s, at: at)
        reindex(from: at)
        if total >= 0 {
            total += 1
        }
        return true
    }

    /// Drops `id` from the list and returns the removed summary and the
    /// index it had (the natural place to select a neighbour afterwards).
    @discardableResult
    public mutating func removeMessage(_ id: MessageID) -> (summary: MessageSummary, index: Int)? {
        guard let idx = index[id] else {
            return nil
        }
        let s = messages.remove(at: idx)
        index[id] = nil
        reindex(from: idx)
        if total > 0 {
            total -= 1
        }
        return (s, idx)
    }

    /// Refreshes `index` for the rows from `idx` onwards.
    private mutating func reindex(from idx: Int) {
        var i = idx
        while i < messages.count {
            index[messages[i].id] = i
            i += 1
        }
    }

    /// The summary at list position `idx`.
    public func messageAt(_ idx: Int) -> MessageSummary? {
        guard idx >= 0, idx < messages.count else {
            return nil
        }
        return messages[idx]
    }

    /// The summary with the given id and its list position. In grouped mode
    /// a member of a listed conversation is found too; the index is then
    /// the row it has (-1 while the conversation is folded), never a
    /// position in `messages`.
    public func message(_ id: MessageID) -> (summary: MessageSummary, index: Int)? {
        if let idx = index[id] {
            return (messages[idx], idx)
        }
        if let tid = memberOf[id], let mem = members[tid] {
            for s in mem.list where s.id == id {
                return (s, rowIndexOf(ListKey(thread: tid, message: id)))
            }
        }
        return nil
    }

    /// Applies a flag change to the cached summary (optimistic update or
    /// server notification) and reports whether anything changed. A flag in
    /// both `set` and `clear` ends up set.
    @discardableResult
    public mutating func updateFlags(_ id: MessageID, set: [Flag] = [], clear: [Flag] = []) -> Bool {
        guard let idx = index[id] else {
            return false
        }
        let (flags, changed) = applyFlagChange(messages[idx].flags, set: set, clear: clear)
        if changed {
            messages[idx].flags = flags
        }
        return changed
    }

    /// Replaces the outbox state of a listed message (flat mode; the outbox
    /// folder is never grouped).
    public mutating func setOutbox(_ id: MessageID, _ o: OutboxInfo?) {
        if let idx = index[id] {
            messages[idx].outbox = o
        }
    }

    // MARK: Folders and accounts

    /// Recomputes the sidebar rows from accounts and folders.
    public mutating func rebuildEntries() {
        entries = sortFolders(accounts, folders, collapsed, favourites)
    }

    /// Recomputes the number every visible row shows, after an unread count
    /// moved. A collapsed row sums its hidden descendants, so one changed
    /// folder can move an ancestor's badge and a single-row update is not
    /// enough.
    public mutating func refreshBadges() {
        for i in entries.indices {
            let e = entries[i]
            guard !e.header, let account = e.account, let folder = e.folder else {
                continue
            }
            entries[i].badge = badgeFor(folders[account.id] ?? [], folder, collapsed: e.collapsed)
        }
    }

    /// Looks a folder up by key.
    public func folder(_ k: FolderKey) -> Folder? {
        folders[k.account]?.first { $0.id == k.folder }
    }

    /// Whether the sidebar still lists `k`, whether or not a fold currently
    /// hides its row. It is the test for keeping a selection: folding a
    /// parent must not move it, but an account switched off, a folder gone
    /// from the server or an outbox that has just drained must.
    public func folderListed(_ k: FolderKey?) -> Bool {
        guard let k, let a = account(k.account), a.enabled else {
            return false
        }
        return MalachiCore.visibleFolders(folders[k.account] ?? []).contains { $0.id == k.folder }
    }

    /// The account's folder with the given special-use role (the first one
    /// when a server reports several).
    public func folderByRole(_ acc: AccountID, _ role: FolderRole) -> Folder? {
        folders[acc]?.first { $0.role == role }
    }

    /// The special-use role of folder `k`, `.none` when the folder is
    /// unknown or plain.
    public func folderRole(_ k: FolderKey) -> FolderRole {
        folder(k)?.role ?? .none
    }

    /// Whether `s` is a queued outgoing message: the backend marks it with
    /// `outbox`, and its folder is the account's outbox.
    public func inOutbox(_ s: MessageSummary) -> Bool {
        s.outbox != nil || folderRole(FolderKey(account: s.accountId, folder: s.folderId)) == .outbox
    }

    /// Whether `s` can go to its account's role folder: the folder exists
    /// and `s` is not in it (actions.go `canMoveToRole`).
    public func canMoveToRole(_ s: MessageSummary, _ role: FolderRole) -> Bool {
        guard let f = folderByRole(s.accountId, role) else {
            return false
        }
        return f.id != s.folderId
    }

    /// Changes the cached unread count of a folder by `delta`, never below
    /// zero. Both the folders map and the sidebar entries are updated so a
    /// later rebuild does not undo the change.
    public mutating func adjustUnread(_ k: FolderKey, _ delta: Int) {
        guard var list = folders[k.account], let i = list.firstIndex(where: { $0.id == k.folder }) else {
            return
        }
        list[i].unread = max(list[i].unread + delta, 0)
        folders[k.account] = list
        for j in entries.indices {
            let e = entries[j]
            if !e.header, e.account?.id == k.account, e.folder?.id == k.folder {
                entries[j].folder?.unread = list[i].unread
            }
        }
        // The folder may be hidden under a collapsed ancestor whose badge
        // counts it, so every badge is recomputed, not just this row's.
        refreshBadges()
    }

    /// Looks an account up by id.
    public func account(_ id: AccountID) -> Account? {
        accounts.first { $0.id == id }
    }

    /// The accounts the sidebar shows, in list order.
    public var enabledAccounts: [Account] {
        MalachiCore.enabledAccounts(accounts)
    }

    /// The folder to select when nothing is selected yet: the first Inbox,
    /// otherwise the first selectable folder. The tree is searched before
    /// the Favourites section, which repeats folders of the tree: a pinned
    /// Inbox of a later account must not win over the first account's. The
    /// section only decides when the tree gave nothing (every account
    /// folded).
    public func initialFolder() -> FolderKey? {
        if let k = firstFolder(entries, favourite: false) {
            return k
        }
        return firstFolder(entries, favourite: true)
    }

    // MARK: Generations

    /// Invalidates in-flight message.list replies.
    @discardableResult
    public mutating func bumpList() -> UInt64 {
        listGen += 1
        return listGen
    }

    /// Invalidates in-flight message.get / message.body replies.
    @discardableResult
    public mutating func bumpBody() -> UInt64 {
        bodyGen += 1
        return bodyGen
    }

    /// Invalidates in-flight account.list / folder.list replies.
    @discardableResult
    public mutating func bumpFolders() -> UInt64 {
        foldersGen += 1
        return foldersGen
    }
}
