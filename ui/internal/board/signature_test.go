// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"slices"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The signature verdict of the board's conversation cards (signature.go).

func sigSummary(status api.SignatureStatus, notes ...api.SignatureNote) *api.SignatureSummary {
	return &api.SignatureSummary{
		Kind: api.SignatureSMIME, Status: status, Notes: notes,
		Signer: &api.SignatureSigner{Name: "Ann", Email: "ann@example.invalid", Fingerprint: "ab"},
	}
}

func TestSignatureKeyOf(t *testing.T) {
	eq(t, "nil", SignatureKeyOf(nil), SignatureKey{})
	a, b := sigSummary(api.SignatureValid), sigSummary(api.SignatureValid)
	check(t, a != b && SignatureKeyOf(a) == SignatureKeyOf(b), "equal verdicts behind two pointers differ")
	check(t, SignatureKeyOf(a) != SignatureKey{}, "a verdict is the zero key")
	// Without a signer, with no notes: present all the same.
	check(t, SignatureKeyOf(&api.SignatureSummary{}).Present, "an empty verdict is absent")
	for name, other := range map[string]*api.SignatureSummary{
		"status": sigSummary(api.SignatureUntrusted),
		"notes":  sigSummary(api.SignatureValid, api.SignatureNoteKeyChanged),
		"signer": {Kind: api.SignatureSMIME, Status: api.SignatureValid},
		"part":   {Kind: api.SignatureSMIME, Status: api.SignatureValid, Signer: a.Signer, PartID: "2"},
		"kind":   {Kind: api.SignaturePGP, Status: api.SignatureValid, Signer: a.Signer},
	} {
		check(t, SignatureKeyOf(a) != SignatureKeyOf(other), "%s: the keys are equal", name)
	}
}

func TestSameCard(t *testing.T) {
	at := time.Unix(1_790_000_000, 0).UTC()
	card := func(sig *api.SignatureSummary) MessageCard {
		return MessageCard{ID: "m1", From: "Ann", Sender: "Ann", When: "09:00", Date: at, Text: "Hi", Signature: sig}
	}
	check(t, SameCard(card(nil), card(nil)), "unsigned cards differ")
	check(t, SameCard(card(sigSummary(api.SignatureValid)), card(sigSummary(api.SignatureValid))), "equal verdicts differ")
	check(t, !SameCard(card(nil), card(sigSummary(api.SignatureValid))), "a new verdict is the same card")
	check(t, !SameCard(card(sigSummary(api.SignatureValid)), card(sigSummary(api.SignatureRevoked))), "a changed verdict is the same card")
	other := card(sigSummary(api.SignatureValid))
	other.Text = "Hello"
	check(t, !SameCard(card(sigSummary(api.SignatureValid)), other), "a changed text is the same card")
}

func TestConversationKeyCarriesTheVerdict(t *testing.T) {
	d := Detail{ID: "case", Messages: []MessageCard{{ID: "m1", Text: "Hi", Signature: sigSummary(api.SignatureValid)}, {ID: "m2", Text: "Ho"}}}
	k := ConversationKeyOf(d)
	eq(t, "members", k.Members, []ConversationMember{{ID: "m1", Text: "Hi"}, {ID: "m2", Text: "Ho"}})
	eq(t, "signatures", k.Signatures, []SignatureKey{SignatureKeyOf(sigSummary(api.SignatureValid)), {}})
	d.Messages[0].Signature = sigSummary(api.SignatureValid)
	check(t, slices.Equal(k.Signatures, ConversationKeyOf(d).Signatures), "an equal verdict from another answer changed the key")
}

// verdictKey is cardKey(1, 2, 3) with the verdicts of the members.
func verdictKey(sigs ...*api.SignatureSummary) ConversationKey {
	k := cardKey(1, 2, 3)
	for _, s := range sigs {
		k.Signatures = append(k.Signatures, SignatureKeyOf(s))
	}
	return k
}

