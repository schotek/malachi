// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/board"
	"github.com/schotek/malachi/backend/internal/ingest"
	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/internal/pgpsig"
	"github.com/schotek/malachi/backend/internal/signature"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Signature verification (docs/plans/signatures.md S2, S7, S16, S17,
// §4–§5). The Stored hook of the mail syncers only queues a message
// (noteSignature); the worker verifies it from the raw file after the
// store, never inside ingest, so a hostile signature cannot hold the
// synchronisation, and a panic of a verifier is an error verdict
// (signature.Check recovers; verifyMessage recovers again around
// everything else). The verdict is a row of message_signatures; when the
// summary changes the worker sends notify.messagesChanged for the
// message's folder. The worker also takes the Autocrypt key of new mail
// (§5), computes again the verdicts a change of trust marked stale, and
// rereads the system roots at start and once a day (S7). Nothing about a
// certificate or key (names, addresses, distinguished names) is logged:
// only the class of a failure and ids (S16).

// sigRootsEvery is how often the worker reads the system roots again.
const sigRootsEvery = 24 * time.Hour

// sigBatch is how many queued or stale messages one pass verifies.
const sigBatch = 100

// sigMaxRawBytes caps the raw message read for a verdict.
const sigMaxRawBytes = signature.MaxMessageBytes

// sigState is the worker's state (Backend.sig).
type sigState struct {
	mu      sync.Mutex
	pending map[string]string // message id → account id
	wake    chan struct{}

	roots    *x509.CertPool
	rootsKey string // the S/MIME trust key: a fingerprint of the roots

	keysGen  uint64 // bumped on every change of the stored keys
	keysAt   uint64 // keysGen of keys
	keys     []signature.StoredKey
	revBusy  map[string]chan struct{} // certificate → a running check
	rootsSet bool
}

func (s *sigState) init() {
	s.pending = map[string]string{}
	s.wake = make(chan struct{}, 1)
	s.revBusy = map[string]chan struct{}{}
}

// noteSignature queues a stored message for verification (the Stored
// hook of the mail syncers; plan S2).
func (b *Backend) noteSignature(accountID, messageID string) {
	s := &b.sig
	s.mu.Lock()
	if s.pending == nil {
		s.init()
	}
	s.pending[messageID] = accountID
	s.mu.Unlock()
	b.wakeSignatures()
}

func (b *Backend) wakeSignatures() {
	s := &b.sig
	s.mu.Lock()
	if s.wake == nil {
		s.init()
	}
	w := s.wake
	s.mu.Unlock()
	select {
	case w <- struct{}{}:
	default:
	}
}

// startSignatures runs the worker until ctx ends.
func (b *Backend) startSignatures(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	s := &b.sig
	s.mu.Lock()
	if s.wake == nil {
		s.init()
	}
	wake := s.wake
	s.mu.Unlock()
	go func() {
		defer close(done)
		b.refreshRoots(ctx)
		t := time.NewTicker(sigRootsEvery)
		defer t.Stop()
		for {
			b.drainSignatures(ctx)
			select {
			case <-ctx.Done():
				return
			case <-wake:
			case <-t.C:
				b.refreshRoots(ctx)
			}
		}
	}()
	return done
}

// drainSignatures verifies what is queued, then what is stale, until
// neither has anything. A stale row that comes back within one drain (it
// could not be written) waits for the next wake.
func (b *Backend) drainSignatures(ctx context.Context) {
	seen := map[string]bool{}
	for ctx.Err() == nil {
		s := &b.sig
		s.mu.Lock()
		batch := make([]store.SignatureRef, 0, sigBatch)
		for id, acc := range s.pending {
			batch = append(batch, store.SignatureRef{AccountID: acc, MessageID: id})
			delete(s.pending, id)
			if len(batch) == sigBatch {
				break
			}
		}
		s.mu.Unlock()
		if len(batch) == 0 {
			_, key := b.signatureRoots(ctx)
			stale, err := b.store.StaleSignatures(ctx, signature.VerifierVersion, key, sigBatch)
			if err != nil {
				if !isCancelled(err) {
					b.log.Warn("stale signatures", "err", errClass(err))
				}
				return
			}
			for _, r := range stale {
				if !seen[r.MessageID] {
					batch = append(batch, r)
				}
			}
			if len(batch) == 0 {
				return
			}
		}
		for _, r := range batch {
			if ctx.Err() != nil {
				return
			}
			seen[r.MessageID] = true
			if _, err := b.verifyMessage(ctx, r.AccountID, r.MessageID, true); err != nil && !isCancelled(err) &&
				!errors.Is(err, store.ErrNotFound) {
				b.log.Warn("verify a signature", "account", r.AccountID, "message", r.MessageID, "err", errClass(err))
			}
		}
	}
}

// errClass keeps the class of an error for the log (the store's errors
// name no content; a verifier's never reach here).
func errClass(err error) string {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return "notFound"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "cancelled"
	}
	return "store"
}

