// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"fmt"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The outbox: messages queued for sending live in the account's outbox
// folder, carry their delivery state in MessageSummary.Outbox and are
// counted by SyncState.PendingOutbox. The UI shows the state on a banner
// above the message, offers outbox.retry for a failed one and turns Trash
// into "cancel sending" (message.delete drops the message for good; the
// backend refuses flags and moves on outbox messages). The daemon owns the
// queue: nothing here decides when or whether a message goes out.

// trackOutbox runs after every folder.list of acc: a shrink of the outbox
// folder that the user did not cause by cancelling means messages were
// delivered and their copy filed in Sent (the daemon removes an outbox
// message only then, or right after delivery when the account has no Sent
// folder; a failed send keeps it). Those get a toast.
func (w *Window) trackOutbox(acc api.AccountID) {
	if w.outboxSeen == nil {
		w.outboxSeen = make(map[api.AccountID]int)
		w.outboxCancelled = make(map[api.AccountID]int)
	}
	total := 0
	if f, ok := w.model.folderByRole(acc, api.RoleOutbox); ok {
		total = f.Total
	}
	seen, known := w.outboxSeen[acc]
	w.outboxSeen[acc] = total
	if !known {
		return
	}
	sent := seen - total - w.outboxCancelled[acc]
	w.outboxCancelled[acc] = 0
	if sent > 0 {
		// TRANSLATORS: toast after delivery; %d is the number of messages.
		w.Toast(fmt.Sprintf(i18n.N("%d message sent", "%d messages sent", sent), sent))
	}
}

// outboxBannerText is the banner for a delivery state: the title, the
// button label ("" for no button) and whether the banner shows at all. A
// message that is not in the outbox, or already delivered, has none.
func outboxBannerText(o *api.OutboxInfo) (title, button string, shown bool) {
	if o == nil {
		return "", "", false
	}
	switch o.State {
	case api.OutboxQueued:
		return i18n.T("Queued for sending"), "", true
	case api.OutboxSending:
		return i18n.T("Sending…"), "", true
	case api.OutboxFailed:
		// A typed nil *api.Error must not become a non-nil error.
		var err error
		if o.Error != nil {
			err = o.Error
		}
		return widget.RPCErrorText(i18n.T("Sending the message"), err), i18n.T("Retry"), true
	}
	return "", "", false
}

// renderOutboxBanner shows the delivery state of m (nil while message.get
// has not answered) on b. The title may carry the backend's error message:
// plain text, no markup.
func renderOutboxBanner(b *adw.Banner, m *api.Message) {
	var o *api.OutboxInfo
	if m != nil {
		o = m.Outbox
	}
	title, button, shown := outboxBannerText(o)
	b.SetUseMarkup(false)
	b.SetTitle(title)
	b.SetButtonLabel(button)
	b.SetRevealed(shown)
}

// refreshOutboxViews brings every view of acc's outbox up to date after its
// contents changed: the list when the outbox is the listed folder, the pane
// when the selected message is in it and every message window showing one
// of its messages (each through a fresh message.get).
func (w *Window) refreshOutboxViews(acc api.AccountID) {
	outbox, ok := w.model.folderByRole(acc, api.RoleOutbox)
	if !ok {
		return
	}
	k := folderKey{Account: acc, Folder: outbox.ID}
	if w.model.listFolder == k {
		w.loadMessages()
	}
	if s, ok := w.selectedMessage(); ok && s.AccountID == acc && s.FolderID == outbox.ID {
		// The pane keeps its generation: showMessage owns the bump, this
		// only re-renders what it shows.
		gen := w.model.bodyGen
		w.refetchMessage(s.ID, func(lm *loadedMessage) {
			if gen != w.model.bodyGen {
				return
			}
			if sel, ok := w.selectedMessage(); !ok || sel.ID != s.ID {
				return // the reload dropped the message (delivered)
			}
			w.paneLabels().render(s, lm)
			renderOutboxBanner(w.outboxBanner, lm.msg)
		})
	}
	for id, mw := range w.openMessages {
		s, ok := w.summary(id)
		if !ok || s.AccountID != acc || s.FolderID != outbox.ID {
			continue
		}
		mw := mw
		w.refetchMessage(id, func(lm *loadedMessage) {
			if mw.closed {
				return
			}
			mw.show(s, lm)
		})
	}
}

