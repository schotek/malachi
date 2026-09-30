// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/MailModel+Threads.swift (ListRow)
// and ConversationController.swift (ListRow.showsConversation); GTK:
// ui/internal/window/thread_model.go (listRow) and
// conversation_controller.go (rowShowsConversation). Immutable. Its equality
// compares the summaries' lists by reference, as every record's does; a
// view diffs rows by Key.

using Malachi.Core.Api;

namespace Malachi.Core.Model;

/// <summary>What one row of the list stands for.</summary>
public sealed record ListRow
{
    /// <summary>The row's address.</summary>
    public required ListKey Key { get; init; }

    /// <summary>A folded conversation (two or more members).</summary>
    public bool Thread { get; init; }

    /// <summary>An expanded member, indented under its conversation row.</summary>
    public bool Member { get; init; }

    /// <summary>
    /// The message the row shows; on a conversation row its newest folder
    /// member.
    /// </summary>
    public required MessageSummary Message { get; init; }

    /// <summary>Conversation rows: the aggregates the row shows.</summary>
    public ThreadSummary? Summary { get; init; }

    /// <summary>Conversation-row state: unfolded.</summary>
    public bool Expanded { get; init; }

    /// <summary>Conversation-row state: unfolded while thread.get has not answered yet.</summary>
    public bool Loading { get; init; }

    /// <summary>
    /// A folded conversation row of the grouped list whose selection shows
    /// the whole conversation in the reading pane: a conversation row (not a
    /// member row, not a single-message row) with two or more members in the
    /// folder (<see cref="Conversation.IsConversationRow"/>). Every other row
    /// shows its message alone.
    /// </summary>
    public bool ShowsConversation => Thread && !Member && Key.Thread is not null && Summary is { } s && Conversation.IsConversationRow(s);
}
