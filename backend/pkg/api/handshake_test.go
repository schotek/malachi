// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// testKey is in the key file of these tests; otherKey is another run's.
var testKey, otherKey = NewAuthKey(), NewAuthKey()

// A notification as a daemon of protocol 1 broadcasts it to everyone.
const testNotification = `{"jsonrpc":"2.0","method":"notify.accountsChanged","params":{}}`

// fakeDaemon plays the daemon on one end of a net.Pipe, from a script.
type fakeDaemon struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
	key  AuthKey // the key it proves with

	lines       []string  // the client's lines, in order
	clientNonce AuthNonce // from system.hello
	daemonNonce AuthNonce
	after       string // what the client sent after the script stopped reading lines
}

func (d *fakeDaemon) read() string {
	line, _ := d.r.ReadString('\n')
	if line != "" {
		d.lines = append(d.lines, line)
	}
	return line
}

// hello reads system.hello and keeps the client's nonce.
func (d *fakeDaemon) hello() bool {
	var req struct {
		JSONRPC string            `json:"jsonrpc"`
		ID      json.RawMessage   `json:"id"`
		Method  string            `json:"method"`
		Params  SystemHelloParams `json:"params"`
	}
	line := d.read()
	if json.Unmarshal([]byte(line), &req) != nil || req.JSONRPC != "2.0" || string(req.ID) != "1" || req.Method != MethodSystemHello {
		d.t.Errorf("expected system.hello, got %q", line)
		return false
	}
	n, ok := ParseAuthHex(req.Params.ClientNonce)
	if !ok {
		d.t.Errorf("bad clientNonce in %q", line)
		return false
	}
	d.clientNonce = n
	return true
}

// helloResult is the right system.hello result on this connection.
func (d *fakeDaemon) helloResult() map[string]any {
	return map[string]any{
		"protocolVersion": ProtocolVersion,
		"daemonNonce":     d.daemonNonce.Hex(),
		"daemonProof":     DaemonProof(d.key, d.clientNonce, d.daemonNonce).Hex(),
	}
}

// authenticate reads system.authenticate and checks the client's proof.
func (d *fakeDaemon) authenticate() bool {
	var req struct {
		JSONRPC string                   `json:"jsonrpc"`
		ID      json.RawMessage          `json:"id"`
		Method  string                   `json:"method"`
		Params  SystemAuthenticateParams `json:"params"`
	}
	line := d.read()
	if json.Unmarshal([]byte(line), &req) != nil || req.JSONRPC != "2.0" || string(req.ID) != "2" || req.Method != MethodSystemAuthenticate {
		d.t.Errorf("expected system.authenticate, got %q", line)
		return false
	}
	if want := ClientProof(d.key, d.clientNonce, d.daemonNonce).Hex(); req.Params.ClientProof != want {
		d.t.Errorf("clientProof %s, want %s", req.Params.ClientProof, want)
		return false
	}
	return true
}

// send writes the lines in a single Write.
func (d *fakeDaemon) send(lines ...string) {
	_, _ = d.conn.Write([]byte(strings.Join(lines, "\n") + "\n"))
}

// drain reads whatever else the client sends, until it closes its end.
func (d *fakeDaemon) drain() {
	b, _ := io.ReadAll(d.r)
	d.after = string(b)
}

// secrets lists what no error about this connection may show.
func (d *fakeDaemon) secrets() []string {
	s := []string{d.clientNonce.Hex(), d.daemonNonce.Hex()}
	for _, k := range []AuthKey{testKey, otherKey, d.key} {
		s = append(s, hex.EncodeToString(k[:]),
			DaemonProof(k, d.clientNonce, d.daemonNonce).Hex(),
			ClientProof(k, d.clientNonce, d.daemonNonce).Hex())
	}
	return s
}

type pipeRun struct {
	client net.Conn
	r      *bufio.Reader // the caller's reader over client
	d      *fakeDaemon
	done   chan struct{}
}

// startFake runs script as a daemon proving with key.
func startFake(t *testing.T, key AuthKey, script func(d *fakeDaemon)) *pipeRun {
	t.Helper()
	c, s := net.Pipe()
	p := &pipeRun{
		client: c,
		r:      bufio.NewReader(c),
		d:      &fakeDaemon{t: t, conn: s, r: bufio.NewReader(s), key: key, daemonNonce: NewAuthNonce()},
		done:   make(chan struct{}),
	}
	go func() {
		defer close(p.done)
		defer s.Close()
		script(p.d)
	}()
	t.Cleanup(p.finish)
	return p
}

