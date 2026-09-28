// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Quit (docs/windows-port.md §10, QuitSequence) from the primary menu: the
// app exits with 0, the daemon it started stops (CTRL_BREAK, never a kill:
// the daemon then removes its socket and its key file itself).

using System.IO;
using Xunit;

namespace Malachi.App.UiTests;

/// <summary>A clean Quit.</summary>
[Collection(OneAppAtATime.Name)]
public sealed class QuitTests
{
    [Fact]
    public void QuitStopsTheDaemonAndRemovesTheSocketAndItsKey()
    {
        Assert.SkipWhen(UiEnvironment.SkipReason is not null, UiEnvironment.SkipReason ?? "");
        using var app = AppSession.Start();
        Assert.NotNull(app.MainWindow);
        using var daemon = app.WaitForDaemon();
        Assert.True(File.Exists(app.Socket));

        app.MenuItem("MenuQuit");

        Assert.True(app.WaitForExit(AppSession.QuitTimeout), "the app did not exit:\n" + app.Terminal);
        Assert.Equal(0, app.ExitCode);
        Assert.True(daemon.WaitForExit(10_000), "the daemon was left running");
        Assert.Equal(0, daemon.ExitCode);
        Assert.False(File.Exists(app.KeyFile), "the key file was left behind");
        Assert.False(File.Exists(app.Socket), "the socket was left behind");
    }
}
