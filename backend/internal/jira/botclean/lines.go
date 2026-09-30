// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package botclean

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// kind is how the line model treats an element.
type kind int

const (
	kindInline kind = iota // part of the line it is in (the default, custom elements too)
	kindBreak              // <br>: ends the line
	kindBlock              // a container whose start and end end lines
	kindOpaque             // a block left alone as a whole
	kindMedia              // an embedded object: content, never removed, no text
	kindHidden             // never shown: no text, no content
)

var kinds = map[atom.Atom]kind{
	atom.Br: kindBreak,

	atom.P: kindBlock, atom.Div: kindBlock,
	atom.H1: kindBlock, atom.H2: kindBlock, atom.H3: kindBlock,
	atom.H4: kindBlock, atom.H5: kindBlock, atom.H6: kindBlock,
	atom.Section: kindBlock, atom.Article: kindBlock, atom.Header: kindBlock,
	atom.Footer: kindBlock, atom.Main: kindBlock, atom.Aside: kindBlock,
	atom.Nav: kindBlock, atom.Address: kindBlock, atom.Center: kindBlock,
	atom.Hgroup: kindBlock, atom.Search: kindBlock,
	atom.Figure: kindBlock, atom.Figcaption: kindBlock,

	atom.Pre: kindOpaque, atom.Listing: kindOpaque, atom.Xmp: kindOpaque,
	atom.Plaintext: kindOpaque, atom.Blockquote: kindOpaque,
	atom.Table: kindOpaque, atom.Caption: kindOpaque, atom.Colgroup: kindOpaque,
	atom.Col: kindOpaque, atom.Thead: kindOpaque, atom.Tbody: kindOpaque,
	atom.Tfoot: kindOpaque, atom.Tr: kindOpaque, atom.Td: kindOpaque,
	atom.Th: kindOpaque,
	atom.Ul: kindOpaque, atom.Ol: kindOpaque, atom.Li: kindOpaque,
	atom.Dl: kindOpaque, atom.Dt: kindOpaque, atom.Dd: kindOpaque,
	atom.Menu: kindOpaque, atom.Dir: kindOpaque,
	atom.Form: kindOpaque, atom.Fieldset: kindOpaque, atom.Legend: kindOpaque,
	atom.Optgroup: kindOpaque, atom.Option: kindOpaque, atom.Datalist: kindOpaque,
	atom.Details: kindOpaque, atom.Summary: kindOpaque, atom.Dialog: kindOpaque,
	atom.Noscript: kindOpaque, atom.Marquee: kindOpaque, atom.Frameset: kindOpaque,
	atom.Hr: kindOpaque,

	atom.Img: kindMedia, atom.Picture: kindMedia, atom.Video: kindMedia,
	atom.Audio: kindMedia, atom.Canvas: kindMedia, atom.Iframe: kindMedia,
	atom.Object: kindMedia, atom.Embed: kindMedia, atom.Applet: kindMedia,
	atom.Frame: kindMedia, atom.Input: kindMedia, atom.Button: kindMedia,
	atom.Select: kindMedia, atom.Textarea: kindMedia, atom.Keygen: kindMedia,
	atom.Meter: kindMedia, atom.Progress: kindMedia,

	atom.Script: kindHidden, atom.Style: kindHidden, atom.Template: kindHidden,
	atom.Title: kindHidden, atom.Meta: kindHidden, atom.Link: kindHidden,
	atom.Base: kindHidden, atom.Noembed: kindHidden, atom.Noframes: kindHidden,
	atom.Head: kindHidden, atom.Param: kindHidden, atom.Source: kindHidden,
	atom.Track: kindHidden, atom.Area: kindHidden,
}

func kindOf(n *html.Node) kind {
	if n.Namespace != "" {
		return kindMedia // <svg>, <math>
	}
	return kinds[n.DataAtom] // zero: kindInline
}

// span is a stretch [from, to) of a text node's Data.
type span struct {
	node     *html.Node
	from, to int
}

// line is one line of a comment (see the package documentation), or one
// opaque block.
type line struct {
	spans  []span     // its text, in document order
	br     *html.Node // the <br> that ends it
	nl     span       // or the newline that ends it (node nil if none)
	group  int        // lines of one group are separated by <br> or newlines only
	opaque bool       // a block left alone as a whole
	media  bool       // holds an embedded object
	blank  bool       // has no visible text
	long   bool       // has more than maxLineBytes of it
	text   string     // its visible text unless long
}

// doc is a parsed comment cut into lines.
type doc struct {
	root    *html.Node // a <body> holding the parsed nodes
	lines   []*line    // in document order
	media   bool       // an embedded object is somewhere
	tooMany bool       // over maxLines: not cut into lines
}

