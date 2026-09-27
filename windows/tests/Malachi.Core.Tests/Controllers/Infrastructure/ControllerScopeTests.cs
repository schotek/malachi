// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only tests (docs/windows-port.md §7): the Perform helper and the
// scope that every controller is built on. The first test reproduces the
// trap of a literal port (§7.2, research 02 experiment 1a/1b): the
// reconnect attempt handle of ConnectionController.reconnectNow, cleared
// by an attempt that fails at once.

using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Transport;
using Xunit;

namespace Malachi.Core.Tests.Controllers.Infrastructure;

public sealed class ControllerScopeTests
{
#if DEBUG
    private const bool DebugBuild = true;
#else
    private const bool DebugBuild = false;
#endif

    /// <summary>
    /// An attempt that fails before its first real await still finds its
    /// handle set, and clears it: work starts only after Run has returned.
    /// </summary>
    [Fact]
    public async Task WorkStartsAfterTheCallerHasItsHandle()
    {
        using var ui = new TestUIContext();
        var pending = new PendingWork();
        Task? attempt = null;
        bool? handleWasSetWhenTheWorkRan = null;
        await ui.RunAsync(() =>
        {
            var scope = new ControllerScope(pending);
            attempt = scope.Run(_ =>
            {
                handleWasSetWhenTheWorkRan = attempt is not null;
                attempt = null; // the attempt ends: the next one may start
                return Task.FromException(new RpcClientException(ClientError.NotConnected));
            });
        });
        await pending.IdleAsync(TestContext.Current.CancellationToken);
        await ui.DrainAsync();
        Assert.True(handleWasSetWhenTheWorkRan);
        Assert.Null(attempt);
        Assert.IsType<RpcClientException>(Assert.Single(pending.TakeFaults()));
    }

    [Fact]
    public async Task PerformHandsTheOutcomeToTheUIThread()
    {
        using var ui = new TestUIContext();
        var pending = new PendingWork();
        var threads = new List<int>();
        Outcome<int>? got = null;
        var ranInside = false;
        await ui.RunAsync(() =>
        {
            var scope = new ControllerScope(pending);
            scope.Perform(
                async ct =>
                {
                    ranInside = true;
                    threads.Add(Environment.CurrentManagedThreadId);
                    await Task.Delay(1, ct);
                    return 42;
                },
                outcome =>
                {
                    threads.Add(Environment.CurrentManagedThreadId);
                    got = outcome;
                });
            Assert.False(ranInside, "the call runs after Perform returned");
        });
        await Quiescence.IdleAsync(ui, pending);
        Assert.Equal([ui.ThreadId, ui.ThreadId], threads);
        Assert.True(got!.Value.IsSuccess);
        Assert.Equal(42, got.Value.Value);
    }

    [Fact]
    public async Task AFailureIsAnOutcome()
    {
        using var ui = new TestUIContext();
        var pending = new PendingWork();
        Outcome<int>? got = null;
        await ui.RunAsync(() => new ControllerScope(pending).Perform<int>(
            _ => throw new InvalidOperationException("broken"),
            outcome => got = outcome));
        await Quiescence.IdleAsync(ui, pending);
        Assert.False(got!.Value.IsSuccess);
        Assert.False(got.Value.TryGetValue(out _, out var error));
        Assert.Equal("broken", error!.Message);
    }

    /// <summary>Swift's closed guard: a late outcome after Close() is dropped, and the call was cancelled.</summary>
    [Fact]
    public async Task CloseDropsLateOutcomesAndCancelsTheCall()
    {
        using var ui = new TestUIContext();
        var pending = new PendingWork();
        var started = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var cancelled = false;
        var delivered = false;
        ControllerScope? scope = null;
        await ui.RunAsync(() =>
        {
            scope = new ControllerScope(pending);
            scope.Perform(
                async ct =>
                {
                    started.SetResult();
                    try
                    {
                        await Task.Delay(Timeout.Infinite, ct);
                    }
                    catch (OperationCanceledException)
                    {
                        cancelled = true;
                        throw;
                    }
                    return 1;
                },
                _ => delivered = true);
        });
        await started.Task;
        await ui.RunAsync(() => scope!.Close());
        await Quiescence.IdleAsync(ui, pending);
        Assert.True(cancelled);
        Assert.False(delivered);
        Assert.True(scope!.IsClosed);
        Assert.True(scope.Lifetime.IsCancellationRequested);
    }

