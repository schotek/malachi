// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package client

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// otherKey is another daemon run's key.
var otherKey = api.NewAuthKey()

// socketPath returns the socket path in a new directory with a short path:
// a unix socket's path is limited to about 100 bytes, which a test's
// TempDir can exceed. /tmp is used where it exists, the system's temporary
// directory where it does not.
func socketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "mc-")
	if err != nil {
		dir, err = os.MkdirTemp("", "mc-")
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "rpc.sock")
	if len(sock) >= 100 {
		t.Fatalf("socket path %s is too long", sock)
	}
	return sock
}

// fakeDaemon is a scripted malachid on a unix socket. Like the daemon it
// writes its key file beside the socket (api.KeyPath) once it listens, and
// removes it when it stops; every connection plays script.
type fakeDaemon struct {
	t      *testing.T
	sock   string
	key    api.AuthKey // the key of its key file
	script func(c *fakeConn)
	ln     net.Listener
	ended  chan *fakeConn // every connection once the client closed it
	wg     sync.WaitGroup // serve and the connections

	mu      sync.Mutex
	methods []string // of every request the daemon was sent, in order
	open    []net.Conn
	stopped bool
}

// fakeConn is one connection to a fakeDaemon.
type fakeConn struct {
	d           *fakeDaemon
	conn        net.Conn
	r           *bufio.Reader
	clientNonce api.AuthNonce // from system.hello
	daemonNonce api.AuthNonce
}

type fakeRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// startFake runs a fake daemon on sock with a new key, written to its key
// file when keyFile is set. It stops at the end of the test.
func startFake(t *testing.T, sock string, keyFile bool, script func(*fakeConn)) *fakeDaemon {
	t.Helper()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	d := &fakeDaemon{t: t, sock: sock, key: api.NewAuthKey(), script: script, ln: ln, ended: make(chan *fakeConn, 16)}
	if keyFile {
		if err := os.WriteFile(api.KeyPath(sock), api.FormatKeyFile(d.key), 0o600); err != nil {
			_ = ln.Close()
			t.Fatal(err)
		}
	}
	d.wg.Add(1)
	go d.serve()
	t.Cleanup(d.stop)
	return d
}

func (d *fakeDaemon) serve() {
	defer d.wg.Done()
	for {
		conn, err := d.ln.Accept()
		if err != nil {
			return
		}
		d.mu.Lock()
		if d.stopped {
			d.mu.Unlock()
			_ = conn.Close()
			return
		}
		d.open = append(d.open, conn)
		d.wg.Add(1)
		d.mu.Unlock()
		go d.handle(conn)
	}
}

func (d *fakeDaemon) handle(conn net.Conn) {
	defer d.wg.Done()
	c := &fakeConn{d: d, conn: conn, r: bufio.NewReader(conn), daemonNonce: api.NewAuthNonce()}
	d.script(c)
	// Whatever else the client sends, until it closes the connection.
	for {
		if _, ok := c.read(); !ok {
			break
		}
	}
	_ = conn.Close()
	select {
	case d.ended <- c:
	default:
	}
}

// stop closes the listener and every connection, waits for the scripts to
// end and removes the key file, as the daemon does when it quits.
func (d *fakeDaemon) stop() {
	d.mu.Lock()
	stopped := d.stopped
	d.stopped = true
	open := d.open
	d.mu.Unlock()
	if stopped {
		return
	}
	_ = d.ln.Close()
	for _, conn := range open {
		_ = conn.Close()
	}
	d.wg.Wait()
	_ = os.Remove(api.KeyPath(d.sock))
}

// sent returns the methods of the requests the daemon was sent, in order.
func (d *fakeDaemon) sent() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.methods)
}

// waitEnded waits until the client closed n connections and returns them.
func (d *fakeDaemon) waitEnded(t *testing.T, n int) []*fakeConn {
	t.Helper()
	var ended []*fakeConn
	for range n {
		select {
		case c := <-d.ended:
			ended = append(ended, c)
		case <-time.After(5 * time.Second):
			t.Fatal("the client left its connection open")
		}
	}
	return ended
}

