// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// wireModeKeys makes Ctrl+1 and Ctrl+2 (win.show-mail, win.show-board)
// independent of the keyboard layout. The accelerators (ModeAccels,
// "<Control>1", "<Control>2") match the typed character, and a Czech
// QWERTZ types + and ě on those keys, so they never fire there. A
// capture-phase key controller on the window reads the physical
// number-row key from the hardware keycode instead (board.NumberRowDigit)
// with Ctrl and no other modifier. A key whose own character already is
// the digit is left to the accelerator, so nothing runs twice; whichever of
// the two sees the key first consumes it.
func (w *Window) wireModeKeys() {
	keys := gtk.NewEventControllerKey()
	keys.SetPropagationPhase(gtk.PhaseCapture)
	keys.ConnectKeyPressed(func(val, code uint, state gdk.ModifierType) bool {
		if state&gtk.AcceleratorGetDefaultModMask() != gdk.ControlMask {
			return false
		}
		if r := rune(gdk.KeyvalToUnicode(val)); r == '1' || r == '2' {
			return false // the accelerator's
		}
		digit := board.NumberRowDigit(code)
		if digit == 0 {
			return false
		}
		action, ok := board.KeyFor(digit, true, false, false, w.mode, i18n.Tr)
		if !ok {
			return false
		}
		name := ""
		switch action {
		case board.KeyShowMail:
			name = "show-mail"
		case board.KeyShowBoard:
			name = "show-board"
		default:
			return false
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
