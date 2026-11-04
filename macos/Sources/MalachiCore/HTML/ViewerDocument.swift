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
///
/// A card of the conversation view (`viewerDocument(body:compact:)`) uses
/// the same sheet with the column's padding cut to the card's: the card's
/// own header sits right above the body and its edges are the card's, so
/// the reader's page margins would set the text a whole gap below the
/// header and in from the card's edge (`viewerCompactCSS`).
let viewerBaseCSS = viewerCSS(columnPadding: "12px 24px 24px")

/// `viewerBaseCSS` for a conversation card: 4 above and below the body,
/// the card's 14 at the sides (ConversationLayout.Metrics.cardPaddingH).
let viewerCompactCSS = viewerCSS(columnPadding: "4px 14px")

/// The sheet with the column's padding (`viewerBaseCSS`).
func viewerCSS(columnPadding: String) -> String {
    """
    html, body { margin: 0; padding: 0; background: #ffffff; color: #000000; }
    body { font-family: sans-serif; line-height: 1.35; overflow-wrap: anywhere; }
    #malachi-column { display: block !important; box-sizing: border-box !important; width: min(100%, 900px) !important; margin: 0 auto !important; padding: \(columnPadding) !important; position: relative !important; left: min(calc((100vw - 100%) / 2), max(0px, calc((100% - 900px) / 2))) !important; }
    img { max-width: 100%; }
    pre { white-space: pre-wrap; }
    table { max-width: 100%; }
    blockquote { margin: 0.5em 0 0.5em 1em; padding-left: 0.75em; border-left: 2px solid #c0c0c0; }
    """
}

/// What a chat bubble's document takes of the text bubbles
/// (`viewerChatCSS`): their text and link colours in the dark appearance as
/// CSS values (`labelColor` and `linkColor`, resolved by the view), and
/// their body font size in points at 100 % text zoom (the view's page zoom
/// scales it with the setting, as `Typo.body(zoom:)` scales the text
/// bubbles').
public struct ViewerChatColours: Sendable, Equatable {
    public var text: String
    public var link: String
    public var fontSize: Double

    public init(text: String, link: String, fontSize: Double = 13) {
        self.text = text
        self.link = link
        self.fontSize = fontSize
    }
}

/// The font rule of a chat bubble's document: every element in the system
/// font at the text bubbles' size, whatever the message set (the owner's
/// decision, 2026-10-20); weights, italics, underlines stay the message's.
func viewerChatFontCSS(size: Double) -> String {
    let px = size == size.rounded() ? String(Int(size)) : String(format: "%.1f", size)
    return "html, body, * { font-family: -apple-system, system-ui, sans-serif !important; font-size: \(px)px !important; line-height: 1.3 !important; }"
}

/// The background rule of a chat bubble's document: every background of
/// the content goes, so the bubble's own (the card colour, the user's
/// accent tint) shows through the transparent web view, whatever the
/// message painted (the owner's decision, 2026-10-20).
public let viewerChatBackgroundCSS =
    "html, body, *, *::before, *::after { background-color: transparent !important; background-image: none !important; }"

/// The sizing rules of a chat bubble's document (`viewerChatCSS`).
public let viewerChatFitCSS = """
    html, body { overflow: hidden !important; margin: 0 !important; }
    #malachi-column { display: flow-root !important; }
    #malachi-column > :first-child { margin-top: 0 !important; }
    #malachi-column > :last-child { margin-bottom: 0 !important; }
    """

/// The sheet of a chat bubble (macOS-first; `viewerDocument(body:compact:chat:)`):
/// the compact sheet with no column padding (the bubble's own padding is
/// around the web view, so the document lines up with a text bubble's
/// text), no backgrounds (`viewerChatBackgroundCSS`), and in the dark
/// appearance the text and links in the bubble's colours, since a mail's
/// dark text would sit on the dark bubble. In the light appearance the
/// message keeps its text colours.
///
/// Without padding, the column's first and last children's margins would
/// collapse through it, and the size script, which measures where the
/// column ends, would miss the last one: the document would be taller
/// than the view and scroll. The column is therefore a block formatting
/// context of its own (`flow-root`), its first and last children have no
/// outer margin (the bubble's padding is the gap), and the page never
/// scrolls (`viewerChatFitCSS`).
func viewerChatCSS(_ colours: ViewerChatColours) -> String {
    viewerCSS(columnPadding: "0")
        + viewerChatFitCSS + "\n"
        + viewerChatFontCSS(size: colours.fontSize) + "\n"
        + viewerChatBackgroundCSS + "\n"
        + "@media (prefers-color-scheme: dark) { * { color: \(colours.text) !important; } a, a * { color: \(colours.link) !important; } }\n"
}

/// htmlview.Document: wraps a sanitised body fragment in the page the view
/// loads. The fragment is inserted verbatim: it is the sanitiser's output
/// and nothing else may ever be passed here. `compact` takes the sheet of
/// a conversation card (`viewerCompactCSS`); `chat` the sheet of a chat
/// bubble with its dark colours (`viewerChatCSS`, over `compact`); the
/// policy is the same.
public func viewerDocument(body: String, compact: Bool = false, chat: ViewerChatColours? = nil) -> String {
    let css = chat.map(viewerChatCSS) ?? (compact ? viewerCompactCSS : viewerBaseCSS)
    return "<!DOCTYPE html><html><head><meta charset=\"utf-8\"><meta http-equiv=\"Content-Security-Policy\" content=\""
        + viewerCSP + "\"><style>" + css
        + "</style></head><body><div id=\"malachi-column\">" + body + "</div></body></html>"
}
