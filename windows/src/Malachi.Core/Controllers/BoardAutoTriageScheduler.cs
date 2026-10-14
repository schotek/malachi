// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/BoardAutoTriageScheduler.swift
// (BoardAutoTriageScheduler); GTK: ui/internal/boardtriage/scheduler.go
// (Scheduler).
//
// Runs the board's triage on its own (Board.AutoTriage.Decide) while the
// application runs, once for the whole application. It decides again
// whenever its target reports a change: at once for a change of the
// preferences, the consents, the sign-in, the availability or the run;
// after the debounce (a minute) for new board data, so that a burst of new
// mail gives one run; at the time a wait names; and every heartbeat (30
// minutes) while automatic triage is on (asking the sign-in afresh while
// Claude Code is signed out). A run decision starts the target's automatic
// run.
//
// It remembers, in memory only, when it last started an automatic run and
// how many failed in a row (the back-off): a success or a manual run resets
// that. A cancelled run, an empty queue, a declined consent or the
// assistant off are no failures (Board.AutoTriage.CountsAsFailure). After a
// restart the daemon's last automatic run stands in for the last attempt;
// nothing else persists.
//
// Windows: Swift's sleep closure and now are the injected TimeProvider, its
// Calendar a time zone (Go's Location); the waits run detached on the
// clock, each made at once so that a fake clock's next step sees it.
// Create it, and call it, on the UI thread.

using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Boards;
using Malachi.Core.Controllers.Infrastructure;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Controllers;

/// <summary>Runs the board's triage on its own while the application runs (Swift <c>BoardAutoTriageScheduler</c>).</summary>
public sealed partial class BoardAutoTriageScheduler : IDisposable
{
    /// <summary>How long new board data waits for more before the rule is asked.</summary>
    public static readonly TimeSpan DefaultDebounce = TimeSpan.FromSeconds(60);

    /// <summary>The longest time between two decisions while automatic triage is on.</summary>
    public static readonly TimeSpan DefaultHeartbeat = TimeSpan.FromMinutes(30);

    private readonly IBoardAutoTriageTarget target;
    private readonly TimeProvider time;
    private readonly TimeZoneInfo? timeZone;
    private readonly TimeSpan debounce;
    private readonly TimeSpan heartbeat;
    private readonly ControllerScope scope;
    private readonly ILogger logger;
    private readonly List<BoardObserverToken> tokens = [];
    private int seenRevision;
    private Board.TriageFailure? lastFailure;
    private CancellationTokenSource? debounceStop;
    private CancellationTokenSource? wakeStop;
    private bool started;
    private bool deciding;

    /// <summary>A schedule for <paramref name="target"/>, not started, on the calling (UI) thread.</summary>
    /// <param name="target">What it reads and starts (the application's triage).</param>
    /// <param name="time">The clock of the decisions and the waits; the system's when null.</param>
    /// <param name="timeZone">Where days are counted (the daily cap); the local zone when null.</param>
    /// <param name="debounce">How long new board data waits (<see cref="DefaultDebounce"/>).</param>
    /// <param name="heartbeat">The longest time between two decisions (<see cref="DefaultHeartbeat"/>).</param>
    /// <param name="logger">Receives the runs it starts, with their limits.</param>
    /// <param name="pending">Counts the schedule's background work; one of its own when null.</param>
    public BoardAutoTriageScheduler(
        IBoardAutoTriageTarget target,
        TimeProvider? time = null,
        TimeZoneInfo? timeZone = null,
        TimeSpan? debounce = null,
        TimeSpan? heartbeat = null,
        ILogger<BoardAutoTriageScheduler>? logger = null,
        PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(target);
        this.target = target;
        this.time = time ?? TimeProvider.System;
        this.timeZone = timeZone;
        this.debounce = debounce ?? DefaultDebounce;
        this.heartbeat = heartbeat ?? DefaultHeartbeat;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
        scope = new ControllerScope(pending);
    }

    /// <summary>The last decision; null before the first.</summary>
    public Board.AutoTriage.Decision? Decision { get; private set; }

    /// <summary>When it decides again (a wait, or the heartbeat); null when nothing is scheduled.</summary>
    public DateTimeOffset? WakeAt { get; private set; }

    /// <summary>Automatic runs that failed in a row.</summary>
    public int Failures { get; private set; }

    /// <summary>When this schedule last started an automatic run; null before.</summary>
    public DateTimeOffset? LastAttempt { get; private set; }

    /// <summary>New board data waits for the debounce (the tests read it).</summary>
    public bool Debouncing => debounceStop is not null;

    /// <summary>The decisions made so far (the tests read it).</summary>
    public int Evaluations { get; private set; }

    /// <summary>Starts listening and decides once.</summary>
    public void Start()
    {
        scope.VerifyAccess();
        if (started || scope.IsClosed)
        {
            return;
        }
        started = true;
        seenRevision = target.BoardRevision;
        tokens.Add(target.Observe(Changed));
        tokens.Add(target.ObserveEnded(Ended));
        Evaluate();
    }

    /// <summary>Stops: nothing is decided or started any more.</summary>
    public void Stop()
    {
        scope.VerifyAccess();
        started = false;
        foreach (var t in tokens)
        {
            t.Cancel();
        }
        tokens.Clear();
        StopDebounce();
        CancelWake();
        target.SetAutoPause(null);
    }

