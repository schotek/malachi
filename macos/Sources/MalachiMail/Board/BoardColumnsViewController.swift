// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The board's Columns style: the four states side by side on the board's
/// tinted surface, each a header (the state's title in its colour and the
/// count) over its own table of cards in a vertical scroll view. The first
/// column ends with the user's commitments under "From the Assistant"; an
/// empty column shows a dashed placeholder. The columns share the width
/// equally, none narrower than `minColumnWidth`: on a narrower page the
/// outer scroll view scrolls sideways (it never makes the window wider).
///
/// One selection across the four tables: the table that holds the
/// selected case selects its row, the others none. The selection lives in
/// `BoardController`; the tables follow it in `apply(_:)` without
/// reloading. The detail of the selected case is the page's sliding panel.
@MainActor
final class BoardColumnsViewController: NSViewController, BoardStyleContent {
    var onToast: ((String) -> Void)?

    private let controller: BoardController
    private let outer = NSScrollView()
    private var panes: [Pane] = []
    /// Set while the tables are changed from here, so their delegate
    /// callbacks do not count as the user's.
    private var updating = false

    /// A column narrower than this scrolls the page sideways instead.
    private static let minColumnWidth: CGFloat = 200
    /// Around the columns and between them (the prototype's 14 and 12).
    private static let padding: CGFloat = 14
    private static let columnGap: CGFloat = 12
    /// The header's horizontal inset, so the title lines up with the
    /// card's edge.
    private static let headerInset: CGFloat = 2
    /// The placeholder's padding above and below its text.
    private static let placeholderPadding: CGFloat = 18

    /// The case actions of the context menus.
    private let actions: BoardActions

    init(actions: BoardActions) {
        self.actions = actions
        controller = actions.controller
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    // MARK: View

    override func loadView() {
        let root = SurfaceView()
        root.translatesAutoresizingMaskIntoConstraints = false

        let content = NSView()
        content.translatesAutoresizingMaskIntoConstraints = false
        outer.documentView = content
        outer.drawsBackground = false
        outer.hasHorizontalScroller = true
        outer.hasVerticalScroller = false
        outer.autohidesScrollers = true
        outer.verticalScrollElasticity = .none
        outer.automaticallyAdjustsContentInsets = false
        outer.translatesAutoresizingMaskIntoConstraints = false
        outer.setContentHuggingPriority(.defaultLow, for: .horizontal)
        outer.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        root.addSubview(outer)

        let clip = outer.contentView
        // The page's width, unless four minimum columns need more: then
        // the document is wider and scrolls. Preferred only (≤ 250), so
        // nothing here holds the window wider than its minimum.
        let fill = content.widthAnchor.constraint(equalTo: clip.widthAnchor)
        fill.priority = .defaultLow
        let safe = root.safeAreaLayoutGuide
        var constraints = [
            outer.topAnchor.constraint(equalTo: safe.topAnchor),
            outer.bottomAnchor.constraint(equalTo: root.bottomAnchor),
            outer.leadingAnchor.constraint(equalTo: root.leadingAnchor),
            outer.trailingAnchor.constraint(equalTo: root.trailingAnchor),
            content.leadingAnchor.constraint(equalTo: clip.leadingAnchor),
            content.topAnchor.constraint(equalTo: clip.topAnchor),
            content.heightAnchor.constraint(equalTo: clip.heightAnchor),
            content.widthAnchor.constraint(greaterThanOrEqualTo: clip.widthAnchor),
            fill,
        ]

        for i in Board.State.allCases.indices {
            let pane = makePane()
            panes.append(pane)
            content.addSubview(pane.container)
            constraints += [
                pane.container.topAnchor.constraint(equalTo: content.topAnchor, constant: Self.padding),
                pane.container.bottomAnchor.constraint(equalTo: content.bottomAnchor, constant: -Self.padding),
                pane.container.widthAnchor.constraint(greaterThanOrEqualToConstant: Self.minColumnWidth),
            ]
            if i == 0 {
                constraints.append(pane.container.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: Self.padding))
            } else {
                let previous = panes[i - 1].container
                constraints += [
                    pane.container.leadingAnchor.constraint(equalTo: previous.trailingAnchor, constant: Self.columnGap),
                    pane.container.widthAnchor.constraint(equalTo: panes[0].container.widthAnchor),
                ]
            }
        }
        if let last = panes.last {
            constraints.append(content.trailingAnchor.constraint(equalTo: last.container.trailingAnchor, constant: Self.padding))
        }
        NSLayoutConstraint.activate(constraints)
        view = root
    }

