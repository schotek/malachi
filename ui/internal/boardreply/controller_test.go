// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package boardreply

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/assistantpanel"
	"github.com/schotek/malachi/ui/internal/board"
)

// The board's Suggest Reply (Controller) against a fake daemon (board.get,
// board.setDraft, draft.delete) and the stand-in claude (macOS
// BoardReplyControllerTests). No real Claude Code, daemon or bridge is ever
// run: the bridge's path is only passed on.

const testBridge = "/b/malachi-mcp"

func boardCase(n string) board.Case {
	return board.Case{
		ID: board.CaseID("c_" + n), Account: "acc_1", Person: "P", Date: t0, Subject: "Subject " + n,
		RuleState: board.StateYou, Reply: &board.ReplyTarget{Message: api.MessageID("m_" + n), Folder: "f_inbox"},
	}
}

// replyDaemon is the daemon's side of the reply controller's tests.
type replyDaemon struct {
	mu          sync.Mutex
	getFailure  error
	setFailure  error
	caseDraft   *api.BoardDraft
	gets        []api.BoardCaseID
	sets        []api.BoardSetDraftParams
	deletes     []api.DraftDeleteParams
	holdDeletes bool
	heldDeletes []chan struct{}
}

func (d *replyDaemon) hold(on bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.holdDeletes = on
	if !on {
		held := d.heldDeletes
		d.heldDeletes = nil
		for _, c := range held {
			close(c)
		}
	}
}

func (d *replyDaemon) Call(ctx context.Context, method string, params, result any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	var out any
	switch method {
	case api.MethodBoardGet:
		var p api.BoardGetParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		d.mu.Lock()
		d.gets = append(d.gets, p.CaseID)
		failure, draft := d.getFailure, d.caseDraft
		d.mu.Unlock()
		if failure != nil {
			return failure
		}
		n := string(p.CaseID)[2:]
		var msgs []api.BoardMessage
		for i := 1; i <= 8; i++ {
			msgs = append(msgs, api.BoardMessage{
				ID: api.MessageID(fmt.Sprintf("m_%s_%d", n, i)), FolderID: "f_inbox",
				From: api.Address{Address: "x@y"}, Date: t0, Text: "t",
			})
		}
		out = api.BoardGetResult{Case: wireCase(n, draft), Messages: msgs}
	case api.MethodBoardSetDraft:
		var p api.BoardSetDraftParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		d.mu.Lock()
		d.sets = append(d.sets, p)
		failure := d.setFailure
		d.mu.Unlock()
		if failure != nil {
			return failure
		}
		n := string(p.CaseID)[2:]
		draft := &api.BoardDraft{DraftID: p.DraftID, Text: "x", Updated: t0}
		out = api.BoardSetDraftResult{Case: wireCase(n, draft)}
	case api.MethodDraftDelete:
		var p api.DraftDeleteParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		d.mu.Lock()
		d.deletes = append(d.deletes, p)
		hold := d.holdDeletes
		var wait chan struct{}
		if hold {
			wait = make(chan struct{})
			d.heldDeletes = append(d.heldDeletes, wait)
		}
		d.mu.Unlock()
		if wait != nil {
			select {
			case <-wait:
			case <-ctx.Done():
			}
		}
		out = api.DraftDeleteResult{}
	default:
		return &api.Error{Code: api.CodeMethodNotFound, Message: method}
	}
	if result == nil {
		return nil
	}
	b, err := json.Marshal(out)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, result)
}

func wireCase(n string, draft *api.BoardDraft) api.BoardCase {
	return api.BoardCase{
		ID: api.BoardCaseID("c_" + n), AccountID: "acc_1", RuleState: api.BoardYou,
		RuleReason: api.BoardReasonYouAddressed, Subject: "Subject " + n, Person: api.Address{Address: "p@example.invalid"},
		Date: t0, MessageCount: 3, ReplyMessageID: api.MessageID("m_" + n), ReplyFolderID: "f_inbox",
		LatestMessageID: api.MessageID("m_" + n), Draft: draft, Version: 1,
	}
}

func (d *replyDaemon) list(get func(d *replyDaemon) []api.DraftDeleteParams) []api.DraftDeleteParams {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(get(d))
}

