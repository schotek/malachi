// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/HTML/ViewerDocument.swift; GTK:
// ui/internal/htmlview/document.go (CSP, baseCSS, Document).
//
// The page the message viewer loads, byte for byte GTK's and macOS's with
// one addition: a fixed <title> right after the charset (docs/windows-port.md
// §6.2). WebView2 reports the document's title to its host, which shows it
// in places other processes can read (UI Automation, the window title if a
// host ever used it); a <title> the sanitised fragment might carry comes
// after this one, and only the first counts. The WebView2 layer sends the
// CSP as a response header as well.

using System;

namespace Malachi.Core.Html;

/// <summary>The document around a sanitised message body.</summary>
public static class ViewerDocument
{
    /// <summary>
    /// htmlview.CSP: the document's Content-Security-Policy: no network at
    /// all, images only from the application's own scheme (message parts)
    /// and data: URIs (what the daemon inlined), styles only inline.
    /// </summary>
    public const string Csp = "default-src 'none'; img-src malachi-cid: data:; style-src 'unsafe-inline'";

    /// <summary>The document's fixed title (a brand name, not translated).</summary>
    public const string Title = "Malachi Mail"; // Windows-only string

    /// <summary>
    /// htmlview.baseCSS: the author-level baseline under the message's own
    /// styles: a light canvas whatever the desktop theme, pictures and
    /// preformatted text that do not overflow, a plain sans-serif for text
    /// the mail leaves unstyled. The message sits in #malachi-column, the
    /// column of the headers above it: 900 wide at most, centred, its text 24
    /// in from the sides like theirs; a classic scrollbar narrows the page but
    /// not the headers' pane, so the column moves right by half its width
    /// while there is room.
    /// </summary>
    public const string BaseCss =
        "html, body { margin: 0; padding: 0; background: #ffffff; color: #000000; }\n"
        + "body { font-family: sans-serif; line-height: 1.35; overflow-wrap: anywhere; }\n"
        + "#malachi-column { display: block !important; box-sizing: border-box !important; width: min(100%, 900px) !important; margin: 0 auto !important; padding: 12px 24px 24px !important; position: relative !important; left: min(calc((100vw - 100%) / 2), max(0px, calc((100% - 900px) / 2))) !important; }\n"
        + "img { max-width: 100%; }\n"
        + "pre { white-space: pre-wrap; }\n"
        + "table { max-width: 100%; }\n"
        + "blockquote { margin: 0.5em 0 0.5em 1em; padding-left: 0.75em; border-left: 2px solid #c0c0c0; }";

    /// <summary>
    /// htmlview.Document: wraps a sanitised body fragment in the page the view
    /// loads. The fragment is inserted verbatim: it is the sanitiser's output
    /// and nothing else may ever be passed here.
    /// </summary>
    public static string Document(string body)
    {
        ArgumentNullException.ThrowIfNull(body);
        return "<!DOCTYPE html><html><head><meta charset=\"utf-8\"><title>" + Title
            + "</title><meta http-equiv=\"Content-Security-Policy\" content=\"" + Csp + "\"><style>" + BaseCss
            + "</style></head><body><div id=\"malachi-column\">" + body + "</div></body></html>";
    }
}
