// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the schedule's half of macos/Tests/MalachiCoreTests/
// BoardAutoTriageTests.swift (debounce, preferencesDecideAtOnce,
// intervalBetweenRuns, backOff, dailyCap, signedOut,
// lastAttemptFromTheDaemon, stop; the rule's half is
// Boards/BoardAutoTriageTests.cs); GTK: ui/internal/boardtriage
// auto_test.go (TestScheduler*). The schedule against a stand-in target.
// Swift's TriageClock, whose fire ends every sleep, is a FakeTimeProvider,
// which ends the waits that are due: the decisions are the same, since a
// wait that is not due yet is the heartbeat, which decides as the due one
// does.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Boards;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Tests.Fixtures;
using Microsoft.Extensions.Time.Testing;
using Xunit;
using static Malachi.Core.Boards.Board;

namespace Malachi.Core.Tests.Controllers;

public sealed class BoardAutoTriageSchedulerTests
{
    /// <summary>2026-10-01 10:00:00 UTC.</summary>
    private static readonly DateTimeOffset T0 = DateTimeOffset.FromUnixTimeSeconds(1_790_848_800);

    private static TimeSpan Minutes(double m) => TimeSpan.FromMinutes(m);

    /// <summary>New board data waits a minute for more: a burst gives one run.</summary>
    [Fact]
    public async Task Debounce()
    {
        await using var h = await Harness.StartAsync(t => t.Inputs = t.Inputs with { Queue = 0 });
        Assert.Equal(new AutoTriage.Decision.Off(AutoTriage.OffReason.EmptyQueue), await h.Ui.RunAsync(() => h.S.Decision));
        await h.Ui.RunAsync(() =>
        {
            h.T.Board(queue: 1);
            h.T.Board(queue: 2);
            h.T.Board(queue: 3);
            Assert.True(h.S.Debouncing && h.T.Starts.Count == 0);
        });
        await h.AdvanceAsync(TimeSpan.FromSeconds(60));
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal([40], h.T.Starts);
            Assert.Equal(h.Clock.GetUtcNow(), h.S.LastAttempt);
            Assert.False(h.S.Debouncing);
            // While it runs nothing else starts.
            h.T.Board(queue: 4);
        });
        await h.AdvanceAsync(TimeSpan.FromSeconds(60));
        await h.Ui.RunAsync(() =>
            Assert.True(h.S.Decision == new AutoTriage.Decision.Off(AutoTriage.OffReason.Running) && h.T.Starts.Count == 1));
    }

    /// <summary>A change that is not the board's decides at once.</summary>
    [Fact]
    public async Task PreferencesDecideAtOnce()
    {
        await using var h = await Harness.StartAsync(t => t.Inputs = t.Inputs with { Enabled = false });
        await h.Ui.RunAsync(() =>
        {
            Assert.True(h.S.Decision == new AutoTriage.Decision.Off(AutoTriage.OffReason.SwitchedOff) && h.S.WakeAt is null);
            h.T.Change(i => i with { Enabled = true });
            Assert.Equal([40], h.T.Starts);
        });
    }

    /// <summary>After a run the next waits for the interval from its start.</summary>
    [Fact]
    public async Task IntervalBetweenRuns()
    {
        await using var h = await Harness.StartAsync();
        Assert.Equal([40], await h.Ui.RunAsync(() => h.T.Starts.ToArray()));
        await h.AdvanceAsync(Minutes(5));
        await h.Ui.RunAsync(() =>
        {
            h.T.End();
            Assert.Equal(new AutoTriage.Decision.Wait(T0 + Minutes(30)), h.S.Decision);
            Assert.Equal(T0 + Minutes(30), h.S.WakeAt);
            Assert.Null(h.T.Pause);
        });
        await h.AdvanceAsync(Minutes(25));
        Assert.Equal(2, await h.Ui.RunAsync(() => h.T.Starts.Count));
    }

    /// <summary>
    /// Failed automatic runs double the wait (and say why on the strip); a
    /// success resets it, and so does a manual run.
    /// </summary>
    [Fact]
    public async Task BackOff()
    {
        await using var h = await Harness.StartAsync();
        var until1 = T0 + Minutes(60);
        await h.Ui.RunAsync(() =>
        {
            Assert.Single(h.T.Starts);
            h.T.End(TriageTrigger.Automatic, TriageFailure.Timeout);
            Assert.Equal(1, h.S.Failures);
            Assert.Equal(new AutoTriage.Decision.Wait(until1), h.S.Decision);
            Assert.Equal(new AutoTriagePause.Failed(TriageFailure.Timeout, until1), h.T.Pause);
        });
        await h.AdvanceAsync(Minutes(60));
        var second = h.Clock.GetUtcNow();
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(2, h.T.Starts.Count);
            h.T.End(TriageTrigger.Automatic, TriageFailure.Stopped);
            Assert.Equal(2, h.S.Failures);
            Assert.Equal(new AutoTriage.Decision.Wait(second + Minutes(120)), h.S.Decision);
            Assert.Equal(new AutoTriagePause.Failed(TriageFailure.Stopped, second + Minutes(120)), h.T.Pause);
            // A manual run resets it; the interval from the last automatic
            // attempt still holds.
            h.T.Change(i => i with { Running = true });
            h.T.End(TriageTrigger.Manual, null);
            Assert.True(h.S.Failures == 0 && h.T.Pause is null);
            Assert.Equal(new AutoTriage.Decision.Wait(second + Minutes(30)), h.S.Decision);
        });
        // A cancelled run is no failure.
        await h.AdvanceAsync(Minutes(30));
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(3, h.T.Starts.Count);
            h.T.End(TriageTrigger.Automatic, TriageFailure.Cancelled);
            Assert.Equal(0, h.S.Failures);
        });
        // A success resets after failures too.
        await h.AdvanceAsync(Minutes(30));
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(4, h.T.Starts.Count);
            h.T.End(TriageTrigger.Automatic, TriageFailure.NotFound);
            Assert.Equal(1, h.S.Failures);
        });
        await h.AdvanceAsync(Minutes(60));
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(5, h.T.Starts.Count);
            h.T.End(TriageTrigger.Automatic, null);
            Assert.True(h.S.Failures == 0 && h.T.Pause is null);
        });
    }

    /// <summary>The day's cap used up: the next try is at midnight, when the count read yesterday no longer holds.</summary>
    [Fact]
    public async Task DailyCap()
    {
        await using var h = await Harness.StartAsync(t => t.Inputs = t.Inputs with { AnnotatedToday = 60, CountedAt = T0 });
        var midnight = T0.AddHours(14);
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(new AutoTriage.Decision.Wait(midnight), h.S.Decision);
            // The heartbeat comes first and decides the same.
            Assert.Equal(T0 + Minutes(30), h.S.WakeAt);
        });
        await h.AdvanceAsync(Minutes(30));
        await h.Ui.RunAsync(() => Assert.True(h.S.Decision == new AutoTriage.Decision.Wait(midnight) && h.T.Starts.Count == 0));
        await h.AdvanceAsync(midnight - h.Clock.GetUtcNow());
        Assert.Equal([40], await h.Ui.RunAsync(() => h.T.Starts.ToArray()));
    }

    /// <summary>Signed out: paused, and the heartbeat asks the sign-in afresh.</summary>
    [Fact]
    public async Task SignedOut()
    {
        await using var h = await Harness.StartAsync(t => t.Inputs = t.Inputs with { SignedIn = false });
        await h.Ui.RunAsync(() =>
        {
            Assert.True(h.S.Decision == new AutoTriage.Decision.Off(AutoTriage.OffReason.SignedOut) && h.T.Pause is AutoTriagePause.SignedOut);
            Assert.Equal(T0 + Minutes(30), h.S.WakeAt);
        });
        await h.AdvanceAsync(Minutes(30));
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(1, h.T.Rechecks);
            h.T.Change(i => i with { SignedIn = true });
            Assert.True(h.T.Starts.SequenceEqual([40]) && h.T.Pause is null);
            // The other pauses.
            h.T.End();
            h.T.Change(i => i with { Consent = false });
            Assert.IsType<AutoTriagePause.NoConsent>(h.T.Pause);
            h.T.Change(i => i with { Available = false });
            Assert.IsType<AutoTriagePause.Unavailable>(h.T.Pause);
            // Switched off: no pause, nothing scheduled.
            h.T.Change(i => i with { Enabled = false });
            Assert.True(h.T.Pause is null && h.S.WakeAt is null);
        });
    }

    /// <summary>The daemon's last automatic run stands in for the last attempt after a restart; this schedule's own attempt counts when newer.</summary>
    [Fact]
    public async Task LastAttemptFromTheDaemon()
    {
        await using var h = await Harness.StartAsync(t => t.Inputs = t.Inputs with { LastAttempt = T0 - Minutes(10) });
        Assert.Equal(new AutoTriage.Decision.Wait(T0 + Minutes(20)), await h.Ui.RunAsync(() => h.S.Decision));
    }

    /// <summary>Stopped: nothing is decided any more.</summary>
    [Fact]
    public async Task Stop()
    {
        await using var h = await Harness.StartAsync(t => t.Inputs = t.Inputs with { Queue = 0 });
        await h.Ui.RunAsync(() =>
        {
            h.S.Stop();
            h.T.Board(queue: 3);
            h.T.Change(i => i with { Enabled = true });
        });
        await h.AdvanceAsync(Minutes(60));
        await h.Ui.RunAsync(() => Assert.True(h.T.Starts.Count == 0 && !h.S.Debouncing && h.S.WakeAt is null));
    }

    /// <summary>The schedule's target: inputs the test sets, the runs it started.</summary>
    private sealed class FakeTarget : IBoardAutoTriageTarget
    {
        private readonly BoardObservers changes = new();
        private readonly BoardObservers ends = new();

        public AutoTriage.Inputs Inputs { get; set; } = new() { Now = T0 };

        public int BoardRevision { get; private set; }

        public BoardTriageEnd? LastEnded { get; private set; }

        public List<int?> Starts { get; } = [];

        public AutoTriagePause? Pause { get; private set; }

        public int Rechecks { get; private set; }

        public AutoTriage.Inputs AutoTriageInputs => Inputs;

        public bool Start(TriageTrigger trigger, int? limit = null)
        {
            if (Inputs.Running)
            {
                return false;
            }
            Starts.Add(limit);
            Inputs = Inputs with { Running = true };
            changes.Notify();
            return true;
        }

        public void SetAutoPause(AutoTriagePause? pause) => Pause = pause;

        public void RecheckSignIn() => Rechecks++;

        public BoardObserverToken Observe(Action f) => changes.Add(f);

        public BoardObserverToken ObserveEnded(Action f) => ends.Add(f);

        /// <summary>Something but the board changed.</summary>
        public void Change(Func<AutoTriage.Inputs, AutoTriage.Inputs> f)
        {
            Inputs = f(Inputs);
            changes.Notify();
        }

        /// <summary>New board data.</summary>
        public void Board(int queue)
        {
            Inputs = Inputs with { Queue = queue };
            BoardRevision++;
            changes.Notify();
        }

        /// <summary>The run ended, reported as the controller does: the end first.</summary>
        public void End(TriageTrigger trigger = TriageTrigger.Automatic, TriageFailure? failure = null)
        {
            LastEnded = new BoardTriageEnd(trigger, failure);
            ends.Notify();
            Inputs = Inputs with { Running = false };
            changes.Notify();
        }
    }

    private sealed class Harness : IAsyncDisposable
    {
        public TestUIContext Ui { get; } = new();

        public PendingWork Pending { get; } = new();

        public FakeTimeProvider Clock { get; } = new(T0);

        public FakeTarget T { get; } = new();

        public BoardAutoTriageScheduler S { get; private set; } = null!;

        /// <summary>A started schedule over a target <paramref name="setUp"/> prepared.</summary>
        public static async Task<Harness> StartAsync(Action<FakeTarget>? setUp = null)
        {
            var h = new Harness();
            h.S = await h.Ui.RunAsync(() =>
            {
                setUp?.Invoke(h.T);
                var s = new BoardAutoTriageScheduler(h.T, h.Clock, TimeZoneInfo.Utc, pending: h.Pending);
                s.Start();
                return s;
            });
            return h;
        }

        /// <summary>Moves the clock on and waits until what that ended has decided.</summary>
        public async Task AdvanceAsync(TimeSpan d)
        {
            await IdleAsync();
            Clock.Advance(d);
            await IdleAsync();
        }

        public Task IdleAsync() => Quiescence.IdleAsync(Ui, Pending);

        public async ValueTask DisposeAsync()
        {
            await Ui.RunAsync(S.Dispose);
            await IdleAsync();
            Ui.Dispose();
        }
    }
}
