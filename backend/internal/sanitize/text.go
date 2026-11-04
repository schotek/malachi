// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package sanitize

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// The plain-text rendering of the sanitised tree. It is what a composed
// message carries as its text/plain alternative, so it keeps link targets
// and image descriptions rather than dropping them.

// blockNewlines is how many line breaks an element forces before and after
// itself: two for paragraph-like elements, one for line-like ones. <pre>
// and <blockquote> are paragraph-like too but need bookkeeping of their
// own, so walk handles them before consulting this.
var blockNewlines = map[string]int{
	"p": 2, "h1": 2, "h2": 2, "h3": 2, "h4": 2, "h5": 2, "h6": 2,
	"table": 2, "ul": 2, "ol": 2, "dl": 2, "hr": 2,
	"section": 2, "article": 2, "header": 2, "footer": 2, "figure": 2,
	"address": 2, "center": 2,
	"div": 1, "li": 1, "tr": 1, "dd": 1, "dt": 1, "caption": 1, "thead": 1,
	"tbody": 1, "tfoot": 1, "nav": 1, "aside": 1, "main": 1, "figcaption": 1,
	"summary": 1, "details": 1,
}

// Line breaks are pending, not written: they go out, with the quote marks
// of the lines they open, right before the next character that needs them.
// That keeps trailing breaks (and trailing "> " lines) out of the output
// and lets a quote that closes before the next paragraph leave a plain
// blank line behind it.
type textRenderer struct {
	b       strings.Builder
	max     int
	started bool // something other than whitespace has been written
	nl      int  // pending line breaks
	nlQuote int  // the lowest quote depth a pending break was asked at
	space   bool // a collapsed space is pending
	tab     bool // b ends with a cell separator
	pre     int
	quote   int // <blockquote> depth: lines inside are prefixed with "> " each
}

