// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The counterpart of ui/internal/window/folders_test.go: the sidebar layout
// the window relies on, where ordering, depth and header placement must be
// exact.

/// Renders entries compactly (folders_test.go `ids`): "#acc" for an
/// account heading, "#favourites" for the Favourites heading, "id@depth"
/// for a tree row and "*id@depth" for a row of the Favourites section.
func ids(_ entries: [FolderEntry]) -> [String] {
    entries.map { e in
        if e.header, e.favourite {
            return "#favourites"
        }
        if e.header {
            return "#" + (e.account?.id.rawValue ?? "")
        }
        let id = e.folder?.id.rawValue ?? ""
        if e.favourite {
            return "*\(id)@\(e.depth)"
        }
        return "\(id)@\(e.depth)"
    }
}

@Suite struct FolderTreeTests {
    @Test func sortFoldersHeadersOnlyForEnabled() {
        var accounts = [testAccount("a"), testAccount("b", enabled: false)]
        let folders: [AccountID: [Folder]] = [
            "a": [testFolder("in", path: "INBOX", role: .inbox)],
            "b": [testFolder("in-b", path: "INBOX", role: .inbox)],
        ]
        // One enabled account: no header, the disabled one is absent entirely.
        #expect(ids(sortFolders(accounts, folders, CollapseState(), FavouriteState())) == ["in@0"])
        accounts[1].enabled = true
        #expect(ids(sortFolders(accounts, folders, CollapseState(), FavouriteState())) == ["#a", "in@0", "#b", "in-b@0"])
    }

    @Test func sortFoldersEmptyAccountKeepsHeader() {
        // An account before its first sync lists no folders; its header still
        // appears so the user sees the account is there.
        let accounts = [testAccount("a"), testAccount("b")]
        let folders: [AccountID: [Folder]] = [
            "b": [testFolder("in-b", path: "INBOX", role: .inbox)],
        ]
        #expect(ids(sortFolders(accounts, folders, CollapseState(), FavouriteState())) == ["#a", "#b", "in-b@0"])
        var m = MailModel(accounts: accounts, folders: folders)
        m.rebuildEntries()
        #expect(m.initialFolder() == FolderKey(account: "b", folder: "in-b"), "initialFolder skipped the header")
    }

    @Test func sortFoldersOrdering() {
        let accounts = [testAccount("a")]
        let folders: [AccountID: [Folder]] = ["a": [
            testFolder("b", path: "beta"),
            testFolder("A", path: "Alpha"),
            testFolder("sent", path: "Sent", role: .sent),
            testFolder("all", path: "All Mail", role: .all),
            testFolder("junk", path: "Junk", role: .junk),
            testFolder("in", path: "zzz/INBOX", role: .inbox),
            testFolder("drafts", path: "Drafts", role: .drafts),
            testFolder("arch", path: "Archive", role: .archive),
            testFolder("trash", path: "Trash", role: .trash),
            // Total: an empty outbox is not listed (visibleFolders).
            testFolder("out", path: "Outbox", role: .outbox, total: 1),
        ]]
        // Roles in rank order regardless of path, then plain folders by
        // case-insensitive path.
        let want = ["in@0", "drafts@0", "sent@0", "arch@0", "junk@0", "trash@0", "out@0", "all@0", "A@0", "b@0"]
        #expect(ids(sortFolders(accounts, folders, CollapseState(), FavouriteState())) == want)
    }

    @Test func sortFoldersChildrenFollowParent() {
        let accounts = [testAccount("a")]
        let folders: [AccountID: [Folder]] = ["a": [
            // Children listed before their parent and out of order.
            testFolder("p-b", path: "Projects/b", parent: "p"),
            testFolder("p-a-x", path: "Projects/a/x", parent: "p-a"),
            testFolder("p-a", path: "Projects/a", parent: "p"),
            testFolder("p", path: "Projects", selectable: false),
            testFolder("in", path: "INBOX", role: .inbox),
            // A child with a role sorts before its plain siblings.
            testFolder("p-junk", path: "Projects/zz", role: .junk, parent: "p"),
        ]]
        let want = ["in@0", "p@0", "p-junk@1", "p-a@1", "p-a-x@2", "p-b@1"]
        #expect(ids(sortFolders(accounts, folders, CollapseState(), FavouriteState())) == want)
    }

    @Test func sortFoldersDepthCap() {
        // A chain deeper than maxFolderDepth: the part below the cap is listed
        // flat afterwards, indented by its path, and nothing is lost.
        let n = maxFolderDepth + 5
        var list: [Folder] = []
        var path = ""
        for i in 0..<n {
            if !path.isEmpty {
                path += "/"
            }
            path += "f\(i)"
            list.append(testFolder("f\(i)", path: path, parent: i > 0 ? "f\(i - 1)" : nil))
        }
        let entries = sortFolders([testAccount("a")], ["a": list], CollapseState(), FavouriteState())
        #expect(entries.count == n)
        var seen = Set<FolderID>()
        for (i, e) in entries.enumerated() {
            guard let id = e.folder?.id else {
                Issue.record("entry \(i) has no folder")
                continue
            }
            #expect(!seen.contains(id), "entry \(i) duplicated")
            seen.insert(id)
            #expect(e.depth >= 0 && e.depth <= n, "entry \(i) depth \(e.depth) out of range")
        }
        #expect(entries[0].folder?.id == "f0")
        #expect(entries[0].depth == 0)
        #expect(entries[maxFolderDepth].depth == maxFolderDepth)
        let orphan = entries[maxFolderDepth + 1]
        #expect(orphan.folder?.id == "f33")
        #expect(orphan.depth == 33)
    }

    @Test func sortFoldersIgnoresFoldersOfUnknownAccounts() {
        let accounts = [testAccount("a")]
        let folders: [AccountID: [Folder]] = [
            "a": [testFolder("in", path: "INBOX", role: .inbox)],
            "ghost": [testFolder("g", path: "INBOX", role: .inbox)],
        ]
        #expect(ids(sortFolders(accounts, folders, CollapseState(), FavouriteState())) == ["in@0"])
    }
}
