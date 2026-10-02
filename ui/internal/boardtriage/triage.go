// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package boardtriage is the board's triage run without its widgets: the
// user's own Claude Code annotates the board's cases through the bridge's
// triage tools (docs/api.md §4.13 "Triage and runs", docs/mcp.md "Triage of
// the board" and "The board's triage run in the app", docs/security.md
// §10.2). It holds the run's state and the view model of the Triage control
// and the status strip (triage.go), the pure rule of automatic triage
// (auto.go), the board's preferences in the daemon (preferences.go), the run
// itself (controller.go) and its automatic schedule (scheduler.go).
//
// The macOS client leads (MalachiCore Board/BoardTriage.swift,
// Board/BoardAutoTriage.swift, Controllers/BoardPreferencesController.swift,
// BoardTriageController.swift and BoardAutoTriageScheduler.swift); this is
// its port for the GTK window. The texts are ui/internal/board's
// (triage.go) and ui/internal/assistant's; this package adds none. A run's
// outcome is only counts and classes: neither the model's words nor mail
// text ever get here, and nothing of them is logged.
//
// The package is pure Go (no GTK, no GLib): the window passes a Loop over
// the GLib main loop (assistantpanel.Loop), and everything here runs on it.
package boardtriage

import (
	"strconv"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/assistantpanel"
	"github.com/schotek/malachi/ui/internal/board"
)

// Trigger is who starts a triage run (macOS Board.TriageTrigger).
type Trigger = assistant.TriageTrigger

// The triggers.
const (
	// Manual is the user's Triage button.
	Manual = assistant.TriageManual
	// Automatic is the application's schedule.
	Automatic = assistant.TriageAutomatic
)

// Wire is board.runStart's trigger for t.
func Wire(t Trigger) api.BoardTrigger {
	if t == Manual {
		return api.TriggerManual
	}
	return api.TriggerAuto
}

// RunError is the class board.runEnd records for a run that failed with f
// (api.BoardRunError).
func RunError(f board.TriageFailure) api.BoardRunError {
	switch f {
	case board.FailCancelled:
		return api.RunCancelled
	case board.FailTimeout:
		return api.RunTimeout
	case board.FailNotSignedIn:
		return api.RunSignedOut
	}
	return api.RunFailed
}

// StateKind is where the application's triage run is.
type StateKind int

// The kinds of states.
const (
	// StateIdle: no run since the application started.
	StateIdle StateKind = iota
	// StateStarting: checking what it needs (consent, Claude Code, the
	// sign-in) and starting the run.
	StateStarting
	// StateRunning: Claude Code works, Done cases annotated of Total
	// expected.
	StateRunning
	// StateFinished: it ended well, with Annotated cases annotated and
	// Refused notes the board refused (annotate_case results that were
	// errors), At then.
	StateFinished
	// StateFailed: it failed with Failure, At then.
	StateFailed
)

// State is where the application's triage run is (macOS
// Board.TriageState); only the fields of its Kind are set. Trigger is set
// for every kind but StateIdle.
type State struct {
	Kind               StateKind
	Trigger            Trigger
	Done, Total        int
	Annotated, Refused int
	Failure            board.TriageFailure
	At                 time.Time
}

// Starting is a run of trigger t that is starting.
func Starting(t Trigger) State { return State{Kind: StateStarting, Trigger: t} }

// Running is a run of trigger t with done cases annotated of total.
func Running(t Trigger, done, total int) State {
	return State{Kind: StateRunning, Trigger: t, Done: done, Total: total}
}

// Finished is a run of trigger t that ended well at at.
func Finished(t Trigger, annotated, refused int, at time.Time) State {
	return State{Kind: StateFinished, Trigger: t, Annotated: annotated, Refused: refused, At: at}
}

// Failed is a run of trigger t that failed with f at at.
func Failed(t Trigger, f board.TriageFailure, at time.Time) State {
	return State{Kind: StateFailed, Trigger: t, Failure: f, At: at}
}

