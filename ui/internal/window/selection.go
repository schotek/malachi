// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"slices"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/assistantpanel"
	"github.com/schotek/malachi/ui/internal/compose"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/selection"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The message list's selection of several rows (ui/internal/selection;
// macOS and Windows have the same). The ListBox selects several rows:
// Ctrl-click toggles, Shift-click and Shift+arrows extend, Ctrl+A selects
// every row shown, Escape lets go of several. One selected row behaves as
// before (the pane shows it, it is marked read, every action); two or
// more show their count in the pane, mark nothing read, and the actions
// work on all of them as selection.Combine says. The selection stays
// within one account (selection.Restrict). Programmatic changes go under
// w.reselecting and call onSelectionChanged themselves when the user's
// view must follow.

// selectionPageName is the message stack's page while several rows are
// selected.
const selectionPageName = "selection"

// setupSelection turns the list's multiple selection on and adds the
// pane's page for it; the window calls it once the list exists.
func (w *Window) setupSelection() {
	w.messageList.SetSelectionMode(gtk.SelectionMultiple)
	w.selectionPage = adw.NewStatusPage()
	w.selectionPage.SetIconName("edit-select-all-symbolic")
	w.messageStack.AddNamed(w.selectionPage, selectionPageName)
	w.messageList.ConnectSelectedRowsChanged(func() {
		if !w.reselecting {
			w.onSelectionChanged()
		}
	})
	// Escape lets go of several rows; with one (or none) it goes on to
	// the window.
	sc := gtk.NewShortcutController()
	sc.AddShortcut(gtk.NewShortcut(
		gtk.NewShortcutTriggerParseString("Escape"),
		gtk.NewCallbackAction(func(gtk.Widgetter, *glib.Variant) bool {
			if len(w.messageList.SelectedRows()) < 2 {
				return false
			}
			w.messageList.UnselectAll()
			return true
		}),
	))
	w.messageList.AddController(sc)
}

// keyIndex maps the list's row widgets to their keys, by object identity.
func (w *Window) keyIndex() map[uintptr]listKey {
	m := make(map[uintptr]listKey, len(w.rows))
	for k, r := range w.rows {
		m[coreglib.InternObject(r.ListBoxRow).Native()] = k
	}
	return m
}

// selectedKeys are the keys of the selected rows, in list order (date
// headings are never selected).
func (w *Window) selectedKeys() []listKey {
	sel := w.messageList.SelectedRows()
	if len(sel) == 0 {
		return nil
	}
	index := w.keyIndex()
	keys := make([]listKey, 0, len(sel))
	for _, row := range sel {
		if k, ok := index[coreglib.InternObject(row).Native()]; ok {
			keys = append(keys, k)
		}
	}
	return keys
}

// selectedRows are the model rows of the selection, in list order.
func (w *Window) selectedRows() []listRow {
	keys := w.selectedKeys()
	rows := make([]listRow, 0, len(keys))
	for _, k := range keys {
		if r, ok := w.model.rowAt(w.model.rowIndexOf(k)); ok {
			rows = append(rows, r)
		}
	}
	return rows
}

// selectedRow is the row behind the selection when it is one row; with
// several, the single-message features (the pane, the replies, the
// banners) have none.
func (w *Window) selectedRow() (listRow, bool) {
	rows := w.selectedRows()
	if len(rows) != 1 {
		return listRow{}, false
	}
	return rows[0], true
}

// selectOnly makes row the whole selection; the handler runs for it.
func (w *Window) selectOnly(row *gtk.ListBoxRow) {
	w.reselecting = true
	w.messageList.UnselectAll()
	w.reselecting = false
	w.messageList.SelectRow(row)
}

// selectKeys makes the rows of keys the selection, silently (the caller
// is under w.reselecting); keys without a row are passed over.
func (w *Window) selectKeys(keys []listKey) {
	w.messageList.UnselectAll()
	for _, k := range keys {
		if r := w.rows[k]; r != nil {
			w.messageList.SelectRow(r.ListBoxRow)
		}
	}
}

