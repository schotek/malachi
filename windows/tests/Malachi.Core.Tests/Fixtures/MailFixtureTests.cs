// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only tests of the MailFixture port: the controller tests of wave
// 3 rely on it answering as the daemon does (docs/api.md §4), so its rules
// are checked here over a real connection: paging, filters, counters that
// follow flag changes, moves and deletes, thread aggregates, failures,
// delays on a fake clock and pushed notifications. Swift tests the fixture
// only through its controller tests.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Transport;
using Microsoft.Extensions.Time.Testing;
using Xunit;

namespace Malachi.Core.Tests.Fixtures;

public sealed class MailFixtureTests
{
    private static readonly AccountId Acc = "acc1";
    private static readonly DateTimeOffset T0 = new(2026, 9, 1, 8, 0, 0, TimeSpan.Zero);

    private static CancellationToken Ct => TestContext.Current.CancellationToken;

    [Fact]
    public async Task ServesAccountsFoldersAndStates()
    {
        await using var f = await StartAsync();
        using var c = await ConnectAsync(f);
        Assert.Equal("fake", (await c.CallAsync(API.SystemInfo, new EmptyParams(), Ct)).Version);
        Assert.Equal([Acc], (await c.CallAsync(API.AccountList, new EmptyParams(), Ct)).Accounts.Select(a => a.Id));
        var folders = (await c.CallAsync(API.FolderList, new FolderListParams { AccountId = Acc }, Ct)).Folders;
        Assert.Equal(["inbox", "trash", "outbox"], folders.Select(x => x.Id.Value));
        Assert.Equal((3, 2), (folders[0].Total, folders[0].Unread));
        var unknown = await Assert.ThrowsAsync<RpcException>(() => c.CallAsync(API.FolderList, new FolderListParams { AccountId = "nope" }, Ct));
        Assert.Equal(ErrorCode.AccountNotFound, unknown.Code.Value);
        Assert.Single((await c.CallAsync(API.SyncStatus, new SyncStatusParams(), Ct)).Accounts);
        await c.CallAsync(API.SyncTrigger, new SyncTriggerParams { AccountId = Acc }, Ct);
        Assert.Equal(Acc, Assert.Single(f.Triggers).AccountId);
        Assert.Equal(300, (await c.CallAsync(API.ConfigGet, new EmptyParams(), Ct)).Preferences.SyncIntervalSeconds);
        Assert.Equal(["system.info", "account.list", "folder.list", "folder.list", "sync.status", "sync.trigger", "config.get"], f.Served);
    }

    [Fact]
    public async Task ListsPagesAndFilters()
    {
        await using var f = await StartAsync();
        using var c = await ConnectAsync(f);
        var first = await c.CallAsync(API.MessageList, new MessageListParams { AccountId = Acc, FolderId = "inbox", Page = new Page { Limit = 2 } }, Ct);
        Assert.Equal(["m3", "m2"], first.Messages.Select(m => m.Id.Value)); // newest first
        Assert.Equal(new PageInfo { NextCursor = "2", Total = 3 }, first.Page);
        var rest = await c.CallAsync(API.MessageList, new MessageListParams { AccountId = Acc, FolderId = "inbox", Page = new Page { Cursor = "2" } }, Ct);
        Assert.Equal(["m1"], rest.Messages.Select(m => m.Id.Value));
        Assert.Null(rest.Page.NextCursor);
        var unread = await c.CallAsync(API.MessageList, new MessageListParams { AccountId = Acc, FolderId = "inbox", Filter = MessageFilter.Unread, Sort = SortOrder.DateAsc }, Ct);
        Assert.Equal(["m2", "m3"], unread.Messages.Select(m => m.Id.Value));
        Assert.Equal(3, f.ListRequests.Count);
        Assert.Equal("body of m1", (await c.CallAsync(API.MessageBody, new MessageBodyParams { AccountId = Acc, MessageId = "m1" }, Ct)).Text);
        Assert.Equal("m1", (await c.CallAsync(API.MessageGet, new MessageGetParams { AccountId = Acc, MessageId = "m1" }, Ct)).Message.Summary.Id.Value);
        var gone = await Assert.ThrowsAsync<RpcException>(() => c.CallAsync(API.MessageGet, new MessageGetParams { AccountId = Acc, MessageId = "zz" }, Ct));
        Assert.Equal(ErrorCode.MessageNotFound, gone.Code.Value);
    }

