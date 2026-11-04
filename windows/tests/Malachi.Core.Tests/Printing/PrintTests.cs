// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/printing/printing_test.go and
// macos/Tests/MalachiCoreTests/PrintingTests.swift: the header rows leave
// the empty fields out, every string the application writes is escaped,
// a sanitised body goes in verbatim and a text body escaped in a <pre>,
// the three layouts, an event's and a day's sections, the texts.

using Malachi.Core.Printing;
using Xunit;

namespace Malachi.Core.Tests.Printing;

public sealed class PrintTests
{
    [Fact]
    public void HeaderRowsLeaveOutEmptyFields()
    {
        var got = Print.HeaderRows(new Print.Header
        {
            From = "Ann <ann@example.com>",
            To = "bob@example.com",
            Cc = "  ",
            Date = "Wed, 4 Nov 2026 at 9:41",
            Subject = "Hello",
            Attachments = ["a.pdf (41.1 kB)", "b.png (2 MB)"],
        });
        Assert.Equal(
            [
                new Print.Row("Subject", "Hello", true), new Print.Row("From", "Ann <ann@example.com>"), new Print.Row("To", "bob@example.com"),
                new Print.Row("Date", "Wed, 4 Nov 2026 at 9:41"), new Print.Row("Attachments", "a.pdf (41.1 kB), b.png (2 MB)"),
            ],
            got);
        Assert.Equal([new Print.Row("Bcc", "me@example.com")], Print.HeaderRows(new Print.Header { Bcc = "me@example.com" }));
    }

    [Fact]
    public void Escape()
    {
        Assert.Equal(
            "&lt;script&gt;alert(&#39;x&#39;)&lt;/script&gt; &amp; &quot;q&quot; ✦",
            Print.Escape("<script>alert('x')</script> & \"q\" ✦"));
    }

    [Fact]
    public void MessageEscapesTheHeaderAndKeepsTheSanitisedBody()
    {
        Print.Row[] rows = [new("Subject", "<b>x</b>", true), new("From", "\"Eve\" <eve@evil.example>")];
        var got = Print.Message(rows, new Print.Body(Html: "<p>Hi <img src=\"malachi-cid:1\"></p>"));
        Assert.DoesNotContain("<b>x</b>", got, System.StringComparison.Ordinal);
        Assert.DoesNotContain("<eve@", got, System.StringComparison.Ordinal);
        Assert.Contains("<p>Hi <img src=\"malachi-cid:1\"></p>", got, System.StringComparison.Ordinal);
        Assert.Contains(
            "<tr class=\"malachi-print-subject\"><th>Subject</th><td>&lt;b&gt;x&lt;/b&gt;</td></tr>", got, System.StringComparison.Ordinal);
    }

    [Fact]
    public void MessageTextBodyIsEscapedInPre()
    {
        Assert.Equal(
            "<table class=\"malachi-print-header\"></table><div class=\"malachi-print-body\">"
            + "<pre class=\"malachi-print-text\">a &lt; b\n&lt;/pre&gt;&lt;script&gt;</pre></div>",
            Print.Message(null, new Print.Body(Text: "a < b\n</pre><script>")));
    }

    [Fact]
    public void FragmentLayouts()
    {
        string[] secs = ["A", "B", "C"];
        var css = "<style>" + Print.Css + "</style>";
        Assert.Equal(css + "A<hr class=\"malachi-print-sep\">B<hr class=\"malachi-print-sep\">C", Print.Fragment(secs, Print.Layout.Conversation));
        Assert.Equal(
            css + "A<div class=\"malachi-print-page\"></div>B<div class=\"malachi-print-page\"></div>C", Print.Fragment(secs, Print.Layout.Pages));
        Assert.Equal(css + "A", Print.Fragment(secs[..1], Print.Layout.One));
    }

    [Fact]
    public void EventSection()
    {
        var got = Print.EventSection(new Print.PrintedEvent
        {
            Title = "Review <draft>",
            Lines = ["Wed 4 Nov, 9:00–10:00", "", "Room & Hall"],
            Attendees = ["Ann — Accepted"],
            Notes = "line 1\n<i>2</i>",
        });
        Assert.Equal(
            "<h1 class=\"malachi-print-title\">Review &lt;draft&gt;</h1>"
            + "<p class=\"malachi-print-line\">Wed 4 Nov, 9:00–10:00</p>"
            + "<p class=\"malachi-print-line\">Room &amp; Hall</p>"
            + "<p class=\"malachi-print-line\">Ann — Accepted</p>"
            + "<div class=\"malachi-print-notes\">line 1\n&lt;i&gt;2&lt;/i&gt;</div>",
            got);
    }

    [Fact]
    public void DaySection()
    {
        var got = Print.DaySection(
            "Wednesday 4 November", [new Print.DayItem("All Day", "Holiday"), new Print.DayItem("9:00–10:00", "Review", "<Room>")], "No events");
        Assert.Equal(
            "<h1 class=\"malachi-print-title\">Wednesday 4 November</h1><table class=\"malachi-print-day\">"
            + "<tr><td class=\"time\">All Day</td><td>Holiday</td></tr>"
            + "<tr><td class=\"time\">9:00–10:00</td><td>Review<br>&lt;Room&gt;</td></tr></table>",
            got);
        Assert.Equal(
            "<h1 class=\"malachi-print-title\">Today</h1><p class=\"malachi-print-line\">No events</p>", Print.DaySection("Today", null, "No events"));
    }

    [Fact]
    public void Texts()
    {
        Assert.Equal("_Print…", Print.MenuTitle);
        Assert.Equal("Print", Print.ItemLabel);
        Assert.Equal(50, Print.MaxMessages);
    }
}
