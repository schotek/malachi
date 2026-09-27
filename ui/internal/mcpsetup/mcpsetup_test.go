// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpsetup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// The test binary doubles as the bridge: when fakeEnv is set, TestMain
// plays malachi-mcp in that mode instead of running the tests. The bridge
// inherits the environment, so a t.Setenv in the test selects what the
// process it starts does. holdEnv names the file the "hang" mode runs
// for.
const (
	fakeEnv = "MALACHI_TEST_FAKE_BRIDGE"
	holdEnv = "MALACHI_TEST_FAKE_BRIDGE_HOLD"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeEnv); mode != "" {
		os.Exit(fakeBridgeMain(mode, os.Args[1:]))
	}
	os.Exit(m.Run())
}

const report = `{"command":"/opt/malachi/bin/malachi-mcp","clients":[
{"id":"claude-desktop","name":"Claude Desktop","present":true,"registered":false,
 "path":"/home/u/.config/Claude/claude_desktop_config.json"},
{"id":"claude-code","name":"Claude Code","present":true,"registered":true,
 "path":"/home/u/.claude.json","other":"/usr/local/bin/malachi-mcp"}]}`

const noClientLine = "no Claude app found: neither Claude Desktop nor Claude Code is installed"

// longReason is 100 three-byte runes: the 200-byte cap falls inside a
// rune. A rune constant, so no tool on the way can turn the escape into a
// literal.
var longReason = strings.Repeat(string(rune(0x20AC)), 100) // U+20AC EURO SIGN

// fakeBridgeMain plays malachi-mcp, given its arguments, and returns the
// exit status. The modes:
//
//	report   prints the report above
//	args     prints a report whose command is the arguments it got
//	noclient writes noClientLine to stderr, status 1
//	reason   writes longReason and a second line to stderr, status 2
//	silent   writes nothing, status 3
//	notjson  prints a line that is not JSON
//	hang     starts a child that keeps its stdout and stderr open after it
//	         is killed, then waits; both run while the file named by
//	         holdEnv exists, at most 10 s
//	hold     the child
func fakeBridgeMain(mode string, args []string) int {
	switch mode {
	case "report":
		fmt.Println(report)
	case "args":
		fmt.Printf("{\"command\":%q,\"clients\":[]}\n", strings.Join(args, " "))
	case "noclient":
		fmt.Fprintln(os.Stderr, noClientLine)
		return 1
	case "reason":
		fmt.Fprintf(os.Stderr, "  %s  \nsecond line with details\n", longReason)
		return 2
	case "silent":
		return 3
	case "notjson":
		fmt.Println("not json")
	case "hang":
		self, err := os.Executable()
		if err != nil {
			return 4
		}
		child := exec.Command(self)
		child.Env = append(os.Environ(), fakeEnv+"=hold")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			return 4
		}
		hold()
		fmt.Println("{}")
	case "hold":
		hold()
	default:
		return 5
	}
	return 0
}

// hold returns once the file named by holdEnv is gone, or after 10 s.
func hold() {
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(os.Getenv(holdEnv)); err != nil {
			return
		}
	}
}

// fakeBridge returns a bridge that plays malachi-mcp in mode: the test
// binary itself (see TestMain).
func fakeBridge(t *testing.T, mode string) string {
	t.Helper()
	t.Setenv(fakeEnv, mode)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe
}

func TestQueryParsesReport(t *testing.T) {
	bridge := fakeBridge(t, "report")
	st, err := Query(context.Background(), bridge)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if st.Command != "/opt/malachi/bin/malachi-mcp" {
		t.Errorf("Command = %q", st.Command)
	}
	if len(st.Clients) != 2 {
		t.Fatalf("Clients = %+v", st.Clients)
	}
	want := Client{
		ID: "claude-code", Name: "Claude Code", Present: true, Registered: true,
		Path: "/home/u/.claude.json", Other: "/usr/local/bin/malachi-mcp",
	}
	if st.Clients[1] != want {
		t.Errorf("Clients[1] = %+v, want %+v", st.Clients[1], want)
	}
	if st.Clients[0].Other != "" || st.Clients[0].Registered {
		t.Errorf("Clients[0] = %+v", st.Clients[0])
	}
	if !st.Registered() {
		t.Error("Registered() = false with one registered client")
	}
}

func TestSubcommandsPassJSONFlag(t *testing.T) {
	// The bridge reports its own arguments as the command.
	bridge := fakeBridge(t, "args")
	for _, tc := range []struct {
		name string
		call func(context.Context, string) (Status, error)
		want string
	}{
		{"status", Query, "status --json"},
		{"install", Install, "install --json"},
		{"uninstall", Uninstall, "uninstall --json"},
	} {
		st, err := tc.call(context.Background(), bridge)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if st.Command != tc.want {
			t.Errorf("%s ran with %q, want %q", tc.name, st.Command, tc.want)
		}
		if st.Registered() {
			t.Errorf("%s: Registered() = true with no clients", tc.name)
		}
	}
}

