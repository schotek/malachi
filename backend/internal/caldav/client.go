// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package caldav is the CalDAV client of the calendar (docs/calendar.md
// §3.5, decision D3): discovery of a server, the principal, its calendar
// home and calendars, and the requests a sync and a write need —
// PROPFIND, REPORT calendar-query / calendar-multiget / sync-collection,
// GET, PUT and DELETE with ETags — over net/http and encoding/xml. Every
// answer is hostile input: bodies are capped, XML is read strictly with
// depth and element caps, hrefs on another origin are dropped, and the
// password goes only to the configured server's origin (redirects are
// followed only within it). The Provider of calsync is provider.go.
package caldav

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Limits and timeouts of the client.
const (
	// MaxMultistatusBytes caps a PROPFIND or REPORT answer;
	// MaxObjectBytes a GET of one object.
	MaxMultistatusBytes = 32 << 20
	MaxObjectBytes      = api.MaxICSBytes + 64<<10
	// MaxConcurrent is how many requests one client (one account) has in
	// flight.
	MaxConcurrent = 2
	// MaxRedirects bounds a redirect chain (same origin only).
	MaxRedirects = 5
	// RetryAfterMax caps what a server may ask a caller to wait.
	RetryAfterMax = 10 * time.Minute
	// MaxServerURLBytes bounds a server URL.
	MaxServerURLBytes = 2048
	// RequestTimeout bounds one request with its body.
	RequestTimeout = 60 * time.Second

	maxErrorBody        = 64 << 10
	retryWaitMax        = 30 * time.Second // longer waits go back to the caller
	throttleRetries     = 2
	defaultThrottleWait = 5 * time.Second
	defaultUserAgent    = "Malachi-Mail"
	dialTimeout         = 10 * time.Second
)

// Options configures a Client.
type Options struct {
	// ServerURL is the account's server (CalDAVConfig.ServerURL); it is
	// normalised with NormaliseServerURL.
	ServerURL string
	// Username and Password authenticate with Basic. Password is called
	// for each request (the caller caches and decides when the keyring
	// is read); nil makes an anonymous client (discovery).
	Username string
	Password func(ctx context.Context) (string, error)
	// CertificateSHA256 pins the server's leaf certificate
	// (transport.EndpointTLSConfig); not allowed with an http URL. Only
	// used for the client's own transport (HTTP nil).
	CertificateSHA256 string
	// Origins are further origins ("https://host[:port]") the caller
	// trusts with the password: a home set on another host the user
	// confirmed. The server's own origin is always trusted.
	Origins []string
	// HTTP carries the requests (tests: caldavtest's client). Nil = a
	// transport of the client's own with the TLS policy of the server
	// (and the pin). Redirects are followed by the client itself and
	// cookies are never kept, whatever HTTP says.
	HTTP *http.Client
	// Timeout bounds one request (0 = RequestTimeout).
	Timeout time.Duration
	Log     *slog.Logger
	// Now is the clock for Retry-After dates; Sleep the wait of a
	// throttled request (nil = a timer honouring the context).
	Now       func() time.Time
	Sleep     func(ctx context.Context, d time.Duration) error
	UserAgent string
}

// Client talks to one CalDAV server with one set of credentials. It is
// safe for concurrent use; at most MaxConcurrent requests run at once.
type Client struct {
	base      *url.URL // normalised server URL, no trailing slash
	origins   []*url.URL
	username  string
	password  func(ctx context.Context) (string, error)
	http      *http.Client
	timeout   time.Duration
	log       *slog.Logger
	now       func() time.Time
	sleep     func(ctx context.Context, d time.Duration) error
	userAgent string
	sem       chan struct{}

	mu       sync.Mutex
	peerCert *x509.Certificate // of the last TLS answer
}

