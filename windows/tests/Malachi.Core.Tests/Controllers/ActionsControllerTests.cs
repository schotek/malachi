// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/ActionsControllerTests.swift (every
// test): the per-message actions over the actions controller
// (ui/internal/window/actions.go, the RPC halves of outbox.go, remote.go,
// compose_open.go and drafts.go), exercised against MailFixture: the
// optimistic change, the call, the revert, the confirmation and the toasts.
// The Go tests of those files (actions_test.go, outbox_test.go,
// drafts_test.go) test their pure helpers, which the model suites port
// (ActionHelpersTests, OutboxTests, InDraftsTests, DraftOpenTests).
//
// Swift polls with waitUntil and sleeps before the negative checks; here
// the test waits until the controllers, the daemon and the UI queue are
// idle (IdleAsync) and then asserts. The one daemon slowed down on purpose
// (a refused junk move) waits on the fixture's fake clock, and the test
// advances it. A daemon answer that must stay out while a second click
// arrives is held by a gate instead of Swift's 50 ms sleep. The harness is
// ActionsControllerHarness. One check is the list controller's rather than
// the actions': draftOpensForEditing's activation of the row through
// ListController.activate; the model rule it goes by is checked instead.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Malachi.Core.Model;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Tests.Model;
using Malachi.Core.Transport;
using Xunit;
using static Malachi.Core.Tests.Controllers.ActionsControllerHarness;

namespace Malachi.Core.Tests.Controllers;

public sealed class ActionsControllerTests
{
    private static readonly Flag[] Seen = [Flag.Seen];

    [Fact]
    public async Task SetSeenIsOptimisticAndRevertsOnFailure()
    {
        await using var h = await StartAsync(messages: In(Inbox, Msg("m1", 1), Msg("m2", 2), Msg("m3", 3, flags: Seen)));
        Assert.Equal(2, h.Unread(Inbox));

        // At once: the row, the badge; then one message.flag for what
        // actually changes (m3 is read already, "unknown" is not listed).
        await h.Run(() =>
        {
            h.Actions.SetSeen(["m1", "m3", "unknown"], true);
            Assert.Equal(Seen, h.List.Row(new ListKey(Message: "m1"))!.Message.Flags);
            Assert.Equal(1, h.Unread(Inbox));
        });
        await h.IdleAsync();
        AssertFlagRequest(Assert.Single(h.Fixture.FlagRequests), ["m1"], set: Seen, clear: null);
        Assert.Empty(h.Log.Toasts);

        // Nothing to do: no call at all.
        await h.Run(() =>
        {
            h.Actions.SetSeen(["m1"], true);
            h.Actions.MarkRead("m1");
        });
        await h.IdleAsync();
        Assert.Single(h.Fixture.FlagRequests);

        // A refused change is put back, with a toast naming the count.
        h.Fixture.Fail(API.MessageFlag.Name, Error(ErrorCode.ServerError, "500"));
        await h.Run(() =>
        {
            h.Actions.SetSeen(["m1", "m3"], false);
            Assert.Equal(3, h.Unread(Inbox));
            Assert.Empty(h.List.Row(new ListKey(Message: "m3"))!.Message.Flags);
        });
        await h.IdleAsync();
        Assert.Equal(["Marking 2 messages as unread failed: the server returned an error"], h.Log.Toasts);
        Assert.Equal(1, h.Unread(Inbox));
        Assert.Equal(Seen, h.List.Row(new ListKey(Message: "m1"))!.Message.Flags);
        Assert.Equal(Seen, h.List.Row(new ListKey(Message: "m3"))!.Message.Flags);
        h.Fixture.Succeed(API.MessageFlag.Name);

        // The singular forms.
        h.Fixture.Fail(API.MessageFlag.Name, Error(ErrorCode.NetworkError, "down"));
        await h.Run(() => h.Actions.MarkUnread("m1"));
        await h.IdleAsync();
        Assert.Equal(2, h.Log.Toasts.Count);
        Assert.Equal("Marking the message as unread failed: the server could not be reached", h.Log.Toasts[^1]);
        await h.Run(() => h.Actions.MarkRead("m2"));
        await h.IdleAsync();
        Assert.Equal(3, h.Log.Toasts.Count);
        Assert.Equal("Marking the message as read failed: the server could not be reached", h.Log.Toasts[^1]);
        Assert.Equal(1, h.Unread(Inbox));
        h.Fixture.Succeed(API.MessageFlag.Name);

        // The mark-as-read timer's target: only a message still unread.
        await h.Run(() => h.Actions.MarkRead("m2"));
        await h.IdleAsync();
        Assert.Equal(2, h.Fixture.FlagRequests.Count);
        Assert.Equal(["m2"], IdsOf(h.Fixture.FlagRequests[^1].MessageIds));
        Assert.Equal(0, h.Unread(Inbox));
    }

