// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package boardtriage

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistant"
)

// The stand-ins of the triage's tests: the main loop (testLoop, and
// manualLoop with a clock the test moves), a fake daemon (fakeDaemon:
// preferences, board.runStart, board.runEnd), the settings in memory and
// the stand-in claude of ui/internal/assistantpanel's tests (copied:
// test files are not shared across packages). No real Claude Code, daemon
// or bridge is ever run: the bridge's path is only passed on.

// t0 is 2026-10-01 10:00:00 UTC.
var t0 = time.Unix(1_790_848_800, 0).UTC()

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

func discardLog() *slog.Logger { return slog.New(slog.DiscardHandler) }

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

// manualLoop is a main loop whose clock only the test moves: After fires
// when advance passes its time, Post at once (nothing posts from another
// goroutine in its tests).
type manualLoop struct {
	now    time.Time
	seq    int
	timers []manualTimer
}

type manualTimer struct {
	at  time.Time
	seq int
	f   func()
}

func (l *manualLoop) Post(f func()) { f() }

func (l *manualLoop) After(d time.Duration, f func()) {
	l.seq++
	l.timers = append(l.timers, manualTimer{at: l.now.Add(d), seq: l.seq, f: f})
}

// advance moves the clock on by d, then fires every timer that is due by
// then, earliest first (also those the fired ones add).
func (l *manualLoop) advance(d time.Duration) {
	l.now = l.now.Add(d)
	for {
		sort.SliceStable(l.timers, func(i, j int) bool {
			if !l.timers[i].at.Equal(l.timers[j].at) {
				return l.timers[i].at.Before(l.timers[j].at)
			}
			return l.timers[i].seq < l.timers[j].seq
		})
		if len(l.timers) == 0 || l.timers[0].at.After(l.now) {
			return
		}
		f := l.timers[0].f
		l.timers = l.timers[1:]
		f()
	}
}

// memSettings are the triage's preferences in memory; changed (may be
// nil) is told of a change of a consent, as the window tells the
// controller of a GSettings change.
type memSettings struct {
	model, triageModel     assistant.Model
	consent, triageConsent bool
	claudePath             string
	changed                func()
}

func (s *memSettings) AssistantModel() assistant.Model { return assistant.ParseModel(string(s.model)) }
func (s *memSettings) AssistantConsent() bool          { return s.consent }
func (s *memSettings) AssistantClaudePath() string     { return s.claudePath }
func (s *memSettings) BoardTriageConsent() bool        { return s.triageConsent }
func (s *memSettings) BoardTriageModel() assistant.Model {
	return assistant.ParseModel(string(s.triageModel))
}
func (s *memSettings) SetAssistantConsent(v bool)   { s.set(&s.consent, v) }
func (s *memSettings) SetBoardTriageConsent(v bool) { s.set(&s.triageConsent, v) }
func (s *memSettings) set(key *bool, v bool) {
	if *key == v {
		return
	}
	*key = v
	if s.changed != nil {
		s.changed()
	}
}

// prefsWith are the daemon's default preferences changed by f.
func prefsWith(f func(*api.BoardPreferences)) api.BoardPreferences {
	p := api.DefaultBoardPreferences()
	if f != nil {
		f(&p)
	}
	return p
}

// hold keeps the replies of one method until released.
type hold struct {
	on      bool
	release chan struct{}
	waiting int
}

// fakeDaemon answers the board's preferences and runs from a script, from
// any goroutine.
type fakeDaemon struct {
	mu                                     sync.Mutex
	prefs                                  api.BoardPreferences
	getFailure, setFailure, runStartFailed error
	gets                                   int
	sets                                   []api.BoardPreferences
	runStarts                              []api.BoardRunStartParams
	runEnds                                []api.BoardRunEndParams
	holds                                  map[string]*hold
}

func newFakeDaemon(prefs api.BoardPreferences) *fakeDaemon {
	return &fakeDaemon{prefs: prefs, holds: map[string]*hold{}}
}

// hold keeps (on) or releases the replies of method.
func (d *fakeDaemon) hold(method string, on bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	h := d.holds[method]
	if h == nil {
		h = &hold{}
		d.holds[method] = h
	}
	if on && !h.on {
		h.on, h.release = true, make(chan struct{})
	}
	if !on && h.on {
		h.on = false
		close(h.release)
	}
}

// waiting is how many replies of method are held.
func (d *fakeDaemon) waiting(method string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	if h := d.holds[method]; h != nil {
		return h.waiting
	}
	return 0
}

// wait blocks while method's replies are held (called without the lock).
func (d *fakeDaemon) wait(ctx context.Context, method string) {
	d.mu.Lock()
	h := d.holds[method]
	if h == nil || !h.on {
		d.mu.Unlock()
		return
	}
	h.waiting++
	release := h.release
	d.mu.Unlock()
	select {
	case <-release:
	case <-ctx.Done():
	}
	d.mu.Lock()
	h.waiting--
	d.mu.Unlock()
}

