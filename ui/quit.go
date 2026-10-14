// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package main

// boardExit holds a close/quit request while replies settle and, if that
// fails, while the user decides. The GTK main loop owns it throughout.
type boardExit struct{ pending bool }

func (e *boardExit) request(save, confirm func(func(bool)), resume, proceed func()) {
	if e.pending {
		return
	}
	e.pending = true
	save(func(saved bool) {
		finish := func(allow bool) {
			if allow {
				proceed()
			} else {
				resume()
			}
			e.pending = false
		}
		if saved {
			finish(true)
		} else {
			confirm(finish)
		}
	})
}