// finish closes the client's end and waits for the script to end.
func (p *pipeRun) finish() {
	_ = p.client.Close()
	<-p.done
}

// keyFile writes content as the key file of a new socket and returns its
// path; with nil content there is no file.
func keyFile(t *testing.T, content []byte) string {
	t.Helper()
	path := KeyPath(filepath.Join(t.TempDir(), "rpc.sock"))
	if content != nil {
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func response(id, result any) string {
	b, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	if err != nil {
		panic(err)
	}
	return string(b)
}

func errorResponse(id any, code ErrorCode, message string) string {
	b, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}})
	if err != nil {
		panic(err)
	}
	return string(b)
}

func notifications(n int) []string {
	s := make([]string, n)
	for i := range s {
		s[i] = testNotification
	}
	return s
}

// replies reads system.hello, writes lines(d) in one Write and then reads
// whatever else the client sends.
func replies(lines func(d *fakeDaemon) []string) func(*fakeDaemon) {
	return func(d *fakeDaemon) {
		if d.hello() {
			d.send(lines(d)...)
		}
		d.drain()
	}
}

// helloWith answers system.hello with the right result as edit changes it.
func helloWith(edit func(d *fakeDaemon, r map[string]any)) func(*fakeDaemon) {
	return replies(func(d *fakeDaemon) []string {
		r := d.helloResult()
		edit(d, r)
		return []string{response(1, r)}
	})
}

// rawHello answers system.hello with format, whose %s is the JSON of the
// right result.
func rawHello(format string) func(*fakeDaemon) {
	return replies(func(d *fakeDaemon) []string {
		res, err := json.Marshal(d.helloResult())
		if err != nil {
			panic(err)
		}
		return []string{fmt.Sprintf(format, res)}
	})
}

// answerLine answers system.hello with one fixed line.
func answerLine(s string) func(*fakeDaemon) {
	return replies(func(*fakeDaemon) []string { return []string{s} })
}

// authReplies answers system.hello rightly, reads system.authenticate,
// writes lines(d) in one Write and then reads whatever else comes.
func authReplies(lines func(d *fakeDaemon) []string) func(*fakeDaemon) {
	return func(d *fakeDaemon) {
		if d.hello() {
			d.send(response(1, d.helloResult()))
			if d.authenticate() {
				d.send(lines(d)...)
			}
		}
		d.drain()
	}
}

var answerHello = helloWith(func(*fakeDaemon, map[string]any) {})

type failCase struct {
	name      string
	keyFile   []byte  // content of the key file; nil = FormatKeyFile(testKey)
	noKey     bool    // no key file at all
	daemonKey AuthKey // the key the daemon proves with; zero = testKey
	script    func(d *fakeDaemon)

	reason HandshakeReason
	daemon int       // HandshakeProtocolMismatch
	code   ErrorCode // HandshakeRejected
	sent   int       // lines the client sent: 1 = only system.hello
	is     []error   // errors.Is must hold for each
}

