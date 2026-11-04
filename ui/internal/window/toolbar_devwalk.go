// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"fmt"
	"os"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/internal/toolbar"
)

// devToolbarWalk is the MALACHI_GTK_START step toolbar-walk, a
// development aid: it puts Contacts into the Mail toolbar's folders
// section, opens the editor over the window and, every 150 ms, moves it
// one place with Ctrl+↓ and later Ctrl+↑ through the rows' own shortcut
// controllers (as a key press does), then by drops, printing each step to
// stderr. It reproduces the owner's moves of 2026-11-04 without a pointer.
func (w *Window) devToolbarWalk() {
	if w.settings != nil {
		w.settings.SetToolbarLayout(toolbar.BarMail, []string{"mode", "contacts", "newMessage", "flexibleSpace",
			"|list", "refresh", "flexibleSpace", "search"})
	}
	e := newToolbarEditor(w.settings, w.toolbarFeatures, toolbar.BarMail)
	e.dialog.Present(w)
	at := func() int { return indexOfID(e.ed.Layout(), toolbar.Contacts) }
	steps := 0
	down := true
	glib.TimeoutAdd(150, func() bool {
		steps++
		i := at()
		sc, row := e.keys[i], e.rows[i]
		if sc == nil || row == nil || steps > 60 {
			fmt.Fprintf(os.Stderr, "MALACHI_GTK_START toolbar-walk done after %d steps\n", steps)
			return false
		}
		n := uint(1)
		if !down {
			n = 0
		}
		ok := gtk.BaseShortcutAction(sc.Item(n).Cast().(*gtk.Shortcut).Action()).Activate(0, row, nil)
		fmt.Fprintf(os.Stderr, "MALACHI_GTK_START toolbar-walk step %d down=%v moved=%v at %d %v\n",
			steps, down, ok, at(), e.ed.Layout().Tokens())
		if !ok {
			down = !down
		}
		return true
	})
}

// devToolbarDrag is the step toolbar-drag: Contacts into the folders
// section, the editor over the window, and after every drawing of the
// rows a line "MALACHI_GTK_START drag <id> <x> <y> <rowy> <rowh>" per row
// with the middle of its handle and its row in the window's surface
// coordinates, for a driver that drags with the X server's XTest.
func (w *Window) devToolbarDrag() {
	if w.settings != nil {
		w.settings.SetToolbarLayout(toolbar.BarMail, []string{"mode", "contacts", "newMessage", "flexibleSpace",
			"|list", "refresh", "flexibleSpace", "search"})
	}
	e := newToolbarEditor(w.settings, w.toolbarFeatures, toolbar.BarMail)
	report := func() {
		glib.TimeoutAdd(300, func() bool {
			sx, sy := w.ApplicationWindow.Window.Root.NativeSurface.SurfaceTransform()
			fmt.Fprintf(os.Stderr, "MALACHI_GTK_START drag-layout %v\n", e.ed.Layout().Tokens())
			for i, row := range e.rows {
				id := e.ed.Layout()[i]
				h := e.handles[i]
				if h == nil {
					continue
				}
				b, ok := row.ComputeBounds(w)
				hb, hok := h.ComputeBounds(w)
				if !ok || !hok {
					continue
				}
				hx, hy := float64(hb.X()+hb.Width()/2), float64(hb.Y()+hb.Height()/2)
				fmt.Fprintf(os.Stderr, "MALACHI_GTK_START drag %s %.0f %.0f %.0f %.0f\n", id, hx+sx, hy+sy, float64(b.Y())+sy, float64(b.Height()))
			}
			return false
		})
	}
	e.onRebuilt = report
	e.dialog.Present(w)
	report()
}

func indexOfID(l toolbar.Layout, id toolbar.ID) int {
	for i, x := range l {
		if x == id {
			return i
		}
	}
	return -1
}
