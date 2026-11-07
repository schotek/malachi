// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestAIJSON(t *testing.T) {
	at := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		v    any
		want string
	}{
		{"ai account", AccountConfig{Name: "Ollama doma", Kind: AccountAI, AI: &AIConfig{
			Provider: AIOpenAICompatible, BaseURL: "http://192.168.1.20:11434/v1", Model: "llama3.3",
			Consent: &AIConsent{Version: 1, AcceptedAt: &at}}},
			`{"name":"Ollama doma","email":"","kind":"ai","ai":{"provider":"openaiCompatible",` +
				`"baseUrl":"http://192.168.1.20:11434/v1","model":"llama3.3","consent":{"version":1,"acceptedAt":"2026-11-07T09:00:00Z"}}}`},
		{"anthropic account", AccountConfig{Name: "Anthropic", Kind: AccountAI, AI: &AIConfig{
			Provider: AIAnthropic, Model: "claude-opus-5-5", BoardModel: "claude-haiku-5-5"}},
			`{"name":"Anthropic","email":"","kind":"ai","ai":{"provider":"anthropic","model":"claude-opus-5-5","boardModel":"claude-haiku-5-5"}}`},
		{"test result", AccountTestResult{AI: &AITestResult{EndpointTestResult: EndpointTestResult{OK: true, LatencyMS: 120},
			Models: []AIModel{{ID: "llama3.3"}, {ID: "qwen3", DisplayName: "Qwen 3"}}}},
			`{"ai":{"ok":true,"latencyMs":120,"models":[{"id":"llama3.3"},{"id":"qwen3","displayName":"Qwen 3"}]}}`},
		{"prompt params", AssistantPromptParams{Purpose: PurposeRewrite, Language: "cs", AccountID: "acc_1",
			Input: &PromptInput{Rewrite: &RewriteInput{Preset: RewriteShorter, Passage: "Dobrý den, …"}}},
			`{"purpose":"rewrite","language":"cs","accountId":"acc_1","input":{"rewrite":{"preset":"shorter","passage":"Dobrý den, …"}}}`},
		{"prompt result", PromptResult{SystemPrompt: "You help…", Message: "Shorten:", Tools: []string{"read_message"},
			BridgeArgs: []string{"--in-app", "--reply-only", "m_1"}, JSONSchema: json.RawMessage(`{"type":"object"}`)},
			`{"systemPrompt":"You help…","message":"Shorten:","tools":["read_message"],"bridgeArgs":["--in-app","--reply-only","m_1"],"jsonSchema":{"type":"object"}}`},
		{"session start", AssistantSessionStartParams{AccountID: "acc_9", Purpose: PurposeTriage, Language: "cs",
			Bridge: "/opt/malachi/malachi-mcp", Trigger: TriggerManual, RunID: "run_1",
			Input: &PromptInput{Triage: &TriageInput{MaxCases: 40, Drafts: true}}},
			`{"accountId":"acc_9","purpose":"triage","language":"cs","bridge":"/opt/malachi/malachi-mcp","trigger":"manual","runId":"run_1",` +
				`"input":{"triage":{"maxCases":40,"drafts":true}}}`},
		{"panel turn", AssistantSessionSendParams{SessionID: "as_1", Input: PromptInput{Text: "Shrň to",
			Context: &PromptContext{AccountID: "acc_1", MessageIDs: []MessageID{"m_2", "m_1"}}}},
			`{"sessionId":"as_1","input":{"text":"Shrň to","context":{"accountId":"acc_1","messageIds":["m_2","m_1"]}}}`},
		{"event tool", AssistantEvent{SessionID: "as_1", Kind: EventToolResult, Tool: "read_message", ID: "call_1", Text: "--- BEGIN …"},
			`{"sessionId":"as_1","kind":"toolResult","text":"--- BEGIN …","tool":"read_message","id":"call_1"}`},
		{"event result", AssistantEvent{SessionID: "as_1", Kind: EventResult, Result: &AssistantResult{Success: true, Reason: "endTurn"},
			Usage: &AssistantUsage{InputTokens: 1200, OutputTokens: 80}},
			`{"sessionId":"as_1","kind":"result","result":{"success":true,"reason":"endTurn"},` +
				`"usage":{"inputTokens":1200,"outputTokens":80,"cacheReadInputTokens":0,"cacheCreationInputTokens":0}}`},
		{"event failure", AssistantEvent{SessionID: "as_1", Kind: EventFailure, Failure: &AssistantFailure{Class: FailureRateLimit, RetryAfter: 30}},
			`{"sessionId":"as_1","kind":"failure","failure":{"class":"rateLimit","retryAfter":30}}`},
		{"usage day", AssistantUsageResult{Days: []AssistantUsageDay{{Day: "2026-11-07", AccountID: "acc_9",
			AssistantUsage: AssistantUsage{InputTokens: 10, OutputTokens: 2, CacheReadInputTokens: 1, CacheCreationInputTokens: 0}, Sessions: 1, Turns: 3}}},
			`{"days":[{"day":"2026-11-07","accountId":"acc_9","inputTokens":10,"outputTokens":2,"cacheReadInputTokens":1,"cacheCreationInputTokens":0,"sessions":1,"turns":3}]}`},
	}
	for _, c := range cases {
		raw, err := json.Marshal(c.v)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := string(raw); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
	// Typed round trips: what a client sends comes back as it was.
	roundTrip(t, "ai account", AccountConfig{Name: "Ollama doma", Kind: AccountAI, AI: &AIConfig{
		Provider: AIOpenAICompatible, BaseURL: "http://192.168.1.20:11434/v1", Model: "llama3.3",
		Consent: &AIConsent{Version: 1, AcceptedAt: &at}}})
	roundTrip(t, "event", AssistantEvent{SessionID: "as_1", Kind: EventResult, Result: &AssistantResult{Success: true, Structured: json.RawMessage(`{"query":"from:jana"}`)},
		Usage: &AssistantUsage{InputTokens: 1}})
	roundTrip(t, "start", AssistantSessionStartParams{AccountID: "acc_9", Purpose: PurposeReply, Language: "cs", Bridge: "/x/malachi-mcp",
		Input: &PromptInput{Reply: &ReplyInput{AccountID: "acc_1", MessageID: "m_1", Others: []MessageID{"m_2"}, FollowUp: true}}})
}

