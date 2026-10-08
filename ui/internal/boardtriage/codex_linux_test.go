// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package boardtriage

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/chatgpt"
)

// The board's triage through the real ChatGPT session (ui/internal/chatgpt:
// its gateway, its tool gate, its JSON-RPC to Codex and to the bridge)
// against a fake codex app-server and a fake bridge. Both are this test
// binary run again (TestMain): the session runs only an ELF executable as
// Codex, so a script would not do. The fake Codex speaks the app-server
// protocol the session expects (initialize, thread/start, turn/start,
// turn/started, item/tool/call, thread/tokenUsage/updated, item/completed,
// turn/completed) and sends one Responses request through the gateway,
// whose answer (a fake transport, no network) carries the usage. The
// fakes read their mode from $HOME/mode and write what they saw to $HOME.
// Linux only, as the chatgpt package (Pdeathsig).

func TestMain(m *testing.M) {
	switch {
	case len(os.Args) > 1 && os.Args[1] == "app-server":
		fakeCodexMain()
		os.Exit(0)
	case slices.Contains(os.Args, "--allow-triage"):
		fakeBridgeMain()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// fakeTokens is a ChatGPT connection that always has a token.
type fakeTokens struct{}

func (fakeTokens) GetAccessToken(context.Context) (string, error) {
	return "fixture-token-never-real", nil
}
func (fakeTokens) SessionContext() context.Context { return context.Background() }

// roundTrip is an http.RoundTripper of a function.
type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// upstream answers the gateway's request with one completed response
// whose usage is 100 input tokens (40 of them cached) and 10 output.
func upstream(t *testing.T) *http.Client {
	return &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != chatgpt.Resource+"/responses" || r.Header.Get("Authorization") != "Bearer fixture-token-never-real" {
			t.Errorf("the gateway sent %s with the wrong token", r.URL)
		}
		_, _ = io.Copy(io.Discard, r.Body)
		body := `data: {"type":"response.created","response":{"id":"resp_1","status":"in_progress","output":[]}}` + "\n\n" +
			`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[],` +
			`"usage":{"input_tokens":100,"output_tokens":10,"input_tokens_details":{"cached_tokens":40}}}}` + "\n\n"
		h := http.Header{}
		h.Set("Content-Type", "text/event-stream")
		return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
}

// codexHarness is a triage harness whose request runs the real ChatGPT
// provider with the fakes in mode.
func codexHarness(t *testing.T, mode string) (*harness, string) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := scratch(t)
	if err := os.WriteFile(filepath.Join(home, "mode"), []byte(mode), 0o600); err != nil {
		t.Fatal(err)
	}
	p := chatgpt.NewProvider(chatgpt.Options{
		Executable: func() string { return self },
		Directory:  filepath.Join(home, "runtime"),
		Env:        []string{"HOME=" + home},
		Model:      func() string { return "fixture-model" },
		HasConsent: func() bool { return true },
		HTTPClient: upstream(t),
	}, fakeTokens{})
	h := newHarness(t, newFakeClaude(t, "true"), options{provider: p, bridge: self})
	h.c.Source = func() string { return "malachi-chatgpt" }
	return h, home
}

func TestFakeCodexTriageRun(t *testing.T) {
	h, home := codexHarness(t, "normal")
	h.board(5, true, 0, nil)
	if !h.c.Start(Manual, 0) {
		t.Fatal("not started")
	}
	h.ended()
	h.expectState("ended", Finished(Manual, 1, 1, t0))
	// The gateway's response reported its whole usage and the turn
	// completed: 60 input, 40 read from cache, 10 output, no lower bound.
	h.expectEnds("usage", end("run_1", "", &api.BoardUsage{InputTokens: 60, OutputTokens: 10, CacheReadInputTokens: 40}))
	if got := readFile(t, filepath.Join(home, "bridge-calls")); got != "annotate_case c_1\nannotate_case c_2\n" {
		t.Errorf("bridge calls %q", got)
	}
}

func TestFakeCodexHostileRun(t *testing.T) {
	h, home := codexHarness(t, "hostile")
	h.board(5, true, 0, nil)
	h.c.Start(Manual, 0)
	h.ended()
	// The second turn ends the session: the run stops with what it did.
	h.expectState("ended", Failed(Manual, board.FailStopped, t0))
	h.expectEnds("end", end("run_1", api.RunFailed, atLeast(&api.BoardUsage{InputTokens: 60, OutputTokens: 10, CacheReadInputTokens: 40})))
	if got := readFile(t, filepath.Join(home, "hostile")); got != "denied\n" {
		t.Errorf("the tool outside the policy: %q", got)
	}
	if got := readFile(t, filepath.Join(home, "bridge-calls")); got != "annotate_case c_1\n" {
		t.Errorf("bridge calls %q", got)
	}
}

// --- the fake codex app-server ---------------------------------------------

// rpcPeer is one side of line-delimited JSON-RPC over stdin and stdout.
type rpcPeer struct {
	mu      sync.Mutex
	out     io.Writer
	next    int
	pending map[string]chan json.RawMessage
}

type rpcLine struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
}

