// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Calendar/CalendarAvailability.swift;
// GTK: ui/internal/calendar/availability.go (AxisSpan, AvailabilityRow,
// Busy, axisSpans, AvailabilityRows, You, Unknown, busyDuring, Summary,
// NextFreeSlot, NextFreeSlotButton, NextFreeSlotText, NoFreeSlot).
//
// The editor's availability rows (calendar.freeBusy, Graph only;
// docs/calendar.md §5.6): per person the busy spans on the editor's day
// axis, "availability unknown" for people without free/busy, the summary
// line of the proposed time and Next Free Slot (calendar.findFreeTime).
// The names and addresses are cleaned for display.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.I18n;

namespace Malachi.Core.Calendars;

public static partial class Cal
{
    /// <summary>availability.go <c>AxisSpan</c>: a busy span on an axis, as fractions 0–1 of its length.</summary>
    /// <param name="From">The start's fraction.</param>
    /// <param name="To">The end's fraction.</param>
    /// <param name="ShowAs">How the time is shown.</param>
    public sealed record AxisSpan(double From, double To, ShowAs ShowAs);

    /// <summary>availability.go <c>AvailabilityRow</c>: one person's row.</summary>
    /// <param name="Label">"You" or the attendee's name (else address), cleaned.</param>
    /// <param name="Address">The address the row is about, as asked.</param>
    /// <param name="Known">The person's free/busy is known.</param>
    /// <param name="Busy">The busy spans on the axis, by start (empty when unknown).</param>
    public sealed record AvailabilityRow(string Label, string Address, bool Known, IReadOnlyList<AxisSpan> Busy);

    /// <summary>
    /// availability.go <c>Busy</c>: a show-as that blocks a meeting: busy,
    /// out of office, tentative and unknown; free and working elsewhere do
    /// not.
    /// </summary>
    public static bool Busy(ShowAs s) => s != ShowAs.Free && s != ShowAs.WorkingElsewhere;

    /// <summary>Go's <c>axisSpans</c>: the blocking intervals of <paramref name="busy"/> placed on [<paramref name="from"/>, <paramref name="to"/>), by start.</summary>
    internal static IReadOnlyList<AxisSpan> AxisSpans(IReadOnlyList<BusyInterval> busy, DateTimeOffset from, DateTimeOffset to)
    {
        ArgumentNullException.ThrowIfNull(busy);
        var total = (double)(to - from).Ticks;
        if (total <= 0)
        {
            return [];
        }
        var output = new List<AxisSpan>();
        foreach (var b in busy)
        {
            if (!Busy(b.ShowAs) || b.End <= from || b.Start >= to || b.End <= b.Start)
            {
                continue;
            }
            var s = b.Start > from ? b.Start : from;
            var e = b.End < to ? b.End : to;
            output.Add(new AxisSpan((s - from).Ticks / total, (e - from).Ticks / total, b.ShowAs));
        }
        return StableSorted(output, (a, b) => Compare(a.From, b.From));
    }

    /// <summary>
    /// availability.go <c>AvailabilityRows</c>: the rows of a freeBusy result
    /// on the axis [<paramref name="from"/>, <paramref name="to"/>): the user
    /// first, then the attendees in the order asked; <paramref name="names"/>
    /// maps an address to the name the recipient field shows.
    /// </summary>
    public static IReadOnlyList<AvailabilityRow> AvailabilityRows(
        CalendarFreeBusyResult res, IReadOnlyDictionary<string, string>? names, DateTimeOffset from, DateTimeOffset to)
    {
        ArgumentNullException.ThrowIfNull(res);
        var rows = new List<AvailabilityRow> { new(You, res.Me.Address, true, AxisSpans(res.Me.Busy, from, to)) };
        foreach (var a in res.Attendees)
        {
            var label = CleanLine(names?.GetValueOrDefault(a.Address));
            if (label.Length == 0)
            {
                label = CleanLine(a.Address);
            }
            rows.Add(new AvailabilityRow(label, a.Address, a.Known, a.Known ? AxisSpans(a.Busy, from, to) : []));
        }
        return rows;
    }