func TestAIConfigHelpers(t *testing.T) {
	for _, p := range []AIProvider{AIAnthropic, AIOpenAI, AIOpenAICompatible} {
		if !p.Valid() {
			t.Errorf("%q not valid", p)
		}
	}
	for _, p := range []AIProvider{"", "Anthropic", "ollama"} {
		if p.Valid() {
			t.Errorf("%q valid", p)
		}
	}
	if (AIConfig{Provider: AIAnthropic, BaseURL: "http://x"}).EffectiveBaseURL() != AnthropicBaseURL {
		t.Error("anthropic base URL is fixed")
	}
	if (AIConfig{Provider: AIOpenAI}).EffectiveBaseURL() != OpenAIBaseURL {
		t.Error("openai base URL is fixed")
	}
	if (AIConfig{Provider: AIOpenAICompatible, BaseURL: "http://localhost:11434/v1"}).EffectiveBaseURL() != "http://localhost:11434/v1" {
		t.Error("compatible base URL is the configured one")
	}
	if (AIConfig{}).Consented() || (AIConfig{Consent: &AIConsent{Version: AssistantConsentVersion - 1}}).Consented() {
		t.Error("no or an old consent counts")
	}
	if !(AIConfig{Consent: &AIConsent{Version: AssistantConsentVersion}}).Consented() {
		t.Error("the current consent does not count")
	}
	for _, p := range []PromptPurpose{PurposePanel, PurposeRewrite, PurposeWrite, PurposeSearch, PurposeTriage, PurposeReply} {
		if !p.Valid() {
			t.Errorf("%q not valid", p)
		}
	}
	if PromptPurpose("chat").Valid() {
		t.Error("unknown purpose valid")
	}
	sum := AssistantUsage{InputTokens: 1, OutputTokens: 2}.Add(AssistantUsage{InputTokens: 3, CacheReadInputTokens: 4})
	if sum != (AssistantUsage{InputTokens: 4, OutputTokens: 2, CacheReadInputTokens: 4}) {
		t.Errorf("sum %+v", sum)
	}
}

func TestConnContext(t *testing.T) {
	if _, ok := ConnFromContext(context.Background()); ok {
		t.Error("a bare context has a connection")
	}
	if _, ok := ConnFromContext(WithConn(context.Background(), "")); ok {
		t.Error("an empty id counts")
	}
	id, ok := ConnFromContext(WithConn(context.Background(), "c_1"))
	if !ok || id != "c_1" {
		t.Errorf("got %q %v", id, ok)
	}
}
