// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// ui/internal/bulkmail/bulkmail_test.go (English catalogue: the msgids come
// back verbatim). The Go po_test.go only checks the template against the
// msgids the package asks for; the macOS catalogues are generated from po/
// and the msgid coverage is `LocalizationTests`' business.

private func bulkDate(_ d: Date) -> String {
    var cal = Calendar(identifier: .gregorian)
    cal.timeZone = TimeZone(identifier: "UTC") ?? .gmt
    let c = cal.dateComponents([.year, .month, .day], from: d)
    return String(format: "%04d-%02d-%02d", c.year ?? 0, c.month ?? 0, c.day ?? 0)
}

/// bulkmail_test.go `msg`.
private func bulkMsg(_ b: BulkInfo?, _ o: UnsubscribeOffer?) -> Message {
    var s = MessageSummary(
        id: "m1", accountId: "a1", folderId: "f1", from: [], subject: "", date: .goZero, snippet: "", flags: [],
        hasAttachments: false, size: 0)
    s.bulk = b
    return Message(summary: s, unsubscribe: o)
}

private func bulkScalar(_ v: UInt32) -> String {
    String(UnicodeScalar(v) ?? " ")
}

@Suite struct BulkMailTests {
    @Test func tag() {
        let cases: [(BulkInfo?, String)] = [
            (nil, ""),
            (BulkInfo(kind: .newsletter), "Bulk"),
            (BulkInfo(kind: .list), "Mailing List"),
            (BulkInfo(kind: .automated), "Automated"),
            (BulkInfo(kind: "weird"), ""),
            (BulkInfo(kind: ""), ""),
        ]
        for (b, want) in cases {
            #expect(Bulk.tag(b) == want, "\(String(describing: b))")
        }
    }

    @Test func stripFor() {
        let at = Date(timeIntervalSince1970: 1_790_769_600) // 2026-09-30 12:00 UTC
        let news = BulkInfo(kind: .newsletter, listId: "news.shop.example", domain: "shop.example")
        let list = BulkInfo(kind: .list, listId: "golang-nuts.example", domain: "example.org")
        let listNoID = BulkInfo(kind: .list, domain: "example.org")
        let auto = BulkInfo(kind: .automated, domain: "bank.example")
        let one = UnsubscribeOffer(method: .oneClick, target: "shop.example")
        let mailto = UnsubscribeOffer(method: .mailto, target: "u@shop.example")
        let page = UnsubscribeOffer(method: .url, target: "shop.example", url: "https://shop.example/u")
        let done = UnsubscribeOffer(method: .oneClick, target: "shop.example", unsubscribedAt: at)
        let junkText = "Unsubscribing would confirm to the sender that your address exists."
        let cases: [(String, Message?, FolderRole, Bulk.Strip)] = [
            ("nil message", nil, .inbox, Bulk.Strip()),
            ("nil bulk", bulkMsg(nil, one), .inbox, Bulk.Strip()),
            ("unknown kind", bulkMsg(BulkInfo(kind: "x"), one), .inbox, Bulk.Strip()),
            ("junk newsletter", bulkMsg(news, one), .junk, Bulk.Strip(kind: .junk, text: junkText, warning: true)),
            ("junk list", bulkMsg(list, nil), .junk, Bulk.Strip(kind: .junk, text: junkText, warning: true)),
            ("junk role string", bulkMsg(news, one), "junk", Bulk.Strip(kind: .junk, text: junkText, warning: true)),
            ("junk automated", bulkMsg(auto, nil), .junk, Bulk.Strip(kind: .automated, text: "Automated message")),
            ("automated", bulkMsg(auto, one), .inbox, Bulk.Strip(kind: .automated, text: "Automated message")),
            ("junk-like role", bulkMsg(news, one), "junkish",
             Bulk.Strip(kind: .newsletter, text: "Bulk message from shop.example", action: "_Unsubscribe")),
            ("unsubscribed", bulkMsg(news, done), .inbox, Bulk.Strip(kind: .unsubscribed, text: "Unsubscribed on 2026-09-30")),
            ("unsubscribed list", bulkMsg(list, done), .inbox, Bulk.Strip(kind: .unsubscribed, text: "Unsubscribed on 2026-09-30")),
            ("newsletter one click", bulkMsg(news, one), .inbox,
             Bulk.Strip(kind: .newsletter, text: "Bulk message from shop.example", action: "_Unsubscribe")),
            ("newsletter mailto", bulkMsg(news, mailto), .inbox,
             Bulk.Strip(kind: .newsletter, text: "Bulk message from shop.example", action: "_Unsubscribe")),
            ("newsletter url", bulkMsg(news, page), .inbox,
             Bulk.Strip(kind: .newsletter, text: "Bulk message from shop.example", action: "_Unsubscribe…")),
            ("newsletter no offer", bulkMsg(news, nil), .inbox,
             Bulk.Strip(kind: .newsletter, text: "Bulk message from shop.example")),
            ("newsletter no domain", bulkMsg(BulkInfo(kind: .newsletter, listId: "l.example"), nil), .inbox,
             Bulk.Strip(kind: .newsletter, text: "Bulk message from l.example")),
            ("list mailto", bulkMsg(list, mailto), .inbox,
             Bulk.Strip(kind: .list, text: "Message from mailing list golang-nuts.example", action: "_Leave List")),
            ("list url", bulkMsg(list, page), .inbox,
             Bulk.Strip(kind: .list, text: "Message from mailing list golang-nuts.example", action: "_Leave List…")),
            ("list without list id falls back to domain", bulkMsg(listNoID, one), .inbox,
             Bulk.Strip(kind: .list, text: "Message from mailing list example.org", action: "_Leave List")),
            ("list no offer", bulkMsg(list, nil), .inbox,
             Bulk.Strip(kind: .list, text: "Message from mailing list golang-nuts.example")),
        ]
        for (name, m, role, want) in cases {
            let got = Bulk.stripFor(m, role: role, date: bulkDate)
            #expect(got == want, "\(name): got \(got), want \(want)")
            #expect(got.visible == (want.kind != .none), "\(name): visible")
        }
        // A nil date formatter must not crash.
        _ = Bulk.stripFor(bulkMsg(news, done), role: .inbox, date: nil)
    }

