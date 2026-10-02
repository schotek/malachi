// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The board's Today style ("Dnes"): a greeting, the count tiles, what is
/// hot, the top of what waits for the user and what the assistant says
/// the user promised, in one table; beside it (under it in a narrow
/// window) the deadlines and a placeholder for the calendar. Every text is
/// a cleaned plain string of the view model, shown through `stringValue`.
///
/// Port of: ui/internal/board (not yet; Swift-first, see
/// MalachiCore/Board/BoardView.swift).
@MainActor
final class BoardTodayViewController: NSViewController, BoardStyleContent, NSTableViewDataSource, NSTableViewDelegate {
    /// The rows of the main column, in order.
    private enum Item {
        case header
        case tiles
        case section(title: String, count: Int, color: NSColor)
        case caseRow(Board.Row)
        case text(String)
        case more(Int)
        case commitment(Board.CommitmentRow)
        case commitmentsHeading(Int)
    }

    private let controller: BoardController
    private let table = BoardTodayTableView()
    private let scroll = NSScrollView()
    private let side = BoardTodaySideView()
    private var items: [Item] = []
    private var wiredButtons: [NSView] = []
    /// Set while this view moves the table's selection itself.
    private var syncing = false

    var onToast: ((String) -> Void)?

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
        let column = NSTableColumn(identifier: NSUserInterfaceItemIdentifier("today"))
        column.resizingMask = .autoresizingMask
        table.addTableColumn(column)
        table.headerView = nil
        table.style = .plain
        table.allowsMultipleSelection = false
        table.allowsEmptySelection = true
        table.usesAutomaticRowHeights = false
        table.intercellSpacing = .zero
        table.gridStyleMask = []
        table.backgroundColor = .clear
        table.columnAutoresizingStyle = .uniformColumnAutoresizingStyle
        table.dataSource = self
        table.delegate = self
        table.target = self
        table.action = #selector(rowClicked)
        table.onReturn = { [weak self] in self?.activateSelectedRow() }
        table.menuProvider = { [weak self] row in self?.menu(forRow: row) }
        table.setAccessibilityLabel(Board.Text.styleTitle(.today))

        scroll.documentView = table
        scroll.hasVerticalScroller = true
        scroll.drawsBackground = false
        scroll.borderType = .noBorder
        scroll.automaticallyAdjustsContentInsets = false

        side.onSelect = { [weak self] id in self?.controller.select(id) }

