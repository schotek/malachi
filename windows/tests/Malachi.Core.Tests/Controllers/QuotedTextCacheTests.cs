// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/QuotedTextTests.swift
// (QuotedTextCacheTests): the cache asks message.body for the variant of
// the body on display (trimQuoted unless the quoted history shows), holds
// both variants, and asks the remote images for the variant shown, over a
// FakeDaemon. The tests wait for quiescence instead of polling.

using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Model;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Tests.Model;
using Malachi.Core.Transport;
using Xunit;

namespace Malachi.Core.Tests.Controllers;

public sealed class QuotedTextCacheTests
{
    private static CancellationToken Ct => TestContext.Current.CancellationToken;

    private static MessageSummary Summary(string id) => new()
    {
        Id = id,
        AccountId = "acc_1",
        FolderId = "fld_1",
        From = [new Address { Name = "Alice", Email = "alice@example.com" }],
        Subject = $"Hello {id}",
        Date = System.DateTimeOffset.FromUnixTimeSeconds(1_700_000_000),
        Snippet = "",
        Flags = [],
        HasAttachments = false,
        Size = 10,
    };

    [Fact]
    public async Task FetchAsksForTheVariantOnDisplay()
    {
        await using var h = await Harness.CreateAsync();
        var s = Summary("a");
        LoadedMessage? got = null;
        await h.Ui.RunAsync(() => h.Cache.FetchBody(s, lm => got = lm));
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.True(got?.Body?.IsQuotedTrimmed);
            Assert.Equal(QuotedTextOffer.Show, LoadedMessageText.QuotedTextOfferFor(got));
        });
        Assert.Equal([true], h.Requests.Select(r => r.Trim).ToArray()); // trimmed by default

        // Show Quoted Text: the whole body is asked for.
        await h.Ui.RunAsync(() =>
        {
            got = null;
            h.Cache.FetchBody(s, lm => got = lm, quoted: true);
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal("new\n> old", got?.Body?.Text);
            Assert.Equal(QuotedTextOffer.Hide, LoadedMessageText.QuotedTextOfferFor(got));
        });
        Assert.Equal([true, false], h.Requests.Select(r => r.Trim).ToArray());

        // Hide and show again: both are held, nothing is asked.
        await h.Ui.RunAsync(() =>
        {
            var before = h.Loaded.Count;
            got = null;
            h.Cache.FetchBody(s, lm => got = lm, quoted: false);
            Assert.Equal("new", got?.Body?.Text); // at once
            Assert.Equal(before + 1, h.Loaded.Count); // the views showing it hear about it
            h.Cache.FetchBody(s, lm => got = lm, quoted: true);
            Assert.Equal("new\n> old", got?.Body?.Text);
            // Null keeps the variant.
            h.Cache.FetchBody(s, lm => got = lm);
            Assert.True(got?.QuotedShown);
        });
        await h.IdleAsync();
        Assert.Equal(2, h.BodyCalls);
    }

    [Fact]
    public async Task RemoteImagesReaskTheVariantShown()
    {
        await using var h = await Harness.CreateAsync();
        var s = Summary("a");
        LoadedMessage? got = null;
        await h.Ui.RunAsync(() => h.Cache.FetchBody(s, lm => got = lm));
        await h.IdleAsync();
        await h.Ui.RunAsync(() => h.Cache.FetchBody(s, lm => got = lm, quoted: true));
        await h.IdleAsync();
        await h.Ui.RunAsync(() => Assert.Equal("new\n> old", got?.Body?.Text));

        LoadedMessage? done = null;
        await h.Ui.RunAsync(() => h.Cache.LoadImages(s, o => done = o.TryGetValue(out var lm, out _) ? lm : null));
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            var lm = Assert.IsType<LoadedMessage>(done);
            Assert.True(lm.QuotedShown && lm.Body?.RemoteContent == RemoteContentPolicy.Allow);
            Assert.Equal("new\n> old", lm.Body?.Text);
            Assert.Null(lm.OtherBody); // the trimmed body under block went
            // Hide: the trimmed body is asked for again, with the images.
            got = null;
            h.Cache.FetchBody(s, l => got = l, quoted: false);
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal("new", got?.Body?.Text);
            Assert.True(got?.Body?.RemoteContent == RemoteContentPolicy.Allow);
        });
        var all = h.Requests;
        Assert.Equal([true, false, false, true], all.Select(r => r.Trim).ToArray());
        Assert.Equal([null, null, RemoteContentPolicy.Allow, RemoteContentPolicy.Allow], all.Select(r => r.Policy).ToArray());
    }

    [Fact]
    public async Task NothingCutNoButton()
    {
        await using var h = await Harness.CreateAsync(cut: false);
        LoadedMessage? got = null;
        await h.Ui.RunAsync(() => h.Cache.FetchBody(Summary("a"), lm => got = lm));
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.NotNull(got?.Body);
            Assert.Null(LoadedMessageText.QuotedTextOfferFor(got));
        });
    }

    /// <summary>
    /// A daemon answering message.body for any message: trimmed with
    /// trimQuoted, else whole (cut for the trimmed answer), under the policy
    /// asked for; a cache made on the test's UI thread.
    /// </summary>
    private sealed class Harness : System.IAsyncDisposable
    {
        private readonly Lock gate = new();
        private readonly List<(bool Trim, string? Policy)> requests = [];

        private Harness()
        {
            Client = new RpcClient(Daemon.Path, PortableKeyFilePolicy.Instance);
        }

        public FakeDaemon Daemon { get; } = new();

        public RpcClient Client { get; }

        public TestUIContext Ui { get; } = new();

        public PendingWork Pending { get; } = new();

        public MessageCache Cache { get; private set; } = null!;

        /// <summary>Every MessageLoaded, read on the UI thread.</summary>
        public List<MessageId> Loaded { get; } = [];

        /// <summary>The message.body requests answered: trimQuoted asked for, and the policy.</summary>
        public IReadOnlyList<(bool Trim, string? Policy)> Requests
        {
            get
            {
                lock (gate)
                {
                    return [.. requests];
                }
            }
        }

        public int BodyCalls => Daemon.Calls.Count(m => m == API.MessageBody.Name);

        public static async Task<Harness> CreateAsync(bool cut = true)
        {
            var h = new Harness();
            h.Cache = await h.Ui.RunAsync(() => new MessageCache(h.Client, _ => { }, pending: h.Pending));
            await h.Ui.RunAsync(() => h.Cache.MessageLoaded += (_, e) => h.Loaded.Add(e.Id));
            h.Daemon.On(API.MessageBody.Name, p =>
            {
                var q = JsonCoding.Decode<MessageBodyParams>(p);
                lock (h.gate)
                {
                    h.requests.Add((q.TrimQuoted == true, q.RemoteContent?.Value));
                }
                var allow = q.RemoteContent?.Value == RemoteContentPolicy.Allow;
                return JsonCoding.EncodeToString(QuotedTextLogicTests.Body(
                    q.MessageId.Value, whole: q.TrimQuoted != true, cut: cut, remote: allow ? RemoteContentPolicy.Allow : RemoteContentPolicy.Block));
            });
            await h.Daemon.StartAsync();
            await h.Client.ConnectAsync(Ct);
            return h;
        }

        public Task IdleAsync() => Quiescence.IdleAsync(Ui, Pending, Daemon);

        public async ValueTask DisposeAsync()
        {
            await Ui.RunAsync(Cache.Dispose);
            Client.Dispose();
            await Daemon.StopAsync();
            Ui.Dispose();
        }
    }
}
