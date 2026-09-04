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

// folderRow is one selectable sidebar row with its unread badge.
type folderRow struct {
	*adw.ActionRow
	badge *gtk.Label
}

// setUnread shows n on the badge, hiding it at zero.
func (r *folderRow) setUnread(n int) {
	r.badge.SetVisible(n > 0)
	r.badge.SetText(strconv.Itoa(n))
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
func (w *Window) rebuildFolderList() {
	w.model.rebuildEntries()

	w.reselecting = true
	w.folderList.RemoveAll()
	w.folderRows = make(map[folderKey]*folderRow, len(w.model.entries))
	for _, e := range w.model.entries {
		if e.Header {
			w.folderList.Append(newHeaderRow(accountLabel(e.Account)))
			continue
		}
		r := newFolderRow(e)
		w.folderRows[folderKey{Account: e.Account.ID, Folder: e.Folder.ID}] = r
		w.folderList.Append(r)
	}
	w.reselecting = false

	if len(w.folderRows) == 0 {
		w.showEmptySidebarStatus()
		// Nothing to show; a folder selected earlier is gone with its rows.
		if w.model.selected != (folderKey{}) {
			w.model.selected = folderKey{}
			w.loadMessages()
		}
		return
	}
	w.folderStack.SetVisibleChildName("folders")

	if _, ok := w.folderRows[w.model.selected]; ok {
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

// newHeaderRow builds an account heading row. It cannot be selected or
// activated, so the row-selected handler never sees it.
func newHeaderRow(text string) *gtk.ListBoxRow {
	row := gtk.NewListBoxRow()
	row.SetActivatable(false)
	row.SetSelectable(false)
	label := gtk.NewLabel(text)
	label.SetUseMarkup(false)
	label.SetXAlign(0)
	label.SetEllipsize(pango.EllipsizeEnd)
	label.SetMarginTop(6)
	label.SetMarginBottom(0)
	label.SetMarginStart(6)
	label.AddCSSClass("caption-heading")
	label.AddCSSClass("dim-label")
	row.SetChild(label)
	return row
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

// newFolderRow builds the row for one folder entry. The folder name is
// hostile input and is shown as plain text.
func newFolderRow(e folderEntry) *folderRow {
	row := adw.NewActionRow()
	row.AddCSSClass("folder-row")
	row.SetUseMarkup(false)
	row.SetTitle(folderTitle(e.Folder))
	row.SetTitleLines(1)
	row.SetTooltipText(e.Folder.Path)
	row.SetMarginStart(folderIndent * e.Depth)
	row.AddPrefix(gtk.NewImageFromIconName(roleIcon(e.Folder.Role)))

	badge := gtk.NewLabel("")
	badge.SetUseMarkup(false)
	badge.AddCSSClass("caption")
	badge.AddCSSClass("numeric")
	badge.AddCSSClass("dim-label")
	badge.SetVAlign(gtk.AlignCenter)
	row.AddSuffix(badge)

	if !e.Folder.Selectable {
		row.SetSelectable(false)
		row.SetActivatable(false)
		row.AddCSSClass("dim-label")
	}
	r := &folderRow{ActionRow: row, badge: badge}
	r.setUnread(e.Folder.Unread)
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
// split view to the list.
func (w *Window) highlightFolderRow(k folderKey) {
	r := w.folderRows[k]
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

// updateFolderRow refreshes the unread badge of one sidebar row.
func (w *Window) updateFolderRow(k folderKey) {
	r := w.folderRows[k]
	if r == nil {
		return
	}
	if f, ok := w.model.folder(k); ok {
		r.setUnread(f.Unread)
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
		// only a settled list gets the row prepended.
		if !w.model.loading && w.model.listErr == nil && w.model.insertMessage(0, s) {
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
