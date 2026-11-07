// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistant

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The board's Suggest Reply: the request's command line and message (macOS
// BoardSuggestReplyTests, the part of Assistant).

func TestCleanSuggestReplyInstruction(t *testing.T) {
	tests := []struct{ in, want string }{
		{"a\x00b\x07c", "abc"},
		{"x‮y⁦z", "xyz"},
		{"\t one \r\n  two ", "one two"},
		{"", ""},
		{"   ", ""},
	}
	for _, tt := range tests {
		if got := CleanSuggestReplyInstruction(tt.in); got != tt.want {
			t.Errorf("CleanSuggestReplyInstruction(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	long := strings.Repeat("é", 600)
	if n := utf8.RuneCountInString(CleanSuggestReplyInstruction(long)); n != 500 {
		t.Errorf("a long one keeps %d characters", n)
	}
	c := CleanSuggestReplyInstruction(strings.Repeat("ab ", 300))
	if utf8.RuneCountInString(c) > 500 || strings.HasSuffix(c, " ") {
		t.Errorf("a spaced one: %d characters, %q at the end", utf8.RuneCountInString(c), c[len(c)-3:])
	}
	// Invalid UTF-8 is a replacement character, not lost words.
	if got := CleanSuggestReplyInstruction("a\xffb"); got != "a�b" {
		t.Errorf("invalid UTF-8: %q", got)
	}
}
