// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"testing"
	"time"

	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// The macOS gating of the board's actions (BoardActions.swift).
func TestBoardGatesFor(t *testing.T) {
	reply := &board.ReplyTarget{Message: "m_1", Folder: "f_inbox"}
	cases := []struct {
		name    string
		d       board.Detail
		samples bool
		want    boardActionGates
	}{
		{"live case with a reply target", board.Detail{Reply: reply, LatestMessage: "m_1"}, false,
			boardActionGates{markDone: true, archive: true, reply: true, showInMail: true, moveTo: true}},
		{"done case the source can still archive", board.Detail{IsDone: true, CanArchive: true, LatestMessage: "m_1"}, false,
			boardActionGates{reopen: true, archive: true, showInMail: true}},
		{"done case nothing left to archive", board.Detail{IsDone: true, LatestMessage: "m_1"}, false,
			boardActionGates{reopen: true, showInMail: true}},
		{"no reply target, a linked draft", board.Detail{DraftID: "d_1", LatestMessage: "m_2"}, false,
			boardActionGates{markDone: true, archive: true, reply: true, showInMail: true, moveTo: true}},
		{"no reply target, no draft: Show in Mail falls back to the newest", board.Detail{LatestMessage: "m_2"}, false,
			boardActionGates{markDone: true, archive: true, showInMail: true, moveTo: true}},
		{"a real case without any message", board.Detail{}, false,
			boardActionGates{markDone: true, archive: true, moveTo: true}},
		{"the samples always offer Show in Mail (it says it cannot)", board.Detail{}, true,
			boardActionGates{markDone: true, archive: true, showInMail: true, moveTo: true}},
		{"star", board.Detail{CanUnstar: true}, false,
			boardActionGates{markDone: true, archive: true, unflag: true, moveTo: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := boardGatesFor(c.d, c.samples); got != c.want {
				t.Errorf("boardGatesFor() = %+v, want %+v", got, c.want)
			}
		})
	}
}

// countingSource counts the conversation loads that reach the source.
type countingSource struct {
	board.DataSource
	loads []board.CaseID
}

func (s *countingSource) LoadMessages(id board.CaseID) {
	s.loads = append(s.loads, id)
	s.DataSource.LoadMessages(id)
}

// A board built in Mail (automatic triage) loads no conversation until it
// shows; then only the last case asked for.
func TestBoardMessagesGate(t *testing.T) {
	inner := &countingSource{DataSource: board.NewInMemorySource(board.EmptySnapshot(), i18n.Tr)}
	shown := false
	g := &boardMessagesGate{DataSource: inner, shown: func() bool { return shown }}
	g.LoadMessages("c_1")
	g.LoadMessages("c_2")
	g.flush()
	if len(inner.loads) != 0 {
		t.Fatalf("loads while in Mail: %v", inner.loads)
	}
	shown = true
	g.flush()
	g.flush()
	if len(inner.loads) != 1 || inner.loads[0] != "c_2" {
		t.Fatalf("loads after showing = %v, want [c_2]", inner.loads)
	}
	g.LoadMessages("c_3")
	if len(inner.loads) != 2 || inner.loads[1] != "c_3" {
		t.Fatalf("loads while shown = %v, want c_3 at once", inner.loads)
	}
}

// The gate keeps Undo working for a source that cannot move messages
// back: the case is only reopened.
func TestBoardMessagesGateUndo(t *testing.T) {
	now := time.Now()
	src := board.NewDummySource(true, now, time.Local, i18n.Tr)
	var id board.CaseID
	for _, c := range src.Snapshot().Cases {
		if !c.Done() {
			id = c.ID
			break
		}
	}
	if id == "" {
		t.Skip("the samples have no live case")
	}
	g := &boardMessagesGate{DataSource: src, shown: func() bool { return true }}
	g.SetDone(id, true)
	g.UndoArchive(board.ArchiveOutcome{Case: id})
	if c, _ := src.Snapshot().Case(id); c.Done() {
		t.Fatal("Undo through the gate left the case done")
	}
}
