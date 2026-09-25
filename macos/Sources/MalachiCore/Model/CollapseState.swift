// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// Which parts of the folder sidebar the user folded away
/// (ui/internal/window/collapse.go `collapseState`). This is pure
/// presentation: it changes nothing about the mail and no other client
/// cares, so it lives in the app's own settings rather than in the daemon.
/// A missing entry means expanded, which is what a fresh profile gets.
///
/// The `savingCollapse` guard against reacting to one's own write belongs
/// to the controller, as it does to the GTK window.
public struct CollapseState: Sendable, Equatable {
    public var folders: Set<FolderKey>
    public var accounts: Set<AccountID>

    /// An empty state: everything expanded (collapse.go `newCollapseState`).
    public init(folders: Set<FolderKey> = [], accounts: Set<AccountID> = []) {
        self.folders = folders
        self.accounts = accounts
    }

    /// Whether the folder hides its children.
    public func folderCollapsed(_ k: FolderKey) -> Bool {
        folders.contains(k)
    }

    /// Whether the account hides its folders.
    public func accountCollapsed(_ id: AccountID) -> Bool {
        accounts.contains(id)
    }

    /// Folds `k` away or unfolds it.
    public mutating func setFolder(_ k: FolderKey, _ collapsed: Bool) {
        if collapsed {
            folders.insert(k)
        } else {
            folders.remove(k)
        }
    }

    /// Folds an account's whole tree away or unfolds it.
    public mutating func setAccount(_ id: AccountID, _ collapsed: Bool) {
        if collapsed {
            accounts.insert(id)
        } else {
            accounts.remove(id)
        }
    }

    /// The state change of the window's `toggleFolder`: folds a folder's
    /// children away, or brings them back. Persisting and rebuilding the
    /// sidebar are the controller's part.
    public mutating func toggleFolder(_ k: FolderKey) {
        setFolder(k, !folderCollapsed(k))
    }

    /// The state change of the window's `toggleAccount`.
    public mutating func toggleAccount(_ id: AccountID) {
        setAccount(id, !accountCollapsed(id))
    }

    /// Reads the stored state (collapse.go `loadCollapse`). Entries that do
    /// not parse are dropped: the list is data from disk, possibly written
    /// by another version.
    @MainActor
    public static func load(from s: Settings) -> CollapseState {
        var c = CollapseState()
        for e in s.collapsedFolders {
            if let k = FolderKey(encoded: e) {
                c.folders.insert(k)
            }
        }
        for e in s.collapsedAccounts {
            let id = e.trimmingCharacters(in: .whitespacesAndNewlines)
            if !id.isEmpty {
                c.accounts.insert(AccountID(id))
            }
        }
        return c
    }

    /// Writes the state back (collapse.go `save`), dropping entries of
    /// accounts that are gone. Folders are kept even when their account
    /// currently has no folder list: folder.list may simply have failed or
    /// the account may be switched off, and losing the tree's shape over
    /// that would be worse than a stale entry. An empty account list means
    /// "not loaded yet", not "no accounts": nothing is pruned against it.
    /// The stored lists are sorted so a no-op save does not look like a
    /// change to other windows.
    @MainActor
    public func save(to s: Settings, accounts: [Account]) {
        let known = Set(accounts.map(\.id))
        let prune = !accounts.isEmpty
        let folderEntries = folders
            .filter { !prune || known.contains($0.account) }
            .map(\.encoded)
            .sorted()
        let accountEntries = self.accounts
            .filter { !prune || known.contains($0) }
            .map(\.rawValue)
            .sorted()
        s.collapsedFolders = folderEntries
        s.collapsedAccounts = accountEntries
    }
}
