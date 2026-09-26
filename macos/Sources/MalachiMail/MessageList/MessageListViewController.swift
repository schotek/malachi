// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The message list pane (window.blp lines 151–301): the backend and
/// sign-in banners, the table of rows with its Load More footer, and the
/// status page that replaces the rows while there are none. The All /
/// Unread / Flagged filter is a toolbar menu, as in Mail (MainToolbar); so
/// is the search field, whose scope bar (`SearchScopeBar`) sits over the
/// rows while a search is on.
///
/// The list controller owns every decision; this view mirrors its rows by
/// key (`apply(rows:hint:)`) and sends the clicks and keys back. It
/// installs the view-facing callbacks of the list controller and the sync
/// controller's `onAuthBanner`; the connection state is pushed in by the
/// app through `showConnectionState`.
@MainActor
final class MessageListViewController: NSViewController, NSTableViewDataSource, NSTableViewDelegate {
    let list: ListController
    let mailbox: MailboxController
    let connection: ConnectionController
    let settings: Settings

    /// Forwarded from the list controller: the message the pane should
    /// show (nil when nothing is selected), and a message row activated by
    /// double-click or Return.
    var onSelectedMessageChanged: (@MainActor (MessageSummary?) -> Void)?
    var onActivateMessage: (@MainActor (MessageSummary) -> Void)?
    /// The sign-in banner's button (window.go `onAuthBannerButton`: the
    /// preferences, or the online accounts panel).
    var onAuthBannerButton: (@MainActor () -> Void)?
    /// The certificate banner's "Edit Account…" (window.go
    /// `onCertBannerButton`).
    var onCertBannerButton: (@MainActor () -> Void)?

    /// The row behind the selection, if any.
    var selectedRow: ListRow? {
        list.selectedRow
    }

    private let backendBanner = BannerView(
        title: L10n.T("The mail backend (malachid) is not running."), buttonTitle: L10n.T("Retry"),
        symbol: "exclamationmark.triangle.fill", severity: .warning
    )
    private let authBanner = BannerView(
        title: "", buttonTitle: L10n.T("Open Preferences"),
        symbol: "person.crop.circle.badge.exclamationmark", severity: .warning
    )
    /// window.blp `cert_banner`: the first account whose server's
    /// certificate was refused or has changed.
    private let certBanner = BannerView(
        title: "", buttonTitle: mn(L10n.T("_Edit Account…")),
        symbol: "lock.trianglebadge.exclamationmark", severity: .warning
    )
    private let scroll = NSScrollView()
    private let table = MessageListTableView()
    private let messagesPage = FillStackView()
    private let statusPage: StatusPageView
    private let retryButton = NSButton(title: L10n.T("Try Again"), target: nil, action: nil)
    private let loadMoreButton = NSButton(title: L10n.T("Load More"), target: nil, action: nil)
    private let loadMoreSpinner = Spinner(size: 16)
    /// The foot under the list with the spinner and the retry button;
    /// hidden while it has neither, so the rows reach the pane's bottom.
    private let loadMoreBox = NSStackView()
    /// Where a search looks (window.blp `search_scope`).
    private let scopeBar = SearchScopeBar()
    /// Under the last page of search results: how far back search reaches
    /// (window.blp `search_note`).
    private let searchNote = NSTextField(wrappingLabelWithString: "")
    /// The note with its margins; hidden with it, so no empty strip stays
    /// under the rows.
    private let noteBox = NSStackView()

    /// The table's rows, by position, and what each key shows.
    private var keys: [ListKey] = []
    private var rowsByKey: [ListKey: ListRow] = [:]
    /// A programmatic selection change must not re-enter `select(key:)`
    /// (window.go `reselecting`).
    private var isReselecting = false
    private var appearance: RowAppearance
    private var settingsTokens: [Settings.ChangeToken] = []
    /// The scroll edge was reached; reset once the user scrolls away, so
    /// one arrival at the bottom asks for one page (Gtk.ScrolledWindow
    /// `edge-reached`).
    private var atBottom = false
    /// The row count when this view last asked for the next page, until
    /// the answer; nil when no request is open.
    private var requestedAt: Int?