    [Fact]
    public async Task StarActsOnEveryMemberOfAConversation()
    {
        await using var h = await StartAsync(messages: In(Inbox, ThreadedMessages()), grouped: true);
        Assert.NotEmpty(h.List.Rows);
        IReadOnlyList<MessageId> members = [];
        var target = false;
        await h.Run(() =>
        {
            h.List.Select(new ListKey(Thread: "t1"));
            h.List.SelectedIds((row, ids) =>
            {
                members = ids;
                target = ListHalf.FlagTarget(row);
            });
        });
        await h.IdleAsync();
        Assert.True(target, "no member flagged: the star flags them all");
        Assert.Equal(["a1", "a2", "a3"], IdsOf(members));
        var row = h.List.SelectedRow!;

        // Every member at once, one call, every star told.
        await h.Run(() =>
        {
            h.Actions.SetFlagged(members, ListHalf.FlagTarget(row));
            Assert.Equal(["a1:true", "a2:true", "a3:true"], h.Log.Stars);
            Assert.Contains<Flag>(Flag.Flagged, h.List.SelectedRow!.Summary!.Flags);
            Assert.True(h.Actions.ActionFlagsFor(h.List.SelectedRow).Flagged);
        });
        await h.IdleAsync();
        AssertFlagRequest(Assert.Single(h.Fixture.FlagRequests), ["a1", "a2", "a3"], set: [Flag.Flagged], clear: null);

        // Any member flagged: the target is off for all of them, and only
        // the flagged ones are sent.
        Assert.False(ListHalf.FlagTarget(h.List.SelectedRow!));
        await h.Run(() => h.Actions.SetFlagged(members, false));
        await h.IdleAsync();
        Assert.Equal(2, h.Fixture.FlagRequests.Count);
        Assert.Equal(6, h.Log.Stars.Count);
        await h.Run(() => h.Actions.SetFlagged(["a2"], true));
        await h.IdleAsync();
        Assert.Equal(3, h.Fixture.FlagRequests.Count);
        var partial = h.List.SelectedRow!;
        Assert.False(ListHalf.FlagTarget(partial));
        await h.Run(() => h.Actions.SetFlagged(members, ListHalf.FlagTarget(partial)));
        await h.IdleAsync();
        Assert.Equal(4, h.Fixture.FlagRequests.Count);
        AssertFlagRequest(h.Fixture.FlagRequests[^1], ["a2"], set: null, clear: [Flag.Flagged]);
        Assert.Equal("a2:false", h.Log.Stars[^1]);

        // A single message toggles; a refused change comes back.
        h.Fixture.Fail(API.MessageFlag.Name, Error(ErrorCode.StorageError, "disk"));
        await h.Run(() =>
        {
            h.Actions.ToggleFlagged("b1");
            Assert.Equal("b1:true", h.Log.Stars[^1]);
        });
        await h.IdleAsync();
        Assert.Equal(["Starring the message failed"], h.Log.Toasts);
        Assert.Equal("b1:false", h.Log.Stars[^1]);
        Assert.Equal(Seen, h.Mailbox.Model.Message("b1")!.Value.Summary.Flags);
    }

    [Fact]
    public async Task TrashAsksThenMovesAndCancelsASendInTheOutbox()
    {
        var queued = Msg("o1", 5) with { Outbox = new OutboxInfo { State = OutboxState.Queued, Attempts = 0 } };
        await using var h = await StartAsync(messages: new Dictionary<FolderKey, MessageSummary[]>
        {
            [Inbox] = [Msg("m1", 1), Msg("m2", 2, flags: Seen)],
            [OutboxFolder] = [queued],
        });
        var log = h.Log;

        // Declined: nothing happens.
        log.Answer = false;
        await h.Run(() => h.Actions.Trash(["m1"], "s-m1"));
        await h.IdleAsync();
        Assert.Equal(new Confirmation("Move to Trash?", "s-m1", "Move to _Trash"), Assert.Single(log.Confirmations));
        Assert.Equal(2, h.List.Rows.Count);
        Assert.Empty(h.Fixture.DeleteRequests);

        // Confirmed: the rows go at once, the windows close, the unread
        // badge moves to Trash, message.delete follows.
        log.Answer = true;
        await h.Run(() => h.Actions.Trash(["m1", "m2"], "two"));
        await h.IdleAsync();
        Assert.Equal(2, log.Confirmations.Count);
        Assert.Equal("Move 2 messages to Trash?", log.Confirmations[^1].Heading);
        Assert.Empty(h.List.Rows);
        Assert.Equal(["m1", "m2"], IdsOf(log.ClosedWindows));
        Assert.Equal(0, h.Unread(Inbox));
        Assert.Equal(1, h.Unread(Trash));
        AssertDeleteRequest(Assert.Single(h.Fixture.DeleteRequests), ["m1", "m2"]);
        Assert.Empty(log.Toasts);

        // Without the setting the question is skipped; a message already in
        // Trash is expunged, so no badge is credited.
        h.Settings.ConfirmDelete = false;
        await h.SelectAsync(Trash);
        Assert.Equal(["m2", "m1"], Ids(h.List.Rows));
        await h.Run(() =>
        {
            h.Actions.Trash("m1");
            Assert.Equal(2, log.Confirmations.Count);
            Assert.Equal(["m2"], Ids(h.List.Rows));
            Assert.Equal(0, h.Unread(Trash));
            Assert.Equal(0, h.Unread(Inbox));
        });
        await h.IdleAsync();
        Assert.Equal(2, h.Fixture.DeleteRequests.Count);
        Assert.Equal(["m2"], IdsOf(h.Fixture.Messages(Acc, Trash.Folder).Select(s => s.Id)));

        // A single outbox message: cancelling the send, always asked, the
        // sidebar refreshed afterwards without a "sent" toast.
        await h.SelectAsync(OutboxFolder);
        Assert.True(h.List.InOutbox);
        var folderLists = h.Fixture.CallCount(API.FolderList.Name);
        await h.Run(() => h.Actions.Trash(["o1"], "ignored"));
        await h.IdleAsync();
        Assert.Equal(3, log.Confirmations.Count);
        Assert.Equal(
            new Confirmation("Cancel sending this message?", "“s-o1” will be removed from the outbox and not sent.", "Do Not _Send"),
            log.Confirmations[^1]);
        Assert.Empty(h.List.Rows);
        Assert.Equal("o1", log.ClosedWindows[^1].Value);
        Assert.Equal(0, h.Unread(OutboxFolder));
        Assert.Equal(3, h.Fixture.DeleteRequests.Count);
        AssertDeleteRequest(h.Fixture.DeleteRequests[^1], ["o1"]);
        Assert.Equal(folderLists + 1, h.Fixture.CallCount(API.FolderList.Name));
        Assert.Equal(0, h.Mailbox.Model.Folder(OutboxFolder)?.Total);
        Assert.True(log.Toasts.Count == 0, "the drop was ours, not a delivery");
        Assert.Empty(h.Fixture.Messages(Acc, OutboxFolder.Folder));
    }

