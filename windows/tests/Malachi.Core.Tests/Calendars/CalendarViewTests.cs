// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CalendarViewTests.swift; GTK:
// ui/internal/calendar/view_test.go (TestViewNicks, TestViewRange,
// TestStep, TestWeekStart, TestWeekNumber, TestTitleAndSubtitle,
// TestSpanAndKey, TestSwitchAnchor; TestEnglishStrftime is in
// CalendarHelpersTests, as in Swift, and Swift's activeCalendars in
// CalendarActiveTests, after Go's calendars_test.go). The table cells are
// wall-clock times in Prague (CalFixture.Local), a real zone with a day of
// 23 and one of 25 hours in 2026.

using System;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Xunit;
using static Malachi.Core.Calendars.Cal;
using F = Malachi.Core.Tests.Calendars.CalFixture;

namespace Malachi.Core.Tests.Calendars;

public sealed class CalendarViewTests
{
    private static readonly TimeZoneInfo P = F.Prague;

    [Fact]
    public void ViewNicks()
    {
        foreach (var v in Views)
        {
            Assert.Equal((v, true), ParseView(v.Nick));
        }
        Assert.Equal((View.Week, false), ParseView("year"));
        Assert.Equal((View.Week, false), ParseView(null));
        // The settings' nicks are the members' names in lower case, in the gschema's order.
        Assert.Equal(["day", "week", "month", "agenda"], Views.Select(v => v.Nick));
        Assert.Equal(Views, Enum.GetValues<View>());
        Assert.Equal(View.Week, DefaultView);
        Assert.Equal(30, AgendaDays);
    }

    [Theory]
    [InlineData("week from Monday", View.Week, "2026-10-16T15:30", Weekday.Monday, "2026-10-12", "2026-10-19", 168)]
    [InlineData("week from Sunday", View.Week, "2026-10-16T15:30", Weekday.Sunday, "2026-10-11", "2026-10-18", 168)]
    [InlineData("week from Saturday", View.Week, "2026-10-16T15:30", Weekday.Saturday, "2026-10-10", "2026-10-17", 168)]
    [InlineData("week with the autumn change", View.Week, "2026-10-25T12:00", Weekday.Monday, "2026-10-19", "2026-10-26", 169)]
    [InlineData("day", View.Day, "2026-10-16T15:30", Weekday.Monday, "2026-10-16", "2026-10-17", 24)]
    [InlineData("day of 23 hours", View.Day, "2026-03-29T12:00", Weekday.Monday, "2026-03-29", "2026-03-30", 23)]
    [InlineData("day of 25 hours", View.Day, "2026-10-25T12:00", Weekday.Monday, "2026-10-25", "2026-10-26", 25)]
    [InlineData("month grid", View.Month, "2026-10-16T15:30", Weekday.Monday, "2026-09-28", "2026-11-09", 42 * 24 + 1)]
    [InlineData("agenda", View.Agenda, "2026-10-16T15:30", Weekday.Monday, "2026-10-16", "2026-11-15", 30 * 24 + 1)]
    public void ViewRangeCases(string name, View v, string anchor, Weekday ws, string from, string to, double hours)
    {
        var r = ViewRange(v, F.Local(anchor), ws, P);
        Assert.True(r.From == F.Local(from) && r.To == F.Local(to), $"{name}: {r.From:o}–{r.To:o}");
        Assert.Equal(hours, F.Hours(r.From, r.To));
        Assert.Same(P, r.Zone);
    }

    [Fact]
    public void AWeekWithTheSpringChangeHasSevenDays()
    {
        var days = ViewRange(View.Week, F.At(2026, 3, 29), Weekday.Monday, P).Days();
        Assert.Equal(7, days.Count);
        Assert.Equal(F.At(2026, 3, 23), days[0]);
        Assert.Equal(F.At(2026, 3, 29), days[6]);
        Assert.All(days, d => Assert.Equal(0, PartsOf(d, P).Hour));
    }

    [Theory]
    [InlineData(View.Day, "2026-03-28T10:00", 1, "2026-03-29")]
    [InlineData(View.Day, "2026-03-29T10:00", 1, "2026-03-30")]
    [InlineData(View.Week, "2026-10-16", -1, "2026-10-09")]
    [InlineData(View.Week, "2026-10-20", 1, "2026-10-27")]
    [InlineData(View.Month, "2026-01-31", 1, "2026-02-28")]
    [InlineData(View.Month, "2026-03-31", -1, "2026-02-28")]
    [InlineData(View.Month, "2026-12-15", 1, "2027-01-15")]
    [InlineData(View.Agenda, "2026-10-16", 1, "2026-11-15")]
    public void StepCases(View v, string anchor, int dir, string want) =>
        Assert.Equal(F.Local(want), Step(v, F.Local(anchor), dir, P));

