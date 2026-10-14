// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/ContextMenuTests.swift, the
// counterpart of ui/internal/contextmenu/contextmenu_test.go
// (TestMessageItems, TestFolderItems, TestMoveTargets, TestLabels,
// TestEmptyQuestion, TestProgressTexts), with Swift's messageStateOfFlags.
// Go passes a translator that returns the msgids; the process-wide
// catalogue is English here, so every msgid is its own translation. The
// groups are written as in Go ("id id- | id", "-" after a disabled item),
// with Go's ids (ContextMenu.Name).

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Malachi.Core.Tests.Model;
using Xunit;
using static Malachi.Core.ContextMenus.ContextMenu;

namespace Malachi.Core.Tests.ContextMenus;

public sealed class ContextMenuTests
{
    // A mail message everything applies to.
    private static readonly MessageState Mail = new()
    {
        OfferReply = true,
        OfferReplyAll = true,
        OfferForward = true,
        OfferArchive = true,
        OfferMove = true,
        OfferJunk = true,
        OfferTrash = true,
        Reply = true,
        ReplyAll = true,
        Forward = true,
        MarkUnread = true,
        ToggleFlag = true,
        Archive = true,
        Move = true,
        Junk = true,
        Trash = true,
        Assistant = true,
    };

    // A queued message: replies, forwards and Cancel Sending.
    private static readonly MessageState Queued = new()
    {
        Outbox = true,
        OfferReply = true,
        OfferForward = true,
        OfferTrash = true,
        OfferArchive = true,
        OfferMove = true,
        OfferJunk = true,
        Reply = true,
        Forward = true,
        Trash = true,
    };

    // An issue tracker's message: comments and forwards only; Trash is not offered.
    private static readonly MessageState Issue = new()
    {
        OfferReply = true,
        OfferForward = true,
        Reply = true,
        Forward = true,
        MarkRead = true,
        ToggleFlag = true,
        ChangeStatus = true,
        Assistant = true,
    };

    // show: groups as "id id- | id", "-" after a disabled item.
    private static string Show(IReadOnlyList<IReadOnlyList<Item>> gs) =>
        string.Join(" | ", gs.Select(g => string.Join(" ", g.Select(it => Name(it.Id) + (it.Enabled ? "" : "-")))));

    public static TheoryData<string, MessageState, string> MessageCases => new()
    {
        { "read mail", Mail, "open | reply replyAll forward | markUnread toggleFlag | archive moveTo junk trash | assistant" },
        { "unread mail, no Assistant", Mail with { MarkUnread = false, MarkRead = true, Assistant = false }, "open | reply replyAll forward | markRead toggleFlag | archive moveTo junk trash" },
        { "conversation with read and unread members", Mail with { MarkRead = true }, "open | reply replyAll forward | markRead markUnread toggleFlag | archive moveTo junk trash | assistant" },
        { "in the Archive and the Junk folder of its own", Mail with { Archive = false, Junk = false }, "open | reply replyAll forward | markUnread toggleFlag | archive- moveTo junk- trash | assistant" },
        { "draft", Mail with { Draft = true }, "edit | reply replyAll forward | markUnread toggleFlag | archive moveTo junk trash | assistant" },
        { "outbox", Queued, "open | reply forward | toggleFlag- | archive- moveTo- junk- trash" },
        { "issue tracker", Issue, "open | reply forward | markRead toggleFlag changeStatus | assistant" },
        { "queued comment of an issue tracker", new MessageState { Outbox = true, Trash = true }, "open | toggleFlag- | trash" },
    };

    [Theory]
    [MemberData(nameof(MessageCases))]
    public void MessageItemsTest(string name, MessageState s, string want) =>
        Assert.True(Show(MessageItems(s)) == want, $"{name}:\n got {Show(MessageItems(s))}\nwant {want}");

