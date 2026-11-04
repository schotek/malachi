// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Printing/Printing.swift; GTK:
// ui/internal/printing/printing.go (MaxMessages, MenuTitle, ItemLabel,
// Header, Row, HeaderRows, Escape, Body, CSS, Message, Layout, Fragment,
// Event, EventSection, DayItem, DaySection).
//
// The document the client prints (docs/plans/toolbar-customization.md §8,
// decisions P1–P8): a message, a conversation, several messages, a
// calendar event or a calendar day, as the HTML fragment the locked
// message view loads in place of a message body. The application writes
// and escapes the header rows and the event's and day's lines here
// (Escape); the message bodies are the sanitiser's output and go in
// verbatim, a body without HTML (or withheld) as its plain text, escaped,
// in a <pre>. The fragment goes through the same document and policy as a
// message on screen (no script, no network, malachi-cid: and data:
// pictures only); nothing here fetches anything. Dates, addresses and
// sizes come formatted by the caller (PrintComposer).
//
// Windows: Go's Event is PrintedEvent (Event is a keyword of Visual Basic,
// CA1716), Go's Single layout is One (CA1720: a type's name), and the
// class is Print in the namespace Printing.

using System;
using System.Collections.Generic;
using System.Text;
using Malachi.Core.I18n;

namespace Malachi.Core.Printing;

/// <summary>The print document (ui/internal/printing, macOS <c>Printing</c>).</summary>
public static class Print
{
    /// <summary>
    /// printing.go <c>MaxMessages</c>: the most messages one print takes (a
    /// selection or a conversation); the client offers Print for no more.
    /// </summary>
    public const int MaxMessages = 50;

    /// <summary>printing.go <c>MenuTitle</c>: Print in a menu, with its mnemonic.</summary>
    public static string MenuTitle => L10n.T("_Print…");

    /// <summary>printing.go <c>ItemLabel</c>: Print as a toolbar item and its tooltip.</summary>
    public static string ItemLabel => L10n.T("Print");

    /// <summary>
    /// printing.go <c>Header</c>: a message's header as the client formatted
    /// it; empty fields are left out of the print.
    /// </summary>
    public sealed record Header
    {
        /// <summary>The sender.</summary>
        public string From { get; init; } = "";

        /// <summary>The recipients.</summary>
        public string To { get; init; } = "";

        /// <summary>The copies.</summary>
        public string Cc { get; init; } = "";

        /// <summary>The blind copies (only of the user's own messages, P6).</summary>
        public string Bcc { get; init; } = "";

        /// <summary>The date.</summary>
        public string Date { get; init; } = "";

        /// <summary>The subject.</summary>
        public string Subject { get; init; } = "";

        /// <summary>The attachments' names with their sizes, as the reader's tiles show them (no inline pictures).</summary>
        public IReadOnlyList<string> Attachments { get; init; } = [];
    }

    /// <summary>printing.go <c>Row</c>: a labelled line of a header; Strong is the subject's.</summary>
    /// <param name="Label">The label.</param>
    /// <param name="Value">The value.</param>
    /// <param name="Strong">The subject's row.</param>
    public sealed record Row(string Label, string Value, bool Strong = false);

    /// <summary>
    /// printing.go <c>HeaderRows</c>: <paramref name="h"/>'s rows in print
    /// order, Subject, From, To, Cc, Bcc, Date, Attachments (joined), the
    /// empty ones left out (P6: Bcc is given only for the user's own messages).
    /// </summary>
    public static IReadOnlyList<Row> HeaderRows(Header h)
    {
        ArgumentNullException.ThrowIfNull(h);
        var rows = new List<Row>();
        void Add(string label, string value)
        {
            if (value.Trim().Length > 0)
            {
                rows.Add(new Row(label, value));
            }
        }
        Add(L10n.T("Subject"), h.Subject);
        if (rows.Count == 1)
        {
            rows[0] = rows[0] with { Strong = true };
        }
        Add(L10n.T("From"), h.From);
        Add(L10n.T("To"), h.To);
        Add(L10n.T("Cc"), h.Cc);
        Add(L10n.T("Bcc"), h.Bcc);
        Add(L10n.T("Date"), h.Date);
        Add(L10n.T("Attachments"), string.Join(", ", h.Attachments));
        return rows;
    }

