// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CalendarRecurrenceTests.swift; GTK:
// ui/internal/calendar/recurrence_test.go (TestRecurrenceText,
// TestRepeatChoices, TestCustomRecurrence, TestParseDayCode). A rule is
// written in the table as "frequency;interval;byDay;byMonthDay;byMonth;
// until;count;complex" (lists comma-separated, empty fields absent).

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Xunit;
using F = Malachi.Core.Tests.Calendars.CalFixture;

namespace Malachi.Core.Tests.Calendars;

public sealed class CalendarRecurrenceTests
{
    // Tuesday 13 October 2026, 10:00 in Prague.
    private static readonly DateTimeOffset Start = F.At(2026, 10, 13, 10);

    // A rule of the table's notation; null for "".
    private static Recurrence? Rule(string text)
    {
        if (text.Length == 0)
        {
            return null;
        }
        var f = text.Split(';');
        string? Field(int i) => i < f.Length && f[i].Length > 0 ? f[i] : null;
        return new Recurrence
        {
            Frequency = f[0],
            Interval = Field(1) is { } n ? int.Parse(n, System.Globalization.CultureInfo.InvariantCulture) : 0,
            ByDay = Field(2)?.Split(','),
            ByMonthDay = Field(3)?.Split(',').Select(s => int.Parse(s, System.Globalization.CultureInfo.InvariantCulture)).ToArray(),
            ByMonth = Field(4)?.Split(',').Select(s => int.Parse(s, System.Globalization.CultureInfo.InvariantCulture)).ToArray(),
            Until = Field(5) is { } u ? new CalendarDate(u) : default(CalendarDate?),
            Count = Field(6) is { } c ? int.Parse(c, System.Globalization.CultureInfo.InvariantCulture) : null,
            Complex = Field(7) == "complex" ? true : null,
        };
    }

    [Theory]
    [InlineData("", "")]
    [InlineData("daily;1", "Every day")]
    [InlineData("daily", "Every day")]
    [InlineData("daily;3", "Every 3 days")]
    [InlineData("daily;1;;;;;10", "Every day, 10 times")]
    [InlineData("daily;1;;;;;1", "Every day, 1 time")]
    [InlineData("weekly;1", "Every week on Tuesday")]
    [InlineData("weekly;1;TU;;;2027-03-01", "Every week on Tuesday, until 1 March 2027")]
    [InlineData("weekly;2;WE,MO", "Every 2 weeks on Monday and Wednesday")]
    [InlineData("weekly;1;FR,MO,WE", "Every week on Monday, Wednesday and Friday")]
    [InlineData("weekly;1;MO,TU,WE,TH,FR", "Every weekday")]
    [InlineData("weekly;1;SU,MO,TU,WE,TH,FR,SA", "Every day")]
    [InlineData("weekly;2;SU,MO", "Every 2 weeks on Monday and Sunday")]
    [InlineData("monthly;1", "Monthly on day 13")]
    [InlineData("monthly;1;;14", "Monthly on day 14")]
    [InlineData("monthly;3;;14", "Every 3 months on day 14")]
    [InlineData("monthly;1;;-1", "Monthly on the last day")]
    [InlineData("monthly;1;2TU", "Monthly on the second Tuesday")]
    [InlineData("monthly;1;+1MO", "Monthly on the first Monday")]
    [InlineData("monthly;1;-1FR", "Monthly on the last Friday")]
    [InlineData("monthly;2;-2FR", "Every 2 months on the second-to-last Friday")]
    [InlineData("yearly;1", "Yearly on 13 October")]
    [InlineData("yearly;1;;29;2", "Yearly on 29 February")]
    [InlineData("yearly;2;2TU;;10", "Every 2 years on the second Tuesday of October")]
    // Outside the editor's shape.
    [InlineData("weekly;1;;;;;;complex", "Custom")]
    [InlineData("weekly;1;2TU", "Custom")]
    [InlineData("monthly;1;1MO,3MO", "Custom")]
    [InlineData("monthly;1;MO", "Custom")]
    [InlineData("monthly;1;;-2", "Custom")]
    [InlineData("monthly;1;;;3", "Custom")]
    [InlineData("daily;1;MO", "Custom")]
    [InlineData("yearly;1;;;2,3", "Custom")]
    [InlineData("yearly;1;;31;4", "Custom")]
    [InlineData("yearly;1;;;13", "Custom")]
    [InlineData("hourly;1", "Custom")]
    [InlineData("weekly;1;XX", "Custom")]
    public void RecurrenceText(string rule, string want) => Assert.Equal(want, Cal.RecurrenceText(Rule(rule), Start, F.Env()));

