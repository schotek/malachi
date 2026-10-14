// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package boardreply

import (
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
)

// The rules of the board's inline reply panes (Panes) with fake panes over
// a real Editor and a fake daemon: a retired pane is saved and closed; a
// sending pane is never settled and its outcome always arrives; a pane
// with unsaved text is kept (never trimmed), retried with back-off and
// shown again with its note; another link is not proof the draft went; a
// pane taken back while it settles stays; the quit says whether anything
// is at stake (macOS BoardReplyPanesTests). Back-off delays are
// milliseconds, and every wait is for a condition.
//
// fakePane never spawns a goroutine of its own: Pane.Settle's done must
// run on the main loop, and every call into a fakePane in these tests
// already comes from the one goroutine the testLoop drains on (test code
// itself, or a closure the loop posted) — so a held settle's done is
// called synchronously from release(), called the same way.

// pendingSettle is one Settle call fakePane is holding: the outcome it
// already computed, and the done it owes.
type pendingSettle struct {
	ok   bool
	done func(ok bool)
}

// fakePane is a pane as the rules see it.
type fakePane struct {
	// results are the outcomes of the next settles; past the list,
	// fallback.
	results  []bool
	fallback bool
	// holding: settles wait for release while set, and so does every
	// settle from holdFrom on (counting from 1).
	holding    bool
	holdFrom   int
	held       []pendingSettle
	settles    int
	closes     int
	abandons   int
	hasUnsaved bool
	sending    bool
	lost       bool
	replyTitle string
}

func newFakePane() *fakePane {
	return &fakePane{fallback: true, holdFrom: 1 << 30, replyTitle: "Re: Offer"}
}

func (p *fakePane) Settle(done func(ok bool)) {
	p.settles++
	ok := p.fallback
	if len(p.results) > 0 {
		ok, p.results = p.results[0], p.results[1:]
	}
	p.hasUnsaved = !ok
	if p.holding || p.settles >= p.holdFrom {
		p.held = append(p.held, pendingSettle{ok: ok, done: done})
		return
	}
	done(ok)
}

// release lets every held settle go on.
func (p *fakePane) release() {
	p.holding = false
	p.holdFrom = 1 << 30
	held := p.held
	p.held = nil
	for _, h := range held {
		h.done(h.ok)
	}
}

func (p *fakePane) waiting() int         { return len(p.held) }
func (p *fakePane) settleCount() int     { return p.settles }
func (p *fakePane) Close()               { p.closes++ }
func (p *fakePane) Abandon()             { p.abandons++ }
func (p *fakePane) HasUnsavedText() bool { return p.hasUnsaved }
func (p *fakePane) IsSending() bool      { return p.sending }
func (p *fakePane) setSending(v bool)    { p.sending = v }
func (p *fakePane) IsLost() bool         { return p.lost }
func (p *fakePane) setLost(v bool)       { p.lost = v }
func (p *fakePane) ReplyTitle() string   { return p.replyTitle }
func (p *fakePane) closed() bool         { return p.closes > 0 }
func (p *fakePane) setFallback(v bool)   { p.fallback = v }
func (p *fakePane) setResults(v []bool)  { p.results = v }
func (p *fakePane) setHolding(v bool)    { p.holding = v }
func (p *fakePane) setHoldFrom(v int)    { p.holdFrom = v }

func panesCase(n, draft string) board.Case {
	c := board.Case{
		ID: board.CaseID("c_" + n), Account: "acc_1", Thread: api.ThreadID("t_" + n), Person: "Ann", Date: t0,
		Subject: "Offer", RuleState: board.StateYou, Reply: &board.ReplyTarget{Message: "m_2", Folder: "f_inbox"},
		Version: 1,
	}
	if draft != "" {
		c.Draft = &board.DraftLink{ID: api.DraftID(draft + "_" + n)}
	}
	return c
}

type panesHarness struct {
	t        *testing.T
	loop     *testLoop
	daemon   *draftDaemon
	editor   *Editor
	panes    *Panes[*fakePane]
	made     []*fakePane
	detached []*fakePane
	adopted  []*fakePane
	toasts   []string
	setUp    func(*fakePane)
}

