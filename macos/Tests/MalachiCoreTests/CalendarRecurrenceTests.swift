// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// MalachiCore/Calendar/CalendarRecurrence.swift, the cases of Go
// recurrence_test.go.

private typealias F = CalFixture

private func rule(
    _ f: RecurrenceFrequency, _ interval: Int = 0, byDay: [String]? = nil, byMonthDay: [Int]? = nil, byMonth: [Int]? = nil,
    until: CalendarDay? = nil, count: Int = 0, complex: Bool = false
) -> Recurrence {
    Recurrence(
        frequency: f, interval: interval, byDay: byDay, byMonthDay: byMonthDay, byMonth: byMonth, until: until, count: count,
        complex: complex)
}

@Suite struct CalendarRecurrenceTests {
    private let p = F.prague

    @Test func recurrenceText() {
        let env = F.env()
        let start = F.at(2026, 10, 13, 10)  // a Tuesday
        let cases: [(Recurrence?, String)] = [
            (nil, ""),
            (rule(.daily, 1), "Every day"),
            (rule(.daily), "Every day"),
            (rule(.daily, 3), "Every 3 days"),
            (rule(.daily, 1, count: 10), "Every day, 10 times"),
            (rule(.daily, 1, count: 1), "Every day, 1 time"),
            (rule(.weekly, 1), "Every week on Tuesday"),
            (rule(.weekly, 1, byDay: ["TU"], until: "2027-03-01"), "Every week on Tuesday, until 1 March 2027"),
            (rule(.weekly, 2, byDay: ["WE", "MO"]), "Every 2 weeks on Monday and Wednesday"),
            (rule(.weekly, 1, byDay: ["FR", "MO", "WE"]), "Every week on Monday, Wednesday and Friday"),
            (rule(.weekly, 1, byDay: ["MO", "TU", "WE", "TH", "FR"]), "Every weekday"),
            (rule(.weekly, 1, byDay: ["SU", "MO", "TU", "WE", "TH", "FR", "SA"]), "Every day"),
            (rule(.weekly, 2, byDay: ["SU", "MO"]), "Every 2 weeks on Monday and Sunday"),
            (rule(.monthly, 1), "Monthly on day 13"),
            (rule(.monthly, 1, byMonthDay: [14]), "Monthly on day 14"),
            (rule(.monthly, 3, byMonthDay: [14]), "Every 3 months on day 14"),
            (rule(.monthly, 1, byMonthDay: [-1]), "Monthly on the last day"),
            (rule(.monthly, 1, byDay: ["2TU"]), "Monthly on the second Tuesday"),
            (rule(.monthly, 1, byDay: ["+1MO"]), "Monthly on the first Monday"),
            (rule(.monthly, 1, byDay: ["-1FR"]), "Monthly on the last Friday"),
            (rule(.monthly, 2, byDay: ["-2FR"]), "Every 2 months on the second-to-last Friday"),
            (rule(.yearly, 1), "Yearly on 13 October"),
            (rule(.yearly, 1, byMonthDay: [29], byMonth: [2]), "Yearly on 29 February"),
            (rule(.yearly, 2, byDay: ["2TU"], byMonth: [10]), "Every 2 years on the second Tuesday of October"),
            // Outside the editor's shape.
            (rule(.weekly, 1, complex: true), "Custom"),
            (rule(.weekly, 1, byDay: ["2TU"]), "Custom"),
            (rule(.monthly, 1, byDay: ["1MO", "3MO"]), "Custom"),
            (rule(.monthly, 1, byDay: ["MO"]), "Custom"),
            (rule(.monthly, 1, byMonthDay: [-2]), "Custom"),
            (rule(.monthly, 1, byMonth: [3]), "Custom"),
            (rule(.daily, 1, byDay: ["MO"]), "Custom"),
            (rule(.yearly, 1, byMonth: [2, 3]), "Custom"),
            (rule(.yearly, 1, byMonthDay: [31], byMonth: [4]), "Custom"),
            (rule(.yearly, 1, byMonth: [13]), "Custom"),
            (rule("hourly", 1), "Custom"),
            (rule(.weekly, 1, byDay: ["XX"]), "Custom"),
        ]
        for (r, want) in cases {
            #expect(Cal.recurrenceText(r, start: start, env: env) == want, "\(String(describing: r))")
        }
    }

