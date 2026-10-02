// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package boardreply is the board's Suggest Reply (docs/mcp.md "A suggested
// reply on the board", docs/security.md §10.2) without its widgets: on the
// user's click in a case's detail, the user's own Claude Code writes one
// reply draft for that case through the bridge, the draft is linked to the
// case as its suggested reply, and the case's detail edits it in place.
// Three parts: the request itself, once for the whole application
// (Controller, this file), the inline reply editor that loads the linked
// draft for the selected case (Editor, editor.go) and the rules that decide
// what happens to the compose panes that edit such drafts, for one main
// window (Panes, panes.go).
//
// The macOS client leads (MalachiCore Controllers/BoardReplyController.swift,
// BoardReplyEditorController.swift, BoardReplyPanes.swift); this is its
// port for the GTK window and its compose pane, which are not implemented
// here. The texts are ui/internal/board's (reply.go, text.go) and
// ui/internal/assistant's; this package adds none. Nothing the model writes
// is shown or logged: what it wrote reaches the user only as the draft,
// which the case's detail shows from the daemon.
//
// The package is pure Go (no GTK, no GLib): the window passes a Loop over
// the GLib main loop (assistantpanel.Loop, the method set Post/After), and
// everything here runs on it; the calls to the daemon run on their own
// goroutines and come back through it.
package boardreply

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/assistantpanel"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/boardtriage"
)

// Caller is the daemon as this package needs it: one JSON-RPC call, a
// server-side error as *api.Error. ui/internal/client's Client has it. It
// blocks, so it is only ever called off the main loop.
type Caller interface {
	Call(ctx context.Context, method string, params any, result any) error
}

// CallTimeout bounds one call to the daemon.
const CallTimeout = 30 * time.Second

// callWith runs one call bounded by CallTimeout; off the main loop.
func callWith(c Caller, method string, params, result any) error {
	ctx, cancel := context.WithTimeout(context.Background(), CallTimeout)
	defer cancel()
	return c.Call(ctx, method, params, result)
}

// EndWait is how long CancelAndCleanUp waits for the board.setDraft and
// draft.delete calls on their way, at most: quitting calls it before the
// connection stops, and it never hangs on a daemon that does not answer
// (the triage's own bound at quit, boardtriage.EndWait).
const EndWait = boardtriage.EndWait

// ReplyNeedsInAppTarget mirrors boardtriage.TriageNeedsInAppTarget: a
// suggested reply is offered only under the assistant's "In App
// (experimental)" target, the condition of the panel, the compose rewrite,
// the search in the user's own words and the board's triage run. It stays
// experimental until Anthropic confirms the terms for running Claude Code
// from an application.
const ReplyNeedsInAppTarget = true

// ReplyAvailable says whether a suggested reply is available with the
// Assistant shown (assistant.Shown) and target chosen; the window passes it
// as Config.Available.
func ReplyAvailable(shown bool, target assistant.Target) bool {
	return shown && (!ReplyNeedsInAppTarget || target == assistant.App)
}

// Config is what a Controller needs from the application.
type Config struct {
	Caller   Caller
	Settings assistantpanel.Settings
	Locator  boardtriage.Locator
	// Request is the one-shot request a suggested reply uses (its consent
	// hook is cleared: the controller asks itself, before its own timer
	// starts); one of its own, not the panel's, the rewrite's, the
	// search's or the triage's.
	Request *assistantpanel.Request
	Loop    assistantpanel.Loop
	Log     *slog.Logger
	// Bridge is malachi-mcp beside the application ("" without one);
	// Socket the daemon's socket for it ("" for the bridge's default).
	Bridge, Socket string
	// Available says whether the feature can exist (ReplyAvailable); its
	// changes come through AvailabilityChanged.
	Available func() bool
}