    [Fact]
    public async Task PerformCallsTheDaemon()
    {
        await using var fake = new FakeDaemon((method, line) => Transport.RpcClientTests.Standard(method, line));
        await fake.StartAsync();
        using var client = new RpcClient(fake.Path, PortableKeyFilePolicy.Instance);
        await client.ConnectAsync(TestContext.Current.CancellationToken);
        using var ui = new TestUIContext();
        var pending = new PendingWork();
        var outcomes = new List<Outcome<SystemInfoResult>>();
        await ui.RunAsync(() =>
        {
            var scope = new ControllerScope(pending);
            scope.Perform(client, API.SystemInfo, new EmptyParams(), outcomes.Add);
        });
        await Quiescence.IdleAsync(ui, pending, fake);
        Assert.Equal(42, Assert.Single(outcomes).Value!.Pid);

        // A call that cannot go out fails at once; its outcome still comes
        // after Perform returned, on the UI thread.
        client.Close();
        var returned = false;
        await ui.RunAsync(() =>
        {
            var scope = new ControllerScope(pending);
            scope.Perform(client, API.SystemInfo, new EmptyParams(), o =>
            {
                Assert.True(returned);
                outcomes.Add(o);
            });
            returned = true;
        });
        await Quiescence.IdleAsync(ui, pending, fake);
        var refused = Assert.IsType<RpcClientException>(outcomes[1].Error);
        Assert.Equal(ClientError.NotConnected, refused.Error);
    }

    /// <summary>A detached loop is not waited for, ends quietly with the scope, and its failure is reported.</summary>
    [Fact]
    public async Task DetachedWorkIsNotTracked()
    {
        using var ui = new TestUIContext();
        var pending = new PendingWork();
        var looping = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        ControllerScope? scope = null;
        Task? loop = null;
        await ui.RunAsync(() =>
        {
            scope = new ControllerScope(pending);
            loop = scope.RunDetached(async ct =>
            {
                looping.SetResult();
                await Task.Delay(Timeout.Infinite, ct);
            });
        });
        await looping.Task;
        await Quiescence.IdleAsync(ui, pending); // does not wait for the loop
        await ui.RunAsync(() => scope!.Close());
        await loop!;
        Assert.Empty(pending.TakeFaults());

        await ui.RunAsync(() => new ControllerScope(pending).RunDetached(_ => throw new InvalidOperationException("loop broke")));
        await ui.DrainAsync();
        await ui.DrainAsync();
        Assert.Equal("loop broke", Assert.Single(pending.TakeFaults()).Message);
    }

    [Fact]
    public async Task AScopeBelongsToItsThread()
    {
        using var ui = new TestUIContext();
        var scope = await ui.RunAsync(() => new ControllerScope());
        Assert.Equal(ui.ThreadId, scope.Thread.ThreadId);
        Assert.Same(ui, scope.Thread.Context);
        Assert.True(await ui.RunAsync(scope.Thread.CheckAccess));
        Assert.False(scope.Thread.CheckAccess());
        Assert.SkipUnless(DebugBuild, "VerifyAccess checks in debug builds only");
        Assert.Throws<InvalidOperationException>(scope.VerifyAccess);
        Assert.IsType<InvalidOperationException>(Record.Exception(() => { _ = scope.Perform(_ => Task.FromResult(1), _ => { }); }));
        await ui.RunAsync(scope.VerifyAccess);
    }
}
