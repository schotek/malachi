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
// control and invalid characters, strips leading dots and surrounding
// whitespace, and truncates on a rune boundary keeping the extension when
// possible.
func Filename(raw string) string {
	s := strings.ToValidUTF8(raw, "")
	// Last path component, treating both separators as separators.
	if i := strings.LastIndexAny(s, `/\`); i >= 0 {
		s = s[i+1:]
	}
	var b strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) || r == utf8.RuneError {
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
