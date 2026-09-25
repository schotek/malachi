// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The counterpart of ui/internal/window/model_test.go. The helpers below
// are shared by the other model suites, as their Go originals are.

/// A list summary with the given id and flags (model_test.go `summary`).
func summary(_ id: String, _ flags: Flag...) -> MessageSummary {
    summary(id, flags: flags)
}

func summary(_ id: String, flags: [Flag]) -> MessageSummary {
    MessageSummary(
        id: MessageID(id), accountId: "", folderId: "", from: [], subject: "s-" + id, date: .goZero, snippet: "",
        flags: flags, hasAttachments: false, size: 0
    )
}

/// A folder of the tests; the name defaults to the id.
func testFolder(
    _ id: String, path: String, role: FolderRole = .none, parent: String? = nil, name: String? = nil,
    selectable: Bool = true, unread: Int = 0, total: Int = 0
) -> Folder {
    Folder(
        id: FolderID(id), accountId: "", parentId: parent.map { FolderID($0) }, name: name ?? id, path: path,
        role: role, subscribed: true, selectable: selectable, synced: true, unread: unread, total: total
    )
}

/// An account of the tests.
func testAccount(
    _ id: String, enabled: Bool = true, name: String = "", email: String = "", displayName: String? = nil,
    state: SyncState? = nil
) -> Account {
    Account(
        id: AccountID(id), config: AccountConfig(name: name, email: email, displayName: displayName), enabled: enabled,
        state: state ?? SyncState(accountId: AccountID(id), status: .idle)
    )
}

/// Three accounts, one disabled, with a nested tree on the first
/// (model_test.go `testAccounts`).
func testAccounts() -> ([Account], [AccountID: [Folder]]) {
    let accounts = [
        testAccount("acc1", email: "one@example.invalid"),
        testAccount("acc2", email: "two@example.invalid"),
        testAccount("acc3", enabled: false, email: "off@example.invalid"),
    ]
    let folders: [AccountID: [Folder]] = [
        "acc1": [
            testFolder("zeta", path: "zeta"),
            testFolder("trash", path: "Trash", role: .trash, name: "Trash"),
            testFolder("inbox", path: "INBOX", role: .inbox, name: "INBOX", unread: 2),
            testFolder("alpha", path: "Alpha", name: "Alpha"),
            testFolder("sub2", path: "Alpha/b", parent: "alpha", name: "b"),
            testFolder("sub1", path: "Alpha/A", parent: "alpha", name: "A"),
            testFolder("deep", path: "Alpha/A/deep", parent: "sub1"),
            testFolder("container", path: "Container", name: "Container", selectable: false),
            testFolder("leaf", path: "Container/leaf", parent: "container"),
        ],
        "acc2": [testFolder("in2", path: "INBOX", role: .inbox, name: "INBOX")],
        "acc3": [testFolder("in3", path: "INBOX", role: .inbox, name: "INBOX")],
    ]
    return (accounts, folders)
}

let errTest = RPCError(code: .internalError, message: "test")

@Suite struct MailModelTests {
    @Test func hasFlagTest() {
        let flags: [Flag] = [.seen, .flagged]
        #expect(hasFlag(flags, .flagged))
        #expect(!hasFlag(flags, .junk))
        #expect(!hasFlag([], .seen))
    }

    @Test func matchesFilterTest() {
        let seen = summary("a", .seen)
        let seenFlagged = summary("b", .seen, .flagged)
        let unread = summary("c")
        let unreadFlagged = summary("d", .flagged)
        let cases: [(MessageFilter, [MessageID: Bool])] = [
            (MessageFilter(rawValue: ""), ["a": true, "b": true, "c": true, "d": true]),
            (.all, ["a": true, "b": true, "c": true, "d": true]),
            (.unread, ["a": false, "b": false, "c": true, "d": true]),
            (.flagged, ["a": false, "b": true, "c": false, "d": true]),
        ]
        for (filter, want) in cases {
            for s in [seen, seenFlagged, unread, unreadFlagged] {
                #expect(matchesFilter(s, filter) == want[s.id], "matchesFilter(\(s.id), \(filter))")
            }
        }
    }

