// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of BridgeRunnerTests in macos/Tests/MalachiCoreTests/
// MCPRegistrationTests.swift, and of the process cases of
// ui/internal/mcpsetup/mcpsetup_test.go (TestSubcommandsPassJSONFlag,
// TestTimeoutKillsTheBridge, TestMissingBridge; the report parsing is the
// MCP registration controller's), against Malachi.FakeBridge. Windows
// differences: a kill leaves -1 where Swift reports -9 (and a crash its
// NTSTATUS); a timeout ends the whole tree at once, which the child that
// holds the pipes shows. Added: both streams at their cap at once (they
// are read concurrently), an orphan holding the pipes after a normal exit
// (the EOF grace), the caller's cancellation, and the stand-in's own rules.

using System;
using System.Diagnostics;
using System.Globalization;
using System.IO;
using System.Linq;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Platform;
using Malachi.FakeBridge;
using Microsoft.Extensions.Time.Testing;
using Xunit;

namespace Malachi.Core.Tests.Platform;

public sealed class BridgeRunnerTests
{
    // How long the stand-in and its child hold at most: longer than a test
    // waits for anything, so that a hold running out cannot stand in for
    // the kill.
    private const int HeldFor = 120_000;

    private static readonly string[] StatusJson = ["status", "--json"];

    // How long a test waits for the stand-in's processes to start.
    private static readonly TimeSpan ProcessStart = TimeSpan.FromSeconds(60);

    // How long a run may take to end once its tree was killed; only a tree
    // that survived takes longer (with a fake clock, for ever).
    private static readonly TimeSpan TreeEnd = TimeSpan.FromSeconds(30);

    private static string Text(ReadOnlyMemory<byte> bytes) => Encoding.UTF8.GetString(bytes.Span);

    private static string Bridge(TemporaryDirectory directory, FakeBridgeScript script) => script.CreateIn(directory.Path);

    [Fact]
    public async Task CollectsBothStreamsAndTheExitStatus()
    {
        using var dir = new TemporaryDirectory();
        var bridge = Bridge(dir, new([FakeBridgeStep.Stdout("out\n"), FakeBridgeStep.Stderr("err\n"), FakeBridgeStep.Exit(3)]));
        var r = await new BridgeRunner().RunAsync(bridge, StatusJson, TimeSpan.FromSeconds(5), TestContext.Current.CancellationToken);
        Assert.Equal("out\n", Text(r.Stdout));
        Assert.Equal("err\n", Text(r.Stderr));
        Assert.Equal(3, r.Status);
        Assert.Equal(["status --json"], FakeBridgeScript.Calls(dir.Path));
    }

    [Fact]
    public async Task OutputIsCapped()
    {
        // 3 MiB of x: the first MiB is kept, the rest read and dropped.
        using var dir = new TemporaryDirectory();
        var bridge = Bridge(dir, new([FakeBridgeStep.Fill((byte)'x', 3 << 20)]));
        var r = await new BridgeRunner().RunAsync(bridge, StatusJson, TimeSpan.FromSeconds(20), TestContext.Current.CancellationToken);
        Assert.Equal(BridgeRunner.MaxOutput, r.Stdout.Length);
        Assert.True(r.Stdout.Span.IndexOfAnyExcept((byte)'x') < 0);
        Assert.Equal(0, r.Status);
    }

    [Fact]
    public async Task BothStreamsAreDrainedAtOnce()
    {
        // A child that fills stderr while stdout is still being written
        // would block for ever on a runner that reads one pipe at a time.
        using var dir = new TemporaryDirectory();
        var bridge = Bridge(dir, new([
            FakeBridgeStep.Fill((byte)'e', 3 << 20, toStderr: true),
            FakeBridgeStep.Fill((byte)'o', 3 << 20),
            FakeBridgeStep.Exit(4),
        ]));
        var r = await new BridgeRunner().RunAsync(bridge, StatusJson, TimeSpan.FromSeconds(20), TestContext.Current.CancellationToken);
        Assert.Equal(BridgeRunner.MaxOutput, r.Stdout.Length);
        Assert.Equal(BridgeRunner.MaxOutput, r.Stderr.Length);
        Assert.True(r.Stderr.Span.IndexOfAnyExcept((byte)'e') < 0);
        Assert.Equal(4, r.Status);
    }

