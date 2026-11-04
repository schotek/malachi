// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/toolbar"
)

// The customizable header bars (decisions T1–T15,
// docs/plans/toolbar-customization.md and -gtk.md §4): every item's
// widget stays in its Blueprint with its id, action and bindings; a
// toolbarSet only takes the items out of their header bars and packs them
// again in the order of the user's arrangement (toolbar.PresentFor with
// toolbar.GTK), the gaps (toolbar.Space) as margins. An item the
// arrangement leaves out has no parent but lives on (the set holds it),
// so the code that shows, hides, enables or relabels it keeps working on
// it unchanged; the conditional items (✦, Triage, eWay-CRM) are packed
// always and their own code shows them in place (T7). The widgets the
// catalog does not have (the main menus, the assistant panel's toggle,
// the titles, the board detail's menu) never move: packed first by the
// Blueprint they stay at the very end of their bar, as PackEnd puts a
// later child left of the earlier ones.

// tbItem is one toolbar item's widgets in screen order.
type tbItem struct {
	id      toolbar.ID
	widgets []gtk.Widgetter
	// baseStart and baseEnd are the outer margins the Blueprint gave the
	// first and the last widget (a Space adds toolbarGap to baseEnd).
	baseStart, baseEnd int
	// width is the item's last measured natural width with the header
	// bar's spacing, kept for the overflow while it is not packed.
	width int
}

// tbHeader is one header bar and the sections of each bar it draws.
type tbHeader struct {
	header   *adw.HeaderBar
	sections map[toolbar.Bar][]toolbar.Section
	// leadInset, when not 0, is the start margin of the first item at the
	// header's start (the board detail aligns it with its 24 px inset).
	leadInset int
	// column is the width the header bar would get, for the overflow
	// (toolbar_fit.go); nil: no overflow.
	column func() int

	// shownBar is the bar the header shows now, entries what it packs of
	// that bar's arrangement and overflow the items toolbar.Fit left out.
	shownBar toolbar.Bar
	entries  []toolbar.Shown
	pack     headerPack
	overflow map[toolbar.ID]bool
	// lastAvailable and lastMin are what the overflow last saw.
	lastAvailable, lastMin int
	fitting                bool
}

// toolbarSet are the items and header bars of one part of the window (the
// Mail mode, the board page, the Calendar's page).
type toolbarSet struct {
	items   map[toolbar.ID]*tbItem
	headers []*tbHeader
	// selfSpaced are the items whose widgets carry their gaps themselves.
	selfSpaced map[toolbar.ID]bool
	// onOverflow runs after the overflow of a header changed.
	onOverflow func()
}

func newToolbarSet() *toolbarSet {
	return &toolbarSet{items: map[toolbar.ID]*tbItem{}, selfSpaced: map[toolbar.ID]bool{}}
}

// add registers id's widgets (in screen order) and their Blueprint margins.
func (s *toolbarSet) add(id toolbar.ID, widgets ...gtk.Widgetter) *tbItem {
	it := &tbItem{id: id, widgets: widgets}
	if len(widgets) > 0 {
		it.baseStart = gtk.BaseWidget(widgets[0]).MarginStart()
		it.baseEnd = gtk.BaseWidget(widgets[len(widgets)-1]).MarginEnd()
	}
	s.items[id] = it
	return it
}

// addHeader registers a header bar that draws the sections of bar
// (several bars for the board's shared header).
func (s *toolbarSet) addHeader(h *adw.HeaderBar) *tbHeader {
	th := &tbHeader{header: h, sections: map[toolbar.Bar][]toolbar.Section{}, overflow: map[toolbar.ID]bool{}}
	s.headers = append(s.headers, th)
	return th
}

// on says the header draws secs of bar.
func (h *tbHeader) on(bar toolbar.Bar, secs ...toolbar.Section) *tbHeader {
	h.sections[bar] = secs
	return h
}

// apply packs bar's arrangement l into every header bar that draws one of
// its sections; only is, when not nil, the headers to do it in (the
// board's detail header shows the List's detail in every style).
func (s *toolbarSet) apply(bar toolbar.Bar, l toolbar.Layout, f toolbar.Features, only ...*tbHeader) {
	shown := toolbar.PresentFor(gtkVariant, bar, l, f)
	var targets []*tbHeader
	for _, h := range s.headers {
		if _, ok := h.sections[bar]; !ok {
			continue
		}
		if len(only) > 0 && !containsHeader(only, h) {
			continue
		}
		targets = append(targets, h)
	}
	for _, h := range targets {
		h.shownBar = bar
		h.entries = sectionEntries(bar, shown, h.sections[bar])
		h.pack = packHeader(h.entries, func(id toolbar.ID) bool { return s.selfSpaced[id] })
		h.overflow = map[toolbar.ID]bool{}
		h.lastAvailable, h.lastMin = -1, -1
	}
	// Take every item of the targets out first: an item may move from one
	// header bar to another (Contacts from the folders' to the list's).
	for _, h := range targets {
		s.unpackFrom(h)
	}
	for _, h := range targets {
		s.packInto(h)
	}
}

