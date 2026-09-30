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

// Remote content and links of an HTML message: the banner that says what
// the sanitiser removed, loading those images through the daemon, trusting
// a sender, fetching inline pictures for the web view, downloading the ones
// kept on the mail server only, and opening a link the user activated. The
// daemon decides everything about the content; this file only asks and
// shows.

// remoteTimeout bounds a message.body call that lets the daemon fetch
// remote images: several seconds is normal, the usual rpcTimeout is not
// enough. That is every call without a "block" override, since a stored
// "allow" or a known sender resolves to it inside the daemon.
const remoteTimeout = 30 * time.Second

// remoteBarState is what the bar shows: nothing, how many remote images
// were blocked with the buttons that load them, or the notice that they are
// on their way.
type remoteBarState struct {
	visible bool
	loading bool
	blocked int
}

// remoteBarStateFor derives the bar from what is known about the message.
func remoteBarStateFor(lm *loadedMessage) remoteBarState {
	if lm == nil {
		return remoteBarState{}
	}
	if lm.loadingImages {
		return remoteBarState{visible: true, loading: true}
	}
	n := loadableImages(lm.body)
	return remoteBarState{visible: n > 0, blocked: n}
}

// renderRemoteBar shows how many remote images the sanitiser removed from
// the body on display, with the buttons that load them, or the spinner
// while they load.
func renderRemoteBar(v *messageView, lm *loadedMessage) {
	v.showRemoteBar(remoteBarStateFor(lm))
}

// showRemoteBar puts the bar in state st.
func (v *messageView) showRemoteBar(st remoteBarState) {
	switch {
	case st.loading:
		v.barLabel.SetLabel(i18n.T("Loading remote images…"))
	case st.blocked > 0:
		// TRANSLATORS: %d is the number of remote images the message tried to load.
		v.barLabel.SetLabel(fmt.Sprintf(i18n.N("%d remote image was blocked", "%d remote images were blocked", st.blocked), st.blocked))
	}
	v.setBarLoading(st.loading)
	v.setBarVisible(st.visible)
}

// loadableImages is how many remote images of the body could still be
// shown by asking the daemon again, which is what the bar offers.
//
// Only the block policy leaves anything to load. Under allow the daemon
// already fetched what it could, and the images the sanitiser still counts
// are the ones it removes whatever the policy: CSS url(), srcset,
// background attributes, plain http:, a download that failed. Offering to
// load those would be a button that changes nothing. Tracking pixels are
// counted separately and never loaded at all.
func loadableImages(b *api.MessageBodyResult) int {
	if b == nil || b.HTML == "" || b.RemoteContent != api.RemoteBlock {
		return 0
	}
	return b.Blocked.RemoteImages
}

// fetchPart serves the web view's malachi-cid: pictures through
// message.part, off the main loop; a failure goes by pictureFailed.
func (w *Window) fetchPart(ctx context.Context, acc api.AccountID, id api.MessageID, part string) (string, []byte, error) {
	res, err := w.fetchAttachment(ctx, acc, id, part)
	if err != nil {
		return "", nil, w.pictureFailed(acc, id, part, err)
	}
	return res.ContentType, res.Data, nil
}

// loadRemoteImages fetches the body of id again with remote images allowed
// for this one call, and shows the result wherever the message is on
// display. The daemon does the fetching; the view only gets the inlined
// pictures. The bar shows the wait from the click on: on a slow connection
// the daemon can take most of remoteTimeout, and a button that seems to do
// nothing gets clicked again.
func (w *Window) loadRemoteImages(id api.MessageID) {
	if _, ok := w.summary(id); !ok {
		return
	}
	lm := w.loadedFor(id)
	if lm.loadingImages {
		return
	}
	lm.loadingImages = true
	w.refreshRemoteBar(id, lm)
	w.fetchRemoteImages(id, lm)
}

// fetchRemoteImages is the message.body call under allow for a request the
// bar already shows as loading (lm.loadingImages); it ends the request
// either way, with the images on display or the bar back as it was and a
// toast.
func (w *Window) fetchRemoteImages(id api.MessageID, lm *loadedMessage) {
	s, ok := w.summary(id)
	if !ok {
		w.imagesDone(id, lm)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), remoteTimeout)
		defer cancel()
		var res api.MessageBodyResult
		err := w.client.Call(ctx, api.MethodMessageBody,
			api.MessageBodyParams{AccountID: s.AccountID, MessageID: id, RemoteContent: api.RemoteAllow}, &res)
		glib.IdleAdd(func() {
			if err != nil {
				w.log.Warn("message.body (allow)", "err", err)
				w.Toast(widget.RPCErrorText(i18n.T("Loading the images"), err))
				w.imagesDone(id, lm)
				return
			}
			lm.loadingImages = false
			lm.body, lm.err = &res, nil
			if w.loaded[id] == nil {
				w.storeLoaded(id, lm)
			}
			w.showLoaded(id, lm)
		})
	}()
}

