// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package signature

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"

	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/internal/pgpsig"
	"github.com/schotek/malachi/backend/internal/smime"
	"github.com/schotek/malachi/backend/pkg/api"
)

// StoredKey is an OpenPGP key the daemon keeps, with its trust.
type StoredKey struct {
	Entity      *openpgp.Entity
	Fingerprint string
	Trust       api.PGPKeyTrust // seen or verified
	Source      api.PGPKeySource
	FirstSeen   time.Time
	Emails      []string // normalised
}

// Revocation is the stored revocation state of a certificate.
type Revocation struct {
	State     api.RevocationState
	CheckedAt *time.Time
	RevokedAt *time.Time
	Source    api.RevocationSource
}

// Input is what Check judges a message by.
type Input struct {
	Raw []byte
	// At is the message's arrival (plan S8), Now the present.
	At, Now time.Time
	// Roots are the trusted S/MIME roots (plan S7).
	Roots *x509.CertPool
	// Keys are the stored OpenPGP keys.
	Keys []StoredKey
	// Revocation returns the stored revocation state of a certificate by
	// its SHA-256 (hex); nil = never checked.
	Revocation func(certSHA256 string) (Revocation, bool)
	// Attachments are the message's parts as the parser lists them (the
	// keys it carries are offered for import).
	Attachments []api.Attachment
	// ShownFrom is the From the store keeps for the message (the server's
	// envelope may win over the header): when it is not the header's only
	// From address, no identity matches. nil = not known.
	ShownFrom []string
	// Rebuilt: the account's server rebuilds MIME (Microsoft Graph), so a
	// clear signature that does not match is unsupported/rebuilt, not
	// invalid.
	Rebuilt bool
}

// Outcome is the verdict of a message and what it rests on.
type Outcome struct {
	// Details are the dialog's details (MessageID and ArrivedAt are the
	// caller's to fill); every string is cleaned.
	Details api.SignatureDetails
	// CertSHA256 is the S/MIME signer's certificate; KeyID the OpenPGP
	// signing key's; FromEmail the From address judged against.
	CertSHA256 string
	KeyID      string
	FromEmail  string
	// Signer and Issuer are the S/MIME signer's certificate and its issuer
	// (for a revocation check; nil when unknown).
	Signer, Issuer *x509.Certificate
	// Autocrypt is the message's Autocrypt key for its From address, when
	// it has one (the caller stores it or offers it, plan §5).
	Autocrypt *pgpsig.Autocrypt
}

// Check finds and verifies the signature of the whole message. ErrNone
// when the message has none; a verifier's panic is an error verdict (S17).
func Check(in Input) (out *Outcome, err error) {
	loc, err := Locate(in.Raw)
	if err != nil {
		return nil, err
	}
	defer func() {
		if p := recover(); p != nil {
			out = &Outcome{Details: api.SignatureDetails{SignatureSummary: api.SignatureSummary{
				Kind: loc.Kind, Status: api.SignatureError}, Reason: api.SignatureReasonInternal}}
			err = nil
		}
	}()
	from := FromAddresses(in.Raw)
	if in.ShownFrom != nil && (len(from) != 1 || len(in.ShownFrom) != 1 ||
		NormalizeAddress(in.ShownFrom[0]) != NormalizeAddress(from[0])) {
		from = nil
	}
	out = &Outcome{}
	if len(from) == 1 {
		out.FromEmail = NormalizeAddress(from[0])
	}
	f := Findings{Kind: loc.Kind, From: from}
	d := &out.Details
	d.Kind = loc.Kind
	d.PartID = loc.SigPartID
	if loc.Opaque {
		f.Notes = append(f.Notes, api.SignatureNoteOpaque)
		d.PartID = ""
	}
	if loc.Footer {
		f.Notes = append(f.Notes, api.SignatureNotePartialFooter)
	}
	switch {
	case loc.Inline:
		d.PartID = ""
		f.Notes = append(f.Notes, api.SignatureNoteInlinePGP)
		f.Unsupported = api.SignatureReasonFormat
		v := Compose(f)
		d.Status, d.Notes = v.Status, v.Notes
		d.Reason = "" // inline OpenPGP has the note only
		return out, nil
	case loc.Malformed:
		f.Invalid = api.SignatureReasonMalformed
	case loc.Kind == api.SignatureSMIME:
		if err := checkSMIME(in, loc, &f, out); err != nil {
			return nil, err
		}
	case loc.Kind == api.SignaturePGP:
		checkPGP(in, loc, &f, out)
	}
	if in.Rebuilt && !loc.Opaque && (f.Invalid == api.SignatureReasonDigestMismatch || f.Invalid == api.SignatureReasonBadSignature) {
		f.Invalid, f.Unsupported = "", api.SignatureReasonRebuilt
	}
	// Protected headers (S6), only of a signature that matched.
	if f.Verified {
		signed := loc.Signed
		if loc.Opaque {
			if c, err := smime.ExtractContent(loc.Signature); err == nil {
				signed = c
			}
		}
		if ph := ProtectedHeaders(signed, in.Raw); len(ph) > 0 {
			d.ProtectedHeaders = ph
			f.Notes = append(f.Notes, api.SignatureNoteProtectedHeadersDiffer)
		}
	}
	v := Compose(f)
	d.Status, d.Reason, d.Notes = v.Status, v.Reason, v.Notes
	switch d.Status {
	case api.SignatureValid, api.SignatureValidSeen, api.SignatureMismatch, api.SignatureRevoked:
		d.Reason = ""
	}
	if d.Signer != nil && v.Matched != "" {
		d.Signer.Email = Clean(v.Matched)
	}
	if d.Signer != nil && *d.Signer == (api.SignatureSigner{}) {
		d.Signer = nil
	}
	if d.Status != api.SignatureUntrusted && d.Reason == api.SignatureReasonExpired {
		d.Reason = ""
	}
	return out, nil
}

