// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package chatgpt

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/assistantpanel"
)

type fixtureTokens struct{ ctx context.Context }

func (t fixtureTokens) SessionContext() context.Context {
	if t.ctx != nil {
		return t.ctx
	}
	return context.Background()
}
func (fixtureTokens) GetAccessToken(context.Context) (string, error) {
	return "fixture-token-never-real", nil
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestGatewayFiltersAndDeniesTools(t *testing.T) {
	g := &inferenceGate{tools: map[string]bool{"read_message": true}}
	body := `{"tools":[{"type":"function","name":"exec_command"},{"type":"namespace","name":"malachi","tools":[{"type":"function","name":"read_message"},{"type":"function","name":"delete_message"}]}],"store":true,"stream":false,"tool_choice":"required","parallel_tool_calls":true}`
	b, err := g.filterRequest([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "exec_command") || strings.Contains(string(b), "delete_message") || strings.Contains(string(b), "tool_choice") {
		t.Fatalf("unsafe catalog: %s", b)
	}
	var r map[string]any
	_ = json.Unmarshal(b, &r)
	if r["store"] != false || r["stream"] != true || r["parallel_tool_calls"] != false {
		t.Fatalf("retention/replay: %s", b)
	}
	for _, body := range []string{`{"previous_response_id":"x"}`, `{"conversation":"x"}`, `{"input":[{"type":"additional_tools"}]}`, `{"tools":[]}`, `{"tools":[{"type":"namespace","name":"malachi","tools":[{"type":"function","name":"read_message"},{"type":"function","name":"read_message"}]}]}`} {
		if _, err = g.filterRequest([]byte(body)); err == nil {
			t.Fatalf("accepted: %s", body)
		}
	}
	for _, data := range []string{`{"item":{"type":"web_search_call"}}`, `{"item":{"type":"function_call","namespace":"other","name":"read_message"}}`, `{"item":{"type":"function_call","namespace":"malachi","name":"create_draft"}}`, `{"response":{"output":[{"type":"local_shell_call"}]}}`} {
		if g.validateEvent(data) == nil {
			t.Fatalf("accepted: %s", data)
		}
	}
	if g.validateEvent(`{"item":{"type":"function_call","namespace":"malachi","name":"read_message"}}`) != nil {
		t.Fatal("allowed denied")
	}
}
func TestGatewayBeforeForwardingAndTokenBoundary(t *testing.T) {
	called := 0
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		called++
		if r.URL.String() != Resource+"/responses" || r.Header.Get("Authorization") != "Bearer fixture-token-never-real" {
			t.Fatal("credential/endpoint boundary")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"item\":{\"type\":\"local_shell_call\"}}\n\n"))}, nil
	})}
	g, err := newInferenceGate(fixtureTokens{}, map[string]bool{}, client)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	send := func(path, token string) (string, int) {
		r, _ := http.NewRequest("POST", path, strings.NewReader(`{"tools":[]}`))
		r.Header.Set("Authorization", "Bearer "+token)
		c := &http.Client{Timeout: time.Second}
		res, err := c.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return string(b), res.StatusCode
	}
	if _, code := send(g.baseURL+"/responses", "wrong"); code != 403 || called != 0 {
		t.Fatal("unauthenticated forwarding")
	}
	body, status := send(g.baseURL+"/responses", g.credential)
	if status != 502 || strings.Contains(body, "local_shell") || g.Failure() != "codex_disallowed_inference_tool" || called != 1 {
		t.Fatalf("unvalidated body: %d %q %s", status, body, g.Failure())
	}
}
func TestToolPolicySnapshotsAndTiers(t *testing.T) {
	for _, tools := range []*assistantpanel.Tools{nil, {Allowed: assistant.AllowedTools}, {BridgeArgs: assistant.TriageBridgeArgs("run_1", 40), Allowed: assistant.TriageToolsFor(assistant.TriageManual)}, {BridgeArgs: assistant.TriageBridgeArgs("run_2", 40), Allowed: assistant.TriageToolsFor(assistant.TriageAutomatic)}, {BridgeArgs: assistant.SuggestReplyBridgeArgs("m_1"), Allowed: assistant.SuggestReplyTools}} {
		set, args, err := toolPolicy(tools)
		if err != nil {
			t.Fatal(err)
		}
		if tools != nil && len(set) != len(tools.Allowed) {
			t.Fatal("tools lost")
		}
		if tools != nil && len(args) > 0 {
			args[0] = "--allow-send"
			if tools.BridgeArgs[0] == "--allow-send" {
				t.Fatal("mutable policy")
			}
		}
	}
	for _, tools := range []*assistantpanel.Tools{{BridgeArgs: []string{"--allow-send"}, Allowed: assistant.AllowedTools}, {Allowed: []string{"mcp__malachi__send_message"}}, {Allowed: []string{"read_message"}}, {BridgeArgs: assistant.SuggestReplyBridgeArgs("m_1"), Allowed: assistant.TriageTools}, {BridgeArgs: []string{"--allow-triage", "--triage-run", "r", "--triage-max", "201"}, Allowed: assistant.TriageTools}} {
		if _, _, err := toolPolicy(tools); err == nil {
			t.Fatalf("accepted unsafe policy %#v", tools)
		}
	}
	set, _, _ := toolPolicy(&assistantpanel.Tools{BridgeArgs: assistant.TriageBridgeArgs("r", 2), Allowed: assistant.TriageToolsFor(assistant.TriageAutomatic)})
	if set["create_draft"] {
		t.Fatal("automatic run received drafts")
	}
}

