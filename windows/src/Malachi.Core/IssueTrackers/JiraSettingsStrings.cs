// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Jira/JiraSettings.swift
// (Jira.SettingsStrings); GTK: ui/internal/jira/settings.go
// (SettingsStrings).

namespace Malachi.Core.IssueTrackers;

/// <summary>jira.SettingsStrings: the fixed texts of the settings page.</summary>
public sealed record JiraSettingsStrings
{
    /// <summary>The window's title.</summary>
    public string Title { get; init; } = "";

    /// <summary>The site's section.</summary>
    public string SiteTitle { get; init; } = "";

    /// <summary>The site's row.</summary>
    public string SiteAddress { get; init; } = "";

    /// <summary>The account name's row.</summary>
    public string AccountName { get; init; } = "";

    /// <summary>The signed-in user's row.</summary>
    public string SignedInAs { get; init; } = "";

    /// <summary>The button of the token's row.</summary>
    public string ReplaceToken { get; init; } = "";

    /// <summary>The spaces' section.</summary>
    public string SpacesTitle { get; init; } = "";

    /// <summary>Its explanation.</summary>
    public string SpacesDescription { get; init; } = "";

    /// <summary>The text of an empty list.</summary>
    public string NoSpaces { get; init; } = "";

    /// <summary>The synchronisation's section.</summary>
    public string SyncTitle { get; init; } = "";

    /// <summary>The offline window.</summary>
    public string KeepOffline { get; init; } = "";

    /// <summary>Its subtitle.</summary>
    public string KeepOfflineSubtitle { get; init; } = "";

    /// <summary>The switch that keeps only the user's issues.</summary>
    public string OnlyMine { get; init; } = "";

    /// <summary>Its subtitle.</summary>
    public string OnlyMineSubtitle { get; init; } = "";

    /// <summary>The switch of the event rows.</summary>
    public string ShowEvents { get; init; } = "";

    /// <summary>The folders' section (its switches are the views' titles).</summary>
    public string FoldersTitle { get; init; } = "";

    /// <summary>The picker of the closed statuses.</summary>
    public string ClosedStatuses { get; init; } = "";

    /// <summary>Its subtitle.</summary>
    public string ClosedStatusesSubtitle { get; init; } = "";

    /// <summary>The notification e-mails' section.</summary>
    public string NotificationTitle { get; init; } = "";

    /// <summary>The mode.</summary>
    public string NotificationMode { get; init; } = "";

    /// <summary>The senders.</summary>
    public string Senders { get; init; } = "";

    /// <summary>Their subtitle.</summary>
    public string SendersSubtitle { get; init; } = "";

    /// <summary>The bots' section.</summary>
    public string BotsTitle { get; init; } = "";

    /// <summary>The bot names.</summary>
    public string BotNames { get; init; } = "";

    /// <summary>Their subtitle.</summary>
    public string BotNamesSubtitle { get; init; } = "";

    /// <summary>The hidden lines.</summary>
    public string HiddenLines { get; init; } = "";

    /// <summary>Their subtitle.</summary>
    public string HiddenLinesSubtitle { get; init; } = "";

    /// <summary>The name prefixes.</summary>
    public string NamePrefixes { get; init; } = "";

    /// <summary>Their subtitle.</summary>
    public string NamePrefixesSubtitle { get; init; } = "";

    /// <summary>The button next to a list's field.</summary>
    public string Add { get; init; } = "";

    /// <summary>The button of a list's entry.</summary>
    public string Remove { get; init; } = "";

    /// <summary>The progress of account.listSpaces.</summary>
    public string Loading { get; init; } = "";

    /// <summary>The progress of account.update.</summary>
    public string Saving { get; init; } = "";
}