func newPanesHarness(t *testing.T, timing Timing) *panesHarness {
	t.Helper()
	h := &panesHarness{t: t, loop: newTestLoop(), daemon: &draftDaemon{}, setUp: func(*fakePane) {}}
	h.editor = NewEditor(h.daemon, h.loop, discardLog())
	h.panes = NewPanes[*fakePane](h.editor, timing, h.loop, tr)
	h.panes.Make = func(key Key, params Params) (*fakePane, bool) {
		p := newFakePane()
		h.setUp(p)
		h.made = append(h.made, p)
		return p, true
	}
	h.panes.Detach = func(p *fakePane) { h.detached = append(h.detached, p) }
	h.panes.OnAdopt = func(p *fakePane) { h.adopted = append(h.adopted, p) }
	h.panes.OnToast = func(s string) { h.toasts = append(h.toasts, s) }
	return h
}

// open selects c and waits until its pane is live.
func (h *panesHarness) open(c board.Case) *fakePane {
	h.t.Helper()
	h.panes.Show(&c)
	h.loop.runUntil(h.t, func() bool {
		key, ok := h.panes.LiveKey()
		return ok && key.CaseID == c.ID
	})
	live, _ := h.panes.Live()
	return live
}

func TestPanesReadyDraftGetsAPaneAndSameKeyKeepsIt(t *testing.T) {
	h := newPanesHarness(t, Timing{RetryFirst: 20 * time.Millisecond, RetryMax: 80 * time.Millisecond})
	c1 := panesCase("1", "d")
	p := h.open(c1)
	if len(h.made) != 1 || len(h.adopted) != 1 {
		t.Fatalf("made = %d, adopted = %d", len(h.made), len(h.adopted))
	}
	// The board lists the case again (every autosave): nothing changes.
	h.panes.Show(&c1)
	h.panes.Show(&c1)
	live, ok := h.panes.Live()
	if !ok || live != p || len(h.made) != 1 || p.settleCount() != 0 {
		t.Fatalf("live changed: live=%v ok=%v made=%d settles=%d", live, ok, len(h.made), p.settleCount())
	}
}

func TestPanesAnotherSelectionSavesThenClosesThePane(t *testing.T) {
	h := newPanesHarness(t, Timing{RetryFirst: 20 * time.Millisecond, RetryMax: 80 * time.Millisecond})
	c1 := panesCase("1", "d")
	p := h.open(c1)
	p.setHolding(true)
	c2 := panesCase("2", "d")
	h.panes.Show(&c2)
	h.loop.runUntil(t, func() bool { return p.waiting() == 1 })
	if p.closed() || len(h.detached) != 0 {
		t.Fatal("closed before its save answered")
	}
	p.release()
	h.loop.runUntil(t, p.closed)
	if len(h.detached) != 1 || h.detached[0] != p || p.settleCount() != 1 || p.abandons != 0 {
		t.Errorf("detached=%d settles=%d abandons=%d", len(h.detached), p.settleCount(), p.abandons)
	}
}

// Finding 2: Send in flight, then another case.
func TestPanesSendingPaneNeverSettledAndItsSuccessArrives(t *testing.T) {
	h := newPanesHarness(t, Timing{RetryFirst: 20 * time.Millisecond, RetryMax: 80 * time.Millisecond})
	c1 := panesCase("1", "d")
	p := h.open(c1)
	p.setSending(true)
	c2 := panesCase("2", "d")
	h.panes.Show(&c2)
	h.loop.runUntil(t, func() bool {
		key, ok := h.panes.LiveKey()
		return ok && key.CaseID == "c_2"
	})
	h.loop.settle(t, 50*time.Millisecond)
	if p.settleCount() != 0 || p.closed() || len(h.detached) != 0 {
		t.Fatalf("settles=%d closed=%v detached=%d", p.settleCount(), p.closed(), len(h.detached))
	}
	// The send answers while the user is on case 2.
	p.setSending(false)
	h.panes.Ended(p, End{Kind: EndSent, Text: "Message queued for sending"})
	if len(h.toasts) != 1 || h.toasts[0] != "Message queued for sending" {
		t.Errorf("toasts = %+v", h.toasts)
	}
	if !p.closed() {
		t.Error("pane not closed")
	}
	found := false
	for _, d := range h.detached {
		if d == p {
			found = true
		}
	}
	if !found {
		t.Error("pane not detached")
	}
	// The loader hides the sent draft while the board still links it.
	h.panes.Show(&c1)
	h.loop.settle(t, 50*time.Millisecond)
	if _, ok := h.panes.Live(); ok {
		t.Error("live pane should be none")
	}
	if len(h.made) != 2 {
		t.Errorf("made = %d", len(h.made))
	}
}

