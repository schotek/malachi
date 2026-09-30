// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/ActionRules.swift (flagChange,
// messageActionState, idleActionState, unsupportedActions); GTK:
// ui/internal/window/actions.go (flagChange) and action_rules.go
// (messageActionState, idleActionState, canMoveToRole). ActionFlags and
// MessageActionKind, the rest of that Swift file, have files of their own.
//
// The pure rules of the per-message actions: the flag pair of a toggle and
// which actions the selected row allows, as far as its account offers them
// (Capabilities: a Jira account has no Reply, Forward or Trash of its own,
// comments instead of replying and forwards from a mail account). The rule
// reads the model, as Swift's does: the row's account, every account (one
// to forward from), the listed folder and the role folders.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.IssueTrackers;

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
    /// The actions the selected row allows (action_rules.go
    /// <c>messageActionState</c>): archive and junk only when the account has
    /// such a folder and the message is not in it already, mark read /
    /// unread according to the seen flag; the star shows the flagged state. A
    /// conversation row is read when every member is, flagged when any is. An
    /// outbox message keeps only reply, forward and trash (which cancels the
    /// send; the daemon refuses flags and moves). Reply, Reply All, Forward,
    /// Trash, Archive and Junk need the account's capabilities as well
    /// (Capabilities.Available); what the account lacks altogether is
    /// Unsupported. With <paramref name="on"/> false (or no row) everything is
    /// off, and Unsupported and Comment are those of the listed folder's
    /// account, so that the toolbar of a Jira folder does not show Reply
    /// before a row is chosen.
    /// </summary>
    public static ActionFlags MessageActionState(ListRow? row, MailModel model, bool on = true)
    {
        ArgumentNullException.ThrowIfNull(model);
        if (!on || row is null)
        {
            return IdleActionState(model);
        }
        var s = row.Message;
        var outbox = model.InOutbox(s);
        var flagged = s.Flags.Contains(Flag.Flagged);
        var seen = s.Flags.Contains(Flag.Seen);
        var unread = !seen;
        if (row.Thread && row.Summary is { } summary)
        {
            flagged = summary.Flags.Contains(Flag.Flagged);
            unread = summary.UnreadCount > 0;
            seen = summary.UnreadCount < summary.MessageCount;
        }
        var account = model.Account(s.AccountId);
        var situation = new CapabilitySituation
        {
            Account = account,
            Selected = true,
            Outbox = outbox,
            Archive = model.CanMoveToRole(s, FolderRole.Archive),
            Junk = model.CanMoveToRole(s, FolderRole.Junk),
            ComposeAccount = Capabilities.ForwardAccounts(model.Accounts).Count > 0,
        };
        var supported = Capabilities.Supported(situation);
        var available = Capabilities.Available(situation);
        return new ActionFlags
        {
            On = true,
            Outbox = outbox,
            Flagged = flagged,
            Reply = available.Reply,
            ReplyAll = available.ReplyAll,
            Forward = available.Forward,
            Star = !outbox,
            Trash = available.Trash,
            Archive = available.Archive,
            Junk = available.Junk,
            MarkRead = !outbox && unread,
            MarkUnread = !outbox && seen,
            ToggleFlag = !outbox,
            LoadImages = true,
            TrustSender = !outbox,
            Comment = supported.Comment,
            ChangeStatus = s.Issue is not null && account is not null && Jira.CanTransition(account),
            Unsupported = UnsupportedActions(supported),
        };
    }

    // idleActionState: the flags with nothing selected: every action off, and
    // what the listed folder's account offers at all (none listed: the mail
    // default).
    private static ActionFlags IdleActionState(MailModel model)
    {
        var k = model.ListFolder;
        var situation = new CapabilitySituation
        {
            Account = k is { } key ? model.Account(key.Account) : null,
            Outbox = k is { } folder && folder.Folder.Value.Length > 0 && model.FolderRole(folder) == FolderRole.Outbox,
            ComposeAccount = Capabilities.ForwardAccounts(model.Accounts).Count > 0,
        };
        var supported = Capabilities.Supported(situation);
        return new ActionFlags { Comment = supported.Comment, Unsupported = UnsupportedActions(supported) };
    }

    // unsupportedActions: the actions supported leaves out.
    private static MessageActionKind UnsupportedActions(CapabilityActions supported)
    {
        var output = MessageActionKind.None;
        foreach (var (kind, offered) in new[]
        {
            (MessageActionKind.Reply, supported.Reply), (MessageActionKind.ReplyAll, supported.ReplyAll),
            (MessageActionKind.Forward, supported.Forward), (MessageActionKind.Trash, supported.Trash),
            (MessageActionKind.Move, supported.Move), (MessageActionKind.Archive, supported.Archive),
            (MessageActionKind.Junk, supported.Junk),
        })
        {
            if (!offered)
            {
                output |= kind;
            }
        }
        return output;
    }
}
