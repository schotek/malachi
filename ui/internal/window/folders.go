// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"strconv"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// Accounts and the folder sidebar: account.list, folder.list per enabled
// account, the folder rows and the reactions to sync / new-message events
// that touch the sidebar.

// folderIndent is the sidebar indentation per tree level, in pixels.
const folderIndent = 12

// folderHeadingGap is the space above a sidebar heading, in pixels: enough
// to set the sections apart, in keeping with the density the rows have
// (.folder-list in internal/style).
const folderHeadingGap = 3

// folderRow is one selectable sidebar row with its unread badge, its fold
// arrow (nested accounts only) and its pin star (selectable folders only).
type folderRow struct {
	*adw.ActionRow
	badge  *gtk.Label
	twisty *gtk.Button
	star   *gtk.Button
}

// rowKey addresses a sidebar row: the folder, and whether the row is the one
// in the Favourites section or the one in the account's tree — a pinned
// folder has both.
type rowKey struct {
	folderKey
	fav bool
}

// setUnread shows n on the badge, hiding it at zero.
func (r *folderRow) setUnread(n int) {
	r.badge.SetVisible(n > 0)
	r.badge.SetText(strconv.Itoa(n))
}

// newTwisty builds the fold arrow shown left of a row's icon. A row that
// cannot be folded still gets one, invisible and inert, so that its title
// lines up with its foldable siblings.
func newTwisty(collapsed, active bool) *gtk.Button {
	name := "pan-down-symbolic"
	tip := i18n.T("Collapse")
	if collapsed {
		name = "pan-end-symbolic"
		tip = i18n.T("Expand")
	}
	b := gtk.NewButtonFromIconName(name)
	b.AddCSSClass("flat")
	b.AddCSSClass("folder-twisty")
	b.SetVAlign(gtk.AlignCenter)
	if !active {
		b.SetOpacity(0)
		b.SetSensitive(false)
		b.SetCanTarget(false)
		b.SetCanFocus(false)
		return b
	}
	b.SetTooltipText(tip)
	return b
}

// loadAccounts runs account.list, then folder.list for every enabled
// account, rebuilds the sidebar and selects the initial folder. Every
// reply is guarded by the folders generation, so a reconnect or a repeated
// call in the meantime simply wins.
func (w *Window) loadAccounts() {
	gen := w.model.bumpFolders()
	if len(w.model.entries) == 0 {
		w.showFolderStatus("", i18n.T("Loading…"), "")
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		var res api.AccountListResult
		err := w.client.Call(ctx, api.MethodAccountList, api.AccountListParams{}, &res)
		glib.IdleAdd(func() {
			if gen != w.model.foldersGen {
				return
			}
			if err != nil {
				w.log.Warn("account.list", "err", err)
				if len(w.model.entries) == 0 {
					w.showFolderStatus("dialog-warning-symbolic", i18n.T("Folders Unavailable"),
						widget.RPCErrorText(i18n.T("Loading folders"), err))
				}
				return
			}
			w.model.accounts = res.Accounts
			w.model.folders = make(map[api.AccountID][]api.Folder, len(res.Accounts))
			w.model.folderErr = make(map[api.AccountID]error)
			w.hasAccounts = len(res.Accounts) > 0
			if w.messageList.SelectedRow() == nil {
				w.messageStack.SetVisibleChildName(w.emptyPageName())
			}

			enabled := w.model.enabledAccounts()
			if len(enabled) == 0 {
				w.rebuildFolderList()
				return
			}
			// One rebuild once every account answered (or failed).
			pending := len(enabled)
			for _, a := range enabled {
				w.fetchFolders(a.ID, gen, func() {
					pending--
					if pending == 0 {
						w.rebuildFolderList()
					}
				})
			}
		})
	}()
}

// loadFolders runs folder.list for one account and rebuilds the sidebar
// from the answer; gen guards the reply.
func (w *Window) loadFolders(acc api.AccountID, gen uint64) {
	w.fetchFolders(acc, gen, w.rebuildFolderList)
}

