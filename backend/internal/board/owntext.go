// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package board

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/internal/sanitize"
	"github.com/schotek/malachi/backend/internal/thread"
)

const (
	// maxInputBytes bounds any text the package looks at; the rest of a
	// longer one is ignored (and an excerpt of it is marked trimmed).
	maxInputBytes = 1 << 20
	// maxOwnTextBytes bounds the own text searched for a question mark.
	maxOwnTextBytes = 8 << 10
	// shortOwnText: a thread-starting message that quotes something below
	// less than this of its own text is a forward.
	shortOwnText = 300
	// maxSubjectScan bounds a subject looked at for forward markers.
	maxSubjectScan = 4 << 10
)

// maxOwnTextLines bounds the lines of a text OwnText reads (the limit of
// sanitize.TrimQuotedText, which leaves a longer text whole): a longer text
// has no own text.
const maxOwnTextLines = 100_000

// OwnText returns what the user wrote in m, one of the user's messages:
// the own text the rules search for a question mark (them.asked) and a
// commitment's quote must be in (QuoteInOwnText). It starts from
// m.OwnText when the caller set it (OwnTextSet: derived from the HTML
// part), else from m.Text, and cuts off, failing closed (less own text,
// never more):
//   - everything, when the text is over maxOwnTextLines lines (the quote
//     trimming gives up there) or starts with a quoted history (a forward);
//   - the quoted history sanitize.TrimQuotedText finds;
//   - everything from an attribution line ("On … wrote:") on when the line
//     below it is not ">"-quoted — a reply whose quote is not prefixed
//     (the text alternative of HTML, document.body.innerText) — and always
//     when the text is m.OwnText; an attribution line above ">"-quoted
//     lines (an interleaved reply) is dropped alone;
//   - every ">"-quoted line;
//   - the signature, from the RFC 3676 separator line "-- " on.
//
// Not cleaned; the input is looked at up to 1 MiB.
func OwnText(m Member) string {
	text := m.Text
	if m.OwnTextSet {
		text = m.OwnText
	}
	text = capBytes(text, maxInputBytes)
	if strings.Count(text, "\n") >= maxOwnTextLines || startsWithQuote(text) {
		return ""
	}
	if t, ok := sanitize.TrimQuotedText(text); ok {
		text = t
	}
	text = dropQuotes(text, m.OwnTextSet)
	text, _ = cutSignature(text)
	return text
}

// excerptText is what an excerpt shows of a message's stored plain text:
// the quoted history (sanitize.TrimQuotedText) and the signature cut off,
// reporting whether anything was cut.
func excerptText(text string) (string, bool) {
	cut := false
	if len(text) > maxInputBytes {
		text, cut = capBytes(text, maxInputBytes), true
	}
	if t, ok := sanitize.TrimQuotedText(text); ok {
		text, cut = t, true
	}
	if t, ok := cutSignature(text); ok {
		text, cut = t, true
	}
	return text, cut
}

// dropQuotes drops the ">"-quoted lines of text and cuts it at an
// attribution line as OwnText says; always cuts when cutAlways.
func dropQuotes(text string, cutAlways bool) string {
	lines := strings.Split(text, "\n")
	kept := make([]int, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSuffix(lines[i], "\r")
		if quoted(line) {
			continue
		}
		if !attributionLine(line) {
			kept = append(kept, i)
			continue
		}
		// A wrapped attribution ("On …, Jan <jan@example.org>" / "wrote:"):
		// a short line takes the one above it along.
		if n := len(kept); n > 0 && kept[n-1] == i-1 && len(strings.Fields(line)) < 4 {
			if prev := lines[i-1]; !blank(prev) && len(prev)+len(line) < maxAttributionBytes {
				kept = kept[:n-1]
			}
		}
		if !cutAlways {
			if next := nextNonBlank(lines, i+1); next >= 0 && quoted(lines[next]) {
				continue // an interleaved reply: only the attribution goes
			}
		}
		break
	}
	var b strings.Builder
	b.Grow(len(text))
	for n, i := range kept {
		if n > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(lines[i])
	}
	return strings.TrimFunc(b.String(), unicode.IsSpace)
}

// maxAttributionBytes bounds an attribution line (with the line above it
// when wrapped), as sanitize does.
const maxAttributionBytes = 400

// quoted reports whether line is quoted with ">".
func quoted(line string) bool {
	return strings.HasPrefix(strings.TrimLeft(line, " \t"), ">")
}

// blank reports whether line shows nothing.
func blank(line string) bool {
	return strings.TrimFunc(line, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.Is(unicode.Cf, r)
	}) == ""
}

func nextNonBlank(lines []string, from int) int {
	for j := from; j < len(lines); j++ {
		if !blank(lines[j]) {
			return j
		}
	}
	return -1
}

