// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Calendar/CalendarAgendaViewController.swift
// (AgendaEntry, AgendaItemCell.configure's badge) and
// CalendarSearchController.swift (entries); GTK: the calendar's views
// follow (docs/calendar.md §6.4).
//
// A line of the agenda list: a day's heading, or an occurrence with the
// text of its time column (Cal.ItemTime, or for a search result its day
// before its time), its colour, title, place and badge: the response for
// an unanswered, tentative or declined occurrence, "cancelled", else "Day
// 2 of 3" for a part of a longer one. Every text is cleaned for display;
// the key keeps the list's rows in place across a refresh (a heading by
// its day, an occurrence by its key and day).

using System;
using System.Globalization;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Windows.UI;

namespace Malachi.App.Calendars;

/// <summary>A line of the agenda list (macOS <c>AgendaEntry</c>).</summary>
/// <param name="Key">The line's identity in the list.</param>
/// <param name="IsHeading">A day's heading, not an occurrence.</param>
/// <param name="Heading">The heading's text ("" for an occurrence).</param>
/// <param name="Time">The time column.</param>
/// <param name="Title">The occurrence's title.</param>
/// <param name="Place">Its place.</param>
/// <param name="Badge">Its badge ("" none).</param>
/// <param name="Color">Its calendar's colour (the dot).</param>
/// <param name="Struck">Its title is struck through (declined, cancelled).</param>
/// <param name="Label">What a screen reader says of the line.</param>
/// <param name="Occurrence">The occurrence (null for a heading).</param>
public sealed record AgendaEntry(
    string Key,
    bool IsHeading,
    string Heading,
    string Time,
    string Title,
    string Place,
    string Badge,
    Color Color,
    bool Struck,
    string Label,
    Occurrence? Occurrence)
{
    /// <summary>The heading line of <paramref name="day"/>.</summary>
    internal static AgendaEntry DayHeading(Cal.AgendaDay day) =>
        new("d:" + day.Day.ToUnixTimeSeconds().ToString(CultureInfo.InvariantCulture), true, day.Heading, "", "", "", "", default, false, day.Heading, null);

    /// <summary>The line of <paramref name="item"/> with <paramref name="time"/> in its time column, keyed <paramref name="key"/>.</summary>
    internal static AgendaEntry Item(string key, Cal.AgendaItem item, string time, Color color, Cal.Env env)
    {
        ArgumentNullException.ThrowIfNull(item);
        var o = item.Occurrence;
        var badge = item.State switch
        {
            Cal.BlockState.Unanswered or Cal.BlockState.Tentative or Cal.BlockState.Declined => Cal.ResponseLabel(o.MyResponse),
            Cal.BlockState.Cancelled => Cal.BlockStateText(Cal.BlockState.Cancelled),
            _ => "",
        };
        if (badge.Length == 0 && item.Days > 1)
        {
            badge = Cal.DayOfText(item.DayOf, item.Days);
        }
        return new(
            key, false, "", CalendarDisplay.Clean(time), CalendarDisplay.Title(o.Title), CalendarDisplay.Line(o.Location), badge, color,
            item.State is Cal.BlockState.Declined or Cal.BlockState.Cancelled, CalendarDisplay.Clean(Cal.BlockLabel(o, env)), o);
    }
}
