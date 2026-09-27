// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package rpc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// --- helpers ---------------------------------------------------------------

// shortDir returns a new empty directory with a short path: t.TempDir()
// can make a socket path longer than sun_path allows (104 bytes on macOS).
func shortDir(t testing.TB) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "mr")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func tempSock(t testing.TB) string { return filepath.Join(shortDir(t), "rpc.sock") }

func quietLog() *slog.Logger { return slog.New(slog.DiscardHandler) }

// testServer is a Server serving on its own socket until the test ends.
type testServer struct {
	*Server
	sock string
	stop func() // ends Serve's context and waits for Serve to return
}

// startServer listens on a new socket with backend (a StubBackend when nil)
// and log (discarded when nil), and serves. tweak, when not nil, changes
// the server before Listen.
func startServer(t *testing.T, backend api.Backend, log *slog.Logger, tweak func(*Server)) *testServer {
	t.Helper()
	if backend == nil {
		backend = &StubBackend{Version: "test"}
	}
	if log == nil {
		log = quietLog()
	}
	s := NewServer(backend, log)
	if tweak != nil {
		tweak(s)
	}
	sock := tempSock(t)
	if err := s.Listen(sock); err != nil {
		t.Fatal(err)
	}
	return serve(t, s, sock)
}

// serve runs Serve for s, which listens on sock, until the test ends.
func serve(t *testing.T, s *Server, sock string) *testServer {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.Serve(ctx)
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}
	t.Cleanup(stop)
	return &testServer{Server: s, sock: sock, stop: stop}
}

func serverKey(s *Server) api.AuthKey {
	s.keyMu.Lock()
	defer s.keyMu.Unlock()
	if s.key == nil {
		return api.AuthKey{}
	}
	return s.key()
}

func pendingCount(s *Server) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pending
}

func connCount(s *Server) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

