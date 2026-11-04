// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The time grid of the Week and Day views (Go week.go): the timed blocks of
// a day with their columns, the all-day row's lanes, the now line, and how
// a block is drawn. Positions are wall-clock minutes from midnight
// (0–1440), so the grid always has 24 rows: on the day the clocks spring
// forward an hour is missing from the events, not from the grid; on the
// day they fall back the repeated hour folds onto one row.

import Foundation

extension Cal {
    /// The height of the grid in minutes.
    public static let dayMinutes = 24 * 60

    /// The shortest block drawn.
    public static let minBlockMinutes = 15

    /// week.go `BlockState`: how a block, bar or pill is drawn.
    public enum BlockState: Int, Sendable, CaseIterable {
        /// Filled with the calendar's colour.
        case normal
        /// Answered tentatively, or tentative by status or show-as: stripes.
        case tentative
        /// Waiting for the user's answer: dashed with a "?" badge.
        case unanswered
        /// Declined by the user: struck through (only with showDeclined).
        case declined
        /// Cancelled by the organiser: struck through and faded.
        case cancelled
    }

    /// week.go `StateOf`: cancelled, then declined, then unanswered, then
    /// tentative.
    public static func stateOf(_ o: Occurrence) -> BlockState {
        if o.status == .cancelled {
            return .cancelled
        }
        if o.myResponse == .declined {
            return .declined
        }
        if o.invitation != nil || o.myResponse == .needsAction {
            return .unanswered
        }
        if o.myResponse == .tentative || o.status == .tentative || o.showAs == .tentative {
            return .tentative
        }
        return .normal
    }

    /// week.go `Shown`: declined occurrences only with showDeclined.
    public static func shown(_ o: Occurrence, showDeclined: Bool) -> Bool {
        showDeclined || o.myResponse != .declined
    }

    /// week.go `Block`: a timed occurrence placed in a day's column.
    public struct Block: Sendable, Equatable {
        public var occurrence: Occurrence
        /// The occurrence clipped to the day.
        public var start: Date
        public var end: Date
        /// Wall-clock minutes from midnight, bottom ≥ top + 15 and ≤ 1440.
        public var top: Int
        public var bottom: Int
        /// The block's column (0-based) among `columns` side by side.
        public var column: Int
        public var columns: Int
        public var continuesBefore: Bool
        public var continuesAfter: Bool
        public var state: BlockState
    }

    /// The wall-clock minutes of `t` from the midnight of the day.
    private static func wallMinutes(_ t: Date, _ dayStart: Date, _ dayEnd: Date, _ zone: TimeZone) -> Int {
        if t >= dayEnd {
            return dayMinutes
        }
        if t <= dayStart {
            return 0
        }
        let p = parts(t, zone)
        return p.hour * 60 + p.minute
    }

    /// week.go `LayoutDay`: the timed occurrences that touch `day` in
    /// columns (greedy interval colouring, sorted by top, the longer first,
    /// start and key); a cluster of overlapping blocks shares one count.
    public static func layoutDay(_ occurrences: [Occurrence], day: Date, zone: TimeZone, showDeclined: Bool) -> [Block] {
        let dayStart = startOfDay(day, zone)
        let dayEnd = addDays(dayStart, 1, zone)
        var blocks: [Block] = []
        for o in occurrences {
            guard !o.allDay, shown(o, showDeclined: showDeclined), overlaps(o, from: dayStart, to: dayEnd, zone),
                  let (s, e) = span(o, zone) else { continue }
            var b = Block(
                occurrence: o, start: max(s, dayStart), end: min(e, dayEnd), top: 0, bottom: 0, column: 0, columns: 0,
                continuesBefore: s < dayStart, continuesAfter: e > dayEnd, state: stateOf(o))
            b.top = wallMinutes(b.start, dayStart, dayEnd, zone)
            b.bottom = wallMinutes(b.end, dayStart, dayEnd, zone)
            if b.bottom < b.top + minBlockMinutes {
                b.bottom = b.top + minBlockMinutes
            }
            if b.bottom > dayMinutes {
                b.bottom = dayMinutes
                b.top = dayMinutes - minBlockMinutes
            }
            blocks.append(b)
        }
        blocks = stableSorted(blocks) { a, b in
            or(compare(a.top, b.top), compare(b.bottom - b.top, a.bottom - a.top), compare(a.start, b.start),
               compareBytes(occurrenceKey(a.occurrence), occurrenceKey(b.occurrence)))
        }
        assignColumns(&blocks)
        return blocks
    }

    /// Colours the sorted blocks cluster by cluster.
    private static func assignColumns(_ blocks: inout [Block]) {
        var start = 0
        var clusterEnd = -1
        var ends: [Int] = []
        func flush(_ to: Int) {
            for i in start..<to {
                blocks[i].columns = ends.count
            }
        }
        for i in blocks.indices {
            if blocks[i].top >= clusterEnd {
                flush(i)
                start = i
                ends.removeAll()
            }
            var col = -1
            for (c, end) in ends.enumerated() where end <= blocks[i].top {
                col = c
                break
            }
            if col < 0 {
                col = ends.count
                ends.append(0)
            }
            ends[col] = blocks[i].bottom
            blocks[i].column = col
            clusterEnd = max(clusterEnd, blocks[i].bottom)
        }
        flush(blocks.count)
    }

