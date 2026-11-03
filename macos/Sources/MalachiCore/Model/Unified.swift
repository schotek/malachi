// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// The "All Inboxes" item of the sidebar (ui/internal/unified, ported 1:1;
/// docs/plans/unified-inbox.md, decisions U1–U4; docs/api.md §4.3 and §4.4,
/// "Unified listing"): the inboxes of every enabled mail account in one
/// message list. It says when the item is shown (`shown`), which folders it
/// covers (`inboxes`, `covers`, `coversAny`) and its badge (`unread`,
/// `total`), builds the listing's parameters (`messageParams`,
/// `threadParams`), names its sidebar key (`FolderKey.unified`) and splits
/// an action on rows of several accounts into one call per account
/// (`groupByAccount`). Pure logic, no widgets.
///
/// The scope is the daemon's (core/unified.go): folders of role inbox that
/// are synced and not virtual, of the enabled accounts of kind imap or
/// graph, never jira, caldav or eway. The client mirrors it only to know
/// which notifications and counts belong to the list; the rows come from
/// the daemon.
public enum Unified {
    /// unified.Role: the folder role the item lists (`api.UnifiedRoles`);
    /// the API takes it as `unified`.
    public static let role: FolderRole = .inbox

    /// unified.MinAccounts: how many mail accounts the item needs to be
    /// shown; with one, its inbox is the same list.
    public static let minAccounts = 2

    /// unified.KeyFolder: the folder of the item's sidebar key; its account
    /// is "". A real folder id never starts with "@" and no real key has an
    /// empty account, so the key cannot collide.
    public static let keyFolder: FolderID = "@unified:inbox"

    /// unified.IsKey: whether the pair is the item's key.
    public static func isKey(_ account: AccountID, _ folder: FolderID) -> Bool {
        account.rawValue.isEmpty && folder == keyFolder
    }

    /// unified.IsMailAccount: an account the item covers when enabled, of
    /// kind imap or graph (an absent kind is imap).
    public static func isMailAccount(_ a: Account) -> Bool {
        let k = a.config.protocolKind
        return k == .imap || k == .graph
    }

    /// unified.Accounts: the ids of the accounts the item covers, the
    /// enabled mail accounts, in the order given (account.list's, which is
    /// the sidebar's).
    public static func accounts(_ accounts: [Account]) -> [AccountID] {
        accounts.filter { $0.enabled && isMailAccount($0) }.map(\.id)
    }

    /// unified.Shown: whether the sidebar has the item: at least
    /// `minAccounts` accounts it covers and the preference `unified-inbox`
    /// (`setting`) on.
    public static func shown(_ accounts: [Account], setting: Bool) -> Bool {
        setting && Self.accounts(accounts).count >= minAccounts
    }

    /// unified.IsInbox: whether `f` is a folder the item lists: role inbox,
    /// synced and not a view of an issue tracker.
    public static func isInbox(_ f: Folder) -> Bool {
        f.role == role && f.synced && (f.virtual?.rawValue ?? "").isEmpty
    }

    /// unified.Inboxes: the folders the item lists: those `isInbox` of each
    /// account `accounts` covers, in the accounts' order and then the order
    /// of folders (folder.list's of each account).
    public static func inboxes(_ accounts: [Account], _ folders: [AccountID: [Folder]]) -> [Folder] {
        var out: [Folder] = []
        for id in Self.accounts(accounts) {
            out.append(contentsOf: (folders[id] ?? []).filter(isInbox))
        }
        return out
    }

    /// unified.Unread: the item's badge, the unread count of the inboxes.
    public static func unread(_ inboxes: [Folder]) -> Int {
        inboxes.reduce(0) { $0 + $1.unread }
    }

    /// unified.Total: the number of messages of the inboxes, for the list's
    /// subtitle ("%d unread of %d", the existing msgid).
    public static func total(_ inboxes: [Folder]) -> Int {
        inboxes.reduce(0) { $0 + $1.total }
    }

    /// unified.Covers: whether a change of `folderID` of `accountID`
    /// (notify.newMessage, a folder's sync finishing, an undone move into
    /// it) belongs to the list shown: the list is the item (`listIsUnified`)
    /// and the folder is one of `inboxes`.
    public static func covers(listIsUnified: Bool, _ accountID: AccountID, _ folderID: FolderID, _ inboxes: [Folder]) -> Bool {
        guard listIsUnified else { return false }
        return inboxes.contains { $0.accountId == accountID && $0.id == folderID }
    }

    /// unified.CoversAny: `covers` for a notification naming several folders
    /// of one account (notify.messagesChanged): any of them is covered; no
    /// folder at all means any folder of the account, covered when the
    /// account has an inbox among `inboxes`.
    public static func coversAny(listIsUnified: Bool, _ accountID: AccountID, _ folderIDs: [FolderID], _ inboxes: [Folder]) -> Bool {
        guard listIsUnified else { return false }
        if folderIDs.isEmpty {
            return inboxes.contains { $0.accountId == accountID }
        }
        return folderIDs.contains { covers(listIsUnified: true, accountID, $0, inboxes) }
    }

