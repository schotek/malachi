// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// scriptOpts is how a scripted daemon departs from a correct daemon of
// this protocol version; the zero value is a correct one.
type scriptOpts struct {
	old             bool          // a daemon of protocol 1: system.hello is methodNotFound
	notifyFirst     int           // notifications before the system.hello answer, as protocol 1 broadcasts them
	version         int           // protocolVersion of the system.hello answer; 0 is api.ProtocolVersion
	wrongKey        bool          // proves another key than the one in its key file
	reject          bool          // answers system.authenticate with unauthenticated (1005)
	noKeyFile       bool          // writes no key file
	malformed       bool          // sends daemonNonce in upper case
	silent          bool          // never answers
	helloDelay      time.Duration // waits this long before it answers system.hello
	notifyAfterAuth bool          // sends a notification in the same write as the system.authenticate answer
}

// scriptedDaemon speaks the handshake of docs/api.md §1.4 on a plain unix
// socket as its options script it, and records the methods it receives on
// each connection. Like the real daemon it answers any request that is not
// the next step of the handshake with unauthenticated (1005) and closes the
// connection; once a connection is authenticated, every call gets an empty
// object as its result.
type scriptedDaemon struct {
	opts  scriptOpts
	key   api.AuthKey // written to the key file, and checked in clientProof
	prove api.AuthKey // the key of its daemonProof

	mu      sync.Mutex
	methods [][]string // per connection, in the order accepted
	ended   int        // connections that are over
	secrets []string   // the keys, nonces and proofs, as hex: never to appear in text or logs
	conns   []net.Conn
}

// startScriptedDaemon listens on sock and writes the key file beside it
// (unless opts.noKeyFile) until the test ends.
func startScriptedDaemon(t *testing.T, sock string, opts scriptOpts) *scriptedDaemon {
	t.Helper()
	d := &scriptedDaemon{opts: opts, key: api.NewAuthKey()}
	d.prove = d.key
	if opts.wrongKey {
		d.prove = api.NewAuthKey()
	}
	d.keep(hex.EncodeToString(d.key[:]), hex.EncodeToString(d.prove[:]))

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	if !opts.noKeyFile {
		if err := os.WriteFile(api.KeyPath(sock), api.FormatKeyFile(d.key), 0o600); err != nil {
			_ = ln.Close()
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	accepting := make(chan struct{})
	go func() {
		defer close(accepting)
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			d.mu.Lock()
			i := len(d.methods)
			d.methods = append(d.methods, nil)
			d.conns = append(d.conns, nc)
			d.mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				d.serve(i, nc)
			}()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		<-accepting
		d.mu.Lock()
		for _, nc := range d.conns {
			_ = nc.Close()
		}
		d.mu.Unlock()
		wg.Wait()
		_ = os.Remove(api.KeyPath(sock))
	})
	return d
}

// serve handles connection i until the peer closes it or the handshake
// fails.
func (d *scriptedDaemon) serve(i int, nc net.Conn) {
	defer func() {
		_ = nc.Close()
		d.mu.Lock()
		d.ended++
		d.mu.Unlock()
	}()
	r := bufio.NewReader(nc)
	var (
		cn, dn api.AuthNonce
		hello  bool // system.hello is answered
		authed bool
	)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return
		}
		var req api.Request
		if json.Unmarshal(line, &req) != nil {
			return
		}
		d.mu.Lock()
		d.methods[i] = append(d.methods[i], req.Method)
		d.mu.Unlock()

		switch {
		case authed:
			d.write(nc, answerWith(req.ID, struct{}{}))
		case d.opts.silent:
		case req.Method == api.MethodSystemHello && !hello:
			time.Sleep(d.opts.helloDelay)
			if d.opts.old {
				for range d.opts.notifyFirst {
					d.write(nc, api.Notification{JSONRPC: api.JSONRPCVersion, Method: api.NotifySyncState, Params: json.RawMessage(`{}`)})
				}
				d.write(nc, api.Response{JSONRPC: api.JSONRPCVersion, ID: req.ID,
					Error: api.NewError(api.CodeMethodNotFound, "unknown method %q", req.Method)})
				continue
			}
			var p api.SystemHelloParams
			nonce, ok := [32]byte{}, false
			if json.Unmarshal(req.Params, &p) == nil {
				nonce, ok = api.ParseAuthHex(p.ClientNonce)
			}
			if !ok {
				d.refuse(nc, req.ID)
				return
			}
			cn, dn = api.AuthNonce(nonce), api.NewAuthNonce()
			proof := api.DaemonProof(d.prove, cn, dn)
			d.keep(cn.Hex(), dn.Hex(), strings.ToUpper(dn.Hex()), proof.Hex())
			res := api.SystemHelloResult{ProtocolVersion: api.ProtocolVersion, DaemonNonce: dn.Hex(), DaemonProof: proof.Hex()}
			if d.opts.version != 0 {
				res.ProtocolVersion = d.opts.version
			}
			if d.opts.malformed {
				res.DaemonNonce = strings.ToUpper(res.DaemonNonce)
			}
			d.write(nc, answerWith(req.ID, res))
			hello = true
		case req.Method == api.MethodSystemAuthenticate && hello:
			var p api.SystemAuthenticateParams
			_ = json.Unmarshal(req.Params, &p)
			d.keep(p.ClientProof)
			proof, ok := api.ParseAuthHex(p.ClientProof)
			if d.opts.reject || !ok || !api.ClientProof(d.key, cn, dn).Equal(api.AuthProof(proof)) {
				d.refuse(nc, req.ID)
				return
			}
			answer := d.line(answerWith(req.ID, api.SystemAuthenticateResult{}))
			if d.opts.notifyAfterAuth {
				answer = append(answer, d.line(api.Notification{JSONRPC: api.JSONRPCVersion, Method: api.NotifyAccountsChanged, Params: json.RawMessage(`{}`)})...)
			}
			_, _ = nc.Write(answer)
			authed = true
		default:
			d.refuse(nc, req.ID)
			return
		}
	}
}

