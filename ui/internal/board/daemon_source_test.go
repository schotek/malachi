// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/client"
)

// The board from the daemon against a fake daemon: loading, notifications
// with their debounce, one board.list at a time, dropped stale replies,
// optimistic writes and their revert, the phases and the retries, the
// conversations cached by version (macOS DaemonBoardSourceTests.swift, the
// same cases). No test depends on how fast the machine is: the source's
// timers wait until the test fires them (manualLoop), the fake daemon
// holds replies until the test releases them, and "nothing happened" is
// checked once the source is idle (nothing waiting or on its way).

// manualLoop is the main loop of a test: what is posted runs on the test's
// goroutine while it waits (runUntil); a timer waits until fire.
type manualLoop struct {
	mu     sync.Mutex
	queue  []func()
	timers []manualTimer
	wake   chan struct{}
	// ran counts what was posted and has run (the test's goroutine only).
	ran int
}

type manualTimer struct {
	d time.Duration
	f func()
}

func newManualLoop() *manualLoop { return &manualLoop{wake: make(chan struct{}, 1)} }

func (l *manualLoop) Post(f func()) {
	l.mu.Lock()
	l.queue = append(l.queue, f)
	l.mu.Unlock()
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

func (l *manualLoop) After(d time.Duration, f func()) {
	l.mu.Lock()
	l.timers = append(l.timers, manualTimer{d, f})
	l.mu.Unlock()
}

// pending is the durations of the timers waiting, in order (a cancelled
// one too, until it is fired: it then does nothing).
func (l *manualLoop) pending() []time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := []time.Duration{}
	for _, t := range l.timers {
		out = append(out, t.d)
	}
	return out
}

// fire ends every timer waiting.
func (l *manualLoop) fire() {
	l.mu.Lock()
	timers := l.timers
	l.timers = nil
	l.mu.Unlock()
	for _, t := range timers {
		t.f()
	}
}

// runUntil runs what was posted until cond holds; the test fails after ten
// seconds.
func (l *manualLoop) runUntil(t *testing.T, cond func() bool) {
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
			l.ran++
			continue
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting")
		}
		select {
		case <-l.wake:
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// held is what the fake daemon can hold until the test releases it.
type held int

const (
	heldLists held = iota
	heldWrites
	heldGets
)

var t0 = time.Unix(1_790_000_000, 0).UTC()

func wireCase(n string, state api.BoardState, version int64) api.BoardCase {
	return api.BoardCase{
		ID: CaseID("c_" + n), AccountID: "acc_1", ThreadID: api.ThreadID("t_" + n), RuleState: state,
		RuleReason: api.BoardReasonYouAddressed, Visibility: api.BoardLive, Subject: "Subject " + n,
		Person: api.Address{Name: "Person " + n, Address: n + "@example.invalid"}, Date: t0, MessageCount: 2,
		ReplyMessageID: api.MessageID("m_" + n), ReplyFolderID: "f_inbox", LatestMessageID: api.MessageID("m_" + n),
		CanArchive: true, Version: version,
	}
}

func caseID(n string) CaseID { return CaseID("c_" + n) }

func wireCommitment(state api.BoardCommitmentState) api.BoardCommitment {
	return api.BoardCommitment{
		ID: "k_1", CaseID: "c_1", AccountID: "acc_1", MessageID: "m_1", Text: "Send it", Quote: "I will send it",
		State: state, At: t0,
	}
}

// fakeDaemon is the daemon's side of the board: its cases, and what each
// method did. Params and results go through JSON, as on the wire.
type fakeDaemon struct {
	mu           sync.Mutex
	cases        []api.BoardCase
	commitments  []api.BoardCommitment
	accountName  string
	enabled      bool
	ready        bool
	lastRun      *api.BoardRun
	listFailure  error
	writeFailure error
	getFailure   error
	connected    bool
	calls        []string
	deleted      []api.DraftDeleteParams
	moves        []api.MessageMoveParams
	moveFailure  error
	noMoved      bool
	holding      map[held]bool
	gates        map[held][]chan error
}

func newFakeDaemon(connected bool) *fakeDaemon {
	return &fakeDaemon{
		cases:       []api.BoardCase{wireCase("1", api.BoardYou, 1), wireCase("2", api.BoardHot, 1)},
		accountName: "Work", enabled: true, ready: true, connected: connected,
		holding: map[held]bool{}, gates: map[held][]chan error{},
	}
}

func (f *fakeDaemon) set(change func(f *fakeDaemon)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

func (f *fakeDaemon) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == method {
			n++
		}
	}
	return n
}

// hold holds the replies of kind from now on, or lets them through again
// and releases the ones held.
func (f *fakeDaemon) hold(kind held, on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.holding[kind] = on
	if !on {
		for _, g := range f.gates[kind] {
			g <- nil
		}
		f.gates[kind] = nil
	}
}

func (f *fakeDaemon) held(kind held) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.gates[kind])
}

// disconnect drops the connection: every call held fails, and so does
// every call after it until connect.
func (f *fakeDaemon) disconnect() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connected = false
	for kind, gates := range f.gates {
		for _, g := range gates {
			g <- client.ErrDisconnected
		}
		f.gates[kind] = nil
	}
}

func (f *fakeDaemon) connect() { f.set(func(f *fakeDaemon) { f.connected = true }) }

// release lets every call go, for the end of a test.
func (f *fakeDaemon) release() {
	f.hold(heldLists, false)
	f.hold(heldWrites, false)
	f.hold(heldGets, false)
}

// wait blocks while kind is held; called with f.mu locked, returns with it
// locked.
func (f *fakeDaemon) wait(kind held) error {
	if !f.holding[kind] {
		return nil
	}
	g := make(chan error, 1)
	f.gates[kind] = append(f.gates[kind], g)
	f.mu.Unlock()
	err := <-g
	f.mu.Lock()
	return err
}

func roundTrip(from, to any) error {
	b, err := json.Marshal(from)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, to)
}

func (f *fakeDaemon) Call(ctx context.Context, method string, params any, result any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.connected {
		return client.ErrDisconnected
	}
	f.calls = append(f.calls, method)
	answer, err := f.serve(method, params)
	if err != nil {
		return err
	}
	return roundTrip(answer, result)
}

