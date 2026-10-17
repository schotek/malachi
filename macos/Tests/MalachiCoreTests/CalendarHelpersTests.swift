// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The fixtures of the calendar tests (Go helpers_test.go) and the tests of
// format.go (EnglishStrftime) and of the wall-clock arithmetic the port
// adds in place of Go's time.Date.

enum CalFixture {
    static let prague = TimeZone(identifier: "Europe/Prague")!
    static let utc = TimeZone(identifier: "UTC")!

    /// The English env in `zone`.
    static func env(_ zone: TimeZone = prague) -> Cal.Env { .english(zone) }

    /// A time in `zone` (Go `at`).
    static func at(_ zone: TimeZone, _ y: Int, _ m: Int, _ d: Int, _ h: Int, _ mi: Int) -> Date {
        Cal.date(y, m, d, h, mi, in: zone)
    }

    /// A Prague time.
    static func at(_ y: Int, _ m: Int, _ d: Int, _ h: Int = 0, _ mi: Int = 0) -> Date {
        at(prague, y, m, d, h, mi)
    }

    /// A UTC instant.
    static func utcAt(_ y: Int, _ m: Int, _ d: Int, _ h: Int, _ mi: Int) -> Date {
        at(utc, y, m, d, h, mi)
    }

    /// A timed occurrence with an id (Go `timed`).
    static func timed(_ id: String, _ start: Date, _ end: Date) -> Occurrence {
        Occurrence(
            eventId: EventID(id), accountId: "acc", title: id, start: start, end: end, allDay: false, status: .confirmed,
            showAs: .busy)
    }

    /// An all-day occurrence from `start` for `days` days (Go `allDayOcc`).
    static func allDayOcc(_ id: String, _ start: Date, _ days: Int, _ zone: TimeZone = prague) -> Occurrence {
        Occurrence(
            eventId: EventID(id), accountId: "acc", title: id, startDate: Cal.dateOf(start, zone),
            endDate: Cal.dateOf(Cal.addDays(Cal.startOfDay(start, zone), days, zone), zone), allDay: true, status: .confirmed,
            showAs: .free)
    }

    /// A bare occurrence (Go's zero value plus what a case sets).
    static func occ(
        status: EventStatus = EventStatus(""), showAs: ShowAs = ShowAs(""), myResponse: EventResponse? = nil,
        invitation: MessageRef? = nil
    ) -> Occurrence {
        Occurrence(accountId: "", title: "", allDay: false, status: status, showAs: showAs, myResponse: myResponse, invitation: invitation)
    }

    /// A bare event (Go's zero value).
    static func event(_ o: Occurrence = occ()) -> Event {
        Event(
            occurrence: o, etag: "", attendeesTruncated: false, responseRequested: false, canEdit: false, canDelete: false,
            canRespond: false, canProposeNewTime: false, removeIsDelete: false)
    }

    /// A bare invitation card (Go's zero value).
    static func card(_ s: InvitationSummary = InvitationSummary(method: "", kind: "", state: "", title: "")) -> InvitationCard {
        InvitationCard(
            summary: s, partId: "", uid: "", sequence: 0, organizerMismatch: false, attendeeCount: 0, conflicts: [],
            day: InvitationDay(from: Date(), to: Date()), targets: [], fixedTarget: false, canRespond: false,
            canProposeNewTime: false, replyVia: "", replyApplied: false)
    }

    /// The hours of an interval.
    static func hours(_ a: Date, _ b: Date) -> Double { b.timeIntervalSince(a) / 3600 }
}

@Suite struct CalendarHelpersTests {
    private typealias F = CalFixture

