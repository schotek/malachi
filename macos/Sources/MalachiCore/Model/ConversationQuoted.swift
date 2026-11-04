// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/conversation/quoted.go: the body of a message shows its new
// text: the daemon cuts the quoted history under it (message.body with
// `trimQuoted`; `quotedTrimmed` says it cut something), and a small button
// under the body ("•••") shows it, then hides it again. What the user
// revealed holds until another conversation or message is shown, like the
// folds (`QuotedReveal`). Wherever a body shows: the cards of the
// conversation, the message alone in the pane, the message window.
//
// Swift-first: `QuotedReveal` and the two variants of a cached body
// (`LoadedMessage.showQuoted`) were written here first; Go has them as
// `conversation.QuotedReveal` / `OfferQuoted` and ui/internal/window/quoted.go.

import Foundation

extension Conversation {
    /// conversation.QuotedTextLabel: the tooltip and accessible name of the
    /// button under a trimmed body; `shown` is whether the quoted history
    /// shows now.
    public static func quotedTextLabel(shown: Bool) -> String {
        if shown {
            // TRANSLATORS: the tooltip of the three-dot button under a message whose quoted earlier messages show; hides them.
            return L10n.T("Hide Quoted Text")
        }
        // TRANSLATORS: the tooltip of the three-dot button under a message whose quoted earlier messages are hidden; shows them.
        return L10n.T("Show Quoted Text")
    }
}

/// The messages whose quoted history the user revealed in one view, for
/// what that view shows now (a conversation, or a message alone): another
/// selection forgets them, an update of the same one keeps them. A body
/// asked for again (the cache let it go, the daemon rebuilt the message)
/// comes back as the user left it. A new value is ready.
public struct QuotedReveal: Sendable, Equatable {
    private var shown: String?
    private var revealed: Set<MessageID> = []

    public init() {}

    /// Called whenever the view shows `selection` (a thread or message id),
    /// anew or updated: the messages revealed for another selection are
    /// forgotten, those for this one kept.
    public mutating func show(_ selection: String) {
        if selection != shown {
            shown = selection
            revealed = []
        }
    }

    /// Forgets the selection and what was revealed for it.
    public mutating func clear() {
        shown = nil
        revealed = []
    }

    /// Whether the quoted history of message `id` shows.
    public func isRevealed(_ id: MessageID) -> Bool {
        revealed.contains(id)
    }

    /// Records the user's Show Quoted Text (true) or Hide Quoted Text of
    /// message `id`.
    public mutating func set(_ id: MessageID, _ on: Bool) {
        if id.rawValue.isEmpty {
            return
        }
        if on {
            revealed.insert(id)
        } else {
            revealed.remove(id)
        }
    }
}
