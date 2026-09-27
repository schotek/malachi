// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only tests of DaemonSupervisor's state machine on a fake clock
// and a fake process host: the numbers of the backoff of
// DaemonSupervisor.swift and daemon.go (backoff), which their tests only
// touch at the second exit; the start as the host sees it (arguments,
// environment); a start that fails; BeforeStart; the clean stop and the
// kill after StopTimeout; BeginStopping, the console's CTRL_CLOSE path;
// cancellation; the backoff's seconds in the message. EnsureAsync and
// StopAsync leave the caller's thread first, so the clock is advanced only
// once the supervisor is seen to wait on it (WatchedTimeProvider).

using System;
using System.Collections.Concurrent;
using System.Collections.Generic;
using System.ComponentModel;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Daemon;
using Malachi.Core.Tests.Fixtures;
using Xunit;

namespace Malachi.Core.Tests.Daemon;

public sealed class SupervisorStateTests
{
    private static readonly DaemonLaunch Launch = new()
    {
        Executable = @"C:\app\malachid.exe",
        Socket = @"C:\run\rpc.sock",
        Config = @"C:\data\config.toml",
        Store = @"C:\data\store.db",
    };

    // 1 s doubling from the second exit in a row, capped at 60 s.
    private static readonly int[] ExpectedBackoff = [1, 2, 4, 8, 16, 32, 60, 60];

    private sealed class Probe
    {
        public bool Answers { get; set; }

        public int Calls { get; private set; }

        public ValueTask<bool> Call(string socket, CancellationToken cancellationToken)
        {
            Calls++;
            return ValueTask.FromResult(Answers);
        }
    }

    private static (DaemonSupervisor Supervisor, FakeProcessHost Host, Probe Probe, WatchedTimeProvider Time) Make(
        Action? beforeStart = null, IReadOnlyDictionary<string, string?>? environment = null)
    {
        var host = new FakeProcessHost();
        var probe = new Probe();
        var time = new WatchedTimeProvider(new DateTimeOffset(2026, 9, 27, 12, 0, 0, TimeSpan.Zero));
        var supervisor = new DaemonSupervisor(Launch, Launch.Socket, host)
        {
            Time = time,
            Probe = probe.Call,
            BeforeStart = beforeStart,
            BaseEnvironment = environment ?? new Dictionary<string, string?> { ["PATH"] = @"C:\Windows" },
        };
        return (supervisor, host, probe, time);
    }

    // Starts a daemon that answers at the poll after its start.
    private static async Task StartAsync(DaemonSupervisor sup, Probe probe, WatchedTimeProvider time)
    {
        var polling = time.NextTimer();
        var ensure = sup.EnsureAsync(TestContext.Current.CancellationToken);
        await WatchedTimeProvider.WaitForAsync(polling, ensure);
        probe.Answers = true;
        time.Advance(DaemonSupervisor.DefaultPollInterval);
        await ensure;
    }

    [Fact]
    public async Task BackoffDoublesFromOneSecondToTheCap()
    {
        var (sup, host, _, time) = Make();
        host.ExitAtOnce = 1;
        var cancellationToken = TestContext.Current.CancellationToken;
        // The first exit is retried at once.
        await Assert.ThrowsAsync<DaemonSupervisorException>(() => sup.EnsureAsync(cancellationToken));
        var waits = new List<TimeSpan>();
        DaemonSupervisorException? last = null;
        for (var exits = 2; exits <= 9; exits++)
        {
            var e = await Assert.ThrowsAsync<DaemonSupervisorException>(() => sup.EnsureAsync(cancellationToken));
            Assert.Equal(DaemonSupervisorFailure.ExitedEarly, e.Failure);
            Assert.Equal(exits, sup.Spawns);
            last = await Assert.ThrowsAsync<DaemonSupervisorException>(() => sup.EnsureAsync(cancellationToken));
            Assert.Equal(DaemonSupervisorFailure.Backoff, last.Failure);
            Assert.Equal(exits, last.Failures);
            Assert.Equal(exits, sup.Spawns);
            waits.Add(last.RetryIn);
            // Not a moment before the pause is over.
            time.Advance(last.RetryIn - TimeSpan.FromMilliseconds(1));
            await Assert.ThrowsAsync<DaemonSupervisorException>(() => sup.EnsureAsync(cancellationToken));
            Assert.Equal(exits, sup.Spawns);
            time.Advance(TimeSpan.FromMilliseconds(1));
        }
        Assert.Equal(ExpectedBackoff.Select(s => TimeSpan.FromSeconds(s)), waits);
        Assert.Equal("malachid exited 9 times in a row; next start in 60 s", last!.Message);
        Assert.All(host.Started, p => Assert.True(p.Disposed, "an exited process is released"));
    }

