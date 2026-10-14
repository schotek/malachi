// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistantpanel

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/ui/internal/assistant"
)

// The one-shot request with the bridge (StartCall with Tools), as the
// board's triage run uses it (macOS AssistantTriageTests, the request
// part), against the fake claude of the panel's tests.

// triageTools is the bridge of the tests' triage requests.
var triageTools = &Tools{
	Bridge: "/b/malachi-mcp", Socket: "/s.sock",
	BridgeArgs: assistant.TriageBridgeArgs("run_7", 3), Allowed: assistant.TriageTools,
}

// toolsRequest records what a StartCall reports.
type toolsRequest struct {
	tools    []assistant.Event
	usage    []assistant.Event
	outcomes []Outcome
}

func (rec *toolsRequest) call(c Call) Call {
	c.OnTool = func(e assistant.Event) { rec.tools = append(rec.tools, e) }
	c.OnUsage = func(e assistant.Event) { rec.usage = append(rec.usage, e) }
	return c
}

func (rec *toolsRequest) done(o Outcome) { rec.outcomes = append(rec.outcomes, o) }

// The request starts Claude Code with the bridge and the tools it was
// given, and hands every tool call and result to OnTool.
func TestRequestWithTools(t *testing.T) {
	fake := newFakeClaude(t, "true", "", fakeTurn{lines: []string{
		fakeInit, fakeToolUse("t1", "list_triage_queue"), fakeToolResult("t1", "2 cases", false),
		fakeToolUse("t2", "annotate_case"), fakeToolResult("t2", "ok", false),
		fakeToolUse("t3", "annotate_case"), fakeToolResult("t3", "conflict", true),
		fakeText("Done."), fakeResult("Done.", true),
	}})
	h := newRequestHarness(t, fake, true, true)
	h.request.env = append(h.request.env, "MALACHI_MCP_ALLOW_SEND=1")
	var rec toolsRequest
	h.request.StartCall(rec.call(Call{SystemPrompt: "SYS", Message: "triage", Tools: triageTools}), rec.done)
	h.loop.runUntil(t, func() bool { return len(rec.outcomes) > 0 })
	if len(rec.outcomes) != 1 || rec.outcomes[0].Kind != OutcomeAnswered || rec.outcomes[0].Text != "Done." {
		t.Errorf("outcomes %+v", rec.outcomes)
	}
	var kinds []assistant.EventKind
	var tools, ids []string
	var errs []bool
	for _, e := range rec.tools {
		kinds = append(kinds, e.Kind)
		tools = append(tools, e.Tool)
		ids = append(ids, e.ToolUseID)
		errs = append(errs, e.IsError)
	}
	use, res := assistant.EventToolUse, assistant.EventToolResult
	if !slices.Equal(kinds, []assistant.EventKind{use, res, use, res, use, res}) ||
		!slices.Equal(tools, []string{"list_triage_queue", "", "annotate_case", "", "annotate_case", ""}) ||
		!slices.Equal(ids, []string{"t1", "t1", "t2", "t2", "t3", "t3"}) ||
		!slices.Equal(errs, []bool{false, false, false, false, false, true}) {
		t.Errorf("tools %v %q %q %v", kinds, tools, ids, errs)
	}
	want := assistant.Args(assistant.Options{
		Bridge: "/b/malachi-mcp", Socket: "/s.sock", Model: assistant.Haiku, SystemPrompt: "SYS",
		BridgeArgs: []string{"--allow-triage", "--triage-run", "run_7", "--triage-max", "3"}, Tools: assistant.TriageTools,
	})
	if got := fake.args(); !slices.Equal(got, want) {
		t.Errorf("args\n%q\nwant\n%q", got, want)
	}
	if !slices.Equal(fake.prompts(), []string{"triage"}) {
		t.Errorf("prompts %q", fake.prompts())
	}
	// The child's environment keeps no MALACHI_* variable: the run id goes
	// as a flag.
	if strings.Contains(fake.env(), "MALACHI_") {
		t.Errorf("environment:\n%s", fake.env())
	}
}

// With the bridge asked for, an init that does not report it connected
// ends the request; without the bridge the init is not looked at.
func TestRequestToolsMissing(t *testing.T) {
	fake := newFakeClaude(t, "true", "", fakeTurn{lines: []string{fakeInitFailed, fakeText("x"), fakeResult("x", true)}})
	h := newRequestHarness(t, fake, true, true)
	var rec toolsRequest
	h.request.StartCall(rec.call(Call{SystemPrompt: "SYS", Message: "triage", Tools: triageTools}), rec.done)
	h.loop.runUntil(t, func() bool { return len(rec.outcomes) > 0 })
	if !slices.EqualFunc(rec.outcomes, []Outcome{failed(FailureToolsMissing, "")}, sameOutcome) {
		t.Errorf("outcomes %+v", rec.outcomes)
	}
	f := Failure{Kind: FailureToolsMissing}
	if f.Text(tr) != assistant.PanelTexts(tr).ToolsMissing || f.ReasonText(tr) != assistant.PanelTexts(tr).ToolsMissing {
		t.Errorf("texts %q, %q", f.Text(tr), f.ReasonText(tr))
	}

	other := newRequestHarness(t, newFakeClaude(t, "true", "", fakeTurn{lines: []string{fakeInitFailed, fakeText("x"), fakeResult("x", true)}}), true, true)
	var rec2 toolsRequest
	other.request.StartCall(rec2.call(Call{SystemPrompt: "SYS", Message: "m"}), rec2.done)
	other.loop.runUntil(t, func() bool { return len(rec2.outcomes) > 0 })
	if len(rec2.outcomes) != 1 || rec2.outcomes[0].Kind != OutcomeAnswered || rec2.outcomes[0].Text != "x" {
		t.Errorf("without the bridge: %+v", rec2.outcomes)
	}
}

