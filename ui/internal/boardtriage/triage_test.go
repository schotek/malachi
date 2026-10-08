// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package boardtriage

import (
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/assistantpanel"
	"github.com/schotek/malachi/ui/internal/board"
)

// The Triage control and the status strip (ViewOf; macOS
// BoardTriageViewTests). The texts themselves are ui/internal/board's.

var (
	signedIn   = assistantpanel.SignIn{Known: true, SignedIn: true}
	signedOutS = assistantpanel.SignIn{Known: true}
)

// in are the inputs of a ready triage: shown, Claude Code and the bridge
// there, signed in, the board's notes on; change adjusts them.
func in(change func(*ViewInputs)) ViewInputs {
	i := ViewInputs{Shown: true, ClaudeFound: true, Bridge: true, SignedIn: signedIn, AssistantOn: true, Now: t0}
	if change != nil {
		change(&i)
	}
	return i
}

func view(change func(*ViewInputs)) View { return ViewOf(in(change), tr) }

// ranAgo is a finished automatic run that ended d before t0.
func ranAgo(d time.Duration) *api.BoardRun {
	end := t0.Add(-d)
	return &api.BoardRun{At: end.Add(-time.Minute), EndedAt: &end, Trigger: api.TriggerAuto, Source: "claude-code"}
}

func pause(kind board.PauseKind) *board.Pause { return &board.Pause{Kind: kind} }

func failedPause(f board.TriageFailure, until time.Time) *board.Pause {
	return &board.Pause{Kind: board.PauseFailed, Failure: f, Until: until}
}

func TestViewControl(t *testing.T) {
	stop := assistant.PanelTexts(tr).Stop
	rows := []struct {
		name    string
		change  func(*ViewInputs)
		control Control
		title   string
		enabled bool
	}{
		{"ready", nil, ControlTriage, "✦ Triage", true},
		{"sign-in not known", func(i *ViewInputs) { i.SignedIn = assistantpanel.SignIn{} }, ControlTriage, "✦ Triage", true},
		{"needs consent: still Triage", func(i *ViewInputs) { i.NeedsConsent = true }, ControlTriage, "✦ Triage", true},
		{"the assistant off", func(i *ViewInputs) { i.Shown = false }, ControlHidden, "", false},
		{"no Claude Code", func(i *ViewInputs) { i.ClaudeFound = false }, ControlGetClaudeCode, assistant.SignInTexts(tr).GetClaudeCode, true},
		{"no bridge", func(i *ViewInputs) { i.Bridge = false }, ControlUnavailable, "✦ Triage", false},
		{"no preferences from the daemon", func(i *ViewInputs) { i.BackendFailed = true }, ControlUnavailable, "✦ Triage", false},
		{"running wins over no preferences", func(i *ViewInputs) { i.State = Starting(Manual); i.BackendFailed = true }, ControlStop, stop, true},
		{"signed out", func(i *ViewInputs) { i.SignedIn = signedOutS }, ControlSignIn, assistant.SignInTexts(tr).SignIn, true},
		{"running", func(i *ViewInputs) { i.State = Running(Manual, 1, 3) }, ControlStop, stop, true},
		{"starting", func(i *ViewInputs) { i.State = Starting(Automatic) }, ControlStop, stop, true},
		{"running wins over a sign-in lost meanwhile", func(i *ViewInputs) {
			i.SignedIn = signedOutS
			i.State = Running(Automatic, 0, 1)
		}, ControlStop, stop, true},
	}
	for _, r := range rows {
		i := in(r.change)
		v := ViewOf(i, tr)
		if v.Control != r.control || v.Title != r.title || v.Enabled != r.enabled || v.NeedsConsent != i.NeedsConsent {
			t.Errorf("%s: %+v", r.name, v)
		}
	}
	if got := view(func(i *ViewInputs) { i.Bridge = false }).ToolTip; got != "the Malachi Mail tools are not available to the assistant" {
		t.Errorf("no bridge: tooltip %q", got)
	}
}