func (f *fakeDaemon) serve(method string, params any) (any, error) {
	switch method {
	case api.MethodAccountList:
		a := api.Account{ID: "acc_1", Config: api.AccountConfig{Name: f.accountName, Email: "me@example.invalid"}, Enabled: true}
		off := api.Account{ID: "acc_2", Config: api.AccountConfig{Name: "Paused", Email: "p@example.invalid"}}
		return api.AccountListResult{Accounts: []api.Account{a, off}}, nil
	case api.MethodBoardList:
		// What the daemon has at the time of the call, sent when released.
		var cases []api.BoardCase
		if f.enabled {
			cases = slices.Clone(f.cases)
		}
		result := api.BoardListResult{
			Cases: cases, Commitments: slices.Clone(f.commitments), Enabled: f.enabled,
			Triage: api.BoardTriage{LastRun: f.lastRun, AnnotatedTodayAuto: 2, Queue: 4}, Ready: f.ready,
		}
		failure := f.listFailure
		if err := f.wait(heldLists); err != nil {
			return nil, err
		}
		return result, failure
	case api.MethodBoardGet:
		var p api.BoardGetParams
		if err := roundTrip(params, &p); err != nil {
			return nil, err
		}
		failure := f.getFailure
		i := slices.IndexFunc(f.cases, func(c api.BoardCase) bool { return c.ID == p.CaseID })
		var c api.BoardCase
		if i >= 0 {
			c = f.cases[i]
		}
		if err := f.wait(heldGets); err != nil {
			return nil, err
		}
		if failure != nil {
			return nil, failure
		}
		if i < 0 {
			return nil, &api.Error{Code: api.CodeCaseNotFound, Message: "no case"}
		}
		m := api.BoardMessage{
			ID: "m_a", FolderID: "f_inbox", From: api.Address{Name: "Ann", Address: "ann@example.invalid"}, Date: t0,
			Text: fmt.Sprintf("version %d", c.Version),
		}
		return api.BoardGetResult{Case: c, Messages: []api.BoardMessage{m}}, nil
	case api.MethodBoardSetState:
		var p api.BoardSetStateParams
		_ = roundTrip(params, &p)
		c, err := f.write(p.CaseID, func(c *api.BoardCase) { c.UserState = p.State })
		return api.BoardSetStateResult{Case: c}, err
	case api.MethodBoardSetDone:
		var p api.BoardSetDoneParams
		_ = roundTrip(params, &p)
		c, err := f.write(p.CaseID, func(c *api.BoardCase) {
			c.Visibility, c.DoneAt, c.RemindAt = api.BoardLive, nil, nil
			if p.Done {
				c.Visibility, c.DoneAt = api.BoardDone, &t0
			}
		})
		return api.BoardSetDoneResult{Case: c}, err
	case api.MethodBoardRemind:
		var p api.BoardRemindParams
		_ = roundTrip(params, &p)
		c, err := f.write(p.CaseID, func(c *api.BoardCase) {
			c.Visibility, c.RemindAt = api.BoardSnoozed, p.Until
			if p.Until == nil {
				c.Visibility = api.BoardLive
			}
		})
		return api.BoardRemindResult{Case: c}, err
	case api.MethodBoardArchive:
		var p api.BoardArchiveParams
		_ = roundTrip(params, &p)
		c, err := f.write(p.CaseID, func(c *api.BoardCase) { c.Visibility, c.CanArchive = api.BoardDone, false })
		moved := []api.BoardMoved{{MessageID: "m_a", FromFolderID: "f_inbox"}, {MessageID: "m_b", FromFolderID: "f_other"}}
		if f.noMoved {
			moved = nil
		}
		return api.BoardArchiveResult{Archived: 2, Case: c, Moved: moved}, err
	case api.MethodMessageMove:
		var p api.MessageMoveParams
		_ = roundTrip(params, &p)
		f.moves = append(f.moves, p)
		return api.MessageMoveResult{}, f.moveFailure
	case api.MethodBoardDiscardDraft:
		var p api.BoardDiscardDraftParams
		_ = roundTrip(params, &p)
		c, err := f.write(p.CaseID, func(c *api.BoardCase) { c.Draft = nil })
		return api.BoardDiscardDraftResult{Case: c}, err
	case api.MethodBoardUnflag:
		var p api.BoardUnflagParams
		_ = roundTrip(params, &p)
		// The case as stored: the rules judge it again later (the board.list
		// after the write shows what they made of it).
		c, err := f.write(p.CaseID, func(*api.BoardCase) {})
		if err == nil {
			i := slices.IndexFunc(f.cases, func(c api.BoardCase) bool { return c.ID == p.CaseID })
			f.cases[i].RuleState, f.cases[i].RuleReason = api.BoardYou, api.BoardReasonYouAddressed
			f.cases[i].Version++
		}
		return api.BoardUnflagResult{Case: c, Unflagged: 2}, err
	case api.MethodBoardSetCommitment:
		var p api.BoardSetCommitmentParams
		_ = roundTrip(params, &p)
		if err := f.wait(heldWrites); err != nil {
			return nil, err
		}
		if f.writeFailure != nil {
			return nil, f.writeFailure
		}
		i := slices.IndexFunc(f.commitments, func(k api.BoardCommitment) bool { return k.ID == p.CommitmentID })
		if i < 0 {
			return nil, &api.Error{Code: api.CodeCaseNotFound, Message: "no commitment"}
		}
		f.commitments[i].State = api.CommitmentOpen
		if p.Done {
			f.commitments[i].State = api.CommitmentDone
		}
		return api.BoardSetCommitmentResult{Commitment: f.commitments[i]}, nil
	case api.MethodDraftDelete:
		var p api.DraftDeleteParams
		_ = roundTrip(params, &p)
		f.deleted = append(f.deleted, p)
		return api.DraftDeleteResult{}, nil
	}
	return nil, &api.Error{Code: api.CodeMethodNotFound, Message: method}
}

// write changes case id (version + 1) and answers it.
func (f *fakeDaemon) write(id CaseID, change func(*api.BoardCase)) (api.BoardCase, error) {
	if err := f.wait(heldWrites); err != nil {
		return api.BoardCase{}, err
	}
	if f.writeFailure != nil {
		return api.BoardCase{}, f.writeFailure
	}
	i := slices.IndexFunc(f.cases, func(c api.BoardCase) bool { return c.ID == id })
	if i < 0 {
		return api.BoardCase{}, &api.Error{Code: api.CodeCaseNotFound, Message: "no case"}
	}
	change(&f.cases[i])
	f.cases[i].Version++
	return f.cases[i], nil
}

// harness is a DaemonSource over a fakeDaemon on a manualLoop.
type harness struct {
	t       *testing.T
	fake    *fakeDaemon
	loop    *manualLoop
	source  *DaemonSource
	reports int
	errors  []string
	notices []string
}

func newHarness(t *testing.T, connected bool) *harness {
	h := &harness{t: t, fake: newFakeDaemon(connected), loop: newManualLoop()}
	h.source = NewDaemonSource(h.fake, h.loop, tr, DaemonOptions{})
	h.source.SetHandlers(Handlers{
		Change: func() { h.reports++ },
		Error:  func(s string) { h.errors = append(h.errors, s) },
		Notice: func(s string) { h.notices = append(h.notices, s) },
	})
	t.Cleanup(func() {
		h.source.Stop()
		h.fake.release()
	})
	return h
}

