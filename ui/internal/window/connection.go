// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"errors"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/client"
)

// How an attempt to connect to the daemon ends, for the status line
// (statusLineFor), the backend banner (showConnectionState) and the log
// (logConnection). The client authenticates every connection
// (api.ClientHandshake), and an attempt ends in one of three ways:
//
//   - connected;
//   - a protocol mismatch: the handshake found a daemon of another protocol
//     version (client.DaemonProtocol; 1 for a daemon older than the
//     handshake). A daemon runs, so the banner saying that none does stays
//     hidden; the status line names both versions and cannot be clicked,
//     and nothing is loaded;
//   - unavailable: nothing answers on the socket, the connection broke, or
//     the handshake refused the daemon for another reason. The status line
//     says that the backend is unavailable, and the banner offers a retry.
//
// The mismatch is sticky: the retry every reconnectInterval goes through
// Connecting, which keeps it, so the line does not flicker to "Connecting…"
// and back with every attempt; it changes once an attempt ends otherwise.
//
// This file has no translatable text and is not in po/POTFILES; the texts
// of the status line are in status.go.

// maxConnWarned bounds what logConnection remembers: the peer on the socket
// chooses the error codes of its refusals, and so their texts.
const maxConnWarned = 16

// nextConnView is the connection view once the client reported state s
// with err. What system.info and sync.status said is forgotten with every
// change and asked again on connecting. A daemon of another protocol
// version is kept while the next attempt is underway (Connecting), and
// replaced by how that attempt ends.
func nextConnView(prev connView, s client.State, err error) connView {
	next := connView{State: s, Mismatch: client.DaemonProtocol(err)}
	if s == client.Connecting {
		next.Mismatch = prev.Mismatch
	}
	return next
}

// backendProtocol reports a daemon this UI cannot use because of its
// protocol version, and that version: one the handshake refused
// (Mismatch), or, as a defence, one whose system.info names another
// version on a connection the handshake accepted. What a daemon said
// before its connection went away does not count.
func (c connView) backendProtocol() (int, bool) {
	switch {
	case c.Mismatch != 0:
		return c.Mismatch, true
	case c.State == client.Connected && c.Info != nil && c.Info.ProtocolVersion != api.ProtocolVersion:
		return c.Info.ProtocolVersion, true
	}
	return 0, false
}

// logConnection logs the client's report of state s with err
// (showConnectionState). A daemon the handshake refused, for a protocol
// mismatch or for anything else, is worth a warning once per distinct
// error text until a connection succeeds; the retries repeat it at Debug.
// Anything else stays at Debug: nothing answering on the socket, or a
// connection that broke, is routine while the daemon is down or starting.
func (w *Window) logConnection(s client.State, err error) {
	if s == client.Connected {
		w.connWarned = nil
		return
	}
	var refused *api.HandshakeError
	switch {
	case err == nil:
		return
	case !errors.As(err, &refused):
		// Repeated dial failures while the daemon is down are expected;
		// keep them at debug so the log stays readable.
		w.log.Debug("backend unavailable", "err", err)
		return
	}
	text := err.Error()
	if w.connWarned[text] {
		w.log.Debug("backend handshake failed", "err", err)
		return
	}
	if len(w.connWarned) >= maxConnWarned {
		clear(w.connWarned)
	}
	if w.connWarned == nil {
		w.connWarned = make(map[string]bool)
	}
	w.connWarned[text] = true
	w.log.Warn("backend handshake failed", "err", err)
}
