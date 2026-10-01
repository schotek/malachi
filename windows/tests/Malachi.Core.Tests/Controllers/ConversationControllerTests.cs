// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/ConversationControllerTests.swift,
// the counterpart of ui/internal/window/conversation_controller_test.go:
// the conversation view's controller half and what the list controller
// does for it (SelectedRowChanged, ThreadMembersChanged, the mark-as-read
// of a conversation row), over MailFixture. A conversation row shows the
// whole conversation through a folder-scoped thread.get and marks only its
// newest message read; a member row keeps the single message; arrivals,
// flag changes and removals reach the model; bodies are fetched per card,
// message.get only when needed and not again once it failed; a
// conversation shown from the listing, after thread.get failed, is built
// anew when its members arrive; the user's replies in Sent are sent cards
// and a reply that lands in Sent joins the conversation; Show Quoted Text
// holds for the conversation. Swift sleeps through the mark-as-read delay;
// here the clock is moved.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Model;
using Xunit;
using static Malachi.Core.Tests.Model.MailModelTests;
using Change = Malachi.Core.Controllers.ConversationController.Change;

namespace Malachi.Core.Tests.Controllers;

public sealed class ConversationControllerTests
{
    private static readonly AccountId Account = "a";
    private static readonly FolderKey Inbox = new("a", "in");
    private static readonly FolderKey SentBox = new("a", "sent");
    private static readonly TimeSpan Delay = TimeSpan.FromSeconds(1);

    // 2026-09-01T10:00:00Z.
    private static readonly DateTimeOffset Base = DateTimeOffset.FromUnixTimeSeconds(1_788_256_800);

    [Fact]
    public async Task ConversationRowShowsTheWholeConversation()
    {
        await using var c = await Conv.StartAsync();
        var h = c.H;
        Assert.Equal([new ListKey(Thread: "t3"), new ListKey("t2", "b1"), new ListKey(Thread: "t1")], h.List.Rows.Select(r => r.Key));
        Assert.True(h.List.Rows[0].ShowsConversation);
        Assert.False(h.List.Rows[1].ShowsConversation); // a single-message conversation is a plain row

        await c.SelectAsync(new ListKey(Thread: "t1"));
        Assert.Equal([true], c.Shown);
        Assert.Equal("t1", c.Conversation.Thread?.Value);
        Assert.Equal([Change.Loading, Change.Opened], c.Changes);
        var asked = Assert.Single(h.Fixture.ThreadGetRequests);
        Assert.Equal(("a", "t1", "in", (bool?)true), (asked.AccountId.Value, asked.ThreadId.Value, asked.FolderId?.Value, asked.WithSent));
        Assert.Equal(["a1", "a2", "a3"], Shape(c.Conversation.Model));
        Assert.Equal("a3", c.Conversation.Model?.MarkRead?.Value);
        Assert.Equal(2, c.Conversation.Model?.ScrollTo);
        Assert.Equal(["bob", "alice", "carol"], c.Conversation.Model!.Items.Select(it => it.Sender));
        // The toolbar still acts on the row (its newest member).
        Assert.Equal(["a3"], c.Selections.Select(id => id?.Value));
        Assert.Equal(3, h.List.Rows.Count); // showing the conversation does not unfold the row

        // The same row announced again keeps the model.
        Assert.True(await h.On(() => c.Conversation.Show(h.List.SelectedRow)));
        Assert.Equal([Change.Loading, Change.Opened], c.Changes);

        // Known members: no second thread.get for another visit.
        await c.SelectAsync(new ListKey("t2", "b1"));
        Assert.Equal([true, false], c.Shown);
        Assert.True(c.Conversation.Thread is null && c.Conversation.Model is null);
        Assert.Equal(Change.Cleared, c.Changes[^1]);
        await c.SelectAsync(new ListKey(Thread: "t1"));
        Assert.Single(h.Fixture.ThreadGetRequests);
        Assert.Equal(["a1", "a2", "a3"], Shape(c.Conversation.Model));
    }

