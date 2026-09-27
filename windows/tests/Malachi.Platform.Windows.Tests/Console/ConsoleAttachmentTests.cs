// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only tests of the console recipe of docs/windows-port.md §5
// (ConsoleAttachment, ConsoleBreak, DaemonProcessHost), the measured runs of
// INPUT-SPIKES.md §4 replayed with the stand-in daemon, and with malachid
// when MALACHI_TEST_MALACHID names it: an app attached to its terminal
// stops a daemon that shares its console directly (runs 1, 13, 15); Ctrl+C
// in the terminal reaches the app and spares the daemon (runs 2, 2b); a
// daemon on a console of its own is reached by leaving the terminal's
// console and coming back (run 4); an app without a console borrows the
// daemon's (runs 7, 17); a launcher's pipe is where the log goes (run 11).
// The control handler's routing is tested directly; a closing tab
// (CTRL_CLOSE) cannot be produced without a pseudo console, so the hold of
// the handler is tested on its own and the daemon's own exit on CTRL_CLOSE
// stays the measured run 5 (with DaemonSupervisor.BeginStopping keeping it
// from being restarted, SupervisorStateTests).

using System;
using System.IO;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.TestDaemon;
using Malachi.Platform.Windows.Consoles;
using Malachi.Platform.Windows.Tests.Files;
using Malachi.Platform.Windows.Tests.Processes;
using Microsoft.Extensions.Time.Testing;
using Windows.Win32;
using Xunit;

namespace Malachi.Platform.Windows.Tests.Consoles;

public sealed class ConsoleAttachmentTests
{
    [Theory]
    [InlineData(PInvoke.CTRL_C_EVENT, ConsoleControl.Interrupt)]
    [InlineData(PInvoke.CTRL_BREAK_EVENT, ConsoleControl.Break)]
    public void CtrlCAndCtrlBreakGoToTheCallbackAndReturnAtOnce(uint ctrlType, ConsoleControl expected)
    {
        ConsoleControl? got = null;
        var attachment = new ConsoleAttachment(attached: true, terminal: null, kind => got = kind, new FakeTimeProvider());
        Assert.True(attachment.HandleControl(ctrlType));
        Assert.Equal(expected, got);
    }

    [Fact]
    public void AnUnknownEventIsNotOurs()
    {
        var called = false;
        var attachment = new ConsoleAttachment(attached: true, terminal: null, _ => called = true, new FakeTimeProvider());
        Assert.False(attachment.HandleControl(42));
        Assert.False(called);
    }

    [Theory]
    [InlineData(PInvoke.CTRL_CLOSE_EVENT, ConsoleControl.Close)]
    [InlineData(PInvoke.CTRL_LOGOFF_EVENT, ConsoleControl.Logoff)]
    [InlineData(PInvoke.CTRL_SHUTDOWN_EVENT, ConsoleControl.Shutdown)]
    public async Task ClosingHoldsTheProcessUntilTheShutdownIsComplete(uint ctrlType, ConsoleControl expected)
    {
        var got = new TaskCompletionSource<ConsoleControl>(TaskCreationOptions.RunContinuationsAsynchronously);
        var attachment = new ConsoleAttachment(attached: true, terminal: null, kind => got.TrySetResult(kind), new FakeTimeProvider());
        var handler = Task.Factory.StartNew(() => attachment.HandleControl(ctrlType), CancellationToken.None, TaskCreationOptions.LongRunning, TaskScheduler.Default);
        Assert.Equal(expected, await got.Task.WaitAsync(TimeSpan.FromSeconds(5), TestContext.Current.CancellationToken));
        await Assert.ThrowsAsync<TimeoutException>(() => handler.WaitAsync(TimeSpan.FromMilliseconds(100), TestContext.Current.CancellationToken));
        attachment.ShutdownCompleted();
        Assert.True(await handler.WaitAsync(TimeSpan.FromSeconds(5), TestContext.Current.CancellationToken));
    }

    [Fact]
    public async Task ClosingGivesUpAfterTheGrace()
    {
        var time = new FakeTimeProvider();
        var called = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var attachment = new ConsoleAttachment(attached: true, terminal: null, _ => called.TrySetResult(), time);
        var handler = Task.Factory.StartNew(() => attachment.HandleControl(PInvoke.CTRL_CLOSE_EVENT), CancellationToken.None, TaskCreationOptions.LongRunning, TaskScheduler.Default);
        await called.Task.WaitAsync(TimeSpan.FromSeconds(5), TestContext.Current.CancellationToken);
        // The grace was running before the callback was called.
        time.Advance(ConsoleAttachment.CloseGrace - TimeSpan.FromMilliseconds(1));
        await Assert.ThrowsAsync<TimeoutException>(() => handler.WaitAsync(TimeSpan.FromMilliseconds(100), TestContext.Current.CancellationToken));
        time.Advance(TimeSpan.FromMilliseconds(1));
        Assert.True(await handler.WaitAsync(TimeSpan.FromSeconds(5), TestContext.Current.CancellationToken));
        Assert.Equal(TimeSpan.FromMilliseconds(4500), ConsoleAttachment.CloseGrace);
    }

