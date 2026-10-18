// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package eway

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"sync"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// SupervisorDeps are the supervisor's outside world.
type SupervisorDeps struct {
	// Password is the account's password from the keyring.
	Password func(ctx context.Context, accountID string) (string, error)
	// ClientID is the daemon's clientMachineIdentifier (D5).
	ClientID func(ctx context.Context) string
	HTTP     *http.Client
	// UserAgent ("" = DefaultUserAgent).
	UserAgent string
	Log       *slog.Logger
}

// Supervisor is the entry of the eway kind in the daemon's table of
// supervisors (D21): an eway account synchronises nothing. Start builds
// the account's client (no network), State is its last known sign-in
// state, Trigger does nothing, Stop signs out.
type Supervisor struct {
	deps SupervisorDeps

	mu      sync.Mutex
	clients map[string]*Client
	ctx     context.Context
}

// NewSupervisor builds the supervisor.
func NewSupervisor(d SupervisorDeps) *Supervisor {
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	return &Supervisor{deps: d, clients: map[string]*Client{}, ctx: context.Background()}
}

// Run waits for ctx and then signs every account out.
func (s *Supervisor) Run(ctx context.Context) {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
	<-ctx.Done()
	s.mu.Lock()
	clients := s.clients
	s.clients = map[string]*Client{}
	s.mu.Unlock()
	var wg sync.WaitGroup
	for _, c := range clients {
		wg.Add(1)
		go func() { defer wg.Done(); c.Close(context.WithoutCancel(ctx)) }()
	}
	wg.Wait()
}

// Start builds the account's client; a second Start is a no-op.
func (s *Supervisor) Start(a store.Account) {
	if a.Config.Protocol() != api.AccountEway || a.Config.Eway == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.clients[a.ID]; ok {
		return
	}
	c, err := s.newClient(a)
	if err != nil {
		s.deps.Log.Warn("eway account not started", "account", a.ID, "code", asAPI(err).Code.String())
		return
	}
	s.clients[a.ID] = c
}

func (s *Supervisor) newClient(a store.Account) (*Client, error) {
	id := a.ID
	clientID := ""
	if s.deps.ClientID != nil {
		clientID = s.deps.ClientID(context.Background())
	}
	return NewClient(Options{
		ServiceURL: a.Config.Eway.ServiceURL,
		Username:   a.Config.Eway.Username,
		Password: func(ctx context.Context) (string, error) {
			if s.deps.Password == nil {
				return "", api.NewError(api.CodeAuthRequired, "eway: no password")
			}
			return s.deps.Password(ctx, id)
		},
		ClientID:  clientID,
		HTTP:      s.deps.HTTP,
		UserAgent: s.deps.UserAgent,
		Log:       s.deps.Log.With("account", id),
	})
}

// Stop forgets the account's client and closes it in the background: a
// call still running cannot sign in again, its live session is logged
// out, and background work of the account is cancelled.
func (s *Supervisor) Stop(accountID string) {
	s.mu.Lock()
	c := s.clients[accountID]
	delete(s.clients, accountID)
	ctx := s.ctx
	s.mu.Unlock()
	if c != nil {
		go c.Close(context.WithoutCancel(ctx))
	}
}

// Restart applies a changed configuration (a new client: the sign-in
// latch is cleared by an edit).
func (s *Supervisor) Restart(a store.Account) {
	s.Stop(a.ID)
	s.Start(a)
}

func (s *Supervisor) Reload() {}

// Trigger: nothing to synchronise.
func (s *Supervisor) Trigger(string, api.FolderID, bool) bool { return false }

// State is the account's last known sign-in state.
func (s *Supervisor) State(accountID string) (api.SyncState, bool) {
	s.mu.Lock()
	c := s.clients[accountID]
	s.mu.Unlock()
	if c == nil {
		return api.SyncState{}, false
	}
	return c.State(accountID), true
}

func (s *Supervisor) States() []api.SyncState {
	s.mu.Lock()
	ids := make([]string, 0, len(s.clients))
	for id := range s.clients {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	sort.Strings(ids)
	var out []api.SyncState
	for _, id := range ids {
		if st, ok := s.State(id); ok {
			out = append(out, st)
		}
	}
	return out
}

// Client is the running account's client; false when the account is not
// started (paused, removed, not an eway account).
func (s *Supervisor) Client(accountID string) (*Client, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.clients[accountID]
	return c, ok
}
