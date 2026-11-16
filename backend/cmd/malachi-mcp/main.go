// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Command malachi-mcp is a Model Context Protocol server over stdio that
// gives AI agents a gated view of a running malachid: the server of
// package mcpbridge, with the document worker of package extract.
//
// It is a client of the daemon's JSON-RPC socket exactly like the desktop
// UI is: it imports only pkg/api, holds no mail logic, never returns HTML,
// and offers the tools that change or send mail only when started with
// --allow-modify or --allow-send, and those that write the board's triage
// notes only with --allow-triage. See docs/mcp.md.
//
// stdout carries the MCP frames. Everything else (logs, errors) goes to
// stderr; the only other writes to stdout are -version and the reports of
// the setup subcommands (status, install, uninstall; setup.go), which
// register the binary with the Claude apps and exit.
//
// Started with extract.WorkerArg first, the binary is instead the document
// worker of get_attachment: the bridge starts itself again that way for
// each PDF, DOCX or XLSX it reads, and the worker reads the document on
// stdin, writes its text to stdout and exits (extract_runner.go). That
// mode is internal and not in the usage.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/schotek/malachi/backend/cmd/malachi-mcp/internal/extract"
	"github.com/schotek/malachi/backend/cmd/malachi-mcp/mcpbridge"
)

// version is injected at build time: -ldflags "-X main.version=…".
var version = "dev"

func main() {
	// The document worker: before anything else, so that it never parses
	// the bridge's flags, reads its socket or key, or logs.
	if len(os.Args) > 1 && os.Args[1] == extract.WorkerArg {
		os.Exit(extract.ServeWorker(os.Args[2:], os.Stdin, os.Stdout))
	}
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "malachi-mcp:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	// A first argument that is not a flag is a setup subcommand; the
	// server keeps every other command line it accepted so far.
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return runSetup(args[0], args[1:], stdout, stderr)
	}
	// Started with no arguments but a setup request named in the
	// environment: a caller that cannot pass arguments (setup.go).
	if len(args) == 0 {
		if path := os.Getenv(setupRequestEnv); path != "" {
			return runSetupRequest(path)
		}
	}
	cfg, showVersion, err := mcpbridge.ParseArgs(args, os.Getenv, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if showVersion {
		fmt.Fprintln(stdout, "malachi-mcp", version)
		return nil
	}
	if os.Geteuid() == 0 {
		return errors.New("refusing to run as root: malachid is a per-user daemon")
	}

	log := newLogger(stderr)
	log.Info("starting malachi-mcp", append([]any{"version", version}, cfg.LogAttrs()...)...)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Serve returns when the client closes stdin or the context ends.
	// Either way the session is over and there is nothing to retry: the
	// reason is logged (it is a transport-level text, never mail content)
	// and the process exits cleanly, as the client expects of a stdio
	// server.
	err = mcpbridge.Serve(ctx, cfg, mcpbridge.Options{Version: version, Log: log, Extract: extractInWorker},
		&mcp.StdioTransport{})
	switch {
	case err == nil || ctx.Err() != nil:
		log.Info("malachi-mcp stopped")
	case errors.Is(err, io.EOF) || errors.Is(err, mcp.ErrConnectionClosed):
		log.Info("client disconnected")
	default:
		log.Info("session ended", "reason", err.Error())
	}
	return nil
}

// newLogger is the daemon's logger setup, writing to w (stderr).
func newLogger(w io.Writer) *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(os.Getenv("MALACHI_LOG_LEVEL")) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	case "info", "":
	default:
		fmt.Fprintln(w, "malachi-mcp: unknown MALACHI_LOG_LEVEL, using info")
	}
	var h slog.Handler
	if os.Getenv("MALACHI_LOG_FORMAT") == "json" {
		h = slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})
	} else {
		h = slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})
	}
	return slog.New(h)
}
