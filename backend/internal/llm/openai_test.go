// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package llm_test

import (
	"context"
	"testing"

	"github.com/schotek/malachi/backend/internal/llm"
	"github.com/schotek/malachi/backend/internal/llm/llmtest"
	"github.com/schotek/malachi/backend/pkg/api"
)

func TestOpenAIText(t *testing.T) {
	srv := llmtest.New(t, llmtest.OpenAI, llmtest.Turn{Text: "Hello, world!", Usage: usage})
	srv.Key = testKey
	p := newProvider(t, srv, testKey)
	events, err := run(t, p, llm.Request{Model: "gpt-6.1-sol", System: "Be brief.", Messages: history(), Tools: tools()})
	// The Responses API has no cache-creation counter.
	want := *usage
	want.CacheCreationInputTokens = 0
	textTurnChecks(t, events, err, "Hello, world!", want)

	r := srv.Requests()[0]
	if r.Method != "POST" || r.Path != "/v1/responses" || !r.HasKey || r.Header.Get("Accept") != "text/event-stream" {
		t.Errorf("request = %+v", r)
	}
	b := r.Body
	if field(t, b, "model") != "gpt-6.1-sol" || field(t, b, "instructions") != "Be brief." || field(t, b, "stream") != true || field(t, b, "store") != false || field(t, b, "max_output_tokens") != float64(llm.DefaultMaxOutputTokens) {
		t.Errorf("body = %v", b)
	}
	if hasField(b, "text") || hasField(b, "messages") || hasField(b, "system") {
		t.Errorf("unexpected fields in %v", b)
	}
	if field(t, b, "input.0.role") != "user" || field(t, b, "input.0.content") != "Summarise <b>this</b>." {
		t.Errorf("input[0] = %v", field(t, b, "input.0"))
	}
	if field(t, b, "input.1.role") != "assistant" || field(t, b, "input.1.content.0.type") != "output_text" || field(t, b, "input.1.content.0.text") != "Reading it." {
		t.Errorf("input[1] = %v", field(t, b, "input.1"))
	}
	if field(t, b, "input.2.type") != "function_call" || field(t, b, "input.2.call_id") != "call_1" || field(t, b, "input.2.name") != "read_message" || field(t, b, "input.2.arguments") != `{"id":"m1"}` {
		t.Errorf("input[2] = %v", field(t, b, "input.2"))
	}
	if field(t, b, "input.3.type") != "function_call_output" || field(t, b, "input.3.call_id") != "call_1" || field(t, b, "input.3.output") != "ERROR: the mail" {
		t.Errorf("input[3] = %v", field(t, b, "input.3"))
	}
	if field(t, b, "tools.0.type") != "function" || field(t, b, "tools.0.name") != "read_message" || field(t, b, "tools.0.strict") != false || field(t, b, "tools.0.parameters.type") != "object" {
		t.Errorf("tools = %v", field(t, b, "tools"))
	}
}

func TestOpenAIToolCalls(t *testing.T) {
	srv := llmtest.New(t, llmtest.OpenAI, toolTurn())
	p := newProvider(t, srv, testKey)
	events, err := run(t, p, llm.Request{Model: "m", Messages: history()[:1], Tools: tools()})
	toolTurnChecks(t, events, err)
	if u := last(find(events, llm.EventUsage)).Usage; u.InputTokens != 10 || u.OutputTokens != 5 || u.CacheReadInputTokens != 2 {
		t.Errorf("usage = %+v", u)
	}
}

func TestOpenAISchemaIsTextFormat(t *testing.T) {
	srv := llmtest.New(t, llmtest.OpenAI, llmtest.Turn{Text: `{"q":"x"}`})
	p := newProvider(t, srv, testKey)
	if _, err := run(t, p, llm.Request{Model: "m", System: "Search.", Messages: history()[:1], JSONSchema: schema}); err != nil {
		t.Fatal(err)
	}
	b := srv.Requests()[0].Body
	if field(t, b, "instructions") != "Search." || field(t, b, "text.format.type") != "json_schema" || field(t, b, "text.format.name") != "answer" || field(t, b, "text.format.strict") != true || field(t, b, "text.format.schema.type") != "object" {
		t.Errorf("body = %v", b)
	}
}

