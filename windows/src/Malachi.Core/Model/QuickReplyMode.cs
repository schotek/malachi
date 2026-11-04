// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/ConversationReply.swift
// (Conversation.QuickReplyMode); GTK: ui/internal/conversation/reply.go
// (QuickReplyMode, QuickReply, QuickReplyAll, QuickForward). Swift's
// computed properties composeMode and composeKind are the extension
// properties of QuickReplyModeExtensions.

namespace Malachi.Core.Model;

/// <summary>conversation.QuickReplyMode: what the quick-reply bar's mode button chose.</summary>
public enum QuickReplyMode
{
    /// <summary>
    /// QuickReply: answers the sender (on an issue: a public comment, which
    /// the view labels with <see cref="IssueTrackers.Jira.ReplyLabel"/>).
    /// </summary>
    Reply,

    /// <summary>QuickReplyAll: answers everyone.</summary>
    ReplyAll,

    /// <summary>
    /// QuickForward: the menu's Forward…: it never sends from the bar but
    /// opens the compose window with a forward of the target.
    /// </summary>
    Forward,
}
