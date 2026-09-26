// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/schotek/malachi/backend/pkg/api"
)

func sortStrings(s []string) { sort.Strings(s) }

func mcpCallParams(name string) mcp.CallToolParams {
	return mcp.CallToolParams{Name: name, Arguments: map[string]any{}}
}

func TestDaemonDownThenRecovers(t *testing.T) {
	sock := tempSocket(t)
	cs, _, _ := connectBridge(t, sock, false, false)
	text, isErr := callTool(t, cs, "list_accounts", nil)
	if !isErr || !strings.Contains(text, "malachid is not running; start it with make run-dev") || !strings.Contains(text, sock) {
		t.Fatalf("daemon down: err=%v %q", isErr, text)
	}
	startFakeDaemon(t, newFixture(), sock)
	text, isErr = callTool(t, cs, "list_accounts", nil)
	if isErr || !strings.Contains(text, `"id": "a1"`) {
		t.Fatalf("after daemon start: err=%v %q", isErr, text)
	}
}

// A daemon of another protocol version is refused at its system.hello
// answer: the key file is not read, nothing else is sent, and the
// connection is not kept.
func TestProtocolVersionMismatch(t *testing.T) {
	cases := []struct {
		name   string
		opts   scriptOpts
		daemon int // the version the error names
	}{
		// A daemon of protocol 1 does not know system.hello, and broadcasts
		// notifications to every connection.
		{"protocol 1", scriptOpts{old: true, notifyFirst: 2}, 1},
		{"newer protocol", scriptOpts{version: api.ProtocolVersion + 1}, api.ProtocolVersion + 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sock := tempSocket(t)
			d := startScriptedDaemon(t, sock, tc.opts)
			cs, logs, b := connectBridge(t, sock, false, false)

			text, isErr := callTool(t, cs, "list_accounts", nil)
			want := fmt.Sprintf("malachid speaks protocol version %d but this bridge expects %d; rebuild both with make build",
				tc.daemon, api.ProtocolVersion)
			if !isErr || strings.TrimSuffix(text, "\n") != want {
				t.Fatalf("got err=%v %q, want %q", isErr, text, want)
			}
			if liveConn(b.rpc) != nil {
				t.Error("connection kept after a protocol mismatch")
			}
			d.waitAllEnded(t)
			if got := d.received(); !reflect.DeepEqual(got, [][]string{{api.MethodSystemHello}}) {
				t.Errorf("the daemon received %q, want only system.hello", got)
			}
			mustContain(t, logs.String(), "reason=protocolMismatch", fmt.Sprintf("daemonProtocol=%d", tc.daemon))
		})
	}
}

