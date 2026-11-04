// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/ingest"
	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/internal/revocation"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

var (
	sigArrival = time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	sigNow     = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
)

// sigRecorder counts notify.signaturesChanged.
type sigRecorder struct {
	mu      sync.Mutex
	changed int
}

func (r *sigRecorder) NewMessage(api.NewMessageNotification)           {}
func (r *sigRecorder) SyncState(api.SyncStateNotification)             {}
func (r *sigRecorder) AuthRequired(api.AuthRequiredNotification)       {}
func (r *sigRecorder) AccountsChanged(api.AccountsChangedNotification) {}
func (r *sigRecorder) MessagesChanged(api.MessagesChangedNotification) {}
func (r *sigRecorder) SignaturesChanged(api.SignaturesChangedNotification) {
	r.mu.Lock()
	r.changed++
	r.mu.Unlock()
}

func (r *sigRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.changed
}

type sigBox struct {
	t     *testing.T
	ctx   context.Context
	b     *Backend
	acc   string
	inbox string
	junk  string
	uid   uint32
	rec   *sigRecorder
}

// newSigBox is a mail account whose verifier trusts the test root
// (testdata/signatures, never a production pool) at a fixed time.
func newSigBox(t *testing.T) *sigBox {
	b, _ := newSyncBackend(t)
	acc := seedAccount(t, b, "bob@example.net")
	folders := seedFolders(t, b, acc, []store.Folder{
		{Mailbox: "INBOX", Name: "Inbox", Path: "Inbox", Role: api.RoleInbox, Subscribed: true, Selectable: true},
		{Mailbox: "Junk", Name: "Junk", Path: "Junk", Role: api.RoleJunk, Subscribed: true, Selectable: true},
	})
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "signatures", "root.pem"))
	if err != nil {
		t.Fatal(err)
	}
	p, _ := pem.Decode(data)
	root, err := x509.ParseCertificate(p.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(root)
	b.SignatureRoots = func() (*x509.CertPool, string) { return pool, "test-roots" }
	b.SignatureNow = func() time.Time { return sigNow }
	rec := &sigRecorder{}
	b.SetNotifier(rec)
	return &sigBox{t: t, ctx: context.Background(), b: b, acc: acc, inbox: folders["INBOX"].ID, junk: folders["Junk"].ID, rec: rec}
}

// store puts a testdata/mime fixture in the folder as a syncer does
// (envelope, parse, raw file), without verifying.
func (x *sigBox) store(folder, name string) store.Message {
	x.t.Helper()
	raw := readMIME(x.t, name)
	return x.storeRaw(folder, raw)
}

func (x *sigBox) storeRaw(folder string, raw []byte) store.Message {
	x.t.Helper()
	parsed, err := mime.Parse(bytes.NewReader(raw), mime.DefaultLimits())
	if err != nil {
		x.t.Fatal(err)
	}
	x.uid++
	row := &store.Message{AccountID: x.acc, FolderID: folder, UID: x.uid, Subject: parsed.Subject, From: parsed.From,
		Date: parsed.Date, InternalDate: sigArrival, RFCMessageID: parsed.MessageID, Size: int64(len(raw))}
	if err := x.b.store.UpsertMessages(x.ctx, []*store.Message{row}); err != nil {
		x.t.Fatal(err)
	}
	if err := x.b.store.SetMessageBody(x.ctx, row.ID, store.BodyUpdate{Text: parsed.Text, HasHTML: parsed.HasHTML,
		Snippet: parsed.Snippet, Attachments: parsed.Attachments, HasAttachments: parsed.HasAttachments,
		Headers: parsed.Headers, State: store.BodyFetched}); err != nil {
		x.t.Fatal(err)
	}
	if _, err := x.b.store.WriteMessageRaw(x.ctx, x.acc, row.ID, bytes.NewReader(raw), 25<<20); err != nil {
		x.t.Fatal(err)
	}
	return x.get(row.ID)
}

func (x *sigBox) get(id string) store.Message {
	x.t.Helper()
	m, err := x.b.store.GetMessage(x.ctx, x.acc, id)
	if err != nil {
		x.t.Fatal(err)
	}
	return m
}

func (x *sigBox) verify(m store.Message) {
	x.t.Helper()
	if _, err := x.b.verifyMessage(x.ctx, x.acc, m.ID, true); err != nil {
		x.t.Fatal(err)
	}
}

