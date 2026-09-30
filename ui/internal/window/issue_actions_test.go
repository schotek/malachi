// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/jira"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The Change Status menu's controller over a fake daemon (the port of
// macOS IssueActionsControllerTests): the items of issue.transitions (a
// transition that needs fields in Jira disabled with the hint),
// issue.transition with its toast and the refreshed issue for the cards,
// the failure toasts, the stale-reply discipline of the loads, one
// transition per issue at a time, and nothing at all for an account
// without the capability.

var testIssue = api.IssueInfo{
	Key: "ITSD-42", URL: "https://acme.atlassian.net/browse/ITSD-42", Summary: "The printer on the third floor",
	Status: "To Do", StatusCategory: api.StatusCategoryTodo, Assignee: "Jana Dvořáková",
}

func inProgressIssue() api.IssueInfo {
	i := testIssue
	i.Status = "In Progress"
	i.StatusCategory = api.StatusCategoryInProgress
	return i
}

// issueScript is the fake daemon's side: its answers, what it was asked.
type issueScript struct {
	mu              sync.Mutex
	listErr         error
	listDelay       time.Duration
	transitionErr   error
	transitionDelay time.Duration
	refreshed       api.IssueInfo
	lists           []api.IssueTransitionsParams
	transitions     []api.IssueTransitionParams
	calls           []string
}

func (s *issueScript) set(fn func(*issueScript)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s)
}

func (s *issueScript) Call(_ context.Context, method string, params, result any) error {
	s.mu.Lock()
	s.calls = append(s.calls, method)
	var delay time.Duration
	var err error
	var answer any
	switch method {
	case api.MethodIssueTransitions:
		s.lists = append(s.lists, params.(api.IssueTransitionsParams))
		delay, err = s.listDelay, s.listErr
		answer = api.IssueTransitionsResult{Issue: testIssue, Transitions: []api.IssueTransition{
			{ID: "11", Name: "Start Progress", To: "In Progress", ToCategory: api.StatusCategoryInProgress},
			{ID: "31", Name: "Resolve", To: "Resolved", ToCategory: api.StatusCategoryDone, NeedsInput: true},
			{ID: "21", Name: "Done", To: "Done", ToCategory: api.StatusCategoryDone},
		}}
	case api.MethodIssueTransition:
		s.transitions = append(s.transitions, params.(api.IssueTransitionParams))
		delay, err = s.transitionDelay, s.transitionErr
		answer = api.IssueTransitionResult{Issue: s.refreshed}
	default:
		s.mu.Unlock()
		return &api.Error{Code: api.CodeMethodNotFound}
	}
	s.mu.Unlock()
	time.Sleep(delay)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(answer)
	return json.Unmarshal(raw, result)
}

// issueHarness runs the controller with a main loop of its own: post
// queues onto a channel that the test drains on its goroutine, as
// glib.IdleAdd would on GTK's.
type issueHarness struct {
	t       *testing.T
	script  *issueScript
	c       *issueActions
	loop    chan func()
	toasts  []string
	busy    []busyEvent
	changed []api.IssueInfo
	loaded  []loadOutcome
}

type busyEvent struct {
	key  string
	busy bool
}

type loadOutcome struct {
	got loadedTransitions
	err error
}

func newIssueHarness(t *testing.T) *issueHarness {
	h := &issueHarness{t: t, script: &issueScript{refreshed: inProgressIssue()}, loop: make(chan func(), 64)}
	accounts := map[api.AccountID]api.Account{
		"j": {ID: "j", Enabled: true, Config: api.AccountConfig{Name: "Acme Jira", Kind: api.AccountJira},
			Capabilities: []api.AccountCapability{api.CapabilityComment, api.CapabilityForward, api.CapabilityTransition}},
		"p": {ID: "p", Enabled: true, Config: api.AccountConfig{Name: "Plain", Kind: api.AccountJira},
			Capabilities: []api.AccountCapability{api.CapabilityComment, api.CapabilityForward}},
		"m": {ID: "m", Enabled: true, Config: api.AccountConfig{Email: "me@example.invalid"}},
	}
	h.c = &issueActions{
		client: h.script,
		log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		post:   func(fn func()) { h.loop <- fn },
		account: func(id api.AccountID) (api.Account, bool) {
			a, ok := accounts[id]
			return a, ok
		},
		toast:   func(s string) { h.toasts = append(h.toasts, s) },
		onBusy:  func(_ api.AccountID, key string, busy bool) { h.busy = append(h.busy, busyEvent{key, busy}) },
		onIssue: func(_ api.AccountID, i api.IssueInfo) { h.changed = append(h.changed, i) },
	}
	return h
}

