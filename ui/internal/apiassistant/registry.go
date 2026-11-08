// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package apiassistant

import (
	"encoding/json"
	"sync"
	"time"

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
	// early holds the events of a session not open here yet: a turn sent
	// with assistant.sessionStart may notify before the start answers
	// with the id; add replays them. At most earlyMax events per id, and
	// an id nobody claimed within earlyFor is forgotten.
	early map[string]*earlyEvents
	// stops counts the assistant.sessionStop calls under way (Terminate
	// sends them in the background); Wait lets the application send them
	// before it closes the connection.
	stops sync.WaitGroup
}

// earlyEvents are the events of a session before it was added.
type earlyEvents struct {
	at     time.Time
	events []api.AssistantEvent
}

const (
	earlyMax = 256
	earlyFor = 30 * time.Second
)

// NewSessions is an empty registry.
func NewSessions() *Sessions {
	return &Sessions{open: map[string]*Session{}, early: map[string]*earlyEvents{}}
}

func (r *Sessions) add(s *Session) {
	r.mu.Lock()
	r.open[s.id] = s
	e := r.early[s.id]
	delete(r.early, s.id)
	r.mu.Unlock()
	if e != nil {
		for _, ev := range e.events {
			s.Handle(ev)
		}
	}
}

// keepEarly remembers an event of a session not open yet, and forgets
// the ids nobody claimed in time.
func (r *Sessions) keepEarly(e api.AssistantEvent) {
	now := time.Now()
	for id, early := range r.early {
		if now.Sub(early.at) > earlyFor {
			delete(r.early, id)
		}
	}
	early := r.early[e.SessionID]
	if early == nil {
		early = &earlyEvents{at: now}
		r.early[e.SessionID] = early
	}
	if len(early.events) < earlyMax {
		early.events = append(early.events, e)
	}
}

func (r *Sessions) remove(s *Session) {
	r.mu.Lock()
	if r.open[s.id] == s {
		delete(r.open, s.id)
	}
	r.mu.Unlock()
}

// Wait waits for the sessionStop calls under way (Terminate sends them
// in the background), at most d: true when all are through. The
// application calls it as it quits, after it ended the panel's and the
// requests' sessions and before it closes the connection, so that the
// daemon gets each stop (it would end the sessions with the connection
// anyway; the call then just fails on the closed connection).
func (r *Sessions) Wait(d time.Duration) bool {
	done := make(chan struct{})
	go func() {
		r.stops.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

// Len is the number of open sessions.
func (r *Sessions) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.open)
}

// Handle takes a notification of the daemon: true when it was a
// notify.assistantEvent (handled, or kept for a session not open here
// yet); false for any other method, which the caller dispatches on.
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
	if s == nil && e.SessionID != "" {
		r.keepEarly(e)
	}
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
