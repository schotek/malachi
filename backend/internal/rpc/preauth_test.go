// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package rpc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// countingBackend counts the calls that reach it.
type countingBackend struct {
	StubBackend
	calls atomic.Int64
}

func (b *countingBackend) System() api.SystemService {
	return countingSystem{stubSystem{&b.StubBackend}, &b.calls}
}
func (b *countingBackend) Accounts() api.AccountService {
	return countingAccounts{stubAccounts{}, &b.calls}
}
func (b *countingBackend) Messages() api.MessageService {
	return countingMessages{stubMessages{}, &b.calls}
}

type countingSystem struct {
	stubSystem
	calls *atomic.Int64
}

func (s countingSystem) Info(ctx context.Context, p api.SystemInfoParams) (*api.SystemInfoResult, error) {
	s.calls.Add(1)
	return s.stubSystem.Info(ctx, p)
}

type countingAccounts struct {
	stubAccounts
	calls *atomic.Int64
}

func (a countingAccounts) List(ctx context.Context, p api.AccountListParams) (*api.AccountListResult, error) {
	a.calls.Add(1)
	return a.stubAccounts.List(ctx, p)
}

type countingMessages struct {
	stubMessages
	calls *atomic.Int64
}

func (m countingMessages) List(ctx context.Context, p api.MessageListParams) (*api.MessageListResult, error) {
	m.calls.Add(1)
	return m.stubMessages.List(ctx, p)
}

// logBuffer collects log output from several goroutines.
type logBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// captureLog returns a logger that records everything down to Debug.
func captureLog() (*slog.Logger, *logBuffer) {
	buf := &logBuffer{}
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})), buf
}

