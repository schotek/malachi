// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Calendar/CalendarRecurrence.swift; GTK:
// ui/internal/calendar/recurrence.go (DayCode, ParseDayCode, weekdayName,
// ListText, ordinalWeekday, Custom, RecurrenceText, ruleText, weeklyText,
// monthPart, monthlyText, yearlyText, Repeat, Repeats, RepeatTitle,
// RepeatRecurrence, RepeatOf, sameRule, CustomRecurrence, IntervalUnit,
// EveryLabel) and Swift's fromMonday.
//
// Recurrence texts ("Every 2 weeks on Monday and Wednesday", "Monthly on
// the second Tuesday", "Yearly on 14 October, until 1 March 2027") and the
// editor's Repeat choices with their Recurrence values. A rule outside the
// editor's shape (Recurrence.Complex, several ordinals, a by-month-day
// list with a weekday, …) is "Custom". Go's times carry their zone; here,
// as in Swift, the functions that read a start's weekday, day or month
// take the zone (RepeatRecurrence, RepeatOf, CustomRecurrence).

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.I18n;

namespace Malachi.Core.Calendars;

public static partial class Cal
{
    // The iCalendar day codes by Weekday (Sunday 0).
    private static readonly string[] DayCodes = ["SU", "MO", "TU", "WE", "TH", "FR", "SA"];

    /// <summary>recurrence.go <c>DayCode</c>: the iCalendar code of a weekday, "MO".</summary>
    public static string DayCode(Weekday d) => DayCodes[((int)d % 7 + 7) % 7];

    /// <summary>
    /// recurrence.go <c>ParseDayCode</c>: "MO", "2TU", "-1FR", "+3SU": the
    /// weekday and the ordinal (0 none; -5 to 5 otherwise); null for
    /// anything else.
    /// </summary>
    public static (Weekday Day, int Ordinal)? ParseDayCode(string? s)
    {
        if (s is null || s.Length < 2)
        {
            return null;
        }
        var code = s[^2..];
        var ord = 0;
        var n = s[..^2];
        if (n.Length > 0)
        {
            if (n[0] == '+')
            {
                n = n[1..];
            }
            if (Atoi(n) is not { } v || v == 0 || v < -5 || v > 5)
            {
                return null;
            }
            ord = v;
        }
        var i = Array.IndexOf(DayCodes, code);
        return i < 0 ? null : ((Weekday)i, ord);
    }

    // Go's strconv.Atoi for the small numbers of a day code: an optional
    // sign and ASCII digits; null for anything else. A value past ±99999 is
    // returned as that bound (the caller refuses it anyway; Atoi's overflow
    // error is a refusal too).
    private static int? Atoi(string s)
    {
        var i = 0;
        var negative = false;
        if (s.Length > 0 && s[0] is '+' or '-')
        {
            negative = s[0] == '-';
            i = 1;
        }
        if (i == s.Length)
        {
            return null;
        }
        var v = 0;
        for (; i < s.Length; i++)
        {
            if (s[i] is < '0' or > '9')
            {
                return null;
            }
            v = Math.Min(v * 10 + (s[i] - '0'), 99_999);
        }
        return negative ? -v : v;
    }

    /// <summary>A weekday's name in the env's locale, through a reference date (Go's <c>weekdayName</c>).</summary>
    internal static string WeekdayName(Weekday d, Env env)
    {
        ArgumentNullException.ThrowIfNull(env);
        // 2026-10-04 is a Sunday.
        return Text.WeekdayText(Date(2026, 10, 4 + (int)d, 12, 0, env.Zone), env);
    }

    /// <summary>
    /// recurrence.go <c>ListText</c>: names joined into a list: "Monday",
    /// "Monday and Wednesday", "Monday, Wednesday and Friday".
    /// </summary>
    public static string ListText(IReadOnlyList<string> items)
    {
        ArgumentNullException.ThrowIfNull(items);
        switch (items.Count)
        {
            case 0:
                return "";
            case 1:
                return items[0];
        }
        var output = items[0];
        for (var i = 1; i < items.Count - 1; i++)
        {
            // TRANSLATORS: joins all but the last two items of a list, e.g. the
            // weekdays "Monday, Wednesday and Friday".
            output = L10n.T("%s, %s", output, items[i]);
        }
        // TRANSLATORS: joins the last item of a list, e.g. "Monday and
        // Wednesday".
        return L10n.T("%s and %s", output, items[^1]);
    }

