// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package boardtriage

import (
	"log/slog"
	"time"

	"github.com/schotek/malachi/ui/internal/assistantpanel"
	"github.com/schotek/malachi/ui/internal/board"
)

// AutoTarget is what the automatic triage's schedule reads and starts: the
// application's Controller (the tests put a stand-in here).
type AutoTarget interface {
	// AutoInputs are the rule's inputs as the target knows them; the
	// schedule sets the trigger, the clock, its own last attempt and the
	// failures.
	AutoInputs() AutoInputs
	// BoardRevision changes whenever the board reported new triage data.
	BoardRevision() int
	// LastEnded is how the run that ended last ended.
	LastEnded() (Ended, bool)
	// Start starts an automatic run of at most limit cases; false when one
	// is active.
	Start(t Trigger, limit int) bool
	// SetAutoPause says why automatic triage pauses, for the status strip
	// (nil: it does not).
	SetAutoPause(p *board.Pause)
	// RecheckSignIn asks whether Claude Code is signed in afresh (after a
	// sign-in elsewhere, such as in a terminal).
	RecheckSignIn()
	Observe(f func()) (remove func())
	ObserveEnded(f func()) (remove func())
}

// The schedule's times.
const (
	// DefaultDebounce is how long new board data waits for more before the
	// rule is asked.
	DefaultDebounce = time.Minute
	// DefaultHeartbeat is the longest time between two decisions while
	// automatic triage is on.
	DefaultHeartbeat = 30 * time.Minute
)

// Scheduler runs the board's triage on its own (Decide) while the
// application runs, once for the whole application (macOS
// BoardAutoTriageScheduler).
//
// It decides again whenever its target reports a change: at once for a
// change of the preferences, the consents, the sign-in, the availability
// or the run; after Debounce (a minute) for new board data, so that a burst
// of new mail gives one run; at the time a wait names; and every Heartbeat
// while automatic triage is on (asking the sign-in afresh while Claude
// Code is signed out). A run decision starts the target's automatic run.
//
// It remembers, in memory only, when it last started an automatic run and
// how many failed in a row (the back-off): a success or a manual run
// resets that. A cancelled run, an empty queue, a declined consent or the
// assistant off are no failures (CountsAsFailure). After a restart the
// daemon's last automatic run stands in for the last attempt; nothing else
// persists. Timers go through the Loop. Main loop only.
type Scheduler struct {
	// Now is the clock; Location where days are counted (nil: time.Local);
	// Debounce and Heartbeat as above. Set them before Start.
	Now                 func() time.Time
	Location            *time.Location
	Debounce, Heartbeat time.Duration

	target AutoTarget
	loop   assistantpanel.Loop
	log    *slog.Logger

	decision Decision
	decided  bool
	// wakeAt is when a wake is due, while waking; wakeGen retires it.
	wakeAt  time.Time
	waking  bool
	wakeGen int
	// debouncing: new board data waits; debounceGen retires the wait.
	debouncing  bool
	debounceGen int

	failures       int
	lastFailure    board.TriageFailure
	hasLastFailure bool
	lastAttempt    time.Time

	removes      []func()
	seenRevision int
	started      bool
	deciding     bool
	evaluations  int
}

// NewScheduler is a schedule for target, not started.
func NewScheduler(target AutoTarget, loop assistantpanel.Loop, log *slog.Logger) *Scheduler {
	return &Scheduler{
		Now: time.Now, Debounce: DefaultDebounce, Heartbeat: DefaultHeartbeat,
		target: target, loop: loop, log: orDiscard(log),
	}
}

// Decision is the last decision; false before the first.
func (s *Scheduler) Decision() (Decision, bool) { return s.decision, s.decided }

// WakeAt is when it decides again (a wait, or the heartbeat); false when
// nothing is scheduled.
func (s *Scheduler) WakeAt() (time.Time, bool) { return s.wakeAt, s.waking }

// Failures is the automatic runs that failed in a row.
func (s *Scheduler) Failures() int { return s.failures }

// LastAttempt is when this schedule last started an automatic run; zero
// before.
func (s *Scheduler) LastAttempt() time.Time { return s.lastAttempt }

// Debouncing says whether new board data waits for the debounce.
func (s *Scheduler) Debouncing() bool { return s.debouncing }

// Start starts listening and decides once.
func (s *Scheduler) Start() {
	if s.started {
		return
	}
	s.started = true
	s.seenRevision = s.target.BoardRevision()
	s.removes = []func(){
		s.target.Observe(s.changed),
		s.target.ObserveEnded(s.ended),
	}
	s.Evaluate()
}