type replyHarness struct {
	t             *testing.T
	loop          *testLoop
	daemon        *replyDaemon
	fake          *fakeClaude
	settings      *memSettings
	available     bool
	c             *Controller
	states        []board.SuggestReplyState
	refreshes     int
	consentAsked  int
	consentAnswer bool
}

func discardLog() *slog.Logger { return slog.New(slog.DiscardHandler) }

func newReplyHarness(t *testing.T, fake *fakeClaude, consent bool, bridge string) *replyHarness {
	t.Helper()
	h := &replyHarness{
		t: t, loop: newTestLoop(), daemon: &replyDaemon{}, fake: fake, available: true,
		consentAnswer: true,
	}
	h.settings = &memSettings{model: assistant.Haiku, consent: consent, claudePath: fake.path}
	locator := assistantpanel.NewLocator(h.settings, []string{"HOME=" + fake.dir}, h.loop, "", discardLog())
	request := assistantpanel.NewRequest(assistantpanel.RequestConfig{
		Settings: h.settings, Locator: locator, Loop: h.loop, Log: discardLog(), Directory: filepath.Join(fake.dir, "work"),
		Env: []string{"HOME=" + fake.dir}, KillGrace: 300 * time.Millisecond,
	})
	request.Timeout = 10 * time.Second
	h.c = NewController(Config{
		Caller: h.daemon, Settings: h.settings, Locator: locator, Request: request, Loop: h.loop,
		Log: discardLog(), Bridge: bridge, Socket: "/s.sock", Available: func() bool { return h.available },
	})
	h.c.Timeout = 10 * time.Second
	h.c.OnRefresh = func() { h.refreshes++ }
	h.c.Consent = func(done func(bool)) {
		h.consentAsked++
		done(h.consentAnswer)
	}
	h.c.Observe(func() {
		if len(h.states) == 0 || h.states[len(h.states)-1] != h.c.State() {
			h.states = append(h.states, h.c.State())
		}
	})
	t.Cleanup(func() {
		h.c.Cancel()
		h.c.Close()
		h.daemon.hold(false)
	})
	return h
}

func (h *replyHarness) ended() {
	h.t.Helper()
	h.loop.runUntil(h.t, h.c.Idle)
}

func TestReplySuccess(t *testing.T) {
	fake := newFakeClaude(t, "true", draftTurn("d_9", "acc_1", ""))
	h := newReplyHarness(t, fake, true, testBridge)
	if !h.c.Start(boardCase("1"), "  Say yes,\n thanks  ") {
		t.Fatal("Start refused")
	}
	if got := h.c.State(); got.Kind != board.SuggestRunning || got.Case != "c_1" {
		t.Fatalf("state = %+v", got)
	}
	h.loop.runUntil(t, func() bool { return h.c.State().Kind == board.SuggestRunning && fake.starts() == 1 })
	h.ended()
	if got := h.c.State(); got != (board.SuggestReplyState{}) {
		t.Errorf("state = %+v, want idle", got)
	}
	if got := h.daemon.list(func(d *replyDaemon) []api.DraftDeleteParams { return d.deletes }); len(got) != 0 {
		t.Errorf("deletes = %+v", got)
	}
	if len(h.daemon.sets) != 1 || h.daemon.sets[0] != (api.BoardSetDraftParams{CaseID: "c_1", DraftID: "d_9"}) {
		t.Errorf("sets = %+v", h.daemon.sets)
	}
	if h.refreshes != 1 {
		t.Errorf("refreshes = %d", h.refreshes)
	}
	if h.consentAsked != 0 {
		t.Errorf("consentAsked = %d, consent was already given", h.consentAsked)
	}
	joined := strings.Join(fake.args(), " ")
	for _, bad := range []string{"--allow-triage", "--allow-modify", "--allow-send"} {
		if strings.Contains(joined, bad) {
			t.Errorf("args contain %q: %s", bad, joined)
		}
	}
	if !strings.Contains(joined, "--reply-only") || !strings.Contains(joined, "m_1") {
		t.Errorf("args missing --reply-only m_1: %s", joined)
	}
	prompts := fake.prompts()
	if len(prompts) != 1 || !strings.Contains(prompts[0], "Say yes, thanks") {
		t.Errorf("prompts = %+v", prompts)
	}
	if strings.Contains(prompts[0], "m_1_3") || !strings.Contains(prompts[0], "m_1_4") {
		t.Errorf("prompt message ids wrong: %s", prompts[0])
	}
}