// imagesDone ends a request for the images without a new body: the bar
// offers them again.
func (w *Window) imagesDone(id api.MessageID, lm *loadedMessage) {
	lm.loadingImages = false
	w.refreshRemoteBar(id, lm)
}

// refreshRemoteBar redraws the bar of message id wherever it is on display
// and leaves the body alone (showLoaded would reload the web view).
func (w *Window) refreshRemoteBar(id api.MessageID, lm *loadedMessage) {
	w.conversationRefreshBars(id, lm)
	if s, ok := w.selectedMessage(); ok && s.ID == id && !w.conversationShown() {
		renderRemoteBar(w.pane, lm)
	}
	if mw, ok := w.openMessages[id]; ok {
		renderRemoteBar(mw.view, lm)
	}
}

// showLoaded re-renders message id wherever it is on display: the pane
// when it is the selected message, and its own window when one is open.
func (w *Window) showLoaded(id api.MessageID, lm *loadedMessage) {
	w.conversationShowLoaded(id, lm)
	if s, ok := w.selectedMessage(); ok && s.ID == id && !w.conversationShown() {
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
// nothing), and loads this message's images now. The bar shows the wait
// from the click on, through all three calls.
func (w *Window) trustSender(id api.MessageID) {
	s, ok := w.summary(id)
	if !ok || len(s.From) == 0 || strings.TrimSpace(s.From[0].Address) == "" {
		return
	}
	address := strings.TrimSpace(s.From[0].Address)
	lm := w.loadedFor(id)
	if lm.loadingImages {
		return
	}
	lm.loadingImages = true
	w.refreshRemoteBar(id, lm)
	w.callThen(i18n.T("Trusting the sender"), api.MethodSenderAdd, api.SenderAddParams{Address: address},
		func() { w.imagesDone(id, lm) },
		func() { w.ensureKnownSendersPolicy(func() { w.fetchRemoteImages(id, lm) }) })
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

// openAssistantAnswerLink opens a link of an answer in the assistant panel
// (assistant_panel.go) only after "Open This Link?" has shown where it
// leads: nothing is known of the text it wore, and the answer may quote
// mail. Only http and https come here (assistant.Markdown).
func (w *Window) openAssistantAnswerLink(uri string) {
	if !htmlview.AllowedLink(uri) || strings.HasPrefix(strings.ToLower(uri), "mailto:") {
		return
	}
	d := adw.NewAlertDialog(i18n.T("Open This Link?"),
		// TRANSLATORS: %s is the link's real destination.
		fmt.Sprintf(i18n.T("This link leads to %s."), uri))
	d.SetHeadingUseMarkup(false)
	d.SetBodyUseMarkup(false)
	d.AddResponse("cancel", i18n.T("_Cancel"))
	d.AddResponse("open", i18n.T("_Open Link"))
	d.SetResponseAppearance("open", adw.ResponseSuggested)
	d.SetDefaultResponse("cancel")
	d.SetCloseResponse("cancel")
	d.ConnectResponse(func(response string) {
		if response == "open" {
			w.launchURI(&w.ApplicationWindow.Window, uri)
		}
	})
	d.Present(w)
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

// launchURI opens uri with the desktop's handler (widget.LaunchURI).
func (w *Window) launchURI(parent *gtk.Window, uri string) {
	widget.LaunchURI(parent, uri, func(err error) {
		if err != nil {
			w.log.Warn("open link", "err", err)
			w.Toast(widget.LaunchErrorText(err))
		}
	})
}

// Pictures kept on the mail server only. Under neverStoreAttachments the
// daemon does not store the pictures the HTML shows of
// api.LargeAttachmentMinBytes and more: message.body counts them
// (RemotePictures), message.part answers partNotDownloaded for them, and a
// second bar under the remote-image one offers to download them. The daemon
// then fetches the whole message into its memory (message.download), and
// the body asked for again shows them. Only that click contacts the mail
// server, never showing a message.

// picturesBarState is what the pictures bar shows: nothing, how many
// pictures of the body are on the mail server only with the button that
// downloads them, or the notice that they are on their way.
type picturesBarState struct {
	visible bool
	loading bool
	remote  int
}

// picturesBarStateFor derives the bar from what is known about the message.
func picturesBarStateFor(lm *loadedMessage) picturesBarState {
	if lm == nil {
		return picturesBarState{}
	}
	if lm.loadingPictures {
		return picturesBarState{visible: true, loading: true}
	}
	n := remotePictures(lm.body)
	return picturesBarState{visible: n > 0, remote: n}
}

// remotePictures is how many pictures of the body on display are on the
// mail server only. Only an HTML body shown as such misses any: the plain
// text has no pictures.
func remotePictures(b *api.MessageBodyResult) int {
	if !showsHTML(b) || b.RemotePictures < 0 {
		return 0
	}
	return b.RemotePictures
}

// picturesPolicy is the remote-content override for the body asked for
// again once the pictures are downloaded: allow when the remote images of
// the body on display were loaded, or are being loaded, so that the new
// body does not take them away again; otherwise none, and the stored
// preference applies as it did for the body on display.
func picturesPolicy(lm *loadedMessage) api.RemoteContentPolicy {
	if lm.loadingImages || (lm.body != nil && lm.body.RemoteContent == api.RemoteAllow) {
		return api.RemoteAllow
	}
	return ""
}

// renderPicturesBar shows how many pictures of the body on display are on
// the mail server only, with the button that downloads them, or the
// spinner while they download.
func renderPicturesBar(v *messageView, lm *loadedMessage) {
	v.showPicturesBar(picturesBarStateFor(lm))
}

// refreshPicturesBar redraws the pictures bar of message id wherever it is
// on display and leaves the body alone.
func (w *Window) refreshPicturesBar(id api.MessageID, lm *loadedMessage) {
	w.conversationRefreshBars(id, lm)
	if s, ok := w.selectedMessage(); ok && s.ID == id && !w.conversationShown() {
		renderPicturesBar(w.pane, lm)
	}
	if mw, ok := w.openMessages[id]; ok {
		renderPicturesBar(mw.view, lm)
	}
}

// downloadPictures is the bar's Download Pictures for message id: the
// message is downloaded (joining a download of it already running, so the
// chips show the wait as well), then its body is asked for again and shown
// wherever the message is on display. The bar shows the wait from the
// click on; a failure is a toast through say, in the window the click came
// from, and the bar offers the pictures again.
func (w *Window) downloadPictures(id api.MessageID, say func(string)) {
	s, ok := w.summary(id)
	if !ok {
		return
	}
	lm := w.loadedFor(id)
	if lm.loadingPictures {
		return
	}
	lm.loadingPictures = true
	w.refreshPicturesBar(id, lm)
	acc := s.AccountID
	failed := func(err error) {
		lm.loadingPictures = false
		say(widget.RPCErrorText(i18n.T("Downloading the pictures"), err))
		w.refreshPicturesBar(id, lm)
	}
	go func() {
		_, err := w.download(acc, id) // logs its own failure
		glib.IdleAdd(func() {
			if err != nil {
				failed(err)
				return
			}
			// Decided on the main loop, where the body on display is.
			policy := picturesPolicy(lm)
			go func() {
				// The policy may let the daemon fetch remote images first.
				ctx, cancel := context.WithTimeout(context.Background(), remoteTimeout)
				defer cancel()
				var res api.MessageBodyResult
				err := w.client.Call(ctx, api.MethodMessageBody,
					api.MessageBodyParams{AccountID: acc, MessageID: id, RemoteContent: policy}, &res)
				glib.IdleAdd(func() {
					if err != nil {
						w.log.Warn("message.body (pictures)", "err", err)
						failed(err)
						return
					}
					lm.loadingPictures = false
					lm.body, lm.err = &res, nil
					if w.loaded[id] == nil {
						w.storeLoaded(id, lm)
					}
					w.showLoaded(id, lm)
				})
			}()
		})
	}()
}

// showPicturesBar puts the pictures bar in state st. The focus leaves the
// bar before its button or the bar itself is hidden, for the reason given
// in newMessageView. A view without the bar (an attached message) has
// nothing to show.
func (v *messageView) showPicturesBar(st picturesBarState) {
	if v.picturesBar == nil {
		return
	}
	switch {
	case st.loading:
		v.picturesLabel.SetLabel(i18n.T("Downloading pictures…"))
	case st.remote > 0:
		// TRANSLATORS: %d is the number of pictures of the message kept on the mail server only.
		v.picturesLabel.SetLabel(fmt.Sprintf(i18n.N("%d picture of this message is on the server only", "%d pictures of this message are on the server only", st.remote), st.remote))
	}
	if (st.loading || !st.visible) && v.picturesBar.FocusChild() != nil {
		v.stack.GrabFocus()
	}
	v.picturesSpinner.SetVisible(st.loading)
	v.picturesDownload.SetVisible(!st.loading)
	v.picturesBar.SetVisible(st.visible)
}

// The count going out of date. A body cached while the daemon held the
// message in memory counts no picture on the server; once the daemon has
// dropped that copy (30 minutes unused, its memory cap, the switch turned
// off, a restart) the same body shown again asks for pictures that
// message.part answers partNotDownloaded for, and without a new count there
// would be no bar to get them back. The first such answer for a picture
// the body lists asks for the body again (store and memory only, never the
// mail server), whose count brings the bar back. The other way round, a
// download of the message for anything else (a chip, a reply, a forward)
// asks again for a body that counts pictures on the server, so that they
// show and the bar goes (endDownload).

// pictureFailed is fetchPart's failure err for picture part of message id,
// returned as it is; a partNotDownloaded goes to lostPicture on the main
// loop first. Off the main loop.
func (w *Window) pictureFailed(acc api.AccountID, id api.MessageID, part string, err error) error {
	if partNotDownloaded(err) {
		glib.IdleAdd(func() { w.lostPicture(acc, id, part) })
	}
	return err
}

// lostPicture asks for the body of message id again when picture part,
// which the cached body counts as here, turned out to be on the mail server
// only (recheckPictures). Main loop.
func (w *Window) lostPicture(acc api.AccountID, id api.MessageID, part string) {
	lm := w.loaded[id]
	if !recheckPictures(lm, part) {
		return
	}
	lm.picturesRechecked = true
	w.reloadPictures(acc, id, lm)
}

// recheckPictures reports whether a partNotDownloaded for picture part
// says that the cached body lm is out of date: the body lists the part
// among the pictures it shows (InlineParts) yet counts none on the server,
// and nothing that brings a newer body is on its way (the body itself,
// the remote images, Download Pictures). Once until the next download of
// the message (picturesRechecked): a body that still counts none while
// the daemon will not serve the picture is not asked for in a loop.
func recheckPictures(lm *loadedMessage, part string) bool {
	if lm == nil || lm.picturesRechecked || lm.fetching || lm.loadingImages || lm.loadingPictures {
		return false
	}
	if !showsHTML(lm.body) || lm.body.RemotePictures > 0 {
		return false
	}
	for _, p := range lm.body.InlineParts {
		if p == part {
			return true
		}
	}
	return false
}

// reloadAfterDownload reports whether a download of the message lm holds
// should ask for its body again: the body on display counts pictures on
// the mail server, which the daemon now holds, and neither Download
// Pictures (which asks for the body itself) nor a body is on its way.
func reloadAfterDownload(lm *loadedMessage) bool {
	return lm != nil && !lm.loadingPictures && !lm.fetching && remotePictures(lm.body) > 0
}

// reloadPictures asks for the body of message id again, under the policy
// of the body on display (picturesPolicy: remote images the user loaded
// stay), and shows it wherever the message is on display. The daemon
// answers from its store and memory. A body that replaced the one on
// display meanwhile, or Download Pictures started meanwhile, wins over the
// answer; a failure is only logged and the body on display stays. Main
// loop.
func (w *Window) reloadPictures(acc api.AccountID, id api.MessageID, lm *loadedMessage) {
	shown := lm.body
	policy := picturesPolicy(lm)
	go func() {
		// The policy may let the daemon fetch remote images first.
		ctx, cancel := context.WithTimeout(context.Background(), remoteTimeout)
		defer cancel()
		var res api.MessageBodyResult
		err := w.client.Call(ctx, api.MethodMessageBody,
			api.MessageBodyParams{AccountID: acc, MessageID: id, RemoteContent: policy}, &res)
		glib.IdleAdd(func() {
			if err != nil {
				w.log.Warn("message.body (pictures again)", "err", err)
				return
			}
			if lm.body != shown || lm.loadingPictures {
				return
			}
			lm.body, lm.err = &res, nil
			if w.loaded[id] == nil {
				w.storeLoaded(id, lm)
			}
			w.showLoaded(id, lm)
		})
	}()
}
