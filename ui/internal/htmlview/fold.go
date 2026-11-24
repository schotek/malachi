// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package htmlview

import (
	"html"
	"strings"

	"github.com/diamondburned/gotk4-webkitgtk/pkg/javascriptcore/v6"
)

// The folded quoted history of a body (message.body foldQuoted,
// docs/api.md): the daemon keeps the history in the HTML, a
// <details class="malachi-quoted"> toggle where it starts and its nodes
// marked malachi-quoted-more. The document's sheet (foldCSS) hides them
// while the toggle is closed and draws the toggle as the small "•••"
// button, so a click shows the history in place, without asking the
// daemon or loading the document again. The view's own script (foldScript,
// in the isolated world of the size script) reports the user's clicks, so
// that the next document of the same message (its pictures loaded, a card
// made live again) starts as the user left it (View.FoldOpen,
// Card.FoldOpen), and swaps the toggle's tooltip and accessible name.
// A port of the macOS client's ViewerFold (ViewerDocument.swift).

// foldCSS is the fold's part of every sheet but the chat's, which hides the
// toggle (chatFitCSS). !important, because the mail's own <style> comes
// later. The rules for a link in the summary serve the Windows client,
// whose views run no script of the page: its toggle's face is a link the
// host answers (ViewerDocument.Folded there).
const foldCSS = `html:has(details.malachi-quoted:not([open])) .malachi-quoted-more { display: none !important; }
details.malachi-quoted { display: block !important; margin: 10px 0 !important; padding: 0 !important; border: 0 !important; }
details.malachi-quoted > summary { display: inline-block !important; list-style: none !important; margin: 0 !important; padding: 2px 9px 4px !important; border: 0 !important; border-radius: 6px !important; background: #e9e9ee !important; color: #5c5c66 !important; font: 700 12px/1 sans-serif !important; letter-spacing: 1px !important; cursor: default !important; -webkit-user-select: none !important; user-select: none !important; }
details.malachi-quoted > summary::-webkit-details-marker { display: none !important; }
details.malachi-quoted > summary:hover { background: #dedee5 !important; }
details.malachi-quoted[open] > summary { background: #d4d4dc !important; }
details.malachi-quoted > summary:focus { outline: none !important; }
details.malachi-quoted > summary:focus-visible { outline: 2px solid Highlight !important; outline-offset: 1px !important; }
details.malachi-quoted > summary > a { color: inherit !important; text-decoration: none !important; cursor: default !important; outline: none !important; }
details.malachi-quoted > summary > a:focus-visible { outline: 2px solid Highlight !important; outline-offset: 3px !important; }`

// FoldLabels are the toggle's tooltip and accessible name while the
// history is hidden (Show) and while it shows (Hide); the window sets
// them once, translated, before the first document.
var FoldLabels struct{ Show, Hide string }

// foldToggle is the toggle exactly as the sanitiser writes it
// (backend/internal/sanitize/fold.go). Only the sanitiser writes the
// class, so only its toggle matches.
const foldToggle = `<details class="malachi-quoted"><summary>` + "•••" + `</summary>`

// Folded is body with its fold (if any) open or closed and labelled with
// FoldLabels: the open attribute, the summary's title and aria-label, and
// the two labels for foldScript (data-malachi-show, data-malachi-hide; the
// sanitiser drops every data- attribute of the mail, so these are the
// application's). Only the first toggle is the sanitiser's fold; a body
// without one comes back as it is.
func Folded(body string, open bool) string {
	i := strings.Index(body, foldToggle)
	if i < 0 {
		return body
	}
	show, hide := FoldLabels.Show, FoldLabels.Hide
	label := show
	attr := ""
	if open {
		label, attr = hide, ` open=""`
	}
	esc := html.EscapeString
	toggle := `<details class="malachi-quoted"` + attr + `><summary title="` + esc(label) + `" aria-label="` + esc(label) +
		`" data-malachi-show="` + esc(show) + `" data-malachi-hide="` + esc(hide) + `">` + "•••" + `</summary>`
	return body[:i] + toggle + body[i+len(foldToggle):]
}

// foldHandler is foldScript's message handler, registered in sizeWorld
// only, like the size handler.
const foldHandler = "fold"

// foldScript reports the user's opening (true) or closing (false) of the
// fold to the fold handler and swaps the toggle's title and aria-label. A
// toggle event does not bubble; a listener on the document in the capture
// phase still hears it.
const foldScript = `(function () {
  document.addEventListener('toggle', function (e) {
    var d = e.target;
    if (!d || !d.matches || !d.matches('details.malachi-quoted')) { return; }
    var s = d.querySelector('summary');
    if (s) {
      var l = s.getAttribute(d.open ? 'data-malachi-hide' : 'data-malachi-show');
      if (l) { s.setAttribute('title', l); s.setAttribute('aria-label', l); }
    }
    try { window.webkit.messageHandlers.` + foldHandler + `.postMessage(!!d.open); } catch (e) {}
  }, true);
})();`

// readFold reads a message of foldScript: a boolean; anything else is not
// one (false).
func readFold(v *javascriptcore.Value) (open, ok bool) {
	if v == nil || !v.IsBoolean() {
		return false, false
	}
	return v.ToBoolean(), true
}
