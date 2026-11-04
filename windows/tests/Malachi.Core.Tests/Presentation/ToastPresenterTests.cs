// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ToastPresenter (Core/Presentation): the queue of
// macos/Sources/MalachiMail/Shared/ToastPresenter.swift and Adw.ToastOverlay
// (window.go Toast and ToastFor), which macOS leaves untested in AppKit:
// one toast at a time, the rest in order, 5 s by default, 0 kept until
// replaced, a click dismisses; a toast's button (window/undo.go's Undo)
// dismisses it and then runs, and whoever showed a toast can take it away.
// The timeouts run on a fake clock.

using System;
using System.Collections.Generic;
using Malachi.Core.Presentation;
using Microsoft.Extensions.Time.Testing;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class ToastPresenterTests
{
    private readonly FakeTimeProvider clock = new();

    private (ToastPresenter Toasts, List<string> Shown) Make()
    {
        var toasts = new ToastPresenter(clock);
        var shown = new List<string>();
        toasts.CurrentChanged += (_, t) => shown.Add(t is null ? "-" : $"{t.Text}/{t.Seconds}");
        return (toasts, shown);
    }

    [Fact]
    public void AToastGoesAfterFiveSeconds()
    {
        var (toasts, shown) = Make();
        toasts.Show("Sent");
        Assert.Equal(new Toast("Sent", ToastPresenter.DefaultSeconds), toasts.Current);
        clock.Advance(TimeSpan.FromSeconds(4.9));
        Assert.NotNull(toasts.Current);
        clock.Advance(TimeSpan.FromSeconds(0.1));
        Assert.Null(toasts.Current);
        Assert.Equal(["Sent/5", "-"], shown);
    }

    [Fact]
    public void ToastsWaitTheirTurnInOrder()
    {
        var (toasts, shown) = Make();
        toasts.Show("one");
        toasts.Show("two", 2);
        toasts.Show("three");
        Assert.Equal(2, toasts.Waiting);
        clock.Advance(TimeSpan.FromSeconds(5));
        Assert.Equal("two", toasts.Current?.Text);
        clock.Advance(TimeSpan.FromSeconds(2));
        Assert.Equal("three", toasts.Current?.Text);
        clock.Advance(TimeSpan.FromSeconds(5));
        Assert.Null(toasts.Current);
        Assert.Equal(["one/5", "-", "two/2", "-", "three/5", "-"], shown);
    }

    [Fact]
    public void AToastWithoutTimeoutStaysUntilTheNextReplacesIt()
    {
        var (toasts, shown) = Make();
        toasts.Show("sticky", 0);
        clock.Advance(TimeSpan.FromHours(1));
        Assert.Equal("sticky", toasts.Current?.Text);
        toasts.Show("next");
        Assert.Equal("next", toasts.Current?.Text);
        Assert.Equal(0, toasts.Waiting);
        clock.Advance(TimeSpan.FromSeconds(5));
        Assert.Null(toasts.Current);
        Assert.Equal(["sticky/0", "next/5", "-"], shown);
    }

    [Fact]
    public void NegativeSecondsMeanNoTimeout()
    {
        var (toasts, _) = Make();
        toasts.Show("x", -3);
        Assert.Equal(0, toasts.Current?.Seconds);
    }

    [Fact]
    public void AClickDismissesAndTheNextFollowsWithItsOwnTime()
    {
        var (toasts, shown) = Make();
        toasts.Show("one");
        toasts.Show("two");
        clock.Advance(TimeSpan.FromSeconds(3));
        toasts.DismissCurrent();
        Assert.Equal("two", toasts.Current?.Text);
        // The first toast's timer no longer counts.
        clock.Advance(TimeSpan.FromSeconds(4));
        Assert.Equal("two", toasts.Current?.Text);
        clock.Advance(TimeSpan.FromSeconds(1));
        Assert.Null(toasts.Current);
        Assert.Equal(["one/5", "-", "two/5", "-"], shown);
        // Dismissing nothing does nothing.
        toasts.DismissCurrent();
        Assert.Equal(4, shown.Count);
    }

    [Fact]
    public void AButtonsClickDismissesThenRuns()
    {
        var (toasts, shown) = Make();
        var clicked = new List<string?>();
        var undo = toasts.Show(new Toast("Moved", 8, new ToastButton("Undo (Ctrl+Z)", () => clicked.Add(toasts.Current?.Text))));
        toasts.Show("next");
        Assert.Equal("Undo (Ctrl+Z)", toasts.Current?.Button?.Label);
        toasts.ClickButton();
        // The toast went first: the button ran with the next one shown.
        Assert.Equal(["next"], clicked);
        Assert.Equal(["Moved/8", "-", "next/5"], shown);
        // A toast without a button has no click.
        toasts.ClickButton();
        Assert.Equal("next", toasts.Current?.Text);
        Assert.Single(clicked);
        Assert.Equal(new Toast("Moved", 8, undo.Button), undo);
    }

    [Fact]
    public void DismissTakesAwayTheShownOrAWaitingToast()
    {
        var (toasts, shown) = Make();
        var first = toasts.Show(new Toast("one", 5));
        var second = toasts.Show(new Toast("two", 5));
        toasts.Show("three");
        toasts.Dismiss(second);
        Assert.Equal(1, toasts.Waiting);
        toasts.Dismiss(first);
        Assert.Equal("three", toasts.Current?.Text);
        // One already gone, or an equal copy, is left alone.
        toasts.Dismiss(first);
        toasts.Dismiss(new Toast("three", 5));
        Assert.Equal("three", toasts.Current?.Text);
        Assert.Equal(["one/5", "-", "three/5"], shown);
        // Negative seconds come back as the 0 shown, the instance to dismiss.
        var sticky = toasts.Show(new Toast("x", -1));
        Assert.Equal(0, sticky.Seconds);
        clock.Advance(TimeSpan.FromSeconds(5));
        Assert.Equal("x", toasts.Current?.Text);
        toasts.Dismiss(sticky);
        Assert.Null(toasts.Current);
    }

    [Fact]
    public void DisposeStopsTheTimerAndDropsTheQueue()
    {
        var (toasts, shown) = Make();
        toasts.Show("one");
        toasts.Show("two");
        toasts.Dispose();
        clock.Advance(TimeSpan.FromSeconds(10));
        Assert.Equal(["one/5"], shown);
        Assert.Equal(0, toasts.Waiting);
    }
}