// started starts the source and waits for the board and the accounts.
func (h *harness) started() *harness {
	h.source.Start()
	h.idle()
	return h
}

// idle waits until nothing is waiting or on its way in the source.
func (h *harness) idle() {
	h.t.Helper()
	h.loop.runUntil(h.t, h.source.idle)
}

func (h *harness) until(cond func() bool) {
	h.t.Helper()
	h.loop.runUntil(h.t, cond)
}

func (h *harness) c(n string) Case {
	c, ok := h.source.Snapshot().Case(caseID(n))
	if !ok {
		h.t.Fatalf("no case %s", n)
	}
	return c
}

func (h *harness) has(n string) bool {
	_, ok := h.source.Snapshot().Case(caseID(n))
	return ok
}

func pendingIs(l *manualLoop, want ...time.Duration) func() bool {
	return func() bool { return slices.Equal(l.pending(), want) }
}

func TestDaemonLoadsTheBoardAndTheAccounts(t *testing.T) {
	h := newHarness(t, true).started()
	s := h.source.Snapshot()
	eq(t, "phase", s.Phase, PhaseReady)
	eq(t, "accounts", s.Accounts, []AccountInfo{{ID: "acc_1", Name: "Work", Badge: "IMAP", CanReply: true}}) // enabled only
	var ids []CaseID
	for _, c := range s.Cases {
		ids = append(ids, c.ID)
	}
	eq(t, "cases", ids, []CaseID{caseID("1"), caseID("2")})
	c := h.c("1")
	check(t, c.Person == "Person 1" && c.Subject == "Subject 1" && c.RuleState == StateYou, "case %+v", c)
	check(t, c.RuleReason == api.BoardReasonYouAddressed && c.Thread == "t_1" && c.CanArchive && c.Version == 1, "case %+v", c)
	check(t, c.Reply != nil && *c.Reply == ReplyTarget{Message: "m_1", Folder: "f_inbox"} && c.LatestMessage == "m_1", "case %+v", c)
	check(t, !c.MessagesLoaded && c.Visibility.IsLive(), "case %+v", c)
	check(t, s.Triage == TriageInfo{Queue: 4, AnnotatedToday: 2} && !s.Annotated, "triage %+v", s.Triage)
}

// TestDaemonSnapshotsReachTheTriage: every published snapshot also goes to
// OnSnapshot (the application's triage); the last run carries its trigger
// and start.
func TestDaemonSnapshotsReachTheTriage(t *testing.T) {
	h := newHarness(t, true)
	started := t0.Add(-10 * time.Minute)
	end := t0
	h.fake.set(func(f *fakeDaemon) {
		f.lastRun = &api.BoardRun{At: started, EndedAt: &end, Trigger: api.TriggerAuto, Source: "claude-code", Annotated: 3, Error: api.RunTimeout}
	})
	var seen []Snapshot
	h.source.OnSnapshot = func(s Snapshot) { seen = append(seen, s) }
	h.started()
	check(t, len(seen) > 0 && seen[len(seen)-1].Equal(h.source.Snapshot()), "the last snapshot did not reach the triage")
	eq(t, "count", len(seen), h.reports)
	run := h.source.Snapshot().Run
	check(t, run != nil && run.Trigger == "auto" && run.Started.Equal(started) && run.Date.Equal(t0), "run %+v", run)
	check(t, run.Annotated == 3 && run.Error == "timeout" && !run.Running && run.Model == "claude-code", "run %+v", run)
}

func TestDaemonPhases(t *testing.T) {
	h := newHarness(t, true)
	h.fake.set(func(f *fakeDaemon) { f.enabled, f.ready = true, false })
	h.started()
	eq(t, "preparing", h.source.Phase(), PhasePreparing)
	h.fake.set(func(f *fakeDaemon) { f.enabled, f.ready = false, true })
	h.source.Refresh()
	h.idle()
	check(t, h.source.Phase() == PhaseOff && len(h.source.Snapshot().Cases) == 0, "off")
	// The board comes back: ready.
	h.fake.set(func(f *fakeDaemon) { f.enabled, f.ready = true, true })
	h.source.Refresh()
	h.idle()
	check(t, h.source.Phase() == PhaseReady && len(h.source.Snapshot().Cases) == 2, "ready")
}

// TestDaemonAFailedListIsAskedAgainLater: a list the daemon could not
// answer is PhaseFailed, keeps the cases and is asked again after a
// back-off that grows; an answer ends it.
func TestDaemonAFailedListIsAskedAgainLater(t *testing.T) {
	h := newHarness(t, true).started()
	h.fake.set(func(f *fakeDaemon) { f.listFailure = &api.Error{Code: api.CodeStorageError, Message: "disk"} })
	h.source.Refresh()
	h.until(pendingIs(h.loop, 2*time.Second))
	h.until(func() bool { return h.source.Phase() == PhaseFailed })
	check(t, len(h.source.Snapshot().Cases) == 2 && len(h.errors) == 0, "failed %+v", h.errors)
	eq(t, "lists", h.fake.count(api.MethodBoardList), 2)
	// Still failing: the next wait is longer.
	h.loop.fire()
	h.until(pendingIs(h.loop, 4*time.Second))
	eq(t, "lists 3", h.fake.count(api.MethodBoardList), 3)
	h.loop.fire()
	h.until(pendingIs(h.loop, 8*time.Second))
	// Asked for now (the board shown again): the wait is over at once.
	h.fake.set(func(f *fakeDaemon) { f.listFailure = nil })
	h.source.Refresh()
	h.idle()
	eq(t, "ready", h.source.Phase(), PhaseReady)
	eq(t, "lists 5", h.fake.count(api.MethodBoardList), 5)
	// The next failure starts the back-off over.
	h.fake.set(func(f *fakeDaemon) { f.listFailure = &api.Error{Code: api.CodeInternalError, Message: "x"} })
	h.source.Refresh()
	h.until(func() bool { p := h.loop.pending(); return len(p) > 0 && p[len(p)-1] == 2*time.Second })
	h.until(func() bool { return h.source.Phase() == PhaseFailed })
}

func TestRetryDelays(t *testing.T) {
	var got []time.Duration
	for i := 1; i <= 8; i++ {
		got = append(got, RetryDelay(i))
	}
	eq(t, "delays", got, []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second,
		32 * time.Second, time.Minute, time.Minute, time.Minute})
	eq(t, "zero", RetryDelay(0), 2*time.Second)
}