    @Test func summaryMessageTest() {
        let date = Date(timeIntervalSince1970: 1_788_429_600) // 2026-09-03T10:00:00Z
        var s = MessageSummary(
            id: "m", accountId: "", folderId: "",
            from: [Address(name: "Alice", address: "alice@example.invalid")], subject: "Hi", date: date,
            snippet: "snip", flags: [.flagged], hasAttachments: true, size: 0
        )
        var m = summaryMessage(s)
        #expect(m.from.count == 1)
        #expect(m.from.first?.name == "Alice")
        #expect(m.subject == "Hi")
        #expect(m.snippet == "snip")
        #expect(m.date == date)
        #expect(m.unread)
        #expect(m.flagged)
        #expect(m.hasAttachments)
        s.flags = [.seen]
        s.from = []
        m = summaryMessage(s)
        #expect(!m.unread)
        #expect(!m.flagged)
        #expect(m.from.isEmpty)
    }

    @Test func setAppendMessages() throws {
        var m = MailModel()
        m.setMessages([summary("a"), summary("b"), summary("a")], page: PageInfo(nextCursor: "c1", total: 10))
        #expect(m.messages.count == 2)
        #expect(m.index["a"] == 0)
        #expect(m.index["b"] == 1)
        #expect(m.nextCursor == "c1")
        #expect(m.total == 10)
        let added = m.appendMessages([summary("b"), summary("c")], page: PageInfo(total: 10))
        #expect(added == 1)
        #expect(m.messages.count == 3)
        #expect(m.index["c"] == 2)
        #expect(m.nextCursor == nil)
        let c = try #require(m.message("c"))
        #expect(c.index == 2)
        #expect(c.summary.id == "c")
        #expect(m.messageAt(3) == nil)
        #expect(m.messageAt(1)?.id == "b")
        // Replacing the page resets the index and error.
        m.listErr = errTest
        m.setMessages([], page: PageInfo(total: -1))
        #expect(m.messages.isEmpty)
        #expect(m.index.isEmpty)
        #expect(m.listErr == nil)
        #expect(m.total == -1)
    }

    @Test func insertRemoveMessage() throws {
        var m = MailModel()
        let hoisted1 = m.insertMessage(at: 5, summary("a"))
        #expect(hoisted1)
        #expect(m.messages.count == 1)
        #expect(m.index["a"] == 0)
        m.total = 1
        let hoisted2 = m.insertMessage(at: 0, summary("b"))
        #expect(hoisted2)
        #expect(m.messages[0].id == "b")
        #expect(m.index["a"] == 1)
        #expect(m.total == 2)
        let hoisted3 = m.insertMessage(at: 0, summary("a"))
        #expect(!hoisted3, "duplicate insert accepted")
        m.insertMessage(at: -1, summary("c"))
        #expect(m.messages[0].id == "c")
        #expect(m.index["b"] == 1)
        #expect(m.index["a"] == 2)

        let hoisted4 = m.removeMessage("b")
        let removed = try #require(hoisted4)
        #expect(removed.index == 1)
        #expect(removed.summary.id == "b")
        #expect(m.messages.count == 2)
        #expect(m.index["a"] == 1)
        #expect(m.total == 2)
        #expect(m.index["b"] == nil, "removed id still indexed")
        let hoisted5 = m.removeMessage("b")
        #expect(hoisted5 == nil, "second remove succeeded")
        m.total = -1
        m.removeMessage("a")
        #expect(m.total == -1, "unknown total was decremented")
    }

    @Test func updateFlagsTest() throws {
        var m = MailModel()
        m.setMessages([summary("a", .seen)], page: PageInfo(total: 0))
        let hoisted6 = m.updateFlags("a", set: [.seen])
        #expect(!hoisted6, "no-op reported a change")
        let hoisted7 = m.updateFlags("a", set: [.flagged], clear: [.seen])
        #expect(hoisted7, "change not reported")
        let s = try #require(m.message("a")).summary
        #expect(!hasFlag(s.flags, .seen))
        #expect(hasFlag(s.flags, .flagged))
        #expect(s.flags.count == 1)
        let hoisted8 = m.updateFlags("zz", set: [.seen])
        #expect(!hoisted8, "unknown id reported a change")
    }

