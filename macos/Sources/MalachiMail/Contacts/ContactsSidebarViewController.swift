// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The Contacts window's sidebar (W1 ②–④, W5 a, b): All Contacts, then per
/// account a heading with its kind capsule (M365, CARDDAV) and its visible
/// books with their counts, then On This Computer with Recently Used —
/// `ContactsListController.items`, the rows of `ContactBook.sidebar`. A
/// source-list table: the keyboard moves through the selectable rows, the
/// headings are group rows. Every name is a server's text, shown through
/// `stringValue`.
@MainActor
final class ContactsSidebarViewController: NSViewController, NSTableViewDataSource, NSTableViewDelegate {
    weak var window: ContactsWindowController?

    private let table = NSTableView()
    private var items: [ContactBook.Item] = []
    private var applying = false

    static let rowHeight: CGFloat = 28
    static let headingHeight: CGFloat = 26

    override func loadView() {
        let scroll = NSScrollView()
        scroll.drawsBackground = false
        scroll.hasVerticalScroller = true
        scroll.autohidesScrollers = true
        let column = NSTableColumn(identifier: NSUserInterfaceItemIdentifier("book"))
        column.resizingMask = .autoresizingMask
        table.addTableColumn(column)
        table.headerView = nil
        table.style = .sourceList
        table.backgroundColor = .clear
        table.dataSource = self
        table.delegate = self
        table.floatsGroupRows = false
        table.allowsEmptySelection = true
        table.setAccessibilityLabel(ContactBook.windowTitle)
        scroll.documentView = table
        view = scroll
    }

    /// Reads the controller's rows and selection; rebuilds only on change.
    func update() {
        guard let c = window?.list else { return }
        _ = view
        if c.items != items {
            items = c.items
            table.reloadData()
        }
        applying = true
        defer { applying = false }
        if let i = items.firstIndex(where: { $0.selectable && $0.book == c.selectedBook }) {
            if table.selectedRow != i {
                table.selectRowIndexes([i], byExtendingSelection: false)
            }
        } else {
            table.deselectAll(nil)
        }
    }

    // MARK: Table

    func numberOfRows(in tableView: NSTableView) -> Int { items.count }

    func tableView(_ tableView: NSTableView, isGroupRow row: Int) -> Bool {
        !items[row].selectable
    }

    func tableView(_ tableView: NSTableView, shouldSelectRow row: Int) -> Bool {
        items[row].selectable
    }

    func tableView(_ tableView: NSTableView, heightOfRow row: Int) -> CGFloat {
        items[row].selectable ? Self.rowHeight : Self.headingHeight
    }

    func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int) -> NSView? {
        let item = items[row]
        if !item.selectable {
            return ContactsSidebarHeading(text: item.title, badge: item.badge)
        }
        return ContactsSidebarRow(item: item)
    }

    func tableViewSelectionDidChange(_ notification: Foundation.Notification) {
        guard !applying, table.selectedRow >= 0, table.selectedRow < items.count,
              let book = items[table.selectedRow].book
        else { return }
        window?.chooseBook(book)
    }
}

/// An account's heading (or On This Computer): its name and kind capsule.
@MainActor
final class ContactsSidebarHeading: NSTableCellView {
    init(text: String, badge: String) {
        super.init(frame: .zero)
        let label = NSTextField(labelWithString: text)
        label.font = Typo.sidebarHeading
        label.textColor = .secondaryLabelColor
        label.lineBreakMode = .byTruncatingTail
        label.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        let kind = PillLabel()
        kind.font = Typo.sidebarKindBadge
        kind.textColor = .secondaryLabelColor
        kind.stringValue = badge
        kind.isHidden = badge.isEmpty
        let row = NSStackView(views: [label, kind])
        row.orientation = .horizontal
        row.alignment = .centerY
        row.spacing = 6
        row.detachesHiddenViews = true
        row.translatesAutoresizingMaskIntoConstraints = false
        addSubview(row)
        NSLayoutConstraint.activate([
            row.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 4),
            row.trailingAnchor.constraint(lessThanOrEqualTo: trailingAnchor, constant: -4),
            row.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -3),
        ])
        setAccessibilityElement(true)
        setAccessibilityRole(.staticText)
        setAccessibilityLabel(badge.isEmpty ? text : ContactBook.twoParts(text, badge))
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }
}

/// A selectable row: its symbol (All Contacts, a book, Recently Used), its
/// name and its count.
@MainActor
final class ContactsSidebarRow: NSTableCellView {
    init(item: ContactBook.Item) {
        super.init(frame: .zero)
        let symbol: String
        switch item.kind {
        case .all: symbol = "person.2"
        case .recent: symbol = "clock"
        default: symbol = "book.closed"
        }
        let icon = NSImageView(image: NSImage(systemSymbolName: symbol, accessibilityDescription: nil) ?? NSImage())
        icon.symbolConfiguration = .init(pointSize: Typo.sidebarTitleSize + 1, weight: .regular)
        icon.contentTintColor = .controlAccentColor
        icon.setContentHuggingPriority(.required, for: .horizontal)
        let title = NSTextField(labelWithString: item.title)
        title.font = .systemFont(ofSize: Typo.sidebarTitleSize + 1)
        title.lineBreakMode = .byTruncatingTail
        title.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        textField = title
        let count = NSTextField(labelWithString: item.count > 0 ? item.count.formatted() : "")
        count.font = .monospacedDigitSystemFont(ofSize: Typo.sidebarTitleSize, weight: .regular)
        count.textColor = .secondaryLabelColor
        count.setContentHuggingPriority(.required, for: .horizontal)
        count.setContentCompressionResistancePriority(.required, for: .horizontal)
        let spacer = NSView()
        spacer.setContentHuggingPriority(.init(1), for: .horizontal)
        let row = NSStackView(views: [icon, title, spacer, count])
        row.orientation = .horizontal
        row.alignment = .centerY
        row.spacing = 6
        row.translatesAutoresizingMaskIntoConstraints = false
        addSubview(row)
        NSLayoutConstraint.activate([
            row.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 2),
            row.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -6),
            row.centerYAnchor.constraint(equalTo: centerYAnchor),
        ])
        setAccessibilityLabel(item.count > 0 ? ContactBook.twoParts(item.title, String(item.count)) : item.title)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }
}
