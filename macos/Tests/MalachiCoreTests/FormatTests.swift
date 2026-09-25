// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// The counterpart of ui/internal/widget/format_test.go. Dates are
/// rendered in a fixed locale so the month names are the English ones the
/// Go test expects whatever the machine's language.
@Suite struct FormatTests {
    private let posix = Locale(identifier: "en_US_POSIX")

    @Test func displayNameTest() {
        let cases: [(Address, String)] = [
            (Address(name: "Alice Example", address: "alice@example.invalid"), "Alice Example"),
            (Address(address: "bob@example.invalid"), "bob@example.invalid"),
            (Address(name: "   ", address: " carol@example.invalid "), "carol@example.invalid"),
            (Address(name: "<b>bold</b>", address: "x@y"), "<b>bold</b>"), // passed through, never markup
            (Address(address: ""), ""),
        ]
        for (input, want) in cases {
            #expect(displayName(input) == want, "displayName(\(input))")
        }
    }

    @Test func formatAddressTest() {
        let cases: [(Address, String)] = [
            (Address(name: "Alice", address: "alice@example.invalid"), "Alice <alice@example.invalid>"),
            (Address(address: "bob@example.invalid"), "bob@example.invalid"),
            (Address(name: "Nameless", address: ""), "Nameless"),
            (Address(address: ""), ""),
        ]
        for (input, want) in cases {
            #expect(formatAddress(input) == want, "formatAddress(\(input))")
        }
    }

    @Test func formatSizeTest() {
        let cases = [5: "5 B", 2048: "2 KiB", 3 << 20: "3.0 MiB"]
        for (input, want) in cases {
            #expect(formatSize(input) == want, "formatSize(\(input))")
        }
    }

    @Test func formatDateTest() throws {
        let cal = Calendar.current
        let now = try #require(cal.date(from: DateComponents(year: 2026, month: 9, day: 2, hour: 15, minute: 30)))
        let cases: [(Date, String)] = [
            (.goZero, ""),
            (now.addingTimeInterval(-2 * 3600), "13:30"),
            (try #require(cal.date(byAdding: .day, value: -1, to: now)), "1 Sep"),
            (try #require(cal.date(byAdding: .month, value: -3, to: now)), "2 Jun"),
            (try #require(cal.date(byAdding: .year, value: -1, to: now)), "2025-09-02"),
            (try #require(cal.date(from: DateComponents(year: 2026, month: 1, day: 1, hour: 0, minute: 0, second: 1))), "1 Jan"),
        ]
        for (input, want) in cases {
            #expect(formatDate(input, now: now, locale: posix, calendar: cal) == want, "formatDate(\(input))")
        }
        #expect(formatDateTime(now, locale: posix, calendar: cal) == "Wed, 2 Sep 2026 at 15:30")
        #expect(formatTime(now, locale: posix, calendar: cal) == "15:30")
    }

    @Test func formatParticipantsTest() {
        let list = [
            Address(name: "Bob", address: "bob@example.invalid"),
            Address(address: "BOB@example.invalid"), // the same person
            Address(name: "Alice", address: "alice@example.invalid"),
            Address(name: "Carol", address: ""), // name only
            Address(address: ""), // nothing
            Address(address: "dave@example.invalid"),
            Address(name: "<b>x</b>", address: "x@example.invalid"), // markup is text
        ]
        #expect(formatParticipants(list) == "Bob, Alice, Carol, dave@example.invalid, <b>x</b>")
        #expect(formatParticipants([]) == "")
    }

    @Test func threadCountTextTest() {
        for (n, want) in [0: "", 1: "", 2: "2", 17: "17"] {
            #expect(threadCountText(n) == want, "threadCountText(\(n))")
        }
    }
}
