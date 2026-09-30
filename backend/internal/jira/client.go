// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

const (
	// requestTimeout bounds one JSON exchange (redirects and the body
	// included); contentTimeout one attachment or picture download.
	requestTimeout = 60 * time.Second
	contentTimeout = 5 * time.Minute
	// maxConcurrent bounds the requests one client (one account) has in
	// flight: Jira Cloud's rate limiter counts per user.
	maxConcurrent = 4
	// retryAfterMax is the longest a request waits on a 429/503 itself;
	// a longer Retry-After ends the request with a StatusError carrying
	// it (the syncer backs off as a whole). throttleRetries is how often
	// one request waits; defaultThrottleWait is the wait when the server
	// names none.
	retryAfterMax       = 60 * time.Second
	throttleRetries     = 2
	defaultThrottleWait = 5 * time.Second
	// retryAfterCap bounds what a server may ask for at all.
	retryAfterCap = time.Hour
	// maxRedirects bounds a redirect chain.
	maxRedirects = 5
	// maxJSONBytes bounds a JSON response; maxErrorBody what is read of an
	// error response.
	maxJSONBytes = 32 << 20
	maxErrorBody = 64 << 10
	// GatewayBaseURL + cloud id is the route through the Atlassian API
	// gateway, which scoped API tokens need.
	GatewayBaseURL   = "https://api.atlassian.com/ex/jira/"
	defaultUserAgent = "Malachi-Mail"
)

// ErrTooLarge ends a content download (OpenContent) that exceeds its limit.
var ErrTooLarge = errors.New("jira: content over the size limit")

// cloudIDPattern is the shape of a cloud id (a UUID).
var cloudIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Options configures a Client.
type Options struct {
	// SiteURL is the site; it is normalised (NormaliseSiteURL).
	SiteURL string
	// Deployment picks the authentication and the REST flavour. It may be
	// empty only for an anonymous client (Token nil).
	Deployment api.JiraDeployment
	// CloudID (cloud only) enables the gateway route: a request the site
	// refuses with 401 is tried once through GatewayBaseURL+CloudID (and
	// the other way round), and the route that worked is kept.
	CloudID string
	// Login is the Atlassian account e-mail (cloud, required with a
	// token); datacenter ignores it.
	Login string
	// Token returns the API token (cloud) or personal access token
	// (datacenter); called for every request, so the caller caches. Nil =
	// anonymous requests only (site detection).
	Token func(ctx context.Context) (string, error)
	// HTTP is the client whose Transport carries the requests (nil =
	// http.DefaultTransport). The client itself is not modified: its
	// redirect policy and cookie jar are replaced in a copy (redirects are
	// followed here, cookies never kept).
	HTTP *http.Client
	Log  *slog.Logger
	// Now is the clock for Retry-After dates (nil = time.Now); Sleep the
	// wait on a 429/503 (nil = a timer honouring the context).
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error
	// Gateway starts on the gateway route (a route remembered from an
	// earlier client of the same account). Ignored without CloudID.
	Gateway bool
	// UserAgent is the User-Agent header ("" = "Malachi-Mail").
	UserAgent string
}

// Client is a thin Jira REST client for one site and one set of
// credentials. It authenticates per deployment (cloud: Basic
// login:token; datacenter: Bearer), bounds concurrency, honours
// Retry-After a bounded number of times, follows redirects itself
// (Authorization only ever goes to the site's origin or to the API
// gateway, never to another host), caps what it reads and maps failures
// to *StatusError or *api.Error. The token is never logged and never part
// of an error.
type Client struct {
	site        *url.URL
	siteBase    string
	gatewayBase string // "" without a cloud id
	deployment  api.JiraDeployment
	cloudID     string
	login       string
	token       func(ctx context.Context) (string, error)
	http        *http.Client
	log         *slog.Logger
	now         func() time.Time
	sleep       func(ctx context.Context, d time.Duration) error
	userAgent   string
	sem         chan struct{}
	gateway     atomic.Bool
}