    // "second Tuesday", "last Friday".
    private static string OrdinalWeekday(int ord, Weekday d, Env env)
    {
        var day = WeekdayName(d, env);
        return ord switch
        {
            // TRANSLATORS: a weekday of a month in a recurrence, %s is its name:
            // "Monthly on the first Monday".
            1 => L10n.T("first %s", day),
            // TRANSLATORS: a weekday of a month in a recurrence: "second Tuesday".
            2 => L10n.T("second %s", day),
            // TRANSLATORS: a weekday of a month in a recurrence: "third Tuesday".
            3 => L10n.T("third %s", day),
            // TRANSLATORS: a weekday of a month in a recurrence: "fourth Tuesday".
            4 => L10n.T("fourth %s", day),
            // TRANSLATORS: a weekday of a month in a recurrence: "fifth Tuesday".
            5 => L10n.T("fifth %s", day),
            // TRANSLATORS: a weekday of a month in a recurrence: "last Friday".
            -1 => L10n.T("last %s", day),
            // TRANSLATORS: a weekday of a month in a recurrence: "second-to-last
            // Friday" (and further from the end).
            _ => L10n.T("second-to-last %s", day),
        };
    }

    /// <summary>recurrence.go <c>Custom</c>: the text of a rule the editor cannot show.</summary>
    public static string Custom =>
        // TRANSLATORS: a recurrence rule the calendar cannot describe.
        L10n.T("Custom");

    /// <summary>
    /// recurrence.go <c>RecurrenceText</c>: describes <paramref name="r"/>;
    /// <paramref name="start"/> is the series' first start (read in the
    /// env's zone, it gives the day where the rule names none). "" for null;
    /// <see cref="Custom"/> for a rule it cannot describe.
    /// </summary>
    public static string RecurrenceText(Recurrence? r, DateTimeOffset start, Env env)
    {
        ArgumentNullException.ThrowIfNull(env);
        if (r is null)
        {
            return "";
        }
        if (r.Complex == true)
        {
            return Custom;
        }
        if (RuleText(r, ToZone(start, env.Zone), env) is not { } text)
        {
            return Custom;
        }
        if (r.Count is { } count && count > 0)
        {
            // TRANSLATORS: a recurrence that ends after a number of times:
            // "Every week on Monday, 10 times".
            return L10n.N("%s, %d time", "%s, %d times", count, text, count);
        }
        if (r.Until is { Value.Length: > 0 } until && ParseDate(until, env.Zone) is { } u)
        {
            // TRANSLATORS: a recurrence that ends on a date: "Every day,
            // until 1 March 2027".
            return L10n.T("%s, until %s", text, Text.DayMonthYearText(u, env));
        }
        return text;
    }

    // A rule without its end; null when the rule cannot be described.
    private static string? RuleText(Recurrence r, DateTimeOffset start, Env env)
    {
        var n = Math.Max(1, r.Interval);
        switch (r.Frequency.Value)
        {
            case RecurrenceFrequency.Daily:
                if (Any(r.ByDay) || Any(r.ByMonthDay) || Any(r.ByMonth))
                {
                    return null;
                }
                return n == 1 ? L10n.T("Every day") : L10n.N("Every %d day", "Every %d days", n);
            case RecurrenceFrequency.Weekly:
                return WeeklyText(r, n, start, env);
            case RecurrenceFrequency.Monthly:
                return MonthlyText(r, n, start, env);
            case RecurrenceFrequency.Yearly:
                return YearlyText(r, n, start, env);
            default:
                return null;
        }
    }

    private static bool Any<T>(IReadOnlyList<T>? list) => list is { Count: > 0 };

    /// <summary>Weekdays in the week's order from Monday, as people read them (Swift <c>fromMonday</c>).</summary>
    internal static List<Weekday> FromMonday(IEnumerable<Weekday> days) =>
        StableSorted(days, (a, b) => Compare(a.FromMonday, b.FromMonday));

