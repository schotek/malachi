// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The rules behind the board's inline reply editor for one main window:
// when a pane is made, shown, kept, settled, closed or abandoned. Editor
// (editor.go) says which draft the selected case links; this file decides
// what happens to the panes that edit such drafts. The panes themselves
// (a compose pane in the app, fakes in the tests) are reached through
// Pane; the window only makes them, puts their views into the detail and
// forwards their ends here.
//
// The theme of every rule: what the user typed, and the outcome of a Send,
// are never lost silently.
//
// Swift-first, like the board.

package boardreply

import (
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistantpanel"
	"github.com/schotek/malachi/ui/internal/board"
)

// Pane is what Panes needs of a compose pane editing a case's suggested
// reply (ComposePane in the app, fakes in the tests); the window only
// makes them, puts their views into the detail and forwards their ends
// here (Discarding, SendFailed, Ended). A pane is made over
// ui/internal/compose's draft controller with owner board, not window (the
// GTK port of ComposeDraftController lives in compose/draft.go,
// outside this package); ComposeDraftBoardOwnerTests names the guarantees
// such a pane must give, one sentence each:
//
//   - Close is the controller's cleanup, never its finish, and never asks
//     before closing: the board keeps the draft (closingNeverAsksAndNeverDeletes),
//     even a late save after Close answers never deletes it either
//     (aLateSaveNeverDeletes) — the window owner's comment draft is the
//     one contrast that still deletes on close
//     (theWindowStillDeletesAClosedComment).
//   - Settle flushes the editor, saves while there is a real, reported
//     change or a save already under way, first waits out a send under
//     way so its outcome is never missed
//     (settleWaitsForASendUnderWay, settleSavesAndLeavesTheControllerOpen),
//     and leaves the controller open and reusable either way.
//   - What the editor reports before it is known ready, or its own
//     normalisation of a draft it was only given (never typed), is not an
//     edit: nothing is saved unless two reads around the flush actually
//     differ (anUntouchedPaneSavesNothing, theEditorsNormalisationBeforeItIsKnownIsNoEdit,
//     anEditSavesExactlyOnce).
//   - A save that conflicts with a newer one elsewhere keeps the user's
//     own text and writes it into the very same draft, fetched fresh with
//     draft.get for its version, never a new one
//     (aConflictKeepsOurTextInTheSameDraft, finishRidesOutAConflict).
//   - A draft deleted elsewhere is reported once as lost (IsLost) and
//     never recreated by a later save, unlike the window owner, which
//     starts a fresh one (aDraftDeletedElsewhereIsLostNotRecreated,
//     contrast theWindowStillStartsANewDraftWhenItsDraftWent).
//   - Discard goes through the host's DiscardStoredDraft (Controller's of
//     the same name) when one is wired, a plain draft.delete otherwise
//     (discardGoesThroughTheHost, discardWithoutAHostDeletesTheDraft), and
//     a failed discard keeps the pane open with a toast
//     (aFailedDiscardKeepsTheForm); Abandon never saves or deletes at all
//     (abandonForgetsWithoutSavingOrDeleting).
//
// The real-draft-controller tests of BoardReplyPanesTests.swift
// (aPaneLeftWithoutAnEditSavesNothing, anEditIsSavedOnceWhenThePaneIsLeft)
// are the acceptance list once such a pane exists in ui/internal/compose;
// they are not ported here (see the package's final report).
type Pane interface {
	// Settle saves everything typed and calls done once, on the main
	// loop, with whether nothing typed is left unsaved. Never cleans up:
	// the pane stays usable either way.
	Settle(done func(ok bool))
	// Close ends the pane for good once nothing is at stake (sent,
	// discarded, or settled): the draft controller cleans up. Idempotent.
	Close()
	// Abandon forgets the pane without saving: its draft is gone.
	// Idempotent.
	Abandon()
	// HasUnsavedText reports edits not yet saved, or a save under way.
	HasUnsavedText() bool
	// IsSending reports that Send was pressed and has not answered yet.
	IsSending() bool
	// IsLost reports the draft was deleted elsewhere (draftNotFound).
	IsLost() bool
	// ReplyTitle is the reply's subject (or a comment's title), for a
	// toast about a pane out of sight.
	ReplyTitle() string
}

// ComparablePane is Pane plus comparable, so Panes can tell its panes
// apart by identity (==, as Swift's === does): a real pane is always a
// pointer type, which is always comparable.
type ComparablePane interface {
	Pane
	comparable
}

