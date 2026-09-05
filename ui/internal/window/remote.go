// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/compose"
	"github.com/schotek/malachi/ui/internal/htmlview"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// Remote content and links of an HTML message: the banner that says what
// the sanitiser removed, loading those images through the daemon, trusting
// a sender, fetching inline pictures for the web view, and opening a link
// the user activated. The daemon decides everything about the content;
// this file only asks and shows.

// remoteTimeout bounds a message.body call that lets the daemon fetch
// remote images: several seconds is normal, the usual rpcTimeout is not
// enough.
const remoteTimeout = 30 * time.Second

// renderRemoteBar shows how many remote images the sanitiser removed from
// the body on display, with the buttons that load them. Tracking pixels
// are not counted: they are never loaded, there is nothing to offer. Once
// the user asked for the images the bar stays down, whatever the daemon
// could not fetch.
func renderRemoteBar(v *messageView, lm *loadedMessage) {
	n := 0
	if lm != nil && lm.body != nil && lm.body.HTML != "" && !lm.allowed {
		n = lm.body.Blocked.RemoteImages
	}
	if n > 0 {
		// TRANSLATORS: %d is the number of remote images the message tried to load.
		v.barLabel.SetLabel(fmt.Sprintf(i18n.N("%d remote image was blocked", "%d remote images were blocked", n), n))
	}
	v.bar.SetVisible(n > 0)
}

// fetchPart serves the web view's malachi-cid: pictures through
// message.part. It runs off the main loop.
func (w *Window) fetchPart(ctx context.Context, acc api.AccountID, id api.MessageID, part string) (string, []byte, error) {
	var res api.MessagePartResult
	if err := w.client.Call(ctx, api.MethodMessagePart, api.MessagePartParams{AccountID: acc, MessageID: id, PartID: part}, &res); err != nil {
		return "", nil, err
	}
	return res.ContentType, res.Data, nil
}

// loadRemoteImages fetches the body of id again with remote images allowed
// for this one call, and shows the result wherever the message is on
// display. The daemon does the fetching; the view only gets the inlined
// pictures.
func (w *Window) loadRemoteImages(id api.MessageID) {
	s, ok := w.summary(id)
	if !ok {
		return
	}
	lm := w.loaded[id]
	if lm == nil {
		lm = &loadedMessage{}
		w.storeLoaded(id, lm)
	}
	if lm.fetching {
		return
	}
	lm.fetching = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), remoteTimeout)
		defer cancel()
		var res api.MessageBodyResult
		err := w.client.Call(ctx, api.MethodMessageBody,
			api.MessageBodyParams{AccountID: s.AccountID, MessageID: id, RemoteContent: api.RemoteAllow}, &res)
		glib.IdleAdd(func() {
			lm.fetching = false
			if err != nil {
				w.log.Warn("message.body (allow)", "err", err)
				w.Toast(widget.RPCErrorText(i18n.T("Loading the images"), err))
				return
			}
			lm.body, lm.err, lm.allowed = &res, nil, true
			if w.loaded[id] == nil {
				w.storeLoaded(id, lm)
			}
			w.showLoaded(id, lm)
		})
	}()
}

// showLoaded re-renders message id wherever it is on display: the pane
// when it is the selected message, and its own window when one is open.
func (w *Window) showLoaded(id api.MessageID, lm *loadedMessage) {
	if s, ok := w.selectedMessage(); ok && s.ID == id {
		w.paneLabels().render(s, lm)
	}
	if mw, ok := w.openMessages[id]; ok {
		if s, ok := w.summary(id); ok {
			mw.show(s, lm)
		}
	}
}

