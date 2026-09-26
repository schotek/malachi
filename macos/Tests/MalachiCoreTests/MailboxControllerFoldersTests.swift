// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The folder half of the main window over the controller
// (ui/internal/window/folders.go, collapse.go, favourites.go and the
// sidebar parts of notify.go and window.go), exercised against MailFixture.

private struct Timeout: Error {}

@MainActor
private func waitUntil(_ timeout: Duration = .seconds(5), _ cond: () -> Bool) async throws {
    let deadline = ContinuousClock.now + timeout
    while !cond() {
        if ContinuousClock.now > deadline { throw Timeout() }
        try await Task.sleep(for: .milliseconds(10))
    }
}

/// What the controller emitted, in order.
@MainActor
private final class Log {
    var toasts: [String] = []
    var statuses: [SidebarStatus] = []
    var rebuilds = 0
    var badgeRefreshes = 0
    var highlights: [(key: FolderKey?, fav: Bool)] = []
    var selections: [FolderKey?] = []
    var reloads = 0
    var accountsLoaded = 0
    var listInserts: [NewMessageNotification] = []
    var outboxRefreshes: [AccountID] = []
    var collapses = 0
    var banners: [(account: AccountID?, title: String?, button: String?)] = []
    /// [title, subtitle] per onListTitleChanged.
    var titles: [[String]] = []
}

/// A fixture, a connected client and a controller over a throwaway
/// settings domain.
@MainActor
private final class Harness {
    let fixture: MailFixture
    let client: RPCClient
    let scratch: ScratchSettings
    let sync: SyncController
    let mailbox: MailboxController
    let log = Log()
    private var pump: Task<Void, Never>?

    static let info = SystemInfo(version: "fake", protocolVersion: API.protocolVersion, pid: 7, storePath: "/tmp/s.db")

    init(accounts: [Account], folders: [AccountID: [Folder]], states: [SyncState] = [], connect: Bool = true) async throws {
        fixture = try MailFixture()
        await fixture.setAccounts(accounts)
        await fixture.setFolders(folders)
        await fixture.setSyncStates(states)
        try await fixture.start()
        client = RPCClient(socketPath: fixture.path)
        scratch = ScratchSettings()
        sync = SyncController()
        sync.fallbackDelay = .milliseconds(150)
        let log = log
        mailbox = MailboxController(client: client, settings: scratch.settings, sync: sync) { log.toasts.append($0) }
        mailbox.onEntriesChanged = { log.rebuilds += 1 }
        mailbox.onBadgesChanged = { log.badgeRefreshes += 1 }
        mailbox.onFolderStatus = { log.statuses.append($0) }
        mailbox.onSelectionChanged = { key, fav in log.highlights.append((key, fav)) }
        mailbox.onFolderSelected = { key, _ in log.selections.append(key) }
        mailbox.reloadMessages = { [unowned mailbox] in
            // The list half's loadMessages, as far as the folder half sees it.
            mailbox.model.listFolder = mailbox.model.selected
            log.reloads += 1
        }
        mailbox.onAccountsLoaded = { _ in log.accountsLoaded += 1 }
        mailbox.onNewMessageForList = { log.listInserts.append($0) }
        mailbox.refreshOutboxViews = { log.outboxRefreshes.append($0) }
        mailbox.collapseLoading = { log.collapses += 1 }
        mailbox.onListTitleChanged = { title, subtitle in log.titles.append([title, subtitle]) }
        sync.onAuthBanner = { acc, title, button in log.banners.append((acc, title, button)) }
        if connect {
            try await self.connect()
        }
    }

    /// Dials and tells the controller, as the app does on
    /// `ConnectionController.onState`.
    func connect() async throws {
        try await client.connect()
        mailbox.handleConnection(.connected(Harness.info))
    }

    /// Routes the daemon's notifications into the controller, as the app
    /// does from `ConnectionController.onNotification`.
    func pumpNotifications() {
        let client = client
        let mailbox = mailbox
        pump = Task { @MainActor in
            for await raw in client.notifications {
                if let n = try? DaemonNotification(raw) {
                    mailbox.handleNotification(n)
                }
            }
        }
    }

    /// Waits for the sidebar to settle after a load: the rebuild count
    /// reaches `rebuilds`.
    func settle(rebuilds: Int) async throws {
        try await waitUntil { self.log.rebuilds >= rebuilds }
    }

    func stop() async {
        pump?.cancel()
        mailbox.close()
        await client.close()
        await fixture.stop()
    }
}

private func folderIDs(_ entries: [FolderEntry]) -> [String] {
    entries.compactMap { $0.header ? nil : $0.folder?.id.rawValue }
}

private let inbox1 = FolderKey(account: "acc1", folder: "inbox")