// signatureRoots returns the trusted S/MIME roots and their trust key,
// reading the system's on first use.
func (b *Backend) signatureRoots(ctx context.Context) (*x509.CertPool, string) {
	s := &b.sig
	s.mu.Lock()
	set := s.rootsSet
	pool, key := s.roots, s.rootsKey
	s.mu.Unlock()
	if !set {
		b.refreshRoots(ctx)
		s.mu.Lock()
		pool, key = s.roots, s.rootsKey
		s.mu.Unlock()
	}
	return pool, key
}

func (b *Backend) sigRootsKey() string {
	s := &b.sig
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rootsKey
}

// refreshRoots reads the roots (Backend.SignatureRoots, the system's by
// default) and their fingerprint; another fingerprint than before makes
// every S/MIME verdict of the old one stale by its trust key (plan S7).
func (b *Backend) refreshRoots(ctx context.Context) {
	get := b.SignatureRoots
	if get == nil {
		get = systemRoots
	}
	pool, key := get()
	s := &b.sig
	s.mu.Lock()
	changed := s.rootsSet && key != s.rootsKey
	s.roots, s.rootsKey, s.rootsSet = pool, key, true
	s.mu.Unlock()
	if changed {
		b.log.Info("signature roots changed")
		b.signaturesChanged()
		b.wakeSignatures()
	}
}

// systemRoots is the system's trust store (x509.SystemCertPool: the
// platform verifier on macOS and Windows, the distribution's bundle on
// Linux) and a fingerprint of it. Where the pool cannot list its roots
// (macOS, Windows) the fingerprint names only the platform, so a change
// there is seen at the next verification of a message, not as a change of
// trust.
func systemRoots() (*x509.CertPool, string) {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		return x509.NewCertPool(), "none"
	}
	return pool, rootsFingerprint(pool)
}

