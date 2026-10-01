// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/MailModel.swift (RowMessage); GTK:
// ui/internal/widget/message_row.go (Message, with its Tag). Immutable; the model builds
// a row's variant with `with`. Its equality compares the lists by
// reference, as every record's does.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.IssueTrackers;

namespace Malachi.Core.Model;

/// <summary>
/// What a message row displays, a projection of a list summary. A search
/// result adds where it lies (<see cref="Origin"/>, with the full path and
/// account as <see cref="OriginTooltip"/>) and the matched words of
/// <see cref="Snippet"/> (<see cref="Highlights"/>, UTF-8 byte ranges into
/// it, as the daemon sent them). A message of a Jira account adds its issue
/// (<see cref="Issue"/>, <see cref="Jira.RowIssue"/>). Every text is hostile
/// input, shown as plain text.
/// </summary>
public sealed record RowMessage
{
    /// <summary>The senders.</summary>
    public required IReadOnlyList<Address> From { get; init; }

    /// <summary>The subject.</summary>
    public required string Subject { get; init; }

    /// <summary>The preview line, or the excerpt of a search result.</summary>
    public required string Snippet { get; init; }

    /// <summary>The date.</summary>
    public required DateTimeOffset Date { get; init; }

    /// <summary>Without the seen flag.</summary>
    public required bool Unread { get; init; }

    /// <summary>With the flagged flag.</summary>
    public required bool Flagged { get; init; }

    /// <summary>Whether it carries attachments.</summary>
    public required bool HasAttachments { get; init; }

    /// <summary>Where a search result lies; empty outside a search over several folders.</summary>
    public string Origin { get; init; } = "";

    /// <summary>The folder's path and the account of a search result.</summary>
    public string OriginTooltip { get; init; } = "";

    /// <summary>The matched words of a search result's excerpt, byte ranges into <see cref="Snippet"/>.</summary>
    public IReadOnlyList<MatchRange> Highlights { get; init; } = [];

    /// <summary>
    /// The issue's key, summary and status, and whether the row is an event
    /// (a status or assignee change); null for a mail message.
    /// </summary>
    public JiraIssueRow? Issue { get; init; }

    /// <summary>
    /// The neutral pill of a bulk message (<see cref="Malachi.Core.Bulk.BulkMail.Tag"/>:
    /// "Bulk", "Mailing List", "Automated"); empty for personal mail.
    /// </summary>
    public string Tag { get; init; } = "";
}
