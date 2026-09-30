// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"encoding/json"
	stdhtml "html"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"

	imime "github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Outgoing comments. A comment draft's body is the sanitised compose HTML
// draft.save stored (or plain text); neither is what a site takes: Jira
// Cloud wants the Atlassian Document Format (adf.go), Data Center wiki
// markup (wiki.go). The HTML is first read into a small document model —
// paragraphs, headings, lists, quotes, code blocks, rules, and runs of text
// with marks and links — which both writers share.
//
// The HTML is hostile like any other input (a crafted draft, a file changed
// on disk): the walk is bounded in depth, nodes and text; deeper content is
// kept as its text; only http(s) and mailto links survive; pictures and
// everything else that is not text are dropped (a comment carries no
// attachments). What the user typed stays data in both formats — wiki.go
// escapes the markup characters, ADF has no markup inside text — and
// nothing of it is interpreted: bidirectional or zero-width characters and
// emoji go through as typed.

const (
	// MaxCommentChars is the most characters a comment may have in the
	// site's format: Jira's limit for a text field. On Cloud the ADF
	// document counts as its JSON, which is what the site measures.
	MaxCommentChars = 32767

	// maxCommentDepth is the element nesting the walk follows; the content
	// of deeper elements is kept as plain text.
	maxCommentDepth = 48
	// maxCommentNodes bounds the nodes visited; a document with more is
	// refused.
	maxCommentNodes = 200_000
	// maxCommentText bounds the text gathered: past it the comment is over
	// MaxCommentChars in any format, so the walk stops there.
	maxCommentText = 2 * MaxCommentChars
	// maxCommentListDepth is the list nesting kept; deeper lists become
	// paragraphs of the item they are in.
	maxCommentListDepth = 6
	// maxLinkURLBytes bounds a link target.
	maxLinkURLBytes = 2048
)

// CommentBody is a comment in the site's own format, as BuildComment makes
// it: ADF for Jira Cloud, wiki markup for Data Center.
type CommentBody struct {
	ADF  json.RawMessage // cloud: an ADF document (version 1)
	Wiki string          // datacenter
}

// BuildComment converts a comment draft's body into the deployment's
// format: htmlBody (the sanitiser's compose output), or textBody when
// htmlBody is empty. A comment that is empty after the conversion (only
// whitespace, pictures, rules), longer than MaxCommentChars in the site's
// format, or too complex to walk is invalidArgument.
func BuildComment(htmlBody, textBody string, d api.JiraDeployment) (CommentBody, error) {
	src := htmlBody
	if strings.TrimSpace(src) == "" {
		src = TextToHTML(textBody)
	}
	blocks, err := parseCommentHTML(src)
	if err != nil {
		return CommentBody{}, err
	}
	var n int
	var body CommentBody
	switch d {
	case api.JiraCloud:
		raw, err := adfDocument(blocks)
		if err != nil {
			return CommentBody{}, api.NewError(api.CodeInvalidArgument, "jira: comment: %v", err)
		}
		body.ADF, n = raw, utf8.RuneCount(raw)
	case api.JiraDataCenter:
		body.Wiki = wikiMarkup(blocks)
		n = utf8.RuneCountInString(body.Wiki)
	default:
		return CommentBody{}, api.NewError(api.CodeInvalidArgument, "jira: unknown deployment %q", d)
	}
	if n > MaxCommentChars {
		return CommentBody{}, commentTooLong(n)
	}
	return body, nil
}

// CheckComment reports what BuildComment would refuse of the HTML of a
// comment draft (a plain-text draft is passed as TextToHTML of its text):
// invalidArgument when the comment is empty after the conversion, longer
// than MaxCommentChars in the deployment's format, or too complex.
func CheckComment(html string, d api.JiraDeployment) error {
	_, err := BuildComment(html, "", d)
	return err
}

func commentTooLong(n int) *api.Error {
	return api.NewError(api.CodeInvalidArgument, "jira: comment too long (%d characters, limit %d)", n, MaxCommentChars)
}

// blankLines separates the paragraphs of a plain-text comment.
var blankLines = regexp.MustCompile(`\n[ \t]*\n`)

