// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"errors"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/compose"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// Replying and forwarding: the backend builds the template (draft.create:
// recipients, subject, the original quoted formatted with its pictures
// copied into the attachment store) and the compose window opens with it.
// Without a backend the window opens at once with the UI's own plain-text
// quote (compose.Prefill).

// composeTimeout bounds draft.create: the backend re-reads the original
// and copies its pictures, which can take longer than an ordinary call.
const composeTimeout = 30 * time.Second

// openCompose opens a reply or forward of message id. The template comes
// from the backend; a second click while it is being prepared does
// nothing (one window will appear). Only when the backend cannot answer
// does the window open from what the pane knows.
func (w *Window) openCompose(kind compose.Kind, id api.MessageID) {
	s, ok := w.summary(id)
	if !ok || w.composing[id] {
		return
	}
	src := composeSource(id, s, w.loaded[id])
	self := w.compose.SelfAddress()
	if acc, ok := w.model.account(s.AccountID); ok {
		self = selfAddress(acc)
	}
	fallback := func() {
		p := compose.Prefill(kind, src, self)
		p.AccountID = s.AccountID
		w.compose.Open(p)
	}

	w.composing[id] = true
	params := api.DraftCreateParams{
		AccountID:   s.AccountID,
		Mode:        kind.Mode(),
		MessageID:   id,
		Attribution: compose.Attribution(kind, src),
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), composeTimeout)
		defer cancel()
		var res api.DraftCreateResult
		err := w.client.Call(ctx, api.MethodDraftCreate, params, &res)
		glib.IdleAdd(func() {
			delete(w.composing, id)
			if err != nil {
				w.log.Warn("draft.create", "mode", params.Mode, "err", err)
				if text := composeFallbackText(composeWhat(kind), err); text != "" {
					w.Toast(text)
				}
				fallback()
				return
			}
			p := compose.FromDraft(kind, res.Draft, res.Blocked)
			p.AccountID = s.AccountID
			w.compose.Open(p)
		})
	}()
}

// composeSource is what the pane knows about the message: the summary,
// bettered by the full headers and the text when they were loaded.
func composeSource(id api.MessageID, s api.MessageSummary, lm *loadedMessage) compose.Source {
	src := compose.Source{ID: id, From: s.From, To: s.To, Subject: s.Subject, Date: s.Date}
	if lm == nil {
		return src
	}
	if m := lm.msg; m != nil {
		src.From, src.ReplyTo, src.To, src.CC = m.From, m.ReplyTo, m.To, m.CC
		src.Subject, src.Date = m.Subject, m.Date
	}
	if lm.body != nil && lm.body.BodyState == api.BodyFetched {
		src.Text = lm.body.Text
	}
	return src
}

// composeWhat names the action for an error toast.
func composeWhat(kind compose.Kind) string {
	if kind == compose.KindForward {
		return i18n.T("Preparing the forwarded message")
	}
	return i18n.T("Preparing the reply")
}

// composeFallbackText is the toast shown when draft.create failed and the
// window opens with the plain quote instead: nothing when there is no
// backend to ask or it does not offer the call yet (the fallback is the
// normal course then), the usual sentence otherwise.
func composeFallbackText(what string, err error) string {
	var e *api.Error
	if errors.Is(err, client.ErrDisconnected) || (errors.As(err, &e) && e.Code == api.CodeNotImplemented) {
		return ""
	}
	return widget.RPCErrorText(what, err)
}
