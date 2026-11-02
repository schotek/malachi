// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// TestSignatureRows: a verdict is stored, listed with its message,
// marked stale, and goes with the message.
func TestSignatureRows(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	f := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	acc, folder := f.AccountID, f.ID
	signed := seedMessage(t, s, f, 1, "signed", time.Now())
	seedMessage(t, s, f, 2, "plain", time.Now())
	id := signed.ID
	row := SignatureRow{MessageID: id, AccountID: acc, Version: "1", TrustKey: "roots", VerifiedAt: time.Now(),
		CertSHA256: "ab", Details: api.SignatureDetails{SignatureSummary: api.SignatureSummary{Kind: api.SignatureSMIME,
			Status: api.SignatureValid, PartID: "2", Signer: &api.SignatureSigner{Email: "a@example.org"},
			Notes: []api.SignatureNote{api.SignatureNoteRevocationUnchecked}}}}
	if err := s.PutSignature(ctx, row); err != nil {
		t.Fatal(err)
	}
	if err := s.PutSignature(ctx, SignatureRow{MessageID: "m_gone", AccountID: acc, Version: "1"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("a verdict for no message: %v", err)
	}
	list, _, _, err := s.ListMessages(ctx, acc, folder, "", 10, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range list {
		switch m.ID {
		case id:
			if m.Signature == nil || m.Signature.Status != api.SignatureValid || m.Signature.Signer.Email != "a@example.org" ||
				len(m.Signature.Notes) != 1 || m.Signature.PartID != "2" {
				t.Errorf("listed %+v", m.Signature)
			}
		default:
			if m.Signature != nil {
				t.Errorf("unsigned listed with %+v", m.Signature)
			}
		}
	}
	if st, _ := s.StaleSignatures(ctx, "1", "roots", 10); len(st) != 0 {
		t.Errorf("fresh row stale: %v", st)
	}
	if st, _ := s.StaleSignatures(ctx, "1", "other roots", 10); len(st) != 1 {
		t.Errorf("other roots: %v", st)
	}
	if n, _ := s.MarkSignaturesStale(ctx, api.SignatureSMIME, "ab"); n != 1 {
		t.Errorf("marked %d", n)
	}
	if st, _ := s.StaleSignatures(ctx, "1", "roots", 10); len(st) != 1 {
		t.Errorf("marked row: %v", st)
	}
	if err := s.DeleteMessagesByUID(ctx, folder, []uint32{1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSignature(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("the verdict outlived its message: %v", err)
	}
}

func TestPGPKeyRows(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	k := PGPKeyRow{Fingerprint: "aa", Data: []byte{1}, Emails: []string{"a@x"}, Trust: api.PGPKeySeen,
		Source: api.PGPSourceAutocrypt, FirstSeen: now, CreatedAt: now}
	if err := s.PutPGPKey(ctx, k); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPGPKeyTrust(ctx, "aa", api.PGPKeyVerified); err != nil {
		t.Fatal(err)
	}
	// A new copy keeps the trust, source and first sight.
	k.Trust, k.Source, k.FirstSeen, k.UserIDs = api.PGPKeySeen, api.PGPSourceImport, now.Add(time.Hour), []string{"A <a@x>"}
	if err := s.PutPGPKey(ctx, k); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetPGPKey(ctx, "aa")
	if err != nil || got.Trust != api.PGPKeyVerified || got.Source != api.PGPSourceAutocrypt || !got.FirstSeen.Equal(now) || len(got.UserIDs) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	if n, _ := s.CountPGPKeys(ctx); n != 1 {
		t.Errorf("count %d", n)
	}
	if err := s.DeletePGPKey(ctx, "aa"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePGPKey(ctx, "aa"); !errors.Is(err, ErrNotFound) {
		t.Errorf("again: %v", err)
	}
	if err := s.PutRevocation(ctx, RevocationRow{CertSHA256: "ab", State: api.RevocationRevoked, RevokedAt: now, CheckedAt: now, Source: api.RevocationCRL}); err != nil {
		t.Fatal(err)
	}
	r, err := s.GetRevocation(ctx, "ab")
	if err != nil || r.State != api.RevocationRevoked || !r.RevokedAt.Equal(now) || r.Source != api.RevocationCRL {
		t.Errorf("%+v %v", r, err)
	}
}
