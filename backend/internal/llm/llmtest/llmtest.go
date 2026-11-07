// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package llmtest is a fake model API for tests: an httptest server
// speaking one of the three dialects internal/llm calls — the Anthropic
// Messages API, the OpenAI Responses API or the OpenAI-compatible chat
// completions interface — with the real wire shapes of each (streamed
// events, error bodies, GET /models), scripted turn by turn. No network.
package llmtest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/schotek/malachi/backend/internal/llm"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Dialect is which API the fake speaks.
type Dialect int

const (
	Anthropic Dialect = iota + 1
	OpenAI
	Compat
)

// Call is a tool call the scripted answer makes; Input is the arguments'
// JSON text, sent as it is (an invalid text tests the client's refusal).
type Call struct {
	ID, Name, Input string
}

// Turn scripts the answer of the n-th model call: Text streamed in a few
// deltas, then Calls, then Usage (nil = none sent) and Stop (empty =
// toolUse with calls, endTurn without). Status, when set, is an HTTP
// error instead, with Body (empty = the dialect's error JSON with a
// scripted message) and the Retry-After header. Truncate closes the
// connection after the first events; NoStream (Compat) answers a plain
// JSON completion.
type Turn struct {
	Text       string
	Calls      []Call
	Usage      *api.AssistantUsage
	Stop       llm.StopReason
	Status     int
	Body       string
	RetryAfter string
	Truncate   bool
	NoStream   bool
}

// Captured is one request the fake received: the method and path, the
// headers without the key, whether the key header was present, and the
// decoded JSON body (nil for GET).
type Captured struct {
	Method string
	Path   string
	Header http.Header
	HasKey bool
	Body   map[string]any
}

// Server is the fake. Models and Key may be set before the first
// request.
type Server struct {
	*httptest.Server
	Dialect Dialect
	// Models is what GET …/models lists.
	Models []string
	// PageSize is the Anthropic listing's page (0 = 100).
	PageSize int
	// Key, when set, is the only key accepted: another or none is 401.
	Key string

	mu       sync.Mutex
	turns    []Turn
	calls    int
	requests []Captured
}

// New starts the fake of dialect with the scripted turns; it stops with
// t.
func New(t testing.TB, dialect Dialect, script ...Turn) *Server {
	t.Helper()
	s := &Server{Dialect: dialect, turns: script}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// URL is the base URL in the dialect's form: the root for Anthropic,
// root + "/v1" for OpenAI and a compatible server.
func (s *Server) URL() string {
	if s.Dialect == Anthropic {
		return s.Server.URL
	}
	return s.Server.URL + "/v1"
}

// Requests returns a copy of the requests received so far.
func (s *Server) Requests() []Captured {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Captured(nil), s.requests...)
}

// Calls is how many model calls (POSTs) were answered.
func (s *Server) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	cap := Captured{Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone()}
	if r.URL.RawQuery != "" {
		cap.Path += "?" + r.URL.RawQuery
	}
	key := s.keyOf(r)
	cap.HasKey = key != ""
	cap.Header.Del("X-Api-Key")
	cap.Header.Del("Authorization")
	if len(body) > 0 {
		_ = json.Unmarshal(body, &cap.Body)
	}
	s.mu.Lock()
	s.requests = append(s.requests, cap)
	s.mu.Unlock()

	if s.Key != "" && key != s.Key {
		s.writeError(w, http.StatusUnauthorized, "", "invalid x-api-key / Incorrect API key provided")
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
		s.serveModels(w, r)
	case r.Method == http.MethodPost && r.URL.Path == s.postPath():
		s.serveTurn(w)
	default:
		s.writeError(w, http.StatusNotFound, "", "not found")
	}
}

func (s *Server) postPath() string {
	switch s.Dialect {
	case Anthropic:
		return "/v1/messages"
	case OpenAI:
		return "/v1/responses"
	}
	return "/v1/chat/completions"
}

func (s *Server) keyOf(r *http.Request) string {
	if s.Dialect == Anthropic {
		return r.Header.Get("x-api-key")
	}
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}

// writeError writes the dialect's error JSON.
func (s *Server) writeError(w http.ResponseWriter, status int, retryAfter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	if retryAfter != "" {
		w.Header().Set("Retry-After", retryAfter)
	}
	w.WriteHeader(status)
	_, _ = w.Write([]byte(s.errorJSON(status, msg)))
}

