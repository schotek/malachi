// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/BoardPreferencesController.swift
// (defaultWindows, validWindows, setEnabled, setWindows, setTriageAccounts,
// toggleTriageAccount); GTK:
// ui/internal/boardtriage/preferences.go (DefaultWindows, ValidWindows,
// SetEnabled, SetWindows, SetTriageAccounts, triageByDefault,
// TriageAccountChecked, ToggleTriageAccount).
//
// The board's preferences Settings writes: Show the Board, Keep cases for
// (the windows of the states), Triage These Accounts. Each write is an
// Update (optimistic, the whole object sent); the checklist's rules are
// the daemon's: no account listed means every enabled mail account.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;

namespace Malachi.Core.Controllers;

public sealed partial class BoardPreferencesController
{
    /// <summary>The daemon's windows when none were set (docs/api.md §4.13 BoardWindows: 90/30/30/14 days).</summary>
    public static BoardWindows DefaultWindows { get; } = new()
    {
        Hot = BoardLimits.DefaultBoardHotDays,
        You = BoardLimits.DefaultBoardYouDays,
        Them = BoardLimits.DefaultBoardThemDays,
        Info = BoardLimits.DefaultBoardInfoDays,
    };

    /// <summary>Whether <c>board.setPreferences</c> takes <paramref name="w"/>: every window 1..<see cref="BoardLimits.MaxBoardWindowDays"/> days.</summary>
    public static bool ValidWindows(BoardWindows w)
    {
        ArgumentNullException.ThrowIfNull(w);
        return new[] { w.Hot, w.You, w.Them, w.Info }.All(d => d is >= 1 and <= BoardLimits.MaxBoardWindowDays);
    }

    /// <summary>Turns the board on or off (Show the Board), as <see cref="Update"/>.</summary>
    public void SetEnabled(bool on, Action<bool>? completion = null) =>
        Update(p => p with { Enabled = on }, completion: completion);

    /// <summary>
    /// Sets how long cases of each state stay, as <see cref="Update"/>;
    /// windows the daemon would refuse (<see cref="ValidWindows"/>) are not
    /// written: false, and <paramref name="completion"/> is not called.
    /// </summary>
    public bool SetWindows(BoardWindows windows, Action<bool>? completion = null)
    {
        ArgumentNullException.ThrowIfNull(windows);
        if (!ValidWindows(windows))
        {
            return false;
        }
        Update(p => p with { Windows = windows }, completion: completion);
        return true;
    }

    /// <summary>
    /// Sets the accounts triage may read and annotate (empty: every enabled
    /// mail account), as <see cref="Update"/>; empty ids and duplicates are
    /// left out, the order kept. Triage These Accounts builds the ids with
    /// <see cref="ToggleTriageAccount"/>.
    /// </summary>
    public void SetTriageAccounts(IReadOnlyList<AccountId> ids, Action<bool>? completion = null)
    {
        ArgumentNullException.ThrowIfNull(ids);
        var list = new List<AccountId>(ids.Count);
        foreach (var id in ids)
        {
            if (!string.IsNullOrEmpty(id.Value) && !list.Contains(id))
            {
                list.Add(id);
            }
        }
        Update(p => p with { TriageAccounts = [.. list] }, completion: completion);
    }

    /// <summary>What Triage These Accounts' subtitle says (<see cref="TriageAccountsSubtitle"/>).</summary>
    public enum TriageAccountsCoverage
    {
        /// <summary>The list names accounts and some of them are checked; the boxes say which, no subtitle.</summary>
        Some,

        /// <summary>Nothing is listed, so every enabled mail account is triaged (<see cref="Boards.Board.Text.TriageSettingsAccountsAll"/>).</summary>
        All,

        /// <summary>
        /// The list names accounts but none of them is an enabled account any
        /// more (all removed or turned off). The daemon keeps such a list,
        /// since an empty one would widen the triage to every account, so the
        /// triage reads nothing (<see cref="Boards.Board.Text.TriageSettingsAccountsNone"/>);
        /// checking an account replaces the list (<see cref="ToggleTriageAccount"/>).
        /// </summary>
        None,
    }

    /// <summary>
    /// The coverage of <paramref name="listed"/> (the preferences'
    /// TriageAccounts) over <paramref name="accounts"/> (Go
    /// <c>TriageAccountsSubtitle</c>): All only when the list is empty, None
    /// when it is not and no account is checked under it, else Some.
    /// </summary>
    public static TriageAccountsCoverage TriageAccountsSubtitle(IReadOnlyList<AccountId> listed, IReadOnlyList<Account> accounts)
    {
        ArgumentNullException.ThrowIfNull(listed);
        ArgumentNullException.ThrowIfNull(accounts);
        if (listed.Count == 0)
        {
            return TriageAccountsCoverage.All;
        }
        return accounts.Any(a => TriageAccountChecked(listed, a)) ? TriageAccountsCoverage.Some : TriageAccountsCoverage.None;
    }

    /// <summary>
    /// Whether <paramref name="account"/> is triaged under
    /// <paramref name="listed"/> (the preferences' TriageAccounts), as the
    /// daemon decides: a listed account when some are listed, else every
    /// enabled mail account. A disabled account is never triaged.
    /// </summary>
    public static bool TriageAccountChecked(IReadOnlyList<AccountId> listed, Account account)
    {
        ArgumentNullException.ThrowIfNull(listed);
        ArgumentNullException.ThrowIfNull(account);
        if (!account.Enabled)
        {
            return false;
        }
        return listed.Count == 0 ? TriageByDefault(account) : listed.Contains(account.Id);
    }

    /// <summary>
    /// <paramref name="listed"/> with account <paramref name="id"/> checked
    /// (<paramref name="on"/>) or not, for <paramref name="accounts"/>: the
    /// accounts checked now (<see cref="TriageAccountChecked"/>) with
    /// <paramref name="id"/> changed, in the order of the accounts; empty
    /// again when that is exactly every enabled mail account, so that a mail
    /// account added later is triaged as before. A disabled account listed
    /// stays listed (in its place), so that it is triaged again once enabled.
    /// Null, when no enabled account would be left checked (an empty list
    /// would mean every account) or <paramref name="id"/> is not an enabled
    /// account: the list stays as it was.
    /// </summary>
    public static IReadOnlyList<AccountId>? ToggleTriageAccount(
        IReadOnlyList<AccountId> listed, IReadOnlyList<Account> accounts, AccountId id, bool on)
    {
        ArgumentNullException.ThrowIfNull(listed);
        ArgumentNullException.ThrowIfNull(accounts);
        var found = false;
        var enabled = 0;
        var checkedIds = new List<AccountId>();
        var defaults = new List<AccountId>();
        foreach (var a in accounts)
        {
            if (!a.Enabled)
            {
                if (listed.Contains(a.Id))
                {
                    checkedIds.Add(a.Id);
                }
                continue;
            }
            var c = TriageAccountChecked(listed, a);
            if (a.Id == id)
            {
                found = true;
                c = on;
            }
            if (c)
            {
                checkedIds.Add(a.Id);
                enabled++;
            }
            if (TriageByDefault(a))
            {
                defaults.Add(a.Id);
            }
        }
        if (!found || enabled == 0)
        {
            return null;
        }
        return checkedIds.SequenceEqual(defaults) ? [] : checkedIds;
    }

    // Whether account a is triaged when the preferences name no account: an
    // enabled mail account (the daemon's triageAccounts: not an issue
    // tracker).
    private static bool TriageByDefault(Account a) => a.Enabled && a.Config.ProtocolKind.Value != AccountKind.Jira;
}
