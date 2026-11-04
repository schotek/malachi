// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package smime

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	_ "crypto/sha1" // registers SHA-1 (weak, still verified)
	_ "crypto/sha256"
	_ "crypto/sha512"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
	"slices"
	"time"

	"golang.org/x/crypto/cryptobyte"
	cbasn1 "golang.org/x/crypto/cryptobyte/asn1"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Digest and signature algorithm identifiers.
var (
	oidMD5    = asn1.ObjectIdentifier{1, 2, 840, 113549, 2, 5}
	oidSHA1   = asn1.ObjectIdentifier{1, 3, 14, 3, 2, 26}
	oidSHA224 = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 4}
	oidSHA256 = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	oidSHA384 = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 2}
	oidSHA512 = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 3}

	oidRSA          = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}
	oidMD5WithRSA   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 4}
	oidSHA1WithRSA  = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 5}
	oidSHA256RSA    = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 11}
	oidSHA384RSA    = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 12}
	oidSHA512RSA    = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 13}
	oidSHA224RSA    = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 14}
	oidRSAPSS       = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 10}
	oidMGF1         = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 8}
	oidECPublicKey  = asn1.ObjectIdentifier{1, 2, 840, 10045, 2, 1}
	oidECDSASHA1    = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 1}
	oidECDSASHA224  = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 1}
	oidECDSASHA256  = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 2}
	oidECDSASHA384  = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 3}
	oidECDSASHA512  = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 4}
	oidEd25519      = asn1.ObjectIdentifier{1, 3, 101, 112}
	oidDSA          = asn1.ObjectIdentifier{1, 2, 840, 10040, 4, 1}
	oidDSASHA1      = asn1.ObjectIdentifier{1, 2, 840, 10040, 4, 3}
	oidDSASHA224    = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 3, 1}
	oidDSASHA256    = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 3, 2}
	oidEmailAddress = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 1}
)

// Options of Verify.
type Options struct {
	// Roots are the trusted roots (the system's, plan S7); nil trusts
	// nothing (every chain is untrusted).
	Roots *x509.CertPool
	// At is when the message arrived: the certificate must be valid then
	// (plan S8). Now is the present, for SignatureNoteExpiredSince.
	At, Now time.Time
}

// Result is what Verify found. The reasons are the findings of each class;
// internal/signature orders them into the verdict (plan §3).
type Result struct {
	// Verified: the signature matches the content and the signer's key.
	Verified bool
	// Invalid, Untrusted, Weak, Unsupported, Error are "" when the class
	// does not apply.
	Invalid     api.SignatureReason
	Untrusted   api.SignatureReason
	Weak        api.SignatureReason
	Unsupported api.SignatureReason
	Error       api.SignatureReason

	// Signer is the signer's certificate (nil when it is missing); Chain
	// the signer first, then its issuers as verified or, when the chain
	// did not verify, as found in the message; SystemRoot that the last
	// one of Chain is a root of Roots.
	Signer     *x509.Certificate
	Chain      []*x509.Certificate
	SystemRoot bool

	// Name and Emails are the signer's identity (plan S5): the subject's
	// common name, and the certificate's rfc822Name addresses, else the
	// DN's emailAddress. Raw, not cleaned.
	Name   string
	Emails []string

	SigningTime *time.Time
	Algorithm   string
	// ExpiredAt is the signer certificate's NotAfter when it has expired
	// (since the arrival, or before it with Untrusted expired).
	ExpiredAt *time.Time
}

// errVerify is a signature that does not verify with the key.
var errVerify = errors.New("smime: signature does not verify")