// onSelectionChanged follows the user's selection: none clears the pane,
// one shows it (onMessageRowSelected), several their count. A row of
// another account than the one just added leaves the selection first.
func (w *Window) onSelectionChanged() {
	keys := w.selectedKeys()
	if len(keys) > 1 {
		keys = w.restrictSelection(keys)
	}
	prev := w.lastSelected
	w.lastSelected = keys
	switch len(keys) {
	case 0:
		w.onMessageRowSelected(nil)
	case 1:
		w.onMessageRowSelected(w.rows[keys[0]].ListBoxRow)
	default:
		if len(prev) < 2 || !slices.Equal(prev, keys) {
			w.showSelectionPage(len(keys))
		}
	}
}

// restrictSelection keeps the selection within the account of the row the
// user added last (the focused one, else one not selected before).
func (w *Window) restrictSelection(keys []listKey) []listKey {
	rows := make([]selection.Row[listKey], 0, len(keys))
	for _, k := range keys {
		r, ok := w.model.rowAt(w.model.rowIndexOf(k))
		if !ok {
			continue
		}
		rows = append(rows, selection.Row[listKey]{Key: k, Account: r.Message.AccountID})
	}
	latest, found := keys[len(keys)-1], false
	if focus := w.messageList.FocusChild(); focus != nil {
		if k, ok := w.keyIndex()[coreglib.InternObject(focus).Native()]; ok && slices.Contains(keys, k) {
			latest, found = k, true
		}
	}
	for _, k := range keys {
		if found {
			break
		}
		if !slices.Contains(w.lastSelected, k) {
			latest, found = k, true
		}
	}
	kept := selection.Restrict(rows, latest)
	if len(kept) == len(keys) {
		return keys
	}
	w.reselecting = true
	w.selectKeys(kept)
	w.reselecting = false
	return kept
}

// showSelectionPage is the pane while n rows are selected: their count,
// no conversation, nothing marked read, the actions of the selection.
func (w *Window) showSelectionPage(n int) {
	if w.assistantPanel != nil {
		w.assistantPanel.followSelection()
	}
	w.hideConversation()
	w.paneQuoted.Clear()
	w.selectionPage.SetTitle(selection.SelectedText(i18n.Tr, n))
	w.messageStack.SetVisibleChildName(selectionPageName)
	w.outboxBanner.SetRevealed(false)
	w.draftBanner.SetRevealed(false)
	w.scheduleMarkRead("")
	w.setMessageActionsSensitive(true)
}

// selectionState is what the actions allow for the selection: the row's
// own state for one (messageActionState), selection.Combine of every
// row's for several, the account's offers and labels from the first.
func (w *Window) selectionState(on bool) actionState {
	rows := w.selectedRows()
	if !on || len(rows) == 0 {
		return w.model.messageActionState(listRow{}, false)
	}
	states := make([]actionState, len(rows))
	flags := make([]selection.Flags, len(rows))
	for i, r := range rows {
		states[i] = w.model.messageActionState(r, true)
		flags[i] = states[i].flags()
	}
	return states[0].with(selection.Combine(flags))
}

// selectedIDs runs fn with the selected rows and every message they stand
// for: one each, or all the folder members of a conversation row, fetched
// first when they are not known yet (the selection must still be those
// rows by then).
func (w *Window) selectedIDs(fn func([]listRow, []api.MessageID)) {
	rows := w.selectedRows()
	if len(rows) == 0 {
		return
	}
	var missing []api.ThreadID
	for _, r := range rows {
		if w.model.rowIDs(r) == nil {
			missing = append(missing, r.Key.Thread)
		}
	}
	keys := w.selectedKeys()
	run := func() {
		if !slices.Equal(w.selectedKeys(), keys) {
			return
		}
		now := w.selectedRows()
		var ids []api.MessageID
		for _, r := range now {
			got := w.model.rowIDs(r)
			if got == nil {
				return
			}
			ids = append(ids, got...)
		}
		fn(now, ids)
	}
	if len(missing) == 0 {
		run()
		return
	}
	left := len(missing)
	for _, tid := range missing {
		w.ensureMembers(tid, func() {
			if left--; left == 0 {
				run()
			}
		})
	}
}

