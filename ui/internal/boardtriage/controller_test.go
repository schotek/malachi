// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package boardtriage

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/assistantpanel"
	"github.com/schotek/malachi/ui/internal/board"
)

// The board's triage run (Controller) against the fake daemon and the
// stand-in claude (macOS BoardTriageControllerTests). No real Claude Code,
// daemon or bridge is ever run: the bridge's path is only passed on.

const testBridge = "/b/malachi-mcp"

// flagLocator is the application's locator with a switch for "found".
type flagLocator struct {
	*assistantpanel.Locator
	found *bool
}

func (l flagLocator) Locate() string {
	if !*l.found {
		return ""
	}
	return l.Locator.Locate()
}

// options of a harness: the zero value is consent given, the board's
// assistant preference on, the bridge there, the preferences loaded.
type options struct {
	noConsent bool
	prefs     *api.BoardPreferences
	noBridge  bool
	noLoad    bool
	// provider runs the request instead of the fake claude (the board's
	// Codex path).
	provider assistantpanel.Provider
	// bridge replaces testBridge when set.
	bridge string
}

type harness struct {
	t         *testing.T
	loop      *testLoop
	daemon    *fakeDaemon
	fake      *fakeClaude
	settings  *memSettings
	found     bool
	available bool
	prefs     *Preferences
	c         *Controller
	states    []State
	refreshes int
	asked     int
	ends      []Ended
}

func newHarness(t *testing.T, fake *fakeClaude, o options) *harness {
	t.Helper()
	prefs := prefsWith(func(p *api.BoardPreferences) { p.Assistant = true })
	if o.prefs != nil {
		prefs = *o.prefs
	}
	h := &harness{t: t, loop: newTestLoop(), daemon: newFakeDaemon(prefs), fake: fake, found: true, available: true}
	// The panel's model and the board's apart: the run takes the board's.
	h.settings = &memSettings{
		model: assistant.Haiku, triageModel: assistant.Opus, consent: !o.noConsent, triageConsent: !o.noConsent,
		claudePath: fake.path,
	}
	real := assistantpanel.NewLocator(h.settings, []string{"HOME=" + fake.dir}, h.loop, "", discardLog())
	request := assistantpanel.NewRequest(assistantpanel.RequestConfig{
		Settings: h.settings, Locator: real, Loop: h.loop, Log: discardLog(), Directory: filepath.Join(fake.dir, "work"),
		Env: []string{"HOME=" + fake.dir}, KillGrace: 300 * time.Millisecond,
	})
	if o.provider != nil {
		request = assistantpanel.NewRequest(assistantpanel.RequestConfig{
			Settings: h.settings, Locator: real, Loop: h.loop, Log: discardLog(), Directory: filepath.Join(fake.dir, "work"),
			Env: []string{"HOME=" + fake.dir}, KillGrace: 300 * time.Millisecond,
			Provider: func() assistantpanel.Provider { return o.provider },
		})
	}
	request.Timeout = 10 * time.Second
	h.prefs = newPrefs(h.daemon, h.loop)
	bridge := testBridge
	if o.bridge != "" {
		bridge = o.bridge
	}
	if o.noBridge {
		bridge = ""
	}
	h.c = New(Config{
		Caller: h.daemon, Settings: h.settings, Locator: flagLocator{real, &h.found}, Preferences: h.prefs,
		Request: request, Loop: h.loop, Log: discardLog(), Translator: tr, Bridge: bridge, Socket: "/s.sock",
		Available: func() bool { return h.available }, Location: time.UTC,
	})
	h.c.Now = func() time.Time { return t0 }
	h.c.Language = func() string { return "Czech" }
	h.c.Today = func() string { return "2026-10-01" }
	h.c.Timeout = 10 * time.Second
	h.c.OnRefresh = func() { h.refreshes++ }
	h.c.Observe(func() {
		if len(h.states) == 0 || !h.states[len(h.states)-1].Equal(h.c.State()) {
			h.states = append(h.states, h.c.State())
		}
	})
	h.c.ObserveEnded(func() {
		if e, ok := h.c.LastEnded(); ok {
			h.ends = append(h.ends, e)
		}
	})
	h.settings.changed = h.c.SettingsChanged
	if !o.noLoad {
		loadNow(t, h.loop, h.prefs)
		h.loop.runUntil(t, h.prefs.Idle)
	}
	t.Cleanup(func() {
		h.c.Cancel()
		h.c.Close()
		h.daemon.releaseAll()
	})
	return h
}

// board is a board.list with queue cases for the assistant.
func (h *harness) board(queue int, assistantOn bool, annotatedToday int, run *api.BoardRun) {
	h.c.BoardChanged(Snapshot{
		Phase: board.PhaseReady, Assistant: assistantOn,
		Triage: api.BoardTriage{Queue: queue, AnnotatedTodayAuto: annotatedToday, LastRun: run},
	})
}

// running waits until a run is running.
func (h *harness) running() {
	h.t.Helper()
	h.loop.runUntil(h.t, func() bool { return h.c.State().Kind == StateRunning })
}

// ended waits until the run ended and its board.runEnd was answered.
func (h *harness) ended() {
	h.t.Helper()
	h.loop.runUntil(h.t, func() bool { return !h.c.State().Active() && h.c.Idle() })
}

func (h *harness) expectState(name string, want State) {
	h.t.Helper()
	if got := h.c.State(); !got.Equal(want) {
		h.t.Errorf("%s: state %+v, want %+v", name, got, want)
	}
}

func (h *harness) expectEnds(name string, want ...api.BoardRunEndParams) {
	h.t.Helper()
	if got := h.daemon.ends(); !sameEnds(got, want) {
		h.t.Errorf("%s: board.runEnd %+v, want %+v", name, got, want)
	}
}

// update changes the preferences and waits for the daemon.
func (h *harness) update(change func(*api.BoardPreferences)) bool {
	h.t.Helper()
	return update(h.t, h.loop, h.prefs, false, change)
}

func end(id string, class api.BoardRunError, usage *api.BoardUsage) api.BoardRunEndParams {
	return api.BoardRunEndParams{RunID: api.BoardRunID(id), Error: class, Usage: usage}
}

func usage(input, output, write, read int64) *api.BoardUsage {
	return &api.BoardUsage{InputTokens: input, OutputTokens: output, CacheCreationInputTokens: write, CacheReadInputTokens: read}
}

// atLeast is u marked as a lower bound.
func atLeast(u *api.BoardUsage) *api.BoardUsage {
	u.LowerBound = true
	return u
}

func (h *harness) hasState(want State) bool {
	return slices.ContainsFunc(h.states, want.Equal)
}

// The triage's command line for run id with limit and its tools.
func triageArgs(id string, limit int, drafts bool) []string {
	return assistant.Args(assistant.Options{
		Bridge: testBridge, Socket: "/s.sock", Model: assistant.Opus,
		SystemPrompt: assistant.TriageSystemPrompt("Czech", "2026-10-01"),
		BridgeArgs:   assistant.TriageBridgeArgs(id, limit), Tools: assistant.TriageToolsWith(drafts),
	})
}

// A run

