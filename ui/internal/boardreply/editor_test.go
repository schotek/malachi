// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package boardreply

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
)

// The board's inline reply editor state (Editor) against a fake daemon: a
// new key loads the draft with draft.get, the same key never reloads (an
// autosave bumps the case's version), a stale answer is dropped, a draft
// the pane ended stays hidden until the board drops the link, retry, and
// the failures (macOS BoardReplyEditorControllerTests). The fake holds
// draft.get answers until the test releases them, so no outcome depends on
// timing.

// draftDaemon is draft.get: what it was asked, the answers held until
// released.
type draftDaemon struct {
	mu      sync.Mutex
	failure error
	holding bool
	asked   []api.DraftGetParams
	waiting []chan struct{}
}

func (d *draftDaemon) setFailure(err error) {
	d.mu.Lock()
	d.failure = err
	d.mu.Unlock()
}

func (d *draftDaemon) hold(on bool) {
	d.mu.Lock()
	d.holding = on
	var w []chan struct{}
	if !on {
		w, d.waiting = d.waiting, nil
	}
	d.mu.Unlock()
	for _, c := range w {
		close(c)
	}
}

// releaseOne releases the oldest held answer only.
func (d *draftDaemon) releaseOne() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.waiting) == 0 {
		return
	}
	close(d.waiting[0])
	d.waiting = d.waiting[1:]
}

func (d *draftDaemon) held() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.waiting)
}

func (d *draftDaemon) askedCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.asked)
}

