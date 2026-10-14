// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/BoardPreferencesController.swift
// (BoardPreferencesController); GTK: ui/internal/boardtriage/preferences.go
// (Preferences).
//
// The board's preferences in the daemon (docs/api.md §4.13
// board.preferences, board.setPreferences): whether the board is on,
// whether the assistant's notes count, the windows, the triage accounts and
// the automatic triage's schedule, once for the whole application (the
// triage controller, its schedule and Preferences → AI read the same
// object).
//
// Preferences is null until the daemon answered. Load asks again (at the
// start, on a reconnect, when a window shows the board); a reply that a
// newer load or a lost connection overtook is dropped. Update is optimistic
// like the board's other writes: the change is laid over the daemon's
// preferences at once and reported, then board.setPreferences sends the
// whole object with it (writes go one after another, each with what the
// earlier ones left), and its answer, the preferences as stored, replaces
// it; a refused write is taken back, reported, and ToastRequested says why.
// The contract has no partial write (docs/api.md §4.13: "every field"), so
// each write reads the preferences afresh first and lays only the user's
// changes (the writes' functions) over them: a field another client changed
// since the last load is not written back. A write whose fresh read fails
// goes on with the preferences last read; with none, it fails.
//
// Windows: Swift's inout closures are functions from the preferences to the
// changed ones (`p => p with { AutoTriage = true }`); its onError is
// ToastRequested; the writes wait in a queue as Go's do. The write itself is
// sent even when the controller closes meanwhile (a mutation, as
// PerformPastClose), and the writes still waiting then complete with false,
// so an awaited Update never hangs (Swift's writeCompletesWhenTheControllerGoes).
// The record compares its account list by reference, so the preferences are
// compared field by field as Go's samePrefs does. Create it, and call it, on
// the UI thread.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Controllers;

/// <summary>The board's preferences in the daemon, once for the application (Swift <c>BoardPreferencesController</c>).</summary>
public sealed partial class BoardPreferencesController : IDisposable
{
    private static readonly AccountId[] NoAccounts = [];

    private readonly RpcClient client;
    private readonly ControllerScope scope;
    private readonly ILogger logger;
    private readonly BoardObservers observers = new();
    private readonly BoardObservers loaded = new();

    // The writes under way, laid over the daemon's preferences in order.
    private readonly List<(int Token, Func<BoardPreferences, BoardPreferences> Apply)> overlays = [];

    // The writes under way or waiting, in order; the first is in flight while
    // writing.
    private readonly List<(int Token, bool Quiet, Action<bool>? Done)> writes = [];

    // The daemon's preferences as last answered.
    private BoardPreferences? stored;
    private int nextToken;
    private bool writing;

    // Bumped by every load and a lost connection: older replies are dropped.
    private int loadGeneration;
    private bool loading;

    // Why the last load failed, for the toast of a write that needed it.
    private Exception? loadError;

    /// <summary>A controller over <paramref name="client"/>, on the calling (UI) thread.</summary>
    /// <param name="client">The daemon.</param>
    /// <param name="logger">Receives method names and error kinds, never values.</param>
    /// <param name="pending">Counts the controller's background work; one of its own when null.</param>
    public BoardPreferencesController(RpcClient client, ILogger<BoardPreferencesController>? logger = null, PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(client);
        this.client = client;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
        scope = new ControllerScope(pending);
    }

    /// <summary>
    /// Called with a short sentence for a toast when a write failed (it is
    /// taken back by then), unless the write was quiet (Swift <c>onError</c>).
    /// </summary>
    public event EventHandler<string>? ToastRequested;

    /// <summary>The preferences with the writes under way; null until the daemon answered.</summary>
    public BoardPreferences? Preferences { get; private set; }

    /// <summary>The daemon's preferences as last read, without the writes under way; null until the daemon answered.</summary>
    public BoardPreferences? Stored => stored;

    /// <summary>The last <c>board.preferences</c> failed (the daemon is unreachable, or does not know the board); false once one answered.</summary>
    public bool LastLoadFailed { get; private set; }

    /// <summary>A write is under way or waiting.</summary>
    public bool Writing => writes.Count > 0;

