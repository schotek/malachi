package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

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

func startTestServer(t *testing.T) (string, context.CancelFunc) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "rpc.sock")
	s := NewServer(&StubBackend{Version: "test"}, slog.New(slog.DiscardHandler))
	if err := s.Listen(sock); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = s.Serve(ctx) }()
	return sock, cancel
}

func call(t *testing.T, c net.Conn, r *bufio.Reader, method string, params any) api.Response {
	t.Helper()
	raw, _ := json.Marshal(params)
	req := api.Request{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: method, Params: raw}
	if err := json.NewEncoder(c).Encode(req); err != nil {
		t.Fatal(err)
	}
	line, err := r.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var resp api.Response
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("bad response %q: %v", line, err)
	}
	return resp
}

func TestRoundTrip(t *testing.T) {
	sock, cancel := startTestServer(t)
	defer cancel()

	c, err := net.DialTimeout("unix", sock, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	r := bufio.NewReader(c)

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

	resp = call(t, c, r, "nope.nothing", nil)
	if resp.Error == nil || resp.Error.Code != api.CodeMethodNotFound {
		t.Errorf("expected methodNotFound, got %+v", resp)
	}
}

func TestStaleSocketIsReplaced(t *testing.T) {
	sock, cancel := startTestServer(t)
	cancel()
	time.Sleep(50 * time.Millisecond)

	// Simulate a crash that left the file behind (nobody listening).
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewServer(&StubBackend{}, slog.New(slog.DiscardHandler))
	if err := s.Listen(sock); err != nil {
		t.Fatalf("expected stale socket to be replaced: %v", err)
	}
	s.Close()
}
