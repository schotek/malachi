// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import "testing"

// An HTML body takes its own height only when it is known and fits the
// room the page has; the comparison is the same whichever way the view is
// sized now, so a body that just fits stays fitted.
func TestFitsBody(t *testing.T) {
	for _, c := range []struct {
		px, room int
		want     bool
	}{
		{-1, 700, false}, // not measured yet
		{0, 700, true},
		{128, 700, true},
		{700, 700, true},
		{701, 700, false},
		{128, 0, false}, // the page not laid out yet
	} {
		if got := fitsBody(c.px, c.room); got != c.want {
			t.Errorf("fitsBody(%d, %d) = %v, want %v", c.px, c.room, got, c.want)
		}
	}
}