// TextToHTML is the HTML of a plain-text comment: a paragraph per run of
// lines between blank lines, a line break per line, everything escaped.
func TextToHTML(text string) string {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	var b strings.Builder
	for _, para := range blankLines.Split(text, -1) {
		para = strings.Trim(para, "\n")
		if strings.TrimSpace(para) == "" {
			continue
		}
		b.WriteString("<p>")
		b.WriteString(strings.ReplaceAll(stdhtml.EscapeString(para), "\n", "<br>"))
		b.WriteString("</p>")
	}
	return b.String()
}

// The document model.

// runMarks are the marks of a run of text.
type runMarks uint8

const (
	markStrong runMarks = 1 << iota
	markEm
	markUnderline
	markStrike
	markCode // alone, or with a link: neither format combines code with other marks
)

// docRun is a run of text with the same marks and link, or a line break.
type docRun struct {
	text  string
	br    bool
	marks runMarks
	href  string // "" = no link; else an http(s) or mailto URL (linkTarget)
}

type docKind int

const (
	docPara docKind = iota
	docHeading
	docCode
	docRule
	docQuote
	docList
)

// docBlock is a block of the document.
type docBlock struct {
	kind    docKind
	level   int        // docHeading: 1–6
	runs    []docRun   // docPara, docHeading: never empty, no leading or trailing break
	code    string     // docCode: the text, lines separated by LF, never blank
	blocks  []docBlock // docQuote: never a quote itself
	ordered bool       // docList
	items   [][]docBlock
}

// commentParser bounds one walk.
type commentParser struct {
	nodes   int
	text    int
	tooMany bool
	tooLong bool
}

func (p *commentParser) stop() bool { return p.tooMany || p.tooLong }

// visit counts a node; false once there were too many.
func (p *commentParser) visit() bool {
	p.nodes++
	if p.nodes > maxCommentNodes {
		p.tooMany = true
	}
	return !p.tooMany
}

// gather counts text; false once there was too much.
func (p *commentParser) gather(s string) bool {
	p.text += utf8.RuneCountInString(s)
	if p.text > maxCommentText {
		p.tooLong = true
	}
	return !p.tooLong
}

// parseCommentHTML reads the HTML into the document model; invalidArgument
// when the result has no text, or the HTML is too large or too complex.
// HTML the parser refuses (it nests more than 512 elements, deeper than
// the sanitiser ever lets through) is read as its text.
func parseCommentHTML(src string) ([]docBlock, error) {
	src = strings.ToValidUTF8(src, "")
	root, err := html.Parse(strings.NewReader(src))
	if err != nil {
		text := imime.HTMLToText(src, 4*maxCommentText)
		if root, err = html.Parse(strings.NewReader(TextToHTML(text))); err != nil {
			return nil, api.NewError(api.CodeInvalidArgument, "jira: comment HTML does not parse")
		}
	}
	p := &commentParser{}
	f := &docFlow{p: p}
	f.children(root, 0, runStyle{})
	f.flush()
	switch {
	case p.tooMany:
		return nil, api.NewError(api.CodeInvalidArgument, "jira: comment too complex (more than %d nodes)", maxCommentNodes)
	case p.tooLong:
		return nil, commentTooLong(p.text)
	}
	if strings.TrimFunc(docText(f.blocks), unicode.IsSpace) == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "jira: comment is empty")
	}
	return f.blocks, nil
}

// runStyle is what the elements around a text node make of it.
type runStyle struct {
	marks runMarks
	href  string
}

// docFlow collects the blocks of one container (the document, a quote, a
// list item): inline content gathers into the current paragraph until a
// block boundary flushes it.
type docFlow struct {
	p      *commentParser
	blocks []docBlock
	cur    []docRun
	quoted bool // inside a quote: a nested quote adds its blocks here
	lists  int  // list nesting of this container
}

// sub is a flow for the content of a container inside this one.
func (f *docFlow) sub(quoted bool, lists int) *docFlow {
	return &docFlow{p: f.p, quoted: quoted, lists: lists}
}

func (f *docFlow) flush() {
	if runs := normaliseRuns(f.cur); len(runs) > 0 {
		f.blocks = append(f.blocks, docBlock{kind: docPara, runs: runs})
	}
	f.cur = nil
}

func (f *docFlow) text(s string, st runStyle) {
	if s == "" || !f.p.gather(s) {
		return
	}
	f.cur = append(f.cur, docRun{text: s, marks: st.marks, href: st.href})
}

func (f *docFlow) children(n *html.Node, depth int, st runStyle) {
	for c := n.FirstChild; c != nil && !f.p.stop(); c = c.NextSibling {
		f.node(c, depth+1, st)
	}
}