// fetchFolders runs folder.list for acc in the background and stores the
// result (or the error) in the model, then calls done on the main loop.
// A reply from a stale generation is dropped without calling done.
func (w *Window) fetchFolders(acc api.AccountID, gen uint64, done func()) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		var res api.FolderListResult
		err := w.client.Call(ctx, api.MethodFolderList, api.FolderListParams{AccountID: acc}, &res)
		glib.IdleAdd(func() {
			if gen != w.model.foldersGen {
				return
			}
			if w.model.folders == nil {
				w.model.folders = make(map[api.AccountID][]api.Folder)
			}
			if w.model.folderErr == nil {
				w.model.folderErr = make(map[api.AccountID]error)
			}
			if err != nil {
				w.log.Warn("folder.list", "account", acc, "err", err)
				w.model.folderErr[acc] = err
				// Keep the last good list, if any, rather than emptying
				// the sidebar on a transient failure.
			} else {
				w.model.folders[acc] = res.Folders
				delete(w.model.folderErr, acc)
				w.trackOutbox(acc)
			}
			done()
		})
	}()
}

// rebuildFolderList recreates the sidebar rows from model.entries. One row
// per entry, in order: the row-selected handler in New maps
// row.Index() back onto model.entries. The selection is preserved when its
// folder still exists, otherwise the initial folder is selected.
//
// A folder hidden under a fold still exists: folding must not move the
// selection or reload the message list, so existence is decided against the
// model and not against the rows that happen to be on screen.
func (w *Window) rebuildFolderList() {
	w.model.rebuildEntries()

	// With several accounts a pinned "Inbox" says whose it is.
	several := len(w.model.enabledAccounts()) >= 2

	w.reselecting = true
	w.folderList.RemoveAll()
	w.folderRows = make(map[rowKey]*folderRow, len(w.model.entries))
	for _, e := range w.model.entries {
		if e.Header && e.Favourite {
			row, _ := newHeaderRow(i18n.T("Favourites"), false, false)
			w.folderList.Append(row)
			continue
		}
		if e.Header {
			row, twisty := newHeaderRow(accountLabel(e.Account), e.Collapsed, true)
			acc := e.Account.ID
			twisty.ConnectClicked(func() { w.toggleAccount(acc) })
			w.folderList.Append(row)
			continue
		}
		k := folderKey{Account: e.Account.ID, Folder: e.Folder.ID}
		subtitle := ""
		if e.Favourite && several {
			subtitle = accountLabel(e.Account)
		}
		r := newFolderRow(e, subtitle)
		if e.HasChildren && r.twisty != nil {
			r.twisty.ConnectClicked(func() { w.toggleFolder(k) })
		}
		if r.star != nil {
			r.star.ConnectClicked(func() { w.toggleFavourite(k) })
		}
		w.addFolderShortcuts(r, k, e)
		w.folderRows[rowKey{folderKey: k, fav: e.Favourite}] = r
		w.folderList.Append(r)
	}
	w.reselecting = false

	// Entries, not rows: an account folded shut leaves its header behind and
	// the sidebar is not empty.
	if len(w.model.entries) == 0 {
		w.showEmptySidebarStatus()
		// Nothing to show; a folder selected earlier is gone with its rows.
		if w.model.selected != (folderKey{}) {
			w.model.selected = folderKey{}
			w.loadMessages()
		}
		return
	}
	w.folderStack.SetVisibleChildName("folders")

	if w.model.folderListed(w.model.selected) {
		// Highlights the row when there is one, and does nothing beyond
		// that while the folder is folded out of sight.
		w.selectFolder(w.model.selected)
		return
	}
	if k, ok := w.model.initialFolder(); ok {
		w.selectFolder(k)
		return
	}
	// Only non-selectable containers: clear the list.
	w.model.selected = folderKey{}
	w.loadMessages()
}

// newHeaderRow builds a heading row with its fold arrow: an account, or the
// Favourites section, which does not fold (foldable false keeps the arrow's
// space so the headings line up, but nothing to click). The row cannot be
// selected or activated, so the row-selected handler never sees it; the
// arrow is a button and receives its clicks regardless.
func newHeaderRow(text string, collapsed, foldable bool) (*gtk.ListBoxRow, *gtk.Button) {
	row := gtk.NewListBoxRow()
	row.SetActivatable(false)
	row.SetSelectable(false)

	label := gtk.NewLabel(text)
	label.SetUseMarkup(false)
	label.SetXAlign(0)
	label.SetEllipsize(pango.EllipsizeEnd)
	label.SetHExpand(true)
	label.AddCSSClass("caption-heading")
	label.AddCSSClass("dim-label")

	twisty := newTwisty(collapsed, foldable)
	box := gtk.NewBox(gtk.OrientationHorizontal, 0)
	box.SetMarginTop(folderHeadingGap)
	box.Append(twisty)
	box.Append(label)
	row.SetChild(box)
	return row, twisty
}

