// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The counterpart of ui/internal/window/favourites_test.go: the Favourites
// section and the pin state behind it.

/// A state with the given folders pinned (favourites_test.go `pinned`).
func pinned(_ keys: FolderKey...) -> FavouriteState {
    var f = FavouriteState()
    for k in keys {
        f.set(k, true)
    }
    return f
}

@MainActor
@Suite struct FavouriteStateTests {
    @Test func favouriteSectionFirstInTreeOrder() {
        let (accounts, folders) = testAccounts()
        let f = pinned(
            FolderKey(account: "acc2", folder: "in2"),
            FolderKey(account: "acc1", folder: "zeta"),
            FolderKey(account: "acc1", folder: "deep"),
            FolderKey(account: "acc1", folder: "inbox")
        )
        let entries = sortFolders(accounts, folders, CollapseState(), f)

        // Accounts in list order, then role, then path: not the order pinned.
        let section = ["#favourites", "*inbox@0", "*deep@0", "*zeta@0", "*in2@0"]
        let plain = ids(sortFolders(accounts, folders, CollapseState(), FavouriteState()))
        #expect(ids(entries) == section + plain)
        for e in entries[1..<5] {
            #expect(e.depth == 0 && !e.hasChildren && !e.nested && !e.collapsed && e.starred && e.favourite,
                    "section row \(e.folder?.id.rawValue ?? ""): want depth 0, no children, starred")
            #expect(e.account != nil, "section row carries no account")
        }
        #expect(entries[0].account == nil)
        #expect(entries[0].header)

