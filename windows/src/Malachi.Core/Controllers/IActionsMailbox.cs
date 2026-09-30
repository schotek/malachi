// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The part of macos/Sources/MalachiCore/Controllers/MailboxController.swift
// that ActionsController.swift uses (model, perform, adjustCounts,
// moveCounts, noteOutboxCancelled, noteOutboxCancelFailed, onOutboxChanged,
// withdrawNotifications); GTK: the Window fields and methods actions.go and
// outbox.go reach (model, client, updateFolderRow, outboxCancelled,
// onOutboxChanged, withdrawNotifications).
//
// Swift's ActionsController holds the MailboxController itself. The C#
// controllers of the main window are written side by side, so the actions
// name the members they use here, and the mailbox controller implements
// them; nothing else stands between the two.

using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Model;
using Malachi.Core.Transport;

namespace Malachi.Core.Controllers;

/// <summary>
/// The folder half of the main window as <see cref="ActionsController"/>
/// sees it: the model, the RPC plumbing it shares, the counts and badges,
/// the outbox bookkeeping. UI-thread-affine.
/// </summary>
public interface IActionsMailbox
{
    /// <summary>The window's view model (the rows' summaries, the folders and their counts).</summary>
    MailModel Model { get; }

    /// <summary>
    /// The scope the mailbox and its halves share: the actions' calls end,
    /// and their outcomes are dropped, when the mailbox closes (Swift
    /// <c>mailbox.perform</c>).
    /// </summary>
    ControllerScope Scope { get; }

    /// <summary>The transport.</summary>
    RpcClient Client { get; }

    /// <summary>
    /// Changes the cached unread and total counts of a folder and refreshes
    /// the badges and the title (the bookkeeping of actions.go
    /// <c>setSeenIDs</c>).
    /// </summary>
    void AdjustCounts(FolderKey k, int dUnread, int dTotal);

    /// <summary>
    /// Shifts the cached counts for messages leaving <paramref name="src"/>
    /// for <paramref name="target"/> (null: leaving the store;
    /// <see cref="MailModel.MoveCounts"/>) and refreshes the badges and the
    /// title (actions.go <c>trackMoves</c>' shift).
    /// </summary>
    void MoveCounts(FolderKey src, FolderKey? target, int unread, int n);

    /// <summary>
    /// Notes that the user removed one outbox message of
    /// <paramref name="acc"/>, so the next shrink of the outbox is not
    /// toasted as a delivery (outbox.go <c>cancelSendFrom</c>).
    /// </summary>
    void NoteOutboxCancelled(AccountId acc);

    /// <summary>Takes a <see cref="NoteOutboxCancelled"/> back after the daemon refused the removal.</summary>
    void NoteOutboxCancelFailed(AccountId acc);

    /// <summary>
    /// The account's outbox changed (folders.go <c>onOutboxChanged</c>): the
    /// folders are reloaded and the views showing the outbox refreshed.
    /// </summary>
    void OnOutboxChanged(AccountId acc);

    /// <summary>
    /// The messages were read, moved or trashed here: their desktop
    /// notifications, if the app still shows any, are withdrawn (notify.go
    /// <c>withdrawNotifications</c>).
    /// </summary>
    void WithdrawNotifications(IReadOnlyList<MessageId> ids);
}
