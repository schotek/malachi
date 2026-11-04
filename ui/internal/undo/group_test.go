// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package undo

import "testing"

func TestTakeGroupTakesTheRestOfAnAction(t *testing.T) {
	var s Stack
	a := s.Push(Entry{Kind: Trash, Account: "a", Group: 7})
	other := s.Push(Entry{Kind: Trash, Account: "c"})
	b := s.Push(Entry{Kind: Trash, Account: "b", Group: 7})
	e, ok := s.Take(b.ID)
	if !ok {
		t.Fatal("take")
	}
	rest := s.TakeGroup(e.Group)
	if len(rest) != 1 || rest[0].ID != a.ID {
		t.Fatalf("rest %+v", rest)
	}
	if s.Len() != 1 {
		t.Fatalf("len %d", s.Len())
	}
	if p, _ := s.Peek(); p.ID != other.ID {
		t.Error("the stranger entry went")
	}
}

func TestTakeGroupZeroIsNothing(t *testing.T) {
	var s Stack
	s.Push(Entry{Kind: Trash, Account: "a"})
	s.Push(Entry{Kind: Trash, Account: "b"})
	if got := s.TakeGroup(0); len(got) != 0 || s.Len() != 2 {
		t.Errorf("group 0 took %v", got)
	}
}
