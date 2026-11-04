// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Calendar/EventBlockView.swift
// (CalendarText.title, CalendarText.line); GTK: the calendar's views follow
// (docs/calendar.md §6.4).
//
// A server's strings for the calendar's views, cleaned the way Core cleans
// them (Board.CleanLine: control, bidi and invisible characters out, one
// line, a byte limit) and then as the app's chrome shows mail text
// (DisplayText.Clean, docs/windows-port.md §3). They go into TextBlock.Text
// only, never a parser.

using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.Calendars;
using Malachi.Core.Text;

namespace Malachi.App.Calendars;

/// <summary>Event titles, places and names as the calendar's views show them.</summary>
internal static class CalendarDisplay
{
    /// <summary>An event's title, Untitled Event when it draws nothing.</summary>
    public static string Title(string? s)
    {
        var t = DisplayText.CleanTrimmed(Board.CleanLine(s ?? "", API.Limits.MaxEventTitleBytes));
        return t.Length == 0 ? Cal.Text.Untitled : t;
    }

    /// <summary>A place or a name.</summary>
    public static string Line(string? s) => DisplayText.CleanTrimmed(Board.CleanLine(s ?? "", API.Limits.MaxEventLocationBytes));

    /// <summary>A text Core composed (it carries a server's strings).</summary>
    public static string Clean(string? s) => DisplayText.Clean(s);
}
