// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// ui/internal/composebody: presentation of a newly prepared reply/forward:
/// its initial body is the backend's complete quoted original, not text the
/// user has written. Existing drafts remain intact; their body can already
/// contain user edits.
public struct ComposeBodyLayout: Sendable, Equatable {
    /// composebody.EmptyBody: the editor's body above an original shown
    /// apart.
    public static let emptyBody = "<p><br></p>"

    public let editorHTML: String
    public let quotedHTML: String

    /// composebody.New: lays `bodyHTML` out for a window of `mode`: a
    /// reply, a reply to all or a forward that is not a comment, with a
    /// body, has its body as the original and an empty editor; everything
    /// else has its body in the editor. A caller that must keep a body
    /// whole (a draft opened from Drafts, the board's inline reply) passes
    /// `.new`.
    public init(mode: ComposeMode, comment: Bool, bodyHTML: String) {
        let separate = !comment && [ComposeMode.reply, .replyAll, .forward].contains(mode) && !bodyHTML.isEmpty
        editorHTML = separate ? Self.emptyBody : bodyHTML
        quotedHTML = separate ? bodyHTML : ""
    }

    /// The window's layout of `params` (compose/quoted.go `bodyLayout` with
    /// `LayoutWindow`): `init(mode:comment:bodyHTML:)` for its kind, started
    /// with the user's own text `params.ownHTML` (`withOwn`). The inline
    /// layout builds params of its own (`.edit`, no own text).
    public init(_ params: ComposeParams) {
        self = ComposeBodyLayout(mode: params.kind.mode, comment: params.comment != nil, bodyHTML: params.bodyHTML)
            .withOwn(params.ownHTML)
    }

    private init(editorHTML: String, quotedHTML: String) {
        self.editorHTML = editorHTML
        self.quotedHTML = quotedHTML
    }

    /// composebody.Layout.WithOwn: the layout with `own`, the user's own
    /// text written before the window opened (the chat's quick-reply bar;
    /// already safe HTML, `Conversation.quickReplyHTML`), in the editor in
    /// place of what it would start with; the original apart stays as it
    /// is. "" keeps the layout unchanged.
    public func withOwn(_ own: String) -> ComposeBodyLayout {
        own.isEmpty ? self : ComposeBodyLayout(editorHTML: own, quotedHTML: quotedHTML)
    }

    public func html(own: String, includeQuote: Bool) -> String {
        includeQuote && !quotedHTML.isEmpty ? own + "\n" + quotedHTML : own
    }

    public func text(own: String, quote: String, includeQuote: Bool) -> String {
        includeQuote && !quotedHTML.isEmpty ? own + "\n\n" + quote : own
    }

    /// composebody.DraftBody: the body of a draft (draft.create, draft.open)
    /// as HTML for a window or a quick reply: its `htmlBody`, safe since the
    /// backend's sanitiser produced it; a draft without one (the backend
    /// could not quote formatted, or it is plain text) has its `textBody`
    /// escaped (`escapeText`, composebody.EscapeText).
    public static func draftBody(_ draft: Draft) -> String {
        if let html = draft.htmlBody, !html.isEmpty {
            return html
        }
        return escapeText(draft.textBody)
    }
}