@MainActor
@Suite(.serialized) struct MailboxControllerFoldersTests {
    @Test func loadsAccountsAndFoldersAndSelectsTheInbox() async throws {
        let (accounts, folders) = testAccounts()
        let h = try await Harness(accounts: accounts, folders: folders)
        defer { Task { await h.stop() } }
        #expect(h.log.statuses.first == .status(icon: "", title: "Loading…", description: ""))
        try await h.settle(rebuilds: 1)

        // Two enabled accounts: a header each, the disabled one left out.
        let entries = h.mailbox.model.entries
        #expect(entries.filter(\.header).count == 2)
        #expect(folderIDs(entries) == ["inbox", "trash", "alpha", "sub1", "deep", "sub2", "container", "leaf", "zeta", "in2"])
        #expect(h.mailbox.hasAccounts)
        #expect(h.log.accountsLoaded == 1)
        #expect(h.log.statuses.last == .folders)
        #expect(h.mailbox.sidebarStatus == .folders)

        // The first Inbox is selected, announced, highlighted and listed once.
        #expect(h.mailbox.model.selected == inbox1)
        #expect(h.log.selections == [inbox1])
        #expect(h.log.highlights.last?.key == inbox1)
        #expect(h.log.reloads == 1)
        #expect(h.mailbox.selectedFolderTitle == "Inbox")
        // folder.list once per enabled account, after account.list.
        #expect(await h.fixture.callCount(API.AccountList.name) == 1)
        #expect(await h.fixture.callCount(API.FolderList.name) == 2)
        #expect(await h.fixture.callCount(API.SyncStatus.name) == 1)
        #expect(h.log.toasts.isEmpty)
        // The footer follows the account.list states (all idle), and the
        // line knows the connection.
        #expect(h.sync.footer.text == "Up to date")
        #expect(h.sync.line == StatusLine(text: "Up to date", active: true, daemon: "Connected to malachid fake (pid 7)"))
        // The window title: the Inbox, without counts (it has no total).
        #expect(h.log.titles.last == ["Inbox", ""])
    }

    @Test func singleAccountHasNoHeadersUntilAFavourite() async throws {
        let (accounts, folders) = nestedAccount()
        let h = try await Harness(accounts: accounts, folders: folders)
        defer { Task { await h.stop() } }
        try await h.settle(rebuilds: 1)
        #expect(h.mailbox.model.entries.filter(\.header).isEmpty)
        #expect(folderIDs(h.mailbox.model.entries) == ["in", "work", "bugs", "old", "zulu"])
        #expect(h.mailbox.model.selected == FolderKey(account: "a", folder: "in"))

        // Pinning a folder adds the Favourites section and, above the tree,
        // the account's heading.
        h.mailbox.toggleFavourite(FolderKey(account: "a", folder: "zulu"))
        let entries = h.mailbox.model.entries
        #expect(entries.count == 8)
        #expect(entries[0].header && entries[0].favourite)
        #expect(entries[1].favourite && entries[1].folder?.id == "zulu")
        #expect(entries[2].header && entries[2].account?.id == "a")
        #expect(h.scratch.settings.favouriteFolders == ["a/zulu"])
        // Pinning is not navigating: the selection and the list stayed.
        #expect(h.mailbox.model.selected == FolderKey(account: "a", folder: "in"))
        #expect(h.log.reloads == 1)

        h.mailbox.toggleFavourite(FolderKey(account: "a", folder: "zulu"))
        #expect(h.mailbox.model.entries.count == 5)
        #expect(h.scratch.settings.favouriteFolders.isEmpty)
    }

