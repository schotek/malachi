// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/ClaudeDesktopControllerTests.swift:
// Claude Desktop around a change of "Register with Claude", with a fake
// Claude Desktop (running or not, quitting or not) and fake writes that log
// in the same place, so the order of quit, write and launch shows. Nothing
// here touches the real Claude Desktop or its configuration. No Go
// counterpart: GTK has no equivalent.
//
// Windows differences: the quit timeout is 45 s; a held quit is a gate the
// test opens and a slow write one the test releases, where Swift sleeps;
// "nothing more happened" is the tracked work at rest (IdleAsync) instead of
// a 50 ms sleep; the own write's stand-in bridge is Malachi.FakeBridge.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Platform;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Tests.Platform;
using Malachi.FakeBridge;
using Xunit;

namespace Malachi.Core.Tests.Controllers;

[Collection(typeof(McpRegistrationProcesses))]
public sealed class ClaudeDesktopControllerTests
{
    [Fact]
    public async Task TheRestartIsOfferedWhileClaudeDesktopRunsAsAPresentClient()
    {
        using var h = await Harness.MakeAsync(running: true);
        await h.Ui.RunAsync(() =>
        {
            Assert.True(h.C.OffersRestart(Status(desktop: true)));
            Assert.True(h.C.OffersRestart(Status(desktop: false)));
            Assert.False(h.C.OffersRestart(Status(desktop: true, present: false)));
            Assert.False(h.C.OffersRestart(null));
            Assert.False(h.C.OffersRestart(new McpStatus { Command = "/x", Clients = [] }));
            h.Fake.Running = false;
            Assert.False(h.C.OffersRestart(Status(desktop: true)));
        });
        Assert.Empty(h.Fake.Log);
    }

    [Fact]
    public async Task RestartQuitsThenWritesThenLaunches()
    {
        using var h = await Harness.MakeAsync(running: true);
        await h.FlipAsync(true, ClaudeDesktopController.Answer.Restart);
        Assert.Equal(["ask", "quit", "page True", "launch"], h.Fake.Log);
        Assert.Equal([TimeSpan.FromSeconds(45)], h.Fake.Timeouts);
        Assert.True(h.Fake.Running);
        Assert.Null(h.C.Pending);
        Assert.False(h.C.IsBusy);
        Assert.Equal([true], h.Statuses);
        Assert.Empty(h.Toasts);
        Assert.Equal([true, false], h.Changes.Select(c => c.Busy));

        await h.FlipAsync(false, ClaudeDesktopController.Answer.Restart);
        Assert.Equal(["ask", "quit", "page False", "launch"], h.Fake.Log.TakeLast(4));
        Assert.Equal([true, false], h.Statuses);
    }

    [Fact]
    public async Task AQuitTimeoutWritesAnywayKeepsItPendingAndSaysSo()
    {
        using var h = await Harness.MakeAsync(running: true);
        h.Fake.Quits = false;
        await h.FlipAsync(true, ClaudeDesktopController.Answer.Restart);
        Assert.Equal(["ask", "quit", "page True"], h.Fake.Log);
        Assert.Equal(["Claude Desktop did not quit"], h.Toasts);
        Assert.True(h.C.Pending);
        Assert.Equal([true], h.Statuses);

        // It quits later after all: the change is written once more.
        h.Fake.Running = false;
        await h.Ui.RunAsync(h.C.Terminated);
        await h.IdleAsync();
        Assert.Null(h.C.Pending);
        Assert.Equal(["ask", "quit", "page True", "own True"], h.Fake.Log);
        Assert.Equal([true, true], h.Statuses);
    }

    [Fact]
    public async Task LaterWritesNowAndTheTerminationWritesOnceMore()
    {
        using var h = await Harness.MakeAsync(running: true);
        await h.FlipAsync(false, ClaudeDesktopController.Answer.Later);
        Assert.Equal(["ask", "page False"], h.Fake.Log);
        Assert.False(h.C.Pending);
        Assert.Equal([false], h.Statuses);
        Assert.False(h.C.IsBusy);

        // Claude Desktop quits by itself; the notice may come twice.
        h.Fake.Running = false;
        await h.Ui.RunAsync(() =>
        {
            h.C.Terminated();
            h.C.Terminated();
        });
        await h.IdleAsync();
        Assert.Null(h.C.Pending);
        Assert.Equal(["ask", "page False", "own False"], h.Fake.Log);
        Assert.Equal([false, false], h.Statuses);
        Assert.False(h.C.IsBusy);

        // Nothing pending any more: a later termination does nothing.
        await h.Ui.RunAsync(h.C.Terminated);
        await h.IdleAsync();
        Assert.Equal(3, h.Fake.Log.Count);
    }

