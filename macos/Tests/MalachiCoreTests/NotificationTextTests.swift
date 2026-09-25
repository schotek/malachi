// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// The counterpart of ui/internal/window/notify_test.go (which also holds
/// the preference-choice cases of preferences.go).
@Suite struct NotificationTextTests {
    @Test func notificationTextTest() {
        var s = summary("m")
        s.from = [Address(name: "Alice Example", address: "alice@example.invalid")]
        s.subject = "  Lunch?  "
        var n = NewMessageNotification(accountId: "a", folderId: "f", message: s)
        var got = notificationText(n)
        #expect(got.title == "Alice Example")
        #expect(got.body == "Lunch?")

        n.message.from = []
        n.message.subject = ""
        got = notificationText(n)
        #expect(got.title == "New message")
        #expect(got.body == "(No subject)")

        n.message.subject = String(repeating: "ž", count: 500)
        got = notificationText(n)
        #expect(got.body.count <= notificationBodyMax + 1)
        #expect(got.body.hasSuffix("…"))
        #expect(got.body.contains("ž"))
        #expect(!got.body.contains("\u{FFFD}"), "cap left an invalid UTF-8 sequence")
        // 200 bytes hold exactly 100 two-byte characters.
        #expect(got.body.count == 101)

        // The title, a display name, is capped the same way.
        n.message.from = [Address(name: String(repeating: "ž", count: 500), address: "x@example.invalid")]
        got = notificationText(n)
        #expect(got.title.count == 101)
        #expect(got.title.hasSuffix("…"))
        #expect(!got.title.contains("\u{FFFD}"), "cap left an invalid UTF-8 sequence")
        n.message.from = [Address(name: String(repeating: "a", count: 200), address: "x@example.invalid")]
        #expect(notificationText(n).title.count == 200, "a title at the cap is not cut")
    }

    @Test func nearestIntervalTest() {
        let cases = [0: 0, -5: 0, 60: 1, 300: 1, 500: 1, 700: 2, 900: 2, 1300: 2, 1400: 3, 1800: 3, 99999: 3]
        for (input, want) in cases {
            #expect(nearestInterval(input) == want, "nearestInterval(\(input))")
        }
        #expect(indexOfPolicy(.allow) == 2)
        #expect(indexOfPolicy("bogus") == 0)
    }

    @Test func indexOfRetentionTest() {
        let cases = [7: 0, 10: 0, 30: 1, 60: 1, 90: 2, 200: 2, 365: 3, 1000: 3, 0: 4, -1: 4]
        for (input, want) in cases {
            #expect(indexOfRetention(input) == want, "indexOfRetention(\(input))")
        }
        for (i, days) in retentionChoices.enumerated() {
            #expect(indexOfRetention(days) == i, "retentionChoices[\(i)]=\(days) maps back")
        }
    }
}
