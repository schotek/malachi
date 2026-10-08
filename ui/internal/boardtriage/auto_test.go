// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package boardtriage

import (
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistantpanel"
	"github.com/schotek/malachi/ui/internal/board"
)

// Automatic triage: the pure rule (Decide) as a table, and the schedule
// (Scheduler) against a stand-in target with a clock the test moves
// (manualLoop), so nothing depends on how fast the machine is (macOS
// BoardAutoTriageTests).

// auto are the inputs of an automatic run that may start: on, available,
// signed in, consent, one case waiting, the default cap and interval;
// change adjusts them.
func auto(change func(*AutoInputs)) AutoInputs {
	i := AutoInputs{
		Trigger: Automatic, Enabled: true, Available: true, SignedIn: assistantpanel.SignIn{Known: true, SignedIn: true},
		Consent: true, Queue: 1, DailyCap: api.DefaultBoardAutoTriageDailyCases, Minutes: api.DefaultBoardAutoTriageMinutes,
		Now: t0,
	}
	if change != nil {
		change(&i)
	}
	return i
}

func minutes(m float64) time.Duration { return time.Duration(m * float64(time.Minute)) }

func TestDecide(t *testing.T) {
	next := t0.Add(14 * time.Hour) // 2026-10-02 00:00 UTC
	rows := []struct {
		name   string
		change func(*AutoInputs)
		want   Decision
	}{
		{"all set, first run", nil, run(40)},
		{"switched off", func(i *AutoInputs) { i.Enabled = false }, off(OffSwitchedOff)},
		{"assistant cannot run", func(i *AutoInputs) { i.Available = false }, off(OffUnavailable)},
		{"signed out", func(i *AutoInputs) { i.SignedIn = assistantpanel.SignIn{Known: true} }, off(OffSignedOut)},
		{"sign-in not known counts as signed in", func(i *AutoInputs) { i.SignedIn = assistantpanel.SignIn{} }, run(40)},
		{"no consent", func(i *AutoInputs) { i.Consent = false }, off(OffNoConsent)},
		{"a run under way", func(i *AutoInputs) { i.Running = true }, off(OffRunning)},
		{"empty queue", func(i *AutoInputs) { i.Queue = 0 }, off(OffEmptyQueue)},
		{"no daily cases", func(i *AutoInputs) { i.DailyCap = 0 }, off(OffNoDailyCases)},
		{"the cap is the limit", func(i *AutoInputs) { i.AnnotatedToday, i.DailyCap = 50, 60 }, run(10)},
		{"the batch is the limit", func(i *AutoInputs) { i.AnnotatedToday, i.DailyCap = 0, 1000 }, run(40)},
		{"cap used up: tomorrow", func(i *AutoInputs) { i.AnnotatedToday, i.DailyCap = 60, 60 }, wait(next)},
		{"cap counted yesterday is 0", func(i *AutoInputs) {
			i.AnnotatedToday, i.CountedAt, i.DailyCap = 60, t0.Add(-24*time.Hour), 60
		}, run(40)},
		{"cap counted today counts", func(i *AutoInputs) { i.AnnotatedToday, i.CountedAt, i.DailyCap = 60, t0.Add(-time.Hour), 60 }, wait(next)},
		{"interval not over", func(i *AutoInputs) { i.Minutes, i.LastAttempt = 30, t0.Add(-minutes(10)) }, wait(t0.Add(minutes(20)))},
		{"interval over", func(i *AutoInputs) { i.Minutes, i.LastAttempt = 30, t0.Add(-minutes(30)) }, run(40)},
		{"one failure doubles it", func(i *AutoInputs) { i.Minutes, i.LastAttempt, i.Failures = 30, t0.Add(-minutes(30)), 1 },
			wait(t0.Add(minutes(30)))},
		{"three failures: 4 hours", func(i *AutoInputs) { i.Minutes, i.LastAttempt, i.Failures = 30, t0, 3 }, wait(t0.Add(minutes(240)))},
		{"the back-off stops at a day", func(i *AutoInputs) { i.Minutes, i.LastAttempt, i.Failures = 600, t0, 5 }, wait(t0.Add(24 * time.Hour))},
		{"the switch before everything", func(i *AutoInputs) {
			i.Enabled, i.Available, i.SignedIn = false, false, assistantpanel.SignIn{Known: true}
		}, off(OffSwitchedOff)},
		{"the cap before the interval", func(i *AutoInputs) { i.AnnotatedToday, i.DailyCap, i.LastAttempt = 60, 60, t0 }, wait(next)},
		{"manual ignores the switch, interval, cap, back-off and queue", func(i *AutoInputs) {
			i.Trigger, i.Enabled, i.Queue, i.AnnotatedToday, i.DailyCap, i.LastAttempt, i.Failures = Manual, false, 0, 60, 60, t0, 4
		}, run(40)},
		{"manual without consent runs (it asks)", func(i *AutoInputs) { i.Trigger, i.Consent = Manual, false }, run(40)},
		{"manual: not while one runs", func(i *AutoInputs) { i.Trigger, i.Running = Manual, true }, off(OffRunning)},
		{"manual: not signed out", func(i *AutoInputs) { i.Trigger, i.SignedIn = Manual, assistantpanel.SignIn{Known: true} }, off(OffSignedOut)},
		{"manual: not unavailable", func(i *AutoInputs) { i.Trigger, i.Available = Manual, false }, off(OffUnavailable)},
	}
	for _, r := range rows {
		if got := Decide(auto(r.change), time.UTC); !got.Equal(r.want) {
			t.Errorf("%s: %+v, want %+v", r.name, got, r.want)
		}
	}
	// The day is the location's: at 23:30 in Prague (UTC+2) it is already
	// the next day, and the count read at 10:00 UTC is yesterday's.
	prague := time.FixedZone("CEST", 2*60*60)
	late := t0.Add(13*time.Hour + 30*time.Minute) // 23:30 UTC = 01:30 the next day in Prague
	i := auto(func(i *AutoInputs) { i.AnnotatedToday, i.CountedAt, i.DailyCap, i.Now = 60, t0, 60, late })
	if got := Decide(i, prague); !got.Equal(run(40)) {
		t.Errorf("Prague: %+v", got)
	}
	if got := Decide(i, time.UTC); !got.Equal(wait(t0.Add(14 * time.Hour))) {
		t.Errorf("UTC: %+v", got)
	}
}

