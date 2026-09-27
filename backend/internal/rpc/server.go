// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package rpc implements the JSON-RPC 2.0 server that exposes the backend on
// a local unix socket. Wire types and method names come from pkg/api; this
// package owns only transport, framing, the connection handshake, dispatch
// and notification fan-out.
package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// maxLineBytes bounds a single JSON-RPC message. Bodies are the largest
// payload (message.body) and are capped by the sanitiser well below this.
const maxLineBytes = 32 << 20

const (
	// maxPendingConns caps the connections in the handshake at a time
	// (docs/api.md §1.4); further ones are accepted and closed at once.
	maxPendingConns = 32

	// preAuthTimeout is how long a connection has, from accept, to complete
	// the handshake.
	preAuthTimeout = 10 * time.Second

	// staleDialTimeout bounds Listen's test whether a daemon answers on a
	// socket that is already there.
	staleDialTimeout = 500 * time.Millisecond

	// The pause after a failed Accept doubles from acceptBackoffMin up to
	// acceptBackoffMax, as in net/http, instead of spinning.
	acceptBackoffMin = 5 * time.Millisecond
	acceptBackoffMax = time.Second
)

// Server accepts connections on a unix socket, authenticates each one
// (docs/api.md §1.4) and dispatches its JSON-RPC calls. Authentication
// cannot be turned off.
type Server struct {
	log      *slog.Logger
	handlers map[string]handler

	// Set by NewServer; tests in this package change them before Listen.
	maxPending     int
	preAuthTimeout time.Duration
	newNonce       func() api.AuthNonce
	failLog        logLimiter // failed handshakes
	capLog         logLimiter // connections closed over maxPending
	keyLog         logLimiter // trouble with the key file after Listen

	// lifeMu serialises Listen and Close.
	lifeMu    sync.Mutex
	closeOnce sync.Once

	// keyMu guards the key file and these fields.
	keyMu      sync.Mutex
	key        secretKey // this run's key, set by Listen
	keyPath    string
	keyRetired bool // Close retired the key: its file is never written again

	mu       sync.Mutex
	listener *net.UnixListener
	sockPath string
	sockInfo os.FileInfo // the socket file Listen bound; Close removes no other
	conns    map[*conn]struct{}
	pending  int // connections in the handshake (conn.pending)
	closed   bool
}

// NewServer wires a Backend implementation to the method table.
func NewServer(backend api.Backend, log *slog.Logger) *Server {
	s := &Server{
		log:            log.With("component", "rpc"),
		handlers:       make(map[string]handler),
		conns:          make(map[*conn]struct{}),
		maxPending:     maxPendingConns,
		preAuthTimeout: preAuthTimeout,
		newNonce:       api.NewAuthNonce,
	}
	s.registerTransport()
	s.registerBackend(backend)
	return s
}

// Listen creates the socket at path, replacing a stale one left behind by a
// crashed daemon, and writes this run's key beside it (api.KeyPath) before
// any connection can be accepted. It refuses to start if another daemon is
// alive on the socket, or if something that is not a socket or not a key
// file is in the way; it never removes such a thing.
func (s *Server) Listen(path string) error {
	// sun_path is 108 bytes on Linux including the terminating NUL.
	if len(path) > 107 {
		return fmt.Errorf("socket path too long (%d bytes, max 107): %s", len(path), path)
	}
	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()
	s.mu.Lock()
	closed, bound := s.closed, s.listener != nil
	s.mu.Unlock()
	switch {
	case closed:
		return errors.New("rpc: Listen after Close")
	case bound:
		return errors.New("rpc: Listen called twice")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create socket directory: %w", err)
	}
	if err := s.clearStaleSocket(path); err != nil {
		return err
	}

	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return fmt.Errorf("listen on %s: %w", path, err)
	}
	// Close removes the socket file itself, and only while it is still this
	// one (removeSocket): closing the listener must not remove whatever is
	// at the path by then.
	ln.SetUnlinkOnClose(false)
	sockInfo, err := os.Lstat(path)
	if err != nil {
		_ = ln.Close()
		return fmt.Errorf("inspect socket %s: %w", path, err)
	}
	// Close removes the socket file only while SameFile finds it is still
	// this one. Windows may read a file's identity lazily, by path, at the
	// first SameFile, and again at the next one if that failed: read it
	// now, while the path is this socket. If it cannot be read now, forget
	// it rather than let Close read the identity of whatever is at the
	// path by then: the socket file stays, and the next start removes it
	// as stale.
	if !os.SameFile(sockInfo, sockInfo) {
		s.log.Warn("cannot identify the socket file; it will be left in place at shutdown", "path", path)
		sockInfo = nil
	}
	fail := func(err error) error {
		if sockInfo != nil {
			s.removeSocket(path, sockInfo)
		}
		_ = ln.Close()
		return err
	}
	// Only the owning user may talk to the daemon.
	if err := os.Chmod(path, 0o600); err != nil {
		return fail(fmt.Errorf("chmod socket: %w", err))
	}

	keyPath := api.KeyPath(path)
	if err := keyReplaceable(keyPath); err != nil {
		return fail(fmt.Errorf("refusing to replace %s with the key file: %w", keyPath, err))
	}
	key := newSecretKey(api.NewAuthKey())
	if err := writeKeyFile(keyPath, key()); err != nil {
		return fail(err)
	}

	s.keyMu.Lock()
	s.key, s.keyPath = key, keyPath
	s.keyMu.Unlock()
	s.mu.Lock()
	s.listener, s.sockPath, s.sockInfo = ln, path, sockInfo
	s.mu.Unlock()
	s.log.Info("listening", "socket", path, "key", keyPath)
	return nil
}

