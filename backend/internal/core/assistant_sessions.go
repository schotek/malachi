// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/agent"
	"github.com/schotek/malachi/backend/internal/assistant"
	"github.com/schotek/malachi/backend/internal/llm"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The daemon's own assistant over ai accounts (docs/api.md §4.18;
// docs/security.md §10.4; docs/plans/ai-api.md D2, D5, D7, D12): the
// sessions of the connected clients, kept per RPC connection and ended
// with it, each a conversation of internal/agent over a model API client
// of internal/llm with the MCP bridge as its tools. The key leaves the
// keyring only into the session's client. Nothing of a conversation is
// logged: only session ids, purposes and failure classes.

const (
	assistantClientName = "malachi-mail" // initialize.clientInfo.name towards the bridge
	bridgeStartTimeout  = 30 * time.Second
	assistantSweep      = time.Minute
	modelsCacheTTL      = time.Hour
	assistantUsageDays  = 400 // days of usage kept
)

// runIDRe is the shape of a board run id the bridge accepts (--triage-run).
var runIDRe = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// assistantState is the Backend's part of it.
type assistantState struct {
	mgr *agent.Manager

	mu       sync.Mutex
	info     map[string]*sessionInfo       // by session id
	models   map[api.AccountID]modelsEntry // assistant.providerModels cache
	runUsage map[string]api.AssistantUsage // tokens of triage sessions by run id (D19)
	// status is what the last test or session of an ai account said about
	// its key: authRequired (refused) or error; absent = idle
	// (docs/api.md §4.1).
	status map[api.AccountID]api.SyncStatus
}

// sessionInfo is what the service keeps beside an agent.Session.
type sessionInfo struct {
	conn    api.ConnID
	account api.AccountID
	purpose api.PromptPurpose
	opts    assistant.Options
	model   string
	runID   string
	turns   int
	usage   api.AssistantUsage // recorded so far
	// counted: the session is in the store's session count (the first
	// turn with tokens wrote it).
	counted bool
}

type modelsEntry struct {
	models []api.AIModel
	at     time.Time
}

func newAssistantState() assistantState {
	return assistantState{
		mgr:      agent.NewManager(api.MaxAssistantSessions, api.MaxAssistantSessionsPerConn, api.AssistantSessionIdle),
		info:     map[string]*sessionInfo{},
		models:   map[api.AccountID]modelsEntry{},
		runUsage: map[string]api.AssistantUsage{},
		status:   map[api.AccountID]api.SyncStatus{},
	}
}

// setStatus records the outcome of a test or turn of an ai account:
// "" clears it.
func (st *assistantState) setStatus(id api.AccountID, status api.SyncStatus) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if status == "" {
		delete(st.status, id)
		return
	}
	st.status[id] = status
}

// aiStatus is the recorded status of an ai account, "" for idle.
func (st *assistantState) aiStatus(id api.AccountID) api.SyncStatus {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.status[id]
}

// aiOutcome says what a test or turn of an ai account tells about the
// account's state: a refused key (401, authFailed) is authRequired, any
// other answer of the provider clears it (the key was accepted; a turn
// that failed otherwise is reported where it happened, never as the
// state of an account that synchronises nothing), and a failure before
// the provider answered (no connection, a timeout, TLS, the bridge, a
// stop) says nothing about it (known false).
func aiOutcome(err error) (status api.SyncStatus, known bool) {
	if err == nil {
		return "", true
	}
	var ae *api.Error
	if errors.As(err, &ae) {
		switch ae.Code {
		case api.CodeAuthFailed:
			return api.SyncAuthRequired, true
		case api.CodeNetworkError, api.CodeServerTimeout, api.CodeTLSError, api.CodeCancelled:
			return "", false
		}
		return "", true
	}
	switch llm.ClassOf(err) {
	case api.FailureAuth:
		return api.SyncAuthRequired, true
	case api.FailureNetwork, api.FailureTimeout, api.FailureStopped, api.FailureProtocol, api.FailureBridge:
		return "", false
	}
	return "", true
}

func (st *assistantState) put(id string, info *sessionInfo) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.info[id] = info
}

func (st *assistantState) get(id string) *sessionInfo {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.info[id]
}

func (st *assistantState) drop(id string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	delete(st.info, id)
}

// takeRunUsage returns and forgets the tokens the daemon's triage
// sessions of a board run used (board.runEnd without usage, D19).
func (st *assistantState) takeRunUsage(runID string) (api.AssistantUsage, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	u, ok := st.runUsage[runID]
	delete(st.runUsage, runID)
	return u, ok
}