    [Fact]
    public async Task AnAnsweringDaemonResetsTheFailures()
    {
        var (sup, host, probe, time) = Make();
        var cancellationToken = TestContext.Current.CancellationToken;
        host.ExitAtOnce = 1;
        await Assert.ThrowsAsync<DaemonSupervisorException>(() => sup.EnsureAsync(cancellationToken));
        await Assert.ThrowsAsync<DaemonSupervisorException>(() => sup.EnsureAsync(cancellationToken));
        time.Advance(TimeSpan.FromSeconds(1));

        // The third start answers: the count starts again.
        host.ExitAtOnce = null;
        probe.Answers = false;
        await StartAsync(sup, probe, time);
        Assert.Equal(3, sup.Spawns);
        Assert.True(sup.Running);

        // A crash after that is retried at once, as the first exit ever.
        probe.Answers = false;
        host.Started[^1].ExitWith(2);
        host.ExitAtOnce = 1;
        var e = await Assert.ThrowsAsync<DaemonSupervisorException>(() => sup.EnsureAsync(cancellationToken));
        Assert.Equal(DaemonSupervisorFailure.ExitedEarly, e.Failure);
        Assert.Equal(4, sup.Spawns);
    }

    [Fact]
    public async Task TheStartIsWhatTheDaemonExpects()
    {
        var environment = new Dictionary<string, string?> { ["Path"] = @"C:\Windows", ["MALACHI_KEYRING"] = "none" };
        var (sup, host, probe, time) = Make(environment: environment);
        host.ExitAtOnce = 0;
        await Assert.ThrowsAsync<DaemonSupervisorException>(() => sup.EnsureAsync(TestContext.Current.CancellationToken));
        var start = Assert.Single(host.Starts);
        Assert.Equal(Launch.Executable, start.Executable);
        Assert.Equal(["--socket", Launch.Socket, "--config", Launch.Config, "--store", Launch.Store], start.Arguments);
        Assert.Equal(@"C:\Windows", start.Environment["Path"]);
        Assert.Equal("none", start.Environment["MALACHI_KEYRING"]);
        Assert.Equal("disabled:", start.Environment["DBUS_SESSION_BUS_ADDRESS"]);
        Assert.Equal("malachid exited with status 0 before opening its socket",
            (await Assert.ThrowsAsync<DaemonSupervisorException>(() => sup.EnsureAsync(TestContext.Current.CancellationToken))).Message);
    }

    [Fact]
    public async Task AStartThatFailsCountsAsAnExit()
    {
        var (sup, host, _, time) = Make();
        var cancellationToken = TestContext.Current.CancellationToken;
        host.Fails = new Win32Exception(2, @"start C:\app\malachid.exe: The system cannot find the file specified.");
        var e = await Assert.ThrowsAsync<Win32Exception>(() => sup.EnsureAsync(cancellationToken));
        Assert.Contains("cannot find", e.Message, StringComparison.Ordinal);
        Assert.Equal(0, sup.Spawns);
        // A first failure is retried at once; a second one backs off.
        host.Fails = new Win32Exception(2);
        await Assert.ThrowsAsync<Win32Exception>(() => sup.EnsureAsync(cancellationToken));
        var backoff = await Assert.ThrowsAsync<DaemonSupervisorException>(() => sup.EnsureAsync(cancellationToken));
        Assert.Equal(DaemonSupervisorFailure.Backoff, backoff.Failure);
        Assert.Equal(TimeSpan.FromSeconds(1), backoff.RetryIn);
        Assert.Equal(2, host.Starts.Count);
    }

    [Fact]
    public async Task BeforeStartRunsBeforeEveryStartAndItsFailureCounts()
    {
        var calls = 0;
        var fail = false;
        var (sup, host, _, time) = Make(beforeStart: () =>
        {
            calls++;
            if (fail)
            {
                throw new UnauthorizedAccessException("the run directory could not be made private");
            }
        });
        var cancellationToken = TestContext.Current.CancellationToken;
        host.ExitAtOnce = 1;
        await Assert.ThrowsAsync<DaemonSupervisorException>(() => sup.EnsureAsync(cancellationToken));
        await Assert.ThrowsAsync<DaemonSupervisorException>(() => sup.EnsureAsync(cancellationToken));
        Assert.Equal(2, calls);
        time.Advance(TimeSpan.FromSeconds(1));
        fail = true;
        await Assert.ThrowsAsync<UnauthorizedAccessException>(() => sup.EnsureAsync(cancellationToken));
        Assert.Equal(3, calls);
        Assert.Equal(2, host.Starts.Count);
        Assert.Equal(TimeSpan.FromSeconds(2), (await Assert.ThrowsAsync<DaemonSupervisorException>(() => sup.EnsureAsync(cancellationToken))).RetryIn);
    }

    [Fact]
    public async Task StopAsksThenKillsAfterTheStopTimeout()
    {
        var (sup, host, probe, time) = Make();
        host.ExitOnStop = false;
        probe.Answers = false;
        await StartAsync(sup, probe, time);
        var daemon = Assert.Single(host.Started);

        var timeout = time.NextTimer();
        var stop = sup.StopAsync();
        await WatchedTimeProvider.WaitForAsync(timeout, stop);
        Assert.Equal(1, daemon.StopRequests);
        Assert.False(stop.IsCompleted);
        time.Advance(DaemonSupervisor.DefaultStopTimeout - TimeSpan.FromMilliseconds(1));
        Assert.False(stop.IsCompleted);
        Assert.False(daemon.Killed);
        time.Advance(TimeSpan.FromMilliseconds(1));
        await stop;
        Assert.True(daemon.Killed);
        Assert.False(sup.Running);
    }

