// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package toolbar

import (
	"reflect"
	"strings"
	"testing"
)

func ids(s string) Layout {
	var out Layout
	for _, f := range strings.Fields(s) {
		out = append(out, ID(f))
	}
	return out
}

func TestDefaultsAreNormal(t *testing.T) {
	for _, bar := range Bars {
		d := Default(bar)
		if got := Normalize(bar, d.Tokens()); !got.Equal(d) {
			t.Errorf("%s: Normalize(Default) = %v, want %v", bar, got, d)
		}
		for _, id := range d {
			if IsSpace(id) || IsSeparator(id) {
				continue
			}
			if _, ok := Lookup(bar, id); !ok {
				t.Errorf("%s: default item %s is not in the catalog", bar, id)
			}
		}
	}
}

func TestDefaultsAreTheToolbarsBeforeCustomization(t *testing.T) {
	// MainToolbar.defaultItems with AppKit's sidebar separator as |list.
	want := ids("mode flexibleSpace |list toggleSidebar newMessage contacts filter refresh flexibleSpace " +
		"|message reply replyAll forward flexibleSpace archive trash space crm space star assistant moreActions search")
	if got := Default(BarMail); !got.Equal(want) {
		t.Errorf("mail default = %v", got)
	}
	want = ids("mode boardStyle contacts flexibleSpace boardAccount boardTriage")
	if got := Default(BarBoardFlow); !got.Equal(want) {
		t.Errorf("board-flow default = %v", got)
	}
}

func TestNewItemsAreOnlyInThePalette(t *testing.T) {
	d := Default(BarMail)
	for _, id := range []ID{MarkRead, Junk, MoveTo, Print, Undo, LoadPictures} {
		for _, x := range d {
			if x == id {
				t.Errorf("%s is in the default toolbar", id)
			}
		}
		if _, ok := Lookup(BarMail, id); !ok {
			t.Errorf("%s is not in the catalog", id)
		}
	}
}

func TestEmptyIsTheDefault(t *testing.T) {
	if got := Normalize(BarMail, nil); !got.Equal(Default(BarMail)) {
		t.Errorf("Normalize(nil) = %v", got)
	}
	if got := Stored(BarMail, Default(BarMail)); len(got) != 0 {
		t.Errorf("Stored(default) = %v, want empty", got)
	}
}

func TestNormalize(t *testing.T) {
	cases := []struct {
		name   string
		bar    Bar
		stored string
		want   string
	}{
		{
			"unknown and duplicate items go",
			BarMail,
			"mode |list refresh bogus refresh |message reply reply junk",
			"mode |list refresh |message reply junk",
		},
		{
			"a missing fixed item and missing separators come back",
			BarMail,
			"reply",
			"mode |list |message reply",
		},
		{
			"a message action in the list section goes to its home",
			BarMail,
			"mode |list archive refresh |message reply",
			"mode |list refresh |message reply archive",
		},
		{
			"a list tool may stand in the sidebar section",
			BarMail,
			"mode refresh |list |message",
			"mode refresh |list |message",
		},
		{
			"a fixed item moved elsewhere goes back to the start",
			BarMail,
			"flexibleSpace |list newMessage mode |message",
			"mode flexibleSpace |list newMessage |message",
		},
		{
			"separators out of order and twice are ignored",
			BarMail,
			"mode |message reply |list refresh |message forward",
			// refresh stands in the message section and goes home to the list.
			"mode |list refresh |message reply forward",
		},
		{
			"spaces stand any number of times",
			BarMail,
			"mode |list space space |message flexibleSpace flexibleSpace",
			"mode |list space space |message flexibleSpace flexibleSpace",
		},
		{
			"a board detail action goes to the detail section",
			BarBoardList,
			"mode |list boardDone |detail boardReply",
			"mode |list |detail boardReply boardDone",
		},
		{
			"a section's separator of another toolbar is unknown",
			BarBoardFlow,
			"|list mode contacts |detail boardStyle",
			"mode contacts boardStyle",
		},
		{
			"print in the calendar",
			BarCalendar,
			"mode |main calendarToday print",
			"mode |main calendarToday print",
		},
		{
			"a mail item in the calendar is unknown",
			BarCalendar,
			"mode |main reply calendarToday",
			"mode |main calendarToday",
		},
	}
	for _, c := range cases {
		got := Normalize(c.bar, ids(c.stored).Tokens())
		if want := ids(c.want); !got.Equal(want) {
			t.Errorf("%s: Normalize(%q) = %v, want %v", c.name, c.stored, got, want)
		}
		if again := Normalize(c.bar, got.Tokens()); !again.Equal(got) {
			t.Errorf("%s: not idempotent: %v", c.name, again)
		}
	}
}