    [Fact]
    public async Task OnlyTheNewestUnreadMemberIsMarkedAfterTheDelay()
    {
        await using var c = await Conv.StartAsync();
        var h = c.H;
        await c.SelectAsync(new ListKey(Thread: "t1"));
        Assert.Empty(c.Marks); // not before the delay
        await h.AdvanceAsync(Delay);
        Assert.Equal(["a3"], Ids(c.Marks)); // a1 is unread too and stays so

        // A conversation whose newest member is read marks nothing.
        await c.SelectAsync(new ListKey(Thread: "t3"));
        await h.AdvanceAsync(Delay);
        Assert.Equal(["a3"], Ids(c.Marks));

        // Leaving before the timer fires cancels it.
        h.Fixture.SetMessages([.. Messages().Select(s => s with { Flags = [] })], Account, "in");
        await h.On(h.List.LoadMessages);
        await h.IdleAsync();
        Assert.Equal(2, h.List.RowFor(new ListKey(Thread: "t3"))?.Summary?.UnreadCount);
        await h.On(() =>
        {
            h.List.Select(new ListKey(Thread: "t3"));
            h.List.Select(new ListKey("t2", "b1"));
        });
        await h.IdleAsync();
        await h.AdvanceAsync(Delay);
        Assert.Equal(["a3", "b1"], Ids(c.Marks)); // c2's timer went with the selection; the plain row marks itself
    }

    [Fact]
    public async Task MemberRowKeepsTheSingleMessage()
    {
        await using var c = await Conv.StartAsync();
        var h = c.H;
        await h.On(() => h.List.ToggleThread("t1"));
        await h.IdleAsync();
        Assert.Equal(6, h.List.Rows.Count);
        await c.SelectAsync(new ListKey("t1", "a1"));
        Assert.Equal([false], c.Shown);
        Assert.True(c.Conversation.Thread is null && c.Changes.Count == 0);
        Assert.Equal(["a1"], c.Selections.Select(id => id?.Value));
        await h.AdvanceAsync(Delay);
        Assert.Equal(["a1"], Ids(c.Marks)); // a member row marks its own message, as before

        // The unfolded conversation row itself shows the conversation.
        await c.SelectAsync(new ListKey(Thread: "t1"));
        Assert.Equal([false, true], c.Shown);
        Assert.Equal(["a1", "a2", "a3"], Shape(c.Conversation.Model));
    }

    [Fact]
    public async Task FlatListShowsSingleMessages()
    {
        await using var c = await Conv.StartAsync(grouped: false);
        await c.SelectAsync(new ListKey(Message: "a3"));
        Assert.Equal([false], c.Shown);
        Assert.Empty(c.H.Fixture.ThreadGetRequests);
    }

    [Fact]
    public async Task MembersFollowTheList()
    {
        await using var c = await Conv.StartAsync();
        var h = c.H;
        await c.SelectAsync(new ListKey(Thread: "t1"));
        var opened = c.Changes.Count;

        // A new arrival in the conversation: the row keeps its key, the model
        // takes the member.
        var late = Msg("a4", 7, "t1", "gina");
        h.Fixture.AddMessage(late);
        await h.On(() => h.Mailbox.HandleNewMessage(New(late)));
        Assert.Equal(new ListKey(Thread: "t1"), h.List.SelectedKey);
        Assert.Equal(["a1", "a2", "a3", "a4"], Shape(c.Conversation.Model));
        Assert.True(c.Changes.Count == opened + 1 && c.Changes[^1] == Change.Updated);
        Assert.Equal("a4", c.Conversation.Model?.MarkRead?.Value);

        // A flag change: the member's card follows.
        Assert.Equal(["a4"], Ids(await h.On(() => h.List.ApplyFlags(["a4"], setFlags: [Flag.Seen]))));
        Assert.False(c.Conversation.Model!.Items[3].Unread);
        Assert.Null(c.Conversation.Model.MarkRead);
        Assert.Equal(Change.Updated, c.Changes[^1]);

        // A removal drops the card; its undo brings it back.
        var restore = await h.On(() => h.List.RemoveRows(["a2"]));
        Assert.Equal(["a1", "a3", "a4"], Shape(c.Conversation.Model));
        await h.On(restore);
        Assert.Equal(["a1", "a2", "a3", "a4"], Shape(c.Conversation.Model));

        // A message of another conversation changes nothing here.
        var count = c.Changes.Count;
        await h.On(() => h.Mailbox.HandleNewMessage(New(Msg("c3", 8, "t3"))));
        Assert.Equal(count, c.Changes.Count);
        Assert.Equal(["a1", "a2", "a3", "a4"], Shape(c.Conversation.Model));
        await h.IdleAsync();
    }

