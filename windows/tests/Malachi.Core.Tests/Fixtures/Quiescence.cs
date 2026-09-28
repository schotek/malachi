// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// IdleAsync of docs/windows-port.md §7.2, in place of the waitUntil polls
// and the negative sleeps of the macOS controller tests: a test waits
// until nothing is left to happen, then asserts what happened (or that
// something did not).

using System;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Controllers.Infrastructure;

namespace Malachi.Core.Tests.Fixtures;

internal static class Quiescence
{
    /// <summary>
    /// Waits until, all at once, the controllers' tracked work is done
    /// (<paramref name="pending"/>), the fake daemon serves no call
    /// (<paramref name="daemon"/>), the UI queue is empty
    /// (<paramref name="ui"/>) and <paramref name="also"/> holds (say, every
    /// pushed notification was consumed). Then throws the failures of the
    /// tracked work, if any, so that a broken callback fails its test.
    /// </summary>
    /// <remarks>
    /// A wait on a fake clock is not work in progress: advance the clock,
    /// then wait again.
    /// </remarks>
    public static async Task IdleAsync(
        TestUIContext ui, PendingWork pending, FakeDaemon? daemon = null, Func<bool>? also = null, TimeSpan? timeout = null)
    {
        ArgumentNullException.ThrowIfNull(ui);
        ArgumentNullException.ThrowIfNull(pending);
        var deadline = DateTime.UtcNow + (timeout ?? TimeSpan.FromSeconds(10));
        while (true)
        {
            var left = deadline - DateTime.UtcNow;
            if (left <= TimeSpan.Zero)
            {
                throw new TimeoutException(
                    $"not idle: {pending.Count} tracked tasks, {daemon?.InFlight ?? 0} calls in the daemon, UI queue {(ui.IsIdle ? "empty" : "busy")}, condition {(also?.Invoke() ?? true)}");
            }
            await ui.DrainAsync().WaitAsync(left);
            await pending.IdleAsync().WaitAsync(left);
            if (daemon is not null)
            {
                await daemon.IdleAsync().WaitAsync(left);
            }
            await ui.DrainAsync().WaitAsync(left);
            if (pending.Count == 0 && (daemon?.InFlight ?? 0) == 0 && ui.IsIdle && (also?.Invoke() ?? true))
            {
                break;
            }
            // Something is on its way (bytes in a socket, a continuation being
            // posted): look again shortly.
            await Task.Delay(1);
        }
        var faults = pending.TakeFaults();
        if (faults.Count > 0)
        {
            throw new AggregateException("background work of a controller failed", faults);
        }
        if (ui.Failures.Count > 0)
        {
            throw new AggregateException("a callback on the UI thread failed", ui.Failures.ToArray());
        }
    }
}