    [Fact]
    public async Task CountersFollowFlagsMovesAndDeletes()
    {
        await using var f = await StartAsync();
        using var c = await ConnectAsync(f);
        await c.CallAsync(API.MessageFlag, new MessageFlagParams { AccountId = Acc, MessageIds = ["m2", "m3"], Set = [Flag.Seen] }, Ct);
        Assert.Equal(0, f.Folders(Acc)[0].Unread);
        Assert.Equal([Flag.Seen], f.Message("m2")!.Flags);
        var bad = await Assert.ThrowsAsync<RpcException>(() =>
            c.CallAsync(API.MessageFlag, new MessageFlagParams { AccountId = Acc, MessageIds = ["m1"], Set = [Flag.Deleted] }, Ct));
        Assert.Equal(ErrorCode.InvalidArgument, bad.Code.Value);

        await c.CallAsync(API.MessageMove, new MessageMoveParams { AccountId = Acc, MessageIds = ["m1"], TargetFolderId = "trash" }, Ct);
        Assert.Equal((2, 1), (f.Folders(Acc)[0].Total, f.Folders(Acc)[1].Total));
        Assert.Equal("trash", f.Message("m1")!.FolderId.Value);

        // To the trash, then out of the store from the trash.
        await c.CallAsync(API.MessageDelete, new MessageDeleteParams { AccountId = Acc, MessageIds = ["m2"] }, Ct);
        Assert.Equal("trash", f.Message("m2")!.FolderId.Value);
        await c.CallAsync(API.MessageDelete, new MessageDeleteParams { AccountId = Acc, MessageIds = ["m1", "m2"] }, Ct);
        Assert.Null(f.Message("m1"));
        Assert.Empty(f.Messages(Acc, "trash"));
        Assert.Equal((2, 1, 2), (f.FlagRequests.Count, f.MoveRequests.Count, f.DeleteRequests.Count)); // refused requests are recorded too
    }

    [Fact]
    public async Task AggregatesThreads()
    {
        await using var f = await StartAsync();
        f.AddMessage(Summary("m4", "inbox", T0.AddHours(4), thread: "t1", subject: "Re: Plans", seen: true) with { From = [new Address { Email = "b@example.org" }] });
        using var c = await ConnectAsync(f);
        var list = await c.CallAsync(API.ThreadList, new ThreadListParams { AccountId = Acc, FolderId = "inbox" }, Ct);
        Assert.Equal(["t1", "unlinked:m3", "unlinked:m2"], list.Threads.Select(t => t.Id.Value));
        var t1 = list.Threads[0];
        Assert.Equal(("Plans", 2, 0, "m4"), (t1.Subject, t1.MessageCount, t1.UnreadCount, t1.Latest.Id.Value));
        Assert.Equal(["b@example.org", "a@example.org"], t1.Participants.Select(p => p.Email));
        var unread = await c.CallAsync(API.ThreadList, new ThreadListParams { AccountId = Acc, FolderId = "inbox", Filter = MessageFilter.Unread }, Ct);
        Assert.DoesNotContain(unread.Threads, t => t.Id.Value == "t1");
        var got = await c.CallAsync(API.ThreadGet, new ThreadGetParams { AccountId = Acc, ThreadId = "t1", FolderId = "inbox" }, Ct);
        Assert.Equal(["m1", "m4"], got.Messages.Select(m => m.Id.Value)); // oldest first
        var none = await Assert.ThrowsAsync<RpcException>(() => c.CallAsync(API.ThreadGet, new ThreadGetParams { AccountId = Acc, ThreadId = "t9" }, Ct));
        Assert.Equal(ErrorCode.ThreadNotFound, none.Code.Value);
        Assert.Single(f.ThreadListRequests, r => r.Filter is null);
        Assert.Equal(2, f.ThreadGetRequests.Count);
    }