// NewClient builds a client from opts. Errors are invalidArgument.
func NewClient(opts Options) (*Client, error) {
	site, err := NormaliseSiteURL(opts.SiteURL)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(site)
	if err != nil {
		return nil, api.NewError(api.CodeInvalidArgument, "jira: site URL does not parse")
	}
	switch opts.Deployment {
	case api.JiraCloud, api.JiraDataCenter:
	case "":
		if opts.Token != nil {
			return nil, api.NewError(api.CodeInvalidArgument, "jira: deployment is required with a token")
		}
	default:
		return nil, api.NewError(api.CodeInvalidArgument, "jira: unknown deployment")
	}
	if opts.Deployment == api.JiraCloud && opts.Token != nil && strings.TrimSpace(opts.Login) == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "jira: a cloud site needs the login of the API token")
	}
	if opts.CloudID != "" && !cloudIDPattern.MatchString(opts.CloudID) {
		return nil, api.NewError(api.CodeInvalidArgument, "jira: cloud id is not a UUID")
	}
	c := &Client{
		site:       u,
		siteBase:   site,
		deployment: opts.Deployment,
		cloudID:    strings.ToLower(opts.CloudID),
		login:      strings.TrimSpace(opts.Login),
		token:      opts.Token,
		log:        opts.Log,
		now:        opts.Now,
		sleep:      opts.Sleep,
		userAgent:  opts.UserAgent,
		sem:        make(chan struct{}, maxConcurrent),
	}
	if c.cloudID != "" && c.deployment != api.JiraDataCenter {
		c.gatewayBase = GatewayBaseURL + c.cloudID
		c.gateway.Store(opts.Gateway)
	}
	var hc http.Client
	if opts.HTTP != nil {
		hc = *opts.HTTP
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	hc.Jar = nil
	c.http = &hc
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

// SiteURL is the normalised site.
func (c *Client) SiteURL() string { return c.siteBase }

// Deployment is the site's deployment ("" for an anonymous client).
func (c *Client) Deployment() api.JiraDeployment { return c.deployment }

// CloudID is the cloud id the client routes with ("" without one).
func (c *Client) CloudID() string { return c.cloudID }

// Gateway reports whether requests go through the Atlassian API gateway
// (the route that worked last).
func (c *Client) Gateway() bool { return c.gatewayBase != "" && c.gateway.Load() }

// StatusError is a non-2xx answer: the status, the service's error text
// (cleaned, credentials redacted) and, for 429/503, how long the service
// asked to wait.
type StatusError struct {
	Status     int
	Message    string
	RetryAfter time.Duration
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("jira: HTTP %d: %s", e.Status, e.Message)
}

func statusOf(err error) int {
	var se *StatusError
	if errors.As(err, &se) {
		return se.Status
	}
	return 0
}

// IsNotFound reports a 404: the item is gone or not visible to the user.
func IsNotFound(err error) bool { return statusOf(err) == http.StatusNotFound }

// IsForbidden reports a 403: the user may not see this (a space whose
// permission was taken away; only that space is affected).
func IsForbidden(err error) bool { return statusOf(err) == http.StatusForbidden }

// IsUnauthorized reports a 401 on both routes: the token (or the login it
// goes with) is not accepted.
func IsUnauthorized(err error) bool { return statusOf(err) == http.StatusUnauthorized }

// RetryAfter is how long a throttled request asked to wait (a 429/503
// whose Retry-After exceeded what the client waits by itself); 0 for
// anything else.
func RetryAfter(err error) time.Duration {
	var se *StatusError
	if errors.As(err, &se) {
		return se.RetryAfter
	}
	return 0
}

// ToAPIError maps a client failure to the contract: *api.Error passes
// through; a StatusError becomes authFailed (401), serverTimeout (429,
// 503, 504: transient) or serverError (403, 404 and the rest; IsNotFound
// and IsForbidden tell those apart before mapping); ErrTooLarge is
// attachmentTooBig; transport failures are networkError, tlsError or
// serverTimeout. Nil for nil.
func ToAPIError(err error) *api.Error {
	if err == nil {
		return nil
	}
	var ae *api.Error
	if errors.As(err, &ae) {
		return ae
	}
	var se *StatusError
	if errors.As(err, &se) {
		msg := transport.CleanMessage(fmt.Sprintf("HTTP %d: %s", se.Status, se.Message))
		switch se.Status {
		case http.StatusUnauthorized:
			return api.NewError(api.CodeAuthFailed, "jira: %s", msg)
		case http.StatusTooManyRequests, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return api.NewError(api.CodeServerTimeout, "jira: %s", msg)
		}
		return api.NewError(api.CodeServerError, "jira: %s", msg)
	}
	switch {
	case errors.Is(err, ErrTooLarge):
		return api.NewError(api.CodeAttachmentTooBig, "jira: content over the size limit")
	case errors.Is(err, context.Canceled):
		return api.NewError(api.CodeCancelled, "cancelled")
	case errors.Is(err, context.DeadlineExceeded):
		return api.NewError(api.CodeServerTimeout, "jira: request timed out")
	}
	return api.NewError(api.CodeNetworkError, "jira: %s", transport.CleanMessage(err.Error()))
}

// request is one API call. path is relative to the route (the site, or
// the gateway), starts with "/" and carries its query already encoded.
type request struct {
	method    string
	path      string
	body      []byte // JSON; nil = no body
	accept    string // "" = application/json
	siteOnly  bool   // never the gateway (detection, tenant info)
	anonymous bool   // no Authorization
	crossHost bool   // follow redirects to other hosts (credentials dropped)
	timeout   time.Duration
}

// getJSON fetches a JSON document (at most maxJSONBytes).
func (c *Client) getJSON(ctx context.Context, path string, q url.Values) ([]byte, error) {
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return c.fetchJSON(ctx, request{method: http.MethodGet, path: path})
}

// postJSON sends body as JSON and returns the JSON answer.
func (c *Client) postJSON(ctx context.Context, path string, body any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(body); err != nil {
		return nil, fmt.Errorf("jira: encode request: %w", err)
	}
	return c.fetchJSON(ctx, request{method: http.MethodPost, path: path, body: bytes.TrimSpace(buf.Bytes())})
}

func (c *Client) fetchJSON(ctx context.Context, rq request) ([]byte, error) {
	resp, err := c.exchange(ctx, rq)
	if err != nil {
		return nil, err
	}
	return readJSON(ctx, resp)
}

// readJSON reads a successful JSON response within maxJSONBytes. An HTML
// page (a login form, a proxy's error page) is not a JSON response.
func readJSON(ctx context.Context, resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	if ct := strings.ToLower(resp.Header.Get("Content-Type")); strings.HasPrefix(ct, "text/html") {
		return nil, api.NewError(api.CodeServerError, "jira: the site answered with a web page, not JSON")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxJSONBytes+1))
	if err != nil {
		return nil, classifyTransport(ctx, err)
	}
	if len(data) > maxJSONBytes {
		return nil, api.NewError(api.CodeServerError, "jira: response larger than %d bytes", maxJSONBytes)
	}
	return data, nil
}

