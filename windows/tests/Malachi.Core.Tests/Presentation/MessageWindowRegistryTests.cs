// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of MessageWindowRegistry: ui/internal/window/message_view.go
// (openMessageWindow, closeMessageWindow), embedded.go (openEmbeddedWindow,
// closeEmbeddedWindows), remote.go (showLoaded, refreshRemoteBar),
// outbox.go (showOutboxState), actions.go (refreshStars, refreshSeen) and
// macos MessageWindows.swift and WindowRegistry.swift, with windows that
// only record what they were asked.

using System.Collections.Generic;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
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
        await registry.OpenEmbeddedAsync(containing, "2", "chip window");
        Assert.Empty(made);
        Assert.Equal([("chip window", "Opening the attached message failed")], toasts);

        cache.Embedded = _ => Embedded("Inner");
        await registry.OpenEmbeddedAsync(containing, "2", null);
        var w = Assert.Single(made);
        Assert.Equal("Inner", w.Reader.Subject);
        Assert.Equal(1, w.Presented);

        // Open again: raised, not fetched.
        await registry.OpenEmbeddedAsync(containing, "2", null);
        Assert.Single(made);
        Assert.Equal(2, w.Presented);
        Assert.Equal(2, cache.EmbeddedCalls.Count);
    }

    [Fact]
    public async Task ASecondClickThatOvertookTheFirstRaisesItsWindow()
    {
        var containing = Summary("m1");
        cache.EmbeddedGate = new TaskCompletionSource();
        cache.Embedded = _ => Embedded("Inner");
        var first = registry.OpenEmbeddedAsync(containing, "2", null);
        var second = registry.OpenEmbeddedAsync(containing, "2", null);
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
        await registry.OpenEmbeddedAsync(Summary("m1"), "2", null);
        await registry.OpenEmbeddedAsync(Summary("m2"), "3", null);
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
        await registry.OpenEmbeddedAsync(s, "2", null);
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

        // Another message's news reach nobody here.
        registry.ShowLoaded("m2", new LoadedMessage { Body = TextBody("m2", "other") });
        Assert.Equal("<p>x</p>", pane.Html);

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
