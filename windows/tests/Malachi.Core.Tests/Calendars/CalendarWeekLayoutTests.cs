// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CalendarWeekLayoutTests.swift; GTK:
// ui/internal/calendar/week_test.go (TestLayoutDayOverlaps,
// TestLayoutDayClipsAndDeclined, TestLayoutDayDaylightSaving,
// TestAllDayLanes, TestNowLine, TestStateOf, TestBlockLabel), with Swift's
// cases of ParseClock. Overlaps of 1 to 8 events, clusters, clipping, the
// days of 23 and 25 hours, all-day lanes across a week boundary.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Xunit;
using static Malachi.Core.Calendars.Cal;
using F = Malachi.Core.Tests.Calendars.CalFixture;

namespace Malachi.Core.Tests.Calendars;

public sealed class CalendarWeekLayoutTests
{
    private static readonly TimeZoneInfo P = F.Prague;

    // A block's id, column, columns, top and bottom.
    private sealed record Placed(string Id, int Col, int Cols, int Top, int Bottom);

    private static List<Placed> Layout(IReadOnlyList<Occurrence> occ, DateTimeOffset day, bool showDeclined = false) =>
        [.. LayoutDay(occ, day, P, showDeclined).Select(b => new Placed(b.Occurrence.EventId?.Value ?? "", b.Column, b.Columns, b.Top, b.Bottom))];

    private static DateTimeOffset H(int hh, int mm) => F.At(2026, 10, 14, hh, mm);

    [Fact]
    public void OverlapsOfOneToEight()
    {
        var day = F.At(2026, 10, 14);
        // 1 to 8 events at the same time: one column each.
        for (var n = 1; n <= 8; n++)
        {
            var occ = new List<Occurrence>();
            var want = new List<Placed>();
            for (var i = 0; i < n; i++)
            {
                occ.Add(F.Timed($"e{i}", H(10, 0), H(11, 0)));
                want.Add(new Placed($"e{i}", i, n, 600, 660));
            }
            // Given in reverse: the order is by key, not by input.
            occ.Reverse();
            Assert.Equal(want, Layout(occ, day));
        }
    }

    private static readonly Dictionary<string, (Occurrence[] Occ, Placed[] Want)> OverlapCases = new()
    {
        ["staircase reuses the freed column"] = (
            [F.Timed("a", H(9, 0), H(10, 0)), F.Timed("b", H(9, 30), H(10, 30)), F.Timed("c", H(10, 0), H(11, 0))],
            [new("a", 0, 2, 540, 600), new("b", 1, 2, 570, 630), new("c", 0, 2, 600, 660)]),
        ["two clusters"] = (
            [F.Timed("a", H(9, 0), H(10, 0)), F.Timed("b", H(9, 0), H(10, 0)), F.Timed("c", H(11, 0), H(12, 0))],
            [new("a", 0, 2, 540, 600), new("b", 1, 2, 540, 600), new("c", 0, 1, 660, 720)]),
        ["longer first, greedy"] = (
            [F.Timed("d", H(10, 30), H(11, 30)), F.Timed("b", H(9, 0), H(10, 0)), F.Timed("c", H(10, 0), H(11, 0)),
                F.Timed("a", H(9, 0), H(12, 0))],
            [new("a", 0, 3, 540, 720), new("b", 1, 3, 540, 600), new("c", 1, 3, 600, 660), new("d", 2, 3, 630, 690)]),
        ["back to back do not overlap"] = (
            [F.Timed("a", H(9, 0), H(10, 0)), F.Timed("b", H(10, 0), H(11, 0))],
            [new("a", 0, 1, 540, 600), new("b", 0, 1, 600, 660)]),
        ["short blocks get the minimum height and overlap by it"] = (
            [F.Timed("a", H(9, 0), H(9, 5)), F.Timed("b", H(9, 10), H(9, 20))],
            [new("a", 0, 2, 540, 555), new("b", 1, 2, 550, 565)]),
        ["no length at the end of the day"] = (
            [F.Timed("a", H(23, 55), H(23, 55))],
            [new("a", 0, 1, 1425, 1440)]),
        ["all-day left out"] = (
            [F.AllDayOcc("x", F.At(2026, 10, 14), 1), F.Timed("a", H(8, 0), H(9, 0))],
            [new("a", 0, 1, 480, 540)]),
    };

    [Theory]
    [InlineData("staircase reuses the freed column")]
    [InlineData("two clusters")]
    [InlineData("longer first, greedy")]
    [InlineData("back to back do not overlap")]
    [InlineData("short blocks get the minimum height and overlap by it")]
    [InlineData("no length at the end of the day")]
    [InlineData("all-day left out")]
    public void Overlaps(string name)
    {
        var (occ, want) = OverlapCases[name];
        Assert.Equal(want, Layout(occ, F.At(2026, 10, 14)));
    }

