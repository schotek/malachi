// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Calendar/CalendarWeekLayout.swift; GTK:
// ui/internal/calendar/week.go (DayMinutes, MinBlockMinutes, BlockState,
// StateOf, Shown, Block, wallMinutes, LayoutDay, assignColumns, Bar,
// AllDayLanes, NowLine, ScrollMinute, ParseClock, BlockStateText,
// BlockLabel).
//
// The time grid of the Week and Day views: the timed blocks of a day with
// their columns, the all-day row's lanes, the now line, and how a block is
// drawn. Positions are wall-clock minutes from midnight (0–1440), so the
// grid always has 24 rows: on the day the clocks spring forward an hour is
// missing from the events, not from the grid; on the day they fall back
// the repeated hour folds onto one row (a block of that hour keeps the
// minimum height).

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.I18n;

namespace Malachi.Core.Calendars;

public static partial class Cal
{
    /// <summary>The height of the grid in minutes.</summary>
    public const int DayMinutes = 24 * 60;

    /// <summary>The shortest block drawn.</summary>
    public const int MinBlockMinutes = 15;

    /// <summary>week.go <c>BlockState</c>: how a block, bar or pill is drawn.</summary>
    public enum BlockState
    {
        /// <summary>Filled with the calendar's colour.</summary>
        Normal,

        /// <summary>Answered tentatively, or tentative by status or show-as: diagonal stripes.</summary>
        Tentative,

        /// <summary>An invitation waiting for the user's answer: a dashed outline with a "?" badge.</summary>
        Unanswered,

        /// <summary>Declined by the user: struck through (shown only with the preference showDeclined).</summary>
        Declined,

        /// <summary>The organiser cancelled it: struck through and faded.</summary>
        Cancelled,
    }

    /// <summary>
    /// week.go <c>StateOf</c>: how an occurrence is drawn: cancelled, then
    /// declined, then unanswered (an invitation not in a calendar, or one the
    /// user has not answered), then tentative.
    /// </summary>
    public static BlockState StateOf(Occurrence o)
    {
        ArgumentNullException.ThrowIfNull(o);
        var response = o.MyResponse?.Value;
        if (o.Status.Value == EventStatus.Cancelled)
        {
            return BlockState.Cancelled;
        }
        if (response == EventResponse.Declined)
        {
            return BlockState.Declined;
        }
        if (o.Invitation is not null || response == EventResponse.NeedsAction)
        {
            return BlockState.Unanswered;
        }
        if (response == EventResponse.Tentative || o.Status.Value == EventStatus.Tentative || o.ShowAs.Value == ShowAs.Tentative)
        {
            return BlockState.Tentative;
        }
        return BlockState.Normal;
    }

    /// <summary>week.go <c>Shown</c>: an occurrence the views draw: declined ones only with <paramref name="showDeclined"/>.</summary>
    public static bool Shown(Occurrence o, bool showDeclined)
    {
        ArgumentNullException.ThrowIfNull(o);
        return showDeclined || o.MyResponse?.Value != EventResponse.Declined;
    }

    /// <summary>week.go <c>Block</c>: a timed occurrence placed in a day's column.</summary>
    /// <param name="Occurrence">The occurrence.</param>
    /// <param name="Start">The occurrence's start clipped to the day.</param>
    /// <param name="End">The occurrence's end clipped to the day.</param>
    /// <param name="Top">Wall-clock minutes from midnight.</param>
    /// <param name="Bottom">Wall-clock minutes from midnight, at least <c>Top + MinBlockMinutes</c> and at most <see cref="DayMinutes"/>.</param>
    /// <param name="Column">The block's column (0-based) among <paramref name="Columns"/> side by side.</param>
    /// <param name="Columns">The columns of the block's cluster.</param>
    /// <param name="ContinuesBefore">The occurrence started the day before.</param>
    /// <param name="ContinuesAfter">The occurrence ends after this day.</param>
    /// <param name="State">How it is drawn.</param>
    public sealed record Block(
        Occurrence Occurrence,
        DateTimeOffset Start,
        DateTimeOffset End,
        int Top,
        int Bottom,
        int Column,
        int Columns,
        bool ContinuesBefore,
        bool ContinuesAfter,
        BlockState State);

    // week.go wallMinutes: t's wall-clock minutes from the midnight of the
    // day (1440 when t is at or after the next midnight).
    private static int WallMinutes(DateTimeOffset t, DateTimeOffset dayStart, DateTimeOffset dayEnd, TimeZoneInfo zone)
    {
        if (t >= dayEnd)
        {
            return DayMinutes;
        }
        if (t <= dayStart)
        {
            return 0;
        }
        var p = PartsOf(t, zone);
        return p.Hour * 60 + p.Minute;
    }

