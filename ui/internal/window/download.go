// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"errors"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Attachments on demand. Under Preferences.AttachmentOfflineDays the large
// attachments of older messages stay on the mail server (Attachment.Remote),
// under Preferences.NeverStoreAttachments all of them, and a message whose
// body the syncer has not downloaded yet has no parts on this computer at
// all. Opening, previewing or saving such a part, Save
// All, an attached message and a forward therefore first ask the daemon to
// fetch the whole message (message.download), then do what was asked. The
// daemon decides what is stored and fetches only on these clicks; the UI
// asks and shows the wait: the chips of the message trade the server icon
// for a spinner.

// downloadTimeout bounds one message.download: the daemon gives itself 4
// minutes and asks its clients to wait at least 5 (docs/api.md).
const downloadTimeout = 5 * time.Minute

// downloadCall is one message.download in flight. Callers for the same
// message wait for it rather than asking again.
type downloadCall struct {
	done chan struct{} // closed once msg or err is set
	msg  *api.Message  // the message after the download; read-only
	err  error
}

// download makes message id complete on this computer (message.download)
// and returns it as the daemon reports it afterwards. A call for a message
// whose download is running joins that one. It blocks, so it runs off the
// main loop; the call has a context of its own, so no caller's deadline
// cuts the download short for the others. The chips of the message show
// the wait (beginDownload, endDownload).
func (w *Window) download(acc api.AccountID, id api.MessageID) (*api.Message, error) {
	w.downloadsMu.Lock()
	c, joined := w.downloads[id]
	if !joined {
		c = &downloadCall{done: make(chan struct{})}
		w.downloads[id] = c
	}
	w.downloadsMu.Unlock()
	if joined {
		<-c.done
		return c.msg, c.err
	}

	glib.IdleAdd(func() { w.beginDownload(id) })
	ctx, cancel := context.WithTimeout(context.Background(), downloadTimeout)
	var res api.MessageDownloadResult
	err := w.client.Call(ctx, api.MethodMessageDownload, api.MessageDownloadParams{AccountID: acc, MessageID: id}, &res)
	cancel()
	if err != nil {
		w.log.Warn("message.download", "err", err)
		c.err = err
	} else {
		c.msg = &res.Message
	}
	// Queued before the call is forgotten, so that a download of the same
	// message starting right after cannot have its spinner cleared by
	// this one's end.
	glib.IdleAdd(func() { w.endDownload(acc, id, c.msg) })
	w.downloadsMu.Lock()
	delete(w.downloads, id)
	w.downloadsMu.Unlock()
	close(c.done)
	return c.msg, c.err
}

// beginDownload arms the spinner of message id's chips. Like the body's it
// appears only once the wait is long enough to notice (bodySpinnerDelay):
// a message with nothing missing answers at once. Main loop.
func (w *Window) beginDownload(id api.MessageID) {
	if w.spinning[id] || w.spinTimers[id] != 0 {
		return
	}
	w.spinTimers[id] = glib.TimeoutAdd(bodySpinnerDelay, func() bool {
		delete(w.spinTimers, id)
		w.spinning[id] = true
		w.refreshChips(id)
		return false
	})
}

// endDownload ends the wait for message id on the main loop. After a
// success m, the message as the daemon reports it now, replaces the cached
// one: none of its attachments is remote any more (under
// neverStoreAttachments they stay remote, held in the daemon's memory for
// a while, and the next action downloads again), and on Microsoft 365
// their part ids may have changed. The chips are rebuilt wherever the
// message is shown; a body that had not been downloaded is fetched again
// and shown, and so is one that counts pictures on the mail server only
// (reloadAfterDownload), which the daemon holds now: they show and the
// pictures bar goes, whatever the download was for.
func (w *Window) endDownload(acc api.AccountID, id api.MessageID, m *api.Message) {
	if t := w.spinTimers[id]; t != 0 {
		glib.SourceRemove(t)
		delete(w.spinTimers, id)
	}
	delete(w.spinning, id)
	if lm := w.loaded[id]; lm != nil && m != nil {
		msg := *m // the callers of download read m off the main loop
		lm.msg = &msg
		// Pictures that go missing from now on may ask for the body once
		// more (recheckPictures).
		lm.picturesRechecked = false
		if b := lm.otherBody; b != nil && b.BodyState != api.BodyFetched {
			lm.otherBody = nil // fetched again when switched to
		}
		switch {
		case lm.body != nil && lm.body.BodyState != api.BodyFetched && !lm.fetching:
			lm.body, lm.err = nil, nil
			w.fetchMessage(acc, id, func(lm *loadedMessage) { w.showLoaded(id, lm) })
		case reloadAfterDownload(lm):
			w.reloadPictures(acc, id, lm)
		}
	}
	w.refreshChips(id)
}

