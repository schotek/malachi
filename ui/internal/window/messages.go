// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"

	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/settings"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The message list: message.list paging, the rows and the list states.
// With "Group by Conversation" on the list is thread.list instead
// (threads.go, thread_model.go); the outbox folder is never grouped.

// loadMessages runs message.list (or thread.list) for the selected folder
// (first page). A switch to another folder, or of the grouping mode,
// empties the list at once; a reload of the same folder keeps the rows
// until the reply, so the list never flickers through the loading state
// and the selection survives (by key).
func (w *Window) loadMessages() {
	k := w.model.selected
	gen := w.model.bumpList()
	w.model.loadingMore = false
	grouped := w.settings.GroupByConversation() && w.model.folderRole(k) != api.RoleOutbox
	if k != w.model.listFolder || grouped != w.model.grouped {
		w.model.listFolder = k
		w.model.grouped = grouped
		w.model.clearMessages()
		w.rebuildMessageRows()
	}
	if k == (folderKey{}) {
		w.model.loading = false
		w.showListState()
		return
	}
	if f, ok := w.model.folder(k); ok && !f.Synced {
		// Never downloaded (Gmail's All Mail): nothing to ask for.
		w.model.loading = false
		w.showListState()
		return
	}
	w.model.loading = true
	w.model.listErr = nil
	w.showListState()
	if grouped {
		w.loadThreadPage(k, gen)
		return
	}

	params := api.MessageListParams{
		AccountID: k.Account,
		FolderID:  k.Folder,
		Page:      api.Page{Limit: api.DefaultPageLimit},
		Sort:      api.SortDateDesc,
		Filter:    w.model.listFilter,
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		var res api.MessageListResult
		err := w.client.Call(ctx, api.MethodMessageList, params, &res)
		glib.IdleAdd(func() {
			if gen != w.model.listGen {
				return
			}
			w.model.loading = false
			if err != nil {
				w.log.Warn("message.list", "folder", k.Folder, "err", err)
				if w.model.rowCount() > 0 {
					// A reload failed: keep what is shown, say so once.
					w.Toast(widget.RPCErrorText(i18n.T("Loading messages"), err))
					return
				}
				w.model.listErr = err
				w.showListState()
				return
			}
			w.model.setMessages(res.Messages, res.Page)
			w.rebuildMessageRows()
			w.showListState()
		})
	}()
}

// setListFilter switches the list between all, unread and flagged
// messages. The backend does the filtering, so the rows and the cursor
// from the previous filter are dropped and the folder is paged again from
// the start. Called from the toggle group, which also fires when Go sets
// the active name, hence the no-op on an unchanged filter.
func (w *Window) setListFilter(f api.MessageFilter) {
	if f == "" {
		f = api.FilterAll
	}
	if f == w.model.listFilter {
		return
	}
	w.model.listFilter = f
	w.model.clearMessages()
	w.rebuildMessageRows()
	w.loadMessages()
}

// loadMore fetches the next page (Load More button, scroll edge). It is a
// no-op while a page is in flight or when the last page is shown.
func (w *Window) loadMore() {
	m := &w.model
	if m.loading || m.loadingMore || m.nextCursor == "" || m.listErr != nil {
		return
	}
	k := m.listFolder
	gen := m.listGen
	m.loadingMore = true
	w.showLoadMore()
	if m.grouped {
		w.loadMoreThreads(k, gen)
		return
	}

	params := api.MessageListParams{
		AccountID: k.Account,
		FolderID:  k.Folder,
		Page:      api.Page{Cursor: m.nextCursor, Limit: api.DefaultPageLimit},
		Sort:      api.SortDateDesc,
		Filter:    w.model.listFilter,
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		var res api.MessageListResult
		err := w.client.Call(ctx, api.MethodMessageList, params, &res)
		glib.IdleAdd(func() {
			if gen != w.model.listGen {
				return
			}
			w.model.loadingMore = false
			if err != nil {
				w.log.Warn("message.list (more)", "folder", k.Folder, "err", err)
				w.Toast(widget.RPCErrorText(i18n.T("Loading more messages"), err))
				w.showLoadMore() // the cursor is still there; the button offers a retry
				return
			}
			added := w.model.appendMessages(res.Messages, res.Page)
			for _, s := range w.model.messages[len(w.model.messages)-added:] {
				r := w.newMessageRow(s)
				w.rows[listKey{Message: s.ID}] = r
				w.messageList.Append(r)
			}
			w.showListState()
		})
	}()
}

