// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

// DefaultBaseURL is the Microsoft Graph v1.0 endpoint of the global cloud.
const DefaultBaseURL = "https://graph.microsoft.com/v1.0"

const (
	// requestTimeout bounds one JSON round trip; bodyTimeout one $value
	// download (the raw cap is maxRawMessageBytes).
	requestTimeout = 60 * time.Second
	bodyTimeout    = 5 * time.Minute
	// maxConcurrent is the Outlook service's per-mailbox concurrency limit.
	maxConcurrent = 4
	// retryAfterMax caps how long one request waits on a 429/503 before
	// giving up on it (the syncer then backs off as a whole);
	// throttleRetries is how many times that happens per request.
	retryAfterMax   = 60 * time.Second
	throttleRetries = 2
	// maxErrorBody bounds what is read of an error response.
	maxErrorBody = 64 << 10
	// preferHeader asks for immutable ids (a move keeps the id) and full
	// pages.
	preferHeader = `IdType="ImmutableId", odata.maxpagesize=200`
)

// Options configures a Client. Token is required.
type Options struct {
	BaseURL string       // "" = DefaultBaseURL
	HTTP    *http.Client // nil = a client with sane timeouts
	// Token returns a valid access token; Invalidate (optional) drops a
	// cached one after the service rejected it.
	Token      func(ctx context.Context) (string, error)
	Invalidate func()
	Log        *slog.Logger
	// Sleep is the wait used for Retry-After; tests inject a fast one.
	Sleep func(ctx context.Context, d time.Duration) error
}

// Client is a thin Microsoft Graph HTTP client: it adds the token and the
// Prefer header, bounds concurrency, honours Retry-After a bounded number
// of times, refreshes the token once on 401 and maps failures to
// *api.Error / *StatusError. Tokens are never logged.
type Client struct {
	base       string
	http       *http.Client
	token      func(ctx context.Context) (string, error)
	invalidate func()
	log        *slog.Logger
	sleep      func(ctx context.Context, d time.Duration) error
	sem        chan struct{}
}

// NewClient builds a client from opts.
func NewClient(opts Options) *Client {
	c := &Client{
		base:       strings.TrimRight(opts.BaseURL, "/"),
		http:       opts.HTTP,
		token:      opts.Token,
		invalidate: opts.Invalidate,
		log:        opts.Log,
		sleep:      opts.Sleep,
		sem:        make(chan struct{}, maxConcurrent),
	}
	if c.base == "" {
		c.base = DefaultBaseURL
	}
	if c.http == nil {
		c.http = &http.Client{Timeout: bodyTimeout}
	}
	if c.log == nil {
		c.log = slog.New(slog.DiscardHandler)
	}
	if c.invalidate == nil {
		c.invalidate = func() {}
	}
	if c.sleep == nil {
		c.sleep = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
	}
	return c
}

// StatusError is a non-2xx answer with the service's error code and
// message (technical text, cleaned).
type StatusError struct {
	Status     int
	Code       string
	Message    string
	RetryAfter time.Duration
}

func (e *StatusError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("graph: HTTP %d %s: %s", e.Status, e.Code, e.Message)
	}
	return fmt.Sprintf("graph: HTTP %d: %s", e.Status, e.Message)
}

// IsNotFound reports a 404: the item (or folder) is gone.
func IsNotFound(err error) bool {
	var se *StatusError
	return errors.As(err, &se) && se.Status == http.StatusNotFound
}

// IsGone reports 410 (a delta token the service no longer accepts).
func IsGone(err error) bool {
	var se *StatusError
	return errors.As(err, &se) && se.Status == http.StatusGone
}

// isSyncStateError reports a delta failure that asks for a full resync.
func isSyncStateError(err error) bool {
	var se *StatusError
	if !errors.As(err, &se) {
		return false
	}
	return se.Status == http.StatusGone || strings.HasPrefix(se.Code, "SyncState")
}

// ToAPIError maps a client failure to the contract: *api.Error passes
// through; a StatusError becomes authRequired (401), serverTimeout (429,
// 503, 504: transient, the syncer backs off), serverError otherwise.
func ToAPIError(err error) *api.Error {
	var ae *api.Error
	if errors.As(err, &ae) {
		return ae
	}
	var se *StatusError
	if errors.As(err, &se) {
		msg := transport.CleanMessage(fmt.Sprintf("HTTP %d %s: %s", se.Status, se.Code, se.Message))
		switch se.Status {
		case http.StatusUnauthorized:
			return api.NewError(api.CodeAuthRequired, "graph: %s", msg)
		case http.StatusTooManyRequests, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return api.NewError(api.CodeServerTimeout, "graph: %s", msg)
		}
		return api.NewError(api.CodeServerError, "graph: %s", msg)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return api.NewError(api.CodeServerTimeout, "graph: request timed out")
	}
	return api.NewError(api.CodeNetworkError, "graph: %s", transport.CleanMessage(err.Error()))
}

// URL joins a path (or a query) onto the base; an absolute URL (a
// nextLink) is used as is.
func (c *Client) URL(p string) string {
	if strings.HasPrefix(p, "https://") || strings.HasPrefix(p, "http://") {
		return p
	}
	return c.base + "/" + strings.TrimLeft(p, "/")
}

// Get fetches JSON into out.
func (c *Client) Get(ctx context.Context, u string, out any) error {
	return c.do(ctx, http.MethodGet, u, nil, "", out, requestTimeout)
}

