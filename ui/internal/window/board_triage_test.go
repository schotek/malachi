// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/boardtriage"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// A snapshot of the board's source reaches the triage as the board.list
// behind it said it: the phase, the assistant preference, the queue and
// counts, and the last run as the daemon reported it (the source's
// convertRun turned around: started at Started, ended at Date unless it
// runs).
func TestTriageSnapshot(t *testing.T) {
	start := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	end := start.Add(4 * time.Minute)
	usage := &api.BoardUsageTotal{BoardUsage: api.BoardUsage{InputTokens: 1200, OutputTokens: 300}, Runs: 2}
	cases := []struct {
		name string
		in   board.Snapshot
		want boardtriage.Snapshot
	}{
		{"no run yet",
			board.Snapshot{Phase: board.PhasePreparing, Triage: board.TriageInfo{Queue: 3}},
			boardtriage.Snapshot{Phase: board.PhasePreparing, Triage: api.BoardTriage{Queue: 3}}},
		{"an ended run",
			board.Snapshot{
				Phase: board.PhaseReady, Annotated: true,
				Run: &board.Run{
					Model: "claude-code", Date: end, Started: start, Annotated: 5, Error: "timeout", Trigger: "auto",
				},
				Triage: board.TriageInfo{Queue: 4, AnnotatedToday: 12, Usage24h: usage},
			},
			boardtriage.Snapshot{Phase: board.PhaseReady, Assistant: true, Triage: api.BoardTriage{
				LastRun: &api.BoardRun{
					At: start, EndedAt: &end, Trigger: api.TriggerAuto, Source: "claude-code", Annotated: 5,
					Error: api.RunTimeout,
				},
				AnnotatedTodayAuto: 12, Queue: 4, Usage24h: usage,
			}}},
		{"a run under way",
			board.Snapshot{
				Phase: board.PhaseReady, Annotated: true,
				Run: &board.Run{Model: "claude-code", Date: start, Started: start, Running: true, Trigger: "manual"},
			},
			boardtriage.Snapshot{Phase: board.PhaseReady, Assistant: true, Triage: api.BoardTriage{
				LastRun: &api.BoardRun{At: start, Trigger: api.TriggerManual, Source: "claude-code"},
			}}},
		{"a board turned off",
			board.Snapshot{Phase: board.PhaseOff},
			boardtriage.Snapshot{Phase: board.PhaseOff}},
	}
	for _, c := range cases {
		if got := triageSnapshot(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: triageSnapshot = %+v, want %+v", c.name, got, c.want)
		}
	}
}

// A manual run's end is a toast, unless the user declined the consent; an
// automatic run's never is (the status strip says enough).
func TestManualRunToasts(t *testing.T) {
	cases := []struct {
		e    boardtriage.Ended
		want bool
	}{
		{boardtriage.Ended{Trigger: boardtriage.Manual}, true},
		{boardtriage.Ended{Trigger: boardtriage.Manual, Failed: true, Failure: board.FailTimeout}, true},
		{boardtriage.Ended{Trigger: boardtriage.Manual, Failed: true, Failure: board.FailCancelled}, true},
		{boardtriage.Ended{Trigger: boardtriage.Manual, Failed: true, Failure: board.FailDeclined}, false},
		{boardtriage.Ended{Trigger: boardtriage.Automatic}, false},
		{boardtriage.Ended{Trigger: boardtriage.Automatic, Failed: true, Failure: board.FailTimeout}, false},
	}
	for _, c := range cases {
		if got := manualRunToasts(c.e); got != c.want {
			t.Errorf("manualRunToasts(%+v) = %v, want %v", c.e, got, c.want)
		}
	}
}

// Token counts group their digits with a narrow no-break space, in every
// language.
func TestFormatTokens(t *testing.T) {
	for n, want := range map[int64]string{
		0:          "0",
		999:        "999",
		1000:       "1\u202f000",
		1234567:    "1\u202f234\u202f567",
		-12345:     "-12\u202f345",
		1000000000: "1\u202f000\u202f000\u202f000",
	} {
		if got := formatTokens(n); got != want {
			t.Errorf("formatTokens(%d) = %q, want %q", n, got, want)
		}
	}
	value, detail := boardtriage.UsageTexts(&api.BoardUsageTotal{BoardUsage: api.BoardUsage{InputTokens: 1234567}, Runs: 1}, formatTokens, i18n.Tr)
	if value != "1\u202f234\u202f567" || detail == "" {
		t.Errorf("UsageTexts with formatTokens = %q, %q", value, detail)
	}
}

// The board's triage is available exactly while the Assistant is shown
// (its menu on, the bridge registered) with the In App target.
func TestTriageAvailable(t *testing.T) {
	a := testAssistant()
	a.settings.SetAssistantMenu(true)
	a.settings.SetAssistantTarget(assistant.App)
	if a.triageAvailable() {
		t.Error("available before the bridge is known to be registered")
	}
	a.Apply(statusWith(false, true))
	if !a.triageAvailable() {
		t.Error("not available, shown with In App")
	}
	a.settings.SetAssistantTarget(assistant.Code)
	if a.triageAvailable() {
		t.Error("available with Claude Code in a terminal")
	}
	// A stored Claude Desktop reads as Claude Code here.
	a.settings.SetAssistantTarget(assistant.Desktop)
	if a.triageAvailable() {
		t.Error("available with Claude Desktop")
	}
	a.settings.SetAssistantTarget(assistant.App)
	a.settings.SetAssistantMenu(false)
	if a.triageAvailable() {
		t.Error("available with the Assistant turned off")
	}
}

// countingCaller counts the board.preferences calls and answers none of
// them (the loop drops the answers).
type countingCaller struct {
	mu    sync.Mutex
	calls int
	got   chan struct{}
}

func (c *countingCaller) Call(_ context.Context, method string, _, _ any) error {
	c.mu.Lock()
	if method == api.MethodBoardPreferences {
		c.calls++
	}
	c.mu.Unlock()
	c.got <- struct{}{}
	return nil
}

func (c *countingCaller) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// droppingLoop runs nothing it is given.
type droppingLoop struct{}

func (droppingLoop) Post(func())                 {}
func (droppingLoop) After(time.Duration, func()) {}

// The connection's state is fed by the window and by the poll: the
// preferences are asked for once per connection, and a state already known
// changes nothing.
func TestBoardTriageConnectionChanged(t *testing.T) {
	caller := &countingCaller{got: make(chan struct{}, 8)}
	b := &BoardTriage{prefs: boardtriage.NewPreferences(boardtriage.PreferencesConfig{
		Caller: caller, Loop: droppingLoop{}, Translator: i18n.Tr,
	})}
	// next waits for one more call (each load makes exactly one).
	next := func() {
		t.Helper()
		select {
		case <-caller.got:
		case <-time.After(5 * time.Second):
			t.Fatalf("no board.preferences call after %d", caller.count())
		}
	}
	b.ConnectionChanged(false)
	b.ConnectionChanged(true)
	b.ConnectionChanged(true)
	next()
	b.ConnectionChanged(false)
	b.ConnectionChanged(false)
	b.ConnectionChanged(true)
	next()
	b.stopped = true
	b.ConnectionChanged(false)
	b.ConnectionChanged(true)
	// Nothing more may come (a repeated state loading again would show
	// here too); give a stray call the time to show.
	select {
	case <-caller.got:
		t.Fatalf("a call after the triage stopped (%d in all)", caller.count())
	case <-time.After(50 * time.Millisecond):
	}
	if n := caller.count(); n != 2 {
		t.Errorf("%d board.preferences calls, want 2", n)
	}
}