    static let loadMoreSpacing: CGFloat = 6
    static let loadMoreMargin: CGFloat = 6

    init(list: ListController, mailbox: MailboxController, connection: ConnectionController, settings: Settings) {
        self.list = list
        self.mailbox = mailbox
        self.connection = connection
        self.settings = settings
        appearance = RowAppearance(settings: settings)
        statusPage = StatusPageView(illustration: .none, title: "", description: "")
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    // MARK: View

    override func loadView() {
        backendBanner.onButton = { [weak self] in self?.connection.reconnectNow() }
        authBanner.onButton = { [weak self] in self?.onAuthBannerButton?() }
        certBanner.onButton = { [weak self] in self?.onCertBannerButton?() }

        // The table (window.blp `message_list`).
        let column = NSTableColumn(identifier: NSUserInterfaceItemIdentifier("message"))
        column.resizingMask = .autoresizingMask
        table.addTableColumn(column)
        table.headerView = nil
        table.style = .plain
        table.usesAutomaticRowHeights = true
        table.intercellSpacing = .zero
        table.allowsEmptySelection = true
        table.allowsMultipleSelection = false
        table.columnAutoresizingStyle = .uniformColumnAutoresizingStyle
        table.selectionHighlightStyle = .regular
        table.dataSource = self
        table.delegate = self
        table.target = self
        table.doubleAction = #selector(rowDoubleClicked(_:))
        table.onActivate = { [weak self] in self?.activateSelected() ?? false }
        table.onFold = { [weak self] in self?.foldSelected(false) ?? false }
        table.onUnfold = { [weak self] in self?.foldSelected(true) ?? false }

        scroll.documentView = table
        scroll.hasVerticalScroller = true
        scroll.hasHorizontalScroller = false
        scroll.autohidesScrollers = true
        scroll.translatesAutoresizingMaskIntoConstraints = false
        scroll.setContentHuggingPriority(.defaultLow, for: .vertical)
        scroll.contentView.postsBoundsChangedNotifications = true
        NotificationCenter.default.addObserver(
            self, selector: #selector(clipBoundsChanged(_:)), name: NSView.boundsDidChangeNotification, object: scroll.contentView
        )

        // Load More (window.blp) the Mac's way: the list pages itself, as
        // Mail's does (the scroll edge, or rows too few to fill the pane),
        // the footer shows a small spinner while a page loads, and the
        // button, a standard small one, appears only to retry a page that
        // failed (a deviation, macos/README.md).
        loadMoreButton.bezelStyle = .push
        loadMoreButton.controlSize = .small
        loadMoreButton.font = NSFont.systemFont(ofSize: NSFont.systemFontSize(for: .small))
        loadMoreButton.target = self
        loadMoreButton.action = #selector(loadMoreClicked(_:))
        loadMoreButton.isHidden = true
        loadMoreSpinner.isHidden = true
        loadMoreBox.isHidden = true
        loadMoreBox.orientation = .horizontal
        loadMoreBox.alignment = .centerY
        loadMoreBox.spacing = Self.loadMoreSpacing
        loadMoreBox.edgeInsets = NSEdgeInsets(top: Self.loadMoreMargin, left: 0, bottom: Self.loadMoreMargin, right: 0)
        loadMoreBox.addView(loadMoreButton, in: .center)
        loadMoreBox.addView(loadMoreSpinner, in: .center)
        loadMoreBox.translatesAutoresizingMaskIntoConstraints = false

        searchNote.font = Typo.caption
        searchNote.textColor = Tint.secondary
        searchNote.alignment = .center
        searchNote.isSelectable = false
        noteBox.addArrangedSubview(searchNote)
        noteBox.isHidden = true
        noteBox.orientation = .vertical
        noteBox.edgeInsets = NSEdgeInsets(top: 0, left: 12, bottom: Self.loadMoreMargin, right: 12)
        noteBox.translatesAutoresizingMaskIntoConstraints = false

        messagesPage.spacing = 0
        messagesPage.addArrangedSubview(scroll)
        messagesPage.addArrangedSubview(loadMoreBox)
        messagesPage.addArrangedSubview(noteBox)

        // The status page with its Try Again (window.blp `list_status_page`).
        retryButton.bezelStyle = .rounded
        retryButton.controlSize = .large
        retryButton.target = self
        retryButton.action = #selector(retryClicked(_:))
        retryButton.isHidden = true
        statusPage.setChild(retryButton)
        statusPage.isHidden = true

        let pages = NSView()
        pages.translatesAutoresizingMaskIntoConstraints = false
        pages.setContentHuggingPriority(.defaultLow, for: .vertical)
        pages.setContentCompressionResistancePriority(.defaultLow, for: .vertical)
        pages.addSubview(messagesPage)
        pages.addSubview(statusPage)
        NSLayoutConstraint.activate([
            messagesPage.topAnchor.constraint(equalTo: pages.topAnchor),
            messagesPage.bottomAnchor.constraint(equalTo: pages.bottomAnchor),
            messagesPage.leadingAnchor.constraint(equalTo: pages.leadingAnchor),
            messagesPage.trailingAnchor.constraint(equalTo: pages.trailingAnchor),
            statusPage.topAnchor.constraint(equalTo: pages.topAnchor),
            statusPage.bottomAnchor.constraint(equalTo: pages.bottomAnchor),
            statusPage.leadingAnchor.constraint(equalTo: pages.leadingAnchor),
            statusPage.trailingAnchor.constraint(equalTo: pages.trailingAnchor),
        ])

        scopeBar.onScope = { [weak self] scope in self?.list.setSearchScope(scope) }

        let root = FillStackView(fillingViews: [backendBanner, authBanner, certBanner, scopeBar, pages])
        root.spacing = 0
        // Below the unified toolbar (fullSizeContentView): the banners
        // must not run under it; the scroll view alone
        // would inset itself. The whole column is painted like the list
        // (window.blp: the list pane is a `view` styled box), status pages
        // included.
        let container = ContentBackgroundView()
        container.translatesAutoresizingMaskIntoConstraints = false
        container.addSubview(root)
        NSLayoutConstraint.activate([
            root.topAnchor.constraint(equalTo: container.safeAreaLayoutGuide.topAnchor),
            root.bottomAnchor.constraint(equalTo: container.bottomAnchor),
            root.leadingAnchor.constraint(equalTo: container.leadingAnchor),
            root.trailingAnchor.constraint(equalTo: container.trailingAnchor),
        ])
        view = container
    }

    override func viewDidLoad() {
        super.viewDidLoad()
        list.onRows = { [weak self] rows, hint in self?.apply(rows: rows, hint: hint) }
        list.onListState = { [weak self] state in self?.show(state) }
        list.onLoadMore = { [weak self] state in self?.showLoadMore(state) }
        list.onRowsRefreshed = { [weak self] keys in self?.refreshRows(keys) }
        list.onSelectionCleared = { [weak self] in self?.syncSelection() }
        list.onSearchBar = { [weak self] state in self?.scopeBar.show(state) }
        list.onFocusRow = { [weak self] key in self?.focus(key) }
        // Forwarded to this view's own callbacks; a handler the app put on
        // the controller before the view loaded keeps running too.
        let selected = list.onSelectedMessageChanged
        list.onSelectedMessageChanged = { [weak self] s in
            selected?(s)
            self?.onSelectedMessageChanged?(s)
        }
        let activated = list.onActivateMessage
        list.onActivateMessage = { [weak self] s in
            activated?(s)
            self?.onActivateMessage?(s)
        }
        mailbox.sync.onAuthBanner = { [weak self] _, title, button in
            guard let self else { return }
            if let title {
                self.showAuthBanner(title: title, button: button ?? L10n.T("Open Preferences"))
            } else {
                self.hideAuthBanner()
            }
        }
        mailbox.sync.onCertBanner = { [weak self] _, title, button in
            guard let self else { return }
            if let title {
                self.certBanner.title = title
                self.certBanner.buttonTitle = button ?? mn(L10n.T("_Edit Account…"))
                self.certBanner.reveal(true)
            } else {
                self.certBanner.reveal(false)
            }
        }
        // Density, preview and avatars re-lay the rows out; grouping is a
        // different listing, which the list controller reloads itself.
        for key in [Settings.Key.density, .showPreviewLine, .showAvatars, .monochromeAvatars] {
            settingsTokens.append(settings.onChange(key) { [weak self] in self?.applyAppearance() })
        }
        // What the controllers hold already, for a list attached late.
        apply(rows: list.rows, hint: .clear)
        show(list.listState)
        showLoadMore(list.loadMoreState)
        showConnectionState(connection.state)
    }

    // MARK: Banners

    /// The backend banner follows the connection state (window.go
    /// `showConnectionState`); the app calls it from its
    /// `ConnectionController.onState` fan-out, after the mailbox. An
    /// attempt in progress leaves the banner as it is: the retry loop
    /// reports one every few seconds while the daemon is down, and the
    /// banner must not blink with it.
    func showConnectionState(_ state: ConnectionController.ConnectionState) {
        switch state {
        case .unavailable, .stopping:
            backendBanner.reveal(true)
        case .connected, .protocolMismatch, .infoFailed:
            backendBanner.reveal(false)
        case .connecting:
            break
        }
        list.handleConnection(state)
    }

    /// Reveals the sign-in banner (sync.go `showAuthRequired`).
    func showAuthBanner(title: String, button: String) {
        authBanner.title = title
        authBanner.buttonTitle = button
        authBanner.reveal(true)
    }

    func hideAuthBanner() {
        authBanner.reveal(false)
    }

    // MARK: Rows

    /// Brings the table in line with the controller's rows (threads.go
    /// `reconcileRows`, messages.go `rebuildMessageRows`): rows whose key
    /// is gone are removed, new keys inserted at their position, rows that
    /// stayed are updated in place; then the selection is set to the
    /// controller's `selectedKey`. A list replaced whole (another folder)
    /// is reloaded without animation.
    private func apply(rows: [ListRow], hint: SelectionHint) {
        if hint == .clear {
            // Another listing: a page asked for the one before is moot.
            requestedAt = nil
            loadMoreButton.isHidden = true
            updateLoadMoreBox()
        }
        let newKeys = rows.map(\.key)
        var newByKey: [ListKey: ListRow] = [:]
        newByKey.reserveCapacity(rows.count)
        for r in rows where newByKey[r.key] == nil {
            newByKey[r.key] = r
        }
        let oldByKey = rowsByKey
        let oldKeys = keys
        let common = !oldKeys.isEmpty && oldKeys.contains { newByKey[$0] != nil }

        isReselecting = true
        defer { isReselecting = false }
        keys = newKeys
        rowsByKey = newByKey
        if !common || hint == .clear {
            table.reloadData()
            syncSelection()
            return
        }

        let diff = newKeys.difference(from: oldKeys)
        var removed = IndexSet()
        var inserted = IndexSet()
        for change in diff {
            switch change {
            case .remove(let offset, _, _):
                removed.insert(offset)
            case .insert(let offset, _, _):
                inserted.insert(offset)
            }
        }
        table.beginUpdates()
        if !removed.isEmpty {
            table.removeRows(at: removed, withAnimation: .slideUp)
        }
        if !inserted.isEmpty {
            table.insertRows(at: inserted, withAnimation: .slideDown)
        }
        table.endUpdates()

        // Rows that stayed: re-render the ones whose content moved.
        var changed = IndexSet()
        for (i, key) in newKeys.enumerated() where !inserted.contains(i) {
            if let old = oldByKey[key], let new = newByKey[key], old != new {
                changed.insert(i)
            }
        }
        for i in changed {
            render(row: i)
        }
        if !changed.isEmpty {
            table.noteHeightOfRows(withIndexesChanged: changed)
        }
        // The hairline of the row that stopped, or started, being the last.
        let last = table.numberOfRows - 1
        if last >= 0 {
            table.rowView(atRow: last, makeIfNecessary: false)?.needsDisplay = true
            if last > 0 {
                table.rowView(atRow: last - 1, makeIfNecessary: false)?.needsDisplay = true
            }
        }
        syncSelection()
    }

    /// Flat mode: a flag change re-rendered these keys (actions.go
    /// `refreshRow`).
    private func refreshRows(_ changed: [ListKey]) {
        for key in changed {
            guard let row = list.row(for: key), let i = keys.firstIndex(of: key) else { continue }
            rowsByKey[key] = row
            render(row: i)
        }
    }

    /// Pushes the row's content to its visible cell, if any; a cell off
    /// screen is configured when it scrolls in.
    private func render(row i: Int) {
        guard i >= 0, i < keys.count, let r = rowsByKey[keys[i]] else { return }
        if let cell = table.view(atColumn: 0, row: i, makeIfNecessary: false) as? MessageCellView {
            configure(cell, r)
        }
        if let rowView = table.rowView(atRow: i, makeIfNecessary: false) as? MessageRowView {
            rowView.isMember = r.member
        }
    }

    private func configure(_ cell: MessageCellView, _ r: ListRow) {
        cell.configure(r, message: list.rowMessage(r.message), reserveExpander: mailbox.model.grouped, appearance: rowAppearance)
        let tid = r.key.thread
        cell.onToggle = { [weak self] in
            if let tid {
                self?.list.toggleThread(tid)
            }
        }
    }

    /// The rows' look from the settings; a search result always shows its
    /// excerpt, which says why it was found (search.go).
    private var rowAppearance: RowAppearance {
        var a = appearance
        if list.searchActive {
            a.showPreview = true
        }
        return a
    }

    /// Return in the search field: the first result is selected and the
    /// list takes the keyboard (search.go `selectFirstResult`). The table's
    /// selection change tells the controller.
    private func focus(_ key: ListKey) {
        guard let i = keys.firstIndex(of: key) else { return }
        table.selectRowIndexes(IndexSet(integer: i), byExtendingSelection: false)
        table.scrollRowToVisible(i)
        view.window?.makeFirstResponder(table)
    }

    /// Selects the controller's `selectedKey` without re-entering
    /// `select(key:)`.
    private func syncSelection() {
        let wasReselecting = isReselecting
        isReselecting = true
        defer { isReselecting = wasReselecting }
        guard let key = list.selectedKey, let i = keys.firstIndex(of: key) else {
            if table.selectedRow >= 0 {
                table.deselectAll(nil)
            }
            return
        }
        if table.selectedRow != i {
            table.selectRowIndexes(IndexSet(integer: i), byExtendingSelection: false)
        }
    }

    /// The list settings changed (window.go, the settings handlers): every
    /// row is laid out again; the selection stays.
    private func applyAppearance() {
        appearance = RowAppearance(settings: settings)
        isReselecting = true
        defer { isReselecting = false }
        table.reloadData()
        syncSelection()
    }

    // MARK: States

    /// Switches between the rows and the status page (messages.go
    /// `showListState`, window.blp `list_stack`).
    private func show(_ state: ListState) {
        switch state {
        case .messages:
            statusPage.isHidden = true
            messagesPage.isHidden = false
        case .status(let icon, let title, let description, let retry):
            // An empty icon is the "Loading…" page, which has none in GTK.
            statusPage.illustration = icon.isEmpty ? .none : .icon(icon)
            statusPage.title = title
            statusPage.descriptionText = description
            retryButton.isHidden = !retry
            messagesPage.isHidden = true
            statusPage.isHidden = false
        }
    }

    private func showLoadMore(_ state: LoadMoreState) {
        loadMoreSpinner.isHidden = !state.spinner
        if state.spinner {
            loadMoreSpinner.start()
        } else {
            loadMoreSpinner.stop()
        }
        // A page this view asked for is back: rows came, or it failed (the
        // controller toasts why) and the button offers the retry.
        var failed = false
        if state.button, let at = requestedAt {
            requestedAt = nil
            failed = keys.count == at
        }
        loadMoreButton.isHidden = !failed
        updateLoadMoreBox()
        searchNote.stringValue = state.note
        noteBox.isHidden = state.note.isEmpty
        if state.button, !failed {
            DispatchQueue.main.async { [weak self] in self?.fillPane() }
        }
    }

    private func updateLoadMoreBox() {
        loadMoreBox.isHidden = loadMoreSpinner.isHidden && loadMoreButton.isHidden
    }

    /// Asks for the next page, noting the rows it had then.
    private func requestMore() {
        requestedAt = keys.count
        list.loadMore()
    }

    /// Rows too few to fill the pane have no edge to scroll to: the list
    /// asks for the next page itself (GTK shows its Load More button
    /// then), unless a request is open or the last one failed.
    private func fillPane() {
        guard list.loadMoreState.button, requestedAt == nil, loadMoreButton.isHidden else { return }
        let rows = table.numberOfRows
        let height = rows > 0 ? table.rect(ofRow: rows - 1).maxY : 0
        guard height <= scroll.contentView.bounds.height else { return }
        requestMore()
    }

    // MARK: Actions

    @objc private func retryClicked(_ sender: Any?) {
        list.retry()
    }

    @objc private func loadMoreClicked(_ sender: Any?) {
        loadMoreButton.isHidden = true
        updateLoadMoreBox()
        requestMore()
    }

    @objc private func rowDoubleClicked(_ sender: Any?) {
        let row = table.clickedRow
        guard row >= 0, row < keys.count else { return }
        list.activate(key: keys[row])
    }

    /// The scroll edge (window.go `ConnectEdgeReached`): the bottom asks
    /// for the next page once per arrival. Rows that do not fill the
    /// viewport have no edge to reach: `fillPane` asks then, also when the
    /// pane grows.
    @objc private func clipBoundsChanged(_ note: Notification) {
        let clip = scroll.contentView
        let scrollable = table.bounds.height > clip.bounds.height
        let bottom = scrollable && clip.bounds.maxY >= table.bounds.height - 1
        if bottom, !atBottom, loadMoreButton.isHidden {
            requestMore()
        }
        atBottom = bottom
        if !scrollable {
            fillPane()
        }
    }

    private func activateSelected() -> Bool {
        let row = table.selectedRow
        guard row >= 0, row < keys.count else { return false }
        list.activate(key: keys[row])
        return true
    }

    /// Left folds the conversation of the selected row (a conversation row
    /// or one of its members), Right unfolds a conversation row; false
    /// when there is nothing to do (threads.go `addThreadShortcuts`).
    private func foldSelected(_ on: Bool) -> Bool {
        let row = table.selectedRow
        guard row >= 0, row < keys.count, let r = rowsByKey[keys[row]], let tid = r.key.thread else { return false }
        if on, r.member {
            return false
        }
        return list.setThreadExpanded(tid, on)
    }

    // MARK: NSTableViewDataSource

    func numberOfRows(in tableView: NSTableView) -> Int {
        keys.count
    }

    // MARK: NSTableViewDelegate

    func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int) -> NSView? {
        guard row >= 0, row < keys.count, let r = rowsByKey[keys[row]] else { return nil }
        let cell = table.makeView(withIdentifier: MessageCellView.reuseIdentifier, owner: nil) as? MessageCellView
            ?? MessageCellView()
        configure(cell, r)
        return cell
    }

    func tableView(_ tableView: NSTableView, rowViewForRow row: Int) -> NSTableRowView? {
        let rowView = table.makeView(withIdentifier: MessageRowView.reuseIdentifier, owner: nil) as? MessageRowView
            ?? MessageRowView()
        rowView.isMember = row >= 0 && row < keys.count ? (rowsByKey[keys[row]]?.member ?? false) : false
        return rowView
    }

    func tableViewSelectionDidChange(_ notification: Notification) {
        guard !isReselecting else { return }
        let row = table.selectedRow
        list.select(key: row >= 0 && row < keys.count ? keys[row] : nil)
    }
}