func TestValidatedUsageAndCachedTokens(t *testing.T) {
	g := &inferenceGate{}
	var event assistant.Event
	g.usage = func(e assistant.Event) { event = e }
	g.reportUsage(`{"type":"response.completed","response":{"id":"r","usage":{"input_tokens":10,"output_tokens":3,"input_tokens_details":{"cached_tokens":4}}}}`)
	if event.Usage == nil || event.MessageID != "r" || event.Usage.InputTokens != 6 || event.Usage.OutputTokens != 3 || event.Usage.CacheReadInputTokens != 4 {
		t.Fatal(event)
	}
	event = assistant.Event{}
	g.reportUsage(`{"type":"response.completed","response":{"id":"r","usage":{"input_tokens":null,"output_tokens":3}}}`)
	if event.Usage != nil {
		t.Fatal("malformed usage accepted")
	}
	if uniqueJSON([]byte(`{"sub":"a","sub":"b"}`)) || uniqueJSON([]byte(`{"x":1} {"x":2}`)) {
		t.Fatal("duplicate/trailing JSON accepted")
	}
}

func TestCanceledSessionCannotSendToolCall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &rpcClient{}
	if _, err := client.Call(ctx, "tools/call", map[string]any{}); err != context.Canceled {
		t.Fatal("canceled RPC reached child")
	}
	s := &session{ctx: ctx, active: true, thread: "thread", turn: "turn", bridge: client, tools: map[string]bool{"create_draft": true}, calls: map[string]bool{}}
	result := s.serverRequest("item/tool/call", json.RawMessage(`{"threadId":"thread","turnId":"turn","namespace":"malachi","tool":"create_draft","callId":"call","arguments":{}}`))
	if result.(map[string]any)["error"] == nil || len(s.calls) != 0 {
		t.Fatal("canceled session authorized a tool")
	}
}