// Verify checks sd's only signer over content (the detached content of
// multipart/signed, or sd.EContent for opaque signed data when content is
// nil).
func Verify(sd *SignedData, content []byte, opts Options) Result {
	var r Result
	if len(sd.Signers) != 1 {
		r.Unsupported = api.SignatureReasonMultipleSigners
		return r
	}
	if content == nil {
		content = sd.EContent
	}
	if content == nil {
		r.Invalid = api.SignatureReasonMalformed
		return r
	}
	si := sd.Signers[0]
	r.SigningTime = si.SigningTime
	r.Signer = findSigner(sd.Certificates, si)
	if r.Signer != nil {
		r.Name = r.Signer.Subject.CommonName
		r.Emails = Emails(r.Signer)
	}

	digestHash, digestName, dClass := hashOf(si.DigestAlgorithm)
	sigKind, sigHash, sClass := signatureAlgorithm(si.SignatureAlgorithm, si.SignatureParams)
	switch {
	case dClass == classMD5 || sClass == classMD5:
		r.Invalid = api.SignatureReasonMD5
		return r
	case sClass == classDSA:
		r.Unsupported = api.SignatureReasonAlgorithm
		return r
	case dClass == classUnknown || sClass == classUnknown:
		r.Unsupported = api.SignatureReasonAlgorithm
		return r
	}
	if sigHash == 0 {
		sigHash = digestHash
	}
	if dClass == classSHA1 || sigHash == crypto.SHA1 {
		r.Weak = api.SignatureReasonSHA1
	}
	if r.Signer == nil {
		r.Unsupported = api.SignatureReasonFormat
		return r
	}
	r.Algorithm = keyName(r.Signer) + " / " + digestName
	if w := keyWeakness(r.Signer); w != "" && r.Weak == "" {
		r.Weak = w
	}

	// The content's digest, then the signature over the signed attributes
	// (or the content when there are none).
	signed := content
	if si.SignedAttrs != nil {
		if !si.ContentType.Equal(sd.EContentType) {
			r.Invalid = api.SignatureReasonMalformed
			return r
		}
		h := digestHash.New()
		h.Write(content)
		if !bytes.Equal(h.Sum(nil), si.MessageDigest) {
			r.Invalid = api.SignatureReasonDigestMismatch
			return r
		}
		signed = si.SignedAttrs
	}
	if err := checkSignature(r.Signer, sigKind, sigHash, si.SignatureParams, signed, si.Signature); err != nil {
		switch {
		case errors.Is(err, errUnsupportedKey):
			r.Unsupported = api.SignatureReasonAlgorithm
		case si.SignedAttrs == nil:
			// Without signed attributes the signature is over the content
			// itself: a change of the content shows here.
			r.Invalid = api.SignatureReasonDigestMismatch
		default:
			r.Invalid = api.SignatureReasonBadSignature
		}
		return r
	}
	r.Verified = true

	if !emailUsage(r.Signer) {
		r.Invalid = api.SignatureReasonKeyUsage
	}
	r.chain(sd.Certificates, opts)
	if na := r.Signer.NotAfter; !opts.Now.IsZero() && na.Before(opts.Now) {
		t := na.UTC()
		r.ExpiredAt = &t
	}
	return r
}

// VerifyChain re-judges only the chain of a verified result (another
// root pool, another time): what the backfill does after a change of
// trust without the message's bytes. Not used for the signature itself.
func (r *Result) chain(certs []*x509.Certificate, opts Options) {
	leaf := r.Signer
	inter := x509.NewCertPool()
	for _, c := range certs {
		if c != leaf {
			inter.AddCert(c)
		}
	}
	at := opts.At
	if at.IsZero() {
		at = opts.Now
	}
	vo := x509.VerifyOptions{
		Intermediates: inter,
		Roots:         opts.Roots,
		CurrentTime:   at,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection},
	}
	if opts.Roots == nil {
		vo.Roots = x509.NewCertPool()
	}
	chains, err := leaf.Verify(vo)
	if err == nil && len(chains) > 0 {
		best := chains[0]
		for _, c := range chains[1:] {
			if len(c) < len(best) {
				best = c
			}
		}
		if len(best) > MaxCertificates {
			best = best[:MaxCertificates]
		}
		r.Chain = best
		r.SystemRoot = true
		return
	}
	r.Chain = foundChain(leaf, certs)
	var inv x509.CertificateInvalidError
	switch {
	case errors.As(err, &inv) && inv.Reason == x509.Expired:
		bad := inv.Cert
		if bad == nil {
			bad = leaf
		}
		if at.Before(bad.NotBefore) {
			r.Untrusted = api.SignatureReasonNotYetValid
		} else {
			r.Untrusted = api.SignatureReasonExpired
			if bad == leaf {
				t := leaf.NotAfter.UTC()
				r.ExpiredAt = &t
			}
		}
	case selfSigned(leaf):
		r.Untrusted = api.SignatureReasonSelfSigned
	default:
		r.Untrusted = api.SignatureReasonUnknownIssuer
	}
}