func (d *draftDaemon) Call(ctx context.Context, method string, params, result any) error {
	if method != api.MethodDraftGet {
		return &api.Error{Code: api.CodeMethodNotFound, Message: method}
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	var p api.DraftGetParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	d.mu.Lock()
	d.asked = append(d.asked, p)
	hold, failure := d.holding, d.failure
	var wait chan struct{}
	if hold {
		wait = make(chan struct{})
		d.waiting = append(d.waiting, wait)
	}
	d.mu.Unlock()
	if wait != nil {
		select {
		case <-wait:
		case <-ctx.Done():
		}
	}
	if failure != nil {
		return failure
	}
	draft := api.Draft{
		ID: p.DraftID, AccountID: p.AccountID, Version: 5, To: []api.Address{{Name: "Ann", Address: "ann@example.org"}},
		Subject: "Re: Offer", TextBody: "Text of " + string(p.DraftID), HTMLBody: "<p>Text of " + string(p.DraftID) + "</p>",
		InReplyTo: "m_2", Local: true,
	}
	out, err := json.Marshal(api.DraftGetResult{Draft: draft})
	if err != nil {
		return err
	}
	if result == nil {
		return nil
	}
	return json.Unmarshal(out, result)
}

func editorCase(n, draft string, version int64) board.Case {
	c := board.Case{
		ID: board.CaseID("c_" + n), Account: "acc_1", Thread: api.ThreadID("t_" + n), Person: "Ann", Date: t0,
		Subject: "Offer", RuleState: board.StateYou, Reply: &board.ReplyTarget{Message: "m_2", Folder: "f_inbox"},
		Version: version,
	}
	if draft != "" {
		c.Draft = &board.DraftLink{ID: api.DraftID(draft)}
	}
	return c
}

func editorKey(n, draft string) Key {
	return Key{CaseID: board.CaseID("c_" + n), Account: "acc_1", Draft: api.DraftID(draft)}
}

type editorHarness struct {
	t       *testing.T
	loop    *testLoop
	daemon  *draftDaemon
	editor  *Editor
	changes int
	token   *board.ObserverToken
}

func newEditorHarness(t *testing.T) *editorHarness {
	t.Helper()
	h := &editorHarness{t: t, loop: newTestLoop(), daemon: &draftDaemon{}}
	h.editor = NewEditor(h.daemon, h.loop, discardLog())
	h.token = h.editor.Observe(func() { h.changes++ })
	return h
}

func (h *editorHarness) waitReady(key Key) api.Draft {
	h.t.Helper()
	h.loop.runUntil(h.t, func() bool {
		return h.editor.Phase().Kind == EditorReady && h.editor.Phase().Key == key
	})
	return h.editor.Phase().Draft
}

func TestEditorCaseWithoutADraftShowsNothing(t *testing.T) {
	h := newEditorHarness(t)
	h.editor.Show(ptrCase(editorCase("1", "", 1)))
	h.editor.Show(nil)
	if h.editor.Phase().Kind != EditorNone || h.changes != 0 {
		t.Fatalf("phase = %+v, changes = %d", h.editor.Phase(), h.changes)
	}
	h.loop.settle(t, 50*time.Millisecond)
	if n := h.daemon.askedCount(); n != 0 {
		t.Errorf("asked = %d", n)
	}
}

func TestEditorNewKeyLoadsTheDraft(t *testing.T) {
	h := newEditorHarness(t)
	h.daemon.hold(true)
	key := editorKey("1", "d_1")
	h.editor.Show(ptrCase(editorCase("1", "d_1", 1)))
	if h.editor.Phase().Kind != EditorLoading || h.editor.Phase().Key != key {
		t.Fatalf("phase = %+v", h.editor.Phase())
	}
	h.loop.runUntil(t, func() bool { return h.daemon.held() == 1 })
	h.daemon.hold(false)
	draft := h.waitReady(key)
	if draft.Version != 5 || draft.AccountID != "acc_1" || draft.ID != "d_1" {
		t.Errorf("draft = %+v", draft)
	}
	if draft.HTMLBody != "<p>Text of d_1</p>" || draft.InReplyTo != "m_2" || draft.Subject != "Re: Offer" {
		t.Errorf("draft body = %+v", draft)
	}
	if n := h.daemon.askedCount(); n != 1 {
		t.Errorf("asked = %d", n)
	}
	if h.changes != 2 {
		t.Errorf("changes = %d, want loading+ready", h.changes)
	}
}

func TestEditorSameKeyNeverReloads(t *testing.T) {
	h := newEditorHarness(t)
	h.editor.Show(ptrCase(editorCase("1", "d_1", 1)))
	h.waitReady(editorKey("1", "d_1"))
	changes := h.changes
	// Autosaves bump the case's version; the board lists it again.
	for v := int64(2); v <= 6; v++ {
		h.editor.Show(ptrCase(editorCase("1", "d_1", v)))
	}
	h.loop.settle(t, 50*time.Millisecond)
	if n := h.daemon.askedCount(); n != 1 {
		t.Errorf("asked = %d", n)
	}
	if h.changes != changes {
		t.Errorf("changes = %d, want %d", h.changes, changes)
	}
	if h.editor.Phase().Kind != EditorReady {
		t.Errorf("phase = %+v", h.editor.Phase())
	}
	// Also while loading.
	h.daemon.hold(true)
	h.editor.Show(ptrCase(editorCase("2", "d_2", 1)))
	h.editor.Show(ptrCase(editorCase("2", "d_2", 9)))
	h.loop.runUntil(t, func() bool { return h.daemon.held() == 1 })
	if n := h.daemon.askedCount(); n != 2 {
		t.Errorf("asked = %d", n)
	}
}

func TestEditorStaleAnswerIsDropped(t *testing.T) {
	h := newEditorHarness(t)
	h.daemon.hold(true)
	h.editor.Show(ptrCase(editorCase("1", "d_1", 1)))
	h.loop.runUntil(t, func() bool { return h.daemon.held() == 1 })
	h.editor.Show(ptrCase(editorCase("2", "d_2", 1)))
	h.loop.runUntil(t, func() bool { return h.daemon.held() == 2 })
	// The first case's answer arrives after the second was selected.
	h.daemon.releaseOne()
	h.loop.settle(t, 100*time.Millisecond)
	if got := h.editor.Phase(); got.Kind != EditorLoading || got.Key != editorKey("2", "d_2") {
		t.Fatalf("phase = %+v", got)
	}
	h.daemon.releaseOne()
	h.waitReady(editorKey("2", "d_2"))
	// Deselected while loading: the answer is dropped too.
	h.editor.Show(ptrCase(editorCase("3", "d_3", 1)))
	h.loop.runUntil(t, func() bool { return h.daemon.held() == 1 })
	h.editor.Show(nil)
	h.daemon.hold(false)
	h.loop.settle(t, 100*time.Millisecond)
	if h.editor.Phase().Kind != EditorNone {
		t.Errorf("phase = %+v", h.editor.Phase())
	}
}

func TestEditorAnotherDraftOfTheSameCaseLoads(t *testing.T) {
	h := newEditorHarness(t)
	h.editor.Show(ptrCase(editorCase("1", "d_1", 1)))
	h.waitReady(editorKey("1", "d_1"))
	h.editor.Show(ptrCase(editorCase("1", "d_9", 1)))
	h.waitReady(editorKey("1", "d_9"))
	if n := h.daemon.askedCount(); n != 2 {
		t.Errorf("asked = %d", n)
	}
	// The link dropped: nothing.
	h.editor.Show(ptrCase(editorCase("1", "", 1)))
	if h.editor.Phase().Kind != EditorNone {
		t.Errorf("phase = %+v", h.editor.Phase())
	}
}

func TestEditorEndedHidesUntilTheLinkGoes(t *testing.T) {
	h := newEditorHarness(t)
	h.editor.Show(ptrCase(editorCase("1", "d_1", 1)))
	h.waitReady(editorKey("1", "d_1"))
	h.editor.Ended(editorKey("1", "d_1"))
	if h.editor.Phase().Kind != EditorNone {
		t.Fatalf("phase = %+v", h.editor.Phase())
	}
	// The board still links it for a moment (the refresh is on its way).
	h.editor.Show(ptrCase(editorCase("1", "d_1", 2)))
	h.editor.Show(ptrCase(editorCase("2", "", 1)))
	h.editor.Show(ptrCase(editorCase("1", "d_1", 3)))
	if h.editor.Phase().Kind != EditorNone {
		t.Fatalf("phase = %+v", h.editor.Phase())
	}
	h.loop.settle(t, 50*time.Millisecond)
	if n := h.daemon.askedCount(); n != 1 {
		t.Errorf("asked = %d", n)
	}
	// The link went; a later suggested reply, even under the same id, is
	// edited again.
	h.editor.Show(ptrCase(editorCase("1", "", 1)))
	h.editor.Show(ptrCase(editorCase("1", "d_1", 4)))
	if h.editor.Phase().Kind != EditorLoading {
		t.Fatalf("phase = %+v", h.editor.Phase())
	}
	h.loop.runUntil(t, func() bool { return h.daemon.askedCount() == 2 })
	h.waitReady(editorKey("1", "d_1"))
	// Ending another key changes nothing shown.
	h.editor.Ended(editorKey("7", "d_7"))
	if h.editor.Phase().Kind != EditorReady {
		t.Errorf("phase = %+v", h.editor.Phase())
	}
}

func TestEditorFailuresAndRetry(t *testing.T) {
	h := newEditorHarness(t)
	h.daemon.setFailure(&api.Error{Code: api.CodeStorageError, Message: "disk"})
	h.editor.Show(ptrCase(editorCase("1", "d_1", 1)))
	h.loop.runUntil(t, func() bool {
		p := h.editor.Phase()
		return p.Kind == EditorFailed && p.Key == editorKey("1", "d_1") && p.Failure == EditorBackend
	})
	// The same key while failed: no new request (Try Again asks).
	h.editor.Show(ptrCase(editorCase("1", "d_1", 2)))
	h.loop.settle(t, 50*time.Millisecond)
	if n := h.daemon.askedCount(); n != 1 {
		t.Errorf("asked = %d", n)
	}
	h.daemon.setFailure(nil)
	h.editor.Retry()
	if h.editor.Phase().Kind != EditorLoading {
		t.Fatalf("phase = %+v", h.editor.Phase())
	}
	h.waitReady(editorKey("1", "d_1"))
	if n := h.daemon.askedCount(); n != 2 {
		t.Errorf("asked = %d", n)
	}
	// Retry outside a failure does nothing.
	h.editor.Retry()
	h.loop.settle(t, 50*time.Millisecond)
	if n := h.daemon.askedCount(); n != 2 {
		t.Errorf("asked = %d", n)
	}

	h.daemon.setFailure(&api.Error{Code: api.CodeDraftNotFound, Message: "gone"})
	h.editor.Show(ptrCase(editorCase("2", "d_2", 1)))
	h.loop.runUntil(t, func() bool {
		p := h.editor.Phase()
		return p.Kind == EditorFailed && p.Key == editorKey("2", "d_2") && p.Failure == EditorGone
	})
}

func TestEditorObserversCanBeRemoved(t *testing.T) {
	h := newEditorHarness(t)
	h.token.Cancel()
	h.editor.Show(ptrCase(editorCase("1", "d_1", 1)))
	h.waitReady(editorKey("1", "d_1"))
	if h.changes != 0 {
		t.Errorf("changes = %d", h.changes)
	}
}

func ptrCase(c board.Case) *board.Case { return &c }
