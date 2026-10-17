// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// fakeDaemon serves one connection: the handshake of docs/api.md §1.4,
// then answers the first call with res or rpcErr, preceded by a notification.
func fakeDaemon(t *testing.T, version int, res string, rpcErr *api.Error) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "mrpc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s")
	key := api.NewAuthKey()
	if err := os.WriteFile(api.KeyPath(sock), api.FormatKeyFile(key), 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		send := func(v any) {
			b, _ := json.Marshal(v)
			_, _ = c.Write(append(b, '\n'))
		}
		read := func() (api.Request, bool) {
			var req api.Request
			line, err := r.ReadBytes('\n')
			return req, err == nil && json.Unmarshal(line, &req) == nil
		}
		req, ok := read()
		if !ok {
			return
		}
		var hp api.SystemHelloParams
		_ = json.Unmarshal(req.Params, &hp)
		cn, _ := api.ParseAuthHex(hp.ClientNonce)
		dn := api.NewAuthNonce()
		proof := api.DaemonProof(key, api.AuthNonce(cn), dn)
		hr, _ := json.Marshal(api.SystemHelloResult{ProtocolVersion: version, DaemonNonce: dn.Hex(), DaemonProof: proof.Hex()})
		send(api.Response{JSONRPC: api.JSONRPCVersion, ID: req.ID, Result: hr})
		if version != api.ProtocolVersion {
			return
		}
		req, ok = read()
		if !ok {
			return
		}
		ar, _ := json.Marshal(api.SystemAuthenticateResult{})
		send(api.Response{JSONRPC: api.JSONRPCVersion, ID: req.ID, Result: ar})
		req, ok = read()
		if !ok {
			return
		}
		send(api.Notification{JSONRPC: api.JSONRPCVersion, Method: api.NotifyAccountsChanged, Params: json.RawMessage(`{}`)})
		if rpcErr != nil {
			send(api.Response{JSONRPC: api.JSONRPCVersion, ID: req.ID, Error: rpcErr})
			return
		}
		send(api.Response{JSONRPC: api.JSONRPCVersion, ID: req.ID, Result: json.RawMessage(res)})
	}()
	return sock
}

func TestRun(t *testing.T) {
	sock := fakeDaemon(t, api.ProtocolVersion, `{"a":[1,2]}`, nil)
	var out, errb bytes.Buffer
	code := run([]string{"--socket", sock, "--listen", "200ms", "calendar.list", `{"x":1}`}, nil, true, &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	want := "{\n  \"a\": [\n    1,\n    2\n  ]\n}\nnotify "
	if !strings.HasPrefix(out.String(), want) || !strings.Contains(out.String(), api.NotifyAccountsChanged) {
		t.Fatalf("stdout %q", out.String())
	}
}

func TestRunRPCError(t *testing.T) {
	sock := fakeDaemon(t, api.ProtocolVersion, "", api.NewError(api.CodeMethodNotFound, "nope"))
	var out, errb bytes.Buffer
	code := run([]string{"--socket", sock, "x.y"}, strings.NewReader(""), true, &out, &errb)
	if code != 1 || out.Len() != 0 || !strings.Contains(errb.String(), "nope") {
		t.Fatalf("exit %d out %q err %q", code, out.String(), errb.String())
	}
}

func TestRunProtocolMismatch(t *testing.T) {
	sock := fakeDaemon(t, api.ProtocolVersion+1, "{}", nil)
	var out, errb bytes.Buffer
	code := run([]string{"--socket", sock, "x.y"}, nil, true, &out, &errb)
	if code != 2 || out.Len() != 0 || !strings.Contains(errb.String(), "protocol version") {
		t.Fatalf("exit %d out %q err %q", code, out.String(), errb.String())
	}
}

func TestRunUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run(nil, nil, true, &out, &errb); code != 2 {
		t.Fatalf("exit %d", code)
	}
	if code := run([]string{"--socket", "/nonexistent/s", "a.b"}, nil, true, &out, &errb); code != 2 {
		t.Fatalf("exit %d", code)
	}
}