    [Fact]
    public async Task AKillIsReportedAsMinusOne()
    {
        // Swift: "kill -9 $$" gives -9; TerminateProcess leaves -1.
        Assert.SkipUnless(OperatingSystem.IsWindows(), "the status of a kill on Windows");
        using var dir = new TemporaryDirectory();
        var bridge = Bridge(dir, new([FakeBridgeStep.KillSelf()]));
        var r = await new BridgeRunner().RunAsync(bridge, StatusJson, TimeSpan.FromSeconds(5), TestContext.Current.CancellationToken);
        Assert.Equal(-1, r.Status);
    }

    [Fact]
    public async Task ACrashIsItsNtStatus()
    {
        Assert.SkipUnless(OperatingSystem.IsWindows(), "an NTSTATUS is a Windows exit status");
        using var dir = new TemporaryDirectory();
        var bridge = Bridge(dir, new([FakeBridgeStep.Exit(unchecked((int)0xC0000005))]));
        var r = await new BridgeRunner().RunAsync(bridge, StatusJson, TimeSpan.FromSeconds(5), TestContext.Current.CancellationToken);
        Assert.Equal(unchecked((int)0xC0000005), r.Status);
        Assert.Equal("died with status 0xC0000005", Malachi.Core.Daemon.ExitStatus.Describe(r.Status));
    }

    [Fact]
    public async Task AMissingExecutableThrows()
    {
        using var dir = new TemporaryDirectory();
        var e = await Assert.ThrowsAsync<BridgeRunnerException>(() => new BridgeRunner().RunAsync(
            Path.Combine(dir.Path, FakeBridgeScript.BridgeFileName), StatusJson, TimeSpan.FromSeconds(5), TestContext.Current.CancellationToken));
        Assert.Equal(BridgeRunnerFailure.Launch, e.Failure);
        Assert.StartsWith("malachi-mcp could not be started: ", e.Message, StringComparison.Ordinal);
        Assert.DoesNotContain(dir.Path, e.Message, StringComparison.Ordinal);
    }

    [Fact]
    public async Task ATimeoutKillsTheProcess()
    {
        using var dir = new TemporaryDirectory();
        var bridge = Bridge(dir, new([FakeBridgeStep.Sleep(30_000)]));
        var clock = Stopwatch.StartNew();
        var e = await Assert.ThrowsAsync<BridgeRunnerException>(() => new BridgeRunner().RunAsync(
            bridge, StatusJson, TimeSpan.FromMilliseconds(300), TestContext.Current.CancellationToken));
        Assert.True(clock.Elapsed < TimeSpan.FromSeconds(3), $"took {clock.Elapsed}");
        Assert.Equal(BridgeRunnerFailure.Timeout, e.Failure);
        Assert.Equal("malachi-mcp did not finish within 0.3 s", e.Message);
        Assert.Equal(TimeSpan.FromMilliseconds(300), e.Timeout);
    }

    [Fact]
    public async Task SubcommandsPassJsonFlag()
    {
        // The bridge reports its own arguments as the command.
        using var dir = new TemporaryDirectory();
        var echo = new[] { FakeBridgeStep.EchoArguments() };
        var bridge = Bridge(dir, new(echo, echo, echo));
        foreach (var sub in new[] { "status", "install", "uninstall" })
        {
            var r = await new BridgeRunner().RunAsync(bridge, [sub, "--json"], TimeSpan.FromSeconds(5), TestContext.Current.CancellationToken);
            Assert.Equal("{\"command\":\"" + sub + " --json\",\"clients\":[]}\n", Text(r.Stdout));
            Assert.Equal(0, r.Status);
        }
        Assert.Equal(["status --json", "install --json", "uninstall --json"], FakeBridgeScript.Calls(dir.Path));
    }