    /// <summary>Swift's messageStateOfFlags: the state taken from the window's action flags.</summary>
    [Fact]
    public void MessageStateOfFlags()
    {
        var f = new ActionFlags
        {
            On = true,
            Flagged = true,
            Reply = true,
            Forward = true,
            Trash = true,
            Archive = true,
            Move = true,
            MarkRead = true,
            ToggleFlag = true,
            ChangeStatus = true,
            Unsupported = MessageActionKind.ReplyAll | MessageActionKind.Junk,
        };
        var s = MessageState.Of(f, draft: true, assistant: false);
        Assert.True(s.Draft && s.ChangeStatus && !s.Assistant && !s.Outbox, s.ToString());
        Assert.True(s.OfferReply && !s.OfferReplyAll && s.OfferForward && s.OfferArchive && s.OfferMove && !s.OfferJunk && s.OfferTrash, s.ToString());
        Assert.True(s.Reply && !s.ReplyAll && s.Forward && s.Trash && s.Archive && !s.Junk && s.Move && s.MarkRead && !s.MarkUnread && s.ToggleFlag, s.ToString());
    }

    public static TheoryData<string, FolderState, string> FolderCases => new()
    {
        { "inbox with unread mail", new FolderState { Selectable = true, Role = FolderRole.Inbox, Unread = 3, Total = 9, CanDelete = true, Foldable = true }, "checkMail | markAllRead | favourite fold" },
        { "all read", new FolderState { Selectable = true, Role = FolderRole.None, Total = 9, CanDelete = true }, "checkMail | markAllRead- | favourite" },
        { "trash", new FolderState { Selectable = true, Role = FolderRole.Trash, Unread = 1, Total = 4, CanDelete = true }, "checkMail | markAllRead empty | favourite" },
        { "empty junk", new FolderState { Selectable = true, Role = FolderRole.Junk, CanDelete = true }, "checkMail | markAllRead- empty- | favourite" },
        { "trash of an account that cannot delete", new FolderState { Selectable = true, Role = FolderRole.Trash, Total = 4 }, "checkMail | markAllRead- | favourite" },
        { "outbox", new FolderState { Selectable = true, Role = FolderRole.Outbox, Total = 2, CanDelete = true }, "favourite" },
        { "container", new FolderState { Role = FolderRole.None, Foldable = true }, "fold" },
        { "container that does not fold", new FolderState { Role = FolderRole.Trash, Total = 3, CanDelete = true }, "" },
    };

    [Theory]
    [MemberData(nameof(FolderCases))]
    public void FolderItemsTest(string name, FolderState f, string want) =>
        Assert.True(Show(FolderItems(f)) == want, $"{name}: got \"{Show(FolderItems(f))}\", want \"{want}\"");

    [Fact]
    public void AccountItemsTest()
    {
        Assert.Equal("checkMail | fold", Show(AccountItems(true)));
        Assert.Equal("checkMail", Show(AccountItems(false)));
    }

    private static Folder F(string id, FolderRole role, bool selectable) =>
        MailModelTests.TestFolder(id, id, role, selectable: selectable);

    [Fact]
    public void MoveTargetsTest()
    {
        var view = F("open", FolderRole.None, true) with { Virtual = VirtualFolder.Open };
        Entry[] entries =
        [
            new(F("", FolderRole.None, false), 0), // a heading
            new(F("inbox", FolderRole.Inbox, true), 0),
            new(F("drafts", FolderRole.Drafts, true), 0),
            new(F("sent", FolderRole.Sent, true), 0),
            new(F("all", FolderRole.All, true), 0),
            new(F("trash", FolderRole.Trash, true), 0),
            new(F("outbox", FolderRole.Outbox, true), 0),
            new(view, 0),
            // A container with a subfolder, a container with only a
            // container under it, and a nested one whose subfolder is the
            // current one.
            new(F("projects", FolderRole.None, false), 0),
            new(F("client", FolderRole.None, true), 1),
            new(F("empty", FolderRole.None, false), 0),
            new(F("emptier", FolderRole.None, false), 1),
            new(F("lists", FolderRole.None, false), 0),
            new(F("deep", FolderRole.None, false), 1),
            new(F("current", FolderRole.None, true), 2),
        ];
        var got = string.Join(" ", MoveTargets(entries, "current").Select(t => $"{t.Folder.Id.Value}/{t.Depth}" + (t.Enabled ? "" : "-")));
        Assert.Equal("inbox/0 sent/0 trash/0 projects/0- client/1 lists/0- deep/1- current/2-", got);
        Assert.Empty(MoveTargets([], "x"));
    }

