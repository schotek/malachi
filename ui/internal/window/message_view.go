// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/compose"
	"github.com/schotek/malachi/ui/internal/htmlview"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The message pane: headers from the summary, then message.get and
// message.body in parallel; the loaded cache is shared with stand-alone
// message windows and the compose prefill.

// maxLoaded bounds the loaded cache by entries and maxLoadedBytes by the
// size of the bodies in it (an HTML body carries its inlined pictures);
// the oldest entries are evicted first.
const (
	maxLoaded      = 64
	maxLoadedBytes = 32 << 20
)

// bodySpinnerDelay is how long the body area stays blank before the
// spinner appears, in milliseconds. Bodies come from the local store, so
// most messages render sooner than this and never show a spinner at all;
// without the delay one would flash on every click, which is more
// distracting than the blank area it replaces.
const bodySpinnerDelay = 400

// loadedSeq numbers cache insertions so pruneLoaded can find the oldest.
// Touched on the main loop only.
var loadedSeq uint64

// loadedMessage is what fetchMessage produced for one message: the full
// header view, the body, or the error that prevented the body. A failed
// message.get is only logged (the summary headers stay) and retried the
// next time the message is shown; a failed body is retried the same way.
type loadedMessage struct {
	msg  *api.Message
	body *api.MessageBodyResult
	err  error // message.body failure

	seq uint64 // insertion order in Window.loaded

	// In-flight halves and who wants to hear about them; a second
	// fetchMessage for the same id while one runs joins instead of asking
	// the daemon twice.
	getting, fetching bool
	waiters           []func(*loadedMessage)
}

// complete reports whether nothing is left to fetch.
func (lm *loadedMessage) complete() bool { return lm.msg != nil && lm.body != nil }

// bodySettled reports whether the body half has an answer (content or error).
func (lm *loadedMessage) bodySettled() bool { return lm.body != nil || lm.err != nil }

// size is what the entry costs the cache: its body.
func (lm *loadedMessage) size() int {
	if lm.body == nil {
		return 0
	}
	return len(lm.body.HTML) + len(lm.body.Text)
}

// messageView is one message display, shared in shape by the main pane and
// the stand-alone window: the header labels, the plain-text body, the HTML
// view (created when the first HTML message is shown) and the remote-image
// banner. Everything shown is server data: the labels never interpret
// markup, and the HTML view only ever gets the sanitiser's output.
type messageView struct {
	win    *Window
	parent *gtk.Window // for dialogs the view opens

	subject, from, recipients, date, body *gtk.Label
	hint                                  *gtk.Label // why only text is shown
	stack                                 *gtk.Stack // "text" | "loading" | "html"
	slot                                  *gtk.Box   // hosts html
	html                                  *htmlview.View

	// The attachment chips (attachments.go) and what is in the box now.
	attachments *adw.WrapBox
	chips       []gtk.Widgetter

	// nested is set on the view of an attached message (embedded.go): its
	// parts have no numbers and message.part cannot serve them, so the
	// chips only name them.
	nested bool

	// The remote-image bar: the count, and the buttons whose work the
	// owner supplies as load (this message) and trust (this sender).
	bar         *gtk.Box
	barLabel    *gtk.Label
	load, trust func()

	// toast shows a message in the owning window, when it wired one.
	toast func(string)

	links []api.Link // of the body on display, for link activation

	// spinner is the pending "reveal the spinner" timer, 0 when none.
	spinner glib.SourceHandle
}

// newMessageView binds the widgets of one message display from a builder;
// the object IDs are the same in window.blp and message_window.blp.
func newMessageView(w *Window, parent *gtk.Window, b *gtk.Builder) *messageView {
	v := &messageView{
		win:         w,
		parent:      parent,
		subject:     b.GetObject("message_subject").Cast().(*gtk.Label),
		from:        b.GetObject("message_from").Cast().(*gtk.Label),
		recipients:  b.GetObject("message_recipients").Cast().(*gtk.Label),
		date:        b.GetObject("message_date").Cast().(*gtk.Label),
		attachments: b.GetObject("message_attachments").Cast().(*adw.WrapBox),
		body:        b.GetObject("message_body").Cast().(*gtk.Label),
		hint:        b.GetObject("body_hint").Cast().(*gtk.Label),
		stack:       b.GetObject("body_stack").Cast().(*gtk.Stack),
		slot:        b.GetObject("html_slot").Cast().(*gtk.Box),
		bar:         b.GetObject("remote_bar").Cast().(*gtk.Box),
		barLabel:    b.GetObject("remote_label").Cast().(*gtk.Label),
	}
	v.plain()
	v.hint.SetLabel(i18n.T("The formatted version of this message could not be shown safely; this is its plain text."))
	load := b.GetObject("remote_load").Cast().(*gtk.Button)
	load.ConnectClicked(func() {
		if v.load != nil {
			v.load()
		}
	})
	trust := b.GetObject("remote_trust").Cast().(*gtk.Button)
	trust.ConnectClicked(func() {
		if v.trust != nil {
			v.trust()
		}
	})
	// The bar goes away the moment the images are in, and hiding the widget
	// that holds the focus makes GTK pass the focus to the next one in the
	// chain — one of the selectable header labels, which selects all of its
	// text as soon as it is focused (see the note on focus-widget in
	// message_window.blp). The message then looked as though it had selected
	// itself while the user was still letting go of the button. Clicking
	// these two therefore leaves the focus where it was; setBarVisible takes
	// care of the keyboard, which has to focus the button to press it.
	load.SetFocusOnClick(false)
	trust.SetFocusOnClick(false)
	return v
}

