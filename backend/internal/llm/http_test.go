// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package llm_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/llm"
	"github.com/schotek/malachi/backend/internal/llm/llmtest"
	"github.com/schotek/malachi/backend/pkg/api"
)

// newRawServer answers every request with status 200, the content type
// and body given.
func newRawServer(t *testing.T, contentType, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestNew(t *testing.T) {
	bad := []llm.Config{
		{Provider: api.AIAnthropic},
		{Provider: api.AIAnthropic, BaseURL: "ftp://x"},
		{Provider: api.AIAnthropic, BaseURL: "https://user:pw@api.example.com"},
		{Provider: api.AIProvider("gemini"), BaseURL: "https://api.example.com"},
		{Provider: "", BaseURL: "https://api.example.com"},
	}
	for _, c := range bad {
		if p, err := llm.New(c); err == nil {
			t.Errorf("New(%+v) = %T, want an error", c, p)
		}
	}
	for _, c := range []llm.Config{
		{Provider: api.AIAnthropic, BaseURL: api.AnthropicBaseURL},
		{Provider: api.AIOpenAI, BaseURL: api.OpenAIBaseURL},
		{Provider: api.AIOpenAICompatible, BaseURL: "http://localhost:11434/v1/", CertificateSHA256: strings.Repeat("ab", 32)},
		{Provider: api.AIOpenAICompatible, BaseURL: "https://gw.example.com/v1", CertificateSHA256: strings.Repeat("ab", 32)},
	} {
		if _, err := llm.New(c); err != nil {
			t.Errorf("New(%+v): %v", c, err)
		}
	}
}

func TestRedirectIsNotFollowed(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	t.Cleanup(target.Close)
	for _, client := range []*http.Client{nil, {Timeout: 5 * time.Second}} {
		redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL+r.URL.Path, http.StatusFound)
		}))
		t.Cleanup(redirect.Close)
		for _, prov := range []api.AIProvider{api.AIAnthropic, api.AIOpenAI, api.AIOpenAICompatible} {
			p, err := llm.New(llm.Config{Provider: prov, BaseURL: redirect.URL + "/v1", Key: testKey, UserAgent: "t", Client: client})
			if err != nil {
				t.Fatal(err)
			}
			_, err = run(t, p, llm.Request{Model: "m", Messages: history()[:1]})
			e := wantClass(t, err, api.FailureProtocol)
			if e.Status != http.StatusFound {
				t.Errorf("%s: error = %+v", prov, e)
			}
			if _, err := p.Models(context.Background()); llm.ClassOf(err) != api.FailureProtocol {
				t.Errorf("%s Models: %v", prov, err)
			}
		}
	}
	if hits.Load() != 0 {
		t.Errorf("the redirect target was hit %d times", hits.Load())
	}
}

