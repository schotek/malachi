// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/BoardRemindTests.swift; GTK:
// ui/internal/board/remind_test.go (TestRemindAtNoon, TestLaterTodayRule,
// TestRemindPresetsThroughTheDay, TestNextWeekIsTheComingMonday,
// TestRemindAlwaysInTheFuture, TestRemindDaylightSavingTime). Remind…
// presets: Later Today, This Evening, Tomorrow (or This Morning) at 9, next
// Monday at 9. UTC and a fixed date; the 15th of October 2026 is a
// Thursday.

using System;
using System.Collections.Generic;
using System.Linq;
using Xunit;
using static Malachi.Core.Boards.Board;
using F = Malachi.Core.Tests.Boards.BoardFixture;

namespace Malachi.Core.Tests.Boards;

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
        Assert.Equal(["Thu at 15:00", "Fri at 09:00", "Mon at 09:00"], p.Select(x => x.When));
        Assert.Equal("Later Today, Thu at 15:00", p[0].Label);
    }

    public static TheoryData<DateTimeOffset, DateTimeOffset?> LaterTodayCases => new()
    {
        { F.Day(15, 0, 30), null }, // not before dawn
        { F.Day(15, 1, 10), null },
        { F.Day(15, 4, 59), null },
        { F.Day(15, 5), F.Day(15, 8) },
        { F.Day(15, 8), F.Day(15, 11) },
        { F.Day(15, 12, 20), F.Day(15, 16) },
        { F.Day(15, 14, 59), F.Day(15, 18) },
        { F.Day(15, 16, 59), F.Day(15, 20) },
        { F.Day(15, 17), F.Day(15, 20) }, // exactly 20:00
        { F.Day(15, 17, 1), null }, // 21:00 is too late
        { F.Day(15, 18, 59), null },
        { F.Day(15, 19), null },
        { F.Day(15, 20, 59), null },
        { F.Day(15, 23, 30), null },
    };

    /// <summary>Three hours ahead, rounded up to the hour, offered only up to 20:00 the same day, never from 19:00 and not before 05:00.</summary>
    [Theory]
    [MemberData(nameof(LaterTodayCases))]
    public void LaterTodayRule(DateTimeOffset now, DateTimeOffset? want) => Assert.Equal(want, LaterToday(now));

    /// <summary>The whole menu at the times of the day where the rule changes.</summary>
    [Fact]
    public void PresetsThroughTheDay()
    {
        const RemindPresetKind Later = RemindPresetKind.LaterToday;
        const RemindPresetKind Evening = RemindPresetKind.ThisEvening;
        const RemindPresetKind Morning = RemindPresetKind.ThisMorning;
        const RemindPresetKind Tomorrow = RemindPresetKind.Tomorrow;
        const RemindPresetKind Week = RemindPresetKind.NextWeek;
        var cases = new (DateTimeOffset Now, (RemindPresetKind Kind, DateTimeOffset Date)[] Want)[]
        {
            (F.Day(15, 0, 30), [(Morning, F.Day(15, 9)), (Week, F.Day(19, 9))]),
            (F.Day(15, 1, 10), [(Morning, F.Day(15, 9)), (Week, F.Day(19, 9))]),
            (F.Day(15, 4, 59), [(Morning, F.Day(15, 9)), (Week, F.Day(19, 9))]),
            (F.Day(15, 5), [(Later, F.Day(15, 8)), (Tomorrow, F.Day(16, 9)), (Week, F.Day(19, 9))]),
            (F.Day(15, 12), [(Later, F.Day(15, 15)), (Tomorrow, F.Day(16, 9)), (Week, F.Day(19, 9))]),
            (F.Day(15, 16, 59), [(Later, F.Day(15, 20)), (Tomorrow, F.Day(16, 9)), (Week, F.Day(19, 9))]),
            // This Evening is Later Today's time: offered once, as Later Today.
            (F.Day(15, 17), [(Later, F.Day(15, 20)), (Tomorrow, F.Day(16, 9)), (Week, F.Day(19, 9))]),
            (F.Day(15, 18, 59), [(Evening, F.Day(15, 20)), (Tomorrow, F.Day(16, 9)), (Week, F.Day(19, 9))]),
            (F.Day(15, 19), [(Tomorrow, F.Day(16, 9)), (Week, F.Day(19, 9))]),
            (F.Day(15, 20), [(Tomorrow, F.Day(16, 9)), (Week, F.Day(19, 9))]),
            (F.Day(15, 23, 30), [(Tomorrow, F.Day(16, 9)), (Week, F.Day(19, 9))]),
        };
        foreach (var (now, want) in cases)
        {
            Assert.Equal(want, Presets(now).Select(p => (p.Kind, p.Date)));
        }
        Assert.Equal("This Evening", Presets(F.Day(15, 18)).First(p => p.Kind == Evening).Title);
        Assert.Equal("This Morning", Presets(F.Day(15, 1)).First(p => p.Kind == Morning).Title);
    }

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
        Assert.Equal(["Sat at 15:00", "Sun at 09:00", "Mon at 09:00"], p.Select(x => x.When));
        // On the night of the change, before 05:00: This Morning only.
        p = P(At(10, 25, 1, 30));
        Assert.True(p.Count == 2 && p[0].Kind == RemindPresetKind.ThisMorning && p[0].Date == At(10, 25, 9));
        // Later Today counts real hours once it is dawn.
        p = P(At(10, 25, 5, 30));
        Assert.True(p[0].Kind == RemindPresetKind.LaterToday && p[0].Date == At(10, 25, 9));
        // Saturday before the spring change.
        p = P(At(3, 28, 16));
        Assert.Equal([At(3, 28, 19), At(3, 29, 9), At(3, 30, 9)], p.Select(x => x.Date));
        Assert.Equal(["Sat at 19:00", "Sun at 09:00", "Mon at 09:00"], p.Select(x => x.When));
        // The night of the spring change, before 05:00: This Morning is 09:00
        // summer time the same day.
        p = P(At(3, 29, 1, 30));
        Assert.True(p.Count == 2 && p[0].Kind == RemindPresetKind.ThisMorning && p[0].Date == At(3, 29, 9));
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
