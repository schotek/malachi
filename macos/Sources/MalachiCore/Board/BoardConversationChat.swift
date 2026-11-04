// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The chat layout of a case's conversation, the board's counterpart of the
// chat of Mail (Model/ConversationChat.swift), which the toggle in the
// block's heading row (`Text.chatToggleLabel`, the Mail key
// conversation-chat-layout) switches to. A bubble's text is the member's
// `MessageCard.text`, the text `board.get` carries (the quoted history cut
// as trimQuoted cuts it, the signature cut, at most 8000 bytes): the chat
// fetches no body and shows no web view unless the preference
// conversation-chat-html (Render HTML in Chat, Mail's key) is on: then the
// bubbles near the visible part of the detail ask message.body through
// Mail's cache as the open cards do and show the message's HTML as the
// Mail chat does (the same rule, `Conversation.bubbleRendersHTML`, at most
// the board's `ConversationCards.maxLiveWebViews` web views,
// `ChatBodies`), the board.get text the fallback; the samples have no ids
// and read the same either way.
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

    /// Which bubbles of the board's chat asked message.body for the
    /// preference Render HTML in Chat (macOS-first; the cards keep their
    /// own `ConversationCards` bookkeeping): only a member with an id, only
    /// with the preference on, only near the visible part, each once until
    /// the preference goes off or the member goes. Keyed by the member (id
    /// and excerpt), as the cards' bodies are: a member the daemon rebuilt
    /// asks again.
    public struct ChatBodies: Sendable, Equatable {
        public private(set) var members: [ConversationCards.Member] = []
        private var askedFor: Set<ConversationCards.Member> = []

        public init() {}

        /// The members on show now (`ConversationCards.key(d).members`):
        /// those gone are forgotten.
        public mutating func apply(_ members: [ConversationCards.Member]) {
            self.members = members
            askedFor.formIntersection(members)
        }

        /// Bubble `i` asks for its body now: the preference on, near the
        /// visible part, an id, not asked yet.
        public func needsBody(_ i: Int, near: Bool, setting: Bool) -> Bool {
            guard setting, near, members.indices.contains(i), members[i].id != nil else { return false }
            return !askedFor.contains(members[i])
        }

        /// Bubble `i` asked.
        public mutating func asked(_ i: Int) {
            guard members.indices.contains(i) else { return }
            askedFor.insert(members[i])
        }

        /// Whether bubble `i` asked (its answers, also later ones through
        /// Mail's fan-out, are its own to show).
        public func wasAsked(_ i: Int) -> Bool {
            members.indices.contains(i) && askedFor.contains(members[i])
        }

        /// The preference went off: nothing is asked any more, and on
        /// again every near bubble asks anew.
        public mutating func forget() {
            askedFor = []
        }

        /// The bubbles at `frames` near `visible` and, of those whose body
        /// renders HTML (`html`), the ones that get a web view: Mail's
        /// rule (`ConversationLayout.live`) with the board's cap.
        public static func live(
            frames: [ConversationLayout.Span], html: [Bool], visible: ConversationLayout.Span
        ) -> ConversationLayout.Live {
            ConversationLayout.live(
                frames: frames, html: html, visible: visible, cap: ConversationCards.maxLiveWebViews)
        }
    }
}
