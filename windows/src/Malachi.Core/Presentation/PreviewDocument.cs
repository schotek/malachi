// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only (docs/windows-port.md §6.6): the previewer's own document for
// text, and the policies of what it serves. The text is escaped into a
// <pre>, so an HTML, SVG or XML file and a message show their source and
// nothing of them is ever parsed as markup; the page has no script and may
// load nothing (default-src 'none'), as the viewer's documents; a fixed
// <title>, as every document of the views (§6.2). The look is the viewer's
// light canvas with a monospace font, as Sushi shows text.

using System;
using System.Text;

namespace Malachi.Core.Presentation;

/// <summary>The documents and policies of the attachment previewer.</summary>
public static class PreviewDocument
{
    /// <summary>The CSP of the text document: inline styles, nothing else.</summary>
    public const string Csp = "default-src 'none'; style-src 'unsafe-inline'";

    /// <summary>
    /// The CSP a picture or a PDF is served with: nothing may load from it.
    /// Chromium draws a picture served as the document and hands a PDF to its
    /// built-in viewer without a request of the page's own.
    /// </summary>
    public const string MediaCsp = "default-src 'none'; style-src 'unsafe-inline'";

    /// <summary>The document's fixed title (a brand name, not translated).</summary>
    public const string Title = "Malachi Mail"; // Windows-only string

    /// <summary>
    /// The document that shows <paramref name="text"/>: HTML-escaped in a
    /// <c>&lt;pre&gt;</c> that wraps long lines.
    /// </summary>
    public static string Text(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        var b = new StringBuilder(text.Length + 512);
        b.Append("<!DOCTYPE html><html><head><meta charset=\"utf-8\"><title>").Append(Title)
            .Append("</title><meta http-equiv=\"Content-Security-Policy\" content=\"").Append(Csp)
            .Append("\"><style>html, body { margin: 0; padding: 0; background: #ffffff; color: #000000; }\n")
            .Append("pre { margin: 0; padding: 12px 16px; white-space: pre-wrap; overflow-wrap: anywhere; ")
            .Append("font-family: \"Cascadia Mono\", Consolas, monospace; font-size: 13px; line-height: 1.4; }")
            .Append("</style></head><body><pre>");
        Escape(b, text);
        return b.Append("</pre></body></html>").ToString();
    }

    /// <summary>
    /// <paramref name="text"/> with <c>&amp; &lt; &gt; " '</c> as character
    /// references and NUL as U+FFFD (what an HTML parser would make of it).
    /// </summary>
    public static string Escape(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        var b = new StringBuilder(text.Length);
        Escape(b, text);
        return b.ToString();
    }

    private static void Escape(StringBuilder b, string text)
    {
        foreach (var c in text)
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
                case '\0':
                    b.Append('�');
                    break;
                default:
                    b.Append(c);
                    break;
            }
        }
    }
}
