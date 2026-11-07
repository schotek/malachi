// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

// maxMessageBytes bounds the provider's error message kept in an
// Error.Message; maxModelsBytes one listing of models (a gateway's list
// runs to megabytes of descriptions).
const (
	maxMessageBytes = 200
	maxModelsBytes  = 8 << 20
	// defaultRetryAfter is waited when a 429, 503 or 529 names no
	// Retry-After.
	defaultRetryAfter = time.Second
	// maxModelPages bounds Models' pagination (Anthropic).
	maxModelPages = 5
)

// New returns the Provider of cfg: the Anthropic Messages API, the OpenAI
// Responses API or the OpenAI-compatible chat completions interface,
// by cfg.Provider. An unknown provider, an empty or malformed BaseURL,
// or one carrying user info, is an error. cfg.Client replaces the
// package's own HTTP client (tests); redirects are never followed through
// either.
func New(cfg Config) (Provider, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New("llm: empty base URL")
	}
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("llm: base URL is not an http(s) URL")
	}
	if u.User != nil {
		return nil, errors.New("llm: base URL must not carry user info")
	}
	c := &client{cfg: cfg, base: strings.TrimRight(cfg.BaseURL, "/")}
	if cfg.Client != nil {
		hc := *cfg.Client
		hc.CheckRedirect = noRedirect
		c.http = &hc
	} else {
		c.http = newHTTPClient(cfg, u)
	}
	switch cfg.Provider {
	case api.AIAnthropic:
		return &anthropic{c}, nil
	case api.AIOpenAI:
		return &openai{c}, nil
	case api.AIOpenAICompatible:
		return &openaiCompat{c}, nil
	}
	return nil, fmt.Errorf("llm: unknown provider %q", string(cfg.Provider))
}

// noRedirect is the http.Client's CheckRedirect: a 3xx answer is returned
// as it is and mapped to a protocol error, never followed (the key would
// otherwise travel to wherever the provider points).
func noRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

// newHTTPClient is the package's own client: the daemon's outbound HTTP
// rules (no redirects, proxy from the environment, timeouts of one
// call) and the TLS policy of BaseURL's host — the system roots, or the
// pinned certificate of cfg.CertificateSHA256 over https. Every request
// of a Provider goes to that host (redirects are not followed), so the
// one transport carries the pin for that host only; plain http is the
// user's choice and goes through as it is.
func newHTTPClient(cfg Config, base *url.URL) *http.Client {
	pin := ""
	if base.Scheme == "https" {
		pin = cfg.CertificateSHA256
	}
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: DialTimeout}).DialContext,
		TLSClientConfig:       transport.EndpointTLSConfig(api.ServerConfig{Host: strings.ToLower(base.Hostname()), CertificateSHA256: pin}),
		TLSHandshakeTimeout:   TLSHandshakeTimeout,
		ResponseHeaderTimeout: HeaderTimeout,
		ForceAttemptHTTP2:     true,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
	}
	return &http.Client{Transport: tr, CheckRedirect: noRedirect}
}

// client is what the three dialects share: the configuration, the base
// URL without a trailing slash and the HTTP client.
type client struct {
	cfg  Config
	base string
	http *http.Client
}

// connError is a failed connection: the *Error (FailureNetwork) that
// ClassOf reports, and the transport's classification (tlsError with the
// certificate's data, networkError, serverTimeout) that ToAPIError passes
// through for account.test.
type connError struct {
	err   *Error
	cause *api.Error
}

func (e *connError) Error() string   { return e.err.Error() }
func (e *connError) Unwrap() []error { return []error{e.err, e.cause} }

// encodeJSON marshals v without HTML escaping (the prompts carry mail).
func encodeJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, &Error{Class: api.FailureInvalidRequest, Message: "request could not be encoded"}
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// newRequest builds one request of the dialect: method, the path under
// the base URL, the JSON body (nil for GET), the Accept header and the
// headers the dialect adds (its auth header among them).
func (c *client) newRequest(ctx context.Context, method, path string, body []byte, accept string, headers map[string]string) (*http.Request, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return nil, &Error{Class: api.FailureInvalidRequest, Message: "request could not be built"}
	}
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	req.Header.Set("Accept", accept)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req, nil
}

// send performs one request, retrying once after a 429, 503 or 529
// (Retry-After, defaultRetryAfter without one, never beyond
// MaxRetryAfter and never past ctx's deadline). build makes a fresh
// request for each attempt (the body is re-sent). A transport failure is
// a *connError (ctx.Err() when ctx ended); an HTTP error status is an
// *Error through the dialect's classification; the response of a 2xx is
// returned with its body open.
func (c *client) send(ctx context.Context, build func() (*http.Request, error), classify func(*http.Response, []byte) *Error) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		req, err := build()
		if err != nil {
			return nil, err
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, c.transportError(ctx, err)
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, MaxErrorBytes))
		resp.Body.Close()
		e := classify(resp, body)
		if attempt == 0 && retryable(resp.StatusCode) {
			if wait, ok := retryDelay(ctx, resp.Header.Get("Retry-After")); ok {
				if err := sleep(ctx, wait); err != nil {
					return nil, err
				}
				continue
			}
		}
		return nil, e
	}
}

