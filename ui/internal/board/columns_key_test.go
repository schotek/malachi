// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import "testing"

func TestSidewaysTarget(t *testing.T) {
	cases := []struct {
		name     string
		counts   []int
		from     int
		pos      int
		delta    int
		col, row int
		ok       bool
	}{
		{"right keeps position", []int{3, 4, 2, 1}, 0, 2, 1, 1, 2, true},
		{"right clamps to last", []int{3, 4, 2, 1}, 1, 3, 1, 2, 1, true},
		{"left keeps position", []int{3, 4, 2, 1}, 1, 1, -1, 0, 1, true},
		{"empty column is passed over", []int{3, 0, 0, 2}, 0, 1, 1, 3, 1, true},
		{"empty column left", []int{2, 0, 3}, 2, 2, -1, 0, 1, true},
		{"right edge", []int{3, 2}, 1, 0, 1, 0, 0, false},
		{"left edge", []int{3, 2}, 0, 0, -1, 0, 0, false},
		{"only empty beyond", []int{3, 0, 0}, 0, 0, 1, 0, 0, false},
		{"nothing selected counts as first", []int{3, 2}, 0, -1, 1, 1, 0, true},
		{"bad delta", []int{3, 2}, 0, 0, 2, 0, 0, false},
		{"bad column", []int{3, 2}, 5, 0, 1, 0, 0, false},
		{"no columns", nil, 0, 0, 1, 0, 0, false},
	}
	for _, c := range cases {
		col, row, ok := SidewaysTarget(c.counts, c.from, c.pos, c.delta)
		if ok != c.ok || (ok && (col != c.col || row != c.row)) {
			t.Errorf("%s: got (%d,%d,%v), want (%d,%d,%v)", c.name, col, row, ok, c.col, c.row, c.ok)
		}
	}
}
