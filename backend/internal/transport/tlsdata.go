// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package transport

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Limits on the certificate details forwarded to clients: the certificate
// is the server's text, i.e. hostile input (docs/api.md §2).
const (
	MaxCertTextBytes = 128 // one subject, issuer or name
	MaxCertNames     = 8   // DNS names, and IP addresses, each
)

// NewTLSError is a tlsError whose Data carries a reason and no
// certificate: for what the protocol layers find out themselves (STARTTLS
// not offered or refused, TLS demanded before login).
func NewTLSError(reason api.TLSErrorReason, format string, args ...any) *api.Error {
	e := api.NewError(api.CodeTLSError, format, args...)
	e.Data = api.TLSErrorData{Reason: reason}
	return e
}

// tlsError is Classify's tlsError: msg plus the details of err. A
// verdict on a certificate gets a fixed message instead of the library's
// text, which quotes the certificate's names and issuer (the server's
// text, forwarded to logs and to agents through the MCP bridge); the
// details are in Data.
func tlsError(stage Stage, msg string, err error) *api.Error {
	data := tlsErrorData(err)
	if c := data.Certificate; c != nil {
		msg = fmt.Sprintf("%s: certificate refused (%s), sha256 %s", stage, data.Reason, c.SHA256)
		if data.ExpectedSHA256 != "" {
			msg += ", pinned " + data.ExpectedSHA256
		}
	}
	e := api.NewError(api.CodeTLSError, "%s", msg)
	e.Data = data
	return e
}

// tlsErrorData says why a TLS connection failed and describes the
// certificate the server presented, when err carries it: a pinned endpoint
// that got another certificate, or the verifier's verdict on the chain
// (the leaf is the first of the certificates it refused). Without either
// the handshake itself failed.
func tlsErrorData(err error) api.TLSErrorData {
	var pin *PinMismatchError
	if errors.As(err, &pin) {
		return api.TLSErrorData{Reason: api.TLSPinMismatch, Certificate: certificateInfo(pin.Cert), ExpectedSHA256: pin.Expected}
	}
	var leaf *x509.Certificate
	var cve *tls.CertificateVerificationError
	verdict := errors.As(err, &cve)
	if verdict && len(cve.UnverifiedCertificates) > 0 {
		leaf = cve.UnverifiedCertificates[0]
	}
	reason, named := verifyReason(err, leaf)
	if leaf == nil {
		leaf = named
	} else if named != nil && !bytes.Equal(named.Raw, leaf.Raw) && reason != api.TLSUntrusted {
		// The verdict is about a certificate further up the chain (an
		// expired intermediate, say) while the details describe the
		// server's own: say only that the chain is not valid.
		reason = api.TLSInvalid
	}
	switch {
	case reason != "":
	case verdict || leaf != nil:
		// The platform verifier refused for a reason it does not type
		// (macOS: "not standards compliant", a policy violation...).
		// A certificate outside its validity is at least that.
		reason = api.TLSOther
		if leaf != nil {
			if t := time.Now(); t.Before(leaf.NotBefore) {
				reason = api.TLSNotYetValid
			} else if t.After(leaf.NotAfter) {
				reason = api.TLSExpired
			}
		}
	default:
		return api.TLSErrorData{Reason: api.TLSHandshake}
	}
	return api.TLSErrorData{Reason: reason, Certificate: certificateInfo(leaf)}
}

