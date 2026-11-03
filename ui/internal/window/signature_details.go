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
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/signature"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The Signature Details dialog (signature_details.blp; plan §6 and
// §11.3, the macOS popover's and the Windows flyout's GTK counterpart):
// opened from a header's badge, it shows what the badge's verdict carries
// at once and asks message.signature for the details; the buttons are
// Check Revocation (message.signatureRevocation, 20 s), Import Key
// (pgp.keyImport of the key the message offers) and Confirm Key…
// (pgp.keyVerify after ConfirmKeyQuestion). One dialog per main window;
// notify.signaturesChanged asks message.signature again (signature.go).
// The daemon's notifications refresh the rest after a button's work.

// signatureRevocationTimeout is the client's timeout of
// message.signatureRevocation: the daemon's check takes at most
// api.RevocationTimeout (plan §11.1: clients give it 20 s).
const signatureRevocationTimeout = 20 * time.Second

// sigDialogs is the open details dialog of each main window (main loop
// only).
var sigDialogs = map[*Window]*signatureDialog{}

// signatureDialog is one open details dialog.
type signatureDialog struct {
	w  *Window
	id api.MessageID
	// from is the message's From address (a mismatch's explanation).
	from string
	// details is what the dialog shows: message.signature's answer, or
	// until then (and when it fails) the summary the badge showed.
	details *api.SignatureDetails
	// busy is the button whose work runs; problem why the last check or
	// import failed ("" none); loading that message.signature runs.
	busy    signature.Action
	problem string
	loading bool
	// gen numbers the message.signature requests: only the last answer
	// counts.
	gen    int
	closed bool

	dialog       *adw.Dialog
	scroller     *gtk.ScrolledWindow
	icon         *gtk.Image
	title        *gtk.Label
	explanation  *gtk.Label
	spinner      *adw.Spinner
	rows         *gtk.Box
	notes        *gtk.Box
	caveat       *gtk.Label
	buttons      *gtk.Box
	revocation   *gtk.Button
	revSpinner   *adw.Spinner
	importKey    *gtk.Button
	confirmKey   *gtk.Button
	revCaption   *gtk.Label
	problemLabel *gtk.Label
	// style is the class the icon and title carry now.
	style string
}

// showSignatureDetails opens the details of message s as lm holds it
// over parent (the badge's window); nothing without a verdict. A dialog
// the window had open already makes way for it.
func (w *Window) showSignatureDetails(parent *gtk.Window, s api.MessageSummary, lm *loadedMessage) {
	sum := shownSignature(s, lm)
	if sum == nil {
		return
	}
	if old := sigDialogs[w]; old != nil {
		old.dialog.ForceClose()
	}
	d := newSignatureDialog(w, s.ID, fromAddress(s, lm), sum)
	sigDialogs[w] = d
	d.dialog.Present(parent)
	d.load()
}

// newSignatureDialog builds the dialog over the summary sum of message id.
func newSignatureDialog(w *Window, id api.MessageID, from string, sum *api.SignatureSummary) *signatureDialog {
	b := data.Builder("signature_details.ui")
	get := func(name string) any { return b.GetObject(name).Cast() }
	d := &signatureDialog{
		w:            w,
		id:           id,
		from:         from,
		details:      &api.SignatureDetails{SignatureSummary: *sum, MessageID: id},
		dialog:       get("signature_dialog").(*adw.Dialog),
		scroller:     get("signature_scroller").(*gtk.ScrolledWindow),
		icon:         get("signature_status_icon").(*gtk.Image),
		title:        get("signature_status_title").(*gtk.Label),
		explanation:  get("signature_explanation").(*gtk.Label),
		spinner:      get("signature_loading").(*adw.Spinner),
		rows:         get("signature_rows").(*gtk.Box),
		notes:        get("signature_notes").(*gtk.Box),
		caveat:       get("signature_caveat").(*gtk.Label),
		buttons:      get("signature_buttons").(*gtk.Box),
		revocation:   get("signature_revocation").(*gtk.Button),
		revSpinner:   get("signature_revocation_spinner").(*adw.Spinner),
		importKey:    get("signature_import").(*gtk.Button),
		confirmKey:   get("signature_confirm").(*gtk.Button),
		revCaption:   get("signature_revocation_caption").(*gtk.Label),
		problemLabel: get("signature_problem").(*gtk.Label),
	}
	d.dialog.SetTitle(signature.DialogTitle(i18n.Tr))
	d.revocation.ConnectClicked(d.checkRevocation)
	d.importKey.ConnectClicked(d.importOffered)
	d.confirmKey.ConnectClicked(d.askConfirm)
	d.dialog.ConnectClosed(func() {
		d.closed = true
		if sigDialogs[w] == d {
			delete(sigDialogs, w)
		}
	})
	d.render()
	return d
}

