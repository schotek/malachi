// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of QuietHours: which SHQueryUserNotificationState answers keep the
// new-mail sound quiet (every one but QUNS_ACCEPTS_NOTIFICATIONS), and that
// the real call answers.

using Malachi.Platform.Windows.Notifications;
using Xunit;

namespace Malachi.Platform.Windows.Tests.Notifications;

public sealed class QuietHoursTests
{
    [Theory]
    [InlineData(1, true)] // QUNS_NOT_PRESENT: screen saver, locked, another user's session
    [InlineData(2, true)] // QUNS_BUSY: a full-screen application
    [InlineData(3, true)] // QUNS_RUNNING_D3D_FULL_SCREEN: a game
    [InlineData(4, true)] // QUNS_PRESENTATION_MODE
    [InlineData(5, false)] // QUNS_ACCEPTS_NOTIFICATIONS
    [InlineData(6, true)] // QUNS_QUIET_TIME
    [InlineData(7, true)] // QUNS_APP: an immersive application
    [InlineData(0, true)]
    [InlineData(99, true)]
    public void OnlyAcceptingNotificationsLetsTheSoundPlay(int state, bool quiet)
    {
        Assert.Equal(quiet, QuietHours.IsQuiet(state));
    }

    [Fact]
    public void TheShellAnswers()
    {
        // Whatever this desktop's state, the call works and does not throw.
        _ = QuietHours.IsQuiet();
    }
}