func (h *issueHarness) load(acc api.AccountID, msg api.MessageID) bool {
	return h.c.load(issueSubject{Account: acc, Message: msg}, func(l loadedTransitions, err error) {
		h.loaded = append(h.loaded, loadOutcome{l, err})
	})
}

// runUntil runs the main loop until cond holds, failing after 5 s.
func (h *issueHarness) runUntil(cond func() bool) {
	h.t.Helper()
	deadline := time.After(5 * time.Second)
	for !cond() {
		select {
		case fn := <-h.loop:
			fn()
		case <-deadline:
			h.t.Fatal("timed out")
		}
	}
}

// runFor runs the main loop for d.
func (h *issueHarness) runFor(d time.Duration) {
	end := time.After(d)
	for {
		select {
		case fn := <-h.loop:
			fn()
		case <-end:
			return
		}
	}
}

func (h *issueHarness) calls() []string {
	h.script.mu.Lock()
	defer h.script.mu.Unlock()
	return append([]string(nil), h.script.calls...)
}

// The transition items of the script's answer.
var (
	startItem   = jira.TransitionItem{ID: "11", Title: "Start Progress", Target: "In Progress", Subtitle: "In Progress", Enabled: true}
	resolveItem = jira.TransitionItem{ID: "31", Title: "Resolve", Target: "Resolved", Subtitle: "Resolved", Hint: "Needs fields in Jira"}
	doneItem    = jira.TransitionItem{ID: "21", Title: "Done", Target: "Done", Enabled: true}
)

func TestIssueActionsLoadListsTheTransitions(t *testing.T) {
	h := newIssueHarness(t)
	if !h.c.canTransition("j") || !h.load("j", "m1") {
		t.Fatal("the account changes statuses")
	}
	h.runUntil(func() bool { return len(h.loaded) == 1 })
	got := h.loaded[0]
	if got.err != nil || !reflect.DeepEqual(got.got.Issue, testIssue) {
		t.Fatalf("loaded %+v", got)
	}
	if want := []jira.TransitionItem{startItem, resolveItem, doneItem}; !reflect.DeepEqual(got.got.Items, want) {
		t.Errorf("items %+v, want %+v", got.got.Items, want)
	}
	if want := []api.IssueTransitionsParams{{AccountID: "j", MessageID: "m1"}}; !reflect.DeepEqual(h.script.lists, want) {
		t.Errorf("asked %+v", h.script.lists)
	}
	if !reflect.DeepEqual(h.calls(), []string{api.MethodIssueTransitions}) {
		t.Errorf("calls %v", h.calls())
	}
}

func TestIssueActionsNothingWithoutTheCapability(t *testing.T) {
	h := newIssueHarness(t)
	for _, acc := range []api.AccountID{"p", "m", "unknown"} {
		if h.c.canTransition(acc) {
			t.Errorf("%s changes statuses", acc)
		}
		if h.load(acc, "m1") {
			t.Errorf("%s loaded", acc)
		}
	}
	if h.c.perform(issueSubject{Account: "p", Message: "m1"}, startItem, testIssue) {
		t.Error("performed on an account without the capability")
	}
	h.runFor(50 * time.Millisecond)
	if len(h.loaded)+len(h.toasts)+len(h.busy) != 0 || len(h.calls()) != 0 {
		t.Errorf("the daemon was asked: %v", h.calls())
	}
}

