// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package search

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// isTokenRune mirrors the token characters of SQLite's unicode61
// tokenizer: letters, numbers and private-use characters, plus the
// combining marks it folds away with remove_diacritics.
func isTokenRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.Is(unicode.Co, r) || unicode.Is(unicode.Mn, r)
}

// Fold is what unicode61 with remove_diacritics 2 compares: the text
// decomposed, its combining marks dropped, lower-cased ("Přílohy" and
// "prilohy" fold alike). Letters the tokenizer folds by a table of its own
// (ł, ø, ß) may differ here; that only ever affects which words an excerpt
// highlights, never which messages match.
func Fold(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range norm.NFD.String(s) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// Token is one run of token characters of a text: its byte span and its
// folded form.
type Token struct {
	Start, End int
	Key        string
}

// Tokens splits s the way the tokenizer does: every maximal run of token
// characters is a token, everything else separates.
func Tokens(s string) []Token {
	var out []Token
	start := -1
	for i, r := range s {
		if isTokenRune(r) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			out = append(out, Token{Start: start, End: i, Key: Fold(s[start:i])})
			start = -1
		}
	}
	if start >= 0 {
		out = append(out, Token{Start: start, End: len(s), Key: Fold(s[start:])})
	}
	return out
}
