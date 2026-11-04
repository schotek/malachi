// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package smime reads and verifies S/MIME signatures: a minimal parser of
// CMS SignedData (RFC 5652) over golang.org/x/crypto/cryptobyte, the
// verification of one signer's signature (RSA PKCS #1 v1.5 and PSS, ECDSA,
// Ed25519; SHA-2), the signer's chain against a pool the caller passes, at
// a time the caller passes (the message's arrival, never the declared
// signing time), and the identity of the signer's certificate.
// docs/plans/signatures.md S7, S8, S10, S13.
//
// Everything here is pure: no network (no AIA fetching), no files, no
// clock. The input is hostile: ASN.1 nesting is capped at MaxDepth, an
// object at MaxObjectBytes, certificates at MaxCertificates; exceeding a
// cap is ErrLimit, broken structure ErrMalformed. Nothing is logged.
package smime

import (
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"math/big"
	"time"

	"golang.org/x/crypto/cryptobyte"
	cbasn1 "golang.org/x/crypto/cryptobyte/asn1"
)

// Limits of the parser (plan §4).
const (
	MaxDepth        = 32
	MaxObjectBytes  = 32 << 20
	MaxCertificates = 16
)

// Errors of Parse and ExtractContent; errors.Is tells the class.
var (
	ErrLimit     = errLimit
	ErrMalformed = errMalformed
	// ErrNotSignedData: a well-formed ContentInfo of another type
	// (enveloped data, say), or signed data whose content is not id-data.
	ErrNotSignedData = errors.New("smime: not signed data")
)

// Object identifiers.
var (
	oidData              = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}
	oidSignedData        = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}
	oidAttrContentType   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 3}
	oidAttrMessageDigest = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 4}
	oidAttrSigningTime   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 5}
)

// SignedData is the parsed CMS structure.
type SignedData struct {
	// EContentType is the encapsulated content's type; EContent its bytes
	// (nil when the signature is detached, as in multipart/signed).
	EContentType asn1.ObjectIdentifier
	EContent     []byte
	// Certificates are the certificates the message carries that Go could
	// parse (others are skipped), at most MaxCertificates.
	Certificates []*x509.Certificate
	// Signers holds every SignerInfo; more than one is unsupported.
	Signers []SignerInfo
}

// SignerInfo is one signer's signature.
type SignerInfo struct {
	// The signer's certificate is named by issuer and serial number, or by
	// subject key identifier.
	IssuerRaw    []byte
	SerialNumber *big.Int
	SubjectKeyID []byte

	DigestAlgorithm asn1.ObjectIdentifier
	// SignedAttrs is the DER of the signed attributes as a SET (the tag
	// rewritten from [0] IMPLICIT), what the signature covers; nil when
	// the signer signed the content itself.
	SignedAttrs []byte
	// From the signed attributes.
	ContentType   asn1.ObjectIdentifier
	MessageDigest []byte
	SigningTime   *time.Time

	SignatureAlgorithm asn1.ObjectIdentifier
	// SignatureParams are the algorithm's parameters (the RSASSA-PSS
	// parameters), nil when absent.
	SignatureParams []byte
	Signature       []byte
}

// Parse reads a DER or BER ContentInfo holding SignedData.
func Parse(data []byte) (*SignedData, error) {
	if len(data) > MaxObjectBytes {
		return nil, ErrLimit
	}
	der, err := toDER(data, MaxDepth, MaxObjectBytes)
	if err != nil {
		return nil, err
	}
	return parseDER(der)
}

// ExtractContent returns the encapsulated content of opaque signed data
// (application/pkcs7-mime; smime-type=signed-data) without verifying
// anything: what the MIME parser shows (plan S14). ErrNotSignedData when
// there is no id-data content.
func ExtractContent(data []byte) ([]byte, error) {
	sd, err := Parse(data)
	if err != nil {
		return nil, err
	}
	if !sd.EContentType.Equal(oidData) || sd.EContent == nil {
		return nil, ErrNotSignedData
	}
	return sd.EContent, nil
}