// Params is what a pane is made from: the account and the draft as
// Editor's ready phase holds it (ready to open as an edit,
// ui/internal/compose.FromDraft outside this package), and the case's
// reply target (the message and folder a reply to it answers; on a Jira
// account, the comment's issue) for whatever the pane needs beyond the
// draft itself. The owner is always the board (see Pane's doc comment):
// a pane made from Params never deletes its draft on Close and never asks
// before it.
type Params struct {
	Account api.AccountID
	Draft   api.Draft
	Reply   board.ReplyTarget
}

// SlotKind is what a case's reply slot shows.
type SlotKind int

// The kinds.
const (
	// SlotNone: nothing of the panes': Suggest Reply, or the samples'
	// block.
	SlotNone SlotKind = iota
	// SlotLoading: the draft is loading.
	SlotLoading
	// SlotFailed: the draft could not be opened; Retry offers Try Again.
	SlotFailed
	// SlotPane: the case's pane; Unsaved: what was typed could not be
	// saved yet (the pane keeps trying).
	SlotPane
)

// Slot is what one case's reply slot shows (Panes.Slot).
type Slot[P ComparablePane] struct {
	Kind SlotKind
	// Pane is set for SlotPane.
	Pane P
	// Retry is set for SlotFailed: Try Again is offered (a backend
	// failure, not a gone draft).
	Retry bool
	// Unsaved is set for SlotPane.
	Unsaved bool
}

// EndKind is how a pane ended (ComposePane.End without the widgets).
type EndKind int

// The kinds.
const (
	// EndSent: Text is the confirmation ("Message queued for sending").
	EndSent EndKind = iota
	EndDiscarded
	EndClosed
	// EndLost: the draft was deleted elsewhere.
	EndLost
)

// End is how a pane ended.
type End struct {
	Kind EndKind
	// Text is set for EndSent.
	Text string
}

// Timing is the back-off of a kept pane's settles (tests shorten it).
type Timing struct {
	// RetryFirst is the wait before the first retry; doubled after every
	// failure.
	RetryFirst time.Duration
	// RetryMax is the longest wait between retries.
	RetryMax time.Duration
}

// KeptLimit: at most this many panes with nothing at stake are kept
// (those kept after a failed send); the oldest is closed beyond that.
// Panes with unsaved text are never counted nor closed.
const KeptLimit = 3

// entry is one pane Panes holds, live or parked.
type entry[P ComparablePane] struct {
	key  Key
	pane P
	// settling: a Settle call is on its way.
	settling bool
	// retryArmed: a retry of a kept pane with unsaved text is scheduled;
	// retryGen invalidates a stale timer.
	retryArmed bool
	retryGen   int
	// failures counts the settles that failed in a row.
	failures int
	// unsaved: its last settle failed; it holds unsaved text.
	unsaved bool
	// awaitingSend: retired while sending; waits for the outcome.
	awaitingSend bool
	// keep: kept after a failed send; shown again when its case is.
	keep bool
	// ending: Discard is under way: a dropped link is its doing.
	ending bool
}

// pending reports something still to come before the entry can be judged.
func (e *entry[P]) pending() bool { return e.settling || e.awaitingSend || e.ending }

// Panes are the inline reply panes of one window over Editor
// (BoardReplyPanes).
//
// The rules:
//   - One live pane, the selected case's, made when the loader has the
//     draft ready, or a parked pane of the same case and draft taken
//     back.
//   - Retired, never dropped: when its case is no longer the one shown
//     (another selection, the case gone, Mail mode, a closed window,
//     quit) or the board shows its case linking another draft or none
//     (not proof the draft went), the live pane is parked and settled
//     (saved). Only the draft controller's draftNotFound (End.Kind
//     EndLost) abandons it.
//   - Sending panes are never settled, closed or released before the
//     send answered; the outcome is always delivered: success → the
//     queued toast and Ended(key) wherever the user is; failure → the
//     draft controller's own toast and, out of sight,
//     board.ReplyNotSent, and the pane is kept so the user can come back
//     to it.
//   - Unsaved text is kept: a parked pane whose settle failed stays,
//     whatever their number, and settles again with back-off (Timing);
//     its case shows it again with Slot.Kind SlotPane, Unsaved true.
//     Only panes with nothing at stake (kept after a failed send, saved)
//     are bounded (KeptLimit).
//   - A pane taken back while it settles stays: a settle that ends while
//     the user is typing in the pane again does not close it.
type Panes[P ComparablePane] struct {
	// Make builds the pane for a draft the loader has ready; ok false:
	// none made.
	Make func(Key, Params) (pane P, ok bool)
	// Detach: the pane is gone from here for good; take its view away.
	Detach func(P)
	// OnToast is a short message over the page.
	OnToast func(string)
	// OnChange: what a slot shows changed.
	OnChange func()
	// OnAdopt: a pane became the live one (made, or taken back).
	OnAdopt func(P)

	loader *Editor
	timing Timing
	loop   assistantpanel.Loop
	tr     board.Translator

	live        *entry[P]
	parked      []*entry[P]
	selected    *board.Case
	suspended   bool
	loaderToken *board.ObserverToken

	// quitDone and quitGen are QuitWait's wait: done once nothing is
	// pending, or once the bound passed.
	quitDone func()
	quitGen  int
}