    [Fact]
    public async Task NothingPendingMeansTheTerminationDoesNothing()
    {
        using var h = await Harness.MakeAsync(running: true);
        h.Fake.Running = false;
        await h.Ui.RunAsync(h.C.Terminated);
        await h.IdleAsync();
        Assert.Empty(h.Fake.Log);
        Assert.Empty(h.Statuses);
        Assert.Empty(h.Changes);
    }

    [Fact]
    public async Task WithoutTheOfferTheChangeIsWrittenAtOnce()
    {
        // Not running: no question, and the change sticks.
        using var h = await Harness.MakeAsync(running: false);
        await h.FlipAsync(true, ClaudeDesktopController.Answer.Restart);
        Assert.Equal(["page True"], h.Fake.Log);
        Assert.Null(h.C.Pending);
        Assert.Equal([true], h.Statuses);

        // Running, but not a client the bridge knows: no question either.
        using var g = await Harness.MakeAsync(running: true);
        await g.FlipAsync(true, ClaudeDesktopController.Answer.Restart, present: false);
        Assert.Equal(["page True"], g.Fake.Log);
        Assert.Null(g.C.Pending);
    }

    [Fact]
    public async Task AWriteWhileClaudeDesktopDoesNotRunClearsWhatWasPending()
    {
        using var h = await Harness.MakeAsync(running: true);
        await h.FlipAsync(true, ClaudeDesktopController.Answer.Later);
        Assert.True(h.C.Pending);
        // It quit, and its termination was not seen: the next write sticks.
        h.Fake.Running = false;
        await h.FlipAsync(false, ClaudeDesktopController.Answer.Later);
        Assert.Equal(["ask", "page True", "page False"], h.Fake.Log);
        Assert.Null(h.C.Pending);
    }

    [Fact]
    public async Task TheRestartsOwnTerminationIsNotWrittenAgain()
    {
        using var h = await Harness.MakeAsync(running: true);
        await h.FlipAsync(true, ClaudeDesktopController.Answer.Later);
        Assert.True(h.C.Pending);
        // The pending row's Restart; the notice arrives while the restart
        // waits for the quit.
        h.Fake.OnQuit = h.C.Terminated;
        await h.Ui.InvokeAsync(() => h.C.RestartPendingAsync(h.Fake.Write("page")));
        await h.IdleAsync();
        Assert.Equal(["ask", "page True", "quit", "page True", "launch"], h.Fake.Log);
        Assert.Null(h.C.Pending);
    }

    [Fact]
    public async Task RestartPendingAppliesThePendingState()
    {
        using var h = await Harness.MakeAsync(running: true);
        await h.FlipAsync(false, ClaudeDesktopController.Answer.Later);
        await h.Ui.InvokeAsync(() => h.C.RestartPendingAsync(h.Fake.Write("page")));
        Assert.Equal(["ask", "page False", "quit", "page False", "launch"], h.Fake.Log);
        Assert.Null(h.C.Pending);
        Assert.Equal([false, false], h.Statuses);

        // Nothing pending: nothing happens.
        await h.Ui.InvokeAsync(() => h.C.RestartPendingAsync(h.Fake.Write("page")));
        Assert.Equal(5, h.Fake.Log.Count);
    }

    [Fact]
    public async Task NoSecondWriteWhileClaudeDesktopRunsAgain()
    {
        using var h = await Harness.MakeAsync(running: true);
        await h.FlipAsync(true, ClaudeDesktopController.Answer.Later);
        // Terminated, but already started again when the write would run: it
        // would not stick, so it stays pending.
        await h.Ui.RunAsync(h.C.Terminated);
        await h.IdleAsync();
        Assert.Equal(["ask", "page True"], h.Fake.Log);
        Assert.True(h.C.Pending);
    }

