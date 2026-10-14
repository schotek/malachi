// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/ContextMenu/ContextMenu.swift; GTK:
// ui/internal/contextmenu/contextmenu.go (ItemID, Item, MessageState,
// MessageItems, FolderState, FolderItems, AccountItems, groups, Entry,
// Target, MoveTargets).
//
// What the right-click menus of the message list and of the sidebar offer,
// in all three clients: which items, in which groups, and which of them are
// enabled. A right click on a message selects it first, so the message menu
// acts on the selection through the window's own commands; the sidebar's
// menu acts on the folder or account clicked and leaves the selection
// alone. Pure logic, no widgets: the views map the item ids to their
// actions and build their menus from the groups (a separator between two).
// The texts are ContextMenuText.cs.
//
// Windows: Go's ItemID string is an enum here, its string (Swift's
// rawValue) is Name (the tests' notation and the menu items' automation
// ids); Swift's MessageState(flags:…) initialiser is MessageState.Of.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Model;

namespace Malachi.Core.ContextMenus;

/// <summary>ui/internal/contextmenu: a namespace of types and functions, so that the Go and Swift names map 1:1.</summary>
public static partial class ContextMenu
{
    /// <summary>contextmenu.ItemID: an item of a context menu.</summary>
    public enum ItemId
    {
        // The message menu.

        /// <summary>Opens the message in a window of its own, as a double click does ("open").</summary>
        OpenMessage,

        /// <summary><see cref="OpenMessage"/> for a message of the Drafts folder, which opens in the compose window ("edit").</summary>
        EditDraft,

        /// <summary>Reply, or Comment on an issue tracker's account (<see cref="Labels.Comment"/>).</summary>
        Reply,

        /// <summary>Reply All.</summary>
        ReplyAll,

        /// <summary>Forward.</summary>
        Forward,

        /// <summary>Mark as Read, only when it applies; a conversation with read and unread members has both.</summary>
        MarkRead,

        /// <summary>Mark as Unread, only when it applies.</summary>
        MarkUnread,

        /// <summary>Star or Unstar (<see cref="Labels.Flagged"/>).</summary>
        ToggleFlag,

        /// <summary>The submenu of an issue's transitions.</summary>
        ChangeStatus,

        /// <summary>Archive.</summary>
        Archive,

        /// <summary>The submenu of the account's folders (<see cref="MoveTargets"/>).</summary>
        MoveTo,

        /// <summary>Mark as Junk.</summary>
        Junk,

        /// <summary>Move to Trash, or Cancel Sending in the outbox (<see cref="Labels.Outbox"/>).</summary>
        Trash,

        /// <summary>The submenu of the Assistant's actions on the message.</summary>
        Assistant,

        // The sidebar's menus.

        /// <summary>Synchronises the folder clicked, or the whole account from its heading.</summary>
        CheckMail,

        /// <summary><c>folder.markRead</c>.</summary>
        MarkAllRead,

        /// <summary>
        /// <c>folder.empty</c>: Empty Trash or Empty Junk (<see cref="Labels.Junk"/>),
        /// always after a confirmation (<see cref="EmptyQuestion"/>) ("empty").
        /// </summary>
        EmptyFolder,

        /// <summary>Add to or Remove from Favourites (<see cref="Labels.Favourite"/>).</summary>
        Favourite,

        /// <summary>Expand or Collapse (<see cref="Labels.Collapsed"/>).</summary>
        Fold,
    }

