// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The counterpart of ui/internal/window/collapse_test.go: folding parts of
// the sidebar away, which rows survive, what the badges then say, and what
// is written to and read back from the settings.

/// One account whose tree is INBOX; Work -> Work/Bugs -> Work/Bugs/Old;
/// Zulu, with unread counts that make a roll-up visible (collapse_test.go
/// `nestedFolders`).
func nestedFolders() -> [Folder] {
    [
        testFolder("in", path: "INBOX", role: .inbox, unread: 1),
        testFolder("work", path: "Work", unread: 2),
        testFolder("bugs", path: "Work/Bugs", parent: "work", unread: 4),
        testFolder("old", path: "Work/Bugs/Old", parent: "bugs", unread: 8),
        testFolder("zulu", path: "Zulu", unread: 16),
    ]
}

func nestedAccount() -> ([Account], [AccountID: [Folder]]) {
    ([testAccount("a")], ["a": nestedFolders()])
}

/// Finds a row by folder id (collapse_test.go `entryByID`).
func entryByID(_ entries: [FolderEntry], _ id: FolderID) -> FolderEntry? {
    entries.first { !$0.header && $0.folder?.id == id }
}

/// A throwaway defaults domain for the persistence cases, wiped when the
/// test is done.
final class ScratchSettings {
    let suite: String
    let defaults: UserDefaults
    let settings: Settings

    @MainActor init() {
        suite = "io.github.schotek.Malachi.model-test-\(UUID().uuidString)"
        defaults = UserDefaults(suiteName: suite)!
        settings = Settings(defaults: defaults)
    }

    deinit {
        defaults.removePersistentDomain(forName: suite)
    }
}

@MainActor
@Suite struct CollapseStateTests {
    @Test func folderTreeExpandedByDefault() throws {
        let (accounts, folders) = nestedAccount()
        let entries = sortFolders(accounts, folders, CollapseState(), FavouriteState())

        #expect(ids(entries) == ["in@0", "work@0", "bugs@1", "old@2", "zulu@0"])
        let work = try #require(entryByID(entries, "work"))
        #expect(work.hasChildren)
        #expect(!work.collapsed)
        #expect(work.badge == 2, "expanded Work badge shows its own 2")
        // Every row of a nested account reserves the arrow column so the titles
        // line up, including the leaves.
        for e in entries {
            #expect(e.nested, "\(e.folder?.id.rawValue ?? ""): Nested = false in a nested account")
        }
        let zulu = try #require(entryByID(entries, "zulu"))
        #expect(!zulu.hasChildren, "Zulu has no children but claims to")
    }

    @Test func folderTreeCollapsedHidesWholeSubtree() throws {
        let (accounts, folders) = nestedAccount()
        var c = CollapseState()
        c.setFolder(FolderKey(account: "a", folder: "work"), true)
        let entries = sortFolders(accounts, folders, c, FavouriteState())

        // Both the child and the grandchild go, not just the child.
        #expect(ids(entries) == ["in@0", "work@0", "zulu@0"])
        let work = try #require(entryByID(entries, "work"))
        #expect(work.collapsed)
        #expect(work.hasChildren)
        // 2 of its own plus 4 and 8 from the two hidden descendants.
        #expect(work.badge == 14)
        // Siblings are untouched.
        #expect(entryByID(entries, "in")?.badge == 1)
    }

    @Test func folderTreeCollapsedInnerNode() throws {
        let (accounts, folders) = nestedAccount()
        var c = CollapseState()
        c.setFolder(FolderKey(account: "a", folder: "bugs"), true)
        let entries = sortFolders(accounts, folders, c, FavouriteState())

        #expect(ids(entries) == ["in@0", "work@0", "bugs@1", "zulu@0"])
        #expect(entryByID(entries, "bugs")?.badge == 12, "collapsed Work/Bugs badge is 4+8")
        // The expanded ancestor keeps counting only itself: its child is visible
        // and carries the rest.
        #expect(entryByID(entries, "work")?.badge == 2)
    }

