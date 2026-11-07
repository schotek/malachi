// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistantpanel

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistant"
)

// Caller calls the daemon (the window's RPC client; the method set of
// board.Caller, so the window passes the same one).
type Caller interface {
	Call(ctx context.Context, method string, params any, result any) error
}

// Prompter builds the prompts of a purpose: the system prompt with the
// user's profile, the tools, the bridge's arguments, the answer's schema
// and the message of a turn (docs/api.md §4.18 assistant.prompt,
// docs/plans/ai-api.md D6). The daemon builds every prompt; the clients
// that run Claude Code or Codex themselves take it from here, a session
// the daemon runs over an ai account (ui/internal/apiassistant) needs
// none. It may be called from any goroutine.
type Prompter interface {
	Prompt(ctx context.Context, p api.AssistantPromptParams) (api.PromptResult, error)
}

// promptTimeout bounds one assistant.prompt call.
const promptTimeout = 15 * time.Second

// daemonPrompter is the Prompter over the daemon (assistant.prompt).
type daemonPrompter struct{ caller Caller }

// NewPrompter is the Prompter over the daemon: assistant.prompt through
// caller, each call bounded by promptTimeout (and by ctx).
func NewPrompter(caller Caller) Prompter { return daemonPrompter{caller} }

func (d daemonPrompter) Prompt(ctx context.Context, p api.AssistantPromptParams) (api.PromptResult, error) {
	ctx, cancel := context.WithTimeout(ctx, promptTimeout)
	defer cancel()
	var res api.PromptResult
	if err := d.caller.Call(ctx, api.MethodAssistantPrompt, p, &res); err != nil {
		return api.PromptResult{}, err
	}
	return res, nil
}

// Provider opens an isolated session; credentials are never exposed to views.
// A nil Provider factory keeps the established Claude implementation.
type Provider interface {
	// Name is the provider's name for the panel's subtitle (ChatGPT's
	// assistant.ChatGPTText Name, an ai account's name).
	Name() string
	Model() string
	HasConsent() bool
	AcceptConsent()
	Open(context.Context, SessionSpec) (Session, error)
}

// SessionSpec freezes what a session is for, and the instructions and tool
// authority resolved for it (Prompter), before any input is sent. A
// provider that runs the model itself (Codex) takes SystemPrompt, Tools,
// JSONSchema and the message of each turn from the Prompter; the daemon's
// own session (apiassistant) takes Purpose, Language, NoteAccountID,
// Trigger, RunID and BridgePath and builds the rest itself.
type SessionSpec struct {
	// Purpose is what the session is for; Language the UI language (BCP
	// 47) the model answers in; NoteAccountID the account whose profile
	// note a one-shot purpose of a compose window appends; Trigger and
	// RunID the triage's trigger and board run.
	Purpose       api.PromptPurpose
	Language      string
	NoteAccountID api.AccountID
	Trigger       api.BoardTrigger
	RunID         string
	// BridgePath is the bundled bridge's absolute path ("" without one).
	BridgePath string
	// The resolved prompt of the purpose (api.PromptResult without the
	// message): the system prompt with the profile, the answer's schema
	// (as a string, "" for none), the tools (Tools.Allowed, prefixed) and
	// the bridge's arguments (Tools.BridgeArgs).
	SystemPrompt, JSONSchema string
	Tools                    *Tools
	// ModelID overrides the provider's model; Timeout bounds one turn (0:
	// the provider's default).
	ModelID string
	Timeout time.Duration
}

// Session normalizes provider events to the existing panel protocol.
// Handlers may run on a worker; controllers marshal them through their Loop.
type Session interface {
	Running() bool
	// Submit sends one turn: the panel's text with its context, or the
	// structured input of a one-shot purpose (api.PromptInput).
	Submit(context.Context, api.PromptInput) error
	Terminate()
	Completion() <-chan struct{}
	SetHandlers(func([]assistant.Event), func(Exit))
}

// FailureLimit: the provider refused the request because the usage limit
// of the user's plan was reached (ChatGPT's HTTP 429, an API provider's
// rate limit); Reason is the provider's code. It follows the kinds of
// oneshot.go.
const FailureLimit = FailureToolsMissing + 1

// ProviderFailure is the failure a provider's reason stands for (an error
// of Provider.Open or Session.Submit, a failed result's text, an exit's
// reason): Codex missing is FailureNotFound; a ChatGPT connection that is
// missing, lapsed, refused or without consent is FailureNotSignedIn (the
// board offers to connect again, as it offers Claude Code's sign-in); the
// plan's usage limit is FailureLimit; anything else FailureStopped with
// the reason as it is. The reasons of a session the daemon runs over an
// ai account are "api_<class>" (ui/internal/apiassistant): a missing
// consent or a refused key is FailureNotSignedIn, a rate limit or an
// overloaded provider FailureLimit, a model without tools or a bridge
// that could not start FailureToolsMissing, the rest FailureStopped.
func ProviderFailure(reason string) Failure {
	switch reason {
	case "codex_not_found":
		return Failure{Kind: FailureNotFound, Reason: reason}
	case "chatgpt_not_connected", "chatgpt_reconnect_required", "chatgpt_consent_required",
		"chatgpt_permission_denied", "chatgpt_identity_mismatch",
		"api_consent_required", "api_auth":
		return Failure{Kind: FailureNotSignedIn, Reason: reason}
	case "chatgpt_usage_limit", "api_rateLimit", "api_overloaded":
		return Failure{Kind: FailureLimit, Reason: reason}
	case "api_toolsUnsupported", "api_bridge":
		return Failure{Kind: FailureToolsMissing, Reason: reason}
	}
	return Failure{Kind: FailureStopped, Reason: reason}
}

