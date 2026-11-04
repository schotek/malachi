// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/Conversation.swift
// (Conversation.ItemKind); GTK: ui/internal/conversation/conversation.go
// (ItemKind).

namespace Malachi.Core.Model;

/// <summary>conversation.ItemKind: what an item of the stack is.</summary>
public enum ConversationItemKind
{
    /// <summary>
    /// A message card: a mail message, or the description or a comment of an
    /// issue. Its body is shown in full.
    /// </summary>
    Message,

    /// <summary>
    /// A compact row of an issue's status or assignee changes: native text
    /// only (never a web view), never unread, never marked read.
    /// </summary>
    Event,

    /// <summary>The row that says how many older members are left out (thread.get returns the newest MaxThreadMessages).</summary>
    Truncated,
}
