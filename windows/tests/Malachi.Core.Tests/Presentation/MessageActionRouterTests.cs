// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of MessageActionRouter: the MessageActions (the selection, window.go
// registerActions) and the MessageActionDelegate (one message,
// message_window.go's msg.* group) of macos MessageActionsController.swift,
// over the real ActionsController and MailFixture
// (ActionsControllerHarness). What the actions themselves do is
// ActionsControllerTests'; this checks that each command reaches the right
// messages with the right subject and window.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Malachi.Core.Controllers;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
using Malachi.Core.Tests.Controllers;
using Xunit;
using static Malachi.Core.Tests.Controllers.ActionsControllerHarness;

namespace Malachi.Core.Tests.Presentation;

public sealed class MessageActionRouterTests
{
    private static Dictionary<FolderKey, MessageSummary[]> In(FolderKey k, params MessageSummary[] list) => new() { [k] = list };

    private static MessageActionRouter Router(ActionsControllerHarness h, List<ComposeParams>? composed = null) =>
        new(h.Actions, new Selection(h.List))
        {
            MainWindow = () => "main",
            Compose = p => composed?.Add(p),
        };

    [Fact]
    public async Task TheSelectionsTrashAsksWithTheRowsSubjectOverTheMainWindow()
    {
        await using var h = await StartAsync(messages: In(Inbox, Msg("m1", 1), Msg("m2", 2)));
        h.Log.Answer = true;
        var router = Router(h);
        await h.Run(() =>
        {
            h.List.Select(new ListKey(Message: "m2"));
            router.Trash();
        });
        await h.IdleAsync();
        Assert.Equal(new Confirmation("Move to Trash?", "s-m2", "Move to _Trash"), Assert.Single(h.Log.Confirmations));
        Assert.Equal(["m2"], h.Fixture.DeleteRequests.Single().MessageIds.Select(id => id.Value));

        // Nothing selected: nothing happens.
        await h.Run(() =>
        {
            h.List.Select(null);
            router.Trash();
            router.Junk();
            router.Archive();
            router.Reply();
        });
        await h.IdleAsync();
        Assert.Single(h.Log.Confirmations);
    }

    [Fact]
    public async Task AConversationRowActsOnEveryMember()
    {
        await using var h = await StartAsync(messages: In(Inbox, ThreadedMessages()), grouped: true);
        var router = Router(h);
        await h.Run(() =>
        {
            h.List.Select(new ListKey(Thread: "t1"));
            router.ToggleFlag();
        });
        await h.IdleAsync();
        Assert.Equal(["a1", "a2", "a3"], h.Fixture.FlagRequests.Single().MessageIds.Select(id => id.Value).Order());
        Assert.Equal([Flag.Flagged], h.Fixture.FlagRequests.Single().Set);

        await h.Run(router.MarkRead);
        await h.IdleAsync();
        Assert.Equal(2, h.Fixture.FlagRequests.Count);
        Assert.Equal([Flag.Seen], h.Fixture.FlagRequests[^1].Set);

        await h.Run(router.Archive);
        await h.IdleAsync();
        Assert.Equal(["a1", "a2", "a3"], h.Fixture.MoveRequests.Single().MessageIds.Select(id => id.Value).Order());
    }

    [Fact]
    public async Task OneMessagesCommandsActOnItAndAskOverItsWindow()
    {
        await using var h = await StartAsync(messages: In(Inbox, Msg("m1", 1), Msg("m2", 2)));
        h.Log.Answer = true;
        var router = Router(h);
        await h.Run(() =>
        {
            router.ToggleFlag("m1");
            router.MarkRead("m1");
        });
        await h.IdleAsync();
        Assert.Equal(2, h.Fixture.FlagRequests.Count);
        Assert.All(h.Fixture.FlagRequests, r => Assert.Equal(["m1"], r.MessageIds.Select(id => id.Value)));

        await h.Run(() => router.Junk("m2", "window"));
        await h.IdleAsync();
        Assert.Equal("Mark as junk?", Assert.Single(h.Log.Confirmations).Heading);
        Assert.Equal(["m2"], h.Log.ClosedWindows.Select(id => id.Value));
    }

    [Fact]
    public async Task FlagsComeFromTheLiveSummary()
    {
        await using var h = await StartAsync(messages: In(Inbox, Msg("m1", 1)));
        var router = Router(h);
        var opened = Msg("m1", 1);
        Assert.True(router.FlagsFor(opened).MarkRead);
        await h.Run(() => router.MarkRead("m1"));
        await h.IdleAsync();
        // The window still holds the summary it was opened with.
        Assert.False(router.FlagsFor(opened).MarkRead);
        Assert.True(router.FlagsFor(opened).MarkUnread);

        await h.Run(() => h.List.Select(new ListKey(Message: "m1")));
        Assert.True(router.Flags.On);
        Assert.False(router.IsDraft(opened));
    }

    [Fact]
    public async Task NewMessageWritesFromTheAccountOfTheMessage()
    {
        await using var h = await StartAsync(messages: In(Inbox, Msg("m1", 1)));
        var composed = new List<ComposeParams>();
        var router = Router(h, composed);
        var bob = new Address { Name = "Bob", Email = "bob@example.invalid" };
        router.NewMessage(bob, "a");
        var p = Assert.Single(composed);
        Assert.Equal(ComposeKind.New, p.Kind);
        Assert.Equal("a", p.AccountId?.Value);
        Assert.Equal([bob], p.To);
    }

    // ListHalf is the harness's stand-in for ListController.
    private sealed class Selection(ListHalf list) : IListSelection
    {
        public ListRow? SelectedRow => list.SelectedRow;

        public void SelectedIds(Action<ListRow, IReadOnlyList<MessageId>> done) => list.SelectedIds(done);
    }
}
