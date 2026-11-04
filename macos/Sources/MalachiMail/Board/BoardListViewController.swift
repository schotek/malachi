// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The board's List style ("Seznam"): a split view of its own with the
/// navigation column as its sidebar (full height, the window's buttons at
/// its top), the sectioned list of cases and the detail of the selected one
/// beside it, resizable like the mail's panes. The board's toolbar cuts its
/// sections at these dividers (`BoardToolbar`: AppKit's sidebar tracking
/// separator and one on `splitView`'s second divider), so the List is the
/// only split view in the window while it shows (the mail's leaves the
/// window in the Board mode, `MainContentViewController.setMode`).
/// Below 900 pt the navigation column folds away (it comes back on widening
/// unless the user folded it, as in the mail's split view); below 640 pt,
/// or where the list and the detail no longer fit beside an open navigation
/// column, the detail does too and the page's sliding panel shows it instead
/// (`setInlineDetail`). All state is the controller's: the table only
/// mirrors `controller.view`.
@MainActor
final class BoardListViewController: NSSplitViewController, BoardStyleContent {
    private let controller: BoardController

    private let nav: BoardNavView
    private let table = BoardListTableView()
    private let scroll = NSScrollView()
    private let emptyLabel = NSTextField(labelWithString: "")
    /// `View.notice` above the list (the cases are partial or old): in the
    /// list's pane, so the navigation column keeps running up behind the
    /// toolbar. The other styles have the page's.
    private let notice = BannerView(symbol: "info.circle", severity: .info)
    private let noticeHost = FillStackView()
    private let detail: BoardDetailViewController
    private let noSelection = StatusPageView(
        illustration: .symbol("tray"), title: Board.Text.noSelectionTitle, description: Board.Text.noSelectionBody)
    /// The three panes' controllers; the views go into their views.
    private let navPane = BoardListPane()
    private let listPane = BoardListPane()
    private let detailPane = BoardListPane()
    private var navItem: NSSplitViewItem!
    private var listItem: NSSplitViewItem!
    private var detailItem: NSSplitViewItem!

    private var items: [Item] = []
    /// Set while the table follows the controller, so the selection it
    /// reports back is not sent to the controller again.
    private var syncing = false
    /// Whether the detail is beside the list, as the layout last decided.
    private var showsDetail = true
    /// The navigation column folded by the width, to come back on widening;
    /// a fold by the user (the toolbar's toggle) stays.
    private var navAutoCollapsed = false
    private var navWidthClassWide: Bool?
    private var appliedDefaultLayout = false
    /// The row last scrolled into view for the selection, and whether it
    /// still has to be (after the next layout, when the clip view has its
    /// final size).
    private var revealedIndex: Int?
    private var revealPending = false

    var onToast: ((String) -> Void)? {
        didSet { detail.onToast = onToast }
    }

    private enum Item {
        case header(title: String, count: Int, color: NSColor)
        /// "From the Assistant" with the commitments' count.
        case commitmentsHeader(count: Int)
        case commitment(Board.CommitmentRow)
        case row(Board.Row)
    }

    /// The navigation column: wide enough for the window's buttons, the
    /// Mail/Board switch and the sidebar toggle in its toolbar section.
    static let navMinimum: CGFloat = 240
    static let navDefault: CGFloat = 248
    static let navMaximum: CGFloat = 320
    /// The list: its toolbar section holds the style switch, the account
    /// filter and Triage (sections do not overflow, their items push the
    /// divider's separator).
    static let listMinimum: CGFloat = 372
    static let listDefault: CGFloat = 400
    static let listMaximum: CGFloat = 520
    static let listAloneMinimum: CGFloat = 200
    /// With the list's minimum, the detail folds below 640 pt, as before.
    static let detailMinimum: CGFloat = 266
    private static let hideNavBelow: CGFloat = 900
    private static let hideDetailBelow: CGFloat = 640

    /// The case actions of the context menu and the detail.
    private let actions: BoardActions

