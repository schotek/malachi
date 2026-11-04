// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Where the manifest is (docs/services.md §2.1, §2.6): the API, then the
// asset of the latest GitHub release. The daemon trusts neither host,
// only the signature.
const (
	PrimaryURL  = "https://admin.malachimail.app/api/v1/updates"
	FallbackURL = "https://github.com/schotek/malachi/releases/latest/download/updates.json"
)

const (
	// Timeout bounds one request, connection to last byte.
	Timeout = 30 * time.Second
	// MaxRetryAfter caps a server's Retry-After.
	MaxRetryAfter = 24 * time.Hour
	maxRedirects  = 5
)

// Failure is the class of a failed check (docs/services.md §2.5).
type Failure string

const (
	FailNetwork   Failure = "network"
	FailServer    Failure = "server"
	FailSignature Failure = "signature"
	FailFormat    Failure = "format"
)

// FailureOf classifies an *Error of Verify or Parse.
func FailureOf(err error) Failure {
	var e *Error
	if errors.As(err, &e) && (e.Kind == Signature || e.Kind == UnknownKey) {
		return FailSignature
	}
	return FailFormat
}

// FetchError is a request that brought no envelope. Its text names the
// class and the status, never a body.
type FetchError struct {
	Class      Failure
	Status     int
	RetryAfter time.Duration // 429 and 503 only, capped
	Err        error
}

func (e *FetchError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("update: %s: HTTP %d", e.Class, e.Status)
	}
	return fmt.Sprintf("update: %s: %v", e.Class, e.Err)
}

func (e *FetchError) Unwrap() error { return e.Err }

// Response is what a request brought: an envelope, or NotModified.
type Response struct {
	Envelope    []byte
	NotModified bool
	ETag        string
}

// Client fetches envelopes. The zero value is not usable; use NewClient.
type Client struct {
	// Primary asks the API: no redirect, an answer must be JSON.
	Primary *http.Client
	// Fallback asks GitHub, whose release asset redirects to its download
	// host: https only, on FallbackHosts only, at most five hops.
	Fallback      *http.Client
	UserAgent     string
	PrimaryURL    string
	FallbackURL   string
	FallbackHosts []string // exact hosts, or ".suffix"
}

// NewClient returns the client of the daemon of version, with the system
// proxy like the daemon's other HTTP clients and no cookie jar.
func NewClient(version string) *Client {
	c := &Client{
		UserAgent:     "MalachiMail/" + version,
		PrimaryURL:    PrimaryURL,
		FallbackURL:   FallbackURL,
		FallbackHosts: []string{"github.com", ".githubusercontent.com"},
	}
	transport := func() *http.Transport {
		return &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 15 * time.Second,
			MaxIdleConns:          2,
			IdleConnTimeout:       30 * time.Second,
		}
	}
	c.Primary = &http.Client{
		Timeout:   Timeout,
		Transport: transport(),
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse // a redirect is an answer, and not a 200
		},
	}
	c.Fallback = &http.Client{
		Timeout:       Timeout,
		Transport:     transport(),
		CheckRedirect: c.checkFallbackRedirect,
	}
	return c
}

func (c *Client) checkFallbackRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return errors.New("too many redirects")
	}
	if req.URL.Scheme != "https" || req.URL.User != nil || req.URL.Port() != "" || !c.fallbackHost(req.URL.Hostname()) {
		return errors.New("redirect off the allowed hosts")
	}
	req.Header.Del("Referer")
	return nil
}

func (c *Client) fallbackHost(h string) bool {
	for _, a := range c.FallbackHosts {
		if h == a || (strings.HasPrefix(a, ".") && strings.HasSuffix(h, a)) {
			return true
		}
	}
	return false
}

// FetchPrimary asks the API, with If-None-Match when etag is set.
func (c *Client) FetchPrimary(ctx context.Context, etag string) (Response, error) {
	return c.fetch(ctx, c.Primary, c.PrimaryURL, etag, true)
}

// FetchFallback asks GitHub.
func (c *Client) FetchFallback(ctx context.Context) (Response, error) {
	return c.fetch(ctx, c.Fallback, c.FallbackURL, "", false)
}

func (c *Client) fetch(ctx context.Context, hc *http.Client, url, etag string, primary bool) (Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Response{}, &FetchError{Class: FailNetwork, Err: err}
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return Response{}, &FetchError{Class: FailNetwork, Err: errors.Unwrap(err)}
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotModified && etag != "":
		return Response{NotModified: true, ETag: etag}, nil
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable:
		return Response{}, &FetchError{Class: FailServer, Status: resp.StatusCode,
			RetryAfter: RetryAfter(resp.Header.Get("Retry-After"), time.Now())}
	case resp.StatusCode != http.StatusOK:
		return Response{}, &FetchError{Class: FailServer, Status: resp.StatusCode}
	}
	if primary {
		// Anything but JSON (a sign-in page in front of the API) is not
		// an API answer.
		mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		if err != nil || mt != "application/json" {
			return Response{}, &FetchError{Class: FailServer, Status: resp.StatusCode, Err: errors.New("not JSON")}
		}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxEnvelope+1))
	if err != nil {
		return Response{}, &FetchError{Class: FailNetwork, Err: err}
	}
	if len(body) > MaxEnvelope {
		return Response{}, &FetchError{Class: FailFormat, Err: errors.New("envelope over the limit")}
	}
	r := Response{Envelope: body}
	if primary {
		r.ETag = resp.Header.Get("ETag")
	}
	return r, nil
}

// RetryAfter reads a Retry-After header (seconds or an HTTP date),
// capped at MaxRetryAfter; 0 when absent or unreadable.
func RetryAfter(h string, now time.Time) time.Duration {
	h = strings.TrimSpace(h)
	if h == "" {
		return 0
	}
	var d time.Duration
	if n, err := strconv.ParseInt(h, 10, 64); err == nil {
		if n <= 0 {
			return 0
		}
		if n > int64(MaxRetryAfter/time.Second) {
			return MaxRetryAfter
		}
		d = time.Duration(n) * time.Second
	} else if t, err := http.ParseTime(h); err == nil {
		d = t.Sub(now)
	}
	return min(max(d, 0), MaxRetryAfter)
}