// skippedElements vanish with their content: what is not text of the
// comment (pictures, media, forms, scripts, metadata).
var skippedElements = map[string]bool{
	"script": true, "style": true, "template": true, "head": true, "title": true, "noscript": true,
	"iframe": true, "frame": true, "frameset": true, "object": true, "embed": true, "applet": true,
	"svg": true, "math": true, "canvas": true, "video": true, "audio": true, "img": true,
	"picture": true, "source": true, "track": true, "map": true, "area": true, "input": true,
	"button": true, "select": true, "textarea": true, "option": true, "optgroup": true,
	"datalist": true, "meta": true, "link": true, "base": true, "col": true, "colgroup": true,
	"dialog": true, "slot": true, "portal": true,
}

// blockElements end the paragraph before them and after them.
var blockElements = map[string]bool{
	"p": true, "div": true, "section": true, "article": true, "header": true, "footer": true,
	"main": true, "nav": true, "aside": true, "address": true, "center": true, "figure": true,
	"figcaption": true, "details": true, "summary": true, "dl": true, "dt": true, "dd": true,
	"li": true, "table": true, "caption": true, "thead": true, "tbody": true, "tfoot": true,
	"tr": true, "form": true, "fieldset": true, "legend": true, "body": true, "html": true,
}

// breaksLine reports the elements whose content does not run on with the
// text around them.
func breaksLine(name string) bool {
	switch name {
	case "br", "hr", "pre", "blockquote", "ul", "ol", "h1", "h2", "h3", "h4", "h5", "h6", "td", "th":
		return true
	}
	return blockElements[name]
}

func (f *docFlow) node(n *html.Node, depth int, st runStyle) {
	if !f.p.visit() {
		return
	}
	switch n.Type {
	case html.TextNode:
		f.text(n.Data, st)
		return
	case html.ElementNode:
	default:
		return // comments, doctypes
	}
	name := n.Data
	if n.Namespace != "" || skippedElements[name] {
		return
	}
	if depth > maxCommentDepth {
		// Too deep to follow: its text, as a run of the current paragraph.
		f.text(f.p.textOf(n), st)
		return
	}
	switch name {
	case "br":
		f.cur = append(f.cur, docRun{br: true})
		return
	case "hr":
		f.flush()
		f.blocks = append(f.blocks, docBlock{kind: docRule})
		return
	case "b", "strong":
		st.marks |= markStrong
	case "i", "em":
		st.marks |= markEm
	case "u", "ins":
		st.marks |= markUnderline
	case "s", "strike", "del":
		st.marks |= markStrike
	case "code", "tt", "kbd", "samp":
		st.marks |= markCode
	case "a":
		if href := linkTarget(attr(n, "href")); href != "" {
			st.href = href
		}
	case "h1", "h2", "h3", "h4", "h5", "h6":
		f.flush()
		if runs := f.headingRuns(n, depth, st); len(runs) > 0 {
			f.blocks = append(f.blocks, docBlock{kind: docHeading, level: int(name[1] - '0'), runs: runs})
		}
		return
	case "pre":
		f.flush()
		if code := f.p.preText(n); code != "" {
			f.blocks = append(f.blocks, docBlock{kind: docCode, code: code})
		}
		return
	case "blockquote":
		f.flush()
		inner := f.sub(true, f.lists)
		inner.children(n, depth, st)
		inner.flush()
		switch {
		case len(inner.blocks) == 0:
		case f.quoted:
			// Neither format nests quotes: the inner one's content joins
			// the outer one.
			f.blocks = append(f.blocks, inner.blocks...)
		default:
			f.blocks = append(f.blocks, docBlock{kind: docQuote, blocks: inner.blocks})
		}
		return
	case "ul", "ol":
		f.flush()
		f.list(n, depth, st, name == "ol")
		return
	case "td", "th":
		// Cells of a row become one paragraph, separated by a bar.
		for prev := n.PrevSibling; prev != nil; prev = prev.PrevSibling {
			if prev.Type == html.ElementNode && (prev.Data == "td" || prev.Data == "th") {
				f.text(" | ", runStyle{})
				break
			}
		}
		f.children(n, depth, st)
		return
	}
	if blockElements[name] {
		f.flush()
		f.children(n, depth, st)
		f.flush()
		return
	}
	f.children(n, depth, st)
}