// rootsFingerprint hashes the subjects of a pool.
func rootsFingerprint(pool *x509.CertPool) string {
	//nolint:staticcheck // Subjects is the only view of a pool's roots; for the system pool of macOS and Windows it is empty.
	subjects := pool.Subjects()
	sort.Slice(subjects, func(i, j int) bool { return bytes.Compare(subjects[i], subjects[j]) < 0 })
	h := sha256.New()
	h.Write([]byte(runtime.GOOS))
	for _, s := range subjects {
		fmt.Fprintf(h, "%d:", len(s))
		h.Write(s)
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// storedKeys returns the stored OpenPGP keys as the verifier reads them,
// parsed again only after a change.
func (b *Backend) storedKeys(ctx context.Context) ([]signature.StoredKey, error) {
	s := &b.sig
	s.mu.Lock()
	if s.keys != nil && s.keysAt == s.keysGen {
		keys := s.keys
		s.mu.Unlock()
		return keys, nil
	}
	gen := s.keysGen
	s.mu.Unlock()
	rows, err := b.store.ListPGPKeys(ctx)
	if err != nil {
		return nil, err
	}
	keys := make([]signature.StoredKey, 0, len(rows))
	for _, r := range rows {
		el := pgpsig.ParseKeyring([][]byte{r.Data})
		if len(el) != 1 {
			continue
		}
		keys = append(keys, signature.StoredKey{Entity: el[0], Fingerprint: r.Fingerprint, Trust: r.Trust,
			Source: r.Source, FirstSeen: r.FirstSeen, Emails: r.Emails})
	}
	s.mu.Lock()
	if s.keysGen == gen {
		s.keys, s.keysAt = keys, gen
	}
	s.mu.Unlock()
	return keys, nil
}

// keysChanged forgets the parsed keys and marks every OpenPGP verdict
// stale.
func (b *Backend) keysChanged(ctx context.Context) {
	s := &b.sig
	s.mu.Lock()
	s.keysGen++
	s.keys = nil
	s.mu.Unlock()
	if _, err := b.store.MarkSignaturesStale(ctx, api.SignaturePGP, ""); err != nil && !isCancelled(err) {
		b.log.Warn("mark signatures stale", "err", errClass(err))
	}
	b.signaturesChanged()
	b.wakeSignatures()
}

// signaturesChanged sends notify.signaturesChanged.
func (b *Backend) signaturesChanged() {
	if n, ok := b.getNotifier().(api.SignatureNotifier); ok {
		n.SignaturesChanged(api.SignaturesChangedNotification{})
	}
}

// signatureAccount: an account whose mail is verified (imap, graph);
// issue trackers synthesise their mail and calendars have none.
func signatureAccount(a store.Account) bool {
	switch a.Config.Protocol() {
	case api.AccountIMAP, api.AccountGraph:
		return true
	}
	return false
}

// readRaw reads a stored message whole, within the cap.
func (b *Backend) readRaw(ctx context.Context, accountID, id string) ([]byte, error) {
	f, err := b.store.OpenMessageRaw(ctx, accountID, id)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, sigMaxRawBytes))
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// arrival is when the message arrived as the verifier judges it (plan
// S8): the board's rule (internal date, else Date, never after it was
// stored nor after now).
func arrival(m store.Message, now time.Time) time.Time {
	return board.Arrival(board.Member{Date: m.Date, InternalDate: m.InternalDate, StoredAt: m.CreatedAt}, now)
}

// sigVerdict is what one verification found, for the service methods.
type sigVerdict struct {
	row     *store.SignatureRow // nil: no verdict
	outcome *signature.Outcome
	changed bool // the summary differs from what was stored
}

// verifyMessage computes a message's verdict and stores it (or removes
// the stored one when the message has none); autocrypt takes the
// message's Autocrypt key (§5) when allowed. A panic is an error verdict
// of the message's kind if one was found, else nothing (S17).
func (b *Backend) verifyMessage(ctx context.Context, accountID, id string, autocrypt bool) (v sigVerdict, err error) {
	a, err := b.store.GetAccount(ctx, accountID)
	if err != nil {
		return v, err
	}
	m, err := b.store.GetMessage(ctx, accountID, id)
	if err != nil {
		return v, err
	}
	old := m.Signature
	if !signatureAccount(a) || m.BodyState != store.BodyFetched {
		return b.dropVerdict(ctx, m, old)
	}
	raw, err := b.readRaw(ctx, accountID, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return b.dropVerdict(ctx, m, old)
		}
		return v, err
	}
	defer func() {
		if p := recover(); p != nil {
			b.log.Warn("signature verifier failed", "account", accountID, "message", id)
			v, err = sigVerdict{}, nil
		}
	}()
	now := b.sigNow()
	if autocrypt {
		b.takeAutocrypt(ctx, a, m, raw, now)
	}
	if !signature.Candidate(raw, m.Attachments) {
		return b.dropVerdict(ctx, m, old)
	}
	roots, rootsKey := b.signatureRoots(ctx)
	keys, err := b.storedKeys(ctx)
	if err != nil {
		return v, err
	}
	out, err := signature.Check(signature.Input{
		Raw:         raw,
		At:          arrival(m, now),
		Now:         now,
		Roots:       roots,
		Keys:        keys,
		Attachments: m.Attachments,
		ShownFrom:   addressesOf(m.From),
		Rebuilt:     a.Config.Protocol() == api.AccountGraph,
		Revocation: func(cert string) (signature.Revocation, bool) {
			r, err := b.store.GetRevocation(ctx, cert)
			if err != nil {
				return signature.Revocation{}, false
			}
			out := signature.Revocation{State: r.State, Source: r.Source}
			ca := r.CheckedAt
			out.CheckedAt = &ca
			if !r.RevokedAt.IsZero() {
				ra := r.RevokedAt
				out.RevokedAt = &ra
			}
			return out, true
		},
	})
	if errors.Is(err, signature.ErrNone) {
		return b.dropVerdict(ctx, m, old)
	}
	if err != nil {
		return v, err
	}
	row := store.SignatureRow{
		MessageID: m.ID, AccountID: m.AccountID, Details: out.Details,
		CertSHA256: out.CertSHA256, KeyID: out.KeyID, FromEmail: out.FromEmail,
		Version: signature.VerifierVersion, VerifiedAt: now,
	}
	if out.Details.Kind == api.SignatureSMIME {
		row.TrustKey = rootsKey
	}
	if err := b.store.PutSignature(ctx, row); err != nil {
		return v, err
	}
	v = sigVerdict{row: &row, outcome: out, changed: !sameSummary(old, row.Summary())}
	b.fixHasAttachments(ctx, m, row.Details.PartID)
	if v.changed {
		b.messagesChanged(map[string][]string{m.AccountID: {m.FolderID}})
	}
	return v, nil
}

// addressesOf lists the addresses of a stored address list (never nil).
func addressesOf(list []api.Address) []string {
	out := make([]string, 0, len(list))
	for _, a := range list {
		out = append(out, a.Address)
	}
	return out
}

