// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"encoding/json"
	"strings"

	"github.com/diamondburned/gotk4/pkg/gio/v2"

	"github.com/schotek/malachi/backend/pkg/api"
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
	default:
		// TODO(phase-1): notify.syncState → status line, notify.authRequired →
		// OpenURI portal flow.
		w.log.Info("notification", "method", method)
	}
}

// notifyNewMessage shows a desktop notification (and plays the sound) unless
// the user is looking at the main window right now.
func (w *Window) notifyNewMessage(n api.NewMessageNotification) {
	if w.IsActive() {
		return
	}
	if w.settings.DesktopNotifications() {
		title, body := notificationText(n)
		note := gio.NewNotification(title)
		note.SetBody(body)
		note.SetIcon(gio.NewThemedIcon("mail-unread-symbolic"))
		// TODO(phase-1): target the message once the list selects by ID.
		note.SetDefaultAction("app.show")
		w.app.SendNotification("message-"+string(n.Message.ID), note)
	}
	if w.settings.NotificationSound() {
		w.playNewMailSound()
	}
}

// notificationText builds the plain-text title and body. GNotification
// bodies are not markup, but the text is still attacker-controlled: it is
// trimmed and capped.
func notificationText(n api.NewMessageNotification) (title, body string) {
	title = "New message"
	if len(n.Message.From) > 0 {
		if name := widget.DisplayName(n.Message.From[0]); name != "" {
			title = name
		}
	}
	body = strings.TrimSpace(n.Message.Subject)
	if body == "" {
		body = "(No subject)"
	}
	if len(body) > notificationBodyMax {
		body = strings.ToValidUTF8(body[:notificationBodyMax], "") + "…"
	}
	return title, body
}
