// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package toolbaredit

import (
	"reflect"
	"strings"
	"testing"

	"github.com/schotek/malachi/ui/internal/toolbar"
)

func ids(s string) toolbar.Layout {
	var out toolbar.Layout
	for _, f := range strings.Fields(s) {
		out = append(out, toolbar.ID(f))
	}
	return out
}

func layout(e *Editor) string {
	return strings.Join(e.Layout().Tokens(), " ")
}

func index(e *Editor, id toolbar.ID) int {
	for i, x := range e.Layout() {
		if x == id {
			return i
		}
	}
	return -1
}

var all = toolbar.Features{Have: map[toolbar.Feature]bool{
	toolbar.FeatureCRM: true, toolbar.FeatureAssistant: true, toolbar.FeatureTriage: true,
}}

func TestOpensOnTheClientsDefault(t *testing.T) {
	e := New(toolbar.GTK, toolbar.BarMail, nil, all)
	if !e.Layout().Equal(toolbar.DefaultFor(toolbar.GTK, toolbar.BarMail)) || !e.IsDefault() {
		t.Fatalf("layout = %s", layout(e))
	}
	if got := e.Tokens(); len(got) != 0 {
		t.Errorf("untouched tokens = %v", got)
	}
	secs := e.Sections()
	var names []toolbar.Section
	for _, s := range secs {
		names = append(names, s.Section)
	}
	if want := toolbar.Sections(toolbar.BarMail); !reflect.DeepEqual(names, want) {
		t.Errorf("sections = %v", names)
	}
	first := secs[0].Rows[0]
	if first.ID != toolbar.Mode || !first.Fixed || first.CanMoveUp || first.CanMoveDown {
		t.Errorf("mode row = %+v", first)
	}
	for _, s := range secs {
		for _, r := range s.Rows {
			if toolbar.IsSeparator(r.ID) {
				t.Errorf("separator %s is a row", r.ID)
			}
			if got := toolbar.SectionAt(toolbar.BarMail, e.Layout(), r.Index); got != s.Section {
				t.Errorf("%s in section %s, row of %s", r.ID, got, s.Section)
			}
		}
	}
}

func TestUnavailableRows(t *testing.T) {
	e := New(toolbar.Windows, toolbar.BarMail, nil, toolbar.Features{})
	for _, s := range e.Sections() {
		for _, r := range s.Rows {
			want := r.ID == toolbar.CRM || r.ID == toolbar.Assistant
			if r.Unavailable != want {
				t.Errorf("%s unavailable = %v", r.ID, r.Unavailable)
			}
		}
	}
	for _, id := range e.Available(toolbar.SectionMessage) {
		if id == toolbar.Print {
			t.Errorf("print offered without printing")
		}
	}
}

func TestMoveWithinAndAcrossSections(t *testing.T) {
	e := New(toolbar.Variant{}, toolbar.BarMail, ids("mode |list refresh newMessage |message reply forward").Tokens(), all)
	i := index(e, toolbar.NewMessage)
	j, ok := e.MoveUp(i)
	if !ok || layout(e) != "mode |list newMessage refresh |message reply forward" || j != i-1 {
		t.Fatalf("MoveUp = %d %v: %s", j, ok, layout(e))
	}
	// Past the heading into the sidebar section, right after the mode switch.
	j, ok = e.MoveUp(j)
	if !ok || layout(e) != "mode newMessage |list refresh |message reply forward" {
		t.Fatalf("MoveUp across = %v: %s", ok, layout(e))
	}
	// Not above the fixed mode switch.
	if _, ok := e.MoveUp(j); ok {
		t.Errorf("moved above the mode switch: %s", layout(e))
	}
	// A list tool never into the message section.
	i = index(e, toolbar.Refresh)
	if _, ok := e.MoveDown(i); ok {
		t.Errorf("refresh moved into the message section: %s", layout(e))
	}
	// A message action never up into the list section.
	i = index(e, toolbar.Reply)
	if _, ok := e.MoveUp(i); ok {
		t.Errorf("reply moved into the list section: %s", layout(e))
	}
	j, ok = e.MoveDown(i)
	if !ok || layout(e) != "mode newMessage |list refresh |message forward reply" {
		t.Errorf("MoveDown = %v: %s", ok, layout(e))
	}
	if _, ok := e.MoveDown(j); ok {
		t.Errorf("moved past the end")
	}
	// Separators and fixed items do not move; bad indexes are refused.
	for _, i := range []int{0, index(e, toolbar.Separator(toolbar.SectionList)), -1, 99} {
		if _, ok := e.MoveUp(i); ok {
			t.Errorf("MoveUp(%d) moved", i)
		}
		if _, ok := e.MoveDown(i); ok {
			t.Errorf("MoveDown(%d) moved", i)
		}
	}
}