func TestViewProgressAndResult(t *testing.T) {
	v := view(func(i *ViewInputs) { i.State = Running(Manual, 2, 12) })
	if !v.Running || v.Progress != "Triaging… 2 of 12" || v.Result != "" || v.StatusLine != "The assistant is triaging the board… 2 of 12" {
		t.Errorf("running: %+v", v)
	}
	// More accepted than expected (the queue grew) never shows more.
	if v := view(func(i *ViewInputs) { i.State = Running(Manual, 5, 3) }); v.Progress != "Triaging… 3 of 3" {
		t.Errorf("more than expected: %q", v.Progress)
	}
	v = view(func(i *ViewInputs) { i.State = Starting(Manual) })
	if v.Progress != "Starting the triage…" || v.StatusLine != "The assistant is triaging the board…" {
		t.Errorf("starting: %+v", v)
	}
	results := []struct {
		state State
		want  string
	}{
		{Finished(Manual, 4, 0, t0), "Triage finished: 4 conversations refined."},
		{Finished(Manual, 1, 0, t0), "Triage finished: 1 conversation refined."},
		{Finished(Automatic, 0, 0, t0), "Triage finished. No conversation needed new notes."},
		{Failed(Manual, board.FailCancelled, t0), "Triage stopped."},
		{Failed(Manual, board.FailNotSignedIn, t0), "Triage failed: Claude Code is not signed in."},
		{Finished(Manual, 3, 2, t0), "Triage finished: 3 conversations refined. The board refused 2 of the assistant’s notes."},
		{Failed(Automatic, board.FailNotesRefused, t0), "Triage failed: the board refused the assistant’s notes."},
	}
	for _, r := range results {
		v := view(func(i *ViewInputs) { i.State = r.state })
		if v.Running || v.Progress != "" || v.Result != r.want {
			t.Errorf("%+v: %+v", r.state, v)
		}
	}
	if got := board.AutoTriagePaused(*failedPause(board.FailNoProgress, t0.Add(time.Hour)), t0, tr); got != "Automatic triage paused: the assistant added no notes · next try in 1 hour" {
		t.Errorf("no progress: %q", got)
	}
}

func TestViewStatusLine(t *testing.T) {
	if got := view(func(i *ViewInputs) { i.AssistantOn = false }).StatusLine; got != "Sorted by the daemon’s rules · assistant off" {
		t.Errorf("assistant off: %q", got)
	}
	if got := view(nil).StatusLine; got != "Triaged by rules · not refined by the assistant yet" {
		t.Errorf("not yet: %q", got)
	}
	run := ranAgo(5 * time.Minute)
	if got := view(func(i *ViewInputs) { i.LastRun = run }).StatusLine; got != "Triaged by rules · refined by the assistant 5 minutes ago" {
		t.Errorf("refined: %q", got)
	}
	// A run still open has not refined it yet.
	open := &api.BoardRun{At: t0.Add(-time.Minute), Trigger: api.TriggerManual}
	if got := view(func(i *ViewInputs) { i.LastRun = open }).StatusLine; got != "Triaged by rules · not refined by the assistant yet" {
		t.Errorf("open run: %q", got)
	}
	// Without the in-app assistant: no "assistant off" at all; notes
	// another client wrote still have their line.
	if got := view(func(i *ViewInputs) { i.Shown, i.AssistantOn = false, false }).StatusLine; got != "" {
		t.Errorf("not shown, off: %q", got)
	}
	if got := view(func(i *ViewInputs) { i.Shown, i.LastRun = false, run }).StatusLine; got != "Triaged by rules · refined by the assistant 5 minutes ago" {
		t.Errorf("not shown, on: %q", got)
	}
}

