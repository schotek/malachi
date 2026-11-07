// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/llm"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Config is what a Session needs.
type Config struct {
	// ID names the session in its events (api.AssistantEvent.SessionID).
	ID string
	// Provider calls the model; Model is the model id.
	Provider llm.Provider
	Model    string
	// System is the system prompt; JSONSchema asks for a JSON answer.
	System     string
	JSONSchema json.RawMessage
	// Tools is the bridge; nil for a purpose without tools. Allowed are
	// the bare names the model may call (nil with Tools: every tool of
	// the bridge); a call outside them is refused without the bridge.
	Tools   Tools
	Allowed []string
	// Timeout bounds one turn (0 = api.DefaultAssistantTimeout);
	// MaxRounds the tool rounds of a turn (0 = api.MaxAssistantTurnTools);
	// MaxHistory the history in bytes (0 = api.MaxAssistantHistoryBytes);
	// MaxResult a tool result kept (0 = api.MaxAssistantToolResultBytes).
	Timeout    time.Duration
	MaxRounds  int
	MaxHistory int
	MaxResult  int
	// Emit receives every event of the session, in order, from the
	// turn's goroutine.
	Emit func(api.AssistantEvent)
	Log  *slog.Logger
}

// Errors of Turn.
var (
	ErrBusy  = errors.New("agent: a turn is running")
	ErrEnded = errors.New("agent: the session ended")
)

// Session is one conversation: its history, its tools and its usage.
type Session struct {
	cfg   Config
	tools []llm.Tool
	names []string
	allow map[string]bool

	mu        sync.Mutex
	history   []llm.Message
	histBytes int
	running   bool
	ended     bool
	cancel    context.CancelFunc
	usage     api.AssistantUsage
	turns     int
}

// New builds a session; the bridge's tools are filtered to Allowed.
func New(cfg Config) *Session {
	if cfg.Timeout <= 0 {
		cfg.Timeout = api.DefaultAssistantTimeout
	}
	if cfg.MaxRounds <= 0 {
		cfg.MaxRounds = api.MaxAssistantTurnTools
	}
	if cfg.MaxHistory <= 0 {
		cfg.MaxHistory = api.MaxAssistantHistoryBytes
	}
	if cfg.MaxResult <= 0 {
		cfg.MaxResult = api.MaxAssistantToolResultBytes
	}
	if cfg.Emit == nil {
		cfg.Emit = func(api.AssistantEvent) {}
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	s := &Session{cfg: cfg}
	if cfg.Tools != nil {
		var allow map[string]bool
		if cfg.Allowed != nil {
			allow = map[string]bool{}
			for _, n := range cfg.Allowed {
				allow[n] = true
			}
		}
		for _, t := range cfg.Tools.Tools() {
			if allow == nil || allow[t.Name] {
				s.tools = append(s.tools, t)
				s.names = append(s.names, t.Name)
			}
		}
		s.allow = map[string]bool{}
		for _, n := range s.names {
			s.allow[n] = true
		}
	}
	return s
}

// ID is the session's id.
func (s *Session) ID() string { return s.cfg.ID }

// ToolNames lists the tools the model has.
func (s *Session) ToolNames() []string { return append([]string(nil), s.names...) }

// Usage is the session's usage so far; Turns how many turns ran.
func (s *Session) Usage() api.AssistantUsage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.usage
}

func (s *Session) Turns() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.turns
}

// Ended reports a session that is over (Stop, a fatal failure).
func (s *Session) Ended() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ended
}

// Running reports a turn in progress.
func (s *Session) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// Stop ends the session: a running turn is cancelled (it reports failure
// stopped), the bridge closed. Safe to call twice.
func (s *Session) Stop() {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.ended = true
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if s.cfg.Tools != nil {
		_ = s.cfg.Tools.Close()
	}
}

// fatal are the failure classes that end the session.
var fatal = map[api.AssistantFailureClass]bool{
	api.FailureAuth: true, api.FailurePermission: true, api.FailureToolsUnsupported: true,
	api.FailureContextFull: true, api.FailureBridge: true, api.FailureStopped: true,
}

