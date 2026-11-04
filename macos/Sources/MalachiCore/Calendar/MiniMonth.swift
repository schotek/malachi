// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The sidebar's month (docs/calendar.md §5.2): the 6×7 days of a month,
// today, and the band of the days the page shows (a day, a week, the
// month's grid or an agenda page), built from `Cal.monthCells`. It has no
// texts but the day numbers and the weekday headers of the month view.

import Foundation

extension Cal {
    public struct MiniMonth: Sendable, Equatable {
        /// One day of the grid.
        public struct Day: Sendable, Equatable {
            public var date: Date
            /// The day of the month, "14" (`Text.dayOfMonth`).
            public var number: String
            public var inMonth: Bool
            public var today: Bool
            public var weekend: Bool
            /// The day is in the range the page shows (drawn banded).
            public var shown: Bool
            /// The first and last shown day of its row, for the band's
            /// rounded ends.
            public var bandStart: Bool
            public var bandEnd: Bool
        }

        /// The midnight of the month's 1st.
        public var month: Date
        /// "October 2026".
        public var title: String
        /// The seven weekday headers from the week's first day.
        public var headers: [String]
        /// The 42 days, row by row.
        public var days: [Day]
        /// The six rows' week numbers.
        public var weekNumbers: [Int]

        /// The month of `anchor`, with `shown` banded. The shown range only
        /// colours days; it may lie partly or wholly outside the grid.
        public init(anchor: Date, weekStart: Weekday, now: Date, shown: Range?, env: Env) {
            let zone = env.zone
            let g = Cal.monthCells(anchor: anchor, weekStart: weekStart, now: now, zone: zone)
            month = g.month
            title = Text.monthYear(g.month, env)
            headers = Cal.weekdayHeaders(weekStart, env: env)
            weekNumbers = g.weekNumbers
            let flags = g.cells.map { c in shown?.contains(c.day) ?? false }
            days = g.cells.enumerated().map { i, c in
                let col = i % 7
                let on = flags[i]
                return Day(
                    date: c.day, number: Text.dayOfMonth(c.day, env), inMonth: c.inMonth, today: c.today,
                    weekend: c.weekend, shown: on,
                    bandStart: on && (col == 0 || !flags[i - 1]),
                    bandEnd: on && (col == 6 || !flags[i + 1]))
            }
        }

        /// The anchor of the month before (-1) or after (+1): the 1st.
        public static func step(_ month: Date, _ dir: Int, zone: TimeZone) -> Date {
            let p = Cal.parts(month, zone)
            return Cal.date(p.year, p.month + dir, 1, in: zone)
        }

        /// The index of the day `t` falls on; nil outside the grid.
        public func index(of t: Date, zone: TimeZone) -> Int? {
            let d = Cal.startOfDay(t, zone)
            return days.firstIndex { $0.date == d }
        }
    }
}
