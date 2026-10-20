// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Board/BoardConversationChat.swift; Go:
// ui/internal/board/conversation_chat.go (ChatBubble, ChatRows, ChatOpens).
//
// The chat layout of a case's conversation, the board's counterpart of the
// chat of Mail (Model/Conversation.Chat.cs), which the toggle in the
// block's heading row (Text.ChatToggleLabel, the Mail key
// conversation-chat-layout) switches to. A bubble's text is the member's
// MessageCard.Text, the text board.get carries (the quoted history cut as
// trimQuoted cuts it, the signature cut, at most 8000 bytes): the chat
// fetches no body and shows no web view, so the samples read the same. The
// detail keeps its place at the top; the chat reads down from the oldest
// bubble to the newest, never scrolled to its end.

using System;
using System.Collections.Generic;

namespace Malachi.Core.Boards;

public static partial class Board
{
    /// <summary>
    /// One row of the chat layout of a case's conversation: the member
    /// <c>Detail.Messages[Index]</c>, on the right when it is the user's own.
    /// </summary>
    /// <param name="Index">The member's index in <see cref="Detail.Messages"/>.</param>
    /// <param name="Right">The user's own (<see cref="MessageCard.Mine"/>).</param>
    public readonly record struct ChatBubble(int Index, bool Right);

    /// <summary>
    /// The rows of the chat layout of a case's conversation, one bubble per
    /// member in the order given (oldest first, as <see cref="Detail.Messages"/>
    /// and the cards are): the user's own messages on the right, every other
    /// on the left. The board's members hold no Jira events, no drafts and
    /// no older-messages row, so the chat has no centred rows; the suggested
    /// reply is the inline editor above the block, never a bubble. A sample
    /// member (no id) is a bubble too, but opens nothing
    /// (<see cref="ChatOpens"/>). The block's loading, failure and empty
    /// notes (<see cref="Detail.MessagesNote"/>) stand in place of the chat
    /// as they do of the cards.
    /// </summary>
    public static IReadOnlyList<ChatBubble> ChatRows(IReadOnlyList<MessageCard>? messages)
    {
        if (messages is null || messages.Count == 0)
        {
            return [];
        }
        var rows = new ChatBubble[messages.Count];
        for (var i = 0; i < messages.Count; i++)
        {
            rows[i] = new ChatBubble(i, messages[i].Mine);
        }
        return rows;
    }

    /// <summary>
    /// Whether a double tap on the bubble of <paramref name="m"/> can open
    /// the message in its own window: a member with an id (never a sample).
    /// </summary>
    public static bool ChatOpens(MessageCard m)
    {
        ArgumentNullException.ThrowIfNull(m);
        return m.Id is { } id && !string.IsNullOrEmpty(id.Value);
    }
}
