// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/windows-port.md §7.2): the tests of macOS poll
// with waitUntil and sleep for the negative cases; the C# tests wait until
// the controllers' background work is done instead, which this tracker
// tells.

using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Controllers.Infrastructure;

/// <summary>
/// The background work that controllers started and has not finished
/// (<see cref="ControllerScope.Perform{TResult}(Func{CancellationToken, Task{TResult}}, Action{Outcome{TResult}})"/>,
/// <see cref="ControllerScope.Run"/>): <see cref="IdleAsync"/> completes when
/// there is none. A task that fails is logged and kept in
/// <see cref="TakeFaults"/>, so that no failure of a callback disappears
/// with its fire-and-forget task; the last <see cref="MaxKeptFaults"/> are
/// kept, so that a failure that recurs in an app where nobody takes them
/// costs its log lines and no more. Thread-safe.
/// </summary>
public sealed partial class PendingWork
{
    /// <summary>How many failures <see cref="TakeFaults"/> keeps: the latest.</summary>
    public const int MaxKeptFaults = 64;

    private readonly Lock gate = new();
    private readonly Queue<Exception> faults = new();
    private readonly ILogger logger;
    private int count;
    private TaskCompletionSource? idle;

    /// <summary>A tracker that logs failed work to <paramref name="logger"/>.</summary>
    public PendingWork(ILogger? logger = null)
    {
        this.logger = logger ?? NullLogger.Instance;
    }

    /// <summary>How many tracked tasks have not finished.</summary>
    public int Count
    {
        get
        {
            lock (gate)
            {
                return count;
            }
        }
    }

    /// <summary>Counts <paramref name="task"/> until it finishes.</summary>
    public void Track(Task task)
    {
        ArgumentNullException.ThrowIfNull(task);
        lock (gate)
        {
            count++;
            idle ??= new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        }
        task.ContinueWith(Finished, CancellationToken.None, TaskContinuationOptions.ExecuteSynchronously, TaskScheduler.Default);
    }

    /// <summary>
    /// Completes when no tracked task is running; at once when none is.
    /// Work that a finishing task starts in its last step is tracked before
    /// that task counts as finished, so the tracker does not flicker idle in
    /// between.
    /// </summary>
    public Task IdleAsync(CancellationToken cancellationToken = default)
    {
        lock (gate)
        {
            return idle is null ? Task.CompletedTask : idle.Task.WaitAsync(cancellationToken);
        }
    }

    /// <summary>
    /// The failures of tracked tasks since the last call, oldest first, at
    /// most the last <see cref="MaxKeptFaults"/>; clears them.
    /// </summary>
    public IReadOnlyList<Exception> TakeFaults()
    {
        lock (gate)
        {
            var taken = faults.ToArray();
            faults.Clear();
            return taken;
        }
    }

    /// <summary>Records a failure of work that is not tracked but must not vanish either.</summary>
    public void Report(Exception error)
    {
        ArgumentNullException.ThrowIfNull(error);
        LogFailed(logger, error);
        lock (gate)
        {
            if (faults.Count == MaxKeptFaults)
            {
                faults.Dequeue();
            }
            faults.Enqueue(error);
        }
    }

    [LoggerMessage(Level = LogLevel.Error, Message = "Background work of a controller failed")]
    private static partial void LogFailed(ILogger logger, Exception error);

    private void Finished(Task task)
    {
        if (task.Exception is { } failed)
        {
            foreach (var e in failed.InnerExceptions)
            {
                Report(e);
            }
        }
        TaskCompletionSource? nowIdle = null;
        lock (gate)
        {
            if (--count == 0)
            {
                nowIdle = idle;
                idle = null;
            }
        }
        nowIdle?.TrySetResult();
    }
}