    @Test func folderTreeCollapsingALeafDoesNothing() throws {
        let (accounts, folders) = nestedAccount()
        var c = CollapseState()
        // A stale entry for a folder that has no children any more must not
        // remove it from the list or change its badge.
        c.setFolder(FolderKey(account: "a", folder: "zulu"), true)
        let entries = sortFolders(accounts, folders, c, FavouriteState())

        #expect(ids(entries) == ["in@0", "work@0", "bugs@1", "old@2", "zulu@0"])
        let zulu = try #require(entryByID(entries, "zulu"))
        #expect(!zulu.collapsed)
        #expect(zulu.badge == 16)
    }

    @Test func sortFoldersCollapsedAccount() {
        let accounts = [testAccount("a"), testAccount("b")]
        let folders: [AccountID: [Folder]] = [
            "a": nestedFolders(),
            "b": [testFolder("in-b", path: "INBOX", role: .inbox)],
        ]
        var c = CollapseState()
        c.setAccount("a", true)
        let entries = sortFolders(accounts, folders, c, FavouriteState())

        // The header stays, its whole tree goes, the other account is untouched.
        #expect(ids(entries) == ["#a", "#b", "in-b@0"])
        #expect(entries[0].collapsed && entries[0].hasChildren, "collapsed account header must be foldable and folded")
        #expect(!entries[1].collapsed, "the other account must stay expanded")
    }

    @Test func sortFoldersSingleAccountIgnoresAccountFold() {
        // With one account there is no header, so there is nothing to click and
        // a stored fold must not blank the sidebar.
        let (accounts, folders) = nestedAccount()
        var c = CollapseState()
        c.setAccount("a", true)
        #expect(sortFolders(accounts, folders, c, FavouriteState()).count == 5)
    }

    @Test func folderTreeFlatAccountReservesNoArrow() {
        let accounts = [testAccount("a")]
        let folders: [AccountID: [Folder]] = ["a": [
            testFolder("in", path: "INBOX", role: .inbox),
            testFolder("zulu", path: "Zulu"),
        ]]
        for e in sortFolders(accounts, folders, CollapseState(), FavouriteState()) {
            #expect(!e.nested && !e.hasChildren, "\(e.folder?.id.rawValue ?? ""): want no arrow in a flat account")
        }
    }

    @Test func folderTreeCollapsedParentCycleStaysFlat() {
        // Two folders pointing at each other are unreachable from any root; the
        // orphan sweep lists them flat and folding does not apply.
        let accounts = [testAccount("a")]
        let folders: [AccountID: [Folder]] = ["a": [
            testFolder("x", path: "X/One", parent: "y", unread: 3),
            testFolder("y", path: "X/Two", parent: "x"),
        ]]
        var c = CollapseState()
        c.setFolder(FolderKey(account: "a", folder: "x"), true)
        let entries = sortFolders(accounts, folders, c, FavouriteState())

        #expect(entries.count == 2, "want both folders listed")
        for e in entries {
            #expect(!e.collapsed, "\(e.folder?.id.rawValue ?? ""): an unreachable folder must not be folded")
        }
        #expect(entryByID(entries, "x")?.badge == 3, "X/One badge is its own 3")
    }

    @Test func refreshBadgesFollowsUnread() {
        let (accounts, folders) = nestedAccount()
        var m = MailModel(accounts: accounts, folders: folders)
        m.collapsed.setFolder(FolderKey(account: "a", folder: "work"), true)
        m.rebuildEntries()

        // A new message lands in the hidden grandchild: the visible ancestor's
        // badge has to move even though the folder itself has no row.
        m.adjustCounts(FolderKey(account: "a", folder: "old"), 1, 1)
        #expect(entryByID(m.entries, "work")?.badge == 15, "Work badge after the hidden grandchild gained one")
    }

