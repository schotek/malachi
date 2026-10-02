// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// Remind… presets (MalachiCore/Board/BoardRemind.swift): Later Today,
// Tomorrow at 9, next Monday at 9. A UTC calendar and a fixed date; the
// 15th of October 2026 is a Thursday.

private typealias F = BoardFixture

@Suite struct BoardRemindTests {
    private func presets(_ now: Date) -> [Board.RemindPreset] {
        Board.remindPresets(now: now, calendar: F.calendar)
    }

    private func laterToday(_ now: Date) -> Date? {
        presets(now).first { $0.kind == .laterToday }?.date
    }

    @Test func atNoon() {
        let p = presets(F.day(15, 12))
        #expect(p.map(\.kind) == [.laterToday, .tomorrow, .nextWeek])
        #expect(p.map(\.date) == [F.day(15, 15), F.day(16, 9), F.day(19, 9)])
        #expect(p.map(\.title) == ["Later Today", "Tomorrow", "Next Week"])
        #expect(p.map(\.when) == ["Thu 15:00", "Fri 09:00", "Mon 09:00"])
    }

    /// Three hours ahead, rounded up to the hour; 18:00 when that is
    /// earlier and still an hour away; none past the day.
    @Test func laterTodayRule() {
        let cases: [(Date, Date?)] = [
            (F.day(15, 8), F.day(15, 11)),
            (F.day(15, 12, 20), F.day(15, 16)),
            (F.day(15, 14, 59), F.day(15, 18)),
            (F.day(15, 15, 30), F.day(15, 18)),  // 19:00 rounded, 18:00 first
            (F.day(15, 16, 30), F.day(15, 18)),
            (F.day(15, 17), F.day(15, 18)),  // exactly an hour away
            (F.day(15, 17, 30), F.day(15, 21)),  // 18:00 too close
            (F.day(15, 19, 30), F.day(15, 23)),
            (F.day(15, 20, 59), nil),  // 23:59 rounds up to midnight: tomorrow
            (F.day(15, 21), nil),
            (F.day(15, 23, 30), nil),
        ]
        for (now, want) in cases {
            #expect(laterToday(now) == want, "\(now)")
        }
    }

    @Test func nextWeekIsTheComingMonday() {
        // Sunday the 18th: Monday is tomorrow, offered once, as Tomorrow.
        let sunday = presets(F.day(18, 10))
        #expect(sunday.map(\.kind) == [.laterToday, .tomorrow])
        #expect(sunday.first { $0.kind == .tomorrow }?.date == F.day(19, 9))
        // Monday the 19th: a week ahead.
        #expect(presets(F.day(19, 8)).first { $0.kind == .nextWeek }?.date == F.day(26, 9))
        // Saturday the 17th.
        #expect(presets(F.day(17, 23)).first { $0.kind == .nextWeek }?.date == F.day(19, 9))
    }

    /// Every preset lies after now and within the daemon's year.
    @Test func alwaysInTheFuture() {
        var t = F.day(12, 0)
        while t < F.day(20, 0) {
            for p in presets(t) {
                #expect(p.date > t && p.date.timeIntervalSince(t) < 365 * 86400, "\(p.kind) at \(t)")
            }
            t = t.addingTimeInterval(17 * 60)
        }
    }

    /// Across a change of daylight saving time the presets keep their wall
    /// clock time (Prague: summer time ends on Sunday 25 October 2026 and
    /// starts on Sunday 29 March 2026).
    @Test func daylightSavingTime() {
        var cal = Calendar(identifier: .gregorian)
        cal.timeZone = TimeZone(identifier: "Europe/Prague")!
        cal.locale = Locale(identifier: "en_US_POSIX")
        func at(_ m: Int, _ d: Int, _ h: Int, _ min: Int = 0) -> Date {
            cal.date(from: DateComponents(year: 2026, month: m, day: d, hour: h, minute: min))!
        }
        // Saturday before the autumn change: Tomorrow is Sunday 09:00,
        // Next Week Monday 09:00, both in winter time.
        var p = Board.remindPresets(now: at(10, 24, 12), calendar: cal)
        #expect(p.map(\.date) == [at(10, 24, 15), at(10, 25, 9), at(10, 26, 9)])
        #expect(p.map(\.when) == ["Sat 15:00", "Sun 09:00", "Mon 09:00"])
        // On the night of the change: Later Today counts real hours.
        p = Board.remindPresets(now: at(10, 25, 1, 30), calendar: cal)
        #expect(p.first?.kind == .laterToday && p.first?.date == at(10, 25, 4))
        // Saturday before the spring change.
        p = Board.remindPresets(now: at(3, 28, 20), calendar: cal)
        #expect(p.map(\.date) == [at(3, 28, 23), at(3, 29, 9), at(3, 30, 9)])
        #expect(p.map(\.when) == ["Sat 23:00", "Sun 09:00", "Mon 09:00"])
    }
}