// list adds a list block of the items of n; past maxCommentListDepth the
// items become paragraphs of this container.
func (f *docFlow) list(n *html.Node, depth int, st runStyle, ordered bool) {
	if f.lists >= maxCommentListDepth {
		for c := n.FirstChild; c != nil && !f.p.stop(); c = c.NextSibling {
			f.flush()
			f.node(c, depth+1, st)
			f.flush()
		}
		return
	}
	b := docBlock{kind: docList, ordered: ordered}
	for c := n.FirstChild; c != nil && !f.p.stop(); c = c.NextSibling {
		if c.Type == html.TextNode && strings.TrimFunc(c.Data, isHTMLSpace) == "" {
			continue
		}
		item := f.sub(f.quoted, f.lists+1)
		if c.Type == html.ElementNode && c.Data == "li" {
			if !f.p.visit() {
				break
			}
			item.children(c, depth+1, st)
		} else {
			// Content outside an item is an item of its own.
			item.node(c, depth+1, st)
		}
		item.flush()
		if len(item.blocks) > 0 {
			b.items = append(b.items, item.blocks)
		}
	}
	if len(b.items) > 0 {
		f.blocks = append(f.blocks, b)
	}
}

// headingRuns is the inline content of a heading: its blocks joined by a
// space, line breaks as spaces.
func (f *docFlow) headingRuns(n *html.Node, depth int, st runStyle) []docRun {
	inner := f.sub(f.quoted, f.lists)
	inner.children(n, depth, st)
	inner.flush()
	var runs []docRun
	for _, b := range inner.blocks {
		if len(runs) > 0 {
			runs = append(runs, docRun{text: " "})
		}
		runs = append(runs, blockRuns(b)...)
	}
	for i := range runs {
		if runs[i].br {
			runs[i] = docRun{text: " "}
		}
	}
	return normaliseRuns(runs)
}

// blockRuns is a block's content as runs: its own for a paragraph or a
// heading, its text otherwise.
func blockRuns(b docBlock) []docRun {
	switch b.kind {
	case docPara, docHeading:
		return b.runs
	case docRule:
		return nil
	}
	return []docRun{{text: docText([]docBlock{b})}}
}

// textOf is the text of a subtree, with a space for every element that
// breaks a line, walked without recursion (it is what the walk keeps of
// content nested too deep to follow).
func (p *commentParser) textOf(n *html.Node) string {
	var b strings.Builder
	stack := []*html.Node{}
	for c := n.LastChild; c != nil; c = c.PrevSibling {
		stack = append(stack, c)
	}
	for len(stack) > 0 && !p.stop() {
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if !p.visit() {
			break
		}
		switch c.Type {
		case html.TextNode:
			b.WriteString(c.Data)
		case html.ElementNode:
			if c.Namespace != "" || skippedElements[c.Data] {
				continue
			}
			if breaksLine(c.Data) {
				b.WriteByte(' ')
			}
			for g := c.LastChild; g != nil; g = g.PrevSibling {
				stack = append(stack, g)
			}
		}
	}
	return b.String()
}

