// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package llm_test

import (
	"context"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/internal/llm"
	"github.com/schotek/malachi/backend/internal/llm/llmtest"
	"github.com/schotek/malachi/backend/pkg/api"
)

func TestAnthropicText(t *testing.T) {
	srv := llmtest.New(t, llmtest.Anthropic, llmtest.Turn{Text: "Hello, world!", Usage: usage})
	srv.Key = testKey
	p := newProvider(t, srv, testKey)
	events, err := run(t, p, llm.Request{Model: "claude-opus-5-5", System: "Be brief.", Messages: history(), Tools: tools()})
	textTurnChecks(t, events, err, "Hello, world!", *usage)

	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d", len(reqs))
	}
	r := reqs[0]
	if r.Method != "POST" || r.Path != "/v1/messages" || !r.HasKey {
		t.Errorf("request = %s %s key=%v", r.Method, r.Path, r.HasKey)
	}
	if r.Header.Get("Anthropic-Version") != "2023-06-01" || r.Header.Get("User-Agent") != "MalachiMail/test" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Accept") != "text/event-stream" {
		t.Errorf("headers = %v", r.Header)
	}
	b := r.Body
	if field(t, b, "model") != "claude-opus-5-5" || field(t, b, "system") != "Be brief." || field(t, b, "stream") != true || field(t, b, "max_tokens") != float64(llm.DefaultMaxOutputTokens) {
		t.Errorf("body = %v", b)
	}
	if hasField(b, "output_config") || hasField(b, "tool_choice") {
		t.Errorf("unexpected fields in %v", b)
	}
	if field(t, b, "messages.0.role") != "user" || field(t, b, "messages.0.content.0.type") != "text" || field(t, b, "messages.0.content.0.text") != "Summarise <b>this</b>." {
		t.Errorf("messages[0] = %v", field(t, b, "messages.0"))
	}
	if field(t, b, "messages.1.role") != "assistant" || field(t, b, "messages.1.content.1.type") != "tool_use" || field(t, b, "messages.1.content.1.id") != "call_1" || field(t, b, "messages.1.content.1.name") != "read_message" || field(t, b, "messages.1.content.1.input.id") != "m1" {
		t.Errorf("messages[1] = %v", field(t, b, "messages.1"))
	}
	if field(t, b, "messages.2.content.0.type") != "tool_result" || field(t, b, "messages.2.content.0.tool_use_id") != "call_1" || field(t, b, "messages.2.content.0.content") != "the mail" || field(t, b, "messages.2.content.0.is_error") != true {
		t.Errorf("messages[2] = %v", field(t, b, "messages.2"))
	}
	if field(t, b, "tools.0.name") != "read_message" || field(t, b, "tools.0.description") != "Reads a message" || field(t, b, "tools.0.input_schema.type") != "object" {
		t.Errorf("tools = %v", field(t, b, "tools"))
	}
}

func TestAnthropicToolCalls(t *testing.T) {
	srv := llmtest.New(t, llmtest.Anthropic, toolTurn())
	p := newProvider(t, srv, testKey)
	events, err := run(t, p, llm.Request{Model: "m", Messages: history()[:1], Tools: tools()})
	toolTurnChecks(t, events, err)
	// The deltas of the text precede its done; usage carries message_start's input counters.
	want := []llm.EventKind{llm.EventTextDelta, llm.EventTextDelta, llm.EventTextDelta, llm.EventTextDone, llm.EventToolCall, llm.EventToolCall, llm.EventUsage, llm.EventStop}
	if got := kinds(events); strings.Join(toStrings(got), ",") != strings.Join(toStrings(want), ",") {
		t.Errorf("kinds = %v, want %v", got, want)
	}
	if u := last(find(events, llm.EventUsage)).Usage; *u != *usage {
		t.Errorf("usage = %+v", u)
	}
}

func toStrings(k []llm.EventKind) []string {
	out := make([]string, len(k))
	for i, x := range k {
		out[i] = string(x)
	}
	return out
}

func TestAnthropicSchemaGoesIntoThePrompt(t *testing.T) {
	srv := llmtest.New(t, llmtest.Anthropic, llmtest.Turn{Text: `{"q":"x"}`})
	p := newProvider(t, srv, testKey)
	if _, err := run(t, p, llm.Request{Model: "m", System: "Search.", Messages: history()[:1], JSONSchema: schema, MaxOutputTokens: 300}); err != nil {
		t.Fatal(err)
	}
	b := srv.Requests()[0].Body
	sys, _ := field(t, b, "system").(string)
	if !strings.HasPrefix(sys, "Search.\n\nAnswer with exactly one JSON object that matches this JSON schema and nothing else:\n") || !strings.HasSuffix(sys, string(schema)) {
		t.Errorf("system = %q", sys)
	}
	if hasField(b, "output_config") || hasField(b, "output_format") || field(t, b, "max_tokens") != float64(300) {
		t.Errorf("body = %v", b)
	}
}