// TestDaemonABackendWithoutTheBoard: a backend without the board says so
// and is not asked again by itself; one that is not running is
// unavailable.
func TestDaemonABackendWithoutTheBoard(t *testing.T) {
	h := newHarness(t, true).started()
	h.fake.set(func(f *fakeDaemon) { f.listFailure = &api.Error{Code: api.CodeMethodNotFound, Message: "board.list"} })
	h.source.Refresh()
	h.idle()
	eq(t, "unsupported", h.source.Phase(), PhaseUnsupported)
	eq(t, "no retry", h.loop.pending(), []time.Duration{})
	h.fake.disconnect()
	h.source.Refresh()
	h.idle()
	eq(t, "unavailable", h.source.Phase(), PhaseUnavailable)
	eq(t, "no retry either", h.loop.pending(), []time.Duration{})
}

// TestDaemonUnavailableAndReconnect: without a daemon the board is
// unavailable; the connection coming loads it.
func TestDaemonUnavailableAndReconnect(t *testing.T) {
	h := newHarness(t, false).started()
	check(t, h.source.Phase() == PhaseUnavailable && len(h.source.Snapshot().Cases) == 0, "phase %d", h.source.Phase())
	h.fake.connect()
	h.source.ConnectionChanged(true)
	h.idle()
	check(t, h.source.Phase() == PhaseReady && len(h.source.Snapshot().Accounts) == 1, "phase %d", h.source.Phase())
	eq(t, "cases", len(h.source.Snapshot().Cases), 2)
	// The connection goes: the last snapshot stays.
	h.source.ConnectionChanged(false)
	check(t, h.source.Phase() == PhaseUnavailable && len(h.source.Snapshot().Cases) == 2, "gone")
}

// TestDaemonNothingLoadsOutsideStartAndStop: nothing loads before Start or
// after Stop; nothing is even on its way.
func TestDaemonNothingLoadsOutsideStartAndStop(t *testing.T) {
	h := newHarness(t, true)
	poke := func() {
		h.source.BoardChanged(api.BoardChangedNotification{})
		h.source.AccountsChanged()
		h.source.ConnectionChanged(true)
		h.source.Refresh()
	}
	poke()
	check(t, h.source.idle() && h.source.Phase() == PhaseLoading, "before start")
	eq(t, "no timer", h.loop.pending(), []time.Duration{})
	h.started()
	lists, accounts := h.fake.count(api.MethodBoardList), h.fake.count(api.MethodAccountList)
	check(t, lists == 1 && accounts == 1, "lists %d accounts %d", lists, accounts)
	h.source.Stop()
	poke()
	check(t, h.source.idle(), "after stop")
	eq(t, "lists", h.fake.count(api.MethodBoardList), lists)
	eq(t, "accounts", h.fake.count(api.MethodAccountList), accounts)
}

// TestDaemonNotificationsAreDebounced: notifications in a burst make one
// board.list after the debounce.
func TestDaemonNotificationsAreDebounced(t *testing.T) {
	h := newHarness(t, true).started()
	h.fake.set(func(f *fakeDaemon) {
		f.cases = []api.BoardCase{wireCase("1", api.BoardYou, 1), wireCase("2", api.BoardHot, 1), wireCase("3", api.BoardInfo, 1)}
	})
	for range 5 {
		h.source.BoardChanged(api.BoardChangedNotification{AccountIDs: []api.AccountID{"acc_1"}})
	}
	h.until(pendingIs(h.loop, DefaultDebounce))
	eq(t, "not before the debounce", h.fake.count(api.MethodBoardList), 1)
	h.loop.fire()
	h.idle()
	eq(t, "cases", len(h.source.Snapshot().Cases), 3)
	eq(t, "lists", h.fake.count(api.MethodBoardList), 2)
}

// TestDaemonOneListAtATime: a board.list asked for while one is on its way
// waits for it and runs once after it, however often it was asked for: a
// slow list under a stream of notifications still lands.
func TestDaemonOneListAtATime(t *testing.T) {
	h := newHarness(t, true).started()
	h.fake.hold(heldLists, true)
	h.source.Refresh()
	h.until(func() bool { return h.fake.held(heldLists) == 1 })
	h.fake.set(func(f *fakeDaemon) {
		f.cases = []api.BoardCase{wireCase("1", api.BoardYou, 1), wireCase("2", api.BoardHot, 1), wireCase("3", api.BoardInfo, 1)}
	})
	for range 3 {
		h.source.BoardChanged(api.BoardChangedNotification{})
		h.until(func() bool { return len(h.loop.pending()) == 1 })
		h.loop.fire()
		h.until(func() bool { return len(h.loop.pending()) == 0 })
	}
	h.source.Refresh()
	eq(t, "none started meanwhile", h.fake.count(api.MethodBoardList), 2)
	// The held reply lands (the old cases), then one more list.
	h.fake.hold(heldLists, false)
	h.idle()
	eq(t, "cases", len(h.source.Snapshot().Cases), 3)
	eq(t, "lists", h.fake.count(api.MethodBoardList), 3)
}

// TestDaemonALostConnectionDropsTheReplyOnItsWay: the reply of a list
// asked for before the connection went is dropped.
func TestDaemonALostConnectionDropsTheReplyOnItsWay(t *testing.T) {
	h := newHarness(t, true).started()
	h.fake.hold(heldLists, true)
	h.fake.set(func(f *fakeDaemon) { f.cases = []api.BoardCase{wireCase("1", api.BoardYou, 1)} })
	h.source.Refresh()
	h.until(func() bool { return h.fake.held(heldLists) == 1 && h.source.accountsInFlight == 0 })
	h.source.ConnectionChanged(false)
	eq(t, "unavailable", h.source.Phase(), PhaseUnavailable)
	ran := h.loop.ran
	h.fake.hold(heldLists, false)
	h.until(func() bool { return h.loop.ran > ran }) // the reply came back, and was dropped
	h.idle()
	check(t, h.source.Phase() == PhaseUnavailable && len(h.source.Snapshot().Cases) == 2, "the reply landed")
}

// TestDaemonAccountsChanged: notify.accountsChanged lists the accounts and
// the board again.
func TestDaemonAccountsChanged(t *testing.T) {
	h := newHarness(t, true).started()
	h.fake.set(func(f *fakeDaemon) { f.accountName = "Office" })
	h.source.AccountsChanged()
	h.idle()
	check(t, len(h.source.Snapshot().Accounts) == 1 && h.source.Snapshot().Accounts[0].Name == "Office", "accounts %+v", h.source.Snapshot().Accounts)
	eq(t, "account lists", h.fake.count(api.MethodAccountList), 2)
	eq(t, "board lists", h.fake.count(api.MethodBoardList), 2)
}

