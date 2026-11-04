// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package compose

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/recipients"
)

// A recipient field (To, Cc, Bcc): every finished address is a badge with
// an ×, what is still being typed stays in the entry after the badges. All
// rules for when text becomes a badge live in ui/internal/recipients; this
// file only shows the model's tokens and feeds it keystrokes, clicks and
// pastes. Addresses are hostile input: labels never interpret markup and
// their text is capped before a widget gets it.

const (
	// recipientLabelChars is the width a badge's label may take, in
	// characters; longer ones ellipsize in the middle.
	recipientLabelChars = 28
	// recipientLabelMax and recipientTooltipMax cap the text handed to a
	// widget: the model keeps what was typed, the widget never lays out
	// more than this.
	recipientLabelMax   = 200
	recipientTooltipMax = 1000
	// recipientLines is how many lines of badges a field shows before it
	// scrolls inside itself, and recipientLineHeight the height of one.
	recipientLines      = 4
	recipientLineHeight = 30
	recipientLineGap    = 2
)

// recipientField is the widget and the model of one recipient row.
type recipientField struct {
	scroll *gtk.ScrolledWindow
	row    *adw.WrapBox
	entry  *gtk.Entry
	tokens *recipients.Tokens
	badges []*gtk.Box
	sel    int // the selected badge, -1 for none

	syncing bool // the entry is being set from the model: its "changed" is not an edit
	closed  bool

	// removeText is the tooltip of a badge's × button.
	removeText string
	// skipCommit tells that a focus loss must not commit the pending text:
	// the window went to the background, or a suggestion is being clicked.
	skipCommit func() bool
	// changed reports that Text() differs from before an operation (a
	// dirty draft), typed that the user edited the entry (completion).
	changed func()
	typed   func()
}

// newRecipientField fills the scrolled wrap box of one row from the
// builder: the badges go before the entry, which is made here.
func newRecipientField(scroll *gtk.ScrolledWindow, row *adw.WrapBox, label *gtk.Label, removeText string) *recipientField {
	f := &recipientField{
		scroll:     scroll,
		row:        row,
		tokens:     recipients.New(),
		sel:        -1,
		removeText: removeText,
	}
	scroll.SetPropagateNaturalHeight(true)
	scroll.SetMaxContentHeight(recipientLines*recipientLineHeight + (recipientLines-1)*recipientLineGap)
	// A scrollbar that may show makes the scrolled window at least as tall
	// as the scrollbar's own minimum, which is more than one line of badges.
	// So the scrollbar exists only while the badges overflow; until then
	// the field is just as tall as its content.
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyExternal)
	adj := scroll.VAdjustment()
	adj.ConnectChanged(func() {
		policy := gtk.PolicyExternal
		if adj.Upper() > adj.PageSize()+0.5 {
			policy = gtk.PolicyAutomatic
		}
		if _, v := scroll.Policy(); v != policy {
			scroll.SetPolicy(gtk.PolicyNever, policy)
		}
	})

	f.entry = gtk.NewEntry()
	f.entry.SetHasFrame(false)
	f.entry.SetWidthChars(20)
	f.entry.SetVAlign(gtk.AlignCenter)
	row.Append(f.entry)
	// The row label names the entry (the accessible labelled-by relation).
	label.SetMnemonicWidget(f.entry)

	f.entry.ConnectChanged(f.onEntryChanged)
	f.entry.ConnectActivate(func() {
		f.deselect()
		f.commit()
	})

	focus := gtk.NewEventControllerFocus()
	focus.ConnectLeave(func() {
		f.deselect()
		if f.closed || (f.skipCommit != nil && f.skipCommit()) {
			return
		}
		f.commit()
	})
	f.entry.AddController(focus)

	// A click on the empty part of the row goes to the entry. The target
	// phase fires only when the row itself was hit, never a badge.
	click := gtk.NewGestureClick()
	click.SetPropagationPhase(gtk.PhaseTarget)
	click.ConnectPressed(func(int, float64, float64) {
		f.deselect()
		f.focusEntry()
	})
	row.AddController(click)
	return f
}

