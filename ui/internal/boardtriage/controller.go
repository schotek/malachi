// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package boardtriage

import (
	"log/slog"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/assistantpanel"
	"github.com/schotek/malachi/ui/internal/board"
)

// TriageNeedsInAppTarget is the owner's decision (2026-10-01): triage is
// offered only under the assistant's "In App (experimental)" target, the
// condition of the panel, the compose rewrite and the search in the user's
// own words. It is the only target under which the application itself
// starts Claude Code, and it stays experimental until Anthropic confirms
// the terms for running Claude Code from an application. False would offer
// triage whenever the Assistant is shown, whatever the target.
const TriageNeedsInAppTarget = true

// TriageAvailable says whether triage is available with the Assistant
// shown (assistant.Shown) and target chosen (TriageNeedsInAppTarget); the
// window passes it as Config.Available.
func TriageAvailable(shown bool, target assistant.Target) bool {
	return shown && (!TriageNeedsInAppTarget || target == assistant.App)
}

// The controller's times.
const (
	// LimitGrace is how long a run that reached its limit waits for Claude
	// Code's result, the only report of the whole run's usage (the API
	// messages carry only their starts' output tokens). The bridge has
	// closed the queue and tells the model to stop, so it needs one or two
	// more API calls over its cached context to notice and end its turn;
	// on the owner's first real run a call took about 10 s (40 notes, 8
	// minutes), so 45 s leaves room for a slow pair without a full
	// progress bar hanging for long.
	LimitGrace = 45 * time.Second
	// EndWait is how long CancelAndEnd waits for board.runEnd at most.
	EndWait = 2 * time.Second
	// ClockTick is how often the view is published again while it names a
	// relative time (View.RelativeTime).
	ClockTick = time.Minute
)

// Settings are the preferences the triage reads and writes: the panel's
// (the consent assistant-consent, the claude executable, its model) and
// the board's own consent (board-triage-consent) and model
// (board-triage-model, Sonnet by default, apart from assistant-model).
// settings.Store has them.
type Settings interface {
	assistantpanel.Settings
	BoardTriageConsent() bool
	SetBoardTriageConsent(bool)
	BoardTriageModel() assistant.Model
}

// Locator is the application's Claude Code as the triage asks it;
// *assistantpanel.Locator has it (shared with the panel).
type Locator interface {
	Locate() string
	Refresh()
	SignedIn(done func(assistantpanel.SignIn))
	SigningIn() bool
	OnSignInChange(f func()) (remove func())
}

var (
	_ Locator    = (*assistantpanel.Locator)(nil)
	_ AutoTarget = (*Controller)(nil)
)

// Snapshot is what a board.list said about the triage, as the window's
// board model reports it (macOS Board.Snapshot): the board's phase, its
// assistant preference (board.list's assistant) and its triage part.
type Snapshot struct {
	Phase     board.Phase
	Assistant bool
	Triage    api.BoardTriage
}

// BoardInfo is what the board last reported about the triage.
type BoardInfo struct {
	// Known: a board.list has arrived.
	Known bool
	// AssistantOn is the board's assistant preference as board.list
	// reported it.
	AssistantOn bool
	// Queue is the cases waiting for the assistant (0 while it is off).
	Queue int
	// AnnotatedToday is the cases automatic runs annotated today, and
	// CountedAt when that was reported.
	AnnotatedToday int
	CountedAt      time.Time
	// LastRun is the daemon's last run; Usage24h the tokens triage runs
	// used in the last 24 hours (nil when none reported any).
	LastRun  *api.BoardRun
	Usage24h *api.BoardUsageTotal
}

// Ended is how the run that ended last ended: its trigger and, when
// Failed, its failure.
type Ended struct {
	Trigger Trigger
	Failed  bool
	Failure board.TriageFailure
}

// Config is what a Controller needs from the application.
type Config struct {
	Caller      Caller
	Settings    Settings
	Locator     Locator
	Preferences *Preferences
	// Request is the one-shot request the runs use (its consent hook is
	// cleared: the run asks itself); one of its own, not the rewrite's or
	// the search's.
	Request    *assistantpanel.Request
	Loop       assistantpanel.Loop
	Log        *slog.Logger
	Translator board.Translator
	// Bridge is malachi-mcp beside the application ("" without one);
	// Socket the daemon's socket for it ("" for the bridge's default).
	Bridge, Socket string
	// Available says whether triage is available (TriageAvailable); its
	// changes come through AvailabilityChanged.
	Available func() bool
	// Location is where "today" is counted (nil: time.Local).
	Location *time.Location
	// Number formats a count of tokens for the locale (ViewInputs.Number).
	Number func(int64) string
}

