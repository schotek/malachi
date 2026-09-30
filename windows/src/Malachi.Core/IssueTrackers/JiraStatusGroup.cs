// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Jira/JiraSettings.swift
// (Jira.StatusGroup); GTK: ui/internal/jira/settings.go (StatusGroup).

using System.Collections.Generic;
using Malachi.Core.Api;

namespace Malachi.Core.IssueTrackers;

/// <summary>jira.StatusGroup: the statuses of a category, under its title and in its colour.</summary>
public sealed record JiraStatusGroup
{
    /// <summary>The category; "" for the group of the statuses without a known one.</summary>
    public IssueStatusCategory Category { get; init; } = "";

    /// <summary>The group's title.</summary>
    public string Title { get; init; } = "";

    /// <summary>The category's colour.</summary>
    public JiraStatusStyle Style { get; init; }

    /// <summary>One check box per name.</summary>
    public IReadOnlyList<JiraStatusChoice> Choices { get; init; } = [];
}
