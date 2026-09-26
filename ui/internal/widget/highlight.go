// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package widget

import (
	"sort"
	"unicode/utf8"

	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/backend/pkg/api"
)

// maxHighlights caps the bold ranges of one label.
const maxHighlights = 32

// validRanges keeps the ranges that can be applied to text as they are:
// inside it, not empty, starting and ending on a character boundary, in
// order and not overlapping, at most maxHighlights. The daemon promises
// all of that (docs/api.md, search.query); a range that breaks it is
// dropped rather than trusted, since a byte index into the middle of a
// character is what Pango would mis-render.
func validRanges(text string, rs []api.MatchRange) []api.MatchRange {
	boundary := func(i int) bool { return i == len(text) || utf8.RuneStart(text[i]) }
	out := make([]api.MatchRange, 0, len(rs))
	for _, r := range rs {
		if r.Start < 0 || r.Start >= r.End || r.End > len(text) || !boundary(r.Start) || !boundary(r.End) {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	kept := out[:0]
	end := -1
	for _, r := range out {
		if r.Start < end {
			continue
		}
		kept = append(kept, r)
		end = r.End
		if len(kept) == maxHighlights {
			break
		}
	}
	return kept
}

// highlightAttrs turns the ranges into bold attributes for a label that
// shows text; nil without any, which clears earlier ones.
func highlightAttrs(text string, rs []api.MatchRange) *pango.AttrList {
	rs = validRanges(text, rs)
	if len(rs) == 0 {
		return nil
	}
	list := pango.NewAttrList()
	for _, r := range rs {
		a := pango.NewAttrWeight(pango.WeightBold)
		a.SetStartIndex(uint(r.Start))
		a.SetEndIndex(uint(r.End))
		list.Insert(a)
	}
	return list
}
