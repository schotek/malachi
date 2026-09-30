// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package htmlview

import (
	"math"
	"strings"
	"testing"
)

// A reported height is taken only as a finite number that is not
// negative, and capped.
func TestValidHeight(t *testing.T) {
	for _, c := range []struct {
		in   float64
		want float64
		ok   bool
	}{
		{0, 0, true},
		{123.5, 123.5, true},
		{maxReportedCSS, maxReportedCSS, true},
		{maxReportedCSS * 4, maxReportedCSS, true},
		{math.MaxFloat64, maxReportedCSS, true},
		{-1, 0, false},
		{math.Copysign(0, -1), 0, true},
		{math.NaN(), 0, false},
		{math.Inf(1), 0, false},
		{math.Inf(-1), 0, false},
	} {
		got, ok := validHeight(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("validHeight(%v) = %v, %v; want %v, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

// The card's document is the viewer's with the column's padding cut to
// the card's: the same policy, the same column, one body inside it.
func TestCompactDocument(t *testing.T) {
	body := `<p class="x">hello</p>`
	full, compact := Document(body), CompactDocument(body)
	meta := `<meta http-equiv="Content-Security-Policy" content="` + CSP + `">`
	for name, doc := range map[string]string{"full": full, "compact": compact} {
		if !strings.Contains(doc, meta) {
			t.Errorf("%s: no CSP meta", name)
		}
		if strings.Count(doc, `<div id="malachi-column">`+body+`</div>`) != 1 {
			t.Errorf("%s: the body is not in the column once", name)
		}
		if strings.Contains(strings.ToLower(doc), "<script") {
			t.Errorf("%s: a script in the document", name)
		}
	}
	if !strings.Contains(full, "padding: 12px 24px 24px !important") {
		t.Error("the viewer's column padding changed")
	}
	if !strings.Contains(compact, "padding: 4px 14px !important") || strings.Contains(compact, "12px 24px 24px") {
		t.Error("the card's column padding")
	}
	if strings.Replace(compact, "4px 14px", "12px 24px 24px", 1) != full {
		t.Error("the two documents differ in more than the padding")
	}
}

// The script reports through its own handler only, and reads the column.
func TestSizeScript(t *testing.T) {
	if !strings.Contains(sizeScript, "window.webkit.messageHandlers."+sizeHandler+".postMessage") {
		t.Error("the script does not post to its handler")
	}
	if strings.Count(sizeScript, "postMessage") != 1 {
		t.Error("the script posts more than the height")
	}
	if !strings.Contains(sizeScript, "getElementById('malachi-column')") || !strings.Contains(sizeScript, "ResizeObserver") {
		t.Error("the script does not observe the column")
	}
}