// Controller is the board's triage run (docs/api.md §4.13 "Triage and
// runs", docs/mcp.md "Triage of the board", docs/security.md §10.2; macOS
// BoardTriageController), once for the whole application: the user's
// Claude Code annotates the cases of the board through the bridge's triage
// tools, in one bounded one-shot request (assistantpanel.Request with
// Tools). The daemon never talks to an assistant; this controller starts
// the run on the user's click (Start(Manual, 0)) or on the schedule's
// (Scheduler).
//
// A run, each step of which may end it (State):
//
//  1. What it needs: triage available (Config.Available: the Assistant
//     shown with the In App target, TriageNeedsInAppTarget), the bridge
//     beside the application, Claude Code found and not signed out (asked
//     afresh for a manual run), and consent (ConsentGiven: the panel's
//     assistant-consent, the board's own board-triage-consent and the
//     board's assistant preference). A manual run without consent asks
//     (Consent, the sheet) and gives it (GiveConsent); an automatic run
//     never asks. A manual run whose queue is known to be empty ends at
//     once (board.FailNothingToDo).
//  2. board.runStart with the trigger and assistant.TriageSource.
//  3. The request: assistant.TriageMessage for at most the run's limit
//     under assistant.TriageSystemPrompt, with the board's own model
//     (board-triage-model, read when the run starts; not the panel's
//     assistant-model), with the bridge started as --socket <socket>
//     --allow-triage --triage-run <runId> --triage-max <limit> and only
//     assistant.TriageToolsFor(trigger) (no create_draft for an automatic
//     run), for at most Timeout. Every annotate_case call the bridge
//     accepted counts as progress, of the queue's size at the start capped
//     by the limit; refused ones are counted apart. When the accepted ones
//     reach the limit the run has succeeded: the bridge refuses more and
//     closes the queue, telling the model to stop, so the request is left
//     to end by itself and deliver Claude Code's result, whose usage is
//     the whole run's; after Grace without it the request is cancelled.
//     Meanwhile the run is still running (its progress full), later tool
//     calls are not counted, and whatever ends it ends it as a success.
//  4. board.runEnd with the failure's class, or none, and the tokens the
//     run used as far as its Claude Code reported them
//     (assistant.UsageTally: the result's usage, else the sum of the API
//     messages seen before it ended — cancelled, timed out, at its limit
//     without a result in time: a lower bound; none when nothing reported
//     any); then OnRefresh asks the board to list again. A run the
//     application could not end (it was killed) the daemon ends itself
//     later.
//
// One run at a time: Start while one is active does nothing. Cancel ends
// it (board.FailCancelled, recorded so); so does losing what it needs
// while it runs (triage no longer available, a consent withdrawn, the
// board or its assistant preference off, and for an automatic run
// autoTriage off). An automatic run that ends with no note accepted
// although its queue had cases fails (board.FailNotesRefused,
// board.FailNoProgress), so the schedule backs off. Nothing the model
// writes is shown or logged, nor any mail text: a run's outcome is its
// counts and its class.
//
// The daemon's assistant preference on while the board's consent key is
// not set here (a withdrawal whose write failed, say) is turned off again
// at every answer of board.preferences, so the daemon stops handing mail
// to a triage bridge; not while consent is being given or a write is under
// way (each write reads afresh first).
//
// The window calls SettingsChanged when assistant-consent or
// board-triage-consent changed, AvailabilityChanged when the Assistant's
// shown state or assistant-target changed, and BoardChanged with every
// board.list. Main loop only.
type Controller struct {
	// Provider metadata is captured at the start of each run/consent.
	Source          func() string
	ModelID         func() string
	ConsentIdentity func() int
	// Consent asks the user whether the board's mail may go to the
	// assistant (the sheet: board.TriageConsentHeading,
	// board.TriageConsentBody, the panel's Allow and Cancel) and calls
	// done with the answer, true allowing. Without it a manual run that
	// needs consent ends as board.FailDeclined.
	Consent func(done func(allowed bool))
	// Timeout is how long a run may take (assistant.TriageTimeout).
	Timeout time.Duration
	// Grace is how long a run at its limit waits for the result
	// (LimitGrace).
	Grace time.Duration
	// OnRefresh asks the board to list again (after a run ended, and from
	// RelistBoard).
	OnRefresh func()
	// Now is the clock; Language the English name of the UI language ("":
	// English) and Today today as YYYY-MM-DD, for the system prompt.
	Now      func() time.Time
	Language func() string
	Today    func() string

	caller    Caller
	settings  Settings
	locator   Locator
	prefs     *Preferences
	request   *assistantpanel.Request
	loop      assistantpanel.Loop
	log       *slog.Logger
	tr        board.Translator
	bridge    string
	socket    string
	available func() bool
	location  *time.Location
	number    func(int64) string

	state    State
	signedIn assistantpanel.SignIn
	board    BoardInfo
	revision int
	// boardPhase is the board's last decisive phase from a snapshot
	// (ready, preparing, off, unsupported), when phaseKnown.
	boardPhase board.Phase
	phaseKnown bool
	autoPause  *board.Pause

	observers, endedObs observers
	ended               Ended
	hasEnded            bool
	removes             []func()

	// gen is bumped by every Start and Cancel: the steps of an older run
	// stop at their next callback.
	gen int
	// runID is the daemon's run under way, once board.runStart answered.
	runID api.BoardRunID
	// annotateCalls are the run's annotate_case calls waiting for their
	// results; refused the ones the bridge refused.
	annotateCalls map[string]bool
	refused       int
	usage         assistant.UsageTally
	runLimit      int
	// limitHit: the run reached its limit and waits for Claude Code's
	// result; it ends as a success however it ends. graceGen retires the
	// wait.
	limitHit bool
	graceGen int
	// queueHadCases: the run's queue was known to have cases when it
	// started.
	queueHadCases bool
	// permitted: the run passed its consent step; a consent lost from now
	// on stops it.
	permitted bool
	// granting: consent is being given; the repair of a stray assistant
	// preference waits.
	granting bool
	// signInGen is bumped by every sign-in check and every sign-in a run
	// learnt: an older check's late answer is dropped. signInAnswers
	// counts the answers (the tests wait for them).
	signInGen, signInAnswers int
	// ending counts the board.runEnd calls on their way; endDone closes
	// when each is answered (CancelAndEnd waits for them).
	ending  int
	endDone map[int]chan struct{}
	nextEnd int
	// clockGen retires the clock's tick; clockRunning: a tick is pending.
	clockGen     int
	clockRunning bool
	clockTick    time.Duration
}

