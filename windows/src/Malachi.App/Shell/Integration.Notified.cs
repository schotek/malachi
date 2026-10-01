// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/Integration+Notifications.swift
// (openNotifiedMessage, openPendingNotified) and
// ui/internal/window/notify_open.go (OpenNotifiedMessage): a click on a
// new-message notification opens its message in its own window
// (App.Activate with ActivationRequest.NotifiedMessage).

using Malachi.Core.Api;
using Malachi.Core.Controllers;

namespace Malachi.App.Shell;

/// <summary>The clicked notifications of the Integration.</summary>
public sealed partial class Integration
{
    // The message of a notification clicked before the connection to the
    // daemon was up; the latest click replaces an earlier one.
    private (AccountId Account, MessageId Message)? notifiedPending;

    /// <summary>
    /// Opens message <paramref name="id"/> of account
    /// <paramref name="account"/>, whose desktop notification the user
    /// clicked, in its own window (or raises the window that already shows
    /// it) and marks it read, as a double-click in the list does. The main
    /// window stays as it is: hidden when it runs in the background, behind
    /// the message window otherwise. The message need not be in any list the
    /// window shows; what the cache does not know, <c>message.get</c> tells.
    /// A message the daemon no longer has shows the main window instead, as
    /// every click did before. A click that started the app comes before
    /// the connection: the message waits for it, and the main window shows
    /// meanwhile (with the banner, should the daemon not come).
    /// </summary>
    public void OpenNotifiedMessage(AccountId account, MessageId id)
    {
        Mailbox.WithdrawNotifications([id]);
        if (state.Notifications.ConnectionState is not ConnectionState.Connected)
        {
            notifiedPending = (account, id);
            state.ShowMainWindow();
            return;
        }
        if (Actions.Summary(id) is { } known)
        {
            OpenNotified(known);
            return;
        }
        Cache.LookUp(account, id, s =>
        {
            if (s is null)
            {
                state.ShowMainWindow();
                return;
            }
            OpenNotified(s);
        });
    }

    // The message of a notification clicked before the connection was up,
    // once it is (WireConnection).
    private void OpenPendingNotified()
    {
        if (notifiedPending is not { } p)
        {
            return;
        }
        notifiedPending = null;
        OpenNotifiedMessage(p.Account, p.Message);
    }

    // Opens the window of message s and marks it read (SetSeen leaves a read
    // or outbox message alone).
    private void OpenNotified(MessageSummary s)
    {
        if (Reader is not { } reader)
        {
            state.ShowMainWindow();
            return;
        }
        reader.Services.Windows.OpenMessage(s);
        Actions.MarkRead(s.Id);
    }
}
