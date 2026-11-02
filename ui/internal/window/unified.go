// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/undo"
	"github.com/schotek/malachi/ui/internal/unified"
)

// The All Inboxes item (docs/plans/unified-inbox.md, decisions U1–U4): a
// row at the very top of the sidebar whose list is the inboxes of every
// enabled mail account in one (message.list / thread.list "unified"). The
// rules are ui/internal/unified's; this file is the window's side of them.
//
// The item is a folderEntry with Unified set and a synthetic Folder (role
// inbox, key unified.KeyFolder, the counts of the covered inboxes), which
// mailModel.folder returns for the key too, so the title, the counts, the
// date groups and the idle actions follow without a case of their own. The
// key is never stored in favourite-folders or collapsed-folders.

// isUnifiedKey says whether k is the All Inboxes item's key.
func isUnifiedKey(k folderKey) bool { return unified.IsKey(k.Account, k.Folder) }

// unifiedShown says whether the sidebar has the item (unified.Shown).
func (m *mailModel) unifiedShown() bool { return unified.Shown(m.accounts, m.unifiedOn) }

// unifiedInboxes are the folders the item covers (unified.Inboxes).
func (m *mailModel) unifiedInboxes() []api.Folder { return unified.Inboxes(m.accounts, m.folders) }

// unifiedFolder is the item's pseudo folder: the sum of the covered
// inboxes' counts under the item's key.
func (m *mailModel) unifiedFolder() api.Folder {
	in := m.unifiedInboxes()
	return api.Folder{
		ID: unified.KeyFolder, Name: unified.Title(i18n.Tr),
		Role: api.RoleInbox, Selectable: true, Synced: true,
		Unread: unified.Unread(in), Total: unified.Total(in),
	}
}

// unifiedEntry is the item's sidebar row.
func (m *mailModel) unifiedEntry() folderEntry {
	f := m.unifiedFolder()
	return folderEntry{Unified: true, Folder: f, Badge: f.Unread}
}

// selectedUnified says whether the item is the selected sidebar row.
func (m *mailModel) selectedUnified() bool { return isUnifiedKey(m.selected) }

// listIsUnified says whether the list shows the item (a search shows its
// results, not the item: listFolder is empty then).
func (m *mailModel) listIsUnified() bool { return isUnifiedKey(m.listFolder) }

// listCovers says whether a change in folder k (a new message, a sync, an
// undone move) belongs to the list shown: the folder itself, or an inbox
// the item covers.
func (m *mailModel) listCovers(k folderKey) bool {
	if k == m.listFolder {
		return true
	}
	return unified.Covers(m.listIsUnified(), k.Account, k.Folder, m.unifiedInboxes())
}

// messageListParams is message.list of folder k, first page (cursor "")
// or the page after cursor.
func (m *mailModel) messageListParams(k folderKey, cursor string) api.MessageListParams {
	if isUnifiedKey(k) {
		return unified.MessageParams(m.listFilter, cursor)
	}
	return api.MessageListParams{
		AccountID: k.Account,
		FolderID:  k.Folder,
		Page:      api.Page{Limit: api.DefaultPageLimit, Cursor: cursor},
		Sort:      api.SortDateDesc,
		Filter:    m.listFilter,
	}
}

// threadListParams is thread.list of folder k, as messageListParams.
func (m *mailModel) threadListParams(k folderKey, cursor string) api.ThreadListParams {
	if isUnifiedKey(k) {
		return unified.ThreadParams(m.listFilter, cursor)
	}
	return api.ThreadListParams{
		AccountID: k.Account,
		FolderID:  k.Folder,
		Page:      api.Page{Limit: api.DefaultPageLimit, Cursor: cursor},
		Sort:      api.SortDateDesc,
		Filter:    m.listFilter,
	}
}

// membersFolder is the folder thread.get reads conversation tid in: the
// listed folder, and in the All Inboxes list the row's own account and
// the folder of its latest member (the conversation's inbox).
func (m *mailModel) membersFolder(tid api.ThreadID) folderKey {
	if m.listIsUnified() {
		if i, ok := m.tindex[tid]; ok && i < len(m.threads) {
			t := m.threads[i]
			return folderKey{Account: t.AccountID, Folder: t.Latest.FolderID}
		}
	}
	return m.listFolder
}

// unifiedOrigin is the origin label of a row in the All Inboxes list: the
// name of its account. Empty in any other list.
func (m *mailModel) unifiedOrigin(s api.MessageSummary) (label, tooltip string) {
	if !m.listIsUnified() {
		return "", ""
	}
	a, ok := m.account(s.AccountID)
	if !ok {
		return "", ""
	}
	return accountLabel(a), a.Config.Email
}

// messageAccount is the account of a message summary (for GroupByAccount).
func messageAccount(s api.MessageSummary) api.AccountID { return s.AccountID }

// accountGroups splits summaries by account, one group per account
// (unified.GroupByAccount): an action on a selection of the item runs one
// call per group.
func accountGroups(list []api.MessageSummary) []unified.Group[api.MessageSummary] {
	return unified.GroupByAccount(list, messageAccount)
}

// crmSubjectsMixed: the subjects belong to more than one account.
func crmSubjectsMixed(subjects []crmSubject) bool {
	return unified.Mixed(subjects, func(x crmSubject) api.AccountID { return x.s.AccountID })
}