func TestReplyNoDraft(t *testing.T) {
	fake := newFakeClaude(t, "true", fakeTurn{lines: []string{fakeInit, fakeText("Here you go"), fakeResult("Here you go", true)}})
	h := newReplyHarness(t, fake, true, testBridge)
	h.c.Start(boardCase("1"), "")
	h.ended()
	if got := h.c.State(); got.Kind != board.SuggestFailed || got.Failure != board.ReplyNoDraft {
		t.Fatalf("state = %+v", got)
	}
	if len(h.daemon.sets) != 0 {
		t.Errorf("sets = %+v", h.daemon.sets)
	}
}

// A refused create_draft (an error result) is no draft.
func TestReplyRefusedCreateIsNoDraft(t *testing.T) {
	fake := newFakeClaude(t, "true", fakeTurn{lines: []string{
		fakeInit, fakeToolUse("c", "create_draft"), fakeToolResult("c", draftResultText("d_9", "acc_1"), true), fakeResult("no", true),
	}})
	h := newReplyHarness(t, fake, true, testBridge)
	h.c.Start(boardCase("1"), "")
	h.ended()
	if got := h.c.State(); got.Kind != board.SuggestFailed || got.Failure != board.ReplyNoDraft {
		t.Fatalf("state = %+v", got)
	}
}

func TestReplyLinkRefusedDeletesTheDraft(t *testing.T) {
	fake := newFakeClaude(t, "true", draftTurn("d_9", "acc_1", ""))
	h := newReplyHarness(t, fake, true, testBridge)
	h.daemon.setFailure = &api.Error{Code: api.CodeStorageError, Message: "disk"}
	h.c.Start(boardCase("1"), "")
	h.ended()
	if got := h.c.State(); got.Kind != board.SuggestFailed || got.Failure != board.ReplyBackend {
		t.Fatalf("state = %+v", got)
	}
	if len(h.daemon.deletes) != 1 || h.daemon.deletes[0] != (api.DraftDeleteParams{AccountID: "acc_1", DraftID: "d_9"}) {
		t.Errorf("deletes = %+v", h.daemon.deletes)
	}
	if _, ok := h.c.Created(); ok {
		t.Error("Created still set")
	}
}

// The case got a suggested reply meanwhile: ours goes, quietly.
func TestReplyLinkConflictEndsQuietly(t *testing.T) {
	fake := newFakeClaude(t, "true", draftTurn("d_9", "acc_1", ""))
	h := newReplyHarness(t, fake, true, testBridge)
	h.daemon.setFailure = &api.Error{Code: api.CodeConflict, Message: "linked"}
	h.c.Start(boardCase("1"), "")
	h.ended()
	if got := h.c.State(); got != (board.SuggestReplyState{}) {
		t.Fatalf("state = %+v, want idle", got)
	}
	if len(h.daemon.deletes) != 1 {
		t.Errorf("deletes = %+v", h.daemon.deletes)
	}
}

// Stop after the draft was created and before it was linked deletes it.
func TestReplyStopDeletesTheCreatedDraft(t *testing.T) {
	fake := newFakeClaude(t, "true", draftTurn("d_9", "acc_1", "sleep 30"))
	h := newReplyHarness(t, fake, true, testBridge)
	h.c.Start(boardCase("1"), "")
	h.loop.runUntil(t, func() bool { _, ok := h.c.Created(); return ok })
	h.c.Cancel()
	if got := h.c.State(); got.Kind != board.SuggestFailed || got.Failure != board.ReplyCancelled {
		t.Fatalf("state = %+v", got)
	}
	h.ended()
	if len(h.daemon.deletes) != 1 {
		t.Errorf("deletes = %+v", h.daemon.deletes)
	}
	if len(h.daemon.sets) != 0 {
		t.Errorf("sets = %+v", h.daemon.sets)
	}
}

func TestReplyTimeoutDeletesTheCreatedDraft(t *testing.T) {
	fake := newFakeClaude(t, "true", draftTurn("d_9", "acc_1", "sleep 30"))
	h := newReplyHarness(t, fake, true, testBridge)
	h.c.Timeout = 50 * time.Millisecond
	h.c.Start(boardCase("1"), "")
	h.loop.runUntil(t, func() bool { _, ok := h.c.Created(); return ok })
	h.loop.runUntil(t, func() bool { return !h.c.State().IsRunning() })
	h.ended()
	if got := h.c.State(); got.Kind != board.SuggestFailed || got.Failure != board.ReplyTimeout {
		t.Fatalf("state = %+v", got)
	}
	if len(h.daemon.deletes) != 1 {
		t.Errorf("deletes = %+v", h.daemon.deletes)
	}
}