func TestNormalizeIsBounded(t *testing.T) {
	stored := make([]string, 10*MaxTokens)
	for i := range stored {
		stored[i] = "space"
	}
	if got := Normalize(BarMail, stored); len(got) > MaxTokens+len(Sections(BarMail)) {
		t.Errorf("Normalize kept %d tokens", len(got))
	}
}

func TestCanInsert(t *testing.T) {
	cases := []struct {
		bar  Bar
		id   ID
		s    Section
		want bool
	}{
		{BarMail, Reply, SectionMessage, true},
		{BarMail, Reply, SectionList, false},
		{BarMail, Refresh, SectionSidebar, true},
		{BarMail, Refresh, SectionMessage, false},
		{BarMail, Space, SectionSidebar, true},
		{BarMail, FlexibleSpace, SectionList, true},
		{BarMail, Mode, SectionSidebar, false},
		{BarMail, Separator(SectionList), SectionList, false},
		{BarMail, BoardDone, SectionMessage, false},
		{BarBoardList, BoardDone, SectionDetail, true},
		{BarBoardList, BoardDone, SectionList, false},
		{BarCalendar, Print, SectionMain, true},
	}
	for _, c := range cases {
		if got := CanInsert(c.bar, c.id, c.s); got != c.want {
			t.Errorf("CanInsert(%s, %s, %s) = %v, want %v", c.bar, c.id, c.s, got, c.want)
		}
	}
}

func TestImmovable(t *testing.T) {
	want := []ID{Mode, Separator(SectionList), Separator(SectionMessage)}
	if got := Immovable(BarMail); !reflect.DeepEqual(got, want) {
		t.Errorf("Immovable(mail) = %v", got)
	}
	if got := Immovable(BarBoardFlow); !reflect.DeepEqual(got, []ID{Mode}) {
		t.Errorf("Immovable(board-flow) = %v", got)
	}
}

func TestSectionAt(t *testing.T) {
	l := Default(BarMail)
	want := map[ID]Section{Mode: SectionSidebar, Separator(SectionList): SectionList, Refresh: SectionList, Reply: SectionMessage, Search: SectionMessage}
	for i, id := range l {
		if s, ok := want[id]; ok && SectionAt(BarMail, l, i) != s {
			t.Errorf("SectionAt(%s) = %s, want %s", id, SectionAt(BarMail, l, i), s)
		}
	}
	if got := SectionAt(BarBoardFlow, Default(BarBoardFlow), 3); got != SectionMain {
		t.Errorf("board-flow section = %s", got)
	}
}

func TestAllowedLeavesOutMissingFeatures(t *testing.T) {
	got := Allowed(BarMail, Features{Have: map[Feature]bool{FeatureAssistant: true}})
	has := map[ID]bool{}
	for _, id := range got {
		has[id] = true
	}
	if has[CRM] || has[Print] {
		t.Errorf("Allowed offers crm or print without the feature: %v", got)
	}
	if !has[Assistant] || !has[Reply] || !has[Mode] {
		t.Errorf("Allowed lacks assistant, reply or mode: %v", got)
	}
}

func TestPresentHidesAndKeepsPlaces(t *testing.T) {
	l := Default(BarMail)
	got := Present(BarMail, l, Features{Have: map[Feature]bool{FeatureAssistant: true}})
	if len(got) != len(l) {
		t.Fatalf("Present has %d entries, the layout %d", len(got), len(l))
	}
	for i, e := range got {
		if e.ID != l[i] {
			t.Fatalf("entry %d = %s, want %s", i, e.ID, l[i])
		}
		if want := e.ID == CRM; e.Hidden != want {
			t.Errorf("%s hidden = %v, want %v", e.ID, e.Hidden, want)
		}
	}
}

