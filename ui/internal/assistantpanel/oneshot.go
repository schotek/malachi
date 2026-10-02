// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistantpanel

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/schotek/malachi/ui/internal/assistant"
)

// The one-shot requests of the In App target (ui/internal/assistant
// rewrite.go and search.go): the compose window's rewrite (Rewriter) and
// the search in the user's own words (Searcher), each one question to the
// user's Claude Code that reads no mail (Request), and the board's triage
// run (ui/internal/boardtriage), which reads it through the bridge
// (Request.StartCall with Tools). The macOS client leads (MalachiCore
// AssistantRequest, ComposeRewriteController, SearchConversion); this is
// its port.

// FailureKind is why a request brought no answer.
type FailureKind int

// The kinds of failures.
const (
	// FailureStopped: it ended badly; Reason is technical (the result's
	// text or subtype, stderr's first line, the timeout, a launch failure).
	FailureStopped FailureKind = iota
	// FailureNotFound: Claude Code was not found on this computer.
	FailureNotFound
	// FailureNotSignedIn: Claude Code says it is not signed in, or the API
	// refused its sign-in.
	FailureNotSignedIn
	// FailureToolsMissing: a request with Tools; Claude Code did not
	// report the bridge connected.
	FailureToolsMissing
)

// Failure is why a request brought no answer.
type Failure struct {
	Kind   FailureKind
	Reason string
}

// Text is the line where the panel's errors are shown (the compose
// window's popover): the panel's texts, and for a missing sign-in where to
// sign in (a request has no Sign In… of its own).
func (f Failure) Text(tr assistant.Translator) string {
	switch f.Kind {
	case FailureNotFound:
		return assistant.PanelTexts(tr).NotFound
	case FailureNotSignedIn:
		return assistant.SignInTexts(tr).Hint
	case FailureToolsMissing:
		return assistant.PanelTexts(tr).ToolsMissing
	}
	return assistant.StoppedText(tr, f.Reason)
}

// ReasonText is the reason inside another sentence ("The search could not
// be converted: %s").
func (f Failure) ReasonText(tr assistant.Translator) string {
	switch f.Kind {
	case FailureNotFound:
		return assistant.PanelTexts(tr).NotFound
	case FailureNotSignedIn:
		return assistant.SignInTexts(tr).Hint
	case FailureToolsMissing:
		return assistant.PanelTexts(tr).ToolsMissing
	}
	return f.Reason
}

// TimedOut says whether the request ended because no answer came within
// its timeout.
func (f Failure) TimedOut() bool {
	return f.Kind == FailureStopped && f.Reason == timedOut
}

// OutcomeKind is how a request ended.
type OutcomeKind int

// The kinds of outcomes.
const (
	// OutcomeAnswered: the result's text (the streamed text when the result
	// has none) and its structured_output (nil without one).
	OutcomeAnswered OutcomeKind = iota
	// OutcomeFailed: Failure says why.
	OutcomeFailed
	// OutcomeDeclined: the user declined the consent question; nothing was
	// sent.
	OutcomeDeclined
)

// Outcome is how a request ended; only the fields of its Kind are set.
type Outcome struct {
	Kind       OutcomeKind
	Text       string
	Structured []byte
	Failure    Failure
}

// DefaultRequestTimeout is how long an answer is waited for.
const DefaultRequestTimeout = 120 * time.Second

// timedOut is the reason when the answer was not there in time.
const timedOut = "no answer in time"

// RequestConfig is what a Request needs from the application.
type RequestConfig struct {
	Settings Settings
	Locator  *Locator
	Loop     Loop
	Log      *slog.Logger
	// Directory is Claude Code's working directory (empty, private).
	Directory string
	// Env is the application's environment, filtered by
	// assistant.ChildEnv.
	Env []string
	// KillGrace is the time from SIGTERM to SIGKILL; 0 is
	// DefaultKillGrace.
	KillGrace time.Duration
}