// TestDaemonOptimisticWrite: a write shows at once, in one report, and the
// daemon's case follows.
func TestDaemonOptimisticWrite(t *testing.T) {
	h := newHarness(t, true).started()
	h.fake.hold(heldWrites, true)
	before := h.reports
	h.source.SetState(caseID("1"), optState(StateThem))
	eq(t, "at once", h.reports, before+1)
	check(t, *h.c("1").UserState == StateThem && h.c("1").Version == 1, "case %+v", h.c("1"))
	h.fake.hold(heldWrites, false)
	h.idle()
	check(t, *h.c("1").UserState == StateThem && h.c("1").Version == 2 && len(h.errors) == 0, "case %+v", h.c("1"))
	// Done, then back.
	h.source.SetDone(caseID("1"), true)
	check(t, h.c("1").Done(), "not done at once")
	h.idle()
	check(t, h.c("1").Visibility.Equal(Visibility{Kind: VisibleDone, At: t0}) && h.c("1").Version == 3, "done %+v", h.c("1").Visibility)
	h.source.SetDone(caseID("1"), false)
	h.idle()
	check(t, h.c("1").Visibility.IsLive() && h.c("1").Version == 4, "live %+v", h.c("1").Visibility)
	// Remind.
	until := t0.Add(24 * time.Hour)
	h.source.Remind(caseID("2"), &until)
	snoozed := Visibility{Kind: VisibleSnoozed, At: until}
	check(t, h.c("2").Visibility.Equal(snoozed), "not snoozed at once")
	h.idle()
	check(t, h.c("2").Visibility.Equal(snoozed) && h.c("2").Version == 2, "snoozed %+v", h.c("2"))
}

// TestDaemonRefusedWriteIsReverted: a refused write is taken back and
// said; a list arriving while a write is under way keeps the write's
// change.
func TestDaemonRefusedWriteIsReverted(t *testing.T) {
	h := newHarness(t, true).started()
	h.fake.set(func(f *fakeDaemon) {
		f.writeFailure = &api.Error{Code: api.CodeCaseNotFound, Message: "internal words"}
	})
	h.source.SetDone(caseID("1"), true)
	check(t, h.c("1").Done(), "not done at once")
	h.idle()
	check(t, !h.c("1").Done(), "not taken back")
	eq(t, "errors", h.errors, []string{"Marking the case done failed: the case is no longer on the board."})
	h.fake.set(func(f *fakeDaemon) { f.writeFailure = nil })
	h.fake.hold(heldWrites, true)
	h.source.SetState(caseID("2"), optState(StateInfo))
	h.source.Refresh()
	h.until(func() bool {
		return h.fake.count(api.MethodBoardList) == 2 && len(h.source.Snapshot().Accounts) == 1 && h.source.listInFlight == 0
	})
	h.until(func() bool { return h.fake.held(heldWrites) == 1 })
	check(t, *h.c("2").UserState == StateInfo, "not laid over the list") // still laid over the list
	h.fake.hold(heldWrites, false)
	h.idle()
	check(t, *h.c("2").UserState == StateInfo && h.c("2").Version == 2, "case %+v", h.c("2"))
	// An unknown case is not written.
	reports := h.reports
	h.source.SetDone("nope", true)
	check(t, h.source.idle() && h.reports == reports, "an unknown case was written")
	eq(t, "setDone calls", h.fake.count(api.MethodBoardSetDone), 1)
}

// TestDaemonAListOlderThanTheWriteKeepsTheWritesCase: a list asked for
// before a write's answer, and answered after it, keeps the newer case.
func TestDaemonAListOlderThanTheWriteKeepsTheWritesCase(t *testing.T) {
	h := newHarness(t, true).started()
	h.fake.hold(heldLists, true)
	h.source.Refresh()
	h.until(func() bool { return h.fake.held(heldLists) == 1 }) // it has version 1
	h.source.SetDone(caseID("1"), true)
	h.until(func() bool { return h.c("1").Version == 2 })
	h.fake.hold(heldLists, false)
	h.idle()
	check(t, h.c("1").Version == 2 && h.c("1").Done(), "case %+v", h.c("1"))
}

// TestDaemonTheConnectionGoesDuringAWrite: the connection going during a
// write takes the change back and says why.
func TestDaemonTheConnectionGoesDuringAWrite(t *testing.T) {
	h := newHarness(t, true).started()
	h.fake.hold(heldWrites, true)
	h.source.SetDone(caseID("1"), true)
	h.until(func() bool { return h.fake.held(heldWrites) == 1 })
	h.fake.disconnect()
	h.until(func() bool { return len(h.errors) > 0 })
	check(t, !h.c("1").Done(), "not taken back")
	eq(t, "errors", h.errors, []string{"Marking the case done failed: the mail backend is not running."})
}

func TestDaemonArchiveSaysWhatItDid(t *testing.T) {
	h := newHarness(t, true).started()
	h.source.Archive(caseID("1"))
	check(t, h.c("1").Done(), "not done at once")
	h.idle()
	eq(t, "notices", h.notices, []string{"Archived 2 messages."})
	check(t, !h.c("1").CanArchive, "can still archive")
}

func TestDaemonArchiveCanBeUndone(t *testing.T) {
	h := newHarness(t, true).started()
	var outcome *ArchiveOutcome
	h.source.SetHandlers(Handlers{
		Change:   func() { h.reports++ },
		Error:    func(s string) { h.errors = append(h.errors, s) },
		Archived: func(o ArchiveOutcome) { outcome = &o },
	})
	h.source.Archive(caseID("1"))
	h.idle()
	if outcome == nil {
		t.Fatal("no outcome")
	}
	eq(t, "outcome", *outcome, ArchiveOutcome{
		Case: caseID("1"), Account: "acc_1", Text: "Archived 2 messages.", UndoLabel: "Undo",
		Moved: []api.BoardMoved{{MessageID: "m_a", FromFolderID: "f_inbox"}, {MessageID: "m_b", FromFolderID: "f_other"}},
	})
	h.source.UndoArchive(*outcome)
	h.idle()
	// The moves run side by side: in any order.
	moves := slices.Clone(h.fake.moves)
	slices.SortFunc(moves, func(a, b api.MessageMoveParams) int {
		return strings.Compare(string(a.TargetFolderID), string(b.TargetFolderID))
	})
	eq(t, "moves", moves, []api.MessageMoveParams{
		{AccountID: "acc_1", MessageIDs: []api.MessageID{"m_a"}, TargetFolderID: "f_inbox"},
		{AccountID: "acc_1", MessageIDs: []api.MessageID{"m_b"}, TargetFolderID: "f_other"},
	})
	check(t, !h.c("1").Done(), "still done after Undo")
	eq(t, "errors", len(h.errors), 0)
}

func TestDaemonUndoArchiveFailedMoveKeepsCaseDone(t *testing.T) {
	h := newHarness(t, true).started()
	var outcome *ArchiveOutcome
	h.source.SetHandlers(Handlers{
		Change:   func() { h.reports++ },
		Error:    func(s string) { h.errors = append(h.errors, s) },
		Archived: func(o ArchiveOutcome) { outcome = &o },
	})
	h.source.Archive(caseID("1"))
	h.idle()
	h.fake.set(func(f *fakeDaemon) { f.moveFailure = errors.New("messageNotFound") })
	h.source.UndoArchive(*outcome)
	h.idle()
	check(t, h.c("1").Done(), "the case came back although the mail is still archived")
	eq(t, "errors", h.errors, []string{"Could not undo the archive."})
	for _, c := range h.fake.calls {
		check(t, c != api.MethodBoardSetDone, "board.setDone called after a failed move")
	}
}

