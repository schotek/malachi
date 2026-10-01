// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/client"
)

// OpenMessageAction is the application action a click on a new-message
// notification activates (the default action of notifyNewMessage), with
// NotificationTarget as its parameter.
const OpenMessageAction = "open-message"

// notificationTargetType is the parameter type of app.open-message: the
// account and the message.
const notificationTargetType = "(ss)"

// NotificationTargetType is the parameter type of app.open-message.
func NotificationTargetType() *glib.VariantType {
	return glib.NewVariantType(notificationTargetType)
}

// NotificationTarget is the parameter of app.open-message for message id of
// account acc.
func NotificationTarget(acc api.AccountID, id api.MessageID) *glib.Variant {
	return glib.NewVariantTuple([]*glib.Variant{
		glib.NewVariantString(string(acc)),
		glib.NewVariantString(string(id)),
	})
}

// ParseNotificationTarget reads the parameter of app.open-message. ok is
// false for anything but two non-empty strings: the action is public on
// the session bus, so the parameter is no more trusted than the click.
func ParseNotificationTarget(v *glib.Variant) (acc api.AccountID, id api.MessageID, ok bool) {
	if v == nil || !v.IsOfType(NotificationTargetType()) {
		return "", "", false
	}
	return notificationIDs(v.ChildValue(0).String(), v.ChildValue(1).String())
}

// notificationIDs are the ids of a notification's target, ok only when
// both are there.
func notificationIDs(acc, id string) (api.AccountID, api.MessageID, bool) {
	if acc == "" || id == "" {
		return "", "", false
	}
	return api.AccountID(acc), api.MessageID(id), true
}

// OpenNotifiedMessage opens message id of account acc, whose desktop
// notification the user clicked, in its own window (or raises the window
// that already shows it) and marks it read, as a double-click in the list
// does. The main window stays as it is: hidden when it runs in the
// background, behind the message window otherwise. The message need not be
// in any list the window shows; what the window does not know, message.get
// tells. A message the daemon no longer has (moved away, deleted, its
// account removed) presents the main window instead, as every click did
// before.
//
// A click that started the application comes before the connection to the
// daemon: the message waits for it (openPendingNotified), the latest click
// replacing an earlier one, and the main window shows meanwhile (with the
// banner, should the daemon not come).
func (w *Window) OpenNotifiedMessage(acc api.AccountID, id api.MessageID) {
	w.withdrawNotifications([]api.MessageID{id})
	if w.client.State() != client.Connected {
		w.notifiedPending = &notifiedTarget{account: acc, message: id}
		w.Present()
		return
	}
	if _, ok := w.summary(id); ok {
		w.openNotified(id)
		return
	}
	// The waiters of fetchMessage hear about each half (message.get,
	// message.body) as it arrives, and maybe again later: only the answer
	// of message.get decides, once.
	decided := false
	w.fetchMessage(acc, id, func(lm *loadedMessage) {
		if decided || lm.getting {
			return
		}
		decided = true
		if lm.msg == nil {
			w.Present()
			return
		}
		w.openNotified(id)
	})
}

// notifiedTarget is the message of a clicked notification.
type notifiedTarget struct {
	account api.AccountID
	message api.MessageID
}

// openPendingNotified opens the message of a notification clicked before
// the connection was up, once it is (showConnectionState).
func (w *Window) openPendingNotified() {
	p := w.notifiedPending
	if p == nil {
		return
	}
	w.notifiedPending = nil
	w.OpenNotifiedMessage(p.account, p.message)
}

// openNotified opens the window of a message the window knows and marks
// the message read (setSeenIDs leaves a read or outbox message alone).
func (w *Window) openNotified(id api.MessageID) {
	w.openMessageWindow(id)
	w.markRead(id)
}