// New is the application's triage, idle.
func New(cfg Config) *Controller {
	loc := cfg.Location
	if loc == nil {
		loc = time.Local
	}
	c := &Controller{
		Timeout:   assistant.TriageTimeout,
		Grace:     LimitGrace,
		Now:       time.Now,
		Language:  func() string { return "" },
		caller:    cfg.Caller,
		settings:  cfg.Settings,
		locator:   cfg.Locator,
		prefs:     cfg.Preferences,
		request:   cfg.Request,
		loop:      cfg.Loop,
		log:       orDiscard(cfg.Log),
		tr:        cfg.Translator,
		bridge:    cfg.Bridge,
		socket:    cfg.Socket,
		available: cfg.Available,
		location:  loc,
		number:    cfg.Number,
		endDone:   map[int]chan struct{}{},
		clockTick: ClockTick,
		runLimit:  assistant.TriageBatch,
	}
	c.Today = func() string { return c.Now().In(c.location).Format("2006-01-02") }
	if c.available == nil {
		c.available = func() bool { return false }
	}
	// The run checks consent itself and never lets the request ask.
	c.request.Consent = nil
	c.removes = append(c.removes,
		c.prefs.Observe(c.permissionsChanged),
		c.prefs.ObserveLoaded(c.repairAssistantPreference),
		// A sign-in that starts or ends: the view says it waits for the
		// browser meanwhile, and asks the sign-in again after.
		c.locator.OnSignInChange(func() {
			c.CheckSignIn()
			c.publish()
		}))
	return c
}

// Close stops listening to the preferences and the locator and stops the
// clock; a run under way goes on (CancelAndEnd ends it).
func (c *Controller) Close() {
	for _, r := range c.removes {
		r()
	}
	c.removes = nil
	c.clockGen++
	c.clockRunning = false
}

// State is where the application's run is.
func (c *Controller) State() State { return c.state }

// SignedIn is whether Claude Code is signed in, as last asked.
func (c *Controller) SignedIn() assistantpanel.SignIn { return c.signedIn }

// Board is what the board last reported about the triage.
func (c *Controller) Board() BoardInfo { return c.board }

// BoardRevision changes whenever Board changed.
func (c *Controller) BoardRevision() int { return c.revision }

// BoardPhase is the board's last decisive phase from a snapshot (ready,
// preparing, off, unsupported); false before one.
func (c *Controller) BoardPhase() (board.Phase, bool) { return c.boardPhase, c.phaseKnown }

// AutoPause is why automatic triage pauses, as the schedule set it.
func (c *Controller) AutoPause() *board.Pause { return c.autoPause }

// LastEnded is how the run that ended last ended; false before one.
func (c *Controller) LastEnded() (Ended, bool) { return c.ended, c.hasEnded }

// Idle says whether no run is active and no board.runEnd is on its way.
func (c *Controller) Idle() bool { return !c.state.Active() && c.ending == 0 }

// Observe calls f after any change of what the view shows: the state, the
// sign-in, the board's triage, the consents, the preferences, the pause,
// and once a minute while the view names a relative time; until the
// returned function is called.
func (c *Controller) Observe(f func()) (remove func()) { return c.observers.add(f) }

// ObserveEnded calls f once each time a run ended, read through LastEnded;
// State still says the run is active then, and changes right after.
func (c *Controller) ObserveEnded(f func()) (remove func()) { return c.endedObs.add(f) }

func (c *Controller) setState(s State) {
	if s.Equal(c.state) {
		return
	}
	c.state = s
	c.publish()
}

// Inputs