    /// <summary>Stops, for good.</summary>
    public void Dispose()
    {
        if (scope.IsClosed)
        {
            return;
        }
        Stop();
        scope.Close();
    }

    /// <summary>Asks the rule now and acts on it.</summary>
    public void Evaluate()
    {
        if (!started || deciding)
        {
            return;
        }
        deciding = true;
        try
        {
            Evaluations++;
            var now = time.GetUtcNow();
            var i = target.AutoTriageInputs;
            var last = i.LastAttempt;
            if (LastAttempt is { } mine && (last is null || last < mine))
            {
                last = mine;
            }
            i = i with { Trigger = Board.TriageTrigger.Automatic, Now = now, LastAttempt = last, Failures = Failures };
            var d = Board.AutoTriage.Decide(i, timeZone);
            Decision = d;
            target.SetAutoPause(Pause(d));
            switch (d)
            {
                case Board.AutoTriage.Decision.Run run:
                    LastAttempt = now;
                    CancelWake();
                    if (target.Start(Board.TriageTrigger.Automatic, run.Limit))
                    {
                        LogRun(logger, run.Limit);
                    }
                    break;
                case Board.AutoTriage.Decision.Wait wait:
                    var beat = now + heartbeat;
                    Schedule(wait.Until < beat ? wait.Until : beat);
                    break;
                case Board.AutoTriage.Decision.Off { Reason: Board.AutoTriage.OffReason.SwitchedOff }:
                    CancelWake();
                    break;
                default:
                    Schedule(now + heartbeat);
                    break;
            }
        }
        finally
        {
            deciding = false;
        }
    }

    private void Changed()
    {
        if (!started || deciding)
        {
            return;
        }
        if (target.BoardRevision != seenRevision)
        {
            seenRevision = target.BoardRevision;
            if (debounceStop is not null)
            {
                return;
            }
            var stop = CancellationTokenSource.CreateLinkedTokenSource(scope.Lifetime);
            debounceStop = stop;
            var delay = Task.Delay(debounce, time, stop.Token);
            scope.RunDetached(async _ =>
            {
                try
                {
                    await delay;
                }
                catch (OperationCanceledException)
                {
                    return;
                }
                if (debounceStop != stop)
                {
                    return;
                }
                debounceStop = null;
                stop.Dispose();
                scope.Guard(Evaluate);
            });
            return;
        }
        Evaluate();
    }

    private void Ended()
    {
        if (!started || target.LastEnded is not { } e)
        {
            return;
        }
        if (e.Trigger == Board.TriageTrigger.Manual || e.Failure is null)
        {
            Failures = 0;
            lastFailure = null;
        }
        else if (e.Failure == Board.TriageFailure.Limit)
        {
            // The plan's usage limit: one step longer, not doubling on until
            // a day, since the limit lifts on its own.
            Failures = 1;
            lastFailure = Board.TriageFailure.Limit;
        }
        else if (e.Failure is { } f && Board.AutoTriage.CountsAsFailure(f))
        {
            Failures++;
            lastFailure = f;
        }
        Evaluate();
    }

    // The pause the status strip shows for decision d.
    private Board.AutoTriagePause? Pause(Board.AutoTriage.Decision d) => d switch
    {
        Board.AutoTriage.Decision.Off { Reason: Board.AutoTriage.OffReason.SignedOut } => new Board.AutoTriagePause.SignedOut(),
        Board.AutoTriage.Decision.Off { Reason: Board.AutoTriage.OffReason.Unavailable } => new Board.AutoTriagePause.Unavailable(),
        Board.AutoTriage.Decision.Off { Reason: Board.AutoTriage.OffReason.NoConsent } => new Board.AutoTriagePause.NoConsent(),
        Board.AutoTriage.Decision.Wait w when Failures > 0 && lastFailure is { } f => new Board.AutoTriagePause.Failed(f, w.Until),
        _ => null,
    };

    // Decides again at `at` (a wait, or the heartbeat).
    private void Schedule(DateTimeOffset at)
    {
        if (WakeAt == at && wakeStop is not null)
        {
            return;
        }
        CancelWake();
        WakeAt = at;
        var delay = at - time.GetUtcNow();
        if (delay < TimeSpan.Zero)
        {
            delay = TimeSpan.Zero;
        }
        var stop = CancellationTokenSource.CreateLinkedTokenSource(scope.Lifetime);
        wakeStop = stop;
        var wait = Task.Delay(delay, time, stop.Token);
        scope.RunDetached(async _ =>
        {
            try
            {
                await wait;
            }
            catch (OperationCanceledException)
            {
                return;
            }
            if (wakeStop != stop)
            {
                return;
            }
            wakeStop = null;
            WakeAt = null;
            stop.Dispose();
            scope.Guard(() =>
            {
                if (Decision is Board.AutoTriage.Decision.Off { Reason: Board.AutoTriage.OffReason.SignedOut })
                {
                    target.RecheckSignIn();
                }
                Evaluate();
            });
        });
    }

    private void CancelWake()
    {
        if (wakeStop is { } w)
        {
            wakeStop = null;
            w.Cancel();
        }
        WakeAt = null;
    }

    private void StopDebounce()
    {
        if (debounceStop is { } d)
        {
            debounceStop = null;
            d.Cancel();
        }
    }

    [LoggerMessage(Level = LogLevel.Information, Message = "board triage: automatic run of at most {Limit} cases")]
    private static partial void LogRun(ILogger logger, int limit);
}
