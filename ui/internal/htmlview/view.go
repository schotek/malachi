// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package htmlview shows the sanitised HTML body of a message in a
// WebKitGTK 6.0 view where the document can run no script, a strict
// Content-Security-Policy,
// no network and no navigation. It is layer 2 of docs/security.md §3.2: the
// backend's sanitiser is what makes the content safe, this view is what
// keeps a sanitiser bug from becoming a compromise. It knows nothing about
// mail beyond "here is a body fragment and a way to fetch its pictures".
//
// Both the viewer (View) and a card of the conversation view (Card,
// card.go) run one script of the application's own in an isolated world
// that reports the document's height (size.go), the document itself still
// unable to run any. A card takes that height; the viewer keeps scrolling
// its document and only tells its owner (OnSize), so that the body area
// needs no more room than the document and what comes under it follows
// its last line.
package htmlview

import (
	"log/slog"
	"math"

	"github.com/diamondburned/gotk4-webkitgtk/pkg/javascriptcore/v6"
	"github.com/diamondburned/gotk4-webkitgtk/pkg/webkit/v6"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/ui/data"
	_ "github.com/schotek/malachi/ui/internal/webkitenv" // renderer switch before the first view
)

// View is the viewer widget: the WebView with a small status label for the
// link under the pointer. Embed it where a widget is expected.
type View struct {
	*gtk.Overlay

	web    *webkit.WebView
	ucm    *webkit.UserContentManager
	status *gtk.Label
	log    *slog.Logger

	// message is the handler of the size messages, disconnected by Close.
	message coreglib.SignalHandle
	// committed says the document of the last Load has replaced the one
	// before (load-changed "committed"): a size report before that is the
	// previous document's and is dropped.
	committed bool
	// css is the document's last height in CSS pixels, -1 while unknown.
	css float64
	// zoom is the text zoom as WebKit's zoom level.
	zoom float64
	// closed says Close ended the view.
	closed bool

	// OnLink is called, on the main loop, with the target of a link the
	// user activated: an http(s) or mailto URL. The view itself never
	// follows one. nil ignores clicks.
	OnLink func(uri string)
	// OnSize is called with the document's height in pixels at the text
	// zoom whenever it changes, and with -1 when a new document starts
	// loading and its height is not known yet. The view never takes the
	// height itself: it scrolls its document in whatever height it gets.
	OnSize func(px int)
}

// New builds a viewer from data/ui/html_view.blp. fetch serves the
// malachi-cid: pictures of every viewer in the process (the scheme handler
// is registered once, on the default WebContext).
func New(log *slog.Logger, fetch PartFetcher) *View {
	registerScheme(fetch)

	b := data.Builder("html_view.ui")
	v := &View{
		Overlay: gtk.NewOverlay(),
		web:     b.GetObject("html_view").Cast().(*webkit.WebView),
		ucm:     b.GetObject("html_ucm").Cast().(*webkit.UserContentManager),
		log:     log.With("component", "htmlview"),
		css:     -1,
		zoom:    1,
	}
	v.Overlay.SetChild(v.web)

	// The link under the pointer, browser-style, in the bottom-left corner.
	v.status = gtk.NewLabel("")
	v.status.SetUseMarkup(false)
	v.status.SetEllipsize(pango.EllipsizeMiddle)
	v.status.SetMaxWidthChars(80)
	v.status.SetHAlign(gtk.AlignStart)
	v.status.SetVAlign(gtk.AlignEnd)
	v.status.SetMarginStart(6)
	v.status.SetMarginBottom(6)
	v.status.AddCSSClass("osd")
	v.status.AddCSSClass("caption")
	v.status.SetVisible(false)
	v.Overlay.AddOverlay(v.status)

	// Nothing the document asks for may leave the process: the CSP stops
	// it first, this stops whatever might slip past the CSP.
	v.web.NetworkSession().SetProxySettings(webkit.NetworkProxyModeCustom,
		webkit.NewNetworkProxySettings("http://127.0.0.1:1", nil))

	// The card's measurement, in the same isolated world (size.go): the
	// handler connected before it is registered, both before the script
	// and the first document.
	v.message = v.ucm.ConnectScriptMessageReceived(v.sizeMessage)
	if !v.ucm.RegisterScriptMessageHandler(sizeHandler, sizeWorld) {
		v.log.Warn("size handler not registered; the body keeps the whole area")
	}
	v.ucm.AddScript(webkit.NewUserScriptForWorld(sizeScript,
		webkit.UserContentInjectTopFrame, webkit.UserScriptInjectAtDocumentEnd, sizeWorld, nil, nil))
	v.web.ConnectLoadChanged(func(e webkit.LoadEvent) {
		if e == webkit.LoadCommitted {
			v.committed = true
		}
	})

	v.web.ConnectDecidePolicy(v.decidePolicy)
	v.web.ConnectContextMenu(v.contextMenu)
	v.web.ConnectMouseTargetChanged(func(hit *webkit.HitTestResult, _ uint) {
		if hit.ContextIsLink() {
			v.status.SetText(hit.LinkURI())
			v.status.SetVisible(true)
		} else {
			v.status.SetVisible(false)
		}
	})
	v.web.ConnectWebProcessTerminated(func(reason webkit.WebProcessTerminationReason) {
		if reason == webkit.WebProcessTerminatedByApi {
			return // Close ended it
		}
		v.log.Warn("web process terminated", "reason", int(reason))
	})
	return v
}