// Whether the view names a time that goes stale.
func TestViewRelativeTime(t *testing.T) {
	run := ranAgo(5 * time.Minute)
	until := t0.Add(time.Hour)
	rows := []struct {
		name   string
		change func(*ViewInputs)
		want   bool
	}{
		{"refined", func(i *ViewInputs) { i.LastRun = run }, true},
		{"never", nil, false},
		{"notes off", func(i *ViewInputs) { i.AssistantOn, i.LastRun = false, run }, false},
		{"running", func(i *ViewInputs) { i.State, i.LastRun = Running(Manual, 1, 2), run }, false},
		{"paused till later", func(i *ViewInputs) { i.AutoTriage, i.Pause = true, failedPause(board.FailTimeout, until) }, true},
		{"paused, signed out", func(i *ViewInputs) { i.AutoTriage, i.Pause = true, pause(board.PauseSignedOut) }, false},
	}
	for _, r := range rows {
		if got := view(r.change).RelativeTime; got != r.want {
			t.Errorf("%s: %v", r.name, got)
		}
	}
}

// Today's automatic count, while automatic triage is on.
func TestViewToday(t *testing.T) {
	v := view(func(i *ViewInputs) { i.AutoTriage, i.AnnotatedToday, i.TodayKnown = true, 1, true })
	if !v.TodayShown || v.AnnotatedToday != 1 || v.TodayLine != "1 conversation triaged automatically today" {
		t.Errorf("one: %+v", v)
	}
	v = view(func(i *ViewInputs) { i.AutoTriage, i.AnnotatedToday, i.TodayKnown = true, 12, true })
	if v.TodayLine != "12 conversations triaged automatically today" {
		t.Errorf("twelve: %q", v.TodayLine)
	}
	v = view(func(i *ViewInputs) { i.AnnotatedToday, i.TodayKnown = 12, true })
	if v.TodayShown || v.TodayLine != "" {
		t.Errorf("automatic off: %+v", v)
	}
	v = view(func(i *ViewInputs) { i.AutoTriage = true })
	if v.TodayShown || v.TodayLine != "" {
		t.Errorf("not known: %+v", v)
	}
}

func TestViewPaused(t *testing.T) {
	until := t0.Add(time.Hour)
	rows := []struct {
		name   string
		change func(*ViewInputs)
		want   string
	}{
		{"failed", func(i *ViewInputs) { i.AutoTriage, i.Pause = true, failedPause(board.FailTimeout, until) },
			"Automatic triage paused: it took too long · next try in 1 hour"},
		{"signed out", func(i *ViewInputs) { i.AutoTriage, i.Pause = true, pause(board.PauseSignedOut) },
			"Automatic triage paused: Claude Code is not signed in"},
		{"no consent", func(i *ViewInputs) { i.AutoTriage, i.Pause = true, pause(board.PauseNoConsent) },
			"Automatic triage paused: sending mail to the assistant is not allowed"},
		{"unavailable", func(i *ViewInputs) { i.AutoTriage, i.Pause = true, pause(board.PauseUnavailable) },
			"Automatic triage paused: the assistant cannot run"},
		// Not with automatic triage off, nor while a run works, nor with
		// the assistant hidden.
		{"automatic off", func(i *ViewInputs) { i.Pause = pause(board.PauseSignedOut) }, ""},
		{"a run", func(i *ViewInputs) {
			i.State, i.AutoTriage, i.Pause = Starting(Manual), true, pause(board.PauseSignedOut)
		}, ""},
		{"hidden", func(i *ViewInputs) { i.Shown, i.AutoTriage, i.Pause = false, true, pause(board.PauseSignedOut) }, ""},
	}
	for _, r := range rows {
		if got := view(r.change).Paused; got != r.want {
			t.Errorf("%s: %q", r.name, got)
		}
	}
}

func TestRunErrorsAndWire(t *testing.T) {
	if RunError(board.FailCancelled) != api.RunCancelled || RunError(board.FailTimeout) != api.RunTimeout ||
		RunError(board.FailNotSignedIn) != api.RunSignedOut {
		t.Error("the classes")
	}
	for _, f := range []board.TriageFailure{board.FailNotFound, board.FailToolsMissing, board.FailDeclined, board.FailAssistantOff,
		board.FailBackend, board.FailStopped, board.FailNothingToDo, board.FailNotesRefused, board.FailNoProgress} {
		if RunError(f) != api.RunFailed {
			t.Errorf("%d: %q", f, RunError(f))
		}
	}
	if Wire(Manual) != api.TriggerManual || Wire(Automatic) != api.TriggerAuto {
		t.Error("Wire")
	}
	// Every failure has its words.
	for _, f := range board.TriageFailures {
		if board.TriageFailureText(f, tr) == "" {
			t.Errorf("failure %d has no words", f)
		}
	}
}