// load asks message.signature for the details (again after
// notify.signaturesChanged). A failure, an older daemon
// (notImplemented) or no verdict any more keeps what the dialog shows;
// the failure is only logged.
func (d *signatureDialog) load() {
	if d.closed || d.w.client == nil {
		return
	}
	d.gen++
	gen := d.gen
	d.loading = true
	d.render()
	w, id := d.w, d.id
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		var res api.MessageSignatureResult
		err := w.client.Call(ctx, api.MethodMessageSignature, api.MessageSignatureParams{MessageID: id}, &res)
		glib.IdleAdd(func() {
			if d.closed || gen != d.gen {
				return
			}
			d.loading = false
			if err != nil {
				w.log.Info("message.signature failed", "err", err)
			} else if res.Signature != nil {
				d.details = res.Signature
			}
			d.render()
		})
	}()
}

// render draws the dialog from signature.DetailsFor: the heading, the
// rows and sections (rebuilt), the notes, the caveat, the buttons (kept,
// so the keyboard stays on them) and the problem line.
func (d *signatureDialog) render() {
	m := signature.DetailsFor(d.details, d.from, d.busy, widget.FormatDateTime, i18n.Tr)
	d.icon.SetFromIconName(m.Icon)
	d.title.SetText(m.Title)
	if d.style != m.Style {
		for _, x := range []interface {
			AddCSSClass(string)
			RemoveCSSClass(string)
		}{d.icon, d.title} {
			if d.style != "" {
				x.RemoveCSSClass(d.style)
			}
			if m.Style != "" {
				x.AddCSSClass(m.Style)
			}
		}
		d.style = m.Style
	}
	d.explanation.SetText(m.Explanation)
	d.explanation.SetVisible(m.Explanation != "")
	d.spinner.SetVisible(d.loading)

	removeChildren(d.rows)
	if len(m.Rows) > 0 {
		d.rows.Append(signatureGrid(m.Rows))
	}
	for _, sec := range m.Sections {
		box := gtk.NewBox(gtk.OrientationVertical, 6)
		heading := gtk.NewLabel(sec.Title)
		heading.SetUseMarkup(false)
		heading.SetXAlign(0)
		heading.AddCSSClass("heading")
		box.Append(heading)
		box.Append(signatureGrid(sec.Rows))
		d.rows.Append(box)
	}
	d.rows.SetVisible(len(m.Rows)+len(m.Sections) > 0)

	removeChildren(d.notes)
	for _, n := range m.Notes {
		l := gtk.NewLabel(n)
		l.SetUseMarkup(false)
		l.SetXAlign(0)
		l.SetWrap(true)
		l.SetWrapMode(pango.WrapWordChar)
		d.notes.Append(l)
	}
	d.notes.SetVisible(len(m.Notes) > 0)

	d.caveat.SetText(m.Caveat)
	d.caveat.SetVisible(m.Caveat != "")

	shown := map[signature.Action]*gtk.Button{
		signature.ActionCheckRevocation: d.revocation,
		signature.ActionImportKey:       d.importKey,
		signature.ActionConfirmKey:      d.confirmKey,
	}
	offered := map[signature.Action]signature.Button{}
	for _, b := range m.Buttons {
		offered[b.Action] = b
	}
	for action, button := range shown {
		b, ok := offered[action]
		if (!ok || !b.Enabled) && button.HasFocus() {
			// A button that goes away or waits gives the keyboard to the
			// scroller, not to a selectable value (which would select all
			// of its text).
			d.scroller.GrabFocus()
		}
		button.SetVisible(ok)
		if ok {
			button.SetLabel(b.Label)
			button.SetSensitive(b.Enabled && d.busy == signature.ActionNone)
		}
	}
	d.revSpinner.SetVisible(d.busy == signature.ActionCheckRevocation)
	d.buttons.SetVisible(len(m.Buttons) > 0)
	d.revCaption.SetText(m.RevocationCaption)
	d.revCaption.SetVisible(m.RevocationCaption != "")

	d.problemLabel.SetText(d.problem)
	d.problemLabel.SetVisible(d.problem != "")
}