    @Test func repeatChoices() {
        let env = F.env()
        let start = F.at(2026, 10, 13, 10)
        let texts: [Cal.Repeat: String] = [
            .daily: "Every day", .weekly: "Every week on Tuesday", .biweekly: "Every 2 weeks on Tuesday",
            .monthly: "Monthly on day 13", .yearly: "Yearly on 13 October",
        ]
        for c in Cal.repeats {
            let r = Cal.repeatRecurrence(c, start: start, zone: p)
            #expect((r == nil) == (c == .never || c == .custom))
            #expect(Cal.recurrenceText(r, start: start, env: env) == texts[c, default: ""], "\(c)")
            if c != .custom {
                #expect(Cal.repeatOf(r, start: start, zone: p) == c)
            }
            #expect(!Cal.repeatTitle(c).isEmpty)
        }
        let cases: [(Recurrence, Cal.Repeat)] = [
            (rule(.weekly), .weekly),
            (rule(.weekly, 2), .biweekly),
            (rule(.monthly, 1), .monthly),
            (rule(.yearly, 1), .yearly),
            (rule(.daily, 1, count: 3), .custom),
            (rule(.weekly, 1, byDay: ["MO"]), .custom),
            (rule(.weekly, 3), .custom),
            (rule(.daily, 1, complex: true), .custom),
        ]
        for (r, want) in cases {
            #expect(Cal.repeatOf(r, start: start, zone: p) == want, "\(r)")
        }
        #expect(Cal.repeatTitle(.biweekly) == "Every 2 Weeks" && Cal.repeatTitle(.custom) == "Custom…"
            && Cal.repeatTitle(.never) == "Never")
    }

    @Test func customRecurrence() {
        let u = F.utc
        let start = F.utcAt(2026, 10, 13, 10, 0)
        let r = Cal.customRecurrence(.weekly, interval: 0, days: [.friday, .monday, .friday, .sunday], start: start, zone: u)
        #expect(r.interval == 1 && r.byDay == ["MO", "FR", "SU"])
        let d = Cal.customRecurrence(.weekly, interval: 150, days: [], start: start, zone: u)
        #expect(d.interval == 99 && d.byDay == ["TU"])
        #expect(Cal.customRecurrence(.monthly, interval: 2, days: [], start: start, zone: u).byMonthDay == [13])
        let y = Cal.customRecurrence(.yearly, interval: 1, days: [], start: start, zone: u)
        #expect(y.byMonth == [10] && y.byMonthDay == [13])
        let daily = Cal.customRecurrence(.daily, interval: 5, days: [], start: start, zone: u)
        #expect(daily.interval == 5 && daily.byDay == nil)
        #expect(Cal.intervalUnit(.weekly, 2) == "weeks" && Cal.intervalUnit(.daily, 1) == "day")
    }

    @Test func parseDayCode() {
        let cases: [(String, Cal.Weekday, Int, Bool)] = [
            ("MO", .monday, 0, true), ("2TU", .tuesday, 2, true), ("-1FR", .friday, -1, true), ("+3SU", .sunday, 3, true),
            ("0MO", .sunday, 0, false), ("6MO", .sunday, 0, false), ("M", .sunday, 0, false), ("XX", .sunday, 0, false),
            ("aMO", .sunday, 0, false),
        ]
        for (s, d, ord, ok) in cases {
            let got = Cal.parseDayCode(s)
            #expect((got != nil) == ok, "\(s)")
            if ok {
                #expect(got?.day == d && got?.ordinal == ord, "\(s)")
            }
        }
        #expect(Cal.listText([]) == "" && Cal.listText(["a"]) == "a" && Cal.listText(["a", "b"]) == "a and b")
    }
}
