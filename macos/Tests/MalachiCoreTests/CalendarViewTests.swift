// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// MalachiCore/Calendar/Calendar.swift (views, ranges, spans), the cases of
// Go view_test.go.

private typealias F = CalFixture

@Suite struct CalendarViewTests {
    private let p = F.prague

    @Test func viewNicks() {
        for v in Cal.views {
            let (got, ok) = Cal.parseView(v.nick)
            #expect(ok && got == v)
        }
        let (v, ok) = Cal.parseView("year")
        #expect(!ok && v == .week)
    }

    @Test func viewRange() {
        let anchor = F.at(2026, 10, 16, 15, 30)  // a Friday
        let cases: [(String, Cal.View, Date, Cal.Weekday, Date, Date, Double)] = [
            ("week from Monday", .week, anchor, .monday, F.at(2026, 10, 12), F.at(2026, 10, 19), 168),
            ("week from Sunday", .week, anchor, .sunday, F.at(2026, 10, 11), F.at(2026, 10, 18), 168),
            ("week from Saturday", .week, anchor, .saturday, F.at(2026, 10, 10), F.at(2026, 10, 17), 168),
            ("week with the autumn change", .week, F.at(2026, 10, 25, 12), .monday, F.at(2026, 10, 19), F.at(2026, 10, 26), 169),
            ("day", .day, anchor, .monday, F.at(2026, 10, 16), F.at(2026, 10, 17), 24),
            ("day of 23 hours", .day, F.at(2026, 3, 29, 12), .monday, F.at(2026, 3, 29), F.at(2026, 3, 30), 23),
            ("day of 25 hours", .day, F.at(2026, 10, 25, 12), .monday, F.at(2026, 10, 25), F.at(2026, 10, 26), 25),
            ("month grid", .month, anchor, .monday, F.at(2026, 9, 28), F.at(2026, 11, 9), 42 * 24 + 1),
            ("agenda", .agenda, anchor, .monday, F.at(2026, 10, 16), F.at(2026, 11, 15), 30 * 24 + 1),
        ]
        for (name, v, a, ws, from, to, hours) in cases {
            let r = Cal.viewRange(v, anchor: a, weekStart: ws, zone: p)
            #expect(r.from == from && r.to == to, "\(name)")
            #expect(F.hours(r.from, r.to) == hours, "\(name)")
        }
        #expect(Cal.viewRange(.week, anchor: F.at(2026, 3, 29), weekStart: .monday, zone: p).days.count == 7)
    }

    @Test func step() {
        let cases: [(Cal.View, Date, Int, Date)] = [
            (.day, F.at(2026, 3, 28, 10), 1, F.at(2026, 3, 29)),
            (.day, F.at(2026, 3, 29, 10), 1, F.at(2026, 3, 30)),
            (.week, F.at(2026, 10, 16), -1, F.at(2026, 10, 9)),
            (.week, F.at(2026, 10, 20), 1, F.at(2026, 10, 27)),
            (.month, F.at(2026, 1, 31), 1, F.at(2026, 2, 28)),
            (.month, F.at(2026, 3, 31), -1, F.at(2026, 2, 28)),
            (.month, F.at(2026, 12, 15), 1, F.at(2027, 1, 15)),
            (.agenda, F.at(2026, 10, 16), 1, F.at(2026, 11, 15)),
        ]
        for (v, a, dir, want) in cases {
            #expect(Cal.step(v, anchor: a, dir, zone: p) == want, "\(v) \(a) \(dir)")
        }
    }

    @Test func switchAnchor() {
        let today = F.at(2026, 10, 16, 15, 30)
        let anchor = F.at(2026, 12, 3)
        let cases: [(Cal.View, Cal.View, Date)] = [
            (.week, .agenda, F.at(2026, 10, 16)), (.day, .agenda, F.at(2026, 10, 16)), (.month, .agenda, F.at(2026, 10, 16)),
            (.agenda, .agenda, anchor), (.agenda, .week, anchor), (.agenda, .month, anchor), (.week, .month, anchor),
            (.month, .day, anchor), (.day, .week, anchor),
        ]
        for (from, to, want) in cases {
            #expect(Cal.switchAnchor(from: from, to: to, anchor: anchor, today: today, zone: p) == want, "\(from) → \(to)")
        }
    }

    @Test func weekStart() {
        let cases: [(WeekStart, Cal.Weekday, Cal.Weekday)] = [
            (.locale, .sunday, .sunday), (.locale, .monday, .monday), (.monday, .sunday, .monday),
            (.sunday, .monday, .sunday), (.saturday, .monday, .saturday), ("tuesday", .monday, .monday),
        ]
        for (pref, locale, want) in cases {
            #expect(Cal.weekStart(pref, localeFirstDay: locale) == want)
        }
    }

