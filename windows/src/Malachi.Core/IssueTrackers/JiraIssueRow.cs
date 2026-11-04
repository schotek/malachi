// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Jira/JiraView.swift (Jira.IssueRow);
// GTK: ui/internal/jira/jira.go (IssueRow).

namespace Malachi.Core.IssueTrackers;

/// <summary>
/// jira.IssueRow: what a message list row shows of an issue: the key, the
/// summary and the status pill on the subject line, the internal badge, and
/// for an event the changes instead of the preview.
/// </summary>
public sealed record JiraIssueRow
{
    /// <summary>The key, cleaned.</summary>
    public string Key { get; init; } = "";

    /// <summary>The summary, cleaned.</summary>
    public string Summary { get; init; } = "";

    /// <summary>The status pill's text.</summary>
    public string Status { get; init; } = "";

    /// <summary>The status pill's colour.</summary>
    public JiraStatusStyle StatusStyle { get; init; }

    /// <summary>An internal comment; <see cref="InternalLabel"/> is its badge.</summary>
    public bool Internal { get; init; }

    /// <summary>The badge; "" when not internal.</summary>
    public string InternalLabel { get; init; } = "";

    /// <summary>
    /// An event row (for a conversation: its latest member is one):
    /// <see cref="EventText"/> replaces the preview, and the row has the
    /// secondary style.
    /// </summary>
    public bool Event { get; init; }

    /// <summary>The changes on one line.</summary>
    public string EventText { get; init; } = "";

    /// <summary>Whether the row shows as unread. An event never does, whatever its flags.</summary>
    public bool Unread { get; init; }
}
