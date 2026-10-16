// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// MalachiCore/Calendar/CalendarWeekLayout.swift, the cases of Go
// week_test.go: overlaps of 1 to 8 events, clusters, clipping, DST days of
// 23 and 25 hours, all-day lanes across a week boundary.

private typealias F = CalFixture

private struct Placed: Equatable, CustomStringConvertible {
    var id: String
    var col, cols, top, bottom: Int
    init(_ id: String, _ col: Int, _ cols: Int, _ top: Int, _ bottom: Int) {
        self.id = id
        self.col = col
        self.cols = cols
        self.top = top
        self.bottom = bottom
    }
    var description: String { "\(id) \(col)/\(cols) \(top)–\(bottom)" }
}

@Suite struct CalendarWeekLayoutTests {
    private let p = F.prague

    private func layout(_ occ: [Occurrence], _ day: Date, showDeclined: Bool = false) -> [Placed] {
        Cal.layoutDay(occ, day: day, zone: p, showDeclined: showDeclined).map {
            Placed($0.occurrence.eventId?.rawValue ?? "", $0.column, $0.columns, $0.top, $0.bottom)
        }
    }

    @Test func overlaps() {
        let day = F.at(2026, 10, 14)
        func h(_ hh: Int, _ mm: Int) -> Date { F.at(2026, 10, 14, hh, mm) }
        // 1 to 8 events at the same time: one column each.
        for n in 1...8 {
            var occ: [Occurrence] = []
            var want: [Placed] = []
            for i in 0..<n {
                occ.append(F.timed("e\(i)", h(10, 0), h(11, 0)))
                want.append(Placed("e\(i)", i, n, 600, 660))
            }
            // Given in reverse: the order is by key, not by input.
            #expect(layout(occ.reversed(), day) == want, "\(n) overlapping")
        }
        let cases: [(String, [Occurrence], [Placed])] = [
            ("staircase reuses the freed column",
             [F.timed("a", h(9, 0), h(10, 0)), F.timed("b", h(9, 30), h(10, 30)), F.timed("c", h(10, 0), h(11, 0))],
             [Placed("a", 0, 2, 540, 600), Placed("b", 1, 2, 570, 630), Placed("c", 0, 2, 600, 660)]),
            ("two clusters",
             [F.timed("a", h(9, 0), h(10, 0)), F.timed("b", h(9, 0), h(10, 0)), F.timed("c", h(11, 0), h(12, 0))],
             [Placed("a", 0, 2, 540, 600), Placed("b", 1, 2, 540, 600), Placed("c", 0, 1, 660, 720)]),
            ("longer first, greedy",
             [F.timed("d", h(10, 30), h(11, 30)), F.timed("b", h(9, 0), h(10, 0)), F.timed("c", h(10, 0), h(11, 0)),
              F.timed("a", h(9, 0), h(12, 0))],
             [Placed("a", 0, 3, 540, 720), Placed("b", 1, 3, 540, 600), Placed("c", 1, 3, 600, 660), Placed("d", 2, 3, 630, 690)]),
            ("back to back do not overlap",
             [F.timed("a", h(9, 0), h(10, 0)), F.timed("b", h(10, 0), h(11, 0))],
             [Placed("a", 0, 1, 540, 600), Placed("b", 0, 1, 600, 660)]),
            ("short blocks get the minimum height and overlap by it",
             [F.timed("a", h(9, 0), h(9, 5)), F.timed("b", h(9, 10), h(9, 20))],
             [Placed("a", 0, 2, 540, 555), Placed("b", 1, 2, 550, 565)]),
            ("no length at the end of the day",
             [F.timed("a", h(23, 55), h(23, 55))],
             [Placed("a", 0, 1, 1425, 1440)]),
            ("all-day left out",
             [F.allDayOcc("x", day, 1), F.timed("a", h(8, 0), h(9, 0))],
             [Placed("a", 0, 1, 480, 540)]),
        ]
        for (name, occ, want) in cases {
            #expect(layout(occ, day) == want, "\(name)")
        }
    }

