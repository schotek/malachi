// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// ui/internal/unified unified_test.go, the unified part of
/// ui/internal/selection and ui/internal/contextmenu tests, and the sidebar
/// and model parts of the All Inboxes item, ported.
struct UnifiedTests {
    private func account(_ id: String, _ kind: AccountKind?, _ enabled: Bool) -> Account {
        var a = testAccount(id, enabled: enabled)
        a.config.kind = kind
        return a
    }

    private func inbox(_ acc: String, _ id: String, _ unread: Int, _ total: Int) -> Folder {
        Folder(
            id: FolderID(id), accountId: AccountID(acc), name: id, path: id, role: .inbox, subscribed: true,
            selectable: true, synced: true, unread: unread, total: total)
    }

    private var sample: [Account] {
        [
            account("jira", .jira, true),
            account("b", .graph, true),
            account("off", .imap, false),
            account("cal", .caldav, true),
            account("a", nil, true),  // an absent kind is imap
            account("crm", .eway, true),
        ]
    }

    @Test func accountsAndShown() {
        #expect(Unified.accounts(sample) == ["b", "a"])
        #expect(Unified.shown(sample, setting: true))
        #expect(!Unified.shown(sample, setting: false), "two mail accounts: shown only with the setting on")
        let one = [account("a", .imap, true), account("off", .graph, false), account("j", .jira, true)]
        #expect(!Unified.shown(one, setting: true), "one mail account: not shown")
        #expect(!Unified.shown([], setting: true), "no account: not shown")
    }

    @Test func inboxesAndCounts() {
        let folders: [AccountID: [Folder]] = [
            "a": [
                Folder(id: "a-sent", accountId: "a", name: "Sent", path: "Sent", role: .sent, subscribed: true, selectable: true,
                       synced: true, unread: 9, total: 0),
                inbox("a", "a-in", 2, 10),
            ],
            "b": [
                inbox("b", "b-in", 3, 5),
                Folder(id: "b-arch", accountId: "b", name: "x", path: "x", role: .inbox, subscribed: true, selectable: true,
                       synced: false, unread: 7, total: 7),
            ],
            "off": [inbox("off", "off-in", 4, 4)],
            "jira": [
                Folder(id: "j-v", accountId: "jira", name: "Open", path: "Open", role: .inbox, subscribed: true, selectable: true,
                       synced: true, unread: 1, total: 0, virtual: .open)
            ],
        ]
        let got = Unified.inboxes(sample, folders)
        #expect(got.map(\.id) == ["b-in", "a-in"], "the accounts' order, synced and not virtual")
        #expect(Unified.unread(got) == 5 && Unified.total(got) == 15)
        #expect(Unified.unread([]) == 0 && Unified.total([]) == 0)
        #expect(Unified.covers(listIsUnified: true, "a", "a-in", got))
        #expect(!Unified.covers(listIsUnified: false, "a", "a-in", got))
        #expect(!Unified.covers(listIsUnified: true, "a", "a-sent", got))
        #expect(!Unified.covers(listIsUnified: true, "b", "a-in", got))
        #expect(!Unified.covers(listIsUnified: true, "off", "off-in", got))
        #expect(Unified.coversAny(listIsUnified: true, "b", [], got))
        #expect(!Unified.coversAny(listIsUnified: true, "off", [], got))
        #expect(!Unified.coversAny(listIsUnified: false, "b", [], got))
        #expect(Unified.coversAny(listIsUnified: true, "a", ["a-sent", "a-in"], got))
        #expect(!Unified.coversAny(listIsUnified: true, "a", ["a-sent"], got))
    }

    @Test func key() {
        #expect(FolderKey.unified.isUnified && Unified.isKey("", Unified.keyFolder))
        for k in [
            FolderKey(account: "", folder: ""), FolderKey(account: "a", folder: Unified.keyFolder),
            FolderKey(account: "", folder: "f"), FolderKey(account: "a", folder: "a-in"),
        ] {
            #expect(!k.isUnified, "\(k) is not the key")
        }
        #expect(Unified.keyFolder.rawValue.hasPrefix("@"))
        #expect(FolderKey(encoded: FolderKey.unified.encoded) == nil, "the key never round-trips through a stored list")
    }