    [Fact]
    public void RepeatChoices()
    {
        var texts = new Dictionary<Cal.Repeat, string>
        {
            [Cal.Repeat.Daily] = "Every day",
            [Cal.Repeat.Weekly] = "Every week on Tuesday",
            [Cal.Repeat.Biweekly] = "Every 2 weeks on Tuesday",
            [Cal.Repeat.Monthly] = "Monthly on day 13",
            [Cal.Repeat.Yearly] = "Yearly on 13 October",
        };
        foreach (var c in Cal.Repeats)
        {
            var r = Cal.RepeatRecurrence(c, Start, F.Prague);
            Assert.Equal(c is Cal.Repeat.Never or Cal.Repeat.Custom, r is null);
            Assert.Equal(texts.GetValueOrDefault(c, ""), Cal.RecurrenceText(r, Start, F.Env()));
            if (c != Cal.Repeat.Custom)
            {
                Assert.Equal(c, Cal.RepeatOf(r, Start, F.Prague));
            }
            Assert.NotEqual("", Cal.RepeatTitle(c));
        }
        Assert.Equal(Enum.GetValues<Cal.Repeat>(), Cal.Repeats);
        Assert.Equal("Every 2 Weeks", Cal.RepeatTitle(Cal.Repeat.Biweekly));
        Assert.Equal("Custom…", Cal.RepeatTitle(Cal.Repeat.Custom));
        Assert.Equal("Never", Cal.RepeatTitle(Cal.Repeat.Never));
    }

    [Theory]
    [InlineData("weekly", Cal.Repeat.Weekly)]
    [InlineData("weekly;2", Cal.Repeat.Biweekly)]
    [InlineData("monthly;1", Cal.Repeat.Monthly)]
    [InlineData("yearly;1", Cal.Repeat.Yearly)]
    [InlineData("daily;1;;;;;3", Cal.Repeat.Custom)]
    [InlineData("weekly;1;MO", Cal.Repeat.Custom)]
    [InlineData("weekly;3", Cal.Repeat.Custom)]
    [InlineData("daily;1;;;;;;complex", Cal.Repeat.Custom)]
    // Swift-only: an until is an end, and Never is no rule.
    [InlineData("daily;1;;;;2027-01-01", Cal.Repeat.Custom)]
    [InlineData("", Cal.Repeat.Never)]
    public void RepeatOf(string rule, Cal.Repeat want) => Assert.Equal(want, Cal.RepeatOf(Rule(rule), Start, F.Prague));

    // The start's day is read in the zone given: 23:30 UTC on Monday is
    // Tuesday in Prague.
    [Fact]
    public void RepeatRecurrenceReadsTheZone()
    {
        var late = F.UtcAt(2026, 10, 12, 23, 30);
        Assert.Equal(["TU"], Cal.RepeatRecurrence(Cal.Repeat.Weekly, late, F.Prague)!.ByDay!);
        Assert.Equal(["MO"], Cal.RepeatRecurrence(Cal.Repeat.Weekly, late, F.Utc)!.ByDay!);
        var yearly = Cal.RepeatRecurrence(Cal.Repeat.Yearly, late, F.Prague)!;
        Assert.Equal([10], yearly.ByMonth!);
        Assert.Equal([13], yearly.ByMonthDay!);
    }

