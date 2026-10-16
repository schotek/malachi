// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// MalachiCore/Calendar/CalendarMonthLayout.swift and MiniMonth.swift, the
// cases of Go month_test.go plus the sidebar month.

private typealias F = CalFixture

@Suite struct CalendarMonthLayoutTests {
    private let p = F.prague

    @Test func monthCells() {
        let now = F.at(2026, 10, 16, 12)
        // 2026: the 1st falls on every weekday in one of these months.
        let months: [Cal.Weekday: Int] = [.sunday: 2, .monday: 6, .tuesday: 9, .wednesday: 4, .thursday: 10, .friday: 5, .saturday: 8]
        for (wd, m) in months {
            for ws in [Cal.Weekday.monday, .sunday] {
                let g = Cal.monthCells(anchor: F.at(2026, m, 15), weekStart: ws, now: now, zone: p)
                let first = F.at(2026, m, 1)
                #expect(Cal.parts(first, p).weekday == wd)
                let lead = (wd.rawValue - ws.rawValue + 7) % 7
                #expect(Cal.parts(g.cells[0].day, p).weekday == ws && g.cells[lead].day == first, "\(m) from \(ws)")
                let days = Cal.parts(Cal.addDays(Cal.date(2026, m + 1, 1, in: p), -1, p), p).day
                #expect(g.cells.count == 42 && g.weekNumbers.count == 6)
                var inMonth = 0
                for (i, c) in g.cells.enumerated() {
                    if i > 0 {
                        #expect(Cal.daysBetween(g.cells[i - 1].day, c.day, p) == 1)
                    }
                    if c.inMonth {
                        inMonth += 1
                    }
                    let cp = Cal.parts(c.day, p)
                    #expect(c.weekend == (cp.weekday == .saturday || cp.weekday == .sunday))
                    #expect(cp.hour == 0)
                }
                #expect(inMonth == days, "\(m) from \(ws)")
                for r in 0..<6 {
                    #expect(g.weekNumbers[r] == Cal.weekNumber(g.cells[r * 7].day, weekStart: ws, zone: p))
                }
            }
        }
        let g = Cal.monthCells(anchor: now, weekStart: .monday, now: now, zone: p)
        #expect(g.cells.indices.filter { g.cells[$0].today } == [18])
        #expect(g.weekNumbers == [40, 41, 42, 43, 44, 45])
        let gs = Cal.monthCells(anchor: now, weekStart: .sunday, now: now, zone: p)
        #expect(gs.weekNumbers == [40, 41, 42, 43, 44, 45] && Cal.parts(gs.cells[0].day, p).day == 27)
    }

    @Test func weekdayHeaders() {
        let env = F.env(F.utc)
        #expect(Cal.weekdayHeaders(.monday, env: env) == ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"])
        #expect(Cal.weekdayHeaders(.sunday, env: env) == ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"])
        let sat = Cal.weekdayHeaders(.saturday, env: env)
        #expect(sat[0] == "Sat" && sat[6] == "Fri")
    }

    @Test func cellCapacity() {
        let cases: [(Double, Double, Int, Int, Double)] = [
            (100, 20, 5, 0, 0), (99.9, 20, 4, 0, 0), (19, 20, 0, 0, 0), (0, 20, 0, 0, 0), (50, 0, 0, 0, 0), (-5, 10, 0, 0, 0),
            (80, 16, 5, 0, 16), (100, 20, 3, 2, 18), (100, 20, 0, 6, 18), (100, 20, 5, -1, 18),
        ]
        for (h, line, want, lanes, laneH) in cases {
            #expect(Cal.cellCapacity(cellHeight: h, lineHeight: line, lanes: lanes, laneHeight: laneH) == want, "\(h) \(lanes)")
        }
    }

    @Test func cellItems() {
        let day = F.at(2026, 10, 14)
        let occ = [
            F.timed("t2", F.at(2026, 10, 14, 14), F.at(2026, 10, 14, 15)),
            F.timed("t1", F.at(2026, 10, 14, 9), F.at(2026, 10, 14, 10)),
            F.timed("t1b", F.at(2026, 10, 14, 9), F.at(2026, 10, 14, 9, 30)),
            F.allDayOcc("one", day, 1),
            F.allDayOcc("long", F.at(2026, 10, 13), 3),
            F.timed("night", F.at(2026, 10, 13, 22), F.at(2026, 10, 14, 1)),
            F.timed("other", F.at(2026, 10, 15, 9), F.at(2026, 10, 15, 10)),
        ]
        func ids(_ ps: [Cal.Pill]) -> [String] { ps.map { $0.occurrence.eventId!.rawValue } }
        let cases: [(Int, [String], Int)] = [
            (10, ["t1b", "t1", "t2"], 0), (3, ["t1b", "t1", "t2"], 0), (2, ["t1b"], 2), (1, [], 3), (0, [], 3),
        ]
        for (capacity, want, more) in cases {
            let (ps, m) = Cal.cellItems(occ, day: day, capacity: capacity, zone: p, showDeclined: false)
            #expect(ids(ps) == want && m == more, "capacity \(capacity)")
        }
        #expect(Cal.more(2) == "+2 more")
    }