// trustSender puts the sender of id on the daemon's known-senders list
// (sender.add), switches the stored remote-content preference to "from
// known senders" when it was "never" (otherwise the list would change
// nothing), and loads this message's images now.
func (w *Window) trustSender(id api.MessageID) {
	s, ok := w.summary(id)
	if !ok || len(s.From) == 0 || strings.TrimSpace(s.From[0].Address) == "" {
		return
	}
	address := strings.TrimSpace(s.From[0].Address)
	w.callThen(i18n.T("Trusting the sender"), api.MethodSenderAdd, api.SenderAddParams{Address: address}, nil, func() {
		w.ensureKnownSendersPolicy(func() { w.loadRemoteImages(id) })
	})
}

// ensureKnownSendersPolicy raises the stored remote-content preference from
// "block" to "knownSenders" (config.get, then config.set with the whole set
// echoed back) and runs done afterwards, or at once when nothing needs
// changing.
func (w *Window) ensureKnownSendersPolicy(done func()) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		var got api.ConfigGetResult
		err := w.client.Call(ctx, api.MethodConfigGet, api.ConfigGetParams{}, &got)
		if err == nil && got.Preferences.RemoteContent == api.RemoteBlock {
			want := got.Preferences
			want.RemoteContent = api.RemoteKnownSenders
			err = w.client.Call(ctx, api.MethodConfigSet, api.ConfigSetParams{Preferences: want}, nil)
		}
		glib.IdleAdd(func() {
			if err != nil {
				w.log.Warn("remote content preference", "err", err)
				w.Toast(widget.RPCErrorText(i18n.T("Changing the remote content preference"), err))
			}
			done()
		})
	}()
}

// openLink handles a link the user activated in a message: mailto: opens a
// new message, http(s) is handed to the desktop through the OpenURI portal,
// after a look at whether the link's text pretends to lead elsewhere.
// links are the body's links as the daemon listed them, text and real
// target side by side.
func (w *Window) openLink(parent *gtk.Window, uri string, links []api.Link) {
	if !htmlview.AllowedLink(uri) {
		return
	}
	if strings.HasPrefix(strings.ToLower(uri), "mailto:") {
		p, err := compose.ParseMailto(uri)
		if err != nil {
			w.log.Debug("mailto link refused", "err", err)
			return
		}
		w.compose.Open(p)
		return
	}
	text := linkTextFor(uri, links)
	if !htmlview.Masked(text, uri) {
		w.launchURI(parent, uri)
		return
	}
	// The text says one site, the target is another: say so before opening.
	d := adw.NewAlertDialog(i18n.T("Open This Link?"),
		// TRANSLATORS: %s are the link's visible text and its real destination.
		fmt.Sprintf(i18n.T("The link is shown as “%s” but leads to %s."), text, uri))
	d.SetHeadingUseMarkup(false)
	d.SetBodyUseMarkup(false)
	d.AddResponse("cancel", i18n.T("_Cancel"))
	d.AddResponse("open", i18n.T("_Open Link"))
	d.SetResponseAppearance("open", adw.ResponseSuggested)
	d.SetDefaultResponse("cancel")
	d.SetCloseResponse("cancel")
	d.ConnectResponse(func(response string) {
		if response == "open" {
			w.launchURI(parent, uri)
		}
	})
	d.Present(parent)
}

// linkTextFor is the visible text of the first link in links with this
// target, "" when the daemon listed none (the click came from somewhere
// the list does not cover).
func linkTextFor(uri string, links []api.Link) string {
	for _, l := range links {
		if l.Href == uri {
			return l.Text
		}
	}
	return ""
}

// launchURI opens uri with the desktop's handler through the OpenURI
// portal (gtk.URILauncher), never xdg-open directly.
func (w *Window) launchURI(parent *gtk.Window, uri string) {
	l := gtk.NewURILauncher(uri)
	l.Launch(context.Background(), parent, func(res gio.AsyncResulter) {
		if err := l.LaunchFinish(res); err != nil {
			w.log.Warn("open link", "err", err)
			// TRANSLATORS: %s is a technical error message.
			w.Toast(fmt.Sprintf(i18n.T("The link could not be opened: %s"), err))
		}
	})
}
