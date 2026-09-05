// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package sanitize turns hostile e-mail HTML into something a
// JavaScript-disabled webview can render safely. This is the single most
// security-critical package in the backend; see docs/security.md.
//
// Contract: message.body returns ONLY the output of this package. There is
// no code path that returns stored HTML unsanitised, no debug flag, no
// test-only shortcut. On any error the output is empty: the input never
// leaks through, not even partially.
//
// How it works: the input is parsed to a tree with golang.org/x/net/html
// (the tolerant HTML5 parser, scripting off so <noscript> content is real
// elements), a new tree is built from allow-listed elements and attributes
// only (policy.go, walk.go), URL-valued attributes are classified after the
// normalisation a browser would apply (url.go), CSS in style="" and <style>
// is re-emitted through a property allow-list by a filter that never keeps
// what it does not understand (css.go), and the new tree is serialised with
// html.Render — never by regex over the source. The plain-text form is
// rendered from that same tree (text.go).
//
// What that gives:
//   - no <script>, <iframe>, <object>, <embed>, <applet>, <form> and
//     controls, <meta>, <link>, <base>, <svg>, <math>, media elements;
//   - no on* attribute; URLs restricted to http(s): and mailto: for links
//     and to cid: (rewritten to the view's local scheme, only for parts the
//     message has) plus policy-dependent https: for images; javascript:,
//     vbscript:, data: and unknown schemes go, including obfuscated forms;
//   - remote references (img src, srcset, CSS url(), @import, @font-face,
//     background attributes, media) are removed under RemoteBlock and
//     counted in api.BlockedContent; under RemoteAllow the caller's
//     RemoteImage hook fetches https: images and they are inlined as data:
//     URIs, so the view itself never touches the network. A tracking pixel
//     (≤ 2 px or hidden) is never fetched under any policy;
//   - CSS through a property allow-list: no url(), expression(), @import,
//     @font-face, position: fixed/absolute, hidden text, content:;
//   - links keep only http(s): and mailto:, get rel="noopener noreferrer"
//     in the view, lose target, and are exported in api.Link;
//   - <style> elements are filtered and hoisted to the top; a <body>'s
//     presentational attributes move to a wrapping <div class="malachi-body">;
//   - caps on input size, output size, nesting depth, node count, attribute
//     count and CSS rules; a cap breach is an error, not a truncation;
//   - sanitising the output again (under RemoteBlock) is the identity.
//
// The library decision (docs/architecture.md §7): own code over
// golang.org/x/net/html. E-mail depends on <style> blocks and inline CSS
// that general-purpose sanitisers drop, and the remote-content policy, the
// cid: rewrite and the blocked-content report are specific to this program.
package sanitize