// preText is the text of a <pre>, whitespace kept, <br> as a line break,
// control characters dropped; "" when it is blank.
func (p *commentParser) preText(n *html.Node) string {
	var b strings.Builder
	stack := []*html.Node{}
	for c := n.LastChild; c != nil; c = c.PrevSibling {
		stack = append(stack, c)
	}
	for len(stack) > 0 && !p.stop() {
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if !p.visit() {
			break
		}
		switch c.Type {
		case html.TextNode:
			if p.gather(c.Data) {
				b.WriteString(c.Data)
			}
		case html.ElementNode:
			if c.Namespace != "" || skippedElements[c.Data] {
				continue
			}
			if c.Data == "br" {
				b.WriteByte('\n')
			}
			for g := c.LastChild; g != nil; g = g.PrevSibling {
				stack = append(stack, g)
			}
		}
	}
	s := strings.ReplaceAll(strings.ReplaceAll(b.String(), "\r\n", "\n"), "\r", "\n")
	s = strings.Map(func(r rune) rune {
		if r != '\n' && r != '\t' && isControl(r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimRight(s, " \t\n")
	for {
		line, rest, ok := strings.Cut(s, "\n")
		if !ok || strings.TrimFunc(line, isHTMLSpace) != "" {
			break
		}
		s = rest
	}
	if strings.TrimFunc(s, unicode.IsSpace) == "" {
		return ""
	}
	return s
}

// normaliseRuns collapses the whitespace of a paragraph's runs the way a
// browser shows it (a run of spaces, tabs and newlines is one space; none
// at the start, at the end, or around a line break), drops control
// characters, empty runs and leading and trailing breaks, merges
// neighbours with the same marks and link, and returns nil when no text is
// left (a no-break space alone is no text). A code run keeps no other mark.
func normaliseRuns(in []docRun) []docRun {
	var out []docRun
	space := true // at the start and after a break, whitespace is dropped
	for _, r := range in {
		if r.br {
			trimTrailingSpace(&out)
			out = append(out, docRun{br: true})
			space = true
			continue
		}
		var b strings.Builder
		for _, c := range r.text {
			switch {
			case isHTMLSpace(c):
				if !space {
					b.WriteByte(' ')
					space = true
				}
			case isControl(c):
			default:
				b.WriteRune(c)
				space = false
			}
		}
		if b.Len() == 0 {
			continue
		}
		run := docRun{text: b.String(), marks: r.marks, href: r.href}
		if run.marks&markCode != 0 {
			run.marks = markCode
		}
		if n := len(out); n > 0 && !out[n-1].br && out[n-1].marks == run.marks && out[n-1].href == run.href {
			out[n-1].text += run.text
			continue
		}
		out = append(out, run)
	}
	trimTrailingSpace(&out)
	for len(out) > 0 && out[0].br {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1].br {
		out = out[:len(out)-1]
		trimTrailingSpace(&out)
	}
	for _, r := range out {
		if !r.br && strings.TrimFunc(r.text, unicode.IsSpace) != "" {
			return out
		}
	}
	return nil
}

// trimTrailingSpace drops the space the last run ends with, and the run
// when nothing else is in it.
func trimTrailingSpace(runs *[]docRun) {
	n := len(*runs)
	if n == 0 || (*runs)[n-1].br {
		return
	}
	last := &(*runs)[n-1]
	last.text = strings.TrimSuffix(last.text, " ")
	if last.text == "" {
		*runs = (*runs)[:n-1]
	}
}

// isHTMLSpace is the whitespace HTML collapses (not the no-break space).
func isHTMLSpace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '\f':
		return true
	}
	return false
}

// isControl reports C0 and C1 control characters and DEL.
func isControl(r rune) bool { return r < 0x20 || (r >= 0x7f && r <= 0x9f) }

// docText is the plain text of blocks, a space between runs of different
// blocks: what the emptiness check reads.
func docText(blocks []docBlock) string {
	var b strings.Builder
	var walk func(bs []docBlock)
	walk = func(bs []docBlock) {
		for _, bl := range bs {
			switch bl.kind {
			case docPara, docHeading:
				for _, r := range bl.runs {
					b.WriteString(r.text)
				}
			case docCode:
				b.WriteString(bl.code)
			case docQuote:
				walk(bl.blocks)
			case docList:
				for _, it := range bl.items {
					walk(it)
				}
			}
			b.WriteByte(' ')
		}
	}
	walk(blocks)
	return b.String()
}

// linkTarget is a link's href when a comment may carry it: an absolute
// http(s) URL with a host and no user info, or a mailto URL, without
// whitespace or control characters and within maxLinkURLBytes; "" for
// anything else (javascript:, data:, cid:, relative links...).
func linkTarget(raw string) string {
	raw = strings.TrimFunc(raw, isHTMLSpace)
	if raw == "" || len(raw) > maxLinkURLBytes || !utf8.ValidString(raw) {
		return ""
	}
	for _, r := range raw {
		if r <= 0x20 || isControl(r) || unicode.IsSpace(r) || isInvisible(r) {
			return ""
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		if u.Host == "" || u.User != nil || u.Opaque != "" {
			return ""
		}
	case "mailto":
		if u.Opaque == "" && u.Path == "" {
			return ""
		}
	default:
		return ""
	}
	u.Scheme = strings.ToLower(u.Scheme)
	s := u.String()
	if len(s) > maxLinkURLBytes {
		return ""
	}
	return s
}

// attr is the value of an attribute of n ("" when absent).
func attr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if a.Namespace == "" && strings.EqualFold(a.Key, name) {
			return a.Val
		}
	}
	return ""
}
