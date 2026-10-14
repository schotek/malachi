// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Board/BoardRemind.swift; GTK:
// ui/internal/board/remind.go (RemindChoice, RemindItem, RemindPresets,
// laterToday) and text.go (RemindKinds).
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
        /// Three hours from now rounded up to the hour, offered only when that
        /// is 20:00 or earlier the same day and now is before 19:00.
        /// </summary>
        LaterToday,

        /// <summary>09:00 tomorrow.</summary>
        Tomorrow,

        /// <summary>09:00 next Monday (a week ahead on a Monday).</summary>
        NextWeek,

        /// <summary>20:00 today, offered from 17:00 to 18:59.</summary>
        ThisEvening,

        /// <summary>09:00 today, offered before 05:00 in place of <see cref="Tomorrow"/>.</summary>
        ThisMorning,
    }

    /// <summary>Every preset kind (Go <c>RemindKinds</c>).</summary>
    public static IReadOnlyList<RemindPresetKind> RemindKinds { get; } =
    [
        RemindPresetKind.LaterToday, RemindPresetKind.Tomorrow, RemindPresetKind.NextWeek,
        RemindPresetKind.ThisEvening, RemindPresetKind.ThisMorning,
    ];

    /// <summary>A remind time the menu offers (Go <c>RemindChoice</c>).</summary>
    /// <param name="Kind">Which preset.</param>
    /// <param name="Date">When.</param>
    /// <param name="Title">"Later Today".</param>
    /// <param name="When">"Thu at 18:00" (<see cref="Text.DayAndTime"/> of the weekday and the time).</param>
    /// <param name="Label"><paramref name="Title"/> and <paramref name="When"/> as one menu item (<see cref="Text.RemindItem"/>).</param>
    public sealed record RemindPreset(RemindPresetKind Kind, DateTimeOffset Date, string Title, string When, string Label);

    /// <summary>The hour of This Morning, Tomorrow and Next Week.</summary>
    internal const int RemindMorningHour = 9;

    /// <summary>This Evening's hour, and the latest Later Today may be.</summary>
    internal const int RemindEveningHour = 20;

    /// <summary>The hour from which This Evening is offered (until <see cref="RemindNightFrom"/>).</summary>
    internal const int RemindEveningFrom = 17;

    /// <summary>The hour from which neither Later Today nor This Evening is offered.</summary>
    internal const int RemindNightFrom = 19;

    /// <summary>The hour before which a night still belongs to the day before: Tomorrow then means this morning (This Morning).</summary>
    internal const int RemindDawn = 5;

    /// <summary>
    /// The presets for <paramref name="now"/>, in menu order (which is the
    /// order of their times), by the wall clock of
    /// <paramref name="timeZone"/>: Later Today (three hours from now
    /// rounded up to the hour, only when that is 20:00 or earlier the same
    /// day, so not from 17:01, and never from 19:00); This Evening (20:00,
    /// from 17:00 to 18:59); Tomorrow (09:00 tomorrow; before 05:00 the
    /// night still belongs to yesterday, so it is 09:00 today, named This
    /// Morning); Next Week (09:00 next Monday, a week ahead on a Monday). A
    /// time an earlier preset offers already (This Evening at 17:00 is Later
    /// Today; Next Week on a Sunday is Tomorrow) is offered once, under the
    /// first. Every preset lies after now.
    /// </summary>
    public static IReadOnlyList<RemindPreset> RemindPresets(
        DateTimeOffset now, CultureInfo? culture = null, TimeZoneInfo? timeZone = null)
    {
        culture ??= CultureInfo.CurrentCulture;
        timeZone ??= TimeZoneInfo.Local;
        var output = new List<RemindPreset>();
        void Add(RemindPresetKind kind, DateTimeOffset? date)
        {
            if (date is not { } d || d <= now || output.Exists(p => p.Date == d))
            {
                return;
            }
            var title = Text.RemindPreset(kind);
            var when = Text.DayAndTime(Strftime.Format(d, "%a", culture, timeZone), DateFormat.FormatTime(d, culture, timeZone));
            output.Add(new RemindPreset(kind, d, title, when, Text.RemindItem(title, when)));
        }
        var today = LocalDate(now, timeZone);
        var hour = TimeZoneInfo.ConvertTime(now, timeZone).Hour;
        Add(RemindPresetKind.LaterToday, LaterToday(now, timeZone));
        if (hour is >= RemindEveningFrom and < RemindNightFrom)
        {
            Add(RemindPresetKind.ThisEvening, AtWallClock(today, RemindEveningHour, timeZone));
        }
        if (hour < RemindDawn)
        {
            Add(RemindPresetKind.ThisMorning, AtWallClock(today, RemindMorningHour, timeZone));
        }
        else
        {
            Add(RemindPresetKind.Tomorrow, AtWallClock(today.AddDays(1), RemindMorningHour, timeZone));
        }
        var ahead = ((int)DayOfWeek.Monday - (int)today.DayOfWeek + 7) % 7;
        if (ahead == 0)
        {
            ahead = 7;
        }
        Add(RemindPresetKind.NextWeek, AtWallClock(today.AddDays(ahead), RemindMorningHour, timeZone));
        return output;
    }

    // Later Today's time, null when it is not offered: three hours from now
    // rounded up to the hour of the wall clock (an exact hour stays), when
    // that is the same day at 20:00 or earlier and now is before 19:00 and
    // not before dawn (05:00: the night still belongs to the day before,
    // This Morning is offered).
    private static DateTimeOffset? LaterToday(DateTimeOffset now, TimeZoneInfo zone)
    {
        var hour = TimeZoneInfo.ConvertTime(now, zone).Hour;
        if (hour >= RemindNightFrom || hour < RemindDawn)
        {
            return null;
        }
        var plus3 = now.AddHours(3);
        var local = TimeZoneInfo.ConvertTime(plus3, zone);
        var rounded = plus3 - new TimeSpan(local.TimeOfDay.Ticks % TimeSpan.TicksPerHour);
        if (rounded < plus3)
        {
            rounded = rounded.AddHours(1);
        }
        return rounded > AtWallClock(LocalDate(now, zone), RemindEveningHour, zone) ? null : rounded;
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