    @Test func englishStrftime() {
        let t = F.utcAt(2026, 10, 4, 9, 5)
        let cases = [
            "%a %A %b %B": "Sun Sunday Oct October",
            "%d %-d %e|%-e": "04 4  4|4",
            "%H:%M %-H": "09:05 9",
            "%I %-I %p": "09 9 AM",
            "%m %-m %Y %y": "10 10 2026 26",
            "100%% %q": "100% %q",
            "%OB %Y|%OX": "October 2026|%OX",
        ]
        for (f, want) in cases {
            #expect(Cal.englishStrftime(t, f, zone: F.utc) == want, "\(f)")
        }
        let noonish = Cal.englishStrftime(t.addingTimeInterval(15 * 3600), "%-I %p", zone: F.utc)
        #expect(noonish == "12 AM" || noonish == "12 PM")
    }

    /// Go's time.Date normalises every part; a time the clocks skip moves
    /// on by the gap.
    @Test func dateNormalises() {
        #expect(Cal.date(2026, 13, 1, in: F.prague) == Cal.date(2027, 1, 1, in: F.prague))
        #expect(Cal.date(2026, 3, 0, in: F.prague) == Cal.date(2026, 2, 28, in: F.prague))
        #expect(Cal.date(2026, 10, 14, 23, 60, in: F.prague) == Cal.date(2026, 10, 15, in: F.prague))
        #expect(Cal.date(2026, 1, -1, in: F.utc) == Cal.date(2025, 12, 30, in: F.utc))
        // 29 March 2026, 02:00 CET does not exist: 03:00 CEST (01:00 UTC).
        #expect(Cal.date(2026, 3, 29, 2, 0, in: F.prague) == F.utcAt(2026, 3, 29, 1, 0))
        // 25 October 2026, 02:30 is the first one (CEST, 00:30 UTC).
        #expect(Cal.date(2026, 10, 25, 2, 30, in: F.prague) == F.utcAt(2026, 10, 25, 0, 30))
        for n in [-800_000, -1, 0, 1, 20_000, 100_000] {
            let c = Cal.civilFromDays(n)
            #expect(Cal.daysFromCivil(c.year, c.month, c.day) == n)
        }
    }

    /// %OB is the standalone month name (LLLL): the nominative in Czech.
    @Test func standaloneMonth() {
        #expect(Strftime.toDateFormat("%OB %Y") == "LLLL' 'yyyy")
        #expect(Strftime.toDateFormat("%B") == "MMMM")
        #expect(Strftime.toDateFormat("%Oq") == "'%Oq'")
        let t = F.at(2026, 10, 16, 12)
        #expect(Cal.englishStrftime(t, "%OB %Y", zone: F.prague) == "October 2026")
        let cs = Cal.Env(zone: F.prague, locale: Locale(identifier: "cs_CZ"))
        #expect(cs.format(t, "%OB %Y", F.prague) == "říjen 2026")
        #expect(cs.format(t, "%-d. %B", F.prague) == "16. října")
    }

    /// A C length modifier a catalogue generator added ("%-ld") is read as
    /// the directive after it, never shown as text.
    @Test func lengthModifiers() {
        #expect(Strftime.toDateFormat("%ld %-ld %lH %-lldx") == Strftime.toDateFormat("%d %-d %H %-dx"))
        #expect(Strftime.toDateFormat("%l") == "'%l'" && Strftime.toDateFormat("%-l") == "'%-l'")
        let t = F.at(2026, 10, 16, 9)
        let cs = Cal.Env(zone: F.prague, locale: Locale(identifier: "cs_CZ"))
        #expect(cs.format(t, "%-ld. %OB %Y", F.prague) == "16. říjen 2026")
        #expect(cs.format(t, "%-ld", F.prague) == "16")
        #expect(cs.format(t, "%lH:%lM", F.prague) == "09:00")
        #expect(Cal.englishStrftime(t, "%-ld %OB %Y", zone: F.prague) == "16 October 2026")
        #expect(Cal.englishStrftime(t, "%-ld|%ld|%lH|%-lld", zone: F.prague) == "16|16|09|16")
    }

    @Test func weekdayOfFoundation() {
        #expect(Cal.Weekday(firstWeekday: 1) == .sunday)
        #expect(Cal.Weekday(firstWeekday: 2) == .monday)
        #expect(Cal.Weekday(firstWeekday: 7) == .saturday)
    }
}
