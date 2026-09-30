// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"math"
	"slices"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/conversation"
)

// The arithmetic of the conversation view that needs no GTK: which cards
// are near enough to the viewport to hold a body and a web view, where the
// viewport goes to keep an item in place and for a page of Space, the
// timeline in the gutter beside the cards (which item gets an avatar and
// which a dot, where the line runs), the short dates of a narrow pane, and
// how a card's web view follows the height of the document it shows. The
// pane (conversation_view.go, conversation_card.go, conversation_rows.go
// and htmlview.Card) applies it. A port of the macOS client's
// MalachiCore/Model/ConversationLayout.swift, with its tests.

// The conversation view's rules for keeping cards cheap: every card is a
// native widget, but a body is fetched only for the cards within
// convLiveScreens screens of the viewport, and a web view exists only for at
// most convMaxLiveWebViews of those, the nearest first.
const (
	// convLiveScreens is how many viewport heights above and below the
	// visible part of the stack count as near.
	convLiveScreens = 2.0
	// convMaxLiveWebViews is the most web views the pane keeps at once.
	convMaxLiveWebViews = 8
	// convEstimatedBodyHeight is the height a card's body is given until
	// it is known.
	convEstimatedBodyHeight = 160
	// convInitialWebHeight is the height a web view starts from before its
	// document reports one.
	convInitialWebHeight = 120
)

// The measures of the conversation's column, in pixels: a gutter with the
// timeline (a line through the avatars of the messages and the dots of the
// events), and beside it the cards.
const (
	// convMaxWidth is the widest the column gets, the gutter included.
	convMaxWidth = 900
	// convSideInset is between the pane's edge and the column, on both
	// sides.
	convSideInset = 16
	// convAvatar is the avatar of a message, which is also the gutter's
	// width.
	convAvatar = 28
	// convGutterGap is between the gutter and the cards.
	convGutterGap = 12
	// convItemGap is between two items of the stack.
	convItemGap = 12
	// convDot is the dot of an event and of the row of older messages.
	convDot = 7
	// convLine is the line's width.
	convLine = 1
	// convLineBreak is between the line's end and the avatar or dot it
	// runs to.
	convLineBreak = 3
	// convCardRadius is a card's corner radius; convCardPaddingV and
	// convCardPaddingH the padding inside it.
	convCardRadius   = 10
	convCardPaddingV = 10
	convCardPaddingH = 14
	// convCompactHeader: a header narrower than this shows the short date
	// of the list instead of the full date and time.
	convCompactHeader = 420
)

// convSpan is a vertical stretch of the stack, in the document's
// coordinates (top down).
type convSpan struct {
	Min, Max float64
}

// convSpanOf is the stretch from minY to maxY; an inverted one is empty at its
// top.
func convSpanOf(minY, maxY float64) convSpan {
	return convSpan{Min: minY, Max: math.Max(minY, maxY)}
}

// distance is how far s is from o: 0 when they overlap (touching counts).
func (s convSpan) distance(o convSpan) float64 {
	switch {
	case s.Max < o.Min:
		return o.Min - s.Max
	case s.Min > o.Max:
		return s.Min - o.Max
	}
	return 0
}

// convLive is what liveCards decided: the items near the viewport (a body
// is fetched for them) and those of them that get a web view.
type convLive struct {
	Near map[int]bool
	Web  map[int]bool
}

// liveCards is the items of the stack at frames near visible (within
// screens viewport heights of it; an empty viewport counts the first
// screen from its top), and of those the ones whose body is HTML (html,
// same indices as frames) that get a web view: the nearest to the visible
// part first, ties by position, at most limit.
func liveCards(frames []convSpan, html []bool, visible convSpan, screens float64, limit int) convLive {
	height := math.Max(visible.Max-visible.Min, 1)
	window := convSpanOf(visible.Min-screens*height, visible.Max+screens*height)
	out := convLive{Near: map[int]bool{}, Web: map[int]bool{}}
	type candidate struct {
		index    int
		distance float64
	}
	var candidates []candidate
	for i, f := range frames {
		if f.distance(window) != 0 {
			continue
		}
		out.Near[i] = true
		if i < len(html) && html[i] {
			candidates = append(candidates, candidate{i, f.distance(visible)})
		}
	}
	slices.SortStableFunc(candidates, func(a, b candidate) int {
		switch {
		case a.distance < b.distance:
			return -1
		case a.distance > b.distance:
			return 1
		}
		return a.index - b.index
	})
	for i, c := range candidates {
		if i >= max(limit, 0) {
			break
		}
		out.Web[c.index] = true
	}
	return out
}