    @Test func monthLanes() {
        let g = Cal.monthCells(anchor: F.at(2026, 10, 1), weekStart: .monday, now: F.at(2026, 10, 16), zone: p)
        func row(_ r: Int) -> [Date] { (0..<7).map { g.cells[r * 7 + $0].day } }
        let two = F.allDayOcc("two", F.at(2026, 10, 7), 2)  // Wed–Thu of row 1
        let ten = F.allDayOcc("ten", F.at(2026, 10, 9), 10)  // Fri 9 – Sun 18: rows 1 and 2
        let other = F.allDayOcc("other", F.at(2026, 10, 8), 3)  // Thu–Sat, overlaps both
        let night = F.timed("night", F.at(2026, 10, 13, 22), F.at(2026, 10, 14, 2))
        let inDay = F.timed("day", F.at(2026, 10, 13, 9), F.at(2026, 10, 13, 10))
        let toMidnight = F.timed("mid", F.at(2026, 10, 13, 23), F.at(2026, 10, 14))
        let occ = [night, ten, inDay, other, two, toMidnight, two]
        func got(_ r: Int) -> [String] {
            Cal.monthLanes(occ, weekDays: row(r), zone: p, showDeclined: false).map {
                "\($0.key) \($0.lane) \($0.startCol) \($0.endCol) \($0.continuesBefore) \($0.continuesAfter)"
            }
        }
        // Row 1 is Mon 5 – Sun 11 October.
        #expect(got(1) == ["two| 0 2 3 false false", "other| 1 3 5 false false", "ten| 0 4 6 false true"])
        // Row 2 is Mon 12 – Sun 18 October.
        #expect(got(2) == ["ten| 0 0 6 true false", "night| 1 1 2 false false"])
        #expect(Cal.laneCount(Cal.monthLanes(occ, weekDays: row(1), zone: p, showDeclined: false)) == 2)
        #expect(Cal.laneCount([]) == 0 && Cal.monthLanes(occ, weekDays: row(4), zone: p, showDeclined: false).isEmpty)
        #expect(!Cal.isMonthSpan(inDay, zone: p) && !Cal.isMonthSpan(toMidnight, zone: p))
        #expect(Cal.isMonthSpan(night, zone: p) && Cal.isMonthSpan(two, zone: p))
        // The pills of Tuesday 13 leave out the night's bar; the capacity of
        // row 2 loses its two lanes.
        let (pills, _) = Cal.cellItems(occ, day: F.at(2026, 10, 13), capacity: 10, zone: p, showDeclined: false)
        #expect(pills.map { $0.occurrence.eventId!.rawValue } == ["day", "mid"])
        let lanes = Cal.laneCount(Cal.monthLanes(occ, weekDays: row(2), zone: p, showDeclined: false))
        #expect(Cal.cellCapacity(cellHeight: 100, lineHeight: 20, lanes: lanes, laneHeight: 18) == 3)
        var declined = two
        declined.myResponse = .declined
        #expect(Cal.monthLanes([declined], weekDays: row(1), zone: p, showDeclined: false).isEmpty)
        #expect(Cal.monthLanes([declined], weekDays: row(1), zone: p, showDeclined: true).count == 1)
        let first = Cal.monthLanes([ten], weekDays: row(1), zone: p, showDeclined: false)[0]
        #expect(first.title == "ten" && first.calendarId == nil)
    }

    /// The sidebar month: the grid of `monthCells`, today, and the band of
    /// the shown week with its ends.
    @Test func miniMonth() {
        let env = F.env()
        let now = F.at(2026, 10, 16, 12)
        let week = Cal.viewRange(.week, anchor: now, weekStart: .monday, zone: p)
        let m = Cal.MiniMonth(anchor: now, weekStart: .monday, now: now, shown: week, env: env)
        #expect(m.title == "October 2026" && m.month == F.at(2026, 10, 1))
        #expect(m.headers.first == "Mon" && m.days.count == 42 && m.weekNumbers == [40, 41, 42, 43, 44, 45])
        #expect(m.days.indices.filter { m.days[$0].today } == [18])
        #expect(m.days.indices.filter { m.days[$0].shown } == Array(14..<21))
        #expect(m.days[14].bandStart && !m.days[14].bandEnd && m.days[20].bandEnd && !m.days[15].bandStart)
        #expect(m.days[0].number == "28" && !m.days[0].inMonth && m.days[3].number == "1" && m.days[3].inMonth)
        #expect(m.index(of: F.at(2026, 10, 16, 23, 59), zone: p) == 18 && m.index(of: F.at(2027, 1, 1), zone: p) == nil)
        // A range across rows is banded per row; no range bands nothing.
        let agenda = Cal.viewRange(.agenda, anchor: F.at(2026, 10, 30), weekStart: .monday, zone: p)
        let a = Cal.MiniMonth(anchor: now, weekStart: .monday, now: now, shown: agenda, env: env)
        #expect(a.days[32].shown && a.days[32].bandStart && a.days[34].bandEnd && a.days[35].bandStart)
        #expect(Cal.MiniMonth(anchor: now, weekStart: .monday, now: now, shown: nil, env: env).days.allSatisfy { !$0.shown })
        #expect(Cal.MiniMonth.step(F.at(2026, 12, 31), 1, zone: p) == F.at(2027, 1, 1))
        #expect(Cal.MiniMonth.step(F.at(2026, 3, 31), -1, zone: p) == F.at(2026, 2, 1))
    }
}
