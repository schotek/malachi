// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/schotek/malachi/backend/internal/llm"
	"github.com/schotek/malachi/backend/pkg/api"
)

// scripted answers the n-th Stream from the n-th script entry: its events
// in order, or its error; block, when set, holds every call until the
// context ends or the channel is closed.
type scripted struct {
	mu    sync.Mutex
	steps [][]llm.Event
	errs  []error
	reqs  []llm.Request
	block chan struct{}
}

func (p *scripted) Stream(ctx context.Context, req llm.Request, emit func(llm.Event) error) error {
	p.mu.Lock()
	p.reqs = append(p.reqs, req)
	i := len(p.reqs) - 1
	block := p.block
	p.mu.Unlock()
	if block != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-block:
		}
	}
	if i < len(p.errs) && p.errs[i] != nil {
		return p.errs[i]
	}
	if i >= len(p.steps) {
		return &llm.Error{Class: api.FailureProtocol, Message: "no script for this call"}
	}
	for _, ev := range p.steps[i] {
		if err := emit(ev); err != nil {
			return err
		}
	}
	return nil
}

func (p *scripted) Models(context.Context) ([]api.AIModel, error) { return nil, nil }

func (p *scripted) requests() []llm.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]llm.Request(nil), p.reqs...)
}

type readIn struct {
	ID string `json:"id"`
}
type draftIn struct {
	Body string `json:"body"`
}
type noIn struct{}

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

// fakeBridge is an in-memory MCP server with three tools, connected as
// the daemon connects to the bridge.
func fakeBridge(t *testing.T) *Bridge {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "fake-bridge", Version: "0"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "read_message", Description: "Reads a message."},
		func(_ context.Context, _ *mcp.CallToolRequest, in readIn) (*mcp.CallToolResult, any, error) {
			return text("--- BEGIN UNTRUSTED MAIL CONTENT abc ---\nhello from " + in.ID + "\n--- END UNTRUSTED MAIL CONTENT abc ---"), nil, nil
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "create_draft", Description: "Makes a draft."},
		func(_ context.Context, _ *mcp.CallToolRequest, in draftIn) (*mcp.CallToolResult, any, error) {
			return text("Draft d_1 saved (" + in.Body + ")"), nil, nil
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "fail", Description: "Refuses."},
		func(context.Context, *mcp.CallToolRequest, noIn) (*mcp.CallToolResult, any, error) {
			r := text("refused")
			r.IsError = true
			return r, nil, nil
		})
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	b, err := Connect(ctx, ct, "malachi-mail", "test")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// collector records events.
type collector struct {
	mu  sync.Mutex
	evs []api.AssistantEvent
}

func (c *collector) emit(ev api.AssistantEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evs = append(c.evs, ev)
}

func (c *collector) kinds() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, e := range c.evs {
		out = append(out, string(e.Kind))
	}
	return out
}

func (c *collector) last() api.AssistantEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.evs[len(c.evs)-1]
}

func (c *collector) find(kind api.AssistantEventKind) *api.AssistantEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.evs {
		if c.evs[i].Kind == kind {
			return &c.evs[i]
		}
	}
	return nil
}

func toolCall(id, name, input string) llm.Event {
	var in json.RawMessage
	if input != "" {
		in = json.RawMessage(input)
	}
	return llm.Event{Kind: llm.EventToolCall, Call: &llm.ToolCall{ID: id, Name: name, Input: in}}
}

func usage(in, out int64) llm.Event {
	return llm.Event{Kind: llm.EventUsage, Usage: &api.AssistantUsage{InputTokens: in, OutputTokens: out}}
}

func TestConnectListsTools(t *testing.T) {
	b := fakeBridge(t)
	defer b.Close()
	names := []string{}
	for _, tool := range b.Tools() {
		names = append(names, tool.Name)
		if len(tool.InputSchema) == 0 || !json.Valid(tool.InputSchema) {
			t.Errorf("%s: schema %s", tool.Name, tool.InputSchema)
		}
	}
	if strings.Join(names, ",") != "create_draft,fail,read_message" {
		t.Fatalf("tools %v", names)
	}
	got, isErr, err := b.Call(context.Background(), "read_message", json.RawMessage(`{"id":"m_1"}`))
	if err != nil || isErr || !strings.Contains(got, "hello from m_1") {
		t.Fatalf("call: %q %v %v", got, isErr, err)
	}
	if got, isErr, err := b.Call(context.Background(), "fail", nil); err != nil || !isErr || got != "refused" {
		t.Fatalf("fail: %q %v %v", got, isErr, err)
	}
}

