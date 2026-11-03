// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/signature"
)

// smokeBoardSignatureBadge (within TestBoardGTKSmoke, no daemon): the
// samples' signed message (Ada's final version in the first case) shows
// its badge on its card; then an invented detail whose verdicts change
// without anything else: the badge follows on the same card (shown folded
// too), an equal verdict from another answer changes nothing, a verdict
// that goes hides it, and a click opens the details dialog with the
// card's verdict and id (a sample's: the verdict alone).
func smokeBoardSignatureBadge(t *testing.T, p *boardPage, src *board.InMemorySource, flush func()) {
	t.Helper()
	var first board.Case
	for _, c := range src.Snapshot().Cases {
		if len(c.Messages) > 0 && c.Messages[len(c.Messages)-1].Signature != nil && !c.Done() {
			first = c
		}
	}
	if first.ID == "" {
		t.Fatal("no sample case on the board has a signed newest message")
	}
	p.ctl.SetStyle(board.StyleList)
	p.ctl.Select(first.ID)
	p.applyAll()
	flush()
	b := p.conversation
	if b == nil || len(b.cards) != len(first.Messages) {
		t.Fatal("the first sample case has no conversation cards")
	}
	want := signature.BadgeFor(first.Messages[len(first.Messages)-1].Signature, i18n.Tr).Text
	if got := b.cards[len(b.cards)-1].sig.shown(); got == "" || got != want {
		t.Fatalf("the sample's signed card shows %q, want %q", got, want)
	}
	if got := b.cards[0].sig.shown(); got != "" {
		t.Fatalf("an unsigned sample card shows a badge %q", got)
	}
	// A sample's card (no id) opens the dialog with its verdict alone.
	b.cards[len(b.cards)-1].openSignature()
	if dlg := sigDialogs[p.w]; dlg == nil || dlg.id != "" || dlg.loading || dlg.details.Kind != api.SignatureSMIME {
		t.Fatalf("the sample's details dialog: %+v", dlg)
	}
	sigDialogs[p.w].dialog.ForceClose()
	flush()

	b.close()
	removeAllChildren(p.conversationBox)
	p.conversation = nil
	signer := &api.SignatureSigner{Name: "Ann <b>Example</b>", Email: "ann@example.invalid"}
	verdict := func(status api.SignatureStatus) *api.SignatureSummary {
		return &api.SignatureSummary{Kind: api.SignatureSMIME, Status: status, Signer: signer}
	}
	d := board.Detail{ID: "smoke-signature-case", AccountID: "smoke-account", ConversationTitle: "Invented signed conversation",
		Messages: []board.MessageCard{
			{ID: "smoke-signed", From: "Ann Example", Text: "Signed and folded", Signature: verdict(api.SignatureValid)},
			{ID: "smoke-unsigned", From: "Ben Example", Text: "Not signed"},
		}}
	p.renderDetailConversation(d)
	flush()
	b = p.conversation
	defer func() {
		b.close()
		removeAllChildren(p.conversationBox)
		p.conversation = nil
	}()
	signed, unsigned := b.cards[0], b.cards[1]
	if !b.state.IsFolded(0) {
		t.Fatal("the older card is open")
	}
	valid := signature.BadgeFor(verdict(api.SignatureValid), i18n.Tr).Text
	if got := signed.sig.shown(); got != valid || signed.sig.label.UseMarkup() {
		t.Fatalf("folded signed card shows %q (markup %v), want %q", got, signed.sig.label.UseMarkup(), valid)
	}
	if unsigned.sig.shown() != "" {
		t.Fatal("an unsigned card shows a badge")
	}
	// An equal verdict from another answer: the same card, nothing changes.
	d.Messages[0].Signature = verdict(api.SignatureValid)
	b.apply(&d)
	flush()
	if b.cards[0] != signed || signed.sig.shown() != valid {
		t.Fatal("an equal verdict replaced the card or its badge")
	}
	// A changed verdict alone: the same card, the new badge.
	d.Messages[0].Signature = verdict(api.SignatureRevoked)
	b.apply(&d)
	flush()
	revoked := signature.BadgeFor(verdict(api.SignatureRevoked), i18n.Tr)
	if b.cards[0] != signed || signed.sig.shown() != revoked.Text || signed.sig.style != revoked.Style {
		t.Fatalf("a changed verdict shows %q (%s), want %q (%s)", signed.sig.shown(), signed.sig.style, revoked.Text, revoked.Style)
	}
	if !b.state.IsFolded(0) {
		t.Fatal("a changed verdict opened the card")
	}
	// The verdict comes to the newest card, then goes from the older one.
	d.Messages[1].Signature = verdict(api.SignatureUntrusted)
	d.Messages[0].Signature = nil
	b.apply(&d)
	flush()
	if b.cards[1] != unsigned || unsigned.sig.shown() == "" || signed.sig.shown() != "" {
		t.Fatal("the verdicts did not move between the cards")
	}
	// A click opens the dialog with the card's verdict and its id (no
	// client here: nothing is asked).
	unsigned.openSignature()
	dlg := sigDialogs[p.w]
	if dlg == nil || dlg.id != "smoke-unsigned" || dlg.details.Status != api.SignatureUntrusted || dlg.loading {
		t.Fatalf("the details dialog: %+v", dlg)
	}
	dlg.dialog.ForceClose()
	flush()
	if sigDialogs[p.w] != nil {
		t.Fatal("the details dialog stayed registered")
	}
}
