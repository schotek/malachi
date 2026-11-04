// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Calendar/Calendar.swift; GTK:
// ui/internal/calendar/format.go (Env, Strftime, EnglishStrftime) and
// view.go (View, Views, DefaultView, AgendaDays, ParseView, Range,
// StartOfDay, AddDays, SameDay, DaysBetween, StartOfWeek, ViewRange, Step,
// SwitchAnchor, WeekStart, WeekNumber, Title, Subtitle, WeekLabel,
// ParseDate, DateOf, Span, OccurrenceKey, Overlaps), and the helpers the
// Swift file shares with the other calendar files (cleanTitle, cleanLine,
// nameOf, trimSpace, Go's cmp.Compare, cmp.Or and slices.SortStableFunc).
// View.Nick is CalViewNickExtensions, Weekday.FromMonday
// CalWeekdayExtensions.
//
// The calendar's pure UI logic (docs/calendar.md §5.1): the views and their
// ranges, the day arithmetic, how an occurrence spans a zone, and the
// strftime writers the date texts go through. Every other calendar file
// is a partial of Cal (CalendarText.cs, CalendarMonthGrid.cs, MiniMonth.cs,
// CalendarActive.cs, and those the later ports add).
//
// Swift's namespace enum Cal is this class; the namespace is Calendars,
// because a namespace or class Calendar would hide
// System.Globalization.Calendar and the API's Calendar record. Go's
// time.Time carries its zone; a DateTimeOffset carries only an offset, so
// every function that needs a day takes the zone explicitly, as Swift's
// do, and TimeZoneInfo.Local is never read here: the caller passes it.
// Days are midnights made from wall-clock parts in that zone (Date, Go's
// time.Date), so a day of 23 or 25 hours (a daylight-saving change) is
// still one day. Every instant these functions return is expressed in the
// offset its zone has at that instant (Go's t.In(loc)); DateTimeOffset
// compares instants, so the offset never changes an equality, but read
// wall-clock parts with PartsOf and the zone, never from the offset.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Text;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.I18n;

namespace Malachi.Core.Calendars;

/// <summary>The calendar's pure logic and texts (Swift's <c>Cal</c>, Go's package <c>calendar</c>).</summary>
public static partial class Cal
{
    // ------------------------------------------------------------------
    // Weekday
    // ------------------------------------------------------------------

    /// <summary>Go's <c>time.Weekday</c>: Sunday is 0 (as .NET's <see cref="DayOfWeek"/>).</summary>
    public enum Weekday
    {
        /// <summary>Sunday, 0.</summary>
        Sunday,

        /// <summary>Monday.</summary>
        Monday,

        /// <summary>Tuesday.</summary>
        Tuesday,

        /// <summary>Wednesday.</summary>
        Wednesday,

        /// <summary>Thursday.</summary>
        Thursday,

        /// <summary>Friday.</summary>
        Friday,

        /// <summary>Saturday, 6.</summary>
        Saturday,
    }

    /// <summary>The weekday of .NET's <see cref="DayOfWeek"/> (Swift <c>Weekday(firstWeekday:)</c> for Foundation's).</summary>
    public static Weekday WeekdayOf(DayOfWeek day) => (Weekday)(((int)day % 7 + 7) % 7);

    /// <summary>
    /// The first day of the week of <paramref name="culture"/>'s regional
    /// format: the <c>localeFirstDay</c> of <see cref="WeekStart"/>.
    /// </summary>
    public static Weekday LocaleFirstDay(CultureInfo culture)
    {
        ArgumentNullException.ThrowIfNull(culture);
        return WeekdayOf(culture.DateTimeFormat.FirstDayOfWeek);
    }

    // ------------------------------------------------------------------
    // Env and strftime (format.go)
    // ------------------------------------------------------------------

    /// <summary>
    /// format.go <c>Strftime</c>: writes <paramref name="t"/> in
    /// <paramref name="zone"/> with a strftime format (a msgid already
    /// translated). The production writer is <see cref="CultureStrftime"/>,
    /// the tests' and the samples' <see cref="EnglishStrftime"/>.
    /// </summary>
    public delegate string StrftimeWriter(DateTimeOffset t, string format, TimeZoneInfo zone);

    /// <summary>
    /// format.go <c>Env</c>: what the texts with dates are written with. The
    /// translator is the static <see cref="L10n"/>, as for the board;
    /// <see cref="Zone"/> decides what a day is.
    /// </summary>
    public sealed record Env
    {
        /// <summary>An env that writes with <paramref name="format"/> in <paramref name="zone"/>.</summary>
        public Env(TimeZoneInfo zone, StrftimeWriter format)
        {
            ArgumentNullException.ThrowIfNull(zone);
            ArgumentNullException.ThrowIfNull(format);
            Zone = zone;
            Format = format;
        }

        /// <summary>The zone that decides what a day is (the caller passes <see cref="TimeZoneInfo.Local"/>).</summary>
        public TimeZoneInfo Zone { get; init; }