    /// <summary>availability.go <c>You</c>: labels the user's own row.</summary>
    public static string You =>
        // TRANSLATORS: the user's own row of the availability rows.
        L10n.T("You");

    /// <summary>availability.go <c>Unknown</c>: the row text of a person without free/busy.</summary>
    public static string Unknown => L10n.T("availability unknown");

    // Whether busy blocks any of [start, end).
    private static bool BusyDuring(IReadOnlyList<BusyInterval> busy, DateTimeOffset start, DateTimeOffset end)
    {
        foreach (var b in busy)
        {
            if (Busy(b.ShowAs) && b.Start < end && b.End > start)
            {
                return true;
            }
        }
        return false;
    }

    /// <summary>
    /// availability.go <c>Summary</c>: the availability lines of the proposed
    /// time [<paramref name="start"/>, <paramref name="end"/>): "10:00–11:00:
    /// all known attendees are free" or "2 of 3 attendees are busy" (the user
    /// and unknown people left out), with the unknown count on a second line;
    /// empty without attendees.
    /// </summary>
    public static IReadOnlyList<string> Summary(CalendarFreeBusyResult res, DateTimeOffset start, DateTimeOffset end, Env env)
    {
        ArgumentNullException.ThrowIfNull(res);
        ArgumentNullException.ThrowIfNull(env);
        int known = 0, busy = 0, unknown = 0;
        foreach (var a in res.Attendees)
        {
            if (!a.Known)
            {
                unknown++;
                continue;
            }
            known++;
            if (BusyDuring(a.Busy, start, end))
            {
                busy++;
            }
        }
        var lines = new List<string>();
        if (known > 0 && busy == 0)
        {
            // TRANSLATORS: the availability summary; %s is the proposed time,
            // "10:00–11:00".
            lines.Add(L10n.T("%s: all known attendees are free", Text.TimeRangeText(start, end, env)));
        }
        else if (known > 0)
        {
            // TRANSLATORS: the availability summary: how many of the attendees
            // whose availability is known are busy at the proposed time.
            lines.Add(L10n.N("%d of %d attendees is busy", "%d of %d attendees are busy", busy, busy, known));
        }
        if (unknown > 0)
        {
            lines.Add(L10n.N("Availability of %d attendee is unknown", "Availability of %d attendees is unknown", unknown));
        }
        return lines;
    }

    /// <summary>
    /// availability.go <c>NextFreeSlot</c>: the first slot of a findFreeTime
    /// result that starts at or after <paramref name="after"/> and lasts;
    /// null when there is none.
    /// </summary>
    public static TimeSlot? NextFreeSlot(CalendarFindFreeTimeResult res, DateTimeOffset after)
    {
        ArgumentNullException.ThrowIfNull(res);
        TimeSlot? best = null;
        foreach (var s in res.Slots)
        {
            if (s.Start < after || s.End <= s.Start)
            {
                continue;
            }
            if (best is null || s.Start < best.Start)
            {
                best = s;
            }
        }
        return best;
    }

    /// <summary>availability.go <c>NextFreeSlotButton</c>: the button that moves the event to the next free slot.</summary>
    public static string NextFreeSlotButton => L10n.T("Next Free Slot");

    /// <summary>availability.go <c>NextFreeSlotText</c>: names the next free slot, "Next free slot: Wednesday 15 October 2026 · 14:00–15:00".</summary>
    public static string NextFreeSlotText(TimeSlot s, Env env)
    {
        ArgumentNullException.ThrowIfNull(s);
        ArgumentNullException.ThrowIfNull(env);
        // TRANSLATORS: the editor's availability: the next time everyone is
        // free; %s is its date and time.
        return L10n.T("Next free slot: %s", Text.Dots(Text.LongDateText(s.Start, env), Text.TimeRangeText(s.Start, s.End, env)));
    }

    /// <summary>availability.go <c>NoFreeSlot</c>: the text when findFreeTime found nothing.</summary>
    public static string NoFreeSlot => L10n.T("No free slot was found in the next two weeks.");
}
