// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit

/// The list of the Accounts page: a table of 56 pt rows that grows with
/// its rows (no scrolling), reorderable by dragging a row's handle and with
/// ⌥⌘↑ / ⌥⌘↓ (⌃↑/⌃↓ of the GTK UI are Mission Control here, deviation
/// D10). The data source is the pane; positions come from its model.
@MainActor
final class AccountsTableView: NSTableView, PrefsGroupMember {
    static let rowHeight: CGFloat = 56

    /// ⌥⌘↑ (-1) or ⌥⌘↓ (+1) on the selected row.
    var onMoveSelected: ((Int) -> Void)?
    /// Where the last mouse-down landed, in the table's coordinates: a drag
    /// starts only on a row's handle.
    private(set) var mouseDownPoint: NSPoint?

    init() {
        super.init(frame: .zero)
        let column = NSTableColumn(identifier: NSUserInterfaceItemIdentifier("account"))
        column.resizingMask = .autoresizingMask
        column.width = 560
        addTableColumn(column)
        columnAutoresizingStyle = .lastColumnOnlyAutoresizingStyle
        // Follow the clip view's width; the one column follows the table's.
        autoresizingMask = [.width]
        headerView = nil
        rowHeight = AccountsTableView.rowHeight
        intercellSpacing = .zero
        backgroundColor = .clear
        style = .plain
        selectionHighlightStyle = .regular
        allowsEmptySelection = true
        allowsMultipleSelection = false
        focusRingType = .none
        usesAutomaticRowHeights = false
        registerForDraggedTypes([.string])
        draggingDestinationFeedbackStyle = .gap
        setDraggingSourceOperationMask(.move, forLocal: true)
        setDraggingSourceOperationMask([], forLocal: false)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func mouseDown(with event: NSEvent) {
        mouseDownPoint = convert(event.locationInWindow, from: nil)
        super.mouseDown(with: event)
    }

    override func keyDown(with event: NSEvent) {
        let mods = event.modifierFlags.intersection([.command, .option, .control, .shift])
        if mods == [.option, .command], event.keyCode == 126 || event.keyCode == 125 {
            onMoveSelected?(event.keyCode == 126 ? -1 : 1)
            return
        }
        super.keyDown(with: event)
    }

    /// Whether the last mouse-down was on the handle of the row's cell.
    func mouseDownOnHandle(row: Int) -> Bool {
        guard let p = mouseDownPoint, let cell = view(atColumn: 0, row: row, makeIfNecessary: false) as? AccountRowCell else {
            return false
        }
        let inCell = cell.convert(p, from: self)
        return cell.handleFrame.contains(inCell)
    }

    /// Dims the row being dragged (the GTK `.dragging` class).
    func setRowDragging(_ row: Int, _ dragging: Bool) {
        rowView(atRow: row, makeIfNecessary: false)?.alphaValue = dragging ? 0.4 : 1
    }

    func clearDragging() {
        enumerateAvailableRowViews { rowView, _ in
            rowView.alphaValue = 1
        }
    }

    func setGroupEnabled(_ enabled: Bool) {
        isEnabled = enabled
        enumerateAvailableRowViews { rowView, _ in
            (rowView.view(atColumn: 0) as? AccountRowCell)?.setGroupEnabled(enabled)
        }
    }
}
