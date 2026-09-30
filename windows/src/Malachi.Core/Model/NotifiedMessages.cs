// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/NotifiedMessages.swift
// (notifiedMax, notificationID, NotifiedEntry, NotifiedMessages,
// notificationOutdated); GTK: ui/internal/window/notified.go (notifiedMax,
// notificationID, notifiedSet, notifiedEntry, notificationOutdated). The
// messages the app sent a desktop notification for that may still show in
// the notification centre, so that it withdraws those and only those once
// they are outdated (MailboxController.Notifications.cs). Windows-only, the
// toast's tag is notificationID made to fit a tag
// (DesktopNotification.TagOf).

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Transport;

namespace Malachi.Core.Model;

/// <summary>
/// The messages the app sent a desktop notification for that may still
/// show, oldest first, each with the folder it was notified in (notified.go
/// <c>notifiedSet</c>). Only messages in it are ever withdrawn, so the app
/// never withdraws blindly.
/// </summary>
public sealed class NotifiedMessages
{
    /// <summary>
    /// notified.go <c>notifiedMax</c>: how many notifications the app
    /// remembers. The oldest one is withdrawn when a newer one pushes it out:
    /// once forgotten it could never be withdrawn again.
    /// </summary>
    public const int Max = 50;

    private readonly List<NotifiedEntry> entries = [];

    /// <summary>The entries, oldest first.</summary>
    public IReadOnlyList<NotifiedEntry> Entries => entries;

    /// <summary>notified.go <c>notificationID</c>: the notification's id, <c>message-&lt;id&gt;</c>.</summary>
    public static string NotificationId(MessageId id) => "message-" + id.Value;

    /// <summary>
    /// Records the notification of message <paramref name="id"/> in folder
    /// <paramref name="k"/> as the newest; a message notified twice keeps one
    /// entry. Returns the messages pushed out beyond <see cref="Max"/>,
    /// oldest first, for the caller to withdraw.
    /// </summary>
    public IReadOnlyList<MessageId> Add(MessageId id, FolderKey k)
    {
        Drop(e => e.Id == id);
        entries.Add(new NotifiedEntry(id, k));
        var over = entries.Count - Max;
        if (over <= 0)
        {
            return [];
        }
        MessageId[] evicted = [.. entries.Take(over).Select(e => e.Id)];
        entries.RemoveRange(0, over);
        return evicted;
    }

    /// <summary>Forgets the given messages and returns those it held.</summary>
    public IReadOnlyList<MessageId> Remove(IReadOnlyCollection<MessageId> ids)
    {
        ArgumentNullException.ThrowIfNull(ids);
        if (entries.Count == 0)
        {
            return [];
        }
        var want = ids.ToHashSet();
        return Drop(e => want.Contains(e.Id));
    }

    /// <summary>Forgets the messages notified in folder <paramref name="k"/> and returns them.</summary>
    public IReadOnlyList<MessageId> RemoveFolder(FolderKey k) => Drop(e => e.Key == k);

    /// <summary>Forgets the messages of every account not in <paramref name="keep"/> and returns them.</summary>
    public IReadOnlyList<MessageId> RemoveAccountsExcept(IReadOnlySet<AccountId> keep)
    {
        ArgumentNullException.ThrowIfNull(keep);
        return Drop(e => !keep.Contains(e.Key.Account));
    }

    /// <summary>The entries of account <paramref name="acc"/>, oldest first.</summary>
    public IReadOnlyList<NotifiedEntry> OfAccount(AccountId acc) => [.. entries.Where(e => e.Key.Account == acc)];

    /// <summary>
    /// notified.go <c>notificationOutdated</c>: reads message.get's answer
    /// about a notified message (<paramref name="message"/> on success,
    /// <paramref name="error"/> otherwise). True when the message was read or
    /// has left the folder it was notified in, or when the daemon no longer
    /// has the message or its account; false while it is still unread where
    /// it arrived; null when the answer tells nothing (no connection, a
    /// timeout, a storage error), and the notification then stays.
    /// </summary>
    public static bool? Outdated(NotifiedEntry e, MessageSummary? message, Exception? error)
    {
        if (message is { } m)
        {
            var moved = !string.IsNullOrEmpty(m.FolderId.Value) && m.FolderId != e.Key.Folder;
            return moved || FolderTree.HasFlag(m.Flags, Flag.Seen);
        }
        if (error is RpcException { Code.Value: ErrorCode.MessageNotFound or ErrorCode.AccountNotFound })
        {
            return true;
        }
        return null;
    }

    // Removes the entries match selects and returns their messages, in order.
    private List<MessageId> Drop(Func<NotifiedEntry, bool> match)
    {
        var removed = new List<MessageId>();
        entries.RemoveAll(e =>
        {
            if (!match(e))
            {
                return false;
            }
            removed.Add(e.Id);
            return true;
        });
        return removed;
    }
}