func containsHeader(hs []*tbHeader, h *tbHeader) bool {
	for _, x := range hs {
		if x == h {
			return true
		}
	}
	return false
}

// unpackFrom takes every item widget out of h's header bar (only those
// packed there) and gives it its Blueprint margins back.
func (s *toolbarSet) unpackFrom(h *tbHeader) {
	for _, it := range s.items {
		for _, w := range it.widgets {
			base := gtk.BaseWidget(w)
			if base.Parent() != nil && base.IsAncestor(h.header) {
				h.header.Remove(w)
			}
		}
		if len(it.widgets) > 0 && !s.packed(it.id) {
			gtk.BaseWidget(it.widgets[0]).SetMarginStart(it.baseStart)
			gtk.BaseWidget(it.widgets[len(it.widgets)-1]).SetMarginEnd(it.baseEnd)
		}
	}
}

// unparent removes w from the header bar it is packed in, h's or another
// one of the set.
func (s *toolbarSet) unparent(w gtk.Widgetter, prefer *tbHeader) {
	base := gtk.BaseWidget(w)
	if base.Parent() == nil {
		return
	}
	for _, h := range append([]*tbHeader{prefer}, s.headers...) {
		if h != nil && base.IsAncestor(h.header) {
			h.header.Remove(w)
			return
		}
	}
}

// packInto packs h's plan without the items in its overflow.
func (s *toolbarSet) packInto(h *tbHeader) {
	first := true
	for _, id := range h.pack.Start {
		it := s.items[id]
		if it == nil || h.overflow[id] {
			continue
		}
		s.margins(it, h, first, h.pack.Gap[id])
		first = false
		for _, w := range it.widgets {
			s.unparent(w, h)
			h.header.PackStart(w)
		}
	}
	// PackEnd puts each child left of the ones packed before: right to left.
	for i := len(h.pack.End) - 1; i >= 0; i-- {
		id := h.pack.End[i]
		it := s.items[id]
		if it == nil || h.overflow[id] {
			continue
		}
		s.margins(it, h, false, h.pack.Gap[id])
		for j := len(it.widgets) - 1; j >= 0; j-- {
			s.unparent(it.widgets[j], h)
			h.header.PackEnd(it.widgets[j])
		}
	}
}

// margins gives it its outer margins in h: the Blueprint's, the lead inset
// for the first item at the start, toolbarGap more at the end after a gap.
func (s *toolbarSet) margins(it *tbItem, h *tbHeader, lead, gap bool) {
	if len(it.widgets) == 0 {
		return
	}
	start := it.baseStart
	if lead && h.leadInset > 0 {
		start = h.leadInset
	}
	gtk.BaseWidget(it.widgets[0]).SetMarginStart(start)
	end := it.baseEnd
	if gap {
		end += toolbarGap
	}
	gtk.BaseWidget(it.widgets[len(it.widgets)-1]).SetMarginEnd(end)
}

// packed reports whether id is packed in one of the set's header bars.
func (s *toolbarSet) packed(id toolbar.ID) bool {
	it := s.items[id]
	if it == nil || len(it.widgets) == 0 {
		return false
	}
	return gtk.BaseWidget(it.widgets[0]).Parent() != nil
}

// overflowed are the items the overflow of every header bar left out, in
// the order of their arrangements.
func (s *toolbarSet) overflowed() []toolbar.ID {
	var out []toolbar.ID
	for _, h := range s.headers {
		for _, e := range h.entries {
			if h.overflow[e.ID] {
				out = append(out, e.ID)
			}
		}
	}
	return out
}

// headerFor is the header bar of the set that h's widget is, or nil.
func (s *toolbarSet) headerFor(hb *adw.HeaderBar) *tbHeader {
	for _, h := range s.headers {
		if h.header == hb {
			return h
		}
	}
	return nil
}

// tbFace is the display mode of an icon button of a toolbar (T9, O4):
// its icon, its label in the editor, and the button.
type tbFace struct {
	button gtk.Widgetter
	icon   string
	label  string
	mode   toolbar.DisplayMode
}

// toolbarFaces are the faced buttons by their object, so the code that
// changes an icon (setStar, presentReply) changes the face instead.
// Main loop only.
var toolbarFaces = map[uintptr]*tbFace{}