func parseDER(der []byte) (*SignedData, error) {
	in := cryptobyte.String(der)
	var ci, content cryptobyte.String
	var ctype asn1.ObjectIdentifier
	if !in.ReadASN1(&ci, cbasn1.SEQUENCE) || !in.Empty() {
		return nil, malformed("content info")
	}
	if !ci.ReadASN1ObjectIdentifier(&ctype) {
		return nil, malformed("content type")
	}
	if !ctype.Equal(oidSignedData) {
		return nil, ErrNotSignedData
	}
	if !ci.ReadASN1(&content, cbasn1.Tag(0).ContextSpecific().Constructed()) || !ci.Empty() {
		return nil, malformed("content")
	}
	var sd cryptobyte.String
	if !content.ReadASN1(&sd, cbasn1.SEQUENCE) || !content.Empty() {
		return nil, malformed("signed data")
	}
	var version int64
	if !sd.ReadASN1Integer(&version) {
		return nil, malformed("version")
	}
	var digestAlgs cryptobyte.String
	if !sd.ReadASN1(&digestAlgs, cbasn1.SET) {
		return nil, malformed("digest algorithms")
	}
	out := &SignedData{}
	var encap cryptobyte.String
	if !sd.ReadASN1(&encap, cbasn1.SEQUENCE) || !encap.ReadASN1ObjectIdentifier(&out.EContentType) {
		return nil, malformed("encapsulated content info")
	}
	if !encap.Empty() {
		var explicit cryptobyte.String
		if !encap.ReadASN1(&explicit, cbasn1.Tag(0).ContextSpecific().Constructed()) || !encap.Empty() {
			return nil, malformed("encapsulated content")
		}
		var ec []byte
		if !explicit.ReadASN1Bytes(&ec, cbasn1.OCTET_STRING) || !explicit.Empty() {
			return nil, malformed("encapsulated content octets")
		}
		if ec == nil {
			ec = []byte{}
		}
		out.EContent = ec
	}
	// certificates [0] IMPLICIT CertificateSet OPTIONAL
	var certs cryptobyte.String
	var present bool
	if !sd.ReadOptionalASN1(&certs, &present, cbasn1.Tag(0).ContextSpecific().Constructed()) {
		return nil, malformed("certificates")
	}
	if present {
		n := 0
		for !certs.Empty() {
			var el cryptobyte.String
			var tag cbasn1.Tag
			if !certs.ReadAnyASN1Element(&el, &tag) {
				return nil, malformed("certificate")
			}
			n++
			if n > MaxCertificates {
				return nil, ErrLimit
			}
			if tag != cbasn1.SEQUENCE {
				continue // an attribute or other certificate choice
			}
			c, err := x509.ParseCertificate(el)
			if err != nil {
				continue
			}
			out.Certificates = append(out.Certificates, c)
		}
	}
	// crls [1] IMPLICIT RevocationInfoChoices OPTIONAL: skipped.
	var crls cryptobyte.String
	if !sd.ReadOptionalASN1(&crls, &present, cbasn1.Tag(1).ContextSpecific().Constructed()) {
		return nil, malformed("crls")
	}
	var signers cryptobyte.String
	if !sd.ReadASN1(&signers, cbasn1.SET) || !sd.Empty() {
		return nil, malformed("signer infos")
	}
	for !signers.Empty() {
		var si cryptobyte.String
		if !signers.ReadASN1(&si, cbasn1.SEQUENCE) {
			return nil, malformed("signer info")
		}
		if len(out.Signers) >= MaxCertificates {
			return nil, ErrLimit
		}
		s, err := parseSignerInfo(si)
		if err != nil {
			return nil, err
		}
		out.Signers = append(out.Signers, s)
	}
	if len(out.Signers) == 0 {
		return nil, malformed("no signer")
	}
	return out, nil
}

func parseSignerInfo(si cryptobyte.String) (SignerInfo, error) {
	var s SignerInfo
	var version int64
	if !si.ReadASN1Integer(&version) {
		return s, malformed("signer version")
	}
	var tag cbasn1.Tag
	var sid cryptobyte.String
	if !si.ReadAnyASN1(&sid, &tag) {
		return s, malformed("signer identifier")
	}
	switch tag {
	case cbasn1.SEQUENCE:
		var issuer cryptobyte.String
		if !sid.ReadASN1Element(&issuer, cbasn1.SEQUENCE) {
			return s, malformed("issuer")
		}
		s.SerialNumber = new(big.Int)
		if !sid.ReadASN1Integer(s.SerialNumber) || !sid.Empty() {
			return s, malformed("serial number")
		}
		s.IssuerRaw = issuer
	case cbasn1.Tag(0).ContextSpecific():
		s.SubjectKeyID = append([]byte{}, sid...)
	default:
		return s, malformed("signer identifier choice")
	}
	var err error
	if s.DigestAlgorithm, _, err = readAlgorithm(&si); err != nil {
		return s, err
	}
	// signedAttrs [0] IMPLICIT SET OF Attribute OPTIONAL
	if si.PeekASN1Tag(cbasn1.Tag(0).ContextSpecific().Constructed()) {
		var el cryptobyte.String
		if !si.ReadASN1Element(&el, cbasn1.Tag(0).ContextSpecific().Constructed()) {
			return s, malformed("signed attributes")
		}
		attrs := append([]byte{}, el...)
		attrs[0] = 0x31 // the signature covers the attributes as a SET
		s.SignedAttrs = attrs
		if err := s.readAttributes(attrs); err != nil {
			return s, err
		}
	}
	if s.SignatureAlgorithm, s.SignatureParams, err = readAlgorithm(&si); err != nil {
		return s, err
	}
	if !si.ReadASN1Bytes(&s.Signature, cbasn1.OCTET_STRING) {
		return s, malformed("signature")
	}
	// unsignedAttrs [1] IMPLICIT OPTIONAL: ignored, never trusted.
	var un cryptobyte.String
	var present bool
	if !si.ReadOptionalASN1(&un, &present, cbasn1.Tag(1).ContextSpecific().Constructed()) || !si.Empty() {
		return s, malformed("signer info end")
	}
	return s, nil
}

