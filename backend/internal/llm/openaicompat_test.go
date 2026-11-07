// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package llm_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/internal/llm"
	"github.com/schotek/malachi/backend/internal/llm/llmtest"
	"github.com/schotek/malachi/backend/pkg/api"
)

func TestCompatText(t *testing.T) {
	srv := llmtest.New(t, llmtest.Compat, llmtest.Turn{Text: "Hello, world!", Usage: usage})
	srv.Key = testKey
	p := newProvider(t, srv, testKey)
	events, err := run(t, p, llm.Request{Model: "llama3.3", System: "Be brief.", Messages: history(), Tools: tools()})
	want := *usage
	want.CacheCreationInputTokens = 0
	textTurnChecks(t, events, err, "Hello, world!", want)

	r := srv.Requests()[0]
	if r.Method != "POST" || r.Path != "/v1/chat/completions" || !r.HasKey {
		t.Errorf("request = %+v", r)
	}
	b := r.Body
	if field(t, b, "model") != "llama3.3" || field(t, b, "stream") != true || field(t, b, "stream_options.include_usage") != true || field(t, b, "max_tokens") != float64(llm.DefaultMaxOutputTokens) {
		t.Errorf("body = %v", b)
	}
	if hasField(b, "response_format") || hasField(b, "instructions") || hasField(b, "store") || hasField(b, "input") {
		t.Errorf("unexpected fields in %v", b)
	}
	if field(t, b, "messages.0.role") != "system" || field(t, b, "messages.0.content") != "Be brief." {
		t.Errorf("messages[0] = %v", field(t, b, "messages.0"))
	}
	if field(t, b, "messages.1.role") != "user" || field(t, b, "messages.1.content") != "Summarise <b>this</b>." {
		t.Errorf("messages[1] = %v", field(t, b, "messages.1"))
	}
	if field(t, b, "messages.2.role") != "assistant" || field(t, b, "messages.2.content") != "Reading it." || field(t, b, "messages.2.tool_calls.0.id") != "call_1" || field(t, b, "messages.2.tool_calls.0.type") != "function" || field(t, b, "messages.2.tool_calls.0.function.name") != "read_message" || field(t, b, "messages.2.tool_calls.0.function.arguments") != `{"id":"m1"}` {
		t.Errorf("messages[2] = %v", field(t, b, "messages.2"))
	}
	if field(t, b, "messages.3.role") != "tool" || field(t, b, "messages.3.tool_call_id") != "call_1" || field(t, b, "messages.3.content") != "ERROR: the mail" {
		t.Errorf("messages[3] = %v", field(t, b, "messages.3"))
	}
	if field(t, b, "tools.0.type") != "function" || field(t, b, "tools.0.function.name") != "read_message" || field(t, b, "tools.0.function.description") != "Reads a message" || field(t, b, "tools.0.function.parameters.type") != "object" {
		t.Errorf("tools = %v", field(t, b, "tools"))
	}
}

func TestCompatToolCalls(t *testing.T) {
	srv := llmtest.New(t, llmtest.Compat, toolTurn())
	p := newProvider(t, srv, testKey)
	events, err := run(t, p, llm.Request{Model: "m", Messages: history()[:1], Tools: tools()})
	toolTurnChecks(t, events, err)
}

func TestCompatNoKeyNoAuthorization(t *testing.T) {
	srv := llmtest.New(t, llmtest.Compat, llmtest.Turn{Text: "ok"})
	p := newProvider(t, srv, "")
	if _, err := run(t, p, llm.Request{Model: "m", Messages: history()[:1]}); err != nil {
		t.Fatal(err)
	}
	if r := srv.Requests()[0]; r.HasKey {
		t.Errorf("Authorization sent without a key: %+v", r)
	}
}

