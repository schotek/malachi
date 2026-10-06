// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Board/BoardRemind.swift; GTK:
// ui/internal/board/remind.go (RemindChoice, RemindPresets, laterToday).
//
// Remind…: the presets the button's menu and the case menu offer. A remind
// hides a case until its time (board.remind); the daemon takes any time in
// the future up to a year ahead, so every preset lies after now. Swift's
// Calendar is the time zone that says what a day is and the culture that
// writes the weekday and the time (the current ones when null); a wall
// clock time a change of daylight saving time skips is the first one after
// it, a repeated one the earlier of the two.

using System;
using System.Collections.Generic;
using System.Globalization;
using Malachi.Core.I18n;
using DateFormat = Malachi.Core.Text.Format;

namespace Malachi.Core.Boards;

public static partial class Board
{
    /// <summary>A remind preset (Swift <c>RemindPreset.Kind</c>, Go <c>RemindKind</c>).</summary>
    public enum RemindPresetKind
    {
        /// <summary>
        /// Three hours from now rounded up to the hour, or 18:00 when that
        /// comes first and is an hour away or more; none when neither is today.
        /// </summary>
        LaterToday,

        /// <summary>09:00 tomorrow.</summary>
        Tomorrow,

        /// <summary>09:00 next Monday (a week ahead on a Monday).</summary>
        NextWeek,
    }

    /// <summary>A remind time the menu offers.</summary>
    /// <param name="Kind">Which preset.</param>
    /// <param name="Date">When.</param>
    /// <param name="Title">"Later Today".</param>
    /// <param name="When">"Thu 18:00".</param>
    public sealed record RemindPreset(RemindPresetKind Kind, DateTimeOffset Date, string Title, string When);

    /// <summary>The hour of the morning presets.</summary>
    internal const int RemindMorningHour = 9;

    /// <summary>The hour Later Today does not go past while it is an hour away.</summary>
    internal const int RemindEveningHour = 18;

    /// <summary>
    /// The presets for <paramref name="now"/>, in menu order; Later Today is
    /// left out late in the evening, Next Week on a Sunday (it would be
    /// Tomorrow). The days are those of <paramref name="timeZone"/>.
    /// </summary>
    public static IReadOnlyList<RemindPreset> RemindPresets(
        DateTimeOffset now, CultureInfo? culture = null, TimeZoneInfo? timeZone = null)
    {
        culture ??= CultureInfo.CurrentCulture;
        timeZone ??= TimeZoneInfo.Local;
        var output = new List<RemindPreset>();
        void Add(RemindPresetKind kind, DateTimeOffset? date)
        {
            // A time an earlier preset offers already (Next Week on a Sunday
            // is Tomorrow) is offered once, under the first.
            if (date is not { } d || d <= now || output.Exists(p => p.Date == d))
            {
                return;
            }
            var when = Strftime.Format(d, "%a", culture, timeZone) + " " + DateFormat.FormatTime(d, culture, timeZone);
            output.Add(new RemindPreset(kind, d, Text.RemindPreset(kind), when));
        }
        Add(RemindPresetKind.LaterToday, LaterToday(now, timeZone));
        var today = LocalDate(now, timeZone);
        Add(RemindPresetKind.Tomorrow, AtWallClock(today.AddDays(1), RemindMorningHour, timeZone));
        var ahead = ((int)DayOfWeek.Monday - (int)today.DayOfWeek + 7) % 7;
        if (ahead == 0)
        {
            ahead = 7;
        }
        Add(RemindPresetKind.NextWeek, AtWallClock(today.AddDays(ahead), RemindMorningHour, timeZone));
        return output;
    }

    // Later Today's time; null when it would not be today.
    private static DateTimeOffset? LaterToday(DateTimeOffset now, TimeZoneInfo zone)
    {
        var plus3 = now.AddHours(3);
        // Rounded up to the hour of the wall clock: an exact hour stays.
        var local = TimeZoneInfo.ConvertTime(plus3, zone);
        var rounded = plus3 - new TimeSpan(local.TimeOfDay.Ticks % TimeSpan.TicksPerHour);
        if (rounded < plus3)
        {
            rounded = rounded.AddHours(1);
        }
        var pick = rounded;
        var today = LocalDate(now, zone);
        var evening = AtWallClock(today, RemindEveningHour, zone);
        if (evening < rounded && evening >= now.AddHours(1))
        {
            pick = evening;
        }
        return LocalDate(pick, zone) == today ? pick : null;
    }

    // The calendar day of t in zone.
    internal static DateTime LocalDate(DateTimeOffset t, TimeZoneInfo zone) => TimeZoneInfo.ConvertTime(t, zone).Date;

    // hour o'clock on day in zone (Swift date(bySettingHour:), Go
    // time.Date): a wall clock time a change of daylight saving time skips
    // is the first one after the gap, a repeated one the earlier instant.
    internal static DateTimeOffset AtWallClock(DateTime day, int hour, TimeZoneInfo zone)
    {
        var wall = DateTime.SpecifyKind(day.Date.AddHours(hour), DateTimeKind.Unspecified);
        while (zone.IsInvalidTime(wall))
        {
            wall = wall.AddMinutes(1);
        }
        TimeSpan offset;
        if (zone.IsAmbiguousTime(wall))
        {
            var offsets = zone.GetAmbiguousTimeOffsets(wall);
            offset = offsets[0] > offsets[^1] ? offsets[0] : offsets[^1];
        }
        else
        {
            offset = zone.GetUtcOffset(wall);
        }
        return new DateTimeOffset(wall, offset);
    }
}
