// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"os"
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

func TestProtocolVersionMismatch(t *testing.T) {
	fb := newFixture()
	fb.protocolVersion = 99
	h := newHarness(t, fb, false, false)
	h.fail(t, "list_accounts", nil, "malachid speaks protocol version 99 but this bridge expects 1")
	h.b.rpc.mu.Lock()
	conn := h.b.rpc.conn
	h.b.rpc.mu.Unlock()
	if conn != nil {
		t.Error("connection kept after a protocol mismatch")
	}
}

func TestSocketPermissionCheck(t *testing.T) {
	sock := tempSocket(t)
	if err := os.WriteFile(sock, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cs, _, _ := connectBridge(t, sock, false, false)
	text, isErr := callTool(t, cs, "list_accounts", nil)
	if !isErr || !strings.Contains(text, "not a unix socket") {
		t.Fatalf("regular file: err=%v %q", isErr, text)
	}

	sock2 := tempSocket(t)
	startFakeDaemon(t, newFixture(), sock2)
	if err := os.Chmod(sock2, 0o660); err != nil {
		t.Fatal(err)
	}
	cs2, _, _ := connectBridge(t, sock2, false, false)
	text, isErr = callTool(t, cs2, "list_accounts", nil)
	if !isErr || !strings.Contains(text, "mode 0660 lets other users reach it") {
		t.Fatalf("group-writable socket: err=%v %q", isErr, text)
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

	fb2 := newFixture()
	startFakeDaemon(t, fb2, sock)
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
	mustNotContain(t, logs,
		"alice@example.org", "Alice", "Quarterly", "Hello Bob", "notes.txt", "hello, notes", "example.org/x",
		"logo.png", "report.pdf", "SENTINEL", "sentinel@example.test", "SECRET-ERROR-TEXT", fxHTML)
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
