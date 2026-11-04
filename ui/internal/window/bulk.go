// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/bulkmail"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// Bulk mail: the strip above a newsletter, mailing-list or automated
// message with the Unsubscribe button, the confirmation before anything
// happens, and the call to the daemon. The daemon classifies and does the
// unsubscribing (message.unsubscribe); every decision about texts and
// buttons is ui/internal/bulkmail's, this file only shows it. All text is
// plain: the strings come from the mail.

// unsubscribeTimeout bounds message.unsubscribe: the daemon may verify the
// message and talk to the sender's server (15 s) first.
const unsubscribeTimeout = 30 * time.Second

// bulkIcons is the symbolic icon of each kind of strip.
var bulkIcons = map[bulkmail.StripKind]string{
	bulkmail.StripNewsletter:   "mail-send-symbolic",
	bulkmail.StripList:         "system-users-symbolic",
	bulkmail.StripAutomated:    "applications-system-symbolic",
	bulkmail.StripJunk:         "dialog-warning-symbolic",
	bulkmail.StripUnsubscribed: "object-select-symbolic",
}

// bulkMessage is the message the strip is decided from: the cached full
// message when message.get answered, else the summary alone (the strip
// shows at once, the button follows with the offer).
func bulkMessage(s api.MessageSummary, lm *loadedMessage) *api.Message {
	if lm == nil || lm.msg == nil {
		return &api.Message{MessageSummary: s}
	}
	if lm.msg.Bulk == nil && s.Bulk != nil {
		m := *lm.msg
		m.Bulk = s.Bulk
		return &m
	}
	return lm.msg
}

// wantsOffer reports a summary whose card needs message.get for the
// unsubscribe offer.
func wantsOffer(s api.MessageSummary) bool {
	return s.Bulk != nil && (s.Bulk.Kind == api.BulkNewsletter || s.Bulk.Kind == api.BulkList)
}

// bulkStripFor is the strip of message s as lm holds it, from the folder
// it lies in.
func (w *Window) bulkStripFor(s api.MessageSummary, lm *loadedMessage) bulkmail.Strip {
	role := w.model.folderRole(folderKey{Account: s.AccountID, Folder: s.FolderID})
	return bulkmail.StripFor(bulkMessage(s, lm), role, widget.FormatDateTime, i18n.Tr)
}

// renderBulk shows the strip of s on a view that has the bar (not an
// attached message's).
func (v *messageView) renderBulk(s api.MessageSummary, lm *loadedMessage) {
	if v.bulkBar == nil {
		return
	}
	v.showBulk(v.win.bulkStripFor(s, lm), lm != nil && lm.unsubscribing)
}

// showBulk puts the strip in the bar. busy: a request is on its way and
// the button waits for it. The focus leaves the bar before it or its
// button goes away, for the reason given in newMessageView.
func (v *messageView) showBulk(st bulkmail.Strip, busy bool) {
	if v.bulkBar == nil {
		return
	}
	if (!st.Visible() || st.Action == "") && v.bulkBar.FocusChild() != nil {
		v.stack.GrabFocus()
	}
	v.bulkBar.SetVisible(st.Visible())
	if !st.Visible() {
		return
	}
	v.bulkLabel.SetLabel(st.Text)
	v.bulkIcon.SetFromIconName(bulkIcons[st.Kind])
	if st.Warning {
		v.bulkBar.AddCSSClass("bulk-warning")
	} else {
		v.bulkBar.RemoveCSSClass("bulk-warning")
	}
	v.bulkAction.SetVisible(st.Action != "")
	v.bulkAction.SetLabel(st.Action)
	v.bulkAction.SetSensitive(!busy)
}

// bindBulk wires the button of a bar that was built or found; the owner
// supplies v.bulk.
func (v *messageView) bindBulk() {
	v.bulkLabel.SetUseMarkup(false)
	v.bulkAction.SetUseUnderline(true)
	// The strip changes when the request is done; a click leaves the focus
	// where it was (newMessageView).
	v.bulkAction.SetFocusOnClick(false)
	v.bulkAction.ConnectClicked(func() {
		if v.bulk != nil {
			v.bulk()
		}
	})
}

// bulkSay shows text as a toast in the window that owns the view.
func (v *messageView) bulkSay(text string) {
	if v.toast != nil {
		v.toast(text)
		return
	}
	v.win.Toast(text)
}

// makeBulkBar builds the strip of a conversation card as window.blp's
// bulk_bar, once, as the first of the card's bars.
func (c *convCard) makeBulkBar() {
	v := c.mv
	if v.bulkBar != nil {
		return
	}
	bar, label, _ := convBar()
	bar.AddCSSClass("bulk-bar")
	label.SetWrapMode(pango.WrapWordChar)
	v.bulkBar, v.bulkLabel = bar, label
	v.bulkIcon = gtk.NewImage()
	v.bulkIcon.SetVAlign(gtk.AlignCenter)
	bar.Prepend(v.bulkIcon)
	v.bulkAction = gtk.NewButton()
	bar.Append(v.bulkAction)
	v.bindBulk()
	c.bars.Prepend(bar)
}

// renderBulk redraws the card's strip; the bar is made when first needed.
func (c *convCard) renderBulk(lm *loadedMessage) {
	st := c.cv.w.bulkStripFor(c.s, lm)
	if !st.Visible() && c.mv.bulkBar == nil {
		return
	}
	c.makeBulkBar()
	c.mv.showBulk(st, lm != nil && lm.unsubscribing)
}