// assistantEmit sends an event to one connection; a connection that is
// gone drops it.
func (b *Backend) assistantEmit(conn api.ConnID, ev api.AssistantEvent) {
	if n, ok := b.getNotifier().(api.AssistantEventNotifier); ok {
		n.AssistantEvent(conn, ev)
	}
}

// SessionStart is assistant.sessionStart.
func (s *assistantService) SessionStart(ctx context.Context, p api.AssistantSessionStartParams) (*api.AssistantSessionStartResult, error) {
	b := s.b
	conn, ok := api.ConnFromContext(ctx)
	if !ok {
		return nil, api.NewError(api.CodeInvalidArgument, "an assistant session needs an RPC connection")
	}
	if !p.Purpose.Valid() {
		return nil, api.NewError(api.CodeInvalidArgument, "unknown purpose %q", p.Purpose)
	}
	a, err := b.requireAccount(ctx, string(p.AccountID))
	if err != nil {
		return nil, err
	}
	if a.Config.Protocol() != api.AccountAI || a.Config.AI == nil {
		return nil, api.NewError(api.CodeInvalidArgument, "account %q is not an ai account", p.AccountID)
	}
	if !a.Enabled {
		return nil, api.NewError(api.CodeUnavailable, "account %q is paused", p.AccountID)
	}
	cfg := *a.Config.AI
	if !cfg.Consented() {
		return nil, api.NewError(api.CodeAssistantConsentRequired, "the account's user has not accepted the consent text (version %d)", api.AssistantConsentVersion)
	}
	if p.Purpose == api.PurposeTriage && !runIDRe.MatchString(p.RunID) {
		return nil, api.NewError(api.CodeInvalidArgument, "a triage session needs the board run's runId")
	}
	if p.Purpose == api.PurposeReply && (p.Input == nil || p.Input.Reply == nil) {
		return nil, api.NewError(api.CodeInvalidArgument, "a reply session needs input.reply")
	}
	if p.TimeoutSeconds < 0 || time.Duration(p.TimeoutSeconds)*time.Second > api.MaxAssistantTimeout {
		return nil, api.NewError(api.CodeInvalidArgument, "timeoutSeconds must be 0..%d", int(api.MaxAssistantTimeout/time.Second))
	}
	key, err := b.aiKeyFor(ctx, a.Config, p.AccountID, "")
	if err != nil {
		return nil, err
	}
	opts, err := b.promptOptions(ctx, p.Language, p.NoteAccountID, p.Trigger, p.RunID)
	if err != nil {
		return nil, err
	}
	res, err := assistant.Build(p.Purpose, p.Input, opts)
	if err != nil {
		return nil, err
	}
	prov, err := b.aiProvider(cfg, key)
	if err != nil {
		return nil, llm.ToAPIError(err)
	}
	timeout := api.DefaultAssistantTimeout
	if p.Purpose == api.PurposeTriage {
		timeout = api.MaxAssistantTimeout
	}
	if p.TimeoutSeconds > 0 {
		timeout = time.Duration(p.TimeoutSeconds) * time.Second
	}
	model := cfg.Model
	if p.Purpose == api.PurposeTriage && cfg.BoardModel != "" {
		model = cfg.BoardModel
	}

	var tools agent.Tools
	if len(res.Tools) > 0 {
		br, err := b.startBridge(ctx, p.Bridge, res.BridgeArgs, res.Tools)
		if err != nil {
			return nil, err
		}
		tools = br
	}
	id := agent.NewID()
	sess := agent.New(agent.Config{
		ID: id, Provider: prov, Model: model, System: res.SystemPrompt, JSONSchema: res.JSONSchema,
		Tools: tools, Allowed: res.Tools, Timeout: timeout,
		Emit: func(ev api.AssistantEvent) { b.assistantEmit(conn, ev) },
		Log:  b.log.With("component", "assistant"),
	})
	if err := b.assist.mgr.Add(conn, sess, time.Now()); err != nil {
		sess.Stop()
		switch {
		case errors.Is(err, agent.ErrTooManyForConn):
			return nil, api.NewError(api.CodeInvalidArgument, "at most %d sessions per connection", api.MaxAssistantSessionsPerConn)
		default:
			return nil, api.NewError(api.CodeAssistantUnavailable, "at most %d open sessions", api.MaxAssistantSessions)
		}
	}
	info := &sessionInfo{conn: conn, account: p.AccountID, purpose: p.Purpose, opts: opts, model: model, runID: p.RunID}
	b.assist.put(id, info)
	b.log.Info("assistant session started", "session", id, "purpose", p.Purpose, "provider", cfg.Provider, "tools", len(sess.ToolNames()))
	if p.Input != nil {
		if res.Message == "" {
			b.endSession(conn, id, sess)
			return nil, api.NewError(api.CodeInvalidArgument, "the input makes no message")
		}
		if err := b.beginTurn(sess, info, res.Message); err != nil {
			b.endSession(conn, id, sess)
			return nil, err
		}
	}
	return &api.AssistantSessionStartResult{SessionID: id, Tools: sess.ToolNames()}, nil
}

