// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package sanitize

import "strings"

// TrimQuotedText cuts the quoted history off a plain-text body, by the
// rules quote.go applies to HTML, and reports whether it did; the text
// comes back unchanged otherwise. It is the plain-text side of
// message.body trimQuoted: the body of a message without HTML, the text
// when its HTML was withheld, and the text alternative of HTML that was
// trimmed.
//
// A history starts at the first line that is
//   - a "-----Original Message-----" separator or one of its translations
//     (separatorLine), or
//   - a line of underscores (Outlook's plain-text separator) followed by a
//     header block naming From, Sent or Date, and To or Subject, or
//   - such a header block on its own (Outlook's text alternative of an
//     HTML reply, where the separator was a border): at least three
//     "Label: value" lines in a row, after a blank line and at the start of
//     their lines, the first From with a value, a Sent or Date whose value
//     has a digit, a To or Subject, closed by a blank line and followed by
//     the quoted message, the text above not ending with a colon
//     (headerBlockAt);
//
// or a nested quote runs to the end: an attribution line ("On … wrote:",
// possibly wrapped onto two lines) followed by nothing but ">"-quoted and
// blank lines. Nothing is cut when the history holds nothing or nothing
// but blank lines would be left above it.
func TrimQuotedText(s string) (string, bool) {
	lines := strings.Split(s, "\n")
	if len(lines) > maxTextLines {
		return s, false
	}
	t := &textLines{lines: lines, lastShown: -1, lastPlain: -1, lastQuoted: -1}
	for i := range lines {
		switch l := t.text(i); {
		case blankText(l):
			continue
		case quotedLine(l):
			t.lastQuoted = i
		default:
			t.lastPlain = i
		}
		t.lastShown = i
	}
	cut := -1
	for i := range lines {
		if at, ok := t.quoteAt(i); ok {
			cut = at
			break
		}
	}
	if cut < 0 {
		return s, false
	}
	above := strings.Join(lines[:cut], "\n")
	if blankText(above) {
		return s, false
	}
	return strings.TrimRightFunc(above, blankRune), true
}

// maxTextLines bounds the lines TrimQuotedText looks at; a longer text is
// left whole.
const maxTextLines = 100_000

// textLines is a text split into lines, with where its last lines of each
// sort are (-1 for none), so that each candidate is checked in constant
// time.
type textLines struct {
	lines      []string
	lastShown  int // the last line that is not blank
	lastPlain  int // the last line that is neither blank nor quoted
	lastQuoted int // the last ">"-quoted line
}

func (t *textLines) text(i int) string { return strings.TrimRight(t.lines[i], "\r") }

// quoteAt reports whether a quote starts at line i, and the first line to
// cut (which may be earlier, for a wrapped attribution).
func (t *textLines) quoteAt(i int) (int, bool) {
	line := t.text(i)
	switch {
	case separatorLine(line):
		return i, t.lastShown > i
	case underscoreLine(line):
		return i, headerLabels(func(k int) (string, bool) {
			j := t.nextNonBlank(i+1, k)
			if j < 0 {
				return "", false
			}
			return t.text(j), true
		})
	case t.headerBlockAt(i):
		return i, true
	case attributionText(line) && !quotedLine(line):
		start := i
		// "On Wed, 1 Oct 2026 at 10:00, Jan <jan@example.org>" / "wrote:":
		// a short last line takes the one before it along.
		if len(strings.Fields(line)) < 4 && i > 0 {
			prev := t.text(i - 1)
			if !blankText(prev) && !quotedLine(prev) && len(prev)+len(line) < attributionMaxLen {
				start = i - 1
			}
		}
		// Only quoted and blank lines below it: anything else is a reply
		// below the quote.
		return start, t.lastPlain == i && t.lastQuoted > i
	}
	return 0, false
}

// headerBlockMinLines is the fewest lines of a header block without a
// separator above it.
const headerBlockMinLines = 3

// headerBlockAt reports whether an Outlook header block without a
// separator starts at line i: Outlook's text alternative of an HTML reply
// (the separator there is a <div> with a top border, which leaves no line)
// and its plain-text replies.
//
//	From: Jan Novák <jan@example.org>
//	Sent: Wednesday, October 1, 2026 9:12 AM
//	To: Petr <petr@example.org>
//	Subject: RE: Report
//
// Without a separator line text written by hand could read alike, so the
// block must be one beyond doubt: line i follows a blank line and starts
// at the start of its line (no indent, no ">"), at least
// headerBlockMinLines consecutive lines (at most quoteHeaderLines) carry
// a known label (quoteLabels), the first From with a value, then a Sent or
// Date whose value has a digit (a date) and a To or Subject; the line
// after the block is blank and something shows below it; and the text
// above does not end with a colon (introduced: Apple Mail's "Begin
// forwarded message:" over a forward, which stays whole).
func (t *textLines) headerBlockAt(i int) bool {
	if i == 0 || !blankText(t.text(i-1)) {
		return false
	}
	if labelOf(t.text(i)) != labelFrom {
		return false // the common case, before anything else is read
	}
	if t.introduced(i) {
		return false
	}
	var seen [labelSubject + 1]bool
	end := i
	for ; end < len(t.lines) && end < i+quoteHeaderLines; end++ {
		line := t.text(end)
		if line == "" || line != strings.TrimLeftFunc(line, blankRune) {
			break
		}
		kind := labelOf(line)
		if kind == 0 {
			break
		}
		value := strings.TrimFunc(line[strings.IndexByte(line, ':')+1:], blankRune)
		switch {
		case end == i && (kind != labelFrom || value == ""):
			return false
		case kind == labelSent:
			if !strings.ContainsAny(value, "0123456789") {
				return false
			}
		}
		seen[kind] = true
	}
	if end-i < headerBlockMinLines || !seen[labelSent] || !(seen[labelTo] || seen[labelSubject]) {
		return false
	}
	// Closed by a blank line, with the quoted message below.
	return end < len(t.lines) && blankText(t.text(end)) && t.lastShown > end
}

// introduced reports whether the nearest line above line i that is not
// blank ends with a colon: a line that introduces what follows ("Begin
// forwarded message:", "head the report like this:"), so the block below
// is the message itself, or text, never a history to hide.
func (t *textLines) introduced(i int) bool {
	for j := i - 1; j >= 0 && j >= i-quoteHeaderLines*2; j-- {
		if l := strings.TrimRightFunc(t.text(j), blankRune); l != "" {
			return strings.HasSuffix(l, ":")
		}
	}
	return false
}

// nextNonBlank is the index of the k-th (from 0) line that is not blank
// from line from on, -1 when there are fewer.
func (t *textLines) nextNonBlank(from, k int) int {
	for j := from; j < len(t.lines) && j < from+quoteHeaderLines*2; j++ {
		if blankText(t.text(j)) {
			continue
		}
		if k == 0 {
			return j
		}
		k--
	}
	return -1
}

// underscoreLine reports whether line is Outlook's plain-text separator: a
// run of at least ten underscores and nothing else.
func underscoreLine(line string) bool {
	line = strings.TrimFunc(line, blankRune)
	return len(line) >= 10 && strings.Trim(line, "_") == ""
}

// quotedLine reports whether line is quoted with ">".
func quotedLine(line string) bool {
	return strings.HasPrefix(strings.TrimLeft(line, " \t"), ">")
}