// Post sends body as JSON (nil = empty) and decodes JSON into out (nil =
// discard).
func (c *Client) Post(ctx context.Context, u string, body, out any) error {
	r, ct, err := jsonBody(body)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, u, r, ct, out, requestTimeout)
}

// Patch sends body as JSON.
func (c *Client) Patch(ctx context.Context, u string, body, out any) error {
	r, ct, err := jsonBody(body)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPatch, u, r, ct, out, requestTimeout)
}

// Delete issues a DELETE.
func (c *Client) Delete(ctx context.Context, u string) error {
	return c.do(ctx, http.MethodDelete, u, nil, "", nil, requestTimeout)
}

// PostRaw sends an arbitrary body (e.g. base64 MIME) with the given
// content type and length.
func (c *Client) PostRaw(ctx context.Context, u string, body func() (io.Reader, error), contentType string, length int64) error {
	return c.request(ctx, http.MethodPost, u, body, contentType, length, nil, bodyTimeout, nil)
}

// GetRaw streams a non-JSON response ($value); the caller must close it.
// The download is bounded by the context and the client's timeout.
func (c *Client) GetRaw(ctx context.Context, u string) (io.ReadCloser, error) {
	var rc io.ReadCloser
	err := c.request(ctx, http.MethodGet, u, nil, "", 0, nil, bodyTimeout, func(resp *http.Response) error {
		rc = resp.Body
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rc, nil
}

func jsonBody(v any) (func() (io.Reader, error), string, error) {
	if v == nil {
		return nil, "", nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, "", fmt.Errorf("graph: encode request: %w", err)
	}
	return func() (io.Reader, error) { return bytes.NewReader(b), nil }, "application/json", nil
}

func (c *Client) do(ctx context.Context, method, u string, body func() (io.Reader, error), contentType string, out any, timeout time.Duration) error {
	return c.request(ctx, method, u, body, contentType, -1, out, timeout, nil)
}

// request runs one call with the retry policy. keep, when set, takes over
// the response body (streaming); otherwise the body is decoded into out
// and closed here.
func (c *Client) request(ctx context.Context, method, u string, body func() (io.Reader, error), contentType string, length int64, out any, timeout time.Duration, keep func(*http.Response) error) error {
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-c.sem }()

	refreshed := false
	throttled := 0
	for {
		token, err := c.token(ctx)
		if err != nil {
			return err
		}
		resp, err := c.once(ctx, method, u, body, contentType, length, token, timeout)
		if err != nil {
			return err
		}
		switch {
		case resp.StatusCode == http.StatusUnauthorized && !refreshed:
			drain(resp)
			refreshed = true
			c.log.Debug("token rejected, asking the source again")
			c.invalidate()
			continue
		case (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable) && throttled < throttleRetries:
			se := statusError(resp)
			throttled++
			wait := se.RetryAfter
			if wait <= 0 {
				wait = 5 * time.Second
			}
			if wait > retryAfterMax {
				return se
			}
			c.log.Debug("throttled by graph", "status", resp.StatusCode, "retryAfter", wait)
			if err := c.sleep(ctx, wait); err != nil {
				return err
			}
			continue
		case resp.StatusCode < 200 || resp.StatusCode >= 300:
			se := statusError(resp)
			// A service echoing the token must not put it into logs.
			se.Message = strings.ReplaceAll(se.Message, token, "***")
			return se
		}
		if keep != nil {
			return keep(resp)
		}
		defer resp.Body.Close()
		if out == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBody))
			return nil
		}
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return api.NewError(api.CodeServerError, "graph: malformed response: %s", transport.CleanMessage(err.Error()))
		}
		return nil
	}
}

func (c *Client) once(ctx context.Context, method, u string, body func() (io.Reader, error), contentType string, length int64, token string, timeout time.Duration) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		var err error
		if r, err = body(); err != nil {
			return nil, err
		}
	}
	rctx, cancel := context.WithTimeout(ctx, timeout)
	req, err := http.NewRequestWithContext(rctx, method, c.URL(u), r)
	if err != nil {
		cancel()
		return nil, api.NewError(api.CodeInvalidArgument, "graph: bad request: %s", transport.CleanMessage(err.Error()))
	}
	if length >= 0 && r != nil {
		req.ContentLength = length
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Prefer", preferHeader)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		cancel()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, classifyTransport(err)
	}
	// The body outlives this call; cancel when it is closed.
	resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
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

// statusError reads the service's error envelope ({"error":{"code","message"}}).
func statusError(resp *http.Response) *StatusError {
	defer resp.Body.Close()
	se := &StatusError{Status: resp.StatusCode}
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(ra)); err == nil && secs >= 0 {
			se.RetryAfter = time.Duration(secs) * time.Second
		}
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &env) == nil && (env.Error.Code != "" || env.Error.Message != "") {
		se.Code = transport.CleanMessage(env.Error.Code)
		se.Message = transport.CleanMessage(env.Error.Message)
		return se
	}
	se.Message = transport.CleanMessage(http.StatusText(resp.StatusCode))
	return se
}

// classifyTransport maps a transport failure: timeouts are serverTimeout,
// everything else networkError. The token never appears in these texts.
func classifyTransport(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return api.NewError(api.CodeServerTimeout, "graph: request timed out")
	}
	return api.NewError(api.CodeNetworkError, "graph: %s", transport.CleanMessage(err.Error()))
}
