// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package client is the JSON-RPC 2.0 client for the malachid unix socket.
//
// It is transport only: it authenticates every connection it makes
// (api.ClientHandshake, docs/api.md §1.4), sends requests, matches
// responses by ID and forwards server notifications. It contains no mail
// logic; everything it knows about the protocol comes from backend/pkg/api.
package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// ErrDisconnected is returned by Call when there is no live connection.
var ErrDisconnected = errors.New("not connected to malachid")

// State is the connection state as seen by the UI.
type State int

const (
	Disconnected State = iota
	Connecting
	Connected
)

func (s State) String() string {
	switch s {
	case Connecting:
		return "connecting"
	case Connected:
		return "connected"
	default:
		return "disconnected"
	}
}

// Client talks to one daemon socket. It is safe for concurrent use.
// Callbacks run on the reader goroutine; UI code must marshal them to the
// main loop (glib.IdleAdd).
type Client struct {
	Socket string

	// OnNotification receives every server notification.
	OnNotification func(method string, params json.RawMessage)
	// OnStateChange is called whenever the connection state changes. The
	// error of Disconnected says why: the dial's, the handshake's (an
	// *api.HandshakeError for a daemon it refused), or what ended the
	// connection.
	OnStateChange func(State, error)

	// handshakeTimeout bounds the handshake of each Connect; 0 means
	// api.HandshakeTimeout. Tests shorten it.
	handshakeTimeout time.Duration

	nextID  atomic.Int64
	mu      sync.Mutex
	conn    net.Conn
	state   State
	pending map[string]chan api.Response
}

// New creates a client for the given socket path (see DefaultSocketPath).
func New(socket string) *Client {
	return &Client{Socket: socket, pending: make(map[string]chan api.Response)}
}

// DefaultSocketPath mirrors the daemon's resolution of the socket location
// (backend/internal/config.ResolvePaths); the Flatpak rule is api.SocketBase.
func DefaultSocketPath() string {
	if p := os.Getenv("MALACHI_SOCKET"); p != "" {
		return p
	}
	if base := api.SocketBase(os.Getenv("XDG_RUNTIME_DIR"), os.Getenv("FLATPAK_ID")); base != "" {
		return filepath.Join(base, api.SocketRelPath)
	}
	// Same fallback as backend/internal/config when no session runtime dir
	// exists (containers, ssh).
	cache := os.Getenv("XDG_CACHE_HOME")
	if cache == "" {
		home, _ := os.UserHomeDir()
		cache = filepath.Join(home, ".cache")
	}
	return filepath.Join(cache, "malachi", "run", "rpc.sock")
}

