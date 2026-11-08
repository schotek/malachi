// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

// The setup subcommands (status, install, uninstall) register this binary
// in the user-level MCP configuration of the Claude apps, so the desktop
// clients can offer a "Register with Claude" switch per app without
// editing JSON themselves (install and uninstall act on every present app,
// or with --client only on the named ones). They touch nothing but the two files the report names,
// never create the apps' directories (an app that is not installed is
// reported, not configured), and write the read-only tier: no --allow-*
// flag ever lands in a file. Flags let a desktop client supply what the
// bridge cannot find by itself (a Claude Desktop packaged where
// os.UserConfigDir does not lead, a launcher path to register, the
// configuration files when $HOME is not the user's home, a socket the
// registered bridge must use, a file for the report when nothing reads
// stdout); the knowledge of any platform's packaging stays in that
// client. See docs/mcp.md.

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
)

// mcpServerName is the key under mcpServers in both apps' files.
const mcpServerName = "malachi"

// setupEnv is what the subcommands work with: where the two Claude apps
// keep their configuration and the command that gets registered.
type setupEnv struct {
	home          string // ~/.claude.json and ~/.claude/
	desktopConfig string // Claude Desktop's configuration file
	codeConfig    string // Claude Code's ~/.claude.json
	command       string // what install writes and status compares against
	socket        string // MALACHI_SOCKET of the entry; "" for none
}

// setupFlags are the flags that replace a lookup; empty means not given.
type setupFlags struct {
	desktopConfig string // --claude-desktop-config
	codeConfig    string // --claude-code-config
	command       string // --command
	socket        string // --socket
	output        string // --output
	// clients are the ids --client named (install and uninstall), each
	// once, in the order given; none means every client.
	clients []string
}

// setupLookups asks the machine for what no flag gave. It is a variable
// so the tests can point the subcommands at a temporary directory
// instead of the real home.
var setupLookups = struct {
	homeDir, configDir, executable func() (string, error)
}{os.UserHomeDir, os.UserConfigDir, os.Executable}

// locateSetupEnv takes what the flags give and looks up the rest: the
// home directory always (Claude Code has no flag), the user config
// directory only without --claude-desktop-config and this executable only
// without --command, so a lookup a flag replaces cannot fail the command.
func locateSetupEnv(given setupFlags) (setupEnv, error) {
	env := setupEnv{desktopConfig: given.desktopConfig, codeConfig: given.codeConfig, command: given.command, socket: given.socket}
	if env.codeConfig != "" {
		// Claude Code's directory lies beside its file.
		env.home = filepath.Dir(env.codeConfig)
	} else {
		home, err := setupLookups.homeDir()
		if err != nil {
			return setupEnv{}, fmt.Errorf("home directory: %w", err)
		}
		env.home = home
		env.codeConfig = filepath.Join(home, ".claude.json")
	}
	if env.desktopConfig == "" {
		configDir, err := setupLookups.configDir()
		if err != nil {
			return setupEnv{}, fmt.Errorf("config directory: %w", err)
		}
		env.desktopConfig = filepath.Join(configDir, "Claude", "claude_desktop_config.json")
	}
	if env.command == "" {
		exe, err := setupLookups.executable()
		if err != nil {
			return setupEnv{}, fmt.Errorf("own executable: %w", err)
		}
		// A symlink (a bin directory pointing into a checkout, say) would
		// make the registered command differ from what the next run
		// resolves to. --command is only cleaned, never resolved: a caller
		// that names a stable launcher (an alias, a current/ directory)
		// means that path.
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		env.command = exe
	}
	return env, nil
}

// absPathFlag parses a path flag into dst: an absolute path, cleaned. A
// relative one is refused rather than resolved: Claude would look for a
// relative command from its own working directory, and a report is read
// by an app that does not know where the bridge ran.
func absPathFlag(dst *string) func(string) error {
	return func(v string) error {
		if !filepath.IsAbs(v) {
			return errors.New("not an absolute path")
		}
		*dst = filepath.Clean(v)
		return nil
	}
}

