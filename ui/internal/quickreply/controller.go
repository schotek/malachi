// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package quickreply is the quick-reply bar under a conversation shown as
// chat in Mail (docs/plans/chat-view.md §10 and §11) without its widgets:
// what the bar shows (its target, modes and text, per conversation) and
// what its buttons do — Send through the daemon's usual way, Open in a
// Window and Forward… through the compose window.
//
// The rules of the bar's content are ui/internal/conversation's
// (QuickReplyTarget, QuickReplyModes, QuickReplyHTML, QuickReplyBlank,
// QuickReplyBody); the texts are its too and the view's (i18n, L10n): this
// package has none, the host says what happened (Sent, Failed) and the
// view says it in words. It is pure Go (no GTK, no GLib), so its tests run
// anywhere; the window gives it a Host over the GLib main loop and the
// daemon's client (ui/internal/window quick_reply.go). Ported 1:1 to
// MalachiCore Controllers/QuickReplyController.swift and
// Malachi.Core Controllers/QuickReplyController.cs.
//
// Threads: every method runs on the main loop. The calls to the daemon run
// on a goroutine of their own and come back through Host.Post.
package quickreply

import (
	"context"
	"slices"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/capabilities"
	"github.com/schotek/malachi/ui/internal/conversation"
)

// CallTimeout bounds each call of Send (draft.create, draft.save,
// message.send, draft.delete): draft.create re-reads the original and
// copies its pictures, which can take longer than an ordinary call. The
// same bound as the compose window's draft.create (ui/internal/window
// composeTimeout).
const CallTimeout = 30 * time.Second

// Host is what the controller needs of the window. Call blocks and is only
// ever called off the main loop; every other method is called on it.
type Host interface {
	// Call is one JSON-RPC call to the daemon, a server-side error as
	// *api.Error (ui/internal/client's Client has it).
	Call(ctx context.Context, method string, params any, result any) error
	// Post runs f on the main loop, after what was posted before; it may
	// be called from any goroutine.
	Post(f func())
	// Attribution is the line above the quote of a reply in mode to
	// message id, as the compose window computes it (compose.Attribution
	// over what the window knows of the message); "" for none.
	Attribution(mode conversation.QuickReplyMode, id api.MessageID) string
	// BeforeCreate runs before draft.create of a reply (never of a
	// comment): the window downloads message id first when its compose
	// window would (pictures of the quote kept on the mail server only),
	// ignores the outcome as the window does, and calls done on the main
	// loop. Without a reason to download it calls done at once.
	BeforeCreate(id api.MessageID, done func())
	// OpenCompose opens the compose window with a reply, reply all or
	// forward (mode) of message id — a comment when the account comments —
	// prepared by draft.create as the window always is, with ownHTML (safe
	// HTML: conversation.QuickReplyHTML) in its editor as the user's own
	// text, marked as an edit. opened is called on the main loop once a
	// window really opened, and never when none does (one is being
	// prepared for the message already, the message is unknown, or a
	// comment's draft.create failed).
	OpenCompose(mode conversation.QuickReplyMode, id api.MessageID, ownHTML string, opened func())
	// Sent says a quick reply went to the outbox (comment: a comment on an
	// issue); the window shows the compose window's toast.
	Sent(comment bool)
	// Failed says Send did not go through (err from draft.create,
	// draft.save or message.send); the window shows the compose window's
	// toast for a failed send. The text stays in the bar.
	Failed(err error)
}