func TestRetryOnce(t *testing.T) {
	for _, d := range []llmtest.Dialect{llmtest.Anthropic, llmtest.OpenAI, llmtest.Compat} {
		for _, status := range []int{429, 503, 529} {
			srv := llmtest.New(t, d, llmtest.Turn{Status: status, RetryAfter: "0"}, llmtest.Turn{Text: "after the retry"})
			p := newProvider(t, srv, testKey)
			events, err := run(t, p, llm.Request{Model: "m", Messages: history()[:1]})
			if err != nil {
				t.Fatalf("%v/%d: %v", d, status, err)
			}
			if deltaText(events) != "after the retry" || srv.Calls() != 2 {
				t.Errorf("%v/%d: text %q, calls %d", d, status, deltaText(events), srv.Calls())
			}
		}
	}
	// Only once: two 429s are the error, with the second answer's Retry-After.
	srv := llmtest.New(t, llmtest.Compat, llmtest.Turn{Status: 429, RetryAfter: "0"}, llmtest.Turn{Status: 429, RetryAfter: "0"}, llmtest.Turn{Text: "never"})
	p := newProvider(t, srv, testKey)
	_, err := run(t, p, llm.Request{Model: "m", Messages: history()[:1]})
	e := wantClass(t, err, api.FailureRateLimit)
	if e.RetryAfter != 0 || srv.Calls() != 2 {
		t.Errorf("error = %+v, calls = %d", e, srv.Calls())
	}
	// Not when the context has less time than the wait.
	srv = llmtest.New(t, llmtest.Compat, llmtest.Turn{Status: 429, RetryAfter: "2"}, llmtest.Turn{Text: "never"})
	p = newProvider(t, srv, testKey)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err = p.Stream(ctx, llm.Request{Model: "m", Messages: history()[:1]}, func(llm.Event) error { return nil })
	e = wantClass(t, err, api.FailureRateLimit)
	if e.RetryAfter != 2 || srv.Calls() != 1 {
		t.Errorf("error = %+v, calls = %d", e, srv.Calls())
	}
	// Models retries too.
	srv = llmtest.New(t, llmtest.OpenAI)
	srv.Models = []string{"m"}
	calls := 0
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"m"}]}`))
	}))
	t.Cleanup(proxy.Close)
	p, err = llm.New(llm.Config{Provider: api.AIOpenAI, BaseURL: proxy.URL + "/v1", Key: testKey, UserAgent: "t"})
	if err != nil {
		t.Fatal(err)
	}
	models, err := p.Models(context.Background())
	if err != nil || len(models) != 1 || calls != 2 {
		t.Errorf("models = %+v, %v, calls %d", models, err, calls)
	}
}

func TestTruncatedStream(t *testing.T) {
	for _, d := range []llmtest.Dialect{llmtest.Anthropic, llmtest.OpenAI, llmtest.Compat} {
		srv := llmtest.New(t, d, llmtest.Turn{Text: "A long enough answer to be cut in the middle.", Truncate: true})
		p := newProvider(t, srv, testKey)
		events, err := run(t, p, llm.Request{Model: "m", Messages: history()[:1]})
		if err == nil {
			t.Fatalf("%v: no error after a truncated stream (%v)", d, kinds(events))
		}
		var e *llm.Error
		if !errors.As(err, &e) || (e.Class != api.FailureNetwork && e.Class != api.FailureProtocol) {
			t.Errorf("%v: err = %v", d, err)
		}
		if len(find(events, llm.EventTextDelta)) == 0 {
			t.Errorf("%v: no events before the cut: %v", d, kinds(events))
		}
		if len(find(events, llm.EventStop)) != 0 {
			t.Errorf("%v: stop after a truncated stream", d)
		}
	}
}

func TestStreamEndsEarly(t *testing.T) {
	// A clean end without the terminal event is a protocol error for
	// the two named providers.
	cases := map[api.AIProvider]string{
		api.AIAnthropic: "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":1}}}\n\n",
		api.AIOpenAI:    "event: response.created\ndata: {\"type\":\"response.created\"}\n\n",
	}
	for prov, body := range cases {
		srv := newRawServer(t, "text/event-stream", body)
		p, err := llm.New(llm.Config{Provider: prov, BaseURL: srv.URL + "/v1", Key: testKey, UserAgent: "t"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = run(t, p, llm.Request{Model: "m", Messages: history()[:1]})
		wantClass(t, err, api.FailureProtocol)
	}
}

func TestOverLongLineIsAProtocolError(t *testing.T) {
	for _, prov := range []api.AIProvider{api.AIAnthropic, api.AIOpenAI, api.AIOpenAICompatible} {
		srv := newRawServer(t, "text/event-stream", "data: "+strings.Repeat("x", llm.MaxLineBytes+1)+"\n\n")
		p, err := llm.New(llm.Config{Provider: prov, BaseURL: srv.URL + "/v1", Key: testKey, UserAgent: "t"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = run(t, p, llm.Request{Model: "m", Messages: history()[:1]})
		wantClass(t, err, api.FailureProtocol)
	}
}

func TestNotAnEventStream(t *testing.T) {
	srv := newRawServer(t, "text/html", "<html>hi</html>")
	for _, prov := range []api.AIProvider{api.AIAnthropic, api.AIOpenAI, api.AIOpenAICompatible} {
		p, err := llm.New(llm.Config{Provider: prov, BaseURL: srv.URL + "/v1", Key: testKey, UserAgent: "t"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = run(t, p, llm.Request{Model: "m", Messages: history()[:1]})
		wantClass(t, err, api.FailureProtocol)
	}
}

func TestEmitErrorStopsTheStream(t *testing.T) {
	srv := llmtest.New(t, llmtest.Anthropic, llmtest.Turn{Text: "Hello, world!"})
	p := newProvider(t, srv, testKey)
	refused := errors.New("refused")
	n := 0
	err := p.Stream(context.Background(), llm.Request{Model: "m", Messages: history()[:1]}, func(llm.Event) error {
		n++
		return refused
	})
	if !errors.Is(err, refused) || n != 1 {
		t.Errorf("err = %v, n = %d", err, n)
	}
}

func TestCancelledContext(t *testing.T) {
	srv := llmtest.New(t, llmtest.Compat, llmtest.Turn{Text: "Hello"})
	p := newProvider(t, srv, testKey)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := p.Stream(ctx, llm.Request{Model: "m", Messages: history()[:1]}, func(llm.Event) error { return nil })
	if !errors.Is(err, context.Canceled) || llm.ClassOf(err) != api.FailureStopped {
		t.Errorf("err = %v", err)
	}
}

func TestConnectionRefused(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close()
	p, err := llm.New(llm.Config{Provider: api.AIOpenAICompatible, BaseURL: url + "/v1", UserAgent: "t"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = run(t, p, llm.Request{Model: "m", Messages: history()[:1]})
	e := wantClass(t, err, api.FailureNetwork)
	if ae := llm.ToAPIError(err); ae.Code != api.CodeNetworkError {
		t.Errorf("ToAPIError = %+v", ae)
	}
	if strings.Contains(e.Message, "http://") || strings.Contains(e.Message, "/v1/") {
		t.Errorf("the URL is in the message: %q", e.Message)
	}
}

func TestCertificatePin(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"m"}]}`))
	}))
	t.Cleanup(srv.Close)
	sum := sha256.Sum256(srv.Certificate().Raw)
	pin := hex.EncodeToString(sum[:])
	wrong := strings.Repeat("00", 32)

	// The pinned certificate is accepted although nothing trusts it.
	p, err := llm.New(llm.Config{Provider: api.AIOpenAICompatible, BaseURL: srv.URL + "/v1", UserAgent: "t", CertificateSHA256: pin})
	if err != nil {
		t.Fatal(err)
	}
	if models, err := p.Models(context.Background()); err != nil || len(models) != 1 {
		t.Fatalf("pinned: %+v, %v", models, err)
	}
	// Another pin, or no pin, fails the handshake: a network failure whose
	// api error is tlsError.
	for _, pin := range []string{wrong, ""} {
		p, err := llm.New(llm.Config{Provider: api.AIOpenAICompatible, BaseURL: srv.URL + "/v1", UserAgent: "t", CertificateSHA256: pin})
		if err != nil {
			t.Fatal(err)
		}
		_, err = p.Models(context.Background())
		wantClass(t, err, api.FailureNetwork)
		if ae := llm.ToAPIError(err); ae.Code != api.CodeTLSError {
			t.Errorf("pin %q: ToAPIError = %+v", pin, ae)
		}
	}
}
