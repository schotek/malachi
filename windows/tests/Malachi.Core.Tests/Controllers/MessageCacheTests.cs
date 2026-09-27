// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/MessageCacheTests.swift: the
// loaded-message cache over a FakeDaemon (ui/internal/window/
// message_view.go fetchMessage/settleLoaded, remote.go loadRemoteImages,
// outbox.go refetchMessage). Swift delays the daemon's answers by 60 ms to
// catch a request in flight; here the answers are held until the test lets
// them go, and the tests wait for quiescence instead of polling. Added:
// AFastFailingCallReleasesTheWaiters, the trap of docs/windows-port.md §7.2
// (a call that fails before its first await must not settle the entry
// while its other half has not started), and AThrowingViewStrandsNothing,
// a handler or waiter that throws, which a Swift callback cannot.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Tests.Api;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Xunit;

namespace Malachi.Core.Tests.Controllers;

public sealed class MessageCacheTests
{
    private static readonly AccountId Account = "acc_1";
    private static readonly FolderId Folder = "fld_1";

    private static CancellationToken Ct => TestContext.Current.CancellationToken;

    [Fact]
    public async Task FetchRunsBothHalvesOnceAndAnswersEveryWaiter()
    {
        await using var h = await Harness.CreateAsync();
        h.Serve("m1", held: true);
        await h.StartAsync();
        var s = Summary("m1");
        var first = new List<bool>();
        var second = new List<bool>();
        await h.Ui.RunAsync(() =>
        {
            h.Cache.Fetch(s, lm => first.Add(lm.Complete));
            // A second request while the first runs joins it.
            h.Cache.Fetch(s, lm => second.Add(lm.Complete));
            Assert.True(h.Cache.Loaded(s.Id)?.Getting);
            Assert.True(h.Cache.Loaded(s.Id)?.Fetching);
        });

        h.Release();
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.True(h.Cache.Loaded(s.Id)?.Complete);
            // Once per half, for both waiters: the first answer is a partial
            // entry, the second the complete one.
            Assert.Equal([false, true], first);
            Assert.Equal([false, true], second);
            var lm = h.Cache.Loaded(s.Id)!;
            Assert.Equal("Hello m1", lm.Msg?.Summary.Subject);
            Assert.Equal("cc@example.com", lm.Msg?.Cc?[0].Email);
            Assert.Equal("text of m1", lm.Body?.Text);
            Assert.Null(lm.Err);
            Assert.Equal([s.Id, s.Id], h.Log.Loaded);
        });
        Assert.Equal(1, h.Calls(API.MessageGet.Name));
        Assert.Equal(1, h.Calls(API.MessageBody.Name));

        // A complete entry answers at once and asks the daemon nothing.
        var third = 0;
        await h.Ui.RunAsync(() =>
        {
            h.Cache.Fetch(s, _ => third++);
            Assert.Equal(1, third);
        });
        await h.IdleAsync();
        Assert.Equal(1, h.Calls(API.MessageGet.Name));
        Assert.Equal(1, h.Calls(API.MessageBody.Name));
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(2, h.Log.Loaded.Count);
            Assert.Empty(h.Log.Toasts);
        });
    }

    [Fact]
    public async Task BodyFailureIsRetriedOnTheNextFetch()
    {
        await using var h = await Harness.CreateAsync();
        h.Serve("m2", bodyFails: true);
        await h.StartAsync();
        var s = Summary("m2");
        var seen = new List<bool>();
        await h.Ui.RunAsync(() => h.Cache.Fetch(s, lm => seen.Add(lm.BodySettled)));
        await h.IdleAsync();
        var lm = await h.Ui.RunAsync(() => h.Cache.Loaded(s.Id)!);
        await h.Ui.RunAsync(() =>
        {
            Assert.True(lm.BodySettled && lm.Msg is not null);
            Assert.Null(lm.Body);
            Assert.Equal(ErrorCode.InternalError, Assert.IsType<RpcException>(lm.Err).Code.Value);
            Assert.False(lm.Complete);
            // The failure is the pane's to show, not a toast.
            Assert.Empty(h.Log.Toasts);
        });

        // The next fetch retries the body and keeps the headers.
        h.Serve("m2");
        var again = new List<bool>();
        await h.Ui.RunAsync(() =>
        {
            h.Cache.Fetch(s, l => again.Add(l.Complete));
            Assert.Null(lm.Err); // cleared before the retry
            Assert.True(lm.Fetching);
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.True(lm.Complete);
            Assert.Equal([true], again);
            Assert.Equal("text of m2", lm.Body?.Text);
        });
        Assert.Equal(1, h.Calls(API.MessageGet.Name));
        Assert.Equal(2, h.Calls(API.MessageBody.Name));
    }

    [Fact]
    public async Task GetFailureKeepsTheBodyAndRetriesTheHeaders()
    {
        await using var h = await Harness.CreateAsync();
        h.Serve("m3", getFails: true);
        await h.StartAsync();
        var s = Summary("m3");
        var answers = 0;
        await h.Ui.RunAsync(() => h.Cache.Fetch(s, _ => answers++));
        await h.IdleAsync();
        var lm = await h.Ui.RunAsync(() => h.Cache.Loaded(s.Id)!);
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(2, answers);
            Assert.Null(lm.Msg);
            Assert.Equal("text of m3", lm.Body?.Text);
            Assert.Null(lm.Err);
            Assert.True(!lm.Getting && !lm.Fetching);
        });

        h.Serve("m3");
        await h.Ui.RunAsync(() => h.Cache.Fetch(s, _ => answers++));
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.True(lm.Complete);
            Assert.Equal(3, answers);
        });
        Assert.Equal(2, h.Calls(API.MessageGet.Name));
        Assert.Equal(1, h.Calls(API.MessageBody.Name));
    }

    [Fact]
    public async Task EvictedEntryIsStoredBackWhenItsHalfArrives()
    {
        await using var h = await Harness.CreateAsync();
        h.Serve("m4", held: true);
        await h.StartAsync();
        var s = Summary("m4");
        var answers = 0;
        var inFlight = await h.Ui.RunAsync(() =>
        {
            h.Cache.Fetch(s, _ => answers++);
            var lm = h.Cache.Loaded(s.Id)!;
            h.Cache.Evict(s.Id);
            Assert.Null(h.Cache.Loaded(s.Id));
            return lm;
        });

        h.Release();
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(2, answers);
            // The same entry, complete, is back in the cache.
            Assert.Same(inFlight, h.Cache.Loaded(s.Id));
            Assert.True(inFlight.Complete);
            Assert.Equal(1, h.Cache.Cache.Count);
        });
    }

    [Fact]
    public async Task LoadImagesReplacesTheBodyAndShowsTheWait()
    {
        await using var h = await Harness.CreateAsync();
        h.Serve("m5", html: "<p>blocked</p>", allowHtml: "<p>with pictures</p>");
        await h.StartAsync();
        var s = Summary("m5");
        await h.Ui.RunAsync(() => h.Cache.Fetch(s, _ => { }));
        await h.IdleAsync();
        var lm = await h.Ui.RunAsync(() => h.Cache.Loaded(s.Id)!);
        await h.Ui.RunAsync(() =>
        {
            Assert.True(lm.Complete);
            Assert.Equal(2, RemoteBar.LoadableImages(lm.Body));
        });

        Outcome<LoadedMessage>? outcome = null;
        await h.Ui.RunAsync(() =>
        {
            h.Cache.LoadImages(s, o => outcome = o);
            // The bar shows the wait from the click on.
            Assert.True(lm.LoadingImages);
            Assert.Equal([true], h.Log.Bars.Select(b => b.Loading));
            // A second click while the daemon works is ignored.
            h.Cache.LoadImages(s, _ => Assert.Fail("a second request was started"));
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.True(outcome?.IsSuccess, "loadImages failed");
            Assert.Same(lm, outcome!.Value.Value);
            Assert.False(lm.LoadingImages);
            Assert.Equal("<p>with pictures</p>", lm.Body?.Html);
            Assert.Equal(RemoteContentPolicy.Allow, lm.Body?.RemoteContent.Value);
            Assert.Null(lm.Err);
            Assert.Equal(0, RemoteBar.LoadableImages(lm.Body));
            // Two settles of the fetch, then the images.
            Assert.Equal([s.Id, s.Id, s.Id], h.Log.Loaded);
            Assert.Single(h.Log.Bars);
            Assert.Empty(h.Log.Toasts);
        });
        Assert.Equal(2, h.Calls(API.MessageBody.Name));
    }

    [Fact]
    public async Task LoadImagesFailureToastsAndOffersTheImagesAgain()
    {
        await using var h = await Harness.CreateAsync();
        h.Serve("m6", html: "<p>blocked</p>", allowFails: true);
        await h.StartAsync();
        var s = Summary("m6");
        await h.Ui.RunAsync(() => h.Cache.Fetch(s, _ => { }));
        await h.IdleAsync();
        var lm = await h.Ui.RunAsync(() => h.Cache.Loaded(s.Id)!);

        Outcome<LoadedMessage>? outcome = null;
        await h.Ui.RunAsync(() => h.Cache.LoadImages(s, o => outcome = o));
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.False(outcome?.IsSuccess ?? true, "loadImages succeeded");
            var err = outcome!.Value.Error!;
            Assert.Equal(ErrorCode.NetworkError, Assert.IsType<RpcException>(err).Code.Value);
            Assert.Equal([RpcErrorText.Text(L10n.T("Loading the images"), err)], h.Log.Toasts);
            // The body on display stays, the flag is cleared and the bar was
            // redrawn on the way in and on the way out.
            Assert.Equal("<p>blocked</p>", lm.Body?.Html);
            Assert.False(lm.LoadingImages);
            Assert.Equal([true, false], h.Log.Bars.Select(b => b.Loading));
            Assert.Equal(2, h.Log.Loaded.Count);
        });
    }

    [Fact]
    public async Task RefetchDropsTheHeadersAndKeepsTheBody()
    {
        await using var h = await Harness.CreateAsync();
        h.Serve("m7");
        await h.StartAsync();
        var s = Summary("m7");
        await h.Ui.RunAsync(() => h.Cache.Fetch(s, _ => { }));
        await h.IdleAsync();
        var lm = await h.Ui.RunAsync(() => h.Cache.Loaded(s.Id)!);
        var body = lm.Body;
        Assert.True(lm.Complete);

        var answers = new List<bool>();
        await h.Ui.RunAsync(() =>
        {
            h.Cache.Refetch(s, l => answers.Add(l.Complete));
            Assert.Null(lm.Msg);
            Assert.True(lm.Getting);
            Assert.Same(body, lm.Body); // untouched
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.True(lm.Complete);
            Assert.Equal([true], answers);
        });
        Assert.Equal(2, h.Calls(API.MessageGet.Name));
        Assert.Equal(1, h.Calls(API.MessageBody.Name));

        // By id: only for a message whose full message is cached.
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal("Hello m7", h.Cache.Summary(s.Id)?.Subject);
            h.Cache.Refetch(new MessageId("unknown"), _ => Assert.Fail("refetched an unknown message"));
            h.Cache.Refetch(s.Id, _ => { });
        });
        await h.IdleAsync();
        Assert.True(await h.Ui.RunAsync(() => lm.Complete));
        Assert.Equal(3, h.Calls(API.MessageGet.Name));
    }

    [Fact]
    public async Task PartsAndAttachedMessagesComeThrough()
    {
        await using var h = await Harness.CreateAsync();
        var part = new MessagePartResult { PartId = "2", ContentType = "image/png; x=y", Filename = "a.png", Size = 3, Data = [1, 2, 3] };
        var embedded = new MessageEmbeddedResult { PartId = "3", Message = Message("inner"), Body = Body("inner") };
        var partJson = JsonCoding.EncodeToString(part);
        var embeddedJson = JsonCoding.EncodeToString(embedded);
        h.Daemon.On(API.MessagePart.Name, _ => partJson);
        h.Daemon.On(API.MessageEmbedded.Name, p =>
        {
            var q = JsonCoding.Decode<MessageEmbeddedParams>(p);
            if (q.PartId != "3" || q.RemoteContent?.Value != RemoteContentPolicy.Allow)
            {
                throw new RpcException(new RpcError { Code = ErrorCode.InvalidArgument, Message = "unexpected params" });
            }
            return embeddedJson;
        });
        await h.StartAsync();

        var got = await h.Ui.InvokeAsync(() => h.Cache.FetchPartAsync(Account, "m8", "2", Ct));
        Assert.Equal("image/png; x=y", got.ContentType);
        Assert.Equal([1, 2, 3], got.Data);
        var res = await h.Ui.InvokeAsync(() => h.Cache.FetchEmbeddedAsync(Account, "m8", "3", RemoteContentPolicy.Allow, Ct));
        ApiJson.AssertSameValue(embedded, res);
    }

    /// <summary>
    /// docs/windows-port.md §7.2: without a connection both calls fail before
    /// their first await. Fetch starts the second half before the first one
    /// can settle, so the first answer keeps the waiters for the second, and
    /// every waiter hears both; then they are released, and a later fetch
    /// has waiters of its own.
    /// </summary>
    [Fact]
    public async Task AFastFailingCallReleasesTheWaiters()
    {
        await using var h = await Harness.CreateAsync(); // never connected
        var s = Summary("m9");
        var first = new List<bool>();
        var second = new List<bool>();
        await h.Ui.RunAsync(() =>
        {
            h.Cache.Fetch(s, lm => first.Add(lm.BodySettled));
            h.Cache.Fetch(s, lm => second.Add(lm.BodySettled));
        });
        await h.IdleAsync();
        var lm = await h.Ui.RunAsync(() => h.Cache.Loaded(s.Id)!);
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal([false, true], first);
            Assert.Equal([false, true], second);
            Assert.Equal(ClientError.NotConnected, Assert.IsType<RpcClientException>(lm.Err).Error);
            Assert.True(!lm.Getting && !lm.Fetching && !lm.Complete);
            Assert.Equal([s.Id, s.Id], h.Log.Loaded);
        });

        // The next fetch retries both halves; the earlier waiters are gone.
        var third = new List<bool>();
        await h.Ui.RunAsync(() => h.Cache.Fetch(s, l => third.Add(l.BodySettled)));
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal([false, true], third);
            Assert.Equal(2, first.Count);
            Assert.Equal(2, second.Count);
        });
        Assert.Empty(h.Daemon.Calls);
    }

    /// <summary>
    /// A view whose handlers or waiter throw is reported and strands
    /// nothing: the other waiters and views hear every settle, and the
    /// images request it came with still runs, ends and answers.
    /// </summary>
    [Fact]
    public async Task AThrowingViewStrandsNothing()
    {
        await using var h = await Harness.CreateAsync();
        h.Serve("m10", html: "<p>blocked</p>", allowHtml: "<p>with pictures</p>");
        await h.StartAsync();
        var s = Summary("m10");
        var boom = new InvalidOperationException("a broken view");
        var heard = new List<bool>();
        await h.Ui.RunAsync(() =>
        {
            h.Cache.Fetch(s, _ => throw boom);
            h.Cache.Fetch(s, l => heard.Add(l.Complete));
        });
        var failed = await Assert.ThrowsAsync<AggregateException>(h.IdleAsync);
        Assert.Equal(2, failed.InnerExceptions.Count);
        Assert.All(failed.InnerExceptions, e => Assert.Same(boom, e));
        var lm = await h.Ui.RunAsync(() => h.Cache.Loaded(s.Id)!);
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal([false, true], heard);
            Assert.Equal([s.Id, s.Id], h.Log.Loaded);
        });

        Outcome<LoadedMessage>? outcome = null;
        await h.Ui.RunAsync(() =>
        {
            h.Cache.RemoteBarChanged += (_, _) => throw boom;
            h.Cache.MessageLoaded += (_, _) => throw boom;
            h.Cache.LoadImages(s, o => outcome = o);
        });
        failed = await Assert.ThrowsAsync<AggregateException>(h.IdleAsync);
        Assert.Equal(2, failed.InnerExceptions.Count);
        Assert.All(failed.InnerExceptions, e => Assert.Same(boom, e));
        await h.Ui.RunAsync(() =>
        {
            Assert.True(outcome?.IsSuccess, "loadImages failed");
            Assert.False(lm.LoadingImages);
            Assert.Equal("<p>with pictures</p>", lm.Body?.Html);
            // The views before the broken ones heard the bar and the images.
            Assert.Equal([true], h.Log.Bars.Select(b => b.Loading));
            Assert.Equal([s.Id, s.Id, s.Id], h.Log.Loaded);
        });
    }

    private static MessageSummary Summary(string id) => new()
    {
        Id = id,
        AccountId = Account,
        FolderId = Folder,
        From = [new Address { Name = "Alice", Email = "alice@example.com" }],
        Subject = $"Hello {id}",
        Date = DateTimeOffset.FromUnixTimeSeconds(1_700_000_000),
        Snippet = "",
        Flags = [],
        HasAttachments = false,
        Size = 10,
    };

    private static Message Message(string id) => new() { Summary = Summary(id), Cc = [new Address { Email = "cc@example.com" }], Attachments = [] };

    private static MessageBodyResult Body(string id, string? html = null, string remote = RemoteContentPolicy.Block) => new()
    {
        MessageId = id,
        BodyState = BodyState.Fetched,
        HasHtml = html is not null,
        Html = html,
        Text = $"text of {id}",
        Blocked = new BlockedContent { RemoteImages = remote == RemoteContentPolicy.Block ? 2 : 0 },
        RemoteContent = remote,
        SanitizerVersion = "1",
    };

    /// <summary>
    /// A daemon serving <c>message.get</c> and <c>message.body</c> from
    /// tables, a client and a cache made on the test's UI thread.
    /// </summary>
    private sealed class Harness : IAsyncDisposable
    {
        private TaskCompletionSource held = new(TaskCreationOptions.RunContinuationsAsynchronously);

        private Harness()
        {
            Client = new RpcClient(Daemon.Path, PortableKeyFilePolicy.Instance);
        }

        public FakeDaemon Daemon { get; } = new();

        public RpcClient Client { get; }

        public TestUIContext Ui { get; } = new();

        public PendingWork Pending { get; } = new();

        public MessageCache Cache { get; private set; } = null!;

        public Log Log { get; } = new();

        public static async Task<Harness> CreateAsync()
        {
            var h = new Harness();
            h.Cache = await h.Ui.RunAsync(() => new MessageCache(h.Client, h.Log.Toasts.Add, pending: h.Pending));
            h.Log.Attach(h.Cache);
            return h;
        }

        /// <summary>
        /// Serves <c>message.get</c> and <c>message.body</c> for
        /// <paramref name="id"/>, held until <see cref="Release"/> when
        /// <paramref name="held"/>; <paramref name="getFails"/> and
        /// <paramref name="bodyFails"/> answer with an internal error instead.
        /// The body under <c>remoteContent: allow</c> carries
        /// <paramref name="allowHtml"/> and no blocked images.
        /// </summary>
        public void Serve(
            string id, bool held = false, bool getFails = false, bool bodyFails = false, string? html = null, string? allowHtml = null,
            bool allowFails = false)
        {
            var gate = held ? Hold() : Task.CompletedTask;
            var get = JsonCoding.EncodeToString(new MessageGetResult { Message = Message(id) });
            var block = JsonCoding.EncodeToString(Body(id, html));
            var allow = JsonCoding.EncodeToString(Body(id, allowHtml ?? html, RemoteContentPolicy.Allow));
            Daemon.On(API.MessageGet.Name, async _ =>
            {
                await gate;
                if (getFails)
                {
                    throw new RpcException(new RpcError { Code = ErrorCode.InternalError, Message = "get failed" });
                }
                return get;
            });
            Daemon.On(API.MessageBody.Name, async p =>
            {
                await gate;
                var q = JsonCoding.Decode<MessageBodyParams>(p);
                if (q.RemoteContent?.Value == RemoteContentPolicy.Allow)
                {
                    if (allowFails)
                    {
                        throw new RpcException(new RpcError { Code = ErrorCode.NetworkError, Message = "no network" });
                    }
                    return allow;
                }
                if (bodyFails)
                {
                    throw new RpcException(new RpcError { Code = ErrorCode.InternalError, Message = "body failed" });
                }
                return block;
            });
        }

        /// <summary>Lets the held answers go.</summary>
        public void Release() => held.TrySetResult();

        public async Task StartAsync()
        {
            await Daemon.StartAsync();
            await Client.ConnectAsync(Ct);
        }

        public Task IdleAsync() => Quiescence.IdleAsync(Ui, Pending, Daemon);

        public int Calls(string method) => Daemon.Calls.Count(m => m == method);

        public async ValueTask DisposeAsync()
        {
            Release();
            await Ui.RunAsync(Cache.Dispose);
            Client.Dispose();
            await Daemon.StopAsync();
            Ui.Dispose();
        }

        private Task Hold()
        {
            held = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
            return held.Task;
        }
    }

    /// <summary>What the cache reported, in order; read on the UI thread.</summary>
    private sealed class Log
    {
        public List<string> Toasts { get; } = [];

        public List<MessageId> Loaded { get; } = [];

        public List<(MessageId Id, bool Loading)> Bars { get; } = [];

        public void Attach(MessageCache cache)
        {
            cache.MessageLoaded += (_, e) => Loaded.Add(e.Id);
            cache.RemoteBarChanged += (_, e) => Bars.Add((e.Id, e.Loaded.LoadingImages));
        }
    }
}