// retryable: the statuses the one retry applies to.
func retryable(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable || status == 529
}

// retryDelay is how long the one retry waits: the Retry-After header
// (seconds or an HTTP date), defaultRetryAfter without a readable one;
// false when that is more than MaxRetryAfter or more than ctx has left.
func retryDelay(ctx context.Context, header string) (time.Duration, bool) {
	wait := defaultRetryAfter
	if secs, ok := parseRetryAfter(header); ok {
		wait = time.Duration(secs) * time.Second
	}
	if wait > MaxRetryAfter {
		return 0, false
	}
	if dl, ok := ctx.Deadline(); ok && time.Until(dl) < wait {
		return 0, false
	}
	return wait, true
}

// parseRetryAfter reads a Retry-After header as whole seconds (a date is
// converted; a past date is 0).
func parseRetryAfter(header string) (int, bool) {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0, false
	}
	if n, err := strconv.Atoi(header); err == nil {
		if n < 0 {
			return 0, false
		}
		return n, true
	}
	t, err := http.ParseTime(header)
	if err != nil {
		return 0, false
	}
	d := time.Until(t)
	if d < 0 {
		return 0, true
	}
	return int((d + time.Second - 1) / time.Second), true
}

// retryAfterOf is the header's value for Error.RetryAfter (0 when absent
// or unreadable).
func retryAfterOf(resp *http.Response) int {
	n, _ := parseRetryAfter(resp.Header.Get("Retry-After"))
	return n
}

// sleep waits d or until ctx ends.
func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// transportError is a failed request before any response: ctx's own
// error when ctx ended, else a *connError (network) whose message
// carries the transport's error without the request URL.
func (c *client) transportError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return &connError{
		err:   &Error{Class: api.FailureNetwork, Message: cleanMessage(err.Error())},
		cause: transport.Classify(ctx, transport.StageDial, err),
	}
}

// readError maps the error of reading a response: a *readFailed is
// ctx's error when ctx ended, else a network *Error; anything else (a
// protocol *Error, emit's own error) is returned as it is.
func readError(ctx context.Context, err error) error {
	var rf *readFailed
	if !errors.As(err, &rf) {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(rf.err, context.Canceled) || errors.Is(rf.err, context.DeadlineExceeded) {
		return rf.err
	}
	return &Error{Class: api.FailureNetwork, Message: "the connection was lost: " + cleanMessage(rf.err.Error())}
}

// httpError classifies an HTTP error status the way every dialect
// shares; typ and msg are the provider's own error type and message read
// from the body (either may be empty). The message never carries more
// than the provider's cleaned message.
func httpError(resp *http.Response, typ, msg string) *Error {
	e := &Error{Status: resp.StatusCode, Message: cleanMessage(msg)}
	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		e.Class = api.FailureProtocol
		e.Message = "the provider redirected the request; redirects are not followed"
	case resp.StatusCode == http.StatusUnauthorized:
		e.Class = api.FailureAuth
	case resp.StatusCode == http.StatusForbidden:
		e.Class = api.FailurePermission
	case resp.StatusCode == http.StatusTooManyRequests:
		e.Class = api.FailureRateLimit
		e.RetryAfter = retryAfterOf(resp)
	case resp.StatusCode == 529, resp.StatusCode == http.StatusServiceUnavailable:
		e.Class = api.FailureOverloaded
		e.RetryAfter = retryAfterOf(resp)
	case resp.StatusCode == http.StatusBadRequest && mentionsToolsUnsupported(msg):
		e.Class = api.FailureToolsUnsupported
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		e.Class = api.FailureInvalidRequest
	default:
		e.Class = api.FailureProtocol
	}
	if e.Message == "" {
		e.Message = "HTTP " + strconv.Itoa(resp.StatusCode)
		if typ != "" {
			e.Message += " " + cleanMessage(typ)
		}
	}
	return e
}

// mentionsToolsUnsupported recognises the 400 of a server whose model
// cannot call tools (Ollama "… does not support tools", LM Studio
// "… does not support tool calling", vLLM "… tool choice requires
// --enable-auto-tool-choice …").
func mentionsToolsUnsupported(msg string) bool {
	m := strings.ToLower(msg)
	if !strings.Contains(m, "tool") && !strings.Contains(m, "function call") {
		return false
	}
	return strings.Contains(m, "not support") || strings.Contains(m, "unsupported") || strings.Contains(m, "requires --")
}

