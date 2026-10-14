// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/BoardAutoTriageScheduler.swift
// (BoardAutoTriageTarget); GTK: ui/internal/boardtriage/scheduler.go
// (AutoTarget).

using System;
using Malachi.Core.Boards;

namespace Malachi.Core.Controllers;

/// <summary>
/// What the automatic triage's schedule reads and starts: the application's
/// <see cref="BoardTriageController"/> (the tests put a stand-in here).
/// </summary>
public interface IBoardAutoTriageTarget
{
    /// <summary>
    /// The rule's inputs as the target knows them: the preferences, the
    /// availability, the sign-in, the consents, its run, the board's queue
    /// and counts, and the daemon's last automatic run as the last attempt.
    /// The schedule sets the trigger, the clock, its own last attempt and the
    /// failures.
    /// </summary>
    Board.AutoTriage.Inputs AutoTriageInputs { get; }

    /// <summary>Changes whenever the board reported new triage data.</summary>
    int BoardRevision { get; }

    /// <summary>The run that ended last; null before one.</summary>
    BoardTriageEnd? LastEnded { get; }

    /// <summary>Starts a run of at most <paramref name="limit"/> cases; false when one is active.</summary>
    bool Start(Board.TriageTrigger trigger, int? limit = null);

    /// <summary>Why automatic triage pauses, for the status strip (null: it does not).</summary>
    void SetAutoPause(Board.AutoTriagePause? pause);

    /// <summary>Asks whether Claude Code is signed in afresh (after a sign-in elsewhere, such as in a terminal).</summary>
    void RecheckSignIn();

    /// <summary>Calls <paramref name="f"/> after any change the target reports.</summary>
    BoardObserverToken Observe(Action f);

    /// <summary>Calls <paramref name="f"/> once each time a run ended (read through <see cref="LastEnded"/>).</summary>
    BoardObserverToken ObserveEnded(Action f);
}