// BoardChanged takes what a board.list said: the triage's queue and
// counts and the daemon's last run. A snapshot of a board that could not
// be listed (loading, unavailable, failed) changes nothing.
func (c *Controller) BoardChanged(s Snapshot) {
	switch s.Phase {
	case board.PhaseReady, board.PhasePreparing, board.PhaseOff:
	case board.PhaseUnsupported:
		// An older daemon: no board to triage, nothing else to learn.
		if !c.phaseKnown || c.boardPhase != board.PhaseUnsupported {
			c.boardPhase, c.phaseKnown = board.PhaseUnsupported, true
			c.publish()
		}
		return
	default:
		return
	}
	if !c.phaseKnown || c.boardPhase != s.Phase {
		c.boardPhase, c.phaseKnown = s.Phase, true
		c.publish()
	}
	b := BoardInfo{
		Known:          true,
		AssistantOn:    s.Assistant,
		Queue:          s.Triage.Queue,
		AnnotatedToday: s.Triage.AnnotatedTodayAuto,
		CountedAt:      c.Now(),
		LastRun:        cloneRun(s.Triage.LastRun),
		Usage24h:       cloneUsage(s.Triage.Usage24h),
	}
	if c.board.Known && b.AssistantOn == c.board.AssistantOn && b.Queue == c.board.Queue &&
		b.AnnotatedToday == c.board.AnnotatedToday && sameRun(b.LastRun, c.board.LastRun) &&
		sameUsage(b.Usage24h, c.board.Usage24h) {
		c.board.CountedAt = b.CountedAt
		return
	}
	c.board = b
	c.revision++
	c.publish()
}

func cloneRun(r *api.BoardRun) *api.BoardRun {
	if r == nil {
		return nil
	}
	out := *r
	if r.EndedAt != nil {
		at := *r.EndedAt
		out.EndedAt = &at
	}
	return &out
}

func cloneUsage(u *api.BoardUsageTotal) *api.BoardUsageTotal {
	if u == nil {
		return nil
	}
	out := *u
	return &out
}

func sameRun(a, b *api.BoardRun) bool {
	if a == nil || b == nil {
		return a == b
	}
	endedSame := (a.EndedAt == nil) == (b.EndedAt == nil) && (a.EndedAt == nil || a.EndedAt.Equal(*b.EndedAt))
	return endedSame && a.At.Equal(b.At) && a.Trigger == b.Trigger && a.Source == b.Source &&
		a.Annotated == b.Annotated && a.Error == b.Error
}

func sameUsage(a, b *api.BoardUsageTotal) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// AvailabilityChanged says that whether triage is available, or where
// Claude Code is, may have changed: a run that lost it stops, the sign-in
// is asked again, and the change reported.
func (c *Controller) AvailabilityChanged() {
	c.enforce()
	c.CheckSignIn()
	c.publish()
}

// SettingsChanged says that assistant-consent or board-triage-consent
// changed.
func (c *Controller) SettingsChanged() { c.permissionsChanged() }

// permissionsChanged: the consents or the preferences changed.
func (c *Controller) permissionsChanged() {
	c.enforce()
	c.publish()
}

// enforce stops the run under way when it lost what it needs (see the
// type's comment).
func (c *Controller) enforce() {
	if !c.state.Active() {
		return
	}
	p, ok := c.prefs.Current()
	lost := !c.available() || (c.permitted && !c.ConsentGiven()) || (ok && !p.Enabled) ||
		(c.state.Trigger == Automatic && ok && !p.AutoTriage)
	if lost {
		c.log.Info("board triage: stopped, no longer allowed or available")
		c.Cancel()
	}
}

// repairAssistantPreference turns the daemon's assistant preference off
// when it is on without the board's consent here (see the type's
// comment).
func (c *Controller) repairAssistantPreference() {
	if c.granting || c.prefs.Writing() || c.settings.BoardTriageConsent() {
		return
	}
	if p, ok := c.prefs.Stored(); !ok || !p.Assistant {
		return
	}
	c.log.Info("board triage: the assistant preference was on without consent; turning it off")
	c.prefs.Update(true, func(p *api.BoardPreferences) { p.Assistant = false }, nil)
}

// RelistBoard asks the board to list again (OnRefresh), so the tokens of
// the last 24 hours, which age out without a notification, are current
// (Preferences → AI asks whenever its page comes up).
func (c *Controller) RelistBoard() {
	if c.OnRefresh != nil {
		c.OnRefresh()
	}
}

// SetAutoPause is set by the schedule.
func (c *Controller) SetAutoPause(p *board.Pause) {
	if samePause(p, c.autoPause) {
		return
	}
	if p != nil {
		v := *p
		p = &v
	}
	c.autoPause = p
	c.publish()
}

func samePause(a, b *board.Pause) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Kind == b.Kind && a.Failure == b.Failure && a.Until.Equal(b.Until)
}

// CheckSignIn asks Claude Code whether it is signed in (its cached answer
// unless a sign-in or the locator's Refresh dropped it). An answer that a
// later check, or a run's own finding, overtook is dropped.
func (c *Controller) CheckSignIn() {
	c.signInGen++
	g := c.signInGen
	if c.locator.Locate() == "" {
		c.setSignedIn(assistantpanel.SignIn{})
		return
	}
	c.locator.SignedIn(func(s assistantpanel.SignIn) {
		c.signInAnswers++
		if g != c.signInGen {
			return
		}
		c.setSignedIn(s)
	})
}

// RecheckSignIn asks afresh (after a sign-in elsewhere, such as in a
// terminal).
func (c *Controller) RecheckSignIn() {
	c.locator.Refresh()
	c.CheckSignIn()
}

