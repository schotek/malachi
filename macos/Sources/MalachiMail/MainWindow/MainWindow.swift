// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import ObjectiveC

/// The main window's NSWindow: Edit ▸ Undo (⌘Z) takes back the newest move
/// of mail (ActionsController+Undo; window.go `win.undo`) unless text has
/// the keyboard, whose own Undo takes back typing (window.go
/// `undoAccelAllowed`: never in a text field or an editor).
///
/// NSWindow answers `undo:` itself, before the window controller in the
/// responder chain, and disables it while its undo manager has nothing, so
/// the mail's Undo has to live here. Text keeps NSWindow's own `undo:` and
/// its validation (the field editor, an editable text view, the board's
/// inline reply editor all register their typing with the window's undo
/// manager). The read-only message web view does not answer `undo:`.
@MainActor
final class MainWindow: NSWindow {
    /// Whether the mail's Undo is offered and has a move: nil hands ⌘Z to
    /// the window's own undo (the Calendar mode, no actions installed),
    /// false offers it disabled. Installed by the application.
    var mailUndoState: (@MainActor () -> Bool?)?
    /// Takes the newest move back. Installed by the application.
    var onMailUndo: (@MainActor () -> Void)?

    private static let undoSelector = Selector(("undo:"))

    @objc func undo(_ sender: Any?) {
        guard !textHasKeyboard, mailUndoState?() != nil, let onMailUndo else {
            windowUndo(sender)
            return
        }
        onMailUndo()
    }

    override func validateMenuItem(_ item: NSMenuItem) -> Bool {
        if item.action == Self.undoSelector, !textHasKeyboard, let enabled = mailUndoState?() {
            item.title = "Undo" // macOS-only string: the Edit menu's (MainMenu)
            return enabled
        }
        return super.validateMenuItem(item)
    }

    /// Whether an editor of text has the keyboard: an editable text view (a
    /// field editor too) or a compose web view (the board's inline reply).
    private var textHasKeyboard: Bool {
        if (firstResponder as? NSTextView)?.isEditable == true {
            return true
        }
        var v = firstResponder as? NSView
        while let view = v {
            if view is ComposeWebView {
                return true
            }
            v = view.superview
        }
        return false
    }

    /// NSWindow's own `undo:` (not declared in its header, so not callable
    /// as `super.undo`).
    private func windowUndo(_ sender: Any?) {
        typealias Undo = @convention(c) (AnyObject, Selector, AnyObject?) -> Void
        guard let imp = class_getMethodImplementation(NSWindow.self, Self.undoSelector) else { return }
        unsafeBitCast(imp, to: Undo.self)(self, Self.undoSelector, sender as AnyObject?)
    }
}