    // The account of the conversation tells the user's own messages
    // (ConversationItem.Mine): mail by the address of its first sender, an
    // item of an issue by what the site says.
    [Fact]
    public async Task OwnMessagesAreTold()
    {
        var own = Msg("a2", 2, "t1", "a", Flag.Seen) with { From = [new Address { Name = "Alena", Email = " A@Example.INVALID " }] };
        await using var c = await Conv.StartAsync([Msg("a1", 1, "t1", "bob", Flag.Seen), own, Msg("a3", 3, "t1", "carol", Flag.Seen)]);
        var h = c.H;
        await c.SelectAsync(new ListKey(Thread: "t1"));
        Assert.Equal([false, true, false], c.Conversation.Model!.Items.Select(it => it.Mine));
        Assert.Equal(["bob", "Alena", "carol"], c.Conversation.Model.Items.Select(it => it.Sender)); // the name stays the sender's

        // An arrival while the conversation is shown.
        var late = Msg("a4", 7, "t1", "a");
        h.Fixture.AddMessage(late);
        await h.On(() => h.Mailbox.HandleNewMessage(New(late)));
        Assert.Equal(["a1", "a2", "a3", "a4"], Shape(c.Conversation.Model));
        Assert.Equal([false, true, false, true], c.Conversation.Model!.Items.Select(it => it.Mine));
    }

    [Fact]
    public async Task OwnItemsOfAnIssueAreTold()
    {
        var info = new IssueInfo
        {
            Key = "MOB-3",
            Url = "https://acme.atlassian.net/browse/MOB-3",
            Summary = "Login screen flickers",
            Status = "To Do",
            StatusCategory = IssueStatusCategory.Todo,
        };
        MessageSummary Comment(string id, int hours, string from, bool? mine = null, string? via = null) =>
            Msg(id, hours, "issue-MOB-3", from, Flag.Seen) with { Issue = MessageIssue.Of(info, IssueItemKind.Comment) with { Via = via, Mine = mine } };
        await using var c = await Conv.StartAsync(
        [
            Comment("c1", 1, "Jana Dvořáková"),
            Comment("c2", 2, "a", mine: true),
            // The sender's address is the account's, the site says nothing: not the user's.
            Comment("c3", 3, "a"),
            Comment("c4", 4, "Petr Svoboda", mine: true, via: "Issue Sync"),
        ]);
        await c.SelectAsync(new ListKey(Thread: "issue-MOB-3"));
        Assert.Equal(["c1", "c2", "c3", "c4"], Shape(c.Conversation.Model));
        Assert.Equal([false, true, false, false], c.Conversation.Model!.Items.Select(it => it.Mine));
    }

    [Fact]
    public async Task ReloadThatChangesTheConversationFetchesItAgain()
    {
        await using var c = await Conv.StartAsync();
        var h = c.H;
        await c.SelectAsync(new ListKey(Thread: "t1"));
        Assert.Single(h.Fixture.ThreadGetRequests);

        // A reload with the same shape keeps the members: no thread.get.
        await h.On(() => h.Mailbox.ReloadMessages!());
        await h.IdleAsync();
        Assert.Equal(2, h.Fixture.ThreadListRequests.Count);
        Assert.Single(h.Fixture.ThreadGetRequests);
        Assert.Equal(["a1", "a2", "a3"], Shape(c.Conversation.Model));

        // A member arrived meanwhile (a sync): asked for again and merged.
        h.Fixture.AddMessage(Msg("a5", 9, "t1", "hana"));
        await h.On(() => h.Mailbox.ReloadMessages!());
        await h.IdleAsync();
        Assert.Equal(["a1", "a2", "a3", "a5"], Shape(c.Conversation.Model));
        Assert.Equal(2, h.Fixture.ThreadGetRequests.Count);
        Assert.Equal("t1", c.Conversation.Thread?.Value);
        Assert.Equal(Change.Updated, c.Changes[^1]);
    }