func TestInterval(t *testing.T) {
	rows := []struct {
		minutes, failures int
		want              time.Duration
	}{
		{30, 0, 30 * time.Minute}, {30, 1, time.Hour}, {30, 2, 2 * time.Hour}, {30, 100, 24 * time.Hour},
		{1440, 0, 24 * time.Hour},
		// Out of range minutes are taken as the nearest allowed.
		{1, 0, 5 * time.Minute}, {99999, 0, 24 * time.Hour}, {30, -3, 30 * time.Minute},
	}
	for _, r := range rows {
		if got := Interval(r.minutes, r.failures); got != r.want {
			t.Errorf("Interval(%d, %d) = %v", r.minutes, r.failures, got)
		}
	}
}

func TestCountsAsFailure(t *testing.T) {
	for _, f := range board.TriageFailures {
		want := true
		switch f {
		case board.FailCancelled, board.FailDeclined, board.FailAssistantOff, board.FailNothingToDo:
			want = false
		}
		if CountsAsFailure(f) != want {
			t.Errorf("failure %d: %v", f, !want)
		}
	}
}

// The schedule.

// fakeTarget is the schedule's target: inputs the test sets, the runs it
// started.
type fakeTarget struct {
	inputs   AutoInputs
	revision int
	ended    Ended
	hasEnded bool
	starts   []int
	pause    *board.Pause
	rechecks int
	changes  observers
	ends     observers
}