// testClock is a clock that moves only when told to.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newTestClock() *testClock { return &testClock{t: time.Unix(1_800_000_000, 0)} }

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func TestCallsBeforeHandshakeAreRejected(t *testing.T) {
	b := &countingBackend{}
	ts := startServer(t, b, nil, nil)
	nonce := api.NewAuthNonce().Hex()
	for _, tc := range []struct{ name, line, id string }{
		{"system.info, as a client of protocol 1 calls first", `{"jsonrpc":"2.0","id":7,"method":"system.info","params":{}}`, "7"},
		{"message.list", `{"jsonrpc":"2.0","id":"abc","method":"message.list","params":{"accountId":"a","folderId":"f"}}`, `"abc"`},
		{"account.list", `{"jsonrpc":"2.0","id":3,"method":"account.list"}`, "3"},
		{"authenticate before hello", authLine("4", strings.Repeat("ab", 32)), "4"},
		{"unknown method", `{"jsonrpc":"2.0","id":5,"method":"nope.nothing"}`, "5"},
		{"no method", `{"jsonrpc":"2.0","id":6}`, "6"},
		{"method not a string", `{"jsonrpc":"2.0","id":7,"method":["system.hello"],"params":{"clientNonce":"` + nonce + `"}}`, "7"},
		{"method in another case", `{"jsonrpc":"2.0","id":8,"method":"System.Hello","params":{"clientNonce":"` + nonce + `"}}`, "8"},
		{"a fraction as id", `{"jsonrpc":"2.0","id":1.5,"method":"system.info"}`, "1.5"},
		{"an object as id", `{"jsonrpc":"2.0","id":{"a":[1]},"method":"system.info"}`, `{"a":[1]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, r := dial(t, ts.sock)
			sendLine(t, c, tc.line)
			expectRejected(t, c, r, tc.id)
		})
	}
	for _, tc := range []struct{ name, line string }{
		{"a second hello", helloLine("2", api.NewAuthNonce().Hex())},
		{"system.info", `{"jsonrpc":"2.0","id":2,"method":"system.info","params":{}}`},
		{"message.list", `{"jsonrpc":"2.0","id":2,"method":"message.list","params":{"accountId":"a","folderId":"f"}}`},
		{"unknown method", `{"jsonrpc":"2.0","id":2,"method":"nope.nothing"}`},
	} {
		t.Run("after hello: "+tc.name, func(t *testing.T) {
			c, r := dial(t, ts.sock)
			helloExchange(t, c, r, api.NewAuthNonce())
			sendLine(t, c, tc.line)
			expectRejected(t, c, r, "2")
		})
	}
	if n := b.calls.Load(); n != 0 {
		t.Errorf("the backend was called %d times before authentication", n)
	}
	// Once authenticated, calls reach it: the counter works.
	c, r := dialAuthed(t, ts.sock)
	if resp := call(t, c, r, api.MethodSystemInfo, api.SystemInfoParams{}); resp.Error != nil {
		t.Fatal(resp.Error)
	}
	if n := b.calls.Load(); n != 1 {
		t.Errorf("the backend was called %d times, want 1", n)
	}
}

func TestGarbageIsClosedSilently(t *testing.T) {
	ts := startServer(t, nil, nil, nil)
	nonce := api.NewAuthNonce().Hex()
	for _, tc := range []struct{ name, line string }{
		{"not JSON", "hello"},
		{"HTTP", "GET / HTTP/1.1"},
		{"array", "[1,2]"},
		{"batch", "[" + helloLine("1", nonce) + "]"},
		{"null", "null"},
		{"empty object", "{}"},
		{"string", `"system.hello"`},
		{"number", "42"},
		{"true", "true"},
		{"white space", " \t "},
		{"no id", `{"jsonrpc":"2.0","method":"system.hello","params":{"clientNonce":"` + nonce + `"}}`},
		{"null id", `{"jsonrpc":"2.0","id":null,"method":"system.hello","params":{"clientNonce":"` + nonce + `"}}`},
		{"id in another case", `{"jsonrpc":"2.0","ID":1,"method":"system.hello","params":{"clientNonce":"` + nonce + `"}}`},
		{"truncated", `{"jsonrpc":"2.0","id":1,"method":"system.hello"`},
		{"two objects", helloLine("1", nonce) + helloLine("2", nonce)},
		{"trailing garbage", helloLine("1", nonce) + " x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, r := dial(t, ts.sock)
			sendLine(t, c, tc.line)
			expectClosed(t, c, r, 5*time.Second)
		})
		t.Run("after hello: "+tc.name, func(t *testing.T) {
			c, r := dial(t, ts.sock)
			helloExchange(t, c, r, api.NewAuthNonce())
			sendLine(t, c, tc.line)
			expectClosed(t, c, r, 5*time.Second)
		})
	}
}

func TestJSONRPCVersionIsChecked(t *testing.T) {
	ts := startServer(t, nil, nil, nil)
	params := `"method":"system.hello","params":{"clientNonce":"` + api.NewAuthNonce().Hex() + `"}`
	for _, tc := range []struct{ name, line string }{
		{"1.0", `{"jsonrpc":"1.0","id":1,` + params + `}`},
		{"missing", `{"id":1,` + params + `}`},
		{"a number", `{"jsonrpc":2.0,"id":1,` + params + `}`},
		{"padded", `{"jsonrpc":"2.0 ","id":1,` + params + `}`},
		{"null", `{"jsonrpc":null,"id":1,` + params + `}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, r := dial(t, ts.sock)
			sendLine(t, c, tc.line)
			expectRejected(t, c, r, "1")
		})
	}
}

