// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// ui/internal/htmlview/links_test.go.
struct HTMLLinksTests {
    @Test(arguments: [
        ("https://bank.example.org/", "https://evil.example.net/login", true),
        ("bank.example.org", "https://evil.example.net/", true),
        ("www.bank.example.org/account", "http://evil.example.net", true),
        ("https://example.org", "https://example.org/x", false),
        ("example.org", "https://www.example.org/", false),
        ("www.example.org", "https://example.org/", false),
        ("docs.example.org", "https://example.org/docs", false),
        ("example.org", "https://docs.example.org/", false),
        ("bank.example.org", "https://evil.example.org/", true),
        ("click here", "https://evil.example.net/", false),
        ("Read more", "https://example.org/", false),
        ("v1.2", "https://example.org/", false),
        ("e.g.", "https://example.org/", false),
        ("", "https://example.org/", false),
        ("https://example.org", "mailto:a@example.net", false),
        ("support@example.org", "https://evil.example.net/", false),
        // Port, userinfo, upper case and a malformed target.
        ("https://bank.example.org", "https://user:pw@EVIL.example.net:8443/x?y#z", true),
        ("https://bank.example.org:8443", "https://bank.example.org/", false),
        ("bank.example.org", "https://evil.example.net/%zz", false),
        ("bank.example.org", "https://[::1]/", true),
    ])
    func masked(_ text: String, _ href: String, _ want: Bool) {
        #expect(isMasked(text: text, href: href) == want)
    }

    @Test func allowedLinks() {
        for ok in ["https://x/", "HTTP://x", "mailto:a@b"] {
            #expect(allowedLink(ok), "\(ok) refused")
        }
        for bad in ["javascript:x", "ftp://x", "data:text/html,x", "", "malachi-cid:a/b/1", "cid:x", " https://x/"] {
            #expect(!allowedLink(bad), "\(bad) allowed")
        }
    }

    @Test func parsePath() throws {
        let parsed = try #require(parsePartPath("acc_1a2b/m_3c4d/1.2"))
        #expect(parsed.accountID == "acc_1a2b")
        #expect(parsed.messageID == "m_3c4d")
        #expect(parsed.partID == "1.2")
        for bad in ["", "a/b", "a/b/c/d", "/b/1", "a//1", "a/b/x", "a/b/1..2", "../etc/1", "a/b/" + String(repeating: "1", count: 200),
                    "a b/c/1", "a/b/1?x=1", String(repeating: "a", count: 129) + "/b/1", "a/b/.1", "a/b/1.", "\u{e1}/b/1"] {
            #expect(parsePartPath(bad) == nil, "ParsePath(\(bad)) accepted")
        }
        #expect(partScheme == "malachi-cid")
    }

    @Test func imageTypes() {
        for ok in ["image/png", "IMAGE/JPEG", "image/gif; charset=binary", " image/webp "] {
            #expect(isImageType(ok), "\(ok) refused")
        }
        for bad in ["image/svg+xml", "IMAGE/SVG+XML; charset=utf-8", "text/html", "application/pdf", "", "imagex/png"] {
            #expect(!isImageType(bad), "\(bad) accepted")
        }
    }

    @Test func document() {
        let doc = viewerDocument(body: "<p>a &amp; b</p>")
        for want in [viewerCSP, "<meta charset=\"utf-8\">", "<body><p>a &amp; b</p></body>", "img { max-width: 100%; }"] {
            #expect(doc.contains(want), "document lacks \(want)")
        }
        #expect(doc.hasPrefix("<!DOCTYPE html>"))
        #expect(doc.hasSuffix("</body></html>"))
        #expect(viewerCSP == "default-src 'none'; img-src malachi-cid: data:; style-src 'unsafe-inline'")
    }

    /// The decision over an activated link (remote.go `openLink`, plus
    /// the unlisted case macOS confirms): the attribute as written is
    /// what the daemon lists, so it must be matched, not WebKit's
    /// normalised URL.
    @Test func linkDecisions() {
        let masked = [Link(text: "https://bank.example", href: "https://evil.example")]
        let plain = [Link(text: "click here", href: "https://evil.example")]

        // The attribute lacks the slash WebKit adds: still matched by `raw`.
        let noSlash = ActivatedLink(raw: "https://evil.example", resolved: "https://evil.example/")
        #expect(noSlash.href == "https://evil.example")
        #expect(linkDecision(noSlash.href, masked) == .confirm(text: "https://bank.example", href: "https://evil.example"))
        #expect(linkDecision(noSlash.href, plain) == .open("https://evil.example"))

        // Only the navigation policy saw it: the resolved URL is unlisted.
        let policyOnly = ActivatedLink(raw: nil, resolved: "https://evil.example/")
        #expect(policyOnly.href == "https://evil.example/")
        #expect(linkDecision(policyOnly.href, masked) == .confirm(text: "", href: "https://evil.example/"))
        #expect(linkDecision(policyOnly.href, plain) == .confirm(text: "", href: "https://evil.example/"))

        // Upper-case host, which WebKit lower-cases.
        let upper = [Link(text: "https://bank.example", href: "https://EVIL.example/x")]
        let upperLink = ActivatedLink(raw: "https://EVIL.example/x", resolved: "https://evil.example/x")
        #expect(linkDecision(upperLink.href, upper) == .confirm(text: "https://bank.example", href: "https://EVIL.example/x"))
        #expect(linkDecision(upperLink.resolved, upper) == .confirm(text: "", href: "https://evil.example/x"))

        // An IDN host, which WebKit turns into punycode.
        let idn = [Link(text: "Bücher", href: "https://bücher.example/")]
        let idnLink = ActivatedLink(raw: "https://bücher.example/", resolved: "https://xn--bcher-kva.example/")
        #expect(linkDecision(idnLink.href, idn) == .open("https://bücher.example/"))
        #expect(linkDecision(idnLink.resolved, idn) == .confirm(text: "", href: "https://xn--bcher-kva.example/"))

        // Not on the list at all: confirmed, never opened silently.
        #expect(linkDecision("https://other.example/", masked) == .confirm(text: "", href: "https://other.example/"))
        #expect(linkDecision("https://other.example/", []) == .confirm(text: "", href: "https://other.example/"))

        // mailto: goes to the composer, listed or not; the rest is refused.
        #expect(linkDecision("mailto:a@example.org", []) == .mailto("mailto:a@example.org"))
        #expect(linkDecision("MAILTO:a@example.org", masked) == .mailto("MAILTO:a@example.org"))
        for bad in ["javascript:alert(1)", "ftp://x/", "", "malachi-cid:a/b/1", " https://evil.example"] {
            #expect(linkDecision(bad, masked) == .refused, "\(bad) not refused")
        }
    }

    @Test func hostsOfText() {
        #expect(hostOfText("HTTPS://Bank.Example.org/x") == "bank.example.org")
        #expect(hostOfText("bank.example.org") == "bank.example.org")
        #expect(hostOfText("http://") == "")
        #expect(hostOfText("a b.example.org") == "")
        #expect(hostOfText("example.o") == "")
        #expect(hostOfText("192.168.0.1") == "")
        #expect(looksLikeHost("a-b.example"))
        #expect(!looksLikeHost("example"))
        #expect(!looksLikeHost("a..example"))
        #expect(!looksLikeHost("a.example1"))
        // A label of dashes passes, as in Go: only the characters are checked.
        #expect(looksLikeHost("-.example"))
        #expect(sameSite("www.example.org", "example.org"))
        #expect(sameSite("a.b.example.org", "example.org"))
        #expect(!sameSite("notexample.org", "example.org"))
    }
}
