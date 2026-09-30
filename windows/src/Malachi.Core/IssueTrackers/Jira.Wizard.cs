// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Jira/JiraWizard.swift; GTK:
// ui/internal/jira/wizard.go (TokenHelpURL, SitePlaceholder, maxNameBytes,
// WizardTexts, CredentialFields, NeedsEmail, CheckSiteInput, Detected,
// DefaultAccountName, SpaceTitle, ApproxCount, SpaceRows, SpacesProblem,
// OfflineChoices, OfflineChoiceLabels, IndexOfOfflineDays, ClassOf,
// Classify, FailureOf).
//
// The assistant that adds a Jira account has three pages: the site
// (account.detectSite), the credentials (checked by account.listSpaces,
// which also lists the spaces) and the spaces with the offline window
// (account.add). Editing an account opens on the credentials page and
// saves with account.update. Go's types are top-level here (JiraWizardPage,
// JiraWizardStep, JiraWizardStrings, JiraCredentialPage, JiraSpaceRow,
// JiraSetup, JiraErrorClass, JiraFailure); Classify reads an RpcException
// where Go unwraps an *api.Error.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Assistants;
using Malachi.Core.Compose;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Transport;

namespace Malachi.Core.IssueTrackers;

public static partial class Jira
{
    /// <summary>
    /// jira.TokenHelpURL: where a Jira Cloud user creates an API token. Data
    /// Center has no such page: its personal access tokens are made in the
    /// user's profile on the site.
    /// </summary>
    public const string TokenHelpUrl = "https://id.atlassian.com/manage-profile/security/api-tokens";

    /// <summary>jira.SitePlaceholder: the example in the empty site address field; not translated.</summary>
    public const string SitePlaceholder = "example.atlassian.net";

    /// <summary>jira.maxNameBytes: the daemon's limit of AccountConfig.Name.</summary>
    internal const int MaxNameBytes = 256;

    /// <summary>jira.WizardTexts: the fixed texts, translated.</summary>
    public static JiraWizardStrings WizardTexts() => new()
    {
        // TRANSLATORS: menu item; "Jira" is a product name.
        AddMenu = L10n.T("Add _Jira Account…"),
        Title = L10n.T("Add Jira Account"),
        // TRANSLATORS: title of the page that asks for the address of a Jira installation.
        SiteTitle = L10n.T("Jira Site"),
        SiteDescription = L10n.T("Enter the address of your Jira site. Malachi Mail finds out whether it runs in the cloud or in your company's data center."),
        SiteAddress = L10n.T("Site Address"),
        LookingUp = L10n.T("Looking up the Jira site"),
        LoadingSpaces = L10n.T("Loading the spaces"),
        // TRANSLATORS: Jira projects, which Jira calls spaces.
        SpacesTitle = L10n.C("jira", "Spaces"),
        SpacesDescription = L10n.T("Choose the spaces whose issues appear as folders."),
        NoSpaces = L10n.T("No spaces are visible to this account"),
        OnlyMine = L10n.T("Only Issues Involving Me"),
        OnlyMineSubtitle = L10n.T("Assigned to you, reported by you or watched by you"),
        KeepOffline = L10n.T("Keep Issues Offline For"),
        // TRANSLATORS: subtitle of "Keep Issues Offline For".
        KeepOfflineSubtitle = L10n.T("Older issues stay on the site and are not shown"),
        Adding = L10n.T("Adding the account"),
        Saving = L10n.T("Saving the account"),
    };

    /// <summary>jira.CredentialFields: the credentials page of a deployment; an unknown one is treated as Data Center.</summary>
    public static JiraCredentialPage CredentialFields(JiraDeployment d)
    {
        var p = new JiraCredentialPage
        {
            LoginLabel = L10n.T("E-mail Address"),
            Rejected = L10n.T("The Jira site rejected the token"),
        };
        if (d == JiraDeployment.Cloud)
        {
            return p with
            {
                ShowsLogin = true,
                TokenLabel = L10n.T("API Token"),
                Help = L10n.T("Create an API token for Malachi Mail in your Atlassian account, then paste it here."),
                // TRANSLATORS: button that opens id.atlassian.com in the browser.
                HelpButton = L10n.T("Create API Token…"),
                HelpUrl = TokenHelpUrl,
                TokenPrompt = L10n.T("Enter the API token for this account"),
            };
        }
        return p with
        {
            TokenLabel = L10n.T("Personal Access Token"),
            Help = L10n.T("Create a personal access token in your Jira profile, then paste it here."),
            TokenPrompt = L10n.T("Enter the personal access token for this account"),
        };
    }