// setBarVisible shows or hides the remote-image bar, taking the focus out of
// it first: the body is somewhere harmless to put it, unlike the selectable
// label GTK would pick (see newMessageView).
func (v *messageView) setBarVisible(show bool) {
	if !show && v.bar.FocusChild() != nil {
		v.stack.GrabFocus()
	}
	v.bar.SetVisible(show)
}

// paneLabels is the main window's message pane.
func (w *Window) paneLabels() *messageView { return w.pane }

// plain switches markup off on every label (CLAUDE.md rule 3).
func (v *messageView) plain() {
	for _, lb := range []*gtk.Label{v.subject, v.from, v.recipients, v.date, v.body, v.hint, v.barLabel} {
		lb.SetUseMarkup(false)
	}
}

// htmlView is the WebKit view, created on first use: a plain-text mailbox
// never starts a web process.
func (v *messageView) htmlView() *htmlview.View {
	if v.html == nil {
		v.html = htmlview.New(v.win.log, v.win.fetchPart)
		v.html.OnLink = func(uri string) { v.win.openLink(v.parent, uri, v.links) }
		v.html.SetZoom(v.win.settings.TextZoom())
		v.slot.Append(v.html)
	}
	return v.html
}

// setZoom pushes the text-zoom setting to the HTML view, if there is one.
func (v *messageView) setZoom(percent int) {
	if v.html != nil {
		v.html.SetZoom(percent)
	}
}

// showText shows plain text in the body area.
func (v *messageView) showText(text string) {
	v.body.SetLabel(text)
	v.stack.SetVisibleChildName("text")
	if v.html != nil {
		v.html.Clear() // drop the pictures of the previous message
	}
}

// renderHeaders shows the headers: from the summary alone, or from the full
// message when m is not nil (recipients with Cc). The attachment chips are
// renderAttachments' business: they depend on the body too.
func (v *messageView) renderHeaders(s api.MessageSummary, m *api.Message) {
	from, to, date := s.From, s.To, s.Date
	var cc []api.Address
	if m != nil {
		from, to, cc, date = m.From, m.To, m.CC, m.Date
		s.Subject = m.Subject
	}
	v.subject.SetLabel(subjectText(s.Subject))
	var first api.Address
	if len(from) > 0 {
		first = from[0]
	}
	v.from.SetLabel(widget.FormatAddress(first))
	r := recipientsText(to, cc)
	v.recipients.SetLabel(r)
	v.recipients.SetVisible(r != "")
	if date.IsZero() {
		v.date.SetLabel("")
	} else {
		v.date.SetLabel(widget.FormatDateTime(date))
	}
}

// renderBody shows the body, its state, or the error that prevented it:
// the sanitised HTML in the web view when there is one, the plain text
// otherwise, with a hint when the HTML was withheld and the banner when
// remote images were removed.
func (v *messageView) renderBody(lm *loadedMessage) {
	v.cancelSpinner()
	b, err := lm.body, lm.err
	v.links = nil
	if err != nil {
		v.hint.SetVisible(false)
		v.setBarVisible(false)
		v.showText(widget.RPCErrorText(i18n.T("Loading the message"), err))
		return
	}
	if showsHTML(b) {
		v.links = b.Links
		v.hint.SetVisible(false)
		v.htmlView().Load(b.HTML)
		v.stack.SetVisibleChildName("html")
		renderRemoteBar(v, lm)
		return
	}
	v.hint.SetVisible(b != nil && b.HTMLWithheld)
	v.setBarVisible(false)
	v.showText(bodyText(b))
}

