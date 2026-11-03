// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"slices"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/signature"
)

// Signed mail in the GTK client (phase 1 of docs/plans/signatures.md,
// §6 and §11.3): the badge in a message's header — the reading pane, the
// message window and a conversation card, never a chat bubble or an
// attached message —, what notify.signaturesChanged refreshes, and the
// signature part the attachment tiles leave out (chipAttachments). The
// daemon verifies and decides; the texts, icons and rules are the pure
// reference ui/internal/signature, used as it is. The details dialog is
// signature_details.go, Preferences → Security preferences_security.go.
// Everything of a certificate or key is shown as plain text.

// signatureBadge is the badge: a flat button with the verdict's icon and
// sentence (signature.BadgeFor), coloured by its style class; a click
// opens the details. Hidden for a message without a verdict.
type signatureBadge struct {
	button *gtk.Button
	icon   *gtk.Image
	label  *gtk.Label
	// style is the class icon and label carry now ("" none).
	style string
}

// newSignatureBadge makes a hidden badge whose click runs open.
func newSignatureBadge(open func()) *signatureBadge {
	sb := &signatureBadge{
		button: gtk.NewButton(),
		icon:   gtk.NewImage(),
		label:  gtk.NewLabel(""),
	}
	sb.label.SetUseMarkup(false)
	sb.label.SetXAlign(0)
	sb.label.SetWrap(true)
	sb.label.SetWrapMode(pango.WrapWordChar)
	sb.icon.SetVAlign(gtk.AlignStart)
	box := gtk.NewBox(gtk.OrientationHorizontal, 6)
	box.Append(sb.icon)
	box.Append(sb.label)
	sb.button.SetChild(box)
	sb.button.AddCSSClass("flat")
	sb.button.AddCSSClass("signature-badge")
	sb.button.SetHAlign(gtk.AlignStart)
	sb.button.SetVAlign(gtk.AlignCenter)
	sb.button.SetVisible(false)
	sb.button.ConnectClicked(open)
	return sb
}

// show shows b (hidden when it is not Visible). The verdict's sentence is
// the button's accessible name too: the label is its child.
func (sb *signatureBadge) show(b signature.Badge) {
	if !b.Visible {
		sb.button.SetVisible(false)
		return
	}
	sb.icon.SetFromIconName(b.Icon)
	sb.label.SetText(b.Text)
	sb.button.SetTooltipText(b.Tooltip)
	if sb.style != b.Style {
		for _, w := range []interface {
			AddCSSClass(string)
			RemoveCSSClass(string)
		}{sb.icon, sb.label} {
			if sb.style != "" {
				w.RemoveCSSClass(sb.style)
			}
			if b.Style != "" {
				w.AddCSSClass(b.Style)
			}
		}
		sb.style = b.Style
	}
	sb.button.SetVisible(true)
}

// shown reports the badge's text while it shows ("" hidden; tests).
func (sb *signatureBadge) shown() string {
	if sb == nil || !sb.button.Visible() {
		return ""
	}
	return sb.label.Text()
}

// shownSignature is the verdict a header shows for s as lm holds it:
// message.get's summary (with the signer) once it answered; before that
// nothing, so the badge does not change its sentence a moment later;
// after a failed message.get the list's verdict alone (kind and status,
// no signer). message.embedded never carries one (plan §6).
func shownSignature(s api.MessageSummary, lm *loadedMessage) *api.SignatureSummary {
	if lm != nil && lm.msg != nil {
		return lm.msg.Signature
	}
	if s.Signature == nil || lm == nil || lm.getting || lm.getErr == nil {
		return nil
	}
	return &api.SignatureSummary{Kind: s.Signature.Kind, Status: s.Signature.Status}
}

// fromAddress is the From address a mismatch's explanation names: the
// full message's, else the summary's ("" without one).
func fromAddress(s api.MessageSummary, lm *loadedMessage) string {
	from := s.From
	if lm != nil && lm.msg != nil {
		from = lm.msg.From
	}
	if len(from) == 0 {
		return ""
	}
	return from[0].Address
}

// bindSignatureBadge puts the badge under the date of a message display
// that has one: the reading pane and the message window. The view of an
// attached message (no invitation slot, nested) has none.
func (v *messageView) bindSignatureBadge() {
	if v.invitation == nil {
		return
	}
	box, ok := v.date.Parent().(*gtk.Box)
	if !ok {
		return
	}
	v.sigBadge = newSignatureBadge(func() {
		v.win.showSignatureDetails(v.parent, v.shown, v.shownLoaded)
	})
	box.InsertChildAfter(v.sigBadge.button, v.date)
}

// renderSignature shows the badge of s as lm holds it. A badge that goes
// away while it has the keyboard hands it to the body first (the
// selectable labels would select themselves, see newMessageView).
func (v *messageView) renderSignature(s api.MessageSummary, lm *loadedMessage) {
	if v.sigBadge == nil {
		return
	}
	b := signature.BadgeFor(shownSignature(s, lm), i18n.Tr)
	if !b.Visible && v.sigBadge.button.HasFocus() {
		v.stack.GrabFocus()
	}
	v.sigBadge.show(b)
}

// tileAttachments are the parts of m the attachment tiles may show: the
// detached signature part of a message with a verdict is not one of them
// (plan S15); the tiles' other rules follow (chipAttachments).
func tileAttachments(m *api.Message) []api.Attachment {
	return signature.TileAttachments(m.Attachments, m.Signature)
}

// signaturesChanged follows notify.signaturesChanged: trust changed (a
// key imported, confirmed or removed, a revocation check). Every cached
// message that has a verdict is asked for again (message.get) and shown
// again wherever it is on display (showLoaded: the pane, its windows, the
// conversation cards); an open details dialog asks message.signature
// again, Preferences → Security pgp.keyList (it listens itself), the
// board's shown conversation board.get. The rows whose verdict changed
// arrive as notify.messagesChanged.
func (w *Window) signaturesChanged() {
	for id, lm := range w.loaded {
		if lm.msg == nil || lm.msg.Signature == nil || lm.getting || lm.account == "" {
			continue
		}
		w.refreshMessageGet(lm.account, id, lm)
	}
	if d := sigDialogs[w]; d != nil {
		d.load()
	}
	// The board's cards carry board.get's verdicts, not the cache's.
	w.boardVerdictsChanged("")
}

// signatureListeners hear notify.signaturesChanged: Preferences →
// Security, which is no window's (main loop only).
var (
	signatureListeners    = map[int]func(){}
	signatureListenerNext int
)

// listenSignatures adds a listener of notify.signaturesChanged; cancel
// removes it.
func listenSignatures(f func()) (cancel func()) {
	signatureListenerNext++
	id := signatureListenerNext
	signatureListeners[id] = f
	return func() { delete(signatureListeners, id) }
}

// dispatchSignatures tells the listeners, in the order they were added
// (one may cancel another while it runs).
func dispatchSignatures() {
	ids := make([]int, 0, len(signatureListeners))
	for id := range signatureListeners {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if f, ok := signatureListeners[id]; ok {
			f()
		}
	}
}

// refreshMessageGet asks message.get for id again and keeps the answer in
// lm (a failure keeps what lm had and is only logged), then shows lm
// again.
func (w *Window) refreshMessageGet(acc api.AccountID, id api.MessageID, lm *loadedMessage) {
	if w.client == nil {
		return
	}
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
				lm.getErr = nil
			}
			// Waiters that joined meanwhile hear the answer, as from
			// fetchMessage.
			w.settleLoaded(id, lm)
			if err == nil {
				w.showLoaded(id, lm)
			}
		})
	}()
}
