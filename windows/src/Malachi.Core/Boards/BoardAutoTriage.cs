// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Board/BoardAutoTriage.swift; Go:
// ui/internal/boardtriage/auto.go (MaxInterval, AutoInputs, OffReason,
// Decision, Interval, Decide, CountsAsFailure).
//
// When the board's triage runs on its own (docs/api.md §4.13, the
// autoTriage* preferences, which the daemon only stores): the pure rule.
// The scheduler gathers the inputs and acts on the decision; the triage
// controller runs it.
//
// An automatic run starts when automatic triage is on, the assistant can
// run (shown, Claude Code found, the bridge beside the application) and is
// not known to be signed out, both consents are given and the board's
// assistant preference is on, no run is under way, the triage queue has
// cases, the day's cap of cases for automatic runs is not used up, and at
// least autoTriageMinutes passed since the last automatic attempt (after
// Failures failed automatic runs in a row that interval doubles each time,
// at most a day). A success or a manual run resets the failures. A manual
// run ignores the switch, the interval, the cap, the back-off and the queue
// (its consent is asked when it starts).
//
// Swift's Calendar is a TimeZoneInfo (Go's *time.Location): the daemon
// counts the day's cases by its local day. CountsAsFailure is here as in
// Go (auto.go); Swift has it on BoardAutoTriageScheduler.

using System;
using Malachi.Core.Api;
using Malachi.Core.Assistants;

namespace Malachi.Core.Boards;

public static partial class Board
{
    /// <summary>Who starts a triage run.</summary>
    public enum TriageTrigger
    {
        /// <summary>The user's Triage button.</summary>
        Manual,

        /// <summary>The application's schedule.</summary>
        Automatic,
    }

    /// <summary>The rule of automatic triage (Swift <c>Board.AutoTriage</c>).</summary>
    public static class AutoTriage
    {
        /// <summary>The longest wait between two automatic attempts, back-off included.</summary>
        public static readonly TimeSpan MaxInterval = TimeSpan.FromHours(24);

        /// <summary>Why no run starts and nothing is waited for: the next change of the inputs decides again.</summary>
        public enum OffReason
        {
            /// <summary>Automatic triage is off.</summary>
            SwitchedOff,

            /// <summary>The assistant is off, Claude Code was not found, or the bridge is missing.</summary>
            Unavailable,

            /// <summary>Claude Code is signed out.</summary>
            SignedOut,

            /// <summary>A consent is missing, or the board's assistant preference is off.</summary>
            NoConsent,

            /// <summary>A run is under way.</summary>
            Running,

            /// <summary>Nothing waits for the assistant.</summary>
            EmptyQueue,

            /// <summary>The daily cap is 0: automatic runs annotate nothing.</summary>
            NoDailyCases,
        }

        /// <summary>What the rule reads.</summary>
        public sealed record Inputs
        {
            /// <summary>Who would start the run.</summary>
            public TriageTrigger Trigger { get; init; } = TriageTrigger.Automatic;

            /// <summary>The autoTriage preference.</summary>
            public bool Enabled { get; init; } = true;

            /// <summary>The assistant is shown, Claude Code found and the bridge is beside the application.</summary>
            public bool Available { get; init; } = true;

            /// <summary>Whether Claude Code is signed in; null when not known (which does not hold a run back: the run asks again).</summary>
            public bool? SignedIn { get; init; } = true;

            /// <summary>Both consents and the board's assistant preference.</summary>
            public bool Consent { get; init; } = true;

            /// <summary>A run is under way (this application's, or one the daemon reports open).</summary>
            public bool Running { get; init; }

            /// <summary>Cases <c>board.queue</c> would offer.</summary>
            public int Queue { get; init; } = 1;

            /// <summary>Cases automatic runs annotated on the day of <see cref="CountedAt"/>.</summary>
            public int AnnotatedToday { get; init; }

            /// <summary>When <see cref="AnnotatedToday"/> was reported; on an earlier day it counts as 0. Null: as reported today.</summary>
            public DateTimeOffset? CountedAt { get; init; }

            /// <summary><c>autoTriageDailyCases</c>; 0 = none.</summary>
            public int DailyCap { get; init; } = BoardLimits.DefaultBoardAutoTriageDailyCases;

            /// <summary><c>autoTriageMinutes</c>.</summary>
            public int Minutes { get; init; } = BoardLimits.DefaultBoardAutoTriageMinutes;

            /// <summary>When the last automatic run started (or was tried); null before the first.</summary>
            public DateTimeOffset? LastAttempt { get; init; }

            /// <summary>Automatic runs that failed in a row since the last success or manual run.</summary>
            public int Failures { get; init; }

