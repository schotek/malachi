// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CalendarHelpersTests.swift
// (englishStrftime, dateNormalises, standaloneMonth, lengthModifiers,
// weekdayOfFoundation); GTK: ui/internal/calendar/view_test.go
// (TestEnglishStrftime); the fixtures of helpers_test.go are CalFixture.
//
// The strftime writers and the wall-clock arithmetic the port adds in place
// of Go's time.Date. Swift's cases of its DateFormatter mapping
// (Strftime.toDateFormat) are the .NET writer's here: Cal.CultureStrftime
// over I18n.Strftime, with the culture's own names (ICU).

using System;
using System.Globalization;
using Malachi.Core.Calendars;
using Xunit;
using static Malachi.Core.Calendars.Cal;
using F = Malachi.Core.Tests.Calendars.CalFixture;

namespace Malachi.Core.Tests.Calendars;

public sealed class CalendarHelpersTests
{
    private static readonly CultureInfo Czech = CultureInfo.GetCultureInfo("cs-CZ");

    [Theory]
    [InlineData("%a %A %b %B", "Sun Sunday Oct October")]
    [InlineData("%d %-d %e|%-e", "04 4  4|4")]
    [InlineData("%H:%M %-H", "09:05 9")]
    [InlineData("%I %-I %p", "09 9 AM")]
    [InlineData("%m %-m %Y %y", "10 10 2026 26")]
    [InlineData("100%% %q", "100% %q")]
    [InlineData("%OB %Y|%OX", "October 2026|%OX")]
    [InlineData("%", "%")]
    [InlineData("%-", "%-")]
    [InlineData("%-q", "%-q")]
    [InlineData("plain", "plain")]
    [InlineData("", "")]
    public void EnglishStrftimeCases(string format, string want) =>
        Assert.Equal(want, EnglishStrftime(F.UtcAt(2026, 10, 4, 9, 5), format, F.Utc));

    [Fact]
    public void EnglishStrftimeNoonAndTheZone()
    {
        var t = F.UtcAt(2026, 10, 4, 9, 5);
        Assert.Equal("12 AM", EnglishStrftime(t.AddHours(15), "%-I %p", F.Utc));
        Assert.Equal("12 PM", EnglishStrftime(F.UtcAt(2026, 10, 4, 12, 0), "%-I %p", F.Utc));
        // The writer reads the wall clock of the zone it is given, not of the offset.
        Assert.Equal("11:05", EnglishStrftime(t, "%H:%M", F.Prague));
        Assert.Equal("Mon 5", EnglishStrftime(F.UtcAt(2026, 10, 4, 23, 0), "%a %-d", F.Prague));
        // The env writes in its zone.
        Assert.Equal("11:05", Cal.Text.TimeText(t, F.Env(F.Prague)));
        Assert.Equal("09:05", Cal.Text.TimeText(t, F.Env(F.Utc)));
    }

    /// <summary>Go's time.Date normalises every part; a time the clocks skip moves on by the gap.</summary>
    [Fact]
    public void DateNormalises()
    {
        Assert.Equal(Date(2027, 1, 1, F.Prague), Date(2026, 13, 1, F.Prague));
        Assert.Equal(Date(2026, 2, 28, F.Prague), Date(2026, 3, 0, F.Prague));
        Assert.Equal(Date(2026, 10, 15, F.Prague), Date(2026, 10, 14, 23, 60, F.Prague));
        Assert.Equal(Date(2025, 12, 30, F.Utc), Date(2026, 1, -1, F.Utc));
        Assert.Equal(Date(2026, 10, 15, 0, 0, F.Utc), Date(2026, 10, 14, 23, 59, 60, F.Utc));
        Assert.Equal(Date(2025, 11, 1, F.Utc), Date(2026, -1, 1, F.Utc));
        // 29 March 2026, 02:00 CET does not exist: 03:00 CEST (01:00 UTC).
        Assert.Equal(F.UtcAt(2026, 3, 29, 1, 0), Date(2026, 3, 29, 2, 0, F.Prague));
        Assert.Equal(F.UtcAt(2026, 3, 29, 1, 30), Date(2026, 3, 29, 2, 30, F.Prague));
        // 25 October 2026, 02:30 is the first one (CEST, 00:30 UTC).
        Assert.Equal(F.UtcAt(2026, 10, 25, 0, 30), Date(2026, 10, 25, 2, 30, F.Prague));
        // Around the changes the wall clock is what was asked.
        Assert.Equal(F.UtcAt(2026, 3, 29, 0, 59), Date(2026, 3, 29, 1, 59, F.Prague));
        Assert.Equal(F.UtcAt(2026, 10, 25, 2, 0), Date(2026, 10, 25, 3, 0, F.Prague));
        // A result is expressed in the zone's offset at that instant (Go's In(loc)).
        Assert.Equal(TimeSpan.FromHours(2), Date(2026, 10, 16, F.Prague).Offset);
        Assert.Equal(TimeSpan.FromHours(1), Date(2026, 12, 16, F.Prague).Offset);
        foreach (var n in new[] { -800_000, -1, 0, 1, 20_000, 100_000 })
        {
            var (y, m, d) = CivilFromDays(n);
            Assert.Equal(n, DaysFromCivil(y, m, d));
        }
        Assert.Equal((1970, 1, 1), CivilFromDays(0));
        Assert.Equal(20_742, DaysFromCivil(2026, 10, 16));
        // Outside DateTimeOffset's years: its ends.
        Assert.Equal(DateTimeOffset.MinValue, Date(0, 12, 31, F.Utc));
        Assert.Equal(DateTimeOffset.MaxValue, Date(10_000, 1, 1, F.Utc));
    }

