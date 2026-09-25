// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os

/// What the list pane shows (window.blp `list_stack`): the rows with their
/// Load More footer, or a status page for the times there are none (nothing
/// selected, loading, empty, an error with a Try Again button). Icons are
/// GTK names; the AppKit layer maps them to symbols. An empty icon means
/// none (the "Loading…" page).
public enum ListState: Equatable, Sendable {
    case messages
    case status(icon: String, title: String, description: String, retry: Bool)
}

/// The Load More footer under the rows (messages.go `showLoadMore`): the
/// button while a further page exists, the spinner while it is fetched.
public struct LoadMoreState: Equatable, Sendable {
    public var spinner: Bool
    public var button: Bool

    public init(spinner: Bool = false, button: Bool = false) {
        self.spinner = spinner
        self.button = button
    }
}

/// How the selection was reconciled with a new set of rows (threads.go
/// `reconcileRows`), handed to the view with the rows so it can mirror the
/// controller's `selectedKey` afterwards.
///
/// - `keep`: the same key stays selected; when a member row folded away its
///   conversation row takes over; otherwise the selection is dropped.
/// - `neighbour`: as `keep`, but a selected row that is gone hands the
///   selection to the row now at its place (a removal).
/// - `clear`: the list was emptied for another folder, mode or filter; the
///   selection is dropped without looking.
public enum SelectionHint: Equatable, Sendable {
    case keep, neighbour, clear
}

/// The message-list half of the GTK main window (ui/internal/window/
/// messages.go, threads.go, the list part of folders.go `onNewMessage`
/// and the selection handler of window.go): paging through message.list or
/// thread.list, the rows and their keys, the status pages, folding
/// conversations, the selection with its mark-as-read timer, and the
/// in-place edits between two loads (a notified arrival, a flag change, a
/// removal and its undo).
///
/// It is the second half of one GTK window object: it owns a reference to
/// the `MailboxController` (the folder half), reads and writes the shared
/// `model` there and installs itself into the folder half's list hooks
/// (`reloadMessages`, `onNewMessageForList`, `refreshOutboxViews`,
/// `collapseLoading`). No AppKit: the view subscribes to the callbacks and
/// sends the user's clicks back through the methods; the actions phase
/// drives `applyFlags`, `removeRows`, `selectedIDs` and `onMarkRead`.
///
/// Every RPC runs as a `Task` on the main actor through the mailbox's
/// `perform`, so the continuation is on the main actor too and the list
/// generation checks race with nothing (`glib.IdleAdd` in GTK).
@MainActor
public final class ListController {
    public typealias Restore = @MainActor () -> Void

    public let mailbox: MailboxController
    public let settings: Settings

    /// What the pane shows: the rows or a status page.
    public private(set) var listState: ListState = .messages
    /// The Load More footer.
    public private(set) var loadMoreState = LoadMoreState()
    /// The rows on screen, in order: `model.rows` in grouped mode, one row
    /// per message in flat mode (messages.go `rebuildMessageRows`).
    public private(set) var rows: [ListRow] = []
    /// The key of the selected row, nil for none (threads.go `selectedKey`).
    public private(set) var selectedKey: ListKey?
    /// What the selection allows (actions.go `setMessageActionsSensitive`).
    public private(set) var actionFlags: ActionFlags = .none

    /// The unit of `Settings.markReadDelay` (seconds); tests shorten it.
    public var markReadTick: Duration = .seconds(1)

    // MARK: Callbacks (the view)

    /// The rows changed: reconcile the table by key (`apply(rows:hint:)`),
    /// then mirror `selectedKey`.
    public var onRows: (@MainActor ([ListRow], SelectionHint) -> Void)?
    /// Switch between the rows and a status page.
    public var onListState: (@MainActor (ListState) -> Void)?
    /// The Load More footer changed.
    public var onLoadMore: (@MainActor (LoadMoreState) -> Void)?
    /// The controller dropped the selection (also announced as
    /// `onSelectedMessageChanged(nil)`).
    public var onSelectionCleared: (@MainActor () -> Void)?
    /// Flat mode: the rows with these keys changed in place (a flag);
    /// `row(for:)` has the new content. Grouped mode goes through `onRows`.
    public var onRowsRefreshed: (@MainActor ([ListKey]) -> Void)?

    // MARK: Callbacks (the rest of the window)

