// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package chatgpt

import (
	"bufio"
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/schotek/malachi/ui/internal/assistant"
)

const frameLimit = 16 << 20

// inferenceGate gives Codex only an opaque localhost credential. The native
// application alone attaches renewable OpenAI credentials to the fixed target.
type inferenceGate struct {
	tokens                    TokenSource
	tools                     map[string]bool
	credential, baseURL, path string
	client                    *http.Client
	server                    *http.Server
	listener                  net.Listener
	ctx                       context.Context
	cancel                    context.CancelFunc
	mu                        sync.Mutex
	lastFailure               string
	usage                     func(assistant.Event)
	done                      chan struct{}
	slots                     chan struct{}
}

func newInferenceGate(tokens TokenSource, tools map[string]bool, client *http.Client) (*inferenceGate, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, errors.New("codex_gate_unavailable")
	}
	if client == nil {
		client = &http.Client{}
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	ctx, cancel := context.WithCancel(tokens.SessionContext())
	g := &inferenceGate{tokens: tokens, tools: tools, credential: randomValue(), path: "/" + randomValue() + "/v1/responses", client: &copyClient, listener: listener, ctx: ctx, cancel: cancel, done: make(chan struct{}), slots: make(chan struct{}, 4)}
	g.baseURL = "http://" + listener.Addr().String() + strings.TrimSuffix(g.path, "/responses")
	g.server = &http.Server{Handler: g, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, MaxHeaderBytes: 32 << 10, BaseContext: func(net.Listener) context.Context { return ctx }}
	go func() { defer close(g.done); _ = g.server.Serve(listener) }()
	go func() { <-ctx.Done(); _ = g.server.Close() }()
	return g, nil
}
func (g *inferenceGate) failure(code string) { g.mu.Lock(); g.lastFailure = code; g.mu.Unlock() }

