// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/Outbox.swift (OutboxTracker); GTK:
// ui/internal/window/outbox.go (trackOutbox, the outboxSeen and
// outboxCancelled maps, cancelSendFrom). A class where Swift has a mutable
// struct: the sync controller owns the one instance and takes no snapshot.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;

namespace Malachi.Core.Model;

/// <summary>
/// The bookkeeping of outbox.go <c>trackOutbox</c>: after every
/// <c>folder.list</c> of an account, a shrink of the outbox folder that the
/// user did not cause by cancelling means messages were delivered and their
/// copy filed in Sent (the daemon removes an outbox message only then, or
/// right after delivery when the account has no Sent folder; a failed send
/// keeps it). Those get a toast. A cancel is used up only by a shrink it
/// explains: a reload that lands between counting it and the daemon's delete
/// leaves it for the reload that sees the drop. UI-thread-affine.
/// </summary>
public sealed class OutboxTracker
{
    // The outbox total last seen per account.
    private readonly Dictionary<AccountId, int> seen = [];

    // Removals the user asked for since the last look, not deliveries.
    private readonly Dictionary<AccountId, int> cancelled = [];

    /// <summary>
    /// Records the outbox folder's current <paramref name="total"/> (0 when
    /// the account has no outbox folder) and returns how many messages were
    /// delivered since the last call, 0 on the first look or when nothing
    /// left.
    /// </summary>
    public int Track(AccountId acc, int total)
    {
        var known = seen.TryGetValue(acc, out var previous);
        seen[acc] = total;
        if (!known)
        {
            return 0;
        }
        var shrink = Math.Max(previous - total, 0);
        var notes = cancelled.GetValueOrDefault(acc);
        var explained = Math.Min(notes, shrink);
        cancelled[acc] = notes - explained;
        return shrink - explained;
    }

    /// <summary>
    /// Notes that the user removed one outbox message of
    /// <paramref name="acc"/> (cancel sending), so the next shrink is not
    /// counted as a delivery.
    /// </summary>
    public void NoteCancelled(AccountId acc) => cancelled[acc] = cancelled.GetValueOrDefault(acc) + 1;

    /// <summary>
    /// Takes one <see cref="NoteCancelled"/> of <paramref name="acc"/> back:
    /// the daemon refused the removal (outbox.go <c>cancelSendFrom</c>).
    /// Never below zero, since a folder reload in between may have used the
    /// note up already.
    /// </summary>
    public void NoteCancelFailed(AccountId acc)
    {
        if (cancelled.TryGetValue(acc, out var n) && n > 0)
        {
            cancelled[acc] = n - 1;
        }
    }
}