    /// One column: the header over a vertical scroll view with the table.
    private func makePane() -> Pane {
        let header = BoardSectionHeaderView(horizontalPadding: Self.headerInset)
        header.translatesAutoresizingMaskIntoConstraints = false

        let table = BoardColumnTableView()
        let column = NSTableColumn(identifier: NSUserInterfaceItemIdentifier("case"))
        column.resizingMask = .autoresizingMask
        table.addTableColumn(column)
        table.headerView = nil
        table.style = .plain
        table.backgroundColor = .clear
        table.usesAutomaticRowHeights = false
        table.intercellSpacing = .zero
        table.allowsEmptySelection = true
        table.allowsMultipleSelection = false
        table.columnAutoresizingStyle = .uniformColumnAutoresizingStyle
        table.selectionHighlightStyle = .regular
        table.dataSource = self
        table.delegate = self
        table.onStep = { [weak self, weak table] delta in
            guard let self, let table else { return false }
            return self.step(table, delta)
        }
        table.onSideways = { [weak self, weak table] delta in
            guard let self, let table else { return false }
            return self.sideways(table, delta)
        }
        // The panel is open on the selection already: Return keeps it.
        table.onActivate = { [weak table] in
            (table?.selectedRow ?? -1) >= 0
        }
        table.menuForRow = { [weak self, weak table] row in
            guard let self, let table, let pane = self.pane(of: table), row < pane.items.count,
                  let id = pane.items[row].caseID
            else { return nil }
            return BoardCaseMenu.contextMenu(for: id, actions: self.actions)
        }

        let scroll = ColumnScrollView()
        scroll.documentView = table
        scroll.drawsBackground = false
        scroll.hasVerticalScroller = true
        scroll.hasHorizontalScroller = false
        scroll.autohidesScrollers = true
        scroll.horizontalScrollElasticity = .none
        scroll.automaticallyAdjustsContentInsets = false
        scroll.translatesAutoresizingMaskIntoConstraints = false

        let container = NSView()
        container.translatesAutoresizingMaskIntoConstraints = false
        container.addSubview(header)
        container.addSubview(scroll)
        let headerHeight = BoardMetrics.sectionHeader
        NSLayoutConstraint.activate([
            header.topAnchor.constraint(equalTo: container.topAnchor),
            header.leadingAnchor.constraint(equalTo: container.leadingAnchor),
            header.trailingAnchor.constraint(equalTo: container.trailingAnchor),
            header.heightAnchor.constraint(equalToConstant: headerHeight),
            scroll.topAnchor.constraint(equalTo: header.bottomAnchor),
            scroll.leadingAnchor.constraint(equalTo: container.leadingAnchor),
            scroll.trailingAnchor.constraint(equalTo: container.trailingAnchor),
            scroll.bottomAnchor.constraint(equalTo: container.bottomAnchor),
        ])
        return Pane(container: container, header: header, table: table)
    }

    override func viewDidLoad() {
        super.viewDidLoad()
        render(force: true)
    }

    override func viewWillAppear() {
        super.viewWillAppear()
        // Changes may have gone by while another style showed.
        render(force: true)
    }

    // MARK: BoardStyleContent

    func apply(_ changes: BoardController.Changes) {
        guard isViewLoaded else { return }
        if !changes.isDisjoint(with: [.content, .filters, .style]) {
            render(force: false)
        } else if changes.contains(.selection) {
            reflectSelection()
        }
    }

    var focusTarget: NSView? {
        loadViewIfNeeded()
        return (panes.first { $0.table.selectedRow >= 0 } ?? panes.first { $0.hasSelectableRow } ?? panes.first)?.table
    }

    func focusContent() {
        guard let target = focusTarget else { return }
        view.window?.makeFirstResponder(target)
    }

    // MARK: Content

