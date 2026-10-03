// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The grouped message list on screen: thread.list pages, thread.get for an
// unfolded conversation, and syncRows, which reconciles the ListBox with
// model.rows by key instead of rebuilding it (a rebuild would drop the
// scroll position on every change). The model is thread_model.go.

// loadThreadPage runs thread.list for folder k (first page); loadMessages
// calls it in grouped mode. gen is the list generation the reply belongs
// to.
func (w *Window) loadThreadPage(k folderKey, gen uint64) {
	params := api.ThreadListParams{
		AccountID: k.Account,
		FolderID:  k.Folder,
		Page:      api.Page{Limit: api.DefaultPageLimit},
		Sort:      api.SortDateDesc,
		Filter:    w.model.listFilter,
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		var res api.ThreadListResult
		err := w.client.Call(ctx, api.MethodThreadList, params, &res)
		glib.IdleAdd(func() {
			if gen != w.model.listGen {
				return
			}
			w.model.loading = false
			if err != nil {
				w.log.Warn("thread.list", "folder", k.Folder, "err", err)
				if w.model.rowCount() > 0 {
					w.Toast(widget.RPCErrorText(i18n.T("Loading messages"), err))
					return
				}
				w.model.listErr = err
				w.showListState()
				return
			}
			w.model.setThreads(res.Threads, res.Page)
			w.syncRows()
			w.fetchExpandedMembers()
		})
	}()
}

// loadMoreThreads fetches the next page of conversations (loadMore in
// grouped mode).
func (w *Window) loadMoreThreads(k folderKey, gen uint64) {
	params := api.ThreadListParams{
		AccountID: k.Account,
		FolderID:  k.Folder,
		Page:      api.Page{Cursor: w.model.nextCursor, Limit: api.DefaultPageLimit},
		Sort:      api.SortDateDesc,
		Filter:    w.model.listFilter,
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		var res api.ThreadListResult
		err := w.client.Call(ctx, api.MethodThreadList, params, &res)
		glib.IdleAdd(func() {
			if gen != w.model.listGen {
				return
			}
			w.model.loadingMore = false
			if err != nil {
				w.log.Warn("thread.list (more)", "folder", k.Folder, "err", err)
				w.Toast(widget.RPCErrorText(i18n.T("Loading more messages"), err))
				w.showLoadMore()
				return
			}
			w.model.appendThreads(res.Threads, res.Page)
			w.syncRows()
		})
	}()
}

// toggleThread folds or unfolds a conversation row; unfolding asks for
// the members when they are not known yet.
func (w *Window) toggleThread(tid api.ThreadID) {
	on := !w.model.expanded[tid]
	w.model.setExpanded(tid, on)
	if on {
		w.ensureMembers(tid, nil)
	}
	w.syncRows()
}

// ensureMembers makes the folder members of a conversation known,
// through thread.get when needed, and runs then afterwards (at once when
// they are). A failed fetch folds the row back and says why. thread.get
// asks for the user's replies in Sent too (withSent), which the
// conversation view shows beside the members (threadMembers.sent).
func (w *Window) ensureMembers(tid api.ThreadID, then func()) {
	mem := w.model.members[tid]
	if mem == nil {
		return
	}
	if mem.complete {
		if then != nil {
			then()
		}
		return
	}
	if then != nil {
		mem.waiters = append(mem.waiters, then)
	}
	if mem.fetching {
		return
	}
	w.fetchMembers(tid, false)
}

// refetchMembers asks thread.get for conversation tid again while the
// model keeps what it holds of it, so nothing blinks: the user's replies
// in Sent changed (one arrived, or a message that may carry a reply's
// Message-ID arrived in the folder; the window cannot compare Message-IDs,
// the daemon does), or the shown conversation's sent folder changed. The
// answer replaces the members, the replies and the summary (SentCount, so
// a single message the user answered becomes a conversation row, and the
// selection follows it in syncRows) and reaches the conversation view
// through syncRows. A fetch in flight is followed by one more, since it
// may have been answered before the change. A failure is only logged,
// unless someone waits for the members. A port of the macOS client's
// ListController.refetchMembers.
func (w *Window) refetchMembers(tid api.ThreadID) {
	mem := w.model.members[tid]
	if mem == nil {
		return
	}
	if mem.fetching {
		mem.again = true
		return
	}
	w.fetchMembers(tid, true)
}