// Quitting stops the request and waits for the delete of its draft, at
// most the bound.
func TestReplyQuitCleansUp(t *testing.T) {
	fake := newFakeClaude(t, "true", draftTurn("d_9", "acc_1", "sleep 30"))
	h := newReplyHarness(t, fake, true, testBridge)
	h.c.Start(boardCase("1"), "")
	h.loop.runUntil(t, func() bool { _, ok := h.c.Created(); return ok })
	h.c.CancelAndCleanUp(2 * time.Second)
	// The daemon has the call when it returns; the main-loop bookkeeping
	// (clearing pending, Idle) runs once the loop is pumped.
	if len(h.daemon.deletes) != 1 {
		t.Errorf("deletes = %+v", h.daemon.deletes)
	}
	h.ended()
}

// A daemon that does not answer the delete does not hold the quit beyond
// the bound.
func TestReplyQuitIsBounded(t *testing.T) {
	fake := newFakeClaude(t, "true", draftTurn("d_9", "acc_1", "sleep 30"))
	h := newReplyHarness(t, fake, true, testBridge)
	h.daemon.hold(true)
	h.c.Start(boardCase("1"), "")
	h.loop.runUntil(t, func() bool { _, ok := h.c.Created(); return ok })
	h.c.CancelAndCleanUp(50 * time.Millisecond)
	if h.c.Idle() {
		t.Error("the delete still waits")
	}
	h.daemon.hold(false)
	h.ended()
}

// One request at a time; another case shows that one runs elsewhere.
func TestReplyOneAtATime(t *testing.T) {
	fake := newFakeClaude(t, "true", fakeTurn{lines: []string{fakeInit}, shell: "sleep 30"})
	h := newReplyHarness(t, fake, true, testBridge)
	if !h.c.Start(boardCase("1"), "") {
		t.Fatal("first Start refused")
	}
	if h.c.Start(boardCase("2"), "") {
		t.Fatal("second Start should be refused")
	}
	here := h.c.View(boardCase("1"), board.Snapshot{}, false, board.PanelWords{}, tr)
	if !here.Running || here.Enabled {
		t.Errorf("view of running case = %+v", here)
	}
	there := h.c.View(boardCase("2"), board.Snapshot{}, false, board.PanelWords{}, tr)
	if there.Running || there.Enabled || there.Note != board.SuggestReplyElsewhere(tr) {
		t.Errorf("view of another case = %+v", there)
	}
	h.loop.runUntil(t, func() bool { return fake.starts() == 1 })
	if h.c.Start(boardCase("2"), "") {
		t.Fatal("still refused while running")
	}
	h.c.Cancel()
	h.ended()
	if len(h.daemon.gets) != 1 {
		t.Errorf("gets = %+v", h.daemon.gets)
	}
}

// The assistant's consent is asked first; declined, nothing runs.
func TestReplyConsentDeclined(t *testing.T) {
	fake := newFakeClaude(t, "true", draftTurn("d_9", "acc_1", ""))
	h := newReplyHarness(t, fake, false, testBridge)
	h.consentAnswer = false
	h.c.Start(boardCase("1"), "")
	h.ended()
	if h.consentAsked != 1 {
		t.Errorf("consentAsked = %d", h.consentAsked)
	}
	if got := h.c.State(); got != (board.SuggestReplyState{}) {
		t.Errorf("state = %+v, want idle", got)
	}
	if fake.starts() != 0 {
		t.Errorf("starts = %d", fake.starts())
	}
	if len(h.daemon.gets) != 0 {
		t.Errorf("gets = %+v", h.daemon.gets)
	}
	if h.settings.AssistantConsent() {
		t.Error("consent should not have been stored")
	}
}

