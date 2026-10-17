// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CalendarMonthLayoutTests.swift, all
// but the grid (cellCapacity, cellItems, monthLanes); GTK:
// ui/internal/calendar/month_test.go (TestCellCapacity, TestCellItems,
// TestMonthLanes). The grid's cases are CalendarMonthLayoutTests.Grid.cs,
// the sidebar month's MiniMonthTests.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Xunit;
using static Malachi.Core.Calendars.Cal;
using F = Malachi.Core.Tests.Calendars.CalFixture;

namespace Malachi.Core.Tests.Calendars;

public sealed partial class CalendarMonthLayoutTests
{
    [Theory]
    [InlineData(100, 20, 5, 0, 0)]
    [InlineData(99.9, 20, 4, 0, 0)]
    [InlineData(19, 20, 0, 0, 0)]
    [InlineData(0, 20, 0, 0, 0)]
    [InlineData(50, 0, 0, 0, 0)]
    [InlineData(-5, 10, 0, 0, 0)]
    [InlineData(80, 16, 5, 0, 16)]
    [InlineData(100, 20, 3, 2, 18)]
    [InlineData(100, 20, 0, 6, 18)]
    [InlineData(100, 20, 5, -1, 18)]
    // Swift's guard: no room for a NaN or a negative lane height.
    [InlineData(double.NaN, 20, 0, 0, 0)]
    [InlineData(100, double.NaN, 0, 0, 0)]
    [InlineData(100, 20, 5, 2, -18)]
    public void CellCapacityCases(double h, double line, int want, int lanes, double laneH) =>
        Assert.Equal(want, CellCapacity(h, line, lanes, laneH));

    [Fact]
    public void CellCapacityDefaultsToNoLanes() => Assert.Equal(5, CellCapacity(100, 20));

    [Theory]
    [InlineData(10, "t1b t1 t2", 0)]
    [InlineData(3, "t1b t1 t2", 0)]
    [InlineData(2, "t1b", 2)]
    [InlineData(1, "", 3)]
    [InlineData(0, "", 3)]
    public void CellItemsCases(int capacity, string want, int more)
    {
        var day = F.At(2026, 10, 14);
        Occurrence[] occ =
        [
            F.Timed("t2", F.At(2026, 10, 14, 14), F.At(2026, 10, 14, 15)),
            F.Timed("t1", F.At(2026, 10, 14, 9), F.At(2026, 10, 14, 10)),
            F.Timed("t1b", F.At(2026, 10, 14, 9), F.At(2026, 10, 14, 9, 30)),
            F.AllDayOcc("one", day, 1),
            F.AllDayOcc("long", F.At(2026, 10, 13), 3),
            F.Timed("night", F.At(2026, 10, 13, 22), F.At(2026, 10, 14, 1)),
            F.Timed("other", F.At(2026, 10, 15, 9), F.At(2026, 10, 15, 10)),
        ];
        var (pills, m) = CellItems(occ, day, capacity, F.Prague, false);
        Assert.Equal(want, string.Join(" ", pills.Select(p => p.Occurrence.EventId?.Value)));
        Assert.Equal(more, m);
    }

    [Fact]
    public void MoreText() => Assert.Equal("+2 more", More(2));

    [Fact]
    public void MonthLanesOfTwoRows()
    {
        var p = F.Prague;
        var g = MonthCells(F.At(2026, 10, 1), Weekday.Monday, F.At(2026, 10, 16), p);
        DateTimeOffset[] Row(int r) => [.. Enumerable.Range(0, 7).Select(i => g.Cells[r * 7 + i].Day)];
        var two = F.AllDayOcc("two", F.At(2026, 10, 7), 2); // Wed–Thu of row 1
        var ten = F.AllDayOcc("ten", F.At(2026, 10, 9), 10); // Fri 9 – Sun 18: rows 1 and 2
        var other = F.AllDayOcc("other", F.At(2026, 10, 8), 3); // Thu–Sat, overlaps both
        var night = F.Timed("night", F.At(2026, 10, 13, 22), F.At(2026, 10, 14, 2));
        var inDay = F.Timed("day", F.At(2026, 10, 13, 9), F.At(2026, 10, 13, 10));
        var toMidnight = F.Timed("mid", F.At(2026, 10, 13, 23), F.At(2026, 10, 14));
        Occurrence[] occ = [night, ten, inDay, other, two, toMidnight, two];
        List<string> Got(int r) =>
            [.. MonthLanes(occ, Row(r), p, false).Select(s => $"{s.Key} {s.Lane} {s.StartCol} {s.EndCol} {s.ContinuesBefore} {s.ContinuesAfter}")];
        // Row 1 is Mon 5 – Sun 11 October.
        Assert.Equal(["two| 0 2 3 False False", "other| 1 3 5 False False", "ten| 0 4 6 False True"], Got(1));
        // Row 2 is Mon 12 – Sun 18 October.
        Assert.Equal(["ten| 0 0 6 True False", "night| 1 1 2 False False"], Got(2));
        Assert.Equal(2, LaneCount(MonthLanes(occ, Row(1), p, false)));
        Assert.Equal(0, LaneCount([]));
        Assert.Empty(MonthLanes(occ, Row(4), p, false));
        Assert.False(IsMonthSpan(inDay, p));
        Assert.False(IsMonthSpan(toMidnight, p));
        Assert.True(IsMonthSpan(night, p));
        Assert.True(IsMonthSpan(two, p));
        // The pills of Tuesday 13 leave out the night's bar; the capacity of
        // row 2 loses its two lanes.
        var (pills, _) = CellItems(occ, F.At(2026, 10, 13), 10, p, false);
        Assert.Equal(["day", "mid"], pills.Select(x => x.Occurrence.EventId?.Value));
        Assert.Equal(3, CellCapacity(100, 20, LaneCount(MonthLanes(occ, Row(2), p, false)), 18));
        var declined = two with { MyResponse = EventResponse.Declined };
        Assert.Empty(MonthLanes([declined], Row(1), p, false));
        Assert.Single(MonthLanes([declined], Row(1), p, true));
        // Swift's: the title is cleaned, the colour's calendar carried.
        var first = MonthLanes([ten], Row(1), p, false)[0];
        Assert.Equal("ten", first.Title);
        Assert.Null(first.CalendarId);
        var untitled = MonthLanes([ten with { Title = "", CalendarId = new CalendarId("cal") }], Row(1), p, false)[0];
        Assert.Equal("Untitled Event", untitled.Title);
        Assert.Equal(new CalendarId("cal"), untitled.CalendarId);
        // A row of other than seven days has no bars.
        Assert.Empty(MonthLanes(occ, Row(1)[..6], p, false));
    }

    [Fact]
    public void ACellAcrossTheAutumnChange()
    {
        var p = F.Prague;
        // The 25-hour Sunday: a timed pill late in the day stays in its cell.
        var late = F.Timed("late", F.At(2026, 10, 25, 23), F.At(2026, 10, 25, 23, 30));
        var (pills, more) = CellItems([late], F.At(2026, 10, 25), 3, p, false);
        Assert.Equal(0, more);
        Assert.False(Assert.Single(pills).ContinuesBefore);
        Assert.Empty(CellItems([late], F.At(2026, 10, 26), 3, p, false).Pills);
        Assert.False(IsMonthSpan(late, p));
    }
}