    /// <summary>
    /// week.go <c>LayoutDay</c>: places the timed occurrences that touch
    /// <paramref name="day"/> (any time of it) in columns: occurrences are
    /// sorted by start, then the longer first, then by
    /// <see cref="OccurrenceKey"/>; each joins the first column whose last
    /// block ended by its start (greedy interval colouring); a cluster of
    /// transitively overlapping blocks shares one column count. All-day
    /// occurrences and, without <paramref name="showDeclined"/>, declined
    /// ones are left out.
    /// </summary>
    public static IReadOnlyList<Block> LayoutDay(
        IReadOnlyList<Occurrence> occurrences, DateTimeOffset day, TimeZoneInfo zone, bool showDeclined)
    {
        ArgumentNullException.ThrowIfNull(occurrences);
        var dayStart = StartOfDay(day, zone);
        var dayEnd = AddDays(dayStart, 1, zone);
        var blocks = new List<Block>();
        foreach (var o in occurrences)
        {
            if (o is null || o.AllDay || !Shown(o, showDeclined) || !Overlaps(o, dayStart, dayEnd, zone)
                || Span(o, zone) is not (var s, var e))
            {
                continue;
            }
            var start = s > dayStart ? s : dayStart;
            var end = e < dayEnd ? e : dayEnd;
            var top = WallMinutes(start, dayStart, dayEnd, zone);
            var bottom = WallMinutes(end, dayStart, dayEnd, zone);
            if (bottom < top + MinBlockMinutes)
            {
                bottom = top + MinBlockMinutes;
            }
            if (bottom > DayMinutes)
            {
                bottom = DayMinutes;
                top = DayMinutes - MinBlockMinutes;
            }
            blocks.Add(new Block(o, start, end, top, bottom, 0, 0, s < dayStart, e > dayEnd, StateOf(o)));
        }
        var sorted = StableSorted(blocks, (a, b) => Or(
            Compare(a.Top, b.Top),
            Compare(b.Bottom - b.Top, a.Bottom - a.Top),
            Compare(a.Start, b.Start),
            CompareBytes(OccurrenceKey(a.Occurrence), OccurrenceKey(b.Occurrence))));
        AssignColumns(sorted);
        return sorted;
    }

    // week.go assignColumns: colours the sorted blocks cluster by cluster.
    private static void AssignColumns(List<Block> blocks)
    {
        var start = 0;
        var clusterEnd = -1;
        var ends = new List<int>(); // the bottom of each column's last block
        void Flush(int to)
        {
            for (var i = start; i < to; i++)
            {
                blocks[i] = blocks[i] with { Columns = ends.Count };
            }
        }
        for (var i = 0; i < blocks.Count; i++)
        {
            var b = blocks[i];
            if (b.Top >= clusterEnd)
            {
                Flush(i);
                start = i;
                ends.Clear();
            }
            var col = ends.FindIndex(end => end <= b.Top);
            if (col < 0)
            {
                col = ends.Count;
                ends.Add(0);
            }
            ends[col] = b.Bottom;
            blocks[i] = b with { Column = col };
            clusterEnd = Math.Max(clusterEnd, b.Bottom);
        }
        Flush(blocks.Count);
    }

    /// <summary>week.go <c>Bar</c>: an all-day occurrence in the all-day row of a range of days.</summary>
    /// <param name="Occurrence">The occurrence.</param>
    /// <param name="Lane">The bar's row (0-based) in the all-day area.</param>
    /// <param name="First">The index of the bar's first day in the days given.</param>
    /// <param name="Last">The index of the bar's last day in the days given.</param>
    /// <param name="ContinuesBefore">The event goes on before the first day shown.</param>
    /// <param name="ContinuesAfter">The event goes on after the last day shown.</param>
    /// <param name="State">How it is drawn.</param>
    public sealed record Bar(
        Occurrence Occurrence, int Lane, int First, int Last, bool ContinuesBefore, bool ContinuesAfter, BlockState State);

    /// <summary>
    /// week.go <c>AllDayLanes</c>: places the all-day occurrences that touch
    /// <paramref name="days"/> (consecutive midnights, a week or a day) in
    /// lanes: sorted by first day, then the longer first, then by
    /// <see cref="OccurrenceKey"/>; each takes the lowest lane free over its
    /// days. <c>Lanes</c> is the number of lanes.
    /// </summary>
    public static (IReadOnlyList<Bar> Bars, int Lanes) AllDayLanes(
        IReadOnlyList<Occurrence> occurrences, IReadOnlyList<DateTimeOffset> days, TimeZoneInfo zone, bool showDeclined)
    {
        ArgumentNullException.ThrowIfNull(occurrences);
        ArgumentNullException.ThrowIfNull(days);
        if (days.Count == 0)
        {
            return ([], 0);
        }
        var from = days[0];
        var to = AddDays(days[^1], 1, zone);
        var bars = new List<Bar>();
        foreach (var o in occurrences)
        {
            if (o is null || !o.AllDay || !Shown(o, showDeclined) || !Overlaps(o, from, to, zone)
                || Span(o, zone) is not (var s, var e))
            {
                continue;
            }
            var first = Math.Max(0, DaysBetween(from, s, zone));
            var last = Math.Min(days.Count - 1, DaysBetween(from, e, zone) - 1);
            bars.Add(new Bar(o, 0, first, last, s < from, e > to, StateOf(o)));
        }
        var sorted = StableSorted(bars, (a, b) => Or(
            Compare(a.First, b.First),
            Compare(b.Last - b.First, a.Last - a.First),
            CompareBytes(OccurrenceKey(a.Occurrence), OccurrenceKey(b.Occurrence))));
        var laneEnds = new List<int>(); // the last day taken in each lane
        for (var i = 0; i < sorted.Count; i++)
        {
            var bar = sorted[i];
            var lane = laneEnds.FindIndex(end => end < bar.First);
            if (lane < 0)
            {
                lane = laneEnds.Count;
                laneEnds.Add(0);
            }
            laneEnds[lane] = bar.Last;
            sorted[i] = bar with { Lane = lane };
        }
        return (sorted, laneEnds.Count);
    }

