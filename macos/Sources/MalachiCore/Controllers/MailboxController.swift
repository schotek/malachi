// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os

/// What the sidebar shows in place of the folder list while there is
/// nothing to list (window.blp `folder_stack`): the list itself, or a status
/// page. Icons are GTK names; the AppKit layer maps them to symbols. An
/// empty icon means none (the "Loading…" page).
public enum SidebarStatus: Equatable, Sendable {
    case folders
    case status(icon: String, title: String, description: String)
}

/// The folder half of the GTK main window (ui/internal/window/folders.go,
/// collapse.go, favourites.go, and the sidebar parts of notify.go and
/// window.go): accounts and folders, the sidebar entries, the selection,
/// folds and pins, and the reactions to sync and new-message events that
/// touch the sidebar. No AppKit: the views subscribe to the callbacks and
/// send the user's actions back through the methods.
///
/// The message list half is `ListController` (`MailboxController+List.swift`),
/// which installs itself into the list hooks below (`reloadMessages` and
/// the others).
///
/// Every RPC runs as a `Task` on the main actor, so the continuation after
/// the call is on the main actor too and the generation checks race with
/// nothing (the GTK window's `glib.IdleAdd` discipline).
@MainActor
public final class MailboxController {
    public let client: RPCClient
    public let settings: Settings
    public let sync: SyncController

    /// The view model; the list extension mutates its message fields.
    public internal(set) var model: MailModel
    /// account.list returned at least one account (window.go `hasAccounts`);
    /// false until the first answer. The message pane's No Accounts
    /// placeholder keys off it.
    public private(set) var hasAccounts = false
    /// What the sidebar shows right now.
    public private(set) var sidebarStatus: SidebarStatus = .folders

    // MARK: Callbacks (sidebar)

    /// `model.entries` were rebuilt: reload the sidebar rows.
    public var onEntriesChanged: (@MainActor () -> Void)?
    /// Only the badges of `model.entries` moved (folders.go
    /// `updateFolderRow`); falls back to `onEntriesChanged` when unset.
    public var onBadgesChanged: (@MainActor () -> Void)?
    /// Switch between the folder list and a status page.
    public var onFolderStatus: (@MainActor (SidebarStatus) -> Void)?
    /// Highlight the selected folder's row (nil: clear the highlight);
    /// the flag says which of a pinned folder's two rows the user clicked
    /// last (folders.go `highlightFolderRow`). Called on every
    /// `selectFolder`, including a re-entry for the already selected folder.
    public var onSelectionChanged: (@MainActor (FolderKey?, Bool) -> Void)?

    // MARK: Callbacks (the rest of the window)

    /// The selected folder changed (nil: nothing selected any more), with
    /// the Favourites flag.
    public var onFolderSelected: (@MainActor (FolderKey?, Bool) -> Void)?
    /// The title over the message list and the counts under it, the
    /// window's title and subtitle here (window.go `refreshListTitle`):
    /// called wherever the selection or the cached counts change, with
    /// `selectedFolderTitle` and `selectedFolderSubtitle`.
    public var onListTitleChanged: (@MainActor (String, String) -> Void)?
    /// account.list answered.
    public var onAccountsLoaded: (@MainActor ([Account]) -> Void)?
    /// The list's `loadMessages` (messages.go), installed by the list
    /// extension. Called whenever the GTK window calls `loadMessages` from
    /// the folder code: a new selection, a cleared one, a finished sync of
    /// the selected folder.
    public var reloadMessages: (@MainActor () -> Void)?
    /// A notified message for the listed folder that the model does not
    /// hold yet: the list inserts it (folders.go `onNewMessage`, the list
    /// half). The notification's summary carries its account and folder ids
    /// filled in.
    public var onNewMessageForList: (@MainActor (NewMessageNotification) -> Void)?
    /// The account's outbox contents changed and its folder list was
    /// reloaded: refresh the views showing the outbox (outbox.go
    /// `refreshOutboxViews`).
    public var refreshOutboxViews: (@MainActor (AccountID) -> Void)?
    /// The backend went away: fold a conversation waiting for its members
    /// back (window.go `showConnectionState`, `collapseLoading` + `syncRows`).
    public var collapseLoading: (@MainActor () -> Void)?