// fetchMembers is the thread.get of ensureMembers and refetchMembers;
// quiet: a failure nobody waits for is only logged.
func (w *Window) fetchMembers(tid api.ThreadID, quiet bool) {
	mem := w.model.members[tid]
	mem.fetching = true
	k := w.model.listFolder
	gen := w.model.listGen
	params := api.ThreadGetParams{AccountID: k.Account, ThreadID: tid, FolderID: k.Folder, WithSent: true}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		var res api.ThreadGetResult
		err := w.client.Call(ctx, api.MethodThreadGet, params, &res)
		glib.IdleAdd(func() {
			if gen != w.model.listGen {
				return
			}
			mem := w.model.members[tid]
			if mem == nil {
				return
			}
			mem.fetching = false
			waiters := mem.waiters
			mem.waiters = nil
			again := mem.again
			mem.again = false
			if err != nil {
				w.log.Warn("thread.get", "err", err)
				if quiet && len(waiters) == 0 {
					return
				}
				w.Toast(widget.RPCErrorText(i18n.T("Loading the conversation"), err))
				w.model.setExpanded(tid, false)
				w.syncRows()
				return
			}
			w.model.setMembers(tid, res.Thread, res.Messages, res.Sent)
			w.syncRows()
			if row, ok := w.selectedRow(); ok && row.Key.Thread == tid {
				w.setMessageActionsSensitive(true)
			}
			for _, fn := range waiters {
				fn()
			}
			if again {
				w.refetchMembers(tid)
			}
		})
	}()
}

// refetchReplies follows a message that arrived in the listed folder into
// conversation tid (onNewMessage, after applyNewMessage): a conversation
// that shows the user's replies is asked for again, since the arrival may
// be one of them (a Bcc to oneself), which only the daemon tells by its
// Message-ID.
func (w *Window) refetchReplies(tid api.ThreadID) {
	if tid == "" {
		return
	}
	replies := false
	if i, ok := w.model.tindex[tid]; ok && i < len(w.model.threads) {
		replies = w.model.threads[i].SentCount > 0
	}
	if mem := w.model.members[tid]; replies || (mem != nil && len(mem.sent) > 0) {
		w.refetchMembers(tid)
	}
}

// applyNewMessageElsewhere takes notify.newMessage for another folder of
// the listed folder's account (onNewMessage): a message in a sent folder
// that belongs to a listed conversation is the user's reply to it, so the
// conversation is asked for again (refetchMembers): its SentCount changes
// (a single message becomes a conversation row) and the conversation view,
// when it shows it, gets the reply. Nothing in flat mode, and nothing
// while a sent folder or the outbox is listed (their conversations have
// no replies of their own). s carries its account and folder.
func (w *Window) applyNewMessageElsewhere(s api.MessageSummary) {
	m := &w.model
	k := m.listFolder
	if !m.grouped || m.loading || k.Account != s.AccountID || k.Folder == s.FolderID || s.ThreadID == "" {
		return
	}
	if _, listed := m.tindex[s.ThreadID]; !listed {
		return
	}
	if m.folderRole(folderKey{Account: s.AccountID, Folder: s.FolderID}) != api.RoleSent {
		return
	}
	if role := m.folderRole(k); role == api.RoleSent || role == api.RoleOutbox {
		return
	}
	w.refetchMembers(s.ThreadID)
}

// refreshShownSent: notify.messagesChanged named a sent folder of account
// acc and not the selected folder (handleMessagesChanged): the user's
// replies the conversation on show holds are asked for again.
func (w *Window) refreshShownSent(acc api.AccountID) {
	if !w.model.grouped {
		return
	}
	row, ok := w.selectedRow()
	if !ok || row.Message.AccountID != acc || !rowShowsConversation(row) {
		return
	}
	w.refetchMembers(row.Key.Thread)
}

// fetchExpandedMembers asks for the members of every unfolded conversation
// that lost them (a reload that changed the conversation).
func (w *Window) fetchExpandedMembers() {
	for tid := range w.model.expanded {
		if mem := w.model.members[tid]; mem != nil && !mem.complete {
			w.ensureMembers(tid, nil)
		}
	}
}

// syncRows brings the ListBox in line with model.rows: rows whose key is
// gone are removed, new keys get a widget at their position, the rest are
// moved when needed and re-rendered. The selection follows its key; when
// a member row folded away its conversation row takes over (and the pane
// shows the newest member); when the selected row is gone the pane is
// cleared. Row signals from all this are suppressed (reselecting).
func (w *Window) syncRows() { w.reconcileRows(false) }

// syncRowsAfterRemoval is syncRows for a removal: a selected row that is
// gone hands the selection to the row now at its place, pane included,
// as the flat list does.
func (w *Window) syncRowsAfterRemoval() { w.reconcileRows(true) }