// A board the daemon has turned off, or does not have (an older daemon),
// offers no triage: one rule for the toolbar and Preferences.
func TestViewBoardGone(t *testing.T) {
	run := ranAgo(5 * time.Minute)
	for _, phase := range []board.Phase{board.PhaseOff, board.PhaseUnsupported} {
		v := view(func(i *ViewInputs) {
			i.LastRun, i.AutoTriage, i.Pause = run, true, pause(board.PauseSignedOut)
			i.AnnotatedToday, i.TodayKnown, i.BoardPhase = 3, true, phase
		})
		if v.Control != ControlHidden || v.Offered() || v.Enabled || v.Title != "" {
			t.Errorf("phase %d: %+v", phase, v)
		}
		if v.StatusLine != "" || v.Paused != "" || v.TodayLine != "" || v.RelativeTime {
			t.Errorf("phase %d: lines %+v", phase, v)
		}
		// A run under way keeps its Stop.
		if c := view(func(i *ViewInputs) { i.State, i.BoardPhase = Starting(Manual), phase }).Control; c != ControlStop {
			t.Errorf("phase %d, a run: %d", phase, c)
		}
	}
	for _, phase := range []board.Phase{board.PhaseReady, board.PhasePreparing, board.PhaseLoading, board.PhaseFailed, board.PhaseUnavailable} {
		if c := view(func(i *ViewInputs) { i.BoardPhase = phase }).Control; c != ControlTriage {
			t.Errorf("phase %d: %d", phase, c)
		}
	}
	if !view(nil).Offered() || view(func(i *ViewInputs) { i.Shown = false }).Offered() {
		t.Error("Offered")
	}
}

// While the application's sign-in waits for the browser, the control
// cannot start a second one and says why.
func TestViewSigningIn(t *testing.T) {
	waiting := assistant.SignInTexts(tr).Waiting
	v := view(func(i *ViewInputs) { i.SignedIn, i.SigningIn = signedOutS, true })
	if v.Control != ControlSignIn || v.Enabled || !v.SigningIn || v.Title != assistant.SignInTexts(tr).SignIn || v.ToolTip != waiting {
		t.Errorf("signing in: %+v", v)
	}
	// Also while a sign-in started elsewhere replaces a known one.
	if v := view(func(i *ViewInputs) { i.SigningIn = true }); v.Control != ControlSignIn || v.Enabled {
		t.Errorf("signed in, signing in: %+v", v)
	}
	// Without it: Sign In… can be clicked.
	if v := view(func(i *ViewInputs) { i.SignedIn = signedOutS }); v.Control != ControlSignIn || !v.Enabled || v.SigningIn {
		t.Errorf("signed out: %+v", v)
	}
	// A run, a missing Claude Code or bridge come first.
	rows := []struct {
		change func(*ViewInputs)
		want   Control
	}{
		{func(i *ViewInputs) { i.State = Starting(Manual) }, ControlStop},
		{func(i *ViewInputs) { i.ClaudeFound = false }, ControlGetClaudeCode},
		{func(i *ViewInputs) { i.Bridge = false }, ControlUnavailable},
		{func(i *ViewInputs) { i.Shown = false }, ControlHidden},
	}
	for n, r := range rows {
		if c := view(func(i *ViewInputs) { r.change(i); i.SigningIn = true }).Control; c != r.want {
			t.Errorf("row %d: %d", n, c)
		}
	}
	v = view(func(i *ViewInputs) { i.SignedIn, i.SigningIn = signedOutS, true })
	if got := StripText(v, true, board.PhaseReady, tr); got != waiting {
		t.Errorf("strip: %q", got)
	}
}