    let toast: @MainActor (String) -> Void
    let log = Logger(subsystem: "io.github.schotek.Malachi", category: "mailbox")

    private var outbox = OutboxTracker()
    private var savingCollapse = false
    private var savingFavourites = false
    private var settingsTokens: [Settings.ChangeToken] = []
    private var closed = false

    /// - Parameters:
    ///   - client: the transport; calls fail with `notConnected` until the
    ///     connection controller reports a connection.
    ///   - settings: where folds and pins persist.
    ///   - sync: the sync footer and banner state; one is created when
    ///     none is given.
    ///   - toast: shows a transient message (the window's toast overlay).
    public init(client: RPCClient, settings: Settings, sync: SyncController = SyncController(), toast: @escaping @MainActor (String) -> Void) {
        self.client = client
        self.settings = settings
        self.sync = sync
        self.toast = toast
        model = MailModel(collapsed: CollapseState.load(from: settings), favourites: FavouriteState.load(from: settings))
        // The folded-away parts of the sidebar and the pinned folders follow
        // along when another window (or `defaults write`) changes them.
        settingsTokens.append(settings.onChange(.collapsedFolders) { [weak self] in self?.onCollapseChanged() })
        settingsTokens.append(settings.onChange(.collapsedAccounts) { [weak self] in self?.onCollapseChanged() })
        settingsTokens.append(settings.onChange(.favouriteFolders) { [weak self] in self?.onFavouritesChanged() })
        sync.accounts = { [weak self] in self?.model.accounts ?? [] }
        sync.folderName = { [weak self] acc, id in
            guard let self, let f = self.model.folder(FolderKey(account: acc, folder: id)) else { return "" }
            return folderTitle(f)
        }
    }

    /// Detaches from the settings and drops every late reply (the window
    /// closed).
    public func close() {
        closed = true
        for t in settingsTokens {
            t.cancel()
        }
        settingsTokens = []
        sync.close()
    }

    // MARK: RPC plumbing

    /// Runs one call and hands the outcome back on the main actor, unless
    /// the controller closed meanwhile. `timeout` nil takes the method's
    /// own (5 s for everything the sidebar asks).
    func perform<M: RPCMethod>(
        _ method: M.Type, _ params: M.Params, timeout: Duration? = nil,
        _ done: @escaping @MainActor (Result<M.Result, any Error>) -> Void
    ) {
        let client = client
        Task { [weak self] in
            let outcome: Result<M.Result, any Error>
            do {
                outcome = .success(try await client.call(M.self, params, timeout: timeout))
            } catch {
                outcome = .failure(error)
            }
            guard let self, !self.closed else { return }
            done(outcome)
        }
    }

    /// The window's `callThen` (actions.go): on failure the error is logged,
    /// toasted as `rpcErrorText(what, err)` and handed to `onError`; on
    /// success `onOK` runs with the result. Either callback may be nil.
    func call<M: RPCMethod>(
        _ method: M.Type, _ params: M.Params, what: String, timeout: Duration? = nil,
        onError: (@MainActor (any Error) -> Void)? = nil, onOK: (@MainActor (M.Result) -> Void)? = nil
    ) {
        perform(method, params, timeout: timeout) { [weak self] outcome in
            guard let self else { return }
            switch outcome {
            case .success(let res):
                onOK?(res)
            case .failure(let err):
                self.log.warning("\(M.name, privacy: .public): \(String(describing: err), privacy: .public)")
                self.toast(rpcErrorText(what, err))
                onError?(err)
            }
        }
    }

    // MARK: Accounts and folders