// A manual run: runStart, the request with the bridge for the run,
// progress from the accepted annotate_case calls, runEnd, a refresh.
func TestManualRun(t *testing.T) {
	fake := newFakeClaude(t, "true", fakeTurn{lines: lines(
		[]string{fakeInit, fakeToolUse("q", "list_triage_queue"), fakeToolResult("q", "3 cases", false)},
		annotate("a1", true), annotate("a2", false), annotate("a3", true),
		[]string{fakeText("Done."), fakeResult("Done.", true)})})
	h := newHarness(t, fake, options{})
	h.board(3, true, 0, nil)
	if !h.c.Start(Manual, 0) {
		t.Fatal("not started")
	}
	h.expectState("at once", Starting(Manual))
	h.ended()
	h.expectState("ended", Finished(Manual, 2, 1, t0))
	want := []State{Starting(Manual), Running(Manual, 0, 3), Running(Manual, 1, 3), Running(Manual, 2, 3), Finished(Manual, 2, 1, t0)}
	states := slices.DeleteFunc(slices.Clone(h.states), func(s State) bool { return s.Kind == StateIdle })
	if !slices.EqualFunc(states, want, State.Equal) {
		t.Errorf("states %+v", states)
	}
	if got := h.c.View().Result; got != "Triage finished: 2 conversations refined. The board refused 1 of the assistant’s notes." {
		t.Errorf("result %q", got)
	}
	if got := h.daemon.starts(); !slices.Equal(got, []api.BoardRunStartParams{{Trigger: api.TriggerManual, Source: "claude-code"}}) {
		t.Errorf("runStart %+v", got)
	}
	h.expectEnds("run", end("run_1", "", nil))
	if h.refreshes != 1 || len(h.ends) != 1 || h.ends[0] != (Ended{Trigger: Manual}) {
		t.Errorf("refreshes %d, ends %+v", h.refreshes, h.ends)
	}
	// The command line: the bridge for this run and its limit, the
	// triage's tools with create_draft.
	if got := fake.args(); !slices.Equal(got, triageArgs("run_1", 40, true)) {
		t.Errorf("args\n%q\nwant\n%q", got, triageArgs("run_1", 40, true))
	}
	if !strings.Contains(strings.Join(fake.args(), " "), "mcp__malachi__create_draft") {
		t.Error("no create_draft for a manual run")
	}
	if !slices.Equal(fake.prompts(), []string{assistant.TriageMessage(40, true)}) {
		t.Errorf("prompts %q", fake.prompts())
	}
	if h.c.SignedIn() != (assistantpanel.SignIn{Known: true, SignedIn: true}) {
		t.Errorf("signed in %+v", h.c.SignedIn())
	}
	// Nothing of consent was asked or written.
	if len(h.daemon.setList()) != 0 {
		t.Errorf("sets %+v", h.daemon.setList())
	}
}

// An automatic run asks for its limit; the progress counts against the
// smaller of the queue and the limit. It gets no create_draft and is told
// so.
func TestAutomaticRun(t *testing.T) {
	fake := newFakeClaude(t, "true", fakeTurn{lines: lines([]string{fakeInit}, annotate("a1", true), []string{fakeResult("ok", true)})})
	h := newHarness(t, fake, options{})
	h.board(12, true, 0, nil)
	if !h.c.Start(Automatic, 5) {
		t.Fatal("not started")
	}
	h.ended()
	if !h.hasState(Running(Automatic, 0, 5)) {
		t.Errorf("states %+v", h.states)
	}
	h.expectState("ended", Finished(Automatic, 1, 0, t0))
	if got := h.daemon.starts(); !slices.Equal(got, []api.BoardRunStartParams{{Trigger: api.TriggerAuto, Source: "claude-code"}}) {
		t.Errorf("runStart %+v", got)
	}
	if !slices.Equal(fake.prompts(), []string{assistant.TriageMessage(5, false)}) {
		t.Errorf("prompts %q", fake.prompts())
	}
	if got := fake.args(); !slices.Equal(got, triageArgs("run_1", 5, false)) || strings.Contains(strings.Join(got, " "), "create_draft") {
		t.Errorf("args %q", got)
	}
}

// The run's limit is a hard one: once the accepted notes reach it the run
// has succeeded, whatever the model does next (later tool calls are not
// counted); without a result in the grace period it ends so.
func TestLimitEndsTheRun(t *testing.T) {
	fake := newFakeClaude(t, "true", fakeTurn{
		lines: lines([]string{fakeInit}, annotate("a1", true), annotate("a2", false), annotate("a3", true),
			annotate("a4", true), annotate("a5", false)),
		shell: "sleep 30",
	})
	h := newHarness(t, fake, options{})
	h.c.Grace = 300 * time.Millisecond
	h.board(12, true, 0, nil)
	h.c.Start(Automatic, 2)
	h.ended()
	h.expectState("ended", Finished(Automatic, 2, 1, t0))
	for _, s := range h.states {
		if s.Kind == StateRunning && s.Done == 3 {
			t.Errorf("counted past the limit: %+v", h.states)
		}
	}
	h.expectEnds("run", end("run_1", "", nil))
	if len(h.ends) != 1 || h.ends[0].Failed || !h.c.SignedIn().SignedIn {
		t.Errorf("ends %+v, signed in %+v", h.ends, h.c.SignedIn())
	}
}

// The run takes the board's own model, not the panel's, read when it
// starts: a change applies to the next run, not to the one under way.
func TestBoardsOwnModel(t *testing.T) {
	fake := newFakeClaude(t, "true", fakeTurn{lines: []string{fakeInit}, shell: "sleep 0.3; echo '" + fakeResult("ok", true) + "'"})
	h := newHarness(t, fake, options{})
	h.board(3, true, 0, nil)
	h.c.Start(Manual, 0)
	h.loop.runUntil(t, func() bool { return fake.starts() == 1 && len(fake.args()) > 0 })
	h.settings.triageModel = assistant.Sonnet
	h.ended()
	if fake.model() != "opus" {
		t.Errorf("first run's model %q", fake.model())
	}
	h.expectState("first", Finished(Manual, 0, 0, t0))
	h.c.Start(Automatic, 3)
	h.ended()
	if fake.starts() != 2 || fake.model() != "sonnet" || h.settings.AssistantModel() != assistant.Haiku {
		t.Errorf("second run: starts %d, model %q", fake.starts(), fake.model())
	}
}

