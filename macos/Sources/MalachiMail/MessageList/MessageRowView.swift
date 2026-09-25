// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit

/// The row behind a message cell (style.go `list.message-list > row`): a
/// hairline under every row but the last, the faint tint of a member of an
/// unfolded conversation while it is neither selected nor hovered, and the
/// system selection highlight (the plan's D19).
@MainActor
final class MessageRowView: NSTableRowView {
    static let reuseIdentifier = NSUserInterfaceItemIdentifier("MessageRow")

    /// The row is a member of an unfolded conversation (`row.thread-member`).
    var isMember = false {
        didSet {
            if isMember != oldValue {
                needsDisplay = true
            }
        }
    }

    private var tracking: NSTrackingArea?
    private var hovered = false {
        didSet {
            if hovered != oldValue {
                needsDisplay = true
            }
        }
    }

    init() {
        super.init(frame: .zero)
        identifier = MessageRowView.reuseIdentifier
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func drawBackground(in dirtyRect: NSRect) {
        super.drawBackground(in: dirtyRect)
        // The tint is for the resting state only (style.go).
        if isMember, !isSelected, !hovered {
            Tint.fg(alpha: 0.03).setFill()
            bounds.fill()
        }
    }

    /// The table draws no grid of its own (`drawGrid` is a no-op there);
    /// the hairline is this row's, in the separator colour, and the last
    /// row keeps no trailing line (`row:last-child { border-bottom: none }`).
    override func draw(_ dirtyRect: NSRect) {
        super.draw(dirtyRect)
        guard !isLastRow else { return }
        let line = NSRect(x: bounds.minX, y: bounds.maxY - 1, width: bounds.width, height: 1)
        guard line.intersects(dirtyRect) else { return }
        NSColor.separatorColor.setFill()
        line.fill()
    }

    override func drawSeparator(in dirtyRect: NSRect) {
        // Drawn in `draw` above, after the selection.
    }

    private var isLastRow: Bool {
        var v: NSView? = superview
        while let view = v, !(view is NSTableView) {
            v = view.superview
        }
        guard let table = v as? NSTableView else { return false }
        let row = table.row(for: self)
        return row >= 0 && row == table.numberOfRows - 1
    }

    // MARK: Hover

    override func updateTrackingAreas() {
        super.updateTrackingAreas()
        if let tracking {
            removeTrackingArea(tracking)
        }
        let area = NSTrackingArea(
            rect: bounds, options: [.mouseEnteredAndExited, .activeInKeyWindow, .inVisibleRect], owner: self, userInfo: nil
        )
        addTrackingArea(area)
        tracking = area
    }

    override func mouseEntered(with event: NSEvent) {
        super.mouseEntered(with: event)
        hovered = true
    }

    override func mouseExited(with event: NSEvent) {
        super.mouseExited(with: event)
        hovered = false
    }

    override func prepareForReuse() {
        super.prepareForReuse()
        hovered = false
        isMember = false
    }
}