// Load shows a sanitised body fragment.
func (v *View) Load(body string) {
	v.status.SetVisible(false)
	v.committed = false
	v.css = -1
	if v.OnSize != nil {
		v.OnSize(-1)
	}
	v.web.LoadHtml(Document(body), "")
}

// Clear drops the current document (and its pictures).
func (v *View) Clear() {
	v.Load("")
}

// Close ends the view's web process at once, for a view whose window
// closes: without it the process lives on until the Go wrapper of the view
// is collected, and windows opened and closed one after another pile
// processes up (conversation cards showed where that ends: the sandbox
// runs out of namespaces and WebKit aborts). The view shows nothing
// afterwards.
func (v *View) Close() {
	v.OnLink, v.OnSize = nil, nil
	v.closed = true
	if v.message != 0 {
		v.ucm.HandlerDisconnect(v.message)
		v.message = 0
	}
	v.ucm.UnregisterScriptMessageHandler(sizeHandler, sizeWorld)
	v.ucm.RemoveAllScripts()
	v.web.StopLoading()
	v.web.TerminateWebProcess()
}

// SetZoom scales the content; percent is the text-zoom setting.
func (v *View) SetZoom(percent int) {
	if percent <= 0 {
		percent = 100
	}
	v.zoom = float64(percent) / 100
	v.web.SetZoomLevel(v.zoom)
	v.reportSize()
}

// sizeMessage is a message of the view's script: the height, validated
// (readSize), goes to OnSize at the text zoom. Anything else is dropped.
func (v *View) sizeMessage(val *javascriptcore.Value) {
	if !v.committed || v.closed {
		return // the previous document's, or none any more
	}
	css, _, ok := readSize(val)
	if !ok {
		v.log.Debug("size message dropped")
		return
	}
	v.css = css
	v.reportSize()
}

// reportSize hands the last height, if known, to OnSize in pixels.
func (v *View) reportSize() {
	if v.css < 0 || v.OnSize == nil {
		return
	}
	v.OnSize(sizePixels(v.css, v.zoom))
}

// sizePixels is a height in CSS pixels at WebKit's zoom level z, in whole
// widget pixels (rounded up, so the last line is never cut).
func sizePixels(css, z float64) int {
	return int(math.Ceil(css * z))
}

// decidePolicy allows only the initial document load. A link the user
// activated is handed to OnLink and refused for the view; everything else
// is refused outright. Resource responses are allowed: the CSP has already
// limited them to the application's own pictures.
func (v *View) decidePolicy(decision webkit.PolicyDecisioner, t webkit.PolicyDecisionType) bool {
	return decidePolicy(v.log, v.OnLink, decision, t)
}

// decidePolicy is the navigation policy of the viewer and of a
// conversation card (Card): the initial load only, a user's link to
// onLink (nil ignores it).
func decidePolicy(log *slog.Logger, onLink func(string), decision webkit.PolicyDecisioner, t webkit.PolicyDecisionType) bool {
	base := webkit.BasePolicyDecision(decision)
	switch t {
	case webkit.PolicyDecisionTypeNavigationAction, webkit.PolicyDecisionTypeNewWindowAction:
		nav, ok := decision.(*webkit.NavigationPolicyDecision)
		if !ok {
			log.Warn("navigation decision of unexpected type; refusing")
			base.Ignore()
			return true
		}
		action := nav.NavigationAction()
		uri := action.Request().URI()
		if t == webkit.PolicyDecisionTypeNavigationAction && uri == "about:blank" && !action.IsUserGesture() {
			base.Use()
			return true
		}
		base.Ignore()
		if action.IsUserGesture() && AllowedLink(uri) && onLink != nil {
			onLink(uri)
		} else {
			log.Debug("navigation refused", "uri", uri)
		}
	default:
		base.Use()
	}
	return true
}

// contextMenu keeps only what makes sense for inert content: copying the
// selection and the link under the pointer. Reload, Back, Open in New
// Window and the rest of WebKit's menu are gone.
func (v *View) contextMenu(menu *webkit.ContextMenu, hit *webkit.HitTestResult) bool {
	return inertContextMenu(menu, hit)
}

// inertContextMenu is the context menu of the viewer and of a conversation
// card (Card): Copy, and Copy Link over a link.
func inertContextMenu(menu *webkit.ContextMenu, hit *webkit.HitTestResult) bool {
	menu.RemoveAll()
	menu.Append(webkit.NewContextMenuItemFromStockAction(webkit.ContextMenuActionCopy))
	if hit.ContextIsLink() {
		menu.Append(webkit.NewContextMenuItemFromStockAction(webkit.ContextMenuActionCopyLinkToClipboard))
	}
	return false
}