func TestPathologicalHello(t *testing.T) {
	ts := startServer(t, nil, nil, nil)
	key := serverKey(ts.Server)
	valid := api.NewAuthNonce().Hex()
	nonce := func(s string) string { return `{"clientNonce":"` + s + `"}` }
	for _, tc := range []struct{ name, params string }{
		{"no params", ""},
		{"null params", "null"},
		{"empty params", "{}"},
		{"array params", "[]"},
		{"the nonce in an array", `["` + valid + `"]`},
		{"string params", `"x"`},
		{"number params", "1"},
		{"empty nonce", nonce("")},
		{"null nonce", `{"clientNonce":null}`},
		{"number nonce", `{"clientNonce":42}`},
		{"63 digits", nonce(valid[:63])},
		{"65 digits", nonce(valid + "0")},
		{"upper case", nonce(strings.ToUpper(valid))},
		{"0x prefix", nonce("0x" + valid[:62])},
		{"not hex", nonce(strings.Repeat("g", 64))},
		{"a multi-byte rune", nonce(valid[:62] + "é")},
		{"NUL", nonce(valid[:63] + `\u0000`)},
		{"leading space", nonce(" " + valid[:63])},
		{"trailing newline", nonce(valid + `\n`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			line := `{"jsonrpc":"2.0","id":1,"method":"system.hello"`
			if tc.params != "" {
				line += `,"params":` + tc.params
			}
			c, r := dial(t, ts.sock)
			sendLine(t, c, line+"}")
			expectRejected(t, c, r, "1")
		})
	}

	// Unknown members are tolerated, in params and beside them.
	for _, tc := range []struct{ name, line, id string }{
		{"an unknown param", `{"jsonrpc":"2.0","id":1,"method":"system.hello","params":{"clientNonce":"` + valid + `","extra":{"x":[1,2]}}}`, "1"},
		{"an unknown member", `{"jsonrpc":"2.0","id":1,"method":"system.hello","params":{"clientNonce":"` + valid + `"},"extra":true}`, "1"},
		{"a string id", helloLine(`"x"`, valid), `"x"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, r := dial(t, ts.sock)
			sendLine(t, c, tc.line)
			resp := readResponse(t, c, r)
			if resp.Error != nil || string(resp.ID) != tc.id {
				t.Fatalf("hello answer %+v", resp)
			}
			var res api.SystemHelloResult
			if err := json.Unmarshal(resp.Result, &res); err != nil {
				t.Fatal(err)
			}
			cn, _ := api.ParseAuthHex(valid)
			dn, _ := api.ParseAuthHex(res.DaemonNonce)
			sendLine(t, c, authLine("2", api.ClientProof(key, cn, dn).Hex()))
			expectAuthenticated(t, c, r, "2")
		})
	}
}

func TestPathologicalAuthenticate(t *testing.T) {
	ts := startServer(t, nil, nil, nil)
	key := serverKey(ts.Server)
	proof := func(s string) string { return `{"clientProof":"` + s + `"}` }
	for _, tc := range []struct {
		name   string
		params func(cn, dn api.AuthNonce, dp api.AuthProof) string
	}{
		{"no params", func(_, _ api.AuthNonce, _ api.AuthProof) string { return "" }},
		{"null params", func(_, _ api.AuthNonce, _ api.AuthProof) string { return "null" }},
		{"empty params", func(_, _ api.AuthNonce, _ api.AuthProof) string { return "{}" }},
		{"array params", func(cn, dn api.AuthNonce, _ api.AuthProof) string {
			return `["` + api.ClientProof(key, cn, dn).Hex() + `"]`
		}},
		{"empty proof", func(_, _ api.AuthNonce, _ api.AuthProof) string { return proof("") }},
		{"null proof", func(_, _ api.AuthNonce, _ api.AuthProof) string { return `{"clientProof":null}` }},
		{"63 digits", func(cn, dn api.AuthNonce, _ api.AuthProof) string {
			return proof(api.ClientProof(key, cn, dn).Hex()[:63])
		}},
		{"65 digits", func(cn, dn api.AuthNonce, _ api.AuthProof) string {
			return proof(api.ClientProof(key, cn, dn).Hex() + "0")
		}},
		{"the right proof in upper case", func(cn, dn api.AuthNonce, _ api.AuthProof) string {
			p := api.ClientProof(key, cn, dn).Hex()
			if strings.ToUpper(p) == p {
				p = "A" + p[1:] // no hex letter to change: make one
			}
			return proof(strings.ToUpper(p))
		}},
		{"the daemon's proof sent back", func(_, _ api.AuthNonce, dp api.AuthProof) string { return proof(dp.Hex()) }},
		{"the nonces swapped", func(cn, dn api.AuthNonce, _ api.AuthProof) string {
			return proof(api.ClientProof(key, dn, cn).Hex())
		}},
		{"another key", func(cn, dn api.AuthNonce, _ api.AuthProof) string {
			return proof(api.ClientProof(api.NewAuthKey(), cn, dn).Hex())
		}},
		{"the right proof and a space", func(cn, dn api.AuthNonce, _ api.AuthProof) string {
			return proof(api.ClientProof(key, cn, dn).Hex() + " ")
		}},
		{"0x prefix", func(cn, dn api.AuthNonce, _ api.AuthProof) string {
			return proof("0x" + api.ClientProof(key, cn, dn).Hex()[:62])
		}},
		{"zeros", func(_, _ api.AuthNonce, _ api.AuthProof) string { return proof(strings.Repeat("0", 64)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, r := dial(t, ts.sock)
			cn := api.NewAuthNonce()
			dn, dp := helloExchange(t, c, r, cn)
			line := `{"jsonrpc":"2.0","id":2,"method":"system.authenticate"`
			if p := tc.params(cn, dn, dp); p != "" {
				line += `,"params":` + p
			}
			sendLine(t, c, line+"}")
			expectRejected(t, c, r, "2")
		})
	}
}

func TestProofReplayFails(t *testing.T) {
	ts := startServer(t, nil, nil, nil)
	key := serverKey(ts.Server)
	c1, r1 := dial(t, ts.sock)
	rec := handshakeBy(t, c1, r1, key)

	// The same client nonce again: the daemon's nonce is new, the old proof
	// is worthless.
	c2, r2 := dial(t, ts.sock)
	if dn, _ := helloExchange(t, c2, r2, rec.cn); dn == rec.dn {
		t.Fatal("the daemon repeated its nonce")
	}
	sendLine(t, c2, authLine("2", rec.cp.Hex()))
	expectRejected(t, c2, r2, "2")

	c3, r3 := dial(t, ts.sock)
	helloExchange(t, c3, r3, api.NewAuthNonce())
	sendLine(t, c3, authLine("2", rec.cp.Hex()))
	expectRejected(t, c3, r3, "2")
}

func TestHandshakeTimeout(t *testing.T) {
	const timeout = 500 * time.Millisecond
	ts := startServer(t, nil, nil, func(s *Server) { s.preAuthTimeout = timeout })
	t.Run("silent", func(t *testing.T) {
		start := time.Now()
		c, r := dial(t, ts.sock)
		expectClosed(t, c, r, 5*time.Second)
		if d := time.Since(start); d < timeout/2 {
			t.Errorf("closed after %v, before the timeout", d)
		}
	})
	t.Run("silent after hello", func(t *testing.T) {
		c, r := dial(t, ts.sock)
		helloExchange(t, c, r, api.NewAuthNonce())
		expectClosed(t, c, r, 5*time.Second)
	})
	t.Run("half a line", func(t *testing.T) {
		start := time.Now()
		c, r := dial(t, ts.sock)
		if _, err := io.WriteString(c, helloLine("1", api.NewAuthNonce().Hex())[:40]); err != nil {
			t.Fatal(err)
		}
		expectClosed(t, c, r, 5*time.Second)
		if d := time.Since(start); d < timeout/2 {
			t.Errorf("closed after %v, before the timeout", d)
		}
	})
}

func TestDeadlineClearedAfterAuth(t *testing.T) {
	const timeout = 500 * time.Millisecond
	ts := startServer(t, nil, nil, func(s *Server) { s.preAuthTimeout = timeout })
	c, r := dialAuthed(t, ts.sock)
	time.Sleep(2 * timeout)
	if resp := call(t, c, r, api.MethodSystemInfo, api.SystemInfoParams{}); resp.Error != nil {
		t.Fatal(resp.Error)
	}
}

// writeInBackground writes s to c from another goroutine and ignores the
// outcome: the daemon may close the connection before all of it is read.
func writeInBackground(c net.Conn, s string) {
	go func() { _, _ = io.WriteString(c, s) }()
}

func TestHandshakeByteBudget(t *testing.T) {
	// The default timeout of 10 s: closing within 5 s is the budget's doing.
	ts := startServer(t, nil, nil, nil)
	key := serverKey(ts.Server)
	for _, tc := range []struct{ name, data string }{
		{"one 5 KiB line", `{"jsonrpc":"2.0","id":1,"method":"system.hello","params":{"clientNonce":"` + strings.Repeat("a", 5<<10) + `"}}` + "\n"},
		{"5000 blank lines", strings.Repeat("\n", 5000)},
		{"1 MiB without a newline", strings.Repeat("x", 1<<20)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, r := dial(t, ts.sock)
			writeInBackground(c, tc.data)
			expectClosed(t, c, r, 5*time.Second)
		})
	}

	// One line as long as the whole budget, newline included, is a line. No
	// byte is left for system.authenticate, so the daemon closes the
	// connection right after its answer.
	t.Run("one line of the whole budget", func(t *testing.T) {
		c, r := dial(t, ts.sock)
		hello := helloLine("1", api.NewAuthNonce().Hex())
		// JSON allows white space before the last brace.
		line := hello[:len(hello)-1] + strings.Repeat(" ", api.MaxHandshakeBytes-len(hello)-1) + "}\n"
		if len(line) != api.MaxHandshakeBytes {
			t.Fatalf("the line is %d bytes", len(line))
		}
		if _, err := io.WriteString(c, line); err != nil {
			t.Fatal(err)
		}
		resp := readResponse(t, c, r)
		var res api.SystemHelloResult
		if resp.Error != nil || string(resp.ID) != "1" || json.Unmarshal(resp.Result, &res) != nil {
			t.Fatalf("hello answer %+v", resp)
		}
		expectClosed(t, c, r, 5*time.Second)
	})

	// Blank lines count too: exactly api.MaxHandshakeBytes is fine, one
	// byte more is not.
	authLen := len(authLine("2", strings.Repeat("0", 64))) + 1
	for _, over := range []int{0, 1} {
		t.Run(fmt.Sprintf("%d bytes over", over), func(t *testing.T) {
			c, r := dial(t, ts.sock)
			cn := api.NewAuthNonce()
			hello := helloLine("1", cn.Hex()) + "\n"
			pad := api.MaxHandshakeBytes - authLen - len(hello) + over
			if _, err := io.WriteString(c, strings.Repeat("\n", pad)+hello); err != nil {
				t.Fatal(err)
			}
			resp := readResponse(t, c, r)
			var res api.SystemHelloResult
			if resp.Error != nil || json.Unmarshal(resp.Result, &res) != nil {
				t.Fatalf("hello answer %+v", resp)
			}
			dn, _ := api.ParseAuthHex(res.DaemonNonce)
			sendLine(t, c, authLine("2", api.ClientProof(key, cn, dn).Hex()))
			if over > 0 {
				expectClosed(t, c, r, 5*time.Second)
				return
			}
			expectAuthenticated(t, c, r, "2")
			if resp := call(t, c, r, api.MethodSystemInfo, api.SystemInfoParams{}); resp.Error != nil {
				t.Fatal(resp.Error)
			}
		})
	}
}

func TestPendingConnectionsAreCapped(t *testing.T) {
	ts := startServer(t, nil, nil, func(s *Server) { s.maxPending = 3 })
	pendingIs := func(n int) {
		t.Helper()
		waitFor(t, fmt.Sprintf("%d connections in the handshake", n), func() bool { return pendingCount(ts.Server) == n })
	}
	var cs [3]net.Conn
	var rs [3]*bufio.Reader
	for i := range cs {
		cs[i], rs[i] = dial(t, ts.sock)
	}
	pendingIs(3)
	c4, r4 := dial(t, ts.sock)
	expectClosed(t, c4, r4, 5*time.Second)
	expectOpen(t, cs[0], rs[0], 100*time.Millisecond)

	// Authenticating one frees its slot.
	if err := clientHandshake(cs[0], rs[0], ts.sock); err != nil {
		t.Fatal(err)
	}
	pendingIs(2)
	c5, r5 := dial(t, ts.sock)
	if err := clientHandshake(c5, r5, ts.sock); err != nil {
		t.Fatalf("a connection within the limit: %v", err)
	}
	pendingIs(2)

	// So does closing one.
	_ = cs[1].Close()
	pendingIs(1)
	dial(t, ts.sock)
	dial(t, ts.sock)
	pendingIs(3)
	c8, r8 := dial(t, ts.sock)
	expectClosed(t, c8, r8, 5*time.Second)
}

func TestNoNotificationsBeforeAuthentication(t *testing.T) {
	ts := startServer(t, nil, nil, nil)
	key := serverKey(ts.Server)
	c, r := dial(t, ts.sock)
	waitFor(t, "the connection to be admitted", func() bool { return pendingCount(ts.Server) == 1 })
	ts.AccountsChanged(api.AccountsChangedNotification{})

	cn := api.NewAuthNonce()
	dn, _ := helloExchange(t, c, r, cn) // the very next line is the answer
	for range 3 {
		ts.AccountsChanged(api.AccountsChangedNotification{})
	}
	sendLine(t, c, authLine("2", api.ClientProof(key, cn, dn).Hex()))
	expectAuthenticated(t, c, r, "2")
	expectOpen(t, c, r, 200*time.Millisecond) // nothing was kept for later

	ts.AccountsChanged(api.AccountsChangedNotification{})
	line, err := readLine(c, r, 5*time.Second)
	if err != nil || !strings.Contains(string(line), api.NotifyAccountsChanged) {
		t.Fatalf("no notification after authentication: %q, %v", line, err)
	}
}

func TestAuthenticateAnswerPrecedesNotifications(t *testing.T) {
	ts := startServer(t, nil, nil, nil)
	key := serverKey(ts.Server)
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			ts.AccountsChanged(api.AccountsChangedNotification{})
			runtime.Gosched()
		}
	}()
	defer func() {
		close(stop)
		<-done
	}()
	for range 30 {
		c, r := dial(t, ts.sock)
		handshakeBy(t, c, r, key) // each answer the very next line
		line, err := readLine(c, r, 5*time.Second)
		if err != nil || !strings.Contains(string(line), api.NotifyAccountsChanged) {
			t.Fatalf("no notification after authentication: %q, %v", line, err)
		}
		_ = c.Close()
	}
}

func TestProbesAreNotWarnings(t *testing.T) {
	log, logs := captureLog()
	ts := startServer(t, nil, log, func(s *Server) { s.preAuthTimeout = 300 * time.Millisecond })
	for range 20 {
		c, err := net.DialTimeout("unix", ts.sock, 2*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		_ = c.Close()
	}
	// A connection that says nothing until the timeout is no worse.
	c, r := dial(t, ts.sock)
	expectClosed(t, c, r, 5*time.Second)
	waitFor(t, "the connections to go", func() bool { return connCount(ts.Server) == 0 })
	if n := pendingCount(ts.Server); n != 0 {
		t.Errorf("%d connections still count as in the handshake", n)
	}
	out := logs.String()
	if strings.Contains(out, "level=WARN") || strings.Contains(out, "level=ERROR") {
		t.Errorf("probes were logged above Debug:\n%s", out)
	}
	if !strings.Contains(out, "client left before the handshake") {
		t.Errorf("probes are not logged at Debug:\n%s", out)
	}
}

func TestHandshakeFailuresAreRateLimited(t *testing.T) {
	log, logs := captureLog()
	clk := newTestClock()
	ts := startServer(t, nil, log, func(s *Server) { s.failLog.now = clk.now })
	fail := func() {
		t.Helper()
		c, r := dial(t, ts.sock)
		sendLine(t, c, "garbage")
		expectClosed(t, c, r, 5*time.Second)
	}
	for range 5 {
		fail()
	}
	if n := strings.Count(logs.String(), "level=WARN"); n != 1 {
		t.Errorf("%d warnings for 5 failures within %v, want 1:\n%s", n, warnEvery, logs)
	}
	clk.advance(warnEvery)
	fail()
	out := logs.String()
	if n := strings.Count(out, "level=WARN"); n != 2 {
		t.Errorf("%d warnings, want 2:\n%s", n, out)
	}
	if !strings.Contains(out, "suppressed=4") {
		t.Errorf("the second warning does not count the 4 held back:\n%s", out)
	}
	if n := strings.Count(out, `reason="not a JSON-RPC request with an id"`); n != 6 {
		t.Errorf("%d failures logged, want all 6 (5 at Debug):\n%s", n, out)
	}
}

func TestLogsCarryNoSecrets(t *testing.T) {
	log, logs := captureLog()
	ts := startServer(t, nil, log, func(s *Server) { s.preAuthTimeout = time.Second })
	key := serverKey(ts.Server)
	kp := api.KeyPath(ts.sock)
	secrets := []string{string(api.FormatKeyFile(key)[:64])}
	record := func(rec handshakeRecord) {
		secrets = append(secrets, rec.cn.Hex(), rec.dn.Hex(), rec.dp.Hex(), rec.cp.Hex())
	}

	// A handshake by hand and a call.
	c, r := dial(t, ts.sock)
	record(handshakeBy(t, c, r, key))
	if resp := call(t, c, r, api.MethodSystemInfo, api.SystemInfoParams{}); resp.Error != nil {
		t.Fatal(resp.Error)
	}
	// A wrong proof.
	c, r = dial(t, ts.sock)
	cn := api.NewAuthNonce()
	dn, dp := helloExchange(t, c, r, cn)
	wrong := api.ClientProof(api.NewAuthKey(), cn, dn)
	sendLine(t, c, authLine("2", wrong.Hex()))
	expectRejected(t, c, r, "2")
	secrets = append(secrets, cn.Hex(), dn.Hex(), dp.Hex(), wrong.Hex(), api.ClientProof(key, cn, dn).Hex())
	// The key file restored on hello: deleted, then holding another key.
	if err := os.Remove(kp); err != nil {
		t.Fatal(err)
	}
	c, r = dial(t, ts.sock)
	record(handshakeBy(t, c, r, key))
	old := api.NewAuthKey()
	if err := os.WriteFile(kp, api.FormatKeyFile(old), 0o600); err != nil {
		t.Fatal(err)
	}
	secrets = append(secrets, string(api.FormatKeyFile(old)[:64]))
	c, r = dial(t, ts.sock)
	record(handshakeBy(t, c, r, key))
	// The Go client, too many bytes, and a timeout after hello.
	dialAuthed(t, ts.sock)
	c, r = dial(t, ts.sock)
	writeInBackground(c, strings.Repeat("\n", 5000))
	expectClosed(t, c, r, 5*time.Second)
	c, r = dial(t, ts.sock)
	cn = api.NewAuthNonce()
	dn, dp = helloExchange(t, c, r, cn)
	secrets = append(secrets, cn.Hex(), dn.Hex(), dp.Hex())
	expectClosed(t, c, r, 5*time.Second)

	ts.stop()
	waitFor(t, "the connections to go", func() bool { return connCount(ts.Server) == 0 })
	out := logs.String()
	for i, s := range secrets {
		if strings.Contains(out, s) {
			t.Errorf("secret %d (a key, nonce or proof) is in the log", i)
		}
	}
	for _, want := range []string{"listening", "client authenticated", "wrong clientProof", "key file restored", failBudget, failTimeout, "rpc server closed"} {
		if !strings.Contains(out, want) {
			t.Errorf("the log does not mention %q", want)
		}
	}
}

// A Server, a connection or a handshake printed whole does not show the key,
// whatever the verb: they hold it in a closure. (fmt prints the bytes of an
// unexported api.AuthKey field, and with a verb such as %s even those
// behind a pointer.)
func TestPrintingDoesNotShowTheKey(t *testing.T) {
	s := NewServer(&StubBackend{}, quietLog())
	if err := s.Listen(tempSock(t)); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	key := serverKey(s)
	k := [32]byte(key)
	forms := []string{
		string(api.FormatKeyFile(key)[:64]),
		strings.ToUpper(string(api.FormatKeyFile(key)[:64])),
		strings.TrimSuffix(strings.TrimPrefix(fmt.Sprint(k), "["), "]"),
		strings.TrimSuffix(strings.TrimPrefix(fmt.Sprintf("%#v", k), "[32]uint8{"), "}"),
	}
	hs := &handshake{key: newSecretKey(key)}
	c := &conn{key: newSecretKey(key)}
	for _, format := range []string{"%v", "%+v", "%#v", "%x", "%X", "%s", "%q", "%d", "%t"} {
		for _, v := range []any{s, hs, c} {
			printed := fmt.Sprintf(format, v)
			for _, f := range forms {
				if strings.Contains(printed, f) {
					t.Errorf("%s of a %T shows the key", format, v)
				}
			}
		}
	}
}
