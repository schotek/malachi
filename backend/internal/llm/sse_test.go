// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

type sseEvent struct{ event, data string }

func readAll(t *testing.T, s string) ([]sseEvent, error) {
	t.Helper()
	var out []sseEvent
	err := readSSE(strings.NewReader(s), func(event, data string) error {
		out = append(out, sseEvent{event, data})
		return nil
	})
	return out, err
}

func TestSSEReader(t *testing.T) {
	stream := ": a comment\n" +
		"event: message_start\n" +
		"data: {\"a\":1}\n" +
		"\n" +
		"id: 7\n" +
		"retry: 100\n" +
		"data: first\n" +
		"data:second\n" +
		"data: \n" +
		"\n" +
		"event: crlf\r\n" +
		"data: with\r\n" +
		"\r\n" +
		"unknownfield: ignored\n" +
		"data: unterminated"
	got, err := readAll(t, stream)
	if err != nil {
		t.Fatal(err)
	}
	want := []sseEvent{
		{"message_start", `{"a":1}`},
		{"", "first\nsecond\n"},
		{"crlf", "with"},
		{"", "unterminated"},
	}
	if len(got) != len(want) {
		t.Fatalf("events = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestSSEReaderEmptyEventsAndCallbackError(t *testing.T) {
	got, err := readAll(t, "event: ping\n\n\n\ndata: x\n\n")
	if err != nil || len(got) != 1 || got[0] != (sseEvent{"", "x"}) {
		t.Fatalf("got %+v, %v", got, err)
	}
	stop := errors.New("stop")
	n := 0
	err = readSSE(strings.NewReader("data: 1\n\ndata: 2\n\n"), func(string, string) error {
		n++
		return stop
	})
	if !errors.Is(err, stop) || n != 1 {
		t.Fatalf("callback error: n=%d err=%v", n, err)
	}
}

func TestSSEReaderLimits(t *testing.T) {
	long := "data: " + strings.Repeat("a", MaxLineBytes+10) + "\n\n"
	_, err := readAll(t, long)
	var e *Error
	if !errors.As(err, &e) || e.Class != api.FailureProtocol {
		t.Fatalf("over-long line: %v", err)
	}
	// Many lines under the line limit, over the event limit.
	var b strings.Builder
	line := "data: " + strings.Repeat("b", MaxLineBytes/2) + "\n"
	for b.Len() < MaxEventBytes+MaxLineBytes {
		b.WriteString(line)
	}
	b.WriteString("\n")
	_, err = readAll(t, b.String())
	if !errors.As(err, &e) || e.Class != api.FailureProtocol {
		t.Fatalf("over-long event: %v", err)
	}
}

func TestSSEReaderReadError(t *testing.T) {
	r := io.MultiReader(strings.NewReader("data: a\n\ndata: b"), failReader{})
	var got []sseEvent
	err := readSSE(r, func(event, data string) error {
		got = append(got, sseEvent{event, data})
		return nil
	})
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v", err)
	}
	if len(got) != 1 || got[0].data != "a" {
		t.Fatalf("events before the failure = %+v", got)
	}
}

type failReader struct{}

func (failReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

// endless repeats a chunk for ever.
type endless struct {
	chunk []byte
	off   int
}

func (e *endless) Read(p []byte) (int, error) {
	n := copy(p, e.chunk[e.off:])
	e.off = (e.off + n) % len(e.chunk)
	return n, nil
}

func TestSSEReaderWholeStreamBound(t *testing.T) {
	pings := 0
	err := readSSE(&endless{chunk: []byte("event: ping\ndata: {\"type\":\"ping\"}\n\n")}, func(string, string) error {
		pings++
		return nil
	})
	var e *Error
	if !errors.As(err, &e) || e.Class != api.FailureProtocol {
		t.Fatalf("endless pings: %v", err)
	}
	if pings < MaxStreamBytes/64 || pings > MaxStreamBytes/16 {
		t.Errorf("pings = %d", pings)
	}
}

func TestCleanText(t *testing.T) {
	cases := []struct{ in, want string }{
		{" plain ", "plain"},
		{"tab\there\x00x", "tab here x"},
		{"bad\xffutf8", "badutf8"},
		{strings.Repeat("é", 150), strings.Repeat("é", 100)},
	}
	for _, c := range cases {
		if got := cleanText(c.in, 200); got != c.want {
			t.Errorf("cleanText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if m, ok := cleanModel("  ", "x"); ok || m.ID != "" {
		t.Errorf("empty id kept: %+v", m)
	}
	m, ok := cleanModel("claude-opus-5-5", "Claude\nOpus")
	if !ok || m.ID != "claude-opus-5-5" || m.DisplayName != "Claude Opus" {
		t.Errorf("cleanModel = %+v", m)
	}
}

func TestRetryDelay(t *testing.T) {
	ctx := context.Background()
	if d, ok := retryDelay(ctx, ""); !ok || d != defaultRetryAfter {
		t.Errorf("no header: %v %v", d, ok)
	}
	if d, ok := retryDelay(ctx, "0"); !ok || d != 0 {
		t.Errorf("zero: %v %v", d, ok)
	}
	if d, ok := retryDelay(ctx, "garbage"); !ok || d != defaultRetryAfter {
		t.Errorf("unreadable header: %v %v", d, ok)
	}
	if _, ok := retryDelay(ctx, "31"); ok {
		t.Error("beyond MaxRetryAfter retried")
	}
	date := time.Now().Add(3 * time.Second).UTC().Format(http.TimeFormat)
	if d, ok := retryDelay(ctx, date); !ok || d < 2*time.Second || d > 4*time.Second {
		t.Errorf("date: %v %v", d, ok)
	}
	past := time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)
	if d, ok := retryDelay(ctx, past); !ok || d != 0 {
		t.Errorf("past date: %v %v", d, ok)
	}
	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, ok := retryDelay(short, "1"); ok {
		t.Error("retried past the deadline")
	}
}

func TestProviderError(t *testing.T) {
	cases := []struct {
		body     string
		typ, msg string
	}{
		{`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, "overloaded_error", "Overloaded"},
		{`{"error":{"message":"Incorrect API key","type":"invalid_request_error","code":"invalid_api_key"}}`, "invalid_request_error", "Incorrect API key"},
		{`{"error":{"message":"no key","code":"invalid_api_key"}}`, "invalid_api_key", "no key"},
		{`{"error":"model 'x' not found"}`, "", "model 'x' not found"},
		{`{"message":"plain"}`, "", "plain"},
		{`{"detail":"Not Found"}`, "", "Not Found"},
		{`{"detail":[{"msg":"field required"}]}`, "", "field required"},
		{`<html>`, "", ""},
	}
	for _, c := range cases {
		typ, msg := providerError([]byte(c.body))
		if typ != c.typ || msg != c.msg {
			t.Errorf("providerError(%s) = %q, %q; want %q, %q", c.body, typ, msg, c.typ, c.msg)
		}
	}
}

func TestMentionsToolsUnsupported(t *testing.T) {
	yes := []string{
		"registry.ollama.ai/library/llama2:latest does not support tools",
		"This model does not support tool calling",
		"Function calling is not supported by this model",
		`"auto" tool choice requires --enable-auto-tool-choice and --tool-call-parser to be set`,
	}
	for _, m := range yes {
		if !mentionsToolsUnsupported(m) {
			t.Errorf("%q not recognised", m)
		}
	}
	no := []string{"invalid tools[0].name", "max_tokens is too large", "model not found"}
	for _, m := range no {
		if mentionsToolsUnsupported(m) {
			t.Errorf("%q recognised", m)
		}
	}
}
