// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/schotek/malachi/backend/internal/rpc"
	"github.com/schotek/malachi/backend/pkg/api"
)

// tempSocket returns a short socket path: sun_path is 108 bytes (104 on
// macOS) and t.TempDir() can be longer than that. /tmp gives the shortest
// path; where no directory can be made there (no /tmp at all), the
// system's temporary directory is used instead.
func tempSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "malachi-mcp-")
	if err != nil {
		dir, err = os.MkdirTemp("", "malachi-mcp-")
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "rpc.sock")
}

// daemonKeyHex returns the key the daemon on sock has written beside it
// (api.KeyPath), as the 64 hex digits of the file: the tests look for it
// where it must never appear.
func daemonKeyHex(t *testing.T, sock string) string {
	t.Helper()
	b, err := os.ReadFile(api.KeyPath(sock))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.ParseKeyFile(b); err != nil {
		t.Fatalf("the daemon's key file: %v", err)
	}
	return strings.TrimSuffix(string(b), "\n")
}

// liveConn returns the connection c has published for calls, nil for none.
func liveConn(c *rpcClient) net.Conn {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn
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

// startFakeDaemon serves fb over the real rpc server on sock.
func startFakeDaemon(t *testing.T, fb *fakeBackend, sock string) (stop func()) {
	t.Helper()
	s := rpc.NewServer(fb, slog.New(slog.DiscardHandler))
	if err := s.Listen(sock); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = s.Serve(ctx); close(done) }()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			s.Close()
			<-done
		})
	}
	t.Cleanup(stop)
	return stop
}

// syncWriter makes a bytes.Buffer safe for the bridge's goroutines.
type syncWriter struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *syncWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

// connectBridge builds a bridge for sock and connects an in-process MCP
// client to it. The daemon need not be running: the bridge dials lazily.
func connectBridge(t *testing.T, sock string, allowModify, allowSend bool) (*mcp.ClientSession, *syncWriter, *bridge) {
	t.Helper()
	logs := &syncWriter{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	b := newBridge(config{socket: sock, allowModify: allowModify, allowSend: allowSend}, logger)
	srv := b.mcpServer()
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cs.Close()
		_ = ss.Close()
		b.rpc.close()
	})
	return cs, logs, b
}

type harness struct {
	fb   *fakeBackend
	sock string
	cs   *mcp.ClientSession
	logs *syncWriter
	b    *bridge
}

// newHarness starts a fake daemon on a fixture and connects a bridge to it.
func newHarness(t *testing.T, fb *fakeBackend, allowModify, allowSend bool) *harness {
	t.Helper()
	sock := tempSocket(t)
	startFakeDaemon(t, fb, sock)
	cs, logs, b := connectBridge(t, sock, allowModify, allowSend)
	return &harness{fb: fb, sock: sock, cs: cs, logs: logs, b: b}
}

// callRaw calls a tool and returns the whole result.
func callRaw(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

// callTool calls a tool and returns its text blocks joined and IsError.
func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res := callRaw(t, cs, name, args)
	return textOf(res), res.IsError
}

func textOf(res *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

func (h *harness) call(t *testing.T, name string, args map[string]any) (string, bool) {
	t.Helper()
	return callTool(t, h.cs, name, args)
}

// ok calls a tool and fails the test on a tool error.
func (h *harness) ok(t *testing.T, name string, args map[string]any) string {
	t.Helper()
	text, isErr := h.call(t, name, args)
	if isErr {
		t.Fatalf("%s returned an error: %s", name, text)
	}
	return text
}

// fail calls a tool and fails the test unless it returns a tool error
// whose text contains want.
func (h *harness) fail(t *testing.T, name string, args map[string]any, want string) string {
	t.Helper()
	text, isErr := h.call(t, name, args)
	if !isErr {
		t.Fatalf("%s succeeded, expected an error containing %q: %s", name, want, text)
	}
	if !strings.Contains(text, want) {
		t.Fatalf("%s error %q does not contain %q", name, text, want)
	}
	return text
}

func listTools(t *testing.T, cs *mcp.ClientSession) map[string]*mcp.Tool {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]*mcp.Tool, len(res.Tools))
	for _, tool := range res.Tools {
		out[tool.Name] = tool
	}
	return out
}

func toolNames(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()
	var names []string
	for n := range listTools(t, cs) {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func mustContain(t *testing.T, s string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(s, w) {
			t.Errorf("output does not contain %q:\n%s", w, s)
		}
	}
}

func mustNotContain(t *testing.T, s string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if strings.Contains(s, w) {
			t.Errorf("output must not contain %q:\n%s", w, s)
		}
	}
}