// Controller is the board's Suggest Reply (BoardReplyController), once for
// the whole application: on the user's click, the user's Claude Code writes
// one reply draft for a case through the bridge, and the draft is linked as
// the case's suggested reply.
//
// A request, each step of which may end it (State):
//
//  1. It needs what the compose rewrite needs (CanRun: the assistant shown
//     with the In App target, the bridge beside the application, Claude
//     Code found), a case with a message to reply to and no suggested
//     reply yet, and no other request running (one at a time for the
//     application; Start refuses another). The first request ever asks
//     the assistant's consent (Consent, the panel's sheet,
//     assistant-consent); the board's triage consent is not needed.
//     Declined: back to idle. The time (Timeout) runs from here.
//  2. board.get for the case: its newest members' ids and, fresh, its
//     reply target; a case that has a suggested reply by now ends it
//     quietly.
//  3. The request: assistant.SuggestReplyMessage under
//     assistant.SuggestReplySystemPrompt, with the assistant's model, the
//     bridge started as "--socket <socket> --reply-only <replyMessageId>"
//     and only assistant.SuggestReplyTools.
//  4. The draft is the one a successful create_draft result names
//     (assistant.ParseDraftResult, as the panel's Open Draft card).
//     Without one the request failed (board.ReplyNoDraft). With one it is
//     linked with board.setDraft, and the board lists again (OnRefresh);
//     a link the daemon refuses deletes the draft (draft.delete, so no
//     orphan stays in Drafts), a refusal because the case got a suggested
//     reply meanwhile (conflict) ends quietly, any other as
//     board.ReplyBackend.
//
// Stop (Cancel) and the timeout delete a draft that was created and not
// linked yet; a link already on its way is left to finish (it is a local
// call), and deletes the draft only when it fails. Losing what it needs
// while it runs (AvailabilityChanged) stops it like Stop. Quitting stops it
// and waits for those deletes, at most EndWait (CancelAndCleanUp). Usage is
// not recorded: the 24-hour row of tokens is the triage's (board.runEnd),
// and a suggested reply is no run.
//
// A sample case (Case.Reply is always nil for the invented samples) never
// reaches Start: its guard refuses before any call is made, the same way
// SuggestReplyOffered never offers the control for one.
//
// One state per application, not per case (State.Case names which one):
// starting a reply for another case while one runs fails (Start returns
// false), and the detail of the other case says so through
// board.SuggestReplyViewOf (board.SuggestReplyElsewhere). Main loop only.
type Controller struct {
	// Consent asks the user whether mail may go to the assistant (the
	// panel's sheet, shared with the panel, the compose rewrite, the
	// search and the triage) and calls done with the answer, true
	// allowing. Without it a request that needs consent ends quietly.
	Consent func(done func(allowed bool))
	// Timeout is how long a request may take (assistant.SuggestReplyTimeout).
	// The controller keeps the time itself (loop.After), so the end of a
	// request that runs out of it is the controller's, created draft and
	// all; the request's own bound (Timeout + 10s) is a little longer.
	Timeout time.Duration
	// OnRefresh asks the board to list again (after a draft was linked).
	OnRefresh func()

	caller    Caller
	settings  assistantpanel.Settings
	locator   boardtriage.Locator
	request   *assistantpanel.Request
	loop      assistantpanel.Loop
	log       *slog.Logger
	bridge    string
	socket    string
	available func() bool

	observers    board.Observers
	state        board.SuggestReplyState
	signedIn     assistantpanel.SignIn
	removeSignIn func()

	// gen is bumped by every Start and every end (Cancel, the timeout, the
	// timeout and Stop both go through end): the steps of an older
	// request stop at their next callback.
	gen int
	// draftCalls are the create_draft calls of the request under way,
	// waiting for their results, by tool use id.
	draftCalls map[string]bool
	// created is the draft the request created and that is not linked
	// yet (the tests read it).
	created *assistant.DraftRef
	// linking: board.setDraft is on its way; Stop leaves the draft to it.
	linking bool
	// signInGen is bumped by every sign-in check and every sign-in a
	// request learnt: an older check's late answer is dropped.
	signInGen int
	// pending are the board.setDraft and draft.delete calls on their way,
	// closed once the daemon answered (CancelAndCleanUp waits for them).
	pending     map[int]chan struct{}
	nextPending int
}