        let container = BoardTodayContainer(main: scroll, side: side)
        view = container
    }

    override func viewDidLoad() {
        super.viewDidLoad()
        render()
    }

    override func viewWillAppear() {
        super.viewWillAppear()
        render()
    }

    // MARK: BoardStyleContent

    func apply(_ changes: BoardController.Changes) {
        guard isViewLoaded else { return }
        if changes.contains(.content) {
            render()
        } else if changes.contains(.selection) {
            syncSelection()
        }
    }

    var focusTarget: NSView? { table }

    func focusContent() {
        view.window?.makeFirstResponder(table)
    }

    // MARK: Rendering

    private func render() {
        let today = controller.view.today
        var next: [Item] = [.header, .tiles]
        next.append(.section(title: Board.Text.stateName(.hot), count: today.hot.count, color: BoardPalette.accent(.hot)))
        if today.hot.isEmpty {
            let empty = controller.view.columns.first { $0.state == .hot }?.emptyText ?? Board.Text.sectionEmpty
            next.append(.text(empty))
        } else {
            next += today.hot.map { .caseRow($0) }
        }
        next.append(
            .section(
                title: Board.Text.stateName(.you), count: today.you.count + today.youMore,
                color: BoardPalette.accent(.you)))
        if today.you.isEmpty {
            let empty = controller.view.columns.first { $0.state == .you }?.emptyText ?? Board.Text.sectionEmpty
            next.append(.text(empty))
        } else {
            next += today.you.map { .caseRow($0) }
        }
        if today.youMore > 0 {
            next.append(.more(today.youMore))
        }
        if !today.commitments.isEmpty {
            next.append(.commitmentsHeading(today.commitments.count))
            next += today.commitments.map { .commitment($0) }
        }
        items = next
        syncing = true
        table.reloadData()
        syncing = false
        side.configure(today)
        // The deadlines come after the table in the key-view order.
        wireKeyViews()
        syncSelection()
        view.needsLayout = true
    }

    /// Puts the deadline buttons after the table in the key-view loop (the
    /// main window does not recalculate it), keeping what followed the table.
    private func wireKeyViews() {
        let buttons = side.keyViews
        var following = table.nextKeyView
        if let f = following, wiredButtons.contains(where: { $0 === f }) {
            following = wiredButtons.last?.nextKeyView
        }
        wiredButtons = buttons
        guard let first = buttons.first else {
            table.nextKeyView = following
            return
        }
        table.nextKeyView = first
        for (a, b) in zip(buttons, buttons.dropFirst()) {
            a.nextKeyView = b
        }
        buttons.last?.nextKeyView = following
    }

    /// Makes the table show `controller.view.selection` without reloading.
    private func syncSelection() {
        let selection = controller.view.selection
        // A selected commitment stays while its case is the selection.
        let current = table.selectedRow
        if current >= 0, case .commitment(let k) = items[current], k.caseID == selection {
            return
        }
        let target = selection.flatMap { id in
            items.firstIndex { item in
                if case .caseRow(let r) = item { return r.id == id }
                return false
            }
        }
        if let target {
            if table.selectedRow != target {
                syncing = true
                table.selectRowIndexes(IndexSet(integer: target), byExtendingSelection: false)
                syncing = false
                table.scrollRowToVisible(target)
            }
        } else if selection == nil || current >= 0 {
            // Nothing selected, or the case is not a row here; the "and N
            // more" row keeps its highlight only while it is clicked.
            if current >= 0, case .more = items[current], selection != nil { return }
            syncing = true
            table.deselectAll(nil)
            syncing = false
        }
    }

    // MARK: Actions

    @objc private func rowClicked() {
        let row = table.clickedRow
        guard row >= 0, row < items.count, case .more = items[row] else { return }
        controller.showWaitingForYou()
    }

    private func activateSelectedRow() {
        let row = table.selectedRow
        guard row >= 0, row < items.count else { return }
        switch items[row] {
        case .more:
            controller.showWaitingForYou()
        case .caseRow(let r):
            controller.select(r.id)
        case .commitment(let k):
            controller.select(k.caseID)
        default:
            break
        }
    }

    private func menu(forRow row: Int) -> NSMenu? {
        guard row >= 0, row < items.count else { return nil }
        switch items[row] {
        case .caseRow(let r): return BoardCaseMenu.contextMenu(for: r.id, actions: actions)
        case .commitment(let k): return BoardCaseMenu.contextMenu(for: k.caseID, actions: actions)
        default: return nil
        }
    }

    // MARK: NSTableViewDataSource

    func numberOfRows(in tableView: NSTableView) -> Int {
        items.count
    }

    // MARK: NSTableViewDelegate

    func tableView(_ tableView: NSTableView, heightOfRow row: Int) -> CGFloat {
        switch items[row] {
        case .header: return BoardTodayMetrics.headerRow
        case .tiles: return BoardTodayMetrics.tilesRow
        case .section, .commitmentsHeading: return BoardMetrics.todaySectionHeader
        case .caseRow: return BoardMetrics.todayRow
        case .text, .more: return BoardTodayMetrics.textRow
        case .commitment: return BoardMetrics.commitmentRow
        }
    }

    func tableView(_ tableView: NSTableView, shouldSelectRow row: Int) -> Bool {
        switch items[row] {
        case .caseRow, .more, .commitment: return true
        default: return false
        }
    }

    func tableView(_ tableView: NSTableView, rowViewForRow row: Int) -> NSTableRowView? {
        switch items[row] {
        case .caseRow, .more, .commitment:
            let id = MessageRowView.reuseIdentifier
            return tableView.makeView(withIdentifier: id, owner: nil) as? MessageRowView ?? MessageRowView()
        default:
            return nil
        }
    }

    func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int) -> NSView? {
        switch items[row] {
        case .header:
            let cell = reuse(BoardTodayHeaderView.self, BoardTodayHeaderView.reuseIdentifier) { BoardTodayHeaderView() }
            let today = controller.view.today
            cell.configure(title: today.title, phrase: today.phrase)
            return cell
        case .tiles:
            let cell = reuse(BoardTodayTilesView.self, BoardTodayTilesView.reuseIdentifier) { BoardTodayTilesView() }
            cell.configure(controller.view.today.tiles)
            return cell
        case .section(let title, let count, let color):
            let cell = reuse(BoardSectionHeaderView.self, BoardSectionHeaderView.reuseIdentifier) { BoardSectionHeaderView() }
            cell.configure(title: title, count: count, color: color)
            return cell
        case .commitmentsHeading(let n):
            let cell = reuse(BoardSectionHeaderView.self, BoardSectionHeaderView.reuseIdentifier) { BoardSectionHeaderView() }
            cell.configureCommitments(count: n)
            return cell
        case .caseRow(let r):
            let cell = reuse(BoardCaseContentView.self, BoardCaseContentView.reuseIdentifier) {
                BoardCaseContentView(compact: true)
            }
            cell.configure(r)
            return cell
        case .text(let text):
            let cell = reuse(BoardTodayTextView.self, BoardTodayTextView.reuseIdentifier) { BoardTodayTextView() }
            cell.configure(text, accent: false)
            return cell
        case .more(let n):
            let cell = reuse(BoardTodayTextView.self, BoardTodayTextView.reuseIdentifier) { BoardTodayTextView() }
            cell.configure(Board.Text.andMore(n), accent: true)
            return cell
        case .commitment(let k):
            let cell = reuse(BoardCommitmentContentView.self, BoardCommitmentContentView.reuseIdentifier) {
                BoardCommitmentContentView()
            }
            cell.configure(k)
            cell.onTick = { [weak controller = self.controller] in controller?.setCommitmentDone(k.id, done: true) }
            return cell
        }
    }

    func tableViewSelectionDidChange(_ notification: Notification) {
        guard !syncing else { return }
        let row = table.selectedRow
        guard row >= 0, row < items.count else {
            // A click on empty space: the panel closes with the selection.
            if row < 0 { controller.select(nil) }
            return
        }
        switch items[row] {
        case .caseRow(let r): controller.select(r.id)
        case .commitment(let k): controller.select(k.caseID)
        default: break  // "and N more" acts on a click or Return, not on an arrow
        }
    }

    private func reuse<V: NSView>(_ type: V.Type, _ id: NSUserInterfaceItemIdentifier, make: () -> V) -> V {
        table.makeView(withIdentifier: id, owner: nil) as? V ?? make()
    }
}