func failCases() []failCase {
	set := func(key string, v any) func(*fakeDaemon, map[string]any) {
		return func(_ *fakeDaemon, r map[string]any) { r[key] = v }
	}
	upper := func(key string) func(*fakeDaemon, map[string]any) {
		return func(_ *fakeDaemon, r map[string]any) { r[key] = strings.ToUpper(r[key].(string)) }
	}
	const (
		mismatch = HandshakeProtocolMismatch
		keyGone  = HandshakeKeyUnavailable
		unproven = HandshakeDaemonUnproven
		rejected = HandshakeRejected
		garbled  = HandshakeMalformed
	)
	return []failCase{
		// Other protocol versions: nothing is sent after system.hello and the
		// key is not read.
		{name: "protocol 1", script: answerLine(errorResponse(1, CodeMethodNotFound, `unknown method "system.hello"`)),
			reason: mismatch, daemon: 1, sent: 1},
		{name: "protocol 1 after 8 notifications", script: replies(func(*fakeDaemon) []string {
			return append(notifications(8), errorResponse(1, CodeMethodNotFound, "unknown method"))
		}), reason: mismatch, daemon: 1, sent: 1},
		{name: "protocol 99 without a key file", noKey: true, script: answerLine(response(1, map[string]any{"protocolVersion": 99})),
			reason: mismatch, daemon: 99, sent: 1},
		{name: "protocol 3 with another result", noKey: true,
			script: answerLine(response(1, map[string]any{"protocolVersion": 3, "daemonNonce": map[string]any{"bytes": 32}})),
			reason: mismatch, daemon: 3, sent: 1},

		// The key file.
		{name: "missing key file", noKey: true, script: answerHello, reason: keyGone, sent: 1,
			is: []error{ErrKeyUnavailable, fs.ErrNotExist}},
		{name: "empty key file", keyFile: []byte{}, script: answerHello, reason: keyGone, sent: 1, is: []error{ErrKeyUnavailable}},
		{name: "upper-case key file", keyFile: bytes.ToUpper(FormatKeyFile(testKey)), script: answerHello, reason: keyGone, sent: 1,
			is: []error{ErrKeyUnavailable}},
		{name: "key file with CRLF", keyFile: []byte(hex.EncodeToString(testKey[:]) + "\r\n"), script: answerHello, reason: keyGone,
			sent: 1, is: []error{ErrKeyUnavailable}},

		// Proofs that are not the daemon's: authenticate is never sent.
		{name: "another key", daemonKey: otherKey, script: answerHello, reason: unproven, sent: 1},
		{name: "reflected proof", script: helloWith(func(d *fakeDaemon, r map[string]any) {
			r["daemonProof"] = ClientProof(d.key, d.clientNonce, d.daemonNonce).Hex()
		}), reason: unproven, sent: 1},
		{name: "swapped nonces", script: helloWith(func(d *fakeDaemon, r map[string]any) {
			r["daemonProof"] = DaemonProof(d.key, d.daemonNonce, d.clientNonce).Hex()
		}), reason: unproven, sent: 1},
		{name: "zero proof", script: helloWith(set("daemonProof", strings.Repeat("0", 64))), reason: unproven, sent: 1},

		// Malformed system.hello answers.
		{name: "upper-case daemonNonce", script: helloWith(upper("daemonNonce")), reason: garbled, sent: 1},
		{name: "upper-case daemonProof", script: helloWith(upper("daemonProof")), reason: garbled, sent: 1},
		{name: "short daemonNonce", script: helloWith(func(d *fakeDaemon, r map[string]any) {
			r["daemonNonce"] = d.daemonNonce.Hex()[:62]
		}), reason: garbled, sent: 1},
		{name: "missing daemonProof", script: helloWith(func(_ *fakeDaemon, r map[string]any) { delete(r, "daemonProof") }),
			reason: garbled, sent: 1},
		{name: "daemonProof a number", script: helloWith(set("daemonProof", 5)), reason: garbled, sent: 1},
		{name: "missing protocolVersion", script: helloWith(func(_ *fakeDaemon, r map[string]any) { delete(r, "protocolVersion") }),
			reason: garbled, sent: 1},
		{name: "protocolVersion 0", script: helloWith(set("protocolVersion", 0)), reason: garbled, sent: 1},
		{name: "negative protocolVersion", script: helloWith(set("protocolVersion", -2)), reason: garbled, sent: 1},
		{name: "protocolVersion a string", script: helloWith(set("protocolVersion", "2")), reason: garbled, sent: 1},
		{name: "protocolVersion a fraction", script: helloWith(set("protocolVersion", 2.5)), reason: garbled, sent: 1},
		{name: "result null", script: answerLine(`{"jsonrpc":"2.0","id":1,"result":null}`), reason: garbled, sent: 1},
		{name: "result an array", script: answerLine(`{"jsonrpc":"2.0","id":1,"result":[2]}`), reason: garbled, sent: 1},
		{name: "result a string", script: answerLine(`{"jsonrpc":"2.0","id":1,"result":"ok"}`), reason: garbled, sent: 1},
		{name: "result a number", script: answerLine(`{"jsonrpc":"2.0","id":1,"result":2}`), reason: garbled, sent: 1},
		{name: "result and error", script: rawHello(`{"jsonrpc":"2.0","id":1,"result":%s,"error":{"code":1005,"message":"no"}}`),
			reason: garbled, sent: 1},
		{name: "neither result nor error", script: answerLine(`{"jsonrpc":"2.0","id":1}`), reason: garbled, sent: 1},
		{name: "wrong id", script: rawHello(`{"jsonrpc":"2.0","id":7,"result":%s}`), reason: garbled, sent: 1},
		{name: "id a string", script: rawHello(`{"jsonrpc":"2.0","id":"1","result":%s}`), reason: garbled, sent: 1},
		{name: "missing id", script: rawHello(`{"jsonrpc":"2.0","result":%s}`), reason: garbled, sent: 1},
		{name: "null id", script: rawHello(`{"jsonrpc":"2.0","id":null,"result":%s}`), reason: garbled, sent: 1},
		{name: "no jsonrpc member", script: rawHello(`{"id":1,"result":%s}`), reason: garbled, sent: 1},
		{name: "JSON-RPC 1.0", script: rawHello(`{"jsonrpc":"1.0","id":1,"result":%s}`), reason: garbled, sent: 1},
		{name: "a request", script: answerLine(`{"jsonrpc":"2.0","id":1,"method":"system.hello","params":{}}`), reason: garbled, sent: 1},
		{name: "a JSON array", script: rawHello(`[{"jsonrpc":"2.0","id":1,"result":%s}]`), reason: garbled, sent: 1},
		{name: "not JSON", script: answerLine(`hello`), reason: garbled, sent: 1},
		{name: "JSON null", script: answerLine(`null`), reason: garbled, sent: 1},
		{name: "an empty line", script: answerLine(``), reason: garbled, sent: 1},
		{name: "an empty message", script: answerLine(`{"jsonrpc":"2.0"}`), reason: garbled, sent: 1},
		{name: "9 notifications", script: replies(func(d *fakeDaemon) []string {
			return append(notifications(9), response(1, d.helloResult()))
		}), reason: garbled, sent: 1},
		{name: "a line over 64 KiB", script: replies(func(d *fakeDaemon) []string {
			big := `{"jsonrpc":"2.0","method":"notify.big","params":{"p":"` + strings.Repeat("a", 64<<10) + `"}}`
			return []string{big, response(1, d.helloResult())}
		}), reason: garbled, sent: 1},

		// Answers other than success.
		{name: "system.hello rejected", script: answerLine(errorResponse(1, CodeUnauthenticated, "no")),
			reason: rejected, code: CodeUnauthenticated, sent: 1},
		{name: "system.hello invalidRequest", script: answerLine(errorResponse(1, CodeInvalidRequest, "already authenticated")),
			reason: rejected, code: CodeInvalidRequest, sent: 1},
		{name: "system.authenticate rejected", script: authReplies(func(d *fakeDaemon) []string {
			// The message is dropped; it quotes a nonce to prove that.
			return []string{errorResponse(2, CodeUnauthenticated, "wrong proof for "+d.clientNonce.Hex())}
		}), reason: rejected, code: CodeUnauthenticated, sent: 2},
		{name: "notification before the system.authenticate answer", script: authReplies(func(*fakeDaemon) []string {
			return []string{testNotification, response(2, map[string]any{})}
		}), reason: garbled, sent: 2},
		{name: "system.authenticate result null", script: authReplies(func(*fakeDaemon) []string {
			return []string{`{"jsonrpc":"2.0","id":2,"result":null}`}
		}), reason: garbled, sent: 2},
		{name: "system.authenticate without a result", script: authReplies(func(*fakeDaemon) []string {
			return []string{`{"jsonrpc":"2.0","id":2}`}
		}), reason: garbled, sent: 2},
		{name: "system.authenticate result not an object", script: authReplies(func(*fakeDaemon) []string {
			return []string{`{"jsonrpc":"2.0","id":2,"result":true}`}
		}), reason: garbled, sent: 2},
		{name: "system.authenticate answered with id 1", script: authReplies(func(*fakeDaemon) []string {
			return []string{response(1, map[string]any{})}
		}), reason: garbled, sent: 2},
	}
}