    /// <summary>
    /// jira.NeedsEmail: a Data Center sign-in whose user the site does not
    /// give an e-mail address: the assistant asks for it (the account's
    /// address). A Jira Cloud account's address is its login.
    /// </summary>
    public static bool NeedsEmail(JiraDeployment d, SiteUser user)
    {
        ArgumentNullException.ThrowIfNull(user);
        return d != JiraDeployment.Cloud && string.IsNullOrWhiteSpace(user.Email);
    }

    /// <summary>
    /// jira.CheckSiteInput: what the user typed as the site's address,
    /// checked before account.detectSite: a host ("acme.atlassian.net") or an
    /// http(s) URL. Ok enables Next; Problem is the text under the field, ""
    /// while the field is empty or fine. The daemon checks the address again.
    /// </summary>
    public static (bool Ok, string Problem) CheckSiteInput(string raw)
    {
        ArgumentNullException.ThrowIfNull(raw);
        var s = raw.Trim();
        if (s.Length == 0)
        {
            return (false, "");
        }
        var bad = (false, L10n.T("This is not a web address"));
        foreach (var r in s.EnumerateRunes())
        {
            if (Assistant.IsSpace(r.Value) || Assistant.IsControl(r.Value) || IsFormat(r.Value) || r.Value == '\\')
            {
                return bad;
            }
        }
        var i = s.IndexOf("://", StringComparison.Ordinal);
        if (i >= 0)
        {
            if (CodePoints.ToLower(s[..i]) is not ("https" or "http"))
            {
                return bad;
            }
        }
        else
        {
            s = "https://" + s;
        }
        if (UrlSyntax.Parse(s) is not { } u || u.Rest.Host.Length == 0 || u.Rest.HasUserinfo || UrlSyntax.SplitHostPort(u.Rest.Host).Host.Length == 0)
        {
            return bad;
        }
        return (true, "");
    }

    /// <summary>
    /// jira.Detected: the text under the site field once account.detectSite
    /// answered: the site's title (its deployment's name when it has none),
    /// and for Data Center its version.
    /// </summary>
    public static string Detected(AccountDetectSiteResult res)
    {
        ArgumentNullException.ThrowIfNull(res);
        var title = Clean(res.Title);
        if (title.Length == 0)
        {
            title = DeploymentName(res.Deployment);
        }
        if (Clean(res.Version) is { Length: > 0 } v && res.Deployment == JiraDeployment.Datacenter)
        {
            // TRANSLATORS: the first %s is the name of a Jira site, the second its version ("9.12.4").
            return L10n.T("Found %s, version %s", title, v);
        }
        // TRANSLATORS: %s is the name of a Jira site.
        return L10n.T("Found %s", title);
    }

    /// <summary>jira.DefaultAccountName: the name of a new account: the site's title, else its host, else "Jira".</summary>
    public static string DefaultAccountName(AccountDetectSiteResult res)
    {
        ArgumentNullException.ThrowIfNull(res);
        var name = Clean(res.Title);
        if (name.Length == 0 && UrlSyntax.Parse(res.SiteUrl.Trim()) is { } u)
        {
            name = Clean(CodePoints.ToLower(UrlSyntax.SplitHostPort(u.Rest.Host).Host));
        }
        return name.Length == 0 ? "Jira" : Truncate(name, MaxNameBytes);
    }

    /// <summary>jira.SpaceTitle: a space as the spaces page lists it: "KEY – Name", or whichever of the two it has.</summary>
    public static string SpaceTitle(Space s)
    {
        ArgumentNullException.ThrowIfNull(s);
        var key = Clean(s.Key);
        var name = Clean(s.Name);
        if (key.Length == 0)
        {
            return name;
        }
        return name.Length == 0 ? key : key + " – " + name;
    }

    /// <summary>jira.ApproxCount: the estimate of a space's issues in the offline window; "" when the daemon did not count (-1).</summary>
    public static string ApproxCount(int n) =>
        n < 0 ? "" :
        // TRANSLATORS: an estimate of the issues of a Jira space.
        L10n.N("about %d issue", "about %d issues", n, n);

    /// <summary>jira.SpaceRows: the spaces of account.listSpaces for the spaces page, in the daemon's order.</summary>
    public static IReadOnlyList<JiraSpaceRow> SpaceRows(IReadOnlyList<Space>? spaces) =>
        [.. (spaces ?? []).Select(s => new JiraSpaceRow
        {
            Id = s.Id,
            Title = SpaceTitle(s),
            Count = ApproxCount(s.Issues),
            ServiceDesk = s.ServiceDesk == true,
        })];