// foundChain follows issuers by name among certs, from leaf, without
// verifying (for the details of an untrusted signature).
func foundChain(leaf *x509.Certificate, certs []*x509.Certificate) []*x509.Certificate {
	chain := []*x509.Certificate{leaf}
	cur := leaf
	for len(chain) < MaxCertificates && !selfSigned(cur) {
		var next *x509.Certificate
		for _, c := range certs {
			if bytes.Equal(c.RawSubject, cur.RawIssuer) && !slices.Contains(chain, c) {
				next = c
				break
			}
		}
		if next == nil {
			break
		}
		chain = append(chain, next)
		cur = next
	}
	return chain
}

// selfSigned: issuer and subject agree and the certificate's key signed
// it.
func selfSigned(c *x509.Certificate) bool {
	if !bytes.Equal(c.RawSubject, c.RawIssuer) {
		return false
	}
	return c.CheckSignature(c.SignatureAlgorithm, c.RawTBSCertificate, c.Signature) == nil
}

// findSigner picks the certificate the signer info names.
func findSigner(certs []*x509.Certificate, si SignerInfo) *x509.Certificate {
	for _, c := range certs {
		if si.SubjectKeyID != nil {
			if len(c.SubjectKeyId) > 0 && bytes.Equal(c.SubjectKeyId, si.SubjectKeyID) {
				return c
			}
			continue
		}
		if si.SerialNumber != nil && c.SerialNumber != nil &&
			bytes.Equal(c.RawIssuer, si.IssuerRaw) && c.SerialNumber.Cmp(si.SerialNumber) == 0 {
			return c
		}
	}
	return nil
}

