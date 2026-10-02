// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import "github.com/diamondburned/gotk4/pkg/glib/v2"

// watchBoardAutoStart follows preferences and Claude availability arriving
// after window construction. Automatic triage needs the board's queue even
// when the user has not opened Board yet. CloseBoardReplies removes the
// observer and cancels a queued start when this window closes.
func (w *Window) watchBoardAutoStart() {
	bt := w.boardTriageOrNil()
	if bt == nil {
		return
	}
	closed := false
	start := &boardAutoStart{
		wanted: func() bool {
			return !closed && w.boardPage == nil && bt.Controller().WantsBoardData()
		},
		post:  func(f func()) { glib.IdleAdd(f) },
		start: w.ensureBoard,
	}
	remove := bt.Controller().Observe(start.changed)
	w.boardAutoStartRemove = func() {
		closed = true
		remove()
	}
	start.changed()
}

// Defer construction until the current notification finishes: wiring the
// source can publish another triage change before ensureBoard assigns the
// completed page. Coalesce such notifications and check availability again
// when the queued callback runs.
type boardAutoStart struct {
	wanted func() bool
	post   func(func())
	start  func()
	queued bool
}

func (s *boardAutoStart) changed() {
	if s.queued || !s.wanted() {
		return
	}
	s.queued = true
	s.post(func() {
		if s.wanted() {
			s.start()
		}
		s.queued = false
	})
}