    [Fact]
    public void ClipsAndDeclined()
    {
        var night = F.Timed("n", F.At(2026, 10, 14, 22), F.At(2026, 10, 15, 2));
        var b = LayoutDay([night], F.At(2026, 10, 14), P, false);
        Assert.Single(b);
        Assert.True(b[0].Top == 1320 && b[0].Bottom == 1440 && b[0].ContinuesAfter && !b[0].ContinuesBefore, $"first night: {b[0]}");
        b = LayoutDay([night], F.At(2026, 10, 15), P, false);
        Assert.Single(b);
        Assert.True(b[0].Top == 0 && b[0].Bottom == 120 && b[0].ContinuesBefore && !b[0].ContinuesAfter, $"second night: {b[0]}");
        Assert.Equal(F.At(2026, 10, 15), b[0].Start);
        Assert.Equal(F.At(2026, 10, 15, 2), b[0].End);
        Assert.Empty(LayoutDay([night], F.At(2026, 10, 16), P, false));
        var declined = F.Timed("d", F.At(2026, 10, 14, 9), F.At(2026, 10, 14, 10)) with { MyResponse = EventResponse.Declined };
        Assert.Empty(LayoutDay([declined], F.At(2026, 10, 14), P, false));
        b = LayoutDay([declined], F.At(2026, 10, 14), P, true);
        Assert.Single(b);
        Assert.Equal(BlockState.Declined, b[0].State);
    }

    [Fact]
    public void DaylightSaving()
    {
        // 29 March 2026: 02:00 CET becomes 03:00 CEST, the day has 23 hours.
        var spring = F.At(2026, 3, 29);
        Assert.Equal(23, F.Hours(spring, AddDays(spring, 1, P)));
        var s1 = F.UtcAt(2026, 3, 29, 0, 30); // 01:30 CET
        var e1 = F.UtcAt(2026, 3, 29, 1, 30); // 03:30 CEST, one hour later
        var s2 = F.UtcAt(2026, 3, 29, 1, 0); // 03:00 CEST
        var e2 = F.UtcAt(2026, 3, 29, 2, 0); // 04:00 CEST
        Assert.Equal(
            [new Placed("a", 0, 2, 90, 210), new Placed("b", 1, 2, 180, 240)],
            Layout([F.Timed("a", s1, e1), F.Timed("b", s2, e2)], spring));
        // 25 October 2026: 03:00 CEST becomes 02:00 CET, the day has 25 hours.
        var autumn = F.At(2026, 10, 25);
        Assert.Equal(25, F.Hours(autumn, AddDays(autumn, 1, P)));
        var s3 = F.UtcAt(2026, 10, 25, 0, 30); // 02:30 CEST
        var e3 = F.UtcAt(2026, 10, 25, 1, 30); // 02:30 CET
        var s4 = F.UtcAt(2026, 10, 24, 23, 0); // 01:00 CEST
        var e4 = F.UtcAt(2026, 10, 25, 3, 0); // 04:00 CET
        var s5 = F.UtcAt(2026, 10, 25, 22, 0); // 23:00 CET
        var e5 = F.UtcAt(2026, 10, 25, 23, 0); // 00:00 next day
        Assert.Equal(
            [new Placed("d", 0, 2, 60, 240), new Placed("c", 1, 2, 150, 165), new Placed("e", 0, 1, 1380, 1440)],
            Layout([F.Timed("c", s3, e3), F.Timed("d", s4, e4), F.Timed("e", s5, e5)], autumn));
    }

    [Fact]
    public void AllDayLanesAcrossWeeks()
    {
        var week1 = ViewRange(View.Week, F.At(2026, 10, 14), Weekday.Monday, P).Days();
        var week2 = ViewRange(View.Week, F.At(2026, 10, 21), Weekday.Monday, P).Days();
        var span = F.AllDayOcc("span", F.At(2026, 10, 17), 4); // Saturday to Tuesday
        var a = F.AllDayOcc("a", F.At(2026, 10, 12), 3); // Monday to Wednesday
        var b = F.AllDayOcc("b", F.At(2026, 10, 13), 1); // Tuesday
        var c = F.AllDayOcc("c", F.At(2026, 10, 15), 1); // Thursday
        var timedOne = F.Timed("t", F.At(2026, 10, 13, 9), F.At(2026, 10, 13, 10));
        var (bars, lanes) = AllDayLanes([c, span, timedOne, b, a], week1, P, false);
        Assert.Equal(
            ["a 0 0 2 False False", "b 1 1 1 False False", "c 0 3 3 False False", "span 0 5 6 False True"],
            bars.Select(x => $"{x.Occurrence.EventId?.Value} {x.Lane} {x.First} {x.Last} {x.ContinuesBefore} {x.ContinuesAfter}"));
        Assert.Equal(2, lanes);
        (bars, lanes) = AllDayLanes([span], week2, P, false);
        Assert.Single(bars);
        Assert.True(bars[0].First == 0 && bars[0].Last == 1 && bars[0].ContinuesBefore && !bars[0].ContinuesAfter, $"week 2: {bars[0]}");
        Assert.Equal(1, lanes);
        // One day of the Day view.
        (bars, _) = AllDayLanes([span], [week2[0]], P, false);
        Assert.Single(bars);
        Assert.True(bars[0].First == 0 && bars[0].Last == 0 && bars[0].ContinuesBefore && bars[0].ContinuesAfter, $"one day: {bars[0]}");
        var none = AllDayLanes([], [], P, false);
        Assert.Empty(none.Bars);
        Assert.Equal(0, none.Lanes);
    }