// selectionSubject is the body of a confirmation for rows: the subject of
// one row, nothing for several (the heading counts them).
func selectionSubject(rows []listRow) string {
	if len(rows) == 1 {
		return rowSubject(rows[0])
	}
	return ""
}

// flagTargetOf is what the star does to rows: a row's own rule
// (mailModel.flagTarget), for several selection.FlagTarget.
func (w *Window) flagTargetOf(rows []listRow) bool {
	if len(rows) == 1 {
		return w.model.flagTarget(rows[0])
	}
	return selection.FlagTarget(w.selectionState(true).flags())
}

// openSelection is Open in New Window: the selected row's message (a
// draft in the compose window), or a window for each of several rows (the
// newest member of a conversation row), at most selection.MaxOpen.
func (w *Window) openSelection() {
	rows := w.selectedRows()
	if len(rows) > selection.MaxOpen {
		return // the action is off; an accelerator bypasses that
	}
	for _, r := range rows {
		w.openMessage(r.Message)
	}
}

// forwardSelection is Forward: the selected message as before
// (openCompose), or several rows in one new message, each message
// attached whole (forwardMessages), at most selection.MaxForward.
func (w *Window) forwardSelection() {
	rows := w.selectedRows()
	switch {
	case len(rows) == 1:
		w.openCompose(compose.KindForward, rows[0].Message.ID)
	case len(rows) > 1 && len(rows) <= selection.MaxForward:
		w.selectedIDs(func(_ []listRow, ids []api.MessageID) {
			if len(ids) > selection.MaxForward {
				ids = ids[:selection.MaxForward]
			}
			w.forwardMessages(ids)
		})
	}
}

// forwardMessages forwards several messages in one new message
// (draft.create forward with messageIds; the daemon attaches each whole):
// from their account, or from a mail account for an issue tracker's
// (forwardAccount), as one message is forwarded. A message the daemon
// could not attach (kept on the mail server only, over a cap) is counted
// in the compose window's note (compose.Params.Skipped). One request at a
// time.
func (w *Window) forwardMessages(ids []api.MessageID) {
	if len(ids) < 2 || w.forwardingMany {
		return
	}
	s, ok := w.summary(ids[0])
	if !ok {
		return
	}
	from := s.AccountID
	var messageAccount api.AccountID
	if own, known := w.model.account(s.AccountID); known && !own.Can(api.CapabilityCompose) {
		target, ok := w.forwardAccount()
		if !ok {
			return
		}
		from, messageAccount = target.ID, s.AccountID
	}
	params := api.DraftCreateParams{AccountID: from, Mode: api.ComposeForward, MessageIDs: ids, MessageAccountID: messageAccount}
	w.forwardingMany = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), composeTimeout)
		defer cancel()
		var res api.DraftCreateResult
		err := w.client.Call(ctx, api.MethodDraftCreate, params, &res)
		glib.IdleAdd(func() {
			w.forwardingMany = false
			if err != nil {
				w.log.Warn("draft.create", "mode", params.Mode, "messages", len(ids), "err", err)
				w.Toast(widget.RPCErrorText(composeWhat(compose.KindForward), err))
				return
			}
			p := compose.FromDraft(compose.KindForward, res.Draft, res.Blocked)
			p.AccountID = from
			p.Skipped = len(res.Skipped)
			w.compose.Open(p)
		})
	}()
}

