// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package board

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/pkg/api"
)

const (
	zwnj = 0x200C // zero width non-joiner: kept (Persian, Indic scripts)
	zwj  = 0x200D // zero width joiner: kept (emoji sequences)
	vs15 = 0xFE0E // variation selector 15 (text presentation): kept after an emoji
	vs16 = 0xFE0F // variation selector 16 (emoji presentation): kept after an emoji
)

// CleanText makes text from mail or from an assistant safe to show as
// plain text: valid UTF-8 (invalid bytes become U+FFFD), line breaks
// normalised to "\n" (CRLF, CR, NEL, U+2028, U+2029), every other
// whitespace character a space, and no control, format or other
// default-ignorable characters (bidi overrides, zero-width spaces, the
// Tags block, variation selectors, the combining grapheme joiner, Hangul
// fillers…) except ZWJ and ZWNJ, and VS15/VS16 right after an emoji.
// Nothing is collapsed. At most the first 1 MiB of s is looked at.
func CleanText(s string) string {
	s = capBytes(s, maxInputBytes)
	var b strings.Builder
	b.Grow(len(s))
	var last rune // the last character written, 0 at a line start
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		i += n
		switch {
		case r == utf8.RuneError && n == 1:
			r = utf8.RuneError
		case r == '\r':
			if i < len(s) && s[i] == '\n' {
				continue
			}
			r = '\n'
		case r == '\n' || r == 0x85 || r == 0x2028 || r == 0x2029:
			r = '\n'
		case unicode.IsSpace(r):
			r = ' '
		case unicode.IsControl(r):
			continue
		case r == vs15 || r == vs16:
			if !emojiBase(last) {
				continue
			}
		case r == zwnj || r == zwj:
		case ignorable(r):
			continue
		}
		if r == '\n' {
			b.WriteByte('\n')
			last = 0
			continue
		}
		b.WriteRune(r)
		last = r
	}
	return b.String()
}

// ignorable reports whether r shows nothing and is dropped: a format
// character (Cf) or a default-ignorable code point (Unicode's
// Default_Ignorable_Code_Point: Other_Default_Ignorable_Code_Point and the
// variation selectors besides Cf).
func ignorable(r rune) bool {
	return unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r) ||
		unicode.Is(unicode.Variation_Selector, r)
}

// emojiBase reports whether r can take an emoji or text presentation
// selector: a symbol (So, which holds the emoji), a keycap base, or one of
// the few emoji outside So.
func emojiBase(r rune) bool {
	switch {
	case r == '#' || r == '*' || (r >= '0' && r <= '9'):
		return true
	case r == 0x203C || r == 0x2049 || (r >= 0x2194 && r <= 0x21AA):
		return true
	}
	return unicode.Is(unicode.So, r)
}

// cleanField cleans a one-line field from mail (a subject, a name, an
// address): CleanText, every run of whitespace one space, trimmed, cut to
// max bytes. Unlike CleanLine it keeps URLs: they are part of what the
// sender wrote and the field is never a link.
func cleanField(s string, max int) string {
	out, _ := cut(strings.Join(strings.Fields(CleanText(s)), " "), max)
	return out
}

// CleanLine cleans an annotation string that is one line: CleanText, URLs
// removed (stripURLs), every run of whitespace — line breaks included —
// one space, trimmed. The result is cut to max bytes at a character
// boundary (max ≤ 0: no limit); fits is false when it had to be cut.
func CleanLine(s string, max int) (out string, fits bool) {
	out = strings.Join(strings.Fields(stripURLs(CleanText(s), "")), " ")
	return cut(out, max)
}

// CleanBlock cleans an annotation string that is a block (a summary):
// like CleanLine, but line breaks stay — each line's whitespace collapsed
// and trimmed, at most one empty line in a row, none at the start or end.
func CleanBlock(s string, max int) (out string, fits bool) {
	return cut(collapseBlock(stripURLs(CleanText(s), "")), max)
}

// CleanTasks cleans an annotation's tasks with CleanLine, dropping the
// ones that come out empty. fits is false when there were more than
// api.MaxBoardTasks or one was longer than api.MaxBoardTaskBytes; the
// result is then cut to those limits. Never nil.
func CleanTasks(tasks []string) (out []string, fits bool) {
	out, fits = []string{}, true
	for _, t := range tasks {
		c, ok := CleanLine(t, api.MaxBoardTaskBytes)
		fits = fits && ok
		if c == "" {
			continue
		}
		if len(out) == api.MaxBoardTasks {
			return out, false
		}
		out = append(out, c)
	}
	return out, fits
}

// cut cuts s to max bytes at a character boundary, trailing space
// trimmed.
func cut(s string, max int) (string, bool) {
	if max <= 0 || len(s) <= max {
		return s, true
	}
	return strings.TrimRightFunc(capBytes(s, max), unicode.IsSpace), false
}

// collapseBlock collapses the whitespace of every line of cleaned text to
// single spaces, trims the lines, keeps at most one empty line in a row
// and none at either end.
func collapseBlock(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	blank := 0
	for line := range strings.SplitSeq(s, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			blank++
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
			if blank > 0 {
				b.WriteByte('\n')
			}
		}
		blank = 0
		b.WriteString(line)
	}
	return b.String()
}

// stripURLs replaces in cleaned text every whitespace-separated token that
// is or holds a URL (urlLike: "scheme://…", or "www.…") by repl, keeping
// the line breaks. What stands before the URL in the token is kept when it
// holds a letter or a digit ("click:https://…" keeps "click:").
func stripURLs(s, repl string) string {
	if !strings.Contains(s, "://") && !strings.Contains(strings.ToLower(s), "www.") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		j := i
		for j < len(s) && s[j] != ' ' && s[j] != '\n' {
			j++
		}
		if tok := s[i:j]; !urlLike(tok) {
			b.WriteString(tok)
		} else {
			if p := urlPrefix(tok); strings.ContainsFunc(p, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) {
				b.WriteString(p)
			}
			b.WriteString(repl)
		}
		if j < len(s) {
			b.WriteByte(s[j])
			j++
		}
		i = j
	}
	return b.String()
}

// urlPrefix returns what stands before the URL in a urlLike token: before
// the scheme of its first "://" (letters, digits, "+", "-" and "."), or the
// opening punctuation before "www.".
func urlPrefix(tok string) string {
	k := strings.Index(tok, "://")
	if k < 0 {
		return tok[:len(tok)-len(strings.TrimLeft(tok, openingPunct))]
	}
	for k > 0 {
		c := tok[k-1]
		if c < 0x80 && (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.') {
			k--
			continue
		}
		break
	}
	return tok[:k]
}