    [Fact]
    public async Task FailedThreadGetShowsWhatTheListingKnows()
    {
        await using var c = await Conv.StartAsync();
        var h = c.H;
        h.Fixture.Fail(API.ThreadGet.Name, new RpcError { Code = ErrorCode.StorageError, Message = "disk" });
        await c.SelectAsync(new ListKey(Thread: "t1"));
        Assert.Equal(["more", "a3"], Shape(c.Conversation.Model));
        Assert.Equal(2, c.Conversation.Model?.Earlier);
        Assert.Equal([Change.Loading, Change.Opened], c.Changes);
        await h.AdvanceAsync(Delay);
        Assert.Empty(c.Marks); // nothing is marked without the members

        // The list telling of the members for other reasons does not ask
        // again.
        await h.On(() => c.Conversation.MembersChanged("t1"));
        Assert.Equal(["a3"], Ids(await h.On(() => h.List.ApplyFlags(["a3"], setFlags: [Flag.Flagged]))));
        await h.IdleAsync();
        Assert.Equal(1, h.Fixture.CallCount(API.ThreadGet.Name));
        Assert.Equal(["more", "a3"], Shape(c.Conversation.Model));

        // A reload lists the conversation anew and asks again; the members
        // build the model anew (the row of older members goes) and mark.
        h.Fixture.Succeed(API.ThreadGet.Name);
        h.Fixture.AddMessage(Msg("a4", 8, "t1", "gina"));
        await h.On(() => h.Mailbox.ReloadMessages!());
        await h.IdleAsync();
        Assert.Equal(["a1", "a2", "a3", "a4"], Shape(c.Conversation.Model));
        Assert.Equal(2, h.Fixture.CallCount(API.ThreadGet.Name));
        Assert.Equal(0, c.Conversation.Model?.Earlier);
        Assert.Equal(Change.Opened, c.Changes[^1]);
        await h.AdvanceAsync(Delay);
        Assert.Equal(["a4"], Ids(c.Marks)); // marked once the members arrived

        // From then on the model follows the list as any other.
        var count = c.Changes.Count;
        Assert.Equal(["a4"], Ids(await h.On(() => h.List.ApplyFlags(["a4"], setFlags: [Flag.Seen]))));
        Assert.True(c.Changes.Count == count + 1 && c.Changes[^1] == Change.Updated);
    }

    [Fact]
    public async Task IssueConversationMarksItsNewestCommentNeverAnEvent()
    {
        var info = new IssueInfo
        {
            Key = "WEB-7",
            Url = "https://acme.atlassian.net/browse/WEB-7",
            Summary = "Checkout fails",
            Status = "In Progress",
            StatusCategory = IssueStatusCategory.InProgress,
        };
        MessageSummary Item(string id, int hours, IssueItemKind kind, IssueChange[]? changes = null, params Flag[] flags) =>
            Msg(id, hours, "issue-WEB-7", "Jana Dvořáková", flags) with { Issue = MessageIssue.Of(info, kind) with { Changes = changes ?? [] } };
        await using var c = await Conv.StartAsync(
        [
            Item("d", 1, IssueItemKind.Description, flags: Flag.Seen),
            Item("c1", 2, IssueItemKind.Comment),
            Item("c2", 3, IssueItemKind.Comment),
            Item("e1", 4, IssueItemKind.Event, [new IssueChange { Field = IssueField.Status, From = "To Do", To = "In Progress" }], Flag.Seen),
        ]);
        var h = c.H;
        await c.SelectAsync(new ListKey(Thread: "issue-WEB-7"));
        var m = c.Conversation.Model!;
        Assert.Equal(["d", "c1", "c2", "e1"], Shape(m));
        Assert.Equal([ConversationItemKind.Message, ConversationItemKind.Message, ConversationItemKind.Message, ConversationItemKind.Event], m.Items.Select(it => it.Kind));
        Assert.Equal(["Status: To Do → In Progress"], m.Items[3].EventLines);
        Assert.True(m.Issue?.Key == "WEB-7" && m.Issue.Status == "In Progress");
        Assert.Equal("c2", m.MarkRead?.Value);
        await h.AdvanceAsync(Delay);
        Assert.Equal(["c2"], Ids(c.Marks)); // c1 stays unread; the event is never marked

        // An event has no body to fetch.
        await h.On(() => c.Conversation.NeedsBody("e1"));
        await h.IdleAsync();
        Assert.Equal(0, h.Fixture.CallCount(API.MessageBody.Name));
        Assert.False(c.Conversation.Loaded.ContainsKey("e1"));
    }

