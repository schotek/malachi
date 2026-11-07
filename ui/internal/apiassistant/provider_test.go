// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package apiassistant

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/assistantpanel"
)

// The provider against a fake daemon: every call recorded, the daemon's
// notifications fed through Sessions as the window feeds them.

// fakeDaemon records the calls and answers them; hold keeps the answers
// of a method until released.
type fakeDaemon struct {
	mu       sync.Mutex
	calls    []call
	failures map[string]error
	nextID   int
	tools    []string
	// updated is the config of the last account.update.
	updated *api.AccountUpdateParams
}

type call struct {
	method string
	params any
}

func (d *fakeDaemon) Call(_ context.Context, method string, params any, result any) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, call{method, params})
	if err := d.failures[method]; err != nil {
		return err
	}
	switch method {
	case api.MethodAssistantSessionStart:
		d.nextID++
		*result.(*api.AssistantSessionStartResult) = api.AssistantSessionStartResult{SessionID: "as_" + string(rune('0'+d.nextID)), Tools: d.tools}
	case api.MethodAccountUpdate:
		p := params.(api.AccountUpdateParams)
		d.updated = &p
	}
	return nil
}

func (d *fakeDaemon) methods() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, len(d.calls))
	for i, c := range d.calls {
		out[i] = c.method
	}
	return out
}

func (d *fakeDaemon) last(method string) (any, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := len(d.calls) - 1; i >= 0; i-- {
		if d.calls[i].method == method {
			return d.calls[i].params, true
		}
	}
	return nil, false
}

// waitFor waits until the daemon saw a call of method.
func (d *fakeDaemon) waitFor(t *testing.T, method string) any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if p, ok := d.last(method); ok {
			return p
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no %s call", method)
	return nil
}

type testLoop struct{}

func (testLoop) Post(f func())                   { f() }
func (testLoop) After(_ time.Duration, f func()) { f() }

func aiAccount(consented bool) api.Account {
	a := api.Account{ID: "acc_ai", Enabled: true, Config: api.AccountConfig{
		Name: "Claude API", Kind: api.AccountAI,
		AI: &api.AIConfig{Provider: api.AIAnthropic, Model: "claude-sonnet-5"},
	}}
	if consented {
		a.Config.AI.Consent = &api.AIConsent{Version: api.AssistantConsentVersion}
	}
	return a
}

// recorder collects what a session delivers.
type recorder struct {
	mu     sync.Mutex
	events []assistant.Event
	exits  []assistantpanel.Exit
}

func (r *recorder) attach(s assistantpanel.Session) {
	s.SetHandlers(func(events []assistant.Event) {
		r.mu.Lock()
		r.events = append(r.events, events...)
		r.mu.Unlock()
	}, func(e assistantpanel.Exit) {
		r.mu.Lock()
		r.exits = append(r.exits, e)
		r.mu.Unlock()
	})
}

func (r *recorder) kinds() []assistant.EventKind {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]assistant.EventKind, len(r.events))
	for i, e := range r.events {
		out[i] = e.Kind
	}
	return out
}

// notify feeds a notify.assistantEvent through the registry as the
// window's dispatcher does.
func notify(t *testing.T, reg *Sessions, e api.AssistantEvent) {
	t.Helper()
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if !reg.Handle(api.NotifyAssistantEvent, raw) {
		t.Fatal("the event was not taken")
	}
}

func newProvider(t *testing.T, d *fakeDaemon, consented bool) (*Provider, *Sessions) {
	t.Helper()
	reg := NewSessions()
	return New(d, testLoop{}, aiAccount(consented), reg, nil), reg
}