// Whatever is at the socket path, only a daemon answers there: a regular
// file fails the dial like a missing socket, and is left as it is.
func TestSocketPathIsARegularFile(t *testing.T) {
	sock := tempSocket(t)
	if err := os.WriteFile(sock, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cs, _, b := connectBridge(t, sock, false, false)
	text, isErr := callTool(t, cs, "list_accounts", nil)
	if !isErr || !strings.Contains(text, "malachid is not running; start it with make run-dev") || !strings.Contains(text, sock) {
		t.Fatalf("regular file: err=%v %q", isErr, text)
	}
	if liveConn(b.rpc) != nil {
		t.Error("a connection was kept")
	}
	if got, err := os.ReadFile(sock); err != nil || string(got) != "x" {
		t.Errorf("the file changed: %q, %v", got, err)
	}
}

func TestAPIErrorMapping(t *testing.T) {
	fb := newFixture()
	fb.setFail(api.MethodMessageGet, api.NewError(api.CodeMessageNotFound, "unknown message\r\nm9"))
	fb.setFail(api.MethodSyncStatus, api.ErrNotImplemented)
	h := newHarness(t, fb, false, false)
	text := h.fail(t, "read_message", map[string]any{"accountId": "a1", "messageId": "m9"}, "messageNotFound (1102): unknown message m9")
	if !strings.HasPrefix(text, "messageNotFound (1102): unknown message m9") {
		t.Errorf("error text should start with the code: %q", text)
	}
	h.fail(t, "sync_status", nil, "notImplemented (1000): not implemented; this daemon does not implement that method yet")
}

func TestRPCClientMatchesOutOfOrderResponses(t *testing.T) {
	fb := newFixture()
	fb.delay = map[string]time.Duration{api.MethodAccountList: 200 * time.Millisecond}
	sock := tempSocket(t)
	startFakeDaemon(t, fb, sock)
	c := newRPCClient(sock, nil)
	defer c.close()

	type result struct {
		accounts *api.AccountListResult
		states   *api.SyncStatusResult
		err      error
	}
	ch := make(chan result, 2)
	go func() {
		r, err := callRPC[api.AccountListResult](context.Background(), c, api.MethodAccountList, api.AccountListParams{})
		ch <- result{accounts: r, err: err}
	}()
	time.Sleep(20 * time.Millisecond) // let the slow call go first
	go func() {
		r, err := callRPC[api.SyncStatusResult](context.Background(), c, api.MethodSyncStatus, api.SyncStatusParams{})
		ch <- result{states: r, err: err}
	}()
	for i := 0; i < 2; i++ {
		r := <-ch
		if r.err != nil {
			t.Fatal(r.err)
		}
		if r.accounts != nil && (len(r.accounts.Accounts) != 1 || r.accounts.Accounts[0].ID != "a1") {
			t.Errorf("account.list got %+v", r.accounts)
		}
		if r.states != nil && (len(r.states.Accounts) != 1 || r.states.Accounts[0].Status != api.SyncError) {
			t.Errorf("sync.status got %+v", r.states)
		}
	}
}

func TestRPCClientDisconnectFailsPending(t *testing.T) {
	fb := newFixture()
	fb.delay = map[string]time.Duration{api.MethodSyncStatus: 3 * time.Second}
	sock := tempSocket(t)
	stop := startFakeDaemon(t, fb, sock)
	c := newRPCClient(sock, nil)
	defer c.close()
	oldKey := daemonKeyHex(t, sock)

	errCh := make(chan error, 1)
	go func() {
		_, err := callRPC[api.SyncStatusResult](context.Background(), c, api.MethodSyncStatus, api.SyncStatusParams{})
		errCh <- err
	}()
	time.Sleep(100 * time.Millisecond)
	stop()
	select {
	case err := <-errCh:
		if !errors.Is(err, errDisconnected) {
			t.Fatalf("pending call got %v, want errDisconnected", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending call did not fail when the daemon went away")
	}

	// The daemon's next run has a new key: the next call dials and passes
	// the handshake only because the key file is read for every connection.
	fb2 := newFixture()
	startFakeDaemon(t, fb2, sock)
	if daemonKeyHex(t, sock) == oldKey {
		t.Fatal("the restarted daemon has the same key")
	}
	if _, err := callRPC[api.AccountListResult](context.Background(), c, api.MethodAccountList, api.AccountListParams{}); err != nil {
		t.Fatalf("call after the daemon came back: %v", err)
	}
}

func TestLogsContainNoContent(t *testing.T) {
	fb := newFixture()
	fb.setFail(api.MethodSyncStatus, api.NewError(api.CodeStorageError, "SECRET-ERROR-TEXT"))
	h := newHarness(t, fb, true, true)
	h.ok(t, "list_messages", map[string]any{"accountId": "a1", "folderId": "f_in"})
	h.ok(t, "read_message", map[string]any{"accountId": "a1", "messageId": "m1", "includeLinks": true})
	h.ok(t, "get_attachment", map[string]any{"accountId": "a1", "messageId": "m1", "partId": "2"})
	h.ok(t, "create_draft", map[string]any{"accountId": "a1", "to": []string{"SENTINEL-NAME <sentinel@example.test>"}, "subject": "SENTINEL-SUBJECT", "body": "SENTINEL-BODY"})
	h.ok(t, "create_draft", map[string]any{"accountId": "a1", "mode": "reply", "messageId": "m1", "body": "SENTINEL-REPLY", "attribution": "SENTINEL-ATTR"})
	h.ok(t, "create_draft", map[string]any{"accountId": "a1", "mode": "forward", "messageId": "m1", "omitQuote": true})
	h.fail(t, "sync_status", nil, "SECRET-ERROR-TEXT")

	logs := h.logs.String()
	if !strings.Contains(logs, "method=message.body") || !strings.Contains(logs, "method=draft.create") || !strings.Contains(logs, "method=attachment.remove") {
		t.Fatalf("debug log does not even record calls:\n%s", logs)
	}
	mustContain(t, logs, "connected to malachid", fmt.Sprintf("protocol=%d", api.ProtocolVersion))
	mustNotContain(t, logs,
		"alice@example.org", "Alice", "Quarterly", "Hello Bob", "notes.txt", "hello, notes", "example.org/x",
		"logo.png", "report.pdf", "SENTINEL", "sentinel@example.test", "SECRET-ERROR-TEXT", fxHTML,
		daemonKeyHex(t, h.sock))
}

func TestVersionFlag(t *testing.T) {
	var out, errOut strings.Builder
	if err := run([]string{"-version"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if out.String() != "malachi-mcp dev\n" || errOut.Len() != 0 {
		t.Errorf("stdout %q stderr %q", out.String(), errOut.String())
	}
}
