// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package htmlview

import "fmt"

// CSP is the document's Content-Security-Policy: no network at all, images
// only from the application's own scheme (message parts) and data: URIs
// (what the daemon inlined), styles only inline. The same string is the
// view's default policy in html_view.blp; here it is repeated as a <meta>
// so the document carries it wherever it is loaded.
const CSP = "default-src 'none'; img-src malachi-cid: data:; style-src 'unsafe-inline'"

// baseCSS is the author-level baseline under the message's own styles: a
// light canvas whatever the desktop theme (a mail designed for white stays
// readable; adapting colours a mail did not set while keeping the ones it
// did cannot be done safely), pictures and preformatted text that do not
// overflow, and a plain sans-serif for text the mail leaves unstyled.
//
// The message sits in #malachi-column, the column of the headers above it:
// 900 wide at most, centred, its text 24 in from the sides like theirs
// (window.blp's message clamps, whose tightening threshold equals the
// maximum so that the two match). Text wraps at that width and a centred
// newsletter stays centred; a layout with wider fixed widths (a table
// quoted from Outlook) starts at the column's edge and reaches out to the
// right, so the text above and below stays in line with the headers. The column
// is our own element, !important, because mails reset body's margins and
// padding in their <style> (which the sanitiser keeps). A classic
// scrollbar narrows the page but not the headers' pane, so the column
// moves right by half its width while there is room: `left` is the
// smaller of half the scrollbar and half the room beside a 900 column.
//
// A card of the conversation view (CompactDocument) uses the same sheet
// with the column's padding cut to the card's: the card's own header sits
// right above the body and its edges are the card's, so the reader's page
// margins would set the text a whole gap below the header and in from the
// card's edge (compactCSS).
var baseCSS = columnCSS("12px 24px 24px")

// compactCSS is baseCSS for a conversation card: 4 above and below the
// body, the card's 14 at the sides (window.convCardPaddingH).
var compactCSS = columnCSS("4px 14px")

// columnCSS is the sheet with the column's padding (baseCSS).
func columnCSS(padding string) string {
	return `html, body { margin: 0; padding: 0; background: #ffffff; color: #000000; }
body { font-family: sans-serif; line-height: 1.35; overflow-wrap: anywhere; }
#malachi-column { display: block !important; box-sizing: border-box !important; width: min(100%, 900px) !important; margin: 0 auto !important; padding: ` + padding + ` !important; position: relative !important; left: min(calc((100vw - 100%) / 2), max(0px, calc((100% - 900px) / 2))) !important; }
img { max-width: 100%; }
pre { white-space: pre-wrap; }
table { max-width: 100%; }
blockquote { margin: 0.5em 0 0.5em 1em; padding-left: 0.75em; border-left: 2px solid #c0c0c0; }`
}

// Document wraps a sanitised body fragment in the page the view loads. The
// fragment is inserted verbatim: it is the sanitiser's output and nothing
// else may ever be passed here.
func Document(body string) string {
	return document(body, baseCSS)
}

// CompactDocument is Document for a card of the conversation view
// (htmlview.Card): the same policy, the column's padding cut to the card's
// (compactCSS). The fragment is the sanitiser's output of one message and
// nothing else.
func CompactDocument(body string) string {
	return document(body, compactCSS)
}

func document(body, css string) string {
	return `<!DOCTYPE html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="` +
		CSP + `"><style>` + css + `</style></head><body><div id="malachi-column">` + body + `</div></body></html>`
}

// Colour is an sRGB colour for the chat's document (ChatStyle), written to
// the sheet as #rrggbb, so that nothing but six hex digits ever reaches it.
type Colour struct{ R, G, B uint8 }

func (c Colour) css() string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }

// ChatStyle is how a bubble of the chat layout shows a message's HTML
// (ChatDocument): on the bubble's own background, whichever the message
// set, and in the dark style in the theme's text and link colours.
type ChatStyle struct {
	// Dark: the dark style; Text and Link then replace every colour of the
	// content (dark text would sit on the dark bubble).
	Dark       bool
	Text, Link Colour
}

// chatNeutralCSS takes every background of the content away, so that the
// transparent view shows the bubble's background (card colour for the
// others, the accent tint for the user's own).
const chatNeutralCSS = `html, body, *, *::before, *::after { background-color: transparent !important; background-image: none !important; }`

// chatFitCSS keeps a bubble's document from ever showing a scrollbar (the
// view is sized to it; a rounding difference must not show a bar) and
// makes the column contain its children's margins (flow-root): without
// the cards' padding a last paragraph's bottom margin would collapse
// through the column, past where sizeScript measures it ends.
const chatFitCSS = `html, body { overflow: hidden !important; margin: 0 !important; }
#malachi-column { display: flow-root !important; }`

// chatCSS is the sheet of a chat bubble's document: the cards' sheet with
// no padding of its own (the bubble's padding is the text's), every
// background neutralised and, in the dark style, the text and the links in
// the theme's colours.
func chatCSS(s ChatStyle) string {
	css := columnCSS("0") + "\n" + chatNeutralCSS + "\n" + chatFitCSS
	if s.Dark {
		css += "\n* { color: " + s.Text.css() + " !important; }\na, a * { color: " + s.Link.css() + " !important; }"
	}
	return css
}

// ChatDocument is CompactDocument for a bubble of the chat layout: the same
// policy (the CSP as a <meta>), the sheet chatCSS. The fragment is the
// sanitiser's output of one message and nothing else.
func ChatDocument(body string, s ChatStyle) string {
	return document(body, chatCSS(s))
}