func TestPanesFailedSendOutOfSightSaysWhichReplyAndKeepsThePane(t *testing.T) {
	h := newPanesHarness(t, Timing{RetryFirst: 20 * time.Millisecond, RetryMax: 80 * time.Millisecond})
	c1 := panesCase("1", "d")
	p := h.open(c1)
	p.setSending(true)
	c2 := panesCase("2", "d")
	h.panes.Show(&c2)
	p.setSending(false)
	h.panes.SendFailed(p)
	if len(h.toasts) != 1 || h.toasts[0] != board.ReplyNotSent("Re: Offer", tr) {
		t.Errorf("toasts = %+v", h.toasts)
	}
	h.loop.runUntil(t, func() bool { return p.settleCount() == 1 })
	h.loop.settle(t, 50*time.Millisecond)
	if p.closed() || len(h.detached) != 0 {
		t.Error("kept for the user to come back")
	}
	// Back on case 1: the same pane, Send there again.
	h.panes.Show(&c1)
	live, ok := h.panes.Live()
	if !ok || live != p {
		t.Fatalf("live = %v, %v", live, ok)
	}
}

func TestPanesFailedSendInSightAddsNothing(t *testing.T) {
	h := newPanesHarness(t, Timing{RetryFirst: 20 * time.Millisecond, RetryMax: 80 * time.Millisecond})
	c1 := panesCase("1", "d")
	p := h.open(c1)
	h.panes.SendFailed(p)
	live, ok := h.panes.Live()
	if len(h.toasts) != 0 || !ok || live != p || p.settleCount() != 0 {
		t.Fatalf("toasts=%+v live=%v settles=%d", h.toasts, live, p.settleCount())
	}
}

// Finding 3: unsaved text is kept, retried, never trimmed.
func TestPanesUnsavedPaneIsKeptAndRetriedWithBackOff(t *testing.T) {
	h := newPanesHarness(t, Timing{RetryFirst: 20 * time.Millisecond, RetryMax: 80 * time.Millisecond})
	c1 := panesCase("1", "d")
	p := h.open(c1)
	p.setResults([]bool{false, false, false})
	c2 := panesCase("2", "d")
	h.panes.Show(&c2)
	// Two identical failures do not end the retries.
	h.loop.runUntil(t, func() bool { return p.settleCount() == 4 })
	h.loop.runUntil(t, p.closed)
	if p.abandons != 0 {
		t.Errorf("abandons = %d", p.abandons)
	}
}

func TestPanesWithUnsavedTextAreNeverTrimmed(t *testing.T) {
	h := newPanesHarness(t, Timing{RetryFirst: 10 * time.Minute, RetryMax: 10 * time.Minute})
	h.setUp = func(p *fakePane) { p.setFallback(false) }
	for n := 1; n <= 6; n++ {
		h.open(panesCase(itoa(n), "d"))
	}
	h.panes.Show(nil)
	h.loop.runUntil(t, func() bool {
		for _, p := range h.made {
			if p.settleCount() != 1 {
				return false
			}
		}
		return true
	})
	h.loop.settle(t, 50*time.Millisecond)
	if len(h.made) != 6 {
		t.Fatalf("made = %d", len(h.made))
	}
	for _, p := range h.made {
		if p.closed() || p.abandons != 0 {
			t.Errorf("pane closed=%v abandons=%d", p.closed(), p.abandons)
		}
	}
	if len(h.detached) != 0 {
		t.Errorf("detached = %d", len(h.detached))
	}
}

func TestPanesComingBackShowsTheKeptPaneWithItsNote(t *testing.T) {
	h := newPanesHarness(t, Timing{RetryFirst: 10 * time.Minute, RetryMax: 10 * time.Minute})
	c1 := panesCase("1", "d")
	p := h.open(c1)
	p.setFallback(false)
	c2 := panesCase("2", "d")
	h.panes.Show(&c2)
	h.loop.runUntil(t, func() bool { return p.settleCount() == 1 })
	h.panes.Show(&c1)
	live, ok := h.panes.Live()
	if !ok || live != p {
		t.Fatalf("live = %v, %v", live, ok)
	}
	if slot := h.panes.Slot(c1.ID); slot.Kind != SlotPane || !slot.Unsaved {
		t.Errorf("slot = %+v", slot)
	}
}

