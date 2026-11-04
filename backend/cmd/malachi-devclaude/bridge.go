// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

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
)

// bridge is the malachi-mcp server of --mcp-config, started and talked to
// over stdio as Claude Code does (one process for the conversation). The
// client's name is what Claude Code reports, so that the bridge records
// the triage notes under the same source the clients expect.
type bridge struct {
	session *mcp.ClientSession
	tools   []string
	timeout time.Duration
}

// mcpConfig is the shape of --mcp-config (ui/internal/assistant mcpConfig).
type mcpConfig struct {
	MCPServers map[string]struct {
		Type    string   `json:"type"`
		Command string   `json:"command"`
		Args    []string `json:"args"`
	} `json:"mcpServers"`
}

// connectBridge starts the server named "malachi" (or the only server) of
// the configuration and lists its tools.
func connectBridge(ctx context.Context, config string, stderr io.Writer) (*bridge, error) {
	var cfg mcpConfig
	if err := json.Unmarshal([]byte(config), &cfg); err != nil {
		return nil, fmt.Errorf("--mcp-config: %w", err)
	}
	srv, ok := cfg.MCPServers["malachi"]
	if !ok {
		for _, s := range cfg.MCPServers {
			srv, ok = s, true
			break
		}
	}
	if !ok || srv.Command == "" {
		return nil, errors.New("--mcp-config names no server")
	}
	cmd := exec.CommandContext(ctx, srv.Command, srv.Args...)
	// The bridge's log stays out of this process's stderr, whose first
	// line the clients show as the reason of a failure; it is kept with
	// MALACHI_DEVCLAUDE_DEBUG set.
	if os.Getenv("MALACHI_DEVCLAUDE_DEBUG") != "" {
		cmd.Stderr = stderr
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "2.1.285"}, nil)
	ctxConnect, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	session, err := client.Connect(ctxConnect, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	b := &bridge{session: session, timeout: 2 * time.Minute}
	res, err := session.ListTools(ctxConnect, nil)
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("list tools: %w", err)
	}
	for _, t := range res.Tools {
		b.tools = append(b.tools, "mcp__malachi__"+t.Name)
	}
	return b, nil
}

func (b *bridge) close() {
	if b != nil && b.session != nil {
		_ = b.session.Close()
	}
}

// has says whether the bridge offers the tool (without the prefix).
func (b *bridge) has(tool string) bool {
	if b == nil {
		return false
	}
	for _, t := range b.tools {
		if t == "mcp__malachi__"+tool {
			return true
		}
	}
	return false
}

// call runs a tool and returns its text (the text blocks joined) and
// whether the tool reported an error; a transport failure is an error of
// its own.
func (b *bridge) call(ctx context.Context, tool string, args map[string]any) (string, bool, error) {
	if b == nil || b.session == nil {
		return "", true, errors.New("no bridge")
	}
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	res, err := b.session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		return "", true, err
	}
	var parts []string
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, t.Text)
		}
	}
	return strings.Join(parts, "\n"), res.IsError, nil
}