func runFailCase(t *testing.T, fc failCase) (*fakeDaemon, error) {
	t.Helper()
	content := fc.keyFile
	if content == nil && !fc.noKey {
		content = FormatKeyFile(testKey)
	}
	path := keyFile(t, content)
	key := testKey
	if fc.daemonKey != (AuthKey{}) {
		key = fc.daemonKey
	}
	p := startFake(t, key, fc.script)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := ClientHandshake(ctx, p.client, p.r, path)
	p.finish()
	return p.d, err
}

func TestHandshakeFailures(t *testing.T) {
	for _, fc := range failCases() {
		t.Run(fc.name, func(t *testing.T) {
			d, err := runFailCase(t, fc)
			var he *HandshakeError
			if !errors.As(err, &he) {
				t.Fatalf("got %v, want a HandshakeError %s", err, fc.reason)
			}
			if he.Reason != fc.reason || he.Daemon != fc.daemon || he.Code != fc.code {
				t.Errorf("got %s (daemon %d, code %d): %v; want %s (daemon %d, code %d)",
					he.Reason, he.Daemon, he.Code, err, fc.reason, fc.daemon, fc.code)
			}
			for _, target := range fc.is {
				if !errors.Is(err, target) {
					t.Errorf("%v is not %v", err, target)
				}
			}
			if len(d.lines) != fc.sent || d.after != "" {
				t.Errorf("the client sent %d lines, then %q; want %d and nothing more", len(d.lines), d.after, fc.sent)
			}
		})
	}
}

