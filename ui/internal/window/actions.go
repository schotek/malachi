// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"fmt"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/settings"
	"github.com/schotek/malachi/ui/internal/widget"
)

// Per-message actions (win.* in the main window, msg.* in a message
// window): optimistic change in the model and rows, message.flag /
// message.move / message.delete in the background, revert on failure.
// Every action takes a list of messages: one from a message window or a
// plain row, all the folder members from a conversation row of the
// grouped list. Log lines carry method names and errors only, never
// subjects or addresses.

// rpcTimeout bounds user-triggered calls to the daemon.
const rpcTimeout = 5 * time.Second

// confirmTrash asks before moving n messages to Trash when the setting is
// on, then runs proceed. subject is hostile input and is shown as plain
// text.
func confirmTrash(parent gtk.Widgetter, s *settings.Store, n int, subject string, proceed func()) {
	if !s.ConfirmDelete() {
		proceed()
		return
	}
	heading := i18n.T("Move to Trash?")
	if n > 1 {
		// TRANSLATORS: %d is the number of messages of a conversation.
		heading = fmt.Sprintf(i18n.N("Move %d message to Trash?", "Move %d messages to Trash?", n), n)
	}
	widget.ConfirmDestructive(parent, heading, subject, i18n.T("Move to _Trash"), proceed)
}

// flagChange is the message.flag set / clear pair that turns f on or off.
func flagChange(f api.Flag, on bool) (set, clear []api.Flag) {
	if on {
		return []api.Flag{f}, nil
	}
	return nil, []api.Flag{f}
}

// setStar shows the flagged state on a star toggle button.
func setStar(b *gtk.ToggleButton, on bool) {
	b.SetActive(on)
	if on {
		b.SetIconName("starred-symbolic")
		b.SetTooltipText(i18n.T("Unstar"))
	} else {
		b.SetIconName("non-starred-symbolic")
		b.SetTooltipText(i18n.T("Star"))
	}
}

// refreshRow pushes the model's summary of id to its list row, if listed.
// A row that the change makes stop matching the active list filter is
// updated in place rather than removed (see matchesFilter): reading a
// message under the unread filter must not pull it out from under the
// user. The filter is applied again on the next load of the list.
func (w *Window) refreshRow(id api.MessageID) {
	s, _, ok := w.model.message(id)
	if !ok {
		return
	}
	if r := w.rowFor(id); r != nil {
		r.SetMessage(summaryMessage(s))
	}
}

// refreshRows pushes the model to the rows of the given messages; in
// grouped mode the conversation rows carry aggregates, so the whole list
// is reconciled.
func (w *Window) refreshRows(ids []api.MessageID) {
	if w.model.grouped {
		w.syncRows()
		return
	}
	for _, id := range ids {
		w.refreshRow(id)
	}
}

// refreshStars shows the flagged state of id on every star button that
// displays it: the pane's when id is what the pane shows, and its message
// window's. A conversation row's star shows the union; refreshMessageActions
// sets that.
func (w *Window) refreshStars(id api.MessageID, on bool) {
	if s, ok := w.selectedMessage(); ok && s.ID == id {
		setStar(w.starButton, on)
	}
	if mw, ok := w.openMessages[id]; ok {
		setStar(mw.star, on)
	}
}

// refreshMessageActions re-evaluates the per-message actions for the
// selected row (after its flags changed).
func (w *Window) refreshMessageActions() {
	w.setMessageActionsSensitive(true)
}

// summaries is what the window knows of the given messages, leaving out
// the unknown ones.
func (w *Window) summaries(ids []api.MessageID) []api.MessageSummary {
	out := make([]api.MessageSummary, 0, len(ids))
	for _, id := range ids {
		if s, ok := w.summary(id); ok {
			out = append(out, s)
		}
	}
	return out
}

func idsOf(list []api.MessageSummary) []api.MessageID {
	out := make([]api.MessageID, 0, len(list))
	for _, s := range list {
		out = append(out, s.ID)
	}
	return out
}

// markRead sets the seen flag on message id.
func (w *Window) markRead(id api.MessageID) { w.setSeenIDs([]api.MessageID{id}, true) }

// markUnread clears the seen flag on message id.
func (w *Window) markUnread(id api.MessageID) { w.setSeenIDs([]api.MessageID{id}, false) }