// Stop stops: nothing is decided or started any more.
func (s *Scheduler) Stop() {
	s.started = false
	for _, r := range s.removes {
		r()
	}
	s.removes = nil
	s.debounceGen++
	s.debouncing = false
	s.cancelWake()
	s.target.SetAutoPause(nil)
}

func (s *Scheduler) changed() {
	if !s.started || s.deciding {
		return
	}
	if rev := s.target.BoardRevision(); rev != s.seenRevision {
		s.seenRevision = rev
		if s.debouncing {
			return
		}
		s.debouncing = true
		s.debounceGen++
		g := s.debounceGen
		s.loop.After(s.Debounce, func() {
			if g != s.debounceGen {
				return
			}
			s.debouncing = false
			s.Evaluate()
		})
		return
	}
	s.Evaluate()
}

func (s *Scheduler) ended() {
	if !s.started {
		return
	}
	e, ok := s.target.LastEnded()
	if !ok {
		return
	}
	switch {
	case e.Trigger == Manual || !e.Failed:
		s.failures = 0
		s.hasLastFailure = false
	case e.Failure == board.FailLimit:
		// The plan's usage limit: one step longer, not doubling on until a
		// day, since the limit lifts on its own.
		s.failures = 1
		s.lastFailure, s.hasLastFailure = e.Failure, true
	case CountsAsFailure(e.Failure):
		s.failures++
		s.lastFailure, s.hasLastFailure = e.Failure, true
	}
	s.Evaluate()
}

// Evaluate asks the rule now and acts on it.
func (s *Scheduler) Evaluate() {
	if !s.started || s.deciding {
		return
	}
	s.deciding = true
	defer func() { s.deciding = false }()
	s.evaluations++
	i := s.target.AutoInputs()
	i.Trigger = Automatic
	i.Now = s.Now()
	if !s.lastAttempt.IsZero() && (i.LastAttempt.IsZero() || i.LastAttempt.Before(s.lastAttempt)) {
		i.LastAttempt = s.lastAttempt
	}
	i.Failures = s.failures
	d := Decide(i, s.Location)
	s.decision, s.decided = d, true
	s.target.SetAutoPause(s.pause(d))
	switch d.Kind {
	case DecideRun:
		s.lastAttempt = i.Now
		s.cancelWake()
		if s.target.Start(Automatic, d.Limit) {
			s.log.Info("board triage: automatic run", "limit", d.Limit)
		}
	case DecideWait:
		at := i.Now.Add(s.Heartbeat)
		if d.Until.Before(at) {
			at = d.Until
		}
		s.schedule(at)
	case DecideOff:
		if d.Off == OffSwitchedOff {
			s.cancelWake()
		} else {
			s.schedule(i.Now.Add(s.Heartbeat))
		}
	}
}

// pause is the pause the status strip shows for decision d.
func (s *Scheduler) pause(d Decision) *board.Pause {
	switch {
	case d.Kind == DecideOff && d.Off == OffSignedOut:
		return &board.Pause{Kind: board.PauseSignedOut}
	case d.Kind == DecideOff && d.Off == OffUnavailable:
		return &board.Pause{Kind: board.PauseUnavailable}
	case d.Kind == DecideOff && d.Off == OffNoConsent:
		return &board.Pause{Kind: board.PauseNoConsent}
	case d.Kind == DecideWait && s.failures > 0 && s.hasLastFailure:
		return &board.Pause{Kind: board.PauseFailed, Failure: s.lastFailure, Until: d.Until}
	}
	return nil
}

// schedule decides again at at (a wait, or the heartbeat).
func (s *Scheduler) schedule(at time.Time) {
	if s.waking && s.wakeAt.Equal(at) {
		return
	}
	s.cancelWake()
	s.wakeAt, s.waking = at, true
	g := s.wakeGen
	s.loop.After(max(0, at.Sub(s.Now())), func() {
		if g != s.wakeGen {
			return
		}
		s.waking, s.wakeAt = false, time.Time{}
		if s.decided && s.decision.Kind == DecideOff && s.decision.Off == OffSignedOut {
			s.target.RecheckSignIn()
		}
		s.Evaluate()
	})
}

func (s *Scheduler) cancelWake() {
	s.wakeGen++
	s.waking, s.wakeAt = false, time.Time{}
}