// NewPanes makes the inline reply panes of one window: loader is the
// window's Editor, loop the main loop (for the back-off's timers), tr the
// translator of the toasts (board.ReplyNotSent, board.ReplyRemoved). A
// zero Timing field is DefaultTiming's.
func NewPanes[P ComparablePane](loader *Editor, timing Timing, loop assistantpanel.Loop, tr board.Translator) *Panes[P] {
	d := DefaultTiming()
	if timing.RetryFirst <= 0 {
		timing.RetryFirst = d.RetryFirst
	}
	if timing.RetryMax <= 0 {
		timing.RetryMax = d.RetryMax
	}
	p := &Panes[P]{loader: loader, timing: timing, loop: loop, tr: tr}
	p.loaderToken = loader.Observe(p.phaseChanged)
	return p
}

// DefaultTiming is the back-off NewPanes uses for a zero Timing field.
func DefaultTiming() Timing { return Timing{RetryFirst: 5 * time.Second, RetryMax: 120 * time.Second} }

// Close stops following the loader (the window is closing); any pane it
// still holds is left as it is.
func (p *Panes[P]) Close() {
	if p.loaderToken != nil {
		p.loaderToken.Cancel()
		p.loaderToken = nil
	}
}

// What is shown.

// Live is the live pane, false when there is none.
func (p *Panes[P]) Live() (P, bool) {
	if p.live == nil {
		var zero P
		return zero, false
	}
	return p.live.pane, true
}

// LiveKey is the live pane's key, false when there is none.
func (p *Panes[P]) LiveKey() (Key, bool) {
	if p.live == nil {
		return Key{}, false
	}
	return p.live.key, true
}

// All is every pane this type holds, live and parked.
func (p *Panes[P]) All() []P {
	out := make([]P, 0, len(p.parked)+1)
	if p.live != nil {
		out = append(out, p.live.pane)
	}
	for _, e := range p.parked {
		out = append(out, e.pane)
	}
	return out
}

// Slot is what the reply slot of case id shows.
func (p *Panes[P]) Slot(id board.CaseID) Slot[P] {
	if p.live != nil && p.live.key.CaseID == id {
		return Slot[P]{Kind: SlotPane, Pane: p.live.pane, Unsaved: p.live.unsaved}
	}
	phase := p.loader.Phase()
	key, ok := phase.hasKey()
	if !ok || key.CaseID != id {
		return Slot[P]{Kind: SlotNone}
	}
	switch phase.Kind {
	case EditorLoading, EditorReady:
		return Slot[P]{Kind: SlotLoading}
	case EditorFailed:
		return Slot[P]{Kind: SlotFailed, Retry: phase.Failure == EditorBackend}
	}
	return Slot[P]{Kind: SlotNone}
}

// keyOf is the key a case's link gives.
func keyOf(c board.Case) (Key, bool) {
	if c.Draft == nil {
		return Key{}, false
	}
	return Key{CaseID: c.ID, Account: c.Account, Draft: c.Draft.ID}, true
}

// Following the board.

// Show is the selected case (nil: none), after every change of the board:
// the live pane stays for its case and draft, and is retired otherwise; a
// parked pane of the case's draft is taken back; the loader is told.
func (p *Panes[P]) Show(c *board.Case) {
	p.selected = c
	if p.suspended {
		return
	}
	if e := p.live; e != nil {
		switch {
		case c != nil && c.ID == e.key.CaseID:
			if !e.ending && !e.pane.IsSending() && (c.Draft == nil || c.Draft.ID != e.key.Draft) {
				// Another link, or none: saved first; only the daemon's
				// draftNotFound makes it lost.
				p.retire(e)
			}
		default:
			p.retire(e)
		}
	}
	if p.live == nil && c != nil {
		if key, ok := keyOf(*c); ok {
			if i := p.findParked(key); i >= 0 {
				p.adopt(p.takeParked(i))
			}
		}
	}
	p.loader.Show(c)
	p.phaseChanged()
}

// Suspend: the page left the window (Mail mode, a closed window): the live
// pane is retired and nothing loads until Resume.
func (p *Panes[P]) Suspend() {
	if p.suspended {
		return
	}
	if p.live != nil {
		p.retire(p.live)
	}
	p.suspended = true
	p.loader.Show(nil)
}

