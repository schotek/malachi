// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// errDisconnected is returned to a call whose connection went away before
// the answer arrived. The next call dials again.
var errDisconnected = errors.New("connection to malachid was lost")

// errWriteFailed marks a request that never reached the socket at all
// (nothing was written), which is the only case a call may retry: after a
// partial or complete write the daemon may have acted on it.
var errWriteFailed = errors.New("write to malachid failed")

// daemonDownError: nothing listens on the socket.
type daemonDownError struct {
	socket string
	cause  error
}

func (e *daemonDownError) Error() string {
	return fmt.Sprintf("malachid is not running; start it with make run-dev (socket: %s)", e.socket)
}

func (e *daemonDownError) Unwrap() error { return e.cause }

// rpcClient is a minimal JSON-RPC 2.0 client for the daemon socket: lazy
// dial, one connection authenticated before any call (api.ClientHandshake,
// docs/api.md §1.4), requests matched to responses by id, notifications
// ignored. Modelled on ui/internal/client, which is GPL and internal. The
// daemon's key is read by the handshake of every new connection and never
// kept.
type rpcClient struct {
	socket string
	log    *slog.Logger

	nextID atomic.Int64
	dialMu sync.Mutex // one dial + handshake at a time

	mu      sync.Mutex // guards conn and pending
	conn    net.Conn   // set only once its handshake succeeded
	pending map[string]chan api.Response
}

func newRPCClient(socket string, log *slog.Logger) *rpcClient {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &rpcClient{socket: socket, log: log, pending: make(map[string]chan api.Response)}
}