    [Fact]
    public async Task BodiesAreFetchedPerCard()
    {
        await using var c = await Conv.StartAsync();
        var h = c.H;
        await c.SelectAsync(new ListKey(Thread: "t1"));
        Assert.Equal(0, h.Fixture.CallCount(API.MessageBody.Name)); // nothing before the pane asks

        // Without attachments: message.body alone.
        await h.On(() =>
        {
            c.Conversation.NeedsBody("a1");
            c.Conversation.NeedsBody("a1");
        });
        await h.IdleAsync();
        Assert.NotNull(c.Conversation.Loaded["a1"].Body);
        Assert.Equal(1, h.Fixture.CallCount(API.MessageBody.Name));
        Assert.Equal(0, h.Fixture.CallCount(API.MessageGet.Name));
        Assert.Equal("body of a1", c.Conversation.Loaded["a1"].Body?.Text);
        Assert.Equal(["a1"], Ids(c.Loads));

        // Held: no second request.
        await h.On(() => c.Conversation.NeedsBody("a1"));
        await h.IdleAsync();
        Assert.Equal(1, h.Fixture.CallCount(API.MessageBody.Name));

        // With attachments (the chips): message.get too.
        await h.On(() => c.Conversation.NeedsBody("a3"));
        await h.IdleAsync();
        Assert.True(c.Conversation.Loaded["a3"].Complete);
        Assert.Equal(1, h.Fixture.CallCount(API.MessageGet.Name));

        // The recipients' disclosure (Cc): message.get for a card without
        // attachments too.
        await h.On(() => c.Conversation.NeedsBody("a1", details: true));
        await h.IdleAsync();
        Assert.NotNull(c.Conversation.Loaded["a1"].Msg);
        Assert.Equal(2, h.Fixture.CallCount(API.MessageGet.Name));
        Assert.Equal(2, h.Fixture.CallCount(API.MessageBody.Name)); // a1's body came from the cache

        // An id that is not shown, and an entry adopted from the cache's
        // fan-out.
        await h.On(() => c.Conversation.NeedsBody("b1"));
        await h.IdleAsync();
        Assert.False(c.Conversation.Loaded.ContainsKey("b1"));
        var lm = new LoadedMessage();
        await h.On(() =>
        {
            c.Conversation.Adopt("b1", lm);
            Assert.False(c.Conversation.Loaded.ContainsKey("b1"));
            c.Conversation.Adopt("a2", lm);
            Assert.Same(lm, c.Conversation.Loaded["a2"]);

            // Over the budget, far entries go, the largest first; near ones stay.
            c.Conversation.Trim(new HashSet<MessageId> { "a1" }, budget: 0);
            Assert.Equal(["a1"], c.Conversation.Loaded.Keys.Select(k => k.Value));
        });

        // Another conversation forgets the entries.
        await c.SelectAsync(new ListKey(Thread: "t3"));
        Assert.Empty(c.Conversation.Loaded);
    }

