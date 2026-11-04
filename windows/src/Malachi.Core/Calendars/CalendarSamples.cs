// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Calendar/CalendarSamples.swift
// (Cal.sampleSnapshot); Swift-first, no GTK counterpart (the GTK client
// has no calendar source yet).
//
// MALACHI_CALENDAR_SAMPLES (docs/calendar.md §5.10): the invented sample
// data of Cal.SampleData as a source's snapshot. Swift's extension of
// InMemoryCalendarSource (samplesRequested, samples()) is in
// InMemoryCalendarSource.cs, as the board's Dummy is in its source.
// DEVELOPMENT AID, never the owner's data.

using System;

namespace Malachi.Core.Calendars;

public static partial class Cal
{
    /// <summary>Swift <c>Cal.sampleSnapshot</c>: the snapshot of the sample data, ready.</summary>
    public static Snapshot SampleSnapshot(Samples d)
    {
        ArgumentNullException.ThrowIfNull(d);
        return new Snapshot
        {
            Phase = Phase.Ready,
            Accounts = d.Accounts,
            CalendarAccounts = d.Calendars.Accounts,
            Unanswered = d.Calendars.Unanswered,
            Invitations = d.Invitations,
            Preferences = d.Preferences,
        };
    }
}
