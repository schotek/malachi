// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CalendarMonthLayoutTests.swift, the
// grid's cases (monthCells, weekdayHeaders); GTK:
// ui/internal/calendar/month_test.go (TestMonthCells, TestWeekdayHeaders).
// The class is partial: the rest of month_test.go (TestCellCapacity,
// TestCellItems, TestMonthLanes) goes into CalendarMonthLayoutTests.cs
// with the port of CalendarMonthLayout.cs.

using System;
using System.Linq;
using Malachi.Core.Calendars;
using Xunit;
using static Malachi.Core.Calendars.Cal;
using F = Malachi.Core.Tests.Calendars.CalFixture;

namespace Malachi.Core.Tests.Calendars;

public sealed partial class CalendarMonthLayoutTests
{
    [Theory]
    // 2026: the 1st falls on every weekday in one of these months.
    [InlineData(Weekday.Sunday, 2)]
    [InlineData(Weekday.Monday, 6)]
    [InlineData(Weekday.Tuesday, 9)]
    [InlineData(Weekday.Wednesday, 4)]
    [InlineData(Weekday.Thursday, 10)]
    [InlineData(Weekday.Friday, 5)]
    [InlineData(Weekday.Saturday, 8)]
    public void MonthCellsOfEveryFirstWeekday(Weekday wd, int m)
    {
        var p = F.Prague;
        var now = F.At(2026, 10, 16, 12);
        foreach (var ws in new[] { Weekday.Monday, Weekday.Sunday })
        {
            var g = MonthCells(F.At(2026, m, 15), ws, now, p);
            var first = F.At(2026, m, 1);
            Assert.Equal(wd, PartsOf(first, p).Weekday);
            var lead = ((int)wd - (int)ws + 7) % 7;
            Assert.Equal(ws, PartsOf(g.Cells[0].Day, p).Weekday);
            Assert.Equal(first, g.Cells[lead].Day);
            Assert.Equal(first, g.Month);
            var days = DateTime.DaysInMonth(2026, m);
            Assert.Equal(42, g.Cells.Count);
            Assert.Equal(6, g.WeekNumbers.Count);
            for (var i = 0; i < g.Cells.Count; i++)
            {
                var c = g.Cells[i];
                if (i > 0)
                {
                    Assert.Equal(1, DaysBetween(g.Cells[i - 1].Day, c.Day, p));
                }
                var cp = PartsOf(c.Day, p);
                Assert.Equal(cp.Weekday is Weekday.Saturday or Weekday.Sunday, c.Weekend);
                Assert.Equal(0, cp.Hour);
            }
            Assert.Equal(days, g.Cells.Count(c => c.InMonth));
            for (var r = 0; r < 6; r++)
            {
                Assert.Equal(WeekNumber(g.Cells[r * 7].Day, ws, p), g.WeekNumbers[r]);
            }
        }
    }

    [Fact]
    public void MonthCellsOfOctober()
    {
        var p = F.Prague;
        var now = F.At(2026, 10, 16, 12);
        var g = MonthCells(now, Weekday.Monday, now, p);
        Assert.Equal([18], Enumerable.Range(0, g.Cells.Count).Where(i => g.Cells[i].Today));
        Assert.Equal([40, 41, 42, 43, 44, 45], g.WeekNumbers);
        var gs = MonthCells(now, Weekday.Sunday, now, p);
        Assert.Equal([40, 41, 42, 43, 44, 45], gs.WeekNumbers);
        Assert.Equal(27, PartsOf(gs.Cells[0].Day, p).Day);
        // The 25-hour day is one cell, and the next starts at midnight.
        Assert.Equal(F.At(2026, 10, 26), g.Cells[28].Day);
        Assert.Equal(g, MonthCells(now, Weekday.Monday, now, p));
    }

    [Fact]
    public void WeekdayHeaders()
    {
        var env = F.Env(F.Utc);
        Assert.Equal(["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"], Cal.WeekdayHeaders(Weekday.Monday, env));
        Assert.Equal(["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"], Cal.WeekdayHeaders(Weekday.Sunday, env));
        var sat = Cal.WeekdayHeaders(Weekday.Saturday, env);
        Assert.Equal("Sat", sat[0]);
        Assert.Equal("Fri", sat[6]);
        // A zone far east or west writes the same days.
        Assert.Equal(["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"], Cal.WeekdayHeaders(Weekday.Monday, F.Env(F.Prague)));
    }
}