// sigNow is the verifier's clock (a test may set Backend.SignatureNow).
func (b *Backend) sigNow() time.Time {
	if b.SignatureNow != nil {
		return b.SignatureNow()
	}
	return time.Now()
}

// dropVerdict removes a stored verdict the message no longer has.
func (b *Backend) dropVerdict(ctx context.Context, m store.Message, old *api.SignatureSummary) (sigVerdict, error) {
	if old == nil {
		return sigVerdict{}, nil
	}
	if _, err := b.store.DeleteSignature(ctx, m.ID); err != nil {
		return sigVerdict{}, err
	}
	b.fixHasAttachments(ctx, m, "")
	b.messagesChanged(map[string][]string{m.AccountID: {m.FolderID}})
	return sigVerdict{changed: true}, nil
}

// fixHasAttachments makes has_attachments count neither the signature part
// of a message with a verdict (S15) nor the calendar part its invitation
// card shows.
func (b *Backend) fixHasAttachments(ctx context.Context, m store.Message, sigPart string) {
	skip := []string{}
	if sigPart != "" {
		skip = append(skip, sigPart)
	}
	if inv, err := b.store.GetInvitation(ctx, m.ID); err == nil && inv.PartID != "" {
		skip = append(skip, inv.PartID)
	}
	has := hasOtherAttachment(m.Attachments, skip...)
	if has != m.HasAttachments {
		if err := b.store.SetHasAttachments(ctx, m.ID, has); err != nil && !errors.Is(err, store.ErrNotFound) && !isCancelled(err) {
			b.log.Warn("set has attachments", "err", errClass(err))
		}
	}
}

func sameSummary(a, b *api.SignatureSummary) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a.Kind != b.Kind || a.Status != b.Status || a.PartID != b.PartID || !slices.Equal(a.Notes, b.Notes) {
		return false
	}
	if (a.Signer == nil) != (b.Signer == nil) {
		return false
	}
	return a.Signer == nil || *a.Signer == *b.Signer
}

// takeAutocrypt stores the Autocrypt key of a message (plan §5) when the
// header names the message's only From address, the message is no bulk
// mail and not in Junk or Trash, the key is one public key whose user IDs
// name that address and no other, no key is stored for the address yet
// (a new key for a known address is only offered: TOFU) and the store has
// room. Nothing is logged about the key.
func (b *Backend) takeAutocrypt(ctx context.Context, a store.Account, m store.Message, raw []byte, now time.Time) {
	vals := signature.HeaderValues(raw, "Autocrypt")
	if len(vals) == 0 {
		return
	}
	ac, ok := pgpsig.ParseAutocrypt(vals)
	if !ok {
		return
	}
	from := signature.FromAddresses(raw)
	if len(from) != 1 || signature.NormalizeAddress(from[0]) != ac.Addr {
		return
	}
	if (m.Bulk != "" && m.Bulk != "none") || signature.LooksBulk(raw) {
		return
	}
	if f, err := b.store.GetFolder(ctx, a.ID, m.FolderID); err != nil ||
		f.Role == api.RoleJunk || f.Role == api.RoleTrash {
		return
	}
	keys, err := pgpsig.ReadKeys(ac.KeyData, now)
	if err != nil || len(keys) != 1 {
		return
	}
	k := keys[0]
	if len(k.Emails) == 0 || slices.ContainsFunc(k.Emails, func(e string) bool { return e != ac.Addr }) {
		return
	}
	stored, err := b.store.ListPGPKeys(ctx)
	if err != nil || len(stored) >= api.MaxPGPStoredKeys {
		return
	}
	for _, s := range stored {
		if s.Fingerprint == k.Fingerprint || slices.Contains(s.Emails, ac.Addr) {
			return
		}
	}
	if err := b.store.PutPGPKey(ctx, keyRow(k, api.PGPKeySeen, api.PGPSourceAutocrypt, now)); err != nil {
		if !isCancelled(err) {
			b.log.Warn("store an Autocrypt key", "err", errClass(err))
		}
		return
	}
	b.keysChanged(ctx)
}

// keyRow is the store's row of a read key, its listings cleaned.
func keyRow(k pgpsig.Key, trust api.PGPKeyTrust, source api.PGPKeySource, now time.Time) store.PGPKeyRow {
	r := store.PGPKeyRow{
		Fingerprint: k.Fingerprint, Data: k.Data,
		UserIDs: signature.CleanList(k.UserIDs, api.MaxSignatureUserIDs),
		Emails:  cleanEmails(k.Emails),
		KeyIDs:  k.KeyIDs,
		Trust:   trust, Source: source, FirstSeen: now, CreatedAt: k.CreatedAt,
		Algorithm: signature.Clean(k.Algorithm),
	}
	if k.ExpiresAt != nil {
		r.ExpiresAt = *k.ExpiresAt
	}
	if k.RevokedAt != nil {
		r.RevokedAt = *k.RevokedAt
	}
	return r
}

