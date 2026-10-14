// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"testing"

	"github.com/schotek/malachi/ui/internal/board"
)

func TestMessageAccelsAllowed(t *testing.T) {
	cases := []struct {
		mode         board.Mode
		typingAllows bool
		want         bool
	}{
		{board.ModeMail, true, true},
		{board.ModeMail, false, false},
		{board.ModeBoard, true, false},
		{board.ModeBoard, false, false},
	}
	for _, c := range cases {
		if got := messageAccelsAllowed(c.mode, c.typingAllows); got != c.want {
			t.Errorf("messageAccelsAllowed(%v, %v) = %v, want %v", c.mode, c.typingAllows, got, c.want)
		}
	}
}

func TestBoardStatusText(t *testing.T) {
	cases := []struct {
		name             string
		base, triageNote string
		want             string
	}{
		{"no triage note: the base line", "Up to date", "", "Up to date"},
		{"a triage note replaces the base line", "Up to date", "Triaging…", "Triaging…"},
		{"an empty base with a note still shows it", "", "Triaging…", "Triaging…"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := boardStatusText(c.base, c.triageNote); got != c.want {
				t.Errorf("boardStatusText(%q, %q) = %q, want %q", c.base, c.triageNote, got, c.want)
			}
		})
	}
}
