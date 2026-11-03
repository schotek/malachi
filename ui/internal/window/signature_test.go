// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"errors"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/signature"
)

// The pure helpers of the signature badge, the list icon and the tiles
// (signature.go). i18n is not bound in tests, so the English msgids come
// back verbatim.

func TestShownSignature(t *testing.T) {
	list := api.MessageSummary{ID: "m1", Signature: &api.MessageSignature{Kind: api.SignatureSMIME, Status: api.SignatureValid}}
	full := &api.SignatureSummary{Kind: api.SignatureSMIME, Status: api.SignatureValid,
		Signer: &api.SignatureSigner{Name: "Jan Novák", Email: "jan@example.org"}}

	if got := shownSignature(list, nil); got != nil {
		t.Errorf("nothing loaded yet: %+v, want none (no sentence that changes a moment later)", got)
	}
	if got := shownSignature(list, &loadedMessage{getting: true}); got != nil {
		t.Errorf("message.get on its way: %+v", got)
	}
	if got := shownSignature(list, &loadedMessage{}); got != nil {
		t.Errorf("message.get never asked (a card without details): %+v", got)
	}
	if got := shownSignature(list, &loadedMessage{msg: &api.Message{Signature: full}}); got != full {
		t.Errorf("message.get's summary with the signer: %+v", got)
	}
	if got := shownSignature(list, &loadedMessage{msg: &api.Message{}}); got != nil {
		t.Errorf("message.get without a verdict wins over the list's: %+v", got)
	}
	got := shownSignature(list, &loadedMessage{getErr: errors.New("boom")})
	if got == nil || got.Kind != api.SignatureSMIME || got.Status != api.SignatureValid || got.Signer != nil {
		t.Errorf("a failed message.get falls back to the list's verdict alone: %+v", got)
	}
	if got := shownSignature(api.MessageSummary{}, &loadedMessage{getErr: errors.New("boom")}); got != nil {
		t.Errorf("no verdict anywhere: %+v", got)
	}
}

func TestSignatureBadgeOfTheHeader(t *testing.T) {
	sum := &api.SignatureSummary{Kind: api.SignaturePGP, Status: api.SignatureMismatch,
		Signer: &api.SignatureSigner{Email: "other@example.org"}}
	b := signature.BadgeFor(shownSignature(api.MessageSummary{}, &loadedMessage{msg: &api.Message{Signature: sum}}), i18n.Tr)
	if !b.Visible || b.Icon != signature.IconWarning || b.Style != signature.StyleWarning ||
		b.Text != "Signed by someone else: other@example.org" {
		t.Errorf("badge %+v", b)
	}
	if b := signature.BadgeFor(shownSignature(api.MessageSummary{}, nil), i18n.Tr); b.Visible {
		t.Error("no verdict, no badge")
	}
}

func TestFromAddress(t *testing.T) {
	s := api.MessageSummary{From: []api.Address{{Address: "list@example.org"}}}
	if got := fromAddress(s, nil); got != "list@example.org" {
		t.Errorf("summary: %q", got)
	}
	lm := &loadedMessage{msg: &api.Message{MessageSummary: api.MessageSummary{From: []api.Address{{Address: "jan@example.org"}}}}}
	if got := fromAddress(s, lm); got != "jan@example.org" {
		t.Errorf("message.get: %q", got)
	}
	if got := fromAddress(api.MessageSummary{}, nil); got != "" {
		t.Errorf("no From: %q", got)
	}
}

func TestTileAttachmentsHideTheSignaturePart(t *testing.T) {
	report := api.Attachment{PartID: "1.2", Filename: "report.pdf"}
	sig := api.Attachment{PartID: "2", Filename: "smime.p7s", ContentType: "application/pkcs7-signature"}
	m := &api.Message{Attachments: []api.Attachment{report, sig},
		Signature: &api.SignatureSummary{Kind: api.SignatureSMIME, Status: api.SignatureValid, PartID: "2"}}
	if got := chipAttachments(tileAttachments(m), nil, nil); len(got) != 1 || got[0].PartID != "1.2" {
		t.Errorf("with a verdict: %+v", got)
	}
	// Without a verdict (a signature deeper in the message, S4) the part
	// is a file like any other.
	m.Signature = nil
	if got := chipAttachments(tileAttachments(m), nil, nil); len(got) != 2 {
		t.Errorf("without a verdict: %+v", got)
	}
	// The chat's paperclip counts what the tiles show.
	m.Signature = &api.SignatureSummary{Kind: api.SignatureSMIME, Status: api.SignatureValid, PartID: "2"}
	if got := bubbleAttachmentBadge(api.MessageSummary{HasAttachments: true}, &loadedMessage{msg: m}, true); got.Count != 1 {
		t.Errorf("bubble paperclip %+v", got)
	}
}

func TestListIconOfRows(t *testing.T) {
	s := api.MessageSummary{ID: "m1", Signature: &api.MessageSignature{Kind: api.SignaturePGP, Status: api.SignatureValidSeen}}
	if m := summaryMessage(s); m.Signature.Icon != signature.IconGoodSeen || m.Signature.Style != signature.StyleGood ||
		m.Signature.Tooltip == "" {
		t.Errorf("message row %+v", m.Signature)
	}
	// Untrusted, weak and the grey verdicts show nothing in the list.
	for _, st := range []api.SignatureStatus{api.SignatureUntrusted, api.SignatureWeak, api.SignatureUnknownKey,
		api.SignatureUnsupported, api.SignatureError, "future"} {
		s.Signature.Status = st
		if m := summaryMessage(s); m.Signature.Icon != "" {
			t.Errorf("%s: %+v", st, m.Signature)
		}
	}
	s.Signature = nil
	if m := summaryMessage(s); m.Signature.Icon != "" {
		t.Error("no verdict, no icon")
	}
	// A conversation row shows its newest member's verdict.
	th := api.ThreadSummary{Latest: api.MessageSummary{Signature: &api.MessageSignature{Kind: api.SignatureSMIME, Status: api.SignatureInvalid}}}
	if icon := summaryThread(th, false, false).Signature; icon.Icon != signature.IconBad || icon.Style != signature.StyleBad {
		t.Errorf("thread row %+v", icon)
	}
}

func TestKeyActionFailedText(t *testing.T) {
	for a, want := range map[signature.Action]string{
		signature.ActionConfirmKey:   "The key could not be confirmed.",
		signature.ActionUnconfirmKey: "The confirmation could not be removed.",
		signature.ActionRemoveKey:    "The key could not be removed.",
	} {
		if got := keyActionFailedText(a); got != want {
			t.Errorf("%d: %q, want %q", a, got, want)
		}
	}
}
