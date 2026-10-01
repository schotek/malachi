// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/Conversation.swift
// (Conversation.Model); GTK: ui/internal/conversation/conversation.go
// (Model, Model.Index).

using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.IssueTrackers;

namespace Malachi.Core.Model;

/// <summary>
/// conversation.Model: what the reading pane shows of one conversation. Its
/// equality compares <see cref="Items"/> by reference, as every record's
/// does.
/// </summary>
public sealed record ConversationModel
{
    /// <summary>The conversation; Merge ignores a message of another one.</summary>
    public ThreadId Thread { get; init; } = "";

    /// <summary>
    /// The stack, oldest first by (date, id): a Truncated row on top when
    /// Earlier > 0, then the members and the sent cards. Empty when the
    /// conversation has no member to show (sent cards alone are no
    /// conversation of the folder): the pane shows its empty page then, or, when Remove
    /// took the last shown member and Earlier > 0, loads the conversation
    /// again.
    /// </summary>
    public IReadOnlyList<ConversationItem> Items { get; init; } = [];

    /// <summary>
    /// The issue card shown once above the stack of a Jira conversation
    /// (Jira.IssueCard without an item: no badges); null for mail and for an
    /// empty model.
    /// </summary>
    public JiraCard? Issue { get; init; }

    /// <summary>
    /// How many older members of the conversation in the folder are not in
    /// Items (sent cards older than the oldest member shown are left out
    /// then, and not counted).
    /// </summary>
    public int Earlier { get; init; }

    /// <summary>
    /// The member opening the conversation marks read (after the usual
    /// delay): the newest message card that is neither a queued message of
    /// the outbox nor a sent card, when it is unread; null when it is read or there is none.
    /// Older unread members stay unread, and events are never marked. A
    /// client acts on it when the conversation is opened, never after a flag
    /// change: a member the user marked unread stays unread.
    /// </summary>
    public MessageId? MarkRead { get; init; }

    /// <summary>The index into Items the pane scrolls to when it opens the conversation: the newest item; -1 when Items is empty.</summary>
    public int ScrollTo { get; init; } = -1;

    /// <summary>conversation.Model.Index: the position in Items of the member <paramref name="id"/>; -1 when it is not shown.</summary>
    public int Index(MessageId? id)
    {
        if (id is not { } want || want.Value.Length == 0)
        {
            return -1;
        }
        for (var i = 0; i < Items.Count; i++)
        {
            if (Items[i].Kind != ConversationItemKind.Truncated && Items[i].Message!.Id == want)
            {
                return i;
            }
        }
        return -1;
    }
}