    [Fact]
    public async Task ARefusedRemovalRestoresTheRowsAndTheBadges()
    {
        await using var h = await StartAsync(messages: In(Inbox, Msg("m1", 1), Msg("m2", 2, flags: Seen)), confirmDelete: false);
        h.Fixture.Fail(API.MessageDelete.Name, Error(ErrorCode.StorageError, "disk"));
        await h.Run(() =>
        {
            h.Actions.Trash(["m1", "m2"], "x");
            Assert.Empty(h.List.Rows);
            Assert.Equal(0, h.Unread(Inbox));
            Assert.Equal(1, h.Unread(Trash));
            Assert.Equal(["m1", "m2"], IdsOf(h.Log.ClosedWindows));
        });
        await h.IdleAsync();
        Assert.Equal(["Moving 2 messages to Trash failed"], h.Log.Toasts);
        Assert.Equal(["m2", "m1"], Ids(h.List.Rows));
        Assert.Equal(1, h.Unread(Inbox));
        Assert.Equal(0, h.Unread(Trash));

        // A refused cancel puts the outbox row back too.
        h.Fixture.Fail(API.MessageDelete.Name, Error(ErrorCode.InvalidArgument, "sending"));
        var sending = Msg("o1", 5) with { Outbox = new OutboxInfo { State = OutboxState.Sending, Attempts = 1 } };
        h.Fixture.SetMessages([sending], Acc, OutboxFolder.Folder);
        await h.SelectAsync(OutboxFolder);
        await h.Run(() => h.Actions.CancelSend("o1"));
        await h.IdleAsync();
        Assert.Single(h.Log.Confirmations);
        Assert.Equal(2, h.Log.Toasts.Count);
        Assert.Equal("Cancelling the send was rejected: sending", h.Log.Toasts[^1]);
        Assert.Equal(["o1"], Ids(h.List.Rows));
        Assert.Equal(1, h.Unread(OutboxFolder));

        // The bookkeeping alone: read messages move no badge, an unread one
        // leaves its folder for the target and comes back on undo.
        await h.SelectAsync(Inbox);
        var read = h.Summary("m2");
        var unread = h.Summary("m1");
        await h.Run(() =>
        {
            _ = h.Actions.TrackMoves([read], Trash);
            Assert.Equal((1, 0), (h.Unread(Inbox), h.Unread(Trash)));
            var undo = h.Actions.TrackMoves([unread, read], Trash);
            Assert.Equal((0, 1), (h.Unread(Inbox), h.Unread(Trash)));
            undo();
            Assert.Equal((1, 0), (h.Unread(Inbox), h.Unread(Trash)));
            var gone = h.Actions.TrackMoves([unread], null);
            Assert.Equal((0, 0), (h.Unread(Inbox), h.Unread(Trash)));
            gone();
            Assert.Equal(1, h.Unread(Inbox));
        });
    }

