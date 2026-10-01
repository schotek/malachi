// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/jira"
	"github.com/schotek/malachi/ui/internal/widget"
)

// notificationBodyMax caps the notification body; subjects are hostile input
// and notification servers do not need a novel.
const notificationBodyMax = 200

// handleNotification runs on the main loop for every server notification.
func (w *Window) handleNotification(method string, params json.RawMessage) {
	switch method {
	case api.NotifyNewMessage:
		var n api.NewMessageNotification
		if err := json.Unmarshal(params, &n); err != nil {
			w.log.Warn("bad notify.newMessage payload", "err", err)
			return
		}
		w.notifyNewMessage(n)
		w.onNewMessage(n)
	case api.NotifySyncState:
		var n api.SyncStateNotification
		if err := json.Unmarshal(params, &n); err != nil {
			w.log.Warn("bad notify.syncState payload", "err", err)
			return
		}
		w.applySyncState(n.State)
	case api.NotifyAuthRequired:
		var n api.AuthRequiredNotification
		if err := json.Unmarshal(params, &n); err != nil {
			w.log.Warn("bad notify.authRequired payload", "err", err)
			return
		}
		w.showAuthRequired(n)
	case api.NotifyMessagesChanged:
		var n api.MessagesChangedNotification
		if err := json.Unmarshal(params, &n); err != nil {
			w.log.Warn("bad notify.messagesChanged payload", "err", err)
			return
		}
		w.handleMessagesChanged(n)
	case api.NotifyAccountsChanged:
		// The account set changed under us: the banner's account may be
		// gone or edited; a still-failing account is announced again by
		// the daemon once its syncer restarts.
		w.hideAuthBanner()
		w.compose.Invalidate()
		w.loadAccounts()
	default:
		w.log.Info("notification", "method", method)
	}
}

// notifyNewMessage shows a desktop notification (and plays the sound) unless
// the user is looking at the main window right now, or has read the message
// elsewhere before it arrived here. The notification is remembered, so it
// can be withdrawn once it is outdated (withdrawNotifications).
func (w *Window) notifyNewMessage(n api.NewMessageNotification) {
	if w.IsActive() || hasFlag(n.Message.Flags, api.FlagSeen) {
		return
	}
	if w.settings.DesktopNotifications() {
		title, body := notificationText(n)
		note := gio.NewNotification(title)
		note.SetBody(body)
		note.SetIcon(gio.NewThemedIcon("mail-unread-symbolic"))
		// TODO(phase-1): target the message once the list selects by ID.
		note.SetDefaultAction("app.show")
		w.app.SendNotification(notificationID(n.Message.ID), note)
		w.withdraw(w.notified.add(n.Message.ID, folderKey{Account: n.AccountID, Folder: n.FolderID}))
	}
	if w.settings.NotificationSound() {
		w.playNewMailSound()
	}
}

// notificationText builds the plain-text title and body. GNotification
// bodies are not markup, but the text is still attacker-controlled: it is
// trimmed and capped. A message of an issue (a description or a comment of
// a Jira account) says which issue under its author: "KEY: summary" from
// MessageSummary.Issue (issueNotificationLine), not from the subject.
func notificationText(n api.NewMessageNotification) (title, body string) {
	title = i18n.T("New message")
	if len(n.Message.From) > 0 {
		if name := widget.DisplayName(n.Message.From[0]); name != "" {
			title = name
		}
	}
	if n.Message.Issue != nil {
		body = issueNotificationLine(n.Message.Issue)
	}
	if body == "" {
		body = strings.TrimSpace(n.Message.Subject)
	}
	if body == "" {
		body = i18n.T("(No subject)")
	}
	if len(body) > notificationBodyMax {
		body = strings.ToValidUTF8(body[:notificationBodyMax], "") + "…"
	}
	return title, body
}

// issueNotificationLine is the line of a notification that names an
// issue: its key and summary, "ITSD-42: The printer is on fire", or
// whichever of the two it has; "" when it has neither. Both are the site's
// texts, hostile like a subject: cleaned to one line without invisible
// characters (jira.Clean).
func issueNotificationLine(issue *api.MessageIssue) string {
	key, summary := jira.Clean(issue.Key), jira.Clean(issue.Summary)
	switch {
	case key == "":
		return summary
	case summary == "":
		return key
	}
	return key + ": " + summary
}

