// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The counterpart of ui/internal/assistant/markdown_test.go: the Markdown
// subset of the panel's answers. A Swift string holds no invalid UTF-8, so
// Go's cases of it are left out (U+FFFD stands in for them in the
// linearity inputs).

private typealias B = Assistant.Block
private typealias S = Assistant.Span

/// plain: one unstyled span.
private func plain(_ s: String) -> [S] { [S(text: s)] }

/// para: a paragraph of one unstyled span.
private func para(_ s: String) -> B { B(kind: .paragraph, spans: plain(s)) }

@Suite struct AssistantMarkdownTests {
    @Test func blocks() {
        let cases: [(String, String, [B])] = [
            ("empty", "", []),
            ("only blank lines", "\n  \n\t\n", []),
            ("a paragraph", "Hello world", [para("Hello world")]),
            ("lines stay lines, blank lines split", "a\n  b  \n\n\nc", [para("a\nb"), para("c")]),
            ("line endings", "a\r\nb\rc\n\r\nd", [para("a\nb\nc"), para("d")]),
            ("headings", "# One\n## Two\n### Three", [
                B(kind: .heading, level: 1, spans: plain("One")),
                B(kind: .heading, level: 2, spans: plain("Two")),
                B(kind: .heading, level: 3, spans: plain("Three")),
            ]),
            ("what is no heading", "#### Four\n#NoSpace\n# \n    # indented four", [para("#### Four\n#NoSpace\n#\n# indented four")]),
            ("a heading indented three spaces, with a tab", "   ##\tTitle  ", [B(kind: .heading, level: 2, spans: plain("Title"))]),
            ("a heading ends a paragraph", "text\n# Head\nmore",
             [para("text"), B(kind: .heading, level: 1, spans: plain("Head")), para("more")]),
            ("bullets", "- one\n* two", [
                B(kind: .bullet, spans: plain("one")),
                B(kind: .bullet, spans: plain("two")),
            ]),
            ("numbered", "1. First\n2. Second\n10. Tenth\n007. Bond", [
                B(kind: .numbered, number: 1, spans: plain("First")),
                B(kind: .numbered, number: 2, spans: plain("Second")),
                B(kind: .numbered, number: 10, spans: plain("Tenth")),
                B(kind: .numbered, number: 7, spans: plain("Bond")),
            ]),
            ("nesting", "- a\n  - b\n    - c\n  - d\n- e\n\t- f", [
                B(kind: .bullet, level: 0, spans: plain("a")),
                B(kind: .bullet, level: 1, spans: plain("b")),
                B(kind: .bullet, level: 2, spans: plain("c")),
                B(kind: .bullet, level: 1, spans: plain("d")),
                B(kind: .bullet, level: 0, spans: plain("e")),
                B(kind: .bullet, level: 1, spans: plain("f")),
            ]),
            ("nested under a numbered item by three or four spaces", "1. a\n   - b\n2. c\n    - d", [
                B(kind: .numbered, number: 1, spans: plain("a")),
                B(kind: .bullet, level: 1, spans: plain("b")),
                B(kind: .numbered, number: 2, spans: plain("c")),
                B(kind: .bullet, level: 1, spans: plain("d")),
            ]),
            ("nesting stops at the limit", "- 0\n - 1\n  - 2\n   - 3\n    - 4\n     - 5\n - back", [
                B(kind: .bullet, level: 0, spans: plain("0")),
                B(kind: .bullet, level: 1, spans: plain("1")),
                B(kind: .bullet, level: 2, spans: plain("2")),
                B(kind: .bullet, level: 3, spans: plain("3")),
                B(kind: .bullet, level: 3, spans: plain("4")),
                B(kind: .bullet, level: 3, spans: plain("5")),
                B(kind: .bullet, level: 1, spans: plain("back")),
            ]),
            ("a list survives a blank line", "- a\n\n  - b", [
                B(kind: .bullet, level: 0, spans: plain("a")),
                B(kind: .bullet, level: 1, spans: plain("b")),
            ]),
            ("a paragraph starts the list afresh", "- a\n\ntext\n\n  - b", [
                B(kind: .bullet, level: 0, spans: plain("a")),
                para("text"),
                B(kind: .bullet, level: 0, spans: plain("b")),
            ]),
            ("an item continues", "- item\ncontinued\n  more", [B(kind: .bullet, spans: plain("item\ncontinued\nmore"))]),
            ("an item ends a paragraph", "text\n- item", [para("text"), B(kind: .bullet, spans: plain("item"))]),
            ("markers without text, and what is no marker",
             "- \n* \n1. \n-no\n1.5 million\n1234567890. ten digits\n+ plus\n1) paren",
             [para("-\n*\n1.\n-no\n1.5 million\n1234567890. ten digits\n+ plus\n1) paren")]),
            ("a code block", "```go\nfunc main() {\n\t**not bold** <b>\n}\n```\nafter", [
                B(kind: .code, spans: [S(text: "func main() {\n\t**not bold** <b>\n}", code: true)]),
                para("after"),
            ]),
            ("a code block keeps blank lines and indentation", "```\n  a\n\n  b\n```", [
                B(kind: .code, spans: [S(text: "  a\n\n  b", code: true)]),
            ]),
            ("a fence ends a paragraph", "para\n```\nx\n```", [para("para"), B(kind: .code, spans: [S(text: "x", code: true)])]),
            ("an unclosed fence runs to the end", "text\n```\ncode *x*\n# not a heading", [
                para("text"),
                B(kind: .code, spans: [S(text: "code *x*\n# not a heading", code: true)]),
            ]),
            ("an empty code block", "```\n```", [B(kind: .code, spans: [])]),
            ("a fence indented four spaces is text", "    ```\n    x", [para("```\nx")]),
        ]
        for (name, input, want) in cases {
            #expect(Assistant.markdown(input) == want, "\(name)")
        }
    }