// A panel turn: the model reads a message through the bridge, then
// answers; the events, the history and the second request carry the
// tool result verbatim.
func TestTurnWithTools(t *testing.T) {
	p := &scripted{steps: [][]llm.Event{
		{{Kind: llm.EventTextDelta, Text: "Let me "}, {Kind: llm.EventTextDelta, Text: "look."}, {Kind: llm.EventTextDone, Text: "Let me look."},
			toolCall("call_1", "read_message", `{"id":"m_1"}`), usage(100, 10), {Kind: llm.EventStop, Stop: llm.StopToolUse}},
		{{Kind: llm.EventTextDelta, Text: "Hello back."}, {Kind: llm.EventTextDone, Text: "Hello back."}, usage(200, 20),
			{Kind: llm.EventStop, Stop: llm.StopEndTurn}},
	}}
	c := &collector{}
	s := New(Config{ID: "as_1", Provider: p, Model: "m", System: "sys", Tools: fakeBridge(t),
		Allowed: []string{"read_message", "create_draft"}, Emit: c.emit})
	if names := s.ToolNames(); strings.Join(names, ",") != "create_draft,read_message" {
		t.Fatalf("tools %v", names)
	}
	if err := s.Turn(context.Background(), "Summarise m_1"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	want := "init,textDelta,textDelta,text,toolUse,usage,toolResult,textDelta,text,usage,result"
	if got := strings.Join(c.kinds(), ","); got != want {
		t.Fatalf("events\n got %s\nwant %s", got, want)
	}
	tr := c.find(api.EventToolResult)
	if tr.Tool != "read_message" || tr.ID != "call_1" || tr.IsError || !strings.Contains(tr.Text, "hello from m_1") {
		t.Fatalf("tool result %+v", tr)
	}
	res := c.last()
	if res.Result == nil || !res.Result.Success || res.Result.Reason != "endTurn" || res.Usage == nil || res.Usage.InputTokens != 300 || res.Usage.OutputTokens != 30 {
		t.Fatalf("result %+v usage %+v", res.Result, res.Usage)
	}
	if u := s.Usage(); u.InputTokens != 300 || s.Turns() != 1 {
		t.Fatalf("session usage %+v turns %d", u, s.Turns())
	}
	reqs := p.requests()
	if len(reqs) != 2 || reqs[0].System != "sys" || reqs[0].Model != "m" || len(reqs[0].Tools) != 2 {
		t.Fatalf("requests %+v", reqs)
	}
	h := reqs[1].Messages
	if len(h) != 3 || h[0].Role != llm.RoleUser || h[1].Role != llm.RoleAssistant || h[2].Role != llm.RoleUser {
		t.Fatalf("history %+v", h)
	}
	if h[1].Blocks[1].Kind != llm.BlockToolUse || h[1].Blocks[1].ID != "call_1" || string(h[1].Blocks[1].Input) != `{"id":"m_1"}` {
		t.Fatalf("assistant blocks %+v", h[1].Blocks)
	}
	if h[2].Blocks[0].Kind != llm.BlockToolResult || h[2].Blocks[0].ID != "call_1" || !strings.Contains(h[2].Blocks[0].Text, "hello from m_1") {
		t.Fatalf("result block %+v", h[2].Blocks[0])
	}
	// A second turn continues the history.
	p.steps = append(p.steps, []llm.Event{{Kind: llm.EventTextDone, Text: "Sure."}, {Kind: llm.EventStop, Stop: llm.StopEndTurn}})
	if err := s.Turn(context.Background(), "Thanks"); err != nil {
		t.Fatal(err)
	}
	if len(p.requests()[2].Messages) != 5 || s.Turns() != 2 {
		t.Fatalf("second turn history %d", len(p.requests()[2].Messages))
	}
}

// A tool outside the allow-list and arguments that were not JSON are
// answered without the bridge, as error results; the turn goes on.
func TestRefusedCalls(t *testing.T) {
	p := &scripted{steps: [][]llm.Event{
		{toolCall("c1", "fail", `{}`), toolCall("c2", "read_message", ""), {Kind: llm.EventStop, Stop: llm.StopToolUse}},
		{{Kind: llm.EventTextDone, Text: "ok"}, {Kind: llm.EventStop, Stop: llm.StopEndTurn}},
	}}
	c := &collector{}
	s := New(Config{ID: "as_2", Provider: p, Tools: fakeBridge(t), Allowed: []string{"read_message"}, Emit: c.emit})
	if err := s.Turn(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	var results []api.AssistantEvent
	for _, e := range c.evs {
		if e.Kind == api.EventToolResult {
			results = append(results, e)
		}
	}
	if len(results) != 2 || !results[0].IsError || !strings.Contains(results[0].Text, "not available") ||
		!results[1].IsError || !strings.Contains(results[1].Text, "not valid JSON") {
		t.Fatalf("results %+v", results)
	}
	if !c.last().Result.Success {
		t.Fatal("the turn did not end well")
	}
}

func TestRoundLimit(t *testing.T) {
	loop := []llm.Event{toolCall("c", "read_message", `{"id":"m"}`), {Kind: llm.EventStop, Stop: llm.StopToolUse}}
	p := &scripted{steps: [][]llm.Event{loop, loop, loop, loop}}
	c := &collector{}
	s := New(Config{ID: "as_3", Provider: p, Tools: fakeBridge(t), MaxRounds: 2, Emit: c.emit})
	err := s.Turn(context.Background(), "x")
	if err == nil || llm.ClassOf(err) != api.FailureLimit {
		t.Fatalf("err %v", err)
	}
	if f := c.last(); f.Kind != api.EventFailure || f.Failure.Class != api.FailureLimit {
		t.Fatalf("last %+v", f)
	}
	if len(p.requests()) != 3 || s.Ended() {
		t.Fatalf("%d requests, ended %v", len(p.requests()), s.Ended())
	}
}

func TestContextFullEndsSession(t *testing.T) {
	loop := []llm.Event{toolCall("c", "read_message", `{"id":"m"}`), {Kind: llm.EventStop, Stop: llm.StopToolUse}}
	p := &scripted{steps: [][]llm.Event{loop, loop, loop}}
	c := &collector{}
	s := New(Config{ID: "as_4", Provider: p, Tools: fakeBridge(t), MaxHistory: 100, Emit: c.emit})
	err := s.Turn(context.Background(), "x")
	if llm.ClassOf(err) != api.FailureContextFull || !s.Ended() {
		t.Fatalf("err %v ended %v", err, s.Ended())
	}
	if err := s.Turn(context.Background(), "again"); !errors.Is(err, ErrEnded) {
		t.Fatalf("after the end: %v", err)
	}
}

func TestStructuredResult(t *testing.T) {
	p := &scripted{steps: [][]llm.Event{
		{{Kind: llm.EventTextDone, Text: "```json\n{ \"query\": \"from:jana\" }\n```"}, {Kind: llm.EventStop, Stop: llm.StopEndTurn}},
		{{Kind: llm.EventTextDone, Text: "not json"}, {Kind: llm.EventStop, Stop: llm.StopEndTurn}},
	}}
	c := &collector{}
	s := New(Config{ID: "as_5", Provider: p, JSONSchema: json.RawMessage(`{"type":"object"}`), Emit: c.emit})
	if err := s.Turn(context.Background(), "words"); err != nil {
		t.Fatal(err)
	}
	if got := string(c.last().Result.Structured); got != `{"query":"from:jana"}` {
		t.Fatalf("structured %s", got)
	}
	if r := p.requests()[0]; string(r.JSONSchema) != `{"type":"object"}` || r.Tools != nil {
		t.Fatalf("request %+v", r)
	}
	if err := s.Turn(context.Background(), "again"); err != nil {
		t.Fatal(err)
	}
	if c.last().Result.Structured != nil || !c.last().Result.Success {
		t.Fatalf("a text answer: %+v", c.last().Result)
	}
	if got := c.find(api.EventInit); got.Tools != nil {
		t.Fatalf("a session without tools lists %v", got.Tools)
	}
}

// closeRecorder wraps the bridge to see Close.
type closeRecorder struct {
	inner  Tools
	closed int
}

func (r *closeRecorder) Tools() []llm.Tool { return r.inner.Tools() }
func (r *closeRecorder) Call(ctx context.Context, name string, input json.RawMessage) (string, bool, error) {
	return r.inner.Call(ctx, name, input)
}
func (r *closeRecorder) Close() error { r.closed++; return r.inner.Close() }

func TestFatalFailureEndsSession(t *testing.T) {
	p := &scripted{errs: []error{&llm.Error{Class: api.FailureAuth, Status: 401, Message: "invalid x-api-key"}}}
	c := &collector{}
	br := &closeRecorder{inner: fakeBridge(t)}
	s := New(Config{ID: "as_6", Provider: p, Tools: br, Emit: c.emit})
	err := s.Turn(context.Background(), "x")
	if llm.ClassOf(err) != api.FailureAuth || !s.Ended() || br.closed != 1 {
		t.Fatalf("err %v ended %v closed %d", err, s.Ended(), br.closed)
	}
	if f := c.last(); f.Failure == nil || f.Failure.Class != api.FailureAuth || f.Failure.Message != "invalid x-api-key" {
		t.Fatalf("failure %+v", f.Failure)
	}
	s.Stop()
	if br.closed != 1 {
		t.Fatal("Stop closed the bridge again")
	}
	// A rate limit does not end the session.
	p2 := &scripted{errs: []error{&llm.Error{Class: api.FailureRateLimit, Status: 429, RetryAfter: 7}},
		steps: [][]llm.Event{nil, {{Kind: llm.EventTextDone, Text: "ok"}, {Kind: llm.EventStop, Stop: llm.StopEndTurn}}}}
	c2 := &collector{}
	s2 := New(Config{ID: "as_7", Provider: p2, Emit: c2.emit})
	if err := s2.Turn(context.Background(), "x"); llm.ClassOf(err) != api.FailureRateLimit || s2.Ended() {
		t.Fatalf("rate limit: %v ended %v", err, s2.Ended())
	}
	if c2.last().Failure.RetryAfter != 7 {
		t.Fatalf("retryAfter %+v", c2.last().Failure)
	}
	if err := s2.Turn(context.Background(), "again"); err != nil {
		t.Fatalf("after a rate limit: %v", err)
	}
}

func TestBridgeFailureEndsSession(t *testing.T) {
	br := fakeBridge(t)
	_ = br.Close() // every call fails from now on
	p := &scripted{steps: [][]llm.Event{{toolCall("c", "read_message", `{"id":"m"}`), {Kind: llm.EventStop, Stop: llm.StopToolUse}}}}
	c := &collector{}
	s := New(Config{ID: "as_8", Provider: p, Tools: br, Emit: c.emit})
	err := s.Turn(context.Background(), "x")
	if llm.ClassOf(err) != api.FailureBridge || !s.Ended() {
		t.Fatalf("err %v ended %v", err, s.Ended())
	}
}

func TestStopDuringTurnAndBusy(t *testing.T) {
	p := &scripted{block: make(chan struct{})}
	c := &collector{}
	s := New(Config{ID: "as_9", Provider: p, Emit: c.emit, Timeout: time.Minute})
	done := make(chan error, 1)
	go func() { done <- s.Turn(context.Background(), "x") }()
	deadline := time.Now().Add(2 * time.Second)
	for !s.Running() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if err := s.Turn(context.Background(), "y"); !errors.Is(err, ErrBusy) {
		t.Fatalf("busy: %v", err)
	}
	s.Stop()
	select {
	case err := <-done:
		if llm.ClassOf(err) != api.FailureStopped {
			t.Fatalf("stopped turn: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the turn did not end")
	}
	if f := c.last(); f.Kind != api.EventFailure || f.Failure.Class != api.FailureStopped || !s.Ended() {
		t.Fatalf("last %+v ended %v", f, s.Ended())
	}
	if err := s.Turn(context.Background(), "z"); !errors.Is(err, ErrEnded) {
		t.Fatalf("after Stop: %v", err)
	}
}

func TestTurnTimeout(t *testing.T) {
	p := &scripted{block: make(chan struct{})}
	c := &collector{}
	s := New(Config{ID: "as_10", Provider: p, Emit: c.emit, Timeout: 30 * time.Millisecond})
	err := s.Turn(context.Background(), "x")
	if llm.ClassOf(err) != api.FailureTimeout || s.Ended() {
		t.Fatalf("err %v ended %v", err, s.Ended())
	}
}

func TestLongToolResultIsCut(t *testing.T) {
	br := fakeBridge(t)
	p := &scripted{steps: [][]llm.Event{
		{toolCall("c", "create_draft", `{"body":"`+strings.Repeat("x", 300)+`"}`), {Kind: llm.EventStop, Stop: llm.StopToolUse}},
		{{Kind: llm.EventTextDone, Text: "ok"}, {Kind: llm.EventStop, Stop: llm.StopEndTurn}},
	}}
	c := &collector{}
	s := New(Config{ID: "as_11", Provider: p, Tools: br, MaxResult: 64, Emit: c.emit})
	if err := s.Turn(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	tr := c.find(api.EventToolResult)
	if len(tr.Text) > 64+40 || !strings.HasSuffix(tr.Text, "[the result was cut here]") {
		t.Fatalf("result %q", tr.Text)
	}
}

func TestChildEnv(t *testing.T) {
	got := childEnv([]string{"PATH=/bin", "HOME=/h", "MALACHI_SOCKET=/x", "MALACHI_MCP_TRIAGE_RUN=r", "ANTHROPIC_API_KEY=k", "LANG=cs_CZ.UTF-8", "junk"})
	if strings.Join(got, " ") != "PATH=/bin HOME=/h LANG=cs_CZ.UTF-8" {
		t.Fatalf("env %v", got)
	}
}

func TestManager(t *testing.T) {
	m := NewManager(3, 2, time.Minute)
	now := time.Now()
	mk := func(id string) *Session { return New(Config{ID: id, Provider: &scripted{}}) }
	a1, a2, a3, b1 := mk("a1"), mk("a2"), mk("a3"), mk("b1")
	if err := m.Add("A", a1, now); err != nil {
		t.Fatal(err)
	}
	if err := m.Add("A", a2, now); err != nil {
		t.Fatal(err)
	}
	if err := m.Add("A", a3, now); !errors.Is(err, ErrTooManyForConn) {
		t.Fatalf("third of A: %v", err)
	}
	if err := m.Add("B", b1, now); err != nil {
		t.Fatal(err)
	}
	if err := m.Add("C", mk("c1"), now); !errors.Is(err, ErrTooManySessions) {
		t.Fatalf("fourth overall: %v", err)
	}
	if _, ok := m.Get("B", "a1", now); ok {
		t.Fatal("another connection's session")
	}
	if s, ok := m.Get("A", "a1", now.Add(30*time.Second)); !ok || s != a1 {
		t.Fatal("own session")
	}
	// a2 untouched for over a minute goes; a1 was touched; b1 ended.
	b1.Stop()
	gone := m.Sweep(now.Add(70 * time.Second))
	if len(gone) != 2 || m.Len() != 1 {
		t.Fatalf("swept %d, left %d", len(gone), m.Len())
	}
	if _, ok := m.Get("A", "a1", now); !ok {
		t.Fatal("a1 went")
	}
	if s, ok := m.Remove("A", "a1"); !ok || s != a1 || m.Len() != 0 {
		t.Fatal("remove")
	}
	_ = m.Add("A", a2, now)
	_ = m.Add("A", a3, now)
	if got := m.RemoveConn("A"); len(got) != 2 || m.Len() != 0 {
		t.Fatalf("remove conn %d", len(got))
	}
	if id := NewID(); !strings.HasPrefix(id, "as_") || len(id) != 15 {
		t.Fatalf("id %q", id)
	}
	if got := m.All(); len(got) != 0 {
		t.Fatal("all")
	}
}