    /// <summary>
    /// The ItemID string of Go (Swift's rawValue): <c>open</c>,
    /// <c>replyAll</c>, <c>empty</c>, …; "" for a value that is no item.
    /// </summary>
    public static string Name(ItemId id) => id switch
    {
        ItemId.OpenMessage => "open",
        ItemId.EditDraft => "edit",
        ItemId.Reply => "reply",
        ItemId.ReplyAll => "replyAll",
        ItemId.Forward => "forward",
        ItemId.MarkRead => "markRead",
        ItemId.MarkUnread => "markUnread",
        ItemId.ToggleFlag => "toggleFlag",
        ItemId.ChangeStatus => "changeStatus",
        ItemId.Archive => "archive",
        ItemId.MoveTo => "moveTo",
        ItemId.Junk => "junk",
        ItemId.Trash => "trash",
        ItemId.Assistant => "assistant",
        ItemId.CheckMail => "checkMail",
        ItemId.MarkAllRead => "markAllRead",
        ItemId.EmptyFolder => "empty",
        ItemId.Favourite => "favourite",
        ItemId.Fold => "fold",
        _ => "",
    };

    /// <summary>contextmenu.Item: one entry of a menu.</summary>
    /// <param name="Id">The item.</param>
    /// <param name="Enabled">Whether it can be chosen now.</param>
    public readonly record struct Item(ItemId Id, bool Enabled);

    /// <summary>
    /// contextmenu.MessageState: what the window knows of the message row
    /// clicked (by then the selected one; a folded conversation stands for
    /// its members in the folder, as for the window's other actions).
    /// </summary>
    public readonly record struct MessageState
    {
        /// <summary>A message of the Drafts folder, which opens in the compose window.</summary>
        public bool Draft { get; init; }

        /// <summary>A queued message (Trash cancels its sending).</summary>
        public bool Outbox { get; init; }

        /// <summary>The account offers Reply at all (Capabilities.Supported): an item it does not offer is left out.</summary>
        public bool OfferReply { get; init; }

        /// <summary>The account offers Reply All.</summary>
        public bool OfferReplyAll { get; init; }

        /// <summary>The account offers Forward.</summary>
        public bool OfferForward { get; init; }

        /// <summary>The account offers Archive.</summary>
        public bool OfferArchive { get; init; }

        /// <summary>The account offers Move To.</summary>
        public bool OfferMove { get; init; }

        /// <summary>The account offers Mark as Junk.</summary>
        public bool OfferJunk { get; init; }

        /// <summary>The account offers Move to Trash (the outbox always offers Trash).</summary>
        public bool OfferTrash { get; init; }

        /// <summary>Reply can be done now (the window's action rules): an offered item that cannot is disabled.</summary>
        public bool Reply { get; init; }

        /// <summary>Reply All can be done now.</summary>
        public bool ReplyAll { get; init; }

        /// <summary>Forward can be done now.</summary>
        public bool Forward { get; init; }

        /// <summary>Mark as Read applies.</summary>
        public bool MarkRead { get; init; }

        /// <summary>Mark as Unread applies.</summary>
        public bool MarkUnread { get; init; }

        /// <summary>The star can be toggled now.</summary>
        public bool ToggleFlag { get; init; }

        /// <summary>Archive can be done now.</summary>
        public bool Archive { get; init; }

        /// <summary>Move To can be done now.</summary>
        public bool Move { get; init; }

        /// <summary>Mark as Junk can be done now.</summary>
        public bool Junk { get; init; }

        /// <summary>Move to Trash (or Cancel Sending) can be done now.</summary>
        public bool Trash { get; init; }

        /// <summary>The message is an issue's and its account can change the status.</summary>
        public bool ChangeStatus { get; init; }

        /// <summary>The Assistant is shown (its submenu says what can run).</summary>
        public bool Assistant { get; init; }

        /// <summary>
        /// Swift's <c>MessageState(flags:draft:changeStatus:assistant:)</c>:
        /// the state of the window's action flags (ActionRules.MessageActionState),
        /// what the account offers being what <see cref="ActionFlags.Unsupported"/>
        /// leaves out; <paramref name="draft"/> says the message is a draft and
        /// <paramref name="assistant"/> that the Assistant is shown.
        /// </summary>
        public static MessageState Of(ActionFlags f, bool draft, bool assistant)
        {
            bool Offers(MessageActionKind kind) => (f.Unsupported & kind) == 0;
            return new MessageState
            {
                Draft = draft,
                Outbox = f.Outbox,
                OfferReply = Offers(MessageActionKind.Reply),
                OfferReplyAll = Offers(MessageActionKind.ReplyAll),
                OfferForward = Offers(MessageActionKind.Forward),
                OfferArchive = Offers(MessageActionKind.Archive),
                OfferMove = Offers(MessageActionKind.Move),
                OfferJunk = Offers(MessageActionKind.Junk),
                OfferTrash = Offers(MessageActionKind.Trash),
                Reply = f.Reply,
                ReplyAll = f.ReplyAll,
                Forward = f.Forward,
                MarkRead = f.MarkRead,
                MarkUnread = f.MarkUnread,
                ToggleFlag = f.ToggleFlag,
                Archive = f.Archive,
                Move = f.Move,
                Junk = f.Junk,
                Trash = f.Trash,
                ChangeStatus = f.ChangeStatus,
                Assistant = assistant,
            };
        }
    }