// refetchMessage forgets the cached message.get result of id (unless one is
// in flight, which then serves) and fetches again; the body stays cached.
// done runs as fetchMessage's does.
func (w *Window) refetchMessage(id api.MessageID, done func(*loadedMessage)) {
	s, ok := w.summary(id)
	if !ok {
		return
	}
	if lm := w.loaded[id]; lm != nil && !lm.getting {
		lm.msg = nil
	}
	w.fetchMessage(s.AccountID, id, done)
}

// showOutboxState pushes the cached delivery state of id to the banners
// showing it: the pane's when id is selected, and its message window's.
func (w *Window) showOutboxState(id api.MessageID) {
	var m *api.Message
	if lm := w.loaded[id]; lm != nil {
		m = lm.msg
	}
	if s, ok := w.selectedMessage(); ok && s.ID == id {
		renderOutboxBanner(w.outboxBanner, m)
	}
	if mw, ok := w.openMessages[id]; ok {
		renderOutboxBanner(mw.banner, m)
	}
}

// retryOutbox re-queues the failed message id (outbox.retry). The banner
// shows "queued" at once; a refused retry fetches the real state back.
func (w *Window) retryOutbox(id api.MessageID) {
	s, ok := w.summary(id)
	if !ok {
		return
	}
	if lm := w.loaded[id]; lm != nil && lm.msg != nil && lm.msg.Outbox != nil {
		o := *lm.msg.Outbox
		o.State = api.OutboxQueued
		o.Error = nil
		lm.msg.Outbox = &o
	}
	if _, idx, ok := w.model.message(id); ok && w.model.messages[idx].Outbox != nil {
		o := *w.model.messages[idx].Outbox
		o.State = api.OutboxQueued
		o.Error = nil
		w.model.messages[idx].Outbox = &o
	}
	w.showOutboxState(id)
	w.call(i18n.T("Retrying the send"), api.MethodOutboxRetry, api.OutboxRetryParams{
		AccountID: s.AccountID, MessageID: id,
	}, func() {
		w.refetchMessage(id, func(*loadedMessage) { w.showOutboxState(id) })
	})
}

// cancelSendFrom asks (always: there is no Trash to get the message back
// from) and then removes the outbox message id for good with
// message.delete; the confirmation is shown over parent. The row goes at
// once and comes back when the daemon refuses.
func (w *Window) cancelSendFrom(parent gtk.Widgetter, id api.MessageID) {
	s, ok := w.summary(id)
	if !ok {
		return
	}
	// TRANSLATORS: %s is the subject of the message.
	body := fmt.Sprintf(i18n.T("“%s” will be removed from the outbox and not sent."), subjectText(s.Subject))
	widget.ConfirmDestructive(parent, i18n.T("Cancel sending this message?"), body, i18n.T("Do Not _Send"), func() {
		restore := w.removeMessageRow(id)
		w.closeMessageWindow(id)
		undo := w.trackMove(s, folderKey{})
		w.callThen(i18n.T("Cancelling the send"), api.MethodMessageDelete, api.MessageDeleteParams{
			AccountID: s.AccountID, MessageIDs: []api.MessageID{id},
		}, func() {
			restore()
			undo()
		}, func() {
			// Removing a failed message changes no pendingOutbox count, so
			// no notify.syncState follows: refresh the sidebar ourselves
			// (an empty outbox disappears). The drop is ours, not a delivery.
			if w.outboxCancelled == nil {
				w.outboxCancelled = make(map[api.AccountID]int)
			}
			w.outboxCancelled[s.AccountID]++
			w.onOutboxChanged(s.AccountID)
		})
	})
}

// trashTooltip is the trash button's tooltip: for an outbox message the
// button cancels the send instead.
func trashTooltip(outbox bool) string {
	if outbox {
		return i18n.T("Cancel Sending")
	}
	return i18n.T("Move to Trash")
}