func (f *fakeTarget) AutoInputs() AutoInputs      { return f.inputs }
func (f *fakeTarget) BoardRevision() int          { return f.revision }
func (f *fakeTarget) LastEnded() (Ended, bool)    { return f.ended, f.hasEnded }
func (f *fakeTarget) SetAutoPause(p *board.Pause) { f.pause = p }
func (f *fakeTarget) RecheckSignIn()              { f.rechecks++ }
func (f *fakeTarget) Observe(fn func()) func()    { return f.changes.add(fn) }
func (f *fakeTarget) ObserveEnded(fn func()) func() {
	return f.ends.add(fn)
}

func (f *fakeTarget) Start(_ Trigger, limit int) bool {
	if f.inputs.Running {
		return false
	}
	f.starts = append(f.starts, limit)
	f.inputs.Running = true
	f.changes.notify()
	return true
}

// change changes something but the board.
func (f *fakeTarget) change(fn func(*AutoInputs)) {
	fn(&f.inputs)
	f.changes.notify()
}

// board is new board data.
func (f *fakeTarget) board(queue int) {
	f.inputs.Queue = queue
	f.revision++
	f.changes.notify()
}

// end ends the run as the controller does: the end first.
func (f *fakeTarget) end(t Trigger, failed bool, failure board.TriageFailure) {
	f.ended, f.hasEnded = Ended{Trigger: t, Failed: failed, Failure: failure}, true
	f.ends.notify()
	f.inputs.Running = false
	f.changes.notify()
}

type schedHarness struct {
	t      *testing.T
	target *fakeTarget
	loop   *manualLoop
	s      *Scheduler
}

func newSchedHarness(t *testing.T) *schedHarness {
	target := &fakeTarget{inputs: auto(nil)}
	loop := &manualLoop{now: t0}
	s := NewScheduler(target, loop, discardLog())
	s.Now = func() time.Time { return loop.now }
	s.Location = time.UTC
	return &schedHarness{t: t, target: target, loop: loop, s: s}
}

func (h *schedHarness) decision() Decision {
	d, ok := h.s.Decision()
	if !ok {
		h.t.Fatal("no decision")
	}
	return d
}

func (h *schedHarness) wakeAt() time.Time {
	at, _ := h.s.WakeAt()
	return at
}

func (h *schedHarness) expect(name string, want Decision) {
	h.t.Helper()
	if got := h.decision(); !got.Equal(want) {
		h.t.Errorf("%s: decision %+v, want %+v", name, got, want)
	}
}

func (h *schedHarness) expectPause(name string, want *board.Pause) {
	h.t.Helper()
	if !samePause(h.target.pause, want) {
		h.t.Errorf("%s: pause %+v, want %+v", name, h.target.pause, want)
	}
}

func (h *schedHarness) expectStarts(name string, want ...int) {
	h.t.Helper()
	if len(h.target.starts) != len(want) {
		h.t.Fatalf("%s: starts %v, want %v", name, h.target.starts, want)
	}
	for i := range want {
		if h.target.starts[i] != want[i] {
			h.t.Fatalf("%s: starts %v, want %v", name, h.target.starts, want)
		}
	}
}

// New board data waits a minute for more: a burst gives one run.
func TestSchedulerDebounce(t *testing.T) {
	h := newSchedHarness(t)
	h.target.inputs.Queue = 0
	h.s.Start()
	h.expect("start", off(OffEmptyQueue))
	h.target.board(1)
	h.target.board(2)
	h.target.board(3)
	if !h.s.Debouncing() || len(h.target.starts) != 0 {
		t.Errorf("debouncing %v, starts %v", h.s.Debouncing(), h.target.starts)
	}
	h.loop.advance(time.Minute)
	h.expectStarts("after the debounce", 40)
	if !h.s.LastAttempt().Equal(h.loop.now) || h.s.Debouncing() {
		t.Errorf("last attempt %v, debouncing %v", h.s.LastAttempt(), h.s.Debouncing())
	}
	// While it runs nothing else starts.
	h.target.board(4)
	h.loop.advance(time.Minute)
	h.expect("while it runs", off(OffRunning))
	h.expectStarts("while it runs", 40)
	h.s.Stop()
}