// providerError reads the error type and message of a JSON error body in
// the shapes the three dialects and the common servers use:
// {"error":{"type","code","message"}} (Anthropic, OpenAI, llama.cpp,
// vLLM), {"error":"…"} (Ollama), {"message":"…"}, {"detail":"…"} and
// {"detail":[{"msg":"…"}]} (FastAPI).
func providerError(body []byte) (typ, msg string) {
	var v struct {
		Type    string          `json:"type"`
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
		Detail  json.RawMessage `json:"detail"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return "", ""
	}
	if len(v.Error) > 0 {
		var obj struct {
			Type    string `json:"type"`
			Code    any    `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(v.Error, &obj) == nil && (obj.Message != "" || obj.Type != "") {
			typ = obj.Type
			if typ == "" {
				if s, ok := obj.Code.(string); ok {
					typ = s
				}
			}
			return typ, obj.Message
		}
		var s string
		if json.Unmarshal(v.Error, &s) == nil {
			return v.Type, s
		}
	}
	if v.Message != "" {
		return v.Type, v.Message
	}
	if len(v.Detail) > 0 {
		var s string
		if json.Unmarshal(v.Detail, &s) == nil {
			return v.Type, s
		}
		var items []struct {
			Msg string `json:"msg"`
		}
		if json.Unmarshal(v.Detail, &items) == nil && len(items) > 0 {
			return v.Type, items[0].Msg
		}
	}
	return v.Type, ""
}

// mediaType is the response's media type ("" when unreadable).
func mediaType(resp *http.Response) string {
	mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil {
		return ""
	}
	return mt
}

// isJSON reports an application/json (or +json) body.
func isJSON(resp *http.Response) bool {
	mt := mediaType(resp)
	return mt == "application/json" || strings.HasSuffix(mt, "+json")
}

// cleanText makes s valid UTF-8 without control characters (each
// becomes a space), trimmed and at most max bytes, cut at a rune.
func cleanText(s string, max int) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if len(s) > max {
		s = s[:max]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
		s = strings.TrimSpace(s)
	}
	return s
}

// cleanMessage bounds a provider's error message for an Error.Message.
func cleanMessage(s string) string {
	return cleanText(s, maxMessageBytes)
}

// cleanModel cleans a listed model: the id and display name valid UTF-8
// without control characters and at most MaxModelIDBytes; false for an
// id that is empty after cleaning.
func cleanModel(id, name string) (api.AIModel, bool) {
	id = cleanText(id, MaxModelIDBytes)
	if id == "" {
		return api.AIModel{}, false
	}
	return api.AIModel{ID: id, DisplayName: cleanText(name, MaxModelIDBytes)}, true
}

// maxOutputTokens is Request.MaxOutputTokens or the default.
func maxOutputTokens(req Request) int {
	if req.MaxOutputTokens <= 0 {
		return DefaultMaxOutputTokens
	}
	return req.MaxOutputTokens
}

// schemaInstruction is the system prompt's instruction that asks for a
// JSON answer where the provider has no structured output the package
// uses (Anthropic; the compatible server that refused response_format);
// the caller validates the answer.
func schemaInstruction(system string, schema json.RawMessage) string {
	s := "Answer with exactly one JSON object that matches this JSON schema and nothing else:\n" + string(bytes.TrimSpace(schema))
	if system == "" {
		return s
	}
	return system + "\n\n" + s
}

// schemaOr is schema, or def for an empty one.
func schemaOr(schema json.RawMessage, def string) json.RawMessage {
	if len(bytes.TrimSpace(schema)) == 0 {
		return json.RawMessage(def)
	}
	return schema
}

// parseToolInput validates the arguments a model assembled: valid JSON
// (an empty text is the empty object) is the call's Input; anything else
// is reported with Input nil and the raw text for the caller to refuse.
func parseToolInput(id, name, args string) Event {
	args = strings.TrimSpace(args)
	if args == "" {
		args = "{}"
	}
	call := &ToolCall{ID: id, Name: name}
	if json.Valid([]byte(args)) {
		call.Input = json.RawMessage(args)
		return Event{Kind: EventToolCall, Call: call}
	}
	return Event{Kind: EventToolCall, Call: call, Text: args}
}

// streamEnded is the error of a stream that ended before the dialect's
// terminal event.
func streamEnded() *Error {
	return &Error{Class: api.FailureProtocol, Message: "the stream ended before the answer was complete"}
}

// protocolError is a malformed answer.
func protocolError(what string) *Error {
	return &Error{Class: api.FailureProtocol, Message: what}
}

// withModelsTimeout bounds a whole listing of models.
func withModelsTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, ModelsTimeout)
}

// boundedBody reads at most max bytes of a response body: more is a
// protocol error (the body is read with one byte of slack to tell).
func boundedBody(r io.Reader, max int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, &readFailed{err}
	}
	if int64(len(b)) > max {
		return nil, protocolError("the answer is longer than the package reads")
	}
	return b, nil
}
