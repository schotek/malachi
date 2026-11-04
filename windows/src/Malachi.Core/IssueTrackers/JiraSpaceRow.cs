// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Jira/JiraWizard.swift (Jira.SpaceRow);
// GTK: ui/internal/jira/wizard.go (SpaceRow).

namespace Malachi.Core.IssueTrackers;

/// <summary>
/// jira.SpaceRow: one space of the spaces page: a check box with
/// <see cref="Title"/> and the estimate <see cref="Count"/> ("" when not
/// counted).
/// </summary>
public sealed record JiraSpaceRow
{
    /// <summary>The space's id.</summary>
    public string Id { get; init; } = "";

    /// <summary>"KEY – Name".</summary>
    public string Title { get; init; } = "";

    /// <summary>The estimate of its issues; "" when not counted.</summary>
    public string Count { get; init; } = "";

    /// <summary>A service-desk space.</summary>
    public bool ServiceDesk { get; init; }
}
