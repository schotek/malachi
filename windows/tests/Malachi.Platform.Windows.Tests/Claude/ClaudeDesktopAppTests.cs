// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of ClaudeDesktopApp, Claude Desktop as Windows sees it. They never
// touch the real Claude Desktop (a Claude Code session inside it would end
// with it): they ask about a package family no one has, the test host's
// own process, and the pure choice of the roots. The quit through the
// Restart Manager was measured by hand with another Electron app (see the
// file's header) and is checked by hand with Claude Desktop.

using System;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Platform.Windows.Claude;
using Xunit;

namespace Malachi.Platform.Windows.Tests.Claude;

public sealed class ClaudeDesktopAppTests
{
    // A package family no one has installed.
    private const string Nobody = "Malachi.Nobody_0000000000000";

    [Fact]
    public void TheStoresPackageIsTheDefault()
    {
        var app = new ClaudeDesktopApp();
        Assert.Equal("Claude_pzs8sxrjxfjjc", app.PackageFamily);
        Assert.Equal("Claude_pzs8sxrjxfjjc!Claude", app.AppUserModelId);
    }

    [Fact]
    public async Task APackageThatDoesNotRunQuitsAtOnce()
    {
        var app = new ClaudeDesktopApp(Nobody);
        Assert.False(app.IsRunning());
        Assert.Empty(app.Processes());
        Assert.True(await app.QuitAsync(TimeSpan.FromSeconds(1), TestContext.Current.CancellationToken));
        using var waited = new CancellationTokenSource(TimeSpan.FromSeconds(5));
        await app.WaitForExitAsync(waited.Token);
    }

    [Fact]
    public void TheTestHostBelongsToNoPackage()
    {
        Assert.Equal("", ClaudeDesktopApp.FamilyOf(Environment.ProcessId));
    }

    [Fact]
    public void AProcessThatIsGoneHasNoFamily()
    {
        // Process ids are multiples of four; an odd one never exists.
        Assert.Null(ClaudeDesktopApp.FamilyOf(3));
    }

    [Fact]
    public void TheRootsAreTheProcessesWhoseParentIsNotTheirs()
    {
        // Two instances: 10 (started by 1) with helpers 11 and 12 (12 a
        // child of 11), and 20 (started by 2) with helper 21.
        (int, int)[] processes = [(11, 10), (10, 1), (12, 11), (21, 20), (20, 2)];
        Assert.Equal([10, 20], ClaudeDesktopApp.Roots(processes));
        Assert.Empty(ClaudeDesktopApp.Roots([]));
    }

    [Fact]
    public void LaunchingAnAppThatIsNotInstalledFails()
    {
        Assert.False(new ClaudeDesktopApp(Nobody).Launch());
    }
}