// drain runs the worker's queue and stale rows to the end.
func (x *sigBox) drain() { x.b.drainSignatures(x.ctx) }

func (x *sigBox) details(id string) *api.SignatureDetails {
	x.t.Helper()
	res, err := x.b.Signatures().Signature(x.ctx, api.MessageSignatureParams{MessageID: api.MessageID(id)})
	if err != nil {
		x.t.Fatal(err)
	}
	return res.Signature
}

func (x *sigBox) apiMessage(id string) api.Message {
	x.t.Helper()
	res, err := x.b.Messages().Get(x.ctx, api.MessageGetParams{AccountID: api.AccountID(x.acc), MessageID: api.MessageID(id)})
	if err != nil {
		x.t.Fatal(err)
	}
	return res.Message
}

// TestSignatureVerdict: the hook queues, the worker stores the verdict,
// the summary reaches the list and message.get, the signature part no
// longer counts as an attachment (S15), and message.signature has the
// details.
func TestSignatureVerdict(t *testing.T) {
	x := newSigBox(t)
	m := x.store(x.inbox, "smime-valid-clear.eml")
	if !m.HasAttachments {
		t.Fatal("the parse should count the signature part")
	}
	x.b.afterMailStored(x.ctx, x.acc, m.ID, ingest.Policy{})
	x.drain()
	msg := x.apiMessage(m.ID)
	if msg.Signature == nil || msg.Signature.Status != api.SignatureValid || msg.Signature.PartID != "2" ||
		msg.Signature.Signer == nil || msg.Signature.Signer.Email != "alice@example.org" {
		t.Fatalf("message.get signature = %+v", msg.Signature)
	}
	if msg.HasAttachments {
		t.Error("the signature part still counts as an attachment")
	}
	list, err := x.b.Messages().List(x.ctx, api.MessageListParams{AccountID: api.AccountID(x.acc), FolderID: api.FolderID(x.inbox)})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Messages) != 1 || list.Messages[0].Signature == nil || *list.Messages[0].Signature != (api.MessageSignature{Kind: api.SignatureSMIME, Status: api.SignatureValid}) {
		t.Fatalf("list = %+v", list.Messages)
	}
	d := x.details(m.ID)
	if d == nil || d.MessageID != api.MessageID(m.ID) || !d.ArrivedAt.Equal(sigArrival) || d.SMIME == nil || len(d.SMIME.Chain) != 3 {
		t.Fatalf("details = %+v", d)
	}
	// A message without a signature has none, and message.signature says
	// null.
	plain := x.store(x.inbox, "alternative.eml")
	x.verify(plain)
	if x.details(plain.ID) != nil || x.apiMessage(plain.ID).Signature != nil {
		t.Error("a verdict for an unsigned message")
	}
	if _, err := x.b.Signatures().Signature(x.ctx, api.MessageSignatureParams{MessageID: "m_nothing"}); apiCode(err) != api.CodeMessageNotFound {
		t.Errorf("unknown message: %v", err)
	}
}

// TestSignatureOpaqueBackfill: a message stored before the parser
// unwrapped opaque S/MIME is parsed again in core.Maintain (S14) and then
// verified; the cursor ends done.
func TestSignatureOpaqueBackfill(t *testing.T) {
	x := newSigBox(t)
	m := x.store(x.inbox, "smime-valid-opaque.eml")
	// As an older parser stored it: the wrapper as the only attachment.
	if err := x.b.store.SetMessageBody(x.ctx, m.ID, store.BodyUpdate{State: store.BodyFetched, HasAttachments: true,
		Attachments: []api.Attachment{{PartID: "1", Filename: "smime.p7m", ContentType: "application/pkcs7-mime", Size: 3000}}}); err != nil {
		t.Fatal(err)
	}
	if err := x.b.backfillOpaque(x.ctx); err != nil {
		t.Fatal(err)
	}
	got := x.get(m.ID)
	if got.Snippet != "Opaque hello from Alice." || len(got.Attachments) != 2 || got.Attachments[1].PartID != "1.3" {
		t.Fatalf("after the backfill: %q %+v", got.Snippet, got.Attachments)
	}
	if v, _, _ := x.b.store.GetMeta(x.ctx, metaOpaqueUnwrapped); v != opaqueUnwrapVersion+":done" {
		t.Errorf("cursor %q", v)
	}
	x.drain()
	s := x.apiMessage(m.ID).Signature
	if s == nil || s.Status != api.SignatureValid || !slices.Contains(s.Notes, api.SignatureNoteOpaque) || s.PartID != "" {
		t.Fatalf("signature %+v", s)
	}
}

