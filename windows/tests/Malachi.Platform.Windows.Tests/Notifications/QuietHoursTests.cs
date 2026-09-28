// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of QuietHours: which SHQueryUserNotificationState answers keep the
// new-mail sound quiet (the blocked states and quiet time, not a Store app
// in front), that Do Not Disturb (a ToastNotificationMode other than
// Unrestricted) does on its own, that what Windows cannot say does not, and
// that the real calls answer.

using Malachi.Platform.Windows.Notifications;
using Xunit;

namespace Malachi.Platform.Windows.Tests.Notifications;

public sealed class QuietHoursTests
{
    private const int Unrestricted = 0;
    private const int PriorityOnly = 1;
    private const int AlarmsOnly = 2;

    [Theory]
    [InlineData(1, true)] // QUNS_NOT_PRESENT: screen saver, locked, another user's session
    [InlineData(2, true)] // QUNS_BUSY: a full-screen application
    [InlineData(3, true)] // QUNS_RUNNING_D3D_FULL_SCREEN: a game
    [InlineData(4, true)] // QUNS_PRESENTATION_MODE
    [InlineData(5, false)] // QUNS_ACCEPTS_NOTIFICATIONS
    [InlineData(6, true)] // QUNS_QUIET_TIME: the first hour after a first sign-in
    [InlineData(7, false)] // QUNS_APP: a Store app in front, in an ordinary window
    [InlineData(0, false)] // not a state
    [InlineData(99, false)] // a state Windows adds later
    public void TheShellsBlockedStatesAndQuietTimeAreQuiet(int state, bool quiet)
    {
        Assert.Equal(quiet, QuietHours.IsQuietState(state));
        Assert.Equal(quiet, QuietHours.IsQuiet(state, Unrestricted));
        Assert.Equal(quiet, QuietHours.IsQuiet(state, null));
    }

    [Theory]
    [InlineData(PriorityOnly)]
    [InlineData(AlarmsOnly)]
    [InlineData(3)] // a mode Windows adds later is not Unrestricted either
    public void DoNotDisturbIsQuietWhateverTheShellSays(int mode)
    {
        Assert.True(QuietHours.IsQuiet(5, mode));
        Assert.True(QuietHours.IsQuiet(7, mode));
        Assert.True(QuietHours.IsQuiet(null, mode));
    }

    [Fact]
    public void WhatWindowsCannotSayIsNotQuiet()
    {
        Assert.False(QuietHours.IsQuiet(null, null));
        Assert.False(QuietHours.IsQuiet(null, Unrestricted));
        Assert.False(QuietHours.IsQuiet(5, null));
    }

    [Fact]
    public void TheRealCallsAnswer()
    {
        // Whatever this desktop's state, the calls work and do not throw (a
        // session without a shell may have no answer).
        var state = QuietHours.ShellState();
        Assert.True(state is null or >= 1);
        var mode = QuietHours.ToastModeNow();
        Assert.True(mode is null or >= Unrestricted and <= AlarmsOnly);
        _ = QuietHours.IsQuiet();
    }
}
