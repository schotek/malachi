// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package boardtriage

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/assistantpanel"
	"github.com/schotek/malachi/ui/internal/board"
)

// The board's triage through a provider other than Claude Code (ChatGPT
// over Codex): a scripted session that emits what ui/internal/chatgpt's
// session emits (EventInit, a tool call and its result per bridge call,
// each response's whole usage from the gateway, then the turn's result or
// a failure code). The real session against a fake codex app-server is
// codex_linux_test.go.

// scriptedProvider opens sessions that play script on Submit.
type scriptedProvider struct {
	script  func(s *scriptedSession)
	openErr error
	mu      sync.Mutex
	specs   []assistantpanel.SessionSpec
}

func (p *scriptedProvider) Model() string    { return "fixture-model" }
func (p *scriptedProvider) HasConsent() bool { return true }
func (p *scriptedProvider) AcceptConsent()   {}
func (p *scriptedProvider) Open(_ context.Context, spec assistantpanel.SessionSpec) (assistantpanel.Session, error) {
	p.mu.Lock()
	p.specs = append(p.specs, spec)
	p.mu.Unlock()
	if p.openErr != nil {
		return nil, p.openErr
	}
	return &scriptedSession{script: p.script, done: make(chan struct{})}, nil
}

type scriptedSession struct {
	script func(s *scriptedSession)
	mu     sync.Mutex
	events func([]assistant.Event)
	exit   func(assistantpanel.Exit)
	done   chan struct{}
	ended  bool
}

func (s *scriptedSession) Running() bool { s.mu.Lock(); defer s.mu.Unlock(); return !s.ended }
func (s *scriptedSession) Submit(context.Context, string) error {
	go s.script(s)
	return nil
}
func (s *scriptedSession) Terminate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ended {
		s.ended = true
		close(s.done)
	}
}
func (s *scriptedSession) Completion() <-chan struct{} { return s.done }
func (s *scriptedSession) SetHandlers(events func([]assistant.Event), exit func(assistantpanel.Exit)) {
	s.mu.Lock()
	s.events, s.exit = events, exit
	s.mu.Unlock()
}

// emit sends e as the session does, unless it was terminated.
func (s *scriptedSession) emit(e assistant.Event) {
	s.mu.Lock()
	fn, ended := s.events, s.ended
	s.mu.Unlock()
	if fn != nil && !ended {
		fn([]assistant.Event{e})
	}
}

// call is one bridge call: its use and its result.
func (s *scriptedSession) call(id, tool, result string, isError bool) {
	s.emit(assistant.Event{Kind: assistant.EventToolUse, Tool: tool, ToolUseID: id})
	s.emit(assistant.Event{Kind: assistant.EventToolResult, ToolUseID: id, ResultText: result, IsError: isError})
}

// response is one Responses API response's whole usage, as the gateway
// reports it.
func (s *scriptedSession) response(id string, input, output, cached int64) {
	s.emit(assistant.Event{Kind: assistant.EventOther, MessageID: id, UsageFinal: true,
		Usage: &assistant.Usage{InputTokens: input, OutputTokens: output, CacheReadInputTokens: cached}})
}

func (s *scriptedSession) result(success bool, text string) {
	s.emit(assistant.Event{Kind: assistant.EventResult, Success: success, IsError: !success, ResultText: text})
}

func TestCodexRunCountsDistinctCasesAndWholeUsage(t *testing.T) {
	p := &scriptedProvider{script: func(s *scriptedSession) {
		s.emit(assistant.Event{Kind: assistant.EventInit, BridgeConnected: true})
		s.response("resp_1", 1000, 40, 200)
		s.call("call_1", "list_triage_queue", "case c_1 …", false)
		s.call("call_2", assistant.TriageAnnotateTool, "annotated case c_1: state in effect you", false)
		s.response("resp_2", 1200, 60, 900)
		// The same case again: the bridge takes it without another slot.
		s.call("call_3", assistant.TriageAnnotateTool, "annotated case c_1: state in effect hot", false)
		s.call("call_4", assistant.TriageAnnotateTool, "conflict: read the queue again", true)
		s.call("call_5", assistant.TriageAnnotateTool, "annotated case c_2: state in effect info", false)
		s.response("resp_3", 1300, 20, 1000)
		s.result(true, "Annotated 2 cases.")
	}}
	h := newHarness(t, newFakeClaude(t, "true"), options{provider: p})
	h.c.Source = func() string { return "malachi-chatgpt" }
	h.board(5, true, 0, nil)
	if !h.c.Start(Manual, 0) {
		t.Fatal("not started")
	}
	h.ended()
	if h.hasState(Running(Manual, 3, 5)) {
		t.Errorf("a case counted twice: %+v", h.states)
	}
	h.expectState("ended", Finished(Manual, 2, 1, t0))
	// Every response reported its whole usage and the turn completed: no
	// lower bound.
	h.expectEnds("usage", end("run_1", "", &api.BoardUsage{InputTokens: 3500, OutputTokens: 120, CacheReadInputTokens: 2100}))
	if f := h.fake.starts(); f != 0 {
		t.Errorf("claude started %d times", f)
	}
	if len(p.specs) != 1 || p.specs[0].Tools == nil || p.specs[0].Tools.BridgeArgs[0] != "--allow-triage" {
		t.Errorf("specs %+v", p.specs)
	}
}

