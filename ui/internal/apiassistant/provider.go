// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package apiassistant is the assistant the daemon runs itself over an ai
// account (docs/api.md §4.18, docs/plans/ai-api.md D7, E1.2) as a
// provider of the panel's protocol (assistantpanel.Provider): Open is
// assistant.sessionStart, a Session's Submit assistant.sessionSend,
// Terminate assistant.sessionStop, and every notify.assistantEvent of the
// session, routed to it by Sessions, is turned into the assistant.Events
// the panel, the one-shot requests, the board's triage and the suggested
// reply already read (what ui/internal/chatgpt's session emits for
// Codex). The daemon builds the prompts, calls the model and runs the
// bridge; nothing here sees a key or a model's text beyond passing the
// events on. It knows no GTK: the window owns the provider per ai account
// and feeds Sessions from its notification dispatcher.
package apiassistant

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistantpanel"
)

// Caller calls the daemon (the window's RPC client; the method set of
// assistantpanel.Caller and board.Caller).
type Caller = assistantpanel.Caller

// Loop runs callbacks on the application's main loop (assistantpanel.Loop).
type Loop = assistantpanel.Loop

// callTimeout bounds one call to the daemon other than a session's start
// (the start waits for the bridge: startTimeout).
const callTimeout = 15 * time.Second

// startTimeout bounds assistant.sessionStart: the daemon starts the
// bridge and lists its tools first.
const startTimeout = 60 * time.Second

// Provider is the daemon's assistant over one ai account. One per account
// and window; the window makes it from account.list and replaces it when
// the account changes (its name, model or consent).
type Provider struct {
	caller   Caller
	loop     Loop
	sessions *Sessions
	log      *slog.Logger

	mu      sync.Mutex
	account api.Account
	// consent is the account.update of AcceptConsent on its way (nil
	// when none): Open waits for it before it starts a session, so the
	// daemon sees the consent.
	consent *consentWrite
}

// consentWrite is one AcceptConsent: done closes when the daemon answered,
// err is its answer.
type consentWrite struct {
	done chan struct{}
	err  error
}

// New is the provider of account (kind ai) over caller; its sessions
// register with sessions, which the window feeds with the daemon's
// notifications. log may be nil.
func New(caller Caller, loop Loop, account api.Account, sessions *Sessions, log *slog.Logger) *Provider {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Provider{caller: caller, loop: loop, account: account, sessions: sessions, log: log}
}

// Account is the account as the provider has it (the consent as last
// accepted here).
func (p *Provider) Account() api.Account {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.account
}

// AccountID is the account's id.
func (p *Provider) AccountID() api.AccountID { return p.Account().ID }

// Name is the account's name for the panel's subtitle (its address, or
// its id, when it has none).
func (p *Provider) Name() string {
	a := p.Account()
	switch {
	case a.Config.Name != "":
		return a.Config.Name
	case a.Config.Email != "":
		return a.Config.Email
	}
	return string(a.ID)
}

// Model is the account's model (AIConfig.Model), "" without an AIConfig.
func (p *Provider) Model() string {
	a := p.Account()
	if a.Config.AI == nil {
		return ""
	}
	return a.Config.AI.Model
}

// HasConsent says whether the user accepted the current consent text for
// the account (AIConfig.Consented, or AcceptConsent here).
func (p *Provider) HasConsent() bool {
	a := p.Account()
	return a.Config.AI != nil && a.Config.AI.Consented()
}