    // The user's replies in Sent stand among the members by date as sent
    // cards: never marked read, never among what the conversation's actions
    // take; one message and the user's reply to it are a conversation row.
    [Fact]
    public async Task RepliesInSentAreSentCards()
    {
        await using var c = await Conv.StartAsync(sent: [Reply("r1", 2, "t1"), Reply("r2", 6, "t2")]);
        var h = c.H;
        // A message and the user's reply are a conversation row.
        Assert.Equal([new ListKey(Thread: "t3"), new ListKey(Thread: "t2"), new ListKey(Thread: "t1")], h.List.Rows.Select(r => r.Key));
        Assert.True(h.List.Rows[1].ShowsConversation);
        Assert.Equal(1, h.List.Rows[1].Summary?.SentCount);

        await c.SelectAsync(new ListKey(Thread: "t1"));
        Assert.Equal(["a1", "a2", "sent:r1", "a3"], SentShape(c.Conversation.Model));
        Assert.Equal("a3", c.Conversation.Model?.MarkRead?.Value);
        var model = await h.On(() => h.Mailbox.Model);
        Assert.Equal(["a1", "a2", "a3"], Ids(await h.On(() => model.RowIds(h.List.Rows[2])!))); // the reply is no member
        Assert.Equal((FolderId?)SentBox.Folder, await h.On(() => c.Conversation.Member("r1")?.FolderId)); // its card has a body to ask for

        await c.SelectAsync(new ListKey(Thread: "t2"));
        Assert.Equal(["b1", "sent:r2"], SentShape(c.Conversation.Model));
        Assert.Equal(["b1"], Ids(await h.On(() => model.RowIds(h.List.Rows[1])!)));
        Assert.Equal("r2", await h.On(() => model.SentMessage("r2")?.Id.Value)); // the card's reply and forward find it
        Assert.All(h.Fixture.ThreadGetRequests, q => Assert.True(q.WithSent));
    }

    // A reply that lands in Sent asks for the conversation again: a single
    // message the user answered becomes a conversation row, the selection
    // moves to it, and the conversation shows the reply.
    [Fact]
    public async Task ReplyArrivingInSentJoinsTheConversation()
    {
        await using var c = await Conv.StartAsync();
        var h = c.H;
        await c.SelectAsync(new ListKey("t2", "b1"));
        Assert.Equal([false], c.Shown);

        var r = Reply("r9", 7, "t2");
        h.Fixture.AddMessage(r);
        await h.On(() => h.Mailbox.HandleNewMessage(InSent(r)));
        await h.IdleAsync();
        Assert.Equal(new ListKey(Thread: "t2"), await h.On(() => h.List.SelectedKey));
        Assert.Equal(["b1", "sent:r9"], SentShape(c.Conversation.Model));

        // Into a conversation on show.
        await c.SelectAsync(new ListKey(Thread: "t1"));
        var r2 = Reply("r10", 8, "t1");
        h.Fixture.AddMessage(r2);
        await h.On(() => h.Mailbox.HandleNewMessage(InSent(r2)));
        await h.IdleAsync();
        Assert.Equal(["a1", "a2", "a3", "sent:r10"], SentShape(c.Conversation.Model));
        Assert.Equal(Change.Updated, c.Changes[^1]);

        // Gone from Sent: the next answer drops the card.
        h.Fixture.SetMessages([], Account, SentBox.Folder);
        await h.On(() => h.List.RefetchMembers("t1"));
        await h.IdleAsync();
        Assert.Equal(["a1", "a2", "a3"], SentShape(c.Conversation.Model));
    }

    // Show Quoted Text on a card: the whole body is asked for and the choice
    // holds through the pane's asking again (every scroll) until another
    // conversation is shown; Hide shows the trimmed body held.
    [Fact]
    public async Task QuotedTextHoldsForTheConversation()
    {
        await using var c = await Conv.StartAsync();
        var h = c.H;
        await c.SelectAsync(new ListKey(Thread: "t1"));
        await h.On(() => c.Conversation.NeedsBody("a1"));
        await h.IdleAsync();
        Assert.NotNull(c.Conversation.Loaded["a1"].Body);
        Assert.Equal(new bool?[] { true }, h.Fixture.BodyRequests.Select(q => q.TrimQuoted));
        Assert.False(c.Conversation.QuotedRevealed("a1"));

        await h.On(() => c.Conversation.SetQuoted("a1", true));
        await h.IdleAsync();
        Assert.True(c.Conversation.Loaded["a1"].QuotedShown && c.Conversation.Loaded["a1"].Body is not null);
        Assert.True(c.Conversation.QuotedRevealed("a1"));
        await h.On(() => c.Conversation.NeedsBody("a1"));
        await h.IdleAsync();
        Assert.Equal(new bool?[] { true, null }, h.Fixture.BodyRequests.Select(q => q.TrimQuoted)); // held: not asked again

        await h.On(() =>
        {
            c.Conversation.SetQuoted("a1", false);
            Assert.True(!c.Conversation.Loaded["a1"].QuotedShown && c.Conversation.Loaded["a1"].Body is not null);
            c.Conversation.SetQuoted("a1", true);
            c.Conversation.SetQuoted("zz", true);
            Assert.False(c.Conversation.QuotedRevealed("zz")); // not a member
        });
        await h.IdleAsync();
        Assert.Equal(2, h.Fixture.BodyRequests.Count); // both variants held

        await c.SelectAsync(new ListKey(Thread: "t3"));
        await c.SelectAsync(new ListKey(Thread: "t1"));
        Assert.False(c.Conversation.QuotedRevealed("a1")); // another conversation forgot it
        await h.On(() => c.Conversation.NeedsBody("a1"));
        await h.IdleAsync();
        Assert.False(c.Conversation.Loaded["a1"].QuotedShown);
        Assert.Equal(2, h.Fixture.BodyRequests.Count); // the trimmed body was held by the cache
    }

