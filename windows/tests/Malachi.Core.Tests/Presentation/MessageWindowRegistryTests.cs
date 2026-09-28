// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of MessageWindowRegistry: ui/internal/window/message_view.go
// (openMessageWindow, closeMessageWindow), embedded.go (openEmbeddedWindow,
// closeEmbeddedWindows), remote.go (showLoaded, refreshRemoteBar),
// download.go (refreshChips, embeddedData), outbox.go (showOutboxState),
// actions.go (refreshStars, refreshSeen) and
// macos MessageWindows.swift and WindowRegistry.swift, with windows that
// only record what they were asked.

using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Transport;
using Microsoft.Extensions.Time.Testing;
using Xunit;
using static Malachi.Core.Tests.Presentation.ReaderFixtures;

namespace Malachi.Core.Tests.Presentation;

public sealed class MessageWindowRegistryTests
{
    private readonly FakeReaderCache cache = new();
    private readonly FakeTimeProvider clock = new();
    private readonly List<FakeWindow> made = [];
    private readonly List<(object? Window, string Text)> toasts = [];
    private readonly MessageWindowRegistry registry;

    public MessageWindowRegistryTests()
    {
        registry = new MessageWindowRegistry(cache)
        {
            MakeMessageWindow = s => Make(ReaderMode.Window),
            MakeEmbeddedWindow = (containing, part, result) =>
            {
                var w = Make(ReaderMode.Embedded);
                w.Reader.ShowEmbedded(containing, part, result);
                return w;
            },
            Toast = (w, text) => toasts.Add((w, text)),
        };
    }

    private FakeWindow Make(ReaderMode mode)
    {
        var w = new FakeWindow(new ReaderController(mode, cache, timeProvider: clock), registry);
        made.Add(w);
        return w;
    }

    // The chip of an attached message under part.
    private static Attachment Eml(string part) => Attachment(part, "fwd.eml", "message/rfc822");

    private static MessageEmbeddedResult Embedded(string subject) => new()
    {
        PartId = "2",
        Message = Message(Summary("m1", subject)),
        Body = TextBody("m1", "inner"),
    };

    [Fact]
    public void OneWindowPerMessage()
    {
        var s = Summary("m1", "Plans");
        cache.Entry("m1").Body = TextBody("m1");
        registry.OpenMessage(s);
        var w = Assert.Single(made);
        Assert.Equal(1, w.Presented);
        Assert.Equal("Plans", w.Reader.Subject);
        Assert.Contains(w.Reader, registry.Readers);

        registry.OpenMessage(s);
        Assert.Single(made);
        Assert.Equal(2, w.Presented);

        // Closed by the user: forgotten, its view with it; the next open makes a new one.
        w.Close();
        Assert.Empty(registry.Keys);
        Assert.DoesNotContain(w.Reader, registry.Readers);
        registry.OpenMessage(s);
        Assert.Equal(2, made.Count);
    }

    [Fact]
    public async Task AnAttachedMessageOpensOnceAndAFailureToastsWhereTheChipWas()
    {
        var containing = Summary("m1", "Outer");
        cache.Embedded = _ => null;
        await registry.OpenEmbeddedAsync(containing, Eml("2"), remote: false, "chip window");
        Assert.Empty(made);
        Assert.Equal([("chip window", "Opening the attached message failed")], toasts);

        cache.Embedded = _ => Embedded("Inner");
        await registry.OpenEmbeddedAsync(containing, Eml("2"), remote: false, null);
        var w = Assert.Single(made);
        Assert.Equal("Inner", w.Reader.Subject);
        Assert.Equal(1, w.Presented);

        // Open again: raised, not fetched.
        await registry.OpenEmbeddedAsync(containing, Eml("2"), remote: false, null);
        Assert.Single(made);
        Assert.Equal(2, w.Presented);
        Assert.Equal(2, cache.EmbeddedCalls.Count);
    }

    /// <summary>
    /// embedded.go <c>openEmbeddedWindow</c> with <c>embeddedData</c>: an
    /// attached message the chip showed on the mail server is downloaded
    /// first, and the window is known by the part the daemon rendered, which
    /// a download on Microsoft 365 may have moved; a failed download is a
    /// toast where the chip is.
    /// </summary>
    [Fact]
    public async Task AnAttachedMessageOnTheServerIsDownloadedFirst()
    {
        var containing = Summary("m1", "Outer");
        cache.DownloadError = new RpcException(new RpcError { Code = ErrorCode.Offline, Message = "no network" });
        await registry.OpenEmbeddedAsync(containing, Eml("2"), remote: true, "chip window");
        Assert.Empty(made);
        Assert.Empty(cache.EmbeddedParts);
        Assert.Equal([("chip window", "Opening the attached message failed: no network connection")], toasts);

        cache.DownloadError = null;
        cache.Downloaded = Message(containing, [Eml("5")]);
        cache.Embedded = _ => Embedded("Inner") with { PartId = "5" };
        await registry.OpenEmbeddedAsync(containing, Eml("2"), remote: true, null);
        var w = Assert.Single(made);
        Assert.Equal(["m1", "m1"], cache.DownloadCalls.Select(id => id.Value));
        Assert.Equal(["5"], cache.EmbeddedParts);
        Assert.Contains(new MessageWindowKey.Embedded("m1", "5"), registry.Keys);

        // Asked for again under the part rendered: raised, not fetched.
        await registry.OpenEmbeddedAsync(containing, Eml("5"), remote: false, null);
        Assert.Single(made);
        Assert.Equal(2, w.Presented);
        Assert.Single(cache.EmbeddedParts);
    }