func TestRegistered(t *testing.T) {
	for _, tc := range []struct {
		name string
		st   Status
		want bool
	}{
		{"empty", Status{}, false},
		{"present but not registered", Status{Clients: []Client{{Present: true}, {Present: true}}}, false},
		{"one registered", Status{Clients: []Client{{Present: true}, {Present: true, Registered: true}}}, true},
		{"all registered", Status{Clients: []Client{{Registered: true}, {Registered: true}}}, true},
	} {
		if got := tc.st.Registered(); got != tc.want {
			t.Errorf("%s: Registered() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestInstallNoClient(t *testing.T) {
	bridge := fakeBridge(t, "noclient")
	_, err := Install(context.Background(), bridge)
	if !errors.Is(err, ErrNoClient) {
		t.Fatalf("Install error = %v, want ErrNoClient", err)
	}
	if err.Error() != noClientLine {
		t.Errorf("Error() = %q, want the bridge's reason", err.Error())
	}
	var exit *ExitError
	if !errors.As(err, &exit) || exit.Code != 1 || exit.Subcommand != "install" {
		t.Errorf("error = %#v, want *ExitError{install, 1}", err)
	}
}

func TestExitReasonIsFirstLineBounded(t *testing.T) {
	bridge := fakeBridge(t, "reason")
	_, err := Uninstall(context.Background(), bridge)
	if err == nil {
		t.Fatal("Uninstall succeeded on exit 2")
	}
	if errors.Is(err, ErrNoClient) {
		t.Error("an unrelated reason matched ErrNoClient")
	}
	msg := err.Error()
	switch {
	case len(msg) > reasonLimit:
		t.Errorf("reason is %d bytes, cap is %d", len(msg), reasonLimit)
	case len(msg) != 198:
		t.Errorf("reason is %d bytes, want 198 (66 whole runes)", len(msg))
	case !utf8.ValidString(msg):
		t.Errorf("reason was cut inside a rune: %q", msg)
	case strings.ContainsAny(msg, "\n\r") || strings.Contains(msg, "second line"):
		t.Errorf("reason leaks past the first line: %q", msg)
	case !strings.HasPrefix(longReason, msg):
		t.Errorf("reason is not a prefix of the first line: %q", msg)
	}
}

func TestExitWithoutReason(t *testing.T) {
	bridge := fakeBridge(t, "silent")
	_, err := Query(context.Background(), bridge)
	if err == nil {
		t.Fatal("Query succeeded on exit 3")
	}
	if !strings.Contains(err.Error(), "status 3") {
		t.Errorf("Error() = %q, want the exit status", err.Error())
	}
	if errors.Is(err, ErrNoClient) {
		t.Error("an empty reason matched ErrNoClient")
	}
}

func TestBadReport(t *testing.T) {
	bridge := fakeBridge(t, "notjson")
	if _, err := Query(context.Background(), bridge); err == nil {
		t.Fatal("Query accepted a non-JSON report")
	}
}

func TestTimeoutKillsTheBridge(t *testing.T) {
	old := waitDelay
	waitDelay = 100 * time.Millisecond
	t.Cleanup(func() { waitDelay = old })

	// The bridge's child keeps the pipes open after the bridge itself is
	// killed: WaitDelay has to end the wait. Both stop holding when the
	// test is over and its temporary directory goes.
	holdFile := filepath.Join(t.TempDir(), "hold")
	if err := os.WriteFile(holdFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(holdEnv, holdFile)
	bridge := fakeBridge(t, "hang")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Install(ctx, bridge)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Install error = %v, want DeadlineExceeded", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("Install took %v after a 200 ms deadline", d)
	}
}

func TestMissingBridge(t *testing.T) {
	_, err := Query(context.Background(), filepath.Join(t.TempDir(), Name))
	if err == nil {
		t.Fatal("Query succeeded without an executable")
	}
	if errors.Is(err, ErrNoClient) || errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a missing executable was mapped to %v", err)
	}
}

// executableName is how this system spells a program called name, the file
// a PATH search finds: the bare name where a file runs by its execute
// permission (Unix), else name with the extension this test binary has
// (".exe" on Windows).
func executableName(t *testing.T, name string) string {
	t.Helper()
	probe := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(probe, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath(probe); err == nil {
		return name
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return name + filepath.Ext(self)
}

// writeProgram creates an empty file named name in dir with the given mode
// and returns its path; Locate only looks at it.
func writeProgram(t *testing.T, dir, name string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, nil, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil { // umask may have narrowed it
		t.Fatal(err)
	}
	return p
}

func TestLocate(t *testing.T) {
	old := executable
	t.Cleanup(func() { executable = old })

	t.Run("beside the executable", func(t *testing.T) {
		dir := t.TempDir()
		executable = func() (string, error) { return filepath.Join(dir, "malachi"), nil }
		want := writeProgram(t, dir, Name, 0o755)
		fi, err := os.Stat(want)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode()&0o111 == 0 {
			// Windows keeps no execute permission, which this rule reads.
			t.Skipf("the file system reports mode %v for a 0755 file", fi.Mode())
		}
		t.Setenv("PATH", t.TempDir())
		if got, err := Locate(); got != want || err != nil {
			t.Fatalf("Locate = %q, %v; want %q", got, err, want)
		}
	})
	t.Run("path", func(t *testing.T) {
		executable = func() (string, error) { return filepath.Join(t.TempDir(), "malachi"), nil }
		dir := t.TempDir()
		want := writeProgram(t, dir, executableName(t, Name), 0o755)
		t.Setenv("PATH", dir)
		if got, err := Locate(); got != want || err != nil {
			t.Fatalf("Locate = %q, %v; want %q", got, err, want)
		}
	})
	t.Run("not executable beside", func(t *testing.T) {
		dir := t.TempDir()
		executable = func() (string, error) { return filepath.Join(dir, "malachi"), nil }
		writeProgram(t, dir, Name, 0o644)
		t.Setenv("PATH", t.TempDir())
		if got, err := Locate(); err == nil {
			t.Fatalf("Locate = %q, want an error for a non-executable file", got)
		}
	})
	t.Run("missing", func(t *testing.T) {
		executable = func() (string, error) { return filepath.Join(t.TempDir(), "malachi"), nil }
		t.Setenv("PATH", t.TempDir())
		if got, err := Locate(); err == nil {
			t.Fatalf("Locate = %q, want an error", got)
		}
	})
}