    public static TheoryData<ItemId, Labels, string> LabelCases => new()
    {
        { ItemId.OpenMessage, new Labels(), "Open in New Window" },
        { ItemId.EditDraft, new Labels(), "Edit" },
        { ItemId.Reply, new Labels(), "Reply" },
        { ItemId.Reply, new Labels { Comment = true }, "Comment" },
        { ItemId.ReplyAll, new Labels(), "Reply All" },
        { ItemId.Forward, new Labels(), "Forward" },
        { ItemId.MarkRead, new Labels(), "Mark as _Read" },
        { ItemId.MarkUnread, new Labels(), "Mark as _Unread" },
        { ItemId.ToggleFlag, new Labels(), "Star" },
        { ItemId.ToggleFlag, new Labels { Flagged = true }, "Unstar" },
        { ItemId.ChangeStatus, new Labels(), "Change Status" },
        { ItemId.Archive, new Labels(), "Archive" },
        { ItemId.MoveTo, new Labels(), "Move To" },
        { ItemId.Junk, new Labels(), "Mark as Junk" },
        { ItemId.Trash, new Labels(), "Move to Trash" },
        { ItemId.Trash, new Labels { Outbox = true }, "Cancel Sending" },
        { ItemId.Assistant, new Labels(), "Assistant" },
        { ItemId.CheckMail, new Labels(), "Check for New Mail" },
        { ItemId.MarkAllRead, new Labels(), "Mark All as Read" },
        { ItemId.EmptyFolder, new Labels(), "Empty Trash" },
        { ItemId.EmptyFolder, new Labels { Junk = true }, "Empty Junk" },
        { ItemId.Favourite, new Labels(), "Add to Favourites" },
        { ItemId.Favourite, new Labels { Favourite = true }, "Remove from Favourites" },
        { ItemId.Fold, new Labels(), "Collapse" },
        { ItemId.Fold, new Labels { Collapsed = true }, "Expand" },
    };

    [Theory]
    [MemberData(nameof(LabelCases))]
    public void LabelsTest(ItemId id, Labels l, string want) => Assert.Equal(want, Label(id, l));

    /// <summary>Every item has Go's id; a value that is no item (Go's "nothing") has no id and no label.</summary>
    [Fact]
    public void NamesAreGos()
    {
        Assert.Equal("", Label((ItemId)999, new Labels()));
        Assert.Equal(
            "open edit reply replyAll forward markRead markUnread toggleFlag changeStatus archive moveTo junk trash assistant "
            + "checkMail markAllRead empty favourite fold",
            string.Join(" ", Enum.GetValues<ItemId>().Select(Name)));
        Assert.Equal("", Name((ItemId)999));
    }

    [Fact]
    public void EmptyQuestionTest()
    {
        var q = EmptyQuestion(false, 1);
        Assert.Equal("Empty the Trash?", q.Heading);
        Assert.Equal("_Empty", q.Confirm);
        Assert.Equal("1 message will be deleted for good. Older messages that are only on the mail server stay there.", q.Body);
        q = EmptyQuestion(true, 12);
        Assert.Equal("Empty the Junk Folder?", q.Heading);
        Assert.StartsWith("12 messages will be deleted for good.", q.Body, StringComparison.Ordinal);
    }

    [Fact]
    public void ProgressTexts()
    {
        Assert.Equal("Moving the message", MovingText(1));
        Assert.Equal("Moving 3 messages", MovingText(3));
        Assert.Equal("Marking the folder as read", MarkingReadText());
        Assert.Equal("Emptying the Trash", EmptyingText(false));
        Assert.Equal("Emptying the Junk folder", EmptyingText(true));
    }
}
