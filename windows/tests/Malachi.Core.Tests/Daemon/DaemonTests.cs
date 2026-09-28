// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/daemon/daemon_test.go. The test binary that doubles
// as the daemon there (fakeDaemon: listen, slow, exit, deaf) is
// Malachi.Core.TestDaemon; t.Setenv is the supervisor's BaseEnvironment
// and the shortened package variables are the supervisor's timeouts. The
// timeout case gives the slow daemon seconds rather than Go's 400 ms: a
// .NET process takes longer to start than a Go one, and the case is about
// a daemon that is alive but silent.

using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Daemon;
using Malachi.Core.TestDaemon;
using Xunit;

namespace Malachi.Core.Tests.Daemon;

public sealed class DaemonTests
{
    [Fact]
    public async Task EnsureUsesRunningDaemon()
    {
        await using var f = new DaemonFixture();
        f.Listen(f.Socket);
        var s = f.Track(new DaemonSupervisor(f.Launch(Path.Combine(f.Path, "nonexistent", DaemonSupervisor.ExecutableName)), f.Socket, f.Host));
        await s.EnsureAsync(TestContext.Current.CancellationToken);
        Assert.True(s.Spawns == 0 && !s.Running, $"spawned {s.Spawns}, running {s.Running}; expected the listening daemon to be used");
        await s.StopAsync(); // must not touch the foreign process
        Assert.True(DaemonFixture.Answers(f.Socket), "Stop closed a daemon it did not start");
    }

    [Fact]
    public async Task EnsureWithoutPath()
    {
        await using var f = new DaemonFixture();
        using var s = new DaemonSupervisor(null, f.Socket);
        var e = await Assert.ThrowsAsync<DaemonSupervisorException>(() => s.EnsureAsync(TestContext.Current.CancellationToken));
        Assert.Equal(DaemonSupervisorFailure.NoDaemon, e.Failure);
    }

    [Fact]
    public async Task EnsureStartsAndStops()
    {
        await using var f = new DaemonFixture();
        var s = f.Supervisor(TestDaemonSettings.Listen);
        var cancellationToken = TestContext.Current.CancellationToken;
        await s.EnsureAsync(cancellationToken);
        Assert.True(s.Running && DaemonFixture.Answers(f.Socket), $"running {s.Running}, answers {DaemonFixture.Answers(f.Socket)}");
        Assert.Equal(1, s.Spawns);
        // A second Ensure finds the daemon and does nothing.
        await s.EnsureAsync(cancellationToken);
        Assert.Equal(1, s.Spawns);

        await s.StopAsync();
        Assert.False(s.Running, "daemon still running after Stop");
        Assert.False(DaemonFixture.Answers(f.Socket), "socket still answers after Stop");
        // After Stop nothing is started any more.
        var e = await Assert.ThrowsAsync<DaemonSupervisorException>(() => s.EnsureAsync(cancellationToken));
        Assert.Equal(DaemonSupervisorFailure.Stopping, e.Failure);
        Assert.Equal(1, s.Spawns);
    }

    [Fact]
    public async Task EnsureWaitsForSlowDaemon()
    {
        await using var f = new DaemonFixture();
        var s = f.Supervisor(TestDaemonSettings.Slow);
        // Concurrent callers (the startup and the window's reconnect) share
        // one spawn and all return once the socket answers.
        var cancellationToken = TestContext.Current.CancellationToken;
        var calls = Enumerable.Range(0, 3).Select(_ => Task.Run(() => s.EnsureAsync(cancellationToken), cancellationToken)).ToArray();
        await Task.WhenAll(calls);
        Assert.True(s.Spawns == 1 && DaemonFixture.Answers(f.Socket), $"spawns = {s.Spawns}, answers {DaemonFixture.Answers(f.Socket)}");
    }