// deadlineRecorder records the deadlines ClientHandshake sets.
type deadlineRecorder struct {
	net.Conn
	mu        sync.Mutex
	deadlines []time.Time
}

func (c *deadlineRecorder) SetDeadline(t time.Time) error {
	c.mu.Lock()
	c.deadlines = append(c.deadlines, t)
	c.mu.Unlock()
	return c.Conn.SetDeadline(t)
}

// successScript completes the handshake and writes the system.authenticate
// answer and a notification in a single Write.
func successScript(d *fakeDaemon) {
	if d.hello() {
		d.send(response(1, d.helloResult()))
		if d.authenticate() {
			d.send(response(2, map[string]any{}), testNotification)
		}
	}
	d.drain()
}

func TestHandshakeSuccess(t *testing.T) {
	p := startFake(t, testKey, successScript)
	conn := &deadlineRecorder{Conn: p.client}
	start := time.Now()
	if err := ClientHandshake(context.Background(), conn, p.r, keyFile(t, FormatKeyFile(testKey))); err != nil {
		t.Fatal(err)
	}
	end := time.Now()

	// The notification that came in the same Write as the answer is still
	// in the caller's reader.
	_ = p.client.SetReadDeadline(time.Now().Add(2 * time.Second))
	if got, err := p.r.ReadString('\n'); err != nil || got != testNotification+"\n" {
		t.Fatalf("after the handshake: %q, %v", got, err)
	}
	p.finish()

	d := p.d
	want := []string{
		`{"jsonrpc":"2.0","id":1,"method":"system.hello","params":{"clientNonce":"` + d.clientNonce.Hex() + `"}}` + "\n",
		`{"jsonrpc":"2.0","id":2,"method":"system.authenticate","params":{"clientProof":"` +
			ClientProof(testKey, d.clientNonce, d.daemonNonce).Hex() + `"}}` + "\n",
	}
	if len(d.lines) != 2 || d.lines[0] != want[0] || d.lines[1] != want[1] || d.after != "" {
		t.Errorf("the client sent %q, then %q; want %q", d.lines, d.after, want)
	}

	conn.mu.Lock()
	defer conn.mu.Unlock()
	ds := conn.deadlines
	if len(ds) < 2 || ds[0].Before(start.Add(HandshakeTimeout)) || ds[0].After(end.Add(HandshakeTimeout)) || !ds[len(ds)-1].IsZero() {
		t.Errorf("deadlines %v: want now+HandshakeTimeout, then none", ds)
	}
}

func TestHandshakeSkipsNotifications(t *testing.T) {
	p := startFake(t, testKey, func(d *fakeDaemon) {
		if d.hello() {
			nulled := `{"jsonrpc":"2.0","id":null,"method":"notify.syncState","params":{}}`
			d.send(append(append(notifications(7), nulled), response(1, d.helloResult()))...)
			if d.authenticate() {
				d.send(response(2, map[string]any{}))
			}
		}
		d.drain()
	})
	if err := ClientHandshake(context.Background(), p.client, p.r, keyFile(t, FormatKeyFile(testKey))); err != nil {
		t.Fatal(err)
	}
}

