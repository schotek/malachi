// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package mime

import (
	"strings"
	"unicode"
)

// Snippet condenses a plain-text body into a single line of at most
// maxRunes runes for the message list: quoted lines (starting with '>') are
// skipped, whitespace runs collapse to one space and control characters are
// dropped. A non-positive maxRunes yields "".
func Snippet(text string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	var b strings.Builder
	n := 0
	pendingSpace := false
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '>' {
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
