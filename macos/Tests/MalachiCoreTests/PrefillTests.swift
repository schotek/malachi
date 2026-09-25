// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// ui/internal/compose/prefill_test.go. The texts are the English msgids:
/// the process-wide catalogue is English unless a test swaps it.
struct PrefillTests {
    @Test func subjectPrefixes() {
        let cases = [
            "Hello": "Re: Hello",
            "Re: Hello": "Re: Hello",
            "RE: re: Hello": "Re: Hello",
            "Fwd: FW: Hello": "Re: Hello",
            "AW: Hello": "Re: Hello",
            "  Re:   spaced  ": "Re: spaced",
            "": "Re: ",
            "Rear window": "Re: Rear window",
        ]
        for (input, want) in cases {
            #expect(replySubject(input) == want, "ReplySubject(\(input))")
        }
        #expect(forwardSubject("Re: Fwd: x") == "Fwd: x")
    }

    @Test func prefillEscapes() throws {
        let me = Address(address: "me@example.invalid")
        let date: Date = try #require(RFC3339.parse("2026-09-02T14:03:00Z"))
        let src = ComposeSource(
            id: "m_1",
            from: [Address(name: "<b>Alice</b>", address: "alice@example.invalid")],
            to: [me, Address(address: "bob@example.invalid")],
            cc: [Address(address: "ALICE@example.invalid"), Address(address: "carol@example.invalid")],
            subject: "Re: <script>alert(1)</script> & co",
            date: date,
            text: "line1\r\nline2 </blockquote><img src=x onerror=alert(1)>"
        )

        let p = prefill(kind: .replyAll, source: src, self: me)
        #expect(p.kind == .replyAll)
        #expect(p.inReplyTo == "m_1")
        #expect(p.forwarding == nil)
        #expect(p.to.map(\.address) == ["alice@example.invalid"])
        // Self and duplicates must be dropped.
        #expect(p.cc.map(\.address) == ["bob@example.invalid", "carol@example.invalid"])
        #expect(p.subject == "Re: <script>alert(1)</script> & co")
        // Nothing from the source may become markup: only our own tags exist.
        for bad in ["<script", "<img", "<b>Alice"] {
            #expect(!p.bodyHTML.contains(bad), "unescaped \(bad) in body")
        }
        #expect(p.bodyHTML.components(separatedBy: "<blockquote").count == 2)
        #expect(p.bodyHTML.components(separatedBy: "</blockquote>").count == 2)
        for good in ["&lt;b&gt;Alice&lt;/b&gt; wrote:</div>", "line1<br>line2", "<blockquote type=\"cite\">", "&lt;img src=x onerror=alert(1)&gt;"] {
            #expect(p.bodyHTML.contains(good), "missing \(good) in body")
        }

        let f = prefill(kind: .forward, source: src, self: me)
        #expect(f.forwarding == "m_1")
        #expect(f.inReplyTo == nil)
        #expect(f.to.isEmpty)
        #expect(f.subject == "Fwd: <script>alert(1)</script> & co")
        #expect(f.bodyHTML.contains("Forwarded message"))
        #expect(!f.bodyHTML.contains("<script>"))

        let r = prefill(kind: .reply, source: ComposeSource(from: [Address(address: "x@example.invalid")], text: "hi"), self: me)
        #expect(r.to.count == 1)
        #expect(r.cc.isEmpty)
        #expect(r.inReplyTo == nil)
        #expect(r.bodyHTML.contains("<div>x@example.invalid wrote:</div>"))

        let n = prefill(kind: .new, source: src, self: me)
        #expect(n == ComposeParams(kind: .new))
    }