// Request is one question to the user's Claude Code (macOS
// AssistantRequest). It runs the panel's protocol once: the command line of
// assistant.Args without the bridge (no MCP server, no tool), or with the
// bridge, its extra arguments and the tools of Tools when the caller
// passes them (StartCall), with --json-schema when the answer has a shape,
// in the panel's private directory and with assistant.ChildEnv; one
// assistant.UserMessage on stdin, which is then closed; the answer is the
// result event. The steps, each of which may end it:
//
//  1. The first request ever asks for consent (Consent, the panel's "Send
//     Mail to Claude?" on the window that asks; the answer is the shared
//     assistant-consent, kept even when the request was cancelled
//     meanwhile). Declined: OutcomeDeclined, nothing is sent.
//  2. Claude Code is located (none: FailureNotFound) and must not say it is
//     signed out (Locator.SignedIn, asked afresh: FailureNotSignedIn; not
//     known counts as signed in, and the process then says what is wrong:
//     a turn the API refused for its sign-in, assistant.EventFailure, is
//     FailureNotSignedIn too).
//  3. The process starts; text deltas stream to onText (a whole text block
//     replaces the deltas before it), the result ends it: a success is
//     OutcomeAnswered with the result's text and structured_output,
//     anything else FailureStopped with the result's text or subtype. The
//     process ending before its result is FailureStopped with its stderr's
//     first line, and no result within Timeout (or the call's own) ends it
//     the same way (Failure.TimedOut). With Tools, the init event must
//     report the bridge connected (else FailureToolsMissing), and every
//     tool call and tool result goes to Call.OnTool. Every event that
//     carries usage (an API message's, the result's) goes to Call.OnUsage
//     first, before the event is handled (assistant.UsageTally adds them
//     up); none arrives once the request ended or was cancelled.
//
// One request at a time: Start cancels the one under way, and Cancel ends
// it (its process terminated); a cancelled request never calls its
// completion. Nothing is kept on disk; model text is never logged. Main
// loop only.
type Request struct {
	// Consent asks the user before the first request ever and calls done
	// with the answer; without it nothing is ever sent.
	Consent func(done func(allowed bool))
	// Timeout is how long the answer is waited for.
	Timeout time.Duration

	settings  Settings
	locator   *Locator
	loop      Loop
	log       *slog.Logger
	directory string
	env       []string
	killGrace time.Duration

	running bool
	// gen is bumped by every Start and Cancel: the steps of an older
	// request stop at their next callback, its events and its end are
	// dropped.
	gen     int
	process *Process
	// blocks are the text blocks of the answer that are whole, streamed
	// the deltas since the last of them.
	blocks, streamed string
}

// NewRequest is a request with nothing asked yet.
func NewRequest(cfg RequestConfig) *Request {
	grace := cfg.KillGrace
	if grace == 0 {
		grace = DefaultKillGrace
	}
	return &Request{
		Timeout:   DefaultRequestTimeout,
		settings:  cfg.Settings,
		locator:   cfg.Locator,
		loop:      cfg.Loop,
		log:       cfg.Log,
		directory: cfg.Directory,
		env:       cfg.Env,
		killGrace: grace,
	}
}

// Running says whether a request is under way.
func (r *Request) Running() bool { return r.running }

// Tools are the bridge a request gives Claude Code, and what of it may
// run.
type Tools struct {
	// Bridge is the path of malachi-mcp; Socket the daemon's socket
	// (--socket), "" for the bridge's default.
	Bridge, Socket string
	// BridgeArgs are the bridge's further arguments
	// (assistant.TriageBridgeArgs).
	BridgeArgs []string
	// Allowed is --allowedTools (assistant.TriageToolsFor).
	Allowed []string
}

// Call is what one StartCall asks for.
type Call struct {
	// SystemPrompt and Message are the system prompt and the one turn.
	SystemPrompt, Message string
	// JSONSchema, when set, is the answer's shape.
	JSONSchema string
	// Tools gives Claude Code the bridge; nil: no tool at all.
	Tools *Tools
	// Timeout replaces the request's own for this call when above 0.
	Timeout time.Duration
	// Model replaces the assistant-model setting when set (read at the
	// start).
	Model assistant.Model
	// OnText (may be nil) gets the answer's text as it streams, all of it
	// so far.
	OnText func(string)
	// OnTool (may be nil) gets every tool call and tool result
	// (assistant.EventToolUse, assistant.EventToolResult); it may cancel
	// the request.
	OnTool func(assistant.Event)
	// OnUsage (may be nil) gets every event that carries usage, before
	// the event is handled.
	OnUsage func(assistant.Event)
}

// Start asks Claude Code once: message as the one turn under systemPrompt,
// with the model of assistant-model and, when jsonSchema is set, that shape
// of answer. A request under way is cancelled first. onText (may be nil)
// gets the answer's text as it streams, all of it so far; completion is
// called once with the outcome, unless the request is cancelled.
func (r *Request) Start(systemPrompt, message, jsonSchema string, onText func(string), completion func(Outcome)) {
	r.StartCall(Call{SystemPrompt: systemPrompt, Message: message, JSONSchema: jsonSchema, OnText: onText}, completion)
}

