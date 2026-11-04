// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package mime

import (
	"net/url"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

const (
	// maxCIDRefs caps the identifiers CIDReferences collects; past it the
	// result is incomplete.
	maxCIDRefs = 1024
	// maxCIDScanBytes: longer HTML is not looked at, the result is
	// incomplete. Parse keeps MaxTextBytes of it and the sanitiser refuses
	// far less than this.
	maxCIDScanBytes = 8 << 20
	// maxCIDBytes bounds what follows "cid:" in one reference. Nothing
	// longer can name a Content-ID: Parse keeps MaxFieldBytes of one (three
	// times that percent-encoded) and the sanitiser drops a URL over 2048
	// bytes.
	maxCIDBytes = 8 << 10
)

// CIDReferences lists the Content-IDs an HTML body may reference, in the
// form NormalizeCID gives them. It is a superset of what internal/sanitize
// resolves, which is the src of an <img> naming a cid: URL.
//
// The HTML is parsed the way the sanitiser parses it (x/net/html with
// scripting off, so <noscript> holds markup, not text), and then every
// attribute of every element counts, not only img src: a value that is a
// cid: URL after the normalisation a browser applies (entities decoded by
// the parser, surrounding spaces trimmed, tabs and newlines removed, the
// scheme in any case) adds its whole remainder, percent-decoded, without
// surrounding spaces and angle brackets; and every "cid:" inside an
// attribute value or a text node (a <style> sheet, a srcset, a CSS url())
// adds what follows it up to whitespace or one of "'),;<>. Comments are
// skipped: the sanitiser drops them, and Outlook's conditional comments
// would otherwise keep the pictures of their VML copy.
//
// complete is false when the list may miss something the sanitiser
// resolves: more than maxCIDRefs identifiers, HTML over maxCIDScanBytes, a
// parse failure, or a malachi-cid: URL, the view's own scheme, which names a
// part by number instead of by Content-ID. The caller then counts every part
// with a Content-ID as referenced.
func CIDReferences(rawHTML string) (refs map[string]bool, complete bool) {
	refs = make(map[string]bool)
	if len(rawHTML) > maxCIDScanBytes {
		return refs, false
	}
	doc, err := html.ParseWithOptions(strings.NewReader(rawHTML), html.ParseOptionEnableScripting(false))
	if err != nil {
		return refs, false
	}
	c := &cidCollector{refs: refs, complete: true}
	for n := doc; n != nil && !c.full; n = nextNode(doc, n) {
		switch n.Type {
		case html.ElementNode:
			for _, a := range n.Attr {
				c.attribute(a.Val)
			}
		case html.TextNode:
			c.embedded(n.Data)
		}
	}
	return refs, c.complete
}

// NormalizeCID is the form CIDReferences reports an identifier in, and the
// form an Attachment.ContentID must be brought into before it is looked up
// there: whitespace and control characters removed, invalid UTF-8 replaced,
// lower-cased, surrounding angle brackets trimmed. An identifier the
// sanitiser matches exactly always normalises alike; lower-casing only lets
// more of them match, which errs on the side of keeping a part.
func NormalizeCID(id string) string {
	var b strings.Builder
	b.Grow(len(id))
	for _, r := range id { // an invalid byte arrives as utf8.RuneError
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return strings.Trim(b.String(), "<>")
}

// cidCollector gathers the references of one document.
type cidCollector struct {
	refs     map[string]bool
	complete bool
	full     bool // the cap was hit; the walk stops
}

// attribute takes one attribute value: the value as a whole when it is a
// cid: URL (what the sanitiser resolves), then every cid: inside it.
func (c *cidCollector) attribute(v string) {
	if scheme, rest, ok := strings.Cut(urlForm(v), ":"); ok {
		switch {
		case strings.EqualFold(scheme, "cid"):
			c.add(rest)
		case strings.EqualFold(scheme, "malachi-cid"):
			c.complete = false
		}
	}
	c.embedded(v)
}

// embedded adds what follows every "cid:" (in any case) in s, up to
// whitespace or one of "'),;<>. The end of consecutive references is found
// once, so a string of "cid:cid:cid:…" stays linear.
func (c *cidCollector) embedded(s string) {
	end := -1 // the first terminator at or after the last start
	for i := 0; !c.full; {
		j := indexCID(s, i)
		if j < 0 {
			return
		}
		start := j + len("cid:")
		if start > end {
			end = len(s)
			if k := strings.IndexAny(s[start:], " \t\n\r\f\v\"'),;<>"); k >= 0 {
				end = start + k
			}
		}
		c.add(s[start:end])
		i = start
	}
}

// add records the identifier a cid: URL names: percent-decoded (RFC 2392)
// and without surrounding spaces and angle brackets, exactly as the
// sanitiser's contentID derives it, then normalised.
func (c *cidCollector) add(id string) {
	if id == "" || len(id) > maxCIDBytes {
		return
	}
	if dec, err := url.PathUnescape(id); err == nil {
		id = dec
	}
	id = NormalizeCID(strings.Trim(strings.TrimSpace(id), "<>"))
	if id == "" || c.refs[id] {
		return
	}
	if len(c.refs) >= maxCIDRefs {
		c.complete = false
		c.full = true
		return
	}
	c.refs[id] = true
}

// urlForm normalises an attribute value the way the sanitiser's
// classifyURL (and a browser's URL parser) does before looking at the
// scheme: C0 controls and spaces trimmed from both ends, tab, LF and CR
// removed everywhere.
func urlForm(v string) string {
	v = strings.TrimFunc(v, func(r rune) bool { return r <= ' ' })
	if !strings.ContainsAny(v, "\t\n\r") {
		return v
	}
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, v)
}

// indexCID returns the index of the first "cid:", in any case, at or after
// from, or -1.
func indexCID(s string, from int) int {
	for i := from + 3; i < len(s); i++ {
		k := strings.IndexByte(s[i:], ':')
		if k < 0 {
			return -1
		}
		i += k
		// ORing in 0x20 lower-cases a letter and maps no other byte onto
		// 'c', 'i' or 'd'.
		if s[i-3]|0x20 == 'c' && s[i-2]|0x20 == 'i' && s[i-1]|0x20 == 'd' {
			return i - 3
		}
	}
	return -1
}

// nextNode returns the node after n in document order below root, or nil.
// The walk needs no recursion, however deep the tree.
func nextNode(root, n *html.Node) *html.Node {
	if n.FirstChild != nil {
		return n.FirstChild
	}
	for n != nil && n != root {
		if n.NextSibling != nil {
			return n.NextSibling
		}
		n = n.Parent
	}
	return nil
}
