// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/capabilities"
	"github.com/schotek/malachi/ui/internal/compose"
	"github.com/schotek/malachi/ui/internal/conversation"
	"github.com/schotek/malachi/ui/internal/quickreply"
)

// The quick-reply bar of the reading pane (docs/plans/chat-view.md §10,
// §11, §12): its rules and state are ui/internal/quickreply's Controller,
// which this file gives the window as its Host; its widgets are
// quick_reply_bar.go's (quick_reply_bar.blp). The window owns both, made
// with it (attachQuickReply): the bar is the last child of window.blp's
// reading_column, under message_stack, so it stands in one place at the
// bottom of the pane under every page that has it, and a hidden bar takes
// no room.
//
// Which page has it is this file's (updateQuickReply, the one place that
// feeds the controller): the page "message" (a single message,
// showPaneMessage: its model of one member, conversation.SingleModel, the
// actions of its card and its account; no bar in the Outbox or Drafts) and
// the conversation page while the chat shows (the conversation
// controller's quickReplyInput, conversation_host.go reports every change
// and every switch of the layout). The empty page, the page of several
// selected rows and the timeline have none; neither has the board (its
// page replaces the Mail page with the bar in it) nor a message window.
// It runs when message_stack switches pages, when the pane shows another
// single message, and on every change of the conversation shown; a
// conversation cleared while its page still shows (hideConversation runs
// before the next page is chosen) reports nothing, so the bar does not
// hide and come back, keyboard and all, on the way from a conversation to
// a message of the same thread.
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

// attachQuickReply makes the window's quick-reply controller and bar and
// appends the bar to column under message_stack (window.blp's
// reading_column; a smoke test's column of its own). From then on the bar
// follows the page message_stack shows.
func (w *Window) attachQuickReply(column *gtk.Box) {
	w.quickReply = newQuickReply(w)
	w.quickReplyBar = newQuickReplyBar(w, data.Builder("quick_reply_bar.ui"), w.quickReply)
	column.Append(w.quickReplyBar.root)
	w.messageStack.NotifyProperty("visible-child-name", w.updateQuickReply)
	w.updateQuickReply()
}

// useQuickReply makes ctrl the bar's controller (a smoke test's, over a
// fake host) and brings it up to date with the page shown.
func (w *Window) useQuickReply(ctrl *quickreply.Controller) {
	w.quickReply = ctrl
	w.quickReplyBar.setController(ctrl)
	w.updateQuickReply()
}

// showPaneMessage shows the page "message" for single message s
// (showMessage) and has the quick-reply bar follow it: s is noted before
// the page switches, so that the switch finds it, and the bar follows once
// more for a message after a message, where the page does not switch.
func (w *Window) showPaneMessage(s api.MessageSummary) {
	w.paneMessage = s
	w.messageStack.SetVisibleChildName("message")
	w.updateQuickReply()
}

// updateQuickReply has the quick-reply bar follow the reading pane: the
// conversation it shows (Show; a single message's thread and account), its
// model, the actions of the card a reply goes to and its account (Update),
// and whether the page has the bar (SetShown). The bar renders once, after
// all of it (hold), so that a step in between (Show of another thread
// before its model) never hides the bar with the keyboard in it.
func (w *Window) updateQuickReply() {
	qr, bar := w.quickReply, w.quickReplyBar
	if qr == nil || bar == nil {
		return
	}
	page := w.messageStack.VisibleChildName()
	bar.hold(func() {
		bar.setConversationSurface(page == convPageName)
		switch page {
		case "message":
			s := w.paneMessage
			m, actions, account, shown := w.model.singleQuickReplyInput(s)
			bar.leaveIfOther(s.AccountID, s.ThreadID)
			qr.Show(s.AccountID, s.ThreadID)
			qr.Update(m, actions, account)
			qr.SetShown(shown)
		case convPageName:
			h := w.conv
			if h == nil || h.ctrl.thread == "" {
				// Not built yet, or cleared on the way to another page,
				// which reports as it shows.
				return
			}
			bar.leaveIfOther(h.ctrl.account, h.ctrl.thread)
			qr.Show(h.ctrl.account, h.ctrl.thread)
			// Without a model (the members are on their way, or thread.get
			// failed with nothing to build from) the empty model: the bar
			// hides until the real one, the same thread shown a moment ago
			// as a single message must not lend it its target.
			m, actions, account, ok := h.ctrl.quickReplyInput()
			if !ok {
				m, actions, account = conversation.Model{}, capabilities.Actions{}, api.Account{}
			}
			qr.Update(m, actions, account)
			qr.SetShown(h.chatShown)
		default:
			qr.Show("", "")
			qr.SetShown(false)
		}
	})
}

// singleQuickReplyInput is what the quick-reply bar needs of single
// message s on the pane's page "message": its model of one member
// (conversation.SingleModel; Mine by the account's address, as a
// conversation's member, or for a message of a Sent folder), the actions
// of its card (conversation.CardActions, as a conversation's card offers
// them: none for an unknown account or an event of an issue), its
// account, and whether the bar has a place under it at all (false in the
// Outbox, in Drafts, whose message has an Edit bar, and for no message).
// A message without a thread id gets no bar either (the controller keeps
// no text without a thread; SingleModel's Thread is ""). The macOS
// client's ConversationController.quickReplyInput(single:).
func (m *mailModel) singleQuickReplyInput(s api.MessageSummary) (conversation.Model, capabilities.Actions, api.Account, bool) {
	acc, known := m.account(s.AccountID)
	sent := m.folderRole(folderKey{Account: s.AccountID, Folder: s.FolderID}) == api.RoleSent
	model := conversation.SingleModel(s, conversation.IsMine(s, acc) || sent)
	var actions capabilities.Actions
	if known {
		actions = conversation.CardActions(acc, s, len(capabilities.ForwardAccounts(m.accounts)) > 0)
	}
	shown := s.ID != "" && !m.inOutbox(s) && !m.inDrafts(s)
	return model, actions, acc, shown
}

// focusReadingView gives the keyboard to the view the reading pane shows:
// the body of the single message, the layout of the conversation shown,
// else whatever on the page takes it; false when nothing does.
func (w *Window) focusReadingView() bool {
	switch w.messageStack.VisibleChildName() {
	case "message":
		if w.pane != nil && w.pane.stack.GrabFocus() {
			return true
		}
	case convPageName:
		if w.conv != nil && w.conv.shownScroller().GrabFocus() {
			return true
		}
	}
	// No wider fallback: the page's box would give the keyboard to its
	// first focusable child, the selectable subject label on the message
	// page (which newMessageView keeps the keyboard away from).
	return false
}