func TestOpenAIIncomplete(t *testing.T) {
	srv := llmtest.New(t, llmtest.OpenAI, llmtest.Turn{Text: "cut off", Stop: llm.StopMaxTokens}, llmtest.Turn{Text: "no", Stop: llm.StopRefusal})
	p := newProvider(t, srv, testKey)
	events, err := run(t, p, llm.Request{Model: "m", Messages: history()[:1]})
	if err != nil {
		t.Fatal(err)
	}
	if l := last(events); l.Stop != llm.StopMaxTokens {
		t.Errorf("stop = %+v", l)
	}
	events, err = run(t, p, llm.Request{Model: "m", Messages: history()[:1]})
	if err != nil {
		t.Fatal(err)
	}
	if l := last(events); l.Stop != llm.StopRefusal {
		t.Errorf("stop = %+v", l)
	}
}

func TestOpenAIErrors(t *testing.T) {
	cases := []struct {
		name  string
		turn  llmtest.Turn
		class api.AssistantFailureClass
		retry int
	}{
		{"401", llmtest.Turn{Status: 401}, api.FailureAuth, 0},
		{"403", llmtest.Turn{Status: 403}, api.FailurePermission, 0},
		{"429", llmtest.Turn{Status: 429, RetryAfter: "60"}, api.FailureRateLimit, 60},
		{"503", llmtest.Turn{Status: 503, RetryAfter: "40"}, api.FailureOverloaded, 40},
		{"529", llmtest.Turn{Status: 529, RetryAfter: "40"}, api.FailureOverloaded, 40},
		{"400", llmtest.Turn{Status: 400}, api.FailureInvalidRequest, 0},
		{"400 tools", llmtest.Turn{Status: 400, Body: `{"error":{"message":"The model gpt-6-luna does not support function calling","type":"invalid_request_error"}}`}, api.FailureToolsUnsupported, 0},
		// An account without quota answers 429 insufficient_quota: quota,
		// not rateLimit, and the one retry of a 429 is not made.
		{"429 quota", llmtest.Turn{Status: 429, RetryAfter: "1", Body: `{"error":{"message":"You exceeded your current quota, please check your plan and billing details.","type":"insufficient_quota","code":"insufficient_quota"}}`}, api.FailureQuota, 0},
		// An account without credit (seen 2026-11-07): the message names
		// the credit, whatever the 4xx; a 402 is quota whatever it says.
		{"402 no credits", llmtest.Turn{Status: 402, Body: `{"error":{"message":"You have no credits remaining. Add credits to continue using the API at https://platform.openai.com/settings/organization/billing/.","type":"invalid_request_error","code":null}}`}, api.FailureQuota, 0},
		{"400 no credits", llmtest.Turn{Status: 400, Body: `{"error":{"message":"You have no credits remaining. Add credits to continue using the API at https://platform.openai.com/settings/organization/billing/.","type":"invalid_request_error","code":null}}`}, api.FailureQuota, 0},
		{"402 bare", llmtest.Turn{Status: 402}, api.FailureQuota, 0},
		{"403 billing word", llmtest.Turn{Status: 403, Body: `{"error":{"message":"Your organization's billing admin must grant this project access.","type":"permission_error"}}`}, api.FailurePermission, 0},
		{"404", llmtest.Turn{Status: 404}, api.FailureInvalidRequest, 0},
		{"502", llmtest.Turn{Status: 502}, api.FailureProtocol, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := llmtest.New(t, llmtest.OpenAI, c.turn)
			p := newProvider(t, srv, testKey)
			_, err := run(t, p, llm.Request{Model: "m", Messages: history()[:1]})
			e := wantClass(t, err, c.class)
			if e.Status != c.turn.Status || e.RetryAfter != c.retry {
				t.Errorf("error = %+v", e)
			}
			if n := len(srv.Requests()); c.class == api.FailureQuota && n != 1 {
				t.Errorf("%d requests for a quota failure, want 1", n)
			}
		})
	}
}