// NewClient builds a client. Errors are invalidArgument.
func NewClient(opts Options) (*Client, error) {
	s, err := NormaliseServerURL(opts.ServerURL)
	if err != nil {
		return nil, err
	}
	base, _ := url.Parse(s)
	if opts.CertificateSHA256 != "" {
		if base.Scheme != "https" {
			return nil, api.NewError(api.CodeInvalidArgument, "caldav: a certificate pin needs an https URL")
		}
		if _, ok := api.NormalizeCertificateSHA256(opts.CertificateSHA256); !ok {
			return nil, api.NewError(api.CodeInvalidArgument, "caldav: certificate pin is not a SHA-256 fingerprint")
		}
	}
	c := &Client{
		base:      base,
		username:  opts.Username,
		password:  opts.Password,
		timeout:   opts.Timeout,
		log:       opts.Log,
		now:       opts.Now,
		sleep:     opts.Sleep,
		userAgent: opts.UserAgent,
		sem:       make(chan struct{}, MaxConcurrent),
	}
	c.origins = append(c.origins, originOf(base))
	for _, o := range opts.Origins {
		u, err := url.Parse(o)
		if err != nil || (u.Scheme != "https" && !(u.Scheme == "http" && transport.IsLoopbackHost(u.Hostname()))) || u.Host == "" || u.User != nil {
			return nil, api.NewError(api.CodeInvalidArgument, "caldav: trusted origin is not an https origin")
		}
		c.origins = append(c.origins, originOf(u))
	}
	var hc http.Client
	if opts.HTTP != nil {
		hc = *opts.HTTP
	} else {
		hc.Transport = newTransport(base.Hostname(), opts.CertificateSHA256)
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	hc.Jar = nil
	hc.Timeout = 0
	c.http = &hc
	if c.timeout <= 0 {
		c.timeout = RequestTimeout
	}
	if c.log == nil {
		c.log = slog.New(slog.DiscardHandler)
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.sleep == nil {
		c.sleep = sleepCtx
	}
	if c.userAgent == "" {
		c.userAgent = defaultUserAgent
	}
	return c, nil
}

// newTransport is the client's own transport: the TLS policy of an
// endpoint (with the pin), the environment's proxy, bounded dialling.
func newTransport(host, pin string) *http.Transport {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: dialTimeout}).DialContext,
		TLSClientConfig:       transport.EndpointTLSConfig(api.ServerConfig{Host: host, CertificateSHA256: pin}),
		TLSHandshakeTimeout:   dialTimeout,
		ResponseHeaderTimeout: RequestTimeout,
		ForceAttemptHTTP2:     true,
		MaxIdleConnsPerHost:   MaxConcurrent,
		IdleConnTimeout:       90 * time.Second,
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// NormaliseServerURL is the rule of CalDAVConfig.ServerURL: a host or a
// URL, https assumed; https, or http for a loopback host; a valid host;
// no user info, query or fragment; trailing slashes removed; ≤
// MaxServerURLBytes. Errors are invalidArgument.
func NormaliseServerURL(raw string) (string, error) {
	u, err := parseServerURL(raw)
	if err != nil {
		return "", err
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	s := u.String()
	if len(s) > MaxServerURLBytes {
		return "", api.NewError(api.CodeInvalidArgument, "caldav: server URL longer than %d bytes", MaxServerURLBytes)
	}
	return s, nil
}

func parseServerURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > MaxServerURLBytes {
		return nil, api.NewError(api.CodeInvalidArgument, "caldav: no usable server URL")
	}
	for _, r := range raw {
		if r < 0x21 || r == 0x7f {
			return nil, api.NewError(api.CodeInvalidArgument, "caldav: server URL has spaces or control characters")
		}
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, api.NewError(api.CodeInvalidArgument, "caldav: server URL does not parse")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	switch {
	case u.User != nil:
		return nil, api.NewError(api.CodeInvalidArgument, "caldav: server URL must not carry a user name or password")
	case u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "":
		return nil, api.NewError(api.CodeInvalidArgument, "caldav: server URL must not have a query or fragment")
	case !transport.ValidHost(u.Hostname()):
		return nil, api.NewError(api.CodeInvalidArgument, "caldav: server URL has no valid host")
	case u.Scheme == "http" && !transport.IsLoopbackHost(u.Hostname()):
		return nil, api.NewError(api.CodeInvalidArgument, "caldav: http is allowed only for a loopback host")
	case u.Scheme != "https" && u.Scheme != "http":
		return nil, api.NewError(api.CodeInvalidArgument, "caldav: server URL must be https")
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return nil, api.NewError(api.CodeInvalidArgument, "caldav: server URL has a bad port")
		}
	}
	for _, seg := range strings.Split(u.Path, "/") {
		if seg == "." || seg == ".." {
			return nil, api.NewError(api.CodeInvalidArgument, "caldav: server URL has dot segments")
		}
	}
	u.Host = strings.ToLower(u.Host)
	return u, nil
}

// ServerURL is the normalised server URL.
func (c *Client) ServerURL() string { return c.base.String() }

// URL resolves href (a path, or an absolute URL) against the server.
func (c *Client) URL(href string) (*url.URL, error) {
	u, ok := resolveURL(c.base, href)
	if !ok {
		return nil, api.NewError(api.CodeInvalidArgument, "caldav: unusable href")
	}
	return u, nil
}

// rootURL is the server URL as a collection (with a trailing slash).
func (c *Client) rootURL() *url.URL {
	u := *c.base
	u.Path += "/"
	return &u
}

// PeerCertificate is the server's leaf certificate of the last TLS
// answer, nil before one (or over plain http, or with a fake transport).
func (c *Client) PeerCertificate() *x509.Certificate {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.peerCert
}

// trusted reports whether credentials may go to u.
func (c *Client) trusted(u *url.URL) bool {
	for _, o := range c.origins {
		if sameOrigin(u, o) {
			return true
		}
	}
	return false
}

// originOf is u's scheme and host.
func originOf(u *url.URL) *url.URL {
	return &url.URL{Scheme: strings.ToLower(u.Scheme), Host: strings.ToLower(u.Host)}
}

// sameOrigin compares scheme, host (case-insensitively) and port, a port
// left out counting as the scheme's default.
func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Hostname(), b.Hostname()) &&
		effectivePort(a) == effectivePort(b)
}

