// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The Agenda view and the search results (Go agenda.go): the occurrences
// grouped by day under "Today", "Tomorrow" or the weekday and date; one
// over several days is listed on each of its days with "Day 2 of 3".

import Foundation

extension Cal {
    /// agenda.go `AgendaItem`: one row of the agenda.
    public struct AgendaItem: Sendable, Equatable {
        public var occurrence: Occurrence
        /// The occurrence's span in the zone.
        public var start: Date
        public var end: Date
        /// The row is day `dayOf` (1-based) of an occurrence of `days` days.
        public var dayOf: Int
        public var days: Int
        public var state: BlockState

        public init(occurrence: Occurrence, start: Date, end: Date, dayOf: Int, days: Int, state: BlockState = .normal) {
            self.occurrence = occurrence
            self.start = start
            self.end = end
            self.dayOf = dayOf
            self.days = days
            self.state = state
        }
    }

    /// agenda.go `AgendaDay`.
    public struct AgendaDay: Sendable, Equatable {
        public var day: Date
        public var heading: String
        public var items: [AgendaItem]
    }

    /// How many calendar days an occurrence touches: a timed one ending
    /// exactly at a midnight does not touch that day.
    static func occurrenceDays(_ start: Date, _ end: Date, _ zone: TimeZone) -> Int {
        guard end > start else { return 1 }
        var last = end
        if startOfDay(end, zone) == end {
            // Go steps back a nanosecond; a millisecond is what a Date
            // keeps reliably, and API times are whole seconds.
            last = end.addingTimeInterval(-0.001)
        }
        return daysBetween(start, last, zone) + 1
    }

    /// agenda.go `Agenda`: the occurrences that touch [from, from + days)
    /// by day, only days that have some, each day all-day first, then by
    /// start, end and key.
    public static func agenda(
        _ occurrences: [Occurrence], from: Date, days: Int, now: Date, env: Env, showDeclined: Bool
    ) -> [AgendaDay] {
        let zone = env.zone
        let first = startOfDay(from, zone)
        var byDay: [Int: [AgendaItem]] = [:]
        for o in occurrences {
            guard shown(o, showDeclined: showDeclined), let (s, e) = span(o, zone) else { continue }
            let n = occurrenceDays(s, e, zone)
            let base = daysBetween(first, s, zone)
            for i in 0..<n {
                let idx = base + i
                guard idx >= 0, idx < days else { continue }
                byDay[idx, default: []].append(AgendaItem(
                    occurrence: o, start: s, end: e, dayOf: i + 1, days: n, state: stateOf(o)))
            }
        }
        return byDay.keys.sorted().map { i in
            let d = addDays(first, i, zone)
            return AgendaDay(day: d, heading: dayHeading(d, now: now, env: env), items: stableSorted(byDay[i]!, by: compareItems))
        }
    }

    static func compareItems(_ a: AgendaItem, _ b: AgendaItem) -> Int {
        if a.occurrence.allDay != b.occurrence.allDay {
            return a.occurrence.allDay ? -1 : 1
        }
        return or(compare(a.start, b.start), compare(a.end, b.end),
                  compareBytes(occurrenceKey(a.occurrence), occurrenceKey(b.occurrence)))
    }

    /// agenda.go `DayHeading`: Today, Tomorrow, Yesterday, else the weekday
    /// and date (with the year when it is not now's).
    public static func dayHeading(_ day: Date, now: Date, env: Env) -> String {
        let zone = env.zone
        switch daysBetween(now, day, zone) {
        case 0: return L10n.T("Today")
        case 1: return L10n.T("Tomorrow")
        case -1: return L10n.T("Yesterday")
        default: break
        }
        if parts(day, zone).year != parts(now, zone).year {
            return Text.longDate(day, env)
        }
        return Text.weekdayDate(day, env)
    }

    /// agenda.go `DayOfText`: "Day 2 of 3".
    public static func dayOfText(_ n: Int, of: Int) -> String {
        L10n.T("Day %d of %d", n, of)
    }

    /// agenda.go `ItemTime`: All Day, the time range, or a part of an
    /// occurrence over several days ("from 22:00", "until 02:00").
    public static func itemTime(_ it: AgendaItem, env: Env) -> String {
        if it.occurrence.allDay {
            return Text.allDay
        }
        if it.days <= 1 {
            return Text.timeRange(it.start, it.end, env)
        }
        switch it.dayOf {
        case 1: return L10n.T("from %s", Text.time(it.start, env))
        case it.days: return L10n.T("until %s", Text.time(it.end, env))
        default: return Text.allDay
        }
    }

    public static var agendaEmpty: String { L10n.T("No events in this period.") }
    public static var resultsHeading: String { L10n.T("Results") }
    public static var noResults: String { L10n.T("No events match the search.") }
    /// The sidebar's row and the agenda's heading of the unanswered
    /// invitations.
    public static var unansweredHeading: String { L10n.T("Invitations") }
}