func checkSMIME(in Input, loc *Located, f *Findings, out *Outcome) error {
	d := &out.Details
	sd, err := smime.Parse(loc.Signature)
	switch {
	case errors.Is(err, smime.ErrLimit):
		f.Error = api.SignatureReasonLimit
		return nil
	case errors.Is(err, smime.ErrNotSignedData) && loc.Opaque:
		return ErrNone // enveloped data: not a signature (phase 2)
	case err != nil:
		f.Invalid = api.SignatureReasonMalformed
		return nil
	}
	if loc.Opaque && (sd.EContent == nil || !sd.EContentType.Equal(oidData)) {
		f.Invalid = api.SignatureReasonMalformed
		return nil
	}
	opts := smime.Options{Roots: in.Roots, At: in.At, Now: in.Now}
	var r smime.Result
	if loc.Opaque {
		r = smime.Verify(sd, nil, opts)
	} else {
		canon := Canonical(loc.Signed)
		r = smime.Verify(sd, canon, opts)
		if r.Invalid == api.SignatureReasonDigestMismatch && !bytes.Equal(canon, loc.Signed) {
			if again := smime.Verify(sd, loc.Signed, opts); again.Invalid != api.SignatureReasonDigestMismatch {
				r = again
			}
		}
	}
	f.Verified = r.Verified
	f.Invalid, f.Untrusted, f.Weak, f.Unsupported, f.Error = r.Invalid, r.Untrusted, r.Weak, r.Unsupported, r.Error
	f.Identities = r.Emails
	d.SignedAt = r.SigningTime
	d.Algorithm = Clean(r.Algorithm)
	if r.Signer == nil {
		return nil
	}
	sum := sha256.Sum256(r.Signer.Raw)
	out.CertSHA256 = hex.EncodeToString(sum[:])
	out.Signer = r.Signer
	d.Signer = &api.SignatureSigner{Name: Clean(r.Name), Fingerprint: out.CertSHA256}
	if len(r.Emails) > 0 {
		d.Signer.Email = Clean(r.Emails[0])
	}
	sm := &api.SMIMEDetails{Chain: []api.SignatureCertificate{}}
	for i, c := range r.Chain {
		if i >= api.MaxSignatureChain {
			break
		}
		cs := sha256.Sum256(c.Raw)
		sc := api.SignatureCertificate{
			SHA256:     hex.EncodeToString(cs[:]),
			Subject:    Clean(c.Subject.String()),
			Issuer:     Clean(c.Issuer.String()),
			Emails:     CleanList(smime.Emails(c), api.MaxSignatureEmails),
			NotBefore:  c.NotBefore.UTC(),
			NotAfter:   c.NotAfter.UTC(),
			SelfSigned: bytes.Equal(c.RawSubject, c.RawIssuer) && c.CheckSignature(c.SignatureAlgorithm, c.RawTBSCertificate, c.Signature) == nil,
		}
		if r.SystemRoot && i == len(r.Chain)-1 {
			sc.SystemRoot = true
		}
		sm.Chain = append(sm.Chain, sc)
	}
	if len(r.Chain) > 1 {
		out.Issuer = r.Chain[1]
	} else {
		// The issuer may be among the message's certificates even when the
		// chain stopped short.
		for _, c := range sd.Certificates {
			if bytes.Equal(c.RawSubject, r.Signer.RawIssuer) && r.Signer.CheckSignatureFrom(c) == nil {
				out.Issuer = c
				break
			}
		}
	}
	sm.Revocation = api.SMIMERevocation{State: api.RevocationUnchecked}
	sm.Revocation.Checkable = revocationURLs(r.Signer) && out.Issuer != nil
	if in.Revocation != nil {
		if rv, ok := in.Revocation(out.CertSHA256); ok {
			sm.Revocation.State = rv.State
			sm.Revocation.CheckedAt = rv.CheckedAt
			sm.Revocation.RevokedAt = rv.RevokedAt
			sm.Revocation.Source = rv.Source
		}
	}
	if r.Verified {
		switch sm.Revocation.State {
		case api.RevocationUnchecked:
			f.Notes = append(f.Notes, api.SignatureNoteRevocationUnchecked)
		case api.RevocationGood:
			f.Notes = append(f.Notes, api.SignatureNoteRevocationGood)
		case api.RevocationRevoked:
			t := sm.Revocation.RevokedAt
			d.RevokedAt = t
			if t == nil || !t.After(in.At) {
				f.Revoked = true
			} else {
				f.Notes = append(f.Notes, api.SignatureNoteRevokedAfter)
			}
		}
	}
	if r.ExpiredAt != nil {
		d.ExpiredAt = r.ExpiredAt
		if r.Untrusted != api.SignatureReasonExpired {
			f.Notes = append(f.Notes, api.SignatureNoteExpiredSince)
		}
	}
	d.SMIME = sm
	return nil
}