// Begin starts a turn with the user's message in the background: the
// checks are made now (ErrBusy while another turn runs, ErrEnded after
// the session ended), the turn runs in its own goroutine and done, when
// given, gets its outcome (nil after result, the failure's error
// otherwise) once every event of the turn was emitted.
func (s *Session) Begin(ctx context.Context, message string, done func(error)) error {
	s.mu.Lock()
	switch {
	case s.ended:
		s.mu.Unlock()
		return ErrEnded
	case s.running:
		s.mu.Unlock()
		return ErrBusy
	}
	s.running = true
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	s.cancel = cancel
	s.turns++
	s.mu.Unlock()
	go func() {
		err := s.run(ctx, message)
		cancel()
		s.mu.Lock()
		s.running = false
		s.cancel = nil
		s.mu.Unlock()
		if done != nil {
			done(err)
		}
	}()
	return nil
}

// Turn is Begin and waiting for the turn: it emits init, the answer's
// text as it streams, the tool calls and their results, the usage and
// result or failure, and returns the failure's error (nil after result).
func (s *Session) Turn(ctx context.Context, message string) error {
	ch := make(chan error, 1)
	if err := s.Begin(ctx, message, func(err error) { ch <- err }); err != nil {
		return err
	}
	return <-ch
}

// run is the turn itself.
func (s *Session) run(ctx context.Context, message string) error {
	s.append(llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{{Kind: llm.BlockText, Text: message}}})
	s.emit(api.AssistantEvent{Kind: api.EventInit, Tools: s.ToolNames()})

	var turnUsage api.AssistantUsage
	for round := 0; ; round++ {
		req := llm.Request{Model: s.cfg.Model, System: s.cfg.System, Messages: s.snapshot(), Tools: s.tools, JSONSchema: s.cfg.JSONSchema}
		var (
			assistant []llm.Block
			calls     []llm.ToolCall
			callUsage *api.AssistantUsage
			stop      llm.StopReason
			final     strings.Builder
		)
		err := s.cfg.Provider.Stream(ctx, req, func(ev llm.Event) error {
			switch ev.Kind {
			case llm.EventTextDelta:
				s.emit(api.AssistantEvent{Kind: api.EventTextDelta, Text: ev.Text})
			case llm.EventTextDone:
				assistant = append(assistant, llm.Block{Kind: llm.BlockText, Text: ev.Text})
				final.WriteString(ev.Text)
				s.emit(api.AssistantEvent{Kind: api.EventText, Text: ev.Text})
			case llm.EventToolCall:
				if ev.Call == nil {
					return nil
				}
				c := *ev.Call
				if c.Input == nil {
					// Arguments that were not JSON: the call is answered with
					// an error result below; the model sees what it sent.
					c.Input = json.RawMessage(`{}`)
					calls = append(calls, llm.ToolCall{ID: c.ID, Name: c.Name, Input: nil})
				} else {
					calls = append(calls, c)
				}
				assistant = append(assistant, llm.Block{Kind: llm.BlockToolUse, ID: c.ID, Name: c.Name, Input: c.Input})
				s.emit(api.AssistantEvent{Kind: api.EventToolUse, Tool: c.Name, ID: c.ID})
			case llm.EventUsage:
				if ev.Usage != nil {
					u := *ev.Usage
					callUsage = &u
				}
			case llm.EventStop:
				stop = ev.Stop
			}
			return nil
		})
		if callUsage != nil {
			turnUsage = turnUsage.Add(*callUsage)
			s.addUsage(*callUsage)
		}
		if err != nil {
			return s.fail(err, turnUsage)
		}
		s.emit(api.AssistantEvent{Kind: api.EventUsage, Usage: usagePtr(turnUsage)})
		if len(assistant) > 0 {
			s.append(llm.Message{Role: llm.RoleAssistant, Blocks: assistant})
		}
		if len(calls) == 0 || stop != llm.StopToolUse {
			result := api.AssistantResult{Reason: string(stop)}
			switch stop {
			case llm.StopEndTurn, llm.StopOther, "":
				result.Success = true
				if stop == "" {
					result.Reason = string(llm.StopEndTurn)
				}
			case llm.StopToolUse:
				// Tool use without a call: the answer is what there is.
				result.Success = true
				result.Reason = string(llm.StopEndTurn)
			}
			if s.cfg.JSONSchema != nil {
				result.Structured = structured(final.String())
			}
			s.emit(api.AssistantEvent{Kind: api.EventResult, Result: &result, Usage: usagePtr(turnUsage)})
			return nil
		}
		if round >= s.cfg.MaxRounds {
			return s.failClass(api.FailureLimit, 0, "the turn exceeded its tool rounds", turnUsage)
		}
		results := make([]llm.Block, 0, len(calls))
		for _, c := range calls {
			text, isErr, down := s.call(ctx, c)
			if ctx.Err() != nil {
				return s.fail(ctx.Err(), turnUsage)
			}
			if down {
				return s.failClass(api.FailureBridge, 0, "the bridge failed", turnUsage)
			}
			s.emit(api.AssistantEvent{Kind: api.EventToolResult, Tool: c.Name, ID: c.ID, Text: text, IsError: isErr})
			results = append(results, llm.Block{Kind: llm.BlockToolResult, ID: c.ID, Text: text, IsError: isErr})
		}
		s.append(llm.Message{Role: llm.RoleUser, Blocks: results})
		if s.histBytes > s.cfg.MaxHistory {
			return s.failClass(api.FailureContextFull, 0, "the conversation is too long", turnUsage)
		}
	}
}