    [Fact]
    public async Task ArchiveAndJunkNeedTheRoleFolders()
    {
        // Without an Archive or Junk folder: a toast, nothing else.
        await using (var bare = await StartAsync(
            folders: [MailModelTests.TestFolder("in", "INBOX", FolderRole.Inbox), MailModelTests.TestFolder("trash", "Trash", FolderRole.Trash)],
            messages: In(Inbox, Msg("m1", 1))))
        {
            await bare.Run(() =>
            {
                bare.Actions.Archive(["m1"]);
                Assert.Equal(["This account has no archive folder"], bare.Log.Toasts);
                bare.Actions.Junk(["m1"], "s");
                Assert.Equal("This account has no junk folder", bare.Log.Toasts[^1]);
            });
            await bare.IdleAsync();
            Assert.Empty(bare.Log.Confirmations);
            Assert.Single(bare.List.Rows);
            Assert.False(bare.Actions.ActionFlagsFor(bare.List.Rows[0]).Archive);
            Assert.False(bare.Actions.ActionFlagsFor(bare.List.Rows[0]).Junk);
        }

        await using var h = await StartAsync(
            messages: new Dictionary<FolderKey, MessageSummary[]>
            {
                [Inbox] = [Msg("m1", 1), Msg("m2", 2, flags: Seen)],
                [ArchiveFolder] = [Msg("x1", 3)],
            },
            confirmDelete: false);
        Assert.True(h.Actions.ActionFlagsFor(h.List.Rows[0]).Archive);

        // Archive: no question, the row and the badge go, message.move.
        await h.Run(() =>
        {
            h.Actions.Archive(["m1", "unknown"]);
            Assert.Equal(["m2"], Ids(h.List.Rows));
            Assert.Equal(["m1"], IdsOf(h.Log.ClosedWindows));
            Assert.Equal(0, h.Unread(Inbox));
            Assert.Equal(2, h.Unread(ArchiveFolder));
        });
        await h.IdleAsync();
        AssertMoveRequest(Assert.Single(h.Fixture.MoveRequests), ["m1"], "arch");
        Assert.Empty(h.Log.Confirmations);

        // Junk always asks, whatever the Trash setting says; declined is a
        // no-op, confirmed moves.
        h.Log.Answer = false;
        await h.Run(() => h.Actions.Junk("m2"));
        await h.IdleAsync();
        Assert.Equal(new Confirmation("Mark as junk?", "s-m2", "Mark as _Junk"), Assert.Single(h.Log.Confirmations));
        Assert.Equal(["m2"], Ids(h.List.Rows));
        h.Log.Answer = true;
        await h.Run(() => h.Actions.Junk(["m2"], "s-m2"));
        await h.IdleAsync();
        Assert.Equal(2, h.Log.Confirmations.Count);
        Assert.Equal(2, h.Fixture.MoveRequests.Count);
        AssertMoveRequest(h.Fixture.MoveRequests[^1], ["m2"], "junk");
        Assert.Empty(h.List.Rows);
        Assert.Equal(["m1", "m2"], IdsOf(h.Log.ClosedWindows));

        // Already in the target folder: nothing happens.
        await h.SelectAsync(ArchiveFolder);
        Assert.Equal(["x1", "m1"], Ids(h.List.Rows));
        Assert.False(h.Actions.ActionFlagsFor(h.List.Rows[0]).Archive);
        await h.Run(() => h.Actions.Archive(["x1"]));
        await h.IdleAsync();
        Assert.Equal(["x1", "m1"], Ids(h.List.Rows));
        Assert.Equal(2, h.Fixture.MoveRequests.Count);

        // A refused move comes back with a toast, the badges with it. The
        // daemon is slowed down (on the fixture's clock) so the rows can be
        // seen gone meanwhile.
        h.Fixture.Fail(API.MessageMove.Name, Error(ErrorCode.NetworkError, "down"));
        h.Fixture.Delay(API.MessageMove.Name, TimeSpan.FromMilliseconds(150));
        await h.Run(() => h.Actions.Junk(["x1", "m1"], "both"));
        await Eventually.Holds(() => h.Fixture.Served.Count(m => m == API.MessageMove.Name) == 3);
        await h.Ui.DrainAsync();
        Assert.Equal(3, h.Log.Confirmations.Count);
        Assert.Equal("Mark 2 messages as junk?", h.Log.Confirmations[^1].Heading);
        Assert.Empty(h.List.Rows);
        Assert.Equal(0, h.Unread(ArchiveFolder));
        Assert.Equal(2, h.Unread(JunkFolder));
        Assert.Equal(["m1", "m2", "x1", "m1"], IdsOf(h.Log.ClosedWindows));
        Assert.Empty(h.Log.Toasts);
        h.Time.Advance(TimeSpan.FromMilliseconds(150));
        await h.IdleAsync();
        Assert.Equal(["Marking 2 messages as junk failed: the server could not be reached"], h.Log.Toasts);
        Assert.Equal(["x1", "m1"], Ids(h.List.Rows));
        Assert.Equal(2, h.Unread(ArchiveFolder));
        Assert.Equal(0, h.Unread(JunkFolder));
    }

    [Fact]
    public async Task RetryOutboxShowsQueuedAtOnceAndFetchesTheTruthBack()
    {
        var failed = Msg("o1", 5) with
        {
            FolderId = OutboxFolder.Folder,
            Outbox = new OutboxInfo { State = OutboxState.Failed, Attempts = 2, Error = new RpcError { Code = ErrorCode.NetworkError, Message = "no route" } },
        };
        await using var h = await StartAsync(messages: In(OutboxFolder, failed));
        // message.get keeps answering with the failed state; outbox.retry is
        // scripted.
        var detail = JsonCoding.EncodeToString(new MessageGetResult { Message = new Message { Summary = failed } });
        Answer(h, API.MessageGet.Name, _ => detail);
        Answer(h, API.OutboxRetry.Name, _ => "{}");
        await h.SelectAsync(OutboxFolder);
        await h.LoadAsync("o1");
        Assert.Equal(OutboxState.Failed, h.Cache.Loaded("o1")!.Msg!.Summary.Outbox!.State.Value);
        Assert.True(h.Actions.FlagsFor(h.Summary("o1")).Outbox);

        // Queued at once, in the list and in the cache, the banners told.
        await h.Run(() =>
        {
            h.Actions.RetryOutbox("o1");
            Assert.Equal(OutboxState.Queued, h.Mailbox.Model.Message("o1")!.Value.Summary.Outbox!.State.Value);
            Assert.Null(h.Mailbox.Model.Message("o1")!.Value.Summary.Outbox!.Error);
            Assert.Equal(OutboxState.Queued, h.Cache.Loaded("o1")!.Msg!.Summary.Outbox!.State.Value);
            Assert.Null(h.Cache.Loaded("o1")!.Msg!.Summary.Outbox!.Error);
            Assert.Equal(["o1"], IdsOf(h.Log.OutboxStates));
        });
        await h.IdleAsync();
        Assert.Equal(1, h.Fixture.CallCount(API.OutboxRetry.Name));
        Assert.Empty(h.Log.Toasts);
        Assert.True(h.Cache.Loaded("o1")!.Msg!.Summary.Outbox!.State.Value == OutboxState.Queued, "an accepted retry fetches nothing");
        Assert.Equal(1, h.Fixture.CallCount(API.MessageGet.Name));

        // Refused: the toast, then message.get brings the real state back.
        Refuse(h, API.OutboxRetry.Name, ErrorCode.InvalidArgument, "not failed");
        await h.Run(() =>
        {
            h.Actions.RetryOutbox("o1");
            Assert.Equal(["o1", "o1"], IdsOf(h.Log.OutboxStates));
        });
        await h.IdleAsync();
        Assert.Equal(3, h.Log.OutboxStates.Count);
        Assert.Equal(["Retrying the send was rejected: not failed"], h.Log.Toasts);
        Assert.Equal(OutboxState.Failed, h.Cache.Loaded("o1")!.Msg!.Summary.Outbox!.State.Value);
        Assert.Equal(2, h.Fixture.CallCount(API.MessageGet.Name));

        // Unknown: nothing.
        await h.Run(() => h.Actions.RetryOutbox("nope"));
        await h.IdleAsync();
        Assert.Equal(2, h.Fixture.CallCount(API.OutboxRetry.Name));
    }