// attributionLine reports whether line reads as an attribution line ("On
// … wrote:" and its translations) by sanitize's own rule: a quote below it
// would be trimmed.
func attributionLine(line string) bool {
	t := strings.TrimRightFunc(line, unicode.IsSpace)
	if t == "" || len(t) > maxAttributionBytes || !strings.HasSuffix(t, ":") || quoted(t) {
		return false
	}
	const sentinel = "\u00a7" // a line of text above (and a blank line between, so it is not taken as the attribution's first half)
	out, ok := sanitize.TrimQuotedText(sentinel + "\n\n" + t + "\n> x")
	return ok && out == sentinel
}

// cutSignature cuts text before its first line that is exactly "-- " (a
// CR before the line break allowed).
func cutSignature(text string) (string, bool) {
	start := 0
	for start <= len(text) {
		end := strings.IndexByte(text[start:], '\n')
		line := text[start:]
		if end >= 0 {
			line = text[start : start+end]
		}
		if strings.TrimSuffix(line, "\r") == "-- " {
			return strings.TrimRight(text[:start], " \t\r\n"), true
		}
		if end < 0 {
			break
		}
		start += end + 1
	}
	return text, false
}

// capBytes cuts s to at most n bytes at a character boundary.
func capBytes(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	i := n
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return s[:i]
}

// questionMarks are the question marks hasQuestion looks for: ASCII, full
// width and Arabic.
const questionMarks = "?\uFF1F\u061F"

// hasQuestion reports whether text holds a question mark outside tokens
// that look like a URL ("://" or a leading "www.") or an address ("@"),
// where "?" starts a query string; a question mark in the sentence
// punctuation ending such a token ("… to anna@example.cz?") counts.
func hasQuestion(text string) bool {
	if !strings.ContainsAny(text, questionMarks) {
		return false
	}
	for _, tok := range strings.Fields(text) {
		if !strings.ContainsAny(tok, questionMarks) {
			continue
		}
		body := strings.TrimRight(tok, trailingPunct)
		if !urlLike(body) && !strings.Contains(body, "@") {
			return true
		}
		if strings.ContainsAny(tok[len(body):], questionMarks) {
			return true
		}
	}
	return false
}

// trailingPunct is the sentence punctuation that may follow a URL or an
// address in running text.
const trailingPunct = ".,;:!)]}>\"'»”’…" + questionMarks

// urlLike reports whether a whitespace-free token is or holds a URL:
// "scheme://…" anywhere in it, or "www." at its start after opening
// punctuation.
func urlLike(tok string) bool {
	if strings.Contains(tok, "://") {
		return true
	}
	tok = strings.TrimLeft(tok, openingPunct)
	return len(tok) >= 4 && strings.EqualFold(tok[:4], "www.")
}

// openingPunct is the punctuation that may open a URL in running text.
const openingPunct = "([{<\"'«“‘"

// forwardMarkers are the markers of thread.NormalizeSubject's set that say
// forwarded: English, German (WG), French (TR), Portuguese (ENC), Spanish
// (RV) and Polish (PD).
var forwardMarkers = map[string]bool{
	"fw": true, "fwd": true, "wg": true, "tr": true, "enc": true, "rv": true, "pd": true,
}

// forwardSubject reports whether the reply and forward markers
// thread.NormalizeSubject strips off subject include a forward marker.
func forwardSubject(subject string) bool {
	s := strings.TrimSpace(capBytes(subject, maxSubjectScan))
	rest := thread.NormalizeSubject(s)
	if len(rest) >= len(s) || !strings.HasSuffix(s, rest) {
		return false
	}
	prefix := s[:len(s)-len(rest)]
	for _, word := range strings.FieldsFunc(prefix, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z')
	}) {
		if forwardMarkers[strings.ToLower(word)] {
			return true
		}
	}
	return false
}

// startsWithQuote reports whether text has nothing of its own above a
// quoted history: its first line that is not blank starts one (a
// "Forwarded message" or "Original Message" separator, Outlook's header
// block, or an attribution with a quote to the end).
func startsWithQuote(text string) bool {
	// A line of its own above the text, so that TrimQuotedText has
	// something to keep, and a blank line between, which an Outlook header
	// block without a separator needs above it.
	const sentinel = "\u00a7"
	out, ok := sanitize.TrimQuotedText(sentinel + "\n\n" + capBytes(text, maxInputBytes))
	return ok && out == sentinel
}

// forwardShaped reports whether a message of the user's is shaped like a
// forward: a forward marker in its subject, a message/rfc822 part, a text
// that starts with a quoted history, or — for the message that starts the
// thread — a quoted history below less than shortOwnText of its own text.
func forwardShaped(m *Member, startsThread bool) bool {
	if forwardSubject(m.Subject) || m.HasMessagePart || startsWithQuote(m.Text) {
		return true
	}
	if !startsThread {
		return false
	}
	own, ok := sanitize.TrimQuotedText(capBytes(m.Text, maxInputBytes))
	return ok && len(strings.TrimSpace(own)) < shortOwnText
}