    [Fact]
    public async Task AFailedWriteLeavesThePendingStateAlone()
    {
        using var h = await Harness.MakeAsync(running: true);
        h.Fake.Failing = ["page"];
        await h.FlipAsync(true, ClaudeDesktopController.Answer.Later);
        Assert.Null(h.C.Pending);
        Assert.Empty(h.Statuses);

        // The restart's write fails: Claude Desktop is started again all the
        // same.
        await h.FlipAsync(true, ClaudeDesktopController.Answer.Restart);
        Assert.Equal(["ask", "page True", "ask", "quit", "page True", "launch"], h.Fake.Log);
        Assert.Null(h.C.Pending);

        // A pending change whose second write fails stays pending.
        h.Fake.Failing = ["own"];
        await h.FlipAsync(true, ClaudeDesktopController.Answer.Later);
        Assert.True(h.C.Pending);
        h.Fake.Running = false;
        await h.Ui.RunAsync(h.C.Terminated);
        await h.IdleAsync();
        Assert.Equal("own True", h.Fake.Log[^1]);
        Assert.True(h.C.Pending);
    }

    [Fact]
    public async Task WritesNeverOverlap()
    {
        using var h = await Harness.MakeAsync(running: true);
        await h.FlipAsync(true, ClaudeDesktopController.Answer.Later);
        h.Fake.HoldWrites = true;
        h.Fake.Running = false;
        // The re-apply starts; the switch flips while it runs.
        await h.Ui.RunAsync(h.C.Terminated);
        await h.Conditions.WhenAsync(h.Ui, () => h.Fake.Log[^1] == "own True", "the re-apply writes");
        Assert.True(h.C.IsBusy);
        var flip = h.FlipAsync(false, ClaudeDesktopController.Answer.Later);
        await h.Ui.DrainAsync();
        Assert.Equal("own True", h.Fake.Log[^1]);
        await h.Ui.RunAsync(h.Fake.ReleaseWrites);
        await h.Conditions.WhenAsync(h.Ui, () => h.Fake.Log[^1] == "page False", "the flip writes after the re-apply");
        await h.Ui.RunAsync(h.Fake.ReleaseWrites);
        await flip;
        await h.IdleAsync();
        Assert.Equal(["ask", "page True", "own True", "own True end", "page False", "page False end"], h.Fake.Log);
        Assert.Null(h.C.Pending);
        Assert.False(h.C.IsBusy);
    }

    [Fact]
    public async Task BusyWhileTheRestartWaitsForTheQuit()
    {
        using var h = await Harness.MakeAsync(running: true);
        h.Fake.HoldQuit();
        var flip = h.FlipAsync(true, ClaudeDesktopController.Answer.Restart);
        await h.Conditions.WhenAsync(h.Ui, () => h.Fake.Log.Count > 0 && h.Fake.Log[^1] == "quit", "the quit is asked");
        Assert.True(await h.Ui.RunAsync(() => h.C.IsBusy));
        h.Fake.ReleaseQuit();
        await flip;
        Assert.False(h.C.IsBusy);
        Assert.Equal(["ask", "quit", "page True", "launch"], h.Fake.Log);
    }

    [Fact]
    public async Task AClosedControllerIgnoresLateEvents()
    {
        // Closed while the restart waits for the quit: no write, no launch.
        using var h = await Harness.MakeAsync(running: true);
        h.Fake.HoldQuit();
        var flip = h.FlipAsync(true, ClaudeDesktopController.Answer.Restart);
        await h.Conditions.WhenAsync(h.Ui, () => h.Fake.Log.Count > 0 && h.Fake.Log[^1] == "quit", "the quit is asked");
        await h.Ui.RunAsync(h.C.Close);
        h.Fake.ReleaseQuit();
        await flip;
        Assert.Equal(["ask", "quit"], h.Fake.Log);
        Assert.Empty(h.Statuses);
        Assert.Null(h.C.Pending);

        // Closed with a change pending: the termination does nothing, and
        // neither does anything else.
        using var g = await Harness.MakeAsync(running: true);
        await g.FlipAsync(true, ClaudeDesktopController.Answer.Later);
        Assert.True(g.C.Pending);
        await g.Ui.RunAsync(g.C.Close);
        g.Fake.Running = false;
        await g.Ui.RunAsync(g.C.Terminated);
        await g.Ui.InvokeAsync(() => g.C.RestartPendingAsync(g.Fake.Write("page")));
        await g.FlipAsync(false, ClaudeDesktopController.Answer.Later);
        Assert.False(await g.Ui.RunAsync(() => g.C.OffersRestart(Status(desktop: true))));
        Assert.Null(await g.Ui.InvokeAsync(() => g.C.WriteOwnAsync(false)));
        await g.IdleAsync();
        Assert.Equal(["ask", "page True"], g.Fake.Log);
        Assert.Equal([true], g.Statuses);
    }

