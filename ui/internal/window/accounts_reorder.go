// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/graphene"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// Reordering the accounts of the preferences dialog. The order belongs to
// the daemon (account.reorder); the sidebar of the main window follows
// through notify.accountsChanged, so nothing here talks to it.
//
// The drag payload is the account id as a string, because it is the only
// identifier that survives setAccounts tearing every row down. Positions
// come from the d.accountRows slice, never from ListBoxRow.Index(), which
// counts the hidden "no accounts yet" row of the blueprint as well.

// addRowReorder makes the row draggable by its handle and a drop target for
// the other rows, and binds Ctrl+Up / Ctrl+Down. Called from newAccountRow.
func (d *PreferencesDialog) addRowReorder(c *client.Client, row *accountRow) {
	// A plain click on the handle would otherwise reach the row, and the row
	// activates its switch (SetActivatableWidget) — clicking the handle must
	// not pause the account. Claim the sequence on release, not on press:
	// claiming on press would deny the drag source below, which only claims
	// once the pointer has actually moved.
	click := gtk.NewGestureClick()
	click.ConnectReleased(func(int, float64, float64) { click.SetState(gtk.EventSequenceClaimed) })
	row.handle.AddController(click)

	// The drag source sits on the row, not on the handle, so that the
	// coordinates it reports are the row's own and can be tested against the
	// handle's bounds. Only a press on the handle starts a drag; anywhere
	// else prepare returns nil and the gesture is dropped, which also keeps
	// the switch and the buttons untouched.
	src := gtk.NewDragSource()
	src.SetActions(gdk.ActionMove)
	src.ConnectPrepare(func(x, y float64) *gdk.ContentProvider {
		if !onHandle(row, x, y) {
			return nil
		}
		return gdk.NewContentProviderForValue(coreglib.NewValue(string(row.account.ID)))
	})
	src.ConnectDragBegin(func(gdk.Dragger) {
		// Keep the paintable alive for as long as the drag lasts.
		icon := gtk.NewWidgetPaintable(row)
		// The icon hangs from the pointer by its top-left corner. Anchoring
		// it at the grab point instead landed it a quarter of the screen away
		// from the pointer (Wayland, 2× display): the hotspot does not reach
		// the compositor as given, and a zero one is immune to whatever
		// transforms it.
		src.SetIcon(icon, 0, 0)
		row.AddCSSClass("dragging")
	})
	end := func() {
		row.RemoveCSSClass("dragging")
		// A row destroyed under the pointer never gets its leave signal.
		d.clearDropHints()
	}
	src.ConnectDragEnd(func(gdk.Dragger, bool) { end() })
	src.ConnectDragCancel(func(gdk.Dragger, gdk.DragCancelReason) bool { end(); return false })
	row.AddController(src)

	dst := gtk.NewDropTarget(coreglib.TypeString, gdk.ActionMove)
	// Without preloading, DropTarget.Value() is nil until the drop itself, so
	// the motion handler below could never tell whose id is being dragged and
	// would refuse every drop.
	dst.SetPreload(true)
	dst.ConnectMotion(func(_, y float64) gdk.DragAction {
		from, to, ok := d.dropTarget(dst.Value(), row, y)
		if !ok || from == to {
			row.setDropHint(hintNone)
			return 0
		}
		if y < float64(row.Height())/2 {
			row.setDropHint(hintAbove)
		} else {
			row.setDropHint(hintBelow)
		}
		return gdk.ActionMove
	})
	dst.ConnectLeave(func() { row.setDropHint(hintNone) })
	dst.ConnectDrop(func(value *coreglib.Value, _, y float64) bool {
		d.clearDropHints()
		from, to, ok := d.dropTarget(value, row, y)
		if !ok || from == to {
			return false
		}
		// Never rebuild the rows from inside the drop handler: it would
		// destroy the widget whose controller is still running.
		glib.IdleAdd(func() {
			if d.closed {
				return
			}
			d.reorderAccounts(c, from, to, false)
		})
		return true
	})
	row.AddController(dst)

	sc := gtk.NewShortcutController()
	sc.SetScope(gtk.ShortcutScopeLocal)
	for _, s := range []struct {
		trigger string
		delta   int
	}{{"<Control>Up", -1}, {"<Control>Down", 1}} {
		delta := s.delta
		sc.AddShortcut(gtk.NewShortcut(
			gtk.NewShortcutTriggerParseString(s.trigger),
			gtk.NewCallbackAction(func(gtk.Widgetter, *glib.Variant) bool {
				return d.moveAccountBy(c, row.account.ID, delta)
			}),
		))
	}
	row.AddController(sc)
}

