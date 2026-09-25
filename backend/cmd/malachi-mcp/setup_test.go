// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// setupFixture is a fresh home and config directory the subcommands are
// pointed at for one test; the real machine is never touched.
type setupFixture struct {
	home, configDir, command string
}

func newSetupFixture(t *testing.T) setupFixture {
	t.Helper()
	root := t.TempDir()
	fx := setupFixture{
		home:      filepath.Join(root, "home"),
		configDir: filepath.Join(root, "config"),
		command:   filepath.Join(root, "bin", "malachi-mcp"),
	}
	for _, d := range []string{fx.home, fx.configDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	prev := locateSetupEnv
	locateSetupEnv = func() (setupEnv, error) {
		return setupEnv{home: fx.home, configDir: fx.configDir, command: fx.command}, nil
	}
	t.Cleanup(func() { locateSetupEnv = prev })
	return fx
}

func (fx setupFixture) desktopDir() string { return filepath.Join(fx.configDir, "Claude") }
func (fx setupFixture) desktopFile() string {
	return filepath.Join(fx.desktopDir(), "claude_desktop_config.json")
}
func (fx setupFixture) codeDir() string  { return filepath.Join(fx.home, ".claude") }
func (fx setupFixture) codeFile() string { return filepath.Join(fx.home, ".claude.json") }

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	mkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil { // WriteFile honours the umask
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// parseFile decodes a configuration file the way the code does.
func parseFile(t *testing.T, path string) map[string]any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(readFile(t, path)))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return m
}

func serverEntry(t *testing.T, path, name string) (map[string]any, bool) {
	t.Helper()
	servers, _ := parseFile(t, path)["mcpServers"].(map[string]any)
	raw, ok := servers[name]
	if !ok {
		return nil, false
	}
	m, _ := raw.(map[string]any)
	return m, true
}

// runCmd runs one command line through run and returns stdout, stderr
// and the error main would print (exit 1).
func runCmd(args ...string) (stdout, stderr string, err error) {
	var out, errOut strings.Builder
	err = run(args, &out, &errOut)
	return out.String(), errOut.String(), err
}

// statusJSON runs sub --json and decodes the report.
func statusJSON(t *testing.T, sub string) setupStatus {
	t.Helper()
	out, errOut, err := runCmd(sub, "--json")
	if err != nil {
		t.Fatalf("%s --json: %v", sub, err)
	}
	if errOut != "" {
		t.Errorf("%s --json wrote to stderr: %q", sub, errOut)
	}
	var st setupStatus
	dec := json.NewDecoder(strings.NewReader(out))
	if err := dec.Decode(&st); err != nil {
		t.Fatalf("%s --json is not JSON: %v\n%s", sub, err, out)
	}
	if dec.More() {
		t.Errorf("%s --json printed more than one JSON value:\n%s", sub, out)
	}
	return st
}