// The status strip's note by the window's mode and the board's phase.
func TestStripText(t *testing.T) {
	run := ranAgo(5 * time.Minute)
	idle := view(func(i *ViewInputs) { i.LastRun = run })
	running := view(func(i *ViewInputs) { i.State = Running(Manual, 1, 4) })
	starting := view(func(i *ViewInputs) { i.State = Starting(Automatic) })
	paused := view(func(i *ViewInputs) { i.LastRun, i.AutoTriage, i.Pause = run, true, pause(board.PauseNoConsent) })
	line := "Triaged by rules · refined by the assistant 5 minutes ago"
	type row struct {
		name    string
		v       View
		inBoard bool
		phase   board.Phase
		want    string
	}
	rows := []row{
		{"idle in Mail", idle, false, board.PhaseReady, ""},
		{"running in Mail", running, false, board.PhaseReady, "Triaging… 1 of 4"},
		{"idle", idle, true, board.PhaseReady, line},
		{"running", running, true, board.PhaseReady, "Triaging… 1 of 4"},
		{"starting", starting, true, board.PhaseReady, "Starting the triage…"},
		{"paused", paused, true, board.PhaseReady, "Automatic triage paused: sending mail to the assistant is not allowed"},
	}
	for _, phase := range []board.Phase{board.PhaseOff, board.PhaseUnsupported} {
		rows = append(rows,
			row{"idle, no board", idle, true, phase, ""},
			row{"paused, no board", paused, true, phase, ""},
			row{"running, no board", running, true, phase, "Triaging… 1 of 4"})
	}
	for _, r := range rows {
		if got := StripText(r.v, r.inBoard, r.phase, tr); got != r.want {
			t.Errorf("%s (phase %d): %q", r.name, r.phase, got)
		}
	}
}