// startBridge starts the MCP bridge for a session with tools and checks
// that it offers at least one tool of the purpose.
func (b *Backend) startBridge(ctx context.Context, path string, args, wanted []string) (*agent.Bridge, error) {
	if path == "" || !filepath.IsAbs(path) {
		return nil, api.NewError(api.CodeInvalidArgument, "bridge must be the absolute path of malachi-mcp")
	}
	if fi, err := os.Stat(path); err != nil || !fi.Mode().IsRegular() {
		return nil, api.NewError(api.CodeInvalidArgument, "bridge is not an executable file")
	}
	if b.SocketPath == "" {
		return nil, api.NewError(api.CodeAssistantUnavailable, "the daemon has no socket for the bridge")
	}
	bctx, cancel := context.WithTimeout(ctx, bridgeStartTimeout)
	defer cancel()
	br, err := agent.StartBridge(bctx, agent.BridgeConfig{Path: path, Socket: b.SocketPath, Args: args, Name: assistantClientName, Version: b.Version})
	if err != nil {
		b.log.Warn("assistant bridge did not start", "err", err.Error())
		return nil, api.NewError(api.CodeAssistantUnavailable, "the MCP bridge could not be started")
	}
	have := map[string]bool{}
	for _, t := range br.Tools() {
		have[t.Name] = true
	}
	for _, name := range wanted {
		if have[name] {
			return br, nil
		}
	}
	_ = br.Close()
	return nil, api.NewError(api.CodeAssistantUnavailable, "the MCP bridge offers none of the purpose's tools")
}

// beginTurn starts a turn and records its usage when it ends.
func (b *Backend) beginTurn(sess *agent.Session, info *sessionInfo, message string) error {
	b.assist.mu.Lock()
	info.turns++
	b.assist.mu.Unlock()
	err := sess.Begin(context.Background(), message, func(err error) {
		b.recordUsage(sess, info)
		if s, ok := aiOutcome(err); ok {
			b.assist.setStatus(info.account, s)
		}
		if sess.Ended() {
			b.endSession(info.conn, sess.ID(), sess)
		}
	})
	switch {
	case errors.Is(err, agent.ErrBusy):
		return api.NewError(api.CodeAssistantSessionBusy, "the session's turn is still running")
	case errors.Is(err, agent.ErrEnded):
		return api.NewError(api.CodeAssistantSessionNotFound, "the session ended")
	}
	return err
}

// endSession forgets a session and stops it.
func (b *Backend) endSession(conn api.ConnID, id string, sess *agent.Session) {
	b.assist.mgr.Remove(conn, id)
	b.assist.drop(id)
	sess.Stop()
}

// SessionSend is assistant.sessionSend.
func (s *assistantService) SessionSend(ctx context.Context, p api.AssistantSessionSendParams) (*api.AssistantSessionSendResult, error) {
	b := s.b
	conn, ok := api.ConnFromContext(ctx)
	if !ok {
		return nil, api.NewError(api.CodeInvalidArgument, "an assistant session needs an RPC connection")
	}
	sess, ok := b.assist.mgr.Get(conn, p.SessionID, time.Now())
	info := b.assist.get(p.SessionID)
	if !ok || info == nil || sess.Ended() {
		if ok {
			b.endSession(conn, p.SessionID, sess)
		}
		return nil, api.NewError(api.CodeAssistantSessionNotFound, "no open session %q on this connection", p.SessionID)
	}
	b.assist.mu.Lock()
	turns := info.turns
	b.assist.mu.Unlock()
	if info.purpose != api.PurposePanel && turns >= 1 {
		return nil, api.NewError(api.CodeInvalidArgument, "a %s session takes one turn", info.purpose)
	}
	res, err := assistant.Build(info.purpose, &p.Input, info.opts)
	if err != nil {
		return nil, err
	}
	if res.Message == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "the input makes no message")
	}
	if err := b.beginTurn(sess, info, res.Message); err != nil {
		return nil, err
	}
	return &api.AssistantSessionSendResult{}, nil
}