// read returns the client's next request, and false once the client closed
// the connection.
func (c *fakeConn) read() (fakeRequest, bool) {
	line, err := c.r.ReadBytes('\n')
	if err != nil {
		return fakeRequest{}, false
	}
	var req fakeRequest
	if err := json.Unmarshal(line, &req); err != nil {
		c.d.t.Errorf("the client sent %q", line)
		return fakeRequest{}, false
	}
	c.d.mu.Lock()
	c.d.methods = append(c.d.methods, req.Method)
	c.d.mu.Unlock()
	return req, true
}

// hello reads system.hello and keeps the client's nonce.
func (c *fakeConn) hello() (json.RawMessage, bool) {
	req, ok := c.read()
	if !ok {
		return nil, false
	}
	var p api.SystemHelloParams
	if req.Method != api.MethodSystemHello || json.Unmarshal(req.Params, &p) != nil {
		c.d.t.Errorf("expected system.hello, got %s %s", req.Method, req.Params)
		return nil, false
	}
	n, ok := api.ParseAuthHex(p.ClientNonce)
	if !ok {
		c.d.t.Errorf("system.hello with the clientNonce %q", p.ClientNonce)
		return nil, false
	}
	c.clientNonce = n
	return req.ID, true
}

// helloResult is a system.hello result of the protocol version that proves
// key.
func (c *fakeConn) helloResult(version int, key api.AuthKey) api.SystemHelloResult {
	return api.SystemHelloResult{
		ProtocolVersion: version,
		DaemonNonce:     c.daemonNonce.Hex(),
		DaemonProof:     api.DaemonProof(key, c.clientNonce, c.daemonNonce).Hex(),
	}
}

// authenticate reads system.authenticate and reports whether it came with
// the right proof.
func (c *fakeConn) authenticate() (json.RawMessage, bool) {
	req, ok := c.read()
	if !ok {
		return nil, false
	}
	var p api.SystemAuthenticateParams
	if req.Method != api.MethodSystemAuthenticate || json.Unmarshal(req.Params, &p) != nil {
		c.d.t.Errorf("expected system.authenticate, got %s %s", req.Method, req.Params)
		return nil, false
	}
	proof, ok := api.ParseAuthHex(p.ClientProof)
	if !ok || !api.ClientProof(c.d.key, c.clientNonce, c.daemonNonce).Equal(proof) {
		c.d.t.Errorf("system.authenticate with a wrong clientProof")
		return nil, false
	}
	return req.ID, true
}

// send writes msgs, a line each, in a single Write; a string is a line as
// it is.
func (c *fakeConn) send(msgs ...any) {
	var b []byte
	for _, m := range msgs {
		line, ok := m.(string)
		if !ok {
			raw, err := json.Marshal(m)
			if err != nil {
				panic(err)
			}
			line = string(raw)
		}
		b = append(append(b, line...), '\n')
	}
	_, _ = c.conn.Write(b)
}

// secrets lists what no error about the connection may show: the keys, the
// nonces and every proof made of them.
func (c *fakeConn) secrets() []string {
	s := []string{c.clientNonce.Hex(), c.daemonNonce.Hex()}
	for _, k := range []api.AuthKey{c.d.key, otherKey} {
		s = append(s, hex.EncodeToString(k[:]),
			api.DaemonProof(k, c.clientNonce, c.daemonNonce).Hex(),
			api.ClientProof(k, c.clientNonce, c.daemonNonce).Hex())
	}
	return s
}

func result(id json.RawMessage, v any) api.Response {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return api.Response{JSONRPC: api.JSONRPCVersion, ID: id, Result: raw}
}

func failure(id json.RawMessage, code api.ErrorCode, message string) api.Response {
	return api.Response{JSONRPC: api.JSONRPCVersion, ID: id, Error: &api.Error{Code: code, Message: message}}
}

