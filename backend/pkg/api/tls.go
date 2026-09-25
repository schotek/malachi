// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"strings"
	"time"
)

// TLSErrorReason says why a TLS connection to an IMAP/SMTP endpoint
// failed (Error.Data of CodeTLSError). Clients translate it; unknown
// values are treated as "other".
type TLSErrorReason string

const (
	TLSUntrusted        TLSErrorReason = "untrusted"        // issuer not trusted (self-signed or private CA)
	TLSHostnameMismatch TLSErrorReason = "hostnameMismatch" // certificate is for another name
	TLSExpired          TLSErrorReason = "expired"          // past notAfter
	TLSNotYetValid      TLSErrorReason = "notYetValid"      // before notBefore
	TLSInvalid          TLSErrorReason = "invalid"          // otherwise malformed or not allowed for a server
	TLSOther            TLSErrorReason = "other"            // rejected by the system verifier for another reason (e.g. "not standards compliant" on macOS)
	TLSHandshake        TLSErrorReason = "handshake"        // protocol failure before a certificate was judged
	TLSStartTLSUnavail  TLSErrorReason = "starttlsUnavailable"
	TLSRequired         TLSErrorReason = "tlsRequired" // the server demands TLS before login
	TLSPinMismatch      TLSErrorReason = "pinMismatch" // not the certificate pinned in ServerConfig.CertificateSHA256
)

// CertificateInfo describes the server's leaf certificate. Every string is
// untrusted text from the server: control and format characters removed,
// at most 128 bytes; lists hold at most 8 entries.
type CertificateInfo struct {
	SHA256      string    `json:"sha256"` // 64 lowercase hex digits of the DER encoding
	Subject     string    `json:"subject,omitempty"`
	Issuer      string    `json:"issuer,omitempty"`
	DNSNames    []string  `json:"dnsNames,omitempty"`
	IPAddresses []string  `json:"ipAddresses,omitempty"`
	NotBefore   time.Time `json:"notBefore"`
	NotAfter    time.Time `json:"notAfter"`
	SelfSigned  bool      `json:"selfSigned"`
}

// TLSErrorData is Error.Data of CodeTLSError for IMAP/SMTP endpoints.
// Certificate is set when the server presented one; ExpectedSHA256 only
// for TLSPinMismatch.
type TLSErrorData struct {
	Reason         TLSErrorReason   `json:"reason"`
	Certificate    *CertificateInfo `json:"certificate,omitempty"`
	ExpectedSHA256 string           `json:"expectedSha256,omitempty"`
}

// TLSErrorDataOf returns the TLS details of e: the struct itself when the
// error was made in this process, or decoded from the JSON map a client
// receives. false when e is not a tlsError or carries no details.
func TLSErrorDataOf(e *Error) (TLSErrorData, bool) {
	if e == nil || e.Code != CodeTLSError || e.Data == nil {
		return TLSErrorData{}, false
	}
	switch d := e.Data.(type) {
	case TLSErrorData:
		return d, true
	case *TLSErrorData:
		if d == nil {
			return TLSErrorData{}, false
		}
		return *d, true
	}
	raw, err := json.Marshal(e.Data)
	if err != nil {
		return TLSErrorData{}, false
	}
	var d TLSErrorData
	if err := json.Unmarshal(raw, &d); err != nil || d.Reason == "" {
		return TLSErrorData{}, false
	}
	return d, true
}

// NormalizeCertificateSHA256 accepts a SHA-256 fingerprint as 64 hex
// digits, optionally separated by colons or spaces and in any case, and
// returns it as 64 lowercase hex digits. false for anything else.
func NormalizeCertificateSHA256(s string) (string, bool) {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == ':' || r == ' ':
			continue
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
			b.WriteRune(r)
		case r >= 'A' && r <= 'F':
			b.WriteRune(r + ('a' - 'A'))
		default:
			return "", false
		}
	}
	if b.Len() != 64 {
		return "", false
	}
	return b.String(), true
}
