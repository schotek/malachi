// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Jira/JiraSettings.swift
// (Jira.StatusChoice); GTK: ui/internal/jira/settings.go (StatusChoice).

using System.Collections.Generic;

namespace Malachi.Core.IssueTrackers;

/// <summary>
/// jira.StatusChoice: one check box of the picker of closed statuses: a
/// name, which stands for every status of the site called so in its
/// category (team-managed spaces each have their own "Done"). Its equality
/// compares <see cref="Ids"/> by reference, as every record's does.
/// </summary>
public sealed record JiraStatusChoice
{
    /// <summary>The status's name, cleaned.</summary>
    public string Name { get; init; } = "";

    /// <summary>The ids of the statuses of that name.</summary>
    public IReadOnlyList<string> Ids { get; init; } = [];

    /// <summary>Whether they count as closed.</summary>
    public bool Selected { get; init; }
}
