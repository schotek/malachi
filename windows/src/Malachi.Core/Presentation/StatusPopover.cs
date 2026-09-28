// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/window/status.go (refreshStatusPopover, the rows by
// account and the order they were built for); macOS:
// MainWindow/StatusPopoverViewController.swift (update, setDaemon), which
// keeps this in AppKit and Windows in Core (docs/windows-port.md §7.4). The
// status flyout's content: one StatusPopoverRow per account, paused ones
// included, rebuilt only when the accounts changed (sameAccounts) and
// updated in place otherwise, so a row keeps the keyboard focus while its
// account syncs; and the daemon at the foot.

using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;
using System.Linq;
using CommunityToolkit.Mvvm.ComponentModel;
using Malachi.Core.Api;
using Malachi.Core.Model;

namespace Malachi.Core.Presentation;

/// <summary>The status flyout's rows and foot.</summary>
public sealed partial class StatusPopover : ObservableObject
{
    /// <summary>An empty flyout.</summary>
    public StatusPopover()
    {
        Daemon = "";
    }

    /// <summary>The accounts' rows, in account.list order.</summary>
    public ObservableCollection<StatusPopoverRow> Rows { get; } = [];

    /// <summary>The list is shown (there is an account).</summary>
    [ObservableProperty]
    public partial bool HasRows { get; private set; }

    /// <summary>window.blp status_daemon: the daemon's version and pid, or that system.info failed; "" hides it.</summary>
    [ObservableProperty]
    public partial string Daemon { get; set; }

    /// <summary>
    /// Shows <paramref name="list"/> (status.go <c>refreshStatusPopover</c>):
    /// the rows are rebuilt when the accounts changed and updated in place
    /// otherwise; <paramref name="outbox"/> says whether an account's outbox
    /// can be shown (<see cref="MailModel.OutboxKey"/>).
    /// </summary>
    public void Update(IReadOnlyList<AccountStatus> list, Func<AccountId, bool> outbox)
    {
        ArgumentNullException.ThrowIfNull(list);
        ArgumentNullException.ThrowIfNull(outbox);
        if (!SyncStatusTexts.SameAccounts([.. Rows.Select(r => r.Account)], list))
        {
            Rows.Clear();
            foreach (var st in list)
            {
                Rows.Add(new StatusPopoverRow(st.Account));
            }
        }
        for (var i = 0; i < list.Count; i++)
        {
            Rows[i].Apply(list[i], outbox(list[i].Account));
            Rows[i].IsFirst = i == 0;
        }
        HasRows = list.Count > 0;
    }
}