// A run that ends with no note accepted: refused notes fail it (both
// triggers); an automatic run whose queue had cases and that tried nothing
// fails as no progress (the schedule backs off); a manual run that tried
// nothing has simply finished.
func TestEmptyRuns(t *testing.T) {
	rows := []struct {
		name    string
		trigger Trigger
		lines   []string
		want    State
	}{
		{"refused, automatic", Automatic, lines(annotate("a1", false), annotate("a2", false)), Failed(Automatic, board.FailNotesRefused, t0)},
		{"refused, manual", Manual, annotate("a1", false), Failed(Manual, board.FailNotesRefused, t0)},
		{"nothing, automatic", Automatic, nil, Failed(Automatic, board.FailNoProgress, t0)},
		{"nothing, manual", Manual, nil, Finished(Manual, 0, 0, t0)},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			h := newHarness(t, newFakeClaude(t, "true", fakeTurn{lines: lines([]string{fakeInit}, r.lines, []string{fakeResult("ok", true)})}), options{})
			h.board(3, true, 0, nil)
			h.c.Start(r.trigger, 3)
			h.ended()
			h.expectState(r.name, r.want)
			var class api.BoardRunError
			if r.want.Kind == StateFailed {
				class = api.RunFailed
				if !CountsAsFailure(r.want.Failure) {
					t.Errorf("%s: no back-off", r.name)
				}
			}
			h.expectEnds(r.name, end("run_1", class, nil))
		})
	}
	if board.TriageFailed(board.FailNotesRefused, tr) != "Triage failed: the board refused the assistant’s notes." ||
		board.TriageFailed(board.FailNoProgress, tr) != "Triage failed: the assistant added no notes." {
		t.Error("the texts")
	}
}

// A queue the board has not reported with the assistant on is not known:
// the run asks for its limit and counts against it.
func TestUnknownQueue(t *testing.T) {
	h := newHarness(t, newFakeClaude(t, "true", fakeTurn{lines: []string{fakeInit, fakeResult("ok", true)}}), options{})
	h.board(0, false, 0, nil)
	if !h.c.Start(Manual, 0) {
		t.Fatal("not started")
	}
	h.ended()
	if !h.hasState(Running(Manual, 0, 40)) {
		t.Errorf("states %+v", h.states)
	}
	h.expectState("ended", Finished(Manual, 0, 0, t0))
}

// One run at a time.
func TestOneRunAtATime(t *testing.T) {
	fake := newFakeClaude(t, "true", fakeTurn{lines: lines([]string{fakeInit}, annotate("a1", true)), shell: "sleep 30"})
	h := newHarness(t, fake, options{})
	h.board(2, true, 0, nil)
	if !h.c.Start(Manual, 0) || h.c.Start(Manual, 0) || h.c.Start(Automatic, 3) {
		t.Fatal("a second run started")
	}
	h.loop.runUntil(t, func() bool { return h.c.State().Equal(Running(Manual, 1, 2)) })
	if h.c.Start(Manual, 0) || len(h.daemon.starts()) != 1 || fake.starts() != 1 {
		t.Errorf("runStarts %d, processes %d", len(h.daemon.starts()), fake.starts())
	}
}

// Stop: the request ends, the run is recorded as cancelled, the board
// asked again; nothing of the request comes later.
func TestCancel(t *testing.T) {
	fake := newFakeClaude(t, "true", fakeTurn{lines: lines([]string{fakeInit}, annotate("a1", true)), shell: "sleep 30; echo '" + fakeResult("late", true) + "'"})
	h := newHarness(t, fake, options{})
	h.board(4, true, 0, nil)
	h.c.Start(Manual, 0)
	h.loop.runUntil(t, func() bool { return h.c.State().Equal(Running(Manual, 1, 4)) })
	h.c.Cancel()
	h.expectState("cancelled", Failed(Manual, board.FailCancelled, t0))
	h.ended()
	h.expectEnds("cancelled", end("run_1", api.RunCancelled, nil))
	if h.refreshes != 1 {
		t.Errorf("refreshes %d", h.refreshes)
	}
	h.loop.settle(t, 200*time.Millisecond)
	h.expectState("later", Failed(Manual, board.FailCancelled, t0))
	if len(h.ends) != 1 {
		t.Errorf("ends %+v", h.ends)
	}
}

// Cancelled while board.runStart is on its way: the run it started is
// ended as cancelled, and no request starts.
func TestCancelWhileStarting(t *testing.T) {
	fake := newFakeClaude(t, "true", answerTurn("x"))
	h := newHarness(t, fake, options{})
	h.board(1, true, 0, nil)
	h.daemon.hold(api.MethodBoardRunStart, true)
	h.c.Start(Manual, 0)
	h.loop.runUntil(t, func() bool { return h.daemon.waiting(api.MethodBoardRunStart) == 1 })
	h.c.Cancel()
	h.daemon.hold(api.MethodBoardRunStart, false)
	h.loop.runUntil(t, func() bool { return len(h.daemon.ends()) == 1 })
	h.expectEnds("cancelled", end("run_1", api.RunCancelled, nil))
	h.expectState("cancelled", Failed(Manual, board.FailCancelled, t0))
	h.loop.settle(t, 100*time.Millisecond)
	if fake.starts() != 0 {
		t.Errorf("processes %d", fake.starts())
	}
}

// Failures

// What fails before the daemon's run starts records no run.
func TestFailuresBeforeTheRun(t *testing.T) {
	t.Run("Claude Code not found", func(t *testing.T) {
		h := newHarness(t, newFakeClaude(t, "true", answerTurn("x")), options{})
		h.found = false
		h.board(1, true, 0, nil)
		h.c.Start(Manual, 0)
		h.ended()
		h.expectState("not found", Failed(Manual, board.FailNotFound, t0))
		if len(h.daemon.starts()) != 0 {
			t.Error("a run started")
		}
	})
	t.Run("signed out", func(t *testing.T) {
		h := newHarness(t, newFakeClaude(t, "false", answerTurn("x")), options{})
		h.board(1, true, 0, nil)
		h.c.Start(Automatic, 3)
		h.ended()
		h.expectState("signed out", Failed(Automatic, board.FailNotSignedIn, t0))
		if h.c.SignedIn() != (assistantpanel.SignIn{Known: true}) || len(h.daemon.starts()) != 0 || h.fake.starts() != 0 {
			t.Errorf("signed in %+v, runStarts %d, processes %d", h.c.SignedIn(), len(h.daemon.starts()), h.fake.starts())
		}
	})
	t.Run("no bridge", func(t *testing.T) {
		h := newHarness(t, newFakeClaude(t, "true", answerTurn("x")), options{noBridge: true})
		h.c.Start(Manual, 0)
		h.ended()
		h.expectState("no bridge", Failed(Manual, board.FailToolsMissing, t0))
	})
	t.Run("the assistant off", func(t *testing.T) {
		h := newHarness(t, newFakeClaude(t, "true", answerTurn("x")), options{})
		h.available = false
		h.c.Start(Manual, 0)
		h.ended()
		h.expectState("off", Failed(Manual, board.FailAssistantOff, t0))
	})
	t.Run("nothing waits", func(t *testing.T) {
		h := newHarness(t, newFakeClaude(t, "true", answerTurn("x")), options{})
		h.board(0, true, 0, nil)
		h.c.Start(Manual, 0)
		h.ended()
		h.expectState("nothing", Failed(Manual, board.FailNothingToDo, t0))
		if len(h.daemon.starts()) != 0 {
			t.Error("a run started")
		}
	})
	t.Run("the daemon refuses the run", func(t *testing.T) {
		h := newHarness(t, newFakeClaude(t, "true", answerTurn("x")), options{})
		h.daemon.failRunStart(&api.Error{Code: api.CodeStorageError, Message: "disk"})
		h.board(1, true, 0, nil)
		h.c.Start(Manual, 0)
		h.ended()
		h.expectState("refused", Failed(Manual, board.FailBackend, t0))
		if len(h.daemon.ends()) != 0 || h.fake.starts() != 0 {
			t.Errorf("runEnds %d, processes %d", len(h.daemon.ends()), h.fake.starts())
		}
	})
}

