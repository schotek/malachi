// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"testing"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// board.get carries each member's signature verdict as message.get does,
// and none for a member without one.
func TestBoardGetCarriesTheSignature(t *testing.T) {
	x := newBoardBox(t)
	signed := x.put(bmail{folder: x.inbox, thread: "t_sig", rfc: "s1", from: boardAlice, to: []api.Address{boardMe}, text: "Signed, please check."})
	x.put(bmail{folder: x.inbox, thread: "t_sig", rfc: "s2", inReplyTo: "s1", from: boardAlice, to: []api.Address{boardMe}, at: 1, text: "And unsigned."})
	want := api.SignatureSummary{Kind: api.SignatureSMIME, Status: api.SignatureValid, PartID: "2",
		Signer: &api.SignatureSigner{Name: "Alice", Email: boardAlice.Address, Fingerprint: "ab12"},
		Notes:  []api.SignatureNote{api.SignatureNoteRevocationUnchecked}}
	if err := x.b.store.PutSignature(x.ctx, store.SignatureRow{MessageID: signed, AccountID: x.acc,
		Details: api.SignatureDetails{SignatureSummary: want}}); err != nil {
		t.Fatal(err)
	}
	x.drain()

	got, err := x.svc.Get(x.ctx, api.BoardGetParams{CaseID: x.caseOf("t_sig").ID})
	if err != nil || len(got.Messages) != 2 {
		t.Fatalf("board.get: %+v %v", got, err)
	}
	for _, m := range got.Messages {
		switch string(m.ID) {
		case signed:
			s := m.Signature
			if s == nil || s.Kind != want.Kind || s.Status != want.Status || s.PartID != want.PartID ||
				s.Signer == nil || *s.Signer != *want.Signer || len(s.Notes) != 1 || s.Notes[0] != want.Notes[0] {
				t.Errorf("signed member: %+v", s)
			}
		default:
			if m.Signature != nil {
				t.Errorf("unsigned member has %+v", m.Signature)
			}
		}
	}
}