    [Fact]
    public async Task EnsureBacksOffAfterRepeatedExits()
    {
        await using var f = new DaemonFixture();
        var s = f.Supervisor(TestDaemonSettings.Exit);
        var cancellationToken = TestContext.Current.CancellationToken;
        var e = await Assert.ThrowsAsync<DaemonSupervisorException>(() => s.EnsureAsync(cancellationToken));
        Assert.Contains("before opening its socket", e.Message, StringComparison.Ordinal);
        // One exit is retried at once.
        e = await Assert.ThrowsAsync<DaemonSupervisorException>(() => s.EnsureAsync(cancellationToken));
        Assert.Contains("before opening its socket", e.Message, StringComparison.Ordinal);
        Assert.Equal(2, s.Spawns);
        // The second exit in a row starts the backoff: no spawn, an immediate error.
        var clock = Stopwatch.StartNew();
        e = await Assert.ThrowsAsync<DaemonSupervisorException>(() => s.EnsureAsync(cancellationToken));
        Assert.Contains("next start in", e.Message, StringComparison.Ordinal);
        Assert.Equal(2, s.Spawns);
        Assert.True(clock.Elapsed < TimeSpan.FromSeconds(1), $"Ensure blocked {clock.Elapsed} during backoff");
    }

    [Fact]
    public async Task EnsureTimesOut()
    {
        // A daemon slower than the timeout: alive, but the socket never comes.
        await using var f = new DaemonFixture();
        var s = f.Supervisor(
            TestDaemonSettings.Slow,
            startTimeout: TimeSpan.FromMilliseconds(300),
            extra: [(TestDaemonSettings.DelayEnv, "5000")]);
        var e = await Assert.ThrowsAsync<DaemonSupervisorException>(() => s.EnsureAsync(TestContext.Current.CancellationToken));
        Assert.Contains("did not open", e.Message, StringComparison.Ordinal);
        Assert.Equal(DaemonSupervisorFailure.StartTimeout, e.Failure);
        Assert.Equal("malachid did not open " + f.Socket + " within 0.3 s", e.Message);
    }

    [Fact]
    public async Task StopKillsDeafDaemon()
    {
        await using var f = new DaemonFixture();
        var s = f.Supervisor(TestDaemonSettings.Deaf, stopTimeout: TimeSpan.FromMilliseconds(300));
        await s.EnsureAsync(TestContext.Current.CancellationToken);
        var clock = Stopwatch.StartNew();
        await s.StopAsync();
        Assert.False(s.Running, "daemon survived Stop");
        Assert.True(clock.Elapsed < TimeSpan.FromSeconds(5), $"Stop took {clock.Elapsed}");
        Assert.Contains(f.Host.Lines, line => line.Contains("stdin closed", StringComparison.Ordinal));
        Assert.DoesNotContain(f.Host.Lines, line => line.StartsWith(TestDaemonSettings.ShuttingDown, StringComparison.Ordinal));
    }

    [Fact]
    public async Task StopWithoutDaemon()
    {
        await using var f = new DaemonFixture();
        using var s = new DaemonSupervisor(f.Launch(Path.Combine(f.Path, "nonexistent", DaemonSupervisor.ExecutableName)), f.Socket, f.Host);
        await s.StopAsync(); // no exception, nothing to do
    }

    [Fact]
    public async Task Locate()
    {
        // none
        Assert.Null(DaemonSupervisor.Locate(null, new Dictionary<string, string?> { ["MALACHI_DAEMON"] = "none" }));
        // explicit
        Assert.Equal(
            @"C:\opt\malachi\malachid.exe",
            DaemonSupervisor.Locate(null, new Dictionary<string, string?> { ["MALACHI_DAEMON"] = @"C:\opt\malachi\malachid.exe" }));
        // path
        await using var f = new DaemonFixture();
        var p = f.Program("bin", DaemonSupervisor.ExecutableName);
        Assert.Equal(p, DaemonSupervisor.Locate(Path.Combine(f.Path, "empty"), new Dictionary<string, string?> { ["PATH"] = Path.GetDirectoryName(p) }));
        // missing
        Directory.CreateDirectory(Path.Combine(f.Path, "other"));
        Assert.Throws<DaemonSupervisorException>(
            () => DaemonSupervisor.Locate(Path.Combine(f.Path, "empty"), new Dictionary<string, string?> { ["PATH"] = Path.Combine(f.Path, "other") }));
    }
}