// revocationURLs: the certificate names an http or https OCSP responder or
// CRL distribution point.
func revocationURLs(c *x509.Certificate) bool {
	for _, list := range [][]string{c.OCSPServer, c.CRLDistributionPoints} {
		for _, u := range list {
			l := strings.ToLower(u)
			if strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") {
				return true
			}
		}
	}
	return false
}

func checkPGP(in Input, loc *Located, f *Findings, out *Outcome) {
	d := &out.Details
	ring := make(openpgp.EntityList, 0, len(in.Keys))
	byFpr := map[string]StoredKey{}
	for _, k := range in.Keys {
		ring = append(ring, k.Entity)
		byFpr[k.Fingerprint] = k
	}
	opts := pgpsig.Options{At: in.At, Now: in.Now}
	canon := Canonical(loc.Signed)
	r := pgpsig.Verify(canon, loc.Signature, ring, opts)
	if (r.Invalid == api.SignatureReasonDigestMismatch || r.Invalid == api.SignatureReasonBadSignature) && !bytes.Equal(canon, loc.Signed) {
		if again := pgpsig.Verify(loc.Signed, loc.Signature, ring, opts); again.Verified {
			r = again
		}
	}
	f.Verified = r.Verified
	f.Invalid, f.Untrusted, f.Weak, f.Unsupported, f.Error = r.Invalid, r.Untrusted, r.Weak, r.Unsupported, r.Error
	f.UnknownKey = r.UnknownKey
	f.Revoked = r.Revoked
	d.SignedAt = r.SigningTime
	d.Algorithm = Clean(r.Algorithm)
	out.KeyID = r.KeyID
	pd := &api.PGPDetails{KeyTrust: api.PGPKeyUnknown, KeyID: r.KeyID}
	d.PGP = pd
	var stored StoredKey
	known := false
	if r.Entity != nil {
		fpr := pgpsig.Fingerprint(r.Entity)
		stored, known = byFpr[fpr]
		names, emails := pgpsig.Identities(r.Entity, in.At)
		f.Identities = emails
		pd.UserIDs = CleanList(sortedIdentities(r.Entity), api.MaxSignatureUserIDs)
		signer := &api.SignatureSigner{Fingerprint: fpr}
		// The user ID that is the From address names the signer, else the
		// first one.
		pick := 0
		for i, e := range emails {
			if out.FromEmail != "" && NormalizeAddress(e) == out.FromEmail {
				pick = i
				break
			}
		}
		if len(names) > 0 {
			signer.Name = Clean(names[pick])
			signer.Email = Clean(emails[pick])
		}
		d.Signer = signer
		if known {
			pd.KeyTrust = stored.Trust
			pd.PrimaryFingerprint = fpr
			pd.KeySource = stored.Source
			fs := stored.FirstSeen.UTC()
			pd.FirstSeen = &fs
			f.Seen = stored.Trust != api.PGPKeyVerified
		}
	}
	if r.RevokedAfter && r.Verified {
		f.Notes = append(f.Notes, api.SignatureNoteRevokedAfter)
	}
	if r.RevokedAt != nil {
		d.RevokedAt = r.RevokedAt
	}
	if r.ExpiredAt != nil {
		d.ExpiredAt = r.ExpiredAt
		if r.Untrusted != api.SignatureReasonExpired && r.Verified {
			f.Notes = append(f.Notes, api.SignatureNoteExpiredSince)
		}
	}
	// keyChanged (TOFU): another stored key for the From address, seen
	// before this one (or this one unknown), and this one not confirmed.
	if out.FromEmail != "" && (!known || stored.Trust != api.PGPKeyVerified) && !r.Revoked {
		var prev *StoredKey
		for i := range in.Keys {
			k := in.Keys[i]
			if known && k.Fingerprint == stored.Fingerprint {
				continue
			}
			if r.Entity != nil && k.Fingerprint == pgpsig.Fingerprint(r.Entity) {
				continue
			}
			if !containsString(k.Emails, out.FromEmail) {
				continue
			}
			if known && k.FirstSeen.After(stored.FirstSeen) {
				continue
			}
			if prev == nil || k.FirstSeen.Before(prev.FirstSeen) {
				prev = &k
			}
		}
		if prev != nil && (r.Verified || r.UnknownKey) {
			f.Notes = append(f.Notes, api.SignatureNoteKeyChanged)
			pd.PreviousFingerprint = prev.Fingerprint
		}
	}
	// Offers: the message's Autocrypt key for From, or an attached key.
	if vals := HeaderValues(in.Raw, "Autocrypt"); len(vals) > 0 {
		if ac, ok := pgpsig.ParseAutocrypt(vals); ok && out.FromEmail != "" && ac.Addr == out.FromEmail {
			out.Autocrypt = &ac
			if keys, err := pgpsig.ReadKeys(ac.KeyData, in.Now); err == nil && len(keys) == 1 {
				if _, have := byFpr[keys[0].Fingerprint]; !have {
					pd.Importable = &api.PGPKeyOffer{Fingerprint: keys[0].Fingerprint, Autocrypt: true}
				}
			}
		}
	}
	if pd.Importable == nil {
		pd.Importable = attachedOffer(in, byFpr, r.KeyID)
	}
}

