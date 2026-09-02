// Package rpc implements the JSON-RPC 2.0 server that exposes the backend on
// a local unix socket. Wire types and method names come from pkg/api; this
// package owns only transport, framing, dispatch and notification fan-out.
package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// maxLineBytes bounds a single JSON-RPC message. Bodies are the largest
// payload (message.body) and are capped by the sanitiser well below this.
const maxLineBytes = 32 << 20

// Server accepts connections on a unix socket and dispatches JSON-RPC calls.
type Server struct {
	log      *slog.Logger
	handlers map[string]handler

	mu       sync.Mutex
	listener net.Listener
	conns    map[*conn]struct{}
	closed   bool
}

// NewServer wires a Backend implementation to the method table.
func NewServer(backend api.Backend, log *slog.Logger) *Server {
	s := &Server{
		log:      log.With("component", "rpc"),
		handlers: make(map[string]handler),
		conns:    make(map[*conn]struct{}),
	}
	s.registerBackend(backend)
	return s
}

// Listen creates the socket at path, replacing a stale one left behind by a
// crashed daemon. It refuses to start if another daemon is alive on it.
func (s *Server) Listen(path string) error {
	// sun_path is 108 bytes on Linux including the terminating NUL.
	if len(path) > 107 {
		return fmt.Errorf("socket path too long (%d bytes, max 107): %s", len(path), path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create socket directory: %w", err)
	}

	if _, err := os.Stat(path); err == nil {
		// Something is there. Is anybody listening?
		c, dialErr := net.DialTimeout("unix", path, 500*time.Millisecond)
		if dialErr == nil {
			c.Close()
			return fmt.Errorf("another malachid is already listening on %s", path)
		}
		s.log.Warn("removing stale socket", "path", path)
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove stale socket: %w", err)
		}
	}

	ln, err := net.Listen("unix", path)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", path, err)
	}
	// Only the owning user may talk to the daemon.
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return fmt.Errorf("chmod socket: %w", err)
	}

	s.mu.Lock()
	s.listener = ln
	s.mu.Unlock()
	s.log.Info("listening", "socket", path)
	return nil
}

// Serve accepts connections until ctx is cancelled, then shuts down
// gracefully: stops accepting, closes every connection, removes the socket.
func (s *Server) Serve(ctx context.Context) error {
	s.mu.Lock()
	ln := s.listener
	s.mu.Unlock()
	if ln == nil {
		return errors.New("rpc: Serve called before Listen")
	}

	// Unblock Accept when the context ends; the full teardown happens
	// synchronously below so that callers can rely on Serve having finished
	// it when it returns.
	stopAccept := context.AfterFunc(ctx, func() { ln.Close() })
	defer stopAccept()

	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				s.Close()
				return nil
			}
			s.log.Error("accept failed", "err", err)
			continue
		}
		cn := newConn(s, c)
		s.mu.Lock()
		s.conns[cn] = struct{}{}
		s.mu.Unlock()
		go cn.serve(ctx)
	}
}

// Close stops the listener, drops all connections and unlinks the socket.
// Safe to call more than once.
func (s *Server) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	ln := s.listener
	conns := make([]*conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()

	for _, c := range conns {
		c.close()
	}
	if ln != nil {
		ln.Close()
		if ul, ok := ln.(*net.UnixListener); ok {
			// UnixListener unlinks on Close by default, but be explicit in
			// case SetUnlinkOnClose was changed.
			_ = os.Remove(ul.Addr().String())
		}
	}
	s.log.Info("rpc server closed")
}

func (s *Server) removeConn(c *conn) {
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
}

// broadcast sends a notification to every connected client.
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
		if err := c.write(n); err != nil {
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

// --- connection ----------------------------------------------------------

type conn struct {
	srv  *Server
	nc   net.Conn
	wmu  sync.Mutex
	enc  *json.Encoder
	once sync.Once
	wg   sync.WaitGroup
}

func newConn(s *Server, nc net.Conn) *conn {
	return &conn{srv: s, nc: nc, enc: json.NewEncoder(nc)}
}

func (c *conn) serve(ctx context.Context) {
	defer c.srv.removeConn(c)
	defer c.close()

	log := c.srv.log.With("peer", fmt.Sprintf("%p", c))
	log.Debug("client connected")

	sc := bufio.NewScanner(c.nc)
	sc.Buffer(make([]byte, 0, 64<<10), maxLineBytes)

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

func (c *conn) write(v any) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	// json.Encoder appends a trailing newline: that is our framing.
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
