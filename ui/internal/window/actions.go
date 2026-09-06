// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
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
// Log lines carry method names and errors only, never subjects or
// addresses.

// rpcTimeout bounds user-triggered calls to the daemon.
const rpcTimeout = 5 * time.Second

// confirmTrash asks before moving a message to Trash when the setting is on,
// then runs proceed. subject is hostile input and is shown as plain text.
func confirmTrash(parent gtk.Widgetter, s *settings.Store, subject string, proceed func()) {
	if !s.ConfirmDelete() {
		proceed()
		return
	}
	widget.ConfirmDestructive(parent, i18n.T("Move to Trash?"), subject, i18n.T("Move to _Trash"), proceed)
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
	if r := w.rows[id]; r != nil {
		r.SetMessage(summaryMessage(s))
	}
}

// refreshStars shows the flagged state of id on every star button that
// displays it: the pane's when id is selected, and its message window's.
func (w *Window) refreshStars(id api.MessageID, on bool) {
	if s, ok := w.selectedMessage(); ok && s.ID == id {
		setStar(w.starButton, on)
	}
	if mw, ok := w.openMessages[id]; ok {
		setStar(mw.star, on)
	}
}

// refreshMessageActions re-evaluates the per-message actions for the
// selected message (after its flags changed).
func (w *Window) refreshMessageActions() {
	w.setMessageActionsSensitive(true)
}

// markRead sets the seen flag on message id.
func (w *Window) markRead(id api.MessageID) { w.setSeen(id, true) }

// markUnread clears the seen flag on message id.
func (w *Window) markUnread(id api.MessageID) { w.setSeen(id, false) }

// setSeen changes the seen flag of message id optimistically (row, unread
// badge of its folder, menu actions) and sends message.flag; a failure
// puts everything back.
func (w *Window) setSeen(id api.MessageID, seen bool) {
	s, ok := w.summary(id)
	if !ok || hasFlag(s.Flags, api.FlagSeen) == seen || w.model.inOutbox(s) {
		return // accelerators bypass the disabled actions
	}
	k := folderKey{Account: s.AccountID, Folder: s.FolderID}
	delta := 1 // unread count change when marking unread
	if seen {
		delta = -1
	}
	apply := func(on bool, delta int) {
		set, clear := flagChange(api.FlagSeen, on)
		if !w.model.updateFlags(id, set, clear) {
			return
		}
		w.refreshRow(id)
		w.model.adjustUnread(k, delta)
		w.updateFolderRow(k)
		w.refreshMessageActions()
	}
	apply(seen, delta)

	what := i18n.T("Marking the message as unread")
	if seen {
		what = i18n.T("Marking the message as read")
	}
	set, clear := flagChange(api.FlagSeen, seen)
	w.call(what, api.MethodMessageFlag, api.MessageFlagParams{
		AccountID: s.AccountID, MessageIDs: []api.MessageID{id}, Set: set, Clear: clear,
	}, func() { apply(!seen, -delta) })
}

// toggleFlagged stars or unstars message id.
func (w *Window) toggleFlagged(id api.MessageID) {
	s, ok := w.summary(id)
	if !ok || w.model.inOutbox(s) {
		return // accelerators bypass the disabled actions
	}
	on := !hasFlag(s.Flags, api.FlagFlagged)
	apply := func(on bool) {
		set, clear := flagChange(api.FlagFlagged, on)
		w.model.updateFlags(id, set, clear)
		w.refreshRow(id)
		w.refreshStars(id, on)
	}
	apply(on)

	what := i18n.T("Removing the star")
	if on {
		what = i18n.T("Starring the message")
	}
	set, clear := flagChange(api.FlagFlagged, on)
	w.call(what, api.MethodMessageFlag, api.MessageFlagParams{
		AccountID: s.AccountID, MessageIDs: []api.MessageID{id}, Set: set, Clear: clear,
	}, func() { apply(!on) })
}

// trash moves the selected message id to Trash (message.delete), after the
// optional confirmation shown over the main window.
func (w *Window) trash(id api.MessageID) { w.trashFrom(w, id) }

// trashFrom is trash with the confirmation shown over parent (a message
// window asks over itself). For an outbox message it cancels the send
// instead (outbox.go).
func (w *Window) trashFrom(parent gtk.Widgetter, id api.MessageID) {
	s, ok := w.summary(id)
	if !ok {
		return
	}
	if w.model.inOutbox(s) {
		w.cancelSendFrom(parent, id)
		return
	}
	confirmTrash(parent, w.settings, subjectText(s.Subject), func() {
		restore := w.removeMessageRow(id)
		w.closeMessageWindow(id)
		// message.delete moves to the Trash role folder; a message already
		// there is expunged instead (no target badge to credit).
		var target folderKey
		if trash, ok := w.model.folderByRole(s.AccountID, api.RoleTrash); ok && trash.ID != s.FolderID {
			target = folderKey{Account: s.AccountID, Folder: trash.ID}
		}
		undo := w.trackMove(s, target)
		w.call(i18n.T("Moving the message to Trash"), api.MethodMessageDelete, api.MessageDeleteParams{
			AccountID: s.AccountID, MessageIDs: []api.MessageID{id},
		}, func() {
			restore()
			undo()
		})
	})
}

