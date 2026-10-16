// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// MALACHI_CALENDAR_SAMPLES (docs/calendar.md §5.10): the invented sample
// data of `Cal.sampleData` as a source. Read once at start, like
// MALACHI_BOARD_SAMPLES; a window then gets `InMemoryCalendarSource.samples()`
// instead of the daemon's source. DEVELOPMENT AID, never the owner's data.

import Foundation

extension Cal {
    /// The snapshot of the sample data.
    public static func sampleSnapshot(_ d: SampleData) -> Snapshot {
        Snapshot(
            phase: .ready, accounts: d.accounts, calendarAccounts: d.calendars.accounts, unanswered: d.calendars.unanswered,
            invitations: d.invitations, preferences: d.preferences)
    }
}

extension InMemoryCalendarSource {
    /// MALACHI_CALENDAR_SAMPLES=1, read once.
    public static let samplesRequested = ProcessInfo.processInfo.environment["MALACHI_CALENDAR_SAMPLES"] == "1"

    /// The sample calendar of the week of `now`: reads work, writes answer
    /// "The preview cannot do that." (`Cal.SourceError.preview`); the
    /// calendars' checkboxes work, they only change what is shown.
    public static func samples(now: Date = Date(), zone: TimeZone = .current) -> InMemoryCalendarSource {
        InMemoryCalendarSource(Cal.sampleData(now: now, zone: zone))
    }
}