func (s *Server) errorJSON(status int, msg string) string {
	m, _ := json.Marshal(msg)
	if s.Dialect == Anthropic {
		typ := map[int]string{400: "invalid_request_error", 401: "authentication_error", 403: "permission_error", 404: "not_found_error", 429: "rate_limit_error", 529: "overloaded_error", 500: "api_error"}[status]
		if typ == "" {
			typ = "api_error"
		}
		return fmt.Sprintf(`{"type":"error","error":{"type":%q,"message":%s}}`, typ, m)
	}
	typ := map[int]string{400: "invalid_request_error", 401: "invalid_request_error", 403: "permission_error", 404: "invalid_request_error", 429: "rate_limit_error", 503: "server_error", 500: "server_error"}[status]
	if typ == "" {
		typ = "server_error"
	}
	code := "null"
	if status == 401 {
		code = `"invalid_api_key"`
	}
	return fmt.Sprintf(`{"error":{"message":%s,"type":%q,"param":null,"code":%s}}`, m, typ, code)
}

func (s *Server) serveModels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.Dialect != Anthropic {
		items := make([]map[string]any, 0, len(s.Models))
		for _, id := range s.Models {
			items = append(items, map[string]any{"id": id, "object": "model", "owned_by": "fake"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": items})
		return
	}
	page := s.PageSize
	if page <= 0 {
		page = 100
	}
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n < page {
		page = n
	}
	start := 0
	if after := r.URL.Query().Get("after_id"); after != "" {
		for i, id := range s.Models {
			if id == after {
				start = i + 1
			}
		}
	}
	end := min(start+page, len(s.Models))
	items := make([]map[string]any, 0, end-start)
	for _, id := range s.Models[start:end] {
		items = append(items, map[string]any{"type": "model", "id": id, "display_name": "Model " + id, "created_at": "2026-01-01T00:00:00Z"})
	}
	out := map[string]any{"data": items, "has_more": end < len(s.Models)}
	if len(items) > 0 {
		out["first_id"] = items[0]["id"]
		out["last_id"] = items[len(items)-1]["id"]
	} else {
		out["first_id"], out["last_id"] = nil, nil
	}
	_ = json.NewEncoder(w).Encode(out)
}

// serveTurn answers the next scripted turn.
func (s *Server) serveTurn(w http.ResponseWriter) {
	s.mu.Lock()
	n := s.calls
	s.calls++
	s.mu.Unlock()
	if n >= len(s.turns) {
		s.writeError(w, http.StatusInternalServerError, "", "no scripted turn")
		return
	}
	t := s.turns[n]
	if t.Status != 0 {
		if t.Body != "" {
			w.Header().Set("Content-Type", "application/json")
			if t.RetryAfter != "" {
				w.Header().Set("Retry-After", t.RetryAfter)
			}
			w.WriteHeader(t.Status)
			_, _ = w.Write([]byte(t.Body))
			return
		}
		s.writeError(w, t.Status, t.RetryAfter, "scripted error")
		return
	}
	stop := t.Stop
	if stop == "" {
		stop = llm.StopEndTurn
		if len(t.Calls) > 0 {
			stop = llm.StopToolUse
		}
	}
	if t.NoStream && s.Dialect == Compat {
		s.plainCompat(w, t, stop)
		return
	}
	sw := &streamWriter{w: w, f: w.(http.Flusher)}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	switch s.Dialect {
	case Anthropic:
		s.streamAnthropic(sw, t, stop)
	case OpenAI:
		s.streamOpenAI(sw, t, stop)
	default:
		s.streamCompat(sw, t, stop)
	}
	if t.Truncate {
		sw.f.Flush()
		panic(http.ErrAbortHandler)
	}
}

// streamWriter writes server-sent events and stops writing at the
// truncation point.
type streamWriter struct {
	w      http.ResponseWriter
	f      http.Flusher
	n      int
	deltas int // text and argument deltas written
}

func (sw *streamWriter) event(name string, data any) {
	b, _ := json.Marshal(data)
	if name != "" {
		fmt.Fprintf(sw.w, "event: %s\n", name)
	}
	fmt.Fprintf(sw.w, "data: %s\n\n", b)
	sw.f.Flush()
	sw.n++
}

func (sw *streamWriter) raw(line string) {
	fmt.Fprint(sw.w, line)
	sw.f.Flush()
	sw.n++
}

// deltas splits text into a few pieces.
func deltas(text string) []string {
	if text == "" {
		return nil
	}
	r := []rune(text)
	if len(r) < 3 {
		return []string{text}
	}
	a, b := len(r)/3, 2*len(r)/3
	return []string{string(r[:a]), string(r[a:b]), string(r[b:])}
}

// cut reports that a truncated stream has written its first delta and
// the connection is to drop now.
func (sw *streamWriter) cut(t Turn) bool { return t.Truncate && sw.deltas >= 1 }

func usageOf(t Turn) api.AssistantUsage {
	if t.Usage != nil {
		return *t.Usage
	}
	return api.AssistantUsage{}
}

func (s *Server) streamAnthropic(sw *streamWriter, t Turn, stop llm.StopReason) {
	u := usageOf(t)
	sw.event("message_start", map[string]any{"type": "message_start", "message": map[string]any{
		"id": "msg_fake", "type": "message", "role": "assistant", "model": "fake", "content": []any{}, "stop_reason": nil,
		"usage": map[string]any{"input_tokens": u.InputTokens, "output_tokens": 1, "cache_creation_input_tokens": u.CacheCreationInputTokens, "cache_read_input_tokens": u.CacheReadInputTokens},
	}})
	sw.event("ping", map[string]any{"type": "ping"})
	index := 0
	if t.Text != "" {
		sw.event("content_block_start", map[string]any{"type": "content_block_start", "index": index, "content_block": map[string]any{"type": "text", "text": ""}})
		for _, d := range deltas(t.Text) {
			if sw.cut(t) {
				return
			}
			sw.event("content_block_delta", map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "text_delta", "text": d}})
			sw.deltas++
		}
		sw.event("content_block_stop", map[string]any{"type": "content_block_stop", "index": index})
		index++
	}
	for _, c := range t.Calls {
		if sw.cut(t) {
			return
		}
		sw.event("content_block_start", map[string]any{"type": "content_block_start", "index": index, "content_block": map[string]any{"type": "tool_use", "id": c.ID, "name": c.Name, "input": map[string]any{}}})
		for _, d := range deltas(c.Input) {
			sw.event("content_block_delta", map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "input_json_delta", "partial_json": d}})
			sw.deltas++
		}
		sw.event("content_block_stop", map[string]any{"type": "content_block_stop", "index": index})
		index++
	}
	if sw.cut(t) {
		return
	}
	reason := map[llm.StopReason]string{llm.StopEndTurn: "end_turn", llm.StopToolUse: "tool_use", llm.StopMaxTokens: "max_tokens", llm.StopRefusal: "refusal"}[stop]
	if reason == "" {
		reason = "pause_turn"
	}
	if t.Usage != nil {
		sw.event("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": reason, "stop_sequence": nil}, "usage": map[string]any{"output_tokens": u.OutputTokens}})
	} else {
		sw.event("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": reason, "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 0}})
	}
	sw.event("message_stop", map[string]any{"type": "message_stop"})
}

