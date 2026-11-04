// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package pgpsig verifies OpenPGP/MIME signatures (RFC 3156) with
// github.com/ProtonMail/go-crypto against a key ring the caller passes,
// judges the key at the message's arrival (revoked, expired), reads the
// keys of an import (public keys only: a secret key refuses the whole
// data), recognises an inline signature (never verified, plan S11) and
// reads the Autocrypt header. docs/plans/signatures.md S11–S13 and §5.
//
// Everything here is pure: no network, no files, no clock. The input is
// hostile; a panic inside go-crypto is recovered as an error result.
// Nothing is logged.
package pgpsig

import (
	"bytes"
	"crypto"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	pgperrors "github.com/ProtonMail/go-crypto/openpgp/errors"
	"github.com/ProtonMail/go-crypto/openpgp/packet"

	"github.com/schotek/malachi/backend/pkg/api"
)

// MaxSignatureBytes caps a signature part.
const MaxSignatureBytes = 64 << 10

// Options of Verify.
type Options struct {
	// At is when the message arrived (the key must be valid then); Now
	// is the present (expired or revoked since).
	At, Now time.Time
}

// Result is what Verify found; internal/signature orders it into the
// verdict.
type Result struct {
	Verified    bool
	Invalid     api.SignatureReason
	Untrusted   api.SignatureReason
	Weak        api.SignatureReason
	Unsupported api.SignatureReason
	Error       api.SignatureReason
	// UnknownKey: no key of the ring has the signature's key ID.
	UnknownKey bool
	// Revoked: the key (primary or the signing subkey) was revoked before
	// the arrival; RevokedAfter: since. RevokedAt is the revocation's time.
	Revoked      bool
	RevokedAfter bool
	RevokedAt    *time.Time
	// ExpiredAt is the key's expiry when it has passed (Untrusted expired
	// at the arrival, or expired since).
	ExpiredAt *time.Time

	// KeyID is the signing (sub)key's ID (16 lowercase hex), from the
	// signature; Entity the stored key it belongs to (nil when unknown).
	KeyID  string
	Entity *openpgp.Entity

	SigningTime *time.Time
	Algorithm   string
}

// Fingerprint renders a primary key's fingerprint as lowercase hex.
func Fingerprint(e *openpgp.Entity) string {
	return hex.EncodeToString(e.PrimaryKey.Fingerprint)
}

// Verify checks the detached signature sig (armoured or binary) over
// content (the signed part, CRLF-canonical) with the keys of ring.
func Verify(content, sig []byte, ring openpgp.EntityList, opts Options) (r Result) {
	defer func() {
		if p := recover(); p != nil {
			r = Result{Error: api.SignatureReasonInternal}
		}
	}()
	if len(sig) > MaxSignatureBytes {
		return Result{Error: api.SignatureReasonLimit}
	}
	data, err := dearmor(sig, openpgp.SignatureType)
	if err != nil {
		return Result{Invalid: api.SignatureReasonMalformed}
	}
	if md5Signature(data) {
		return Result{Invalid: api.SignatureReasonMD5}
	}
	packets := packet.NewReader(bytes.NewReader(data))
	var s *packet.Signature
	for {
		p, err := packets.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			var unsup pgperrors.UnsupportedError
			if errors.As(err, &unsup) {
				return Result{Unsupported: api.SignatureReasonAlgorithm}
			}
			return Result{Invalid: api.SignatureReasonMalformed}
		}
		one, ok := p.(*packet.Signature)
		if !ok {
			return Result{Invalid: api.SignatureReasonMalformed}
		}
		if s != nil {
			return Result{Unsupported: api.SignatureReasonMultipleSigners}
		}
		s = one
	}
	if s == nil {
		return Result{Invalid: api.SignatureReasonMalformed}
	}
	if s.SigType != packet.SigTypeBinary && s.SigType != packet.SigTypeText {
		return Result{Invalid: api.SignatureReasonMalformed}
	}
	t := s.CreationTime.UTC()
	r.SigningTime = &t
	var id uint64
	switch {
	case s.IssuerKeyId != nil:
		id = *s.IssuerKeyId
	case len(s.IssuerFingerprint) >= 8:
		fp := s.IssuerFingerprint
		if s.IssuerKeyVersion == 4 {
			fp = fp[len(fp)-8:]
		}
		for _, b := range fp[:8] {
			id = id<<8 | uint64(b)
		}
	default:
		return Result{Invalid: api.SignatureReasonMalformed}
	}
	r.KeyID = fmt.Sprintf("%016x", id)

	switch s.PubKeyAlgo {
	case packet.PubKeyAlgoDSA, packet.PubKeyAlgoElGamal:
		r.Unsupported = api.SignatureReasonAlgorithm
		return r
	}
	hashName, hashClass := hashOf(s.Hash)
	switch hashClass {
	case hashUnknown:
		r.Unsupported = api.SignatureReasonAlgorithm
		return r
	case hashSHA1:
		r.Weak = api.SignatureReasonSHA1
	}

	keys := ring.KeysById(id)
	if len(keys) == 0 {
		r.UnknownKey = true
		return r
	}
	// The key that verifies: a signing key of its entity.
	var key *openpgp.Key
	var lastErr error
	for i := range keys {
		k := keys[i]
		h, err := s.PrepareVerify()
		if err != nil {
			r.Unsupported = api.SignatureReasonAlgorithm
			return r
		}
		var w io.Writer = h
		if s.SigType == packet.SigTypeText {
			w = openpgp.NewCanonicalTextHash(h)
		}
		_, _ = w.Write(content)
		if err := k.PublicKey.VerifySignature(h, s); err != nil {
			lastErr = err
			continue
		}
		key = &k
		break
	}
	if key == nil {
		r.Entity = keys[0].Entity
		var unsup pgperrors.UnsupportedError
		var inv pgperrors.InvalidArgumentError
		switch {
		case errors.As(lastErr, &unsup), errors.As(lastErr, &inv):
			r.Unsupported = api.SignatureReasonAlgorithm
		case lastErr != nil && strings.Contains(lastErr.Error(), "hash tag"):
			r.Invalid = api.SignatureReasonDigestMismatch
		default:
			r.Invalid = api.SignatureReasonBadSignature
		}
		return r
	}
	r.Verified = true
	r.Entity = key.Entity
	r.Algorithm = KeyAlgorithm(key.PublicKey) + " / " + hashName
	if weakKey(key.PublicKey) && r.Weak == "" {
		r.Weak = api.SignatureReasonShortKey
	}
	// The key must be one that signs.
	if !signingKey(key) {
		r.Invalid = api.SignatureReasonKeyUsage
	}
	r.judgeKey(key, opts)
	return r
}