// setSeenIDs changes the seen flag of the messages that do not have it so
// yet, optimistically (rows, unread badge of the folder, menu actions),
// and sends one message.flag; a failure puts everything back. The
// messages are of one folder (a conversation row's members are).
func (w *Window) setSeenIDs(ids []api.MessageID, seen bool) {
	var todo []api.MessageID
	var k folderKey
	for _, id := range ids {
		s, ok := w.summary(id)
		if !ok || hasFlag(s.Flags, api.FlagSeen) == seen || w.model.inOutbox(s) {
			continue // accelerators bypass the disabled actions
		}
		todo = append(todo, id)
		k = folderKey{Account: s.AccountID, Folder: s.FolderID}
	}
	if len(todo) == 0 {
		return
	}
	apply := func(on bool) {
		set, clear := flagChange(api.FlagSeen, on)
		changed := w.model.applyFlags(todo, set, clear)
		if len(changed) == 0 {
			return
		}
		w.refreshRows(changed)
		delta := len(changed) // marking unread raises the unread count
		if on {
			delta = -delta
		}
		w.model.adjustUnread(k, delta)
		w.updateFolderRow(k)
		w.refreshMessageActions()
	}
	apply(seen)

	n := len(todo)
	var what string
	switch {
	case seen && n == 1:
		what = i18n.T("Marking the message as read")
	case seen:
		what = fmt.Sprintf(i18n.N("Marking %d message as read", "Marking %d messages as read", n), n)
	case n == 1:
		what = i18n.T("Marking the message as unread")
	default:
		what = fmt.Sprintf(i18n.N("Marking %d message as unread", "Marking %d messages as unread", n), n)
	}
	set, clear := flagChange(api.FlagSeen, seen)
	w.call(what, api.MethodMessageFlag, api.MessageFlagParams{
		AccountID: k.Account, MessageIDs: todo, Set: set, Clear: clear,
	}, func() { apply(!seen) })
}

// toggleFlagged stars or unstars message id.
func (w *Window) toggleFlagged(id api.MessageID) {
	s, ok := w.summary(id)
	if !ok {
		return
	}
	w.setFlaggedIDs([]api.MessageID{id}, !hasFlag(s.Flags, api.FlagFlagged))
}

// setFlaggedIDs stars or unstars the messages that are not so yet, like
// setSeenIDs.
func (w *Window) setFlaggedIDs(ids []api.MessageID, on bool) {
	var todo []api.MessageID
	var acc api.AccountID
	for _, id := range ids {
		s, ok := w.summary(id)
		if !ok || hasFlag(s.Flags, api.FlagFlagged) == on || w.model.inOutbox(s) {
			continue // accelerators bypass the disabled actions
		}
		todo = append(todo, id)
		acc = s.AccountID
	}
	if len(todo) == 0 {
		return
	}
	apply := func(on bool) {
		set, clear := flagChange(api.FlagFlagged, on)
		changed := w.model.applyFlags(todo, set, clear)
		w.refreshRows(changed)
		for _, id := range changed {
			w.refreshStars(id, on)
		}
		w.refreshMessageActions()
	}
	apply(on)

	n := len(todo)
	var what string
	switch {
	case on && n == 1:
		what = i18n.T("Starring the message")
	case on:
		what = fmt.Sprintf(i18n.N("Starring %d message", "Starring %d messages", n), n)
	case n == 1:
		what = i18n.T("Removing the star")
	default:
		what = fmt.Sprintf(i18n.N("Removing the star from %d message", "Removing the star from %d messages", n), n)
	}
	set, clear := flagChange(api.FlagFlagged, on)
	w.call(what, api.MethodMessageFlag, api.MessageFlagParams{
		AccountID: acc, MessageIDs: todo, Set: set, Clear: clear,
	}, func() { apply(!on) })
}

// trash moves the selected message id to Trash (message.delete), after the
// optional confirmation shown over the main window.
func (w *Window) trash(id api.MessageID) { w.trashFrom(w, id) }

// trashFrom is trash with the confirmation shown over parent (a message
// window asks over itself).
func (w *Window) trashFrom(parent gtk.Widgetter, id api.MessageID) {
	s, ok := w.summary(id)
	if !ok {
		return
	}
	w.trashIDs(parent, []api.MessageID{id}, subjectText(s.Subject))
}

