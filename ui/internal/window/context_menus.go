// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"strings"

	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/contextmenu"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The right-click menus of the message list and of the sidebar
// (ui/internal/contextmenu decides what they offer; macOS and Windows have
// the same). A right click, a long press, Shift+F10 or the Menu key opens
// them. On a message the row is selected first, and the menu acts on the
// selection through the window's win.* actions, so its items are enabled
// exactly as the header bar's are; on a folder or an account heading the
// menu acts on the row clicked and the selection stays.

// messageMenuActions are the win.* actions of the message menu's items;
// Move To and the Assistant are submenus.
var messageMenuActions = map[contextmenu.ItemID]string{
	contextmenu.OpenMessage:  "win.open-message",
	contextmenu.EditDraft:    "win.open-message",
	contextmenu.Reply:        "win.reply",
	contextmenu.ReplyAll:     "win.reply-all",
	contextmenu.Forward:      "win.forward",
	contextmenu.MarkRead:     "win.mark-read",
	contextmenu.MarkUnread:   "win.mark-unread",
	contextmenu.ToggleFlag:   "win.toggle-flag",
	contextmenu.ChangeStatus: "win.change-status",
	contextmenu.Archive:      "win.archive",
	contextmenu.Junk:         "win.junk",
	contextmenu.Trash:        "win.trash",
}

// menuTriggers are the keys that open a row's menu.
const menuTriggers = "<Shift>F10|Menu"

// addRowMenu opens open's menu on row by a right click and a long press.
func addRowMenu(row gtk.Widgetter, open func(x, y float64)) {
	base := gtk.BaseWidget(row)
	click := gtk.NewGestureClick()
	click.SetButton(3) // the secondary button
	click.ConnectPressed(func(_ int, x, y float64) { open(x, y) })
	base.AddController(click)
	long := gtk.NewGestureLongPress()
	long.ConnectPressed(func(x, y float64) { open(x, y) })
	base.AddController(long)
}

// addMenuKeys opens a menu on list by Shift+F10 and the Menu key, for the
// row open finds (false: no menu).
func addMenuKeys(list *gtk.ListBox, open func() bool) {
	sc := gtk.NewShortcutController()
	sc.AddShortcut(gtk.NewShortcut(
		gtk.NewShortcutTriggerParseString(menuTriggers),
		gtk.NewCallbackAction(func(gtk.Widgetter, *glib.Variant) bool { return open() }),
	))
	list.AddController(sc)
}

// rowMenuPoint is where a menu opened from the keyboard points: near the
// start of the row, at half its height.
func rowMenuPoint(row *gtk.Widget) (x, y float64) {
	return 24, float64(row.Height()) / 2
}

// wireContextMenus gives both lists their menu keys; the rows get their
// gestures as they are made (attachMessageMenu, attachFolderMenu).
func (w *Window) wireContextMenus() {
	addMenuKeys(w.messageList, func() bool {
		// The focused row when it is selected, else the first selected.
		sel := w.messageList.SelectedRows()
		if len(sel) == 0 {
			return false
		}
		row := sel[0]
		if focus, ok := w.messageList.FocusChild().(*gtk.ListBoxRow); ok && focus != nil && focus.IsSelected() {
			row = focus
		}
		x, y := rowMenuPoint(&row.Widget)
		w.showMessageMenu(row, x, y)
		return true
	})
	addMenuKeys(w.folderList, func() bool {
		row := w.folderList.SelectedRow()
		if row == nil {
			if focus := w.folderList.FocusChild(); focus != nil {
				row, _ = focus.(*gtk.ListBoxRow)
			}
		}
		if row == nil {
			return false
		}
		x, y := rowMenuPoint(&row.Widget)
		return w.showFolderMenuAt(&row.Widget, row.Index(), x, y)
	})
}

// attachMessageMenu gives a message row its menu.
func (w *Window) attachMessageMenu(r *gtk.ListBoxRow) {
	addRowMenu(r, func(x, y float64) { w.showMessageMenu(r, x, y) })
}

