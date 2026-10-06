// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/BoardPreferencesControllerTests.swift
// and of ui/internal/boardtriage/preferences_test.go, whose
// TestPreferencesWriteFromDone ends the file: the board's preferences
// against a fake daemon (BoardTriageDaemon): loading, optimistic writes,
// their order and their revert. Swift's controller going away is Close
// here (writeCompletesWhenTheControllerGoes). Go's TestWhyOf has no port:
// the reasons are Board.Text.Failed's (BoardTextTests).

using System;
using System.Collections.Generic;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Tests.Fixtures;
using Xunit;
using D = Malachi.Core.Tests.Controllers.BoardTriageDaemon;

namespace Malachi.Core.Tests.Controllers;

public sealed class BoardPreferencesControllerTests
{
    [Fact]
    public async Task Loads()
    {
        await using var h = await Harness.StartAsync();
        h.D.Preferences = D.Prefs(assistant: true, autoTriage: true, autoTriageMinutes: 60);
        var reports = 0;
        var token = await h.Ui.RunAsync(() => h.C.Observe(() => reports++));
        Assert.Null(await h.Ui.RunAsync(() => h.C.Preferences));
        Assert.True(await h.Ui.InvokeAsync(h.C.LoadNowAsync));
        D.Same(D.Prefs(assistant: true, autoTriage: true, autoTriageMinutes: 60), await h.Ui.RunAsync(() => h.C.Preferences));
        Assert.Equal(1, reports);
        // The same answer again reports nothing.
        Assert.True(await h.Ui.InvokeAsync(h.C.LoadNowAsync));
        Assert.Equal(1, await h.Ui.RunAsync(() => reports));
        await h.Ui.RunAsync(token.Cancel);
    }

    /// <summary>A write shows at once, sends the whole object, and the answer stands.</summary>
    [Fact]
    public async Task OptimisticWrite()
    {
        await using var h = await Harness.StartAsync();
        Assert.True(await h.Ui.InvokeAsync(h.C.LoadNowAsync));
        h.D.Sets.Hold(true);
        var results = new List<bool>();
        await h.Ui.RunAsync(() =>
        {
            h.C.Update(p => p with { AutoTriage = true }, completion: results.Add);
            Assert.True(h.C.Preferences?.AutoTriage);
        });
        await D.UntilAsync(h.Ui, () => h.D.Sets.Waiting == 1);
        h.D.Sets.Hold(false);
        await D.UntilAsync(h.Ui, () => results.Count == 1 && h.C.IsIdle);
        Assert.Equal([true], results);
        D.Same([D.Prefs(autoTriage: true)], h.D.SetCalls);
        D.Same(D.Prefs(autoTriage: true), await h.Ui.RunAsync(() => h.C.Preferences));
    }

    /// <summary>A refused write is taken back and says why.</summary>
    [Fact]
    public async Task RefusedWriteIsTakenBack()
    {
        await using var h = await Harness.StartAsync();
        Assert.True(await h.Ui.InvokeAsync(h.C.LoadNowAsync));
        h.D.SetFailure = D.Error(ErrorCode.InvalidArgument, "autoTriageMinutes out of range");
        h.D.Sets.Hold(true);
        var reports = 0;
        await h.Ui.RunAsync(() => h.C.Observe(() => reports++));
        var task = await h.Ui.RunAsync(() => h.C.UpdateAsync(p => p with { AutoTriageMinutes = 3 }));
        await D.UntilAsync(h.Ui, () => h.C.Preferences?.AutoTriageMinutes == 3);
        h.D.Sets.Hold(false);
        Assert.False(await task);
        D.Same(D.Prefs(), await h.Ui.RunAsync(() => h.C.Preferences));
        Assert.Equal(2, await h.Ui.RunAsync(() => reports));
        Assert.Equal(["Changing the board’s settings failed: the board did not accept it."], await h.ToastsAsync());
    }