// Allowed: the assistant's consent is kept.
func TestReplyConsentGiven(t *testing.T) {
	fake := newFakeClaude(t, "true", draftTurn("d_9", "acc_1", ""))
	h := newReplyHarness(t, fake, false, testBridge)
	h.c.Start(boardCase("1"), "")
	h.ended()
	if h.consentAsked != 1 {
		t.Errorf("consentAsked = %d", h.consentAsked)
	}
	if !h.settings.AssistantConsent() {
		t.Error("consent should have been stored")
	}
	if len(h.daemon.sets) != 1 {
		t.Errorf("sets = %+v", h.daemon.sets)
	}
}

func TestReplyBoardGetFails(t *testing.T) {
	fake := newFakeClaude(t, "true", draftTurn("d_9", "acc_1", ""))
	h := newReplyHarness(t, fake, true, testBridge)
	h.daemon.getFailure = &api.Error{Code: api.CodeStorageError, Message: "x"}
	h.c.Start(boardCase("1"), "")
	h.ended()
	if got := h.c.State(); got.Kind != board.SuggestFailed || got.Failure != board.ReplyBackend {
		t.Fatalf("state = %+v", got)
	}
	if fake.starts() != 0 {
		t.Errorf("starts = %d", fake.starts())
	}
}

// The case has a suggested reply by now: nothing is asked.
func TestReplyCaseHasDraftAlready(t *testing.T) {
	fake := newFakeClaude(t, "true", draftTurn("d_9", "acc_1", ""))
	h := newReplyHarness(t, fake, true, testBridge)
	h.daemon.caseDraft = &api.BoardDraft{DraftID: "d_1", Text: "x", Updated: t0}
	h.c.Start(boardCase("1"), "")
	h.ended()
	if got := h.c.State(); got != (board.SuggestReplyState{}) {
		t.Errorf("state = %+v, want idle", got)
	}
	if fake.starts() != 0 {
		t.Errorf("starts = %d", fake.starts())
	}
	if h.refreshes != 1 {
		t.Errorf("refreshes = %d", h.refreshes)
	}
}

func TestReplyNotSignedIn(t *testing.T) {
	fake := newFakeClaude(t, "false", draftTurn("d_9", "acc_1", ""))
	h := newReplyHarness(t, fake, true, testBridge)
	h.c.Start(boardCase("1"), "")
	h.ended()
	if got := h.c.State(); got.Kind != board.SuggestFailed || got.Failure != board.ReplyNotSignedIn {
		t.Fatalf("state = %+v", got)
	}
	if s := h.c.SignedIn(); !s.Known || s.SignedIn {
		t.Errorf("signedIn = %+v", s)
	}
}

func TestReplyToolsMissing(t *testing.T) {
	fake := newFakeClaude(t, "true", fakeTurn{lines: []string{fakeInitFailed, fakeResult("x", true)}})
	h := newReplyHarness(t, fake, true, testBridge)
	h.c.Start(boardCase("1"), "")
	h.ended()
	if got := h.c.State(); got.Kind != board.SuggestFailed || got.Failure != board.ReplyToolsMissing {
		t.Fatalf("state = %+v", got)
	}
}

// Nothing starts without the feature, the bridge or Claude Code; a request
// that loses the feature stops.
func TestReplyAvailability(t *testing.T) {
	fake := newFakeClaude(t, "true", fakeTurn{lines: []string{fakeInit}, shell: "sleep 30"})
	h := newReplyHarness(t, fake, true, testBridge)
	h.available = false
	if h.c.Start(boardCase("1"), "") {
		t.Fatal("Start without the feature should be refused")
	}
	h.available = true
	h.c.Start(boardCase("1"), "")
	h.loop.runUntil(t, func() bool { return fake.starts() == 1 })
	h.available = false
	h.c.AvailabilityChanged()
	if got := h.c.State(); got.Kind != board.SuggestFailed || got.Failure != board.ReplyCancelled {
		t.Fatalf("state = %+v", got)
	}
	h.ended()
}

// Samples never reach Start: Reply is always nil for them.
func TestReplyNeverOffersTheSamples(t *testing.T) {
	fake := newFakeClaude(t, "true", draftTurn("d_9", "acc_1", ""))
	h := newReplyHarness(t, fake, true, testBridge)
	sample := boardCase("1")
	sample.Reply = nil
	if h.c.Start(sample, "") {
		t.Fatal("a sample case should never start a request")
	}
	if fake.starts() != 0 {
		t.Errorf("starts = %d", fake.starts())
	}
}
