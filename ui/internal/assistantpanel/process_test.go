// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistantpanel

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/ui/internal/assistant"
)

const initLine = `{"type":"system","subtype":"init","mcp_servers":[{"name":"malachi","status":"connected"}],"tools":[]}`

// recorded is what a process reported.
type recorded struct {
	events []assistant.Event
	exits  []Exit
}

func (r *recorded) attach(p *Process) {
	p.OnEvents = func(e []assistant.Event) { r.events = append(r.events, e...) }
	p.OnExit = func(e Exit) { r.exits = append(r.exits, e) }
}

func (r *recorded) count(k assistant.EventKind) int {
	n := 0
	for _, e := range r.events {
		if e.Kind == k {
			n++
		}
	}
	return n
}

// makeProcess is a process over a script whose body gets the test's
// directory.
func makeProcess(t *testing.T, body func(dir string) string) (*Process, string, *recorded, *testLoop) {
	t.Helper()
	dir := scratch(t)
	exe := writeScript(t, filepath.Join(dir, "claude"), body(dir))
	loop := newTestLoop()
	p := NewProcess(loop, discardLog(), exe, []string{"-p", "--verbose"}, []string{"HOME=" + dir, "PATH=/usr/bin:/bin"}, dir, 300*time.Millisecond)
	r := &recorded{}
	r.attach(p)
	t.Cleanup(p.Terminate)
	return p, dir, r, loop
}

// Each stdin line is a turn: its events arrive in order, one process keeps
// them all, and the end is reported once.
func TestProcessTurnsArriveInOrder(t *testing.T) {
	p, dir, r, loop := makeProcess(t, func(dir string) string {
		return fmt.Sprintf(`t=0
while IFS= read -r line; do
  t=$((t+1))
  printf '%%s\n' "$line" >> '%s/stdin'
  echo '%s'
  echo '{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"turn '$t'"}}}'
  echo 'not json at all'
  echo '{"type":"assistant","message":{"content":[{"type":"text","text":"turn '$t' done"}]}}'
  echo '{"type":"result","subtype":"success","is_error":false,"result":"ok '$t'"}'
done`, dir, initLine)
	})
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	if !p.Running() {
		t.Fatal("not running after Start")
	}
	for i := 1; i <= 3; i++ {
		if !p.Send(assistant.UserMessage(fmt.Sprintf("question %d", i))) {
			t.Fatalf("Send %d refused", i)
		}
	}
	loop.runUntil(t, func() bool { return r.count(assistant.EventResult) == 3 })
	var texts []string
	for _, e := range r.events {
		if e.Kind == assistant.EventText {
			texts = append(texts, e.Text)
		}
	}
	if want := []string{"turn 1 done", "turn 2 done", "turn 3 done"}; !slices.Equal(texts, want) {
		t.Errorf("texts %q, want %q", texts, want)
	}
	var kinds []assistant.EventKind
	for _, e := range r.events[:4] {
		kinds = append(kinds, e.Kind)
	}
	if want := []assistant.EventKind{assistant.EventInit, assistant.EventTextDelta, assistant.EventText, assistant.EventResult}; !slices.Equal(kinds, want) {
		t.Errorf("first kinds %v, want %v", kinds, want)
	}
	stdin := readFile(t, filepath.Join(dir, "stdin"))
	if strings.Count(stdin, "\n") != 3 || !strings.Contains(stdin, "question 2") {
		t.Errorf("stdin %q", stdin)
	}
	if len(r.exits) != 0 {
		t.Fatalf("exits before Terminate: %v", r.exits)
	}
	p.Terminate()
	loop.runUntil(t, func() bool { return len(r.exits) > 0 })
	loop.settle(t, 100*time.Millisecond)
	if len(r.exits) != 1 || p.Running() {
		t.Errorf("exits %v, running %v", r.exits, p.Running())
	}
	if p.Send(assistant.UserMessage("late")) {
		t.Error("Send after the end taken")
	}
	p.Terminate() // no second report
	loop.settle(t, 100*time.Millisecond)
	if len(r.exits) != 1 {
		t.Errorf("exits %v after a second Terminate", r.exits)
	}
}

// An early exit reports the status and stderr's first line, after the
// events it printed.
func TestProcessEarlyExitReportsStderr(t *testing.T) {
	p, _, r, loop := makeProcess(t, func(string) string {
		return fmt.Sprintf(`echo '%s'
echo 'Error: Invalid API key · Please run /login' >&2
echo 'second line' >&2
exit 3`, initLine)
	})
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	loop.runUntil(t, func() bool { return len(r.exits) > 0 })
	if want := (Exit{Status: 3, Reason: "Error: Invalid API key · Please run /login"}); !slices.Equal(r.exits, []Exit{want}) {
		t.Errorf("exits %v, want %v", r.exits, want)
	}
	if len(r.events) != 1 || r.events[0].Kind != assistant.EventInit {
		t.Errorf("events %v", r.events)
	}
	if p.Ended() == nil || p.Ended().Status != 3 {
		t.Errorf("Ended %v", p.Ended())
	}
	if p.Send([]byte("x")) {
		t.Error("Send after the end taken")
	}
}

