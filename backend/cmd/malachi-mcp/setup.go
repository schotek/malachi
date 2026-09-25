// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

// The setup subcommands (status, install, uninstall) register this binary
// in the user-level MCP configuration of the Claude apps, so the desktop
// clients can offer one "Register with Claude" switch without editing
// JSON themselves. They touch nothing but the two files named in
// clientSpecs, never create the apps' directories (an app that is not
// installed is reported, not configured), and write the read-only tier:
// no --allow-* flag ever lands in a file. See docs/mcp.md.

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
)

// mcpServerName is the key under mcpServers in both apps' files.
const mcpServerName = "malachi"

// setupEnv is what the subcommands need from the machine: the two base
// directories the Claude apps use and the path this binary runs from.
type setupEnv struct {
	home      string // os.UserHomeDir: ~/.claude.json and ~/.claude/
	configDir string // os.UserConfigDir: <configDir>/Claude/claude_desktop_config.json
	command   string // this executable, symlinks resolved
}

// locateSetupEnv is a variable so the tests can point the subcommands at
// a temporary directory instead of the real home.
var locateSetupEnv = realSetupEnv

func realSetupEnv() (setupEnv, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return setupEnv{}, fmt.Errorf("home directory: %w", err)
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return setupEnv{}, fmt.Errorf("config directory: %w", err)
	}
	exe, err := os.Executable()
	if err != nil {
		return setupEnv{}, fmt.Errorf("own executable: %w", err)
	}
	// A symlink (a bin directory pointing into a checkout, say) would make
	// the registered command differ from what the next run resolves to.
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return setupEnv{home: home, configDir: configDir, command: exe}, nil
}

// clientSpec is one Claude app: where its configuration lives and whether
// the app looks installed at all.
type clientSpec struct {
	id, name string
	path     string // the configuration file, whether or not it exists
	present  bool
}

// clients lists the apps in the order the report shows them.
func (e setupEnv) clients() []clientSpec {
	desktop := filepath.Join(e.configDir, "Claude", "claude_desktop_config.json")
	code := filepath.Join(e.home, ".claude.json")
	return []clientSpec{
		{
			id: "claude-desktop", name: "Claude Desktop", path: desktop,
			present: isDir(filepath.Dir(desktop)),
		},
		{
			id: "claude-code", name: "Claude Code", path: code,
			present: isFile(code) || isDir(filepath.Join(e.home, ".claude")),
		},
	}
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// setupClient is one app in the report. The JSON shape is what the
// desktop UIs read (docs/mcp.md); the field names are a contract.
type setupClient struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Present    bool   `json:"present"`
	Registered bool   `json:"registered"`
	Path       string `json:"path"`
	// Other is the command of a malachi entry that is not this binary;
	// install replaces it.
	Other string `json:"other,omitempty"`
}

// setupStatus is the whole report.
type setupStatus struct {
	Command string        `json:"command"`
	Clients []setupClient `json:"clients"`
}

// writeText prints the human-readable form of the report.
func (st setupStatus) writeText(w io.Writer) {
	fmt.Fprintf(w, "command: %s\n", st.Command)
	for _, c := range st.Clients {
		var state string
		switch {
		case !c.Present:
			state = "not installed"
		case c.Registered:
			state = "registered"
		case c.Other != "":
			state = "registered elsewhere: " + c.Other
		default:
			state = "not registered"
		}
		fmt.Fprintf(w, "%s: %s (%s)\n", c.Name, state, c.Path)
	}
}

// runSetup runs one of the setup subcommands. Every failure is returned
// as an error (main prints it on one stderr line and exits 1); the
// report goes to stdout, as JSON with --json.
func runSetup(sub string, args []string, stdout, stderr io.Writer) error {
	switch sub {
	case "status", "install", "uninstall":
	default:
		return fmt.Errorf("unknown subcommand %q (status, install, uninstall)", sub)
	}
	fs := flag.NewFlagSet("malachi-mcp "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print the report as one JSON object")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: malachi-mcp %s [--json]\n", sub)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("%s takes no arguments", sub)
	}
	env, err := locateSetupEnv()
	if err != nil {
		return err
	}
	switch sub {
	case "install":
		err = env.install()
	case "uninstall":
		err = env.uninstall()
	}
	if err != nil {
		return err
	}
	st, err := env.status()
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(st)
	}
	st.writeText(stdout)
	return nil
}

// status reads every present app's file. An unparsable file is an error
// rather than "not registered": the UI shows the reason and install
// would refuse the same file.
func (e setupEnv) status() (setupStatus, error) {
	st := setupStatus{Command: e.command, Clients: []setupClient{}}
	for _, c := range e.clients() {
		sc := setupClient{ID: c.id, Name: c.name, Present: c.present, Path: c.path}
		if c.present {
			cfg, err := loadConfig(c.path)
			if err != nil {
				return setupStatus{}, err
			}
			sc.Registered, sc.Other = cfg.registration(e.command)
		}
		st.Clients = append(st.Clients, sc)
	}
	return st, nil
}

// loadPresent parses the files of every present app before anything is
// written, so a broken file leaves every file untouched.
func (e setupEnv) loadPresent() ([]*mcpConfig, error) {
	var cfgs []*mcpConfig
	for _, c := range e.clients() {
		if !c.present {
			continue
		}
		cfg, err := loadConfig(c.path)
		if err != nil {
			return nil, err
		}
		cfgs = append(cfgs, cfg)
	}
	return cfgs, nil
}