    [Fact]
    public async Task ClosedWhileTheQuestionIsUpNothingIsWritten()
    {
        using var h = await Harness.MakeAsync(running: true);
        await h.Ui.InvokeAsync(() => h.C.ChangeAsync(true, Status(desktop: false), () =>
        {
            h.C.Close();
            return Task.FromResult(ClaudeDesktopController.Answer.Later);
        }, h.Fake.Write("page")));
        Assert.Empty(h.Fake.Log);
    }

    [Fact]
    public async Task TheOwnWriteRunsTheBridge()
    {
        // The second constructor's own registration against a stand-in
        // malachi-mcp that logs its arguments.
        using var dir = new TemporaryDirectory();
        using var packages = new TemporaryDirectory();
        var json = """{"command": "C:\\x\\malachi-mcp.exe", "clients": [{"id": "claude-desktop", "name": "Claude Desktop", "present": true, "registered": true}]}""";
        var bridge = new FakeBridgeScript(FakeBridgeStep.Fails("unexpected status"), install: FakeBridgeStep.Prints(json)).CreateIn(dir.Path);
        var desktop = new ClaudeDesktopPackage { PackagesDirectory = packages.Path };
        using var ui = new TestUIContext();
        var pending = new PendingWork();
        var fake = new Fake(running: true);
        var statuses = new List<McpStatus>();
        var c = await ui.RunAsync(() =>
        {
            var controller = new ClaudeDesktopController(bridge, fake.Platform, claudeDesktop: desktop, pending: pending);
            controller.StatusReported += (_, s) => statuses.Add(s);
            return controller;
        });
        await ui.InvokeAsync(() => c.ChangeAsync(true, Status(desktop: false), fake.Ask(ClaudeDesktopController.Answer.Later), fake.Write("page")));
        Assert.True(c.Pending);
        fake.Running = false;
        await ui.RunAsync(c.Terminated);
        await Quiescence.IdleAsync(ui, pending, timeout: TimeSpan.FromSeconds(30));
        Assert.Null(c.Pending);
        Assert.Equal([$"install --json --command {McpRegistrationController.CanonicalPath(bridge)}"], FakeBridgeScript.Calls(dir.Path));
        Assert.Equal(2, statuses.Count);
        Assert.True(statuses[^1].IsRegistered);

        // Without a bridge the own write fails quietly and the change stays
        // pending.
        var none = await ui.RunAsync(() => new ClaudeDesktopController((string?)null, fake.Platform, claudeDesktop: desktop, pending: pending));
        fake.Running = true;
        await ui.InvokeAsync(() => none.ChangeAsync(true, Status(desktop: false), fake.Ask(ClaudeDesktopController.Answer.Later), fake.Write("page")));
        Assert.Null(await ui.InvokeAsync(() => none.WriteOwnAsync(true)));
        Assert.True(none.Pending);
    }

    // What the bridge reports: Claude Desktop (present as asked) registered
    // as desktop, Claude Code present and not registered.
    private static McpStatus Status(bool desktop, bool present = true) => new()
    {
        Command = @"C:\Users\u\AppData\Local\Programs\Malachi Mail\malachi-mcp.exe",
        Clients =
        [
            new McpClient { Id = "claude-desktop", Name = "Claude Desktop", Present = present, Registered = desktop },
            new McpClient { Id = "claude-code", Name = "Claude Code", Present = true, Registered = false },
        ],
    };

    // A stand-in Claude Desktop and the writes, logging in one place; every
    // entry is noted to the conditions on the UI thread.
    private sealed class Fake(bool running)
    {
        private TaskCompletionSource? quitGate;
        private TaskCompletionSource writeGate = new(TaskCreationOptions.RunContinuationsAsynchronously);

