// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package signature

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/internal/pgpsig"
	"github.com/schotek/malachi/backend/pkg/api"
)

const (
	mimeDir = "../../testdata/mime"
	sigDir  = "../../testdata/signatures"
)

var (
	arrival = time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	now     = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
)

// testRoots is the pool with the test root (never a production pool).
func testRoots(t testing.TB) *x509.CertPool {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(sigDir, "root.pem"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := pem.Decode(data)
	c, err := x509.ParseCertificate(b.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(c)
	return pool
}

// testKey reads an armoured key of testdata/signatures as a stored key.
func testKey(t testing.TB, name string, trust api.PGPKeyTrust, firstSeen time.Time) StoredKey {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(sigDir, name))
	if err != nil {
		t.Fatal(err)
	}
	keys, err := pgpsig.ReadKeys(data, now)
	if err != nil || len(keys) != 1 {
		t.Fatalf("%s: %v (%d keys)", name, err, len(keys))
	}
	k := keys[0]
	return StoredKey{Entity: k.Entity, Fingerprint: k.Fingerprint, Trust: trust, Source: api.PGPSourceImport, FirstSeen: firstSeen, Emails: k.Emails}
}

func readFixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(mimeDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func attachmentsOf(t testing.TB, raw []byte) []api.Attachment {
	p, err := mime.Parse(strings.NewReader(string(raw)), mime.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return p.Attachments
}

func TestCheckFixtures(t *testing.T) {
	roots := testRoots(t)
	early := now.AddDate(-1, 0, 0)
	alice := testKey(t, "alice-pgp.asc", api.PGPKeyVerified, early)
	aliceSeen := testKey(t, "alice-pgp.asc", api.PGPKeySeen, early)
	robert := testKey(t, "robert-revoked.asc", api.PGPKeySeen, early)
	carol1 := testKey(t, "carol-1.asc", api.PGPKeySeen, early)
	carol2 := testKey(t, "carol-2.asc", api.PGPKeySeen, now.AddDate(0, 0, -1))
	daveBidi := testKey(t, "dave-bidi.asc", api.PGPKeyVerified, early)

	cases := []struct {
		file   string
		at     time.Time
		keys   []StoredKey
		none   bool
		status api.SignatureStatus
		reason api.SignatureReason
		notes  []api.SignatureNote
		email  string
		kind   api.SignatureKind
		check  func(t *testing.T, o *Outcome)
	}{
		{file: "smime-valid-clear.eml", status: api.SignatureValid, email: "alice@example.org",
			notes: []api.SignatureNote{api.SignatureNoteRevocationUnchecked},
			check: func(t *testing.T, o *Outcome) {
				d := o.Details
				if d.PartID != "2" || d.SMIME == nil || len(d.SMIME.Chain) != 3 || !d.SMIME.Chain[2].SystemRoot ||
					!d.SMIME.Revocation.Checkable || d.Algorithm != "RSA 2048 / SHA-256" || d.SignedAt == nil ||
					len(d.Signer.Fingerprint) != 64 || o.Issuer == nil {
					t.Errorf("details = %+v smime=%+v", d, d.SMIME)
				}
			}},
		{file: "smime-valid-opaque.eml", status: api.SignatureValid,
			notes: []api.SignatureNote{api.SignatureNoteRevocationUnchecked, api.SignatureNoteOpaque},
			check: func(t *testing.T, o *Outcome) {
				if o.Details.PartID != "" {
					t.Errorf("opaque partId = %q", o.Details.PartID)
				}
			}},
		{file: "smime-opaque-in-opaque.eml", status: api.SignatureValid,
			notes: []api.SignatureNote{api.SignatureNoteRevocationUnchecked, api.SignatureNoteOpaque}},
		{file: "smime-tampered.eml", status: api.SignatureInvalid, reason: api.SignatureReasonDigestMismatch},
		{file: "smime-lf.eml", status: api.SignatureValid},
		{file: "smime-other-cert.eml", status: api.SignatureInvalid, reason: api.SignatureReasonBadSignature},
		{file: "smime-from-mismatch.eml", status: api.SignatureMismatch, email: "alice@example.org"},
		{file: "smime-multiple-signers.eml", status: api.SignatureUnsupported, reason: api.SignatureReasonMultipleSigners},
		{file: "smime-nested-signed.eml", status: api.SignatureValid},
		{file: "smime-forwarded.eml", none: true},
		{file: "smime-signed-plus-html.eml", none: true},
		{file: "smime-footer.eml", status: api.SignatureValid,
			notes: []api.SignatureNote{api.SignatureNoteRevocationUnchecked, api.SignatureNotePartialFooter},
			check: func(t *testing.T, o *Outcome) {
				if o.Details.PartID != "1.2" {
					t.Errorf("footer partId = %q", o.Details.PartID)
				}
			}},
		{file: "smime-short-cert.eml", at: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC), status: api.SignatureValid,
			notes: []api.SignatureNote{api.SignatureNoteExpiredSince, api.SignatureNoteRevocationUnchecked},
			check: func(t *testing.T, o *Outcome) {
				if o.Details.ExpiredAt == nil {
					t.Error("no expiredAt")
				}
			}},
		{file: "smime-short-cert.eml", status: api.SignatureUntrusted, reason: api.SignatureReasonExpired},
		{file: "smime-short-cert.eml", at: time.Date(2023, 6, 1, 0, 0, 0, 0, time.UTC), status: api.SignatureUntrusted, reason: api.SignatureReasonNotYetValid},
		{file: "smime-sha1.eml", status: api.SignatureWeak, reason: api.SignatureReasonSHA1},
		{file: "smime-md5.eml", status: api.SignatureInvalid, reason: api.SignatureReasonMD5},
		{file: "smime-rsa1024.eml", status: api.SignatureWeak, reason: api.SignatureReasonShortKey},
		{file: "smime-ecdsa.eml", status: api.SignatureValid, check: func(t *testing.T, o *Outcome) {
			if o.Details.Algorithm != "ECDSA P-256 / SHA-256" {
				t.Errorf("algorithm %q", o.Details.Algorithm)
			}
		}},
		{file: "smime-dsa.eml", status: api.SignatureUnsupported, reason: api.SignatureReasonAlgorithm},
		{file: "smime-no-email-protection.eml", status: api.SignatureInvalid, reason: api.SignatureReasonKeyUsage},
		{file: "smime-chain-20.eml", status: api.SignatureError, reason: api.SignatureReasonLimit},
		{file: "smime-self-signed.eml", status: api.SignatureUntrusted, reason: api.SignatureReasonSelfSigned},
		{file: "smime-unknown-ca.eml", status: api.SignatureUntrusted, reason: api.SignatureReasonUnknownIssuer},
		{file: "smime-asn1-truncated.eml", status: api.SignatureInvalid, reason: api.SignatureReasonMalformed},
		{file: "smime-asn1-deep.eml", status: api.SignatureError, reason: api.SignatureReasonLimit},
		{file: "smime-asn1-huge.eml", status: api.SignatureInvalid, reason: api.SignatureReasonMalformed},
		{file: "smime-bidi-cn.eml", status: api.SignatureValid, check: func(t *testing.T, o *Outcome) {
			n := o.Details.Signer.Name
			for _, r := range n {
				if unicode.Is(unicode.Bidi_Control, r) || unicode.IsControl(r) {
					t.Errorf("name %q keeps %U", n, r)
				}
			}
			if o.Details.Signer.Email != "alice@example.org" {
				t.Errorf("DN address %q", o.Details.Signer.Email)
			}
		}},
		{file: "smime-protected-headers.eml", status: api.SignatureValid,
			notes: []api.SignatureNote{api.SignatureNoteRevocationUnchecked, api.SignatureNoteProtectedHeadersDiffer},
			check: func(t *testing.T, o *Outcome) {
				ph := o.Details.ProtectedHeaders
				if len(ph) != 1 || ph[0].Name != api.ProtectedSubject || ph[0].Value != "The real subject" {
					t.Errorf("protected = %+v", ph)
				}
			}},

		{file: "pgp-valid.eml", keys: []StoredKey{alice}, status: api.SignatureValid, kind: api.SignaturePGP, email: "alice@example.org",
			check: func(t *testing.T, o *Outcome) {
				p := o.Details.PGP
				if p == nil || p.KeyTrust != api.PGPKeyVerified || p.PrimaryFingerprint == "" || len(p.KeyID) != 16 ||
					o.Autocrypt == nil || p.Importable != nil || o.Details.PartID != "2" {
					t.Errorf("pgp = %+v autocrypt=%v", p, o.Autocrypt)
				}
			}},
		{file: "pgp-valid.eml", keys: []StoredKey{aliceSeen}, status: api.SignatureValidSeen, kind: api.SignaturePGP},
		{file: "pgp-valid.eml", status: api.SignatureUnknownKey, kind: api.SignaturePGP, check: func(t *testing.T, o *Outcome) {
			if o.Details.PGP.Importable == nil || !o.Details.PGP.Importable.Autocrypt {
				t.Errorf("no Autocrypt offer: %+v", o.Details.PGP)
			}
		}},
		{file: "pgp-lf.eml", keys: []StoredKey{alice}, status: api.SignatureValid, kind: api.SignaturePGP},
		{file: "pgp-tampered.eml", keys: []StoredKey{alice}, status: api.SignatureInvalid, reason: api.SignatureReasonBadSignature, kind: api.SignaturePGP},
		{file: "pgp-mismatch.eml", keys: []StoredKey{alice}, status: api.SignatureMismatch, kind: api.SignaturePGP},
		{file: "pgp-unknown-key.eml", keys: []StoredKey{alice}, status: api.SignatureUnknownKey, kind: api.SignaturePGP},
		{file: "pgp-inline.eml", status: api.SignatureUnsupported, kind: api.SignaturePGP,
			notes: []api.SignatureNote{api.SignatureNoteInlinePGP}},
		{file: "pgp-autocrypt-other-addr.eml", status: api.SignatureUnknownKey, kind: api.SignaturePGP, check: func(t *testing.T, o *Outcome) {
			if o.Autocrypt != nil || o.Details.PGP.Importable != nil {
				t.Error("Autocrypt for another address is taken")
			}
		}},
		{file: "pgp-autocrypt-bulk.eml", none: true},
		{file: "pgp-revoked-key.eml", keys: []StoredKey{robert}, status: api.SignatureRevoked, kind: api.SignaturePGP},
		{file: "pgp-key-changed.eml", keys: []StoredKey{carol1}, status: api.SignatureUnknownKey, kind: api.SignaturePGP,
			notes: []api.SignatureNote{api.SignatureNoteKeyChanged},
			check: func(t *testing.T, o *Outcome) {
				p := o.Details.PGP
				if p.PreviousFingerprint != carol1.Fingerprint || p.Importable == nil || p.Importable.Fingerprint != carol2.Fingerprint {
					t.Errorf("pgp = %+v", p)
				}
			}},
		{file: "pgp-key-changed.eml", keys: []StoredKey{carol1, carol2}, status: api.SignatureValidSeen, kind: api.SignaturePGP,
			notes: []api.SignatureNote{api.SignatureNoteKeyChanged}},
		{file: "pgp-attached-key.eml", status: api.SignatureUnknownKey, kind: api.SignaturePGP, check: func(t *testing.T, o *Outcome) {
			p := o.Details.PGP
			if p.Importable == nil || p.Importable.PartID == "" {
				t.Errorf("no attached offer: %+v", p)
			}
		}},
		{file: "pgp-bidi-uid.eml", keys: []StoredKey{daveBidi}, status: api.SignatureValid, kind: api.SignaturePGP, check: func(t *testing.T, o *Outcome) {
			for _, s := range append([]string{o.Details.Signer.Name}, o.Details.PGP.UserIDs...) {
				for _, r := range s {
					if unicode.Is(unicode.Bidi_Control, r) || unicode.IsControl(r) {
						t.Errorf("%q keeps %U", s, r)
					}
				}
			}
		}},
	}
	for _, c := range cases {
		name := c.file + "@" + c.at.Format("2006")
		t.Run(name, func(t *testing.T) {
			raw := readFixture(t, c.file)
			at := c.at
			if at.IsZero() {
				at = arrival
			}
			o, err := Check(Input{Raw: raw, At: at, Now: now, Roots: roots, Keys: c.keys, Attachments: attachmentsOf(t, raw)})
			if c.none {
				if !errors.Is(err, ErrNone) {
					t.Fatalf("want no verdict, got %+v, %v", o, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			d := o.Details
			if d.Status != c.status || d.Reason != c.reason {
				t.Errorf("verdict %s/%s, want %s/%s (notes %v)", d.Status, d.Reason, c.status, c.reason, d.Notes)
			}
			kind := c.kind
			if kind == "" {
				kind = api.SignatureSMIME
			}
			if d.Kind != kind {
				t.Errorf("kind %s", d.Kind)
			}
			if c.notes != nil && !slices.Equal(d.Notes, c.notes) {
				t.Errorf("notes %v, want %v", d.Notes, c.notes)
			}
			if c.email != "" && (d.Signer == nil || d.Signer.Email != c.email) {
				t.Errorf("signer %+v", d.Signer)
			}
			switch d.Status {
			case api.SignatureValid, api.SignatureValidSeen, api.SignatureMismatch, api.SignatureUntrusted, api.SignatureWeak, api.SignatureRevoked:
				if d.Signer == nil {
					t.Error("no signer")
				}
			}
			if c.check != nil {
				c.check(t, o)
			}
		})
	}
}

// TestRebuiltIsUnsupported: on a server that rebuilds MIME a clear
// signature that no longer matches is not called invalid.
func TestRebuiltIsUnsupported(t *testing.T) {
	raw := readFixture(t, "smime-tampered.eml")
	o, err := Check(Input{Raw: raw, At: arrival, Now: now, Roots: testRoots(t), Rebuilt: true})
	if err != nil || o.Details.Status != api.SignatureUnsupported || o.Details.Reason != api.SignatureReasonRebuilt {
		t.Fatalf("%+v %v", o, err)
	}
}

// TestRevocationStates: a stored revocation before the arrival makes the
// verdict revoked, one after a note.
func TestRevocationStates(t *testing.T) {
	raw := readFixture(t, "smime-valid-clear.eml")
	before := arrival.Add(-time.Hour)
	after := arrival.Add(time.Hour)
	for _, c := range []struct {
		rv     Revocation
		status api.SignatureStatus
		note   api.SignatureNote
	}{
		{Revocation{State: api.RevocationGood, CheckedAt: &now}, api.SignatureValid, api.SignatureNoteRevocationGood},
		{Revocation{State: api.RevocationRevoked, RevokedAt: &before, CheckedAt: &now}, api.SignatureRevoked, ""},
		{Revocation{State: api.RevocationRevoked, RevokedAt: &after, CheckedAt: &now}, api.SignatureValid, api.SignatureNoteRevokedAfter},
	} {
		o, err := Check(Input{Raw: raw, At: arrival, Now: now, Roots: testRoots(t),
			Revocation: func(string) (Revocation, bool) { return c.rv, true }})
		if err != nil {
			t.Fatal(err)
		}
		if o.Details.Status != c.status || (c.note != "" && !slices.Contains(o.Details.Notes, c.note)) {
			t.Errorf("%+v: %s %v", c.rv, o.Details.Status, o.Details.Notes)
		}
	}
}

// TestNoRoots: without the test root everything S/MIME is untrusted.
func TestNoRoots(t *testing.T) {
	o, err := Check(Input{Raw: readFixture(t, "smime-valid-clear.eml"), At: arrival, Now: now})
	if err != nil || o.Details.Status != api.SignatureUntrusted || o.Details.Reason != api.SignatureReasonUnknownIssuer {
		t.Fatalf("%+v %v", o, err)
	}
}

func TestCompose(t *testing.T) {
	from := []string{"a@example.org"}
	cases := []struct {
		f    Findings
		want api.SignatureStatus
	}{
		{Findings{Verified: true, Identities: []string{"A@Example.org"}, From: from}, api.SignatureValid},
		{Findings{Verified: true, Seen: true, Identities: []string{"a@example.org"}, From: from}, api.SignatureValidSeen},
		{Findings{Verified: true, Identities: []string{"b@example.org"}, From: from}, api.SignatureMismatch},
		// Two From addresses never match (S5).
		{Findings{Verified: true, Identities: []string{"a@example.org"}, From: []string{"a@example.org", "b@example.org"}}, api.SignatureMismatch},
		{Findings{Verified: true, Invalid: api.SignatureReasonKeyUsage, Identities: []string{"b@x"}, From: from}, api.SignatureInvalid},
		{Findings{Verified: true, Revoked: true, Identities: []string{"b@x"}, From: from}, api.SignatureRevoked},
		{Findings{Verified: true, Untrusted: api.SignatureReasonExpired, Weak: api.SignatureReasonSHA1, Identities: from, From: from}, api.SignatureUntrusted},
		{Findings{UnknownKey: true, Weak: api.SignatureReasonSHA1}, api.SignatureUnknownKey},
		{Findings{Verified: true, Weak: api.SignatureReasonSHA1, Identities: from, From: from}, api.SignatureWeak},
		{Findings{Unsupported: api.SignatureReasonAlgorithm}, api.SignatureUnsupported},
		{Findings{Error: api.SignatureReasonLimit}, api.SignatureError},
		{Findings{}, api.SignatureError},
	}
	for i, c := range cases {
		if got := Compose(c.f).Status; got != c.want {
			t.Errorf("%d: %s, want %s", i, got, c.want)
		}
	}
}

// TestIdentityNeedsOneFrom: a second From field, or a From the store shows
// that is not the header's, never matches the signer (S5).
func TestIdentityNeedsOneFrom(t *testing.T) {
	raw := readFixture(t, "smime-valid-clear.eml")
	two := append([]byte("From: Eve <eve@example.com>\r\n"), raw...)
	o, err := Check(Input{Raw: two, At: arrival, Now: now, Roots: testRoots(t)})
	if err != nil || o.Details.Status != api.SignatureMismatch {
		t.Errorf("two From fields: %+v %v", o, err)
	}
	o, err = Check(Input{Raw: raw, At: arrival, Now: now, Roots: testRoots(t), ShownFrom: []string{"eve@example.com"}})
	if err != nil || o.Details.Status != api.SignatureMismatch {
		t.Errorf("another shown From: %+v %v", o, err)
	}
	o, err = Check(Input{Raw: raw, At: arrival, Now: now, Roots: testRoots(t), ShownFrom: []string{"Alice@Example.org"}})
	if err != nil || o.Details.Status != api.SignatureValid {
		t.Errorf("the same From: %+v %v", o, err)
	}
	// A key made after the message arrived cannot have signed it.
	k := testKey(t, "alice-pgp.asc", api.PGPKeyVerified, now)
	o, err = Check(Input{Raw: readFixture(t, "pgp-valid.eml"), At: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), Now: now, Keys: []StoredKey{k}})
	if err != nil || o.Details.Status != api.SignatureUntrusted || o.Details.Reason != api.SignatureReasonNotYetValid {
		t.Errorf("key younger than the message: %+v %v", o.Details, err)
	}
}
