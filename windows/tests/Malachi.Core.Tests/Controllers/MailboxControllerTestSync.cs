// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only test double: the status line as the mailbox tests see it.
// The Swift suites (MailboxControllerFoldersTests.swift,
// MailboxControllerListTests.swift) hand the mailbox a real SyncController
// and read its footer, line and banner; here the mailbox takes IMailboxSync,
// and this double records what the mailbox asked of it. Its footer is the
// one SyncController computes (SyncStatusTexts.SyncStatusText over the
// recorded states and the accounts the mailbox supplies, or "Checking for
// new mail…" after BeginChecking), so the footer assertions of the Swift
// tests port as they are; the status line's connection half and the
// banner's texts are SyncController's own and its tests' (the mailbox tests
// check the connection and the notification it was handed instead).

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Model;
using Malachi.Core.Transport;

namespace Malachi.Core.Tests.Controllers;

/// <summary>An <see cref="IMailboxSync"/> that records what the mailbox asked of it.</summary>
internal sealed class MailboxControllerTestSync : IMailboxSync, IDisposable
{
    private readonly ControllerScope scope;
    private readonly TimeProvider time;
    private readonly Dictionary<AccountId, SyncState> states = [];

    /// <summary>
    /// A double whose sync.status is counted in <paramref name="pending"/>,
    /// as the mailbox's calls are; created on the UI thread.
    /// </summary>
    public MailboxControllerTestSync(PendingWork pending, TimeProvider time)
    {
        scope = new ControllerScope(pending);
        this.time = time;
    }

    public Func<IReadOnlyList<Account>> Accounts { get; set; } = () => [];

    public Func<AccountId, FolderId, string> FolderName { get; set; } = (_, _) => "";

    /// <summary>The sync half of the status line (Swift <c>footer</c>).</summary>
    public (string Text, bool Spinning) Footer { get; private set; } = ("", false);

    /// <summary>The connection last set (the other half of Swift's <c>line</c>).</summary>
    public ConnectionState Connection { get; private set; } = new ConnectionState.Connecting();

    /// <summary>Whether the last sync.status failed.</summary>
    public bool SyncFailed { get; private set; }

    /// <summary>The account the sign-in banner is up for (Swift <c>authBannerAccount</c>).</summary>
    public AccountId? AuthBannerAccount { get; private set; }

    /// <summary>
    /// Every show (the notification and the account the mailbox found for
    /// it) and hide (all null) of the sign-in banner, in order (Swift's
    /// <c>onAuthBanner</c> log).
    /// </summary>
    public List<(AccountId? Account, AuthRequiredNotification? Notification, Account? Known)> Banners { get; } = [];

    /// <summary>How often a refresh the user asked for started (Swift <c>beginChecking</c>).</summary>
    public int Checking { get; private set; }

    /// <summary>How often sync.status was asked for.</summary>
    public int StatusLoads { get; private set; }

    public bool Closed { get; private set; }

    public IReadOnlyDictionary<AccountId, SyncState> States => states;

    public (SyncState? Prev, SyncState Cur) Apply(SyncState s)
    {
        states.TryGetValue(s.AccountId, out var prev);
        states[s.AccountId] = s;
        SyncFailed = false;
        RefreshFooter();
        if (s.AccountId == AuthBannerAccount && s.Status != SyncStatus.AuthRequired)
        {
            HideAuthBanner();
        }
        return (prev, s);
    }

    public void LoadSyncStatus(RpcClient client, Action<SyncState>? each = null)
    {
        StatusLoads++;
        scope.Perform(client, API.SyncStatus, new SyncStatusParams(), outcome =>
        {
            if (Closed)
            {
                return;
            }
            if (!outcome.TryGetValue(out var res, out _))
            {
                SyncFailed = true;
                RefreshFooter();
                return;
            }
            foreach (var s in res.Accounts)
            {
                if (each is not null)
                {
                    each(s);
                }
                else
                {
                    Apply(s);
                }
            }
        });
    }

    public void RefreshFooter()
    {
        if (Closed)
        {
            return;
        }
        Footer = SyncStatusTexts.SyncStatusText(states, Accounts(), FolderName, time.GetLocalNow());
    }

    public void SetConnection(ConnectionState state)
    {
        Connection = state;
        SyncFailed = false;
        RefreshFooter();
    }

    public void BeginChecking()
    {
        if (Closed)
        {
            return;
        }
        Checking++;
        Footer = ("Checking for new mail…", true);
    }

    public void ShowAuthRequired(AuthRequiredNotification n, Account? account)
    {
        AuthBannerAccount = n.AccountId;
        Banners.Add((n.AccountId, n, account));
    }

    public void HideAuthBanner()
    {
        var wasShown = AuthBannerAccount is not null;
        AuthBannerAccount = null;
        if (wasShown)
        {
            Banners.Add((null, null, null));
        }
    }

    public void Close()
    {
        Closed = true;
        scope.Close();
    }

    public void Dispose() => Close();
}