    @Test func sortFoldersTest() {
        let (accounts, folders) = testAccounts()
        let entries = sortFolders(accounts, folders, CollapseState(), FavouriteState())

        struct Row: Equatable {
            var id: FolderID?
            var header = false
            var depth = 0
        }
        let want: [Row] = [
            Row(header: true),
            Row(id: "inbox"), Row(id: "trash"),
            Row(id: "alpha"), Row(id: "sub1", depth: 1), Row(id: "deep", depth: 2), Row(id: "sub2", depth: 1),
            Row(id: "container"), Row(id: "leaf", depth: 1),
            Row(id: "zeta"),
            Row(header: true),
            Row(id: "in2"),
        ]
        #expect(entries.count == want.count)
        for (i, e) in entries.enumerated() where i < want.count {
            let got = Row(id: e.folder?.id, header: e.header, depth: e.depth)
            #expect(got == want[i], "entry \(i)")
        }
        #expect(entries[0].account?.id == "acc1")
        #expect(entries[10].account?.id == "acc2")

        // A single enabled account gets no header row.
        let single = sortFolders(Array(accounts.prefix(1)), folders, CollapseState(), FavouriteState())
        #expect(single.count == 9)
        #expect(!single[0].header)
        #expect(sortFolders([], [:], CollapseState(), FavouriteState()).isEmpty)
    }

    @Test func sortFoldersCycleGuard() {
        let accounts = [testAccount("a")]
        let folders: [AccountID: [Folder]] = ["a": [
            testFolder("x", path: "p/x", parent: "y"),
            testFolder("y", path: "p/q/y", parent: "x"),
            testFolder("self", path: "self", parent: "self"),
            testFolder("orphan", path: "gone/o", parent: "missing", name: "o"),
        ]]
        let entries = sortFolders(accounts, folders, CollapseState(), FavouriteState())
        #expect(entries.count == 4)
        var depths: [FolderID: Int] = [:]
        for e in entries {
            if let id = e.folder?.id {
                depths[id] = e.depth
            }
        }
        // Self-parent and missing parent are roots; the cycle falls back to
        // the path depth.
        #expect(depths["self"] == 0)
        #expect(depths["orphan"] == 0)
        #expect(depths["x"] == 1)
        #expect(depths["y"] == 2)
        #expect(entries[0].folder?.id == "orphan")
        #expect(entries[1].folder?.id == "self")
    }

    @Test func roleRankAndIcon() {
        let roles: [FolderRole] = [.inbox, .drafts, .sent, .archive, .junk, .trash, .outbox, .all]
        for i in 1..<roles.count {
            #expect(roleRank(roles[i - 1]) < roleRank(roles[i]), "\(roles[i - 1]) should sort before \(roles[i])")
        }
        #expect(roleRank(.none) > roleRank(.all))
        #expect(roleRank("bogus") == roleRank(.none))
        let icons: [FolderRole: String] = [
            .inbox: "mail-unread-symbolic",
            .drafts: "document-edit-symbolic",
            .sent: "mail-send-symbolic",
            .trash: "user-trash-symbolic",
            .junk: "mail-mark-junk-symbolic",
            .archive: "folder-download-symbolic",
            .outbox: "mail-send-symbolic",
            .all: "folder-symbolic",
            .none: "folder-symbolic",
            "bogus": "folder-symbolic",
        ]
        for (role, want) in icons {
            #expect(roleIcon(role) == want, "roleIcon(\(role))")
        }
    }

    @Test func modelFolders() throws {
        let (accounts, folders) = testAccounts()
        var m = MailModel(accounts: accounts, folders: folders)
        m.rebuildEntries()

        let k = try #require(m.initialFolder())
        #expect(k == FolderKey(account: "acc1", folder: "inbox"))
        #expect(m.folder(k)?.unread == 2)
        #expect(m.folder(FolderKey(account: "acc1", folder: "nope")) == nil)
        #expect(m.folderByRole("acc1", .trash)?.id == "trash")
        #expect(m.folderByRole("acc1", .archive) == nil)
        #expect(m.account("acc2")?.config.email == "two@example.invalid")
        #expect(m.account("acc9") == nil)
        let enabled = m.enabledAccounts
        #expect(enabled.count == 2)
        #expect(enabled.first?.id == "acc1")
        #expect(enabled.last?.id == "acc2")

        m.adjustUnread(k, -5)
        #expect(m.folder(k)?.unread == 0, "floor")
        m.adjustUnread(k, 3)
        #expect(m.folder(k)?.unread == 3)
        for e in m.entries where !e.header && e.folder?.id == "inbox" {
            #expect(e.folder?.unread == 3, "entry not updated")
        }
        m.adjustUnread(FolderKey(account: "acc1", folder: "nope"), 1) // no crash
    }

