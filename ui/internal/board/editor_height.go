// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import "math"

// How tall the board's inline reply editor is: as tall as its content,
// never shorter than a few lines, never taller than most of what the
// detail shows (above that it scrolls inside). The macOS client leads
// (MalachiCore/Board/BoardEditorHeight.swift); this is its port. Heights
// are in the toolkit's logical pixels.

// The bounds of the editor's height.
const (
	// EditorMinHeight: never shorter, a few lines to type into, even when
	// empty.
	EditorMinHeight = 160.0
	// EditorMaxHeight: never taller, however tall the detail.
	EditorMaxHeight = 480.0
	// EditorVisibleShare: never taller than this share of the detail's
	// visible height (but never below EditorMinHeight).
	EditorVisibleShare = 0.6
)

// EditorHeight is the inline reply editor's height for a content height
// and the height of the detail's visible part.
type EditorHeight struct {
	// Height is the editor's height.
	Height float64
	// Scrolls: the content is taller than Height; the editor scrolls
	// inside.
	Scrolls bool
}

// NewEditorHeight clamps the content height to [EditorMinHeight,
// EditorHeightCap(visible)]. A content height that is not a finite number
// counts as empty.
func NewEditorHeight(content, visible float64) EditorHeight {
	limit := EditorHeightCap(visible)
	c := content
	if math.IsNaN(c) || math.IsInf(c, 0) {
		c = 0
	}
	h := math.Min(math.Max(c, EditorMinHeight), limit)
	return EditorHeight{Height: h, Scrolls: c > h}
}

// EditorHeightCap is min(EditorMaxHeight, EditorVisibleShare × visible),
// never below EditorMinHeight; a visible height that is not a finite
// positive number gives EditorMaxHeight.
func EditorHeightCap(visible float64) float64 {
	if math.IsNaN(visible) || math.IsInf(visible, 0) || visible <= 0 {
		return EditorMaxHeight
	}
	return math.Max(EditorMinHeight, math.Min(EditorMaxHeight, EditorVisibleShare*visible))
}