    /// Runs account.list, then folder.list for every enabled account,
    /// rebuilds the sidebar and selects the initial folder (folders.go
    /// `loadAccounts`). Every reply is guarded by the folders generation, so
    /// a reconnect or a repeated call in the meantime simply wins.
    public func loadAccounts() {
        let gen = model.bumpFolders()
        if model.entries.isEmpty {
            showFolderStatus("", L10n.T("Loading…"), "")
        }
        perform(API.AccountList.self, EmptyParams()) { [weak self] outcome in
            guard let self, gen == self.model.foldersGen else { return }
            switch outcome {
            case .failure(let err):
                self.log.warning("account.list: \(String(describing: err), privacy: .public)")
                if self.model.entries.isEmpty {
                    self.showFolderStatus(
                        "dialog-warning-symbolic", L10n.T("Folders Unavailable"),
                        rpcErrorText(L10n.T("Loading folders"), err)
                    )
                }
            case .success(let res):
                self.model.accounts = res.accounts
                self.model.folders = [:]
                self.model.folderErr = [:]
                self.hasAccounts = !res.accounts.isEmpty
                self.onAccountsLoaded?(res.accounts)
                // sync.status may have answered before the accounts were
                // known, and the status line and its popover follow the
                // account set (added, removed, paused, renamed).
                self.sync.refreshFooter()

                let enabled = self.model.enabledAccounts
                if enabled.isEmpty {
                    self.rebuildFolderList()
                    return
                }
                // One rebuild once every account answered (or failed).
                let pending = Countdown(enabled.count)
                for a in enabled {
                    self.fetchFolders(a.id, gen) { [weak self] in
                        if pending.tick() {
                            self?.rebuildFolderList()
                        }
                    }
                }
            }
        }
    }

    /// Runs folder.list for one account and rebuilds the sidebar from the
    /// answer (folders.go `loadFolders`); `gen` guards the reply.
    public func loadFolders(_ acc: AccountID, _ gen: UInt64) {
        fetchFolders(acc, gen) { [weak self] in self?.rebuildFolderList() }
    }

    /// Runs folder.list for `acc` and stores the result (or the error) in the
    /// model, then calls `done` (folders.go `fetchFolders`). A reply from a
    /// stale generation is dropped without calling `done`.
    func fetchFolders(_ acc: AccountID, _ gen: UInt64, _ done: @escaping @MainActor () -> Void) {
        perform(API.FolderList.self, FolderListParams(accountId: acc)) { [weak self] outcome in
            guard let self, gen == self.model.foldersGen else { return }
            switch outcome {
            case .failure(let err):
                self.log.warning("folder.list \(acc.rawValue, privacy: .public): \(String(describing: err), privacy: .public)")
                // Keep the last good list, if any, rather than emptying the
                // sidebar on a transient failure.
                self.model.folderErr[acc] = err
            case .success(let res):
                self.model.folders[acc] = res.folders
                self.model.folderErr[acc] = nil
                self.trackOutbox(acc)
            }
            done()
        }
    }

    /// The bookkeeping after every folder.list of `acc` (outbox.go
    /// `trackOutbox`): a shrink of the outbox that the user did not cause by
    /// cancelling means messages were delivered, which gets a toast.
    private func trackOutbox(_ acc: AccountID) {
        let total = model.folderByRole(acc, .outbox)?.total ?? 0
        let sent = outbox.track(acc, total: total)
        if sent > 0 {
            // TRANSLATORS: toast after delivery; %d is the number of messages.
            toast(L10n.N("%d message sent", "%d messages sent", sent))
        }
    }

    /// Notes that the user removed one outbox message of `acc` (cancel
    /// sending), so the next shrink of the outbox is not toasted as a
    /// delivery (outbox.go `cancelSendFrom`). The actions call it before
    /// message.delete: removing a queued or failed message moves
    /// pendingOutbox or failedOutbox, and the notify.syncState that follows
    /// reloads the folders, possibly before the reply arrives.
    public func noteOutboxCancelled(_ acc: AccountID) {
        outbox.noteCancelled(acc)
    }

    /// Takes a `noteOutboxCancelled` back after the daemon refused the
    /// removal (outbox.go `cancelSendFrom`'s error path); a folder reload in
    /// between may have used it up already.
    public func noteOutboxCancelFailed(_ acc: AccountID) {
        outbox.noteCancelFailed(acc)
    }

