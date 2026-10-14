// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// The typing gate of the single-key mail shortcuts (MessageAccels: a, j,
// s, u, Delete). They are application accelerators, which GTK runs in the
// window's capture phase, before the focused widget sees the key, so a
// letter typed into any text field of the main window — the search entry,
// the assistant's question, a row of Preferences or of a wizard presented
// over the window — would act on the selected message instead of being
// typed. The gate lifts them while the keyboard is in such a field and
// installs them again when it leaves (setTypingAccels, search.go), the way
// the macOS client refuses its bare-key menu items while an editable text
// view is the first responder and the Windows client drops them while a
// TextBox has the focus. The key's own character is never inspected.

// wireTypingGate watches the window's focus (the focus-widget property,
// which follows the keyboard into popovers and into dialogs presented over
// the window) and keeps the gate in step with it; called once from New.
func (w *Window) wireTypingGate() {
	w.NotifyProperty("focus-widget", func() {
		w.setTypingAccels(!focusTakesTyping(w.Focus()))
	})
}

// focusTakesTyping reports whether a letter typed with the keyboard on f
// is text input: f is an editable text widget — a GtkText (the inside of
// an Entry, a SearchEntry, a PasswordEntry, a SpinButton and an
// Adw.EntryRow, which is what the window reports as focused) or an
// editable TextView. A read-only TextView (the assistant's answers) is
// not: nothing is typed there, so the shortcuts keep working, as on macOS.
// Shared with the board's keyboardInText (board_keys.go).
func focusTakesTyping(f gtk.Widgetter) bool {
	if f == nil {
		return false
	}
	switch v := gtk.BaseWidget(f).Cast().(type) {
	case *gtk.Text:
		return v.Editable()
	case *gtk.TextView:
		return v.Editable()
	case *gtk.Entry:
		return v.Editable()
	case *gtk.SearchEntry:
		return v.Editable()
	case *gtk.PasswordEntry:
		return v.Editable()
	case *gtk.SpinButton:
		return v.Editable()
	}
	return false
}
