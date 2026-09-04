// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/compose"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The message pane: headers from the summary, then message.get and
// message.body in parallel; the loaded cache is shared with stand-alone
// message windows and the compose prefill.

// maxLoaded bounds the loaded cache; the oldest entries are evicted first.
const maxLoaded = 64

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

// messageLabels are the header and body labels of one message view, shared
// by the main pane and the stand-alone window. Everything shown is server
// data: the labels never interpret markup.
type messageLabels struct {
	subject, from, recipients, date, attachments, body *gtk.Label
}

// paneLabels are the main window's message pane labels.
func (w *Window) paneLabels() messageLabels {
	return messageLabels{
		subject:     w.messageSubject,
		from:        w.messageFrom,
		recipients:  w.messageRecipients,
		date:        w.messageDate,
		attachments: w.messageAttachments,
		body:        w.messageBody,
	}
}

// plain switches markup off on every label (CLAUDE.md rule 3).
func (l messageLabels) plain() {
	for _, lb := range []*gtk.Label{l.subject, l.from, l.recipients, l.date, l.attachments, l.body} {
		lb.SetUseMarkup(false)
	}
}

// renderHeaders shows the headers: from the summary alone, or from the full
// message when m is not nil (recipients with Cc, attachments).
func (l messageLabels) renderHeaders(s api.MessageSummary, m *api.Message) {
	from, to, date := s.From, s.To, s.Date
	var cc []api.Address
	var atts []api.Attachment
	if m != nil {
		from, to, cc, date, atts = m.From, m.To, m.CC, m.Date, m.Attachments
		s.Subject = m.Subject
	}
	l.subject.SetLabel(subjectText(s.Subject))
	var first api.Address
	if len(from) > 0 {
		first = from[0]
	}
	l.from.SetLabel(widget.FormatAddress(first))
	r := recipientsText(to, cc)
	l.recipients.SetLabel(r)
	l.recipients.SetVisible(r != "")
	if date.IsZero() {
		l.date.SetLabel("")
	} else {
		l.date.SetLabel(widget.FormatDateTime(date))
	}
	caption := attachmentsCaption(len(atts), attachmentNames(atts))
	l.attachments.SetLabel(caption)
	l.attachments.SetVisible(caption != "")
}

// renderBody shows the body, its state, or the error that prevented it.
func (l messageLabels) renderBody(b *api.MessageBodyResult, err error) {
	if err != nil {
		l.body.SetLabel(widget.RPCErrorText(i18n.T("Loading the message"), err))
		return
	}
	l.body.SetLabel(bodyText(b))
}

// render shows whatever lm holds so far: full headers once message.get
// answered, the body once message.body did. A nil lm shows the summary
// and the loading placeholder.
func (l messageLabels) render(s api.MessageSummary, lm *loadedMessage) {
	if lm == nil {
		l.renderHeaders(s, nil)
		l.body.SetLabel(i18n.T("Loading…"))
		return
	}
	l.renderHeaders(s, lm.msg)
	if lm.bodySettled() {
		l.renderBody(lm.body, lm.err)
	} else {
		l.body.SetLabel(i18n.T("Loading…"))
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

// attachmentNames lists what to call each attachment: its file name, or
// its content type when the part has none. Nameless, typeless parts are
// skipped (attachmentsCaption still counts them).
func attachmentNames(atts []api.Attachment) []string {
	names := make([]string, 0, len(atts))
	for _, a := range atts {
		switch {
		case strings.TrimSpace(a.Filename) != "":
			names = append(names, strings.TrimSpace(a.Filename))
		case strings.TrimSpace(a.ContentType) != "":
			names = append(names, strings.TrimSpace(a.ContentType))
		}
	}
	return names
}

// attachmentsCaption is the "N attachments: names" line; empty when n is 0.
func attachmentsCaption(n int, names []string) string {
	switch {
	case n <= 0:
		return ""
	case len(names) == 0:
		// TRANSLATORS: %d is the number of attachments.
		return fmt.Sprintf(i18n.N("%d attachment", "%d attachments", n), n)
	default:
		// TRANSLATORS: %d is the number of attachments, %s their names.
		return fmt.Sprintf(i18n.N("%d attachment: %s", "%d attachments: %s", n), n, strings.Join(names, ", "))
	}
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
	labels.plain()
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

// storeLoaded caches lm for id, evicting the oldest entries beyond maxLoaded.
func (w *Window) storeLoaded(id api.MessageID, lm *loadedMessage) {
	loadedSeq++
	lm.seq = loadedSeq
	w.loaded[id] = lm
	pruneLoaded(w.loaded, maxLoaded)
}

// pruneLoaded evicts the entries with the lowest seq until at most limit
// remain.
func pruneLoaded(m map[api.MessageID]*loadedMessage, limit int) {
	for len(m) > limit {
		var oldest api.MessageID
		first := true
		for id, lm := range m {
			if first || lm.seq < m[oldest].seq {
				oldest, first = id, false
			}
		}
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

// closeMessageWindow closes the stand-alone window of message id, if any
// (the message left the folder).
func (w *Window) closeMessageWindow(id api.MessageID) {
	if mw, ok := w.openMessages[id]; ok {
		mw.Close()
	}
}