    // A failed message.get is not asked again on every scroll; the summary
    // serves the card.
    [Fact]
    public async Task FailedGetIsNotAskedAgain()
    {
        await using var c = await Conv.StartAsync();
        var h = c.H;
        h.Fixture.Fail(API.MessageGet.Name, new RpcError { Code = ErrorCode.MessageNotFound, Message = "gone" });
        await c.SelectAsync(new ListKey(Thread: "t1"));
        for (var i = 0; i < 3; i++)
        {
            await h.On(() => c.Conversation.NeedsBody("a3"));
            await h.IdleAsync();
            var entry = c.Conversation.Loaded["a3"];
            Assert.True(entry.Body is not null && !entry.Getting && !entry.Fetching);
            await h.On(() => c.Cache.Evict((MessageId)"a3")); // the cache let go of it meanwhile
        }
        Assert.Equal(1, h.Fixture.CallCount(API.MessageGet.Name));
        Assert.NotNull(c.Conversation.Loaded["a3"].Body); // the body is held all the same
        Assert.Null(c.Conversation.Loaded["a3"].Msg);

        // The recipients' disclosure does not ask for it again either.
        await h.On(() => c.Conversation.NeedsBody("a3", details: true));
        await h.IdleAsync();
        Assert.Equal(1, h.Fixture.CallCount(API.MessageGet.Name));

        // The daemon rebuilt the conversation's messages: asked anew.
        h.Fixture.Succeed(API.MessageGet.Name);
        await h.On(() =>
        {
            c.Conversation.Refresh("t1");
            c.Conversation.NeedsBody("a3");
        });
        await h.IdleAsync();
        Assert.True(c.Conversation.Loaded["a3"].Complete);
        Assert.Equal(2, h.Fixture.CallCount(API.MessageGet.Name));
    }

    private static MessageSummary Msg(string id, int hours, string thread, string from = "alice", params Flag[] flags) =>
        Msg(id, hours, thread, from, attachments: false, flags);

    private static MessageSummary Msg(string id, int hours, string thread, string from, bool attachments, params Flag[] flags) => new()
    {
        Id = id,
        AccountId = Account,
        FolderId = Inbox.Folder,
        ThreadId = thread,
        From = [new Address { Name = from, Email = from + "@example.invalid" }],
        Subject = "s-" + id,
        Date = Base.AddHours(hours),
        Snippet = "p-" + id,
        Flags = flags,
        HasAttachments = attachments,
        Size = 0,
    };

    // t1: three members, the newest unread and an older one unread too; t2:
    // one member; t3: two members, all read; t3 is the newest conversation.
    private static MessageSummary[] Messages() =>
    [
        Msg("a1", 1, "t1", "bob"),
        Msg("a2", 2, "t1", "alice", Flag.Seen),
        Msg("a3", 3, "t1", "carol", attachments: true),
        Msg("b1", 5, "t2", "dave", Flag.Seen),
        Msg("c1", 4, "t3", "erin", Flag.Seen),
        Msg("c2", 6, "t3", "frank", Flag.Seen),
    ];