    /// Recreates the sidebar entries from the model (folders.go
    /// `rebuildFolderList`). The selection is preserved when its folder still
    /// exists, otherwise the initial folder is selected. A folder hidden
    /// under a fold still exists: folding must not move the selection or
    /// reload the message list, so existence is decided against the model
    /// (`folderListed`) and not against the rows on screen.
    public func rebuildFolderList() {
        // The list title follows on every way out: the reload that led here
        // may have brought new counts, or taken the selected folder away.
        defer { refreshListTitle() }
        model.rebuildEntries()
        onEntriesChanged?()

        // Entries, not rows: an account folded shut leaves its header behind
        // and the sidebar is not empty.
        if model.entries.isEmpty {
            showEmptySidebarStatus()
            // Nothing to show; a folder selected earlier is gone with its rows.
            if model.selected != nil {
                clearSelection()
            }
            return
        }
        setSidebarStatus(.folders)

        if let sel = model.selected, model.folderListed(sel) {
            // Highlights the row when there is one, and does nothing beyond
            // that while the folder is folded out of sight.
            select(sel)
            return
        }
        if let k = model.initialFolder() {
            select(k)
            return
        }
        // Only non-selectable containers: clear the list.
        clearSelection()
    }

    /// Explains an empty sidebar (folders.go `showEmptySidebarStatus`): no
    /// account, none enabled, an error, or accounts that have not
    /// synchronised yet.
    public func showEmptySidebarStatus() {
        let enabled = model.enabledAccounts
        if !hasAccounts {
            showFolderStatus(
                "system-users-symbolic", L10n.T("No Accounts"),
                L10n.T("Add a mail account in Preferences to see its folders here.")
            )
            return
        }
        if enabled.isEmpty {
            showFolderStatus(
                "system-users-symbolic", L10n.T("No Enabled Accounts"),
                L10n.T("Enable an account in Preferences to see its folders here.")
            )
            return
        }
        for a in enabled {
            if let err = model.folderErr[a.id] {
                showFolderStatus(
                    "dialog-warning-symbolic", L10n.T("Folders Unavailable"),
                    rpcErrorText(L10n.T("Loading folders"), err)
                )
                return
            }
        }
        showFolderStatus(
            "folder-symbolic", L10n.T("No Folders Yet"),
            L10n.T("Folders appear after the first synchronisation.")
        )
    }

    /// Switches the sidebar to a status page (folders.go `showFolderStatus`).
    /// Everything is plain text; error texts carry a technical detail from
    /// the backend and nothing shown to the user is interpreted as markup.
    private func showFolderStatus(_ icon: String, _ title: String, _ description: String) {
        setSidebarStatus(.status(icon: icon, title: title, description: description))
    }

    private func setSidebarStatus(_ s: SidebarStatus) {
        sidebarStatus = s
        onFolderStatus?(s)
    }

    // MARK: Selection

    /// The user chose a folder row (window.go, the row-selected handler):
    /// remembers which of a pinned folder's two rows was clicked, so the
    /// highlight stays on it across rebuilds, and makes the folder current.
    public func selectFolder(_ k: FolderKey, fav: Bool) {
        model.selectedFav = fav
        select(k)
    }

    /// Makes `k` the current folder (folders.go `selectFolder`): highlights
    /// its row, announces it and loads its messages. Idempotent for the
    /// already selected and listed folder, which only re-highlights the row
    /// and refreshes the title, whose counts a folder reload may have
    /// changed.
    private func select(_ k: FolderKey) {
        if k == model.selected, k == model.listFolder {
            onSelectionChanged?(k, model.selectedFav)
            refreshListTitle()
            return
        }
        model.selected = k
        refreshListTitle()
        onFolderSelected?(k, model.selectedFav)
        onSelectionChanged?(k, model.selectedFav)
        requestReloadMessages()
    }

    /// Nothing selected any more: clears the highlight and the list.
    private func clearSelection() {
        model.selected = nil
        onFolderSelected?(nil, model.selectedFav)
        onSelectionChanged?(nil, model.selectedFav)
        requestReloadMessages()
    }

