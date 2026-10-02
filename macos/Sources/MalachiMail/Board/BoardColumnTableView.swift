// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit

/// The table of one column of the board's Columns style: clear, no grid
/// (the rows draw their cards), and the keys that move the one selection
/// shared by the four columns. Up and Down step through the column's
/// selectable rows (past the "From the Assistant" heading), Left and Right
/// go to the neighbouring column, Return keeps the selection (the page's
/// panel is open on it already). Each key handler reports whether it did
/// something; when it did not the key goes on to the table. Escape is
/// left to the responder chain (the page's `cancelOperation`).
@MainActor
final class BoardColumnTableView: NSTableView {
    /// Up (`-1`) or Down (`+1`).
    var onStep: (@MainActor (_ delta: Int) -> Bool)?
    /// Left (`-1`) or Right (`+1`).
    var onSideways: (@MainActor (_ delta: Int) -> Bool)?
    /// Return or Enter on the selected row.
    var onActivate: (@MainActor () -> Bool)?
    /// The context menu of a row, or nil.
    var menuForRow: (@MainActor (_ row: Int) -> NSMenu?)?

    override func drawGrid(inClipRect clipRect: NSRect) {
        // The cards are the grid.
    }

    override func keyDown(with event: NSEvent) {
        let mods = event.modifierFlags.intersection(.deviceIndependentFlagsMask).subtracting([.capsLock, .numericPad, .function])
        if mods.isEmpty {
            switch event.specialKey {
            case .upArrow?:
                if onStep?(-1) == true {
                    return
                }
            case .downArrow?:
                if onStep?(1) == true {
                    return
                }
            case .leftArrow?:
                if onSideways?(-1) == true {
                    return
                }
            case .rightArrow?:
                if onSideways?(1) == true {
                    return
                }
            case .carriageReturn?, .enter?:
                if onActivate?() == true {
                    return
                }
            default:
                break
            }
        }
        super.keyDown(with: event)
    }

    override func menu(for event: NSEvent) -> NSMenu? {
        let row = row(at: convert(event.locationInWindow, from: nil))
        // The menu goes through AppKit's own `menu(for:)`, which draws the
        // outline round the clicked row; it does not change the selection.
        menu = row >= 0 ? menuForRow?(row) : nil
        return menu == nil ? nil : super.menu(for: event)
    }
}
