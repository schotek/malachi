// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// WindowActivation (Core/Presentation): GTK's w.IsActive() of notify.go and
// macOS's isKeyWindow, both false while another application has the
// foreground, kept apart from the window that was active last (where the
// application's toasts go).

using Malachi.Core.Presentation;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class WindowActivationTests
{
    private readonly object main = new();
    private readonly object compose = new();

    [Fact]
    public void AnActivatedWindowIsActiveUntilAnotherApplicationTakesTheForeground()
    {
        var a = new WindowActivation();
        Assert.False(a.IsActive(main));
        Assert.True(a.Activated(main));
        Assert.True(a.IsActive(main));

        // The user switches to the browser: the main window stays the last
        // active one, but is not active.
        a.Deactivated(main);
        Assert.False(a.IsActive(main));
        Assert.Null(a.Focused);
        Assert.Same(main, a.LastActive);

        // Back again: active, and no news for the toasts.
        Assert.False(a.Activated(main));
        Assert.True(a.IsActive(main));
    }

    [Fact]
    public void ActivatingAnotherWindowDeactivatesTheMainWindowInEitherOrder()
    {
        var a = new WindowActivation();
        a.Activated(main);

        // Deactivation first, as Win32 sends WM_ACTIVATE.
        a.Deactivated(main);
        Assert.True(a.Activated(compose));
        Assert.False(a.IsActive(main));
        Assert.True(a.IsActive(compose));
        Assert.Same(compose, a.LastActive);

        // Activation first: the late deactivation of the other window changes nothing.
        Assert.True(a.Activated(main));
        a.Deactivated(compose);
        Assert.True(a.IsActive(main));
        Assert.Same(main, a.LastActive);
    }

    [Fact]
    public void AClosedWindowIsNeitherActiveNorLastActive()
    {
        var a = new WindowActivation();
        a.Activated(main);
        a.Activated(compose);
        Assert.True(a.Closed(compose));
        Assert.Null(a.Focused);
        Assert.Null(a.LastActive);
        Assert.False(a.IsActive(compose));

        // A window that was not the last active one closes quietly.
        a.Activated(main);
        Assert.False(a.Closed(compose));
        Assert.True(a.IsActive(main));
    }

    [Fact]
    public void AClosedWindowThatWasOnlyInTheBackgroundKeepsTheOthers()
    {
        var a = new WindowActivation();
        a.Activated(compose);
        a.Deactivated(compose);
        Assert.True(a.Closed(compose));
        Assert.Null(a.LastActive);
        Assert.False(a.Closed(main));
    }
}