    private static string? WeeklyText(Recurrence r, int n, DateTimeOffset start, Env env)
    {
        if (Any(r.ByMonthDay) || Any(r.ByMonth))
        {
            return null;
        }
        var days = new List<Weekday> { PartsOf(start, env.Zone).Weekday };
        if (r.ByDay is { Count: > 0 } byDay)
        {
            days.Clear();
            foreach (var c in byDay)
            {
                if (ParseDayCode(c) is not { Ordinal: 0 } parsed)
                {
                    return null;
                }
                if (!days.Contains(parsed.Day))
                {
                    days.Add(parsed.Day);
                }
            }
        }
        days = FromMonday(days);
        if (n == 1 && days.SequenceEqual([Weekday.Monday, Weekday.Tuesday, Weekday.Wednesday, Weekday.Thursday, Weekday.Friday]))
        {
            return L10n.T("Every weekday");
        }
        if (n == 1 && days.Count == 7)
        {
            return L10n.T("Every day");
        }
        var list = ListText([.. days.Select(d => WeekdayName(d, env))]);
        if (n == 1)
        {
            // TRANSLATORS: a weekly recurrence; %s is a list of weekdays, "Every
            // week on Monday and Wednesday".
            return L10n.T("Every week on %s", list);
        }
        // TRANSLATORS: a recurrence every few weeks; %d is the number of
        // weeks, %s a list of weekdays: "Every 2 weeks on Monday and Wednesday".
        return L10n.N("Every %d week on %s", "Every %d weeks on %s", n, n, list);
    }

    // The "on …" of a monthly or yearly rule: an ordinal weekday
    // (Ordinal ≠ 0) or a day of the month (Ordinal 0).
    private readonly record struct MonthPart(int Ordinal, Weekday Day, int MonthDay);

    // Go's monthPart: the ordinal weekday, or the day of the month (from
    // start when the rule names none); null when the rule names neither.
    private static MonthPart? MonthPartOf(Recurrence r, DateTimeOffset start, Env env)
    {
        var byDay = r.ByDay ?? [];
        var byMonthDay = r.ByMonthDay ?? [];
        if (byDay.Count == 1 && byMonthDay.Count == 0)
        {
            if (ParseDayCode(byDay[0]) is not { } parsed || parsed.Ordinal == 0)
            {
                return null;
            }
            return new MonthPart(parsed.Ordinal, parsed.Day, 0);
        }
        if (byDay.Count == 0 && byMonthDay.Count == 1)
        {
            return byMonthDay[0] != 0 ? new MonthPart(0, default, byMonthDay[0]) : null;
        }
        if (byDay.Count == 0 && byMonthDay.Count == 0)
        {
            return new MonthPart(0, default, PartsOf(start, env.Zone).Day);
        }
        return null;
    }

    private static string? MonthlyText(Recurrence r, int n, DateTimeOffset start, Env env)
    {
        if (Any(r.ByMonth) || MonthPartOf(r, start, env) is not { } part)
        {
            return null;
        }
        string on;
        if (part.Ordinal != 0)
        {
            // TRANSLATORS: the day of a monthly recurrence; %s is an ordinal
            // weekday such as "second Tuesday": "Monthly on the second Tuesday".
            on = L10n.T("on the %s", OrdinalWeekday(part.Ordinal, part.Day, env));
        }
        else if (part.MonthDay == -1)
        {
            // TRANSLATORS: the day of a monthly recurrence: the month's last day.
            on = L10n.T("on the last day");
        }
        else if (part.MonthDay < 0)
        {
            return null;
        }
        else
        {
            // TRANSLATORS: the day of a monthly recurrence: "Monthly on day 14".
            on = L10n.T("on day %d", part.MonthDay);
        }
        if (n == 1)
        {
            // TRANSLATORS: a monthly recurrence; %s is its day: "Monthly on day
            // 14", "Monthly on the second Tuesday".
            return L10n.T("Monthly %s", on);
        }
        // TRANSLATORS: a recurrence every few months; %d is the number of
        // months, %s the day: "Every 3 months on day 14".
        return L10n.N("Every %d month %s", "Every %d months %s", n, n, on);
    }

    private static string? YearlyText(Recurrence r, int n, DateTimeOffset start, Env env)
    {
        var month = PartsOf(start, env.Zone).Month;
        var byMonth = r.ByMonth ?? [];
        switch (byMonth.Count)
        {
            case 0:
                break;
            case 1:
                if (byMonth[0] is < 1 or > 12)
                {
                    return null;
                }
                month = byMonth[0];
                break;
            default:
                return null;
        }
        if (MonthPartOf(r, start, env) is not { } part || (part.Ordinal == 0 && part.MonthDay < 1))
        {
            return null;
        }
        string on;
        if (part.Ordinal != 0)
        {
            var monthName = Text.MonthText(Date(2026, month, 1, 12, 0, env.Zone), env);
            // TRANSLATORS: the day of a yearly recurrence: an ordinal weekday
            // and a month, "on the second Tuesday of October".
            on = L10n.T("on the %s of %s", OrdinalWeekday(part.Ordinal, part.Day, env), monthName);
        }
        else
        {
            // A leap year, so that 29 February has a date.
            var reference = Date(2028, month, part.MonthDay, 12, 0, env.Zone);
            if (PartsOf(reference, env.Zone).Month != month)
            {
                return null;
            }
            // TRANSLATORS: the day of a yearly recurrence: "on 14 October".
            on = L10n.T("on %s", Text.DayMonthText(reference, env));
        }
        if (n == 1)
        {
            // TRANSLATORS: a yearly recurrence; %s is its day: "Yearly on 14
            // October".
            return L10n.T("Yearly %s", on);
        }
        // TRANSLATORS: a recurrence every few years; %d is the number of years,
        // %s the day: "Every 2 years on 14 October".
        return L10n.N("Every %d year %s", "Every %d years %s", n, n, on);
    }

