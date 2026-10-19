// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/ConversationChat.swift
// (Conversation.ChatSide); GTK: ui/internal/conversation/chat.go (ChatSide).

namespace Malachi.Core.Model;

/// <summary>conversation.ChatSide: where a row of the chat stands.</summary>
public enum ChatSide
{
    /// <summary>ChatLeft: a message of someone else.</summary>
    Left,

    /// <summary>ChatRight: the user's own message, sent reply or draft.</summary>
    Right,

    /// <summary>ChatCenter: a row without a bubble (an issue event, the row of older messages).</summary>
    Center,
}
