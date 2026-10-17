// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// MalachiCore/Calendar/CalendarAvailability.swift, the cases of Go
// availability_test.go.

private typealias F = CalFixture

@Suite struct CalendarAvailabilityTests {
    private func h(_ hh: Int, _ mm: Int = 0) -> Date { F.at(2026, 10, 14, hh, mm) }

    @Test func availability() {
        let env = F.env()
        func busy(_ s: Date, _ e: Date, _ a: ShowAs) -> BusyInterval { BusyInterval(start: s, end: e, showAs: a) }
        let res = CalendarFreeBusyResult(
            me: Availability(address: "me@example.org", known: true, busy: [busy(h(9), h(10), .busy)]),
            attendees: [
                Availability(address: "jana@example.org", known: true, busy: [
                    busy(h(13), h(14), .oof), busy(h(7), h(9), .busy), busy(h(10), h(11), .free),
                    busy(h(11), h(12), .workingElsewhere),
                ]),
                Availability(address: "petr@example.org", known: true, busy: [busy(h(10), h(10, 30), .tentative)]),
                Availability(address: "eva@example.org", known: false),
            ])
        let rows = Cal.availabilityRows(res, names: ["jana@example.org": "Jana"], from: h(8), to: h(16))
        #expect(rows.count == 4 && rows[0].label == "You" && rows[1].label == "Jana" && rows[2].label == "petr@example.org" && !rows[3].known)
        #expect(rows[1].busy == [Cal.AxisSpan(from: 0, to: 0.125, showAs: .busy), Cal.AxisSpan(from: 0.625, to: 0.75, showAs: .oof)])
        #expect(rows[0].busy == [Cal.AxisSpan(from: 0.125, to: 0.25, showAs: .busy)] && rows[3].busy.isEmpty)
        let cases: [(Date, Date, [String])] = [
            (h(15), h(16), ["15:00–16:00: all known attendees are free", "Availability of 1 attendee is unknown"]),
            (h(10), h(11), ["1 of 2 attendees is busy", "Availability of 1 attendee is unknown"]),
            (h(8, 30), h(10, 15), ["2 of 2 attendees are busy", "Availability of 1 attendee is unknown"]),
            (h(11), h(12), ["11:00–12:00: all known attendees are free", "Availability of 1 attendee is unknown"]),
        ]
        for (s, e, want) in cases {
            #expect(Cal.summary(res, start: s, end: e, env: env) == want)
        }
        #expect(Cal.summary(CalendarFreeBusyResult(me: res.me), start: h(9), end: h(10), env: env).isEmpty)
        #expect(Cal.summary(CalendarFreeBusyResult(me: res.me, attendees: Array(res.attendees[2...])), start: h(9), end: h(10), env: env)
            == ["Availability of 1 attendee is unknown"])
        #expect(Cal.axisSpans(res.me.busy, from: h(10), to: h(10)).isEmpty)
    }

    @Test func nextFreeSlot() {
        let env = F.env()
        let res = CalendarFindFreeTimeResult(slots: [
            TimeSlot(start: h(15), end: h(16)), TimeSlot(start: h(9), end: h(10)), TimeSlot(start: h(12), end: h(12)),
            TimeSlot(start: h(11), end: h(12)),
        ])
        let s = Cal.nextFreeSlot(res, after: h(10))
        #expect(s?.start == h(11))
        #expect(Cal.nextFreeSlot(res, after: h(17)) == nil)
        #expect(Cal.nextFreeSlotText(s!, env: env) == "Next free slot: Wednesday 14 October 2026 · 11:00–12:00")
        #expect(Cal.busy(.unknown) && !Cal.busy(.free) && !Cal.busy(.workingElsewhere))
    }
}
