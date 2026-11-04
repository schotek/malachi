// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// ui/internal/printing printing_test.go, ported.
struct PrintingTests {
    @Test func headerRowsLeaveOutEmptyFields() {
        var h = Printing.Header()
        h.from = "Ann <ann@example.com>"
        h.to = "bob@example.com"
        h.cc = "  "
        h.date = "Wed, 4 Nov 2026 at 9:41"
        h.subject = "Hello"
        h.attachments = ["a.pdf (41.1 kB)", "b.png (2 MB)"]
        #expect(Printing.headerRows(h) == [
            Printing.Row("Subject", "Hello", strong: true), Printing.Row("From", "Ann <ann@example.com>"),
            Printing.Row("To", "bob@example.com"), Printing.Row("Date", "Wed, 4 Nov 2026 at 9:41"),
            Printing.Row("Attachments", "a.pdf (41.1 kB), b.png (2 MB)"),
        ])
        var bcc = Printing.Header()
        bcc.bcc = "me@example.com"
        #expect(Printing.headerRows(bcc) == [Printing.Row("Bcc", "me@example.com")])
    }

    @Test func escape() {
        #expect(Printing.escape("<script>alert('x')</script> & \"q\" ✦")
            == "&lt;script&gt;alert(&#39;x&#39;)&lt;/script&gt; &amp; &quot;q&quot; ✦")
    }

    @Test func messageEscapesTheHeaderAndKeepsTheSanitisedBody() {
        let got = Printing.message(
            [Printing.Row("Subject", "<b>x</b>", strong: true), Printing.Row("From", "\"Eve\" <eve@evil.example>")],
            Printing.Body(html: "<p>Hi <img src=\"malachi-cid:1\"></p>"))
        #expect(!got.contains("<b>x</b>") && !got.contains("<eve@"))
        #expect(got.contains("<p>Hi <img src=\"malachi-cid:1\"></p>"))
        #expect(got.contains("<tr class=\"malachi-print-subject\"><th>Subject</th><td>&lt;b&gt;x&lt;/b&gt;</td></tr>"))
    }

    @Test func messageTextBodyIsEscapedInPre() {
        #expect(Printing.message([], Printing.Body(text: "a < b\n</pre><script>"))
            == "<table class=\"malachi-print-header\"></table><div class=\"malachi-print-body\">"
            + "<pre class=\"malachi-print-text\">a &lt; b\n&lt;/pre&gt;&lt;script&gt;</pre></div>")
    }

    @Test func fragmentLayouts() {
        let css = "<style>" + Printing.css + "</style>"
        #expect(Printing.fragment(["A", "B", "C"], .conversation)
            == css + "A<hr class=\"malachi-print-sep\">B<hr class=\"malachi-print-sep\">C")
        #expect(Printing.fragment(["A", "B", "C"], .pages)
            == css + "A<div class=\"malachi-print-page\"></div>B<div class=\"malachi-print-page\"></div>C")
        #expect(Printing.fragment(["A"], .single) == css + "A")
    }

    @Test func eventSection() {
        var e = Printing.Event()
        e.title = "Review <draft>"
        e.lines = ["Wed 4 Nov, 9:00–10:00", "", "Room & Hall"]
        e.attendees = ["Ann — Accepted"]
        e.notes = "line 1\n<i>2</i>"
        #expect(Printing.eventSection(e)
            == "<h1 class=\"malachi-print-title\">Review &lt;draft&gt;</h1>"
            + "<p class=\"malachi-print-line\">Wed 4 Nov, 9:00–10:00</p>"
            + "<p class=\"malachi-print-line\">Room &amp; Hall</p>"
            + "<p class=\"malachi-print-line\">Ann — Accepted</p>"
            + "<div class=\"malachi-print-notes\">line 1\n&lt;i&gt;2&lt;/i&gt;</div>")
    }

    @Test func daySection() {
        #expect(Printing.daySection("Wednesday 4 November", [
            Printing.DayItem(time: "All Day", title: "Holiday"),
            Printing.DayItem(time: "9:00–10:00", title: "Review", location: "<Room>"),
        ], none: "No events")
            == "<h1 class=\"malachi-print-title\">Wednesday 4 November</h1><table class=\"malachi-print-day\">"
            + "<tr><td class=\"time\">All Day</td><td>Holiday</td></tr>"
            + "<tr><td class=\"time\">9:00–10:00</td><td>Review<br>&lt;Room&gt;</td></tr></table>")
        #expect(Printing.daySection("Today", [], none: "No events")
            == "<h1 class=\"malachi-print-title\">Today</h1><p class=\"malachi-print-line\">No events</p>")
    }
}
