// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package update

import (
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Notes and notice texts are plain text that reaches a user. The contract
// (docs/services.md §2.3) lets the daemon clean them; the owner's tool
// refuses what cleaning would change, so the daemon does too: a text is
// accepted only when it is already clean, and a manifest with one that
// is not is discarded whole. Clean means valid UTF-8 in NFC with "\n" as
// the only line break and no control, format (bidi, zero-width),
// line-separator, filler or noncharacter code point, except the zero
// width joiner inside an emoji sequence.

func validUTF8(b []byte) bool { return utf8.Valid(b) }

// textProblem says what is wrong with s, or "" when it is clean.
func textProblem(s string, bad bool) string {
	if bad {
		return "lone surrogate"
	}
	runes := []rune(s)
	for i, r := range runes {
		switch {
		case r == '\n':
		case r < 0x20 || (r >= 0x7f && r <= 0x9f):
			return "control character"
		case r == 0x200d: // zero width joiner
			if !joinsEmoji(runes, i) {
				return "zero width joiner outside an emoji sequence"
			}
		case unicode.Is(unicode.Cf, r):
			return "format character"
		case r == 0x2028 || r == 0x2029:
			return "line or paragraph separator"
		case invisible(r):
			return "invisible character"
		case r == utf8.RuneError:
			return "replacement character"
		case noncharacter(r):
			return "noncharacter"
		}
	}
	if !norm.NFC.IsNormalString(s) {
		return "not in NFC"
	}
	return ""
}

// invisible are letters and marks that render as nothing: the combining
// grapheme joiner, the Hangul fillers and the Khmer inherent vowels.
func invisible(r rune) bool {
	switch r {
	case 0x034f, 0x115f, 0x1160, 0x17b4, 0x17b5, 0x3164, 0xffa0:
		return true
	}
	return false
}

func noncharacter(r rune) bool {
	return (r >= 0xfdd0 && r <= 0xfdef) || r&0xfffe == 0xfffe
}

// joinsEmoji: the joiner at i stands between two emoji (the one before
// may carry a variation selector or a skin tone).
func joinsEmoji(runes []rune, i int) bool {
	if i == 0 || i+1 >= len(runes) || !pictographic(runes[i+1]) {
		return false
	}
	j := i - 1
	for j > 0 && (runes[j] == 0xfe0f || (runes[j] >= 0x1f3fb && runes[j] <= 0x1f3ff)) {
		j--
	}
	return pictographic(runes[j])
}

// pictographic approximates Unicode's Extended_Pictographic, which the
// standard library has no table for: the emoji blocks and the older
// symbol blocks emoji sequences use.
func pictographic(r rune) bool {
	switch {
	case r >= 0x1f000 && r <= 0x1faff,
		r >= 0x2600 && r <= 0x27bf,
		r >= 0x2300 && r <= 0x23ff,
		r >= 0x2b00 && r <= 0x2bff,
		r >= 0x2190 && r <= 0x21ff,
		r == 0x00a9, r == 0x00ae, r == 0x203c, r == 0x2049, r == 0x2122, r == 0x2139:
		return true
	}
	return false
}