func TestDaemonArchiveWithoutMovedHasNoUndo(t *testing.T) {
	h := newHarness(t, true).started()
	var outcome *ArchiveOutcome
	h.source.SetHandlers(Handlers{
		Change:   func() { h.reports++ },
		Archived: func(o ArchiveOutcome) { outcome = &o },
	})
	h.fake.set(func(f *fakeDaemon) { f.noMoved = true })
	h.source.Archive(caseID("1"))
	h.idle()
	check(t, outcome != nil && outcome.UndoLabel == "" && outcome.Text != "", "outcome %+v", outcome)
}

func TestDaemonRemindedAt(t *testing.T) {
	back := t0.Add(-time.Hour)
	h := newHarness(t, true)
	h.fake.set(func(f *fakeDaemon) {
		f.cases[0].RemindedAt = &back
		f.cases[1].RemindedAt = &back
		f.cases[1].Visibility = api.BoardDone // only a live case is reminded
	})
	h.started()
	check(t, h.c("1").Reminded() && h.c("1").RemindedAt.Equal(back), "case 1 %+v", h.c("1"))
	check(t, !h.c("2").Reminded() && h.c("2").RemindedAt.IsZero(), "case 2 %+v", h.c("2"))
	h.source.SetState(caseID("1"), optState(StateThem)) // a user action ends it at once
	check(t, !h.c("1").Reminded(), "still reminded after a move")
}

func draftCase(draft api.DraftID, text string) api.BoardCase {
	c := wireCase("1", api.BoardYou, 1)
	c.Draft = &api.BoardDraft{DraftID: draft, Text: text, Updated: t0}
	return c
}

func TestDaemonDiscardDraft(t *testing.T) {
	h := newHarness(t, true)
	h.fake.set(func(f *fakeDaemon) { f.cases = []api.BoardCase{draftCase("d_1", "Hi")} })
	h.started()
	eq(t, "draft", *h.c("1").Draft, DraftLink{ID: "d_1", Text: "Hi"})
	h.source.DiscardDraft(caseID("1"))
	check(t, h.c("1").Draft == nil, "not at once")
	h.idle()
	check(t, h.c("1").Draft == nil && h.c("1").Version == 2, "case %+v", h.c("1"))
}

// discard runs DiscardStoredDraft and waits for its end.
func (h *harness) discard(draft api.DraftID) error {
	h.t.Helper()
	done := false
	var got error
	h.source.DiscardStoredDraft(caseID("1"), draft, "acc_1", func(err error) { done, got = true, err })
	h.until(func() bool { return done })
	return got
}

// TestDaemonDiscardTheEditorsDraft: the inline editor's Discard deletes the
// draft it edits: through the case while the case links it, alone when it
// no longer does, and a refusal is handed back (the editor says so), never
// a toast of the source.
func TestDaemonDiscardTheEditorsDraft(t *testing.T) {
	h := newHarness(t, true)
	h.fake.set(func(f *fakeDaemon) { f.cases = []api.BoardCase{draftCase("d_1", "Hi")} })
	h.started()
	check(t, h.discard("d_1") == nil, "refused")
	check(t, h.c("1").Draft == nil && h.c("1").Version == 2, "case %+v", h.c("1"))
	eq(t, "discardDraft", h.fake.count(api.MethodBoardDiscardDraft), 1)
	eq(t, "deleted", len(h.fake.deleted), 0)
}

func TestDaemonDiscardADraftTheCaseNoLongerLinks(t *testing.T) {
	h := newHarness(t, true)
	h.fake.set(func(f *fakeDaemon) { f.cases = []api.BoardCase{draftCase("d_2", "Newer")} })
	h.started()
	check(t, h.discard("d_1") == nil, "refused")
	eq(t, "deleted", h.fake.deleted, []api.DraftDeleteParams{{AccountID: "acc_1", DraftID: "d_1"}})
	eq(t, "discardDraft", h.fake.count(api.MethodBoardDiscardDraft), 0)
	eq(t, "the case's own link stays", h.c("1").Draft.ID, api.DraftID("d_2"))
}

func TestDaemonARefusedDiscardIsHandedBackAndTheLinkComesBack(t *testing.T) {
	h := newHarness(t, true)
	h.fake.set(func(f *fakeDaemon) { f.cases = []api.BoardCase{draftCase("d_1", "Hi")} })
	h.started()
	h.fake.set(func(f *fakeDaemon) { f.writeFailure = &api.Error{Code: api.CodeStorageError, Message: "disk"} })
	err := h.discard("d_1")
	var e *api.Error
	check(t, errors.As(err, &e) && e.Code == api.CodeStorageError, "error %v", err)
	eq(t, "link", h.c("1").Draft.ID, api.DraftID("d_1"))
	eq(t, "the editor says it", len(h.errors), 0)
	// Stopped: nothing is asked.
	h.source.Stop()
	check(t, errors.Is(h.discard("d_1"), context.Canceled), "a stopped source discards")
}

// TestDaemonUnflagListsTheBoardAgain: Unstar is board.unflag, nothing
// changed beforehand, then the board listed again so the rules' new state
// arrives without waiting for the notification.
func TestDaemonUnflagListsTheBoardAgain(t *testing.T) {
	h := newHarness(t, true)
	hot := wireCase("2", api.BoardHot, 1)
	hot.RuleReason = api.BoardReasonHotFlagged
	h.fake.set(func(f *fakeDaemon) { f.cases = []api.BoardCase{wireCase("1", api.BoardYou, 1), hot} })
	h.started()
	lists := h.fake.count(api.MethodBoardList)
	eq(t, "reason", h.c("2").RuleReason, api.BoardReasonHotFlagged)
	h.source.Unflag(caseID("2"))
	check(t, h.c("2").RuleReason == api.BoardReasonHotFlagged && h.c("2").RuleState == StateHot, "nothing optimistic")
	h.until(func() bool { return h.fake.count(api.MethodBoardList) == lists+1 })
	h.idle()
	eq(t, "unflag", h.fake.count(api.MethodBoardUnflag), 1)
	check(t, h.c("2").RuleReason == api.BoardReasonYouAddressed && h.c("2").RuleState == StateYou, "case %+v", h.c("2"))
	eq(t, "no error", len(h.errors), 0)
	// Refused: said, and nothing listed again.
	h.fake.set(func(f *fakeDaemon) { f.writeFailure = &api.Error{Code: api.CodeCaseNotFound, Message: "x"} })
	h.source.Unflag(caseID("1"))
	h.until(func() bool { return len(h.errors) > 0 })
	h.idle()
	eq(t, "errors", h.errors, []string{"Removing the star failed: the case is no longer on the board."})
	eq(t, "lists", h.fake.count(api.MethodBoardList), lists+1)
	// An unknown case: nothing asked.
	h.source.Unflag(caseID("9"))
	h.idle()
	eq(t, "unflag calls", h.fake.count(api.MethodBoardUnflag), 2)
}