// The conversations that wait for the assistant, after the line they
// belong to.
func TestViewWaiting(t *testing.T) {
	run := ranAgo(5 * time.Minute)
	refined := "Triaged by rules · refined by the assistant 5 minutes ago"
	notYet := "Triaged by rules · not refined by the assistant yet"
	// Unknown or empty: nothing added.
	for _, known := range []bool{false, true} {
		v := view(func(i *ViewInputs) { i.LastRun, i.QueueKnown = run, known })
		if v.Waiting != "" || v.StatusLine != refined {
			t.Errorf("queue known %v, empty: %+v", known, v)
		}
	}
	queue := func(n int) func(*ViewInputs) {
		return func(i *ViewInputs) { i.Queue, i.QueueKnown = n, true }
	}
	both := func(fs ...func(*ViewInputs)) func(*ViewInputs) {
		return func(i *ViewInputs) {
			for _, f := range fs {
				f(i)
			}
		}
	}
	withRun := func(i *ViewInputs) { i.LastRun = run }
	// Idle, refined or not yet, in the strip and in Preferences.
	v := view(both(withRun, queue(1)))
	if v.Waiting != "1 conversation waits for the assistant" || v.StatusLine != refined+" · 1 conversation waits for the assistant" {
		t.Errorf("idle: %+v", v)
	}
	if StripText(v, true, board.PhaseReady, tr) != v.StatusLine || StripText(v, false, board.PhaseReady, tr) != "" {
		t.Error("idle strip")
	}
	if v := view(queue(40)); v.StatusLine != notYet+" · 40 conversations wait for the assistant" {
		t.Errorf("not yet: %q", v.StatusLine)
	}
	v = view(both(withRun, queue(3), func(i *ViewInputs) { i.AutoTriage, i.AnnotatedToday, i.TodayKnown = true, 2, true }))
	if got := SettingsStatus(v); got != refined+" · 3 conversations wait for the assistant · 2 conversations triaged automatically today" {
		t.Errorf("settings: %q", got)
	}
	// After a run.
	if v := view(both(withRun, queue(7), func(i *ViewInputs) { i.State = Finished(Manual, 5, 0, t0) })); v.StatusLine != refined+" · 7 conversations wait for the assistant" {
		t.Errorf("after a run: %q", v.StatusLine)
	}
	// Paused, in the strip and in Preferences.
	until := t0.Add(time.Hour)
	v = view(both(withRun, queue(12), func(i *ViewInputs) { i.AutoTriage, i.Pause = true, failedPause(board.FailTimeout, until) }))
	paused := "Automatic triage paused: it took too long · next try in 1 hour · 12 conversations wait for the assistant"
	if v.Paused != paused || StripText(v, true, board.PhaseReady, tr) != paused || SettingsStatus(v) != paused {
		t.Errorf("paused: %+v", v)
	}
	v = view(both(queue(2), func(i *ViewInputs) { i.AutoTriage, i.Pause = true, pause(board.PauseSignedOut) }))
	if v.Paused != "Automatic triage paused: Claude Code is not signed in · 2 conversations wait for the assistant" {
		t.Errorf("signed out: %q", v.Paused)
	}
	// The board's notes off, or no board: no count.
	if v := view(both(queue(5), func(i *ViewInputs) { i.AssistantOn = false })); v.Waiting != "" || v.StatusLine != "Sorted by the daemon’s rules · assistant off" {
		t.Errorf("notes off: %+v", v)
	}
	if v := view(both(queue(5), func(i *ViewInputs) { i.Shown, i.AssistantOn = false, false })); v.Waiting != "" || v.StatusLine != "" {
		t.Errorf("hidden: %+v", v)
	}
	for _, phase := range []board.Phase{board.PhaseOff, board.PhaseUnsupported} {
		if v := view(both(withRun, queue(5), func(i *ViewInputs) { i.BoardPhase = phase })); v.Waiting != "" || v.StatusLine != "" {
			t.Errorf("phase %d: %+v", phase, v)
		}
	}
	// A run: only when more waits than it still has to do.
	v = view(both(queue(40), func(i *ViewInputs) { i.State = Running(Manual, 3, 5) }))
	if v.Progress != "Triaging… 3 of 5 · 40 conversations wait for the assistant" ||
		v.StatusLine != "The assistant is triaging the board… 3 of 5 · 40 conversations wait for the assistant" {
		t.Errorf("a run, more waits: %+v", v)
	}
	if StripText(v, true, board.PhaseReady, tr) != v.Progress || StripText(v, false, board.PhaseReady, tr) != v.Progress {
		t.Error("a run's strip")
	}
	if v := view(both(queue(2), func(i *ViewInputs) { i.State = Running(Manual, 3, 5) })); v.Waiting != "" || v.Progress != "Triaging… 3 of 5" {
		t.Errorf("a run, less waits: %+v", v)
	}
	if v := view(both(queue(9), func(i *ViewInputs) { i.State = Running(Manual, 0, 0) })); v.Waiting != "" || v.Progress != "Triaging…" {
		t.Errorf("a run without a total: %+v", v)
	}
	if v := view(both(queue(9), func(i *ViewInputs) { i.State = Starting(Manual) })); v.Waiting != "" || v.Progress != "Starting the triage…" {
		t.Errorf("starting: %+v", v)
	}
}

