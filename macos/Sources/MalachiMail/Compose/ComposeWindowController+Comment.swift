// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

// The comment mode of a compose window, the window's part: no Attach
// button, and the draft menu keeps Discard only. The content's part (the
// header, the formats, refused drops, the allowed actions) is
// ComposePane+Comment.swift.

extension ComposeWindowController {
    /// The window writes a comment (`ComposeForm.isComment`).
    var isComment: Bool { pane.isComment }

    /// Sets the window's toolbar up for a comment; nothing for an e-mail.
    /// Called once from `init`.
    func applyCommentMode() {
        guard isComment, let toolbar = window?.toolbar else { return }
        if let i = toolbar.items.firstIndex(where: { $0.action == Action.attachFiles }) {
            toolbar.removeItem(at: i)
        }
        // The draft menu keeps Discard only.
        if let menuItem = toolbar.items.lazy.compactMap({ $0 as? NSMenuToolbarItem }).first {
            let m = NSMenu()
            m.addItem(withTitle: mn(L10n.T("_Discard")), action: Action.discardDraft, keyEquivalent: "")
            menuItem.menu = m
        }
    }
}
