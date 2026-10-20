// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The chat layout of a case's conversation, the board's counterpart of the
// chat of Mail (Model/ConversationChat.swift), which the toggle in the
// block's heading row (`Text.chatToggleLabel`, the Mail key
// conversation-chat-layout) switches to. A bubble's text is the member's
// `MessageCard.text`, the text `board.get` carries (the quoted history cut
// as trimQuoted cuts it, the signature cut, at most 8000 bytes): the chat
// fetches no body and shows no web view, so the samples read the same.
// The detail keeps its place at the top; the chat reads down from the
// oldest bubble to the newest, never scrolled to its end. Port of
// ui/internal/board/conversation_chat.go.

import Foundation

extension Board {
    /// One row of the chat layout of a case's conversation: the member
    /// `Detail.messages[index]`, on the right when it is the user's own.
    public struct ChatBubble: Sendable, Equatable {
        public var index: Int
        /// `MessageCard.mine`.
        public var right: Bool

        public init(index: Int, right: Bool) {
            self.index = index
            self.right = right
        }
    }

    /// board.ChatRows: the rows of the chat layout of a case's
    /// conversation, one bubble per member in the order given (oldest
    /// first, as `Detail.messages` and the cards are): the user's own
    /// messages on the right, every other on the left. The board's members
    /// hold no Jira events, no drafts and no older-messages row, so the
    /// chat has no centred rows; the suggested reply is the inline editor
    /// above the block, never a bubble. A sample member (id nil) is a
    /// bubble too, but opens nothing (`chatOpens`). The block's loading,
    /// failure and empty notes (`Detail.messagesNote`) stand in place of
    /// the chat as they do of the cards.
    public static func chatRows(_ messages: [MessageCard]) -> [ChatBubble] {
        messages.enumerated().map { ChatBubble(index: $0.offset, right: $0.element.mine) }
    }

    /// board.ChatOpens: whether a double-click on the bubble of `m` can
    /// open the message in its own window: a member with an id (never a
    /// sample).
    public static func chatOpens(_ m: MessageCard) -> Bool { m.id != nil }
}
