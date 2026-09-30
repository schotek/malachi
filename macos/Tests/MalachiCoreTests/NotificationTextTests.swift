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

    /// A message of an issue: the author, and the issue's key and summary
    /// from `issue`, whatever the subject says.
    @Test func issueNotificationText() {
        func issue(_ key: String, _ summary: String, item: IssueItemKind = .comment) -> MessageIssue {
            MessageIssue(info: IssueInfo(key: key, url: "https://acme.atlassian.net/browse/" + key, summary: summary, status: "Open"),
                         item: item)
        }
        var s = summary("m")
        s.from = [Address(name: "Jana Dvořáková", address: "jana@acme.example")]
        s.subject = "ITSD-42: The printer is on fire"
        s.issue = issue("ITSD-42", "The printer is on fire")
        var n = NewMessageNotification(accountId: "j", folderId: "f", message: s)
        var got = notificationText(n)
        #expect(got.title == "Jana Dvořáková")
        #expect(got.body == "ITSD-42: The printer is on fire")

        // The issue, not the subject; a description like a comment.
        n.message.subject = "Re: something else"
        n.message.issue = issue("WEB-7", "  Broken\n link ", item: .description)
        #expect(notificationText(n).body == "WEB-7: Broken link")

        // Whichever of the two the issue has; the subject when it has
        // neither.
        n.message.issue = issue("WEB-7", "")
        #expect(notificationText(n).body == "WEB-7")
        n.message.issue = issue("", "Broken link")
        #expect(notificationText(n).body == "Broken link")
        n.message.issue = issue(" ", "\t")
        #expect(notificationText(n).body == "Re: something else")
        n.message.subject = " "
        #expect(notificationText(n).body == "(No subject)")

        // Hostile text: one line, nothing invisible, capped like a subject.
        let override = String(Unicode.Scalar(0x202E)!)
        let zeroWidth = String(Unicode.Scalar(0x200B)!)
        n.message.issue = issue("MOB-1" + zeroWidth, "Pay" + override + "\r\nnow\u{0}" + String(repeating: "ž", count: 500))
        got = notificationText(n)
        #expect(got.body.hasPrefix("MOB-1: Pay now"))
        #expect(!got.body.unicodeScalars.contains { $0.properties.generalCategory == .format || $0.properties.generalCategory == .control })
        #expect(got.body.utf8.count <= notificationBodyMax + 3 && got.body.hasSuffix("…"))
        #expect(!got.body.contains("\u{FFFD}"), "cap left an invalid UTF-8 sequence")

        // A mail message is as before.
        n.message.issue = nil
        n.message.subject = "  Lunch?  "
        #expect(notificationText(n).body == "Lunch?")
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

    /// preferences.go `indexOfAttachmentDays`: -1 small only, 0 everything,
    /// otherwise the nearest, a tie going to the smaller.
    @Test func indexOfAttachmentDaysTest() {
        let cases = [-1: 0, -7: 0, 0: 4, 1: 1, 7: 1, 18: 1, 19: 2, 30: 2, 59: 2, 60: 2, 61: 3, 90: 3, 365: 3, 3650: 3]
        for (input, want) in cases {
            #expect(indexOfAttachmentDays(input) == want, "indexOfAttachmentDays(\(input))")
        }
        for (i, days) in attachmentChoices.enumerated() {
            #expect(indexOfAttachmentDays(days) == i, "attachmentChoices[\(i)]=\(days) maps back")
        }
        #expect(attachmentChoices == [-1, 7, 30, 90, 0])
    }
}
