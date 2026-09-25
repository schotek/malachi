// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package mcpsetup registers the malachi-mcp bridge with the Claude apps on
// this computer by running the bridge's own status / install / uninstall
// subcommands and reading their JSON report.
//
// The Claude configuration files are the only state: nothing is stored in
// GSettings or in the daemon, so a switch bound to this package shows what
// the bridge reports and nothing else. The package never touches those
// files itself; it speaks only the bridge's command line (docs/mcp.md).
package mcpsetup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// Name is the bridge executable, installed next to malachid and the UI.
const Name = "malachi-mcp"

// Timeout is how long a caller should give one bridge invocation: the
// bridge only reads and writes a few small files, but the first run after
// an install may page the binary in from a slow disk.
const Timeout = 15 * time.Second

// reasonLimit bounds the part of the bridge's stderr kept in an error.
const reasonLimit = 200

// noClientPrefix starts the bridge's one-line reason when neither Claude
// app is installed (part of the malachi-mcp install contract).
const noClientPrefix = "no Claude app found"

// ErrNoClient reports that the bridge found neither Claude Desktop nor
// Claude Code, so there is nothing to register with.
var ErrNoClient = errors.New("no Claude app found")

// Client is one Claude app the bridge knows how to register with.
type Client struct {
	// ID is the stable identifier ("claude-desktop", "claude-code"); Name
	// is the app's display name as the bridge reports it.
	ID   string `json:"id"`
	Name string `json:"name"`
	// Path is the configuration file the bridge reads and writes for this
	// app. Other, when set, is a malachi entry the bridge left alone there
	// because it points at a different executable.
	Path  string `json:"path"`
	Other string `json:"other,omitempty"`
	// Present says whether the app exists on this computer; Registered
	// whether this bridge is listed in its configuration.
	Present    bool `json:"present"`
	Registered bool `json:"registered"`
}

// Status is the bridge's report: the command the clients are (or would be)
// registered with, and every client it knows about, present or not.
type Status struct {
	Command string   `json:"command"`
	Clients []Client `json:"clients"`
}

// Registered says whether the bridge is registered with at least one
// Claude app, which is what a single on/off switch shows.
func (s Status) Registered() bool {
	for _, c := range s.Clients {
		if c.Registered {
			return true
		}
	}
	return false
}

// ExitError is a bridge run that ended with a non-zero status. Error is the
// first line the bridge wrote to stderr, its one-line reason meant for
// people, so callers can show it as it is.
type ExitError struct {
	Subcommand string
	Code       int
	Reason     string
}

func (e *ExitError) Error() string {
	if e.Reason == "" {
		return fmt.Sprintf("%s %s exited with status %d", Name, e.Subcommand, e.Code)
	}
	return e.Reason
}

// Unwrap exposes ErrNoClient to errors.Is when the bridge's reason says no
// Claude app was found; other reasons wrap nothing.
func (e *ExitError) Unwrap() error {
	if strings.HasPrefix(e.Reason, noClientPrefix) {
		return ErrNoClient
	}
	return nil
}

// executable is os.Executable, replaced by the tests.
var executable = os.Executable

// Locate finds the bridge: malachi-mcp beside the running executable
// (/app/bin in Flatpak, build/ in a source tree, the install prefix
// otherwise), otherwise on $PATH. It mirrors daemon.Locate for malachid.
func Locate() (string, error) {
	if exe, err := executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), Name)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	p, err := exec.LookPath(Name)
	if err != nil {
		return "", fmt.Errorf("%s not found beside %s or on PATH: %w", Name, os.Args[0], err)
	}
	return p, nil
}

// Query reports the current registration without changing anything.
func Query(ctx context.Context, bridge string) (Status, error) {
	return run(ctx, bridge, "status")
}

// Install registers the bridge with every Claude app present and reports
// the result. errors.Is(err, ErrNoClient) means none was found.
func Install(ctx context.Context, bridge string) (Status, error) {
	return run(ctx, bridge, "install")
}

// Uninstall removes the bridge's registration from every Claude app and
// reports the result.
func Uninstall(ctx context.Context, bridge string) (Status, error) {
	return run(ctx, bridge, "uninstall")
}

// waitDelay bounds the wait for the bridge's pipes after it was killed on
// a cancelled context, in case a child of its kept them open. A variable
// so the tests can shorten it.
var waitDelay = 2 * time.Second

// run executes one subcommand with --json and parses the report on stdout.
func run(ctx context.Context, bridge, sub string) (Status, error) {
	cmd := exec.CommandContext(ctx, bridge, sub, "--json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = waitDelay
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return Status{}, fmt.Errorf("%s %s: %w", Name, sub, ctx.Err())
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return Status{}, &ExitError{Subcommand: sub, Code: exit.ExitCode(), Reason: reason(stderr.Bytes())}
		}
		return Status{}, fmt.Errorf("running %s %s: %w", Name, sub, err)
	}
	var s Status
	if err := json.Unmarshal(stdout.Bytes(), &s); err != nil {
		return Status{}, fmt.Errorf("parsing the %s %s report: %w", Name, sub, err)
	}
	return s, nil
}

// reason is the trimmed first line of the bridge's stderr, at most
// reasonLimit bytes and never cut inside a UTF-8 sequence.
func reason(stderr []byte) string {
	line, _, _ := strings.Cut(strings.TrimSpace(string(stderr)), "\n")
	line = strings.TrimSpace(line)
	if len(line) <= reasonLimit {
		return line
	}
	n := reasonLimit
	for n > 0 && !utf8.RuneStart(line[n]) {
		n--
	}
	return line[:n]
}