// NewController builds the application's one suggested-reply request,
// idle.
func NewController(cfg Config) *Controller {
	c := &Controller{
		Timeout:    assistant.SuggestReplyTimeout,
		caller:     cfg.Caller,
		settings:   cfg.Settings,
		locator:    cfg.Locator,
		request:    cfg.Request,
		loop:       cfg.Loop,
		log:        cfg.Log,
		bridge:     cfg.Bridge,
		socket:     cfg.Socket,
		available:  cfg.Available,
		draftCalls: map[string]bool{},
		pending:    map[int]chan struct{}{},
	}
	if c.log == nil {
		c.log = slog.New(slog.DiscardHandler)
	}
	if c.available == nil {
		c.available = func() bool { return false }
	}
	// The controller asks consent itself, before its own time runs.
	c.request.Consent = nil
	c.removeSignIn = cfg.Locator.OnSignInChange(func() {
		c.CheckSignIn()
		c.observers.Notify()
	})
	return c
}

// Close stops listening to the locator's sign-in changes; a request under
// way goes on (CancelAndCleanUp ends it).
func (c *Controller) Close() {
	if c.removeSignIn != nil {
		c.removeSignIn()
		c.removeSignIn = nil
	}
}

// Observe calls f after every change of State or SignedIn; the token
// removes it.
func (c *Controller) Observe(f func()) *board.ObserverToken { return c.observers.Add(f) }

// State is what the application's one suggested reply is doing.
func (c *Controller) State() board.SuggestReplyState { return c.state }

// SignedIn is whether Claude Code is signed in, as last asked; Known false
// when not known.
func (c *Controller) SignedIn() assistantpanel.SignIn { return c.signedIn }

// Created is the draft the request under way created and that is not
// linked yet ("", false without one); exported for the window's tests.
func (c *Controller) Created() (assistant.DraftRef, bool) {
	if c.created == nil {
		return assistant.DraftRef{}, false
	}
	return *c.created, true
}

// Idle reports that nothing runs and no call is on its way (the tests wait
// for it).
func (c *Controller) Idle() bool { return !c.state.IsRunning() && len(c.pending) == 0 }

// CanRun says whether the feature can run: available, the bridge and
// Claude Code there.
func (c *Controller) CanRun() bool {
	return c.available() && c.bridge != "" && c.locator.Locate() != ""
}

// View is the control for case k of snapshot s (board.SuggestReplyViewOf);
// samples: the board shows the invented samples.
func (c *Controller) View(k board.Case, s board.Snapshot, samples bool, words board.PanelWords, tr board.Translator) board.SuggestReplyView {
	return board.SuggestReplyViewOf(board.SuggestReplyInputs{
		Offered:     board.SuggestReplyOffered(k, s, samples),
		Available:   c.available() && c.bridge != "",
		ClaudeFound: c.locator.Locate() != "",
		SignedOut:   c.signedIn.Known && !c.signedIn.SignedIn,
		State:       c.state,
		Case:        k.ID,
	}, words, tr)
}

// Inputs.

// AvailabilityChanged says that whether the feature is available, or where
// Claude Code is, may have changed: a request that lost it stops, the
// sign-in is asked again, and the change reported.
func (c *Controller) AvailabilityChanged() {
	if c.state.IsRunning() && !c.CanRun() {
		c.log.Info("board reply: stopped, no longer available")
		c.Cancel()
	}
	c.CheckSignIn()
	c.observers.Notify()
}

// CheckSignIn asks Claude Code whether it is signed in (its cached answer
// unless a sign-in or locator.Refresh dropped it); a late answer that a
// later check, or a request's own finding, overtook is dropped.
func (c *Controller) CheckSignIn() {
	c.signInGen++
	g := c.signInGen
	if !c.available() || c.locator.Locate() == "" {
		c.setSignedIn(assistantpanel.SignIn{})
		return
	}
	c.locator.SignedIn(func(s assistantpanel.SignIn) {
		if g != c.signInGen {
			return
		}
		c.setSignedIn(s)
	})
}