    @Test func params() throws {
        let m = Unified.messageParams(filter: .unread, cursor: "c")
        #expect(m == MessageListParams(
            accountId: "", folderId: "", page: Page(cursor: "c", limit: 50), sort: .dateDesc, filter: .unread, unified: .inbox))
        let th = Unified.threadParams(filter: .all)
        #expect(th.unified == .inbox && th.accountId.rawValue.isEmpty && th.folderId.rawValue.isEmpty)
        #expect(th.page == Page(cursor: nil, limit: 50) && th.sort == .dateDesc)
        #expect(Unified.role == .inbox)

        // On the wire: `unified` is there, and the ids are the empty strings
        // Go reads as its zero value; an ordinary listing has no `unified`.
        let data = try JSONCoding.encoder().encode(m)
        let obj = try #require(try JSONSerialization.jsonObject(with: data) as? [String: Any])
        #expect(obj["unified"] as? String == "inbox")
        #expect(obj["accountId"] as? String == "" && obj["folderId"] as? String == "")
        let plain = try JSONCoding.encoder().encode(MessageListParams(accountId: "a", folderId: "f"))
        let plainObj = try #require(try JSONSerialization.jsonObject(with: plain) as? [String: Any])
        #expect(plainObj["unified"] == nil)
        let tdata = try JSONCoding.encoder().encode(th)
        let tobj = try #require(try JSONSerialization.jsonObject(with: tdata) as? [String: Any])
        #expect(tobj["unified"] as? String == "inbox")
    }

    @Test func groupByAccount() {
        struct Row: Equatable {
            var id: String
            var acc: AccountID
        }
        let rows = [Row(id: "1", acc: "b"), Row(id: "2", acc: "a"), Row(id: "3", acc: "b"), Row(id: "4", acc: "a"), Row(id: "5", acc: "c")]
        let got = Unified.groupByAccount(rows) { $0.acc }
        #expect(got.map(\.account) == ["b", "a", "c"], "the accounts in the order they first appear")
        #expect(got.map { $0.items.map(\.id) } == [["1", "3"], ["2", "4"], ["5"]])
        #expect(Unified.groupByAccount([Row](), account: { $0.acc }).isEmpty, "no rows: no groups")
        #expect(Unified.mixed(rows) { $0.acc })
        #expect(!Unified.mixed(Array(rows[..<1])) { $0.acc })
        #expect(!Unified.mixed([Row(id: "1", acc: "a"), Row(id: "2", acc: "a")]) { $0.acc })
        #expect(!Unified.mixed([Row]()) { $0.acc })
    }

    @Test func threadIdsAreScopedByAccount() {
        let a = Unified.scopedThread("t1", account: "a")
        let b = Unified.scopedThread("t1", account: "b")
        #expect(a != b && Unified.rawThread(a) == "t1" && Unified.rawThread(b) == "t1")
        #expect(Unified.rawThread("plain") == "plain")
        var s = MessageSummary(
            id: "m", accountId: "a", folderId: "f", threadId: "t1", from: [], subject: "", date: Date(), snippet: "",
            flags: [], hasAttachments: false, size: 0)
        #expect(Unified.scoped(s, account: "a").threadId == a)
        s.threadId = nil
        #expect(Unified.scoped(s, account: "a").threadId == nil)
    }

    @Test func texts() {
        #expect(Unified.title == "All Inboxes")
        #expect(Unified.preferenceTitle == "Show All Inboxes")
        #expect(Unified.preferenceSubtitle == "In the sidebar when you have more than one mail account")
    }

    // MARK: Selection

    private var mail: ActionFlags {
        ActionFlags(on: true, reply: true, replyAll: true, forward: true, star: true, trash: true, archive: true,
                    junk: true, move: true, markUnread: true, toggleFlag: true, loadImages: true, trustSender: true)
    }

    @Test func combineAccounts() {
        var crm = mail
        crm.crm = true
        let rows = [crm, crm, crm]
        let same = Selection.combineAccounts(rows, accounts: ["a", "a", "a"])
        #expect(same == Selection.combine(rows) && !same.mixedAccounts, "one account is combine's")
        let got = Selection.combineAccounts(rows, accounts: ["a", "b", "a"])
        var want = Selection.combine(rows)
        want.mixedAccounts = true
        want.forward = false
        want.move = false
        want.crm = false
        #expect(got == want)
        #expect(got.trash && got.archive && got.junk && got.star && got.markUnread && got.open, "the per-account actions stay")
        // One row is never mixed, whatever the accounts say.
        let one = Selection.combineAccounts([crm], accounts: ["a", "b"])
        #expect(!one.mixedAccounts && one.forward && one.move)
        #expect(Selection.combineAccounts([], accounts: []) == ActionFlags())
    }

    @Test func restrictIn() {
        let rows: [(key: String, account: AccountID)] = [("1", "a"), ("2", "b"), ("3", "a")]
        #expect(Selection.restrictIn(rows, latest: "2", crossAccount: false) == Selection.restrict(rows, latest: "2"))
        #expect(Selection.restrictIn(rows, latest: "2", crossAccount: true) == ["1", "2", "3"], "cross-account keeps every row")
        #expect(Selection.mixed(rows))
        #expect(!Selection.mixed(Array(rows[..<1])))
        #expect(!Selection.mixed([(key: "1", account: AccountID("a")), (key: "3", account: AccountID("a"))]))
        #expect(!Selection.mixed([(key: String, account: AccountID)]()))
    }

    @Test func contextMenuOfMixedAccounts() {
        func show(_ groups: [[ContextMenu.Item]]) -> String {
            groups.map { $0.map { "\($0.id)\($0.enabled ? "" : "-")" }.joined(separator: " ") }.joined(separator: " | ")
        }
        var s = ContextMenu.MessageState()
        s.count = 2
        s.open = true
        s.offerForward = true
        s.offerArchive = true
        s.offerMove = true
        s.offerJunk = true
        s.offerTrash = true
        s.forward = true
        s.move = true
        s.archive = true
        s.junk = true
        s.trash = true
        s.toggleFlag = true
        s.markUnread = true
        s.offerCrm = true
        s.crm = true
        s.mixedAccounts = true
        #expect(show(ContextMenu.messageItems(s)) ==
            "openMessage | forward- | crmSave- | markUnread toggleFlag | archive moveTo- junk trash")
        s.mixedAccounts = false
        #expect(show(ContextMenu.messageItems(s)) ==
            "openMessage | forward | crmSave | markUnread toggleFlag | archive moveTo junk trash")
        // One row is never mixed.
        s.count = 1
        s.mixedAccounts = true
        let forward = ContextMenu.messageItems(s).flatMap { $0 }.first { $0.id == .forward }
        #expect(forward?.enabled == true)
    }

    // MARK: The sidebar and the model

    private func twoAccountModel(setting: Bool = true) -> MailModel {
        var m = MailModel(unifiedSetting: setting)
        m.accounts = [testAccount("a", name: "Work"), testAccount("b", name: "Home")]
        m.folders = [
            "a": [inbox("a", "a-in", 2, 10)],
            "b": [inbox("b", "b-in", 3, 5)],
        ]
        m.rebuildEntries()
        return m
    }

    @Test func theItemHeadsTheFavouritesSection() {
        var m = twoAccountModel()
        // The item is the first row of the Favourites section, which exists
        // for it alone (unified_test.go TestUnifiedEntryHeadsTheFavouritesSection).
        #expect(ids(m.entries) == ["#favourites", "*@unified:inbox@0", "#a", "a-in@0", "#b", "b-in@0"])
        let heading = m.entries[0]
        #expect(heading.header && heading.favourite && heading.hasChildren && !heading.collapsed)
        let item = m.entries[1]
        #expect(item.unified && item.favourite && item.key == .unified && item.badge == 5 && item.depth == 0)
        #expect(item.account == nil && item.folder == nil && !item.header)
        #expect(m.entries.filter(\.unified).count == 1)
        // initialFolder is still a real folder.
        #expect(m.initialFolder() == FolderKey(account: "a", folder: "a-in"))

        // Pins follow the item (TestUnifiedEntryComesBeforeThePins).
        m.favourites.toggle(FolderKey(account: "b", folder: "b-in"))
        m.rebuildEntries()
        #expect(Array(ids(m.entries).prefix(3)) == ["#favourites", "*@unified:inbox@0", "*b-in@0"])

        // Folded, the section is its heading alone; the item stays listed
        // and the pinned folder is still in the tree.
        m.collapsed.setFavourites(true)
        m.rebuildEntries()
        #expect(ids(m.entries) == ["#favourites", "#a", "a-in@0", "#b", "b-in@0"])
        #expect(m.entries[0].collapsed && m.entries[0].hasChildren)
        #expect(m.folderListed(.unified))
        #expect(m.initialFolder() == FolderKey(account: "a", folder: "a-in"))

        // Without the item and without pins there is no section to fold.
        m.unifiedSetting = false
        m.favourites = FavouriteState()
        m.rebuildEntries()
        #expect(ids(m.entries) == ["#a", "a-in@0", "#b", "b-in@0"])
    }

    @Test func theItemNeedsTwoMailAccountsAndTheSetting() {
        #expect(twoAccountModel(setting: false).entries.allSatisfy { !$0.unified })
        var one = MailModel()
        one.accounts = [testAccount("a")]
        one.folders = ["a": [inbox("a", "a-in", 1, 1)]]
        one.rebuildEntries()
        #expect(one.entries.allSatisfy { !$0.unified })
        #expect(!one.folderListed(.unified))
        var two = twoAccountModel()
        #expect(two.folderListed(.unified))
        two.unifiedSetting = false
        #expect(!two.folderListed(.unified), "hidden: a selection of it falls back")
    }

    @Test func theBadgeFollowsTheInboxCounts() {
        var m = twoAccountModel()
        m.adjustCounts(FolderKey(account: "b", folder: "b-in"), -2, 0)
        #expect(m.entries[1].badge == 3)
        m.adjustCounts(FolderKey(account: "a", folder: "a-in"), 4, 4)
        #expect(m.entries[1].badge == 7)
        #expect(m.entries[1].unified && m.entries[1].favourite, "the refreshed row is still the section's")
    }

    @Test func listCoversTheInboxesOnlyWhileTheListIsUnified() {
        var m = twoAccountModel()
        m.listFolder = FolderKey(account: "a", folder: "a-in")
        #expect(m.listCovers(FolderKey(account: "a", folder: "a-in")))
        #expect(!m.listCovers(FolderKey(account: "b", folder: "b-in")))
        m.listFolder = .unified
        #expect(m.listIsUnified)
        #expect(m.listCovers(FolderKey(account: "b", folder: "b-in")))
        #expect(!m.listCovers(FolderKey(account: "b", folder: "b-trash")))
        #expect(m.listCoversAny("a", []) && m.listCoversAny("a", ["x", "a-in"]) && !m.listCoversAny("a", ["x"]))
        #expect(!m.listCoversAny("zzz", []))
    }

    @Test func aSearchFromTheItemCoversEveryAccount() {
        var m = twoAccountModel()
        m.selected = .unified
        let (params, effective) = m.searchRequest("hello", scope: .folder)
        #expect(effective == .all && params.accountId == nil && params.folderId == nil)
        let bar = m.searchBar()
        #expect(!bar.narrowEnabled)
    }

    @Test func aRowOfTheItemNamesItsAccount() {
        var m = twoAccountModel()
        let s = MessageSummary(
            id: "m1", accountId: "b", folderId: "b-in", from: [], subject: "s", date: Date(), snippet: "", flags: [],
            hasAttachments: false, size: 0)
        #expect(m.rowMessage(s).origin.isEmpty, "an ordinary list has no origin")
        m.listFolder = .unified
        #expect(m.rowMessage(s).origin == "Home")
    }

    @Test func theCountsOfTheItemAreTheInboxesSum() {
        #expect(countsText(unread: 5, total: 15, outbox: false) == "5 unread of 15")
        #expect(countsText(unread: 0, total: 15, outbox: false) == "15 messages")
        #expect(countsText(unread: 0, total: 0, outbox: false).isEmpty)
        #expect(countsText(unread: 3, total: 4, outbox: true) == "4 messages")
        #expect(folderCountsText(inbox("a", "a-in", 2, 10)) == "2 unread of 10")
    }
}