func TestAnthropicErrors(t *testing.T) {
	cases := []struct {
		name  string
		turn  llmtest.Turn
		class api.AssistantFailureClass
		retry int
		msg   string
	}{
		{"401", llmtest.Turn{Status: 401}, api.FailureAuth, 0, "scripted error"},
		{"403", llmtest.Turn{Status: 403}, api.FailurePermission, 0, "scripted error"},
		{"429", llmtest.Turn{Status: 429, RetryAfter: "60"}, api.FailureRateLimit, 60, "scripted error"},
		{"529", llmtest.Turn{Status: 529, RetryAfter: "45"}, api.FailureOverloaded, 45, "scripted error"},
		{"400", llmtest.Turn{Status: 400}, api.FailureInvalidRequest, 0, "scripted error"},
		{"400 tools", llmtest.Turn{Status: 400, Body: `{"type":"error","error":{"type":"invalid_request_error","message":"This model does not support tools"}}`}, api.FailureToolsUnsupported, 0, "This model does not support tools"},
		{"404", llmtest.Turn{Status: 404}, api.FailureInvalidRequest, 0, "scripted error"},
		{"500", llmtest.Turn{Status: 500}, api.FailureProtocol, 0, "scripted error"},
		{"400 overloaded type", llmtest.Turn{Status: 400, Body: `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`}, api.FailureOverloaded, 0, "Overloaded"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := llmtest.New(t, llmtest.Anthropic, c.turn)
			p := newProvider(t, srv, testKey)
			events, err := run(t, p, llm.Request{Model: "m", Messages: history()[:1]})
			e := wantClass(t, err, c.class)
			if e.Status != c.turn.Status || e.RetryAfter != c.retry || e.Message != c.msg {
				t.Errorf("error = %+v", e)
			}
			if len(events) != 0 {
				t.Errorf("events after an error: %v", kinds(events))
			}
			if strings.Contains(err.Error(), testKey) {
				t.Error("the key is in the error")
			}
		})
	}
}

func TestAnthropicWrongKeyIs401(t *testing.T) {
	srv := llmtest.New(t, llmtest.Anthropic, llmtest.Turn{Text: "never"})
	srv.Key = testKey
	p := newProvider(t, srv, "sk-wrong")
	_, err := run(t, p, llm.Request{Model: "m", Messages: history()[:1]})
	wantClass(t, err, api.FailureAuth)
	if _, err := p.Models(context.Background()); llm.ClassOf(err) != api.FailureAuth {
		t.Errorf("Models: %v", err)
	}
	if r := srv.Requests()[0]; !r.HasKey || r.Header.Get("X-Api-Key") != "" {
		t.Errorf("the key header was not seen, or leaked into the capture: %+v", r)
	}
}

func TestAnthropicStreamErrorEvent(t *testing.T) {
	srv := newRawServer(t, "text/event-stream", "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":3}}}\n\n"+
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"+
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi\"}}\n\n"+
		"event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n")
	p, err := llm.New(llm.Config{Provider: api.AIAnthropic, BaseURL: srv.URL, Key: testKey, UserAgent: "t"})
	if err != nil {
		t.Fatal(err)
	}
	events, err := run(t, p, llm.Request{Model: "m", Messages: history()[:1]})
	e := wantClass(t, err, api.FailureOverloaded)
	if e.Message != "Overloaded" || e.Status != 0 {
		t.Errorf("error = %+v", e)
	}
	if len(events) != 1 || events[0].Kind != llm.EventTextDelta || events[0].Text != "Hi" {
		t.Errorf("events = %+v", events)
	}
}

func TestAnthropicStopReasons(t *testing.T) {
	for _, c := range []struct {
		turn llmtest.Turn
		want llm.StopReason
	}{
		{llmtest.Turn{Text: "cut", Stop: llm.StopMaxTokens}, llm.StopMaxTokens},
		{llmtest.Turn{Text: "no", Stop: llm.StopRefusal}, llm.StopRefusal},
		{llmtest.Turn{Text: "x", Stop: llm.StopOther}, llm.StopOther},
	} {
		srv := llmtest.New(t, llmtest.Anthropic, c.turn)
		p := newProvider(t, srv, testKey)
		events, err := run(t, p, llm.Request{Model: "m", Messages: history()[:1]})
		if err != nil {
			t.Fatal(err)
		}
		if l := last(events); l.Kind != llm.EventStop || l.Stop != c.want {
			t.Errorf("stop = %+v, want %s", l, c.want)
		}
	}
}

func TestAnthropicModelsPaginate(t *testing.T) {
	srv := llmtest.New(t, llmtest.Anthropic)
	srv.Key = testKey
	srv.Models = []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5"}
	srv.PageSize = 2
	p := newProvider(t, srv, testKey)
	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 3 || models[0].ID != "claude-opus-5-5" || models[0].DisplayName != "Model claude-opus-5-5" || models[2].ID != "claude-haiku-5-5" {
		t.Errorf("models = %+v", models)
	}
	reqs := srv.Requests()
	if len(reqs) != 2 || reqs[0].Path != "/v1/models?limit=100" || reqs[1].Path != "/v1/models?after_id=claude-sonnet-5-5&limit=100" {
		t.Errorf("requests = %+v", reqs)
	}
	for _, r := range reqs {
		if r.Method != "GET" || !r.HasKey || r.Header.Get("Anthropic-Version") == "" || r.Header.Get("Accept") != "application/json" {
			t.Errorf("request = %+v", r)
		}
	}
}
