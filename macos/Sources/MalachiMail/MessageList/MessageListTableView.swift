// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit

/// The table of the message list (window.blp `message_list`): no grid of
/// its own (the rows draw their hairline), Return activates the selected
/// row like a double-click (Gtk.ListBox `row-activated`), Left and Right
/// fold and unfold a conversation (threads.go `addThreadShortcuts`), Space
/// and Shift-Space page through the conversation in the reading pane. Each
/// key handler reports whether it did something; when it did not the key
/// goes on to the table for its normal navigation.
@MainActor
final class MessageListTableView: NSTableView {
    /// Return or Enter on the selected row.
    var onActivate: (@MainActor () -> Bool)?
    /// Left: fold the selected row's conversation.
    var onFold: (@MainActor () -> Bool)?
    /// Right: unfold the selected conversation row.
    var onUnfold: (@MainActor () -> Bool)?
    /// Space (Shift-Space: `up`): a page of the conversation in the reading
    /// pane, when it shows one.
    var onPage: (@MainActor (_ up: Bool) -> Bool)?

    override func drawGrid(inClipRect clipRect: NSRect) {
        // The rows draw their own separator (MessageRowView).
    }

    override func keyDown(with event: NSEvent) {
        switch event.specialKey {
        case .carriageReturn?, .enter?:
            if onActivate?() == true {
                return
            }
        case .leftArrow?:
            if onFold?() == true {
                return
            }
        case .rightArrow?:
            if onUnfold?() == true {
                return
            }
        default:
            let mods = event.modifierFlags.intersection(.deviceIndependentFlagsMask).subtracting([.shift, .capsLock])
            if event.charactersIgnoringModifiers == " ", mods.isEmpty,
               onPage?(event.modifierFlags.contains(.shift)) == true {
                return
            }
        }
        super.keyDown(with: event)
    }
}