// StartCall asks Claude Code once as c says (see Call); a request under
// way is cancelled first. completion is called once with the outcome,
// unless the request is cancelled.
func (r *Request) StartCall(c Call, completion func(Outcome)) {
	r.Cancel()
	r.gen++
	my := r.gen
	r.running = true
	r.loop.Post(func() { r.askConsent(my, c, completion) })
}

// Cancel ends the request under way: its process is terminated and its
// completion never called.
func (r *Request) Cancel() {
	r.gen++
	if !r.running {
		return
	}
	r.running = false
	r.retire()
}

// askConsent is step 1: consent, once ever; an answer counts even when the
// request was cancelled while the question was up.
func (r *Request) askConsent(my int, c Call, completion func(Outcome)) {
	if my != r.gen {
		return
	}
	if r.settings.AssistantConsent() {
		r.locate(my, c, completion)
		return
	}
	answer := func(allowed bool) {
		if allowed {
			r.settings.SetAssistantConsent(true)
		}
		if my != r.gen {
			return
		}
		if !allowed {
			r.finish(my, Outcome{Kind: OutcomeDeclined}, completion)
			return
		}
		r.locate(my, c, completion)
	}
	if r.Consent == nil {
		answer(false)
		return
	}
	r.Consent(answer)
}

// locate is step 2: Claude Code, signed in; then step 3.
func (r *Request) locate(my int, c Call, completion func(Outcome)) {
	path := r.locator.Locate()
	if path == "" {
		r.finish(my, failed(FailureNotFound, ""), completion)
		return
	}
	r.locator.Refresh()
	r.locator.SignedIn(func(s SignIn) {
		if my != r.gen {
			return
		}
		if s.Known && !s.SignedIn {
			r.finish(my, failed(FailureNotSignedIn, ""), completion)
			return
		}
		p, err := r.launch(my, path, c, completion)
		if err != nil {
			r.log.Warn("assistant request", "err", err)
			r.finish(my, failed(FailureStopped, err.Error()), completion)
			return
		}
		r.process = p
		if !p.Send(assistant.UserMessage(c.Message)) {
			r.finish(my, failed(FailureStopped, "claude is not running"), completion)
			return
		}
		p.CloseInput()
		timeout := r.Timeout
		if c.Timeout > 0 {
			timeout = c.Timeout
		}
		r.loop.After(timeout, func() {
			if my == r.gen && r.running {
				r.log.Warn("assistant request: no answer in time")
				r.finish(my, failed(FailureStopped, timedOut), completion)
			}
		})
	})
}

func failed(kind FailureKind, reason string) Outcome {
	return Outcome{Kind: OutcomeFailed, Failure: Failure{Kind: kind, Reason: reason}}
}