// splitByAccount runs fn once per account on the ids when they belong to
// more than one, and reports true; false means the ids are of one account
// (or unknown) and the caller does its single call as ever.
func (w *Window) splitByAccount(ids []api.MessageID, fn func([]api.MessageID)) bool {
	list := w.summaries(ids)
	if !unified.Mixed(list, messageAccount) {
		return false
	}
	for _, g := range accountGroups(list) {
		fn(idsOf(g.Items))
	}
	return true
}

// unifiedSettingChanged runs when the unified-inbox setting changed.
func (w *Window) unifiedSettingChanged() {
	if w.settings.UnifiedInbox() == w.model.unifiedOn {
		return
	}
	w.model.unifiedOn = w.settings.UnifiedInbox()
	w.rebuildFolderList()
}

// unifiedMessagesChanged is handleMessagesChanged while the item is
// selected: the list is read again when the change touches a covered
// inbox, and the user's replies a conversation shows when it names a sent
// folder. It reports whether the item is selected (and so handled it).
func (w *Window) unifiedMessagesChanged(n api.MessagesChangedNotification) bool {
	if !w.model.selectedUnified() {
		return false
	}
	if unified.CoversAny(true, n.AccountID, n.FolderIDs, w.model.unifiedInboxes()) {
		w.refreshShown(n.AccountID)
		w.loadMessages()
		return true
	}
	for _, id := range n.FolderIDs {
		if w.model.folderRole(folderKey{Account: n.AccountID, Folder: id}) == api.RoleSent {
			w.refreshShownSent(n.AccountID)
			break
		}
	}
	return true
}

// withdrawUnifiedNotifications withdraws the notifications of every
// covered inbox (withdrawViewedNotifications with the item selected).
func (w *Window) withdrawUnifiedNotifications() {
	for _, f := range w.model.unifiedInboxes() {
		w.withdraw(w.notified.removeFolder(folderKey{Account: f.AccountID, Folder: f.ID}))
	}
}

// unifiedSyncFinished is onSyncFinished while the item is selected: the
// list is read again when the pass touched a covered inbox (or the whole
// account, which has one). It reports whether the item is selected.
func (w *Window) unifiedSyncFinished(cur api.SyncState) bool {
	if !w.model.selectedUnified() {
		return false
	}
	if unified.CoversAny(true, cur.AccountID, folderIDsOf(cur.FolderID), w.model.unifiedInboxes()) {
		w.loadMessages()
	}
	return true
}

// folderIDsOf is the folders a sync state names: none for the whole
// account (CoversAny reads that as any folder of the account).
func folderIDsOf(id api.FolderID) []api.FolderID {
	if id == "" {
		return nil
	}
	return []api.FolderID{id}
}

// unifiedAfterUndo is afterUndo while the item is selected: the list is
// read again when the move touched a covered inbox, and the messages that
// came back to one are selected again (takeUndoReselect). It reports
// whether the item is selected.
func (w *Window) unifiedAfterUndo(e undo.Entry) bool {
	if !w.model.selectedUnified() {
		return false
	}
	if w.model.search.active {
		return true
	}
	inboxes := w.model.unifiedInboxes()
	var back []undo.Moved
	for _, m := range e.Moves {
		if unified.Covers(true, e.Account, m.FromFolderID, inboxes) {
			m.ThreadID = w.model.threadKey(e.Account, m.ThreadID) // the list's key
			back = append(back, m)
		}
	}
	if len(back) == 0 && !unified.Covers(true, e.Account, e.Target, inboxes) {
		return true
	}
	w.undoReselect = back
	w.loadMessages()
	return true
}

// threadKey is the key the model holds a conversation under: its thread
// id, and in the All Inboxes list the account's as well (account, NUL,
// id), because thread ids are unique only within an account (Graph's are
// the Exchange conversation id, which two mailboxes of a tenant share).
// listKey.Thread, tindex, members, memberOf and expanded all use it; the
// daemon and everything outside the list take rawThread.
func (m *mailModel) threadKey(account api.AccountID, id api.ThreadID) api.ThreadID {
	if id == "" || !m.listIsUnified() {
		return id
	}
	return api.ThreadID(string(account) + "\x00" + string(id))
}

// rawThread is the thread id the daemon knows for a model key.
func (m *mailModel) rawThread(key api.ThreadID) api.ThreadID {
	if !m.listIsUnified() {
		return key
	}
	if _, id, ok := strings.Cut(string(key), "\x00"); ok {
		return api.ThreadID(id)
	}
	return key
}

// keyThreads is list with each conversation's ID turned into its model key.
func (m *mailModel) keyThreads(list []api.ThreadSummary) []api.ThreadSummary {
	if !m.listIsUnified() {
		return list
	}
	out := make([]api.ThreadSummary, len(list))
	for i, t := range list {
		t.ID = m.threadKey(t.AccountID, t.ID)
		out[i] = t
	}
	return out
}

// nextUndoGroup is the group id for the entries of one action that is
// split into n per-account moves: a new one for several, 0 for one (the
// entry stands alone). Taking one of the group back takes all
// (runUndoGroup, undo.Stack.TakeGroup).
func (w *Window) nextUndoGroup(n int) int {
	if n < 2 {
		return 0
	}
	w.undoGroup++
	return w.undoGroup
}

// runUndoGroup takes e back with the other entries of its group.
func (w *Window) runUndoGroup(e undo.Entry) {
	for _, other := range w.undo.TakeGroup(e.Group) {
		w.runUndo(other)
	}
	w.runUndo(e)
}
