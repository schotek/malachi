// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"reflect"
	"slices"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Where the board's cases come from. The board reads a snapshot and is
// told when it changes; what the user decides about a case (its state,
// done, a remind, archive, a discarded draft, a promise ticked off) is case
// data and goes back to the source. The daemon's source (DaemonSource)
// starts empty and publishes when its data arrives; the in-memory one
// (InMemorySource) holds invented sample cases or none, for the dummy board
// and the tests.
//
// The macOS client leads (MalachiCore/Board/BoardSource.swift); this is
// its port. The name Source is taken by who decided a case's state
// (text.go), so the seam is DataSource.

// Handlers are the source's one observer, the board's controller.
type Handlers struct {
	// Change is called after the snapshot changed.
	Change func()
	// Error is called with a short sentence for a toast when a write or a
	// load failed (a write is undone by then).
	Error func(string)
	// Notice is called with a short sentence for a toast about what a write
	// did (Archive).
	Notice func(string)
}

// DataSource is the board's cases and the writes the user's decisions make
// (BoardSource). Writes are fire-and-forget: the source calls
// Handlers.Change once its snapshot holds them (at once for both sources:
// the daemon's writes optimistically, and undoes a write the daemon
// refused, with Handlers.Error). Every method runs on the main loop.
type DataSource interface {
	// Snapshot is what the source knows now. The caller does not change
	// it.
	Snapshot() Snapshot
	// SetHandlers installs the observer (NewController does).
	SetHandlers(h Handlers)
	// SetState moves the case to state; nil = back to automatic (the
	// assistant's or the rules' state).
	SetState(id CaseID, state *State)
	// SetDone: done takes the case off the board (and ends a remind); not
	// done puts it back.
	SetDone(id CaseID, done bool)
	// Remind hides the case until *until (in the future, within a year);
	// nil puts a snoozed case back on the board. Ends done.
	Remind(id CaseID, until *time.Time)
	// Archive moves the case's inbox messages to the archive (where the
	// account can) and marks it done; Handlers.Notice says what it did.
	Archive(id CaseID)
	// SetCommitmentDone ticks a promise off (or reopens it).
	SetCommitmentDone(id api.BoardCommitmentID, done bool)
	// DiscardDraft drops the suggested reply (the draft itself, too).
	DiscardDraft(id CaseID)
	// DiscardStoredDraft is Discard of the inline reply editor: it deletes
	// draft (of account) of case id and calls done, on the main loop and
	// possibly before it returns, when that is done. While the case links it, the link goes with it
	// (board.discardDraft); a draft the case no longer links is deleted
	// alone. done gets the error when the daemon refused (the editor says
	// so and keeps its text; no Handlers.Error); a source without drafts
	// (the samples) just drops the link.
	DiscardStoredDraft(id CaseID, draft api.DraftID, account api.AccountID, done func(error))
	// Unflag removes the star from the messages that keep the case hot
	// (Detail.CanUnstar); the rules then decide where the case goes.
	Unflag(id CaseID)
	// LoadMessages loads the case's conversation into Case.Messages unless
	// it is there for the case's current version already.
	LoadMessages(id CaseID)
	// Refresh asks for the data anew.
	Refresh()
}

// InMemorySource is a source over a snapshot held in memory: the dummy
// board, and the tests (InMemoryBoardSource). Writes are emulated locally
// (Archive marks the case done and says it moved its messages when the
// case can archive). A write to an unknown case, or one that changes
// nothing, is ignored and calls no one.
type InMemorySource struct {
	snapshot Snapshot
	h        Handlers
	tr       Translator
}

// NewInMemorySource holds s; tr writes its notices.
func NewInMemorySource(s Snapshot, tr Translator) *InMemorySource {
	return &InMemorySource{snapshot: s, tr: tr}
}

// NewDummySource is the dummy board (MALACHI_BOARD_SAMPLES): the invented
// sample cases (SampleSnapshot), or none.
func NewDummySource(samples bool, now time.Time, loc *time.Location, tr Translator) *InMemorySource {
	if samples {
		return NewInMemorySource(SampleSnapshot(now, loc), tr)
	}
	return NewInMemorySource(EmptySnapshot(), tr)
}

// Snapshot implements DataSource.
func (m *InMemorySource) Snapshot() Snapshot { return m.snapshot }

// SetHandlers implements DataSource.
func (m *InMemorySource) SetHandlers(h Handlers) { m.h = h }

