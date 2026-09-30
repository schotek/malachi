// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package htmlview

import (
	"log/slog"

	"github.com/diamondburned/gotk4-webkitgtk/pkg/javascriptcore/v6"
	"github.com/diamondburned/gotk4-webkitgtk/pkg/webkit/v6"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/data"
)

// Card is the body of one card of the conversation view: the sanitised
// HTML of one message in a view as tall as its document, which the
// window stacks with the other cards in one scrolled pane
// (ui/internal/window conversation_card.go). Never one document of the
// whole conversation: in one document a message's CSS could hide, restyle
// or forge the headers of the others (docs/security.md §3.2).
//
// Everything of View holds (html_card.blp is html_view.blp but for one
// thing): an ephemeral network session behind a proxy nothing answers on,
// the default Content-Security-Policy and the same one as a <meta> in the
// document (CompactDocument, the column's padding cut to the card's),
// pictures only through the malachi-cid: scheme, no navigation but the
// initial load (a link the user activates goes to OnLink), the reduced
// context menu, and the link under the pointer reported (OnHover, for the
// pane's one status label; the card shows none of its own).
//
// The one thing: the JavaScript engine is on, for one script of the
// application's own in an isolated world (sizeScript), which only reads
// the layout and reports the document's height through the one message
// handler, registered in that world alone. The document itself can run no
// script: markup script is off (the parser drops script elements, event
// handlers and javascript: URLs), the CSP forbids script, and the
// sanitiser removed it. Go takes the report only as a number
// (readSize) and hands it to OnSize; the window's governor decides the
// height (capped, and frozen for a document that grows with the view) and
// sets it with SetHeight.
type Card struct {
	web *webkit.WebView
	ucm *webkit.UserContentManager
	log *slog.Logger

	// message is the handler of the size messages, disconnected by
	// Release.
	message coreglib.SignalHandle
	// width is the view's width at the last report.
	width int
	// loaded is the body on display; needsReload says the web process went
	// away, so the next Load happens even for the same body.
	loaded      string
	hasLoaded   bool
	needsReload bool
	// committed says the document of the last Load has replaced the one
	// before (load-changed "committed"): a size report before that is the
	// previous document's (a view handed from one card to another, Reset)
	// and is dropped.
	committed bool
	// released says the view is gone for good (Release): its web process
	// was ended on purpose.
	released bool

	// OnLink is called, on the main loop, with the target of a link the
	// user activated: an http(s) or mailto URL. The view never follows one.
	OnLink func(uri string)
	// OnHover is called with the link under the pointer, "" when there is
	// none.
	OnHover func(uri string)
	// OnSize is called with the document's height in CSS pixels, whether
	// the report followed a change of the view's height alone (viewport),
	// and whether the view's width changed since the last report.
	OnSize func(css float64, viewport, widthChanged bool)
}

// NewCard builds a card's view from data/ui/html_card.blp. fetch serves the
// malachi-cid: pictures (the scheme handler of every view in the process,
// registered once on the default WebContext). zoom is the text-zoom
// setting.
func NewCard(log *slog.Logger, fetch PartFetcher, zoom int) *Card {
	registerScheme(fetch)

	b := data.Builder("html_card.ui")
	c := &Card{
		web: b.GetObject("html_card").Cast().(*webkit.WebView),
		ucm: b.GetObject("html_card_ucm").Cast().(*webkit.UserContentManager),
		log: log.With("component", "htmlview"),
	}

	// Nothing the document asks for may leave the process: the CSP stops
	// it first, this stops whatever might slip past the CSP.
	c.web.NetworkSession().SetProxySettings(webkit.NetworkProxyModeCustom,
		webkit.NewNetworkProxySettings("http://127.0.0.1:1", nil))

	// The handler is connected before it is registered (WebKit's advice),
	// and both before the script and the first document.
	c.message = c.ucm.ConnectScriptMessageReceived(c.sizeMessage)
	if !c.ucm.RegisterScriptMessageHandler(sizeHandler, sizeWorld) {
		c.log.Warn("size handler not registered; the card keeps its height")
	}
	c.ucm.AddScript(webkit.NewUserScriptForWorld(sizeScript,
		webkit.UserContentInjectTopFrame, webkit.UserScriptInjectAtDocumentEnd, sizeWorld, nil, nil))

	c.web.ConnectDecidePolicy(func(d webkit.PolicyDecisioner, t webkit.PolicyDecisionType) bool {
		return decidePolicy(c.log, c.OnLink, d, t)
	})
	c.web.ConnectContextMenu(inertContextMenu)
	c.web.ConnectMouseTargetChanged(func(hit *webkit.HitTestResult, _ uint) {
		if c.OnHover == nil {
			return
		}
		if hit.ContextIsLink() {
			c.OnHover(hit.LinkURI())
		} else {
			c.OnHover("")
		}
	})
	c.web.ConnectLoadChanged(func(e webkit.LoadEvent) {
		if e == webkit.LoadCommitted {
			c.committed = true
		}
	})
	c.web.ConnectWebProcessTerminated(func(reason webkit.WebProcessTerminationReason) {
		if c.released {
			return // Release ended it
		}
		c.log.Warn("web process terminated", "reason", int(reason))
		c.needsReload = true
	})
	c.SetZoom(zoom)
	c.SetHeight(0)
	return c
}

