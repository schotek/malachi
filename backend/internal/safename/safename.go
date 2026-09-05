// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package safename turns attacker-controlled file names (MIME attachment
// names, names supplied by the UI for imports) into names that are safe to
// store and display: no path separators, no control characters, no leading
// dots, bounded length. See docs/security.md §4.
package safename

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxBytes is the longest name produced, in bytes (Linux NAME_MAX).
const MaxBytes = 255

// Fallback is returned when nothing usable remains.
const Fallback = "attachment"

// Filename sanitises raw. It keeps the last path component only, drops
// control, bidi-control and invalid characters, strips leading dots and
// surrounding whitespace, and truncates on a rune boundary keeping the
// extension when possible.
func Filename(raw string) string {
	s := strings.ToValidUTF8(raw, "")
	// Last path component, treating both separators as separators.
	if i := strings.LastIndexAny(s, `/\`); i >= 0 {
		s = s[i+1:]
	}
	var b strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) || bidiControl(r) || r == utf8.RuneError {
			continue
		}
		b.WriteRune(r)
	}
	s = strings.TrimSpace(b.String())
	s = strings.TrimLeft(s, ".")
	s = strings.TrimSpace(s)
	if s == "" {
		return Fallback
	}
	if len(s) > MaxBytes {
		s = truncate(s)
	}
	return s
}

// bidiControl reports the Unicode bidirectional formatting characters.
// They are not control characters to unicode.IsControl, yet a
// RIGHT-TO-LEFT OVERRIDE makes "photo‮gnp.exe" display as
// "photo.exe.png": the extension the user sees is not the one that opens.
func bidiControl(r rune) bool {
	switch {
	case r == 0x061C: // ARABIC LETTER MARK
		return true
	case r == 0x200E || r == 0x200F: // LRM, RLM
		return true
	case 0x202A <= r && r <= 0x202E: // LRE, RLE, PDF, LRO, RLO
		return true
	case 0x2066 <= r && r <= 0x2069: // LRI, RLI, FSI, PDI
		return true
	}
	return false
}

// truncate shortens s to MaxBytes on a rune boundary, keeping a short
// extension if there is one.
func truncate(s string) string {
	ext := ""
	if i := strings.LastIndexByte(s, '.'); i > 0 && len(s)-i <= 16 {
		ext = s[i:]
	}
	base := strings.TrimSuffix(s, ext)
	room := MaxBytes - len(ext)
	for len(base) > room {
		_, size := utf8.DecodeLastRuneInString(base)
		base = base[:len(base)-size]
	}
	if base == "" {
		return Fallback + ext
	}
	return base + ext
}
