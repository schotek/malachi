// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistantpanel

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/ui/internal/assistant"
)

// The panel against stand-in claude scripts (macOS: the same fakes in
// ClaudeCodeProcessTests and AssistantPanelControllerTests). No real Claude
// Code is ever run here.

// testLoop is the main loop of a test: what is posted runs on the test's
// goroutine, in order, while runUntil waits.
type testLoop struct {
	mu    sync.Mutex
	queue []func()
	wake  chan struct{}
}

func newTestLoop() *testLoop {
	return &testLoop{wake: make(chan struct{}, 1)}
}

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

// runUntil runs what was posted, one at a time, until cond holds; the
// test fails after ten seconds.
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
	model      assistant.Model
	consent    bool
	claudePath string
}

func (s *memSettings) AssistantModel() assistant.Model { return assistant.ParseModel(string(s.model)) }
func (s *memSettings) AssistantConsent() bool          { return s.consent }
func (s *memSettings) SetAssistantConsent(v bool)      { s.consent = v }
func (s *memSettings) AssistantClaudePath() string     { return s.claudePath }

// identity translates nothing: the English msgids come back.
type identity struct{}

func (identity) T(msgid string) string { return msgid }
func (identity) N(singular, plural string, n int) string {
	if n == 1 {
		return singular
	}
	return plural
}

var tr identity

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

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

func fakeDelta(t string) string {
	return `{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"` + t + `"}}}`
}

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

// fakeTurn is one turn of the fake: JSON lines, then an optional shell
// snippet (a sleep that keeps the turn open, an exit).
type fakeTurn struct {
	lines []string
	shell string
}

// answerTurn is a plain answer: init, two deltas, the whole text, the
// result.
func answerTurn(text string) fakeTurn {
	half := len(text) / 2
	return fakeTurn{lines: []string{fakeInit, fakeDelta(text[:half]), fakeDelta(text[half:]), fakeText(text), fakeResult("done", true)}}
}

// fakeClaude is the stand-in claude of a controller test and what it
// recorded.
type fakeClaude struct {
	t         *testing.T
	dir, path string
}

// newFakeClaude writes the fake: loggedIn is what auth status --json says
// ("true", "false"), onStart shell run at every start of a conversation
// with $n the start's number, turns the turns in order, counted over every
// start (the second question of a new process is turn 2); the last
// repeats.
func newFakeClaude(t *testing.T, loggedIn, onStart string, turns ...fakeTurn) *fakeClaude {
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
env > "$D/env"
pwd -P > "$D/cwd"
%s
last=%d
while IFS= read -r line; do
  printf '%%s\n' "$line" >> "$D/stdin"
  k=$(wc -l < "$D/stdin" | tr -d ' ')
  [ $k -gt $last ] && k=$last
  . "$D/turn$k.sh"
done`, dir, loggedIn, onStart, len(turns)))
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

func (f *fakeClaude) env() string { return f.read("env") }
func (f *fakeClaude) cwd() string { return strings.TrimSpace(f.read("cwd")) }

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

func (f *fakeClaude) lastPrompt() string {
	p := f.prompts()
	if len(p) == 0 {
		return ""
	}
	return p[len(p)-1]
}

// testBridge is the bridge the tests name; nothing ever runs it.
const testBridge = "/usr/libexec/malachi/malachi-mcp"

// harness is a Controller over a fake claude.
type harness struct {
	t        *testing.T
	loop     *testLoop
	fake     *fakeClaude
	settings *memSettings
	panel    *Controller
	work     string

	consentAsked  int
	consentAnswer bool
	restored      []string
	focused       int
	changes       []Change
	opened        []assistant.DraftRef
}

func newHarness(t *testing.T, fake *fakeClaude, consent bool, bridge string) *harness {
	t.Helper()
	loop := newTestLoop()
	s := &memSettings{consent: consent, claudePath: fake.path}
	work := filepath.Join(fake.dir, "work")
	loc := NewLocator(s, []string{"HOME=" + fake.dir}, loop, "", discardLog())
	loc.timeout = 5 * time.Second
	prefix := fake.dir + "/"
	loc.usable = func(p string) bool { return strings.HasPrefix(p, prefix) && IsExecutableFile(p) }
	panel := New(Config{
		Translator: tr, Settings: s, Locator: loc, Loop: loop, Log: discardLog(),
		Bridge: bridge, Socket: "/tmp/malachi-test.sock", Directory: work,
		Env:       []string{"HOME=" + fake.dir, "LANG=cs_CZ.UTF-8", "ANTHROPIC_API_KEY=sk-never"},
		KillGrace: 300 * time.Millisecond,
	})
	h := &harness{t: t, loop: loop, fake: fake, settings: s, panel: panel, work: work, consentAnswer: true}
	panel.Today = func() string { return "2026-09-29" }
	panel.Language = func() string { return "Czech" }
	panel.Consent = func(done func(bool)) {
		h.consentAsked++
		answer := h.consentAnswer
		loop.Post(func() { done(answer) })
	}
	panel.OnRestoreInput = func(text string) { h.restored = append(h.restored, text) }
	panel.OnFocusInput = func() { h.focused++ }
	panel.OnChange = func(c Change) { h.changes = append(h.changes, c) }
	panel.OpenDraft = func(ref assistant.DraftRef) { h.opened = append(h.opened, ref) }
	t.Cleanup(panel.Close)
	return h
}

func (h *harness) contents() []Content {
	out := make([]Content, len(h.panel.Items()))
	for i, it := range h.panel.Items() {
		out[i] = it.Content
	}
	return out
}

func (h *harness) last() Content {
	c := h.contents()
	if len(c) == 0 {
		return Content{}
	}
	return c[len(c)-1]
}

// turn waits until the turn under way ended.
func (h *harness) turn() {
	h.t.Helper()
	h.loop.runUntil(h.t, func() bool { return h.panel.Phase() != PhaseIdle })
	h.loop.runUntil(h.t, func() bool { return h.panel.Phase() == PhaseIdle })
}

// The contents of the transcript, briefly.
func user(label, text string) Content { return Content{Kind: ContentUser, Label: label, Text: text} }
func answer(text string, streaming bool) Content {
	return Content{Kind: ContentAssistant, Text: text, Streaming: streaming}
}
func activity(label string, done bool) Content {
	return Content{Kind: ContentActivity, Label: label, Done: done}
}
func failure(text string, retry bool) Content {
	return Content{Kind: ContentError, Text: text, Retry: retry}
}
func note(text string) Content { return Content{Kind: ContentNote, Text: text} }

// message is one message of account "a" as a context.
func message(id, subject, thread string) Context {
	return NewContext(assistant.Selection{AccountID: "a", MessageIDs: []string{id}}, 1, false, subject, thread)
}

// folded is a conversation of account "a": ids newest first, only the
// newest when partial.
func folded(ids []string, count int, partial bool, subject, thread string) Context {
	return NewContext(assistant.Selection{AccountID: "a", MessageIDs: ids}, count, partial, subject, thread)
}

func ptr[T any](v T) *T { return &v }

// mustPrompt is a message action's prompt for the panel.
func mustPrompt(t *testing.T, a assistant.Action, sel assistant.Selection) string {
	t.Helper()
	p, err := assistant.Prompt(tr, assistant.App, a, sel)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