// trashIDs moves messages to Trash (message.delete) after the optional
// confirmation shown over parent, with subject as its body. For a single
// outbox message it cancels the send instead (outbox.go).
func (w *Window) trashIDs(parent gtk.Widgetter, ids []api.MessageID, subject string) {
	if len(ids) == 1 {
		if s, ok := w.summary(ids[0]); ok && w.model.inOutbox(s) {
			w.cancelSendFrom(parent, ids[0])
			return
		}
	}
	list := w.summaries(ids)
	if len(list) == 0 {
		return
	}
	confirmTrash(parent, w.settings, len(list), subject, func() {
		restore := w.removeRows(idsOf(list))
		for _, s := range list {
			w.closeMessageWindow(s.ID)
		}
		acc := list[0].AccountID
		// message.delete moves to the Trash role folder; a message already
		// there is expunged instead (no target badge to credit).
		var target folderKey
		if trash, ok := w.model.folderByRole(acc, api.RoleTrash); ok && trash.ID != list[0].FolderID {
			target = folderKey{Account: acc, Folder: trash.ID}
		}
		undo := w.trackMoves(list, target)
		n := len(list)
		what := i18n.T("Moving the message to Trash")
		if n > 1 {
			what = fmt.Sprintf(i18n.N("Moving %d message to Trash", "Moving %d messages to Trash", n), n)
		}
		w.call(what, api.MethodMessageDelete, api.MessageDeleteParams{
			AccountID: acc, MessageIDs: idsOf(list),
		}, func() {
			restore()
			undo()
		})
	})
}

// trackMoves adjusts the unread badges for messages leaving their folder
// for target (a zero target: leaving the store) and returns the reverse,
// for a failed move. Read messages change no badge.
func (w *Window) trackMoves(list []api.MessageSummary, target folderKey) (undo func()) {
	unread := 0
	for _, s := range list {
		if !hasFlag(s.Flags, api.FlagSeen) {
			unread++
		}
	}
	if unread == 0 || len(list) == 0 {
		return func() {}
	}
	src := folderKey{Account: list[0].AccountID, Folder: list[0].FolderID}
	shift := func(delta int) {
		w.model.adjustUnread(src, -delta)
		w.updateFolderRow(src)
		if target.Folder != "" {
			w.model.adjustUnread(target, delta)
			w.updateFolderRow(target)
		}
	}
	shift(unread)
	return func() { shift(-unread) }
}

// trackMove is trackMoves for one message.
func (w *Window) trackMove(s api.MessageSummary, target folderKey) (undo func()) {
	return w.trackMoves([]api.MessageSummary{s}, target)
}

// archive moves message id to the account's Archive folder.
func (w *Window) archive(id api.MessageID) { w.archiveIDs([]api.MessageID{id}) }

// archiveIDs moves messages to the account's Archive folder.
func (w *Window) archiveIDs(ids []api.MessageID) {
	w.moveIDsToRole(ids, api.RoleArchive, func(n int) string {
		if n == 1 {
			return i18n.T("Archiving the message")
		}
		return fmt.Sprintf(i18n.N("Archiving %d message", "Archiving %d messages", n), n)
	}, i18n.T("This account has no archive folder"))
}

// junk moves message id to the account's Junk folder after a confirmation
// shown over the main window.
func (w *Window) junk(id api.MessageID) { w.junkFrom(w, id) }

// junkFrom is junk with the confirmation shown over parent (a message
// window asks over itself).
func (w *Window) junkFrom(parent gtk.Widgetter, id api.MessageID) {
	s, ok := w.summary(id)
	if !ok {
		return
	}
	w.junkIDs(parent, []api.MessageID{id}, subjectText(s.Subject))
}

// junkIDs moves messages to the account's Junk folder after a confirmation
// shown over parent. Unlike Trash the question is always asked: the move
// feeds the server's spam filter and is not undone by moving back.
func (w *Window) junkIDs(parent gtk.Widgetter, ids []api.MessageID, subject string) {
	list := w.summaries(ids)
	if len(list) == 0 {
		return
	}
	if _, ok := w.model.folderByRole(list[0].AccountID, api.RoleJunk); !ok {
		w.Toast(i18n.T("This account has no junk folder"))
		return
	}
	heading := i18n.T("Mark as junk?")
	if n := len(list); n > 1 {
		// TRANSLATORS: %d is the number of messages of a conversation.
		heading = fmt.Sprintf(i18n.N("Mark %d message as junk?", "Mark %d messages as junk?", n), n)
	}
	widget.ConfirmDestructive(parent, heading, subject, i18n.T("Mark as _Junk"), func() {
		w.moveIDsToRole(idsOf(list), api.RoleJunk, func(n int) string {
			if n == 1 {
				return i18n.T("Marking the message as junk")
			}
			return fmt.Sprintf(i18n.N("Marking %d message as junk", "Marking %d messages as junk", n), n)
		}, i18n.T("This account has no junk folder"))
	})
}