    /// The window's `loadMessages`: the list half's hook.
    private func requestReloadMessages() {
        reloadMessages?()
    }

    /// The title of the list page for the selected folder (window.go
    /// `refreshListTitle`): the folder's display name, or "Messages" when
    /// none is selected. The window title follows it (the plan's D1).
    public var selectedFolderTitle: String {
        if let k = model.selected, let f = model.folder(k) {
            return folderTitle(f)
        }
        return L10n.T("Messages")
    }

    /// The counts under the title (window.go `refreshListTitle`,
    /// `folderCountsText`): "" while no folder is selected.
    public var selectedFolderSubtitle: String {
        if let k = model.selected, let f = model.folder(k) {
            return folderCountsText(f)
        }
        return ""
    }

    /// Announces the title and the counts of the selected folder (window.go
    /// `refreshListTitle`). Called wherever the selection or the cached
    /// counts change: `select`, `updateFolderRow` and `rebuildFolderList`.
    func refreshListTitle() {
        onListTitleChanged?(selectedFolderTitle, selectedFolderSubtitle)
    }

    // MARK: Folds and pins

    /// Folds a folder's children away, or brings them back, and persists the
    /// change (collapse.go `toggleFolder`). The selection and the message
    /// list are left alone on purpose: folding is a way of looking at the
    /// sidebar, not a way of navigating.
    public func toggleFolder(_ k: FolderKey) {
        model.collapsed.toggleFolder(k)
        saveCollapse()
        rebuildFolderList()
    }

    /// Folds a whole account's tree away, or brings it back (collapse.go
    /// `toggleAccount`).
    public func toggleAccount(_ id: AccountID) {
        model.collapsed.toggleAccount(id)
        saveCollapse()
        rebuildFolderList()
    }

    /// `toggleFolder` for a view that knows the target state (a native
    /// outline reports "did expand" / "did collapse"): a no-op when the model
    /// already agrees.
    public func setFolderCollapsed(_ k: FolderKey, _ collapsed: Bool) {
        guard model.collapsed.folderCollapsed(k) != collapsed else { return }
        toggleFolder(k)
    }

    /// `toggleAccount` for a view that knows the target state.
    public func setAccountCollapsed(_ id: AccountID, _ collapsed: Bool) {
        guard model.collapsed.accountCollapsed(id) != collapsed else { return }
        toggleAccount(id)
    }

    /// Pins a folder or unpins it, persists the change and redraws the
    /// sidebar at once (favourites.go `toggleFavourite`). The selection and
    /// the message list are left alone: pinning is a way of arranging the
    /// sidebar, not of navigating.
    public func toggleFavourite(_ k: FolderKey) {
        model.favourites.toggle(k)
        saveFavourites()
        rebuildFolderList()
    }

    /// Writes the folds out without reacting to the change notification it
    /// causes in this window (collapse.go `saveCollapse`).
    private func saveCollapse() {
        savingCollapse = true
        model.collapsed.save(to: settings, accounts: model.accounts)
        savingCollapse = false
    }

    /// Re-reads the folds after another window (or another process sharing
    /// the profile) folded something (collapse.go `onCollapseChanged`).
    private func onCollapseChanged() {
        if savingCollapse {
            return
        }
        model.collapsed = CollapseState.load(from: settings)
        rebuildFolderList()
    }

    private func saveFavourites() {
        savingFavourites = true
        model.favourites.save(to: settings, accounts: model.accounts)
        savingFavourites = false
    }

    private func onFavouritesChanged() {
        if savingFavourites {
            return
        }
        model.favourites = FavouriteState.load(from: settings)
        rebuildFolderList()
    }

    // MARK: Counts

    /// Changes the cached unread and total counts of a folder and refreshes
    /// the badges and the title (the mark-read / mark-unread bookkeeping of
    /// actions.go `setSeenIDs`).
    public func adjustCounts(_ k: FolderKey, _ dUnread: Int, _ dTotal: Int) {
        model.adjustCounts(k, dUnread, dTotal)
        updateFolderRow(k)
    }

