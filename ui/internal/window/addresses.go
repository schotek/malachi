// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"fmt"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/compose"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The sender and the recipients above a message, as chips: the display
// name alone, the whole address in the tooltip and in the menu a click
// opens (copy it, write to it). A long To or Cc row shows its first few
// chips and a "+N more" chip that unfolds every row of the message.

// addressChipsFolded is how many chips a row shows while folded. A fold
// that would hide a single address shows it instead: "+1 more" takes the
// room of the chip it hides.
const addressChipsFolded = 5

// addressNameChars caps the name on a chip; the tooltip has all of it.
// The rest of the chip's look is CSS (.address-chip in ui/internal/style).
const addressNameChars = 28

// addressRow is one line of the header grid: its label and chip box, the
// chips in the box now, and what they were built from, so a render that
// changes nothing leaves the row (and an open menu in it) alone.
type addressRow struct {
	label *gtk.Label
	box   *adw.WrapBox
	chips []gtk.Widgetter
	key   string
}

// addressHeader is the From, To and Cc rows of one message view, with
// what they show: the account and lists of the last render, and whether
// the user unfolded them for the message on display.
type addressHeader struct {
	v            *messageView
	from, to, cc *addressRow
	message      api.MessageID
	acc          api.AccountID
	lists        [3][]api.Address // from, to, cc
	expanded     bool
}

// newAddressHeader binds the rows from a builder; the IDs are the same in
// the three message blueprints.
func newAddressHeader(v *messageView, b *gtk.Builder) *addressHeader {
	row := func(id string) *addressRow {
		return &addressRow{
			label: b.GetObject(id + "_label").Cast().(*gtk.Label),
			box:   b.GetObject(id).Cast().(*adw.WrapBox),
		}
	}
	return &addressHeader{v: v, from: row("message_from"), to: row("message_to"), cc: row("message_cc")}
}

// show renders the three rows for message id. Another message starts
// folded again.
func (h *addressHeader) show(id api.MessageID, acc api.AccountID, from, to, cc []api.Address) {
	if id != h.message {
		h.message = id
		h.expanded = false
	}
	h.acc = acc
	h.lists = [3][]api.Address{from, to, cc}
	h.render()
}

// render rebuilds the rows whose content changed.
func (h *addressHeader) render() {
	for i, r := range []*addressRow{h.from, h.to, h.cc} {
		h.fill(r, h.lists[i])
	}
}

// fill rebuilds one row, or hides it when the list has nobody to show.
func (h *addressHeader) fill(r *addressRow, list []api.Address) {
	shown, more := foldAddresses(list, h.expanded)
	key := addressKey(h.acc, shown, more)
	if key == r.key {
		return
	}
	r.key = key
	// A chip about to go may hold the focus, and GTK would hand it on to
	// the next widget in the chain: the selectable body label, which then
	// selects all of its text (see newMessageView).
	if r.box.FocusChild() != nil {
		h.v.stack.GrabFocus()
	}
	for _, c := range r.chips {
		r.box.Remove(c)
	}
	r.chips = nil
	for _, a := range shown {
		r.add(h.chip(a))
	}
	if more > 0 {
		r.add(h.moreChip(r, more, len(shown)))
	}
	visible := len(r.chips) > 0
	r.label.SetVisible(visible)
	r.box.SetVisible(visible)
}

// add appends a chip to the row and remembers it for removal.
func (r *addressRow) add(c gtk.Widgetter) {
	r.box.Append(c)
	r.chips = append(r.chips, c)
}

// chip is one address: a menu button with the name, whose menu names the
// address again in full and offers to copy it or to write to it. The
// actions live on the chip and close over its address; the menu is built
// the first time it opens, so a message to hundreds of people costs a
// button each.
func (h *addressHeader) chip(a api.Address) gtk.Widgetter {
	name := widget.DisplayName(a)
	addr := strings.TrimSpace(a.Address)
	acc := h.acc
	label := gtk.NewLabel(name)
	label.SetUseMarkup(false)
	label.SetEllipsize(pango.EllipsizeEnd)
	label.SetMaxWidthChars(addressNameChars)
	chip := gtk.NewMenuButton()
	chip.SetChild(label)
	chip.AddCSSClass("address-chip")
	if full := widget.FormatAddress(a); full != name {
		chip.SetTooltipText(full)
	}

	g := gio.NewSimpleActionGroup()
	copyAction := gio.NewSimpleAction("copy", nil)
	copyAction.SetEnabled(addr != "")
	copyAction.ConnectActivate(func(*glib.Variant) {
		gdk.DisplayGetDefault().Clipboard().SetText(addr)
		h.v.say(i18n.T("Address copied"))
	})
	writeAction := gio.NewSimpleAction("write", nil)
	writeAction.SetEnabled(addr != "")
	writeAction.ConnectActivate(func(*glib.Variant) {
		h.v.win.compose.Open(compose.Params{Kind: compose.KindNew, AccountID: acc, To: []api.Address{a}})
	})
	g.AddAction(copyAction)
	g.AddAction(writeAction)
	chip.InsertActionGroup("addr", g)

	chip.SetCreatePopupFunc(func(mb *gtk.MenuButton) {
		if mb.Popover() == nil {
			mb.SetPopover(addressMenu(a))
		}
	})
	return chip
}

