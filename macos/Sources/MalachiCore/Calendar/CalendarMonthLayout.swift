// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The Month view (Go month.go): a 6×7 grid of days from the week that holds
// the 1st, the week numbers at the left, how many pills a cell has room
// for and which pills a day shows, with "+2 more" for the rest.

import Foundation

extension Cal {
    /// month.go `Cell`: one day of the month grid.
    public struct Cell: Sendable, Equatable {
        /// The midnight of the day.
        public var day: Date
        /// The day belongs to the grid's month (others are grey).
        public var inMonth: Bool
        /// Today (ringed).
        public var today: Bool
        /// Saturday or Sunday (shaded).
        public var weekend: Bool
    }

    /// month.go `MonthGrid`.
    public struct MonthGrid: Sendable, Equatable {
        /// The midnight of the month's 1st.
        public var month: Date
        /// The 42 days, row by row.
        public var cells: [Cell]
        /// The numbers of the six rows' weeks.
        public var weekNumbers: [Int]
    }

    /// The first day of the grid of `anchor`'s month.
    static func gridStart(_ anchor: Date, _ weekStart: Weekday, _ zone: TimeZone) -> Date {
        let a = parts(anchor, zone)
        return startOfWeek(date(a.year, a.month, 1, in: zone), weekStart, zone)
    }

    /// month.go `MonthCells`: six weeks from the week that holds the 1st,
    /// today marked.
    public static func monthCells(anchor: Date, weekStart: Weekday, now: Date, zone: TimeZone) -> MonthGrid {
        let a = parts(anchor, zone)
        let first = date(a.year, a.month, 1, in: zone)
        var g = MonthGrid(month: first, cells: [], weekNumbers: [])
        var d = gridStart(anchor, weekStart, zone)
        let today = startOfDay(now, zone)
        for i in 0..<42 {
            let p = parts(d, zone)
            g.cells.append(Cell(
                day: d, inMonth: p.month == a.month, today: d == today,
                weekend: p.weekday == .saturday || p.weekday == .sunday))
            if i % 7 == 0 {
                g.weekNumbers.append(weekNumber(d, weekStart: weekStart, zone: zone))
            }
            d = addDays(d, 1, zone)
        }
        return g
    }

    /// month.go `WeekdayHeaders`: the seven abbreviated days from
    /// `weekStart` ("Mon").
    public static func weekdayHeaders(_ weekStart: Weekday, env: Env) -> [String] {
        // Any week will do: 2026-10-04 is a Sunday.
        let sunday = date(2026, 10, 4, 12, 0, in: env.zone)
        return (0..<7).map { i in
            Text.shortWeekday(addDays(sunday, (weekStart.rawValue + i) % 7, env.zone), env)
        }
    }

    /// month.go `CellCapacity`: how many lines of pills a cell holds:
    /// `cellHeight` below the day number, less the row's `lanes` of
    /// spanning bars of `laneHeight` each.
    public static func cellCapacity(cellHeight: Double, lineHeight: Double, lanes: Int = 0, laneHeight: Double = 0) -> Int {
        let room = cellHeight - Double(max(0, lanes)) * max(0, laneHeight)
        guard lineHeight > 0, room > 0 else { return 0 }
        return Int(room / lineHeight)
    }

    /// month.go `MonthSpan`: an occurrence drawn as one bar across the
    /// days it covers in a week row (an all-day event, or a timed one that
    /// crosses midnight).
    public struct MonthSpan: Sendable, Equatable {
        public var occurrence: Occurrence
        /// `occurrenceKey`.
        public var key: String
        /// The cleaned title (Untitled when empty).
        public var title: String
        /// The source of its colour.
        public var calendarId: CalendarID?
        /// The bar's row (0-based) under the day numbers.
        public var lane: Int
        /// First and last column (0–6, inclusive), clipped to the week.
        public var startCol: Int
        public var endCol: Int
        public var continuesBefore: Bool
        public var continuesAfter: Bool
        public var state: BlockState
    }

    /// An occurrence's first day and the last day it touches.
    static func spanDays(_ o: Occurrence, _ zone: TimeZone) -> (first: Date, last: Date)? {
        guard let (s, e) = span(o, zone) else { return nil }
        let first = startOfDay(s, zone)
        if o.allDay {
            return (first, addDays(startOfDay(e, zone), -1, zone))
        }
        return (first, addDays(first, occurrenceDays(s, e, zone) - 1, zone))
    }