// wireKeys adds the key handling. It runs after the completion's own
// controller was added, so that Return and Tab pick a suggestion first.
func (f *recipientField) wireKeys() {
	keys := gtk.NewEventControllerKey()
	keys.SetPropagationPhase(gtk.PhaseCapture)
	keys.ConnectKeyPressed(f.onKey)
	f.entry.AddController(keys)
}

// Text is the field value in the form the rest of the window used when the
// row was a plain entry. Parsing for sending goes through resolved.
func (f *recipientField) Text() string { return f.tokens.Text() }

// SetText replaces the whole value (prefill, draft load); it is not an
// edit and reports nothing.
func (f *recipientField) SetText(s string) {
	f.tokens = recipients.NewTokens(s)
	f.sel = -1
	f.rebuild()
	f.syncEntry(-1)
}

// SetAddresses is SetText for a list that is already parsed: nothing is
// read back from text, so no name can change meaning on the way.
func (f *recipientField) SetAddresses(list []api.Address) {
	f.tokens = recipients.New()
	for _, a := range list {
		if strings.TrimSpace(a.Address) != "" {
			f.tokens.Add(a)
		}
	}
	f.sel = -1
	f.rebuild()
	f.syncEntry(-1)
}

// resolved is the recipients as if the pending text were committed, and
// the entries that are not addresses.
func (f *recipientField) resolved() ([]api.Address, []string) { return f.tokens.Resolved() }

// commitPending makes the pending text a badge (before sending, so that an
// invalid entry is shown red).
func (f *recipientField) commitPending() { f.commit() }

// close stops the field reacting; the window is going away.
func (f *recipientField) close() {
	f.closed = true
	f.changed, f.typed, f.skipCommit = nil, nil, nil
}

// add appends a picked suggestion as a badge.
func (f *recipientField) add(a api.Address) {
	if f.closed {
		return
	}
	before := f.tokens.Text()
	f.deselect()
	f.tokens.Add(a)
	f.rebuild()
	f.syncEntry(-1)
	f.notify(before)
}

// notify reports a change of the value to the window.
func (f *recipientField) notify(before string) {
	if f.changed != nil && f.tokens.Text() != before {
		f.changed()
	}
}

// commit is Enter, Tab and focus loss.
func (f *recipientField) commit() {
	before := f.tokens.Text()
	if f.tokens.Commit() {
		f.rebuild()
	}
	if f.entry.Text() != f.tokens.Pending() {
		f.syncEntry(-1)
	}
	f.notify(before)
}

// onEntryChanged takes every edit of the entry to the model.
func (f *recipientField) onEntryChanged() {
	if f.syncing || f.closed {
		return
	}
	f.deselect()
	before := f.tokens.Text()
	text := f.entry.Text()
	atEnd := f.entry.Position() >= utf8.RuneCountInString(text)
	if f.tokens.SetPending(text) {
		f.rebuild()
		// A separator typed in the middle commits the left part; the
		// caret then goes to the start of what is left.
		pos := 0
		if atEnd {
			pos = -1
		}
		f.syncEntry(pos)
	}
	f.notify(before)
	if f.typed != nil {
		f.typed()
	}
}

// syncEntry sets the entry to the model's pending text and puts the caret
// at pos (-1 is the end). The undo history goes: it would bring back text
// that is now a badge.
func (f *recipientField) syncEntry(pos int) {
	f.syncing = true
	f.entry.SetEnableUndo(false)
	f.entry.SetText(f.tokens.Pending())
	f.entry.SetEnableUndo(true)
	f.entry.SetPosition(pos)
	f.syncing = false
}

// focusEntry puts the focus in the entry without selecting its text.
func (f *recipientField) focusEntry() {
	f.entry.GrabFocus()
	f.entry.SetPosition(-1)
}

// rebuild shows the model's tokens as badges before the entry.
func (f *recipientField) rebuild() {
	for _, b := range f.badges {
		f.row.Remove(b)
	}
	f.badges = f.badges[:0]
	var prev gtk.Widgetter
	for i, tok := range f.tokens.Items() {
		b := f.newBadge(i, tok)
		f.row.InsertChildAfter(b, prev)
		prev = b
		f.badges = append(f.badges, b)
	}
	if f.sel >= len(f.badges) {
		f.sel = -1
	}
	if f.sel >= 0 {
		f.badges[f.sel].AddCSSClass("selected")
	}
	// Keep the entry in sight when the field scrolls.
	glib.IdleAdd(func() {
		if f.closed {
			return
		}
		adj := f.scroll.VAdjustment()
		adj.SetValue(adj.Upper() - adj.PageSize())
	})
}