// addressMenu is a chip's menu: the name and the address on top, as
// text, then the actions (resolved on the chip).
func addressMenu(a api.Address) *gtk.PopoverMenu {
	header := gio.NewMenuItem("", "")
	header.SetAttributeValue("custom", glib.NewVariantString("address"))
	top := gio.NewMenu()
	top.AppendItem(header)
	actions := gio.NewMenu()
	actions.Append(i18n.T("_Copy Address"), "addr.copy")
	actions.Append(i18n.T("_New Message"), "addr.write")
	m := gio.NewMenu()
	m.AppendSection("", top)
	m.AppendSection("", actions)

	card := gtk.NewBox(gtk.OrientationVertical, 2)
	card.AddCSSClass("address-card")
	name := strings.TrimSpace(a.Name)
	addr := strings.TrimSpace(a.Address)
	if name != "" {
		l := gtk.NewLabel(name)
		l.SetUseMarkup(false)
		l.SetXAlign(0)
		l.SetWrap(true)
		l.SetMaxWidthChars(40)
		l.AddCSSClass("heading")
		card.Append(l)
	}
	if addr != "" {
		l := gtk.NewLabel(addr)
		l.SetUseMarkup(false)
		l.SetXAlign(0)
		l.SetWrap(true)
		l.SetWrapMode(pango.WrapWordChar)
		l.SetMaxWidthChars(40)
		if name != "" {
			l.AddCSSClass("dim-label")
		}
		card.Append(l)
	}
	p := gtk.NewPopoverMenuFromModel(m)
	p.AddChild(card, "address")
	return p
}

// moreChip is "+N more" at the end of folded row r, standing in for n
// addresses from chip number at on; it unfolds every row of the message.
// A click leaves the focus alone (a mouse user wants nothing focused);
// from the keyboard the focus moves on to the first chip the unfold
// revealed.
func (h *addressHeader) moreChip(r *addressRow, n, at int) gtk.Widgetter {
	// TRANSLATORS: the chip after the first few recipients of a message;
	// %d is how many more there are. Clicking it lists them all.
	b := gtk.NewButtonWithLabel(fmt.Sprintf(i18n.N("+%d more", "+%d more", n), n))
	b.AddCSSClass("flat")
	b.AddCSSClass("address-more")
	b.SetFocusOnClick(false)
	b.ConnectClicked(func() {
		keyboard := b.HasFocus()
		h.expanded = true
		h.render()
		if keyboard && at < len(r.chips) {
			gtk.BaseWidget(r.chips[at]).GrabFocus()
		}
	})
	return b
}

// foldAddresses is what a row shows: the addresses with something to
// display, all of them when expanded or when at most one would fold away,
// otherwise the first addressChipsFolded and how many more there are.
func foldAddresses(list []api.Address, expanded bool) (shown []api.Address, more int) {
	for _, a := range list {
		if widget.DisplayName(a) != "" {
			shown = append(shown, a)
		}
	}
	if expanded || len(shown) <= addressChipsFolded+1 {
		return shown, 0
	}
	return shown[:addressChipsFolded], len(shown) - addressChipsFolded
}

// addressKey identifies what a row shows: the account the chips write
// from, each name and address, and the fold.
func addressKey(acc api.AccountID, shown []api.Address, more int) string {
	var b strings.Builder
	b.WriteString(string(acc))
	for _, a := range shown {
		b.WriteString("\x00")
		b.WriteString(a.Name)
		b.WriteString("\x01")
		b.WriteString(a.Address)
	}
	fmt.Fprintf(&b, "\x00%d", more)
	return b.String()
}
