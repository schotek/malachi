// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Jira/JiraWizard.swift (Jira.Setup and
// its config()); GTK: ui/internal/jira/wizard.go (Setup, Setup.Config).

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;

namespace Malachi.Core.IssueTrackers;

/// <summary>jira.Setup: what the assistant collected for a new account.</summary>
public sealed record JiraSetup
{
    /// <summary>What account.detectSite found.</summary>
    public required AccountDetectSiteResult Site { get; init; }

    /// <summary>The Atlassian account's e-mail address (Jira Cloud), which is also the account's address.</summary>
    public string Login { get; init; } = "";

    /// <summary>
    /// The account's address on Data Center: the signed-in user's
    /// (account.listSpaces), or what the user typed when the site hides it.
    /// </summary>
    public string Email { get; init; } = "";

    /// <summary>The account's name; "" is <see cref="Jira.DefaultAccountName"/>.</summary>
    public string Name { get; init; } = "";

    /// <summary>The chosen spaces, in the order shown.</summary>
    public IReadOnlyList<Space> Spaces { get; init; } = [];

    /// <summary>Only the issues involving the user.</summary>
    public bool OnlyMine { get; init; }

    /// <summary>The offline window; 0 is <see cref="API.Limits.DefaultJiraOfflineDays"/>.</summary>
    public int OfflineDays { get; init; }

    /// <summary>
    /// jira.Setup.Config: the AccountConfig of account.add (and of
    /// account.listSpaces, which accepts it without spaces). Go's empty
    /// values are null here, as omitempty leaves them off the wire.
    /// </summary>
    public AccountConfig Config()
    {
        var days = Math.Min(Math.Max(OfflineDays, 0), API.Limits.MaxJiraOfflineDays);
        var jc = new JiraConfig
        {
            SiteUrl = Site.SiteUrl,
            Deployment = Site.Deployment,
            CloudId = NonEmpty(Site.CloudId ?? ""),
            Spaces = [.. Spaces.Select(s => new SpaceRef { Id = s.Id, Key = s.Key, Name = NonEmpty(s.Name) })],
            OfflineDays = days == 0 ? null : days,
            OnlyMine = OnlyMine ? true : null,
        };
        var email = Email.Trim();
        if (Site.Deployment == JiraDeployment.Cloud)
        {
            var login = Login.Trim();
            jc = jc with { Login = NonEmpty(login) };
            email = login;
        }
        else
        {
            jc = jc with { CloudId = null };
        }
        var name = Jira.Truncate(Name.Trim(), Jira.MaxNameBytes);
        if (name.Length == 0)
        {
            name = Jira.DefaultAccountName(Site);
        }
        return new AccountConfig { Name = name, Email = email, Kind = AccountKind.Jira, Jira = jc };
    }

    // Go's "" as null.
    private static string? NonEmpty(string s) => s.Length == 0 ? null : s;
}