// refreshChips rebuilds the attachment chips of message id wherever it is
// shown (the pane, its own window), leaving the body alone. A view whose
// message the cache no longer holds (it can drop out while a download
// runs) redraws from what it last rendered, so the spinner never stays.
func (w *Window) refreshChips(id api.MessageID) {
	lm := w.loaded[id]
	w.conversationRefreshChips(id, lm)
	if s, ok := w.selectedMessage(); ok && s.ID == id && !w.conversationShown() {
		w.pane.refreshChips(id, lm)
	}
	if mw, ok := w.openMessages[id]; ok {
		mw.view.refreshChips(id, lm)
	}
}

// refreshChips redraws the chips of the view when it shows message id:
// from lm, or from the cache entry it last rendered when lm is nil.
func (v *messageView) refreshChips(id api.MessageID, lm *loadedMessage) {
	if v.shown.ID != id {
		return
	}
	if lm == nil {
		lm = v.shownLoaded
	}
	v.shownLoaded = lm
	v.renderAttachments(v.shown, lm)
}

// partData is message.part for attachment a of message id: downloading the
// message first when a is on the mail server only (remote, the chip's
// partRemote), or once the daemon answers that it is (withDownload). Off
// the main loop.
func (w *Window) partData(acc api.AccountID, id api.MessageID, a api.Attachment, remote bool) (*api.MessagePartResult, error) {
	return withDownload(a, remote, func(a api.Attachment) (*api.MessagePartResult, error) {
		ctx, cancel := context.WithTimeout(context.Background(), partTimeout)
		defer cancel()
		return w.fetchAttachment(ctx, acc, id, a.PartID)
	}, func() (*api.Message, error) { return w.download(acc, id) })
}

// embeddedData is message.embedded for the attached message a of message
// id under policy, downloading the message first as partData does. Off the
// main loop.
func (w *Window) embeddedData(acc api.AccountID, id api.MessageID, a api.Attachment, remote bool, policy api.RemoteContentPolicy) (*api.MessageEmbeddedResult, error) {
	return withDownload(a, remote, func(a api.Attachment) (*api.MessageEmbeddedResult, error) {
		// The daemon may fetch remote images first under allow.
		ctx, cancel := context.WithTimeout(context.Background(), remoteTimeout)
		defer cancel()
		return w.fetchEmbedded(ctx, acc, id, a.PartID, policy)
	}, func() (*api.Message, error) { return w.download(acc, id) })
}

// errPartNotFound is what partData and embeddedData fail with when the
// downloaded message no longer lists attachment a (partAfterDownload):
// nothing is fetched, since an old part id may name another file by now,
// and the action's toast says that the attachment no longer exists
// (partNotFound in widget.RPCErrorText).
var errPartNotFound error = api.NewError(api.CodePartNotFound, "the downloaded message no longer lists the part")

// withDownload runs fetch for attachment a of a message. When a is known
// to be on the mail server only (remote) the message is downloaded first;
// otherwise fetch runs at once, and only when the daemon answers that the
// part is not on this computer (it went to the server since the chip was
// built) is the message downloaded and fetch run once more. After a
// download fetch gets a as the downloaded message lists it
// (partAfterDownload), or does not run at all when that message no longer
// lists it (errPartNotFound). Any other error is returned as it is: never
// more than one download and one retry. download returns the message
// after the download (nil when unknown).
func withDownload[T any](a api.Attachment, remote bool, fetch func(api.Attachment) (T, error), download func() (*api.Message, error)) (T, error) {
	var zero T
	if !remote {
		v, err := fetch(a)
		if !partNotDownloaded(err) {
			return v, err
		}
	}
	m, err := download()
	if err != nil {
		return zero, err
	}
	now, ok := partAfterDownload(a, m)
	if !ok {
		return zero, errPartNotFound
	}
	return fetch(now)
}

// partNotDownloaded reports the daemon's answer for a part kept on the mail
// server only (message.part, message.embedded).
func partNotDownloaded(err error) bool {
	var e *api.Error
	return errors.As(err, &e) && e.Code == api.CodePartNotDownloaded
}

// methodUnsupported reports a daemon that does not offer a method: an
// older one (methodNotFound) or one without it yet (notImplemented).
func methodUnsupported(err error) bool {
	var e *api.Error
	return errors.As(err, &e) && (e.Code == api.CodeMethodNotFound || e.Code == api.CodeNotImplemented)
}

// partAfterDownload is attachment a as the downloaded message m lists it.
// A Microsoft 365 server rebuilds a message it serves again, so part ids
// may change (docs/api.md, message.download): the part under a's id counts
// while it is still the same file (name and type), otherwise the one part
// of m with a's name and type. ok is false when m has no such part or
// several: a's old id may name another file by now, so nothing may be
// fetched under it (the MCP bridge refuses the same way). Without m
// (nothing was downloaded), a as it is.
func partAfterDownload(a api.Attachment, m *api.Message) (api.Attachment, bool) {
	if m == nil {
		return a, true
	}
	same := func(b api.Attachment) bool { return b.Filename == a.Filename && b.ContentType == a.ContentType }
	var match api.Attachment
	n := 0
	for _, b := range m.Attachments {
		if !same(b) {
			continue
		}
		if b.PartID == a.PartID {
			return b, true
		}
		match = b
		n++
	}
	if n == 1 {
		return match, true
	}
	return api.Attachment{}, false
}
