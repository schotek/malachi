// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package htmlview

import (
	"math"

	"github.com/diamondburned/gotk4-webkitgtk/pkg/javascriptcore/v6"
)

// The height measurement of a conversation card (Card): the one script of
// the application's own that runs in a card's view, and what Go takes from
// it. The script lives in an isolated world of its own, so nothing of the
// document shares its globals or can reach its message handler, which is
// registered in that world alone; the document cannot run script at all
// (html_card.blp). The script only reads the layout, and the one thing it
// reports is the document's height in CSS pixels, with a flag of its own
// making (the report followed a change of the view's height alone). Go
// takes nothing else, and takes that only as a finite number that is not
// negative (readSize).

const (
	// sizeWorld is the isolated script world of the card's script.
	sizeWorld = "malachi-size"
	// sizeHandler is the script's message handler, registered in
	// sizeWorld only.
	sizeHandler = "size"
	// maxReportedCSS caps a reported height, in CSS pixels, before the
	// window's governor (which caps the view at 4000 pixels) sees it.
	maxReportedCSS = 1 << 20
)

// sizeScript reports the document's height in CSS pixels to the size
// handler whenever it changes: a ResizeObserver on the document element
// and on the column Document puts the message in, and every picture that
// finishes loading. The height is where the column ends, its overflow
// included (not document.documentElement.scrollHeight, which never falls
// below the view's own height, so a card could never shrink), so it does
// not depend on the view's height unless the message's CSS makes it
// (100vh, height: 100%): a report within 100 ms of a change of the view's
// height alone says so (v), for the window's governor to stop such a
// document from growing the view without end. It reads the layout only;
// the document is not changed, and nothing of it but the number leaves.
const sizeScript = `(function () {
  var last = -1, vw = window.innerWidth, vh = window.innerHeight, viewport = false, timer = null;
  function post(h) {
    try { window.webkit.messageHandlers.` + sizeHandler + `.postMessage({h: h, v: viewport}); } catch (e) {}
  }
  function measure() {
    var col = document.getElementById('malachi-column');
    var h = 0;
    if (col) {
      var r = col.getBoundingClientRect();
      h = r.top + window.scrollY + Math.max(r.height, col.scrollHeight);
    } else if (document.body) {
      h = document.body.scrollHeight;
    }
    h = Math.ceil(h);
    if (h !== last) { last = h; post(h); }
  }
  window.addEventListener('resize', function () {
    var w = window.innerWidth, hh = window.innerHeight;
    if (w === vw && hh !== vh) {
      viewport = true;
      if (timer !== null) { clearTimeout(timer); }
      timer = setTimeout(function () { viewport = false; timer = null; }, 100);
    } else {
      viewport = false;
    }
    vw = w; vh = hh;
  });
  try {
    var ro = new ResizeObserver(function () { measure(); });
    ro.observe(document.documentElement);
    var col = document.getElementById('malachi-column');
    if (col) { ro.observe(col); }
  } catch (e) {}
  document.addEventListener('load', function () { measure(); }, true);
  window.addEventListener('load', function () { measure(); });
  measure();
})();`

// validHeight is a reported height as Go takes it: a finite number that is
// not negative, capped at maxReportedCSS; false for anything else.
func validHeight(h float64) (float64, bool) {
	if math.IsNaN(h) || math.IsInf(h, 0) || h < 0 {
		return 0, false
	}
	return math.Min(h, maxReportedCSS), true
}

// readSize reads a message of sizeScript: an object whose h is the height
// (validHeight) and whose v, when a boolean, says the report followed a
// change of the view's height alone. Anything else is dropped (false).
func readSize(v *javascriptcore.Value) (css float64, viewport bool, ok bool) {
	if v == nil || !v.IsObject() {
		return 0, false, false
	}
	h := v.ObjectGetProperty("h")
	if h == nil || !h.IsNumber() {
		return 0, false, false
	}
	css, ok = validHeight(h.ToDouble())
	if !ok {
		return 0, false, false
	}
	if f := v.ObjectGetProperty("v"); f != nil && f.IsBoolean() {
		viewport = f.ToBoolean()
	}
	return css, viewport, true
}