// A change that is not the board's decides at once.
func TestSchedulerPreferencesDecideAtOnce(t *testing.T) {
	h := newSchedHarness(t)
	h.target.inputs.Enabled = false
	h.s.Start()
	h.expect("off", off(OffSwitchedOff))
	if _, ok := h.s.WakeAt(); ok {
		t.Error("a wake while switched off")
	}
	h.target.change(func(i *AutoInputs) { i.Enabled = true })
	h.expectStarts("switched on", 40)
	h.s.Stop()
}

// After a run the next waits for the interval from its start.
func TestSchedulerIntervalBetweenRuns(t *testing.T) {
	h := newSchedHarness(t)
	h.s.Start()
	h.expectStarts("start", 40)
	h.loop.advance(minutes(5))
	h.target.end(Automatic, false, 0)
	h.expect("after the run", wait(t0.Add(minutes(30))))
	if !h.wakeAt().Equal(t0.Add(minutes(30))) {
		t.Errorf("wake at %v", h.wakeAt())
	}
	h.expectPause("after the run", nil)
	h.loop.advance(minutes(25))
	h.expectStarts("the interval over", 40, 40)
	h.s.Stop()
}

// Failed automatic runs double the wait (and say why on the strip); a
// success resets it, and so does a manual run.
func TestSchedulerBackOff(t *testing.T) {
	h := newSchedHarness(t)
	h.s.Start()
	h.expectStarts("start", 40)
	h.target.end(Automatic, true, board.FailTimeout)
	if h.s.Failures() != 1 {
		t.Errorf("failures %d", h.s.Failures())
	}
	until1 := t0.Add(minutes(60))
	h.expect("one failure", wait(until1))
	h.expectPause("one failure", failedPause(board.FailTimeout, until1))
	h.loop.advance(minutes(60))
	h.expectStarts("after the back-off", 40, 40)
	second := h.loop.now
	h.target.end(Automatic, true, board.FailStopped)
	if h.s.Failures() != 2 {
		t.Errorf("failures %d", h.s.Failures())
	}
	h.expect("two failures", wait(second.Add(minutes(120))))
	h.expectPause("two failures", failedPause(board.FailStopped, second.Add(minutes(120))))
	// A manual run resets it; the interval from the last automatic attempt
	// still holds.
	h.target.change(func(i *AutoInputs) { i.Running = true })
	h.target.end(Manual, false, 0)
	if h.s.Failures() != 0 || h.target.pause != nil {
		t.Errorf("after a manual run: failures %d, pause %+v", h.s.Failures(), h.target.pause)
	}
	h.expect("after a manual run", wait(second.Add(minutes(30))))
	// A cancelled run is no failure.
	h.loop.advance(minutes(30))
	h.expectStarts("the interval over", 40, 40, 40)
	h.target.end(Automatic, true, board.FailCancelled)
	if h.s.Failures() != 0 {
		t.Errorf("a cancelled run counted: %d", h.s.Failures())
	}
	// A success resets after failures too.
	h.loop.advance(minutes(30))
	h.expectStarts("again", 40, 40, 40, 40)
	h.target.end(Automatic, true, board.FailNotFound)
	if h.s.Failures() != 1 {
		t.Errorf("failures %d", h.s.Failures())
	}
	h.loop.advance(minutes(60))
	h.expectStarts("after the back-off", 40, 40, 40, 40, 40)
	h.target.end(Automatic, false, 0)
	if h.s.Failures() != 0 || h.target.pause != nil {
		t.Errorf("after a success: failures %d, pause %+v", h.s.Failures(), h.target.pause)
	}
	h.s.Stop()
}

// The day's cap used up: the next try is at midnight, when the count read
// yesterday no longer holds.
func TestSchedulerDailyCap(t *testing.T) {
	h := newSchedHarness(t)
	h.target.inputs.AnnotatedToday = 60
	h.target.inputs.CountedAt = t0
	h.s.Start()
	midnight := t0.Add(14 * time.Hour)
	h.expect("cap used up", wait(midnight))
	// The heartbeat comes first and decides the same.
	if !h.wakeAt().Equal(t0.Add(minutes(30))) {
		t.Errorf("wake at %v", h.wakeAt())
	}
	h.loop.advance(minutes(30))
	h.expect("the heartbeat", wait(midnight))
	if len(h.target.starts) != 0 {
		t.Errorf("starts %v", h.target.starts)
	}
	h.loop.advance(midnight.Sub(h.loop.now))
	h.expectStarts("midnight", 40)
	h.s.Stop()
}