    [Fact]
    public void NowLineAndScrollMinute()
    {
        var r = ViewRange(View.Week, F.At(2026, 10, 14), Weekday.Monday, P);
        Assert.Equal((2, 817), NowLine(F.At(2026, 10, 14, 13, 37), r, P));
        Assert.Null(NowLine(F.At(2026, 10, 19), r, P));
        Assert.Equal(420, ScrollMinute(Wh("08:00")));
        Assert.Equal(420, ScrollMinute(Wh("x")));
        Assert.Equal(0, ScrollMinute(Wh("00:30")));
    }

    private static WorkingHours Wh(string start) => new() { Start = start, End = "" };

    [Theory]
    [InlineData("9:30", 570)]
    [InlineData("08:00", 480)]
    [InlineData("23:59", 1439)]
    [InlineData("00:00", 0)]
    [InlineData("24:00", null)]
    [InlineData("08:0", null)]
    [InlineData("08:00 ", null)]
    [InlineData("08:60", null)]
    [InlineData("123:00", null)]
    [InlineData("8", null)]
    [InlineData("", null)]
    [InlineData(null, null)]
    [InlineData("٠٨:٠٠", null)]
    public void ParseClockCases(string? s, int? want) => Assert.Equal(want, ParseClock(s));

    [Theory]
    [InlineData("confirmed", "busy", null, false, BlockState.Normal)]
    [InlineData("", "", EventResponse.Accepted, false, BlockState.Normal)]
    [InlineData("", "", EventResponse.Tentative, false, BlockState.Tentative)]
    [InlineData("tentative", "", null, false, BlockState.Tentative)]
    [InlineData("", "tentative", null, false, BlockState.Tentative)]
    [InlineData("", "", EventResponse.NeedsAction, false, BlockState.Unanswered)]
    [InlineData("", "tentative", null, true, BlockState.Unanswered)]
    [InlineData("", "tentative", EventResponse.Declined, false, BlockState.Declined)]
    [InlineData("cancelled", "", EventResponse.Declined, false, BlockState.Cancelled)]
    public void StateOfCases(string status, string showAs, string? myResponse, bool invitation, BlockState want)
    {
        // Not ": null": EventResponse converts from a string, so a bare null would be one.
        EventResponse? response = myResponse is { } r ? new EventResponse(r) : default(EventResponse?);
        MessageRef? inv = invitation ? new MessageRef { AccountId = new AccountId(""), MessageId = new MessageId("") } : null;
        var o = F.Occ(status, showAs, response, inv);
        Assert.Equal(want, StateOf(o));
    }

    [Fact]
    public void BlockLabelCases()
    {
        var env = F.Env();
        var o = F.Timed("Standup", F.At(2026, 10, 13, 10), F.At(2026, 10, 13, 10, 30)) with { MyResponse = EventResponse.NeedsAction };
        Assert.Equal("Standup, Tuesday 10:00–10:30, awaiting your answer", BlockLabel(o, env));
        o = o with { MyResponse = null };
        Assert.Equal("Standup, Tuesday 10:00–10:30", BlockLabel(o, env));
        var ad = F.AllDayOcc("Offsite", F.At(2026, 10, 15), 2);
        Assert.Equal("Offsite, Thursday, all day", BlockLabel(ad, env));
        Assert.Equal("X", BlockLabel(F.Occ() with { Title = "\u202EX" }, env));
        // Untitled, spoken with its state.
        var cancelled = F.Timed("", F.At(2026, 10, 13, 10), F.At(2026, 10, 13, 11)) with { Status = EventStatus.Cancelled };
        Assert.Equal("Untitled Event, Tuesday 10:00–11:00, cancelled", BlockLabel(cancelled, env));
    }

    [Theory]
    [InlineData(BlockState.Normal, "")]
    [InlineData(BlockState.Tentative, "tentative")]
    [InlineData(BlockState.Unanswered, "awaiting your answer")]
    [InlineData(BlockState.Declined, "declined")]
    [InlineData(BlockState.Cancelled, "cancelled")]
    public void BlockStateTexts(BlockState s, string want) => Assert.Equal(want, BlockStateText(s));
}