func (p *rpcPeer) send(v any) {
	b, _ := json.Marshal(v)
	p.mu.Lock()
	defer p.mu.Unlock()
	_, _ = p.out.Write(append(b, '\n'))
}

func (p *rpcPeer) reply(id json.RawMessage, result any) {
	p.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (p *rpcPeer) notify(method string, params any) {
	p.send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

// call sends a request and waits for its result.
func (p *rpcPeer) call(method string, params any) json.RawMessage {
	p.mu.Lock()
	p.next++
	id := "s" + strconv.Itoa(p.next)
	ch := make(chan json.RawMessage, 1)
	p.pending[strconv.Quote(id)] = ch
	p.mu.Unlock()
	p.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	return <-ch
}

// serve reads lines, hands results to their calls and requests and
// notifications to handle, until stdin ends.
func (p *rpcPeer) serve(handle func(rpcLine)) {
	scan := bufio.NewScanner(os.Stdin)
	scan.Buffer(make([]byte, 8192), 16<<20)
	for scan.Scan() {
		var l rpcLine
		if json.Unmarshal(scan.Bytes(), &l) != nil {
			return
		}
		if l.Method == "" {
			p.mu.Lock()
			ch := p.pending[string(l.ID)]
			delete(p.pending, string(l.ID))
			p.mu.Unlock()
			if ch != nil {
				ch <- l.Result
			}
			continue
		}
		handle(l)
	}
}

func appendLine(name, line string) {
	f, err := os.OpenFile(filepath.Join(os.Getenv("HOME"), name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		_, _ = f.WriteString(line + "\n")
		_ = f.Close()
	}
}

func fakeMode() string {
	b, _ := os.ReadFile(filepath.Join(os.Getenv("HOME"), "mode"))
	return strings.TrimSpace(string(b))
}

func fakeCodexMain() {
	baseURL := ""
	for _, a := range os.Args {
		if v, ok := strings.CutPrefix(a, "model_providers.malachi_chatgpt.base_url="); ok {
			baseURL, _ = strconv.Unquote(v)
		}
	}
	p := &rpcPeer{out: os.Stdout, pending: map[string]chan json.RawMessage{}}
	var tools []string
	p.serve(func(l rpcLine) {
		switch l.Method {
		case "initialize":
			p.reply(l.ID, map[string]any{"userAgent": "fake-codex"})
		case "thread/start":
			var params struct {
				DynamicTools []struct {
					Tools []struct {
						Name string `json:"name"`
					} `json:"tools"`
				} `json:"dynamicTools"`
			}
			_ = json.Unmarshal(l.Params, &params)
			for _, ns := range params.DynamicTools {
				for _, t := range ns.Tools {
					tools = append(tools, t.Name)
				}
			}
			p.reply(l.ID, map[string]any{
				"thread":             map[string]any{"id": "thr_1", "ephemeral": true},
				"instructionSources": []any{}, "modelProvider": "malachi_chatgpt",
			})
		case "turn/start":
			p.reply(l.ID, map[string]any{"turn": map[string]any{"id": "turn_1"}})
			go fakeCodexTurn(p, baseURL, tools)
		case "turn/interrupt":
			p.reply(l.ID, map[string]any{})
		}
	})
}

// fakeCodexTurn is the turn: one Responses request through the gateway, the
// bridge calls, and the end (or, hostile, a tool outside the policy and a
// second turn).
func fakeCodexTurn(p *rpcPeer, baseURL string, tools []string) {
	p.notify("turn/started", map[string]any{"threadId": "thr_1", "turn": map[string]any{"id": "turn_1"}})
	inference(baseURL, tools)
	toolCall := func(call, tool string, args map[string]any) json.RawMessage {
		return p.call("item/tool/call", map[string]any{
			"threadId": "thr_1", "turnId": "turn_1", "callId": call, "namespace": "malachi", "tool": tool, "arguments": args,
		})
	}
	toolCall("call_1", assistant.TriageAnnotateTool, map[string]any{"caseId": "c_1", "inputKey": "k1"})
	if fakeMode() == "hostile" {
		r := toolCall("call_2", "send_message", map[string]any{"to": "x@example.invalid"})
		if bytes.Contains(r, []byte(`"error"`)) {
			appendLine("hostile", "denied")
		} else {
			appendLine("hostile", "allowed")
		}
		p.notify("turn/started", map[string]any{"threadId": "thr_1", "turn": map[string]any{"id": "turn_2"}})
		return
	}
	toolCall("call_2", assistant.TriageAnnotateTool, map[string]any{"caseId": "c_2", "inputKey": "k2"})
	p.notify("thread/tokenUsage/updated", map[string]any{"threadId": "thr_1", "turnId": "turn_1",
		"tokenUsage": map[string]any{"total": map[string]any{"inputTokens": 100, "outputTokens": 10}}})
	p.notify("item/completed", map[string]any{"threadId": "thr_1", "turnId": "turn_1",
		"item": map[string]any{"type": "agentMessage", "id": "msg_1", "text": "Annotated 1 case."}})
	p.notify("turn/completed", map[string]any{"threadId": "thr_1", "turn": map[string]any{"id": "turn_1", "status": "completed"}})
}

// inference is one Responses request to the gateway with the dynamic
// tools as the namespace catalog the gateway requires.
func inference(baseURL string, tools []string) {
	functions := []map[string]any{}
	for _, t := range tools {
		functions = append(functions, map[string]any{"type": "function", "name": t, "parameters": map[string]any{"type": "object"}})
	}
	body, _ := json.Marshal(map[string]any{
		"model": "fixture-model", "input": []any{},
		"tools": []any{map[string]any{"type": "namespace", "name": "malachi", "tools": functions}},
	})
	req, err := http.NewRequest("POST", baseURL+"/responses", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+os.Getenv("MALACHI_CODEX_GATE_CREDENTIAL"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		appendLine("inference", "failed")
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	appendLine("inference", strconv.Itoa(resp.StatusCode))
}

// --- the fake bridge ---------------------------------------------------------

func fakeBridgeMain() {
	p := &rpcPeer{out: os.Stdout, pending: map[string]chan json.RawMessage{}}
	p.serve(func(l rpcLine) {
		switch l.Method {
		case "initialize":
			p.reply(l.ID, map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "malachi"}})
		case "tools/list":
			list := []any{}
			for _, name := range assistant.TriageTools {
				list = append(list, map[string]any{
					"name": strings.TrimPrefix(name, "mcp__malachi__"), "description": "fixture",
					"inputSchema": map[string]any{"type": "object"},
				})
			}
			p.reply(l.ID, map[string]any{"tools": list})
		case "tools/call":
			var c struct {
				Name      string `json:"name"`
				Arguments struct {
					CaseID string `json:"caseId"`
				} `json:"arguments"`
			}
			_ = json.Unmarshal(l.Params, &c)
			appendLine("bridge-calls", c.Name+" "+c.Arguments.CaseID)
			text, isError := "unknown tool", true
			if c.Name == assistant.TriageAnnotateTool {
				switch c.Arguments.CaseID {
				case "c_1":
					text, isError = fmt.Sprintf("annotated case %s: state in effect you", c.Arguments.CaseID), false
				default:
					text = "conflict: the case changed; read the queue again"
				}
			}
			p.reply(l.ID, map[string]any{"isError": isError, "content": []any{map[string]any{"type": "text", "text": text}}})
		}
	})
}
