// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package htmlview shows the sanitised HTML body of a message in a
// WebKitGTK 6.0 view with JavaScript off, a strict Content-Security-Policy,
// no network and no navigation. It is layer 2 of docs/security.md §3.2: the
// backend's sanitiser is what makes the content safe, this view is what
// keeps a sanitiser bug from becoming a compromise. It knows nothing about
// mail beyond "here is a body fragment and a way to fetch its pictures".
package htmlview

import (
	"log/slog"

	"github.com/diamondburned/gotk4-webkitgtk/pkg/webkit/v6"
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
	status *gtk.Label
	log    *slog.Logger

	// OnLink is called, on the main loop, with the target of a link the
	// user activated: an http(s) or mailto URL. The view itself never
	// follows one. nil ignores clicks.
	OnLink func(uri string)
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
		log:     log.With("component", "htmlview"),
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
		v.log.Warn("web process terminated", "reason", int(reason))
	})
	return v
}

// Load shows a sanitised body fragment.
func (v *View) Load(body string) {
	v.status.SetVisible(false)
	v.web.LoadHtml(Document(body), "")
}

// Clear drops the current document (and its pictures).
func (v *View) Clear() {
	v.Load("")
}

// SetZoom scales the content; percent is the text-zoom setting.
func (v *View) SetZoom(percent int) {
	if percent <= 0 {
		percent = 100
	}
	v.web.SetZoomLevel(float64(percent) / 100)
}

// decidePolicy allows only the initial document load. A link the user
// activated is handed to OnLink and refused for the view; everything else
// is refused outright. Resource responses are allowed: the CSP has already
// limited them to the application's own pictures.
func (v *View) decidePolicy(decision webkit.PolicyDecisioner, t webkit.PolicyDecisionType) bool {
	base := webkit.BasePolicyDecision(decision)
	switch t {
	case webkit.PolicyDecisionTypeNavigationAction, webkit.PolicyDecisionTypeNewWindowAction:
		nav, ok := decision.(*webkit.NavigationPolicyDecision)
		if !ok {
			v.log.Warn("navigation decision of unexpected type; refusing")
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
		if action.IsUserGesture() && AllowedLink(uri) && v.OnLink != nil {
			v.OnLink(uri)
		} else {
			v.log.Debug("navigation refused", "uri", uri)
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
	menu.RemoveAll()
	menu.Append(webkit.NewContextMenuItemFromStockAction(webkit.ContextMenuActionCopy))
	if hit.ContextIsLink() {
		menu.Append(webkit.NewContextMenuItemFromStockAction(webkit.ContextMenuActionCopyLinkToClipboard))
	}
	return false
}