            /// <summary>Now.</summary>
            public required DateTimeOffset Now { get; init; }
        }

        /// <summary>What the rule decided.</summary>
        public abstract record Decision
        {
            // Only the cases below derive from it.
            private Decision()
            {
            }

            /// <summary>Start a run of at most <paramref name="Limit"/> cases.</summary>
            /// <param name="Limit">The most cases.</param>
            public sealed record Run(int Limit) : Decision;

            /// <summary>Decide again at <paramref name="Until"/> (the interval, the back-off, the next day for a cap used up).</summary>
            /// <param name="Until">When.</param>
            public sealed record Wait(DateTimeOffset Until) : Decision;

            /// <summary>Nothing runs, and nothing is waited for.</summary>
            /// <param name="Reason">Why.</param>
            public sealed record Off(OffReason Reason) : Decision;
        }

        /// <summary>
        /// The interval after <paramref name="failures"/> failed automatic runs
        /// in a row: <paramref name="minutes"/> (within the daemon's range),
        /// doubled per failure, at most <see cref="MaxInterval"/>.
        /// </summary>
        public static TimeSpan Interval(int minutes, int failures)
        {
            var m = Math.Clamp(minutes, BoardLimits.MinBoardAutoTriageMinutes, BoardLimits.MaxBoardAutoTriageMinutes);
            var d = TimeSpan.FromMinutes(m);
            for (var k = 0; k < Math.Max(0, failures); k++)
            {
                d *= 2;
                if (d >= MaxInterval)
                {
                    return MaxInterval;
                }
            }
            return d < MaxInterval ? d : MaxInterval;
        }

        /// <summary>The rule (see the top of the file); days are <paramref name="zone"/>'s (the local zone when null).</summary>
        public static Decision Decide(Inputs i, TimeZoneInfo? zone = null)
        {
            ArgumentNullException.ThrowIfNull(i);
            zone ??= TimeZoneInfo.Local;
            if (i.Trigger == TriageTrigger.Manual)
            {
                if (!i.Available)
                {
                    return new Decision.Off(OffReason.Unavailable);
                }
                if (i.SignedIn == false)
                {
                    return new Decision.Off(OffReason.SignedOut);
                }
                if (i.Running)
                {
                    return new Decision.Off(OffReason.Running);
                }
                return new Decision.Run(Assistant.TriageBatch);
            }
            if (!i.Enabled)
            {
                return new Decision.Off(OffReason.SwitchedOff);
            }
            if (!i.Available)
            {
                return new Decision.Off(OffReason.Unavailable);
            }
            if (i.SignedIn == false)
            {
                return new Decision.Off(OffReason.SignedOut);
            }
            if (!i.Consent)
            {
                return new Decision.Off(OffReason.NoConsent);
            }
            if (i.Running)
            {
                return new Decision.Off(OffReason.Running);
            }
            if (i.Queue <= 0)
            {
                return new Decision.Off(OffReason.EmptyQueue);
            }
            if (i.DailyCap <= 0)
            {
                return new Decision.Off(OffReason.NoDailyCases);
            }
            var counted = i.CountedAt is not { } at || SameDay(at, i.Now, zone);
            var remaining = i.DailyCap - (counted ? Math.Max(0, i.AnnotatedToday) : 0);
            if (remaining <= 0)
            {
                var tomorrow = TimeZoneInfo.ConvertTime(i.Now, zone).Date.AddDays(1);
                return new Decision.Wait(new DateTimeOffset(tomorrow, zone.GetUtcOffset(tomorrow)));
            }
            if (i.LastAttempt is { } last)
            {
                var earliest = last + Interval(i.Minutes, i.Failures);
                if (i.Now < earliest)
                {
                    return new Decision.Wait(earliest);
                }
            }
            return new Decision.Run(Math.Min(Assistant.TriageBatch, remaining));
        }

        /// <summary>
        /// Whether an automatic run that failed with <paramref name="f"/> makes
        /// the next one wait longer. A cancelled run, an empty queue, a
        /// declined consent or the assistant off are no failures; a run that
        /// added no note, or whose notes were all refused, is one, so that it
        /// does not repeat at every interval.
        /// </summary>
        public static bool CountsAsFailure(TriageFailure f) =>
            f is not (TriageFailure.Cancelled or TriageFailure.Declined or TriageFailure.AssistantOff or TriageFailure.NothingToDo);

        private static bool SameDay(DateTimeOffset a, DateTimeOffset b, TimeZoneInfo zone) =>
            TimeZoneInfo.ConvertTime(a, zone).Date == TimeZoneInfo.ConvertTime(b, zone).Date;
    }
}
