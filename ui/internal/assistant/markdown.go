// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistant

// The small Markdown subset the panel shows the model's answers in (the
// system prompt asks for it). An answer is hostile input like mail: it
// may quote a message verbatim. So this is a linear scanner over a fixed
// set of constructs: no HTML ever, no nesting beyond bold and italic
// around text, links only to http and https, every control character but
// the newline and the tab dropped. The client turns the blocks into its
// own attributed text; nothing here knows about rendering.
//
// Linear: every opening marker finds its closing one through a cursor
// over the closing positions of its kind, which only moves forward
// (markers are met left to right, and one kind never nests in itself), so
// a megabyte of asterisks or brackets costs what a megabyte of letters
// does.
//
// This file holds no translatable text.

import (
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

// BlockKind is what a Block is.
type BlockKind int

// The kinds of blocks.
const (
	BlockParagraph BlockKind = iota
	BlockHeading
	BlockBullet
	BlockNumbered
	BlockCode
)

// Block is a paragraph, a heading, a list item or a code block.
type Block struct {
	Kind BlockKind
	// Level is a heading's level (1–3) or a list item's nesting (0 to
	// maxListLevel); 0 otherwise.
	Level int
	// Number is a numbered item's number as written; 0 otherwise.
	Number int
	// Spans are the block's text; a code block has one code span with
	// its lines, nothing parsed inside (none when it is empty).
	Spans []Span
}

// Span is a run of text in one style. Link is an http or https URL, ""
// for none; Text is what is shown.
type Span struct {
	Text               string
	Bold, Italic, Code bool
	Link               string
}

// maxListLevel is the deepest list nesting kept; deeper items stay at it.
const maxListLevel = 3

// Markdown reads the blocks of text. Lines are separated by "\n", "\r\n"
// or "\r". A blank line ends a paragraph or list item; a paragraph's lines
// stay separate lines ("\n" in the text), and a line that starts nothing
// continues the open paragraph or list item. After at most three spaces:
// "# ", "## " and "### " start a heading; a line of three backticks a
// code block up to the next such line (or the end: an answer that is
// still streaming). After any indentation: "- " and "* " start a bullet,
// 1 to 9 digits and ". " a numbered item, nested by indentation (an item
// indented more than the one before goes one level deeper, one indented
// as an earlier one returns to its level). A heading or list marker with
// no text after it is text. Inline: **bold**, *italic* and _italic_ (an
// underscore only at a word's edges, so create_draft stays as it is),
// `code`, [text](http(s)://…) and bare http(s):// URLs at a word's start.
// A marker without its closing half, and a link to anything but http or
// https, stays literal text.
func Markdown(text string) []Block {
	var p mdParser
	for _, line := range strings.Split(cleanText(text), "\n") {
		p.line(line)
	}
	p.end()
	return p.blocks
}

// cleanText turns "\r\n" and "\r" into "\n", drops every control
// character but "\n" and "\t" and replaces invalid UTF-8 with U+FFFD.
func cleanText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, w := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == '\r':
			b.WriteByte('\n')
			if i+1 < len(s) && s[i+1] == '\n' {
				w = 2
			}
		case r == '\n' || r == '\t':
			b.WriteByte(byte(r))
		case unicode.IsControl(r):
		default:
			b.WriteRune(r)
		}
		i += w
	}
	return b.String()
}

// mdParser reads the lines of Markdown.
type mdParser struct {
	blocks []Block
	// inCode while a code block is open; code its lines.
	inCode bool
	code   []string
	// open is the open paragraph or list item, if any, and lines its
	// lines.
	open  *Block
	lines []string
	// indents are the indentations of the open list's levels, outermost
	// first.
	indents []int
}

func (p *mdParser) line(l string) {
	indent, rest := splitIndent(l)
	if p.inCode {
		if indent <= 3 && strings.HasPrefix(rest, "```") {
			p.closeCode()
			return
		}
		p.code = append(p.code, l)
		return
	}
	if strings.Trim(rest, " \t") == "" {
		p.flush()
		return
	}
	if indent <= 3 && strings.HasPrefix(rest, "```") {
		p.flush()
		p.indents = nil
		p.inCode = true
		return
	}
	if indent <= 3 {
		if level, text, ok := heading(rest); ok {
			p.flush()
			p.indents = nil
			p.blocks = append(p.blocks, Block{Kind: BlockHeading, Level: level, Spans: inlineSpans(text)})
			return
		}
	}
	if kind, number, text, ok := listItem(rest); ok {
		p.flush()
		p.open = &Block{Kind: kind, Level: p.listLevel(indent), Number: number}
		p.lines = []string{text}
		return
	}
	text := strings.Trim(rest, " \t")
	if p.open != nil {
		p.lines = append(p.lines, text)
		return
	}
	p.indents = nil
	p.open = &Block{Kind: BlockParagraph}
	p.lines = []string{text}
}