    [Fact]
    public async Task TimeoutKillsTheBridge()
    {
        // The bridge's child keeps the pipes open and would outlive the
        // bridge: the whole tree goes, and the run ends with it.
        using var dir = new TemporaryDirectory();
        var hold = Path.Combine(dir.Path, "hold");
        File.WriteAllBytes(hold, []);
        var bridge = Bridge(dir, new(
            [FakeBridgeStep.Stdout("{}\n")],
            install: [FakeBridgeStep.HoldingChild(hold, HeldFor), FakeBridgeStep.Hold(hold, HeldFor)]));
        // Go's 200 ms deadline on the runner's clock, which moves once the
        // tree is up: the child must be running when the deadline passes,
        // and two .NET processes may take seconds to start under a full
        // parallel test run (a real 1.5 s deadline was not always enough,
        // TimeoutKillsTheBridge failed). The clock also stands still for the
        // EOF grace, so the run can end only by the pipes closing: the
        // bridge and its child both gone.
        var time = new FakeTimeProvider();
        var deadline = TimeSpan.FromMilliseconds(200);
        try
        {
            var run = new BridgeRunner(time).RunAsync(bridge, ["install", "--json"], deadline, TestContext.Current.CancellationToken);
            var child = await ChildPidAsync(hold, run);
            time.Advance(deadline);
            var ended = await Task.WhenAny(run, Task.Delay(TreeEnd, TestContext.Current.CancellationToken)) == run;
            Assert.True(ended, "the run did not end at its deadline: part of the bridge's tree still holds the pipes");
            var e = await Assert.ThrowsAsync<BridgeRunnerException>(() => run);
            Assert.Equal(BridgeRunnerFailure.Timeout, e.Failure);
            Assert.Equal("malachi-mcp did not finish within 0.2 s", e.Message);
            Assert.True(HasExited(child), "the bridge's child survived the timeout");
        }
        finally
        {
            // Lets a child that survived go.
            File.Delete(hold);
        }
    }

    [Fact]
    public async Task AChildHoldingThePipesDoesNotHoldTheResult()
    {
        // The bridge answers and exits; its child keeps the pipes open. The
        // drains give up EofGrace after the exit.
        using var dir = new TemporaryDirectory();
        var hold = Path.Combine(dir.Path, "hold");
        File.WriteAllBytes(hold, []);
        var bridge = Bridge(dir, new([FakeBridgeStep.HoldingChild(hold), .. FakeBridgeStep.Prints("{\"command\":\"x\",\"clients\":[]}")]));
        try
        {
            var clock = Stopwatch.StartNew();
            var r = await new BridgeRunner().RunAsync(bridge, StatusJson, TimeSpan.FromSeconds(10), TestContext.Current.CancellationToken);
            Assert.True(clock.Elapsed < TimeSpan.FromSeconds(5), $"took {clock.Elapsed}");
            Assert.Equal(0, r.Status);
            Assert.Equal("{\"command\":\"x\",\"clients\":[]}\n", Text(r.Stdout));
        }
        finally
        {
            File.Delete(hold);
        }
        var child = await ChildPidAsync(hold);
        await WaitForExitAsync(child);
    }

    [Fact]
    public async Task CancellationKillsTheRun()
    {
        using var dir = new TemporaryDirectory();
        var bridge = Bridge(dir, new([FakeBridgeStep.Sleep(30_000)]));
        using var cancel = CancellationTokenSource.CreateLinkedTokenSource(TestContext.Current.CancellationToken);
        cancel.CancelAfter(TimeSpan.FromMilliseconds(200));
        var clock = Stopwatch.StartNew();
        await Assert.ThrowsAnyAsync<OperationCanceledException>(() => new BridgeRunner().RunAsync(bridge, StatusJson, TimeSpan.FromSeconds(30), cancel.Token));
        Assert.True(clock.Elapsed < TimeSpan.FromSeconds(3), $"took {clock.Elapsed}");
    }

