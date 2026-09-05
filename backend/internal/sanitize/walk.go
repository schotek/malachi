// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package sanitize

import (
	"encoding/base64"
	"errors"
	"strings"

	"golang.org/x/net/html"

	"github.com/schotek/malachi/backend/pkg/api"
)

// walker turns the parsed tree into a new one made only of allowed nodes.
// It never modifies the input tree and never copies an attribute it has not
// looked at.
type walker struct {
	in   Input
	view bool

	nodes    int
	cssRules int
	blocked  api.BlockedContent
	links    []api.Link
	cids     map[string]bool
	styles   []*html.Node // hoisted <style> elements (view mode)
	inlined  int          // bytes of data: URIs this walker inlined; outside the output cap
	err      error
}

func (w *walker) fail(msg string) {
	if w.err == nil {
		w.err = errors.New(msg)
	}
}

// children converts n's children in order.
func (w *walker) children(n *html.Node, depth int) []*html.Node {
	var out []*html.Node
	for c := n.FirstChild; c != nil && w.err == nil; c = c.NextSibling {
		out = append(out, w.convert(c, depth+1)...)
	}
	return out
}

// convert returns the replacement for n: nothing, one element, or (when n
// is unwrapped) its converted children.
func (w *walker) convert(n *html.Node, depth int) []*html.Node {
	if w.err != nil {
		return nil
	}
	w.nodes++
	if w.nodes > maxNodes {
		w.fail("too many nodes")
		return nil
	}
	if depth > maxDepth {
		w.fail("nesting too deep")
		return nil
	}
	switch n.Type {
	case html.TextNode:
		return []*html.Node{{Type: html.TextNode, Data: n.Data}}
	case html.ElementNode:
		return w.element(n, depth)
	}
	return nil // comments and doctypes
}

func (w *walker) element(n *html.Node, depth int) []*html.Node {
	name := n.Data
	if cat, ok := droppedElems[name]; ok {
		w.countDropped(cat, n)
		return nil
	}
	if n.Namespace != "" {
		return nil // foreign content outside svg/math roots: nothing we know
	}
	switch name {
	case "html", "head", "body":
		return w.children(n, depth)
	case "style":
		if w.view {
			w.hoistStyle(n)
		}
		return nil
	}
	if !allowedElems[name] {
		return w.children(n, depth) // unknown: keep what it contains
	}
	if len(n.Attr) > maxAttrs {
		w.fail("too many attributes")
		return nil
	}
	attrs, keep := w.attrs(name, n)
	if !keep {
		return nil
	}
	e := &html.Node{Type: html.ElementNode, Data: name, DataAtom: n.DataAtom, Attr: attrs}
	for _, c := range w.children(n, depth) {
		e.AppendChild(c)
	}
	return []*html.Node{e}
}

func (w *walker) countDropped(cat dropCategory, n *html.Node) {
	switch cat {
	case dropScript:
		w.blocked.Scripts++
	case dropFrame:
		w.blocked.EmbeddedFrames++
	case dropForm:
		w.blocked.Forms++
	case dropRemoteStyle:
		if attrValue(n, "href") != "" {
			w.blocked.RemoteStyles++
		}
	case dropNavigation:
		if n.Data == "base" || strings.EqualFold(attrValue(n, "http-equiv"), "refresh") {
			w.blocked.DangerousURLs++
		}
	case dropMedia:
		if attrValue(n, "src") != "" || attrValue(n, "poster") != "" {
			w.blocked.RemoteImages++
		}
	}
}

// hoistStyle filters a <style> element's text and queues it for the top of
// the output. The parser hands the CSS over as one text node.
func (w *walker) hoistStyle(n *html.Node) {
	var src strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode {
			src.WriteString(c.Data)
		}
	}
	css := filterStylesheet(src.String(), &w.blocked, &w.cssRules)
	if css == "" {
		return
	}
	s := &html.Node{Type: html.ElementNode, Data: "style", DataAtom: n.DataAtom}
	s.AppendChild(&html.Node{Type: html.TextNode, Data: css})
	w.styles = append(w.styles, s)
}

// attrs filters n's attributes for an allowed element and reports whether
// the element is worth keeping at all (an image without a usable source is
// not). The first occurrence of a duplicated attribute wins, so a second
// src can never override the one that was checked.
func (w *walker) attrs(name string, n *html.Node) ([]html.Attribute, bool) {
	var out []html.Attribute
	seen := make(map[string]bool, len(n.Attr))
	tiny := name == "img" && isTrackingPixel(n)
	hasSrc, hasHref := false, false
	for _, a := range n.Attr {
		if a.Namespace != "" {
			continue
		}
		key := strings.ToLower(a.Key)
		if seen[key] {
			continue
		}
		seen[key] = true
		switch {
		case strings.HasPrefix(key, "on"):
			w.blocked.EventHandlers++
		case key == "style":
			if v := filterDeclarations(a.Val, &w.blocked); v != "" {
				out = append(out, html.Attribute{Key: "style", Val: v})
			}
		case key == "href" && name == "a":
			href, ok := w.linkHref(a.Val)
			if ok && len(w.links) < maxLinks {
				w.links = append(w.links, api.Link{Text: linkText(n), Href: href})
				out = append(out, html.Attribute{Key: "href", Val: href})
				hasHref = true
			}
		case key == "src" && name == "img":
			if src, ok := w.imageSrc(a.Val, tiny); ok {
				out = append(out, html.Attribute{Key: "src", Val: src})
				hasSrc = true
			}
		case remoteAttrs[key]:
			w.blocked.RemoteImages++
		case silentAttrs[key] || strings.HasPrefix(key, "data-") || strings.HasPrefix(key, "xlink:") || strings.HasPrefix(key, "xml:"):
		case globalAttrs[key] || elemAttrs[name][key] || strings.HasPrefix(key, "aria-"):
			if v, ok := cleanAttrValue(key, a.Val); ok {
				out = append(out, html.Attribute{Key: key, Val: v})
			}
		}
	}
	if name == "img" && !hasSrc {
		return nil, false
	}
	if name == "a" && hasHref && w.view {
		out = append(out, html.Attribute{Key: "rel", Val: "noopener noreferrer"})
	}
	return out, true
}