// flush ends the open paragraph or list item.
func (p *mdParser) flush() {
	if p.open == nil {
		return
	}
	b := *p.open
	b.Spans = inlineSpans(strings.Join(p.lines, "\n"))
	p.blocks = append(p.blocks, b)
	p.open, p.lines = nil, nil
}

func (p *mdParser) closeCode() {
	b := Block{Kind: BlockCode}
	if text := strings.Join(p.code, "\n"); text != "" {
		b.Spans = []Span{{Text: text, Code: true}}
	}
	p.blocks = append(p.blocks, b)
	p.inCode, p.code = false, nil
}

func (p *mdParser) end() {
	if p.inCode {
		p.closeCode()
	}
	p.flush()
}

// listLevel is the level of a list item indented by indent columns.
func (p *mdParser) listLevel(indent int) int {
	for len(p.indents) > 0 && p.indents[len(p.indents)-1] > indent {
		p.indents = p.indents[:len(p.indents)-1]
	}
	if len(p.indents) == 0 || p.indents[len(p.indents)-1] < indent {
		p.indents = append(p.indents, indent)
	}
	return min(len(p.indents)-1, maxListLevel)
}

// splitIndent is the columns of l's leading spaces and tabs (a tab to the
// next multiple of 4) and the rest of l.
func splitIndent(l string) (int, string) {
	cols, i := 0, 0
	for ; i < len(l); i++ {
		switch l[i] {
		case ' ':
			cols++
		case '\t':
			cols += 4 - cols%4
		default:
			return cols, l[i:]
		}
	}
	return cols, ""
}

// heading reads "#", "##" or "###", a space or a tab and a text.
func heading(rest string) (int, string, bool) {
	n := 0
	for n < len(rest) && rest[n] == '#' {
		n++
	}
	if n < 1 || n > 3 || n == len(rest) || (rest[n] != ' ' && rest[n] != '\t') {
		return 0, "", false
	}
	text := strings.Trim(rest[n:], " \t")
	return n, text, text != ""
}

// listItem reads "- ", "* " or digits and ". ", and a text.
func listItem(rest string) (BlockKind, int, string, bool) {
	if strings.HasPrefix(rest, "- ") || strings.HasPrefix(rest, "* ") {
		text := strings.Trim(rest[2:], " \t")
		return BlockBullet, 0, text, text != ""
	}
	n, digits := 0, 0
	for digits < len(rest) && '0' <= rest[digits] && rest[digits] <= '9' {
		if digits == 9 {
			return 0, 0, "", false
		}
		n = n*10 + int(rest[digits]-'0')
		digits++
	}
	if digits == 0 || !strings.HasPrefix(rest[digits:], ". ") {
		return 0, 0, "", false
	}
	text := strings.Trim(rest[digits+2:], " \t")
	return BlockNumbered, n, text, text != ""
}

// closers are the positions of one kind of closing marker in a block's
// text, ascending, with a cursor that only moves forward.
type closers struct {
	pos []int
	k   int
}

// next is the first position at or after q; -1 when there is none.
// Successive calls must not ask for a smaller q.
func (c *closers) next(q int) int {
	for c.k < len(c.pos) && c.pos[c.k] < q {
		c.k++
	}
	if c.k < len(c.pos) {
		return c.pos[c.k]
	}
	return -1
}

// inliner reads the inline constructs of one block's text.
type inliner struct {
	s string
	// The closing markers: `, **, *, _, ] and ); opens are the "(".
	ticks, bolds, stars, unders, brackets, parens, opens closers
	// The cached target of the last "](": the ] it follows, the ) that
	// ends it (-1 for none) and whether the URL between is a web URL.
	linkAt, linkEnd int
	linkOK          bool
	// The last newline before nlScan, for "is there a newline in the
	// link's text".
	nlScan, nlLast int
	// spans are the finished spans; cur the open one, whose text is in
	// buf.
	spans []Span
	cur   Span
	buf   strings.Builder
	has   bool
}

