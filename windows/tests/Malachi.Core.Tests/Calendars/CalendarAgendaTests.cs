// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CalendarAgendaTests.swift; GTK:
// ui/internal/calendar/agenda_test.go (TestAgenda, TestDayHeading), with
// the agenda's texts and the days of a long occurrence clipped to the
// range.

using System;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Xunit;
using static Malachi.Core.Calendars.Cal;
using F = Malachi.Core.Tests.Calendars.CalFixture;

namespace Malachi.Core.Tests.Calendars;

public sealed class CalendarAgendaTests
{
    private static string Lines(System.Collections.Generic.IReadOnlyList<AgendaDay> days, Cal.Env env) =>
        string.Join("\n", days.Select(d =>
            d.Heading + ":" + string.Concat(d.Items.Select(it => $" {it.Occurrence.EventId?.Value}({it.DayOf}/{it.Days} {ItemTime(it, env)})"))));

    [Fact]
    public void AgendaByDay()
    {
        var env = F.Env();
        var now = F.At(2026, 10, 14, 12);
        var trip = F.AllDayOcc("trip", F.At(2026, 10, 15), 3);
        var night = F.Timed("night", F.At(2026, 10, 14, 22), F.At(2026, 10, 15, 2));
        var midnight = F.Timed("mid", F.At(2026, 10, 14, 23), F.At(2026, 10, 15));
        var early = F.Timed("early", F.At(2026, 10, 14, 8), F.At(2026, 10, 14, 9));
        var declined = F.Timed("declined", F.At(2026, 10, 14, 9), F.At(2026, 10, 14, 10)) with { MyResponse = EventResponse.Declined };
        var before = F.Timed("before", F.At(2026, 10, 1, 8), F.At(2026, 10, 1, 9));
        var days = Agenda([trip, night, midnight, early, declined, before], now, 30, now, env, false);
        Assert.Equal(
            string.Join("\n",
                "Today: early(1/1 08:00–09:00) night(1/2 from 22:00) mid(1/1 23:00–00:00)",
                "Tomorrow: trip(1/3 All Day) night(2/2 until 02:00)",
                "Friday 16 October: trip(2/3 All Day)",
                "Saturday 17 October: trip(3/3 All Day)"),
            Lines(days, env));
        Assert.Equal(F.At(2026, 10, 14), days[0].Day);
        Assert.Equal(F.At(2026, 10, 17), days[3].Day);
        Assert.Empty(Agenda([], now, 30, now, env, false));
        Assert.Equal("Day 2 of 3", DayOfText(2, 3));
        var end = now.AddHours(72);
        var middle = new AgendaItem(F.Timed("x", now, end), now, end, 2, 4);
        Assert.Equal("All Day", ItemTime(middle, env));
        Assert.Equal(BlockState.Normal, middle.State);
    }

    [Fact]
    public void DeclinedWithShowDeclined()
    {
        var env = F.Env();
        var now = F.At(2026, 10, 14, 12);
        var declined = F.Timed("declined", F.At(2026, 10, 14, 9), F.At(2026, 10, 14, 10)) with { MyResponse = EventResponse.Declined };
        var days = Agenda([declined], now, 1, now, env, true);
        Assert.Equal(BlockState.Declined, Assert.Single(Assert.Single(days).Items).State);
    }

    [Fact]
    public void ALongOccurrenceIsClippedToTheRange()
    {
        var env = F.Env();
        var now = F.At(2026, 10, 14, 12);
        // From 1 October to 1 January: the 30 days from now, days 14 to 43 of 92.
        var quarter = F.AllDayOcc("quarter", F.At(2026, 10, 1), 92);
        var days = Agenda([quarter], now, 30, now, env, false);
        Assert.Equal(30, days.Count);
        Assert.Equal((14, 92), (days[0].Items[0].DayOf, days[0].Items[0].Days));
        Assert.Equal((43, 92), (days[29].Items[0].DayOf, days[29].Items[0].Days));
        // The days of the autumn change are one day each.
        Assert.Equal(F.At(2026, 10, 25), days[11].Day);
        Assert.Equal(F.At(2026, 10, 26), days[12].Day);
        Assert.Empty(Agenda([quarter], now, 0, now, env, false));
    }

    [Fact]
    public void ATimedOccurrenceEndingAtMidnightKeepsToItsDay()
    {
        var env = F.Env();
        var now = F.At(2026, 10, 24, 12);
        // 23:00 to the midnight after the 25-hour day: two days, not three.
        var o = F.Timed("o", F.At(2026, 10, 24, 23), F.At(2026, 10, 26));
        var days = Agenda([o], now, 5, now, env, false);
        Assert.Equal(["Today", "Tomorrow"], days.Select(d => d.Heading));
        Assert.Equal("from 23:00", ItemTime(days[0].Items[0], env));
        Assert.Equal("until 00:00", ItemTime(days[1].Items[0], env));
        // One of no length is one day.
        var instant = F.Timed("i", F.At(2026, 10, 25), F.At(2026, 10, 25));
        Assert.Equal("00:00–00:00", ItemTime(Assert.Single(Assert.Single(Agenda([instant], now, 5, now, env, false)).Items), env));
    }

    [Theory]
    [InlineData("2026-12-31", "Today")]
    [InlineData("2027-01-01", "Tomorrow")]
    [InlineData("2026-12-30", "Yesterday")]
    [InlineData("2026-12-25", "Friday 25 December")]
    [InlineData("2027-01-05", "Tuesday 5 January 2027")]
    public void DayHeadingCases(string day, string want) =>
        Assert.Equal(want, DayHeading(F.Local(day), F.At(2026, 12, 31, 23, 30), F.Env()));

    [Fact]
    public void Texts()
    {
        Assert.Equal("No events in this period.", AgendaEmpty);
        Assert.Equal("Results", ResultsHeading);
        Assert.Equal("No events match the search.", NoResults);
        Assert.Equal("Invitations", UnansweredHeading);
    }
}