    @Test func confirm() {
        let news = BulkInfo(kind: .newsletter, domain: "shop.example")
        let list = BulkInfo(kind: .list, listId: "l.example", domain: "example.org")
        let one = UnsubscribeOffer(method: .oneClick, target: "shop.example")
        let mailto = UnsubscribeOffer(method: .mailto, target: "u@shop.example")
        let page = UnsubscribeOffer(method: .url, target: "shop.example", url: "https://shop.example/u?x=1")
        let oneBody = "Malachi Mail will ask shop.example to stop sending these messages. The sender may still send a few more over the next days."
        let mailBody = "Malachi Mail will send an unsubscribe request to u@shop.example from your account. It will appear in Sent."
        let cases: [(String, Message?, Bulk.Confirmation?)] = [
            ("nil", nil, nil),
            ("no offer", bulkMsg(news, nil), nil),
            ("unknown method", bulkMsg(news, UnsubscribeOffer(method: "x", target: "")), nil),
            ("one click newsletter", bulkMsg(news, one),
             Bulk.Confirmation(heading: "Unsubscribe from shop.example?", body: oneBody, confirm: "_Unsubscribe")),
            ("one click list", bulkMsg(list, one),
             Bulk.Confirmation(heading: "Unsubscribe from l.example?", body: oneBody, confirm: "_Unsubscribe")),
            ("one click nil bulk", bulkMsg(nil, one),
             Bulk.Confirmation(heading: "Unsubscribe from shop.example?", body: oneBody, confirm: "_Unsubscribe")),
            ("mailto newsletter", bulkMsg(news, mailto),
             Bulk.Confirmation(heading: "Unsubscribe from shop.example?", body: mailBody, confirm: "_Send Request")),
            ("mailto list", bulkMsg(list, mailto),
             Bulk.Confirmation(heading: "Leave the mailing list l.example?", body: mailBody, confirm: "_Send Request")),
            ("url", bulkMsg(news, page),
             Bulk.Confirmation(
                heading: "Open the unsubscribe page?",
                body: "The sender does not offer unsubscribing in one step. This page opens in your browser:\nhttps://shop.example/u?x=1",
                confirm: "_Open in Browser")),
        ]
        for (name, m, want) in cases {
            #expect(Bulk.confirm(m) == want, "\(name)")
        }
    }

    @Test func fallback() {
        var m = bulkMsg(
            BulkInfo(kind: .newsletter, domain: "shop.example"), UnsubscribeOffer(method: .oneClick, target: "t.example"))
        let res = MessageUnsubscribeResult(outcome: .openUrl, url: "https://shop.example/u", unverified: true)
        let want = Bulk.Confirmation(
            heading: "The sender could not be verified",
            body: "Malachi Mail sent nothing because the message is not signed by shop.example. You can unsubscribe on the sender's page instead:\nhttps://shop.example/u",
            confirm: "_Open in Browser")
        #expect(Bulk.fallback(m, res) == want)
        // Without a domain the offer's target stands in; a nil message is safe.
        m.summary.bulk = nil
        #expect(Bulk.fallback(m, res).body.contains("signed by t.example."))
        #expect(!Bulk.fallback(nil, res).heading.isEmpty)
    }

