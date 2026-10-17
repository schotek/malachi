// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// wireModeKeys makes Ctrl+1, Ctrl+2 and Ctrl+3 (win.show-mail,
// win.show-board, win.show-calendar) and the Calendar's views
// (Ctrl+Shift+1–4) independent of the keyboard layout. The accelerators
// (ModeAccels, "<Control>1" …) match the typed character, and a Czech
// QWERTZ types +, ě and š on those keys, so they never fire there. A
// capture-phase key controller on the window reads the physical
// number-row key from the hardware keycode instead (board.NumberRowDigit).
// With Ctrl and no other modifier it is a mode key (board.KeyFor,
// calendar.KeyFor); a key whose own character already is the digit is left
// to the accelerator, so nothing runs twice, and whichever of the two sees
// the key first consumes it. With Ctrl+Shift it is a view key of the
// Calendar (calendar.KeyFor with Shift standing for Control), which acts
// only in Calendar mode and goes to calendarKey (calendar.go).
func (w *Window) wireModeKeys() {
	keys := gtk.NewEventControllerKey()
	keys.SetPropagationPhase(gtk.PhaseCapture)
	keys.ConnectKeyPressed(func(val, code uint, state gdk.ModifierType) bool {
		var shift bool
		switch state & gtk.AcceleratorGetDefaultModMask() {
		case gdk.ControlMask:
		case gdk.ControlMask | gdk.ShiftMask:
			shift = true
		default:
			return false
		}
		if !shift {
			if r := rune(gdk.KeyvalToUnicode(val)); r == '1' || r == '2' || r == '3' {
				return false // the accelerator's
			}
		}
		digit := board.NumberRowDigit(code)
		if digit == 0 {
			return false
		}
		press := calendar.Press{Rune: digit, Primary: true, Shift: shift, ShiftModifier: true}
		ctx := calendar.KeyContext{Mode: w.mode}
		if shift {
			k, ok := calendar.KeyFor(press, ctx, i18n.Tr)
			if !ok {
				return false
			}
			w.calendarKey(k)
			return true
		}
		name := ""
		if action, ok := board.KeyFor(digit, true, false, false, w.mode, i18n.Tr); ok {
			switch action {
			case board.KeyShowMail:
				name = "show-mail"
			case board.KeyShowBoard:
				name = "show-board"
			}
		} else if k, ok := calendar.KeyFor(press, ctx, i18n.Tr); ok && k == calendar.KeyShowCalendar {
			name = "show-calendar"
		}
		a := w.actions[name]
		if a == nil || !a.Enabled() {
			return false
		}
		a.Activate(nil)
		return true
	})
	w.AddController(keys)
}
