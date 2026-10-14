// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Selection/Selection.swift; GTK:
// ui/internal/selection/selection.go (MaxOpen, MaxForward, Combine,
// FlagTarget, Restrict, Reconcile), text.go (SelectedText,
// CancelSendingQuestion) and ui/internal/window/selection.go
// (newestFirstIDs, Swift's MailModel.newestFirst).
//
// The message list's selection of several rows, in all three clients: what
// the window's actions allow for it (Combine), that it stays within one
// account (Restrict), what it becomes when the list changes under it
// (Reconcile), and its texts. One row is the row's own flags, unchanged:
// everything here is about two or more. A row stands for one message or,
// folded, for a conversation's members in the folder, as before. Pure
// logic, no widgets.
//
// Windows: the namespace is Selections, as Boards and ContextMenus are (a
// namespace Selection would hide the class); Go's Flags is the window's
// ActionFlags, as in Swift, with Go's ChangeStatus off for several. Go's
// Row[K] is a tuple of the key and the account, as Swift's is; Reconcile's
// keys are values (the list's ListKey), a gone key a null.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.Model;

namespace Malachi.Core.Selections;

/// <summary>ui/internal/selection: the message list's selection of several rows.</summary>
public static class Selection
{
    /// <summary>selection.MaxOpen: the most rows Open in New Window opens in windows of their own at once.</summary>
    public const int MaxOpen = 10;

    /// <summary>selection.MaxForward: the most rows Forward takes into one message (api.MaxForwardMessages).</summary>
    public const int MaxForward = API.Limits.MaxForwardMessages;

    /// <summary>
    /// selection.Combine: what the window's actions allow for the selected
    /// rows, the flags of each (<see cref="ActionRules.MessageActionState"/>;
    /// one account, a client never passes rows of two). One row is that
    /// row's flags; several:
    /// <list type="bullet">
    /// <item>Mark as Read when any row is unread, Mark as Unread when any is
    /// read; the star stars them all unless every row is starred, which
    /// unstars them all (<see cref="ActionFlags.Flagged"/>: every row
    /// starred);</item>
    /// <item>Archive and Junk when any row can go there (those already there
    /// stay); Trash, Move To and the star only when every row allows
    /// them;</item>
    /// <item>Forward forwards them all in one message, up to
    /// <see cref="MaxForward"/> rows; Open opens up to
    /// <see cref="MaxOpen"/> windows;</item>
    /// <item>Reply, Reply All, Change Status, Load Images and Trust Sender are
    /// for one message, and off;</item>
    /// <item>in the outbox Trash cancels the sending of them all, and nothing
    /// else goes.</item>
    /// </list>
    /// What the account offers (<see cref="ActionFlags.Unsupported"/>) and
    /// Comment are the first row's.
    /// </summary>
    public static ActionFlags Combine(IReadOnlyList<ActionFlags> rows)
    {
        ArgumentNullException.ThrowIfNull(rows);
        switch (rows.Count)
        {
            case 0:
                return ActionFlags.None;
            case 1:
                return rows[0] with { Count = 1, Open = rows[0].On };
        }
        var n = rows.Count;
        var outbox = true;
        var flagged = true;
        var forward = n <= MaxForward;
        var star = true;
        var toggleFlag = true;
        var trash = true;
        var move = true;
        var archive = false;
        var junk = false;
        var markRead = false;
        var markUnread = false;
        foreach (var r in rows)
        {
            outbox = outbox && r.Outbox;
            flagged = flagged && r.Flagged;
            forward = forward && r.Forward;
            star = star && r.Star;
            toggleFlag = toggleFlag && r.ToggleFlag;
            trash = trash && r.Trash;
            move = move && r.Move;
            archive = archive || r.Archive;
            junk = junk || r.Junk;
            markRead = markRead || r.MarkRead;
            markUnread = markUnread || r.MarkUnread;
        }
        if (outbox)
        {
            forward = false;
            star = false;
            toggleFlag = false;
            move = false;
            archive = false;
            junk = false;
            markRead = false;
            markUnread = false;
        }
        return new ActionFlags
        {
            Count = n,
            Open = n <= MaxOpen,
            On = true,
            Outbox = outbox,
            Flagged = flagged,
            Forward = forward,
            Star = star,
            Trash = trash,
            Archive = archive,
            Junk = junk,
            Move = move,
            MarkRead = markRead,
            MarkUnread = markUnread,
            ToggleFlag = toggleFlag,
            Comment = rows[0].Comment,
            Unsupported = rows[0].Unsupported,
        };
    }