// verifyReason maps a typed verdict of crypto/x509 in err to a reason,
// with the certificate the verdict names; "" when err holds none. Go's
// verifier types all of them; the macOS one only an untrusted issuer, an
// expired certificate and a name mismatch.
func verifyReason(err error, leaf *x509.Certificate) (api.TLSErrorReason, *x509.Certificate) {
	var (
		ua  x509.UnknownAuthorityError
		hn  x509.HostnameError
		ci  x509.CertificateInvalidError
		cv  x509.ConstraintViolationError
		uce x509.UnhandledCriticalExtension
		sr  x509.SystemRootsError
	)
	switch {
	case errors.As(err, &hn):
		return api.TLSHostnameMismatch, hn.Certificate
	case errors.As(err, &ci):
		if ci.Reason != x509.Expired {
			return api.TLSInvalid, ci.Cert
		}
		// Expired covers both ends of the validity period.
		c := ci.Cert
		if c == nil {
			c = leaf
		}
		if c != nil && time.Now().Before(c.NotBefore) {
			return api.TLSNotYetValid, ci.Cert
		}
		return api.TLSExpired, ci.Cert
	case errors.As(err, &ua):
		return api.TLSUntrusted, ua.Cert
	case errors.As(err, &cv), errors.As(err, &uce):
		return api.TLSInvalid, nil
	case errors.As(err, &sr):
		return api.TLSOther, nil
	}
	return "", nil
}

// certificateInfo describes c for a client; nil for no certificate. Every
// string is cleaned (cleanCertText), the name lists capped.
func certificateInfo(c *x509.Certificate) *api.CertificateInfo {
	if c == nil || len(c.Raw) == 0 {
		return nil
	}
	sum := sha256.Sum256(c.Raw)
	info := &api.CertificateInfo{
		SHA256:    hex.EncodeToString(sum[:]),
		Subject:   nameText(c.Subject),
		Issuer:    nameText(c.Issuer),
		NotBefore: c.NotBefore.UTC(),
		NotAfter:  c.NotAfter.UTC(),
		// Issued by itself: the same name, and its own key verifies the
		// signature (no CA constraints asked, unlike CheckSignatureFrom).
		SelfSigned: bytes.Equal(c.RawSubject, c.RawIssuer) &&
			c.CheckSignature(c.SignatureAlgorithm, c.RawTBSCertificate, c.Signature) == nil,
	}
	for _, n := range c.DNSNames {
		if len(info.DNSNames) == MaxCertNames {
			break
		}
		if n = cleanCertText(n); n != "" {
			info.DNSNames = append(info.DNSNames, n)
		}
	}
	for _, ip := range c.IPAddresses {
		if len(info.IPAddresses) == MaxCertNames {
			break
		}
		if len(ip) == 4 || len(ip) == 16 {
			info.IPAddresses = append(info.IPAddresses, ip.String())
		}
	}
	return info
}

// nameText is a distinguished name for display: its common name, or the
// whole name when it has none.
func nameText(n pkix.Name) string {
	if cn := cleanCertText(n.CommonName); cn != "" {
		return cn
	}
	return cleanCertText(n.String())
}

// cleanCertText makes certificate text safe to forward: valid UTF-8
// without control or format characters (bidi overrides, zero-width
// joiners...), line or paragraph separators and U+FFFD, trimmed, and at
// most MaxCertTextBytes, cut at a rune boundary.
func cleanCertText(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.Map(func(r rune) rune {
		if r == utf8.RuneError || unicode.In(r, unicode.Cc, unicode.Cf, unicode.Zl, unicode.Zp) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if len(s) > MaxCertTextBytes {
		s = s[:MaxCertTextBytes]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
		s = strings.TrimSpace(s)
	}
	return s
}

// SameTLSDetails reports whether a and b carry the same TLS details: the
// reason, the certificate's fingerprint and the pinned one (true when
// neither carries any). State comparisons use it so that another
// certificate is news even when the error code and text stay the same.
func SameTLSDetails(a, b *api.Error) bool {
	da, okA := api.TLSErrorDataOf(a)
	db, okB := api.TLSErrorDataOf(b)
	if okA != okB {
		return false
	}
	return !okA || da.Reason == db.Reason && da.ExpectedSHA256 == db.ExpectedSHA256 &&
		certSHA256(da.Certificate) == certSHA256(db.Certificate)
}

func certSHA256(c *api.CertificateInfo) string {
	if c == nil {
		return ""
	}
	return c.SHA256
}