// clearStaleSocket makes way for a new socket at path. A socket nobody
// answers on is left over from a daemon that crashed, and is removed. A
// daemon that answers, or one too busy to answer in time, keeps its socket:
// Listen fails. Whatever is not a socket is never removed.
func (s *Server) clearStaleSocket(path string) error {
	fi, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("inspect %s: %w", path, err)
	case fi.Mode().Type() != fs.ModeSocket:
		return fmt.Errorf("%s exists and is not a socket; not replacing it", path)
	}
	c, err := net.DialTimeout("unix", path, staleDialTimeout)
	if err == nil {
		_ = c.Close()
		return fmt.Errorf("another malachid is already listening on %s", path)
	}
	if isTimeout(err) {
		return fmt.Errorf("the socket %s does not answer in time; another malachid may be busy on it", path)
	}
	s.log.Warn("removing stale socket", "path", path)
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove stale socket: %w", err)
	}
	return nil
}

// Serve accepts connections until ctx is cancelled or the server is closed,
// and returns once Close has finished (the end of ctx closes the server).
func (s *Server) Serve(ctx context.Context) error {
	s.mu.Lock()
	ln := s.listener
	s.mu.Unlock()
	if ln == nil {
		return errors.New("rpc: Serve called before Listen")
	}
	key := s.authKey()

	// Close, not just the listener: shutdown must follow Close's order,
	// the key file before the listener.
	stop := context.AfterFunc(ctx, s.Close)
	defer stop()

	var backoff time.Duration
	for {
		nc, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) || s.isClosed() {
				s.Close() // waits for a Close that is already running
				return nil
			}
			backoff = min(max(2*backoff, acceptBackoffMin), acceptBackoffMax)
			s.log.Error("accept failed", "err", err, "retryIn", backoff)
			t := time.NewTimer(backoff)
			select {
			case <-t.C:
			case <-ctx.Done():
				t.Stop()
			}
			continue
		}
		backoff = 0
		s.admit(ctx, nc, key)
	}
}

// admit registers a new connection and serves it. When the server is
// closed, or maxPending connections are in the handshake already, it
// closes the connection at once instead (Accept is never paused).
func (s *Server) admit(ctx context.Context, nc net.Conn, key secretKey) {
	accepted := time.Now()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = nc.Close()
		return
	}
	if s.pending >= s.maxPending {
		s.mu.Unlock()
		_ = nc.Close()
		warnLimited(s.log, &s.capLog, "too many connections in the handshake; closing a new one", "limit", s.maxPending)
		return
	}
	c := newConn(s, nc, key, accepted)
	c.pending = true
	s.pending++
	s.conns[c] = struct{}{}
	s.mu.Unlock()
	go c.serve(ctx)
}

func (s *Server) authKey() secretKey {
	s.keyMu.Lock()
	defer s.keyMu.Unlock()
	return s.key
}

func (s *Server) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// Close shuts the server down, in this order: no connection is admitted
// any more; the key file is removed while it holds this run's key
// (retireKeyFile), while the listener still holds the socket, so that no
// successor can have written its own; the socket file is removed while it
// is still the one Listen bound, also before the listener closes, so that
// it cannot be a successor's; the listener closes; every connection is
// closed. Safe to call more than once and concurrently: every call returns
// once all this is done. Removals that fail are logged, not fatal (Windows
// cannot remove a file another process holds open).
func (s *Server) Close() { s.closeOnce.Do(s.shutdown) }

func (s *Server) shutdown() {
	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()
	s.mu.Lock()
	s.closed = true
	ln, sockPath, sockInfo := s.listener, s.sockPath, s.sockInfo
	s.mu.Unlock()

	s.retireKeyFile()
	if sockInfo != nil {
		s.removeSocket(sockPath, sockInfo)
	}
	if ln != nil {
		_ = ln.Close()
	}

	s.mu.Lock()
	conns := make([]*conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		c.close()
	}
	s.log.Info("rpc server closed")
}