// inlineSpans are the spans of a block's text.
func inlineSpans(s string) []Span {
	if s == "" {
		return nil
	}
	in := inliner{s: s, linkAt: -1, nlLast: -1}
	for j := 0; j < len(s); j++ {
		switch s[j] {
		case '`':
			in.ticks.pos = append(in.ticks.pos, j)
		case '*':
			if j+1 < len(s) && s[j+1] == '*' && j > 0 && !spaceBefore(s, j) {
				in.bolds.pos = append(in.bolds.pos, j)
			}
			if j > 0 && s[j-1] != '*' && !spaceBefore(s, j) && (j+1 == len(s) || s[j+1] != '*') {
				in.stars.pos = append(in.stars.pos, j)
			}
		case '_':
			if j > 0 && s[j-1] != '_' && !spaceBefore(s, j) && (j+1 == len(s) || s[j+1] != '_' && !wordAt(s, j+1)) {
				in.unders.pos = append(in.unders.pos, j)
			}
		case ']':
			in.brackets.pos = append(in.brackets.pos, j)
		case ')':
			in.parens.pos = append(in.parens.pos, j)
		case '(':
			in.opens.pos = append(in.opens.pos, j)
		}
	}
	in.run(0, len(s), false, false)
	in.closeSpan()
	return in.spans
}

// run reads s[lo:hi] with the style bold and italic around it.
func (in *inliner) run(lo, hi int, bold, italic bool) {
	s := in.s
	lit, i := lo, lo
	for i < hi {
		next := -1
		switch s[i] {
		case '`':
			if j := in.ticks.next(i + 1); j > i+1 && j < hi {
				in.text(s[lit:i], bold, italic)
				in.add(Span{Text: s[i+1 : j], Bold: bold, Italic: italic, Code: true})
				next = j + 1
			}
		case '*':
			if i+1 < hi && s[i+1] == '*' {
				if !bold && opensAt(s, i+2, hi) {
					if j := in.bolds.next(i + 3); j >= 0 && j+2 <= hi {
						in.text(s[lit:i], bold, italic)
						in.run(i+2, j, true, italic)
						next = j + 2
					}
				}
				if next < 0 {
					i += 2 // a literal "**" stays a pair
					continue
				}
			} else if !italic && opensAt(s, i+1, hi) {
				if j := in.stars.next(i + 2); j >= 0 && j < hi {
					in.text(s[lit:i], bold, italic)
					in.run(i+1, j, bold, true)
					next = j + 1
				}
			}
		case '_':
			if !italic && i+1 < hi && s[i+1] != '_' && opensAt(s, i+1, hi) && !wordBefore(s, i) {
				if j := in.unders.next(i + 2); j >= 0 && j < hi {
					in.text(s[lit:i], bold, italic)
					in.run(i+1, j, bold, true)
					next = j + 1
				}
			}
		case '[':
			if j, k, ok := in.link(i, hi); ok {
				in.text(s[lit:i], bold, italic)
				in.add(Span{Text: s[i+1 : j], Bold: bold, Italic: italic, Link: s[j+2 : k]})
				next = k + 1
			}
		case 'h', 'H':
			if !wordBefore(s, i) && hasWebScheme(s[i:hi]) {
				end, u := bareURL(s, i, hi)
				if u == "" {
					i = end // not a URL: literal text, never read again
					continue
				}
				in.text(s[lit:i], bold, italic)
				in.add(Span{Text: u, Bold: bold, Italic: italic, Link: u})
				next = i + len(u)
			}
		}
		if next >= 0 {
			lit, i = next, next
		} else {
			i++
		}
	}
	in.text(s[lit:hi], bold, italic)
}

// link reads "[text](url)" at i within hi: the positions of "](" and ")",
// and whether it is a link (text non-empty and on one line, url a web
// URL without a "(" of its own).
//
// The "(" rule keeps the scan linear: every later "](" that ends at the
// same ")" puts its "(" inside this target, so the targets read as URLs
// never overlap. A URL with parentheses, "[x](http://h/a_(b))", is then no
// link target, and the bare URL inside it is found as one.
func (in *inliner) link(i, hi int) (int, int, bool) {
	s := in.s
	j := in.brackets.next(i + 1)
	if j <= i+1 || j+1 >= hi || s[j+1] != '(' {
		return 0, 0, false
	}
	if j != in.linkAt {
		in.linkAt = j
		in.linkEnd = in.parens.next(j + 2)
		open := in.opens.next(j + 2)
		in.linkOK = in.linkEnd >= 0 && (open < 0 || open > in.linkEnd) && isWebURL(s[j+2:in.linkEnd])
	}
	if !in.linkOK || in.linkEnd >= hi || in.newlineBefore(j) > i {
		return 0, 0, false
	}
	return j, in.linkEnd, true
}

