// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

// SidewaysTarget is where Left (delta -1) or Right (+1) leads in the
// Columns style: the neighbouring column's selectable row at the same
// position as the current one, the last one when that column is shorter;
// an empty column is passed over. selectable holds each column's count of
// selectable rows (cards and commitments, not headings or placeholders),
// from is the current column and position the current row among its
// selectable rows (a negative position, nothing selected, counts as the
// first). ok is false when nothing moves (the edge, or only empty columns
// beyond it); the key is consumed all the same. Reference: macOS
// BoardColumnsViewController.sideways.
func SidewaysTarget(selectable []int, from, position, delta int) (column, row int, ok bool) {
	if delta != -1 && delta != 1 {
		return 0, 0, false
	}
	if from < 0 || from >= len(selectable) {
		return 0, 0, false
	}
	position = max(position, 0)
	for i := from + delta; i >= 0 && i < len(selectable); i += delta {
		if n := selectable[i]; n > 0 {
			return i, min(position, n-1), true
		}
	}
	return 0, 0, false
}