func (s *Server) streamOpenAI(sw *streamWriter, t Turn, stop llm.StopReason) {
	u := usageOf(t)
	sw.event("response.created", map[string]any{"type": "response.created", "sequence_number": 0, "response": map[string]any{"id": "resp_fake", "object": "response", "status": "in_progress"}})
	sw.event("response.in_progress", map[string]any{"type": "response.in_progress", "sequence_number": 1, "response": map[string]any{"id": "resp_fake", "object": "response", "status": "in_progress"}})
	index := 0
	if t.Text != "" {
		sw.event("response.output_item.added", map[string]any{"type": "response.output_item.added", "output_index": index, "item": map[string]any{"id": "msg_fake", "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}})
		sw.event("response.content_part.added", map[string]any{"type": "response.content_part.added", "item_id": "msg_fake", "output_index": index, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}})
		for _, d := range deltas(t.Text) {
			if sw.cut(t) {
				return
			}
			sw.event("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "item_id": "msg_fake", "output_index": index, "content_index": 0, "delta": d})
			sw.deltas++
		}
		sw.event("response.output_text.done", map[string]any{"type": "response.output_text.done", "item_id": "msg_fake", "output_index": index, "content_index": 0, "text": t.Text})
		sw.event("response.content_part.done", map[string]any{"type": "response.content_part.done", "item_id": "msg_fake", "output_index": index, "content_index": 0, "part": map[string]any{"type": "output_text", "text": t.Text, "annotations": []any{}}})
		sw.event("response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": index, "item": map[string]any{"id": "msg_fake", "type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": t.Text, "annotations": []any{}}}}})
		index++
	}
	for i, c := range t.Calls {
		if sw.cut(t) {
			return
		}
		itemID := fmt.Sprintf("fc_%d", i+1)
		sw.event("response.output_item.added", map[string]any{"type": "response.output_item.added", "output_index": index, "item": map[string]any{"id": itemID, "type": "function_call", "call_id": c.ID, "name": c.Name, "arguments": "", "status": "in_progress"}})
		for _, d := range deltas(c.Input) {
			sw.event("response.function_call_arguments.delta", map[string]any{"type": "response.function_call_arguments.delta", "item_id": itemID, "output_index": index, "delta": d})
			sw.deltas++
		}
		sw.event("response.function_call_arguments.done", map[string]any{"type": "response.function_call_arguments.done", "item_id": itemID, "output_index": index, "arguments": c.Input})
		sw.event("response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": index, "item": map[string]any{"id": itemID, "type": "function_call", "call_id": c.ID, "name": c.Name, "arguments": c.Input, "status": "completed"}})
		index++
	}
	if sw.cut(t) {
		return
	}
	resp := map[string]any{
		"id": "resp_fake", "object": "response", "status": "completed", "incomplete_details": nil,
		"usage": map[string]any{
			"input_tokens": u.InputTokens + u.CacheReadInputTokens, "input_tokens_details": map[string]any{"cached_tokens": u.CacheReadInputTokens},
			"output_tokens": u.OutputTokens, "output_tokens_details": map[string]any{"reasoning_tokens": 0}, "total_tokens": u.InputTokens + u.CacheReadInputTokens + u.OutputTokens,
		},
	}
	switch stop {
	case llm.StopMaxTokens:
		resp["status"] = "incomplete"
		resp["incomplete_details"] = map[string]any{"reason": "max_output_tokens"}
		sw.event("response.incomplete", map[string]any{"type": "response.incomplete", "response": resp})
	case llm.StopRefusal:
		resp["status"] = "incomplete"
		resp["incomplete_details"] = map[string]any{"reason": "content_filter"}
		sw.event("response.incomplete", map[string]any{"type": "response.incomplete", "response": resp})
	default:
		sw.event("response.completed", map[string]any{"type": "response.completed", "response": resp})
	}
}

