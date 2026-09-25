// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package transport is the one place that opens outbound connections for
// IMAP and SMTP: the TLS policy (docs/security.md §7), context-aware
// dialling, timeouts, error classification into api error codes, and the
// scrubbing of server-supplied capability strings.
package transport

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Timeouts for one endpoint probe. The RPC layer has no per-request
// deadline, so every network operation carries its own.
const (
	DialTimeout     = 10 * time.Second // TCP connect (+ TLS handshake for implicit TLS)
	CommandTimeout  = 10 * time.Second // one protocol exchange
	EndpointTimeout = 20 * time.Second // whole probe of one endpoint
	QuitTimeout     = 2 * time.Second  // best-effort LOGOUT / QUIT
)

// Limits on server-supplied capability lists (hostile input).
const (
	MaxCapabilities  = 64
	MaxCapabilityLen = 64
	MaxMessageBytes  = 200 // error text forwarded to clients
	MaxHostBytes     = 253
)

// rootCAs overrides the system pool; only tests set it.
var rootCAs *x509.CertPool

// TLSConfig is the policy for every outbound TLS connection: TLS 1.2 or
// newer, the system trust store, the host name verified. There is no
// insecure option; the one exception, a certificate the user pinned for
// an IMAP/SMTP endpoint, is EndpointTLSConfig's.
func TLSConfig(host string) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: host,
		RootCAs:    rootCAs,
	}
}

// EndpointTLSConfig is the policy of an IMAP or SMTP endpoint: exactly
// TLSConfig(sc.Host) unless the endpoint pins a certificate
// (sc.CertificateSHA256, docs/security.md §7). A pinned endpoint accepts
// exactly the certificate whose DER encoding has that SHA-256, whatever
// its issuer, names or validity, and fails the handshake with a
// *PinMismatchError for any other; TLS 1.2 minimum and SNI stay. A pin
// that is not a fingerprint (validation normalises it, so only a bug gets
// here) matches no certificate. This is the only place that turns chain
// verification off.
func EndpointTLSConfig(sc api.ServerConfig) *tls.Config {
	cfg := TLSConfig(sc.Host)
	if sc.CertificateSHA256 == "" {
		return cfg
	}
	pin, _ := api.NormalizeCertificateSHA256(sc.CertificateSHA256)
	want, err := hex.DecodeString(pin)
	if err != nil || len(want) != sha256.Size {
		want = nil
	}
	// Replaced by VerifyConnection, which runs on every handshake
	// (resumed ones too) and whose error fails it.
	cfg.InsecureSkipVerify = true
	cfg.VerifyConnection = func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return &PinMismatchError{Expected: pin}
		}
		leaf := cs.PeerCertificates[0]
		got := sha256.Sum256(leaf.Raw)
		if want != nil && subtle.ConstantTimeCompare(got[:], want) == 1 {
			return nil
		}
		return &PinMismatchError{Expected: pin, Cert: leaf}
	}
	return cfg
}

// PinMismatchError fails the handshake of an endpoint that pins a
// certificate when the server presents another one (or none).
type PinMismatchError struct {
	Expected string            // the pinned fingerprint, 64 lowercase hex digits ("" if the pin was malformed)
	Cert     *x509.Certificate // the server's leaf certificate; nil when it sent none
}

func (e *PinMismatchError) Error() string {
	if e.Cert == nil {
		return "tls: server presented no certificate, a pinned one was expected"
	}
	sum := sha256.Sum256(e.Cert.Raw)
	return "tls: server certificate sha256 " + hex.EncodeToString(sum[:]) + " is not the pinned certificate"
}

// DialContext connects to the endpoint sc. For SecurityTLS the returned
// conn is a *tls.Conn verified by EndpointTLSConfig; for STARTTLS and None
// it is the raw TCP connection (the protocol layer upgrades it with
// EndpointTLSConfig). Errors are already classified.
func DialContext(ctx context.Context, sc api.ServerConfig) (net.Conn, error) {
	d := net.Dialer{Timeout: DialTimeout}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(sc.Host, strconv.Itoa(sc.Port)))
	if err != nil {
		return nil, Classify(ctx, StageDial, err)
	}
	if sc.Security != api.SecurityTLS {
		return conn, nil
	}
	tc := tls.Client(conn, EndpointTLSConfig(sc))
	hctx, cancel := context.WithTimeout(ctx, DialTimeout)
	defer cancel()
	if err := tc.HandshakeContext(hctx); err != nil {
		conn.Close()
		return nil, Classify(ctx, StageTLS, err)
	}
	return tc, nil
}

// hostLabel is one DNS label: letters, digits and inner hyphens.
var hostLabel = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?$`)

// ValidHost accepts an IP literal or a hostname made of DNS labels.
func ValidHost(h string) bool {
	if h == "" || len(h) > MaxHostBytes {
		return false
	}
	if net.ParseIP(h) != nil {
		return true
	}
	for _, label := range strings.Split(strings.TrimSuffix(h, "."), ".") {
		if len(label) > 63 || !hostLabel.MatchString(label) {
			return false
		}
	}
	return true
}

// IsLoopbackHost is where plaintext connections are tolerated
// (docs/security.md §7).
func IsLoopbackHost(h string) bool {
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}