    init(actions: BoardActions) {
        self.actions = actions
        let controller = actions.controller
        self.controller = controller
        nav = BoardNavView(controller: controller)
        detail = BoardDetailViewController(actions: actions, presentation: .pane)
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    // MARK: View

    override func viewDidLoad() {
        super.viewDidLoad()
        splitView.isVertical = true
        splitView.dividerStyle = .thin

        let navItem = NSSplitViewItem(sidebarWithViewController: navPane)
        navItem.minimumThickness = Self.navMinimum
        navItem.maximumThickness = Self.navMaximum
        navItem.holdingPriority = NSLayoutConstraint.Priority(260)
        navItem.canCollapse = true
        // As the mail's sidebar: a fold gives the width to the siblings,
        // never to the window.
        navItem.collapseBehavior = .preferResizingSiblingsWithFixedSplitView
        navItem.allowsFullHeightLayout = true
        navItem.titlebarSeparatorStyle = .none
        self.navItem = navItem

        let listItem = NSSplitViewItem(contentListWithViewController: listPane)
        listItem.minimumThickness = Self.listMinimum
        listItem.maximumThickness = Self.listMaximum
        listItem.holdingPriority = NSLayoutConstraint.Priority(255)
        listItem.canCollapse = false
        self.listItem = listItem

        let detailItem = NSSplitViewItem(viewController: detailPane)
        detailItem.minimumThickness = Self.detailMinimum
        detailItem.holdingPriority = NSLayoutConstraint.Priority(250)
        detailItem.canCollapse = true
        detailItem.collapseBehavior = .preferResizingSiblingsWithFixedSplitView
        self.detailItem = detailItem

        // The default widths, before the first layout.
        navPane.view.setFrameSize(NSSize(width: Self.navDefault, height: 400))
        listPane.view.setFrameSize(NSSize(width: Self.listDefault, height: 400))
        addSplitViewItem(navItem)
        addSplitViewItem(listItem)
        addSplitViewItem(detailItem)

        configureTable()
        addChild(detail)

        emptyLabel.font = Typo.body
        emptyLabel.textColor = .secondaryLabelColor
        emptyLabel.alignment = .center
        emptyLabel.maximumNumberOfLines = 0
        emptyLabel.lineBreakMode = .byWordWrapping
        emptyLabel.isHidden = true

        for v in [nav, scroll, emptyLabel, detail.view, noSelection] {
            v.translatesAutoresizingMaskIntoConstraints = false
        }
        // Each pane's content starts under the toolbar; the sidebar's
        // material runs up behind it.
        let navView = navPane.view
        navView.addSubview(nav)
        let listView = listPane.view
        noticeHost.addArrangedSubview(notice)
        listView.addSubview(noticeHost)
        listView.addSubview(scroll)
        listView.addSubview(emptyLabel)
        NSLayoutConstraint.activate([
            nav.topAnchor.constraint(equalTo: navView.safeAreaLayoutGuide.topAnchor),
            nav.bottomAnchor.constraint(equalTo: navView.bottomAnchor),
            nav.leadingAnchor.constraint(equalTo: navView.leadingAnchor),
            nav.trailingAnchor.constraint(equalTo: navView.trailingAnchor),
            noticeHost.topAnchor.constraint(equalTo: listView.safeAreaLayoutGuide.topAnchor),
            noticeHost.leadingAnchor.constraint(equalTo: listView.leadingAnchor),
            noticeHost.trailingAnchor.constraint(equalTo: listView.trailingAnchor),
            scroll.topAnchor.constraint(equalTo: noticeHost.bottomAnchor),
            scroll.bottomAnchor.constraint(equalTo: listView.bottomAnchor),
            scroll.leadingAnchor.constraint(equalTo: listView.leadingAnchor),
            scroll.trailingAnchor.constraint(equalTo: listView.trailingAnchor),
            emptyLabel.centerXAnchor.constraint(equalTo: listView.centerXAnchor),
            emptyLabel.centerYAnchor.constraint(equalTo: listView.centerYAnchor),
            emptyLabel.leadingAnchor.constraint(greaterThanOrEqualTo: listView.leadingAnchor, constant: 16),
            listView.trailingAnchor.constraint(greaterThanOrEqualTo: emptyLabel.trailingAnchor, constant: 16),
        ])
        attachDetail(true)
        render()
    }

    /// Puts the detail and the status page into their pane, or takes them
    /// out of it while it is folded away (a folded pane keeps its frame,
    /// and nothing in it should take part in the layout meanwhile).
    private func attachDetail(_ on: Bool) {
        let host = detailPane.view
        let attached = detail.view.superview === host
        guard on != attached else { return }
        if !on {
            for v in [detail.view, noSelection] {
                v.removeFromSuperview()
            }
            return
        }
        for v in [detail.view, noSelection] {
            host.addSubview(v)
            NSLayoutConstraint.activate([
                v.leadingAnchor.constraint(equalTo: host.leadingAnchor),
                v.trailingAnchor.constraint(equalTo: host.trailingAnchor),
                v.topAnchor.constraint(equalTo: host.topAnchor),
                v.bottomAnchor.constraint(equalTo: host.bottomAnchor),
            ])
        }
    }

    private func configureTable() {
        let column = NSTableColumn(identifier: NSUserInterfaceItemIdentifier("BoardListColumn"))
        column.resizingMask = .autoresizingMask
        column.minWidth = 0
        table.addTableColumn(column)
        table.headerView = nil
        table.style = .plain
        table.intercellSpacing = .zero
        table.usesAutomaticRowHeights = false
        table.allowsMultipleSelection = false
        table.allowsEmptySelection = true
        table.selectionHighlightStyle = .regular
        table.columnAutoresizingStyle = .uniformColumnAutoresizingStyle
        table.dataSource = self
        table.delegate = self
        table.setAccessibilityLabel(Board.Text.styleTitle(.list))
        table.onReturn = { [weak self] in self?.focusDetail() }
        table.menuProvider = { [weak self] row in self?.menu(forRow: row) }

        scroll.documentView = table
        scroll.hasVerticalScroller = true
        scroll.autohidesScrollers = true
        scroll.drawsBackground = false
        scroll.borderType = .noBorder
        scroll.automaticallyAdjustsContentInsets = false
    }

    override func viewWillAppear() {
        super.viewWillAppear()
        // The fold is decided before the first render, from the width the
        // page gives this view (its own frame is still empty when it was
        // just put in): coming from Columns or Today into a wide List, the
        // page's panel must not show over the List until a later pass. The
        // slot's width, not this view's: that is the one it had when it
        // last showed (the window may have narrowed under another style).
        if let width = view.superview?.bounds.width, width > 0 {
            layoutPanes(width: width, immediately: true)
        }
        render()
    }

    /// The window put the List's toolbar in place: the first time, the
    /// panes get their default widths once the toolbar's tracking
    /// separators had their say (they would leave the list at its
    /// maximum, as in the mail's split view).
    func toolbarInstalled() {
        guard !appliedDefaultLayout else { return }
        appliedDefaultLayout = true
        DispatchQueue.main.async { [weak self] in
            self?.applyDefaultWidths()
        }
    }

    /// The window is about to take `width` (a resize by the user, a tile, a
    /// zoom): the folds are decided before AppKit lays the panes out, or the
    /// minimums of panes about to fold would keep the window wider.
    func willResize(toWidth width: CGFloat) {
        guard isViewLoaded, view.window != nil else { return }
        layoutPanes(width: width, immediately: true)
    }

    /// Folded panes are left alone: positioning a divider would unfold
    /// them.
    private func applyDefaultWidths() {
        guard splitView.bounds.width > 0 else { return }
        var x: CGFloat = 0
        if !navItem.isCollapsed {
            splitView.setPosition(Self.navDefault, ofDividerAt: 0)
            x = Self.navDefault + splitView.dividerThickness
        }
        if !detailItem.isCollapsed {
            splitView.setPosition(x + Self.listDefault, ofDividerAt: 1)
        }
    }

    override func viewDidLayout() {
        super.viewDidLayout()
        layoutPanes(width: view.bounds.width)
        if revealPending {
            revealPending = false
            // After the whole pass: the clip view has its size then.
            DispatchQueue.main.async { [weak self] in
                self?.revealSelection()
            }
        }
    }

    // MARK: Narrow window

    /// The toolbar's sidebar toggle: the user's choice from then on, the
    /// width no longer brings the column back.
    override func toggleSidebar(_ sender: Any?) {
        // Unfolding in a narrow window: the detail gives way first, or the
        // three panes' minimums would make the window wider.
        if navItem.isCollapsed {
            layoutPanes(width: view.bounds.width, immediately: true, navOpen: true)
        }
        super.toggleSidebar(sender)
        navAutoCollapsed = false
    }

    /// View ▸ Show/Hide Sidebar says what it does, as for the mail's.
    override func validateUserInterfaceItem(_ item: any NSValidatedUserInterfaceItem) -> Bool {
        if item.action == Action.toggleSidebar, let menuItem = item as? NSMenuItem {
            // macOS-only strings
            menuItem.title = navItem?.isCollapsed == true ? "Show Sidebar" : "Hide Sidebar"
        }
        return super.validateUserInterfaceItem(item)
    }

    /// Folds the navigation column and the detail by the page's width, only
    /// when a threshold is crossed (`navOpen`: as if the column were open,
    /// for the toggle that is about to open it). The controller hears of the detail's
    /// fold after the layout pass (it answers with a change that reloads),
    /// or at once outside of one (`immediately`).
    private func layoutPanes(width: CGFloat, immediately: Bool = false, navOpen: Bool? = nil) {
        guard width > 0, isViewLoaded, navItem != nil else { return }
        let wide = width >= Self.hideNavBelow
        if wide != navWidthClassWide {
            navWidthClassWide = wide
            // Not animated: a pane that folds keeps its minimum in the
            // window's minimum width until it is folded, and the window
            // would stay wider than it is being made.
            if !wide, !navItem.isCollapsed {
                navAutoCollapsed = true
                navItem.isCollapsed = true
            } else if wide, navAutoCollapsed {
                navAutoCollapsed = false
                navItem.isCollapsed = false
            }
        }
        // The detail stays beside the list while both keep their minimums
        // next to an open navigation column.
        let open = navOpen ?? !navItem.isCollapsed
        let navThickness = open ? max(navPane.view.frame.width, Self.navMinimum) + splitView.dividerThickness : 0
        let room = width - navThickness
        let detail = width >= Self.hideDetailBelow
            && room >= Self.listMinimum + splitView.dividerThickness + Self.detailMinimum
        guard detail != showsDetail || controller.state.inlineDetail != detail else { return }
        if !detail, let window = view.window, let focus = window.firstResponder as? NSView,
           focus.isDescendant(of: detailPane.view)
        {
            // The keyboard stays in the board, not with the window.
            window.makeFirstResponder(table)
        }
        if detail != showsDetail {
            showsDetail = detail
            if detail {
                listItem.minimumThickness = Self.listMinimum
                detailItem.minimumThickness = Self.detailMinimum
                attachDetail(true)
                detailItem.isCollapsed = false
                // The list keeps its width; the detail takes the rest.
                listItem.maximumThickness = Self.listMaximum
            } else {
                detailItem.isCollapsed = true
                // A folded pane keeps its minimum in the window's minimum
                // width; the list takes the whole width meanwhile.
                detailItem.minimumThickness = 0
                listItem.maximumThickness = NSSplitViewItem.unspecifiedDimension
                // Alone, the list goes down to the window's minimum; its
                // toolbar items then overflow like any other.
                listItem.minimumThickness = Self.listAloneMinimum
                attachDetail(false)
            }
        }
        if immediately {
            controller.setInlineDetail(detail)
        } else {
            DispatchQueue.main.async { [weak self] in
                guard let self, self.controller.state.inlineDetail != detail else { return }
                self.controller.setInlineDetail(detail)
            }
        }
    }

    // MARK: BoardStyleContent

    var focusTarget: NSView? { table }

    func focusContent() {
        view.window?.makeFirstResponder(table)
    }

    /// The navigation column runs up behind the toolbar, the window's
    /// buttons at its top: the page puts this view at its very top.
    var extendsUnderToolbar: Bool { true }

    func apply(_ changes: BoardController.Changes) {
        detail.apply(changes)
        if !changes.isDisjoint(with: [.content, .filters]) {
            render()
        } else if changes.contains(.selection) {
            syncSelection()
            updateDetailVisibility()
        }
    }

    // MARK: Rendering

    /// Rebuilds the rows and the navigation from the view model.
    private func render() {
        guard isViewLoaded else { return }
        let model = controller.view
        nav.configure(model)
        items = Self.items(for: model)
        syncing = true
        table.reloadData()
        syncing = false
        syncSelection()
        notice.title = model.notice
        notice.reveal(!model.notice.isEmpty, animated: false)
        let empty = model.sections.isEmpty
        emptyLabel.stringValue = model.sectionsEmptyText
        emptyLabel.isHidden = !empty
        scroll.isHidden = empty
        updateDetailVisibility()
    }

    private static func items(for model: Board.View) -> [Item] {
        var items: [Item] = []
        if model.showsCommitmentsInList, !model.commitments.isEmpty {
            items.append(.commitmentsHeader(count: model.commitments.count))
            items.append(contentsOf: model.commitments.map { .commitment($0) })
        }
        for section in model.sections {
            items.append(.header(title: section.title, count: section.rows.count, color: BoardPalette.accent(section.kind)))
            items.append(contentsOf: section.rows.map { .row($0) })
        }
        return items
    }

    private func updateDetailVisibility() {
        let has = controller.view.detail != nil && controller.view.selection != nil
        detail.view.isHidden = !has
        noSelection.isHidden = has
    }

    /// Selects the row of the controller's selection, with no reload and
    /// without telling the controller, and scrolls to it after the next
    /// layout when it is another row than before. A selected commitment
    /// stays selected while its case is the selection (as in Columns), so
    /// ↑ and ↓ walk through the commitments.
    private func syncSelection() {
        guard isViewLoaded else { return }
        let selection = controller.view.selection
        let current = table.selectedRow
        if items.indices.contains(current), case .commitment(let c) = items[current], c.caseID == selection {
            return
        }
        let index = selection.flatMap { id in
            items.firstIndex { if case .row(let r) = $0 { return r.id == id } else { return false } }
        }
        syncing = true
        defer { syncing = false }
        if let index {
            if current != index {
                table.selectRowIndexes(IndexSet(integer: index), byExtendingSelection: false)
            }
            if index != revealedIndex {
                revealedIndex = index
                revealPending = true
                view.needsLayout = true
            }
        } else {
            table.deselectAll(nil)
            revealedIndex = nil
        }
    }

    /// Brings the selected row into view unless it is in view already.
    /// The first case (the one the board selects by itself) and any row
    /// that fits on the first screen show the list's top with them: the
    /// commitments and the header above it stay in view.
    private func revealSelection() {
        guard let index = revealedIndex, items.indices.contains(index), table.selectedRow == index else { return }
        let clip = scroll.contentView
        let visible = clip.documentVisibleRect
        guard visible.height > 0 else { return }
        let top = -scroll.contentInsets.top
        let scrollToTop = { [scroll] in
            guard visible.minY > top + 0.5 else { return }
            clip.scroll(to: NSPoint(x: visible.minX, y: top))
            scroll.reflectScrolledClipView(clip)
        }
        let firstCase = items.firstIndex { if case .row = $0 { return true } else { return false } }
        if index == firstCase {
            scrollToTop()
            return
        }
        let row = table.rect(ofRow: index)
        if visible.contains(row) { return }
        if row.maxY <= visible.height + top {
            scrollToTop()
        } else {
            table.scrollRowToVisible(index)
        }
    }

    /// Return: to the detail beside the list. The narrow List's detail is
    /// the page's panel, which has its own way in (a click, Tab).
    private func focusDetail() {
        guard showsDetail, !detail.view.isHidden, let target = detail.focusTarget else { return }
        view.window?.makeFirstResponder(target)
    }

    private func menu(forRow row: Int) -> NSMenu? {
        guard items.indices.contains(row) else { return nil }
        switch items[row] {
        case .row(let r): return BoardCaseMenu.contextMenu(for: r.id, actions: actions)
        case .commitment(let c): return BoardCaseMenu.contextMenu(for: c.caseID, actions: actions)
        case .header, .commitmentsHeader: return nil
        }
    }
}

// MARK: - Table

extension BoardListViewController: NSTableViewDataSource, NSTableViewDelegate {
    func numberOfRows(in tableView: NSTableView) -> Int {
        items.count
    }