// render shows whatever lm holds so far: full headers once message.get
// answered, the body once message.body did, and the attachment chips from
// both. A nil lm shows the summary and the loading placeholder.
func (v *messageView) render(s api.MessageSummary, lm *loadedMessage) {
	if lm == nil {
		v.renderHeaders(s, nil)
		v.loading()
		v.renderAttachments(s, nil)
		return
	}
	v.renderHeaders(s, lm.msg)
	if lm.bodySettled() {
		v.renderBody(lm)
	} else {
		v.loading()
	}
	v.renderAttachments(s, lm)
}

// loading empties the body area while the body is on its way and, if it
// takes long enough to notice, shows a spinner in the middle of it.
func (v *messageView) loading() {
	v.cancelSpinner()
	v.links = nil
	v.hint.SetVisible(false)
	v.setBarVisible(false)
	// Blank at once: this also drops the pictures of the message before.
	v.showText("")
	v.spinner = glib.TimeoutAdd(bodySpinnerDelay, func() bool {
		v.spinner = 0
		v.stack.SetVisibleChildName("loading")
		return false
	})
}

// cancelSpinner disarms the pending spinner, if any. Safe to call twice
// and after the view is gone.
func (v *messageView) cancelSpinner() {
	if v.spinner != 0 {
		glib.SourceRemove(v.spinner)
		v.spinner = 0
	}
}

// subjectText is the subject to display; an empty one gets a placeholder.
func subjectText(subject string) string {
	if s := strings.TrimSpace(subject); s != "" {
		return s
	}
	return i18n.T("(No subject)")
}

// recipientsText is the To / Cc block, one line each, empty without any.
func recipientsText(to, cc []api.Address) string {
	var lines []string
	if len(to) > 0 {
		// TRANSLATORS: message header line; %s is a list of recipients.
		lines = append(lines, fmt.Sprintf(i18n.T("To: %s"), compose.FormatAddressList(to)))
	}
	if len(cc) > 0 {
		// TRANSLATORS: message header line; %s is a list of recipients.
		lines = append(lines, fmt.Sprintf(i18n.T("Cc: %s"), compose.FormatAddressList(cc)))
	}
	return strings.Join(lines, "\n")
}

// bodyText is what the body label shows for a message.body result.
func bodyText(b *api.MessageBodyResult) string {
	if b == nil {
		return i18n.T("(Empty message)")
	}
	switch b.BodyState {
	case api.BodyPending:
		return i18n.T("Downloading…")
	case api.BodyTooBig:
		return i18n.T("This message is too large to download.")
	case api.BodyFailed:
		return i18n.T("This message could not be read.")
	}
	text := strings.TrimRight(b.Text, " \t\r\n")
	if strings.TrimSpace(text) == "" {
		return i18n.T("(Empty message)")
	}
	return text
}

// showMessage displays message id in the pane: summary headers at once,
// the rest (and the outbox banner, for a queued message) when fetchMessage
// returns.
func (w *Window) showMessage(id api.MessageID) {
	s, _, ok := w.model.message(id)
	if !ok {
		return
	}
	gen := w.model.bumpBody()
	labels := w.paneLabels()
	labels.render(s, nil)
	w.outboxBanner.SetRevealed(false)
	w.messageStack.SetVisibleChildName("message")
	w.fetchMessage(s.AccountID, id, func(lm *loadedMessage) {
		if gen != w.model.bodyGen {
			return // the pane moved on
		}
		labels.render(s, lm)
		renderOutboxBanner(w.outboxBanner, lm.msg)
	})
}

// fetchMessage runs message.get and message.body for id (each only when
// the cache lacks it and no request is in flight) and calls done on the
// main loop after every answer, with the cache entry so far; a complete
// entry calls done at once. Callers guard staleness themselves (the pane by
// bodyGen, a message window by its closed flag): the cache is keyed by id
// and stays valid whatever the pane shows now.
func (w *Window) fetchMessage(acc api.AccountID, id api.MessageID, done func(*loadedMessage)) {
	lm := w.loaded[id]
	if lm == nil {
		lm = &loadedMessage{}
		w.storeLoaded(id, lm)
	}
	if lm.complete() {
		done(lm)
		return
	}
	lm.waiters = append(lm.waiters, done)
	if lm.msg == nil && !lm.getting {
		lm.getting = true
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
			defer cancel()
			var res api.MessageGetResult
			err := w.client.Call(ctx, api.MethodMessageGet, api.MessageGetParams{AccountID: acc, MessageID: id}, &res)
			glib.IdleAdd(func() {
				lm.getting = false
				if err != nil {
					w.log.Warn("message.get", "err", err)
				} else {
					lm.msg = &res.Message
				}
				w.settleLoaded(id, lm)
			})
		}()
	}
	if lm.body == nil && !lm.fetching {
		lm.fetching = true
		lm.err = nil // a retry after a failure
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
			defer cancel()
			var res api.MessageBodyResult
			err := w.client.Call(ctx, api.MethodMessageBody, api.MessageBodyParams{AccountID: acc, MessageID: id}, &res)
			glib.IdleAdd(func() {
				lm.fetching = false
				if err != nil {
					w.log.Warn("message.body", "err", err)
					lm.err = err
				} else {
					lm.body = &res
				}
				w.settleLoaded(id, lm)
			})
		}()
	}
}

