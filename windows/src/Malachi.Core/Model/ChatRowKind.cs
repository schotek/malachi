// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/ConversationChat.swift
// (Conversation.ChatRowKind); GTK: ui/internal/conversation/chat.go
// (ChatRowKind).

namespace Malachi.Core.Model;

/// <summary>conversation.ChatRowKind: what a row of the chat is.</summary>
public enum ChatRowKind
{
    /// <summary>
    /// ChatBubble: a message: a mail message, a sent reply, or the
    /// description or a comment of an issue.
    /// </summary>
    Bubble,

    /// <summary>ChatDraft: an unsent reply or comment (a <see cref="ConversationDraftCard"/>).</summary>
    Draft,

    /// <summary>ChatEvent: an issue's status or assignee changes.</summary>
    Event,

    /// <summary>ChatTruncated: the row that says how many older messages are left out.</summary>
    Truncated,
}