    [Fact]
    public async Task LoadImagesGoesThroughTheCache()
    {
        await using var h = await StartAsync(messages: In(Inbox, Msg("m1", 1, flags: Seen)));
        var blocked = JsonCoding.EncodeToString(Body("m1", "<p>blocked</p>", RemoteContentPolicy.Block));
        var allowed = JsonCoding.EncodeToString(Body("m1", "<p>with pictures</p>", RemoteContentPolicy.Allow));
        Answer(h, API.MessageBody.Name, p => JsonCoding.Decode<MessageBodyParams>(p).RemoteContent?.Value == RemoteContentPolicy.Allow ? allowed : blocked);
        await h.LoadAsync("m1");
        Assert.Equal(2, RemoteBar.LoadableImages(h.Cache.Loaded("m1")!.Body));

        // Unknown: nothing. Known: the bar shows the wait, the body is
        // replaced.
        await h.Run(() =>
        {
            h.Actions.LoadImages("nope");
            h.Actions.LoadImages("m1");
            Assert.True(h.Cache.Loaded("m1")!.LoadingImages);
            Assert.Equal(["m1:true"], h.Log.Bars);
        });
        await h.IdleAsync();
        Assert.Equal("<p>with pictures</p>", h.Cache.Loaded("m1")!.Body!.Html);
        Assert.False(h.Cache.Loaded("m1")!.LoadingImages);
        Assert.Equal(RemoteContentPolicy.Allow, h.Cache.Loaded("m1")!.Body!.RemoteContent.Value);
        Assert.Empty(h.Log.Toasts);
        Assert.Equal(2, h.Fixture.CallCount(API.MessageBody.Name));

        // A failure: the toast, the bar back, the body on display kept.
        Answer(h, API.MessageBody.Name, p => JsonCoding.Decode<MessageBodyParams>(p).RemoteContent?.Value == RemoteContentPolicy.Allow
            ? throw new RpcException(Error(ErrorCode.NetworkError, "no network"))
            : blocked);
        await h.Run(() => h.Actions.LoadImages("m1"));
        await h.IdleAsync();
        Assert.Equal(["Loading the images failed: the server could not be reached"], h.Log.Toasts);
        Assert.Equal(["m1:true", "m1:true", "m1:false"], h.Log.Bars);
        Assert.Equal("<p>with pictures</p>", h.Cache.Loaded("m1")!.Body!.Html);
    }