    /// <summary>jira.SpacesProblem: the reason the spaces page cannot add the account with <paramref name="selected"/> spaces chosen; "" when it can.</summary>
    public static string SpacesProblem(int selected)
    {
        if (selected <= 0)
        {
            return L10n.T("Select at least one space");
        }
        if (selected > API.Limits.MaxJiraSpaces)
        {
            return L10n.N("Select at most %d space", "Select at most %d spaces", API.Limits.MaxJiraSpaces, API.Limits.MaxJiraSpaces);
        }
        return "";
    }

    /// <summary>
    /// jira.OfflineChoices: the offline windows the assistant offers, in
    /// days: 1 week, 1 month, 3 months, 1 year. There is no "Everything": a
    /// Jira account keeps at most <see cref="API.Limits.MaxJiraOfflineDays"/>.
    /// </summary>
    public static IReadOnlyList<int> OfflineChoices { get; } = [7, 30, 90, 365];

    /// <summary>jira.OfflineChoiceLabels: the labels of <see cref="OfflineChoices"/>, in order.</summary>
    public static IReadOnlyList<string> OfflineChoiceLabels() =>
        [L10n.T("1 week"), L10n.T("1 month"), L10n.T("3 months"), L10n.T("1 year")];

    /// <summary>
    /// jira.IndexOfOfflineDays: the position in <see cref="OfflineChoices"/>
    /// shown for JiraConfig.OfflineDays: 0 (or less) is the default window,
    /// any other value the nearest choice, a tie going to the shorter.
    /// </summary>
    public static int IndexOfOfflineDays(int days)
    {
        if (days <= 0)
        {
            days = API.Limits.DefaultJiraOfflineDays;
        }
        var best = 0;
        var bestDiff = -1;
        for (var i = 0; i < OfflineChoices.Count; i++)
        {
            var diff = Math.Abs(OfflineChoices[i] - days);
            if (bestDiff < 0 || diff < bestDiff)
            {
                best = i;
                bestDiff = diff;
            }
        }
        return best;
    }

    /// <summary>jira.ClassOf: the class of an API error code; one that is no API error is Other.</summary>
    public static JiraErrorClass ClassOf(ErrorCode code) => code.Value switch
    {
        ErrorCode.InvalidArgument or ErrorCode.InvalidParams => JiraErrorClass.Invalid,
        ErrorCode.ServerError => JiraErrorClass.Server,
        ErrorCode.Offline or ErrorCode.NetworkError or ErrorCode.ServerTimeout => JiraErrorClass.Network,
        ErrorCode.TlsError => JiraErrorClass.Tls,
        ErrorCode.AuthFailed => JiraErrorClass.AuthFailed,
        ErrorCode.AuthRequired => JiraErrorClass.AuthRequired,
        ErrorCode.Conflict => JiraErrorClass.Conflict,
        _ => JiraErrorClass.Other,
    };

    /// <summary>jira.Classify: <see cref="ClassOf"/> for a failure of an RPC call; anything but the daemon's error is Other.</summary>
    public static JiraErrorClass Classify(Exception? error) => error is RpcException e ? ClassOf(e.Code) : JiraErrorClass.Other;

    /// <summary>
    /// jira.FailureOf: a failed step of the assistant for a deployment;
    /// <paramref name="editing"/> when the assistant edits an existing
    /// account (it saves from the credentials page).
    /// </summary>
    public static JiraFailure FailureOf(JiraWizardStep step, JiraErrorClass errorClass, JiraDeployment d, bool editing)
    {
        var creds = CredentialFields(d);
        switch (step)
        {
            case JiraWizardStep.Detect:
                return new JiraFailure(JiraWizardPage.Site, errorClass switch
                {
                    JiraErrorClass.Server => L10n.T("This address is not a Jira site"),
                    JiraErrorClass.Invalid => L10n.T("This is not a web address"),
                    _ => "",
                }, L10n.T("Looking up the Jira site"));
            case JiraWizardStep.Spaces:
                return new JiraFailure(JiraWizardPage.Credentials, errorClass switch
                {
                    JiraErrorClass.AuthFailed => creds.Rejected,
                    JiraErrorClass.AuthRequired => creds.TokenPrompt,
                    _ => "",
                }, L10n.T("Loading the spaces"));
            default:
                break;
        }
        var f = editing
            ? new JiraFailure(JiraWizardPage.Credentials, "", L10n.T("Saving the account"))
            : new JiraFailure(JiraWizardPage.Spaces, "", L10n.T("Adding the account"));
        return errorClass switch
        {
            JiraErrorClass.Conflict => f with { Banner = L10n.T("An account for this Jira site already exists") },
            JiraErrorClass.AuthFailed => f with { Page = JiraWizardPage.Credentials, Banner = creds.Rejected },
            JiraErrorClass.AuthRequired => f with { Page = JiraWizardPage.Credentials, Banner = creds.TokenPrompt },
            _ => f,
        };
    }
}