    [Fact]
    public async Task MissingBridge()
    {
        using var dir = new TemporaryDirectory();
        var e = await Assert.ThrowsAsync<BridgeRunnerException>(() => new BridgeRunner().RunAsync(
            Path.Combine(dir.Path, "malachi-mcp"), StatusJson, TimeSpan.FromSeconds(5), TestContext.Current.CancellationToken));
        Assert.NotEqual(BridgeRunnerFailure.Timeout, e.Failure);
    }

    [Fact]
    public async Task TheStandInBehavesAsTheScriptOfTheSwiftTests()
    {
        // The guard of the #!/bin/sh stand-in, its default of exit 9, and
        // the flag file that fails the first status and answers the second.
        using var dir = new TemporaryDirectory();
        var flag = Path.Combine(dir.Path, "flag");
        var bridge = Bridge(dir, new([
            FakeBridgeStep.IfExists(
                flag,
                FakeBridgeStep.Prints("{}"),
                [FakeBridgeStep.Touch(flag), .. FakeBridgeStep.Fails("read config: permission denied")]),
        ]));
        var runner = new BridgeRunner();
        var cancellationToken = TestContext.Current.CancellationToken;
        var noJson = await runner.RunAsync(bridge, ["status"], TimeSpan.FromSeconds(5), cancellationToken);
        Assert.Equal((2, "expected --json, got \n"), (noJson.Status, Text(noJson.Stderr)));
        var unknown = await runner.RunAsync(bridge, ["bogus", "--json"], TimeSpan.FromSeconds(5), cancellationToken);
        Assert.Equal((2, "unknown command bogus\n"), (unknown.Status, Text(unknown.Stderr)));
        var install = await runner.RunAsync(bridge, ["install", "--json"], TimeSpan.FromSeconds(5), cancellationToken);
        Assert.Equal(9, install.Status);
        var first = await runner.RunAsync(bridge, StatusJson, TimeSpan.FromSeconds(5), cancellationToken);
        Assert.Equal((1, "read config: permission denied\n", ""), (first.Status, Text(first.Stderr), Text(first.Stdout)));
        var second = await runner.RunAsync(bridge, StatusJson, TimeSpan.FromSeconds(5), cancellationToken);
        Assert.Equal((0, "{}\n"), (second.Status, Text(second.Stdout)));
        Assert.Equal(["status", "bogus --json", "install --json", "status --json", "status --json"], FakeBridgeScript.Calls(dir.Path));
    }

    // The process ID the bridge's child wrote once it ran. A run given as
    // well must not end first (the child would never come); the limit is
    // for two .NET processes to start, however busy the machine.
    private static async Task<int> ChildPidAsync(string hold, Task? run = null)
    {
        var path = hold + FakeBridgeScript.ChildPidSuffix;
        var limit = Stopwatch.StartNew();
        while (!File.Exists(path))
        {
            if (run is { IsCompleted: true })
            {
                await run;
                Assert.Fail("the run ended before the bridge's child was up");
            }
            Assert.True(limit.Elapsed < ProcessStart, "the bridge's child did not start");
            await Task.Delay(10, TestContext.Current.CancellationToken);
        }
        return int.Parse(File.ReadAllText(path), CultureInfo.InvariantCulture);
    }

    private static bool HasExited(int pid)
    {
        try
        {
            using var process = Process.GetProcessById(pid);
            return process.WaitForExit(TimeSpan.FromSeconds(5));
        }
        catch (ArgumentException)
        {
            return true; // gone
        }
    }

    private static async Task WaitForExitAsync(int pid)
    {
        try
        {
            using var process = Process.GetProcessById(pid);
            await process.WaitForExitAsync(TestContext.Current.CancellationToken).WaitAsync(TimeSpan.FromSeconds(10), TestContext.Current.CancellationToken);
        }
        catch (ArgumentException)
        {
            // Gone already.
        }
    }
}
