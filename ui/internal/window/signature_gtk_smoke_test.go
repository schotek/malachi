// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"log/slog"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/signature"
	"github.com/schotek/malachi/ui/internal/widget"
)

// Run with a display (xvfb-run, Broadway): MALACHI_GTK_SMOKE=1 go test
// ./internal/window -run 'Signature' -count=1 -v. The list row's icon,
// the header's badge, the details dialog over invented S/MIME and
// OpenPGP details and Preferences → Security over invented keys, from
// the real Blueprints; no daemon, nothing is asked or stored.

func signatureSmoke(t *testing.T) {
	t.Helper()
	if os.Getenv("MALACHI_GTK_SMOKE") != "1" {
		t.Skip("set MALACHI_GTK_SMOKE=1 with a GTK display to run the widget smoke test")
	}
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	adw.Init()
}

func TestSignatureRowAndBadgeGTKSmoke(t *testing.T) {
	signatureSmoke(t)
	row := widget.NewMessageRow()
	s := api.MessageSummary{ID: "m1", Subject: "Signed", Signature: &api.MessageSignature{Kind: api.SignatureSMIME, Status: api.SignatureValid}}
	row.SetMessage(summaryMessage(s))
	if icon, tip := row.SignatureIcon(); icon != signature.IconGood || tip == "" {
		t.Errorf("valid: %q %q", icon, tip)
	}
	s.Signature.Status = api.SignatureInvalid
	row.SetMessage(summaryMessage(s))
	if icon, _ := row.SignatureIcon(); icon != signature.IconBad {
		t.Errorf("invalid: %q", icon)
	}
	s.Signature.Status = api.SignatureWeak
	row.SetMessage(summaryMessage(s))
	if icon, _ := row.SignatureIcon(); icon != "" {
		t.Errorf("weak shows nothing in the list: %q", icon)
	}
	th := api.ThreadSummary{Subject: "t", MessageCount: 2,
		Latest: api.MessageSummary{Signature: &api.MessageSignature{Kind: api.SignaturePGP, Status: api.SignatureValidSeen}}}
	row.SetThread(summaryThread(th, false, false))
	if icon, _ := row.SignatureIcon(); icon != signature.IconGoodSeen {
		t.Errorf("thread row: %q", icon)
	}

	opened := 0
	b := newSignatureBadge(func() { opened++ })
	sum := &api.SignatureSummary{Kind: api.SignatureSMIME, Status: api.SignatureValid,
		Signer: &api.SignatureSigner{Name: "Jan <b>Novák</b>", Email: "jan@example.org"}}
	b.show(signature.BadgeFor(sum, i18n.Tr))
	if got := b.shown(); got != "Signed by Jan <b>Novák</b> <jan@example.org>" || b.label.UseMarkup() {
		t.Errorf("badge %q (markup %v): plain text, never markup", got, b.label.UseMarkup())
	}
	if !b.icon.HasCSSClass(signature.StyleGood) || !b.label.HasCSSClass(signature.StyleGood) {
		t.Error("green")
	}
	sum.Status = api.SignatureRevoked
	b.show(signature.BadgeFor(sum, i18n.Tr))
	if b.icon.HasCSSClass(signature.StyleGood) || !b.icon.HasCSSClass(signature.StyleBad) {
		t.Error("the colour follows the verdict")
	}
	b.button.Activate()
	if opened != 1 {
		t.Errorf("a click opens the details: %d", opened)
	}
	b.show(signature.BadgeFor(nil, i18n.Tr))
	if b.shown() != "" {
		t.Error("no verdict hides the badge")
	}
}