    /// <summary>recurrence.go <c>Repeat</c>: a choice of the editor's Repeat popup, in the popup's order.</summary>
    public enum Repeat
    {
        /// <summary>The event does not repeat.</summary>
        Never,

        /// <summary>Every day.</summary>
        Daily,

        /// <summary>Every week on the start's weekday.</summary>
        Weekly,

        /// <summary>Every 2 weeks on the start's weekday.</summary>
        Biweekly,

        /// <summary>Every month on the start's day.</summary>
        Monthly,

        /// <summary>Every year on the start's day and month.</summary>
        Yearly,

        /// <summary>Opens the small popover with interval and days; also the choice of a rule none of the others is.</summary>
        Custom,
    }

    /// <summary>recurrence.go <c>Repeats</c>: the choices in the popup's order.</summary>
    public static IReadOnlyList<Repeat> Repeats { get; } =
        [Repeat.Never, Repeat.Daily, Repeat.Weekly, Repeat.Biweekly, Repeat.Monthly, Repeat.Yearly, Repeat.Custom];

    /// <summary>recurrence.go <c>RepeatTitle</c>: a choice's title in the popup.</summary>
    public static string RepeatTitle(Repeat c) => c switch
    {
        Repeat.Daily => L10n.T("Every Day"),
        Repeat.Weekly => L10n.T("Every Week"),
        Repeat.Biweekly => L10n.T("Every 2 Weeks"),
        Repeat.Monthly => L10n.T("Every Month"),
        Repeat.Yearly => L10n.T("Every Year"),
        Repeat.Custom => L10n.T("Custom…"),
        // TRANSLATORS: the editor's Repeat popup: the event does not repeat.
        _ => L10n.T("Never"),
    };

    /// <summary>
    /// recurrence.go <c>RepeatRecurrence</c>: the rule of a choice for an
    /// event starting at <paramref name="start"/> in its
    /// <paramref name="zone"/>: weekly rules name the start's weekday,
    /// monthly its day, yearly its day and month. Never and Custom are null
    /// (Custom's rule comes from <see cref="CustomRecurrence"/>).
    /// </summary>
    public static Recurrence? RepeatRecurrence(Repeat c, DateTimeOffset start, TimeZoneInfo zone)
    {
        var p = PartsOf(start, zone);
        IReadOnlyList<string> day = [DayCode(p.Weekday)];
        return c switch
        {
            Repeat.Daily => new Recurrence { Frequency = RecurrenceFrequency.Daily, Interval = 1 },
            Repeat.Weekly => new Recurrence { Frequency = RecurrenceFrequency.Weekly, Interval = 1, ByDay = day },
            Repeat.Biweekly => new Recurrence { Frequency = RecurrenceFrequency.Weekly, Interval = 2, ByDay = day },
            Repeat.Monthly => new Recurrence { Frequency = RecurrenceFrequency.Monthly, Interval = 1, ByMonthDay = [p.Day] },
            Repeat.Yearly => new Recurrence
            {
                Frequency = RecurrenceFrequency.Yearly,
                Interval = 1,
                ByMonth = [p.Month],
                ByMonthDay = [p.Day],
            },
            _ => null,
        };
    }