    @Test func applied() {
        let at = Date(timeIntervalSince1970: 1_790_816_523)
        let offer = UnsubscribeOffer(method: .mailto, target: "u@x.example")
        #expect(Bulk.applied(nil, MessageUnsubscribeResult(outcome: .unsubscribed)) == nil, "a nil offer stays nil")
        for o in [UnsubscribeOutcome.unsubscribed, .queued] {
            let got = Bulk.applied(offer, MessageUnsubscribeResult(outcome: o, unsubscribedAt: at))
            #expect(got?.unsubscribedAt == at, "\(o)")
            #expect(got?.method == offer.method && got?.target == offer.target, "\(o): fields lost")
            #expect(offer.unsubscribedAt == nil, "the original offer was modified")
        }
        // A result without a time still marks the offer as done.
        #expect(Bulk.applied(offer, MessageUnsubscribeResult(outcome: .queued))?.unsubscribedAt != nil)
        #expect(Bulk.applied(offer, MessageUnsubscribeResult(outcome: .openUrl, unsubscribedAt: at))?.unsubscribedAt == nil,
                "openUrl must not mark the offer")
    }

    @Test func texts() {
        #expect(Bulk.queued() == "Unsubscribe request queued")
        #expect(Bulk.errorWhat() == "Unsubscribing")
        #expect(Bulk.refused() == "The sender's server refused the request.")
        // rpc.go: 1505 is the refused sentence whatever the action is.
        #expect(rpcErrorText(Bulk.errorWhat(), RPCError(code: .unsubscribeFailed, message: "status 500"))
            == "The sender's server refused the request.")
        #expect(rpcErrorText(Bulk.errorWhat(), RPCClient.ClientError.timeout(method: "message.unsubscribe"))
            == "Unsubscribing timed out")
    }

    /// window/bulk.go `wantsOffer`: a card of a newsletter or list message
    /// asks for message.get, whose offer the strip needs.
    @Test func wantsOffer() {
        func summary(_ b: BulkInfo?) -> MessageSummary { bulkMsg(b, nil).summary }
        #expect(Bulk.wantsOffer(summary(BulkInfo(kind: .newsletter))))
        #expect(Bulk.wantsOffer(summary(BulkInfo(kind: .list))))
        #expect(!Bulk.wantsOffer(summary(BulkInfo(kind: .automated))))
        #expect(!Bulk.wantsOffer(summary(BulkInfo(kind: "x"))))
        #expect(!Bulk.wantsOffer(summary(nil)))
    }

    @Test func openableURL() {
        let cases: [(String, Bool)] = [
            ("https://shop.example/u?x=1", true),
            ("HTTPS://shop.example/", true),
            ("http://shop.example/", false),
            ("javascript:alert(1)", false),
            ("data:text/html,x", false),
            ("https:///path", false),
            ("/relative", false),
            ("", false),
            ("https://shop.example/a b", false),
            ("https://shop.example/" + bulkScalar(0), false),
            ("https://shop.example/" + bulkScalar(0x202E), false),
            ("https://" + String(repeating: "a", count: 3000), false),
        ]
        for (raw, ok) in cases {
            #expect((Bulk.openableURL(raw) != nil) == ok, "\(raw.prefix(40))")
            if ok { #expect(Bulk.openableURL(raw) == raw) }
        }
    }

    /// Hostile strings are shown as plain text by the view; here they must
    /// only pass through without a crash or a rewrite.
    @Test func hostileStrings() {
        let huge = String(repeating: "a", count: 1 << 20)
        let bidi = bulkScalar(0x202E) + "evil" + bulkScalar(0x2066) + ".example" + bulkScalar(0) + "%s%d<b>"
        for s in [huge, bidi, "", "%", "%!s(MISSING)"] {
            let b = BulkInfo(kind: .list, listId: s, domain: s)
            let o = UnsubscribeOffer(method: .url, target: s, url: s)
            let m = bulkMsg(b, o)
            let st = Bulk.stripFor(m, role: .inbox, date: bulkDate)
            #expect(s.isEmpty || st.text.contains(s), "list id was rewritten for \(s.prefix(20))")
            _ = Bulk.confirm(m)
            _ = Bulk.fallback(m, MessageUnsubscribeResult(outcome: .openUrl, url: s))
            _ = Bulk.tag(b)
        }
    }
}