// MARK: - Metrics

/// The fixed measures of the Today page.
@MainActor
private enum BoardTodayMetrics {
    static let padding: CGFloat = BoardMetrics.listPaddingH
    static var headingFont: NSFont { .systemFont(ofSize: Typo.title1.pointSize + 6, weight: .bold) }
    static var countFont: NSFont { .systemFont(ofSize: Typo.title1.pointSize + 8, weight: .bold) }
    static let tileRadius: CGFloat = 10
    static let tilePadding: CGFloat = 10
    static let tileGap: CGFloat = 8

    static var headerRow: CGFloat {
        20 + BoardMetrics.lineHeight(headingFont) + BoardMetrics.lineGap + BoardMetrics.lineHeight(Typo.body) + 12
    }
    static var tileHeight: CGFloat {
        2 * tilePadding + BoardMetrics.lineHeight(countFont) + BoardMetrics.lineHeight(Typo.caption)
    }
    static var tilesRow: CGFloat { tileHeight + 8 }
    static var textRow: CGFloat { BoardMetrics.lineHeight(Typo.body) + 2 * 8 }

    static let sideMin: CGFloat = 280
    static let sidePreferred: CGFloat = 440
    /// Below this page width the side column goes under the main one.
    static let narrow: CGFloat = 760
}

// MARK: - Table

/// The table: Return activates the selected row, the menu is the row's.
@MainActor
private final class BoardTodayTableView: NSTableView {
    var onReturn: (() -> Void)?
    var menuProvider: ((Int) -> NSMenu?)?

    override func keyDown(with event: NSEvent) {
        if event.keyCode == 36 || event.keyCode == 76, event.modifierFlags.intersection(.deviceIndependentFlagsMask).isEmpty {
            onReturn?()
            return
        }
        super.keyDown(with: event)
    }