// onHandle reports whether the point, in the row's coordinates, is on the
// row's drag handle. A row whose handle is not laid out yet cannot be
// dragged.
func onHandle(row *accountRow, x, y float64) bool {
	bounds, ok := row.handle.ComputeBounds(row)
	if !ok {
		return false
	}
	// graphene.Point wraps a C allocation; its zero value has none and
	// crashes on the first method call, so it must be allocated.
	p := graphene.NewPointAlloc()
	p.Init(float32(x), float32(y))
	return bounds.ContainsPoint(p)
}

// dropTarget resolves a drag payload against the current rows: the index it
// comes from and the index it would land on when dropped on row at height y.
// It reports false for anything that is not one of our account ids — the
// target accepts strings, so text dragged in from another application ends
// up here too.
func (d *PreferencesDialog) dropTarget(value *coreglib.Value, row *accountRow, y float64) (from, to int, ok bool) {
	if value == nil || value.Type() != coreglib.TypeString {
		return 0, 0, false
	}
	from = d.rowIndex(api.AccountID(value.String()))
	target := d.rowIndex(row.account.ID)
	if from < 0 || target < 0 {
		return 0, 0, false
	}
	return from, insertIndex(from, target, y < float64(row.Height())/2), true
}

// rowIndex is the position of the account among the rows, or -1.
func (d *PreferencesDialog) rowIndex(id api.AccountID) int {
	for i, row := range d.accountRows {
		if row.account.ID == id {
			return i
		}
	}
	return -1
}

// clearDropHints removes every insertion line.
func (d *PreferencesDialog) clearDropHints() {
	for _, row := range d.accountRows {
		row.setDropHint(hintNone)
	}
}

// insertIndex is the slot a row dragged from `from` takes when it is dropped
// on the row at `target`: before that row when the pointer is in its upper
// half, after it otherwise. The result is an index in the list with the
// dragged row already taken out, so `from` means "no change".
func insertIndex(from, target int, above bool) int {
	to := target
	if !above {
		to++
	}
	if from < to {
		to--
	}
	return to
}

// moveAccountBy shifts the account by delta positions (the keyboard path).
// It reports whether it moved anything, so the shortcut is not swallowed at
// the ends of the list.
func (d *PreferencesDialog) moveAccountBy(c *client.Client, id api.AccountID, delta int) bool {
	from := d.rowIndex(id)
	if from < 0 {
		return false
	}
	to := from + delta
	if to < 0 || to >= len(d.accountRows) {
		return false
	}
	d.reorderAccounts(c, from, to, true)
	return true
}

// reorderAccounts moves the row at from to to: the rows are rebuilt at once
// so the gesture feels immediate, then account.reorder saves the order and
// the daemon's notify.accountsChanged reorders the main window's sidebar. A
// failure reloads the page, because the daemon's order is the truth. With
// focusMoved the moved row takes the focus again, so holding Ctrl+Down keeps
// walking the account down the list.
func (d *PreferencesDialog) reorderAccounts(c *client.Client, from, to int, focusMoved bool) {
	if from == to || from < 0 || to < 0 || from >= len(d.accountRows) || to >= len(d.accountRows) {
		return
	}
	// Snapshot the accounts, not the rows: setAccounts destroys the rows.
	accounts := make([]api.Account, 0, len(d.accountRows))
	for _, row := range d.accountRows {
		accounts = append(accounts, row.account)
	}
	moved := accounts[from].ID
	accounts = moveAccount(accounts, from, to)
	ids := make([]api.AccountID, len(accounts))
	for i, a := range accounts {
		ids[i] = a.ID
	}

	d.setAccounts(c, accounts)
	if focusMoved {
		if i := d.rowIndex(moved); i >= 0 {
			d.accountRows[i].GrabFocus()
		}
	}
	d.accountsGroup.SetSensitive(false)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		err := c.Call(ctx, api.MethodAccountReorder,
			api.AccountReorderParams{AccountIDs: ids}, &api.AccountReorderResult{})
		glib.IdleAdd(func() {
			if d.closed {
				return
			}
			d.accountsGroup.SetSensitive(true)
			if err != nil {
				d.AddToast(widget.PlainToast(widget.RPCErrorText(i18n.T("Saving the account order"), err)))
				d.loadAccounts(c)
			}
		})
	}()
}

// moveAccount returns accounts with the entry at from moved to to. The
// input is left alone.
func moveAccount(accounts []api.Account, from, to int) []api.Account {
	if from == to || from < 0 || to < 0 || from >= len(accounts) || to >= len(accounts) {
		return append([]api.Account(nil), accounts...)
	}
	out := make([]api.Account, 0, len(accounts))
	out = append(out, accounts[:from]...)
	out = append(out, accounts[from+1:]...)
	rest := append([]api.Account(nil), out[to:]...)
	out = append(out[:to], accounts[from])
	return append(out, rest...)
}