func (w *Window) reconcileRows(neighbour bool) {
	if w.usesDateGroups() {
		w.syncDateRows(neighbour)
		return
	}
	prev, hadSel := w.selectedKey()
	prevIdx := -1
	if hadSel {
		prevIdx = w.messageList.SelectedRow().Index()
	}
	w.reselecting = true
	for key, r := range w.rows {
		if _, ok := w.model.rowIdx[key]; !ok {
			w.messageList.Remove(r)
			delete(w.rows, key)
		}
	}
	for i, row := range w.model.rows {
		r := w.rows[row.Key]
		if r == nil {
			r = w.newRowFor(row)
			w.rows[row.Key] = r
			w.messageList.Insert(r, i)
			continue
		}
		if r.Index() != i {
			w.messageList.Remove(r)
			w.messageList.Insert(r, i)
		}
		w.renderRow(r, row)
	}
	selected := false
	if hadSel {
		if r := w.rows[prev]; r != nil {
			w.messageList.SelectRow(r.ListBoxRow)
			selected = true
		} else if r := w.rows[listKey{Thread: prev.Thread}]; prev.Thread != "" && r != nil {
			w.messageList.SelectRow(r.ListBoxRow)
			w.reselecting = false
			w.onMessageRowSelected(r.ListBoxRow)
			selected = true
		} else if next := min(prevIdx, w.model.rowCount()-1); neighbour && next >= 0 {
			if row := w.messageList.RowAtIndex(next); row != nil {
				w.messageList.SelectRow(row)
				w.reselecting = false
				w.onMessageRowSelected(row)
				selected = true
			}
		}
	}
	w.reselecting = false
	if hadSel && !selected {
		w.onMessageRowSelected(nil)
	}
	w.showListState()
	w.conversationListChanged()
}

// newRowFor builds the widget of a row with the current appearance and
// its handlers: the fold arrow of a conversation row, the Left/Right keys.
func (w *Window) newRowFor(row listRow) *widget.MessageRow {
	r := widget.NewMessageRow()
	w.applyRowAppearance(r)
	w.renderRow(r, row)
	tid := row.Key.Thread
	if row.Thread {
		r.ConnectExpander(func() { w.toggleThread(tid) })
	}
	w.addThreadShortcuts(r, tid, !row.Thread)
	return r
}

// renderRow pushes what a row stands for to its widget. A plain row keeps
// the arrow's place so the avatars of the grouped list line up.
func (w *Window) renderRow(r *widget.MessageRow, row listRow) {
	if row.Thread {
		r.SetThread(summaryThread(row.Summary, row.Expanded, row.Loading))
	} else {
		r.SetMessage(summaryMessage(row.Message))
		r.SetReserveExpander(true)
	}
	r.SetMember(row.Member)
}

// addThreadShortcuts gives a row of the grouped list the tree keys: Left
// folds the conversation (from its row or one of its members), Right
// unfolds it. Both return false when there is nothing to do, so the key
// still reaches the list for its normal navigation.
func (w *Window) addThreadShortcuts(r *widget.MessageRow, tid api.ThreadID, member bool) {
	sc := gtk.NewShortcutController()
	sc.SetScope(gtk.ShortcutScopeLocal)
	add := func(trigger string, fn func() bool) {
		sc.AddShortcut(gtk.NewShortcut(
			gtk.NewShortcutTriggerParseString(trigger),
			gtk.NewCallbackAction(func(gtk.Widgetter, *glib.Variant) bool { return fn() }),
		))
	}
	add("Left", func() bool {
		if !w.model.expanded[tid] {
			return false
		}
		w.toggleThread(tid)
		return true
	})
	if !member {
		add("Right", func() bool {
			if w.model.expanded[tid] {
				return false
			}
			w.toggleThread(tid)
			return true
		})
	}
	r.AddController(sc)
}

// selectedRow is the row behind the selection, if any.
func (w *Window) selectedRow() (listRow, bool) {
	row := w.messageList.SelectedRow()
	if row == nil {
		return listRow{}, false
	}
	return w.rowForWidget(row)
}

// selectedKey is the key of the selected row, if any, read off the row
// widgets rather than the model: reconcileRows needs it after the model
// has moved on, when the selected row's index no longer says which row it
// is (a removal above it, a conversation moved to the top).
func (w *Window) selectedKey() (listKey, bool) {
	sel := w.messageList.SelectedRow()
	if sel == nil {
		return listKey{}, false
	}
	idx := sel.Index()
	for key, r := range w.rows {
		if r.Index() == idx {
			return key, true
		}
	}
	return listKey{}, false
}

// rowFor is the widget showing message id on its own row, if listed.
func (w *Window) rowFor(id api.MessageID) *widget.MessageRow {
	return w.rows[w.model.keyFor(id)]
}

// selectedIDs runs fn with the selected row and every message it stands
// for: one, or all the folder members of a conversation row, fetched
// first when they are not known yet (the selection must still be that
// conversation by then).
func (w *Window) selectedIDs(fn func(listRow, []api.MessageID)) {
	row, ok := w.selectedRow()
	if !ok {
		return
	}
	if ids := w.model.rowIDs(row); ids != nil {
		fn(row, ids)
		return
	}
	tid := row.Key.Thread
	w.ensureMembers(tid, func() {
		if r, ok := w.selectedRow(); ok && r.Thread && r.Key.Thread == tid {
			if ids := w.model.rowIDs(r); ids != nil {
				fn(r, ids)
			}
		}
	})
}

// rowSubject is the subject a confirmation shows for a row: the
// conversation's, or the message's.
func rowSubject(row listRow) string {
	if row.Thread {
		return subjectText(row.Summary.Subject)
	}
	return subjectText(row.Message.Subject)
}
