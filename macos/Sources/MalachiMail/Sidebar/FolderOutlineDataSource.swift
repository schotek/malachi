// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

// The items of the folder outline, built from `MailModel.entries` (the flat
// list the GTK sidebar shows one row per entry). The nodes are cached by
// key across rebuilds so the outline keeps their identity: `reloadData()`
// then preserves the selection and the expansion of what did not change.

/// The Favourites heading (`FolderEntry.header && favourite`).
@MainActor
final class FavouritesNode: NSObject {
    var children: [FolderNode] = []
}

/// An account heading (`FolderEntry.header`), present from two enabled
/// accounts on or when a Favourites section sits above the tree.
@MainActor
final class AccountNode: NSObject {
    let id: AccountID
    var entry: FolderEntry
    var children: [FolderNode] = []

    init(id: AccountID, entry: FolderEntry) {
        self.id = id
        self.entry = entry
    }
}

/// A folder row: the one in its account's tree, or the one in the
/// Favourites section (`fav`), which a pinned folder has as well
/// (folders.go `rowKey`).
@MainActor
final class FolderNode: NSObject {
    let key: FolderKey
    let fav: Bool
    var entry: FolderEntry
    /// The account under the name (a pinned folder with several accounts).
    var subtitle = ""
    var children: [FolderNode] = []

    init(key: FolderKey, fav: Bool, entry: FolderEntry) {
        self.key = key
        self.fav = fav
        self.entry = entry
    }
}

/// Builds the outline's tree from the sidebar entries and serves it to the
/// outline view.
@MainActor
final class FolderOutlineDataSource: NSObject, NSOutlineViewDataSource {
    /// Addresses a folder row (folders.go `rowKey`).
    struct RowKey: Hashable {
        let key: FolderKey
        let fav: Bool
    }

    /// The top-level items in order.
    private(set) var roots: [NSObject] = []
    private var favouritesNode: FavouritesNode?
    private var accountNodes: [AccountID: AccountNode] = [:]
    private var folderNodes: [RowKey: FolderNode] = [:]

    /// Rebuilds the tree from `entries` (folders.go `rebuildFolderList`).
    /// `several` says whether a pinned folder's row names its account. Nodes
    /// that still exist keep their identity; the rest are dropped.
    func rebuild(entries: [FolderEntry], several: Bool) {
        var roots: [NSObject] = []
        var folders: [RowKey: FolderNode] = [:]
        var accounts: [AccountID: AccountNode] = [:]
        var favourites: FavouritesNode?
        // The section the rows go under (nil without headings) and the
        // ancestors of the current row by depth.
        var section: NSObject?
        var stack: [FolderNode] = []

        func append(_ node: FolderNode, to parent: NSObject?) {
            switch parent {
            case let f as FolderNode:
                f.children.append(node)
            case let a as AccountNode:
                a.children.append(node)
            case let s as FavouritesNode:
                s.children.append(node)
            default:
                roots.append(node)
            }
        }

        for e in entries {
            if e.header, e.favourite {
                let node = favouritesNode ?? FavouritesNode()
                node.children = []
                favourites = node
                roots.append(node)
                section = node
                stack = []
                continue
            }
            if e.header, let account = e.account {
                let node = accountNodes[account.id] ?? AccountNode(id: account.id, entry: e)
                node.entry = e
                node.children = []
                accounts[account.id] = node
                roots.append(node)
                section = node
                stack = []
                continue
            }
            guard let key = e.key, let account = e.account else { continue }
            let rowKey = RowKey(key: key, fav: e.favourite)
            let node = folderNodes[rowKey] ?? FolderNode(key: key, fav: e.favourite, entry: e)
            node.entry = e
            node.children = []
            node.subtitle = e.favourite && several ? accountLabel(account) : ""
            folders[rowKey] = node
            // The rows of the Favourites section are all at depth 0; in the
            // tree a row's parent is the nearest earlier row one level up. An
            // orphan indented deeper than the tree reaches (a parent cycle)
            // hangs off the last row above it.
            while stack.count > e.depth {
                stack.removeLast()
            }
            append(node, to: stack.last ?? section)
            stack.append(node)
        }

        self.roots = roots
        favouritesNode = favourites
        accountNodes = accounts
        folderNodes = folders
    }

    /// The row of a folder, in the section or in the tree.
    func node(_ key: FolderKey, fav: Bool) -> FolderNode? {
        folderNodes[RowKey(key: key, fav: fav)]
    }

    /// The children of an item (the roots for nil).
    func children(of item: Any?) -> [NSObject] {
        switch item {
        case nil:
            return roots
        case let f as FavouritesNode:
            return f.children
        case let a as AccountNode:
            return a.children
        case let f as FolderNode:
            return f.children
        default:
            return []
        }
    }

    // MARK: NSOutlineViewDataSource

    func outlineView(_ outlineView: NSOutlineView, numberOfChildrenOfItem item: Any?) -> Int {
        children(of: item).count
    }

    func outlineView(_ outlineView: NSOutlineView, child index: Int, ofItem item: Any?) -> Any {
        children(of: item)[index]
    }

    func outlineView(_ outlineView: NSOutlineView, isItemExpandable item: Any) -> Bool {
        switch item {
        case is FavouritesNode:
            return true
        case let a as AccountNode:
            return a.entry.hasChildren
        case let f as FolderNode:
            return f.entry.hasChildren
        default:
            return false
        }
    }
}
