// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/MailboxController+Notifications.swift
// (recordNotification, withdrawNotifications, withdrawDelivered,
// withdrawViewedNotifications, withdrawAccountNotifications,
// verifyNotifications); GTK: ui/internal/window/notify.go
// (withdrawNotifications, withdraw, withdrawViewedNotifications,
// withdrawAccountNotifications, verifyNotifications and the tail of
// notifyNewMessage). Withdrawing the desktop notifications of new messages
// once they are outdated: the platform's notifier posts each one and hands
// it over here (RecordNotification), the mailbox decides which to withdraw
// and when, and OnWithdrawNotifications removes them (the app turns the
// messages into toast tags, DesktopNotification.TagOf). Log lines carry the
// method and error only.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Microsoft.Extensions.Logging;

namespace Malachi.Core.Controllers;

public sealed partial class MailboxController
{
    // The messages whose desktop notification may still show (window.go
    // notified), and the accounts whose notifications are being checked
    // with the daemon: true asks for one more check after the running one
    // (VerifyNotifications).
    private readonly NotifiedMessages notified = new();
    private readonly Dictionary<AccountId, bool> verifyingNotifications = [];

    /// <summary>
    /// Removes the desktop notifications of these messages from the
    /// notification centre (the GTK window's <c>WithdrawNotification</c>;
    /// Swift <c>onWithdrawNotifications</c>). Only messages whose
    /// notification the mailbox recorded ever reach it.
    /// </summary>
    public Action<IReadOnlyList<MessageId>>? OnWithdrawNotifications { get; set; }

    /// <summary>
    /// Whether the main window is the active window right now (the GTK
    /// window's <c>IsActive</c>; Swift <c>isMainWindowKey</c>); without it no
    /// folder counts as viewed.
    /// </summary>
    public Func<bool>? IsMainWindowActive { get; set; }

    /// <summary>
    /// A notification for <paramref name="n"/> was shown (the tail of
    /// notify.go <c>notifyNewMessage</c>): it is remembered, and one pushed
    /// out beyond <see cref="NotifiedMessages.Max"/> is withdrawn.
    /// </summary>
    public void RecordNotification(NewMessageNotification n)
    {
        ArgumentNullException.ThrowIfNull(n);
        Scope.VerifyAccess();
        WithdrawDelivered(notified.Add(n.Message.Id, new FolderKey(n.AccountId, n.FolderId)));
    }

    /// <summary>
    /// Withdraws the notifications of the given messages that the app showed
    /// and still remembers; the others are left alone (notify.go
    /// <c>withdrawNotifications</c>). Called when the actions read, moved or
    /// trashed them and when the daemon says they are outdated
    /// (<see cref="VerifyNotifications"/>).
    /// </summary>
    public void WithdrawNotifications(IReadOnlyList<MessageId> ids)
    {
        ArgumentNullException.ThrowIfNull(ids);
        Scope.VerifyAccess();
        WithdrawDelivered(notified.Remove(ids));
    }

    /// <summary>
    /// Withdraws the notifications of the selected folder's messages while
    /// the main window is active (it became active, or the folder was
    /// selected in it) (notify.go <c>withdrawViewedNotifications</c>): the
    /// folder's list shows those messages now, so their notifications have
    /// nothing left to announce. Notifications of other folders stay until
    /// their folder is viewed or their message is read, moved or deleted.
    /// </summary>
    public void WithdrawViewedNotifications()
    {
        Scope.VerifyAccess();
        if (Model.Selected is not { } sel || !(IsMainWindowActive?.Invoke() ?? false))
        {
            return;
        }
        WithdrawDelivered(notified.RemoveFolder(sel));
    }

    // notify.go withdrawAccountNotifications: after account.list, the
    // notifications of accounts that are gone or paused go; their messages
    // are shown nowhere, and no sync pass of theirs will check them.
    private void WithdrawAccountNotifications() =>
        WithdrawDelivered(notified.RemoveAccountsExcept(Model.EnabledAccounts.Select(a => a.Id).ToHashSet()));

    // notify.go withdraw: removes the notifications of messages the set has
    // just let go of.
    private void WithdrawDelivered(IReadOnlyList<MessageId> ids)
    {
        if (ids.Count > 0)
        {
            OnWithdrawNotifications?.Invoke(ids);
        }
    }

    // notify.go verifyNotifications: after a sync pass of account acc, asks
    // the daemon (message.get) about every message of the account whose
    // notification may still show, one after another, and withdraws the
    // outdated ones (NotifiedMessages.Outdated): read, moved or deleted on
    // another device, by another client of the daemon or by a server rule.
    // The pass is how such a change reaches the daemon, so its end is when
    // to ask. An answer that tells nothing ends the check (the next pass
    // asks again). One check per account runs at a time; a pass that ends
    // meanwhile gets one more check once it is done.
    private void VerifyNotifications(AccountId acc)
    {
        var entries = notified.OfAccount(acc);
        if (entries.Count == 0)
        {
            return;
        }
        if (verifyingNotifications.ContainsKey(acc))
        {
            verifyingNotifications[acc] = true;
            return;
        }
        verifyingNotifications[acc] = false;
        var outdated = new List<MessageId>();
        void Done()
        {
            var again = verifyingNotifications.Remove(acc, out var more) && more;
            WithdrawNotifications(outdated);
            if (again)
            {
                VerifyNotifications(acc);
            }
        }
        void Ask(int i)
        {
            if (i == entries.Count)
            {
                Done();
                return;
            }
            var e = entries[i];
            Perform(API.MessageGet, new MessageGetParams { AccountId = acc, MessageId = e.Id }, outcome =>
            {
                var answered = outcome.TryGetValue(out var res, out var err);
                if (NotifiedMessages.Outdated(e, answered ? res.Message.Summary : null, err) is not { } stale)
                {
                    LogNotificationCheckFailed(logger, API.MessageGet.Name, err?.Message ?? "");
                    Done();
                    return;
                }
                if (stale)
                {
                    outdated.Add(e.Id);
                }
                Ask(i + 1);
            });
        }
        Ask(0);
    }

    [LoggerMessage(Level = LogLevel.Debug, Message = "{Method}: {Reason}; the notifications are checked after the next pass")]
    private static partial void LogNotificationCheckFailed(ILogger logger, string method, string reason);
}