    [Fact]
    public async Task ASecondClickThatOvertookTheFirstRaisesItsWindow()
    {
        // The registry is the UI thread's, as in the app: both clicks start
        // there and their fetches come back to it one after the other.
        // Started on the test's thread, which has no synchronization
        // context, the gate released one continuation inline and queued the
        // other to the thread pool, and when they ran at once both made a
        // window.
        using var ui = new TestUIContext();
        var containing = Summary("m1");
        cache.EmbeddedGate = new TaskCompletionSource();
        cache.Embedded = _ => Embedded("Inner");
        var (first, second) = await ui.RunAsync(() =>
            (registry.OpenEmbeddedAsync(containing, Eml("2"), remote: false, null), registry.OpenEmbeddedAsync(containing, Eml("2"), remote: false, null)));
        cache.EmbeddedGate.SetResult();
        await Task.WhenAll(first, second);
        var w = Assert.Single(made);
        Assert.Equal(2, w.Presented);
    }

    [Fact]
    public async Task AMessageLeavingItsFolderClosesItsWindowsAndItsAttachedOnes()
    {
        registry.OpenMessage(Summary("m1"));
        registry.OpenMessage(Summary("m2"));
        cache.Embedded = _ => Embedded("Inner");
        await registry.OpenEmbeddedAsync(Summary("m1"), Eml("2"), remote: false, null);
        await registry.OpenEmbeddedAsync(Summary("m2"), Eml("3"), remote: false, null);
        Assert.Equal(4, made.Count);

        registry.CloseMessage("m1");
        Assert.True(made[0].Closed);
        Assert.False(made[1].Closed);
        Assert.True(made[2].Closed);
        Assert.False(made[3].Closed);
        Assert.Equal(2, registry.Keys.Count);
        Assert.DoesNotContain(made[0].Reader, registry.Readers);
    }

    [Fact]
    public async Task WhatTheCacheLearnsReachesEveryViewOfTheMessageButAttachedOnes()
    {
        var pane = new ReaderController(ReaderMode.Pane, cache, timeProvider: clock);
        registry.Track(pane);
        var s = Summary("m1");
        pane.Show(s);
        registry.OpenMessage(s);
        cache.Embedded = _ => Embedded("Inner");
        await registry.OpenEmbeddedAsync(s, Eml("2"), remote: false, null);
        var window = made[0];
        var attached = made[1];

        var lm = cache.Entry("m1");
        lm.Body = HtmlBody("m1", "<p>x</p>", remoteImages: 2);
        registry.ShowLoaded("m1", lm);
        Assert.Equal("<p>x</p>", pane.Html);
        Assert.Equal("<p>x</p>", window.Reader.Html);
        Assert.Equal("inner", attached.Reader.BodyText);

        lm.LoadingImages = true;
        registry.RefreshRemoteBar("m1", lm);
        Assert.True(pane.RemoteBarLoading);
        Assert.True(window.Reader.RemoteBarLoading);
        Assert.False(attached.Reader.RemoteBarLoading);

        lm.Msg = Message(s with { Outbox = new OutboxInfo { State = OutboxState.Sending, Attempts = 1 } });
        registry.ShowOutboxState("m1");
        Assert.True(pane.OutboxVisible);
        Assert.Equal("Sending…", window.Reader.OutboxTitle);

        // download.go refreshChips: the chips of every view of the message,
        // attached ones left out.
        lm.Msg = Message(s, [Attachment("3", "big.pdf", size: 300_000) with { Remote = true }]);
        cache.Spinning.Add("m1");
        registry.RefreshChips("m1", lm);
        Assert.True(pane.Chips[0].Downloading);
        Assert.True(window.Reader.Chips[0].Downloading);
        Assert.DoesNotContain(attached.Reader.Chips, c => c.Downloading);
        cache.Spinning.Remove("m1");
        registry.RefreshChips("m1", null); // the cache let it go: what each drew last
        Assert.False(pane.Chips[0].Downloading);
        Assert.False(window.Reader.Chips[0].Downloading);

        // Another message's news reach nobody here.
        registry.ShowLoaded("m2", new LoadedMessage { Body = TextBody("m2", "other") });
        Assert.Equal("<p>x</p>", pane.Html);
        cache.Spinning.Add("m2");
        registry.RefreshChips("m2", null);
        Assert.False(pane.Chips[0].Downloading);

        registry.RefreshActions();
        Assert.Equal(1, window.Refreshed);
        Assert.Equal(1, attached.Refreshed);
    }

    private sealed class FakeWindow(ReaderController reader, MessageWindowRegistry registry) : IMessageWindowHandle
    {
        public ReaderController Reader { get; } = reader;

        public int Presented { get; private set; }

        public int Refreshed { get; private set; }

        public bool Closed { get; private set; }

        public void Present() => Presented++;

        public void RefreshActions() => Refreshed++;

        // As the app's windows do: the close reports itself.
        public void Close()
        {
            Closed = true;
            foreach (var key in new List<MessageWindowKey>(registry.Keys))
            {
                if (ReferenceEquals(registry.Window(key), this))
                {
                    registry.Closed(key, this);
                }
            }
            Reader.Close();
        }
    }
}