func client(t *testing.T, st setupStatus, id string) setupClient {
	t.Helper()
	for _, c := range st.Clients {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no client %q in %+v", id, st)
	return setupClient{}
}

func TestSetupStatusPresence(t *testing.T) {
	fx := newSetupFixture(t)

	st := statusJSON(t, "status")
	if st.Command != fx.command {
		t.Errorf("command %q, want %q", st.Command, fx.command)
	}
	if len(st.Clients) != 2 || st.Clients[0].ID != "claude-desktop" || st.Clients[1].ID != "claude-code" {
		t.Fatalf("clients %+v, want claude-desktop then claude-code", st.Clients)
	}
	want := []setupClient{
		{ID: "claude-desktop", Name: "Claude Desktop", Path: fx.desktopFile()},
		{ID: "claude-code", Name: "Claude Code", Path: fx.codeFile()},
	}
	if !reflect.DeepEqual(st.Clients, want) {
		t.Errorf("nothing installed: clients %+v, want %+v", st.Clients, want)
	}

	mkdir(t, fx.desktopDir())
	st = statusJSON(t, "status")
	if d := client(t, st, "claude-desktop"); !d.Present || d.Registered {
		t.Errorf("Claude directory present: %+v", d)
	}
	if c := client(t, st, "claude-code"); c.Present {
		t.Errorf("no .claude.json nor .claude/: %+v", c)
	}

	mkdir(t, fx.codeDir())
	st = statusJSON(t, "status")
	for _, id := range []string{"claude-desktop", "claude-code"} {
		if c := client(t, st, id); !c.Present || c.Registered || c.Other != "" {
			t.Errorf("both directories present, no files: %+v", c)
		}
	}
}

func TestSetupStatusCodeFileAlone(t *testing.T) {
	fx := newSetupFixture(t)
	writeFile(t, fx.codeFile(), `{"numStartups": 3}`, 0o600)
	st := statusJSON(t, "status")
	if c := client(t, st, "claude-code"); !c.Present || c.Registered {
		t.Errorf(".claude.json without .claude/: %+v", c)
	}
}

func TestSetupInstallCreatesFile(t *testing.T) {
	fx := newSetupFixture(t)
	mkdir(t, fx.desktopDir())

	out, errOut, err := runCmd("install")
	if err != nil {
		t.Fatalf("install: %v (stderr %q)", err, errOut)
	}
	mustContain(t, out, "command: "+fx.command, "Claude Desktop: registered ("+fx.desktopFile()+")", "Claude Code: not installed (")

	entry, ok := serverEntry(t, fx.desktopFile(), "malachi")
	if !ok {
		t.Fatalf("no malachi entry:\n%s", readFile(t, fx.desktopFile()))
	}
	want := map[string]any{"type": "stdio", "command": fx.command, "args": []any{}}
	if !reflect.DeepEqual(entry, want) {
		t.Errorf("entry %#v, want %#v", entry, want)
	}
	info, err := os.Stat(fx.desktopFile())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("new file mode %o, want 0600", info.Mode().Perm())
	}
	if _, err := os.Stat(fx.codeFile()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("install created %s for an app that is not installed", fx.codeFile())
	}
	if _, err := os.Stat(fx.codeDir()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("install created %s", fx.codeDir())
	}

	st := statusJSON(t, "status")
	if d := client(t, st, "claude-desktop"); !d.Present || !d.Registered || d.Other != "" {
		t.Errorf("after install: %+v", d)
	}
}

func TestSetupInstallKeepsForeignContent(t *testing.T) {
	fx := newSetupFixture(t)
	const before = `{
  "n": 12345678901234567890,
  "projects": {"/x": {"allowedTools": ["a", "b"], "big": 1.0e+30, "t": 1758700000123}},
  "mcpServers": {"other": {"type": "stdio", "command": "/bin/other", "args": ["--x"]}},
  "s": "<tag> & ünïcödé"
}
`
	writeFile(t, fx.codeFile(), before, 0o644)

	if _, _, err := runCmd("install"); err != nil {
		t.Fatal(err)
	}
	after := readFile(t, fx.codeFile())
	mustContain(t, after,
		`"n": 12345678901234567890`, `"big": 1.0e+30`, `"t": 1758700000123`,
		`"allowedTools": [`, `"other": {`, `"/bin/other"`, `"--x"`,
		`"s": "<tag> & ünïcödé"`,
		"\n  \"mcpServers\": {\n    \"malachi\": {\n      \"args\": [],\n      \"command\": \""+fx.command+"\",\n      \"type\": \"stdio\"\n    },\n")
	// The escapes are assembled here because some editors decode them.
	esc := "\\" + "u"
	mustNotContain(t, after, esc+"003c", esc+"0026")
	if !strings.HasSuffix(after, "}\n") {
		t.Errorf("file does not end with a newline:\n%s", after)
	}
	root := parseFile(t, fx.codeFile())
	servers, _ := root["mcpServers"].(map[string]any)
	if len(servers) != 2 {
		t.Errorf("mcpServers %v, want other and malachi", servers)
	}
	if n, _ := root["n"].(json.Number); n.String() != "12345678901234567890" {
		t.Errorf("n = %v", root["n"])
	}
	info, err := os.Stat(fx.codeFile())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode %o, want the original 0644", info.Mode().Perm())
	}
	entries, err := os.ReadDir(fx.home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != ".claude.json" {
		t.Errorf("temporary file left behind: %v", entries)
	}
}