// settleLoaded runs on the main loop after one half of lm arrived: the
// entry is put back if it was evicted meanwhile and the waiters hear about
// it; once nothing is in flight any more they are dropped.
func (w *Window) settleLoaded(id api.MessageID, lm *loadedMessage) {
	if w.loaded[id] == nil {
		w.storeLoaded(id, lm)
	}
	waiters := lm.waiters
	if !lm.getting && !lm.fetching {
		lm.waiters = nil
	}
	for _, done := range waiters {
		done(lm)
	}
}

// storeLoaded caches lm for id, evicting the oldest entries beyond the
// caps.
func (w *Window) storeLoaded(id api.MessageID, lm *loadedMessage) {
	loadedSeq++
	lm.seq = loadedSeq
	w.loaded[id] = lm
	pruneLoaded(w.loaded, maxLoaded, maxLoadedBytes)
}

// pruneLoaded evicts the entries with the lowest seq until at most limit
// remain and their bodies fit in maxBytes; the newest entry always stays.
func pruneLoaded(m map[api.MessageID]*loadedMessage, limit, maxBytes int) {
	total := 0
	for _, lm := range m {
		total += lm.size()
	}
	for len(m) > 1 && (len(m) > limit || total > maxBytes) {
		var oldest api.MessageID
		first := true
		for id, lm := range m {
			if first || lm.seq < m[oldest].seq {
				oldest, first = id, false
			}
		}
		total -= m[oldest].size()
		delete(m, oldest)
	}
}

// summary is what the window knows about message id: the list entry, or
// the cached full message when the list has moved on (a message window
// outliving the folder it was opened from).
func (w *Window) summary(id api.MessageID) (api.MessageSummary, bool) {
	if s, _, ok := w.model.message(id); ok {
		return s, true
	}
	if lm := w.loaded[id]; lm != nil && lm.msg != nil {
		return lm.msg.MessageSummary, true
	}
	return api.MessageSummary{}, false
}

// openCompose opens a reply or forward of message id, prefilled from the
// loaded message when message.get and message.body have answered, else
// from the summary alone.
func (w *Window) openCompose(kind compose.Kind, id api.MessageID) {
	s, ok := w.summary(id)
	if !ok {
		return
	}
	src := compose.Source{ID: id, From: s.From, To: s.To, Subject: s.Subject, Date: s.Date}
	if lm := w.loaded[id]; lm != nil {
		if m := lm.msg; m != nil {
			src.From, src.ReplyTo, src.To, src.CC = m.From, m.ReplyTo, m.To, m.CC
			src.Subject, src.Date = m.Subject, m.Date
		}
		if lm.body != nil && lm.body.BodyState == api.BodyFetched {
			src.Text = lm.body.Text
		}
	}
	self := w.compose.SelfAddress()
	if acc, ok := w.model.account(s.AccountID); ok {
		self = selfAddress(acc)
	}
	p := compose.Prefill(kind, src, self, time.Now())
	p.AccountID = s.AccountID
	w.compose.Open(p)
}

// openMessageWindow opens message id in its own window, or raises the
// window that already shows it (w.openMessages).
func (w *Window) openMessageWindow(id api.MessageID) {
	if mw, ok := w.openMessages[id]; ok {
		mw.Present()
		return
	}
	s, ok := w.summary(id)
	if !ok {
		return
	}
	mw := newMessageWindow(w, s)
	w.openMessages[id] = mw
	mw.ConnectCloseRequest(func() bool {
		mw.closed = true
		// The only timer that would outlive the window.
		mw.view.cancelSpinner()
		delete(w.openMessages, id)
		return false
	})
	mw.Present()
	w.fetchMessage(s.AccountID, id, func(lm *loadedMessage) {
		if mw.closed {
			return
		}
		mw.show(s, lm)
	})
}

// closeMessageWindow closes the stand-alone window of message id, if any,
// and the windows of the messages attached to it (the message left the
// folder).
func (w *Window) closeMessageWindow(id api.MessageID) {
	if mw, ok := w.openMessages[id]; ok {
		mw.Close()
	}
	w.closeEmbeddedWindows(id)
}
