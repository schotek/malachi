// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/SelectionTests.swift, the
// counterpart of ui/internal/selection/selection_test.go
// (TestCombineOneRowIsTheRow, TestCombineSeveral, TestCombineCaps, TestCombineCrm,
// TestCombineOutbox, TestRestrict, TestReconcile, TestTexts) and
// ui/internal/assistant/selection_test.go (TestMessageActionsFor), with
// Swift's combineKeepsTheAccountsOffer. The context menu of several rows
// (Swift's contextMenuOfSeveral) is ContextMenuTests' MessageItemsSeveral,
// as in Go. Go's Flags are the window's ActionFlags here, as in Swift (Go's
// ChangeStatus is ActionFlags' too, off for several); Reconcile's keys are
// the list's ListKey (Go's tests use strings). Beyond Swift: NewestFirst
// (window/selection.go newestFirstIDs, Swift's MailModel.newestFirst).

using System;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Assistants;
using Malachi.Core.Model;
using Malachi.Core.Selections;
using Malachi.Core.Tests.Model;
using Xunit;

namespace Malachi.Core.Tests.Selections;

public sealed class SelectionTests
{
    // A selected mail message everything applies to, read and not starred.
    private static readonly ActionFlags Mail = new()
    {
        On = true,
        Reply = true,
        ReplyAll = true,
        Forward = true,
        Star = true,
        ToggleFlag = true,
        Trash = true,
        Archive = true,
        Junk = true,
        Move = true,
        MarkUnread = true,
        LoadImages = true,
        TrustSender = true,
    };

    [Fact]
    public void CombineOneRowIsTheRow()
    {
        Assert.Equal(ActionFlags.None, Selection.Combine([]));
        var one = Mail with { ChangeStatus = true, Comment = true };
        Assert.Equal(one with { Count = 1, Open = true }, Selection.Combine([one]));
    }

    /// <summary>
    /// selection_test.go TestCombineCrm: Save to eWay-CRM for several: every
    /// row savable, at most api.MaxCrmMessages, never the saved-state
    /// items; not in the outbox.
    /// </summary>
    [Fact]
    public void CombineCrm()
    {
        var savable = Mail with { Crm = true, CrmSaved = true };
        var one = Selection.Combine([savable]);
        Assert.True(one.Crm && one.CrmSaved);
        var two = Selection.Combine([savable, savable]);
        Assert.True(two.Crm && !two.CrmSaved);
        Assert.False(Selection.Combine([savable, Mail]).Crm);
        var rows = Enumerable.Repeat(savable, API.Limits.MaxCrmMessages + 1).ToArray();
        Assert.False(Selection.Combine(rows).Crm);
        Assert.True(Selection.Combine(rows[..API.Limits.MaxCrmMessages]).Crm);
        var queued = savable with { Outbox = true };
        Assert.False(Selection.Combine([queued, queued]).Crm);
    }

    [Fact]
    public void CombineSeveral()
    {
        var unread = Mail with { MarkUnread = false, MarkRead = true };
        var starred = Mail with { Flagged = true };
        var inArchive = Mail with { Archive = false };
        var got = Selection.Combine([Mail, unread, starred, inArchive]);
        var want = new ActionFlags
        {
            Count = 4,
            Open = true,
            On = true,
            Forward = true,
            Star = true,
            ToggleFlag = true,
            Trash = true,
            Archive = true,
            Junk = true,
            Move = true,
            MarkRead = true,
            MarkUnread = true,
        };
        Assert.Equal(want, got);
        Assert.True(Selection.FlagTarget(got), "not every row is starred: the star stars them");
        var all = Selection.Combine([starred, starred]);
        Assert.True(all.Flagged && !Selection.FlagTarget(all), all.ToString());
        // Archive only when some row can go; Trash and Move only when all can.
        var noTrash = Mail with { Trash = false, Move = false, Archive = false };
        var c = Selection.Combine([inArchive, noTrash]);
        Assert.True(!c.Archive && !c.Trash && !c.Move && c.Junk, c.ToString());
        // Change Status, like the replies, is for one message.
        Assert.False(Selection.Combine([Mail with { ChangeStatus = true }, Mail with { ChangeStatus = true }]).ChangeStatus);
    }

    [Fact]
    public void CombineCaps()
    {
        var overOpen = Selection.Combine([.. Enumerable.Repeat(Mail, Selection.MaxOpen + 1)]);
        Assert.True(!overOpen.Open && overOpen.Forward, overOpen.ToString());
        var overForward = Selection.Combine([.. Enumerable.Repeat(Mail, Selection.MaxForward + 1)]);
        Assert.True(!overForward.Open && !overForward.Forward, overForward.ToString());
        var two = Selection.Combine([Mail, Mail]);
        Assert.True(two.Open && two.Forward, two.ToString());
    }

