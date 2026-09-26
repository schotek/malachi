// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package rpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The test vectors of docs/api.md §1.4: key = bytes 0x00..0x1f, clientNonce
// = 0x20..0x3f, daemonNonce = 0x40..0x5f.
var vecKey, vecCN, vecDN = func() (api.AuthKey, api.AuthNonce, api.AuthNonce) {
	var k api.AuthKey
	var cn, dn api.AuthNonce
	for i := range k {
		k[i], cn[i], dn[i] = byte(i), byte(0x20+i), byte(0x40+i)
	}
	return k, cn, dn
}()

const (
	vecDaemonProof = "04abc851d52b40dc687920756f15f42f44da2635331732bf02be0a0b01de2a1f"
	vecClientProof = "024f86a00c241237f4556a27e83f8e41b13053bbcfd2980e029c300a5a2e84b2"
)

// vectorHandshake is a handshake with the vectors' key whose daemon nonce
// is the vectors' one.
func vectorHandshake() *handshake {
	return &handshake{key: newSecretKey(vecKey), newNonce: func() api.AuthNonce { return vecDN }}
}

func TestHandshakeStepVectors(t *testing.T) {
	h := vectorHandshake()
	st := h.step([]byte(helloLine("1", vecCN.Hex())))
	if !st.hello || st.authed || st.fail != "" || st.reply == nil || st.reply.Error != nil {
		t.Fatalf("hello: %+v", st)
	}
	wire, err := json.Marshal(st.reply)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":2,"daemonNonce":"` + vecDN.Hex() +
		`","daemonProof":"` + vecDaemonProof + `"}}`
	if string(wire) != want {
		t.Errorf("hello answer\n%s\nwant\n%s", wire, want)
	}

	st = h.step([]byte(authLine("2", vecClientProof)))
	if !st.authed || st.hello || st.fail != "" || st.reply == nil {
		t.Fatalf("authenticate: %+v", st)
	}
	if wire, _ := json.Marshal(st.reply); string(wire) != `{"jsonrpc":"2.0","id":2,"result":{}}` {
		t.Errorf("authenticate answer %s", wire)
	}

	// The handshake is over: nothing more is answered.
	st = h.step([]byte(helloLine("3", vecCN.Hex())))
	if st.reply != nil || st.fail == "" || st.hello || st.authed {
		t.Errorf("a line after the handshake: %+v", st)
	}
}

func TestHandshakeStepStates(t *testing.T) {
	hello := helloLine("1", vecCN.Hex())
	for _, tc := range []struct {
		name  string
		lines []string
		// what the last line gets: "" no answer, "result" or "1005"
		want   string
		authed bool
	}{
		{"hello, authenticate", []string{hello, authLine("2", vecClientProof)}, "result", true},
		{"hello", []string{hello}, "result", false},
		{"authenticate first", []string{authLine("1", vecClientProof)}, "1005", false},
		{"another method first", []string{`{"jsonrpc":"2.0","id":1,"method":"system.info"}`}, "1005", false},
		{"a second hello", []string{hello, helloLine("2", vecCN.Hex())}, "1005", false},
		{"another method after hello", []string{hello, `{"jsonrpc":"2.0","id":2,"method":"system.info"}`}, "1005", false},
		{"the daemon's proof", []string{hello, authLine("2", vecDaemonProof)}, "1005", false},
		{"upper case proof", []string{hello, authLine("2", strings.ToUpper(vecClientProof))}, "1005", false},
		{"not JSON", []string{"x"}, "", false},
		{"not JSON after hello", []string{hello, "x"}, "", false},
		{"no id", []string{`{"jsonrpc":"2.0","method":"system.hello","params":{"clientNonce":"` + vecCN.Hex() + `"}}`}, "", false},
		{"a line after a failure", []string{"x", hello}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := vectorHandshake()
			var st hsStep
			for _, line := range tc.lines {
				st = h.step([]byte(line))
			}
			got := ""
			switch {
			case st.reply == nil:
			case st.reply.Error != nil && st.reply.Error.Code == api.CodeUnauthenticated:
				got = "1005"
			case st.reply.Error == nil && st.reply.Result != nil:
				got = "result"
			default:
				t.Fatalf("answer %+v", st.reply)
			}
			if got != tc.want || st.authed != tc.authed {
				t.Errorf("answer %q authed %v, want %q %v", got, st.authed, tc.want, tc.authed)
			}
			if (got == "result") == (st.fail != "") {
				t.Errorf("a result must come without a failure and the reverse: %+v", st)
			}
		})
	}
}