// showMessageMenu shows the menu of row at (x, y) in it: of the
// selection it belongs to, or of row alone, selected first.
func (w *Window) showMessageMenu(r *gtk.ListBoxRow, x, y float64) {
	if _, ok := w.rowForWidget(r); !ok {
		return // a date heading
	}
	if !r.IsSelected() {
		w.selectOnly(r)
	}
	w.setMessageActionsSensitive(true)
	w.syncAssistantActions()
	rows := w.selectedRows()
	if len(rows) == 0 {
		return
	}
	row := rows[0]
	st := w.selectionState(true)
	assistantShown := w.assist != nil && w.assist.shown()
	s := contextmenu.MessageState{
		Count:         st.count,
		Open:          st.open,
		Draft:         w.model.inDrafts(row.Message),
		Outbox:        st.outbox,
		OfferReply:    st.supported.Reply,
		OfferReplyAll: st.supported.ReplyAll,
		OfferForward:  st.supported.Forward,
		OfferArchive:  st.supported.Archive,
		OfferMove:     st.supported.Move,
		OfferJunk:     st.supported.Junk,
		OfferTrash:    st.supported.Trash,
		Reply:         st.reply,
		ReplyAll:      st.replyAll,
		Forward:       st.forward,
		MarkRead:      st.markRead,
		MarkUnread:    st.markUnread,
		ToggleFlag:    st.toggleFlag,
		Archive:       st.archive,
		Move:          st.move,
		Junk:          st.junk,
		Trash:         st.trash,
		ChangeStatus:  st.changeStatus,
		Assistant:     assistantShown,
	}
	labels := contextmenu.Labels{Comment: st.comment, Flagged: st.flagged, Outbox: st.outbox, Count: st.count}
	m := gio.NewMenu()
	for _, g := range contextmenu.MessageItems(s) {
		section := gio.NewMenu()
		for _, it := range g {
			label := contextmenu.Label(i18n.Tr, it.ID, labels)
			switch it.ID {
			case contextmenu.MoveTo:
				item := gio.NewMenuItem(label, "")
				if it.Enabled {
					item.SetSubmenu(w.moveToMenu(row.Message.AccountID, commonFolder(rows)))
				}
				section.AppendItem(item)
			case contextmenu.Assistant:
				menu, update := w.assist.assistantMenuFor("win", false, len(rows))
				update()
				item := gio.NewMenuItem(label, "")
				item.SetSubmenu(menu)
				section.AppendItem(item)
			default:
				section.Append(label, messageMenuActions[it.ID])
			}
		}
		m.AppendSection("", section)
	}
	popupContextMenu(&r.Widget, &w.messageList.Widget, m, "", nil, x, y)
}

// commonFolder is the folder every row is in, "" when they differ (a
// search).
func commonFolder(rows []listRow) api.FolderID {
	f := rows[0].Message.FolderID
	for _, r := range rows[1:] {
		if r.Message.FolderID != f {
			return ""
		}
	}
	return f
}

// moveToMenu is the Move To submenu of the selection: its account's
// folders as the sidebar orders them, indented by depth; the current
// folder (when all are in one) and the containers without an action, so
// insensitive.
func (w *Window) moveToMenu(account api.AccountID, current api.FolderID) *gio.Menu {
	acc, _ := w.model.account(account)
	var entries []contextmenu.Entry
	for _, e := range folderTree(acc, w.model.folders[account], newCollapseState(), newFavouriteState()) {
		if !e.Header {
			entries = append(entries, contextmenu.Entry{Folder: e.Folder, Depth: e.Depth})
		}
	}
	m := gio.NewMenu()
	for _, t := range contextmenu.MoveTargets(entries, current) {
		item := gio.NewMenuItem(strings.Repeat("\u2003", t.Depth)+folderTitle(t.Folder), "")
		if t.Enabled {
			item.SetActionAndTargetValue("win.move-to", glib.NewVariantString(string(t.Folder.ID)))
		}
		m.AppendItem(item)
	}
	return m
}

// attachFolderMenu gives the sidebar's row idx (of w.model.entries) its
// menu.
func (w *Window) attachFolderMenu(row gtk.Widgetter, idx int) {
	base := gtk.BaseWidget(row)
	addRowMenu(row, func(x, y float64) { w.showFolderMenuAt(base, idx, x, y) })
}