// removeSocket removes the socket file at path if it is still the one
// described by fi, the file this server bound; a socket another daemon has
// bound there since stays.
func (s *Server) removeSocket(path string, fi os.FileInfo) {
	cur, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return
	case err != nil:
		s.log.Warn("cannot inspect the socket file", "path", path, "err", err)
		return
	case !os.SameFile(fi, cur):
		s.log.Info("socket file left in place: it is not the one this daemon bound", "path", path)
		return
	}
	if err := retryFileOp(func() error { return os.Remove(path) }); err != nil && !errors.Is(err, fs.ErrNotExist) {
		s.log.Warn("cannot remove the socket file", "path", path, "err", err)
	}
}

func (s *Server) removeConn(c *conn) {
	s.mu.Lock()
	delete(s.conns, c)
	s.releaseLocked(c)
	s.mu.Unlock()
}

// releasePending gives c's handshake slot back, once.
func (s *Server) releasePending(c *conn) {
	s.mu.Lock()
	s.releaseLocked(c)
	s.mu.Unlock()
}

func (s *Server) releaseLocked(c *conn) {
	if c.pending {
		c.pending = false
		s.pending--
	}
}

// broadcast sends a notification to every authenticated connection. One
// still in the handshake does not get it, not even later.
func (s *Server) broadcast(method string, params any) {
	raw, err := json.Marshal(params)
	if err != nil {
		s.log.Error("marshal notification", "method", method, "err", err)
		return
	}
	n := api.Notification{JSONRPC: api.JSONRPCVersion, Method: method, Params: raw}

	s.mu.Lock()
	conns := make([]*conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()

	for _, c := range conns {
		if err := c.notify(n); err != nil {
			s.log.Debug("notification dropped", "method", method, "err", err)
		}
	}
}

// --- Notifier -------------------------------------------------------------

// Server implements api.Notifier so that other packages can push events
// without importing the transport.
var _ api.Notifier = (*Server)(nil)

func (s *Server) NewMessage(n api.NewMessageNotification) { s.broadcast(api.NotifyNewMessage, n) }
func (s *Server) SyncState(n api.SyncStateNotification)   { s.broadcast(api.NotifySyncState, n) }
func (s *Server) AuthRequired(n api.AuthRequiredNotification) {
	s.broadcast(api.NotifyAuthRequired, n)
}
func (s *Server) AccountsChanged(n api.AccountsChangedNotification) {
	s.broadcast(api.NotifyAccountsChanged, n)
}

// --- connection ----------------------------------------------------------

type conn struct {
	srv      *Server
	nc       net.Conn
	key      secretKey
	accepted time.Time

	pending bool // guarded by srv.mu: the connection holds a handshake slot

	wmu    sync.Mutex
	enc    *json.Encoder
	authed atomic.Bool // set under wmu, right after the system.authenticate answer

	once sync.Once
	wg   sync.WaitGroup
}

func newConn(s *Server, nc net.Conn, key secretKey, accepted time.Time) *conn {
	return &conn{srv: s, nc: nc, key: key, accepted: accepted, enc: json.NewEncoder(nc)}
}

func (c *conn) serve(ctx context.Context) {
	defer c.srv.removeConn(c)
	defer c.close()

	log := c.srv.log.With("peer", fmt.Sprintf("%p", c))
	log.Debug("client connected")

	lim := newHandshakeLimit(c.nc)
	sc := bufio.NewScanner(lim)
	// One byte more than a connection may send before it authenticates: a
	// scanner grows its buffer when it is full, and this one never is
	// until lift. After that it grows up to maxLineBytes as lines need.
	sc.Buffer(make([]byte, 0, api.MaxHandshakeBytes+1), maxLineBytes)
	sc.Split(lim.split)
	if !c.authenticate(sc, lim, log) {
		return
	}

	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var req api.Request
		if err := json.Unmarshal(line, &req); err != nil {
			_ = c.write(api.Response{
				JSONRPC: api.JSONRPCVersion,
				ID:      json.RawMessage("null"),
				Error:   api.NewError(api.CodeParseError, "parse error: %v", err),
			})
			continue
		}
		if req.JSONRPC != api.JSONRPCVersion || req.Method == "" {
			_ = c.write(api.Response{
				JSONRPC: api.JSONRPCVersion,
				ID:      idOrNull(req.ID),
				Error:   api.NewError(api.CodeInvalidRequest, "invalid request"),
			})
			continue
		}

		// Client-sent notifications (no id) are accepted but ignored: the
		// contract defines none in this direction.
		if len(req.ID) == 0 || string(req.ID) == "null" {
			log.Debug("ignoring client notification", "method", req.Method)
			continue
		}

		c.wg.Add(1)
		go func(req api.Request) {
			defer c.wg.Done()
			c.handle(ctx, req)
		}(req)
	}
	if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
		log.Debug("client read error", "err", err)
	}
	c.wg.Wait()
	log.Debug("client disconnected")
}