// learnSignedIn is what a run learnt about the sign-in, which overtakes
// any check under way.
func (c *Controller) learnSignedIn(s assistantpanel.SignIn) {
	c.signInGen++
	c.setSignedIn(s)
}

func (c *Controller) setSignedIn(s assistantpanel.SignIn) {
	if !s.Known {
		s = assistantpanel.SignIn{}
	}
	if s == c.signedIn {
		return
	}
	c.signedIn = s
	c.publish()
}

// What it can do

// CanRun says whether triage is available, Claude Code is there and so is
// the bridge.
func (c *Controller) CanRun() bool {
	return c.available() && c.bridge != "" && c.locator.Locate() != ""
}

// ConsentGiven says whether both consents are given and the board's
// assistant preference is on.
func (c *Controller) ConsentGiven() bool {
	p, ok := c.prefs.Current()
	return c.settings.AssistantConsent() && c.settings.BoardTriageConsent() && ok && p.Assistant
}

// NeedsConsent says whether a manual run would ask for consent first.
func (c *Controller) NeedsConsent() bool { return !c.ConsentGiven() }

// WantsBoardData says whether the board source should run even while the
// board is not shown: the schedule learns the queue only from its
// snapshots. True while automatic triage is on, consent is given and
// triage can run.
func (c *Controller) WantsBoardData() bool {
	p, ok := c.prefs.Current()
	return ok && p.AutoTriage && c.ConsentGiven() && c.CanRun()
}

// GiveConsent is the user's agreement: the board's assistant preference
// goes on and, once the daemon stored it, both consents are kept (the
// board's sheet grants the panel's consent too: one sheet, both keys).
// done (may be nil) gets true once stored; false sets no key, and a
// refusal is reported through the preferences' OnError unless quiet (a run
// reports its own failure).
func (c *Controller) GiveConsent(quiet bool, done func(stored bool)) {
	identity := 0
	if c.ConsentIdentity != nil {
		identity = c.ConsentIdentity()
	}
	current := func() bool { return c.ConsentIdentity == nil || c.ConsentIdentity() == identity }
	c.granting = true
	keep := func() {
		c.settings.SetAssistantConsent(true)
		c.settings.SetBoardTriageConsent(true)
	}
	end := func(stored bool) {
		c.granting = false
		if done != nil {
			done(stored)
		}
		if !current() {
			c.repairAssistantPreference()
		}
	}
	if p, ok := c.prefs.Current(); ok && p.Assistant && !c.prefs.Writing() {
		keep()
		end(true)
		return
	}
	c.prefs.Update(quiet, func(p *api.BoardPreferences) { p.Assistant = true }, func(stored bool) {
		stored = stored && current()
		if stored {
			keep()
		}
		end(stored)
	})
}

// WithdrawConsent is the user withdrawing the board's consent (Preferences
// → AI): a run under way stops, the board's assistant preference goes off
// (its notes no longer count), so does automatic triage (a consent given
// again later must not bring back runs the user did not turn on again),
// and the assistant's consent for the panel stays.
func (c *Controller) WithdrawConsent() {
	c.Cancel()
	c.settings.SetBoardTriageConsent(false)
	c.prefs.Update(false, func(p *api.BoardPreferences) {
		p.Assistant = false
		p.AutoTriage = false
	}, nil)
}

// View is the Triage control and the status strip.
func (c *Controller) View() View {
	now := c.Now()
	todayKnown := c.board.Known && !c.board.CountedAt.IsZero() && sameDay(c.board.CountedAt, now, c.location)
	p, ok := c.prefs.Current()
	assistantOn := c.ConsentGiven()
	if c.board.Known {
		assistantOn = c.board.AssistantOn
	}
	return ViewOf(ViewInputs{
		Shown:          c.available(),
		ClaudeFound:    c.locator.Locate() != "",
		Bridge:         c.bridge != "",
		SignedIn:       c.signedIn,
		NeedsConsent:   c.NeedsConsent(),
		AssistantOn:    assistantOn,
		State:          c.state,
		LastRun:        c.board.LastRun,
		AutoTriage:     ok && p.AutoTriage,
		Pause:          c.autoPause,
		BackendFailed:  !ok && c.prefs.LastLoadFailed(),
		AnnotatedToday: c.board.AnnotatedToday,
		TodayKnown:     todayKnown,
		BoardPhase:     c.viewBoardPhase(),
		SigningIn:      c.locator.SigningIn(),
		UsageKnown:     c.board.Known,
		Usage24h:       c.board.Usage24h,
		Queue:          c.board.Queue,
		QueueKnown:     c.board.Known && c.board.AssistantOn,
		Number:         c.number,
		Now:            now,
	}, c.tr)
}

// viewBoardPhase is the board's phase for the view: off as the daemon's
// preferences say once known (they are newer than a snapshot), else as the
// board last reported; PhaseLoading for "not known".
func (c *Controller) viewBoardPhase() board.Phase {
	if !c.phaseKnown {
		return board.PhaseLoading
	}
	p, ok := c.prefs.Current()
	if !ok || c.boardPhase == board.PhaseUnsupported {
		return c.boardPhase
	}
	if !p.Enabled {
		return board.PhaseOff
	}
	if c.boardPhase == board.PhaseOff {
		return board.PhaseLoading
	}
	return c.boardPhase
}