// Many lines and then the exit: every event comes first.
func TestProcessEveryEventBeforeTheExit(t *testing.T) {
	p, _, r, loop := makeProcess(t, func(string) string {
		return `i=0
while [ $i -lt 200 ]; do
  i=$((i+1))
  echo '{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"'$i' "}}}'
done`
	})
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	loop.runUntil(t, func() bool { return len(r.exits) > 0 })
	if len(r.events) != 200 {
		t.Fatalf("%d events, want 200", len(r.events))
	}
	for i, e := range r.events {
		if want := fmt.Sprintf("%d ", i+1); e.Text != want {
			t.Fatalf("event %d text %q, want %q", i, e.Text, want)
		}
	}
	if !slices.Equal(r.exits, []Exit{{Status: 0}}) || r.exits[0].Description() != "claude exited with status 0" {
		t.Errorf("exits %v", r.exits)
	}
}

// stderr is kept bounded, the reason is its first line cut at 400 bytes.
func TestProcessStderrIsBounded(t *testing.T) {
	p, _, r, loop := makeProcess(t, func(string) string {
		return `i=0
while [ $i -lt 2000 ]; do i=$((i+1)); printf 'xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx' >&2; done
exit 1`
	})
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	loop.runUntil(t, func() bool { return len(r.exits) > 0 })
	if r.exits[0].Status != 1 || len(r.exits[0].Reason) != reasonLimit {
		t.Errorf("exit %+v (reason %d bytes)", r.exits[0], len(r.exits[0].Reason))
	}
}

// A line longer than the limit is dropped; the lines around it are not.
func TestProcessDropsAnOverlongLine(t *testing.T) {
	dir := scratch(t)
	big := filepath.Join(dir, "big")
	if err := os.WriteFile(big, []byte(`{"x":"`+strings.Repeat("y", maxLine)+`"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, _, r, loop := makeProcess(t, func(string) string {
		return fmt.Sprintf(`echo '%s'
cat '%s'
echo '{"type":"result","subtype":"success","is_error":false,"result":"ok"}'`, initLine, big)
	})
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	loop.runUntil(t, func() bool { return len(r.exits) > 0 })
	if len(r.events) != 2 || r.events[0].Kind != assistant.EventInit || r.events[1].Kind != assistant.EventResult {
		t.Errorf("events %v", r.events)
	}
}

// A process that ignores SIGTERM is killed after the grace, and its end is
// still reported once.
func TestProcessSigkillAfterTheGrace(t *testing.T) {
	p, _, r, loop := makeProcess(t, func(string) string {
		return fmt.Sprintf(`trap '' TERM
echo '%s'
while :; do sleep 0.1; done`, initLine)
	})
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	loop.runUntil(t, func() bool { return len(r.events) == 1 })
	started := time.Now()
	p.Terminate()
	loop.runUntil(t, func() bool { return len(r.exits) > 0 })
	if r.exits[0].Status != -9 {
		t.Errorf("status %d, want -9", r.exits[0].Status)
	}
	if time.Since(started) < 250*time.Millisecond {
		t.Errorf("killed after %v, before the grace", time.Since(started))
	}
	loop.settle(t, 200*time.Millisecond)
	if len(r.exits) != 1 {
		t.Errorf("exits %v", r.exits)
	}
}

// SIGTERM ends a process waiting for its next turn.
func TestProcessSigtermEndsAWaitingProcess(t *testing.T) {
	p, _, r, loop := makeProcess(t, func(string) string {
		return fmt.Sprintf(`echo '%s'
while IFS= read -r line; do :; done
sleep 5`, initLine)
	})
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	loop.runUntil(t, func() bool { return len(r.events) == 1 })
	p.Terminate()
	loop.runUntil(t, func() bool { return len(r.exits) > 0 })
	// stdin closed first: the loop may end before the signal lands.
	if s := r.exits[0].Status; s != -15 && s != 0 {
		t.Errorf("status %d, want -15 or 0", s)
	}
}

// The environment is exactly the one given, the working directory the
// private one.
func TestProcessEnvironmentAndDirectory(t *testing.T) {
	dir := scratch(t)
	exe := writeScript(t, filepath.Join(dir, "claude"), fmt.Sprintf(`env > '%s/env'
pwd -P > '%s/cwd'`, dir, dir))
	work := filepath.Join(dir, "work")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	env := assistant.ChildEnv([]string{"HOME=" + dir, "ANTHROPIC_API_KEY=sk-test", "CLAUDECODE=1", "LANG=cs_CZ.UTF-8"}, exe)
	loop := newTestLoop()
	p := NewProcess(loop, discardLog(), exe, nil, env, work, DefaultKillGrace)
	r := &recorded{}
	r.attach(p)
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	loop.runUntil(t, func() bool { return len(r.exits) > 0 })
	seen := readFile(t, filepath.Join(dir, "env"))
	for _, want := range []string{"HOME=" + dir + "\n", "LANG=cs_CZ.UTF-8\n", "PATH=" + dir + ":/usr/bin:/bin:/usr/sbin:/sbin\n"} {
		if !strings.Contains(seen, want) {
			t.Errorf("environment lacks %q:\n%s", want, seen)
		}
	}
	if strings.Contains(seen, "ANTHROPIC") || strings.Contains(seen, "CLAUDECODE") {
		t.Errorf("environment leaks:\n%s", seen)
	}
	if cwd := strings.TrimSpace(readFile(t, filepath.Join(dir, "cwd"))); cwd != work {
		t.Errorf("cwd %q, want %q", cwd, work)
	}
}

func TestProcessLaunchFailure(t *testing.T) {
	dir := scratch(t)
	p := NewProcess(newTestLoop(), discardLog(), filepath.Join(dir, "missing"), nil, nil, dir, DefaultKillGrace)
	if err := p.Start(); err == nil {
		t.Fatal("Start of a missing executable succeeded")
	}
	if p.Running() || p.Send([]byte("x")) {
		t.Error("a process that never started runs")
	}
}
