// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// Which folders the user pinned to the Favourites section at the top of
/// the sidebar (ui/internal/window/favourites.go `favouriteState`). Like
/// the folds this is pure presentation, kept in the app's own settings and
/// encoded with the same account/folder keys. The section lists the pins in
/// tree order (`sortFolders`), so the set carries no order of its own.
public struct FavouriteState: Sendable, Equatable {
    public var folders: Set<FolderKey>

    /// An empty set (favourites.go `newFavouriteState`).
    public init(_ folders: Set<FolderKey> = []) {
        self.folders = folders
    }

    /// Whether `k` is pinned.
    public func has(_ k: FolderKey) -> Bool {
        folders.contains(k)
    }

    /// Pins `k` or unpins it.
    public mutating func set(_ k: FolderKey, _ on: Bool) {
        if on {
            folders.insert(k)
        } else {
            folders.remove(k)
        }
    }

    /// The state change of the window's `toggleFavourite`.
    public mutating func toggle(_ k: FolderKey) {
        set(k, !has(k))
    }

    public var count: Int { folders.count }
    public var isEmpty: Bool { folders.isEmpty }

    /// Reads the stored set (favourites.go `loadFavourites`). Entries that
    /// do not parse are dropped.
    @MainActor
    public static func load(from s: Settings) -> FavouriteState {
        var f = FavouriteState()
        for e in s.favouriteFolders {
            if let k = FolderKey(encoded: e) {
                f.folders.insert(k)
            }
        }
        return f
    }

    /// Writes the set back (favourites.go `save`), sorted so a no-op save
    /// does not look like a change to other windows, and without the
    /// entries of accounts that are gone. An empty account list means "not
    /// loaded yet" and prunes nothing (as `CollapseState.save`). A folder
    /// that no longer resolves is kept: folder.list may have failed, the
    /// account may be switched off, and the pin is worth more than the stale
    /// entry costs.
    @MainActor
    public func save(to s: Settings, accounts: [Account]) {
        let known = Set(accounts.map(\.id))
        let prune = !accounts.isEmpty
        s.favouriteFolders = folders
            .filter { !prune || known.contains($0.account) }
            .map(\.encoded)
            .sorted()
    }
}
