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
using Xunit;

namespace Malachi.Core.Tests.Platform;

public sealed class BridgeRunnerTests
{
    private static readonly string[] StatusJson = ["status", "--json"];

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
        // bridge: the whole tree goes, and the run ends promptly.
        using var dir = new TemporaryDirectory();
        var hold = Path.Combine(dir.Path, "hold");
        File.WriteAllBytes(hold, []);
        var bridge = Bridge(dir, new(
            [FakeBridgeStep.Stdout("{}\n")],
            install: [FakeBridgeStep.HoldingChild(hold), FakeBridgeStep.Hold(hold)]));
        // Go's 200 ms deadline, with room for two .NET processes to start
        // on top: the child must be up before the deadline, and under a
        // full parallel test run (four test assemblies and the WebView2
        // canary) 1.5 s was not always enough for that. What the test
        // measures is the end of the tree after the deadline, not how fast
        // processes start.
        var deadline = TimeSpan.FromSeconds(8);
        var clock = Stopwatch.StartNew();
        var e = await Assert.ThrowsAsync<BridgeRunnerException>(() => new BridgeRunner().RunAsync(
            bridge, ["install", "--json"], deadline, TestContext.Current.CancellationToken));
        Assert.Equal(BridgeRunnerFailure.Timeout, e.Failure);
        Assert.True(clock.Elapsed < deadline + TimeSpan.FromSeconds(2), $"Install took {clock.Elapsed} after an {deadline.TotalSeconds} s deadline");
        var child = await ChildPidAsync(hold);
        Assert.True(HasExited(child), "the bridge's child survived the timeout");
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

    private static async Task<int> ChildPidAsync(string hold)
    {
        var path = hold + FakeBridgeScript.ChildPidSuffix;
        for (var i = 0; i < 500 && !File.Exists(path); i++)
        {
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