    /// The message the pane should show: the selected row's (a conversation
    /// row's newest folder member), nil when nothing is selected
    /// (window.go `onMessageRowSelected`).
    public var onSelectedMessageChanged: (@MainActor (MessageSummary?) -> Void)?
    /// A message row was activated (double-click, Return): open it in a
    /// window (window.go, the row-activated handler). A conversation row
    /// folds or unfolds instead and never reaches this.
    public var onActivateMessage: (@MainActor (MessageSummary) -> Void)?
    /// The per-message actions changed with the selection or its flags.
    public var onActionFlagsChanged: (@MainActor (ActionFlags) -> Void)?
    /// The mark-as-read timer fired for the selected message (actions.go
    /// `scheduleMarkRead` → `markRead`); the actions phase sets the flag.
    public var onMarkRead: (@MainActor (MessageID) -> Void)?
    /// The list's share of outbox.go `refreshOutboxViews` ran (the outbox
    /// listing reloaded when it was shown); the reader's share follows.
    public var onOutboxRefreshed: (@MainActor (AccountID) -> Void)?

    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "list")
    /// Closures to run once a conversation's members are known (the
    /// `waiters` of thread_model.go `threadMembers`).
    private var waiters: [ThreadID: [@MainActor () -> Void]] = [:]
    private var markReadTask: Task<Void, Never>?
    private var markReadID: MessageID?
    private var settingsToken: Settings.ChangeToken?
    private var closed = false

    /// Installs the list half into `mailbox`'s hooks and publishes the
    /// initial state (the folder the mailbox has selected, if any, is
    /// listed at once).
    public init(mailbox: MailboxController, settings: Settings) {
        self.mailbox = mailbox
        self.settings = settings
        // The filter is deliberately not persisted (window.go).
        mailbox.model.listFilter = .all
        mailbox.reloadMessages = { [weak self] in self?.loadMessages() }
        mailbox.onNewMessageForList = { [weak self] n in self?.applyNewMessage(n) }
        mailbox.refreshOutboxViews = { [weak self] acc in self?.refreshOutboxViews(acc) }
        mailbox.collapseLoading = { [weak self] in self?.collapseLoadingRows() }
        // Grouping is a different listing (thread.list): loadMessages
        // notices the mode change and starts the folder over (window.go).
        settingsToken = settings.onChange(.groupByConversation) { [weak self] in self?.loadMessages() }
        if mailbox.model.selected != nil {
            loadMessages()
        } else {
            showListState()
        }
    }

    /// Stops the timers and detaches from the settings (the window closed).
    public func close() {
        closed = true
        markReadTask?.cancel()
        markReadTask = nil
        settingsToken?.cancel()
        settingsToken = nil
        waiters = [:]
    }

    // MARK: Loading

    /// Runs message.list (or thread.list) for the selected folder, first
    /// page (messages.go `loadMessages`). A switch to another folder, or of
    /// the grouping mode, empties the list at once; a reload of the same
    /// folder keeps the rows until the reply, so the list never flickers
    /// through the loading state and the selection survives (by key).
    public func loadMessages() {
        let k = mailbox.model.selected
        let gen = mailbox.model.bumpList()
        mailbox.model.loadingMore = false
        let grouped = settings.groupByConversation && folderRole(k) != .outbox
        if k != mailbox.model.listFolder || grouped != mailbox.model.grouped {
            mailbox.model.listFolder = k
            mailbox.model.grouped = grouped
            mailbox.model.clearMessages()
            reconcile(.clear)
        }
        guard let k else {
            mailbox.model.loading = false
            showListState()
            return
        }
        if folderUnsynced(k) {
            // Never downloaded (Gmail's All Mail): nothing to ask for.
            mailbox.model.loading = false
            showListState()
            return
        }
        mailbox.model.loading = true
        mailbox.model.listErr = nil
        showListState()
        if grouped {
            loadThreadPage(k, gen)
            return
        }
        let params = MessageListParams(
            accountId: k.account, folderId: k.folder, page: Page(limit: API.Limits.defaultPageLimit),
            sort: .dateDesc, filter: mailbox.model.listFilter
        )
        mailbox.perform(API.MessageList.self, params) { [weak self] outcome in
            guard let self, gen == self.mailbox.model.listGen else { return }
            self.mailbox.model.loading = false
            switch outcome {
            case .failure(let err):
                self.log.warning("message.list \(k.folder.rawValue, privacy: .public): \(String(describing: err), privacy: .public)")
                if self.mailbox.model.rowCount > 0 {
                    // A reload failed: keep what is shown, say so once.
                    self.mailbox.toast(rpcErrorText(L10n.T("Loading messages"), err))
                    return
                }
                self.mailbox.model.listErr = err
                self.showListState()
            case .success(let res):
                self.mailbox.model.setMessages(res.messages, page: res.page)
                self.reconcile(.keep)
            }
        }
    }

    /// Runs thread.list for folder `k`, first page (threads.go
    /// `loadThreadPage`); `gen` is the list generation the reply belongs to.
    private func loadThreadPage(_ k: FolderKey, _ gen: UInt64) {
        let params = ThreadListParams(
            accountId: k.account, folderId: k.folder, page: Page(limit: API.Limits.defaultPageLimit),
            sort: .dateDesc, filter: mailbox.model.listFilter
        )
        mailbox.perform(API.ThreadList.self, params) { [weak self] outcome in
            guard let self, gen == self.mailbox.model.listGen else { return }
            self.mailbox.model.loading = false
            switch outcome {
            case .failure(let err):
                self.log.warning("thread.list \(k.folder.rawValue, privacy: .public): \(String(describing: err), privacy: .public)")
                if self.mailbox.model.rowCount > 0 {
                    self.mailbox.toast(rpcErrorText(L10n.T("Loading messages"), err))
                    return
                }
                self.mailbox.model.listErr = err
                self.showListState()
            case .success(let res):
                self.mailbox.model.setThreads(res.threads, page: res.page)
                self.syncRows()
                self.fetchExpandedMembers()
            }
        }
    }

    /// The Try Again button of the error page.
    public func retry() {
        loadMessages()
    }

    /// Switches the list between all, unread and flagged messages
    /// (messages.go `setListFilter`). The backend does the filtering, so
    /// the rows and the cursor of the previous filter are dropped and the
    /// folder is paged again from the start. A no-op on an unchanged
    /// filter, as the toggle group also fires for its own write-back.
    public func setListFilter(_ filter: MessageFilter) {
        var f = filter
        if f.rawValue.isEmpty {
            f = .all
        }
        guard f != mailbox.model.listFilter else { return }
        mailbox.model.listFilter = f
        mailbox.model.clearMessages()
        reconcile(.clear)
        loadMessages()
    }

    /// Fetches the next page (the Load More button, the scroll edge;
    /// messages.go `loadMore`). A no-op while a page is in flight or when
    /// the last page is shown.
    public func loadMore() {
        let m = mailbox.model
        guard !m.loading, !m.loadingMore, let cursor = m.nextCursor, !cursor.isEmpty, m.listErr == nil,
              let k = m.listFolder else { return }
        let gen = m.listGen
        mailbox.model.loadingMore = true
        showLoadMore()
        if m.grouped {
            let params = ThreadListParams(
                accountId: k.account, folderId: k.folder, page: Page(cursor: cursor, limit: API.Limits.defaultPageLimit),
                sort: .dateDesc, filter: m.listFilter
            )
            mailbox.perform(API.ThreadList.self, params) { [weak self] outcome in
                guard let self, gen == self.mailbox.model.listGen else { return }
                self.mailbox.model.loadingMore = false
                switch outcome {
                case .failure(let err):
                    self.log.warning("thread.list (more): \(String(describing: err), privacy: .public)")
                    self.mailbox.toast(rpcErrorText(L10n.T("Loading more messages"), err))
                    self.showLoadMore()
                case .success(let res):
                    self.mailbox.model.appendThreads(res.threads, page: res.page)
                    self.syncRows()
                }
            }
            return
        }
        let params = MessageListParams(
            accountId: k.account, folderId: k.folder, page: Page(cursor: cursor, limit: API.Limits.defaultPageLimit),
            sort: .dateDesc, filter: m.listFilter
        )
        mailbox.perform(API.MessageList.self, params) { [weak self] outcome in
            guard let self, gen == self.mailbox.model.listGen else { return }
            self.mailbox.model.loadingMore = false
            switch outcome {
            case .failure(let err):
                self.log.warning("message.list (more): \(String(describing: err), privacy: .public)")
                self.mailbox.toast(rpcErrorText(L10n.T("Loading more messages"), err))
                self.showLoadMore() // the cursor is still there; the button offers a retry
            case .success(let res):
                self.mailbox.model.appendMessages(res.messages, page: res.page)
                self.reconcile(.keep)
            }
        }
    }

    // MARK: States

    /// Switches between the rows and the status page (messages.go
    /// `showListState`): nothing selected, not synchronised, an error with
    /// Try Again, loading, or empty for the active filter.
    public func showListState() {
        let m = mailbox.model
        if m.rowCount > 0 {
            setListState(.messages)
            showLoadMore()
            return
        }
        let state: ListState
        if m.selected == nil {
            state = .status(
                icon: "folder-symbolic", title: L10n.T("Select a folder"),
                description: L10n.T("Choose a folder in the sidebar to see its messages."), retry: false
            )
        } else if let sel = m.selected, folderUnsynced(sel) {
            state = .status(
                icon: "folder-download-symbolic", title: L10n.T("Not Synchronised"),
                description: L10n.T("Messages moved here are archived on the server; the folder itself is not downloaded."),
                retry: false
            )
        } else if let err = m.listErr {
            state = .status(
                icon: "dialog-warning-symbolic", title: L10n.T("Messages Unavailable"),
                description: rpcErrorText(L10n.T("Loading messages"), err), retry: true
            )
        } else if m.loading {
            state = .status(icon: "", title: L10n.T("Loading…"), description: "", retry: false)
        } else if m.listFilter == .unread {
            state = .status(
                icon: "mail-read-symbolic", title: L10n.T("No Unread Messages"),
                description: L10n.T("Everything in this folder has been read."), retry: false
            )
        } else if m.listFilter == .flagged {
            state = .status(
                icon: "starred-symbolic", title: L10n.T("No Flagged Messages"),
                description: L10n.T("No message in this folder carries a flag."), retry: false
            )
        } else {
            state = .status(
                icon: "mail-unread-symbolic", title: L10n.T("No Messages"),
                description: L10n.T("This folder is empty."), retry: false
            )
        }
        setListState(state)
    }

    /// Shows the Load More button while a further page exists and the
    /// spinner while it is being fetched (messages.go `showLoadMore`).
    public func showLoadMore() {
        let m = mailbox.model
        let cursor = m.nextCursor ?? ""
        let state = LoadMoreState(spinner: m.loadingMore, button: !m.loadingMore && !cursor.isEmpty && m.listErr == nil)
        guard state != loadMoreState else { return }
        loadMoreState = state
        onLoadMore?(state)
    }

    private func setListState(_ s: ListState) {
        guard s != listState else { return }
        listState = s
        onListState?(s)
    }

    /// The special-use role of the listed folder, `.none` when nothing is
    /// listed or the folder is plain.
    public var folderRole: FolderRole {
        folderRole(mailbox.model.listFolder)
    }

    /// The listed folder is the account's outbox: never grouped, never
    /// marked read.
    public var inOutbox: Bool {
        folderRole == .outbox
    }

    private func folderRole(_ k: FolderKey?) -> FolderRole {
        guard let k else { return .none }
        return mailbox.model.folderRole(k)
    }

    /// A selected folder the daemon never downloads (messages.go
    /// `folderUnsynced`).
    private func folderUnsynced(_ k: FolderKey) -> Bool {
        guard let f = mailbox.model.folder(k) else { return false }
        return !f.synced
    }

    // MARK: Rows

    /// The row with the given key, if listed.
    public func row(for key: ListKey) -> ListRow? {
        let idx = mailbox.model.rowIndexOf(key)
        return mailbox.model.rowAt(idx)
    }

    /// The row behind the selection, if any (threads.go `selectedRow`).
    public var selectedRow: ListRow? {
        guard let key = selectedKey else { return nil }
        return row(for: key)
    }

    /// The rows the model has right now, in either mode.
    private func currentRows() -> [ListRow] {
        let m = mailbox.model
        if m.grouped {
            return m.rows
        }
        return m.messages.map { ListRow(key: ListKey(message: $0.id), message: $0) }
    }

    /// Brings the rows in line with the model (threads.go `syncRows`): the
    /// selection follows its key; when a member row folded away its
    /// conversation row takes over (and the pane shows the newest member);
    /// when the selected row is gone the pane is cleared.
    public func syncRows() {
        reconcile(.keep)
    }

    /// `syncRows` for a removal (threads.go `syncRowsAfterRemoval`): a
    /// selected row that is gone hands the selection to the row now at its
    /// place, pane included, as the flat list does.
    private func syncRowsAfterRemoval() {
        reconcile(.neighbour)
    }

    /// Publishes the model's rows and reconciles the selection with them
    /// (threads.go `reconcileRows`, messages.go `rebuildMessageRows`). The
    /// index of the selected row is read off the rows as they were before
    /// the model moved on, as the GTK code reads it off the widgets.
    private func reconcile(_ hint: SelectionHint) {
        let prevKey = selectedKey
        let prevIdx = prevKey.flatMap { k in rows.firstIndex { $0.key == k } } ?? -1
        rows = currentRows()
        var changed = false
        if let prevKey {
            if hint == .clear {
                selectedKey = nil
                changed = true
            } else if rows.contains(where: { $0.key == prevKey }) {
                // Still listed: the pane already shows it.
            } else if let tid = prevKey.thread, let r = rows.first(where: { $0.key == ListKey(thread: tid) }) {
                selectedKey = r.key
                changed = true
            } else if hint == .neighbour, prevIdx >= 0, min(prevIdx, rows.count - 1) >= 0 {
                selectedKey = rows[min(prevIdx, rows.count - 1)].key
                changed = true
            } else {
                selectedKey = nil
                changed = true
            }
        }
        onRows?(rows, hint)
        if changed {
            announceSelection()
        }
        showListState()
    }

    /// Pushes the model to the rows of the given messages (actions.go
    /// `refreshRows`); in grouped mode the conversation rows carry
    /// aggregates, so the whole list is reconciled.
    public func refreshRows(_ ids: [MessageID]) {
        if mailbox.model.grouped {
            syncRows()
            return
        }
        rows = currentRows()
        let keys = ids.filter { mailbox.model.index[$0] != nil }.map { ListKey(message: $0) }
        if !keys.isEmpty {
            onRowsRefreshed?(keys)
        }
    }

    // MARK: Conversations

    /// Folds or unfolds a conversation row; unfolding asks for the members
    /// when they are not known yet (threads.go `toggleThread`).
    public func toggleThread(_ tid: ThreadID) {
        let on = !mailbox.model.expanded.contains(tid)
        mailbox.model.setExpanded(tid, on)
        if on {
            ensureMembers(tid, nil)
        }
        syncRows()
    }

    /// `toggleThread` for the Left/Right keys (threads.go
    /// `addThreadShortcuts`): false when the conversation is in that state
    /// already, so the key still reaches the list for its normal navigation.
    @discardableResult
    public func setThreadExpanded(_ tid: ThreadID, _ on: Bool) -> Bool {
        guard mailbox.model.expanded.contains(tid) != on else { return false }
        toggleThread(tid)
        return true
    }

    /// Double-click or Return on a row (window.go, the row-activated
    /// handler): a conversation row folds or unfolds, a message opens in a
    /// window.
    public func activate(key: ListKey) {
        guard let r = row(for: key) else { return }
        if r.thread, let tid = r.key.thread {
            toggleThread(tid)
        } else {
            onActivateMessage?(r.message)
        }
    }

    /// Makes the folder members of a conversation known, through thread.get
    /// when needed, and runs `then` afterwards (at once when they are). A
    /// failed fetch folds the row back and says why (threads.go
    /// `ensureMembers`).
    public func ensureMembers(_ tid: ThreadID, _ then: (@MainActor () -> Void)?) {
        guard let mem = mailbox.model.members[tid] else { return }
        if mem.complete {
            then?()
            return
        }
        if let then {
            waiters[tid, default: []].append(then)
        }
        if mem.fetching {
            return
        }
        guard let k = mailbox.model.listFolder else { return }
        mailbox.model.members[tid]?.fetching = true
        let gen = mailbox.model.listGen
        let params = ThreadGetParams(accountId: k.account, threadId: tid, folderId: k.folder)
        mailbox.perform(API.ThreadGet.self, params) { [weak self] outcome in
            guard let self, gen == self.mailbox.model.listGen, self.mailbox.model.members[tid] != nil else { return }
            self.mailbox.model.members[tid]?.fetching = false
            let waiting = self.waiters[tid] ?? []
            self.waiters[tid] = nil
            switch outcome {
            case .failure(let err):
                self.log.warning("thread.get: \(String(describing: err), privacy: .public)")
                self.mailbox.toast(rpcErrorText(L10n.T("Loading the conversation"), err))
                self.mailbox.model.setExpanded(tid, false)
                self.syncRows()
            case .success(let res):
                self.mailbox.model.setMembers(tid, res.thread, res.messages)
                self.syncRows()
                if let row = self.selectedRow, row.key.thread == tid {
                    self.refreshActionFlags()
                }
                for fn in waiting {
                    fn()
                }
            }
        }
    }

    /// Asks for the members of every unfolded conversation that lost them
    /// (a reload that changed the conversation; threads.go
    /// `fetchExpandedMembers`).
    private func fetchExpandedMembers() {
        for tid in mailbox.model.expanded {
            if let mem = mailbox.model.members[tid], !mem.complete {
                ensureMembers(tid, nil)
            }
        }
    }

    /// Folds every conversation waiting for its members and forgets their
    /// waiters (the backend went away; window.go `showConnectionState`).
    public func collapseLoadingRows() {
        mailbox.model.collapseLoading()
        waiters = [:]
        syncRows()
        showLoadMore()
    }

    /// The list's share of window.go `showConnectionState`, after the
    /// folder half's: the model's `bumpAll` there dropped the in-flight
    /// replies and cleared the loading flags, so the Load More footer is
    /// redrawn from it (the spinner stops) and a conversation waiting for
    /// its members folds back. The folder half's `collapseLoading` hook
    /// does the latter as well; both are idempotent.
    public func handleConnection(_ state: ConnectionController.ConnectionState) {
        switch state {
        case .unavailable, .stopping:
            if mailbox.model.grouped {
                collapseLoadingRows()
            }
            showLoadMore()
        case .connecting, .connected, .protocolMismatch, .infoFailed:
            break
        }
    }

    // MARK: Selection

    /// The user selected a row, or cleared the selection (window.go, the
    /// row-selected handler → `onMessageRowSelected`): the pane shows the
    /// row's message (a conversation row's newest folder member), the
    /// actions follow, the mark-as-read timer is armed.
    public func select(key: ListKey?) {
        selectedKey = key
        announceSelection()
    }

    /// window.go `onMessageRowSelected` for the current `selectedKey`.
    private func announceSelection() {
        let row = selectedRow
        if row == nil {
            selectedKey = nil
            onSelectionCleared?()
        }
        onSelectedMessageChanged?(row?.message)
        refreshActionFlags()
        if let row, !mailbox.model.inOutbox(row.message) {
            scheduleMarkRead(row.message.id)
        } else {
            scheduleMarkRead(nil) // the daemon refuses flags on outbox messages
        }
    }

    /// Re-evaluates the per-message actions for the selected row
    /// (actions.go `refreshMessageActions`).
    public func refreshActionFlags() {
        let f = messageActionState(selectedRow, model: mailbox.model)
        guard f != actionFlags else { return }
        actionFlags = f
        onActionFlagsChanged?(f)
    }

    /// Runs `then` with the selected row and every message it stands for:
    /// one, or all the folder members of a conversation row, fetched first
    /// when they are not known yet (the selection must still be that
    /// conversation by then; threads.go `selectedIDs`).
    public func selectedIDs(_ then: @escaping @MainActor (ListRow, [MessageID]) -> Void) {
        guard let row = selectedRow else { return }
        if let ids = mailbox.model.rowIDs(row) {
            then(row, ids)
            return
        }
        guard let tid = row.key.thread else { return }
        ensureMembers(tid) { [weak self] in
            guard let self, let r = self.selectedRow, r.thread, r.key.thread == tid,
                  let ids = self.mailbox.model.rowIDs(r) else { return }
            then(r, ids)
        }
    }

    /// The subject a confirmation shows for a row: the conversation's, or
    /// the message's (threads.go `rowSubject`).
    public func rowSubject(_ row: ListRow) -> String {
        if row.thread, let summary = row.summary {
            return subjectText(summary.subject)
        }
        return subjectText(row.message.subject)
    }

    /// The flagged state the star moves a row to (thread_model.go
    /// `flagTarget`).
    public func flagTarget(_ row: ListRow) -> Bool {
        mailbox.model.flagTarget(row)
    }

    /// Arms the mark-as-read timer for the newly selected message,
    /// cancelling any pending one; nil only cancels (actions.go
    /// `scheduleMarkRead`). A message that is read already, or unknown to
    /// the list, arms nothing.
    private func scheduleMarkRead(_ id: MessageID?) {
        markReadTask?.cancel()
        markReadTask = nil
        markReadID = id
        guard let id, let found = mailbox.model.message(id), !hasFlag(found.summary.flags, .seen) else { return }
        let delay = settings.markReadDelay
        if delay <= 0 {
            onMarkRead?(id)
            return
        }
        let wait = markReadTick * delay
        markReadTask = Task { [weak self] in
            try? await Task.sleep(for: wait)
            guard let self, !Task.isCancelled, !self.closed else { return }
            self.markReadTask = nil
            if self.markReadID == id {
                self.onMarkRead?(id)
            }
        }
    }

    // MARK: Changes between loads

    /// Inserts a notified message at the top of the list when it belongs to
    /// the listed folder (the list part of folders.go `onNewMessage`; the
    /// folder half adjusts the badge and filters out what the model holds
    /// already). While the page is (re)loading the reply will include the
    /// message; only a settled list gets the row, and only when the active
    /// filter would have listed it anyway.
    public func applyNewMessage(_ n: NewMessageNotification) {
        let k = FolderKey(account: n.accountId, folder: n.folderId)
        let s = n.message
        guard k == mailbox.model.listFolder, mailbox.model.message(s.id) == nil else { return }
        let m = mailbox.model
        if m.loading || m.listErr != nil {
            return
        }
        if m.grouped {
            // Into its conversation row, or a new one at the top; without a
            // thread id (a daemon still linking) the list is asked again.
            if mailbox.model.applyNewMessage(s, filter: m.listFilter, selected: selectedKey ?? ListKey()) {
                syncRows()
            } else {
                loadMessages()
            }
            return
        }
        if matchesFilter(s, m.listFilter), mailbox.model.insertMessage(at: 0, s) {
            reconcile(.keep)
        }
    }

    /// Changes the flags of the given messages in the model, refreshes their
    /// rows and the actions, and returns the ids that actually changed (the
    /// `apply` step of actions.go `setSeenIDs` / `setFlaggedIDs`; the
    /// caller adjusts the folder badge and sends message.flag).
    @discardableResult
    public func applyFlags(_ ids: [MessageID], set: [Flag] = [], clear: [Flag] = []) -> [MessageID] {
        let changed = mailbox.model.applyFlags(ids, set: set, clear: clear)
        refreshRows(changed)
        refreshActionFlags()
        return changed
    }

    /// Drops messages from the list in either mode and returns the closure
    /// that puts them back after a failed move or delete (messages.go
    /// `removeRows`). A grouped conversation whose members are not all
    /// known cannot be edited in place; the list is loaded again instead
    /// and the restore does nothing. A restore after the list moved on (a
    /// reload) does nothing either.
    public func removeRows(_ ids: [MessageID]) -> Restore {
        if !mailbox.model.grouped {
            let restores = ids.map { removeMessageRow($0) }
            return {
                for r in restores.reversed() {
                    r()
                }
            }
        }
        guard let removal = mailbox.model.removeMessages(ids) else {
            loadMessages()
            return {}
        }
        let gen = mailbox.model.listGen
        syncRowsAfterRemoval()
        return { [weak self] in
            guard let self, self.mailbox.model.listGen == gen else { return }
            self.mailbox.model.restoreRemoval(removal)
            self.syncRows()
        }
    }

    /// Drops a message from the flat list (messages.go `removeMessageRow`).
    /// When it was the selected one its neighbour is selected (the pane
    /// follows), or the pane is cleared when the list ran empty.
    private func removeMessageRow(_ id: MessageID) -> Restore {
        guard let (s, idx) = mailbox.model.removeMessage(id) else {
            return {}
        }
        let gen = mailbox.model.listGen
        syncRowsAfterRemoval()
        return { [weak self] in
            guard let self, self.mailbox.model.listGen == gen, self.mailbox.model.insertMessage(at: idx, s) else { return }
            self.reconcile(.keep)
        }
    }

    /// The list's share of outbox.go `refreshOutboxViews`: the outbox
    /// listing is reloaded when it is shown; `onOutboxRefreshed` hands the
    /// account on for the reader's share.
    public func refreshOutboxViews(_ acc: AccountID) {
        if let outbox = mailbox.model.folderByRole(acc, .outbox),
           mailbox.model.listFolder == FolderKey(account: acc, folder: outbox.id) {
            loadMessages()
        }
        onOutboxRefreshed?(acc)
    }
}