// The call's own timeout replaces the request's; the call's model the
// setting.
func TestRequestPerCallTimeoutAndModel(t *testing.T) {
	fake := newFakeClaude(t, "true", "", fakeTurn{lines: []string{fakeInit, fakeToolUse("t1", "list_triage_queue")}, shell: "sleep 30"})
	h := newRequestHarness(t, fake, true, true)
	var rec toolsRequest
	started := time.Now()
	h.request.StartCall(rec.call(Call{SystemPrompt: "S", Message: "m", Tools: triageTools, Timeout: 400 * time.Millisecond, Model: assistant.Opus}), rec.done)
	h.loop.runUntil(t, func() bool { return len(rec.outcomes) > 0 })
	if !slices.EqualFunc(rec.outcomes, []Outcome{failed(FailureStopped, timedOut)}, sameOutcome) || !rec.outcomes[0].Failure.TimedOut() {
		t.Errorf("outcomes %+v", rec.outcomes)
	}
	if time.Since(started) > 5*time.Second || h.request.Timeout != 10*time.Second {
		t.Errorf("took %v, timeout %v", time.Since(started), h.request.Timeout)
	}
	args := fake.args()
	if i := slices.Index(args, "--model"); i < 0 || args[i+1] != "opus" {
		t.Errorf("args %q", args)
	}
	if (Failure{Kind: FailureStopped, Reason: "boom"}).TimedOut() || (Failure{Kind: FailureNotFound, Reason: timedOut}).TimedOut() {
		t.Error("TimedOut of another failure")
	}
}

// A handler that cancels the request from a tool event stops it: no
// further event, no completion.
func TestRequestCancelFromATool(t *testing.T) {
	fake := newFakeClaude(t, "true", "", fakeTurn{lines: []string{
		fakeInit, fakeToolUse("t1", "annotate_case"), fakeToolUse("t2", "annotate_case"), fakeResult("x", true),
	}})
	h := newRequestHarness(t, fake, true, true)
	var tools []string
	var outcomes []Outcome
	h.request.StartCall(Call{SystemPrompt: "S", Message: "m", Tools: triageTools, OnTool: func(e assistant.Event) {
		tools = append(tools, e.ToolUseID)
		h.request.Cancel()
	}}, func(o Outcome) { outcomes = append(outcomes, o) })
	h.loop.runUntil(t, func() bool { return len(tools) > 0 && !h.request.Running() })
	h.loop.settle(t, 200*time.Millisecond)
	if !slices.Equal(tools, []string{"t1"}) || len(outcomes) != 0 {
		t.Errorf("tools %q, outcomes %+v", tools, outcomes)
	}
}

// Every event with usage goes to OnUsage before it is handled: the API
// messages' and the result's.
func TestRequestUsage(t *testing.T) {
	msg := `{"type":"assistant","message":{"id":"m1","role":"assistant","content":[{"type":"tool_use","id":"t1","name":"mcp__malachi__annotate_case","input":{}}],"usage":{"input_tokens":5,"output_tokens":1,"cache_creation_input_tokens":10,"cache_read_input_tokens":100}},"parent_tool_use_id":null}`
	result := `{"type":"result","subtype":"success","is_error":false,"result":"ok","usage":{"input_tokens":40,"output_tokens":900,"cache_creation_input_tokens":1200,"cache_read_input_tokens":50000}}`
	fake := newFakeClaude(t, "true", "", fakeTurn{lines: []string{fakeInit, msg, fakeToolResult("t1", "ok", false), result}})
	h := newRequestHarness(t, fake, true, true)
	var rec toolsRequest
	var order []string
	c := rec.call(Call{SystemPrompt: "S", Message: "m", Tools: triageTools})
	onUsage, onTool := c.OnUsage, c.OnTool
	c.OnUsage = func(e assistant.Event) { order = append(order, "usage"); onUsage(e) }
	c.OnTool = func(e assistant.Event) { order = append(order, "tool"); onTool(e) }
	h.request.StartCall(c, rec.done)
	h.loop.runUntil(t, func() bool { return len(rec.outcomes) > 0 })
	if len(rec.usage) != 2 || rec.usage[0].MessageID != "m1" || rec.usage[1].Kind != assistant.EventResult {
		t.Fatalf("usage %+v", rec.usage)
	}
	var tally assistant.UsageTally
	for _, e := range rec.usage {
		tally.Add(e)
	}
	if u, ok := tally.Total(); !ok || u != (assistant.Usage{InputTokens: 40, OutputTokens: 900, CacheCreationInputTokens: 1200, CacheReadInputTokens: 50000}) {
		t.Errorf("total %+v %v", u, ok)
	}
	if !slices.Equal(order, []string{"usage", "tool", "tool", "usage"}) {
		t.Errorf("order %q", order)
	}
}
