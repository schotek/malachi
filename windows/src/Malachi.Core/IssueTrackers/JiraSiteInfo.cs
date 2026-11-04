// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Jira/JiraSettings.swift (Jira.SiteInfo);
// GTK: ui/internal/jira/settings.go (SiteInfo).

namespace Malachi.Core.IssueTrackers;

/// <summary>jira.SiteInfo: the read-only part of the settings page.</summary>
public sealed record JiraSiteInfo
{
    /// <summary>The site's URL.</summary>
    public string Address { get; init; } = "";

    /// <summary>Its brand name ("Jira Cloud").</summary>
    public string Deployment { get; init; } = "";

    /// <summary>
    /// Who the account signs in as: the user's name once account.listSpaces
    /// told it, the login or the account's address before.
    /// </summary>
    public string User { get; init; } = "";

    /// <summary>The address under a name; "" when <see cref="User"/> is the address already.</summary>
    public string UserDetail { get; init; } = "";

    /// <summary>The token of the deployment ("API Token").</summary>
    public string TokenLabel { get; init; } = "";
}
