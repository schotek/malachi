// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Signature verdicts, revocation states and OpenPGP keys (migration 0026,
// docs/plans/signatures.md §4). message_signatures is a cache of
// internal/signature's verdict per message; a row goes with its message.

// SignatureRow is a message's verdict.
type SignatureRow struct {
	MessageID string
	AccountID string
	// Details is the dialog's content; its summary fields (Kind, Status,
	// Signer, Notes, PartID, Reason) are the columns.
	Details    api.SignatureDetails
	CertSHA256 string
	KeyID      string
	FromEmail  string
	Version    string
	TrustKey   string
	VerifiedAt time.Time
}

// Summary is what message.get carries.
func (r SignatureRow) Summary() *api.SignatureSummary {
	s := r.Details.SignatureSummary
	return &s
}

// PutSignature stores (or replaces) a message's verdict; the row is no
// longer stale. ErrNotFound when the message is gone.
func (s *Store) PutSignature(ctx context.Context, r SignatureRow) error {
	d := r.Details
	details, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("put signature: %w", err)
	}
	var name, email, fpr string
	if d.Signer != nil {
		name, email, fpr = d.Signer.Name, d.Signer.Email, d.Signer.Fingerprint
	}
	notes := make([]string, len(d.Notes))
	for i, n := range d.Notes {
		notes[i] = string(n)
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO message_signatures (message_id, account_id, kind, status, reason,
			signer_name, signer_email, fingerprint, notes, part_id, cert_sha256, key_id, from_email, details,
			verifier_version, trust_key, stale, verified_at)
		SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ? WHERE EXISTS (SELECT 1 FROM messages WHERE id = ?)
		ON CONFLICT (message_id) DO UPDATE SET account_id = excluded.account_id, kind = excluded.kind,
			status = excluded.status, reason = excluded.reason, signer_name = excluded.signer_name,
			signer_email = excluded.signer_email, fingerprint = excluded.fingerprint, notes = excluded.notes,
			part_id = excluded.part_id, cert_sha256 = excluded.cert_sha256, key_id = excluded.key_id,
			from_email = excluded.from_email, details = excluded.details,
			verifier_version = excluded.verifier_version, trust_key = excluded.trust_key, stale = 0,
			verified_at = excluded.verified_at`,
		r.MessageID, r.AccountID, string(d.Kind), string(d.Status), string(d.Reason), name, email, fpr,
		strings.Join(notes, ","), d.PartID, r.CertSHA256, r.KeyID, r.FromEmail, string(details),
		r.Version, r.TrustKey, stamp(r.VerifiedAt), r.MessageID)
	if err != nil {
		return fmt.Errorf("put signature: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteSignature removes a message's verdict (it has none any more);
// reports whether there was one.
func (s *Store) DeleteSignature(ctx context.Context, messageID string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM message_signatures WHERE message_id = ?`, messageID)
	if err != nil {
		return false, fmt.Errorf("delete signature: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// GetSignature returns a message's verdict; ErrNotFound without one.
func (s *Store) GetSignature(ctx context.Context, messageID string) (SignatureRow, error) {
	var r SignatureRow
	var details, verified string
	err := s.db.QueryRowContext(ctx, `SELECT message_id, account_id, details, cert_sha256, key_id, from_email,
			verifier_version, trust_key, verified_at FROM message_signatures WHERE message_id = ?`, messageID).
		Scan(&r.MessageID, &r.AccountID, &details, &r.CertSHA256, &r.KeyID, &r.FromEmail, &r.Version, &r.TrustKey, &verified)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, fmt.Errorf("get signature: %w", err)
	}
	if err := json.Unmarshal([]byte(details), &r.Details); err != nil {
		return r, fmt.Errorf("get signature: %w", err)
	}
	r.VerifiedAt = parseStamp(verified)
	return r, nil
}

// attachSignaturesTo sets Message.Signature of the listed rows that have
// a verdict (every listing passes here with the invitations).
func attachSignaturesTo(ctx context.Context, q querier, msgs []*Message) error {
	if len(msgs) == 0 {
		return nil
	}
	at := make(map[string][]*Message, len(msgs))
	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		if _, ok := at[m.ID]; !ok {
			ids = append(ids, m.ID)
		}
		at[m.ID] = append(at[m.ID], m)
	}
	for _, chunk := range chunkStrings(ids, 500) {
		rows, err := q.QueryContext(ctx, `SELECT message_id, kind, status, reason, signer_name, signer_email,
				fingerprint, notes, part_id FROM message_signatures WHERE message_id IN (`+inPlaceholders(len(chunk))+`)`,
			toAny(chunk)...)
		if err != nil {
			return fmt.Errorf("signatures: %w", err)
		}
		for rows.Next() {
			var id, kind, status, reason, name, email, fpr, notes, part string
			if err := rows.Scan(&id, &kind, &status, &reason, &name, &email, &fpr, &notes, &part); err != nil {
				rows.Close()
				return fmt.Errorf("signatures: %w", err)
			}
			sum := api.SignatureSummary{Kind: api.SignatureKind(kind), Status: api.SignatureStatus(status), PartID: part}
			if name != "" || email != "" || fpr != "" {
				sum.Signer = &api.SignatureSigner{Name: name, Email: email, Fingerprint: fpr}
			}
			if notes != "" {
				for _, n := range strings.Split(notes, ",") {
					sum.Notes = append(sum.Notes, api.SignatureNote(n))
				}
			}
			for _, m := range at[id] {
				one := sum
				m.Signature = &one
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return fmt.Errorf("signatures: %w", err)
		}
	}
	return nil
}

// SignatureRef names a message to verify (again).
type SignatureRef struct {
	AccountID string
	MessageID string
}

// StaleSignatures lists verdicts to compute again: marked stale, of
// another verifier version, or S/MIME ones of another trust key (the
// system roots changed). At most limit.
func (s *Store) StaleSignatures(ctx context.Context, version, smimeTrust string, limit int) ([]SignatureRef, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT account_id, message_id FROM message_signatures
		WHERE stale = 1 OR verifier_version != ? OR (kind = 'smime' AND trust_key != ?)
		ORDER BY message_id LIMIT ?`, version, smimeTrust, limit)
	if err != nil {
		return nil, fmt.Errorf("stale signatures: %w", err)
	}
	defer rows.Close()
	var out []SignatureRef
	for rows.Next() {
		var r SignatureRef
		if err := rows.Scan(&r.AccountID, &r.MessageID); err != nil {
			return nil, fmt.Errorf("stale signatures: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkSignaturesStale marks the verdicts of a kind ("" = every kind), or
// only those of one certificate (certSHA256 != ""), to be computed again.
func (s *Store) MarkSignaturesStale(ctx context.Context, kind api.SignatureKind, certSHA256 string) (int, error) {
	q := `UPDATE message_signatures SET stale = 1 WHERE stale = 0`
	var args []any
	if kind != "" {
		q += ` AND kind = ?`
		args = append(args, string(kind))
	}
	if certSHA256 != "" {
		q += ` AND cert_sha256 = ?`
		args = append(args, certSHA256)
	}
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("mark signatures stale: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// SignatureCandidates pages through fetched messages that may carry a
// signature of the whole message (a signature part, an S/MIME wrapper,
// or a text that starts with an inline signature) and have no verdict of
// version, in id order after afterID. last is the last id looked at ("" =
// done).
func (s *Store) SignatureCandidates(ctx context.Context, version, afterID string, limit int) (refs []SignatureRef, last string, err error) {
	rows, err := s.db.QueryContext(ctx, `SELECT m.id, m.account_id,
			(m.attachments_json LIKE '%pkcs7-signature%' OR m.attachments_json LIKE '%pgp-signature%'
			 OR m.attachments_json LIKE '%pkcs7-mime%'
			 OR substr(ltrim(m.text_body, ' '||char(9)||char(10)||char(13)), 1, 34) = '-----BEGIN PGP SIGNED MESSAGE-----')
			AND NOT EXISTS (SELECT 1 FROM message_signatures g WHERE g.message_id = m.id AND g.verifier_version = ?)
		FROM messages m WHERE m.id > ? AND m.body_state = 'fetched' ORDER BY m.id LIMIT ?`, version, afterID, limit)
	if err != nil {
		return nil, "", fmt.Errorf("signature candidates: %w", err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var id, acc string
		var want bool
		if err := rows.Scan(&id, &acc, &want); err != nil {
			return nil, "", fmt.Errorf("signature candidates: %w", err)
		}
		n++
		last = id
		if want {
			refs = append(refs, SignatureRef{AccountID: acc, MessageID: id})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("signature candidates: %w", err)
	}
	if n < limit {
		last = ""
	}
	return refs, last, nil
}

// SignaturesOfCert lists the messages whose S/MIME signer is the
// certificate.
func (s *Store) SignaturesOfCert(ctx context.Context, certSHA256 string) ([]SignatureRef, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT account_id, message_id FROM message_signatures WHERE cert_sha256 = ?`, certSHA256)
	if err != nil {
		return nil, fmt.Errorf("signatures of a certificate: %w", err)
	}
	defer rows.Close()
	var out []SignatureRef
	for rows.Next() {
		var r SignatureRef
		if err := rows.Scan(&r.AccountID, &r.MessageID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// OpaqueCandidates pages through fetched messages whose attachments list
// an S/MIME wrapper (application/pkcs7-mime): what the parser now unwraps
// (plan S14), in id order after afterID; last as SignatureCandidates.
func (s *Store) OpaqueCandidates(ctx context.Context, afterID string, limit int) (refs []SignatureRef, last string, err error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, account_id, attachments_json LIKE '%pkcs7-mime%'
		FROM messages WHERE id > ? AND body_state = 'fetched' ORDER BY id LIMIT ?`, afterID, limit)
	if err != nil {
		return nil, "", fmt.Errorf("opaque candidates: %w", err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var id, acc string
		var want bool
		if err := rows.Scan(&id, &acc, &want); err != nil {
			return nil, "", fmt.Errorf("opaque candidates: %w", err)
		}
		n++
		last = id
		if want {
			refs = append(refs, SignatureRef{AccountID: acc, MessageID: id})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	if n < limit {
		last = ""
	}
	return refs, last, nil
}

// --- revocation --------------------------------------------------------------

// RevocationRow is the last revocation check of a certificate.
type RevocationRow struct {
	CertSHA256 string
	State      api.RevocationState
	RevokedAt  time.Time // zero when not revoked
	CheckedAt  time.Time
	Source     api.RevocationSource
}

// GetRevocation returns the stored check; ErrNotFound when never checked.
func (s *Store) GetRevocation(ctx context.Context, certSHA256 string) (RevocationRow, error) {
	r := RevocationRow{CertSHA256: certSHA256}
	var state, revoked, checked, source string
	err := s.db.QueryRowContext(ctx, `SELECT state, revoked_at, checked_at, source FROM smime_revocation WHERE cert_sha256 = ?`,
		certSHA256).Scan(&state, &revoked, &checked, &source)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, fmt.Errorf("get revocation: %w", err)
	}
	r.State, r.Source = api.RevocationState(state), api.RevocationSource(source)
	if revoked != "" {
		r.RevokedAt = parseStamp(revoked)
	}
	r.CheckedAt = parseStamp(checked)
	return r, nil
}

// PutRevocation stores a check's result.
func (s *Store) PutRevocation(ctx context.Context, r RevocationRow) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO smime_revocation (cert_sha256, state, revoked_at, checked_at, source)
		VALUES (?, ?, ?, ?, ?) ON CONFLICT (cert_sha256) DO UPDATE SET state = excluded.state,
			revoked_at = excluded.revoked_at, checked_at = excluded.checked_at, source = excluded.source`,
		r.CertSHA256, string(r.State), optStamp(r.RevokedAt), stamp(r.CheckedAt), string(r.Source))
	if err != nil {
		return fmt.Errorf("put revocation: %w", err)
	}
	return nil
}

// --- OpenPGP keys ------------------------------------------------------------

// PGPKeyRow is a stored public key.
type PGPKeyRow struct {
	Fingerprint string
	Data        []byte // binary transferable public key
	UserIDs     []string
	Emails      []string
	KeyIDs      []string
	Trust       api.PGPKeyTrust
	Source      api.PGPKeySource
	FirstSeen   time.Time
	CreatedAt   time.Time
	ExpiresAt   time.Time // zero = never
	RevokedAt   time.Time // zero = not revoked
	Algorithm   string
}

const pgpKeyColumns = `fingerprint, key_data, user_ids, emails, key_ids, trust, source, first_seen, created_at,
	expires_at, revoked_at, algorithm`

func scanPGPKey(row scanner) (PGPKeyRow, error) {
	var k PGPKeyRow
	var uids, emails, kids, trust, source, first, created, expires, revoked string
	if err := row.Scan(&k.Fingerprint, &k.Data, &uids, &emails, &kids, &trust, &source, &first, &created,
		&expires, &revoked, &k.Algorithm); err != nil {
		return k, err
	}
	_ = json.Unmarshal([]byte(uids), &k.UserIDs)
	_ = json.Unmarshal([]byte(emails), &k.Emails)
	_ = json.Unmarshal([]byte(kids), &k.KeyIDs)
	k.Trust, k.Source = api.PGPKeyTrust(trust), api.PGPKeySource(source)
	k.FirstSeen, k.CreatedAt = parseStamp(first), parseStamp(created)
	if expires != "" {
		k.ExpiresAt = parseStamp(expires)
	}
	if revoked != "" {
		k.RevokedAt = parseStamp(revoked)
	}
	return k, nil
}

// ListPGPKeys returns every stored key.
func (s *Store) ListPGPKeys(ctx context.Context) ([]PGPKeyRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+pgpKeyColumns+` FROM pgp_keys ORDER BY fingerprint`)
	if err != nil {
		return nil, fmt.Errorf("list pgp keys: %w", err)
	}
	defer rows.Close()
	var out []PGPKeyRow
	for rows.Next() {
		k, err := scanPGPKey(rows)
		if err != nil {
			return nil, fmt.Errorf("list pgp keys: %w", err)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// GetPGPKey returns one key; ErrNotFound for an unknown fingerprint.
func (s *Store) GetPGPKey(ctx context.Context, fingerprint string) (PGPKeyRow, error) {
	k, err := scanPGPKey(s.db.QueryRowContext(ctx, `SELECT `+pgpKeyColumns+` FROM pgp_keys WHERE fingerprint = ?`, fingerprint))
	if errors.Is(err, sql.ErrNoRows) {
		return k, ErrNotFound
	}
	if err != nil {
		return k, fmt.Errorf("get pgp key: %w", err)
	}
	return k, nil
}

// CountPGPKeys counts the stored keys.
func (s *Store) CountPGPKeys(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pgp_keys`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count pgp keys: %w", err)
	}
	return n, nil
}

// PutPGPKey inserts a key or updates a stored one's data and listings;
// a stored key keeps its trust, source and first sight.
func (s *Store) PutPGPKey(ctx context.Context, k PGPKeyRow) error {
	js := func(v []string) string {
		if v == nil {
			v = []string{}
		}
		b, _ := json.Marshal(v)
		return string(b)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO pgp_keys (`+pgpKeyColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (fingerprint) DO UPDATE SET key_data = excluded.key_data, user_ids = excluded.user_ids,
			emails = excluded.emails, key_ids = excluded.key_ids, created_at = excluded.created_at,
			expires_at = excluded.expires_at, revoked_at = excluded.revoked_at, algorithm = excluded.algorithm`,
		k.Fingerprint, k.Data, js(k.UserIDs), js(k.Emails), js(k.KeyIDs), string(k.Trust), string(k.Source),
		stamp(k.FirstSeen), stamp(k.CreatedAt), optStamp(k.ExpiresAt), optStamp(k.RevokedAt), k.Algorithm)
	if err != nil {
		return fmt.Errorf("put pgp key: %w", err)
	}
	return nil
}

// SetPGPKeyTrust sets a key's trust; ErrNotFound for an unknown one.
func (s *Store) SetPGPKeyTrust(ctx context.Context, fingerprint string, trust api.PGPKeyTrust) error {
	res, err := s.db.ExecContext(ctx, `UPDATE pgp_keys SET trust = ? WHERE fingerprint = ?`, string(trust), fingerprint)
	if err != nil {
		return fmt.Errorf("set pgp key trust: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeletePGPKey removes a key; ErrNotFound for an unknown one.
func (s *Store) DeletePGPKey(ctx context.Context, fingerprint string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM pgp_keys WHERE fingerprint = ?`, fingerprint)
	if err != nil {
		return fmt.Errorf("delete pgp key: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MessageAccount returns the account of a message; ErrNotFound for an
// unknown id.
func (s *Store) MessageAccount(ctx context.Context, id string) (string, error) {
	var acc string
	err := s.db.QueryRowContext(ctx, `SELECT account_id FROM messages WHERE id = ?`, id).Scan(&acc)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("message account: %w", err)
	}
	return acc, nil
}