func (d *fakeDaemon) Call(ctx context.Context, method string, params, result any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	var out any
	switch method {
	case api.MethodBoardPreferences:
		d.mu.Lock()
		d.gets++
		err, p := d.getFailure, d.prefs
		d.mu.Unlock()
		if err != nil {
			return err
		}
		out = api.BoardPreferencesResult{Preferences: p}
	case api.MethodBoardSetPreferences:
		var p api.BoardSetPreferencesParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		d.mu.Lock()
		d.sets = append(d.sets, p.Preferences)
		d.mu.Unlock()
		d.wait(ctx, method)
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.setFailure != nil {
			return d.setFailure
		}
		d.prefs = p.Preferences
		out = api.BoardSetPreferencesResult{Preferences: d.prefs}
	case api.MethodBoardRunStart:
		var p api.BoardRunStartParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		d.mu.Lock()
		d.runStarts = append(d.runStarts, p)
		n := len(d.runStarts)
		d.mu.Unlock()
		d.wait(ctx, method)
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.runStartFailed != nil {
			return d.runStartFailed
		}
		out = api.BoardRunStartResult{RunID: api.BoardRunID(fmt.Sprintf("run_%d", n))}
	case api.MethodBoardRunEnd:
		var p api.BoardRunEndParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		d.mu.Lock()
		d.runEnds = append(d.runEnds, p)
		d.mu.Unlock()
		d.wait(ctx, method)
		out = api.BoardRunEndResult{}
	default:
		return &api.Error{Code: api.CodeMethodNotFound, Message: method}
	}
	if result == nil {
		return nil
	}
	b, err := json.Marshal(out)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, result)
}

func (d *fakeDaemon) setPrefs(p api.BoardPreferences) { d.mu.Lock(); d.prefs = p; d.mu.Unlock() }
func (d *fakeDaemon) failGet(err error)               { d.mu.Lock(); d.getFailure = err; d.mu.Unlock() }
func (d *fakeDaemon) failSet(err error)               { d.mu.Lock(); d.setFailure = err; d.mu.Unlock() }
func (d *fakeDaemon) failRunStart(err error)          { d.mu.Lock(); d.runStartFailed = err; d.mu.Unlock() }

func (d *fakeDaemon) getCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.gets
}

func (d *fakeDaemon) setList() []api.BoardPreferences {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.sets)
}

func (d *fakeDaemon) starts() []api.BoardRunStartParams {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.runStarts)
}

func (d *fakeDaemon) ends() []api.BoardRunEndParams {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.runEnds)
}

// releaseAll lets every held reply go.
func (d *fakeDaemon) releaseAll() {
	for _, m := range []string{api.MethodBoardSetPreferences, api.MethodBoardRunStart, api.MethodBoardRunEnd} {
		d.hold(m, false)
	}
}

// samePrefList says whether two lists of preferences are the same.
func samePrefList(a, b []api.BoardPreferences) bool {
	return slices.EqualFunc(a, b, func(x, y api.BoardPreferences) bool { return samePrefs(&x, &y) })
}

// sameEnds says whether two lists of board.runEnd parameters are the same.
func sameEnds(a, b []api.BoardRunEndParams) bool {
	return slices.EqualFunc(a, b, func(x, y api.BoardRunEndParams) bool {
		if x.RunID != y.RunID || x.Error != y.Error || (x.Usage == nil) != (y.Usage == nil) {
			return false
		}
		return x.Usage == nil || *x.Usage == *y.Usage
	})
}

// The stand-in claude.

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

// fakeFailure is the message Claude Code writes itself when the API refused
// the turn; the result repeats its text.
func fakeFailure(failure, text string) string {
	return `{"type":"assistant","message":{"model":"<synthetic>","role":"assistant","content":[{"type":"text","text":"` + text + `"}]},"error":"` + failure + `"}`
}

func fakeResult(text string, success bool) string {
	return fmt.Sprintf(`{"type":"result","subtype":"success","is_error":%t,"result":"%s","total_cost_usd":0.01}`, !success, text)
}

// annotate is an annotate_case the bridge accepted (ok) or refused.
func annotate(id string, ok bool) []string {
	text := "annotated"
	if !ok {
		text = "conflict"
	}
	return []string{fakeToolUse(id, "annotate_case"), fakeToolResult(id, text, !ok)}
}

// annotateUsing is an annotate_case call in an API message msg that
// reports input input and read cache-read tokens (output 1, cache writes
// 10), with the bridge's acceptance.
func annotateUsing(id, msg string, input, read int) []string {
	return []string{
		fmt.Sprintf(`{"type":"assistant","message":{"id":"%s","role":"assistant","content":[{"type":"tool_use","id":"%s","name":"mcp__malachi__annotate_case","input":{}}],"usage":{"input_tokens":%d,"output_tokens":1,"cache_creation_input_tokens":10,"cache_read_input_tokens":%d}},"parent_tool_use_id":null}`, msg, id, input, read),
		fakeToolResult(id, "annotated", false),
	}
}

// resultUsing is a result line with its usage; success false is an error
// result.
func resultUsing(input, output, write, read int, success bool) string {
	subtype := "success"
	if !success {
		subtype = "error_during_execution"
	}
	return fmt.Sprintf(`{"type":"result","subtype":"%s","is_error":%t,"result":"ok","usage":{"input_tokens":%d,"output_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d}}`,
		subtype, !success, input, output, write, read)
}

// lines joins groups of lines.
func lines(groups ...[]string) []string {
	var out []string
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

// fakeTurn is one turn of the fake: JSON lines, then an optional shell
// snippet (a sleep that keeps the turn open, an exit).
type fakeTurn struct {
	lines []string
	shell string
}

// answerTurn is a plain answer.
func answerTurn(text string) fakeTurn {
	return fakeTurn{lines: []string{fakeInit, fakeText(text), fakeResult("done", true)}}
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
env > "$D/env"
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

// model is the --model of the last command line.
func (f *fakeClaude) model() string {
	args := f.args()
	if i := slices.Index(args, "--model"); i >= 0 && i+1 < len(args) {
		return args[i+1]
	}
	return ""
}