    func tableView(_ tableView: NSTableView, heightOfRow row: Int) -> CGFloat {
        switch items[row] {
        case .header, .commitmentsHeader: return BoardMetrics.sectionHeader
        case .commitment: return BoardMetrics.commitmentRow
        case .row: return BoardMetrics.listCaseRow
        }
    }

    func tableView(_ tableView: NSTableView, shouldSelectRow row: Int) -> Bool {
        switch items[row] {
        case .header, .commitmentsHeader: return false
        case .row, .commitment: return true
        }
    }

    func tableView(_ tableView: NSTableView, rowViewForRow row: Int) -> NSTableRowView? {
        switch items[row] {
        case .header, .commitmentsHeader: return NSTableRowView()
        case .row, .commitment: break
        }
        let id = MessageRowView.reuseIdentifier
        return tableView.makeView(withIdentifier: id, owner: nil) as? MessageRowView ?? MessageRowView()
    }

    func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int) -> NSView? {
        switch items[row] {
        case .header(let title, let count, let color):
            let id = BoardSectionHeaderView.reuseIdentifier
            let header = tableView.makeView(withIdentifier: id, owner: nil) as? BoardSectionHeaderView ?? BoardSectionHeaderView()
            header.configure(title: title, count: count, color: color)
            return header
        case .commitmentsHeader(let count):
            let id = BoardSectionHeaderView.reuseIdentifier
            let header = tableView.makeView(withIdentifier: id, owner: nil) as? BoardSectionHeaderView ?? BoardSectionHeaderView()
            header.configureCommitments(count: count)
            return header
        case .commitment(let c):
            let id = BoardCommitmentContentView.reuseIdentifier
            let cell = tableView.makeView(withIdentifier: id, owner: nil) as? BoardCommitmentContentView ?? BoardCommitmentContentView()
            cell.configure(c)
            cell.onTick = { [weak controller = self.controller] in controller?.setCommitmentDone(c.id, done: true) }
            return cell
        case .row(let r):
            let id = BoardCaseContentView.reuseIdentifier
            let cell = tableView.makeView(withIdentifier: id, owner: nil) as? BoardCaseContentView ?? BoardCaseContentView()
            cell.configure(r)
            return cell
        }
    }