    /// <summary>
    /// week.go <c>NowLine</c>: where the red line of now goes: the index of
    /// today among the range's days and the wall-clock minute; null when
    /// <paramref name="now"/> is outside <paramref name="r"/>.
    /// </summary>
    public static (int Day, int Minute)? NowLine(DateTimeOffset now, Range r, TimeZoneInfo zone)
    {
        ArgumentNullException.ThrowIfNull(r);
        if (!r.Contains(now))
        {
            return null;
        }
        var p = PartsOf(now, zone);
        return (DaysBetween(r.From, now, zone), p.Hour * 60 + p.Minute);
    }

    /// <summary>
    /// week.go <c>ScrollMinute</c>: the minute a time grid scrolls to at its
    /// first show: the start of the working hours ("HH:MM"), an hour before
    /// when there is room; 7:00 when the preference cannot be read.
    /// </summary>
    public static int ScrollMinute(WorkingHours wh)
    {
        ArgumentNullException.ThrowIfNull(wh);
        return Math.Max(0, (ParseClock(wh.Start) ?? 8 * 60) - 60);
    }

    /// <summary>
    /// week.go <c>ParseClock</c>: "HH:MM" (Go's layout "15:04": a one- or
    /// two-digit hour below 24, two-digit minutes below 60, nothing else) in
    /// minutes from midnight; null for anything else.
    /// </summary>
    public static int? ParseClock(string? s)
    {
        if (s is null)
        {
            return null;
        }
        int? Digit(int i) => i < s.Length && s[i] is >= '0' and <= '9' ? s[i] - '0' : null;
        if (Digit(0) is not { } h)
        {
            return null;
        }
        var i = 1;
        if (Digit(1) is { } d)
        {
            h = h * 10 + d;
            i = 2;
        }
        if (h >= 24 || i >= s.Length || s[i] != ':' || Digit(i + 1) is not { } m1 || Digit(i + 2) is not { } m2
            || i + 3 != s.Length)
        {
            return null;
        }
        var m = m1 * 10 + m2;
        return m < 60 ? h * 60 + m : null;
    }

    /// <summary>week.go <c>BlockStateText</c>: the spoken state of a block ("" for a normal one).</summary>
    public static string BlockStateText(BlockState s) => s switch
    {
        // TRANSLATORS: spoken after an event of the calendar the user
        // answered tentatively.
        BlockState.Tentative => L10n.T("tentative"),
        // TRANSLATORS: spoken after an event of the calendar the user has
        // not answered yet.
        BlockState.Unanswered => L10n.T("awaiting your answer"),
        // TRANSLATORS: spoken after an event the user declined.
        BlockState.Declined => L10n.T("declined"),
        // TRANSLATORS: spoken after an event the organiser cancelled.
        BlockState.Cancelled => L10n.T("cancelled"),
        _ => "",
    };

    /// <summary>
    /// week.go <c>BlockLabel</c>: a block's accessibility label: "Standup,
    /// Tuesday 10:00–10:30, awaiting your answer".
    /// </summary>
    public static string BlockLabel(Occurrence o, Env env)
    {
        ArgumentNullException.ThrowIfNull(o);
        ArgumentNullException.ThrowIfNull(env);
        var title = CleanTitle(o.Title);
        var when = "";
        if (Span(o, env.Zone) is (var s, var e))
        {
            when = o.AllDay
                // TRANSLATORS: the time of an all-day event when spoken: the
                // weekday, e.g. "Tuesday, all day".
                ? L10n.T("%s, all day", Text.WeekdayText(s, env))
                // TRANSLATORS: the time of an event when spoken: the weekday and
                // the time range, e.g. "Tuesday 10:00–10:30".
                : L10n.T("%s %s", Text.WeekdayText(s, env), Text.TimeRangeText(s, e, env));
        }
        var state = BlockStateText(StateOf(o));
        if (when.Length == 0)
        {
            return title;
        }
        if (state.Length == 0)
        {
            // TRANSLATORS: an event of the calendar when spoken: its title and
            // its time.
            return L10n.T("%s, %s", title, when);
        }
        // TRANSLATORS: an event of the calendar when spoken: its title, its
        // time and its state, e.g. "Standup, Tuesday 10:00–10:30, awaiting
        // your answer".
        return L10n.T("%s, %s, %s", title, when, state);
    }
}
