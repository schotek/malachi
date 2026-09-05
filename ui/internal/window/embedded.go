// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"fmt"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// An attached message (a message/rfc822 part, or a part named .eml) opened
// from its chip: message.embedded renders it read-only from the part's
// bytes and this window (data/ui/embedded_window.blp) shows the result. The
// daemon does the parsing and sanitising, and only when asked; its pictures
// arrive inlined, so the view needs no part server, and its own attachments
// have no part numbers, so their chips only name them. The window has no
// actions: the message has no id of its own, so there is nothing to flag,
// move or reply to.

// embeddedKey identifies an open attached-message window: the containing
// message and the part.
type embeddedKey struct {
	id   api.MessageID
	part string
}

// EmbeddedWindow shows one attached message.
type EmbeddedWindow struct {
	*adw.Window

	key embeddedKey
	acc api.AccountID

	// closed is set from close-request so a late reply is dropped.
	closed bool
	// loading is set while a message.embedded call for the images runs.
	loading bool

	title  *adw.WindowTitle
	view   *messageView
	toasts *adw.ToastOverlay
}

// attachedMessage reports whether a is a message attached to another:
// declared as one, or named as the file mail clients write one to. The
// daemon applies the same test and refuses anything else.
func attachedMessage(a api.Attachment) bool {
	ct := strings.ToLower(strings.TrimSpace(a.ContentType))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	return ct == "message/rfc822" || strings.HasSuffix(strings.ToLower(strings.TrimSpace(a.Filename)), ".eml")
}

// fetchEmbedded is message.embedded for one part. It runs off the main
// loop; the timeout allows for the daemon fetching remote images under
// allow.
func (w *Window) fetchEmbedded(ctx context.Context, acc api.AccountID, id api.MessageID, part string, policy api.RemoteContentPolicy) (*api.MessageEmbeddedResult, error) {
	var res api.MessageEmbeddedResult
	p := api.MessageEmbeddedParams{AccountID: acc, MessageID: id, PartID: part, RemoteContent: policy}
	if err := w.client.Call(ctx, api.MethodMessageEmbedded, p, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// openEmbeddedWindow shows the attached message a of message id in its own
// window, or raises the window already showing it. Nothing changes on
// screen while the daemon renders; a failure is a toast where the chip is.
func (v *messageView) openEmbeddedWindow(acc api.AccountID, id api.MessageID, a api.Attachment) {
	w := v.win
	key := embeddedKey{id: id, part: a.PartID}
	if ew, ok := w.openEmbedded[key]; ok {
		ew.Present()
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), remoteTimeout)
		defer cancel()
		res, err := w.fetchEmbedded(ctx, acc, id, a.PartID, "")
		glib.IdleAdd(func() {
			if err != nil {
				w.log.Warn("message.embedded", "part", a.PartID, "err", err)
				v.say(widget.RPCErrorText(i18n.T("Opening the attached message"), err))
				return
			}
			if ew, ok := w.openEmbedded[key]; ok {
				ew.Present() // a second click overtook the first
				return
			}
			ew := newEmbeddedWindow(w, acc, key, res)
			w.openEmbedded[key] = ew
			ew.ConnectCloseRequest(func() bool {
				ew.closed = true
				ew.view.cancelSpinner()
				delete(w.openEmbedded, key)
				return false
			})
			ew.Present()
		})
	}()
}

// newEmbeddedWindow builds the window for res. Like the other windows it
// only displays what it is given: plain labels, and the HTML view gets the
// sanitiser's output only.
func newEmbeddedWindow(w *Window, acc api.AccountID, key embeddedKey, res *api.MessageEmbeddedResult) *EmbeddedWindow {
	b := data.Builder("embedded_window.ui")
	ew := &EmbeddedWindow{
		Window: b.GetObject("embedded_window").Cast().(*adw.Window),
		key:    key,
		acc:    acc,
		title:  b.GetObject("window_title").Cast().(*adw.WindowTitle),
		toasts: b.GetObject("toast_overlay").Cast().(*adw.ToastOverlay),
	}
	ew.SetApplication(&w.app.Application)
	ew.view = newMessageView(w, &ew.Window.Window, b)
	ew.view.nested = true
	ew.view.load = ew.loadImages
	ew.view.toast = func(text string) { ew.toasts.AddToast(widget.PlainToast(text)) }
	ew.show(res)
	return ew
}

// show renders res: the attached message's subject as the title, the
// containing message's as the subtitle, then headers, body and chips
// through the shared view.
func (ew *EmbeddedWindow) show(res *api.MessageEmbeddedResult) {
	msg, body := res.Message, res.Body
	subject := subjectText(msg.Subject)
	ew.title.SetTitle(subject)
	ew.SetTitle(subject)
	if s, ok := ew.view.win.summary(ew.key.id); ok {
		// TRANSLATORS: window subtitle; %s is the subject of the message this one was attached to.
		ew.title.SetSubtitle(fmt.Sprintf(i18n.T("Attached to “%s”"), subjectText(s.Subject)))
	}
	ew.view.render(msg.MessageSummary, &loadedMessage{msg: &msg, body: &body})
}

// loadImages is the bar's Load Images: message.embedded again with remote
// images allowed for this one call, shown in place of what is on display.
func (ew *EmbeddedWindow) loadImages() {
	if ew.loading {
		return
	}
	ew.loading = true
	w := ew.view.win
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), remoteTimeout)
		defer cancel()
		res, err := w.fetchEmbedded(ctx, ew.acc, ew.key.id, ew.key.part, api.RemoteAllow)
		glib.IdleAdd(func() {
			ew.loading = false
			if ew.closed {
				return
			}
			if err != nil {
				w.log.Warn("message.embedded (allow)", "part", ew.key.part, "err", err)
				ew.view.say(widget.RPCErrorText(i18n.T("Loading the images"), err))
				return
			}
			ew.show(res)
		})
	}()
}

// closeEmbeddedWindows closes the attached-message windows opened from
// message id (the message left the folder).
func (w *Window) closeEmbeddedWindows(id api.MessageID) {
	for key, ew := range w.openEmbedded {
		if key.id == id {
			ew.Close()
		}
	}
}
