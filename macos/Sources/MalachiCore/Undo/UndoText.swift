// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The texts of Undo: port of ui/internal/undo/text.go, with the GTK msgids
// as keys (L10n).

import Foundation

extension Undo {
    /// The text of the toast after the move of `e`: what happened.
    public static func done(_ e: Entry) -> String {
        let n = e.count
        switch e.kind {
        case .archive, .boardArchive:
            return L10n.N("Archived %d message.", "Archived %d messages.", n)
        case .junk:
            return L10n.N("Moved %d message to Junk.", "Moved %d messages to Junk.", n)
        case .move:
            if !e.targetName.isEmpty {
                return L10n.N("Moved %d message to “%s”.", "Moved %d messages to “%s”.", n, n, e.targetName)
            }
            return L10n.N("Moved %d message.", "Moved %d messages.", n)
        case .trash, .boardTrash:
            return L10n.N("Moved %d message to Trash.", "Moved %d messages to Trash.", n)
        }
    }

    /// The button of the toast that takes the move back, with the keyboard
    /// shortcut that does the same (`shortcut`: "⌘Z"; never translated);
    /// without a shortcut just Undo.
    public static func button(_ shortcut: String) -> String {
        if shortcut.isEmpty {
            return L10n.T("Undo")
        }
        return L10n.T("Undo (%s)", shortcut)
    }

    /// The toast after Undo took `e` back.
    public static func undone(_ e: Entry) -> String {
        L10n.N("Moved %d message back.", "Moved %d messages back.", e.count)
    }

    /// The toast when Undo could not move the messages back.
    public static var failed: String {
        L10n.T("Could not move the messages back.")
    }

    /// The toast of Undo with nothing on the stack.
    public static var nothing: String {
        L10n.T("Nothing to undo.")
    }
}