    /// <summary>A day of 23 or 25 hours is still one day; midnights stay midnights.</summary>
    [Fact]
    public void DaysAcrossTheChanges()
    {
        var p = F.Prague;
        Assert.Equal(F.At(2026, 3, 30), AddDays(F.At(2026, 3, 29, 12), 1, p));
        Assert.Equal(F.At(2026, 10, 26), AddDays(F.At(2026, 10, 25), 1, p));
        Assert.Equal(F.At(2026, 10, 24), AddDays(F.At(2026, 10, 25, 23, 59), -1, p));
        Assert.Equal(F.At(2026, 10, 25), StartOfDay(F.At(2026, 10, 25, 23, 59), p));
        Assert.Equal(1, DaysBetween(F.At(2026, 3, 29), F.At(2026, 3, 30), p));
        Assert.Equal(1, DaysBetween(F.At(2026, 10, 25, 23, 0), F.At(2026, 10, 26, 0, 30), p));
        Assert.Equal(-365, DaysBetween(F.At(2026, 10, 16), F.At(2025, 10, 16), p));
        Assert.True(SameDay(F.At(2026, 10, 25, 0, 0), F.At(2026, 10, 25, 23, 59), p));
        Assert.False(SameDay(F.At(2026, 10, 25, 23, 59), F.At(2026, 10, 26), p));
        // 23:30 in Prague is another day in UTC.
        Assert.False(SameDay(F.At(2026, 10, 16, 23, 30), F.At(2026, 10, 16, 0, 30), F.Utc));
        Assert.Equal(F.At(2026, 10, 12), StartOfWeek(F.At(2026, 10, 18, 23), Weekday.Monday, p));
        Assert.Equal(F.At(2026, 10, 18), StartOfWeek(F.At(2026, 10, 18, 23), Weekday.Sunday, p));
        Assert.Equal(new Parts(2026, 10, 25, 2, 30, 0, Weekday.Sunday), PartsOf(F.UtcAt(2026, 10, 25, 0, 30), p));
        Assert.Equal(new Parts(2026, 10, 25, 2, 30, 0, Weekday.Sunday), PartsOf(F.UtcAt(2026, 10, 25, 1, 30), p));
    }

    /// <summary>%OB is the standalone month name: the nominative in Czech, the genitive is %B's.</summary>
    [Fact]
    public void StandaloneMonth()
    {
        var t = F.At(2026, 10, 16, 12);
        Assert.Equal("October 2026", EnglishStrftime(t, "%OB %Y", F.Prague));
        var cs = Env.ForCulture(F.Prague, Czech);
        Assert.Equal("říjen 2026", cs.Format(t, "%OB %Y", F.Prague));
        Assert.Equal("říjen", Cal.Text.MonthText(t, cs));
        Assert.Equal("16. října", cs.Format(t, "%-d. %B", F.Prague));
        Assert.Equal("16. října 2026", cs.Format(t, "%-d. %B %Y", F.Prague));
        // A literal around %OB stays text, a % in it too.
        Assert.Equal("100% říjen at", cs.Format(t, "100%% %OB at", F.Prague));
        var en = Env.ForCulture(F.Prague, CultureInfo.GetCultureInfo("en-US"));
        Assert.Equal("October 2026", Cal.Text.MonthYearText(t, en));
        Assert.Equal("Friday 16 October 2026", Cal.Text.LongDateText(t, en));
        Assert.Equal("Fri 16 Oct 2026 12:00", Cal.Text.DateTimeText(t, en));
    }

    /// <summary>A C length modifier a catalogue generator added ("%-ld") is read as the directive after it, never shown as text.</summary>
    [Fact]
    public void LengthModifiers()
    {
        var t = F.At(2026, 10, 16, 9);
        var cs = Env.ForCulture(F.Prague, Czech);
        Assert.Equal("16. říjen 2026", cs.Format(t, "%-ld. %OB %Y", F.Prague));
        Assert.Equal("16", cs.Format(t, "%-ld", F.Prague));
        Assert.Equal("09:00", cs.Format(t, "%lH:%lM", F.Prague));
        Assert.Equal("%l", cs.Format(t, "%l", F.Prague));
        Assert.Equal("%-l", cs.Format(t, "%-l", F.Prague));
        Assert.Equal("16 October 2026", EnglishStrftime(t, "%-ld %OB %Y", F.Prague));
        Assert.Equal("16|16|09|16", EnglishStrftime(t, "%-ld|%ld|%lH|%-lld", F.Prague));
        Assert.Equal("%l", EnglishStrftime(t, "%l", F.Prague));
        Assert.Equal("%-l", EnglishStrftime(t, "%-l", F.Prague));
    }

    /// <summary>Swift's Weekday(firstWeekday:) for Foundation's is .NET's DayOfWeek and the culture's first day here.</summary>
    [Fact]
    public void WeekdayOfDotNet()
    {
        Assert.Equal(Weekday.Sunday, WeekdayOf(DayOfWeek.Sunday));
        Assert.Equal(Weekday.Monday, WeekdayOf(DayOfWeek.Monday));
        Assert.Equal(Weekday.Saturday, WeekdayOf(DayOfWeek.Saturday));
        Assert.Equal(Weekday.Monday, LocaleFirstDay(Czech));
        Assert.Equal(Weekday.Sunday, LocaleFirstDay(CultureInfo.GetCultureInfo("en-US")));
        Assert.Equal(0, Weekday.Monday.FromMonday);
        Assert.Equal(6, Weekday.Sunday.FromMonday);
        Assert.Equal(5, Weekday.Saturday.FromMonday);
    }
}
