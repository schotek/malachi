// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// The body area of a message view takes no more room than its body needs
// while that fits, so the "•••" of the quoted history under it follows the
// body's last line; a longer body takes the whole area and scrolls, the
// button then at the bottom (macOS MessageViewController textFit and
// htmlFit). The text page does it on its own: its scrolled window takes
// the label's natural height (propagate-natural-height) and the page box
// gives the rest to body_filler. The HTML page cannot, because a web view
// has no natural height: htmlview.View reports its document's height
// (OnSize), and while that fits the room the page has (html_probe), the
// view asks for exactly that height inside html_scroll, which has no
// minimum height of its own, so the window can still shrink; otherwise the
// view takes the whole area and scrolls its document itself. The spinner
// takes the whole area. The view of an attached message, whose builder has
// no body_filler (and no "•••"), keeps the whole area always.
type bodyFit struct {
	// filler is nil on a view without fitting.
	filler *gtk.Box
	stack  *gtk.Stack
	// page is the body page on display: "text", "loading" or "html".
	page string
	// px is the HTML document's height in pixels, -1 while unknown; room
	// is the HTML page's height.
	px, room int
	// fitted says the HTML view asks for its document's height.
	fitted bool
	// size sets the HTML view's height request (-1 for none); nil before
	// the view exists. asked is the request it last set.
	size  func(px int)
	asked int
}

// fitsBody is whether an HTML document px pixels tall fits the room the
// HTML page has. While the view asks for its document's height the page is
// exactly that tall when it fits and less when the box squeezed it; while
// the view takes the whole area the page has all the room there is: the
// same comparison holds in both, so the two never flip back and forth.
func fitsBody(px, room int) bool {
	return px >= 0 && room > 0 && px <= room
}

// bindBodyFit takes the widgets of the fitting from the builder, when it
// has them (window.blp and message_window.blp).
func (v *messageView) bindBodyFit(b *gtk.Builder) {
	v.fit = bodyFit{stack: v.stack, page: "text", px: -1, asked: -1}
	filler := b.GetObject("body_filler")
	probe := b.GetObject("html_probe")
	if filler == nil || probe == nil {
		return
	}
	v.fit.filler = filler.Cast().(*gtk.Box)
	probe.Cast().(*gtk.DrawingArea).ConnectResize(func(_, h int) {
		v.fit.room = h
		v.fit.apply()
	})
	v.fit.apply()
}

// setBodyPage shows body page name ("text", "loading" or "html") and
// gives the body area its height for it.
func (v *messageView) setBodyPage(name string) {
	v.stack.SetVisibleChildName(name)
	v.fit.page = name
	v.fit.apply()
}

// setHTMLHeight is the HTML view's report of its document's height.
func (f *bodyFit) setHTMLHeight(px int) {
	f.px = px
	f.apply()
}

// apply gives the body area its height for the page on display.
func (f *bodyFit) apply() {
	if f.filler == nil {
		return
	}
	fitted := fitsBody(f.px, f.room)
	ask := -1
	if fitted {
		ask = f.px
	}
	if f.size != nil && ask != f.asked {
		f.asked = ask
		f.size(ask)
	}
	f.fitted = fitted
	fill := f.page == "loading" || f.page == "html" && !fitted
	f.stack.SetVExpand(fill)
	f.filler.SetVisible(!fill)
}
