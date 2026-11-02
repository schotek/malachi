// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp/packet"

	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/internal/pgpsig"
	"github.com/schotek/malachi/backend/internal/revocation"
	"github.com/schotek/malachi/backend/internal/signature"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The signature and OpenPGP key methods (docs/api.md §4.21).

func (b *Backend) Signatures() api.SignatureService { return &signatureService{b} }
func (b *Backend) PGP() api.PGPService              { return &pgpService{b} }

type signatureService struct{ b *Backend }

// messageOf finds a message by id alone (message.signature names no
// account).
func (b *Backend) messageOf(ctx context.Context, id api.MessageID) (store.Account, store.Message, error) {
	if id == "" {
		return store.Account{}, store.Message{}, api.NewError(api.CodeInvalidArgument, "messageId is required")
	}
	acc, err := b.store.MessageAccount(ctx, string(id))
	switch {
	case errors.Is(err, store.ErrNotFound):
		return store.Account{}, store.Message{}, api.NewError(api.CodeMessageNotFound, "unknown message %q", id)
	case err != nil:
		return store.Account{}, store.Message{}, api.NewError(api.CodeStorageError, "%v", err)
	}
	a, err := b.requireAccount(ctx, acc)
	if err != nil {
		return store.Account{}, store.Message{}, err
	}
	m, err := b.getMessage(ctx, a.ID, string(id))
	if err != nil {
		return store.Account{}, store.Message{}, err
	}
	return a, m, nil
}