// State is what the bar shows, for the conversation shown (Thread).
type State struct {
	// Thread is the conversation shown; "" for none.
	Thread api.ThreadID
	// Visible: the chat layout is shown, the conversation has a target and
	// its account answers it (Reply). The bar is hidden otherwise.
	Visible bool
	// Target is the message a quick reply answers
	// (conversation.QuickReplyTarget); the zero Item without one. Its
	// Sender names it in the mode button's tooltip.
	Target conversation.Item
	// Modes are the mode menu's entries (conversation.QuickReplyModes),
	// QuickForward among them opens the window (Forward).
	Modes []conversation.QuickReplyMode
	// Mode is the chosen mode: QuickReply or QuickReplyAll, never
	// QuickForward; on an issue always QuickReply.
	Mode conversation.QuickReplyMode
	// Text is what was typed in this conversation's bar.
	Text string
	// Blank: Text has nothing to send (conversation.QuickReplyBlank);
	// Send is disabled.
	Blank bool
	// Sending: a Send of this conversation is on its way; the bar is
	// disabled until it ends.
	Sending bool
	// Comment: the reply is a comment on an issue (the placeholder and
	// the Reply label say so).
	Comment bool
}

// thread is the bar's state of one conversation, kept in memory until the
// text is sent, moved into a window or cleared (Q9).
type thread struct {
	text    string
	mode    conversation.QuickReplyMode
	sending bool
}

// Controller is the quick-reply bar of one main window. Make it with New.
type Controller struct {
	// OnChange is called after every change of State (the view reads it
	// again), on the main loop.
	OnChange func()

	host Host

	// shown is the conversation shown; chatShown says the chat layout is.
	shown     api.ThreadID
	chatShown bool
	// What Update learnt of the shown conversation.
	target  conversation.Item
	hasTgt  bool
	modes   []conversation.QuickReplyMode
	reply   bool
	comment bool
	account api.Account

	threads map[api.ThreadID]*thread
}

// New makes a controller over host.
func New(host Host) *Controller {
	return &Controller{host: host, threads: map[api.ThreadID]*thread{}}
}

// Show says which conversation the pane shows now ("" for none). Its
// target is unknown until the next Update; the text and mode of every
// conversation stay.
func (c *Controller) Show(t api.ThreadID) {
	if t == c.shown {
		return
	}
	c.shown = t
	c.target, c.hasTgt, c.modes, c.reply, c.comment = conversation.Item{}, false, nil, false, false
	c.account = api.Account{}
	c.changed()
}

// Update takes the shown conversation's model m, the actions of its
// target's card (conversation.CardActions for the target) and its account.
// A model of another conversation (m.Thread neither "" nor the one shown)
// shows that one first, as Show does.
func (c *Controller) Update(m conversation.Model, actions capabilities.Actions, account api.Account) {
	if m.Thread != "" && m.Thread != c.shown {
		c.shown = m.Thread
	}
	c.target, c.hasTgt = conversation.QuickReplyTarget(m)
	c.modes = conversation.QuickReplyModes(actions)
	c.reply = actions.Reply
	c.comment = actions.Comment
	c.account = account
	c.changed()
}

// SetChatShown says whether the chat layout is shown (the timeline has no
// bar).
func (c *Controller) SetChatShown(shown bool) {
	if shown == c.chatShown {
		return
	}
	c.chatShown = shown
	c.changed()
}

// SetText is what the field holds now, for the conversation shown.
func (c *Controller) SetText(text string) {
	if c.shown == "" {
		return
	}
	st := c.threads[c.shown]
	if st == nil {
		if text == "" {
			return
		}
		st = &thread{}
		c.threads[c.shown] = st
	}
	if st.text == text {
		return
	}
	st.text = text
	c.prune(c.shown)
	c.changed()
}

// SetMode chooses QuickReply or QuickReplyAll among the modes offered for
// the conversation shown; anything else (QuickForward, a mode not
// offered) is ignored.
func (c *Controller) SetMode(mode conversation.QuickReplyMode) {
	if c.shown == "" || mode == conversation.QuickForward || !slices.Contains(c.modes, mode) {
		return
	}
	st := c.threads[c.shown]
	if st == nil {
		if mode == (thread{}).mode {
			return // the zero state's mode: nothing to keep
		}
		st = &thread{}
		c.threads[c.shown] = st
	}
	if st.mode == mode {
		return
	}
	st.mode = mode
	c.prune(c.shown)
	c.changed()
}