    @Test func collapseStateRoundTrip() {
        let s = ScratchSettings().settings
        var c = CollapseState()
        c.setFolder(FolderKey(account: "acc_1", folder: "f_1"), true)
        c.setFolder(FolderKey(account: "acc_2", folder: "f_2"), true)
        c.setAccount("acc_2", true)
        c.save(to: s, accounts: [testAccount("acc_1"), testAccount("acc_2")])

        var got = CollapseState.load(from: s)
        #expect(got.folderCollapsed(FolderKey(account: "acc_1", folder: "f_1")))
        #expect(got.folderCollapsed(FolderKey(account: "acc_2", folder: "f_2")))
        #expect(got.accountCollapsed("acc_2"))
        #expect(!got.accountCollapsed("acc_1"))

        // Unfolding removes the entry rather than storing a false.
        got.setFolder(FolderKey(account: "acc_1", folder: "f_1"), false)
        got.save(to: s, accounts: [testAccount("acc_1"), testAccount("acc_2")])
        #expect(s.collapsedFolders.count == 1)
    }

    @Test func collapseStatePrunesRemovedAccounts() {
        let s = ScratchSettings().settings
        var c = CollapseState()
        c.setFolder(FolderKey(account: "acc_gone", folder: "f_1"), true)
        c.setFolder(FolderKey(account: "acc_1", folder: "f_2"), true)
        c.setAccount("acc_gone", true)

        c.save(to: s, accounts: [testAccount("acc_1")])
        #expect(s.collapsedFolders == ["acc_1/f_2"], "only the surviving account's folders are stored")
        #expect(s.collapsedAccounts.isEmpty)

        // An empty account list means "not loaded yet" and must prune nothing.
        c.save(to: s, accounts: [])
        #expect(s.collapsedFolders.count == 2, "both kept when no accounts are known")
    }

    @Test func collapseStateStoredSorted() {
        // Set order is arbitrary; a stable stored value keeps a no-op save from
        // looking like a change to the other windows listening for it.
        let s = ScratchSettings().settings
        var c = CollapseState()
        for id in ["f_3", "f_1", "f_2"] {
            c.setFolder(FolderKey(account: "acc_1", folder: FolderID(id)), true)
        }
        c.save(to: s, accounts: [testAccount("acc_1")])
        #expect(s.collapsedFolders == ["acc_1/f_1", "acc_1/f_2", "acc_1/f_3"])
    }

    @Test func decodeFolderKeyRejectsJunk() {
        for input in ["", "/", "acc_1", "acc_1/", "/f_1"] {
            #expect(FolderKey(encoded: input) == nil, "FolderKey(encoded: \(input)) accepted a malformed entry")
        }
        let k = FolderKey(encoded: "acc_1/f_1")
        #expect(k?.account == "acc_1")
        #expect(k?.folder == "f_1")
        #expect(k?.encoded == "acc_1/f_1")
    }

    @Test func loadCollapseSkipsJunk() {
        let s = ScratchSettings().settings
        s.collapsedFolders = ["acc_1/f_1", "nonsense", "", "acc_2/f_2"]
        s.collapsedAccounts = ["acc_3", "  "]

        let c = CollapseState.load(from: s)
        #expect(c.folders.count == 2, "want the two well-formed entries")
        #expect(c.accounts.count == 1)
        #expect(c.accountCollapsed("acc_3"))
    }

    @Test func folderListedIgnoresFolds() {
        let (accounts, folders) = nestedAccount()
        var m = MailModel(accounts: accounts, folders: folders)
        m.collapsed.setFolder(FolderKey(account: "a", folder: "work"), true)
        m.rebuildEntries()

        // Folded out of sight is still listed, so the selection stays put.
        #expect(m.folderListed(FolderKey(account: "a", folder: "old")), "a folder hidden by a fold must still count as listed")
        #expect(!m.folderListed(FolderKey(account: "a", folder: "nope")), "an unknown folder must not count as listed")
        #expect(!m.folderListed(nil), "the zero key must not count as listed")

        // An empty outbox is hidden outright, and a selection there must move.
        m.folders["a", default: []].append(testFolder("out", path: "Outbox", role: .outbox))
        #expect(!m.folderListed(FolderKey(account: "a", folder: "out")), "an empty outbox is not listed")

        m.accounts[0].enabled = false
        #expect(!m.folderListed(FolderKey(account: "a", folder: "in")), "a disabled account's folders are not listed")
    }
}