// parse parses src as the content of a <body> and cuts it into lines. It
// refuses input the parser could inflate (see inflates); the parser refuses
// a tree nested more than 512 elements deep. Scripting is on, as in the
// parser's default: the content of <noscript> stays text, so rendering the
// tree again gives it back as it came.
func parse(src string) (*doc, error) {
	if inflates(src) {
		return nil, errInflates
	}
	body := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(strings.NewReader(src), body)
	if err != nil {
		return nil, fmt.Errorf("parse comment: %w", err)
	}
	d := &doc{root: &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}}
	for _, n := range nodes {
		d.root.AppendChild(n)
	}
	s := segmenter{d: d}
	s.flow(d.root, 0)
	s.boundary()
	if d.tooMany {
		d.lines = nil
	}
	return d, nil
}

// segmenter cuts a tree into lines.
type segmenter struct {
	d     *doc
	cur   *line
	group int
}

func (s *segmenter) current() *line {
	if s.cur == nil {
		s.cur = &line{group: s.group}
	}
	return s.cur
}

// end ends the current line, if there is one.
func (s *segmenter) end() {
	if s.cur == nil {
		return
	}
	s.cur.measure()
	s.add(s.cur)
	s.cur = nil
}

// boundary ends the current line and group.
func (s *segmenter) boundary() {
	s.end()
	s.group++
}

func (s *segmenter) add(l *line) {
	if len(s.d.lines) >= maxLines {
		s.d.tooMany = true
		return
	}
	s.d.lines = append(s.d.lines, l)
}

func (s *segmenter) flow(n *html.Node, depth int) {
	for c := n.FirstChild; c != nil && !s.d.tooMany; c = c.NextSibling {
		switch c.Type {
		case html.TextNode:
			s.text(c)
		case html.ElementNode:
			k := kindOf(c)
			if depth >= maxDepth && (k == kindInline || k == kindBlock) {
				k = kindOpaque
			}
			switch k {
			case kindBreak:
				s.current().br = c
				s.end()
			case kindInline:
				s.flow(c, depth+1)
			case kindBlock:
				s.boundary()
				s.flow(c, depth+1)
				s.boundary()
			case kindOpaque:
				s.boundary()
				text, media := inspect(c, depth+1)
				s.add(&line{group: s.group, opaque: true, blank: !text, media: media})
				s.d.media = s.d.media || media
				s.boundary()
			case kindMedia:
				s.current().media = true
				s.d.media = true
			}
		}
	}
}

// text adds a text node to the lines, ending one at each newline.
func (s *segmenter) text(n *html.Node) {
	data := n.Data
	from := 0
	for {
		i := strings.IndexByte(data[from:], '\n')
		if i < 0 {
			break
		}
		if i > 0 {
			l := s.current()
			l.spans = append(l.spans, span{n, from, from + i})
		}
		s.current().nl = span{n, from + i, from + i + 1}
		s.end()
		from += i + 1
	}
	if from < len(data) {
		l := s.current()
		l.spans = append(l.spans, span{n, from, len(data)})
	}
}

// measure sets text, blank and long.
func (l *line) measure() {
	t := texter{max: maxLineBytes}
	for _, sp := range l.spans {
		t.write(sp.node.Data[sp.from:sp.to])
		if t.long {
			break
		}
	}
	l.long = t.long
	l.blank = !t.long && t.b.Len() == 0
	if !t.long {
		l.text = t.b.String()
	}
}

// inspect reports whether n holds visible text and embedded objects.
func inspect(n *html.Node, depth int) (text, media bool) {
	if depth >= maxDepth {
		return true, false // assume content rather than look further
	}
	for c := n.FirstChild; c != nil && !(text && media); c = c.NextSibling {
		switch c.Type {
		case html.TextNode:
			text = text || !blankText(c.Data)
		case html.ElementNode:
			switch kindOf(c) {
			case kindMedia:
				media = true
			case kindHidden:
			default:
				t, m := inspect(c, depth+1)
				text, media = text || t, media || m
			}
		}
	}
	return text, media
}

// contentWithout reports whether anything would be left to read with the
// lines in remove removed.
func (d *doc) contentWithout(remove map[*line]bool) bool {
	if d.media {
		return true
	}
	for _, l := range d.lines {
		if !l.blank && !remove[l] {
			return true
		}
	}
	return false
}

// firstText is the first line with visible text that is not in remove.
func (d *doc) firstText(remove map[*line]bool) *line {
	for _, l := range d.lines {
		if !l.blank && !remove[l] {
			return l
		}
	}
	return nil
}

// cut removes the lines in remove, and the blank lines around them, from
// the tree and renders what is left.
func (d *doc) cut(remove map[*line]bool) (string, error) {
	e := editor{
		root:    d.root,
		cuts:    make(map[*html.Node][]span),
		touched: make(map[*html.Node]bool),
	}
	brs := d.widen(remove)
	for _, l := range d.lines {
		if !remove[l] {
			continue
		}
		for _, sp := range l.spans {
			e.cuts[sp.node] = append(e.cuts[sp.node], sp)
		}
		if l.nl.node != nil {
			e.cuts[l.nl.node] = append(e.cuts[l.nl.node], l.nl)
		}
		if l.br != nil {
			brs = append(brs, l.br)
		}
	}
	e.apply()
	for _, br := range brs {
		if p := br.Parent; p != nil {
			p.RemoveChild(br)
			e.touch(p)
		}
	}
	e.sweep(d.root, 0)

	var b strings.Builder
	for c := d.root.FirstChild; c != nil; c = c.NextSibling {
		if err := html.Render(&b, c); err != nil {
			return "", fmt.Errorf("render comment: %w", err)
		}
	}
	return b.String(), nil
}