// State is what the bar shows now.
func (c *Controller) State() State {
	s := State{
		Thread:  c.shown,
		Target:  c.target,
		Modes:   slices.Clone(c.modes),
		Comment: c.comment,
		Mode:    c.mode(),
	}
	if st := c.threads[c.shown]; st != nil && c.shown != "" {
		s.Text, s.Sending = st.text, st.sending
	}
	s.Blank = conversation.QuickReplyBlank(s.Text)
	s.Visible = c.visible()
	return s
}

// visible: the bar shows (State.Visible).
func (c *Controller) visible() bool {
	return c.chatShown && c.shown != "" && c.hasTgt && c.reply
}

// mode is the chosen mode of the conversation shown when it is still
// offered, else the first mode offered that is not QuickForward, else
// QuickReply; on an issue always QuickReply.
func (c *Controller) mode() conversation.QuickReplyMode {
	if c.comment {
		return conversation.QuickReply
	}
	if st := c.threads[c.shown]; st != nil && slices.Contains(c.modes, st.mode) {
		return st.mode
	}
	for _, m := range c.modes {
		if m != conversation.QuickForward {
			return m
		}
	}
	return conversation.QuickReply
}

// Send sends the text of the conversation shown as a reply (or comment) to
// its target: draft.create on the target (after Host.BeforeCreate, never
// for a comment) with the attribution the window would use, one draft.save
// of its draft with the typed text above the original quoted
// (conversation.QuickReplyBody), and message.send. Success clears the
// text and says Host.Sent; a failure says Host.Failed and keeps the text,
// and a failed message.send first deletes the saved draft (draft.delete,
// its error ignored) so that no draft the user never saw stays behind. The
// outcome goes to the conversation Send started in, whichever is shown by
// then. Nothing happens when the bar is hidden, the text is blank or a
// Send of this conversation is on its way.
func (c *Controller) Send() {
	st := c.threads[c.shown]
	if !c.visible() || st == nil || st.sending || conversation.QuickReplyBlank(st.text) {
		return
	}
	t, target, text, comment := c.shown, c.target, st.text, c.comment
	mode := c.mode()
	acc := target.Message.AccountID
	if acc == "" {
		acc = c.account.ID
	}
	id := target.Message.ID
	st.sending = true
	c.changed()

	start := func() {
		params := api.DraftCreateParams{AccountID: acc, Mode: mode.ComposeMode(), MessageID: id}
		if !comment {
			params.Attribution = c.host.Attribution(mode, id)
		}
		go func() {
			err := deliver(c.host, params, text)
			c.host.Post(func() { c.finish(t, text, comment, err) })
		}()
	}
	if comment {
		start()
		return
	}
	c.host.BeforeCreate(id, start)
}

// call is one call bounded by CallTimeout; off the main loop.
func call(h Host, method string, params, result any) error {
	ctx, cancel := context.WithTimeout(context.Background(), CallTimeout)
	defer cancel()
	return h.Call(ctx, method, params, result)
}

// deliver is Send's calls, off the main loop: draft.create, draft.save,
// message.send, and draft.delete after a failed message.send.
func deliver(h Host, params api.DraftCreateParams, text string) error {
	var created api.DraftCreateResult
	if err := call(h, api.MethodDraftCreate, params, &created); err != nil {
		return err
	}
	d := SaveDraft(params.Mode, text, created.Draft)
	if d.AccountID == "" {
		d.AccountID = params.AccountID
	}
	var saved api.DraftSaveResult
	if err := call(h, api.MethodDraftSave, api.DraftSaveParams{Draft: d}, &saved); err != nil {
		return err
	}
	var sent api.MessageSendResult
	err := call(h, api.MethodMessageSend, api.MessageSendParams{AccountID: d.AccountID, DraftID: saved.DraftID, Version: saved.Version}, &sent)
	if err != nil {
		// An unknown draft is no error of draft.delete, so a message.send
		// that queued the message and then timed out leaves nothing; any
		// other failure leaves a draft the conversation shows.
		_ = call(h, api.MethodDraftDelete, api.DraftDeleteParams{AccountID: d.AccountID, DraftID: saved.DraftID}, &api.DraftDeleteResult{})
	}
	return err
}