// refreshBulk redraws the strip of message id wherever it is on display.
func (w *Window) refreshBulk(id api.MessageID, lm *loadedMessage) {
	w.conversationRefreshBars(id, lm)
	if s, ok := w.selectedMessage(); ok && s.ID == id && !w.conversationShown() {
		w.pane.renderBulk(s, lm)
	}
	if mw, ok := w.openMessages[id]; ok {
		if s, ok := w.summary(id); ok {
			mw.view.renderBulk(s, lm)
		}
	}
}

// unsubscribe is the strip's button for message id: the confirmation
// first (nothing happens without it), then the page in the browser for a
// web-page offer, otherwise message.unsubscribe. parent hosts the dialogs;
// say shows toasts in the window the click came from.
func (w *Window) unsubscribe(parent *gtk.Window, id api.MessageID, say func(string)) {
	lm := w.loaded[id]
	if lm == nil || lm.msg == nil || lm.unsubscribing || lm.msg.Unsubscribe == nil || lm.msg.Unsubscribe.UnsubscribedAt != nil {
		return
	}
	m := bulkMessage(lm.msg.MessageSummary, lm)
	conf, ok := bulkmail.Confirm(m, i18n.Tr)
	if !ok {
		return
	}
	offer := *m.Unsubscribe
	acc := m.AccountID
	w.bulkDialog(parent, conf, func() {
		if offer.Method == api.UnsubscribeURL {
			w.openBulkPage(parent, offer.URL)
			return
		}
		w.callUnsubscribe(parent, acc, id, lm, "", say)
	})
}

// bulkDialog asks conf with Cancel and the suggested confirm response; run
// is called on confirm only.
func (w *Window) bulkDialog(parent *gtk.Window, conf bulkmail.Confirmation, run func()) {
	d := adw.NewAlertDialog(conf.Heading, conf.Body)
	d.SetHeadingUseMarkup(false)
	d.SetBodyUseMarkup(false)
	d.AddResponse("cancel", i18n.T("_Cancel"))
	d.AddResponse("go", conf.Confirm)
	d.SetResponseAppearance("go", adw.ResponseSuggested)
	d.SetDefaultResponse("go")
	d.SetCloseResponse("cancel")
	d.ConnectResponse(func(response string) {
		if response == "go" {
			run()
		}
	})
	d.Present(parent)
}

// openBulkPage opens the sender's unsubscribe page in the browser: https
// only, never anything else the mail carries.
func (w *Window) openBulkPage(parent *gtk.Window, raw string) {
	if uri, ok := bulkmail.OpenableURL(raw); ok {
		w.launchURI(parent, uri)
	}
}

// callUnsubscribe runs message.unsubscribe for message id, whose cache
// entry is lm, and shows what came of it.
func (w *Window) callUnsubscribe(parent *gtk.Window, acc api.AccountID, id api.MessageID, lm *loadedMessage, method api.UnsubscribeMethod, say func(string)) {
	lm.unsubscribing = true
	w.refreshBulk(id, lm)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), unsubscribeTimeout)
		defer cancel()
		var res api.MessageUnsubscribeResult
		err := w.client.Call(ctx, api.MethodMessageUnsubscribe,
			api.MessageUnsubscribeParams{AccountID: acc, MessageID: id, Method: method}, &res)
		glib.IdleAdd(func() {
			lm.unsubscribing = false
			// message.get may have replaced the cache entry meanwhile; the
			// answer belongs to the entry on display now.
			if cur := w.loaded[id]; cur != nil && cur != lm {
				cur.unsubscribing = false
				lm = cur
			}
			// The window the click came from may be gone; the main window
			// hosts a dialog then.
			if !parent.IsVisible() {
				parent = &w.ApplicationWindow.Window
			}
			if err != nil {
				w.log.Warn("message.unsubscribe", "err", err)
				say(widget.RPCErrorText(bulkmail.ErrorWhat(i18n.Tr), err))
				w.refreshBulk(id, lm)
				return
			}
			switch res.Outcome {
			case api.UnsubscribeDone, api.UnsubscribeQueued:
				if lm.msg != nil {
					lm.msg.Unsubscribe = bulkmail.Applied(lm.msg.Unsubscribe, res)
				}
				if res.Outcome == api.UnsubscribeQueued {
					say(bulkmail.Queued(i18n.Tr))
				}
			case api.UnsubscribeUnverified:
				w.bulkUnverified(parent, acc, id, lm, res, say)
			case api.UnsubscribeOpenURL:
				// Only for a web-page offer, confirmed already.
				w.openBulkPage(parent, res.URL)
			}
			w.refreshBulk(id, lm)
		})
	}()
}

// bulkUnverified is the dialog after the daemon sent nothing because it
// could not verify the one-click request. With a mailto: alternative the
// user may confirm sending the request by mail (message.unsubscribe again
// with that method); without, the dialog only informs.
func (w *Window) bulkUnverified(parent *gtk.Window, acc api.AccountID, id api.MessageID, lm *loadedMessage, res api.MessageUnsubscribeResult, say func(string)) {
	var m *api.Message
	if lm != nil {
		m = lm.msg
	}
	conf := bulkmail.Unverified(m, res, i18n.Tr)
	if conf.Confirm != "" {
		w.bulkDialog(parent, conf, func() {
			if lm != nil && !lm.unsubscribing {
				w.callUnsubscribe(parent, acc, id, lm, api.UnsubscribeMailto, say)
			}
		})
		return
	}
	d := adw.NewAlertDialog(conf.Heading, conf.Body)
	d.SetHeadingUseMarkup(false)
	d.SetBodyUseMarkup(false)
	d.AddResponse("close", bulkmail.Close(i18n.Tr))
	d.SetDefaultResponse("close")
	d.SetCloseResponse("close")
	d.Present(parent)
}
