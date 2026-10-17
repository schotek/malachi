// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The folder sidebar (window.blp lines 36–86): the outline of accounts,
/// pinned folders and folders with their badges and stars, and the status
/// page that replaces it while there is nothing to list. A native source
/// list: headings and folders fold with the disclosure chevron at the start
/// of the row, always visible like the GTK twisty (the hover button of
/// group rows was easy to miss); Left/Right are the outline's own. The
/// status line GTK has at the bottom of the sidebar is the window's status
/// bar here (`StatusBarViewController`), so the outline runs to the bottom.
///
/// A right click (or a Control-click) on a folder or an account heading
/// opens its context menu (ui/internal/contextmenu `FolderItems`,
/// `AccountItems`), which acts on the row clicked and leaves the selection
/// alone; the Favourites heading has none.
///
/// The controller owns every decision; this view mirrors `model.entries`
/// and sends clicks back. It installs the sidebar-facing callbacks of the
/// mailbox controller (`onEntriesChanged`, `onBadgesChanged`,
/// `onFolderStatus`, `onSelectionChanged`).
@MainActor
final class FolderSidebarViewController: NSViewController, NSOutlineViewDelegate {
    let mailbox: MailboxController
    /// Mark All as Read and Empty Trash or Junk of a folder's context menu
    /// (`ActionsController`), installed by the integration.
    var onMarkFolderRead: (@MainActor (FolderKey) -> Void)?
    var onEmptyFolder: (@MainActor (FolderKey) -> Void)?

    private let outline = FolderOutlineView()
    private let scroll = NSScrollView()
    private let statusView = SidebarStatusView()
    private let dataSource = FolderOutlineDataSource()
    /// A programmatic selection change must not re-enter `selectFolder`
    /// (window.go `reselecting`).
    private var isReselecting = false
    /// Expansion re-applied after a rebuild must not be read as the user
    /// folding something.
    private var isApplyingExpansion = false

