// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"time"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// The keyboard of the board's Columns and Today styles. Up and Down are
// the ListBoxes' own; the rest is here, in a file of its own so that the
// files carrying texts keep their line numbers (po/POTFILES). Reference:
// macOS BoardColumnTableView.keyDown and BoardTodayViewController.
//
// Selecting a case opens the sliding detail panel (renderPanel), so Return
// on a selected row only selects it again, which is what a click does.

// wireKeys connects Left and Right across the columns and Return in the
// columns and in Today, and the whole page's keys (wireBoardKeys); called
// from ensureBoard after bindColumns and wireToday.
func (p *boardPage) wireKeys() {
	p.wireBoardKeys()
	if c := p.columns; c != nil {
		keys := gtk.NewEventControllerKey()
		keys.SetPropagationPhase(gtk.PhaseCapture)
		keys.ConnectKeyPressed(func(val, _ uint, state gdk.ModifierType) bool {
			if state&gtk.AcceleratorGetDefaultModMask() != 0 {
				return false
			}
			switch val {
			case gdk.KEY_Left, gdk.KEY_KP_Left:
				return c.sideways(-1)
			case gdk.KEY_Right, gdk.KEY_KP_Right:
				return c.sideways(1)
			}
			return false
		})
		c.row.AddController(keys)
		for _, pane := range c.panes {
			pane.list.ConnectRowActivated(func(row *gtk.ListBoxRow) {
				if row == nil {
					return
				}
				c.choose(pane, row.Index())
			})
		}
	}
	if t := p.today; t != nil {
		t.list.ConnectRowActivated(func(row *gtk.ListBoxRow) {
			if row == nil {
				return
			}
			idx := row.Index()
			if idx < 0 || idx >= len(t.items) {
				return
			}
			if id, ok := t.items[idx].caseID(); ok {
				p.ctl.Select(id)
				return
			}
			if t.items[idx].kind == todayItemMore {
				p.ctl.ShowWaitingForYou()
			}
		})
	}
}

// selectableRows is the indexes into pane.items of the rows the user can
// select: cards and commitments, not the placeholder or the heading.
func (pane *boardColumnPane) selectableRows() []int {
	var rows []int
	for i, it := range pane.items {
		if _, ok := it.caseID(); ok {
			rows = append(rows, i)
		}
	}
	return rows
}

// focusedPane is the pane whose list holds the keyboard focus, else the
// one holding the selection.
func (c *boardColumns) focusedPane() int {
	for i, pane := range c.panes {
		if pane != nil && pane.list.FocusChild() != nil {
			return i
		}
	}
	for i, pane := range c.panes {
		if pane != nil && pane.list.SelectedRow() != nil {
			return i
		}
	}
	return -1
}

// sideways moves the selection to the neighbouring column (Left, Right)
// and puts the focus on its row; true when the key is consumed.
func (c *boardColumns) sideways(delta int) bool {
	from := c.focusedPane()
	if from < 0 {
		return false
	}
	counts := make([]int, len(c.panes))
	for i, pane := range c.panes {
		if pane != nil {
			counts[i] = len(pane.selectableRows())
		}
	}
	position := -1
	if row := c.panes[from].list.SelectedRow(); row != nil {
		for i, idx := range c.panes[from].selectableRows() {
			if idx == row.Index() {
				position = i
				break
			}
		}
	}
	col, rowPos, ok := board.SidewaysTarget(counts, from, position, delta)
	if !ok {
		return true
	}
	target := c.panes[col]
	idx := target.selectableRows()[rowPos]
	if row := target.list.RowAtIndex(idx); row != nil {
		target.list.SelectRow(row)
		row.GrabFocus()
	}
	return true
}

// wireBoardKeys is one capture-phase key controller on the whole board
// page (board_page_root), so it sees a key wherever the keyboard is on the
// page, before the focused widget does:
//
//   - Escape in two steps (board.EscapeFor): an open popup of the inline
//     reply (compose.Pane.PopupOpen: the recipients' suggestions, the
//     link popover, the style menus) handles it itself; the keyboard in the reply editor or its recipient fields goes
//     to the state pill; anywhere else an open panel closes (the sliding
//     panel of Columns and Today, the pushed detail of a narrow List).
//     Popovers and menus elsewhere on the page (Remind…, the state pill's
//     menu, a context menu, the account filter's dropdown, the main menu)
//     are surfaces of their own: a popover's parent is its anchor on this
//     page, so this capture controller would see their keys first, and
//     ownsKeys hands every key back while the keyboard is in one.
//   - E, D and R (board.KeyFor): Archive, Done or Move Back to Board, and
//     Remind…, on the selected case, never while the keyboard is in a text
//     field or the editor. Ctrl+1 and Ctrl+2 are the window's.
//
// Modifiers are read under gtk.AcceleratorGetDefaultModMask, so Caps Lock
// and Num Lock never stop a key.
func (p *boardPage) wireBoardKeys() {
	keys := gtk.NewEventControllerKey()
	keys.SetPropagationPhase(gtk.PhaseCapture)
	keys.ConnectKeyPressed(func(val, _ uint, state gdk.ModifierType) bool {
		if p.replyClosed || p.w.mode != board.ModeBoard || !p.ownsKeys() {
			return false
		}
		mods := state & gtk.AcceleratorGetDefaultModMask()
		if val == gdk.KEY_Escape {
			if mods != 0 {
				return false
			}
			return p.escape()
		}
		r := rune(gdk.KeyvalToUnicode(val))
		if r == 0 {
			return false
		}
		primary := mods&gdk.ControlMask != 0
		other := mods&^(gdk.ControlMask|gdk.ShiftMask) != 0
		action, ok := board.KeyFor(r, primary, other, p.keyboardInText(), p.w.mode, i18n.Tr)
		if !ok {
			return false
		}
		return p.boardKey(action)
	})
	p.root.AddController(keys)
}

