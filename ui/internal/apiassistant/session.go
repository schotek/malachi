// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package apiassistant

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/assistantpanel"
)

// Session is one session of the daemon's assistant (assistant.sessionStart
// … sessionStop) as the panel's protocol sees it. The daemon's events of
// the session (Handle, from Sessions) become assistant.Events:
//
//   - init: EventInit with the bridge connected and the tools;
//   - textDelta, text: EventTextDelta, EventText (the text blocks of the
//     turn are kept for the result);
//   - toolUse, toolResult: EventToolUse (the tool, the call's id),
//     EventToolResult (the id, whether it failed, the bridge's text);
//   - usage: EventOther with the usage so far as the whole, final usage
//     of the turn's message (its id is the session's and the turn's
//     number, so that assistant.UsageTally keeps the last value);
//   - result: EventResult with the success, the turn's text blocks
//     joined by a newline and the structured answer;
//   - failure: EventFailure with the reason "api_<class>", and, as the
//     daemon ends the turn with it, an EventResult that failed with that
//     reason (what the panel and the requests read); a class that ends
//     the session (auth, permission, toolsUnsupported, contextFull,
//     bridge, stopped) ends it here too: Exit with that reason, and
//     Completion closes.
//
// Terminate is assistant.sessionStop (in the background; Sessions.Wait
// waits for it), an Exit of "api_stopped" and Completion closed;
// Disconnected (the connection went) the same without the call. Handlers may run on the dispatcher's
// goroutine; the controllers marshal them through their Loop.
type Session struct {
	p     *Provider
	id    string
	tools []string

	mu     sync.Mutex
	events func([]assistant.Event)
	exit   func(assistantpanel.Exit)
	done   chan struct{}
	ended  bool
	// sent: the first turn went with assistant.sessionStart (a one-shot
	// purpose's input), so the first Submit sends nothing.
	sent bool
	// turn counts the turns for the usage's message id; blocks are the
	// text blocks of the turn under way.
	turn   int
	blocks []string
	// deliverMu keeps the events and the exit in order; queued and
	// queuedExit hold what arrived before SetHandlers (a turn sent with
	// the start answers before the caller attached), delivered then.
	deliverMu  sync.Mutex
	queued     []assistant.Event
	queuedExit *assistantpanel.Exit
}

func newSession(p *Provider, id string, tools []string, sent bool) *Session {
	s := &Session{p: p, id: id, tools: append([]string{}, tools...), done: make(chan struct{}), sent: sent}
	if sent {
		s.turn = 1
	}
	return s
}

// ID is the daemon's session id.
func (s *Session) ID() string { return s.id }

// Tools are the bare tools the daemon gave the model.
func (s *Session) Tools() []string { return append([]string{}, s.tools...) }

// Running says whether the session is open.
func (s *Session) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.ended
}

// Submit sends one turn (assistant.sessionSend). The daemon's refusal is
// returned as the reason assistantpanel.ProviderFailure reads: a session
// it no longer has ends this one.
func (s *Session) Submit(ctx context.Context, input api.PromptInput) error {
	if !s.Running() {
		return errors.New(reasonStopped)
	}
	s.mu.Lock()
	if s.sent {
		// The start sent this turn; its events are on their way.
		s.sent = false
		s.mu.Unlock()
		return nil
	}
	s.turn++
	s.blocks = nil
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	var res api.AssistantSessionSendResult
	err := s.p.caller.Call(ctx, api.MethodAssistantSessionSend, api.AssistantSessionSendParams{SessionID: s.id, Input: input}, &res)
	if err == nil {
		return nil
	}
	var e *api.Error
	if errors.As(err, &e) && e.Code == api.CodeAssistantSessionNotFound {
		s.end(reasonStopped, false)
	}
	return failure(err)
}

// Terminate ends the session: assistant.sessionStop in the background
// (its answer is not waited for), the Exit and Completion at once.
func (s *Session) Terminate() {
	s.end(reasonStopped, true)
}

// Disconnected: the connection to the daemon went, and the session with
// it (the daemon ends it itself).
func (s *Session) Disconnected() {
	s.end(reasonStopped, false)
}

// end ends the session once with reason, telling the daemon when stop.
func (s *Session) end(reason string, stop bool) {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.ended = true
	exit := s.exit
	s.mu.Unlock()
	if s.p.sessions != nil {
		s.p.sessions.remove(s)
	}
	if stop {
		var stops *sync.WaitGroup
		if s.p.sessions != nil {
			stops = &s.p.sessions.stops
			stops.Add(1)
		}
		go func() {
			if stops != nil {
				defer stops.Done()
			}
			ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
			defer cancel()
			var res api.AssistantSessionStopResult
			if err := s.p.caller.Call(ctx, api.MethodAssistantSessionStop, api.AssistantSessionStopParams{SessionID: s.id}, &res); err != nil {
				s.p.log.Info("ai assistant: sessionStop", "err", err)
			}
		}()
	}
	s.deliverMu.Lock()
	if exit != nil {
		exit(assistantpanel.Exit{Status: 1, Reason: reason})
	} else {
		s.queuedExit = &assistantpanel.Exit{Status: 1, Reason: reason}
	}
	s.deliverMu.Unlock()
	close(s.done)
}

