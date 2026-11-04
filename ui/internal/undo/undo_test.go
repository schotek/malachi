// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package undo

import (
	"reflect"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

type identity struct{}

func (identity) T(msgid string) string { return msgid }
func (identity) N(msgid, plural string, n int) string {
	if n == 1 {
		return msgid
	}
	return plural
}
func (identity) C(_, msgid string) string { return msgid }

func summary(id, account, folder string) api.MessageSummary {
	return api.MessageSummary{ID: api.MessageID(id), AccountID: api.AccountID(account), FolderID: api.FolderID(folder)}
}

func TestRecord(t *testing.T) {
	m1 := summary("m1", "a", "inbox")
	m1.ThreadID = "t1"
	list := []api.MessageSummary{
		m1,
		summary("m2", "a", "trash"), // already there
		summary("m3", "b", "inbox"), // another account
		summary("m4", "a", "work"),
		summary("m1", "a", "inbox"), // twice
		summary("", "a", "inbox"),
		summary("m5", "a", ""),
	}
	e, ok := Record(Trash, "trash", list)
	if !ok {
		t.Fatal("nothing recorded")
	}
	want := []Moved{{"m1", "inbox", "t1"}, {"m4", "work", ""}}
	if e.Account != "a" || e.Target != "trash" || e.Kind != Trash || !reflect.DeepEqual(e.Moves, want) {
		t.Errorf("entry %+v", e)
	}
	if _, ok := Record(Trash, "trash", []api.MessageSummary{summary("m2", "a", "trash")}); ok {
		t.Error("a message already in the target recorded")
	}
	if _, ok := Record(Archive, "arch", nil); ok {
		t.Error("an empty move recorded")
	}
}

func TestFromBoard(t *testing.T) {
	e, ok := FromBoard(true, "a", "c_1", []api.BoardMoved{
		{MessageID: "m1", FromFolderID: "inbox"}, {MessageID: "", FromFolderID: "inbox"},
	})
	if !ok || e.Kind != BoardTrash || e.Case != "c_1" || len(e.Moves) != 1 || !e.Kind.Board() {
		t.Errorf("entry %+v", e)
	}
	if _, ok := FromBoard(false, "a", "c_1", nil); ok {
		t.Error("a board move without messages recorded")
	}
	if Trash.Board() || Move.Board() {
		t.Error("mail kinds are not the board's")
	}
}

func TestCallsGroupByFolder(t *testing.T) {
	e := Entry{Account: "a", Moves: []Moved{
		{"m1", "inbox", ""}, {"m2", "work", ""}, {"m3", "inbox", ""}, {"m1", "inbox", ""}, {"", "inbox", ""}, {"m4", "", ""},
	}}
	got := Calls(e)
	want := []api.MessageMoveParams{
		{AccountID: "a", TargetFolderID: "inbox", MessageIDs: []api.MessageID{"m1", "m3"}},
		{AccountID: "a", TargetFolderID: "work", MessageIDs: []api.MessageID{"m2"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("calls %+v", got)
	}
	if Calls(Entry{}) != nil {
		t.Error("calls of an empty entry")
	}
}

func TestReselect(t *testing.T) {
	e := Entry{Account: "a", Moves: []Moved{{"m1", "inbox", "t1"}, {"m2", "work", ""}, {"m3", "inbox", ""}, {"m1", "inbox", "t1"}}}
	if got := Reselect(e, "a", "inbox"); !reflect.DeepEqual(got, []Moved{{"m1", "inbox", "t1"}, {"m3", "inbox", ""}}) {
		t.Errorf("inbox: %v", got)
	}
	if got := Reselect(e, "b", "inbox"); got != nil {
		t.Errorf("another account: %v", got)
	}
	if got := Reselect(e, "a", "trash"); got != nil {
		t.Errorf("another folder: %v", got)
	}
	if got := Reselect(e, "a", ""); got != nil {
		t.Errorf("no folder: %v", got)
	}
}

func TestStack(t *testing.T) {
	var s Stack
	if _, ok := s.Pop(); ok {
		t.Fatal("pop of an empty stack")
	}
	a := s.Push(Entry{Account: "a", Target: "trash"})
	b := s.Push(Entry{Account: "a", Target: "arch"})
	c := s.Push(Entry{Account: "b", Target: "trash"})
	if a.ID == b.ID || b.ID == c.ID || s.Len() != 3 {
		t.Fatalf("ids %d %d %d", a.ID, b.ID, c.ID)
	}
	if p, _ := s.Peek(); p.ID != c.ID || s.Len() != 3 {
		t.Error("peek")
	}
	if e, ok := s.Take(a.ID); !ok || e.ID != a.ID || s.Len() != 2 {
		t.Error("take from the bottom")
	}
	if _, ok := s.Take(a.ID); ok {
		t.Error("taken twice")
	}
	if e, _ := s.Pop(); e.ID != c.ID {
		t.Error("pop is not the newest")
	}
	if e, _ := s.Pop(); e.ID != b.ID {
		t.Error("pop order")
	}
}

func TestStackLimit(t *testing.T) {
	var s Stack
	first := s.Push(Entry{})
	for range Limit {
		s.Push(Entry{})
	}
	if s.Len() != Limit {
		t.Fatalf("len %d", s.Len())
	}
	if _, ok := s.Take(first.ID); ok {
		t.Error("the oldest move stayed past the limit")
	}
}

func TestForget(t *testing.T) {
	var s Stack
	s.Push(Entry{Account: "a", Target: "trash"})
	s.Push(Entry{Account: "a", Target: "arch"})
	s.Push(Entry{Account: "b", Target: "trash"})
	s.ForgetFolder("a", "trash")
	if s.Len() != 2 {
		t.Fatalf("forget folder: %d", s.Len())
	}
	s.Forget("b")
	if e, _ := s.Pop(); s.Len() != 0 || e.Target != "arch" {
		t.Errorf("forget account: %+v", e)
	}
}

func TestTexts(t *testing.T) {
	tr := identity{}
	one := Entry{Moves: []Moved{{MessageID: "m1", FromFolderID: "inbox"}}}
	two := Entry{Moves: []Moved{{MessageID: "m1", FromFolderID: "inbox"}, {MessageID: "m2", FromFolderID: "inbox"}}}
	cases := []struct {
		kind Kind
		name string
		e    Entry
		want string
	}{
		{Trash, "", one, "Moved 1 message to Trash."},
		{BoardTrash, "", two, "Moved 2 messages to Trash."},
		{Archive, "", one, "Archived 1 message."},
		{BoardArchive, "", two, "Archived 2 messages."},
		{Junk, "", two, "Moved 2 messages to Junk."},
		{Move, "Work", one, "Moved 1 message to “Work”."},
		{Move, "", two, "Moved 2 messages."},
	}
	for _, c := range cases {
		e := c.e
		e.Kind, e.TargetName = c.kind, c.name
		if got := Done(e, tr); got != c.want {
			t.Errorf("%v: %q", c.kind, got)
		}
	}
	if Button("", tr) != "Undo" || Button("Ctrl+Z", tr) != "Undo (Ctrl+Z)" {
		t.Error(Button("", tr), Button("Ctrl+Z", tr))
	}
	if Undone(two, tr) != "Moved 2 messages back." || Undone(one, tr) != "Moved 1 message back." {
		t.Error(Undone(two, tr))
	}
	if Failed(tr) == "" || Nothing(tr) == "" {
		t.Error("empty texts")
	}
}