func TestSetupInstallReplacesOtherCommand(t *testing.T) {
	fx := newSetupFixture(t)
	writeFile(t, fx.desktopFile(),
		`{"mcpServers": {"malachi": {"command": "/old/malachi-mcp", "args": ["--allow-send"]}}}`, 0o600)

	st := statusJSON(t, "status")
	if d := client(t, st, "claude-desktop"); !d.Present || d.Registered || d.Other != "/old/malachi-mcp" {
		t.Errorf("foreign entry: %+v", d)
	}
	out, _, err := runCmd("status")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Claude Desktop: registered elsewhere: /old/malachi-mcp (")

	st = statusJSON(t, "install")
	if d := client(t, st, "claude-desktop"); !d.Registered || d.Other != "" {
		t.Errorf("after install: %+v", d)
	}
	entry, _ := serverEntry(t, fx.desktopFile(), "malachi")
	want := map[string]any{"type": "stdio", "command": fx.command, "args": []any{}}
	if !reflect.DeepEqual(entry, want) {
		t.Errorf("entry %#v, want %#v", entry, want)
	}
}

func TestSetupInstallLeavesAnExactEntryAlone(t *testing.T) {
	fx := newSetupFixture(t)
	// Compact, unsorted, no trailing newline: a rewrite would change it.
	before := `{"z":1,"mcpServers":{"malachi":{"type":"stdio","command":"` + fx.command + `","args":[]}}}`
	writeFile(t, fx.codeFile(), before, 0o600)
	if _, _, err := runCmd("install"); err != nil {
		t.Fatal(err)
	}
	if after := readFile(t, fx.codeFile()); after != before {
		t.Errorf("an already registered file was rewritten:\n%s", after)
	}
}

func TestSetupUninstall(t *testing.T) {
	fx := newSetupFixture(t)
	writeFile(t, fx.desktopFile(), `{"mcpServers": {"malachi": {"command": "`+fx.command+`"}}}`, 0o600)
	writeFile(t, fx.codeFile(), `{"keep": true, "mcpServers": {"malachi": {"command": "x"}, "other": {"command": "y"}}}`, 0o600)

	st := statusJSON(t, "uninstall")
	for _, id := range []string{"claude-desktop", "claude-code"} {
		if c := client(t, st, id); !c.Present || c.Registered || c.Other != "" {
			t.Errorf("after uninstall: %+v", c)
		}
	}
	if root := parseFile(t, fx.desktopFile()); len(root) != 0 {
		t.Errorf("desktop file %v, want an empty object (empty mcpServers removed)", root)
	}
	root := parseFile(t, fx.codeFile())
	if root["keep"] != true {
		t.Errorf("foreign key lost: %v", root)
	}
	servers, _ := root["mcpServers"].(map[string]any)
	if _, ok := servers["malachi"]; ok || len(servers) != 1 {
		t.Errorf("mcpServers %v, want only other", servers)
	}

	// Nothing to do is not an error and does not create files.
	if err := os.Remove(fx.desktopFile()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runCmd("uninstall"); err != nil {
		t.Errorf("uninstall with nothing to remove: %v", err)
	}
	if _, err := os.Stat(fx.desktopFile()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("uninstall created %s", fx.desktopFile())
	}
}

func TestSetupInstallNoClient(t *testing.T) {
	fx := newSetupFixture(t)
	out, _, err := runCmd("install")
	if err == nil || err.Error() != "no Claude app found (Claude Desktop or Claude Code)" {
		t.Fatalf("install without any app: err %v, stdout %q", err, out)
	}
	if out != "" {
		t.Errorf("stdout %q, want nothing", out)
	}
	for _, d := range []string{fx.home, fx.configDir} {
		entries, err := os.ReadDir(d)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Errorf("%s: install created %v", d, entries)
		}
	}
	// uninstall and status have nothing to refuse.
	if _, _, err := runCmd("uninstall"); err != nil {
		t.Errorf("uninstall: %v", err)
	}
	if _, _, err := runCmd("status"); err != nil {
		t.Errorf("status: %v", err)
	}
}

func TestSetupUnparsableFile(t *testing.T) {
	fx := newSetupFixture(t)
	mkdir(t, fx.codeDir())
	for _, broken := range []string{"{ not json", "[]", `{"mcpServers": []}`, `{} {}`} {
		writeFile(t, fx.desktopFile(), broken, 0o600)
		for _, sub := range []string{"install", "uninstall", "status"} {
			out, _, err := runCmd(sub)
			if err == nil {
				t.Fatalf("%s with %q: no error, stdout %q", sub, broken, out)
			}
			if !strings.Contains(err.Error(), fx.desktopFile()) || !strings.Contains(err.Error(), "cannot parse") {
				t.Errorf("%s with %q: error %q does not name the file and the reason", sub, broken, err)
			}
			if out != "" {
				t.Errorf("%s with %q: stdout %q, want nothing", sub, broken, out)
			}
		}
		if got := readFile(t, fx.desktopFile()); got != broken {
			t.Errorf("broken file was touched: %q", got)
		}
		// The other app's file is not written either: all files are
		// parsed before any is written.
		if _, err := os.Stat(fx.codeFile()); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("install wrote %s although %s was refused", fx.codeFile(), fx.desktopFile())
		}
	}
}