// waitFor polls cond until it holds, for at most 5 s.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// dial connects to sock; the connection is closed when the test ends.
func dial(t *testing.T, sock string) (net.Conn, *bufio.Reader) {
	t.Helper()
	c, err := net.DialTimeout("unix", sock, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, bufio.NewReader(c)
}

// clientHandshake authenticates c as the Go clients do.
func clientHandshake(c net.Conn, r *bufio.Reader, sock string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return api.ClientHandshake(ctx, c, r, api.KeyPath(sock))
}

// dialAuthed connects to sock and authenticates with api.ClientHandshake.
func dialAuthed(t *testing.T, sock string) (net.Conn, *bufio.Reader) {
	t.Helper()
	c, r := dial(t, sock)
	if err := clientHandshake(c, r, sock); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	return c, r
}

// sendLine writes s and a newline.
func sendLine(t *testing.T, c net.Conn, s string) {
	t.Helper()
	_ = c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(c, s+"\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = c.SetWriteDeadline(time.Time{})
}

// readLine returns the next line from the daemon, waiting at most d.
func readLine(c net.Conn, r *bufio.Reader, d time.Duration) ([]byte, error) {
	_ = c.SetReadDeadline(time.Now().Add(d))
	defer func() { _ = c.SetReadDeadline(time.Time{}) }()
	return r.ReadBytes('\n')
}

func readResponse(t *testing.T, c net.Conn, r *bufio.Reader) api.Response {
	t.Helper()
	line, err := readLine(c, r, 5*time.Second)
	if err != nil {
		t.Fatalf("no answer: %v", err)
	}
	var resp api.Response
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("bad answer %q: %v", line, err)
	}
	return resp
}

// call sends a request with id 1 and returns the answer.
func call(t *testing.T, c net.Conn, r *bufio.Reader, method string, params any) api.Response {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	line, err := json.Marshal(api.Request{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: method, Params: raw})
	if err != nil {
		t.Fatal(err)
	}
	sendLine(t, c, string(line))
	return readResponse(t, c, r)
}

// expectClosed fails unless the daemon closes c within d without sending
// anything more.
func expectClosed(t *testing.T, c net.Conn, r *bufio.Reader, d time.Duration) {
	t.Helper()
	line, err := readLine(c, r, d)
	switch {
	case len(line) > 0:
		t.Fatalf("expected the connection to be closed, got %q", line)
	case err == nil || isTimeout(err):
		t.Fatalf("the connection is still open after %v (%v)", d, err)
	}
}

// expectOpen fails unless c stays open and silent for d.
func expectOpen(t *testing.T, c net.Conn, r *bufio.Reader, d time.Duration) {
	t.Helper()
	line, err := readLine(c, r, d)
	if len(line) > 0 || !isTimeout(err) {
		t.Fatalf("expected an open, silent connection, got %q, %v", line, err)
	}
}

func helloLine(id, clientNonce string) string {
	return `{"jsonrpc":"2.0","id":` + id + `,"method":"system.hello","params":{"clientNonce":"` + clientNonce + `"}}`
}

func authLine(id, clientProof string) string {
	return `{"jsonrpc":"2.0","id":` + id + `,"method":"system.authenticate","params":{"clientProof":"` + clientProof + `"}}`
}

// helloExchange sends system.hello with cn and returns the daemon's nonce
// and proof from the very next line: nothing may come before the answer.
func helloExchange(t *testing.T, c net.Conn, r *bufio.Reader, cn api.AuthNonce) (api.AuthNonce, api.AuthProof) {
	t.Helper()
	sendLine(t, c, helloLine("1", cn.Hex()))
	resp := readResponse(t, c, r)
	if resp.JSONRPC != "2.0" || string(resp.ID) != "1" || resp.Error != nil {
		t.Fatalf("hello answer %+v", resp)
	}
	var res api.SystemHelloResult
	if err := json.Unmarshal(resp.Result, &res); err != nil {
		t.Fatalf("hello result: %v", err)
	}
	if res.ProtocolVersion != api.ProtocolVersion {
		t.Fatalf("protocolVersion %d", res.ProtocolVersion)
	}
	dn, ok1 := api.ParseAuthHex(res.DaemonNonce)
	dp, ok2 := api.ParseAuthHex(res.DaemonProof)
	if !ok1 || !ok2 {
		t.Fatal("daemonNonce or daemonProof is not 64 lowercase hex digits")
	}
	return dn, dp
}

// expectAuthenticated fails unless the very next line is the
// system.authenticate answer for id.
func expectAuthenticated(t *testing.T, c net.Conn, r *bufio.Reader, id string) {
	t.Helper()
	line, err := readLine(c, r, 5*time.Second)
	if err != nil {
		t.Fatalf("no system.authenticate answer: %v", err)
	}
	if got, want := strings.TrimRight(string(line), "\n"), `{"jsonrpc":"2.0","id":`+id+`,"result":{}}`; got != want {
		t.Fatalf("system.authenticate answer %s, want %s", got, want)
	}
}

// expectRejected fails unless the next line is error 1005 with id and the
// daemon closes c after it.
func expectRejected(t *testing.T, c net.Conn, r *bufio.Reader, id string) {
	t.Helper()
	resp := readResponse(t, c, r)
	if resp.JSONRPC != "2.0" || string(resp.ID) != id || resp.Result != nil ||
		resp.Error == nil || resp.Error.Code != api.CodeUnauthenticated {
		t.Fatalf("want error 1005 with id %s, got %+v", id, resp)
	}
	expectClosed(t, c, r, 5*time.Second)
}

// handshakeRecord is what one handshake by hand exchanged.
type handshakeRecord struct {
	cn, dn api.AuthNonce
	dp, cp api.AuthProof
}

// handshakeBy authenticates c by hand with key, checking that each answer
// is the very next line.
func handshakeBy(t *testing.T, c net.Conn, r *bufio.Reader, key api.AuthKey) handshakeRecord {
	t.Helper()
	cn := api.NewAuthNonce()
	dn, dp := helloExchange(t, c, r, cn)
	if !api.DaemonProof(key, cn, dn).Equal(dp) {
		t.Fatal("the daemon's proof does not verify")
	}
	cp := api.ClientProof(key, cn, dn)
	sendLine(t, c, authLine("2", cp.Hex()))
	expectAuthenticated(t, c, r, "2")
	return handshakeRecord{cn: cn, dn: dn, dp: dp, cp: cp}
}

// --- tests -----------------------------------------------------------------

func TestAllMethodsRegistered(t *testing.T) {
	s := NewServer(&StubBackend{}, slog.Default())
	for _, m := range api.AllMethods {
		if _, ok := s.handlers[m]; !ok {
			t.Errorf("method %q listed in api.AllMethods but not registered", m)
		}
	}
	for m := range s.handlers {
		found := false
		for _, want := range api.AllMethods {
			if m == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("handler %q registered but missing from api.AllMethods", m)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	ts := startServer(t, nil, nil, nil)
	c, r := dialAuthed(t, ts.sock)

	resp := call(t, c, r, api.MethodSystemInfo, api.SystemInfoParams{})
	if resp.Error != nil {
		t.Fatalf("system.info failed: %v", resp.Error)
	}
	var info api.SystemInfoResult
	if err := json.Unmarshal(resp.Result, &info); err != nil {
		t.Fatal(err)
	}
	if info.ProtocolVersion != api.ProtocolVersion || info.Version != "test" {
		t.Errorf("unexpected info: %+v", info)
	}

	resp = call(t, c, r, api.MethodMessageList, api.MessageListParams{AccountID: "x"})
	if resp.Error == nil || resp.Error.Code != api.CodeNotImplemented {
		t.Errorf("expected notImplemented, got %+v", resp)
	}

	resp = call(t, c, r, api.MethodAttachmentImport, api.AttachmentImportParams{AccountID: "x", Path: "/tmp/x"})
	if resp.Error == nil || resp.Error.Code != api.CodeNotImplemented {
		t.Errorf("expected notImplemented from the stub, got %+v", resp)
	}

	resp = call(t, c, r, "nope.nothing", nil)
	if resp.Error == nil || resp.Error.Code != api.CodeMethodNotFound {
		t.Errorf("expected methodNotFound, got %+v", resp)
	}
}

func TestStaleSocketIsReplaced(t *testing.T) {
	sock := tempSock(t)
	// A daemon that crashed left its socket, which nobody listens on any
	// more, and its key file.
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	ln.SetUnlinkOnClose(false)
	_ = ln.Close()
	if _, err := os.Lstat(sock); err != nil {
		t.Fatalf("the stale socket is gone: %v", err)
	}
	old := api.NewAuthKey()
	if err := os.WriteFile(api.KeyPath(sock), api.FormatKeyFile(old), 0o600); err != nil {
		t.Fatal(err)
	}

	s := NewServer(&StubBackend{}, quietLog())
	if err := s.Listen(sock); err != nil {
		t.Fatalf("expected the stale socket to be replaced: %v", err)
	}
	ts := serve(t, s, sock)
	k, err := api.ReadKeyFile(api.KeyPath(sock))
	if err != nil {
		t.Fatal(err)
	}
	if k.Equal(old) || !k.Equal(serverKey(s)) {
		t.Error("the key file does not hold the new daemon's key")
	}
	dialAuthed(t, ts.sock)
}

func TestListenRefusesWhatIsNotASocket(t *testing.T) {
	t.Run("regular file", func(t *testing.T) {
		sock := tempSock(t)
		if err := os.WriteFile(sock, []byte("not a socket"), 0o600); err != nil {
			t.Fatal(err)
		}
		s := NewServer(&StubBackend{}, quietLog())
		if err := s.Listen(sock); err == nil {
			s.Close()
			t.Fatal("Listen replaced a regular file")
		}
		if b, err := os.ReadFile(sock); err != nil || string(b) != "not a socket" {
			t.Errorf("the file changed: %q, %v", b, err)
		}
		if _, err := os.Lstat(api.KeyPath(sock)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("a key file was written: %v", err)
		}
	})
	t.Run("directory", func(t *testing.T) {
		sock := tempSock(t)
		if err := os.Mkdir(sock, 0o700); err != nil {
			t.Fatal(err)
		}
		s := NewServer(&StubBackend{}, quietLog())
		if err := s.Listen(sock); err == nil {
			s.Close()
			t.Fatal("Listen replaced a directory")
		}
		if fi, err := os.Lstat(sock); err != nil || !fi.IsDir() {
			t.Errorf("the directory changed: %v", err)
		}
	})
}

func TestSecondDaemonIsRefused(t *testing.T) {
	ts := startServer(t, nil, nil, nil)
	kp := api.KeyPath(ts.sock)
	before, err := os.ReadFile(kp)
	if err != nil {
		t.Fatal(err)
	}
	s2 := NewServer(&StubBackend{}, quietLog())
	if err := s2.Listen(ts.sock); err == nil || !strings.Contains(err.Error(), "already listening") {
		t.Fatalf("a second daemon was not refused: %v", err)
	}
	s2.Close()
	if after, err := os.ReadFile(kp); err != nil || !bytes.Equal(after, before) {
		t.Errorf("the refused daemon changed the key file (%v)", err)
	}
	dialAuthed(t, ts.sock)
}

func TestCloseLeavesASuccessorsSocket(t *testing.T) {
	ts := startServer(t, nil, nil, nil)
	// A successor took the path over: it removed this daemon's socket,
	// bound its own and wrote its key.
	if err := os.Remove(ts.sock); err != nil {
		t.Fatal(err)
	}
	succ, err := net.ListenUnix("unix", &net.UnixAddr{Name: ts.sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = succ.Close() })
	kp := api.KeyPath(ts.sock)
	other := api.FormatKeyFile(api.NewAuthKey())
	if err := os.WriteFile(kp, other, 0o600); err != nil {
		t.Fatal(err)
	}

	ts.stop()

	if fi, err := os.Lstat(ts.sock); err != nil || fi.Mode().Type() != fs.ModeSocket {
		t.Fatalf("the successor's socket is gone: %v", err)
	}
	c, err := net.DialTimeout("unix", ts.sock, 2*time.Second)
	if err != nil {
		t.Fatalf("the successor's socket does not answer: %v", err)
	}
	_ = c.Close()
	if b, err := os.ReadFile(kp); err != nil || !bytes.Equal(b, other) {
		t.Errorf("the successor's key file changed (%v)", err)
	}
}

func TestConnAcceptedAfterCloseIsClosed(t *testing.T) {
	s := NewServer(&StubBackend{}, quietLog())
	s.Close()
	a, b := net.Pipe()
	defer b.Close()
	s.admit(context.Background(), a, nil)
	_ = b.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := b.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("the connection is still open: %v", err)
	}
	if n, p := connCount(s), pendingCount(s); n != 0 || p != 0 {
		t.Errorf("%d connections, %d pending after Close", n, p)
	}
}

func TestListenAfterClose(t *testing.T) {
	sock := tempSock(t)
	s := NewServer(&StubBackend{}, quietLog())
	s.Close()
	if err := s.Listen(sock); err == nil {
		t.Fatal("Listen after Close succeeded")
	}
	for _, p := range []string{sock, api.KeyPath(sock)} {
		if _, err := os.Lstat(p); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s was created: %v", filepath.Base(p), err)
		}
	}
}

func TestCloseDuringHandshake(t *testing.T) {
	ts := startServer(t, nil, nil, nil)
	c, r := dial(t, ts.sock)
	helloExchange(t, c, r, api.NewAuthNonce())

	ts.stop()

	expectClosed(t, c, r, 5*time.Second)
	for _, p := range []string{ts.sock, api.KeyPath(ts.sock)} {
		if _, err := os.Lstat(p); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s is still there: %v", filepath.Base(p), err)
		}
	}
	ts.Close() // again: harmless
	waitFor(t, "the connection to go", func() bool { return connCount(ts.Server) == 0 })
}

