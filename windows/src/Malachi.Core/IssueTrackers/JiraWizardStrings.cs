// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Jira/JiraWizard.swift
// (Jira.WizardStrings); GTK: ui/internal/jira/wizard.go (WizardStrings).

namespace Malachi.Core.IssueTrackers;

/// <summary>jira.WizardStrings: the fixed texts of the Jira account assistant.</summary>
public sealed record JiraWizardStrings
{
    /// <summary>The menu item, with a mnemonic.</summary>
    public string AddMenu { get; init; } = "";

    /// <summary>The window's title.</summary>
    public string Title { get; init; } = "";

    /// <summary>The site page's title.</summary>
    public string SiteTitle { get; init; } = "";

    /// <summary>The site page's explanation.</summary>
    public string SiteDescription { get; init; } = "";

    /// <summary>The address field's label.</summary>
    public string SiteAddress { get; init; } = "";

    /// <summary>The progress while the site is looked up.</summary>
    public string LookingUp { get; init; } = "";

    /// <summary>The progress while the spaces load.</summary>
    public string LoadingSpaces { get; init; } = "";

    /// <summary>The spaces page's title.</summary>
    public string SpacesTitle { get; init; } = "";

    /// <summary>The spaces page's explanation.</summary>
    public string SpacesDescription { get; init; } = "";

    /// <summary>The text of an empty list.</summary>
    public string NoSpaces { get; init; } = "";

    /// <summary>The switch that keeps only the user's issues.</summary>
    public string OnlyMine { get; init; } = "";

    /// <summary>Its subtitle.</summary>
    public string OnlyMineSubtitle { get; init; } = "";

    /// <summary>The offline window's row.</summary>
    public string KeepOffline { get; init; } = "";

    /// <summary>Its subtitle.</summary>
    public string KeepOfflineSubtitle { get; init; } = "";

    /// <summary>The progress of account.add.</summary>
    public string Adding { get; init; } = "";

    /// <summary>The progress of account.update.</summary>
    public string Saving { get; init; } = "";
}
