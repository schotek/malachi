// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// WindowLifetime (Core/Presentation): the GApplication rule of ui/main.go
// and window.go's close-request, as docs/windows-port.md §10 keeps it for
// the one main window of the WinUI app.

using Malachi.Core.Presentation;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class WindowLifetimeTests
{
    [Fact]
    public void ClosingTheMainWindowQuitsWhenNothingElseIsOpen()
    {
        var l = new WindowLifetime(startHidden: false);
        l.MainWindowShown();
        Assert.False(l.ShouldQuit);
        Assert.True(l.MainWindowClosed(runInBackground: false));
    }

    [Fact]
    public void RunInBackgroundKeepsTheAppWithItsHiddenWindow()
    {
        var l = new WindowLifetime(startHidden: false);
        l.MainWindowShown();
        Assert.False(l.MainWindowClosed(runInBackground: true));
        Assert.True(l.MainWindowAlive);
        // A compose window that comes and goes changes nothing.
        var compose = new object();
        l.WindowOpened(compose);
        Assert.False(l.WindowClosed(compose));
    }

    [Fact]
    public void AnOpenComposeWindowOutlivesTheMainWindow()
    {
        var l = new WindowLifetime(startHidden: false);
        l.MainWindowShown();
        var compose = new object();
        l.WindowOpened(compose);
        Assert.False(l.MainWindowClosed(runInBackground: false));
        Assert.Equal(1, l.OtherWindows);
        Assert.True(l.WindowClosed(compose));
    }

    [Fact]
    public void AColdMailtoQuitsWithItsComposer()
    {
        // GTK's open signal on a cold start: no main window, no hold.
        var l = new WindowLifetime(startHidden: false);
        var compose = new object();
        l.WindowOpened(compose);
        Assert.True(l.WindowClosed(compose));
    }

    [Fact]
    public void ABackgroundStartHoldsTheAppUntilTheMainWindowShows()
    {
        var l = new WindowLifetime(startHidden: true);
        Assert.True(l.Held);
        Assert.False(l.ShouldQuit);
        var compose = new object();
        l.WindowOpened(compose);
        Assert.False(l.WindowClosed(compose));
        // The first show releases the hold (GTK app.Release).
        l.MainWindowShown();
        Assert.False(l.Held);
        Assert.True(l.MainWindowClosed(runInBackground: false));
    }

    [Fact]
    public void AWindowCountsOnceAndOnlyOpenOnesCount()
    {
        var l = new WindowLifetime(startHidden: false);
        var w = new object();
        l.WindowOpened(w);
        l.WindowOpened(w);
        Assert.Equal(1, l.OtherWindows);
        Assert.True(l.WindowClosed(w));
        // A second close of the same window, or of one never opened, is no news.
        Assert.False(l.WindowClosed(w));
        Assert.False(l.WindowClosed(new object()));
    }

    [Fact]
    public void ShowingTheMainWindowAgainMakesItCountAgain()
    {
        var l = new WindowLifetime(startHidden: false);
        l.MainWindowShown();
        var compose = new object();
        l.WindowOpened(compose);
        Assert.False(l.MainWindowClosed(runInBackground: false));
        l.MainWindowShown();
        Assert.False(l.WindowClosed(compose));
        Assert.True(l.MainWindowAlive);
    }
}