    /// <summary>Nothing is loading or being written (the tests wait for it).</summary>
    public bool IsIdle => !loading && writes.Count == 0;

    /// <summary>Whether <see cref="Close"/> ran.</summary>
    public bool IsClosed => scope.IsClosed;

    /// <summary>Calls <paramref name="f"/> after every change of <see cref="Preferences"/> (and of <see cref="LastLoadFailed"/>).</summary>
    public BoardObserverToken Observe(Action f)
    {
        ArgumentNullException.ThrowIfNull(f);
        return observers.Add(() => scope.Guard(f));
    }

    /// <summary>Calls <paramref name="f"/> after every answer of <c>board.preferences</c> (<see cref="Stored"/> holds it), also the reads before a write.</summary>
    public BoardObserverToken ObserveLoaded(Action f)
    {
        ArgumentNullException.ThrowIfNull(f);
        return loaded.Add(() => scope.Guard(f));
    }

    /// <summary>Drops the loads on their way and fails the writes still waiting; a write in flight is still sent.</summary>
    public void Close()
    {
        scope.VerifyAccess();
        scope.Close();
    }

    /// <summary>Closes the controller.</summary>
    public void Dispose() => Close();

    // Loading

    /// <summary>Asks the daemon for the preferences (the answer is reported).</summary>
    public void Load()
    {
        scope.VerifyAccess();
        if (IsClosed)
        {
            return;
        }
        scope.Run(async _ => await LoadNowAsync());
    }

    /// <summary>Asks the daemon now and waits for the answer: true when it came (and no newer load overtook it).</summary>
    public async Task<bool> LoadNowAsync()
    {
        scope.VerifyAccess();
        var generation = ++loadGeneration;
        loading = true;
        BoardPreferencesResult? result = null;
        Exception? error = null;
        try
        {
            result = await client.CallAsync(API.BoardPreferences, new EmptyParams(), scope.Lifetime);
        }
        catch (Exception e) when (e is RpcException or RpcClientException or OperationCanceledException or TimeoutException)
        {
            error = e;
        }
        if (generation != loadGeneration || IsClosed)
        {
            return false;
        }
        loading = false;
        if (result is not null)
        {
            loadError = null;
            stored = result.Preferences;
            Publish();
            if (LastLoadFailed)
            {
                LastLoadFailed = false;
                observers.Notify();
            }
            loaded.Notify();
            return true;
        }
        LogCallFailed(API.BoardPreferences.Name, error);
        loadError = error;
        if (!LastLoadFailed)
        {
            LastLoadFailed = true;
            observers.Notify();
        }
        return false;
    }

    /// <summary>
    /// The connection came (the preferences are asked for again) or went
    /// (replies on the way are dropped; the last preferences stay).
    /// </summary>
    public void ConnectionChanged(bool connected)
    {
        scope.VerifyAccess();
        if (connected)
        {
            Load();
            return;
        }
        loadGeneration++;
        loading = false;
    }

    // Writing

    /// <summary>
    /// Changes the preferences: at once here, then in the daemon (see the
    /// class's comment). True when the daemon stored it; false when it was
    /// refused (taken back, <see cref="ToastRequested"/> raised unless
    /// <paramref name="quiet"/>), the preferences could not be loaded first,
    /// or the controller closed before the write ran.
    /// </summary>
    public Task<bool> UpdateAsync(Func<BoardPreferences, BoardPreferences> change, bool quiet = false)
    {
        var done = new TaskCompletionSource<bool>(TaskCreationOptions.RunContinuationsAsynchronously);
        Update(change, quiet, stored => done.TrySetResult(stored));
        return done.Task;
    }

    /// <summary><see cref="UpdateAsync"/> with a completion instead of a task; the completion is always called once.</summary>
    public void Update(Func<BoardPreferences, BoardPreferences> change, bool quiet = false, Action<bool>? completion = null)
    {
        ArgumentNullException.ThrowIfNull(change);
        scope.VerifyAccess();
        if (IsClosed)
        {
            Completed(completion, false);
            return;
        }
        var token = ++nextToken;
        overlays.Add((token, change));
        Publish();
        writes.Add((token, quiet, completion));
        if (!writing)
        {
            WriteNext();
        }
    }