func TestPanesNoteGoesOnceAKeptPaneSaves(t *testing.T) {
	h := newPanesHarness(t, Timing{RetryFirst: 20 * time.Millisecond, RetryMax: 80 * time.Millisecond})
	c1 := panesCase("1", "d")
	p := h.open(c1)
	p.setResults([]bool{false})
	// The retry waits until the user is back.
	p.setHoldFrom(2)
	c2 := panesCase("2", "d")
	h.panes.Show(&c2)
	// The first failed; its retry has begun and waits.
	h.loop.runUntil(t, func() bool { return p.waiting() == 1 })
	h.panes.Show(&c1)
	if slot := h.panes.Slot(c1.ID); slot.Kind != SlotPane || !slot.Unsaved {
		t.Fatalf("slot = %+v", slot)
	}
	// The retry saves while the user is back in it: the note goes, the
	// pane stays the live one.
	p.release()
	h.loop.runUntil(t, func() bool {
		slot := h.panes.Slot(c1.ID)
		return slot.Kind == SlotPane && !slot.Unsaved
	})
	live, ok := h.panes.Live()
	if !ok || live != p || p.closed() {
		t.Fatalf("live=%v ok=%v closed=%v", live, ok, p.closed())
	}
}

// Finding 4: another link, or none, is not proof the draft went.
func TestPanesChangedLinkSavesThePaneInsteadOfAbandoningIt(t *testing.T) {
	h := newPanesHarness(t, Timing{RetryFirst: 20 * time.Millisecond, RetryMax: 80 * time.Millisecond})
	c1 := panesCase("1", "d")
	p := h.open(c1)
	other := panesCase("1", "other")
	h.panes.Show(&other)
	h.loop.runUntil(t, p.closed)
	if p.settleCount() != 1 || p.abandons != 0 || len(h.toasts) != 0 {
		t.Errorf("settles=%d abandons=%d toasts=%+v", p.settleCount(), p.abandons, h.toasts)
	}
	// The new link gets its own pane.
	h.loop.runUntil(t, func() bool {
		key, ok := h.panes.LiveKey()
		return ok && key.Draft == "other_1"
	})
	c2 := panesCase("2", "d")
	q := h.open(c2)
	none := panesCase("2", "")
	h.panes.Show(&none)
	h.loop.runUntil(t, q.closed)
	if q.abandons != 0 || len(h.toasts) != 0 {
		t.Errorf("abandons=%d toasts=%+v", q.abandons, h.toasts)
	}
}

func TestPanesOnlyTheDraftControllersLostAbandons(t *testing.T) {
	h := newPanesHarness(t, Timing{RetryFirst: 20 * time.Millisecond, RetryMax: 80 * time.Millisecond})
	c1 := panesCase("1", "d")
	p := h.open(c1)
	p.setHolding(true)
	none := panesCase("1", "")
	h.panes.Show(&none)
	h.loop.runUntil(t, func() bool { return p.waiting() == 1 })
	// The save said draftNotFound: the draft controller abandoned and
	// reported it.
	p.setLost(true)
	p.setFallback(false)
	h.panes.Ended(p, End{Kind: EndLost})
	p.release()
	h.loop.runUntil(t, func() bool { return len(h.detached) == 1 })
	h.loop.settle(t, 50*time.Millisecond)
	if len(h.toasts) != 1 || h.toasts[0] != board.ReplyRemoved(tr) {
		t.Errorf("toasts = %+v", h.toasts)
	}
	if p.settleCount() != 1 {
		t.Errorf("settles = %d, no retry for a lost draft", p.settleCount())
	}
}

// Finding 6.
func TestPanesTakenBackWhileItSavesIsNotReleasedUnderTheUser(t *testing.T) {
	h := newPanesHarness(t, Timing{RetryFirst: 20 * time.Millisecond, RetryMax: 80 * time.Millisecond})
	c1 := panesCase("1", "d")
	p := h.open(c1)
	p.setHolding(true)
	c2 := panesCase("2", "d")
	h.panes.Show(&c2)
	h.loop.runUntil(t, func() bool { return p.waiting() == 1 })
	// Back at once: the same pane, no loading row while it saves.
	h.panes.Show(&c1)
	live, ok := h.panes.Live()
	if !ok || live != p {
		t.Fatalf("live=%v ok=%v", live, ok)
	}
	p.release()
	h.loop.settle(t, 50*time.Millisecond)
	live, ok = h.panes.Live()
	detached := false
	for _, d := range h.detached {
		if d == p {
			detached = true
		}
	}
	if !ok || live != p || p.closed() || detached {
		t.Fatalf("live=%v ok=%v closed=%v detached=%v", live, ok, p.closed(), detached)
	}
	// Left again later: saved and closed as usual.
	h.panes.Show(nil)
	h.loop.runUntil(t, p.closed)
}