func TestHandshakeLimit(t *testing.T) {
	l := newHandshakeLimit(strings.NewReader(strings.Repeat("a", api.MaxHandshakeBytes+10)))
	b, err := io.ReadAll(l)
	if len(b) != api.MaxHandshakeBytes || !errors.Is(err, errHandshakeBudget) {
		t.Fatalf("read %d bytes, %v; want %d and the budget error", len(b), err, api.MaxHandshakeBytes)
	}
	if !l.read {
		t.Error("the bytes read are not recorded")
	}
	l.lift()
	if rest, err := io.ReadAll(l); len(rest) != 10 || err != nil {
		t.Errorf("after lift: %d bytes, %v", len(rest), err)
	}

	probe := newHandshakeLimit(strings.NewReader(""))
	if _, err := io.ReadAll(probe); err != nil || probe.read {
		t.Errorf("an empty connection: %v, read %v", err, probe.read)
	}

	// Before authentication a fragment without its newline is not a line.
	for _, tc := range []struct {
		data    string
		atEOF   bool
		lifted  bool
		advance int
		token   string
	}{
		{"abc", true, false, 0, ""},
		{"abc\ndef", true, false, 4, "abc"},
		{"abc\r\n", false, false, 5, "abc"},
		{"abc", false, false, 0, ""},
		{"abc", true, true, 3, "abc"},
	} {
		l := newHandshakeLimit(nil)
		l.lifted = tc.lifted
		advance, token, err := l.split([]byte(tc.data), tc.atEOF)
		if advance != tc.advance || string(token) != tc.token || err != nil {
			t.Errorf("split(%q, %v) lifted %v = %d %q %v", tc.data, tc.atEOF, tc.lifted, advance, token, err)
		}
	}
}

func TestLogLimiter(t *testing.T) {
	clk := newTestClock()
	l := &logLimiter{now: clk.now}
	if ok, n := l.allow(); !ok || n != 0 {
		t.Fatalf("first event: %v %d", ok, n)
	}
	for range 3 {
		if ok, _ := l.allow(); ok {
			t.Fatal("an event within the interval was let through")
		}
	}
	clk.advance(warnEvery - time.Nanosecond)
	if ok, _ := l.allow(); ok {
		t.Fatal("an event within the interval was let through")
	}
	clk.advance(time.Nanosecond)
	if ok, n := l.allow(); !ok || n != 4 {
		t.Fatalf("after the interval: %v, %d held back; want 4", ok, n)
	}
	if ok, _ := l.allow(); ok {
		t.Fatal("the interval did not start again")
	}

	log, logs := captureLog()
	lim := &logLimiter{now: clk.now}
	clk.advance(warnEvery)
	warnLimited(log, lim, "trouble", "reason", "r")
	warnLimited(log, lim, "trouble", "reason", "r")
	clk.advance(warnEvery)
	warnLimited(log, lim, "trouble", "reason", "r")
	out := logs.String()
	if strings.Count(out, "level=WARN") != 2 || strings.Count(out, "level=DEBUG") != 1 || !strings.Contains(out, "suppressed=1") {
		t.Errorf("log:\n%s", out)
	}
}