    @Test func visibleFoldersTest() {
        var list = [
            testFolder("in", path: "INBOX", role: .inbox),
            testFolder("trash", path: "Trash", role: .trash),
            testFolder("out", path: "Outbox", role: .outbox),
        ]
        // An empty outbox is hidden; an empty Trash (or Inbox) is not.
        var got = visibleFolders(list)
        #expect(got.map(\.id) == ["in", "trash"])
        list[2].total = 1
        got = visibleFolders(list)
        #expect(got.map(\.id) == ["in", "trash", "out"])
        #expect(visibleFolders([]).isEmpty)

        // The sidebar entries follow, while the model still knows the folder.
        var m = MailModel(
            accounts: [testAccount("a")],
            folders: ["a": [list[0], list[1], testFolder("out", path: "Outbox", role: .outbox)]]
        )
        m.rebuildEntries()
        #expect(ids(m.entries) == ["in@0", "trash@0"])
        #expect(m.folderByRole("a", .outbox)?.id == "out", "hidden outbox not found by role")
        #expect(m.folderRole(FolderKey(account: "a", folder: "out")) == .outbox)
        #expect(m.folderRole(FolderKey(account: "a", folder: "nope")) == .none)
        m.folders["a"]?[2].total = 2
        m.rebuildEntries()
        #expect(ids(m.entries) == ["in@0", "trash@0", "out@0"])
    }

    @Test func initialFolderFallbacks() {
        var m = MailModel(
            accounts: [testAccount("a")],
            folders: ["a": [
                testFolder("c", path: "c", selectable: false),
                testFolder("b", path: "b"),
            ]]
        )
        m.rebuildEntries()
        #expect(m.initialFolder()?.folder == "b", "first selectable")
        m.folders["a"]?[1].selectable = false
        m.rebuildEntries()
        #expect(m.initialFolder() == nil, "nothing selectable but a folder was returned")
        #expect(MailModel().initialFolder() == nil, "empty model returned a folder")
    }

    @Test func generations() {
        var m = MailModel()
        let hoisted9 = m.bumpList()
        #expect(hoisted9 == 1)
        let hoisted10 = m.bumpList()
        #expect(hoisted10 == 2)
        let hoisted11 = m.bumpBody()
        #expect(hoisted11 == 1)
        let hoisted12 = m.bumpFolders()
        #expect(hoisted12 == 1)
        #expect(m.listGen == 2)
        m.loading = true
        m.loadingMore = true
        m.bumpAll()
        #expect(m.listGen == 3)
        #expect(m.bodyGen == 2)
        #expect(m.foldersGen == 2)
        #expect(!m.loading)
        #expect(!m.loadingMore)
    }

    @Test func clearMessagesTest() {
        var m = MailModel()
        m.setMessages([summary("a")], page: PageInfo(nextCursor: "c", total: 3))
        m.listErr = errTest
        let gen = m.listGen
        m.clearMessages()
        #expect(m.messages.isEmpty)
        #expect(m.index.isEmpty)
        #expect(m.nextCursor == nil)
        #expect(m.total == -1)
        #expect(m.listErr == nil)
        #expect(m.listGen == gen, "clearMessages must not touch the generation")
    }

    @Test func accountLabelTest() {
        var a = testAccount("a", name: " Work ", email: "me@example.invalid")
        #expect(accountLabel(a) == "Work")
        a.config.name = "  "
        #expect(accountLabel(a) == "me@example.invalid")
    }

    @Test func selfAddressTest() {
        let a = testAccount("a", email: "me@example.invalid", displayName: "Me")
        #expect(selfAddress(a) == Address(name: "Me", address: "me@example.invalid"))
    }
}