func commitmentStates(s Snapshot) []CommitmentState {
	var out []CommitmentState
	for _, k := range s.Commitments {
		out = append(out, k.State)
	}
	return out
}

func TestDaemonCommitments(t *testing.T) {
	h := newHarness(t, true)
	h.fake.set(func(f *fakeDaemon) { f.commitments = []api.BoardCommitment{wireCommitment(api.CommitmentOpen)} })
	h.started()
	eq(t, "open", commitmentStates(h.source.Snapshot()), []CommitmentState{CommitmentOpen})
	h.source.SetCommitmentDone("k_1", true)
	eq(t, "done at once", commitmentStates(h.source.Snapshot()), []CommitmentState{CommitmentDone})
	h.idle()
	eq(t, "done", commitmentStates(h.source.Snapshot()), []CommitmentState{CommitmentDone})
	eq(t, "no error", len(h.errors), 0)
	// Refused: back to done.
	h.fake.set(func(f *fakeDaemon) { f.writeFailure = &api.Error{Code: api.CodeStorageError, Message: "x"} })
	h.source.SetCommitmentDone("k_1", false)
	eq(t, "open at once", commitmentStates(h.source.Snapshot()), []CommitmentState{CommitmentOpen})
	h.idle()
	eq(t, "back", commitmentStates(h.source.Snapshot()), []CommitmentState{CommitmentDone})
	eq(t, "errors", h.errors, []string{"Changing the promise failed: the mail backend could not save it."})
}

// TestDaemonAListOlderThanTheTickKeepsThePromiseTicked: a promise ticked
// off stays ticked while a list asked for before the answer arrives; a
// list asked for later has the last word.
func TestDaemonAListOlderThanTheTickKeepsThePromiseTicked(t *testing.T) {
	h := newHarness(t, true)
	h.fake.set(func(f *fakeDaemon) { f.commitments = []api.BoardCommitment{wireCommitment(api.CommitmentOpen)} })
	h.started()
	h.fake.hold(heldLists, true)
	h.source.Refresh()
	h.until(func() bool { return h.fake.held(heldLists) == 1 }) // it has the promise open
	h.source.SetCommitmentDone("k_1", true)
	h.until(func() bool { return h.fake.count(api.MethodBoardSetCommitment) == 1 })
	h.fake.hold(heldLists, false)
	h.idle()
	eq(t, "ticked", commitmentStates(h.source.Snapshot()), []CommitmentState{CommitmentDone})
	// The daemon reopens it (another client): the next list says so.
	h.fake.set(func(f *fakeDaemon) { f.commitments = []api.BoardCommitment{wireCommitment(api.CommitmentOpen)} })
	h.source.Refresh()
	h.idle()
	eq(t, "reopened", commitmentStates(h.source.Snapshot()), []CommitmentState{CommitmentOpen})
}

func messageTexts(c Case) []string {
	var out []string
	for _, m := range c.Messages {
		out = append(out, m.Text)
	}
	return out
}

// TestDaemonMessagesAreCachedByVersion: the conversation is asked for once
// per version.
func TestDaemonMessagesAreCachedByVersion(t *testing.T) {
	h := newHarness(t, true).started()
	h.source.LoadMessages(caseID("1"))
	h.idle()
	eq(t, "loaded", messageTexts(h.c("1")), []string{"version 1"})
	eq(t, "from", h.c("1").Messages[0].From, "Ann")
	h.source.LoadMessages(caseID("1"))
	check(t, h.source.idle(), "asked again")
	eq(t, "gets", h.fake.count(api.MethodBoardGet), 1)
	// A new version: the old messages stay shown until the new arrive.
	h.fake.set(func(f *fakeDaemon) {
		f.cases = []api.BoardCase{wireCase("1", api.BoardYou, 5), wireCase("2", api.BoardHot, 1)}
	})
	h.source.Refresh()
	h.idle()
	check(t, h.c("1").Version == 5 && slices.Equal(messageTexts(h.c("1")), []string{"version 1"}), "case %+v", h.c("1"))
	h.source.LoadMessages(caseID("1"))
	h.idle()
	eq(t, "version 5", messageTexts(h.c("1")), []string{"version 5"})
	eq(t, "gets 2", h.fake.count(api.MethodBoardGet), 2)
	// A failure with nothing loaded says so; asking again retries.
	h.fake.set(func(f *fakeDaemon) { f.getFailure = &api.Error{Code: api.CodeStorageError, Message: "x"} })
	h.source.LoadMessages(caseID("2"))
	check(t, !h.c("2").MessagesLoaded && !h.c("2").MessagesFailed, "loading") // loading
	h.idle()
	check(t, h.c("2").MessagesFailed && len(h.errors) == 0, "failed") // the detail says it; no toast
	h.fake.set(func(f *fakeDaemon) { f.getFailure = nil })
	h.source.LoadMessages(caseID("2"))
	check(t, !h.c("2").MessagesFailed, "still failed")
	h.idle()
	check(t, h.c("2").MessagesLoaded, "not loaded")
}

// TestDaemonConversationsAreBounded: the source keeps the conversations of
// the most recently opened cases only.
func TestDaemonConversationsAreBounded(t *testing.T) {
	h := newHarness(t, true)
	n := ConversationsKept + 5
	var cases []api.BoardCase
	for i := 1; i <= n; i++ {
		cases = append(cases, wireCase(fmt.Sprint(i), api.BoardYou, 1))
	}
	h.fake.set(func(f *fakeDaemon) { f.cases = cases })
	h.started()
	for i := 1; i <= n; i++ {
		h.source.LoadMessages(caseID(fmt.Sprint(i)))
		h.idle()
	}
	var kept, want []CaseID
	for _, c := range h.source.Snapshot().Cases {
		if c.MessagesLoaded {
			kept = append(kept, c.ID)
		}
	}
	for i := 6; i <= n; i++ {
		want = append(want, caseID(fmt.Sprint(i)))
	}
	eq(t, "kept", kept, want)
	// Opening a kept one again keeps it longest.
	h.source.LoadMessages(caseID("6"))
	h.source.LoadMessages(caseID("1"))
	h.idle()
	check(t, h.c("6").MessagesLoaded && !h.c("7").MessagesLoaded && h.c("1").MessagesLoaded, "LRU")
}

