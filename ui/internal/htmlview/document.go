// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package htmlview

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
const baseCSS = `html, body { margin: 0; padding: 0; background: #ffffff; color: #000000; }
body { padding: 12px; font-family: sans-serif; line-height: 1.35; overflow-wrap: anywhere; }
img { max-width: 100%; }
pre { white-space: pre-wrap; }
table { max-width: 100%; }
blockquote { margin: 0.5em 0 0.5em 1em; padding-left: 0.75em; border-left: 2px solid #c0c0c0; }`

// Document wraps a sanitised body fragment in the page the view loads. The
// fragment is inserted verbatim: it is the sanitiser's output and nothing
// else may ever be passed here.
func Document(body string) string {
	return `<!DOCTYPE html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="` +
		CSP + `"><style>` + baseCSS + `</style></head><body>` + body + `</body></html>`
}
