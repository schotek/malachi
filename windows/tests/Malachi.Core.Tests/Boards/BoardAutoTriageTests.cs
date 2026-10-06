// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the rule's half of macos/Tests/MalachiCoreTests/
// BoardAutoTriageTests.swift (decideTable, interval, and the
// countsAsFailure checks of backOff); Go: ui/internal/boardtriage
// auto_test.go. The schedule's tests (debounce, preferencesDecideAtOnce,
// intervalBetweenRuns, backOff, dailyCap, signedOut,
// lastAttemptFromTheDaemon, stop) are in
// Controllers/BoardAutoTriageSchedulerTests.cs.

using System;
using Malachi.Core.Boards;
using Xunit;
using static Malachi.Core.Boards.Board;

namespace Malachi.Core.Tests.Boards;

public sealed class BoardAutoTriageTests
{
    /// <summary>2026-10-01 10:00:00 UTC.</summary>
    private static readonly DateTimeOffset T0 = DateTimeOffset.FromUnixTimeSeconds(1_790_848_800);

    private static AutoTriage.Inputs I => new() { Now = T0 };

    private static TimeSpan Minutes(double m) => TimeSpan.FromMinutes(m);

    [Fact]
    public void DecideTable()
    {
        var next = T0.AddHours(14); // 2026-10-02 00:00 UTC
        (string Name, AutoTriage.Inputs I, AutoTriage.Decision Want)[] rows =
        [
            ("all set, first run", I, new AutoTriage.Decision.Run(40)),
            ("switched off", I with { Enabled = false }, new AutoTriage.Decision.Off(AutoTriage.OffReason.SwitchedOff)),
            ("assistant cannot run", I with { Available = false }, new AutoTriage.Decision.Off(AutoTriage.OffReason.Unavailable)),
            ("signed out", I with { SignedIn = false }, new AutoTriage.Decision.Off(AutoTriage.OffReason.SignedOut)),
            ("sign-in not known counts as signed in", I with { SignedIn = null }, new AutoTriage.Decision.Run(40)),
            ("no consent", I with { Consent = false }, new AutoTriage.Decision.Off(AutoTriage.OffReason.NoConsent)),
            ("a run under way", I with { Running = true }, new AutoTriage.Decision.Off(AutoTriage.OffReason.Running)),
            ("empty queue", I with { Queue = 0 }, new AutoTriage.Decision.Off(AutoTriage.OffReason.EmptyQueue)),
            ("no daily cases", I with { DailyCap = 0 }, new AutoTriage.Decision.Off(AutoTriage.OffReason.NoDailyCases)),
            ("the cap is the limit", I with { AnnotatedToday = 50, DailyCap = 60 }, new AutoTriage.Decision.Run(10)),
            ("the batch is the limit", I with { AnnotatedToday = 0, DailyCap = 1000 }, new AutoTriage.Decision.Run(40)),
            ("cap used up: tomorrow", I with { AnnotatedToday = 60, DailyCap = 60 }, new AutoTriage.Decision.Wait(next)),
            ("cap counted yesterday is 0", I with { AnnotatedToday = 60, CountedAt = T0.AddDays(-1), DailyCap = 60 },
                new AutoTriage.Decision.Run(40)),
            ("interval not over", I with { Minutes = 30, LastAttempt = T0 - Minutes(10) }, new AutoTriage.Decision.Wait(T0 + Minutes(20))),
            ("interval over", I with { Minutes = 30, LastAttempt = T0 - Minutes(30) }, new AutoTriage.Decision.Run(40)),
            ("one failure doubles it", I with { Minutes = 30, LastAttempt = T0 - Minutes(30), Failures = 1 },
                new AutoTriage.Decision.Wait(T0 + Minutes(30))),
            ("three failures: 4 hours", I with { Minutes = 30, LastAttempt = T0, Failures = 3 }, new AutoTriage.Decision.Wait(T0 + Minutes(240))),
            ("the back-off stops at a day", I with { Minutes = 600, LastAttempt = T0, Failures = 5 },
                new AutoTriage.Decision.Wait(T0.AddDays(1))),
            ("the switch before everything", I with { Enabled = false, Available = false, SignedIn = false },
                new AutoTriage.Decision.Off(AutoTriage.OffReason.SwitchedOff)),
            ("the cap before the interval", I with { AnnotatedToday = 60, DailyCap = 60, LastAttempt = T0 }, new AutoTriage.Decision.Wait(next)),
            ("manual ignores the switch, interval, cap, back-off and queue",
                I with
                {
                    Trigger = TriageTrigger.Manual, Enabled = false, Queue = 0, AnnotatedToday = 60, DailyCap = 60, LastAttempt = T0,
                    Failures = 4,
                },
                new AutoTriage.Decision.Run(40)),
            ("manual without consent runs (it asks)", I with { Trigger = TriageTrigger.Manual, Consent = false }, new AutoTriage.Decision.Run(40)),
            ("manual: not while one runs", I with { Trigger = TriageTrigger.Manual, Running = true },
                new AutoTriage.Decision.Off(AutoTriage.OffReason.Running)),
            ("manual: not signed out", I with { Trigger = TriageTrigger.Manual, SignedIn = false },
                new AutoTriage.Decision.Off(AutoTriage.OffReason.SignedOut)),
            ("manual: not unavailable", I with { Trigger = TriageTrigger.Manual, Available = false },
                new AutoTriage.Decision.Off(AutoTriage.OffReason.Unavailable)),
        ];
        foreach (var (name, i, want) in rows)
        {
            Assert.True(AutoTriage.Decide(i, TimeZoneInfo.Utc) == want, $"{name}: {AutoTriage.Decide(i, TimeZoneInfo.Utc)}");
        }
    }