// callRPC performs one typed request.
func callRPC[T any](ctx context.Context, c *rpcClient, method string, params any) (*T, error) {
	var out T
	if err := c.call(ctx, method, params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// call performs one request and decodes its result. It dials on first use
// and after a lost connection; a request that could not be written at all
// is retried once on a fresh connection.
func (c *rpcClient) call(ctx context.Context, method string, params, result any) error {
	start := time.Now()
	err := c.callOnce(ctx, method, params, result)
	if errors.Is(err, errWriteFailed) {
		err = c.callOnce(ctx, method, params, result)
	}
	c.log.Debug("call", "method", method, "dur", time.Since(start), "err", errorCode(err))
	return err
}

func (c *rpcClient) callOnce(ctx context.Context, method string, params, result any) error {
	conn, err := c.connect(ctx)
	if err != nil {
		return err
	}
	return c.send(ctx, conn, method, params, result)
}

// errorCode names an error for the log without its message, which may
// carry an address or a path.
func errorCode(err error) string {
	var (
		hs     *api.HandshakeError
		apiErr *api.Error
	)
	switch {
	case err == nil:
		return ""
	case errors.As(err, &hs):
		return "handshake." + hs.Reason.String()
	case errors.As(err, &apiErr):
		return apiErr.Code.String()
	default:
		return "transport"
	}
}

// connect returns the live connection or dials a new one and authenticates
// it (api.ClientHandshake, docs/api.md §1.4): the handshake sends
// system.hello, refuses a daemon of another protocol version, reads the key
// file beside the socket afresh (a restarted daemon has a new key) and
// checks the daemon's proof before it proves its own. Only a connection
// whose handshake succeeded is published for calls, so no request can
// reach a daemon that has not proved the key, or go out before the
// handshake's last answer.
func (c *rpcClient) connect(ctx context.Context) (net.Conn, error) {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn != nil {
		return conn, nil
	}

	c.dialMu.Lock()
	defer c.dialMu.Unlock()
	c.mu.Lock()
	conn = c.conn
	c.mu.Unlock()
	if conn != nil { // another caller connected while we waited
		return conn, nil
	}

	d := net.Dialer{Timeout: dialTimeout}
	nc, err := d.DialContext(ctx, "unix", c.socket)
	if err != nil {
		return nil, &daemonDownError{socket: c.socket, cause: err}
	}
	// The handshake and then readLoop read through r: whatever the daemon
	// sent right after the handshake's last answer is already in it.
	r := bufio.NewReaderSize(nc, 64<<10)
	if err := api.ClientHandshake(ctx, nc, r, api.KeyPath(c.socket)); err != nil {
		// Never published: no call and no readLoop know nc.
		_ = nc.Close()
		return nil, c.handshakeFailed(ctx, err)
	}
	c.mu.Lock()
	c.conn = nc
	c.mu.Unlock()
	go c.readLoop(nc, r)
	c.log.Info("connected to malachid", "protocol", api.ProtocolVersion)
	return nc, nil
}

// handshakeFailed logs a failed handshake and returns the error for the
// call. A daemon the handshake refused, or one that refused the bridge, is
// a warning with the reason; api.HandshakeError texts never contain the
// key, a nonce or a proof. A connection that broke during the handshake is
// a lost connection, like one that breaks later.
func (c *rpcClient) handshakeFailed(ctx context.Context, err error) error {
	var hs *api.HandshakeError
	switch {
	case errors.As(err, &hs):
		args := []any{"reason", hs.Reason.String()}
		if hs.Reason == api.HandshakeProtocolMismatch {
			args = append(args, "daemonProtocol", hs.Daemon, "protocol", api.ProtocolVersion)
		}
		c.log.Warn("handshake with malachid failed", append(args, "err", hs.Error())...)
		return err
	case ctx.Err() != nil:
		return ctx.Err()
	default:
		c.log.Debug("connection to malachid ended during the handshake", "err", err)
		return fmt.Errorf("%w: %v", errDisconnected, err)
	}
}

// send writes one request on conn and waits for its response.
func (c *rpcClient) send(ctx context.Context, conn net.Conn, method string, params, result any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("marshal params of %s: %w", method, err)
	}
	id := strconv.FormatInt(c.nextID.Add(1), 10)
	req := api.Request{JSONRPC: api.JSONRPCVersion, ID: json.RawMessage(id), Method: method, Params: raw}
	line, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request %s: %w", method, err)
	}

	ch := make(chan api.Response, 1)
	c.mu.Lock()
	if c.conn != conn {
		c.mu.Unlock()
		return errDisconnected
	}
	c.pending[id] = ch
	c.mu.Unlock()

	n, err := conn.Write(append(line, '\n'))
	if err != nil {
		c.forget(id)
		c.drop(conn)
		if n == 0 {
			return fmt.Errorf("%w: %v", errWriteFailed, err)
		}
		return fmt.Errorf("%w: %v", errDisconnected, err)
	}

	select {
	case <-ctx.Done():
		c.forget(id)
		return ctx.Err()
	case resp, ok := <-ch:
		if !ok {
			return errDisconnected
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

func (c *rpcClient) forget(id string) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// readLoop delivers responses to their waiting calls until conn dies. It
// reads conn through r, the reader the handshake read it with, so that
// nothing the daemon sent right after the handshake is lost.
func (c *rpcClient) readLoop(conn net.Conn, r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxLineBytes)
	for sc.Scan() {
		var env api.Envelope
		if err := json.Unmarshal(sc.Bytes(), &env); err != nil {
			c.log.Debug("malformed line from malachid ignored")
			continue
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
			c.log.Debug("notification ignored", "method", env.Method)
		}
	}
	if err := sc.Err(); err != nil {
		c.log.Debug("connection to malachid ended", "err", err)
	}
	c.drop(conn)
}

// drop closes conn and, if it is the live one, fails every pending call.
// Safe to call more than once and for a connection already replaced.
func (c *rpcClient) drop(conn net.Conn) {
	_ = conn.Close()
	c.mu.Lock()
	if c.conn != conn {
		c.mu.Unlock()
		return
	}
	c.conn = nil
	pending := c.pending
	c.pending = make(map[string]chan api.Response)
	c.mu.Unlock()
	for _, ch := range pending {
		close(ch)
	}
}

// close drops the live connection, if any.
func (c *rpcClient) close() {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn != nil {
		c.drop(conn)
	}
}
