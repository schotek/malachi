// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// A stand-in for the UI thread's DispatcherQueueSynchronizationContext
// (docs/windows-port.md §7.1): one thread that runs what is posted to it in
// order, for the tests of changes that arrive from another thread.

using System;
using System.Collections.Concurrent;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;

namespace Malachi.Core.Tests.Settings;

internal sealed class TestUIContext : SynchronizationContext, IDisposable
{
    private readonly BlockingCollection<(SendOrPostCallback Callback, object? State)> queue = [];
    private readonly Thread thread;
    private readonly List<Exception> failures = [];
    private bool closed;

    public TestUIContext()
    {
        thread = new Thread(Run) { IsBackground = true, Name = "test UI thread" };
        thread.Start();
    }

    /// <summary>The managed id of the context's thread.</summary>
    public int ThreadId => thread.ManagedThreadId;

    /// <summary>Exceptions thrown by posted callbacks.</summary>
    public IReadOnlyList<Exception> Failures
    {
        get
        {
            lock (failures)
            {
                return [.. failures];
            }
        }
    }

    // After Dispose, posts are dropped: a watcher may still report a change
    // while the test tears down.
    public override void Post(SendOrPostCallback d, object? state)
    {
        lock (queue)
        {
            if (!closed)
            {
                queue.Add((d, state));
            }
        }
    }

    public override void Send(SendOrPostCallback d, object? state) => throw new NotSupportedException();

    public override SynchronizationContext CreateCopy() => this;

    /// <summary>Runs <paramref name="action"/> on the context's thread and waits for it.</summary>
    public Task<T> RunAsync<T>(Func<T> action)
    {
        var done = new TaskCompletionSource<T>(TaskCreationOptions.RunContinuationsAsynchronously);
        Post(_ =>
        {
            try
            {
                done.SetResult(action());
            }
            catch (Exception e)
            {
                done.SetException(e);
            }
        }, null);
        return done.Task;
    }

    /// <summary>Completes once everything posted before it has run.</summary>
    public Task DrainAsync() => RunAsync(() => true);

    public void Dispose()
    {
        lock (queue)
        {
            if (closed)
            {
                return;
            }
            closed = true;
            queue.CompleteAdding();
        }
        thread.Join();
        queue.Dispose();
    }

    private void Run()
    {
        SetSynchronizationContext(this);
        foreach (var (callback, state) in queue.GetConsumingEnumerable())
        {
            try
            {
                callback(state);
            }
            catch (Exception e)
            {
                lock (failures)
                {
                    failures.Add(e);
                }
            }
        }
    }
}
