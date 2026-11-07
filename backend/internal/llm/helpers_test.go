// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package llm_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/llm"
	"github.com/schotek/malachi/backend/internal/llm/llmtest"
	"github.com/schotek/malachi/backend/pkg/api"
)

const testKey = "sk-test-key-0123456789"

// providerOf is the dialect's api.AIProvider.
func providerOf(d llmtest.Dialect) api.AIProvider {
	switch d {
	case llmtest.Anthropic:
		return api.AIAnthropic
	case llmtest.OpenAI:
		return api.AIOpenAI
	}
	return api.AIOpenAICompatible
}

// newProvider makes the Provider of a fake with the package's own HTTP
// client.
func newProvider(t *testing.T, srv *llmtest.Server, key string) llm.Provider {
	t.Helper()
	p, err := llm.New(llm.Config{Provider: providerOf(srv.Dialect), BaseURL: srv.URL(), Key: key, UserAgent: "MalachiMail/test"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// run streams req and collects the events.
func run(t *testing.T, p llm.Provider, req llm.Request) ([]llm.Event, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var events []llm.Event
	err := p.Stream(ctx, req, func(e llm.Event) error {
		events = append(events, e)
		return nil
	})
	return events, err
}

func kinds(events []llm.Event) []llm.EventKind {
	out := make([]llm.EventKind, len(events))
	for i, e := range events {
		out[i] = e.Kind
	}
	return out
}

func deltaText(events []llm.Event) string {
	var b strings.Builder
	for _, e := range events {
		if e.Kind == llm.EventTextDelta {
			b.WriteString(e.Text)
		}
	}
	return b.String()
}

func find(events []llm.Event, kind llm.EventKind) []llm.Event {
	var out []llm.Event
	for _, e := range events {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func last(events []llm.Event) llm.Event {
	if len(events) == 0 {
		return llm.Event{}
	}
	return events[len(events)-1]
}

// field walks a decoded JSON body: "a.b.0.c".
func field(t *testing.T, v any, path string) any {
	t.Helper()
	for _, p := range strings.Split(path, ".") {
		switch x := v.(type) {
		case map[string]any:
			var ok bool
			if v, ok = x[p]; !ok {
				t.Fatalf("field %q: %q missing", path, p)
			}
		case []any:
			i := 0
			for _, r := range p {
				i = i*10 + int(r-'0')
			}
			if i >= len(x) {
				t.Fatalf("field %q: index %d of %d", path, i, len(x))
			}
			v = x[i]
		default:
			t.Fatalf("field %q: %q is not a container (%T)", path, p, v)
		}
	}
	return v
}

func hasField(v any, path string) bool {
	for _, p := range strings.Split(path, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return false
		}
		if v, ok = m[p]; !ok {
			return false
		}
	}
	return true
}

func wantClass(t *testing.T, err error, class api.AssistantFailureClass) *llm.Error {
	t.Helper()
	var e *llm.Error
	if !errors.As(err, &e) {
		t.Fatalf("err = %v (%T), want *llm.Error %s", err, err, class)
	}
	if e.Class != class {
		t.Fatalf("class = %s, want %s (%v)", e.Class, class, err)
	}
	if llm.ClassOf(err) != class {
		t.Fatalf("ClassOf = %s, want %s", llm.ClassOf(err), class)
	}
	return e
}

var schema = json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}},"required":["q"],"additionalProperties":false}`)

// history is a user turn, an assistant turn with a text and a tool call,
// and the tool's result.
func history() []llm.Message {
	return []llm.Message{
		{Role: llm.RoleUser, Blocks: []llm.Block{{Kind: llm.BlockText, Text: "Summarise <b>this</b>."}}},
		{Role: llm.RoleAssistant, Blocks: []llm.Block{
			{Kind: llm.BlockText, Text: "Reading it."},
			{Kind: llm.BlockToolUse, ID: "call_1", Name: "read_message", Input: json.RawMessage(`{"id":"m1"}`)},
		}},
		{Role: llm.RoleUser, Blocks: []llm.Block{{Kind: llm.BlockToolResult, ID: "call_1", Text: "the mail", IsError: true}}},
	}
}

func tools() []llm.Tool {
	return []llm.Tool{{Name: "read_message", Description: "Reads a message", InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}}}`)}}
}

var usage = &api.AssistantUsage{InputTokens: 10, OutputTokens: 5, CacheReadInputTokens: 2, CacheCreationInputTokens: 1}

// textTurnChecks are the assertions every dialect's text answer meets.
func textTurnChecks(t *testing.T, events []llm.Event, err error, text string, want api.AssistantUsage) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if got := deltaText(events); got != text {
		t.Errorf("deltas = %q, want %q", got, text)
	}
	done := find(events, llm.EventTextDone)
	if len(done) != 1 || done[0].Text != text {
		t.Errorf("textDone = %+v, want one with %q", done, text)
	}
	us := find(events, llm.EventUsage)
	if len(us) == 0 || *last(us).Usage != want {
		t.Errorf("usage = %+v, want %+v", us, want)
	}
	if l := last(events); l.Kind != llm.EventStop || l.Stop != llm.StopEndTurn {
		t.Errorf("last event = %+v, want stop endTurn", l)
	}
	for _, e := range events[:len(events)-1] {
		if e.Kind == llm.EventStop {
			t.Error("stop before the end")
		}
	}
}

// toolTurnChecks are the assertions every dialect's tool turn meets:
// a text, two calls (the second with unparsable arguments) and toolUse.
func toolTurnChecks(t *testing.T, events []llm.Event, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	calls := find(events, llm.EventToolCall)
	if len(calls) != 2 {
		t.Fatalf("tool calls = %+v, want 2 (events %v)", calls, kinds(events))
	}
	c := calls[0].Call
	if c == nil || c.ID != "call_1" || c.Name != "read_message" || string(c.Input) != `{"id":"m1"}` || calls[0].Text != "" {
		t.Errorf("call 1 = %+v", calls[0])
	}
	c = calls[1].Call
	if c == nil || c.ID != "call_2" || c.Name != "list_folders" || c.Input != nil || calls[1].Text != `{"account":` {
		t.Errorf("call 2 = %+v %+v", calls[1], c)
	}
	done := find(events, llm.EventTextDone)
	if len(done) != 1 || done[0].Text != "Let me look." {
		t.Errorf("textDone = %+v", done)
	}
	if l := last(events); l.Kind != llm.EventStop || l.Stop != llm.StopToolUse {
		t.Errorf("last event = %+v, want stop toolUse", l)
	}
}

func toolTurn() llmtest.Turn {
	return llmtest.Turn{Text: "Let me look.", Calls: []llmtest.Call{
		{ID: "call_1", Name: "read_message", Input: `{"id":"m1"}`},
		{ID: "call_2", Name: "list_folders", Input: `{"account":`},
	}, Usage: usage}
}