// State returns the current connection state.
func (c *Client) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// Connect dials the socket, authenticates the connection (docs/api.md
// §1.4) and starts the reader; it does nothing unless disconnected. It
// returns within the dial and handshake timeouts, and the connection is
// used for calls only once the handshake succeeded. A failure is reported
// both as the error and through OnStateChange: a daemon the handshake
// refused is an *api.HandshakeError (DaemonProtocol names a daemon of
// another protocol version). The key file is read afresh on every call,
// since a restarted daemon has a new key.
func (c *Client) Connect() error {
	c.mu.Lock()
	if c.state != Disconnected {
		c.mu.Unlock()
		return nil
	}
	c.state = Connecting
	c.mu.Unlock()
	c.emit(Connecting, nil)

	conn, err := net.DialTimeout("unix", c.Socket, 2*time.Second)
	if err != nil {
		c.fail(err)
		return err
	}
	// The handshake and then readLoop read through r: whatever the daemon
	// sent right after the handshake's last answer is already in it.
	r := bufio.NewReaderSize(conn, 64<<10)
	timeout := c.handshakeTimeout
	if timeout <= 0 {
		timeout = api.HandshakeTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	err = api.ClientHandshake(ctx, conn, r, api.KeyPath(c.Socket))
	cancel()
	if err != nil {
		conn.Close()
		c.fail(err)
		return err
	}

	c.mu.Lock()
	c.conn = conn
	c.state = Connected
	c.mu.Unlock()
	c.emit(Connected, nil)

	go c.readLoop(conn, r)
	return nil
}

// fail ends a connection attempt that did not connect: the state goes
// back to Disconnected and err is reported as it is.
func (c *Client) fail(err error) {
	c.mu.Lock()
	c.state = Disconnected
	c.mu.Unlock()
	c.emit(Disconnected, err)
}

// DaemonProtocol returns the protocol version of the daemon when err is,
// or wraps, the handshake's report that the daemon speaks another one
// (api.HandshakeProtocolMismatch; 1 for a daemon older than the
// handshake), and 0 for any other error and for nil.
func DaemonProtocol(err error) int {
	var he *api.HandshakeError
	if errors.As(err, &he) && he.Reason == api.HandshakeProtocolMismatch {
		return he.Daemon
	}
	return 0
}

// Close drops the connection. Pending calls fail with ErrDisconnected.
func (c *Client) Close() {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn != nil {
		conn.Close()
	}
}

// Call performs one request and decodes the result into result (may be nil).
// A server-side error is returned as *api.Error.
func (c *Client) Call(ctx context.Context, method string, params any, result any) error {
	c.mu.Lock()
	conn := c.conn
	connected := c.state == Connected
	c.mu.Unlock()
	if !connected || conn == nil {
		return ErrDisconnected
	}

	id := fmt.Sprint(c.nextID.Add(1))
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("marshal params: %w", err)
		}
		raw = b
	}
	req := api.Request{JSONRPC: api.JSONRPCVersion, ID: json.RawMessage(id), Method: method, Params: raw}

	ch := make(chan api.Response, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()

	line, err := json.Marshal(req)
	if err != nil {
		c.forget(id)
		return err
	}
	if _, err := conn.Write(append(line, '\n')); err != nil {
		c.forget(id)
		return fmt.Errorf("write: %w", err)
	}

	select {
	case <-ctx.Done():
		c.forget(id)
		return ctx.Err()
	case resp, ok := <-ch:
		if !ok {
			return ErrDisconnected
		}
		if resp.Error != nil {
			return resp.Error
		}
		if result != nil && len(resp.Result) > 0 {
			if err := json.Unmarshal(resp.Result, result); err != nil {
				return fmt.Errorf("decode result of %s: %w", method, err)
			}
		}
		return nil
	}
}

func (c *Client) forget(id string) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *Client) emit(s State, err error) {
	if c.OnStateChange != nil {
		c.OnStateChange(s, err)
	}
}

// readLoop reads the connection through r, the reader the handshake used,
// until it ends.
func (c *Client) readLoop(conn net.Conn, r *bufio.Reader) {
	var readErr error
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			readErr = err
			break
		}
		var env api.Envelope
		if err := json.Unmarshal(line, &env); err != nil {
			continue // malformed line from the daemon; ignore, keep reading
		}
		switch {
		case env.IsResponse():
			c.mu.Lock()
			ch := c.pending[string(env.ID)]
			delete(c.pending, string(env.ID))
			c.mu.Unlock()
			if ch != nil {
				ch <- api.Response{JSONRPC: env.JSONRPC, ID: env.ID, Result: env.Result, Error: env.Error}
			}
		case env.IsNotification() && env.Method != "":
			if c.OnNotification != nil {
				c.OnNotification(env.Method, env.Params)
			}
		}
	}

	conn.Close()
	c.mu.Lock()
	if c.conn == conn {
		c.conn = nil
		c.state = Disconnected
	}
	pending := c.pending
	c.pending = make(map[string]chan api.Response)
	c.mu.Unlock()
	for _, ch := range pending {
		close(ch)
	}
	c.emit(Disconnected, readErr)
}