// ownsKeys reports whether a key pressed now is the page's to act on:
// false while the keyboard focus sits in a popover (gtk_widget_get_ancestor
// with GtkPopover finds menus and every subclass, walking up through the
// popover to its anchor) or outside the page, so a popover's own Escape
// closes it and E, D and R typed in a menu stay the menu's.
func (p *boardPage) ownsKeys() bool {
	f := p.w.Focus()
	if f == nil {
		return true
	}
	w := gtk.BaseWidget(f)
	if w.Ancestor(gtk.GTypePopover) != nil {
		return false
	}
	return w.IsAncestor(p.root)
}

// escape is Escape on the board page; true when it is consumed.
func (p *boardPage) escape() bool {
	inPane := p.keyboardInReplyPane()
	popup := inPane && p.replyShown.PopupOpen()
	style := p.ctl.State().Style
	listPushed := style == board.StyleList && p.listSplit.Collapsed() && p.listSplit.ShowContent()
	panelOpen := listPushed || (style != board.StyleList && p.panel.split.ShowSidebar())
	switch board.EscapeFor(inPane, popup, panelOpen) {
	case board.EscapeFocusStatePill:
		if p.statePill != nil && p.statePill.IsVisible() {
			p.statePill.GrabFocus()
		}
		return true
	case board.EscapeClosePanel:
		if listPushed {
			p.listSplit.SetShowContent(false)
			focusSelectedRow(p.caseList)
			return true
		}
		p.ctl.Select("")
		return true
	}
	// EscapeClosePopup: the popup's own handler closes it;
	// EscapeNothing: the key goes on.
	return false
}

// keyboardInReplyPane reports the keyboard inside the inline reply shown
// (its editor or its recipient fields).
func (p *boardPage) keyboardInReplyPane() bool {
	return p.replyShown != nil && p.replyShown.KeyboardInside()
}

// keyboardInText reports the keyboard where a letter is typing: a text
// field or text view anywhere on the page, or anything in the reply slot
// (the inline editor, its fields, Suggest Reply's instruction).
func (p *boardPage) keyboardInText() bool {
	f := p.w.Focus()
	if f == nil {
		return false
	}
	if gtk.BaseWidget(f).IsAncestor(p.replySlot) {
		return true
	}
	switch gtk.BaseWidget(f).Cast().(type) {
	case *gtk.Text, *gtk.TextView, *gtk.Entry, *gtk.SearchEntry, *gtk.PasswordEntry, *gtk.SpinButton:
		return true
	}
	return false
}

// boardKey runs a board key on the selected case through the same
// win.board-* actions the header uses (their enabled state applies); true
// when consumed.
func (p *boardPage) boardKey(action board.KeyAction) bool {
	d := p.ctl.View().Detail
	if d == nil {
		return false
	}
	activate := func(name string) bool {
		a := p.w.actions[name]
		if a == nil || !a.Enabled() {
			return false
		}
		a.Activate(nil)
		return true
	}
	switch action {
	case board.KeyArchive:
		return activate("board-archive")
	case board.KeyDone:
		if d.IsDone {
			return activate("board-reopen")
		}
		return activate("board-mark-done")
	case board.KeyRemind:
		if !p.remindButton.Sensitive() {
			return false
		}
		if p.ctl.State().Style == board.StyleList && p.listSplit.Collapsed() {
			// A narrow List: the detail (and its Remind…) is pushed.
			p.listSplit.SetShowContent(true)
		}
		p.popupRemind()
		return true
	}
	return false
}

// popupRemind opens Remind…'s popover. On a narrow List the detail was
// pushed just now and the button is not mapped yet, so a popover popped up
// at once would have no place to point at: it opens from the button's own
// "map" instead, once, on the next idle so that the button has its size.
func (p *boardPage) popupRemind() {
	b := p.remindButton
	if b.Mapped() {
		b.Popup()
		return
	}
	// A map that comes much later (the push never happened, the user came
	// back to the detail another way) is not this R's.
	asked := time.Now()
	var handler glib.SignalHandle
	handler = b.ConnectMap(func() {
		b.HandlerDisconnect(handler)
		glib.IdleAdd(func() {
			if b.Mapped() && p.w.mode == board.ModeBoard && time.Since(asked) < time.Second {
				b.Popup()
			}
		})
	})
}