// Active says whether a run is starting or running.
func (s State) Active() bool { return s.Kind == StateStarting || s.Kind == StateRunning }

// Equal says whether s and o are the same state (times compared as
// instants).
func (s State) Equal(o State) bool {
	return s.Kind == o.Kind && s.Trigger == o.Trigger && s.Done == o.Done && s.Total == o.Total &&
		s.Annotated == o.Annotated && s.Refused == o.Refused && s.Failure == o.Failure && s.At.Equal(o.At)
}

// Control is what the Triage control is (macOS Board.TriageControl).
type Control int

// The controls.
const (
	// ControlHidden: no control; triage is not offered.
	ControlHidden Control = iota
	// ControlGetClaudeCode: Claude Code is missing; the control offers Get
	// Claude Code… (assistant.InstallURL in the browser).
	ControlGetClaudeCode
	// ControlSignIn: Claude Code is signed out; Sign In…
	// (assistantpanel.Locator.SignIn), insensitive while a sign-in waits
	// for the browser.
	ControlSignIn
	// ControlUnavailable: the bridge is missing, or the daemon does not
	// answer the board's preferences; shown, insensitive, ToolTip says
	// why.
	ControlUnavailable
	// ControlTriage: ready; a click starts a manual run (which asks for
	// consent first when NeedsConsent).
	ControlTriage
	// ControlStop: a run is under way; a click stops it.
	ControlStop
)

// Unavailable is why the control is ControlUnavailable.
type Unavailable int

// The reasons.
const (
	// UnavailableNone: the control is not ControlUnavailable.
	UnavailableNone Unavailable = iota
	// UnavailableNoTools: malachi-mcp is not beside the application.
	UnavailableNoTools
	// UnavailableNoBackend: the daemon did not answer the board's
	// preferences, and none are known.
	UnavailableNoBackend
)

// ViewInputs are what ViewOf reads (macOS Board.TriageViewInputs).
type ViewInputs struct {
	// Shown: triage is available (TriageAvailable: the Assistant shown
	// with the In App target).
	Shown bool
	// ClaudeFound: the locator found Claude Code; Bridge: malachi-mcp is
	// beside the application.
	ClaudeFound, Bridge bool
	// SignedIn is whether Claude Code is signed in, as last asked.
	SignedIn assistantpanel.SignIn
	// NeedsConsent: the triage would ask for consent first.
	NeedsConsent bool
	// AssistantOn: the board's assistant preference is on (its notes
	// count).
	AssistantOn bool
	State       State
	// LastRun is the daemon's last run (board.list triage.lastRun), nil
	// before the first.
	LastRun *api.BoardRun
	// AutoTriage is the autoTriage preference; Pause why automatic triage
	// pauses (nil: it does not).
	AutoTriage bool
	Pause      *board.Pause
	// BackendFailed: the daemon did not answer the board's preferences,
	// and none are known: a run could neither take a consent nor start.
	BackendFailed bool
	// AnnotatedToday is the cases automatic runs annotated today when
	// TodayKnown (reported today).
	AnnotatedToday int
	TodayKnown     bool
	// BoardPhase is the board's phase as far as it decides whether triage
	// exists: board.PhaseOff (turned off in the daemon) and
	// board.PhaseUnsupported (an older daemon without the board) hide the
	// control; any other phase does not (PhaseLoading, the zero value,
	// stands for "not known").
	BoardPhase board.Phase
	// SigningIn: Claude Code's sign-in in the browser is under way (the
	// application's one, whoever started it).
	SigningIn bool
	// UsageKnown: a board.list has said what the triage used in the last
	// 24 hours (Usage24h, nil when no run reported any).
	UsageKnown bool
	Usage24h   *api.BoardUsageTotal
	// Queue is the cases waiting for the assistant (board.list
	// triage.queue, uncapped) when QueueKnown (a board.list said it with
	// the board's notes on).
	Queue      int
	QueueKnown bool
	// Number formats a count of tokens for the locale ("1,234,567";
	// "1 234 567" in Czech); nil writes plain digits.
	Number func(int64) string
	Now    time.Time
}

