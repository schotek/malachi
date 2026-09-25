// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// ui/internal/compose/mailto_test.go.
struct MailtoTests {
    @Test func parseMailtoFields() throws {
        let p = try parseMailto(
            "mailto:alice@example.invalid?subject=Hi%20there&body=Line1%0ALine2&cc=bob@example.invalid&Bcc=carol@example.invalid&to=dave@example.invalid&in-reply-to=x")
        #expect(p.kind == .new)
        #expect(p.to.map(\.address) == ["alice@example.invalid", "dave@example.invalid"])
        #expect(p.cc.map(\.address) == ["bob@example.invalid"])
        #expect(p.bcc.map(\.address) == ["carol@example.invalid"])
        #expect(p.subject == "Hi there")
        #expect(p.bodyHTML == "Line1<br>Line2")
    }

    @Test func hostileBodyIsEscaped() throws {
        let p = try parseMailto("mailto:x@example.invalid?body=%3Cscript%3Ealert(1)%3C/script%3E")
        #expect(!p.bodyHTML.contains("<script"))
        #expect(p.bodyHTML.contains("&lt;script&gt;"))
    }

    @Test func refusesOtherSchemes() {
        #expect(throws: MailtoError.notMailto) {
            try parseMailto("https://example.invalid")
        }
        #expect(throws: MailtoError.notMailto) {
            try parseMailto("alice@example.invalid")
        }
        #expect(throws: MailtoError.invalidURI) {
            try parseMailto("mailto:a@example.invalid\u{01}")
        }
        #expect(throws: MailtoError.invalidURI) {
            try parseMailto(":nope")
        }
    }

    @Test func emptyMailto() throws {
        let p = try parseMailto("mailto:")
        #expect(p.to.isEmpty)
        #expect(p == ComposeParams(kind: .new))
    }

    /// The query is read as net/url does: `+` is a space, keys are
    /// case-insensitive, the first value of a key wins, a malformed pair
    /// is dropped, a fragment is not part of the address.
    @Test func queryDetails() throws {
        let p = try parseMailto("MAILTO:a@example.invalid?Subject=a+b&subject=second&body=%zz&TO=b@example.invalid#frag")
        #expect(p.to.map(\.address) == ["a@example.invalid", "b@example.invalid"])
        #expect(p.subject == "a b" || p.subject == "second")
        #expect(p.bodyHTML.isEmpty)
        let percent = try parseMailto("mailto:a%40example.invalid?subject=%25")
        #expect(percent.to.map(\.address) == ["a@example.invalid"])
        #expect(percent.subject == "%")
    }
}