// Handlers are the handlers installed (the tests call them).
func (m *InMemorySource) Handlers() Handlers { return m.h }

// Replace replaces the whole snapshot, as a source does when its data
// arrives; the same snapshot again calls no one.
func (m *InMemorySource) Replace(s Snapshot) {
	if s.Equal(m.snapshot) {
		return
	}
	m.snapshot = s
	m.changed()
}

// SetState implements DataSource.
func (m *InMemorySource) SetState(id CaseID, state *State) {
	m.update(id, func(c *Case) {
		if state == nil {
			c.UserState = nil
		} else {
			c.UserState = statePtr(*state)
		}
	})
}

// SetDone implements DataSource.
func (m *InMemorySource) SetDone(id CaseID, done bool) {
	m.update(id, func(c *Case) { c.SetDone(done) })
}

// Remind implements DataSource.
func (m *InMemorySource) Remind(id CaseID, until *time.Time) {
	m.update(id, func(c *Case) { remindCase(c, until) })
}

// remindCase lays a remind over c: snoozed until *until, or back on the
// board from a remind (done stays done).
func remindCase(c *Case, until *time.Time) {
	if until != nil {
		c.Visibility = Visibility{Kind: VisibleSnoozed, At: *until}
	} else if _, ok := c.Visibility.RemindAt(); ok {
		c.Visibility = Visibility{}
	}
}

// Archive implements DataSource.
func (m *InMemorySource) Archive(id CaseID) {
	c, ok := m.snapshot.Case(id)
	if !ok {
		return
	}
	moved := 0
	if c.CanArchive {
		moved = max(1, c.MessageCount)
	}
	m.update(id, func(c *Case) {
		c.Visibility = Visibility{Kind: VisibleDone}
		c.CanArchive = false
	})
	if m.h.Notice != nil {
		m.h.Notice(Archived(moved, !c.CanArchive, m.tr))
	}
}

// SetCommitmentDone implements DataSource.
func (m *InMemorySource) SetCommitmentDone(id api.BoardCommitmentID, done bool) {
	i := slices.IndexFunc(m.snapshot.Commitments, func(k Commitment) bool { return k.ID == id })
	if i < 0 {
		return
	}
	state := CommitmentOpen
	if done {
		state = CommitmentDone
	}
	if m.snapshot.Commitments[i].State == state {
		return
	}
	ks := slices.Clone(m.snapshot.Commitments)
	ks[i].State = state
	m.snapshot.Commitments = ks
	m.changed()
}

// DiscardDraft implements DataSource.
func (m *InMemorySource) DiscardDraft(id CaseID) {
	m.update(id, func(c *Case) { c.Draft = nil })
}

// DiscardStoredDraft implements DataSource: no drafts behind the cases, the
// link goes and nothing can fail.
func (m *InMemorySource) DiscardStoredDraft(id CaseID, _ api.DraftID, _ api.AccountID, done func(error)) {
	m.DiscardDraft(id)
	if done != nil {
		done(nil)
	}
}

// Unflag implements DataSource: the samples have no message to unflag, a
// placeholder toast.
func (m *InMemorySource) Unflag(id CaseID) {
	if _, ok := m.snapshot.Case(id); !ok {
		return
	}
	if m.h.Notice != nil {
		m.h.Notice(Later(m.tr))
	}
}

// LoadMessages implements DataSource: the samples carry their messages; a
// case without any gets none.
func (m *InMemorySource) LoadMessages(id CaseID) {
	m.update(id, func(c *Case) {
		if !c.MessagesLoaded {
			c.Messages = nil
			c.MessagesLoaded = true
		}
	})
}

// Refresh implements DataSource; there is nothing to ask.
func (m *InMemorySource) Refresh() {}

// update changes case id with change, on a copy of the cases (snapshots
// handed out stay as they were), and reports it when it changed.
func (m *InMemorySource) update(id CaseID, change func(*Case)) {
	i := slices.IndexFunc(m.snapshot.Cases, func(c Case) bool { return c.ID == id })
	if i < 0 {
		return
	}
	c := m.snapshot.Cases[i]
	change(&c)
	if reflect.DeepEqual(c, m.snapshot.Cases[i]) {
		return
	}
	cases := slices.Clone(m.snapshot.Cases)
	cases[i] = c
	m.snapshot.Cases = cases
	m.changed()
}

func (m *InMemorySource) changed() {
	if m.h.Change != nil {
		m.h.Change()
	}
}