// folderTitle is the display name of a folder: the localised name for a
// role folder (whatever the server calls it), the server's name otherwise.
func folderTitle(f api.Folder) string {
	switch f.Role {
	case api.RoleInbox:
		return i18n.C("folder", "Inbox")
	case api.RoleDrafts:
		return i18n.C("folder", "Drafts")
	case api.RoleSent:
		return i18n.C("folder", "Sent")
	case api.RoleTrash:
		return i18n.C("folder", "Trash")
	case api.RoleJunk:
		return i18n.C("folder", "Junk")
	case api.RoleArchive:
		return i18n.C("folder", "Archive")
	case api.RoleAll:
		return i18n.C("folder", "All Mail")
	case api.RoleOutbox:
		return i18n.C("folder", "Outbox")
	}
	return f.Name
}

// newFolderRow builds the row for one folder entry; subtitle, when given,
// goes under the name (the account of a pinned folder). The folder name and
// the account's are hostile input and are shown as plain text.
func newFolderRow(e folderEntry, subtitle string) *folderRow {
	row := adw.NewActionRow()
	row.AddCSSClass("folder-row")
	if e.Favourite {
		row.AddCSSClass("favourite")
	}
	row.SetUseMarkup(false)
	row.SetTitle(folderTitle(e.Folder))
	row.SetTitleLines(1)
	if subtitle != "" {
		row.SetSubtitle(subtitle)
		row.SetSubtitleLines(1)
	}
	row.SetTooltipText(e.Folder.Path)
	row.SetMarginStart(folderIndent * e.Depth)
	row.AddPrefix(gtk.NewImageFromIconName(roleIcon(e.Folder.Role)))

	// AddPrefix prepends, so the arrow goes in after the icon to end up left
	// of it. Accounts without any nesting get no arrow column at all.
	var twisty *gtk.Button
	if e.Nested {
		twisty = newTwisty(e.Collapsed, e.HasChildren)
		row.AddPrefix(twisty)
	}

	badge := gtk.NewLabel("")
	badge.SetUseMarkup(false)
	badge.AddCSSClass("caption")
	badge.AddCSSClass("numeric")
	badge.AddCSSClass("dim-label")
	badge.SetVAlign(gtk.AlignCenter)
	row.AddSuffix(badge)

	// AddSuffix appends, so the star ends up at the far right, after the
	// badge. A container that cannot be opened cannot be pinned either.
	var star *gtk.Button
	if e.Folder.Selectable {
		star = newStar(e.Starred)
		row.AddSuffix(star)
	}

	if !e.Folder.Selectable {
		row.SetSelectable(false)
		row.SetActivatable(false)
		row.AddCSSClass("dim-label")
	}
	r := &folderRow{ActionRow: row, badge: badge, twisty: twisty, star: star}
	r.setUnread(e.Badge)
	return r
}

// showEmptySidebarStatus explains an empty sidebar: no account, none
// enabled, an error, or accounts that have not synchronised yet.
func (w *Window) showEmptySidebarStatus() {
	enabled := w.model.enabledAccounts()
	switch {
	case !w.hasAccounts:
		w.showFolderStatus("system-users-symbolic", i18n.T("No Accounts"),
			i18n.T("Add a mail account in Preferences to see its folders here."))
	case len(enabled) == 0:
		w.showFolderStatus("system-users-symbolic", i18n.T("No Enabled Accounts"),
			i18n.T("Enable an account in Preferences to see its folders here."))
	default:
		for _, a := range enabled {
			if err := w.model.folderErr[a.ID]; err != nil {
				w.showFolderStatus("dialog-warning-symbolic", i18n.T("Folders Unavailable"),
					widget.RPCErrorText(i18n.T("Loading folders"), err))
				return
			}
		}
		w.showFolderStatus("folder-symbolic", i18n.T("No Folders Yet"),
			i18n.T("Folders appear after the first synchronisation."))
	}
}

// showFolderStatus switches the sidebar to the status page.
func (w *Window) showFolderStatus(icon, title, description string) {
	setStatusPage(w.folderStatusPage, icon, title, description)
	w.folderStack.SetVisibleChildName("status")
}

// setStatusPage fills a status page. The title is plain text; the
// description is Pango markup by contract, so it is escaped: error texts
// carry a technical detail from the backend and nothing shown to the user
// is interpreted as markup on principle.
func setStatusPage(p *adw.StatusPage, icon, title, description string) {
	p.SetIconName(icon)
	p.SetTitle(title)
	p.SetDescription(glib.MarkupEscapeText(description))
}

