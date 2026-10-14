// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// The Assistant's message actions on a selection of several rows of the
// message list (ui/internal/assistant selection.go).

extension Assistant {
    /// assistant.MessageActionsFor: several rows leave out Draft a Reply…,
    /// which answers one message.
    public static func messageActionsFor(_ count: Int) -> [Action] {
        count <= 1 ? messageActions : messageActions.filter { $0 != .draftReply }
    }

    /// assistant.LabelFor: Ask names several rows as several.
    public static func labelFor(_ a: Action, count: Int) -> String {
        if a == .ask, count > 1 {
            // TRANSLATORS: An item of the Assistant menu while several messages are selected.
            return L10n.T("Ask About These Messages…")
        }
        return label(a)
    }
}