    /// <summary>
    /// recurrence.go <c>RepeatOf</c>: the choice that shows
    /// <paramref name="r"/> for an event starting at <paramref name="start"/>
    /// in <paramref name="zone"/>: Never for null, the choice whose rule it
    /// is (without an end), else Custom.
    /// </summary>
    public static Repeat RepeatOf(Recurrence? r, DateTimeOffset start, TimeZoneInfo zone)
    {
        if (r is null)
        {
            return Repeat.Never;
        }
        if (r.Complex == true || r.Count > 0 || r.Until is { Value.Length: > 0 })
        {
            return Repeat.Custom;
        }
        for (var i = 1; i < Repeats.Count - 1; i++)
        {
            if (RepeatRecurrence(Repeats[i], start, zone) is { } rule && SameRule(r, rule))
            {
                return Repeats[i];
            }
        }
        // A weekly or monthly rule without days takes them from the start.
        if (!Any(r.ByDay) && !Any(r.ByMonthDay) && !Any(r.ByMonth))
        {
            switch (r.Frequency.Value, Math.Max(1, r.Interval))
            {
                case (RecurrenceFrequency.Daily, 1):
                    return Repeat.Daily;
                case (RecurrenceFrequency.Weekly, 1):
                    return Repeat.Weekly;
                case (RecurrenceFrequency.Weekly, 2):
                    return Repeat.Biweekly;
                case (RecurrenceFrequency.Monthly, 1):
                    return Repeat.Monthly;
                case (RecurrenceFrequency.Yearly, 1):
                    return Repeat.Yearly;
            }
        }
        return Repeat.Custom;
    }

    /// <summary>Go's <c>sameRule</c>: the same frequency, interval and lists (an absent list is an empty one).</summary>
    internal static bool SameRule(Recurrence a, Recurrence b)
    {
        ArgumentNullException.ThrowIfNull(a);
        ArgumentNullException.ThrowIfNull(b);
        return a.Frequency == b.Frequency && Math.Max(1, a.Interval) == Math.Max(1, b.Interval)
            && (a.ByDay ?? []).SequenceEqual(b.ByDay ?? [], StringComparer.Ordinal)
            && (a.ByMonthDay ?? []).SequenceEqual(b.ByMonthDay ?? [])
            && (a.ByMonth ?? []).SequenceEqual(b.ByMonth ?? []);
    }

    /// <summary>
    /// recurrence.go <c>CustomRecurrence</c>: the Custom… popover's rule: a
    /// frequency, an interval clamped to 1–<see cref="API.Limits.MaxRecurrenceInterval"/>
    /// and, for weekly, the days (none: the start's day in
    /// <paramref name="zone"/>) in the week's order from Monday, once each.
    /// </summary>
    public static Recurrence CustomRecurrence(
        RecurrenceFrequency f, int interval, IReadOnlyList<Weekday>? days, DateTimeOffset start, TimeZoneInfo zone)
    {
        var p = PartsOf(start, zone);
        var r = new Recurrence { Frequency = f, Interval = Math.Min(Math.Max(1, interval), API.Limits.MaxRecurrenceInterval) };
        switch (f.Value)
        {
            case RecurrenceFrequency.Weekly:
                {
                    var ds = new List<Weekday>();
                    foreach (var d in FromMonday(days is { Count: > 0 } ? days : [p.Weekday]))
                    {
                        if (ds.Count == 0 || ds[^1] != d)
                        {
                            ds.Add(d);
                        }
                    }
                    return r with { ByDay = [.. ds.Select(DayCode)] };
                }
            case RecurrenceFrequency.Monthly:
                return r with { ByMonthDay = [p.Day] };
            case RecurrenceFrequency.Yearly:
                return r with { ByMonth = [p.Month], ByMonthDay = [p.Day] };
            default:
                return r;
        }
    }

    /// <summary>recurrence.go <c>IntervalUnit</c>: the Custom… popover's unit after the interval field: "day"/"days", "week"/"weeks", ….</summary>
    public static string IntervalUnit(RecurrenceFrequency f, int n) => f.Value switch
    {
        // TRANSLATORS: the unit after the interval of a custom recurrence,
        // "Every [2] weeks".
        RecurrenceFrequency.Weekly => L10n.N("week", "weeks", n),
        // TRANSLATORS: the unit after the interval of a custom recurrence.
        RecurrenceFrequency.Monthly => L10n.N("month", "months", n),
        // TRANSLATORS: the unit after the interval of a custom recurrence.
        RecurrenceFrequency.Yearly => L10n.N("year", "years", n),
        // TRANSLATORS: the unit after the interval of a custom recurrence.
        _ => L10n.N("day", "days", n),
    };

    /// <summary>recurrence.go <c>EveryLabel</c>: the Custom… popover's label before the interval field.</summary>
    public static string EveryLabel =>
        // TRANSLATORS: the custom recurrence popover: "Every [2] weeks".
        L10n.T("Every");
}