func faceKey(w gtk.Widgetter) uintptr { return coreglib.InternObject(w).Native() }

// addFace makes b a faced button with label; its icon is the one it has.
func addFace(b gtk.Widgetter, label string) *tbFace {
	f := &tbFace{button: b, label: label, mode: toolbar.DisplayIcon}
	switch x := b.(type) {
	case *gtk.MenuButton:
		f.icon = x.IconName()
	case *gtk.ToggleButton:
		f.icon = x.IconName()
	case *gtk.Button:
		f.icon = x.IconName()
	}
	toolbarFaces[faceKey(b)] = f
	return f
}

// setFace changes the icon and the label of a faced button and reports
// whether b is one; label "" keeps it.
func setFace(b gtk.Widgetter, icon, label string) bool {
	f := toolbarFaces[faceKey(b)]
	if f == nil {
		return false
	}
	if icon != "" {
		f.icon = icon
	}
	if label != "" {
		f.label = label
	}
	f.apply(f.mode)
	return true
}

// apply shows the face in mode m.
func (f *tbFace) apply(m toolbar.DisplayMode) {
	f.mode = m
	var content *adw.ButtonContent
	if m == toolbar.DisplayIconAndLabel {
		content = adw.NewButtonContent()
		content.SetIconName(f.icon)
		content.SetLabel(f.label)
		content.SetUseUnderline(false)
	}
	switch b := f.button.(type) {
	case *gtk.MenuButton:
		switch m {
		case toolbar.DisplayIconAndLabel:
			b.SetChild(content)
		case toolbar.DisplayLabel:
			b.SetLabel(f.label)
		default:
			b.SetIconName(f.icon)
		}
	case *gtk.ToggleButton:
		faceButton(&b.Button, m, f, content)
	case *gtk.Button:
		faceButton(b, m, f, content)
	}
}

func faceButton(b *gtk.Button, m toolbar.DisplayMode, f *tbFace, content *adw.ButtonContent) {
	switch m {
	case toolbar.DisplayIconAndLabel:
		b.SetChild(content)
	case toolbar.DisplayLabel:
		b.SetUseUnderline(false)
		b.SetLabel(f.label)
	default:
		b.SetIconName(f.icon)
	}
}

// applyFaces shows every face in mode m.
func applyFaces(faces []*tbFace, m toolbar.DisplayMode) {
	for _, f := range faces {
		f.apply(m)
	}
}

// interactiveCSS are the CSS names of the widgets a right click on a
// header bar leaves to themselves: a click there is not on an empty spot
// (T15).
var interactiveCSS = map[string]bool{
	"button": true, "menubutton": true, "dropdown": true, "toggle-group": true,
	"toggle": true, "windowcontrols": true, "entry": true, "switch": true, "spinbutton": true,
}

// onEmptySpot reports whether a widget picked in a header bar is none of
// its controls nor inside one.
func onEmptySpot(picked gtk.Widgetter, header gtk.Widgetter) bool {
	if picked == nil {
		return true
	}
	hb := faceKey(header)
	for w := gtk.BaseWidget(picked); w != nil; {
		if faceKey(w) == hb {
			return true
		}
		if interactiveCSS[w.CSSName()] {
			return false
		}
		p := w.Parent()
		if p == nil {
			return true
		}
		w = gtk.BaseWidget(p)
	}
	return true
}

// wireCustomizeMenu gives a header bar the right-click menu of its empty
// spots (T15): one item, Customize Toolbar…, that runs open with the bar
// the header bar shows (bar()).
func wireCustomizeMenu(hb *adw.HeaderBar, bar func() toolbar.Bar, open func(toolbar.Bar)) {
	click := gtk.NewGestureClick()
	click.SetButton(3) // the secondary button
	// Capture: the window handle inside the header bar would show the
	// window manager's menu first.
	click.SetPropagationPhase(gtk.PhaseCapture)
	click.ConnectPressed(func(_ int, x, y float64) {
		if !onEmptySpot(hb.Pick(x, y, gtk.PickDefault), hb) {
			return
		}
		click.SetState(gtk.EventSequenceClaimed)
		b := bar()
		group := gio.NewSimpleActionGroup()
		a := gio.NewSimpleAction("customize", nil)
		a.ConnectActivate(func(*glib.Variant) { open(b) })
		group.AddAction(a)
		m := gio.NewMenu()
		m.Append(toolbar.CustomizeTitle(i18n.Tr), "toolbar.customize")
		popupContextMenu(&hb.Widget, &hb.Widget, m, "toolbar", group, x, y)
	})
	hb.AddController(click)
}

// idle runs fn on the main loop's next idle round.
func idle(fn func()) { glib.IdleAdd(fn) }