// answerWith is the answer to id with result v.
func answerWith(id json.RawMessage, v any) api.Response {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return api.Response{JSONRPC: api.JSONRPCVersion, ID: id, Result: raw}
}

// refuse answers id with unauthenticated (1005), as the real daemon does
// before it closes a connection that broke the handshake.
func (d *scriptedDaemon) refuse(nc net.Conn, id json.RawMessage) {
	d.write(nc, api.Response{JSONRPC: api.JSONRPCVersion, ID: id,
		Error: api.NewError(api.CodeUnauthenticated, "the connection is not authenticated")})
}

func (d *scriptedDaemon) line(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return append(b, '\n')
}

func (d *scriptedDaemon) write(nc net.Conn, v any) { _, _ = nc.Write(d.line(v)) }

func (d *scriptedDaemon) keep(hexes ...string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, h := range hexes {
		if h != "" {
			d.secrets = append(d.secrets, h)
		}
	}
}

// received returns the methods received so far, per connection.
func (d *scriptedDaemon) received() [][]string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([][]string, len(d.methods))
	for i, m := range d.methods {
		out[i] = append([]string(nil), m...)
	}
	return out
}

// waitAllEnded waits until every connection accepted so far is over: the
// client has closed each one, so nothing more can arrive on it.
func (d *scriptedDaemon) waitAllEnded(t *testing.T) {
	t.Helper()
	waitFor(t, "the client to close its connections", func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.ended == len(d.methods)
	})
}

// mustNotLeak fails if any key, nonce or proof of d's handshakes is in s.
func (d *scriptedDaemon) mustNotLeak(t *testing.T, what, s string) {
	t.Helper()
	d.mu.Lock()
	secrets := append([]string(nil), d.secrets...)
	d.mu.Unlock()
	for _, h := range secrets {
		if strings.Contains(s, h) {
			t.Errorf("%s contains a key, nonce or proof of the handshake:\n%s", what, s)
		}
	}
}

func TestHandshakeRefusals(t *testing.T) {
	cases := []struct {
		name   string
		opts   scriptOpts
		reason api.HandshakeReason
		// The tool error text: want exactly, or else wantPrefix, the key
		// file's path and wantSuffix (the system's text for the missing
		// file comes between them).
		want, wantPrefix, wantSuffix string
		methods                      []string // what the daemon received
	}{
		{
			name: "daemon proves another key", opts: scriptOpts{wrongKey: true}, reason: api.HandshakeDaemonUnproven,
			want:    "refusing to use the socket: the process on the socket did not prove it holds malachid's connection key; is MALACHI_SOCKET pointing at another daemon?",
			methods: []string{api.MethodSystemHello},
		},
		{
			name: "daemon rejects the proof", opts: scriptOpts{reject: true}, reason: api.HandshakeRejected,
			want:    "malachid rejected the handshake (unauthenticated); retry the call, or restart malachid",
			methods: []string{api.MethodSystemHello, api.MethodSystemAuthenticate},
		},
		{
			name: "no key file", opts: scriptOpts{noKeyFile: true}, reason: api.HandshakeKeyUnavailable,
			wantPrefix: "cannot use malachid's connection key: rpc key unavailable: ",
			wantSuffix: "; the running daemon writes it next to its socket: start or restart malachid as this user",
			methods:    []string{api.MethodSystemHello},
		},
		{
			name: "malformed hello answer", opts: scriptOpts{malformed: true}, reason: api.HandshakeMalformed,
			want:    "malformed handshake answer from malachid: daemonNonce is not 64 lowercase hex digits; retry the call, or restart malachid",
			methods: []string{api.MethodSystemHello},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sock := tempSocket(t)
			d := startScriptedDaemon(t, sock, tc.opts)
			cs, logs, b := connectBridge(t, sock, false, false)

			text, isErr := callTool(t, cs, "list_accounts", nil)
			text = strings.TrimSuffix(text, "\n")
			switch {
			case !isErr:
				t.Fatalf("the call succeeded: %q", text)
			case tc.want != "" && text != tc.want:
				t.Errorf("tool error %q, want %q", text, tc.want)
			case tc.want == "" && (!strings.HasPrefix(text, tc.wantPrefix) || !strings.HasSuffix(text, tc.wantSuffix) ||
				!strings.Contains(text, api.KeyPath(sock))):
				t.Errorf("tool error %q, want %q, the key file's path, then %q", text, tc.wantPrefix, tc.wantSuffix)
			}
			if liveConn(b.rpc) != nil {
				t.Error("the refused connection was kept")
			}
			d.waitAllEnded(t)
			if got := d.received(); !reflect.DeepEqual(got, [][]string{tc.methods}) {
				t.Errorf("the daemon received %q, want one connection with %q", got, tc.methods)
			}
			log := logs.String()
			mustContain(t, log, "level=WARN", "reason="+tc.reason.String(), "err=handshake."+tc.reason.String())
			d.mustNotLeak(t, "the tool error", text)
			d.mustNotLeak(t, "the log", log)
		})
	}
}