// The Board group of Preferences → AI: its status row and its description.
func TestSettingsTexts(t *testing.T) {
	run := ranAgo(5 * time.Minute)
	line := "Triaged by rules · refined by the assistant 5 minutes ago"
	if got := SettingsStatus(view(func(i *ViewInputs) { i.LastRun = run })); got != line {
		t.Errorf("status: %q", got)
	}
	if got := SettingsStatus(view(func(i *ViewInputs) { i.LastRun, i.AutoTriage, i.AnnotatedToday, i.TodayKnown = run, true, 1, true })); got != line+" · 1 conversation triaged automatically today" {
		t.Errorf("status with today: %q", got)
	}
	if got := SettingsStatus(view(func(i *ViewInputs) {
		i.LastRun, i.AutoTriage, i.Pause, i.AnnotatedToday, i.TodayKnown = run, true, pause(board.PauseSignedOut), 1, true
	})); got != "Automatic triage paused: Claude Code is not signed in" {
		t.Errorf("status paused: %q", got)
	}
	if got := SettingsStatus(view(func(i *ViewInputs) { i.Shown, i.AssistantOn = false, false })); got != "" {
		t.Errorf("status hidden: %q", got)
	}
	rows := []struct {
		name   string
		change func(*ViewInputs)
		want   string
	}{
		{"ready", nil, ""},
		{"running", func(i *ViewInputs) { i.State = Starting(Manual) }, ""},
		{"no Claude Code", func(i *ViewInputs) { i.ClaudeFound = false },
			"The triage runs your Claude Code, which was not found on this computer. The Claude Code row above offers to get it."},
		{"signed out", func(i *ViewInputs) { i.SignedIn = signedOutS },
			"Claude Code is not signed in. The Claude Code row above offers to sign in."},
		{"signing in", func(i *ViewInputs) { i.SignedIn, i.SigningIn = signedOutS, true }, assistant.SignInTexts(tr).Waiting},
		{"no bridge", func(i *ViewInputs) { i.Bridge = false },
			"The Malachi Mail tools are not available to the assistant, so the board cannot be triaged."},
		{"no preferences", func(i *ViewInputs) { i.BackendFailed = true },
			"The mail backend did not answer with the board’s settings, so they cannot be changed now."},
	}
	for _, r := range rows {
		if got := SettingsDescription(view(r.change), tr); got != r.want {
			t.Errorf("%s: %q", r.name, got)
		}
	}
	if view(func(i *ViewInputs) { i.BackendFailed = true }).Unavailable != UnavailableNoBackend ||
		view(func(i *ViewInputs) { i.Bridge = false }).Unavailable != UnavailableNoTools ||
		view(nil).Unavailable != UnavailableNone {
		t.Error("Unavailable")
	}
}

// The row of the tokens of the last 24 hours: shown with the Board group
// once a board.list said what they were; the sum formatted, the split and
// the runs as detail, "None" without.
func TestViewUsage(t *testing.T) {
	en := func(n int64) string { return GroupDigits(n, ",") }
	if view(nil).UsageShown {
		t.Error("shown before a board.list")
	}
	known := func(i *ViewInputs) { i.UsageKnown, i.Number = true, en }
	v := view(known)
	if !v.UsageShown || v.UsageValue != "None" || v.UsageDetail != "" ||
		v.UsageToolTip != "Counts only the triage runs Malachi Mail started, not those of other assistants" {
		t.Errorf("no usage: %+v", v)
	}
	u := &api.BoardUsageTotal{BoardUsage: api.BoardUsage{InputTokens: 1200, OutputTokens: 340, CacheCreationInputTokens: 5, CacheReadInputTokens: 1_234_567}, Runs: 1}
	v = view(func(i *ViewInputs) { known(i); i.Usage24h = u })
	if v.UsageValue != "1,236,112" || v.UsageDetail != "Input 1,200 · output 340 · written to cache 5 · read from cache 1,234,567\nFrom 1 triage run" {
		t.Errorf("usage: %q %q", v.UsageValue, v.UsageDetail)
	}
	u3 := *u
	u3.Runs = 3
	if v := view(func(i *ViewInputs) { known(i); i.Usage24h = &u3 }); !strings.HasSuffix(v.UsageDetail, "\nFrom 3 triage runs") {
		t.Errorf("runs: %q", v.UsageDetail)
	}
	// Not offered: no row.
	if view(func(i *ViewInputs) { known(i); i.Shown = false }).UsageShown {
		t.Error("shown while not offered")
	}
	// Formatted as the caller says; plain digits without a formatter; a
	// sum beyond int64 stops there.
	cs, _ := UsageTexts(&api.BoardUsageTotal{BoardUsage: api.BoardUsage{InputTokens: 1_234_567}, Runs: 1}, func(n int64) string { return GroupDigits(n, " ") }, tr)
	if cs != "1 234 567" {
		t.Errorf("Czech: %q", cs)
	}
	if plain, _ := UsageTexts(&api.BoardUsageTotal{BoardUsage: api.BoardUsage{InputTokens: 1_234_567}, Runs: 1}, nil, tr); plain != "1234567" {
		t.Errorf("plain: %q", plain)
	}
	value, detail := UsageTexts(&api.BoardUsageTotal{BoardUsage: api.BoardUsage{InputTokens: maxInt64, OutputTokens: maxInt64, CacheReadInputTokens: -5}}, en, tr)
	if value != "9,223,372,036,854,775,807" || !strings.Contains(detail, "read from cache 0\nFrom 1 triage run") {
		t.Errorf("huge: %q %q", value, detail)
	}
	// A total some run of which was only partly reported says so.
	if v, _ := UsageTexts(&api.BoardUsageTotal{BoardUsage: api.BoardUsage{InputTokens: 1200, LowerBound: true}, Runs: 2}, en, tr); v != "at least 1,200" {
		t.Errorf("lower bound: %q", v)
	}
}