    override func menu(for event: NSEvent) -> NSMenu? {
        // Through AppKit's `menu(for:)`, which outlines the clicked row
        // without changing the selection.
        menu = menuProvider?(row(at: convert(event.locationInWindow, from: nil)))
        return menu == nil ? nil : super.menu(for: event)
    }
}

// MARK: - Rows

@MainActor
private final class BoardTodayHeaderView: NSView {
    static let reuseIdentifier = NSUserInterfaceItemIdentifier("BoardTodayHeader")

    private let title = NSTextField(labelWithString: "")
    private let phrase = NSTextField(labelWithString: "")

    init() {
        super.init(frame: .zero)
        identifier = Self.reuseIdentifier
        title.font = BoardTodayMetrics.headingFont
        title.lineBreakMode = .byTruncatingTail
        title.isSelectable = false
        phrase.font = Typo.body
        phrase.textColor = Tint.secondary
        phrase.lineBreakMode = .byTruncatingTail
        phrase.isSelectable = false
        for label in [title, phrase] {
            label.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
            label.translatesAutoresizingMaskIntoConstraints = false
            label.setAccessibilityElement(false)
            addSubview(label)
        }
        let pad = BoardTodayMetrics.padding
        NSLayoutConstraint.activate([
            title.leadingAnchor.constraint(equalTo: leadingAnchor, constant: pad),
            title.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -pad),
            title.topAnchor.constraint(equalTo: topAnchor, constant: 20),
            phrase.leadingAnchor.constraint(equalTo: title.leadingAnchor),
            phrase.trailingAnchor.constraint(equalTo: title.trailingAnchor),
            phrase.topAnchor.constraint(equalTo: title.bottomAnchor, constant: BoardMetrics.lineGap),
        ])
        setAccessibilityElement(true)
        setAccessibilityRole(.staticText)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    func configure(title text: String, phrase line: String) {
        title.stringValue = text
        phrase.stringValue = line
        setAccessibilityLabel(text + ". " + line)
    }
}

/// The count tiles in one row.
@MainActor
private final class BoardTodayTilesView: NSView {
    static let reuseIdentifier = NSUserInterfaceItemIdentifier("BoardTodayTiles")

    private let stack = NSStackView()