func TestCompatSchemaAndFallback(t *testing.T) {
	srv := llmtest.New(t, llmtest.Compat,
		llmtest.Turn{Status: 400, Body: `{"error":{"message":"response_format value 'json_schema' is not supported","type":"invalid_request_error"}}`},
		llmtest.Turn{Text: `{"q":"x"}`})
	p := newProvider(t, srv, testKey)
	events, err := run(t, p, llm.Request{Model: "m", System: "Search.", Messages: history()[:1], JSONSchema: schema})
	if err != nil {
		t.Fatal(err)
	}
	if d := find(events, llm.EventTextDone); len(d) != 1 || d[0].Text != `{"q":"x"}` {
		t.Errorf("events = %+v", events)
	}
	reqs := srv.Requests()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d", len(reqs))
	}
	first := reqs[0].Body
	if field(t, first, "response_format.type") != "json_schema" || field(t, first, "response_format.json_schema.name") != "answer" || field(t, first, "response_format.json_schema.strict") != true || field(t, first, "response_format.json_schema.schema.type") != "object" || field(t, first, "messages.0.content") != "Search." {
		t.Errorf("first body = %v", first)
	}
	second := reqs[1].Body
	if hasField(second, "response_format") {
		t.Errorf("second body still has response_format: %v", second)
	}
	sys, _ := field(t, second, "messages.0.content").(string)
	if !strings.HasPrefix(sys, "Search.\n\nAnswer with exactly one JSON object") || !strings.HasSuffix(sys, string(schema)) {
		t.Errorf("second system = %q", sys)
	}
	// Any other 400 is not retried.
	srv2 := llmtest.New(t, llmtest.Compat, llmtest.Turn{Status: 400}, llmtest.Turn{Text: "never"})
	p2 := newProvider(t, srv2, testKey)
	_, err = run(t, p2, llm.Request{Model: "m", Messages: history()[:1], JSONSchema: schema})
	wantClass(t, err, api.FailureInvalidRequest)
	if srv2.Calls() != 1 {
		t.Errorf("calls = %d", srv2.Calls())
	}
}

func TestCompatPlainJSONAnswer(t *testing.T) {
	srv := llmtest.New(t, llmtest.Compat,
		llmtest.Turn{Text: "Hello, world!", Usage: usage, NoStream: true},
		llmtest.Turn{Calls: toolTurn().Calls, NoStream: true})
	p := newProvider(t, srv, testKey)
	events, err := run(t, p, llm.Request{Model: "m", Messages: history()[:1]})
	want := *usage
	want.CacheCreationInputTokens = 0
	textTurnChecks(t, events, err, "Hello, world!", want)
	events, err = run(t, p, llm.Request{Model: "m", Messages: history()[:1], Tools: tools()})
	if err != nil {
		t.Fatal(err)
	}
	calls := find(events, llm.EventToolCall)
	if len(calls) != 2 || calls[0].Call.ID != "call_1" || string(calls[0].Call.Input) != `{"id":"m1"}` || calls[1].Call.Input != nil || calls[1].Text != `{"account":` {
		t.Errorf("calls = %+v", calls)
	}
	if l := last(events); l.Kind != llm.EventStop || l.Stop != llm.StopToolUse {
		t.Errorf("last = %+v", l)
	}
}

func TestCompatErrors(t *testing.T) {
	cases := []struct {
		name  string
		turn  llmtest.Turn
		class api.AssistantFailureClass
		msg   string
	}{
		{"401", llmtest.Turn{Status: 401}, api.FailureAuth, "scripted error"},
		{"403", llmtest.Turn{Status: 403}, api.FailurePermission, "scripted error"},
		{"429", llmtest.Turn{Status: 429, RetryAfter: "60"}, api.FailureRateLimit, "scripted error"},
		{"503", llmtest.Turn{Status: 503, RetryAfter: "60"}, api.FailureOverloaded, "scripted error"},
		{"400 ollama tools", llmtest.Turn{Status: 400, Body: `{"error":{"message":"registry.ollama.ai/library/llama2:latest does not support tools","type":"api_error","param":null,"code":null}}`}, api.FailureToolsUnsupported, "registry.ollama.ai/library/llama2:latest does not support tools"},
		{"400 lm studio tools", llmtest.Turn{Status: 400, Body: `{"error":"This model does not support tool calling"}`}, api.FailureToolsUnsupported, "This model does not support tool calling"},
		{"404 ollama model", llmtest.Turn{Status: 404, Body: `{"error":{"message":"model \"nope\" not found, try pulling it first","type":"api_error","param":null,"code":null}}`}, api.FailureInvalidRequest, `model "nope" not found, try pulling it first`},
		{"404 html", llmtest.Turn{Status: 404, Body: `<html>Not Found</html>`}, api.FailureInvalidRequest, "HTTP 404"},
		{"429 quota", llmtest.Turn{Status: 429, Body: `{"error":{"message":"You exceeded your current quota, please check your plan and billing details.","type":"insufficient_quota","code":"insufficient_quota"}}`}, api.FailureQuota, "You exceeded your current quota, please check your plan and billing details."},
		{"500", llmtest.Turn{Status: 500}, api.FailureProtocol, "scripted error"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := llmtest.New(t, llmtest.Compat, c.turn)
			p := newProvider(t, srv, testKey)
			_, err := run(t, p, llm.Request{Model: "m", Messages: history()[:1]})
			e := wantClass(t, err, c.class)
			if e.Status != c.turn.Status || e.Message != c.msg {
				t.Errorf("error = %+v", e)
			}
		})
	}
}