// SessionStop is assistant.sessionStop.
func (s *assistantService) SessionStop(ctx context.Context, p api.AssistantSessionStopParams) (*api.AssistantSessionStopResult, error) {
	b := s.b
	conn, ok := api.ConnFromContext(ctx)
	if !ok {
		return nil, api.NewError(api.CodeInvalidArgument, "an assistant session needs an RPC connection")
	}
	sess, ok := b.assist.mgr.Get(conn, p.SessionID, time.Now())
	if !ok {
		return nil, api.NewError(api.CodeAssistantSessionNotFound, "no open session %q on this connection", p.SessionID)
	}
	b.endSession(conn, p.SessionID, sess)
	b.log.Info("assistant session stopped", "session", p.SessionID)
	return &api.AssistantSessionStopResult{}, nil
}

// ProviderModels is assistant.providerModels.
func (s *assistantService) ProviderModels(ctx context.Context, p api.AssistantProviderModelsParams) (*api.AssistantProviderModelsResult, error) {
	b := s.b
	a, err := b.requireAccount(ctx, string(p.AccountID))
	if err != nil {
		return nil, err
	}
	if a.Config.Protocol() != api.AccountAI || a.Config.AI == nil {
		return nil, api.NewError(api.CodeInvalidArgument, "account %q is not an ai account", p.AccountID)
	}
	if !p.Refresh {
		b.assist.mu.Lock()
		e, ok := b.assist.models[p.AccountID]
		b.assist.mu.Unlock()
		if ok && time.Since(e.at) < modelsCacheTTL {
			return &api.AssistantProviderModelsResult{Models: append([]api.AIModel{}, e.models...)}, nil
		}
	}
	key, err := b.aiKeyFor(ctx, a.Config, p.AccountID, "")
	if err != nil {
		return nil, err
	}
	models, err := b.aiModels(ctx, *a.Config.AI, key)
	if err != nil {
		return nil, err
	}
	if models == nil {
		models = []api.AIModel{}
	}
	b.assist.mu.Lock()
	b.assist.models[p.AccountID] = modelsEntry{models: models, at: time.Now()}
	b.assist.mu.Unlock()
	return &api.AssistantProviderModelsResult{Models: append([]api.AIModel{}, models...)}, nil
}

// ConnClosed ends the sessions of a connection that went away
// (api.ConnObserver).
func (b *Backend) ConnClosed(id api.ConnID) {
	for _, sess := range b.assist.mgr.RemoveConn(id) {
		b.assist.drop(sess.ID())
		sess.Stop()
	}
}

// forgetAIAccount ends the sessions of an account that is removed or
// paused and drops its cached models.
func (b *Backend) forgetAIAccount(id api.AccountID) {
	b.assist.mu.Lock()
	delete(b.assist.models, id)
	delete(b.assist.status, id)
	var ids []string
	for sid, info := range b.assist.info {
		if info.account == id {
			ids = append(ids, sid)
		}
	}
	b.assist.mu.Unlock()
	for _, sid := range ids {
		info := b.assist.get(sid)
		if info == nil {
			continue
		}
		if sess, ok := b.assist.mgr.Remove(info.conn, sid); ok {
			b.assist.drop(sid)
			sess.Stop()
		}
	}
}

// startAssistant runs the sweeper: idle and ended sessions go every
// minute, old usage rows every hour; at the end every session stops.
func (b *Backend) startAssistant(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(assistantSweep)
		defer t.Stop()
		lastPrune := time.Time{}
		for {
			select {
			case <-ctx.Done():
				b.stopAssistant()
				return
			case now := <-t.C:
				for _, sess := range b.assist.mgr.Sweep(now) {
					b.assist.drop(sess.ID())
					sess.Stop()
				}
				if now.Sub(lastPrune) >= time.Hour {
					lastPrune = now
					before := now.AddDate(0, 0, -assistantUsageDays).Format("2006-01-02")
					if n, err := b.store.PruneAssistantUsage(ctx, before); err != nil {
						b.log.Warn("prune assistant usage", "err", err)
					} else if n > 0 {
						b.log.Info("assistant usage pruned", "rows", n)
					}
				}
			}
		}
	}()
	return done
}

// stopAssistant ends every session (shutdown).
func (b *Backend) stopAssistant() {
	for _, sess := range b.assist.mgr.All() {
		b.assist.drop(sess.ID())
		sess.Stop()
	}
}