// TestSignatureBackfill: stored signed mail without a verdict is verified
// by core.Maintain's pass; a new verifier version would start it again.
func TestSignatureBackfill(t *testing.T) {
	x := newSigBox(t)
	a := x.store(x.inbox, "smime-tampered.eml")
	c := x.store(x.inbox, "pgp-inline.eml")
	plain := x.store(x.inbox, "alternative.eml")
	if err := x.b.backfillSignatures(x.ctx); err != nil {
		t.Fatal(err)
	}
	if s := x.get(a.ID).Signature; s == nil || s.Status != api.SignatureInvalid {
		t.Errorf("tampered: %+v", s)
	}
	if s := x.get(c.ID).Signature; s == nil || s.Status != api.SignatureUnsupported || !slices.Contains(s.Notes, api.SignatureNoteInlinePGP) {
		t.Errorf("inline: %+v", s)
	}
	if x.get(plain.ID).Signature != nil {
		t.Error("plain mail has a verdict")
	}
	if v, _, _ := x.b.store.GetMeta(x.ctx, metaSignatureVerified); v != "1:done" {
		t.Errorf("cursor %q", v)
	}
}

// TestSignatureRevocation: the check on request (S9) stores the state,
// judges the message again, answers from the store within a minute, and
// reports a failure as revocationFailed with its class.
func TestSignatureRevocation(t *testing.T) {
	x := newSigBox(t)
	m := x.store(x.inbox, "smime-valid-clear.eml")
	x.verify(m)
	calls := 0
	revokedAt := sigArrival.Add(-time.Hour)
	x.b.CheckRevocation = func(_ context.Context, cert, issuer *x509.Certificate) (revocation.Result, error) {
		calls++
		if cert == nil || issuer == nil {
			t.Error("no certificate or issuer")
		}
		return revocation.Result{State: api.RevocationRevoked, RevokedAt: &revokedAt, Source: api.RevocationOCSP}, nil
	}
	before := x.rec.count()
	res, err := x.b.Signatures().Revocation(x.ctx, api.MessageSignatureRevocationParams{MessageID: api.MessageID(m.ID)})
	if err != nil {
		t.Fatal(err)
	}
	if d := res.Signature; d.Status != api.SignatureRevoked || d.SMIME.Revocation.State != api.RevocationRevoked ||
		d.SMIME.Revocation.Source != api.RevocationOCSP || d.RevokedAt == nil {
		t.Fatalf("after the check: %+v %+v", d, d.SMIME.Revocation)
	}
	if x.rec.count() <= before {
		t.Error("no notify.signaturesChanged")
	}
	if _, err := x.b.Signatures().Revocation(x.ctx, api.MessageSignatureRevocationParams{MessageID: api.MessageID(m.ID)}); err != nil || calls != 1 {
		t.Errorf("within a minute: %d calls, %v", calls, err)
	}
	// A minute later the network is asked again; it fails.
	x.b.SignatureNow = func() time.Time { return sigNow.Add(2 * time.Minute) }
	x.b.CheckRevocation = func(context.Context, *x509.Certificate, *x509.Certificate) (revocation.Result, error) {
		return revocation.Result{}, &revocation.Error{Reason: api.RevocationFailTimeout}
	}
	_, err = x.b.Signatures().Revocation(x.ctx, api.MessageSignatureRevocationParams{MessageID: api.MessageID(m.ID)})
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.CodeRevocationFailed || ae.Data.(api.RevocationFailedData).Reason != api.RevocationFailTimeout {
		t.Errorf("failure: %v", err)
	}
	// Not S/MIME: invalidArgument.
	p := x.store(x.inbox, "pgp-inline.eml")
	x.verify(p)
	if _, err := x.b.Signatures().Revocation(x.ctx, api.MessageSignatureRevocationParams{MessageID: api.MessageID(p.ID)}); apiCode(err) != api.CodeInvalidArgument {
		t.Errorf("pgp: %v", err)
	}
}