// A daemon that never answers holds a call only as long as the handshake
// may last: the call's deadline when it is nearer than api.HandshakeTimeout.
func TestHandshakeTimeout(t *testing.T) {
	sock := tempSocket(t)
	d := startScriptedDaemon(t, sock, scriptOpts{silent: true})
	logs := &syncWriter{}
	c := newRPCClient(sock, slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer c.close()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := callRPC[api.AccountListResult](ctx, c, api.MethodAccountList, api.AccountListParams{})
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("the call returned after %v", took)
	}
	var hs *api.HandshakeError
	if !errors.As(err, &hs) || hs.Reason != api.HandshakeTimedOut {
		t.Fatalf("got %v, want a handshake timeout", err)
	}
	want := fmt.Sprintf("malachid did not complete the connection handshake within %s; retry the call", api.HandshakeTimeout)
	if got := errorText(err); got != want {
		t.Errorf("tool error %q, want %q", got, want)
	}
	if liveConn(c) != nil {
		t.Error("the connection was kept")
	}
	d.waitAllEnded(t)
	if got := d.received(); !reflect.DeepEqual(got, [][]string{{api.MethodSystemHello}}) {
		t.Errorf("the daemon received %q, want only system.hello", got)
	}
	mustContain(t, logs.String(), "reason=timedOut")
}

// Calls that come while the first connection is in its handshake wait for
// it and then share it: nothing but the handshake goes out on a connection
// before its last answer. (The bridge used to publish a connection before it
// had checked the daemon, and the go-sdk runs tool calls concurrently; a
// call that found the connection published took it at once.)
func TestConnectionUsedOnlyAfterHandshake(t *testing.T) {
	sock := tempSocket(t)
	d := startScriptedDaemon(t, sock, scriptOpts{helloDelay: 300 * time.Millisecond})
	c := newRPCClient(sock, nil)
	defer c.close()

	const calls = 4
	errs := make(chan error, calls)
	call := func() {
		_, err := callRPC[api.AccountListResult](context.Background(), c, api.MethodAccountList, api.AccountListParams{})
		errs <- err
	}
	go call()
	// The other calls come while the first one waits for the hello answer.
	waitFor(t, "system.hello", func() bool {
		got := d.received()
		return len(got) == 1 && len(got[0]) == 1
	})
	for range calls - 1 {
		go call()
	}
	for i := range calls {
		select {
		case err := <-errs:
			if err != nil {
				t.Errorf("call %d: %v", i, err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("the calls did not return")
		}
	}
	want := []string{api.MethodSystemHello, api.MethodSystemAuthenticate}
	for range calls {
		want = append(want, api.MethodAccountList)
	}
	if got := d.received(); !reflect.DeepEqual(got, [][]string{want}) {
		t.Errorf("the daemon received %q, want one connection with %q", got, want)
	}
}

// What the daemon sends right after the system.authenticate answer, in the
// same write, reaches the read loop: it reads through the handshake's
// reader, where those bytes already are.
func TestReadLoopKeepsWhatFollowsTheHandshake(t *testing.T) {
	sock := tempSocket(t)
	startScriptedDaemon(t, sock, scriptOpts{notifyAfterAuth: true})
	logs := &syncWriter{}
	c := newRPCClient(sock, slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer c.close()

	if _, err := callRPC[api.AccountListResult](context.Background(), c, api.MethodAccountList, api.AccountListParams{}); err != nil {
		t.Fatal(err)
	}
	// The notification came before the call's answer on the connection.
	mustContain(t, logs.String(), `msg="notification ignored" method=`+api.NotifyAccountsChanged)
}