    [Theory]
    [InlineData("locale", Weekday.Sunday, Weekday.Sunday)]
    [InlineData("locale", Weekday.Monday, Weekday.Monday)]
    [InlineData("monday", Weekday.Sunday, Weekday.Monday)]
    [InlineData("sunday", Weekday.Monday, Weekday.Sunday)]
    [InlineData("saturday", Weekday.Monday, Weekday.Saturday)]
    [InlineData("tuesday", Weekday.Monday, Weekday.Monday)]
    [InlineData("", Weekday.Saturday, Weekday.Saturday)]
    public void WeekStartCases(string pref, Weekday locale, Weekday want) =>
        Assert.Equal(want, Cal.WeekStart(new WeekStart(pref), locale));

    [Theory]
    // ISO 8601.
    [InlineData("2026-01-01", Weekday.Monday, 1)]
    [InlineData("2026-10-16", Weekday.Monday, 42)]
    [InlineData("2027-01-01", Weekday.Monday, 53)]
    [InlineData("2027-01-04", Weekday.Monday, 1)]
    [InlineData("2024-12-30", Weekday.Monday, 1)]
    // US: the week that holds 1 January is week 1.
    [InlineData("2026-01-01", Weekday.Sunday, 1)]
    [InlineData("2025-12-28", Weekday.Sunday, 1)]
    [InlineData("2026-01-04", Weekday.Sunday, 2)]
    [InlineData("2026-10-16", Weekday.Sunday, 42)]
    [InlineData("2026-12-27", Weekday.Sunday, 1)]
    [InlineData("2026-12-26", Weekday.Sunday, 52)]
    [InlineData("2027-01-01", Weekday.Saturday, 1)]
    [InlineData("2027-01-02", Weekday.Saturday, 2)]
    public void WeekNumberCases(string day, Weekday ws, int want) =>
        Assert.Equal(want, WeekNumber(F.Local(day, F.Utc), ws, F.Utc));

    [Theory]
    [InlineData(View.Week, "2026-10-16T09:00", Weekday.Monday, "October 2026", "12–18 October · week 42")]
    [InlineData(View.Week, "2026-10-01T09:00", Weekday.Monday, "September–October 2026", "28 September–4 October · week 40")]
    [InlineData(View.Week, "2026-12-30T09:00", Weekday.Monday, "December 2026–January 2027", "28 December 2026–3 January 2027 · week 53")]
    [InlineData(View.Week, "2026-10-16T09:00", Weekday.Sunday, "October 2026", "11–17 October · week 42")]
    [InlineData(View.Day, "2026-10-16T09:00", Weekday.Monday, "16 October 2026", "Friday · week 42")]
    [InlineData(View.Month, "2026-10-16T09:00", Weekday.Monday, "October 2026", "")]
    [InlineData(View.Month, "2026-06-01T09:00", Weekday.Monday, "June 2026", "")]
    [InlineData(View.Agenda, "2026-10-16T09:00", Weekday.Monday, "October 2026", "16 October–14 November 2026")]
    public void TitleAndSubtitle(View v, string anchor, Weekday ws, string title, string subtitle)
    {
        var env = F.Env(P);
        var r = ViewRange(v, F.Local(anchor), ws, P);
        Assert.Equal(title, Title(v, r, env));
        Assert.Equal(subtitle, Subtitle(v, r, ws, env));
    }