// View is the Triage control and the status strip (macOS
// Board.TriageView).
type View struct {
	// Control is ControlHidden exactly when triage is not offered: the
	// toolbar item and the Board group of Preferences → AI follow this one
	// rule (Offered).
	Control Control
	// Title is the control's title ("" when hidden); Enabled whether it
	// can be clicked; ToolTip its tooltip.
	Title   string
	Enabled bool
	ToolTip string
	// NeedsConsent: a click asks for consent first.
	NeedsConsent bool
	// Running: a run is starting or running.
	Running bool
	// Progress is "Triaging… 3 of 12" while a run works; "" otherwise.
	Progress string
	// StatusLine is the strip's line: who sorted the board and when the
	// assistant last refined it, or the run's progress. "" while triage
	// is not offered and the board's notes are off: a user without the
	// in-app assistant gets no "assistant off".
	StatusLine string
	// RelativeTime: the lines name a time relative to Now ("5 minutes
	// ago", "next try in 1 hour"), which goes stale as time passes.
	RelativeTime bool
	// Result is how the application's last run ended ("" before one, and
	// while one runs).
	Result string
	// Paused is "Automatic triage paused: …", or "". Both it and
	// StatusLine end with Waiting after " · " when that is not "".
	Paused string
	// Waiting is "3 conversations wait for the assistant" while the queue
	// is known and not empty (and, during a run, has cases beyond what
	// the run still has to do), else "". Already part of StatusLine,
	// Paused and Progress.
	Waiting string
	// AnnotatedToday is the cases automatic runs annotated today when
	// TodayShown: automatic triage is on and the count is known for today.
	AnnotatedToday int
	TodayShown     bool
	// TodayLine is AnnotatedToday as "12 conversations triaged
	// automatically today", or "".
	TodayLine string
	// Unavailable is why the control is ControlUnavailable.
	Unavailable Unavailable
	// SigningIn: Claude Code's sign-in waits for the browser; the control
	// is Sign In…, insensitive, and says so.
	SigningIn bool
	// UsageShown: the Board group's row of the tokens of the last 24 hours
	// shows (triage is offered and a board.list said what they were).
	UsageShown bool
	// UsageValue is that row's value: the four counters added up,
	// formatted, or "None".
	UsageValue string
	// UsageDetail is that row's detail: the tokens by kind on one line,
	// the runs they come from on the next; "" without usage.
	UsageDetail string
	// UsageToolTip is that row's tooltip: only this application's runs
	// count.
	UsageToolTip string
}

// Offered says whether triage is offered: the toolbar shows its control
// and Preferences → AI its Board group.
func (v View) Offered() bool { return v.Control != ControlHidden }

// signedOut says whether s says Claude Code is signed out.
func signedOut(s assistantpanel.SignIn) bool { return s.Known && !s.SignedIn }

