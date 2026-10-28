// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package eway is the daemon's client of eWay-CRM's web service
// (docs/eway.md): sign-in, saving a message as an e-mail item with its
// relations, finding contacts, companies, deals and projects, and the
// supervisor of eway accounts, which synchronise nothing.
package eway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

const (
	// requestTimeout bounds one JSON exchange, uploadTimeout one upload of
	// a message, loginTimeout one LogIn (D16).
	requestTimeout = 30 * time.Second
	uploadTimeout  = 2 * time.Minute
	loginTimeout   = 15 * time.Second
	// maxConcurrent bounds the requests one client (one account) has in
	// flight.
	maxConcurrent = 4
	// retryAfterMax is the longest a request waits on a 429/503 itself,
	// throttleRetries how often; defaultThrottleWait without a
	// Retry-After.
	retryAfterMax       = 60 * time.Second
	throttleRetries     = 2
	defaultThrottleWait = 5 * time.Second
	// maxRedirects bounds a redirect chain (GET only, within the origin).
	maxRedirects = 5
	// maxJSONBytes bounds an answer, maxErrorBody what is read of an error
	// answer.
	maxJSONBytes = 8 << 20
	maxErrorBody = 64 << 10

	// AppVersion identifies the client to eWay-CRM (letters, then a
	// number); the number changes only when eWay-CRM must tell versions
	// apart (D5).
	AppVersion = "MalachiMail1"
	// MachineName is the clientMachineName sent instead of the host name
	// (D5).
	MachineName = "Malachi Mail"
)

// DefaultUserAgent is the User-Agent without a version; Cloudflare in front
// of the hosted instances refuses Go's default one (403, error code 1010).
const DefaultUserAgent = "MalachiMail (+https://github.com/schotek/malachi)"

// UserAgent is the User-Agent for a daemon version.
func UserAgent(version string) string {
	v := strings.Map(func(r rune) rune {
		if r <= ' ' || r > '~' || r == '(' || r == ')' {
			return -1
		}
		return r
	}, version)
	if v == "" {
		return DefaultUserAgent
	}
	return "MalachiMail/" + v + " (+https://github.com/schotek/malachi)"
}

// Options configures a Client.
type Options struct {
	// ServiceURL is the instance; it is normalised (NormaliseServiceURL).
	ServiceURL string
	// Username and Password sign in; Password is called at each LogIn.
	// Nil Password = anonymous calls only (DetectAuth).
	Username string
	Password func(ctx context.Context) (string, error)
	// ClientID is the random clientMachineIdentifier of this daemon (D5).
	ClientID string
	// HTTP carries the requests (nil = http.DefaultTransport); a copy is
	// used, with redirects followed here and no cookie jar.
	HTTP      *http.Client
	UserAgent string // "" = DefaultUserAgent
	Log       *slog.Logger
	Now       func() time.Time
	Sleep     func(ctx context.Context, d time.Duration) error
}

// Client talks to one eWay-CRM instance as one user. It holds the session
// in memory (lazy LogIn, one new LogIn on rcBadSession), bounds
// concurrency, honours Retry-After a bounded number of times, never
// follows a redirect of a POST and a GET only within the service's
// origin, caps what it reads and maps failures to *api.Error. Neither the
// password, its hash nor the session id is logged or part of an error.
type Client struct {
	service   string
	origin    *url.URL
	username  string
	password  func(ctx context.Context) (string, error)
	clientID  string
	http      *http.Client
	userAgent string
	log       *slog.Logger
	now       func() time.Time
	sleep     func(ctx context.Context, d time.Duration) error
	sem       chan struct{}

	loginMu sync.Mutex // one LogIn at a time
	mu      sync.Mutex
	session string
	user    string // UserItemGuid
	failed  *api.Error
	stopped bool
	state   stateRecord
	ctx     context.Context
	cancel  context.CancelFunc

	cacheMu sync.Mutex
	cache   map[string]cachedAddress
}

type stateRecord struct {
	err  *api.Error
	at   time.Time
	used bool
}

