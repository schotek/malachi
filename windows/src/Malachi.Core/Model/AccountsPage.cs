// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/AccountsPage.swift
// (accountRowTitle, accountStatusText, accountRowOffersSignIn, insertIndex,
// moveAccount); GTK: ui/internal/window/accounts_page.go (accountRowTitle,
// accountStatusText, accountRow) and accounts_reorder.go (insertIndex,
// moveAccount).

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.Text;
using Malachi.Core.Wizard;

namespace Malachi.Core.Model;

/// <summary>The pure parts of the Accounts page of the preferences.</summary>
public static class AccountsPage
{
    /// <summary>
    /// The account name, or the address when unnamed (accounts_page.go
    /// <c>accountRowTitle</c>). Unlike the sidebar's account label
    /// (<c>accountLabel</c>) nothing is trimmed: this is the name as the user
    /// typed it.
    /// </summary>
    public static string AccountRowTitle(Account a)
    {
        ArgumentNullException.ThrowIfNull(a);
        return a.Config.Name.Length > 0 ? a.Config.Name : a.Config.Email;
    }

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
