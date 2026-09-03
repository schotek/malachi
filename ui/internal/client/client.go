// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package client is the JSON-RPC 2.0 client for the malachid unix socket.
//
// It is transport only: it sends requests, matches responses by ID and
// forwards server notifications. It contains no mail logic; everything it
// knows about the protocol comes from backend/pkg/api.
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
	// OnStateChange is called whenever the connection state changes.
	OnStateChange func(State, error)

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

// DefaultSocketPath mirrors the daemon's resolution of the socket location.
func DefaultSocketPath() string {
	if p := os.Getenv("MALACHI_SOCKET"); p != "" {
		return p
	}
	if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" {
		return filepath.Join(rt, api.SocketRelPath)
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

// Connect dials the socket and starts the reader. It returns quickly; a
// failure is reported both as the error and through OnStateChange.
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
		c.mu.Lock()
		c.state = Disconnected
		c.mu.Unlock()
		c.emit(Disconnected, err)
		return err
	}

	c.mu.Lock()
	c.conn = conn
	c.state = Connected
	c.mu.Unlock()
	c.emit(Connected, nil)

	go c.readLoop(conn)
	return nil
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

func (c *Client) readLoop(conn net.Conn) {
	r := bufio.NewReaderSize(conn, 64<<10)
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