// apiFailure says whether reason is a session's failure class of the
// daemon's assistant (assistant.APIFailurePrefix).
func apiFailure(reason string) bool {
	return strings.HasPrefix(reason, assistant.APIFailurePrefix)
}

// providerFailed is the outcome of a provider's request that failed for
// reason.
func providerFailed(reason string) Outcome {
	return Outcome{Kind: OutcomeFailed, Failure: ProviderFailure(reason)}
}

// ProviderChanged ends an old provider/profile conversation before new input.
func (c *Controller) ProviderChanged() { c.NewConversation(); c.selectedProvider = nil }
func (c *Controller) hasSession() bool {
	return (c.process != nil && c.process.Running()) || (c.providerSession != nil && c.providerSession.Running())
}

// providerPrompt is the turn of a provider's session: the daemon's
// prompt of tn (composeTurn) is asked for first; the running session then
// gets the turn's input, else a session is opened with the panel's prompt
// (the system prompt with the profile, the tools and the bridge's
// arguments) and gets it.
func (c *Controller) providerPrompt(my int, tn turn, told []int) {
	if c.prompter == nil {
		c.fail(assistant.StoppedText(c.tr, errNoPrompter.Error()), false)
		return
	}
	prompter := c.prompter
	params := c.promptParams()
	go func() {
		res, input, err := composeTurn(context.Background(), prompter, params, tn)
		c.loop.Post(func() {
			if my != c.gen || c.closed {
				return
			}
			if err != nil {
				c.log.Warn("assistant prompt", "err", err)
				c.fail(assistant.StoppedText(c.tr, promptReason(err)), true)
				return
			}
			if c.providerSession != nil && c.providerSession.Running() {
				c.sendProvider(my, input, told)
				return
			}
			c.openProvider(my, res, input, told)
		})
	}()
}

// openProvider opens the provider's session with the panel's prompt res
// and sends the turn's input.
func (c *Controller) openProvider(my int, res api.PromptResult, input api.PromptInput, told []int) {
	ctx, cancel := context.WithCancel(context.Background())
	c.cancelProvider = cancel
	provider := c.selectedProvider
	spec := SessionSpec{
		Purpose: api.PurposePanel, Language: c.Language(), BridgePath: c.bridge, SystemPrompt: res.SystemPrompt,
		Tools: &Tools{Bridge: c.bridge, Socket: c.socket, BridgeArgs: slices.Clone(res.BridgeArgs), Allowed: assistant.PrefixTools(res.Tools)},
	}
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
				c.providerFail(err.Error(), false)
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
						c.providerExited(exit)
					}
				})
			})
			c.sendProvider(my, input, told)
		})
	}()
}
func (c *Controller) sendProvider(my int, input api.PromptInput, told []int) {
	session := c.providerSession
	if session == nil {
		c.providerFail("codex_session_closed", false)
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
		err := session.Submit(context.Background(), input)
		if err != nil {
			c.loop.Post(func() {
				if c.providerSession == session && my == c.gen && c.phase == PhaseRunning {
					c.endProcess()
					c.providerFail(err.Error(), false)
				}
			})
		}
	}()
}

// launchProvider is a one-shot request through the provider: a session
// opened with the call's resolved prompt (res), its input submitted.
func (r *Request) launchProvider(my int, c Call, res api.PromptResult, completion func(Outcome)) {
	ctx, cancel := context.WithCancel(context.Background())
	r.cancelProvider = cancel
	provider := r.selectedProvider
	spec := SessionSpec{
		Purpose: c.Purpose, Language: r.language(), NoteAccountID: c.NoteAccountID, Trigger: c.Trigger, RunID: c.RunID,
		SystemPrompt: res.SystemPrompt, JSONSchema: string(res.JSONSchema), ModelID: c.ModelID, Timeout: c.Timeout,
	}
	if c.Tools != nil {
		spec.BridgePath = c.Tools.Bridge
		spec.Tools = &Tools{Bridge: c.Tools.Bridge, Socket: c.Tools.Socket, BridgeArgs: slices.Clone(res.BridgeArgs), Allowed: assistant.PrefixTools(res.Tools)}
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
				r.finish(my, providerFailed(err.Error()), completion)
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
						r.finish(my, providerFailed(exit.Description()), completion)
					}
				})
			})
			go func() {
				if err := session.Submit(ctx, c.Input); err != nil {
					r.loop.Post(func() {
						if my == r.gen && r.running && r.providerSession == session {
							r.finish(my, providerFailed(err.Error()), completion)
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
				r.finish(my, providerFailed(e.ResultText), completion)
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
