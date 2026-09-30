// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Jira/JiraSettings.swift (Jira.ListKind);
// GTK: ui/internal/jira/settings.go (ListKind).

namespace Malachi.Core.IssueTrackers;

/// <summary>jira.ListKind: one of the lists of texts the settings page edits.</summary>
public enum JiraListKind
{
    /// <summary>JiraConfig.BotNames.</summary>
    BotNames,

    /// <summary>JiraConfig.MetadataFilters.</summary>
    MetadataFilters,

    /// <summary>JiraConfig.AuthorPrefixes.</summary>
    AuthorPrefixes,

    /// <summary>JiraConfig.NotificationSenders.</summary>
    Senders,
}