// Emails returns a certificate's addresses: the rfc822Name entries of the
// subject alternative name, else the DN's emailAddress attributes.
func Emails(c *x509.Certificate) []string {
	if len(c.EmailAddresses) > 0 {
		return append([]string(nil), c.EmailAddresses...)
	}
	var out []string
	for _, n := range c.Subject.Names {
		if n.Type.Equal(oidEmailAddress) {
			if s, ok := n.Value.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// emailUsage: the certificate may sign e-mail. An extended key usage, when
// present, must allow email protection (or any); a key usage, when present,
// digital signature or non-repudiation.
func emailUsage(c *x509.Certificate) bool {
	if len(c.ExtKeyUsage) > 0 || len(c.UnknownExtKeyUsage) > 0 {
		ok := false
		for _, u := range c.ExtKeyUsage {
			if u == x509.ExtKeyUsageEmailProtection || u == x509.ExtKeyUsageAny {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	if c.KeyUsage != 0 && c.KeyUsage&(x509.KeyUsageDigitalSignature|x509.KeyUsageContentCommitment) == 0 {
		return false
	}
	return true
}

type algClass int

const (
	classOK algClass = iota
	classSHA1
	classMD5
	classDSA
	classUnknown
)

func hashOf(oid asn1.ObjectIdentifier) (crypto.Hash, string, algClass) {
	switch {
	case oid.Equal(oidSHA256):
		return crypto.SHA256, "SHA-256", classOK
	case oid.Equal(oidSHA384):
		return crypto.SHA384, "SHA-384", classOK
	case oid.Equal(oidSHA512):
		return crypto.SHA512, "SHA-512", classOK
	case oid.Equal(oidSHA224):
		return crypto.SHA224, "SHA-224", classOK
	case oid.Equal(oidSHA1):
		return crypto.SHA1, "SHA-1", classSHA1
	case oid.Equal(oidMD5):
		return 0, "MD5", classMD5
	}
	return 0, "", classUnknown
}

type sigKind int

const (
	sigRSA sigKind = iota + 1
	sigPSS
	sigECDSA
	sigEd25519
)

// signatureAlgorithm maps the signature algorithm to its kind and, when
// the identifier names one, its hash (0 = the digest algorithm's).
func signatureAlgorithm(oid asn1.ObjectIdentifier, params []byte) (sigKind, crypto.Hash, algClass) {
	switch {
	case oid.Equal(oidRSA):
		return sigRSA, 0, classOK
	case oid.Equal(oidSHA256RSA):
		return sigRSA, crypto.SHA256, classOK
	case oid.Equal(oidSHA384RSA):
		return sigRSA, crypto.SHA384, classOK
	case oid.Equal(oidSHA512RSA):
		return sigRSA, crypto.SHA512, classOK
	case oid.Equal(oidSHA224RSA):
		return sigRSA, crypto.SHA224, classOK
	case oid.Equal(oidSHA1WithRSA):
		return sigRSA, crypto.SHA1, classSHA1
	case oid.Equal(oidMD5WithRSA):
		return sigRSA, 0, classMD5
	case oid.Equal(oidRSAPSS):
		h, _, ok := pssParams(params)
		if !ok {
			return 0, 0, classUnknown
		}
		if h == crypto.SHA1 {
			return sigPSS, h, classSHA1
		}
		return sigPSS, h, classOK
	case oid.Equal(oidECPublicKey):
		return sigECDSA, 0, classOK
	case oid.Equal(oidECDSASHA256):
		return sigECDSA, crypto.SHA256, classOK
	case oid.Equal(oidECDSASHA384):
		return sigECDSA, crypto.SHA384, classOK
	case oid.Equal(oidECDSASHA512):
		return sigECDSA, crypto.SHA512, classOK
	case oid.Equal(oidECDSASHA224):
		return sigECDSA, crypto.SHA224, classOK
	case oid.Equal(oidECDSASHA1):
		return sigECDSA, crypto.SHA1, classSHA1
	case oid.Equal(oidEd25519):
		return sigEd25519, 0, classOK
	case oid.Equal(oidDSA), oid.Equal(oidDSASHA1), oid.Equal(oidDSASHA224), oid.Equal(oidDSASHA256):
		return 0, 0, classDSA
	}
	return 0, 0, classUnknown
}

// pssParams reads RSASSA-PSS-params: the hash (default SHA-1), MGF1 with
// the same hash (another is not supported), the salt length (default 20).
func pssParams(params []byte) (crypto.Hash, int, bool) {
	h, salt := crypto.SHA1, 20
	if params == nil {
		return h, salt, true
	}
	in := cryptobyte.String(params)
	var seq cryptobyte.String
	if !in.ReadASN1(&seq, cbasn1.SEQUENCE) || !in.Empty() {
		return 0, 0, false
	}
	var present bool
	var hashAlg cryptobyte.String
	if !seq.ReadOptionalASN1(&hashAlg, &present, cbasn1.Tag(0).ContextSpecific().Constructed()) {
		return 0, 0, false
	}
	if present {
		oid, _, err := readAlgorithm(&hashAlg)
		if err != nil {
			return 0, 0, false
		}
		var class algClass
		h, _, class = hashOf(oid)
		if class != classOK && class != classSHA1 {
			return 0, 0, false
		}
	}
	var mgf cryptobyte.String
	if !seq.ReadOptionalASN1(&mgf, &present, cbasn1.Tag(1).ContextSpecific().Constructed()) {
		return 0, 0, false
	}
	if present {
		oid, mp, err := readAlgorithm(&mgf)
		if err != nil || !oid.Equal(oidMGF1) || mp == nil {
			return 0, 0, false
		}
		inner := cryptobyte.String(mp)
		moid, _, err := readAlgorithm(&inner)
		if err != nil {
			return 0, 0, false
		}
		mh, _, _ := hashOf(moid)
		if mh != h {
			return 0, 0, false
		}
	}
	var saltField cryptobyte.String
	if !seq.ReadOptionalASN1(&saltField, &present, cbasn1.Tag(2).ContextSpecific().Constructed()) {
		return 0, 0, false
	}
	if present {
		var n int64
		if !saltField.ReadASN1Integer(&n) || n < 0 || n > 1024 {
			return 0, 0, false
		}
		salt = int(n)
	}
	var trailer cryptobyte.String
	if !seq.ReadOptionalASN1(&trailer, &present, cbasn1.Tag(3).ContextSpecific().Constructed()) {
		return 0, 0, false
	}
	if present {
		var n int64
		if !trailer.ReadASN1Integer(&n) || n != 1 {
			return 0, 0, false
		}
	}
	if !seq.Empty() {
		return 0, 0, false
	}
	return h, salt, true
}

var errUnsupportedKey = errors.New("smime: key and algorithm do not match")

func checkSignature(cert *x509.Certificate, kind sigKind, h crypto.Hash, params, signed, sig []byte) error {
	digest := func() []byte {
		d := h.New()
		d.Write(signed)
		return d.Sum(nil)
	}
	switch pub := cert.PublicKey.(type) {
	case *rsa.PublicKey:
		switch kind {
		case sigRSA:
			if err := rsa.VerifyPKCS1v15(pub, h, digest(), sig); err != nil {
				return errVerify
			}
			return nil
		case sigPSS:
			_, salt, ok := pssParams(params)
			if !ok {
				return errUnsupportedKey
			}
			if err := rsa.VerifyPSS(pub, h, digest(), sig, &rsa.PSSOptions{SaltLength: salt, Hash: h}); err != nil {
				return errVerify
			}
			return nil
		}
	case *ecdsa.PublicKey:
		if kind == sigECDSA {
			if !ecdsa.VerifyASN1(pub, digest(), sig) {
				return errVerify
			}
			return nil
		}
	case ed25519.PublicKey:
		if kind == sigEd25519 {
			if !ed25519.Verify(pub, signed, sig) {
				return errVerify
			}
			return nil
		}
	}
	return errUnsupportedKey
}

// keyName describes the certificate's public key ("RSA 2048", "ECDSA
// P-256", "Ed25519").
func keyName(c *x509.Certificate) string {
	switch pub := c.PublicKey.(type) {
	case *rsa.PublicKey:
		return fmt.Sprintf("RSA %d", pub.N.BitLen())
	case *ecdsa.PublicKey:
		return "ECDSA " + pub.Curve.Params().Name
	case ed25519.PublicKey:
		return "Ed25519"
	}
	return c.PublicKeyAlgorithm.String()
}

// keyWeakness: RSA shorter than 2048 bits, or P-224.
func keyWeakness(c *x509.Certificate) api.SignatureReason {
	switch pub := c.PublicKey.(type) {
	case *rsa.PublicKey:
		if pub.N.BitLen() < 2048 {
			return api.SignatureReasonShortKey
		}
	case *ecdsa.PublicKey:
		if pub.Curve == elliptic.P224() {
			return api.SignatureReasonShortKey
		}
	}
	return ""
}

// NameString renders a distinguished name for display (raw, not cleaned).
func NameString(n pkix.Name) string { return n.String() }