    /// Brings the four tables up to `controller.view`: a table reloads
    /// only when its rows changed (or `force`); the row it had selected
    /// stays selected when it is still there.
    private func render(force: Bool) {
        let v = controller.view
        for (i, column) in v.columns.enumerated() where i < panes.count {
            let pane = panes[i]
            pane.header.configure(column)
            pane.table.setAccessibilityLabel(column.title)

            var items: [Item] = column.rows.isEmpty ? [.placeholder(column.emptyText)] : column.rows.map(Item.card)
            if i == 0, !v.commitments.isEmpty {
                items.append(.assistantHeading(v.commitments.count))
                items += v.commitments.map(Item.commitment)
            }
            guard force || items != pane.items else { continue }

            let selected = pane.table.selectedRow
            let held = selected >= 0 && selected < pane.items.count ? pane.items[selected].key : nil
            pane.items = items
            updating = true
            pane.table.reloadData()
            if let held, let row = items.firstIndex(where: { $0.key == held }) {
                pane.table.selectRowIndexes(IndexSet(integer: row), byExtendingSelection: false)
            }
            updating = false
        }
        reflectSelection()
    }

    // MARK: Selection

    /// Shows `controller.view.selection` in the tables without reloading:
    /// a row that shows the case already keeps it (a commitment stays the
    /// selected row when the user picked it), otherwise the case's card
    /// is selected and scrolled to; every other table selects nothing.
    /// While the keyboard is in a column it follows the selection.
    private func reflectSelection() {
        updating = true
        defer { updating = false }
        var holder: Pane?
        if let id = controller.view.selection {
            if let pane = panes.first(where: { $0.selectedCaseID == id }) {
                holder = pane
            } else if let (pane, row) = locate(id) {
                pane.table.selectRowIndexes(IndexSet(integer: row), byExtendingSelection: false)
                reveal(pane, row: row)
                holder = pane
            }
        }
        for pane in panes where pane !== holder && pane.table.selectedRow >= 0 {
            pane.table.deselectAll(nil)
        }
        if let holder, let window = view.window, panes.contains(where: { window.firstResponder === $0.table }),
           window.firstResponder !== holder.table {
            window.makeFirstResponder(holder.table)
        }
    }

    /// The card of case `id`, else a commitment of it.
    private func locate(_ id: Board.CaseID) -> (Pane, Int)? {
        for pane in panes {
            if let row = pane.items.firstIndex(where: { $0.cardID == id }) {
                return (pane, row)
            }
        }
        for pane in panes {
            if let row = pane.items.firstIndex(where: { $0.caseID == id }) {
                return (pane, row)
            }
        }
        return nil
    }

    /// Scrolls the row into view in its column, and the column into view
    /// on a page too narrow for all four.
    private func reveal(_ pane: Pane, row: Int) {
        pane.table.scrollRowToVisible(row)
        pane.container.scrollToVisible(pane.container.bounds)
    }

    /// The user selected `row` of `pane` (a key or a click): the other
    /// tables let go, and the controller selects the case.
    private func choose(_ pane: Pane, row: Int, focus: Bool) {
        guard row < pane.items.count, let id = pane.items[row].caseID else { return }
        updating = true
        if pane.table.selectedRow != row {
            pane.table.selectRowIndexes(IndexSet(integer: row), byExtendingSelection: false)
        }
        for other in panes where other !== pane && other.table.selectedRow >= 0 {
            other.table.deselectAll(nil)
        }
        updating = false
        reveal(pane, row: row)
        if focus, let window = view.window, window.firstResponder !== pane.table {
            window.makeFirstResponder(pane.table)
        }
        controller.select(id)
    }

    // MARK: Keys

    /// Up and Down: the previous or next selectable row of the column; at
    /// its end the selection stays. Without a selection Down takes the
    /// first row and Up the last.
    private func step(_ table: NSTableView, _ delta: Int) -> Bool {
        guard let pane = pane(of: table) else { return false }
        let rows = pane.selectableRows
        guard !rows.isEmpty else { return true }
        let current = table.selectedRow
        let target: Int?
        if current < 0 {
            target = delta > 0 ? rows.first : rows.last
        } else if delta > 0 {
            target = rows.first { $0 > current }
        } else {
            target = rows.last { $0 < current }
        }
        if let target {
            choose(pane, row: target, focus: false)
        }
        return true
    }

    /// Left and Right: the neighbouring column's row at the same position
    /// (among the selectable rows), the last one when the column is
    /// shorter; an empty column is passed over. At the edge nothing moves.
    private func sideways(_ table: NSTableView, _ delta: Int) -> Bool {
        guard let from = panes.firstIndex(where: { $0.table === table }) else { return false }
        let position = panes[from].selectableRows.firstIndex(of: table.selectedRow) ?? 0
        var i = from + delta
        while panes.indices.contains(i) {
            let rows = panes[i].selectableRows
            if let last = rows.last {
                choose(panes[i], row: position < rows.count ? rows[position] : last, focus: true)
                return true
            }
            i += delta
        }
        return true
    }

