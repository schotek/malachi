// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package boardreply

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/ui/internal/assistant"
)

// Shared stand-ins for this package's tests: the main loop (testLoop), the
// settings in memory (memSettings) and the stand-in claude of
// ui/internal/assistantpanel's tests (copied: test files are not shared
// across packages, as ui/internal/boardtriage's harness_test.go notes). No
// real Claude Code, daemon or bridge is ever run: the bridge's path is only
// passed on.

// t0 is 2026-10-02 10:00:00 UTC.
var t0 = time.Unix(1_790_935_200, 0).UTC()

// identity translates nothing: the English msgids come back.
type identity struct{}

func (identity) T(msgid string) string { return msgid }
func (identity) N(singular, plural string, n int) string {
	if n == 1 {
		return singular
	}
	return plural
}
func (identity) C(_, msgid string) string { return msgid }

var tr identity

// testLoop is the main loop of a test: what is posted runs on the test's
// goroutine, in order, while runUntil waits; After waits in real time.
type testLoop struct {
	mu    sync.Mutex
	queue []func()
	wake  chan struct{}
}

func newTestLoop() *testLoop { return &testLoop{wake: make(chan struct{}, 1)} }

func (l *testLoop) Post(f func()) {
	l.mu.Lock()
	l.queue = append(l.queue, f)
	l.mu.Unlock()
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

func (l *testLoop) After(d time.Duration, f func()) {
	time.AfterFunc(d, func() { l.Post(f) })
}

// runUntil runs what was posted, one at a time, until cond holds; the test
// fails after ten seconds.
func (l *testLoop) runUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		l.mu.Lock()
		var f func()
		if len(l.queue) > 0 {
			f = l.queue[0]
			l.queue = l.queue[1:]
		}
		l.mu.Unlock()
		if f != nil {
			f()
			continue
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting")
		}
		select {
		case <-l.wake:
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// settle runs what is posted for d.
func (l *testLoop) settle(t *testing.T, d time.Duration) {
	t.Helper()
	end := time.Now().Add(d)
	l.runUntil(t, func() bool { return time.Now().After(end) })
}

// memSettings are the panel's preferences in memory.
type memSettings struct {
	mu         sync.Mutex
	model      assistant.Model
	consent    bool
	claudePath string
}

func (s *memSettings) AssistantModel() assistant.Model {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.model
}
func (s *memSettings) AssistantConsent() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.consent
}
func (s *memSettings) SetAssistantConsent(v bool) {
	s.mu.Lock()
	s.consent = v
	s.mu.Unlock()
}
func (s *memSettings) AssistantClaudePath() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.claudePath
}

// scratch is a fresh directory by its real path (as pwd -P prints it).
func scratch(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// writeScript writes an executable #!/bin/sh script.
func writeScript(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

// Canned stream-json.
const (
	fakeInit       = `{"type":"system","subtype":"init","mcp_servers":[{"name":"malachi","status":"connected"}],"tools":["mcp__malachi__read_message"],"model":"claude-sonnet"}`
	fakeInitFailed = `{"type":"system","subtype":"init","mcp_servers":[{"name":"malachi","status":"failed"}],"tools":[]}`
)

func fakeText(t string) string {
	return `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"` + t + `"}]}}`
}

func fakeToolUse(id, tool string) string {
	return `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"` + id + `","name":"mcp__malachi__` + tool + `","input":{}}]}}`
}

func fakeToolResult(id, text string, isError bool) string {
	return fmt.Sprintf(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"%s","is_error":%t,"content":[{"type":"text","text":"%s"}]}]}}`, id, isError, text)
}

func fakeResult(text string, success bool) string {
	return fmt.Sprintf(`{"type":"result","subtype":"success","is_error":%t,"result":"%s","total_cost_usd":0.01}`, !success, text)
}

// draftResultText is the bridge's create_draft result head
// (backend/cmd/malachi-mcp/tools_write.go).
func draftResultText(id, account string) string {
	return fmt.Sprintf("draft %s (version 1) stored in account %s; it is NOT sent.", id, account)
}

// fakeTurn is one turn of the fake: JSON lines, then an optional shell
// snippet (a sleep that keeps the turn open, an exit).
type fakeTurn struct {
	lines []string
	shell string
}

// draftTurn is a turn that reads the message, then creates draft id.
func draftTurn(id, account, shell string) fakeTurn {
	lines := []string{
		fakeInit, fakeToolUse("r", "read_message"), fakeToolResult("r", "the message", false),
		fakeToolUse("c", "create_draft"), fakeToolResult("c", draftResultText(id, account), false),
	}
	if shell == "" {
		lines = append(lines, fakeResult("", true))
	}
	return fakeTurn{lines: lines, shell: shell}
}

// fakeClaude is the stand-in claude of a test and what it recorded.
type fakeClaude struct {
	t         *testing.T
	dir, path string
}

// newFakeClaude writes the fake: loggedIn is what auth status --json says
// ("true", "false"); turns the turns in order, counted over every start
// (the second run's process gets turn 2); the last repeats.
func newFakeClaude(t *testing.T, loggedIn string, turns ...fakeTurn) *fakeClaude {
	t.Helper()
	dir := scratch(t)
	for i, turn := range turns {
		body := "cat <<'MALACHI_EOF'\n" + strings.Join(turn.lines, "\n") + "\nMALACHI_EOF\n" + turn.shell + "\n"
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("turn%d.sh", i+1)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	path := writeScript(t, filepath.Join(dir, "claude"), fmt.Sprintf(`D='%s'
case "$1" in
--version) echo '2.1.178 (Claude Code)'; exit 0;;
auth) echo '{"loggedIn": %s}'; exit 0;;
esac
echo start >> "$D/starts"
n=$(wc -l < "$D/starts" | tr -d ' ')
: > "$D/args"
for a in "$@"; do printf '%%s\n' "$a" >> "$D/args"; done
last=%d
k=$n
[ $k -gt $last ] && k=$last
while IFS= read -r line; do
  printf '%%s\n' "$line" >> "$D/stdin"
  . "$D/turn$k.sh"
done`, dir, loggedIn, len(turns)))
	return &fakeClaude{t: t, dir: dir, path: path}
}

func (f *fakeClaude) read(name string) string { return readFile(f.t, filepath.Join(f.dir, name)) }

func (f *fakeClaude) starts() int { return strings.Count(f.read("starts"), "\n") }

func (f *fakeClaude) args() []string {
	s := f.read("args")
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// prompts are the text of every turn written to stdin.
func (f *fakeClaude) prompts() []string {
	var out []string
	for _, line := range strings.Split(f.read("stdin"), "\n") {
		var turn struct {
			Message struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if line == "" || json.Unmarshal([]byte(line), &turn) != nil || len(turn.Message.Content) == 0 {
			continue
		}
		out = append(out, turn.Message.Content[0].Text)
	}
	return out
}
