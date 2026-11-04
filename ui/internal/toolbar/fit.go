// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package toolbar

// FitResult is what of a bar's entries shows in the room it has (T5).
type FitResult struct {
	// Visible is, per entry of the Present list, whether it shows: false
	// for an entry Present hid (its feature missing) and for one that did
	// not fit.
	Visible []bool
	// Overflow are the items that did not fit, in the layout's order: the
	// client offers them in its More Actions menu or its » button (an
	// entry Present hid is not among them).
	Overflow []ID
}

// Fit decides which of shown (a run of PresentFor that one strip or
// header bar draws) fit into available, widths[i] being the width shown[i]
// takes, its gap included (a flexible space its minimum, usually 0).
// While the total is too wide, the item with the lowest visibility
// priority goes, of equal ones the rightmost (as AppKit's overflow);
// separators, spaces, fixed items and items of PriorityUser and above
// never go, nor does an item of no width. Missing or negative widths
// count as 0; an entry hidden by
// Present takes no room. Pure: the client measures, Fit decides.
func Fit(v Variant, bar Bar, shown []Shown, widths []int, available int) FitResult {
	n := len(shown)
	res := FitResult{Visible: make([]bool, n)}
	width := func(i int) int {
		if i < len(widths) && widths[i] > 0 {
			return widths[i]
		}
		return 0
	}
	total := 0
	for i, e := range shown {
		if e.Hidden {
			continue
		}
		res.Visible[i] = true
		total += width(i)
	}
	priority := func(i int) (int, bool) {
		id := shown[i].ID
		if IsSpace(id) || IsSeparator(id) {
			return 0, false
		}
		it, ok := LookupFor(v, bar, id)
		if !ok {
			return PriorityStandard, true
		}
		if it.Fixed || it.Priority >= PriorityUser {
			return 0, false
		}
		return it.Priority, true
	}
	gone := make([]bool, n)
	for total > available {
		pick, best := -1, 0
		for i := n - 1; i >= 0; i-- {
			if !res.Visible[i] || width(i) == 0 {
				continue
			}
			p, ok := priority(i)
			if !ok {
				continue
			}
			if pick < 0 || p < best {
				pick, best = i, p
			}
		}
		if pick < 0 {
			break
		}
		res.Visible[pick] = false
		gone[pick] = true
		total -= width(pick)
	}
	for i, g := range gone {
		if g {
			res.Overflow = append(res.Overflow, shown[i].ID)
		}
	}
	return res
}