// Resume: the page is back; Show the selected case next.
func (p *Panes[P]) Resume() { p.suspended = false }

// phaseChanged: the loader's phase changed: a ready draft of the selected
// case gets its pane (a parked one, or a new one).
func (p *Panes[P]) phaseChanged() {
	defer p.changed()
	if p.suspended || p.live != nil {
		return
	}
	phase := p.loader.Phase()
	if phase.Kind != EditorReady || p.selected == nil || p.selected.ID != phase.Key.CaseID {
		return
	}
	key := phase.Key
	if i := p.findParked(key); i >= 0 {
		p.adopt(p.takeParked(i))
		return
	}
	if p.Make == nil {
		return
	}
	var reply board.ReplyTarget
	if p.selected.Reply != nil {
		reply = *p.selected.Reply
	}
	pane, ok := p.Make(key, Params{Account: key.Account, Draft: phase.Draft, Reply: reply})
	if !ok {
		return
	}
	p.adopt(&entry[P]{key: key, pane: pane})
}

func (p *Panes[P]) adopt(e *entry[P]) {
	// The user's again: retired later, it is settled and closed like any
	// other.
	e.keep = false
	p.live = e
	if p.OnAdopt != nil {
		p.OnAdopt(e.pane)
	}
}

func (p *Panes[P]) findParked(key Key) int {
	for i, e := range p.parked {
		if e.key == key {
			return i
		}
	}
	return -1
}

func (p *Panes[P]) takeParked(i int) *entry[P] {
	e := p.parked[i]
	p.parked = append(p.parked[:i], p.parked[i+1:]...)
	return e
}

func (p *Panes[P]) removeParked(e *entry[P]) {
	for i, k := range p.parked {
		if k == e {
			p.parked = append(p.parked[:i], p.parked[i+1:]...)
			return
		}
	}
}

// Retiring and settling.

// retire: the live pane's case is no longer the one shown: parked, and
// saved unless it is sending (then it waits for the send) or discarding.
func (p *Panes[P]) retire(e *entry[P]) {
	if p.live == e {
		p.live = nil
	}
	p.parked = append(p.parked, e)
	switch {
	case e.pane.IsSending():
		e.awaitingSend = true
	case !e.ending:
		p.settle(e)
	}
	p.changed()
}

func (p *Panes[P]) settle(e *entry[P]) {
	if e.settling {
		return
	}
	e.retryArmed = false
	e.retryGen++
	e.settling = true
	e.pane.Settle(func(ok bool) { p.settled(e, ok) })
}

func (p *Panes[P]) tracked(e *entry[P]) bool {
	if p.live == e {
		return true
	}
	for _, k := range p.parked {
		if k == e {
			return true
		}
	}
	return false
}

func (p *Panes[P]) settled(e *entry[P], ok bool) {
	e.settling = false
	defer p.checkQuit()
	if !p.tracked(e) {
		return
	}
	if ok {
		e.unsaved = false
		e.failures = 0
		switch {
		case p.live == e:
			// Taken back while it settled: it stays the user's.
			p.changed()
		case e.keep:
			p.trimKept()
			p.changed()
		default:
			p.removeParked(e)
			e.pane.Close()
			if p.Detach != nil {
				p.Detach(e.pane)
			}
			p.changed()
		}
		return
	}
	// Lost: its end (EndLost) has come or is coming.
	if e.pane.IsLost() {
		return
	}
	e.unsaved = true
	e.failures++
	p.scheduleRetry(e)
	p.changed()
}

// scheduleRetry is the next settle of a pane with unsaved text, later each
// time.
func (p *Panes[P]) scheduleRetry(e *entry[P]) {
	e.retryGen++
	gen := e.retryGen
	delay := p.timing.RetryFirst
	for i := 1; i < max(e.failures, 1); i++ {
		if delay < p.timing.RetryMax {
			delay *= 2
		}
	}
	delay = min(delay, p.timing.RetryMax)
	e.retryArmed = true
	p.loop.After(delay, func() {
		if gen != e.retryGen || !e.retryArmed || !p.tracked(e) || !e.unsaved {
			return
		}
		e.retryArmed = false
		p.settle(e)
	})
}

// trimKept: beyond KeptLimit panes with nothing at stake the oldest is
// closed (settled once more first: should it hold text after all, it
// stays).
func (p *Panes[P]) trimKept() {
	var clean []*entry[P]
	for _, e := range p.parked {
		if e.keep && !e.unsaved && !e.pending() && !e.pane.HasUnsavedText() {
			clean = append(clean, e)
		}
	}
	if len(clean) <= KeptLimit {
		return
	}
	oldest := clean[0]
	oldest.keep = false
	p.settle(oldest)
}

