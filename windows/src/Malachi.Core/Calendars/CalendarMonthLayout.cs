// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Calendar/CalendarMonthLayout.swift, all
// but the grid (cellCapacity, MonthSpan, spanDays, isMonthSpan, monthLanes,
// laneCount, Pill, cellItems, more); GTK: ui/internal/calendar/month.go
// (CellCapacity, MonthSpan, spanDays, IsMonthSpan, MonthLanes, LaneCount,
// Pill, CellItems, More).
//
// The Month view's content: the bars of the occurrences that span days (an
// all-day event, or a timed one that crosses midnight) in lanes under each
// week row's day numbers, as Apple Calendar draws them; how many lines of
// pills a cell has room for under its row's lanes; and which pills a day
// shows, with "+2 more" for the rest. The grid itself (Cell, MonthGrid,
// MonthCells, WeekdayHeaders) is CalendarMonthGrid.cs.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.I18n;

namespace Malachi.Core.Calendars;

public static partial class Cal
{
    /// <summary>
    /// month.go <c>CellCapacity</c>: how many lines of pills a cell's content
    /// area holds: <paramref name="cellHeight"/> is the room below the day
    /// number, less the <paramref name="lanes"/> of spanning bars of its week
    /// row (<see cref="MonthLanes"/>, <see cref="LaneCount"/>) of
    /// <paramref name="laneHeight"/> each; <paramref name="lineHeight"/> is
    /// one pill's height; at least 0.
    /// </summary>
    public static int CellCapacity(double cellHeight, double lineHeight, int lanes = 0, double laneHeight = 0)
    {
        var room = cellHeight - Math.Max(0, lanes) * Math.Max(0, laneHeight);
        // Swift's guard: a NaN is no room either.
        if (!(lineHeight > 0) || !(room > 0))
        {
            return 0;
        }
        return (int)Math.Min(room / lineHeight, int.MaxValue);
    }

    /// <summary>
    /// month.go <c>MonthSpan</c>: an occurrence drawn as one bar across the
    /// days it covers in a week row of the month grid (like Apple Calendar):
    /// an all-day event, or a timed one that crosses midnight.
    /// </summary>
    /// <param name="Occurrence">The occurrence.</param>
    /// <param name="Key">Its <see cref="OccurrenceKey"/>.</param>
    /// <param name="Title">The cleaned title, <see cref="Text.Untitled"/> when empty (Swift's; Go leaves it "").</param>
    /// <param name="CalendarId">The source of its colour.</param>
    /// <param name="Lane">The bar's row (0-based) under the day numbers.</param>
    /// <param name="StartCol">Its first column (0–6), clipped to the week.</param>
    /// <param name="EndCol">Its last column (0–6, inclusive), clipped to the week.</param>
    /// <param name="ContinuesBefore">The occurrence goes on before the row's first day.</param>
    /// <param name="ContinuesAfter">The occurrence goes on after the row's last day.</param>
    /// <param name="State">How it is drawn.</param>
    public sealed record MonthSpan(
        Occurrence Occurrence,
        string Key,
        string Title,
        CalendarId? CalendarId,
        int Lane,
        int StartCol,
        int EndCol,
        bool ContinuesBefore,
        bool ContinuesAfter,
        BlockState State);

    // month.go spanDays: an occurrence's first day and its last day touched
    // (a timed one ending exactly at a midnight does not touch that day).
    private static (DateTimeOffset First, DateTimeOffset Last)? SpanDays(Occurrence o, TimeZoneInfo zone)
    {
        if (Span(o, zone) is not (var s, var e))
        {
            return null;
        }
        var first = StartOfDay(s, zone);
        if (o.AllDay)
        {
            return (first, AddDays(StartOfDay(e, zone), -1, zone));
        }
        return (first, AddDays(first, OccurrenceDays(s, e, zone) - 1, zone));
    }

    /// <summary>
    /// month.go <c>IsMonthSpan</c>: an occurrence the month grid draws as a
    /// bar: all day, or timed over more than one calendar day. A timed one
    /// within a day stays a pill (<see cref="CellItems"/>).
    /// </summary>
    public static bool IsMonthSpan(Occurrence o, TimeZoneInfo zone)
    {
        ArgumentNullException.ThrowIfNull(o);
        return SpanDays(o, zone) is var (first, last) && (o.AllDay || last > first);
    }

