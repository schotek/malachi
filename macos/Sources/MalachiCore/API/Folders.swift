// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Folders (docs/api.md §4.2; types.go "Folders").

import Foundation

/// api.Folder. `name` and `path` are display text from the server; never
/// interpret them as markup.
public struct Folder: Codable, Sendable, Equatable {
    public var id: FolderID
    public var accountId: AccountID
    public var parentId: FolderID?
    /// Leaf display name.
    public var name: String
    /// Full display path, "/"-separated.
    public var path: String
    public var role: FolderRole
    public var subscribed: Bool
    /// False for a \Noselect container.
    public var selectable: Bool
    /// False for a folder the daemon lists and accepts moves into but never
    /// downloads (Gmail's All Mail); a message moved there leaves the store.
    public var synced: Bool
    public var unread: Int
    public var total: Int

    public init(
        id: FolderID, accountId: AccountID, parentId: FolderID? = nil, name: String, path: String,
        role: FolderRole, subscribed: Bool, selectable: Bool, synced: Bool, unread: Int, total: Int
    ) {
        self.id = id
        self.accountId = accountId
        self.parentId = parentId
        self.name = name
        self.path = path
        self.role = role
        self.subscribed = subscribed
        self.selectable = selectable
        self.synced = synced
        self.unread = unread
        self.total = total
    }
}

/// api.FolderListParams.
public struct FolderListParams: Codable, Sendable, Equatable {
    public var accountId: AccountID
    /// Also return folders the user has not subscribed to.
    public var includeUnsubscribed: Bool?

    public init(accountId: AccountID, includeUnsubscribed: Bool? = nil) {
        self.accountId = accountId
        self.includeUnsubscribed = includeUnsubscribed
    }
}

/// api.FolderListResult: role folders first, then the rest by path.
public struct FolderListResult: Codable, Sendable, Equatable {
    @NullAsEmpty public var folders: [Folder]

    public init(folders: [Folder]) {
        self.folders = folders
    }
}

/// api.FolderSubscribeParams (`folder.subscribe` is notImplemented).
public struct FolderSubscribeParams: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var folderId: FolderID
    public var subscribed: Bool

    public init(accountId: AccountID, folderId: FolderID, subscribed: Bool) {
        self.accountId = accountId
        self.folderId = folderId
        self.subscribed = subscribed
    }
}