func (c *Controller) learnSignedIn(s assistantpanel.SignIn) {
	c.signInGen++
	c.setSignedIn(s)
}

func (c *Controller) setSignedIn(s assistantpanel.SignIn) {
	if !s.Known {
		s = assistantpanel.SignIn{}
	}
	if s == c.signedIn {
		return
	}
	c.signedIn = s
	c.observers.Notify()
}

// A request.

// Start starts a suggested reply for k with the user's instruction (may be
// empty). False, and nothing changes, when one runs already or it cannot
// run (see the type's comment: no reply target, a reply already, or the
// feature unavailable — always true of the invented samples, whose
// Reply is nil).
func (c *Controller) Start(k board.Case, instruction string) bool {
	if c.state.IsRunning() || !c.CanRun() || k.Reply == nil || k.Draft != nil {
		return false
	}
	c.gen++
	my := c.gen
	c.draftCalls = map[string]bool{}
	c.created = nil
	c.linking = false
	c.setState(board.SuggestReplyState{Kind: board.SuggestRunning, Case: k.ID})
	c.log.Info("board reply: started")
	id := k.ID
	c.loop.Post(func() { c.run(my, id, instruction) })
	return true
}

// Cancel ends the request under way as cancelled; a draft it created and
// that is not being linked is deleted.
func (c *Controller) Cancel() {
	if !c.state.IsRunning() {
		return
	}
	c.log.Info("board reply: stopped")
	c.end(c.state.Case, board.ReplyCancelled)
}

// timedOut ends the request of my as a timeout; my's own Timeout has
// passed.
func (c *Controller) timedOut(my int) {
	if my != c.gen || !c.state.IsRunning() {
		return
	}
	c.log.Info("board reply: no draft in time")
	c.end(c.state.Case, board.ReplyTimeout)
}

// end ends the request under way from outside its own steps (Stop, the
// timeout): its process goes, a draft it created and that is not being
// linked is deleted.
func (c *Controller) end(id board.CaseID, failure board.SuggestReplyFailure) {
	c.gen++
	c.request.Cancel()
	if c.created != nil && !c.linking {
		c.delete(*c.created)
	}
	c.created = nil
	c.setState(board.SuggestReplyState{Kind: board.SuggestFailed, Case: id, Failure: failure})
}