// call runs one tool call: a tool the model may not call or arguments
// that were not JSON are answered without the bridge; down reports a
// failure of the bridge itself.
func (s *Session) call(ctx context.Context, c llm.ToolCall) (text string, isErr, down bool) {
	switch {
	case !s.allow[c.Name]:
		return "The tool " + c.Name + " is not available in this session.", true, false
	case c.Input == nil:
		return "The arguments of " + c.Name + " were not valid JSON; send a JSON object.", true, false
	}
	text, isErr, err := s.cfg.Tools.Call(ctx, c.Name, c.Input)
	if err != nil {
		if ctx.Err() == nil {
			s.cfg.Log.Warn("bridge call failed", "tool", c.Name, "err", err.Error())
		}
		return "", true, true
	}
	if len(text) > s.cfg.MaxResult {
		text = cutUTF8(text, s.cfg.MaxResult) + "\n[the result was cut here]"
	}
	return text, isErr, false
}

// fail turns a provider or context error into the failure event.
func (s *Session) fail(err error, turnUsage api.AssistantUsage) error {
	class := llm.ClassOf(err)
	retry := 0
	msg := ""
	var le *llm.Error
	if errors.As(err, &le) {
		retry = le.RetryAfter
		msg = le.Message
	}
	if errors.Is(err, context.DeadlineExceeded) {
		msg = "the turn timed out"
	}
	return s.failClass(class, retry, msg, turnUsage)
}

func (s *Session) failClass(class api.AssistantFailureClass, retry int, msg string, turnUsage api.AssistantUsage) error {
	s.emit(api.AssistantEvent{Kind: api.EventFailure, Failure: &api.AssistantFailure{Class: class, RetryAfter: retry, Message: msg},
		Usage: usagePtr(turnUsage)})
	if fatal[class] {
		s.mu.Lock()
		ended := s.ended
		s.ended = true
		s.mu.Unlock()
		if !ended && s.cfg.Tools != nil {
			_ = s.cfg.Tools.Close()
		}
	}
	s.cfg.Log.Info("assistant turn failed", "session", s.cfg.ID, "class", class)
	return &llm.Error{Class: class, RetryAfter: retry, Message: msg}
}

func (s *Session) emit(ev api.AssistantEvent) {
	ev.SessionID = s.cfg.ID
	s.cfg.Emit(ev)
}

func (s *Session) append(m llm.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = append(s.history, m)
	for _, b := range m.Blocks {
		s.histBytes += len(b.Text) + len(b.Input) + len(b.Name) + len(b.ID)
	}
}

func (s *Session) snapshot() []llm.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]llm.Message(nil), s.history...)
}

func (s *Session) addUsage(u api.AssistantUsage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.usage = s.usage.Add(u)
}

func usagePtr(u api.AssistantUsage) *api.AssistantUsage { return &u }

// structured is the answer as compact JSON when it is a JSON document,
// else nil (the client falls back to the text).
func structured(text string) json.RawMessage {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```") {
		// A fenced block: the first fence line and the closing fence go.
		if i := strings.Index(text, "\n"); i >= 0 {
			text = text[i+1:]
		}
		text = strings.TrimSuffix(strings.TrimSpace(text), "```")
		text = strings.TrimSpace(text)
	}
	if !json.Valid([]byte(text)) {
		return nil
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(text)); err != nil {
		return nil
	}
	return json.RawMessage(buf.Bytes())
}

// cutUTF8 cuts s to at most n bytes on a rune boundary.
func cutUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && n < len(s) && (s[n]&0xC0) == 0x80 {
		n--
	}
	return s[:n]
}
