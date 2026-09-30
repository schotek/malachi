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

    // Jira accounts (Swift-first; mirror in folders_test.go when GTK gets
    // them): the fixed views below the role folders, above the spaces.

    @Test func jiraViewsSortAboveTheSpaces() {
        let accounts = [jiraTestAccount("j")]
        let folders: [AccountID: [Folder]] = ["j": [
            testFolder("web", path: "Website"),
            testFolder("itsd", path: "IT Service Desk"),
            jiraView("open", .open),
            jiraView("mine", .assignedToMe),
            jiraView("watch", .watching),
            // A queued comment keeps the outbox above the views; an empty
            // one is not listed.
            testFolder("out", path: "Outbox", role: .outbox, total: 1),
        ]]
        let want = ["out@0", "mine@0", "watch@0", "open@0", "itsd@0", "web@0"]
        #expect(ids(sortFolders(accounts, folders, CollapseState(), FavouriteState())) == want)
        var empty = folders
        empty["j"]?.removeLast()
        empty["j"]?.append(testFolder("out", path: "Outbox", role: .outbox))
        #expect(ids(sortFolders(accounts, empty, CollapseState(), FavouriteState())) == Array(want.dropFirst()))
        // A view this client does not know sorts like a space.
        let later = [jiraView("later", "later", path: "Later"), testFolder("abc", path: "ABC"), jiraView("open", .open)]
        #expect(sortSiblings(later).map(\.id.rawValue) == ["open", "abc", "later"])
        // Mail accounts keep their order.
        let mail = [testFolder("b", path: "beta"), testFolder("in", path: "INBOX", role: .inbox), testFolder("A", path: "Alpha")]
        #expect(sortSiblings(mail).map(\.id.rawValue) == ["in", "A", "b"])
    }

    @Test func jiraViewsHaveTheirIconAndTitle() {
        #expect(folderIcon(jiraView("mine", .assignedToMe)) == "folder-saved-search-symbolic")
        #expect(folderIcon(jiraView("open", .open)) == "folder-saved-search-symbolic")
        #expect(folderIcon(testFolder("in", path: "INBOX", role: .inbox)) == "mail-unread-symbolic")
        #expect(folderIcon(testFolder("itsd", path: "IT Service Desk")) == "folder-symbolic")
        #expect(folderIcon(testFolder("out", path: "Outbox", role: .outbox)) == "mail-send-symbolic")

        // The daemon's English name is only a fallback: the code decides.
        #expect(folderTitle(jiraView("mine", .assignedToMe, name: "assigned")) == "Assigned to Me")
        #expect(folderTitle(jiraView("watch", .watching, name: "watching")) == "Watching")
        #expect(folderTitle(jiraView("open", .open, name: "open")) == "Open")
        #expect(folderTitle(jiraView("later", "later", name: "Later")) == "Later")
        #expect(folderTitle(testFolder("itsd", path: "IT Service Desk", name: "IT Service Desk")) == "IT Service Desk")
    }

    /// The Czech titles of the views: the "folder" context entries that
    /// `folderTitle` asks for, not the plain verb "Open".
    @Test(.enabled(if: folderTreeLocaleDir != nil, "run `make -C macos locale` and export MALACHI_LOCALE_DIR"))
    func jiraViewsInCzech() throws {
        let dir = try #require(folderTreeLocaleDir)
        let cs = Catalogue.load(from: dir, languages: ["cs"])
        #expect(cs.context("folder", folderTitle(jiraView("mine", .assignedToMe))) == "Přiřazené mně")
        #expect(cs.context("folder", folderTitle(jiraView("watch", .watching))) == "Sledované")
        #expect(cs.context("folder", folderTitle(jiraView("open", .open))) == "Neuzavřené")
    }

    @Test func jiraAccountLabelFallsBackToTheSite() {
        var a = jiraTestAccount("j", name: "  Acme Jira ")
        #expect(accountLabel(a) == "Acme Jira")
        a.config.name = " "
        #expect(accountLabel(a) == "acme.atlassian.net")
        a.config.jira?.siteUrl = "not a url"
        #expect(accountLabel(a) == "jana@acme.example")
        // A mail account is unchanged.
        #expect(accountLabel(testAccount("m", name: "", email: " me@example.invalid ")) == "me@example.invalid")

        #expect(accountHeaderBadge(jiraTestAccount("j")) == "JIRA")
        #expect(accountHeaderBadge(testAccount("m")) == "")
    }

    @Test func initialFolderPrefersTheMailInbox() {
        // The Jira account comes first and has no Inbox: the mail
        // account's Inbox still wins.
        let accounts = [jiraTestAccount("j"), testAccount("m")]
        let folders: [AccountID: [Folder]] = [
            "j": [jiraView("mine", .assignedToMe), testFolder("itsd", path: "IT Service Desk")],
            "m": [testFolder("arch", path: "Archive", role: .archive), testFolder("in", path: "INBOX", role: .inbox)],
        ]
        var m = MailModel(accounts: accounts, folders: folders)
        m.rebuildEntries()
        #expect(ids(m.entries) == ["#j", "mine@0", "itsd@0", "#m", "in@0", "arch@0"])
        #expect(m.initialFolder() == FolderKey(account: "m", folder: "in"))
        // Without a mail account the first view is it.
        var jiraOnly = MailModel(accounts: [accounts[0]], folders: folders)
        jiraOnly.rebuildEntries()
        #expect(jiraOnly.initialFolder() == FolderKey(account: "j", folder: "mine"))
    }

    @Test func folderCountsTextTest() {
        let cases: [(String, Folder, String)] = [
            ("empty", testFolder("in", path: "INBOX", role: .inbox), ""),
            ("unsynced", testFolder("all", path: "All Mail", role: .all, synced: false), ""),
            ("inconsistent unread without total", testFolder("f", path: "f", unread: 3), ""),
            ("one read message", testFolder("f", path: "f", total: 1), "1 message"),
            ("only read messages", testFolder("f", path: "f", total: 1234), "1234 messages"),
            ("one unread", testFolder("f", path: "f", unread: 1, total: 1234), "1 unread of 1234"),
            ("several unread", testFolder("f", path: "f", unread: 12, total: 1234), "12 unread of 1234"),
            ("all unread", testFolder("f", path: "f", unread: 2, total: 2), "2 unread of 2"),
            // The outbox holds what is to be sent, not mail to read.
            ("outbox", testFolder("out", path: "Outbox", role: .outbox, unread: 1, total: 2), "2 messages"),
            ("outbox of one", testFolder("out", path: "Outbox", role: .outbox, total: 1), "1 message"),
        ]
        for (name, f, want) in cases {
            #expect(folderCountsText(f) == want, "\(name): folderCountsText = \(folderCountsText(f))")
        }
    }
}

/// A virtual folder of a Jira account (role none, `virtual` set).
private func jiraView(_ id: String, _ v: VirtualFolder, name: String? = nil, path: String? = nil) -> Folder {
    var f = testFolder(id, path: path ?? id, name: name)
    f.virtual = v
    return f
}

/// A Jira Cloud account of the sidebar tests.
private func jiraTestAccount(_ id: String, name: String = "") -> Account {
    Account(
        id: AccountID(id),
        config: AccountConfig(
            name: name, email: "jana@acme.example", kind: .jira,
            jira: JiraConfig(siteUrl: "https://Acme.Atlassian.net:443", deployment: .cloud, login: "jana@acme.example")
        ),
        enabled: true,
        state: SyncState(accountId: AccountID(id), status: .idle),
        capabilities: []
    )
}

/// The generated catalogues (`MALACHI_LOCALE_DIR`, as `make test-macos`
/// exports it); nil skips the Czech case.
private let folderTreeLocaleDir: URL? = {
    guard let dir = ProcessInfo.processInfo.environment[Catalogue.localeDirEnv], !dir.isEmpty else { return nil }
    let url = URL(fileURLWithPath: dir, isDirectory: true)
    return Catalogue.availableLanguages(in: url).contains("cs") ? url : nil
}()