// details is the stored verdict as the dialog gets it; nil without one.
func (b *Backend) signatureDetails(ctx context.Context, m store.Message) (*api.SignatureDetails, error) {
	row, err := b.store.GetSignature(ctx, m.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	d := row.Details
	d.MessageID = api.MessageID(m.ID)
	d.ArrivedAt = arrival(m, b.sigNow()).UTC()
	return &d, nil
}

func (s *signatureService) Signature(ctx context.Context, p api.MessageSignatureParams) (*api.MessageSignatureResult, error) {
	_, m, err := s.b.messageOf(ctx, p.MessageID)
	if err != nil {
		return nil, err
	}
	d, err := s.b.signatureDetails(ctx, m)
	if err != nil {
		return nil, err
	}
	return &api.MessageSignatureResult{Signature: d}, nil
}

// Revocation checks the S/MIME signer's certificate (plan S9): within
// api.RevocationCheckInterval of the last check of that certificate the
// stored state answers; otherwise the certificate and its issuer are read
// from the message again, OCSP then the CRL are asked, the result is
// stored with the certificate, and every message it signed is judged
// again (this one at once).
func (s *signatureService) Revocation(ctx context.Context, p api.MessageSignatureRevocationParams) (*api.MessageSignatureResult, error) {
	b := s.b
	a, m, err := b.messageOf(ctx, p.MessageID)
	if err != nil {
		return nil, err
	}
	d, err := b.signatureDetails(ctx, m)
	if err != nil {
		return nil, err
	}
	if d == nil || d.Kind != api.SignatureSMIME || d.SMIME == nil || len(d.SMIME.Chain) == 0 || !d.SMIME.Revocation.Checkable {
		return nil, api.NewError(api.CodeInvalidArgument, "the message has no S/MIME certificate to check")
	}
	cert := d.SMIME.Chain[0].SHA256
	now := b.sigNow()
	if r, err := b.store.GetRevocation(ctx, cert); err == nil && now.Sub(r.CheckedAt) < api.RevocationCheckInterval {
		return &api.MessageSignatureResult{Signature: d}, nil
	}
	// One check of a certificate at a time; a second caller waits for it.
	b.sig.mu.Lock()
	if busy, ok := b.sig.revBusy[cert]; ok {
		b.sig.mu.Unlock()
		select {
		case <-busy:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		d, err := b.signatureDetails(ctx, m)
		if err != nil {
			return nil, err
		}
		return &api.MessageSignatureResult{Signature: d}, nil
	}
	busy := make(chan struct{})
	b.sig.revBusy[cert] = busy
	b.sig.mu.Unlock()
	defer func() {
		b.sig.mu.Lock()
		delete(b.sig.revBusy, cert)
		b.sig.mu.Unlock()
		close(busy)
	}()

	raw, err := b.readRaw(ctx, a.ID, m.ID)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	roots, _ := b.signatureRoots(ctx)
	out, err := signature.Check(signature.Input{Raw: raw, At: arrival(m, now), Now: now, Roots: roots})
	if err != nil || out.Signer == nil || out.Issuer == nil || out.CertSHA256 != cert {
		return nil, api.NewError(api.CodeInvalidArgument, "the message has no S/MIME certificate to check")
	}
	check := b.CheckRevocation
	if check == nil {
		check = revocation.New().Check
	}
	res, err := check(ctx, out.Signer, out.Issuer)
	if err != nil {
		reason := revocation.FailureOf(err)
		if reason == "" {
			reason = api.RevocationFailNetwork
		}
		b.log.Info("revocation check failed", "message", m.ID, "reason", string(reason))
		return nil, &api.Error{Code: api.CodeRevocationFailed, Message: "revocation check failed: " + string(reason),
			Data: api.RevocationFailedData{Reason: reason}}
	}
	row := store.RevocationRow{CertSHA256: cert, State: res.State, CheckedAt: now, Source: res.Source}
	if res.RevokedAt != nil {
		row.RevokedAt = *res.RevokedAt
	}
	if err := b.store.PutRevocation(ctx, row); err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	if _, err := b.store.MarkSignaturesStale(ctx, api.SignatureSMIME, cert); err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	if _, err := b.verifyMessage(ctx, a.ID, m.ID, false); err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	b.signaturesChanged()
	b.wakeSignatures()
	d, err = b.signatureDetails(ctx, m)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, api.NewError(api.CodeInvalidArgument, "the message has no S/MIME certificate to check")
	}
	return &api.MessageSignatureResult{Signature: d}, nil
}

// --- OpenPGP keys -------------------------------------------------------------

type pgpService struct{ b *Backend }

// importFailed is invalidArgument with the class of a refused import.
func importFailed(reason api.PGPImportFailure) error {
	return &api.Error{Code: api.CodeInvalidArgument, Message: "key import refused: " + string(reason),
		Data: api.PGPImportErrorData{Reason: reason}}
}

func (s *pgpService) KeyImport(ctx context.Context, p api.PGPKeyImportParams) (*api.PGPKeyImportResult, error) {
	b := s.b
	n := 0
	if p.PartID != "" {
		n++
	}
	if p.Autocrypt {
		n++
	}
	if p.Armored != "" {
		n++
	}
	if p.Data != nil {
		n++
	}
	if n != 1 || (p.MessageID == "") != (p.PartID == "" && !p.Autocrypt) {
		return nil, api.NewError(api.CodeInvalidArgument, "give exactly one of messageId with partId, messageId with autocrypt, armored, data")
	}
	var data []byte
	var source api.PGPKeySource
	switch {
	case p.Armored != "":
		if len(p.Armored) > api.MaxPGPKeyBytes {
			return nil, importFailed(api.PGPImportTooLarge)
		}
		data, source = []byte(p.Armored), api.PGPSourceImport
	case p.Data != nil:
		if len(p.Data) > api.MaxPGPKeyBytes {
			return nil, importFailed(api.PGPImportTooLarge)
		}
		data, source = p.Data, api.PGPSourceImport
	case p.PartID != "":
		a, m, err := b.messageOf(ctx, p.MessageID)
		if err != nil {
			return nil, err
		}
		if !mime.ValidPartID(p.PartID) {
			return nil, api.NewError(api.CodePartNotFound, "no part %q", p.PartID)
		}
		if slices.Contains(m.RemoteParts, p.PartID) {
			return nil, api.NewError(api.CodePartNotDownloaded, "part %q is on the server", p.PartID)
		}
		f, err := b.store.OpenMessageRaw(ctx, a.ID, m.ID)
		if err != nil {
			return nil, api.NewError(api.CodePartNotFound, "no part %q", p.PartID)
		}
		part, err := mime.ExtractPart(f, p.PartID, mime.DefaultLimits(), api.MaxPGPKeyBytes)
		f.Close()
		switch {
		case errors.Is(err, mime.ErrPartTooBig):
			return nil, importFailed(api.PGPImportTooLarge)
		case err != nil:
			return nil, api.NewError(api.CodePartNotFound, "no part %q", p.PartID)
		}
		data, source = part.Body, api.PGPSourceAttached
	default:
		a, m, err := b.messageOf(ctx, p.MessageID)
		if err != nil {
			return nil, err
		}
		raw, err := b.readRaw(ctx, a.ID, m.ID)
		if err != nil {
			return nil, api.NewError(api.CodeInvalidArgument, "the message has no Autocrypt key")
		}
		ac, ok := pgpsig.ParseAutocrypt(signature.HeaderValues(raw, "Autocrypt"))
		from := signature.FromAddresses(raw)
		if !ok || len(from) != 1 || signature.NormalizeAddress(from[0]) != ac.Addr {
			return nil, api.NewError(api.CodeInvalidArgument, "the message has no Autocrypt key for its sender")
		}
		data, source = ac.KeyData, api.PGPSourceAutocrypt
	}
	return b.importKeys(ctx, data, source)
}

// importKeys stores the keys of data: a new key with trust seen, a stored
// one merged (it keeps its trust and source, gains user IDs, revocations,
// subkeys and newer self-signatures).
func (b *Backend) importKeys(ctx context.Context, data []byte, source api.PGPKeySource) (*api.PGPKeyImportResult, error) {
	now := b.sigNow()
	keys, err := pgpsig.ReadKeys(data, now)
	if err != nil {
		var ie *pgpsig.ImportError
		if errors.As(err, &ie) {
			return nil, importFailed(ie.Reason)
		}
		return nil, importFailed(api.PGPImportMalformed)
	}
	count, err := b.store.CountPGPKeys(ctx)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	res := &api.PGPKeyImportResult{Keys: []api.PGPKey{}}
	var fresh int
	for _, k := range keys {
		if _, err := b.store.GetPGPKey(ctx, k.Fingerprint); errors.Is(err, store.ErrNotFound) {
			fresh++
		}
	}
	if count+fresh > api.MaxPGPStoredKeys {
		return nil, importFailed(api.PGPImportTooMany)
	}
	changed := false
	for _, k := range keys {
		old, err := b.store.GetPGPKey(ctx, k.Fingerprint)
		switch {
		case errors.Is(err, store.ErrNotFound):
			if err := b.store.PutPGPKey(ctx, keyRow(k, api.PGPKeySeen, source, now)); err != nil {
				return nil, api.NewError(api.CodeStorageError, "%v", err)
			}
			res.Added++
			changed = true
		case err != nil:
			return nil, api.NewError(api.CodeStorageError, "%v", err)
		default:
			merged, grew := mergeKey(old, k, now)
			if grew {
				row := keyRow(merged, old.Trust, old.Source, old.FirstSeen)
				if err := b.store.PutPGPKey(ctx, row); err != nil {
					return nil, api.NewError(api.CodeStorageError, "%v", err)
				}
				res.Updated++
				changed = true
			}
		}
		row, err := b.store.GetPGPKey(ctx, k.Fingerprint)
		if err != nil {
			return nil, api.NewError(api.CodeStorageError, "%v", err)
		}
		res.Keys = append(res.Keys, toAPIKey(row))
	}
	if changed {
		b.keysChanged(ctx)
	}
	return res, nil
}

// mergeKey adds to the stored key what the imported one has more: user
// IDs, revocations, subkeys, newer binding signatures. grew reports a
// change.
func mergeKey(old store.PGPKeyRow, k pgpsig.Key, now time.Time) (pgpsig.Key, bool) {
	el := pgpsig.ParseKeyring([][]byte{old.Data})
	if len(el) != 1 {
		return k, true
	}
	e, n := el[0], k.Entity
	grew := false
	has := func(list []*packet.Signature, s *packet.Signature) bool {
		for _, x := range list {
			if x.CreationTime.Equal(s.CreationTime) && x.SigType == s.SigType {
				return true
			}
		}
		return false
	}
	for _, r := range n.Revocations {
		if !has(e.Revocations, r) {
			e.Revocations = append(e.Revocations, r)
			grew = true
		}
	}
	for name, id := range n.Identities {
		cur, ok := e.Identities[name]
		switch {
		case !ok:
			e.Identities[name] = id
			grew = true
		case id.SelfSignature != nil && (cur.SelfSignature == nil || id.SelfSignature.CreationTime.After(cur.SelfSignature.CreationTime)):
			e.Identities[name] = id
			grew = true
		default:
			for _, r := range id.Revocations {
				if !has(cur.Revocations, r) {
					cur.Revocations = append(cur.Revocations, r)
					cur.Signatures = append(cur.Signatures, r)
					grew = true
				}
			}
		}
	}
	for _, sk := range n.Subkeys {
		found := false
		for i := range e.Subkeys {
			cur := &e.Subkeys[i]
			if cur.PublicKey.KeyId != sk.PublicKey.KeyId {
				continue
			}
			found = true
			for _, r := range sk.Revocations {
				if !has(cur.Revocations, r) {
					cur.Revocations = append(cur.Revocations, r)
					grew = true
				}
			}
			if sk.Sig != nil && (cur.Sig == nil || sk.Sig.CreationTime.After(cur.Sig.CreationTime)) {
				cur.Sig = sk.Sig
				grew = true
			}
		}
		if !found {
			e.Subkeys = append(e.Subkeys, sk)
			grew = true
		}
	}
	if !grew {
		return k, false
	}
	var buf bytes.Buffer
	if err := e.Serialize(&buf); err != nil {
		return k, false
	}
	merged, err := pgpsig.ReadKeys(buf.Bytes(), now)
	if err != nil || len(merged) != 1 {
		return k, false
	}
	return merged[0], true
}

// fingerprintParam normalises a fingerprint parameter: hex, 40 or 64
// digits, spaces allowed, lower case.
func fingerprintParam(s string) (string, error) {
	f := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
	if _, err := hex.DecodeString(f); err != nil || (len(f) != 40 && len(f) != 64) {
		return "", api.NewError(api.CodeInvalidArgument, "fingerprint is not 40 or 64 hex digits")
	}
	return f, nil
}

func (s *pgpService) KeyVerify(ctx context.Context, p api.PGPKeyVerifyParams) (*api.PGPKeyVerifyResult, error) {
	b := s.b
	fpr, err := fingerprintParam(p.Fingerprint)
	if err != nil {
		return nil, err
	}
	trust := api.PGPKeySeen
	if p.Verified {
		trust = api.PGPKeyVerified
	}
	switch err := b.store.SetPGPKeyTrust(ctx, fpr, trust); {
	case errors.Is(err, store.ErrNotFound):
		return nil, api.NewError(api.CodeInvalidArgument, "unknown key")
	case err != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	row, err := b.store.GetPGPKey(ctx, fpr)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	b.keysChanged(ctx)
	return &api.PGPKeyVerifyResult{Key: toAPIKey(row)}, nil
}

func (s *pgpService) KeyList(ctx context.Context, _ api.PGPKeyListParams) (*api.PGPKeyListResult, error) {
	rows, err := s.b.store.ListPGPKeys(ctx)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	keys := make([]api.PGPKey, 0, len(rows))
	for _, r := range rows {
		keys = append(keys, toAPIKey(r))
	}
	first := func(k api.PGPKey) string {
		if len(k.Emails) > 0 {
			return k.Emails[0]
		}
		return "￿"
	}
	sort.SliceStable(keys, func(i, j int) bool {
		if a, c := first(keys[i]), first(keys[j]); a != c {
			return a < c
		}
		return keys[i].Fingerprint < keys[j].Fingerprint
	})
	return &api.PGPKeyListResult{Keys: keys}, nil
}

func (s *pgpService) KeyRemove(ctx context.Context, p api.PGPKeyRemoveParams) (*api.PGPKeyRemoveResult, error) {
	fpr, err := fingerprintParam(p.Fingerprint)
	if err != nil {
		return nil, err
	}
	switch err := s.b.store.DeletePGPKey(ctx, fpr); {
	case errors.Is(err, store.ErrNotFound):
		return nil, api.NewError(api.CodeInvalidArgument, "unknown key")
	case err != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	s.b.keysChanged(ctx)
	return &api.PGPKeyRemoveResult{}, nil
}

func toAPIKey(r store.PGPKeyRow) api.PGPKey {
	k := api.PGPKey{
		Fingerprint: r.Fingerprint, UserIDs: r.UserIDs, Emails: r.Emails,
		Trust: r.Trust, Source: r.Source, FirstSeen: r.FirstSeen.UTC(), CreatedAt: r.CreatedAt.UTC(),
		Algorithm: r.Algorithm,
	}
	if k.UserIDs == nil {
		k.UserIDs = []string{}
	}
	if k.Emails == nil {
		k.Emails = []string{}
	}
	if !r.ExpiresAt.IsZero() {
		t := r.ExpiresAt.UTC()
		k.ExpiresAt = &t
	}
	if !r.RevokedAt.IsZero() {
		t := r.RevokedAt.UTC()
		k.RevokedAt = &t
	}
	return k
}
