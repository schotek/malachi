// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the RPC plumbing the controllers of
// macos/Sources/MalachiCore/Controllers share (MailboxController.perform,
// the closed flag and close(), the `Task { [weak self] in … }` of every
// controller); GTK: the window's callThen and its closed checks
// (ui/internal/window/actions.go). docs/windows-port.md §7.
//
// A Swift Task always defers; a C# async method runs synchronously up to
// its first incomplete await. Ported literally, a transport that fails at
// once leaves ConnectionController's attempt handle set for ever and drops
// MessageCache's waiters (reproduced). So every fire-and-forget path starts
// here, with await Task.Yield() before anything else.

using System;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Transport;

namespace Malachi.Core.Controllers.Infrastructure;

/// <summary>
/// The life of a controller on the UI thread, or of controllers that live
/// and die together (the mailbox with its list and actions halves): the
/// thread it belongs to, its <see cref="IsClosed"/> flag, the
/// <see cref="Lifetime"/> token its calls end with, the
/// <see cref="PendingWork"/> its background work is counted in, and the
/// helpers that start that work.
/// </summary>
/// <remarks>
/// <para>
/// Create it on the UI thread. <see cref="Perform{TResult}(Func{CancellationToken, Task{TResult}}, Action{Outcome{TResult}})"/>,
/// <see cref="Run"/> and <see cref="RunDetached"/> return before their work
/// starts, as a Swift <c>Task</c> does: the work begins with
/// <c>await Task.Yield()</c>, which posts it to the UI thread's context, and
/// every continuation after an <c>await</c> comes back there, so the work may
/// touch the controller's state without locks (no
/// <c>ConfigureAwait(false)</c> in controllers).
/// </para>
/// <para>
/// <see cref="Close"/> sets <see cref="IsClosed"/> and cancels
/// <see cref="Lifetime"/>: calls in flight end, and their outcomes are
/// dropped instead of reaching a closed window (Swift's <c>closed</c> guard
/// and <c>[weak self]</c>).
/// </para>
/// </remarks>
public sealed class ControllerScope : IDisposable
{
    private readonly CancellationTokenSource lifetime = new();

    /// <summary>A scope of the calling (UI) thread whose work <paramref name="pending"/> counts; a tracker of its own when null.</summary>
    public ControllerScope(PendingWork? pending = null)
    {
        Thread = new ThreadAffinity();
        Pending = pending ?? new PendingWork();
    }

    /// <summary>The thread the controllers of the scope belong to.</summary>
    public ThreadAffinity Thread { get; }

    /// <summary>Counts the tracked work; tests wait on its <see cref="PendingWork.IdleAsync"/>.</summary>
    public PendingWork Pending { get; }

    /// <summary>Whether <see cref="Close"/> ran: late outcomes are dropped.</summary>
    public bool IsClosed { get; private set; }

    /// <summary>Cancelled by <see cref="Close"/>; every call of the scope passes it.</summary>
    public CancellationToken Lifetime => lifetime.Token;

    /// <summary>Throws in debug builds when called off the scope's thread.</summary>
    public void VerifyAccess() => Thread.VerifyAccess();

    /// <summary>
    /// Runs <paramref name="call"/> in the background and hands its outcome
    /// to <paramref name="done"/> on the UI thread, unless the scope closed
    /// meanwhile (Swift <c>perform</c>). Tracked. A failure is an outcome,
    /// never an exception of the returned task; an exception of
    /// <paramref name="done"/> fails the task and is reported by
    /// <see cref="Pending"/>.
    /// </summary>
    public Task Perform<TResult>(Func<CancellationToken, Task<TResult>> call, Action<Outcome<TResult>> done)
    {
        ArgumentNullException.ThrowIfNull(call);
        ArgumentNullException.ThrowIfNull(done);
        VerifyAccess();
        var task = PerformAsync(call, done);
        Pending.Track(task);
        return task;
    }

    /// <summary>
    /// One RPC call with the method's own timeout, or
    /// <paramref name="timeout"/>, its outcome handed to
    /// <paramref name="done"/> as by
    /// <see cref="Perform{TResult}(Func{CancellationToken, Task{TResult}}, Action{Outcome{TResult}})"/>.
    /// </summary>
    public Task Perform<TParams, TResult>(
        RpcClient client, RpcMethod<TParams, TResult> method, TParams parameters, Action<Outcome<TResult>> done, TimeSpan? timeout = null)
    {
        ArgumentNullException.ThrowIfNull(client);
        ArgumentNullException.ThrowIfNull(method);
        return Perform(ct => client.CallAsync(method, parameters, timeout ?? method.Timeout, ct), done);
    }

    /// <summary>
    /// Runs <paramref name="work"/> in the background on the UI thread, with
    /// <see cref="Lifetime"/>; tracked, so it must end on its own (a wait on
    /// the clock belongs in <see cref="RunDetached"/>). The work checks
    /// <see cref="IsClosed"/> itself where it matters.
    /// </summary>
    public Task Run(Func<CancellationToken, Task> work)
    {
        ArgumentNullException.ThrowIfNull(work);
        VerifyAccess();
        var task = RunAsync(work);
        Pending.Track(task);
        return task;
    }

    /// <summary>
    /// Runs <paramref name="work"/> in the background on the UI thread, with
    /// <see cref="Lifetime"/>, without tracking it: for loops that live as
    /// long as the scope (the reconnect loop, a stream's reader) and for
    /// waits on the clock, which a fake clock would hold for ever. What the
    /// work starts through <see cref="Perform{TResult}(Func{CancellationToken, Task{TResult}}, Action{Outcome{TResult}})"/>
    /// or <see cref="Run"/> is tracked. Its end by <see cref="Lifetime"/> is
    /// quiet; a failure is reported by <see cref="Pending"/>.
    /// </summary>
    public Task RunDetached(Func<CancellationToken, Task> work)
    {
        ArgumentNullException.ThrowIfNull(work);
        VerifyAccess();
        return RunDetachedAsync(work);
    }

    /// <summary>Marks the scope closed and cancels <see cref="Lifetime"/>. Idempotent.</summary>
    public void Close()
    {
        if (IsClosed)
        {
            return;
        }
        IsClosed = true;
        lifetime.Cancel();
    }

    /// <summary>Closes the scope.</summary>
    public void Dispose() => Close();

    private async Task PerformAsync<TResult>(Func<CancellationToken, Task<TResult>> call, Action<Outcome<TResult>> done)
    {
        await Task.Yield();
        Outcome<TResult> outcome;
        try
        {
            outcome = Outcome.Success(await call(Lifetime));
        }
#pragma warning disable CA1031 // Every failure of the call is its outcome, as Swift's Result is.
        catch (Exception e)
#pragma warning restore CA1031
        {
            outcome = Outcome.Failure<TResult>(e);
        }
        if (IsClosed)
        {
            return;
        }
        done(outcome);
    }

    private async Task RunAsync(Func<CancellationToken, Task> work)
    {
        await Task.Yield();
        await work(Lifetime);
    }

    private async Task RunDetachedAsync(Func<CancellationToken, Task> work)
    {
        await Task.Yield();
        try
        {
            await work(Lifetime);
        }
        catch (OperationCanceledException) when (Lifetime.IsCancellationRequested)
        {
            // Closed: the loop ends with the scope.
        }
#pragma warning disable CA1031 // Reported, not lost: nobody awaits a detached task.
        catch (Exception e)
#pragma warning restore CA1031
        {
            Pending.Report(e);
        }
    }
}