// exchange runs one call with the route and retry policy and returns a
// 2xx response whose body the caller must close. Any other status is a
// *StatusError; transport failures are *api.Error.
func (c *Client) exchange(ctx context.Context, rq request) (*http.Response, error) {
	if rq.timeout == 0 {
		rq.timeout = requestTimeout
	}
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, classifyTransport(ctx, ctx.Err())
	}
	defer func() { <-c.sem }()

	var token string
	if !rq.anonymous {
		if c.token == nil {
			return nil, api.NewError(api.CodeAuthRequired, "jira: no token")
		}
		t, err := c.token(ctx)
		if err != nil {
			return nil, err
		}
		if t == "" {
			return nil, api.NewError(api.CodeAuthRequired, "jira: no token")
		}
		token = t
	}
	canSwitch := !rq.anonymous && !rq.siteOnly && c.gatewayBase != ""
	viaGateway := canSwitch && c.gateway.Load()
	switched := false
	throttled := 0
	for {
		base := c.siteBase
		if viaGateway {
			base = c.gatewayBase
		}
		resp, _, err := c.send(ctx, rq, base+rq.path, token)
		if err != nil {
			return nil, err
		}
		switch {
		case resp.StatusCode == http.StatusUnauthorized && canSwitch && !switched:
			drain(resp)
			switched = true
			viaGateway = !viaGateway
			c.log.Debug("jira route refused the token, trying the other one", "gateway", viaGateway)
			continue
		case (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable) && throttled < throttleRetries:
			se := c.statusError(resp, token)
			wait := se.RetryAfter
			if wait <= 0 {
				wait = defaultThrottleWait
			}
			if wait > retryAfterMax {
				se.RetryAfter = wait
				return nil, se
			}
			throttled++
			c.log.Debug("throttled by jira", "status", se.Status, "retryAfter", wait)
			if err := c.sleep(ctx, wait); err != nil {
				return nil, classifyTransport(ctx, err)
			}
			continue
		case resp.StatusCode < 200 || resp.StatusCode > 299:
			return nil, c.statusError(resp, token)
		}
		if switched && c.gateway.Swap(viaGateway) != viaGateway {
			c.log.Info("jira route changed", "gateway", viaGateway)
		}
		return resp, nil
	}
}

