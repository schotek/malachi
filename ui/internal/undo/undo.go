// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package undo is what Undo (Ctrl+Z, ⌘Z on macOS, and the button of the
// toast after a move) takes back: the moves of mail the user made in this
// session — Move to Trash, Archive, Junk, Move To, and the board's Archive
// and Delete — newest first. The daemon has no undo of its own: a moved
// message keeps its id (docs/api.md, message.move), so taking a move back
// is message.move of each message to the folder it came from (Calls).
//
// What cannot be taken back never gets an entry: a message expunged from
// Trash, a cancelled send of the outbox, a move to a folder the daemon does
// not keep (Gmail's All Mail drops the row). The stack lives in memory
// only and ends with the application.
//
// Pure logic without gotk4: the reference of MalachiCore/Undo and
// Malachi.Core/Undo, which port it with its tests.
package undo

import (
	"slices"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Limit is how many moves the stack keeps; a push past it forgets the
// oldest.
const Limit = 20

// Kind is what the move was, for its texts.
type Kind int

// The kinds.
const (
	// Trash is Move to Trash (message.delete of messages not in Trash).
	Trash Kind = iota
	// Archive is Archive (message.move to the archive role).
	Archive
	// Junk is Junk (message.move to the junk role).
	Junk
	// Move is Move To (message.move to a folder the user picked).
	Move
	// BoardArchive is the board's Archive (board.archive moved).
	BoardArchive
	// BoardTrash is the board's Delete (board.trash moved).
	BoardTrash
)

// Board reports whether the move was the board's: taking it back also puts
// the case back on the board (board.setDone done false).
func (k Kind) Board() bool { return k == BoardArchive || k == BoardTrash }

// Moved is one message moved and the folder it came from, with its
// conversation (empty when not known: the board's moves, an older daemon)
// for a list grouped by conversation to select again.
type Moved struct {
	MessageID    api.MessageID
	FromFolderID api.FolderID
	ThreadID     api.ThreadID
}

// Entry is one move the user made, all of one account.
type Entry struct {
	// ID is the entry's number in the stack, given by Push; the toast's
	// button takes back its own entry by it (Take).
	ID      int
	Kind    Kind
	Account api.AccountID
	// Target is the folder the messages went to; empty for the board's
	// moves when the daemon did not say.
	Target api.FolderID
	// TargetName is the folder's name for the text of Move To.
	TargetName string
	// Moves are the messages and where they came from, in their order.
	Moves []Moved
	// Case is the board's case of a board move (Kind.Board).
	Case string
}

// Count is how many messages the entry moved.
func (e Entry) Count() int { return len(e.Moves) }

// Record is the entry of moving list (summaries taken before their rows
// went) to target with kind: the account is the first message's, messages
// of another account, without a folder or already in target are left out
// (they did not move). ok is false when nothing moved.
func Record(kind Kind, target api.FolderID, list []api.MessageSummary) (e Entry, ok bool) {
	if len(list) == 0 {
		return Entry{}, false
	}
	e = Entry{Kind: kind, Account: list[0].AccountID, Target: target}
	for _, s := range list {
		if s.AccountID != e.Account || s.ID == "" || s.FolderID == "" || s.FolderID == target {
			continue
		}
		if slices.ContainsFunc(e.Moves, func(m Moved) bool { return m.MessageID == s.ID }) {
			continue
		}
		e.Moves = append(e.Moves, Moved{MessageID: s.ID, FromFolderID: s.FolderID, ThreadID: s.ThreadID})
	}
	return e, len(e.Moves) > 0
}

// FromBoard is the entry of the board's Archive (trash false) or Delete of
// case id in account, from what the daemon moved (board.archive and
// board.trash moved). ok is false when it moved nothing: the board then
// offers no Undo either.
func FromBoard(trash bool, account api.AccountID, id string, moved []api.BoardMoved) (e Entry, ok bool) {
	e = Entry{Kind: BoardArchive, Account: account, Case: id}
	if trash {
		e.Kind = BoardTrash
	}
	for _, m := range moved {
		if m.MessageID == "" || m.FromFolderID == "" {
			continue
		}
		e.Moves = append(e.Moves, Moved{MessageID: m.MessageID, FromFolderID: m.FromFolderID})
	}
	return e, len(e.Moves) > 0
}

// Calls are the message.move calls that take e back: one per folder the
// messages came from, in the order the folders first appear, the messages
// in their order, each once.
func Calls(e Entry) []api.MessageMoveParams {
	var moves []api.MessageMoveParams
	index := map[api.FolderID]int{}
	for _, m := range e.Moves {
		if m.MessageID == "" || m.FromFolderID == "" {
			continue
		}
		i, ok := index[m.FromFolderID]
		if !ok {
			i = len(moves)
			index[m.FromFolderID] = i
			moves = append(moves, api.MessageMoveParams{AccountID: e.Account, TargetFolderID: m.FromFolderID})
		}
		if !slices.Contains(moves[i].MessageIDs, m.MessageID) {
			moves[i].MessageIDs = append(moves[i].MessageIDs, m.MessageID)
		}
	}
	return moves
}

// Reselect are the moves of e that came back to folder of account, the
// folder the window shows: after Undo the window selects their rows again
// (a message's, or its conversation's). None when the window shows another
// folder.
func Reselect(e Entry, account api.AccountID, folder api.FolderID) []Moved {
	if account != e.Account || folder == "" {
		return nil
	}
	var back []Moved
	for _, m := range e.Moves {
		if m.FromFolderID == folder && !slices.ContainsFunc(back, func(b Moved) bool { return b.MessageID == m.MessageID }) {
			back = append(back, m)
		}
	}
	return back
}

// Stack is the moves of the session, newest last. The zero value is an
// empty stack.
type Stack struct {
	entries []Entry
	next    int
}

// Push adds e as the newest move and returns it with its ID; past Limit
// the oldest is forgotten.
func (s *Stack) Push(e Entry) Entry {
	s.next++
	e.ID = s.next
	s.entries = append(s.entries, e)
	if over := len(s.entries) - Limit; over > 0 {
		s.entries = slices.Delete(s.entries, 0, over)
	}
	return e
}

// Pop takes the newest move off the stack (Ctrl+Z).
func (s *Stack) Pop() (Entry, bool) {
	if len(s.entries) == 0 {
		return Entry{}, false
	}
	e := s.entries[len(s.entries)-1]
	s.entries = s.entries[:len(s.entries)-1]
	return e, true
}

// Peek is the newest move, left on the stack.
func (s *Stack) Peek() (Entry, bool) {
	if len(s.entries) == 0 {
		return Entry{}, false
	}
	return s.entries[len(s.entries)-1], true
}

// Take takes the move with id off the stack wherever it is (the button of
// its toast); ok is false when it is gone (taken back already, forgotten).
func (s *Stack) Take(id int) (Entry, bool) {
	i := slices.IndexFunc(s.entries, func(e Entry) bool { return e.ID == id })
	if i < 0 {
		return Entry{}, false
	}
	e := s.entries[i]
	s.entries = slices.Delete(s.entries, i, i+1)
	return e, true
}

// Len is how many moves the stack holds.
func (s *Stack) Len() int { return len(s.entries) }

// Forget drops the moves of account (removed or paused: its messages are
// gone from the store).
func (s *Stack) Forget(account api.AccountID) {
	s.entries = slices.DeleteFunc(s.entries, func(e Entry) bool { return e.Account == account })
}

// ForgetFolder drops the moves of account into folder (Empty Trash and
// Empty Junk deleted the messages for good).
func (s *Stack) ForgetFolder(account api.AccountID, folder api.FolderID) {
	s.entries = slices.DeleteFunc(s.entries, func(e Entry) bool {
		return e.Account == account && e.Target == folder
	})
}
