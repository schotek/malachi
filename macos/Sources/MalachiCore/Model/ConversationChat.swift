// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/conversation/chat.go: the conversation shows in one of two
// layouts, which a toggle above it switches for every conversation (the
// settings key `conversation-chat-layout`, which outlives a restart): the
// timeline (the cards stacked with the avatars and the line in the left
// margin, the card that opened the conversation on top, the others newest
// first, open at the top; the default) and the chat. The chat reads like a
// messaging app: the others' messages as bubbles on the left, the user's on
// the right, oldest first from the top, open at the end (`chatScrollTo`). A
// bubble is plain text only: the sender, the date, the unread dot, a
// paperclip, the Jira badges and the message's text as the daemon derived
// it, with the quoted history cut (message.body's text with `trimQuoted`;
// `bubbleText`). It has no buttons, no attachment chips, no web view and
// folds never; its text cannot be selected, so that a double click anywhere
// in it opens the message in its own window (a draft in the compose
// window), where everything else is. Events and the row of older messages
// are centred rows without a bubble; the drafts come last.
//
// The model does not change: the chat is a second layout over the same
// conversation controller and its cache of bodies, so that switching shows
// the same conversation without asking thread.get again. A bubble shows the
// summary's snippet, dimmed, until its body is loaded, and the chat asks
// for the bodies of the bubbles near the visible part only (two heights of
// the view, counted from the end), as the timeline does for its cards.
// Where the bubbles stand and what is near are the client's measures
// (`ConversationLayout.chatBubbleMax`), not this file's. Go's zero values of
// a row's item and draft are nil here.

import Foundation

extension Conversation {
    /// conversation.ChatSide: where a row of the chat stands.
    public enum ChatSide: Sendable, Equatable {
        /// A message of someone else.
        case left
        /// The user's own message, sent reply or draft.
        case right
        /// A row without a bubble (an issue event, the row of older
        /// messages).
        case center
    }

    /// conversation.ChatRowKind: what a row of the chat is.
    public enum ChatRowKind: Sendable, Equatable {
        /// A message: a mail message, a sent reply, or the description or a
        /// comment of an issue.
        case bubble
        /// An unsent reply or comment (a `DraftCard`).
        case draft
        /// An issue's status or assignee changes.
        case event
        /// The row that says how many older messages are left out.
        case truncated
    }

    /// conversation.ChatRow: one row of the chat.
    public struct ChatRow: Sendable, Equatable {
        public var kind: ChatRowKind
        public var side: ChatSide
        /// The model's item of a `.bubble`, `.event` or `.truncated` row
        /// (whose `text` is the row's sentence); nil for a `.draft`.
        public var item: Item?
        /// The draft card of a `.draft` row; nil otherwise.
        public var draft: DraftCard?

        public init(kind: ChatRowKind, side: ChatSide, item: Item? = nil, draft: DraftCard? = nil) {
            self.kind = kind
            self.side = side
            self.item = item
            self.draft = draft
        }
    }

    /// conversation.ChatRows: the rows of the chat of `model`, top to
    /// bottom: the row of older messages first when there is one (centred),
    /// then the items of the model in its order (oldest first), a message as
    /// a bubble and an event centred, then the drafts (`drafts(_:account:)`'s
    /// cards) at the end, on the right, the oldest saved first (equal
    /// `updatedAt` in the order given; `drafts` itself is not reordered).
    ///
    /// A bubble stands on the right when the user wrote it (`Item.mine`) or
    /// it is the user's reply in Sent (`Item.sent`): a reply sent from
    /// another identity of the user, whose address is not the account's,
    /// has `mine` false and still stands on the right. A comment an
    /// integration relayed (`Item.via`) is never `mine`, whatever the site
    /// says, so it stands on the left. The issue card is no row: the client
    /// puts it above them, across the column.
    public static func chatRows(_ model: Model, drafts: [DraftCard]) -> [ChatRow] {
        var rows: [ChatRow] = []
        rows.reserveCapacity(model.items.count + drafts.count)
        for it in model.items where it.kind == .truncated {
            rows.append(ChatRow(kind: .truncated, side: .center, item: it))
        }
        for it in model.items {
            switch it.kind {
            case .message:
                rows.append(ChatRow(kind: .bubble, side: it.mine || it.sent ? .right : .left, item: it))
            case .event:
                rows.append(ChatRow(kind: .event, side: .center, item: it))
            case .truncated:
                break
            }
        }
        // A stable sort, which Swift's sort does not promise: the position
        // given breaks ties of `updatedAt`.
        let oldest = drafts.enumerated().sorted { a, b in
            if a.element.draft.updatedAt != b.element.draft.updatedAt {
                return a.element.draft.updatedAt < b.element.draft.updatedAt
            }
            return a.offset < b.offset
        }
        for d in oldest {
            rows.append(ChatRow(kind: .draft, side: .right, draft: d.element))
        }
        return rows
    }

    /// conversation.ChatScrollTo: the index into `rows` the chat scrolls to
    /// when it opens a conversation: the last row (the newest draft, else
    /// the newest message); -1 when there are no rows. It is not
    /// `Model.scrollTo`, which indexes `Model.items`.
    public static func chatScrollTo(_ rows: [ChatRow]) -> Int {
        rows.count - 1
    }

    /// conversation.BubbleText: the text of a message bubble from the plain
    /// text of its loaded body (message.body's text, the variant without
    /// the quoted history): cleaned as a draft card's text is
    /// (`Board.cleanBlock`, at most `draftTextBytes`: line breaks kept, at
    /// most one empty line in a row, control and format characters gone,
    /// trimmed), so that a message bubble and a draft bubble read alike; ""
    /// for an empty text. The states of a body (on its way, too large,
    /// unreadable, empty) and the errors of the call are the view's to
    /// show, with the texts of the timeline's cards; until a body is asked
    /// for, the bubble shows the summary's snippet.
    public static func bubbleText(_ text: String) -> String {
        if text.isEmpty {
            return ""
        }
        return Board.cleanBlock(text, max: draftTextBytes)
    }

    /// conversation.ChatToggleLabel: the toggle button above the
    /// conversation that shows it as a chat (on) or as the timeline (off);
    /// the button shows its state.
    public static func chatToggleLabel() -> String {
        // TRANSLATORS: a toggle button above a conversation in the reading pane; on, the messages show as chat bubbles.
        L10n.T("Show as Chat")
    }

    /// conversation.ChatToggleTooltip: the tooltip of the toggle.
    public static func chatToggleTooltip() -> String {
        // TRANSLATORS: the tooltip of the "Show as Chat" toggle above a conversation.
        L10n.T("Others on the left, your messages on the right; double-click opens the message")
    }
}
