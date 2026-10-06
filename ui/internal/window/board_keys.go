// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/internal/board"
)

// The keyboard of the board's Columns and Today styles. Up and Down are
// the ListBoxes' own; the rest is here, in a file of its own so that the
// files carrying texts keep their line numbers (po/POTFILES). Reference:
// macOS BoardColumnTableView.keyDown and BoardTodayViewController.
//
// Selecting a case opens the sliding detail panel (renderPanel), so Return
// on a selected row only selects it again, which is what a click does.

// wireKeys connects Left and Right across the columns and Return in the
// columns and in Today; called from ensureBoard after bindColumns and
// wireToday.
func (p *boardPage) wireKeys() {
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