    init(mailbox: MailboxController) {
        self.mailbox = mailbox
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    // MARK: View

    override func loadView() {
        outline.style = .sourceList
        outline.rowSizeStyle = .custom
        outline.rowHeight = SidebarMetrics.rowHeight
        outline.indentationPerLevel = SidebarMetrics.indentPerLevel
        outline.indentationMarkerFollowsCell = true
        outline.floatsGroupRows = false
        outline.allowsMultipleSelection = false
        outline.allowsEmptySelection = true
        outline.autosaveExpandedItems = false
        outline.headerView = nil
        outline.usesAutomaticRowHeights = false
        outline.columnAutoresizingStyle = .uniformColumnAutoresizingStyle
        let column = NSTableColumn(identifier: NSUserInterfaceItemIdentifier("folder"))
        column.resizingMask = .autoresizingMask
        outline.addTableColumn(column)
        outline.outlineTableColumn = column
        outline.dataSource = dataSource
        outline.delegate = self
        outline.menuForRow = { [weak self] row in self?.menu(forRow: row) }

        scroll.documentView = outline
        scroll.hasVerticalScroller = true
        scroll.hasHorizontalScroller = false
        scroll.autohidesScrollers = true
        scroll.drawsBackground = false
        scroll.translatesAutoresizingMaskIntoConstraints = false

        statusView.translatesAutoresizingMaskIntoConstraints = false
        statusView.isHidden = true

        let container = NSView()
        container.addSubview(scroll)
        container.addSubview(statusView)
        NSLayoutConstraint.activate([
            scroll.topAnchor.constraint(equalTo: container.topAnchor),
            scroll.leadingAnchor.constraint(equalTo: container.leadingAnchor),
            scroll.trailingAnchor.constraint(equalTo: container.trailingAnchor),
            scroll.bottomAnchor.constraint(equalTo: container.bottomAnchor),
            statusView.topAnchor.constraint(equalTo: scroll.topAnchor),
            statusView.leadingAnchor.constraint(equalTo: scroll.leadingAnchor),
            statusView.trailingAnchor.constraint(equalTo: scroll.trailingAnchor),
            statusView.bottomAnchor.constraint(equalTo: scroll.bottomAnchor),
        ])
        view = container
    }

    override func viewDidLoad() {
        super.viewDidLoad()
        mailbox.onEntriesChanged = { [weak self] in self?.rebuildRows() }
        mailbox.onBadgesChanged = { [weak self] in self?.refreshBadges() }
        mailbox.onFolderStatus = { [weak self] status in self?.show(status) }
        mailbox.onSelectionChanged = { [weak self] key, fav in self?.highlightFolderRow(key, fav: fav) }
        // What the controller holds already, for a sidebar attached late.
        rebuildRows()
        show(mailbox.sidebarStatus)
        if let sel = mailbox.model.selected {
            highlightFolderRow(sel, fav: mailbox.model.selectedFav)
        }
    }

    // MARK: Rows

    /// Recreates the outline from `model.entries` (folders.go
    /// `rebuildFolderList`, the rows): cached nodes keep their identity
    /// through `reloadData()`, then the folds of the model are re-applied.
    /// The selection follows through `onSelectionChanged`.
    private func rebuildRows() {
        let model = mailbox.model
        // With several accounts a pinned "Inbox" says whose it is.
        let several = model.mailSidebarAccounts.count >= 2
        isReselecting = true
        dataSource.rebuild(entries: model.entries, several: several)
        outline.reloadData()
        applyExpansion()
        isReselecting = false
    }

    /// Refreshes every visible row after the badges moved (folders.go
    /// `updateFolderRow`): the entries have the same shape, only the numbers
    /// changed, so the rows are reloaded in place and the selection stays.
    private func refreshBadges() {
        let model = mailbox.model
        dataSource.rebuild(entries: model.entries, several: model.mailSidebarAccounts.count >= 2)
        let rows = outline.numberOfRows
        guard rows > 0 else { return }
        isReselecting = true
        outline.reloadData(forRowIndexes: IndexSet(integersIn: 0..<rows), columnIndexes: IndexSet(integer: 0))
        isReselecting = false
    }

    /// Expands and collapses the items as the model says, top down so that
    /// children are reachable once their parent is open. The Favourites
    /// section is always open.
    private func applyExpansion() {
        isApplyingExpansion = true
        defer { isApplyingExpansion = false }
        for root in dataSource.roots {
            applyExpansion(root)
        }
    }

    private func applyExpansion(_ item: NSObject) {
        switch item {
        case let f as FavouritesNode:
            outline.expandItem(f)
        case let a as AccountNode:
            if a.entry.collapsed {
                outline.collapseItem(a)
            } else {
                outline.expandItem(a)
                for child in a.children {
                    applyExpansion(child)
                }
            }
        case let f as FolderNode:
            guard f.entry.hasChildren else { return }
            if f.entry.collapsed {
                outline.collapseItem(f)
            } else {
                outline.expandItem(f)
                for child in f.children {
                    applyExpansion(child)
                }
            }
        default:
            break
        }
    }

    /// Switches between the list and the status page (folders.go
    /// `showFolderStatus`, window.blp `folder_stack`).
    private func show(_ status: SidebarStatus) {
        switch status {
        case .folders:
            statusView.isHidden = true
            scroll.isHidden = false
        case .status(let icon, let title, let description):
            statusView.show(icon: icon, title: title, description: description)
            statusView.isHidden = false
            scroll.isHidden = true
        }
    }

    /// Selects a folder's row without re-entering `selectFolder`
    /// (folders.go `highlightFolderRow`). A pinned folder has two rows: the
    /// one the user clicked last is preferred, the other one stands in when
    /// it is folded away or was just unpinned. Nothing happens while the row
    /// is out of sight under a fold; nil clears the selection.
    private func highlightFolderRow(_ key: FolderKey?, fav: Bool) {
        guard let key else {
            isReselecting = true
            outline.deselectAll(nil)
            isReselecting = false
            return
        }
        guard let node = dataSource.node(key, fav: fav) ?? dataSource.node(key, fav: !fav) else { return }
        let row = outline.row(forItem: node)
        guard row >= 0, outline.selectedRow != row else { return }
        isReselecting = true
        outline.selectRowIndexes(IndexSet(integer: row), byExtendingSelection: false)
        isReselecting = false
    }

    // MARK: NSOutlineViewDelegate

    func outlineView(_ outlineView: NSOutlineView, shouldSelectItem item: Any) -> Bool {
        guard let node = item as? FolderNode, let folder = node.entry.folder else { return false }
        return folder.selectable
    }

    func outlineView(_ outlineView: NSOutlineView, shouldShowOutlineCellForItem item: Any) -> Bool {
        // The Favourites section does not fold; its chevron's space stays,
        // so the headings line up (folders.go `newHeaderRow`).
        !(item is FavouritesNode)
    }

    func outlineView(_ outlineView: NSOutlineView, shouldCollapseItem item: Any) -> Bool {
        !(item is FavouritesNode)
    }

    func outlineView(_ outlineView: NSOutlineView, heightOfRowByItem item: Any) -> CGFloat {
        switch item {
        case let node as FolderNode:
            return node.subtitle.isEmpty ? SidebarMetrics.rowHeight : SidebarMetrics.subtitleRowHeight
        default:
            return SidebarMetrics.headerRowHeight
        }
    }

    func outlineView(_ outlineView: NSOutlineView, rowViewForItem item: Any) -> NSTableRowView? {
        item is FolderNode ? FolderRowView() : nil
    }

    func outlineView(_ outlineView: NSOutlineView, viewFor tableColumn: NSTableColumn?, item: Any) -> NSView? {
        switch item {
        case is FavouritesNode:
            let cell = headerCell()
            cell.configure(text: L10n.T("Favourites"))
            return cell
        case let node as AccountNode:
            let cell = headerCell()
            cell.configure(text: node.entry.account.map(accountLabel) ?? "", tag: node.entry.account.map(accountHeaderBadge) ?? "")
            return cell
        case let node as FolderNode:
            let cell = outline.makeView(withIdentifier: FolderCellView.reuseIdentifier, owner: nil) as? FolderCellView
                ?? FolderCellView()
            cell.configure(entry: node.entry, subtitle: node.subtitle)
            // A view of an issue account has the daemon's English name as
            // its path: the tooltip is the title the row shows.
            cell.toolTip = node.entry.folder.map { $0.virtual == nil ? $0.path : folderTitle($0) }
            let key = node.key
            cell.onToggleFavourite = { [weak self] in self?.mailbox.toggleFavourite(key) }
            return cell
        default:
            return nil
        }
    }

    private func headerCell() -> SidebarHeaderCellView {
        outline.makeView(withIdentifier: SidebarHeaderCellView.reuseIdentifier, owner: nil) as? SidebarHeaderCellView
            ?? SidebarHeaderCellView()
    }

    func outlineViewSelectionDidChange(_ notification: Notification) {
        guard !isReselecting else { return }
        let row = outline.selectedRow
        // An emptied selection changes nothing, as the GTK row-selected
        // handler ignores a nil row.
        guard row >= 0, let node = outline.item(atRow: row) as? FolderNode else { return }
        mailbox.selectFolder(node.key, fav: node.fav)
    }

    func outlineViewItemDidExpand(_ notification: Notification) {
        folded(notification, collapsed: false)
    }

    func outlineViewItemDidCollapse(_ notification: Notification) {
        folded(notification, collapsed: true)
    }

    /// The user folded a heading or a folder (collapse.go `toggleAccount`,
    /// `toggleFolder`). The model changes once the outline is out of its own
    /// notification: the rebuild that follows reloads the rows.
    private func folded(_ notification: Notification, collapsed: Bool) {
        guard !isApplyingExpansion, let item = notification.userInfo?["NSObject"] else { return }
        if let account = item as? AccountNode {
            let id = account.id
            Task { @MainActor [weak self] in
                self?.mailbox.setAccountCollapsed(id, collapsed)
            }
        } else if let folder = item as? FolderNode, folder.entry.hasChildren {
            let key = folder.key
            Task { @MainActor [weak self] in
                self?.mailbox.setFolderCollapsed(key, collapsed)
            }
        }
    }
}

// MARK: - Context menu

extension FolderSidebarViewController {
    /// The menu of the outline's `row`: a folder's or an account
    /// heading's, its items closures over the row clicked; nil for the
    /// Favourites heading or nothing to offer.
    fileprivate func menu(forRow row: Int) -> NSMenu? {
        let node = outline.item(atRow: row)
        var groups: [[ContextMenu.Item]] = []
        var labels = ContextMenu.Labels()
        var run: [ContextMenu.ItemID: @MainActor () -> Void] = [:]
        let mailbox = self.mailbox
        // Folding goes through the outline, whose delegate tells the
        // mailbox (`outlineViewItemDidExpand`/`Collapse`).
        let fold: @MainActor () -> Void = { [weak outline] in
            guard let outline else { return }
            if outline.isItemExpanded(node) {
                outline.collapseItem(node)
            } else {
                outline.expandItem(node)
            }
        }
        switch node {
        case let a as AccountNode:
            let id = a.id
            groups = ContextMenu.accountItems(foldable: outline.isExpandable(a))
            labels.collapsed = !outline.isItemExpanded(a)
            run[.checkMail] = { mailbox.triggerSync(accountId: id) }
            run[.fold] = fold
        case let f as FolderNode:
            let k = f.key
            guard let folder = mailbox.model.folder(k) ?? f.entry.folder else { return nil }
            let account = mailbox.model.account(k.account)
            groups = ContextMenu.folderItems(ContextMenu.FolderState(
                selectable: folder.selectable, role: folder.role, unread: folder.unread, total: folder.total,
                canDelete: account?.can(.delete) ?? false, foldable: !f.fav && outline.isExpandable(f)))
            labels = ContextMenu.Labels(junk: folder.role == .junk, favourite: f.entry.starred,
                                        collapsed: !outline.isItemExpanded(f))
            run[.checkMail] = { mailbox.triggerSync(folder: folder) }
            run[.markAllRead] = { [weak self] in self?.onMarkFolderRead?(k) }
            run[.emptyFolder] = { [weak self] in self?.onEmptyFolder?(k) }
            run[.favourite] = { mailbox.toggleFavourite(k) }
            run[.fold] = fold
        default:
            return nil
        }
        guard !groups.isEmpty else { return nil }
        let menu = NSMenu()
        menu.autoenablesItems = false
        for (i, group) in groups.enumerated() {
            if i > 0 {
                menu.addItem(.separator())
            }
            for it in group {
                let action = run[it.id] ?? {}
                let item = BoardMenuAction.item(mn(ContextMenu.label(it.id, labels)), action)
                item.isEnabled = it.enabled && run[it.id] != nil
                menu.addItem(item)
            }
        }
        return menu
    }
}

/// The sidebar's outline: a right click (or a Control-click) asks
/// `menuForRow` for the row's context menu.
@MainActor
final class FolderOutlineView: NSOutlineView {
    var menuForRow: (@MainActor (Int) -> NSMenu?)?

    override func menu(for event: NSEvent) -> NSMenu? {
        let row = row(at: convert(event.locationInWindow, from: nil))
        // AppKit's own `menu(for:)` draws the outline round the clicked
        // row; the selection stays.
        menu = row >= 0 ? menuForRow?(row) : nil
        return menu == nil ? nil : super.menu(for: event)
    }
}