// authenticate runs the connection's handshake (docs/api.md §1.4) on sc,
// synchronously in the read loop: no goroutine, no handler, no backend
// code. It reports whether the connection is authenticated; when it is
// not, the caller closes it.
func (c *conn) authenticate(sc *bufio.Scanner, lim *handshakeLimit, log *slog.Logger) bool {
	if err := c.nc.SetDeadline(c.accepted.Add(c.srv.preAuthTimeout)); err != nil {
		log.Debug("cannot set the handshake deadline", "err", err)
		return false
	}
	hs := handshake{key: c.key, newNonce: c.srv.newNonce}
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		st := hs.step(line)
		if st.hello {
			c.srv.ensureKeyFile()
		}
		if st.authed {
			if err := c.completeAuth(st.reply); err != nil {
				log.Debug("cannot answer system.authenticate", "err", err)
				return false
			}
			lim.lift()
			c.srv.releasePending(c)
			log.Debug("client authenticated")
			return true
		}
		if st.reply != nil {
			if err := c.write(st.reply); err != nil {
				log.Debug("cannot answer during the handshake", "err", err)
				return false
			}
		}
		if st.fail != "" {
			warnLimited(log, &c.srv.failLog, "rpc handshake failed", "reason", st.fail)
			return false
		}
	}
	err := sc.Err()
	switch {
	case errors.Is(err, errHandshakeBudget):
		warnLimited(log, &c.srv.failLog, "rpc handshake failed", "reason", failBudget)
	case lim.read && isTimeout(err):
		warnLimited(log, &c.srv.failLog, "rpc handshake failed", "reason", failTimeout)
	case !lim.read:
		// Connecting and closing again is how clients test whether a
		// daemon is alive.
		log.Debug("client left before the handshake", "err", err)
	default:
		log.Debug("client left during the handshake", "err", err)
	}
	return false
}

// completeAuth sends the system.authenticate answer, clears the handshake
// deadline and marks the connection authenticated, in one critical section
// with every other write: no notification can precede the answer.
func (c *conn) completeAuth(reply *api.Response) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if err := c.enc.Encode(reply); err != nil {
		return err
	}
	if err := c.nc.SetDeadline(time.Time{}); err != nil {
		return err
	}
	c.authed.Store(true)
	return nil
}

func (c *conn) handle(ctx context.Context, req api.Request) {
	resp := api.Response{JSONRPC: api.JSONRPCVersion, ID: req.ID}

	h, ok := c.srv.handlers[req.Method]
	if !ok {
		resp.Error = api.NewError(api.CodeMethodNotFound, "unknown method %q", req.Method)
		_ = c.write(resp)
		return
	}

	start := time.Now()
	result, err := h(ctx, req.Params)
	if err != nil {
		resp.Error = toAPIError(err)
		if resp.Error.Code == api.CodeInternalError {
			c.srv.log.Error("handler failed", "method", req.Method, "err", err)
		}
	} else {
		raw, mErr := json.Marshal(result)
		if mErr != nil {
			resp.Error = api.NewError(api.CodeInternalError, "marshal result")
			c.srv.log.Error("marshal result", "method", req.Method, "err", mErr)
		} else {
			resp.Result = raw
		}
	}
	c.srv.log.Debug("call", "method", req.Method, "dur", time.Since(start), "err", resp.Error)
	_ = c.write(resp)
}

// write sends one message: an answer, during the handshake or after it.
func (c *conn) write(v any) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	// json.Encoder appends a trailing newline: that is our framing.
	return c.enc.Encode(v)
}

// notify sends a notification if the connection is authenticated, and
// drops it otherwise: nothing is kept for later. The check needs no lock,
// so a connection in the handshake never holds up a broadcast; authed is
// set only after the answer is written, under wmu, so a notification that
// sees it set is written after the answer.
func (c *conn) notify(v any) error {
	if !c.authed.Load() {
		return nil
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.enc.Encode(v)
}

func (c *conn) close() {
	c.once.Do(func() { c.nc.Close() })
}

func idOrNull(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage("null")
	}
	return id
}

// isTimeout reports whether err is a deadline or another timeout.
func isTimeout(err error) bool {
	var ne net.Error
	return errors.Is(err, os.ErrDeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout())
}

// toAPIError maps any error to the wire error. Non-API errors become a
// generic internal error; their text is logged, not leaked to the client.
func toAPIError(err error) *api.Error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		return apiErr
	}
	if errors.Is(err, context.Canceled) {
		return api.NewError(api.CodeCancelled, "cancelled")
	}
	return api.NewError(api.CodeInternalError, "internal error")
}