    [Fact]
    public async Task ACleanStopIsNotKilled()
    {
        var (sup, host, probe, time) = Make();
        probe.Answers = false;
        await StartAsync(sup, probe, time);
        await sup.StopAsync();
        var daemon = Assert.Single(host.Started);
        Assert.Equal(1, daemon.StopRequests);
        Assert.False(daemon.Killed);
        // Stopped twice: nothing more is sent.
        await sup.StopAsync();
        Assert.Equal(1, daemon.StopRequests);
        sup.Dispose();
        Assert.True(daemon.Disposed);
    }

    [Fact]
    public async Task BeginStoppingKeepsADaemonThatStopsByItselfFromBeingRestarted()
    {
        var (sup, host, probe, time) = Make();
        probe.Answers = false;
        await StartAsync(sup, probe, time);
        var daemon = Assert.Single(host.Started);

        // CTRL_CLOSE: the app marks itself as quitting, the daemon got the
        // event too and exits on its own.
        sup.BeginStopping();
        probe.Answers = false;
        daemon.ExitWith(0);
        var e = await Assert.ThrowsAsync<DaemonSupervisorException>(() => sup.EnsureAsync(TestContext.Current.CancellationToken));
        Assert.Equal(DaemonSupervisorFailure.Stopping, e.Failure);
        Assert.Equal("the application is quitting", e.Message);
        Assert.Equal(1, sup.Spawns);
        await sup.StopAsync();
        Assert.Equal(0, daemon.StopRequests);
    }

    [Fact]
    public async Task EnsureCanBeCancelledWhileWaiting()
    {
        var (sup, host, probe, time) = Make();
        using var cancel = CancellationTokenSource.CreateLinkedTokenSource(TestContext.Current.CancellationToken);
        var polling = time.NextTimer();
        var ensure = sup.EnsureAsync(cancel.Token);
        await WatchedTimeProvider.WaitForAsync(polling, ensure);
        Assert.Equal(1, sup.Spawns);
        Assert.False(ensure.IsCompleted);
        await cancel.CancelAsync();
        await Assert.ThrowsAnyAsync<OperationCanceledException>(() => ensure);
        // The daemon still coming up is waited for by the next call.
        probe.Answers = true;
        await sup.EnsureAsync(TestContext.Current.CancellationToken);
        Assert.Equal(1, sup.Spawns);
        Assert.True(sup.Running);
    }

    [Fact]
    public async Task EnsureAndStopLeaveTheCallersThread()
    {
        // The caller is the UI thread (ConnectionController): the probe, the
        // start and the stop request run elsewhere, as a call into the Swift
        // actor runs off the main actor.
        using var ui = new TestUIContext();
        var host = new FakeProcessHost();
        var threads = new ConcurrentBag<int>();
        using var sup = new DaemonSupervisor(Launch, Launch.Socket, host)
        {
            Probe = (_, _) =>
            {
                threads.Add(Environment.CurrentManagedThreadId);
                return ValueTask.FromResult(host.Started.Count > 0);
            },
            BeforeStart = () => threads.Add(Environment.CurrentManagedThreadId),
            BaseEnvironment = new Dictionary<string, string?>(),
        };
        await await ui.RunAsync(() => sup.EnsureAsync(TestContext.Current.CancellationToken));
        await await ui.RunAsync(sup.StopAsync);
        var daemon = Assert.Single(host.Started);
        Assert.Equal(1, daemon.StopRequests);
        threads.Add(daemon.StopRequestThread);
        Assert.Equal(4, threads.Count);
        Assert.DoesNotContain(ui.ThreadId, threads);
        Assert.Empty(ui.Failures);
    }

    [Theory]
    [InlineData(1000, "1")]
    [InlineData(997, "1")]
    [InlineData(1499, "1")]
    [InlineData(1500, "2")]
    [InlineData(400, "0")]
    [InlineData(60000, "60")]
    public void TheBackoffMessageRoundsTheSecondsAsGoDoes(int milliseconds, string seconds)
    {
        // A 1 s pause read a few milliseconds later is still "1 s".
        var e = DaemonSupervisorException.Backoff(2, TimeSpan.FromMilliseconds(milliseconds));
        Assert.Equal("malachid exited 2 times in a row; next start in " + seconds + " s", e.Message);
        Assert.Equal(TimeSpan.FromMilliseconds(milliseconds), e.RetryIn);
    }

    [Fact]
    public void ALaunchNeedsAHost()
    {
        Assert.Throws<ArgumentNullException>(() => new DaemonSupervisor(Launch, Launch.Socket));
    }
}