    [Fact]
    public void CustomRecurrence()
    {
        var start = F.UtcAt(2026, 10, 13, 10, 0);
        var r = Cal.CustomRecurrence(
            RecurrenceFrequency.Weekly, 0, [Cal.Weekday.Friday, Cal.Weekday.Monday, Cal.Weekday.Friday, Cal.Weekday.Sunday], start, F.Utc);
        Assert.Equal(1, r.Interval);
        Assert.Equal(["MO", "FR", "SU"], r.ByDay!);
        r = Cal.CustomRecurrence(RecurrenceFrequency.Weekly, 150, null, start, F.Utc);
        Assert.Equal(99, r.Interval);
        Assert.Equal(["TU"], r.ByDay!);
        Assert.Equal([13], Cal.CustomRecurrence(RecurrenceFrequency.Monthly, 2, null, start, F.Utc).ByMonthDay!);
        r = Cal.CustomRecurrence(RecurrenceFrequency.Yearly, 1, null, start, F.Utc);
        Assert.Equal([10], r.ByMonth!);
        Assert.Equal([13], r.ByMonthDay!);
        r = Cal.CustomRecurrence(RecurrenceFrequency.Daily, 5, null, start, F.Utc);
        Assert.Equal(5, r.Interval);
        Assert.Null(r.ByDay);
        Assert.Equal("weeks", Cal.IntervalUnit(RecurrenceFrequency.Weekly, 2));
        Assert.Equal("day", Cal.IntervalUnit(RecurrenceFrequency.Daily, 1));
        Assert.Equal("months", Cal.IntervalUnit(RecurrenceFrequency.Monthly, 3));
        Assert.Equal("year", Cal.IntervalUnit(RecurrenceFrequency.Yearly, 1));
        Assert.Equal("Every", Cal.EveryLabel);
    }

    [Theory]
    [InlineData("MO", Cal.Weekday.Monday, 0, true)]
    [InlineData("2TU", Cal.Weekday.Tuesday, 2, true)]
    [InlineData("-1FR", Cal.Weekday.Friday, -1, true)]
    [InlineData("+3SU", Cal.Weekday.Sunday, 3, true)]
    [InlineData("0MO", Cal.Weekday.Sunday, 0, false)]
    [InlineData("6MO", Cal.Weekday.Sunday, 0, false)]
    [InlineData("M", Cal.Weekday.Sunday, 0, false)]
    [InlineData("XX", Cal.Weekday.Sunday, 0, false)]
    [InlineData("aMO", Cal.Weekday.Sunday, 0, false)]
    // Windows-only: Go's Atoi takes a sign after the stripped "+", and
    // nothing else.
    [InlineData("++2MO", Cal.Weekday.Monday, 2, true)]
    [InlineData("+-1MO", Cal.Weekday.Monday, -1, true)]
    [InlineData("+MO", Cal.Weekday.Sunday, 0, false)]
    [InlineData("-MO", Cal.Weekday.Sunday, 0, false)]
    [InlineData("99999999999MO", Cal.Weekday.Sunday, 0, false)]
    [InlineData("mo", Cal.Weekday.Sunday, 0, false)]
    [InlineData("", Cal.Weekday.Sunday, 0, false)]
    public void ParseDayCode(string s, Cal.Weekday d, int ord, bool ok)
    {
        var got = Cal.ParseDayCode(s);
        Assert.Equal(ok, got is not null);
        if (got is { } g)
        {
            Assert.Equal((d, ord), (g.Day, g.Ordinal));
        }
    }

    [Fact]
    public void ListTextAndDayCodes()
    {
        Assert.Equal("", Cal.ListText([]));
        Assert.Equal("a", Cal.ListText(["a"]));
        Assert.Equal("a and b", Cal.ListText(["a", "b"]));
        Assert.Equal("a, b and c", Cal.ListText(["a", "b", "c"]));
        Assert.Equal(["SU", "MO", "TU", "WE", "TH", "FR", "SA"], Enum.GetValues<Cal.Weekday>().Select(Cal.DayCode));
        Assert.Equal("Custom", Cal.Custom);
    }
}
