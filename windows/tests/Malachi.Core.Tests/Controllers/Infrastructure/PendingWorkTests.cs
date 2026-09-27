// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only tests (docs/windows-port.md §7.2): the tracker IdleAsync
// waits on.

using System;
using System.Globalization;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Controllers.Infrastructure;
using Xunit;

namespace Malachi.Core.Tests.Controllers.Infrastructure;

public sealed class PendingWorkTests
{
    [Fact]
    public async Task IdleAtOnceWithoutWork()
    {
        var pending = new PendingWork();
        Assert.Equal(0, pending.Count);
        Assert.True(pending.IdleAsync(TestContext.Current.CancellationToken).IsCompleted);
        pending.Track(Task.CompletedTask);
        await pending.IdleAsync(TestContext.Current.CancellationToken);
        Assert.Equal(0, pending.Count);
    }

    [Fact]
    public async Task IdleWhenTheLastTaskEnds()
    {
        var pending = new PendingWork();
        var first = new TaskCompletionSource();
        var second = new TaskCompletionSource();
        pending.Track(first.Task);
        pending.Track(second.Task);
        var idle = pending.IdleAsync(TestContext.Current.CancellationToken);
        Assert.Equal(2, pending.Count);
        first.SetResult();
        Assert.False(idle.IsCompleted);
        using var cancelled = new CancellationTokenSource();
        cancelled.Cancel();
        second.SetCanceled(cancelled.Token); // an ended task, not a failure
        await idle;
        Assert.Empty(pending.TakeFaults());
    }

    /// <summary>Work a task starts in its last step is counted before that task ends.</summary>
    [Fact]
    public async Task WorkStartedByWorkKeepsItBusy()
    {
        var pending = new PendingWork();
        var later = new TaskCompletionSource();
        var outer = new TaskCompletionSource();
        pending.Track(outer.Task.ContinueWith(_ => pending.Track(later.Task), TaskScheduler.Default));
        var idle = pending.IdleAsync(TestContext.Current.CancellationToken);
        outer.SetResult();
        await Task.Yield();
        Assert.False(idle.IsCompleted);
        later.SetResult();
        await idle;
    }

    [Fact]
    public async Task FailuresAreKeptAndTakenOnce()
    {
        var pending = new PendingWork();
        pending.Track(Task.FromException(new InvalidOperationException("one")));
        pending.Track(Task.Run(() => throw new InvalidOperationException("two"), TestContext.Current.CancellationToken));
        await pending.IdleAsync(TestContext.Current.CancellationToken);
        var faults = pending.TakeFaults();
        Assert.Equal(2, faults.Count);
        Assert.Empty(pending.TakeFaults());
        pending.Report(new InvalidOperationException("three"));
        Assert.Equal("three", Assert.Single(pending.TakeFaults()).Message);
    }

    /// <summary>A failure that recurs where nobody takes the failures keeps only the latest.</summary>
    [Fact]
    public void KeepsTheLatestFailures()
    {
        var pending = new PendingWork();
        for (var i = 0; i < PendingWork.MaxKeptFaults + 36; i++)
        {
            pending.Report(new InvalidOperationException(i.ToString(CultureInfo.InvariantCulture)));
        }
        var faults = pending.TakeFaults();
        Assert.Equal(PendingWork.MaxKeptFaults, faults.Count);
        Assert.Equal("36", faults[0].Message);
        Assert.Equal((PendingWork.MaxKeptFaults + 35).ToString(CultureInfo.InvariantCulture), faults[^1].Message);
    }

    [Fact]
    public async Task WaitingCanBeCancelled()
    {
        var pending = new PendingWork();
        pending.Track(new TaskCompletionSource().Task);
        using var cancel = new CancellationTokenSource();
        var idle = pending.IdleAsync(cancel.Token);
        cancel.Cancel();
        await Assert.ThrowsAnyAsync<OperationCanceledException>(() => idle);
    }
}
