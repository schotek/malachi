// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Command malachi-rpc is a developer tool: a generic JSON-RPC client for
// the socket of a running malachid, to exercise daemon methods before any
// UI exists. It performs the handshake of docs/api.md §1.4
// (api.ClientHandshake), sends one request and prints the result.
//
// It is deliberately not installed: neither the Makefile nor
// scripts/build.sh builds or packages it. Build it by hand with
// "go build ./cmd/malachi-rpc" in backend/.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

const usageText = `usage: malachi-rpc [--socket PATH] [--timeout 45s] [--listen DURATION] METHOD [PARAMS-JSON]

PARAMS-JSON is read from the argument, or from stdin when the argument is
"-" or absent and stdin is not a terminal; {} when there is none. The result
is printed as indented JSON on stdout (exit 0); an RPC error object goes to
stderr (exit 1); usage and connection errors exit 2. --listen also prints
every notification received during the call and that long after it, one JSON
object per line, prefixed by "notify ".

Examples:
  malachi-rpc calendar.list
  malachi-rpc account.discoverCalDAV '{"url":"cloud.example.org"}'
  malachi-rpc event.list '{"from":"2026-10-12T00:00:00Z","to":"2026-10-19T00:00:00Z","timeZone":"Europe/Prague"}'

Params with credentials (credentials.password) are sent as given. Your shell
keeps arguments in its history: pass them on stdin instead, e.g.
  malachi-rpc account.add - < params.json
`

// maxLine caps one line from the daemon.
const maxLine = 32 << 20

func main() {
	tty := false
	if fi, err := os.Stdin.Stat(); err == nil {
		tty = fi.Mode()&os.ModeCharDevice != 0
	}
	os.Exit(run(os.Args[1:], os.Stdin, tty, os.Stdout, os.Stderr))
}

func defaultSocketPath() string {
	if p := os.Getenv("MALACHI_SOCKET"); p != "" {
		return p
	}
	if base := api.SocketBase(os.Getenv("XDG_RUNTIME_DIR"), os.Getenv("FLATPAK_ID")); base != "" {
		return filepath.Join(base, api.SocketRelPath)
	}
	cache := os.Getenv("XDG_CACHE_HOME")
	if cache == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		cache = filepath.Join(home, ".cache")
	}
	return filepath.Join(cache, "malachi", "run", "rpc.sock")
}

// run is main with its environment injected; it returns the exit code.
func run(args []string, stdin io.Reader, stdinIsTTY bool, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("malachi-rpc", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	socket := fs.String("socket", defaultSocketPath(), "")
	timeout := fs.Duration("timeout", 45*time.Second, "")
	listen := fs.Duration("listen", 0, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = io.WriteString(stdout, usageText)
			return 0
		}
		fmt.Fprintf(stderr, "malachi-rpc: %v\n\n%s", err, usageText)
		return 2
	}
	rest := fs.Args()
	if len(rest) < 1 || len(rest) > 2 || *timeout <= 0 || *listen < 0 {
		_, _ = io.WriteString(stderr, usageText)
		return 2
	}
	method := rest[0]

	var params json.RawMessage
	switch {
	case len(rest) == 2 && rest[1] != "-":
		params = json.RawMessage(rest[1])
	case (len(rest) == 2 || !stdinIsTTY) && stdin != nil:
		b, err := io.ReadAll(io.LimitReader(stdin, maxLine))
		if err != nil {
			fmt.Fprintf(stderr, "malachi-rpc: read params: %v\n", err)
			return 2
		}
		params = json.RawMessage(strings.TrimSpace(string(b)))
	}
	if len(params) == 0 {
		params = json.RawMessage("{}")
	}
	if !json.Valid(params) {
		fmt.Fprintln(stderr, "malachi-rpc: PARAMS-JSON is not valid JSON")
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", *socket)
	if err != nil {
		fmt.Fprintf(stderr, "malachi-rpc: cannot connect to %s: %v\n", *socket, err)
		return 2
	}
	defer conn.Close()
	r := bufio.NewReaderSize(conn, 64<<10)
	if err := api.ClientHandshake(ctx, conn, r, api.KeyPath(*socket)); err != nil {
		fmt.Fprintf(stderr, "malachi-rpc: handshake: %v\n", err)
		return 2
	}

	req, err := json.Marshal(api.Request{JSONRPC: api.JSONRPCVersion, ID: json.RawMessage("1"), Method: method, Params: params})
	if err != nil {
		fmt.Fprintf(stderr, "malachi-rpc: %v\n", err)
		return 2
	}
	_ = conn.SetDeadline(time.Now().Add(*timeout))
	if _, err := conn.Write(append(req, '\n')); err != nil {
		fmt.Fprintf(stderr, "malachi-rpc: send: %v\n", err)
		return 2
	}

	var notes [][]byte
	var resp *api.Envelope
	for resp == nil {
		line, err := readLine(r)
		if err != nil {
			fmt.Fprintf(stderr, "malachi-rpc: waiting for the answer: %v\n", err)
			return 2
		}
		var env api.Envelope
		if json.Unmarshal(line, &env) != nil {
			continue
		}
		if env.IsNotification() {
			notes = append(notes, line)
			continue
		}
		resp = &env
	}

	code := 0
	if resp.Error != nil {
		code = 1
		writeIndented(stderr, resp.Error)
	} else {
		raw := resp.Result
		if len(raw) == 0 {
			raw = json.RawMessage("null")
		}
		writeIndented(stdout, raw)
	}

	if *listen > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(*listen))
		for {
			line, err := readLine(r)
			if err != nil {
				break
			}
			var env api.Envelope
			if json.Unmarshal(line, &env) == nil && env.IsNotification() {
				notes = append(notes, line)
			}
		}
		for _, n := range notes {
			if c, err := compact(n); err == nil {
				fmt.Fprintf(stdout, "notify %s\n", c)
			}
		}
	}
	return code
}

func readLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		part, err := r.ReadSlice('\n')
		line = append(line, part...)
		if len(line) > maxLine {
			return nil, errors.New("line too long")
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			return nil, err
		}
		return line, nil
	}
}

func compact(b []byte) ([]byte, error) {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

func writeIndented(w io.Writer, v any) {
	var b []byte
	var err error
	if raw, ok := v.(json.RawMessage); ok {
		var tmp any
		if err = json.Unmarshal(raw, &tmp); err == nil {
			b, err = json.MarshalIndent(tmp, "", "  ")
		}
	} else {
		b, err = json.MarshalIndent(v, "", "  ")
	}
	if err != nil {
		fmt.Fprintf(w, "%v\n", err)
		return
	}
	fmt.Fprintf(w, "%s\n", b)
}