    [Fact]
    public void CombineOutbox()
    {
        var queued = new ActionFlags { On = true, Outbox = true, Reply = true, Forward = true, Trash = true, LoadImages = true };
        var want = new ActionFlags { Count = 2, Open = true, On = true, Outbox = true, Trash = true };
        Assert.Equal(want, Selection.Combine([queued, queued]));
    }

    /// <summary>What the account offers and Comment are the first row's.</summary>
    [Fact]
    public void CombineKeepsTheAccountsOffer()
    {
        var issue = Mail with { Comment = true, Unsupported = MessageActionKind.Archive | MessageActionKind.Move };
        var c = Selection.Combine([issue, issue]);
        Assert.True(c.Comment, c.ToString());
        Assert.Equal(MessageActionKind.Archive | MessageActionKind.Move, c.Unsupported);
    }

    [Fact]
    public void Restrict()
    {
        (string Key, AccountId Account)[] rows = [("a1", "a"), ("b1", "b"), ("a2", "a"), ("b2", "b")];
        Assert.Equal(["b1", "b2"], Selection.Restrict(rows, "b2"));
        Assert.Equal(["a1", "a2"], Selection.Restrict(rows, "a1"));
        Assert.Equal(4, Selection.Restrict(rows, "zz").Count);
        Assert.Empty(Selection.Restrict(Array.Empty<(string Key, AccountId Account)>(), "x"));
    }

    [Fact]
    public void Reconcile()
    {
        static ListKey M(string id) => new(Message: id);
        var t = new ListKey(Thread: "t");
        var n = new ListKey(Message: "n");
        // m2 and m3 are members of conversation t, folded away; m9 is gone.
        ListKey? Resolve(ListKey k) => k.Message?.Value switch
        {
            "m2" or "m3" => t,
            "m9" => null,
            _ => k,
        };
        ListKey[] kept = [M("m1"), M("m2"), M("m9"), M("m3"), M("m4")];
        ListKey[] gone = [M("m9")];
        Assert.Equal([M("m1"), t, M("m4")], Selection.Reconcile(kept, Resolve, () => n));
        Assert.Equal([n], Selection.Reconcile(gone, Resolve, () => n));
        Assert.Empty(Selection.Reconcile(gone, Resolve));
        Assert.Empty(Selection.Reconcile(gone, Resolve, () => null));
        Assert.Empty(Selection.Reconcile(Array.Empty<ListKey>(), Resolve, () => n));
    }

    [Fact]
    public void Texts()
    {
        Assert.Equal("1 message selected", Selection.SelectedText(1));
        Assert.Equal("5 messages selected", Selection.SelectedText(5));
        Assert.Equal("Cancel sending 3 messages?", Selection.CancelSendingQuestion(3));
    }

    /// <summary>
    /// window/selection.go newestFirstIDs: by the date of each message the
    /// model lists, the selection's order among equal dates, an unknown one
    /// last.
    /// </summary>
    [Fact]
    public void NewestFirst()
    {
        var day = new DateTimeOffset(2026, 10, 14, 9, 0, 0, TimeSpan.Zero);
        var model = new MailModel();
        model.SetMessages(
            [
                MailModelTests.Summary("old") with { Date = day },
                MailModelTests.Summary("new") with { Date = day.AddHours(2) },
                MailModelTests.Summary("same") with { Date = day },
            ],
            new PageInfo { Total = 3 });
        MessageId[] ids = ["old", "unknown", "new", "same"];
        Assert.Equal(["new", "old", "same", "unknown"], Selection.NewestFirst(model, ids).Select(id => id.Value));
    }

    [Fact]
    public void AssistantActionsFor()
    {
        Assert.Equal(Assistant.MessageActions, Assistant.MessageActionsFor(0));
        Assert.Equal(Assistant.MessageActions, Assistant.MessageActionsFor(1));
        Assert.Equal([AssistantAction.Summarize, AssistantAction.Tasks, AssistantAction.Ask], Assistant.MessageActionsFor(3));
        Assert.Equal("Ask About These Messages…", Assistant.LabelFor(AssistantAction.Ask, 2));
        Assert.Equal(Assistant.Label(AssistantAction.Ask), Assistant.LabelFor(AssistantAction.Ask, 1));
        Assert.Equal(Assistant.Label(AssistantAction.Summarize), Assistant.LabelFor(AssistantAction.Summarize, 4));
    }
}
