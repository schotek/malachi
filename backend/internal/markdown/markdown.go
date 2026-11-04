// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package markdown renders text pasted into the compose editor as HTML
// when it reads as Markdown (draft.markdown, docs/api.md §4.5): CommonMark
// with GitHub's tables, strikethrough and bare-URL links, and a line break
// wherever the text has one, as mail is written. Raw HTML in the text is
// shown as text, never markup; an image becomes a link to its address (a
// draft carries no remote pictures). Tables, code and quotes get inline
// styles, the only kind a message keeps. Nothing here is trusted: the
// caller sanitises the output like any HTML of the editor.
package markdown

import (
	"bytes"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// MaxDepth bounds the nesting of the parsed text (quotes in quotes, lists
// in lists, …). Deeper text is not rendered, so the recursive walks of
// the renderer and the sanitiser never see it; it is pasted as text.
const MaxDepth = 32

// Inline styles of the elements a recipient's client would otherwise show
// bare.
const (
	tableStyle      = "border-collapse:collapse"
	cellStyle       = "border:1px solid #d0d7de;padding:4px 8px"
	codeStyle       = "font-family:monospace;background-color:#f2f2f2;padding:0 3px;border-radius:3px"
	preStyle        = "font-family:monospace;background-color:#f6f8fa;padding:8px;border-radius:4px;white-space:pre-wrap"
	blockquoteStyle = "margin:0 0 0 .8ex;border-left:2px solid #ccc;padding-left:1ex;color:#555"
)

var md = goldmark.New(
	goldmark.WithExtensions(extension.Table, extension.Strikethrough, extension.Linkify),
	goldmark.WithParserOptions(parser.WithASTTransformers(util.Prioritized(styler{}, 100))),
	goldmark.WithRendererOptions(
		html.WithHardWraps(),
		renderer.WithNodeRenderers(util.Prioritized(overrides{}, 100)),
	),
)

// Render returns text as HTML and true when it reads as Markdown: it has a
// heading, a list, emphasis, code, a quote, a link, an image, a rule, a
// table or strikethrough. Plain text, or text nested deeper than MaxDepth,
// gives "" and false. An indented block alone does not count (pasted
// plain text is often indented), and neither does a bare address.
func Render(src string) (string, bool) {
	source := []byte(src)
	doc := md.Parser().Parse(text.NewReader(source))
	if tooDeep(doc, MaxDepth) || !hasMarkdown(doc) {
		return "", false
	}
	var out bytes.Buffer
	if err := md.Renderer().Render(&out, source, doc); err != nil {
		return "", false
	}
	return out.String(), true
}

// tooDeep walks without recursion, so it is safe on any parse.
func tooDeep(root ast.Node, limit int) bool {
	depth := 0
	n := root
	for {
		if c := n.FirstChild(); c != nil {
			n = c
			depth++
			if depth > limit {
				return true
			}
			continue
		}
		for n != root && n.NextSibling() == nil {
			n = n.Parent()
			depth--
		}
		if n == root {
			return false
		}
		n = n.NextSibling()
	}
}

func hasMarkdown(doc ast.Node) bool {
	found := false
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n.Kind() {
		case ast.KindHeading, ast.KindList, ast.KindEmphasis, ast.KindCodeSpan,
			ast.KindFencedCodeBlock, ast.KindBlockquote, ast.KindLink, ast.KindImage,
			ast.KindThematicBreak, east.KindTable, east.KindStrikethrough:
			found = true
			return ast.WalkStop, nil
		}
		return ast.WalkContinue, nil
	})
	return found
}

// styler sets the inline styles the renderers write as attributes.
type styler struct{}

func (styler) Transform(doc *ast.Document, _ text.Reader, _ parser.Context) {
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n.Kind() {
		case east.KindTable:
			n.SetAttributeString("style", []byte(tableStyle))
		case east.KindTableCell:
			n.SetAttributeString("style", []byte(cellStyle))
		case ast.KindCodeSpan:
			n.SetAttributeString("style", []byte(codeStyle))
		case ast.KindBlockquote:
			n.SetAttributeString("style", []byte(blockquoteStyle))
		}
		return ast.WalkContinue, nil
	})
}

// overrides replaces the renderers of raw HTML (shown as text), images
// (links) and code blocks (styled, no language class).
type overrides struct{}

func (overrides) RegisterFuncs(r renderer.NodeRendererFuncRegisterer) {
	r.Register(ast.KindRawHTML, renderRawHTML)
	r.Register(ast.KindHTMLBlock, renderHTMLBlock)
	r.Register(ast.KindImage, renderImage)
	r.Register(ast.KindCodeBlock, renderCode)
	r.Register(ast.KindFencedCodeBlock, renderCode)
}

func renderRawHTML(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		segs := n.(*ast.RawHTML).Segments
		for i := range segs.Len() {
			seg := segs.At(i)
			writeText(w, seg.Value(source))
		}
	}
	return ast.WalkSkipChildren, nil
}

func renderHTMLBlock(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.HTMLBlock)
	var lines [][]byte
	for i := range n.Lines().Len() {
		line := n.Lines().At(i)
		lines = append(lines, line.Value(source))
	}
	if n.HasClosure() {
		lines = append(lines, n.ClosureLine.Value(source))
	}
	_, _ = w.WriteString("<p>")
	for i, line := range lines {
		if i > 0 {
			_, _ = w.WriteString("<br>\n")
		}
		writeText(w, bytes.TrimRight(line, "\r\n"))
	}
	_, _ = w.WriteString("</p>\n")
	return ast.WalkSkipChildren, nil
}

func renderImage(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*ast.Image)
	dest := util.URLEscape(n.Destination, true)
	link := !html.IsDangerousURL(dest)
	if entering {
		if link {
			_, _ = w.WriteString(`<a href="`)
			_, _ = w.Write(util.EscapeHTML(dest))
			_, _ = w.WriteString(`">`)
		}
		if n.FirstChild() == nil {
			writeText(w, n.Destination)
		}
		return ast.WalkContinue, nil
	}
	if link {
		_, _ = w.WriteString("</a>")
	}
	return ast.WalkContinue, nil
}

func renderCode(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		_, _ = w.WriteString("</code></pre>\n")
		return ast.WalkContinue, nil
	}
	_, _ = w.WriteString(`<pre style="` + preStyle + `"><code>`)
	for i := range n.Lines().Len() {
		line := n.Lines().At(i)
		writeText(w, line.Value(source))
	}
	return ast.WalkSkipChildren, nil
}

// writeText writes b as text: HTML special characters escaped, nothing
// else interpreted.
func writeText(w util.BufWriter, b []byte) {
	html.DefaultWriter.RawWrite(w, b)
}