// newTurn forgets the failure of an earlier turn, so a turn that fails for
// another reason is not reported with it.
func (g *inferenceGate) newTurn()        { g.failure("") }
func (g *inferenceGate) Failure() string { g.mu.Lock(); defer g.mu.Unlock(); return g.lastFailure }
func (g *inferenceGate) Close()          { g.cancel(); _ = g.server.Close(); <-g.done }
func rawObject(b []byte) (map[string]json.RawMessage, error) {
	var value map[string]json.RawMessage
	if !uniqueJSON(b) {
		return nil, errors.New("codex_invalid_json")
	}
	err := json.Unmarshal(b, &value)
	if err != nil || value == nil {
		return nil, errors.New("codex_invalid_json")
	}
	return value, nil
}
func (g *inferenceGate) filterRequest(body []byte) ([]byte, error) {
	root, err := rawObject(body)
	if err != nil {
		return nil, err
	}
	for _, key := range []string{"previous_response_id", "conversation"} {
		if _, ok := root[key]; ok {
			return nil, errors.New("codex_persistent_upstream_history_denied")
		}
	}
	// Responses Lite places the tool catalog in developer input items. Convert
	// those declarations to the same filtered Responses catalog as older Codex.
	var catalog []map[string]json.RawMessage
	_ = json.Unmarshal(root["tools"], &catalog)
	var input []json.RawMessage
	if json.Unmarshal(root["input"], &input) == nil {
		kept := []json.RawMessage{}
		for _, raw := range input {
			item, err := rawObject(raw)
			if err != nil || str(item, "type") != "additional_tools" {
				kept = append(kept, raw)
				continue
			}
			var added []map[string]json.RawMessage
			if json.Unmarshal(item["tools"], &added) != nil || added == nil {
				return nil, errors.New("codex_invalid_tool_catalog")
			}
			catalog = append(catalog, added...)
		}
		root["input"], _ = json.Marshal(kept)
	}
	accepted := []map[string]json.RawMessage{}
	seen := map[string]bool{}
	for _, entry := range catalog {
		if str(entry, "type") != "namespace" || str(entry, "name") != "malachi" {
			continue
		}
		var nested []map[string]json.RawMessage
		if json.Unmarshal(entry["tools"], &nested) != nil {
			return nil, errors.New("codex_invalid_tool_catalog")
		}
		functions := []map[string]json.RawMessage{}
		for _, tool := range nested {
			name := str(tool, "name")
			if str(tool, "type") == "function" && g.tools[name] {
				if seen[name] {
					return nil, errors.New("codex_duplicate_tool")
				}
				seen[name] = true
				functions = append(functions, tool)
			}
		}
		if len(functions) > 0 {
			b, _ := json.Marshal(functions)
			accepted = append(accepted, map[string]json.RawMessage{"type": json.RawMessage(`"namespace"`), "name": json.RawMessage(`"malachi"`), "description": json.RawMessage(`"Malachi Mail tools"`), "tools": b})
		}
	}
	if len(seen) != len(g.tools) {
		return nil, errors.New("codex_required_tools_missing")
	}
	root["tools"], _ = json.Marshal(accepted)
	root["store"] = json.RawMessage("false")
	root["stream"] = json.RawMessage("true")
	root["parallel_tool_calls"] = json.RawMessage("false")
	delete(root, "tool_choice")
	return json.Marshal(root)
}
func (g *inferenceGate) validateEvent(data string) error {
	if data == "[DONE]" {
		return nil
	}
	root, err := rawObject([]byte(data))
	if err != nil {
		return err
	}
	if item, ok := root["item"]; ok {
		if err = g.validateItem(item); err != nil {
			return err
		}
	}
	if response, ok := root["response"]; ok {
		r, err := rawObject(response)
		if err != nil {
			return err
		}
		if output, ok := r["output"]; ok {
			var items []json.RawMessage
			if json.Unmarshal(output, &items) != nil {
				return errors.New("codex_invalid_response_output")
			}
			for _, item := range items {
				if err = g.validateItem(item); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func (g *inferenceGate) validateItem(body json.RawMessage) error {
	item, err := rawObject(body)
	if err != nil {
		return err
	}
	switch str(item, "type") {
	case "message", "reasoning", "compaction":
		return nil
	case "function_call":
		if str(item, "namespace") == "malachi" && g.tools[str(item, "name")] {
			return nil
		}
	}
	return errors.New("codex_disallowed_inference_tool")
}
func (g *inferenceGate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" || r.URL.Path != g.path || r.URL.RawQuery != "" || r.Host != g.listener.Addr().String() || len(r.TransferEncoding) > 0 || r.ContentLength < 1 || r.ContentLength > frameLimit || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+g.credential)) != 1 {
		g.failure("codex_gate_request_denied")
		http.Error(w, "request denied", 403)
		return
	}
	select {
	case g.slots <- struct{}{}:
		defer func() { <-g.slots }()
	default:
		http.Error(w, "busy", 503)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	b, err := io.ReadAll(io.LimitReader(r.Body, frameLimit+1))
	if err != nil || len(b) > frameLimit {
		g.failure("codex_gate_request_limit")
		return
	}
	filtered, err := g.filterRequest(b)
	if err != nil {
		g.failure(err.Error())
		http.Error(w, "policy denied", 403)
		return
	}
	token, err := g.tokens.GetAccessToken(ctx)
	if err != nil {
		g.failure("chatgpt_reconnect_required")
		http.Error(w, "credential unavailable", 401)
		return
	}
	request, err := http.NewRequestWithContext(ctx, "POST", Resource+"/responses", bytes.NewReader(filtered))
	if err != nil {
		return
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	response, err := g.client.Do(request)
	if err != nil {
		g.failure("codex_gateway_failed")
		return
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		code := "chatgpt_inference_refused"
		switch response.StatusCode {
		case 401:
			code = "chatgpt_reconnect_required"
		case 403:
			code = "chatgpt_permission_denied"
		case 429:
			code = "chatgpt_usage_limit"
		}
		g.failure(code)
		if observer, ok := g.tokens.(InferenceObserver); ok {
			_ = observer.InferenceRejected(ctx, token, response.StatusCode)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write([]byte(`{"error":{"message":"ChatGPT inference refused"}}`))
		return
	}
	contentType := strings.TrimSpace(response.Header.Get("Content-Type"))
	untyped := contentType == ""
	if !untyped {
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || mediaType != "text/event-stream" {
			g.failure("codex_gate_non_streaming_response")
			http.Error(w, "invalid inference stream", http.StatusBadGateway)
			return
		}
	}
	started := false
	reject := func(code string) {
		g.failure(code)
		if !started {
			http.Error(w, "invalid inference stream", http.StatusBadGateway)
		}
	}
	scan := bufio.NewScanner(response.Body)
	scan.Buffer(make([]byte, 8192), frameLimit)
	lines := []string{}
	size := 0
	for scan.Scan() {
		line := scan.Text()
		if !utf8.ValidString(line) {
			reject("codex_gate_invalid_utf8")
			return
		}
		size += len(line)
		if size > frameLimit {
			reject("codex_gate_response_limit")
			return
		}
		if line != "" {
			lines = append(lines, line)
			continue
		}
		data := []string{}
		for _, l := range lines {
			if strings.HasPrefix(l, "data:") {
				data = append(data, strings.TrimPrefix(strings.TrimPrefix(l, "data:"), " "))
			}
		}
		if len(data) > 0 {
			payload := strings.Join(data, "\n")
			if !started && untyped {
				// Some SIWC responses omit Content-Type. Require an actual
				// Responses event before presenting this body as SSE to Codex.
				root, parseErr := rawObject([]byte(payload))
				if parseErr != nil || !strings.HasPrefix(str(root, "type"), "response.") {
					reject("codex_gate_non_streaming_response")
					return
				}
			}
			if err = g.validateEvent(payload); err != nil {
				reject(err.Error())
				return
			}
			g.reportUsage(payload)
		}
		if !started {
			if len(data) == 0 {
				for _, line := range lines {
					if !strings.HasPrefix(line, ":") {
						reject("codex_gate_non_streaming_response")
						return
					}
				}
				lines = nil
				size = 0
				continue
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(200)
			started = true
		}
		if _, err = fmt.Fprint(w, strings.Join(lines, "\n")+"\n\n"); err != nil {
			return
		}
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		lines = nil
		size = 0
	}
	if !started {
		reject("codex_gate_non_streaming_response")
	} else if scan.Err() != nil || len(lines) > 0 {
		reject("codex_gate_truncated_event")
	}
}

// Usage comes from validated Responses completion frames, keyed by response ID.
// Codex billing does not expose a USD cost; shared tallies record actual tokens.
func (g *inferenceGate) reportUsage(data string) {
	root, err := rawObject([]byte(data))
	if err != nil || str(root, "type") != "response.completed" {
		return
	}
	response, err := rawObject(root["response"])
	if err != nil || str(response, "id") == "" {
		return
	}
	raw, err := rawObject(response["usage"])
	if err != nil {
		return
	}
	u := assistant.Usage{}
	for key, out := range map[string]*int64{"input_tokens": &u.InputTokens, "output_tokens": &u.OutputTokens} {
		n, ok := number(raw, key)
		if !ok || n < 0 || n > assistant.MaxUsageTokens {
			return
		}
		*out = n
	}
	if details, err := rawObject(raw["input_tokens_details"]); err == nil {
		if n, ok := number(details, "cached_tokens"); ok && n >= 0 && n <= u.InputTokens {
			u.CacheReadInputTokens = n
			u.InputTokens -= n
		}
	}
	g.mu.Lock()
	fn := g.usage
	g.mu.Unlock()
	if fn != nil {
		fn(assistant.Event{Kind: assistant.EventOther, MessageID: str(response, "id"), Usage: &u, UsageFinal: true})
	}
}