import (
	"bytes"
	"errors"
	"sort"
	"strings"

	"golang.org/x/net/html"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Version identifies the current ruleset. Bump on any behavioural change so
// cached bodies can be invalidated.
const Version = "1"

// Caps. A breach fails the whole body: partial output would be a guess about
// which part was safe.
const (
	// DefaultMaxOutputSize bounds the serialised HTML when the caller does
	// not say otherwise. Inlined remote images do not count against it.
	DefaultMaxOutputSize = 4 << 20

	maxInputBytes = 2 << 20
	maxDepth      = 200
	maxNodes      = 100_000
	maxAttrs      = 64
	maxAttrValue  = 1024
	maxURLLength  = 2048
	maxLinks      = 1000
	maxLinkText   = 200
)

// Mode selects which direction the HTML is travelling.
type Mode int

const (
	// ModeView: incoming mail for display. cid: references are rewritten to
	// the webview's local scheme; the remote policy comes from the caller.
	ModeView Mode = iota
	// ModeCompose: HTML written in the editor, on its way into a draft.
	// Policy must be RemoteBlock (anything else is an error); cid: is kept
	// verbatim only when present in KnownCIDs; data: URLs are removed;
	// <style> blocks are dropped, only style="" survives.
	ModeCompose
)

// Input is what message.body (ModeView) or draft.save (ModeCompose) hands
// over.
type Input struct {
	HTML   string
	Mode   Mode
	Policy api.RemoteContentPolicy
	// KnownCIDs maps a Content-ID to what a surviving cid: reference is
	// rewritten to: in the view the "<accountId>/<messageId>/<partId>"
	// path behind malachi-cid:, in a draft the attachment ID (the reference
	// itself is kept as cid:<contentId>).
	KnownCIDs     map[string]string
	MaxOutputSize int // bytes; 0 = DefaultMaxOutputSize
	// RemoteImage fetches one https: image under RemoteAllow and returns its
	// media type and bytes for inlining as a data: URI; ok = false drops the
	// image. nil leaves https: image references in place.
	RemoteImage func(url string) (mediaType string, data []byte, ok bool)
	// InlineCID resolves one cid: reference to picture bytes when the
	// caller cannot serve parts by URL (an attached message shown from its
	// own bytes, message.embedded): the picture is inlined as a data: URI
	// like a fetched remote image, and the Content-ID is reported in CIDs.
	// ok = false drops the reference. nil (the usual case) leaves cid: to
	// KnownCIDs. ModeView only.
	InlineCID func(contentID string) (mediaType string, data []byte, ok bool)
}

// Output is the only form of HTML that may cross the API.
type Output struct {
	HTML string
	// Text is the plain-text rendering of the sanitised tree: the text
	// alternative of a composed message and the "text derived from html" of
	// message.body.
	Text    string
	Blocked api.BlockedContent
	Links   []api.Link
	// CIDs lists the Content-IDs whose cid: references survived (ModeCompose
	// uses it to keep only referenced inline attachments bound).
	CIDs    []string
	Version string
}

// Sanitize runs the ruleset. It fails closed: on error the Output carries
// nothing but the version.
func Sanitize(in Input) (Output, error) {
	out, err := sanitize(in)
	if err != nil {
		return Output{Version: Version}, api.NewError(api.CodeSanitizeFailed, "sanitiser: %v", err)
	}
	return out, nil
}

func sanitize(in Input) (Output, error) {
	switch in.Mode {
	case ModeView:
		if in.Policy != api.RemoteBlock && in.Policy != api.RemoteAllow {
			return Output{}, errors.New("unsupported remote content policy")
		}
	case ModeCompose:
		if in.Policy != api.RemoteBlock {
			return Output{}, errors.New("compose mode requires the block policy")
		}
	default:
		return Output{}, errors.New("unknown mode")
	}
	if len(in.HTML) > maxInputBytes {
		return Output{}, errors.New("input too large")
	}
	max := in.MaxOutputSize
	if max <= 0 {
		max = DefaultMaxOutputSize
	}

	doc, err := html.ParseWithOptions(strings.NewReader(in.HTML), html.ParseOptionEnableScripting(false))
	if err != nil {
		return Output{}, err
	}
	w := &walker{in: in, view: in.Mode == ModeView, cids: make(map[string]bool)}
	head, body := headAndBody(doc)
	if head != nil {
		// Nothing a head holds is content, but its <style> elements are
		// hoisted and its <link>, <meta> and <base> are counted.
		w.children(head, 0)
	}
	var kids []*html.Node
	if body != nil {
		kids = trimLeadingSpace(w.children(body, 0))
	}
	if w.err != nil {
		return Output{}, w.err
	}

	root := &html.Node{Type: html.DocumentNode}
	for _, s := range w.styles {
		root.AppendChild(s)
	}
	if wrap := w.bodyWrapper(body); wrap != nil {
		for _, k := range kids {
			wrap.AppendChild(k)
		}
		root.AppendChild(wrap)
	} else {
		for _, k := range kids {
			root.AppendChild(k)
		}
	}

	var buf bytes.Buffer
	if err := html.Render(&buf, root); err != nil {
		return Output{}, err
	}
	if buf.Len()-w.inlined > max {
		return Output{}, errors.New("output too large")
	}

	links := w.links
	if links == nil {
		links = []api.Link{}
	}
	cids := make([]string, 0, len(w.cids))
	for id := range w.cids {
		cids = append(cids, id)
	}
	sort.Strings(cids)
	return Output{
		HTML:    buf.String(),
		Text:    renderText(root, max),
		Blocked: w.blocked,
		Links:   links,
		CIDs:    cids,
		Version: Version,
	}, nil
}

// trimLeadingSpace removes the whitespace before the first real content:
// whole whitespace-only text nodes and the leading whitespace of the first
// text node with content. The parser discards exactly that whitespace when
// it precedes the body (it is dropped before the body element exists), so
// keeping it would make sanitising the output differ from the output. It is
// invisible either way: leading whitespace collapses in rendering.
func trimLeadingSpace(nodes []*html.Node) []*html.Node {
	for len(nodes) > 0 && nodes[0].Type == html.TextNode {
		trimmed := strings.TrimLeft(nodes[0].Data, " \t\n\r\f")
		if trimmed != "" {
			nodes[0].Data = trimmed
			break
		}
		nodes = nodes[1:]
	}
	return nodes
}

// headAndBody finds the two children the parser always gives an <html>
// element.
func headAndBody(doc *html.Node) (head, body *html.Node) {
	for n := doc.FirstChild; n != nil; n = n.NextSibling {
		if n.Type == html.ElementNode && n.Data == "html" {
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type != html.ElementNode {
					continue
				}
				switch c.Data {
				case "head":
					head = c
				case "body":
					body = c
				}
			}
		}
	}
	return head, body
}