// A verdict that changes alone keeps every card where it is, its fold and
// its body (a body still asked for too: its answer names the same member),
// and asks for no body again; the change is still reported, so the view
// updates the badge.
func TestConversationCardsVerdictChangeKeepsTheCards(t *testing.T) {
	c := NewConversationCards(0)
	c.Apply(verdictKey(nil, nil, nil))
	c.SetFolded(1, false)
	c.Asked(1)
	c.Answered(1, true)
	c.Asked(2)
	expectCards(t, c, []bool{true, false, false}, []ConversationBody{ConversationBodyUnknown, ConversationBodyHTML, ConversationBodyAsked})
	identity := map[int]int{0: 0, 1: 1, 2: 2}
	expectCardChange(t, c.Apply(verdictKey(nil, sigSummary(api.SignatureValid), sigSummary(api.SignatureUntrusted))), identity)
	expectCards(t, c, []bool{true, false, false}, []ConversationBody{ConversationBodyUnknown, ConversationBodyHTML, ConversationBodyAsked})
	check(t, !c.NeedsBody(1) && !c.NeedsBody(2), "a verdict change asks for a body again")
	eq(t, "web cards", c.WebCards(), []int{1})
	c.Answered(2, false)
	eq(t, "answer after the change", c.Body(2), ConversationBodyText)
	// The same verdicts from another answer change nothing.
	eq(t, "unchanged", c.Apply(verdictKey(nil, sigSummary(api.SignatureValid), sigSummary(api.SignatureUntrusted))).Kind, ConversationChangeNone)
	// Missing entries are no verdict.
	expectCardChange(t, c.Apply(verdictKey(nil)), identity)
	eq(t, "no verdicts", c.Apply(cardKey(1, 2, 3)).Kind, ConversationChangeNone)
	// A folded card keeps its body too.
	c.SetFolded(1, true)
	expectCardChange(t, c.Apply(verdictKey(sigSummary(api.SignatureRevoked))), identity)
	expectCards(t, c, []bool{true, true, false}, []ConversationBody{ConversationBodyUnknown, ConversationBodyHTML, ConversationBodyText})
	// A changed excerpt with a changed verdict reloads the open card as
	// ever, and the new verdicts are kept.
	k := verdictKey(nil, nil, sigSummary(api.SignatureValid))
	k.Members[2].Text = "new text"
	expectCardChange(t, c.Apply(k), identity, 2)
	eq(t, "kept verdicts", c.Apply(k).Kind, ConversationChangeNone)
}

// The detail's card carries the case's verdict; board.get's verdict
// reaches the case.
func TestDetailCardsCarryTheVerdict(t *testing.T) {
	sig := sigSummary(api.SignatureValid)
	got := convertMessage(api.BoardMessage{ID: "m1", From: api.Address{Name: "Ann", Address: "ann@example.invalid"}, Text: "Hi", Signature: sig})
	check(t, got.Signature == sig, "board.get's verdict was dropped: %+v", got.Signature)
	check(t, convertMessage(api.BoardMessage{ID: "m2"}).Signature == nil, "a verdict without one")
}

// The samples carry one signed message (Ada's final version in the first
// case), so the board's samples show a badge; the macOS and Windows
// samples mirror it.
func TestSamplesHaveASignedMessage(t *testing.T) {
	s := SampleSnapshot(time.Unix(1_790_000_000, 0), time.UTC)
	var signed []CaseMessage
	for _, c := range s.Cases {
		for _, m := range c.Messages {
			if m.Signature != nil {
				signed = append(signed, m)
			}
		}
	}
	if len(signed) != 1 {
		t.Fatalf("signed sample messages: %d", len(signed))
	}
	m := signed[0]
	check(t, m.From == "Ada Example" && m.Signature.Kind == api.SignatureSMIME && m.Signature.Status == api.SignatureValid &&
		m.Signature.Signer != nil && m.Signature.Signer.Name == "Ada Example" && m.Signature.Signer.Email == "ada@example.invalid",
		"sample verdict %+v", m.Signature)
	first := s.Cases[0].Messages
	check(t, first[len(first)-1].Signature != nil, "not the first case's newest message")
}
