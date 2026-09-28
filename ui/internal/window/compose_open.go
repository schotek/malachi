// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"errors"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

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
// quote (compose.Prefill). A forward of a message whose attachments are on
// the mail server only downloads it first (message.download), since the
// backend never forwards what it does not have; so does a reply whose
// quote would show pictures kept there.

// composeTimeout bounds draft.create: the backend re-reads the original
// and copies its pictures, which can take longer than an ordinary call.
const composeTimeout = 30 * time.Second

// openCompose opens a reply or forward of message id from the main window
// (openComposeFrom).
func (w *Window) openCompose(kind compose.Kind, id api.MessageID) {
	w.openComposeFrom(w, kind, id)
}

// openComposeFrom opens a reply or forward of message id; parent is the
// window the request came from, for the question below. The template comes
// from the backend; a second click while it is being prepared does
// nothing (one window will appear). A forward that needs the original
// downloaded first (forwardNeedsDownload) waits for message.download; when
// that fails, the user is asked whether to forward without the
// attachments (askForwardWithout), and Cancel leaves it there. A reply
// whose pictures are on the mail server only (replyNeedsDownload) waits for
// the download too, but goes on whatever its outcome. Only when the
// backend cannot answer does the window open from what the pane knows.
func (w *Window) openComposeFrom(parent gtk.Widgetter, kind compose.Kind, id api.MessageID) {
	s, ok := w.summary(id)
	if !ok || w.composing[id] {
		return
	}
	lm := w.loaded[id]
	src := composeSource(id, s, lm)
	self := w.compose.SelfAddress()
	if acc, ok := w.model.account(s.AccountID); ok {
		self = selfAddress(acc)
	}
	fallback := func() {
		p := compose.Prefill(kind, src, self)
		p.AccountID = s.AccountID
		w.compose.Open(p)
	}

	params := api.DraftCreateParams{
		AccountID:   s.AccountID,
		Mode:        kind.Mode(),
		MessageID:   id,
		Attribution: compose.Attribution(kind, src),
	}
	// create runs draft.create and opens the window; w.composing[id] is set.
	create := func() {
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
				p.Skipped = len(res.Skipped)
				w.compose.Open(p)
			})
		}()
	}

	w.composing[id] = true
	if kind != compose.KindForward {
		if !replyNeedsDownload(lm) {
			create()
			return
		}
		// The quote takes its pictures from what the daemon has: those on
		// the mail server only are downloaded first, without a question.
		// Should that fail the reply goes on all the same, and the compose
		// window says what the quote lacks (draft.create's skipped).
		go func() {
			_, _ = w.download(s.AccountID, id) // logs its own failure
			glib.IdleAdd(create)
		}()
		return
	}
	if !forwardNeedsDownload(lm) {
		create()
		return
	}
	go func() {
		_, err := w.download(s.AccountID, id)
		glib.IdleAdd(func() {
			if !askForwardWithout(err) {
				create()
				return
			}
			// The dialog has no answer for Cancel: the request ends here,
			// and a confirmation starts it again.
			delete(w.composing, id)
			if !gtk.BaseWidget(parent).Mapped() {
				parent = w // its message window was closed meanwhile
			}
			widget.ConfirmDestructive(parent, i18n.T("Forward Without Attachments?"),
				widget.RPCErrorText(i18n.T("Downloading the attachments"), err),
				i18n.T("_Forward Without Attachments"), func() {
					if w.composing[id] {
						return
					}
					w.composing[id] = true
					create()
				})
		})
	}()
}

// forwardNeedsDownload reports whether a forward of the message lm holds
// should download it first: an attachment is on the mail server only (an
// inline picture too: under neverStoreAttachments the large ones the HTML
// shows stay there, and the forward would lose them), or the body has not
// been downloaded yet (draft.create would forward nothing of it). Without the full message in the cache nothing is known,
// so it downloads as well: message.download answers at once when nothing
// is missing.
func forwardNeedsDownload(lm *loadedMessage) bool {
	if lm == nil || lm.msg == nil {
		return true
	}
	if lm.body != nil && lm.body.BodyState == api.BodyPending {
		return true
	}
	for _, a := range lm.msg.Attachments {
		if a.Remote {
			return true
		}
	}
	return false
}

// replyNeedsDownload reports whether a reply (or reply all) to the message
// lm holds should download it first: the body on display counts pictures
// the quote would show that are on the mail server only (remotePictures;
// under neverStoreAttachments the large ones stay there). The count is the
// daemon's: a part on the server with a Content-ID is no reason by itself
// (Outlook and Apple Mail give ordinary attachments one, and a quote never
// copies those), nor is a picture the daemon holds in memory from an
// earlier download. Without a body in the cache nothing is known and the
// reply goes on at once: draft.create lists what it could not take.
func replyNeedsDownload(lm *loadedMessage) bool {
	return lm != nil && remotePictures(lm.body) > 0
}

// askForwardWithout reports whether a failed download before a forward is
// a question for the user (forward without the attachments, or not at
// all). The forward goes on at once with what the daemon has where asking
// would change nothing: without a daemon to ask (draft.create fails the
// same way and the window opens from what the pane knows), for a daemon
// that cannot download (methodNotFound, notImplemented), as before
// attachments on demand, and for a message over the daemon's cap, which
// can never be downloaded (attachmentTooBig; draft.create lists what it
// could not take).
func askForwardWithout(err error) bool {
	var e *api.Error
	switch {
	case err == nil, errors.Is(err, client.ErrDisconnected), methodUnsupported(err):
		return false
	case errors.As(err, &e) && e.Code == api.CodeAttachmentTooBig:
		return false
	}
	return true
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