func TestPanesKeptWithNothingAtStakeAreBounded(t *testing.T) {
	h := newPanesHarness(t, Timing{RetryFirst: 20 * time.Millisecond, RetryMax: 80 * time.Millisecond})
	var kept []*fakePane
	for n := 1; n <= KeptLimit+1; n++ {
		p := h.open(panesCase(itoa(n), "d"))
		p.setSending(true)
		h.panes.Show(nil)
		p.setSending(false)
		h.panes.SendFailed(p)
		h.loop.runUntil(t, func() bool { return p.settleCount() == 1 })
		kept = append(kept, p)
	}
	// The oldest is settled once more and closed; the others stay.
	h.loop.runUntil(t, kept[0].closed)
	if kept[0].settleCount() != 2 {
		t.Errorf("oldest settles = %d", kept[0].settleCount())
	}
	for _, p := range kept[1:] {
		if p.closed() {
			t.Error("a newer kept pane was closed")
		}
	}
}

func TestPanesDiscardUnderWayIsNotSavedOver(t *testing.T) {
	h := newPanesHarness(t, Timing{RetryFirst: 20 * time.Millisecond, RetryMax: 80 * time.Millisecond})
	c1 := panesCase("1", "d")
	p := h.open(c1)
	h.panes.Discarding(p, true)
	// The board drops the link at once (optimistic).
	none := panesCase("1", "")
	h.panes.Show(&none)
	h.loop.settle(t, 50*time.Millisecond)
	live, ok := h.panes.Live()
	if !ok || live != p || p.settleCount() != 0 {
		t.Fatalf("live=%v ok=%v settles=%d", live, ok, p.settleCount())
	}
	h.panes.Ended(p, End{Kind: EndDiscarded})
	if !p.closed() || len(h.toasts) != 0 {
		t.Errorf("closed=%v toasts=%+v", p.closed(), h.toasts)
	}
}

func TestPanesFailedDiscardOfARetiredPaneSavesIt(t *testing.T) {
	h := newPanesHarness(t, Timing{RetryFirst: 20 * time.Millisecond, RetryMax: 80 * time.Millisecond})
	c1 := panesCase("1", "d")
	p := h.open(c1)
	h.panes.Discarding(p, true)
	c2 := panesCase("2", "d")
	h.panes.Show(&c2)
	h.loop.settle(t, 50*time.Millisecond)
	if p.settleCount() != 0 {
		t.Fatalf("settles = %d", p.settleCount())
	}
	h.panes.Discarding(p, false)
	h.loop.runUntil(t, p.closed)
	if p.settleCount() != 1 {
		t.Errorf("settles = %d", p.settleCount())
	}
}

func TestPanesDiscardedLinkThatCameBackLoadsAgain(t *testing.T) {
	h := newPanesHarness(t, Timing{RetryFirst: 20 * time.Millisecond, RetryMax: 80 * time.Millisecond})
	c1 := panesCase("1", "d")
	p := h.open(c1)
	asked := h.daemon.askedCount()
	h.panes.Ended(p, End{Kind: EndDiscarded})
	if !p.closed() {
		t.Fatal("pane not closed")
	}
	h.loop.runUntil(t, func() bool {
		live, ok := h.panes.Live()
		return ok && live != p
	})
	if n := h.daemon.askedCount(); n != asked+1 {
		t.Errorf("asked = %d, want %d", n, asked+1)
	}
}

// The quit.
func TestPanesQuittingSavesEverything(t *testing.T) {
	h := newPanesHarness(t, Timing{RetryFirst: 20 * time.Millisecond, RetryMax: 80 * time.Millisecond})
	c1 := panesCase("1", "d")
	p := h.open(c1)
	done := make(chan struct{})
	h.panes.QuitWait(10*time.Second, func() { close(done) })
	h.loop.runUntil(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	})
	if h.panes.HasUnsavedOrUnsent() {
		t.Error("should have nothing left")
	}
	if p.settleCount() != 1 || !p.closed() {
		t.Errorf("settles=%d closed=%v", p.settleCount(), p.closed())
	}
}

