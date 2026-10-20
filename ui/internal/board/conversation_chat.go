// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

// The chat layout of a case's conversation, the board's counterpart of
// the chat of Mail (ui/internal/conversation/chat.go), which the toggle
// in the block's heading row (ChatToggleLabel, the Mail key
// conversation-chat-layout) switches to. A bubble's text is the member's
// MessageCard.Text, the text board.get carries (the quoted history cut as
// trimQuoted cuts it, the signature cut, at most 8000 bytes): the chat
// fetches no body and shows no web view, so the samples read the same,
// unless the preference conversation-chat-html (Mail's Render HTML in
// Chat) is on: then the bubbles near the visible part show the message's
// HTML as Mail's chat does (the rule conversation.BubbleRendersHTML, at
// most ConversationMaxLiveWebViews of them, the newest first), the text
// from board.get the fallback.
// The detail keeps its place at the top; the chat reads down from the
// oldest bubble to the newest, never scrolled to its end.

// ChatBubble is one row of the chat layout of a case's conversation: the
// member Detail.Messages[Index], on the right when it is the user's own.
type ChatBubble struct {
	Index int
	Right bool // MessageCard.Mine
}

// ChatRows are the rows of the chat layout of a case's conversation, one
// bubble per member in the order given (oldest first, as Detail.Messages
// and the cards are): the user's own messages on the right, every other
// on the left. The board's members hold no Jira events, no drafts and no
// older-messages row, so the chat has no centred rows; the suggested
// reply is the inline editor above the block, never a bubble. A sample
// member (ID "") is a bubble too, but opens nothing (ChatOpens). The
// block's loading, failure and empty notes (Detail.MessagesNote) stand in
// place of the chat as they do of the cards.
func ChatRows(msgs []MessageCard) []ChatBubble {
	if len(msgs) == 0 {
		return nil
	}
	rows := make([]ChatBubble, len(msgs))
	for i, m := range msgs {
		rows[i] = ChatBubble{Index: i, Right: m.Mine}
	}
	return rows
}

// ChatOpens says whether a double-click on the bubble of m can open the
// message in its own window: a member with an id (never a sample).
func ChatOpens(m MessageCard) bool { return m.ID != "" }