// handleMessagesChanged takes notify.messagesChanged: messages of the
// account's folders changed without arriving or leaving (docs/api.md §5).
// Either they were hidden or shown again (a Jira account hides the
// notification mails of its issues in a mail account, and shows them again
// when that is switched off), or a Jira account's own messages were
// rebuilt in place, keeping their ids (other rendering settings, a comment
// edited or re-attributed, an issue renamed). The account's folders are
// read again for their counts; the message cache lets go of the account's
// entries; and when the selected folder is one of those named — or any
// folder of the account when none is named, or a virtual folder of the
// account, which shows copies of every space's issues — what the pane
// shows of it is fetched again (refreshShown) and it is listed again. A
// sent folder named without the selected one has the conversation shown
// ask for the user's replies again (refreshShownSent).
func (w *Window) handleMessagesChanged(n api.MessagesChangedNotification) {
	a, ok := w.model.account(n.AccountID)
	if !ok || !a.Enabled {
		return
	}
	w.loadFolders(n.AccountID, w.model.foldersGen)
	evictAccount(w.loaded, n.AccountID)
	sel := w.model.selected
	if sel.Account != n.AccountID {
		return
	}
	f, known := w.model.folder(sel)
	if len(n.FolderIDs) > 0 && !slices.Contains(n.FolderIDs, sel.Folder) && !(known && f.Virtual != "") {
		// The user's replies a conversation shows live in Sent.
		if slices.ContainsFunc(n.FolderIDs, func(id api.FolderID) bool {
			return w.model.folderRole(folderKey{Account: n.AccountID, Folder: id}) == api.RoleSent
		}) {
			w.refreshShownSent(n.AccountID)
		}
		return
	}
	w.refreshShown(n.AccountID)
	w.loadMessages()
}

// evictAccount forgets the cache entries of the account's messages (the
// daemon rebuilt them in place), and those whose account is not known,
// which may be its.
func evictAccount(loaded map[api.MessageID]*loadedMessage, acc api.AccountID) {
	for id, lm := range loaded {
		if lm.account == "" || lm.account == acc {
			delete(loaded, id)
		}
	}
}

// refreshShown shows the selected row of account acc afresh, just before
// the folder is listed again (handleMessagesChanged): the cache let go of
// the account's messages, so what the pane shows is fetched again. A
// message row shows its message again; a conversation row's folder members
// are forgotten, so the reload's answer asks thread.get for them again
// (their senders and dates may have changed), and the conversation view
// drops the bodies it holds.
func (w *Window) refreshShown(acc api.AccountID) {
	row, ok := w.selectedRow()
	if !ok || row.Message.AccountID != acc {
		return
	}
	if w.conversationShown() {
		w.model.forgetMembers(row.Key.Thread)
		w.syncRows()
		w.conversationMessagesChanged(acc)
		return
	}
	w.showMessage(row.Message.ID)
}

// withdrawNotifications withdraws the desktop notifications of the given
// messages that the window sent and still remembers; the others are left
// alone. Called when this UI read, moved or trashed them and when the
// daemon says they are outdated (verifyNotifications).
func (w *Window) withdrawNotifications(ids []api.MessageID) {
	w.withdraw(w.notified.remove(ids))
}

// withdraw withdraws the notifications of messages the notified set has
// just let go of.
func (w *Window) withdraw(ids []api.MessageID) {
	for _, id := range ids {
		w.app.WithdrawNotification(notificationID(id))
	}
}

// withdrawViewedNotifications withdraws the notifications of the selected
// folder's messages while the main window is active (it became active, or
// the folder was selected in it): the folder's list shows those messages
// now, so their notifications have nothing left to announce. Notifications
// of other folders stay until their folder is viewed or their message is
// read, moved or deleted.
func (w *Window) withdrawViewedNotifications() {
	if !w.IsActive() || w.model.selected.Folder == "" {
		return
	}
	w.withdraw(w.notified.removeFolder(w.model.selected))
}

// withdrawAccountNotifications withdraws the notifications of accounts
// that are gone or paused (after account.list): their messages are shown
// nowhere, and no sync pass of theirs will check them.
func (w *Window) withdrawAccountNotifications() {
	keep := make(map[api.AccountID]bool)
	for _, a := range w.model.enabledAccounts() {
		keep[a.ID] = true
	}
	w.withdraw(w.notified.removeAccountsExcept(keep))
}

// verifyNotifications runs after a sync pass of account acc: it asks the
// daemon (message.get) about every message of the account whose
// notification may still show and withdraws the outdated ones
// (notificationOutdated) — read, moved or deleted on another device, by
// another client of the daemon or by a server rule. The pass is how such
// a change reaches the daemon, so its end is when to ask. One check per
// account runs at a time; a pass that ends meanwhile gets one more check
// once it is done. Log lines carry the method and error only.
func (w *Window) verifyNotifications(acc api.AccountID) {
	entries := w.notified.ofAccount(acc)
	if len(entries) == 0 {
		return
	}
	if _, running := w.verifying[acc]; running {
		w.verifying[acc] = true
		return
	}
	w.verifying[acc] = false
	go func() {
		var outdated []api.MessageID
		for _, e := range entries {
			var res api.MessageGetResult
			ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
			err := w.client.Call(ctx, api.MethodMessageGet, api.MessageGetParams{AccountID: acc, MessageID: e.id}, &res)
			cancel()
			stale, ok := notificationOutdated(e, res.Message.MessageSummary, err)
			if !ok {
				w.log.Debug("message.get", "err", err)
				break // the next pass asks again
			}
			if stale {
				outdated = append(outdated, e.id)
			}
		}
		glib.IdleAdd(func() {
			again := w.verifying[acc]
			delete(w.verifying, acc)
			w.withdrawNotifications(outdated)
			if again {
				w.verifyNotifications(acc)
			}
		})
	}()
}