    /// Shifts the cached counts for messages leaving `src` for `target`
    /// (nil: leaving the store; `MailModel.moveCounts`) and refreshes the
    /// badges and the title (actions.go `trackMoves`' shift).
    public func moveCounts(_ src: FolderKey, _ target: FolderKey?, _ unread: Int, _ n: Int) {
        model.moveCounts(src, target, unread, n)
        updateFolderRow(src) // refreshes every row, the target's too
    }

    /// Refreshes the unread badges and the list title's counts after the
    /// counts of `k` moved (folders.go `updateFolderRow`). Every row is
    /// refreshed, not just k's: a collapsed ancestor's badge counts the
    /// folders it hides, k itself may be one of them, and a pinned folder
    /// has a second row in the Favourites section.
    public func updateFolderRow(_ k: FolderKey) {
        if let onBadgesChanged {
            onBadgesChanged()
        } else {
            onEntriesChanged?()
        }
        refreshListTitle()
    }

    // MARK: Notifications and connection

    /// Dispatches a decoded daemon notification to the handlers below
    /// (notify.go `handleNotification`, the sidebar's share). The desktop
    /// notification of a new message and the compose windows' account
    /// refresh belong to other components the app wires alongside.
    public func handleNotification(_ n: DaemonNotification) {
        switch n {
        case .newMessage(let m):
            handleNewMessage(m)
        case .syncState(let s):
            handleSyncState(s)
        case .authRequired(let a):
            handleAuthRequired(a)
        case .accountsChanged:
            handleAccountsChanged()
        case .unknown(let method):
            log.info("notification \(method, privacy: .public)")
        }
    }

    /// The account set changed under us (notify.accountsChanged): the
    /// banner's account may be gone or edited; a still-failing account is
    /// announced again by the daemon once its syncer restarts.
    public func handleAccountsChanged() {
        sync.hideAuthBanner()
        loadAccounts()
    }

    /// notify.authRequired: reveals the sign-in banner for the account.
    public func handleAuthRequired(_ n: AuthRequiredNotification) {
        sync.showAuthRequired(n, account: model.account(n.accountId))
    }

    /// notify.newMessage, the sidebar's share (folders.go `onNewMessage`):
    /// the folder's counts move, the total always, the unread count for an
    /// unseen message; a message for the listed folder goes to the list
    /// through `onNewMessageForList` first, unless the list holds it already
    /// (delivered twice: the counts were adjusted the first time).
    public func handleNewMessage(_ n: NewMessageNotification) {
        let k = FolderKey(account: n.accountId, folder: n.folderId)
        var s = n.message
        if s.accountId.rawValue.isEmpty {
            s.accountId = n.accountId
        }
        if s.folderId.rawValue.isEmpty {
            s.folderId = n.folderId
        }
        if k == model.listFolder {
            if model.message(s.id) != nil {
                return
            }
            onNewMessageForList?(NewMessageNotification(accountId: n.accountId, folderId: n.folderId, message: s))
        }
        let unread = hasFlag(s.flags, .seen) ? 0 : 1
        model.adjustCounts(k, unread, 1)
        updateFolderRow(k)
    }

    /// notify.syncState and every state of sync.status (sync.go
    /// `applySyncState`): records the state, then reloads folders when the
    /// account left the syncing state and refreshes the outbox views when
    /// its number of pending or failed outgoing messages moved. An account's
    /// first state is no move: its folders are loaded with the accounts.
    public func handleSyncState(_ s: SyncState) {
        let (prev, cur) = sync.apply(s)
        onSyncFinished(prev, cur)
        if let prev, prev.pendingOutbox != cur.pendingOutbox || prev.failedOutbox != cur.failedOutbox {
            onOutboxChanged(cur.accountId)
        }
    }

    /// Runs when an account leaves the syncing state (folders.go
    /// `onSyncFinished`): folders are reloaded and, when the synced folder is
    /// the selected one (or the whole account was synced), the list.
    func onSyncFinished(_ prev: SyncState?, _ cur: SyncState) {
        guard let prev, prev.status == .syncing, cur.status != .syncing else { return }
        loadFolders(cur.accountId, model.foldersGen)
        if let sel = model.selected, sel.account == cur.accountId, cur.folderId == nil || cur.folderId == sel.folder {
            reloadMessages?()
        }
    }