// newBadge is the capsule of token i: its label, and a × that removes it.
func (f *recipientField) newBadge(i int, tok recipients.Token) *gtk.Box {
	b := gtk.NewBox(gtk.OrientationHorizontal, 0)
	b.AddCSSClass("recipient-token")
	// The entry sets the height of a line; the capsule keeps its own and
	// stays clear of the capsules on the lines above and below.
	b.SetVAlign(gtk.AlignCenter)
	if !tok.Valid() {
		b.AddCSSClass("invalid")
	}
	b.SetTooltipText(clipText(tok.Tooltip(), recipientTooltipMax))

	label := gtk.NewLabel(clipText(tok.Label(), recipientLabelMax))
	label.SetUseMarkup(false)
	label.SetSelectable(false)
	label.SetEllipsize(pango.EllipsizeMiddle)
	label.SetMaxWidthChars(recipientLabelChars)
	b.Append(label)

	remove := gtk.NewButtonFromIconName("window-close-symbolic")
	remove.AddCSSClass("flat")
	remove.AddCSSClass("circular")
	remove.SetFocusOnClick(false)
	remove.SetFocusable(false)
	remove.SetVAlign(gtk.AlignCenter)
	remove.SetTooltipText(f.removeText)
	remove.ConnectClicked(func() {
		// Not inside the button's own signal: the button goes with it.
		glib.IdleAdd(func() { f.remove(i) })
	})
	b.Append(remove)

	click := gtk.NewGestureClick()
	click.SetButton(gdk.BUTTON_PRIMARY)
	click.ConnectPressed(func(n int, _, _ float64) {
		if n >= 2 {
			glib.IdleAdd(func() { f.edit(i) })
			return
		}
		f.focusEntry()
		f.selectBadge(i)
	})
	b.AddController(click)
	return b
}

// selectBadge marks badge i as the selected one.
func (f *recipientField) selectBadge(i int) {
	f.deselect()
	if i < 0 || i >= len(f.badges) {
		return
	}
	f.sel = i
	f.badges[i].AddCSSClass("selected")
}

// deselect clears the badge selection.
func (f *recipientField) deselect() {
	if f.sel >= 0 && f.sel < len(f.badges) {
		f.badges[f.sel].RemoveCSSClass("selected")
	}
	f.sel = -1
}

// remove drops badge i.
func (f *recipientField) remove(i int) {
	if f.closed || i < 0 || i >= len(f.tokens.Items()) {
		return
	}
	before := f.tokens.Text()
	f.deselect()
	f.tokens.Remove(i)
	f.rebuild()
	f.notify(before)
}

// edit turns badge i back into text in the entry.
func (f *recipientField) edit(i int) {
	if f.closed || i < 0 || i >= len(f.tokens.Items()) {
		return
	}
	before := f.tokens.Text()
	f.deselect()
	f.tokens.Edit(i)
	f.rebuild()
	f.syncEntry(-1)
	f.focusEntry()
	f.notify(before)
}

// onKey is the keyboard of the entry: the badges are walked, removed and
// copied from it. It reports whether the key was used.
func (f *recipientField) onKey(keyval, _ uint, state gdk.ModifierType) bool {
	if isModifierKey(keyval) {
		return false
	}
	ctrl := state&gdk.ControlMask != 0
	if f.sel >= 0 {
		return f.onBadgeKey(keyval, ctrl)
	}
	switch {
	case keyval == gdk.KEY_Tab || keyval == gdk.KEY_ISO_Left_Tab:
		f.commit() // the focus moves on in any case
		return false
	case keyval == gdk.KEY_BackSpace && !ctrl && f.noText():
		f.selectLast()
		return f.sel >= 0
	case keyval == gdk.KEY_Left && !ctrl && f.entry.Position() == 0 && f.noSelection():
		f.selectLast()
		return f.sel >= 0
	case ctrl && (keyval == gdk.KEY_v || keyval == gdk.KEY_V), keyval == gdk.KEY_Insert && state&gdk.ShiftMask != 0:
		f.pasteClipboard()
		return true
	}
	return false
}