// publish reports a change, and keeps the clock of relative times running
// exactly while the view names one.
func (c *Controller) publish() {
	c.observers.notify()
	c.updateClock()
}

func (c *Controller) updateClock() {
	if !c.View().RelativeTime {
		c.clockGen++
		c.clockRunning = false
		return
	}
	if c.clockRunning {
		return
	}
	c.clockRunning = true
	g := c.clockGen
	c.loop.After(c.clockTick, func() {
		if g != c.clockGen {
			return
		}
		// Published again; updateClock starts the next tick while the
		// view still names a relative time.
		c.clockRunning = false
		c.publish()
	})
}

// AutoInputs are the rule's inputs as the controller knows them: the
// preferences, the availability, the sign-in, the consents, its run, the
// board's queue and counts, and the daemon's last automatic run as the
// last attempt (the schedule sets the trigger, the clock, its own last
// attempt and the failures).
func (c *Controller) AutoInputs() AutoInputs {
	p, ok := c.prefs.Current()
	i := AutoInputs{
		Trigger:        Automatic,
		Enabled:        ok && p.AutoTriage,
		Available:      c.CanRun(),
		SignedIn:       c.signedIn,
		Consent:        c.ConsentGiven(),
		Running:        c.state.Active(),
		AnnotatedToday: c.board.AnnotatedToday,
		CountedAt:      c.board.CountedAt,
		DailyCap:       api.DefaultBoardAutoTriageDailyCases,
		Minutes:        api.DefaultBoardAutoTriageMinutes,
		Now:            c.Now(),
	}
	if ok {
		i.DailyCap, i.Minutes = p.AutoTriageDailyCases, p.AutoTriageMinutes
	}
	if c.board.Known && c.board.AssistantOn {
		i.Queue = c.board.Queue
	}
	if r := c.board.LastRun; r != nil && r.Trigger == api.TriggerAuto {
		i.LastAttempt = r.At
	}
	return i
}

// A run

// Start starts a run of trigger t (see the type's comment); limit is the
// most cases it asks for (0 or more than assistant.TriageBatch:
// assistant.TriageBatch). False when one is active.
func (c *Controller) Start(t Trigger, limit int) bool {
	if c.state.Active() {
		return false
	}
	c.gen++
	my := c.gen
	c.runID = ""
	c.annotateCalls = map[string]bool{}
	c.refused = 0
	c.usage = assistant.UsageTally{}
	c.limitHit = false
	c.permitted = false
	c.queueHadCases = false
	if limit <= 0 || limit > assistant.TriageBatch {
		limit = assistant.TriageBatch
	}
	c.runLimit = limit
	c.setState(Starting(t))
	c.loop.Post(func() { c.needs(my, t, limit) })
	return true
}

// Cancel ends the run under way as cancelled; one that already reached its
// limit and waits for its result ends at once as the success it is.
func (c *Controller) Cancel() {
	if !c.state.Active() {
		return
	}
	t := c.state.Trigger
	c.gen++
	c.request.Cancel()
	id := c.runID
	c.runID = ""
	if c.limitHit {
		c.finish(t, nil, id)
		return
	}
	f := board.FailCancelled
	c.finish(t, &f, id)
}