// clientSpec is one Claude app: where its configuration lives and whether
// the app looks installed at all.
type clientSpec struct {
	id, name string
	path     string // the configuration file, whether or not it exists
	present  bool
}

// The client ids, as the report gives them.
const (
	clientDesktop = "claude-desktop"
	clientCode    = "claude-code"
)

// clientIDs are the ids --client takes, in the order of the report.
var clientIDs = []string{clientDesktop, clientCode}

// clientFlag parses --client into dst: one of clientIDs, each kept once.
func clientFlag(dst *[]string) func(string) error {
	return func(v string) error {
		if !slices.Contains(clientIDs, v) {
			return fmt.Errorf("unknown client %q (%s)", v, strings.Join(clientIDs, ", "))
		}
		if !slices.Contains(*dst, v) {
			*dst = append(*dst, v)
		}
		return nil
	}
}

// clientsNamed is clients limited to the ids named, in the report's
// order; nil or empty names every client.
func (e setupEnv) clientsNamed(ids []string) []clientSpec {
	all := e.clients()
	if len(ids) == 0 {
		return all
	}
	var out []clientSpec
	for _, c := range all {
		if slices.Contains(ids, c.id) {
			out = append(out, c)
		}
	}
	return out
}

// clients lists every app in the order the report shows them.
func (e setupEnv) clients() []clientSpec {
	code := e.codeConfig
	return []clientSpec{
		{
			id: clientDesktop, name: "Claude Desktop", path: e.desktopConfig,
			present: isDir(filepath.Dir(e.desktopConfig)),
		},
		{
			id: clientCode, name: "Claude Code", path: code,
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
	var given setupFlags
	fs.Func("claude-desktop-config",
		"use `PATH` (absolute) as Claude Desktop's configuration file instead of <UserConfigDir>/Claude/claude_desktop_config.json; Claude Desktop is present when its directory exists",
		absPathFlag(&given.desktopConfig))
	fs.Func("command",
		"register `PATH` (absolute, symlinks not resolved) and compare against it instead of this executable",
		absPathFlag(&given.command))
	fs.Func("claude-code-config",
		"use `PATH` (absolute) as Claude Code's configuration file instead of ~/.claude.json; Claude Code is present when it exists or a .claude directory lies beside it",
		absPathFlag(&given.codeConfig))
	fs.Func("socket",
		"register the bridge with MALACHI_SOCKET=`PATH` (absolute) in its environment, for a daemon whose socket is not at the default path",
		absPathFlag(&given.socket))
	fs.Func("output",
		"write the report as JSON to `PATH` (absolute) instead of stdout, and a failure as {\"error\": …} there too, for a caller that cannot read stdout",
		absPathFlag(&given.output))
	clientUsage := ""
	if sub != "status" {
		fs.Func("client",
			"act only on the Claude app `ID` ("+strings.Join(clientIDs, " or ")+"); repeatable; without it every present app",
			clientFlag(&given.clients))
		clientUsage = " [--client ID]..."
	}
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: malachi-mcp %s [--json]%s [--claude-desktop-config PATH] [--claude-code-config PATH] [--command PATH] [--socket PATH] [--output PATH]\n", sub, clientUsage)
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
	st, err := runSetupOn(sub, given)
	if given.output != "" {
		return writeReport(given.output, st, err)
	}
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
// setupRequestEnv names a file holding a setup command line, for a caller
// that can start the bridge but not pass it arguments: the Mac App Store
// build starts its sandboxed bridge through LaunchServices, which drops
// the arguments of a sandboxed caller, so the bridge's bundle sets this
// variable (LSEnvironment) and the app writes the request
// (docs/plans/mac-app-store.md). A leading "~/" is the user's home from
// the account database, not $HOME, which a sandbox points elsewhere.
// Claude starts the same executable directly, without it, as the server.
const setupRequestEnv = "MALACHI_MCP_SETUP_REQUEST"

// maxSetupRequest bounds the request file.
const maxSetupRequest = 64 << 10

// setupRequest is the request file: one command line, as it would be given.
type setupRequest struct {
	Args []string `json:"args"`
}

// runSetupRequest reads the request at path, removes it so it runs once,
// and runs it. The request must name --output: nothing reads stdout.
func runSetupRequest(path string) error {
	path, err := expandHome(path)
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("setup request: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxSetupRequest+1))
	_ = f.Close()
	_ = os.Remove(path)
	if err != nil {
		return fmt.Errorf("setup request: %w", err)
	}
	if len(data) > maxSetupRequest {
		return errors.New("setup request: too large")
	}
	var req setupRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return fmt.Errorf("setup request: %w", err)
	}
	if len(req.Args) == 0 || !hasOutputFlag(req.Args[1:]) {
		return errors.New("setup request: a subcommand with --output is required")
	}
	return runSetup(req.Args[0], req.Args[1:], io.Discard, io.Discard)
}

// hasOutputFlag reports whether args name --output (either spelling).
func hasOutputFlag(args []string) bool {
	for _, a := range args {
		if a == "--output" || a == "-output" || strings.HasPrefix(a, "--output=") || strings.HasPrefix(a, "-output=") {
			return true
		}
	}
	return false
}

// expandHome replaces a leading "~/" with the user's home directory from
// the account database.
func expandHome(path string) (string, error) {
	if !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	u, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("setup request: home directory: %w", err)
	}
	return filepath.Join(u.HomeDir, path[2:]), nil
}

// runSetupOn does sub with the given flags and returns the report.
func runSetupOn(sub string, given setupFlags) (setupStatus, error) {
	env, err := locateSetupEnv(given)
	if err != nil {
		return setupStatus{}, err
	}
	switch sub {
	case "install":
		err = env.install(given.clients)
	case "uninstall":
		err = env.uninstall(given.clients)
	}
	if err != nil {
		return setupStatus{}, err
	}
	return env.status()
}

// setupFailure is the report --output writes for a failure.
type setupFailure struct {
	Error string `json:"error"`
}

// writeReport writes the report, or the failure, as JSON to path through a
// file beside it and a rename, so a caller polling for it never reads half
// of one; the command still fails with err.
func writeReport(path string, st setupStatus, err error) error {
	var v any = st
	if err != nil {
		v = setupFailure{Error: err.Error()}
	}
	data, jerr := json.MarshalIndent(v, "", "  ")
	if jerr != nil {
		return fmt.Errorf("%s: encode: %w", path, jerr)
	}
	tmp := path + ".tmp"
	if werr := os.WriteFile(tmp, append(data, '\n'), 0o600); werr != nil {
		return fmt.Errorf("%s: cannot write: %w", path, werr)
	}
	if rerr := os.Rename(tmp, path); rerr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("%s: cannot write: %w", path, rerr)
	}
	return err
}

func (e setupEnv) status() (setupStatus, error) {
	st := setupStatus{Command: e.command, Clients: []setupClient{}}
	for _, c := range e.clients() {
		sc := setupClient{ID: c.id, Name: c.name, Present: c.present, Path: c.path}
		if c.present {
			cfg, err := loadConfig(c.path)
			if err != nil {
				return setupStatus{}, err
			}
			sc.Registered, sc.Other = cfg.registration(e.command, e.socket)
		}
		st.Clients = append(st.Clients, sc)
	}
	return st, nil
}

// loadPresent parses the files of every present app before anything is
// written, so a broken file leaves every file untouched (the report after
// the change reads them all too), and returns those of the apps ids names
// (every app without ids).
func (e setupEnv) loadPresent(ids []string) ([]*mcpConfig, error) {
	var cfgs []*mcpConfig
	for _, c := range e.clients() {
		if !c.present {
			continue
		}
		cfg, err := loadConfig(c.path)
		if err != nil {
			return nil, err
		}
		if len(ids) == 0 || slices.Contains(ids, c.id) {
			cfgs = append(cfgs, cfg)
		}
	}
	return cfgs, nil
}

// missingNamed is the names of the apps among ids that are not present,
// in the report's order; none without ids.
func (e setupEnv) missingNamed(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	var names []string
	for _, c := range e.clientsNamed(ids) {
		if !c.present {
			names = append(names, c.name)
		}
	}
	return names
}

// noClientPrefix starts the error of an install with no app to register
// in; the desktop clients map it (part of the setup contract, docs/mcp.md).
const noClientPrefix = "no Claude app found"

// install sets the malachi entry in the file of every present app, or of
// the apps ids names, creating the file when only the directory exists.
// A named app that is not present fails the whole install before any file
// is written, as no app at all does.
func (e setupEnv) install(ids []string) error {
	if missing := e.missingNamed(ids); len(missing) > 0 {
		return fmt.Errorf("%s (%s)", noClientPrefix, strings.Join(missing, " or "))
	}
	cfgs, err := e.loadPresent(ids)
	if err != nil {
		return err
	}
	if len(cfgs) == 0 {
		return errors.New(noClientPrefix + " (Claude Desktop or Claude Code)")
	}
	for _, cfg := range cfgs {
		if !cfg.setEntry(e.command, e.socket) {
			continue
		}
		if err := cfg.write(); err != nil {
			return err
		}
	}
	return nil
}

// uninstall removes the malachi entry wherever it is, in every app or in
// the apps ids names; a missing file or entry, or a named app that is not
// present, is nothing to do.
func (e setupEnv) uninstall(ids []string) error {
	cfgs, err := e.loadPresent(ids)
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

// registration reports whether the malachi entry runs command with
// MALACHI_SOCKET socket ("" for an entry without it); other is the
// command it runs instead, when the entry exists and names one. An entry
// that runs command with another socket is not registered and has no
// other: install rewrites it.
func (c *mcpConfig) registration(command, socket string) (registered bool, other string) {
	e, ok := c.entry()
	if !ok {
		return false, ""
	}
	cmd, _ := e["command"].(string)
	if cmd != command {
		return false, cmd
	}
	return entrySocket(e) == socket, ""
}

// entrySocket is the MALACHI_SOCKET in the entry's env, "" without one.
func entrySocket(e map[string]any) string {
	env, _ := e["env"].(map[string]any)
	s, _ := env["MALACHI_SOCKET"].(string)
	return s
}

// wantEntry is what install writes: the read-only tier, no flags, and
// MALACHI_SOCKET only when a socket was given.
func wantEntry(command, socket string) map[string]any {
	e := map[string]any{"type": "stdio", "command": command, "args": []any{}}
	if socket != "" {
		e["env"] = map[string]any{"MALACHI_SOCKET": socket}
	}
	return e
}

// setEntry sets the malachi entry and reports whether the file changed.
// An entry that already says exactly this is left alone, so the file is
// not rewritten (Claude Code keeps its own state in the same file and
// writes it while running).
func (c *mcpConfig) setEntry(command, socket string) (changed bool) {
	want := wantEntry(command, socket)
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
// replaced at its target, not turned into a regular file. The temporary
// file has a fixed name (tempName), so a sandbox that lets the bridge
// write only named paths can name it too (the Mac App Store build, which
// may write ~/.claude.json and this file beside it, docs/plans/mac-app-store.md);
// one left behind by a crash is replaced.
// tempName is the temporary file write uses for target: a hidden file
// beside it, "." + its name + ".malachi-mcp.tmp".
func tempName(target string) string {
	return filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+".malachi-mcp.tmp")
}

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
	tmpName := tempName(target)
	tmp, err := os.OpenFile(tmpName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		// Left by an install that did not finish: nobody else writes it.
		if err = os.Remove(tmpName); err == nil {
			tmp, err = os.OpenFile(tmpName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		}
	}
	if err != nil {
		return fmt.Errorf("%s: cannot write: %w", c.path, err)
	}
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