    /// week.go `Bar`: an all-day occurrence in the all-day row.
    public struct Bar: Sendable, Equatable {
        public var occurrence: Occurrence
        /// The bar's row (0-based) in the all-day area.
        public var lane: Int
        /// The indexes of the bar's first and last day in the days given.
        public var first: Int
        public var last: Int
        public var continuesBefore: Bool
        public var continuesAfter: Bool
        public var state: BlockState
    }

    /// week.go `AllDayLanes`: the all-day occurrences that touch `days` in
    /// lanes (sorted by first day, the longer first, key; each in the lowest
    /// free lane), and the number of lanes.
    public static func allDayLanes(_ occurrences: [Occurrence], days: [Date], zone: TimeZone, showDeclined: Bool)
        -> (bars: [Bar], lanes: Int)
    {
        guard let firstDay = days.first, let lastDay = days.last else { return ([], 0) }
        let from = firstDay
        let to = addDays(lastDay, 1, zone)
        var bars: [Bar] = []
        for o in occurrences {
            guard o.allDay, shown(o, showDeclined: showDeclined), overlaps(o, from: from, to: to, zone),
                  let (s, e) = span(o, zone) else { continue }
            let first = max(0, daysBetween(from, s, zone))
            let last = min(days.count - 1, daysBetween(from, e, zone) - 1)
            bars.append(Bar(
                occurrence: o, lane: 0, first: first, last: last, continuesBefore: s < from, continuesAfter: e > to,
                state: stateOf(o)))
        }
        bars = stableSorted(bars) { a, b in
            or(compare(a.first, b.first), compare(b.last - b.first, a.last - a.first),
               compareBytes(occurrenceKey(a.occurrence), occurrenceKey(b.occurrence)))
        }
        var laneEnds: [Int] = []
        for i in bars.indices {
            var lane = -1
            for (l, end) in laneEnds.enumerated() where end < bars[i].first {
                lane = l
                break
            }
            if lane < 0 {
                lane = laneEnds.count
                laneEnds.append(0)
            }
            laneEnds[lane] = bars[i].last
            bars[i].lane = lane
        }
        return (bars, laneEnds.count)
    }

    /// week.go `NowLine`: today's index among the range's days and the
    /// wall-clock minute; nil when now is outside the range.
    public static func nowLine(_ now: Date, _ r: Range, zone: TimeZone) -> (day: Int, minute: Int)? {
        guard r.contains(now) else { return nil }
        let p = parts(now, zone)
        return (daysBetween(r.from, now, zone), p.hour * 60 + p.minute)
    }

    /// week.go `ScrollMinute`: an hour before the working hours' start; 7:00
    /// when it cannot be read.
    public static func scrollMinute(_ wh: WorkingHours) -> Int {
        max(0, (parseClock(wh.start) ?? 8 * 60) - 60)
    }

    /// week.go `ParseClock`: "HH:MM" (Go's "15:04": a one- or two-digit
    /// hour, two-digit minutes) into minutes from midnight.
    public static func parseClock(_ s: String) -> Int? {
        let b = Array(s.utf8)
        func digit(_ i: Int) -> Int? {
            guard i < b.count, b[i] >= 0x30, b[i] <= 0x39 else { return nil }
            return Int(b[i] - 0x30)
        }
        guard var h = digit(0) else { return nil }
        var i = 1
        if let d = digit(1) {
            h = h * 10 + d
            i = 2
        }
        guard h < 24, i < b.count, b[i] == UInt8(ascii: ":"), let m1 = digit(i + 1), let m2 = digit(i + 2),
              i + 3 == b.count else { return nil }
        let m = m1 * 10 + m2
        guard m < 60 else { return nil }
        return h * 60 + m
    }

    /// week.go `BlockStateText`: the spoken state ("" for a normal one).
    public static func blockStateText(_ s: BlockState) -> String {
        switch s {
        case .tentative: L10n.T("tentative")
        case .unanswered: L10n.T("awaiting your answer")
        case .declined: L10n.T("declined")
        case .cancelled: L10n.T("cancelled")
        case .normal: ""
        }
    }

    /// week.go `BlockLabel`: "Standup, Tuesday 10:00–10:30, awaiting your
    /// answer".
    public static func blockLabel(_ o: Occurrence, env: Env) -> String {
        let title = cleanTitle(o.title)
        var when = ""
        if let (s, e) = span(o, env.zone) {
            if o.allDay {
                when = L10n.T("%s, all day", Text.weekday(s, env))
            } else {
                when = L10n.T("%s %s", Text.weekday(s, env), Text.timeRange(s, e, env))
            }
        }
        let state = blockStateText(stateOf(o))
        if when.isEmpty {
            return title
        }
        if state.isEmpty {
            return L10n.T("%s, %s", title, when)
        }
        return L10n.T("%s, %s, %s", title, when, state)
    }
}