    func tableViewSelectionDidChange(_ notification: Notification) {
        guard !syncing else { return }
        let row = table.selectedRow
        guard items.indices.contains(row) else {
            // A click on empty space: beside the detail the list always
            // shows its case, so the row comes back; the narrow List closes
            // the panel.
            if showsDetail {
                syncSelection()
            } else {
                controller.select(nil)
            }
            return
        }
        switch items[row] {
        case .row(let r): controller.select(r.id)
        case .commitment(let c): controller.select(c.caseID)
        case .header, .commitmentsHeader: break
        }
    }
}

// MARK: - Helpers

/// The list's table: Return moves the focus to the detail, the context menu
/// comes from the controller's menu for the row under the pointer and
/// leaves the selection alone.
@MainActor
private final class BoardListTableView: NSTableView {
    var onReturn: (() -> Void)?
    var menuProvider: ((Int) -> NSMenu?)?

    override func keyDown(with event: NSEvent) {
        // Return and keypad Enter.
        if event.keyCode == 36 || event.keyCode == 76 {
            onReturn?()
            return
        }
        super.keyDown(with: event)
    }

    override func menu(for event: NSEvent) -> NSMenu? {
        let row = self.row(at: convert(event.locationInWindow, from: nil))
        // AppKit's own `menu(for:)` draws the outline round the clicked row.
        menu = row >= 0 ? menuProvider?(row) : nil
        return menu == nil ? nil : super.menu(for: event)
    }
}

/// One pane of the List's split view; its owner fills the view.
@MainActor
private final class BoardListPane: NSViewController {
    override func loadView() {
        let v = NSView()
        v.translatesAutoresizingMaskIntoConstraints = false
        view = v
    }
}