    /// <summary>Go's sameDay: the day is the zone's (a count reported late yesterday in Prague is still today's in UTC).</summary>
    [Fact]
    public void DaysAreTheZones()
    {
        var prague = TimeZoneInfo.FindSystemTimeZoneById("Europe/Prague");
        // 2026-10-01 23:30 in Prague is 21:30 UTC; now 2026-10-02 00:10 Prague.
        var counted = new DateTimeOffset(2026, 10, 1, 21, 30, 0, TimeSpan.Zero);
        var now = new DateTimeOffset(2026, 10, 1, 22, 10, 0, TimeSpan.Zero);
        var i = new AutoTriage.Inputs { AnnotatedToday = 60, CountedAt = counted, DailyCap = 60, Now = now };
        Assert.Equal(new AutoTriage.Decision.Run(40), AutoTriage.Decide(i, prague));
        Assert.IsType<AutoTriage.Decision.Wait>(AutoTriage.Decide(i, TimeZoneInfo.Utc));
        // Used up today: the next try at the zone's midnight.
        var wait = Assert.IsType<AutoTriage.Decision.Wait>(AutoTriage.Decide(i with { CountedAt = now }, prague));
        Assert.Equal(new DateTimeOffset(2026, 10, 3, 0, 0, 0, TimeSpan.FromHours(2)), wait.Until);
    }

    [Fact]
    public void Interval()
    {
        Assert.Equal(TimeSpan.FromSeconds(1800), AutoTriage.Interval(30, 0));
        Assert.Equal(TimeSpan.FromSeconds(3600), AutoTriage.Interval(30, 1));
        Assert.Equal(TimeSpan.FromSeconds(7200), AutoTriage.Interval(30, 2));
        Assert.Equal(TimeSpan.FromSeconds(86400), AutoTriage.Interval(30, 100));
        Assert.Equal(TimeSpan.FromSeconds(86400), AutoTriage.Interval(1440, 0));
        // Out of range minutes are taken as the nearest allowed.
        Assert.Equal(TimeSpan.FromSeconds(300), AutoTriage.Interval(1, 0));
        Assert.Equal(TimeSpan.FromSeconds(86400), AutoTriage.Interval(99999, 0));
        Assert.Equal(TimeSpan.FromSeconds(1800), AutoTriage.Interval(30, -3));
    }

    /// <summary>Which failed automatic runs make the next one wait longer (Swift backOff's countsAsFailure checks, Go TestCountsAsFailure).</summary>
    [Fact]
    public void CountsAsFailure()
    {
        // A cancelled run is no failure, nor are an empty queue, a declined
        // consent or the assistant off.
        Assert.False(AutoTriage.CountsAsFailure(TriageFailure.Cancelled));
        Assert.False(AutoTriage.CountsAsFailure(TriageFailure.NothingToDo));
        Assert.False(AutoTriage.CountsAsFailure(TriageFailure.AssistantOff));
        Assert.False(AutoTriage.CountsAsFailure(TriageFailure.Declined));
        Assert.True(AutoTriage.CountsAsFailure(TriageFailure.NotSignedIn));
        Assert.True(AutoTriage.CountsAsFailure(TriageFailure.Backend));
        // An automatic run that added no note, or whose notes were all
        // refused, is one: it must not repeat at every interval.
        Assert.True(AutoTriage.CountsAsFailure(TriageFailure.NoProgress));
        Assert.True(AutoTriage.CountsAsFailure(TriageFailure.NotesRefused));
        Assert.True(AutoTriage.CountsAsFailure(TriageFailure.Timeout));
        Assert.True(AutoTriage.CountsAsFailure(TriageFailure.Stopped));
        Assert.True(AutoTriage.CountsAsFailure(TriageFailure.NotFound));
    }
}