// trackMove adjusts the unread badges for message s leaving its folder for
// target (a zero target: leaving the store) and returns the reverse, for a
// failed move. A read message changes no badge.
func (w *Window) trackMove(s api.MessageSummary, target folderKey) (undo func()) {
	if hasFlag(s.Flags, api.FlagSeen) {
		return func() {}
	}
	src := folderKey{Account: s.AccountID, Folder: s.FolderID}
	shift := func(delta int) {
		w.model.adjustUnread(src, -delta)
		w.updateFolderRow(src)
		if target.Folder != "" {
			w.model.adjustUnread(target, delta)
			w.updateFolderRow(target)
		}
	}
	shift(1)
	return func() { shift(-1) }
}

// archive moves message id to the account's Archive folder.
func (w *Window) archive(id api.MessageID) {
	w.moveToRole(id, api.RoleArchive, i18n.T("Archiving the message"), i18n.T("This account has no archive folder"))
}

// junk moves message id to the account's Junk folder after a confirmation
// shown over the main window.
func (w *Window) junk(id api.MessageID) { w.junkFrom(w, id) }

// junkFrom is junk with the confirmation shown over parent (a message
// window asks over itself). Unlike Trash the question is always asked: the
// move feeds the server's spam filter and is not undone by moving back.
func (w *Window) junkFrom(parent gtk.Widgetter, id api.MessageID) {
	s, ok := w.summary(id)
	if !ok {
		return
	}
	if _, ok := w.model.folderByRole(s.AccountID, api.RoleJunk); !ok {
		w.Toast(i18n.T("This account has no junk folder"))
		return
	}
	widget.ConfirmDestructive(parent, i18n.T("Mark as junk?"), subjectText(s.Subject), i18n.T("Mark as _Junk"), func() {
		w.moveToRole(id, api.RoleJunk, i18n.T("Marking the message as junk"), i18n.T("This account has no junk folder"))
	})
}

// moveToRole moves message id to the account's folder with the given role
// (message.move). what is the progressive action for the error toast,
// missing the toast when the account has no such folder. The row goes at
// once and comes back on failure; the unread badges follow an unread
// message from its folder to the target.
func (w *Window) moveToRole(id api.MessageID, role api.FolderRole, what, missing string) {
	s, ok := w.summary(id)
	if !ok || w.model.inOutbox(s) {
		return // accelerators bypass the disabled actions
	}
	target, ok := w.model.folderByRole(s.AccountID, role)
	if !ok {
		w.Toast(missing)
		return
	}
	if target.ID == s.FolderID {
		return
	}
	restore := w.removeMessageRow(id)
	w.closeMessageWindow(id)
	undo := w.trackMove(s, folderKey{Account: s.AccountID, Folder: target.ID})
	w.call(what, api.MethodMessageMove, api.MessageMoveParams{
		AccountID: s.AccountID, MessageIDs: []api.MessageID{id}, TargetFolderID: target.ID,
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
// win.* actions for the selected message: archive and junk only when the
// account has such a folder and the message is not in it already, mark
// read / unread according to the seen flag; the star button shows the
// flagged state. An outbox message keeps only reply, forward and trash
// (which cancels the send; the daemon refuses flags and moves). With on
// false (or nothing selected) everything is off.
func (w *Window) setMessageActionsSensitive(on bool) {
	s, ok := w.selectedMessage()
	on = on && ok
	outbox := on && w.model.inOutbox(s)
	for _, b := range []*gtk.Button{w.replyButton, w.replyAllButton, w.forwardButton} {
		b.SetSensitive(on)
	}
	w.starButton.SetSensitive(on && !outbox)
	setStar(w.starButton, on && hasFlag(s.Flags, api.FlagFlagged))
	w.trashButton.SetTooltipText(trashTooltip(outbox))

	seen := hasFlag(s.Flags, api.FlagSeen)
	enabled := map[string]bool{
		"trash":        on,
		"archive":      on && !outbox && w.canMoveToRole(s, api.RoleArchive),
		"junk":         on && !outbox && w.canMoveToRole(s, api.RoleJunk),
		"mark-read":    on && !outbox && !seen,
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