// TestSignaturePGPKeys: the Autocrypt key of new mail is taken (seen →
// validSeen), confirming it makes the verdict valid, removing it unknown;
// the list and the notifications follow.
func TestSignaturePGPKeys(t *testing.T) {
	x := newSigBox(t)
	m := x.store(x.inbox, "pgp-valid.eml")
	x.verify(m)
	if s := x.get(m.ID).Signature; s == nil || s.Status != api.SignatureValidSeen {
		t.Fatalf("after Autocrypt: %+v", s)
	}
	keys, err := x.b.PGP().KeyList(x.ctx, api.PGPKeyListParams{})
	if err != nil || len(keys.Keys) != 1 || keys.Keys[0].Source != api.PGPSourceAutocrypt || keys.Keys[0].Trust != api.PGPKeySeen ||
		keys.Keys[0].Emails[0] != "alice@example.org" {
		t.Fatalf("keys = %+v %v", keys, err)
	}
	fpr := keys.Keys[0].Fingerprint
	v, err := x.b.PGP().KeyVerify(x.ctx, api.PGPKeyVerifyParams{Fingerprint: fpr, Verified: true})
	if err != nil || v.Key.Trust != api.PGPKeyVerified {
		t.Fatalf("verify: %+v %v", v, err)
	}
	x.drain()
	if s := x.get(m.ID).Signature; s == nil || s.Status != api.SignatureValid {
		t.Fatalf("after confirming: %+v", s)
	}
	if _, err := x.b.PGP().KeyVerify(x.ctx, api.PGPKeyVerifyParams{Fingerprint: "00" + fpr[2:]}); apiCode(err) != api.CodeInvalidArgument {
		t.Errorf("unknown key: %v", err)
	}
	if _, err := x.b.PGP().KeyRemove(x.ctx, api.PGPKeyRemoveParams{Fingerprint: fpr}); err != nil {
		t.Fatal(err)
	}
	// The worker would take the Autocrypt key again on a new message; the
	// stale verdict is computed without it.
	if _, err := x.b.verifyMessage(x.ctx, x.acc, m.ID, false); err != nil {
		t.Fatal(err)
	}
	if s := x.get(m.ID).Signature; s == nil || s.Status != api.SignatureUnknownKey {
		t.Fatalf("after removing: %+v", s)
	}
	d := x.details(m.ID)
	if d.PGP == nil || d.PGP.Importable == nil || !d.PGP.Importable.Autocrypt {
		t.Fatalf("no offer: %+v", d.PGP)
	}
	res, err := x.b.PGP().KeyImport(x.ctx, api.PGPKeyImportParams{MessageID: api.MessageID(m.ID), Autocrypt: true})
	if err != nil || res.Added != 1 || res.Keys[0].Source != api.PGPSourceAutocrypt {
		t.Fatalf("import: %+v %v", res, err)
	}
	if res, err := x.b.PGP().KeyImport(x.ctx, api.PGPKeyImportParams{MessageID: api.MessageID(m.ID), Autocrypt: true}); err != nil ||
		res.Added != 0 || res.Updated != 0 || len(res.Keys) != 1 {
		t.Errorf("again: %+v %v", res, err)
	}
	if x.rec.count() < 3 {
		t.Errorf("%d notifications", x.rec.count())
	}
}