// judgeKey sets the revocation and expiry of the key at the arrival and
// now.
func (r *Result) judgeKey(key *openpgp.Key, opts Options) {
	e := key.Entity
	sub := key.PublicKey != e.PrimaryKey
	revs := append([]*packet.Signature(nil), e.Revocations...)
	if sub {
		revs = append(revs, key.Revocations...)
	}
	if t, ok := revokedAt(revs, opts.At); ok {
		r.Revoked = true
		r.RevokedAt = t
	} else if t, ok := revokedAt(revs, opts.Now); ok {
		r.RevokedAfter = true
		r.RevokedAt = t
	}
	// A key made after the message arrived cannot have signed it then.
	if !opts.At.IsZero() && (e.PrimaryKey.CreationTime.After(opts.At) || key.PublicKey.CreationTime.After(opts.At)) {
		r.Untrusted = api.SignatureReasonNotYetValid
	}
	exp := KeyExpiry(e)
	if sub && key.SelfSignature != nil {
		if se := expiry(key.PublicKey, key.SelfSignature); se != nil && (exp == nil || se.Before(*exp)) {
			exp = se
		}
	}
	if exp != nil {
		switch {
		case !opts.At.IsZero() && opts.At.After(*exp) && r.Untrusted == "":
			r.Untrusted = api.SignatureReasonExpired
			r.ExpiredAt = exp
		case !opts.Now.IsZero() && opts.Now.After(*exp):
			r.ExpiredAt = exp
		}
	}
}

// revokedAt reports the earliest revocation in effect at t (a compromised
// key counts whenever it was revoked; go-crypto's rule).
func revokedAt(revs []*packet.Signature, at time.Time) (*time.Time, bool) {
	if at.IsZero() {
		return nil, false
	}
	var best *time.Time
	for _, rv := range revs {
		compromised := rv.RevocationReason != nil && *rv.RevocationReason == packet.KeyCompromised
		if !compromised && rv.CreationTime.After(at) {
			continue
		}
		t := rv.CreationTime.UTC()
		if best == nil || t.Before(*best) {
			best = &t
		}
	}
	return best, best != nil
}

// KeyExpiry is the primary key's expiry by its primary self-signature,
// nil when it does not expire.
func KeyExpiry(e *openpgp.Entity) *time.Time {
	sig, _ := e.PrimarySelfSignature()
	if sig == nil {
		return nil
	}
	return expiry(e.PrimaryKey, sig)
}

func expiry(pk *packet.PublicKey, sig *packet.Signature) *time.Time {
	if sig.KeyLifetimeSecs == nil || *sig.KeyLifetimeSecs == 0 {
		return nil
	}
	t := pk.CreationTime.Add(time.Duration(*sig.KeyLifetimeSecs) * time.Second).UTC()
	return &t
}

// RevokedAt is when the primary key was revoked (nil: it is not).
func RevokedAt(e *openpgp.Entity, now time.Time) *time.Time {
	t, _ := revokedAt(e.Revocations, now)
	return t
}