func TestSpacesGoAnywhere(t *testing.T) {
	e := New(toolbar.Variant{}, toolbar.BarMail, ids("mode |list refresh |message reply").Tokens(), all)
	if !e.AddSpace(toolbar.FlexibleSpace, index(e, toolbar.Refresh)) {
		t.Fatal("AddSpace refused")
	}
	if layout(e) != "mode |list refresh flexibleSpace |message reply" {
		t.Fatalf("after AddSpace: %s", layout(e))
	}
	// Down across the heading into the message section and back.
	i := index(e, toolbar.FlexibleSpace)
	i, ok := e.MoveDown(i)
	if !ok || layout(e) != "mode |list refresh |message flexibleSpace reply" {
		t.Fatalf("space MoveDown = %v: %s", ok, layout(e))
	}
	if _, ok := e.MoveUp(i); !ok {
		t.Errorf("space MoveUp refused")
	}
	// At the start of the first section: after the mode switch.
	if !e.AddSpace(toolbar.Space, -1) || layout(e) != "mode space |list refresh flexibleSpace |message reply" {
		t.Errorf("AddSpace(-1): %s", layout(e))
	}
	// After a heading.
	if !e.AddSpace(toolbar.Space, index(e, toolbar.Separator(toolbar.SectionMessage))) ||
		layout(e) != "mode space |list refresh flexibleSpace |message space reply" {
		t.Errorf("AddSpace(heading): %s", layout(e))
	}
	if e.AddSpace(toolbar.Reply, 0) || e.AddSpace(toolbar.Space, 99) || e.AddSpace(toolbar.Space, -2) {
		t.Errorf("AddSpace took a bad argument")
	}
	// Remove a space and an item, never the mode switch or a heading.
	if !e.Remove(index(e, toolbar.Space)) || !e.Remove(index(e, toolbar.Reply)) {
		t.Errorf("Remove refused")
	}
	if e.Remove(0) || e.Remove(index(e, toolbar.Separator(toolbar.SectionList))) || e.Remove(-1) || e.Remove(99) {
		t.Errorf("Remove took a fixed item, a heading or a bad index")
	}
	if layout(e) != "mode |list refresh flexibleSpace |message space" {
		t.Errorf("after Remove: %s", layout(e))
	}
}

func TestAddAndAvailable(t *testing.T) {
	e := New(toolbar.GTK, toolbar.BarMail, ids("mode |list refresh |message reply").Tokens(), toolbar.Features{})
	list := e.Available(toolbar.SectionList)
	want := []toolbar.ID{toolbar.NewMessage, toolbar.Contacts, toolbar.Search}
	if !reflect.DeepEqual(list, want) {
		t.Errorf("Available(list) = %v, want %v", list, want)
	}
	msg := e.Available(toolbar.SectionMessage)
	for _, id := range msg {
		switch id {
		case toolbar.Reply, toolbar.CRM, toolbar.Print, toolbar.Assistant, toolbar.Filter:
			t.Errorf("Available(message) offers %s", id)
		}
	}
	if !e.Add(toolbar.Junk, toolbar.SectionMessage) || layout(e) != "mode |list refresh |message reply junk" {
		t.Errorf("Add(junk): %s", layout(e))
	}
	if e.Add(toolbar.Junk, toolbar.SectionMessage) || e.Add(toolbar.Archive, toolbar.SectionList) ||
		e.Add(toolbar.Filter, toolbar.SectionList) || e.Add(toolbar.Mode, toolbar.SectionSidebar) || e.Add(toolbar.Reply, toolbar.SectionDetail) {
		t.Errorf("Add took a duplicate, a wrong section, an omitted or a fixed item: %s", layout(e))
	}
	if !e.Add(toolbar.Search, toolbar.SectionList) || layout(e) != "mode |list refresh search |message reply junk" {
		t.Errorf("Add(search, list) on GTK: %s", layout(e))
	}
	if !e.Add(toolbar.FlexibleSpace, toolbar.SectionSidebar) || layout(e) != "mode flexibleSpace |list refresh search |message reply junk" {
		t.Errorf("Add(flexibleSpace): %s", layout(e))
	}
}