    /// month.go `IsMonthSpan`: all day, or timed over more than one day.
    public static func isMonthSpan(_ o: Occurrence, zone: TimeZone) -> Bool {
        guard let (first, last) = spanDays(o, zone) else { return false }
        return o.allDay || last > first
    }

    /// month.go `MonthLanes`: the bars of one week row (`weekDays`: its
    /// seven midnights), sorted by first column, the longer first, key;
    /// each in the lowest lane free over its columns.
    public static func monthLanes(_ occurrences: [Occurrence], weekDays: [Date], zone: TimeZone, showDeclined: Bool) -> [MonthSpan] {
        guard weekDays.count == 7 else { return [] }
        let from = weekDays[0]
        let to = addDays(weekDays[6], 1, zone)
        var spans: [MonthSpan] = []
        var seen = Set<String>()
        for o in occurrences {
            let key = occurrenceKey(o)
            guard !seen.contains(key), shown(o, showDeclined: showDeclined), isMonthSpan(o, zone: zone),
                  overlaps(o, from: from, to: to, zone), let (first, last) = spanDays(o, zone) else { continue }
            seen.insert(key)
            spans.append(MonthSpan(
                occurrence: o, key: key, title: cleanTitle(o.title), calendarId: o.calendarId, lane: 0,
                startCol: max(0, daysBetween(from, first, zone)), endCol: min(6, daysBetween(from, last, zone)),
                continuesBefore: first < from, continuesAfter: !(last < to), state: stateOf(o)))
        }
        spans = stableSorted(spans) { a, b in
            or(compare(a.startCol, b.startCol), compare(b.endCol - b.startCol, a.endCol - a.startCol), compareBytes(a.key, b.key))
        }
        var laneEnds: [Int] = []
        for i in spans.indices {
            var lane = -1
            for (l, end) in laneEnds.enumerated() where end < spans[i].startCol {
                lane = l
                break
            }
            if lane < 0 {
                lane = laneEnds.count
                laneEnds.append(0)
            }
            laneEnds[lane] = spans[i].endCol
            spans[i].lane = lane
        }
        return spans
    }

    /// month.go `LaneCount`: the lanes the spans of a row use.
    public static func laneCount(_ spans: [MonthSpan]) -> Int {
        spans.map { $0.lane + 1 }.max() ?? 0
    }

    /// month.go `Pill`: an occurrence in a month cell.
    public struct Pill: Sendable, Equatable {
        public var occurrence: Occurrence
        /// The occurrence's start (a timed pill's time).
        public var start: Date
        /// A timed occurrence that started an earlier day: no time shown.
        public var continuesBefore: Bool
        public var state: BlockState
    }

    /// month.go `CellItems`: the pills of `day`, the bars of `monthLanes`
    /// left out (all-day first, the longer first; then timed by start, end
    /// and key); when they do not all fit the last line is "+n more" and
    /// the second result is n.
    public static func cellItems(_ occurrences: [Occurrence], day: Date, capacity: Int, zone: TimeZone, showDeclined: Bool)
        -> (pills: [Pill], more: Int)
    {
        let dayStart = startOfDay(day, zone)
        let dayEnd = addDays(dayStart, 1, zone)
        struct Item {
            var p: Pill
            var start: Date
            var end: Date
        }
        var items: [Item] = []
        for o in occurrences {
            guard shown(o, showDeclined: showDeclined), !isMonthSpan(o, zone: zone),
                  overlaps(o, from: dayStart, to: dayEnd, zone), let (s, e) = span(o, zone) else { continue }
            items.append(Item(
                p: Pill(occurrence: o, start: s, continuesBefore: !o.allDay && s < dayStart, state: stateOf(o)),
                start: s, end: e))
        }
        items = stableSorted(items) { a, b in
            if a.p.occurrence.allDay != b.p.occurrence.allDay {
                return a.p.occurrence.allDay ? -1 : 1
            }
            let key = compareBytes(occurrenceKey(a.p.occurrence), occurrenceKey(b.p.occurrence))
            if a.p.occurrence.allDay {
                return or(compare(a.start, b.start), compare(b.end, a.end), key)
            }
            return or(compare(a.start, b.start), compare(a.end, b.end), key)
        }
        let pills = items.map(\.p)
        if pills.count <= capacity {
            return (pills, 0)
        }
        let shown = max(0, capacity - 1)
        return (Array(pills.prefix(shown)), pills.count - shown)
    }

    /// month.go `More`: "+2 more".
    public static func more(_ n: Int) -> String {
        L10n.N("+%d more", "+%d more", n)
    }
}