// anchoredTop is where the viewport's top goes to keep an item in place:
// the item that was offset below the viewport's top is at itemTop now; the
// result is clamped to the document (documentHeight, a viewport
// viewportHeight tall).
func anchoredTop(itemTop, offset, documentHeight, viewportHeight float64) float64 {
	maxTop := math.Max(0, documentHeight-viewportHeight)
	return math.Min(math.Max(0, itemTop+offset), maxTop)
}

// pageTop is where the viewport's top goes for one page down (or up) of
// Space: the viewport's height less overlap, at least half a viewport,
// clamped to the document.
func pageTop(from float64, up bool, viewportHeight, documentHeight, overlap float64) float64 {
	step := math.Max(viewportHeight-overlap, viewportHeight/2)
	if up {
		step = -step
	}
	maxTop := math.Max(0, documentHeight-viewportHeight)
	return math.Min(math.Max(0, from+step), maxTop)
}

// convMarker is what marks an item on the timeline.
type convMarker int

const (
	// markAvatar is the sender's avatar, its top at the top of the card.
	markAvatar convMarker = iota
	// markDot is a small dot beside the first line of the text.
	markDot
)

// convRail is an item's piece of the timeline.
type convRail struct {
	Marker convMarker
	// Accent tints the avatar with the accent colour: the user's own
	// message (conversation.Item.Mine). Never set for a dot.
	Accent bool
	// Above and Below: the line runs from the item above down to the
	// marker, and from the marker down to the item below.
	Above, Below bool
}

// convDisplay is the stack as the pane shows it (convDisplayOrder): the
// items in order, which of them opened the conversation (root, an index
// into items; -1 when it is not shown), and whether that card starts
// folded to its header.
type convDisplay struct {
	items      []conversation.Item
	root       int
	rootFolded bool
}

// convDisplayOrder is the order the pane shows the model's items in, as
// Jira shows an issue: what opened the conversation first (convRoot: the
// issue's description, or the oldest message of a mail conversation that
// thread.get did not cut), then the rest newest first, so that the newest
// is what the pane opens on right under it, and the row of older members
// left out last. The opening card starts folded while another message card
// follows it (a conversation of one message and its status changes shows
// that message whole). The model (ui/internal/conversation) keeps the
// items oldest first, as the macOS client shows them; items is not
// changed.
func convDisplayOrder(items []conversation.Item) convDisplay {
	d := convDisplay{items: make([]conversation.Item, 0, len(items)), root: -1}
	root := convRoot(items)
	if root >= 0 {
		d.items = append(d.items, items[root])
		d.root = 0
	}
	var truncated []conversation.Item
	for i := len(items) - 1; i >= 0; i-- {
		switch {
		case i == root:
		case items[i].Kind == conversation.ItemTruncated:
			truncated = append(truncated, items[i])
		default:
			d.items = append(d.items, items[i])
			if items[i].Kind == conversation.ItemMessage {
				d.rootFolded = root >= 0
			}
		}
	}
	d.items = append(d.items, truncated...)
	return d
}

// convRoot is the index in items (the model's, oldest first) of the item
// that opened the conversation: the description of a Jira issue wherever
// it is, else the oldest member when it is a message card and no older
// member is left out (no truncated row before it); -1 for none.
func convRoot(items []conversation.Item) int {
	for i, it := range items {
		if it.Kind == conversation.ItemMessage && it.Message.Issue != nil && it.Message.Issue.Item == api.IssueItemDescription {
			return i
		}
	}
	if len(items) > 0 && items[0].Kind == conversation.ItemMessage {
		return 0
	}
	return -1
}

// convRails is the timeline of items, one piece each: a message has its
// sender's avatar (accent-tinted when it is the user's own), an event and
// the row of older messages a dot. The line runs between the markers, from
// the first item's to the last item's, so a conversation of one item has
// none.
func convRails(items []conversation.Item) []convRail {
	out := make([]convRail, len(items))
	for i, it := range items {
		message := it.Kind == conversation.ItemMessage
		out[i] = convRail{Marker: markDot, Above: i > 0, Below: i < len(items)-1}
		if message {
			out[i].Marker = markAvatar
			out[i].Accent = it.Mine
		}
	}
	return out
}

// convCardWidth is the width of the cards in a pane pane wide: the column
// less the insets and the gutter; never negative.
func convCardWidth(pane float64) float64 {
	return math.Max(0, math.Min(pane, convMaxWidth)-2*convSideInset-convAvatar-convGutterGap)
}

// convCompactDates reports whether the cards of a pane pane wide show the
// short date (their header is narrower than convCompactHeader).
func convCompactDates(pane float64) bool {
	return convCardWidth(pane)-2*convCardPaddingH < convCompactHeader
}