func TestDrop(t *testing.T) {
	e := New(toolbar.Variant{}, toolbar.BarMail, ids("mode |list refresh contacts |message reply forward archive").Tokens(), all)
	// Archive to before reply: without archive the layout is
	// mode |list refresh contacts |message reply forward; reply is at 5.
	from := index(e, toolbar.Archive)
	if !e.CanDrop(from, 5) {
		t.Fatal("CanDrop refused")
	}
	if i, ok := e.Drop(from, 5); !ok || i != 5 || layout(e) != "mode |list refresh contacts |message archive reply forward" {
		t.Fatalf("Drop = %d %v: %s", i, ok, layout(e))
	}
	// Archive into the list section is refused and nothing changes.
	before := layout(e)
	if i, ok := e.Drop(5, 2); ok || i != 5 || layout(e) != before {
		t.Errorf("Drop into the list section = %d %v: %s", i, ok, layout(e))
	}
	// Before the mode switch is refused; after it is fine.
	from = index(e, toolbar.Contacts)
	if e.CanDrop(from, 0) {
		t.Errorf("CanDrop before the mode switch")
	}
	if _, ok := e.Drop(from, 1); !ok || layout(e) != "mode contacts |list refresh |message archive reply forward" {
		t.Errorf("Drop after the mode switch: %s", layout(e))
	}
	// Out of range, headings and the mode switch never drop.
	for _, c := range [][2]int{{-1, 0}, {99, 0}, {3, 99}, {3, -1}, {0, 2}, {2, 4}} {
		if e.CanDrop(c[0], c[1]) {
			t.Errorf("CanDrop(%d, %d)", c[0], c[1])
		}
	}
}

func TestTokensKeepOtherClientsItems(t *testing.T) {
	stored := ids("mode |list toggleSidebar filter refresh |message reply search").Tokens()
	e := New(toolbar.GTK, toolbar.BarMail, stored, all)
	if layout(e) != "mode |list refresh |message reply search" {
		t.Fatalf("GTK reads %s", layout(e))
	}
	e.Add(toolbar.Archive, toolbar.SectionMessage)
	got := e.Tokens()
	want := ids("mode |list toggleSidebar filter refresh |message reply search archive").Tokens()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tokens = %v", got)
	}
	// The client writes them and the key comes back: nothing changes.
	e.Reload(got)
	if layout(e) != "mode |list refresh |message reply search archive" || !reflect.DeepEqual(e.Tokens(), got) {
		t.Errorf("after Reload: %s, %v", layout(e), e.Tokens())
	}
	// A change from elsewhere replaces the arrangement.
	e.Reload(ids("mode |list |message star").Tokens())
	if layout(e) != "mode |list |message star" {
		t.Errorf("after a change elsewhere: %s", layout(e))
	}
}

func TestReset(t *testing.T) {
	e := New(toolbar.Windows, toolbar.BarCalendar, ids("mode |main calendarToday print").Tokens(), all)
	if e.IsDefault() {
		t.Fatal("a stored arrangement is the default")
	}
	if got := e.Reset(); got == nil || len(got) != 0 {
		t.Errorf("Reset = %#v, want []", got)
	}
	if !e.IsDefault() || !e.Layout().Equal(toolbar.DefaultFor(toolbar.Windows, toolbar.BarCalendar)) {
		t.Errorf("after Reset: %s", layout(e))
	}
	if got := e.Tokens(); len(got) != 0 {
		t.Errorf("Tokens after Reset = %v", got)
	}
}