func TestKeyReplaceable(t *testing.T) {
	dir := shortDir(t)
	path := filepath.Join(dir, "rpc.sock.key")
	key := api.FormatKeyFile(api.NewAuthKey())
	for _, tc := range []struct {
		name    string
		content []byte // nil: nothing at the path
		ok      bool
	}{
		{"missing", nil, true},
		{"empty", []byte{}, true},
		{"a key", key, true},
		{"torn", key[:20], true},
		{"newlines", []byte("\n\n"), true},
		{"a byte too many", append(bytes.Clone(key), '0'), false},
		{"upper case", bytes.ToUpper(key), false},
		{"CRLF", append(bytes.Clone(key[:64]), '\r', '\n'), false},
		{"text", []byte("hello\n"), false},
		{"large", bytes.Repeat([]byte("a"), 1<<20), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_ = os.Remove(path)
			if tc.content != nil {
				if err := os.WriteFile(path, tc.content, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := keyReplaceable(path); (err == nil) != tc.ok {
				t.Errorf("keyReplaceable: %v, want ok %v", err, tc.ok)
			}
		})
	}
	t.Run("directory", func(t *testing.T) {
		_ = os.Remove(path)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(path)
		if keyReplaceable(path) == nil {
			t.Error("a directory is replaceable")
		}
	})
	t.Run("symlink", func(t *testing.T) {
		_ = os.Remove(path)
		target := filepath.Join(dir, "target")
		if err := os.WriteFile(target, key, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Skipf("cannot make a symbolic link here: %v", err)
		}
		defer os.Remove(path)
		if keyReplaceable(path) == nil {
			t.Error("a symbolic link is replaceable")
		}
	})
}

func TestWriteKeyFile(t *testing.T) {
	dir := shortDir(t)
	path := filepath.Join(dir, "rpc.sock.key")
	key := api.NewAuthKey()
	if err := writeKeyFile(path, key); err != nil {
		t.Fatal(err)
	}
	if k, err := api.ReadKeyFile(path); err != nil || !k.Equal(key) {
		t.Fatalf("read back: %v", err)
	}
	checkNoTempFiles(t, dir)

	// A client reading the old file does not make the write fail (Windows
	// cannot rename over an open file; the write waits for it).
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		defer close(released)
		time.Sleep(100 * time.Millisecond)
		_ = f.Close()
	}()
	key2 := api.NewAuthKey()
	err = writeKeyFile(path, key2)
	<-released
	if err != nil {
		t.Fatalf("write while a reader holds the file: %v", err)
	}
	if k, err := api.ReadKeyFile(path); err != nil || !k.Equal(key2) {
		t.Fatalf("read back: %v", err)
	}
	checkNoTempFiles(t, dir)
}

func TestWriteKeyFileCleansUpOnFailure(t *testing.T) {
	saved := fileRetryWaits
	fileRetryWaits = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { fileRetryWaits = saved })

	dir := shortDir(t)
	path := filepath.Join(dir, "rpc.sock.key")
	// A directory that is not empty: no rename can replace it.
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "inside"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeKeyFile(path, api.NewAuthKey()); err == nil {
		t.Fatal("the write replaced a directory")
	}
	checkNoTempFiles(t, dir)
	if b, err := os.ReadFile(filepath.Join(path, "inside")); err != nil || string(b) != "x" {
		t.Errorf("the directory changed (%v)", err)
	}
}