    /// <summary>
    /// printing.go <c>Escape</c>: <paramref name="s"/> as text of HTML, with
    /// &amp; &lt; &gt; " ' as entities. Every string the application puts
    /// into the fragment goes through it.
    /// </summary>
    public static string Escape(string s)
    {
        ArgumentNullException.ThrowIfNull(s);
        var b = new StringBuilder(s.Length);
        foreach (var c in s)
        {
            switch (c)
            {
                case '&':
                    b.Append("&amp;");
                    break;
                case '<':
                    b.Append("&lt;");
                    break;
                case '>':
                    b.Append("&gt;");
                    break;
                case '"':
                    b.Append("&quot;");
                    break;
                case '\'':
                    b.Append("&#39;");
                    break;
                default:
                    b.Append(c);
                    break;
            }
        }
        return b.ToString();
    }

    /// <summary>
    /// printing.go <c>Body</c>: a message's body for the print, the
    /// sanitiser's HTML, or (empty HTML) the plain text.
    /// </summary>
    /// <param name="Html">The sanitised fragment of message.body, inserted verbatim.</param>
    /// <param name="Text">The plain text, escaped here.</param>
    public sealed record Body(string Html = "", string Text = "");

    /// <summary>
    /// printing.go <c>CSS</c>: the print's own sheet, inside the fragment
    /// (the viewer's CSP allows inline style). The classes are ours; a
    /// message's own styles may still reach them in a conversation, which
    /// only changes the looks.
    /// </summary>
    public const string Css =
        ".malachi-print-header{border-collapse:collapse;margin:0 0 12px 0;font:13px -apple-system,system-ui,sans-serif;color:#000}"
        + ".malachi-print-header th{text-align:right;vertical-align:top;padding:1px 8px 1px 0;font-weight:600;white-space:nowrap;color:#444}"
        + ".malachi-print-header td{padding:1px 0;word-break:break-word}"
        + ".malachi-print-subject td{font-weight:700;font-size:15px}"
        + ".malachi-print-body{margin:0 0 12px 0}"
        + ".malachi-print-text{white-space:pre-wrap;word-wrap:break-word;font:12px ui-monospace,Menlo,monospace;margin:0}"
        + ".malachi-print-sep{border:0;border-top:1px solid #999;margin:18px 0}"
        + ".malachi-print-page{break-before:page;page-break-before:always}"
        + ".malachi-print-title{font:700 17px -apple-system,system-ui,sans-serif;margin:0 0 6px 0}"
        + ".malachi-print-line{font:13px -apple-system,system-ui,sans-serif;margin:2px 0}"
        + ".malachi-print-notes{white-space:pre-wrap;font:13px -apple-system,system-ui,sans-serif;margin:10px 0 0 0}"
        + ".malachi-print-day{border-collapse:collapse;width:100%;font:13px -apple-system,system-ui,sans-serif}"
        + ".malachi-print-day td{padding:4px 8px 4px 0;vertical-align:top;border-bottom:1px solid #ddd}"
        + ".malachi-print-day td.time{white-space:nowrap;color:#444;width:1%}";

    /// <summary>printing.go <c>Message</c>: one message's section, its header table and its body.</summary>
    public static string Message(IReadOnlyList<Row>? rows, Body body)
    {
        ArgumentNullException.ThrowIfNull(body);
        var b = new StringBuilder();
        b.Append("<table class=\"malachi-print-header\">");
        foreach (var r in rows ?? [])
        {
            var cls = r.Strong ? " class=\"malachi-print-subject\"" : "";
            b.Append("<tr").Append(cls).Append("><th>").Append(Escape(r.Label)).Append("</th><td>")
                .Append(Escape(r.Value)).Append("</td></tr>");
        }
        b.Append("</table><div class=\"malachi-print-body\">");
        if (body.Html.Length > 0)
        {
            b.Append(body.Html);
        }
        else
        {
            b.Append("<pre class=\"malachi-print-text\">").Append(Escape(body.Text)).Append("</pre>");
        }
        b.Append("</div>");
        return b.ToString();
    }

    /// <summary>printing.go <c>Layout</c>: how sections follow each other.</summary>
    public enum Layout
    {
        /// <summary>One section.</summary>
        One,

