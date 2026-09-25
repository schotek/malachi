// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// One row of the folder sidebar (ui/internal/window/model.go
/// `folderEntry`): an account header (`header` set, `folder` nil), the
/// Favourites heading (`header` and `favourite` set, `account` nil too) or a
/// folder at the given tree depth.
public struct FolderEntry: Sendable, Equatable {
    public var header: Bool
    /// nil only on the Favourites heading.
    public var account: Account?
    /// nil on header rows.
    public var folder: Folder?
    public var depth: Int

    /// Set on the rows of the Favourites section at the top of the sidebar:
    /// its heading and one row per pinned folder, each at depth 0 without
    /// children. The same folder has a second row in its account's tree, so
    /// a folder key alone does not name a row.
    public var favourite: Bool
    /// The folder is pinned: its star is filled, on the row in the section
    /// and on the one in the tree alike.
    public var starred: Bool

    /// The row can be folded; `collapsed` says it currently is, and its
    /// descendants are left out of the list entirely.
    public var hasChildren: Bool
    public var collapsed: Bool
    /// Set on every row of an account whose tree has at least one parent, so
    /// childless rows can reserve the width of the arrow and their titles
    /// line up. An account with a flat folder list looks as before.
    public var nested: Bool
    /// The number the row shows: the folder's own unread count, or that plus
    /// every hidden descendant's while it is collapsed.
    public var badge: Int

    public init(
        header: Bool = false, account: Account? = nil, folder: Folder? = nil, depth: Int = 0,
        favourite: Bool = false, starred: Bool = false, hasChildren: Bool = false, collapsed: Bool = false,
        nested: Bool = false, badge: Int = 0
    ) {
        self.header = header
        self.account = account
        self.folder = folder
        self.depth = depth
        self.favourite = favourite
        self.starred = starred
        self.hasChildren = hasChildren
        self.collapsed = collapsed
        self.nested = nested
        self.badge = badge
    }

    /// The folder this row stands for; nil on a header row.
    public var key: FolderKey? {
        guard let account, let folder else { return nil }
        return FolderKey(account: account.id, folder: folder.id)
    }
}
