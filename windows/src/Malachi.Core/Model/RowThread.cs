// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/MailModel+Threads.swift
// (RowThread); GTK: ui/internal/widget/message_row.go (Thread, with its
// Tag). Immutable.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.IssueTrackers;

namespace Malachi.Core.Model;

/// <summary>
/// What a conversation row displays, a projection of a thread summary over
/// the members of the listed folder. A conversation of a Jira account is one
/// issue (<see cref="Issue"/>, <see cref="Jira.ThreadRowIssue"/>). Every
/// text is hostile input, shown as plain text.
/// </summary>
public sealed record RowThread
{
    /// <summary>Newest first, as the daemon sent them.</summary>
    public required IReadOnlyList<Address> Participants { get; init; }

    /// <summary>The subject.</summary>
    public required string Subject { get; init; }

    /// <summary>The newest member's preview line.</summary>
    public required string Snippet { get; init; }

    /// <summary>The newest member's date.</summary>
    public required DateTimeOffset Date { get; init; }

    /// <summary>Members in the folder.</summary>
    public required int Count { get; init; }

    /// <summary>Unread members in the folder.</summary>
    public required int Unread { get; init; }

    /// <summary>Some member is flagged.</summary>
    public required bool Flagged { get; init; }

    /// <summary>Some member carries attachments.</summary>
    public required bool HasAttachments { get; init; }

    /// <summary>Unfolded.</summary>
    public required bool Expanded { get; init; }

    /// <summary>Unfolded, members not answered yet.</summary>
    public required bool Loading { get; init; }

    /// <summary>
    /// The issue's key, summary and status, and the change its latest member
    /// made (an event's text); null for a conversation of a mail account.
    /// </summary>
    public JiraIssueRow? Issue { get; init; }

    /// <summary>The tag of the latest member (<see cref="Malachi.Core.Bulk.BulkMail.Tag"/>); empty for personal mail.</summary>
    public string Tag { get; init; } = "";
}
