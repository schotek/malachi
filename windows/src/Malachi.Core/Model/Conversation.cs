// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/Conversation.swift; GTK:
// ui/internal/conversation/conversation.go (IsConversationRow, Build, Merge,
// Remove, CardActions, assemble, truncatedItem, memberItem, markRead, mine,
// sender, sortedUnique, before).
//
// The view logic of a whole conversation in the reading pane. Selecting a
// folded conversation row of the grouped list (two or more members in the
// folder; a Jira folder is always grouped) shows every member the folder
// holds with full bodies, instead of only the newest one; a member row and a
// single-message row keep the single-message view. This turns the answer of
// a folder-scoped thread.get (the summary and the members, oldest first, at
// most MaxThreadMessages, the newest) into the items the pane stacks: a card
// per message (a mail message, or the description or a comment of an issue,
// with the Jira badges), a compact row per status or assignee change of an
// issue, and a row that says how many older members are left out. It also
// picks the one member opening the conversation marks read (the newest that
// is not an event) and the item the pane scrolls to (the newest), and keeps
// the items in step when a member arrives or goes while the conversation is
// shown. The model is oldest first; the order the pane shows is
// ConversationLayout's.
//
// The pane stacks native cards, each body in its own locked view, never one
// composed document: a message's CSS could restyle or forge the headers of
// the others. Every string here that comes from a message is
// attacker-controlled. Go's types are ConversationItem, ConversationItemKind
// and ConversationModel; Go's zero Account (a conversation whose account is
// not known) is null, which has the mail default.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.IssueTrackers;

namespace Malachi.Core.Model;

/// <summary>
/// The conversation package: a namespace, so the Go names map 1:1
/// (<c>conversation.Build</c> → <c>Conversation.Build</c>).
/// </summary>
public static class Conversation
{
    /// <summary>
    /// conversation.IsConversationRow: a listed conversation whose selection
    /// shows the whole conversation: two or more members in the folder. The
    /// outbox is never grouped (the caller's rule, as for the list).
    /// </summary>
    public static bool IsConversationRow(ThreadSummary t)
    {
        ArgumentNullException.ThrowIfNull(t);
        return t.MessageCount >= 2;
    }

    /// <summary>
    /// conversation.Build: the model of a conversation from a folder-scoped
    /// thread.get: <paramref name="thread"/> is its summary,
    /// <paramref name="members"/> its folder members, <paramref name="account"/>
    /// the account they belong to (its address tells the user's own mail,
    /// <see cref="ConversationItem.Mine"/>). The members are ordered oldest
    /// first by (date, id) whatever order they come in; a member without an
    /// id and a repeated id (the first is kept) are dropped; beyond
    /// MaxThreadMessages only the newest are kept. An event whose changes this
    /// client does not know at all is left out. Earlier counts the members of
    /// the thread's count that are not among the members. The issue card is
    /// the thread's issue, else the newest member's. No member to show makes
    /// an empty model with Earlier 0 (the conversation left the folder:
    /// nothing to load again).
    /// </summary>
    public static ConversationModel Build(ThreadSummary thread, IReadOnlyList<MessageSummary> members, Account? account = null)
    {
        ArgumentNullException.ThrowIfNull(thread);
        ArgumentNullException.ThrowIfNull(members);
        var list = SortedUnique(members);
        var shown = list.Count > API.Limits.MaxThreadMessages ? list.Skip(list.Count - API.Limits.MaxThreadMessages).ToList() : list;
        var m = new ConversationModel { Thread = thread.Id, Earlier = Math.Max(thread.MessageCount, list.Count) - shown.Count };
        var items = new List<ConversationItem>(shown.Count);
        foreach (var s in shown)
        {
            if (MemberItem(s, account) is { } it)
            {
                items.Add(it);
            }
        }
        if (items.Count == 0)
        {
            return new ConversationModel { Thread = thread.Id };
        }
        var info = thread.Issue;
        for (var i = shown.Count - 1; info is null && i >= 0; i--)
        {
            info = shown[i].Issue?.Info;
        }
        return Assemble(m, items, info is null ? null : Jira.IssueCard(info));
    }