// What fails during the run is recorded with its class.
func TestFailuresDuringTheRun(t *testing.T) {
	rows := []struct {
		name    string
		turn    fakeTurn
		failure board.TriageFailure
		class   api.BoardRunError
	}{
		{"the bridge not connected", fakeTurn{lines: []string{fakeInitFailed, fakeResult("x", true)}}, board.FailToolsMissing, api.RunFailed},
		{"an error result", fakeTurn{lines: []string{fakeInit, fakeResult("error_max_turns", false)}}, board.FailStopped, api.RunFailed},
		{"Claude Code exits", fakeTurn{lines: []string{fakeInit}, shell: "exit 3"}, board.FailStopped, api.RunFailed},
		{"the API refused the sign-in", fakeTurn{lines: []string{fakeInit, fakeFailure("authentication_failed", "Please run /login"), fakeResult("x", false)}},
			board.FailNotSignedIn, api.RunSignedOut},
		{"too long", fakeTurn{lines: []string{fakeInit}, shell: "sleep 30"}, board.FailTimeout, api.RunTimeout},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			h := newHarness(t, newFakeClaude(t, "true", r.turn), options{})
			// Only the run meant to time out gets a short timeout: a slow
			// start of the fake must not decide the others.
			if r.failure == board.FailTimeout {
				h.c.Timeout = 500 * time.Millisecond
			}
			h.board(2, true, 0, nil)
			h.c.Start(Automatic, 2)
			h.ended()
			h.expectState(r.name, Failed(Automatic, r.failure, t0))
			h.expectEnds(r.name, end("run_1", r.class, nil))
			if h.refreshes != 1 {
				t.Errorf("refreshes %d", h.refreshes)
			}
		})
	}
}

// Consent

// Without consent a manual run asks; declined, nothing is written or
// started; allowed, both consents are kept and the assistant preference
// goes on before the run.
func TestConsent(t *testing.T) {
	def := api.DefaultBoardPreferences()
	h := newHarness(t, newFakeClaude(t, "true", fakeTurn{lines: []string{fakeInit, fakeResult("ok", true)}}), options{noConsent: true, prefs: &def})
	if !h.c.NeedsConsent() || h.c.ConsentGiven() || !h.c.View().NeedsConsent {
		t.Fatal("consent given")
	}
	// No sheet: declined.
	h.c.Start(Manual, 0)
	h.ended()
	h.expectState("no sheet", Failed(Manual, board.FailDeclined, t0))
	// Declined in the sheet.
	answer := false
	h.c.Consent = func(done func(bool)) {
		h.asked++
		a := answer
		h.loop.Post(func() { done(a) })
	}
	h.c.Start(Manual, 0)
	h.ended()
	h.expectState("declined", Failed(Manual, board.FailDeclined, t0))
	if h.asked != 1 || h.settings.triageConsent || h.settings.consent || len(h.daemon.setList()) != 0 || len(h.daemon.starts()) != 0 {
		t.Errorf("asked %d, keys %v %v, sets %d, runStarts %d", h.asked, h.settings.triageConsent, h.settings.consent,
			len(h.daemon.setList()), len(h.daemon.starts()))
	}
	// An automatic run never asks.
	h.c.Start(Automatic, 3)
	h.ended()
	h.expectState("automatic", Failed(Automatic, board.FailDeclined, t0))
	if h.asked != 1 {
		t.Errorf("asked %d", h.asked)
	}
	// Allowed.
	answer = true
	h.c.Start(Manual, 0)
	h.ended()
	if h.asked != 2 || !h.settings.triageConsent || !h.settings.consent {
		t.Errorf("asked %d, keys %v %v", h.asked, h.settings.triageConsent, h.settings.consent)
	}
	want := prefsWith(func(p *api.BoardPreferences) { p.Assistant = true })
	if !samePrefList(h.daemon.setList(), []api.BoardPreferences{want}) || !h.c.ConsentGiven() || h.c.NeedsConsent() {
		t.Errorf("sets %+v", h.daemon.setList())
	}
	h.expectState("allowed", Finished(Manual, 0, 0, t0))
	if len(h.daemon.starts()) != 1 {
		t.Errorf("runStarts %d", len(h.daemon.starts()))
	}
}

// The panel's consent alone is not enough, nor the keys without the
// board's assistant preference.
func TestWhatConsentNeeds(t *testing.T) {
	def := api.DefaultBoardPreferences()
	h := newHarness(t, newFakeClaude(t, "true", answerTurn("x")), options{prefs: &def})
	if !h.c.NeedsConsent() {
		t.Error("no preference, no consent")
	}
	h.update(func(p *api.BoardPreferences) { p.Assistant = true })
	if !h.c.ConsentGiven() {
		t.Error("all three, no consent")
	}
	h.settings.SetBoardTriageConsent(false)
	if !h.c.NeedsConsent() {
		t.Error("the panel's alone is consent")
	}
}

// Withdrawing stops a run, drops the board's consent and turns the
// assistant preference off; the panel's consent stays.
func TestWithdraw(t *testing.T) {
	h := newHarness(t, newFakeClaude(t, "true", fakeTurn{lines: []string{fakeInit}, shell: "sleep 30"}), options{})
	h.board(2, true, 0, nil)
	h.c.Start(Manual, 0)
	h.running()
	h.c.WithdrawConsent()
	h.expectState("withdrawn", Failed(Manual, board.FailCancelled, t0))
	if h.settings.triageConsent || !h.settings.consent {
		t.Errorf("keys %v %v", h.settings.triageConsent, h.settings.consent)
	}
	h.loop.runUntil(t, func() bool { return len(h.daemon.setList()) == 1 && h.prefs.Idle() })
	want := prefsWith(func(p *api.BoardPreferences) { p.Assistant = false })
	if !samePrefList(h.daemon.setList(), []api.BoardPreferences{want}) || !h.c.NeedsConsent() {
		t.Errorf("sets %+v", h.daemon.setList())
	}
	h.ended()
}

// Withdrawing also turns automatic triage off: a consent given again later
// does not bring back runs the user did not turn on again.
func TestWithdrawTurnsAutomaticTriageOff(t *testing.T) {
	on := prefsWith(func(p *api.BoardPreferences) { p.Assistant, p.AutoTriage = true, true })
	h := newHarness(t, newFakeClaude(t, "true", answerTurn("x")), options{prefs: &on})
	h.c.WithdrawConsent()
	h.loop.runUntil(t, func() bool { return len(h.daemon.setList()) == 1 && h.prefs.Idle() })
	want := api.DefaultBoardPreferences()
	if !samePrefList(h.daemon.setList(), []api.BoardPreferences{want}) || h.c.WantsBoardData() {
		t.Errorf("sets %+v", h.daemon.setList())
	}
	if p, _ := h.prefs.Current(); p.AutoTriage {
		t.Error("automatic triage still on")
	}
}

