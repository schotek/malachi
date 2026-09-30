// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Preferences/AccountRowCell.swift
// (apply: the texts, the switch, "Sign In…" and the row's sensitivity);
// GTK: ui/internal/window/accounts_page.go (newAccountRow, accountRow.apply,
// accountIcon). What one row of the Accounts page shows, computed once
// from the daemon's account; everything in it is plain text.

using System;
using Malachi.Core.Api;
using Malachi.Core.Model;

namespace Malachi.Core.Presentation;

/// <summary>One account of the Accounts page of the preferences, as the row shows it.</summary>
public sealed record AccountRow
{
    /// <summary>The account as the daemon last reported it (with the switch's confirmed state).</summary>
    public required Account Account { get; init; }

    /// <summary>The row's key.</summary>
    public AccountId Id => Account.Id;

    /// <summary>The account's name, or its address when unnamed (<see cref="AccountsPage.AccountRowTitle"/>).</summary>
    public required string Title { get; init; }

    /// <summary>
    /// The row's subtitle: the address, or a Jira account's site
    /// (<see cref="AccountsPage.AccountRowSubtitle"/>).
    /// </summary>
    public required string Subtitle { get; init; }

    /// <summary>The short status beside the switch (<see cref="AccountsPage.AccountStatusText"/>); "" hides it.</summary>
    public required string Status { get; init; }

    /// <summary>Whether the row offers "Sign In…" (an account of the browser sign-in that needs one).</summary>
    public required bool OffersSignIn { get; init; }

    /// <summary>
    /// What the Enabled switch shows: the account's state, or, while a
    /// pause or resume is in flight, the state asked for.
    /// </summary>
    public required bool Enabled { get; init; }

    /// <summary>A call about this account is in flight: the row is insensitive.</summary>
    public required bool Busy { get; init; }

    /// <summary>
    /// The GTK icon name of the account's kind (<see cref="AccountsPage.AccountIcon"/>):
    /// a Jira account's task list, else the provider's (the generic mail icon
    /// on Windows, U6).
    /// </summary>
    public required string Icon { get; init; }

    /// <summary>
    /// The row of <paramref name="account"/>; <paramref name="wanted"/> is the
    /// switch state asked for while a call is in flight (null: the
    /// account's own), <paramref name="busy"/> whether one is.
    /// </summary>
    public static AccountRow For(Account account, bool busy = false, bool? wanted = null)
    {
        ArgumentNullException.ThrowIfNull(account);
        return new AccountRow
        {
            Account = account,
            Title = AccountsPage.AccountRowTitle(account),
            Subtitle = AccountsPage.AccountRowSubtitle(account),
            Status = AccountsPage.AccountStatusText(account.State),
            OffersSignIn = AccountsPage.AccountRowOffersSignIn(account),
            Enabled = wanted ?? account.Enabled,
            Busy = busy,
            Icon = AccountsPage.AccountIcon(account),
        };
    }
}