func TestCompatEOFWithoutDone(t *testing.T) {
	// A server that streams chunks and closes without [DONE] or usage.
	srv := newRawServer(t, "text/event-stream",
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hel\"},\"finish_reason\":null}]}\n\n"+
			"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"lo\"},\"finish_reason\":null}]}\n\n"+
			"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
	p, err := llm.New(llm.Config{Provider: api.AIOpenAICompatible, BaseURL: srv.URL + "/v1", UserAgent: "t"})
	if err != nil {
		t.Fatal(err)
	}
	events, err := run(t, p, llm.Request{Model: "m", Messages: history()[:1]})
	if err != nil {
		t.Fatal(err)
	}
	if deltaText(events) != "Hello" || len(find(events, llm.EventUsage)) != 0 {
		t.Errorf("events = %+v", events)
	}
	if l := last(events); l.Kind != llm.EventStop || l.Stop != llm.StopEndTurn {
		t.Errorf("last = %+v", l)
	}
}

func TestCompatStreamedError(t *testing.T) {
	srv := newRawServer(t, "text/event-stream", "data: {\"error\":{\"message\":\"model does not support tools\",\"type\":\"invalid_request_error\"}}\n\n")
	p, err := llm.New(llm.Config{Provider: api.AIOpenAICompatible, BaseURL: srv.URL + "/v1", UserAgent: "t"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = run(t, p, llm.Request{Model: "m", Messages: history()[:1]})
	if e := wantClass(t, err, api.FailureToolsUnsupported); e.Code != "invalid_request_error" {
		t.Errorf("code %q", e.Code)
	}
	// A gateway relaying an account without credit inside the stream.
	srv = newRawServer(t, "text/event-stream", "data: {\"error\":{\"message\":\"You have no credits remaining. Add credits to continue.\",\"type\":\"invalid_request_error\"}}\n\n")
	p, err = llm.New(llm.Config{Provider: api.AIOpenAICompatible, BaseURL: srv.URL + "/v1", UserAgent: "t"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = run(t, p, llm.Request{Model: "m", Messages: history()[:1]})
	wantClass(t, err, api.FailureQuota)
}

func TestCompatModels(t *testing.T) {
	srv := llmtest.New(t, llmtest.Compat)
	srv.Models = []string{"llama3.3", "qwen3:8b"}
	p := newProvider(t, srv, "")
	models, err := p.Models(context.Background())
	if err != nil || len(models) != 2 || models[1].ID != "qwen3:8b" {
		t.Fatalf("models = %+v, %v", models, err)
	}
	if r := srv.Requests()[0]; r.HasKey || r.Path != "/v1/models" {
		t.Errorf("request = %+v", r)
	}
	// Something that is not a listing is a protocol error.
	html := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>welcome</html>"))
	}))
	t.Cleanup(html.Close)
	p, err = llm.New(llm.Config{Provider: api.AIOpenAICompatible, BaseURL: html.URL + "/v1", UserAgent: "t"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Models(context.Background())
	wantClass(t, err, api.FailureProtocol)
}
