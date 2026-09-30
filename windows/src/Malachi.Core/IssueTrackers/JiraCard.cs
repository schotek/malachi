// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Jira/JiraView.swift (Jira.Card); GTK:
// ui/internal/jira/jira.go (Card).

using System.Collections.Generic;

namespace Malachi.Core.IssueTrackers;

/// <summary>
/// jira.Card: the issue card above a Jira message in the reading pane: the
/// key as a link to the issue, the summary, the status pill, the grid of the
/// other fields, and what the message is of the issue. Its equality compares
/// <see cref="Rows"/> by reference, as every record's does; a test compares
/// them apart.
/// </summary>
public sealed record JiraCard
{
    /// <summary>The key; it opens <see cref="Url"/> in the browser (after <see cref="Jira.IsIssueUrl"/>).</summary>
    public string Key { get; init; } = "";

    /// <summary>The issue's page.</summary>
    public string Url { get; init; } = "";

    /// <summary>The link's tooltip.</summary>
    public string OpenTooltip { get; init; } = "";

    /// <summary>The summary, cleaned.</summary>
    public string Summary { get; init; } = "";

    /// <summary>The pill's text; "" hides the pill.</summary>
    public string Status { get; init; } = "";

    /// <summary>The pill's accessible name.</summary>
    public string StatusLabel { get; init; } = "";

    /// <summary>The pill's colour.</summary>
    public JiraStatusStyle StatusStyle { get; init; }

    /// <summary>Assignee, Priority, Type and Reporter, in this order.</summary>
    public IReadOnlyList<JiraCardRow> Rows { get; init; } = [];

    /// <summary>An internal comment of a service-desk issue: the card shows the badge <see cref="InternalLabel"/>.</summary>
    public bool Internal { get; init; }

    /// <summary>The badge; "" when not internal.</summary>
    public string InternalLabel { get; init; } = "";

    /// <summary>The integration that posted the comment for its author ("via Issue Sync"); "" when none.</summary>
    public string Via { get; init; } = "";

    /// <summary>That the comment was changed after it was posted ("Edited"); "" when not.</summary>
    public string Edited { get; init; } = "";
}
