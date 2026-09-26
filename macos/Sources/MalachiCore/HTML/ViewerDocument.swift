// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/htmlview/document.go: the page the message view loads.

import Foundation

/// htmlview.CSP: the document's Content-Security-Policy: no network at
/// all, images only from the application's own scheme (message parts) and
/// data: URIs (what the daemon inlined), styles only inline. GTK repeats
/// the view's default policy as a <meta>; a WKWebView has no default
/// policy, so the <meta> is the only carrier and must stay identical.
public let viewerCSP = "default-src 'none'; img-src malachi-cid: data:; style-src 'unsafe-inline'"

/// htmlview.baseCSS: the author-level baseline under the message's own
/// styles: a light canvas whatever the desktop theme (a mail designed for
/// white stays readable; adapting colours a mail did not set while
/// keeping the ones it did cannot be done safely), pictures and
/// preformatted text that do not overflow, and a plain sans-serif for
/// text the mail leaves unstyled.
///
/// The message sits in #malachi-column, the column of the headers above
/// it: 900 wide at most, centred, its text 24 in from the sides like
/// theirs (MessageViewController's clamps, whose tightening threshold
/// equals the maximum so that the two match). Text wraps at that width and
/// a centred newsletter stays centred; a layout with wider fixed widths (a
/// table quoted from Outlook) starts at the column's edge and reaches out
/// to the right, so the text above and below stays in line with the
/// headers. The column is our own element, !important, because mails reset
/// body's margins and padding in their <style> (which the sanitiser keeps).
/// A classic scrollbar narrows the page but not the headers' pane, so the
/// column moves right by half its width while there is room: `left` is
/// the smaller of half the scrollbar and half the room beside a 900 column.
let viewerBaseCSS = """
html, body { margin: 0; padding: 0; background: #ffffff; color: #000000; }
body { font-family: sans-serif; line-height: 1.35; overflow-wrap: anywhere; }
#malachi-column { display: block !important; box-sizing: border-box !important; width: min(100%, 900px) !important; margin: 0 auto !important; padding: 12px 24px 24px !important; position: relative !important; left: min(calc((100vw - 100%) / 2), max(0px, calc((100% - 900px) / 2))) !important; }
img { max-width: 100%; }
pre { white-space: pre-wrap; }
table { max-width: 100%; }
blockquote { margin: 0.5em 0 0.5em 1em; padding-left: 0.75em; border-left: 2px solid #c0c0c0; }
"""

/// htmlview.Document: wraps a sanitised body fragment in the page the view
/// loads. The fragment is inserted verbatim: it is the sanitiser's output
/// and nothing else may ever be passed here.
public func viewerDocument(body: String) -> String {
    "<!DOCTYPE html><html><head><meta charset=\"utf-8\"><meta http-equiv=\"Content-Security-Policy\" content=\""
        + viewerCSP + "\"><style>" + viewerBaseCSS + "</style></head><body><div id=\"malachi-column\">" + body + "</div></body></html>"
}