// The account's name, model and consent; a session of the panel: its
// start parameters, two turns, a tool call and the usage, every event
// as the panel reads it; Terminate stops it.
func TestPanelConversation(t *testing.T) {
	d := &fakeDaemon{tools: []string{"list_messages", "read_message"}}
	p, reg := newProvider(t, d, true)
	if p.Name() != "Claude API" || p.Model() != "claude-sonnet-5" || !p.HasConsent() {
		t.Fatalf("name %q, model %q, consent %v", p.Name(), p.Model(), p.HasConsent())
	}
	spec := assistantpanel.SessionSpec{
		Purpose: api.PurposePanel, Language: "cs", BridgePath: "/b/malachi-mcp",
		SystemPrompt: "ignored: the daemon builds it", Tools: &assistantpanel.Tools{Bridge: "/b/malachi-mcp"},
		Timeout: 90 * time.Second,
	}
	s, err := p.Open(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	start := d.waitFor(t, api.MethodAssistantSessionStart).(api.AssistantSessionStartParams)
	if start != (api.AssistantSessionStartParams{AccountID: "acc_ai", Purpose: api.PurposePanel, Language: "cs", Bridge: "/b/malachi-mcp", TimeoutSeconds: 90}) {
		t.Errorf("sessionStart %+v", start)
	}
	if !s.Running() || reg.Len() != 1 {
		t.Fatal("not running or not registered")
	}
	var rec recorder
	rec.attach(s)

	// Turn 1: a question with its context.
	input := api.PromptInput{Text: "Shrň to.", Context: &api.PromptContext{AccountID: "acc_1", MessageIDs: []api.MessageID{"m_1"}}}
	if err := s.Submit(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	send := d.waitFor(t, api.MethodAssistantSessionSend).(api.AssistantSessionSendParams)
	if send.SessionID != "as_1" || send.Input.Text != input.Text || send.Input.Context == nil || send.Input.Context.AccountID != "acc_1" {
		t.Errorf("sessionSend %+v", send)
	}
	id := s.(*Session).ID()
	notify(t, reg, api.AssistantEvent{SessionID: id, Kind: api.EventInit, Tools: []string{"list_messages", "read_message"}})
	notify(t, reg, api.AssistantEvent{SessionID: id, Kind: api.EventToolUse, Tool: "read_message", ID: "c_1"})
	notify(t, reg, api.AssistantEvent{SessionID: id, Kind: api.EventToolResult, ID: "c_1", Text: "From: someone\n\nHello"})
	notify(t, reg, api.AssistantEvent{SessionID: id, Kind: api.EventUsage, Usage: &api.AssistantUsage{InputTokens: 100, OutputTokens: 5}})
	notify(t, reg, api.AssistantEvent{SessionID: id, Kind: api.EventTextDelta, Text: "Hel"})
	notify(t, reg, api.AssistantEvent{SessionID: id, Kind: api.EventTextDelta, Text: "lo"})
	notify(t, reg, api.AssistantEvent{SessionID: id, Kind: api.EventText, Text: "Hello"})
	notify(t, reg, api.AssistantEvent{SessionID: id, Kind: api.EventUsage, Usage: &api.AssistantUsage{InputTokens: 300, OutputTokens: 20, CacheReadInputTokens: 50}})
	notify(t, reg, api.AssistantEvent{SessionID: id, Kind: api.EventResult, Result: &api.AssistantResult{Success: true, Reason: "endTurn"}})
	// Another session's event is not this one's.
	notify(t, reg, api.AssistantEvent{SessionID: "as_other", Kind: api.EventText, Text: "not mine"})

	want := []assistant.EventKind{assistant.EventInit, assistant.EventToolUse, assistant.EventToolResult, assistant.EventOther,
		assistant.EventTextDelta, assistant.EventTextDelta, assistant.EventText, assistant.EventOther, assistant.EventResult}
	if !slices.Equal(rec.kinds(), want) {
		t.Fatalf("events %v, want %v", rec.kinds(), want)
	}
	ev := rec.events
	if !ev[0].BridgeConnected || !slices.Equal(ev[0].Tools, []string{"list_messages", "read_message"}) {
		t.Errorf("init %+v", ev[0])
	}
	if ev[1].Tool != "read_message" || ev[1].ToolUseID != "c_1" || ev[2].ToolUseID != "c_1" || ev[2].IsError || ev[2].ResultText != "From: someone\n\nHello" {
		t.Errorf("tool %+v %+v", ev[1], ev[2])
	}
	if ev[3].Usage == nil || !ev[3].UsageFinal || ev[3].MessageID != id+":1" || ev[7].MessageID != id+":1" ||
		*ev[7].Usage != (assistant.Usage{InputTokens: 300, OutputTokens: 20, CacheReadInputTokens: 50}) {
		t.Errorf("usage %+v %+v", ev[3], ev[7])
	}
	var tally assistant.UsageTally
	for _, e := range ev {
		tally.Add(e)
	}
	if u, ok := tally.Total(); !ok || u != (assistant.Usage{InputTokens: 300, OutputTokens: 20, CacheReadInputTokens: 50}) {
		t.Errorf("tally %+v %v", u, ok)
	}
	if !ev[8].Success || ev[8].IsError || ev[8].ResultText != "Hello" || ev[8].Structured != nil {
		t.Errorf("result %+v", ev[8])
	}

	// Turn 2: a follow-up without a context, two text blocks joined.
	if err := s.Submit(context.Background(), api.PromptInput{Text: "A dál?"}); err != nil {
		t.Fatal(err)
	}
	notify(t, reg, api.AssistantEvent{SessionID: id, Kind: api.EventInit})
	notify(t, reg, api.AssistantEvent{SessionID: id, Kind: api.EventText, Text: "One"})
	notify(t, reg, api.AssistantEvent{SessionID: id, Kind: api.EventText, Text: "Two"})
	notify(t, reg, api.AssistantEvent{SessionID: id, Kind: api.EventUsage, Usage: &api.AssistantUsage{InputTokens: 10}})
	notify(t, reg, api.AssistantEvent{SessionID: id, Kind: api.EventResult, Result: &api.AssistantResult{Success: true}})
	ev = rec.events[9:]
	if len(ev) != 5 || !slices.Equal(ev[0].Tools, []string{"list_messages", "read_message"}) || ev[3].MessageID != id+":2" || ev[4].ResultText != "One\nTwo" {
		t.Errorf("turn 2 %+v", ev)
	}
	if len(rec.exits) != 0 || !s.Running() {
		t.Fatal("ended early")
	}

	// Terminate: sessionStop, the exit, Completion closed, unregistered.
	s.Terminate()
	select {
	case <-s.Completion():
	case <-time.After(5 * time.Second):
		t.Fatal("no completion")
	}
	stop := d.waitFor(t, api.MethodAssistantSessionStop).(api.AssistantSessionStopParams)
	if stop.SessionID != id || s.Running() || reg.Len() != 0 || len(rec.exits) != 1 || rec.exits[0].Reason != "api_stopped" {
		t.Errorf("after Terminate: stop %+v, running %v, open %d, exits %+v", stop, s.Running(), reg.Len(), rec.exits)
	}
	s.Terminate() // once
	if err := s.Submit(context.Background(), api.PromptInput{Text: "late"}); err == nil || err.Error() != "api_stopped" {
		t.Errorf("Submit after the end: %v", err)
	}
}

// A one-shot rewrite: the note account in the start, the structured
// answer of a search in the result.
func TestOneShot(t *testing.T) {
	d := &fakeDaemon{}
	p, reg := newProvider(t, d, true)
	s, err := p.Open(context.Background(), assistantpanel.SessionSpec{Purpose: api.PurposeRewrite, Language: "cs", NoteAccountID: "acc_1"})
	if err != nil {
		t.Fatal(err)
	}
	start := d.waitFor(t, api.MethodAssistantSessionStart).(api.AssistantSessionStartParams)
	if start.Purpose != api.PurposeRewrite || start.NoteAccountID != "acc_1" || start.Bridge != "" || start.TimeoutSeconds != 0 {
		t.Errorf("sessionStart %+v", start)
	}
	var rec recorder
	rec.attach(s)
	in := api.PromptInput{Rewrite: &api.RewriteInput{Preset: api.RewriteShorter, Passage: "Dobrý den, posílám fakturu."}}
	if err := s.Submit(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	send := d.waitFor(t, api.MethodAssistantSessionSend).(api.AssistantSessionSendParams)
	if send.Input.Rewrite == nil || send.Input.Rewrite.Preset != api.RewriteShorter {
		t.Errorf("sessionSend %+v", send)
	}
	id := s.(*Session).ID()
	notify(t, reg, api.AssistantEvent{SessionID: id, Kind: api.EventInit})
	notify(t, reg, api.AssistantEvent{SessionID: id, Kind: api.EventText, Text: `{"query":"faktur"}`})
	notify(t, reg, api.AssistantEvent{SessionID: id, Kind: api.EventResult, Result: &api.AssistantResult{Success: true, Structured: json.RawMessage(`{"query":"faktur"}`)}})
	last := rec.events[len(rec.events)-1]
	if last.Kind != assistant.EventResult || !last.Success || last.ResultText != `{"query":"faktur"}` || string(last.Structured) != `{"query":"faktur"}` {
		t.Errorf("result %+v", last)
	}
	// A triage's start names its trigger and run.
	if _, err := p.Open(context.Background(), assistantpanel.SessionSpec{Purpose: api.PurposeTriage, Trigger: api.TriggerAuto, RunID: "run_1", BridgePath: "/b", Timeout: 15 * time.Minute}); err != nil {
		t.Fatal(err)
	}
	start = d.waitFor(t, api.MethodAssistantSessionStart).(api.AssistantSessionStartParams)
	if start.Trigger != api.TriggerAuto || start.RunID != "run_1" || start.TimeoutSeconds != 900 {
		t.Errorf("triage start %+v", start)
	}
}

// A failure that ends only the turn is a failed result with its reason;
// one that ends the session ends it: the exit with the reason, Completion
// closed, no sessionStop.
func TestFailures(t *testing.T) {
	d := &fakeDaemon{}
	p, reg := newProvider(t, d, true)
	s, err := p.Open(context.Background(), assistantpanel.SessionSpec{Purpose: api.PurposePanel, BridgePath: "/b"})
	if err != nil {
		t.Fatal(err)
	}
	var rec recorder
	rec.attach(s)
	id := s.(*Session).ID()
	_ = s.Submit(context.Background(), api.PromptInput{Text: "x"})
	notify(t, reg, api.AssistantEvent{SessionID: id, Kind: api.EventInit})
	notify(t, reg, api.AssistantEvent{SessionID: id, Kind: api.EventTextDelta, Text: "par"})
	notify(t, reg, api.AssistantEvent{SessionID: id, Kind: api.EventFailure, Failure: &api.AssistantFailure{Class: api.FailureRateLimit, RetryAfter: 7, Message: "429"}})
	ev := rec.events
	if len(ev) != 4 || ev[2].Kind != assistant.EventFailure || ev[2].Failure != "api_rateLimit" ||
		ev[3].Kind != assistant.EventResult || ev[3].Success || !ev[3].IsError || ev[3].ResultText != "api_rateLimit" {
		t.Fatalf("events %+v", ev)
	}
	if !s.Running() || len(rec.exits) != 0 {
		t.Fatal("a rate limit ended the session")
	}
	if f := assistantpanel.ProviderFailure(ev[3].ResultText); f.Kind != assistantpanel.FailureLimit {
		t.Errorf("the panel reads %+v", f)
	}
	// The next turn fails for the key: the session is over.
	_ = s.Submit(context.Background(), api.PromptInput{Text: "y"})
	notify(t, reg, api.AssistantEvent{SessionID: id, Kind: api.EventFailure, Failure: &api.AssistantFailure{Class: api.FailureAuth, Message: "401"}})
	select {
	case <-s.Completion():
	case <-time.After(5 * time.Second):
		t.Fatal("no completion")
	}
	if s.Running() || len(rec.exits) != 1 || rec.exits[0].Reason != "api_auth" || reg.Len() != 0 {
		t.Errorf("after auth: running %v, exits %+v, open %d", s.Running(), rec.exits, reg.Len())
	}
	if last := rec.events[len(rec.events)-1]; last.Kind != assistant.EventResult || last.ResultText != "api_auth" {
		t.Errorf("last event %+v", last)
	}
	if f := assistantpanel.ProviderFailure(rec.exits[0].Reason); f.Kind != assistantpanel.FailureNotSignedIn {
		t.Errorf("the panel reads %+v", f)
	}
	if slices.Contains(d.methods(), api.MethodAssistantSessionStop) {
		t.Error("sessionStop after the daemon ended the session")
	}
	// Every class that ends the session, and the ones that do not.
	for class, ends := range map[api.AssistantFailureClass]bool{
		api.FailureAuth: true, api.FailurePermission: true, api.FailureToolsUnsupported: true, api.FailureContextFull: true,
		api.FailureBridge: true, api.FailureStopped: true,
		api.FailureRateLimit: false, api.FailureOverloaded: false, api.FailureInvalidRequest: false, api.FailureNetwork: false,
		api.FailureProtocol: false, api.FailureTimeout: false, api.FailureLimit: false,
	} {
		if endsSession(class) != ends {
			t.Errorf("%s ends the session: %v", class, endsSession(class))
		}
	}
}

// The daemon's refusals of a start and of a turn as the panel reads them.
func TestDaemonErrors(t *testing.T) {
	d := &fakeDaemon{failures: map[string]error{}}
	p, _ := newProvider(t, d, true)
	for code, kind := range map[api.ErrorCode]assistantpanel.FailureKind{
		api.CodeAssistantConsentRequired:  assistantpanel.FailureNotSignedIn,
		api.CodeAuthRequired:              assistantpanel.FailureNotSignedIn,
		api.CodeAssistantToolsUnsupported: assistantpanel.FailureToolsMissing,
		api.CodeAssistantUnavailable:      assistantpanel.FailureToolsMissing,
		api.CodeInvalidArgument:           assistantpanel.FailureStopped,
	} {
		d.failures[api.MethodAssistantSessionStart] = api.NewError(code, "refused %d", int(code))
		_, err := p.Open(context.Background(), assistantpanel.SessionSpec{Purpose: api.PurposePanel})
		if err == nil {
			t.Fatalf("%d: no error", code)
		}
		if f := assistantpanel.ProviderFailure(err.Error()); f.Kind != kind {
			t.Errorf("%d: %q read as %+v", code, err, f)
		}
	}
	// A plain error passes as it is.
	d.failures[api.MethodAssistantSessionStart] = errors.New("connection to malachid lost")
	if _, err := p.Open(context.Background(), assistantpanel.SessionSpec{Purpose: api.PurposePanel}); err == nil || err.Error() != "connection to malachid lost" {
		t.Errorf("plain error %v", err)
	}
	delete(d.failures, api.MethodAssistantSessionStart)
	// A turn the daemon cannot find the session of ends it; a busy one
	// does not.
	s, err := p.Open(context.Background(), assistantpanel.SessionSpec{Purpose: api.PurposePanel})
	if err != nil {
		t.Fatal(err)
	}
	d.failures[api.MethodAssistantSessionSend] = api.NewError(api.CodeAssistantSessionBusy, "busy")
	if err := s.Submit(context.Background(), api.PromptInput{Text: "x"}); err == nil || !s.Running() {
		t.Errorf("busy: %v, running %v", err, s.Running())
	}
	d.failures[api.MethodAssistantSessionSend] = api.NewError(api.CodeAssistantSessionNotFound, "gone")
	if err := s.Submit(context.Background(), api.PromptInput{Text: "x"}); err == nil || err.Error() != "api_stopped" || s.Running() {
		t.Errorf("gone: %v, running %v", err, s.Running())
	}
}

// Without consent nothing starts; AcceptConsent is account.update with
// the config as stored and the consent's version, which Open waits for.
func TestConsent(t *testing.T) {
	d := &fakeDaemon{}
	p, _ := newProvider(t, d, false)
	if p.HasConsent() {
		t.Fatal("consent without one")
	}
	if _, err := p.Open(context.Background(), assistantpanel.SessionSpec{Purpose: api.PurposePanel}); err == nil || err.Error() != "api_consent_required" {
		t.Fatalf("open without consent: %v", err)
	}
	if len(d.methods()) != 0 {
		t.Fatal("the daemon was called")
	}
	p.AcceptConsent()
	if !p.HasConsent() {
		t.Fatal("no consent after AcceptConsent")
	}
	if _, err := p.Open(context.Background(), assistantpanel.SessionSpec{Purpose: api.PurposePanel}); err != nil {
		t.Fatal(err)
	}
	// The update came before the start, with the stored config and the
	// consent's version, and no credentials.
	methods := d.methods()
	if !slices.Equal(methods, []string{api.MethodAccountUpdate, api.MethodAssistantSessionStart}) {
		t.Fatalf("calls %q", methods)
	}
	u := d.updated
	if u == nil || u.AccountID != "acc_ai" || u.Credentials != (api.Credentials{}) || u.Config.Name != "Claude API" || u.Config.AI == nil ||
		u.Config.AI.Model != "claude-sonnet-5" || u.Config.AI.Provider != api.AIAnthropic || u.Config.AI.Consent == nil ||
		u.Config.AI.Consent.Version != api.AssistantConsentVersion || u.Config.AI.Consent.AcceptedAt != nil {
		t.Errorf("account.update %+v", u)
	}
	// A refused update fails the session that waited for it.
	q := &fakeDaemon{failures: map[string]error{api.MethodAccountUpdate: api.NewError(api.CodeInvalidArgument, "no model")}}
	p2, _ := newProvider(t, q, false)
	p2.AcceptConsent()
	if _, err := p2.Open(context.Background(), assistantpanel.SessionSpec{Purpose: api.PurposePanel}); err == nil || err.Error() != "no model" {
		t.Errorf("open after a refused consent: %v", err)
	}
}

// The connection went: every open session ends with "api_stopped" and no
// sessionStop is sent; other notifications are not the registry's.
func TestDisconnected(t *testing.T) {
	d := &fakeDaemon{}
	p, reg := newProvider(t, d, true)
	s, err := p.Open(context.Background(), assistantpanel.SessionSpec{Purpose: api.PurposePanel})
	if err != nil {
		t.Fatal(err)
	}
	var rec recorder
	rec.attach(s)
	if reg.Handle(api.NotifyNewMessage, json.RawMessage(`{}`)) || reg.Handle(api.NotifyAssistantEvent, json.RawMessage(`not json`)) != true {
		t.Error("the registry took another method, or refused bad JSON")
	}
	reg.Disconnected()
	select {
	case <-s.Completion():
	case <-time.After(5 * time.Second):
		t.Fatal("no completion")
	}
	if reg.Len() != 0 || len(rec.exits) != 1 || rec.exits[0].Reason != "api_stopped" || slices.Contains(d.methods(), api.MethodAssistantSessionStop) {
		t.Errorf("after the disconnect: open %d, exits %+v, calls %q", reg.Len(), rec.exits, d.methods())
	}
	// A name falls back to the address, then the id; no model without
	// an AIConfig.
	a := aiAccount(true)
	a.Config.Name, a.Config.Email = "", "key@example.org"
	if n := New(d, testLoop{}, a, nil, nil).Name(); n != "key@example.org" {
		t.Errorf("name %q", n)
	}
	a.Config.Email, a.Config.AI = "", nil
	q := New(d, testLoop{}, a, nil, nil)
	if q.Name() != "acc_ai" || q.Model() != "" || q.HasConsent() {
		t.Errorf("name %q, model %q, consent %v", q.Name(), q.Model(), q.HasConsent())
	}
	q.AcceptConsent() // nothing to write to
	if len(d.methods()) != 1 {
		t.Errorf("calls %q", d.methods())
	}
}
