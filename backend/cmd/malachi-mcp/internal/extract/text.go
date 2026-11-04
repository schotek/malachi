// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package extract

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// minTextChars is how many non-space characters a PDF page needs to count
// as having text, and the fewest the garbled rule judges: a page with less
// is too short to tell a decoding failure from a page number.
const minTextChars = 16

// textBuilder collects the text of a document line by line, up to a byte
// cap. Every reader writes through one, so all three formats share the
// same normalisation: "\n" line ends (CRLF and a lone CR become LF), no
// white space at the end of a line, at most one blank line in a row, no
// blank lines at the start or the end, invalid UTF-8 shown as U+FFFD. No
// Unicode normalisation; the bridge's clean() strips format characters.
//
// The cap cuts at a line boundary: a line that does not fit ends the text
// at the line before it, and nothing written afterwards is kept. Only a
// line longer than the whole cap, which could never fit, is cut inside, at
// a character boundary, so that one huge line still gives text.
type textBuilder struct {
	limit int
	b     strings.Builder
	// sep is the line breaks owed before the next line: none at the start,
	// one after a line, two once a blank line was asked for.
	sep int
	cut bool
}

func newTextBuilder(limit int) *textBuilder {
	return &textBuilder{limit: limit}
}

// Lines adds s, one or more lines. A final newline ends the last line
// rather than starting an empty one, so Lines("a\n") and Lines("a") are
// the same; an empty s is a blank line. It reports false once the text
// has been cut: from then on nothing is added, and a reader can stop.
func (t *textBuilder) Lines(s string) bool {
	if t.cut {
		return false
	}
	s = normalizeNewlines(strings.ToValidUTF8(s, string(utf8.RuneError)))
	s = strings.TrimSuffix(s, "\n")
	for {
		line, rest, more := strings.Cut(s, "\n")
		if !t.line(line) {
			return false
		}
		if !more {
			return true
		}
		s = rest
	}
}

// Blank asks for a blank line before the next line; it is dropped at the
// start and the end of the text and merged with any other blank line.
func (t *textBuilder) Blank() {
	if t.sep > 0 {
		t.sep = 2
	}
}

// line adds one line without a line end.
func (t *textBuilder) line(l string) bool {
	l = strings.TrimRightFunc(l, unicode.IsSpace)
	if l == "" {
		t.Blank()
		return true
	}
	sep := "\n\n"[:t.sep]
	if t.b.Len()+len(sep)+len(l) <= t.limit {
		t.b.WriteString(sep)
		t.b.WriteString(l)
		t.sep = 1
		return true
	}
	t.cut = true
	if len(l) > t.limit {
		part := strings.TrimRightFunc(runePrefix(l, t.limit-t.b.Len()-len(sep)), unicode.IsSpace)
		if part != "" {
			t.b.WriteString(sep)
			t.b.WriteString(part)
		}
	}
	return false
}

// Cut reports whether the cap left text out.
func (t *textBuilder) Cut() bool { return t.cut }

// Len is the length of the text so far, in bytes.
func (t *textBuilder) Len() int { return t.b.Len() }

// String is the text so far.
func (t *textBuilder) String() string { return t.b.String() }

// runePrefix is the longest prefix of s of at most n bytes that ends on a
// character boundary.
func runePrefix(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// normalizeNewlines turns CRLF and a lone CR into LF.
func normalizeNewlines(s string) string {
	if strings.IndexByte(s, '\r') < 0 {
		return s
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// countChars counts the characters of s that are not white space, and of
// them the bad ones: those that show text which could not be decoded.
// Space, tab and newline, and the other Unicode spaces that are not
// controls, are white space; CR, VT, FF and NEL are controls, so they are
// counted, as bad.
func countChars(s string) (bad, nonSpace int) {
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || (unicode.IsSpace(r) && !unicode.IsControl(r)) {
			continue
		}
		nonSpace++
		if badRune(r) {
			bad++
		}
	}
	return bad, nonSpace
}

// badRune reports a character that stands for text a reader could not
// decode: U+FFFD (invalid UTF-8 decodes to it too), private use (what a
// font without a character map often yields: U+E000-U+F8FF and planes 15
// and 16) and controls other than tab and newline.
func badRune(r rune) bool {
	switch {
	case r == utf8.RuneError:
		return true
	case r >= 0xE000 && r <= 0xF8FF:
		return true
	case r >= 0xF0000 && r <= unicode.MaxRune:
		return true
	case r == '\t' || r == '\n':
		return false
	}
	return unicode.IsControl(r)
}

// garbled reports whether a page with these counts is undecodable: at
// least minTextChars non-space characters, and more than maxPercent of
// them bad.
func garbled(bad, nonSpace, maxPercent int) bool {
	return nonSpace >= minTextChars && bad*100 > nonSpace*maxPercent
}