// send runs one request and follows redirects itself: GET only, at most
// maxRedirects, https only (plain http only within the origin of an http
// request chain), another host only with rq.crossHost. Authorization is
// set only while the chain stays on its first host; once it left, never
// again. It returns the final response and URL. The body is bound to the
// request's timeout, which ends when the body is closed.
func (c *Client) send(ctx context.Context, rq request, rawURL, token string) (*http.Response, *url.URL, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, nil, api.NewError(api.CodeInvalidArgument, "jira: bad request URL")
	}
	rctx, cancel := context.WithTimeout(ctx, rq.timeout)
	first := u
	left := false
	for hop := 0; ; hop++ {
		var body io.Reader
		if rq.body != nil {
			body = bytes.NewReader(rq.body)
		}
		req, err := http.NewRequestWithContext(rctx, rq.method, u.String(), body)
		if err != nil {
			cancel()
			return nil, nil, api.NewError(api.CodeInvalidArgument, "jira: bad request: %s", transport.CleanMessage(err.Error()))
		}
		accept := rq.accept
		if accept == "" {
			accept = "application/json"
		}
		req.Header.Set("Accept", accept)
		req.Header.Set("User-Agent", c.userAgent)
		if rq.body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if rq.method != http.MethodGet && rq.method != http.MethodHead {
			req.Header.Set("X-Atlassian-Token", "no-check")
		}
		if strings.Contains(u.EscapedPath(), "/rest/servicedeskapi/") {
			req.Header.Set("X-ExperimentalApi", "opt-in")
		}
		if token != "" && !left {
			req.Header.Set("Authorization", c.authorization(token))
		}
		resp, err := c.http.Do(req)
		if err != nil {
			cancel()
			return nil, nil, classifyTransport(ctx, err)
		}
		if !isRedirect(resp.StatusCode) {
			resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
			return resp, u, nil
		}
		loc := resp.Header.Get("Location")
		drain(resp)
		if rq.method != http.MethodGet && rq.method != http.MethodHead {
			cancel()
			return nil, nil, &StatusError{Status: resp.StatusCode, Message: "unexpected redirect of a " + rq.method + " request"}
		}
		if hop >= maxRedirects {
			cancel()
			return nil, nil, api.NewError(api.CodeServerError, "jira: too many redirects")
		}
		next, err := u.Parse(loc)
		if loc == "" || err != nil || next.Host == "" || next.User != nil {
			cancel()
			return nil, nil, api.NewError(api.CodeServerError, "jira: redirect without a usable location")
		}
		next.Fragment, next.RawFragment = "", ""
		switch {
		case next.Scheme == "https":
		case next.Scheme == "http" && first.Scheme == "http" && sameHost(next, first):
		default:
			cancel()
			return nil, nil, api.NewError(api.CodeServerError, "jira: redirect leaves https")
		}
		if !sameHost(next, first) {
			if !rq.crossHost {
				cancel()
				return nil, nil, api.NewError(api.CodeServerError, "jira: redirect to another host refused")
			}
			left = true
		}
		u = next
	}
}

func isRedirect(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}

// sameHost compares host names (case-insensitively) and ports, a port
// left out counting as its scheme's default: an http→https upgrade on the
// default ports stays on the host.
func sameHost(a, b *url.URL) bool {
	return strings.EqualFold(a.Hostname(), b.Hostname()) && effectivePort(a) == effectivePort(b)
}