// Widget is the view, to put where a widget is expected.
func (c *Card) Widget() gtk.Widgetter { return c.web }

// Load shows a sanitised body fragment, in the compact document. The body
// on display is not loaded again unless reload says so (its pictures kept
// on the mail server were downloaded: the same malachi-cid: URLs have
// something to serve now) or the web process went away.
func (c *Card) Load(body string, reload bool) {
	if c.hasLoaded && !reload && !c.needsReload && c.loaded == body {
		return
	}
	c.loaded, c.hasLoaded, c.needsReload = body, true, false
	c.committed = false
	c.web.LoadHtml(CompactDocument(body), "")
}

// SetZoom scales the content; percent is the text-zoom setting.
func (c *Card) SetZoom(percent int) {
	if percent <= 0 {
		percent = 100
	}
	c.web.SetZoomLevel(float64(percent) / 100)
}

// SetHeight makes the view px pixels tall (the window's governor decides
// how tall; 0 leaves it to the host until the first report).
func (c *Card) SetHeight(px int) {
	if px <= 0 {
		c.web.SetSizeRequest(-1, -1)
		return
	}
	c.web.SetSizeRequest(-1, px)
}

// Reset readies the view for another card of the pane (the window's pool
// of views): the callbacks are dropped and the document shown stays until
// the next Load replaces it, which is always a new one (a size report of
// the old document is dropped meanwhile). The web process stays: a view
// handed on costs no new process, as the single-message view keeps one
// for every message it shows.
func (c *Card) Reset() {
	c.OnLink, c.OnHover, c.OnSize = nil, nil, nil
	c.hasLoaded, c.committed = false, false
	c.width = 0
	c.SetHeight(0)
}

// Release lets go of the view for good: the size messages stop, the
// handler goes, the callbacks are dropped, and the web process is ended at
// once. Without the last it would live on until the Go wrapper of the view
// is collected, and a pane whose cards come and go would pile processes up
// until the sandbox cannot start another one (bwrap runs out of
// namespaces) and WebKit aborts. The owner removes the widget.
func (c *Card) Release() {
	if c.released {
		return
	}
	c.released = true
	c.OnLink, c.OnHover, c.OnSize = nil, nil, nil
	if c.message != 0 {
		c.ucm.HandlerDisconnect(c.message)
		c.message = 0
	}
	c.ucm.UnregisterScriptMessageHandler(sizeHandler, sizeWorld)
	c.ucm.RemoveAllScripts()
	c.web.StopLoading()
	c.web.TerminateWebProcess()
}

// sizeMessage is a message of the card's script: the height, validated
// (readSize), goes to OnSize with whether the width changed since the last
// one. Anything else is dropped.
func (c *Card) sizeMessage(v *javascriptcore.Value) {
	if !c.committed || c.released {
		return // the previous document's, or none any more
	}
	css, viewport, ok := readSize(v)
	if !ok {
		c.log.Debug("size message dropped")
		return
	}
	w := c.web.Width()
	changed := w != c.width
	c.width = w
	if c.OnSize != nil {
		c.OnSize(css, viewport, changed)
	}
}
