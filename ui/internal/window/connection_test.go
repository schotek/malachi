// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/client"
)

func mismatchError(daemon int) error {
	return &api.HandshakeError{Reason: api.HandshakeProtocolMismatch, Daemon: daemon}
}

// A mismatch is shown until an attempt ends otherwise: the retries every
// reconnectInterval go through Connecting and must not make the line
// flicker.
func TestNextConnView(t *testing.T) {
	dial := errors.New("dial unix rpc.sock: connect: no such file or directory")
	protocol1 := fmt.Sprintf("Protocol mismatch: UI %d, backend 1", api.ProtocolVersion)
	later := api.ProtocolVersion + 1
	protocolLater := fmt.Sprintf("Protocol mismatch: UI %d, backend %d", api.ProtocolVersion, later)
	v := connView{State: client.Connecting} // as the window starts
	for i, step := range []struct {
		state    client.State
		err      error
		mismatch int
		line     string
	}{
		{client.Disconnected, dial, 0, "Backend unavailable"},
		{client.Connecting, nil, 0, "Connecting to backend…"},
		{client.Disconnected, mismatchError(1), 1, protocol1},
		{client.Connecting, nil, 1, protocol1},
		{client.Disconnected, mismatchError(1), 1, protocol1},
		{client.Connecting, nil, 1, protocol1},
		// An attempt that ends otherwise replaces the mismatch.
		{client.Disconnected, &api.HandshakeError{Reason: api.HandshakeTimedOut}, 0, "Backend unavailable"},
		{client.Connecting, nil, 0, "Connecting to backend…"},
		{client.Disconnected, fmt.Errorf("connect: %w", mismatchError(later)), later, protocolLater},
		{client.Connecting, nil, later, protocolLater},
		{client.Disconnected, dial, 0, "Backend unavailable"},
		{client.Connecting, nil, 0, "Connecting to backend…"},
		{client.Disconnected, mismatchError(1), 1, protocol1},
		{client.Connecting, nil, 1, protocol1},
		{client.Connected, nil, 0, "Up to date"},
		{client.Disconnected, io.EOF, 0, "Backend unavailable"},
	} {
		v = nextConnView(v, step.state, step.err)
		if v.State != step.state || v.Mismatch != step.mismatch {
			t.Errorf("step %d (%s, %v): state %s, mismatch %d; want mismatch %d", i, step.state, step.err, v.State, v.Mismatch, step.mismatch)
		}
		if got := statusLineFor(v, "Up to date", false).Text; got != step.line {
			t.Errorf("step %d (%s, %v): line %q, want %q", i, step.state, step.err, got, step.line)
		}
	}

	// What system.info and sync.status said goes with every change.
	said := connView{State: client.Connected, Info: &api.SystemInfoResult{ProtocolVersion: api.ProtocolVersion}, InfoFailed: true, SyncFailed: true}
	for _, s := range []client.State{client.Connecting, client.Connected, client.Disconnected} {
		if got := nextConnView(said, s, nil); got != (connView{State: s}) {
			t.Errorf("after %s: %+v", s, got)
		}
	}
}

func TestBackendProtocol(t *testing.T) {
	same := &api.SystemInfoResult{ProtocolVersion: api.ProtocolVersion}
	later := &api.SystemInfoResult{ProtocolVersion: api.ProtocolVersion + 1}
	for _, c := range []struct {
		name     string
		conn     connView
		protocol int
		mismatch bool
	}{
		{"connected", connView{State: client.Connected, Info: same}, 0, false},
		{"system.info pending", connView{State: client.Connected}, 0, false},
		{"handshake", connView{State: client.Disconnected, Mismatch: 1}, 1, true},
		{"handshake, retrying", connView{State: client.Connecting, Mismatch: 1}, 1, true},
		{"system.info", connView{State: client.Connected, Info: later}, api.ProtocolVersion + 1, true},
		{"system.info without a version", connView{State: client.Connected, Info: &api.SystemInfoResult{}}, 0, true},
		{"system.info of a daemon that went away", connView{State: client.Disconnected, Info: later}, 0, false},
		{"system.info while connecting", connView{State: client.Connecting, Info: later}, 0, false},
	} {
		protocol, mismatch := c.conn.backendProtocol()
		if protocol != c.protocol || mismatch != c.mismatch {
			t.Errorf("%s: %d, %v; want %d, %v", c.name, protocol, mismatch, c.protocol, c.mismatch)
		}
	}
}

// logLevels is a slog.Handler that keeps the level of every record.
type logLevels struct {
	levels []slog.Level
}

func (h *logLevels) Enabled(context.Context, slog.Level) bool { return true }

func (h *logLevels) Handle(_ context.Context, r slog.Record) error {
	h.levels = append(h.levels, r.Level)
	return nil
}

func (h *logLevels) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *logLevels) WithGroup(string) slog.Handler      { return h }

// take returns the levels logged since the last call.
func (h *logLevels) take() []slog.Level {
	l := h.levels
	h.levels = nil
	return l
}

func TestLogConnection(t *testing.T) {
	h := &logLevels{}
	w := &Window{log: slog.New(h)}
	debug, warn := []slog.Level{slog.LevelDebug}, []slog.Level{slog.LevelWarn}
	dial := errors.New("dial unix rpc.sock: connect: no such file or directory")
	for i, step := range []struct {
		state client.State
		err   error
		want  []slog.Level
	}{
		{client.Connecting, nil, nil},
		{client.Disconnected, dial, debug}, // no daemon: routine
		{client.Connecting, nil, nil},
		{client.Disconnected, mismatchError(1), warn},
		{client.Connecting, nil, nil},
		{client.Disconnected, mismatchError(1), debug}, // the same text again
		{client.Disconnected, &api.HandshakeError{Reason: api.HandshakeDaemonUnproven}, warn},
		{client.Disconnected, mismatchError(1), debug},
		{client.Disconnected, fmt.Errorf("x: %w", &api.HandshakeError{Reason: api.HandshakeTimedOut}), warn},
		{client.Disconnected, dial, debug},
		{client.Disconnected, nil, nil},
		{client.Connected, nil, nil},         // forgets what was logged
		{client.Disconnected, io.EOF, debug}, // a connection that broke
		{client.Connecting, nil, nil},
		{client.Disconnected, mismatchError(1), warn},
		{client.Disconnected, mismatchError(3), warn}, // another text
	} {
		w.logConnection(step.state, step.err)
		if got := h.take(); !slices.Equal(got, step.want) {
			t.Errorf("step %d (%s, %v): logged %v, want %v", i, step.state, step.err, got, step.want)
		}
	}

	// The peer on the socket chooses the codes of its refusals: every new
	// text is a warning, and the memory stays bounded.
	for code := range 100 {
		w.logConnection(client.Disconnected, &api.HandshakeError{Reason: api.HandshakeRejected, Code: api.ErrorCode(code)})
	}
	if got := h.take(); len(got) != 100 || slices.ContainsFunc(got, func(l slog.Level) bool { return l != slog.LevelWarn }) {
		t.Errorf("100 refusals logged %v", got)
	}
	if n := len(w.connWarned); n > maxConnWarned {
		t.Errorf("logConnection remembers %d texts", n)
	}
}