func effectivePort(u *url.URL) string {
	p := u.Port()
	switch {
	case p == "" && strings.EqualFold(u.Scheme, "https"):
		return "443"
	case p == "" && strings.EqualFold(u.Scheme, "http"):
		return "80"
	}
	return p
}

// request is one exchange.
type request struct {
	method      string
	url         *url.URL
	body        []byte
	contentType string
	depth       string // "" = no Depth header
	accept      string
	headers     map[string]string
	limit       int64 // body cap of a 2xx answer
	anonymous   bool
	// noRedirect returns a 3xx answer as it is (discovery reads its
	// Location); otherwise redirects are followed within the origin.
	noRedirect bool
}

// answer is a response read in full.
type answer struct {
	status int
	header http.Header
	body   []byte
	url    *url.URL // after redirects
}

// do runs rq with the concurrency, credential, redirect and throttle
// policy. A non-2xx answer (and a 3xx without noRedirect) is a
// *StatusError; transport failures are *api.Error.
func (c *Client) do(ctx context.Context, rq request) (*answer, error) {
	if !rq.anonymous && !c.trusted(rq.url) {
		return nil, ErrOtherOrigin
	}
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, classifyTransport(ctx, ctx.Err())
	}
	defer func() { <-c.sem }()

	var auth string
	if !rq.anonymous {
		if c.password == nil {
			return nil, api.NewError(api.CodeAuthRequired, "caldav: no password")
		}
		pw, err := c.password(ctx)
		if err != nil {
			return nil, err
		}
		if pw == "" {
			return nil, api.NewError(api.CodeAuthRequired, "caldav: no password")
		}
		auth = "Basic " + base64.StdEncoding.EncodeToString([]byte(c.username+":"+pw))
	}
	for throttled := 0; ; throttled++ {
		a, err := c.send(ctx, rq, auth)
		if err != nil {
			return nil, err
		}
		if a.status >= 200 && a.status <= 299 || (rq.noRedirect && isRedirect(a.status)) {
			return a, nil
		}
		se := c.statusError(rq.method, a)
		if (a.status == http.StatusTooManyRequests || a.status == http.StatusServiceUnavailable) && throttled < throttleRetries {
			wait := se.RetryAfter
			if wait <= 0 {
				wait = defaultThrottleWait
			}
			if wait <= retryWaitMax {
				c.log.Debug("caldav throttled", "status", a.status, "wait", wait)
				if err := c.sleep(ctx, wait); err != nil {
					return nil, classifyTransport(ctx, err)
				}
				continue
			}
		}
		return nil, se
	}
}