// selectFolder makes k the current folder: highlights its row, updates the
// list page title and loads its messages. It is idempotent for the already
// selected folder (the sidebar's row-selected handler and rebuildFolderList
// both call it), so a re-entry only re-highlights the row.
func (w *Window) selectFolder(k folderKey) {
	if k == w.model.selected && k == w.model.listFolder {
		w.highlightFolderRow(k)
		return
	}
	w.model.selected = k
	if f, ok := w.model.folder(k); ok {
		w.listPage.SetTitle(folderTitle(f))
	} else {
		w.listPage.SetTitle(i18n.T("Messages"))
	}
	w.highlightFolderRow(k)
	w.loadMessages()
}

// highlightFolderRow selects k's sidebar row without re-entering the
// row-selected handler (w.reselecting) and without navigating a collapsed
// split view to the list. A pinned folder has two rows: the one the user
// clicked last is preferred, the other one stands in when it is folded away
// or was just unpinned.
func (w *Window) highlightFolderRow(k folderKey) {
	r := w.folderRows[rowKey{folderKey: k, fav: w.model.selectedFav}]
	if r == nil {
		r = w.folderRows[rowKey{folderKey: k, fav: !w.model.selectedFav}]
	}
	if r == nil {
		return
	}
	if sel := w.folderList.SelectedRow(); sel != nil && sel.Index() == r.Index() {
		return
	}
	w.reselecting = true
	w.folderList.SelectRow(&r.ListBoxRow)
	w.reselecting = false
}

// updateFolderRow refreshes the unread badges after the count of k moved.
// Every row is refreshed, not just k's: a collapsed ancestor's badge counts
// the folders it hides, k itself may be one of them, and a pinned folder
// has a second row in the Favourites section.
func (w *Window) updateFolderRow(k folderKey) {
	for _, e := range w.model.entries {
		if e.Header {
			continue
		}
		key := rowKey{folderKey: folderKey{Account: e.Account.ID, Folder: e.Folder.ID}, fav: e.Favourite}
		if r := w.folderRows[key]; r != nil {
			r.setUnread(e.Badge)
		}
	}
}

// onSyncFinished runs when an account leaves the syncing state: folders
// are reloaded and, when the synced folder is the selected one (or the
// whole account was synced), the list. Called by applySyncState (sync.go).
func (w *Window) onSyncFinished(prev, cur api.SyncState) {
	if prev.Status != api.SyncSyncing || cur.Status == api.SyncSyncing {
		return
	}
	w.loadFolders(cur.AccountID, w.model.foldersGen)
	sel := w.model.selected
	if sel.Account == cur.AccountID && (cur.FolderID == "" || cur.FolderID == sel.Folder) {
		w.loadMessages()
	}
}

// onOutboxChanged runs when the account's number of pending outgoing
// messages moved (a send was queued, delivered or failed): the folders are
// reloaded, so the outbox row appears or goes with its contents, and the
// views showing the outbox are refreshed (outbox.go). Called by
// applySyncState (sync.go). When the selected folder was the outbox and it
// emptied, rebuildFolderList falls back to the initial folder.
func (w *Window) onOutboxChanged(acc api.AccountID) {
	w.fetchFolders(acc, w.model.foldersGen, func() {
		w.rebuildFolderList()
		w.refreshOutboxViews(acc)
	})
}

// onNewMessage inserts a notified message at the top of the list when it
// belongs to the selected folder, and adjusts the folder's unread count.
// Called by handleNotification (notify.go).
func (w *Window) onNewMessage(n api.NewMessageNotification) {
	k := folderKey{Account: n.AccountID, Folder: n.FolderID}
	s := n.Message
	if s.AccountID == "" {
		s.AccountID = n.AccountID
	}
	if s.FolderID == "" {
		s.FolderID = n.FolderID
	}
	if k == w.model.listFolder {
		if _, _, known := w.model.message(s.ID); known {
			return // delivered twice; the badge was adjusted the first time
		}
		// While the page is (re)loading the reply will include the message;
		// only a settled list gets the row prepended, and only when the
		// active filter would have listed it anyway.
		if !w.model.loading && w.model.listErr == nil && matchesFilter(s, w.model.listFilter) &&
			w.model.insertMessage(0, s) {
			r := w.newMessageRow(s)
			w.rows[s.ID] = r
			w.messageList.Prepend(r)
			w.showListState()
		}
	}
	if !hasFlag(s.Flags, api.FlagSeen) {
		w.model.adjustUnread(k, 1)
		w.updateFolderRow(k)
	}
}