    /// <summary>
    /// conversation.Merge: puts a member that arrived while the conversation
    /// is shown in its place by (date, id), or replaces the shown member with
    /// the same id (its flags, delivery state or issue changed) and moves it
    /// if its date changed. A message without an id, or of another
    /// conversation (a thread id that differs), leaves the model as it is;
    /// which folder it is in is the caller's check, as for the list. A member
    /// with an issue refreshes the issue card (an event has changed the
    /// status). MarkRead and ScrollTo follow the rules of Build; the model
    /// given is not modified.
    /// </summary>
    public static ConversationModel Merge(ConversationModel m, MessageSummary arrived, Account? account = null)
    {
        ArgumentNullException.ThrowIfNull(m);
        ArgumentNullException.ThrowIfNull(arrived);
        if (arrived.Id.Value.Length == 0 || (m.Thread.Value.Length > 0 && arrived.ThreadId is { } t && t.Value.Length > 0 && t != m.Thread))
        {
            return m;
        }
        var items = m.Items.Where(it => it.Kind != ConversationItemKind.Truncated && it.Message!.Id != arrived.Id).ToList();
        if (MemberItem(arrived, account) is { } item)
        {
            var at = items.FindIndex(x => Before(arrived, x.Message!));
            items.Insert(at < 0 ? items.Count : at, item);
        }
        var card = arrived.Issue is { } issue ? Jira.IssueCard(issue.Info) : m.Issue;
        return Assemble(m, items, card);
    }

    /// <summary>
    /// conversation.Remove: drops the member <paramref name="id"/> (it was
    /// moved, deleted or left the folder). An id that is not shown leaves the
    /// model as it is. When the last shown member goes, the model becomes
    /// empty and keeps Earlier: a conversation with older members is loaded
    /// again. MarkRead and ScrollTo follow the rules of Build; the model given
    /// is not modified.
    /// </summary>
    public static ConversationModel Remove(ConversationModel m, MessageId? id)
    {
        ArgumentNullException.ThrowIfNull(m);
        var at = m.Index(id);
        if (at < 0)
        {
            return m;
        }
        var items = m.Items.Where((_, i) => i != at).ToList();
        if (!items.Any(it => it.Kind != ConversationItemKind.Truncated))
        {
            return new ConversationModel { Thread = m.Thread, Earlier = m.Earlier };
        }
        return m with { Items = items, MarkRead = MarkRead(items), ScrollTo = items.Count - 1 };
    }

    /// <summary>
    /// conversation.CardActions: the buttons a card offers on hover: Reply
    /// (labelled Comment when Comment is set), Reply All and Forward, as the
    /// message toolbar would offer them for this member alone
    /// (Capabilities.Available with the member selected).
    /// <paramref name="composeAccount"/> says some enabled account can
    /// compose: the forward of an issue goes out from a mail account. An
    /// event offers none; the other actions (move, trash, archive, junk) are
    /// never set here.
    /// </summary>
    public static CapabilityActions CardActions(Account? account, MessageSummary m, bool composeAccount)
    {
        ArgumentNullException.ThrowIfNull(m);
        if (Jira.IsEvent(m.Issue))
        {
            return default;
        }
        var a = Capabilities.Available(new CapabilitySituation
        {
            Account = account,
            Selected = true,
            Outbox = m.Outbox is not null,
            ComposeAccount = composeAccount,
        });
        return new CapabilityActions { Reply = a.Reply, ReplyAll = a.ReplyAll, Forward = a.Forward, Comment = a.Comment };
    }

