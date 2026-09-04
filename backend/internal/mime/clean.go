// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package mime

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// cleanField normalises a header-derived string: valid UTF-8, no control
// characters (which defeats CR/LF injection through encoded words),
// whitespace runs collapsed to one space, trimmed, and at most max bytes on
// a rune boundary (max <= 0 means no cap).
func cleanField(s string, max int) string {
	s = strings.ToValidUTF8(s, "�")
	var b strings.Builder
	b.Grow(len(s))
	pendingSpace := false
	for _, r := range s {
		switch {
		case unicode.IsControl(r):
			// Dropped, not spaced: "Ali\rce" reads "Alice" and an injected
			// "\r\nX-Header:" cannot become a line of its own.
			if r == '\t' {
				pendingSpace = b.Len() > 0
			}
		case unicode.IsSpace(r):
			pendingSpace = b.Len() > 0
		default:
			if pendingSpace {
				b.WriteByte(' ')
				pendingSpace = false
			}
			b.WriteRune(r)
		}
	}
	return truncateBytes(b.String(), max)
}

// cleanID is cleanField for message and content identifiers, which never
// legitimately contain whitespace.
func cleanID(s string, max int) string {
	s = cleanField(s, 0)
	if strings.IndexByte(s, ' ') >= 0 {
		s = strings.ReplaceAll(s, " ", "")
	}
	return truncateBytes(s, max)
}

// cleanText normalises a body: valid UTF-8, CRLF and bare CR folded to LF,
// tabs and newlines kept, every other control character dropped.
func cleanText(s string) string {
	s = strings.ToValidUTF8(s, "�")
	if !needsTextClean(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r == '\r':
			if i < len(s) && s[i] == '\n' {
				continue // the LF that follows is written on its own
			}
			b.WriteByte('\n')
		case unicode.IsControl(r):
			// dropped
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// needsTextClean reports whether s contains a control character other than
// LF and TAB; it is the fast path for well-formed bodies.
func needsTextClean(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 && c != '\n' && c != '\t' {
			return true
		}
		if c == 0x7f || c == 0xc2 { // DEL, or the lead byte of U+0080–U+009F
			return true
		}
	}
	return false
}

// isTextSpace reports whether r counts as whitespace for text output: the
// ASCII whitespace controls and Unicode spaces, but not control characters
// such as U+0085 NEL, which unicode.IsSpace also accepts and which are
// dropped instead.
func isTextSpace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '\f', '\v':
		return true
	}
	return unicode.IsSpace(r) && !unicode.IsControl(r)
}

// truncateBytes cuts s to at most max bytes on a rune boundary; max <= 0
// means no cap.
func truncateBytes(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