// checkStep checks what holds for every step of a handshake, whatever the
// line: a result comes without a failure, a failure without a result and
// with at most error 1005, and an answer is one line of JSON with the id of
// the line it answers.
func checkStep(t *testing.T, line []byte, st hsStep) {
	t.Helper()
	if st.fail == "" {
		if st.reply == nil || st.reply.Error != nil || st.reply.Result == nil || st.hello == st.authed {
			t.Fatalf("a step without a failure: %+v", st)
		}
	} else if st.hello || st.authed || (st.reply != nil && (st.reply.Result != nil || st.reply.Error == nil ||
		st.reply.Error.Code != api.CodeUnauthenticated)) {
		t.Fatalf("a failed step: %+v", st)
	}
	if st.reply == nil {
		return
	}
	var msg map[string]json.RawMessage
	if err := json.Unmarshal(line, &msg); err != nil || !bytes.Equal(msg["id"], st.reply.ID) {
		t.Fatalf("the answer's id %s is not the line's (%v)", st.reply.ID, err)
	}
	wire, err := json.Marshal(st.reply)
	if err != nil || !json.Valid(wire) || bytes.ContainsAny(wire, "\r\n") {
		t.Fatalf("the answer is not one line of JSON: %q, %v", wire, err)
	}
	if st.reply.JSONRPC != api.JSONRPCVersion {
		t.Fatalf("jsonrpc %q", st.reply.JSONRPC)
	}
}

// clientNonceOf and clientProofOf return the argument of a system.hello or
// a system.authenticate line, decoded as the handshake decodes it.
func clientNonceOf(t *testing.T, line []byte) api.AuthNonce {
	t.Helper()
	var p api.SystemHelloParams
	return api.AuthNonce(authHexParam(t, line, &p, func() string { return p.ClientNonce }))
}

func clientProofOf(t *testing.T, line []byte) api.AuthProof {
	t.Helper()
	var p api.SystemAuthenticateParams
	return api.AuthProof(authHexParam(t, line, &p, func() string { return p.ClientProof }))
}

func authHexParam(t *testing.T, line []byte, params any, field func() string) [32]byte {
	t.Helper()
	var msg map[string]json.RawMessage
	if json.Unmarshal(line, &msg) != nil || json.Unmarshal(msg["params"], params) != nil {
		t.Fatal("the line does not decode")
	}
	v, ok := api.ParseAuthHex(field())
	if !ok {
		t.Fatal("the argument is not 64 lowercase hex digits")
	}
	return v
}

func FuzzHandshakeStep(f *testing.F) {
	hello := helloLine("1", vecCN.Hex())
	auth := authLine("2", vecClientProof)
	for _, seed := range [][2]string{
		{hello, auth},
		{hello, hello},
		{auth, hello},
		{hello, authLine("2", vecDaemonProof)},
		{hello, authLine("2", strings.ToUpper(vecClientProof))},
		{`{"jsonrpc":"2.0","id":1,"method":"system.info"}`, auth},
		{"null", "{}"},
		{"[]", `{"id":null}`},
		{hello + " ", auth + " "},
		{`{"jsonrpc":"2.0","id":"x","method":"system.hello","params":{"clientNonce":"` + vecCN.Hex() + `","x":1}}`,
			`{"jsonrpc":"2.0","id":{"a":[1]},"method":"system.authenticate","params":{"clientProof":"` + vecClientProof + `"}}`},
		{`{"jsonrpc":"2.0","id":1,"method":"system.hello","params":{"clientNonce":"` + vecDN.Hex() + `"}}`, auth},
	} {
		f.Add([]byte(seed[0]), []byte(seed[1]))
	}
	f.Fuzz(func(t *testing.T, a, b []byte) {
		h := vectorHandshake()
		first := h.step(a)
		checkStep(t, a, first)
		if first.authed {
			t.Fatal("authenticated by the first line")
		}
		second := h.step(b)
		checkStep(t, b, second)
		if first.fail != "" && second.reply != nil {
			t.Fatal("an answer after a failed handshake")
		}
		if !second.authed {
			return
		}
		if !first.hello {
			t.Fatal("authenticated without an answered system.hello")
		}
		// Only with the one right proof for this connection's nonces.
		if !api.ClientProof(vecKey, clientNonceOf(t, a), vecDN).Equal(clientProofOf(t, b)) {
			t.Fatal("authenticated with a wrong proof")
		}
	})
}