func compatFinish(stop llm.StopReason, calls int) string {
	switch stop {
	case llm.StopMaxTokens:
		return "length"
	case llm.StopRefusal:
		return "content_filter"
	case llm.StopToolUse:
		return "tool_calls"
	}
	if calls > 0 {
		return "tool_calls"
	}
	return "stop"
}

func compatChunk(delta map[string]any, finish any) map[string]any {
	return map[string]any{"id": "chatcmpl-fake", "object": "chat.completion.chunk", "created": 1, "model": "fake",
		"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
}

func compatUsage(u api.AssistantUsage) map[string]any {
	return map[string]any{"prompt_tokens": u.InputTokens + u.CacheReadInputTokens, "completion_tokens": u.OutputTokens,
		"total_tokens": u.InputTokens + u.CacheReadInputTokens + u.OutputTokens, "prompt_tokens_details": map[string]any{"cached_tokens": u.CacheReadInputTokens}}
}

func (s *Server) streamCompat(sw *streamWriter, t Turn, stop llm.StopReason) {
	sw.event("", compatChunk(map[string]any{"role": "assistant", "content": ""}, nil))
	for _, d := range deltas(t.Text) {
		if sw.cut(t) {
			return
		}
		sw.event("", compatChunk(map[string]any{"content": d}, nil))
		sw.deltas++
	}
	for i, c := range t.Calls {
		if sw.cut(t) {
			return
		}
		sw.event("", compatChunk(map[string]any{"tool_calls": []any{map[string]any{"index": i, "id": c.ID, "type": "function", "function": map[string]any{"name": c.Name, "arguments": ""}}}}, nil))
		for _, d := range deltas(c.Input) {
			sw.event("", compatChunk(map[string]any{"tool_calls": []any{map[string]any{"index": i, "function": map[string]any{"arguments": d}}}}, nil))
			sw.deltas++
		}
	}
	if sw.cut(t) {
		return
	}
	sw.event("", compatChunk(map[string]any{}, compatFinish(stop, len(t.Calls))))
	if t.Usage != nil {
		sw.event("", map[string]any{"id": "chatcmpl-fake", "object": "chat.completion.chunk", "created": 1, "model": "fake", "choices": []any{}, "usage": compatUsage(*t.Usage)})
	}
	sw.raw("data: [DONE]\n\n")
}

func (s *Server) plainCompat(w http.ResponseWriter, t Turn, stop llm.StopReason) {
	msg := map[string]any{"role": "assistant", "content": t.Text}
	if len(t.Calls) > 0 {
		calls := make([]any, 0, len(t.Calls))
		for _, c := range t.Calls {
			calls = append(calls, map[string]any{"id": c.ID, "type": "function", "function": map[string]any{"name": c.Name, "arguments": c.Input}})
		}
		msg["tool_calls"] = calls
		if t.Text == "" {
			msg["content"] = nil
		}
	}
	out := map[string]any{"id": "chatcmpl-fake", "object": "chat.completion", "created": 1, "model": "fake",
		"choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": compatFinish(stop, len(t.Calls))}}}
	if t.Usage != nil {
		out["usage"] = compatUsage(*t.Usage)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