    private func pane(of table: NSTableView) -> Pane? {
        panes.first { $0.table === table }
    }

    // MARK: Metrics

    private static var placeholderRow: CGFloat {
        BoardMetrics.lineHeight(Typo.body) + 2 * placeholderPadding + 2 * BoardMetrics.cardInsetV
    }
}

// MARK: - Table

extension BoardColumnsViewController: NSTableViewDataSource, NSTableViewDelegate {
    func numberOfRows(in tableView: NSTableView) -> Int {
        pane(of: tableView)?.items.count ?? 0
    }

    func tableView(_ tableView: NSTableView, heightOfRow row: Int) -> CGFloat {
        guard let item = pane(of: tableView)?.items[safe: row] else { return BoardMetrics.columnCard }
        switch item {
        case .card: return BoardMetrics.columnCard
        case .placeholder: return Self.placeholderRow
        case .assistantHeading: return BoardMetrics.sectionHeader
        case .commitment: return BoardMetrics.commitmentCard
        }
    }

    func tableView(_ tableView: NSTableView, rowViewForRow row: Int) -> NSTableRowView? {
        guard let item = pane(of: tableView)?.items[safe: row] else { return nil }
        let style: BoardCardRowView.Style
        switch item {
        case .card(let r): style = r.state == .hot ? .hot : .plain
        case .placeholder: style = .placeholder
        case .commitment: style = .assistant
        case .assistantHeading: return HeadingRowView()
        }
        let rowView = tableView.makeView(withIdentifier: BoardCardRowView.reuseIdentifier, owner: nil) as? BoardCardRowView
            ?? BoardCardRowView()
        rowView.style = style
        return rowView
    }

    func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int) -> NSView? {
        guard let item = pane(of: tableView)?.items[safe: row] else { return nil }
        switch item {
        case .card(let r):
            let cell = tableView.makeView(withIdentifier: BoardCaseContentView.reuseIdentifier, owner: nil) as? BoardCaseContentView
                ?? BoardCaseContentView(titleLines: 1, snippetLines: 2, card: true)
            cell.configure(r)
            return cell
        case .placeholder(let text):
            let cell = tableView.makeView(withIdentifier: PlaceholderCellView.reuseIdentifier, owner: nil) as? PlaceholderCellView
                ?? PlaceholderCellView()
            cell.configure(text)
            return cell
        case .assistantHeading(let n):
            let cell = tableView.makeView(withIdentifier: BoardSectionHeaderView.reuseIdentifier, owner: nil) as? BoardSectionHeaderView
                ?? BoardSectionHeaderView(horizontalPadding: Self.headerInset)
            cell.configureCommitments(count: n)
            return cell
        case .commitment(let k):
            let cell = tableView.makeView(withIdentifier: BoardCommitmentContentView.reuseIdentifier, owner: nil)
                as? BoardCommitmentContentView ?? BoardCommitmentContentView(card: true)
            cell.configure(k)
            cell.onTick = { [weak controller = self.controller] in controller?.setCommitmentDone(k.id, done: true) }
            return cell
        }
    }

    func tableView(_ tableView: NSTableView, shouldSelectRow row: Int) -> Bool {
        pane(of: tableView)?.items[safe: row]?.caseID != nil
    }

    func tableViewSelectionDidChange(_ notification: Notification) {
        guard !updating, let table = notification.object as? NSTableView, let pane = pane(of: table) else { return }
        let row = table.selectedRow
        if row >= 0 {
            choose(pane, row: row, focus: false)
        } else if !panes.contains(where: { $0.table.selectedRow >= 0 }) {
            // A click on the column's empty space.
            controller.select(nil)
        }
    }
}

// MARK: - Parts

/// What a row of a column's table shows.
private enum Item: Equatable {
    case card(Board.Row)
    /// The empty column's dashed card, with its text.
    case placeholder(String)
    /// "From the Assistant" over the commitments, with their count.
    case assistantHeading(Int)
    case commitment(Board.CommitmentRow)

    /// What keeps a row selected across a reload.
    enum Key: Hashable {
        case card(Board.CaseID)
        case commitment(String)
    }