func sortedIdentities(e *openpgp.Entity) []string {
	out := make([]string, 0, len(e.Identities))
	for n := range e.Identities {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// attachedOffer is the first public key attached to the message that is
// not stored (one that holds the signing key first).
func attachedOffer(in Input, byFpr map[string]StoredKey, keyID string) *api.PGPKeyOffer {
	var first *api.PGPKeyOffer
	n := 0
	for _, a := range in.Attachments {
		if !KeyAttachment(a) {
			continue
		}
		n++
		if n > 4 {
			break
		}
		part, err := mime.ExtractPart(bytes.NewReader(in.Raw), a.PartID, mime.DefaultLimits(), api.MaxPGPKeyBytes)
		if err != nil {
			continue
		}
		keys, err := pgpsig.ReadKeys(part.Body, in.Now)
		if err != nil {
			continue
		}
		for _, k := range keys {
			if _, have := byFpr[k.Fingerprint]; have {
				continue
			}
			offer := &api.PGPKeyOffer{Fingerprint: k.Fingerprint, PartID: a.PartID}
			if containsString(k.KeyIDs, keyID) {
				return offer
			}
			if first == nil {
				first = offer
			}
		}
	}
	return first
}

// KeyAttachment: a part that may hold an OpenPGP public key
// (application/pgp-keys, or a *.asc, *.gpg, *.pgp, *.key file that is no
// signature).
func KeyAttachment(a api.Attachment) bool {
	if a.ContentType == "application/pgp-keys" {
		return true
	}
	if a.ContentType == "application/pgp-signature" {
		return false
	}
	name := strings.ToLower(a.Filename)
	for _, ext := range []string{".asc", ".gpg", ".pgp", ".key"} {
		if strings.HasSuffix(name, ext) && name != "signature.asc" {
			return true
		}
	}
	return false
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

var oidData = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}
