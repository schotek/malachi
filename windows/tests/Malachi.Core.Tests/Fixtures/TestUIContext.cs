// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// A stand-in for the UI thread's DispatcherQueueSynchronizationContext
// (docs/windows-port.md §7.1): one thread that runs what is posted to it in
// order. The controller tests create their controllers on it (Swift's
// @MainActor test suites), and IdleAsync (Quiescence) waits until its
// queue is empty.

using System;
using System.Collections.Concurrent;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;

namespace Malachi.Core.Tests.Fixtures;

/// <summary>A single-thread <see cref="SynchronizationContext"/>, the tests' UI thread.</summary>
internal sealed class TestUIContext : SynchronizationContext, IDisposable
{
    private readonly BlockingCollection<(SendOrPostCallback Callback, object? State)> queue = [];
    private readonly Thread thread;
    private readonly List<Exception> failures = [];
    private int queued; // posted and not finished running
    private bool closed;

    public TestUIContext()
    {
        thread = new Thread(Run) { IsBackground = true, Name = "test UI thread" };
        thread.Start();
    }

    /// <summary>The managed id of the context's thread.</summary>
    public int ThreadId => thread.ManagedThreadId;

    /// <summary>Whether nothing is queued or running.</summary>
    public bool IsIdle => Volatile.Read(ref queued) == 0;

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
                Interlocked.Increment(ref queued);
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

    /// <summary>Runs <paramref name="action"/> on the context's thread and waits for it.</summary>
    public Task RunAsync(Action action) => RunAsync(() =>
    {
        action();
        return true;
    });

    /// <summary>
    /// Starts <paramref name="action"/> on the context's thread, so that its
    /// continuations come back to it, and completes with its task.
    /// </summary>
    public Task<T> InvokeAsync<T>(Func<Task<T>> action) => RunAsync(action).Unwrap();

    /// <inheritdoc cref="InvokeAsync{T}(Func{Task{T}})"/>
    public Task InvokeAsync(Func<Task> action) => RunAsync(action).Unwrap();

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
            finally
            {
                Interlocked.Decrement(ref queued);
            }
        }
    }
}
