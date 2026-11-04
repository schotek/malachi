// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Command malachi-devclaude is a developer tool: a stand-in for the
// Claude Code CLI that the desktop clients run for their assistant (the
// In App target of ui/internal/assistant: the panel, the board's triage
// run, a suggested reply, the compose window's rewrite and writing, the
// search in the user's own words). It speaks the subset of Claude Code's
// command line and stream-json protocol the clients use, answers with
// prepared texts written for the sample world of malachi-devmail
// (answers/en.json, answers/cs.json), and uses the real malachi-mcp
// bridge over MCP where the real Claude Code would: it reads the mail it
// is asked about, creates the drafts (create_draft), and triages the
// board (list_triage_queue, annotate_case, add_commitment). No model is
// ever called and nothing leaves the computer; the screenshots of the
// assistant for the website (docs/screenshots.md) are taken over it, so
// that they are the same on every platform. It is deliberately not
// installed: neither the Makefile nor the packages build it. Build it by
// hand with "go build ./cmd/malachi-devclaude" in backend/.
//
//	malachi-devclaude --version                 the version line
//	malachi-devclaude auth status [--json]      signed in, always
//	malachi-devclaude auth login                a sign-in that succeeds at once
//	malachi-devclaude -p … (the clients' command line, ui/internal/assistant Args)
//
// The conversation form reads one user turn per stdin line (stream-json
// input), writes the events of each turn on stdout (system/init,
// stream_event deltas, assistant text and tool_use messages, user
// tool_result messages, result with usage) and ends at the end of stdin.
// --mcp-config names the bridge, which it starts and talks to over stdio
// like Claude Code; --system-prompt tells it which request this is and
// in which language to answer; --json-schema makes the result carry
// structured_output (the search).
//
// The answers are chosen by what the prompt asks for and, where the
// request names mail, by the subject of the mail read through the bridge;
// a request the answer set does not know gets a plain fallback, never an
// error, so that a scene can be tried freely. The texts are data of this
// program and belong to no sample set.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

// version is what --version prints, the shape of Claude Code's own line.
const version = "2.1.285 (Claude Code)"

const usageText = `usage: malachi-devclaude --version
       malachi-devclaude auth status [--json]
       malachi-devclaude auth login
       malachi-devclaude -p [claude flags] --system-prompt TEXT [--mcp-config JSON] [--json-schema JSON]
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run is the program: the status it exits with.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usageText)
		return 2
	}
	switch args[0] {
	case "--version", "-v":
		fmt.Fprintln(stdout, version)
		return 0
	case "auth":
		return auth(args[1:], stdout, stderr)
	case "-h", "--help":
		fmt.Fprint(stdout, usageText)
		return 0
	}
	opts, err := parseOptions(args)
	if err != nil {
		fmt.Fprintf(stderr, "malachi-devclaude: %v\n", err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := converse(ctx, opts, stdin, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "malachi-devclaude: %v\n", err)
		return 1
	}
	return 0
}

// auth is `auth status [--json]` (signed in, always) and `auth login` (a
// sign-in that ends well at once; nothing is opened).
func auth(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usageText)
		return 2
	}
	switch args[0] {
	case "status":
		for _, a := range args[1:] {
			if a == "--json" {
				fmt.Fprintln(stdout, `{"loggedIn": true, "authMethod": "claude.ai", "apiProvider": "firstParty"}`)
				return 0
			}
		}
		fmt.Fprintln(stdout, "Logged in (claude.ai)")
		return 0
	case "login":
		fmt.Fprintln(stdout, "Login successful")
		return 0
	}
	fmt.Fprint(stderr, usageText)
	return 2
}

// options is what the clients' command line decides (ui/internal/assistant
// Args; the other flags are accepted and ignored).
type options struct {
	systemPrompt string
	mcpConfig    string // the JSON of --mcp-config, "" without a bridge
	allowedTools []string
	model        string
	jsonSchema   string
	print        bool // -p
}

// valueFlags are the flags that take the next argument.
var valueFlags = map[string]bool{
	"--output-format": true, "--input-format": true, "--tools": true, "--disallowedTools": true,
	"--setting-sources": true, "--mcp-config": true, "--allowedTools": true, "--permission-mode": true,
	"--model": true, "--system-prompt": true, "--json-schema": true, "--append-system-prompt": true,
	"--max-turns": true, "--session-id": true, "--resume": true,
}

func parseOptions(args []string) (options, error) {
	var o options
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-p" || a == "--print" {
			o.print = true
			continue
		}
		if !strings.HasPrefix(a, "-") {
			return o, fmt.Errorf("unexpected argument %q", a)
		}
		name, value, inline := strings.Cut(a, "=")
		if !valueFlags[name] {
			continue
		}
		if !inline {
			if i+1 >= len(args) {
				return o, fmt.Errorf("%s needs a value", name)
			}
			i++
			value = args[i]
		}
		switch name {
		case "--system-prompt":
			o.systemPrompt = value
		case "--append-system-prompt":
			o.systemPrompt += "\n" + value
		case "--mcp-config":
			o.mcpConfig = value
		case "--allowedTools":
			if value != "" {
				o.allowedTools = strings.Split(value, ",")
			}
		case "--model":
			o.model = value
		case "--json-schema":
			o.jsonSchema = value
		}
	}
	if !o.print {
		return o, errors.New("only the print mode (-p) is supported")
	}
	return o, nil
}

// userTurn reads one stdin line: the text of a stream-json user message
// (ui/internal/assistant UserMessage). An empty line is skipped.
func userTurn(line []byte) (string, bool) {
	var in struct {
		Type    string `json:"type"`
		Message struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &in); err != nil || in.Type != "user" {
		return "", false
	}
	var parts []string
	for _, c := range in.Message.Content {
		if c.Type == "text" {
			parts = append(parts, c.Text)
		}
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, "\n"), true
}

// converse runs the conversation: the bridge of --mcp-config connected
// once, a turn per stdin line, the end at EOF.
func converse(ctx context.Context, o options, stdin io.Reader, stdout, stderr io.Writer) error {
	out := newEmitter(stdout, modelID(o.model))
	req := classify(o.systemPrompt)
	var br *bridge
	if o.mcpConfig != "" {
		var err error
		br, err = connectBridge(ctx, o.mcpConfig, stderr)
		if err != nil {
			// As Claude Code reports a server that failed: the clients
			// then say the tools are not available.
			fmt.Fprintf(stderr, "malachi-devclaude: bridge: %v\n", err)
		} else {
			defer br.close()
		}
	}
	s := &session{opts: o, req: req, out: out, bridge: br, stderr: stderr}
	r := bufio.NewReaderSize(stdin, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			if text, ok := userTurn(line); ok {
				s.turn(ctx, text)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if ctx.Err() != nil {
			return nil
		}
	}
}

// modelID is the model name the init event reports for a --model alias.
func modelID(alias string) string {
	switch alias {
	case "haiku":
		return "claude-haiku-4-5"
	case "opus":
		return "claude-opus-4-1"
	}
	return "claude-sonnet-4-5"
}