        public bool Running { get; set; } = running;

        // Whether a quit request makes it quit (false: the wait times out).
        public bool Quits { get; set; } = true;

        // Keeps every write waiting until ReleaseWrites.
        public bool HoldWrites { get; set; }

        // Runs inside the quit once it quit: the termination's notice.
        public Action? OnQuit { get; set; }

        public List<string> Log { get; } = [];

        public List<TimeSpan> Timeouts { get; } = [];

        // The writes that fail, by name.
        public HashSet<string> Failing { get; set; } = [];

        public UiConditions? Conditions { get; set; }

        public ClaudeDesktopPlatform Platform => new(
            () => Running,
            async (timeout, _) =>
            {
                Note("quit");
                Timeouts.Add(timeout);
                if (quitGate is { } gate)
                {
                    await gate.Task;
                }
                if (!Quits)
                {
                    return false;
                }
                Running = false;
                OnQuit?.Invoke();
                return true;
            },
            () =>
            {
                Note("launch");
                Running = true;
            });

        public void HoldQuit() => quitGate = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);

        public void ReleaseQuit() => quitGate?.TrySetResult();

        // Lets the held write go on; the next write waits for the next release.
        public void ReleaseWrites()
        {
            var gate = writeGate;
            writeGate = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
            gate.TrySetResult();
        }

        // A write that logs "name True|False" (with "… end" after a held
        // write) and reports that registration, or null when name is failing.
        public Func<bool, Task<McpStatus?>> Write(string name) => async want =>
        {
            Note($"{name} {want}");
            if (HoldWrites)
            {
                var gate = writeGate;
                await gate.Task;
                Note($"{name} {want} end");
            }
            return Failing.Contains(name) ? null : Status(want);
        };

        // The question, answered with answer and logged.
        public Func<Task<ClaudeDesktopController.Answer>> Ask(ClaudeDesktopController.Answer answer) => () =>
        {
            Note("ask");
            return Task.FromResult(answer);
        };

        private void Note(string entry)
        {
            Log.Add(entry);
            Conditions?.Changed();
        }
    }

    // A controller over the fake whose own write is the fake's "own", with
    // what it reports collected.
    private sealed class Harness : IDisposable
    {
        private Harness(Fake fake, ClaudeDesktopController c)
        {
            Fake = fake;
            C = c;
        }

        public TestUIContext Ui { get; private init; } = null!;

        public PendingWork Pending { get; private init; } = null!;

        public Fake Fake { get; }

        public ClaudeDesktopController C { get; }

        public List<bool> Statuses { get; } = [];

        public List<string> Toasts { get; } = [];

        public List<(bool? Pending, bool Busy)> Changes { get; } = [];

        public UiConditions Conditions { get; } = new();

        public static async Task<Harness> MakeAsync(bool running)
        {
            var ui = new TestUIContext();
            var pending = new PendingWork();
            var fake = new Fake(running);
            var h = await ui.RunAsync(() =>
            {
                var c = new ClaudeDesktopController(fake.Platform, fake.Write("own"), pending: pending);
                return new Harness(fake, c) { Ui = ui, Pending = pending };
            });
            fake.Conditions = h.Conditions;
            await ui.RunAsync(() =>
            {
                h.C.StatusReported += (_, s) => h.Statuses.Add(s.Clients.FirstOrDefault(c => c.Id == "claude-desktop")?.Registered ?? false);
                h.C.ToastRequested += (_, text) => h.Toasts.Add(text);
                h.C.Changed += (_, _) =>
                {
                    h.Changes.Add((h.C.Pending, h.C.IsBusy));
                    h.Conditions.Changed();
                };
            });
            return h;
        }

        // The page's flip to want with the question answered answer.
        public Task FlipAsync(bool want, ClaudeDesktopController.Answer answer, bool present = true) =>
            Ui.InvokeAsync(() => C.ChangeAsync(want, Status(!want, present), Fake.Ask(answer), Fake.Write("page")));

        public Task IdleAsync() => Quiescence.IdleAsync(Ui, Pending);

        public void Dispose() => Ui.Dispose();
    }
}