func TestPanesQuittingWithAReplyThatCannotBeSavedSaysSo(t *testing.T) {
	h := newPanesHarness(t, Timing{RetryFirst: 10 * time.Minute, RetryMax: 10 * time.Minute})
	c1 := panesCase("1", "d")
	p := h.open(c1)
	p.setFallback(false)
	done := make(chan struct{})
	h.panes.QuitWait(10*time.Second, func() { close(done) })
	h.loop.runUntil(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	})
	if !h.panes.HasUnsavedOrUnsent() {
		t.Error("should have something left")
	}
	if p.closed() || p.abandons != 0 {
		t.Errorf("closed=%v abandons=%d", p.closed(), p.abandons)
	}
	// The user stays: the pane is there again.
	h.panes.Resume()
	h.panes.Show(&c1)
	live, ok := h.panes.Live()
	if !ok || live != p {
		t.Fatalf("live=%v ok=%v", live, ok)
	}
	if slot := h.panes.Slot(c1.ID); !slot.Unsaved {
		t.Errorf("slot = %+v", slot)
	}
}

func TestPanesQuittingTriesAKeptPaneOnceMore(t *testing.T) {
	h := newPanesHarness(t, Timing{RetryFirst: 10 * time.Minute, RetryMax: 10 * time.Minute})
	c1 := panesCase("1", "d")
	p := h.open(c1)
	p.setResults([]bool{false})
	h.panes.Show(nil)
	h.loop.runUntil(t, func() bool { return p.settleCount() == 1 })
	done := make(chan struct{})
	h.panes.QuitWait(10*time.Second, func() { close(done) })
	h.loop.runUntil(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	})
	if p.settleCount() != 2 || !p.closed() {
		t.Errorf("settles=%d closed=%v", p.settleCount(), p.closed())
	}
}

func TestPanesQuittingWaitsForASendAtMostTheBound(t *testing.T) {
	h := newPanesHarness(t, Timing{RetryFirst: 20 * time.Millisecond, RetryMax: 80 * time.Millisecond})
	c1 := panesCase("1", "d")
	p := h.open(c1)
	p.setSending(true)
	start := time.Now()
	done := make(chan struct{})
	h.panes.QuitWait(100*time.Millisecond, func() { close(done) })
	h.loop.runUntil(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	})
	if time.Since(start) < 100*time.Millisecond {
		t.Errorf("returned too soon: %v", time.Since(start))
	}
	if !h.panes.HasUnsavedOrUnsent() {
		t.Error("a send still owed is something left")
	}
	if p.settleCount() != 0 || p.closed() {
		t.Errorf("settles=%d closed=%v", p.settleCount(), p.closed())
	}
}

func TestPanesQuittingEndsWhenTheSendAnswers(t *testing.T) {
	h := newPanesHarness(t, Timing{RetryFirst: 20 * time.Millisecond, RetryMax: 80 * time.Millisecond})
	c1 := panesCase("1", "d")
	p := h.open(c1)
	p.setSending(true)
	go func() {
		time.Sleep(30 * time.Millisecond)
		h.loop.Post(func() {
			p.setSending(false)
			h.panes.Ended(p, End{Kind: EndSent, Text: "Message queued for sending"})
		})
	}()
	done := make(chan struct{})
	h.panes.QuitWait(10*time.Second, func() { close(done) })
	h.loop.runUntil(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	})
	if len(h.toasts) != 1 || h.toasts[0] != "Message queued for sending" {
		t.Errorf("toasts = %+v", h.toasts)
	}
}

func itoa(n int) string {
	digits := "0123456789"
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{digits[n%10]}, b...)
		n /= 10
	}
	return string(b)
}

func TestPanesHasSendingTellsASendFromUnsavedText(t *testing.T) {
	h := newPanesHarness(t, Timing{RetryFirst: 20 * time.Millisecond, RetryMax: 80 * time.Millisecond})
	c1 := panesCase("1", "d")
	p := h.open(c1)
	p.setSending(true)
	c2 := panesCase("2", "d")
	h.panes.Show(&c2)
	h.loop.runUntil(t, func() bool {
		key, ok := h.panes.LiveKey()
		return ok && key.CaseID == "c_2"
	})
	if !h.panes.HasSending() || h.panes.HasUnsaved() {
		t.Errorf("sending=%v unsaved=%v", h.panes.HasSending(), h.panes.HasUnsaved())
	}
	p.setSending(false)
	h.panes.Ended(p, End{Kind: EndSent, Text: "Message queued for sending"})
	if h.panes.HasSending() {
		t.Error("still sending after the answer")
	}
}
