// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The share of macos/Sources/MalachiCore/Controllers/SyncController.swift
// that MailboxController.swift uses (its `sync` property: apply,
// loadSyncStatus, refreshFooter, setConnection, beginChecking,
// showAuthRequired, hideAuthBanner, close, and the accounts / folderName
// closures it installs); GTK: ui/internal/window/sync.go (applySyncState's
// first half, loadSyncStatus, refreshSyncLabel, startSync's line,
// showAuthRequired, hideAuthBanner). Swift hands the mailbox the class
// itself; here the mailbox takes this seam, which SyncController
// implements, so the mailbox's tests can watch what it asks of the status
// line.

using System;
using System.Collections.Generic;
using System.Diagnostics.CodeAnalysis;
using Malachi.Core.Api;
using Malachi.Core.Transport;

namespace Malachi.Core.Controllers;

/// <summary>
/// The status line, the sign-in banner and the account states as the
/// mailbox drives them (SyncController). Called on the UI thread only.
/// </summary>
public interface IMailboxSync
{
    /// <summary>
    /// The accounts the line counts and the popover lists, in account.list
    /// order; the mailbox supplies its model's (Swift <c>accounts</c>).
    /// </summary>
    Func<IReadOnlyList<Account>> Accounts { get; set; }

    /// <summary>
    /// The display name of a folder, "" when unknown (Swift
    /// <c>folderName</c>; sync.go <c>refreshSyncLabel</c>'s lookup).
    /// </summary>
    Func<AccountId, FolderId, string> FolderName { get; set; }

    /// <summary>
    /// Records one account's state and refreshes the line (the first half of
    /// sync.go <c>applySyncState</c>); returns the state it replaced (null
    /// for the first) and the new one.
    /// </summary>
    (SyncState? Prev, SyncState Cur) Apply(SyncState s);

    /// <summary>
    /// Runs <c>sync.status</c> and hands every state to <paramref name="each"/>,
    /// or to <see cref="Apply"/> without one (sync.go <c>loadSyncStatus</c>).
    /// </summary>
    [SuppressMessage("Naming", "CA1716", Justification = "Swift's name, which SyncController keeps; no Visual Basic code implements this interface.")]
    void LoadSyncStatus(RpcClient client, Action<SyncState>? each = null);

    /// <summary>
    /// Recomputes the line from the cached states and the connection
    /// (sync.go <c>refreshSyncLabel</c>).
    /// </summary>
    void RefreshFooter();

    /// <summary>
    /// The connection changed (window.go <c>showConnectionState</c>): what
    /// sync.status said is forgotten with it, and the line follows.
    /// </summary>
    void SetConnection(ConnectionState state);

    /// <summary>
    /// The line of a refresh the user asked for (sync.go <c>startSync</c>):
    /// "Checking for new mail…" at once, with the fallback timer.
    /// </summary>
    void BeginChecking();

    /// <summary>Reveals the sign-in banner for the notified account (sync.go <c>showAuthRequired</c>).</summary>
    void ShowAuthRequired(AuthRequiredNotification n, Account? account);

    /// <summary>Hides the sign-in banner and forgets its account (sync.go <c>hideAuthBanner</c>).</summary>
    void HideAuthBanner();

    /// <summary>Stops the timers; nothing is emitted afterwards.</summary>
    void Close();
}
