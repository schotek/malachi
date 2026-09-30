// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Jira/JiraView.swift (Jira.CardRow);
// GTK: ui/internal/jira/jira.go (CardRow).

namespace Malachi.Core.IssueTrackers;

/// <summary>
/// jira.CardRow: one line of the issue card's grid, a field and its value.
/// <see cref="Missing"/> marks a placeholder value ("Unassigned", "None"),
/// shown dimmed.
/// </summary>
public sealed record JiraCardRow
{
    /// <summary>The field's name.</summary>
    public string Label { get; init; } = "";

    /// <summary>Its value, cleaned, or the placeholder.</summary>
    public string Value { get; init; } = "";

    /// <summary>Whether <see cref="Value"/> is a placeholder.</summary>
    public bool Missing { get; init; }
}
