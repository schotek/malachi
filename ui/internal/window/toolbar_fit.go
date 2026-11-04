// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/internal/toolbar"
)

// The overflow of the customizable header bars (T5, toolbar.Fit): while a
// header bar's items do not fit into the width its column would get
// (tbHeader.column), the items of the lowest priority are not packed; the
// Mail's More Actions offers them (toolbar_mail.go). A header bar has no
// overflow of its own and its items set its minimum width, so the width
// it is measured against is its column's share of the window, not its
// allocation (which never goes below that minimum).

// headerSpacing is the spacing of an Adw.HeaderBar's start and end boxes.
const headerSpacing = 6

// fitSlack is how much more room an item needs to come back than it took
// when it went, so a header bar at the edge does not flip on every frame.
const fitSlack = 12

// watchFit follows h's column and minimum width on every frame and fits
// the header bar again when either changed.
func (s *toolbarSet) watchFit(h *tbHeader) {
	h.header.AddTickCallback(func(gtk.Widgetter, gdk.FrameClocker) bool {
		s.checkFit(h)
		return true
	})
}

func (s *toolbarSet) checkFit(h *tbHeader) {
	if h.column == nil || h.fitting {
		return
	}
	avail := h.column()
	if avail <= 0 {
		return // not allocated yet
	}
	min, _, _, _ := h.header.Measure(gtk.OrientationHorizontal, -1)
	if avail == h.lastAvailable && min == h.lastMin {
		return
	}
	h.lastAvailable, h.lastMin = avail, min
	h.fitting = true
	idle(func() {
		h.fitting = false
		s.fit(h)
	})
}

// itemWidth is the natural width of it's visible widgets with the header
// bar's spacing; 0 when none shows.
func itemWidth(it *tbItem) int {
	total := 0
	for _, w := range it.widgets {
		base := gtk.BaseWidget(w)
		if !base.Visible() {
			continue
		}
		_, nat, _, _ := base.Measure(gtk.OrientationHorizontal, -1)
		total += nat + headerSpacing
	}
	return total
}

// fit decides h's overflow now and packs it again when it changed.
func (s *toolbarSet) fit(h *tbHeader) {
	if h.column == nil || len(h.entries) == 0 {
		return
	}
	widths := make([]int, len(h.entries))
	packedSum := 0
	for i, e := range h.entries {
		it := s.items[e.ID]
		if it == nil {
			continue
		}
		switch {
		case h.overflow[e.ID]:
			widths[i] = it.width
		case s.packed(e.ID):
			wd := itemWidth(it)
			if wd > 0 {
				it.width = wd
			}
			widths[i] = wd
			packedSum += wd
		}
	}
	min, _, _, _ := h.header.Measure(gtk.OrientationHorizontal, -1)
	available := h.column() - (min - packedSum)
	res := toolbar.Fit(gtkVariant, h.shownBar, h.entries, widths, available)
	if len(res.Overflow) < len(h.overflow) {
		// Coming back needs a little more room than going took.
		res = toolbar.Fit(gtkVariant, h.shownBar, h.entries, widths, available-fitSlack)
	}
	next := map[toolbar.ID]bool{}
	for _, id := range res.Overflow {
		next[id] = true
	}
	if sameSet(next, h.overflow) {
		return
	}
	h.overflow = next
	s.unpackFrom(h)
	s.packInto(h)
	if s.onOverflow != nil {
		s.onOverflow()
	}
}

func sameSet(a, b map[toolbar.ID]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}