    /// Attribution is plain text for the backend to escape: names go in as
    /// they are, lines are joined with "\n", and a new message has none.
    @Test func attributionText() throws {
        let date: Date = try #require(RFC3339.parse("2026-09-02T14:03:00Z"))
        let src = ComposeSource(
            from: [Address(name: "<b>Alice</b>", address: "alice@example.invalid"), Address(address: "bob@example.invalid")],
            to: [Address(name: "Me", address: "me@example.invalid")],
            subject: "Hi & bye",
            date: date
        )
        let reply = attribution(kind: .reply, source: src)
        #expect(reply.hasPrefix("On "))
        #expect(reply.hasSuffix(", <b>Alice</b>, bob@example.invalid wrote:"))
        #expect(!reply.contains("\n"))

        #expect(attribution(kind: .replyAll, source: ComposeSource(from: Array(src.from.prefix(1)))) == "<b>Alice</b> wrote:")
        // Go's zero time is no date either.
        #expect(attribution(kind: .reply, source: ComposeSource(from: Array(src.from.prefix(1)), date: .goZero)) == "<b>Alice</b> wrote:")

        let lines = attribution(kind: .forward, source: src).components(separatedBy: "\n")
        #expect(lines.count == 5)
        #expect(lines[0] == "---------- Forwarded message ----------")
        #expect(lines[1].hasPrefix("From: "))
        #expect(lines[1].contains("alice@example.invalid"))
        #expect(lines[2].hasPrefix("Date: "))
        #expect(lines[3] == "Subject: Hi & bye")
        #expect(lines[4].hasPrefix("To: "))
        #expect(lines[4].contains("Me <me@example.invalid>"))

        let bare = attribution(kind: .forward, source: ComposeSource(subject: "x"))
        #expect(bare.components(separatedBy: "\n").count == 3)

        #expect(attribution(kind: .new, source: src) == "")

        // A To: line of hundreds of addresses is cut to the backend's cap.
        var many = ComposeSource(subject: "x")
        for _ in 0..<300 {
            many.to.append(Address(name: "Řehoř", address: "r@example.invalid"))
        }
        let long = attribution(kind: .forward, source: many)
        #expect(long.utf8.count <= API.Limits.maxDraftAttributionBytes)
        #expect(long.hasSuffix("\u{2026}"))
        // The cut never splits a multi-byte character.
        #expect(long.unicodeScalars.contains(where: { $0 == "\u{2026}" }))
    }

    @Test func kindMode() {
        let want: [ComposeKind: ComposeMode] = [.new: .new, .reply: .reply, .replyAll: .replyAll, .forward: .forward]
        for (kind, mode) in want {
            #expect(kind.mode == mode)
        }
        #expect(Set(ComposeKind.allCases) == Set(want.keys))
    }

    /// FromDraft carries the backend's template over as it is, and shows the
    /// text when there is no HTML.
    @Test func fromDraftParams() {
        let d = Draft(
            accountId: "acc_1", to: [Address(address: "a@example.invalid")], cc: [Address(address: "c@example.invalid")],
            subject: "Re: x", textBody: "> hi", htmlBody: "<p><br/></p><blockquote type=\"cite\">hi</blockquote>",
            inReplyTo: "m_1", attachments: [DraftAttachment(id: "att_1", filename: "a.png", contentType: "image/png", size: 1, inline: true, contentId: "c@malachi.local")]
        )
        let blocked = BlockedContent(remoteImages: 2)
        let p = fromDraft(kind: .reply, draft: d, blocked: blocked)
        #expect(p.kind == .reply)
        #expect(p.accountID == "acc_1")
        #expect(p.to.count == 1)
        #expect(p.cc.count == 1)
        #expect(p.bcc.isEmpty)
        #expect(p.subject == "Re: x")
        #expect(p.bodyHTML == d.htmlBody)
        #expect(p.inReplyTo == "m_1")
        #expect(p.forwarding == nil)
        #expect(p.attachments.count == 1)
        #expect(p.blocked == blocked)

        let plain = fromDraft(kind: .forward, draft: Draft(accountId: "", textBody: "a <b>\nc", forwarding: "m_2"), blocked: BlockedContent())
        #expect(plain.bodyHTML == "a &lt;b&gt;<br>c")
        #expect(plain.forwarding == "m_2")
        #expect(plain.accountID == nil)
    }

    @Test func escapeTextEscapesLikeHTMLEscapeString() {
        #expect(escapeText("a <b> & 'c' \"d\"\r\ne\nf") == "a &lt;b&gt; &amp; &#39;c&#39; &#34;d&#34;<br>e<br>f")
        #expect(escapeText("") == "")
    }

    @Test func dedupe() {
        let list = [
            Address(name: "A", address: "a@x.example"), Address(address: " A@X.example "), Address(address: ""),
            Address(address: "b@x.example"), Address(address: "c@x.example"),
        ]
        #expect(dedupeAddresses(list, exclude: [Address(address: "C@x.example")]).map(\.address) == ["a@x.example", "b@x.example"])
    }
}