    @Test func clipsAndDeclined() {
        let night = F.timed("n", F.at(2026, 10, 14, 22), F.at(2026, 10, 15, 2))
        var b = Cal.layoutDay([night], day: F.at(2026, 10, 14), zone: p, showDeclined: false)
        #expect(b.count == 1 && b[0].top == 1320 && b[0].bottom == 1440 && b[0].continuesAfter && !b[0].continuesBefore)
        b = Cal.layoutDay([night], day: F.at(2026, 10, 15), zone: p, showDeclined: false)
        #expect(b.count == 1 && b[0].top == 0 && b[0].bottom == 120 && b[0].continuesBefore && !b[0].continuesAfter)
        #expect(Cal.layoutDay([night], day: F.at(2026, 10, 16), zone: p, showDeclined: false).isEmpty)
        var declined = F.timed("d", F.at(2026, 10, 14, 9), F.at(2026, 10, 14, 10))
        declined.myResponse = .declined
        #expect(Cal.layoutDay([declined], day: F.at(2026, 10, 14), zone: p, showDeclined: false).isEmpty)
        b = Cal.layoutDay([declined], day: F.at(2026, 10, 14), zone: p, showDeclined: true)
        #expect(b.count == 1 && b[0].state == .declined)
    }

    @Test func daylightSaving() {
        // 29 March 2026: 02:00 CET becomes 03:00 CEST, the day has 23 hours.
        let spring = F.at(2026, 3, 29)
        #expect(F.hours(spring, Cal.addDays(spring, 1, p)) == 23)
        let s1 = F.utcAt(2026, 3, 29, 0, 30)  // 01:30 CET
        let e1 = F.utcAt(2026, 3, 29, 1, 30)  // 03:30 CEST
        let s2 = F.utcAt(2026, 3, 29, 1, 0)  // 03:00 CEST
        let e2 = F.utcAt(2026, 3, 29, 2, 0)  // 04:00 CEST
        #expect(layout([F.timed("a", s1, e1), F.timed("b", s2, e2)], spring)
            == [Placed("a", 0, 2, 90, 210), Placed("b", 1, 2, 180, 240)])
        // 25 October 2026: 03:00 CEST becomes 02:00 CET, 25 hours.
        let autumn = F.at(2026, 10, 25)
        #expect(F.hours(autumn, Cal.addDays(autumn, 1, p)) == 25)
        let s3 = F.utcAt(2026, 10, 25, 0, 30)  // 02:30 CEST
        let e3 = F.utcAt(2026, 10, 25, 1, 30)  // 02:30 CET
        let s4 = F.utcAt(2026, 10, 24, 23, 0)  // 01:00 CEST
        let e4 = F.utcAt(2026, 10, 25, 3, 0)  // 04:00 CET
        let s5 = F.utcAt(2026, 10, 25, 22, 0)  // 23:00 CET
        let e5 = F.utcAt(2026, 10, 25, 23, 0)  // 00:00 next day
        #expect(layout([F.timed("c", s3, e3), F.timed("d", s4, e4), F.timed("e", s5, e5)], autumn)
            == [Placed("d", 0, 2, 60, 240), Placed("c", 1, 2, 150, 165), Placed("e", 0, 1, 1380, 1440)])
    }

    @Test func allDayLanes() {
        let week1 = Cal.viewRange(.week, anchor: F.at(2026, 10, 14), weekStart: .monday, zone: p).days
        let week2 = Cal.viewRange(.week, anchor: F.at(2026, 10, 21), weekStart: .monday, zone: p).days
        let span = F.allDayOcc("span", F.at(2026, 10, 17), 4)  // Saturday to Tuesday
        let a = F.allDayOcc("a", F.at(2026, 10, 12), 3)  // Monday to Wednesday
        let b = F.allDayOcc("b", F.at(2026, 10, 13), 1)  // Tuesday
        let c = F.allDayOcc("c", F.at(2026, 10, 15), 1)  // Thursday
        let timedOne = F.timed("t", F.at(2026, 10, 13, 9), F.at(2026, 10, 13, 10))
        var (bars, lanes) = Cal.allDayLanes([c, span, timedOne, b, a], days: week1, zone: p, showDeclined: false)
        let got = bars.map { "\($0.occurrence.eventId!.rawValue) \($0.lane) \($0.first) \($0.last) \($0.continuesBefore) \($0.continuesAfter)" }
        #expect(got == ["a 0 0 2 false false", "b 1 1 1 false false", "c 0 3 3 false false", "span 0 5 6 false true"])
        #expect(lanes == 2)
        (bars, lanes) = Cal.allDayLanes([span], days: week2, zone: p, showDeclined: false)
        #expect(bars.count == 1 && bars[0].first == 0 && bars[0].last == 1 && bars[0].continuesBefore && !bars[0].continuesAfter)
        #expect(lanes == 1)
        // One day of the Day view.
        (bars, _) = Cal.allDayLanes([span], days: Array(week2[0..<1]), zone: p, showDeclined: false)
        #expect(bars.count == 1 && bars[0].first == 0 && bars[0].last == 0 && bars[0].continuesBefore && bars[0].continuesAfter)
        let none = Cal.allDayLanes([], days: [], zone: p, showDeclined: false)
        #expect(none.bars.isEmpty && none.lanes == 0)
    }

    @Test func nowLine() {
        let r = Cal.viewRange(.week, anchor: F.at(2026, 10, 14), weekStart: .monday, zone: p)
        let line = Cal.nowLine(F.at(2026, 10, 14, 13, 37), r, zone: p)
        #expect(line?.day == 2 && line?.minute == 817)
        #expect(Cal.nowLine(F.at(2026, 10, 19), r, zone: p) == nil)
        func wh(_ s: String) -> WorkingHours { WorkingHours(days: [], start: s, end: "") }
        #expect(Cal.scrollMinute(wh("08:00")) == 420)
        #expect(Cal.scrollMinute(wh("x")) == 420)
        #expect(Cal.scrollMinute(wh("00:30")) == 0)
        #expect(Cal.parseClock("9:30") == 570)
        #expect(Cal.parseClock("24:00") == nil && Cal.parseClock("08:0") == nil && Cal.parseClock("08:00 ") == nil)
    }

    @Test func stateOf() {
        let cases: [(Occurrence, Cal.BlockState)] = [
            (F.occ(status: .confirmed, showAs: .busy), .normal),
            (F.occ(myResponse: .accepted), .normal),
            (F.occ(myResponse: .tentative), .tentative),
            (F.occ(status: .tentative), .tentative),
            (F.occ(showAs: .tentative), .tentative),
            (F.occ(myResponse: .needsAction), .unanswered),
            (F.occ(showAs: .tentative, invitation: MessageRef(accountId: "", messageId: "")), .unanswered),
            (F.occ(showAs: .tentative, myResponse: .declined), .declined),
            (F.occ(status: .cancelled, myResponse: .declined), .cancelled),
        ]
        for (i, (o, want)) in cases.enumerated() {
            #expect(Cal.stateOf(o) == want, "\(i)")
        }
    }

    @Test func blockLabel() {
        let env = F.env()
        var o = F.timed("Standup", F.at(2026, 10, 13, 10), F.at(2026, 10, 13, 10, 30))
        o.myResponse = .needsAction
        #expect(Cal.blockLabel(o, env: env) == "Standup, Tuesday 10:00–10:30, awaiting your answer")
        o.myResponse = nil
        #expect(Cal.blockLabel(o, env: env) == "Standup, Tuesday 10:00–10:30")
        let ad = F.allDayOcc("Offsite", F.at(2026, 10, 15), 2)
        #expect(Cal.blockLabel(ad, env: env) == "Offsite, Thursday, all day")
        var bare = F.occ()
        bare.title = "\u{202E}X"
        #expect(Cal.blockLabel(bare, env: env) == "X")
    }
}