func TestSetupEmptyFileIsEmptyConfig(t *testing.T) {
	fx := newSetupFixture(t)
	writeFile(t, fx.codeFile(), "\n", 0o600)
	st := statusJSON(t, "install")
	if c := client(t, st, "claude-code"); !c.Registered {
		t.Errorf("after install into an empty file: %+v", c)
	}
	if _, ok := serverEntry(t, fx.codeFile(), "malachi"); !ok {
		t.Errorf("no entry:\n%s", readFile(t, fx.codeFile()))
	}
}

func TestSetupHumanOutput(t *testing.T) {
	fx := newSetupFixture(t)
	out, errOut, err := runCmd("status")
	if err != nil || errOut != "" {
		t.Fatalf("status: %v, stderr %q", err, errOut)
	}
	want := "command: " + fx.command + "\n" +
		"Claude Desktop: not installed (" + fx.desktopFile() + ")\n" +
		"Claude Code: not installed (" + fx.codeFile() + ")\n"
	if out != want {
		t.Errorf("status:\n%s\nwant:\n%s", out, want)
	}

	mkdir(t, fx.desktopDir())
	mkdir(t, fx.codeDir())
	if _, _, err := runCmd("install"); err != nil {
		t.Fatal(err)
	}
	out, _, err = runCmd("status")
	if err != nil {
		t.Fatal(err)
	}
	want = "command: " + fx.command + "\n" +
		"Claude Desktop: registered (" + fx.desktopFile() + ")\n" +
		"Claude Code: registered (" + fx.codeFile() + ")\n"
	if out != want {
		t.Errorf("status after install:\n%s\nwant:\n%s", out, want)
	}
	out, _, err = runCmd("uninstall")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Claude Desktop: not registered (", "Claude Code: not registered (")
}

// TestSetupJSONShape pins the documented keys: the desktop UIs decode
// exactly these.
func TestSetupJSONShape(t *testing.T) {
	fx := newSetupFixture(t)
	mkdir(t, fx.desktopDir())
	writeFile(t, fx.codeFile(), `{"mcpServers": {"malachi": {"command": "/elsewhere"}}}`, 0o600)

	out, _, err := runCmd("status", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("not a JSON object: %v\n%s", err, out)
	}
	if keys := sortedKeys(raw); !reflect.DeepEqual(keys, []string{"clients", "command"}) {
		t.Errorf("top-level keys %v", keys)
	}
	var clients []map[string]any
	if err := json.Unmarshal(raw["clients"], &clients); err != nil {
		t.Fatal(err)
	}
	if len(clients) != 2 {
		t.Fatalf("clients %v", clients)
	}
	if keys := sortedKeys(clients[0]); !reflect.DeepEqual(keys, []string{"id", "name", "path", "present", "registered"}) {
		t.Errorf("claude-desktop keys %v", keys)
	}
	if keys := sortedKeys(clients[1]); !reflect.DeepEqual(keys, []string{"id", "name", "other", "path", "present", "registered"}) {
		t.Errorf("claude-code keys %v (a foreign command adds other)", keys)
	}
	want := map[string]any{"id": "claude-code", "name": "Claude Code", "present": true, "registered": false,
		"path": fx.codeFile(), "other": "/elsewhere"}
	if !reflect.DeepEqual(clients[1], want) {
		t.Errorf("claude-code %v, want %v", clients[1], want)
	}
	// -json is the same flag.
	if out2, _, err := runCmd("status", "-json"); err != nil || out2 != out {
		t.Errorf("-json: %v\n%s", err, out2)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func TestSetupCommandLine(t *testing.T) {
	newSetupFixture(t)
	if _, _, err := runCmd("bogus"); err == nil || !strings.Contains(err.Error(), `unknown subcommand "bogus"`) {
		t.Errorf("bogus: %v", err)
	}
	if _, _, err := runCmd("status", "extra"); err == nil || !strings.Contains(err.Error(), "takes no arguments") {
		t.Errorf("status extra: %v", err)
	}
	if _, errOut, err := runCmd("status", "--nope"); err == nil || !strings.Contains(errOut, "Usage: malachi-mcp status [--json]") {
		t.Errorf("status --nope: %v, stderr %q", err, errOut)
	}
	out, errOut, err := runCmd("install", "-h")
	if err != nil || out != "" || !strings.Contains(errOut, "Usage: malachi-mcp install [--json]") {
		t.Errorf("install -h: %v, stdout %q, stderr %q", err, out, errOut)
	}
	// A leading flag is the server's, not a subcommand's.
	if _, _, err := runCmd("--json", "status"); err == nil || !strings.Contains(err.Error(), "-json") {
		t.Errorf("--json status: %v", err)
	}
}

func TestUsageListsSubcommands(t *testing.T) {
	out, errOut, err := runCmd("-h")
	if err != nil || out != "" {
		t.Fatalf("-h: %v, stdout %q", err, out)
	}
	mustContain(t, errOut, "malachi-mcp [flags]", "malachi-mcp status [--json]", "malachi-mcp install [--json]",
		"malachi-mcp uninstall [--json]", "-allow-modify", "-allow-send", "-socket", "-version")
	if !bytes.HasPrefix([]byte(errOut), []byte("Usage:\n")) {
		t.Errorf("usage starts with %q", errOut[:min(len(errOut), 20)])
	}
}

// TestRealSetupEnv only checks that the real lookup works on this
// machine; it reads no file.
func TestRealSetupEnv(t *testing.T) {
	env, err := realSetupEnv()
	if err != nil {
		t.Fatal(err)
	}
	if env.home == "" || env.configDir == "" || !filepath.IsAbs(env.command) {
		t.Errorf("%+v", env)
	}
	if _, err := os.Stat(env.command); err != nil {
		t.Errorf("command %s: %v", env.command, err)
	}
}