// Two accepted notes on one case are one case done, also at the run's
// limit.
func TestReannotationCountsOnce(t *testing.T) {
	p := &scriptedProvider{script: func(s *scriptedSession) {
		s.emit(assistant.Event{Kind: assistant.EventInit, BridgeConnected: true})
		s.call("call_1", assistant.TriageAnnotateTool, "annotated case c_1: x", false)
		s.call("call_2", assistant.TriageAnnotateTool, "annotated case c_1: y", false)
		s.result(true, "done")
	}}
	h := newHarness(t, newFakeClaude(t, "true"), options{provider: p})
	h.board(5, true, 0, nil)
	h.c.Start(Manual, 2)
	h.ended()
	h.expectState("ended", Finished(Manual, 1, 0, t0))
}

// A run stopped before the turn completed has only part of its usage.
func TestCodexStoppedRunIsALowerBound(t *testing.T) {
	release := make(chan struct{})
	p := &scriptedProvider{script: func(s *scriptedSession) {
		s.emit(assistant.Event{Kind: assistant.EventInit, BridgeConnected: true})
		s.response("resp_1", 10, 2, 0)
		s.call("call_1", assistant.TriageAnnotateTool, "annotated case c_1: x", false)
		<-release
	}}
	defer close(release)
	h := newHarness(t, newFakeClaude(t, "true"), options{provider: p})
	h.board(5, true, 0, nil)
	h.c.Start(Manual, 0)
	h.loop.runUntil(t, func() bool { return h.c.State().Equal(Running(Manual, 1, 5)) })
	h.c.Cancel()
	h.ended()
	h.expectEnds("stopped", end("run_1", api.RunCancelled, atLeast(&api.BoardUsage{InputTokens: 10, OutputTokens: 2})))
}

// The provider's failure codes are the board's classes: the plan's usage
// limit, a lapsed connection (the sign-in offer), Codex missing.
func TestCodexFailureClasses(t *testing.T) {
	failing := func(code string) *scriptedProvider {
		return &scriptedProvider{script: func(s *scriptedSession) {
			s.emit(assistant.Event{Kind: assistant.EventInit, BridgeConnected: true})
			s.response("resp_1", 10, 0, 0)
			s.result(false, code)
		}}
	}
	tests := []struct {
		name    string
		p       *scriptedProvider
		failure board.TriageFailure
		class   api.BoardRunError
		usage   *api.BoardUsage
	}{
		{"usage limit", failing("chatgpt_usage_limit"), board.FailLimit, api.RunFailed, atLeast(&api.BoardUsage{InputTokens: 10})},
		{"reconnect", failing("chatgpt_reconnect_required"), board.FailNotSignedIn, api.RunSignedOut, atLeast(&api.BoardUsage{InputTokens: 10})},
		{"other", failing("chatgpt_turn_failed"), board.FailStopped, api.RunFailed, atLeast(&api.BoardUsage{InputTokens: 10})},
		{"not connected at open", &scriptedProvider{openErr: errors.New("chatgpt_not_connected")}, board.FailNotSignedIn, api.RunSignedOut, nil},
		{"codex missing", &scriptedProvider{openErr: errors.New("codex_not_found")}, board.FailNotFound, api.RunFailed, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, newFakeClaude(t, "true"), options{provider: tt.p})
			h.board(5, true, 0, nil)
			h.c.Start(Automatic, 3)
			h.ended()
			h.expectState("ended", Failed(Automatic, tt.failure, t0))
			h.expectEnds("end", end("run_1", tt.class, tt.usage))
			if tt.failure == board.FailNotSignedIn && h.c.SignedIn() != (assistantpanel.SignIn{Known: true}) {
				t.Errorf("signed in %+v, want known signed out", h.c.SignedIn())
			}
		})
	}
}
