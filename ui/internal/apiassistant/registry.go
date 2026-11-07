// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package apiassistant

import (
	"encoding/json"
	"sync"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Sessions are the open sessions of the daemon's assistant, by the
// daemon's session id: the window gives every notification to Handle
// from its dispatcher (any goroutine), and a notify.assistantEvent reaches
// its session. One per application (per connection: a session belongs to
// the connection that started it).
type Sessions struct {
	mu   sync.Mutex
	open map[string]*Session
}

// NewSessions is an empty registry.
func NewSessions() *Sessions {
	return &Sessions{open: map[string]*Session{}}
}

func (r *Sessions) add(s *Session) {
	r.mu.Lock()
	r.open[s.id] = s
	r.mu.Unlock()
}

func (r *Sessions) remove(s *Session) {
	r.mu.Lock()
	if r.open[s.id] == s {
		delete(r.open, s.id)
	}
	r.mu.Unlock()
}

// Len is the number of open sessions.
func (r *Sessions) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.open)
}

// Handle takes a notification of the daemon: true when it was a
// notify.assistantEvent (handled, or dropped for a session not open
// here); false for any other method, which the caller dispatches on.
func (r *Sessions) Handle(method string, params json.RawMessage) bool {
	if method != api.NotifyAssistantEvent {
		return false
	}
	var e api.AssistantEvent
	if err := json.Unmarshal(params, &e); err != nil {
		return true
	}
	r.mu.Lock()
	s := r.open[e.SessionID]
	r.mu.Unlock()
	if s != nil {
		s.Handle(e)
	}
	return true
}

// Disconnected: the connection to the daemon went, and every session with
// it (the daemon ends them itself); each ends with "api_stopped".
func (r *Sessions) Disconnected() {
	r.mu.Lock()
	open := make([]*Session, 0, len(r.open))
	for _, s := range r.open {
		open = append(open, s)
	}
	r.mu.Unlock()
	for _, s := range open {
		s.Disconnected()
	}
}
