// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/calendar"
)

// The Calendar's keys (calendar.KeyFor through the controller's
// HandleKey): Ctrl+T today, Ctrl+← and Ctrl+→ the previous and next range,
// Ctrl+Shift+N New Event, Return the selected event's detail, Delete its
// removal, the arrows the selection, Escape the search. The mode keys and
// the views' Ctrl+Shift+1–4 go by the physical key in mode_keys.go
// (calendarKey). One controller on the window in the bubble phase, so the
// focused widget (a button's Return, an entry's arrows, a list's
// navigation, a popover's Escape) acts first and a key reaches the page
// wherever the keyboard is in it, or nowhere.

// wireCalendarKeys connects the Calendar's keys; once, from ensureCalendar.
func (w *Window) wireCalendarKeys() {
	keys := gtk.NewEventControllerKey()
	keys.ConnectKeyPressed(func(val, _ uint, state gdk.ModifierType) bool {
		p := w.cal.page
		if p == nil || w.mode != board.ModeCalendar || !p.ownsKeys() {
			return false
		}
		press, ok := calendarPress(val, state)
		if !ok {
			return false
		}
		return p.ctl.HandleKey(press, focusTakesTyping(w.Focus()), w.eventDetailShown())
	})
	w.AddController(keys)
}

// calendarPress is a key as calendar.KeyFor reads it on Linux: Ctrl is
// Primary, Shift stands for the keys' Alt and Control; a key with Alt or
// Super is never the calendar's.
func calendarPress(val uint, state gdk.ModifierType) (calendar.Press, bool) {
	mods := state & gtk.AcceleratorGetDefaultModMask()
	if mods&^(gdk.ControlMask|gdk.ShiftMask) != 0 {
		return calendar.Press{}, false
	}
	p := calendar.Press{Primary: mods&gdk.ControlMask != 0, Shift: mods&gdk.ShiftMask != 0, ShiftModifier: true}
	switch val {
	case gdk.KEY_Left, gdk.KEY_KP_Left:
		p.Special = calendar.SpecialLeft
	case gdk.KEY_Right, gdk.KEY_KP_Right:
		p.Special = calendar.SpecialRight
	case gdk.KEY_Up, gdk.KEY_KP_Up:
		p.Special = calendar.SpecialUp
	case gdk.KEY_Down, gdk.KEY_KP_Down:
		p.Special = calendar.SpecialDown
	case gdk.KEY_Return, gdk.KEY_KP_Enter, gdk.KEY_ISO_Enter:
		p.Special = calendar.SpecialReturn
	case gdk.KEY_Delete, gdk.KEY_KP_Delete, gdk.KEY_BackSpace:
		p.Special = calendar.SpecialDelete
	case gdk.KEY_Escape:
		p.Special = calendar.SpecialEscape
	default:
		p.Rune = rune(gdk.KeyvalToUnicode(gdk.KeyvalToLower(val)))
		if p.Rune == 0 {
			return calendar.Press{}, false
		}
	}
	return p, true
}

// ownsKeys reports whether a key pressed now is the page's to act on:
// not while the keyboard sits in a popover (its own Escape closes it, its
// arrows are its own) or outside the page.
func (p *calendarPage) ownsKeys() bool {
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