func TestHandshakeFreshNonce(t *testing.T) {
	seen := map[AuthNonce]bool{}
	for range 5 {
		d, _ := runFailCase(t, failCase{script: answerLine(errorResponse(1, CodeMethodNotFound, "unknown method"))})
		if len(d.lines) != 1 {
			t.Fatalf("the client sent %q", d.lines)
		}
		var req map[string]any
		if err := json.Unmarshal([]byte(d.lines[0]), &req); err != nil || len(req) != 4 || req["jsonrpc"] != "2.0" ||
			req["id"] != 1.0 || req["method"] != MethodSystemHello {
			t.Fatalf("system.hello line %q", d.lines[0])
		}
		if params, _ := req["params"].(map[string]any); len(params) != 1 || params["clientNonce"] != d.clientNonce.Hex() {
			t.Fatalf("system.hello params in %q", d.lines[0])
		}
		if seen[d.clientNonce] || d.clientNonce == (AuthNonce{}) {
			t.Fatalf("clientNonce %s repeats or is zero", d.clientNonce.Hex())
		}
		seen[d.clientNonce] = true
	}
}

// droppedScripts close the daemon's end at points of the handshake.
func droppedScripts() map[string]func(d *fakeDaemon) {
	return map[string]func(d *fakeDaemon){
		"closed at once":     func(*fakeDaemon) {},
		"closed after hello": func(d *fakeDaemon) { d.hello() },
		"closed mid-answer": func(d *fakeDaemon) {
			if d.hello() {
				_, _ = d.conn.Write([]byte(`{"jsonrpc":"2.0","id":1,"res`))
			}
		},
		"closed after authenticate": func(d *fakeDaemon) {
			if d.hello() {
				d.send(response(1, d.helloResult()))
				d.authenticate()
			}
		},
	}
}

func TestHandshakeConnectionDropped(t *testing.T) {
	for name, script := range droppedScripts() {
		t.Run(name, func(t *testing.T) {
			p := startFake(t, testKey, script)
			err := ClientHandshake(context.Background(), p.client, p.r, keyFile(t, FormatKeyFile(testKey)))
			p.finish()
			var he *HandshakeError
			if err == nil || errors.As(err, &he) || !strings.HasPrefix(err.Error(), "rpc handshake: ") {
				t.Fatalf("got %v, want a plain error", err)
			}
			if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrClosedPipe) {
				t.Errorf("%v: the cause is lost", err)
			}
		})
	}
}

func TestHandshakeTimesOut(t *testing.T) {
	for name, script := range map[string]func(d *fakeDaemon){
		"silent after hello": func(d *fakeDaemon) {
			d.hello()
			d.drain()
		},
		"silent after authenticate": func(d *fakeDaemon) {
			if d.hello() {
				d.send(response(1, d.helloResult()))
				d.authenticate()
			}
			d.drain()
		},
	} {
		t.Run(name, func(t *testing.T) {
			p := startFake(t, testKey, script)
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			start := time.Now()
			err := ClientHandshake(ctx, p.client, p.r, keyFile(t, FormatKeyFile(testKey)))
			elapsed := time.Since(start)
			p.finish()
			var he *HandshakeError
			if !errors.As(err, &he) || he.Reason != HandshakeTimedOut || !errors.Is(err, os.ErrDeadlineExceeded) {
				t.Fatalf("got %v, want timedOut", err)
			}
			if elapsed > HandshakeTimeout/2 {
				t.Errorf("took %v with a context of 150ms", elapsed)
			}
		})
	}
}

func TestHandshakeCancelled(t *testing.T) {
	t.Run("while waiting", func(t *testing.T) {
		asked := make(chan struct{})
		p := startFake(t, testKey, func(d *fakeDaemon) {
			d.hello()
			close(asked)
			d.drain()
		})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			<-asked
			cancel()
		}()
		start := time.Now()
		err := ClientHandshake(ctx, p.client, p.r, keyFile(t, FormatKeyFile(testKey)))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want context.Canceled", err)
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Errorf("took %v", elapsed)
		}
	})
	t.Run("before", func(t *testing.T) {
		p := startFake(t, testKey, func(d *fakeDaemon) { d.drain() })
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := ClientHandshake(ctx, p.client, p.r, keyFile(t, FormatKeyFile(testKey)))
		p.finish()
		if !errors.Is(err, context.Canceled) || p.d.after != "" {
			t.Fatalf("got %v after sending %q, want context.Canceled and nothing sent", err, p.d.after)
		}
	})
}