// onBadgeKey is onKey while a badge is selected.
func (f *recipientField) onBadgeKey(keyval uint, ctrl bool) bool {
	i := f.sel
	switch {
	case keyval == gdk.KEY_BackSpace || keyval == gdk.KEY_Delete || keyval == gdk.KEY_KP_Delete:
		f.remove(i)
		return true
	case keyval == gdk.KEY_Left:
		if i > 0 {
			f.selectBadge(i - 1)
		}
		return true
	case keyval == gdk.KEY_Right:
		if i < len(f.badges)-1 {
			f.selectBadge(i + 1)
		} else {
			f.deselect()
			f.entry.SetPosition(0)
		}
		return true
	case keyval == gdk.KEY_Escape:
		f.deselect()
		return true
	case ctrl && (keyval == gdk.KEY_c || keyval == gdk.KEY_C), ctrl && (keyval == gdk.KEY_x || keyval == gdk.KEY_X):
		if items := f.tokens.Items(); i < len(items) {
			gdk.DisplayGetDefault().Clipboard().SetText(items[i].Tooltip())
			if keyval == gdk.KEY_x || keyval == gdk.KEY_X {
				f.remove(i)
			}
		}
		return true
	case keyval == gdk.KEY_Tab || keyval == gdk.KEY_ISO_Left_Tab:
		f.deselect()
		f.commit()
		return false
	}
	// Anything else is typing (or Ctrl+A, a paste): the badge is let go and
	// the key goes on to the entry.
	f.deselect()
	return false
}

func (f *recipientField) noText() bool { return f.entry.Text() == "" }

func (f *recipientField) noSelection() bool {
	_, _, ok := f.entry.SelectionBounds()
	return !ok
}

// selectLast selects the last badge, if there is one.
func (f *recipientField) selectLast() {
	if n := len(f.badges); n > 0 {
		f.selectBadge(n - 1)
	}
}

// pasteClipboard reads the clipboard as plain text and pastes it.
func (f *recipientField) pasteClipboard() {
	cb := gdk.DisplayGetDefault().Clipboard()
	cb.ReadTextAsync(context.Background(), func(res gio.AsyncResulter) {
		text, err := cb.ReadTextFinish(res)
		if err != nil || f.closed {
			return
		}
		f.paste(text)
	})
}

// paste replaces the entry's selection, or inserts at the caret, and lets
// the model split the result into badges and pending text. What follows
// the pasted text stays pending after it, the caret between the two.
func (f *recipientField) paste(text string) {
	before := f.tokens.Text()
	f.deselect()
	full := []rune(f.entry.Text())
	start, end := f.entry.Position(), f.entry.Position()
	if s, e, ok := f.entry.SelectionBounds(); ok {
		start, end = s, e
	}
	start = clampInt(start, 0, len(full))
	end = clampInt(end, start, len(full))
	head, tail := string(full[:start]), string(full[end:])

	f.tokens.SetPending(head)
	f.tokens.Paste(text)
	caret := utf8.RuneCountInString(f.tokens.Pending())
	f.tokens.SetPending(f.tokens.Pending() + tail)
	f.rebuild()
	f.syncEntry(caret)
	f.notify(before)
	if f.typed != nil {
		f.typed()
	}
}

// isModifierKey is a key that only changes the state of the others.
func isModifierKey(k uint) bool {
	switch k {
	case gdk.KEY_Shift_L, gdk.KEY_Shift_R, gdk.KEY_Control_L, gdk.KEY_Control_R,
		gdk.KEY_Alt_L, gdk.KEY_Alt_R, gdk.KEY_Meta_L, gdk.KEY_Meta_R,
		gdk.KEY_Super_L, gdk.KEY_Super_R, gdk.KEY_Caps_Lock, gdk.KEY_ISO_Level3_Shift:
		return true
	}
	return false
}

// clipText is s limited to max characters, an ellipsis marking the cut.
func clipText(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:max])) + string(rune(0x2026))
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