// TestDaemonAConversationOfACaseThatLeftIsDropped: a conversation whose
// case left the board while it loaded is not kept, and nothing waits for
// it.
func TestDaemonAConversationOfACaseThatLeftIsDropped(t *testing.T) {
	h := newHarness(t, true).started()
	h.fake.hold(heldGets, true)
	h.source.LoadMessages(caseID("2"))
	h.until(func() bool { return h.fake.held(heldGets) == 1 })
	h.fake.set(func(f *fakeDaemon) { f.cases = []api.BoardCase{wireCase("1", api.BoardYou, 1)} })
	h.source.Refresh()
	h.idle() // board.get is still held: the loading was pruned
	eq(t, "cases", len(h.source.Snapshot().Cases), 1)
	h.fake.set(func(f *fakeDaemon) {
		f.cases = []api.BoardCase{wireCase("1", api.BoardYou, 1), wireCase("2", api.BoardHot, 1)}
	})
	ran := h.loop.ran
	h.fake.hold(heldGets, false)
	h.until(func() bool { return h.loop.ran > ran }) // the reply came back while the case was gone
	h.source.Refresh()
	h.idle()
	check(t, !h.c("2").MessagesLoaded, "kept a conversation of a case that left")
	h.source.LoadMessages(caseID("2"))
	h.idle()
	check(t, h.c("2").MessagesLoaded, "not loaded")
	eq(t, "gets", h.fake.count(api.MethodBoardGet), 2)
}

// TestDaemonControllerDepartureAndRevert: the board controller over the
// daemon's source: the selection moves on with the optimistic report, and
// a revert brings the case back.
func TestDaemonControllerDepartureAndRevert(t *testing.T) {
	h := newHarness(t, true)
	h.fake.set(func(f *fakeDaemon) {
		f.cases = []api.BoardCase{wireCase("1", api.BoardYou, 1), wireCase("2", api.BoardYou, 1), wireCase("3", api.BoardYou, 1)}
	})
	h.started()
	c := NewController(h.source, controllerOptions(func() time.Time { return t0 }))
	var toasts []string
	c.OnToast = func(s string) { toasts = append(toasts, s) }
	eq(t, "selection", c.State().Selection, caseID("1"))
	h.idle()
	check(t, h.c("1").MessagesLoaded, "selecting did not load the conversation")
	h.fake.set(func(f *fakeDaemon) { f.writeFailure = &api.Error{Code: api.CodeInvalidArgument, Message: "no"} })
	h.fake.hold(heldWrites, true)
	c.MarkDone(caseID("1"))
	eq(t, "at once", c.State().Selection, caseID("2")) // at once, with the optimistic report
	h.fake.hold(heldWrites, false)
	h.until(func() bool { return len(toasts) > 0 })
	eq(t, "toasts", toasts, []string{"Marking the case done failed: the board did not accept it."})
	eq(t, "rows", ctrlRows(c), []string{"c_1", "c_2", "c_3"})
	eq(t, "kept", c.State().Selection, caseID("2"))
}

// TestDaemonControllerRetriesAFailedConversationAfterAReconnect: the
// controller over the daemon's source asks again for a conversation that
// failed once the connection is back.
func TestDaemonControllerRetriesAFailedConversationAfterAReconnect(t *testing.T) {
	h := newHarness(t, true).started()
	h.fake.set(func(f *fakeDaemon) { f.getFailure = &api.Error{Code: api.CodeStorageError, Message: "x"} })
	c := NewController(h.source, controllerOptions(func() time.Time { return t0 }))
	h.idle()
	check(t, c.View().Detail.MessagesRetry, "no retry offered")
	h.fake.set(func(f *fakeDaemon) { f.getFailure = nil })
	h.source.ConnectionChanged(false)
	h.source.ConnectionChanged(true)
	h.idle()
	// The hot case is the list's first row, selected.
	eq(t, "detail", c.View().Detail.ID, caseID("2"))
	check(t, !c.View().Detail.MessagesRetry && h.c("2").MessagesLoaded, "not retried")
	eq(t, "gets", h.fake.count(api.MethodBoardGet), 2)
}

// TestDaemonConvert: the wire's case in the board's terms, times in UTC.
func TestDaemonConvert(t *testing.T) {
	prague, err := time.LoadLocation("Europe/Prague")
	if err != nil {
		t.Fatal(err)
	}
	remind := t0.In(prague).Add(time.Hour)
	w := wireCase("1", api.BoardThem, 3)
	w.UserState = api.Ptr(api.BoardState("later")) // unknown: automatic
	w.RuleState = "someday"                        // unknown: for reading
	w.Visibility, w.RemindAt = api.BoardSnoozed, &remind
	w.Issue = &api.BoardIssue{Key: "K-1", Status: "Open", StatusCategory: api.StatusCategoryInProgress}
	w.Annotation = &api.BoardAnnotation{State: api.Ptr(api.BoardHot), Title: "T", Tasks: []string{"a"},
		Due: &api.BoardDue{At: remind, Quote: "by then", MessageID: "m_1"}, Source: "s", At: remind, Stale: true}
	w.Person = api.Address{Address: "  x@example.invalid "}
	c := convertCase(w)
	check(t, c.UserState == nil && c.RuleState == StateInfo, "states %+v", c)
	check(t, c.Visibility.Kind == VisibleSnoozed && c.Visibility.At.Location() == time.UTC && c.Visibility.At.Equal(remind), "visibility %+v", c.Visibility)
	check(t, c.Issue.Style == "status-in-progress" && c.Person == "x@example.invalid", "case %+v", c)
	a := c.Annotation
	check(t, *a.State == StateHot && a.Due.Equal(remind) && a.DueQuote == "by then" && a.DueMessage == "m_1" && a.Stale, "annotation %+v", a)
	// A snoozed case without a time is live; no reply message, no target.
	w.RemindAt, w.ReplyMessageID = nil, ""
	c = convertCase(w)
	check(t, c.Visibility.IsLive() && c.Reply == nil, "case %+v", c)
	// Commitments: a state this client does not know is not shown.
	k := wireCommitment("kept")
	eq(t, "unknown", convertCommitment(k).State, CommitmentClosed)
}

// TestDaemonAccountInfo: the accounts' names and capsules as the sidebar
// shows them, and whether a reply can be written.
func TestDaemonAccountInfo(t *testing.T) {
	mail := api.Account{ID: "a", Config: api.AccountConfig{Email: " me@example.invalid "}}
	eq(t, "name", accountLabel(mail), "me@example.invalid")
	eq(t, "badge", accountBadge(mail), "IMAP")
	graph := api.Account{ID: "g", Config: api.AccountConfig{Kind: api.AccountGraph, Name: "Work"}}
	eq(t, "graph", accountBadge(graph), "M365")
	jiraAccount := api.Account{ID: "j", Config: api.AccountConfig{Kind: api.AccountJira, Name: "Issues"}}
	eq(t, "jira", accountBadge(jiraAccount), "JIRA")
}
