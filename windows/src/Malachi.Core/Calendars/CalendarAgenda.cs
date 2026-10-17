// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Calendar/CalendarAgenda.swift; GTK:
// ui/internal/calendar/agenda.go (AgendaItem, AgendaDay, occurrenceDays,
// Agenda, compareItems, DayHeading, DayOfText, ItemTime, AgendaEmpty,
// ResultsHeading, NoResults, UnansweredHeading).
//
// The Agenda view and the search results: the occurrences grouped by day
// under "Today", "Tomorrow" or the weekday and date; an occurrence over
// several days is listed on each of its days with "Day 2 of 3". The
// Board's Today strip (CalendarToday.cs) is the agenda of one day.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.I18n;

namespace Malachi.Core.Calendars;

public static partial class Cal
{
    /// <summary>agenda.go <c>AgendaItem</c>: one row of the agenda.</summary>
    /// <param name="Occurrence">The occurrence.</param>
    /// <param name="Start">The occurrence's start in the zone (its whole span, not the day's part).</param>
    /// <param name="End">The occurrence's end in the zone.</param>
    /// <param name="DayOf">The row is day <paramref name="DayOf"/> (1-based) of the occurrence's days.</param>
    /// <param name="Days">The calendar days the occurrence touches; 1 for one that fits in a day.</param>
    /// <param name="State">How it is drawn.</param>
    public sealed record AgendaItem(
        Occurrence Occurrence, DateTimeOffset Start, DateTimeOffset End, int DayOf, int Days, BlockState State = BlockState.Normal);

    /// <summary>agenda.go <c>AgendaDay</c>: a day of the agenda with its rows.</summary>
    public sealed record AgendaDay
    {
        /// <summary>The midnight of the day.</summary>
        public required DateTimeOffset Day { get; init; }

        /// <summary>Today, Tomorrow, Yesterday, or the weekday and date (<see cref="DayHeading"/>).</summary>
        public required string Heading { get; init; }

        /// <summary>The day's rows, all-day first, then by start, end and key.</summary>
        public IReadOnlyList<AgendaItem> Items { get; init => field = value ?? []; } = [];

        /// <summary>Whether both are the same day with the same rows.</summary>
        public bool Equals(AgendaDay? other) =>
            other is not null && Day == other.Day && Heading == other.Heading && Items.SequenceEqual(other.Items);

        /// <inheritdoc/>
        public override int GetHashCode() => HashCode.Combine(Day, Heading, Items.Count);
    }

    /// <summary>
    /// agenda.go <c>occurrenceDays</c>: how many calendar days an occurrence
    /// touches in <paramref name="zone"/>: a timed one ending exactly at a
    /// midnight does not touch that day; one of no length touches one.
    /// </summary>
    internal static int OccurrenceDays(DateTimeOffset start, DateTimeOffset end, TimeZoneInfo zone)
    {
        if (end <= start)
        {
            return 1;
        }
        var last = end;
        if (StartOfDay(end, zone) == end)
        {
            // Go steps back a nanosecond; a tick is DateTimeOffset's least.
            last = end.AddTicks(-1);
        }
        return DaysBetween(start, last, zone) + 1;
    }

    /// <summary>
    /// agenda.go <c>Agenda</c>: groups the occurrences that touch
    /// [<paramref name="from"/>, <paramref name="from"/> +
    /// <paramref name="days"/>) by day, only days that have some, each
    /// day's rows all-day first, then by start, end and key. Without
    /// <paramref name="showDeclined"/> declined occurrences are left out.
    /// </summary>
    public static IReadOnlyList<AgendaDay> Agenda(
        IReadOnlyList<Occurrence> occurrences, DateTimeOffset from, int days, DateTimeOffset now, Env env, bool showDeclined)
    {
        ArgumentNullException.ThrowIfNull(occurrences);
        ArgumentNullException.ThrowIfNull(env);
        var zone = env.Zone;
        var first = StartOfDay(from, zone);
        var byDay = new SortedDictionary<int, List<AgendaItem>>();
        foreach (var o in occurrences)
        {
            if (o is null || !Shown(o, showDeclined) || Span(o, zone) is not (var s, var e))
            {
                continue;
            }
            var n = OccurrenceDays(s, e, zone);
            var state = StateOf(o);
            // Only the days of the range: an occurrence of years stays cheap.
            var baseIndex = DaysBetween(first, s, zone);
            for (var i = Math.Max(0, -baseIndex); i < n && baseIndex + i < days; i++)
            {
                var idx = baseIndex + i;
                if (!byDay.TryGetValue(idx, out var items))
                {
                    items = [];
                    byDay[idx] = items;
                }
                items.Add(new AgendaItem(o, s, e, i + 1, n, state));
            }
        }
        var output = new List<AgendaDay>(byDay.Count);
        foreach (var (i, items) in byDay)
        {
            var d = AddDays(first, i, zone);
            output.Add(new AgendaDay { Day = d, Heading = DayHeading(d, now, env), Items = StableSorted(items, CompareItems) });
        }
        return output;
    }