// A board the daemon does not have, or has turned off, hides the triage;
// a board that lists again brings it back.
func TestBoardPhaseHidesTheTriage(t *testing.T) {
	h := newHarness(t, newFakeClaude(t, "true", answerTurn("x")), options{})
	if !h.c.View().Offered() {
		t.Fatal("not offered")
	}
	h.c.BoardChanged(Snapshot{Phase: board.PhaseUnsupported})
	if p, ok := h.c.BoardPhase(); !ok || p != board.PhaseUnsupported || h.c.View().Offered() {
		t.Errorf("unsupported: %d %v", p, h.c.View().Offered())
	}
	// Transient phases change nothing.
	h.c.BoardChanged(Snapshot{Phase: board.PhaseUnavailable})
	if h.c.View().Offered() {
		t.Error("offered after a transient phase")
	}
	h.board(1, true, 0, nil)
	if p, _ := h.c.BoardPhase(); p != board.PhaseReady || !h.c.View().Offered() {
		t.Errorf("ready: %d", p)
	}
	// Turned off in the daemon's preferences: hidden at once.
	h.update(func(p *api.BoardPreferences) { p.Enabled = false })
	if h.c.View().Offered() {
		t.Error("offered with the board off")
	}
	h.update(func(p *api.BoardPreferences) { p.Enabled = true })
	if !h.c.View().Offered() {
		t.Error("not offered with the board on")
	}
	// A snapshot of the board off, with preferences that say it is on
	// again (newer), does not hide it.
	h.c.BoardChanged(Snapshot{Phase: board.PhaseOff})
	if !h.c.View().Offered() {
		t.Error("hidden by an older snapshot")
	}
}

// The schedule's view of it

// The inputs the schedule reads.
func TestAutoInputs(t *testing.T) {
	on := prefsWith(func(p *api.BoardPreferences) {
		p.Assistant, p.AutoTriage, p.AutoTriageMinutes, p.AutoTriageDailyCases = true, true, 45, 7
	})
	h := newHarness(t, newFakeClaude(t, "true", answerTurn("x")), options{prefs: &on})
	endedAt := t0
	run := &api.BoardRun{At: t0.Add(-time.Minute), EndedAt: &endedAt, Trigger: api.TriggerAuto, Source: "claude-code"}
	h.board(3, true, 2, run)
	rev := h.c.BoardRevision()
	h.c.CheckSignIn()
	h.loop.runUntil(t, func() bool { return h.c.SignedIn().SignedIn })
	i := h.c.AutoInputs()
	if !i.Enabled || !i.Available || !i.SignedIn.SignedIn || !i.Consent || i.Running {
		t.Errorf("inputs %+v", i)
	}
	if i.Queue != 3 || i.AnnotatedToday != 2 || !i.CountedAt.Equal(t0) || i.Minutes != 45 || i.DailyCap != 7 ||
		!i.LastAttempt.Equal(t0.Add(-time.Minute)) {
		t.Errorf("inputs %+v", i)
	}
	// The same board again is no new revision; another queue is.
	h.board(3, true, 2, run)
	if h.c.BoardRevision() != rev {
		t.Error("a revision for the same board")
	}
	h.board(4, true, 2, run)
	if h.c.BoardRevision() != rev+1 {
		t.Error("no revision for another queue")
	}
	// A manual last run is no automatic attempt; a board that could not
	// be listed changes nothing.
	h.board(4, true, 0, &api.BoardRun{At: t0, EndedAt: &endedAt, Trigger: api.TriggerManual, Source: "m"})
	if !h.c.AutoInputs().LastAttempt.IsZero() {
		t.Error("a manual run is an attempt")
	}
	h.c.BoardChanged(Snapshot{Phase: board.PhaseUnavailable})
	if h.c.Board().Queue != 4 {
		t.Errorf("queue %d", h.c.Board().Queue)
	}
	// With the assistant off the queue counts as empty.
	h.board(4, false, 0, nil)
	if h.c.AutoInputs().Queue != 0 {
		t.Error("a queue with the assistant off")
	}
}

// The schedule with the real controller: one automatic run after the
// board's debounce, recorded as auto.
func TestScheduledRun(t *testing.T) {
	fake := newFakeClaude(t, "true", fakeTurn{lines: lines([]string{fakeInit}, annotate("a1", true), []string{fakeResult("ok", true)})})
	on := prefsWith(func(p *api.BoardPreferences) { p.Assistant, p.AutoTriage = true, true })
	h := newHarness(t, fake, options{prefs: &on})
	h.c.CheckSignIn()
	h.loop.runUntil(t, func() bool { return h.c.SignedIn().SignedIn })
	s := NewScheduler(h.c, h.loop, discardLog())
	s.Now = func() time.Time { return t0 }
	s.Location = time.UTC
	s.Debounce = 50 * time.Millisecond
	s.Start()
	t.Cleanup(s.Stop)
	if d, _ := s.Decision(); !d.Equal(off(OffEmptyQueue)) {
		t.Errorf("decision %+v", d)
	}
	h.board(2, true, 0, nil)
	if !s.Debouncing() {
		t.Error("not debouncing")
	}
	h.loop.runUntil(t, func() bool { return h.c.State().Equal(Finished(Automatic, 1, 0, t0)) && h.c.Idle() })
	if got := h.daemon.starts(); !slices.Equal(got, []api.BoardRunStartParams{{Trigger: api.TriggerAuto, Source: "claude-code"}}) {
		t.Errorf("runStart %+v", got)
	}
	if !slices.Equal(fake.prompts(), []string{assistant.TriageMessage(40, false)}) {
		t.Errorf("prompts %q", fake.prompts())
	}
	if !s.LastAttempt().Equal(t0) || s.Failures() != 0 {
		t.Errorf("last attempt %v, failures %d", s.LastAttempt(), s.Failures())
	}
}

// Losing what a run needs

// A run under way stops when triage stops being available, a consent
// goes, the board or its assistant preference goes off, and (an automatic
// one) when automatic triage is switched off; a manual run does not care
// about the switch.
func TestLosingWhatItNeeds(t *testing.T) {
	rows := []struct {
		name    string
		trigger Trigger
		stops   bool
		change  func(*harness)
	}{
		{"not available", Automatic, true, func(h *harness) {
			h.available = false
			h.c.AvailabilityChanged()
		}},
		{"the panel's consent withdrawn", Manual, true, func(h *harness) { h.settings.SetAssistantConsent(false) }},
		{"the board's consent withdrawn", Automatic, true, func(h *harness) { h.settings.SetBoardTriageConsent(false) }},
		{"the assistant preference off", Manual, true, func(h *harness) {
			h.prefs.Update(false, func(p *api.BoardPreferences) { p.Assistant = false }, nil)
		}},
		{"the board off", Manual, true, func(h *harness) {
			h.prefs.Update(false, func(p *api.BoardPreferences) { p.Enabled = false }, nil)
		}},
		{"automatic triage off", Automatic, true, func(h *harness) {
			h.prefs.Update(false, func(p *api.BoardPreferences) { p.AutoTriage = false }, nil)
		}},
		{"automatic triage off, a manual run", Manual, false, func(h *harness) {
			h.prefs.Update(false, func(p *api.BoardPreferences) { p.AutoTriage = false }, nil)
		}},
	}
	on := prefsWith(func(p *api.BoardPreferences) { p.Assistant, p.AutoTriage = true, true })
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			h := newHarness(t, newFakeClaude(t, "true", fakeTurn{lines: []string{fakeInit}, shell: "sleep 30"}), options{prefs: &on})
			h.board(2, true, 0, nil)
			h.c.Start(r.trigger, 2)
			h.running()
			r.change(h)
			if !r.stops {
				h.loop.runUntil(t, h.prefs.Idle)
				if !h.c.State().Active() {
					t.Errorf("stopped: %+v", h.c.State())
				}
				return
			}
			h.expectState(r.name, Failed(r.trigger, board.FailCancelled, t0))
			h.ended()
			h.expectEnds(r.name, end("run_1", api.RunCancelled, nil))
		})
	}
}