// renderText walks the tree that is about to be serialised, so it can only
// ever see what the sanitiser kept.
func renderText(root *html.Node, max int) string {
	r := &textRenderer{max: max}
	r.walk(root)
	s := strings.TrimRight(r.b.String(), " \t\n")
	if max > 0 && len(s) > max {
		s = s[:max]
		for len(s) > 0 && !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	return s
}

func (r *textRenderer) full() bool { return r.max > 0 && r.b.Len() >= r.max }

func (r *textRenderer) walk(n *html.Node) {
	if r.full() {
		return
	}
	switch n.Type {
	case html.TextNode:
		r.text(n.Data)
		return
	case html.ElementNode:
	default:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			r.walk(c)
		}
		return
	}
	name := n.Data
	switch name {
	case "style":
		return
	case "br":
		r.newline(1)
		return
	case "img":
		if alt := strings.TrimSpace(attrValue(n, "alt")); alt != "" {
			r.text("[" + alt + "]")
		}
		return
	case "td", "th":
		r.cell()
	case "li":
		r.newline(1)
		r.text("- ")
	case "pre":
		r.pre++
		r.newline(2)
	case "blockquote":
		// The mail convention: quoted lines carry "> ", nested ones "> > ".
		r.newline(2)
		r.quote++
	default:
		if k, ok := blockNewlines[name]; ok {
			r.newline(k)
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		r.walk(c)
	}
	switch name {
	case "a":
		// The <target> convention of plain-text mail; a space follows so the
		// next run of text does not glue onto the bracket.
		if href := attrValue(n, "href"); href != "" && !linkTextIsHref(n, href) {
			r.text(" <" + href + ">")
			r.space = r.started
		}
	case "pre":
		if r.pre > 0 {
			r.pre--
		}
		r.newline(2)
	case "blockquote":
		r.quote--
		r.newline(2)
	case "td", "th":
	default:
		if k, ok := blockNewlines[name]; ok {
			r.newline(k)
		}
	}
}

// linkTextIsHref reports whether the anchor's text already is its target,
// give or take the scheme, so "https://example.org" is not printed twice.
func linkTextIsHref(n *html.Node, href string) bool {
	strip := func(s string) string {
		s = strings.TrimSpace(strings.ToLower(s))
		for _, p := range []string{"https://", "http://", "mailto:"} {
			s = strings.TrimPrefix(s, p)
		}
		return strings.TrimSuffix(s, "/")
	}
	return strip(linkText(n)) == strip(href)
}

// newline asks for at least n line breaks before the next text (at most
// two are ever emitted in a row).
func (r *textRenderer) newline(n int) {
	if !r.started {
		return
	}
	r.space, r.tab = false, false
	if r.nl == 0 || r.quote < r.nlQuote {
		r.nlQuote = r.quote
	}
	if r.nl < n {
		r.nl = n
	}
}

// pendNewline adds one line break of preformatted content, which is not
// collapsed with its neighbours.
func (r *textRenderer) pendNewline() {
	if r.nl == 0 || r.quote < r.nlQuote {
		r.nlQuote = r.quote
	}
	r.nl++
	r.tab = false
}

// flush writes the pending line breaks and the quote marks of the line
// that is about to receive text. A blank line between two quoted lines
// carries the bare mark of the shallower of the depths at its two ends,
// so a blank line after a quote that closed stays plain; a quote's first
// line follows what came before it without a blank line, the way "X
// wrote:" sits right on top of the quoted text.
func (r *textRenderer) flush() {
	if r.nl > 0 {
		n := r.nl
		if r.quote > r.nlQuote {
			n = 1
		}
		r.b.WriteByte('\n')
		blank := strings.TrimRight(strings.Repeat("> ", min(r.nlQuote, r.quote)), " ")
		for i := 1; i < n; i++ {
			r.b.WriteString(blank)
			r.b.WriteByte('\n')
		}
	}
	if (r.nl > 0 || !r.started) && r.quote > 0 {
		r.b.WriteString(strings.Repeat("> ", r.quote))
	}
	r.nl = 0
}

// cell separates table cells with a tab.
func (r *textRenderer) cell() {
	if !r.started || r.nl > 0 || r.tab {
		return
	}
	r.b.WriteByte('\t')
	r.space, r.tab = false, true
}

// text appends s, collapsing whitespace outside <pre>. Control characters
// count as whitespace; nothing here can produce a tag, the output is text.
func (r *textRenderer) text(s string) {
	for _, c := range s {
		if r.full() {
			return
		}
		switch {
		case r.pre > 0 && c == '\n':
			if r.started {
				r.pendNewline()
			}
		case r.pre > 0 && (c == ' ' || c == '\t'):
			// Preformatted: indentation is content, except before anything
			// has been written at all.
			if r.started {
				r.flush()
				r.b.WriteRune(c)
				r.tab = false
			}
		case unicode.IsSpace(c) || unicode.IsControl(c):
			if r.started {
				r.space = true
			}
		default:
			if r.space && r.nl == 0 && !r.tab {
				r.b.WriteByte(' ')
			}
			r.space = false
			if c == utf8.RuneError {
				c = '�'
			}
			r.flush()
			r.b.WriteRune(c)
			r.started, r.tab = true, false
		}
	}
}

// linkText is the anchor's visible text, whitespace collapsed and capped,
// for api.Link and for the text rendering.
func linkText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if b.Len() > 4*maxLinkText {
			return
		}
		switch n.Type {
		case html.TextNode:
			b.WriteString(n.Data)
			b.WriteByte(' ')
		case html.ElementNode:
			if n.Data == "style" || n.Data == "script" {
				return
			}
			if alt := attrValue(n, "alt"); n.Data == "img" && alt != "" {
				b.WriteString(alt)
				b.WriteByte(' ')
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	s := strings.Join(strings.Fields(b.String()), " ")
	if utf8.RuneCountInString(s) > maxLinkText {
		runes := []rune(s)
		s = string(runes[:maxLinkText])
	}
	return s
}

// attrValue is the value of the element's attribute, "" when absent.
func attrValue(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Namespace == "" && strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}