// install sets the malachi entry in every present app's file, creating
// the file when only the directory exists.
func (e setupEnv) install() error {
	cfgs, err := e.loadPresent()
	if err != nil {
		return err
	}
	if len(cfgs) == 0 {
		return errors.New("no Claude app found (Claude Desktop or Claude Code)")
	}
	for _, cfg := range cfgs {
		if !cfg.setEntry(e.command) {
			continue
		}
		if err := cfg.write(); err != nil {
			return err
		}
	}
	return nil
}

// uninstall removes the malachi entry wherever it is; a missing file or
// entry is nothing to do.
func (e setupEnv) uninstall() error {
	cfgs, err := e.loadPresent()
	if err != nil {
		return err
	}
	for _, cfg := range cfgs {
		if !cfg.exists || !cfg.removeEntry() {
			continue
		}
		if err := cfg.write(); err != nil {
			return err
		}
	}
	return nil
}

// mcpConfig is one app's configuration file held whole, so a rewrite
// changes only the malachi entry. Numbers stay json.Number (written back
// exactly as read) and nothing is HTML-escaped on the way out.
type mcpConfig struct {
	path   string
	exists bool
	mode   os.FileMode // kept on rewrite; 0600 for a new file
	root   map[string]any
}

// loadConfig reads path. A missing file is an empty configuration; an
// existing one must be a JSON object whose mcpServers, if any, is an
// object, or the file is refused with the path and the reason.
func loadConfig(path string) (*mcpConfig, error) {
	c := &mcpConfig{path: path, mode: 0o600, root: map[string]any{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	c.exists = true
	if info, err := os.Stat(path); err == nil {
		c.mode = info.Mode().Perm()
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return c, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		return nil, fmt.Errorf("%s: cannot parse: %w", path, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: cannot parse: data after the JSON object", path)
	}
	obj, ok := root.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: cannot parse: the top level is not a JSON object", path)
	}
	if servers, ok := obj["mcpServers"]; ok {
		if _, ok := servers.(map[string]any); !ok {
			return nil, fmt.Errorf("%s: cannot parse: mcpServers is not a JSON object", path)
		}
	}
	c.root = obj
	return c, nil
}

// entry returns the malachi entry (nil when it is not an object) and
// whether the key exists at all.
func (c *mcpConfig) entry() (map[string]any, bool) {
	servers, _ := c.root["mcpServers"].(map[string]any)
	raw, ok := servers[mcpServerName]
	if !ok {
		return nil, false
	}
	m, _ := raw.(map[string]any)
	return m, true
}

// registration reports whether the malachi entry runs command; other is
// the command it runs instead, when the entry exists and names one.
func (c *mcpConfig) registration(command string) (registered bool, other string) {
	e, ok := c.entry()
	if !ok {
		return false, ""
	}
	cmd, _ := e["command"].(string)
	if cmd == command {
		return true, ""
	}
	return false, cmd
}

// wantEntry is what install writes: the read-only tier, no flags.
func wantEntry(command string) map[string]any {
	return map[string]any{"type": "stdio", "command": command, "args": []any{}}
}

// setEntry sets the malachi entry and reports whether the file changed.
// An entry that already says exactly this is left alone, so the file is
// not rewritten (Claude Code keeps its own state in the same file and
// writes it while running).
func (c *mcpConfig) setEntry(command string) (changed bool) {
	want := wantEntry(command)
	if have, ok := c.entry(); ok && reflect.DeepEqual(have, want) {
		return false
	}
	servers, _ := c.root["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
		c.root["mcpServers"] = servers
	}
	servers[mcpServerName] = want
	return true
}

// removeEntry deletes the malachi entry, and mcpServers itself once it is
// empty, and reports whether the file changed.
func (c *mcpConfig) removeEntry() (changed bool) {
	servers, _ := c.root["mcpServers"].(map[string]any)
	if _, ok := servers[mcpServerName]; !ok {
		return false
	}
	delete(servers, mcpServerName)
	if len(servers) == 0 {
		delete(c.root, "mcpServers")
	}
	return true
}

// write replaces the file atomically: the new content goes to a temporary
// file in the same directory, which is then renamed over the original, so
// a crash or a concurrent reader never sees a half-written file. The mode
// of an existing file is kept, a new one is private; a symlinked file is
// replaced at its target, not turned into a regular file.
func (c *mcpConfig) write() error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(c.root); err != nil {
		return fmt.Errorf("%s: encode: %w", c.path, err)
	}
	target := c.path
	if c.exists {
		if resolved, err := filepath.EvalSymlinks(c.path); err == nil {
			target = resolved
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".*")
	if err != nil {
		return fmt.Errorf("%s: cannot write: %w", c.path, err)
	}
	tmpName := tmp.Name()
	fail := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("%s: cannot write: %w", c.path, err)
	}
	if err := tmp.Chmod(c.mode); err != nil {
		return fail(err)
	}
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("%s: cannot write: %w", c.path, err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("%s: cannot write: %w", c.path, err)
	}
	c.exists = true
	return nil
}
