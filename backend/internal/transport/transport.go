// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package transport is the one place that opens outbound connections for
// IMAP and SMTP: the TLS policy (docs/security.md §7), context-aware
// dialling, timeouts, error classification into api error codes, and the
// scrubbing of server-supplied capability strings.
package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
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
// insecure option by design.
func TLSConfig(host string) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: host,
		RootCAs:    rootCAs,
	}
}

// DialContext connects to host:port. For SecurityTLS the returned conn is a
// verified *tls.Conn; for STARTTLS and None it is the raw TCP connection
// (the protocol layer upgrades it). Errors are already classified.
func DialContext(ctx context.Context, host string, port int, sec api.Security) (net.Conn, error) {
	d := net.Dialer{Timeout: DialTimeout}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, Classify(ctx, StageDial, err)
	}
	if sec != api.SecurityTLS {
		return conn, nil
	}
	tc := tls.Client(conn, TLSConfig(host))
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