func TestIssueActionsLoadFailureReachesTheMenu(t *testing.T) {
	h := newIssueHarness(t)
	h.script.set(func(s *issueScript) {
		s.listErr = &api.Error{Code: api.CodeNetworkError, Message: "dial tcp: refused"}
	})
	h.load("j", "m1")
	h.runUntil(func() bool { return len(h.loaded) == 1 })
	var e *api.Error
	if err := h.loaded[0].err; !errors.As(err, &e) || e.Code != api.CodeNetworkError {
		t.Fatalf("err %v", err)
	}
	if got := widget.RPCErrorText(jira.LoadTransitionsAction(i18n.Tr), h.loaded[0].err); got != "Loading the status changes failed: the server could not be reached" {
		t.Errorf("menu item %q", got)
	}
	if len(h.toasts) != 0 {
		t.Error("the menu shows the failure; no toast")
	}
}

func TestIssueActionsOnlyTheNewestLoadAnswers(t *testing.T) {
	h := newIssueHarness(t)
	h.script.set(func(s *issueScript) { s.listDelay = 150 * time.Millisecond })
	h.load("j", "m1")
	time.Sleep(20 * time.Millisecond)
	h.script.set(func(s *issueScript) { s.listDelay = 0 })
	h.load("j", "m2")
	h.runUntil(func() bool { return len(h.loaded) == 1 })
	h.runFor(250 * time.Millisecond)
	if len(h.loaded) != 1 {
		t.Errorf("the first load's reply was not dropped: %d answers", len(h.loaded))
	}
	if len(h.script.lists) != 2 || h.script.lists[0].MessageID != "m1" || h.script.lists[1].MessageID != "m2" {
		t.Errorf("asked %+v", h.script.lists)
	}

	// cancelLoad: the menu closed before the answer.
	h.script.set(func(s *issueScript) { s.listDelay = 100 * time.Millisecond })
	h.load("j", "m3")
	h.c.cancelLoad()
	h.runFor(250 * time.Millisecond)
	if len(h.loaded) != 1 {
		t.Error("a cancelled load answered")
	}
}

func TestIssueActionsPerformChangesTheStatus(t *testing.T) {
	h := newIssueHarness(t)
	subject := issueSubject{Account: "j", Message: "m1"}
	if !h.c.perform(subject, startItem, testIssue) {
		t.Fatal("not performed")
	}
	if !h.c.busy("j", "ITSD-42") || !reflect.DeepEqual(h.busy, []busyEvent{{"ITSD-42", true}}) {
		t.Errorf("busy %v", h.busy)
	}
	h.runUntil(func() bool { return len(h.toasts) == 1 })
	if h.toasts[0] != "Status changed to In Progress" {
		t.Errorf("toast %q", h.toasts[0])
	}
	if len(h.changed) != 1 || !reflect.DeepEqual(h.changed[0], inProgressIssue()) {
		t.Errorf("the cards got %+v", h.changed)
	}
	if !reflect.DeepEqual(h.busy, []busyEvent{{"ITSD-42", true}, {"ITSD-42", false}}) || h.c.busy("j", "ITSD-42") {
		t.Errorf("busy %v", h.busy)
	}
	if want := []api.IssueTransitionParams{{AccountID: "j", MessageID: "m1", TransitionID: "11"}}; !reflect.DeepEqual(h.script.transitions, want) {
		t.Errorf("asked %+v", h.script.transitions)
	}
}

func TestIssueActionsToastNamesTheTargetWhenTheRefreshWasLate(t *testing.T) {
	h := newIssueHarness(t)
	// The daemon's refresh timed out: the result still shows To Do.
	h.script.set(func(s *issueScript) { s.refreshed = testIssue })
	h.c.perform(issueSubject{Account: "j", Message: "m1"}, startItem, testIssue)
	h.runUntil(func() bool { return len(h.toasts) == 1 })
	if h.toasts[0] != "Status changed to In Progress" {
		t.Errorf("toast %q", h.toasts[0])
	}
	if len(h.changed) != 1 || !reflect.DeepEqual(h.changed[0], testIssue) {
		t.Error("the card shows what the daemon knows; the sync brings the rest")
	}
}

