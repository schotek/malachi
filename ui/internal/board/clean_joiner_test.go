// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import "testing"

// TestCleanJoiners: the joiner rule is the daemon's (backend
// internal/board TestCleanTextJoiners, the same cases), in CleanLine and in
// CleanBlock.
func TestCleanJoiners(t *testing.T) {
	const zj, znj = "‍", "‌"
	for _, c := range []struct{ name, in, line, block string }{
		{"emoji zwj sequence", "\U0001F468" + zj + "\U0001F469" + zj + "\U0001F467", "\U0001F468" + zj + "\U0001F469" + zj + "\U0001F467", ""},
		{"heart on fire keeps vs16 and zwj", "❤️" + zj + "\U0001F525", "❤️" + zj + "\U0001F525", ""},
		{"persian zwnj", "می" + znj + "خ", "می" + znj + "خ", ""},
		{"leading", zj + "a", "a", ""},
		{"trailing", "a" + znj, "a", ""},
		{"before a space", "a" + zj + " b", "a b", ""},
		{"after a space", "a " + zj + "b", "a b", ""},
		{"at a line end", "a" + zj + "\nb", "a b", "a\nb"},
		{"at a line start", "a\n" + znj + "b", "a b", "a\nb"},
		{"a run of joiners", "a" + zj + zj + "b", "ab", ""},
		{"mixed run", "a" + zj + znj + "b", "ab", ""},
		{"invisible between is dropped first", "a" + zj + "​b", "a" + zj + "b", ""},
		{"vs16 after a joiner goes", "❤" + zj + "️\U0001F525", "❤" + zj + "\U0001F525", ""},
		{"only joiners", zj + znj + zj, "", ""},
		{"joiner then a control", "a" + zj + "\x00", "a", ""},
		{"bidi override around", "a" + zj + "‮b", "a" + zj + "b", ""},
	} {
		if got := CleanLine(c.in, 100); got != c.line {
			t.Errorf("CleanLine %s: %+q, want %+q", c.name, got, c.line)
		}
		want := c.block
		if want == "" {
			want = c.line
		}
		if got := CleanBlock(c.in, 100); got != want {
			t.Errorf("CleanBlock %s: %+q, want %+q", c.name, got, want)
		}
	}
}
