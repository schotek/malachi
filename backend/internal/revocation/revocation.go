// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package revocation checks, on the user's request only, whether an S/MIME
// signer's certificate was revoked (docs/plans/signatures.md S9): OCSP at
// the responder the certificate names (AIA), then the CRL at its
// distribution point. The URLs come from a hostile certificate: only http
// and https, never a loopback, private or local name or address (the dial
// guard and host rules of internal/oneclick), no redirects, no cookies, at
// most api.MaxRevocationResponseBytes of an answer and api.RevocationTimeout
// for the whole check. An OCSP answer counts only when the issuer signed it
// or a responder certificate the issuer issued for OCSP signing; a CRL only
// when the issuer signed it. Nothing is logged here; Failure classes are
// what callers log.
package revocation

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/crypto/ocsp"

	"github.com/schotek/malachi/backend/internal/oneclick"
	"github.com/schotek/malachi/backend/pkg/api"
)

const userAgent = "Malachi Mail"

// staleGrace is how far past its next update an answer is still taken
// (responders refresh late; a revocation does not go away).
const staleGrace = 7 * 24 * time.Hour

// Result is what a check found.
type Result struct {
	State     api.RevocationState
	RevokedAt *time.Time
	Source    api.RevocationSource
}

// Error is a failed check, by class.
type Error struct{ Reason api.RevocationFailure }

func (e *Error) Error() string { return "revocation check failed: " + string(e.Reason) }

func fail(r api.RevocationFailure) error { return &Error{Reason: r} }

// Checker carries the checks.
type Checker struct {
	// Client carries the requests; Check works on a copy that never
	// follows a redirect and keeps no cookies. A test replaces it.
	Client *http.Client
	// Refuse decides whether a URL host is refused; nil =
	// oneclick.RefusedHost. Only a test replaces it.
	Refuse func(host string) bool
	// Now is the clock (nil = time.Now).
	Now func() time.Time
}

// New returns a Checker that dials only public addresses (a proxy from
// the environment is taken, as in internal/oneclick).
func New() *Checker {
	dialer := &net.Dialer{Timeout: 10 * time.Second, Control: oneclick.Guard}
	direct := &http.Transport{
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
	return &Checker{Client: &http.Client{Timeout: api.RevocationTimeout, Transport: chooser{direct, viaProxy}}}
}

type chooser struct{ direct, proxy *http.Transport }

func (c chooser) RoundTrip(req *http.Request) (*http.Response, error) {
	if p, err := http.ProxyFromEnvironment(req); err == nil && p != nil {
		return c.proxy.RoundTrip(req)
	}
	return c.direct.RoundTrip(req)
}

// URLs returns the certificate's http and https OCSP responders and CRL
// distribution points.
func URLs(cert *x509.Certificate) (ocspURLs, crlURLs []string) {
	for _, u := range cert.OCSPServer {
		if httpURL(u) {
			ocspURLs = append(ocspURLs, u)
		}
	}
	for _, u := range cert.CRLDistributionPoints {
		if httpURL(u) {
			crlURLs = append(crlURLs, u)
		}
	}
	return ocspURLs, crlURLs
}

// Checkable: the certificate names somewhere to ask and its issuer is
// known.
func Checkable(cert, issuer *x509.Certificate) bool {
	if cert == nil || issuer == nil {
		return false
	}
	o, c := URLs(cert)
	return len(o)+len(c) > 0
}

func httpURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil
}

// Check asks the OCSP responders, then the CRLs, at most
// api.RevocationTimeout in all. The first trusted answer wins; the error
// is the class of the last failure.
func (c *Checker) Check(ctx context.Context, cert, issuer *x509.Certificate) (Result, error) {
	if !Checkable(cert, issuer) {
		return Result{}, fail(api.RevocationFailAddress)
	}
	ctx, cancel := context.WithTimeout(ctx, api.RevocationTimeout)
	defer cancel()
	ocspURLs, crlURLs := URLs(cert)
	var last error = fail(api.RevocationFailAddress)
	for _, u := range ocspURLs {
		res, err := c.ocsp(ctx, u, cert, issuer)
		if err == nil {
			return res, nil
		}
		last = err
		if ctx.Err() != nil {
			return Result{}, fail(api.RevocationFailTimeout)
		}
	}
	for _, u := range crlURLs {
		res, err := c.crl(ctx, u, cert, issuer)
		if err == nil {
			return res, nil
		}
		last = err
		if ctx.Err() != nil {
			return Result{}, fail(api.RevocationFailTimeout)
		}
	}
	return Result{}, last
}

