// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Jira/JiraWizard.swift
// (Jira.CredentialPage); GTK: ui/internal/jira/wizard.go (CredentialPage).

namespace Malachi.Core.IssueTrackers;

/// <summary>
/// jira.CredentialPage: how the credentials page asks for the sign-in of a
/// deployment: Jira Cloud wants the Atlassian account's e-mail address and
/// an API token, Data Center a personal access token alone.
/// </summary>
public sealed record JiraCredentialPage
{
    /// <summary>Whether the e-mail field is shown.</summary>
    public bool ShowsLogin { get; init; }

    /// <summary>The e-mail field's label.</summary>
    public string LoginLabel { get; init; } = "";

    /// <summary>The secret field's label.</summary>
    public string TokenLabel { get; init; } = "";

    /// <summary>Where the token comes from.</summary>
    public string Help { get; init; } = "";

    /// <summary>The button that opens <see cref="HelpUrl"/>; "" when the deployment has no such page.</summary>
    public string HelpButton { get; init; } = "";

    /// <summary>The token page; "" when the deployment has none.</summary>
    public string HelpUrl { get; init; } = "";

    /// <summary>The page's banner when a token is needed and none is stored.</summary>
    public string TokenPrompt { get; init; } = "";

    /// <summary>The page's banner when the site refused the token.</summary>
    public string Rejected { get; init; } = "";
}
