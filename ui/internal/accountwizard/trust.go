// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package accountwizard

import (
	"fmt"
	"time"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/certtrust"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// Trusting a server's certificate (docs/security.md §7): when the
// connection test refuses an IMAP or SMTP server's certificate, its result
// row offers "Trust Certificate…". After an explicit confirmation that
// shows the certificate, its SHA-256 fingerprint is pinned to that endpoint
// (ServerConfig.certificateSha256) and the test runs again; account.add or
// account.update stores the pin with the account. The rules live in
// certtrust; everything shown here is the server's text, as plain text.

// trustOffer is a certificate the results page offers to trust: what the
// test reported and the endpoint configuration it was tested with.
type trustOffer struct {
	problem certtrust.Problem
	server  api.ServerConfig
}

// offerFor is the trust offer of one endpoint's test result, nil when the
// certificate cannot be trusted (certtrust.Trustable) or sc is missing.
func offerFor(res *api.EndpointTestResult, sc *api.ServerConfig) *trustOffer {
	if sc == nil {
		return nil
	}
	p, ok := certtrust.Trustable(res, *sc)
	if !ok {
		return nil
	}
	return &trustOffer{problem: p, server: *sc}
}

// showPin shows the pinned certificate row while the rows send a pin.
func (r *serverRows) showPin() {
	pin := r.read().CertificateSHA256
	r.pinRow.SetSubtitle(certtrust.FormatFingerprint(pin))
	r.pinRow.SetVisible(pin != "")
}

// forgetPin stops trusting the pinned certificate; the next test and the
// saved account go without it.
func (r *serverRows) forgetPin() {
	r.pinned = nil
	r.showPin()
}

// onTrust asks before trusting the certificate offered in rows' result
// row. When the other endpoint presented the same certificate (a mail
// bridge serving IMAP and SMTP), one confirmation trusts both. A pinned
// certificate that changed (pinMismatch) gets its own warning and shows
// the fingerprint trusted before.
func (w *Wizard) onTrust(rows *serverRows) {
	offer := rows.offer
	if offer == nil || offer.problem.Cert == nil {
		return
	}
	targets := []*serverRows{rows}
	other := &w.smtp
	if rows == &w.smtp {
		other = &w.imap
	}
	if other.offer != nil && certtrust.SameCertificate(offer.problem.Cert, other.offer.problem.Cert) {
		targets = []*serverRows{&w.imap, &w.smtp}
	}
	servers := certtrust.ServerName(targets[0].offer.server)
	if len(targets) == 2 {
		// TRANSLATORS: joins two servers ("host:port") in the sentence
		// "… accept exactly this certificate for %s and no other …".
		servers = fmt.Sprintf(i18n.T("%s and %s"), servers, certtrust.ServerName(targets[1].offer.server))
	}
	changed, previous := false, ""
	for _, r := range targets {
		if r.offer.problem.Category() == certtrust.Changed {
			changed = true
		}
		if previous == "" && r.offer.problem.Expected != "" {
			previous = certtrust.FormatFingerprint(r.offer.problem.Expected)
		}
	}
	if !changed {
		previous = ""
	}
	var heading, body string
	if changed {
		// TRANSLATORS: dialog heading, the pinned certificate of a server
		// has changed
		heading = i18n.T("Trust the New Certificate?")
		// TRANSLATORS: %s is one server ("host:port") or two joined by
		// "%s and %s".
		body = fmt.Sprintf(i18n.T("The certificate of %s has changed since you trusted it. If you did not renew it yourself, someone may be intercepting the connection. Trust the new certificate only if you know why it changed."), servers)
	} else {
		// TRANSLATORS: dialog heading
		heading = i18n.T("Trust This Certificate?")
		// TRANSLATORS: %s is one server ("host:port") or two joined by
		// "%s and %s".
		body = fmt.Sprintf(i18n.T("Only trust a certificate you expect, for example the one of a mail bridge on your own computer. Malachi Mail will then accept exactly this certificate for %s and no other, even if it is self-signed, issued for another name or expired."), servers)
	}
	// TRANSLATORS: destructive confirm button of "Trust This Certificate?"
	confirm := i18n.T("_Trust")
	op := w.op
	widget.ConfirmDestructiveExtra(w.Dialog, heading, body, confirm, certificateDetails(*offer.problem.Cert, previous), func() {
		if w.closed || op != w.op {
			return
		}
		for _, r := range targets {
			if r.offer == nil || r.offer.problem.Cert == nil {
				continue
			}
			pinned := r.offer.server
			pinned.CertificateSHA256 = r.offer.problem.Cert.SHA256
			r.pinned = &pinned
			r.showPin()
		}
		w.log.Info("certificate trusted", "endpoints", len(targets))
		w.runTest()
	})
}

// certificateDetails is the confirmation's extra child: what the
// certificate says about itself, as selectable plain text. The fingerprint
// comes first, so no text of the server's can pose as it; previous is the
// formatted fingerprint trusted before a pinMismatch ("" otherwise). Rows
// without a value are left out; "Self-signed" is a line of its own, only
// when true.
func certificateDetails(c api.CertificateInfo, previous string) *gtk.Grid {
	g := gtk.NewGrid()
	g.SetRowSpacing(6)
	g.SetColumnSpacing(12)
	row := 0
	add := func(key, value string, monospace bool) {
		if value == "" {
			return
		}
		k := gtk.NewLabel(key)
		k.SetUseMarkup(false)
		k.SetXAlign(0)
		k.SetYAlign(0)
		k.AddCSSClass("dim-label")
		v := gtk.NewLabel(value) // the server's text
		v.SetUseMarkup(false)
		v.SetSelectable(true)
		v.SetWrap(true)
		v.SetWrapMode(pango.WrapWordChar)
		v.SetXAlign(0)
		v.SetHExpand(true)
		if monospace {
			v.AddCSSClass("monospace")
		}
		g.Attach(k, 0, row, 1, 1)
		g.Attach(v, 1, row, 1, 1)
		row++
	}
	add(i18n.C("certificate", "SHA-256 fingerprint"), certtrust.FormatFingerprint(c.SHA256), true)
	// TRANSLATORS: label of the fingerprint of the certificate that was
	// pinned before
	add(i18n.C("certificate", "Previously trusted"), previous, true)
	add(i18n.C("certificate", "Issued to"), certtrust.IssuedTo(c), false)
	add(i18n.C("certificate", "Issued by"), c.Issuer, false)
	if !c.NotBefore.IsZero() && !c.NotAfter.IsZero() {
		now := time.Now()
		// TRANSLATORS: validity period of a certificate: two dates
		period := fmt.Sprintf(i18n.C("certificate", "%s to %s"), widget.FormatDate(c.NotBefore, now), widget.FormatDate(c.NotAfter, now))
		// TRANSLATORS: label of a certificate's validity period
		add(i18n.C("certificate", "Valid"), period, false)
	}
	if c.SelfSigned {
		l := gtk.NewLabel(i18n.C("certificate", "Self-signed"))
		l.SetXAlign(0)
		g.Attach(l, 0, row, 2, 1)
	}
	return g
}