    // agenda.go compareItems: all-day first, then by start, end and key.
    internal static int CompareItems(AgendaItem a, AgendaItem b)
    {
        if (a.Occurrence.AllDay != b.Occurrence.AllDay)
        {
            return a.Occurrence.AllDay ? -1 : 1;
        }
        return Or(
            Compare(a.Start, b.Start),
            Compare(a.End, b.End),
            CompareBytes(OccurrenceKey(a.Occurrence), OccurrenceKey(b.Occurrence)));
    }

    /// <summary>
    /// agenda.go <c>DayHeading</c>: an agenda day's heading: Today,
    /// Tomorrow, Yesterday, else the weekday and date (with the year when it
    /// is not <paramref name="now"/>'s year).
    /// </summary>
    public static string DayHeading(DateTimeOffset day, DateTimeOffset now, Env env)
    {
        ArgumentNullException.ThrowIfNull(env);
        var zone = env.Zone;
        switch (DaysBetween(now, day, zone))
        {
            case 0:
                // TRANSLATORS: the heading of today's events in the agenda.
                return L10n.T("Today");
            case 1:
                // TRANSLATORS: the heading of tomorrow's events in the agenda.
                return L10n.T("Tomorrow");
            case -1:
                // TRANSLATORS: the heading of yesterday's events in the agenda.
                return L10n.T("Yesterday");
        }
        if (PartsOf(day, zone).Year != PartsOf(now, zone).Year)
        {
            return Text.LongDateText(day, env);
        }
        return Text.WeekdayDateText(day, env);
    }

    /// <summary>agenda.go <c>DayOfText</c>: the note of an occurrence over several days, "Day 2 of 3".</summary>
    public static string DayOfText(int n, int of) =>
        // TRANSLATORS: an event that lasts several days, listed on each of its
        // days in the agenda: which day this is of how many.
        L10n.T("Day %d of %d", n, of);

    /// <summary>
    /// agenda.go <c>ItemTime</c>: a row's time column: All Day, the time
    /// range, or for a day of an occurrence over several days its part
    /// ("from 22:00", "until 02:00", All Day between).
    /// </summary>
    public static string ItemTime(AgendaItem it, Env env)
    {
        ArgumentNullException.ThrowIfNull(it);
        ArgumentNullException.ThrowIfNull(env);
        if (it.Occurrence.AllDay)
        {
            return Text.AllDay;
        }
        if (it.Days <= 1)
        {
            return Text.TimeRangeText(it.Start, it.End, env);
        }
        if (it.DayOf == 1)
        {
            // TRANSLATORS: the first day of an event over several days in the
            // agenda: its start time.
            return L10n.T("from %s", Text.TimeText(it.Start, env));
        }
        if (it.DayOf == it.Days)
        {
            // TRANSLATORS: the last day of an event over several days in the
            // agenda: its end time.
            return L10n.T("until %s", Text.TimeText(it.End, env));
        }
        return Text.AllDay;
    }

    /// <summary>agenda.go <c>AgendaEmpty</c>: the agenda's text for a range without events.</summary>
    public static string AgendaEmpty => L10n.T("No events in this period.");

    /// <summary>agenda.go <c>ResultsHeading</c>: heads the search results in place of the view.</summary>
    public static string ResultsHeading =>
        // TRANSLATORS: the heading of the event search's results.
        L10n.T("Results");

    /// <summary>agenda.go <c>NoResults</c>: the search's text when nothing matched.</summary>
    public static string NoResults => L10n.T("No events match the search.");

    /// <summary>
    /// agenda.go <c>UnansweredHeading</c>: heads the agenda filtered to the
    /// unanswered invitations (the sidebar's Invitations row).
    /// </summary>
    public static string UnansweredHeading =>
        // TRANSLATORS: the sidebar's row and the agenda's heading of the
        // invitations waiting for an answer.
        L10n.T("Invitations");
}
