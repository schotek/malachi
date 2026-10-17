// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/InvitationReading.swift (its
// extension of Cal: cardClockLine, DayStrip, dayStripOf); Swift-first, no
// GTK file yet (docs/calendar.md §5.7, "As built (M8)"). The free functions
// of that Swift file are Model/InvitationReading.cs.
//
// The invitation card's clock line with the repetition, and the geometry
// of its mini day strip (InvitationCard.Day): the axis from Day.From to
// Day.To, its whole hours, the busy blocks and the invitation's own slot,
// as fractions of the axis, clipped to it. A hostile part cannot make the
// card draw much: no strip for a span under an hour or over two days, at
// most MaxBusy blocks and MaxTicks labels.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;

namespace Malachi.Core.Calendars;

public static partial class Cal
{
    /// <summary>
    /// The card's clock line with the repetition: "Tuesday 14 October 2026 ·
    /// 10:00–11:30 · 1 h 30 min · Every 2 weeks on Tuesday" (Swift
    /// <c>cardClockLine</c>); <see cref="CardTimeLine"/> alone without a
    /// start or a rule.
    /// </summary>
    public static string CardClockLine(InvitationCard c, Env env)
    {
        ArgumentNullException.ThrowIfNull(c);
        ArgumentNullException.ThrowIfNull(env);
        var line = CardTimeLine(c, env);
        if (CardStart(c, env.Zone) is not { } start)
        {
            return line;
        }
        return Text.Dots(line, RecurrenceText(c.Recurrence, start, env));
    }

    /// <summary>
    /// The geometry of the card's mini day strip (Swift <c>Cal.DayStrip</c>):
    /// fractions of the axis, 0 at its start, 1 at its end.
    /// </summary>
    public sealed record DayStrip
    {
        /// <summary>At most this many busy blocks are drawn (the daemon caps them too; a hostile part must not make the card draw thousands).</summary>
        public const int MaxBusy = 48;

        /// <summary>No more ticks than this; a longer axis labels every second hour, and so on.</summary>
        public const int MaxTicks = 13;

        /// <summary>A span on the axis.</summary>
        /// <param name="From">The start's fraction.</param>
        /// <param name="To">The end's fraction.</param>
        public sealed record Span(double From, double To);

        /// <summary>A labelled whole hour on the axis.</summary>
        /// <param name="At">Its fraction.</param>
        /// <param name="Label">"10:00" (<see cref="Text.TimeText"/>).</param>
        public sealed record Tick(double At, string Label);

        /// <summary>The whole hours on the axis, its ends included when whole.</summary>
        public IReadOnlyList<Tick> Ticks { get; init; } = [];

        /// <summary>The busy spans of the visible calendars, in time order.</summary>
        public IReadOnlyList<Span> Busy { get; init; } = [];

        /// <summary>The invitation's slot (null: an all-day invitation, or outside the axis).</summary>
        public Span? Proposed { get; init; }

        /// <summary>"10:00–11:30" for the slot ("" without one).</summary>
        public string ProposedLabel { get; init; } = "";

        /// <summary>Whether both have the same ticks, blocks and slot (Swift's Equatable).</summary>
        public bool Equals(DayStrip? other) =>
            other is not null && Ticks.SequenceEqual(other.Ticks) && Busy.SequenceEqual(other.Busy)
            && Equals(Proposed, other.Proposed) && ProposedLabel == other.ProposedLabel;

        /// <inheritdoc/>
        public override int GetHashCode() => HashCode.Combine(Ticks.Count, Busy.Count, Proposed, ProposedLabel);
    }

    /// <summary>
    /// The strip of <paramref name="c"/> (Swift <c>dayStripOf</c>); null when
    /// <c>Day</c> spans nothing (an unreadable part), runs backwards, or is
    /// longer than two days, which is not a day.
    /// </summary>
    public static DayStrip? DayStripOf(InvitationCard c, Env env)
    {
        ArgumentNullException.ThrowIfNull(c);
        ArgumentNullException.ThrowIfNull(env);
        var from = c.Day.From;
        var to = c.Day.To;
        var length = to - from;
        if (length < TimeSpan.FromHours(1) || length > TimeSpan.FromDays(2))
        {
            return null;
        }
        double Fraction(DateTimeOffset t) => Math.Min(1, Math.Max(0, (t - from).Ticks / (double)length.Ticks));
        DayStrip.Span? Clip(DateTimeOffset s, DateTimeOffset e) =>
            e > s && e > from && s < to ? new DayStrip.Span(Fraction(s), Fraction(e)) : null;

        // The whole hours of the zone's clock from the first at or after the
        // axis's start (a zone half an hour off UTC has its own).
        var hours = new List<DateTimeOffset>();
        var t = from.AddTicks(-(ToZone(from, env.Zone).DateTime.Ticks % TimeSpan.TicksPerHour));
        if (t < from)
        {
            t = t.AddHours(1);
        }
        while (t <= to && hours.Count <= 48)
        {
            hours.Add(t);
            t = t.AddHours(1);
        }
        var step = Math.Max(1, (hours.Count + DayStrip.MaxTicks - 1) / DayStrip.MaxTicks);
        var ticks = hours.Where((_, i) => i % step == 0).Select(h => new DayStrip.Tick(Fraction(h), Text.TimeText(h, env))).ToList();

        var busy = new List<DayStrip.Span>();
        foreach (var b in StableSorted(c.Day.Busy, (x, y) => Compare(x.Start, y.Start)).Take(DayStrip.MaxBusy))
        {
            if (Clip(b.Start, b.End) is { } span)
            {
                busy.Add(span);
            }
        }
        DayStrip.Span? proposed = null;
        var label = "";
        if (c.AllDay != true && c.Start is { } s && c.End is { } e && Clip(s, e) is { } p)
        {
            proposed = p;
            label = Text.TimeRangeText(s, e, env);
        }
        return new DayStrip { Ticks = ticks, Busy = busy, Proposed = proposed, ProposedLabel = label };
    }
}