// moveIDsToRole moves messages to their account's folder with the given
// role (message.move). what gives the progressive action for the error
// toast for the number moved, missing is the toast when the account has
// no such folder. The rows go at once and come back on failure; the
// unread badges follow unread messages from their folder to the target.
func (w *Window) moveIDsToRole(ids []api.MessageID, role api.FolderRole, what func(n int) string, missing string) {
	var list []api.MessageSummary
	for _, s := range w.summaries(ids) {
		if !w.model.inOutbox(s) {
			list = append(list, s) // accelerators bypass the disabled actions
		}
	}
	if len(list) == 0 {
		return
	}
	acc := list[0].AccountID
	target, ok := w.model.folderByRole(acc, role)
	if !ok {
		w.Toast(missing)
		return
	}
	if target.ID == list[0].FolderID {
		return
	}
	restore := w.removeRows(idsOf(list))
	for _, s := range list {
		w.closeMessageWindow(s.ID)
	}
	undo := w.trackMoves(list, folderKey{Account: acc, Folder: target.ID})
	w.call(what(len(list)), api.MethodMessageMove, api.MessageMoveParams{
		AccountID: acc, MessageIDs: idsOf(list), TargetFolderID: target.ID,
	}, func() {
		restore()
		undo()
	})
}

// scheduleMarkRead arms the mark-as-read timer for the newly selected
// message id, cancelling any pending one. An empty id only cancels.
func (w *Window) scheduleMarkRead(id api.MessageID) {
	if w.markReadSource != 0 {
		glib.SourceRemove(w.markReadSource)
		w.markReadSource = 0
	}
	w.markReadID = id
	s, _, ok := w.model.message(id)
	if id == "" || !ok || hasFlag(s.Flags, api.FlagSeen) {
		return
	}
	delay := w.settings.MarkReadDelay()
	if delay <= 0 {
		w.markRead(id)
		return
	}
	w.markReadSource = glib.TimeoutSecondsAdd(uint(delay), func() bool {
		w.markReadSource = 0
		if w.markReadID == id {
			w.markRead(id)
		}
		return false
	})
}

// setMessageActionsSensitive enables the per-message header buttons and
// win.* actions for the selected row: archive and junk only when the
// account has such a folder and the message is not in it already, mark
// read / unread according to the seen flag; the star button shows the
// flagged state. A conversation row is read when every member is, flagged
// when any is. An outbox message keeps only reply, forward and trash
// (which cancels the send; the daemon refuses flags and moves). With on
// false (or nothing selected) everything is off.
func (w *Window) setMessageActionsSensitive(on bool) {
	row, ok := w.selectedRow()
	on = on && ok
	s := row.Message
	outbox := on && w.model.inOutbox(s)
	flagged := on && hasFlag(s.Flags, api.FlagFlagged)
	seen := hasFlag(s.Flags, api.FlagSeen)
	unread := !seen
	if on && row.Thread {
		flagged = hasFlag(row.Summary.Flags, api.FlagFlagged)
		unread = row.Summary.UnreadCount > 0
		seen = row.Summary.UnreadCount < row.Summary.MessageCount
	}
	for _, b := range []*gtk.Button{w.replyButton, w.replyAllButton, w.forwardButton} {
		b.SetSensitive(on)
	}
	w.starButton.SetSensitive(on && !outbox)
	setStar(w.starButton, flagged)
	w.trashButton.SetTooltipText(trashTooltip(outbox))

	enabled := map[string]bool{
		"trash":        on,
		"archive":      on && !outbox && w.canMoveToRole(s, api.RoleArchive),
		"junk":         on && !outbox && w.canMoveToRole(s, api.RoleJunk),
		"mark-read":    on && !outbox && unread,
		"mark-unread":  on && !outbox && seen,
		"toggle-flag":  on && !outbox,
		"load-images":  on,
		"trust-sender": on && !outbox,
	}
	for name, e := range enabled {
		if a := w.actions[name]; a != nil {
			a.SetEnabled(e)
		}
	}
}

// canMoveToRole reports whether s can go to its account's role folder:
// the folder exists and s is not in it.
func (w *Window) canMoveToRole(s api.MessageSummary, role api.FolderRole) bool {
	f, ok := w.model.folderByRole(s.AccountID, role)
	return ok && f.ID != s.FolderID
}

// call runs one RPC in the background. what is the translated action in
// progressive form for the error toast (see widget.RPCErrorText); onErr
// runs on the main loop when the call failed, to revert an optimistic
// change. May be nil.
func (w *Window) call(what, method string, params any, onErr func()) {
	w.callThen(what, method, params, onErr, nil)
}

// callThen is call with a success callback, run on the main loop. Either
// callback may be nil.
func (w *Window) callThen(what, method string, params any, onErr, onOK func()) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		err := w.client.Call(ctx, method, params, nil)
		glib.IdleAdd(func() {
			if err == nil {
				if onOK != nil {
					onOK()
				}
				return
			}
			w.log.Warn(method, "err", err)
			w.Toast(widget.RPCErrorText(what, err))
			if onErr != nil {
				onErr()
			}
		})
	}()
}