// widen adds to remove the blank lines next to removed ones: at the start
// and end of each group that loses a line and of the whole comment. It
// returns the <br> elements that end a kept line followed only by removed
// lines of its group: they would leave an empty line behind.
func (d *doc) widen(remove map[*line]bool) []*html.Node {
	trimmable := func(l *line) bool {
		return remove[l] || (!l.opaque && l.blank && !l.media)
	}
	var brs []*html.Node
	for i := 0; i < len(d.lines); {
		j := i + 1
		for j < len(d.lines) && d.lines[j].group == d.lines[i].group {
			j++
		}
		g := d.lines[i:j]
		i = j
		if !slices.ContainsFunc(g, func(l *line) bool { return remove[l] }) {
			continue
		}
		for _, l := range g {
			if !trimmable(l) {
				break
			}
			remove[l] = true
		}
		for k := len(g) - 1; k >= 0; k-- {
			if !trimmable(g[k]) {
				if g[k].br != nil && k < len(g)-1 {
					brs = append(brs, g[k].br)
				}
				break
			}
			remove[g[k]] = true
		}
	}
	for _, l := range d.lines {
		if !trimmable(l) {
			break
		}
		remove[l] = true
	}
	for k := len(d.lines) - 1; k >= 0; k-- {
		if !trimmable(d.lines[k]) {
			break
		}
		remove[d.lines[k]] = true
	}
	return brs
}

// editor removes text and elements from a tree.
type editor struct {
	root    *html.Node
	cuts    map[*html.Node][]span // text to remove, per text node
	touched map[*html.Node]bool   // elements something was removed from, with their ancestors
}

// touch marks n and its ancestors below the root.
func (e *editor) touch(n *html.Node) {
	for ; n != nil && n != e.root && !e.touched[n]; n = n.Parent {
		e.touched[n] = true
	}
}

// apply removes the cut text; a text node left empty goes.
func (e *editor) apply() {
	for n, spans := range e.cuts {
		sort.Slice(spans, func(i, j int) bool { return spans[i].from < spans[j].from })
		var b strings.Builder
		pos := 0
		for _, sp := range spans {
			if sp.from > pos {
				b.WriteString(n.Data[pos:sp.from])
			}
			pos = max(pos, sp.to)
		}
		b.WriteString(n.Data[pos:])
		n.Data = b.String()
		p := n.Parent
		if n.Data == "" && p != nil {
			p.RemoveChild(n)
		}
		e.touch(p)
	}
}

// sweep removes the touched elements below n that are left with nothing to
// read, and reports whether n is.
func (e *editor) sweep(n *html.Node, depth int) bool {
	empty := true
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		switch c.Type {
		case html.TextNode:
			empty = empty && blankText(c.Data)
		case html.ElementNode:
			switch {
			case !prunable(c) || depth >= maxDepth:
				empty = false
			case e.touched[c]:
				if e.sweep(c, depth+1) {
					n.RemoveChild(c)
				} else {
					empty = false
				}
			default:
				empty = empty && emptyElement(c, depth+1)
			}
		}
		c = next
	}
	return empty
}

// prunable reports elements that may go when they hold nothing to read.
func prunable(n *html.Node) bool {
	k := kindOf(n)
	return k == kindInline || k == kindBlock
}

// emptyElement reports whether n holds nothing to read (an untouched
// element: nothing below it was removed).
func emptyElement(n *html.Node, depth int) bool {
	if depth >= maxDepth {
		return false
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case html.TextNode:
			if !blankText(c.Data) {
				return false
			}
		case html.ElementNode:
			if !prunable(c) || !emptyElement(c, depth+1) {
				return false
			}
		}
	}
	return true
}

// text is the visible text of the tree, one line per line, blank lines
// left out.
func (d *doc) text() string {
	var out []string
	t := texter{}
	flush := func() {
		if t.b.Len() > 0 {
			out = append(out, t.b.String())
		}
		t.reset()
	}
	var walk func(n *html.Node, depth int)
	walk = func(n *html.Node, depth int) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			switch c.Type {
			case html.TextNode:
				for i, part := range strings.Split(c.Data, "\n") {
					if i > 0 {
						flush()
					}
					t.write(part)
				}
			case html.ElementNode:
				if depth >= maxDepth {
					continue
				}
				switch kindOf(c) {
				case kindBreak:
					flush()
				case kindInline:
					walk(c, depth+1)
				case kindBlock, kindOpaque:
					flush()
					walk(c, depth+1)
					flush()
				}
			}
		}
	}
	walk(d.root, 0)
	flush()
	return strings.Join(out, "\n")
}