    /// <summary>
    /// contextmenu.MessageItems: the message menu in its groups: opening;
    /// answering; read, star and status; where the message goes; the
    /// Assistant. Empty groups are left out.
    /// </summary>
    public static IReadOnlyList<IReadOnlyList<Item>> MessageItems(MessageState s)
    {
        var open = new Item(s.Draft ? ItemId.EditDraft : ItemId.OpenMessage, true);
        var answer = new List<Item>();
        if (s.OfferReply)
        {
            answer.Add(new Item(ItemId.Reply, s.Reply));
        }
        if (s.OfferReplyAll)
        {
            answer.Add(new Item(ItemId.ReplyAll, s.ReplyAll));
        }
        if (s.OfferForward)
        {
            answer.Add(new Item(ItemId.Forward, s.Forward));
        }
        var mark = new List<Item>();
        if (s.MarkRead)
        {
            mark.Add(new Item(ItemId.MarkRead, true));
        }
        if (s.MarkUnread)
        {
            mark.Add(new Item(ItemId.MarkUnread, true));
        }
        mark.Add(new Item(ItemId.ToggleFlag, s.ToggleFlag));
        if (s.ChangeStatus)
        {
            mark.Add(new Item(ItemId.ChangeStatus, true));
        }
        var move = new List<Item>();
        if (s.OfferArchive)
        {
            move.Add(new Item(ItemId.Archive, s.Archive));
        }
        if (s.OfferMove)
        {
            move.Add(new Item(ItemId.MoveTo, s.Move));
        }
        if (s.OfferJunk)
        {
            move.Add(new Item(ItemId.Junk, s.Junk));
        }
        if (s.OfferTrash || s.Outbox)
        {
            move.Add(new Item(ItemId.Trash, s.Trash));
        }
        var assistant = new List<Item>();
        if (s.Assistant)
        {
            assistant.Add(new Item(ItemId.Assistant, true));
        }
        return Groups([open], answer, mark, move, assistant);
    }

    /// <summary>contextmenu.FolderState: what the sidebar knows of the folder clicked.</summary>
    public readonly record struct FolderState
    {
        /// <summary>The folder holds messages (not a mere container).</summary>
        public bool Selectable { get; init; }

        /// <summary>The folder's role.</summary>
        public FolderRole Role { get; init; }

        /// <summary>The folder's own unread count (<c>folder.list</c>), not the badge, which adds a folded folder's descendants.</summary>
        public int Unread { get; init; }

        /// <summary>The folder's own total.</summary>
        public int Total { get; init; }

        /// <summary>The account has the delete capability.</summary>
        public bool CanDelete { get; init; }

        /// <summary>The folder has subfolders to expand or collapse.</summary>
        public bool Foldable { get; init; }
    }

