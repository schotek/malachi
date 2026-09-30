// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/AccountsPage.swift
// (accountRowTitle, accountRowSubtitle, accountEditor, jiraEditor,
// accountStatusText, accountRowOffersSignIn, insertIndex, moveAccount); GTK:
// ui/internal/window/accounts_page.go (accountRowTitle, accountRowSubtitle,
// accountEditor, jiraEditor, accountIcon, accountStatusText, accountRow)
// and accounts_reorder.go (insertIndex, moveAccount).

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Text;
using Malachi.Core.Wizard;

namespace Malachi.Core.Model;

/// <summary>The pure parts of the Accounts page of the preferences.</summary>
public static class AccountsPage
{
    /// <summary>
    /// The account name, or the address when unnamed (accounts_page.go
    /// <c>accountRowTitle</c>); an unnamed Jira account shows its site's host
    /// before the address (<see cref="Jira.SiteHost"/>). Unlike the sidebar's
    /// account label (<c>accountLabel</c>) nothing is trimmed: this is the
    /// name as the user typed it.
    /// </summary>
    public static string AccountRowTitle(Account a)
    {
        ArgumentNullException.ThrowIfNull(a);
        if (a.Config.Name.Length > 0)
        {
            return a.Config.Name;
        }
        var host = Jira.SiteHost(a.Config);
        return host.Length > 0 ? host : a.Config.Email;
    }

    /// <summary>
    /// The line under an account's name in Preferences → Accounts
    /// (accounts_page.go <c>accountRowSubtitle</c>): the address of a mail
    /// account, the site's host of a Jira account (its address when the title
    /// shows the host already).
    /// </summary>
    public static string AccountRowSubtitle(Account a)
    {
        ArgumentNullException.ThrowIfNull(a);
        var host = Jira.SiteHost(a.Config);
        return host.Length == 0 || host == AccountRowTitle(a) ? a.Config.Email : host;
    }

    /// <summary>
    /// accounts_page.go <c>jiraAccountIcon</c>: the icon of a Jira account's
    /// row (Adwaita has no ticket; a GTK name, which IconGlyphs maps).
    /// </summary>
    public const string JiraAccountIcon = "checkbox-checked-symbolic";

    /// <summary>
    /// The row icon by account kind (accounts_page.go <c>accountIcon</c>): a
    /// task list for a Jira account, the provider's icon otherwise
    /// (<see cref="Provider.ProviderIcon"/>, the generic mail icon on
    /// Windows, U6).
    /// </summary>
    public static string AccountIcon(Account a)
    {
        ArgumentNullException.ThrowIfNull(a);
        return Jira.IsJira(a.Config) ? JiraAccountIcon : Provider.ProviderIcon(Provider.AccountProvider(a.Config));
    }

    /// <summary>
    /// What edits account <paramref name="a"/>, by its kind (accounts_page.go
    /// <c>accountEditor</c>). Every "edit account" route asks this first,
    /// since the mail wizard builds its pages from <c>imap</c> and
    /// <c>smtp</c>, which a Jira account has not.
    /// </summary>
    public static AccountEditor EditorOf(Account a)
    {
        ArgumentNullException.ThrowIfNull(a);
        return Jira.IsJira(a.Config) ? AccountEditor.Jira : AccountEditor.MailWizard;
    }

    /// <summary>
    /// What edits a Jira account on an "edit account" route
    /// (accounts_page.go <c>jiraEditor</c>): the settings, unless the route
    /// asks for the token (<paramref name="requestToken"/>: the reason of the
    /// sign-in banner or of an account's Sign In in the status flyout, null
    /// when it is not known), which the assistant asks for and says why.
    /// </summary>
    public static JiraEditor JiraEditorFor(ErrorCode? requestToken) =>
        requestToken is null ? JiraEditor.Settings : JiraEditor.Token;

    /// <summary>
    /// The short status shown next to the switch; empty for the
    /// unremarkable idle state (accounts_page.go <c>accountStatusText</c>). A
    /// refused server certificate (<see cref="CertTrust.FromSyncState"/>)
    /// says so instead of "Offline"; an offline or failed account with an
    /// error says why (<see cref="RpcErrorText.EndpointErrorText"/>).
    /// </summary>
    public static string AccountStatusText(SyncState state)
    {
        ArgumentNullException.ThrowIfNull(state);
        if (CertTrust.FromSyncState(state) is { } p)
        {
            return SyncStatusTexts.CertStatusText(p.Category);
        }
        switch (state.Status.Value)
        {
            case SyncStatus.Disabled:
                return L10n.T("Paused");
            case SyncStatus.Syncing:
                return L10n.T("Syncing…");
            case SyncStatus.Offline:
                if (state.Error is { } offline)
                {
                    // TRANSLATORS: account status in Settings → Accounts, %s says why
                    return L10n.T("Offline: %s", RpcErrorText.EndpointErrorText(offline));
                }
                return L10n.T("Offline");
            case SyncStatus.AuthRequired:
                return L10n.T("Sign-in required");
            case SyncStatus.Error:
                if (state.Error is { } failed)
                {
                    // TRANSLATORS: account status in Settings → Accounts, %s says why
                    return L10n.T("Error: %s", RpcErrorText.EndpointErrorText(failed));
                }
                return L10n.T("Error");
            default:
                return "";
        }
    }

    /// <summary>
    /// Whether the row offers "Sign In…" (accounts_page.go
    /// <c>accountRow</c>): an account of the browser sign-in that needs one.
    /// </summary>
    public static bool AccountRowOffersSignIn(Account a)
    {
        ArgumentNullException.ThrowIfNull(a);
        return a.State.Status == SyncStatus.AuthRequired && Provider.SignInKindOf(a.Config) == SignInKind.OAuth;
    }

    /// <summary>
    /// The slot a row dragged from <paramref name="from"/> takes when it is
    /// dropped on the row at <paramref name="target"/> (accounts_reorder.go
    /// <c>insertIndex</c>): before that row when the pointer is in its upper
    /// half, after it otherwise. The result is an index in the list with the
    /// dragged row already taken out, so <paramref name="from"/> means "no
    /// change".
    /// </summary>
    public static int InsertIndex(int from, int target, bool above)
    {
        var to = target;
        if (!above)
        {
            to++;
        }
        if (from < to)
        {
            to--;
        }
        return to;
    }

    /// <summary>
    /// <paramref name="accounts"/> with the entry at <paramref name="from"/>
    /// moved to <paramref name="to"/>, as a new list; out-of-range or equal
    /// positions return the list unchanged (accounts_reorder.go
    /// <c>moveAccount</c>).
    /// </summary>
    public static IReadOnlyList<Account> MoveAccount(IReadOnlyList<Account> accounts, int from, int to)
    {
        ArgumentNullException.ThrowIfNull(accounts);
        if (from == to || from < 0 || to < 0 || from >= accounts.Count || to >= accounts.Count)
        {
            return accounts;
        }
        var output = new List<Account>(accounts);
        var moved = output[from];
        output.RemoveAt(from);
        output.Insert(to, moved);
        return output;
    }
}