        /// <summary>The strftime writer.</summary>
        public StrftimeWriter Format { get; init; }

        /// <summary>
        /// The user's regional format (Swift <c>Env(zone:locale:)</c>): the
        /// names of <paramref name="culture"/> through <see cref="CultureStrftime"/>.
        /// </summary>
        public static Env ForCulture(TimeZoneInfo zone, CultureInfo culture) => new(zone, CultureStrftime(culture));

        /// <summary>The C-locale writer of the tests and the samples (Swift <c>Env.english</c>).</summary>
        public static Env English(TimeZoneInfo zone) => new(zone, EnglishStrftime);

        /// <summary>Writes <paramref name="t"/> in the env's zone with a translated format (Go's <c>env.date</c>).</summary>
        public string Date(DateTimeOffset t, string format) => Format(t, format, Zone);
    }

    private static readonly string[] EnglishDays = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];

    private static readonly string[] EnglishMonths =
    [
        "January", "February", "March", "April", "May", "June", "July",
        "August", "September", "October", "November", "December",
    ];

    /// <summary>
    /// format.go <c>EnglishStrftime</c>: <paramref name="t"/> in
    /// <paramref name="zone"/> with <paramref name="format"/> in the C locale
    /// (English names). It knows %a %A %b %B %OB (the standalone month name,
    /// in English the same as %B) %d %e %H %I %M %m %p %Y %y %% and the "-"
    /// flag that drops the padding (%-d, %-H, %-I, %-m); anything else is
    /// copied. As Swift's, a C length modifier a catalogue generator may have
    /// added ("%ld", "%-ld", at most two) is read as the directive after it.
    /// </summary>
    public static string EnglishStrftime(DateTimeOffset t, string format, TimeZoneInfo zone)
    {
        ArgumentNullException.ThrowIfNull(format);
        ArgumentNullException.ThrowIfNull(zone);
        var p = PartsOf(t, zone);
        var b = new StringBuilder(format.Length + 16);
        for (var i = 0; i < format.Length; i++)
        {
            var c = format[i];
            if (c != '%' || i + 1 == format.Length)
            {
                b.Append(c);
                continue;
            }
            i++;
            if (format[i] == 'O' && i + 1 < format.Length && format[i + 1] == 'B')
            {
                // %OB: the standalone month name; the same in English.
                b.Append(EnglishMonths[p.Month - 1]);
                i++;
                continue;
            }
            var pad = true;
            if (format[i] == '-' && i + 1 < format.Length)
            {
                pad = false;
                i++;
            }
            for (var lengths = 0; lengths < 2 && format[i] == 'l' && i + 1 < format.Length; lengths++)
            {
                i++;
            }
            switch (format[i])
            {
                case 'a':
                    b.Append(EnglishDays[(int)p.Weekday], 0, 3);
                    break;
                case 'A':
                    b.Append(EnglishDays[(int)p.Weekday]);
                    break;
                case 'b':
                    b.Append(EnglishMonths[p.Month - 1], 0, 3);
                    break;
                case 'B':
                    b.Append(EnglishMonths[p.Month - 1]);
                    break;
                case 'd':
                    Number(b, p.Day, pad);
                    break;
                case 'e':
                    b.Append(pad ? p.Day.ToString("D", CultureInfo.InvariantCulture).PadLeft(2) : p.Day.ToString(CultureInfo.InvariantCulture));
                    break;
                case 'H':
                    Number(b, p.Hour, pad);
                    break;
                case 'I':
                    Number(b, p.Hour % 12 == 0 ? 12 : p.Hour % 12, pad);
                    break;
                case 'M':
                    Number(b, p.Minute, pad);
                    break;
                case 'm':
                    Number(b, p.Month, pad);
                    break;
                case 'p':
                    b.Append(p.Hour < 12 ? "AM" : "PM");
                    break;
                case 'Y':
                    b.Append(p.Year.ToString(CultureInfo.InvariantCulture));
                    break;
                case 'y':
                    b.Append((p.Year % 100).ToString("D2", CultureInfo.InvariantCulture));
                    break;
                case '%':
                    b.Append('%');
                    break;
                default:
                    b.Append('%');
                    if (!pad)
                    {
                        b.Append('-');
                    }
                    b.Append(format[i]);
                    break;
            }
        }
        return b.ToString();
    }

    private static void Number(StringBuilder b, int n, bool pad) =>
        b.Append(n.ToString(pad ? "D2" : "D", CultureInfo.InvariantCulture));

    /// <summary>
    /// The Windows writer (the production <see cref="Env"/>): the strftime
    /// msgids through <see cref="Strftime.Format"/> with the day and month
    /// names of <paramref name="culture"/> (the regional format, as LC_TIME
    /// is for GLib; months with a day in the genitive, as GLib's %B). What
    /// <see cref="Strftime"/> lacks is met here, as Swift's
    /// <c>Strftime.toDateFormat</c> does: %OB is the standalone (nominative)
    /// month name ("říjen 2026", not the genitive "října" of %B), written in
    /// as text; a C length modifier ("%-ld", at most two l) is dropped.
    /// </summary>
    public static StrftimeWriter CultureStrftime(CultureInfo culture)
    {
        ArgumentNullException.ThrowIfNull(culture);
        return (t, format, zone) => Strftime.Format(t, WithStandaloneMonths(t, format, zone, culture), culture, zone);
    }

    // The format with %OB written in as the culture's standalone month name
    // (escaped, so its letters stay text) and the C length modifiers gone.
    private static string WithStandaloneMonths(DateTimeOffset t, string format, TimeZoneInfo zone, CultureInfo culture)
    {
        ArgumentNullException.ThrowIfNull(format);
        if (!format.Contains('%', StringComparison.Ordinal))
        {
            return format;
        }
        var b = new StringBuilder(format.Length + 16);
        for (var i = 0; i < format.Length; i++)
        {
            var c = format[i];
            if (c != '%' || i + 1 == format.Length)
            {
                b.Append(c);
                continue;
            }
            i++;
            if (format[i] == 'O' && i + 1 < format.Length && format[i + 1] == 'B')
            {
                b.Append(StandaloneMonth(t, zone, culture).Replace("%", "%%", StringComparison.Ordinal));
                i++;
                continue;
            }
            b.Append('%');
            if (format[i] == '-' && i + 1 < format.Length)
            {
                b.Append('-');
                i++;
            }
            for (var lengths = 0; lengths < 2 && format[i] == 'l' && i + 1 < format.Length; lengths++)
            {
                i++;
            }
            b.Append(format[i]);
        }
        return b.ToString();
    }

    // The nominative month name of t's month in zone, in the culture's own
    // calendar (MonthNames: ICU's standalone names, which Strftime.FormatInfo
    // replaces by the genitive ones for %B).
    private static string StandaloneMonth(DateTimeOffset t, TimeZoneInfo zone, CultureInfo culture)
    {
        var info = culture.DateTimeFormat;
        var local = ToZone(t, zone).DateTime;
        int month;
        try
        {
            month = info.Calendar.GetMonth(local);
        }
        catch (ArgumentOutOfRangeException)
        {
            month = local.Month;
        }
        var names = info.MonthNames;
        return month >= 1 && month <= names.Length ? names[month - 1] : "";
    }

    // ------------------------------------------------------------------
    // Wall-clock arithmetic (Go's time.Date and Time.In)
    // ------------------------------------------------------------------

    /// <summary>The wall-clock parts of an instant in a zone.</summary>
    /// <param name="Year">The year.</param>
    /// <param name="Month">1 to 12.</param>
    /// <param name="Day">1 to 31.</param>
    /// <param name="Hour">0 to 23.</param>
    /// <param name="Minute">0 to 59.</param>
    /// <param name="Second">0 to 59.</param>
    /// <param name="Weekday">The day of the week.</param>
    public readonly record struct Parts(int Year, int Month, int Day, int Hour, int Minute, int Second, Weekday Weekday);

    /// <summary>The wall-clock parts of <paramref name="t"/> in <paramref name="zone"/> (Swift <c>parts</c>).</summary>
    public static Parts PartsOf(DateTimeOffset t, TimeZoneInfo zone)
    {
        var l = ToZone(t, zone);
        return new Parts(l.Year, l.Month, l.Day, l.Hour, l.Minute, l.Second, WeekdayOf(l.DayOfWeek));
    }

    /// <summary><paramref name="t"/> expressed in <paramref name="zone"/>'s offset (Go's <c>t.In(loc)</c>).</summary>
    public static DateTimeOffset ToZone(DateTimeOffset t, TimeZoneInfo zone)
    {
        ArgumentNullException.ThrowIfNull(zone);
        try
        {
            return TimeZoneInfo.ConvertTime(t, zone);
        }
        catch (ArgumentOutOfRangeException)
        {
            // At the ends of DateTimeOffset's range: keep it as it is.
            return t;
        }
    }

    /// <summary>Days since 1970-01-01 of a proleptic Gregorian date (H. Hinnant).</summary>
    public static int DaysFromCivil(int year, int month, int day)
    {
        var y = month <= 2 ? year - 1 : year;
        var era = (y >= 0 ? y : y - 399) / 400;
        var yoe = y - era * 400;
        var doy = (153 * (month > 2 ? month - 3 : month + 9) + 2) / 5 + day - 1;
        var doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
        return era * 146_097 + doe - 719_468;
    }

    /// <summary>The date of a day number of <see cref="DaysFromCivil"/>.</summary>
    public static (int Year, int Month, int Day) CivilFromDays(int days)
    {
        var z = days + 719_468;
        var era = (z >= 0 ? z : z - 146_096) / 146_097;
        var doe = z - era * 146_097;
        var yoe = (doe - doe / 1460 + doe / 36524 - doe / 146_096) / 365;
        var y = yoe + era * 400;
        var doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
        var mp = (5 * doy + 2) / 153;
        var d = doy - (153 * mp + 2) / 5 + 1;
        var m = mp < 10 ? mp + 3 : mp - 9;
        return (m <= 2 ? y + 1 : y, m, d);
    }

    private static (int Quotient, int Remainder) FloorDiv(int a, int b)
    {
        var q = a / b;
        var r = a % b;
        if (r < 0)
        {
            q--;
            r += b;
        }
        return (q, r);
    }

    /// <summary>Go's <c>time.Date</c> at midnight: see <see cref="Date(int, int, int, int, int, int, TimeZoneInfo)"/>.</summary>
    public static DateTimeOffset Date(int year, int month, int day, TimeZoneInfo zone) => Date(year, month, day, 0, 0, 0, zone);

    /// <summary>Go's <c>time.Date</c> without seconds: see <see cref="Date(int, int, int, int, int, int, TimeZoneInfo)"/>.</summary>
    public static DateTimeOffset Date(int year, int month, int day, int hour, int minute, TimeZoneInfo zone) =>
        Date(year, month, day, hour, minute, 0, zone);

    /// <summary>
    /// Go's <c>time.Date</c>: the instant of a wall-clock time in
    /// <paramref name="zone"/>, every part normalised the way Go does (month
    /// 13 is January of the next year, day 0 the last day of the month
    /// before, minute 60 the next hour). A time the clocks skip is moved on
    /// by the gap; a repeated one is its first instance (Swift's rule; Go
    /// leaves both unspecified). A date outside DateTimeOffset's years is
    /// its nearest end.
    /// </summary>
    public static DateTimeOffset Date(int year, int month, int day, int hour, int minute, int second, TimeZoneInfo zone)
    {
        ArgumentNullException.ThrowIfNull(zone);
        var ym = FloorDiv(year * 12 + (month - 1), 12);
        var (carry, s) = FloorDiv(second, 60);
        (carry, var mi) = FloorDiv(minute + carry, 60);
        (carry, var h) = FloorDiv(hour + carry, 24);
        var (y, m, d) = CivilFromDays(DaysFromCivil(ym.Quotient, ym.Remainder + 1, 1) + day - 1 + carry);
        if (y < 1)
        {
            return DateTimeOffset.MinValue;
        }
        if (y > 9999)
        {
            return DateTimeOffset.MaxValue;
        }
        return FromWallClock(new DateTime(y, m, d, h, mi, s, DateTimeKind.Unspecified), zone);
    }

    // The instant a valid wall-clock time names in zone: of the offsets
    // around it (a day before, at it read as UTC, a day after) those it is
    // valid with, the earliest instant (the first of a repeated time); in a
    // gap none is valid, and the offset before the gap moves it on by the
    // gap (Go's result).
    private static DateTimeOffset FromWallClock(DateTime wall, TimeZoneInfo zone)
    {
        var asUtc = new DateTimeOffset(wall, TimeSpan.Zero);
        var before = OffsetAt(zone, asUtc, -1);
        DateTimeOffset? best = null;
        foreach (var offset in new[] { before, OffsetAt(zone, asUtc, 0), OffsetAt(zone, asUtc, 1) })
        {
            if (!TrySubtract(asUtc, offset, out var candidate) || OffsetAt(zone, candidate, 0) != offset)
            {
                continue;
            }
            if (best is null || candidate < best.Value)
            {
                best = candidate;
            }
        }
        if (best is null && TrySubtract(asUtc, before, out var moved))
        {
            best = moved;
        }
        return ToZone(best ?? asUtc, zone);
    }

    // The zone's offset at the instant days days from t (clamped to the range).
    private static TimeSpan OffsetAt(TimeZoneInfo zone, DateTimeOffset t, int days)
    {
        var ticks = t.UtcTicks + days * TimeSpan.TicksPerDay;
        ticks = Math.Clamp(ticks, DateTimeOffset.MinValue.UtcTicks, DateTimeOffset.MaxValue.UtcTicks);
        return zone.GetUtcOffset(new DateTimeOffset(ticks, TimeSpan.Zero));
    }

    private static bool TrySubtract(DateTimeOffset t, TimeSpan offset, out DateTimeOffset result)
    {
        var ticks = t.UtcTicks - offset.Ticks;
        if (ticks < DateTimeOffset.MinValue.UtcTicks || ticks > DateTimeOffset.MaxValue.UtcTicks)
        {
            result = default;
            return false;
        }
        result = new DateTimeOffset(ticks, TimeSpan.Zero);
        return true;
    }

    /// <summary>view.go <c>StartOfDay</c>: the midnight of <paramref name="t"/>'s day in <paramref name="zone"/>.</summary>
    public static DateTimeOffset StartOfDay(DateTimeOffset t, TimeZoneInfo zone)
    {
        var p = PartsOf(t, zone);
        return Date(p.Year, p.Month, p.Day, zone);
    }

    /// <summary>view.go <c>AddDays</c>: the midnight <paramref name="n"/> calendar days after <paramref name="day"/>'s.</summary>
    public static DateTimeOffset AddDays(DateTimeOffset day, int n, TimeZoneInfo zone)
    {
        var p = PartsOf(day, zone);
        return Date(p.Year, p.Month, p.Day + n, zone);
    }

    /// <summary>view.go <c>SameDay</c>: <paramref name="a"/> and <paramref name="b"/> on one calendar day in <paramref name="zone"/>.</summary>
    public static bool SameDay(DateTimeOffset a, DateTimeOffset b, TimeZoneInfo zone) => StartOfDay(a, zone) == StartOfDay(b, zone);

    /// <summary>view.go <c>DaysBetween</c>: the calendar days from <paramref name="a"/>'s day to <paramref name="b"/>'s in <paramref name="zone"/>.</summary>
    public static int DaysBetween(DateTimeOffset a, DateTimeOffset b, TimeZoneInfo zone)
    {
        var x = PartsOf(a, zone);
        var y = PartsOf(b, zone);
        return DaysFromCivil(y.Year, y.Month, y.Day) - DaysFromCivil(x.Year, x.Month, x.Day);
    }

    /// <summary>view.go <c>StartOfWeek</c>: the midnight of the first day of <paramref name="t"/>'s week.</summary>
    public static DateTimeOffset StartOfWeek(DateTimeOffset t, Weekday weekStart, TimeZoneInfo zone)
    {
        var d = StartOfDay(t, zone);
        var back = ((int)PartsOf(d, zone).Weekday - (int)weekStart + 7) % 7;
        return AddDays(d, -back, zone);
    }

    // ------------------------------------------------------------------
    // Views and ranges (view.go)
    // ------------------------------------------------------------------

    /// <summary>
    /// view.go <c>View</c>: what the calendar page shows (the key
    /// calendar-view). The members' names in lower case are the gschema's
    /// nicks, in its order (<see cref="CalViewNickExtensions"/>).
    /// </summary>
    public enum View
    {
        /// <summary>One day.</summary>
        Day,

        /// <summary>Seven days side by side.</summary>
        Week,

        /// <summary>The month's 6×7 grid.</summary>
        Month,

        /// <summary>A list of the coming events by day, <see cref="AgendaDays"/> a page.</summary>
        Agenda,
    }

    /// <summary>The views in the toolbar's order.</summary>
    public static IReadOnlyList<View> Views { get; } = [View.Day, View.Week, View.Month, View.Agenda];

    /// <summary>The key calendar-view's default.</summary>
    public const View DefaultView = View.Week;

    /// <summary>The span of an agenda page (it scrolls on in pages of this many days).</summary>
    public const int AgendaDays = 30;

    /// <summary>view.go <c>ParseView</c>: the view a stored nick names; an unknown one is the default with false.</summary>
    public static (View View, bool Known) ParseView(string? nick)
    {
        foreach (var v in Views)
        {
            if (string.Equals(v.Nick, nick, StringComparison.Ordinal))
            {
                return (v, true);
            }
        }
        return (DefaultView, false);
    }

    /// <summary>
    /// view.go <c>Range</c>: the span of days a view shows, [From, To), both
    /// midnights in <paramref name="Zone"/> (Swift's carries the zone, as a
    /// Go time does).
    /// </summary>
    /// <param name="From">The first day's midnight.</param>
    /// <param name="To">The midnight after the last day.</param>
    /// <param name="Zone">The zone the days are in.</param>
    public sealed record Range(DateTimeOffset From, DateTimeOffset To, TimeZoneInfo Zone)
    {
        /// <summary>The midnights of the range.</summary>
        public IReadOnlyList<DateTimeOffset> Days()
        {
            var output = new List<DateTimeOffset>();
            for (var d = From; d < To; d = AddDays(d, 1, Zone))
            {
                if (output.Count > 0 && d <= output[^1])
                {
                    break; // the end of DateTimeOffset's range
                }
                output.Add(d);
            }
            return output;
        }

        /// <summary><paramref name="t"/> within [From, To).</summary>
        public bool Contains(DateTimeOffset t) => t >= From && t < To;
    }

    /// <summary>
    /// view.go <c>ViewRange</c>: the range <paramref name="v"/> shows around
    /// <paramref name="anchor"/>: the day, the week (from
    /// <paramref name="weekStart"/>), the month's 6×7 grid
    /// (<see cref="MonthCells"/>), or <see cref="AgendaDays"/> days from the
    /// anchor's day.
    /// </summary>
    public static Range ViewRange(View v, DateTimeOffset anchor, Weekday weekStart, TimeZoneInfo zone)
    {
        switch (v)
        {
            case View.Day:
                {
                    var d = StartOfDay(anchor, zone);
                    return new Range(d, AddDays(d, 1, zone), zone);
                }
            case View.Month:
                {
                    var first = GridStart(anchor, weekStart, zone);
                    return new Range(first, AddDays(first, 42, zone), zone);
                }
            case View.Agenda:
                {
                    var d = StartOfDay(anchor, zone);
                    return new Range(d, AddDays(d, AgendaDays, zone), zone);
                }
            default:
                {
                    var w = StartOfWeek(anchor, weekStart, zone);
                    return new Range(w, AddDays(w, 7, zone), zone);
                }
        }
    }

    /// <summary>
    /// view.go <c>Step</c>: the anchor after ‹ (<paramref name="dir"/> -1) or
    /// › (+1): a day, a week, a month (the day of the month clamped to the
    /// new month's length), or an agenda page.
    /// </summary>
    public static DateTimeOffset Step(View v, DateTimeOffset anchor, int dir, TimeZoneInfo zone)
    {
        var d = StartOfDay(anchor, zone);
        switch (v)
        {
            case View.Day:
                return AddDays(d, dir, zone);
            case View.Month:
                {
                    var p = PartsOf(d, zone);
                    var first = PartsOf(Date(p.Year, p.Month + dir, 1, zone), zone);
                    var last = PartsOf(AddDays(Date(first.Year, first.Month + 1, 1, zone), -1, zone), zone).Day;
                    return Date(first.Year, first.Month, Math.Min(p.Day, last), zone);
                }
            case View.Agenda:
                return AddDays(d, dir * AgendaDays, zone);
            default:
                return AddDays(d, 7 * dir, zone);
        }
    }

    /// <summary>
    /// view.go <c>SwitchAnchor</c>: the anchor after switching from view
    /// <paramref name="from"/> to <paramref name="to"/>. Switching to the
    /// Agenda starts it at today (the midnight of <paramref name="today"/>
    /// in <paramref name="zone"/>), whatever day the other view showed (the
    /// owner's decision of 2026-10-16); the Agenda moves only with ‹ ›,
    /// Today or a date the user picks while it shows. Switching from the
    /// Agenda keeps its anchor, and every other switch keeps the anchor.
    /// </summary>
    public static DateTimeOffset SwitchAnchor(View from, View to, DateTimeOffset anchor, DateTimeOffset today, TimeZoneInfo zone) =>
        to == View.Agenda && from != View.Agenda ? StartOfDay(today, zone) : anchor;

    /// <summary>
    /// view.go <c>WeekStart</c>: the first day of the week: the preference,
    /// or <paramref name="localeFirstDay"/> (<see cref="LocaleFirstDay"/>)
    /// for <c>locale</c> and an unknown value.
    /// </summary>
    public static Weekday WeekStart(Api.WeekStart pref, Weekday localeFirstDay) => pref.Value switch
    {
        Api.WeekStart.Monday => Weekday.Monday,
        Api.WeekStart.Sunday => Weekday.Sunday,
        Api.WeekStart.Saturday => Weekday.Saturday,
        _ => localeFirstDay,
    };

    /// <summary>
    /// view.go <c>WeekNumber</c>: the number of <paramref name="day"/>'s
    /// week: ISO 8601 when weeks start on Monday; otherwise the US rule (week
    /// 1 is the week, from <paramref name="weekStart"/>, that holds 1
    /// January; a week across the new year is week 1 of the new year).
    /// </summary>
    public static int WeekNumber(DateTimeOffset day, Weekday weekStart, TimeZoneInfo zone)
    {
        var d = StartOfDay(day, zone);
        if (weekStart == Weekday.Monday)
        {
            var p = PartsOf(d, zone);
            return ISOWeek.GetWeekOfYear(new DateTime(p.Year, p.Month, p.Day, 0, 0, 0, DateTimeKind.Unspecified));
        }
        var first = StartOfWeek(d, weekStart, zone);
        var last = AddDays(first, 6, zone);
        var jan1 = Date(PartsOf(last, zone).Year, 1, 1, zone);
        var week1 = StartOfWeek(jan1, weekStart, zone);
        return DaysBetween(week1, first, zone) / 7 + 1;
    }

    /// <summary>
    /// view.go <c>Title</c>: the toolbar's title of a view's range: the
    /// day's date (Day), the month and year (Week, Month, Agenda; a week
    /// across two months names both).
    /// </summary>
    public static string Title(View v, Range r, Env env)
    {
        ArgumentNullException.ThrowIfNull(r);
        ArgumentNullException.ThrowIfNull(env);
        switch (v)
        {
            case View.Day:
                return Text.DayMonthYearText(r.From, env);
            case View.Month:
                return Text.MonthYearText(AddDays(r.From, 14, r.Zone), env);
            case View.Agenda:
                return Text.MonthYearText(r.From, env);
        }
        var last = AddDays(r.To, -1, r.Zone);
        var a = PartsOf(r.From, r.Zone);
        var b = PartsOf(last, r.Zone);
        if (a.Month == b.Month && a.Year == b.Year)
        {
            return Text.MonthYearText(r.From, env);
        }
        if (a.Year == b.Year)
        {
            return Text.RangeText(Text.MonthText(r.From, env), Text.MonthYearText(last, env));
        }
        return Text.RangeText(Text.MonthYearText(r.From, env), Text.MonthYearText(last, env));
    }

    /// <summary>
    /// view.go <c>Subtitle</c>: the toolbar's subtitle: the weekday and week
    /// (Day), the days with the week number (Week: "12–18 October · week
    /// 42"), the span of an agenda page; "" for Month.
    /// </summary>
    public static string Subtitle(View v, Range r, Weekday weekStart, Env env)
    {
        ArgumentNullException.ThrowIfNull(r);
        ArgumentNullException.ThrowIfNull(env);
        var zone = env.Zone;
        switch (v)
        {
            case View.Day:
                return Text.Dot(Text.WeekdayText(r.From, env), WeekLabel(WeekNumber(r.From, weekStart, zone)));
            case View.Month:
                return "";
            case View.Agenda:
                return Text.RangeText(Text.DayMonthText(r.From, env), Text.DayMonthYearText(AddDays(r.To, -1, r.Zone), env));
        }
        var last = AddDays(r.To, -1, r.Zone);
        var a = PartsOf(r.From, r.Zone);
        var b = PartsOf(last, r.Zone);
        string days;
        if (a.Month == b.Month && a.Year == b.Year)
        {
            days = Text.RangeText(Text.DayText(r.From, env), Text.DayMonthText(last, env));
        }
        else if (a.Year == b.Year)
        {
            days = Text.RangeText(Text.DayMonthText(r.From, env), Text.DayMonthText(last, env));
        }
        else
        {
            days = Text.RangeText(Text.DayMonthYearText(r.From, env), Text.DayMonthYearText(last, env));
        }
        return Text.Dot(days, WeekLabel(WeekNumber(r.From, weekStart, zone)));
    }

    /// <summary>view.go <c>WeekLabel</c>: "week 42" (the subtitle, the month grid's week column tooltip).</summary>
    public static string WeekLabel(int n) =>
        // TRANSLATORS: the number of a week of the year, in the calendar's
        // subtitle "12–18 October · week 42".
        L10n.T("week %d", n);

    // ------------------------------------------------------------------
    // Occurrences in a zone (view.go)
    // ------------------------------------------------------------------

    /// <summary>
    /// view.go <c>ParseDate</c>: the midnight of an API date
    /// (<c>2026-10-14</c>, exactly Go's layout <c>2006-01-02</c>) in
    /// <paramref name="zone"/>; null for anything else.
    /// </summary>
    public static DateTimeOffset? ParseDate(CalendarDate? d, TimeZoneInfo zone)
    {
        var s = d?.Value;
        if (s is not { Length: 10 } || s[4] != '-' || s[7] != '-'
            || !TryDigits(s, 0, 4, out var year) || !TryDigits(s, 5, 2, out var month) || !TryDigits(s, 8, 2, out var day)
            || year < 1 || month is < 1 or > 12 || day < 1 || day > DateTime.DaysInMonth(year, month))
        {
            return null;
        }
        return Date(year, month, day, zone);
    }

    private static bool TryDigits(string s, int start, int length, out int value)
    {
        value = 0;
        for (var i = start; i < start + length; i++)
        {
            if (s[i] is < '0' or > '9')
            {
                return false;
            }
            value = value * 10 + (s[i] - '0');
        }
        return true;
    }

    /// <summary>view.go <c>DateOf</c>: <paramref name="t"/>'s calendar date in <paramref name="zone"/>.</summary>
    public static CalendarDate DateOf(DateTimeOffset t, TimeZoneInfo zone)
    {
        var p = PartsOf(t, zone);
        return new CalendarDate(string.Create(CultureInfo.InvariantCulture, $"{p.Year:D4}-{p.Month:D2}-{p.Day:D2}"));
    }

    /// <summary>
    /// view.go <c>Span</c>: an occurrence's [start, end) in
    /// <paramref name="zone"/>: a timed one's instants, an all-day one's
    /// midnights (<c>EndDate</c> exclusive; an end not after the start is the
    /// next day). Null when the occurrence carries no usable time.
    /// </summary>
    public static (DateTimeOffset Start, DateTimeOffset End)? Span(Occurrence o, TimeZoneInfo zone)
    {
        ArgumentNullException.ThrowIfNull(o);
        if (o.AllDay)
        {
            if (ParseDate(o.StartDate, zone) is not { } s)
            {
                return null;
            }
            var e = ParseDate(o.EndDate, zone) ?? s;
            if (e <= s)
            {
                e = AddDays(s, 1, zone);
            }
            return (s, e);
        }
        if (o.Start is not { } start)
        {
            return null;
        }
        var end = o.End is { } en && en > start ? en : start;
        return (ToZone(start, zone), ToZone(end, zone));
    }

    /// <summary>
    /// view.go <c>OccurrenceKey</c>: an occurrence's stable identity in the
    /// views: the event and the recurrence id, or the invitation message of
    /// one not in a calendar.
    /// </summary>
    public static string OccurrenceKey(Occurrence o)
    {
        ArgumentNullException.ThrowIfNull(o);
        var ev = o.EventId?.Value ?? "";
        if (ev.Length == 0 && o.Invitation is { } inv)
        {
            return "inv:" + inv.AccountId.Value + "/" + inv.MessageId.Value;
        }
        return ev + "|" + (o.RecurrenceId?.Value ?? "");
    }

    /// <summary>
    /// view.go <c>Overlaps</c>: an occurrence that touches [<paramref name="from"/>,
    /// <paramref name="to"/>) in <paramref name="zone"/>. A timed one of no
    /// length counts at its start.
    /// </summary>
    public static bool Overlaps(Occurrence o, DateTimeOffset from, DateTimeOffset to, TimeZoneInfo zone)
    {
        if (Span(o, zone) is not { } span)
        {
            return false;
        }
        var (s, e) = span;
        if (e == s)
        {
            return s >= from && s < to;
        }
        return s < to && e > from;
    }

    // ------------------------------------------------------------------
    // Helpers shared by the calendar files (Swift's, Go's unexported ones)
    // ------------------------------------------------------------------

    /// <summary>
    /// Go's <c>cmp.Compare</c> of strings: by their UTF-8 bytes, which is the
    /// order of their code points (UTF-16's ordinal order differs above
    /// U+FFFF).
    /// </summary>
    internal static int CompareBytes(string? a, string? b)
    {
        a ??= "";
        b ??= "";
        if (string.Equals(a, b, StringComparison.Ordinal))
        {
            return 0;
        }
        var x = a.EnumerateRunes();
        var y = b.EnumerateRunes();
        while (true)
        {
            var hasX = x.MoveNext();
            var hasY = y.MoveNext();
            if (!hasX || !hasY)
            {
                return hasX ? 1 : hasY ? -1 : 0;
            }
            var c = x.Current.Value.CompareTo(y.Current.Value);
            if (c != 0)
            {
                return c < 0 ? -1 : 1;
            }
        }
    }

    /// <summary>Go's <c>cmp.Compare</c>: -1, 0 or 1.</summary>
    internal static int Compare<T>(T a, T b)
        where T : IComparable<T>
    {
        var c = a.CompareTo(b);
        return c < 0 ? -1 : c > 0 ? 1 : 0;
    }

    /// <summary>Go's <c>cmp.Or</c>: the first non-zero comparison.</summary>
    internal static int Or(params ReadOnlySpan<int> comparisons)
    {
        foreach (var c in comparisons)
        {
            if (c != 0)
            {
                return c;
            }
        }
        return 0;
    }

    /// <summary>Go's <c>slices.SortStableFunc</c>, into a new list (LINQ's ordering is stable).</summary>
    internal static List<T> StableSorted<T>(IEnumerable<T> items, Comparison<T> compare) =>
        [.. items.Order(Comparer<T>.Create(compare))];

    /// <summary>An event's title for display: cleaned, <see cref="Text.Untitled"/> when empty.</summary>
    internal static string CleanTitle(string? s)
    {
        var t = Board.CleanLine(s, API.Limits.MaxEventTitleBytes);
        return t.Length == 0 ? Text.Untitled : t;
    }

    /// <summary>A server's one-line string for display.</summary>
    internal static string CleanLine(string? s) => Board.CleanLine(s, API.Limits.MaxEventLocationBytes);

    /// <summary>A person's name, else the address.</summary>
    internal static string NameOf(Address? a)
    {
        if (a is null)
        {
            return "";
        }
        var n = CleanLine(a.Name);
        return n.Length > 0 ? n : CleanLine(a.Email);
    }

    /// <summary>
    /// Go's <c>strings.TrimSpace</c>: .NET's white space is Unicode's
    /// White_Space, the set Go's <c>unicode.IsSpace</c> trims.
    /// </summary>
    internal static string TrimSpace(string? s) => (s ?? "").Trim();
}

/// <summary>Go's method of <see cref="Cal.View"/>.</summary>
/// <remarks>One static class per receiver: several extension blocks in one
/// class trip CA1708 over their synthesised names.</remarks>
public static class CalViewNickExtensions
{
    extension(Cal.View view)
    {
        /// <summary>The view's value of the key calendar-view: "day", "week", "month", "agenda".</summary>
        public string Nick => view switch
        {
            Cal.View.Day => "day",
            Cal.View.Month => "month",
            Cal.View.Agenda => "agenda",
            _ => "week",
        };
    }
}

/// <summary>Swift's member of <see cref="Cal.Weekday"/>.</summary>
public static class CalWeekdayExtensions
{
    extension(Cal.Weekday day)
    {
        /// <summary>The position in the week from Monday (Monday 0, Sunday 6), the order people read weekdays in.</summary>
        public int FromMonday => ((int)day + 6) % 7;
    }
}