// accountsChanged is a notification as the daemon broadcasts it.
var accountsChanged = api.Notification{JSONRPC: api.JSONRPCVersion, Method: api.NotifyAccountsChanged, Params: json.RawMessage(`{}`)}

// authOK completes the handshake, sending extra in the same Write as the
// system.authenticate answer, and answers every call after it with the
// call's method.
func authOK(extra ...any) func(*fakeConn) {
	return func(c *fakeConn) {
		id, ok := c.hello()
		if !ok {
			return
		}
		c.send(result(id, c.helloResult(api.ProtocolVersion, c.d.key)))
		if id, ok = c.authenticate(); !ok {
			return
		}
		c.send(append([]any{result(id, api.SystemAuthenticateResult{})}, extra...)...)
		for {
			req, ok := c.read()
			if !ok {
				return
			}
			c.send(result(req.ID, map[string]string{"method": req.Method}))
		}
	}
}

// oldDaemon answers as a daemon of protocol 1: it broadcasts notifications
// to every connection and does not know system.hello.
func oldDaemon(c *fakeConn) {
	if req, ok := c.read(); ok {
		c.send(accountsChanged, failure(req.ID, api.CodeMethodNotFound, `unknown method "system.hello"`))
	}
}

// speaks answers system.hello as a daemon of the protocol version.
func speaks(version int) func(*fakeConn) {
	return func(c *fakeConn) {
		if id, ok := c.hello(); ok {
			c.send(result(id, c.helloResult(version, c.d.key)))
		}
	}
}

// provesWith answers system.hello with a proof made with key.
func provesWith(key api.AuthKey) func(*fakeConn) {
	return func(c *fakeConn) {
		if id, ok := c.hello(); ok {
			c.send(result(id, c.helloResult(api.ProtocolVersion, key)))
		}
	}
}

// rejectsClient answers system.hello and then refuses the client's proof.
func rejectsClient(c *fakeConn) {
	id, ok := c.hello()
	if !ok {
		return
	}
	c.send(result(id, c.helloResult(api.ProtocolVersion, c.d.key)))
	if id, ok = c.authenticate(); ok {
		c.send(failure(id, api.CodeUnauthenticated, "wrong proof"))
	}
}

// garbles answers system.hello with a daemonNonce that is not one.
func garbles(c *fakeConn) {
	if id, ok := c.hello(); ok {
		r := c.helloResult(api.ProtocolVersion, c.d.key)
		r.DaemonNonce = "zz"
		c.send(result(id, r))
	}
}

// silent reads system.hello and never answers.
func silent(c *fakeConn) { c.hello() }

type stateEvent struct {
	state State
	err   error
}

func (e stateEvent) String() string { return fmt.Sprintf("%s(%v)", e.state, e.err) }

// newTestClient returns a client of sock whose state changes and
// notifications (their methods) go to the returned channels. It is closed
// at the end of the test, before the fake daemons started earlier stop.
func newTestClient(t *testing.T, sock string) (*Client, chan stateEvent, chan string) {
	c := New(sock)
	events := make(chan stateEvent, 64)
	notes := make(chan string, 64)
	c.OnStateChange = func(s State, err error) { events <- stateEvent{s, err} }
	c.OnNotification = func(method string, _ json.RawMessage) { notes <- method }
	t.Cleanup(c.Close)
	return c, events, notes
}

// received returns what arrived on ch so far.
func received[T any](ch chan T) []T {
	var got []T
	for {
		select {
		case v := <-ch:
			got = append(got, v)
		default:
			return got
		}
	}
}

// waitState waits for the client to report state s.
func waitState(t *testing.T, events chan stateEvent, s State) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e := <-events:
			if e.state == s {
				return
			}
		case <-timeout:
			t.Fatalf("the client did not become %s", s)
		}
	}
}

