// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CalendarHelpersTests.swift (CalFixture);
// GTK: ui/internal/calendar/helpers_test.go (en, prague, envIn, at, timed,
// allDayOcc, ptr).
//
// The fixtures of the calendar tests: Europe/Prague (a real zone with its
// daylight-saving days, through ICU on Windows) and UTC, the English env,
// times made from wall-clock parts by .NET's own conversion (so that the
// fixtures do not rest on Cal.Date, which the tests check), and bare
// occurrences. Partial: the later ports add their fixtures (events,
// invitation cards) beside these. The catalogue is English in tests, so
// msgids come back verbatim (Go's en translator).

using System;
using System.Globalization;
using Malachi.Core.Api;
using Malachi.Core.Calendars;

namespace Malachi.Core.Tests.Calendars;

/// <summary>Fixtures shared by the calendar's tests.</summary>
internal static partial class CalFixture
{
    /// <summary>Europe/Prague.</summary>
    public static readonly TimeZoneInfo Prague = TimeZoneInfo.FindSystemTimeZoneById("Europe/Prague");

    /// <summary>UTC.</summary>
    public static readonly TimeZoneInfo Utc = TimeZoneInfo.Utc;

    /// <summary>The English env in <paramref name="zone"/>, Prague by default (Go <c>envIn</c>).</summary>
    public static Cal.Env Env(TimeZoneInfo? zone = null) => Cal.Env.English(zone ?? Prague);

    /// <summary>
    /// A wall-clock time in <paramref name="zone"/> (Go <c>at</c>), made with
    /// the zone's own offset; never a repeated or skipped time.
    /// </summary>
    public static DateTimeOffset At(TimeZoneInfo zone, int y, int m, int d, int h, int mi)
    {
        var wall = new DateTime(y, m, d, h, mi, 0, DateTimeKind.Unspecified);
        return new DateTimeOffset(wall, zone.GetUtcOffset(wall));
    }

    /// <summary>A Prague time.</summary>
    public static DateTimeOffset At(int y, int m, int d, int h = 0, int mi = 0) => At(Prague, y, m, d, h, mi);

    /// <summary>A UTC instant.</summary>
    public static DateTimeOffset UtcAt(int y, int m, int d, int h, int mi) => new(y, m, d, h, mi, 0, TimeSpan.Zero);

    /// <summary>
    /// A wall-clock time written "2026-10-16" or "2026-10-16T15:30" in
    /// <paramref name="zone"/>, Prague by default: the cells of the table tests.
    /// </summary>
    public static DateTimeOffset Local(string text, TimeZoneInfo? zone = null)
    {
        var t = DateTime.ParseExact(
            text, ["yyyy-MM-dd", "yyyy-MM-dd'T'HH:mm"], CultureInfo.InvariantCulture, DateTimeStyles.None);
        return At(zone ?? Prague, t.Year, t.Month, t.Day, t.Hour, t.Minute);
    }

    /// <summary>A timed occurrence with an id (Go <c>timed</c>).</summary>
    public static Occurrence Timed(string id, DateTimeOffset start, DateTimeOffset end) => new()
    {
        EventId = new CalendarEventId(id),
        AccountId = new AccountId("acc"),
        Title = id,
        Start = start.ToUniversalTime(),
        End = end.ToUniversalTime(),
        AllDay = false,
        Status = EventStatus.Confirmed,
        ShowAs = ShowAs.Busy,
    };

    /// <summary>An all-day occurrence from <paramref name="start"/> for <paramref name="days"/> days (Go <c>allDayOcc</c>).</summary>
    public static Occurrence AllDayOcc(string id, DateTimeOffset start, int days, TimeZoneInfo? zone = null)
    {
        zone ??= Prague;
        return new Occurrence
        {
            EventId = new CalendarEventId(id),
            AccountId = new AccountId("acc"),
            Title = id,
            StartDate = Cal.DateOf(start, zone),
            EndDate = Cal.DateOf(Cal.AddDays(Cal.StartOfDay(start, zone), days, zone), zone),
            AllDay = true,
            Status = EventStatus.Confirmed,
            ShowAs = ShowAs.Free,
        };
    }

    /// <summary>A bare occurrence (Go's zero value plus what a case sets).</summary>
    public static Occurrence Occ(
        string status = "", string showAs = "", EventResponse? myResponse = null, MessageRef? invitation = null) => new()
        {
            AccountId = new AccountId(""),
            Title = "",
            AllDay = false,
            Status = new EventStatus(status),
            ShowAs = new ShowAs(showAs),
            MyResponse = myResponse,
            Invitation = invitation,
        };

    /// <summary>The hours of an interval.</summary>
    public static double Hours(DateTimeOffset a, DateTimeOffset b) => (b - a).TotalHours;
}