func TestGatewayNormalizesResponsesLiteCatalog(t *testing.T) {
	g := &inferenceGate{tools: map[string]bool{"read_message": true}}
	input := `{"input":[{"type":"additional_tools","role":"developer","tools":[{"type":"namespace","name":"functions","tools":[{"type":"custom","name":"exec"}]},{"type":"namespace","name":"malachi","tools":[{"type":"function","name":"read_message"},{"type":"function","name":"send_message"}]}]},{"type":"message","role":"user","content":"fixture"}],"tools":null}`
	filtered, err := g.filterRequest([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"additional_tools", "send_message", "functions", "exec"} {
		if strings.Contains(string(filtered), forbidden) {
			t.Fatal(string(filtered))
		}
	}
	var root map[string]json.RawMessage
	_ = json.Unmarshal(filtered, &root)
	var items []map[string]json.RawMessage
	_ = json.Unmarshal(root["input"], &items)
	if len(items) != 1 || str(items[0], "type") != "message" || !strings.Contains(string(root["tools"]), "read_message") {
		t.Fatal(string(filtered))
	}
	for _, denied := range []string{
		`{"input":[{"type":"additional_tools","tools":null}]}`,
		`{"input":[{"type":"additional_tools","tools":{}}]}`,
		`{"input":[{"type":"additional_tools","tools":[]}]}`,
		`{"tools":[{"type":"namespace","name":"malachi","tools":[{"type":"function","name":"read_message"}]}],"input":[{"type":"additional_tools","tools":[{"type":"namespace","name":"malachi","tools":[{"type":"function","name":"read_message"}]}]}]}`,
	} {
		if _, err := g.filterRequest([]byte(denied)); err == nil {
			t.Fatal(denied)
		}
	}
}

func TestGatewayStreamWithoutContentType(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, body string
		status                  int
		failure                 string
	}{
		{"missing header", "", "data: {\"type\":\"response.created\",\"response\":{\"output\":[]}}\n\n", 200, ""},
		{"case and parameters", "Text/Event-Stream; Charset=utf-8", "data: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n", 200, ""},
		{"heartbeat", "", ": keepalive\n\ndata: {\"type\":\"response.created\",\"response\":{\"output\":[]}}\n\n", 200, ""},
		{"html", "", "<html>not an event</html>\n\n", 502, "codex_gate_non_streaming_response"},
		{"html before SSE", "", "<html>not an event</html>\n\ndata: {\"type\":\"response.created\"}\n\n", 502, "codex_gate_non_streaming_response"},
		{"json", "", "{\"output\":[]}\n\n", 502, "codex_gate_non_streaming_response"},
		{"wrong mime", "application/json", "data: {\"type\":\"response.created\"}\n\n", 502, "codex_gate_non_streaming_response"},
		{"unrelated JSON data", "", "data: {\"ok\":true}\n\n", 502, "codex_gate_non_streaming_response"},
		{"bad JSON", "", "data: invalid\n\n", 502, "codex_gate_non_streaming_response"},
		{"forbidden first tool", "", "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"local_shell_call\"}}\n\n", 502, "codex_disallowed_inference_tool"},
		{"truncated", "", "data: {\"type\":\"response.created\"}\n", 502, "codex_gate_non_streaming_response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("Accept") != "text/event-stream" {
					t.Error("SSE not requested")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {tc.contentType}}, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}
			g, err := newInferenceGate(fixtureTokens{}, map[string]bool{}, client)
			if err != nil {
				t.Fatal(err)
			}
			defer g.Close()
			req, _ := http.NewRequest("POST", g.baseURL+"/responses", strings.NewReader(`{"tools":[]}`))
			req.Header.Set("Authorization", "Bearer "+g.credential)
			response, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, _ := io.ReadAll(response.Body)
			if response.StatusCode != tc.status || g.Failure() != tc.failure {
				t.Fatalf("status=%d failure=%s", response.StatusCode, g.Failure())
			}
			if tc.status == 200 && response.Header.Get("Content-Type") != "text/event-stream" {
				t.Fatal("SSE MIME not normalized")
			}
			if tc.status != 200 && strings.Contains(string(body), "data:") {
				t.Fatal("unvalidated frame forwarded")
			}
		})
	}
}