    @Test func folderListFailureShowsTheStatusPage() async throws {
        let (accounts, folders) = nestedAccount()
        let h = try await Harness(accounts: accounts, folders: folders, connect: false)
        defer { Task { await h.stop() } }
        await h.fixture.fail(API.FolderList.name, with: RPCError(code: .networkError, message: "down"))
        try await h.connect()
        try await h.settle(rebuilds: 1)
        #expect(h.mailbox.model.entries.isEmpty)
        #expect(h.log.statuses.last == .status(
            icon: "dialog-warning-symbolic", title: "Folders Unavailable",
            description: "Loading folders failed: the server could not be reached"
        ))
        #expect(h.mailbox.model.selected == nil)
        #expect(h.log.reloads == 0, "nothing was selected, nothing to clear")
        #expect(h.log.toasts.isEmpty, "a folder.list failure is a status page, not a toast")

        // A later success replaces the page; the last good list is kept
        // over a transient failure after that.
        await h.fixture.succeed(API.FolderList.name)
        h.mailbox.loadFolders("a", h.mailbox.model.foldersGen)
        try await h.settle(rebuilds: 2)
        #expect(h.log.statuses.last == .folders)
        #expect(h.mailbox.model.selected == FolderKey(account: "a", folder: "in"))
        await h.fixture.fail(API.FolderList.name, with: RPCError(code: .serverError, message: "500"))
        h.mailbox.loadFolders("a", h.mailbox.model.foldersGen)
        try await h.settle(rebuilds: 3)
        #expect(folderIDs(h.mailbox.model.entries) == ["in", "work", "bugs", "old", "zulu"])
        #expect(h.mailbox.model.folderErr["a"] != nil)
        #expect(h.log.statuses.last == .folders)
    }

    @Test func accountListFailureShowsTheStatusPage() async throws {
        let h = try await Harness(accounts: [], folders: [:], connect: false)
        defer { Task { await h.stop() } }
        await h.fixture.fail(API.AccountList.name, with: RPCError(code: .internalError, message: "boom"))
        try await h.connect()
        try await waitUntil { h.log.statuses.count == 2 }
        #expect(h.log.statuses == [
            .status(icon: "", title: "Loading…", description: ""),
            .status(icon: "dialog-warning-symbolic", title: "Folders Unavailable", description: "Loading folders failed"),
        ])
        #expect(h.log.rebuilds == 0)
        #expect(!h.mailbox.hasAccounts)
    }

    @Test func emptySidebarStatuses() async throws {
        // No accounts at all.
        let none = try await Harness(accounts: [], folders: [:])
        defer { Task { await none.stop() } }
        try await none.settle(rebuilds: 1)
        #expect(none.log.statuses.last == .status(
            icon: "system-users-symbolic", title: "No Accounts",
            description: "Add a mail account in Preferences to see its folders here."
        ))
        #expect(none.log.reloads == 0)
        #expect(none.sync.footer.text.isEmpty, "no enabled account: no sync line")

        // Accounts, none enabled.
        let paused = try await Harness(accounts: [testAccount("a", enabled: false)], folders: [:])
        defer { Task { await paused.stop() } }
        try await paused.settle(rebuilds: 1)
        #expect(paused.log.statuses.last == .status(
            icon: "system-users-symbolic", title: "No Enabled Accounts",
            description: "Enable an account in Preferences to see its folders here."
        ))
        #expect(await paused.fixture.callCount(API.FolderList.name) == 0)

        // An enabled account that has not synchronised yet.
        let fresh = try await Harness(accounts: [testAccount("a")], folders: ["a": []])
        defer { Task { await fresh.stop() } }
        try await fresh.settle(rebuilds: 1)
        #expect(fresh.log.statuses.last == .status(
            icon: "folder-symbolic", title: "No Folders Yet",
            description: "Folders appear after the first synchronisation."
        ))
    }

    @Test func accountsChangedReloadsEverything() async throws {
        let (accounts, folders) = testAccounts()
        let h = try await Harness(accounts: accounts, folders: folders)
        defer { Task { await h.stop() } }
        try await h.settle(rebuilds: 1)

        // The banner is up for an account that is then removed.
        h.mailbox.handleAuthRequired(AuthRequiredNotification(accountId: "acc2", reason: .authRequired, message: "x"))
        #expect(h.log.banners.last?.account == "acc2")
        #expect(h.log.banners.last?.title == "No password is stored for two@example.invalid")

        await h.fixture.setAccounts([accounts[0]])
        h.mailbox.handleAccountsChanged()
        try await h.settle(rebuilds: 2)
        #expect(h.log.banners.last?.account == nil, "accountsChanged hides the banner")
        #expect(h.mailbox.model.accounts.count == 1)
        #expect(h.mailbox.model.entries.filter(\.header).isEmpty, "one account: no header")
        #expect(h.mailbox.model.selected == inbox1)
        #expect(h.log.reloads == 1, "the selected folder survived; the list is not reloaded")
        #expect(await h.fixture.callCount(API.AccountList.name) == 2)
        #expect(h.log.accountsLoaded == 2)

        // The selected account switched off: the selection moves to the
        // initial folder of what is left.
        var a2 = accounts[1]
        a2.enabled = true
        await h.fixture.setAccounts([testAccount("acc1", enabled: false, email: "one@example.invalid"), a2])
        h.mailbox.handleAccountsChanged()
        try await h.settle(rebuilds: 3)
        #expect(h.mailbox.model.selected == FolderKey(account: "acc2", folder: "in2"))
        #expect(h.log.reloads == 2)
        #expect(h.log.selections.last == FolderKey(account: "acc2", folder: "in2"))
    }

    @Test func syncFinishedReloadsFoldersAndTheSelectedList() async throws {
        let (accounts, folders) = testAccounts()
        let h = try await Harness(accounts: accounts, folders: folders)
        defer { Task { await h.stop() } }
        try await h.settle(rebuilds: 1)
        let folderLists = await h.fixture.callCount(API.FolderList.name)

        // Syncing → idle for the selected folder: folders and list reload.
        h.mailbox.handleSyncState(SyncState(accountId: "acc1", status: .syncing, folderId: "inbox"))
        #expect(h.sync.footer == .init(text: "Syncing Inbox…", spinning: true))
        #expect(h.log.reloads == 1)
        h.mailbox.handleSyncState(SyncState(accountId: "acc1", status: .idle, folderId: "inbox"))
        try await h.settle(rebuilds: 2)
        #expect(await h.fixture.callCount(API.FolderList.name) == folderLists + 1)
        #expect(h.log.reloads == 2)
        #expect(h.sync.footer.text == "Up to date")

        // Another folder of the account: folders reload, the list does not.
        h.mailbox.handleSyncState(SyncState(accountId: "acc1", status: .syncing, folderId: "zeta"))
        h.mailbox.handleSyncState(SyncState(accountId: "acc1", status: .idle, folderId: "zeta"))
        try await h.settle(rebuilds: 3)
        #expect(h.log.reloads == 2)

        // The whole account: the list reloads too.
        h.mailbox.handleSyncState(SyncState(accountId: "acc1", status: .syncing))
        h.mailbox.handleSyncState(SyncState(accountId: "acc1", status: .idle))
        try await h.settle(rebuilds: 4)
        #expect(h.log.reloads == 3)

        // Another account, or idle without a preceding syncing: nothing.
        h.mailbox.handleSyncState(SyncState(accountId: "acc2", status: .syncing))
        h.mailbox.handleSyncState(SyncState(accountId: "acc2", status: .idle))
        h.mailbox.handleSyncState(SyncState(accountId: "acc1", status: .idle))
        try await h.settle(rebuilds: 5)
        try await Task.sleep(for: .milliseconds(50))
        #expect(h.log.rebuilds == 5)
        #expect(h.log.reloads == 3)

        // The outbox count moved: folders reload and the outbox views refresh.
        h.mailbox.handleSyncState(SyncState(accountId: "acc1", status: .idle, pendingOutbox: 1))
        try await waitUntil { h.log.outboxRefreshes == ["acc1"] }
        #expect(h.log.rebuilds == 6)
        #expect(h.sync.footer == .init(text: "Sending 1 message…", spinning: true))
    }

    @Test func deliveredOutboxMessagesAreToasted() async throws {
        let outbox = testFolder("out", path: "Outbox", role: .outbox, total: 2)
        let inbox = testFolder("in", path: "INBOX", role: .inbox)
        let h = try await Harness(accounts: [testAccount("a")], folders: ["a": [inbox, outbox]])
        defer { Task { await h.stop() } }
        try await h.settle(rebuilds: 1)
        #expect(folderIDs(h.mailbox.model.entries) == ["in", "out"])

        // Both delivered: the outbox empties, its row goes, a toast says so.
        await h.fixture.setFolders([inbox, testFolder("out", path: "Outbox", role: .outbox, total: 0)], for: "a")
        h.mailbox.handleSyncState(SyncState(accountId: "a", status: .idle, pendingOutbox: 0))
        h.mailbox.handleSyncState(SyncState(accountId: "a", status: .idle, pendingOutbox: 2))
        h.mailbox.handleSyncState(SyncState(accountId: "a", status: .idle, pendingOutbox: 0))
        try await waitUntil { h.log.outboxRefreshes.count == 2 }
        #expect(h.log.toasts == ["2 messages sent"])
        #expect(folderIDs(h.mailbox.model.entries) == ["in"])

        // A cancelled send is not a delivery.
        await h.fixture.setFolders([inbox, testFolder("out", path: "Outbox", role: .outbox, total: 1)], for: "a")
        h.mailbox.onOutboxChanged("a")
        try await waitUntil { h.log.outboxRefreshes.count == 3 }
        await h.fixture.setFolders([inbox, testFolder("out", path: "Outbox", role: .outbox, total: 0)], for: "a")
        h.mailbox.noteOutboxCancelled("a")
        h.mailbox.onOutboxChanged("a")
        try await waitUntil { h.log.outboxRefreshes.count == 4 }
        #expect(h.log.toasts == ["2 messages sent"])
    }

    @Test func triggerSyncSendsTheSelectedFolder() async throws {
        let (accounts, folders) = testAccounts()
        let h = try await Harness(accounts: accounts, folders: folders)
        defer { Task { await h.stop() } }
        try await h.settle(rebuilds: 1)

        h.mailbox.triggerSync()
        #expect(h.sync.footer == .init(text: "Checking for new mail…", spinning: true))
        try await waitUntil { h.log.toasts.isEmpty && h.sync.footer.text == "Up to date" }
        #expect(await h.fixture.triggers == [SyncTriggerParams(accountId: "acc1", folderId: "inbox")])

        // Nothing selected: every account.
        await h.fixture.setAccounts([])
        h.mailbox.handleAccountsChanged()
        try await h.settle(rebuilds: 2)
        #expect(h.mailbox.model.selected == nil)
        h.mailbox.triggerSync()
        var tries = 0
        while await h.fixture.triggers.count < 2, tries < 200 {
            tries += 1
            try await Task.sleep(for: .milliseconds(10))
        }
        #expect(await h.fixture.triggers.count == 2)
        #expect(await h.fixture.triggers.last == SyncTriggerParams())

        // A refused trigger: the line is recomputed and a toast explains.
        await h.fixture.fail(API.SyncTrigger.name, with: RPCError(code: .notImplemented, message: "no syncer"))
        h.mailbox.triggerSync()
        #expect(h.sync.footer.spinning)
        try await waitUntil { !h.log.toasts.isEmpty }
        #expect(h.log.toasts == ["Checking for new mail is not available yet"])
        #expect(h.sync.footer == .init(text: "", spinning: false), "no enabled account: empty line")
    }

    @Test func foldsPersistAndFollowExternalChanges() async throws {
        let (accounts, folders) = testAccounts()
        let h = try await Harness(accounts: accounts, folders: folders)
        defer { Task { await h.stop() } }
        try await h.settle(rebuilds: 1)
        let alpha = FolderKey(account: "acc1", folder: "alpha")

        h.mailbox.toggleFolder(alpha)
        #expect(h.scratch.settings.collapsedFolders == ["acc1/alpha"])
        #expect(folderIDs(h.mailbox.model.entries) == ["inbox", "trash", "alpha", "container", "leaf", "zeta", "in2"])
        #expect(h.log.rebuilds == 2, "our own write must not rebuild twice")
        #expect(h.mailbox.model.collapsed.folderCollapsed(alpha))

        h.mailbox.setFolderCollapsed(alpha, true)
        #expect(h.log.rebuilds == 2, "already collapsed: a no-op")
        h.mailbox.setFolderCollapsed(alpha, false)
        #expect(h.log.rebuilds == 3)
        #expect(h.scratch.settings.collapsedFolders.isEmpty)

        h.mailbox.toggleAccount("acc2")
        #expect(h.scratch.settings.collapsedAccounts == ["acc2"])
        #expect(folderIDs(h.mailbox.model.entries).last == "zeta")
        #expect(h.mailbox.model.entries.last?.header == true, "a folded account leaves its header behind")

        // Another window folded something: the sidebar follows.
        h.scratch.settings.collapsedFolders = ["acc1/alpha", "acc1/bogus"]
        h.scratch.settings.collapsedAccounts = []
        #expect(h.mailbox.model.collapsed.folderCollapsed(alpha))
        #expect(!h.mailbox.model.collapsed.accountCollapsed("acc2"))
        #expect(folderIDs(h.mailbox.model.entries) == ["inbox", "trash", "alpha", "container", "leaf", "zeta", "in2"])
        #expect(h.log.rebuilds == 6)

        // Closed: external changes are ignored.
        h.mailbox.close()
        h.scratch.settings.collapsedFolders = []
        #expect(h.log.rebuilds == 6)
    }

    @Test func favouritesPersistAndFollowExternalChanges() async throws {
        let (accounts, folders) = testAccounts()
        let h = try await Harness(accounts: accounts, folders: folders)
        defer { Task { await h.stop() } }
        try await h.settle(rebuilds: 1)
        let in2 = FolderKey(account: "acc2", folder: "in2")

        h.mailbox.toggleFavourite(in2)
        #expect(h.scratch.settings.favouriteFolders == ["acc2/in2"])
        let entries = h.mailbox.model.entries
        #expect(entries[0].header && entries[0].favourite)
        #expect(entries[1].favourite && entries[1].key == in2)
        #expect(entries.last?.starred == true, "the tree row is starred too")
        #expect(h.log.rebuilds == 2)

        // Clicking the row in the Favourites section remembers that side.
        h.mailbox.selectFolder(in2, fav: true)
        #expect(h.mailbox.model.selectedFav)
        #expect(h.log.highlights.last?.key == in2)
        #expect(h.log.highlights.last?.fav == true)
        #expect(h.log.reloads == 2)
        // Re-selecting the listed folder only re-highlights it.
        h.mailbox.selectFolder(in2, fav: false)
        #expect(h.log.reloads == 2)
        #expect(h.log.highlights.last?.fav == false)
        #expect(h.log.selections.count == 2)

        h.scratch.settings.favouriteFolders = ["acc1/inbox", "garbage"]
        #expect(h.mailbox.model.favourites.has(inbox1))
        #expect(!h.mailbox.model.favourites.has(in2))
        #expect(h.mailbox.model.entries[1].key == inbox1)
        #expect(h.mailbox.model.selected == in2, "unpinning is not navigating")
        #expect(h.log.rebuilds == 3)
    }

    @Test func foldingKeepsASelectionOutOfSight() async throws {
        let (accounts, folders) = nestedAccount()
        let h = try await Harness(accounts: accounts, folders: folders)
        defer { Task { await h.stop() } }
        try await h.settle(rebuilds: 1)
        let old = FolderKey(account: "a", folder: "old")
        let work = FolderKey(account: "a", folder: "work")

        h.mailbox.selectFolder(old, fav: false)
        #expect(h.log.reloads == 2)
        #expect(h.mailbox.selectedFolderTitle == "old")
        h.mailbox.toggleFolder(work)
        #expect(folderIDs(h.mailbox.model.entries) == ["in", "work", "zulu"])
        #expect(h.mailbox.model.selected == old, "folding must not move the selection")
        #expect(h.log.reloads == 2, "nor reload the list")
        #expect(h.log.highlights.last?.key == old)
        // The folded row's badge counts what it hides.
        #expect(entryByID(h.mailbox.model.entries, "work")?.badge == 14)

        // The folder gone from the server: the initial folder takes over.
        await h.fixture.setFolders(Array(nestedFolders().prefix(3)), for: "a")
        h.mailbox.loadFolders("a", h.mailbox.model.foldersGen)
        try await h.settle(rebuilds: 3)
        #expect(h.mailbox.model.selected == FolderKey(account: "a", folder: "in"))
        #expect(h.log.reloads == 3)

        // Only containers left: nothing selected, the list cleared.
        await h.fixture.setFolders([testFolder("c", path: "c", selectable: false)], for: "a")
        h.mailbox.loadFolders("a", h.mailbox.model.foldersGen)
        try await h.settle(rebuilds: 4)
        #expect(h.mailbox.model.selected == nil)
        let announced = try #require(h.log.selections.last)
        #expect(announced == nil, "the cleared selection is announced as nil")
        #expect(h.log.reloads == 4)
        #expect(h.mailbox.selectedFolderTitle == "Messages")
    }

    @Test func newMessageAdjustsTheBadgeOnce() async throws {
        let (accounts, folders) = testAccounts()
        let h = try await Harness(accounts: accounts, folders: folders)
        defer { Task { await h.stop() } }
        try await h.settle(rebuilds: 1)
        #expect(entryByID(h.mailbox.model.entries, "inbox")?.badge == 2)

        // For another folder than the listed one: only the counts move, the
        // total always, the badge only for an unseen message.
        let zeta = FolderKey(account: "acc1", folder: "zeta")
        var s = summary("m1")
        h.mailbox.handleNewMessage(NewMessageNotification(accountId: "acc1", folderId: "zeta", message: s))
        #expect(entryByID(h.mailbox.model.entries, "zeta")?.badge == 1)
        #expect(h.mailbox.model.folder(zeta)?.unread == 1)
        #expect(h.mailbox.model.folder(zeta)?.total == 1)
        #expect(h.log.badgeRefreshes == 1)
        #expect(h.log.listInserts.isEmpty)
        h.mailbox.handleNewMessage(NewMessageNotification(accountId: "acc1", folderId: "zeta", message: summary("m2", .seen)))
        #expect(entryByID(h.mailbox.model.entries, "zeta")?.badge == 1)
        #expect(h.mailbox.model.folder(zeta)?.total == 2, "a read message counts in the total")
        #expect(h.log.badgeRefreshes == 2)

        // For the listed folder the list gets it first (with the ids filled
        // in); once it holds the message a second delivery changes nothing.
        h.mailbox.onNewMessageForList = { [unowned h] n in
            h.log.listInserts.append(n)
            h.mailbox.model.insertMessage(at: 0, n.message)
        }
        s = summary("m3")
        h.mailbox.handleNewMessage(NewMessageNotification(accountId: "acc1", folderId: "inbox", message: s))
        #expect(h.log.listInserts.count == 1)
        #expect(h.log.listInserts.last?.message.accountId == "acc1")
        #expect(h.log.listInserts.last?.message.folderId == "inbox")
        #expect(entryByID(h.mailbox.model.entries, "inbox")?.badge == 3)
        h.mailbox.handleNewMessage(NewMessageNotification(accountId: "acc1", folderId: "inbox", message: s))
        #expect(h.log.listInserts.count == 1)
        #expect(entryByID(h.mailbox.model.entries, "inbox")?.badge == 3)
        #expect(h.log.badgeRefreshes == 3)
        #expect(h.mailbox.model.folder(inbox1)?.total == 1)

        // The actions' own adjustments go through the same refresh.
        h.mailbox.adjustCounts(inbox1, -3, 0)
        #expect(entryByID(h.mailbox.model.entries, "inbox")?.badge == 0)
        #expect(h.log.badgeRefreshes == 4)
        #expect(h.mailbox.model.folder(inbox1)?.total == 1)
    }

    @Test func notificationsArriveThroughTheSocket() async throws {
        let (accounts, folders) = testAccounts()
        let h = try await Harness(accounts: accounts, folders: folders)
        defer { Task { await h.stop() } }
        try await h.settle(rebuilds: 1)
        h.pumpNotifications()

        try await h.fixture.push(.syncState(SyncState(accountId: "acc2", status: .syncing, folderId: "in2", progress: 10)))
        try await waitUntil { h.sync.footer.text == "Syncing Inbox… 10 %" }
        try await h.fixture.push(.newMessage(NewMessageNotification(accountId: "acc2", folderId: "in2", message: summary("n1"))))
        try await waitUntil { entryByID(h.mailbox.model.entries, "in2")?.badge == 1 }
        try await h.fixture.push(.authRequired(AuthRequiredNotification(accountId: "acc1", reason: .keyringError, message: "x")))
        try await waitUntil { h.log.banners.count == 1 }
        #expect(h.log.banners.last?.title == "The system keyring is unavailable; one@example.invalid cannot sign in")
        #expect(h.log.banners.last?.button == "Open Preferences")
        try await h.fixture.push(.unknown(method: "notify.future"))
        try await h.fixture.push(.accountsChanged)
        try await h.settle(rebuilds: 2)
        #expect(h.log.banners.last?.account == nil)
        #expect(await h.fixture.callCount(API.AccountList.name) == 2)
    }

    @Test func backendUnavailableDropsLateReplies() async throws {
        let (accounts, folders) = testAccounts()
        let h = try await Harness(accounts: accounts, folders: folders, connect: false)
        defer { Task { await h.stop() } }
        await h.fixture.delay(API.FolderList.name, .milliseconds(200))
        try await h.connect()
        try await waitUntil { h.log.accountsLoaded == 1 }
        let gen = h.mailbox.model.foldersGen
        h.mailbox.model.grouped = true
        h.mailbox.handleConnection(.unavailable("gone"))
        #expect(h.mailbox.model.foldersGen == gen + 1)
        #expect(h.log.collapses == 1)
        #expect(h.sync.line == StatusLine(text: "Backend unavailable", icon: "network-offline-symbolic"))
        try await Task.sleep(for: .milliseconds(400))
        #expect(h.log.rebuilds == 0, "the folder.list replies of the dead connection were dropped")
        #expect(h.mailbox.model.folders.isEmpty)
        #expect(h.log.statuses.last == .status(icon: "", title: "Loading…", description: ""))

        // The reconnect loads again, as the GTK window does on Connected; a
        // failed system.info still loads.
        await h.fixture.delay(API.FolderList.name, .zero)
        h.mailbox.handleConnection(.infoFailed("x"))
        try await h.settle(rebuilds: 1)
        #expect(h.mailbox.model.selected == inbox1)
    }

    /// A daemon of another protocol version is no connection (the handshake
    /// refused it): nothing is loaded, late replies are dropped as when the
    /// backend went away, and the line names the mismatch without being a
    /// button. The client is connected here all the same, so a load would
    /// reach the daemon.
    @Test func protocolMismatchLoadsNothing() async throws {
        let (accounts, folders) = testAccounts()
        let h = try await Harness(accounts: accounts, folders: folders, connect: false)
        defer { Task { await h.stop() } }
        try await h.client.connect()
        let gen = h.mailbox.model.foldersGen
        h.mailbox.model.grouped = true
        h.mailbox.handleConnection(.protocolMismatch(daemon: 1))
        #expect(h.mailbox.model.foldersGen == gen + 1)
        #expect(h.log.collapses == 1)
        #expect(h.sync.line == StatusLine(text: "Protocol mismatch: UI \(API.protocolVersion), backend 1"))
        try await Task.sleep(for: .milliseconds(200))
        #expect(await h.fixture.callCount(API.AccountList.name) == 0, "account.list is not asked")
        #expect(await h.fixture.callCount(API.SyncStatus.name) == 0, "sync.status is not asked")
        #expect(h.log.accountsLoaded == 0 && h.log.rebuilds == 0)
    }

    /// window.go `refreshListTitle`: the selected folder's name over its
    /// counts, from every place the selection or the cached counts change
    /// (selectFolder in both branches, updateFolderRow, rebuildFolderList).
    @Test func listTitleFollowsTheSelectionAndTheCounts() async throws {
        let inbox = testFolder("in", path: "INBOX", role: .inbox, unread: 2, total: 10)
        let archive = testFolder("arch", path: "Archive", role: .archive, total: 3)
        let outbox = testFolder("out", path: "Outbox", role: .outbox, total: 1)
        let h = try await Harness(accounts: [testAccount("a")], folders: ["a": [inbox, archive, outbox]])
        defer { Task { await h.stop() } }
        try await h.settle(rebuilds: 1)
        #expect(h.log.titles.last == ["Inbox", "2 unread of 10"])
        #expect(h.mailbox.selectedFolderSubtitle == "2 unread of 10")

        let arch = FolderKey(account: "a", folder: "arch")
        h.mailbox.selectFolder(arch, fav: false)
        #expect(h.log.titles.last == ["Archive", "3 messages"])
        // Selecting it again only re-highlights, and says the title again.
        let said = h.log.titles.count
        h.mailbox.selectFolder(arch, fav: false)
        #expect(h.log.titles.count == said + 1)
        #expect(h.log.titles.last == ["Archive", "3 messages"])

        // A new message, and the actions' bookkeeping.
        h.mailbox.handleNewMessage(NewMessageNotification(accountId: "a", folderId: "arch", message: summary("n1")))
        #expect(h.log.titles.last == ["Archive", "1 unread of 4"])
        h.mailbox.moveCounts(arch, FolderKey(account: "a", folder: "in"), 1, 2)
        #expect(h.log.titles.last == ["Archive", "2 messages"])
        #expect(h.mailbox.model.folder(FolderKey(account: "a", folder: "in"))?.total == 12)

        // The outbox counts what is to be sent.
        h.mailbox.selectFolder(FolderKey(account: "a", folder: "out"), fav: false)
        #expect(h.log.titles.last == ["Outbox", "1 message"])

        // Nothing selected any more: "Messages" and no counts.
        await h.fixture.setAccounts([])
        h.mailbox.handleAccountsChanged()
        try await h.settle(rebuilds: 2)
        #expect(h.log.titles.last == ["Messages", ""])
        #expect(h.mailbox.selectedFolderSubtitle == "")
    }

    /// sync.go `applySyncState`: a change of failedOutbox reloads the
    /// outbox as a change of pendingOutbox does; an account's first state
    /// is no move (its folders came with the accounts).
    @Test func failedOutboxChangesReloadTheOutbox() async throws {
        let inbox = testFolder("in", path: "INBOX", role: .inbox)
        let outbox = testFolder("out", path: "Outbox", role: .outbox, total: 1)
        let h = try await Harness(accounts: [testAccount("a")], folders: ["a": [inbox, outbox]])
        defer { Task { await h.stop() } }
        try await h.settle(rebuilds: 1)

        h.mailbox.handleSyncState(SyncState(accountId: "a", status: .idle, failedOutbox: 1))
        #expect(h.sync.footer == .init(text: "1 message not sent", spinning: false))
        try await Task.sleep(for: .milliseconds(50))
        #expect(h.log.outboxRefreshes.isEmpty, "the first state is no move")

        h.mailbox.handleSyncState(SyncState(accountId: "a", status: .idle, failedOutbox: 2))
        try await waitUntil { h.log.outboxRefreshes == ["a"] }
        #expect(h.sync.footer.text == "2 messages not sent")

        // Unchanged counts: nothing.
        h.mailbox.handleSyncState(SyncState(accountId: "a", status: .syncing, failedOutbox: 2))
        try await Task.sleep(for: .milliseconds(50))
        #expect(h.log.outboxRefreshes == ["a"])
        // A failed message dropped: pending stays 0, failed moves.
        h.mailbox.handleSyncState(SyncState(accountId: "a", status: .syncing, failedOutbox: 0))
        try await waitUntil { h.log.outboxRefreshes == ["a", "a"] }
    }

    /// sync.go `triggerAccountSync`: Check and Try Again in the status
    /// popover ask for one whole account.
    @Test func triggerSyncForOneAccount() async throws {
        let (accounts, folders) = testAccounts()
        let h = try await Harness(accounts: accounts, folders: folders)
        defer { Task { await h.stop() } }
        try await h.settle(rebuilds: 1)

        h.mailbox.triggerSync(accountId: "acc2")
        #expect(h.sync.footer == .init(text: "Checking for new mail…", spinning: true))
        var tries = 0
        while await h.fixture.triggers.isEmpty, tries < 200 {
            tries += 1
            try await Task.sleep(for: .milliseconds(10))
        }
        #expect(await h.fixture.triggers == [SyncTriggerParams(accountId: "acc2")])
    }

    /// status.go `showOutbox`: the popover's link to unsent messages
    /// selects the account's outbox like a click on its tree row; a paused
    /// account's outbox, or a missing one, is not shown.
    @Test func showOutboxSelectsTheOutbox() async throws {
        let inbox = testFolder("in", path: "INBOX", role: .inbox)
        let outbox = testFolder("out", path: "Outbox", role: .outbox, total: 1)
        let accounts = [testAccount("a"), testAccount("p", enabled: false), testAccount("n")]
        let h = try await Harness(
            accounts: accounts, folders: ["a": [inbox, outbox], "p": [outbox], "n": [testFolder("in2", path: "INBOX", role: .inbox)]])
        defer { Task { await h.stop() } }
        try await h.settle(rebuilds: 1)
        #expect(h.mailbox.model.selected == FolderKey(account: "a", folder: "in"))
        h.mailbox.selectFolder(FolderKey(account: "a", folder: "in"), fav: true)

        #expect(h.mailbox.showOutbox("a"))
        #expect(h.mailbox.model.selected == FolderKey(account: "a", folder: "out"))
        #expect(!h.mailbox.model.selectedFav, "the tree's row, not a pinned one")
        #expect(h.log.reloads == 2)

        #expect(!h.mailbox.showOutbox("p"), "paused")
        #expect(!h.mailbox.showOutbox("n"), "no outbox")
        #expect(!h.mailbox.showOutbox("zzz"), "unknown account")
        #expect(h.mailbox.model.selected == FolderKey(account: "a", folder: "out"))
    }
}
