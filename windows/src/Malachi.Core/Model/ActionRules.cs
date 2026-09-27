// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/ActionRules.swift (flagChange,
// messageActionState); GTK: ui/internal/window/actions.go (flagChange,
// setMessageActionsSensitive). ActionFlags, the rest of that Swift file, has
// a file of its own.
//
// Swift's messageActionState asks the model two questions (inOutbox,
// canMoveToRole); here they are passed as the model's methods
// (MessageActionState(row, model.InOutbox, model.CanMoveToRole)), so the
// rule does not depend on the whole model.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;

namespace Malachi.Core.Model;

/// <summary>
/// The pure rules of the per-message actions: the flag pair of a toggle and
/// which actions the selected row allows.
/// </summary>
public static class ActionRules
{
    /// <summary>
    /// The <c>message.flag</c> set / clear pair that turns
    /// <paramref name="f"/> on or off (actions.go <c>flagChange</c>). The
    /// absent half is null, as the wire omits it.
    /// </summary>
    public static (IReadOnlyList<Flag>? Set, IReadOnlyList<Flag>? Clear) FlagChange(Flag f, bool on) =>
        on ? ([f], null) : (null, [f]);

    /// <summary>
    /// The actions the selected row allows (actions.go
    /// <c>setMessageActionsSensitive</c>): archive and junk only when the
    /// account has such a folder and the message is not in it already
    /// (<paramref name="canMoveToRole"/>, the model's
    /// <c>CanMoveToRole</c>), mark read / unread according to the seen flag;
    /// the star shows the flagged state. A conversation row is read when
    /// every member is, flagged when any is. An outbox message
    /// (<paramref name="inOutbox"/>, the model's <c>InOutbox</c>) keeps only
    /// reply, forward and trash (which cancels the send; the daemon refuses
    /// flags and moves). With <paramref name="on"/> false (or no row)
    /// everything is off.
    /// </summary>
    public static ActionFlags MessageActionState(
        ListRow? row, Func<MessageSummary, bool> inOutbox, Func<MessageSummary, FolderRole, bool> canMoveToRole, bool on = true)
    {
        ArgumentNullException.ThrowIfNull(inOutbox);
        ArgumentNullException.ThrowIfNull(canMoveToRole);
        if (!on || row is null)
        {
            return ActionFlags.None;
        }
        var s = row.Message;
        var outbox = inOutbox(s);
        var flagged = s.Flags.Contains(Flag.Flagged);
        var seen = s.Flags.Contains(Flag.Seen);
        var unread = !seen;
        if (row.Thread && row.Summary is { } summary)
        {
            flagged = summary.Flags.Contains(Flag.Flagged);
            unread = summary.UnreadCount > 0;
            seen = summary.UnreadCount < summary.MessageCount;
        }
        return new ActionFlags
        {
            On = true,
            Outbox = outbox,
            Flagged = flagged,
            Reply = true,
            ReplyAll = true,
            Forward = true,
            Star = !outbox,
            Trash = true,
            Archive = !outbox && canMoveToRole(s, FolderRole.Archive),
            Junk = !outbox && canMoveToRole(s, FolderRole.Junk),
            MarkRead = !outbox && unread,
            MarkUnread = !outbox && seen,
            ToggleFlag = !outbox,
            LoadImages = true,
            TrustSender = !outbox,
        };
    }
}