// The In App target

// Triage needs the In App target (TriageNeedsInAppTarget); a change of the
// availability is reported and hides the control.
func TestNeedsTheInAppTarget(t *testing.T) {
	if !TriageNeedsInAppTarget || !TriageAvailable(true, assistant.App) || TriageAvailable(true, assistant.Desktop) ||
		TriageAvailable(true, assistant.Code) || TriageAvailable(false, assistant.App) {
		t.Error("TriageAvailable")
	}
	h := newHarness(t, newFakeClaude(t, "true", answerTurn("x")), options{})
	reports := 0
	h.c.Observe(func() { reports++ })
	h.available = false
	h.c.AvailabilityChanged()
	if h.c.View().Control != ControlHidden || h.c.CanRun() || h.c.AutoInputs().Available || reports == 0 {
		t.Errorf("unavailable: control %d, reports %d", h.c.View().Control, reports)
	}
	h.available = true
	h.c.AvailabilityChanged()
	if h.c.View().Control == ControlHidden || !h.c.CanRun() {
		t.Errorf("available: control %d", h.c.View().Control)
	}
}

// Consent and the daemon

// Consent is kept only once the daemon stored the assistant preference: a
// refusal sets no key and is reported once (a run's own failure, or
// OnError from Preferences → AI).
func TestConsentOnlyOnceStored(t *testing.T) {
	def := api.DefaultBoardPreferences()
	h := newHarness(t, newFakeClaude(t, "true", answerTurn("x")), options{noConsent: true, prefs: &def})
	var errs []string
	h.prefs.OnError = func(s string) { errs = append(errs, s) }
	h.daemon.failSet(&api.Error{Code: api.CodeStorageError, Message: "disk"})
	h.c.Consent = func(done func(bool)) { h.loop.Post(func() { done(true) }) }
	h.c.Start(Manual, 0)
	h.ended()
	h.expectState("refused", Failed(Manual, board.FailBackend, t0))
	if h.settings.triageConsent || h.settings.consent || len(errs) != 0 {
		t.Errorf("keys %v %v, errors %q (the run reports it)", h.settings.triageConsent, h.settings.consent, errs)
	}
	// From Preferences: reported through OnError, no key either.
	give := func() bool {
		var got []bool
		h.c.GiveConsent(false, func(ok bool) { got = append(got, ok) })
		h.loop.runUntil(t, func() bool { return len(got) > 0 })
		return got[0]
	}
	if give() || h.settings.triageConsent || h.settings.consent || len(errs) != 1 {
		t.Errorf("from Preferences: keys %v %v, errors %q", h.settings.triageConsent, h.settings.consent, errs)
	}
	// Stored: both keys.
	h.daemon.failSet(nil)
	if !give() || !h.settings.triageConsent || !h.settings.consent {
		t.Errorf("stored: keys %v %v", h.settings.triageConsent, h.settings.consent)
	}
}

// A daemon that does not answer the board's preferences: a manual run
// fails without the sheet, and the control is unavailable after it, so the
// failure is not repeated on every click.
func TestNoPreferencesNoSheet(t *testing.T) {
	h := newHarness(t, newFakeClaude(t, "true", answerTurn("x")), options{noConsent: true, noLoad: true})
	h.daemon.failGet(&api.Error{Code: api.CodeMethodNotFound, Message: "no board"})
	h.c.Consent = func(done func(bool)) {
		h.asked++
		h.loop.Post(func() { done(true) })
	}
	h.c.Start(Manual, 0)
	h.ended()
	h.expectState("no preferences", Failed(Manual, board.FailBackend, t0))
	v := h.c.View()
	if h.asked != 0 || h.settings.triageConsent || v.Control != ControlUnavailable || v.Enabled ||
		v.ToolTip != "the mail backend did not answer" {
		t.Errorf("asked %d, view %+v", h.asked, v)
	}
}

// The daemon's assistant preference on while the board's consent is not
// given here (a withdrawal whose write failed) is turned off at the next
// load.
func TestStrayAssistantPreferenceIsRepaired(t *testing.T) {
	h := newHarness(t, newFakeClaude(t, "true", answerTurn("x")), options{noConsent: true, noLoad: true})
	h.daemon.setPrefs(prefsWith(func(p *api.BoardPreferences) { p.Assistant, p.AutoTriage = true, true }))
	h.prefs.ConnectionChanged(true)
	h.loop.runUntil(t, func() bool { return len(h.daemon.setList()) == 1 && h.prefs.Idle() })
	want := prefsWith(func(p *api.BoardPreferences) { p.AutoTriage = true })
	if !samePrefList(h.daemon.setList(), []api.BoardPreferences{want}) {
		t.Errorf("sets %+v", h.daemon.setList())
	}
	// With the consent given nothing is touched.
	h.settings.triageConsent = true
	h.daemon.setPrefs(prefsWith(func(p *api.BoardPreferences) { p.Assistant = true }))
	loadNow(t, h.loop, h.prefs)
	h.loop.runUntil(t, h.prefs.Idle)
	if len(h.daemon.setList()) != 1 {
		t.Errorf("sets %+v", h.daemon.setList())
	}
}

// Sign-in