        // The tree rows of pinned folders are starred, the others are not, and
        // none of them belongs to the section.
        var starred: [FolderID: Bool] = [:]
        for e in entries[5...] where !e.header {
            #expect(!e.favourite, "tree row \(e.folder?.id.rawValue ?? "") marked as section row")
            if let id = e.folder?.id {
                starred[id] = e.starred
            }
        }
        #expect(starred["inbox"] == true)
        #expect(starred["deep"] == true)
        #expect(starred["zeta"] == true)
        #expect(starred["in2"] == true)
        #expect(starred["trash"] == false)
        #expect(starred["alpha"] == false)
    }

    @Test func favouriteSectionGivesSingleAccountAHeader() throws {
        let (accounts, folders) = nestedAccount()
        let f = pinned(FolderKey(account: "a", folder: "bugs"))

        let want = ["#favourites", "*bugs@0", "#a", "in@0", "work@0", "bugs@1", "old@2", "zulu@0"]
        #expect(ids(sortFolders(accounts, folders, CollapseState(), f)) == want)

        // The section row shows the folder's own count even while the tree row
        // is collapsed and rolls its children up.
        var c = CollapseState()
        c.setFolder(FolderKey(account: "a", folder: "bugs"), true)
        let entries = sortFolders(accounts, folders, c, f)
        #expect(entries[1].badge == 4)
        #expect(!entries[1].collapsed)
        let tree = try #require(entryByID(Array(entries[2...]), "bugs"))
        #expect(tree.badge == 12)
        #expect(tree.collapsed)

        // With a heading the single account can be folded; the section stays.
        c.setAccount("a", true)
        #expect(ids(sortFolders(accounts, folders, c, f)) == ["#favourites", "*bugs@0", "#a"])
    }

    @Test func favouriteSectionSkipsWhatCannotShow() {
        var (accounts, folders) = testAccounts()
        folders["acc1", default: []].append(testFolder("out", path: "Outbox", role: .outbox))
        let f = pinned(
            FolderKey(account: "acc1", folder: "container"), // cannot be opened
            FolderKey(account: "acc3", folder: "in3"), // account disabled
            FolderKey(account: "acc1", folder: "gone"), // renamed on the server
            FolderKey(account: "acc1", folder: "out") // empty outbox is hidden
        )
        let plain = ids(sortFolders(accounts, folders, CollapseState(), FavouriteState()))
        #expect(ids(sortFolders(accounts, folders, CollapseState(), f)) == plain, "want no section")

        // An outbox with something in it is a folder like any other.
        let last = folders["acc1"]!.count - 1
        folders["acc1"]?[last].total = 1
        let got = ids(sortFolders(accounts, folders, CollapseState(), f))
        #expect(got.count >= 2)
        #expect(got.first == "#favourites")
        #expect(got.dropFirst().first == "*out@0", "want the outbox pinned")
        accounts.removeAll() // silence the unused-mutation warning
    }

    @Test func favouriteBadgesFollowUnread() {
        let (accounts, folders) = nestedAccount()
        var m = MailModel(accounts: accounts, folders: folders, favourites: pinned(FolderKey(account: "a", folder: "zulu")))
        m.rebuildEntries()
        m.adjustUnread(FolderKey(account: "a", folder: "zulu"), -6)

        var rows = 0
        for e in m.entries where !e.header && e.folder?.id == "zulu" {
            rows += 1
            #expect(e.badge == 10 && e.folder?.unread == 10, "zulu row (favourite=\(e.favourite)) badge \(e.badge)")
        }
        #expect(rows == 2, "zulu has the section's and the tree's row")
    }

    @Test func initialFolderPrefersTheTree() {
        let (accounts, folders) = testAccounts()
        var m = MailModel(accounts: accounts, folders: folders, favourites: pinned(FolderKey(account: "acc2", folder: "in2")))
        m.rebuildEntries()
        #expect(m.initialFolder() == FolderKey(account: "acc1", folder: "inbox"), "want the first account's Inbox")

        // Every account folded away: only the section is left to choose from.
        m.collapsed.setAccount("acc1", true)
        m.collapsed.setAccount("acc2", true)
        m.rebuildEntries()
        #expect(m.initialFolder() == FolderKey(account: "acc2", folder: "in2"), "want the pinned Inbox")
    }

    @Test func favouriteStateRoundTrip() {
        let s = ScratchSettings().settings
        let f = pinned(FolderKey(account: "acc_2", folder: "f_2"), FolderKey(account: "acc_1", folder: "f_1"))
        f.save(to: s, accounts: [testAccount("acc_1"), testAccount("acc_2")])

        // Stored sorted, so a no-op save is not a change for other windows.
        #expect(s.favouriteFolders == ["acc_1/f_1", "acc_2/f_2"])
        var got = FavouriteState.load(from: s)
        #expect(got.count == 2)
        #expect(got.has(FolderKey(account: "acc_1", folder: "f_1")))
        #expect(got.has(FolderKey(account: "acc_2", folder: "f_2")))

        // Unpinning removes the entry rather than storing a false; pinning twice
        // changes nothing.
        got.set(FolderKey(account: "acc_1", folder: "f_1"), false)
        got.set(FolderKey(account: "acc_2", folder: "f_2"), true)
        got.save(to: s, accounts: [testAccount("acc_1"), testAccount("acc_2")])
        #expect(s.favouriteFolders == ["acc_2/f_2"])
    }

    @Test func favouriteStatePrunesRemovedAccounts() {
        let s = ScratchSettings().settings
        let f = pinned(FolderKey(account: "acc_gone", folder: "f_1"), FolderKey(account: "acc_1", folder: "f_2"))

        f.save(to: s, accounts: [testAccount("acc_1")])
        #expect(s.favouriteFolders == ["acc_1/f_2"], "want only the surviving account's")
        // An empty account list means "not loaded yet" and must prune nothing.
        f.save(to: s, accounts: [])
        #expect(s.favouriteFolders.count == 2, "want both kept when no accounts are known")
    }

    @Test func loadFavouritesSkipsJunk() {
        let s = ScratchSettings().settings
        s.favouriteFolders = ["acc_1/f_1", "", "/", "acc", "acc_1/", "acc_1/f_1", "acc_2/f_2"]
        let f = FavouriteState.load(from: s)
        #expect(f.count == 2, "want the two well-formed entries once each")
        #expect(f.has(FolderKey(account: "acc_1", folder: "f_1")))
        #expect(f.has(FolderKey(account: "acc_2", folder: "f_2")))

        // A model without a state (the default) reads as nothing pinned.
        let none = FavouriteState()
        #expect(!none.has(FolderKey(account: "acc_1", folder: "f_1")), "empty state reports a pin")
    }
}
