// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// Presentation of a newly prepared reply/forward: its initial body is the
/// backend's complete quoted original, not text the user has written.
/// Existing drafts remain intact; their body can already contain user edits.
public struct ComposeBodyLayout: Sendable, Equatable {
    public let editorHTML: String
    public let quotedHTML: String

    public init(_ params: ComposeParams) {
        let separate = params.comment == nil && [.reply, .replyAll, .forward].contains(params.kind)
            && !params.bodyHTML.isEmpty
        editorHTML = separate ? "<p><br></p>" : params.bodyHTML
        quotedHTML = separate ? params.bodyHTML : ""
    }

    public func html(own: String, includeQuote: Bool) -> String {
        includeQuote && !quotedHTML.isEmpty ? own + "\n" + quotedHTML : own
    }

    public func text(own: String, quote: String, includeQuote: Bool) -> String {
        includeQuote && !quotedHTML.isEmpty ? own + "\n\n" + quote : own
    }
}