func TestEveryBarOfEveryClient(t *testing.T) {
	for _, v := range []toolbar.Variant{{}, toolbar.GTK, toolbar.Windows} {
		for _, bar := range toolbar.Bars {
			e := New(v, bar, nil, all)
			// Walk every row down and up as far as it goes: the
			// arrangement stays the client's valid one.
			for i := range e.Layout() {
				j := i
				for k := 0; k < 64; k++ {
					var ok bool
					if j, ok = e.MoveDown(j); !ok {
						break
					}
				}
				for k := 0; k < 64; k++ {
					var ok bool
					if j, ok = e.MoveUp(j); !ok {
						break
					}
				}
				l := e.Layout()
				if !toolbar.NormalizeFor(v, bar, l.Tokens()).Equal(l) {
					t.Fatalf("%s: not normal after moves: %v", bar, l)
				}
			}
			if got := len(e.Layout()); got != len(toolbar.DefaultFor(v, bar)) {
				t.Errorf("%s: moves changed the length to %d", bar, got)
			}
			for _, s := range toolbar.Sections(bar) {
				for _, id := range e.Available(s) {
					if !e.Add(id, s) {
						t.Errorf("%s: Available(%s) offers %s, Add refuses", bar, s, id)
					}
				}
			}
		}
	}
}

func TestBounded(t *testing.T) {
	e := New(toolbar.Variant{}, toolbar.BarBoardFlow, nil, all)
	n := 0
	for e.AddSpace(toolbar.Space, 0) {
		n++
		if n > 1000 {
			t.Fatal("AddSpace is unbounded")
		}
	}
	if len(e.Layout()) > MaxTokens {
		t.Errorf("layout grew to %d", len(e.Layout()))
	}
	if back := toolbar.NormalizeFor(toolbar.Variant{}, toolbar.BarBoardFlow, e.Tokens()); !back.Equal(e.Layout()) {
		t.Errorf("a full toolbar does not read back whole")
	}
}

func TestDropAtEnd(t *testing.T) {
	e := New(toolbar.GTK, toolbar.BarMail, nil, all)
	// Refresh to the end of the message section, past More Actions.
	from := index(e, toolbar.Refresh)
	if to := e.DropAtEnd(from, toolbar.SectionMessage); e.CanDrop(from, to) {
		t.Fatalf("Refresh may not stand in the message section (to %d)", to)
	}
	// Reply to the end of its own section: after More Actions.
	from = index(e, toolbar.Reply)
	to := e.DropAtEnd(from, toolbar.SectionMessage)
	if _, ok := e.Drop(from, to); !ok {
		t.Fatalf("Reply could not go to the end of its section (to %d)", to)
	}
	if l := e.Layout(); l[len(l)-1] != toolbar.Reply {
		t.Fatalf("Reply is not last: %s", layout(e))
	}
	// Search to the end of the list section (GTK's extra section).
	from = index(e, toolbar.Search)
	to = e.DropAtEnd(from, toolbar.SectionList)
	if _, ok := e.Drop(from, to); !ok || toolbar.SectionAt(toolbar.BarMail, e.Layout(), index(e, toolbar.Search)) != toolbar.SectionList {
		t.Fatalf("Search did not stay at the end of the list section: %s", layout(e))
	}
	// An empty section of a bar (the Calendar's sidebar holds the fixed mode only).
	c := New(toolbar.GTK, toolbar.BarCalendar, nil, all)
	from = index(c, toolbar.CalendarToday)
	to = c.DropAtEnd(from, toolbar.SectionSidebar)
	if _, ok := c.Drop(from, to); !ok || toolbar.SectionAt(toolbar.BarCalendar, c.Layout(), index(c, toolbar.CalendarToday)) != toolbar.SectionSidebar {
		t.Fatalf("Today did not go to the sidebar section: %s", strings.Join(c.Layout().Tokens(), " "))
	}
	if got := c.DropAtEnd(0, toolbar.SectionDetail); got != -1 {
		t.Fatalf("a section the bar lacks gave %d", got)
	}
}