    /// <summary>Writes go one after another, each with what the earlier left.</summary>
    [Fact]
    public async Task WritesInOrder()
    {
        await using var h = await Harness.StartAsync();
        Assert.True(await h.Ui.InvokeAsync(h.C.LoadNowAsync));
        h.D.Sets.Hold(true);
        await h.Ui.RunAsync(() =>
        {
            h.C.Update(p => p with { AutoTriage = true });
            h.C.Update(p => p with { AutoTriageDailyCases = 10 });
            D.Same(D.Prefs(autoTriage: true, autoTriageDailyCases: 10), h.C.Preferences);
        });
        await D.UntilAsync(h.Ui, () => h.D.Sets.Waiting == 1);
        h.D.Sets.Hold(false);
        await D.UntilAsync(h.Ui, () => h.C.IsIdle);
        D.Same([D.Prefs(autoTriage: true), D.Prefs(autoTriage: true, autoTriageDailyCases: 10)], h.D.SetCalls);
        D.Same(D.Prefs(autoTriage: true, autoTriageDailyCases: 10), await h.Ui.RunAsync(() => h.C.Preferences));
    }

    /// <summary>A write before the first load loads first; a load that fails takes the write back.</summary>
    [Fact]
    public async Task WriteBeforeLoad()
    {
        await using var h = await Harness.StartAsync();
        h.D.Preferences = D.Prefs(hot: 7);
        Assert.True(await await h.Ui.RunAsync(() => h.C.UpdateAsync(p => p with { Assistant = true })));
        Assert.Equal(1, h.D.Gets);
        D.Same([D.Prefs(assistant: true, hot: 7)], h.D.SetCalls);
        D.Same(D.Prefs(assistant: true, hot: 7), await h.Ui.RunAsync(() => h.C.Preferences));

        var errors = new List<string>();
        var other = await h.Ui.RunAsync(() =>
        {
            var o = new BoardPreferencesController(h.D.Client, pending: h.Pending);
            o.ToastRequested += (_, e) => errors.Add(e);
            return o;
        });
        h.D.GetFailure = D.Error(ErrorCode.StorageError, "disk");
        Assert.False(await await h.Ui.RunAsync(() => other.UpdateAsync(p => p with { Assistant = false })));
        Assert.Null(await h.Ui.RunAsync(() => other.Preferences));
        Assert.Equal(["Changing the board’s settings failed: the mail backend could not save it."], await h.Ui.RunAsync(() => errors.ToArray()));
        Assert.Single(h.D.SetCalls);
        await h.Ui.RunAsync(other.Close);
    }

    /// <summary>A write reads the preferences afresh and lays only its own change over them: what another client changed since the last load stays.</summary>
    [Fact]
    public async Task WriteKeepsAnotherClientsChange()
    {
        await using var h = await Harness.StartAsync();
        Assert.True(await h.Ui.InvokeAsync(h.C.LoadNowAsync));
        // Another client changes the interval after this one loaded.
        h.D.Preferences = D.Prefs(autoTriageMinutes: 60);
        Assert.True(await await h.Ui.RunAsync(() => h.C.UpdateAsync(p => p with { AutoTriage = true })));
        D.Same([D.Prefs(autoTriage: true, autoTriageMinutes: 60)], h.D.SetCalls);
        D.Same(D.Prefs(autoTriage: true, autoTriageMinutes: 60), await h.Ui.RunAsync(() => h.C.Preferences));
        // A fresh read that fails: the write goes on with the last one.
        h.D.GetFailure = D.Error(ErrorCode.StorageError, "disk");
        Assert.True(await await h.Ui.RunAsync(() => h.C.UpdateAsync(p => p with { AutoTriageDailyCases = 20 })));
        D.Same(D.Prefs(autoTriage: true, autoTriageMinutes: 60, autoTriageDailyCases: 20), h.D.SetCalls[^1]);
    }

    /// <summary>A quiet write that fails says nothing through ToastRequested; the result still tells.</summary>
    [Fact]
    public async Task QuietWrite()
    {
        await using var h = await Harness.StartAsync();
        Assert.True(await h.Ui.InvokeAsync(h.C.LoadNowAsync));
        h.D.SetFailure = D.Error(ErrorCode.StorageError, "disk");
        Assert.False(await await h.Ui.RunAsync(() => h.C.UpdateAsync(p => p with { Assistant = true }, quiet: true)));
        Assert.Empty(await h.ToastsAsync());
        Assert.False(await await h.Ui.RunAsync(() => h.C.UpdateAsync(p => p with { Assistant = true })));
        Assert.Single(await h.ToastsAsync());
    }