// rebuildMessageRows recreates the rows from the model. In flat mode one
// row per message, in order: the row handlers in New map row.Index() back
// onto model.messages. The selected message stays selected when it is
// still listed, without re-entering the row-selected handler (the pane
// already shows it); when it is gone the pane is cleared. In grouped mode
// the rows are keyed and syncRows does the same from scratch.
func (w *Window) rebuildMessageRows() {
	if w.model.grouped {
		w.reselecting = true
		w.messageList.RemoveAll()
		w.reselecting = false
		w.rows = make(map[listKey]*widget.MessageRow, w.model.rowCount())
		w.syncRows()
		return
	}
	prev, hadSelection := w.selectedMessage()

	w.reselecting = true
	w.messageList.RemoveAll()
	w.rows = make(map[listKey]*widget.MessageRow, len(w.model.messages))
	for _, s := range w.model.messages {
		r := w.newMessageRow(s)
		w.rows[listKey{Message: s.ID}] = r
		w.messageList.Append(r)
	}
	if hadSelection {
		if r := w.rows[listKey{Message: prev.ID}]; r != nil {
			w.messageList.SelectRow(r.ListBoxRow)
		} else {
			hadSelection = false
		}
	}
	w.reselecting = false

	if !hadSelection && prev.ID != "" {
		w.onMessageRowSelected(nil)
	}
}

// newMessageRow builds a row for s with the current appearance settings.
func (w *Window) newMessageRow(s api.MessageSummary) *widget.MessageRow {
	r := widget.NewMessageRow()
	r.SetMessage(summaryMessage(s))
	w.applyRowAppearance(r)
	return r
}

// showListState switches list_stack between the list and the status page
// (nothing selected, loading, no messages, error with retry).
func (w *Window) showListState() {
	m := &w.model
	if m.rowCount() > 0 {
		w.listStack.SetVisibleChildName("messages")
		w.showLoadMore()
		return
	}
	retry := false
	switch {
	case m.selected == (folderKey{}):
		setStatusPage(w.listStatusPage, "folder-symbolic", i18n.T("Select a folder"),
			i18n.T("Choose a folder in the sidebar to see its messages."))
	case w.folderUnsynced(m.selected):
		setStatusPage(w.listStatusPage, "folder-download-symbolic", i18n.T("Not Synchronised"),
			i18n.T("Messages moved here are archived on the server; the folder itself is not downloaded."))
	case m.listErr != nil:
		retry = true
		setStatusPage(w.listStatusPage, "dialog-warning-symbolic", i18n.T("Messages Unavailable"),
			widget.RPCErrorText(i18n.T("Loading messages"), m.listErr))
	case m.loading:
		setStatusPage(w.listStatusPage, "", i18n.T("Loading…"), "")
	case m.listFilter == api.FilterUnread:
		setStatusPage(w.listStatusPage, "mail-read-symbolic", i18n.T("No Unread Messages"),
			i18n.T("Everything in this folder has been read."))
	case m.listFilter == api.FilterFlagged:
		setStatusPage(w.listStatusPage, "starred-symbolic", i18n.T("No Flagged Messages"),
			i18n.T("No message in this folder carries a flag."))
	default:
		setStatusPage(w.listStatusPage, "mail-unread-symbolic", i18n.T("No Messages"),
			i18n.T("This folder is empty."))
	}
	w.listRetryButton.SetVisible(retry)
	w.listStack.SetVisibleChildName("status")
}