    [Fact]
    public async Task TrustSenderAddsThenRaisesThePolicyThenLoads()
    {
        var anonymous = Msg("m2", 2, flags: Seen) with { From = [] };
        var blank = Msg("m3", 3, flags: Seen) with { From = [new Address { Name = "x", Email = "  " }] };
        await using var h = await StartAsync(messages: In(Inbox, Msg("m1", 1, flags: Seen), anonymous, blank));
        var gate = new Lock();
        var senders = new List<string>();
        var preferences = new List<Preferences>();
        Answer(h, API.SenderAdd.Name, p =>
        {
            lock (gate)
            {
                senders.Add(JsonCoding.Decode<SenderAddParams>(p).Address);
            }
            return "{}";
        });
        Answer(h, API.ConfigSet.Name, p =>
        {
            var q = JsonCoding.Decode<ConfigSetParams>(p);
            lock (gate)
            {
                preferences.Add(q.Preferences);
            }
            return JsonCoding.EncodeToString(new ConfigSetResult { Preferences = q.Preferences });
        });
        await h.LoadAsync("m1");
        var before = h.Fixture.Calls().Count;

        // No address: nothing at all.
        await h.Run(() =>
        {
            h.Actions.TrustSender("m2");
            h.Actions.TrustSender("m3");
        });
        await h.IdleAsync();
        Assert.Equal(before, h.Fixture.Calls().Count);

        // sender.add, config.get, config.set (the whole set echoed, the
        // policy raised from block), message.body under allow.
        await h.Run(() =>
        {
            h.Actions.TrustSender("m1");
            Assert.True(h.Cache.Loaded("m1")!.LoadingImages);
            Assert.Equal(["m1:true"], h.Log.Bars);
            h.Actions.TrustSender("m1"); // already on its way
        });
        await h.IdleAsync();
        Assert.False(h.Cache.Loaded("m1")!.LoadingImages);
        Assert.Equal([API.SenderAdd.Name, API.ConfigGet.Name, API.ConfigSet.Name, API.MessageBody.Name], h.Fixture.Calls().Skip(before));
        Assert.Equal(["alice@example.invalid"], senders);
        Assert.Equal([new Preferences { SyncIntervalSeconds = 300, RemoteContent = RemoteContentPolicy.KnownSenders, OfflineDays = 30 }], preferences);
        Assert.Equal(RemoteContentPolicy.Allow, h.Cache.Loaded("m1")!.Body!.RemoteContent.Value);
        Assert.Empty(h.Log.Toasts);

        // Already from known senders: config.set is skipped.
        var known = JsonCoding.EncodeToString(new ConfigGetResult
        {
            Preferences = new Preferences { SyncIntervalSeconds = 60, RemoteContent = RemoteContentPolicy.KnownSenders, OfflineDays = 7 },
        });
        Answer(h, API.ConfigGet.Name, _ => known);
        var second = h.Fixture.Calls().Count;
        await h.Run(() => h.Actions.TrustSender("m1"));
        await h.IdleAsync();
        Assert.False(h.Cache.Loaded("m1")!.LoadingImages);
        Assert.Equal([API.SenderAdd.Name, API.ConfigGet.Name, API.MessageBody.Name], h.Fixture.Calls().Skip(second));
        Assert.Single(preferences);

        // A refused sender.add: the bar goes back, nothing else is asked.
        Refuse(h, API.SenderAdd.Name, ErrorCode.StorageError, "disk");
        var third = h.Fixture.Calls().Count;
        await h.Run(() => h.Actions.TrustSender("m1"));
        await h.IdleAsync();
        Assert.Equal(["Trusting the sender failed"], h.Log.Toasts);
        Assert.False(h.Cache.Loaded("m1")!.LoadingImages);
        Assert.Equal([API.SenderAdd.Name], h.Fixture.Calls().Skip(third));
        Assert.Equal("m1:false", h.Log.Bars[^1]);

        // A failed preference change is said, and the images load anyway.
        Answer(h, API.SenderAdd.Name, _ => "{}");
        var block = JsonCoding.EncodeToString(new ConfigGetResult
        {
            Preferences = new Preferences { SyncIntervalSeconds = 300, RemoteContent = RemoteContentPolicy.Block, OfflineDays = 30 },
        });
        Answer(h, API.ConfigGet.Name, _ => block);
        Refuse(h, API.ConfigSet.Name, ErrorCode.InvalidArgument, "bad");
        var fourth = h.Fixture.Calls().Count;
        await h.Run(() => h.Actions.TrustSender("m1"));
        await h.IdleAsync();
        Assert.Equal(2, h.Log.Toasts.Count);
        Assert.Equal("Changing the remote content preference was rejected: bad", h.Log.Toasts[^1]);
        Assert.False(h.Cache.Loaded("m1")!.LoadingImages);
        Assert.Equal([API.SenderAdd.Name, API.ConfigGet.Name, API.ConfigSet.Name, API.MessageBody.Name], h.Fixture.Calls().Skip(fourth));
    }

