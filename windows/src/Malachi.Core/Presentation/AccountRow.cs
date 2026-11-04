// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Preferences/AccountRowCell.swift
// (apply: the texts, the switch, "Sign In…" and the row's sensitivity);
// GTK: ui/internal/window/accounts_page.go (newAccountRow, accountRow.apply,
// accountIcon) and ui/internal/integrations/integrations.go (Row, RowFor).
// What one row of the Accounts or the Integrations page shows, computed
// once from the daemon's account; everything in it is plain text.

using System;
using Malachi.Core.Api;
using Malachi.Core.Model;

namespace Malachi.Core.Presentation;

/// <summary>One account of the Accounts or the Integrations page of the preferences, as the row shows it.</summary>
public sealed record AccountRow
{
    /// <summary>The account as the daemon last reported it (with the switch's confirmed state).</summary>
    public required Account Account { get; init; }

    /// <summary>The row's key.</summary>
    public AccountId Id => Account.Id;

    /// <summary>The account's name, else a Jira site's host, else its address (<see cref="Integrations.Row.Title"/>).</summary>
    public required string Title { get; init; }

    /// <summary>
    /// The row's subtitle: the address, or the host of a Jira site or a
    /// CalDAV server (<see cref="Integrations.Row.Subtitle"/>).
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
    /// a Jira account's task list, a CalDAV account's calendar, else the
    /// provider's (the generic mail icon on Windows, U6).
    /// </summary>
    public required string Icon { get; init; }

    /// <summary>Whether the row has the Enabled switch (not a CalDAV account's, which has no pause).</summary>
    public required bool OffersSwitch { get; init; }

    /// <summary>What its Edit button opens; <see cref="Integrations.Editor.None"/> shows none.</summary>
    public required Integrations.Editor Editor { get; init; }

    /// <summary>The Edit button's tooltip and name (Edit Account, a CalDAV account's Calendar Settings).</summary>
    public required string EditLabel { get; init; }

    /// <summary>The body of the remove question, by kind (<see cref="Integrations.Row.RemoveBody"/>).</summary>
    public required string RemoveBody { get; init; }

    /// <summary>
    /// The row of <paramref name="account"/>; <paramref name="wanted"/> is the
    /// switch state asked for while a call is in flight (null: the
    /// account's own), <paramref name="busy"/> whether one is.
    /// </summary>
    public static AccountRow For(Account account, bool busy = false, bool? wanted = null)
    {
        ArgumentNullException.ThrowIfNull(account);
        var spec = Integrations.RowFor(account);
        return new AccountRow
        {
            Account = account,
            Title = spec.Title,
            Subtitle = spec.Subtitle,
            Status = AccountsPage.AccountStatusText(account.State),
            OffersSignIn = AccountsPage.AccountRowOffersSignIn(account),
            Enabled = wanted ?? account.Enabled,
            Busy = busy,
            Icon = AccountsPage.AccountIcon(account),
            OffersSwitch = spec.OffersSwitch,
            Editor = spec.Editor,
            EditLabel = spec.EditLabel,
            RemoveBody = spec.RemoveBody,
        };
    }
}