    [Fact]
    public async Task FailsDelaysAndPushesOnRequest()
    {
        var time = new FakeTimeProvider();
        await using var f = await StartAsync(time);
        using var c = await ConnectAsync(f);
        f.Fail(API.AccountList.Name, new RpcError { Code = ErrorCode.Unavailable, Message = "down" });
        Assert.Equal(ErrorCode.Unavailable, (await Assert.ThrowsAsync<RpcException>(() => c.CallAsync(API.AccountList, new EmptyParams(), Ct))).Code.Value);
        f.Succeed(API.AccountList.Name);
        await c.CallAsync(API.AccountList, new EmptyParams(), Ct);

        f.Delay(API.ConfigGet.Name, TimeSpan.FromSeconds(3));
        var delayed = c.CallAsync(API.ConfigGet, new EmptyParams(), TimeSpan.FromMinutes(1), Ct);
        await Eventually.Holds(() => f.Served.Contains(API.ConfigGet.Name));
        Assert.Equal(1, f.Daemon.InFlight);
        Assert.False(delayed.IsCompleted);
        time.Advance(TimeSpan.FromSeconds(3));
        await delayed;
        await f.Daemon.IdleAsync();

        f.On("test.custom", _ => Task.FromResult("{}"));
        Assert.Equal(1, f.CallCount(API.ConfigGet.Name));

        await f.PushAsync(new DaemonNotification.SyncState(new SyncState { AccountId = Acc, Status = SyncStatus.Syncing }));
        await f.PushAsync(new DaemonNotification.AccountsChanged());
        var first = (await c.Notifications.ReadAsync(Ct)).Decode();
        Assert.Equal(SyncStatus.Syncing, Assert.IsType<DaemonNotification.SyncState>(first).State.Status.Value);
        Assert.IsType<DaemonNotification.AccountsChanged>((await c.Notifications.ReadAsync(Ct)).Decode());
        Assert.Equal(2, f.Daemon.Pushed);
    }

    private static async Task<MailFixture> StartAsync(TimeProvider? time = null)
    {
        var f = new MailFixture(time);
        f.SetAccounts([new Account
        {
            Id = Acc,
            Config = new AccountConfig { Name = "Work", Email = "me@example.org" },
            Enabled = true,
            State = new SyncState { AccountId = Acc, Status = SyncStatus.Idle },
        }]);
        f.SetFolders([Folder("inbox", FolderRole.Inbox), Folder("trash", FolderRole.Trash), Folder("outbox", FolderRole.Outbox)], Acc);
        f.SetSyncStates([new SyncState { AccountId = Acc, Status = SyncStatus.Idle }]);
        f.SetMessages(
            [
                Summary("m1", "inbox", T0, thread: "t1", subject: "Plans", seen: true),
                Summary("m2", "inbox", T0.AddHours(1)),
                Summary("m3", "inbox", T0.AddHours(2)),
            ],
            Acc,
            "inbox");
        await f.StartAsync();
        return f;
    }

    private static async Task<RpcClient> ConnectAsync(MailFixture f)
    {
        var c = new RpcClient(f.Path, PortableKeyFilePolicy.Instance);
        await c.ConnectAsync(Ct);
        return c;
    }

    private static Folder Folder(string id, string role) => new()
    {
        Id = id,
        AccountId = Acc,
        Name = id,
        Path = id,
        Role = role,
        Subscribed = true,
        Selectable = true,
        Synced = true,
        Unread = 0,
        Total = 0,
    };

    private static MessageSummary Summary(string id, string folder, DateTimeOffset date, string? thread = null, string subject = "Hello", bool seen = false) => new()
    {
        Id = id,
        AccountId = Acc,
        FolderId = folder,
        ThreadId = thread is null ? (ThreadId?)null : new ThreadId(thread),
        From = [new Address { Email = "a@example.org" }],
        Subject = subject,
        Date = date,
        Snippet = "…",
        Flags = seen ? [Flag.Seen] : [],
        HasAttachments = false,
        Size = 100,
    };
}
