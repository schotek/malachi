// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// A fake clock that says when a timer is set on it. The supervisor's
// EnsureAsync and StopAsync leave the caller's thread first, so a test that
// advances the clock must know the supervisor is waiting on it (the poll
// after a start, the stop timeout) rather than hope it got there.

using System;
using System.Threading;
using System.Threading.Tasks;
using Microsoft.Extensions.Time.Testing;
using Xunit;

namespace Malachi.Core.Tests.Daemon;

/// <summary>A <see cref="FakeTimeProvider"/> that reports the timers set on it.</summary>
internal sealed class WatchedTimeProvider(DateTimeOffset start) : FakeTimeProvider(start)
{
    private readonly Lock gate = new();
    private TaskCompletionSource? next;

    /// <summary>
    /// Completes when the next timer is set on this clock; taken before the
    /// call that sets it, so that the timer cannot come first.
    /// </summary>
    public Task NextTimer()
    {
        lock (gate)
        {
            next ??= new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
            return next.Task;
        }
    }

    /// <inheritdoc/>
    public override ITimer CreateTimer(TimerCallback callback, object? state, TimeSpan dueTime, TimeSpan period)
    {
        var timer = base.CreateTimer(callback, state, dueTime, period);
        TaskCompletionSource? armed;
        lock (gate)
        {
            armed = next;
            next = null;
        }
        armed?.TrySetResult();
        return timer;
    }

    /// <summary>
    /// Waits for <paramref name="timer"/> (from <see cref="NextTimer"/>,
    /// taken before <paramref name="call"/> started); fails when the call
    /// ends first.
    /// </summary>
    public static async Task WaitForAsync(Task timer, Task call)
    {
        await Task.WhenAny(timer, call).WaitAsync(TestContext.Current.CancellationToken);
        if (!timer.IsCompleted)
        {
            await call;
            Assert.Fail("the call ended without setting a timer");
        }
    }
}