// newlineBefore is the position of the last "\n" before j, -1 when there
// is none. Successive calls must not ask for a smaller j.
func (in *inliner) newlineBefore(j int) int {
	for ; in.nlScan < j; in.nlScan++ {
		if in.s[in.nlScan] == '\n' {
			in.nlLast = in.nlScan
		}
	}
	return in.nlLast
}

// text adds literal text in the style bold and italic.
func (in *inliner) text(t string, bold, italic bool) {
	in.add(Span{Text: t, Bold: bold, Italic: italic})
}

// add appends sp, merged into the span before it when their styles are
// the same; empty text adds nothing.
func (in *inliner) add(sp Span) {
	if sp.Text == "" {
		return
	}
	if in.has && in.cur.Bold == sp.Bold && in.cur.Italic == sp.Italic && in.cur.Code == sp.Code && in.cur.Link == sp.Link {
		in.buf.WriteString(sp.Text)
		return
	}
	in.closeSpan()
	in.cur, in.has = sp, true
	in.buf.WriteString(sp.Text)
}

func (in *inliner) closeSpan() {
	if !in.has {
		return
	}
	in.cur.Text = in.buf.String()
	in.spans = append(in.spans, in.cur)
	in.buf.Reset()
	in.has = false
}

// opensAt says whether an opening marker can end before k: k is within hi
// and no space follows the marker.
func opensAt(s string, k, hi int) bool {
	if k >= hi {
		return false
	}
	r, _ := utf8.DecodeRuneInString(s[k:])
	return !unicode.IsSpace(r)
}

// spaceBefore says whether the character before j is a space.
func spaceBefore(s string, j int) bool {
	r, _ := utf8.DecodeLastRuneInString(s[:j])
	return unicode.IsSpace(r)
}

// wordBefore says whether a letter or digit comes right before i.
func wordBefore(s string, i int) bool {
	if i == 0 {
		return false
	}
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// wordAt says whether a letter or digit starts at i.
func wordAt(s string, i int) bool {
	r, _ := utf8.DecodeRuneInString(s[i:])
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// hasWebScheme says whether s starts with http:// or https://, in any
// case.
func hasWebScheme(s string) bool {
	head := asciiLower(s[:min(len(s), 8)])
	return strings.HasPrefix(head, "http://") || head == "https://"
}

// notInURL are the characters that end a bare URL besides spaces and
// control characters.
const notInURL = "<>\"`[]{}|\\^"

// bareURL reads the bare URL at i within hi: where its run of URL
// characters ends, and the URL without trailing punctuation ("" when that
// is not a web URL).
func bareURL(s string, i, hi int) (int, string) {
	end := i
	for end < hi {
		r, w := utf8.DecodeRuneInString(s[end:])
		if unicode.IsSpace(r) || unicode.IsControl(r) || strings.ContainsRune(notInURL, r) {
			break
		}
		end += w
	}
	j := end
	opens, closes := strings.Count(s[i:j], "("), strings.Count(s[i:j], ")")
	for j > i {
		c := s[j-1]
		if strings.IndexByte(".,;:!?'*_", c) >= 0 {
			j--
			continue
		}
		if c == ')' && closes > opens {
			closes--
			j--
			continue
		}
		break
	}
	if u := s[i:j]; isWebURL(u) {
		return end, u
	}
	return end, ""
}

// isWebURL says whether u is an absolute http or https URL with a host
// and nothing a URL does not carry literally: no spaces, control
// characters, quotes, angle brackets, backticks or backslashes.
func isWebURL(u string) bool {
	if !hasWebScheme(u) {
		return false
	}
	for _, r := range u {
		if unicode.IsSpace(r) || unicode.IsControl(r) || strings.ContainsRune("\"<>`\\", r) {
			return false
		}
	}
	p, err := url.Parse(u)
	return err == nil && (p.Scheme == "http" || p.Scheme == "https") && p.Hostname() != ""
}