// linkHref keeps http(s): and mailto: targets. A relative reference is not
// dangerous, just useless without a base, so it goes without a counter.
func (w *walker) linkHref(val string) (string, bool) {
	u, class := classifyURL(val)
	switch class {
	case urlHTTP, urlHTTPS, urlMailto:
		return u, true
	case urlRelative:
		return "", false
	}
	w.blocked.DangerousURLs++
	return "", false
}

// imageSrc decides an <img src>: a known cid: part is rewritten for the
// view (or kept for a draft), an https: image follows the remote policy,
// everything else goes. A tracking pixel is never fetched, whatever the
// policy: it carries no picture, only a beacon.
func (w *walker) imageSrc(val string, tiny bool) (string, bool) {
	u, class := classifyURL(val)
	switch class {
	case urlCID:
		id := contentID(u)
		target, ok := w.in.KnownCIDs[id]
		if !ok || id == "" {
			w.blocked.DangerousURLs++
			return "", false
		}
		w.cids[id] = true
		if w.view {
			return "malachi-cid:" + target, true
		}
		return "cid:" + id, true
	case urlLocal:
		// The view's own scheme, as it appears when sanitised output is
		// sanitised again. It may only name a part of this message: a mail
		// that writes malachi-cid: itself is trying to reach another one's.
		if w.view {
			path := strings.TrimPrefix(u, "malachi-cid:")
			for id, target := range w.in.KnownCIDs {
				if target == path {
					w.cids[id] = true
					return u, true
				}
			}
		}
		w.blocked.DangerousURLs++
		return "", false
	case urlHTTPS:
		if tiny {
			w.blocked.TrackingPixels++
			return "", false
		}
		if w.in.Policy != api.RemoteAllow {
			w.blocked.RemoteImages++
			return "", false
		}
		if w.in.RemoteImage == nil {
			return u, true
		}
		mt, data, ok := w.in.RemoteImage(u)
		if !ok || len(data) == 0 || !safeImageType(mt) {
			w.blocked.RemoteImages++
			return "", false
		}
		d := "data:" + strings.ToLower(strings.TrimSpace(mt)) + ";base64," + base64.StdEncoding.EncodeToString(data)
		w.inlined += len(d)
		return d, true
	case urlHTTP, urlRelative:
		if tiny {
			w.blocked.TrackingPixels++
		} else {
			w.blocked.RemoteImages++
		}
		return "", false
	}
	// data:, javascript:, mailto:, unknown schemes, invalid values
	w.blocked.DangerousURLs++
	return "", false
}

// isTrackingPixel is the heuristic for a beacon: an image that is at most
// two pixels wide or high, or hidden outright.
func isTrackingPixel(n *html.Node) bool {
	tiny := func(v string) bool {
		v = strings.TrimSuffix(strings.TrimSpace(strings.ToLower(v)), "px")
		return v == "0" || v == "1" || v == "2"
	}
	for _, a := range n.Attr {
		if a.Namespace != "" {
			continue
		}
		switch strings.ToLower(a.Key) {
		case "width", "height":
			if tiny(a.Val) {
				return true
			}
		case "hidden":
			return true
		case "style":
			for _, d := range parseDeclarations(stripComments(a.Val)) {
				v := strings.ToLower(d.val)
				switch d.prop {
				case "width", "height", "max-width", "max-height":
					if tiny(v) {
						return true
					}
				case "display":
					if v == "none" {
						return true
					}
				case "visibility":
					if v == "hidden" {
						return true
					}
				}
			}
		}
	}
	return false
}

// bodyWrapper carries a <body>'s presentational attributes over to a <div>
// around the content, so a mail that paints its background on the body
// still looks as designed. Nothing is wrapped when the body has nothing to
// say, which also keeps sanitising the output a no-op.
func (w *walker) bodyWrapper(body *html.Node) *html.Node {
	if body == nil {
		return nil
	}
	var css []string
	for _, a := range body.Attr {
		if a.Namespace != "" {
			continue
		}
		key := strings.ToLower(a.Key)
		if strings.HasPrefix(key, "on") {
			w.blocked.EventHandlers++
			continue
		}
		switch key {
		case "bgcolor":
			if isColorValue(a.Val) {
				css = append(css, "background-color: "+strings.TrimSpace(a.Val))
			}
		case "text":
			if isColorValue(a.Val) {
				css = append(css, "color: "+strings.TrimSpace(a.Val))
			}
		case "style":
			if v := filterDeclarations(a.Val, &w.blocked); v != "" {
				css = append(css, v)
			}
		}
	}
	if len(css) == 0 {
		return nil
	}
	style := filterDeclarations(strings.Join(css, "; "), &w.blocked)
	if style == "" {
		return nil
	}
	return &html.Node{Type: html.ElementNode, Data: "div", Attr: []html.Attribute{
		{Key: "class", Val: "malachi-body"},
		{Key: "style", Val: style},
	}}
}