// restoreSelection reselects prev's rows once the list changed under them
// (the caller set w.reselecting and rebuilt the rows; it is cleared
// here): each key still listed, a member's conversation row when the
// member folded away, and when every one went the row neighbour names
// (nil: none). The pane follows only when the selection changed.
func (w *Window) restoreSelection(prev []listKey, neighbour func() (listKey, bool)) {
	if len(prev) == 0 {
		w.reselecting = false
		return
	}
	keys := selection.Reconcile(prev, func(k listKey) (listKey, bool) {
		if w.rows[k] != nil {
			return k, true
		}
		if c := (listKey{Thread: k.Thread}); k.Thread != "" && w.rows[c] != nil {
			return c, true
		}
		return listKey{}, false
	}, neighbour)
	w.selectKeys(keys)
	w.reselecting = false
	if !slices.Equal(keys, prev) {
		w.onSelectionChanged()
	}
}

// queued are the outbox messages of list.
func (w *Window) queued(list []api.MessageSummary) []api.MessageSummary {
	var out []api.MessageSummary
	for _, s := range list {
		if w.model.inOutbox(s) {
			out = append(out, s)
		}
	}
	return out
}

// cancelSends is Trash on several queued messages: one question, then the
// sending of them all is cancelled (message.delete), as cancelSendFrom
// does for one.
func (w *Window) cancelSends(parent gtk.Widgetter, list []api.MessageSummary) {
	if len(list) == 1 {
		w.cancelSendFrom(parent, list[0].ID)
		return
	}
	widget.ConfirmDestructive(parent, selection.CancelSendingQuestion(i18n.Tr, len(list)), "", i18n.T("Do Not _Send"), func() {
		ids := idsOf(list)
		restore := w.removeRows(ids)
		for _, s := range list {
			w.closeMessageWindow(s.ID)
		}
		undo := w.trackMoves(list, folderKey{})
		acc := list[0].AccountID
		// Counted before the call, as cancelSendFrom does (trackOutbox).
		if w.outboxCancelled == nil {
			w.outboxCancelled = make(map[api.AccountID]int)
		}
		w.outboxCancelled[acc] += len(list)
		w.callThen(i18n.T("Cancelling the send"), api.MethodMessageDelete, api.MessageDeleteParams{
			AccountID: acc, MessageIDs: ids,
		}, func() {
			w.outboxCancelled[acc] = max(0, w.outboxCancelled[acc]-len(list))
			restore()
			undo()
		}, func() { w.onOutboxChanged(acc) })
	})
}

// newestFirstIDs orders the messages of several rows newest first, as the
// Assistant takes them (a conversation's are oldest first, newestFirst).
func (w *Window) newestFirstIDs(ids []api.MessageID) []string {
	sorted := slices.Clone(ids)
	slices.SortStableFunc(sorted, func(a, b api.MessageID) int {
		sa, _ := w.summary(a)
		sb, _ := w.summary(b)
		return sb.Date.Compare(sa.Date)
	})
	out := make([]string, len(sorted))
	for i, id := range sorted {
		out[i] = string(id)
	}
	return out
}

// severalContext is the Assistant panel's context for several selected
// rows (one account): every message they stand for, newest first; the
// newest message of a conversation whose members are not known yet stands
// for it until they are asked for (partial). nil when one is queued.
func (w *Window) severalContext(rows []listRow) *assistantpanel.Context {
	var ids []api.MessageID
	partial := false
	count := 0
	for _, r := range rows {
		if w.model.inOutbox(r.Message) {
			return nil
		}
		if got := w.model.rowIDs(r); got != nil {
			ids = append(ids, got...)
			count += len(got)
			continue
		}
		ids = append(ids, r.Message.ID)
		count += max(r.Summary.MessageCount, 1)
		partial = true
	}
	c := assistantpanel.NewContext(assistant.Selection{AccountID: string(rows[0].Message.AccountID), MessageIDs: w.newestFirstIDs(ids)},
		count, partial, selection.SelectedText(i18n.Tr, len(rows)), "")
	return &c
}