// Signed out: paused, and the heartbeat asks the sign-in afresh.
func TestSchedulerSignedOut(t *testing.T) {
	h := newSchedHarness(t)
	h.target.inputs.SignedIn = assistantpanel.SignIn{Known: true}
	h.s.Start()
	h.expect("signed out", off(OffSignedOut))
	h.expectPause("signed out", pause(board.PauseSignedOut))
	if !h.wakeAt().Equal(t0.Add(minutes(30))) {
		t.Errorf("wake at %v", h.wakeAt())
	}
	h.loop.advance(minutes(30))
	if h.target.rechecks != 1 {
		t.Errorf("rechecks %d", h.target.rechecks)
	}
	h.target.change(func(i *AutoInputs) { i.SignedIn = assistantpanel.SignIn{Known: true, SignedIn: true} })
	h.expectStarts("signed in", 40)
	h.expectPause("signed in", nil)
	// The other pauses.
	h.target.end(Automatic, false, 0)
	h.target.change(func(i *AutoInputs) { i.Consent = false })
	h.expectPause("no consent", pause(board.PauseNoConsent))
	h.target.change(func(i *AutoInputs) { i.Available = false })
	h.expectPause("unavailable", pause(board.PauseUnavailable))
	// Switched off: no pause, nothing scheduled.
	h.target.change(func(i *AutoInputs) { i.Enabled = false })
	h.expectPause("switched off", nil)
	if _, ok := h.s.WakeAt(); ok {
		t.Error("a wake while switched off")
	}
	h.s.Stop()
}

// The daemon's last automatic run stands in for the last attempt after a
// restart; this schedule's own attempt counts when newer.
func TestSchedulerLastAttemptFromTheDaemon(t *testing.T) {
	h := newSchedHarness(t)
	h.target.inputs.LastAttempt = t0.Add(-minutes(10))
	h.s.Start()
	h.expect("the daemon's attempt", wait(t0.Add(minutes(20))))
	h.s.Stop()
}

// Stopped: nothing is decided any more.
func TestSchedulerStop(t *testing.T) {
	h := newSchedHarness(t)
	h.target.inputs.Queue = 0
	h.s.Start()
	h.s.Stop()
	h.target.board(3)
	h.target.change(func(i *AutoInputs) { i.Enabled = true })
	h.loop.advance(minutes(60))
	if len(h.target.starts) != 0 || h.s.Debouncing() {
		t.Errorf("starts %v, debouncing %v", h.target.starts, h.s.Debouncing())
	}
	if _, ok := h.s.WakeAt(); ok {
		t.Error("a wake after Stop")
	}
	if h.target.pause != nil {
		t.Errorf("pause after Stop: %+v", h.target.pause)
	}
}

// The plan's usage limit waits one step longer, however often it repeats.
func TestSchedulerUsageLimitBacksOffOneStep(t *testing.T) {
	h := newSchedHarness(t)
	h.s.Start()
	h.expectStarts("start", 40)
	h.target.end(Automatic, true, board.FailStopped)
	h.loop.advance(minutes(60))
	want := []int{40, 40}
	h.expectStarts("after one failure", want...)
	for i := range 3 {
		at := h.loop.now
		h.target.end(Automatic, true, board.FailLimit)
		if h.s.Failures() != 1 {
			t.Fatalf("limit %d: failures %d", i, h.s.Failures())
		}
		h.expect("limit", wait(at.Add(minutes(60))))
		h.expectPause("limit", failedPause(board.FailLimit, at.Add(minutes(60))))
		h.loop.advance(minutes(60))
		want = append(want, 40)
		h.expectStarts("after the limit", want...)
	}
	h.s.Stop()
}