func TestSignatureDetailsGTKSmoke(t *testing.T) {
	signatureSmoke(t)
	w := &Window{log: slog.Default()}
	arrived := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	sum := &api.SignatureSummary{Kind: api.SignatureSMIME, Status: api.SignatureValid,
		Signer: &api.SignatureSigner{Name: "Jan Novák", Email: "jan@example.org", Fingerprint: "ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34"},
		Notes:  []api.SignatureNote{api.SignatureNoteRevocationUnchecked, api.SignatureNotePartialFooter}, PartID: "2"}
	d := newSignatureDialog(w, "m1", "jan@example.org", sum)
	if d.title.Text() == "" || d.revocation.Visible() {
		t.Error("the badge's summary at once, no Check Revocation without a chain")
	}
	d.details = &api.SignatureDetails{SignatureSummary: *sum, MessageID: "m1", ArrivedAt: arrived, Algorithm: "RSA 2048 / SHA-256",
		SMIME: &api.SMIMEDetails{
			Chain: []api.SignatureCertificate{
				{SHA256: sum.Signer.Fingerprint, Subject: "CN=Jan Novák", Issuer: "CN=Example CA", NotBefore: arrived.AddDate(-1, 0, 0), NotAfter: arrived.AddDate(1, 0, 0)},
				{SHA256: "cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12", Subject: "CN=Example CA", Issuer: "CN=Example CA", SystemRoot: true, NotBefore: arrived.AddDate(-5, 0, 0), NotAfter: arrived.AddDate(5, 0, 0)},
			},
			Revocation: api.SMIMERevocation{State: api.RevocationUnchecked, Checkable: true},
		}}
	d.render()
	if !d.revocation.Visible() || !d.revocation.Sensitive() || !d.revCaption.Visible() {
		t.Error("Check Revocation with its caption")
	}
	if d.importKey.Visible() || d.confirmKey.Visible() {
		t.Error("S/MIME has no key buttons")
	}
	if n := countChildren(d.rows); n != 3 {
		t.Errorf("%d blocks of rows, want the main rows and two certificates", n)
	}
	if !d.notes.Visible() || countChildren(d.notes) != 1 {
		t.Error("the footer note (Revocation says the rest)")
	}
	if !d.caveat.Visible() {
		t.Error("the caveat under a valid verdict")
	}
	// No daemon: the button does nothing, the dialog stays as it was.
	d.checkRevocation()
	if d.busy != signature.ActionNone {
		t.Error("no client, no check")
	}
	d.busy = signature.ActionCheckRevocation
	d.render()
	if d.revocation.Sensitive() || !d.revSpinner.Visible() {
		t.Error("the check waits with its spinner")
	}
	d.busy = signature.ActionNone
	d.problem = signature.RevocationFailedText(api.RevocationFailTimeout, i18n.Tr)
	d.render()
	if !d.problemLabel.Visible() || d.problemLabel.Text() != "The certificate's issuer did not answer in time." {
		t.Errorf("problem %q", d.problemLabel.Text())
	}

	pgp := newSignatureDialog(w, "m2", "eva@example.org", &api.SignatureSummary{Kind: api.SignaturePGP, Status: api.SignatureValidSeen})
	pgp.details = &api.SignatureDetails{MessageID: "m2", SignatureSummary: api.SignatureSummary{Kind: api.SignaturePGP, Status: api.SignatureValidSeen},
		PGP: &api.PGPDetails{KeyTrust: api.PGPKeySeen, PrimaryFingerprint: "0123456789abcdef0123456789abcdef01234567",
			UserIDs: []string{"Eva <eva@example.org>"}, Importable: &api.PGPKeyOffer{Fingerprint: "fedcba9876543210fedcba9876543210fedcba98", Autocrypt: true}}}
	pgp.render()
	if pgp.revocation.Visible() || !pgp.importKey.Visible() || !pgp.confirmKey.Visible() {
		t.Error("OpenPGP: Import Key and Confirm Key…")
	}
	pgp.dialog.ForceClose()
	d.dialog.ForceClose()
}

func TestSignatureSecurityPageGTKSmoke(t *testing.T) {
	signatureSmoke(t)
	d := &PreferencesDialog{PreferencesDialog: adw.NewPreferencesDialog(), log: slog.Default()}
	p := newSecurityPage(d, nil)
	p.load()
	if !p.status.Visible() || p.status.Title() != p.texts.LoadFailed || p.importButton.Sensitive() {
		t.Error("no daemon: Could not load the keys, no import")
	}
	now := time.Now()
	p.show(nil, false)
	if p.status.Title() != p.texts.Empty {
		t.Error("No OpenPGP Keys")
	}
	p.show([]api.PGPKey{
		{Fingerprint: "0123456789abcdef0123456789abcdef01234567", UserIDs: []string{"Zuzana <z@example.org>"}, Trust: api.PGPKeyVerified, Source: api.PGPSourceImport, FirstSeen: now},
		{Fingerprint: "fedcba9876543210fedcba9876543210fedcba98", UserIDs: []string{"Adam <a@example.org>"}, Trust: api.PGPKeySeen, Source: api.PGPSourceAutocrypt, FirstSeen: now},
	}, false)
	if p.status.Visible() {
		t.Error("keys hide the status row")
	}
	if got := p.keyTitles(); len(got) != 2 || got[0] != "Adam <a@example.org>" || got[1] != "Zuzana <z@example.org>" {
		t.Errorf("rows %q", got)
	}
	p.show(nil, false)
	if len(p.rows) != 0 || !p.status.Visible() {
		t.Error("the rows go with the keys")
	}
	p.closed = true
}

// countChildren counts box's children.
func countChildren(box *gtk.Box) int {
	n := 0
	for c := box.FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
		n++
	}
	return n
}