// Completion closes when the session ended.
func (s *Session) Completion() <-chan struct{} { return s.done }

// SetHandlers sets who gets the events and the exit (nil: nobody), and
// hands them what arrived while nobody did, in order.
func (s *Session) SetHandlers(events func([]assistant.Event), exit func(assistantpanel.Exit)) {
	s.mu.Lock()
	s.events, s.exit = events, exit
	s.mu.Unlock()
	s.deliverMu.Lock()
	defer s.deliverMu.Unlock()
	if len(s.queued) > 0 && events != nil {
		queued := s.queued
		s.queued = nil
		events(queued)
	}
	if s.queuedExit != nil && exit != nil {
		e := *s.queuedExit
		s.queuedExit = nil
		exit(e)
	}
}

// endsSession says whether a failure of class ends the session
// (docs/api.md §4.18 assistant.sessionSend).
func endsSession(class api.AssistantFailureClass) bool {
	switch class {
	case api.FailureAuth, api.FailurePermission, api.FailureToolsUnsupported, api.FailureContextFull, api.FailureBridge, api.FailureStopped:
		return true
	}
	return false
}

// Handle takes one event of the session (notify.assistantEvent) and
// delivers what it stands for; false for another session's.
func (s *Session) Handle(e api.AssistantEvent) bool {
	if e.SessionID != s.id {
		return false
	}
	events, exitReason := s.translate(e)
	s.deliver(events)
	if exitReason != "" {
		s.end(exitReason, false)
	}
	return true
}

// translate is the assistant.Events of e, and the reason the session
// ends with ("" when it goes on).
func (s *Session) translate(e api.AssistantEvent) ([]assistant.Event, string) {
	switch e.Kind {
	case api.EventInit:
		tools := e.Tools
		if len(tools) == 0 {
			tools = s.tools
		}
		return []assistant.Event{{Kind: assistant.EventInit, BridgeConnected: true, Tools: append([]string{}, tools...)}}, ""
	case api.EventTextDelta:
		return []assistant.Event{{Kind: assistant.EventTextDelta, Text: e.Text}}, ""
	case api.EventText:
		s.mu.Lock()
		s.blocks = append(s.blocks, e.Text)
		s.mu.Unlock()
		return []assistant.Event{{Kind: assistant.EventText, Text: e.Text}}, ""
	case api.EventToolUse:
		return []assistant.Event{{Kind: assistant.EventToolUse, Tool: e.Tool, ToolUseID: e.ID}}, ""
	case api.EventToolResult:
		return []assistant.Event{{Kind: assistant.EventToolResult, ToolUseID: e.ID, IsError: e.IsError, ResultText: e.Text}}, ""
	case api.EventUsage:
		if e.Usage == nil {
			return nil, ""
		}
		u := assistant.Usage{
			InputTokens: e.Usage.InputTokens, OutputTokens: e.Usage.OutputTokens,
			CacheCreationInputTokens: e.Usage.CacheCreationInputTokens, CacheReadInputTokens: e.Usage.CacheReadInputTokens,
		}
		s.mu.Lock()
		id := s.id + ":" + strconv.Itoa(s.turn)
		s.mu.Unlock()
		return []assistant.Event{{Kind: assistant.EventOther, Usage: &u, UsageFinal: true, MessageID: id}}, ""
	case api.EventResult:
		s.mu.Lock()
		text := strings.Join(s.blocks, "\n")
		s.blocks = nil
		s.mu.Unlock()
		out := assistant.Event{Kind: assistant.EventResult, ResultText: text}
		if e.Result != nil {
			out.Success = e.Result.Success
			out.IsError = !e.Result.Success
			out.Structured = e.Result.Structured
		}
		return []assistant.Event{out}, ""
	case api.EventFailure:
		class := api.FailureStopped
		if e.Failure != nil {
			class = e.Failure.Class
		}
		reason := reasonOf(class)
		s.mu.Lock()
		s.blocks = nil
		s.mu.Unlock()
		events := []assistant.Event{
			{Kind: assistant.EventFailure, Failure: reason},
			{Kind: assistant.EventResult, Success: false, IsError: true, ResultText: reason},
		}
		if endsSession(class) {
			return events, reason
		}
		return events, ""
	}
	return nil, ""
}

// deliver hands events to the handler, if any.
func (s *Session) deliver(events []assistant.Event) {
	if len(events) == 0 {
		return
	}
	s.mu.Lock()
	fn := s.events
	s.mu.Unlock()
	s.deliverMu.Lock()
	defer s.deliverMu.Unlock()
	if fn != nil {
		fn(events)
		return
	}
	s.queued = append(s.queued, events...)
}

var _ assistantpanel.Session = (*Session)(nil)
