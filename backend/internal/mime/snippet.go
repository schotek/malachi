// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package mime

import (
	"strings"
	"unicode"
)

// Snippet condenses a plain-text body into a single line of at most
// maxRunes runes for the message list: quoted lines (starting with '>') and
// rules (lines of separator characters only, as "____" or "-----" above a
// meeting invitation's text) are skipped, whitespace runs collapse to one
// space and control characters are dropped. A non-positive maxRunes yields
// "".
func Snippet(text string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	var b strings.Builder
	n := 0
	pendingSpace := false
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '>' || isRule(line) {
			continue
		}
		for _, r := range line {
			if isTextSpace(r) {
				pendingSpace = n > 0
				continue
			}
			if unicode.IsControl(r) {
				continue
			}
			if pendingSpace {
				if n+2 > maxRunes {
					return b.String()
				}
				b.WriteByte(' ')
				n++
				pendingSpace = false
			}
			if n >= maxRunes {
				return b.String()
			}
			b.WriteRune(r)
			n++
		}
		pendingSpace = n > 0
	}
	return b.String()
}

// isRule reports whether line is made of separator characters only (with
// spaces between them): underscores, hyphens, dashes, equals signs,
// asterisks, tildes and box-drawing lines.
func isRule(line string) bool {
	for _, r := range line {
		switch {
		case isTextSpace(r):
		case strings.ContainsRune("_-=*~#.", r), r >= '\u2010' && r <= '\u2015', r >= '\u2500' && r <= '\u257F':
		default:
			return false
		}
	}
	return true
}