// CancelAndEnd is Cancel, then waits until the board.runEnd calls on their
// way were answered, at most wait: quitting calls it before the connection
// stops, and never hangs on a daemon that does not answer. It blocks the
// main loop; what the answers post (OnRefresh) runs later.
func (c *Controller) CancelAndEnd(wait time.Duration) {
	c.Cancel()
	pending := make([]chan struct{}, 0, len(c.endDone))
	for _, ch := range c.endDone {
		pending = append(pending, ch)
	}
	if len(pending) == 0 {
		return
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for _, ch := range pending {
		select {
		case <-ch:
		case <-timer.C:
			return
		}
	}
}

// needs is step 1: what the run needs.
func (c *Controller) needs(my int, t Trigger, limit int) {
	if my != c.gen {
		return
	}
	switch {
	case !c.available():
		c.fail(my, t, board.FailAssistantOff)
		return
	case c.bridge == "":
		c.fail(my, t, board.FailToolsMissing)
		return
	case c.locator.Locate() == "":
		c.fail(my, t, board.FailNotFound)
		return
	}
	if _, ok := c.prefs.Current(); !ok {
		c.prefs.Load(func(bool) {
			if my != c.gen {
				return
			}
			// A daemon that does not answer the board's preferences (or
			// does not know the board) cannot take a consent: no sheet.
			if _, ok := c.prefs.Current(); !ok {
				c.fail(my, t, board.FailBackend)
				return
			}
			c.consent(my, t, limit)
		})
		return
	}
	c.consent(my, t, limit)
}

// consent is step 1's consent: asked for a manual run, never for an
// automatic one.
func (c *Controller) consent(my int, t Trigger, limit int) {
	if c.ConsentGiven() {
		c.permit(my, t, limit)
		return
	}
	if t != Manual || c.Consent == nil {
		c.fail(my, t, board.FailDeclined)
		return
	}
	c.Consent(func(allowed bool) {
		if my != c.gen {
			return
		}
		if !allowed {
			c.fail(my, t, board.FailDeclined)
			return
		}
		c.GiveConsent(true, func(stored bool) {
			if my != c.gen {
				return
			}
			if !stored {
				c.fail(my, t, board.FailBackend)
				return
			}
			c.permit(my, t, limit)
		})
	})
}

// permit is the rest of step 1: the queue and the sign-in.
func (c *Controller) permit(my int, t Trigger, limit int) {
	c.permitted = true
	// A consent lost while the sheet was up stops it here.
	if !c.ConsentGiven() {
		c.fail(my, t, board.FailDeclined)
		return
	}
	// The queue is known only from a board that listed with the assistant
	// on.
	queueKnown := c.board.Known && c.board.AssistantOn
	if queueKnown && c.board.Queue <= 0 {
		c.fail(my, t, board.FailNothingToDo)
		return
	}
	c.queueHadCases = queueKnown && c.board.Queue > 0
	if t == Manual {
		c.locator.Refresh()
	}
	c.locator.SignedIn(func(s assistantpanel.SignIn) {
		if my != c.gen {
			return
		}
		c.learnSignedIn(s)
		if signedOut(s) {
			c.fail(my, t, board.FailNotSignedIn)
			return
		}
		c.startRun(my, t, limit, queueKnown)
	})
}

// startRun is step 2: the daemon's run, then step 3.
func (c *Controller) startRun(my int, t Trigger, limit int, queueKnown bool) {
	caller := c.caller
	source := assistant.TriageSource
	if c.Source != nil {
		source = c.Source()
	}
	go func() {
		var r api.BoardRunStartResult
		err := callWith(caller, api.MethodBoardRunStart, api.BoardRunStartParams{Trigger: Wire(t), Source: source}, &r)
		c.loop.Post(func() {
			if err != nil {
				c.log.Info("board.runStart", "err", err)
				c.fail(my, t, board.FailBackend)
				return
			}
			if my != c.gen {
				// Cancelled while it started: it is ended as such.
				c.endRun(r.RunID, api.RunCancelled, nil)
				return
			}
			c.ask(my, t, limit, queueKnown, r.RunID)
		})
	}()
}

// ask is step 3: the request.
func (c *Controller) ask(my int, t Trigger, limit int, queueKnown bool, id api.BoardRunID) {
	c.runID = id
	total := limit
	if queueKnown {
		total = min(c.board.Queue, limit)
	}
	c.setState(Running(t, 0, total))
	c.log.Info("board triage: run started", "trigger", string(Wire(t)))
	drafts := assistant.TriageDrafts(t)
	modelID := ""
	if c.ModelID != nil {
		modelID = c.ModelID()
	}
	c.request.StartCall(assistantpanel.Call{
		SystemPrompt: assistant.TriageSystemPrompt(c.Language(), c.Today()),
		Message:      assistant.TriageMessage(limit, drafts),
		Tools: &assistantpanel.Tools{
			Bridge: c.bridge, Socket: c.socket,
			BridgeArgs: assistant.TriageBridgeArgs(string(id), limit),
			Allowed:    assistant.TriageToolsWith(drafts),
		},
		Timeout: c.Timeout,
		Model:   assistant.ParseModel(string(c.settings.BoardTriageModel())),
		ModelID: modelID,
		OnTool:  func(e assistant.Event) { c.tool(my, t, e) },
		OnUsage: func(e assistant.Event) { c.counted(my, e) },
	}, func(o assistantpanel.Outcome) { c.answered(my, t, o) })
}

// tool counts an annotate_case of run my, accepted or refused, until the
// accepted ones reach the run's limit (limitReached).
func (c *Controller) tool(my int, t Trigger, e assistant.Event) {
	if my != c.gen || c.limitHit || c.state.Kind != StateRunning {
		return
	}
	switch {
	case e.Kind == assistant.EventToolUse && e.Tool == assistant.TriageAnnotateTool:
		c.annotateCalls[e.ToolUseID] = true
	case e.Kind == assistant.EventToolResult && c.annotateCalls[e.ToolUseID]:
		delete(c.annotateCalls, e.ToolUseID)
		if e.IsError {
			c.refused++
			return
		}
		done := c.state.Done + 1
		c.setState(Running(t, done, c.state.Total))
		if done >= c.runLimit {
			c.limitReached(my, t)
		}
	}
}

// counted counts the usage an event of run my carries.
func (c *Controller) counted(my int, e assistant.Event) {
	if my == c.gen {
		c.usage.Add(e)
	}
}

// limitReached: the run's limit of accepted notes is reached, and the run
// has succeeded whatever the model does next. The bridge accepts no more
// notes and tells the model to stop, so the request goes on until Claude
// Code's result (answered), which carries the whole run's usage, for at
// most Grace; then it is cancelled and the run ends with the usage seen so
// far, a lower bound.
func (c *Controller) limitReached(my int, t Trigger) {
	c.log.Info("board triage: the run's limit of notes is reached", "limit", c.runLimit)
	c.limitHit = true
	c.learnSignedIn(assistantpanel.SignIn{Known: true, SignedIn: true})
	c.graceGen++
	g := c.graceGen
	c.loop.After(c.Grace, func() {
		if g != c.graceGen || my != c.gen || !c.limitHit {
			return
		}
		c.log.Info("board triage: no result after the limit in time")
		c.gen++
		c.request.Cancel()
		id := c.runID
		c.runID = ""
		c.finish(t, nil, id)
	})
}

// answered: the request of run my ended.
func (c *Controller) answered(my int, t Trigger, o assistantpanel.Outcome) {
	if my != c.gen {
		return
	}
	notSignedIn := o.Kind == assistantpanel.OutcomeFailed && o.Failure.Kind == assistantpanel.FailureNotSignedIn
	if c.limitHit {
		// At its limit the run has succeeded, whatever the result says;
		// its usage was counted before this.
		if notSignedIn {
			c.learnSignedIn(assistantpanel.SignIn{Known: true})
		}
		id := c.runID
		c.runID = ""
		c.finish(t, nil, id)
		return
	}
	var failure *board.TriageFailure
	set := func(f board.TriageFailure) { failure = &f }
	switch o.Kind {
	case assistantpanel.OutcomeAnswered:
		if c.state.Kind == StateRunning && c.state.Done == 0 {
			if c.refused > 0 {
				set(board.FailNotesRefused)
			} else if t == Automatic && c.queueHadCases {
				set(board.FailNoProgress)
			}
		}
	case assistantpanel.OutcomeDeclined:
		set(board.FailDeclined)
	case assistantpanel.OutcomeFailed:
		switch o.Failure.Kind {
		case assistantpanel.FailureNotFound:
			set(board.FailNotFound)
		case assistantpanel.FailureNotSignedIn:
			set(board.FailNotSignedIn)
		case assistantpanel.FailureToolsMissing:
			set(board.FailToolsMissing)
		default:
			if o.Failure.TimedOut() {
				set(board.FailTimeout)
			} else {
				set(board.FailStopped)
			}
		}
	}
	switch {
	case notSignedIn:
		c.learnSignedIn(assistantpanel.SignIn{Known: true})
	case o.Kind == assistantpanel.OutcomeAnswered:
		c.learnSignedIn(assistantpanel.SignIn{Known: true, SignedIn: true})
	}
	id := c.runID
	c.runID = ""
	c.finish(t, failure, id)
}

// finish ends the run with failure (nil: a success): the daemon's run is
// ended, the board asked again, the end reported and the state set.
func (c *Controller) finish(t Trigger, failure *board.TriageFailure, run api.BoardRunID) {
	c.graceGen++
	c.limitHit = false
	annotated := 0
	if c.state.Kind == StateRunning {
		annotated = c.state.Done
	}
	if run != "" {
		var class api.BoardRunError
		if failure != nil {
			class = RunError(*failure)
		}
		var usage *api.BoardUsage
		if u, ok := c.usage.Total(); ok {
			usage = &api.BoardUsage{
				InputTokens: u.InputTokens, OutputTokens: u.OutputTokens,
				CacheCreationInputTokens: u.CacheCreationInputTokens, CacheReadInputTokens: u.CacheReadInputTokens,
			}
		}
		c.endRun(run, class, usage)
	}
	// The end first, while State still says active: the schedule counts
	// the failure before the state's change asks it again.
	c.ended = Ended{Trigger: t}
	if failure != nil {
		c.ended.Failed, c.ended.Failure = true, *failure
	}
	c.hasEnded = true
	c.endedObs.notify()
	c.permitted = false
	if failure != nil {
		c.log.Info("board triage: ended", "failure", int(*failure))
		c.setState(Failed(t, *failure, c.Now()))
		return
	}
	c.log.Info("board triage: ended", "annotated", annotated, "refused", c.refused)
	c.setState(Finished(t, annotated, c.refused, c.Now()))
}

// fail is a failure before the daemon's run started.
func (c *Controller) fail(my int, t Trigger, f board.TriageFailure) {
	if my != c.gen {
		return
	}
	if f == board.FailNotSignedIn {
		c.learnSignedIn(assistantpanel.SignIn{Known: true})
	}
	c.finish(t, &f, "")
}

// endRun is board.runEnd (usage nil: not known, left out), then the board
// listed again.
func (c *Controller) endRun(id api.BoardRunID, class api.BoardRunError, usage *api.BoardUsage) {
	c.ending++
	c.nextEnd++
	key := c.nextEnd
	done := make(chan struct{})
	c.endDone[key] = done
	caller := c.caller
	go func() {
		err := callWith(caller, api.MethodBoardRunEnd, api.BoardRunEndParams{RunID: id, Error: class, Usage: usage}, nil)
		close(done)
		c.loop.Post(func() {
			if err != nil {
				c.log.Info("board.runEnd", "err", err)
			}
			c.ending--
			delete(c.endDone, key)
			if c.OnRefresh != nil {
				c.OnRefresh()
			}
		})
	}()
}