    /// Runs when the account's number of pending or failed outgoing
    /// messages moved (folders.go `onOutboxChanged`): the folders are
    /// reloaded, so the outbox row appears or goes with its contents, and
    /// the views showing the outbox are refreshed. When the selected folder
    /// was the outbox and it emptied, `rebuildFolderList` falls back to the
    /// initial folder.
    public func onOutboxChanged(_ acc: AccountID) {
        fetchFolders(acc, model.foldersGen) { [weak self] in
            guard let self else { return }
            self.rebuildFolderList()
            self.refreshOutboxViews?(acc)
        }
    }

    /// Runs sync.trigger for the selected folder, or for every account when
    /// nothing is selected (sync.go `triggerSync`; win.refresh).
    public func triggerSync() {
        var params = SyncTriggerParams()
        if let sel = model.selected {
            params.accountId = sel.account
            params.folderId = sel.folder
        }
        startSync(params)
    }

    /// Runs sync.trigger for one whole account (sync.go
    /// `triggerAccountSync`: Check and Try Again in the status popover).
    public func triggerSync(accountId: AccountID) {
        startSync(SyncTriggerParams(accountId: accountId))
    }

    /// Runs sync.trigger with `params` (sync.go `startSync`). The line's
    /// spinner starts at once; the daemon's notify.syncState takes over,
    /// with the sync controller's fallback timer so the spinner never
    /// sticks.
    private func startSync(_ params: SyncTriggerParams) {
        sync.beginChecking()
        perform(API.SyncTrigger.self, params) { [weak self] outcome in
            guard let self, case .failure(let err) = outcome else { return }
            self.log.debug("sync.trigger: \(String(describing: err), privacy: .public)")
            self.sync.refreshFooter()
            self.toast(rpcErrorText(L10n.T("Checking for new mail"), err))
        }
    }

    /// Runs sync.status and applies every state (sync.go `loadSyncStatus`).
    public func loadSyncStatus() {
        sync.loadSyncStatus(client: client) { [weak self] s in self?.handleSyncState(s) }
    }

    /// Selects the account's outbox, as a click on its row in the account's
    /// tree would (status.go `showOutbox`: the status popover's link to
    /// unsent messages). False when the outbox cannot be shown: the account
    /// is paused or has no outbox folder (`MailModel.outboxKey`).
    @discardableResult
    public func showOutbox(_ acc: AccountID) -> Bool {
        guard let k = model.outboxKey(acc) else { return false }
        selectFolder(k, fav: false)
        return true
    }

    /// The connection state changed (window.go `showConnectionState`, the
    /// data side): the status line learns it first (it names the connection
    /// while there is none, and forgets what sync.status said); on a
    /// connection the accounts and the sync states are loaded; when the
    /// backend went away every in-flight reply is dropped and what is shown
    /// stays until the reconnect reloads it. A connection with a failed
    /// system.info still loads, as the GTK window does (it loads on the
    /// socket, not on the answer). A daemon of another protocol version is
    /// no connection at all (the handshake refused it): nothing loads, as
    /// when the backend went away.
    public func handleConnection(_ state: ConnectionController.ConnectionState) {
        sync.setConnection(state)
        switch state {
        case .connected, .infoFailed:
            loadAccounts()
            loadSyncStatus()
        case .unavailable, .protocolMismatch, .stopping:
            model.bumpAll()
            if model.grouped {
                collapseLoading?()
            }
        case .connecting:
            break
        }
    }
}

/// Counts the answers of a fan-out down to zero (folders.go `loadAccounts`'s
/// `pending`). A class so the closures of the fan-out share it.
@MainActor
private final class Countdown {
    private var remaining: Int

    init(_ n: Int) {
        remaining = n
    }

    /// Reports whether this was the last answer.
    func tick() -> Bool {
        remaining -= 1
        return remaining == 0
    }
}