func TestGroupDigits(t *testing.T) {
	rows := []struct {
		n    int64
		want string
	}{{0, "0"}, {7, "7"}, {999, "999"}, {1000, "1,000"}, {123456, "123,456"}, {1234567, "1,234,567"}, {-1234, "-1,234"}}
	for _, r := range rows {
		if got := GroupDigits(r.n, ","); got != r.want {
			t.Errorf("GroupDigits(%d) = %q", r.n, got)
		}
	}
}

// The relative times and the choices of the Board group (board's texts,
// as the view uses them).
func TestTriageTextsOfTheView(t *testing.T) {
	past := []struct {
		ago  time.Duration
		want string
	}{
		{0, "just now"}, {59 * time.Second, "just now"}, {time.Minute, "1 minute ago"}, {5 * time.Minute, "5 minutes ago"},
		{time.Hour, "1 hour ago"}, {5 * time.Hour, "5 hours ago"}, {24 * time.Hour, "yesterday"}, {72 * time.Hour, "3 days ago"},
		{-30 * time.Second, "just now"},
	}
	for _, r := range past {
		if got := board.RelativeTime(t0.Add(-r.ago), t0, tr); got != r.want {
			t.Errorf("RelativeTime(%v) = %q", r.ago, got)
		}
	}
	future := []struct {
		ahead time.Duration
		want  string
	}{
		{0, "now"}, {90 * time.Second, "in 1 minute"}, {20 * time.Minute, "in 20 minutes"}, {time.Hour, "in 1 hour"},
		{4 * time.Hour, "in 4 hours"}, {24 * time.Hour, "tomorrow"}, {48 * time.Hour, "in 2 days"},
	}
	for _, r := range future {
		if got := board.RelativeFuture(t0.Add(r.ahead), t0, tr); got != r.want {
			t.Errorf("RelativeFuture(%v) = %q", r.ahead, got)
		}
	}
	minutes := []struct {
		m    int
		want string
	}{{1, "1 minute"}, {15, "15 minutes"}, {60, "1 hour"}, {90, "90 minutes"}, {180, "3 hours"}}
	for _, r := range minutes {
		if got := board.TriageInterval(r.m, tr); got != r.want {
			t.Errorf("TriageInterval(%d) = %q", r.m, got)
		}
	}
	if board.TriageDailyCap(60, tr) != "Up to 60" || board.TriageDailyCap(0, tr) != "None" {
		t.Error("TriageDailyCap")
	}
	// The switch's subtitle says what the sheet says.
	sub, body := board.TriageSettingsConsentSubtitle(tr), board.TriageConsentBody(tr)
	for _, words := range []string{"Anthropic", "through your Claude Code", "cannot send, move or delete mail", "write replies, which stay on the board"} {
		if !strings.Contains(sub, words) || !strings.Contains(body, words) {
			t.Errorf("%q is not in both", words)
		}
	}
}