func cleanEmails(in []string) []string {
	var out []string
	for _, e := range in {
		if n := signature.NormalizeAddress(signature.Clean(e)); n != "" && !slices.Contains(out, n) {
			out = append(out, n)
			if len(out) == api.MaxSignatureEmails {
				break
			}
		}
	}
	return out
}

// --- backfill (core.Maintain) -------------------------------------------------

// Cursors of the backfills in meta.
const (
	metaOpaqueUnwrapped    = "mime.opaqueUnwrapped"
	metaSignatureVerified  = "signature.verified"
	opaqueUnwrapVersion    = "1"
	signatureBackfillBatch = 200
)

// backfillOpaque parses again the stored messages that list an S/MIME
// wrapper among their attachments (plan S14): the parser now unwraps it,
// so their text, snippet, attachments and search index become the
// content's (the board judges them again through its triggers), and each
// is queued for verification. Cursor meta mime.opaqueUnwrapped =
// "<version>:<id>" or "<version>:done".
func (b *Backend) backfillOpaque(ctx context.Context) error {
	cur, _, err := b.store.GetMeta(ctx, metaOpaqueUnwrapped)
	if err != nil {
		return err
	}
	after, done := cursorOf(cur, opaqueUnwrapVersion)
	if done {
		return nil
	}
	for ctx.Err() == nil {
		refs, last, err := b.store.OpaqueCandidates(ctx, after, signatureBackfillBatch)
		if err != nil {
			return err
		}
		for _, r := range refs {
			if err := b.reparseOpaque(ctx, r); err != nil && !isCancelled(err) {
				b.log.Warn("parse an S/MIME message again", "account", r.AccountID, "message", r.MessageID, "err", errClass(err))
			}
		}
		if last == "" {
			return b.store.SetMeta(ctx, metaOpaqueUnwrapped, opaqueUnwrapVersion+":done")
		}
		after = last
		if err := b.store.SetMeta(ctx, metaOpaqueUnwrapped, opaqueUnwrapVersion+":"+after); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (b *Backend) reparseOpaque(ctx context.Context, r store.SignatureRef) error {
	raw, err := b.readRaw(ctx, r.AccountID, r.MessageID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return err
	}
	p, err := mime.Parse(bytes.NewReader(raw), mime.DefaultLimits())
	if err != nil || !p.Opaque {
		return nil
	}
	if err := b.store.SetMessageBody(ctx, r.MessageID, *ingest.BodyUpdateOf(p)); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return err
	}
	if m, err := b.store.GetMessage(ctx, r.AccountID, r.MessageID); err == nil {
		b.messagesChanged(map[string][]string{m.AccountID: {m.FolderID}})
	}
	b.noteSignature(r.AccountID, r.MessageID)
	return nil
}

// backfillSignatures verifies the stored messages that may be signed and
// have no verdict of this verifier version (plan S2). Cursor meta
// signature.verified = "<version>:<id>" or "<version>:done".
func (b *Backend) backfillSignatures(ctx context.Context) error {
	cur, _, err := b.store.GetMeta(ctx, metaSignatureVerified)
	if err != nil {
		return err
	}
	after, done := cursorOf(cur, signature.VerifierVersion)
	if done {
		return nil
	}
	for ctx.Err() == nil {
		refs, last, err := b.store.SignatureCandidates(ctx, signature.VerifierVersion, after, signatureBackfillBatch)
		if err != nil {
			return err
		}
		for _, r := range refs {
			if _, err := b.verifyMessage(ctx, r.AccountID, r.MessageID, false); err != nil && !isCancelled(err) &&
				!errors.Is(err, store.ErrNotFound) {
				b.log.Warn("verify a stored signature", "account", r.AccountID, "message", r.MessageID, "err", errClass(err))
			}
		}
		if last == "" {
			return b.store.SetMeta(ctx, metaSignatureVerified, signature.VerifierVersion+":done")
		}
		after = last
		if err := b.store.SetMeta(ctx, metaSignatureVerified, signature.VerifierVersion+":"+after); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// cursorOf reads "<version>:<id>" / "<version>:done": another version
// starts again.
func cursorOf(cur, version string) (after string, done bool) {
	v, rest, ok := strings.Cut(cur, ":")
	if !ok || v != version {
		return "", false
	}
	if rest == "done" {
		return "", true
	}
	return rest, false
}