    /// <summary>
    /// contextmenu.FolderItems: a folder's menu: checking it; marking it read
    /// or emptying it; its place in Favourites and its folding. Empty groups
    /// are left out; nothing at all opens no menu.
    /// </summary>
    public static IReadOnlyList<IReadOnlyList<Item>> FolderItems(FolderState f)
    {
        var holds = f.Selectable && f.Role != FolderRole.Outbox;
        var check = new List<Item>();
        var act = new List<Item>();
        var place = new List<Item>();
        if (holds)
        {
            check.Add(new Item(ItemId.CheckMail, true));
            act.Add(new Item(ItemId.MarkAllRead, f.Unread > 0));
        }
        if (f.Selectable && (f.Role == FolderRole.Trash || f.Role == FolderRole.Junk) && f.CanDelete)
        {
            act.Add(new Item(ItemId.EmptyFolder, f.Total > 0));
        }
        if (f.Selectable)
        {
            place.Add(new Item(ItemId.Favourite, true));
        }
        if (f.Foldable)
        {
            place.Add(new Item(ItemId.Fold, true));
        }
        return Groups(check, act, place);
    }

    /// <summary>
    /// contextmenu.AccountItems: an account heading's menu: checking the
    /// account, and its folding when the heading folds.
    /// </summary>
    public static IReadOnlyList<IReadOnlyList<Item>> AccountItems(bool foldable)
    {
        List<Item> fold = foldable ? [new Item(ItemId.Fold, true)] : [];
        return Groups([new Item(ItemId.CheckMail, true)], fold);
    }

    // contextmenu.groups: the non-empty groups.
    private static List<IReadOnlyList<Item>> Groups(params IReadOnlyList<Item>[] gs)
    {
        var output = new List<IReadOnlyList<Item>>(gs.Length);
        foreach (var g in gs)
        {
            if (g.Count > 0)
            {
                output.Add(g);
            }
        }
        return output;
    }

    /// <summary>
    /// contextmenu.Entry: a folder of the account in the sidebar's order
    /// (its tree with nothing folded and no Favourites), with its depth in
    /// the tree.
    /// </summary>
    /// <param name="Folder">The folder; one without an id (a heading's place) is left out.</param>
    /// <param name="Depth">Its depth in the tree.</param>
    public readonly record struct Entry(Folder Folder, int Depth);

    /// <summary>contextmenu.Target: an entry of the Move To submenu.</summary>
    /// <param name="Folder">The folder.</param>
    /// <param name="Depth">Its depth in the tree.</param>
    /// <param name="Enabled">
    /// A message can go there (a selectable folder other than the current
    /// one); a container stays as a disabled heading over its subfolders.
    /// </param>
    public readonly record struct Target(Folder Folder, int Depth, bool Enabled);

    /// <summary>
    /// contextmenu.MoveTargets: the Move To submenu of a message in folder
    /// <paramref name="current"/>, from its account's folders in the
    /// sidebar's order: everything but the outbox, the Drafts, an \All
    /// folder (role all) and an issue tracker's views (and entries without
    /// a folder, such as headings); the current folder and the containers
    /// disabled, and a container left out when none of its subfolders is
    /// listed.
    /// </summary>
    public static IReadOnlyList<Target> MoveTargets(IReadOnlyList<Entry> entries, FolderId current)
    {
        ArgumentNullException.ThrowIfNull(entries);
        var kept = new List<Target>();
        foreach (var e in entries)
        {
            var f = e.Folder;
            if (f is null || string.IsNullOrEmpty(f.Id.Value) || !string.IsNullOrEmpty(f.Virtual?.Value))
            {
                continue;
            }
            if (f.Role == FolderRole.Outbox || f.Role == FolderRole.Drafts || f.Role == FolderRole.All)
            {
                continue;
            }
            kept.Add(new Target(f, e.Depth, f.Selectable && f.Id != current));
        }
        // From the end: a container stays only when the next entry kept
        // after it is deeper, one of its subfolders.
        var output = new List<Target>(kept.Count);
        var next = -1;
        for (var i = kept.Count - 1; i >= 0; i--)
        {
            var t = kept[i];
            if (!t.Folder.Selectable && next <= t.Depth)
            {
                continue;
            }
            output.Add(t);
            next = t.Depth;
        }
        output.Reverse();
        return output;
    }
}