    private static NewMessageNotification New(MessageSummary s) => new() { AccountId = Account, FolderId = Inbox.Folder, Message = s };

    // The user's reply in Sent, in conversation thread.
    private static MessageSummary Reply(string id, int hours, string thread) => Msg(id, hours, thread, "a", Flag.Seen) with { FolderId = SentBox.Folder };

    private static NewMessageNotification InSent(MessageSummary s) => new() { AccountId = Account, FolderId = SentBox.Folder, Message = s };

    // Shape with the sent cards marked "sent:".
    private static List<string> SentShape(ConversationModel? m) =>
        [.. (m?.Items ?? []).Select(it => it.Kind == ConversationItemKind.Truncated ? "more" : (it.Sent ? "sent:" : "") + (it.Message?.Id.Value ?? ""))];

    private static List<string> Shape(ConversationModel? m) =>
        [.. (m?.Items ?? []).Select(it => it.Kind == ConversationItemKind.Truncated ? "more" : it.Message?.Id.Value ?? "")];

    private static List<string> Ids(IEnumerable<MessageId> ids) => [.. ids.Select(id => id.Value)];

    // The harness of the Swift suite: a fixture, the mailbox and list
    // halves, the message cache and the conversation controller, wired as
    // the app wires the reading pane.
    private sealed class Conv : IAsyncDisposable
    {
        private Conv(MailboxControllerHarness h) => H = h;

        public MailboxControllerHarness H { get; }

        public MessageCache Cache { get; private set; } = null!;

        public ConversationController Conversation { get; private set; } = null!;

        public List<Change> Changes { get; } = [];

        public List<MessageId> Marks { get; } = [];

        public List<MessageId?> Selections { get; } = [];

        public List<bool> Shown { get; } = [];

        public List<MessageId> Loads { get; } = [];

        public static async Task<Conv> StartAsync(MessageSummary[]? messages = null, bool grouped = true, MessageSummary[]? sent = null)
        {
            Conv? c = null;
            var h = await MailboxControllerHarness.StartAsync(
                f =>
                {
                    f.SetAccounts([TestAccount("a", email: "a@example.invalid")]);
                    f.SetFolders(
                    [
                        TestFolder("in", "INBOX", FolderRole.Inbox) with { AccountId = Account },
                        TestFolder("sent", "Sent", FolderRole.Sent) with { AccountId = Account },
                        TestFolder("trash", "Trash", FolderRole.Trash) with { AccountId = Account },
                    ],
                    Account);
                    f.SetMessages(messages ?? Messages(), Account, Inbox.Folder);
                    f.SetMessages(sent ?? [], Account, SentBox.Folder);
                },
                withList: true,
                wire: h =>
                {
                    c = new Conv(h);
                    c.Cache = new MessageCache(h.Client, h.Toasts.Add, pending: h.Pending);
                    c.Conversation = new ConversationController(h.List, c.Cache);
                    // As the app wires the reading pane.
                    h.List.SelectedRowChanged += (_, row) => c.Shown.Add(c.Conversation.Show(row));
                    h.List.SelectedMessageChanged += (_, s) => c.Selections.Add(s?.Id);
                    h.List.MarkRead += (_, id) => c.Marks.Add(id);
                    c.Conversation.Changed += (_, change) => c.Changes.Add(change);
                    c.Conversation.EntryLoaded += (_, e) => c.Loads.Add(e.Id);
                },
                prepare: s =>
                {
                    s.GroupByConversation = grouped;
                    s.MarkReadDelay = 1;
                });
            await h.IdleAsync();
            Assert.Equal(Inbox, await h.On(() => h.Mailbox.Model.ListFolder));
            return c!;
        }

        // Selects a row and waits until the conversation (if it is one) is
        // built.
        public async Task SelectAsync(ListKey key)
        {
            await H.On(() => H.List.Select(key));
            await H.IdleAsync();
        }

        public async ValueTask DisposeAsync()
        {
            await H.On(() =>
            {
                Conversation.Dispose();
                Cache.Dispose();
            });
            await H.DisposeAsync();
        }
    }
}
