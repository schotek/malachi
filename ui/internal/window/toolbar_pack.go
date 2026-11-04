// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"strings"

	"github.com/schotek/malachi/ui/internal/toolbar"
)

// The pure half of the customizable header bars (toolbar_host.go): how a
// run of toolbar.PresentFor becomes the start and end of one
// Adw.HeaderBar, and how wide the columns of the Mail mode are for the
// overflow (toolbar.Fit). No widgets here; tested in toolbar_pack_test.go.

// gtkVariant is the GTK client's variant of the toolbars (T12).
var gtkVariant = toolbar.GTK

// toolbarGap is the width of a toolbar.Space: the margin it adds to the
// item before it, as the 12 px trash_button and the eWay-CRM slot had.
const toolbarGap = 12

// headerPack is what one header bar packs: the items at its start and its
// end, both in screen order (left to right), and the items a Space
// follows, which get toolbarGap as their end margin.
type headerPack struct {
	Start, End []toolbar.ID
	Gap        map[toolbar.ID]bool
}

// packHeader splits a header bar's entries (one or more sections of
// PresentFor, separators left out) at the last flexible space: before it
// PackStart, after it PackEnd. Earlier flexible spaces mean nothing in a
// header bar, which has one free room, its title; without a flexible
// space everything stands at the start. Entries hidden by Present are
// packed all the same (T7: their code shows them in place). A Space gives
// the item before it on the same side the gap, unless that item spaces
// itself (selfSpaced: the eWay-CRM slot, whose buttons carry 12 px
// margins on both sides); a Space with no item before it on its side
// gives nothing.
func packHeader(entries []toolbar.Shown, selfSpaced func(toolbar.ID) bool) headerPack {
	p := headerPack{Gap: map[toolbar.ID]bool{}}
	split := -1
	for i, e := range entries {
		if e.ID == toolbar.FlexibleSpace {
			split = i
		}
	}
	side := func(from, to int) []toolbar.ID {
		var out []toolbar.ID
		var prev toolbar.ID
		for _, e := range entries[from:to] {
			switch {
			case toolbar.IsSeparator(e.ID), e.ID == toolbar.FlexibleSpace:
			case e.ID == toolbar.Space:
				if prev != "" && (selfSpaced == nil || !selfSpaced(prev)) {
					p.Gap[prev] = true
				}
			default:
				out = append(out, e.ID)
				prev = e.ID
			}
		}
		return out
	}
	if split < 0 {
		p.Start = side(0, len(entries))
		return p
	}
	p.Start = side(0, split)
	p.End = side(split+1, len(entries))
	return p
}

// sectionEntries are the entries of PresentFor that stand in the sections
// secs of bar, in order, separators left out.
func sectionEntries(bar toolbar.Bar, shown []toolbar.Shown, secs []toolbar.Section) []toolbar.Shown {
	want := map[toolbar.Section]bool{}
	for _, s := range secs {
		want[s] = true
	}
	cur := toolbar.SectionMain
	if all := toolbar.Sections(bar); len(all) > 0 {
		cur = all[0]
	}
	var out []toolbar.Shown
	for _, e := range shown {
		if toolbar.IsSeparator(e.ID) {
			cur = toolbar.Section(strings.TrimPrefix(string(e.ID), "|"))
			continue
		}
		if want[cur] {
			out = append(out, e)
		}
	}
	return out
}

// mailColumns are the widths the Mail mode's three header bars get in a
// window w wide, with the assistant panel beside them taking panel (0
// while it overlays or is closed): the folders' column, the list's and
// the message's, as Adw.NavigationSplitView divides them
// (window.blp: sidebar-width-fraction clamped to min/max-sidebar-width);
// a collapsed split shows one page at a time, each as wide as the split.
// They are what the header bars would get without their minimum widths,
// so the overflow can make room before the window grows (toolbar_fit.go).
func mailColumns(w, panel int, outerCollapsed, innerCollapsed bool) (folders, list, message int) {
	w -= panel
	if w < 0 {
		w = 0
	}
	content := w
	folders = w
	if !outerCollapsed {
		folders = clampInt(w*20/100, 200, 320)
		content = max(w-folders, 0)
	}
	list, message = content, content
	if !innerCollapsed {
		list = clampInt(content*38/100, 280, 460)
		message = max(content-list, 0)
	}
	return folders, list, message
}

// assistantPanelWidth is the width the open, unfolded assistant panel
// takes from the window (window.blp assistant_split).
func assistantPanelWidth(w int) int { return clampInt(w*28/100, 280, 480) }

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