// TestSignatureAutocryptRules: no key from list mail, Junk, another
// address; a second key for a known address is only offered (keyChanged).
func TestSignatureAutocryptRules(t *testing.T) {
	x := newSigBox(t)
	for _, c := range []struct{ folder, name string }{
		{x.inbox, "pgp-autocrypt-bulk.eml"},
		{x.inbox, "pgp-autocrypt-other-addr.eml"},
		{x.junk, "pgp-valid.eml"},
	} {
		x.verify(x.store(c.folder, c.name))
	}
	if n, _ := x.b.store.CountPGPKeys(x.ctx); n != 0 {
		t.Fatalf("%d keys taken", n)
	}
	carol, err := os.ReadFile(filepath.Join("..", "..", "testdata", "signatures", "carol-1.asc"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x.b.PGP().KeyImport(x.ctx, api.PGPKeyImportParams{Armored: string(carol)}); err != nil {
		t.Fatal(err)
	}
	m := x.store(x.inbox, "pgp-key-changed.eml")
	x.verify(m)
	if n, _ := x.b.store.CountPGPKeys(x.ctx); n != 1 {
		t.Fatalf("the new key for carol was taken: %d keys", n)
	}
	d := x.details(m.ID)
	if d.Status != api.SignatureUnknownKey || !slices.Contains(d.Notes, api.SignatureNoteKeyChanged) || d.PGP.Importable == nil {
		t.Fatalf("details %+v %+v", d, d.PGP)
	}
	if _, err := x.b.PGP().KeyImport(x.ctx, api.PGPKeyImportParams{MessageID: api.MessageID(m.ID), Autocrypt: true}); err != nil {
		t.Fatal(err)
	}
	x.drain()
	d = x.details(m.ID)
	if d.Status != api.SignatureValidSeen || !slices.Contains(d.Notes, api.SignatureNoteKeyChanged) || d.PGP.PreviousFingerprint == "" {
		t.Fatalf("after import %+v %+v", d, d.PGP)
	}
}

// TestSignatureKeyImport: the import's sources and refusals.
func TestSignatureKeyImport(t *testing.T) {
	x := newSigBox(t)
	m := x.store(x.inbox, "pgp-attached-key.eml")
	x.verify(m)
	d := x.details(m.ID)
	if d == nil || d.PGP == nil || d.PGP.Importable == nil || d.PGP.Importable.PartID == "" {
		t.Fatalf("no attached offer: %+v", d)
	}
	res, err := x.b.PGP().KeyImport(x.ctx, api.PGPKeyImportParams{MessageID: api.MessageID(m.ID), PartID: d.PGP.Importable.PartID})
	if err != nil || res.Added != 1 || res.Keys[0].Source != api.PGPSourceAttached {
		t.Fatalf("attached: %+v %v", res, err)
	}
	x.drain()
	if s := x.get(m.ID).Signature; s == nil || s.Status != api.SignatureValidSeen {
		t.Errorf("after import: %+v", s)
	}
	for name, p := range map[string]api.PGPKeyImportParams{
		"nothing":          {},
		"two":              {Armored: "x", Data: []byte("y")},
		"part without msg": {PartID: "2"},
		"msg alone":        {MessageID: api.MessageID(m.ID)},
	} {
		if _, err := x.b.PGP().KeyImport(x.ctx, p); apiCode(err) != api.CodeInvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
	var ae *api.Error
	_, err = x.b.PGP().KeyImport(x.ctx, api.PGPKeyImportParams{Armored: "-----BEGIN PGP PRIVATE KEY BLOCK-----\n\nxx\n-----END PGP PRIVATE KEY BLOCK-----\n"})
	if !errors.As(err, &ae) || ae.Code != api.CodeInvalidArgument || ae.Data.(api.PGPImportErrorData).Reason != api.PGPImportSecretKey {
		t.Errorf("secret: %v", err)
	}
	if _, err := x.b.PGP().KeyImport(x.ctx, api.PGPKeyImportParams{MessageID: api.MessageID(m.ID), PartID: "9"}); apiCode(err) != api.CodePartNotFound {
		t.Errorf("no part: %v", err)
	}
}

// TestSignatureWorkerSurvives: a queued message that vanished, an
// unsigned one and broken ASN.1 never stop the worker (S17).
func TestSignatureWorkerSurvives(t *testing.T) {
	x := newSigBox(t)
	deep := x.store(x.inbox, "smime-asn1-deep.eml")
	x.b.noteSignature(x.acc, "m_gone")
	x.b.noteSignature(x.acc, deep.ID)
	good := x.store(x.inbox, "smime-valid-clear.eml")
	x.b.noteSignature(x.acc, good.ID)
	x.drain()
	if s := x.get(deep.ID).Signature; s == nil || s.Status != api.SignatureError {
		t.Errorf("deep: %+v", s)
	}
	if s := x.get(good.ID).Signature; s == nil || s.Status != api.SignatureValid {
		t.Errorf("good: %+v", s)
	}
	// Another set of roots makes every S/MIME verdict stale by its trust
	// key; without the test root the message is untrusted.
	x.b.SignatureRoots = func() (*x509.CertPool, string) { return x509.NewCertPool(), "other" }
	x.b.refreshRoots(x.ctx)
	x.drain()
	if s := x.get(good.ID).Signature; s == nil || s.Status != api.SignatureUntrusted {
		t.Errorf("other roots: %+v", s)
	}
}