    // Sends the first waiting write.
    private void WriteNext()
    {
        writing = true;
        var w = writes[0];
        scope.Run(_ => WriteAsync(w.Token, w.Quiet));
    }

    // Sends the preferences as they are with write token and the ones before
    // it, read afresh (see the class's comment), and takes the answer.
    private async Task WriteAsync(int token, bool quiet)
    {
        if (IsClosed)
        {
            Lift(token);
            Complete(false);
            return;
        }
        var fresh = await LoadNowAsync();
        if (IsClosed)
        {
            Lift(token);
            Complete(false);
            return;
        }
        if (!fresh && stored is null)
        {
            Lift(token);
            Failed(quiet, loadError ?? new RpcClientException(ClientError.NotConnected));
            return;
        }
        var wanted = stored!;
        foreach (var o in overlays)
        {
            wanted = o.Apply(wanted);
            if (o.Token == token)
            {
                break;
            }
        }
        BoardSetPreferencesResult result;
        try
        {
            // A mutation: sent even when the controller closes meanwhile.
            result = await client.CallAsync(API.BoardSetPreferences, new BoardSetPreferencesParams { Preferences = wanted }, CancellationToken.None);
        }
        catch (Exception e) when (e is RpcException or RpcClientException or OperationCanceledException or TimeoutException)
        {
            LogCallFailed(API.BoardSetPreferences.Name, e);
            Lift(token);
            Failed(quiet || IsClosed, e);
            return;
        }
        if (!IsClosed)
        {
            stored = result.Preferences;
        }
        Lift(token);
        Complete(true);
    }

    // Reports a failed write unless quiet, and completes it.
    private void Failed(bool quiet, Exception error)
    {
        if (!quiet)
        {
            scope.Raise(ToastRequested, this, Board.Text.Failed(Board.Text.Action.Preferences, error));
        }
        Complete(false);
    }

    // Ends the first write with stored and starts the next; once closed, the
    // writes still waiting end with false.
    private void Complete(bool stored)
    {
        var w = writes[0];
        writes.RemoveAt(0);
        writing = false;
        Completed(w.Done, stored);
        // A completion may have started the next write itself.
        if (writing || writes.Count == 0)
        {
            return;
        }
        if (IsClosed)
        {
            var left = writes.ToList();
            writes.Clear();
            foreach (var x in left)
            {
                overlays.RemoveAll(o => o.Token == x.Token);
                Completed(x.Done, false);
            }
            return;
        }
        WriteNext();
    }

    private void Completed(Action<bool>? done, bool stored)
    {
        if (done is not null)
        {
            scope.Guard(() => done(stored));
        }
    }

    // Drops the overlay of write token.
    private void Lift(int token)
    {
        overlays.RemoveAll(o => o.Token == token);
        Publish();
    }

    // Builds Preferences from the daemon's and the writes under way and
    // reports it when it changed.
    private void Publish()
    {
        if (IsClosed)
        {
            return;
        }
        var next = stored;
        if (next is not null)
        {
            foreach (var o in overlays)
            {
                next = o.Apply(next);
            }
        }
        if (Same(next, Preferences))
        {
            return;
        }
        Preferences = next;
        observers.Notify();
    }

    /// <summary>Whether <paramref name="a"/> and <paramref name="b"/> hold the same preferences, the accounts compared in order (Go <c>samePrefs</c>).</summary>
    public static bool Same(BoardPreferences? a, BoardPreferences? b)
    {
        if (a is null || b is null)
        {
            return ReferenceEquals(a, b);
        }
        return a.TriageAccounts.SequenceEqual(b.TriageAccounts)
            && (a with { TriageAccounts = NoAccounts }) == (b with { TriageAccounts = NoAccounts });
    }

    private void LogCallFailed(string method, Exception? error)
    {
        if (logger.IsEnabled(LogLevel.Information))
        {
            var (kind, daemonError) = RpcErrorText.Classify(error);
            LogFailed(logger, method, kind, daemonError?.Code.Value ?? 0);
        }
    }

    [LoggerMessage(Level = LogLevel.Information, Message = "{Method} failed: {Kind} {Code}")]
    private static partial void LogFailed(ILogger logger, string method, RpcErrorText.FailureKind kind, int code);
}