    /// <summary>
    /// A write still waiting when the controller closes completes, with a
    /// failure, so an awaited update never hangs; the one in flight is still
    /// sent and stored (Swift: the controller goes away).
    /// </summary>
    [Fact]
    public async Task WriteCompletesWhenTheControllerGoes()
    {
        await using var h = await Harness.StartAsync();
        Assert.True(await h.Ui.InvokeAsync(h.C.LoadNowAsync));
        h.D.Sets.Hold(true);
        var results = new List<bool>();
        await h.Ui.RunAsync(() =>
        {
            h.C.Update(p => p with { AutoTriage = true }, completion: results.Add);
            h.C.Update(p => p with { AutoTriageDailyCases = 10 }, completion: results.Add);
        });
        await D.UntilAsync(h.Ui, () => h.D.Sets.Waiting == 1);
        await h.Ui.RunAsync(h.C.Close);
        h.D.Sets.Hold(false);
        await D.UntilAsync(h.Ui, () => results.Count == 2);
        Assert.Equal([true, false], results);
        Assert.True(h.D.Preferences.AutoTriage);
    }

    /// <summary>LastLoadFailed follows the loads; ObserveLoaded reports each answer.</summary>
    [Fact]
    public async Task LoadState()
    {
        await using var h = await Harness.StartAsync();
        var loads = 0;
        var token = await h.Ui.RunAsync(() => h.C.ObserveLoaded(() => loads++));
        h.D.GetFailure = D.Error(ErrorCode.StorageError, "disk");
        Assert.False(await h.Ui.InvokeAsync(h.C.LoadNowAsync));
        Assert.True(await h.Ui.RunAsync(() => h.C.LastLoadFailed && loads == 0 && h.C.Stored is null));
        h.D.GetFailure = null;
        Assert.True(await h.Ui.InvokeAsync(h.C.LoadNowAsync));
        Assert.True(await h.Ui.RunAsync(() => !h.C.LastLoadFailed && loads == 1));
        D.Same(D.Prefs(), await h.Ui.RunAsync(() => h.C.Stored));
        await h.Ui.RunAsync(token.Cancel);
    }

    /// <summary>Go TestPreferencesWriteFromDone: a write whose completion starts another runs them one after another.</summary>
    [Fact]
    public async Task WriteFromDone()
    {
        await using var h = await Harness.StartAsync();
        Assert.True(await h.Ui.InvokeAsync(h.C.LoadNowAsync));
        var results = new List<bool>();
        await h.Ui.RunAsync(() => h.C.Update(p => p with { AutoTriage = true }, completion: ok =>
        {
            results.Add(ok);
            h.C.Update(p => p with { AutoTriageDailyCases = 20 }, completion: results.Add);
        }));
        await D.UntilAsync(h.Ui, () => results.Count == 2 && h.C.IsIdle);
        await h.IdleAsync();
        Assert.Equal([true, true], results);
        Assert.Equal(2, h.D.SetCalls.Count);
    }

    private sealed class Harness : IAsyncDisposable
    {
        private readonly List<string> toasts = [];

        private Harness(D daemon)
        {
            D = daemon;
        }

        public TestUIContext Ui { get; } = new();

        public PendingWork Pending { get; } = new();

        public D D { get; }

        public BoardPreferencesController C { get; private set; } = null!;

        public static async Task<Harness> StartAsync()
        {
            var h = new Harness(await BoardTriageDaemon.StartAsync());
            h.C = await h.Ui.RunAsync(() =>
            {
                var c = new BoardPreferencesController(h.D.Client, pending: h.Pending);
                c.ToastRequested += (_, t) => h.toasts.Add(t);
                return c;
            });
            return h;
        }

        public Task<string[]> ToastsAsync() => Ui.RunAsync(() => toasts.ToArray());

        public Task IdleAsync() => Quiescence.IdleAsync(Ui, Pending, D.Fake);

        public async ValueTask DisposeAsync()
        {
            await Ui.RunAsync(C.Close);
            D.Sets.Hold(false);
            try
            {
                await IdleAsync();
            }
            catch (Exception e) when (e is TimeoutException or AggregateException)
            {
                // A test's own failure, reported there.
            }
            await D.DisposeAsync();
            Ui.Dispose();
        }
    }
}
