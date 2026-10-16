// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The Board → Today strip "Today in Calendar" (Go today.go): today's
// occurrences with their time range (or All Day), title and place, "took
// place" for the ones over, Respond for unanswered invitations, and Open
// Calendar. The strip is hidden when no account has the calendar
// capability.

import Foundation

extension Cal {
    /// today.go `TodayRow`.
    public struct TodayRow: Sendable, Equatable {
        public var occurrence: Occurrence
        /// The time range, All Day, or a part of one over several days.
        public var time: String
        public var title: String
        /// The location ("" none).
        public var place: String
        /// The occurrence ended before now.
        public var tookPlace: Bool
        /// An unanswered invitation (the row offers Respond).
        public var respond: Bool
        public var state: BlockState
    }

    /// today.go `TodayRows`: now's day, in the agenda's order.
    public static func todayRows(_ occurrences: [Occurrence], now: Date, env: Env, showDeclined: Bool) -> [TodayRow] {
        guard let day = agenda(occurrences, from: now, days: 1, now: now, env: env, showDeclined: showDeclined).first else {
            return []
        }
        return day.items.map { it in
            TodayRow(
                occurrence: it.occurrence, time: itemTime(it, env: env), title: cleanTitle(it.occurrence.title),
                place: cleanLine(it.occurrence.location), tookPlace: !(it.end > now), respond: it.state == .unanswered,
                state: it.state)
        }
    }

    /// today.go `ShowsTodayStrip`: some account has the calendar capability.
    public static func showsTodayStrip(_ accounts: [Account]) -> Bool {
        accounts.contains { $0.can(.calendar) }
    }

    public static var todayHeading: String { L10n.T("Today in Calendar") }
    public static var openCalendar: String { L10n.T("Open Calendar") }
    public static var tookPlace: String { L10n.T("took place") }
    public static var respond: String { L10n.T("Respond") }
    public static var nothingToday: String { L10n.T("Nothing in the calendar today.") }
}