// The web view of a card follows the height of its document. The document
// reports its height in CSS pixels whenever it changes (the view's own
// script in an isolated world, htmlview.Card); the view becomes that tall,
// at the page zoom, up to webMaxHeight (beyond it the card scrolls
// inside). Content sized by the viewport (100vh, height: 100%) would grow
// with every step the view grows: a report the document made because the
// view's height changed (viewport) that grows it again counts, and after
// webGrowthLimit of those in a row the height stays where it is (frozen)
// until the document, the width or the zoom changes.
const (
	// webMaxHeight is the tallest a card's web view gets, in pixels.
	webMaxHeight = 4000.0
	// webGrowthLimit is how many growths in a row caused by the view's own
	// growth are accepted before the height is frozen.
	webGrowthLimit = 3
)

// webHeightGovernor is a card's web view height. The zero value is not
// ready; use newWebHeightGovernor.
type webHeightGovernor struct {
	maxHeight   float64
	growthLimit int
	// css is the document's height as last reported, in CSS pixels; hasCSS
	// is false before the first report.
	css    float64
	hasCSS bool
	// zoom is the page zoom the view shows the document at (1 = 100 %).
	zoom float64
	// applied is the height the view was given, in pixels; hasApplied is
	// false before the first.
	applied    float64
	hasApplied bool
	frozen     bool
	streak     int
}

// newWebHeightGovernor is a governor at zoom (0 or less is 100 %) with
// the standard cap and growth limit.
func newWebHeightGovernor(zoom float64) webHeightGovernor {
	return newWebHeightGovernorWith(zoom, webMaxHeight, webGrowthLimit)
}

// newWebHeightGovernorWith is newWebHeightGovernor with a cap and a growth
// limit of its own.
func newWebHeightGovernorWith(zoom, maxHeight float64, growthLimit int) webHeightGovernor {
	if !(zoom > 0) {
		zoom = 1
	}
	return webHeightGovernor{maxHeight: maxHeight, growthLimit: growthLimit, zoom: zoom}
}

// content is the document's height in pixels at the current zoom; false
// before the first report.
func (g *webHeightGovernor) content() (float64, bool) {
	if !g.hasCSS {
		return 0, false
	}
	return math.Ceil(g.css * g.zoom), true
}

// fits reports whether the document fits the view (nothing to scroll
// inside): the scroll wheel goes to the conversation.
func (g *webHeightGovernor) fits() bool {
	c, ok := g.content()
	if !ok || !g.hasApplied {
		return true
	}
	return c <= g.applied+1
}

// reset: a new document, measured from scratch. The view keeps the height
// it has until the first report.
func (g *webHeightGovernor) reset() {
	g.css, g.hasCSS = 0, false
	g.frozen = false
	g.streak = 0
}

// widthChanged: the view's width changed, the document reflows, and a
// frozen height is measured again.
func (g *webHeightGovernor) widthChanged() {
	g.frozen = false
	g.streak = 0
}

// setZoom changes the zoom and returns the height the view should get at
// once (the last report scaled); false when there is none yet or it does
// not change. A frozen height is measured again.
func (g *webHeightGovernor) setZoom(zoom float64) (float64, bool) {
	if !(zoom > 0) {
		zoom = 1
	}
	if zoom == g.zoom {
		return 0, false
	}
	g.zoom = zoom
	g.frozen = false
	g.streak = 0
	return g.apply()
}

// report takes the document's height of css pixels; viewport says the
// report followed a change of the view's height alone. It returns the
// height the view should get, false to keep the one it has.
func (g *webHeightGovernor) report(css float64, viewport bool) (float64, bool) {
	if math.IsNaN(css) || math.IsInf(css, 0) || css < 0 {
		return 0, false
	}
	g.css, g.hasCSS = css, true
	if g.frozen {
		return 0, false
	}
	target := math.Min(math.Max(math.Ceil(css*g.zoom), 1), g.maxHeight)
	if viewport && g.hasApplied && target > g.applied {
		g.streak++
		if g.streak > g.growthLimit {
			g.frozen = true
			return 0, false
		}
	} else {
		g.streak = 0
	}
	return g.apply()
}

// apply gives the view the last report at the current zoom.
func (g *webHeightGovernor) apply() (float64, bool) {
	c, ok := g.content()
	if !ok {
		return 0, false
	}
	target := math.Min(math.Max(c, 1), g.maxHeight)
	if g.hasApplied && target == g.applied {
		return 0, false
	}
	g.applied, g.hasApplied = target, true
	return target, true
}
