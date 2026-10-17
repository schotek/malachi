// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Calendar/CalendarToday.swift; GTK:
// ui/internal/calendar/today.go (TodayRow, TodayRows, ShowsTodayStrip,
// TodayHeading, OpenCalendar, TookPlace, Respond, NothingToday).
//
// The Board → Today strip "Today in Calendar": today's occurrences with
// their time range (or All Day), title and place, "took place" for the
// ones over, Respond for unanswered invitations, and Open Calendar. The
// strip is hidden when no account has the calendar capability. Its rows
// are the agenda of one day (CalendarAgenda.cs).

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.I18n;

namespace Malachi.Core.Calendars;

public static partial class Cal
{
    /// <summary>today.go <c>TodayRow</c>: one row of the strip.</summary>
    /// <param name="Occurrence">The occurrence.</param>
    /// <param name="Time">The time range, All Day, or a part of an occurrence over several days (<see cref="ItemTime"/>).</param>
    /// <param name="Title">The cleaned title, <see cref="Text.Untitled"/> when empty.</param>
    /// <param name="Place">The cleaned location ("" none).</param>
    /// <param name="TookPlace">The occurrence ended before now.</param>
    /// <param name="Respond">An unanswered invitation (the row offers Respond).</param>
    /// <param name="State">How it is drawn.</param>
    public sealed record TodayRow(
        Occurrence Occurrence, string Time, string Title, string Place, bool TookPlace, bool Respond, BlockState State);

    /// <summary>
    /// today.go <c>TodayRows</c>: the strip's rows of <paramref name="now"/>'s
    /// day: all-day first, then by start (<see cref="Agenda"/>'s order);
    /// empty without occurrences that day.
    /// </summary>
    public static IReadOnlyList<TodayRow> TodayRows(IReadOnlyList<Occurrence> occurrences, DateTimeOffset now, Env env, bool showDeclined)
    {
        var days = Agenda(occurrences, now, 1, now, env, showDeclined);
        if (days.Count == 0)
        {
            return [];
        }
        return
        [
            .. days[0].Items.Select(it => new TodayRow(
                it.Occurrence,
                ItemTime(it, env),
                CleanTitle(it.Occurrence.Title),
                CleanLine(it.Occurrence.Location),
                TookPlace: !(it.End > now),
                Respond: it.State == BlockState.Unanswered,
                it.State)),
        ];
    }

    /// <summary>today.go <c>ShowsTodayStrip</c>: the strip shows: some account has the calendar capability.</summary>
    public static bool ShowsTodayStrip(IReadOnlyList<Account>? accounts) =>
        accounts?.Any(a => a is not null && a.Can(Capability.Calendar)) ?? false;

    /// <summary>today.go <c>TodayHeading</c>: the strip's heading.</summary>
    public static string TodayHeading =>
        // TRANSLATORS: the heading of the Board's Today page strip with today's
        // events.
        L10n.T("Today in Calendar");

    /// <summary>today.go <c>OpenCalendar</c>: the strip's link to Calendar mode.</summary>
    public static string OpenCalendar => L10n.T("Open Calendar");

    /// <summary>today.go <c>TookPlace</c>: the note of a row that is over.</summary>
    public static string TookPlace =>
        // TRANSLATORS: beside an event of today that is already over.
        L10n.T("took place");

    /// <summary>today.go <c>Respond</c>: the button of an unanswered invitation's row.</summary>
    public static string Respond =>
        // TRANSLATORS: the button that answers an invitation from the Board's
        // Today page.
        L10n.T("Respond");

    /// <summary>today.go <c>NothingToday</c>: the strip's text without events today.</summary>
    public static string NothingToday => L10n.T("Nothing in the calendar today.");
}
