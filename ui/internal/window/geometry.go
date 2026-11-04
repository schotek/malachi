// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import "github.com/schotek/malachi/ui/internal/settings"

// bindGeometry brings the main window back at the size and maximized state
// it had last time. GTK 4 keeps default-width and default-height at the
// window's unmaximized size while the user resizes it, so binding them and
// maximized to GSettings both restores the geometry and records every
// change; a maximized window still remembers the size it unmaximizes to.
// The window lives as long as the application, so the bindings stay.
func (w *Window) bindGeometry() {
	obj := w.ApplicationWindow.Object
	w.settings.Bind(settings.KeyWindowWidth, obj, "default-width")
	w.settings.Bind(settings.KeyWindowHeight, obj, "default-height")
	w.settings.Bind(settings.KeyWindowMaximized, obj, "maximized")
}