// showFolderMenuAt shows the menu of the sidebar's row idx at (x, y) in
// anchor: an account heading's or a folder's (the Favourites heading has
// none). False when there is nothing to offer.
func (w *Window) showFolderMenuAt(anchor *gtk.Widget, idx int, x, y float64) bool {
	if idx < 0 || idx >= len(w.model.entries) {
		return false
	}
	e := w.model.entries[idx]
	group := gio.NewSimpleActionGroup()
	actions := map[contextmenu.ItemID]func(){}
	var groups [][]contextmenu.Item
	var labels contextmenu.Labels
	switch {
	case e.Header && e.Favourite:
		return false
	case e.Header:
		acc := e.Account.ID
		groups = contextmenu.AccountItems(true)
		labels.Collapsed = e.Collapsed
		actions[contextmenu.CheckMail] = func() { w.triggerAccountSync(acc) }
		actions[contextmenu.Fold] = func() { w.toggleAccount(acc) }
	default:
		k := folderKey{Account: e.Account.ID, Folder: e.Folder.ID}
		f, ok := w.model.folder(k)
		if !ok {
			f = e.Folder
		}
		groups = contextmenu.FolderItems(contextmenu.FolderState{
			Selectable: f.Selectable,
			Role:       f.Role,
			Unread:     f.Unread,
			Total:      f.Total,
			CanDelete:  e.Account.Can(api.CapabilityDelete),
			Foldable:   e.HasChildren && !e.Favourite,
		})
		labels = contextmenu.Labels{Junk: f.Role == api.RoleJunk, Favourite: e.Starred, Collapsed: e.Collapsed}
		actions[contextmenu.CheckMail] = func() { w.checkFolder(f) }
		actions[contextmenu.MarkAllRead] = func() { w.markFolderRead(k) }
		actions[contextmenu.EmptyFolder] = func() { w.emptyFolder(k) }
		actions[contextmenu.Favourite] = func() { w.toggleFavourite(k) }
		actions[contextmenu.Fold] = func() { w.toggleFolder(k) }
	}
	if len(groups) == 0 {
		return false
	}
	m := gio.NewMenu()
	for _, g := range groups {
		section := gio.NewMenu()
		for _, it := range g {
			fn := actions[it.ID]
			a := gio.NewSimpleAction(string(it.ID), nil)
			a.SetEnabled(it.Enabled && fn != nil)
			a.ConnectActivate(func(*glib.Variant) { fn() })
			group.AddAction(a)
			section.Append(contextmenu.Label(i18n.Tr, it.ID, labels), "folderctx."+string(it.ID))
		}
		m.AppendSection("", section)
	}
	popupContextMenu(anchor, &w.folderList.Widget, m, "folderctx", group, x, y)
	return true
}

// checkFolder synchronises one folder; an issue tracker's view is a
// selection of its account's issues, so the whole account.
func (w *Window) checkFolder(f api.Folder) {
	if f.Virtual != "" {
		w.triggerAccountSync(f.AccountID)
		return
	}
	w.startSync(api.SyncTriggerParams{AccountID: f.AccountID, FolderID: f.ID})
}

// markFolderRead is Mark All as Read (folder.markRead). The daemon's
// notify.messagesChanged brings the counts and the list up to date.
func (w *Window) markFolderRead(k folderKey) {
	w.call(contextmenu.MarkingReadText(i18n.Tr), api.MethodFolderMarkRead,
		api.FolderMarkReadParams{AccountID: k.Account, FolderID: k.Folder}, nil)
}

// emptyFolder is Empty Trash or Empty Junk (folder.empty), always after
// the question; the notification brings the folder up to date.
func (w *Window) emptyFolder(k folderKey) {
	f, ok := w.model.folder(k)
	if !ok {
		return
	}
	junk := f.Role == api.RoleJunk
	q := contextmenu.EmptyQuestion(i18n.Tr, junk, f.Total)
	widget.ConfirmDestructive(w, q.Heading, q.Body, q.Confirm, func() {
		w.call(contextmenu.EmptyingText(i18n.Tr, junk), api.MethodFolderEmpty,
			api.FolderEmptyParams{AccountID: k.Account, FolderID: k.Folder}, nil)
	})
}

// popupContextMenu shows m as a popover menu pointing at (x, y) in anchor
// (board_actions.go showCaseContextMenu does the same for a case). It
// hangs on list, not on the row: the list may rebuild its rows while the
// menu is open, and a row going with a popover still parented to it
// leaves the menu on a freed parent. group, when given, is inserted under
// name for the menu's own actions.
func popupContextMenu(anchor, list *gtk.Widget, m *gio.Menu, name string, group *gio.SimpleActionGroup, x, y float64) {
	pm := gtk.NewPopoverMenuFromModel(m)
	if group != nil {
		pm.InsertActionGroup(name, group)
	}
	if px, py, ok := anchor.TranslateCoordinates(list, x, y); ok {
		x, y = px, py
	}
	pm.SetParent(list)
	rect := gdkRectangleAt(x, y)
	pm.SetPointingTo(&rect)
	pm.SetHasArrow(false)
	pm.SetHAlign(gtk.AlignStart)
	// A popover set on a widget stays its child until unparented; closed,
	// it lets go of the list once its own closing has run (idle: an
	// activated item's action runs first).
	pm.ConnectClosed(func() {
		glib.IdleAdd(func() {
			if pm.Parent() != nil {
				pm.Unparent()
			}
		})
	})
	pm.Popup()
}