    init() {
        super.init(frame: .zero)
        identifier = Self.reuseIdentifier
        stack.orientation = .horizontal
        stack.distribution = .fillEqually
        stack.spacing = BoardTodayMetrics.tileGap
        stack.translatesAutoresizingMaskIntoConstraints = false
        addSubview(stack)
        let pad = BoardTodayMetrics.padding
        NSLayoutConstraint.activate([
            stack.leadingAnchor.constraint(equalTo: leadingAnchor, constant: pad),
            stack.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -pad),
            stack.topAnchor.constraint(equalTo: topAnchor),
            stack.heightAnchor.constraint(equalToConstant: BoardTodayMetrics.tileHeight),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    func configure(_ tiles: [Board.Tile]) {
        for old in stack.arrangedSubviews {
            stack.removeArrangedSubview(old)
            old.removeFromSuperview()
        }
        for tile in tiles {
            stack.addArrangedSubview(BoardTodayTileView(tile))
        }
    }
}

@MainActor
private final class BoardTodayTileView: NSView {
    private let tile: Board.Tile
    private let count = NSTextField(labelWithString: "")
    private let title = NSTextField(labelWithString: "")

    init(_ tile: Board.Tile) {
        self.tile = tile
        super.init(frame: .zero)
        count.stringValue = String(tile.count)
        count.font = BoardTodayMetrics.countFont
        count.textColor = Self.colour(tile.kind)
        title.stringValue = tile.title
        title.font = Typo.caption
        title.textColor = Tint.secondary
        title.lineBreakMode = .byTruncatingTail
        for label in [count, title] {
            label.isSelectable = false
            label.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
            label.translatesAutoresizingMaskIntoConstraints = false
            label.setAccessibilityElement(false)
            addSubview(label)
        }
        let pad = BoardTodayMetrics.tilePadding
        NSLayoutConstraint.activate([
            count.leadingAnchor.constraint(equalTo: leadingAnchor, constant: pad),
            count.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -pad),
            count.topAnchor.constraint(equalTo: topAnchor, constant: pad),
            title.leadingAnchor.constraint(equalTo: count.leadingAnchor),
            title.trailingAnchor.constraint(equalTo: count.trailingAnchor),
            title.topAnchor.constraint(equalTo: count.bottomAnchor),
        ])
        // Static text for VoiceOver: "3 Hot".
        setAccessibilityElement(true)
        setAccessibilityRole(.staticText)
        setAccessibilityLabel("\(tile.count) \(tile.title)")
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    private static func colour(_ kind: Board.TileKind) -> NSColor {
        switch kind {
        case .state(let s): return BoardPalette.accent(s)
        case .commitments: return BoardPalette.assistant
        }
    }

    private var isHot: Bool {
        if case .state(.hot) = tile.kind { return true }
        return false
    }

    override func draw(_ dirtyRect: NSRect) {
        let path = NSBezierPath(
            roundedRect: bounds.insetBy(dx: 0.5, dy: 0.5), xRadius: BoardTodayMetrics.tileRadius,
            yRadius: BoardTodayMetrics.tileRadius)
        (isHot ? BoardPalette.hotCardFill : BoardPalette.cardFill).setFill()
        path.fill()
        (isHot ? BoardPalette.hotCardBorder : BoardPalette.cardBorder(in: self)).setStroke()
        path.lineWidth = BoardMetrics.cardBorderWidth
        path.stroke()
    }

    override func viewDidChangeEffectiveAppearance() {
        super.viewDidChangeEffectiveAppearance()
        needsDisplay = true
    }
}

/// An empty section's text, or the "and N more" row.
@MainActor
private final class BoardTodayTextView: NSTableCellView {
    static let reuseIdentifier = NSUserInterfaceItemIdentifier("BoardTodayText")

    private let label = NSTextField(labelWithString: "")
    private var accent = false

    init() {
        super.init(frame: .zero)
        identifier = Self.reuseIdentifier
        label.font = Typo.body
        label.lineBreakMode = .byTruncatingTail
        label.isSelectable = false
        label.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        label.translatesAutoresizingMaskIntoConstraints = false
        label.setAccessibilityElement(false)
        addSubview(label)
        NSLayoutConstraint.activate([
            label.leadingAnchor.constraint(equalTo: leadingAnchor, constant: BoardTodayMetrics.padding),
            label.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -BoardTodayMetrics.padding),
            label.centerYAnchor.constraint(equalTo: centerYAnchor),
        ])
        setAccessibilityElement(true)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    func configure(_ text: String, accent: Bool) {
        self.accent = accent
        label.stringValue = text
        setAccessibilityRole(accent ? .button : .staticText)
        setAccessibilityLabel(text)
        applyColour()
    }

    override var backgroundStyle: NSView.BackgroundStyle {
        didSet { applyColour() }
    }

    private func applyColour() {
        if backgroundStyle == .emphasized {
            label.textColor = .alternateSelectedControlTextColor
        } else {
            label.textColor = accent ? Tint.accent : Tint.secondary
        }
    }

    override func prepareForReuse() {
        super.prepareForReuse()
        backgroundStyle = .normal
    }
}

// MARK: - Side column

/// Lays the main column and the side column out by hand: side by side
/// from 760 pt, else the side column under the main one (at most 40 % of
/// the height, scrolling when it needs more). Nothing here asks for a
/// width, so the window's minimum stays the window's.
@MainActor
private final class BoardTodayContainer: NSView {
    private let main: NSView
    private let side: BoardTodaySideView

    init(main: NSView, side: BoardTodaySideView) {
        self.main = main
        self.side = side
        super.init(frame: .zero)
        addSubview(main)
        addSubview(side)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override var isFlipped: Bool { true }

    override func layout() {
        super.layout()
        let w = bounds.width
        let h = bounds.height
        if w >= BoardTodayMetrics.narrow {
            let sideWidth = min(BoardTodayMetrics.sidePreferred, max(BoardTodayMetrics.sideMin, (w * 0.4).rounded()))
            main.frame = NSRect(x: 0, y: 0, width: w - sideWidth, height: h)
            side.frame = NSRect(x: w - sideWidth, y: 0, width: sideWidth, height: h)
        } else {
            let sideHeight = min(side.contentHeight(width: w), (h * 0.4).rounded())
            main.frame = NSRect(x: 0, y: 0, width: w, height: h - sideHeight)
            side.frame = NSRect(x: 0, y: h - sideHeight, width: w, height: sideHeight)
        }
    }
}

@MainActor
private final class BoardFlippedView: NSView {
    override var isFlipped: Bool { true }
}

/// The side column: the two cards one under the other, each exactly the
/// column's width less the outer padding. Everything is placed by frames
/// computed from the width (no constraints), and the content height is
/// measured when the content is set or the width changes, never in a
/// layout pass of its own.
@MainActor
private final class BoardTodaySideView: NSScrollView {
    var onSelect: ((Board.CaseID) -> Void)?

    private let doc = BoardFlippedView()
    private let outer: CGFloat = BoardTodayMetrics.padding
    private let top: CGFloat = 8
    private let gap: CGFloat = 12
    private var cards: [BoardTodayCard] = []
    private var dueItems: [BoardDueItemView] = []
    private var cached: (width: CGFloat, height: CGFloat)?

    /// The deadline buttons, in order.
    var keyViews: [NSView] { dueItems }

    init() {
        super.init(frame: .zero)
        drawsBackground = false
        hasVerticalScroller = true
        borderType = .noBorder
        automaticallyAdjustsContentInsets = false
        documentView = doc
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    func configure(_ today: Board.Today) {
        for card in cards {
            card.removeFromSuperview()
        }
        dueItems = []
        cached = nil

        let deadlines = BoardTodayCard(dashed: false)
        deadlines.add(heading(Board.Text.deadlines))
        if today.dueGroups.isEmpty {
            deadlines.add(wrappingLabel(today.dueEmpty, font: Typo.body))
        } else {
            for group in today.dueGroups {
                let caption = NSTextField(labelWithString: group.title)
                caption.font = .systemFont(ofSize: Typo.captionSize, weight: .semibold)
                caption.textColor = Tint.secondary
                caption.lineBreakMode = .byTruncatingTail
                caption.isSelectable = false
                deadlines.add(caption, spacingBefore: 4)
                for item in group.items {
                    let button = BoardDueItemView(item)
                    button.onClick = { [weak self] id in self?.onSelect?(id) }
                    dueItems.append(button)
                    deadlines.add(button, spacingBefore: 2)
                }
            }
            // One chip column for the card: every title starts at the
            // same x, whatever the width of its own chip.
            let column = dueItems.map(\.chipWidth).max() ?? 0
            for item in dueItems {
                item.chipColumn = column
            }
        }

        let calendar = BoardTodayCard(dashed: true)
        calendar.add(heading(today.calendarTitle))
        calendar.add(wrappingLabel(today.calendarBody, font: Typo.body))

        cards = [deadlines, calendar]
        for card in cards {
            doc.addSubview(card)
        }
        needsLayout = true
    }

    private func heading(_ text: String) -> NSTextField {
        let label = NSTextField(labelWithString: text)
        label.font = BoardFonts.sectionTitle
        label.lineBreakMode = .byTruncatingTail
        label.isSelectable = false
        return label
    }

    private func wrappingLabel(_ text: String, font: NSFont) -> NSTextField {
        let label = NSTextField(wrappingLabelWithString: text)
        label.font = font
        label.textColor = Tint.secondary
        label.isSelectable = false
        return label
    }

    /// The height of the content at `width`, measured once per width and
    /// content.
    func contentHeight(width: CGFloat) -> CGFloat {
        if let cached, cached.width == width {
            return cached.height
        }
        let cardWidth = max(0, width - 2 * outer)
        var h = top
        for card in cards {
            h += card.height(width: cardWidth) + gap
        }
        h = h - gap + top
        cached = (width, h)
        return h
    }

    override func layout() {
        super.layout()
        let w = contentSize.width
        let cardWidth = max(0, w - 2 * outer)
        var y = top
        for card in cards {
            let h = card.height(width: cardWidth)
            card.frame = NSRect(x: outer, y: y, width: cardWidth, height: h)
            y += h + gap
        }
        doc.frame = NSRect(x: 0, y: 0, width: w, height: max(contentHeight(width: w), contentSize.height))
    }
}

/// A bordered rounded card holding rows one under the other, each as wide
/// as the card less the padding and leading-aligned; frames, no stack.
@MainActor
private final class BoardTodayCard: NSView {
    static let padding: CGFloat = 14
    private static let spacing: CGFloat = 6

    private let dashed: Bool
    private var rows: [(view: NSView, before: CGFloat)] = []

    init(dashed: Bool) {
        self.dashed = dashed
        super.init(frame: .zero)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override var isFlipped: Bool { true }

    func add(_ view: NSView, spacingBefore: CGFloat = 0) {
        rows.append((view, rows.isEmpty ? 0 : spacingBefore))
        addSubview(view)
    }

    private static func height(of view: NSView, width: CGFloat) -> CGFloat {
        if let item = view as? BoardDueItemView {
            return item.height(width: width)
        }
        if let field = view as? NSTextField, let cell = field.cell {
            let bounds = NSRect(x: 0, y: 0, width: width, height: .greatestFiniteMagnitude)
            return ceil(cell.cellSize(forBounds: bounds).height)
        }
        return view.fittingSize.height
    }

    func height(width: CGFloat) -> CGFloat {
        let inner = max(0, width - 2 * Self.padding)
        var h = 2 * Self.padding
        for (i, row) in rows.enumerated() {
            h += Self.height(of: row.view, width: inner) + (i > 0 ? Self.spacing + row.before : 0)
        }
        return h
    }

    override func layout() {
        super.layout()
        let p = Self.padding
        let inner = max(0, bounds.width - 2 * p)
        var y = p
        for (i, row) in rows.enumerated() {
            if i > 0 { y += Self.spacing + row.before }
            let h = Self.height(of: row.view, width: inner)
            row.view.frame = NSRect(x: p, y: y, width: inner, height: h)
            y += h
        }
    }

    override func draw(_ dirtyRect: NSRect) {
        let path = NSBezierPath(
            roundedRect: bounds.insetBy(dx: 0.5, dy: 0.5), xRadius: BoardMetrics.cardRadius, yRadius: BoardMetrics.cardRadius)
        if !dashed {
            BoardPalette.cardFill.setFill()
            path.fill()
        } else {
            path.setLineDash([4, 3], count: 2, phase: 0)
        }
        BoardPalette.cardBorder(in: self).setStroke()
        path.lineWidth = BoardMetrics.cardBorderWidth
        path.stroke()
    }

    override func viewDidChangeEffectiveAppearance() {
        super.viewDidChangeEffectiveAppearance()
        needsDisplay = true
    }
}

/// A deadline of the side card, a borderless button (Space and Return
/// activate it, it takes part in the key-view loop and shows a focus
/// ring): the due chip at the leading edge of a column as wide as the
/// card's widest chip (`chipColumn`), and to its right the case's
/// title (after the assistant's mark when the title is the assistant's),
/// the quote that gave the date and the person, stacked and truncating at
/// the trailing padding. Placed by frames.
@MainActor
private final class BoardDueItemView: NSButton {
    var onClick: ((Board.CaseID) -> Void)?

    private static let inset: CGFloat = 6
    private static let vertical: CGFloat = 4

    private let id: Board.CaseID
    private let chip: PillLabel
    private let lines: [NSTextField]
    /// The assistant's mark before the title (the first line).
    private let mark: NSTextField?
    private var hovered = false {
        didSet {
            if hovered != oldValue {
                needsDisplay = true
            }
        }
    }

    init(_ item: Board.DueItem) {
        id = item.caseID
        chip = BoardTag.due(item.label)
        let titleFont = NSFont.systemFont(ofSize: Typo.bodySize, weight: .semibold)
        var made = [Self.label(item.title, font: titleFont, colour: .labelColor)]
        mark = item.titleIsAssistant ? BoardAssistantMark.label(font: titleFont) : nil
        if !item.quote.isEmpty {
            // Typographic quotes around the quote, set in italics.
            made.append(Self.label(Board.Text.quoted(item.quote), font: BoardFonts.quote, colour: Tint.secondary))
        }
        if !item.person.isEmpty {
            made.append(Self.label(item.person, font: Typo.caption, colour: Tint.secondary))
        }
        lines = made
        super.init(frame: .zero)
        title = ""
        isBordered = false
        setButtonType(.momentaryChange)
        focusRingType = .default
        target = self
        action = #selector(pressed)
        chip.translatesAutoresizingMaskIntoConstraints = true
        addSubview(chip)
        for line in lines {
            addSubview(line)
        }
        if let mark {
            mark.translatesAutoresizingMaskIntoConstraints = true
            addSubview(mark)
        }
        setAccessibilityLabel(
            [item.spokenTitle, Board.Text.spokenDue(item.label), item.person].filter { !$0.isEmpty }
                .joined(separator: ". "))
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    private static func label(_ text: String, font: NSFont, colour: NSColor) -> NSTextField {
        let l = NSTextField(labelWithString: text)
        l.font = font
        l.textColor = colour
        l.lineBreakMode = .byTruncatingTail
        l.maximumNumberOfLines = 1
        l.isSelectable = false
        l.setAccessibilityElement(false)
        return l
    }

    override var isFlipped: Bool { true }

    /// The width of this item's own chip.
    var chipWidth: CGFloat { chip.intrinsicContentSize.width }

    /// The width of the chip column shared by the items of the card (the
    /// widest chip among them); the chip sits at its leading edge.
    var chipColumn: CGFloat = 0 {
        didSet {
            if chipColumn != oldValue {
                needsLayout = true
            }
        }
    }

    private var textX: CGFloat { Self.inset + max(chipColumn, chipWidth) + BoardMetrics.itemGap }

    /// How far the first line starts after `textX`: the mark and its gap.
    private var markWidth: CGFloat {
        mark.map { ceil($0.intrinsicContentSize.width) + BoardAssistantMark.gap } ?? 0
    }

    private func lineHeights(width: CGFloat) -> [CGFloat] {
        lines.enumerated().map { i, line in
            let w = i == 0 ? max(0, width - markWidth) : width
            return ceil(line.cell?.cellSize(forBounds: NSRect(x: 0, y: 0, width: w, height: 100_000)).height ?? 0)
        }
    }

    /// The height at `width`.
    func height(width: CGFloat) -> CGFloat {
        let textWidth = max(0, width - textX - Self.inset)
        let heights = lineHeights(width: textWidth)
        let text = heights.reduce(0, +) + BoardMetrics.lineGap * CGFloat(max(0, heights.count - 1))
        return max(text, chip.intrinsicContentSize.height) + 2 * Self.vertical
    }

    override func layout() {
        super.layout()
        let textWidth = max(0, bounds.width - textX - Self.inset)
        let heights = lineHeights(width: textWidth)
        let chipSize = chip.intrinsicContentSize
        var y = Self.vertical
        for (i, (line, h)) in zip(lines, heights).enumerated() {
            let shift = i == 0 ? markWidth : 0
            line.frame = NSRect(x: textX + shift, y: y, width: max(0, textWidth - shift), height: h)
            if i == 0, let mark {
                let size = mark.intrinsicContentSize
                mark.frame = NSRect(x: textX, y: y, width: ceil(size.width), height: h)
            }
            y += h + BoardMetrics.lineGap
        }
        let firstH = heights.first ?? chipSize.height
        chip.frame = NSRect(
            x: Self.inset, y: Self.vertical + max(0, (firstH - chipSize.height) / 2), width: chipSize.width,
            height: chipSize.height)
    }

    @objc private func pressed() {
        onClick?(id)
    }

    override func hitTest(_ point: NSPoint) -> NSView? {
        bounds.contains(convert(point, from: superview)) ? self : nil
    }

    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }

    override func updateTrackingAreas() {
        super.updateTrackingAreas()
        for area in trackingAreas {
            removeTrackingArea(area)
        }
        addTrackingArea(
            NSTrackingArea(
                rect: bounds, options: [.mouseEnteredAndExited, .mouseMoved, .activeInKeyWindow, .inVisibleRect], owner: self))
    }

    // Not under the page's panel (`receivesPointer`).
    override func mouseEntered(with event: NSEvent) { hovered = receivesPointer(event) }
    override func mouseMoved(with event: NSEvent) { hovered = receivesPointer(event) }
    override func mouseExited(with event: NSEvent) { hovered = false }

    override var focusRingMaskBounds: NSRect { bounds }

    override func drawFocusRingMask() {
        NSBezierPath(roundedRect: bounds, xRadius: 6, yRadius: 6).fill()
    }

    override func draw(_ dirtyRect: NSRect) {
        guard hovered else { return }
        Tint.fg(alpha: 0.06).setFill()
        NSBezierPath(roundedRect: bounds, xRadius: 6, yRadius: 6).fill()
    }
}