// signingKey: the key's binding allows signing (a primary key without key
// flags may sign, as go-crypto takes it).
func signingKey(k *openpgp.Key) bool {
	if k.SelfSignature == nil {
		return false
	}
	if !k.SelfSignature.FlagsValid {
		return k.PublicKey == k.Entity.PrimaryKey
	}
	return k.SelfSignature.FlagSign
}

type hashClass int

const (
	hashOK hashClass = iota
	hashSHA1
	hashUnknown
)

func hashOf(h crypto.Hash) (string, hashClass) {
	switch h {
	case crypto.SHA256:
		return "SHA-256", hashOK
	case crypto.SHA384:
		return "SHA-384", hashOK
	case crypto.SHA512:
		return "SHA-512", hashOK
	case crypto.SHA224:
		return "SHA-224", hashOK
	case crypto.SHA3_256:
		return "SHA3-256", hashOK
	case crypto.SHA3_512:
		return "SHA3-512", hashOK
	case crypto.SHA1:
		return "SHA-1", hashSHA1
	}
	return "", hashUnknown
}

// KeyAlgorithm describes a public key ("RSA 4096", "Ed25519", "ECDSA
// P-256").
func KeyAlgorithm(pk *packet.PublicKey) string {
	switch pk.PubKeyAlgo {
	case packet.PubKeyAlgoRSA, packet.PubKeyAlgoRSASignOnly, packet.PubKeyAlgoRSAEncryptOnly:
		if n, err := pk.BitLength(); err == nil {
			return fmt.Sprintf("RSA %d", n)
		}
		return "RSA"
	case packet.PubKeyAlgoEdDSA, packet.PubKeyAlgoEd25519:
		return "Ed25519"
	case packet.PubKeyAlgoEd448:
		return "Ed448"
	case packet.PubKeyAlgoECDSA:
		if c, err := pk.Curve(); err == nil {
			return "ECDSA " + string(c)
		}
		return "ECDSA"
	case packet.PubKeyAlgoDSA:
		return "DSA"
	case packet.PubKeyAlgoECDH, packet.PubKeyAlgoX25519, packet.PubKeyAlgoX448:
		return "ECDH"
	}
	return fmt.Sprintf("algorithm %d", pk.PubKeyAlgo)
}

func weakKey(pk *packet.PublicKey) bool {
	switch pk.PubKeyAlgo {
	case packet.PubKeyAlgoRSA, packet.PubKeyAlgoRSASignOnly:
		n, err := pk.BitLength()
		return err == nil && n < 2048
	}
	return false
}

// md5Signature peeks at a binary signature packet: go-crypto does not read
// MD5 signatures at all, yet one is invalid, not unsupported (plan S10).
func md5Signature(data []byte) bool {
	if len(data) < 2 || data[0]&0x80 == 0 {
		return false
	}
	var tag byte
	var body int
	if data[0]&0x40 != 0 { // new format
		tag = data[0] & 0x3f
		l := data[1]
		switch {
		case l < 192:
			body = 2
		case l < 224:
			body = 3
		case l == 255:
			body = 6
		default:
			return false
		}
	} else {
		tag = (data[0] >> 2) & 0x0f
		switch data[0] & 3 {
		case 0:
			body = 2
		case 1:
			body = 3
		case 2:
			body = 5
		default:
			body = 1
		}
	}
	if tag != 2 || len(data) < body+4 {
		return false
	}
	b := data[body:]
	switch b[0] {
	case 3:
		return len(b) > 16 && b[16] == 1
	case 4, 5, 6:
		return b[3] == 1
	}
	return false
}

// dearmor returns the binary of armoured data of the given block type, or
// data itself when it is not armoured.
func dearmor(data []byte, blockType string) ([]byte, error) {
	trimmed := bytes.TrimLeft(data, " \t\r\n")
	if !bytes.HasPrefix(trimmed, []byte("-----BEGIN PGP ")) {
		return data, nil
	}
	block, err := armor.Decode(bytes.NewReader(trimmed))
	if err != nil {
		return nil, err
	}
	if blockType != "" && block.Type != blockType {
		return nil, errors.New("pgpsig: unexpected armour type")
	}
	return io.ReadAll(io.LimitReader(block.Body, MaxSignatureBytes*16+1))
}

// InlineMarker opens an inline (cleartext) signature.
const InlineMarker = "-----BEGIN PGP SIGNED MESSAGE-----"

// IsInlineSigned: the text's first non-blank line opens a cleartext
// signature (plan S11; never verified).
func IsInlineSigned(text string) bool {
	text = strings.TrimLeft(text, " \t\r\n\ufeff")
	line, _, _ := strings.Cut(text, "\n")
	return strings.TrimRight(line, " \t\r") == InlineMarker
}