// SaveDraft is the draft a quick reply saves with text typed in mode on
// d, draft.create's result: the shape of the compose window's own save
// (compose draftController.build) — the template's identity, recipients
// and subject, the body conversation.QuickReplyBody (the typed text above
// the original quoted; a comment's text alone), the text as typed (the
// daemon derives its own from the HTML), the template's attachments by id
// (else the quote's pictures would be lost), To never nil, and a comment
// always public (the bar has no visibility; the window has).
func SaveDraft(mode api.ComposeMode, text string, d api.Draft) api.Draft {
	out := api.Draft{
		ID:         d.ID,
		AccountID:  d.AccountID,
		Version:    d.Version,
		To:         d.To,
		CC:         d.CC,
		BCC:        d.BCC,
		Subject:    d.Subject,
		TextBody:   text,
		HTMLBody:   conversation.QuickReplyBody(mode, text, d),
		InReplyTo:  d.InReplyTo,
		Forwarding: d.Forwarding,
		Replaces:   d.Replaces,
	}
	if out.To == nil {
		out.To = []api.Address{}
	}
	if d.Comment != nil {
		out.Comment = &api.DraftComment{Issue: d.Comment.Issue, Visibility: api.CommentPublic}
	}
	for _, a := range d.Attachments {
		out.Attachments = append(out.Attachments, api.DraftAttachment{ID: a.ID})
	}
	return out
}

// finish ends a Send of conversation t whose text was text.
func (c *Controller) finish(t api.ThreadID, text string, comment bool, err error) {
	st := c.threads[t]
	if st == nil {
		st = &thread{}
		c.threads[t] = st
	}
	st.sending = false
	if err == nil {
		// What was typed meanwhile (the field is disabled while sending,
		// but a view may still pass a change) stays.
		if st.text == text {
			st.text = ""
		}
		c.host.Sent(comment)
	} else {
		c.host.Failed(err)
	}
	c.prune(t)
	c.changed()
}

// OpenWindow moves the text of the conversation shown into the compose
// window: a reply in the chosen mode (a comment on an issue) to the
// target, prepared by the daemon as the window always is, with the text as
// the user's own. The bar's text is cleared once the window opened
// (Host.OpenCompose's opened), and only if it is unchanged by then. No
// draft is saved here; the window's own autosave does that.
func (c *Controller) OpenWindow() {
	c.open(c.mode())
}

// Forward is the mode menu's Forward…: the compose window with a forward
// of the target and the text of the conversation shown above it, as
// OpenWindow. Nothing happens when the account does not forward.
func (c *Controller) Forward() {
	if !slices.Contains(c.modes, conversation.QuickForward) {
		return
	}
	c.open(conversation.QuickForward)
}

func (c *Controller) open(mode conversation.QuickReplyMode) {
	if !c.visible() {
		return
	}
	t, text := c.shown, ""
	if st := c.threads[t]; st != nil {
		if st.sending {
			return
		}
		text = st.text
	}
	c.host.OpenCompose(mode, c.target.Message.ID, conversation.QuickReplyHTML(text), func() {
		st := c.threads[t]
		if st == nil || st.text != text || text == "" {
			return
		}
		st.text = ""
		c.prune(t)
		c.changed()
	})
}

// prune forgets a conversation whose state is the zero one.
func (c *Controller) prune(t api.ThreadID) {
	if st := c.threads[t]; st != nil && *st == (thread{}) {
		delete(c.threads, t)
	}
}

func (c *Controller) changed() {
	if c.OnChange != nil {
		c.OnChange()
	}
}