// send runs one request and follows redirects within the request's
// origin, at most MaxRedirects, for every method but PUT and DELETE
// (keeping method and body, as WebDAV servers expect). Authorization is
// set only on URLs of a trusted origin.
func (c *Client) send(ctx context.Context, rq request, auth string) (*answer, error) {
	rctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	u := rq.url
	first := u
	for hop := 0; ; hop++ {
		var body io.Reader
		if rq.body != nil {
			body = bytes.NewReader(rq.body)
		}
		req, err := http.NewRequestWithContext(rctx, rq.method, u.String(), body)
		if err != nil {
			return nil, api.NewError(api.CodeInvalidArgument, "caldav: bad request")
		}
		req.Header.Set("User-Agent", c.userAgent)
		if rq.accept != "" {
			req.Header.Set("Accept", rq.accept)
		}
		if rq.contentType != "" {
			req.Header.Set("Content-Type", rq.contentType)
		}
		if rq.depth != "" {
			req.Header.Set("Depth", rq.depth)
		}
		for k, v := range rq.headers {
			req.Header.Set(k, v)
		}
		if auth != "" && c.trusted(u) {
			req.Header.Set("Authorization", auth)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, classifyTransport(ctx, err)
		}
		if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
			c.mu.Lock()
			c.peerCert = resp.TLS.PeerCertificates[0]
			c.mu.Unlock()
		}
		if isRedirect(resp.StatusCode) && !rq.noRedirect {
			loc := resp.Header.Get("Location")
			drain(resp)
			if rq.method == http.MethodPut || rq.method == http.MethodDelete {
				return &answer{status: resp.StatusCode, header: resp.Header, url: u}, nil
			}
			if hop >= MaxRedirects {
				return nil, api.NewError(api.CodeServerError, "caldav: too many redirects")
			}
			next, err := u.Parse(loc)
			if loc == "" || err != nil || next.Host == "" || next.User != nil {
				return nil, api.NewError(api.CodeServerError, "caldav: redirect without a usable location")
			}
			if !sameOrigin(next, first) {
				return nil, api.NewError(api.CodeServerError, "caldav: redirect to another server refused")
			}
			next.Fragment, next.RawFragment = "", ""
			u = next
			continue
		}
		a := &answer{status: resp.StatusCode, header: resp.Header, url: u}
		limit := int64(maxErrorBody)
		if resp.StatusCode >= 200 && resp.StatusCode <= 299 && rq.limit > 0 {
			limit = rq.limit
		}
		a.body, err = readBody(ctx, resp, limit, resp.StatusCode >= 200 && resp.StatusCode <= 299)
		if err != nil {
			return nil, err
		}
		return a, nil
	}
}

// readBody reads a body within limit. Over the limit a 2xx answer is
// ErrTooLarge; an error answer is cut.
func readBody(ctx context.Context, resp *http.Response, limit int64, strict bool) ([]byte, error) {
	defer resp.Body.Close()
	if strict && resp.ContentLength > limit {
		return nil, fmt.Errorf("%w: %d bytes announced", ErrTooLarge, resp.ContentLength)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, classifyTransport(ctx, err)
	}
	if int64(len(data)) > limit {
		if strict {
			return nil, fmt.Errorf("%w: over %d bytes", ErrTooLarge, limit)
		}
		data = data[:limit]
	}
	return data, nil
}

func isRedirect(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}

func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBody))
	resp.Body.Close()
}

// statusError describes a non-2xx answer: the precondition of a DAV:error
// body, the status text otherwise (never the server's free text, which
// may be a page quoting anything).
func (c *Client) statusError(method string, a *answer) *StatusError {
	se := &StatusError{Method: method, Status: a.status, Message: http.StatusText(a.status)}
	if ct := strings.ToLower(a.header.Get("Content-Type")); strings.Contains(ct, "xml") || ct == "" {
		se.Precondition = parsePrecondition(a.body)
	}
	if a.status == http.StatusTooManyRequests || a.status == http.StatusServiceUnavailable {
		se.RetryAfter = retryAfter(a.header, c.now())
	}
	return se
}

// retryAfter reads Retry-After (seconds or an HTTP date), capped at
// RetryAfterMax; 0 when absent.
func retryAfter(h http.Header, now time.Time) time.Duration {
	ra := strings.TrimSpace(h.Get("Retry-After"))
	if ra == "" {
		return 0
	}
	var d time.Duration
	if secs, err := strconv.ParseInt(ra, 10, 64); err == nil {
		d = time.Duration(min(max(secs, 0), int64(RetryAfterMax/time.Second))) * time.Second
	} else if t, err := http.ParseTime(ra); err == nil {
		d = t.Sub(now)
	}
	return min(max(d, 0), RetryAfterMax)
}

