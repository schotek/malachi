// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CalendarControllerTests.swift (calZone,
// calNow, calEnv, calSamples, CalManualClock), as far as the tests of the
// event controllers use them (CalendarEventDetailControllerTests,
// CalendarEventEditorControllerTests, CalendarInvitationControllerTests).
//
// Swift's @MainActor suites are a TestUIContext here and its calWaitUntil
// polls are Quiescence.IdleAsync (CalUi). The manual clock keeps Swift's
// shape: every sleep waits, cancelled or not, until the test fires them all.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Tests.Fixtures;
using F = Malachi.Core.Tests.Calendars.CalFixture;

namespace Malachi.Core.Tests.Controllers;

/// <summary>The moment, zone and samples of the event controllers' tests.</summary>
internal static class EventControllersFixture
{
    /// <summary>Wednesday 14 October 2026, 10:17 in Prague (Swift <c>calNow</c>).</summary>
    public static readonly DateTimeOffset Now = F.At(2026, 10, 14, 10, 17);

    /// <summary>Europe/Prague (Swift <c>calZone</c>).</summary>
    public static TimeZoneInfo Zone => F.Prague;

    /// <summary>The English env in Prague (Swift <c>calEnv</c>).</summary>
    public static Cal.Env Env => F.Env();

    /// <summary>The samples of <see cref="Now"/>, writes emulated unless <paramref name="writes"/> is false (Swift <c>calSamples</c>).</summary>
    public static InMemoryCalendarSource Samples(bool writes = true) => new(Cal.SampleData(Now, Zone)) { EmulatesWrites = writes };
}

/// <summary>The UI thread and the tracked work of one test.</summary>
internal sealed class CalUi : IDisposable
{
    /// <summary>The test's UI thread.</summary>
    public TestUIContext Ui { get; } = new();

    /// <summary>The controllers' tracked work.</summary>
    public PendingWork Pending { get; } = new();

    /// <summary>Runs <paramref name="f"/> on the UI thread.</summary>
    public Task<T> Run<T>(Func<T> f) => Ui.RunAsync(f);

    /// <summary>Runs <paramref name="a"/> on the UI thread.</summary>
    public Task Run(Action a) => Ui.RunAsync(a);

    /// <summary>Starts <paramref name="f"/> on the UI thread and waits for its task.</summary>
    public Task<T> Invoke<T>(Func<Task<T>> f) => Ui.InvokeAsync(f);

    /// <summary>Waits until nothing is left to happen.</summary>
    public Task IdleAsync() => Quiescence.IdleAsync(Ui, Pending);

    public void Dispose() => Ui.Dispose();
}

/// <summary>Sleeps that wait until the test fires them (Swift <c>CalManualClock</c>).</summary>
internal sealed class ManualSleep
{
    private readonly List<(TimeSpan Delay, TaskCompletionSource Done)> waiting = [];

    /// <summary>The sleep to inject; it ignores its token, as Swift's manual clock ignores cancellation.</summary>
    public Cal.Sleep Sleep => (delay, _) =>
    {
        var done = new TaskCompletionSource();
        lock (waiting)
        {
            waiting.Add((delay, done));
        }
        return done.Task;
    };

    /// <summary>The delays of the sleeps waiting.</summary>
    public IReadOnlyList<TimeSpan> Pending
    {
        get
        {
            lock (waiting)
            {
                return [.. waiting.Select(w => w.Delay)];
            }
        }
    }

    /// <summary>Ends every sleep waiting; call it on the UI thread, so that what follows is posted there before it returns.</summary>
    public void Fire()
    {
        List<(TimeSpan Delay, TaskCompletionSource Done)> w;
        lock (waiting)
        {
            w = [.. waiting];
            waiting.Clear();
        }
        foreach (var (_, done) in w)
        {
            done.TrySetResult();
        }
    }
}