// signatureGrid lays rows out as label and value; the values are plain,
// selectable text (not focusable: the keyboard would select all of it),
// a fingerprint in a monospace face, the user IDs on lines of their own.
func signatureGrid(rows []signature.Row) *gtk.Grid {
	g := gtk.NewGrid()
	g.SetColumnSpacing(12)
	g.SetRowSpacing(6)
	for i, r := range rows {
		k := gtk.NewLabel(r.Label)
		k.SetUseMarkup(false)
		k.SetXAlign(0)
		k.SetYAlign(0)
		k.SetVAlign(gtk.AlignStart)
		k.SetWrap(true)
		k.SetWrapMode(pango.WrapWordChar)
		k.SetMaxWidthChars(18)
		k.AddCSSClass("dim-label")
		v := gtk.NewLabel(r.Value)
		v.SetUseMarkup(false)
		v.SetXAlign(0)
		v.SetHExpand(true)
		v.SetWrap(true)
		v.SetWrapMode(pango.WrapWordChar)
		v.SetSelectable(true)
		v.SetFocusable(false)
		if r.Monospace {
			v.AddCSSClass("monospace")
		}
		g.Attach(k, 0, i, 1, 1)
		g.Attach(v, 1, i, 1, 1)
	}
	return g
}

// removeChildren empties box.
func removeChildren(box *gtk.Box) {
	for c := box.FirstChild(); c != nil; c = box.FirstChild() {
		box.Remove(c)
	}
}

// start marks action as running: the buttons wait (their keyboard goes to
// the scroller, render), the problem line goes.
func (d *signatureDialog) start(action signature.Action) bool {
	if d.closed || d.busy != signature.ActionNone || d.w.client == nil {
		return false
	}
	d.busy, d.problem = action, ""
	d.render()
	return true
}

// checkRevocation is Check Revocation: message.signatureRevocation with
// its 20 s, the spinner meanwhile; the answer is the new details, a
// failure the reference's sentence for its class
// (RevocationFailedText, its general one for any other error).
func (d *signatureDialog) checkRevocation() {
	if !signature.CanCheckRevocation(d.details) || !d.start(signature.ActionCheckRevocation) {
		return
	}
	w, id := d.w, d.id
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), signatureRevocationTimeout)
		defer cancel()
		var res api.MessageSignatureResult
		err := w.client.Call(ctx, api.MethodMessageSignatureRevocation, api.MessageSignatureRevocationParams{MessageID: id}, &res)
		glib.IdleAdd(func() {
			if d.closed {
				return
			}
			d.busy = signature.ActionNone
			switch {
			case err != nil:
				w.log.Info("message.signatureRevocation failed", "reason", string(signature.RevocationFailureOf(err)))
				d.problem = signature.RevocationFailedText(signature.RevocationFailureOf(err), i18n.Tr)
			case res.Signature != nil:
				d.gen++ // a message.signature in flight is older
				d.loading = false
				d.details = res.Signature
			}
			d.render()
		})
	}()
}

