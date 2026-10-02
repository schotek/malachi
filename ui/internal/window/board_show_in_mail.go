// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"errors"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// Show in Mail (the detail header's "…" menu, win.board-show-in-mail, and
// the case row's context menu): leaves Board for Mail, selects the case's
// current folder (message.get first — the case's own Detail.Reply.Folder
// can be stale, since the board last fetched its conversation before the
// message may have moved) and, once the folder's first page is listed,
// selects the message's row — its conversation's, in a grouped listing —
// exactly as a click in the list would. Anything that keeps the row from
// showing (the message gone, the folder unknown to the sidebar, a search
// on screen, a listing that failed or never finished, or the user going
// elsewhere before it did) opens the message in its own window instead
// (openMessageWindow), the fallback every other click already has.
//
// The GTK port of macOS's PendingReveal / ListController.reveal
// (MailboxController+Reveal.swift). Unlike Swift's, which hooks the exact
// RPC reply of loadMessages (messages.go, outside this agent's files),
// this polls the model's own state (listFolder, loading, search.active) a
// few times a second: simpler, and indistinguishable to the user from the
// instant a reply would otherwise fire on.

// revealPollInterval and revealTimeout bound the wait for the folder's
// first page: short enough that a row already there appears right after
// the click, bounded so a listing that never answers still falls back to
// the message's own window rather than wait forever.
const (
	revealPollInterval = 100 * time.Millisecond
	revealTimeout      = 10 * time.Second
)

// revealState is Show in Mail's pending request, the GTK port of macOS's
// PendingReveal; kept on boardPage (board.go) so leaving Mail
// (setMode) can drop it before it selects a row later than the user can
// see why.
type revealState struct {
	active   bool
	folder   folderKey
	message  api.MessageID
	thread   api.ThreadID
	deadline time.Time
	poll     glib.SourceHandle
}

// cancel drops a request still waiting (the window left Mail, or a fresh
// Show in Mail replaces it).
func (r *revealState) cancel() {
	r.active = false
	if r.poll != 0 {
		glib.SourceRemove(r.poll)
		r.poll = 0
	}
}

// revealOutcome is what a tick of the wait for a listing decides, pure and
// tested without GTK.
type revealOutcome int

const (
	// revealWaiting: the listing r asked for is still in flight.
	revealWaiting revealOutcome = iota
	// revealDropped: the user went elsewhere (another folder, a search)
	// before it answered; nothing more happens.
	revealDropped
	// revealReady: the listing for the right folder finished, or the wait
	// timed out: look for the row now, whatever is there.
	revealReady
)

// decideReveal is revealOutcome for a reveal waiting on reqFolder, given
// the model's state now: a search starting or another folder selected
// drops the request; a listing still in flight for the right folder keeps
// it waiting, under whatever generation that listing now is (a reload of
// the same folder before its first page restarts the wait, it does not
// drop the request — there is no generation to compare here, only the
// model's current, single state); the deadline passing ends the wait like
// an answer would, so a listing that never comes still falls back.
func decideReveal(reqFolder, curFolder folderKey, loading, searching, timedOut bool) revealOutcome {
	if searching || curFolder != reqFolder {
		return revealDropped
	}
	if loading && !timedOut {
		return revealWaiting
	}
	return revealReady
}

// revealRowKeys are the list row keys Show in Mail looks for, in order:
// the message's own row (flat mode, or an expanded conversation member),
// then its conversation's (collapsed, or a single-message conversation,
// shown as a plain row under the same key).
func revealRowKeys(msg api.MessageID, thread api.ThreadID) []listKey {
	keys := []listKey{{Thread: thread, Message: msg}}
	if thread != "" {
		keys = append(keys, listKey{Thread: thread})
	}
	return keys
}

// showInMail is win.board-show-in-mail and the row context menu's: d is
// the selected case. The samples never link a message (Detail.Reply is
// nil): a placeholder toast, like the board's other not-yet-real actions
// (board.Later).
func (p *boardPage) showInMail(d board.Detail) {
	w := p.w
	if d.Reply == nil {
		w.Toast(board.Later(i18n.Tr))
		return
	}
	acc, msg, thread, fallbackFolder := d.AccountID, d.Reply.Message, d.Thread, d.Reply.Folder
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		var res api.MessageGetResult
		err := w.client.Call(ctx, api.MethodMessageGet, api.MessageGetParams{AccountID: acc, MessageID: msg}, &res)
		glib.IdleAdd(func() {
			if err != nil {
				w.log.Warn("message.get (show in mail)", "err", err)
				if revealGone(err) {
					w.Toast(board.ShowInMailGone(i18n.Tr))
				} else {
					w.Toast(board.ShowInMailFailed(i18n.Tr))
				}
				return
			}
			folder := res.Message.FolderID
			if folder == "" {
				folder = fallbackFolder
			}
			p.reveal.start(w, folderKey{Account: acc, Folder: folder}, msg, thread)
		})
	}()
}

// revealGone reports message.get saying the message is gone outright
// (moved, deleted, its account removed): the toast names that, instead of
// the generic failure. The same two codes notificationOutdated (notify.go)
// reads for the same reason.
func revealGone(err error) bool {
	var e *api.Error
	return errors.As(err, &e) && (e.Code == api.CodeMessageNotFound || e.Code == api.CodeMessageGone)
}

// start begins waiting for folder k's first page, or opens msg in its own
// window at once when Mail cannot show it (a search on screen, or a
// folder the sidebar does not have).
func (r *revealState) start(w *Window, k folderKey, msg api.MessageID, thread api.ThreadID) {
	r.cancel()
	w.setMode(board.ModeMail)
	if w.model.search.active {
		w.openMessageWindow(msg)
		return
	}
	if _, ok := w.model.folder(k); !ok {
		w.openMessageWindow(msg)
		return
	}
	w.selectFolder(k)
	r.active = true
	r.folder, r.message, r.thread = k, msg, thread
	r.deadline = time.Now().Add(revealTimeout)
	if decideReveal(k, w.model.listFolder, w.model.loading, w.model.search.active, false) == revealReady {
		r.finish(w)
		return
	}
	r.poll = glib.TimeoutAdd(uint(revealPollInterval.Milliseconds()), func() bool { return r.tick(w) })
}

// tick is one poll: true keeps the timer, false stops it (the request
// ended, one way or another).
func (r *revealState) tick(w *Window) bool {
	if !r.active {
		return false
	}
	timedOut := !time.Now().Before(r.deadline)
	switch decideReveal(r.folder, w.model.listFolder, w.model.loading, w.model.search.active, timedOut) {
	case revealWaiting:
		return true
	case revealDropped:
		r.active = false
		r.poll = 0
		return false
	default: // revealReady
		r.finish(w)
		r.poll = 0
		return false
	}
}

// finish is the listing's answer: the row found, selected and given the
// keyboard (which scrolls it into view, as selectFirstResult does for
// search's first result), or the message's own window when nothing in the
// list is it.
func (r *revealState) finish(w *Window) {
	r.active = false
	for _, k := range revealRowKeys(r.message, r.thread) {
		if row, ok := w.rows[k]; ok {
			w.messageList.SelectRow(row.ListBoxRow)
			row.ListBoxRow.GrabFocus()
			w.innerSplit.SetShowContent(true)
			return
		}
	}
	w.openMessageWindow(r.message)
}