// folderUnsynced reports a selected folder the daemon never downloads.
func (w *Window) folderUnsynced(k folderKey) bool {
	f, ok := w.model.folder(k)
	return ok && !f.Synced
}

// showLoadMore shows the Load More button while a further page exists and
// the spinner while it is being fetched.
func (w *Window) showLoadMore() {
	m := &w.model
	w.loadMoreSpinner.SetVisible(m.loadingMore)
	w.loadMoreButton.SetVisible(!m.loadingMore && m.nextCursor != "" && m.listErr == nil)
}

// selectedMessage is the summary behind the selected row, if any: on a
// conversation row its newest folder member (what the pane shows).
func (w *Window) selectedMessage() (api.MessageSummary, bool) {
	r, ok := w.selectedRow()
	return r.Message, ok
}

// removeMessageRow drops a message from the flat list. When it was the
// selected one its neighbour is selected (the pane follows), or the pane
// is cleared when the list ran empty. The returned function puts the
// message back at its place, for reverting a failed move or delete; it
// does nothing once the list was reloaded in the meantime. Grouped mode
// goes through removeRows.
func (w *Window) removeMessageRow(id api.MessageID) (restore func()) {
	s, idx, ok := w.model.removeMessage(id)
	if !ok {
		return func() {}
	}
	gen := w.model.listGen
	wasSelected := false
	key := listKey{Message: id}
	if r := w.rows[key]; r != nil {
		if sel := w.messageList.SelectedRow(); sel != nil && sel.Index() == idx {
			wasSelected = true
		}
		delete(w.rows, key)
		// Removing the selected row emits row-selected(nil); skip it so the
		// pane does not blink through the empty page before the neighbour.
		w.reselecting = true
		w.messageList.Remove(r)
		w.reselecting = false
	}
	if wasSelected {
		next := idx
		if next > len(w.model.messages)-1 {
			next = len(w.model.messages) - 1
		}
		if row := w.messageList.RowAtIndex(next); next >= 0 && row != nil {
			w.messageList.SelectRow(row)
		} else {
			w.onMessageRowSelected(nil)
		}
	}
	w.showListState()

	return func() {
		if w.model.listGen != gen || !w.model.insertMessage(idx, s) {
			return
		}
		_, at, _ := w.model.message(s.ID)
		r := w.newMessageRow(s)
		w.rows[listKey{Message: s.ID}] = r
		w.messageList.Insert(r, at)
		w.showListState()
	}
}

// removeRows drops messages from the list in either mode and returns the
// function that puts them back after a failed move or delete. A grouped
// conversation whose members are not all known cannot be edited in place;
// the list is loaded again instead and the restore does nothing.
func (w *Window) removeRows(ids []api.MessageID) (restore func()) {
	if !w.model.grouped {
		restores := make([]func(), 0, len(ids))
		for _, id := range ids {
			restores = append(restores, w.removeMessageRow(id))
		}
		return func() {
			for i := len(restores) - 1; i >= 0; i-- {
				restores[i]()
			}
		}
	}
	r, ok := w.model.removeMessages(ids)
	if !ok {
		w.loadMessages()
		return func() {}
	}
	gen := w.model.listGen
	w.syncRowsAfterRemoval()
	return func() {
		if w.model.listGen != gen {
			return
		}
		w.model.restoreRemoval(r)
		w.syncRows()
	}
}

// applyListAppearance pushes the current list settings to every row.
func (w *Window) applyListAppearance() {
	for _, r := range w.rows {
		w.applyRowAppearance(r)
	}
}

// applyRowAppearance pushes the current list settings to one row.
func (w *Window) applyRowAppearance(r *widget.MessageRow) {
	r.SetCompact(w.settings.Density() == settings.DensityCompact)
	r.SetShowPreview(w.settings.ShowPreviewLine())
	r.SetShowAvatar(w.settings.ShowAvatars())
}