// ViewOf is the Triage control and the status strip for i.
func ViewOf(i ViewInputs, tr board.Translator) View {
	v := View{
		Control: ControlTriage, Title: board.Triage(tr), Enabled: true, ToolTip: board.TriageToolTip(tr),
		NeedsConsent: i.NeedsConsent, Running: i.State.Active(),
	}
	// A board the daemon does not have, or has turned off, has nothing to
	// triage; a run under way keeps its Stop (losing the board stops it
	// anyway).
	boardGone := i.BoardPhase == board.PhaseOff || i.BoardPhase == board.PhaseUnsupported
	switch {
	case !i.Shown || (boardGone && !i.State.Active()):
		v.Control, v.Title, v.Enabled, v.ToolTip = ControlHidden, "", false, ""
	case i.State.Active():
		v.Control = ControlStop
		v.Title = assistant.PanelTexts(tr).Stop
		v.ToolTip = board.TriageStopToolTip(tr)
	case !i.ClaudeFound:
		v.Control = ControlGetClaudeCode
		v.Title = assistant.SignInTexts(tr).GetClaudeCode
		v.ToolTip = board.TriageNeedsClaudeCode(tr)
	case !i.Bridge:
		v.Control, v.Enabled, v.Unavailable = ControlUnavailable, false, UnavailableNoTools
		v.ToolTip = board.TriageFailureText(board.FailToolsMissing, tr)
	case i.BackendFailed:
		v.Control, v.Enabled, v.Unavailable = ControlUnavailable, false, UnavailableNoBackend
		v.ToolTip = board.TriageFailureText(board.FailBackend, tr)
	case i.SigningIn:
		// One sign-in for the application: a click must not start a
		// second one (which would end the first as cancelled).
		v.Control = ControlSignIn
		v.Title = assistant.SignInTexts(tr).SignIn
		v.Enabled = false
		v.ToolTip = assistant.SignInTexts(tr).Waiting
		v.SigningIn = true
	case signedOut(i.SignedIn):
		v.Control = ControlSignIn
		v.Title = assistant.SignInTexts(tr).SignIn
		v.ToolTip = board.TriageNeedsSignIn(tr)
	}
	switch i.State.Kind {
	case StateStarting:
		v.Progress = board.TriageStarting(tr)
	case StateRunning:
		v.Progress = board.TriageProgress(i.State.Done, i.State.Total, tr)
	case StateFinished:
		v.Result = board.TriageFinished(i.State.Annotated, i.State.Refused, tr)
	case StateFailed:
		if i.State.Failure == board.FailCancelled {
			v.Result = board.TriageStopped(tr)
		} else {
			v.Result = board.TriageFailed(i.State.Failure, tr)
		}
	}
	switch {
	case i.State.Kind == StateRunning:
		v.StatusLine = board.TriageRunningLine(i.State.Done, i.State.Total, tr)
	case i.State.Active():
		v.StatusLine = board.TriageRunningLine(0, 0, tr)
	case v.Control == ControlHidden && boardGone:
		// No board: no line about it.
	case i.Shown || i.AssistantOn:
		// Notes another client wrote (Claude Code with the bridge's
		// triage tier) still count without the in-app assistant.
		var last *time.Time
		if r := i.LastRun; r != nil && r.EndedAt != nil {
			at := *r.EndedAt
			last = &at
		}
		v.StatusLine = board.TriageStatusLine(i.AssistantOn, last, i.Now, tr)
		if i.AssistantOn && last != nil {
			v.RelativeTime = true
		}
	}
	if v.Offered() && i.AutoTriage && !i.State.Active() && i.Pause != nil {
		v.Paused = board.AutoTriagePaused(*i.Pause, i.Now, tr)
		if i.Pause.Kind == board.PauseFailed {
			v.RelativeTime = true
		}
	}
	// The queue, after the line it belongs to. During a run only when
	// something waits beyond what the run still has to do.
	if i.QueueKnown && i.Queue > 0 && i.AssistantOn {
		s := i.State
		switch s.Kind {
		case StateRunning:
			if s.Total > 0 && i.Queue > s.Total-min(s.Done, s.Total) {
				v.Waiting = board.TriageWaiting(i.Queue, tr)
				v.Progress = JoinedNote(v.Progress, v.Waiting)
				v.StatusLine = JoinedNote(v.StatusLine, v.Waiting)
			}
		case StateStarting:
		default:
			if v.StatusLine != "" || v.Paused != "" {
				v.Waiting = board.TriageWaiting(i.Queue, tr)
				v.StatusLine = JoinedNote(v.StatusLine, v.Waiting)
				v.Paused = JoinedNote(v.Paused, v.Waiting)
			}
		}
	}
	if v.Offered() && i.AutoTriage && i.TodayKnown {
		v.AnnotatedToday, v.TodayShown = i.AnnotatedToday, true
		v.TodayLine = board.TriagedToday(i.AnnotatedToday, tr)
	}
	if v.Offered() && i.UsageKnown {
		v.UsageValue, v.UsageDetail = UsageTexts(i.Usage24h, i.Number, tr)
		v.UsageShown = true
		v.UsageToolTip = board.TriageUsageToolTip(tr)
	}
	return v
}