func TestOpenAIFailedEvent(t *testing.T) {
	srv := newRawServer(t, "text/event-stream",
		"event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"r\",\"status\":\"in_progress\"}}\n\n"+
			"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"item_id\":\"m\",\"delta\":\"Hi\"}\n\n"+
			"event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"id\":\"r\",\"status\":\"failed\",\"error\":{\"code\":\"rate_limit_exceeded\",\"message\":\"Rate limit\"}}}\n\n")
	p, err := llm.New(llm.Config{Provider: api.AIOpenAI, BaseURL: srv.URL + "/v1", Key: testKey, UserAgent: "t"})
	if err != nil {
		t.Fatal(err)
	}
	events, err := run(t, p, llm.Request{Model: "m", Messages: history()[:1]})
	e := wantClass(t, err, api.FailureRateLimit)
	if e.Message != "Rate limit" || e.Code != "rate_limit_exceeded" || e.Status != 0 || len(events) != 1 || events[0].Text != "Hi" {
		t.Errorf("error = %+v, events = %+v", e, events)
	}
}

// OpenAI answers a key without credit with a 200 and an error event in
// the stream (no HTTP status; seen 2026-11-07): the message makes it
// quota, with the event's code kept; a model without tools the same way
// is toolsUnsupported; an unknown code with another message is
// invalidRequest.
func TestOpenAIErrorEventByMessage(t *testing.T) {
	for _, c := range []struct {
		data  string
		class api.AssistantFailureClass
		code  string
	}{
		{`{"type":"error","code":"server_error","message":"You have no credits remaining. Add credits to continue using the API at https://platform.openai.com/settings/organization/billing/."}`, api.FailureOverloaded, "server_error"},
		{`{"type":"error","error":{"code":null,"message":"You have no credits remaining. Add credits to continue using the API at https://platform.openai.com/settings/organization/billing/."}}`, api.FailureQuota, ""},
		{`{"type":"error","code":"invalid_request_error","message":"The model gpt-6-luna does not support function calling"}`, api.FailureToolsUnsupported, "invalid_request_error"},
		{`{"type":"error","code":"invalid_prompt","message":"Invalid prompt"}`, api.FailureInvalidRequest, "invalid_prompt"},
		// The code OpenAI really sends for an exhausted credit (B2).
		{`{"type":"error","code":"credit_balance_exhausted","message":"No more."}`, api.FailureQuota, "credit_balance_exhausted"},
		{`{"type":"response.failed","response":{"id":"r","status":"failed","error":{"code":"","message":"Your credit balance is too low."}}}`, api.FailureQuota, ""},
	} {
		srv := newRawServer(t, "text/event-stream", "event: error\ndata: "+c.data+"\n\n")
		p, err := llm.New(llm.Config{Provider: api.AIOpenAI, BaseURL: srv.URL + "/v1", Key: testKey, UserAgent: "t"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = run(t, p, llm.Request{Model: "m", Messages: history()[:1]})
		e := wantClass(t, err, c.class)
		if e.Code != c.code || e.Status != 0 {
			t.Errorf("%s: error %+v", c.data, e)
		}
	}
}

func TestOpenAIModels(t *testing.T) {
	srv := llmtest.New(t, llmtest.OpenAI)
	srv.Key = testKey
	srv.Models = []string{"gpt-6.1-sol", "", "gpt-6-luna"}
	p := newProvider(t, srv, testKey)
	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "gpt-6.1-sol" || models[1].ID != "gpt-6-luna" || models[0].DisplayName != "" {
		t.Errorf("models = %+v", models)
	}
	r := srv.Requests()[0]
	if r.Method != "GET" || r.Path != "/v1/models" || !r.HasKey {
		t.Errorf("request = %+v", r)
	}
}
