// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The wiki markup of a comment for Jira Data Center, written from the
// document model of comment.go. Every character of the user's text that
// the renderer reads as markup is escaped, so text cannot become a macro
// ({html}, {include}...), a link or a mention ([...]), a picture (!...!),
// a table (|), a list or a heading (a line start), or formatting:
//
//   - { } [ ] | * _ ^ ~ ! get a backslash, # at the start of a run (a
//     numbered list at a line start), - and + unless they sit inside a
//     word (e-mail, well-known: the renderer formats only at word
//     boundaries), ? next to another ? (??citation??);
//   - a backslash, which cannot be escaped (\\ is a line break), becomes
//     the character reference &#92;, and every & becomes &amp; (the
//     renderer passes character references through, so text that looks
//     like one would otherwise become the character);
//   - a block that starts like "h1." or "bq." gets its first letter as a
//     character reference.
//
// Blocks are separated by a blank line and a line break is " \\ ", so the
// only line starts are block starts, which the markup of this file owns.
// Links are written as [text|url] for http(s) and mailto targets only
// (linkTarget), with the characters that would end the link
// percent-encoded. Code blocks are {noformat} (verbatim), unless the code
// itself contains {noformat or {quote, which would end it or an enclosing
// quote: then it is escaped text like any other.

// wikiBreak is a line break inside a block.
const wikiBreak = ` \\ `

// wikiMarkChars are the phrase marks, innermost first.
var wikiMarkChars = []struct {
	bit runMarks
	c   string
}{
	{markStrike, "-"}, {markUnderline, "+"}, {markEm, "_"}, {markStrong, "*"},
}

// wikiBlockStart is a line start the renderer reads as a heading or a
// quote.
var wikiBlockStart = regexp.MustCompile(`^(?i)(h[1-6]|bq)\.`)

// wikiMarkup is the wiki markup of blocks.
func wikiMarkup(blocks []docBlock) string {
	var parts []string
	for _, b := range blocks {
		if s := wikiBlock(b); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n\n")
}

func wikiBlock(b docBlock) string {
	switch b.kind {
	case docPara:
		return wikiLine(wikiRuns(b.runs))
	case docHeading:
		return fmt.Sprintf("h%d. %s", b.level, wikiRuns(b.runs))
	case docCode:
		if !wikiVerbatim(b.code) {
			return wikiLine(wikiCodeText(b.code))
		}
		return "{noformat}\n" + b.code + "\n{noformat}"
	case docRule:
		return "----"
	case docQuote:
		inner := wikiMarkup(b.blocks)
		if inner == "" {
			return ""
		}
		return "{quote}\n" + inner + "\n{quote}"
	case docList:
		var lines []string
		wikiList(b, "", &lines)
		return strings.Join(lines, "\n")
	}
	return ""
}

// wikiVerbatim reports whether code can go in a {noformat} block: nothing
// in it ends that block or a quote around it.
func wikiVerbatim(code string) bool {
	lower := strings.ToLower(code)
	return !strings.Contains(lower, "{noformat") && !strings.Contains(lower, "{quote")
}

// wikiList writes a list's items as lines with the prefix of their nesting
// (* bullets, # numbers, "*#" a numbered list in a bulleted one). An item's
// blocks other than lists are joined on its line; its lists follow.
func wikiList(b docBlock, prefix string, lines *[]string) {
	mark := "*"
	if b.ordered {
		mark = "#"
	}
	p := prefix + mark
	for _, item := range b.items {
		var text []string
		var nested []docBlock
		for _, c := range item {
			if c.kind == docList {
				nested = append(nested, c)
			} else if s := wikiInline(c); s != "" {
				text = append(text, s)
			}
		}
		line := strings.Join(text, wikiBreak)
		if line == "" {
			line = "&nbsp;"
		}
		*lines = append(*lines, p+" "+line)
		for _, n := range nested {
			wikiList(n, p, lines)
		}
	}
}

// wikiInline is a block written on one line (inside a list item).
func wikiInline(b docBlock) string {
	switch b.kind {
	case docPara, docHeading:
		return wikiRuns(b.runs)
	case docCode:
		return wikiCodeText(b.code)
	case docQuote:
		var parts []string
		for _, c := range b.blocks {
			if s := wikiInline(c); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, wikiBreak)
	case docList:
		var parts []string
		for _, it := range b.items {
			for _, c := range it {
				if s := wikiInline(c); s != "" {
					parts = append(parts, s)
				}
			}
		}
		return strings.Join(parts, wikiBreak)
	}
	return ""
}

// wikiCodeText is code as escaped text, its lines separated by breaks.
func wikiCodeText(code string) string {
	lines := strings.Split(code, "\n")
	for i, l := range lines {
		lines[i] = wikiEscape(strings.ReplaceAll(l, "\t", "    "))
	}
	return strings.Join(lines, wikiBreak)
}

// wikiLine makes a block start that the renderer would read as a heading
// or a quote plain text.
func wikiLine(s string) string {
	if wikiBlockStart.MatchString(s) {
		r, n := utf8.DecodeRuneInString(s)
		return fmt.Sprintf("&#%d;", r) + s[n:]
	}
	return s
}

func wikiRuns(runs []docRun) string {
	var b strings.Builder
	for _, r := range runs {
		if r.br {
			b.WriteString(wikiBreak)
			continue
		}
		b.WriteString(wikiRun(r))
	}
	return b.String()
}

// wikiRun writes a run: its text escaped, inside its marks (which must
// touch the text, so the spaces at its ends stay outside) and its link.
func wikiRun(r docRun) string {
	core := strings.Trim(r.text, " ")
	if core == "" {
		return r.text
	}
	lead := r.text[:strings.Index(r.text, core)]
	trail := r.text[len(lead)+len(core):]
	s := wikiEscape(core)
	if r.marks&markCode != 0 {
		s = "{{" + s + "}}"
	} else {
		for _, m := range wikiMarkChars {
			if r.marks&m.bit != 0 {
				s = m.c + s + m.c
			}
		}
	}
	if r.href != "" {
		s = "[" + s + "|" + wikiURL(r.href) + "]"
	}
	return lead + s + trail
}

// wikiEscape escapes the text of a run (see the file comment).
func wikiEscape(s string) string {
	rs := []rune(s)
	var b strings.Builder
	b.Grow(len(s) + len(s)/8)
	for i, r := range rs {
		switch r {
		case '\\':
			b.WriteString("&#92;")
		case '&':
			b.WriteString("&amp;")
		case '{', '}', '[', ']', '|', '*', '_', '^', '~', '!':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '#':
			if i == 0 {
				b.WriteByte('\\')
			}
			b.WriteRune(r)
		case '-', '+':
			if i > 0 && i+1 < len(rs) && isWordRune(rs[i-1]) && isWordRune(rs[i+1]) {
				b.WriteRune(r)
			} else {
				b.WriteByte('\\')
				b.WriteRune(r)
			}
		case '?':
			if (i > 0 && rs[i-1] == '?') || (i+1 < len(rs) && rs[i+1] == '?') {
				b.WriteByte('\\')
			}
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// wikiURL is a link target with the characters that would end the link
// or its text percent-encoded.
func wikiURL(href string) string {
	return strings.NewReplacer(
		"|", "%7C", "[", "%5B", "]", "%5D", "{", "%7B", "}", "%7D", `\`, "%5C", " ", "%20",
	).Replace(href)
}