// --- operations -------------------------------------------------------------

// Capabilities are what OPTIONS said about a resource.
type Capabilities struct {
	// DAV holds the compliance classes of the DAV header, lower-cased
	// ("1", "access-control", "calendar-access", …).
	DAV []string
	// CalendarAccess: RFC 4791; AutoSchedule: RFC 6638 server scheduling
	// (calendar-auto-schedule).
	CalendarAccess bool
	AutoSchedule   bool
	// AddressBook: RFC 6352 CardDAV (the "addressbook" class).
	AddressBook bool
	// Status of the OPTIONS answer (200, or 401 for a server that
	// advertises before authentication).
	Status int
}

// Options sends OPTIONS to href (the server URL when empty).
func (c *Client) Options(ctx context.Context, href string) (Capabilities, error) {
	u := c.rootURL()
	if href != "" {
		var err error
		if u, err = c.URL(href); err != nil {
			return Capabilities{}, err
		}
	}
	a, err := c.do(ctx, request{method: http.MethodOptions, url: u, anonymous: c.password == nil})
	if err != nil {
		return Capabilities{}, err
	}
	return parseDAVHeader(a.header, a.status), nil
}

// parseDAVHeader reads the DAV header(s): comma-separated tokens, each
// printable ASCII ≤ 64 bytes, at most 64.
func parseDAVHeader(h http.Header, status int) Capabilities {
	caps := Capabilities{Status: status}
	for _, v := range h.Values("DAV") {
		for _, t := range strings.Split(v, ",") {
			t = strings.ToLower(cleanToken(t, 64))
			if t == "" || len(caps.DAV) >= maxListEntries {
				continue
			}
			caps.DAV = append(caps.DAV, t)
			switch t {
			case "calendar-access":
				caps.CalendarAccess = true
			case "calendar-auto-schedule":
				caps.AutoSchedule = true
			case "addressbook":
				caps.AddressBook = true
			}
		}
	}
	return caps
}

// Propfind asks href (a path or a URL of a trusted origin; "" = the
// server URL) for props at depth 0 or 1 and returns the responses on the
// request's origin.
func (c *Client) Propfind(ctx context.Context, href string, depth int, props ...xml.Name) ([]Response, error) {
	u := c.rootURL()
	if href != "" {
		var err error
		if u, err = c.URL(href); err != nil {
			return nil, err
		}
	}
	ms, err := c.propfind(ctx, u, depth, props)
	if err != nil {
		return nil, err
	}
	return ms.Responses, nil
}

func (c *Client) propfind(ctx context.Context, u *url.URL, depth int, props []xml.Name) (*multistatus, error) {
	d := "0"
	if depth == 1 {
		d = "1"
	}
	return c.multistatus(ctx, request{
		method: "PROPFIND", url: u, depth: d, body: propfindBody(props),
		contentType: `application/xml; charset="utf-8"`,
	})
}

func (c *Client) report(ctx context.Context, u *url.URL, depth string, body []byte) (*multistatus, error) {
	return c.multistatus(ctx, request{
		method: "REPORT", url: u, depth: depth, body: body,
		contentType: `application/xml; charset="utf-8"`,
	})
}

// multistatus runs a PROPFIND or REPORT and parses its 207 (a 200 with an
// XML body is read too; a web page is not an answer).
func (c *Client) multistatus(ctx context.Context, rq request) (*multistatus, error) {
	rq.limit = MaxMultistatusBytes
	rq.accept = "application/xml, text/xml"
	a, err := c.do(ctx, rq)
	if err != nil {
		return nil, err
	}
	if a.status != http.StatusMultiStatus && a.status != http.StatusOK {
		return nil, malformed("%s answered %d, not a multistatus", rq.method, a.status)
	}
	if ct := strings.ToLower(a.header.Get("Content-Type")); strings.HasPrefix(ct, "text/html") {
		return nil, api.NewError(api.CodeServerError, "caldav: the server answered with a web page")
	}
	return parseMultistatus(a.body, a.url)
}