// NewClient builds a client; errors are invalidArgument.
func NewClient(opts Options) (*Client, error) {
	svc, err := NormaliseServiceURL(opts.ServiceURL)
	if err != nil {
		return nil, err
	}
	u, _ := url.Parse(svc)
	cctx, cancel := context.WithCancel(context.Background())
	c := &Client{
		ctx:       cctx,
		cancel:    cancel,
		service:   svc,
		origin:    u,
		username:  strings.TrimSpace(opts.Username),
		password:  opts.Password,
		clientID:  opts.ClientID,
		userAgent: opts.UserAgent,
		log:       opts.Log,
		now:       opts.Now,
		sleep:     opts.Sleep,
		sem:       make(chan struct{}, maxConcurrent),
		cache:     map[string]cachedAddress{},
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
		c.userAgent = DefaultUserAgent
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

// ServiceURL is the normalised instance address.
func (c *Client) ServiceURL() string { return c.service }

// errMethodUnknown is an HTTP 404 with the service's HTML page: the
// instance does not know the method (an older server).
var errMethodUnknown = errors.New("eway: method unknown to the service")

// request is one exchange.
type request struct {
	method  string // HTTP method
	path    string // after /API.svc/, with its query encoded
	body    []byte
	ctype   string
	timeout time.Duration
	secret  string // a session id to scrub from any text
}

// exchange runs rq with the retry policy and returns the answer's body of
// a 2xx response (at most maxJSONBytes).
func (c *Client) exchange(ctx context.Context, rq request) ([]byte, error) {
	if rq.timeout == 0 {
		rq.timeout = requestTimeout
	}
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, classifyTransport(ctx, ctx.Err(), rq.secret)
	}
	defer func() { <-c.sem }()
	throttled := 0
	for {
		status, header, body, err := c.send(ctx, rq)
		if err != nil {
			return nil, err
		}
		switch {
		case (status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable) && throttled < throttleRetries:
			wait := retryAfter(header, c.now())
			if wait <= 0 {
				wait = defaultThrottleWait
			}
			if wait > retryAfterMax {
				return nil, api.NewError(api.CodeServerTimeout, "eway: HTTP %d, asked to wait %s", status, wait.Round(time.Second))
			}
			throttled++
			c.log.Debug("throttled by eway", "status", status, "retryAfter", wait)
			if err := c.sleep(ctx, wait); err != nil {
				return nil, classifyTransport(ctx, err, rq.secret)
			}
			continue
		case status >= 200 && status <= 299:
			return body, nil
		}
		return nil, c.statusError(status, header, body)
	}
}

// statusError maps a non-2xx answer: 403 with Cloudflare's "error code:
// 1010" is a refused User-Agent; 404 with an HTML page is a method the
// service does not know; 401 authFailed; 429/503/504 serverTimeout; the
// rest serverError. Only the status and a class go into the error.
func (c *Client) statusError(status int, h http.Header, body []byte) error {
	switch {
	case status == http.StatusForbidden && bytes.Contains(body, []byte("1010")):
		c.log.Warn("eway refused the request", "class", "blockedUserAgent")
		return api.NewError(api.CodeServerError, "eway: HTTP 403 (blockedUserAgent)")
	case status == http.StatusNotFound && strings.HasPrefix(strings.ToLower(h.Get("Content-Type")), "text/html"):
		return errMethodUnknown
	case status == http.StatusUnauthorized:
		return api.NewError(api.CodeAuthFailed, "eway: HTTP 401")
	case status == http.StatusTooManyRequests, status == http.StatusServiceUnavailable, status == http.StatusGatewayTimeout:
		return api.NewError(api.CodeServerTimeout, "eway: HTTP %d", status)
	}
	return api.NewError(api.CodeServerError, "eway: HTTP %d", status)
}

// send runs one request. A redirect of a POST is an error; a GET follows
// at most maxRedirects within the service's origin (scheme, host, port),
// never to http and never to another host.
func (c *Client) send(ctx context.Context, rq request) (int, http.Header, []byte, error) {
	rctx, cancel := context.WithTimeout(ctx, rq.timeout)
	defer cancel()
	u, err := url.Parse(apiBase(c.service) + "/" + rq.path)
	if err != nil {
		return 0, nil, nil, api.NewError(api.CodeInvalidArgument, "eway: bad request URL")
	}
	for hop := 0; ; hop++ {
		var body io.Reader
		if rq.body != nil {
			body = bytes.NewReader(rq.body)
		}
		req, err := http.NewRequestWithContext(rctx, rq.method, u.String(), body)
		if err != nil {
			return 0, nil, nil, api.NewError(api.CodeInvalidArgument, "eway: bad request")
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", c.userAgent)
		if rq.body != nil {
			ct := rq.ctype
			if ct == "" {
				ct = "application/json"
			}
			req.Header.Set("Content-Type", ct)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return 0, nil, nil, classifyTransport(ctx, err, rq.secret)
		}
		if isRedirect(resp.StatusCode) {
			loc := resp.Header.Get("Location")
			drain(resp)
			if rq.method != http.MethodGet {
				return 0, nil, nil, api.NewError(api.CodeServerError, "eway: unexpected redirect")
			}
			if hop >= maxRedirects {
				return 0, nil, nil, api.NewError(api.CodeServerError, "eway: too many redirects")
			}
			next, err := u.Parse(loc)
			if loc == "" || err != nil || next.User != nil || !c.sameOrigin(next) {
				return 0, nil, nil, api.NewError(api.CodeServerError, "eway: redirect outside the service refused")
			}
			next.Fragment, next.RawFragment = "", ""
			u = next
			continue
		}
		limit := int64(maxJSONBytes)
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			limit = maxErrorBody
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
		resp.Body.Close()
		if err != nil {
			return 0, nil, nil, classifyTransport(ctx, err, rq.secret)
		}
		if int64(len(data)) > limit {
			if limit == maxErrorBody {
				data = data[:limit]
			} else {
				return 0, nil, nil, api.NewError(api.CodeServerError, "eway: answer larger than %d bytes", maxJSONBytes)
			}
		}
		return resp.StatusCode, resp.Header, data, nil
	}
}

func (c *Client) sameOrigin(u *url.URL) bool {
	return strings.EqualFold(u.Scheme, c.origin.Scheme) && strings.EqualFold(u.Hostname(), c.origin.Hostname()) && effectivePort(u) == effectivePort(c.origin)
}

func effectivePort(u *url.URL) string {
	if p := u.Port(); p != "" && p != defaultPort(strings.ToLower(u.Scheme)) {
		return p
	}
	return ""
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

// retryAfter is the wait a throttled answer asks for, at most an hour.
func retryAfter(h http.Header, now time.Time) time.Duration {
	ra := strings.TrimSpace(h.Get("Retry-After"))
	var d time.Duration
	if secs, err := strconv.ParseInt(ra, 10, 64); err == nil {
		d = time.Duration(min(max(secs, 0), 3600)) * time.Second
	} else if t, err := http.ParseTime(ra); err == nil {
		d = t.Sub(now)
	}
	return min(max(d, 0), time.Hour)
}

// classifyTransport maps a transport failure; the session id (in an
// upload's query) is scrubbed from the text.
func classifyTransport(ctx context.Context, err error, secret string) error {
	var ae *api.Error
	if errors.As(err, &ae) {
		return ae
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	e := transport.Classify(ctx, transport.StageDial, err)
	if secret != "" {
		e.Message = strings.ReplaceAll(e.Message, secret, "***")
	}
	if e.Code != api.CodeCancelled && !strings.HasPrefix(e.Message, "eway: ") {
		e.Message = "eway: " + e.Message
	}
	return e
}

// post sends a JSON body to method and decodes the envelope. The return
// code is the caller's to judge.
func (c *Client) post(ctx context.Context, method string, body any, secret string, timeout time.Duration) (*envelope, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(body); err != nil {
		return nil, fmt.Errorf("eway: encode %s: %w", method, err)
	}
	raw, err := c.exchange(ctx, request{method: http.MethodPost, path: method, body: bytes.TrimSpace(buf.Bytes()), secret: secret, timeout: timeout})
	if err != nil {
		if errors.Is(err, errMethodUnknown) {
			return nil, api.NewError(api.CodeServerError, "eway: the service does not know %s", method)
		}
		return nil, err
	}
	return decodeEnvelope(raw)
}

// AuthSettings is what an instance says about its sign-in before one.
type AuthSettings struct {
	// Known: the instance answered GetServiceAuthSettings (an older one
	// does not have it, which says nothing about Microsoft sign-in).
	Known      bool
	AzureAuth  bool
	WcfVersion string
}

// DetectAuth asks the instance anonymously how it signs in (GET
// GetServiceAuthSettings, T0 1). A service that does not know the method
// is Known false, not an error; a page that is not eWay-CRM is
// serverError.
func (c *Client) DetectAuth(ctx context.Context) (AuthSettings, error) {
	raw, err := c.exchange(ctx, request{method: http.MethodGet, path: "GetServiceAuthSettings", timeout: loginTimeout})
	if errors.Is(err, errMethodUnknown) {
		return AuthSettings{}, nil
	}
	if err != nil {
		return AuthSettings{}, err
	}
	env, err := decodeEnvelope(raw)
	if err != nil {
		return AuthSettings{}, api.NewError(api.CodeServerError, "eway: the address is not an eWay-CRM web service")
	}
	if env.ReturnCode != rcSuccess {
		return AuthSettings{}, api.NewError(api.CodeServerError, "eway: GetServiceAuthSettings: %s", rcClass(string(env.ReturnCode)))
	}
	return AuthSettings{Known: true, AzureAuth: env.IsAzureAuth != nil && *env.IsAzureAuth, WcfVersion: cleanDetail(string(env.WcfVersion))}, nil
}