// readAlgorithm reads an AlgorithmIdentifier.
func readAlgorithm(in *cryptobyte.String) (asn1.ObjectIdentifier, []byte, error) {
	var alg cryptobyte.String
	var oid asn1.ObjectIdentifier
	if !in.ReadASN1(&alg, cbasn1.SEQUENCE) || !alg.ReadASN1ObjectIdentifier(&oid) {
		return nil, nil, malformed("algorithm")
	}
	if alg.Empty() {
		return oid, nil, nil
	}
	var params cryptobyte.String
	var tag cbasn1.Tag
	if !alg.ReadAnyASN1Element(&params, &tag) || !alg.Empty() {
		return nil, nil, malformed("algorithm parameters")
	}
	if tag == cbasn1.NULL {
		return oid, nil, nil
	}
	return oid, append([]byte{}, params...), nil
}

// readAttributes reads the content type, message digest and signing time
// of the signed attributes (a SET as DER). Each may appear once with one
// value; a repeated one is malformed (an attacker's second digest must not
// be the one checked).
func (s *SignerInfo) readAttributes(set []byte) error {
	in := cryptobyte.String(set)
	var attrs cryptobyte.String
	if !in.ReadASN1(&attrs, cbasn1.SET) || !in.Empty() {
		return malformed("attributes")
	}
	seen := map[string]bool{}
	for !attrs.Empty() {
		var attr, values cryptobyte.String
		var oid asn1.ObjectIdentifier
		if !attrs.ReadASN1(&attr, cbasn1.SEQUENCE) || !attr.ReadASN1ObjectIdentifier(&oid) ||
			!attr.ReadASN1(&values, cbasn1.SET) || !attr.Empty() {
			return malformed("attribute")
		}
		key := oid.String()
		switch {
		case oid.Equal(oidAttrContentType), oid.Equal(oidAttrMessageDigest), oid.Equal(oidAttrSigningTime):
			if seen[key] {
				return malformed("repeated attribute")
			}
			seen[key] = true
		default:
			continue
		}
		var value cryptobyte.String
		var tag cbasn1.Tag
		if !values.ReadAnyASN1Element(&value, &tag) || !values.Empty() {
			return malformed("attribute value count")
		}
		switch {
		case oid.Equal(oidAttrContentType):
			var ct asn1.ObjectIdentifier
			if !value.ReadASN1ObjectIdentifier(&ct) || !value.Empty() {
				return malformed("content type attribute")
			}
			s.ContentType = ct
		case oid.Equal(oidAttrMessageDigest):
			var d []byte
			if !value.ReadASN1Bytes(&d, cbasn1.OCTET_STRING) || !value.Empty() {
				return malformed("message digest attribute")
			}
			s.MessageDigest = d
		case oid.Equal(oidAttrSigningTime):
			var t time.Time
			switch tag {
			case cbasn1.UTCTime:
				if !value.ReadASN1UTCTime(&t) {
					return malformed("signing time")
				}
			case cbasn1.GeneralizedTime:
				if !value.ReadASN1GeneralizedTime(&t) {
					return malformed("signing time")
				}
			default:
				return malformed("signing time type")
			}
			t = t.UTC()
			s.SigningTime = &t
		}
	}
	if s.SignedAttrs != nil && (s.ContentType == nil || s.MessageDigest == nil) {
		return malformed("signed attributes without content type or digest")
	}
	return nil
}
