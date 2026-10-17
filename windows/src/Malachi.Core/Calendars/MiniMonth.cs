// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Calendar/MiniMonth.swift; GTK:
// ui/internal/calendar/month.go (MonthCells, WeekdayHeaders, which it is
// built from; the sidebar's month has no Go file of its own).
//
// The sidebar's month (docs/calendar.md §5.2): the 6×7 days of a month,
// today, and the band of the days the page shows (a day, a week, the
// month's grid or an agenda page), built from Cal.MonthCells. It has no
// texts but the day numbers and the weekday headers of the month view.

using System;
using System.Collections.Generic;
using System.Linq;

namespace Malachi.Core.Calendars;

public static partial class Cal
{
    /// <summary>The sidebar's month (Swift <c>Cal.MiniMonth</c>).</summary>
    public sealed record MiniMonth
    {
        /// <summary>
        /// The month of <paramref name="anchor"/>, with <paramref name="shown"/>
        /// banded. The shown range only colours days; it may lie partly or
        /// wholly outside the grid.
        /// </summary>
        public MiniMonth(DateTimeOffset anchor, Weekday weekStart, DateTimeOffset now, Range? shown, Env env)
        {
            ArgumentNullException.ThrowIfNull(env);
            var g = MonthCells(anchor, weekStart, now, env.Zone);
            Month = g.Month;
            Title = Text.MonthYearText(g.Month, env);
            Headers = WeekdayHeaders(weekStart, env);
            WeekNumbers = g.WeekNumbers;
            var flags = g.Cells.Select(c => shown?.Contains(c.Day) ?? false).ToArray();
            var days = new Day[g.Cells.Count];
            for (var i = 0; i < days.Length; i++)
            {
                var c = g.Cells[i];
                var col = i % 7;
                var on = flags[i];
                days[i] = new Day(
                    c.Day, Text.DayText(c.Day, env), c.InMonth, c.Today, c.Weekend, on,
                    BandStart: on && (col == 0 || !flags[i - 1]),
                    BandEnd: on && (col == 6 || i + 1 == flags.Length || !flags[i + 1]));
            }
            Days = days;
        }

        /// <summary>One day of the grid.</summary>
        /// <param name="Date">The midnight of the day.</param>
        /// <param name="Number">The day of the month, "14" (<see cref="Text.DayText"/>).</param>
        /// <param name="InMonth">The day belongs to the month (others are grey).</param>
        /// <param name="Today">The day is today.</param>
        /// <param name="Weekend">Saturday or Sunday.</param>
        /// <param name="Shown">The day is in the range the page shows (drawn banded).</param>
        /// <param name="BandStart">The first shown day of its row, for the band's rounded start.</param>
        /// <param name="BandEnd">The last shown day of its row, for the band's rounded end.</param>
        public sealed record Day(
            DateTimeOffset Date, string Number, bool InMonth, bool Today, bool Weekend, bool Shown, bool BandStart, bool BandEnd);

        /// <summary>The midnight of the month's 1st.</summary>
        public DateTimeOffset Month { get; }

        /// <summary>"October 2026".</summary>
        public string Title { get; }

        /// <summary>The seven weekday headers from the week's first day.</summary>
        public IReadOnlyList<string> Headers { get; }

        /// <summary>The 42 days, row by row.</summary>
        public IReadOnlyList<Day> Days { get; }

        /// <summary>The six rows' week numbers.</summary>
        public IReadOnlyList<int> WeekNumbers { get; }

        /// <summary>The anchor of the month before (<paramref name="dir"/> -1) or after (+1): its 1st.</summary>
        public static DateTimeOffset Step(DateTimeOffset month, int dir, TimeZoneInfo zone)
        {
            var p = PartsOf(month, zone);
            return Date(p.Year, p.Month + dir, 1, zone);
        }

        /// <summary>The index of the day <paramref name="t"/> falls on in <paramref name="zone"/>; null outside the grid (Swift <c>index(of:zone:)</c>).</summary>
        public int? IndexOf(DateTimeOffset t, TimeZoneInfo zone)
        {
            var d = StartOfDay(t, zone);
            for (var i = 0; i < Days.Count; i++)
            {
                if (Days[i].Date == d)
                {
                    return i;
                }
            }
            return null;
        }

        /// <summary>Whether both show the same month, days and band.</summary>
        public bool Equals(MiniMonth? other) =>
            other is not null && Month == other.Month && Title == other.Title && Headers.SequenceEqual(other.Headers)
            && Days.SequenceEqual(other.Days) && WeekNumbers.SequenceEqual(other.WeekNumbers);

        /// <inheritdoc/>
        public override int GetHashCode() => HashCode.Combine(Month, Title, Days.Count);
    }
}
