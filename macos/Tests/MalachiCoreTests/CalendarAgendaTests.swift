// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// MalachiCore/Calendar/CalendarAgenda.swift, the cases of Go agenda_test.go.

private typealias F = CalFixture

@Suite struct CalendarAgendaTests {
    @Test func agenda() {
        let env = F.env()
        let now = F.at(2026, 10, 14, 12)
        let trip = F.allDayOcc("trip", F.at(2026, 10, 15), 3)
        let night = F.timed("night", F.at(2026, 10, 14, 22), F.at(2026, 10, 15, 2))
        let midnight = F.timed("mid", F.at(2026, 10, 14, 23), F.at(2026, 10, 15))
        let early = F.timed("early", F.at(2026, 10, 14, 8), F.at(2026, 10, 14, 9))
        var declined = F.timed("declined", F.at(2026, 10, 14, 9), F.at(2026, 10, 14, 10))
        declined.myResponse = .declined
        let before = F.timed("before", F.at(2026, 10, 1, 8), F.at(2026, 10, 1, 9))
        let days = Cal.agenda([trip, night, midnight, early, declined, before], from: now, days: 30, now: now, env: env, showDeclined: false)
        let got = days.map { d in
            d.heading + ":" + d.items.map { " \($0.occurrence.eventId!.rawValue)(\($0.dayOf)/\($0.days) \(Cal.itemTime($0, env: env)))" }.joined()
        }
        #expect(got == [
            "Today: early(1/1 08:00–09:00) night(1/2 from 22:00) mid(1/1 23:00–00:00)",
            "Tomorrow: trip(1/3 All Day) night(2/2 until 02:00)",
            "Friday 16 October: trip(2/3 All Day)",
            "Saturday 17 October: trip(3/3 All Day)",
        ])
        #expect(Cal.agenda([], from: now, days: 30, now: now, env: env, showDeclined: false).isEmpty)
        #expect(Cal.dayOfText(2, of: 3) == "Day 2 of 3")
        let end = now.addingTimeInterval(72 * 3600)
        let middle = Cal.AgendaItem(occurrence: F.timed("x", now, end), start: now, end: end, dayOf: 2, days: 4)
        #expect(Cal.itemTime(middle, env: env) == "All Day")
    }

    @Test func dayHeading() {
        let env = F.env()
        let now = F.at(2026, 12, 31, 23, 30)
        let cases: [(Date, String)] = [
            (F.at(2026, 12, 31), "Today"),
            (F.at(2027, 1, 1), "Tomorrow"),
            (F.at(2026, 12, 30), "Yesterday"),
            (F.at(2026, 12, 25), "Friday 25 December"),
            (F.at(2027, 1, 5), "Tuesday 5 January 2027"),
        ]
        for (day, want) in cases {
            #expect(Cal.dayHeading(day, now: now, env: env) == want)
        }
    }
}
