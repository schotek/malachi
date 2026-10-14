// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"testing"

	"github.com/schotek/malachi/ui/internal/board"
)

func TestBoardPanelShown(t *testing.T) {
	cases := []struct {
		name         string
		style        board.Style
		hasSelection bool
		isEmpty      bool
		want         bool
	}{
		{"Columns with a selection", board.StyleColumns, true, false, true},
		{"Today with a selection", board.StyleToday, true, false, true},
		{"Columns without a selection", board.StyleColumns, false, false, false},
		{"List never shows the panel, even with a selection", board.StyleList, true, false, false},
		{"an empty board never shows the panel", board.StyleColumns, true, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := boardPanelShown(c.style, c.hasSelection, c.isEmpty); got != c.want {
				t.Errorf("boardPanelShown(%v, %v, %v) = %v, want %v", c.style, c.hasSelection, c.isEmpty, got, c.want)
			}
		})
	}
}

func TestBoardDetailBelongsToPanel(t *testing.T) {
	cases := []struct {
		style board.Style
		want  bool
	}{
		{board.StyleList, false},
		{board.StyleColumns, true},
		{board.StyleToday, true},
	}
	for _, c := range cases {
		if got := boardDetailBelongsToPanel(c.style); got != c.want {
			t.Errorf("boardDetailBelongsToPanel(%v) = %v, want %v", c.style, got, c.want)
		}
	}
}
