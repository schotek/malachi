// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package agent

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Errors of Manager.Add.
var (
	ErrTooManySessions = errors.New("agent: too many open sessions")
	ErrTooManyForConn  = errors.New("agent: too many open sessions on this connection")
)

// Manager keeps the open sessions by id and connection, within the
// limits, and finds the idle ones.
type Manager struct {
	max, perConn int
	idle         time.Duration

	mu   sync.Mutex
	byID map[string]*entry
}

type entry struct {
	conn api.ConnID
	s    *Session
	last time.Time
}

// NewManager bounds the sessions of the daemon and of one connection and
// names how long an untouched session lives.
func NewManager(max, perConn int, idle time.Duration) *Manager {
	return &Manager{max: max, perConn: perConn, idle: idle, byID: map[string]*entry{}}
}

// NewID is a fresh session id ("as_" + 12 hex digits).
func NewID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "as_" + hex.EncodeToString(b[:])
}

// Add registers s for conn at now.
func (m *Manager) Add(conn api.ConnID, s *Session, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.byID) >= m.max {
		return ErrTooManySessions
	}
	n := 0
	for _, e := range m.byID {
		if e.conn == conn {
			n++
		}
	}
	if n >= m.perConn {
		return ErrTooManyForConn
	}
	m.byID[s.ID()] = &entry{conn: conn, s: s, last: now}
	return nil
}

// Get is the session id of conn, touched at now; false for another
// connection's or an unknown one.
func (m *Manager) Get(conn api.ConnID, id string, now time.Time) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.byID[id]
	if !ok || e.conn != conn {
		return nil, false
	}
	e.last = now
	return e.s, true
}

// Remove forgets the session id of conn and returns it.
func (m *Manager) Remove(conn api.ConnID, id string) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.byID[id]
	if !ok || e.conn != conn {
		return nil, false
	}
	delete(m.byID, id)
	return e.s, true
}

// RemoveConn forgets every session of conn and returns them.
func (m *Manager) RemoveConn(conn api.ConnID) []*Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*Session
	for id, e := range m.byID {
		if e.conn == conn {
			out = append(out, e.s)
			delete(m.byID, id)
		}
	}
	return out
}

// Sweep forgets and returns the sessions untouched for longer than the
// idle time (a running turn counts as touched) and the ones that ended.
func (m *Manager) Sweep(now time.Time) []*Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*Session
	for id, e := range m.byID {
		if e.s.Ended() || (!e.s.Running() && now.Sub(e.last) > m.idle) {
			out = append(out, e.s)
			delete(m.byID, id)
		}
	}
	return out
}

// All forgets and returns every session (shutdown).
func (m *Manager) All() []*Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Session, 0, len(m.byID))
	for id, e := range m.byID {
		out = append(out, e.s)
		delete(m.byID, id)
	}
	return out
}

// Len is how many sessions are open.
func (m *Manager) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.byID)
}