    @Test func inline() {
        let cases: [(String, String, [S])] = [
            ("bold", "a **b** c", [S(text: "a "), S(text: "b", bold: true), S(text: " c")]),
            ("italic", "*it* and _it_", [S(text: "it", italic: true), S(text: " and "), S(text: "it", italic: true)]),
            ("snake_case stays", "create_draft and read_message_x", plain("create_draft and read_message_x")),
            ("an underscore closes at a word's edge only", "_a_b c_", [S(text: "a_b c", italic: true)]),
            ("code", "use `a **b** [x](https://x.org)` here",
             [S(text: "use "), S(text: "a **b** [x](https://x.org)", code: true), S(text: " here")]),
            ("bold around italic and code", "**bold *it* `c`**", [
                S(text: "bold ", bold: true), S(text: "it", bold: true, italic: true), S(text: " ", bold: true),
                S(text: "c", bold: true, code: true),
            ]),
            ("italic around bold", "*it **b** x*",
             [S(text: "it ", italic: true), S(text: "b", bold: true, italic: true), S(text: " x", italic: true)]),
            ("no bold in bold", "**a **b** c**", [S(text: "a **b", bold: true), S(text: " c**")]),
            ("no italic in italic", "*a _b_ c*", [S(text: "a _b_ c", italic: true)]),
            ("bold over lines", "**one\ntwo**", [S(text: "one\ntwo", bold: true)]),
            ("adjacent code spans merge", "`a``b`", [S(text: "ab", code: true)]),
            ("an empty code span is text", "`` x", plain("`` x")),
            ("unclosed bold", "**open", plain("**open")),
            ("unclosed italic", "*open and _open", plain("*open and _open")),
            ("unclosed code", "`open", plain("`open")),
            ("a spaced opener", "** spaced** and * spaced*", plain("** spaced** and * spaced*")),
            ("a spaced closer", "**a **", plain("**a **")),
            ("arithmetic", "2 * 3 * 4 and a * b", plain("2 * 3 * 4 and a * b")),
            ("empty markers", "**** and ** and __", plain("**** and ** and __")),
            ("a link", "see [Malachi](https://github.com/schotek/malachi).", [
                S(text: "see "), S(text: "Malachi", link: "https://github.com/schotek/malachi"), S(text: "."),
            ]),
            ("a link in bold", "**[a](http://x.org)**", [S(text: "a", bold: true, link: "http://x.org")]),
            ("an upper-case scheme", "[a](HTTPS://Example.org/P?q=1#f)", [S(text: "a", link: "HTTPS://Example.org/P?q=1#f")]),
            ("link text is literal", "[**a** `b`](https://x.org)", [S(text: "**a** `b`", link: "https://x.org")]),
            ("javascript stays text", "[click](javascript:alert(1))", plain("[click](javascript:alert(1))")),
            ("file stays text", "[x](file:///etc/passwd)", plain("[x](file:///etc/passwd)")),
            ("mailto stays text", "[x](mailto:a@b.cz)", plain("[x](mailto:a@b.cz)")),
            ("data stays text", "[x](data:text/html,<b>hi</b>)", plain("[x](data:text/html,<b>hi</b>)")),
            ("scheme-relative stays text", "[x](//evil.org)", plain("[x](//evil.org)")),
            ("no host stays text", "[x](http://) [y](https:///path)", plain("[x](http://) [y](https:///path)")),
            // Not links; the bare URL in them is, up to the space or quote.
            ("a space in the URL", "[x](https://exa mple.org)",
             [S(text: "[x]("), S(text: "https://exa", link: "https://exa"), S(text: " mple.org)")]),
            ("a quote in the URL", #"[x](https://x.org/"onmouseover=)"#,
             [S(text: "[x]("), S(text: "https://x.org/", link: "https://x.org/"), S(text: #""onmouseover=)"#)]),
            ("no link text", "[](https://x.org)", [S(text: "[]("), S(text: "https://x.org", link: "https://x.org"), S(text: ")")]),
            ("link text over lines", "[a\nb](https://x.org)",
             [S(text: "[a\nb]("), S(text: "https://x.org", link: "https://x.org"), S(text: ")")]),
            ("an unclosed link", "[text](https://x.org and [more]",
             [S(text: "[text]("), S(text: "https://x.org", link: "https://x.org"), S(text: " and [more]")]),
            ("brackets without a link", "[1] and [a] (b)", plain("[1] and [a] (b)")),
            ("bare URLs", "see https://example.org/a_b_(c), and http://x.cz.", [
                S(text: "see "), S(text: "https://example.org/a_b_(c)", link: "https://example.org/a_b_(c)"),
                S(text: ", and "), S(text: "http://x.cz", link: "http://x.cz"), S(text: "."),
            ]),
            ("a bare URL in parentheses", "(see https://x.org/y)",
             [S(text: "(see "), S(text: "https://x.org/y", link: "https://x.org/y"), S(text: ")")]),
            ("a bare URL ends at a bracket or quote", #""https://x.org/a"<"#,
             [S(text: "\""), S(text: "https://x.org/a", link: "https://x.org/a"), S(text: "\"<")]),
            ("a bare URL in bold", "**https://x.org**", [S(text: "https://x.org", bold: true, link: "https://x.org")]),
            ("no bare URL inside a word", "xhttps://x.org and 1http://y.org", plain("xhttps://x.org and 1http://y.org")),
            ("a scheme alone", "https:// and http://.", plain("https:// and http://.")),
            ("other schemes are text", "javascript:alert(1) ftp://x.org www.x.org", plain("javascript:alert(1) ftp://x.org www.x.org")),
            ("no HTML", "<b>bold</b> <script>alert(1)</script> &amp; <a href=\"https://x.org\">x</a>", [
                S(text: "<b>bold</b> <script>alert(1)</script> &amp; <a href=\""), S(text: "https://x.org", link: "https://x.org"),
                S(text: "\">x</a>"),
            ]),
            ("control characters", "a\u{0}b\u{1B}c\u{7F}d\u{85}e\tf", plain("abcde\tf")),
            ("Czech", "Příliš **žluťoučký** kůň", [S(text: "Příliš "), S(text: "žluťoučký", bold: true), S(text: " kůň")]),
            ("underscores at Czech word edges", "_čau_ a x_č_y", [S(text: "čau", italic: true), S(text: " a x_č_y")]),
            // Swift only: url.Parse's corners, as Go reads them.
            ("a port after the last colon", "http://a:1:2/x", [S(text: "http://a:1:2/x", link: "http://a:1:2/x")]),
            ("a bad port", "http://x.org:8o/", plain("http://x.org:8o/")),
            ("an IPv6 literal", "[v6](http://[::1]:80/) [zone](http://[fe80::1%25en0]/) [v4](http://[1.2.3.4]/)", [
                S(text: "v6", link: "http://[::1]:80/"), S(text: " "), S(text: "zone", link: "http://[fe80::1%25en0]/"),
                S(text: " [v4](http://[1.2.3.4]/)"),
            ]),
            ("a bad escape in the path or the fragment", "[a](https://x.org/%zz) [b](https://x.org/#%4) [c](https://x.org/?%zz)",
             [S(text: "[a](https://x.org/%zz) [b](https://x.org/#%4) "), S(text: "c", link: "https://x.org/?%zz")]),
            ("an escape in the host", "[a](http://%41.org) [b](http://%c3%a9.org)",
             [S(text: "[a](http://%41.org) "), S(text: "b", link: "http://%c3%a9.org")]),
            ("userinfo", "[a](http://u:p@x.org) [b](http://u{s@x.org)",
             [S(text: "a", link: "http://u:p@x.org"), S(text: " [b]("), S(text: "http://u", link: "http://u"), S(text: "{s@x.org)")]),
        ]
        for (name, input, want) in cases {
            #expect(Assistant.markdown(input) == [B(kind: .paragraph, spans: want)], "\(name)")
        }
    }

    /// Whatever the input, a span's link is an http or https URL.
    @Test func linksAreWebURLs() {
        let inputs = [
            "[a](javascript:x) [b](https://ok.org) https://ok2.org/x) javascript://x.org",
            #"[x](https://a.org\@evil.org) http://a.org\b [y](http://a.org`b)"#,
            "[x](https://\u{2028}evil.org) https://x.org\u{A0}tail",
        ]
        for input in inputs {
            for b in Assistant.markdown(input) {
                for s in b.spans where !s.link.isEmpty {
                    #expect(Assistant.isWebURL(s.link), "\(input): link \(s.link)")
                    #expect(!s.link.unicodeScalars.contains { " \\`\"<>".unicodeScalars.contains($0) }, "\(input): link \(s.link)")
                }
            }
        }
    }

    /// A target with a "(" of its own is no link target; the bare URL in
    /// it is found instead, with its balanced parentheses (markdown_test.go
    /// TestMarkdownLinkTargetWithParens).
    @Test func linkTargetWithParens() {
        #expect(Assistant.markdown("[x](http://h.org/Foo_(bar)) end") == [B(kind: .paragraph, spans: [
            S(text: "[x]("), S(text: "http://h.org/Foo_(bar)", link: "http://h.org/Foo_(bar)"), S(text: ") end"),
        ])])
        #expect(Assistant.markdown("[x](http://h.org/a) end") == [B(kind: .paragraph, spans: [
            S(text: "x", link: "http://h.org/a"), S(text: " end"),
        ])])
    }

    /// A megabyte of every pathological shape costs what a megabyte of
    /// letters does, and nothing is lost but markers.
    @Test func isLinear() {
        let mb = 1 << 20
        var nested = ""
        var i = 0
        while nested.utf8.count < mb {
            nested += String(repeating: " ", count: i % 400) + "- x\n"
            i += 1
        }
        var inputs: [(String, String)] = [
            ("asterisks", String(repeating: "*", count: mb)),
            ("asterisk pairs", String(repeating: "**a", count: mb / 3)),
            ("stars and spaces", String(repeating: "* ", count: mb / 2)),
            ("underscores", String(repeating: "_a", count: mb / 2)),
            ("backticks", String(repeating: "`", count: mb)),
            ("code spans", String(repeating: "`a`", count: mb / 3)),
            ("open brackets", String(repeating: "[", count: mb)),
            ("bracket pairs", String(repeating: "[]", count: mb / 2)),
            ("link openers", String(repeating: "[a](", count: mb / 4)),
            ("bad links", String(repeating: "[a", count: mb / 4) + "](javascript:" + String(repeating: "x", count: mb / 2) + ")"),
            ("one link, many [", String(repeating: "[", count: mb / 2) + "a](https://x.org)"),
            ("schemes", String(repeating: "http://", count: mb / 7)),
            ("schemes and dots", String(repeating: "http://.", count: mb / 8)),
            ("a long URL", "https://x.org/" + String(repeating: "a", count: mb)),
            ("hashes", String(repeating: "#", count: mb)),
            ("digits", String(repeating: "1", count: mb) + ". x"),
            ("nested lists", nested),
            ("fences", String(repeating: "```\n", count: mb / 4)),
            ("lines", String(repeating: "a\n", count: mb / 2)),
            ("mixed", String(repeating: "**_`[*h](", count: mb / 9)),
            ("control bytes", String(repeating: "\u{0}\r", count: mb / 2)),
            ("replacement characters", String(repeating: "\u{FFFD}", count: mb / 3)),
            ("bold over a block", "**" + String(repeating: "a *b* _c_ `d` ", count: mb / 14) + "**"),
        ]
        // Swift only: link targets that share one closing parenthesis. The
        // Go scanner reads each of them to the end again (quadratic: 360 KB
        // take it about 20 s); the port reads them through cursors.
        inputs += [
            ("link targets to one parenthesis", String(repeating: "[a](http://", count: mb / 11) + ")"),
            ("link targets with a query and a fragment", String(repeating: "[a](http:///", count: mb / 12) + "?%#y)"),
            ("link targets with bad escapes", String(repeating: "[a](http://x/%", count: mb / 14) + ")"),
            ("link targets with at signs", String(repeating: "[a](http://u@", count: mb / 13) + ")"),
        ]
        for (name, input) in inputs {
            let start = ContinuousClock.now
            let blocks = Assistant.markdown(input)
            let took = ContinuousClock.now - start
            #expect(took < .seconds(3), "\(name): \(input.utf8.count) bytes took \(took)")
            let n = blocks.reduce(0) { $0 + $1.spans.reduce(0) { $0 + $1.text.utf8.count } }
            #expect(n <= 3 * input.utf8.count, "\(name): \(n) bytes of spans from \(input.utf8.count) bytes")
        }
    }
}
