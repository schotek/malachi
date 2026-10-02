// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistantpanel

import (
	"context"
	"github.com/schotek/malachi/ui/internal/assistant"
	"time"
)

// Provider opens an isolated session; credentials are never exposed to views.
// A nil Provider factory keeps the established Claude implementation.
type Provider interface {
	Model() string
	HasConsent() bool
	AcceptConsent()
	Open(context.Context, SessionSpec) (Session, error)
}

// SessionSpec freezes instructions and tool authority before any input is sent.
type SessionSpec struct {
	SystemPrompt, JSONSchema, ModelID string
	Tools                             *Tools
	Timeout                           time.Duration
}

// Session normalizes provider events to the existing panel protocol.
// Handlers may run on a worker; controllers marshal them through their Loop.
type Session interface {
	Running() bool
	Submit(context.Context, string) error
	Terminate()
	Completion() <-chan struct{}
	SetHandlers(func([]assistant.Event), func(Exit))
}

// ProviderChanged ends an old provider/profile conversation before new input.
func (c *Controller) ProviderChanged() { c.NewConversation(); c.selectedProvider = nil }
func (c *Controller) hasSession() bool {
	return (c.process != nil && c.process.Running()) || (c.providerSession != nil && c.providerSession.Running())
}
func (c *Controller) providerPrompt(my int, prompt string, told []int) {
	if c.providerSession != nil && c.providerSession.Running() {
		c.sendProvider(my, prompt, told)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancelProvider = cancel
	provider := c.selectedProvider
	spec := SessionSpec{SystemPrompt: assistant.SystemPrompt(c.Language(), c.Today()), Tools: &Tools{Bridge: c.bridge, Socket: c.socket, Allowed: append([]string{}, assistant.AllowedTools...)}}
	go func() {
		session, err := provider.Open(ctx, spec)
		c.loop.Post(func() {
			if my != c.gen || c.closed {
				if session != nil {
					session.Terminate()
				}
				return
			}
			if err != nil {
				c.fail(assistant.StoppedText(c.tr, err.Error()), false)
				return
			}
			c.providerSession = session
			session.SetHandlers(func(events []assistant.Event) {
				c.loop.Post(func() {
					if c.providerSession == session && !c.closed {
						c.handle(events)
					}
				})
			}, func(exit Exit) {
				c.loop.Post(func() {
					if c.providerSession == session && !c.closed {
						c.providerSession = nil
						c.exited(exit)
					}
				})
			})
			c.sendProvider(my, prompt, told)
		})
	}()
}
func (c *Controller) sendProvider(my int, prompt string, told []int) {
	session := c.providerSession
	if session == nil {
		c.fail(assistant.StoppedText(c.tr, "codex_session_closed"), false)
		return
	}
	for i := range c.pinned {
		for _, key := range told {
			if c.pinned[i].key == key {
				c.pinned[i].Announced = true
			}
		}
	}
	c.authFailed, c.refreshFailed = false, false
	c.phase = PhaseRunning
	c.state()
	go func() {
		err := session.Submit(context.Background(), prompt)
		if err != nil {
			c.loop.Post(func() {
				if c.providerSession == session && my == c.gen && c.phase == PhaseRunning {
					c.endProcess()
					c.fail(assistant.StoppedText(c.tr, err.Error()), false)
				}
			})
		}
	}()
}
func (r *Request) launchProvider(my int, c Call, completion func(Outcome)) {
	ctx, cancel := context.WithCancel(context.Background())
	r.cancelProvider = cancel
	provider := r.selectedProvider
	spec := SessionSpec{SystemPrompt: c.SystemPrompt, JSONSchema: c.JSONSchema, ModelID: c.ModelID, Tools: c.Tools, Timeout: c.Timeout}
	if c.Tools != nil {
		cp := *c.Tools
		cp.Allowed = append([]string{}, c.Tools.Allowed...)
		cp.BridgeArgs = append([]string{}, c.Tools.BridgeArgs...)
		spec.Tools = &cp
	}
	timeout := r.Timeout
	if c.Timeout > 0 {
		timeout = c.Timeout
	}
	r.loop.After(timeout, func() {
		if my == r.gen && r.running {
			r.finish(my, failed(FailureStopped, timedOut), completion)
		}
	})
	go func() {
		session, err := provider.Open(ctx, spec)
		r.loop.Post(func() {
			if my != r.gen || !r.running {
				if session != nil {
					session.Terminate()
				}
				return
			}
			if err != nil {
				r.finish(my, failed(FailureStopped, err.Error()), completion)
				return
			}
			r.providerSession = session
			r.blocks, r.streamed = "", ""
			session.SetHandlers(func(events []assistant.Event) {
				r.loop.Post(func() {
					if my == r.gen && r.running && r.providerSession == session {
						r.providerEvents(my, session, c, events, completion)
					}
				})
			}, func(exit Exit) {
				r.loop.Post(func() {
					if my == r.gen && r.running && r.providerSession == session {
						r.finish(my, failed(FailureStopped, exit.Description()), completion)
					}
				})
			})
			go func() {
				if err := session.Submit(ctx, c.Message); err != nil {
					r.loop.Post(func() {
						if my == r.gen && r.running && r.providerSession == session {
							r.finish(my, failed(FailureStopped, err.Error()), completion)
						}
					})
				}
			}()
		})
	}()
}
func (r *Request) providerEvents(my int, session Session, c Call, events []assistant.Event, completion func(Outcome)) {
	for _, e := range events {
		if my != r.gen || !r.running || r.providerSession != session {
			return
		}
		if e.Usage != nil && c.OnUsage != nil {
			c.OnUsage(e)
			if my != r.gen || !r.running {
				return
			}
		}
		switch e.Kind {
		case assistant.EventInit:
			if c.Tools != nil && !e.BridgeConnected {
				r.finish(my, failed(FailureToolsMissing, ""), completion)
				return
			}
		case assistant.EventToolUse, assistant.EventToolResult:
			if c.OnTool != nil {
				c.OnTool(e)
			}
		case assistant.EventTextDelta:
			r.streamed += e.Text
			if c.OnText != nil {
				c.OnText(r.blocks + r.streamed)
			}
		case assistant.EventText:
			r.blocks = e.Text
			r.streamed = ""
			if c.OnText != nil {
				c.OnText(r.blocks)
			}
		case assistant.EventResult:
			if !e.Success {
				r.finish(my, failed(FailureStopped, e.ResultText), completion)
				return
			}
			text := e.ResultText
			if text == "" {
				text = r.blocks + r.streamed
			}
			r.finish(my, Outcome{Kind: OutcomeAnswered, Text: text, Structured: e.Structured}, completion)
			return
		}
	}
}