// A sign-in check answered after a run learnt otherwise is dropped.
func TestLateSignInCheckIsDropped(t *testing.T) {
	fake := newFakeClaude(t, "true", answerTurn("x"))
	writeScript(t, fake.path, `D='`+fake.dir+`'
case "$1" in
--version) echo '2.1.178 (Claude Code)'; exit 0;;
auth) while [ ! -f "$D/release" ]; do sleep 0.02; done; echo '{"loggedIn": true}'; exit 0;;
esac
exit 1`)
	h := newHarness(t, fake, options{})
	h.c.CheckSignIn()
	h.c.learnSignedIn(assistantpanel.SignIn{Known: true})
	if err := os.WriteFile(filepath.Join(fake.dir, "release"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	h.loop.runUntil(t, func() bool { return h.c.signInAnswers == 1 })
	if h.c.SignedIn() != (assistantpanel.SignIn{Known: true}) {
		t.Errorf("signed in %+v", h.c.SignedIn())
	}
	// A check of its own still counts.
	h.c.CheckSignIn()
	h.loop.runUntil(t, func() bool { return h.c.signInAnswers == 2 })
	if !h.c.SignedIn().SignedIn {
		t.Errorf("signed in %+v", h.c.SignedIn())
	}
}

// Usage

// The result's usage is the run's, sent with board.runEnd.
func TestUsageFromTheResult(t *testing.T) {
	fake := newFakeClaude(t, "true", fakeTurn{lines: lines([]string{fakeInit}, annotateUsing("a1", "m1", 5, 100),
		[]string{resultUsing(40, 900, 1200, 50000, true)})})
	h := newHarness(t, fake, options{})
	h.board(3, true, 0, nil)
	h.c.Start(Manual, 0)
	h.ended()
	h.expectEnds("usage", end("run_1", "", usage(40, 900, 1200, 50000)))
}

// Without a result: the distinct API messages seen, each once, a lower
// bound. A cancelled
// run, a run stopped at its limit, one that timed out and one that quit
// (CancelAndEnd).
func TestUsageWithoutAResult(t *testing.T) {
	// Two lines of message m1 (counted once), one of m2.
	seen := lines([]string{fakeInit}, annotateUsing("a1", "m1", 5, 100), annotateUsing("a2", "m1", 5, 100), annotateUsing("a3", "m2", 7, 300))
	sum := atLeast(usage(12, 2, 20, 400))
	t.Run("cancelled", func(t *testing.T) {
		h := newHarness(t, newFakeClaude(t, "true", fakeTurn{lines: seen, shell: "sleep 30; echo '" + resultUsing(1, 1, 1, 1, true) + "'"}), options{})
		h.board(4, true, 0, nil)
		h.c.Start(Manual, 0)
		h.loop.runUntil(t, func() bool { return h.c.State().Equal(Running(Manual, 3, 4)) })
		h.c.Cancel()
		h.ended()
		h.expectEnds("cancelled", end("run_1", api.RunCancelled, sum))
	})
	t.Run("at its limit", func(t *testing.T) {
		// What the API messages reported, the one after the limit too.
		h := newHarness(t, newFakeClaude(t, "true", fakeTurn{lines: lines(seen, annotateUsing("a4", "m3", 1000, 1000)), shell: "sleep 30"}), options{})
		h.c.Grace = 300 * time.Millisecond
		h.board(12, true, 0, nil)
		h.c.Start(Automatic, 3)
		h.ended()
		h.expectEnds("limit", end("run_1", "", atLeast(usage(1012, 3, 30, 1400))))
	})
	t.Run("timed out", func(t *testing.T) {
		h := newHarness(t, newFakeClaude(t, "true", fakeTurn{lines: seen, shell: "sleep 30"}), options{})
		h.c.Timeout = 800 * time.Millisecond
		h.board(12, true, 0, nil)
		h.c.Start(Manual, 0)
		h.ended()
		h.expectEnds("timeout", end("run_1", api.RunTimeout, sum))
	})
	t.Run("quit", func(t *testing.T) {
		h := newHarness(t, newFakeClaude(t, "true", fakeTurn{lines: seen, shell: "sleep 30"}), options{})
		h.board(12, true, 0, nil)
		h.c.Start(Manual, 0)
		h.loop.runUntil(t, func() bool { return h.c.State().Equal(Running(Manual, 3, 12)) })
		h.c.CancelAndEnd(EndWait)
		h.expectEnds("quit", end("run_1", api.RunCancelled, sum))
	})
}

// At its limit the run waits for Claude Code's result and sends its usage,
// the whole run's; the run is a success, ended once, and a note the bridge
// refused after the limit is not counted.
func TestLimitWaitsForTheResult(t *testing.T) {
	fake := newFakeClaude(t, "true", fakeTurn{
		lines: lines([]string{fakeInit}, annotateUsing("a1", "m1", 5, 100), annotateUsing("a2", "m2", 7, 300)),
		shell: "sleep 0.3; echo '" + fakeToolUse("a3", "annotate_case") + "'; echo '" + fakeToolResult("a3", "limit", true) + "'; " +
			"echo '" + resultUsing(40, 2500, 1200, 50000, true) + "'",
	})
	h := newHarness(t, fake, options{})
	h.board(12, true, 0, nil)
	h.c.Start(Manual, 2)
	h.ended()
	if !h.hasState(Running(Manual, 2, 2)) {
		t.Errorf("states %+v", h.states)
	}
	h.expectState("ended", Finished(Manual, 2, 0, t0))
	h.expectEnds("usage", end("run_1", "", usage(40, 2500, 1200, 50000)))
	h.loop.settle(t, 200*time.Millisecond)
	if len(h.ends) != 1 || h.ends[0].Failed || len(h.daemon.ends()) != 1 {
		t.Errorf("ends %+v, runEnds %d", h.ends, len(h.daemon.ends()))
	}
}

// An error result after the limit does not turn the run into a failure;
// its usage still counts.
func TestErrorResultAfterTheLimit(t *testing.T) {
	fake := newFakeClaude(t, "true", fakeTurn{
		lines: lines([]string{fakeInit}, annotate("a1", true), annotate("a2", true)),
		shell: "sleep 0.3; echo '" + resultUsing(3, 700, 80, 9000, false) + "'",
	})
	h := newHarness(t, fake, options{})
	h.board(12, true, 0, nil)
	h.c.Start(Automatic, 2)
	h.ended()
	h.expectState("ended", Finished(Automatic, 2, 0, t0))
	h.expectEnds("usage", end("run_1", "", usage(3, 700, 80, 9000)))
	h.loop.settle(t, 200*time.Millisecond)
	if len(h.ends) != 1 || h.ends[0].Failed {
		t.Errorf("ends %+v", h.ends)
	}
}

// Stop while a run at its limit waits for its result ends it at once, as
// the success it is, with the usage seen so far.
func TestStopWhileWaitingForTheResult(t *testing.T) {
	fake := newFakeClaude(t, "true", fakeTurn{
		lines: lines([]string{fakeInit}, annotateUsing("a1", "m1", 5, 100), annotateUsing("a2", "m2", 7, 300)),
		shell: "sleep 30; echo '" + resultUsing(1, 1, 1, 1, true) + "'",
	})
	h := newHarness(t, fake, options{})
	h.c.Grace = time.Minute
	h.board(12, true, 0, nil)
	h.c.Start(Manual, 2)
	h.loop.runUntil(t, func() bool { return h.c.State().Equal(Running(Manual, 2, 2)) })
	h.c.Cancel()
	h.expectState("stopped", Finished(Manual, 2, 0, t0))
	h.ended()
	h.expectEnds("usage", end("run_1", "", atLeast(usage(12, 2, 20, 400))))
	h.loop.settle(t, 200*time.Millisecond)
	if len(h.ends) != 1 || h.ends[0].Failed || len(h.daemon.ends()) != 1 {
		t.Errorf("ends %+v, runEnds %d", h.ends, len(h.daemon.ends()))
	}
}

// A run that reported nothing sends no usage, and a new run does not carry
// the last one's; a result without usage leaves the messages' placeholders,
// a lower bound.
func TestNoUsageIsLeftOut(t *testing.T) {
	fake := newFakeClaude(t, "true",
		fakeTurn{lines: lines([]string{fakeInit}, annotateUsing("a1", "m1", 5, 100), []string{fakeResult("ok", true)})},
		fakeTurn{lines: lines([]string{fakeInit}, annotate("a2", true), []string{fakeResult("ok", true)})})
	h := newHarness(t, fake, options{})
	h.board(3, true, 0, nil)
	h.c.Start(Manual, 0)
	h.ended()
	h.c.Start(Manual, 0)
	h.ended()
	h.expectEnds("two runs", end("run_1", "", atLeast(usage(5, 1, 10, 100))), end("run_2", "", nil))
}

// The board's usage of the last 24 hours reaches the view; unknown before
// a board.list, "None" without usage.
func TestUsageInTheView(t *testing.T) {
	h := newHarness(t, newFakeClaude(t, "true", answerTurn("x")), options{})
	if h.c.View().UsageShown {
		t.Error("shown before a board.list")
	}
	h.board(1, true, 0, nil)
	if v := h.c.View(); !v.UsageShown || v.UsageValue != "None" || v.UsageDetail != "" {
		t.Errorf("no usage: %+v", v)
	}
	reports := 0
	h.c.Observe(func() { reports++ })
	h.c.BoardChanged(Snapshot{Phase: board.PhaseReady, Assistant: true, Triage: api.BoardTriage{
		Queue: 1, Usage24h: &api.BoardUsageTotal{BoardUsage: api.BoardUsage{InputTokens: 10, CacheReadInputTokens: 5000}, Runs: 2},
	}})
	if v := h.c.View(); reports != 1 || !v.UsageShown || !strings.HasSuffix(v.UsageDetail, "From 2 triage runs") {
		t.Errorf("reports %d, view %+v", reports, v)
	}
}

// For the views

// Quitting waits for board.runEnd, but never longer than its bound.
func TestCancelAndEnd(t *testing.T) {
	h := newHarness(t, newFakeClaude(t, "true", fakeTurn{lines: []string{fakeInit}, shell: "sleep 30"}), options{})
	h.board(2, true, 0, nil)
	h.c.Start(Manual, 0)
	h.running()
	h.c.CancelAndEnd(EndWait)
	// The daemon has the call when it returns.
	h.expectEnds("quit", end("run_1", api.RunCancelled, nil))
	h.ended()
	// A daemon that does not answer: it returns anyway.
	h.daemon.hold(api.MethodBoardRunEnd, true)
	h.c.Start(Manual, 0)
	h.running()
	started := time.Now()
	h.c.CancelAndEnd(200 * time.Millisecond)
	if time.Since(started) > 5*time.Second || h.daemon.waiting(api.MethodBoardRunEnd) != 1 {
		t.Errorf("took %v, waiting %d", time.Since(started), h.daemon.waiting(api.MethodBoardRunEnd))
	}
	h.daemon.hold(api.MethodBoardRunEnd, false)
	h.ended()
	// Nothing under way: it returns at once.
	h.c.CancelAndEnd(EndWait)
}

// The view is published again once a tick only while it names a relative
// time.
func TestClockOnlyForRelativeTimes(t *testing.T) {
	h := newHarness(t, newFakeClaude(t, "true", answerTurn("x")), options{})
	h.c.clockTick = 50 * time.Millisecond
	h.board(1, true, 0, nil)
	if h.c.View().RelativeTime || h.c.clockRunning {
		t.Error("a clock without a relative time")
	}
	endedAt := t0.Add(-5 * time.Minute)
	h.board(1, true, 0, &api.BoardRun{At: t0.Add(-6 * time.Minute), EndedAt: &endedAt, Trigger: api.TriggerAuto, Source: "claude-code"})
	if !h.c.View().RelativeTime || !h.c.clockRunning {
		t.Error("no clock for a relative time")
	}
	reports := 0
	h.c.Observe(func() { reports++ })
	h.loop.runUntil(t, func() bool { return reports >= 2 })
	if !h.c.clockRunning {
		t.Error("still relative: no next tick")
	}
	// No relative time any more: the clock stops.
	h.board(1, false, 0, nil)
	if h.c.View().RelativeTime || h.c.clockRunning {
		t.Error("a clock after the relative time went")
	}
	before := reports
	h.loop.settle(t, 200*time.Millisecond)
	if reports != before {
		t.Errorf("published after the clock stopped: %d", reports-before)
	}
}

// The board source should run while automatic triage is on, consent is
// given and triage can run.
func TestWantsBoardData(t *testing.T) {
	on := prefsWith(func(p *api.BoardPreferences) { p.Assistant, p.AutoTriage = true, true })
	h := newHarness(t, newFakeClaude(t, "true", answerTurn("x")), options{prefs: &on})
	if !h.c.WantsBoardData() {
		t.Error("all set")
	}
	h.available = false
	if h.c.WantsBoardData() {
		t.Error("unavailable")
	}
	h.available = true
	h.settings.SetBoardTriageConsent(false)
	if h.c.WantsBoardData() {
		t.Error("no consent")
	}
	h.settings.SetBoardTriageConsent(true)
	h.update(func(p *api.BoardPreferences) { p.AutoTriage = false })
	if h.c.WantsBoardData() {
		t.Error("automatic off")
	}
}

// Today's automatic count is in the view while automatic triage is on.
func TestTodayInTheView(t *testing.T) {
	on := prefsWith(func(p *api.BoardPreferences) { p.Assistant, p.AutoTriage = true, true })
	h := newHarness(t, newFakeClaude(t, "true", answerTurn("x")), options{prefs: &on})
	if v := h.c.View(); v.TodayShown || v.TodayLine != "" {
		t.Errorf("before a board.list: %+v", v)
	}
	h.board(1, true, 4, nil)
	if v := h.c.View(); !v.TodayShown || v.AnnotatedToday != 4 || v.TodayLine != "4 conversations triaged automatically today" {
		t.Errorf("today: %+v", v)
	}
	h.update(func(p *api.BoardPreferences) { p.AutoTriage = false })
	if v := h.c.View(); v.TodayShown || v.TodayLine != "" {
		t.Errorf("automatic off: %+v", v)
	}
}

// The pause the schedule sets reaches the view, and only a change is
// reported.
func TestAutoPauseInTheView(t *testing.T) {
	on := prefsWith(func(p *api.BoardPreferences) { p.Assistant, p.AutoTriage = true, true })
	h := newHarness(t, newFakeClaude(t, "true", answerTurn("x")), options{prefs: &on})
	reports := 0
	h.c.Observe(func() { reports++ })
	h.c.SetAutoPause(pause(board.PauseSignedOut))
	h.c.SetAutoPause(pause(board.PauseSignedOut))
	if reports != 1 || h.c.View().Paused != "Automatic triage paused: Claude Code is not signed in" {
		t.Errorf("reports %d, paused %q", reports, h.c.View().Paused)
	}
	h.c.SetAutoPause(nil)
	if reports != 2 || h.c.View().Paused != "" || h.c.AutoPause() != nil {
		t.Errorf("reports %d, paused %q", reports, h.c.View().Paused)
	}
}
