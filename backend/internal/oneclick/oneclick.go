// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package oneclick sends the RFC 8058 one-click unsubscribe request on
// the user's behalf. The URL comes from a message header and is hostile:
// the request is a single https POST with a fixed body and no identity
// beyond a fixed User-Agent (no cookies, no Referer), redirects are never
// followed, the answer is read only to a small cap and discarded, and a
// connection to a loopback, private, link-local, unspecified, multicast
// or carrier-grade-NAT address is refused at the moment of dialling, so
// neither a literal address nor a name that resolves (or is rebound) to
// one can aim the request at the user's own machine or network. Nothing
// here logs the URL or the address.
package oneclick

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

const (
	// Body is the request body RFC 8058 prescribes.
	Body = "List-Unsubscribe=One-Click"
	// Timeout bounds the whole request.
	Timeout = 15 * time.Second
	// MaxResponseBytes is how much of the answer is read (and discarded).
	MaxResponseBytes = 64 << 10

	userAgent = "Malachi Mail"
)

// ErrBlockedAddress is the dial error of an address the guard refuses.
var ErrBlockedAddress = errors.New("connection to a non-public address refused")

// StatusError is the answer of a server that did not accept the request:
// any status outside 200-299, redirects included.
type StatusError struct{ Code int }

func (e *StatusError) Error() string { return fmt.Sprintf("unsubscribe request answered %d", e.Code) }

// blocked are the ranges besides what netip classifies (loopback, private,
// link-local, unspecified, multicast) that never are a public mail
// sender: "this network", carrier-grade NAT (RFC 6598), benchmarking
// (RFC 2544), reserved and broadcast.
var blocked = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
}

// BlockedIP reports whether the guard refuses to connect to ip.
func BlockedIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	for _, p := range blocked {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// Guard is a net.Dialer.Control function: it refuses the connection when
// the resolved address is one BlockedIP names.
func Guard(network, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return ErrBlockedAddress
	}
	if BlockedIP(ap.Addr()) {
		return ErrBlockedAddress
	}
	return nil
}

// RefusedHost reports whether the URL host is one the request must never
// go to, whatever transport carries it (a configured proxy would resolve
// and connect for us, so the dial guard alone cannot protect these): a
// literal IP that BlockedIP rejects, a single-label name, and names under
// the local-network suffixes.
func RefusedHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return true
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return BlockedIP(ip)
	}
	if !strings.Contains(host, ".") {
		return true
	}
	for _, suffix := range []string{".local", ".localhost", ".internal", ".home.arpa"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

// Class names what went wrong with a request in a form that is safe to
// log: the raw error of a failed request names the host.
func Class(err error) string {
	var (
		se  *StatusError
		dns *net.DNSError
		ne  net.Error
	)
	switch {
	case err == nil:
		return ""
	case errors.As(err, &se):
		return "status"
	case errors.Is(err, ErrBlockedAddress):
		return "blocked"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.As(err, &dns):
		return "dns"
	case isTLS(err):
		return "tls"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "refused"
	case errors.As(err, &ne) && ne.Timeout():
		return "timeout"
	}
	return "other"
}

func isTLS(err error) bool {
	var (
		ci *tls.CertificateVerificationError
		ue x509.UnknownAuthorityError
		hn x509.HostnameError
		ce x509.CertificateInvalidError
		re tls.RecordHeaderError
	)
	return errors.As(err, &ci) || errors.As(err, &ue) || errors.As(err, &hn) || errors.As(err, &ce) || errors.As(err, &re)
}

// Poster sends the request through Client.
type Poster struct {
	// Client carries the request. Post works on a copy that never follows
	// a redirect and keeps no cookies, whatever Client says; a test
	// replaces the transport only.
	Client *http.Client
	// Refuse decides whether a URL host is refused before any transport
	// is chosen; nil = RefusedHost. Only a test replaces it, to reach a
	// server on loopback.
	Refuse func(host string) bool
}

// New returns a Poster whose client dials only public addresses. With an
// HTTP proxy configured in the environment the request goes to the proxy,
// which the user chose and which is not guarded; without one the guard
// applies to every dial.
func New() *Poster {
	dialer := &net.Dialer{Timeout: 10 * time.Second, Control: Guard}
	direct := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		DisableKeepAlives:     true,
	}
	viaProxy := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		DisableKeepAlives:     true,
	}
	return &Poster{Client: &http.Client{Timeout: Timeout, Transport: proxyChooser{direct: direct, proxy: viaProxy}}}
}

// proxyChooser sends a request through the unguarded transport only when
// the environment names a proxy for it.
type proxyChooser struct{ direct, proxy *http.Transport }

func (c proxyChooser) RoundTrip(req *http.Request) (*http.Response, error) {
	if p, err := http.ProxyFromEnvironment(req); err == nil && p != nil {
		return c.proxy.RoundTrip(req)
	}
	return c.direct.RoundTrip(req)
}

// Post sends the one-click request to rawURL. It returns nil for a 2xx
// answer, a *StatusError for any other status (a redirect is one), and
// otherwise the transport's error.
func (p *Poster) Post(ctx context.Context, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Hostname() == "" || u.User != nil {
		return errors.New("not an https URL")
	}
	refuse := p.Refuse
	if refuse == nil {
		refuse = RefusedHost
	}
	if refuse(u.Hostname()) {
		return ErrBlockedAddress
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), strings.NewReader(Body))
	if err != nil {
		return errors.New("bad request URL")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "*/*")
	client := *p.Client
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		// The error text of net/http names the URL: keep only its cause.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		var oe *net.OpError
		if errors.As(err, &oe) {
			// "dial tcp 203.0.113.9:443: …" names the address.
			err = fmt.Errorf("%s: %w", oe.Op, oe.Err)
		}
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, MaxResponseBytes))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &StatusError{Code: resp.StatusCode}
	}
	return nil
}