    /// <summary>
    /// month.go <c>MonthLanes</c>: the bars of one week row
    /// (<paramref name="weekDays"/>: its seven midnights in order; anything
    /// but seven has none, as Swift's): sorted by first column, then the
    /// longer first, then by <see cref="OccurrenceKey"/>; each takes the
    /// lowest lane free over its columns. An occurrence given twice is one
    /// bar; without <paramref name="showDeclined"/> declined occurrences are
    /// left out.
    /// </summary>
    public static IReadOnlyList<MonthSpan> MonthLanes(
        IReadOnlyList<Occurrence> occurrences, IReadOnlyList<DateTimeOffset> weekDays, TimeZoneInfo zone, bool showDeclined)
    {
        ArgumentNullException.ThrowIfNull(occurrences);
        ArgumentNullException.ThrowIfNull(weekDays);
        if (weekDays.Count != 7)
        {
            return [];
        }
        var from = weekDays[0];
        var to = AddDays(weekDays[6], 1, zone);
        var spans = new List<MonthSpan>();
        var seen = new HashSet<string>(StringComparer.Ordinal);
        foreach (var o in occurrences)
        {
            if (o is null)
            {
                continue;
            }
            var key = OccurrenceKey(o);
            if (seen.Contains(key) || !Shown(o, showDeclined) || !IsMonthSpan(o, zone) || !Overlaps(o, from, to, zone)
                || SpanDays(o, zone) is not (var first, var last))
            {
                continue;
            }
            seen.Add(key);
            spans.Add(new MonthSpan(
                o, key, CleanTitle(o.Title), o.CalendarId, 0,
                Math.Max(0, DaysBetween(from, first, zone)), Math.Min(6, DaysBetween(from, last, zone)),
                first < from, !(last < to), StateOf(o)));
        }
        var sorted = StableSorted(spans, (a, b) => Or(
            Compare(a.StartCol, b.StartCol),
            Compare(b.EndCol - b.StartCol, a.EndCol - a.StartCol),
            CompareBytes(a.Key, b.Key)));
        var laneEnds = new List<int>();
        for (var i = 0; i < sorted.Count; i++)
        {
            var span = sorted[i];
            var lane = laneEnds.FindIndex(end => end < span.StartCol);
            if (lane < 0)
            {
                lane = laneEnds.Count;
                laneEnds.Add(0);
            }
            laneEnds[lane] = span.EndCol;
            sorted[i] = span with { Lane = lane };
        }
        return sorted;
    }

    /// <summary>month.go <c>LaneCount</c>: the number of lanes the spans of a row use.</summary>
    public static int LaneCount(IReadOnlyList<MonthSpan>? spans) => spans is null or [] ? 0 : spans.Max(s => s.Lane + 1);

    /// <summary>month.go <c>Pill</c>: an occurrence in a month cell.</summary>
    /// <param name="Occurrence">The occurrence.</param>
    /// <param name="Start">The occurrence's start in the zone (a timed pill's time, <see cref="Text.TimeText"/>).</param>
    /// <param name="ContinuesBefore">A timed occurrence that started an earlier day: its pill shows no time.</param>
    /// <param name="State">How it is drawn.</param>
    public sealed record Pill(Occurrence Occurrence, DateTimeOffset Start, bool ContinuesBefore, BlockState State);

    /// <summary>
    /// month.go <c>CellItems</c>: the pills of <paramref name="day"/> in a
    /// cell of <paramref name="capacity"/> lines: the occurrences within one
    /// day (the others are <see cref="MonthLanes"/>' bars), all-day first
    /// (the longer first), then timed by start, then by end, then by
    /// <see cref="OccurrenceKey"/>. When they do not all fit, the last line
    /// is "+n more" (<see cref="More"/>) and <c>More</c> is n; 0 when
    /// everything fits.
    /// </summary>
    public static (IReadOnlyList<Pill> Pills, int More) CellItems(
        IReadOnlyList<Occurrence> occurrences, DateTimeOffset day, int capacity, TimeZoneInfo zone, bool showDeclined)
    {
        ArgumentNullException.ThrowIfNull(occurrences);
        var dayStart = StartOfDay(day, zone);
        var dayEnd = AddDays(dayStart, 1, zone);
        var items = new List<(Pill P, DateTimeOffset Start, DateTimeOffset End)>();
        foreach (var o in occurrences)
        {
            if (o is null || !Shown(o, showDeclined) || IsMonthSpan(o, zone) || !Overlaps(o, dayStart, dayEnd, zone)
                || Span(o, zone) is not (var s, var e))
            {
                continue;
            }
            items.Add((new Pill(o, s, !o.AllDay && s < dayStart, StateOf(o)), s, e));
        }
        var sorted = StableSorted(items, (a, b) =>
        {
            if (a.P.Occurrence.AllDay != b.P.Occurrence.AllDay)
            {
                return a.P.Occurrence.AllDay ? -1 : 1;
            }
            var key = CompareBytes(OccurrenceKey(a.P.Occurrence), OccurrenceKey(b.P.Occurrence));
            if (a.P.Occurrence.AllDay)
            {
                return Or(Compare(a.Start, b.Start), Compare(b.End, a.End), key);
            }
            return Or(Compare(a.Start, b.Start), Compare(a.End, b.End), key);
        });
        var pills = sorted.Select(it => it.P).ToList();
        if (pills.Count <= capacity)
        {
            return (pills, 0);
        }
        var shown = Math.Max(0, capacity - 1);
        return (pills.Take(shown).ToList(), pills.Count - shown);
    }

    /// <summary>month.go <c>More</c>: the last line of a full cell, "+2 more" (a button that lists the day).</summary>
    public static string More(int n) =>
        // TRANSLATORS: the last line of a day of the month view that has more
        // events than room; %d is how many are not shown.
        L10n.N("+%d more", "+%d more", n);
}
