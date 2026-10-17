// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// The chat layout of the conversation view, the rules of the macOS view
// that need no AppKit (docs/plans/chat-view.md D2, D13, D17): which variant
// of a loaded body a message bubble reads, what its text is in every state
// of that body, and whether the chat is at its end. Swift-first: chat.go
// leaves these to the clients (`BubbleText` is the shared part), and the GTK
// and Windows chats port them from here. Kept apart from
// ConversationChat.swift, the port of chat.go, whose translated strings a
// test counts (ConversationTests `portUsesTheTemplatesEntries`): this file
// is not counted. Its one translated string, "Loading the message", is not
// new: it reuses the msgid of conversation_card.go (the cards' error of
// message.body), so a port reuses that msgid too.

extension Conversation {
    /// What a message bubble of the chat shows as its text.
    public enum BubbleBody: Sendable, Equatable {
        /// Nothing loaded yet: the summary's snippet, dimmed (D17).
        case placeholder(String)
        /// The message's text (`bubbleText`), or the sentence the timeline's
        /// cards show for the state of the body (on its way, too large,
        /// unreadable, empty) or for the error of the call.
        case text(String)
    }

    /// The variant of `lm`'s body a bubble reads: the one without the quoted
    /// history (D13), even when the user revealed it in the timeline.
    /// `quotedRevealed` is whether `lm.body` is the whole body
    /// (`LoadedMessage.quotedShown`): then the trimmed variant kept aside
    /// (`otherBody`) when there is one, else the whole body, which is all
    /// there is; otherwise `lm.body`. nil: no body yet.
    @MainActor
    public static func chatBody(of lm: LoadedMessage, quotedRevealed: Bool) -> MessageBodyResult? {
        if quotedRevealed, let trimmed = lm.otherBody {
            return trimmed
        }
        return lm.body
    }

    /// The text of the bubble of a message whose cache entry is `lm` (nil:
    /// nothing asked for yet, or let go) and whose summary's snippet is
    /// `snippet`: the snippet as a placeholder until a body or its error is
    /// there; the error of message.body as the cards say it; a body in a
    /// state other than fetched as the cards say it (`bodyText`); else the
    /// body's text cleaned (`bubbleText`), "(Empty message)" when nothing is
    /// left. The error belongs to `lm.body`, so it does not hide the
    /// trimmed variant kept aside.
    @MainActor
    public static func bubbleBody(_ lm: LoadedMessage?, snippet: String) -> BubbleBody {
        let placeholder = BubbleBody.placeholder(bubbleText(snippet))
        guard let lm else { return placeholder }
        let aside = lm.quotedShown && lm.otherBody != nil
        if !aside, let err = lm.err {
            return .text(rpcErrorText(L10n.T("Loading the message"), err))
        }
        guard let b = chatBody(of: lm, quotedRevealed: lm.quotedShown) else { return placeholder }
        switch b.bodyState {
        case .pending, .tooBig, .failed:
            return .text(bodyText(b))
        default:
            let text = bubbleText(b.text)
            return .text(text.isEmpty ? bodyText(nil) : text)
        }
    }

    /// Whether the bubble of a message whose cache entry is `lm` shows its
    /// text in the monospaced font: only with the setting for plain text
    /// (`monospace`) and only for a body that is plain text, not the text
    /// the daemon derived from HTML (`MessageBodyResult.hasHtml`), as the
    /// timeline's cards keep the setting to plain-text bodies. A
    /// placeholder, an error and a body in another state than fetched use
    /// the normal font.
    @MainActor
    public static func bubbleMonospace(_ lm: LoadedMessage?, setting monospace: Bool) -> Bool {
        guard monospace, let lm else { return false }
        let aside = lm.quotedShown && lm.otherBody != nil
        guard aside || lm.err == nil, let b = chatBody(of: lm, quotedRevealed: lm.quotedShown) else { return false }
        return b.bodyState == .fetched && !b.hasHtml
    }
}

extension ConversationLayout {
    /// How close to the end of the chat the viewport's bottom may be and
    /// still count as at the end (D2: an arrival keeps a chat at its end in
    /// view).
    public static let chatEndSlack = 4.0

    /// Whether a viewport `viewportHeight` tall whose top is at `top` shows
    /// the end of a document `documentHeight` tall (within `slack`); a
    /// document no taller than the viewport is always at its end.
    public static func chatAtEnd(
        top: Double, viewportHeight: Double, documentHeight: Double, slack: Double = chatEndSlack
    ) -> Bool {
        top + viewportHeight >= documentHeight - slack
    }

    /// The chat's column in a pane `pane` wide: the clamp's inner width
    /// (at most `Metrics.maxWidth`, less the two `Metrics.sideInset`), the
    /// `column` of `chatBubbleMax`; never negative.
    public static func chatColumn(pane: Double) -> Double {
        max(0, min(pane, Metrics.maxWidth) - 2 * Metrics.sideInset)
    }

    /// Whether the chat's bubbles in a column `column` wide
    /// (`chatColumn`) show the list's short date: their widest header,
    /// `chatBubbleMax` less the padding, is narrower than
    /// `Metrics.compactHeader` (the cards' rule, `compactDates(pane:)`,
    /// measured on the bubble, which is narrower than a card).
    public static func chatCompactDates(column: Double) -> Bool {
        chatBubbleMax(column: column) - 2 * Metrics.cardPaddingH < Metrics.compactHeader
    }

    /// What a change of the chat's viewport size asks for
    /// (`chatResize`).
    public struct ChatResize: Sendable, Equatable {
        /// The width changed: the bubbles reflow, the short dates may
        /// switch.
        public var width: Bool
        /// The viewport ends at the end of the chat again, as it did
        /// before the change (or the end was pinned): else the row the
        /// user read keeps its place.
        public var toEnd: Bool

        public init(width: Bool, toEnd: Bool) {
            self.width = width
            self.toEnd = toEnd
        }
    }

    /// The viewport went from `old` to `new` (width, height): nil when
    /// neither changed (by half a point or more). A change of the height
    /// alone moves nothing but the end: a chat that showed its end before
    /// (`wasAtEnd`, measured before the change, or `pinned`) shows it after.
    public static func chatResize(
        from old: (width: Double, height: Double), to new: (width: Double, height: Double), pinned: Bool,
        wasAtEnd: Bool
    ) -> ChatResize? {
        let width = abs(new.width - old.width) >= 0.5
        let height = abs(new.height - old.height) >= 0.5
        guard width || height else { return nil }
        return ChatResize(width: width, toEnd: pinned || wasAtEnd)
    }

    /// Where the viewport's top goes to show the end of the document (the
    /// chat opens there, `Conversation.chatScrollTo`): never above its top.
    public static func chatEndTop(viewportHeight: Double, documentHeight: Double) -> Double {
        max(0, documentHeight - viewportHeight)
    }
}