    var key: Key? {
        switch self {
        case .card(let r): return .card(r.id)
        case .commitment(let k): return .commitment(k.id)
        case .placeholder, .assistantHeading: return nil
        }
    }

    /// The case the row selects; nil for a row that is not selectable.
    var caseID: Board.CaseID? {
        switch self {
        case .card(let r): return r.id
        case .commitment(let k): return k.caseID
        case .placeholder, .assistantHeading: return nil
        }
    }

    /// The case of a card; nil for any other row.
    var cardID: Board.CaseID? {
        if case .card(let r) = self {
            return r.id
        }
        return nil
    }
}

/// One column: its view (header and scroll view), its table and the rows
/// the table shows.
@MainActor
private final class Pane {
    let container: NSView
    let header: BoardSectionHeaderView
    let table: BoardColumnTableView
    var items: [Item] = []

    init(container: NSView, header: BoardSectionHeaderView, table: BoardColumnTableView) {
        self.container = container
        self.header = header
        self.table = table
    }

    var selectableRows: [Int] {
        items.indices.filter { items[$0].caseID != nil }
    }

    var hasSelectableRow: Bool {
        items.contains { $0.caseID != nil }
    }

    /// The case of the selected row, if any.
    var selectedCaseID: Board.CaseID? {
        let row = table.selectedRow
        return row >= 0 && row < items.count ? items[row].caseID : nil
    }
}

/// The page's background: the board's tinted surface, resolved when it
/// draws (dark mode, high contrast).
@MainActor
private final class SurfaceView: NSView {
    override init(frame: NSRect) {
        super.init(frame: frame)
        wantsLayer = true
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override var wantsUpdateLayer: Bool { true }

    override func updateLayer() {
        layer?.backgroundColor = BoardPalette.surface.cgColor
    }
}

/// A column's vertical scroll view that hands a sideways gesture to the
/// page's horizontal scroll view instead of swallowing it. The direction
/// is decided when a trackpad gesture begins and kept through its
/// momentum; a mouse wheel decides per event (Shift-wheel is sideways).
@MainActor
private final class ColumnScrollView: NSScrollView {
    private var sideways = false

    override func scrollWheel(with event: NSEvent) {
        if event.phase == .began || (event.phase.isEmpty && event.momentumPhase.isEmpty) {
            sideways = abs(event.scrollingDeltaX) > abs(event.scrollingDeltaY)
        }
        if sideways, let outer = enclosingScrollView {
            outer.scrollWheel(with: event)
            return
        }
        super.scrollWheel(with: event)
    }
}

/// The row of the "From the Assistant" heading: nothing drawn, never
/// selected.
@MainActor
private final class HeadingRowView: NSTableRowView {
    override func drawBackground(in dirtyRect: NSRect) {}
    override func drawSelection(in dirtyRect: NSRect) {}
    override func drawSeparator(in dirtyRect: NSRect) {}
}

/// The text of an empty column's dashed card ("Nothing burning.",
/// "Empty."), centred.
@MainActor
private final class PlaceholderCellView: NSTableCellView {
    static let reuseIdentifier = NSUserInterfaceItemIdentifier("BoardColumnPlaceholder")

    private let label = NSTextField(labelWithString: "")

    init() {
        super.init(frame: .zero)
        identifier = Self.reuseIdentifier
        label.font = Typo.body
        label.textColor = Tint.secondary
        label.alignment = .center
        label.lineBreakMode = .byTruncatingTail
        label.maximumNumberOfLines = 1
        label.isSelectable = false
        label.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        label.translatesAutoresizingMaskIntoConstraints = false
        label.setAccessibilityElement(false)
        addSubview(label)
        let padH = BoardMetrics.cardPaddingH + BoardMetrics.cardInsetH
        NSLayoutConstraint.activate([
            label.leadingAnchor.constraint(equalTo: leadingAnchor, constant: padH),
            trailingAnchor.constraint(equalTo: label.trailingAnchor, constant: padH),
            label.centerYAnchor.constraint(equalTo: centerYAnchor),
        ])
        setAccessibilityElement(true)
        setAccessibilityRole(.staticText)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    func configure(_ text: String) {
        label.stringValue = text
        setAccessibilityLabel(text)
    }
}

private extension Array {
    subscript(safe i: Int) -> Element? {
        indices.contains(i) ? self[i] : nil
    }
}