func TestPresentLeavesOutSeparatorsAndSections(t *testing.T) {
	// The list pane collapsed: its separator goes, its items stay.
	got := Present(BarMail, ids("mode |list refresh |message reply"), Features{
		HiddenSeparators: map[ID]bool{Separator(SectionList): true},
	})
	want := []Shown{{ID: Mode}, {ID: Refresh}, {ID: Separator(SectionMessage)}, {ID: Reply}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("collapsed list = %v", got)
	}
	// The Board List without a case: the detail section goes whole.
	got = Present(BarBoardList, ids("mode |list boardStyle |detail boardDone boardReply"), Features{
		HiddenSections: map[Section]bool{SectionDetail: true},
	})
	want = []Shown{{ID: Mode}, {ID: Separator(SectionList)}, {ID: BoardStyle}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("hidden detail = %v", got)
	}
}

func TestMissingActions(t *testing.T) {
	l := ids("mode |list |message reply archive crm")
	got := MissingActions(BarMail, l, Features{Have: map[Feature]bool{FeatureCRM: true}})
	// Only actions of the default toolbar: the palette's own are in the menus.
	want := []ID{ReplyAll, Forward, Trash, Star}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MissingActions = %v, want %v", got, want)
	}
	// A hidden item is missing from the toolbar, but its feature is too.
	got = MissingActions(BarMail, l, Features{})
	for _, id := range got {
		if id == CRM {
			t.Errorf("crm offered without an eWay-CRM account: %v", got)
		}
	}
	if got := MissingActions(BarBoardList, Default(BarBoardList), Features{}); len(got) != 0 {
		t.Errorf("the board has no More Actions: %v", got)
	}
}

func TestSearchIndex(t *testing.T) {
	if got := SearchIndex(BarMail, Default(BarMail)); got != -1 {
		t.Errorf("SearchIndex with search = %d", got)
	}
	l := ids("mode |list refresh |message reply")
	if got := SearchIndex(BarMail, l); got != len(l) {
		t.Errorf("SearchIndex(mail) = %d, want %d", got, len(l))
	}
	l = ids("mode |main calendarToday")
	if got := SearchIndex(BarCalendar, l); got != len(l) {
		t.Errorf("SearchIndex(calendar) = %d, want %d", got, len(l))
	}
	if got := SearchIndex(BarBoardFlow, Default(BarBoardFlow)); got != -1 {
		t.Errorf("SearchIndex(board) = %d", got)
	}
}

func TestDisplayModeAndKeys(t *testing.T) {
	for in, want := range map[string]DisplayMode{
		"icon": DisplayIcon, "icon-and-label": DisplayIconAndLabel, "label": DisplayLabel, "": DisplayIcon, "LABEL": DisplayIcon,
	} {
		if got := ParseDisplayMode(in); got != want {
			t.Errorf("ParseDisplayMode(%q) = %s", in, got)
		}
	}
	var keys []string
	for _, b := range Bars {
		keys = append(keys, Key(b))
	}
	want := []string{"toolbar-mail", "toolbar-board-list", "toolbar-board-flow", "toolbar-calendar"}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("keys = %v", keys)
	}
}

func TestCatalogsAreConsistent(t *testing.T) {
	for _, bar := range Bars {
		seen := map[ID]bool{}
		secs := map[Section]bool{}
		for _, s := range Sections(bar) {
			secs[s] = true
		}
		for _, it := range Catalog(bar) {
			if seen[it.ID] {
				t.Errorf("%s: %s twice", bar, it.ID)
			}
			seen[it.ID] = true
			if len(it.Sections) == 0 {
				t.Errorf("%s: %s has no section", bar, it.ID)
			}
			for _, s := range it.Sections {
				if !secs[s] {
					t.Errorf("%s: %s in section %s the toolbar lacks", bar, it.ID, s)
				}
			}
			if IsSpace(it.ID) || IsSeparator(it.ID) {
				t.Errorf("%s: %s is not a catalog item", bar, it.ID)
			}
		}
	}
}