// importOffered is Import Key: the key the message offers
// (signature.ImportParams). The toast says what was imported; the daemon's
// notify.signaturesChanged brings the new verdict (load asks at once too).
func (d *signatureDialog) importOffered() {
	params, ok := signature.ImportParams(d.details)
	if !ok || !d.start(signature.ActionImportKey) {
		return
	}
	w := d.w
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		var res api.PGPKeyImportResult
		err := w.client.Call(ctx, api.MethodPGPKeyImport, params, &res)
		glib.IdleAdd(func() {
			if d.closed {
				return
			}
			d.busy = signature.ActionNone
			if err != nil {
				w.log.Info("pgp.keyImport failed", "reason", string(signature.ImportFailureOf(err)))
				d.problem = signature.ImportFailedText(signature.ImportFailureOf(err), i18n.Tr)
				d.render()
				return
			}
			w.Toast(signature.ImportedText(res, i18n.Tr))
			d.load()
		})
	}()
}

// askConfirm is Confirm Key…: ConfirmKeyQuestion with the fingerprint
// under its text, then pgp.keyVerify {verified: true}.
func (d *signatureDialog) askConfirm() {
	params, ok := signature.ConfirmParams(d.details)
	if !ok || d.closed || d.busy != signature.ActionNone {
		return
	}
	q := signature.ConfirmKeyQuestion(i18n.Tr)
	askSignatureQuestion(d.dialog, q, signature.FormatFingerprint(params.Fingerprint), func() {
		if !d.start(signature.ActionConfirmKey) {
			return
		}
		w := d.w
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
			defer cancel()
			err := w.client.Call(ctx, api.MethodPGPKeyVerify, params, nil)
			glib.IdleAdd(func() {
				if d.closed {
					return
				}
				d.busy = signature.ActionNone
				if err != nil {
					w.log.Info("pgp.keyVerify failed", "err", err)
					d.problem = keyActionFailedText(signature.ActionConfirmKey)
					d.render()
					return
				}
				w.Toast(signature.KeyConfirmedText(i18n.Tr))
				d.load()
			})
		}()
	})
}

// askSignatureQuestion asks q over parent: Cancel is the default and the
// close response, Accept destructive when q says so; a fingerprint (""
// none) shows under the body in a monospace face. accept runs only when
// the user accepts.
func askSignatureQuestion(parent gtk.Widgetter, q signature.Question, fingerprint string, accept func()) {
	dlg := adw.NewAlertDialog(q.Heading, q.Body)
	dlg.SetHeadingUseMarkup(false)
	dlg.SetBodyUseMarkup(false)
	dlg.AddResponse("cancel", q.Cancel)
	dlg.AddResponse("accept", q.Accept)
	if q.Destructive {
		dlg.SetResponseAppearance("accept", adw.ResponseDestructive)
	} else {
		dlg.SetResponseAppearance("accept", adw.ResponseSuggested)
	}
	dlg.SetDefaultResponse("cancel")
	dlg.SetCloseResponse("cancel")
	if fingerprint != "" {
		fp := gtk.NewLabel(fingerprint)
		fp.SetUseMarkup(false)
		fp.SetWrap(true)
		fp.SetWrapMode(pango.WrapWordChar)
		fp.SetSelectable(true)
		fp.SetFocusable(false)
		fp.SetJustify(gtk.JustifyCenter)
		fp.AddCSSClass("monospace")
		dlg.SetExtraChild(fp)
	}
	dlg.ConnectResponse(func(r string) {
		if r == "accept" {
			accept()
		}
	})
	dlg.Present(parent)
}

// keyActionFailedText is the reference sentence for a failed
// pgp.keyVerify or pgp.keyRemove.
func keyActionFailedText(a signature.Action) string {
	switch a {
	case signature.ActionUnconfirmKey:
		return signature.KeyVerifyFailedText(false, i18n.Tr)
	case signature.ActionRemoveKey:
		return signature.KeyRemoveFailedText(i18n.Tr)
	}
	return signature.KeyVerifyFailedText(true, i18n.Tr)
}