func TestHandshakeMethodsAfterAuth(t *testing.T) {
	ts := startServer(t, nil, nil, nil)
	c, r := dialAuthed(t, ts.sock)
	for _, m := range []struct {
		method string
		params any
	}{
		{api.MethodSystemHello, api.SystemHelloParams{ClientNonce: api.NewAuthNonce().Hex()}},
		{api.MethodSystemAuthenticate, api.SystemAuthenticateParams{ClientProof: strings.Repeat("0", 64)}},
	} {
		resp := call(t, c, r, m.method, m.params)
		if resp.Error == nil || resp.Error.Code != api.CodeInvalidRequest {
			t.Errorf("%s after authentication: %+v, want invalidRequest", m.method, resp)
		}
	}
	if resp := call(t, c, r, api.MethodSystemInfo, api.SystemInfoParams{}); resp.Error != nil {
		t.Errorf("the connection is not usable any more: %v", resp.Error)
	}
}

func TestLargeRequestAfterAuth(t *testing.T) {
	ts := startServer(t, nil, nil, nil)
	c, r := dialAuthed(t, ts.sock)
	params := map[string]string{"padding": strings.Repeat("x", 64<<10)}
	if resp := call(t, c, r, api.MethodSystemInfo, params); resp.Error != nil {
		t.Errorf("a 64 KiB request after authentication: %v", resp.Error)
	}
}