    /// <summary>
    /// selection.FlagTarget: what the star does to the selected rows: true
    /// stars them, false unstars them (<see cref="ActionFlags.Flagged"/> of
    /// <see cref="Combine"/>: every row starred).
    /// </summary>
    public static bool FlagTarget(ActionFlags f) => !f.Flagged;

    /// <summary>
    /// selection.Restrict: keeps the selection within one account, that of
    /// <paramref name="latest"/> (the row the user added last): the rows of
    /// another account leave it. <paramref name="rows"/> are the selected
    /// rows in list order, each with the account of what it stands for; the
    /// result keeps that order. <paramref name="latest"/> not among them:
    /// nothing changes.
    /// </summary>
    public static IReadOnlyList<TKey> Restrict<TKey>(IReadOnlyList<(TKey Key, AccountId Account)> rows, TKey latest)
        where TKey : notnull
    {
        ArgumentNullException.ThrowIfNull(rows);
        var comparer = EqualityComparer<TKey>.Default;
        AccountId? account = null;
        foreach (var r in rows)
        {
            if (comparer.Equals(r.Key, latest))
            {
                account = r.Account;
                break;
            }
        }
        var output = new List<TKey>(rows.Count);
        foreach (var r in rows)
        {
            if (account is not { } a || r.Account == a)
            {
                output.Add(r.Key);
            }
        }
        return output;
    }

    /// <summary>
    /// selection.Reconcile: the selection after the list changed under it (a
    /// reload, a page, a conversation folded, rows removed): each previous
    /// key in order resolved to what the list shows it as now (itself; a
    /// member row folded away as its conversation row) or gone (null),
    /// without duplicates. When nothing is left and
    /// <paramref name="neighbour"/> is given, the row it names (the one now
    /// where the first selected row was) is selected instead; it may say
    /// none (null).
    /// </summary>
    public static IReadOnlyList<TKey> Reconcile<TKey>(IReadOnlyList<TKey> prev, Func<TKey, TKey?> resolve, Func<TKey?>? neighbour = null)
        where TKey : struct
    {
        ArgumentNullException.ThrowIfNull(prev);
        ArgumentNullException.ThrowIfNull(resolve);
        var seen = new HashSet<TKey>();
        var output = new List<TKey>();
        foreach (var k in prev)
        {
            if (resolve(k) is not { } now || !seen.Add(now))
            {
                continue;
            }
            output.Add(now);
        }
        if (output.Count == 0 && prev.Count > 0 && neighbour?.Invoke() is { } next)
        {
            output.Add(next);
        }
        return output;
    }

    /// <summary>
    /// The messages of several selected rows newest first, as the Assistant
    /// takes them (window/selection.go <c>newestFirstIDs</c>, Swift
    /// <c>MailModel.newestFirst</c>): by the date of each message the model
    /// lists, the selection's order among equal dates; one the model does
    /// not list goes last.
    /// </summary>
    public static IReadOnlyList<MessageId> NewestFirst(MailModel model, IReadOnlyList<MessageId> ids)
    {
        ArgumentNullException.ThrowIfNull(model);
        ArgumentNullException.ThrowIfNull(ids);
        var ordered = ids
            .Select((id, i) => (Id: id, Index: i, Date: model.Message(id)?.Summary.Date ?? DateTimeOffset.MinValue))
            .OrderByDescending(e => e.Date)
            .ThenBy(e => e.Index)
            .Select(e => e.Id);
        return [.. ordered];
    }

    /// <summary>selection.SelectedText: the reading pane while <paramref name="n"/> rows are selected.</summary>
    public static string SelectedText(int n) =>
        // TRANSLATORS: The reading pane while several messages are selected in the list; %d is their number.
        L10n.N("%d message selected", "%d messages selected", n);

    /// <summary>
    /// selection.CancelSendingQuestion: asked before Trash cancels the
    /// sending of <paramref name="n"/> queued messages at once (one is
    /// <c>ActionsController.CancelSend</c>'s own question).
    /// </summary>
    public static string CancelSendingQuestion(int n) =>
        // TRANSLATORS: The question before the sending of several queued messages is cancelled; %d is their number.
        L10n.N("Cancel sending %d message?", "Cancel sending %d messages?", n);
}
