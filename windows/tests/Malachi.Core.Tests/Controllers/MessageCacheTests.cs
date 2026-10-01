// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/MessageCacheTests.swift: the
// loaded-message cache over a FakeDaemon (ui/internal/window/
// message_view.go fetchMessage/settleLoaded, remote.go loadRemoteImages,
// downloadPictures, lostPicture, outbox.go refetchMessage, download.go
// download, partData, embeddedData). Swift delays the daemon's answers by
// 60 ms to catch a request in flight and sleeps for the download spinner's
// delay; here the answers are held until the test lets them go, the
// spinner's delay runs on a fake clock the test advances, and the tests
// wait for quiescence instead of polling. saveAllIsOnePerMessage is
// AttachmentOpenerTests.SaveAllRunsOncePerMessageAtATime: Windows keeps
// the run of Save All with the attachment actions (AttachmentOpener). Added:
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
using Microsoft.Extensions.Time.Testing;
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

    // A clicked notification (notify_open.go OpenNotifiedMessage, Swift
    // lookUpGetsTheSummaryOnceAndKeepsIt): the ids alone find the summary,
    // by message.get once, and the cache keeps it; a message the daemon
    // lacks answers null.
    [Fact]
    public async Task LookUpGetsTheSummaryOnceAndKeepsIt()
    {
        await using var h = await Harness.CreateAsync();
        h.Serve("m9", held: true);
        await h.StartAsync();
        var found = new List<MessageSummary?>();
        await h.Ui.RunAsync(() =>
        {
            h.Cache.LookUp(Account, "m9", found.Add);
            // A second click while the first runs joins it.
            h.Cache.LookUp(Account, "m9", found.Add);
            Assert.True(h.Cache.Loaded("m9")?.Getting);
        });
        h.Release();
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(["Hello m9", "Hello m9"], found.Select(f => f?.Subject).ToList());
            Assert.Equal("Hello m9", h.Cache.Summary("m9")?.Subject);

            // Known now: answered at once.
            MessageSummary? again = null;
            h.Cache.LookUp(Account, "m9", f => again = f);
            Assert.Equal("m9", again?.Id.Value);
        });
        Assert.Equal(1, h.Calls(API.MessageGet.Name));
        Assert.Equal(0, h.Calls(API.MessageBody.Name));

        h.Serve("m8", getFails: true);
        var gone = new List<MessageSummary?>();
        await h.Ui.RunAsync(() => h.Cache.LookUp(Account, "m8", gone.Add));
        await h.IdleAsync();
        await h.Ui.RunAsync(() => Assert.Null(Assert.Single(gone)));
        // A fetch after it still asks for the headers.
        var answers = 0;
        await h.Ui.RunAsync(() => h.Cache.Fetch(Summary("m8"), _ => answers++));
        await h.IdleAsync();
        await h.Ui.RunAsync(() => Assert.Equal(2, answers));
        Assert.Equal(3, h.Calls(API.MessageGet.Name));
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

    // Downloads (download.go)

    /// <summary>
    /// One message.download however often it is asked for while it runs; the
    /// chips show the spinner only once it took the delay, and the cached
    /// message is replaced with the answer.
    /// </summary>
    [Fact]
    public async Task DownloadIsSharedAndShowsTheSpinnerLate()
    {
        await using var h = await Harness.CreateAsync();
        h.Serve("m11");
        var after = Message("m11") with { Attachments = [BigAttachment(remote: null)] };
        var script = new DownloadScript(after);
        script.Hold();
        h.ServeDownloads(script);
        await h.StartAsync();
        var s = Summary("m11");
        await h.Ui.RunAsync(() => h.Cache.Fetch(s, _ => { }));
        await h.IdleAsync();
        var lm = await h.Ui.RunAsync(() => h.Cache.Loaded(s.Id)!);
        await h.Ui.RunAsync(() => lm.Msg = lm.Msg! with { Attachments = [BigAttachment()] });

        var (first, second) = await h.Ui.RunAsync(() => (h.Cache.DownloadAsync(Account, s.Id), h.Cache.DownloadAsync(Account, s.Id)));
        Assert.Same(first, second); // one call for both
        await script.Started.WaitAsync(Ct);
        await h.Ui.RunAsync(() =>
        {
            Assert.True(h.Cache.Downloading(s.Id));
            Assert.False(h.Cache.ShowsDownload(s.Id), "no spinner before the delay");
            Assert.Empty(h.Log.Chips);
        });
        h.Time.Advance(MessageCache.DownloadSpinnerDelay);
        await h.Ui.DrainAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.True(h.Cache.ShowsDownload(s.Id));
            Assert.Equal([(s.Id, true)], h.Log.Chips);
        });

        script.Release();
        var answers = await Task.WhenAll(first, second);
        await h.IdleAsync();
        Assert.All(answers, m => ApiJson.AssertSameValue(after, m));
        Assert.Equal(["message.download"], script.Log);
        await h.Ui.RunAsync(() =>
        {
            Assert.False(h.Cache.ShowsDownload(s.Id) || h.Cache.Downloading(s.Id));
            Assert.Equal([(s.Id, true), (s.Id, false)], h.Log.Chips); // drawn with the spinner, then without
            // The cached message is the downloaded one; the body stays.
            Assert.False(lm.Msg!.Attachments[0].IsRemote);
            Assert.Equal("text of m11", lm.Body?.Text);
            Assert.Empty(h.Log.Toasts); // the callers say what failed, not the cache
        });
        Assert.Equal(1, h.Calls(API.MessageBody.Name));
    }

    [Fact]
    public async Task AQuickDownloadNeverShowsTheSpinner()
    {
        await using var h = await Harness.CreateAsync();
        h.Serve("m12");
        h.ServeDownloads(new DownloadScript(Message("m12")));
        await h.StartAsync();
        await h.Ui.InvokeAsync(() => h.Cache.DownloadAsync(Account, "m12"));
        await h.IdleAsync();
        Assert.Equal([(new MessageId("m12"), false)], await h.Ui.RunAsync(() => h.Log.Chips.ToList()));

        // The delayed spinner of a finished download never comes.
        h.Time.Advance(MessageCache.DownloadSpinnerDelay);
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.Single(h.Log.Chips);
            Assert.False(h.Cache.ShowsDownload("m12"));
        });
    }

    /// <summary>
    /// A body that was not downloaded is dropped after the download and asked
    /// for again; every view hears about it through MessageLoaded.
    /// </summary>
    [Fact]
    public async Task AnUnfetchedBodyIsFetchedAgainAfterTheDownload()
    {
        await using var h = await Harness.CreateAsync();
        var script = new DownloadScript(Message("m13"));
        h.ServeDownloads(script);
        var pending = JsonCoding.EncodeToString(Body("m13") with { BodyState = BodyState.Pending, Text = "" });
        var fetched = JsonCoding.EncodeToString(Body("m13"));
        var get = JsonCoding.EncodeToString(new MessageGetResult { Message = Message("m13") });
        h.Daemon.On(API.MessageGet.Name, _ => get);
        h.Daemon.On(API.MessageBody.Name, _ => script.Downloaded ? fetched : pending);
        await h.StartAsync();
        var s = Summary("m13");
        await h.Ui.RunAsync(() => h.Cache.Fetch(s, _ => { }));
        await h.IdleAsync();
        var lm = await h.Ui.RunAsync(() => h.Cache.Loaded(s.Id)!);
        var loadedBefore = await h.Ui.RunAsync(() =>
        {
            Assert.Equal(BodyState.Pending, lm.Body?.BodyState);
            return h.Log.Loaded.Count;
        });

        await h.Ui.InvokeAsync(() => h.Cache.DownloadAsync(Account, s.Id));
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(BodyState.Fetched, lm.Body?.BodyState);
            Assert.Equal("text of m13", lm.Body?.Text);
            Assert.Equal(loadedBefore + 1, h.Log.Loaded.Count);
        });
        Assert.Equal(2, h.Calls(API.MessageBody.Name));
        Assert.Equal(1, h.Calls(API.MessageGet.Name)); // the message came with the download
    }

    [Fact]
    public async Task AFailedDownloadReachesEveryCallerAndStopsTheSpinner()
    {
        await using var h = await Harness.CreateAsync();
        h.Serve("m14");
        var script = new DownloadScript(Message("m14")) { Failure = new RpcError { Code = ErrorCode.Offline, Message = "no network" } };
        script.Hold();
        h.ServeDownloads(script);
        await h.StartAsync();
        var s = Summary("m14");
        await h.Ui.RunAsync(() => h.Cache.Fetch(s, _ => { }));
        await h.IdleAsync();
        var before = await h.Ui.RunAsync(() => h.Cache.Loaded(s.Id)!.Msg);

        var (first, second) = await h.Ui.RunAsync(() => (h.Cache.DownloadAsync(Account, s.Id), h.Cache.DownloadAsync(Account, s.Id)));
        await script.Started.WaitAsync(Ct);
        h.Time.Advance(MessageCache.DownloadSpinnerDelay);
        await h.Ui.DrainAsync();
        script.Release();
        foreach (var t in new[] { first, second })
        {
            var e = await Assert.ThrowsAsync<RpcException>(() => t);
            Assert.Equal(ErrorCode.Offline, e.Code.Value);
        }
        await h.IdleAsync();
        Assert.Equal(["message.download"], script.Log);
        await h.Ui.RunAsync(() =>
        {
            Assert.False(h.Cache.ShowsDownload(s.Id) || h.Cache.Downloading(s.Id));
            Assert.Equal([true, false], h.Log.Chips.Select(c => c.Spinning));
            Assert.Same(before, h.Cache.Loaded(s.Id)!.Msg); // nothing replaced
            Assert.Empty(h.Log.Toasts);
        });

        // The next request asks again.
        script.Failure = null;
        script.Release();
        await h.Ui.InvokeAsync(() => h.Cache.DownloadAsync(Account, s.Id));
        Assert.Equal(["message.download", "message.download"], script.Log);
    }

    /// <summary>
    /// download.go <c>partData</c>: a remote part downloads first; a part the
    /// daemon answers partNotDownloaded for gets one download and a retry.
    /// </summary>
    [Fact]
    public async Task PartDataDownloadsWhatIsOnTheServer()
    {
        await using var h = await Harness.CreateAsync();
        h.Serve("m15");
        var script = new DownloadScript(Message("m15") with { Attachments = [BigAttachment(remote: null)] });
        h.ServeDownloads(script);
        await h.StartAsync();

        var res = await h.Ui.InvokeAsync(() => h.Cache.PartDataAsync(Account, "m15", BigAttachment(), onServer: true));
        Assert.Equal([7, 8, 9], res.Data);
        Assert.Equal("2", res.PartId);
        Assert.Equal(["message.download", "message.part:2"], script.Log);

        // The chip said local, the daemon disagrees: one download, one retry.
        script.Downloaded = false;
        var again = await h.Ui.InvokeAsync(() => h.Cache.PartDataAsync(Account, "m15", BigAttachment(remote: null), onServer: false));
        Assert.Equal([7, 8, 9], again.Data);
        Assert.Equal(["message.part:2", "message.download", "message.part:2"], script.Log.TakeLast(3));

        // An attached message the same way; the download moved the part, so
        // the rendered one is the new id.
        var eml = BigAttachment("3") with { Filename = "fwd.eml", ContentType = "message/rfc822" };
        var withEml = Message("m15") with { Attachments = [eml with { PartId = "4", Remote = null }] };
        var emlScript = new DownloadScript(withEml);
        h.ServeDownloads(emlScript);
        var embedded = await h.Ui.InvokeAsync(() => h.Cache.EmbeddedDataAsync(Account, "m15", eml, onServer: true));
        Assert.Equal("4", embedded.PartId);
        Assert.Equal(["message.download", "message.embedded:4"], emlScript.Log);
        await h.IdleAsync();
    }

    // Pictures on the server (remote.go)

    /// <summary>
    /// remote.go <c>downloadPictures</c>: message.download (the shared one, so
    /// the chips hear about it), then message.body again, which now counts no
    /// picture on the server; the bar shows the wait from the click on and a
    /// second click meanwhile is ignored. Remote images shown stay shown
    /// (<see cref="RemoteBar.PicturesPolicy"/>).
    /// </summary>
    [Fact]
    public async Task DownloadPicturesDownloadsThenAsksForTheBodyAgain()
    {
        await using var h = await Harness.CreateAsync();
        const string Html = "<p><img src=\"malachi-cid:acc_1/m20/2\"></p>";
        var get = JsonCoding.EncodeToString(new MessageGetResult { Message = Message("m20") });
        var script = new DownloadScript(Message("m20"));
        script.Hold();
        h.ServeDownloads(script);
        var policies = new List<RemoteContentPolicy?>();
        h.Daemon.On(API.MessageGet.Name, _ => get);
        h.Daemon.On(API.MessageBody.Name, p =>
        {
            var q = JsonCoding.Decode<MessageBodyParams>(p);
            lock (policies)
            {
                policies.Add(q.RemoteContent);
            }
            var n = script.Downloaded ? 0 : 2;
            return JsonCoding.EncodeToString(Body("m20", Html, q.RemoteContent?.Value ?? RemoteContentPolicy.Block) with
            {
                RemotePictures = n == 0 ? null : n,
                Blocked = new BlockedContent(),
            });
        });
        await h.StartAsync();
        var s = Summary("m20");
        await h.Ui.RunAsync(() => h.Cache.Fetch(s, _ => { }));
        await h.IdleAsync();
        var lm = await h.Ui.RunAsync(() => h.Cache.Loaded(s.Id)!);
        var loadedBefore = await h.Ui.RunAsync(() =>
        {
            Assert.Equal(2, RemoteBar.RemotePictures(lm.Body));
            Assert.Equal(new PicturesBarState(Visible: true, Remote: 2), RemoteBar.PicturesBarStateFor(lm));
            return h.Log.Loaded.Count;
        });

        Outcome<LoadedMessage>? outcome = null;
        await h.Ui.RunAsync(() =>
        {
            h.Cache.DownloadPictures(s, null, o => outcome = o);
            Assert.True(lm.LoadingPictures);
            Assert.Equal([true], h.Log.PictureBars);
            Assert.Equal(new PicturesBarState(Visible: true, Loading: true), RemoteBar.PicturesBarStateFor(lm));
            h.Cache.DownloadPictures(s, null, _ => Assert.Fail("a second request was started"));
        });
        await script.Started.WaitAsync(Ct);
        script.Release();
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.True(outcome?.IsSuccess, "downloadPictures failed");
            Assert.Same(lm, outcome!.Value.Value);
            Assert.False(lm.LoadingPictures);
            Assert.Equal(0, RemoteBar.RemotePictures(lm.Body));
            Assert.Equal(new PicturesBarState(), RemoteBar.PicturesBarStateFor(lm));
            Assert.Equal(loadedBefore + 1, h.Log.Loaded.Count);
            Assert.Equal([s.Id], h.Log.Chips.Select(c => c.Id)); // the chips heard about the download
            Assert.Equal([true], h.Log.PictureBars); // the new body redraws the rest
            Assert.Empty(h.Log.Toasts);
        });
        Assert.Equal(["message.download"], script.Log);
        lock (policies)
        {
            Assert.Equal(new RemoteContentPolicy?[] { null, null }, policies); // the stored preference, twice
        }

        // Remote images on display: the body comes under allow again.
        await h.Ui.RunAsync(() =>
        {
            lm.Body = lm.Body! with { RemoteContent = RemoteContentPolicy.Allow };
            outcome = null;
            h.Cache.DownloadPictures(s, null, o => outcome = o);
        });
        await h.IdleAsync();
        lock (policies)
        {
            Assert.Equal(new string?[] { null, null, RemoteContentPolicy.Allow }, policies.Select(p => p?.Value));
        }
        await h.Ui.RunAsync(() =>
        {
            Assert.True(outcome?.IsSuccess);
            Assert.Equal(RemoteContentPolicy.Allow, lm.Body?.RemoteContent.Value);
        });
    }

    /// <summary>
    /// A failed download (or body) is toasted where the click came from, the
    /// body on display stays and the bar offers the pictures again.
    /// </summary>
    [Fact]
    public async Task DownloadPicturesFailureToastsAndOffersThemAgain()
    {
        await using var h = await Harness.CreateAsync();
        var get = JsonCoding.EncodeToString(new MessageGetResult { Message = Message("m21") });
        var before = Body("m21", "<p>x</p>") with { RemotePictures = 1, Blocked = new BlockedContent(), Text = "x" };
        var served = JsonCoding.EncodeToString(before);
        var script = new DownloadScript(Message("m21")) { Failure = new RpcError { Code = ErrorCode.Offline, Message = "no network" } };
        h.ServeDownloads(script);
        h.Daemon.On(API.MessageGet.Name, _ => get);
        h.Daemon.On(API.MessageBody.Name, _ => served);
        await h.StartAsync();
        var s = Summary("m21");
        await h.Ui.RunAsync(() => h.Cache.Fetch(s, _ => { }));
        await h.IdleAsync();
        var lm = await h.Ui.RunAsync(() => h.Cache.Loaded(s.Id)!);
        var shown = await h.Ui.RunAsync(() => lm.Body);

        var said = new List<string>();
        Outcome<LoadedMessage>? outcome = null;
        await h.Ui.RunAsync(() => h.Cache.DownloadPictures(s, said.Add, o => outcome = o));
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.False(outcome?.IsSuccess ?? true, "downloadPictures succeeded");
            Assert.Equal(ErrorCode.Offline, Assert.IsType<RpcException>(outcome!.Value.Error).Code.Value);
            Assert.Equal(["Downloading the pictures failed: no network connection"], said);
            Assert.Empty(h.Log.Toasts); // not the cache's own toast
            Assert.Same(shown, lm.Body);
            Assert.False(lm.LoadingPictures);
            Assert.Equal([true, false], h.Log.PictureBars);
            Assert.Equal(new PicturesBarState(Visible: true, Remote: 1), RemoteBar.PicturesBarStateFor(lm));
        });
        Assert.Equal(1, h.Calls(API.MessageBody.Name)); // no body without the download

        // Without a toast of its own the cache's is used.
        await h.Ui.RunAsync(() => h.Cache.DownloadPictures(s, null, _ => { }));
        await h.IdleAsync();
        await h.Ui.RunAsync(() => Assert.Equal(["Downloading the pictures failed: no network connection"], h.Log.Toasts));
    }

    /// <summary>
    /// remote.go <c>lostPicture</c> and download.go <c>endDownload</c>: a body
    /// cached while the daemon held the message counts no picture on the
    /// server; once the daemon dropped its copy, the first partNotDownloaded
    /// for a picture the body lists asks for the body again (the bar comes
    /// back), never more than once until the next download. A download for
    /// anything else asks for a body that counts pictures again, so they show
    /// and the bar goes; the next loss may ask once more.
    /// </summary>
    [Fact]
    public async Task LostPicturesAskForTheBodyAgain()
    {
        await using var h = await Harness.CreateAsync();
        const string Html = "<p><img src=\"malachi-cid:acc_1/m22/2\"></p>";
        var get = JsonCoding.EncodeToString(new MessageGetResult { Message = Message("m22") });
        var script = new DownloadScript(Message("m22")) { Downloaded = true }; // the daemon holds the message
        h.ServeDownloads(script);
        var policies = new List<RemoteContentPolicy?>();
        h.Daemon.On(API.MessageGet.Name, _ => get);
        h.Daemon.On(API.MessageBody.Name, p =>
        {
            var q = JsonCoding.Decode<MessageBodyParams>(p);
            lock (policies)
            {
                policies.Add(q.RemoteContent);
            }
            var n = script.Downloaded ? 0 : 1;
            return JsonCoding.EncodeToString(Body("m22", Html, q.RemoteContent?.Value ?? RemoteContentPolicy.Block) with
            {
                InlineParts = new Dictionary<string, string> { ["p@x"] = "2" },
                RemotePictures = n == 0 ? null : n,
                Blocked = new BlockedContent(),
            });
        });
        await h.StartAsync();
        var s = Summary("m22");
        await h.Ui.RunAsync(() => h.Cache.Fetch(s, _ => { }));
        await h.IdleAsync();
        var lm = await h.Ui.RunAsync(() => h.Cache.Loaded(s.Id)!);
        var loadedBefore = await h.Ui.RunAsync(() =>
        {
            Assert.Equal(new PicturesBarState(), RemoteBar.PicturesBarStateFor(lm));
            return h.Log.Loaded.Count;
        });

        // The daemon dropped its copy. A part the body does not show as a
        // picture is not a reason to ask.
        script.Downloaded = false;
        await Assert.ThrowsAsync<RpcException>(() => h.Ui.InvokeAsync(() => h.Cache.FetchPartAsync(Account, s.Id, "9", Ct)));
        await h.IdleAsync();
        Assert.Equal(1, h.Calls(API.MessageBody.Name));

        // The picture: the error reaches the web view, the body is asked for
        // again and brings the bar back.
        var e = await Assert.ThrowsAsync<RpcException>(() => h.Ui.InvokeAsync(() => h.Cache.FetchPartAsync(Account, s.Id, "2", Ct)));
        Assert.True(Download.IsPartNotDownloaded(e));
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(1, RemoteBar.RemotePictures(lm.Body));
            Assert.Equal(new PicturesBarState(Visible: true, Remote: 1), RemoteBar.PicturesBarStateFor(lm));
            Assert.True(lm.PicturesRechecked);
            Assert.Equal(loadedBefore + 1, h.Log.Loaded.Count); // every view shows the new body
        });

        // The same body shown again: no second question.
        await Assert.ThrowsAsync<RpcException>(() => h.Ui.InvokeAsync(() => h.Cache.FetchPartAsync(Account, s.Id, "2", Ct)));
        await h.IdleAsync();
        Assert.Equal(2, h.Calls(API.MessageBody.Name));

        // A download for anything else (a chip): the body again, which now
        // counts none; the bar goes and the question is allowed once more.
        await h.Ui.InvokeAsync(() => h.Cache.DownloadAsync(Account, s.Id));
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.False(lm.PicturesRechecked);
            Assert.Equal(0, RemoteBar.RemotePictures(lm.Body));
            Assert.Equal(new PicturesBarState(), RemoteBar.PicturesBarStateFor(lm));
        });
        Assert.Equal(3, h.Calls(API.MessageBody.Name));

        // Remote images on display stay on display when the copy goes again.
        await h.Ui.RunAsync(() => lm.Body = lm.Body! with { RemoteContent = RemoteContentPolicy.Allow });
        script.Downloaded = false;
        await Assert.ThrowsAsync<RpcException>(() => h.Ui.InvokeAsync(() => h.Cache.FetchPartAsync(Account, s.Id, "2", Ct)));
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(1, RemoteBar.RemotePictures(lm.Body));
            Assert.Equal(RemoteContentPolicy.Allow, lm.Body?.RemoteContent.Value);
            Assert.Empty(h.Log.Toasts);
        });
        lock (policies)
        {
            Assert.Equal(new string?[] { null, null, null, RemoteContentPolicy.Allow }, policies.Select(p => p?.Value));
        }
        Assert.Single(script.Log, l => l == "message.download");
    }

    /// <summary>
    /// A download with a body that counts no picture on the server asks for
    /// nothing more; a failed one neither.
    /// </summary>
    [Fact]
    public async Task ADownloadAsksForTheBodyOnlyWhenPicturesAreCounted()
    {
        await using var h = await Harness.CreateAsync();
        var get = JsonCoding.EncodeToString(new MessageGetResult { Message = Message("m23") });
        var script = new DownloadScript(Message("m23")) { Failure = new RpcError { Code = ErrorCode.Offline, Message = "no network" } };
        h.ServeDownloads(script);
        h.Daemon.On(API.MessageGet.Name, _ => get);
        var counted = JsonCoding.EncodeToString(Body("m23", "<p><img src=\"malachi-cid:acc_1/m23/2\"></p>") with
        {
            InlineParts = new Dictionary<string, string> { ["p@x"] = "2" },
            RemotePictures = 1,
            Blocked = new BlockedContent(),
        });
        h.Daemon.On(API.MessageBody.Name, _ => counted);
        await h.StartAsync();
        var s = Summary("m23");
        await h.Ui.RunAsync(() => h.Cache.Fetch(s, _ => { }));
        await h.IdleAsync();
        var lm = await h.Ui.RunAsync(() => h.Cache.Loaded(s.Id)!);

        await Assert.ThrowsAsync<RpcException>(() => h.Ui.InvokeAsync(() => h.Cache.DownloadAsync(Account, s.Id)));
        await h.IdleAsync();
        Assert.Equal(1, h.Calls(API.MessageBody.Name)); // a failed download asks for nothing

        script.Failure = null;
        await h.Ui.RunAsync(() => lm.Body = lm.Body! with { RemotePictures = null });
        await h.Ui.InvokeAsync(() => h.Cache.DownloadAsync(Account, s.Id));
        await h.IdleAsync();
        Assert.Equal(1, h.Calls(API.MessageBody.Name)); // nothing counted, nothing to ask
    }

    private static Attachment BigAttachment(string id = "2", bool? remote = true) => new()
    {
        PartId = id,
        Filename = "big.pdf",
        ContentType = "application/pdf",
        Size = 300_000,
        Inline = false,
        Remote = remote,
    };

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

        /// <summary>The clock of the download spinner's delay.</summary>
        public FakeTimeProvider Time { get; } = new();

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
            h.Cache = await h.Ui.RunAsync(() => new MessageCache(h.Client, h.Log.Toasts.Add, pending: h.Pending, timeProvider: h.Time));
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

        /// <summary>Serves message.download, message.part and message.embedded from <paramref name="script"/>.</summary>
        public void ServeDownloads(DownloadScript script)
        {
            Daemon.On(API.MessageDownload.Name, script.DownloadAsync);
            Daemon.On(API.MessagePart.Name, script.Part);
            Daemon.On(API.MessageEmbedded.Name, script.Embedded);
        }

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

        /// <summary>Every RemoteBarChanged, with whether the pictures were on their way.</summary>
        public List<bool> PictureBars { get; } = [];

        /// <summary>Every ChipsChanged, with whether the spinner showed then.</summary>
        public List<(MessageId Id, bool Spinning)> Chips { get; } = [];

        public void Attach(MessageCache cache)
        {
            cache.MessageLoaded += (_, e) => Loaded.Add(e.Id);
            cache.RemoteBarChanged += (_, e) =>
            {
                Bars.Add((e.Id, e.Loaded.LoadingImages));
                PictureBars.Add(e.Loaded.LoadingPictures);
            };
            cache.ChipsChanged += (_, e) => Chips.Add((e.Id, cache.ShowsDownload(e.Id)));
        }
    }

    /// <summary>
    /// The daemon's side of message.download, message.part and
    /// message.embedded for one message with a part kept on the server: the
    /// part answers partNotDownloaded until the message was downloaded. The
    /// daemon's threads call it; the test reads it once they are done.
    /// </summary>
    private sealed class DownloadScript(Message message)
    {
        private readonly Lock gate = new();
        private readonly List<string> log = [];
        private readonly TaskCompletionSource started = new(TaskCreationOptions.RunContinuationsAsynchronously);
        private TaskCompletionSource hold = Released();
        private bool downloaded;
        private RpcError? failure;

        /// <summary>Completes when the first message.download reached the daemon.</summary>
        public Task Started => started.Task;

        /// <summary>Whether the daemon has (or holds) the message: its parts are served.</summary>
        public bool Downloaded
        {
            get
            {
                lock (gate)
                {
                    return downloaded;
                }
            }
            set
            {
                lock (gate)
                {
                    downloaded = value;
                }
            }
        }

        /// <summary>What message.download answers with instead of the message.</summary>
        public RpcError? Failure
        {
            get
            {
                lock (gate)
                {
                    return failure;
                }
            }
            set
            {
                lock (gate)
                {
                    failure = value;
                }
            }
        }

        /// <summary>message.download, message.part:&lt;id&gt; and message.embedded:&lt;id&gt;, in order.</summary>
        public IReadOnlyList<string> Log
        {
            get
            {
                lock (gate)
                {
                    return [.. log];
                }
            }
        }

        /// <summary>Holds the downloads until <see cref="Release"/>.</summary>
        public void Hold()
        {
            lock (gate)
            {
                hold = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
            }
        }

        /// <summary>Lets the held downloads go.</summary>
        public void Release()
        {
            lock (gate)
            {
                hold.TrySetResult();
            }
        }

        public async Task<string> DownloadAsync(string paramsJson)
        {
            Task wait;
            lock (gate)
            {
                log.Add("message.download");
                wait = hold.Task;
            }
            started.TrySetResult();
            await wait;
            if (Failure is { } f)
            {
                throw new RpcException(f);
            }
            Downloaded = true;
            return JsonCoding.EncodeToString(new MessageDownloadResult { Message = message });
        }

        public string Part(string paramsJson)
        {
            var p = JsonCoding.Decode<MessagePartParams>(paramsJson);
            lock (gate)
            {
                log.Add("message.part:" + p.PartId);
            }
            if (!Downloaded)
            {
                throw new RpcException(new RpcError { Code = ErrorCode.PartNotDownloaded, Message = "on the server" });
            }
            return JsonCoding.EncodeToString(new MessagePartResult { PartId = p.PartId, ContentType = "application/pdf", Filename = "big.pdf", Size = 3, Data = [7, 8, 9] });
        }

        public string Embedded(string paramsJson)
        {
            var p = JsonCoding.Decode<MessageEmbeddedParams>(paramsJson);
            lock (gate)
            {
                log.Add("message.embedded:" + p.PartId);
            }
            if (!Downloaded)
            {
                throw new RpcException(new RpcError { Code = ErrorCode.PartNotDownloaded, Message = "on the server" });
            }
            return JsonCoding.EncodeToString(new MessageEmbeddedResult { PartId = p.PartId, Message = Message("inner"), Body = Body("inner") });
        }

        private static TaskCompletionSource Released()
        {
            var t = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
            t.SetResult();
            return t;
        }
    }
}