// separator joins the notes of a line, as board's status line joins them.
const separator = " · "

// JoinedNote is line and note joined as the status line joins its notes
// (" · "), or line alone when either is "".
func JoinedNote(line, note string) string {
	if line == "" || note == "" {
		return line
	}
	return line + separator + note
}

// UsageTexts are the value and the detail of the Board group's row of the
// tokens of the last 24 hours for u: the sum of the four counters
// (negative ones as 0, saturating at the largest int64) and the split with
// the runs (at least 1); the usage's "None" and "" without usage. number
// formats a count for the locale; nil writes plain digits.
func UsageTexts(u *api.BoardUsageTotal, number func(int64) string, tr board.Translator) (value, detail string) {
	if u == nil {
		return board.TriageUsageNone(tr), ""
	}
	if number == nil {
		number = func(n int64) string { return strconv.FormatInt(n, 10) }
	}
	parts := [4]int64{
		max(u.InputTokens, 0), max(u.OutputTokens, 0),
		max(u.CacheCreationInputTokens, 0), max(u.CacheReadInputTokens, 0),
	}
	var total int64
	for _, p := range parts {
		if total > maxInt64-p {
			total = maxInt64
		} else {
			total += p
		}
	}
	split := board.TriageUsageSplit(number(parts[0]), number(parts[1]), number(parts[2]), number(parts[3]), tr)
	return number(total), split + "\n" + board.TriageUsageRuns(max(u.Runs, 1), tr)
}

// maxInt64 is the largest int64.
const maxInt64 = int64(^uint64(0) >> 1)

// GroupDigits writes n with sep between groups of three digits
// ("1,234,567" with ","), for a Number that groups as the locale does.
func GroupDigits(n int64, sep string) string {
	s := strconv.FormatInt(n, 10)
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	if len(s) <= 3 {
		return sign + s
	}
	var b strings.Builder
	b.WriteString(sign)
	head := len(s) % 3
	if head > 0 {
		b.WriteString(s[:head])
	}
	for i := head; i < len(s); i += 3 {
		if i > 0 {
			b.WriteString(sep)
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// StripText is the status strip's triage note for the window in the board
// (inBoard) or in Mail, with the board in phase: in Mail only a run's
// progress; in the board a run's progress, else the sign-in waiting for
// the browser, else why automatic triage pauses, else the status line;
// nothing in the board while it is off or the daemon has none, unless a
// run works.
func StripText(v View, inBoard bool, phase board.Phase, tr board.Translator) string {
	if !inBoard {
		if v.Running {
			return v.Progress
		}
		return ""
	}
	if v.Running {
		if v.Progress == "" {
			return v.StatusLine
		}
		return v.Progress
	}
	if phase == board.PhaseOff || phase == board.PhaseUnsupported {
		return ""
	}
	if v.SigningIn {
		return assistant.SignInTexts(tr).Waiting
	}
	if v.Paused != "" {
		return v.Paused
	}
	return v.StatusLine
}

// SettingsStatus is the status row of the Board group of Preferences → AI:
// why automatic triage pauses, else the status line with today's automatic
// count.
func SettingsStatus(v View) string {
	if v.Paused != "" {
		return v.Paused
	}
	var parts []string
	for _, s := range []string{v.StatusLine, v.TodayLine} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, separator)
}

// SettingsDescription is the description of the Board group of
// Preferences → AI: why triage cannot run now, in whole sentences, or ""
// when it can.
func SettingsDescription(v View, tr board.Translator) string {
	switch v.Control {
	case ControlGetClaudeCode:
		return board.TriageSettingsNeedsClaudeCode(tr)
	case ControlSignIn:
		if v.SigningIn {
			return assistant.SignInTexts(tr).Waiting
		}
		return board.TriageSettingsNeedsSignIn(tr)
	case ControlUnavailable:
		if v.Unavailable == UnavailableNoBackend {
			return board.TriageSettingsNoBackend(tr)
		}
		return board.TriageSettingsNoTools(tr)
	}
	return ""
}