    [Fact]
    public async Task ReplyComesFromDraftCreateOrThePrefill()
    {
        var original = Msg("m1", 1, flags: Seen) with
        {
            To = [new Address { Email = "me@example.invalid" }, new Address { Name = "bob", Email = "bob@example.invalid" }],
        };
        await using var h = await StartAsync(messages: In(Inbox, original));
        var alice = new Address { Name = "alice", Email = "alice@example.invalid" };
        var draft = new Draft
        {
            AccountId = "a",
            To = [alice],
            Subject = "Re: s-m1",
            TextBody = "quoted",
            HtmlBody = "<p>quoted</p>",
            InReplyTo = "m1",
        };
        var created = JsonCoding.EncodeToString(new DraftCreateResult { Draft = draft, Quoted = QuoteForm.Html, Blocked = new BlockedContent { RemoteImages = 1 } });
        var requests = new List<DraftCreateParams>();
        var held = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        h.Fixture.On(API.DraftCreate.Name, async p =>
        {
            lock (requests)
            {
                requests.Add(JsonCoding.Decode<DraftCreateParams>(p));
            }
            await held.Task;
            return created;
        });

        // The backend's template; a second click while it is prepared does
        // nothing.
        await h.Run(() => h.Actions.OpenCompose(ComposeKind.Reply, "m1"));
        await Eventually.Holds(() => Locked(requests).Count == 1);
        await h.Run(() =>
        {
            h.Actions.OpenCompose(ComposeKind.Reply, "m1");
            h.Actions.OpenCompose(ComposeKind.Reply, "nope");
        });
        held.SetResult();
        await h.IdleAsync();
        var p = Assert.Single(h.Log.Composed);
        Assert.Equal(ComposeKind.Reply, p.Kind);
        Assert.Equal("a", p.AccountId?.Value);
        Assert.Equal([alice], p.To);
        Assert.Equal("Re: s-m1", p.Subject);
        Assert.Equal("<p>quoted</p>", p.BodyHtml);
        Assert.Equal("m1", p.InReplyTo?.Value);
        Assert.Equal(new BlockedContent { RemoteImages = 1 }, p.Blocked);
        var request = Assert.Single(Locked(requests));
        Assert.Equal("a", request.AccountId.Value);
        Assert.Equal(ComposeMode.Reply, request.Mode.Value);
        Assert.Equal("m1", request.MessageId?.Value);
        Assert.EndsWith("alice wrote:", request.Attribution, StringComparison.Ordinal);
        Assert.Empty(h.Log.Toasts);

        // notImplemented: no toast, the UI's own quote from what the pane
        // knows (the body once it is loaded).
        Refuse(h, API.DraftCreate.Name, ErrorCode.NotImplemented, "no");
        await h.LoadAsync("m1");
        await h.Run(() => h.Actions.OpenCompose(ComposeKind.Forward, "m1"));
        await h.IdleAsync();
        Assert.Equal(2, h.Log.Composed.Count);
        var f = h.Log.Composed[1];
        Assert.Equal(ComposeKind.Forward, f.Kind);
        Assert.Equal("a", f.AccountId?.Value);
        Assert.Equal("Fwd: s-m1", f.Subject);
        Assert.Equal("m1", f.Forwarding?.Value);
        Assert.Null(f.InReplyTo);
        Assert.Contains("---------- Forwarded message ----------", f.BodyHtml, StringComparison.Ordinal);
        Assert.EndsWith("body of m1", f.BodyHtml, StringComparison.Ordinal);
        Assert.Empty(h.Log.Toasts);

        // Another failure is said; the fallback is the same. Reply all
        // leaves the account's own address out of the Cc.
        Refuse(h, API.DraftCreate.Name, ErrorCode.ServerError, "500");
        await h.Run(() => h.Actions.OpenCompose(ComposeKind.ReplyAll, "m1"));
        await h.IdleAsync();
        Assert.Equal(["Preparing the reply failed: the server returned an error"], h.Log.Toasts);
        var r = h.Log.Composed[2];
        Assert.Equal(ComposeKind.ReplyAll, r.Kind);
        Assert.Equal("Re: s-m1", r.Subject);
        Assert.Equal([alice], r.To);
        Assert.Equal([new Address { Name = "bob", Email = "bob@example.invalid" }], r.Cc);
        Assert.Equal("m1", r.InReplyTo?.Value);
        Assert.Contains("<blockquote type=\"cite\">body of m1</blockquote>", r.BodyHtml, StringComparison.Ordinal);
        Assert.Contains("alice wrote:", r.BodyHtml, StringComparison.Ordinal);
    }

    [Fact]
    public async Task FlagsFollowTheRowAndTheMessage()
    {
        var queued = Msg("o1", 5) with { Outbox = new OutboxInfo { State = OutboxState.Queued, Attempts = 0 } };
        await using var h = await StartAsync(messages: new Dictionary<FolderKey, MessageSummary[]>
        {
            [Inbox] = [Msg("m1", 1), Msg("m2", 2, flags: [Flag.Seen, Flag.Flagged])],
            [OutboxFolder] = [queued],
        });
        Assert.Equal(ActionFlags.None, h.Actions.ActionFlagsFor(null));
        var unread = h.Actions.ActionFlagsFor(h.List.Rows[1]);
        Assert.True(unread.On && unread.MarkRead && !unread.MarkUnread && !unread.Flagged && unread.Archive && unread.Junk && unread.Trash);
        var read = h.Actions.FlagsFor(h.Summary("m2"));
        Assert.True(read.On && !read.MarkRead && read.MarkUnread && read.Flagged && read.Star && read.TrustSender);
        await h.SelectAsync(OutboxFolder);
        var sending = h.Actions.FlagsFor(h.Summary("o1"));
        Assert.True(sending.On && sending.Outbox && sending.Trash && sending.Reply && sending.LoadImages);
        Assert.True(!sending.Star && !sending.Archive && !sending.Junk && !sending.MarkRead && !sending.MarkUnread && !sending.TrustSender);
        Assert.Equal(sending, h.Actions.ActionFlagsFor(h.List.Rows[0]));

        // An outbox message takes no flags and no moves; trash cancels.
        await h.Run(() =>
        {
            h.Actions.SetSeen(["o1"], true);
            h.Actions.SetFlagged(["o1"], true);
            h.Actions.Archive(["o1"]);
        });
        await h.IdleAsync();
        Assert.Empty(h.Fixture.FlagRequests);
        Assert.Empty(h.Fixture.MoveRequests);
        Assert.Empty(h.Log.Toasts);
        Assert.Equal(["o1"], Ids(h.List.Rows));
    }

    [Fact]
    public async Task DestructiveActionsAreRefusedWithoutAConfirmationHook()
    {
        await using var h = await StartAsync(messages: In(Inbox, Msg("m1", 1)));
        await h.Run(() =>
        {
            h.Actions.Confirm = null;
            h.Actions.Trash(["m1"], "s-m1");
            h.Actions.Junk("m1");
        });
        await h.IdleAsync();
        Assert.Single(h.List.Rows);
        Assert.Empty(h.Fixture.DeleteRequests);
        Assert.Empty(h.Fixture.MoveRequests);
        Assert.Empty(h.Log.Toasts);
    }