    [Fact]
    public void ACallbackThatThrowsStillHandlesTheEvent()
    {
        var attachment = new ConsoleAttachment(attached: true, terminal: null, _ => throw new InvalidOperationException("boom"), new FakeTimeProvider());
        Assert.True(attachment.HandleControl(PInvoke.CTRL_C_EVENT));
    }

    [Fact]
    public void AnAttachedAppStopsADaemonOnItsConsoleDirectly()
    {
        using var dir = new TestDirectory();
        var report = ConsoleRoles.Run(dir.Path);
        Assert.True(report.GetProperty("attached").GetBoolean());
        Assert.Equal("console", report.GetProperty("terminal").GetString());
        Assert.True(report.GetProperty("answered").GetBoolean());
        Assert.True(report.GetProperty("sharesConsole").GetBoolean(), "the daemon inherits the app's console");
        AssertCleanStop(report, "Direct");
        Assert.True(report.GetProperty("onTerminalConsole").GetBoolean());
        Assert.Contains(TestDaemonSettings.ShuttingDown + " reason=SIGQUIT", File.ReadAllText(Path.Combine(dir.Path, "daemon.log")), StringComparison.Ordinal);
    }

    [Fact]
    public void CtrlCInTheTerminalReachesTheAppAndSparesTheDaemon()
    {
        using var dir = new TestDirectory();
        var report = ConsoleRoles.Run(dir.Path, (ConsoleRoles.CtrlCEnv, "1"));
        Assert.True(report.GetProperty("attached").GetBoolean());
        Assert.Equal("Interrupt", report.GetProperty("control").GetString());
        Assert.True(report.GetProperty("daemonAliveAfterCtrlC").GetBoolean(), "the terminal's Ctrl+C reached the daemon");
        AssertCleanStop(report, "Direct");
    }

    [Fact]
    public void ADaemonOnAConsoleOfItsOwnIsReachedByLeavingTheTerminalsAndComingBack()
    {
        using var dir = new TestDirectory();
        var report = ConsoleRoles.Run(dir.Path, (ConsoleRoles.OwnConsoleEnv, "1"));
        Assert.True(report.GetProperty("attached").GetBoolean());
        Assert.False(report.GetProperty("sharesConsole").GetBoolean());
        AssertCleanStop(report, "Juggled");
        Assert.True(report.GetProperty("onTerminalConsole").GetBoolean(), "the app is back on the terminal's console");
        Assert.Equal("console", report.GetProperty("terminalAfter").GetString());
    }

    [Fact]
    public void AnAppWithoutAConsoleBorrowsTheDaemons()
    {
        using var dir = new TestDirectory();
        var report = ConsoleRoles.Run(dir.Path, (ConsoleRoles.NoConsoleEnv, "1"));
        Assert.False(report.GetProperty("attached").GetBoolean());
        Assert.Equal("none", report.GetProperty("terminal").GetString());
        Assert.False(report.GetProperty("sharesConsole").GetBoolean());
        AssertCleanStop(report, "Borrowed");
    }

    [Fact]
    public void ALaunchersPipeGetsTheLogLines()
    {
        using var dir = new TestDirectory();
        var report = ConsoleRoles.Run(dir.Path, (ConsoleRoles.CaptureEnv, "1"));
        Assert.True(report.GetProperty("attached").GetBoolean());
        Assert.Equal("pipe", report.GetProperty("terminal").GetString());
        AssertCleanStop(report, "Direct");
        var terminal = File.ReadAllText(Path.Combine(dir.Path, "report.json.terminal"));
        Assert.Contains("app: attached (žluťoučký kůň)", terminal, StringComparison.Ordinal);
        Assert.Contains("testdaemon: listening on", terminal, StringComparison.Ordinal);
        Assert.Contains("(žluťoučký kůň)", terminal[terminal.IndexOf("testdaemon: listening on", StringComparison.Ordinal)..], StringComparison.Ordinal);
        Assert.Contains(TestDaemonSettings.ShuttingDown, terminal, StringComparison.Ordinal);
        Assert.Contains("app: still writing after the stop", terminal, StringComparison.Ordinal);
    }

    [Fact]
    public void AnAttachedAppStopsTheRealDaemonCleanly()
    {
        var malachid = DaemonProcessHostTests.RealDaemonOrSkip();
        using var dir = new TestDirectory();
        var report = ConsoleRoles.Run(dir.Path, (ConsoleRoles.DaemonEnv, malachid));
        Assert.True(report.GetProperty("answered").GetBoolean());
        AssertCleanStop(report, "Direct");
        var log = File.ReadAllText(Path.Combine(dir.Path, "daemon.log"));
        Assert.Contains("shutting down", log, StringComparison.Ordinal);
    }

    private static void AssertCleanStop(JsonElement report, string path)
    {
        Assert.Equal(path, report.GetProperty("stopPath").GetString());
        Assert.True(report.GetProperty("stopDelivered").GetBoolean());
        Assert.False(report.GetProperty("killed").GetBoolean(), "the daemon had to be killed");
        Assert.Equal(0, report.GetProperty("exit").GetInt32());
        Assert.True(report.GetProperty("stopMs").GetInt64() < 5000, "the stop took " + report.GetProperty("stopMs").GetInt64() + " ms");
        Assert.False(report.GetProperty("socketLeft").GetBoolean(), "the socket was left");
        Assert.False(report.GetProperty("keyLeft").GetBoolean(), "the key file was left");
    }
}