    /// unified.MessageParams: message.list of the item: newest first, the
    /// default page size, the filter of the list and the cursor of the next
    /// page (nil for the first). The cursor must be one a unified listing
    /// returned. `accountId` and `folderId` are empty, which on the wire is
    /// what Go's zero value is.
    public static func messageParams(filter: MessageFilter, cursor: String? = nil) -> MessageListParams {
        MessageListParams(
            accountId: "", folderId: "", page: Page(cursor: cursor, limit: API.Limits.defaultPageLimit),
            sort: .dateDesc, filter: filter, unified: role
        )
    }

    /// unified.ThreadParams: thread.list of the item, as `messageParams`. A
    /// row's conversation is then read with thread.get of the row's account
    /// (`ThreadSummary.accountId`) and folder (`latest.folderId`).
    public static func threadParams(filter: MessageFilter, cursor: String? = nil) -> ThreadListParams {
        ThreadListParams(
            accountId: "", folderId: "", page: Page(cursor: cursor, limit: API.Limits.defaultPageLimit),
            sort: .dateDesc, filter: filter, unified: role
        )
    }

    /// unified.Group: the items of one account, in their order.
    public struct Group<T> {
        public var account: AccountID
        public var items: [T]

        public init(account: AccountID, items: [T]) {
            self.account = account
            self.items = items
        }
    }

    /// unified.GroupByAccount: splits `items` (selected rows, message ids
    /// with their account) by account, the accounts in the order they first
    /// appear and the items of each in their order: an action on a
    /// selection of the item runs one call per group (every message method
    /// takes one account).
    public static func groupByAccount<T>(_ items: [T], account: (T) -> AccountID) -> [Group<T>] {
        var out: [Group<T>] = []
        var index: [AccountID: Int] = [:]
        for it in items {
            let a = account(it)
            let i: Int
            if let known = index[a] {
                i = known
            } else {
                i = out.count
                index[a] = i
                out.append(Group(account: a, items: []))
            }
            out[i].items.append(it)
        }
        return out
    }

    /// unified.Mixed: whether `items` belong to more than one account (a
    /// selection of the item across accounts, decision U2).
    public static func mixed<T>(_ items: [T], account: (T) -> AccountID) -> Bool {
        guard let first = items.first else { return false }
        let a = account(first)
        return items.dropFirst().contains { account($0) != a }
    }

    // MARK: Conversations across accounts

    /// The separator of `scopedThread`; no account or thread id the daemon
    /// mints contains a control character.
    static let threadSeparator: Character = "\u{1F}"

    /// Thread ids are unique only within an account (a Graph account's is
    /// the raw Exchange conversationId, which two mailboxes of one tenant
    /// share), but the grouped list keys its conversations, members, folds
    /// and selection by thread id alone. In the All Inboxes list every
    /// thread id the client takes in (thread.list, thread.get, a notified
    /// message) is therefore made unique by prefixing its account, and
    /// `rawThread` gives the daemon's id back for the one call that sends
    /// it (thread.get). Swift-first: Go and C# mirror it.
    public static func scopedThread(_ t: ThreadID, account: AccountID) -> ThreadID {
        ThreadID(account.rawValue + String(threadSeparator) + t.rawValue)
    }

    /// The daemon's thread id of a `scopedThread` one; any other id is
    /// returned as it is.
    public static func rawThread(_ t: ThreadID) -> ThreadID {
        guard let i = t.rawValue.firstIndex(of: threadSeparator) else { return t }
        return ThreadID(String(t.rawValue[t.rawValue.index(after: i)...]))
    }

    /// `s` with its thread id scoped to its account (`scopedThread`); a
    /// message without a thread id is left alone.
    public static func scoped(_ s: MessageSummary, account: AccountID) -> MessageSummary {
        guard let t = s.threadId, !t.rawValue.isEmpty else { return s }
        var s = s
        s.threadId = scopedThread(t, account: account)
        return s
    }

    /// `t` with its id and its latest message's thread id scoped to its
    /// account.
    public static func scopedConversation(_ t: ThreadSummary) -> ThreadSummary {
        var t = t
        t.id = scopedThread(t.id, account: t.accountId)
        t.latest = scoped(t.latest, account: t.accountId)
        return t
    }

    // MARK: Texts (unified/text.go)

    /// unified.Title: the item's name in the sidebar and the title of its
    /// list.
    public static var title: String {
        // TRANSLATORS: The sidebar item and the list of the inboxes of every mail account together.
        L10n.T("All Inboxes")
    }

    /// unified.PreferenceTitle: the title of the preference row (key
    /// `unified-inbox`).
    public static var preferenceTitle: String {
        // TRANSLATORS: A switch in Preferences; "All Inboxes" is the sidebar item's name.
        L10n.T("Show All Inboxes")
    }

    /// unified.PreferenceSubtitle: the subtitle of the preference row.
    public static var preferenceSubtitle: String {
        // TRANSLATORS: The subtitle of the "Show All Inboxes" switch in Preferences.
        L10n.T("In the sidebar when you have more than one mail account")
    }
}

extension FolderKey {
    /// unified.Key: the sidebar key of the All Inboxes item. It is never
    /// stored in `favourite-folders` or `collapsed-folders` (its `encoded`
    /// form is rejected by `init?(encoded:)`: the account is empty) and
    /// cannot be pinned.
    public static let unified = FolderKey(account: "", folder: Unified.keyFolder)

    /// unified.FolderKey.IsUnified.
    public var isUnified: Bool { Unified.isKey(account, folder) }
}
