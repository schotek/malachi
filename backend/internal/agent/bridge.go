// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package agent runs the daemon's own assistant over an ai account
// (docs/api.md §4.18; docs/security.md §10.4; the design is
// docs/plans/ai-api.md D2 and D5): a Session keeps one conversation with
// a model (internal/llm), executes the model's tool calls through the
// MCP bridge malachi-mcp, which the daemon starts as a child process and
// which connects back to the daemon like any client, and reports what
// happens as api.AssistantEvents. A Manager keeps the open sessions per
// RPC connection. The package knows nothing of mail: every tool result
// is the bridge's text, passed on verbatim, and nothing of a conversation
// is logged.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/schotek/malachi/backend/internal/llm"
)

// KillGrace is how long a bridge gets after its stdin closes before it is
// terminated (mcp.CommandTransport.TerminateDuration).
const KillGrace = 2 * time.Second

// Tools is what a session calls: the bridge, or a fake in tests.
type Tools interface {
	// Tools lists the tools the model may be offered.
	Tools() []llm.Tool
	// Call runs one tool and returns its text (the bridge's result
	// verbatim) and whether the tool refused (an error result); err is a
	// failure of the bridge itself.
	Call(ctx context.Context, name string, input json.RawMessage) (text string, isError bool, err error)
	// Close ends the bridge.
	Close() error
}

// BridgeConfig says which bridge to start and how.
type BridgeConfig struct {
	// Path is the absolute path of the bridge executable.
	Path string
	// Socket is the daemon's own socket, passed as --socket.
	Socket string
	// Args are the bridge's other arguments (--in-app, the tier's).
	Args []string
	// Name and Version identify the daemon to the bridge
	// (initialize.clientInfo; the bridge records Name as the source of
	// board notes).
	Name, Version string
	// Stderr takes the bridge's log; nil discards it.
	Stderr io.Writer
}

// Bridge is a running MCP server (the bridge) seen as Tools.
type Bridge struct {
	cs    *mcp.ClientSession
	tools []llm.Tool
}

// StartBridge starts the bridge as a child process with a minimal
// environment, connects to it over stdio, initialises the session and
// lists its tools.
func StartBridge(ctx context.Context, cfg BridgeConfig) (*Bridge, error) {
	switch {
	case cfg.Path == "":
		return nil, errors.New("agent: no bridge path")
	case cfg.Socket == "":
		return nil, errors.New("agent: no socket")
	}
	cmd := exec.Command(cfg.Path, append([]string{"--socket", cfg.Socket}, cfg.Args...)...)
	cmd.Env = childEnv(os.Environ())
	cmd.Stderr = cfg.Stderr
	if cmd.Stderr == nil {
		cmd.Stderr = io.Discard
	}
	return Connect(ctx, &mcp.CommandTransport{Command: cmd, TerminateDuration: KillGrace}, cfg.Name, cfg.Version)
}

// Connect connects to a bridge over t (a started command, or an
// in-memory transport in tests), initialises the session and lists the
// tools.
func Connect(ctx context.Context, t mcp.Transport, name, version string) (*Bridge, error) {
	cs, err := mcp.NewClient(&mcp.Implementation{Name: name, Version: version}, nil).Connect(ctx, t, nil)
	if err != nil {
		return nil, fmt.Errorf("agent: connect to the bridge: %w", err)
	}
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		_ = cs.Close()
		return nil, fmt.Errorf("agent: list the bridge's tools: %w", err)
	}
	b := &Bridge{cs: cs}
	for _, t := range res.Tools {
		if t == nil || t.Name == "" {
			continue
		}
		schema, err := json.Marshal(t.InputSchema)
		if err != nil || len(schema) == 0 || string(schema) == "null" {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		b.tools = append(b.tools, llm.Tool{Name: t.Name, Description: t.Description, InputSchema: schema})
	}
	return b, nil
}

// Tools lists the bridge's tools.
func (b *Bridge) Tools() []llm.Tool { return append([]llm.Tool(nil), b.tools...) }

// Call runs a tool. Text content is joined with newlines; a content of
// another kind (an image) is named, never decoded.
func (b *Bridge) Call(ctx context.Context, name string, input json.RawMessage) (string, bool, error) {
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	res, err := b.cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: input})
	if err != nil {
		return "", false, fmt.Errorf("agent: call %s: %w", name, err)
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		switch c := c.(type) {
		case *mcp.TextContent:
			sb.WriteString(c.Text)
		default:
			sb.WriteString("[content of another kind was left out]")
		}
	}
	return sb.String(), res.IsError, nil
}

// Close ends the bridge: its stdin closes and, after KillGrace, the
// process is terminated.
func (b *Bridge) Close() error { return b.cs.Close() }

// childEnv is the bridge's environment: the variables a process needs to
// run (the path, the home, the locale, the temporary directory, what
// Windows needs) and nothing of the daemon's own (every MALACHI_* is
// dropped: the bridge gets its socket as an argument).
func childEnv(parent []string) []string {
	keep := map[string]bool{
		"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "SHELL": true,
		"LANG": true, "LC_ALL": true, "LC_CTYPE": true, "TMPDIR": true, "TEMP": true, "TMP": true,
		"XDG_RUNTIME_DIR": true, "XDG_CACHE_HOME": true, "XDG_CONFIG_HOME": true, "XDG_DATA_HOME": true,
		"SYSTEMROOT": true, "SystemRoot": true, "windir": true, "USERPROFILE": true,
		"LOCALAPPDATA": true, "APPDATA": true, "PATHEXT": true, "COMSPEC": true,
	}
	var out []string
	for _, kv := range parent {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || !keep[name] {
			continue
		}
		out = append(out, kv)
	}
	return out
}