    /// <summary>
    /// drafts.go <c>openDraft</c>: draft.open's draft opens in an edit window
    /// (once, however often asked while it runs), a window already editing
    /// it comes to the front instead, an old daemon shows the message, and a
    /// failure is said.
    /// </summary>
    [Fact]
    public async Task DraftOpensForEditing()
    {
        var d1 = Msg("d1", 1, flags: Seen) with { FolderId = Drafts.Folder };
        await using var h = await StartAsync(
            folders: [.. TestFolders(), MailModelTests.TestFolder("dr", "Drafts", FolderRole.Drafts)],
            messages: In(Drafts, d1));
        await h.SelectAsync(Drafts);
        var saved = new Draft
        {
            Id = "d_1",
            AccountId = "a",
            Version = 3,
            Subject = "s-d1",
            TextBody = "x",
            Attachments = [new DraftAttachment { Id = "att_1", Filename = "a.pdf", ContentType = "application/pdf", Size = 1, Inline = false }],
        };
        var opened = JsonCoding.EncodeToString(new DraftOpenResult
        {
            Draft = saved,
            Skipped = [new Attachment { PartId = "3", Filename = "big.iso", ContentType = "application/octet-stream", Size = 1, Inline = false }],
        });
        var requests = new List<DraftOpenParams>();
        var held = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        h.Fixture.On(API.DraftOpen.Name, async p =>
        {
            lock (requests)
            {
                requests.Add(JsonCoding.Decode<DraftOpenParams>(p));
            }
            await held.Task;
            return opened;
        });

        // Swift first activates the row through the list controller, which
        // hands a message of the Drafts folder to onActivateDraft rather
        // than to a message window. The list controller is not part of the
        // actions' seam (IActionsList); what its activation goes by is the
        // model's inDrafts.
        Assert.True(h.Mailbox.Model.InDrafts(h.Summary("d1")));

        await h.Run(() => h.Actions.OpenDraft("d1"));
        await Eventually.Holds(() => Locked(requests).Count == 1);
        await h.Run(() => h.Actions.OpenDraft("d1"));
        held.SetResult();
        await h.IdleAsync();
        var p = Assert.Single(h.Log.Composed);
        Assert.True(p.Kind == ComposeKind.Edit && p.DraftId?.Value == "d_1" && p.Version == 3 && p.Subject == "s-d1" && p.Attachments.Count == 1);
        Assert.Single(h.Log.Raised);
        Assert.Equal(["1 attachment of the draft could not be opened"], h.Log.Toasts);
        var request = Assert.Single(Locked(requests));
        Assert.Equal(("a", "d1"), (request.AccountId.Value, request.MessageId.Value));
        await h.IdleAsync();
        Assert.Single(h.Log.Composed);

        // A window already editing it is raised instead.
        h.Log.Raise = true;
        await h.Run(() => h.Actions.OpenDraft("d1"));
        await h.IdleAsync();
        Assert.Equal(2, h.Log.Raised.Count);
        Assert.Single(h.Log.Composed);

        // A daemon without draft.open shows the message.
        Refuse(h, API.DraftOpen.Name, ErrorCode.MethodNotFound, "no");
        await h.Run(() => h.Actions.OpenDraft("d1"));
        await h.IdleAsync();
        Assert.Equal(["d1"], IdsOf(h.Log.MessageWindows));

        // Not downloaded yet: said, nothing opens.
        Refuse(h, API.DraftOpen.Name, ErrorCode.Unavailable, "later");
        await h.Run(() => h.Actions.OpenDraft("d1"));
        await h.IdleAsync();
        Assert.Equal(2, h.Log.Toasts.Count);
        Assert.Equal("The draft has not been downloaded yet; try again in a moment", h.Log.Toasts[^1]);
        Assert.Single(h.Log.Composed);
    }

    // Helpers

    private static Dictionary<FolderKey, MessageSummary[]> In(FolderKey k, params MessageSummary[] list) => new() { [k] = list };

    private static string[] IdsOf(IEnumerable<MessageId> ids) => [.. ids.Select(id => id.Value)];

    private static RpcError Error(int code, string message) => new() { Code = code, Message = message };

    private static List<T> Locked<T>(List<T> list)
    {
        lock (list)
        {
            return [.. list];
        }
    }

    /// <summary>A scripted answer for <paramref name="method"/>, replacing the fixture's.</summary>
    private static void Answer(ActionsControllerHarness h, string method, Func<string, string> answer) =>
        h.Fixture.Daemon.On(method, answer);

    /// <summary>A scripted refusal for <paramref name="method"/>, replacing the fixture's.</summary>
    private static void Refuse(ActionsControllerHarness h, string method, int code, string message) =>
        h.Fixture.Daemon.On(method, (Func<string, string>)(_ => throw new RpcException(Error(code, message))));

    private static void AssertFlagRequest(MessageFlagParams p, string[] ids, IReadOnlyList<Flag>? set, IReadOnlyList<Flag>? clear)
    {
        Assert.Equal("a", p.AccountId.Value);
        Assert.Equal(ids, IdsOf(p.MessageIds));
        Assert.Equal(set, p.Set);
        Assert.Equal(clear, p.Clear);
    }

    private static void AssertDeleteRequest(MessageDeleteParams p, string[] ids)
    {
        Assert.Equal("a", p.AccountId.Value);
        Assert.Equal(ids, IdsOf(p.MessageIds));
        Assert.Null(p.Permanent);
    }

    private static void AssertMoveRequest(MessageMoveParams p, string[] ids, string target)
    {
        Assert.Equal("a", p.AccountId.Value);
        Assert.Equal(ids, IdsOf(p.MessageIds));
        Assert.Equal(target, p.TargetFolderId.Value);
    }
}