func (c *Checker) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Checker) ocsp(ctx context.Context, rawURL string, cert, issuer *x509.Certificate) (Result, error) {
	reqBody, err := ocsp.CreateRequest(cert, issuer, nil)
	if err != nil {
		return Result{}, fail(api.RevocationFailResponse)
	}
	body, err := c.fetch(ctx, http.MethodPost, rawURL, reqBody)
	if err != nil {
		return Result{}, err
	}
	resp, err := ocsp.ParseResponseForCert(body, cert, issuer)
	if err != nil {
		var re ocsp.ResponseError
		if errors.As(err, &re) {
			return Result{}, fail(api.RevocationFailServer)
		}
		return Result{}, fail(api.RevocationFailResponse)
	}
	// A delegated responder must be the issuer's and meant for OCSP
	// signing; ParseResponseForCert checked only that the issuer signed it.
	if rc := resp.Certificate; rc != nil && !bytes.Equal(rc.Raw, issuer.Raw) {
		ok := false
		for _, u := range rc.ExtKeyUsage {
			if u == x509.ExtKeyUsageOCSPSigning {
				ok = true
			}
		}
		if !ok || !bytes.Equal(rc.RawIssuer, issuer.RawSubject) {
			return Result{}, fail(api.RevocationFailResponse)
		}
		now := c.now()
		if now.Before(rc.NotBefore) || now.After(rc.NotAfter) {
			return Result{}, fail(api.RevocationFailResponse)
		}
	}
	if !c.fresh(resp.ThisUpdate, resp.NextUpdate) {
		return Result{}, fail(api.RevocationFailResponse)
	}
	switch resp.Status {
	case ocsp.Good:
		return Result{State: api.RevocationGood, Source: api.RevocationOCSP}, nil
	case ocsp.Revoked:
		t := resp.RevokedAt.UTC()
		return Result{State: api.RevocationRevoked, RevokedAt: &t, Source: api.RevocationOCSP}, nil
	default:
		return Result{State: api.RevocationUnknown, Source: api.RevocationOCSP}, nil
	}
}

func (c *Checker) fresh(this, next time.Time) bool {
	now := c.now()
	if this.After(now.Add(time.Hour)) {
		return false
	}
	return next.IsZero() || now.Before(next.Add(staleGrace))
}

func (c *Checker) crl(ctx context.Context, rawURL string, cert, issuer *x509.Certificate) (Result, error) {
	body, err := c.fetch(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Result{}, err
	}
	rl, err := x509.ParseRevocationList(body)
	if err != nil {
		return Result{}, fail(api.RevocationFailResponse)
	}
	if !bytes.Equal(rl.RawIssuer, issuer.RawSubject) || rl.CheckSignatureFrom(issuer) != nil {
		return Result{}, fail(api.RevocationFailResponse)
	}
	if !c.fresh(rl.ThisUpdate, rl.NextUpdate) {
		return Result{}, fail(api.RevocationFailResponse)
	}
	for _, e := range rl.RevokedCertificateEntries {
		if e.SerialNumber != nil && e.SerialNumber.Cmp(cert.SerialNumber) == 0 {
			t := e.RevocationTime.UTC()
			return Result{State: api.RevocationRevoked, RevokedAt: &t, Source: api.RevocationCRL}, nil
		}
	}
	return Result{State: api.RevocationGood, Source: api.RevocationCRL}, nil
}

// fetch sends one request and reads the answer within the cap.
func (c *Checker) fetch(ctx context.Context, method, rawURL string, body []byte) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return nil, fail(api.RevocationFailAddress)
	}
	refuse := c.Refuse
	if refuse == nil {
		refuse = oneclick.RefusedHost
	}
	if refuse(u.Hostname()) {
		return nil, fail(api.RevocationFailAddress)
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return nil, fail(api.RevocationFailAddress)
	}
	req.Header.Set("User-Agent", userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/ocsp-request")
		req.Header.Set("Accept", "application/ocsp-response")
	}
	client := *c.Client
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return nil, classify(ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, fail(api.RevocationFailAddress)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fail(api.RevocationFailServer)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, api.MaxRevocationResponseBytes+1))
	if err != nil {
		return nil, classify(ctx, err)
	}
	if len(data) > api.MaxRevocationResponseBytes {
		return nil, fail(api.RevocationFailResponse)
	}
	return data, nil
}

func classify(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, oneclick.ErrBlockedAddress):
		return fail(api.RevocationFailAddress)
	case ctx.Err() != nil, errors.Is(err, context.DeadlineExceeded):
		return fail(api.RevocationFailTimeout)
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return fail(api.RevocationFailTimeout)
	}
	return fail(api.RevocationFailNetwork)
}

// FailureOf returns the class of a Check error ("" for another error).
func FailureOf(err error) api.RevocationFailure {
	var e *Error
	if errors.As(err, &e) {
		return e.Reason
	}
	return ""
}