func effectivePort(u *url.URL) string {
	if p := u.Port(); p != "" && p != defaultPort(u.Scheme) {
		return p
	}
	return ""
}

func defaultPort(scheme string) string {
	switch scheme {
	case "https":
		return "443"
	case "http":
		return "80"
	}
	return ""
}

// authorization is the Authorization header value for token.
func (c *Client) authorization(token string) string {
	if c.deployment == api.JiraCloud {
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(c.login+":"+token))
	}
	return "Bearer " + token
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBody))
	resp.Body.Close()
}

// statusError reads the service's error envelope
// ({"errorMessages":[...],"errors":{...}}, or the gateway's
// {"message":...}); an HTML page contributes only its status text. The
// token and the authorization it builds are redacted before the text is
// cut to size.
func (c *Client) statusError(resp *http.Response, token string) *StatusError {
	defer resp.Body.Close()
	se := &StatusError{Status: resp.StatusCode, RetryAfter: retryAfter(resp.Header, c.now())}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	msg := envelopeMessage(raw)
	if msg == "" {
		msg = http.StatusText(resp.StatusCode)
	}
	if token != "" {
		for _, secret := range []string{c.authorization(token), base64.StdEncoding.EncodeToString([]byte(c.login + ":" + token)), token} {
			msg = strings.ReplaceAll(msg, secret, "***")
		}
	}
	se.Message = transport.CleanMessage(msg)
	return se
}

// envelopeMessage is the text of a Jira error envelope, "" when raw is not
// one.
func envelopeMessage(raw []byte) string {
	var env struct {
		ErrorMessages []flexString          `json:"errorMessages"`
		Errors        map[string]flexString `json:"errors"`
		Message       flexString            `json:"message"`
		ErrorMessage  flexString            `json:"errorMessage"`
	}
	if !decodeLenient(raw, &env) {
		return ""
	}
	var parts []string
	for _, m := range env.ErrorMessages {
		if s := strings.TrimSpace(string(m)); s != "" {
			parts = append(parts, s)
		}
	}
	keys := make([]string, 0, len(env.Errors))
	for k := range env.Errors {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts = append(parts, k+": "+string(env.Errors[k]))
	}
	for _, m := range []flexString{env.Message, env.ErrorMessage} {
		if s := strings.TrimSpace(string(m)); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "; ")
}

// retryAfter is the wait a throttled response asks for: Retry-After in
// seconds or as an HTTP date, else X-RateLimit-Reset (an ISO 8601 time,
// or epoch seconds); 0 when it names none. Capped at retryAfterCap.
func retryAfter(h http.Header, now time.Time) time.Duration {
	var d time.Duration
	if ra := strings.TrimSpace(h.Get("Retry-After")); ra != "" {
		if secs, err := strconv.ParseInt(ra, 10, 64); err == nil {
			d = time.Duration(min(max(secs, 0), int64(retryAfterCap/time.Second))) * time.Second
		} else if t, err := http.ParseTime(ra); err == nil {
			d = t.Sub(now)
		}
	} else if rs := strings.TrimSpace(h.Get("X-RateLimit-Reset")); rs != "" {
		if t, ok := parseResetTime(rs); ok {
			d = t.Sub(now)
		}
	}
	return min(max(d, 0), retryAfterCap)
}

func parseResetTime(s string) (time.Time, bool) {
	if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 1_000_000_000 && n < 100_000_000_000 {
		return time.Unix(n, 0), true
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04Z07:00", "2006-01-02T15:04:05Z0700", "2006-01-02T15:04Z0700"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// classifyTransport maps a transport failure (unwrapping *url.Error):
// cancelled, serverTimeout, tlsError with the certificate's details, or
// networkError. Request URLs carry no credentials, so neither does the
// text.
func classifyTransport(ctx context.Context, err error) error {
	var ae *api.Error
	if errors.As(err, &ae) {
		return ae
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	e := transport.Classify(ctx, transport.StageDial, err)
	if e.Code != api.CodeCancelled && !strings.HasPrefix(e.Message, "jira: ") {
		e.Message = "jira: " + e.Message
	}
	return e
}