// AcceptConsent records the user's consent: account.update with the
// config as stored and the consent's version (the daemon sets
// acceptedAt; no credentials, so the key stays). The write goes in the
// background; the provider counts the consent as given at once, and Open
// waits for the write (a refused one fails the session with its reason).
func (p *Provider) AcceptConsent() {
	p.mu.Lock()
	if p.account.Config.AI == nil {
		p.mu.Unlock()
		return
	}
	cfg := p.account.Config
	ai := *cfg.AI
	ai.Consent = &api.AIConsent{Version: api.AssistantConsentVersion}
	cfg.AI = &ai
	p.account.Config = cfg
	w := &consentWrite{done: make(chan struct{})}
	p.consent = w
	id := p.account.ID
	p.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		var res api.AccountUpdateResult
		err := p.caller.Call(ctx, api.MethodAccountUpdate, api.AccountUpdateParams{AccountID: id, Config: cfg}, &res)
		if err != nil {
			p.log.Warn("ai account: consent", "err", err)
		}
		w.err = err
		close(w.done)
		p.mu.Lock()
		if p.consent == w {
			p.consent = nil
		}
		p.mu.Unlock()
	}()
}

// awaitConsent waits for the consent write on its way, if any.
func (p *Provider) awaitConsent(ctx context.Context) error {
	p.mu.Lock()
	w := p.consent
	p.mu.Unlock()
	if w == nil {
		return nil
	}
	select {
	case <-w.done:
		return w.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Open starts a session of spec over the account (assistant.sessionStart
// with the purpose, the language, the bridge's path, the note account,
// the triage's trigger and run and the turn's timeout; no input: the
// first turn is Submit's) and registers it for the daemon's events. An
// error is the daemon's (assistantConsentRequired, authRequired,
// assistantUnavailable, assistantToolsUnsupported, …), which
// Failure reads (assistantpanel.ProviderFailure of its reason).
func (p *Provider) Open(ctx context.Context, spec assistantpanel.SessionSpec) (assistantpanel.Session, error) {
	if !p.HasConsent() {
		return nil, errors.New(reasonConsentRequired)
	}
	if err := p.awaitConsent(ctx); err != nil {
		return nil, failure(err)
	}
	params := api.AssistantSessionStartParams{
		AccountID: p.AccountID(), Purpose: spec.Purpose, Language: spec.Language, Bridge: spec.BridgePath,
		NoteAccountID: spec.NoteAccountID, Trigger: spec.Trigger, RunID: spec.RunID,
	}
	if spec.Timeout > 0 {
		params.TimeoutSeconds = int((spec.Timeout + time.Second - 1) / time.Second)
	}
	ctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	var res api.AssistantSessionStartResult
	if err := p.caller.Call(ctx, api.MethodAssistantSessionStart, params, &res); err != nil {
		return nil, failure(err)
	}
	if res.SessionID == "" {
		return nil, errors.New(reasonProtocol)
	}
	s := newSession(p, res.SessionID, res.Tools)
	if p.sessions != nil {
		p.sessions.add(s)
	}
	return s, nil
}

// The reasons of the provider's own failures, read by
// assistantpanel.ProviderFailure as the daemon's classes are.
const (
	reasonConsentRequired = "api_consent_required"
	reasonProtocol        = "api_protocol"
	reasonStopped         = "api_stopped"
)

// failure is the reason of a daemon's error: the class the error code
// stands for ("api_consent_required", "api_auth", …), the message of
// any other api.Error, else the error as it is.
func failure(err error) error {
	var e *api.Error
	if !errors.As(err, &e) {
		return err
	}
	switch e.Code {
	case api.CodeAssistantConsentRequired:
		return errors.New(reasonConsentRequired)
	case api.CodeAuthRequired:
		return errors.New(reasonOf(api.FailureAuth))
	case api.CodeAssistantToolsUnsupported:
		return errors.New(reasonOf(api.FailureToolsUnsupported))
	case api.CodeAssistantUnavailable:
		return errors.New(reasonOf(api.FailureBridge))
	case api.CodeAssistantSessionNotFound:
		return errors.New(reasonStopped)
	}
	return fmt.Errorf("%s", e.Message)
}

// reasonOf is the reason of a failure class: "api_" and the class.
func reasonOf(class api.AssistantFailureClass) string {
	return "api_" + string(class)
}

var _ assistantpanel.Provider = (*Provider)(nil)