func TestConnectAuthenticates(t *testing.T) {
	sock := socketPath(t)
	d := startFake(t, sock, true, authOK())
	c, events, _ := newTestClient(t, sock)
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if s := c.State(); s != Connected {
		t.Fatalf("state %s after Connect", s)
	}
	if got, want := received(events), []stateEvent{{Connecting, nil}, {Connected, nil}}; !slices.Equal(got, want) {
		t.Errorf("OnStateChange saw %v, want %v", got, want)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var res map[string]string
	if err := c.Call(ctx, api.MethodSystemInfo, api.SystemInfoParams{}, &res); err != nil || res["method"] != api.MethodSystemInfo {
		t.Fatalf("Call: %v, %v", res, err)
	}
	if got, want := d.sent(), []string{api.MethodSystemHello, api.MethodSystemAuthenticate, api.MethodSystemInfo}; !slices.Equal(got, want) {
		t.Errorf("the daemon was sent %q, want %q", got, want)
	}

	c.Close()
	d.waitEnded(t, 1)
	waitState(t, events, Disconnected)
}

// The daemon may send a notification right after the system.authenticate
// answer, in the same Write; the handshake leaves it in the reader, and the
// reader loop must deliver it.
func TestConnectKeepsWhatFollowsTheHandshake(t *testing.T) {
	sock := socketPath(t)
	startFake(t, sock, true, authOK(accountsChanged))
	c, _, notes := newTestClient(t, sock)
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	select {
	case m := <-notes:
		if m != api.NotifyAccountsChanged {
			t.Errorf("notification %s, want %s", m, api.NotifyAccountsChanged)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the notification sent with the system.authenticate answer was lost")
	}
}

func TestConnectRefusals(t *testing.T) {
	hello := []string{api.MethodSystemHello}
	for _, tc := range []struct {
		name    string
		script  func(*fakeConn)
		noKey   bool          // no key file beside the socket
		timeout time.Duration // the client's handshakeTimeout
		reason  api.HandshakeReason
		code    api.ErrorCode // HandshakeRejected
		daemon  int           // DaemonProtocol
		is      error         // errors.Is holds, when set
		sent    []string      // the methods the daemon was sent
	}{
		{name: "daemon of protocol 1", script: oldDaemon,
			reason: api.HandshakeProtocolMismatch, daemon: 1, sent: hello},
		{name: "daemon of a later protocol", script: speaks(api.ProtocolVersion + 1), noKey: true,
			reason: api.HandshakeProtocolMismatch, daemon: api.ProtocolVersion + 1, sent: hello},
		{name: "daemon with another key", script: provesWith(otherKey),
			reason: api.HandshakeDaemonUnproven, sent: hello},
		{name: "client proof rejected", script: rejectsClient,
			reason: api.HandshakeRejected, code: api.CodeUnauthenticated,
			sent: []string{api.MethodSystemHello, api.MethodSystemAuthenticate}},
		{name: "no key file", script: authOK(), noKey: true,
			reason: api.HandshakeKeyUnavailable, is: fs.ErrNotExist, sent: hello},
		{name: "malformed answer", script: garbles,
			reason: api.HandshakeMalformed, sent: hello},
		{name: "silent daemon", script: silent, timeout: 200 * time.Millisecond,
			reason: api.HandshakeTimedOut, sent: hello},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sock := socketPath(t)
			d := startFake(t, sock, !tc.noKey, tc.script)
			c, events, notes := newTestClient(t, sock)
			c.handshakeTimeout = tc.timeout
			start := time.Now()
			err := c.Connect()
			elapsed := time.Since(start)

			if err == nil {
				t.Fatal("Connect succeeded")
			}
			if s := c.State(); s != Disconnected {
				t.Errorf("state %s after a refusal", s)
			}
			got := received(events)
			if len(got) != 2 || got[0] != (stateEvent{Connecting, nil}) || got[1].state != Disconnected || got[1].err != err {
				t.Errorf("OnStateChange saw %v, want connecting, then disconnected with %v", got, err)
			}
			var he *api.HandshakeError
			switch {
			case !errors.As(err, &he):
				t.Errorf("%v (%T) is no *api.HandshakeError", err, err)
			case he.Reason != tc.reason || he.Code != tc.code:
				t.Errorf("reason %s, code %d, want %s, code %d", he.Reason, he.Code, tc.reason, tc.code)
			}
			if got := DaemonProtocol(err); got != tc.daemon {
				t.Errorf("DaemonProtocol = %d, want %d", got, tc.daemon)
			}
			if tc.is != nil && !errors.Is(err, tc.is) {
				t.Errorf("%v is not %v", err, tc.is)
			}
			if tc.timeout > 0 && elapsed > api.HandshakeTimeout/2 {
				t.Errorf("Connect took %v with a handshake timeout of %v", elapsed, tc.timeout)
			}

			conn := d.waitEnded(t, 1)[0]
			if got := d.sent(); !slices.Equal(got, tc.sent) {
				t.Errorf("the daemon was sent %q, want %q", got, tc.sent)
			}
			for _, s := range conn.secrets() {
				if strings.Contains(err.Error(), s) {
					t.Errorf("the error %q shows a key, a nonce or a proof", err)
				}
			}
			if got := received(notes); len(got) != 0 {
				t.Errorf("notifications of a refused daemon reached the UI: %q", got)
			}
		})
	}
}

