// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistantpanel

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/ui/internal/assistant"
)

type fakeProvider struct {
	consent  bool
	specs    chan SessionSpec
	sessions chan *fakeSession
	gate     chan struct{}
}

func (p *fakeProvider) Model() string    { return "fixture-model" }
func (p *fakeProvider) HasConsent() bool { return p.consent }
func (p *fakeProvider) AcceptConsent()   { p.consent = true }
func (p *fakeProvider) Open(ctx context.Context, spec SessionSpec) (Session, error) {
	p.specs <- spec
	s := &fakeSession{done: make(chan struct{}), sent: make(chan string, 1)}
	p.sessions <- s
	if p.gate != nil {
		select {
		case <-p.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s, nil
}
func newFakeProvider() *fakeProvider {
	return &fakeProvider{consent: true, specs: make(chan SessionSpec, 10), sessions: make(chan *fakeSession, 10)}
}

type fakeSession struct {
	mu     sync.Mutex
	events func([]assistant.Event)
	exit   func(Exit)
	done   chan struct{}
	sent   chan string
	ended  bool
}

func (s *fakeSession) Running() bool                                  { s.mu.Lock(); defer s.mu.Unlock(); return !s.ended }
func (s *fakeSession) Submit(_ context.Context, message string) error { s.sent <- message; return nil }
func (s *fakeSession) SetHandlers(events func([]assistant.Event), exit func(Exit)) {
	s.mu.Lock()
	s.events, s.exit = events, exit
	s.mu.Unlock()
}
func (s *fakeSession) Terminate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ended {
		s.ended = true
		close(s.done)
	}
}
func (s *fakeSession) Completion() <-chan struct{} { return s.done }
func (s *fakeSession) emit(e assistant.Event) {
	s.mu.Lock()
	fn := s.events
	s.mu.Unlock()
	if fn != nil {
		fn([]assistant.Event{e})
	}
}
func TestProviderRequestCallbacksAndCancellation(t *testing.T) {
	loop := newTestLoop()
	provider := newFakeProvider()
	r := NewRequest(RequestConfig{Provider: func() Provider { return provider }, Settings: &memSettings{}, Loop: loop, Log: discardLog()})
	calls := 0
	gotText := ""
	r.StartCall(Call{SystemPrompt: "fixture", Message: "input", ModelID: "board-model", OnText: func(text string) { gotText = text }}, func(o Outcome) {
		calls++
		if o.Kind != OutcomeAnswered || o.Text != "answer" {
			t.Fatal(o)
		}
	})
	loop.runUntil(t, func() bool { return r.providerSession != nil })
	s := <-provider.sessions
	spec := <-provider.specs
	if spec.ModelID != "board-model" || spec.Tools != nil {
		t.Fatal(spec)
	}
	go s.emit(assistant.Event{Kind: assistant.EventTextDelta, Text: "answer"})
	loop.runUntil(t, func() bool { return gotText == "answer" })
	if calls != 0 {
		t.Fatal("delta finished")
	}
	go s.emit(assistant.Event{Kind: assistant.EventResult, Success: true, ResultText: "answer"})
	loop.runUntil(t, func() bool { return calls == 1 })
	if s.Running() {
		t.Fatal("oneshot profile retained")
	}
	r.Start("fixture", "input2", "", nil, func(Outcome) { calls++ })
	loop.runUntil(t, func() bool { return r.providerSession != nil })
	s = <-provider.sessions
	s.mu.Lock()
	stale := s.events
	s.mu.Unlock()
	r.Cancel()
	stale([]assistant.Event{{Kind: assistant.EventResult, Success: true}})
	loop.settle(t, 20*time.Millisecond)
	if calls != 1 || r.Running() {
		t.Fatal("late callback completed canceled generation")
	}
}
func TestProviderRequestPolicySnapshotBeforeConsent(t *testing.T) {
	loop := newTestLoop()
	provider := newFakeProvider()
	provider.consent = false
	selected := provider
	other := newFakeProvider()
	other.consent = false
	r := NewRequest(RequestConfig{Provider: func() Provider { return selected }, Settings: &memSettings{}, Loop: loop, Log: discardLog()})
	var answer func(bool)
	r.Consent = func(done func(bool)) { answer = done }
	tools := &Tools{BridgeArgs: []string{"--reply-only", "m_1"}, Allowed: append([]string{}, assistant.SuggestReplyTools...)}
	r.StartCall(Call{SystemPrompt: "fixture", Tools: tools}, func(Outcome) {})
	loop.runUntil(t, func() bool { return answer != nil })
	tools.BridgeArgs[0] = "--allow-send"
	tools.Allowed[0] = "mcp__malachi__send_message"
	oldAnswer := answer
	selected = other
	r.Start("next", "text", "", nil, func(Outcome) {})
	oldAnswer(true)
	if !provider.consent || other.consent {
		t.Fatal("late consent leaked to changed provider")
	}
	r.Cancel()
}
func TestProviderChangedEndsPanelAndStaleEvents(t *testing.T) {
	loop := newTestLoop()
	provider := newFakeProvider()
	c := New(Config{Provider: func() Provider { return provider }, Translator: tr, Settings: &memSettings{}, Loop: loop, Log: discardLog(), Bridge: "/fixture/malachi-mcp"})
	defer c.Close()
	if !c.Submit("input") {
		t.Fatal("submission refused")
	}
	loop.runUntil(t, func() bool { return c.providerSession != nil })
	s := <-provider.sessions
	s.mu.Lock()
	stale := s.events
	s.mu.Unlock()
	go s.emit(assistant.Event{Kind: assistant.EventTextDelta, Text: "answer"})
	loop.runUntil(t, func() bool { return len(c.Items()) == 2 })
	c.ProviderChanged()
	if s.Running() || c.Running() || len(c.Items()) != 0 {
		t.Fatal("provider change retained old conversation")
	}
	stale([]assistant.Event{{Kind: assistant.EventText, Text: "late"}, {Kind: assistant.EventResult, Success: true}})
	loop.settle(t, 20*time.Millisecond)
	if len(c.Items()) != 0 {
		t.Fatal("stale provider events displayed")
	}
}
func TestProviderOpenCanceledBeforeDelivery(t *testing.T) {
	loop := newTestLoop()
	provider := newFakeProvider()
	provider.gate = make(chan struct{})
	r := NewRequest(RequestConfig{Provider: func() Provider { return provider }, Settings: &memSettings{}, Loop: loop, Log: discardLog()})
	completed := false
	r.Start("fixture", "input", "", nil, func(Outcome) { completed = true })
	loop.runUntil(t, func() bool { return len(provider.sessions) > 0 })
	s := <-provider.sessions
	r.Cancel()
	close(provider.gate)
	loop.settle(t, 30*time.Millisecond)
	if completed || r.providerSession != nil {
		t.Fatal("canceled initialization installed a session")
	}
	_ = s
}

func TestSubtitleFollowsProviderBeforeFirstQuestion(t *testing.T) {
	provider := newFakeProvider()
	var selected Provider = provider
	c := New(Config{Provider: func() Provider { return selected }, Translator: tr, Settings: &memSettings{}, Loop: newTestLoop(), Log: discardLog()})
	defer c.Close()
	if got := c.Subtitle(); got != "ChatGPT (Codex, experimental) · fixture-model" {
		t.Fatal(got)
	}
	selected = nil
	c.ProviderChanged()
	if got := c.Subtitle(); got != "Claude Code · Sonnet" {
		t.Fatal(got)
	}
}