// launch is step 3: the process.
func (r *Request) launch(my int, path string, c Call, completion func(Outcome)) (*Process, error) {
	if err := ensureDirectory(r.directory); err != nil {
		return nil, fmt.Errorf("the assistant's directory: %w", err)
	}
	model := c.Model
	if model == "" {
		model = r.settings.AssistantModel()
	}
	opts := assistant.Options{Model: model, SystemPrompt: c.SystemPrompt, JSONSchema: c.JSONSchema}
	if c.Tools != nil {
		opts.Bridge, opts.Socket = c.Tools.Bridge, c.Tools.Socket
		opts.BridgeArgs = c.Tools.BridgeArgs
		// Never nil with Tools: nil would be the panel's tools.
		opts.Tools = append([]string{}, c.Tools.Allowed...)
	}
	args := assistant.Args(opts)
	onText := c.OnText
	p := NewProcess(r.loop, r.log, path, args, assistant.ChildEnv(r.env, path), r.directory, r.killGrace)
	r.blocks, r.streamed = "", ""
	p.OnEvents = func(events []assistant.Event) {
		if p != r.process || my != r.gen {
			return
		}
		for _, e := range events {
			if e.Usage != nil && c.OnUsage != nil {
				c.OnUsage(e)
				// The handler may have cancelled the request.
				if p != r.process || my != r.gen {
					return
				}
			}
			switch e.Kind {
			case assistant.EventInit:
				if c.Tools != nil && !e.BridgeConnected {
					r.log.Warn("assistant request: the malachi MCP server is not connected")
					r.finish(my, failed(FailureToolsMissing, ""), completion)
					return
				}
			case assistant.EventToolUse, assistant.EventToolResult:
				if c.OnTool != nil {
					c.OnTool(e)
					// The handler may have cancelled the request.
					if p != r.process || my != r.gen {
						return
					}
				}
			case assistant.EventTextDelta:
				r.streamed += e.Text
				if onText != nil {
					onText(r.blocks + r.streamed)
				}
			case assistant.EventText:
				r.blocks += e.Text
				r.streamed = ""
				if onText != nil {
					onText(r.blocks)
				}
			case assistant.EventFailure:
				// Claude Code's own words for a turn the API refused,
				// which the result repeats; a refused sign-in ends it.
				if e.NotSignedIn() {
					r.finish(my, failed(FailureNotSignedIn, ""), completion)
					return
				}
			case assistant.EventResult:
				r.log.Info("assistant request", "success", e.Success, "costUSD", e.CostUSD)
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
	p.OnExit = func(e Exit) {
		if p != r.process || my != r.gen {
			return
		}
		r.log.Info("assistant request: claude ended", "status", e.Status)
		r.finish(my, failed(FailureStopped, e.Description()), completion)
	}
	if err := p.Start(); err != nil {
		return nil, err
	}
	return p, nil
}

// finish ends request my with outcome, once.
func (r *Request) finish(my int, outcome Outcome, completion func(Outcome)) {
	if my != r.gen || !r.running {
		return
	}
	r.running = false
	r.retire()
	completion(outcome)
}

// retire terminates the request's process (after its answer it is about
// to end anyway); its end is not reported.
func (r *Request) retire() {
	p := r.process
	r.process = nil
	if p != nil {
		p.OnEvents = nil
		p.Terminate()
	}
}

// RewriteStateKind is where the compose window's rewrite is.
type RewriteStateKind int

// The states of a rewrite.
const (
	// RewriteIdle: nothing asked, or the consent was declined.
	RewriteIdle RewriteStateKind = iota
	// RewriteRunning: asked; Text is the answer so far, cleaned.
	RewriteRunning
	// RewriteDone: Text is the answer, cleaned and not empty.
	RewriteDone
	// RewriteFailed: Text is what went wrong, as a line to show.
	RewriteFailed
)

// RewriteState is the rewrite's state.
type RewriteState struct {
	Kind RewriteStateKind
	Text string
}

// Rewriter is the compose window's rewrite without its popover (macOS
// ComposeRewriteController): a passage of the message, the selection or
// the user's own text, goes to the user's Claude Code with a preset or the
// user's own instruction (assistant.RewriteMessage under
// assistant.RewriteSystemPrompt, one Request), and the answer, cleaned
// (assistant.CleanRewrite), is offered for Replace or Insert Below, which
// the window does as plain text. Failures are lines to show: Claude Code
// not found or not signed in (the panel's texts), "The assistant stopped:
// …" otherwise, an empty answer included. A new rewrite cancels the one
// under way; Cancel (the popover or the window closed) ends it and goes
// back to idle.
type Rewriter struct {
	// OnState: the state changed.
	OnState func(RewriteState)

	tr      assistant.Translator
	request *Request
	state   RewriteState
}

// NewRewriter rewrites through request.
func NewRewriter(tr assistant.Translator, request *Request) *Rewriter {
	return &Rewriter{tr: tr, request: request}
}

// Request is the rewrite's request (its consent hook is the window's).
func (w *Rewriter) Request() *Request { return w.request }

// State is where the rewrite is.
func (w *Rewriter) State() RewriteState { return w.state }

// Running says whether an answer is awaited.
func (w *Rewriter) Running() bool { return w.state.Kind == RewriteRunning }

func (w *Rewriter) set(s RewriteState) {
	if s == w.state {
		return
	}
	w.state = s
	if w.OnState != nil {
		w.OnState(s)
	}
}

// Start asks for rewrite r of passage (custom: the user's own instruction,
// for assistant.Custom). False, and nothing changes, when there is nothing
// to ask: an empty passage, or Custom without words. A passage that is too
// long (or a rewrite that does not exist) fails at once.
func (w *Rewriter) Start(r assistant.Rewrite, custom, passage string) bool {
	if strings.TrimSpace(passage) == "" || (r == assistant.Custom && strings.TrimSpace(custom) == "") {
		return false
	}
	message, err := assistant.RewriteMessage(r, custom, passage)
	if err != nil {
		w.request.Cancel()
		w.set(RewriteState{Kind: RewriteFailed, Text: assistant.StoppedText(w.tr, err.Error())})
		return true
	}
	w.set(RewriteState{Kind: RewriteRunning})
	w.request.Start(assistant.RewriteSystemPrompt(), message, "", func(text string) {
		if w.Running() {
			w.set(RewriteState{Kind: RewriteRunning, Text: assistant.CleanRewrite(text)})
		}
	}, w.finished)
	return true
}

// Cancel ends the rewrite under way (the popover or the window closed) and
// forgets the answer.
func (w *Rewriter) Cancel() {
	w.request.Cancel()
	w.set(RewriteState{})
}

func (w *Rewriter) finished(o Outcome) {
	switch o.Kind {
	case OutcomeAnswered:
		if clean := assistant.CleanRewrite(o.Text); clean != "" {
			w.set(RewriteState{Kind: RewriteDone, Text: clean})
		} else {
			w.set(RewriteState{Kind: RewriteFailed, Text: assistant.StoppedText(w.tr, "the answer is empty")})
		}
	case OutcomeFailed:
		w.set(RewriteState{Kind: RewriteFailed, Text: o.Failure.Text(w.tr)})
	default:
		w.set(RewriteState{})
	}
}

// SearchOutcomeKind is how a search in the user's own words ended.
type SearchOutcomeKind int

// The kinds of search outcomes.
const (
	// SearchQuery: Text is the query to search for.
	SearchQuery SearchOutcomeKind = iota
	// SearchFailed: Text is the toast.
	SearchFailed
	// SearchDeclined: the user declined the consent question; nothing was
	// sent.
	SearchDeclined
)

// SearchOutcome is how a search in the user's own words ended.
type SearchOutcome struct {
	Kind SearchOutcomeKind
	Text string
}

// Searcher is the search in the user's own words without the search field
// (macOS SearchConversion): the typed words go to the user's Claude Code
// (assistant.SearchMessage under assistant.SearchSystemPrompt, the answer
// shaped by assistant.SearchSchema, one Request), and the query of its
// answer (assistant.ParseSearchQuery of the result's structured_output, or
// of its text when Claude Code gave none) is what the field then searches
// for. A failure is the toast "The search could not be converted: %s"
// (assistant.SearchFailedText); the caller keeps the typed words. A new
// conversion cancels the one under way; Cancel ends it, and its completion
// is not called.
type Searcher struct {
	// Today is the date for the system prompt, YYYY-MM-DD.
	Today func() string

	tr      assistant.Translator
	request *Request
}

// NewSearcher converts through request.
func NewSearcher(tr assistant.Translator, request *Request) *Searcher {
	return &Searcher{Today: func() string { return time.Now().Format("2006-01-02") }, tr: tr, request: request}
}

// Request is the conversion's request (its consent hook is the window's).
func (s *Searcher) Request() *Request { return s.request }

// Running says whether a conversion is under way.
func (s *Searcher) Running() bool { return s.request.Running() }

// Convert asks for the query of words. False, and nothing happens, when
// there are no words; otherwise completion is called once, later (also for
// words that are too long), unless the conversion is cancelled.
func (s *Searcher) Convert(words string, completion func(SearchOutcome)) bool {
	if strings.TrimSpace(words) == "" {
		return false
	}
	message, err := assistant.SearchMessage(words)
	if err != nil {
		s.request.Cancel()
		text := assistant.SearchFailedText(s.tr, err.Error())
		s.request.loop.Post(func() { completion(SearchOutcome{Kind: SearchFailed, Text: text}) })
		return true
	}
	s.request.Start(assistant.SearchSystemPrompt(s.Today()), message, assistant.SearchSchema, nil, func(o Outcome) {
		completion(searchOutcome(s.tr, o))
	})
	return true
}

// Cancel ends the conversion under way; its completion is not called.
func (s *Searcher) Cancel() { s.request.Cancel() }

// searchOutcome is the query of an answer, or the toast.
func searchOutcome(tr assistant.Translator, o Outcome) SearchOutcome {
	switch o.Kind {
	case OutcomeAnswered:
		if q, ok := assistant.ParseSearchQuery(o.Structured); ok {
			return SearchOutcome{Kind: SearchQuery, Text: q}
		}
		if q, ok := assistant.ParseSearchQuery([]byte(o.Text)); ok {
			return SearchOutcome{Kind: SearchQuery, Text: q}
		}
		return SearchOutcome{Kind: SearchFailed, Text: assistant.SearchFailedText(tr, "the answer holds no query")}
	case OutcomeFailed:
		return SearchOutcome{Kind: SearchFailed, Text: assistant.SearchFailedText(tr, o.Failure.ReasonText(tr))}
	}
	return SearchOutcome{Kind: SearchDeclined}
}
