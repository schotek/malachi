// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"

	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/compose"
	"github.com/schotek/malachi/ui/internal/conversation"
	"github.com/schotek/malachi/ui/internal/quickreply"
)

// The quick-reply bar under a conversation shown as chat
// (docs/plans/chat-view.md §10, §11): its rules and state are
// ui/internal/quickreply's Controller, which this file gives the window
// as its Host. The bar's widgets and their wiring to the conversation's
// host are quick_reply_bar.go's and conversation_host.go's.
//
// The texts are the compose window's (compose.QueuedText,
// compose.SendFailedText), so this file adds no msgid.

// quickReplyHost is quickreply.Host over the main window: the daemon's
// client, the GLib main loop, and what the window knows to reply as its
// compose window would.
type quickReplyHost struct{ w *Window }

var _ quickreply.Host = quickReplyHost{}

// newQuickReply is the main window's quick-reply controller.
func newQuickReply(w *Window) *quickreply.Controller {
	return quickreply.New(quickReplyHost{w})
}

// Call is one call through the window's client; off the main loop.
func (h quickReplyHost) Call(ctx context.Context, method string, params, result any) error {
	return h.w.client.Call(ctx, method, params, result)
}

// Post runs f on the GTK main loop.
func (quickReplyHost) Post(f func()) { glib.IdleAdd(f) }

// quickReplyKind is the compose window's kind for mode.
func quickReplyKind(mode conversation.QuickReplyMode) compose.Kind {
	switch mode {
	case conversation.QuickReplyAll:
		return compose.KindReplyAll
	case conversation.QuickForward:
		return compose.KindForward
	}
	return compose.KindReply
}

// Attribution is the line above the quote as openComposeWith computes it
// from the summary and what was loaded of message id; "" when the window
// does not know the message (it would not open a reply to it either).
func (h quickReplyHost) Attribution(mode conversation.QuickReplyMode, id api.MessageID) string {
	s, ok := h.w.summary(id)
	if !ok {
		return ""
	}
	return compose.Attribution(quickReplyKind(mode), composeSource(id, s, h.w.loaded[id]))
}

// BeforeCreate downloads message id first when a reply from the compose
// window would (replyNeedsDownload: pictures of the quote kept on the
// mail server only), whatever the outcome, as openComposeWith does; done
// runs on the main loop.
func (h quickReplyHost) BeforeCreate(id api.MessageID, done func()) {
	s, ok := h.w.summary(id)
	if !ok || !replyNeedsDownload(h.w.loaded[id]) {
		done()
		return
	}
	go func() {
		_, _ = h.w.download(s.AccountID, id) // logs its own failure
		glib.IdleAdd(done)
	}()
}

// OpenCompose opens the compose window from the main window with ownHTML
// as the user's own text (openComposeWith; a comment on an account that
// comments); opened runs once a window opened.
func (h quickReplyHost) OpenCompose(mode conversation.QuickReplyMode, id api.MessageID, ownHTML string, opened func()) {
	h.w.openComposeWith(h.w, quickReplyKind(mode), id, ownHTML, opened)
}

// Sent shows the compose window's toast after Send: "Message queued for
// sending", or a comment's.
func (h quickReplyHost) Sent(comment bool) { h.w.Toast(compose.QueuedText(comment)) }

// Failed shows the compose window's toast for a send that did not go
// through.
func (h quickReplyHost) Failed(err error) {
	h.w.log.Warn("quick reply", "err", err)
	h.w.Toast(compose.SendFailedText(err))
}