// What the panes report.

// Discarding: discard is under way (true) or failed (false) for pane:
// while it runs, a dropped link is its doing and the pane is not saved.
func (p *Panes[P]) Discarding(pane P, on bool) {
	e := p.entryOf(pane)
	if e == nil {
		return
	}
	e.ending = on
	if !on && p.live != e {
		// Retired while it ran: what was typed is saved after all.
		p.settle(e)
	}
	p.checkQuit()
}

// SendFailed: the send of pane failed (the draft controller said why in
// its own toast and gave Send back). Out of sight, the user hears which
// reply, and the pane is kept for when they come back.
func (p *Panes[P]) SendFailed(pane P) {
	e := p.entryOf(pane)
	if e == nil {
		return
	}
	e.awaitingSend = false
	if p.live != e {
		if p.OnToast != nil {
			p.OnToast(board.ReplyNotSent(e.pane.ReplyTitle(), p.tr))
		}
		e.keep = true
		p.settle(e)
	}
	p.changed()
	p.checkQuit()
}

// Ended: pane ended (End).
func (p *Panes[P]) Ended(pane P, end End) {
	e := p.entryOf(pane)
	if e == nil {
		return
	}
	if p.live == e {
		p.live = nil
	}
	p.removeParked(e)
	e.retryArmed = false
	e.retryGen++
	reload := false
	switch end.Kind {
	case EndSent:
		// "Message queued for sending", or the comment's.
		if p.OnToast != nil {
			p.OnToast(end.Text)
		}
		p.loader.Ended(e.key)
	case EndLost:
		if p.OnToast != nil {
			p.OnToast(board.ReplyRemoved(p.tr))
		}
		p.loader.Ended(e.key)
	case EndDiscarded, EndClosed:
		// The board dropped the link already; should the discard fail,
		// the link comes back and the draft loads again.
		if key, ok := p.loader.Phase().hasKey(); ok {
			reload = key == e.key
		}
	}
	e.pane.Close()
	if p.Detach != nil {
		p.Detach(e.pane)
	}
	if reload && !p.suspended {
		p.loader.Show(nil)
		p.loader.Show(p.selected)
	}
	p.changed()
	p.checkQuit()
}

func (p *Panes[P]) entryOf(pane P) *entry[P] {
	if p.live != nil && p.live.pane == pane {
		return p.live
	}
	for _, e := range p.parked {
		if e.pane == pane {
			return e
		}
	}
	return nil
}

func (p *Panes[P]) changed() {
	if p.OnChange != nil {
		p.OnChange()
	}
}

// Quitting.

// QuitWait settles every pane with something at stake (or waits for its
// send or discard to answer), at most limit, then calls done once:
// whatever could be settled within the bound was. The caller then asks
// HasUnsavedOrUnsent, once done ran, to know whether anything was left (the
// "Quit without saving a reply?" question) before actually quitting;
// Resume and Show(selected) if the user stays. Non-blocking: done may run
// before QuitWait returns (nothing was pending) or later, from the main
// loop.
func (p *Panes[P]) QuitWait(limit time.Duration, done func()) {
	if p.live != nil {
		p.retire(p.live)
	}
	p.suspended = true
	p.loader.Show(nil)
	for _, e := range p.parked {
		if e.unsaved && !e.settling {
			p.settle(e)
		}
	}
	if !p.anyPending() {
		if done != nil {
			done()
		}
		return
	}
	p.quitDone = done
	p.quitGen++
	gen := p.quitGen
	p.loop.After(limit, func() {
		if gen != p.quitGen {
			return
		}
		p.openQuit()
	})
	p.checkQuit()
}

// HasUnsavedOrUnsent reports whether a parked pane still holds unsaved
// text or waits for a send, a discard or a save to answer; checked after
// QuitWait's done fired.
func (p *Panes[P]) HasUnsavedOrUnsent() bool {
	for _, e := range p.parked {
		if e.unsaved || e.pending() {
			return true
		}
	}
	return false
}

func (p *Panes[P]) anyPending() bool {
	for _, e := range p.parked {
		if e.pending() {
			return true
		}
	}
	return false
}

func (p *Panes[P]) checkQuit() {
	if p.quitDone == nil || p.anyPending() {
		return
	}
	p.openQuit()
}

func (p *Panes[P]) openQuit() {
	done := p.quitDone
	if done == nil {
		return
	}
	p.quitDone = nil
	p.quitGen++
	done()
}
