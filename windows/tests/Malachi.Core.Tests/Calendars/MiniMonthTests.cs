// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CalendarMonthLayoutTests.swift
// (miniMonth); GTK: none (the sidebar's month is built from month.go's
// MonthCells, whose tests are CalendarMonthLayoutTests).

using System.Linq;
using Malachi.Core.Calendars;
using Xunit;
using static Malachi.Core.Calendars.Cal;
using F = Malachi.Core.Tests.Calendars.CalFixture;

namespace Malachi.Core.Tests.Calendars;

public sealed class MiniMonthTests
{
    /// <summary>The sidebar month: the grid of MonthCells, today, and the band of the shown week with its ends.</summary>
    [Fact]
    public void MiniMonthOfTheShownWeek()
    {
        var p = F.Prague;
        var env = F.Env();
        var now = F.At(2026, 10, 16, 12);
        var week = ViewRange(View.Week, now, Weekday.Monday, p);
        var m = new MiniMonth(now, Weekday.Monday, now, week, env);
        Assert.Equal("October 2026", m.Title);
        Assert.Equal(F.At(2026, 10, 1), m.Month);
        Assert.Equal("Mon", m.Headers[0]);
        Assert.Equal(42, m.Days.Count);
        Assert.Equal([40, 41, 42, 43, 44, 45], m.WeekNumbers);
        Assert.Equal([18], Enumerable.Range(0, m.Days.Count).Where(i => m.Days[i].Today));
        Assert.Equal(Enumerable.Range(14, 7), Enumerable.Range(0, m.Days.Count).Where(i => m.Days[i].Shown));
        Assert.True(m.Days[14].BandStart && !m.Days[14].BandEnd && m.Days[20].BandEnd && !m.Days[15].BandStart);
        Assert.True(m.Days[0].Number == "28" && !m.Days[0].InMonth && m.Days[3].Number == "1" && m.Days[3].InMonth);
        Assert.Equal(18, m.IndexOf(F.At(2026, 10, 16, 23, 59), p));
        Assert.Null(m.IndexOf(F.At(2027, 1, 1), p));
        Assert.True(m.Days[5].Weekend && m.Days[6].Weekend && !m.Days[4].Weekend);

        // A range across rows is banded per row; no range bands nothing.
        var agenda = ViewRange(View.Agenda, F.At(2026, 10, 30), Weekday.Monday, p);
        var a = new MiniMonth(now, Weekday.Monday, now, agenda, env);
        Assert.True(a.Days[32].Shown && a.Days[32].BandStart && a.Days[34].BandEnd && a.Days[35].BandStart);
        Assert.False(a.Days[31].Shown);
        Assert.All(new MiniMonth(now, Weekday.Monday, now, null, env).Days, d => Assert.False(d.Shown));
        Assert.Equal(F.At(2027, 1, 1), MiniMonth.Step(F.At(2026, 12, 31), 1, p));
        Assert.Equal(F.At(2026, 2, 1), MiniMonth.Step(F.At(2026, 3, 31), -1, p));

        // Equal when they show the same.
        Assert.Equal(m, new MiniMonth(now.AddHours(2), Weekday.Monday, now, week, env));
        Assert.NotEqual(m, a);
    }
}