    @Test func weekNumber() {
        let u = F.utc
        let cases: [(Date, Cal.Weekday, Int)] = [
            // ISO 8601.
            (F.at(u, 2026, 1, 1, 0, 0), .monday, 1),
            (F.at(u, 2026, 10, 16, 0, 0), .monday, 42),
            (F.at(u, 2027, 1, 1, 0, 0), .monday, 53),
            (F.at(u, 2027, 1, 4, 0, 0), .monday, 1),
            (F.at(u, 2024, 12, 30, 0, 0), .monday, 1),
            // US: the week that holds 1 January is week 1.
            (F.at(u, 2026, 1, 1, 0, 0), .sunday, 1),
            (F.at(u, 2025, 12, 28, 0, 0), .sunday, 1),
            (F.at(u, 2026, 1, 4, 0, 0), .sunday, 2),
            (F.at(u, 2026, 10, 16, 0, 0), .sunday, 42),
            (F.at(u, 2026, 12, 27, 0, 0), .sunday, 1),
            (F.at(u, 2026, 12, 26, 0, 0), .sunday, 52),
            (F.at(u, 2027, 1, 1, 0, 0), .saturday, 1),
            (F.at(u, 2027, 1, 2, 0, 0), .saturday, 2),
        ]
        for (day, ws, want) in cases {
            #expect(Cal.weekNumber(day, weekStart: ws, zone: u) == want, "\(Cal.dateOf(day, u)) \(ws)")
        }
    }

    @Test func titleAndSubtitle() {
        let env = F.env()
        let cases: [(Cal.View, Date, Cal.Weekday, String, String)] = [
            (.week, F.at(2026, 10, 16, 9), .monday, "October 2026", "12–18 October · week 42"),
            (.week, F.at(2026, 10, 1, 9), .monday, "September–October 2026", "28 September–4 October · week 40"),
            (.week, F.at(2026, 12, 30, 9), .monday, "December 2026–January 2027", "28 December 2026–3 January 2027 · week 53"),
            (.week, F.at(2026, 10, 16, 9), .sunday, "October 2026", "11–17 October · week 42"),
            (.day, F.at(2026, 10, 16, 9), .monday, "16 October 2026", "Friday · week 42"),
            (.month, F.at(2026, 10, 16, 9), .monday, "October 2026", ""),
            (.month, F.at(2026, 6, 1, 9), .monday, "June 2026", ""),
            (.agenda, F.at(2026, 10, 16, 9), .monday, "October 2026", "16 October–14 November 2026"),
        ]
        for (v, a, ws, title, subtitle) in cases {
            let r = Cal.viewRange(v, anchor: a, weekStart: ws, zone: p)
            #expect(Cal.title(v, r, env: env) == title)
            #expect(Cal.subtitle(v, r, weekStart: ws, env: env) == subtitle)
        }
    }

    @Test func spanAndKey() {
        let s = F.at(2026, 10, 16, 10)
        var o = F.timed("ev", s, s.addingTimeInterval(3600))
        let span = Cal.span(o, p)
        #expect(span?.start == s && span?.end == s.addingTimeInterval(3600))
        o.end = nil
        let open = Cal.span(o, p)
        #expect(open != nil && open?.start == open?.end)
        var ad = F.occ()
        ad.allDay = true
        ad.startDate = "2026-10-16"
        let adSpan = Cal.span(ad, p)
        #expect(adSpan?.start == F.at(2026, 10, 16) && adSpan?.end == F.at(2026, 10, 17))
        ad.startDate = "16.10."
        #expect(Cal.span(ad, p) == nil)
        #expect(Cal.span(F.occ(), p) == nil)

        var keyed = F.occ()
        keyed.eventId = "ev"
        keyed.recurrenceId = .time(F.utcAt(2026, 10, 16, 8, 0))
        #expect(Cal.occurrenceKey(keyed) == "ev|2026-10-16T08:00:00Z")
        #expect(Cal.occurrenceKey(F.occ(invitation: MessageRef(accountId: "a", messageId: "m"))) == "inv:a/m")
        let point = F.timed("p", s, s)
        #expect(Cal.overlaps(point, from: F.at(2026, 10, 16), to: F.at(2026, 10, 17), p))
        #expect(!Cal.overlaps(o, from: F.at(2026, 10, 17), to: F.at(2026, 10, 18), p))
    }

    @Test func activeCalendars() {
        func cal(_ id: String, disabled: Bool = false, visible: Bool = true) -> CalendarRecord {
            CalendarRecord(id: CalendarID(id), accountId: "a", name: id, color: "", visible: visible, access: .owner,
                           kind: .events, disabled: disabled)
        }
        let got = Cal.activeCalendars([cal("a"), cal("b", disabled: true), cal("c", visible: false), cal("d")])
        #expect(got.map(\.id.rawValue) == ["a", "c", "d"])
        #expect(Cal.activeCalendars([]).isEmpty && Cal.activeCalendars([cal("x", disabled: true)]).isEmpty)
    }
}