func TestIssueActionsFailuresToast(t *testing.T) {
	h := newIssueHarness(t)
	subject := issueSubject{Account: "j", Message: "m1"}
	h.script.set(func(s *issueScript) {
		s.transitionErr = &api.Error{Code: api.CodeServerError, Message: "Transition is not allowed by the workflow"}
	})
	h.c.perform(subject, startItem, testIssue)
	h.runUntil(func() bool { return len(h.toasts) == 1 })
	if h.toasts[0] != "The status could not be changed: Transition is not allowed by the workflow" {
		t.Errorf("toast %q", h.toasts[0])
	}
	if len(h.changed) != 0 || !reflect.DeepEqual(h.busy, []busyEvent{{"ITSD-42", true}, {"ITSD-42", false}}) {
		t.Errorf("changed %v, busy %v", h.changed, h.busy)
	}

	h.script.set(func(s *issueScript) { s.transitionErr = &api.Error{Code: api.CodeNetworkError, Message: "refused"} })
	h.c.perform(subject, doneItem, testIssue)
	h.runUntil(func() bool { return len(h.toasts) == 2 })
	if h.toasts[1] != "Changing the status failed: the server could not be reached" {
		t.Errorf("toast %q", h.toasts[1])
	}

	h.script.set(func(s *issueScript) {
		s.transitionErr = &api.Error{Code: api.CodeInvalidArgument, Message: "transition needs input"}
	})
	h.c.perform(subject, doneItem, testIssue)
	h.runUntil(func() bool { return len(h.toasts) == 3 })
	if h.toasts[2] != "Changing the status was rejected: transition needs input" {
		t.Errorf("toast %q", h.toasts[2])
	}
}

func TestIssueActionsDisabledItemIsNeverPerformed(t *testing.T) {
	h := newIssueHarness(t)
	subject := issueSubject{Account: "j", Message: "m1"}
	if h.c.perform(subject, resolveItem, testIssue) {
		t.Error("performed a transition that needs fields in Jira")
	}
	if h.c.perform(subject, jira.TransitionItem{Title: "x", Enabled: true}, testIssue) {
		t.Error("performed a transition without an id")
	}
	h.runFor(50 * time.Millisecond)
	if len(h.busy)+len(h.toasts) != 0 || len(h.calls()) != 0 {
		t.Errorf("calls %v", h.calls())
	}
}

func TestIssueActionsOneTransitionPerIssueAtATime(t *testing.T) {
	h := newIssueHarness(t)
	h.script.set(func(s *issueScript) { s.transitionDelay = 150 * time.Millisecond })
	subject := issueSubject{Account: "j", Message: "m1"}
	if !h.c.perform(subject, startItem, testIssue) {
		t.Fatal("not performed")
	}
	if h.c.perform(subject, doneItem, testIssue) {
		t.Error("the issue is busy")
	}
	// Another issue of the account is not.
	other := testIssue
	other.Key = "WEB-7"
	if !h.c.perform(issueSubject{Account: "j", Message: "w1"}, doneItem, other) {
		t.Error("another issue is not busy")
	}
	h.runUntil(func() bool { return len(h.toasts) == 2 })
	h.script.mu.Lock()
	ids := []string{}
	for _, p := range h.script.transitions {
		ids = append(ids, p.TransitionID)
	}
	h.script.mu.Unlock()
	if len(ids) != 2 || !(ids[0] == "11" && ids[1] == "21" || ids[0] == "21" && ids[1] == "11") {
		t.Errorf("transitions %v", ids)
	}
	if h.c.busy("j", "ITSD-42") || h.c.busy("j", "WEB-7") {
		t.Error("still busy")
	}
	// Free again: the next choice goes through.
	if !h.c.perform(subject, doneItem, testIssue) {
		t.Error("not free again")
	}
	h.runUntil(func() bool { return len(h.toasts) == 3 })
}