        /// <summary>One after the other, a rule between them (P5).</summary>
        Conversation,

        /// <summary>Each section after the first on a new page (P5, a selection).</summary>
        Pages,
    }

    /// <summary>printing.go <c>Fragment</c>: the whole print, the sheet and the sections in order.</summary>
    public static string Fragment(IReadOnlyList<string> sections, Layout layout)
    {
        ArgumentNullException.ThrowIfNull(sections);
        var b = new StringBuilder();
        b.Append("<style>").Append(Css).Append("</style>");
        for (var i = 0; i < sections.Count; i++)
        {
            if (i > 0)
            {
                switch (layout)
                {
                    case Layout.Conversation:
                        b.Append("<hr class=\"malachi-print-sep\">");
                        break;
                    case Layout.Pages:
                        b.Append("<div class=\"malachi-print-page\"></div>");
                        break;
                    case Layout.One:
                    default:
                        break;
                }
            }
            b.Append(sections[i]);
        }
        return b.ToString();
    }

    /// <summary>
    /// printing.go <c>Event</c>: a calendar event as the client formatted its
    /// detail, the title, the lines under it (time, recurrence, location,
    /// calendar, people) and the notes as plain text.
    /// </summary>
    public sealed record PrintedEvent
    {
        /// <summary>The title.</summary>
        public string Title { get; init; } = "";

        /// <summary>The lines under the title; empty ones are left out.</summary>
        public IReadOnlyList<string> Lines { get; init; } = [];

        /// <summary>The rows of the people, each "name — response".</summary>
        public IReadOnlyList<string> Attendees { get; init; } = [];

        /// <summary>The notes as plain text.</summary>
        public string Notes { get; init; } = "";
    }

    /// <summary>
    /// printing.go <c>EventSection</c>: an event's print, the title, its
    /// non-empty lines, the attendees and the notes, all escaped.
    /// </summary>
    public static string EventSection(PrintedEvent e)
    {
        ArgumentNullException.ThrowIfNull(e);
        var b = new StringBuilder();
        b.Append("<h1 class=\"malachi-print-title\">").Append(Escape(e.Title)).Append("</h1>");
        foreach (var l in e.Lines)
        {
            if (l.Trim().Length > 0)
            {
                b.Append("<p class=\"malachi-print-line\">").Append(Escape(l)).Append("</p>");
            }
        }
        foreach (var a in e.Attendees)
        {
            b.Append("<p class=\"malachi-print-line\">").Append(Escape(a)).Append("</p>");
        }
        if (e.Notes.Trim().Length > 0)
        {
            b.Append("<div class=\"malachi-print-notes\">").Append(Escape(e.Notes)).Append("</div>");
        }
        return b.ToString();
    }

    /// <summary>
    /// printing.go <c>DayItem</c>: an event of a day's print, its time column
    /// (the agenda's "All Day" or "9:00–10:00"), title and location.
    /// </summary>
    /// <param name="Time">The time column.</param>
    /// <param name="Title">The title.</param>
    /// <param name="Location">The location, "" for none.</param>
    public sealed record DayItem(string Time, string Title, string Location = "");

    /// <summary>
    /// printing.go <c>DaySection</c>: a day's print (P7), the heading and a
    /// row per event; <paramref name="none"/> when there is nothing that day.
    /// </summary>
    public static string DaySection(string heading, IReadOnlyList<DayItem>? items, string none)
    {
        ArgumentNullException.ThrowIfNull(heading);
        ArgumentNullException.ThrowIfNull(none);
        var b = new StringBuilder();
        b.Append("<h1 class=\"malachi-print-title\">").Append(Escape(heading)).Append("</h1>");
        if (items is null || items.Count == 0)
        {
            b.Append("<p class=\"malachi-print-line\">").Append(Escape(none)).Append("</p>");
            return b.ToString();
        }
        b.Append("<table class=\"malachi-print-day\">");
        foreach (var it in items)
        {
            b.Append("<tr><td class=\"time\">").Append(Escape(it.Time)).Append("</td><td>").Append(Escape(it.Title));
            if (it.Location.Trim().Length > 0)
            {
                b.Append("<br>").Append(Escape(it.Location));
            }
            b.Append("</td></tr>");
        }
        b.Append("</table>");
        return b.ToString();
    }
}
