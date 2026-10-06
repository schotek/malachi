// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/BoardRemindTests.swift; GTK:
// ui/internal/board/remind_test.go (TestRemindAtNoon, TestLaterTodayRule,
// TestNextWeekIsTheComingMonday, TestRemindAlwaysInTheFuture,
// TestRemindDaylightSavingTime). Remind… presets: Later Today, Tomorrow at
// 9, next Monday at 9. UTC and a fixed date; the 15th of October 2026 is a
// Thursday.

using System;
using System.Collections.Generic;
using System.Linq;
using Xunit;
using static Malachi.Core.Board.Board;
using F = Malachi.Core.Tests.Board.BoardFixture;

namespace Malachi.Core.Tests.Board;

public sealed class BoardRemindTests
{
    private static IReadOnlyList<RemindPreset> Presets(DateTimeOffset now) => RemindPresets(now, F.Culture, F.Zone);

    private static DateTimeOffset? LaterToday(DateTimeOffset now) =>
        Presets(now).FirstOrDefault(p => p.Kind == RemindPresetKind.LaterToday)?.Date;

    [Fact]
    public void AtNoon()
    {
        var p = Presets(F.Day(15, 12));
        Assert.Equal([RemindPresetKind.LaterToday, RemindPresetKind.Tomorrow, RemindPresetKind.NextWeek], p.Select(x => x.Kind));
        Assert.Equal([F.Day(15, 15), F.Day(16, 9), F.Day(19, 9)], p.Select(x => x.Date));
        Assert.Equal(["Later Today", "Tomorrow", "Next Week"], p.Select(x => x.Title));
        Assert.Equal(["Thu 15:00", "Fri 09:00", "Mon 09:00"], p.Select(x => x.When));
    }

    public static TheoryData<DateTimeOffset, DateTimeOffset?> LaterTodayCases => new()
    {
        { F.Day(15, 8), F.Day(15, 11) },
        { F.Day(15, 12, 20), F.Day(15, 16) },
        { F.Day(15, 14, 59), F.Day(15, 18) },
        { F.Day(15, 15, 30), F.Day(15, 18) }, // 19:00 rounded, 18:00 first
        { F.Day(15, 16, 30), F.Day(15, 18) },
        { F.Day(15, 17), F.Day(15, 18) }, // exactly an hour away
        { F.Day(15, 17, 30), F.Day(15, 21) }, // 18:00 too close
        { F.Day(15, 19, 30), F.Day(15, 23) },
        { F.Day(15, 20, 59), null }, // 23:59 rounds up to midnight: tomorrow
        { F.Day(15, 21), null },
        { F.Day(15, 23, 30), null },
    };

    /// <summary>Three hours ahead, rounded up to the hour; 18:00 when that is earlier and still an hour away; none past the day.</summary>
    [Theory]
    [MemberData(nameof(LaterTodayCases))]
    public void LaterTodayRule(DateTimeOffset now, DateTimeOffset? want) => Assert.Equal(want, LaterToday(now));

    [Fact]
    public void NextWeekIsTheComingMonday()
    {
        // Sunday the 18th: Monday is tomorrow, offered once, as Tomorrow.
        var sunday = Presets(F.Day(18, 10));
        Assert.Equal([RemindPresetKind.LaterToday, RemindPresetKind.Tomorrow], sunday.Select(x => x.Kind));
        Assert.Equal(F.Day(19, 9), sunday.First(x => x.Kind == RemindPresetKind.Tomorrow).Date);
        // Monday the 19th: a week ahead.
        Assert.Equal(F.Day(26, 9), Presets(F.Day(19, 8)).First(x => x.Kind == RemindPresetKind.NextWeek).Date);
        // Saturday the 17th.
        Assert.Equal(F.Day(19, 9), Presets(F.Day(17, 23)).First(x => x.Kind == RemindPresetKind.NextWeek).Date);
    }

    /// <summary>Every preset lies after now and within the daemon's year.</summary>
    [Fact]
    public void AlwaysInTheFuture()
    {
        for (var t = F.Day(12, 0); t < F.Day(20, 0); t = t.AddMinutes(17))
        {
            foreach (var p in Presets(t))
            {
                Assert.True(p.Date > t && p.Date - t < TimeSpan.FromDays(365), $"{p.Kind} at {t:O}");
            }
        }
    }

    /// <summary>
    /// Across a change of daylight saving time the presets keep their wall
    /// clock time (Prague: summer time ends on Sunday 25 October 2026 and
    /// starts on Sunday 29 March 2026).
    /// </summary>
    [Fact]
    public void DaylightSavingTime()
    {
        var prague = TimeZoneInfo.FindSystemTimeZoneById("Europe/Prague");
        DateTimeOffset At(int m, int d, int h, int min = 0)
        {
            var wall = new DateTime(2026, m, d, h, min, 0, DateTimeKind.Unspecified);
            return new DateTimeOffset(wall, prague.GetUtcOffset(wall));
        }
        IReadOnlyList<RemindPreset> P(DateTimeOffset now) => RemindPresets(now, F.Culture, prague);
        // Saturday before the autumn change: Tomorrow is Sunday 09:00, Next
        // Week Monday 09:00, both in winter time.
        var p = P(At(10, 24, 12));
        Assert.Equal([At(10, 24, 15), At(10, 25, 9), At(10, 26, 9)], p.Select(x => x.Date));
        Assert.Equal(["Sat 15:00", "Sun 09:00", "Mon 09:00"], p.Select(x => x.When));
        // On the night of the change: Later Today counts real hours.
        p = P(At(10, 25, 1, 30));
        Assert.True(p[0].Kind == RemindPresetKind.LaterToday && p[0].Date == At(10, 25, 4));
        // Saturday before the spring change.
        p = P(At(3, 28, 20));
        Assert.Equal([At(3, 28, 23), At(3, 29, 9), At(3, 30, 9)], p.Select(x => x.Date));
        Assert.Equal(["Sat 23:00", "Sun 09:00", "Mon 09:00"], p.Select(x => x.When));
    }

    [Fact]
    public void ADaySkippedByTheClockStillHasItsPresets()
    {
        // A zone whose 09:00 falls in the gap: the first time after it.
        var rule = TimeZoneInfo.AdjustmentRule.CreateAdjustmentRule(
            DateTime.MinValue.Date, DateTime.MaxValue.Date, TimeSpan.FromHours(1),
            TimeZoneInfo.TransitionTime.CreateFixedDateRule(new DateTime(1, 1, 1, 9, 0, 0), 10, 16),
            TimeZoneInfo.TransitionTime.CreateFixedDateRule(new DateTime(1, 1, 1, 9, 0, 0), 11, 20));
        var zone = TimeZoneInfo.CreateCustomTimeZone("Test/Gap", TimeSpan.Zero, "Gap", "Gap", "Gap+1", [rule]);
        var p = RemindPresets(new DateTimeOffset(2026, 10, 15, 12, 0, 0, TimeSpan.Zero), F.Culture, zone);
        var tomorrow = p.First(x => x.Kind == RemindPresetKind.Tomorrow).Date;
        Assert.Equal(new DateTimeOffset(2026, 10, 16, 10, 0, 0, TimeSpan.FromHours(1)), tomorrow);
    }
}
