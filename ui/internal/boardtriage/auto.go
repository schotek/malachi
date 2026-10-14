// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package boardtriage

// When the board's triage runs on its own (docs/api.md §4.13, the
// autoTriage* preferences, which the daemon only stores): the pure rule
// (macOS Board/BoardAutoTriage.swift). Scheduler gathers the inputs and
// acts on the decision; Controller runs it.
//
// An automatic run starts when automatic triage is on, the assistant can
// run (shown, Claude Code found, the bridge beside the application) and is
// not known to be signed out, both consents are given and the board's
// assistant preference is on, no run is under way, the triage queue has
// cases, the day's cap of cases for automatic runs is not used up, and at
// least autoTriageMinutes passed since the last automatic attempt (after
// Failures failed automatic runs in a row that interval doubles each time,
// at most a day). A success or a manual run resets the failures. A manual
// run ignores the switch, the interval, the cap, the back-off and the queue
// (its consent is asked when it starts).

import (
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/assistantpanel"
	"github.com/schotek/malachi/ui/internal/board"
)

// MaxInterval is the longest wait between two automatic attempts,
// back-off included.
const MaxInterval = 24 * time.Hour

// AutoInputs are what Decide reads (macOS Board.AutoTriage.Inputs).
type AutoInputs struct {
	Trigger Trigger
	// Enabled is the autoTriage preference.
	Enabled bool
	// Available: the assistant is shown, Claude Code found and the bridge
	// is beside the application.
	Available bool
	// SignedIn is whether Claude Code is signed in; not known does not
	// hold a run back (the run asks again).
	SignedIn assistantpanel.SignIn
	// Consent: both consents and the board's assistant preference.
	Consent bool
	// Running: this application's own run is under way. A run the daemon
	// reports open (another client's, or one left open) does not count:
	// it never holds the scheduler back (decision H3-2).
	Running bool
	// Queue is the cases board.queue would offer (BoardTriage.Queue).
	Queue int
	// AnnotatedToday is the cases automatic runs annotated on the day of
	// CountedAt, when that was reported; on an earlier day it counts as 0.
	// A zero CountedAt counts as reported today.
	AnnotatedToday int
	CountedAt      time.Time
	// DailyCap is autoTriageDailyCases (0 = none); Minutes is
	// autoTriageMinutes.
	DailyCap, Minutes int
	// LastAttempt is when the last automatic run started (or was tried);
	// zero before the first.
	LastAttempt time.Time
	// Failures is the automatic runs that failed in a row since the last
	// success or manual run.
	Failures int
	Now      time.Time
}

// OffReason is why no run starts and nothing is waited for: the next
// change of the inputs decides again.
type OffReason int

// The reasons.
const (
	// OffSwitchedOff: automatic triage is off.
	OffSwitchedOff OffReason = iota
	// OffUnavailable: the assistant is off, Claude Code was not found, or
	// the bridge is missing.
	OffUnavailable
	// OffSignedOut: Claude Code is signed out.
	OffSignedOut
	// OffNoConsent: a consent is missing, or the board's assistant
	// preference is off.
	OffNoConsent
	// OffRunning: a run is under way.
	OffRunning
	// OffEmptyQueue: nothing waits for the assistant.
	OffEmptyQueue
	// OffNoDailyCases: the daily cap is 0; automatic runs annotate
	// nothing.
	OffNoDailyCases
)

// DecisionKind is what Decide decided.
type DecisionKind int

// The kinds of decisions.
const (
	// DecideRun: start a run of at most Limit cases.
	DecideRun DecisionKind = iota
	// DecideWait: decide again at Until (the interval, the back-off, the
	// next day for a cap used up).
	DecideWait
	// DecideOff: Off says why nothing runs.
	DecideOff
)

// Decision is what Decide decided; only the fields of its Kind are set.
type Decision struct {
	Kind  DecisionKind
	Limit int
	Until time.Time
	Off   OffReason
}

// Equal says whether d and o are the same decision (times compared as
// instants).
func (d Decision) Equal(o Decision) bool {
	return d.Kind == o.Kind && d.Limit == o.Limit && d.Until.Equal(o.Until) && d.Off == o.Off
}

func run(limit int) Decision        { return Decision{Kind: DecideRun, Limit: limit} }
func wait(until time.Time) Decision { return Decision{Kind: DecideWait, Until: until} }
func off(r OffReason) Decision      { return Decision{Kind: DecideOff, Off: r} }

// Interval is the interval after failures failed automatic runs in a row:
// minutes (within the daemon's range), doubled per failure, at most
// MaxInterval.
func Interval(minutes, failures int) time.Duration {
	m := min(max(minutes, api.MinBoardAutoTriageMinutes), api.MaxBoardAutoTriageMinutes)
	d := time.Duration(m) * time.Minute
	for range max(0, failures) {
		d *= 2
		if d >= MaxInterval {
			return MaxInterval
		}
	}
	return min(d, MaxInterval)
}

// sameDay says whether a and b fall on the same day in loc.
func sameDay(a, b time.Time, loc *time.Location) bool {
	ay, am, ad := a.In(loc).Date()
	by, bm, bd := b.In(loc).Date()
	return ay == by && am == bm && ad == bd
}

// Decide is the rule (see the top of this file); days are loc's (nil:
// time.Local), as the daemon counts AnnotatedToday by its local day.
func Decide(i AutoInputs, loc *time.Location) Decision {
	if loc == nil {
		loc = time.Local
	}
	if i.Trigger == Manual {
		switch {
		case !i.Available:
			return off(OffUnavailable)
		case signedOut(i.SignedIn):
			return off(OffSignedOut)
		case i.Running:
			return off(OffRunning)
		}
		return run(assistant.TriageBatch)
	}
	switch {
	case !i.Enabled:
		return off(OffSwitchedOff)
	case !i.Available:
		return off(OffUnavailable)
	case signedOut(i.SignedIn):
		return off(OffSignedOut)
	case !i.Consent:
		return off(OffNoConsent)
	case i.Running:
		return off(OffRunning)
	case i.Queue <= 0:
		return off(OffEmptyQueue)
	case i.DailyCap <= 0:
		return off(OffNoDailyCases)
	}
	remaining := i.DailyCap
	if i.CountedAt.IsZero() || sameDay(i.CountedAt, i.Now, loc) {
		remaining -= max(0, i.AnnotatedToday)
	}
	if remaining <= 0 {
		y, m, d := i.Now.In(loc).Date()
		return wait(time.Date(y, m, d+1, 0, 0, 0, 0, loc))
	}
	if !i.LastAttempt.IsZero() {
		if earliest := i.LastAttempt.Add(Interval(i.Minutes, i.Failures)); i.Now.Before(earliest) {
			return wait(earliest)
		}
	}
	return run(min(assistant.TriageBatch, remaining))
}

// CountsAsFailure says whether an automatic run that failed with f makes
// the next one wait longer. A cancelled run, an empty queue, a declined
// consent or the assistant off are no failures; a run that added no note,
// or whose notes were all refused, is one, so that it does not repeat at
// every interval.
func CountsAsFailure(f board.TriageFailure) bool {
	switch f {
	case board.FailCancelled, board.FailDeclined, board.FailAssistantOff, board.FailNothingToDo:
		return false
	}
	return true
}