    // assemble: m completed from its member items, oldest first: the row of
    // older members on top when Earlier > 0, the issue card, MarkRead and
    // ScrollTo. No member items make an empty model (Thread and Earlier kept).
    private static ConversationModel Assemble(ConversationModel m, List<ConversationItem> items, JiraCard? card)
    {
        var output = new ConversationModel { Thread = m.Thread, Earlier = m.Earlier };
        if (items.Count == 0)
        {
            return output;
        }
        var all = new List<ConversationItem>(items.Count + 1);
        if (output.Earlier > 0)
        {
            all.Add(TruncatedItem(output.Earlier));
        }
        all.AddRange(items);
        return output with { Items = all, Issue = card, MarkRead = MarkRead(all), ScrollTo = all.Count - 1 };
    }

    // truncatedItem: the row that says n older members are left out.
    private static ConversationItem TruncatedItem(int n) => new()
    {
        Kind = ConversationItemKind.Truncated,
        // TRANSLATORS: at the top of a conversation in the reading pane; only its newest messages are shown.
        Text = L10n.N("%d earlier message is not shown", "%d earlier messages are not shown", n, n),
    };

    // memberItem: the item of member s of an account's conversation; null
    // for an event without a change this client knows.
    private static ConversationItem? MemberItem(MessageSummary s, Account? account)
    {
        var it = new ConversationItem { Kind = ConversationItemKind.Message, Message = s, Sender = Sender(s.From), Mine = Mine(s, account) };
        if (Jira.IsEvent(s.Issue))
        {
            var lines = Jira.EventLines(s.Issue!.Changes);
            if (lines.Count == 0)
            {
                return null;
            }
            return it with { Kind = ConversationItemKind.Event, EventLines = lines, EventText = Jira.EventText(s.Issue.Changes) };
        }
        it = it with { Unread = !s.Flags.Contains(Flag.Seen) };
        if (s.Issue is { } issue)
        {
            var c = Jira.IssueCard(issue.Info, issue);
            it = it with { Internal = c.Internal, InternalLabel = c.InternalLabel, Via = c.Via, Edited = c.Edited };
        }
        return it;
    }

    // markRead: the newest message card that is not queued in the outbox when
    // it is unread, else null.
    private static MessageId? MarkRead(List<ConversationItem> items)
    {
        for (var i = items.Count - 1; i >= 0; i--)
        {
            var it = items[i];
            if (it.Kind != ConversationItemKind.Message || it.Message!.Outbox is not null)
            {
                continue;
            }
            return it.Unread ? it.Message.Id : (MessageId?)null;
        }
        return null;
    }

    // mine: whether the user of account wrote member s (ConversationItem.Mine).
    private static bool Mine(MessageSummary s, Account? account)
    {
        if (s.Issue is { } issue)
        {
            return issue.Mine == true && string.IsNullOrEmpty(issue.Via);
        }
        if (s.From.Count == 0)
        {
            return false;
        }
        var own = (account?.Config.Email ?? "").Trim();
        var from = s.From[0].Email.Trim();
        return own.Length > 0 && from.Length > 0 && CodePoints.EqualFold(own, from);
    }

    // sender: the cleaned display name (else address) of the first address
    // that has one.
    private static string Sender(IReadOnlyList<Address> from)
    {
        foreach (var a in from)
        {
            if (Jira.Clean(a.Name) is { Length: > 0 } name)
            {
                return name;
            }
            if (Jira.Clean(a.Email) is { Length: > 0 } addr)
            {
                return addr;
            }
        }
        return "";
    }

    // sortedUnique: members without empty and repeated ids (the first kept),
    // oldest first by (date, id).
    private static List<MessageSummary> SortedUnique(IReadOnlyList<MessageSummary> members)
    {
        var seen = new HashSet<MessageId>();
        var output = members.Where(s => s.Id.Value.Length > 0 && seen.Add(s.Id)).ToList();
        return [.. output.OrderBy(s => s.Date).ThenBy(s => s.Id.Value, CodePoints.Comparer)];
    }

    // before: members oldest first: by date, then by id (thread.get's order).
    private static bool Before(MessageSummary a, MessageSummary b) =>
        a.Date != b.Date ? a.Date < b.Date : CodePoints.Compare(a.Id.Value, b.Id.Value) < 0;
}