func TestHandshakeClearsDeadline(t *testing.T) {
	const timeout = 200 * time.Millisecond
	late := make(chan struct{})
	p := startFake(t, testKey, func(d *fakeDaemon) {
		if d.hello() {
			d.send(response(1, d.helloResult()))
			if d.authenticate() {
				d.send(response(2, map[string]any{}))
				<-late
				d.send(testNotification)
			}
		}
		d.drain()
	})
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := ClientHandshake(ctx, p.client, p.r, keyFile(t, FormatKeyFile(testKey))); err != nil {
		close(late)
		t.Fatal(err)
	}
	time.Sleep(2 * timeout)
	close(late)
	if got, err := p.r.ReadString('\n'); err != nil || got != testNotification+"\n" {
		t.Fatalf("read after the handshake: %q, %v", got, err)
	}
}

func TestHandshakeErrorsCarryNoSecrets(t *testing.T) {
	type outcome struct {
		name string
		d    *fakeDaemon
		err  error
	}
	var runs []outcome
	for _, fc := range failCases() {
		d, err := runFailCase(t, fc)
		runs = append(runs, outcome{fc.name, d, err})
	}
	for name, script := range droppedScripts() {
		d, err := runFailCase(t, failCase{script: script})
		runs = append(runs, outcome{name, d, err})
	}
	p := startFake(t, testKey, func(d *fakeDaemon) {
		if d.hello() {
			d.send(response(1, d.helloResult()))
			d.authenticate()
		}
		d.drain()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := ClientHandshake(ctx, p.client, p.r, keyFile(t, FormatKeyFile(testKey)))
	p.finish()
	runs = append(runs, outcome{"timeout", p.d, err})

	for _, r := range runs {
		if r.err == nil {
			t.Errorf("%s: no error", r.name)
			continue
		}
		texts := []string{r.err.Error(), fmt.Sprintf("%v", r.err), fmt.Sprintf("%+v", r.err), fmt.Sprintf("%#v", r.err)}
		for e := errors.Unwrap(r.err); e != nil; e = errors.Unwrap(e) {
			texts = append(texts, e.Error())
		}
		for _, text := range texts {
			for _, s := range r.d.secrets() {
				if strings.Contains(strings.ToLower(text), s) {
					t.Errorf("%s: %q shows a key, nonce or proof", r.name, text)
				}
			}
		}
	}
}

func TestHandshakeLinesFitBudget(t *testing.T) {
	p := startFake(t, testKey, successScript)
	if err := ClientHandshake(context.Background(), p.client, p.r, keyFile(t, FormatKeyFile(testKey))); err != nil {
		t.Fatal(err)
	}
	p.finish()
	n := 0
	for _, l := range p.d.lines {
		n += len(l)
	}
	if len(p.d.lines) != 2 || n > MaxHandshakeBytes/4 {
		t.Errorf("the handshake is %d bytes in %d lines; the daemon reads at most %d", n, len(p.d.lines), MaxHandshakeBytes)
	}
}

func TestHandshakeErrorText(t *testing.T) {
	for _, c := range []struct {
		err  *HandshakeError
		want string
	}{
		{&HandshakeError{Reason: HandshakeProtocolMismatch, Daemon: 1},
			fmt.Sprintf("malachid speaks protocol version 1, this client %d", ProtocolVersion)},
		{&HandshakeError{Reason: HandshakeKeyUnavailable, Err: ErrKeyUnavailable},
			"cannot use malachid's connection key: rpc key unavailable"},
		{&HandshakeError{Reason: HandshakeDaemonUnproven},
			"the process on the socket did not prove it holds malachid's connection key"},
		{&HandshakeError{Reason: HandshakeRejected, Code: CodeUnauthenticated},
			"malachid rejected the handshake (unauthenticated)"},
		{malformed("a line is longer than 64 KiB"),
			"malformed handshake answer from malachid: a line is longer than 64 KiB"},
		{&HandshakeError{Reason: HandshakeTimedOut},
			"malachid did not complete the handshake in time"},
	} {
		if got := c.err.Error(); got != c.want {
			t.Errorf("%s: %q, want %q", c.err.Reason, got, c.want)
		}
	}
	for r, want := range map[HandshakeReason]string{
		HandshakeProtocolMismatch: "protocolMismatch",
		HandshakeKeyUnavailable:   "keyUnavailable",
		HandshakeDaemonUnproven:   "daemonUnproven",
		HandshakeRejected:         "rejected",
		HandshakeMalformed:        "malformed",
		HandshakeTimedOut:         "timedOut",
		0:                         "unknown(0)",
	} {
		if r.String() != want {
			t.Errorf("%d: %q, want %q", int(r), r.String(), want)
		}
	}
}
