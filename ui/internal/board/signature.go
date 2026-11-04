// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The signature verdict of a conversation card (board.get's
// BoardMessage.Signature, docs/plans/signatures.md §6): the card shows the
// same badge as a card in Mail (ui/internal/signature), never a chat
// bubble. A verdict changes without the case's version (a key imported or
// confirmed, a revocation check, a verdict that came after board.get):
// DaemonSource.ReloadConversations asks board.get again, and the cards
// follow a verdict that changed alone (ConversationKey.Signatures) without
// losing their body or fold (the verdict is not the card's identity).

// SignatureKey is a verdict in comparable form: the zero value for none.
// Everything the badge and the details dialog's first look show is in it.
type SignatureKey struct {
	Present                  bool
	Kind, Status             string
	Name, Email, Fingerprint string
	Notes, PartID            string
}

// SignatureKeyOf is s's key (the zero key for nil).
func SignatureKeyOf(s *api.SignatureSummary) SignatureKey {
	if s == nil {
		return SignatureKey{}
	}
	k := SignatureKey{Present: true, Kind: string(s.Kind), Status: string(s.Status), PartID: s.PartID}
	if s.Signer != nil {
		k.Name, k.Email, k.Fingerprint = s.Signer.Name, s.Signer.Email, s.Signer.Fingerprint
	}
	notes := make([]string, len(s.Notes))
	for i, n := range s.Notes {
		notes[i] = string(n)
	}
	// Notes are codes without a comma.
	k.Notes = strings.Join(notes, ",")
	return k
}

// SameCard reports whether a and b show the same: every field by value,
// the signature verdict by its key (two board.get answers never share a
// pointer).
func SameCard(a, b MessageCard) bool {
	if SignatureKeyOf(a.Signature) != SignatureKeyOf(b.Signature) {
		return false
	}
	a.Signature, b.Signature = nil, nil
	return a == b
}