// CancelAndCleanUp is Cancel, then waits until the board.setDraft and
// draft.delete calls on their way were answered, at most wait: quitting
// calls it before the connection stops, and never hangs on a daemon that
// does not answer. It blocks the main loop; what the answers post
// (OnRefresh) runs later.
func (c *Controller) CancelAndCleanUp(wait time.Duration) {
	c.Cancel()
	pending := make([]chan struct{}, 0, len(c.pending))
	for _, ch := range c.pending {
		pending = append(pending, ch)
	}
	if len(pending) == 0 {
		return
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for _, ch := range pending {
		select {
		case <-ch:
		case <-timer.C:
			return
		}
	}
}

// run is step 1: the assistant's consent, once ever; an answer counts even
// when the request was stopped while the question was up.
func (c *Controller) run(my int, id board.CaseID, instruction string) {
	if c.settings.AssistantConsent() {
		c.afterConsent(my, id, instruction)
		return
	}
	ask := c.Consent
	answer := func(allowed bool) {
		if allowed {
			c.settings.SetAssistantConsent(true)
		}
		if my != c.gen {
			return
		}
		if !allowed {
			c.setState(board.SuggestReplyState{})
			return
		}
		c.afterConsent(my, id, instruction)
	}
	if ask == nil {
		answer(false)
		return
	}
	ask(answer)
}

// afterConsent is where the time runs from (not while the consent question
// was up): the controller's own timer starts, then step 2.
func (c *Controller) afterConsent(my int, id board.CaseID, instruction string) {
	if my != c.gen {
		return
	}
	c.loop.After(c.Timeout, func() { c.timedOut(my) })
	caller := c.caller
	go func() {
		var r api.BoardGetResult
		err := callWith(caller, api.MethodBoardGet, api.BoardGetParams{CaseID: id}, &r)
		c.loop.Post(func() { c.gotCase(my, id, instruction, r, err) })
	}()
}

// gotCase is step 2's answer: a case with a suggested reply already ends
// quietly, otherwise step 3.
func (c *Controller) gotCase(my int, id board.CaseID, instruction string, r api.BoardGetResult, err error) {
	if err != nil {
		c.log.Info("board reply: board.get", "err", err)
		c.fail(my, id, board.ReplyBackend)
		return
	}
	if my != c.gen || c.bridge == "" {
		return
	}
	if r.Case.Draft != nil {
		// A suggested reply came meanwhile (the triage): it shows.
		c.log.Info("board reply: the case has a suggested reply already")
		c.setState(board.SuggestReplyState{})
		if c.OnRefresh != nil {
			c.OnRefresh()
		}
		return
	}
	others := make([]string, len(r.Messages))
	for i, m := range r.Messages {
		others[i] = string(m.ID)
	}
	message := assistant.SuggestReplyMessage(string(r.Case.AccountID), string(r.Case.ReplyMessageID), others, instruction)
	c.request.StartCall(assistantpanel.Call{
		SystemPrompt: assistant.SuggestReplySystemPrompt(),
		Message:      message,
		Tools: &assistantpanel.Tools{
			Bridge: c.bridge, Socket: c.socket,
			BridgeArgs: assistant.SuggestReplyBridgeArgs(string(r.Case.ReplyMessageID)),
			Allowed:    assistant.SuggestReplyTools,
		},
		Timeout: c.Timeout + 10*time.Second,
		// The assistant's model (assistant-model), not the board's triage
		// model: the user asked for this reply and waits for it, as for
		// the panel and the compose rewrite.
		Model:  c.settings.AssistantModel(),
		OnTool: func(e assistant.Event) { c.tool(my, e) },
		// No usage is recorded (see the type's comment).
		OnUsage: nil,
	}, func(o assistantpanel.Outcome) { c.answered(my, id, o) })
}

// tool notes the draft a create_draft of request my created.
func (c *Controller) tool(my int, e assistant.Event) {
	if my != c.gen {
		return
	}
	switch {
	case e.Kind == assistant.EventToolUse && e.Tool == assistant.SuggestReplyDraftTool:
		c.draftCalls[e.ToolUseID] = true
	case e.Kind == assistant.EventToolResult && c.draftCalls[e.ToolUseID]:
		delete(c.draftCalls, e.ToolUseID)
		if e.IsError || c.created != nil {
			return
		}
		if ref, ok := assistant.ParseDraftResult(e.ResultText); ok {
			c.created = &ref
		}
	}
}

// answered: the request of my ended.
func (c *Controller) answered(my int, id board.CaseID, o assistantpanel.Outcome) {
	if my != c.gen {
		return
	}
	ref := c.created
	switch {
	case o.Kind == assistantpanel.OutcomeDeclined:
		if ref != nil {
			c.delete(*ref)
		}
		c.created = nil
		c.setState(board.SuggestReplyState{})
	case o.Kind == assistantpanel.OutcomeFailed && o.Failure.TimedOut():
		if ref != nil {
			c.delete(*ref)
		}
		c.fail(my, id, board.ReplyTimeout)
	default:
		switch {
		case o.Kind == assistantpanel.OutcomeAnswered:
			c.learnSignedIn(assistantpanel.SignIn{Known: true, SignedIn: true})
		case o.Kind == assistantpanel.OutcomeFailed && o.Failure.Kind == assistantpanel.FailureNotSignedIn:
			c.learnSignedIn(assistantpanel.SignIn{Known: true})
		}
		if ref != nil {
			// The draft is there, whatever the turn did after it.
			c.link(my, id, *ref)
			return
		}
		if o.Kind == assistantpanel.OutcomeFailed {
			c.fail(my, id, replyFailureOf(o.Failure))
		} else {
			c.fail(my, id, board.ReplyNoDraft)
		}
	}
}

// replyFailureOf maps a request's failure to the board's.
func replyFailureOf(f assistantpanel.Failure) board.SuggestReplyFailure {
	switch f.Kind {
	case assistantpanel.FailureNotFound:
		return board.ReplyNotFound
	case assistantpanel.FailureNotSignedIn:
		return board.ReplyNotSignedIn
	case assistantpanel.FailureToolsMissing:
		return board.ReplyToolsMissing
	default:
		if f.TimedOut() {
			return board.ReplyTimeout
		}
		return board.ReplyStopped
	}
}

// link is step 4: board.setDraft for the created draft, then the board
// lists again; a refused link deletes the draft.
func (c *Controller) link(my int, id board.CaseID, ref assistant.DraftRef) {
	c.linking = true
	caller := c.caller
	c.track(func() error {
		return callWith(caller, api.MethodBoardSetDraft,
			api.BoardSetDraftParams{CaseID: id, DraftID: api.DraftID(ref.DraftID)}, new(api.BoardSetDraftResult))
	}, func(err error) {
		conflict := false
		if err != nil {
			var e *api.Error
			if errors.As(err, &e) {
				conflict = e.Code == api.CodeConflict
			}
			c.log.Info("board reply: board.setDraft", "err", err)
			c.delete(ref)
		}
		if c.OnRefresh != nil {
			c.OnRefresh()
		}
		if my != c.gen {
			return
		}
		c.linking = false
		c.created = nil
		switch {
		case err == nil:
			c.log.Info("board reply: linked")
			c.setState(board.SuggestReplyState{})
		case conflict:
			// The case got a suggested reply meanwhile: that one shows.
			c.setState(board.SuggestReplyState{})
		default:
			c.setState(board.SuggestReplyState{Kind: board.SuggestFailed, Case: id, Failure: board.ReplyBackend})
		}
	})
}

// fail ends request my with failure.
func (c *Controller) fail(my int, id board.CaseID, failure board.SuggestReplyFailure) {
	if my != c.gen {
		return
	}
	if failure == board.ReplyNotSignedIn {
		c.learnSignedIn(assistantpanel.SignIn{Known: true})
	}
	c.created = nil
	c.log.Info("board reply: failed", "failure", int(failure))
	c.setState(board.SuggestReplyState{Kind: board.SuggestFailed, Case: id, Failure: failure})
}

// delete deletes a draft the request created and that is not linked.
func (c *Controller) delete(ref assistant.DraftRef) {
	caller := c.caller
	c.track(func() error {
		return callWith(caller, api.MethodDraftDelete,
			api.DraftDeleteParams{AccountID: api.AccountID(ref.AccountID), DraftID: api.DraftID(ref.DraftID)}, new(api.DraftDeleteResult))
	}, func(err error) {
		if err != nil {
			c.log.Info("board reply: draft.delete", "err", err)
		}
	})
}

// track runs body on its own goroutine, keeping it in c.pending (closed as
// soon as body returns, before after runs: CancelAndCleanUp only waits for
// the daemon's round trip, not for after's own work on the main loop,
// which may run later); after then runs on the main loop with body's
// error.
func (c *Controller) track(body func() error, after func(err error)) {
	c.nextPending++
	key := c.nextPending
	done := make(chan struct{})
	c.pending[key] = done
	go func() {
		err := body()
		close(done)
		c.loop.Post(func() {
			delete(c.pending, key)
			after(err)
		})
	}()
}

// setState changes state, notifying only on a real change.
func (c *Controller) setState(s board.SuggestReplyState) {
	if s == c.state {
		return
	}
	c.state = s
	c.observers.Notify()
}
