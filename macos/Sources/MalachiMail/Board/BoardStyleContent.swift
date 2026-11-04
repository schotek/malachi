// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// What the board page needs of each style's view controller (List,
/// Columns, Today): to pass it the controller's changes and a way to show
/// toasts, to put the keyboard in it, and to say where that is.
@MainActor
protocol BoardStyleContent: NSViewController {
    /// Brings the view up to date with `controller.view` for the given
    /// change set, touching only what it requires (a `.selection` change
    /// reloads no table). The page forwards every change of the board
    /// controller here while the style is shown.
    func apply(_ changes: BoardController.Changes)
    /// Shows a short message over the page (placeholder actions say
    /// `Board.Text.later`); set by the page.
    var onToast: ((String) -> Void)? { get set }
    /// Makes the content the first responder: the table holding the
    /// selected case, or the first one.
    func focusContent()
    /// The view that holds the keyboard focus after `focusContent()`, if
    /// any (the page restores it after a style switch).
    var focusTarget: NSView? { get }
    /// Whether the view runs up behind the toolbar from the page's top (the
    /// List's sidebar); otherwise the page puts it under the toolbar.
    var extendsUnderToolbar: Bool { get }
}

extension BoardStyleContent {
    /// Columns and Today start under the toolbar.
    var extendsUnderToolbar: Bool { false }
}