    [Fact]
    public void SpanAndKey()
    {
        var s = F.At(2026, 10, 16, 10);
        var o = F.Timed("ev", s, s.AddHours(1));
        Assert.Equal(Pair(s, s.AddHours(1)), Span(o, P));
        // Expressed in the zone (Go's In(loc)): the wall clock reads 10:00.
        Assert.Equal(10, Span(o, P)!.Value.Start.Hour);
        o = o with { End = null };
        var open = Span(o, P);
        Assert.NotNull(open);
        Assert.Equal(open.Value.Start, open.Value.End);
        // An end before the start is no end.
        Assert.Equal(Pair(s, s), Span(o with { End = s.AddHours(-1) }, P));

        var ad = F.Occ() with { AllDay = true, StartDate = new CalendarDate("2026-10-16") };
        Assert.Equal(Pair(F.At(2026, 10, 16), F.At(2026, 10, 17)), Span(ad, P));
        // An end not after the start is the next day.
        Assert.Equal(Pair(F.At(2026, 10, 16), F.At(2026, 10, 17)), Span(ad with { EndDate = new CalendarDate("2026-10-15") }, P));
        Assert.Equal(Pair(F.At(2026, 10, 16), F.At(2026, 10, 19)), Span(ad with { EndDate = new CalendarDate("2026-10-19") }, P));
        Assert.Null(Span(ad with { StartDate = new CalendarDate("16.10.") }, P));
        Assert.Null(Span(F.Occ(), P));

        var keyed = F.Occ() with { EventId = new CalendarEventId("ev"), RecurrenceId = new DateOrTime("2026-10-16T08:00:00Z") };
        Assert.Equal("ev|2026-10-16T08:00:00Z", OccurrenceKey(keyed));
        Assert.Equal("ev|", OccurrenceKey(keyed with { RecurrenceId = null }));
        var invitation = new MessageRef { AccountId = new AccountId("a"), MessageId = new MessageId("m") };
        Assert.Equal("inv:a/m", OccurrenceKey(F.Occ(invitation: invitation)));
        // An event id wins over the invitation.
        Assert.Equal("ev|", OccurrenceKey(F.Occ(invitation: invitation) with { EventId = new CalendarEventId("ev") }));
        Assert.Equal("|", OccurrenceKey(F.Occ()));

        var point = F.Timed("p", s, s);
        Assert.True(Overlaps(point, F.At(2026, 10, 16), F.At(2026, 10, 17), P));
        Assert.False(Overlaps(point, F.At(2026, 10, 17), F.At(2026, 10, 18), P));
        Assert.False(Overlaps(o, F.At(2026, 10, 17), F.At(2026, 10, 18), P));
        var hour = F.Timed("h", s, s.AddHours(1));
        Assert.True(Overlaps(hour, s.AddMinutes(59), s.AddHours(2), P));
        Assert.False(Overlaps(hour, s.AddHours(1), s.AddHours(2), P));
        Assert.False(Overlaps(F.Occ(), F.At(2026, 10, 16), F.At(2026, 10, 17), P));
    }

    private static (DateTimeOffset, DateTimeOffset)? Pair(DateTimeOffset start, DateTimeOffset end) => (start, end);

    [Fact]
    public void ParseDateIsGosLayout()
    {
        Assert.Equal(F.At(2026, 10, 16), ParseDate(new CalendarDate("2026-10-16"), P));
        Assert.Equal(F.At(2028, 2, 29), ParseDate(new CalendarDate("2028-02-29"), P));
        foreach (var bad in new[] { "", "2026-1-16", "2026-10-16T00:00", "2026/10/16", "2026-13-01", "2026-02-29", "2026-10-00", "0000-01-01", "２０２６-10-16" })
        {
            Assert.Null(ParseDate(new CalendarDate(bad), P));
        }
        Assert.Null(ParseDate(null, P));
        Assert.Equal(new CalendarDate("2026-10-16"), DateOf(F.At(2026, 10, 16, 23, 59), P));
        // 23:30 in Prague on 16 October is 21:30 UTC, still the 16th there.
        Assert.Equal(new CalendarDate("2026-10-16"), DateOf(F.At(2026, 10, 16, 23, 30), F.Utc));
        Assert.Equal(new CalendarDate("2026-10-15"), DateOf(F.At(2026, 10, 16, 1, 0), F.Utc));
    }

    [Theory]
    [InlineData(View.Week, View.Agenda, "2026-10-16")]
    [InlineData(View.Day, View.Agenda, "2026-10-16")]
    [InlineData(View.Month, View.Agenda, "2026-10-16")]
    [InlineData(View.Agenda, View.Agenda, "2026-12-03")]
    [InlineData(View.Agenda, View.Week, "2026-12-03")]
    [InlineData(View.Agenda, View.Month, "2026-12-03")]
    [InlineData(View.Week, View.Month, "2026-12-03")]
    [InlineData(View.Month, View.Day, "2026-12-03")]
    [InlineData(View.Day, View.Week, "2026-12-03")]
    public void SwitchAnchorCases(View from, View to, string want)
    {
        var today = F.At(2026, 10, 16, 15, 30);
        var anchor = F.At(2026, 12, 3);
        Assert.Equal(F.Local(want), SwitchAnchor(from, to, anchor, today, P));
    }
}
