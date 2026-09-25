// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpsetup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// fakeBridge writes a shell script that plays malachi-mcp and returns its
// path. body runs with the bridge's arguments in $1, $2.
func fakeBridge(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), Name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

const report = `{"command":"/opt/malachi/bin/malachi-mcp","clients":[
{"id":"claude-desktop","name":"Claude Desktop","present":true,"registered":false,
 "path":"/home/u/.config/Claude/claude_desktop_config.json"},
{"id":"claude-code","name":"Claude Code","present":true,"registered":true,
 "path":"/home/u/.claude.json","other":"/usr/local/bin/malachi-mcp"}]}`

func TestQueryParsesReport(t *testing.T) {
	bridge := fakeBridge(t, "cat <<'EOF'\n"+report+"\nEOF\n")
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
	// The script reports its own arguments as the command.
	bridge := fakeBridge(t, `printf '{"command":"%s","clients":[]}\n' "$*"`+"\n")
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
	const line = "no Claude app found: neither Claude Desktop nor Claude Code is installed"
	bridge := fakeBridge(t, "echo '"+line+"' >&2\nexit 1\n")
	_, err := Install(context.Background(), bridge)
	if !errors.Is(err, ErrNoClient) {
		t.Fatalf("Install error = %v, want ErrNoClient", err)
	}
	if err.Error() != line {
		t.Errorf("Error() = %q, want the bridge's reason", err.Error())
	}
	var exit *ExitError
	if !errors.As(err, &exit) || exit.Code != 1 || exit.Subcommand != "install" {
		t.Errorf("error = %#v, want *ExitError{install, 1}", err)
	}
}

func TestExitReasonIsFirstLineBounded(t *testing.T) {
	// 100 three-byte runes: the 200-byte cap falls inside a rune. A rune
	// constant, so no tool on the way can turn the escape into a literal.
	first := strings.Repeat(string(rune(0x20AC)), 100) // U+20AC EURO SIGN
	bridge := fakeBridge(t, "printf '  %s  \\nsecond line with details\\n' '"+first+"' >&2\nexit 2\n")
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
	case !strings.HasPrefix(first, msg):
		t.Errorf("reason is not a prefix of the first line: %q", msg)
	}
}

func TestExitWithoutReason(t *testing.T) {
	bridge := fakeBridge(t, "exit 3\n")
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
	bridge := fakeBridge(t, "echo 'not json'\n")
	if _, err := Query(context.Background(), bridge); err == nil {
		t.Fatal("Query accepted a non-JSON report")
	}
}

func TestTimeoutKillsTheBridge(t *testing.T) {
	old := waitDelay
	waitDelay = 100 * time.Millisecond
	t.Cleanup(func() { waitDelay = old })

	// sleep is a child of the script and keeps the pipes open after the
	// script itself is killed: WaitDelay has to end the wait.
	bridge := fakeBridge(t, "sleep 5\necho '{}'\n")
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

func TestLocate(t *testing.T) {
	old := executable
	t.Cleanup(func() { executable = old })

	t.Run("beside the executable", func(t *testing.T) {
		dir := t.TempDir()
		executable = func() (string, error) { return filepath.Join(dir, "malachi"), nil }
		want := fakeBridge(t, "")
		if err := os.Rename(want, filepath.Join(dir, Name)); err != nil {
			t.Fatal(err)
		}
		want = filepath.Join(dir, Name)
		t.Setenv("PATH", t.TempDir())
		if got, err := Locate(); got != want || err != nil {
			t.Fatalf("Locate = %q, %v; want %q", got, err, want)
		}
	})
	t.Run("path", func(t *testing.T) {
		executable = func() (string, error) { return filepath.Join(t.TempDir(), "malachi"), nil }
		want := fakeBridge(t, "")
		t.Setenv("PATH", filepath.Dir(want))
		if got, err := Locate(); got != want || err != nil {
			t.Fatalf("Locate = %q, %v; want %q", got, err, want)
		}
	})
	t.Run("not executable beside", func(t *testing.T) {
		dir := t.TempDir()
		executable = func() (string, error) { return filepath.Join(dir, "malachi"), nil }
		if err := os.WriteFile(filepath.Join(dir, Name), []byte("#!/bin/sh\n"), 0o644); err != nil {
			t.Fatal(err)
		}
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
