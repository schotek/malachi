// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package htmlview

import (
	"strings"
	"testing"
)

// The chat's document neutralises every background of the content and, in
// the dark style alone, forces the theme's text and link colours; the
// cards' document keeps its white paper and the message's colours.
func TestChatDocument(t *testing.T) {
	card := CompactDocument("<p>x</p>")
	light := ChatDocument("<p>x</p>", ChatStyle{})
	dark := ChatDocument("<p>x</p>", ChatStyle{Dark: true, Text: Colour{255, 255, 255}, Link: Colour{0x78, 0xae, 0xed}})
	if strings.Contains(card, chatNeutralCSS) || strings.Contains(card, chatFitCSS) || strings.Contains(card, "!important; }\na, a *") {
		t.Error("the card's document neutralises backgrounds")
	}
	if !strings.Contains(card, "background: #ffffff") || !strings.Contains(card, "padding: 4px 14px") {
		t.Error("the card's document lost its paper or padding")
	}
	for name, doc := range map[string]string{"light": light, "dark": dark} {
		if !strings.Contains(doc, chatNeutralCSS) {
			t.Errorf("%s: no neutralising sheet", name)
		}
		if !strings.Contains(doc, `content="`+CSP+`"`) || !strings.Contains(doc, "<p>x</p>") {
			t.Errorf("%s: no CSP or body", name)
		}
		if !strings.Contains(doc, "html, body { overflow: hidden !important; margin: 0 !important; }") ||
			!strings.Contains(doc, "#malachi-column { display: flow-root !important; }") {
			t.Errorf("%s: the document may scroll inside its view", name)
		}
		if !strings.Contains(doc, "padding: 0 !important") {
			t.Errorf("%s: the column keeps a padding", name)
		}
	}
	if strings.Contains(light, "* { color:") {
		t.Error("light: the message's colours are forced")
	}
	if !strings.Contains(dark, "* { color: #ffffff !important; }") || !strings.Contains(dark, "a, a * { color: #78aeed !important; }") {
		t.Errorf("dark: colours not forced:\n%s", dark)
	}
	// The neutralising sheet comes after the base sheet's white canvas.
	if i, j := strings.Index(light, "background: #ffffff"), strings.Index(light, chatNeutralCSS); i < 0 || j < i {
		t.Error("the neutralising sheet does not follow the base sheet")
	}
}