// A restarted daemon has a new key: Connect reads the key file again.
func TestConnectRereadsTheKey(t *testing.T) {
	sock := socketPath(t)
	first := startFake(t, sock, true, authOK())
	c, events, _ := newTestClient(t, sock)
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	c.Close()
	waitState(t, events, Disconnected)
	first.stop()

	second := startFake(t, sock, true, authOK())
	if first.key.Equal(second.key) {
		t.Fatal("both daemons have the same key")
	}
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect to the restarted daemon: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Call(ctx, api.MethodSystemInfo, api.SystemInfoParams{}, nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got, want := second.sent(), []string{api.MethodSystemHello, api.MethodSystemAuthenticate, api.MethodSystemInfo}; !slices.Equal(got, want) {
		t.Errorf("the restarted daemon was sent %q, want %q", got, want)
	}
}

// Without a daemon the dial error is reported as it is.
func TestConnectWithoutDaemon(t *testing.T) {
	c, events, _ := newTestClient(t, socketPath(t))
	err := c.Connect()
	var he *api.HandshakeError
	if err == nil || errors.As(err, &he) || DaemonProtocol(err) != 0 {
		t.Fatalf("Connect: %v", err)
	}
	if got := received(events); len(got) != 2 || got[1].state != Disconnected || got[1].err != err {
		t.Errorf("OnStateChange saw %v", got)
	}
}

func TestDaemonProtocol(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
	}{
		{nil, 0},
		{errors.New("dial unix rpc.sock: connect: no such file or directory"), 0},
		{&api.HandshakeError{Reason: api.HandshakeProtocolMismatch, Daemon: 1}, 1},
		{fmt.Errorf("connect: %w", &api.HandshakeError{Reason: api.HandshakeProtocolMismatch, Daemon: 3}), 3},
		{&api.HandshakeError{Reason: api.HandshakeDaemonUnproven}, 0},
		{&api.HandshakeError{Reason: api.HandshakeRejected, Code: api.CodeUnauthenticated}, 0},
		{&api.HandshakeError{Reason: api.HandshakeTimedOut, Daemon: 3}, 0}, // Daemon counts for a mismatch only
		{api.NewError(api.CodeMethodNotFound, "unknown method"), 0},        // a call's error is no mismatch
	} {
		if got := DaemonProtocol(tc.err); got != tc.want {
			t.Errorf("DaemonProtocol(%v) = %d, want %d", tc.err, got, tc.want)
		}
	}
}
