// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Calendar/CalendarMonthLayout.swift, the
// grid part (Cell, MonthGrid, gridStart, monthCells, weekdayHeaders); GTK:
// ui/internal/calendar/month.go (Cell, MonthGrid, gridStart, MonthCells,
// WeekdayHeaders).
//
// The Month view's 6×7 grid of days from the week that holds the 1st, with
// today, the weekends and the week numbers: what ViewRange's month range
// and the sidebar's MiniMonth stand on. The rest of month.go (CellCapacity,
// MonthSpan, MonthLanes, CellItems, Pill, More) is CalendarMonthLayout.cs.

using System;
using System.Collections.Generic;
using System.Linq;

namespace Malachi.Core.Calendars;

public static partial class Cal
{
    /// <summary>month.go <c>Cell</c>: one day of the month grid.</summary>
    /// <param name="Day">The midnight of the day.</param>
    /// <param name="InMonth">The day belongs to the grid's month (others are grey).</param>
    /// <param name="Today">The day is today (ringed).</param>
    /// <param name="Weekend">Saturday or Sunday (shaded).</param>
    public sealed record Cell(DateTimeOffset Day, bool InMonth, bool Today, bool Weekend);

    /// <summary>month.go <c>MonthGrid</c>: the month view of one month.</summary>
    public sealed record MonthGrid
    {
        /// <summary>The midnight of the month's 1st.</summary>
        public required DateTimeOffset Month { get; init; }

        /// <summary>The 42 days, row by row.</summary>
        public IReadOnlyList<Cell> Cells { get; init => field = value ?? []; } = [];

        /// <summary>The numbers of the six rows' weeks (<see cref="WeekNumber"/>).</summary>
        public IReadOnlyList<int> WeekNumbers { get; init => field = value ?? []; } = [];

        /// <summary>Whether both are the same month with the same cells.</summary>
        public bool Equals(MonthGrid? other) =>
            other is not null && Month == other.Month && Cells.SequenceEqual(other.Cells) && WeekNumbers.SequenceEqual(other.WeekNumbers);

        /// <inheritdoc/>
        public override int GetHashCode() => HashCode.Combine(Month, Cells.Count);
    }

    /// <summary>
    /// month.go <c>gridStart</c>: the first day of the grid of
    /// <paramref name="anchor"/>'s month: the start of the week that holds
    /// the 1st.
    /// </summary>
    internal static DateTimeOffset GridStart(DateTimeOffset anchor, Weekday weekStart, TimeZoneInfo zone)
    {
        var a = PartsOf(anchor, zone);
        return StartOfWeek(Date(a.Year, a.Month, 1, zone), weekStart, zone);
    }

    /// <summary>
    /// month.go <c>MonthCells</c>: the grid of <paramref name="anchor"/>'s
    /// month: six weeks from the week that holds the 1st (a month that
    /// starts on the week's first day starts in the first row), with today
    /// (<paramref name="now"/>'s day) marked.
    /// </summary>
    public static MonthGrid MonthCells(DateTimeOffset anchor, Weekday weekStart, DateTimeOffset now, TimeZoneInfo zone)
    {
        var a = PartsOf(anchor, zone);
        var cells = new List<Cell>(42);
        var weeks = new List<int>(6);
        var d = GridStart(anchor, weekStart, zone);
        var today = StartOfDay(now, zone);
        for (var i = 0; i < 42; i++)
        {
            var p = PartsOf(d, zone);
            cells.Add(new Cell(d, p.Month == a.Month, d == today, p.Weekday is Weekday.Saturday or Weekday.Sunday));
            if (i % 7 == 0)
            {
                weeks.Add(WeekNumber(d, weekStart, zone));
            }
            d = AddDays(d, 1, zone);
        }
        return new MonthGrid { Month = Date(a.Year, a.Month, 1, zone), Cells = cells, WeekNumbers = weeks };
    }

    /// <summary>
    /// month.go <c>WeekdayHeaders</c>: the seven days of the grid's header
    /// from <paramref name="weekStart"/>, abbreviated ("Mon").
    /// </summary>
    public static IReadOnlyList<string> WeekdayHeaders(Weekday weekStart, Env env)
    {
        ArgumentNullException.ThrowIfNull(env);
        // Any week will do: 2026-10-04 is a Sunday.
        var sunday = Date(2026, 10, 4, 12, 0, env.Zone);
        var output = new string[7];
        for (var i = 0; i < 7; i++)
        {
            output[i] = Text.ShortWeekdayText(AddDays(sunday, ((int)weekStart + i) % 7, env.Zone), env);
        }
        return output;
    }
}
